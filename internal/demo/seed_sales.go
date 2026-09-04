package demo

import (
	"context"
	"fmt"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/party"
	"github.com/alpyxn/varyaone/internal/sales"
)

// salesBuilder carries the pieces every seeded sales document needs, so the
// individual scenarios below read as the business story they describe rather
// than as struct literals.
type salesBuilder struct {
	runner *Runner
	built  *catalogue
}

func (b salesBuilder) line(product seededProduct, variantIndex int, count int, warehouseID string) sales.CommercialLineInput {
	return sales.CommercialLineInput{
		LineType: "PRODUCT", ProductID: product.ID, VariantID: product.variantID(variantIndex),
		WarehouseID: warehouseID, UnitCode: product.unit(),
		Quantity: quantity(int64(count)), UnitPrice: product.SalesPrice,
	}
}

func (b salesBuilder) serviceLine(product seededProduct, count int) sales.CommercialLineInput {
	return sales.CommercialLineInput{
		LineType: "SERVICE", ProductID: product.ID, UnitCode: product.unit(),
		Quantity: quantity(int64(count)), UnitPrice: product.SalesPrice,
	}
}

func (b salesBuilder) document(customer party.Party, date time.Time, lines []sales.CommercialLineInput) sales.CommercialDocumentInput {
	due := date.AddDate(0, 0, 21)
	return sales.CommercialDocumentInput{
		BranchID: b.built.scope.branchID, DefaultWarehouseID: b.built.scope.warehouseID,
		PartyID: customer.ID, DocumentDate: date, DueDate: &due, CurrencyCode: "TRY", Lines: lines,
	}
}

// following builds the document that continues a chain: same customer, same
// lines, each one pointing at the source line it fulfils or invoices. Building
// it here rather than through ConvertCommercial keeps the seeder's own dates,
// so a chain reads as a week of trading instead of four documents all dated
// today.
func (b salesBuilder) following(source sales.CommercialDocument, sourceKind string, date time.Time) sales.CommercialDocumentInput {
	lines := make([]sales.CommercialLineInput, 0, len(source.Lines))
	for index, line := range source.Lines {
		next := sales.CommercialLineInput{
			LineNo: index + 1, LineType: line.LineType, UnitCode: line.UnitCode,
			Quantity: line.Quantity, UnitPrice: line.UnitPrice, SourceLineID: line.ID,
			Description: line.Description,
		}
		if line.ProductID != nil {
			next.ProductID = *line.ProductID
		}
		if line.VariantID != nil {
			next.VariantID = *line.VariantID
		}
		if line.WarehouseID != nil {
			next.WarehouseID = *line.WarehouseID
		}
		lines = append(lines, next)
	}
	due := date.AddDate(0, 0, 21)
	return sales.CommercialDocumentInput{
		BranchID: source.BranchID, DefaultWarehouseID: b.built.scope.warehouseID,
		PartyID: source.PartyID, DocumentDate: date, DueDate: &due, CurrencyCode: source.CurrencyCode,
		SourceKind: sourceKind, SourceDocumentID: source.DocumentID, Lines: lines,
	}
}

// onlyProducts drops the service lines from a document that is about to become
// a dispatch note: a delivery moves goods, and the service line stays on the
// order to be invoiced separately.
func onlyProducts(input sales.CommercialDocumentInput) sales.CommercialDocumentInput {
	lines := make([]sales.CommercialLineInput, 0, len(input.Lines))
	for _, line := range input.Lines {
		if line.LineType != "PRODUCT" {
			continue
		}
		line.LineNo = len(lines) + 1
		lines = append(lines, line)
	}
	input.Lines = lines
	return input
}

