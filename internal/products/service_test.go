package products

import (
	"errors"
	"strings"
	"testing"

	"github.com/alpyxn/varyaone/internal/identity"
)

func TestNormalizeInputAndDetails(t *testing.T) {
	input, units, barcodes, err := normalizeInput(Input{
		SKU:      " stk-001 ",
		Name:     "  Kahve  ",
		Kind:     "physical",
		BaseUnit: "adet",
		Units: []UnitInput{
			{Code: "ADET", IsBase: true, ConversionFactor: "1.000"},
		},
		Barcodes: []BarcodeInput{{Barcode: "8690001"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if input.Code != "STK-001" || input.Kind != "PHYSICAL" || input.Name != "Kahve" {
		t.Fatalf("input normalization failed: %+v", input)
	}
	if len(units) != 1 || units[0].Code != "ADET" || units[0].ConversionFactor != "1" || units[0].DecimalScale == nil {
		t.Fatalf("unit normalization failed: %+v", units)
	}
	if len(barcodes) != 1 || !barcodes[0].IsPrimary {
		t.Fatalf("barcode normalization failed: %+v", barcodes)
	}
}

func TestNormalizeInputAllowsBlankCodeForAutomaticCreation(t *testing.T) {
	input, _, _, err := normalizeInput(Input{
		Name:     "Kod Sonra Verilecek",
		Kind:     "PHYSICAL",
		BaseUnit: "ADET",
		Units:    []UnitInput{{Code: "ADET", IsBase: true, ConversionFactor: "1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if input.Code != "" {
		t.Fatalf("blank product code was changed during normalization: %q", input.Code)
	}
}

func TestNormalizeInputAcceptsTurkishPriceFormatting(t *testing.T) {
	input, _, _, err := normalizeInput(Input{
		Name:          "Fiyatlı Ürün",
		Kind:          "PHYSICAL",
		PurchasePrice: "1.250,50",
		SalesPrice:    "2.000,00",
		BaseUnit:      "ADET",
		Units:         []UnitInput{{Code: "ADET", IsBase: true, ConversionFactor: "1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if input.PurchasePrice != "1250.5" || input.SalesPrice != "2000" {
		t.Fatalf("unexpected normalized prices: purchase=%q sales=%q", input.PurchasePrice, input.SalesPrice)
	}
}

func TestNormalizeInputPreservesIntegerTrailingZeros(t *testing.T) {
	input, units, _, err := normalizeInput(Input{
		Name:            "Tam Sayı Fiyatı",
		Kind:            "PHYSICAL",
		PurchasePrice:   "600",
		SalesPrice:      "6000",
		PurchaseTaxRate: "20",
		SalesTaxRate:    "10",
		BaseUnit:        "ADET",
		Units:           []UnitInput{{Code: "ADET", IsBase: true, ConversionFactor: "1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if input.PurchasePrice != "600" || input.SalesPrice != "6000" || input.PurchaseTaxRate != "20" || input.SalesTaxRate != "10" {
		t.Fatalf("integer trailing zeros were removed: purchase=%q sales=%q purchase_rate=%q sales_rate=%q", input.PurchasePrice, input.SalesPrice, input.PurchaseTaxRate, input.SalesTaxRate)
	}
	if len(units) != 1 || units[0].ConversionFactor != "1" {
		t.Fatalf("unit conversion factor was changed: %+v", units)
	}
	if got := normalizeDecimal("100"); got != "100" {
		t.Fatalf("integer trailing zeros were removed from conversion factor: %q", got)
	}
}

func TestNormalizeInputTrimsOnlyFractionTrailingZeros(t *testing.T) {
	input, _, _, err := normalizeInput(Input{
		Name:          "Ondalıklı Fiyatı",
		Kind:          "PHYSICAL",
		PurchasePrice: "1.250,50",
		SalesPrice:    "600,00",
		BaseUnit:      "ADET",
		Units:         []UnitInput{{Code: "ADET", IsBase: true, ConversionFactor: "1.000"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if input.PurchasePrice != "1250.5" || input.SalesPrice != "600" {
		t.Fatalf("fraction normalization was incorrect: purchase=%q sales=%q", input.PurchasePrice, input.SalesPrice)
	}
}

func TestValidateBaseUnitImmutable(t *testing.T) {
	units := []UnitInput{{Code: "ADET", IsBase: true}}
	if err := validateBaseUnitImmutable(" adet ", units); err != nil {
		t.Fatalf("same base unit was rejected: %v", err)
	}

	err := validateBaseUnitImmutable("ADET", []UnitInput{{Code: "KOLI", IsBase: true}})
	if !errors.Is(err, identity.ErrValidation) || !strings.Contains(err.Error(), "stok birimi değiştirilemez") || !strings.Contains(err.Error(), "ADET") || !strings.Contains(err.Error(), "KOLI") {
		t.Fatalf("base unit change returned an unhelpful error: %v", err)
	}

	err = validateBaseUnitImmutable("ADET", nil)
	if !errors.Is(err, identity.ErrValidation) || !strings.Contains(err.Error(), "stok birimi korunamadı") {
		t.Fatalf("missing base unit was accepted: %v", err)
	}
}

func TestNormalizeInputKeepsProductTaxProfile(t *testing.T) {
	input, _, _, err := normalizeInput(Input{
		Name: "Vergili Ürün", Kind: "PHYSICAL", BaseUnit: "ADET",
		Units:           []UnitInput{{Code: "ADET", IsBase: true, ConversionFactor: "1"}},
		PurchaseTaxType: "otv", SalesTaxType: "KDV_TEVKIFAT",
		PurchaseTaxRate: "20,00", SalesTaxRate: "10", ExciseTaxRate: "5,5",
		WithholdingCode: "  601 ", WithholdingRate: "7,5", ExemptionCode: "350", TaxNote: "  ürün notu ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if input.PurchaseTaxType != "OTV" || input.SalesTaxType != "KDV_TEVKIFAT" || input.PurchaseTaxRate != "20" || input.ExciseTaxRate != "5.5" || input.WithholdingCode != "601" || input.WithholdingRate != "7.5" || input.ExemptionCode != "350" || input.TaxNote != "ürün notu" {
		t.Fatalf("unexpected tax profile normalization: %+v", input)
	}
	if input.PurchaseTaxProfile == nil || input.PurchaseTaxProfile.Treatment != TaxTreatmentStandard || input.PurchaseTaxProfile.Rate != "20" {
		t.Fatalf("legacy purchase tax profile was not derived: %+v", input.PurchaseTaxProfile)
	}
	if input.SalesTaxProfile == nil || input.SalesTaxProfile.Treatment != TaxTreatmentWithholding || input.SalesTaxProfile.WithholdingCode != "601" || input.SalesTaxProfile.WithholdingRate != "7.5" {
		t.Fatalf("legacy sales tax profile was not derived: %+v", input.SalesTaxProfile)
	}
}

func TestNormalizeInputValidatesDirectionalTaxTreatments(t *testing.T) {
	base := Input{
		Name: "Vergi Profili", Kind: "PHYSICAL", BaseUnit: "ADET",
		Units:              []UnitInput{{Code: "ADET", IsBase: true, ConversionFactor: "1"}},
		PurchaseTaxProfile: &TaxProfileInput{Treatment: TaxTreatmentStandard, TaxCode: "KDV", Rate: "20", TaxRateID: "00000000-0000-0000-0000-000000000001"},
		SalesTaxProfile:    &TaxProfileInput{Treatment: TaxTreatmentExempt, ExemptionCode: "350", ExemptionID: "00000000-0000-0000-0000-000000000002"},
	}
	input, _, _, err := normalizeInput(base)
	if err != nil {
		t.Fatal(err)
	}
	if input.SalesTaxProfile == nil || input.SalesTaxProfile.Treatment != TaxTreatmentExempt {
		t.Fatalf("explicit exemption profile was not preserved: %+v", input.SalesTaxProfile)
	}

	invalid := base
	invalid.SalesTaxProfile = &TaxProfileInput{Treatment: TaxTreatmentWithholding, WithholdingRate: "10"}
	if _, _, _, err = normalizeInput(invalid); err == nil || !strings.Contains(err.Error(), "tevkifat") {
		t.Fatalf("withholding profile without rule/code returned %v", err)
	}

	invalid = base
	invalid.PurchaseTaxProfile = &TaxProfileInput{Treatment: TaxTreatmentNotApplicable, Rate: "1"}
	if _, _, _, err = normalizeInput(invalid); err == nil || !strings.Contains(err.Error(), "uygulanmıyorsa") {
		t.Fatalf("not applicable profile with rate returned %v", err)
	}
}

func TestNormalizeInputReturnsStableTaxValidationCode(t *testing.T) {
	input := Input{
		Name: "Oransız", Kind: "PHYSICAL", BaseUnit: "ADET",
		Units:              []UnitInput{{Code: "ADET", IsBase: true, ConversionFactor: "1"}},
		PurchaseTaxProfile: &TaxProfileInput{Treatment: TaxTreatmentStandard},
		SalesTaxProfile:    &TaxProfileInput{Treatment: TaxTreatmentNotApplicable},
	}
	_, _, _, err := normalizeInput(input)
	if ErrorCode(err) != "TAX_RATE_REQUIRED" {
		t.Fatalf("expected TAX_RATE_REQUIRED, got %v", err)
	}
}

func TestNormalizeInputAcceptsManualKDVRate(t *testing.T) {
	input := Input{
		Name: "Elle Oran", Kind: "PHYSICAL", BaseUnit: "ADET",
		Units:              []UnitInput{{Code: "ADET", IsBase: true, ConversionFactor: "1"}},
		PurchaseTaxProfile: &TaxProfileInput{Treatment: TaxTreatmentStandard, TaxCode: "KDV", Rate: "0"},
		SalesTaxProfile:    &TaxProfileInput{Treatment: TaxTreatmentStandard, TaxCode: "KDV", Rate: "10,5"},
	}
	normalized, _, _, err := normalizeInput(input)
	if err != nil {
		t.Fatal(err)
	}
	if normalized.PurchaseTaxProfile == nil || normalized.PurchaseTaxProfile.Rate != "0" {
		t.Fatalf("manual zero KDV rate was not preserved: %+v", normalized.PurchaseTaxProfile)
	}
	if normalized.SalesTaxProfile == nil || normalized.SalesTaxProfile.Rate != "10.5" {
		t.Fatalf("manual KDV rate was not normalized: %+v", normalized.SalesTaxProfile)
	}
}

func TestNormalizeInputRejectsAmbiguousCatalogDetails(t *testing.T) {
	cases := []Input{
		{Name: "Eksik", Kind: "PHYSICAL", BaseUnit: "ADET", Units: []UnitInput{{Code: "ADET", IsBase: true, ConversionFactor: "1"}, {Code: "ADET", ConversionFactor: "2"}}},
		{Name: "Çoklu birim", Kind: "PHYSICAL", BaseUnit: "ADET", Units: []UnitInput{{Code: "ADET", IsBase: true, ConversionFactor: "1"}, {Code: "KOLI", ConversionFactor: "12"}}},
		{Name: "İki temel", Kind: "PHYSICAL", Units: []UnitInput{{Code: "ADET", IsBase: true, ConversionFactor: "1"}, {Code: "KG", IsBase: true, ConversionFactor: "1"}}},
		{Name: "Sıfır", Kind: "PHYSICAL", Units: []UnitInput{{Code: "ADET", IsBase: true, ConversionFactor: "0"}}},
		{Name: "İki ana barkod", Kind: "SERVICE", Units: []UnitInput{{Code: "SAAT", IsBase: true, ConversionFactor: "1"}}, Barcodes: []BarcodeInput{{Barcode: "1", IsPrimary: true}, {Barcode: "2", IsPrimary: true}}},
	}
	for _, input := range cases {
		if _, _, _, err := normalizeInput(input); err == nil || !strings.Contains(err.Error(), "validation failed") {
			t.Fatalf("invalid catalog details were accepted: %+v err=%v", input, err)
		}
	}
}

func TestNormalizeSearchQueryIsLiteralAndTokenized(t *testing.T) {
	if got := normalizeSearchQuery("STK-001 Kahve"); got != "stk001:* & kahve:*" {
		t.Fatalf("unexpected product search query: %q", got)
	}
	if got := normalizeSearchQuery("---"); got != "" {
		t.Fatalf("punctuation should not become a broad search: %q", got)
	}
}
