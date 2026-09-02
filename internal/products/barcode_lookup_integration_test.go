package products

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/platform/migrations"
	"github.com/google/uuid"
)

// TestResolveBarcodeMatchesProductAndVariant proves the resolver a
// commercial document's line entry would use returns exactly the same
// product/variant a scan resolves to elsewhere in the system (product_barcodes
// is unique per company, so there is exactly one answer), and that a barcode
// nobody registered comes back as ErrBarcodeNotFound rather than an empty
// zero-value match a caller could mistake for "resolved to nothing in
// particular".
func TestResolveBarcodeMatchesProductAndVariant(t *testing.T) {
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
		AdminName: "Barkod Çözümleyici", AdminEmail: fmt.Sprintf("barcode-lookup-%d@example.test", time.Now().UnixNano()), Password: "uzun-ve-guvenli-parola",
		LegalName: "Barkod AŞ", TradeName: "Barkod", EntityType: "LEGAL_ENTITY",
	}, identity.RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(p)

	product, err := service.Create(ctx, session, Input{Code: "SCAN-TEST", Name: "Taranan Ürün", Kind: "PHYSICAL", BaseUnit: "ADET", Units: []UnitInput{{Code: "ADET", IsBase: true, ConversionFactor: "1"}}}, Scope{}, identity.RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.Exec(ctx, `INSERT INTO product_barcodes(id,company_id,product_id,barcode,barcode_type,is_primary) VALUES($1,$2,$3,'8690000000015','EAN',true)`, uuid.NewString(), session.CurrentCompanyID, product.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = p.Exec(ctx, `UPDATE products SET variants_enabled=true WHERE company_id=$1 AND id=$2`, session.CurrentCompanyID, product.ID); err != nil {
		t.Fatal(err)
	}
	variantID := uuid.NewString()
	if _, err = p.Exec(ctx, `INSERT INTO product_variants(id,company_id,product_id,variant_code,variant_signature) VALUES($1,$2,$3,'SCAN-TEST-V1',$4)`, variantID, session.CurrentCompanyID, product.ID, "MANUAL:"+variantID); err != nil {
		t.Fatal(err)
	}
	if _, err = p.Exec(ctx, `INSERT INTO product_barcodes(id,company_id,product_id,variant_id,barcode,barcode_type,is_primary) VALUES($1,$2,$3,$4,'8690000000022','EAN',true)`, uuid.NewString(), session.CurrentCompanyID, product.ID, variantID); err != nil {
		t.Fatal(err)
	}

	t.Run("plain product barcode", func(t *testing.T) {
		match, err := service.ResolveBarcode(ctx, session, "8690000000015")
		if err != nil {
			t.Fatal(err)
		}
		if match.ProductID != product.ID || match.VariantID != nil {
			t.Fatalf("match = %+v, want product %s with no variant", match, product.ID)
		}
		if match.UnitCode != "ADET" {
			t.Fatalf("unit code = %s, want ADET", match.UnitCode)
		}
	})

	t.Run("variant barcode resolves the variant", func(t *testing.T) {
		match, err := service.ResolveBarcode(ctx, session, "8690000000022")
		if err != nil {
			t.Fatal(err)
		}
		if match.ProductID != product.ID || match.VariantID == nil || *match.VariantID != variantID {
			t.Fatalf("match = %+v, want product %s variant %s", match, product.ID, variantID)
		}
		if match.VariantActive == nil || !*match.VariantActive {
			t.Fatalf("variant_active = %v, want true", match.VariantActive)
		}
	})

	t.Run("unregistered barcode is not found", func(t *testing.T) {
		if _, err := service.ResolveBarcode(ctx, session, "0000000000000"); !errors.Is(err, ErrBarcodeNotFound) {
			t.Fatalf("error = %v, want ErrBarcodeNotFound", err)
		}
	})
}
