package database

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func servingTestPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	pool, err := OpenServing(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func currentScope(t *testing.T, ctx context.Context, q Querier) string {
	t.Helper()
	var v string
	if err := q.QueryRow(ctx, `SELECT current_setting('varyaone.company_id', true)`).Scan(&v); err != nil {
		t.Fatalf("read scope: %v", err)
	}
	return v
}

func TestScopedRoutesToPinnedConnectionThenFallsBackToPool(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := servingTestPool(t, ctx)
	scoped := NewScoped(pool)

	// No pinned connection -> pool -> PrepareConn has reset the GUC.
	if got := currentScope(t, ctx, scoped); got != "" {
		t.Fatalf("unpinned scope = %q, want empty", got)
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	const company = "aaaaaaaa-0000-4000-8000-0000000000aa"
	if _, err := conn.Exec(ctx, `SELECT set_config('varyaone.company_id', $1, false)`, company); err != nil {
		t.Fatal(err)
	}
	pinned := ContextWithConn(ctx, conn)

	if got := currentScope(t, pinned, scoped); got != company {
		t.Fatalf("pinned scope = %q, want %q", got, company)
	}
	// WithoutConn escapes the pin even when the context still carries it.
	if got := currentScope(t, WithoutConn(pinned), scoped); got != "" {
		t.Fatalf("WithoutConn scope = %q, want empty", got)
	}
}

func TestScopedBeginUsesPinnedConnection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := servingTestPool(t, ctx)
	scoped := NewScoped(pool)

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	const company = "bbbbbbbb-0000-4000-8000-0000000000bb"
	if _, err := conn.Exec(ctx, `SELECT set_config('varyaone.company_id', $1, false)`, company); err != nil {
		t.Fatal(err)
	}
	pinned := ContextWithConn(ctx, conn)

	tx, err := scoped.Begin(pinned)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	var v string
	if err := tx.QueryRow(pinned, `SELECT current_setting('varyaone.company_id', true)`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v != company {
		t.Fatalf("tx scope = %q, want %q", v, company)
	}
}