// seedSales walks the sales chain end to end so every screen has content: open
// quotes, an accepted quote that became an order, a dispatch note that became
// an invoice, an order that is only half delivered, posted invoices across the
// last two months, and a customer return raised against one of them.
func (r *Runner) seedSales(ctx context.Context, session identity.Session, svc *services, built *catalogue) error {
	steps := []func(context.Context, identity.Session, *services, *catalogue) error{
		r.seedSalesInvoices,
		r.seedSalesChain,
		r.seedPartialOrder,
		r.seedOpenOrders,
		r.seedOpenDispatches,
		r.seedQuotes,
		r.seedSalesReturn,
	}
	for _, step := range steps {
		if err := step(ctx, session, svc, built); err != nil {
			return err
		}
	}
	return nil
}

// seedSalesInvoices posts the everyday trade: invoices spread over the last two
// months, each with a couple of product lines, a variant line every third one
// and a service line on some.
func (r *Runner) seedSalesInvoices(ctx context.Context, session identity.Session, svc *services, built *catalogue) error {
	build := salesBuilder{runner: r, built: built}
	plain := built.plainProducts()
	tracked := built.variantProducts()
	for index := 0; index < 10; index++ {
		customer := built.customers[index%len(built.customers)]
		lines := []sales.CommercialLineInput{
			build.line(plain[index%len(plain)], 0, 2+index%4, built.scope.warehouseID),
			build.line(plain[(index+3)%len(plain)], 0, 1+index%3, built.scope.warehouseID),
		}
		if index%3 == 0 {
			product := tracked[index%len(tracked)]
			lines = append(lines, build.line(product, index, 2, built.scope.warehouseID))
			lines = append(lines, build.serviceLine(built.services[0], 2))
		}
		invoice, err := svc.sales.CreateSalesInvoice(ctx, session, build.document(customer, r.day(-52+index*5), lines), seedMeta(fmt.Sprintf("invoice-create-%d", index)))
		if err != nil {
			return err
		}
		posted, err := svc.sales.TransitionCommercial(ctx, session, sales.SalesInvoice, invoice.ID, "post", invoice.Version, seedMeta(fmt.Sprintf("invoice-post-%d", index)), "")
		if err != nil {
			return err
		}
		built.invoiceIDs = append(built.invoiceIDs, posted.ID)
	}
	return nil
}

// seedSalesChain is the whole customer-facing flow in one story: a quote is
// sent and accepted, becomes an order, the order ships on a dispatch note and
// the dispatch note is invoiced. Every document keeps its source relation, so
// each detail screen shows where it came from.
func (r *Runner) seedSalesChain(ctx context.Context, session identity.Session, svc *services, built *catalogue) error {
	build := salesBuilder{runner: r, built: built}
	customer := built.customers[0]
	plain := built.plainProducts()
	tracked := built.variantProducts()
	quote, err := svc.sales.CreateSalesQuote(ctx, session, build.document(customer, r.day(-24), []sales.CommercialLineInput{
		build.line(plain[2], 0, 6, built.scope.warehouseID),
		build.line(tracked[0], 1, 4, built.scope.warehouseID),
	}), seedMeta("chain-quote-create"))
	if err != nil {
		return err
	}
	sent, err := svc.sales.TransitionCommercial(ctx, session, sales.SalesQuote, quote.ID, "send", quote.Version, seedMeta("chain-quote-send"), "")
	if err != nil {
		return err
	}
	accepted, err := svc.sales.TransitionCommercial(ctx, session, sales.SalesQuote, quote.ID, "accept", sent.Version, seedMeta("chain-quote-accept"), "")
	if err != nil {
		return err
	}
	order, err := svc.sales.CreateSalesOrder(ctx, session, build.following(accepted, "QUOTE", r.day(-22)), seedMeta("chain-order-create"))
	if err != nil {
		return err
	}
	confirmed, err := svc.sales.TransitionCommercial(ctx, session, sales.SalesOrder, order.ID, "confirm", order.Version, seedMeta("chain-order-confirm"), "")
	if err != nil {
		return err
	}
	dispatch, err := svc.sales.CreateSalesDispatch(ctx, session, onlyProducts(build.following(confirmed, "ORDER", r.day(-19))), seedMeta("chain-dispatch-create"))
	if err != nil {
		return err
	}
	shipped, err := svc.sales.TransitionCommercial(ctx, session, sales.SalesDispatch, dispatch.ID, "post", dispatch.Version, seedMeta("chain-dispatch-post"), "")
	if err != nil {
		return err
	}
	invoice, err := svc.sales.CreateSalesInvoice(ctx, session, build.following(shipped, "DISPATCH", r.day(-18)), seedMeta("chain-invoice-create"))
	if err != nil {
		return err
	}
	posted, err := svc.sales.TransitionCommercial(ctx, session, sales.SalesInvoice, invoice.ID, "post", invoice.Version, seedMeta("chain-invoice-post"), "")
	if err != nil {
		return err
	}
	built.invoiceIDs = append(built.invoiceIDs, posted.ID)
	return nil
}

