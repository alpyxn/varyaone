package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alpyxn/varyaone/internal/platform/config"
	"github.com/alpyxn/varyaone/internal/platform/migrations"
)

// TestUpdateLifecycleIntegration walks the full state machine: check -> operator
// requests apply -> agent picks it up -> progress -> success, plus snooze and
// the crashed-agent reconcile path.
func TestUpdateLifecycleIntegration(t *testing.T) {
	databaseURL := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	pool := updateTestPool(t, ctx, databaseURL)
	if err := migrations.New(pool).Up(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Fake catalog: v1.5.0 is the latest on the stable channel.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"schema_version": 1,
			"channels": map[string]any{
				"stable": map[string]any{
					"version":      "v1.5.0",
					"mandatory":    false,
					"notes_md":     "## v1.5.0\n- yeni",
					"published_at": "2026-09-01T00:00:00Z",
				},
			},
		})
	}))
	defer server.Close()

	svc := NewService(pool, config.Config{Release: "v1.4.0"})
	svc.catalogURLs = []string{server.URL}

	// 1. Check populates latest.
	if err := svc.CheckDue(ctx); err != nil {
		t.Fatalf("CheckDue: %v", err)
	}
	st, err := svc.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.UpdateAvailable || st.Latest == nil || st.Latest.Version != "v1.5.0" {
		t.Fatalf("expected v1.5.0 available, got %+v", st)
	}

	// 2. Second check within the interval does not re-hit the network — cheap to
	// assert via no error and unchanged state.
	if err := svc.CheckDue(ctx); err != nil {
		t.Fatalf("CheckDue (2): %v", err)
	}

	// 3. Snooze hides the reminder.
	if err := svc.Snooze(ctx); err != nil {
		t.Fatalf("Snooze: %v", err)
	}
	if st, _ = svc.Status(ctx); !st.Snoozed {
		t.Fatalf("expected snoozed")
	}

	// 4. Operator requests apply.
	if err := svc.RequestApply(ctx); err != nil {
		t.Fatalf("RequestApply: %v", err)
	}
	targetInfo, err := svc.TargetRelease(ctx, "v1.5.0")
	if err != nil || targetInfo.Version != "v1.5.0" || targetInfo.NotesMD == "" {
		t.Fatalf("TargetRelease = %+v err=%v", targetInfo, err)
	}
	// A later catalog refresh must not retarget an already-approved apply.
	newer := &LatestInfo{Version: "v1.6.0", WindowsArtifactURL: "https://example.invalid/v1.6.zip"}
	if err := svc.setRaw(ctx, keyLatest, mustJSON(newer)); err != nil {
		t.Fatal(err)
	}
	if targetInfo, err = svc.TargetRelease(ctx, "v1.5.0"); err != nil || targetInfo.Version != "v1.5.0" {
		t.Fatalf("queued target drifted after latest refresh: %+v err=%v", targetInfo, err)
	}
	if err := svc.setRaw(ctx, keyLatest, mustJSON(st.Latest)); err != nil {
		t.Fatal(err)
	}
	if err := svc.RequestApply(ctx); err != ErrBusy {
		t.Fatalf("second RequestApply = %v, want ErrBusy", err)
	}

	// 5. Agent picks it up -> in_progress.
	action, err := svc.NextAction(ctx)
	if err != nil || action.Action != "apply" || action.TargetVersion != "v1.5.0" {
		t.Fatalf("NextAction = %+v err=%v", action, err)
	}
	if action2, _ := svc.NextAction(ctx); action2.Action != "apply" {
		t.Fatalf("resumed NextAction = %+v, want apply", action2)
	}
	if err := svc.RecordProgress(ctx, "build", "derleniyor"); err != nil {
		t.Fatalf("RecordProgress: %v", err)
	}
	if st, _ = svc.Status(ctx); st.Progress == nil || st.Progress.Phase != "build" {
		t.Fatalf("expected build progress, got %+v", st.Progress)
	}

	// 6. Agent reports success -> done, applied notes fetched, snooze cleared.
	if err := svc.RecordResult(ctx, ResultInput{OK: true, FromVersion: "v1.4.0", ToVersion: "v1.5.0"}); err != nil {
		t.Fatalf("RecordResult: %v", err)
	}
	svc.release = "v1.5.0" // process restarted on the new version
	st, _ = svc.Status(ctx)
	if st.State != StateDone {
		t.Fatalf("state = %q, want done", st.State)
	}
	if st.Applied == nil || st.Applied.Version != "v1.5.0" || st.Applied.NotesMD == "" {
		t.Fatalf("expected applied notes, got %+v", st.Applied)
	}
	if st.UpdateAvailable {
		t.Fatalf("still reports update available after applying")
	}

	// 7. UI acks -> back to idle.
	if err := svc.Ack(ctx); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	if st, _ = svc.Status(ctx); st.State != StateIdle || st.Applied != nil {
		t.Fatalf("after ack: %+v", st)
	}
}

