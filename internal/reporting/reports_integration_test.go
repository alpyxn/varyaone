package reporting

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/alpyxn/varyaone/internal/finance"
	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/platform/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

func reportingTestPool(t *testing.T, ctx context.Context, databaseURL string) *pgxpool.Pool {
	t.Helper()
	base, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("varya_reporting_test_%d", time.Now().UnixNano())
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

// TestReportsExecuteAgainstLiveSchema exercises every reporting query against a
// freshly migrated (empty) database. It does not assert on business results --
// it guarantees the hand-written SQL stays column- and syntax-correct as the
// schema evolves, and that permission gating is enforced.
func TestReportsExecuteAgainstLiveSchema(t *testing.T) {
	databaseURL := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	pool := reportingTestPool(t, ctx, databaseURL)
	if err := migrations.New(pool).Up(ctx); err != nil {
		t.Fatal(err)
	}

	identityService, err := identity.NewService(pool, bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	owner, err := identityService.Setup(ctx, identity.SetupInput{
		AdminName: "Rapor Yönetici", AdminEmail: "reports@example.test", Password: "uzun-ve-guvenli-parola",
		LegalName: "Rapor Firma AŞ", TradeName: "Rapor", EntityType: "LEGAL_ENTITY",
	}, identity.RequestMeta{TraceID: "reporting-integration", IP: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}

	service := NewService(pool, finance.NewService(pool))
	from := time.Now().AddDate(0, -1, 0)
	to := time.Now()
	none := ReportFilters{}

	t.Run("permission gate", func(t *testing.T) {
		denied := identity.Session{User: owner.User, CurrentCompanyID: owner.CurrentCompanyID, Permissions: nil}
		if _, err := service.PartyBalances(ctx, denied, none); err != identity.ErrForbidden {
			t.Fatalf("expected ErrForbidden without reporting.read, got %v", err)
		}
	})

	session := identity.Session{
		User:             owner.User,
		CurrentCompanyID: owner.CurrentCompanyID,
		Permissions: []string{
			"reporting.read", "sales.cost.read",
			"finance.cash_movement.read", "finance.bank_movement.read",
		},
	}

	checks := []struct {
		name string
		run  func() (int, error)
	}{
		{"party-balances", func() (int, error) { r, e := service.PartyBalances(ctx, session, none); return len(r), e }},
		{"stock-status", func() (int, error) { r, e := service.StockStatus(ctx, session, none); return len(r), e }},
		{"stock-movements", func() (int, error) { r, e := service.StockMovements(ctx, session, from, to, none); return len(r), e }},
		{"stock-valuation", func() (int, error) { r, e := service.StockValuation(ctx, session); return len(r), e }},
		{"sales", func() (int, error) { r, e := service.SalesList(ctx, session, from, to, none); return len(r), e }},
		{"purchases", func() (int, error) { r, e := service.PurchaseList(ctx, session, from, to, none); return len(r), e }},
		{"cash-movements", func() (int, error) { r, e := service.CashMovements(ctx, session, from, to, none); return len(r), e }},
		{"bank-movements", func() (int, error) { r, e := service.BankMovements(ctx, session, from, to, none); return len(r), e }},
		{"overdue-receivables", func() (int, error) { r, e := service.OverdueReceivables(ctx, session, to); return len(r), e }},
		{"overdue-payables", func() (int, error) { r, e := service.OverduePayables(ctx, session, to); return len(r), e }},
		{"top-selling", func() (int, error) { r, e := service.TopSellingProducts(ctx, session, from, to, 20); return len(r), e }},
		{"sales-profitability", func() (int, error) { r, e := service.SalesProfitability(ctx, session, from, to); return len(r), e }},
		{"tax-summary-sales", func() (int, error) { r, e := service.TaxSummary(ctx, session, from, to, "SALES"); return len(r), e }},
		{"tax-summary-purchase", func() (int, error) { r, e := service.TaxSummary(ctx, session, from, to, "PURCHASE"); return len(r), e }},
	}
	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			n, err := c.run()
			if err != nil {
				t.Fatalf("%s failed: %v", c.name, err)
			}
			if n != 0 {
				t.Fatalf("%s: expected no rows on an empty database, got %d", c.name, n)
			}
		})
	}
}
