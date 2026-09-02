package httpapi

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/alpyxn/varyaone/internal/platform/database"
	"github.com/jackc/pgx/v5/pgxpool"
)

func scopeTestPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	pool, err := database.OpenServing(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func readScope(t *testing.T, ctx context.Context, q database.Querier) string {
	t.Helper()
	var v string
	if err := q.QueryRow(ctx, `SELECT current_setting('varyaone.company_id', true)`).Scan(&v); err != nil {
		t.Fatalf("read scope: %v", err)
	}
	return v
}

// TestScopeRequestConnectionPinsAndDoesNotLeak covers the requireSession wiring:
// a request's queries run with varyaone.company_id set to the session company,
// the scope is gone once the request's release runs, and a later request (or an
// unauthenticated one) never inherits it.
func TestScopeRequestConnectionPinsAndDoesNotLeak(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := scopeTestPool(t, ctx)
	scoped := database.NewScoped(pool)

	requestScopePool.Store(pool)
	t.Cleanup(func() { requestScopePool.Store(nil) })

	const companyA = "aaaaaaaa-0000-4000-8000-00000000aaaa"
	const companyB = "bbbbbbbb-0000-4000-8000-00000000bbbb"

	reqCtx, release := scopeRequestConnection(ctx, companyA)
	if got := readScope(t, reqCtx, scoped); got != companyA {
		t.Fatalf("request A scope = %q, want %q", got, companyA)
	}
	release()

	// After release the pooled connection is back and clean.
	if got := readScope(t, ctx, scoped); got != "" {
		t.Fatalf("after release, pool scope = %q, want empty", got)
	}

	reqCtx, release = scopeRequestConnection(ctx, companyB)
	if got := readScope(t, reqCtx, scoped); got != companyB {
		t.Fatalf("request B scope = %q, want %q", got, companyB)
	}
	release()

	// An empty company (session with no company selected) is transparent.
	reqCtx, release = scopeRequestConnection(ctx, "")
	if got := readScope(t, reqCtx, scoped); got != "" {
		t.Fatalf("empty-company request scope = %q, want empty", got)
	}
	release()
}

func TestScopeRequestConnectionNoopWithoutPool(t *testing.T) {
	requestScopePool.Store(nil)
	ctx := context.Background()
	got, release := scopeRequestConnection(ctx, "whatever")
	defer release()
	if got != ctx {
		t.Fatal("expected the original context back when no scope pool is configured")
	}
}
