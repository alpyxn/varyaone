package finance

import (
	"errors"
	"fmt"
	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/google/uuid"
	"math/big"
	"testing"
	"time"
)

func TestPaymentPlanValidation(t *testing.T) {
	in := PaymentPlanInput{PartyID: uuid.NewString(), Side: "RECEIVABLE", Currency: "TRY", Description: "Plan", OpenItemIDs: []string{uuid.NewString()}, Installments: []PlanInstallmentInput{{"2026-10-01", "33.33"}, {"2026-11-01", "33.33"}, {"2026-12-01", "33.34"}}, IdempotencyKey: uuid.NewString()}
	sum, err := validatePlan(&in)
	if err != nil || amountString(sum, 4) != "100.0000" {
		t.Fatalf("exact total: %v %v", sum, err)
	}
	in.Installments[1].DueDate = "2026-09-01"
	if _, err = validatePlan(&in); !errors.Is(err, identity.ErrValidation) {
		t.Fatal("out of order dates accepted")
	}
}

func TestPaymentPlanSettlementAndHistoricalAging(t *testing.T) {
	f := newReturnFixture(t, "plan_settlement")
	docID, lineID := f.document("SALES_INVOICE", "PLAN-SF-1", "2026-01-10", "1000", "10")
	oi := f.openItem(docID, "2026-01-10", "1000")
	today := time.Now().UTC().Truncate(24 * time.Hour)
	input := PaymentPlanInput{PartyID: f.partyID, Side: "RECEIVABLE", Currency: "TRY", Description: "Üç taksit", OpenItemIDs: []string{oi}, Installments: []PlanInstallmentInput{{today.AddDate(0, 0, -1).Format("2006-01-02"), "300"}, {today.AddDate(0, 1, 0).Format("2006-01-02"), "300"}, {today.AddDate(0, 2, 0).Format("2006-01-02"), "400"}}, IdempotencyKey: uuid.NewString()}
	meta := identity.RequestMeta{TraceID: "plan-test"}
	p, err := f.service.CreatePaymentPlan(f.ctx, f.session, input, meta)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Installments) != 3 || p.Installments[0].Status != "OVERDUE" {
		t.Fatalf("plan: %+v", p)
	}
	same, err := f.service.CreatePaymentPlan(f.ctx, f.session, input, meta)
	if err != nil || same.ID != p.ID {
		t.Fatalf("idempotent retry: %v", err)
	}
	input.IdempotencyKey = uuid.NewString()
	if _, err = f.service.CreatePaymentPlan(f.ctx, f.session, input, meta); !errors.Is(err, identity.ErrConflict) {
		t.Fatalf("duplicate plan: %v", err)
	}
	aging, err := f.service.PartyAging(f.ctx, f.session, today, f.partyID, "TRY", "RECEIVABLE")
	if err != nil {
		t.Fatal(err)
	}
	if len(aging.Items) != 1 || aging.Items[0].Overdue != "300.0000" || aging.Items[0].NotDue != "700.0000" {
		t.Fatalf("scheduled aging: %+v", aging)
	}
	old, err := f.service.PartyAging(f.ctx, f.session, today.AddDate(0, 0, -1), f.partyID, "TRY", "RECEIVABLE")
	if err != nil || old.Items[0].Overdue != "1000.0000" {
		t.Fatalf("historical aging: %+v %v", old, err)
	}
	account := uuid.NewString()
	f.exec(`INSERT INTO finance_accounts(id,company_id,account_type,code,name,currency) VALUES($1,$2,'CASH','PLAN-KASA','Kasa','TRY')`, account, f.companyID)
	payInput := PaymentInput{PartyID: f.partyID, AccountID: account, PaymentMethod: "CASH", Currency: "TRY", Amount: "100", ExchangeRate: "1", TransactionDate: today, IdempotencyKey: uuid.NewString(), PaymentPlanID: p.ID, InstallmentNo: 1, Allocations: []AllocationInput{{OpenItemID: oi, Amount: "100"}}}
	payment, err := f.service.PostCollection(f.ctx, f.session, payInput, meta)
	if err != nil {
		t.Fatal(err)
	}
	again, err := f.service.PostCollection(f.ctx, f.session, payInput, meta)
	if err != nil || again.ID != payment.ID {
		t.Fatalf("payment retry: %v", err)
	}
	p, err = f.service.GetPaymentPlan(f.ctx, f.session, p.ID)
	if err != nil || p.Installments[0].OpenAmount != "200.0000" {
		t.Fatalf("partial payment: %+v %v", p, err)
	}
	if _, err = f.service.ReversePayment(f.ctx, f.session, payment.ID, uuid.NewString(), "Yanlış ödeme", today, meta); err != nil {
		t.Fatal(err)
	}
	p, err = f.service.GetPaymentPlan(f.ctx, f.session, p.ID)
	if err != nil || p.Installments[0].OpenAmount != "300.0000" {
		t.Fatalf("reverse reopens: %+v %v", p, err)
	}
	// A physical return closes an installment without creating a collection.
	ret, retLine := f.document("SALES_RETURN_INVOICE", "PLAN-I-1", today.Format("2006-01-02"), "200", "2")
	f.openItem(ret, today.Format("2006-01-02"), "200")
	f.source(ret, docID, "RETURN")
	f.allocate(lineID, retLine, "RETURN", "2")
	p, err = f.service.GetPaymentPlan(f.ctx, f.session, p.ID)
	if err != nil || p.Installments[0].OpenAmount != "100.0000" {
		t.Fatalf("return closes balance: %+v %v", p, err)
	}
	restricted := f.session
	restricted.Permissions = []string{"finance.payment.read"}
	if _, e := f.service.ListPaymentPlanSummaries(f.ctx, restricted, PaymentPlanListOptions{Side: "RECEIVABLE"}); !errors.Is(e, identity.ErrForbidden) {
		t.Fatalf("summary permission: %v", e)
	}
	if _, err = f.service.GetPaymentPlan(f.ctx, restricted, p.ID); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("side isolation: %v", err)
	}
	other := f.session
	other.CurrentCompanyID = uuid.NewString()
	if _, err = f.service.GetPaymentPlan(f.ctx, other, p.ID); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("company isolation: %v", err)
	}
	if list, e := f.service.ListPaymentPlanSummaries(f.ctx, other, PaymentPlanListOptions{Side: "RECEIVABLE"}); e != nil || len(list.Items) != 0 {
		t.Fatalf("summary company scope: %+v %v", list, e)
	}
	if _, err = f.service.CancelPaymentPlan(f.ctx, f.session, p.ID, p.Version, "Yeni anlaşma", meta); err != nil {
		t.Fatal(err)
	}
	aging, err = f.service.PartyAging(f.ctx, f.session, today, f.partyID, "TRY", "RECEIVABLE")
	if err != nil || aging.Items[0].Overdue != "800.0000" {
		t.Fatalf("cancel restores original due: %+v %v", aging, err)
	}
	input.Installments = []PlanInstallmentInput{{today.AddDate(0, 1, 0).Format("2006-01-02"), "800"}}
	input.IdempotencyKey = uuid.NewString()
	if _, err = f.service.CreatePaymentPlan(f.ctx, f.session, input, meta); err != nil {
		t.Fatalf("replan remaining balance: %v", err)
	}
}

