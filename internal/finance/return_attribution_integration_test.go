package finance

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/platform/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// returnFixture is the shared scaffolding for the return-attribution tests: an
// isolated schema, a company with one customer, and helpers that write the
// commercial line ledger the attribution view resolves shares through.
type returnFixture struct {
	ctx       context.Context
	t         *testing.T
	pool      *pgxpool.Pool
	session   identity.Session
	companyID string
	userID    string
	branchID  string
	partyID   string
	service   *Service
}

func newReturnFixture(t *testing.T, name string) *returnFixture {
	t.Helper()
	databaseURL := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)

	base, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("varya_ret_%s_%d", name, time.Now().UnixNano())
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
	if err = migrations.New(pool).Up(ctx); err != nil {
		t.Fatal(err)
	}
	identityService, err := identity.NewService(pool, bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	session, err := identityService.Setup(ctx, identity.SetupInput{
		AdminName: "Finans Yönetici", AdminEmail: name + "@example.test",
		Password: "uzun-ve-guvenli-parola", LegalName: "İade AŞ", TradeName: "İade", EntityType: "LEGAL_ENTITY",
	}, identity.RequestMeta{TraceID: name})
	if err != nil {
		t.Fatal(err)
	}
	f := &returnFixture{ctx: ctx, t: t, pool: pool, session: session, companyID: session.CurrentCompanyID, userID: session.User.ID, service: NewService(pool)}
	if err = pool.QueryRow(ctx, `SELECT id FROM branches WHERE company_id=$1`, f.companyID).Scan(&f.branchID); err != nil {
		t.Fatal(err)
	}
	f.partyID = uuid.NewString()
	f.exec(`INSERT INTO parties(id,company_id,code,kind,is_customer,is_supplier,display_name,legal_name,default_currency) VALUES($1,$2,'CARI-RET','ORGANIZATION',true,false,'İade Müşteri','İade Müşteri AŞ','TRY')`, f.partyID, f.companyID)
	return f
}

func (f *returnFixture) exec(sql string, args ...any) {
	f.t.Helper()
	if _, err := f.pool.Exec(f.ctx, sql, args...); err != nil {
		f.t.Fatalf("fixture: %v", err)
	}
}

// document writes a posted commercial document plus one registry line and
// returns the document and line ids.
func (f *returnFixture) document(typeCode, no, date, total string, quantity string) (string, string) {
	f.t.Helper()
	documentID, lineID := uuid.NewString(), uuid.NewString()
	f.exec(`INSERT INTO documents(id,company_id,document_type_code,document_no,branch_id,party_id,document_date,currency_code,grand_total,status,posted_at,created_by,updated_by) VALUES($1,$2,$3,$4,$5,$6,$7::date,'TRY',$8,'POSTED',now(),$9,$9)`,
		documentID, f.companyID, typeCode, no, f.branchID, f.partyID, date, total, f.userID)
	aggregate := map[string]string{
		"SALES_DELIVERY":       "SALES_DISPATCH",
		"SALES_INVOICE":        "SALES_INVOICE",
		"SALES_RETURN_INVOICE": "SALES_RETURN",
	}[typeCode]
	f.exec(`INSERT INTO commercial_line_registry(company_id,line_id,aggregate_type,document_id,line_no,line_type,quantity,base_quantity) VALUES($1,$2,$3,$4,1,'PRODUCT',$5,$5)`,
		f.companyID, lineID, aggregate, documentID, quantity)
	return documentID, lineID
}

// openItem gives a document its finance projection: the party ledger entry and
// the open item every settlement/aging query reads.
func (f *returnFixture) openItem(documentID, date, amount string) string {
	f.t.Helper()
	ledgerID, openItemID := uuid.NewString(), uuid.NewString()
	f.exec(`INSERT INTO party_ledger_entries(id,company_id,party_id,currency,entry_type,source_type,source_id,idempotency_key,description,debit,credit,document_date,actor_user_id) VALUES($1,$2,$3,'TRY','RECEIVABLE','document',$4,$5,'Fatura',$6,0,$7::date,$8)`,
		ledgerID, f.companyID, f.partyID, documentID, "invoice:"+openItemID, amount, date, f.userID)
	f.exec(`INSERT INTO finance_invoice_open_items(id,company_id,document_id,party_id,party_ledger_entry_id,side,currency,original_amount,base_currency,base_amount,document_date) VALUES($1,$2,$3,$4,$5,'RECEIVABLE','TRY',$6,'TRY',$6,$7::date)`,
		openItemID, f.companyID, documentID, f.partyID, ledgerID, amount, date)
	// The posting row is what ReverseInvoiceTx resolves a document through.
	f.exec(`INSERT INTO finance_invoice_postings(id,company_id,document_id,party_ledger_entry_id,open_item_id,idempotency_key,posted_by) VALUES($1,$2,$3,$4,$5,$6,$7)`,
		uuid.NewString(), f.companyID, documentID, ledgerID, openItemID, "posting:"+openItemID, f.userID)
	return openItemID
}

