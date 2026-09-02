package sales

import "testing"

func TestCommercialLifecycleStatusSeparatesOrderAndPostingStates(t *testing.T) {
	tests := []struct {
		kind         CommercialKind
		status, want string
	}{
		{SalesOrder, "CONFIRMED", LifecycleOpen},
		{SalesOrder, "FULFILLED", LifecycleOpen},
		{SalesDispatch, "POSTED", LifecycleFinalized},
		{SalesInvoice, "DRAFT", LifecycleDraft},
		{SalesQuote, "ACCEPTED", "ACCEPTED"},
	}
	for _, test := range tests {
		if got := commercialLifecycleStatus(test.kind, test.status); got != test.want {
			t.Fatalf("commercialLifecycleStatus(%q, %q) = %q, want %q", test.kind, test.status, got, test.want)
		}
	}
}

func TestCommercialInvoicingStatusUsesRemainingQuantities(t *testing.T) {
	lines := []CommercialLine{
		{Quantity: "10", BaseQuantity: "10", RemainingInvoicingQuantity: "4", RemainingInvoicingBaseQuantity: "4"},
		{Quantity: "5", BaseQuantity: "5", RemainingInvoicingQuantity: "0", RemainingInvoicingBaseQuantity: "0"},
	}
	if got := commercialInvoicingStatus(lines); got != InvoicingPartial {
		t.Fatalf("commercialInvoicingStatus() = %q, want %q", got, InvoicingPartial)
	}
}

func TestCommercialOrderInvoicingStatusCountsOnlyFulfilledProducts(t *testing.T) {
	lines := []CommercialLine{
		{LineType: "PRODUCT", Quantity: "10", BaseQuantity: "10", RemainingFulfillmentBaseQuantity: "4", RemainingInvoicingBaseQuantity: "2"},
		{LineType: "SERVICE", Quantity: "3", BaseQuantity: "3", RemainingInvoicingBaseQuantity: "3"},
	}
	if got := commercialInvoicingStatusForKind(lines, SalesOrder); got != InvoicingPartial {
		t.Fatalf("order invoicing status = %q, want %q", got, InvoicingPartial)
	}
}
