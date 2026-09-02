package sales

import (
	"errors"
	"strings"
	"testing"

	"github.com/alpyxn/varyaone/internal/taxes"
)

func TestNormalizeCommercialLinesUsesExactBaseQuantityAndTotals(t *testing.T) {
	lines, totals, err := normalizeCommercialLines([]CommercialLineInput{{
		LineType: "PRODUCT", ProductID: "00000000-0000-4000-8000-000000000001",
		WarehouseID: "00000000-0000-4000-8000-000000000002", UnitCode: "ADET",
		Quantity: "3", ConversionFactor: "2", BaseQuantity: "6", UnitPrice: "19.99",
		DiscountComponents: []CommercialDiscountInput{{Rate: "10"}}, TaxRate: "20",
	}}, "", "TRY")
	if err != nil {
		t.Fatalf("normalizeCommercialLines returned error: %v", err)
	}
	if len(lines) != 1 || lines[0].BaseQuantity != "6.00000000" || lines[0].ConversionFactor != "2.00000000" {
		t.Fatalf("conversion snapshot = %+v", lines)
	}
	if lines[0].GrossAmount != "59.97000000" || lines[0].DiscountAmount != "5.99700000" || lines[0].TaxAmount != "10.79460000" || lines[0].PayableAmount != "64.76760000" {
		t.Fatalf("line totals = %+v", lines[0])
	}
	if totals.PayableTotal != "64.76760000" {
		t.Fatalf("document payable total = %s", totals.PayableTotal)
	}
}

func TestCommercialBaseEquivalentUsesExactDecimalRate(t *testing.T) {
	got, err := commercialBaseEquivalent("123.45", "1.2345")
	if err != nil {
		t.Fatalf("commercialBaseEquivalent returned error: %v", err)
	}
	if got != "152.3990" {
		t.Fatalf("base equivalent = %s, want 152.3990", got)
	}
	if _, err = commercialBaseEquivalent("10", ""); err == nil {
		t.Fatal("blank exchange rate must be rejected")
	}
}

func TestAssignCommercialCreateLineIDsSeparatesSourceAndTarget(t *testing.T) {
	sourceLineID := "00000000-0000-4000-8000-000000000001"
	input := CommercialDocumentInput{
		Allocations: []CommercialAllocationInput{{
			SourceLineID: sourceLineID,
			TargetLineID: sourceLineID,
			Quantity:     "1",
		}},
	}
	lines := []CommercialLine{{ID: sourceLineID, SourceLineID: &sourceLineID}}

	if err := assignCommercialCreateLineIDs(&input, lines); err != nil {
		t.Fatalf("assignCommercialCreateLineIDs returned error: %v", err)
	}
	if lines[0].ID == sourceLineID || input.Allocations[0].TargetLineID != lines[0].ID {
		t.Fatalf("source and target IDs were not separated: line=%q allocation=%q", lines[0].ID, input.Allocations[0].TargetLineID)
	}
	if *lines[0].SourceLineID != sourceLineID {
		t.Fatalf("source provenance changed: %q", *lines[0].SourceLineID)
	}
}

func TestNormalizeCommercialLinesUsesSchemaCompatibleSnapshots(t *testing.T) {
	lines, _, err := normalizeCommercialLines([]CommercialLineInput{{
		LineType: "PRODUCT", ProductID: "00000000-0000-4000-8000-000000000001",
		WarehouseID: "00000000-0000-4000-8000-000000000002", UnitCode: "ADET",
		Quantity: "1", UnitPrice: "10",
	}}, "", "TRY")
	if err != nil {
		t.Fatalf("normalizeCommercialLines returned error: %v", err)
	}
	if len(lines) != 1 {
		t.Fatalf("normalized lines = %d, want 1", len(lines))
	}
	if lines[0].PriceListSnapshot == nil || lines[0].DiscountComponents == nil || lines[0].TaxSnapshot == nil {
		t.Fatalf("schema snapshots must not be nil: %+v", lines[0])
	}
	if got := string(jsonBytes(lines[0].PriceListSnapshot)); got != "{}" {
		t.Fatalf("price list snapshot = %s, want an empty JSON object", got)
	}
	if got := string(jsonBytes(lines[0].DiscountComponents)); got != "[]" {
		t.Fatalf("discount components snapshot = %s, want an empty JSON array", got)
	}
	if got := string(jsonBytes(lines[0].TaxSnapshot)); got == "null" {
		t.Fatal("tax snapshot must be a JSON object, not null")
	}
}

