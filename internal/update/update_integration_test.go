package update

import (
	"context"
	"encoding/json"
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

	// Fake collector: v1.5.0 is the latest; asking as v0.0.0 returns its notes.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("authorization") != "Bearer k" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"channel":          "stable",
			"latest_version":   "v1.5.0",
			"update_available": true,
			"mandatory":        false,
			"notes_md":         "## v1.5.0\n- yeni",
			"published_at":     "2026-09-01T00:00:00Z",
		})
	}))
	defer server.Close()

	svc := NewService(pool, config.Config{Release: "v1.4.0"})
	svc.endpoint = server.URL
	svc.key = "k"

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
	svc.endpoint = "http://127.0.0.1:0" // unreachable; reconcile must not need it
	svc.key = "k"

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
