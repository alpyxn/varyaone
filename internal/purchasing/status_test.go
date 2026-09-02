package purchasing

import "testing"

func TestPurchaseLifecycleStatusSeparatesOrderAndPostingStates(t *testing.T) {
	if got := purchaseLifecycleStatus(PurchaseOrderKind, "CONFIRMED"); got != PurchaseLifecycleOpen {
		t.Fatalf("confirmed purchase order lifecycle = %q, want %q", got, PurchaseLifecycleOpen)
	}
	if got := purchaseLifecycleStatus(GoodsReceiptKind, "POSTED"); got != PurchaseLifecycleFinalized {
		t.Fatalf("posted goods receipt lifecycle = %q, want %q", got, PurchaseLifecycleFinalized)
	}
}

func TestPurchaseFulfillmentStatusUsesServiceAndProductRules(t *testing.T) {
	lines := []PurchaseOrderLine{
		{LineType: "PRODUCT", OrderedQuantity: "10", ReceivedQuantity: "4"},
		{LineType: "SERVICE", OrderedQuantity: "5", InvoicedQuantity: "5"},
	}
	if got := purchaseFulfillmentStatus(lines, "CONFIRMED"); got != PurchaseFulfillmentPartial {
		t.Fatalf("purchaseFulfillmentStatus() = %q, want %q", got, PurchaseFulfillmentPartial)
	}
}