func TestReconcileRecoversCrashedAgent(t *testing.T) {
	databaseURL := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	pool := updateTestPool(t, ctx, databaseURL)
	if err := migrations.New(pool).Up(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	svc := NewService(pool, config.Config{Release: "v2.0.0"})
	svc.catalogURLs = []string{"http://127.0.0.1:0"} // unreachable; reconcile must not need it

	// Simulate an apply that got as far as in_progress toward v2.0.0 and then
	// the agent died — but the process is now already on v2.0.0.
	if err := svc.setRaw(ctx, keyState, quote(StateInProgress)); err != nil {
		t.Fatal(err)
	}
	if err := svc.setRaw(ctx, keyTarget, quote("v2.0.0")); err != nil {
		t.Fatal(err)
	}
	if err := svc.reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if st, _ := svc.Status(ctx); st.State != StateDone {
		t.Fatalf("state = %q, want done", st.State)
	}
}

// TestCheckDueBackoffOnRepeatedFailure verifies H3: a catalog that keeps
// failing backs off (rather than being re-hit on every tick) and never
// disturbs a previously known-good update.latest.
func TestCheckDueBackoffOnRepeatedFailure(t *testing.T) {
	databaseURL := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	pool := updateTestPool(t, ctx, databaseURL)
	if err := migrations.New(pool).Up(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"schema_version": 1,
			"channels": map[string]any{
				"stable": map[string]any{"version": "v1.0.0"},
			},
		})
	}))
	defer good.Close()

	svc := NewService(pool, config.Config{Release: "v0.9.0"})
	svc.catalogURLs = []string{good.URL}
	clock := time.Now()
	svc.now = func() time.Time { return clock }

	// 1. A good check populates latest and clears the interval.
	if err := svc.CheckDue(ctx); err != nil {
		t.Fatalf("CheckDue: %v", err)
	}
	st, err := svc.Status(ctx)
	if err != nil || st.Latest == nil || st.Latest.Version != "v1.0.0" {
		t.Fatalf("Status after good check = %+v err=%v", st, err)
	}

	// 2. Point at an always-failing source; the next N due checks should
	// increase the check_failures counter and never overwrite update.latest.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()
	svc.catalogURLs = []string{bad.URL}

	for i := 0; i < 3; i++ {
		clock = clock.Add(maxCheckInterval + time.Minute) // always past due regardless of backoff so far
		if err := svc.CheckDue(ctx); err == nil {
			t.Fatalf("CheckDue #%d: expected error from the failing catalog", i)
		}
	}

	meta, err := svc.readAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if failures := meta.int(keyCheckFailures); failures != 3 {
		t.Fatalf("check_failures = %d, want 3", failures)
	}
	st, err = svc.Status(ctx)
	if err != nil || st.Latest == nil || st.Latest.Version != "v1.0.0" {
		t.Fatalf("update.latest was disturbed by a failing catalog: %+v err=%v", st, err)
	}

	// 3. A due check right after a failure, before the backed-off interval
	// elapses, must be a no-op (no new network call — the failing source would
	// error if hit, so a clean nil return proves the skip).
	clock = clock.Add(time.Second)
	if err := svc.CheckDue(ctx); err != nil {
		t.Fatalf("CheckDue should have been skipped by backoff, got: %v", err)
	}
	meta, _ = svc.readAll(ctx)
	if failures := meta.int(keyCheckFailures); failures != 3 {
		t.Fatalf("check_failures changed during a backed-off tick: %d", failures)
	}

	// 4. Recovery resets the failure counter.
	svc.catalogURLs = []string{good.URL}
	clock = clock.Add(maxCheckInterval + time.Minute)
	if err := svc.CheckDue(ctx); err != nil {
		t.Fatalf("CheckDue on recovery: %v", err)
	}
	meta, _ = svc.readAll(ctx)
	if failures := meta.int(keyCheckFailures); failures != 0 {
		t.Fatalf("check_failures = %d after recovery, want 0", failures)
	}
}