func TestNormalizeCommercialLinesTaxIncludedSplitsBaseAndTax(t *testing.T) {
	lines, totals, err := normalizeCommercialLines([]CommercialLineInput{{
		LineType: "SERVICE", UnitCode: "SAAT", Quantity: "1", UnitPrice: "120",
		TaxRate: "20", TaxIncluded: true,
	}}, "00000000-0000-4000-8000-000000000002", "TRY")
	if err != nil {
		t.Fatalf("normalizeCommercialLines returned error: %v", err)
	}
	if lines[0].WarehouseID != nil {
		t.Fatal("service line must not inherit a warehouse")
	}
	if lines[0].NetAmount != "100.00000000" || lines[0].TaxAmount != "20.00000000" || totals.PayableTotal != "120.00000000" {
		t.Fatalf("tax-inclusive totals = %+v / %s", lines[0], totals.PayableTotal)
	}
}

func TestInvoicePostingDescriptionUsesDocumentNumberWhenNotesAreBlank(t *testing.T) {
	if got := invoicePostingDescription("SALES_INVOICE", "SF-2026-001", "  "); got != "Satış faturası SF-2026-001" {
		t.Fatalf("blank notes description = %q", got)
	}
	if got := invoicePostingDescription("SALES_RETURN_INVOICE", "SI-2026-001", "Hasarlı ürün"); got != "Hasarlı ürün" {
		t.Fatalf("explicit notes description = %q", got)
	}
}

func TestCommercialDecimalsRejectNonDecimalSyntax(t *testing.T) {
	for _, value := range []string{"1e2", "1/2", "NaN", ""} {
		if _, err := commercialDecimal(value, false); err == nil {
			t.Fatalf("%q should not be accepted as a decimal", value)
		}
	}
	if _, err := commercialDecimal("0", false); err != nil {
		t.Fatalf("zero tax rate should be valid: %v", err)
	}
}

func TestCommercialDecimalsRespectStorageScale(t *testing.T) {
	if _, err := commercialDecimal("1.123456789", false); err == nil {
		t.Fatal("commercial decimals with more than eight fractional digits should be rejected")
	}
	if _, err := commercialDecimal("1.12345678", false); err != nil {
		t.Fatalf("eight fractional digits should be accepted: %v", err)
	}
}

func TestCommercialStockAndReservationLinesExcludeServices(t *testing.T) {
	productID := "00000000-0000-4000-8000-000000000001"
	warehouseID := "00000000-0000-4000-8000-000000000002"
	lines := []CommercialLine{
		{ID: "product", LineType: "PRODUCT", ProductID: &productID, WarehouseID: &warehouseID, Quantity: "1", BaseQuantity: "1", ConversionFactor: "1", UnitCode: "ADET"},
		{ID: "service", LineType: "SERVICE", ProductID: &productID, Quantity: "1", BaseQuantity: "1", ConversionFactor: "1", UnitCode: "SAAT"},
	}
	if got := commercialStockLines(lines); len(got) != 1 || got[0].LineID != "product" {
		t.Fatalf("stock postings = %+v, service line must be excluded", got)
	}
	if got := reservationLines(lines); len(got) != 1 || got[0].OrderLineID != "product" {
		t.Fatalf("reservations = %+v, service line must be excluded", got)
	}
}

func TestCommercialReservationConsumptionQueryGroupsVariantAndStockDimensions(t *testing.T) {
	query := commercialReservationConsumptionQuery()
	for _, field := range []string{
		"a.source_line_id",
		"source.document_id",
		"ol.product_id",
		"ol.variant_id",
		"ol.warehouse_id",
	} {
		if !strings.Contains(query, field) {
			t.Fatalf("reservation consumption query does not group by %s: %s", field, query)
		}
	}
	if !strings.Contains(query, "COALESCE(SUM(a.base_quantity)") {
		t.Fatal("reservation consumption query must aggregate allocation quantity")
	}
}

