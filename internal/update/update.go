// Package update tracks whether a newer Varya One release is available and
// drives the apply lifecycle.
//
// The ERP never touches Docker or git itself. It only:
//   - polls the varya-pulse collector for the latest published release,
//   - records the operator's "apply now" / "remind me tomorrow" decision,
//   - exposes a small state machine that a host-side systemd agent reads and
//     reports progress back to.
//
// All state lives in platform_metadata; there is no dedicated table. The phase
// sequence and rollback contract are owned by the host agent
// (deploy/varyaone-update-agent.sh); this package is the coordination point.
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/platform/database"
	"github.com/jackc/pgx/v5"

	"github.com/alpyxn/varyaone/internal/platform/config"
)

const (
	checkInterval = 6 * time.Hour
	snoozeWindow  = 24 * time.Hour
	httpTimeout   = 15 * time.Second
	channel       = "stable"

	latestRelPath = "/release/v1/latest"

	keyLatest      = "update.latest"
	keyState       = "update.state"
	keyTarget      = "update.target"
	keyTargetInfo  = "update.target_info"
	keyProgress    = "update.progress"
	keyResult      = "update.result"
	keyApplied     = "update.applied"
	keySnoozeUntil = "update.snooze_until"
	keyCheckedAt   = "update.checked_at"
)

// Lifecycle states. The happy path is idle -> apply_requested -> in_progress ->
// done, then the UI acks back to idle after the operator reloads.
const (
	StateIdle           = "idle"
	StateApplyRequested = "apply_requested"
	StateInProgress     = "in_progress"
	StateDone           = "done"
	StateFailed         = "failed"
)

var (
	// ErrNotAvailable is returned by RequestApply when there is nothing newer
	// to install.
	ErrNotAvailable = errors.New("no newer release available")
	// ErrBusy is returned by RequestApply when an apply is already queued or
	// running.
	ErrBusy = errors.New("an update is already in progress")
	// ErrTargetMetadata is returned when the exact release queued by the
	// operator no longer has matching artifact metadata. Applying a newer
	// `latest` artifact under an older target name would corrupt update state.
	ErrTargetMetadata = errors.New("queued release metadata is unavailable")
	// ErrMandatory is returned by Snooze when the pending release may not be
	// deferred.
	ErrMandatory = errors.New("this update is mandatory and cannot be snoozed")
)

// Service coordinates update checks and the apply lifecycle.
type Service struct {
	pool     database.Querier
	client   *http.Client
	endpoint string
	key      string
	release  string
	now      func() time.Time
}

// NewService builds a Service from the shared config. The pulse endpoint + key
// double as the release-catalog endpoint; Configured() reports whether checks
// can run.
func NewService(pool database.Querier, cfg config.Config) *Service {
	return &Service{
		pool:     pool,
		client:   &http.Client{Timeout: httpTimeout},
		endpoint: strings.TrimRight(cfg.PulseEndpoint, "/"),
		key:      cfg.PulseIngestKey,
		release:  cfg.Release,
		now:      time.Now,
	}
}

// Configured reports whether the collector endpoint + key are set.
func (s *Service) Configured() bool { return s.endpoint != "" && s.key != "" }

// CurrentVersion is the running release string (VARYAONE_RELEASE).
func (s *Service) CurrentVersion() string { return s.release }

/* ------------------------------------------------------------- payloads --- */

// LatestInfo is the collector's view of the newest release on the channel.
type LatestInfo struct {
	Version     string `json:"version"`
	NotesMD     string `json:"notes_md,omitempty"`
	Mandatory   bool   `json:"mandatory"`
	MinVersion  string `json:"min_version,omitempty"`
	PublishedAt string `json:"published_at,omitempty"`
	// WindowsArtifactURL / WindowsSHA256 point at the prebuilt Windows desktop
	// bundle (zip) for this release. Empty on Docker-only channels; the Windows
	// update agent (varyaone update-apply) downloads and verifies them.
	WindowsArtifactURL string `json:"windows_artifact_url,omitempty"`
	WindowsSHA256      string `json:"windows_sha256,omitempty"`
	// PGMajor is informational metadata for the UI/catalog. The installers use
	// the actual bundled/running PostgreSQL binaries as the authoritative major
	// version when deciding whether dump-and-restore is required.
	PGMajor int `json:"pg_major,omitempty"`
}