func (f *returnFixture) source(documentID, sourceDocumentID, relation string) {
	f.t.Helper()
	f.exec(`INSERT INTO commercial_document_sources(company_id,document_id,source_document_id,relation_type) VALUES($1,$2,$3,$4)`,
		f.companyID, documentID, sourceDocumentID, relation)
}

func (f *returnFixture) allocate(sourceLineID, targetLineID, allocationType, quantity string) {
	f.t.Helper()
	f.exec(`INSERT INTO commercial_line_allocations(id,company_id,source_line_id,target_line_id,allocation_type,quantity,base_quantity) VALUES($1,$2,$3,$4,$5,$6,$6)`,
		uuid.NewString(), f.companyID, sourceLineID, targetLineID, allocationType, quantity)
}

func (f *returnFixture) openAmounts() map[string]string {
	f.t.Helper()
	items, err := f.service.ListOpenItems(f.ctx, f.session, f.partyID, "TRY", "RECEIVABLE", 100)
	if err != nil {
		f.t.Fatalf("list open items: %v", err)
	}
	result := map[string]string{}
	for _, item := range items {
		result[item.DocumentNo] = item.OpenAmount
	}
	return result
}

// TestReturnAgainstTheInvoiceItselfDeductsInFull is the regression guard for
// the ordinary case: a return raised straight against one invoice must still
// close it completely, exactly as it did before returns became shared.
func TestReturnAgainstTheInvoiceItselfDeductsInFull(t *testing.T) {
	f := newReturnFixture(t, "direct")
	invoiceID, invoiceLine := f.document("SALES_INVOICE", "SF-1", "2026-01-10", "1000", "10")
	f.openItem(invoiceID, "2026-01-10", "1000.0000")

	returnID, returnLine := f.document("SALES_RETURN_INVOICE", "SI-1", "2026-02-01", "400", "4")
	f.openItem(returnID, "2026-02-01", "400.0000")
	f.source(returnID, invoiceID, "RETURN")
	f.allocate(invoiceLine, returnLine, "RETURN", "4")

	if got := f.openAmounts()["SF-1"]; got != "600.0000" {
		t.Fatalf("open amount after a 400 return on a 1000 invoice = %q, want 600.0000", got)
	}
	settlement, err := f.service.ReadDocumentSettlement(f.ctx, f.companyID, invoiceID)
	if err != nil {
		t.Fatalf("settlement: %v", err)
	}
	if settlement.ReturnedTotal != "400.0000" || settlement.AmountDue != "600.0000" {
		t.Fatalf("settlement returned=%q due=%q, want 400.0000 / 600.0000", settlement.ReturnedTotal, settlement.AmountDue)
	}
}

// TestReturnIsNotDoubleCountedAcrossInvoicesSharingADispatch is the bug this
// whole attribution view exists for. One dispatch of ten units is invoiced by
// two invoices of five units each; a return of five units raised against the
// dispatch used to be deducted in full from BOTH invoices, wiping a 1.000 TL
// receivable with a 500 TL return and reporting both invoices as collected.
func TestReturnIsNotDoubleCountedAcrossInvoicesSharingADispatch(t *testing.T) {
	f := newReturnFixture(t, "shared")
	dispatchID, dispatchLine := f.document("SALES_DELIVERY", "IRS-1", "2026-01-05", "1000", "10")

	firstID, firstLine := f.document("SALES_INVOICE", "SF-1", "2026-01-10", "500", "5")
	f.openItem(firstID, "2026-01-10", "500.0000")
	f.source(firstID, dispatchID, "INVOICING")
	f.allocate(dispatchLine, firstLine, "INVOICING", "5")

	secondID, secondLine := f.document("SALES_INVOICE", "SF-2", "2026-01-20", "500", "5")
	f.openItem(secondID, "2026-01-20", "500.0000")
	f.source(secondID, dispatchID, "INVOICING")
	f.allocate(dispatchLine, secondLine, "INVOICING", "5")

	// The return names the dispatch, not either invoice -- the shape that used
	// to attach it to both.
	returnID, returnLine := f.document("SALES_RETURN_INVOICE", "SI-1", "2026-02-01", "500", "5")
	f.openItem(returnID, "2026-02-01", "500.0000")
	f.source(returnID, dispatchID, "RETURN")
	f.allocate(dispatchLine, returnLine, "RETURN", "5")

	open := f.openAmounts()
	// Half the dispatch line was invoiced on each invoice, so the return splits
	// evenly. What must hold regardless of the split is the total: 1.000 of
	// invoices minus a 500 return leaves 500 open, never 0.
	if open["SF-1"] != "250.0000" || open["SF-2"] != "250.0000" {
		t.Fatalf("open amounts = %v, want 250.0000 on each invoice", open)
	}
	total := mustRat(open["SF-1"])
	total.Add(total, mustRat(open["SF-2"]))
	if amountString(total, 4) != "500.0000" {
		t.Fatalf("total still open = %s, want 500.0000", amountString(total, 4))
	}
}