// seedPartialOrder leaves an order half delivered: the order screen has to be
// able to show a fulfilment status that is neither open nor closed, and the
// reserved-versus-available split in stock only appears when something is
// ordered but not yet shipped.
func (r *Runner) seedPartialOrder(ctx context.Context, session identity.Session, svc *services, built *catalogue) error {
	build := salesBuilder{runner: r, built: built}
	plain := built.plainProducts()
	order, err := svc.sales.CreateSalesOrder(ctx, session, build.document(built.customers[3], r.day(-12), []sales.CommercialLineInput{
		build.line(plain[1], 0, 10, built.scope.warehouseID),
		build.line(plain[4], 0, 6, built.scope.warehouseID),
	}), seedMeta("partial-order-create"))
	if err != nil {
		return err
	}
	confirmed, err := svc.sales.TransitionCommercial(ctx, session, sales.SalesOrder, order.ID, "confirm", order.Version, seedMeta("partial-order-confirm"), "")
	if err != nil {
		return err
	}
	partial := onlyProducts(build.following(confirmed, "ORDER", r.day(-9)))
	// Ship less than was ordered on the first line and nothing on the second.
	partial.Lines = partial.Lines[:1]
	partial.Lines[0].Quantity = "4"
	dispatch, err := svc.sales.CreateSalesDispatch(ctx, session, partial, seedMeta("partial-dispatch-create"))
	if err != nil {
		return err
	}
	_, err = svc.sales.TransitionCommercial(ctx, session, sales.SalesDispatch, dispatch.ID, "post", dispatch.Version, seedMeta("partial-dispatch-post"), "")
	return err
}

// seedOpenOrders leaves confirmed orders with stock reserved against them and
// nothing shipped yet.
func (r *Runner) seedOpenOrders(ctx context.Context, session identity.Session, svc *services, built *catalogue) error {
	build := salesBuilder{runner: r, built: built}
	plain := built.plainProducts()
	for index := 0; index < 3; index++ {
		customer := built.customers[(index+1)%len(built.customers)]
		order, err := svc.sales.CreateSalesOrder(ctx, session,
			build.document(customer, r.day(-9+index*3), []sales.CommercialLineInput{
				build.line(plain[(index+6)%len(plain)], 0, 4, built.scope.warehouseID),
				build.line(plain[(index+8)%len(plain)], 0, 2, built.scope.warehouseID),
			}), seedMeta(fmt.Sprintf("order-create-%d", index)))
		if err != nil {
			return err
		}
		if _, err = svc.sales.TransitionCommercial(ctx, session, sales.SalesOrder, order.ID, "confirm", order.Version, seedMeta(fmt.Sprintf("order-confirm-%d", index)), ""); err != nil {
			return err
		}
	}
	return nil
}

