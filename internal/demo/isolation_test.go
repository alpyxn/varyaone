package demo

import (
	"context"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// isolatedDemo gives one demo test its own schema in the shared test database,
// and returns a maintenance DSN that lands in the same schema.
//
// Both halves matter. The demo runner purges and reseeds whole companies over
// an owner connection it opens itself from Options.MaintenanceDSN, so pointing
// only the pool at a schema would leave that connection working in `public`.
// These tests used to run entirely in `public`: they migrated the shared CI
// database to the latest version, which broke the workflow step asserting that
// `migrate status` still reports a pending migration, and left a seeded demo
// company (plus, from the purge guard test, an ordinary company) visible to
// every package that ran afterwards.
func isolatedDemo(t *testing.T, ctx context.Context, databaseURL string) (*pgxpool.Pool, string) {
	t.Helper()
	base, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("varya_demo_%d", time.Now().UnixNano())
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
	t.Cleanup(func() {
		pool.Close()
		_, _ = base.Exec(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
		base.Close()
	})
	return pool, schemaDSN(t, databaseURL, schema)
}

// schemaDSN copies a connection string with search_path pinned to schema. pgx
// forwards unrecognised query parameters as PostgreSQL runtime parameters, so
// every connection opened from the result starts in that schema.
func schemaDSN(t *testing.T, databaseURL, schema string) string {
	t.Helper()
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	// Appending a query parameter only means anything for the URL form; a
	// keyword/value DSN would come back mangled and silently unscoped.
	if parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" {
		t.Fatalf("VARYAONE_TEST_DATABASE_URL must be a postgres:// URL, got %q", databaseURL)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}
