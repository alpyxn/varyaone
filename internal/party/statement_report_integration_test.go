package party

import (
	"bytes"
	"context"
	"math/big"
	"os"
	"testing"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/platform/migrations"
	"github.com/google/uuid"
)

// TestStatementReportRunningBalanceAndPeriodSummary proves the cari ekstre read
// model: entries come back oldest-first with a cumulative running balance that
// carries the pre-period opening balance, and the summary totals match.
func TestStatementReportRunningBalanceAndPeriodSummary(t *testing.T) {
	databaseURL := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := partyTestPool(t, ctx, databaseURL)
	if err := migrations.New(pool).Up(ctx); err != nil {
		t.Fatal(err)
	}
	identityService, err := identity.NewService(pool, bytes.Repeat([]byte{5}, 32))
	if err != nil {
		t.Fatal(err)
	}
	session, err := identityService.Setup(ctx, identity.SetupInput{AdminName: "Ekstre Yönetici", AdminEmail: "ekstre@example.test", Password: "uzun-ve-guvenli-parola", LegalName: "Ekstre AŞ", TradeName: "Ekstre", EntityType: "LEGAL_ENTITY"}, identity.RequestMeta{TraceID: "ekstre-test"})
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(pool)

	created, err := service.Create(ctx, session, Input{Kind: "ORGANIZATION", IsCustomer: true, DisplayName: "Ekstre Cari", LegalName: "Ekstre Cari Ltd", DefaultCurrency: "TRY", RiskPolicy: "WARN"}, identity.RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}

	post := func(debit, credit, date string) {
		t.Helper()
		day, _ := time.Parse("2006-01-02", date)
		if _, err := service.PostLedgerEntry(ctx, session, LedgerEntry{
			PartyID: created.ID, Currency: "TRY", EntryType: "TEST_ENTRY", SourceType: "test",
			SourceID: uuid.NewString(), IdempotencyKey: "ekstre:" + date, Description: "Ekstre " + date,
			Debit: debit, Credit: credit, ExchangeRate: "1", DocumentDate: day,
		}, identity.RequestMeta{}); err != nil {
			t.Fatalf("post %s: %v", date, err)
		}
	}
	// Before the report window: nets to +100 opening balance.
	post("100.0000", "0", "2026-05-10")
	// Inside the window.
	post("300.0000", "0", "2026-06-01")
	post("0", "120.0000", "2026-06-20")

	from, _ := time.Parse("2006-01-02", "2026-06-01")
	to, _ := time.Parse("2006-01-02", "2026-06-30")
	report, err := service.StatementReport(ctx, session, created.ID, from, to, "TRY", 100)
	if err != nil {
		t.Fatalf("statement report: %v", err)
	}
	if report.OpeningBalance != "100.0000" {
		t.Fatalf("opening balance = %q, want 100.0000", report.OpeningBalance)
	}
	if report.TotalDebit != "300.0000" || report.TotalCredit != "120.0000" {
		t.Fatalf("totals debit=%q credit=%q, want 300.0000 / 120.0000", report.TotalDebit, report.TotalCredit)
	}
	if report.ClosingBalance != "280.0000" {
		t.Fatalf("closing balance = %q, want 280.0000", report.ClosingBalance)
	}
	if len(report.Items) != 2 {
		t.Fatalf("expected 2 in-window entries, got %d", len(report.Items))
	}
	// Oldest first, running balance carries the opening balance.
	if report.Items[0].RunningBalance != "400.0000" {
		t.Fatalf("row 1 running balance = %q, want 400.0000", report.Items[0].RunningBalance)
	}
	if report.Items[1].RunningBalance != "280.0000" {
		t.Fatalf("row 2 running balance = %q, want 280.0000", report.Items[1].RunningBalance)
	}

	// A foreign-currency entry: 10 GBP debit at rate 40 => 400 TRY. With no
	// currency filter every figure must be converted to the base currency and
	// summed there, never added to the TRY numbers as a raw 10.
	gbpDay, _ := time.Parse("2006-01-02", "2026-06-15")
	if _, err := service.PostLedgerEntry(ctx, session, LedgerEntry{
		PartyID: created.ID, Currency: "GBP", EntryType: "TEST_ENTRY", SourceType: "test",
		SourceID: uuid.NewString(), IdempotencyKey: "ekstre:gbp", Description: "Ekstre GBP",
		Debit: "10.0000", Credit: "0", ExchangeRate: "40", DocumentDate: gbpDay,
	}, identity.RequestMeta{}); err != nil {
		t.Fatalf("post gbp: %v", err)
	}

	allCcy, err := service.StatementReport(ctx, session, created.ID, from, to, "", 100)
	if err != nil {
		t.Fatalf("statement report (all currencies): %v", err)
	}
	if allCcy.Currency != "TRY" {
		t.Fatalf("all-currency report currency = %q, want TRY", allCcy.Currency)
	}
	// Opening (100) unchanged; period debit 300 TRY + 400 TRY (GBP) = 700.
	if allCcy.TotalDebit != "700.0000" || allCcy.TotalCredit != "120.0000" {
		t.Fatalf("all-currency totals debit=%q credit=%q, want 700.0000 / 120.0000", allCcy.TotalDebit, allCcy.TotalCredit)
	}
	if allCcy.ClosingBalance != "680.0000" {
		t.Fatalf("all-currency closing balance = %q, want 680.0000", allCcy.ClosingBalance)
	}

	// The core ledger invariant must hold on every page/order combination:
	//   closing == opening + Σ(period debit) - Σ(period credit)
	// and the running balance of the chronologically-last row equals closing.
	addSub := func(a, b, c string) string {
		t.Helper()
		ar, _ := new(big.Rat).SetString(a)
		br, _ := new(big.Rat).SetString(b)
		cr, _ := new(big.Rat).SetString(c)
		return ar.Add(ar, br.Sub(br, cr)).FloatString(4)
	}
	for _, currency := range []string{"TRY", ""} {
		for _, order := range []string{"asc", "desc"} {
			rep, repErr := service.StatementReportPage(ctx, session, created.ID, from, to, currency, "", order, 100)
			if repErr != nil {
				t.Fatalf("statement page currency=%q order=%q: %v", currency, order, repErr)
			}
			if got := addSub(rep.OpeningBalance, rep.TotalDebit, rep.TotalCredit); got != rep.ClosingBalance {
				t.Fatalf("currency=%q order=%q: opening(%s)+debit(%s)-credit(%s)=%s != closing %s",
					currency, order, rep.OpeningBalance, rep.TotalDebit, rep.TotalCredit, got, rep.ClosingBalance)
			}
			if len(rep.Items) == 0 {
				continue
			}
			last := rep.Items[len(rep.Items)-1]
			if order == "desc" {
				last = rep.Items[0]
			}
			if last.RunningBalance != rep.ClosingBalance {
				t.Fatalf("currency=%q order=%q: last row running balance %q != closing %q",
					currency, order, last.RunningBalance, rep.ClosingBalance)
			}
		}
	}
}
