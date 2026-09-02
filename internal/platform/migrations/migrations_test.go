package migrations

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestEmbeddedMigrationsAreOrderedAndUnique(t *testing.T) {
	items, err := load()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 {
		t.Fatal("expected at least one embedded migration")
	}
	for i := 1; i < len(items); i++ {
		if items[i-1].Version >= items[i].Version {
			t.Fatalf("migrations are not strictly ordered: %d then %d", items[i-1].Version, items[i].Version)
		}
	}
}

func TestConcurrentUpAppliesEachMigrationOnce(t *testing.T) {
	databaseURL := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := isolatedPool(t, ctx, databaseURL)

	const workers = 4
	errors := make(chan error, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			errors <- New(pool).Up(ctx)
		}()
	}
	group.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatalf("concurrent migration failed: %v", err)
		}
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM platform_schema_migrations WHERE version = 1`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("migration applied %d times, want exactly once", count)
	}
}

func TestCompanyScopedForeignKeysAndImmutableAudit(t *testing.T) {
	databaseURL := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := isolatedPool(t, ctx, databaseURL)
	if err := New(pool).Up(ctx); err != nil {
		t.Fatal(err)
	}
	const companyA = "00000000-0000-4000-8000-000000000001"
	const companyB = "00000000-0000-4000-8000-000000000002"
	const branchA = "00000000-0000-4000-8000-000000000003"
	if _, err := pool.Exec(ctx, `INSERT INTO companies(id,legal_name,trade_name,entity_type) VALUES($1,'A','A','LEGAL_ENTITY'),($2,'B','B','LEGAL_ENTITY')`, companyA, companyB); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO branches(id,company_id,code,name) VALUES($1,$2,'MRK','Merkez')`, branchA, companyA); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO warehouses(id,company_id,branch_id,code,name) VALUES('00000000-0000-4000-8000-000000000004',$1,$2,'X','Cross-company')`, companyB, branchA); err == nil {
		t.Fatal("cross-company branch reference was accepted")
	}
	const auditID = "00000000-0000-4000-8000-000000000005"
	if _, err := pool.Exec(ctx, `INSERT INTO security_audit_events(id,event_type) VALUES($1,'TEST')`, auditID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE security_audit_events SET event_type='MUTATED' WHERE id=$1`, auditID); err == nil {
		t.Fatal("security audit mutation was accepted")
	}
}

func isolatedPool(t *testing.T, ctx context.Context, databaseURL string) *pgxpool.Pool {
	t.Helper()
	base, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("varya_test_%d", time.Now().UnixNano())
	if _, err := base.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
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
	return pool
}