func TestPaymentPlanPayablesMultipleSourcesAndGuards(t *testing.T) {
	f := newReturnFixture(t, "plan_payables")
	f.exec(`UPDATE parties SET is_supplier=true WHERE company_id=$1 AND id=$2`, f.companyID, f.partyID)
	ids := []string{}
	for n, amount := range []string{"400", "600"} {
		doc, ledger, oi := uuid.NewString(), uuid.NewString(), uuid.NewString()
		f.exec(`INSERT INTO documents(id,company_id,document_type_code,document_no,branch_id,party_id,document_date,currency_code,grand_total,status,posted_at,created_by,updated_by) VALUES($1,$2,'PURCHASE_INVOICE',$3,$4,$5,'2026-01-10','TRY',$6,'POSTED',now(),$7,$7)`, doc, f.companyID, fmt.Sprintf("AF-PLAN-%d", n), f.branchID, f.partyID, amount, f.userID)
		f.exec(`INSERT INTO party_ledger_entries(id,company_id,party_id,currency,entry_type,source_type,source_id,idempotency_key,description,debit,credit,document_date,actor_user_id) VALUES($1,$2,$3,'TRY','PAYABLE','document',$4,$5,'Alış faturası',0,$6,'2026-01-10',$7)`, ledger, f.companyID, f.partyID, doc, uuid.NewString(), amount, f.userID)
		f.exec(`INSERT INTO finance_invoice_open_items(id,company_id,document_id,party_id,party_ledger_entry_id,side,currency,original_amount,base_currency,base_amount,document_date,due_date) VALUES($1,$2,$3,$4,$5,'PAYABLE','TRY',$6,'TRY',$6,'2026-01-10','2026-02-10')`, oi, f.companyID, doc, f.partyID, ledger, amount)
		ids = append(ids, oi)
	}
	today := time.Now().UTC()
	date := today.AddDate(0, 1, 0).Format("2006-01-02")
	in := PaymentPlanInput{PartyID: f.partyID, Side: "PAYABLE", Currency: "TRY", Description: "Birleştirilmiş tedarikçi borcu", OpenItemIDs: ids, Installments: []PlanInstallmentInput{{date, "500"}, {today.AddDate(0, 2, 0).Format("2006-01-02"), "500"}}, IdempotencyKey: uuid.NewString()}
	wrong := in
	wrong.Currency = "USD"
	if _, err := f.service.CreatePaymentPlan(f.ctx, f.session, wrong, identity.RequestMeta{}); !errors.Is(err, identity.ErrValidation) {
		t.Fatalf("mixed currency: %v", err)
	}
	wrong = in
	wrong.PartyID = uuid.NewString()
	if _, err := f.service.CreatePaymentPlan(f.ctx, f.session, wrong, identity.RequestMeta{}); !errors.Is(err, identity.ErrValidation) {
		t.Fatalf("mixed party: %v", err)
	}
	wrong = in
	wrong.Installments = []PlanInstallmentInput{{date, "999"}}
	if _, err := f.service.CreatePaymentPlan(f.ctx, f.session, wrong, identity.RequestMeta{}); !errors.Is(err, identity.ErrValidation) {
		t.Fatalf("unbalanced plan: %v", err)
	}
	before := 0
	_ = f.pool.QueryRow(f.ctx, `SELECT count(*) FROM party_ledger_entries WHERE company_id=$1`, f.companyID).Scan(&before)
	p, err := f.service.CreatePaymentPlan(f.ctx, f.session, in, identity.RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	after := 0
	_ = f.pool.QueryRow(f.ctx, `SELECT count(*) FROM party_ledger_entries WHERE company_id=$1`, f.companyID).Scan(&after)
	if before != after {
		t.Fatal("plan created new debt")
	}
	if len(p.Sources) != 2 || len(p.Installments) != 2 {
		t.Fatalf("multi-source plan: %+v", p)
	}
	account := uuid.NewString()
	f.exec(`INSERT INTO finance_accounts(id,company_id,account_type,code,name,currency) VALUES($1,$2,'CASH','PLAN-PAY','Kasa','TRY')`, account, f.companyID)
	f.exec(`INSERT INTO finance_account_movements(id,company_id,account_id,movement_kind,direction,currency,amount,transaction_date,source_type,source_id,idempotency_key,description,exchange_rate,base_currency,base_amount) VALUES($1,$2,$3,'OPENING_BALANCE','IN','TRY',1000,'2026-01-01','finance_account_movement',$1,'plan-opening','Açılış',1,'TRY',1000)`, uuid.NewString(), f.companyID, account)

	pay := PaymentInput{PartyID: f.partyID, AccountID: account, PaymentMethod: "CASH", Currency: "TRY", Amount: "500", ExchangeRate: "1", TransactionDate: today, IdempotencyKey: uuid.NewString(), PaymentPlanID: p.ID, InstallmentNo: 2, Allocations: p.Installments[1].Allocations}
	if _, err = f.service.PostPaymentCommand(f.ctx, f.session, pay, identity.RequestMeta{}); !errors.Is(err, identity.ErrValidation) {
		t.Fatalf("later installment accepted: %v", err)
	}
	pay.InstallmentNo = 1
	pay.Allocations = p.Installments[0].Allocations
	if _, err = f.service.PostPaymentCommand(f.ctx, f.session, pay, identity.RequestMeta{}); err != nil {
		t.Fatal(err)
	}
	p, err = f.service.GetPaymentPlan(f.ctx, f.session, p.ID)
	if err != nil || p.Installments[0].OpenAmount != "0.0000" || p.Installments[1].OpenAmount != "500.0000" {
		t.Fatalf("supplier installment payment: %+v %v", p, err)
	}
	// Branch restrictions must reject the whole multi-source plan.
	branch := uuid.NewString()
	f.exec(`INSERT INTO branches(id,company_id,code,name) VALUES($1,$2,'OTHER-PLAN','Başka şube')`, branch, f.companyID)
	f.exec(`INSERT INTO membership_branch_scopes(company_id,user_id,branch_id) VALUES($1,$2,$3)`, f.companyID, f.userID, branch)
	if list, e := f.service.ListPaymentPlanSummaries(f.ctx, f.session, PaymentPlanListOptions{Side: "PAYABLE"}); e != nil || len(list.Items) != 0 {
		t.Fatalf("summary branch leak: %+v %v", list, e)
	}
	if _, err = f.service.GetPaymentPlan(f.ctx, f.session, p.ID); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("branch leak: %v", err)
	}
}

