package products

import (
	"errors"
	"testing"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestNormalizeVariantBarcodeReplacementIsStrictAndCanonical(t *testing.T) {
	items, err := normalizeVariantBarcodeReplacement([]BarcodeInput{{Barcode: "  8690001  "}, {Barcode: "X-1", BarcodeType: " other "}})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Barcode != "8690001" || items[0].BarcodeType != "EAN" || !items[0].IsPrimary || items[1].BarcodeType != "OTHER" {
		t.Fatalf("unexpected normalized barcodes: %+v", items)
	}

	for _, input := range [][]BarcodeInput{
		{{Barcode: "   "}},
		{{Barcode: "A"}, {Barcode: " A "}},
		{{Barcode: "A", IsPrimary: true}, {Barcode: "B", IsPrimary: true}},
	} {
		if _, err = normalizeVariantBarcodeReplacement(input); err == nil || !errors.Is(err, identity.ErrValidation) {
			t.Fatalf("expected strict barcode validation for %+v, got %v", input, err)
		}
	}
	if _, err = normalizeVariantBarcodeReplacement([]BarcodeInput{{Barcode: "A"}, {Barcode: " A "}}); ErrorCode(err) != "VARIANT_BARCODE_LIST_DUPLICATE" {
		t.Fatalf("expected payload-local duplicate code, got %v", err)
	}
}

func TestMapVariantBarcodeConstraintHasStableDuplicateConflict(t *testing.T) {
	for _, constraint := range []string{
		"product_barcodes_company_id_barcode_key",
		"product_primary_barcode_unique",
		"product_variant_primary_barcode_unique",
	} {
		t.Run(constraint, func(t *testing.T) {
			err := mapVariantBarcodeConstraint(&pgconn.PgError{Code: "23505", ConstraintName: constraint})
			if ErrorCode(err) != "VARIANT_BARCODE_DUPLICATE" || !errors.Is(err, identity.ErrValidation) {
				t.Fatalf("unexpected duplicate barcode error: %v", err)
			}
			const want = "Bu barkod firmada başka bir ürün veya varyantta kullanılıyor."
			if err.Error() != "VARIANT_BARCODE_DUPLICATE: "+want {
				t.Fatalf("unexpected duplicate barcode message: %v", err)
			}
		})
	}
}

func TestVariantUpdateCodePreservesExistingCodeWhenOmitted(t *testing.T) {
	code, err := variantUpdateCode("  ", "PRODUCT-RED")
	if err != nil || code != "PRODUCT-RED" {
		t.Fatalf("omitted variant code should preserve current identity: %q %v", code, err)
	}
	if _, err = variantUpdateCode("***", "PRODUCT-RED"); ErrorCode(err) != "VARIANT_CODE_INVALID" {
		t.Fatalf("invalid explicit variant code returned %v", err)
	}
}
