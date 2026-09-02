package imports

import (
	"testing"

	"github.com/alpyxn/varyaone/internal/dataexchange"
)

func TestPersistedRowStatusUsesDurableErrorValueForInvalidRows(t *testing.T) {
	if got := persistedRowStatus(dataexchange.RowStatusInvalid); got != "ERROR" {
		t.Fatalf("invalid row status=%q, want ERROR", got)
	}
	if got := persistedRowStatus(dataexchange.RowStatusValid); got != "VALID" {
		t.Fatalf("valid row status=%q, want VALID", got)
	}
}

func TestImportedPricesRemainExactAndFitProductScale(t *testing.T) {
	for _, test := range []struct {
		raw, want string
	}{
		{raw: "1.23000000", want: "1.23000000"},
		{raw: "0,50", want: "0.50"},
	} {
		got, err := parseImportPrice(test.raw)
		if err != nil || got != test.want {
			t.Fatalf("parseImportPrice(%q) = %q, %v; want %q", test.raw, got, err, test.want)
		}
	}
	if _, err := parseImportPrice("1234567890123.00"); err == nil {
		t.Fatal("price exceeding numeric(20,8) integer precision was accepted")
	}
	if _, err := parseImportPrice("1e3"); err == nil {
		t.Fatal("scientific notation was accepted")
	}
	if !sameImportDecimal("1.0", "1.00") || sameImportDecimal("1.00", "1.01") {
		t.Fatal("decimal equality does not compare exact numeric values")
	}
}

func TestImportedProductRateAndOpeningStockValidation(t *testing.T) {
	for _, test := range []struct {
		raw, want string
	}{
		{raw: "20", want: "20"},
		{raw: "10,50", want: "10.50"},
	} {
		got, err := parseImportRate(test.raw)
		if err != nil || got != test.want {
			t.Fatalf("parseImportRate(%q) = %q, %v; want %q", test.raw, got, err, test.want)
		}
	}
	for _, raw := range []string{"-1", "100.00000001", "1e1"} {
		if _, err := parseImportRate(raw); err == nil {
			t.Fatalf("parseImportRate(%q) accepted an invalid rate", raw)
		}
	}
	if got, err := parseImportOpeningStockQuantity("10,50000000"); err != nil || got != "10.50000000" {
		t.Fatalf("parseImportOpeningStockQuantity() = %q, %v", got, err)
	}
	for _, raw := range []string{"0", "-1", "1.000000001"} {
		if _, err := parseImportOpeningStockQuantity(raw); err == nil {
			t.Fatalf("parseImportOpeningStockQuantity(%q) accepted an invalid quantity", raw)
		}
	}
}

func TestImportBooleanNormalizationAcceptsTurkishAndEnglishValues(t *testing.T) {
	for raw, want := range map[string]bool{
		"Evet":     true,
		"Aktif":    true,
		"ACTIVE":   true,
		"true":     true,
		"YES":      true,
		"1":        true,
		"Hayır":    false,
		"Pasif":    false,
		"INACTIVE": false,
		"false":    false,
		"NO":       false,
		"0":        false,
	} {
		got, err := parseImportBool(raw)
		if err != nil || got != want {
			t.Errorf("parseImportBool(%q) = %v, %v; want %v", raw, got, err, want)
		}
	}
	for _, raw := range []string{"maybe", "", "2"} {
		if _, err := parseImportBool(raw); err == nil {
			t.Errorf("parseImportBool(%q) accepted an invalid value", raw)
		}
	}
}

func TestImportedOpeningStockLineRequiresWarehouseAndQuantityTogether(t *testing.T) {
	values := map[string]string{
		"product_code":                 "stk-001",
		"opening_stock_warehouse_code": "merkez",
	}
	if _, provided, err := importedOpeningStockLine(values, 2); err == nil || provided {
		t.Fatalf("opening stock with only warehouse = provided:%v, err:%v; want a validation error", provided, err)
	}

	values["opening_stock_quantity"] = "4,25"
	line, provided, err := importedOpeningStockLine(values, 2)
	if err != nil || !provided {
		t.Fatalf("openingStockLine() = %#v, provided:%v, err:%v", line, provided, err)
	}
	if line.WarehouseCode != "MERKEZ" || line.ProductCode != "STK-001" || line.Quantity != "4.25" || line.RowNumber != 2 {
		t.Fatalf("opening stock line = %#v", line)
	}
}

func TestPartyRoleNormalization(t *testing.T) {
	for raw, want := range map[string]string{
		"Müşteri":                  "CUSTOMER",
		"Tedarikçi":                "SUPPLIER",
		"CUSTOMER+SUPPLIER":        "BOTH",
		"MÜŞTERİ/TEDARİKÇİ":        "BOTH",
		"Müşteri ve Tedarikçi":     "BOTH",
		"CUSTOMER AND SUPPLIER":    "BOTH",
		`["CUSTOMER","SUPPLIER"]`:  "BOTH",
		`["Müşteri", "Tedarikçi"]`: "BOTH",
	} {
		if got := normalizePartyRoles(raw); got != want {
			t.Errorf("normalizePartyRoles(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestPartyKindNormalization(t *testing.T) {
	for raw, want := range map[string]string{
		"Kişi":         "PERSON",
		"PERSON":       "PERSON",
		"Kurum":        "ORGANIZATION",
		"ORGANISATION": "ORGANIZATION",
		"organization": "ORGANIZATION",
	} {
		if got := normalizePartyKind(raw); got != want {
			t.Errorf("normalizePartyKind(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestPartyAddressProvidedRequiresOnlyProvinceWhenAddressHasData(t *testing.T) {
	if partyAddressProvided(map[string]string{}) {
		t.Fatal("an empty address block was treated as provided")
	}
	if !partyAddressProvided(map[string]string{"province_name": "İstanbul"}) {
		t.Fatal("province-only address was not treated as provided")
	}
	if !partyAddressProvided(map[string]string{"address_line": "Sokak 1"}) {
		t.Fatal("free-form address was not treated as provided")
	}
}