func TestMapCommercialErrorKeepsInventoryVariantFailuresStable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code string
	}{
		{name: "required", err: errors.New("VARIANT_REQUIRED: varyant seçilmelidir"), code: CommercialErrorVariantRequired},
		{name: "inactive", err: errors.New("VARIANT_INACTIVE: pasif varyant"), code: CommercialErrorVariantInactive},
		{name: "mismatch", err: errors.New("VARIANT_PRODUCT_MISMATCH: yanlış ürün"), code: CommercialErrorInvalidRelation},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mapped := mapCommercialError(tt.err)
			var commercialErr *CommercialError
			if !errors.As(mapped, &commercialErr) || commercialErr.Code != tt.code {
				t.Fatalf("mapped error = %v, want commercial code %s", mapped, tt.code)
			}
		})
	}
}

func TestCommercialSourceTypeRequiresTypedSourceState(t *testing.T) {
	if !commercialSourceTypeAllowed(SalesDispatch, "ORDER", "SALES_ORDER") {
		t.Fatal("sales order should be a dispatch source")
	}
	if commercialSourceTypeAllowed(SalesDispatch, "QUOTE", "SALES_QUOTE") {
		t.Fatal("quote should not bypass the order stage")
	}
	var typed *CommercialError
	if !errors.As(commercialError(CommercialErrorInvalidRelation, "x", "", 0), &typed) {
		t.Fatal("commercial errors must remain typed")
	}
}

func TestSalesReturnRequiresReason(t *testing.T) {
	err := validateCommercialReturnReason(SalesReturn, "  ")
	var commercialErr *CommercialError
	if !errors.As(err, &commercialErr) || commercialErr.Code != CommercialErrorReturnReasonRequired || commercialErr.Field != "reason" {
		t.Fatalf("missing return reason error = %v, want RETURN_REASON_REQUIRED on reason", err)
	}
	if err := validateCommercialReturnReason(SalesReturn, "Ürün hasarlı geldi"); err != nil {
		t.Fatalf("non-empty return reason rejected: %v", err)
	}
	if err := validateCommercialReturnReason(SalesInvoice, ""); err != nil {
		t.Fatalf("non-return document unexpectedly requires a reason: %v", err)
	}
}

func TestCommercialRelationRulesKeepReturnAndDispatchSourcesTyped(t *testing.T) {
	if !commercialSourceTypeAllowed(SalesReturn, "INVOICE", "SALES_INVOICE") {
		t.Fatal("sales invoice should be a sales return source")
	}
	if !commercialSourceTypeAllowed(SalesReturn, "DISPATCH", "SALES_DELIVERY") {
		t.Fatal("sales dispatch should be a sales return source")
	}
	if commercialSourceTypeAllowed(SalesReturn, "ORDER", "SALES_ORDER") {
		t.Fatal("sales order must not bypass the sales return source stages")
	}
	if commercialSourceTypeAllowed(SalesDispatch, "QUOTE", "SALES_QUOTE") {
		t.Fatal("sales quote must not be a direct dispatch source")
	}
}

func TestCommercialReferenceListPredicateExcludesDrafts(t *testing.T) {
	if got := commercialReferenceListPredicate(false); got != "" {
		t.Fatalf("normal list predicate = %q, want empty", got)
	}
	if got := commercialReferenceListPredicate(true); got != " AND t.status <> 'DRAFT'" {
		t.Fatalf("reference list predicate = %q", got)
	}
}

func TestCommercialOrderSourceListsAcceptedQuotes(t *testing.T) {
	got := commercialReferenceListPredicateForTarget(true, SalesQuote, "orders")
	if !strings.Contains(got, "t.status = 'ACCEPTED'") || !strings.Contains(got, "SALES_QUOTE") {
		t.Fatalf("quote source predicate = %q", got)
	}
}

func TestCommercialQuoteAcceptIsAvailableFromSent(t *testing.T) {
	if !commercialPrimaryTransitionAvailable(SalesQuote, "SENT") {
		t.Fatal("a sent quote must expose the accept transition")
	}
	if commercialPrimaryTransitionAvailable(SalesOrder, "SENT") {
		t.Fatal("SENT must not expose a primary transition for orders")
	}
	if commercialPrimaryTransitionAvailable(SalesQuote, "ACCEPTED") {
		t.Fatal("an accepted quote must not expose accept a second time")
	}
}

