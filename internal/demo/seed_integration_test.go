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
		"demo company": {`SELECT count(*) FROM companies WHERE id=$1 AND is_demo`, 1},
		// The placeholder logo reaches the payslip PDF and the app header; a
		// demo with an empty logo hides both.
		"company logo":             {`SELECT count(*) FROM companies WHERE id=$1 AND logo LIKE 'data:image/png;base64,%'`, 1},
		"parties":                  {`SELECT count(*) FROM parties WHERE company_id=$1`, 11},
		"products":                 {`SELECT count(*) FROM products WHERE company_id=$1`, 14},
		"stock movements":          {`SELECT count(*) FROM stock_movements WHERE company_id=$1`, 1},
		"party ledger":             {`SELECT count(*) FROM party_ledger_entries WHERE company_id=$1`, 1},
		"finance payments":         {`SELECT count(*) FROM finance_payments WHERE company_id=$1`, 1},
		"employees":                {`SELECT count(*) FROM employees WHERE company_id=$1`, 6},
		"fixed assets":             {`SELECT count(*) FROM fixed_assets WHERE company_id=$1`, 4},
		"posted sales invoices":    {`SELECT count(*) FROM sales_invoices WHERE company_id=$1 AND status='POSTED'`, 11},
		"confirmed sales orders":   {`SELECT count(*) FROM sales_orders WHERE company_id=$1 AND status='CONFIRMED'`, 3},
		"posted purchase invoices": {`SELECT count(*) FROM purchase_invoices WHERE company_id=$1 AND status='POSTED'`, 5},
		// The showcase is only as good as the parts of the product it reaches:
		// a second warehouse, variant-tracked stock, both returns, the purchase
		// order chain, dispatch notes and transfers each have screens that are
		// empty without seeded data.
		"warehouses":              {`SELECT count(*) FROM warehouses WHERE company_id=$1 AND NOT is_system`, 2},
		"product variants":        {`SELECT count(*) FROM product_variants WHERE company_id=$1`, 11},
		"variant stock positions": {`SELECT count(*) FROM stock_positions WHERE company_id=$1 AND variant_id IS NOT NULL`, 8},
		"store warehouse stock":   {`SELECT count(*) FROM stock_positions p JOIN warehouses w ON w.company_id=p.company_id AND w.id=p.warehouse_id WHERE p.company_id=$1 AND w.code='KDK' AND p.physical_quantity>0`, 3},
		"purchase orders":         {`SELECT count(*) FROM purchase_orders WHERE company_id=$1`, 1},
		"posted goods receipts":   {`SELECT count(*) FROM goods_receipts WHERE company_id=$1 AND status='POSTED'`, 1},
		"posted purchase returns": {`SELECT count(*) FROM purchase_returns WHERE company_id=$1 AND status='POSTED'`, 1},
		"posted sales dispatches": {`SELECT count(*) FROM sales_dispatches WHERE company_id=$1 AND status='POSTED'`, 5},
		"posted sales returns":    {`SELECT count(*) FROM sales_returns WHERE company_id=$1 AND status='POSTED'`, 1},
		"received transfers":      {`SELECT count(*) FROM warehouse_transfers WHERE company_id=$1 AND state='RECEIVED'`, 2},
		"transfers in transit":    {`SELECT count(*) FROM warehouse_transfers WHERE company_id=$1 AND state='IN_TRANSIT'`, 1},
		"manual stock operations": {`SELECT count(*) FROM stock_movement_operations WHERE company_id=$1`, 1},
		"manual stock movements":  {`SELECT count(*) FROM stock_movements WHERE company_id=$1 AND movement_type='MANUAL_ADJUSTMENT'`, 2},
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

	// Seeded cashflow must read like a possible business history. In
	// particular, an automatically allocated collection may not predate the
	// invoice it settles, and the showcase should include a genuinely partial
	// settlement rather than only fully paid and untouched invoices.
	var futureDatedAllocations int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM finance_payment_allocations a
		JOIN finance_payments p ON p.company_id=a.company_id AND p.id=a.payment_id
		JOIN finance_invoice_open_items oi ON oi.company_id=a.company_id AND oi.id=a.open_item_id
		WHERE a.company_id=$1 AND a.reversal_of_id IS NULL AND p.transaction_date < oi.document_date`, CompanyID).Scan(&futureDatedAllocations); err != nil {
		t.Fatalf("future-dated payment allocations: %v", err)
	}
	if futureDatedAllocations != 0 {
		t.Errorf("found %d payment allocations dated before their invoices", futureDatedAllocations)
	}

	var partiallyPaidInvoices int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM (
			SELECT oi.id
			FROM finance_invoice_open_items oi
			JOIN finance_payment_allocations a ON a.company_id=oi.company_id AND a.open_item_id=oi.id AND a.reversal_of_id IS NULL
			WHERE oi.company_id=$1
			GROUP BY oi.id, oi.original_amount
			HAVING SUM(a.amount) > 0 AND SUM(a.amount) < oi.original_amount
		) partial`, CompanyID).Scan(&partiallyPaidInvoices); err != nil {
		t.Fatalf("partially paid invoices: %v", err)
	}
	if partiallyPaidInvoices == 0 {
		t.Error("seed produced no partially paid invoice")
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
