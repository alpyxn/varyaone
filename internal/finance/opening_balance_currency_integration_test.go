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

// TestForeignCurrencyOpeningBalanceNeedsARate pins what the account form has to
// tell the user: a foreign-currency account's opening balance posts normally
// once the company has a rate for the movement date, and is refused with
// EXCHANGE_RATE_REQUIRED (never silently dropped) when it does not.
func TestForeignCurrencyOpeningBalanceNeedsARate(t *testing.T) {
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
	schema := fmt.Sprintf("varya_fin_opening_%d", time.Now().UnixNano())
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
	session, err := identityService.Setup(ctx, identity.SetupInput{
		AdminName: "Kasa Yönetici", AdminEmail: "opening@example.test", Password: "uzun-ve-guvenli-parola",
		LegalName: "Açılış AŞ", TradeName: "Açılış", EntityType: "LEGAL_ENTITY",
	}, identity.RequestMeta{TraceID: "opening-test"})
	if err != nil {
		t.Fatal(err)
	}

	svc := NewService(pool)
	today := time.Now().UTC()
	meta := func() identity.RequestMeta {
		return identity.RequestMeta{TraceID: "opening-test", IdempotencyKey: uuid.NewString()}
	}
	newAccount := func(code, currency string) Account {
		t.Helper()
		account, createErr := svc.CreateAccount(ctx, session, AccountInput{
			AccountType: "CASH", Code: code, Name: code, Currency: currency,
		}, meta())
		if createErr != nil {
			t.Fatalf("create %s account: %v", currency, createErr)
		}
		return account
	}
	postOpening := func(accountID string) (AccountMovement, error) {
		return svc.PostOpeningBalance(ctx, session, AccountMovementInput{
			AccountID: accountID, Direction: "IN", Amount: "1000", TransactionDate: today,
			Description: "Açılış bakiyesi", IdempotencyKey: uuid.NewString(),
		}, meta())
	}

	// Base currency: no rate needed.
	if _, err = postOpening(newAccount("KASA-TRY", "TRY").ID); err != nil {
		t.Fatalf("base currency opening balance: %v", err)
	}

	// Foreign currency without a rate: refused, and loudly.
	noRate := newAccount("KASA-USD-1", "USD")
	if _, err = postOpening(noRate.ID); !errors.Is(err, ErrExchangeRateRequired) {
		t.Fatalf("expected ErrExchangeRateRequired without a rate, got %v", err)
	}

	// Same account once the company has a rate for the date.
	if _, err = pool.Exec(ctx, `INSERT INTO exchange_rates(company_id,currency_code,rate_date,rate_to_base,source_code) VALUES($1,'USD',$2::date,48.2943,'MANUAL')`,
		session.CurrentCompanyID, today.Format("2006-01-02")); err != nil {
		t.Fatal(err)
	}
	movement, err := postOpening(noRate.ID)
	if err != nil {
		t.Fatalf("foreign currency opening balance with a rate: %v", err)
	}
	if movement.Currency != "USD" || movement.Amount != "1000.0000" {
		t.Fatalf("movement kept the wrong figures: %+v", movement)
	}
	if movement.BaseCurrency == nil || *movement.BaseCurrency != "TRY" || movement.BaseAmount == nil || *movement.BaseAmount != "48294.3000" {
		t.Fatalf("base amount = %v %v, want 48294.3000 TRY", derefOrNil(movement.BaseAmount), derefOrNil(movement.BaseCurrency))
	}

	// A tahsilat posted with the rate exactly as the exchange-rate screen
	// publishes it must be accepted; the raw numeric(38,18) text used to be
	// refused with "kur geçersiz".
	var storedRate string
	if err = pool.QueryRow(ctx, `SELECT rate_to_base::text FROM exchange_rates WHERE company_id=$1 AND currency_code='USD'`, session.CurrentCompanyID).Scan(&storedRate); err != nil {
		t.Fatal(err)
	}
	partyID := uuid.NewString()
	if _, err = pool.Exec(ctx, `INSERT INTO parties(id,company_id,code,kind,is_customer,is_supplier,display_name,legal_name,default_currency) VALUES($1,$2,'CARI-USD','ORGANIZATION',true,false,'USD Müşteri','USD Müşteri AŞ','USD')`, partyID, session.CurrentCompanyID); err != nil {
		t.Fatal(err)
	}
	collection, err := svc.PostCollection(ctx, session, PaymentInput{
		PartyID:         partyID,
		AccountID:       noRate.ID,
		PaymentMethod:   "CASH",
		Currency:        "USD",
		Amount:          "100",
		ExchangeRate:    storedRate,
		Description:     "USD tahsilat",
		TransactionDate: today,
		IdempotencyKey:  uuid.NewString(),
	}, meta())
	if err != nil {
		t.Fatalf("collection with the stored rate text %q: %v", storedRate, err)
	}
	if collection.ExchangeRate != "48.2943000000" {
		t.Fatalf("collection rate = %q, want 48.2943000000", collection.ExchangeRate)
	}
}

func derefOrNil(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}