// TestReturnFollowsTheInvoiceThatActuallyBilledTheDispatchLine covers the
// asymmetric split: when only one of the two invoices billed the returned
// dispatch line, the whole return belongs to that invoice and the other one is
// untouched.
func TestReturnFollowsTheInvoiceThatActuallyBilledTheDispatchLine(t *testing.T) {
	f := newReturnFixture(t, "asymmetric")
	dispatchID, billedLine := f.document("SALES_DELIVERY", "IRS-1", "2026-01-05", "1000", "10")
	// A second dispatch line, billed by the other invoice.
	otherLine := uuid.NewString()
	f.exec(`INSERT INTO commercial_line_registry(company_id,line_id,aggregate_type,document_id,line_no,line_type,quantity,base_quantity) VALUES($1,$2,'SALES_DISPATCH',$3,2,'PRODUCT',10,10)`,
		f.companyID, otherLine, dispatchID)

	firstID, firstLine := f.document("SALES_INVOICE", "SF-1", "2026-01-10", "1000", "10")
	f.openItem(firstID, "2026-01-10", "1000.0000")
	f.source(firstID, dispatchID, "INVOICING")
	f.allocate(billedLine, firstLine, "INVOICING", "10")

	secondID, secondLine := f.document("SALES_INVOICE", "SF-2", "2026-01-20", "1000", "10")
	f.openItem(secondID, "2026-01-20", "1000.0000")
	f.source(secondID, dispatchID, "INVOICING")
	f.allocate(otherLine, secondLine, "INVOICING", "10")

	returnID, returnLine := f.document("SALES_RETURN_INVOICE", "SI-1", "2026-02-01", "300", "3")
	f.openItem(returnID, "2026-02-01", "300.0000")
	f.source(returnID, dispatchID, "RETURN")
	f.allocate(billedLine, returnLine, "RETURN", "3")

	open := f.openAmounts()
	if open["SF-1"] != "700.0000" {
		t.Fatalf("invoice that billed the returned line = %q, want 700.0000", open["SF-1"])
	}
	if open["SF-2"] != "1000.0000" {
		t.Fatalf("invoice that did not bill the returned line = %q, want 1000.0000", open["SF-2"])
	}
}

