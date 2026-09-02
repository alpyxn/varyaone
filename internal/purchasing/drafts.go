package purchasing

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// updatePurchaseDraftAnchorTx keeps the generic document identity in lockstep
// with the typed purchasing row. It is only called while the typed draft row
// is locked, so a draft edit cannot race a finalization.
func updatePurchaseDraftAnchorTx(ctx context.Context, tx pgx.Tx, session identity.Session, documentID, documentNo, branchID, warehouseID, partyID string, documentDate time.Time, dueDate *time.Time, currency, exchangeRate, notes, subtotal, discount, tax, grandTotal string) error {
	result, err := tx.Exec(ctx, `UPDATE documents SET document_no=$1,branch_id=$2,warehouse_id=NULLIF($3,'')::uuid,party_id=$4,document_date=$5,due_date=$6,currency_code=$7,exchange_rate=$8,notes=$9,subtotal=$10,discount_total=$11,tax_total=$12,grand_total=$13,updated_by=$14,updated_at=now(),version=version+1 WHERE company_id=$15 AND id=$16 AND status='DRAFT'`, documentNo, branchID, warehouseID, partyID, documentDate, dueDate, currency, exchangeRate, strings.TrimSpace(notes), subtotal, discount, tax, grandTotal, session.User.ID, session.CurrentCompanyID, documentID)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return identity.ErrConflict
	}
	return nil
}

func purchaseDraftAllocationCountTx(ctx context.Context, tx pgx.Tx, companyID, documentID string) (int, error) {
	var count int
	err := tx.QueryRow(ctx, `SELECT count(*) FROM commercial_line_allocations a JOIN commercial_line_registry r ON r.company_id=a.company_id AND (r.line_id=a.source_line_id OR r.line_id=a.target_line_id) WHERE r.company_id=$1 AND r.document_id=$2`, companyID, documentID).Scan(&count)
	return count, err
}

func clearPurchaseDraftRelationsTx(ctx context.Context, tx pgx.Tx, companyID, documentID, lineTable, parentColumn string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM commercial_document_sources WHERE company_id=$1 AND document_id=$2`, companyID, documentID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM `+lineTable+` WHERE company_id=$1 AND `+parentColumn+`=$2`, companyID, documentID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `DELETE FROM commercial_line_registry WHERE company_id=$1 AND document_id=$2`, companyID, documentID)
	return err
}

