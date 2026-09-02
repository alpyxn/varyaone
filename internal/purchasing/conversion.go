package purchasing

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type purchaseConversionOrderLine struct {
	ID               string
	LineType         string
	ProductID        string
	VariantID        string
	ProductName      string
	WarehouseID      string
	UnitCode         string
	OrderedQuantity  string
	OrderedBase      string
	ConversionFactor string
	ReceivedQuantity string
	InvoicedQuantity string
	UnitPrice        string
	DiscountAmount   string
	NetAmount        string
	Currency         string
}

type purchaseConversionOrder struct {
	ID          string
	SupplierID  string
	BranchID    string
	WarehouseID string
	Currency    string
	Status      string
	Version     int64
	Lines       []purchaseConversionOrderLine
}

type purchaseConversionReceiptLine struct {
	ID                  string
	PurchaseOrderLineID string
	ProductID           string
	VariantID           string
	WarehouseID         string
	AcceptedQuantity    string
	ConversionFactor    string
	UnitCode            string
	UnitCost            string
	Currency            string
	AllocatedBase       string
	ReturnedBase        string
}

type purchaseConversionReceipt struct {
	ID          string
	ReceiptNo   string
	SupplierID  string
	BranchID    string
	WarehouseID string
	Currency    string
	Status      string
	Version     int64
	Lines       []purchaseConversionReceiptLine
}