// TestPartyAgingUsesTheAsOfDate proves the aging report reconstructs the past
// instead of mixing today's state into a backdated column: an invoice raised
// after asOf must not appear, and a return posted after asOf must not reduce
// the balance the report shows.
func TestPartyAgingUsesTheAsOfDate(t *testing.T) {
	f := newReturnFixture(t, "aging")
	invoiceID, invoiceLine := f.document("SALES_INVOICE", "SF-1", "2026-01-10", "1000", "10")
	f.openItem(invoiceID, "2026-01-10", "1000.0000")
	returnID, returnLine := f.document("SALES_RETURN_INVOICE", "SI-1", "2026-03-01", "400", "4")
	f.openItem(returnID, "2026-03-01", "400.0000")
	f.source(returnID, invoiceID, "RETURN")
	f.allocate(invoiceLine, returnLine, "RETURN", "4")
	laterID, _ := f.document("SALES_INVOICE", "SF-2", "2026-04-01", "250", "1")
	f.openItem(laterID, "2026-04-01", "250.0000")

	asOfTotal := func(day string) string {
		t.Helper()
		asOf, err := time.Parse("2006-01-02", day)
		if err != nil {
			t.Fatal(err)
		}
		report, err := f.service.PartyAging(f.ctx, f.session, asOf, f.partyID, "TRY", "RECEIVABLE")
		if err != nil {
			t.Fatalf("aging as of %s: %v", day, err)
		}
		if len(report.Items) == 0 {
			return "0.0000"
		}
		if len(report.Items) != 1 {
			t.Fatalf("aging as of %s returned %d rows, want 1", day, len(report.Items))
		}
		return report.Items[0].Total
	}

	// Before the return and before the second invoice: only the 1.000 invoice.
	if got := asOfTotal("2026-02-01"); got != "1000.0000" {
		t.Fatalf("aging as of 2026-02-01 = %q, want 1000.0000 (return and later invoice are still in the future)", got)
	}
	// After the return, before the second invoice.
	if got := asOfTotal("2026-03-15"); got != "600.0000" {
		t.Fatalf("aging as of 2026-03-15 = %q, want 600.0000", got)
	}
	// Everything posted.
	if got := asOfTotal("2026-05-01"); got != "850.0000" {
		t.Fatalf("aging as of 2026-05-01 = %q, want 850.0000", got)
	}
}

// TestFinanceMovementRejectsAStaleExchangeRate covers the cash/bank posting
// path's rate bound. A rate inside the window still posts; one older than
// maxFinanceRateAgeDays is refused with EXCHANGE_RATE_REQUIRED instead of
// silently converting the movement at a months-old rate.
func TestFinanceMovementRejectsAStaleExchangeRate(t *testing.T) {
	f := newReturnFixture(t, "stalerate")
	accountID := uuid.NewString()
	f.exec(`INSERT INTO finance_accounts(id,company_id,account_type,code,name,currency) VALUES($1,$2,'BANK','USD-1','Döviz Hesabı','USD')`, accountID, f.companyID)

	today := time.Now().UTC()
	fresh := today.AddDate(0, 0, -3).Format("2006-01-02")
	f.exec(`INSERT INTO exchange_rates(company_id,currency_code,rate_date,rate_to_base,source_code) VALUES($1,'USD',$2::date,40,'TCMB')`, f.companyID, fresh)

	movement := func(amount string) error {
		_, err := f.service.PostManualAccountMovement(f.ctx, f.session, AccountMovementInput{
			AccountID: accountID, Direction: "IN", Amount: amount, TransactionDate: today,
			Description: "Döviz girişi", IdempotencyKey: uuid.NewString(),
		}, identity.RequestMeta{TraceID: "stale-rate-test", IdempotencyKey: uuid.NewString()})
		return err
	}
	if err := movement("100"); err != nil {
		t.Fatalf("movement with a three-day-old rate: %v", err)
	}

	// Push the only rate outside the window.
	f.exec(`DELETE FROM exchange_rates WHERE company_id=$1`, f.companyID)
	stale := today.AddDate(0, 0, -maxFinanceRateAgeDays-1).Format("2006-01-02")
	f.exec(`INSERT INTO exchange_rates(company_id,currency_code,rate_date,rate_to_base,source_code) VALUES($1,'USD',$2::date,40,'TCMB')`, f.companyID, stale)
	err := movement("200")
	if err == nil {
		t.Fatal("a movement was accepted on a rate older than the bound")
	}
	if code := ErrorCode(err); code != "EXCHANGE_RATE_REQUIRED" {
		t.Fatalf("error code = %q, want EXCHANGE_RATE_REQUIRED (%v)", code, err)
	}
}

// reverseInvoice runs the finance side of a document cancellation in its own
// transaction, the way the sales/purchasing cancel commands call it.
func (f *returnFixture) reverseInvoice(documentID, reason string) error {
	f.t.Helper()
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		f.t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err = f.service.ReverseInvoiceTx(f.ctx, tx, f.session, documentID, uuid.NewString(), reason); err != nil {
		return err
	}
	return tx.Commit(f.ctx)
}

