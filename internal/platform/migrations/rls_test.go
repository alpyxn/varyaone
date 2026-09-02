package migrations

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestDeployAppRoleScriptMatchesMigration guards that the standalone
// app_role.sql (embedded, run after pg_restore and by deploy.sh) stays
// identical, statement for statement, to the role/grant body of the
// app-role migration (000003_app_role).
func TestDeployAppRoleScriptMatchesMigration(t *testing.T) {
	items, err := load()
	if err != nil {
		t.Fatal(err)
	}
	var appRoleMigration string
	for _, item := range items {
		if item.Name == "app_role" {
			appRoleMigration = item.SQL
		}
	}
	if appRoleMigration == "" {
		t.Fatal("app_role migration not found")
	}
	if normalizeSQL(AppRoleSQL()) != normalizeSQL(appRoleMigration) {
		t.Errorf("app_role.sql and the app_role migration have diverged\nscript:\n%s\n\nmigration:\n%s",
			normalizeSQL(AppRoleSQL()), normalizeSQL(appRoleMigration))
	}
}

var sqlCommentLine = regexp.MustCompile(`(?m)^\s*--.*$`)

func normalizeSQL(s string) string {
	s = sqlCommentLine.ReplaceAllString(s, "")
	return strings.Join(strings.Fields(s), " ")
}

// TestEveryCompanyScopedTableHasRowLevelSecurity fails when a table carrying a
// company_id column is missing the FORCE'd company_isolation policy. A new
// company-scoped table therefore cannot ship without an RLS policy (add it to a
// migration alongside the CREATE TABLE).
func TestEveryCompanyScopedTableHasRowLevelSecurity(t *testing.T) {
	databaseURL := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool := isolatedPool(t, ctx, databaseURL)
	if err := New(pool).Up(ctx); err != nil {
		t.Fatal(err)
	}

	scoped, err := CompanyScopedTables()
	if err != nil {
		t.Fatal(err)
	}

	for _, table := range scoped {
		var forced bool
		if err := pool.QueryRow(ctx, `
			SELECT c.relrowsecurity AND c.relforcerowsecurity
			FROM pg_class c
			JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE c.relname = $1 AND n.nspname = current_schema()`, table).Scan(&forced); err != nil {
			t.Fatalf("%s: reading pg_class: %v", table, err)
		}
		if !forced {
			t.Errorf("%s: row-level security is not ENABLEd + FORCEd", table)
		}

		var policies int
		if err := pool.QueryRow(ctx, `
			SELECT count(*) FROM pg_policies
			WHERE schemaname = current_schema() AND tablename = $1 AND policyname = 'company_isolation'`, table).Scan(&policies); err != nil {
			t.Fatalf("%s: reading pg_policies: %v", table, err)
		}
		if policies != 1 {
			t.Errorf("%s: expected exactly one company_isolation policy, found %d", table, policies)
		}
	}
}

