package demo

import (
	"context"
	"fmt"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/party"
	"github.com/alpyxn/varyaone/internal/purchasing"
)

// seededReceipt keeps a finalized goods receipt around: it is what a purchase
// return has to be raised against, and what makes the supplier side of the demo
// a chain rather than a pile of invoices.
type seededReceipt struct {
	receipt  purchasing.GoodsReceipt
	supplier party.Party
}

func (r *Runner) seedPurchases(ctx context.Context, session identity.Session, svc *services, built *catalogue) error {
	steps := []func(context.Context, identity.Session, *services, *catalogue) error{
		r.seedOpeningPurchases,
		r.seedPurchaseOrderChain,
		r.seedPurchaseReturn,
	}
	for _, step := range steps {
		if err := step(ctx, session, svc, built); err != nil {
			return err
		}
	}
	return nil
}

// purchaseInvoiceLines expands one product into the invoice lines it needs. A
// variant-tracked product may never appear on a line without a variant, so it
// contributes one line per variant; a plain product contributes one.
func purchaseInvoiceLines(product seededProduct, warehouseID string, base int64, startNo int) []purchasing.PurchaseInvoiceLine {
	unitPrice := minor(product.spec.purchase)
	build := func(lineNo int, variantID, name string, count int64) purchasing.PurchaseInvoiceLine {
		gross := count * unitPrice
		tax := gross * demoTaxRate / 100
		return purchasing.PurchaseInvoiceLine{
			LineNo: lineNo, LineType: "PRODUCT", ProductID: product.ID, VariantID: variantID,
			WarehouseID: warehouseID, UnitCode: product.unit(), DescriptionSnapshot: name,
			Quantity: quantity(count), UnitPrice: money(unitPrice),
			GrossAmount: money(gross), DiscountAmount: "0.00", TaxBase: money(gross),
			TaxAmount: money(tax), WithholdingAmount: "0.00", PayableAmount: money(gross + tax),
		}
	}
	if len(product.variants) == 0 {
		return []purchasing.PurchaseInvoiceLine{build(startNo, "", product.Name, base)}
	}
	lines := make([]purchasing.PurchaseInvoiceLine, 0, len(product.variants))
	for index, variant := range product.variants {
		// Uneven quantities per variant: a demo where every colour holds the
		// same count tells the reader nothing about variant stock.
		lines = append(lines, build(startNo+index, variant.ID, product.Name+" "+variant.VariantCode, base+int64(index*6)))
	}
	return lines
}

// seedOpeningPurchases brings the opening stock in the way a real business
// does: as posted standalone supplier invoices. That gives every product a cost
// layer and every supplier an open payable, so stock valuation, aging and the
// supplier ledger all have something true to show. Everything lands in the main
// depot first; the store is stocked separately below, so no other step has to
// know which supplier happened to deliver where.
func (r *Runner) seedOpeningPurchases(ctx context.Context, session identity.Session, svc *services, built *catalogue) error {
	for index, supplier := range built.suppliers {
		lines := []purchasing.PurchaseInvoiceLine{}
		for productIndex, product := range built.products {
			if productIndex%len(built.suppliers) != index {
				continue
			}
			lines = append(lines, purchaseInvoiceLines(product, built.scope.warehouseID, int64(40+productIndex*5), len(lines)+1)...)
		}
		if len(lines) == 0 {
			continue
		}
		if err := r.postOpeningInvoice(ctx, session, svc, built, supplier.ID, built.scope.warehouseID,
			r.day(-60+index*3), lines, fmt.Sprintf("purchase-%d", index)); err != nil {
			return err
		}
	}
	return r.seedStoreOpeningPurchase(ctx, session, svc, built)
}

// seedStoreOpeningPurchase stocks the second warehouse from its own supplier
// invoice. Without it the store would only ever hold what a transfer put there,
// and no document in the demo would ship from anywhere but the main depot.
func (r *Runner) seedStoreOpeningPurchase(ctx context.Context, session identity.Session, svc *services, built *catalogue) error {
	plain := built.plainProducts()
	lines := []purchasing.PurchaseInvoiceLine{}
	for _, index := range []int{5, 6, 8} {
		lines = append(lines, purchaseInvoiceLines(plain[index], built.scope.storeID, int64(25+index*4), len(lines)+1)...)
	}
	return r.postOpeningInvoice(ctx, session, svc, built, built.suppliers[len(built.suppliers)-1].ID,
		built.scope.storeID, r.day(-48), lines, "purchase-store")
}