// TestCheckDueYankedCancelsQueuedApply verifies H4: withdrawing a queued (but
// not yet started) release via the catalog's "yanked" list cancels it, while
// an apply already in_progress is left untouched.
func TestCheckDueYankedCancelsQueuedApply(t *testing.T) {
	databaseURL := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	pool := updateTestPool(t, ctx, databaseURL)
	if err := migrations.New(pool).Up(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	catalogBody := `{"schema_version": 1, "channels": {"stable": {"version": "v1.5.0"}}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(catalogBody))
	}))
	defer srv.Close()

	svc := NewService(pool, config.Config{Release: "v1.4.0"})
	svc.catalogURLs = []string{srv.URL}

	if err := svc.CheckDue(ctx); err != nil {
		t.Fatalf("CheckDue: %v", err)
	}
	if err := svc.RequestApply(ctx); err != nil {
		t.Fatalf("RequestApply: %v", err)
	}
	if st, _ := svc.Status(ctx); st.State != StateApplyRequested {
		t.Fatalf("state = %q, want apply_requested", st.State)
	}

	// Withdraw v1.5.0.
	catalogBody = `{"schema_version": 1, "channels": {"stable": {"version": "v1.5.0"}}, "yanked": ["v1.5.0"]}`
	svc.setRaw(ctx, keyCheckedAt, "null") // force due again
	if err := svc.CheckDue(ctx); err != nil {
		t.Fatalf("CheckDue after yank: %v", err)
	}
	st, err := svc.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.State != StateIdle {
		t.Fatalf("state = %q, want idle after the queued release was yanked", st.State)
	}

	// An apply already in_progress must not be touched by a yank.
	if err := svc.setRaw(ctx, keyState, quote(StateInProgress)); err != nil {
		t.Fatal(err)
	}
	if err := svc.setRaw(ctx, keyTarget, quote("v1.5.0")); err != nil {
		t.Fatal(err)
	}
	svc.setRaw(ctx, keyCheckedAt, "null")
	if err := svc.CheckDue(ctx); err != nil {
		t.Fatalf("CheckDue with in_progress apply: %v", err)
	}
	meta, err := svc.readAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if meta.text(keyState) != StateInProgress {
		t.Fatalf("in_progress apply was disturbed by a yank: state = %q", meta.text(keyState))
	}
}

func updateTestPool(t *testing.T, ctx context.Context, databaseURL string) *pgxpool.Pool {
	t.Helper()
	base, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("varya_update_test_%d", time.Now().UnixNano())
	if _, err := base.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		base.Close()
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		base.Close()
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		base.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = base.Exec(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
		base.Close()
	})
	return pool
}

// TestCheckNowBypassesTheSchedule is the regression test for the settings
// screen's "Kontrol et" button. The button used to read stored status only, so
// a release published minutes ago stayed invisible for up to six hours and the
// screen looked like it had checked and found nothing.
func TestCheckNowBypassesTheSchedule(t *testing.T) {
	databaseURL := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	pool := updateTestPool(t, ctx, databaseURL)
	if err := migrations.New(pool).Up(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	published := "v1.4.0"
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"schema_version": 1,
			"channels": map[string]any{
				"stable": map[string]any{"version": published, "published_at": "2026-09-01T00:00:00Z"},
			},
		})
	}))
	defer server.Close()

	svc := NewService(pool, config.Config{Release: "v1.4.0"})
	svc.catalogURLs = []string{server.URL}
	now := time.Date(2026, 9, 4, 9, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }

	if err := svc.CheckDue(ctx); err != nil {
		t.Fatalf("initial CheckDue: %v", err)
	}

	// A release appears minutes later. The scheduled check is not due yet, so
	// the worker must stay quiet...
	published = "v1.5.0"
	now = now.Add(5 * time.Minute)
	if err := svc.CheckDue(ctx); err != nil {
		t.Fatalf("CheckDue inside the interval: %v", err)
	}
	st, err := svc.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.UpdateAvailable {
		t.Fatal("the scheduled check ran inside its interval")
	}

	// ...but the operator pressing the button must see it immediately.
	if err = svc.CheckNow(ctx); err != nil {
		t.Fatalf("CheckNow: %v", err)
	}
	if st, err = svc.Status(ctx); err != nil {
		t.Fatal(err)
	}
	if !st.UpdateAvailable || st.Latest == nil || st.Latest.Version != "v1.5.0" {
		t.Fatalf("after CheckNow status = %+v, want v1.5.0 available", st)
	}

	// Pressing it repeatedly must not hammer the catalog host.
	before := requests
	if err = svc.CheckNow(ctx); !errors.Is(err, ErrCheckTooSoon) {
		t.Fatalf("immediate second CheckNow returned %v, want ErrCheckTooSoon", err)
	}
	if requests != before {
		t.Fatalf("the rate-limited check still contacted the catalog (%d calls)", requests-before)
	}

	// After the cooldown it works again.
	now = now.Add(2 * time.Minute)
	if err = svc.CheckNow(ctx); err != nil {
		t.Fatalf("CheckNow after the cooldown: %v", err)
	}
}
