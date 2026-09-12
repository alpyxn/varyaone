package sales

import (
	"context"
	"testing"
	"time"

	"github.com/alpyxn/varyaone/internal/purchasing"
	"github.com/google/uuid"
)

func TestInvoiceDatesDoNotInheritRetiredPartyPaymentTerms(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	f := newDoubleEffectFixture(t, ctx)
	termID := uuid.NewString()
	if _, err := f.pool.Exec(ctx, `INSERT INTO payment_terms(id,company_id,code,name,due_days) VALUES($1,$2,'NET30','30 gün',30)`, termID, f.companyID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE parties SET payment_term_id=$1,is_supplier=true WHERE id=$2 AND company_id=$3`, termID, f.partyID, f.companyID); err != nil {
		t.Fatal(err)
	}
	date := time.Now().UTC().Truncate(24 * time.Hour)
	explicit := date.AddDate(0, 0, 45)
	for _, due := range []*time.Time{nil, &explicit} {
		input := CommercialDocumentInput{BranchID: f.branchID, DefaultWarehouseID: f.warehouseID, PartyID: f.partyID, DocumentDate: date, DueDate: due, CurrencyCode: "TRY", Lines: []CommercialLineInput{{LineType: "PRODUCT", ProductID: f.productID, WarehouseID: f.warehouseID, UnitCode: "ADET", Quantity: "1", UnitPrice: "100"}}}
		invoice, err := f.service.CreateSalesInvoice(ctx, f.session, input, f.meta())
		if err != nil {
			t.Fatal(err)
		}
		if invoice.PaymentTermID != nil {
			t.Fatal("sales invoice inherited party term")
		}
		if due == nil && invoice.DueDate != nil || due != nil && (invoice.DueDate == nil || !invoice.DueDate.Equal(*due)) {
			t.Fatalf("sales due date = %v, want %v", invoice.DueDate, due)
		}
	}
	f.session.Permissions = append(f.session.Permissions, "purchase.invoice.draft", "purchase.invoice.read", "purchase.invoice.standalone", "purchase.price.override")
	purchases := purchasing.NewService(f.pool, nil, nil)
	for _, due := range []*time.Time{nil, &explicit} {
		invoice, err := purchases.CreatePurchaseInvoice(ctx, f.session, purchasing.PurchaseInvoiceInput{Standalone: true, SupplierID: f.partyID, BranchID: f.branchID, WarehouseID: f.warehouseID, InvoiceDate: date, DueDate: due, Currency: "TRY", Lines: []purchasing.PurchaseInvoiceLine{{LineType: "PRODUCT", ProductID: f.productID, UnitCode: "ADET", Quantity: "1", UnitPrice: "100", DescriptionSnapshot: "Test ürün", GrossAmount: "100", DiscountAmount: "0", TaxBase: "100", TaxAmount: "0", WithholdingAmount: "0", PayableAmount: "100"}}}, f.meta())
		if err != nil {
			t.Fatal(err)
		}
		if due == nil && invoice.DueDate != nil || due != nil && (invoice.DueDate == nil || !invoice.DueDate.Equal(*due)) {
			t.Fatalf("purchase due date = %v, want %v", invoice.DueDate, due)
		}
	}
}
