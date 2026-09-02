package pricing

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
	"github.com/alpyxn/varyaone/internal/taxes"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPricingAndTaxPeriodsAreScopedAndNonOverlapping(t *testing.T) {
	databaseURL := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool := pricingTestPool(t, ctx, databaseURL)
	if err := migrations.New(pool).Up(ctx); err != nil {
		t.Fatal(err)
	}
	identityService, err := identity.NewService(pool, bytes.Repeat([]byte{8}, 32))
	if err != nil {
		t.Fatal(err)
	}
	session, err := identityService.Setup(ctx, identity.SetupInput{
		AdminName: "Fiyat Yönetici", AdminEmail: "pricing@example.test", Password: "uzun-ve-guvenli-parola",
		LegalName: "Fiyat Test AŞ", TradeName: "Fiyat Test", EntityType: "LEGAL_ENTITY",
	}, identity.RequestMeta{TraceID: "pricing-test"})
	if err != nil {
		t.Fatal(err)
	}
	pricingService := NewService(pool)
	currencies, err := pricingService.ListCurrencies(ctx, session, false)
	if err != nil || len(currencies) != 4 {
		t.Fatalf("default currencies=%+v err=%v", currencies, err)
	}

	priceList, err := pricingService.CreatePriceList(ctx, session, PriceList{
		Code: "STD", Name: "Standart", CurrencyCode: "TRY", TaxMode: TaxExclusive,
		RoundPolicy: RoundHalfUp, RoundScale: 2,
	}, identity.RequestMeta{TraceID: "price-list"})
	if err != nil {
		t.Fatal(err)
	}
	categoryID := uuid.NewString()
	if _, err = pool.Exec(ctx, `INSERT INTO product_categories(id,company_id,code,name) VALUES($1,$2,'CAT-TEST','Test kategori')`, categoryID, session.CurrentCompanyID); err != nil {
		t.Fatal(err)
	}
	scopedList, err := pricingService.CreatePriceList(ctx, session, PriceList{
		Code: "CAT", Name: "Kategori fiyatı", CurrencyCode: "TRY", TaxMode: TaxExclusive,
		RoundPolicy: RoundHalfUp, RoundScale: 2, AppliesToAllCategories: false, ScopeCategoryID: &categoryID,
	}, identity.RequestMeta{TraceID: "category-price-list"})
	if err != nil {
		t.Fatal(err)
	}
	if scopedList.AppliesToAllCategories || scopedList.ScopeCategoryID == nil || *scopedList.ScopeCategoryID != categoryID {
		t.Fatalf("category scope was not persisted: %+v", scopedList)
	}
	itemID := uuid.NewString()
	if _, err = pool.Exec(ctx, `INSERT INTO products(id,company_id,code,name,kind,variants_enabled) VALUES($1,$2,'PRC-TEST','Test ürün','PHYSICAL',true)`, itemID, session.CurrentCompanyID); err != nil {
		t.Fatal(err)
	}
	entry, err := pricingService.CreatePriceEntry(ctx, session, PriceEntry{
		PriceListID: priceList.ID, ItemID: itemID, ValidFrom: "2026-01-01", ValidTo: stringPointer("2026-12-31"), UnitPrice: "123.456789",
	}, identity.RequestMeta{TraceID: "price-entry"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pricingService.CreatePriceEntry(ctx, session, PriceEntry{
		PriceListID: priceList.ID, ItemID: itemID, ValidFrom: "2026-06-01", UnitPrice: "125",
	}, identity.RequestMeta{}); !errors.Is(err, ErrOverlap) {
		t.Fatalf("overlapping price entry error=%v", err)
	}
	variantID := uuid.NewString()
	if _, err = pool.Exec(ctx, `INSERT INTO product_variants(id,company_id,product_id,variant_code,variant_signature,is_active) VALUES($1,$2,$3,'PRC-TEST-BLUE-M','MANUAL:PRC-TEST-BLUE-M',true)`, variantID, session.CurrentCompanyID, itemID); err != nil {
		t.Fatal(err)
	}
	resolved, err := pricingService.ResolvePrice(ctx, session, priceList.ID, itemID, variantID, "2026-06-01")
	if err != nil || resolved.ID != entry.ID || resolved.UnitPrice != "123.456789" {
		t.Fatalf("resolved price=%+v err=%v", resolved, err)
	}
	variantEntry, err := pricingService.CreatePriceEntry(ctx, session, PriceEntry{
		PriceListID: priceList.ID, ItemID: itemID, VariantID: &variantID, ValidFrom: "2026-06-01", UnitPrice: "145.50",
	}, identity.RequestMeta{TraceID: "variant-price-entry"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err = pricingService.ResolvePrice(ctx, session, priceList.ID, itemID, variantID, "2026-06-01")
	if err != nil || resolved.ID != variantEntry.ID || resolved.VariantID == nil || *resolved.VariantID != variantID || resolved.UnitPrice != "145.5" {
		t.Fatalf("variant price did not override parent: %+v err=%v", resolved, err)
	}

	taxService := taxes.NewService(pool)
	definition, err := taxService.CreateDefinition(ctx, session, taxes.TaxDefinition{
		Code: "TEST-TAX", Name: "Test vergi", Source: "integration-test", SourceVersion: "1",
	}, identity.RequestMeta{TraceID: "tax-definition"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = taxService.CreateRate(ctx, session, taxes.TaxRate{
		TaxDefinitionID: definition.ID, Rate: "18", ValidFrom: "2026-01-01", ValidTo: stringPointer("2026-12-31"), Source: "integration-test", SourceVersion: "1",
	}, identity.RequestMeta{TraceID: "tax-rate"}); err != nil {
		t.Fatal(err)
	}
	if _, err = taxService.CreateRate(ctx, session, taxes.TaxRate{
		TaxDefinitionID: definition.ID, Rate: "20", ValidFrom: "2026-06-01", Source: "integration-test", SourceVersion: "2",
	}, identity.RequestMeta{}); !errors.Is(err, taxes.ErrRateOverlap) {
		t.Fatalf("overlapping tax rate error=%v", err)
	}

	foreign := session
	foreign.CurrentCompanyID = uuid.NewString()
	foreignLists, err := pricingService.ListPriceLists(ctx, foreign, false)
	if err != nil || len(foreignLists) != 0 {
		t.Fatalf("price list crossed company boundary: items=%+v err=%v", foreignLists, err)
	}
}

func stringPointer(value string) *string { return &value }

func pricingTestPool(t *testing.T, ctx context.Context, databaseURL string) *pgxpool.Pool {
	t.Helper()
	base, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("varya_test_%d", time.Now().UnixNano())
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