// TestInvoiceReversalIsBlockedOnlyByAReturnThatReducesIt covers the dependency
// guard on the same attribution the balances use. An invoice carrying a live
// return cannot be reversed; a sibling invoice sharing the dispatch but none of
// the returned quantity can, where the old document-relation rule blocked it
// too. Reversing the return first releases the invoice.
func TestInvoiceReversalIsBlockedOnlyByAReturnThatReducesIt(t *testing.T) {
	f := newReturnFixture(t, "revguard")
	dispatchID, billedLine := f.document("SALES_DELIVERY", "IRS-1", "2026-01-05", "2000", "10")
	otherLine := uuid.NewString()
	f.exec(`INSERT INTO commercial_line_registry(company_id,line_id,aggregate_type,document_id,line_no,line_type,quantity,base_quantity) VALUES($1,$2,'SALES_DISPATCH',$3,2,'PRODUCT',10,10)`,
		f.companyID, otherLine, dispatchID)

	returnedID, returnedLine := f.document("SALES_INVOICE", "SF-1", "2026-01-10", "1000", "10")
	f.openItem(returnedID, "2026-01-10", "1000.0000")
	f.source(returnedID, dispatchID, "INVOICING")
	f.allocate(billedLine, returnedLine, "INVOICING", "10")

	untouchedID, untouchedLine := f.document("SALES_INVOICE", "SF-2", "2026-01-20", "1000", "10")
	f.openItem(untouchedID, "2026-01-20", "1000.0000")
	f.source(untouchedID, dispatchID, "INVOICING")
	f.allocate(otherLine, untouchedLine, "INVOICING", "10")

	returnID, returnLine := f.document("SALES_RETURN_INVOICE", "SI-1", "2026-02-01", "300", "3")
	f.openItem(returnID, "2026-02-01", "300.0000")
	f.source(returnID, dispatchID, "RETURN")
	f.allocate(billedLine, returnLine, "RETURN", "3")

	// The invoice the return actually reduces stays blocked.
	err := f.reverseInvoice(returnedID, "iptal")
	if err == nil {
		t.Fatal("an invoice carrying a live return was reversed")
	}
	if code := ErrorCode(err); code != "DOCUMENT_HAS_DEPENDENCIES" {
		t.Fatalf("error code = %q, want DOCUMENT_HAS_DEPENDENCIES (%v)", code, err)
	}

	// The sibling invoice, which billed none of the returned quantity, is free.
	if err = f.reverseInvoice(untouchedID, "iptal"); err != nil {
		t.Fatalf("sibling invoice untouched by the return could not be reversed: %v", err)
	}

	// Reversing the return releases the invoice it reduced.
	if err = f.reverseInvoice(returnID, "iade iptali"); err != nil {
		t.Fatalf("reverse the return: %v", err)
	}
	if err = f.reverseInvoice(returnedID, "iptal"); err != nil {
		t.Fatalf("invoice could not be reversed after its return was reversed: %v", err)
	}
}

// TestReturnReversalIsNotBlockedByASiblingReturn is the second half of the same
// guard: two returns raised against one dispatch are independent, and
// cancelling the first must not be refused because the second exists.
func TestReturnReversalIsNotBlockedByASiblingReturn(t *testing.T) {
	f := newReturnFixture(t, "revsibling")
	dispatchID, dispatchLine := f.document("SALES_DELIVERY", "IRS-1", "2026-01-05", "1000", "10")
	invoiceID, invoiceLine := f.document("SALES_INVOICE", "SF-1", "2026-01-10", "1000", "10")
	f.openItem(invoiceID, "2026-01-10", "1000.0000")
	f.source(invoiceID, dispatchID, "INVOICING")
	f.allocate(dispatchLine, invoiceLine, "INVOICING", "10")

	firstReturnID, firstReturnLine := f.document("SALES_RETURN_INVOICE", "SI-1", "2026-02-01", "200", "2")
	f.openItem(firstReturnID, "2026-02-01", "200.0000")
	f.source(firstReturnID, dispatchID, "RETURN")
	f.allocate(dispatchLine, firstReturnLine, "RETURN", "2")

	secondReturnID, secondReturnLine := f.document("SALES_RETURN_INVOICE", "SI-2", "2026-02-10", "300", "3")
	f.openItem(secondReturnID, "2026-02-10", "300.0000")
	f.source(secondReturnID, dispatchID, "RETURN")
	f.allocate(dispatchLine, secondReturnLine, "RETURN", "3")

	if got := f.openAmounts()["SF-1"]; got != "500.0000" {
		t.Fatalf("open amount after two returns = %q, want 500.0000", got)
	}
	if err := f.reverseInvoice(firstReturnID, "iade iptali"); err != nil {
		t.Fatalf("a return was blocked by a sibling return: %v", err)
	}
	if got := f.openAmounts()["SF-1"]; got != "700.0000" {
		t.Fatalf("open amount after cancelling the 200 return = %q, want 700.0000", got)
	}
}
