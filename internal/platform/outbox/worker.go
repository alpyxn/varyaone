package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const maxAttempts = 10
const supportedSchemaVersion = 1

type Event struct {
	ID            string
	Type          string
	SchemaVersion int
	CompanyID     string
	TraceID       string
	Payload       json.RawMessage
	Attempts      int
	lockToken     time.Time
}

type Handler func(context.Context, Event) error

type PermanentError struct{ Err error }

func (e PermanentError) Error() string { return e.Err.Error() }
func (e PermanentError) Unwrap() error { return e.Err }

type Worker struct {
	pool        *pgxpool.Pool
	logger      *slog.Logger
	handlers    map[string]Handler
	auditEvents map[string]struct{}
}

func New(pool *pgxpool.Pool, logger *slog.Logger) *Worker {
	return &Worker{
		pool:        pool,
		logger:      logger,
		handlers:    map[string]Handler{},
		auditEvents: knownAuditEvents(),
	}
}

func (w *Worker) Run(ctx context.Context) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		worked, err := w.runOne(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			w.logger.Error("outbox cycle failed", "error_class", "database")
		}
		if worked {
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (w *Worker) runOne(ctx context.Context) (bool, error) {
	event, found, err := w.claim(ctx)
	if err != nil || !found {
		return false, err
	}
	if event.SchemaVersion != supportedSchemaVersion {
		return true, w.deadLetter(ctx, event, "unknown_event")
	}
	if handler, known := w.handlers[event.Type]; known {
		err = handler(ctx, event)
	} else if _, known := w.auditEvents[event.Type]; known {
		return true, w.ack(ctx, event)
	} else {
		return true, w.deadLetter(ctx, event, "unknown_event")
	}
	if err == nil {
		return true, w.ack(ctx, event)
	}
	if isPermanent(err) || event.Attempts >= maxAttempts {
		return true, w.deadLetter(ctx, event, "permanent")
	}
	_, retryErr := w.pool.Exec(ctx, `UPDATE outbox_events SET locked_at=NULL,available_at=now()+($2 * interval '1 second'),last_error_class='retryable' WHERE event_id=$1 AND processed_at IS NULL AND dead_lettered_at IS NULL AND locked_at=$3`, event.ID, retryDelay(event.Attempts).Seconds(), event.lockToken)
	return true, retryErr
}

func (w *Worker) claim(ctx context.Context) (Event, bool, error) {
	tx, err := w.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Event{}, false, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var event Event
	var payload []byte
	err = tx.QueryRow(ctx, `
		-- company_id/trace_id are nullable in the existing schema; keep old rows readable.
		-- If a target database predates these columns, the parent must add its migration first.
		SELECT event_id,type,schema_version,COALESCE(company_id::text,''),COALESCE(trace_id,''),payload,attempts+1
		FROM outbox_events
		WHERE processed_at IS NULL AND dead_lettered_at IS NULL AND available_at<=now()
		  AND (locked_at IS NULL OR locked_at<now()-interval '5 minutes')
		ORDER BY occurred_at,event_id
		FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&event.ID, &event.Type, &event.SchemaVersion, &event.CompanyID, &event.TraceID, &payload, &event.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return Event{}, false, nil
	}
	if err != nil {
		return Event{}, false, fmt.Errorf("claim outbox event: %w", err)
	}
	event.Payload = json.RawMessage(payload)
	if err = tx.QueryRow(ctx, `UPDATE outbox_events SET locked_at=clock_timestamp(),attempts=$2 WHERE event_id=$1 AND processed_at IS NULL AND dead_lettered_at IS NULL RETURNING locked_at`, event.ID, event.Attempts).Scan(&event.lockToken); err != nil {
		return Event{}, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Event{}, false, err
	}
	return event, true, nil
}

func (w *Worker) ack(ctx context.Context, event Event) error {
	_, err := w.pool.Exec(ctx, `UPDATE outbox_events SET processed_at=now(),locked_at=NULL,last_error_class=NULL WHERE event_id=$1 AND processed_at IS NULL AND dead_lettered_at IS NULL AND locked_at=$2`, event.ID, event.lockToken)
	return err
}

func (w *Worker) deadLetter(ctx context.Context, event Event, class string) error {
	_, err := w.pool.Exec(ctx, `UPDATE outbox_events SET dead_lettered_at=now(),locked_at=NULL,last_error_class=$2 WHERE event_id=$1 AND processed_at IS NULL AND dead_lettered_at IS NULL AND locked_at=$3`, event.ID, class, event.lockToken)
	return err
}

func isPermanent(err error) bool {
	var permanent PermanentError
	var permanentPtr *PermanentError
	return errors.As(err, &permanent) || errors.As(err, &permanentPtr)
}

func knownAuditEvents() map[string]struct{} {
	return map[string]struct{}{
		"identity.setup.completed":                   {},
		"document.created":                           {},
		"document.updated":                           {},
		"document.posted":                            {},
		"document.reversed":                          {},
		"finance.account.created":                    {},
		"finance.manual_entry.posted":                {},
		"finance.payment.allocated":                  {},
		"finance.payment.posted":                     {},
		"finance.payment.reversed":                   {},
		"finance.period.locked":                      {},
		"finance.transfer.posted":                    {},
		"party.created":                              {},
		"party.activated":                            {},
		"party.custom_field.created":                 {},
		"party.deactivated":                          {},
		"party.group.created":                        {},
		"party.group.deactivated":                    {},
		"party.group.activated":                      {},
		"party.group.updated":                        {},
		"party.ledger.posted":                        {},
		"party.payment_term.created":                 {},
		"party.updated":                              {},
		"pricing.currency.created":                   {},
		"pricing.currency.updated":                   {},
		"pricing.entry.created":                      {},
		"pricing.entry.updated":                      {},
		"pricing.price_list.activated":               {},
		"pricing.price_list.created":                 {},
		"pricing.price_list.deactivated":             {},
		"pricing.price_list.updated":                 {},
		"product.attachment.archived":                {},
		"product.attachment.uploaded":                {},
		"product.brand.created":                      {},
		"product.category.created":                   {},
		"product.code_sequence.updated":              {},
		"product.created":                            {},
		"product.deactivated":                        {},
		"product.image.presentation.updated":         {},
		"product.image.uploaded":                     {},
		"product.reference.active":                   {},
		"product.updated":                            {},
		"product.variant.created":                    {},
		"product.variant.deactivated":                {},
		"product.variant.updated":                    {},
		"product.variant_barcodes.replaced":          {},
		"product.variant_config.updated":             {},
		"product.variant_definition.active_changed":  {},
		"product.variant_definition.created":         {},
		"product.variant_option.active_changed":      {},
		"product.variant_option.created":             {},
		"product.variant_option.updated":             {},
		"product.variants.generated":                 {},
		"tax.definition.created":                     {},
		"tax.definition.deactivated":                 {},
		"tax.definition.updated":                     {},
		"tax.rate.created":                           {},
		"purchase.order.created":                     {},
		"purchase.order.confirmed":                   {},
		"purchase.receipt.posted":                    {},
		"purchase.invoice.posted":                    {},
		"purchase.return.posted":                     {},
		"purchase.landed_cost.created":               {},
		"purchase.landed_cost.posted":                {},
		"commercial.sales_quote.created":             {},
		"commercial.sales_quote.updated":             {},
		"commercial.sales_quote.send":                {},
		"commercial.sales_quote.accept":              {},
		"commercial.sales_quote.reject":              {},
		"commercial.sales_quote.cancelled":           {},
		"commercial.sales_order.created":             {},
		"commercial.sales_order.updated":             {},
		"commercial.sales_order.cancelled":           {},
		"commercial.sales_dispatch.created":          {},
		"commercial.sales_dispatch.updated":          {},
		"commercial.sales_dispatch.cancelled":        {},
		"commercial.sales_invoice.created":           {},
		"commercial.sales_invoice.updated":           {},
		"commercial.sales_invoice.cancelled":         {},
		"commercial.sales_return.created":            {},
		"commercial.sales_return.updated":            {},
		"commercial.sales_return.cancelled":          {},
		"sales.order.confirmed":                      {},
		"sales.sales_dispatch.posted":                {},
		"sales.sales_invoice.posted":                 {},
		"sales.sales_return.posted":                  {},
		"commercial_document.create.completed":       {},
		"commercial.sales_quote.send.completed":      {},
		"commercial.sales_quote.accept.completed":    {},
		"commercial.sales_quote.reject.completed":    {},
		"commercial.sales_order.confirm.completed":   {},
		"commercial.sales_dispatch.post.completed":   {},
		"commercial.sales_invoice.post.completed":    {},
		"commercial.sales_return.post.completed":     {},
		"commercial.sales_order.cancel.completed":    {},
		"commercial.sales_dispatch.cancel.completed": {},
		"commercial.sales_invoice.cancel.completed":  {},
		"commercial.sales_return.cancel.completed":   {},
		"supplier.product_reference.created":         {},
		"inventory.movement.posted":                  {},
		"inventory.movement.reversed":                {},
		"inventory.transfer.created":                 {},
		"inventory.transfer.requested":               {},
		"inventory.transfer.approved":                {},
		"inventory.transfer.in_transit":              {},
		"inventory.transfer.partially_received":      {},
		"inventory.transfer.received":                {},
		"inventory.transfer.cancelled":               {},
		"inventory.count.started":                    {},
		"inventory.count.posted":                     {},
		"settings.email.updated":                     {},
		"hr.employee.created":                        {},
		"hr.employee.updated":                        {},
		"hr.employee.private_profile.updated":        {},
	}
}

func retryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 8 {
		attempt = 8
	}
	return time.Duration(1<<(attempt-1)) * time.Second
}