func (s *Service) UpdateGoodsReceipt(ctx context.Context, session identity.Session, id string, expectedVersion int64, input GoodsReceiptInput, meta identity.RequestMeta) (GoodsReceipt, error) {
	if err := s.authorizeAny(session, "purchase.receipt.draft", "purchase.receipt.post"); err != nil {
		return GoodsReceipt{}, err
	}
	if expectedVersion < 1 {
		return GoodsReceipt{}, validation("mal kabul sürümü gereklidir")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return GoodsReceipt{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	replayID, replay, err := reserveCommand(ctx, tx, session, meta, "purchasing.goods_receipt.update", map[string]any{"id": id, "version": expectedVersion, "input": input})
	if err != nil {
		return GoodsReceipt{}, err
	}
	if replay {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return s.GetGoodsReceipt(ctx, session, replayID)
	}
	var documentID, receiptNo, supplierID, branchID, warehouseID, currency, status, exchangeRate string
	var receiptDate time.Time
	if err = tx.QueryRow(ctx, `SELECT COALESCE(document_id::text,''),receipt_no,supplier_id,branch_id,warehouse_id,receipt_date,currency,status,COALESCE((SELECT exchange_rate::text FROM documents d WHERE d.company_id=goods_receipts.company_id AND d.id=goods_receipts.document_id),'1') FROM goods_receipts WHERE company_id=$1 AND id=$2 AND version=$3 FOR UPDATE`, session.CurrentCompanyID, id, expectedVersion).Scan(&documentID, &receiptNo, &supplierID, &branchID, &warehouseID, &receiptDate, &currency, &status, &exchangeRate); errors.Is(err, pgx.ErrNoRows) {
		return GoodsReceipt{}, identity.ErrConflict
	} else if err != nil {
		return GoodsReceipt{}, err
	}
	if status != "DRAFT" {
		return GoodsReceipt{}, validation("yalnız taslak mal kabul düzenlenebilir")
	}
	count, err := purchaseDraftAllocationCountTx(ctx, tx, session.CurrentCompanyID, id)
	if err != nil {
		return GoodsReceipt{}, err
	}
	if count > 0 {
		return GoodsReceipt{}, validation("kaynak belgeye bağlanmış mal kabul düzenlenemez")
	}
	if strings.TrimSpace(input.ReceiptNo) == "" {
		input.ReceiptNo = receiptNo
	}
	if input.ReceiptDate.IsZero() {
		input.ReceiptDate = receiptDate
	}
	if strings.TrimSpace(input.SupplierID) == "" {
		input.SupplierID = supplierID
	}
	if strings.TrimSpace(input.BranchID) == "" {
		input.BranchID = branchID
	}
	if strings.TrimSpace(input.WarehouseID) == "" {
		input.WarehouseID = warehouseID
	}
	if strings.TrimSpace(input.Currency) == "" {
		input.Currency = currency
	}
	input.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))
	input.PurchaseOrderID = strings.TrimSpace(input.PurchaseOrderID)
	if err = validateGoodsReceiptSourceShape(input.PurchaseOrderID, input.Lines); err != nil {
		return GoodsReceipt{}, err
	}
	if strings.TrimSpace(input.ExchangeRate) == "" {
		input.ExchangeRate = exchangeRate
	}
	if !validCurrency(input.Currency) {
		return GoodsReceipt{}, validation("mal kabul para birimi geçersiz")
	}
	if err = s.ensureExchangeRate(ctx, session, input.Currency, input.ReceiptDate, &input.ExchangeRate); err != nil {
		return GoodsReceipt{}, err
	}
	if err = s.ensureScope(ctx, session, input.BranchID, input.WarehouseID); err != nil {
		return GoodsReceipt{}, err
	}
	if err = s.ensureSupplier(ctx, session.CurrentCompanyID, input.SupplierID); err != nil {
		return GoodsReceipt{}, err
	}
	var orderSupplier, orderBranch, orderStatus, policy string
	if input.PurchaseOrderID != "" {
		if err = tx.QueryRow(ctx, `SELECT supplier_id,branch_id,status,over_delivery_policy FROM purchase_orders WHERE company_id=$1 AND id=$2 FOR UPDATE`, session.CurrentCompanyID, input.PurchaseOrderID).Scan(&orderSupplier, &orderBranch, &orderStatus, &policy); errors.Is(err, pgx.ErrNoRows) {
			return GoodsReceipt{}, ErrNotFound
		} else if err != nil {
			return GoodsReceipt{}, err
		}
		if err = s.ensurePurchaseOrderScope(ctx, tx, session, input.PurchaseOrderID); err != nil {
			return GoodsReceipt{}, err
		}
		if orderSupplier != input.SupplierID || orderBranch != input.BranchID || (orderStatus != "CONFIRMED" && orderStatus != "PARTIALLY_FULFILLED") {
			return GoodsReceipt{}, validation("mal kabul siparişi tedarikçi veya durum açısından geçersiz")
		}
	}
	warning := false
	for index := range input.Lines {
		line := &input.Lines[index]
		if err = validateReceiptLine(line); err != nil {
			return GoodsReceipt{}, err
		}
		if line.Currency != input.Currency {
			return GoodsReceipt{}, validation("mal kabul satırlarının para birimi belgeyle eşleşmelidir")
		}
		if err = ensurePurchaseProduct(ctx, tx, session.CurrentCompanyID, line.ProductID, line.VariantID, "PRODUCT"); err != nil {
			return GoodsReceipt{}, err
		}
		line.WarehouseID = strings.TrimSpace(line.WarehouseID)
		if line.WarehouseID == "" {
			line.WarehouseID = input.WarehouseID
		}
		if err = s.ensureScope(ctx, session, input.BranchID, line.WarehouseID); err != nil {
			return GoodsReceipt{}, err
		}
		accepted := zero(line.AcceptedQuantity)
		if input.PurchaseOrderID != "" {
			if line.PurchaseOrderLineID == nil || strings.TrimSpace(*line.PurchaseOrderLineID) == "" {
				return GoodsReceipt{}, validation("siparişli mal kabul satırları sipariş satırına bağlanmalıdır")
			}
			var ordered, received, orderWarehouse, orderLineType, orderProductID, orderVariantID string
			if err = tx.QueryRow(ctx, `SELECT product_id,COALESCE(variant_id::text,''),ordered_quantity::text,received_quantity::text,COALESCE(warehouse_id::text,''),line_type FROM purchase_order_lines WHERE company_id=$1 AND id=$2 AND order_id=$3 FOR UPDATE`, session.CurrentCompanyID, *line.PurchaseOrderLineID, input.PurchaseOrderID).Scan(&orderProductID, &orderVariantID, &ordered, &received, &orderWarehouse, &orderLineType); errors.Is(err, pgx.ErrNoRows) {
				return GoodsReceipt{}, ErrNotFound
			} else if err != nil {
				return GoodsReceipt{}, err
			}
			if normalizePurchaseLineType(orderLineType) != "PRODUCT" || orderProductID != line.ProductID || orderVariantID != strings.TrimSpace(line.VariantID) || orderWarehouse != line.WarehouseID {
				return GoodsReceipt{}, validation("mal kabul satırı sipariş satırıyla eşleşmiyor")
			}
			if compare(add(received, accepted), ordered) > 0 {
				if policy == "BLOCK" {
					return GoodsReceipt{}, ErrOverDelivery
				}
				warning = true
			}
		}
		line.BaseQuantity, line.ConversionFactor, err = resolvePurchaseConversionTx(ctx, tx, session.CurrentCompanyID, line.ProductID, line.UnitCode, accepted, line.BaseQuantity, line.ConversionFactor)
		if err != nil {
			return GoodsReceipt{}, err
		}
		line.ID, line.LineNo = uuid.NewString(), index+1
	}
	if documentID == "" {
		documentID = id
	}
	if err = updatePurchaseDraftAnchorTx(ctx, tx, session, documentID, input.ReceiptNo, input.BranchID, input.WarehouseID, input.SupplierID, input.ReceiptDate, nil, input.Currency, input.ExchangeRate, input.Notes, "0", "0", "0", "0"); err != nil {
		return GoodsReceipt{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE goods_receipts SET receipt_no=$1,purchase_order_id=NULLIF($2,'')::uuid,supplier_id=$3,branch_id=$4,warehouse_id=$5,receipt_date=$6,currency=$7,over_delivery_warning=$8,notes=$9,version=version+1 WHERE company_id=$10 AND id=$11 AND status='DRAFT' AND version=$12`, input.ReceiptNo, input.PurchaseOrderID, input.SupplierID, input.BranchID, input.WarehouseID, input.ReceiptDate, input.Currency, warning, strings.TrimSpace(input.Notes), session.CurrentCompanyID, id, expectedVersion); err != nil {
		return GoodsReceipt{}, err
	}
	if err = clearPurchaseDraftRelationsTx(ctx, tx, session.CurrentCompanyID, id, "goods_receipt_lines", "receipt_id"); err != nil {
		return GoodsReceipt{}, err
	}
	for _, line := range input.Lines {
		if _, err = tx.Exec(ctx, `INSERT INTO goods_receipt_lines(id,company_id,receipt_id,purchase_order_line_id,line_no,product_id,variant_id,warehouse_id,accepted_quantity,damaged_quantity,rejected_quantity,unit_code,base_quantity,conversion_factor,unit_cost,currency,lot_snapshot,serial_snapshot,tax_snapshot) VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,'')::uuid,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`, line.ID, session.CurrentCompanyID, id, line.PurchaseOrderLineID, line.LineNo, line.ProductID, line.VariantID, line.WarehouseID, line.AcceptedQuantity, line.DamagedQuantity, line.RejectedQuantity, line.UnitCode, line.BaseQuantity, line.ConversionFactor, line.UnitCost, input.Currency, jsonArray(line.LotSnapshot), jsonArray(line.SerialSnapshot), jsonObject(line.TaxSnapshot)); err != nil {
			return GoodsReceipt{}, err
		}
		if accepted := zero(line.AcceptedQuantity); compare(accepted, "0") > 0 {
			if err = registerPurchaseLineTx(ctx, tx, session.CurrentCompanyID, "GOODS_RECEIPT", id, line.ID, line.LineNo, "PRODUCT", accepted, line.BaseQuantity); err != nil {
				return GoodsReceipt{}, err
			}
		}
	}
	if input.PurchaseOrderID != "" {
		if _, err = tx.Exec(ctx, `INSERT INTO commercial_document_sources(company_id,document_id,source_document_id,relation_type) VALUES($1,$2,$3,'FULFILLMENT')`, session.CurrentCompanyID, id, input.PurchaseOrderID); err != nil {
			return GoodsReceipt{}, err
		}
	}
	if err = s.auditEventTx(ctx, tx, session, "PURCHASE_RECEIPT_UPDATED", "purchase.receipt.updated", id, meta, map[string]any{"version": expectedVersion + 1}); err != nil {
		return GoodsReceipt{}, err
	}
	if err = completeCommand(ctx, tx, session, meta, id); err != nil {
		return GoodsReceipt{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return GoodsReceipt{}, err
	}
	return s.GetGoodsReceipt(ctx, session, id)
}

func (s *Service) UpdatePurchaseInvoice(ctx context.Context, session identity.Session, id string, expectedVersion int64, input PurchaseInvoiceInput, meta identity.RequestMeta) (PurchaseInvoice, error) {
	if err := s.authorizeAny(session, "purchase.invoice.draft", "purchase.invoice.post"); err != nil {
		return PurchaseInvoice{}, err
	}
	if expectedVersion < 1 {
		return PurchaseInvoice{}, validation("alış faturası sürümü gereklidir")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return PurchaseInvoice{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	replayID, replay, err := reserveCommand(ctx, tx, session, meta, "purchasing.purchase_invoice.update", map[string]any{"id": id, "version": expectedVersion, "input": input})
	if err != nil {
		return PurchaseInvoice{}, err
	}
	if replay {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return s.GetPurchaseInvoice(ctx, session, replayID)
	}
	var documentID, invoiceNo, supplierID, branchID, warehouseID, currency, status, exchangeRate string
	var purchaseOrderID, goodsReceiptID string
	var invoiceDate time.Time
	var dueDate *time.Time
	if err = tx.QueryRow(ctx, `SELECT COALESCE(document_id::text,''),invoice_no,supplier_id,branch_id,COALESCE(warehouse_id::text,''),invoice_date,due_date,currency,status,COALESCE((SELECT exchange_rate::text FROM documents d WHERE d.company_id=purchase_invoices.company_id AND d.id=purchase_invoices.document_id),'1'),COALESCE(purchase_order_id::text,''),COALESCE(goods_receipt_id::text,'') FROM purchase_invoices WHERE company_id=$1 AND id=$2 AND version=$3 FOR UPDATE`, session.CurrentCompanyID, id, expectedVersion).Scan(&documentID, &invoiceNo, &supplierID, &branchID, &warehouseID, &invoiceDate, &dueDate, &currency, &status, &exchangeRate, &purchaseOrderID, &goodsReceiptID); errors.Is(err, pgx.ErrNoRows) {
		return PurchaseInvoice{}, identity.ErrConflict
	} else if err != nil {
		return PurchaseInvoice{}, err
	}
	if status != "DRAFT" {
		return PurchaseInvoice{}, validation("yalnız taslak alış faturası düzenlenebilir")
	}
	count, err := purchaseDraftAllocationCountTx(ctx, tx, session.CurrentCompanyID, id)
	if err != nil {
		return PurchaseInvoice{}, err
	}
	if count > 0 {
		return PurchaseInvoice{}, validation("kaynak belgeye bağlanmış alış faturası düzenlenemez")
	}
	// A standalone invoice (no order/receipt source) stays behind the same
	// permission gate on edit as on create; otherwise a user without it could
	// keep editing one another user drafted.
	if purchaseOrderID == "" && goodsReceiptID == "" && !session.HasPermission("purchase.invoice.standalone") {
		return PurchaseInvoice{}, identity.ErrForbidden
	}
	if strings.TrimSpace(input.InvoiceNo) == "" {
		input.InvoiceNo = invoiceNo
	}
	if input.InvoiceDate.IsZero() {
		input.InvoiceDate = invoiceDate
	}
	if strings.TrimSpace(input.SupplierID) == "" {
		input.SupplierID = supplierID
	}
	if strings.TrimSpace(input.BranchID) == "" {
		input.BranchID = branchID
	}
	if strings.TrimSpace(input.Currency) == "" {
		input.Currency = currency
	}
	if strings.TrimSpace(input.ExchangeRate) == "" {
		input.ExchangeRate = exchangeRate
	}
	input.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))
	input.GoodsReceiptIDs = purchaseReceiptIDs(input)
	if input.Standalone && (input.PurchaseOrderID != "" || len(input.GoodsReceiptIDs) > 0) {
		return PurchaseInvoice{}, validation("standalone alış faturası kaynak belge içeremez")
	}
	if !input.Standalone && input.PurchaseOrderID == "" && len(input.GoodsReceiptIDs) == 0 {
		return PurchaseInvoice{}, validation("alış faturası siparişe veya mal kabule bağlanmalıdır")
	}
	if !validCurrency(input.Currency) {
		return PurchaseInvoice{}, validation("alış faturası para birimi geçersiz")
	}
	if err = s.ensureExchangeRate(ctx, session, input.Currency, input.InvoiceDate, &input.ExchangeRate); err != nil {
		return PurchaseInvoice{}, err
	}
	if err = s.ensureBranch(ctx, session, input.BranchID); err != nil {
		return PurchaseInvoice{}, err
	}
	if err = s.ensureSupplier(ctx, session.CurrentCompanyID, input.SupplierID); err != nil {
		return PurchaseInvoice{}, err
	}
	if err = s.deriveSupplierDueDate(ctx, session.CurrentCompanyID, input.SupplierID, input.InvoiceDate, &input.DueDate); err != nil {
		return PurchaseInvoice{}, err
	}
	if input.PurchaseOrderID != "" {
		if err = s.ensurePurchaseOrderScope(ctx, tx, session, input.PurchaseOrderID); err != nil {
			return PurchaseInvoice{}, err
		}
	}
	for _, receiptID := range input.GoodsReceiptIDs {
		if err = s.ensurePurchaseReceiptScope(ctx, tx, session, receiptID); err != nil {
			return PurchaseInvoice{}, err
		}
	}
	if input.PurchaseOrderID != "" || len(input.GoodsReceiptIDs) > 0 {
		if err = s.lockInvoiceAllocationSourceTx(ctx, tx, session.CurrentCompanyID, &input); err != nil {
			return PurchaseInvoice{}, err
		}
	}
	var subtotal, discount, tax, payable string
	headerWarehouse := strings.TrimSpace(input.WarehouseID)
	for index := range input.Lines {
		line := &input.Lines[index]
		if err = validateInvoiceLine(line); err != nil {
			return PurchaseInvoice{}, err
		}
		if err = s.resolvePurchaseInvoiceLineDefaults(ctx, tx, session, input, line, index+1); err != nil {
			return PurchaseInvoice{}, err
		}
		if err = ensurePurchaseProduct(ctx, tx, session.CurrentCompanyID, line.ProductID, line.VariantID, line.LineType); err != nil {
			return PurchaseInvoice{}, err
		}
		line.BaseQuantity, line.ConversionFactor, err = resolvePurchaseConversionTx(ctx, tx, session.CurrentCompanyID, line.ProductID, line.UnitCode, line.Quantity, line.BaseQuantity, line.ConversionFactor)
		if err != nil {
			return PurchaseInvoice{}, err
		}
		line.WarehouseID = strings.TrimSpace(line.WarehouseID)
		if line.LineType == "SERVICE" {
			if line.WarehouseID != "" {
				return PurchaseInvoice{}, validation("alış faturası hizmet satırında depo bulunamaz")
			}
		} else {
			if line.WarehouseID == "" {
				line.WarehouseID = headerWarehouse
			}
			if line.WarehouseID == "" {
				return PurchaseInvoice{}, validation("alış faturası ürün satırı için depo gereklidir")
			}
			if err = s.ensureScope(ctx, session, input.BranchID, line.WarehouseID); err != nil {
				return PurchaseInvoice{}, err
			}
			if headerWarehouse == "" {
				headerWarehouse = line.WarehouseID
			} else if headerWarehouse != line.WarehouseID {
				headerWarehouse = ""
			}
		}
		line.ID, line.LineNo = uuid.NewString(), index+1
		subtotal, discount, tax, payable = add(subtotal, line.GrossAmount), add(discount, line.DiscountAmount), add(tax, line.TaxAmount), add(payable, line.PayableAmount)
	}
	if documentID == "" {
		documentID = id
	}
	if err = updatePurchaseDraftAnchorTx(ctx, tx, session, documentID, input.InvoiceNo, input.BranchID, headerWarehouse, input.SupplierID, input.InvoiceDate, input.DueDate, input.Currency, input.ExchangeRate, "Alış faturası", subtotal, discount, tax, payable); err != nil {
		return PurchaseInvoice{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE purchase_invoices SET invoice_no=$1,supplier_id=$2,branch_id=$3,warehouse_id=NULLIF($4,'')::uuid,purchase_order_id=NULLIF($5,'')::uuid,goods_receipt_id=NULLIF($6,'')::uuid,invoice_date=$7,due_date=$8,currency=$9,subtotal=$10,discount_total=$11,tax_total=$12,payable_total=$13,version=version+1 WHERE company_id=$14 AND id=$15 AND status='DRAFT' AND version=$16`, input.InvoiceNo, input.SupplierID, input.BranchID, headerWarehouse, input.PurchaseOrderID, firstPurchaseReceiptID(input.GoodsReceiptIDs), input.InvoiceDate, input.DueDate, input.Currency, subtotal, discount, tax, payable, session.CurrentCompanyID, id, expectedVersion); err != nil {
		return PurchaseInvoice{}, err
	}
	if err = clearPurchaseDraftRelationsTx(ctx, tx, session.CurrentCompanyID, id, "purchase_invoice_lines", "invoice_id"); err != nil {
		return PurchaseInvoice{}, err
	}
	for _, line := range input.Lines {
		if _, err = tx.Exec(ctx, `INSERT INTO purchase_invoice_lines(id,company_id,invoice_id,purchase_order_line_id,goods_receipt_line_id,line_no,line_type,product_id,variant_id,warehouse_id,unit_code,base_quantity,conversion_factor,description_snapshot,quantity,unit_price,gross_amount,discount_amount,tax_base,tax_amount,withholding_amount,payable_amount,tax_components_snapshot) VALUES($1,$2,$3,NULLIF($4,'')::uuid,NULLIF($5,'')::uuid,$6,$7,$8,NULLIF($9,'')::uuid,NULLIF($10,'')::uuid,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23)`, line.ID, session.CurrentCompanyID, id, line.PurchaseOrderLineID, line.GoodsReceiptLineID, line.LineNo, line.LineType, line.ProductID, line.VariantID, line.WarehouseID, line.UnitCode, line.BaseQuantity, line.ConversionFactor, line.DescriptionSnapshot, line.Quantity, line.UnitPrice, line.GrossAmount, line.DiscountAmount, line.TaxBase, line.TaxAmount, line.WithholdingAmount, line.PayableAmount, jsonArray(line.TaxComponentsSnapshot)); err != nil {
			return PurchaseInvoice{}, err
		}
		if err = registerPurchaseLineTx(ctx, tx, session.CurrentCompanyID, "PURCHASE_INVOICE", id, line.ID, line.LineNo, line.LineType, line.Quantity, line.BaseQuantity); err != nil {
			return PurchaseInvoice{}, err
		}
	}
	for _, sourceID := range append([]string{input.PurchaseOrderID}, input.GoodsReceiptIDs...) {
		if strings.TrimSpace(sourceID) == "" {
			continue
		}
		if _, err = tx.Exec(ctx, `INSERT INTO commercial_document_sources(company_id,document_id,source_document_id,relation_type) VALUES($1,$2,$3,'INVOICING')`, session.CurrentCompanyID, id, sourceID); err != nil {
			return PurchaseInvoice{}, err
		}
	}
	if err = s.auditEventTx(ctx, tx, session, "PURCHASE_INVOICE_UPDATED", "purchase.invoice.updated", id, meta, map[string]any{"version": expectedVersion + 1}); err != nil {
		return PurchaseInvoice{}, err
	}
	if err = completeCommand(ctx, tx, session, meta, id); err != nil {
		return PurchaseInvoice{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return PurchaseInvoice{}, err
	}
	return s.GetPurchaseInvoice(ctx, session, id)
}

func firstPurchaseReceiptID(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	return ids[0]
}

func (s *Service) UpdatePurchaseReturn(ctx context.Context, session identity.Session, id string, expectedVersion int64, input PurchaseReturnInput, meta identity.RequestMeta) (PurchaseReturn, error) {
	if err := s.authorizeAny(session, "purchase.return.draft", "purchase.return.post"); err != nil {
		return PurchaseReturn{}, err
	}
	if expectedVersion < 1 {
		return PurchaseReturn{}, validation("satın alma iadesi sürümü gereklidir")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return PurchaseReturn{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	replayID, replay, err := reserveCommand(ctx, tx, session, meta, "purchasing.purchase_return.update", map[string]any{"id": id, "version": expectedVersion, "input": input})
	if err != nil {
		return PurchaseReturn{}, err
	}
	if replay {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return s.GetPurchaseReturn(ctx, session, replayID)
	}
	var documentID, returnNo, supplierID, branchID, warehouseID, currency, reason, status, exchangeRate string
	var existingSourceReceiptID *string
	var returnDate time.Time
	if err = tx.QueryRow(ctx, `SELECT COALESCE(document_id::text,''),return_no,supplier_id,branch_id,warehouse_id,source_receipt_id,return_date,currency,reason,status,COALESCE((SELECT exchange_rate::text FROM documents d WHERE d.company_id=purchase_returns.company_id AND d.id=purchase_returns.document_id),'1') FROM purchase_returns WHERE company_id=$1 AND id=$2 AND version=$3 FOR UPDATE`, session.CurrentCompanyID, id, expectedVersion).Scan(&documentID, &returnNo, &supplierID, &branchID, &warehouseID, &existingSourceReceiptID, &returnDate, &currency, &reason, &status, &exchangeRate); errors.Is(err, pgx.ErrNoRows) {
		return PurchaseReturn{}, identity.ErrConflict
	} else if err != nil {
		return PurchaseReturn{}, err
	}
	if status != "DRAFT" {
		return PurchaseReturn{}, validation("yalnız taslak satın alma iadesi düzenlenebilir")
	}
	count, err := purchaseDraftAllocationCountTx(ctx, tx, session.CurrentCompanyID, id)
	if err != nil {
		return PurchaseReturn{}, err
	}
	if count > 0 {
		return PurchaseReturn{}, validation("kaynak belgeye bağlanmış satın alma iadesi düzenlenemez")
	}
	if strings.TrimSpace(input.ReturnNo) == "" {
		input.ReturnNo = returnNo
	}
	if input.ReturnDate.IsZero() {
		input.ReturnDate = returnDate
	}
	if strings.TrimSpace(input.SupplierID) == "" {
		input.SupplierID = supplierID
	}
	if strings.TrimSpace(input.BranchID) == "" {
		input.BranchID = branchID
	}
	if strings.TrimSpace(input.WarehouseID) == "" {
		input.WarehouseID = warehouseID
	}
	if strings.TrimSpace(input.Currency) == "" {
		input.Currency = currency
	}
	if strings.TrimSpace(input.SourceReceiptID) == "" {
		input.SourceReceiptID = dereferenceString(existingSourceReceiptID)
	}
	input.SourceReceiptID = strings.TrimSpace(input.SourceReceiptID)
	if strings.TrimSpace(input.Reason) == "" {
		input.Reason = reason
	}
	input.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))
	if input.Reason == "" || !validCurrency(input.Currency) {
		return PurchaseReturn{}, validation("satın alma iadesi gerekçe ve para birimi gerektirir")
	}
	if len(input.Lines) > 0 && input.SourceReceiptID == "" {
		return PurchaseReturn{}, validation("satın alma iadesi kaynak mal kabul belgesine bağlanmalıdır")
	}
	if err = s.ensureExchangeRate(ctx, session, input.Currency, input.ReturnDate, &input.ExchangeRate); err != nil {
		return PurchaseReturn{}, err
	}
	if err = s.ensureScope(ctx, session, input.BranchID, input.WarehouseID); err != nil {
		return PurchaseReturn{}, err
	}
	if err = s.ensureSupplier(ctx, session.CurrentCompanyID, input.SupplierID); err != nil {
		return PurchaseReturn{}, err
	}
	if err = validatePurchaseReturnSourceShape(input.SourceReceiptID, input.Lines); err != nil {
		return PurchaseReturn{}, err
	}
	if input.SourceReceiptID != "" {
		if err = s.lockPurchaseReturnSourceTx(ctx, tx, session.CurrentCompanyID, &input); err != nil {
			return PurchaseReturn{}, err
		}
	}
	total := "0"
	for index := range input.Lines {
		line := &input.Lines[index]
		if err = validateReturnLine(line, input.Currency); err != nil {
			return PurchaseReturn{}, err
		}
		if err = ensurePurchaseProduct(ctx, tx, session.CurrentCompanyID, line.ProductID, line.VariantID, "PRODUCT"); err != nil {
			return PurchaseReturn{}, err
		}
		line.WarehouseID = strings.TrimSpace(line.WarehouseID)
		if line.WarehouseID == "" {
			line.WarehouseID = input.WarehouseID
		}
		if err = s.ensureScope(ctx, session, input.BranchID, line.WarehouseID); err != nil {
			return PurchaseReturn{}, err
		}
		line.BaseQuantity, line.ConversionFactor, err = resolvePurchaseConversionTx(ctx, tx, session.CurrentCompanyID, line.ProductID, line.UnitCode, line.Quantity, line.BaseQuantity, line.ConversionFactor)
		if err != nil {
			return PurchaseReturn{}, err
		}
		line.ID, line.LineNo = uuid.NewString(), index+1
		total = add(total, multiply(line.Quantity, line.UnitCost))
	}
	if documentID == "" {
		documentID = id
	}
	if err = updatePurchaseDraftAnchorTx(ctx, tx, session, documentID, input.ReturnNo, input.BranchID, input.WarehouseID, input.SupplierID, input.ReturnDate, nil, input.Currency, input.ExchangeRate, input.Reason, total, "0", "0", total); err != nil {
		return PurchaseReturn{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE purchase_returns SET return_no=$1,supplier_id=$2,branch_id=$3,warehouse_id=$4,source_receipt_id=NULLIF($5,'')::uuid,return_date=$6,currency=$7,total=$8,reason=$9,version=version+1 WHERE company_id=$10 AND id=$11 AND status='DRAFT' AND version=$12`, input.ReturnNo, input.SupplierID, input.BranchID, input.WarehouseID, input.SourceReceiptID, input.ReturnDate, input.Currency, total, strings.TrimSpace(input.Reason), session.CurrentCompanyID, id, expectedVersion); err != nil {
		return PurchaseReturn{}, err
	}
	if err = clearPurchaseDraftRelationsTx(ctx, tx, session.CurrentCompanyID, id, "purchase_return_lines", "return_id"); err != nil {
		return PurchaseReturn{}, err
	}
	for _, line := range input.Lines {
		if _, err = tx.Exec(ctx, `INSERT INTO purchase_return_lines(id,company_id,return_id,source_receipt_line_id,line_no,product_id,variant_id,warehouse_id,quantity,base_quantity,conversion_factor,unit_code,unit_cost,currency,reason) VALUES($1,$2,$3,NULLIF($4,'')::uuid,$5,$6,NULLIF($7,'')::uuid,$8,$9,$10,$11,$12,$13,$14,$15)`, line.ID, session.CurrentCompanyID, id, line.SourceReceiptLineID, line.LineNo, line.ProductID, line.VariantID, line.WarehouseID, line.Quantity, line.BaseQuantity, line.ConversionFactor, line.UnitCode, line.UnitCost, input.Currency, line.Reason); err != nil {
			return PurchaseReturn{}, err
		}
		if err = registerPurchaseLineTx(ctx, tx, session.CurrentCompanyID, "PURCHASE_RETURN", id, line.ID, line.LineNo, "PRODUCT", line.Quantity, line.BaseQuantity); err != nil {
			return PurchaseReturn{}, err
		}
	}
	if input.SourceReceiptID != "" {
		if _, err = tx.Exec(ctx, `INSERT INTO commercial_document_sources(company_id,document_id,source_document_id,relation_type) VALUES($1,$2,$3,'RETURN')`, session.CurrentCompanyID, id, input.SourceReceiptID); err != nil {
			return PurchaseReturn{}, err
		}
	}
	if err = s.auditEventTx(ctx, tx, session, "PURCHASE_RETURN_UPDATED", "purchase.return.updated", id, meta, map[string]any{"version": expectedVersion + 1}); err != nil {
		return PurchaseReturn{}, err
	}
	if err = completeCommand(ctx, tx, session, meta, id); err != nil {
		return PurchaseReturn{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return PurchaseReturn{}, err
	}
	return s.GetPurchaseReturn(ctx, session, id)
}

func (s *Service) DeletePurchaseDraft(ctx context.Context, session identity.Session, kind PurchaseKind, id string, expectedVersion int64, meta identity.RequestMeta) error {
	permission, ok := purchaseReadPermission(kind)
	if !ok || kind == PurchaseOrderKind {
		return ErrInvalidTransition
	}
	if err := s.authorize(session, permission); err != nil {
		return err
	}
	if expectedVersion < 1 {
		return validation("taslak sürümü gereklidir")
	}
	table, lineTable, lineParent, aggregate := map[PurchaseKind]string{
		GoodsReceiptKind:    "goods_receipts",
		PurchaseInvoiceKind: "purchase_invoices",
		PurchaseReturnKind:  "purchase_returns",
	}[kind], map[PurchaseKind]string{
		GoodsReceiptKind:    "goods_receipt_lines",
		PurchaseInvoiceKind: "purchase_invoice_lines",
		PurchaseReturnKind:  "purchase_return_lines",
	}[kind], map[PurchaseKind]string{
		GoodsReceiptKind:    "receipt_id",
		PurchaseInvoiceKind: "invoice_id",
		PurchaseReturnKind:  "return_id",
	}[kind], map[PurchaseKind]string{
		GoodsReceiptKind:    "GOODS_RECEIPT",
		PurchaseInvoiceKind: "PURCHASE_INVOICE",
		PurchaseReturnKind:  "PURCHASE_RETURN",
	}[kind]
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	replayID, replay, err := reserveCommand(ctx, tx, session, meta, "purchasing."+strings.ToLower(string(kind))+".delete", map[string]any{"id": id, "version": expectedVersion})
	if err != nil {
		return err
	}
	if replay {
		_ = replayID
		return tx.Commit(ctx)
	}
	var documentID, branchID, warehouseID, status string
	if err = tx.QueryRow(ctx, `SELECT COALESCE(document_id::text,''),branch_id,COALESCE(warehouse_id::text,''),status FROM `+table+` WHERE company_id=$1 AND id=$2 AND version=$3 FOR UPDATE`, session.CurrentCompanyID, id, expectedVersion).Scan(&documentID, &branchID, &warehouseID, &status); errors.Is(err, pgx.ErrNoRows) {
		return identity.ErrConflict
	} else if err != nil {
		return err
	}
	if status != "DRAFT" {
		return validation("yalnız taslak belge silinebilir")
	}
	if err = s.ensureBranch(ctx, session, branchID); err != nil {
		return err
	}
	if warehouseID != "" {
		if err = s.ensureScope(ctx, session, branchID, warehouseID); err != nil {
			return err
		}
	}
	count, err := purchaseDraftAllocationCountTx(ctx, tx, session.CurrentCompanyID, id)
	if err != nil {
		return err
	}
	if count > 0 {
		return validation("kaynak belgeye bağlanmış taslak silinemez")
	}
	var referenced int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM commercial_document_sources WHERE company_id=$1 AND source_document_id=$2`, session.CurrentCompanyID, id).Scan(&referenced); err != nil {
		return err
	}
	if referenced > 0 {
		return validation("başka bir belgeye kaynak olan taslak silinemez")
	}
	if _, err = tx.Exec(ctx, `DELETE FROM `+lineTable+` WHERE company_id=$1 AND `+lineParent+`=$2`, session.CurrentCompanyID, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM commercial_line_registry WHERE company_id=$1 AND document_id=$2 AND aggregate_type=$3`, session.CurrentCompanyID, id, aggregate); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM commercial_document_sources WHERE company_id=$1 AND document_id=$2`, session.CurrentCompanyID, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM `+table+` WHERE company_id=$1 AND id=$2 AND status='DRAFT' AND version=$3`, session.CurrentCompanyID, id, expectedVersion); err != nil {
		return err
	}
	if documentID != "" {
		if _, err = tx.Exec(ctx, `DELETE FROM documents WHERE company_id=$1 AND id=$2 AND status='DRAFT'`, session.CurrentCompanyID, documentID); err != nil {
			return err
		}
	}
	if err = s.auditEventTx(ctx, tx, session, strings.ToUpper(string(kind))+"_DELETED", "purchase."+strings.ToLower(string(kind))+".deleted", id, meta, nil); err != nil {
		return err
	}
	if err = completeCommand(ctx, tx, session, meta, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
