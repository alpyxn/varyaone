package purchasing

import "testing"

func TestPurchaseDiscountChainSkipsZeroDiscount(t *testing.T) {
	line := &PurchaseInvoiceLine{DiscountAmount: "0"}
	if chain := purchaseDiscountChain(line); len(chain) != 0 {
		t.Fatalf("zero discount produced a chain: %+v", chain)
	}
	line.DiscountAmount = ""
	if chain := purchaseDiscountChain(line); len(chain) != 0 {
		t.Fatalf("empty discount produced a chain: %+v", chain)
	}
}

func TestPurchaseDiscountChainWrapsFixedAmount(t *testing.T) {
	line := &PurchaseInvoiceLine{DiscountAmount: "15.5"}
	chain := purchaseDiscountChain(line)
	if len(chain) != 1 || chain[0].Amount != "15.5" {
		t.Fatalf("discount chain = %+v", chain)
	}
}