func TestCommercialReadKeepsUnassignedProductLinesVisible(t *testing.T) {
	got := commercialLineWarehouseReadPredicate()
	if !strings.Contains(got, "l.warehouse_id IS NOT NULL") {
		t.Fatalf("read predicate must allow an unassigned draft warehouse: %q", got)
	}
	if strings.Contains(got, "l.warehouse_id IS NULL") {
		t.Fatalf("read predicate must not classify an unassigned warehouse as unauthorized: %q", got)
	}
}

func TestCommercialReferenceListPredicateHidesCompletedSources(t *testing.T) {
	dispatches := commercialReferenceListPredicateForTarget(true, SalesOrder, "dispatches")
	if !strings.Contains(dispatches, "t.status IN ('CONFIRMED','PARTIALLY_FULFILLED')") || !strings.Contains(dispatches, "sr.line_type='PRODUCT'") {
		t.Fatalf("dispatch source predicate = %q", dispatches)
	}
	invoices := commercialReferenceListPredicateForTarget(true, SalesOrder, "invoices")
	if !strings.Contains(invoices, "t.status IN ('CONFIRMED','PARTIALLY_FULFILLED','FULFILLED')") || !strings.Contains(invoices, "sr.line_type='PRODUCT'") || !strings.Contains(invoices, "sr.line_type='SERVICE'") {
		t.Fatalf("invoice source predicate = %q", invoices)
	}
}

func TestSalesCommercialPostsStock(t *testing.T) {
	tests := []struct {
		kind       CommercialKind
		sourceKind string
		want       bool
	}{
		{kind: SalesDispatch, sourceKind: "ORDER", want: true},
		{kind: SalesInvoice, sourceKind: "DISPATCH", want: false},
		{kind: SalesInvoice, sourceKind: "ORDER", want: false},
		{kind: SalesInvoice, sourceKind: "DIRECT", want: true},
		{kind: SalesReturn, sourceKind: "INVOICE", want: true},
	}
	for _, test := range tests {
		if got := salesCommercialPostsStock(test.kind, test.sourceKind); got != test.want {
			t.Fatalf("salesCommercialPostsStock(%q, %q) = %v, want %v", test.kind, test.sourceKind, got, test.want)
		}
	}
}

func TestSalesInvoiceCanUseFulfilledOrder(t *testing.T) {
	if source, ok := conversionSourceKind(SalesInvoice, SalesOrder, "FULFILLED"); !ok || source != "ORDER" {
		t.Fatalf("fulfilled sales order must remain invoiceable, got source=%q ok=%v", source, ok)
	}
}

// A resolved profile with an excise-style ÖTV component alongside VAT used to
// collapse into a single flat rate and silently drop the second component.
// The shared tax engine now sums every component into TaxAmount.
func TestNormalizeCommercialLinesAppliesEveryResolvedTaxComponent(t *testing.T) {
	unexported := CommercialLineInput{
		LineType: "PRODUCT", ProductID: "00000000-0000-4000-8000-000000000001",
		WarehouseID: "00000000-0000-4000-8000-000000000002", UnitCode: "ADET",
		Quantity: "2", UnitPrice: "100",
	}
	unexported.taxComponentsSnapshot = []taxes.TaxComponent{
		{Code: "KDV", CalculationType: taxes.TaxPercentage, Rate: "20"},
		{Code: "OTV", CalculationType: taxes.TaxQuantityBased, Rate: "5"},
	}

	lines, totals, err := normalizeCommercialLines([]CommercialLineInput{unexported}, "", "TRY")
	if err != nil {
		t.Fatalf("normalizeCommercialLines returned error: %v", err)
	}
	// gross=200, VAT=40 (20% of 200), OTV=10 (5 per unit * 2), total tax=50.
	if lines[0].GrossAmount != "200.00000000" || lines[0].TaxAmount != "50.00000000" || lines[0].PayableAmount != "250.00000000" {
		t.Fatalf("multi-component tax totals = %+v", lines[0])
	}
	if len(lines[0].TaxComponentsSnapshot) != 2 {
		t.Fatalf("tax components snapshot not persisted: %+v", lines[0].TaxComponentsSnapshot)
	}
	if totals.TaxTotal != "50.00000000" {
		t.Fatalf("document tax total = %s, want 50.00000000", totals.TaxTotal)
	}
}
