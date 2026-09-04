package demo

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/platform/database"
	"github.com/alpyxn/varyaone/internal/platform/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestSeedAndReset drives a full demo build against a real database: the
// company is provisioned and seeded, the seeded data is checked for the things
// the showcase depends on (posted documents, stock, ledgers), and a reset is
// then shown to rebuild the same company from scratch.
func TestSeedAndReset(t *testing.T) {
	databaseURL := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	pool, maintenanceDSN := isolatedDemo(t, ctx, databaseURL)
	if err := migrations.New(pool).Up(ctx); err != nil {
		t.Fatal(err)
	}

	seededAt := time.Date(2026, 6, 15, 9, 0, 0, 0, time.UTC)
	runner := New(pool, Options{
		MaintenanceDSN: maintenanceDSN,
		MasterKey:      bytes.Repeat([]byte{9}, 32),
		Email:          "demo@varyaone.test",
		Password:       "varyaone-demo-2026",
		Now:            func() time.Time { return seededAt },
	})

	if err := runner.Ensure(ctx); err != nil {
		t.Fatalf("first seed failed: %v", err)
	}
	assertSeeded(t, ctx, pool)

	// Ensure is called on every start-up, so a second call must be a no-op
	// rather than a second set of demo documents.
	if err := runner.Ensure(ctx); err != nil {
		t.Fatalf("second ensure failed: %v", err)
	}
	assertSeeded(t, ctx, pool)

	if err := runner.Reset(ctx); err != nil {
		t.Fatalf("reset failed: %v", err)
	}
	assertSeeded(t, ctx, pool)
}

func assertSeeded(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	counts := map[string]struct {
		query string
		min   int
	}{
		"demo company":             {`SELECT count(*) FROM companies WHERE id=$1 AND is_demo`, 1},
		"parties":                  {`SELECT count(*) FROM parties WHERE company_id=$1`, 11},
		"products":                 {`SELECT count(*) FROM products WHERE company_id=$1`, 14},
		"stock movements":          {`SELECT count(*) FROM stock_movements WHERE company_id=$1`, 1},
		"party ledger":             {`SELECT count(*) FROM party_ledger_entries WHERE company_id=$1`, 1},
		"finance payments":         {`SELECT count(*) FROM finance_payments WHERE company_id=$1`, 1},
		"employees":                {`SELECT count(*) FROM employees WHERE company_id=$1`, 6},
		"fixed assets":             {`SELECT count(*) FROM fixed_assets WHERE company_id=$1`, 4},
		"posted sales invoices":    {`SELECT count(*) FROM sales_invoices WHERE company_id=$1 AND status='POSTED'`, 10},
		"confirmed sales orders":   {`SELECT count(*) FROM sales_orders WHERE company_id=$1 AND status='CONFIRMED'`, 3},
		"posted purchase invoices": {`SELECT count(*) FROM purchase_invoices WHERE company_id=$1 AND status='POSTED'`, 3},
	}
	for name, check := range counts {
		var actual int
		if err := pool.QueryRow(ctx, check.query, CompanyID).Scan(&actual); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if actual < check.min {
			t.Errorf("%s: got %d, want at least %d", name, actual, check.min)
		}
	}
}

// TestPurgeRefusesNonDemoCompany is the safety net the whole design rests on:
// the reset path must refuse any company that is not flagged is_demo, so demo
// tooling pointed at a real installation destroys nothing.
func TestPurgeRefusesNonDemoCompany(t *testing.T) {
	databaseURL := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool, maintenanceDSN := isolatedDemo(t, ctx, databaseURL)
	if err := migrations.New(pool).Up(ctx); err != nil {
		t.Fatal(err)
	}
	const password = "varyaone-demo-2026"
	runner := New(pool, Options{
		MaintenanceDSN: maintenanceDSN,
		MasterKey:      bytes.Repeat([]byte{9}, 32),
		Email:          "demo@varyaone.test",
		Password:       password,
		Now:            func() time.Time { return time.Date(2026, 6, 15, 9, 0, 0, 0, time.UTC) },
	})
	if err := runner.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	identityService, err := identity.NewService(database.NewScoped(pool), bytes.Repeat([]byte{9}, 32), identity.WithMaintenanceDSN(maintenanceDSN))
	if err != nil {
		t.Fatal(err)
	}
	meta := identity.RequestMeta{TraceID: "demo-guard-test"}
	session, err := identityService.Login(ctx, "demo@varyaone.test", password, "", meta)
	if err != nil {
		t.Fatal(err)
	}
	// A second, ordinary company alongside the demo one: exactly the shape a
	// mistake would take on a shared installation.
	session, err = identityService.CreateCompany(ctx, session, identity.CreateCompanyInput{
		LegalName: "Gerçek Ticaret AŞ", TradeName: "Gerçek Ticaret", EntityType: "LEGAL_ENTITY",
	}, meta)
	if err != nil {
		t.Fatal(err)
	}
	realCompanyID := session.CurrentCompanyID
	if realCompanyID == CompanyID {
		t.Fatal("test setup did not produce a separate company")
	}

	if err = identityService.PurgeDemoCompany(ctx, realCompanyID); !errors.Is(err, identity.ErrNotDemoCompany) {
		t.Fatalf("purge of a real company returned %v, want ErrNotDemoCompany", err)
	}
	// The existence check must refuse it too, rather than adopting it as demo.
	if _, err = identityService.DemoCompanyExists(ctx, realCompanyID); !errors.Is(err, identity.ErrNotDemoCompany) {
		t.Fatalf("existence check on a real company returned %v, want ErrNotDemoCompany", err)
	}
	var survives bool
	if err = pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM companies WHERE id=$1)`, realCompanyID).Scan(&survives); err != nil {
		t.Fatal(err)
	}
	if !survives {
		t.Fatal("the real company was deleted by a demo purge")
	}
	// And a reset of the demo company must leave the real one untouched.
	if err = runner.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM companies WHERE id=$1)`, realCompanyID).Scan(&survives); err != nil {
		t.Fatal(err)
	}
	if !survives {
		t.Fatal("a demo reset deleted the neighbouring real company")
	}
}
