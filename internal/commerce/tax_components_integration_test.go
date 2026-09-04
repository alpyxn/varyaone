package commerce

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/alpyxn/varyaone/internal/platform/migrations"
	"github.com/alpyxn/varyaone/internal/taxes"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestResolveLineDefaultsPricesAdditionalTaxes proves the whole path a product
// card's "diğer vergiler" travels: the card stores an ÖTV component whose rate
// lives in its metadata, the resolver returns it next to the profile's own KDV
// component, and the tax engine charges KDV on the amount ÖTV raised. Before
// this, the resolver dropped the VAT rate whenever a component row existed and
// read the component's rate only from a tax_rates row, so an ÖTV product
// invoiced at plain KDV - or at no tax at all.
func TestResolveLineDefaultsPricesAdditionalTaxes(t *testing.T) {
	databaseURL := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)

	base, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("varya_tax_%d", time.Now().UnixNano())
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

	companyID, productID, partyID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO companies(id,legal_name,trade_name,entity_type,base_currency) VALUES($1,'Vergi Test AŞ','Vergi Test','LEGAL_ENTITY','TRY')`, []any{companyID}},
		{`INSERT INTO parties(id,company_id,code,kind,is_customer,legal_name,display_name,default_currency) VALUES($1,$2,'M-1','ORGANIZATION',true,'Test Müşteri AŞ','Test Müşteri','TRY')`, []any{partyID, companyID}},
		{`INSERT INTO products(id,company_id,code,name,kind,sales_price,purchase_price) VALUES($1,$2,'STK-1','ÖTV''li ürün','PHYSICAL',100,100)`, []any{productID, companyID}},
		{`INSERT INTO product_tax_profiles(company_id,product_id,direction,treatment,tax_code,rate,tax_included) VALUES($1,$2,'SALES','STANDARD','KDV',20,false)`, []any{companyID, productID}},
		// ÖTV carries the rate typed on the card; ÖİV takes the one the seeded
		// company tax catalog holds. Both forms have to price.
		{`INSERT INTO product_tax_profile_components(company_id,product_id,direction,sequence,tax_definition_id,calculation_type,included_in_tax_base,metadata)
		  SELECT $1,$2,'SALES',1,id,'PERCENTAGE',true,'{"rate":"10"}'::jsonb FROM tax_definitions WHERE company_id=$1 AND code='OTV'`, []any{companyID, productID}},
		{`INSERT INTO product_tax_profile_components(company_id,product_id,direction,sequence,tax_definition_id,calculation_type,included_in_tax_base,metadata)
		  SELECT $1,$2,'SALES',2,id,'PERCENTAGE',false,'{}'::jsonb FROM tax_definitions WHERE company_id=$1 AND code='OIV'`, []any{companyID, productID}},
	}
	for _, statement := range statements {
		if _, err = pool.Exec(ctx, statement.query, statement.args...); err != nil {
			t.Fatalf("%s: %v", statement.query, err)
		}
	}

	defaults, err := ResolveLineDefaults(ctx, pool, DocumentContext{
		Direction:    DirectionSales,
		CompanyID:    companyID,
		PartyID:      partyID,
		CurrencyCode: "TRY",
		BaseCurrency: "TRY",
		ExchangeRate: "1",
		DocumentDate: time.Now().Format("2006-01-02"),
	}, LineContext{ProductID: productID, UnitCode: "ADET"}, "")
	if err != nil {
		t.Fatalf("ResolveLineDefaults returned error: %v", err)
	}
	if len(defaults.Components) != 3 {
		t.Fatalf("resolved components = %+v, want KDV + ÖTV + ÖİV", defaults.Components)
	}
	if !defaults.Components[0].Primary || defaults.Components[0].Rate != "20.00000000" {
		t.Fatalf("first component must be the profile's KDV: %+v", defaults.Components[0])
	}
	if defaults.Components[1].Code != "OTV" || defaults.Components[1].Rate != "10" || !defaults.Components[1].IncludedInBase {
		t.Fatalf("ÖTV component = %+v, want the rate typed on the card", defaults.Components[1])
	}
	if defaults.Components[2].Code != "OIV" || defaults.Components[2].Rate != "10.00000000" {
		t.Fatalf("ÖİV component = %+v, want the definition's catalog rate", defaults.Components[2])
	}

	result, err := taxes.Calculate(taxes.TaxCalculationInput{
		Lines:       []taxes.TaxCalculationLine{{UnitPrice: defaults.UnitPrice, Quantity: "1", Components: defaults.Components}},
		RoundScale:  2,
		RoundPolicy: taxes.RoundHalfUp,
	})
	if err != nil {
		t.Fatalf("Calculate returned error: %v", err)
	}
	// ÖTV 10 on 100; KDV %20 and ÖİV %10 on 110 -> 22 + 11; total tax 43.
	if result.TaxAmount != "43" || result.TotalAmount != "143" {
		t.Fatalf("tax = %s, total = %s; want 43 / 143", result.TaxAmount, result.TotalAmount)
	}
}
