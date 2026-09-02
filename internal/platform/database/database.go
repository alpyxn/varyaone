package database

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func Open(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database configuration")
	}
	cfg.MaxConns = 20
	cfg.MinConns = 1
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 15 * time.Minute
	cfg.HealthCheckPeriod = 30 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create connection pool: %w", err)
	}
	return pool, nil
}

// OpenServing opens the pool the HTTP server and worker use to serve traffic.
//
// It differs from Open in two ways that matter once the app connects as the
// non-superuser varyaone_app role and pins a connection per request:
//
//   - a larger MaxConns, because each in-flight request holds a connection for
//     its whole lifetime while the company scope is pinned;
//   - a BeforeAcquire hook that resets varyaone.company_id to its default, so a
//     connection can never be handed out still scoped to a previous request's
//     company even if that request failed to reset it.
func OpenServing(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database configuration")
	}
	cfg.MaxConns = 50
	cfg.MinConns = 1
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 15 * time.Minute
	cfg.HealthCheckPeriod = 30 * time.Second
	cfg.PrepareConn = func(ctx context.Context, conn *pgx.Conn) (bool, error) {
		if _, err := conn.Exec(ctx, `SET `+companyGUC+` TO DEFAULT`); err != nil {
			return false, nil // discard a connection we cannot put into a known state
		}
		return true, nil
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create connection pool: %w", err)
	}
	return pool, nil
}

// companyGUC is the transaction-local setting the row-level-security policies
// installed by migration 000147 read. While it is unset or empty every policy is
// transparent (all rows visible); once set, a connection sees only rows whose
// company_id matches (plus rows with a NULL company_id).
const companyGUC = "varyaone.company_id"

type txBeginner interface {
	Begin(context.Context) (pgx.Tx, error)
}

// WithCompany runs fn inside a single transaction whose row-level-security scope
// is pinned to companyID. Both the RLS policy and any explicit `WHERE company_id`
// predicate then agree, so a query that forgets the predicate still cannot read
// or write another company's rows (when connected as the non-superuser
// varyaone_app role).
//
// It is safe to adopt incrementally: callers still on the raw pool keep working
// because the GUC simply stays unset for them.
func WithCompany(ctx context.Context, db txBeginner, companyID string, fn func(pgx.Tx) error) (err error) {
	if companyID == "" {
		return fmt.Errorf("database: WithCompany requires a company id")
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin company-scoped transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
	}()
	// set_config with is_local=true is the parameterised form of SET LOCAL; it is
	// reverted automatically when the transaction ends, so the pooled connection
	// is never left scoped.
	if _, err = tx.Exec(ctx, `SELECT set_config($1, $2, true)`, companyGUC, companyID); err != nil {
		return fmt.Errorf("pin company scope: %w", err)
	}
	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit company-scoped transaction: %w", err)
	}
	return nil
}