// ConvertPurchaseDocument derives a new purchasing aggregate from the
// remaining source quantities. The target creation method owns the actual
// transaction, posting, idempotency and source-line locking; this method only
// reads immutable source snapshots and builds a normal target input.
func (s *Service) ConvertPurchaseDocument(ctx context.Context, session identity.Session, targetKind PurchaseKind, sourceID string, expectedVersion int64, meta identity.RequestMeta) (any, error) {
	if uuid.Validate(strings.TrimSpace(sourceID)) != nil {
		return nil, validation("kaynak belge kimliği geçersiz")
	}
	if expectedVersion < 1 {
		return nil, validation("kaynak belge sürümü gereklidir")
	}
	switch targetKind {
	case GoodsReceiptKind:
		order, err := s.loadPurchaseConversionOrder(ctx, session.CurrentCompanyID, sourceID)
		if err != nil {
			return nil, err
		}
		if err = s.ensurePurchaseOrderScope(ctx, s.pool, session, sourceID); err != nil {
			return nil, err
		}
		if order.Version != expectedVersion {
			return nil, identity.ErrConflict
		}
		if order.Status != "CONFIRMED" && order.Status != "PARTIALLY_FULFILLED" {
			return nil, ErrInvalidTransition
		}
		input := GoodsReceiptInput{
			PurchaseOrderID: sourceID,
			SupplierID:      order.SupplierID,
			BranchID:        order.BranchID,
			WarehouseID:     order.WarehouseID,
			ReceiptDate:     time.Now().UTC().Truncate(24 * time.Hour),
			Currency:        order.Currency,
			Lines:           make([]GoodsReceiptLine, 0, len(order.Lines)),
		}
		for _, sourceLine := range order.Lines {
			if normalizePurchaseLineType(sourceLine.LineType) != "PRODUCT" {
				continue
			}
			quantity, base, available, err := remainingPurchaseQuantity(sourceLine.OrderedBase, sourceLine.ReceivedQuantity, sourceLine.ConversionFactor)
			if err != nil {
				return nil, err
			}
			if !available {
				continue
			}
			orderLineID := sourceLine.ID
			// The order line's discount is part of what the item actually
			// costs; a provisional receipt cost copied from the gross list
			// price would overstate every layer FIFO opens from it.
			netUnitCost := sourceLine.UnitPrice
			if strings.TrimSpace(sourceLine.NetAmount) != "" && strings.TrimSpace(sourceLine.OrderedQuantity) != "" {
				netUnitCost = divide(sourceLine.NetAmount, sourceLine.OrderedQuantity)
			}
			input.Lines = append(input.Lines, GoodsReceiptLine{
				PurchaseOrderLineID: &orderLineID,
				ProductID:           sourceLine.ProductID,
				VariantID:           sourceLine.VariantID,
				AcceptedQuantity:    quantity,
				DamagedQuantity:     "0",
				RejectedQuantity:    "0",
				WarehouseID:         sourceLine.WarehouseID,
				UnitCode:            sourceLine.UnitCode,
				BaseQuantity:        base,
				ConversionFactor:    sourceLine.ConversionFactor,
				UnitCost:            netUnitCost,
				Currency:            order.Currency,
			})
		}
		if len(input.Lines) == 0 {
			return nil, ErrOverDelivery
		}
		item, err := s.CreateGoodsReceipt(ctx, session, input, meta)
		return item, err

	case PurchaseInvoiceKind:
		if order, err := s.loadPurchaseConversionOrder(ctx, session.CurrentCompanyID, sourceID); err == nil {
			if err = s.ensurePurchaseOrderScope(ctx, s.pool, session, sourceID); err != nil {
				return nil, err
			}
			if order.Version != expectedVersion {
				return nil, identity.ErrConflict
			}
			if order.Status != "CONFIRMED" && order.Status != "PARTIALLY_FULFILLED" && order.Status != "FULFILLED" {
				return nil, ErrInvalidTransition
			}
			input := PurchaseInvoiceInput{
				SupplierID:      order.SupplierID,
				BranchID:        order.BranchID,
				PurchaseOrderID: sourceID,
				InvoiceDate:     time.Now().UTC().Truncate(24 * time.Hour),
				Currency:        order.Currency,
				Lines:           make([]PurchaseInvoiceLine, 0, len(order.Lines)),
			}
			for _, sourceLine := range order.Lines {
				lineType := normalizePurchaseLineType(sourceLine.LineType)
				totalQuantity := sourceLine.ReceivedQuantity
				if lineType == "SERVICE" {
					totalQuantity = sourceLine.OrderedQuantity
				}
				quantity, base, available, remainingErr := remainingPurchaseBetweenQuantities(totalQuantity, sourceLine.InvoicedQuantity, sourceLine.ConversionFactor)
				if remainingErr != nil {
					return nil, remainingErr
				}
				if !available {
					continue
				}
				warehouseID := sourceLine.WarehouseID
				if lineType == "SERVICE" {
					warehouseID = ""
				}
				input.Lines = append(input.Lines, purchaseInvoiceConversionLine(lineType, sourceLine.ProductID, sourceLine.VariantID, warehouseID, sourceLine.UnitCode, base, sourceLine.ConversionFactor, quantity, sourceLine.UnitPrice, sourceLine.ID))
			}
			if len(input.Lines) == 0 {
				return nil, ErrOverDelivery
			}
			item, createErr := s.CreatePurchaseInvoice(ctx, session, input, meta)
			return item, createErr
		} else if !errors.Is(err, ErrNotFound) {
			return nil, err
		}
		receipt, err := s.loadPurchaseConversionReceipt(ctx, session.CurrentCompanyID, sourceID)
		if err != nil {
			return nil, err
		}
		if err = s.ensurePurchaseReceiptScope(ctx, s.pool, session, sourceID); err != nil {
			return nil, err
		}
		if receipt.Version != expectedVersion {
			return nil, identity.ErrConflict
		}
		if receipt.Status != "POSTED" {
			return nil, ErrInvalidTransition
		}
		input := PurchaseInvoiceInput{
			SupplierID:      receipt.SupplierID,
			BranchID:        receipt.BranchID,
			WarehouseID:     receipt.WarehouseID,
			GoodsReceiptID:  sourceID,
			GoodsReceiptIDs: []string{sourceID},
			InvoiceDate:     time.Now().UTC().Truncate(24 * time.Hour),
			Currency:        receipt.Currency,
			Lines:           make([]PurchaseInvoiceLine, 0, len(receipt.Lines)),
		}
		for _, sourceLine := range receipt.Lines {
			acceptedBase := multiply(sourceLine.AcceptedQuantity, sourceLine.ConversionFactor)
			quantity, base, available, remainingErr := remainingPurchaseBase(acceptedBase, sourceLine.AllocatedBase, sourceLine.ConversionFactor)
			if remainingErr != nil {
				return nil, remainingErr
			}
			if !available {
				continue
			}
			line := purchaseInvoiceConversionLine("PRODUCT", sourceLine.ProductID, sourceLine.VariantID, sourceLine.WarehouseID, sourceLine.UnitCode, base, sourceLine.ConversionFactor, quantity, sourceLine.UnitCost, "")
			line.GoodsReceiptLineID = sourceLine.ID
			line.PurchaseOrderLineID = sourceLine.PurchaseOrderLineID
			input.Lines = append(input.Lines, line)
		}
		if len(input.Lines) == 0 {
			return nil, ErrOverDelivery
		}
		item, createErr := s.CreatePurchaseInvoice(ctx, session, input, meta)
		return item, createErr

	case PurchaseReturnKind:
		receipt, err := s.loadPurchaseConversionReceipt(ctx, session.CurrentCompanyID, sourceID)
		if err != nil {
			return nil, err
		}
		if err = s.ensurePurchaseReceiptScope(ctx, s.pool, session, sourceID); err != nil {
			return nil, err
		}
		if receipt.Version != expectedVersion {
			return nil, identity.ErrConflict
		}
		if receipt.Status != "POSTED" {
			return nil, ErrInvalidTransition
		}
		input := PurchaseReturnInput{
			SupplierID:      receipt.SupplierID,
			BranchID:        receipt.BranchID,
			WarehouseID:     receipt.WarehouseID,
			SourceReceiptID: sourceID,
			ReturnDate:      time.Now().UTC().Truncate(24 * time.Hour),
			Currency:        receipt.Currency,
			Reason:          "Alış irsaliyesi " + receipt.ReceiptNo + " iadesi",
			Lines:           make([]PurchaseReturnLine, 0, len(receipt.Lines)),
		}
		for _, sourceLine := range receipt.Lines {
			acceptedBase := multiply(sourceLine.AcceptedQuantity, sourceLine.ConversionFactor)
			quantity, base, available, remainingErr := remainingPurchaseBase(acceptedBase, sourceLine.ReturnedBase, sourceLine.ConversionFactor)
			if remainingErr != nil {
				return nil, remainingErr
			}
			if !available {
				continue
			}
			input.Lines = append(input.Lines, PurchaseReturnLine{
				SourceReceiptLineID: sourceLine.ID,
				ProductID:           sourceLine.ProductID,
				VariantID:           sourceLine.VariantID,
				WarehouseID:         sourceLine.WarehouseID,
				Quantity:            quantity,
				BaseQuantity:        base,
				ConversionFactor:    sourceLine.ConversionFactor,
				UnitCode:            sourceLine.UnitCode,
				UnitCost:            sourceLine.UnitCost,
				Currency:            receipt.Currency,
			})
		}
		if len(input.Lines) == 0 {
			return nil, ErrOverDelivery
		}
		item, createErr := s.CreatePurchaseReturn(ctx, session, input, meta)
		return item, createErr
	default:
		return nil, validation("bu satın alma belgesi türü dönüştürülemez")
	}
}