func (r *Runner) postOpeningInvoice(ctx context.Context, session identity.Session, svc *services, built *catalogue,
	supplierID, warehouseID string, invoiceDate time.Time, lines []purchasing.PurchaseInvoiceLine, key string) error {
	dueDate := invoiceDate.AddDate(0, 0, 30)
	invoice, err := svc.purchasing.CreatePurchaseInvoice(ctx, session, purchasing.PurchaseInvoiceInput{
		SupplierID: supplierID, BranchID: built.scope.branchID, WarehouseID: warehouseID,
		Standalone: true, InvoiceDate: invoiceDate, DueDate: &dueDate, Currency: "TRY", Lines: lines,
	}, seedMeta(key+"-create"))
	if err != nil {
		return err
	}
	_, err = svc.purchasing.FinalizePurchaseInvoice(ctx, session, invoice.ID, invoice.Version, seedMeta(key+"-finalize"))
	return err
}

// seedPurchaseOrderChain walks the supplier side end to end: an order is
// confirmed, the goods arrive on a receipt (one line short of what was ordered
// and one unit of it damaged, which is what receiving actually looks like), and
// the supplier invoice is raised against that receipt. Without it the demo has
// no purchase order, no incoming dispatch note and no document relations on the
// buying side at all.
func (r *Runner) seedPurchaseOrderChain(ctx context.Context, session identity.Session, svc *services, built *catalogue) error {
	supplier := built.suppliers[len(built.suppliers)-2]
	orderDate := r.day(-34)
	lines := []purchasing.PurchaseOrderLine{}
	add := func(product seededProduct, variantID, name string, count int64) {
		lines = append(lines, purchasing.PurchaseOrderLine{
			LineNo: len(lines) + 1, LineType: "PRODUCT", ProductID: product.ID, VariantID: variantID,
			WarehouseID: built.scope.warehouseID, ProductNameSnapshot: name, ProductCodeSnapshot: product.Code,
			UnitCode: product.unit(), OrderedQuantity: quantity(count), UnitPrice: money(minor(product.spec.purchase)),
			Currency: "TRY",
		})
	}
	for _, product := range built.variantProducts() {
		for index, variant := range product.variants {
			add(product, variant.ID, product.Name+" "+variant.VariantCode, int64(10+index*4))
		}
	}
	for index, product := range built.plainProducts() {
		if index%3 != 0 {
			continue
		}
		add(product, "", product.Name, int64(12+index*2))
	}
	order, err := svc.purchasing.CreatePurchaseOrder(ctx, session, purchasing.PurchaseOrderInput{
		SupplierID: supplier.ID, BranchID: built.scope.branchID, WarehouseID: built.scope.warehouseID,
		OrderDate: orderDate, Currency: "TRY", OverDeliveryPolicy: "WARN",
		Notes: "Çeyrek dönem stok tamamlama siparişi", Lines: lines,
	}, seedMeta("purchase-order-create"))
	if err != nil {
		return err
	}
	confirmed, err := svc.purchasing.ConfirmPurchaseOrder(ctx, session, order.ID, order.Version, seedMeta("purchase-order-confirm"))
	if err != nil {
		return err
	}

	receiptLines := make([]purchasing.GoodsReceiptLine, 0, len(confirmed.Lines))
	for index, line := range confirmed.Lines {
		orderLineID := line.ID
		accepted, damaged := line.OrderedQuantity, "0"
		// The last line arrives short and one unit of the first arrives
		// damaged, so the receipt screen shows what a partial delivery looks
		// like and the order stays partially fulfilled.
		switch {
		case index == 0:
			accepted, damaged = subtractWhole(line.OrderedQuantity, 1), "1"
		case index == len(confirmed.Lines)-1:
			accepted = subtractWhole(line.OrderedQuantity, 3)
		}
		receiptLines = append(receiptLines, purchasing.GoodsReceiptLine{
			PurchaseOrderLineID: &orderLineID, ProductID: line.ProductID, VariantID: line.VariantID,
			WarehouseID: line.WarehouseID, UnitCode: line.UnitCode,
			AcceptedQuantity: accepted, DamagedQuantity: damaged, RejectedQuantity: "0",
			UnitCost: line.UnitPrice, Currency: "TRY",
		})
	}
	receipt, err := svc.purchasing.CreateGoodsReceipt(ctx, session, purchasing.GoodsReceiptInput{
		PurchaseOrderID: confirmed.ID, SupplierID: supplier.ID, BranchID: built.scope.branchID,
		WarehouseID: built.scope.warehouseID, ReceiptDate: r.day(-30), Currency: "TRY",
		Notes: "Sipariş teslimatı", Lines: receiptLines,
	}, seedMeta("goods-receipt-create"))
	if err != nil {
		return err
	}
	posted, err := svc.purchasing.FinalizeGoodsReceipt(ctx, session, receipt.ID, receipt.Version, seedMeta("goods-receipt-finalize"))
	if err != nil {
		return err
	}
	built.receipts = append(built.receipts, seededReceipt{receipt: posted, supplier: supplier})

	invoiceLines := make([]purchasing.PurchaseInvoiceLine, 0, len(posted.Lines))
	for index, line := range posted.Lines {
		unitPrice := minor(line.UnitCost)
		count := minor(line.AcceptedQuantity) / 100
		if count == 0 {
			continue
		}
		gross := count * unitPrice
		tax := gross * demoTaxRate / 100
		invoiceLines = append(invoiceLines, purchasing.PurchaseInvoiceLine{
			LineNo: index + 1, LineType: "PRODUCT", GoodsReceiptLineID: line.ID,
			ProductID: line.ProductID, VariantID: line.VariantID, WarehouseID: line.WarehouseID,
			UnitCode: line.UnitCode, DescriptionSnapshot: built.lineDescription(line.ProductID, line.VariantID),
			Quantity: quantity(count), UnitPrice: money(unitPrice),
			GrossAmount: money(gross), DiscountAmount: "0.00", TaxBase: money(gross),
			TaxAmount: money(tax), WithholdingAmount: "0.00", PayableAmount: money(gross + tax),
		})
	}
	invoiceDate := r.day(-29)
	dueDate := invoiceDate.AddDate(0, 0, 30)
	invoice, err := svc.purchasing.CreatePurchaseInvoice(ctx, session, purchasing.PurchaseInvoiceInput{
		SupplierID: supplier.ID, BranchID: built.scope.branchID, WarehouseID: built.scope.warehouseID,
		GoodsReceiptID: posted.ID, InvoiceDate: invoiceDate, DueDate: &dueDate, Currency: "TRY", Lines: invoiceLines,
	}, seedMeta("purchase-chain-invoice-create"))
	if err != nil {
		return err
	}
	_, err = svc.purchasing.FinalizePurchaseInvoice(ctx, session, invoice.ID, invoice.Version, seedMeta("purchase-chain-invoice-finalize"))
	return err
}

