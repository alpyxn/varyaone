package sales

import (
	"errors"
	"testing"

	"github.com/alpyxn/varyaone/internal/identity"
)

func TestValidateLineInputRequiresProductSnapshotOrProductID(t *testing.T) {
	base := DocumentLineInput{UnitCode: "ADET", Quantity: "1", UnitPrice: "10", DiscountRate: "0", TaxRate: "20"}
	if err := validateLineInput(base); !errors.Is(err, identity.ErrValidation) {
		t.Fatalf("missing product snapshot returned %v", err)
	}
	base.ProductID = "00000000-0000-4000-8000-000000000001"
	if err := validateLineInput(base); err != nil {
		t.Fatalf("product id should allow server-side snapshot: %v", err)
	}
}

func TestValidateLineInputRejectsNegativeRatesAndQuantity(t *testing.T) {
	base := DocumentLineInput{ProductNameSnapshot: "Kalem", UnitCode: "ADET", Quantity: "1", UnitPrice: "10", DiscountRate: "0", TaxRate: "20"}
	for name, mutate := range map[string]func(*DocumentLineInput){
		"negative quantity":    func(value *DocumentLineInput) { value.Quantity = "-1" },
		"negative discount":    func(value *DocumentLineInput) { value.DiscountRate = "-1" },
		"tax over one hundred": func(value *DocumentLineInput) { value.TaxRate = "101" },
	} {
		t.Run(name, func(t *testing.T) {
			input := base
			mutate(&input)
			if err := validateLineInput(input); !errors.Is(err, identity.ErrValidation) {
				t.Fatalf("validateLineInput returned %v", err)
			}
		})
	}
}

func TestStockPostingLineDoesNotInferCostFromInvoicePrice(t *testing.T) {
	productID := "00000000-0000-4000-8000-000000000001"
	line := DocumentLine{
		ID:        "00000000-0000-4000-8000-000000000002",
		ProductID: &productID,
		Quantity:  "2",
		UnitCode:  "ADET",
		UnitPrice: "600",
	}

	got := stockPostingLineForInvoice(line)
	if got.UnitCost != "" || got.Currency != "" {
		t.Fatalf("invoice price must not become inventory cost: cost=%q currency=%q", got.UnitCost, got.Currency)
	}
	if got.ProductID != productID || got.Quantity != line.Quantity {
		t.Fatalf("stock line lost invoice identity: %+v", got)
	}
}

func TestStockPostingLineCarriesVariantIdentity(t *testing.T) {
	productID := "00000000-0000-4000-8000-000000000001"
	variantID := "00000000-0000-4000-8000-000000000003"
	line := DocumentLine{ID: "00000000-0000-4000-8000-000000000002", ProductID: &productID, VariantID: &variantID, Quantity: "2", UnitCode: "ADET"}

	got := stockPostingLineForInvoice(line)
	if got.ProductID != productID || got.VariantID != variantID {
		t.Fatalf("stock line lost variant identity: %+v", got)
	}
}

func TestValidateStockLineInputRequiresVariantForStockEffect(t *testing.T) {
	line := DocumentLineInput{ProductID: "00000000-0000-4000-8000-000000000001"}
	if err := validateStockLineInput("OUT", line); !errors.Is(err, identity.ErrValidation) {
		t.Fatalf("missing stock variant returned %v", err)
	}
	line.VariantID = "00000000-0000-4000-8000-000000000003"
	if err := validateStockLineInput("OUT", line); err != nil {
		t.Fatalf("valid stock variant rejected: %v", err)
	}
}
