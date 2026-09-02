package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/alpyxn/varyaone/internal/platform/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRetryDelayIsBoundedExponential(t *testing.T) {
	cases := map[int]time.Duration{0: time.Second, 1: time.Second, 2: 2 * time.Second, 5: 16 * time.Second, 20: 128 * time.Second}
	for attempt, want := range cases {
		if got := retryDelay(attempt); got != want {
			t.Fatalf("attempt %d: got %s, want %s", attempt, got, want)
		}
	}
}

func TestEventCarriesCompanyTraceAndTypedPayload(t *testing.T) {
	event := Event{
		CompanyID: "company-1",
		TraceID:   "trace-1",
		Payload:   json.RawMessage(`{"document_id":"document-1"}`),
	}
	if !json.Valid(event.Payload) {
		t.Fatal("event payload is not valid JSON")
	}
	if event.CompanyID != "company-1" || event.TraceID != "trace-1" {
		t.Fatalf("event context was not preserved: %+v", event)
	}
}

func TestNewSeparatesAuditAndExecutableRegistries(t *testing.T) {
	worker := New(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, ok := worker.auditEvents["identity.setup.completed"]; !ok {
		t.Fatal("known audit event is not registered")
	}
	if _, ok := worker.handlers["identity.setup.completed"]; ok {
		t.Fatal("audit event must not be registered as an executable handler")
	}
	if _, ok := worker.auditEvents["unknown.event"]; ok {
		t.Fatal("unknown event is registered as audit")
	}
}

func TestPermanentErrorRecognizesValueAndPointer(t *testing.T) {
	if !isPermanent(PermanentError{Err: errors.New("permanent")}) {
		t.Fatal("permanent error value was not recognized")
	}
	if !isPermanent(&PermanentError{Err: errors.New("permanent")}) {
		t.Fatal("permanent error pointer was not recognized")
	}
	if isPermanent(errors.New("retryable")) {
		t.Fatal("retryable error was classified as permanent")
	}
}

func TestClaimReadsTypedColumnsAndSkipsLockedRows(t *testing.T) {
	pool, ctx := integrationPool(t)
	insertOutboxEvent(t, ctx, pool, "test.claim", `{"value":42}`)
	worker := New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)))

	event, found, err := worker.claim(ctx)
	if err != nil || !found {
		t.Fatalf("first claim: found=%v err=%v", found, err)
	}
	if event.CompanyID != "" || event.TraceID != "" || string(event.Payload) != `{"value": 42}` || event.Attempts != 1 {
		t.Fatalf("claim did not preserve event fields: %+v", event)
	}
	if _, found, err = worker.claim(ctx); err != nil || found {
		t.Fatalf("locked event was claimed twice: found=%v err=%v", found, err)
	}
	if err = worker.ack(ctx, event); err != nil {
		t.Fatal(err)
	}
}

func TestRetryLeavesEventAvailableAndUnknownEventDeadLetters(t *testing.T) {
	pool, ctx := integrationPool(t)
	insertOutboxEvent(t, ctx, pool, "test.retry", `{}`)
	worker := New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	worker.handlers["test.retry"] = func(context.Context, Event) error { return errors.New("temporary") }

	worked, err := worker.runOne(ctx)
	if err != nil || !worked {
		t.Fatalf("retryable event: worked=%v err=%v", worked, err)
	}
	var available time.Time
	var lastError string
	if err = pool.QueryRow(ctx, `SELECT available_at,last_error_class FROM outbox_events WHERE type='test.retry'`).Scan(&available, &lastError); err != nil {
		t.Fatal(err)
	}
	if !available.After(time.Now()) || lastError != "retryable" {
		t.Fatalf("retry state was not persisted: available=%s class=%s", available, lastError)
	}

	insertOutboxEvent(t, ctx, pool, "test.unknown", `{}`)
	worked, err = worker.runOne(ctx)
	if err != nil || !worked {
		t.Fatalf("unknown event: worked=%v err=%v", worked, err)
	}
	var deadLettered bool
	if err = pool.QueryRow(ctx, `SELECT dead_lettered_at IS NOT NULL FROM outbox_events WHERE type='test.unknown'`).Scan(&deadLettered); err != nil {
		t.Fatal(err)
	}
	if !deadLettered {
		t.Fatal("unknown event was not dead-lettered")
	}
}

func TestAuditEventIsAcknowledgedWithoutExecutableHandler(t *testing.T) {
	pool, ctx := integrationPool(t)
	insertOutboxEvent(t, ctx, pool, "identity.setup.completed", `{}`)
	worker := New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)))

	worked, err := worker.runOne(ctx)
	if err != nil || !worked {
		t.Fatalf("audit event: worked=%v err=%v", worked, err)
	}
	var processed, deadLettered bool
	if err = pool.QueryRow(ctx, `SELECT processed_at IS NOT NULL,dead_lettered_at IS NOT NULL FROM outbox_events WHERE type='identity.setup.completed'`).Scan(&processed, &deadLettered); err != nil {
		t.Fatal(err)
	}
	if !processed || deadLettered {
		t.Fatalf("audit event was not acknowledged: processed=%v dead_lettered=%v", processed, deadLettered)
	}
}

func TestStaleWorkerCannotCompleteAfterAnotherWorkerClaims(t *testing.T) {
	pool, ctx := integrationPool(t)
	insertOutboxEvent(t, ctx, pool, "test.claim.takeover", `{}`)
	first := New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	second := New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	first.handlers["test.claim.takeover"] = func(context.Context, Event) error { return nil }
	second.handlers["test.claim.takeover"] = func(context.Context, Event) error { return nil }

	firstEvent, found, err := first.claim(ctx)
	if err != nil || !found {
		t.Fatalf("first claim: found=%v err=%v", found, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE outbox_events SET locked_at=now()-interval '6 minutes' WHERE event_id=$1`, firstEvent.ID); err != nil {
		t.Fatal(err)
	}
	secondEvent, found, err := second.claim(ctx)
	if err != nil || !found {
		t.Fatalf("takeover claim: found=%v err=%v", found, err)
	}
	if err = first.ack(ctx, firstEvent); err != nil {
		t.Fatal(err)
	}
	var processed bool
	if err = pool.QueryRow(ctx, `SELECT processed_at IS NOT NULL FROM outbox_events WHERE event_id=$1`, firstEvent.ID).Scan(&processed); err != nil {
		t.Fatal(err)
	}
	if processed {
		t.Fatal("stale worker completed an event after takeover")
	}
	if err = second.ack(ctx, secondEvent); err != nil {
		t.Fatal(err)
	}
}

func integrationPool(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()
	databaseURL := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	base, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("outbox_test_%d", time.Now().UnixNano())
	if _, err = base.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		base.Close()
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		base.Close()
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		base.Close()
		t.Fatal(err)
	}
	if err = migrations.New(pool).Up(ctx); err != nil {
		pool.Close()
		base.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = base.Exec(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
		base.Close()
	})
	return pool, ctx
}

func insertOutboxEvent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, eventType, payload string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `INSERT INTO outbox_events(event_id,type,schema_version,payload) VALUES(gen_random_uuid(),$1,1,$2::jsonb)`, eventType, payload); err != nil {
		t.Fatal(err)
	}
}
