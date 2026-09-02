package advance

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/alpyxn/varyaone/internal/finance"
	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/platform/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestEmployeeAdvanceLifecycleIsAtomicIdempotentAndReversible(t *testing.T) {
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
	schema := fmt.Sprintf("varya_employee_advance_%d", time.Now().UnixNano())
	if _, err = base.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
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
	session, err := identityService.Setup(ctx, identity.SetupInput{AdminName: "Avans Yönetici", AdminEmail: "advance@example.test", Password: "uzun-ve-guvenli-parola", LegalName: "Avans AŞ", TradeName: "Avans", EntityType: "LEGAL_ENTITY"}, identity.RequestMeta{TraceID: "advance-test"})
	if err != nil {
		t.Fatal(err)
	}
	istanbul, err := time.LoadLocation("Europe/Istanbul")
	if err != nil {
		t.Fatal(err)
	}
	today := time.Now().In(istanbul).Format("2006-01-02")
	employeeID, employmentID, accountID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	mustExec := func(sql string, args ...any) {
		t.Helper()
		if _, e := pool.Exec(ctx, sql, args...); e != nil {
			t.Fatal(e)
		}
	}
	mustExec(`INSERT INTO employees(id,company_id,employee_code,first_name,last_name) VALUES($1,$2,'P-001','Ada','Yılmaz')`, employeeID, session.CurrentCompanyID)
	mustExec(`INSERT INTO employments(id,company_id,employee_id,start_date) VALUES($1,$2,$3,$4::date)`, employmentID, session.CurrentCompanyID, employeeID, today)
	mustExec(`INSERT INTO finance_accounts(id,company_id,account_type,code,name,currency) VALUES($1,$2,'CASH','KASA-AV','Avans Kasası','TRY')`, accountID, session.CurrentCompanyID)
	svc := NewService(pool, finance.NewService(pool))
	create := CreateInput{EmployeeID: employeeID, Amount: "100.00", AccountID: accountID, Description: "Seyahat avansı", IdempotencyKey: "advance-create-1", OverrideReason: "entegrasyon testi"}
	a, err := svc.Create(ctx, session, create)
	if err != nil {
		t.Fatal(err)
	}
	if a.Status != "OPEN" || a.OutstandingAmount != "100.00" || a.AdvanceDate != today {
		t.Fatalf("unexpected create: %+v", a)
	}
	listed, err := svc.List(ctx, session, ListFilter{EmployeeID: employeeID, Status: "OPEN", Balance: "OPEN"})
	if err != nil || len(listed.Items) != 1 || listed.TotalOutstanding != "100.00" {
		t.Fatalf("list after create: %+v %v", listed, err)
	}
	replay, err := svc.Create(ctx, session, create)
	if err != nil || replay.ID != a.ID {
		t.Fatalf("idempotent replay: %+v %v", replay, err)
	}
	if _, err = svc.Create(ctx, session, func() CreateInput { x := create; x.Amount = "101.00"; return x }()); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("payload conflict=%v", err)
	}
	var movements int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM finance_account_movements WHERE company_id=$1 AND source_type='employee_advance_transaction'`, session.CurrentCompanyID).Scan(&movements); err != nil || movements != 1 {
		t.Fatalf("movement count=%d err=%v", movements, err)
	}
	a, err = svc.Repay(ctx, session, a.ID, RepaymentInput{Amount: "40.00", TransactionDate: today, AccountID: accountID, IdempotencyKey: "advance-repay-1"})
	if err != nil || a.OutstandingAmount != "60.00" {
		t.Fatalf("partial repayment: %+v %v", a, err)
	}
	if _, err = svc.Repay(ctx, session, a.ID, RepaymentInput{Amount: "60.01", TransactionDate: today, AccountID: accountID, IdempotencyKey: "advance-repay-too-much"}); !errors.Is(err, ErrExceedsBalance) {
		t.Fatalf("overpayment=%v", err)
	}
	a, err = svc.Repay(ctx, session, a.ID, RepaymentInput{Amount: "60.00", TransactionDate: today, AccountID: accountID, IdempotencyKey: "advance-repay-2"})
	if err != nil || a.Status != "CLOSED" {
		t.Fatalf("close: %+v %v", a, err)
	}
	if _, err = svc.Repay(ctx, session, a.ID, RepaymentInput{Amount: "1.00", TransactionDate: today, AccountID: accountID, IdempotencyKey: "advance-repay-closed"}); !errors.Is(err, ErrClosed) {
		t.Fatalf("second close=%v", err)
	}
	if _, err = svc.Reverse(ctx, session, a.Transactions[0].ID, ReverseInput{TransactionDate: today, Reason: "hatalı", IdempotencyKey: "advance-reverse-dependent"}); !errors.Is(err, ErrHasDependencies) {
		t.Fatalf("dependent disbursement reverse=%v", err)
	}
	b, err := svc.Create(ctx, session, CreateInput{EmployeeID: employeeID, Amount: "50.00", AccountID: accountID, Description: "Ekipman avansı", IdempotencyKey: "advance-create-2", OverrideReason: "entegrasyon testi"})
	if err != nil {
		t.Fatal(err)
	}
	before := movements
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM finance_account_movements WHERE company_id=$1 AND source_type='employee_advance_transaction'`, session.CurrentCompanyID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	b, err = svc.WriteOff(ctx, session, b.ID, WriteOffInput{TransactionDate: today, Reason: "Yönetim kararı", IdempotencyKey: "advance-writeoff"})
	if err != nil || b.Status != "WRITTEN_OFF" || !b.RequiresAccountingTaxReview {
		t.Fatalf("writeoff: %+v %v", b, err)
	}
	var after int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM finance_account_movements WHERE company_id=$1 AND source_type='employee_advance_transaction'`, session.CurrentCompanyID).Scan(&after); err != nil || after != before {
		t.Fatalf("writeoff created cash movement: before=%d after=%d err=%v", before, after, err)
	}
	writeOffID := b.Transactions[len(b.Transactions)-1].ID
	b, err = svc.Reverse(ctx, session, writeOffID, ReverseInput{TransactionDate: today, Reason: "Karar iptal", IdempotencyKey: "advance-reverse-writeoff"})
	if err != nil || b.Status != "OPEN" || b.OutstandingAmount != "50.00" {
		t.Fatalf("reverse writeoff: %+v %v", b, err)
	}
}