// seedOpenDispatches posts dispatch notes that have not been invoiced yet -
// the queue the invoicing screen exists for. One ships out of the store
// warehouse and one carries variants.
func (r *Runner) seedOpenDispatches(ctx context.Context, session identity.Session, svc *services, built *catalogue) error {
	build := salesBuilder{runner: r, built: built}
	plain := built.plainProducts()
	tracked := built.variantProducts()
	dispatches := [][]sales.CommercialLineInput{
		{build.line(plain[4], 0, 3, built.scope.warehouseID)},
		{build.line(plain[5], 0, 2, built.scope.storeID)},
		{
			build.line(tracked[1], 0, 2, built.scope.warehouseID),
			build.line(tracked[1], 2, 1, built.scope.warehouseID),
		},
	}
	for index, lines := range dispatches {
		customer := built.customers[(index+2)%len(built.customers)]
		document := build.document(customer, r.day(-6+index*2), lines)
		dispatch, err := svc.sales.CreateSalesDispatch(ctx, session, document, seedMeta(fmt.Sprintf("dispatch-create-%d", index)))
		if err != nil {
			return err
		}
		if _, err = svc.sales.TransitionCommercial(ctx, session, sales.SalesDispatch, dispatch.ID, "post", dispatch.Version, seedMeta(fmt.Sprintf("dispatch-post-%d", index)), ""); err != nil {
			return err
		}
	}
	return nil
}

// seedQuotes leaves the quote screen with one of each state: a draft, one sent
// and waiting, and one the customer accepted.
func (r *Runner) seedQuotes(ctx context.Context, session identity.Session, svc *services, built *catalogue) error {
	build := salesBuilder{runner: r, built: built}
	plain := built.plainProducts()
	commands := []string{"", "send", "accept"}
	for index, command := range commands {
		customer := built.customers[(index+3)%len(built.customers)]
		quote, err := svc.sales.CreateSalesQuote(ctx, session,
			build.document(customer, r.day(-4+index), []sales.CommercialLineInput{
				build.line(plain[(index+2)%len(plain)], 0, 5, built.scope.warehouseID),
				build.serviceLine(built.services[1], 1),
			}), seedMeta(fmt.Sprintf("quote-create-%d", index)))
		if err != nil {
			return err
		}
		version := quote.Version
		if command == "accept" {
			sent, sendErr := svc.sales.TransitionCommercial(ctx, session, sales.SalesQuote, quote.ID, "send", version, seedMeta(fmt.Sprintf("quote-send-%d", index)), "")
			if sendErr != nil {
				return sendErr
			}
			version = sent.Version
		}
		if command == "" {
			continue
		}
		if _, err = svc.sales.TransitionCommercial(ctx, session, sales.SalesQuote, quote.ID, command, version, seedMeta(fmt.Sprintf("quote-%s-%d", command, index)), ""); err != nil {
			return err
		}
	}
	return nil
}

// seedSalesReturn takes goods back from a customer against the invoice that
// sold them. The return is what puts a credit note in the customer ledger and
// an inbound movement priced from the original sale's own cost layers.
func (r *Runner) seedSalesReturn(ctx context.Context, session identity.Session, svc *services, built *catalogue) error {
	if len(built.invoiceIDs) == 0 {
		return nil
	}
	source, err := svc.sales.GetCommercialDocument(ctx, session, sales.SalesInvoice, built.invoiceIDs[1])
	if err != nil {
		return err
	}
	build := salesBuilder{runner: r, built: built}
	input := build.following(source, "INVOICE", r.day(-8))
	input.Reason = "Müşteri üründen memnun kalmadı"
	// Only the first product line comes back; a return of everything that was
	// ever invoiced is not what a return screen usually shows.
	for _, line := range input.Lines {
		if line.LineType != "PRODUCT" {
			continue
		}
		line.Quantity = "1"
		input.Lines = []sales.CommercialLineInput{line}
		break
	}
	created, err := svc.sales.CreateSalesReturn(ctx, session, input, seedMeta("sales-return-create"))
	if err != nil {
		return err
	}
	_, err = svc.sales.TransitionCommercial(ctx, session, sales.SalesReturn, created.ID, "post", created.Version, seedMeta("sales-return-post"), "")
	return err
}
