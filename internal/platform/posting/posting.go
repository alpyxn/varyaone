// Package posting holds the composition-root adapters between the commercial
// transaction boundaries and the inventory/finance posting commands. They keep
// the bounded contexts independent while preserving one pgx transaction.
//
// They live in their own package because more than one composition root needs
// them: the HTTP server and worker (internal/platform/app) and the demo seeder
// (internal/demo), which drives the same services without an HTTP request.
package posting

import (
	"context"

	"github.com/alpyxn/varyaone/internal/finance"
	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/inventory"
	"github.com/alpyxn/varyaone/internal/purchasing"
	"github.com/alpyxn/varyaone/internal/sales"
	"github.com/jackc/pgx/v5"
)

// InventoryStockPoster adapts the sales and purchasing transaction boundaries
// to inventory's provider-neutral posting commands.
type InventoryStockPoster struct{ Service *inventory.Service }

func (a InventoryStockPoster) PostInvoiceTx(ctx context.Context, tx pgx.Tx, session identity.Session, input sales.StockPostingInput) error {
	lines := make([]inventory.InvoiceStockLine, 0, len(input.Lines))
	for _, line := range input.Lines {
		lines = append(lines, inventory.InvoiceStockLine{LineID: line.LineID, ProductID: line.ProductID, VariantID: line.VariantID, Quantity: line.Quantity, UnitCode: line.UnitCode, UnitCost: line.UnitCost, Currency: line.Currency})
	}
	return a.Service.PostInvoiceMovementsTx(ctx, tx, session, inventory.InvoiceStockPostingInput{
		DocumentID: input.DocumentID, DocumentType: input.DocumentType, WarehouseID: input.WarehouseID, Lines: lines,
	})
}

func (a InventoryStockPoster) ReverseInvoiceTx(ctx context.Context, tx pgx.Tx, session identity.Session, input sales.StockReversalInput) error {
	return a.Service.ReverseInvoiceMovementsTx(ctx, tx, session, inventory.InvoiceStockReversalInput{
		DocumentID: input.DocumentID, DocumentType: input.DocumentType, WarehouseID: input.WarehouseID,
		ReversalKey: input.ReversalKey, Reason: input.Reason,
	})
}

func (a InventoryStockPoster) PostCommercialStockTx(ctx context.Context, tx pgx.Tx, session identity.Session, input sales.CommercialStockPostingInput) error {
	lines := make([]inventory.CommercialStockLine, 0, len(input.Lines))
	for _, line := range input.Lines {
		lines = append(lines, inventory.CommercialStockLine{
			LineID: line.LineID, ProductID: line.ProductID, VariantID: line.VariantID,
			WarehouseID: line.WarehouseID, Quantity: line.Quantity, BaseQuantity: line.BaseQuantity,
			ConversionFactor: line.ConversionFactor, UnitCode: line.UnitCode,
			UnitCost: line.UnitCost, Currency: line.Currency,
		})
	}
	return a.Service.PostCommercialStockMovementsTx(ctx, tx, session, inventory.CommercialStockPostingInput{
		DocumentID: input.DocumentID, DocumentType: input.DocumentType, Lines: lines,
	})
}

func (a InventoryStockPoster) ReverseCommercialStockTx(ctx context.Context, tx pgx.Tx, session identity.Session, input sales.CommercialStockReversalInput) error {
	return a.Service.ReverseInvoiceMovementsTx(ctx, tx, session, inventory.InvoiceStockReversalInput{
		DocumentID: input.DocumentID, DocumentType: input.DocumentType,
		ReversalKey: input.ReversalKey, Reason: input.Reason,
	})
}

func (a InventoryStockPoster) ReserveSalesOrderTx(ctx context.Context, tx pgx.Tx, session identity.Session, input sales.SalesReservationInput) error {
	lines := make([]inventory.SalesReservationLine, 0, len(input.Lines))
	for _, line := range input.Lines {
		lines = append(lines, inventory.SalesReservationLine{
			OrderLineID: line.OrderLineID, ProductID: line.ProductID, VariantID: line.VariantID,
			WarehouseID: line.WarehouseID, Quantity: line.Quantity,
		})
	}
	return a.Service.ReserveSalesOrderTx(ctx, tx, session, inventory.SalesReservationInput{OrderID: input.OrderID, Lines: lines})
}

func (a InventoryStockPoster) ConsumeSalesOrderReservationsTx(ctx context.Context, tx pgx.Tx, session identity.Session, lines []sales.SalesReservationConsumption) error {
	items := make([]inventory.SalesReservationConsumption, 0, len(lines))
	for _, line := range lines {
		items = append(items, inventory.SalesReservationConsumption{OrderID: line.OrderID, OrderLineID: line.OrderLineID, ProductID: line.ProductID, VariantID: line.VariantID, WarehouseID: line.WarehouseID, Quantity: line.Quantity})
	}
	return a.Service.ConsumeSalesOrderReservationsTx(ctx, tx, session, items)
}