// TestRowLevelSecurityIsolatesCompaniesForAppRole proves the policy actually
// filters rows for a non-superuser role once the GUC is set, and that it stays
// transparent (all rows visible) while the GUC is unset.
func TestRowLevelSecurityIsolatesCompaniesForAppRole(t *testing.T) {
	databaseURL := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool := isolatedPool(t, ctx, databaseURL)
	if err := New(pool).Up(ctx); err != nil {
		t.Fatal(err)
	}

	const companyA = "aaaaaaaa-0000-4000-8000-000000000001"
	const companyB = "bbbbbbbb-0000-4000-8000-000000000002"
	if _, err := pool.Exec(ctx, `INSERT INTO companies(id,legal_name,trade_name,entity_type) VALUES($1,'A','A','LEGAL_ENTITY'),($2,'B','B','LEGAL_ENTITY')`, companyA, companyB); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO parties(id,company_id,code,kind,is_customer,display_name,legal_name,default_currency) VALUES
		(gen_random_uuid(),$1,'A1','ORGANIZATION',true,'A One','A One','TRY'),
		(gen_random_uuid(),$2,'B1','ORGANIZATION',true,'B One','B One','TRY')`, companyA, companyB); err != nil {
		t.Fatal(err)
	}

	// Grant the app role access within this test schema (the migration grants in
	// "public"; isolated tests run in a throwaway schema).
	var schema string
	if err := pool.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`GRANT USAGE ON SCHEMA ` + schema + ` TO varyaone_app`,
		`GRANT SELECT ON ALL TABLES IN SCHEMA ` + schema + ` TO varyaone_app`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `SET ROLE varyaone_app`); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = conn.Exec(context.Background(), `RESET ROLE`) }()

	countParties := func() int {
		t.Helper()
		var n int
		if err := conn.QueryRow(ctx, `SELECT count(*) FROM parties`).Scan(&n); err != nil {
			t.Fatalf("count parties: %v", err)
		}
		return n
	}

	// GUC unset -> transparent, both companies visible (non-breaking guarantee).
	if got := countParties(); got != 2 {
		t.Fatalf("GUC unset: expected 2 parties visible, got %d", got)
	}

	if _, err := conn.Exec(ctx, `SELECT set_config('varyaone.company_id', $1, false)`, companyA); err != nil {
		t.Fatal(err)
	}
	if got := countParties(); got != 1 {
		t.Fatalf("GUC = company A: expected 1 party visible, got %d", got)
	}

	if _, err := conn.Exec(ctx, `SELECT set_config('varyaone.company_id', $1, false)`, companyB); err != nil {
		t.Fatal(err)
	}
	if got := countParties(); got != 1 {
		t.Fatalf("GUC = company B: expected 1 party visible, got %d", got)
	}

	if _, err := conn.Exec(ctx, `SELECT set_config('varyaone.company_id', '', false)`); err != nil {
		t.Fatal(err)
	}
	if got := countParties(); got != 2 {
		t.Fatalf("GUC reset: expected 2 parties visible, got %d", got)
	}
}

// TestRowLevelSecurityIsolatesEveryModuleTable seeds a row per company across a
// spread of module tables (party, catalog, finance settings) and proves the
// varyaone_app role, scoped to one company, sees only that company's rows and
// cannot write another company's — the DB-level backstop for finance, inventory,
// purchasing, payroll and the rest without needing each module's Go API.
func TestRowLevelSecurityIsolatesEveryModuleTable(t *testing.T) {
	databaseURL := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool := isolatedPool(t, ctx, databaseURL)
	if err := New(pool).Up(ctx); err != nil {
		t.Fatal(err)
	}

	const companyA = "aaaaaaaa-0000-4000-8000-00000000000a"
	const companyB = "bbbbbbbb-0000-4000-8000-00000000000b"
	if _, err := pool.Exec(ctx, `INSERT INTO companies(id,legal_name,trade_name,entity_type) VALUES($1,'A','A','LEGAL_ENTITY'),($2,'B','B','LEGAL_ENTITY')`, companyA, companyB); err != nil {
		t.Fatal(err)
	}

	// table -> a parametrised INSERT of one row for $1 = company id.
	seeds := map[string]string{
		"parties":        `INSERT INTO parties(id,company_id,code,kind,is_customer,display_name,legal_name,default_currency) VALUES(gen_random_uuid(),$1,'C','ORGANIZATION',true,'n','n','TRY')`,
		"payment_terms":  `INSERT INTO payment_terms(id,company_id,code,name) VALUES(gen_random_uuid(),$1,'NET','net')`,
		"party_groups":   `INSERT INTO party_groups(id,company_id,code,name) VALUES(gen_random_uuid(),$1,'G','grp')`,
		"product_brands": `INSERT INTO product_brands(id,company_id,code,name) VALUES(gen_random_uuid(),$1,'BR','brand')`,
	}
	for table, stmt := range seeds {
		for _, company := range []string{companyA, companyB} {
			if _, err := pool.Exec(ctx, stmt, company); err != nil {
				t.Fatalf("seed %s: %v", table, err)
			}
		}
	}

	var schema string
	if err := pool.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`GRANT USAGE ON SCHEMA ` + schema + ` TO varyaone_app`,
		`GRANT SELECT, INSERT ON ALL TABLES IN SCHEMA ` + schema + ` TO varyaone_app`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SET ROLE varyaone_app`); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = conn.Exec(context.Background(), `RESET ROLE`) }()
	if _, err := conn.Exec(ctx, `SELECT set_config('varyaone.company_id', $1, false)`, companyA); err != nil {
		t.Fatal(err)
	}

	for table := range seeds {
		var n int
		if err := conn.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&n); err != nil {
			t.Fatalf("%s: count: %v", table, err)
		}
		if n != 1 {
			t.Errorf("%s: company A sees %d rows, want 1 (its own)", table, n)
		}
	}

	// WITH CHECK: cannot insert a row belonging to company B while scoped to A.
	if _, err := conn.Exec(ctx, `INSERT INTO payment_terms(id,company_id,code,name) VALUES(gen_random_uuid(),$1,'X','x')`, companyB); err == nil {
		t.Error("insert of a company B row while scoped to A was allowed")
	}
}
