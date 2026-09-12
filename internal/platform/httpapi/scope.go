package httpapi

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/alpyxn/varyaone/internal/backup"
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
//
// A request that may write also takes the backup write barrier in shared mode
// on this same connection, and holds it for the request's lifetime. That is
// what lets a backup pin a file tree and a database that describe the same
// instant: without it, a write landing between the two captures produces an
// archive whose rows and files disagree — a backup that verifies perfectly and
// restores to something that never existed.
//
// Shared mode means requests never wait for each other. They wait only while a
// backup holds the barrier exclusively, which is the duration of a hard-link
// walk, not of the whole backup.
func scopeRequestConnection(ctx context.Context, companyID string, mutating bool) (context.Context, func(), error) {
	pool := requestScopePool.Load()
	if pool == nil {
		return ctx, func() {}, nil
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		slog.Default().Error("company scope: acquire connection", "trace_id", TraceID(ctx), "error", err)
		return nil, nil, fmt.Errorf("acquire company-scoped connection: %w", err)
	}
	// An empty companyID (a session with no selected company) sets the GUC to the
	// empty string, which every policy treats as "no scope" — transparent, same
	// as an unauthenticated request.
	if _, err := conn.Exec(ctx, `SELECT set_config('varyaone.company_id', $1, false)`, companyID); err != nil {
		slog.Default().Error("company scope: set_config", "trace_id", TraceID(ctx), "error", err)
		conn.Release()
		return nil, nil, fmt.Errorf("set company scope: %w", err)
	}
	release := conn.Release
	if mutating {
		if err := backup.AcquireWriteBarrier(ctx, conn); err != nil {
			slog.Default().Error("write barrier", "trace_id", TraceID(ctx), "error", err)
			conn.Release()
			return nil, nil, err
		}
		release = func() {
			backup.ReleaseWriteBarrier(conn)
			conn.Release()
		}
	}
	return database.ContextWithConn(ctx, conn), release, nil
}

// isMutatingRequest reports whether a request may write, and therefore whether
// it must hold the backup write barrier.
//
// The method is the fast path, with two deliberate exceptions.
//
// The system routes are excluded entirely: that is where the backup itself
// runs, and a download holding the barrier in shared mode while its own engine
// waits to take it exclusively would deadlock against itself.
//
// Anything whose method is not a known read is treated as a writer. Being wrong
// in that direction costs one cheap shared-lock acquisition; being wrong the
// other way costs the consistency the barrier exists to provide.
func isMutatingRequest(r *http.Request) bool {
	if strings.HasPrefix(r.URL.Path, "/api/v1/system/") {
		// Coordinated by the operation lease instead — see internal/platform/opctl.
		return false
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	default:
		return true
	}
}