func (a InventoryStockPoster) ReleaseSalesOrderReservationsTx(ctx context.Context, tx pgx.Tx, session identity.Session, orderID string) error {
	return a.Service.ReleaseSalesOrderReservationsTx(ctx, tx, session, orderID)
}

func (a InventoryStockPoster) RestoreSalesOrderReservationsTx(ctx context.Context, tx pgx.Tx, session identity.Session, lines []sales.SalesReservationConsumption) error {
	items := make([]inventory.SalesReservationConsumption, 0, len(lines))
	for _, line := range lines {
		items = append(items, inventory.SalesReservationConsumption{OrderID: line.OrderID, OrderLineID: line.OrderLineID, ProductID: line.ProductID, VariantID: line.VariantID, WarehouseID: line.WarehouseID, Quantity: line.Quantity})
	}
	return a.Service.RestoreSalesOrderReservationsTx(ctx, tx, session, items)
}

func (a InventoryStockPoster) PostPurchaseReceiptMovementsTx(ctx context.Context, tx pgx.Tx, session identity.Session, input inventory.PurchaseReceiptStockPostingInput) error {
	return a.Service.PostPurchaseReceiptMovementsTx(ctx, tx, session, input)
}

func (a InventoryStockPoster) PostPurchaseInvoiceMovementsTx(ctx context.Context, tx pgx.Tx, session identity.Session, input inventory.PurchaseReceiptStockPostingInput) error {
	return a.Service.PostPurchaseInvoiceMovementsTx(ctx, tx, session, input)
}

func (a InventoryStockPoster) PostPurchaseReturnMovementsTx(ctx context.Context, tx pgx.Tx, session identity.Session, input inventory.PurchaseReturnStockPostingInput) error {
	return a.Service.PostPurchaseReturnMovementsTx(ctx, tx, session, input)
}

func (a InventoryStockPoster) ReversePurchaseMovementsTx(ctx context.Context, tx pgx.Tx, session identity.Session, input inventory.PurchaseStockReversalInput) error {
	return a.Service.ReverseInvoiceMovementsTx(ctx, tx, session, inventory.InvoiceStockReversalInput{
		DocumentID: input.DocumentID, DocumentType: input.SourceType, WarehouseID: input.WarehouseID,
		ReversalKey: input.ReversalKey, Reason: input.Reason,
	})
}

type FinancePurchasePoster struct{ Service *finance.Service }

func (a FinancePurchasePoster) ReadDocumentSettlement(ctx context.Context, companyID, documentID string) (finance.DocumentSettlement, error) {
	return a.Service.ReadDocumentSettlement(ctx, companyID, documentID)
}

func (a FinancePurchasePoster) PostPurchaseInvoiceTx(ctx context.Context, tx pgx.Tx, session identity.Session, input purchasing.PurchaseInvoicePostingInput) (string, error) {
	return a.post(ctx, tx, session, input, "PURCHASE_INVOICE", "purchase-invoice:")
}

func (a FinancePurchasePoster) PostPurchaseReturnTx(ctx context.Context, tx pgx.Tx, session identity.Session, input purchasing.PurchaseInvoicePostingInput) (string, error) {
	return a.post(ctx, tx, session, input, "PURCHASE_RETURN_INVOICE", "purchase-return:")
}

func (a FinancePurchasePoster) ReversePurchaseInvoiceTx(ctx context.Context, tx pgx.Tx, session identity.Session, documentID, reversalKey, reason string) (string, error) {
	return a.Service.ReverseInvoiceTx(ctx, tx, session, documentID, reversalKey, reason)
}

func (a FinancePurchasePoster) ReversePurchaseReturnTx(ctx context.Context, tx pgx.Tx, session identity.Session, documentID, reversalKey, reason string) (string, error) {
	return a.Service.ReverseInvoiceTx(ctx, tx, session, documentID, reversalKey, reason)
}

func (a FinancePurchasePoster) post(ctx context.Context, tx pgx.Tx, session identity.Session, input purchasing.PurchaseInvoicePostingInput, documentType, keyPrefix string) (string, error) {
	posting, err := a.Service.PostInvoiceTx(ctx, tx, session, finance.InvoicePostingInput{
		DocumentID: input.InvoiceID, DocumentType: documentType, PartyID: input.SupplierID,
		Currency: input.Currency, Amount: input.Amount, ExchangeRate: input.ExchangeRate, DocumentDate: input.InvoiceDate,
		DueDate: input.DueDate, Description: input.Description,
		IdempotencyKey: keyPrefix + input.InvoiceID,
	})
	if err != nil {
		return "", err
	}
	return posting.ID, nil
}