// Progress is the agent's current phase within an apply.
type Progress struct {
	Phase     string    `json:"phase"`
	Message   string    `json:"message,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Result is the outcome of the last completed apply attempt.
type Result struct {
	OK          bool      `json:"ok"`
	Error       string    `json:"error,omitempty"`
	RolledBack  bool      `json:"rolled_back"`
	FromVersion string    `json:"from_version,omitempty"`
	ToVersion   string    `json:"to_version,omitempty"`
	LogTail     string    `json:"log_tail,omitempty"`
	FinishedAt  time.Time `json:"finished_at"`
}

// Applied is what the UI shows once an update succeeds: the version now running
// and its release notes (empty notes => the UI shows none).
type Applied struct {
	Version string    `json:"version"`
	NotesMD string    `json:"notes_md,omitempty"`
	At      time.Time `json:"at"`
}

// Status is the full picture returned to the settings UI.
type Status struct {
	CurrentVersion  string      `json:"current_version"`
	Channel         string      `json:"channel"`
	State           string      `json:"state"`
	CheckedAt       *time.Time  `json:"checked_at,omitempty"`
	Latest          *LatestInfo `json:"latest,omitempty"`
	UpdateAvailable bool        `json:"update_available"`
	Mandatory       bool        `json:"mandatory"`
	Snoozed         bool        `json:"snoozed"`
	SnoozeUntil     *time.Time  `json:"snooze_until,omitempty"`
	Progress        *Progress   `json:"progress,omitempty"`
	Result          *Result     `json:"result,omitempty"`
	Applied         *Applied    `json:"applied,omitempty"`
}

/* -------------------------------------------------------------- reading --- */

// Status assembles the current update picture. It never contacts the network.
func (s *Service) Status(ctx context.Context) (Status, error) {
	meta, err := s.readAll(ctx)
	if err != nil {
		return Status{}, err
	}

	st := Status{
		CurrentVersion: s.release,
		Channel:        channel,
		State:          firstNonEmpty(meta.text(keyState), StateIdle),
	}
	if t := meta.time(keyCheckedAt); t != nil {
		st.CheckedAt = t
	}
	if latest := meta.latest(); latest != nil {
		st.Latest = latest
		st.UpdateAvailable = compareVersions(s.release, latest.Version) < 0
		if st.UpdateAvailable {
			st.Mandatory = latest.Mandatory ||
				(latest.MinVersion != "" && compareVersions(s.release, latest.MinVersion) < 0)
		}
	}
	if until := meta.time(keySnoozeUntil); until != nil && until.After(s.now()) && !st.Mandatory {
		st.Snoozed = true
		st.SnoozeUntil = until
	}
	if p := meta.progress(); p != nil && st.State == StateInProgress {
		st.Progress = p
	}
	if r := meta.result(); r != nil && (st.State == StateDone || st.State == StateFailed) {
		st.Result = r
	}
	if a := meta.applied(); a != nil && a.Version == s.release {
		st.Applied = a
	}
	return st, nil
}

/* --------------------------------------------------------- operator ops --- */

// RequestApply queues an apply for the newest release. The host agent picks it
// up on its next poll.
func (s *Service) RequestApply(ctx context.Context) error {
	st, err := s.Status(ctx)
	if err != nil {
		return err
	}
	if !st.UpdateAvailable || st.Latest == nil {
		return ErrNotAvailable
	}
	if st.State == StateApplyRequested || st.State == StateInProgress {
		return ErrBusy
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	writes := []struct{ k, v string }{
		{keyState, quote(StateApplyRequested)},
		{keyTarget, quote(st.Latest.Version)},
		{keyTargetInfo, mustJSON(st.Latest)},
		{keyProgress, "null"},
		{keyResult, "null"},
		{keyApplied, "null"},
	}
	for _, w := range writes {
		if err := setRawTx(ctx, tx, w.k, w.v); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// Snooze defers the reminder for 24h. Mandatory updates cannot be snoozed.
func (s *Service) Snooze(ctx context.Context) error {
	st, err := s.Status(ctx)
	if err != nil {
		return err
	}
	if st.Mandatory {
		return ErrMandatory
	}
	return s.setRaw(ctx, keySnoozeUntil, quote(s.now().UTC().Add(snoozeWindow).Format(time.RFC3339)))
}

// Ack clears a finished (done/failed) apply back to idle. The UI calls it once
// the operator has seen the outcome / reloaded.
func (s *Service) Ack(ctx context.Context) error {
	st, err := s.Status(ctx)
	if err != nil {
		return err
	}
	if st.State != StateDone && st.State != StateFailed {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	for _, k := range []string{keyState, keyTarget, keyTargetInfo, keyProgress, keyResult, keyApplied} {
		v := "null"
		if k == keyState {
			v = quote(StateIdle)
		}
		if err := setRawTx(ctx, tx, k, v); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// TargetRelease returns the immutable release metadata captured when the
// operator queued target. For requests created by an older binary, it safely
// falls back to update.latest only when the versions still match exactly.
func (s *Service) TargetRelease(ctx context.Context, target string) (*LatestInfo, error) {
	meta, err := s.readAll(ctx)
	if err != nil {
		return nil, err
	}
	return targetRelease(meta, target)
}

func targetRelease(meta metaMap, target string) (*LatestInfo, error) {
	if info := meta.targetInfo(); info != nil && info.Version == target {
		return info, nil
	}
	if latest := meta.latest(); latest != nil && latest.Version == target {
		return latest, nil
	}
	return nil, fmt.Errorf("%w: %s", ErrTargetMetadata, target)
}

/* ------------------------------------------------------------ agent ops --- */

// NextAction is polled by the host agent. When an apply is queued it atomically
// flips the state to in_progress and hands out the target version; a repeated
// call while in_progress returns the same target so a restarted agent resumes.
type NextAction struct {
	Action        string `json:"action"` // "apply" | "none"
	TargetVersion string `json:"target_version,omitempty"`
	FromVersion   string `json:"from_version,omitempty"`
}

func (s *Service) NextAction(ctx context.Context) (NextAction, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return NextAction{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var state, target string
	err = tx.QueryRow(ctx,
		`SELECT
		   COALESCE((SELECT value #>> '{}' FROM platform_metadata WHERE key = $1), $3),
		   COALESCE((SELECT value #>> '{}' FROM platform_metadata WHERE key = $2), '')`,
		keyState, keyTarget, StateIdle).Scan(&state, &target)
	if err != nil {
		return NextAction{}, err
	}

	switch state {
	case StateApplyRequested:
		if target == "" {
			return NextAction{Action: "none"}, nil
		}
		if err := setRawTx(ctx, tx, keyState, quote(StateInProgress)); err != nil {
			return NextAction{}, err
		}
		if err := setRawTx(ctx, tx, keyProgress, mustJSON(Progress{
			Phase: "queued", Message: "Güncelleme başlatılıyor", UpdatedAt: s.now().UTC(),
		})); err != nil {
			return NextAction{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return NextAction{}, err
		}
		return NextAction{Action: "apply", TargetVersion: target, FromVersion: s.release}, nil
	case StateInProgress:
		if err := tx.Commit(ctx); err != nil {
			return NextAction{}, err
		}
		return NextAction{Action: "apply", TargetVersion: target, FromVersion: s.release}, nil
	default:
		return NextAction{Action: "none"}, nil
	}
}

// RecordProgress stores the agent's current phase. It is a no-op unless an
// apply is running.
func (s *Service) RecordProgress(ctx context.Context, phase, message string) error {
	state := s.stateOrIdle(ctx)
	if state != StateInProgress {
		return nil
	}
	return s.setRaw(ctx, keyProgress, mustJSON(Progress{
		Phase:     truncate(phase, 40),
		Message:   truncate(message, 500),
		UpdatedAt: s.now().UTC(),
	}))
}

// ResultInput is what the agent posts when an apply finishes (either way).
type ResultInput struct {
	OK          bool
	Error       string
	RolledBack  bool
	FromVersion string
	ToVersion   string
	LogTail     string
}

// RecordResult closes out an apply. On success it also fetches the release
// notes for the new version so the UI can show them after the reload.
func (s *Service) RecordResult(ctx context.Context, in ResultInput) error {
	res := Result{
		OK:          in.OK,
		Error:       truncate(in.Error, 2000),
		RolledBack:  in.RolledBack,
		FromVersion: truncate(in.FromVersion, 60),
		ToVersion:   truncate(in.ToVersion, 60),
		LogTail:     truncate(in.LogTail, 12000),
		FinishedAt:  s.now().UTC(),
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	state := StateFailed
	if in.OK {
		state = StateDone
	}
	if err := setRawTx(ctx, tx, keyState, quote(state)); err != nil {
		return err
	}
	if err := setRawTx(ctx, tx, keyResult, mustJSON(res)); err != nil {
		return err
	}
	if err := setRawTx(ctx, tx, keyProgress, "null"); err != nil {
		return err
	}

	if in.OK {
		applied := Applied{Version: firstNonEmpty(in.ToVersion, s.release), At: s.now().UTC()}
		if notes := s.fetchNotes(ctx, applied.Version); notes != "" {
			applied.NotesMD = notes
		}
		if err := setRawTx(ctx, tx, keyApplied, mustJSON(applied)); err != nil {
			return err
		}
		if err := setRawTx(ctx, tx, keyTarget, "null"); err != nil {
			return err
		}
		if err := setRawTx(ctx, tx, keyTargetInfo, "null"); err != nil {
			return err
		}
		if err := setRawTx(ctx, tx, keySnoozeUntil, "null"); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

/* --------------------------------------------------------------- worker --- */

// CheckDue is the worker entry point. It fetches the latest release at most
// once per checkInterval, and reconciles a stuck state left by a crashed agent.
func (s *Service) CheckDue(ctx context.Context) error {
	if !s.Configured() {
		return nil
	}
	if err := s.reconcile(ctx); err != nil {
		return err
	}

	meta, err := s.readAll(ctx)
	if err != nil {
		return err
	}
	if last := meta.time(keyCheckedAt); last != nil && s.now().Sub(*last) < checkInterval {
		return nil
	}

	latest, err := s.fetchLatest(ctx)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := setRawTx(ctx, tx, keyCheckedAt, quote(s.now().UTC().Format(time.RFC3339))); err != nil {
		return err
	}
	if latest != nil {
		if err := setRawTx(ctx, tx, keyLatest, mustJSON(latest)); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// reconcile recovers from an agent that died mid-apply: if the process is now
// running the target version the apply clearly succeeded, so mark it done.
func (s *Service) reconcile(ctx context.Context) error {
	meta, err := s.readAll(ctx)
	if err != nil {
		return err
	}
	state := firstNonEmpty(meta.text(keyState), StateIdle)
	target := meta.text(keyTarget)
	if state == StateInProgress && target != "" && compareVersions(s.release, target) >= 0 {
		return s.RecordResult(ctx, ResultInput{
			OK: true, ToVersion: s.release, FromVersion: target,
			LogTail: "agent bağlantısı koptu; süreç hedef sürümde çalışıyor — başarı varsayıldı",
		})
	}
	return nil
}

func (s *Service) fetchLatest(ctx context.Context) (*LatestInfo, error) {
	u := fmt.Sprintf("%s%s?channel=%s&current=%s", s.endpoint, latestRelPath, channel, s.release)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("authorization", "Bearer "+s.key)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("release catalog returned %s", resp.Status)
	}
	var body struct {
		LatestVersion      string `json:"latest_version"`
		NotesMD            string `json:"notes_md"`
		Mandatory          bool   `json:"mandatory"`
		MinVersion         string `json:"min_version"`
		PublishedAt        string `json:"published_at"`
		WindowsArtifactURL string `json:"windows_artifact_url"`
		WindowsSHA256      string `json:"windows_sha256"`
		PGMajor            int    `json:"pg_major"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	if body.LatestVersion == "" {
		return nil, nil
	}
	return &LatestInfo{
		Version:            body.LatestVersion,
		NotesMD:            body.NotesMD,
		Mandatory:          body.Mandatory,
		MinVersion:         body.MinVersion,
		PublishedAt:        body.PublishedAt,
		WindowsArtifactURL: body.WindowsArtifactURL,
		WindowsSHA256:      body.WindowsSHA256,
		PGMajor:            body.PGMajor,
	}, nil
}

func (s *Service) fetchNotes(ctx context.Context, version string) string {
	// Ask the catalog as if we were still on the previous version so it returns
	// this version's notes.
	u := fmt.Sprintf("%s%s?channel=%s&current=%s", s.endpoint, latestRelPath, channel, "v0.0.0")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("authorization", "Bearer "+s.key)
	resp, err := s.client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var body struct {
		LatestVersion string `json:"latest_version"`
		NotesMD       string `json:"notes_md"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return ""
	}
	if body.LatestVersion == version {
		return body.NotesMD
	}
	return ""
}

/* -------------------------------------------------- platform_metadata --- */

type metaMap map[string]string

// text returns a scalar string value. Values come back from `value::text`, so a
// JSON string arrives quoted ("idle") — strip the quotes. Object/array values
// (handled by the typed helpers below) are read raw and unaffected.
func (m metaMap) text(key string) string {
	raw := strings.TrimSpace(m[key])
	if raw == "null" {
		return ""
	}
	return strings.Trim(raw, `"`)
}

func (m metaMap) time(key string) *time.Time {
	raw := strings.Trim(strings.TrimSpace(m[key]), `"`)
	if raw == "" || raw == "null" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil
	}
	return &t
}

func (m metaMap) latest() *LatestInfo     { return jsonInto[LatestInfo](m[keyLatest]) }
func (m metaMap) targetInfo() *LatestInfo { return jsonInto[LatestInfo](m[keyTargetInfo]) }
func (m metaMap) progress() *Progress     { return jsonInto[Progress](m[keyProgress]) }
func (m metaMap) result() *Result         { return jsonInto[Result](m[keyResult]) }
func (m metaMap) applied() *Applied       { return jsonInto[Applied](m[keyApplied]) }

func jsonInto[T any](raw string) *T {
	if raw == "" || raw == "null" {
		return nil
	}
	var v T
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil
	}
	return &v
}

func (s *Service) readAll(ctx context.Context) (metaMap, error) {
	keys := []string{
		keyLatest, keyState, keyTarget, keyTargetInfo, keyProgress, keyResult,
		keyApplied, keySnoozeUntil, keyCheckedAt,
	}
	rows, err := s.pool.Query(ctx,
		`SELECT key, value::text FROM platform_metadata WHERE key = ANY($1)`, keys)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := metaMap{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

func (s *Service) stateOrIdle(ctx context.Context) string {
	var v string
	err := s.pool.QueryRow(ctx,
		`SELECT value #>> '{}' FROM platform_metadata WHERE key = $1`, keyState).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) || v == "" {
		return StateIdle
	}
	return v
}

func (s *Service) setRaw(ctx context.Context, key, jsonValue string) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO platform_metadata (key, value, updated_at)
		 VALUES ($1, $2::jsonb, now())
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`,
		key, jsonValue)
	return err
}

func setRawTx(ctx context.Context, tx pgx.Tx, key, jsonValue string) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO platform_metadata (key, value, updated_at)
		 VALUES ($1, $2::jsonb, now())
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`,
		key, jsonValue)
	return err
}

/* --------------------------------------------------------------- utils --- */

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
