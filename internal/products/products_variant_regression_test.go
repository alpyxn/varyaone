package products

import (
	"errors"
	"strings"
	"testing"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestNormalizeInputRejectsExplicitCodeThatNormalizesEmpty(t *testing.T) {
	_, _, _, err := normalizeInput(Input{
		SKU:   "***",
		Name:  "Geçersiz kod",
		Kind:  "PHYSICAL",
		Units: []UnitInput{{Code: "ADET", IsBase: true, ConversionFactor: "1"}},
	})
	if !errors.Is(err, identity.ErrValidation) {
		t.Fatalf("expected validation error, got %v", err)
	}
}

func TestCreateVariantCodeRejectsExplicitCodeThatNormalizesEmpty(t *testing.T) {
	_, err := createVariantCode("***", "STK001", []VariantValue{{OptionShortCode: "KRM"}})
	if ErrorCode(err) != "VARIANT_CODE_INVALID" || !errors.Is(err, identity.ErrValidation) {
		t.Fatalf("expected VARIANT_CODE_INVALID validation error, got %v", err)
	}
	code, err := createVariantCode("", "STK001", []VariantValue{{OptionShortCode: "KRM"}})
	if err != nil || code != "STK001-KRM" {
		t.Fatalf("expected generated code STK001-KRM, got code=%q err=%v", code, err)
	}
}

func TestVariantConfigInputChangedTreatsOptionOrderAsUnchanged(t *testing.T) {
	current := ProductVariantConfig{Definitions: []ProductVariantDefinition{{
		DefinitionID: "color",
		Position:     1,
		Options:      []VariantOption{{ID: "red"}, {ID: "blue"}},
	}}}
	if variantConfigInputChanged(current, []ProductVariantDefinitionInput{{
		DefinitionID: "color",
		OptionIDs:    []string{"blue", "red"},
	}}) {
		t.Fatal("reordering the same options should not change the configuration")
	}
	if !variantConfigInputChanged(current, []ProductVariantDefinitionInput{{
		DefinitionID: "color",
		OptionIDs:    []string{"red"},
	}}) {
		t.Fatal("removing an option should change the configuration")
	}
}

func TestAllVariantSelectionsRejectsInactiveDefinitionAndOption(t *testing.T) {
	base := ProductVariantConfig{Definitions: []ProductVariantDefinition{{
		DefinitionID: "color",
		IsActive:     true,
		Options:      []VariantOption{{ID: "red", IsActive: true}},
	}}}
	base.Definitions[0].IsActive = false
	if ErrorCodeFromVariantSelection(t, base) != "VARIANT_DEFINITION_INACTIVE" {
		t.Fatal("inactive definition was accepted")
	}
	base.Definitions[0].IsActive = true
	base.Definitions[0].Options[0].IsActive = false
	if ErrorCodeFromVariantSelection(t, base) != "VARIANT_OPTION_INACTIVE" {
		t.Fatal("inactive option was accepted")
	}
}

func ErrorCodeFromVariantSelection(t *testing.T, config ProductVariantConfig) string {
	t.Helper()
	_, err := allVariantSelections(config)
	if err == nil {
		t.Fatal("expected variant selection validation error")
	}
	return ErrorCode(err)
}

func TestMapConstraintConvertsVariantTriggerErrorsToDomainErrors(t *testing.T) {
	err := mapConstraint(&pgconn.PgError{Code: "23514", Message: "VARIANT_INACTIVE"})
	if ErrorCode(err) != "VARIANT_INACTIVE" || !errors.Is(err, identity.ErrValidation) {
		t.Fatalf("unexpected mapped error: %v", err)
	}
	err = mapConstraint(&pgconn.PgError{Code: "55000", Message: "VARIANT_IDENTITY_LOCKED"})
	if ErrorCode(err) != "VARIANT_STATE_CONFLICT" || !errors.Is(err, identity.ErrValidation) {
		t.Fatalf("unexpected mapped state error: %v", err)
	}
}

func TestMapConstraintPreservesCatalogAndVariantUniqueMappings(t *testing.T) {
	tests := []struct {
		name       string
		constraint string
		code       string
		message    string
	}{
		{name: "product code", constraint: "products_company_code_unique", message: "stok kodu bu firmada zaten kullanılıyor"},
		{name: "barcode", constraint: "product_barcodes_company_id_barcode_key", code: "VARIANT_BARCODE_DUPLICATE", message: "Bu barkod firmada başka bir ürün veya varyantta kullanılıyor."},
		{name: "primary product barcode", constraint: "product_primary_barcode_unique", code: "VARIANT_BARCODE_DUPLICATE", message: "Bu barkod firmada başka bir ürün veya varyantta kullanılıyor."},
		{name: "primary variant barcode", constraint: "product_variant_primary_barcode_unique", code: "VARIANT_BARCODE_DUPLICATE", message: "Bu barkod firmada başka bir ürün veya varyantta kullanılıyor."},
		{name: "variant SKU", constraint: "product_variants_company_id_variant_code_key", code: "VARIANT_CODE_DUPLICATE", message: "Varyant SKU bu firmada zaten kullanılıyor."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := mapConstraint(&pgconn.PgError{Code: "23505", ConstraintName: tt.constraint})
			if !errors.Is(err, identity.ErrValidation) {
				t.Fatalf("expected validation error, got %v", err)
			}
			if got := ErrorCode(err); got != tt.code {
				t.Fatalf("expected error code %q, got %q (%v)", tt.code, got, err)
			}
			if !strings.HasSuffix(err.Error(), tt.message) {
				t.Fatalf("expected error message to end with %q, got %q", tt.message, err.Error())
			}
			if tt.code != "" {
				var variantErr *VariantValidationError
				if !errors.As(err, &variantErr) {
					t.Fatalf("expected variant validation error, got %T", err)
				}
				if variantErr.Code != tt.code || variantErr.Message != tt.message {
					t.Fatalf("unexpected variant validation details: code=%q message=%q", variantErr.Code, variantErr.Message)
				}
			}
		})
	}
}
