package app

import (
	"context"

	"github.com/alpyxn/varyaone/internal/finance"
	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/inventory"
	"github.com/alpyxn/varyaone/internal/purchasing"
	"github.com/alpyxn/varyaone/internal/sales"
	"github.com/jackc/pgx/v5"
)

// inventoryStockPoster is the composition-root adapter between the sales
// transaction boundary and inventory's provider-neutral posting command. It
// keeps the bounded contexts independent while preserving one pgx transaction.
type inventoryStockPoster struct{ service *inventory.Service }

func (a inventoryStockPoster) PostInvoiceTx(ctx context.Context, tx pgx.Tx, session identity.Session, input sales.StockPostingInput) error {
	lines := make([]inventory.InvoiceStockLine, 0, len(input.Lines))
	for _, line := range input.Lines {
		lines = append(lines, inventory.InvoiceStockLine{LineID: line.LineID, ProductID: line.ProductID, VariantID: line.VariantID, Quantity: line.Quantity, UnitCode: line.UnitCode, UnitCost: line.UnitCost, Currency: line.Currency})
	}
	return a.service.PostInvoiceMovementsTx(ctx, tx, session, inventory.InvoiceStockPostingInput{
		DocumentID: input.DocumentID, DocumentType: input.DocumentType, WarehouseID: input.WarehouseID, Lines: lines,
	})
}

func (a inventoryStockPoster) ReverseInvoiceTx(ctx context.Context, tx pgx.Tx, session identity.Session, input sales.StockReversalInput) error {
	return a.service.ReverseInvoiceMovementsTx(ctx, tx, session, inventory.InvoiceStockReversalInput{
		DocumentID: input.DocumentID, DocumentType: input.DocumentType, WarehouseID: input.WarehouseID,
		ReversalKey: input.ReversalKey, Reason: input.Reason,
	})
}

func (a inventoryStockPoster) PostCommercialStockTx(ctx context.Context, tx pgx.Tx, session identity.Session, input sales.CommercialStockPostingInput) error {
	lines := make([]inventory.CommercialStockLine, 0, len(input.Lines))
	for _, line := range input.Lines {
		lines = append(lines, inventory.CommercialStockLine{
			LineID: line.LineID, ProductID: line.ProductID, VariantID: line.VariantID,
			WarehouseID: line.WarehouseID, Quantity: line.Quantity, BaseQuantity: line.BaseQuantity,
			ConversionFactor: line.ConversionFactor, UnitCode: line.UnitCode,
			UnitCost: line.UnitCost, Currency: line.Currency,
		})
	}
	return a.service.PostCommercialStockMovementsTx(ctx, tx, session, inventory.CommercialStockPostingInput{
		DocumentID: input.DocumentID, DocumentType: input.DocumentType, Lines: lines,
	})
}

func (a inventoryStockPoster) ReverseCommercialStockTx(ctx context.Context, tx pgx.Tx, session identity.Session, input sales.CommercialStockReversalInput) error {
	return a.service.ReverseInvoiceMovementsTx(ctx, tx, session, inventory.InvoiceStockReversalInput{
		DocumentID: input.DocumentID, DocumentType: input.DocumentType,
		ReversalKey: input.ReversalKey, Reason: input.Reason,
	})
}

func (a inventoryStockPoster) ReserveSalesOrderTx(ctx context.Context, tx pgx.Tx, session identity.Session, input sales.SalesReservationInput) error {
	lines := make([]inventory.SalesReservationLine, 0, len(input.Lines))
	for _, line := range input.Lines {
		lines = append(lines, inventory.SalesReservationLine{
			OrderLineID: line.OrderLineID, ProductID: line.ProductID, VariantID: line.VariantID,
			WarehouseID: line.WarehouseID, Quantity: line.Quantity,
		})
	}
	return a.service.ReserveSalesOrderTx(ctx, tx, session, inventory.SalesReservationInput{OrderID: input.OrderID, Lines: lines})
}

func (a inventoryStockPoster) ConsumeSalesOrderReservationsTx(ctx context.Context, tx pgx.Tx, session identity.Session, lines []sales.SalesReservationConsumption) error {
	items := make([]inventory.SalesReservationConsumption, 0, len(lines))
	for _, line := range lines {
		items = append(items, inventory.SalesReservationConsumption{OrderID: line.OrderID, OrderLineID: line.OrderLineID, ProductID: line.ProductID, VariantID: line.VariantID, WarehouseID: line.WarehouseID, Quantity: line.Quantity})
	}
	return a.service.ConsumeSalesOrderReservationsTx(ctx, tx, session, items)
}