func TestPaymentPlanConcurrentCreation(t *testing.T) {
	f := newReturnFixture(t, "plan_race")
	doc, _ := f.document("SALES_INVOICE", "SF-RACE", "2026-01-10", "100", "1")
	oi := f.openItem(doc, "2026-01-10", "100")
	start := make(chan struct{})
	done := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			_, err := f.service.CreatePaymentPlan(f.ctx, f.session, PaymentPlanInput{PartyID: f.partyID, Side: "RECEIVABLE", Currency: "TRY", Description: "Tek plan", OpenItemIDs: []string{oi}, Installments: []PlanInstallmentInput{{time.Now().AddDate(0, 1, 0).Format("2006-01-02"), "100"}}, IdempotencyKey: uuid.NewString()}, identity.RequestMeta{})
			done <- err
		}()
	}
	close(start)
	success := 0
	for i := 0; i < 2; i++ {
		if <-done == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("concurrent plans succeeded: %d", success)
	}
	var n int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM finance_payment_plan_sources WHERE company_id=$1 AND open_item_id=$2 AND active`, f.companyID, oi).Scan(&n); err != nil || n != 1 {
		t.Fatalf("active source count %d: %v", n, err)
	}
}

func TestPaymentPlanFIFOUsesInstallmentDatesAndConsolidatesSources(t *testing.T) {
	date := func(value string) *time.Time { v, _ := time.Parse("2006-01-02", value); return &v }
	items := []OpenItem{
		{ID: "planned", OpenAmount: "100", DueDate: date("2026-01-01"), DueSchedule: []OpenItemDue{{DueDate: date("2026-03-01"), OpenAmount: "30"}, {DueDate: date("2026-05-01"), OpenAmount: "70"}}},
		{ID: "ordinary", OpenAmount: "20", DueDate: date("2026-04-01")},
	}
	allocations, err := FIFOAllocations(items, "80")
	if err != nil {
		t.Fatal(err)
	}
	if len(allocations) != 2 || allocations[0].OpenItemID != "planned" || allocations[0].Amount != "60.0000" || allocations[1].OpenItemID != "ordinary" || allocations[1].Amount != "20.0000" {
		t.Fatalf("scheduled FIFO: %+v", allocations)
	}
}

func TestPaymentPlanPrePlanPaymentReversalRetainsResidual(t *testing.T) {
	f := newReturnFixture(t, "plan_residual")
	doc, _ := f.document("SALES_INVOICE", "SF-RESIDUAL", "2026-01-10", "1000", "10")
	oi := f.openItem(doc, "2026-01-10", "1000")
	account := uuid.NewString()
	f.exec(`INSERT INTO finance_accounts(id,company_id,account_type,code,name,currency) VALUES($1,$2,'CASH','PLAN-RESIDUAL','Kasa','TRY')`, account, f.companyID)
	today := time.Now().UTC()
	meta := identity.RequestMeta{TraceID: "plan-residual"}
	pay, err := f.service.PostCollection(f.ctx, f.session, PaymentInput{PartyID: f.partyID, AccountID: account, PaymentMethod: "CASH", Currency: "TRY", Amount: "200", ExchangeRate: "1", TransactionDate: today, IdempotencyKey: uuid.NewString(), Allocations: []AllocationInput{{OpenItemID: oi, Amount: "200"}}}, meta)
	if err != nil {
		t.Fatal(err)
	}
	p, err := f.service.CreatePaymentPlan(f.ctx, f.session, PaymentPlanInput{PartyID: f.partyID, Side: "RECEIVABLE", Currency: "TRY", Description: "Kalan 800", OpenItemIDs: []string{oi}, Installments: []PlanInstallmentInput{{today.AddDate(0, 1, 0).Format("2006-01-02"), "800"}}, IdempotencyKey: uuid.NewString()}, meta)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.ReversePayment(f.ctx, f.session, pay.ID, uuid.NewString(), "Önceki ödeme iptal", today, meta); err != nil {
		t.Fatal(err)
	}
	items, err := f.service.ListOpenItems(f.ctx, f.session, f.partyID, "TRY", "RECEIVABLE", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].OpenAmount != "1000.0000" || len(items[0].DueSchedule) != 2 {
		t.Fatalf("reopened residual: %+v", items)
	}
	total := new(big.Rat)
	for _, due := range items[0].DueSchedule {
		total.Add(total, mustRat(due.OpenAmount))
	}
	if amountString(total, 4) != "1000.0000" {
		t.Fatal("scheduled debt lost or counted twice")
	}
	p, err = f.service.GetPaymentPlan(f.ctx, f.session, p.ID)
	if err != nil || p.Installments[0].OpenAmount != "800.0000" {
		t.Fatalf("plan capped to agreed balance: %+v %v", p, err)
	}
}
