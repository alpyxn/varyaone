package purchasing

import (
	"strings"
	"testing"
)

func TestValidateReceiptLineKeepsDamagedAndRejectedQuantities(t *testing.T) {
	line := GoodsReceiptLine{
		ProductID:        "10000000-0000-4000-8000-000000000001",
		DamagedQuantity:  "2",
		RejectedQuantity: "1",
		UnitCode:         "ADET",
		UnitCost:         "10.25",
		Currency:         "TRY",
	}
	if err := validateReceiptLine(&line); err != nil {
		t.Fatalf("damaged/rejected-only receipt should be valid: %v", err)
	}
}

func TestValidateReceiptLineRejectsEmptyPhysicalReceipt(t *testing.T) {
	line := GoodsReceiptLine{
		ProductID: "10000000-0000-4000-8000-000000000001",
		UnitCode:  "ADET",
		UnitCost:  "10.25",
		Currency:  "TRY",
	}
	if err := validateReceiptLine(&line); err == nil {
		t.Fatal("empty receipt line should be rejected")
	}
}

func TestValidateReceiptLineRejectsMalformedDecimal(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*GoodsReceiptLine)
	}{
		{name: "accepted exponent", mutate: func(line *GoodsReceiptLine) { line.AcceptedQuantity = "1e2" }},
		{name: "damaged negative", mutate: func(line *GoodsReceiptLine) { line.DamagedQuantity = "-1" }},
		{name: "rejected scale", mutate: func(line *GoodsReceiptLine) { line.RejectedQuantity = "0.123456789" }},
		{name: "unit cost syntax", mutate: func(line *GoodsReceiptLine) { line.UnitCost = "NaN" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			line := GoodsReceiptLine{
				ProductID:        "10000000-0000-4000-8000-000000000001",
				AcceptedQuantity: "1",
				UnitCode:         "ADET",
				UnitCost:         "10",
				Currency:         "TRY",
			}
			test.mutate(&line)
			if err := validateReceiptLine(&line); err == nil {
				t.Fatalf("malformed %s was accepted", test.name)
			}
		})
	}
}

func TestValidateReturnLineRejectsMalformedDecimal(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*PurchaseReturnLine)
	}{
		{name: "quantity exponent", mutate: func(line *PurchaseReturnLine) { line.Quantity = "1e2" }},
		{name: "quantity negative", mutate: func(line *PurchaseReturnLine) { line.Quantity = "-1" }},
		{name: "quantity scale", mutate: func(line *PurchaseReturnLine) { line.Quantity = "0.123456789" }},
		{name: "unit cost syntax", mutate: func(line *PurchaseReturnLine) { line.UnitCost = "NaN" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			line := PurchaseReturnLine{
				ProductID: "10000000-0000-4000-8000-000000000001",
				Quantity:  "1",
				UnitCode:  "ADET",
				UnitCost:  "10",
			}
			test.mutate(&line)
			if err := validateReturnLine(&line, "TRY"); err == nil {
				t.Fatalf("malformed %s was accepted", test.name)
			}
		})
	}
}

func TestExactPurchaseArithmeticDoesNotUseFloatingPoint(t *testing.T) {
	if got := add("9007199254740993.12345678", "0.87654322"); got != "9007199254740994.00000000" {
		t.Fatalf("exact addition = %s", got)
	}
	if got := multiply("0.1", "0.2"); got != "0.02000000" {
		t.Fatalf("exact multiplication = %s", got)
	}
}

func TestValidOverDeliveryPolicies(t *testing.T) {
	for _, policy := range []string{"ALLOW", "WARN", "BLOCK"} {
		if !validPolicy(policy) {
			t.Fatalf("policy %s was rejected", policy)
		}
	}
	if validPolicy("SILENT") {
		t.Fatal("unknown over-delivery policy was accepted")
	}
}

func TestPurchaseDecimalsRejectNonDecimalSyntax(t *testing.T) {
	for _, value := range []string{"1e2", "1/2", "NaN", ""} {
		if _, err := commercialDecimalPurchase(value); err == nil {
			t.Fatalf("%q should not be accepted as a decimal", value)
		}
	}
}

func TestPurchaseDecimalsRespectStorageScale(t *testing.T) {
	if _, err := parsePurchaseDecimal("1.123456789"); err == nil {
		t.Fatal("purchase decimals with more than eight fractional digits should be rejected")
	}
	if _, err := parsePurchaseDecimal("1.12345678"); err != nil {
		t.Fatalf("eight fractional digits should be accepted: %v", err)
	}
}

func TestValidatePurchaseInvoiceServiceLineAllowsZeroAmounts(t *testing.T) {
	line := PurchaseInvoiceLine{
		LineType:            "SERVICE",
		ProductID:           "10000000-0000-4000-8000-000000000001",
		DescriptionSnapshot: "Kurulum hizmeti",
		Quantity:            "1",
		UnitPrice:           "0",
		GrossAmount:         "0",
		DiscountAmount:      "0",
		TaxBase:             "0",
		TaxAmount:           "0",
		WithholdingAmount:   "0",
		PayableAmount:       "0",
	}
	if err := validateInvoiceLine(&line); err != nil {
		t.Fatalf("zero-valued service invoice line should be valid: %v", err)
	}
	if line.UnitCode != "ADET" {
		t.Fatalf("default unit code = %q, want ADET", line.UnitCode)
	}
}

func TestPurchaseServiceInvoiceConversionUsesOrderedQuantity(t *testing.T) {
	quantity, base, available, err := remainingPurchaseBetweenQuantities("5", "0", "1")
	if err != nil {
		t.Fatalf("remaining service quantity returned error: %v", err)
	}
	if !available || quantity != "5.00000000" || base != "5.00000000" {
		t.Fatalf("remaining service quantity = %q/%q/%t", quantity, base, available)
	}
}

