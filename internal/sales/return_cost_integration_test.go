package sales

import (
	"context"
	"testing"
	"time"
)

func TestReturnCostFollowsInvoiceDispatchSource(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	f := newDoubleEffectFixture(t, ctx)
	f.session.Permissions = append(f.session.Permissions, "sales.return.post", "sales.return.read")
	order := f.confirmedOrder(t, ctx, "10")
	dispatch, e := f.service.ConvertCommercial(ctx, f.session, SalesDispatch, order.ID, order.Version, f.meta(), "")
	if e != nil {
		t.Fatal(e)
	}
	dispatch, e = f.service.PostSalesDispatch(ctx, f.session, dispatch.ID, dispatch.Version, f.meta())
	if e != nil {
		t.Fatal(e)
	}
	invoice, e := f.service.ConvertCommercial(ctx, f.session, SalesInvoice, dispatch.ID, dispatch.Version, f.meta(), "")
	if e != nil {
		t.Fatal(e)
	}
	invoice, e = f.service.PostSalesInvoice(ctx, f.session, invoice.ID, invoice.Version, f.meta())
	if e != nil {
		t.Fatal(e)
	}
	returned, e := f.service.ConvertCommercial(ctx, f.session, SalesReturn, invoice.ID, invoice.Version, f.meta(), "İade")
	if e != nil {
		t.Fatal(e)
	}
	returned, e = f.service.PostSalesReturn(ctx, f.session, returned.ID, returned.Version, f.meta())
	if e != nil {
		t.Fatal(e)
	}
	var layerCost, layerQuantity string
	if e = f.pool.QueryRow(ctx, `SELECT l.unit_cost::text,l.quantity::text FROM stock_cost_layers l JOIN stock_movements m ON m.company_id=l.company_id AND m.id=l.source_movement_id WHERE m.company_id=$1 AND m.source_line_id=$2`, f.companyID, returned.Lines[0].ID).Scan(&layerCost, &layerQuantity); e != nil {
		t.Fatal(e)
	}
	if layerCost != "80.00000000" || layerQuantity != "10.00000000" {
		t.Fatalf("returned layer: quantity=%s cost=%s", layerQuantity, layerCost)
	}
	tx, e := f.pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	direct, _, e := resolveSalesReturnLineCostTx(ctx, tx, f.companyID, &dispatch.Lines[0].ID)
	if e != nil {
		t.Fatal(e)
	}
	via, _, e := resolveSalesReturnLineCostTx(ctx, tx, f.companyID, &invoice.Lines[0].ID)
	if e != nil {
		t.Fatal(e)
	}
	t.Logf("dispatch original cost=%q, invoice source return cost=%q", direct, via)
	if direct != "80.00000000" || via != direct {
		t.Errorf("invoice sourced from dispatch lost the return's known original stock cost")
	}
}