func purchaseInvoiceConversionLine(lineType, productID, variantID, warehouseID, unitCode, base, factor, quantity, unitPrice, orderLineID string) PurchaseInvoiceLine {
	gross := multiply(quantity, unitPrice)
	return PurchaseInvoiceLine{
		PurchaseOrderLineID: orderLineID,
		LineType:            normalizePurchaseLineType(lineType),
		ProductID:           productID,
		VariantID:           variantID,
		WarehouseID:         warehouseID,
		UnitCode:            unitCode,
		BaseQuantity:        base,
		ConversionFactor:    factor,
		DescriptionSnapshot: "Alış belgesi satırı",
		Quantity:            quantity,
		UnitPrice:           unitPrice,
		GrossAmount:         gross,
		DiscountAmount:      "0",
		TaxBase:             gross,
		TaxAmount:           "0",
		WithholdingAmount:   "0",
		PayableAmount:       gross,
	}
}

func (s *Service) loadPurchaseConversionOrder(ctx context.Context, companyID, id string) (purchaseConversionOrder, error) {
	var item purchaseConversionOrder
	err := s.pool.QueryRow(ctx, `SELECT id,supplier_id,branch_id,warehouse_id,currency,status,version FROM purchase_orders WHERE company_id=$1 AND id=$2`, companyID, id).Scan(&item.ID, &item.SupplierID, &item.BranchID, &item.WarehouseID, &item.Currency, &item.Status, &item.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return purchaseConversionOrder{}, ErrNotFound
	}
	if err != nil {
		return purchaseConversionOrder{}, err
	}
	rows, err := s.pool.Query(ctx, `SELECT id,line_type,product_id,COALESCE(variant_id::text,''),product_name_snapshot,COALESCE(warehouse_id::text,''),unit_code,ordered_quantity::text,base_quantity::text,conversion_factor::text,received_quantity::text,invoiced_quantity::text,unit_price::text,discount_amount::text,net_amount::text,currency FROM purchase_order_lines WHERE company_id=$1 AND order_id=$2 ORDER BY line_no`, companyID, id)
	if err != nil {
		return purchaseConversionOrder{}, err
	}
	defer rows.Close()
	item.Lines = make([]purchaseConversionOrderLine, 0)
	for rows.Next() {
		var line purchaseConversionOrderLine
		if err = rows.Scan(&line.ID, &line.LineType, &line.ProductID, &line.VariantID, &line.ProductName, &line.WarehouseID, &line.UnitCode, &line.OrderedQuantity, &line.OrderedBase, &line.ConversionFactor, &line.ReceivedQuantity, &line.InvoicedQuantity, &line.UnitPrice, &line.DiscountAmount, &line.NetAmount, &line.Currency); err != nil {
			return purchaseConversionOrder{}, err
		}
		item.Lines = append(item.Lines, line)
	}
	return item, rows.Err()
}

