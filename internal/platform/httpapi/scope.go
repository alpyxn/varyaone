package httpapi

import (
	"context"
	"log/slog"
	"sync/atomic"

	"github.com/alpyxn/varyaone/internal/platform/database"
	"github.com/jackc/pgx/v5/pgxpool"
)

// requestScopePool holds the pool that requireSession pins a connection from so
// that database queries during a request run with varyaone.company_id set and
// the row-level-security policies enforce company isolation.
//
// It is process-global and set by NewRouter from the WithCompanyScope option.
// This is safe:
//   - production runs exactly one router;
//   - tests that do not pass WithCompanyScope leave it nil (requireSession then
//     behaves exactly as before — no connection pinning);
//   - tests connect to Postgres as a superuser, which bypasses row-level
//     security entirely, so even a leaked scope pool changes nothing there.
var requestScopePool atomic.Pointer[pgxpool.Pool]

// WithCompanyScope tells the router to pin a per-request connection scoped to
// the caller's company. Pass the raw serving pool (the same one wrapped by
// database.NewScoped for the services).
func WithCompanyScope(pool *pgxpool.Pool) RouterOption {
	return func(o *routerOptions) { o.scopePool = pool }
}

// scopeRequestConnection acquires a connection, sets its company scope from the
// session, and pins it onto the context. The returned release function must be
// deferred by the caller. When no scope pool is configured it is a no-op.
func scopeRequestConnection(ctx context.Context, companyID string) (context.Context, func()) {
	pool := requestScopePool.Load()
	if pool == nil {
		return ctx, func() {}
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		slog.Default().Error("company scope: acquire connection", "trace_id", TraceID(ctx), "error", err)
		return ctx, func() {}
	}
	// An empty companyID (a session with no selected company) sets the GUC to the
	// empty string, which every policy treats as "no scope" — transparent, same
	// as an unauthenticated request.
	if _, err := conn.Exec(ctx, `SELECT set_config('varyaone.company_id', $1, false)`, companyID); err != nil {
		slog.Default().Error("company scope: set_config", "trace_id", TraceID(ctx), "error", err)
		conn.Release()
		return ctx, func() {}
	}
	return database.ContextWithConn(ctx, conn), conn.Release
}