func TestPurchaseListSpecsUseTypedTablesAndVersions(t *testing.T) {
	checks := map[PurchaseKind]string{
		PurchaseOrderKind:   "purchase_orders",
		GoodsReceiptKind:    "goods_receipts",
		PurchaseInvoiceKind: "purchase_invoices",
		PurchaseReturnKind:  "purchase_returns",
	}
	for kind, table := range checks {
		spec, ok := purchaseListSpec(kind)
		if !ok || spec.table != table || spec.version != "t.version" || spec.lineParentColumn == "" {
			t.Fatalf("%s spec = %+v, ok=%v", kind, spec, ok)
		}
		query := purchaseListQuery(spec)
		if strings.Contains(query, "t.0") {
			t.Fatalf("%s query contains an invalid qualified zero literal", kind)
		}
		if !strings.Contains(query, "(t.version)::bigint") {
			t.Fatalf("%s query does not qualify the version column", kind)
		}
		if !strings.Contains(query, "AS tax_total") || !strings.Contains(query, "AS grand_total") || !strings.Contains(query, "AS payable_total") {
			t.Fatalf("%s query does not expose invoice total projections", kind)
		}
	}
}

func TestPurchaseInvoiceListProjectsGrossAndPayableTotalsSeparately(t *testing.T) {
	spec, ok := purchaseListSpec(PurchaseInvoiceKind)
	if !ok {
		t.Fatal("purchase-invoice list spec is missing")
	}
	if spec.taxTotal != "t.tax_total" || spec.payableTotal != "t.payable_total" || !strings.Contains(spec.grandTotal, "withholding_amount") {
		t.Fatalf("purchase-invoice total projections = %+v", spec)
	}
}

func TestPurchaseListQueryHasBalancedScopePredicates(t *testing.T) {
	spec, ok := purchaseListSpec(PurchaseOrderKind)
	if !ok {
		t.Fatal("purchase-order list spec is missing")
	}
	query := purchaseListQuery(spec)
	depth := 0
	for _, character := range query {
		switch character {
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				t.Fatal("purchase list query closes a parenthesis before opening it")
			}
		}
	}
	if depth != 0 {
		t.Fatalf("purchase list query has unbalanced parentheses: depth=%d", depth)
	}
}

func TestPurchaseReferenceListPredicateExcludesDrafts(t *testing.T) {
	if got := purchaseReferenceListPredicate(false); got != "" {
		t.Fatalf("normal list predicate = %q, want empty", got)
	}
	if got := purchaseReferenceListPredicate(true); got != " AND t.status <> 'DRAFT'" {
		t.Fatalf("reference list predicate = %q", got)
	}
}

func TestPurchaseReferenceListPredicateHidesCompletedSources(t *testing.T) {
	for _, target := range []string{"dispatches", "invoices"} {
		got := purchaseReferenceListPredicateForTarget(true, PurchaseOrderKind, target)
		if target == "dispatches" && (!strings.Contains(got, "t.status IN ('CONFIRMED','PARTIALLY_FULFILLED')") || strings.Contains(got, "t.status IN ('CONFIRMED','PARTIALLY_FULFILLED','FULFILLED')")) {
			t.Fatalf("%s source predicate = %q", target, got)
		}
		if target == "invoices" && (!strings.Contains(got, "t.status IN ('CONFIRMED','PARTIALLY_FULFILLED','FULFILLED')") || !strings.Contains(got, "l.received_quantity>l.invoiced_quantity")) {
			t.Fatalf("%s source predicate = %q", target, got)
		}
	}
}

func TestPurchaseInvoiceStockRequiresNoGoodsReceiptLine(t *testing.T) {
	if !purchaseInvoicePostsStock(PurchaseInvoiceLine{LineType: "PRODUCT"}) {
		t.Fatal("standalone product invoice should affect stock")
	}
	if purchaseInvoicePostsStock(PurchaseInvoiceLine{LineType: "PRODUCT", PurchaseOrderLineID: "order-line"}) {
		t.Fatal("order-linked product invoice must wait for fulfillment")
	}
	if purchaseInvoicePostsStock(PurchaseInvoiceLine{LineType: "PRODUCT", GoodsReceiptLineID: "receipt-line"}) {
		t.Fatal("goods-receipt-linked product invoice must not affect stock twice")
	}
	if purchaseInvoicePostsStock(PurchaseInvoiceLine{LineType: "SERVICE"}) {
		t.Fatal("service invoice must not affect stock")
	}
}

func TestPurchaseSourceLineBindingsRequireMatchingHeaderSource(t *testing.T) {
	orderLine := "10000000-0000-4000-8000-000000000001"
	if err := validateGoodsReceiptSourceShape("", []GoodsReceiptLine{{PurchaseOrderLineID: &orderLine}}); err == nil {
		t.Fatal("receipt line source must not be accepted without a header order")
	}
	if err := validateGoodsReceiptSourceShape("10000000-0000-4000-8000-000000000002", []GoodsReceiptLine{{}}); err == nil {
		t.Fatal("order-linked receipt must require a source order line on every line")
	}
	if err := validatePurchaseReturnSourceShape("", []PurchaseReturnLine{{SourceReceiptLineID: "receipt-line"}}); err == nil {
		t.Fatal("return line source must not be accepted without a header receipt")
	}
	if err := validatePurchaseReturnSourceShape("10000000-0000-4000-8000-000000000002", []PurchaseReturnLine{{}}); err == nil {
		t.Fatal("receipt-linked return must require a source receipt line on every line")
	}
}