func (s *Service) loadPurchaseConversionReceipt(ctx context.Context, companyID, id string) (purchaseConversionReceipt, error) {
	var item purchaseConversionReceipt
	err := s.pool.QueryRow(ctx, `SELECT id,receipt_no,supplier_id,branch_id,warehouse_id,currency,status,version FROM goods_receipts WHERE company_id=$1 AND id=$2`, companyID, id).Scan(&item.ID, &item.ReceiptNo, &item.SupplierID, &item.BranchID, &item.WarehouseID, &item.Currency, &item.Status, &item.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return purchaseConversionReceipt{}, ErrNotFound
	}
	if err != nil {
		return purchaseConversionReceipt{}, err
	}
	rows, err := s.pool.Query(ctx, `SELECT l.id,COALESCE(l.purchase_order_line_id::text,''),l.product_id,COALESCE(l.variant_id::text,''),l.warehouse_id,accepted_quantity::text,conversion_factor::text,unit_code,unit_cost::text,currency,COALESCE((SELECT SUM(a.base_quantity) FROM commercial_line_allocations a WHERE a.company_id=l.company_id AND a.source_line_id=l.id AND a.allocation_type='INVOICING'),'0')::text,COALESCE((SELECT SUM(a.base_quantity) FROM commercial_line_allocations a WHERE a.company_id=l.company_id AND a.source_line_id=l.id AND a.allocation_type='RETURN'),'0')::text FROM goods_receipt_lines l WHERE l.company_id=$1 AND l.receipt_id=$2 ORDER BY l.line_no`, companyID, id)
	if err != nil {
		return purchaseConversionReceipt{}, err
	}
	defer rows.Close()
	item.Lines = make([]purchaseConversionReceiptLine, 0)
	for rows.Next() {
		var line purchaseConversionReceiptLine
		if err = rows.Scan(&line.ID, &line.PurchaseOrderLineID, &line.ProductID, &line.VariantID, &line.WarehouseID, &line.AcceptedQuantity, &line.ConversionFactor, &line.UnitCode, &line.UnitCost, &line.Currency, &line.AllocatedBase, &line.ReturnedBase); err != nil {
			return purchaseConversionReceipt{}, err
		}
		item.Lines = append(item.Lines, line)
	}
	return item, rows.Err()
}

func remainingPurchaseQuantity(totalBase, consumedQuantity, factor string) (string, string, bool, error) {
	factorRat, err := purchaseConversionRat(factor, false)
	if err != nil {
		return "", "", false, validation("birim dönüşüm katsayısı geçersiz")
	}
	consumedRat, err := purchaseConversionRat(consumedQuantity, true)
	if err != nil {
		return "", "", false, validation("kaynak miktarı geçersiz")
	}
	return remainingPurchaseBase(totalBase, canonical(new(big.Rat).Mul(consumedRat, factorRat)), factor)
}

func remainingPurchaseBetweenQuantities(totalQuantity, consumedQuantity, factor string) (string, string, bool, error) {
	factorRat, err := purchaseConversionRat(factor, false)
	if err != nil {
		return "", "", false, validation("birim dönüşüm katsayısı geçersiz")
	}
	totalRat, err := purchaseConversionRat(totalQuantity, false)
	if err != nil {
		return "", "", false, validation("kaynak miktarı geçersiz")
	}
	return remainingPurchaseBase(canonical(new(big.Rat).Mul(totalRat, factorRat)), multiply(consumedQuantity, canonical(factorRat)), factor)
}

func remainingPurchaseBase(totalBase, consumedBase, factor string) (string, string, bool, error) {
	totalRat, err := purchaseConversionRat(totalBase, false)
	if err != nil {
		return "", "", false, validation("kaynak baz miktarı geçersiz")
	}
	consumedRat, err := purchaseConversionRat(consumedBase, true)
	if err != nil {
		return "", "", false, validation("tahsis baz miktarı geçersiz")
	}
	factorRat, err := purchaseConversionRat(factor, false)
	if err != nil {
		return "", "", false, validation("birim dönüşüm katsayısı geçersiz")
	}
	remaining := new(big.Rat).Sub(totalRat, consumedRat)
	if remaining.Sign() < 0 {
		return "", "", false, ErrOverDelivery
	}
	if remaining.Sign() == 0 {
		return "", "", false, nil
	}
	return canonical(new(big.Rat).Quo(remaining, factorRat)), canonical(remaining), true, nil
}

func purchaseConversionRat(value string, allowZero bool) (*big.Rat, error) {
	value = strings.TrimSpace(value)
	if value == "" || !validPurchaseDecimalLiteral(value) {
		return nil, errors.New("invalid decimal")
	}
	ratio, ok := new(big.Rat).SetString(value)
	if !ok || ratio.Sign() < 0 || (!allowZero && ratio.Sign() == 0) {
		return nil, errors.New("invalid decimal")
	}
	return ratio, nil
}