// seedPurchaseReturn sends part of a delivery back to the supplier. It is the
// only thing that puts an outgoing return document, its stock effect and the
// supplier debit note on the screens at all.
func (r *Runner) seedPurchaseReturn(ctx context.Context, session identity.Session, svc *services, built *catalogue) error {
	if len(built.receipts) == 0 {
		return nil
	}
	source := built.receipts[0]
	lines := []purchasing.PurchaseReturnLine{}
	for _, line := range source.receipt.Lines {
		if len(lines) == 2 {
			break
		}
		count := minor(line.AcceptedQuantity) / 100
		if count < 4 {
			continue
		}
		lines = append(lines, purchasing.PurchaseReturnLine{
			LineNo: len(lines) + 1, SourceReceiptLineID: line.ID, ProductID: line.ProductID,
			VariantID: line.VariantID, WarehouseID: line.WarehouseID, UnitCode: line.UnitCode,
			Quantity: "2", UnitCost: line.UnitCost, Currency: "TRY",
			Reason: "Ambalaj hasarlı",
		})
	}
	if len(lines) == 0 {
		return nil
	}
	created, err := svc.purchasing.CreatePurchaseReturn(ctx, session, purchasing.PurchaseReturnInput{
		SupplierID: source.supplier.ID, BranchID: built.scope.branchID, WarehouseID: built.scope.warehouseID,
		SourceReceiptID: source.receipt.ID, ReturnDate: r.day(-26), Currency: "TRY",
		Reason: "Hasarlı ürün iadesi", Lines: lines,
	}, seedMeta("purchase-return-create"))
	if err != nil {
		return err
	}
	_, err = svc.purchasing.FinalizePurchaseReturn(ctx, session, created.ID, created.Version, seedMeta("purchase-return-finalize"))
	return err
}

// subtractWhole takes a whole count off a decimal quantity literal the service
// layer produced, keeping the seeder's arithmetic in the same integer domain as
// minor().
func subtractWhole(value string, amount int64) string {
	remaining := minor(value)/100 - amount
	if remaining < 0 {
		remaining = 0
	}
	return quantity(remaining)
}
