package finance

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/platform/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestExchangeRateGuardRejectsFatFingerRate proves a foreign-currency payment
// whose user-supplied rate deviates far from the stored reference rate is
// rejected (a 1.0 GBP rate into a TRY-base company once silently booked ~655
// TRY as 10 TRY), while a rate close to the reference is accepted and a
// company with no reference rate for that currency is left unblocked.
func TestExchangeRateGuardRejectsFatFingerRate(t *testing.T) {
	databaseURL := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)

	base, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("varya_fx_guard_%d", time.Now().UnixNano())
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

	identityService, err := identity.NewService(pool, bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	session, err := identityService.Setup(ctx, identity.SetupInput{AdminName: "Kur Yönetici", AdminEmail: "fx-guard@example.test", Password: "uzun-ve-guvenli-parola", LegalName: "Kur Guard AŞ", TradeName: "Kur Guard", EntityType: "LEGAL_ENTITY"}, identity.RequestMeta{TraceID: "fx-guard-test"})
	if err != nil {
		t.Fatal(err)
	}
	companyID := session.CurrentCompanyID

	mustExec := func(sql string, args ...any) {
		t.Helper()
		if _, execErr := pool.Exec(ctx, sql, args...); execErr != nil {
			t.Fatalf("fixture: %v", execErr)
		}
	}

	partyID := uuid.NewString()
	mustExec(`INSERT INTO parties(id,company_id,code,kind,is_customer,is_supplier,display_name,legal_name,default_currency) VALUES($1,$2,'CARI-FX','ORGANIZATION',true,false,'FX Müşteri','FX Müşteri AŞ','GBP')`, partyID, companyID)
	accountID := uuid.NewString()
	mustExec(`INSERT INTO finance_accounts(id,company_id,account_type,code,name,currency) VALUES($1,$2,'CASH','KASA-GBP','GBP Kasa','GBP')`, accountID, companyID)
	// Reference rate: 1 GBP = 80 TRY on and after 2026-06-01.
	mustExec(`INSERT INTO exchange_rates(company_id,currency_code,rate_date,rate_to_base,source_code) VALUES($1,'GBP','2026-06-01',80,'MANUAL')`, companyID)

	svc := NewService(pool)
	txDate := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	meta := func() identity.RequestMeta {
		return identity.RequestMeta{TraceID: "fx-guard-test", IdempotencyKey: uuid.NewString()}
	}
	collect := func(rate string) error {
		_, postErr := svc.PostCollection(ctx, session, PaymentInput{
			PartyID: partyID, AccountID: accountID, PaymentMethod: "CASH", Currency: "GBP",
			Amount: "10", ExchangeRate: rate, Description: "GBP tahsilat", TransactionDate: txDate,
			IdempotencyKey: uuid.NewString(),
		}, meta())
		return postErr
	}

	if err = collect("1"); err == nil || !errors.Is(err, identity.ErrValidation) {
		t.Fatalf("rate 1.0 for GBP should be rejected as far from the 80 reference, got: %v", err)
	}
	if err = collect("78"); err != nil {
		t.Fatalf("rate 78 (within ±20%% of 80) should be accepted, got: %v", err)
	}

	// A currency with no reference rate stays unblocked (the caller still
	// requires *a* rate for foreign currency).
	usdAccount := uuid.NewString()
	mustExec(`INSERT INTO finance_accounts(id,company_id,account_type,code,name,currency) VALUES($1,$2,'CASH','KASA-USD','USD Kasa','USD')`, usdAccount, companyID)
	if _, err = svc.PostCollection(ctx, session, PaymentInput{
		PartyID: partyID, AccountID: usdAccount, PaymentMethod: "CASH", Currency: "USD",
		Amount: "10", ExchangeRate: "1", Description: "USD tahsilat", TransactionDate: txDate,
		IdempotencyKey: uuid.NewString(),
	}, meta()); err != nil {
		t.Fatalf("USD collection with no reference rate should be accepted, got: %v", err)
	}
}
