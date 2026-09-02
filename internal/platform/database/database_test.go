package database

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func testPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestWithCompanySetsAndRevertsTheScopeGUC(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := testPool(t, ctx)

	const company = "11111111-1111-4111-8111-111111111111"
	var seen string
	if err := WithCompany(ctx, pool, company, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT current_setting('varyaone.company_id', true)`).Scan(&seen)
	}); err != nil {
		t.Fatal(err)
	}
	if seen != company {
		t.Fatalf("inside WithCompany the GUC was %q, want %q", seen, company)
	}

	// A fresh checkout must not inherit the scope.
	var leaked *string
	if err := pool.QueryRow(ctx, `SELECT current_setting('varyaone.company_id', true)`).Scan(&leaked); err != nil {
		t.Fatal(err)
	}
	if leaked != nil && *leaked != "" {
		t.Fatalf("scope leaked onto pooled connection: %q", *leaked)
	}
}

func TestWithCompanyRollsBackOnError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := testPool(t, ctx)

	sentinel := errors.New("boom")
	err := WithCompany(ctx, pool, "22222222-2222-4222-8222-222222222222", func(tx pgx.Tx) error {
		if _, e := tx.Exec(ctx, `CREATE TEMP TABLE with_company_rollback_probe(x int)`); e != nil {
			return e
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}
}

func TestWithCompanyRejectsEmptyCompany(t *testing.T) {
	if err := WithCompany(context.Background(), nil, "", func(pgx.Tx) error { return nil }); err == nil {
		t.Fatal("expected an error for an empty company id")
	}
}
