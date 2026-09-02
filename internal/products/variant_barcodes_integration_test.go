package products

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/platform/migrations"
	"github.com/google/uuid"
)

func TestVariantBarcodeReplacementAtomicConcurrencyAndSelfUpdate(t *testing.T) {
	databaseURL := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	p := productTestPool(t, ctx, databaseURL)
	if err := migrations.New(p).Up(ctx); err != nil {
		t.Fatal(err)
	}
	identityService, err := identity.NewService(p, []byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatal(err)
	}
	session, err := identityService.Setup(ctx, identity.SetupInput{
		AdminName: "Varyant Barkod Yönetici", AdminEmail: fmt.Sprintf("variant-barcode-%d@example.test", time.Now().UnixNano()), Password: "uzun-ve-guvenli-parola",
		LegalName: "Varyant Barkod AŞ", TradeName: "Varyant Barkod", EntityType: "LEGAL_ENTITY",
	}, identity.RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(p)

	product, err := service.Create(ctx, session, Input{Code: "BARCODE-TEST", Name: "Barkod Test", Kind: "PHYSICAL", BaseUnit: "ADET", Units: []UnitInput{{Code: "ADET", IsBase: true, ConversionFactor: "1"}}}, Scope{}, identity.RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.Exec(ctx, `UPDATE products SET variants_enabled=true WHERE company_id=$1 AND id=$2`, session.CurrentCompanyID, product.ID); err != nil {
		t.Fatal(err)
	}
	variantID := uuid.NewString()
	if _, err = p.Exec(ctx, `INSERT INTO product_variants(id,company_id,product_id,variant_code,variant_signature) VALUES($1,$2,$3,'BARCODE-TEST-V1',$4)`, variantID, session.CurrentCompanyID, product.ID, "MANUAL:"+variantID); err != nil {
		t.Fatal(err)
	}
	if _, err = p.Exec(ctx, `INSERT INTO product_barcodes(id,company_id,product_id,variant_id,barcode,barcode_type,is_primary) VALUES($1,$2,$3,$4,'OLD-1','EAN',true)`, uuid.NewString(), session.CurrentCompanyID, product.ID, variantID); err != nil {
		t.Fatal(err)
	}
	productDetail, err := service.Get(ctx, session, product.ID, Scope{})
	if err != nil {
		t.Fatal(err)
	}
	if len(productDetail.Barcodes) != 0 {
		t.Fatalf("variant barcode leaked into product barcode collection: %+v", productDetail.Barcodes)
	}

	current, err := service.getVariant(ctx, session.CurrentCompanyID, variantID)
	if err != nil {
		t.Fatal(err)
	}
	updatedBarcodes, err := service.ReplaceVariantBarcodes(ctx, session, product.ID, variantID, current.Version, VariantBarcodeReplacementInput{Barcodes: []BarcodeInput{{Barcode: "NEW-1", IsPrimary: true}, {Barcode: "NEW-2"}}}, identity.RequestMeta{})
	if err != nil || len(updatedBarcodes.Barcodes) != 2 {
		t.Fatalf("multiple variant barcodes could not be saved: %+v err=%v", updatedBarcodes.Barcodes, err)
	}
	productDetail, err = service.Get(ctx, session, product.ID, Scope{})
	if err != nil || len(productDetail.Barcodes) != 0 {
		t.Fatalf("variant barcodes leaked into product after replacement: %+v err=%v", productDetail.Barcodes, err)
	}
	current = updatedBarcodes
	_, err = service.ReplaceVariantBarcodes(ctx, session, product.ID, variantID, current.Version, VariantBarcodeReplacementInput{Barcodes: []BarcodeInput{{Barcode: "NEW-1"}, {Barcode: "  "}}}, identity.RequestMeta{})
	if ErrorCode(err) != "VARIANT_BARCODE_REQUIRED" {
		t.Fatalf("invalid replacement returned %v", err)
	}
	afterInvalid, err := service.getVariant(ctx, session.CurrentCompanyID, variantID)
	if err != nil || afterInvalid.Version != current.Version || len(afterInvalid.Barcodes) != 2 || afterInvalid.Barcodes[0].Barcode != "NEW-1" || afterInvalid.Barcodes[1].Barcode != "NEW-2" {
		t.Fatalf("invalid replacement was not rolled back: %+v err=%v", afterInvalid, err)
	}

	other, err := service.Create(ctx, session, Input{Code: "BARCODE-OTHER", Name: "Diğer Barkod", Kind: "PHYSICAL", BaseUnit: "ADET", Units: []UnitInput{{Code: "ADET", IsBase: true, ConversionFactor: "1"}}}, Scope{}, identity.RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.Exec(ctx, `INSERT INTO product_barcodes(id,company_id,product_id,barcode,barcode_type,is_primary) VALUES($1,$2,$3,'TAKEN-1','EAN',true)`, uuid.NewString(), session.CurrentCompanyID, other.ID); err != nil {
		t.Fatal(err)
	}
	_, err = service.ReplaceVariantBarcodes(ctx, session, product.ID, variantID, current.Version, VariantBarcodeReplacementInput{Barcodes: []BarcodeInput{{Barcode: "TAKEN-1"}}}, identity.RequestMeta{})
	if ErrorCode(err) != "VARIANT_BARCODE_DUPLICATE" || !strings.Contains(err.Error(), `"TAKEN-1"`) {
		t.Fatalf("company duplicate returned %v", err)
	}

	price := "12.5"
	updated, err := service.UpdateVariant(ctx, session, product.ID, variantID, current.Version, VariantInput{PurchasePriceOverride: &price}, identity.RequestMeta{})
	if err != nil || updated.VariantCode != "BARCODE-TEST-V1" || updated.PurchasePriceOverride != price {
		t.Fatalf("self-update with omitted code did not preserve SKU or price: %+v err=%v", updated, err)
	}

	start := updated.Version
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, barcode := range []string{"CONCURRENT-A", "CONCURRENT-B"} {
		wg.Add(1)
		go func(barcode string) {
			defer wg.Done()
			_, callErr := service.ReplaceVariantBarcodes(ctx, session, product.ID, variantID, start, VariantBarcodeReplacementInput{Barcodes: []BarcodeInput{{Barcode: barcode}}}, identity.RequestMeta{})
			results <- callErr
		}(barcode)
	}
	wg.Wait()
	close(results)
	var succeeded, conflicted int
	for callErr := range results {
		if callErr == nil {
			succeeded++
		} else if errors.Is(callErr, identity.ErrConflict) {
			conflicted++
		} else {
			t.Fatalf("concurrent replacement returned unexpected error: %v", callErr)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("expected one successful and one stale concurrent replacement, got success=%d conflict=%d", succeeded, conflicted)
	}
}

func TestVariantModeRejectsExistingProductBarcodes(t *testing.T) {
	databaseURL := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	p := productTestPool(t, ctx, databaseURL)
	if err := migrations.New(p).Up(ctx); err != nil {
		t.Fatal(err)
	}
	identityService, err := identity.NewService(p, []byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatal(err)
	}
	session, err := identityService.Setup(ctx, identity.SetupInput{
		AdminName: "Varyant Geçiş Yönetici", AdminEmail: fmt.Sprintf("variant-mode-%d@example.test", time.Now().UnixNano()), Password: "uzun-ve-guvenli-parola",
		LegalName: "Varyant Geçiş AŞ", TradeName: "Varyant Geçiş", EntityType: "LEGAL_ENTITY",
	}, identity.RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(p)
	product, err := service.Create(ctx, session, Input{
		Code: "BARCODE-MODE-TEST", Name: "Barkodlu Geçiş", Kind: "PHYSICAL", BaseUnit: "ADET",
		Units:    []UnitInput{{Code: "ADET", IsBase: true, ConversionFactor: "1"}},
		Barcodes: []BarcodeInput{{Barcode: "MODE-OLD-1", IsPrimary: true}},
	}, Scope{}, identity.RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	_, err = service.UpdateVariantConfig(ctx, session, product.ID, ProductVariantConfigInput{VariantsEnabled: &enabled}, product.Version, identity.RequestMeta{})
	if ErrorCode(err) != "VARIANT_MODE_REQUIRES_EMPTY_PRODUCT_BARCODES" {
		t.Fatalf("expected product barcode transition guard, got %v", err)
	}
}