func (a inventoryStockPoster) ReleaseSalesOrderReservationsTx(ctx context.Context, tx pgx.Tx, session identity.Session, orderID string) error {
	return a.service.ReleaseSalesOrderReservationsTx(ctx, tx, session, orderID)
}

func (a inventoryStockPoster) RestoreSalesOrderReservationsTx(ctx context.Context, tx pgx.Tx, session identity.Session, lines []sales.SalesReservationConsumption) error {
	items := make([]inventory.SalesReservationConsumption, 0, len(lines))
	for _, line := range lines {
		items = append(items, inventory.SalesReservationConsumption{OrderID: line.OrderID, OrderLineID: line.OrderLineID, ProductID: line.ProductID, VariantID: line.VariantID, WarehouseID: line.WarehouseID, Quantity: line.Quantity})
	}
	return a.service.RestoreSalesOrderReservationsTx(ctx, tx, session, items)
}

func (a inventoryStockPoster) PostPurchaseReceiptMovementsTx(ctx context.Context, tx pgx.Tx, session identity.Session, input inventory.PurchaseReceiptStockPostingInput) error {
	return a.service.PostPurchaseReceiptMovementsTx(ctx, tx, session, input)
}

func (a inventoryStockPoster) PostPurchaseInvoiceMovementsTx(ctx context.Context, tx pgx.Tx, session identity.Session, input inventory.PurchaseReceiptStockPostingInput) error {
	return a.service.PostPurchaseInvoiceMovementsTx(ctx, tx, session, input)
}

func (a inventoryStockPoster) PostPurchaseReturnMovementsTx(ctx context.Context, tx pgx.Tx, session identity.Session, input inventory.PurchaseReturnStockPostingInput) error {
	return a.service.PostPurchaseReturnMovementsTx(ctx, tx, session, input)
}

func (a inventoryStockPoster) ReversePurchaseMovementsTx(ctx context.Context, tx pgx.Tx, session identity.Session, input inventory.PurchaseStockReversalInput) error {
	return a.service.ReverseInvoiceMovementsTx(ctx, tx, session, inventory.InvoiceStockReversalInput{
		DocumentID: input.DocumentID, DocumentType: input.SourceType, WarehouseID: input.WarehouseID,
		ReversalKey: input.ReversalKey, Reason: input.Reason,
	})
}

type financePurchasePoster struct{ service *finance.Service }

func (a financePurchasePoster) ReadDocumentSettlement(ctx context.Context, companyID, documentID string) (finance.DocumentSettlement, error) {
	return a.service.ReadDocumentSettlement(ctx, companyID, documentID)
}

func (a financePurchasePoster) PostPurchaseInvoiceTx(ctx context.Context, tx pgx.Tx, session identity.Session, input purchasing.PurchaseInvoicePostingInput) (string, error) {
	return a.post(ctx, tx, session, input, "PURCHASE_INVOICE", "purchase-invoice:")
}

func (a financePurchasePoster) PostPurchaseReturnTx(ctx context.Context, tx pgx.Tx, session identity.Session, input purchasing.PurchaseInvoicePostingInput) (string, error) {
	return a.post(ctx, tx, session, input, "PURCHASE_RETURN_INVOICE", "purchase-return:")
}

func (a financePurchasePoster) ReversePurchaseInvoiceTx(ctx context.Context, tx pgx.Tx, session identity.Session, documentID, reversalKey, reason string) (string, error) {
	return a.service.ReverseInvoiceTx(ctx, tx, session, documentID, reversalKey, reason)
}

func (a financePurchasePoster) ReversePurchaseReturnTx(ctx context.Context, tx pgx.Tx, session identity.Session, documentID, reversalKey, reason string) (string, error) {
	return a.service.ReverseInvoiceTx(ctx, tx, session, documentID, reversalKey, reason)
}

func (a financePurchasePoster) post(ctx context.Context, tx pgx.Tx, session identity.Session, input purchasing.PurchaseInvoicePostingInput, documentType, keyPrefix string) (string, error) {
	posting, err := a.service.PostInvoiceTx(ctx, tx, session, finance.InvoicePostingInput{
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
