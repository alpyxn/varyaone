package database

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Querier is the subset of *pgxpool.Pool that the service layer uses. Services
// hold a Querier instead of a concrete pool so the HTTP layer can hand them a
// request-scoped connection (pinned to one company via the varyaone.company_id
// GUC) without any change at the call sites.
//
// *pgxpool.Pool, *pgxpool.Conn and Scoped all satisfy it.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Begin(ctx context.Context) (pgx.Tx, error)
	BeginTx(ctx context.Context, txOptions pgx.TxOptions) (pgx.Tx, error)
}

var (
	_ Querier = (*pgxpool.Pool)(nil)
	_ Querier = (*pgxpool.Conn)(nil)
	_ Querier = (*Scoped)(nil)
)

type connCtxKey struct{}

// ContextWithConn pins conn onto ctx. Every Scoped operation carrying this ctx
// runs on conn, so a whole HTTP request shares one connection whose
// varyaone.company_id has already been set.
func ContextWithConn(ctx context.Context, conn *pgxpool.Conn) context.Context {
	return context.WithValue(ctx, connCtxKey{}, conn)
}

func connFromContext(ctx context.Context) *pgxpool.Conn {
	conn, _ := ctx.Value(connCtxKey{}).(*pgxpool.Conn)
	return conn
}

// WithoutConn returns a context with the request-pinned connection removed, so
// Scoped falls back to the shared pool. Use it for the few operations that must
// see across companies — company switching and session hydration — which would
// otherwise be filtered to the caller's currently selected company.
func WithoutConn(ctx context.Context) context.Context {
	if connFromContext(ctx) == nil {
		return ctx
	}
	return context.WithValue(ctx, connCtxKey{}, (*pgxpool.Conn)(nil))
}

// Scoped routes each query to the request-pinned connection when the context has
// one, and to the shared pool otherwise (workers, startup, background jobs,
// tests).
type Scoped struct {
	pool *pgxpool.Pool
}

func NewScoped(pool *pgxpool.Pool) *Scoped { return &Scoped{pool: pool} }

// Pool returns the underlying pool for the rare caller that needs pool-only
// behaviour (Ping, Stat).
func (s *Scoped) Pool() *pgxpool.Pool { return s.pool }

func (s *Scoped) handle(ctx context.Context) Querier {
	if conn := connFromContext(ctx); conn != nil {
		return conn
	}
	return s.pool
}

func (s *Scoped) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return s.handle(ctx).Exec(ctx, sql, args...)
}

func (s *Scoped) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return s.handle(ctx).Query(ctx, sql, args...)
}

func (s *Scoped) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return s.handle(ctx).QueryRow(ctx, sql, args...)
}

func (s *Scoped) Begin(ctx context.Context) (pgx.Tx, error) {
	return s.handle(ctx).Begin(ctx)
}

func (s *Scoped) BeginTx(ctx context.Context, txOptions pgx.TxOptions) (pgx.Tx, error) {
	return s.handle(ctx).BeginTx(ctx, txOptions)
}
