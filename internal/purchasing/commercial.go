package purchasing

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// PurchaseKind is the route/service selector for the first-class purchasing
// aggregates. GoodsReceipt is deliberately exposed as PURCHASE_DISPATCH at
// the typed API boundary so the UI can use the familiar "alış irsaliyesi"
// terminology while retaining the existing goods_receipts table.
type PurchaseKind string

const (
	PurchaseOrderKind   PurchaseKind = "PURCHASE_ORDER"
	GoodsReceiptKind    PurchaseKind = "GOODS_RECEIPT"
	PurchaseInvoiceKind PurchaseKind = "PURCHASE_INVOICE"
	PurchaseReturnKind  PurchaseKind = "PURCHASE_RETURN"
)

type PurchaseListOptions struct {
	Status            string
	LifecycleStatus   string
	FulfillmentStatus string
	InvoicingStatus   string
	PaymentStatus     string
	SupplierID        string
	BranchID          string
	CurrencyCode      string
	ForReference      bool
	ReferenceTarget   string
	From              *time.Time
	To                *time.Time
	Cursor            string
	Search            string
	Sort              string
	Limit             int
}

// purchaseSortExpr maps a grid header field to a safe ORDER BY expression for
// the given list spec, or returns ("","") when the field is not sortable.
func purchaseSortExpr(sort string, spec purchaseListTableSpec) (string, string) {
	sort = strings.TrimSpace(sort)
	if sort == "" {
		return "", ""
	}
	field, direction, _ := strings.Cut(sort, ":")
	columns := map[string]string{
		"document_no":   "t." + spec.documentNo,
		"document_date": "t." + spec.documentDate,
		"supplier_name": "p.display_name",
		"party_name":    "p.display_name",
		"grand_total":   spec.grandTotal,
		"payable_total": spec.payableTotal,
		"tax_total":     spec.taxTotal,
		"currency_code": "t.currency",
		"created_at":    "t.created_at",
	}
	expr, ok := columns[strings.TrimSpace(field)]
	if !ok {
		return "", ""
	}
	if strings.EqualFold(strings.TrimSpace(direction), "asc") {
		return expr, "ASC"
	}
	return expr, "DESC"
}

type PurchaseListItem struct {
	ID                string       `json:"id"`
	CompanyID         string       `json:"company_id"`
	Kind              PurchaseKind `json:"kind"`
	DocumentNo        string       `json:"document_no"`
	SupplierID        string       `json:"supplier_id"`
	SupplierCode      string       `json:"supplier_code,omitempty"`
	SupplierName      string       `json:"supplier_name,omitempty"`
	BranchID          string       `json:"branch_id"`
	WarehouseID       string       `json:"warehouse_id"`
	DocumentDate      time.Time    `json:"document_date"`
	Currency          string       `json:"currency"`
	Status            string       `json:"status"`
	LifecycleStatus   string       `json:"lifecycle_status"`
	FulfillmentStatus string       `json:"fulfillment_status,omitempty"`
	FulfillmentAt     *time.Time   `json:"fulfillment_at,omitempty"`
	InvoicingStatus   string       `json:"invoicing_status,omitempty"`
	PaymentStatus     string       `json:"payment_status,omitempty"`
	Total             string       `json:"total"`
	TaxTotal          string       `json:"tax_total"`
	GrandTotal        string       `json:"grand_total"`
	PayableTotal      string       `json:"payable_total"`
	Version           int64        `json:"version"`
	CreatedAt         time.Time    `json:"created_at"`
}

type PurchaseListResult struct {
	Items      []PurchaseListItem `json:"items"`
	NextCursor string             `json:"next_cursor,omitempty"`
}

type purchaseSourceQuery interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (s *Service) loadPurchaseSourceDocuments(ctx context.Context, q purchaseSourceQuery, session identity.Session, documentID string) ([]SourceDocumentReference, error) {
	rows, err := q.Query(ctx, `
		SELECT s.source_document_id,
		       d.document_no,
		       d.document_type_code,
		       CASE d.document_type_code
		           WHEN 'PURCHASE_ORDER' THEN 'ORDER'
		           WHEN 'PURCHASE_DELIVERY' THEN 'RECEIPT'
		           WHEN 'PURCHASE_INVOICE' THEN 'INVOICE'
		           WHEN 'PURCHASE_RETURN_INVOICE' THEN 'RETURN'
		           ELSE dt.kind
		       END AS source_kind,
		       s.relation_type,
		       d.branch_id,
		       d.warehouse_id,
		       COALESCE(po.status, gr.status, pi.status, pr.status, d.status) AS source_status
		  FROM commercial_document_sources s
		  JOIN documents d
		    ON d.company_id=s.company_id AND d.id=s.source_document_id
		  JOIN document_types dt ON dt.code=d.document_type_code
		  LEFT JOIN purchase_orders po
		    ON po.company_id=d.company_id AND po.document_id=d.id AND d.document_type_code='PURCHASE_ORDER'
		  LEFT JOIN goods_receipts gr
		    ON gr.company_id=d.company_id AND gr.document_id=d.id AND d.document_type_code='PURCHASE_DELIVERY'
		  LEFT JOIN purchase_invoices pi
		    ON pi.company_id=d.company_id AND pi.document_id=d.id AND d.document_type_code='PURCHASE_INVOICE'
		  LEFT JOIN purchase_returns pr
		    ON pr.company_id=d.company_id AND pr.document_id=d.id AND d.document_type_code='PURCHASE_RETURN_INVOICE'
		 WHERE s.company_id=$1 AND s.document_id=$2
	 ORDER BY s.created_at,s.source_document_id,s.relation_type`, session.CurrentCompanyID, documentID)
	if err != nil {
		return nil, err
	}
	// Buffer every row before running any dependent query. The request is pinned
	// to a single database connection, so issuing a sub-query while these rows are
	// still open fails with "conn busy".
	type sourceRow struct {
		source      SourceDocumentReference
		sourceKind  string
		branchID    string
		sourceState string
		warehouseID *string
	}
	var buffered []sourceRow
	for rows.Next() {
		var rowData sourceRow
		if err := rows.Scan(&rowData.source.ID, &rowData.source.DocumentNo, &rowData.source.DocumentTypeCode, &rowData.sourceKind, &rowData.source.RelationType, &rowData.branchID, &rowData.warehouseID, &rowData.sourceState); err != nil {
			rows.Close()
			return nil, err
		}
		buffered = append(buffered, rowData)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	result := []SourceDocumentReference{}
	for _, rowData := range buffered {
		source := rowData.source
		sourceKind := rowData.sourceKind
		branchID := rowData.branchID
		sourceStatus := rowData.sourceState
		warehouseID := rowData.warehouseID
		if err := s.ensurePurchaseSourceScope(ctx, q, session, source.DocumentTypeCode, source.ID, branchID, warehouseID); err != nil {
			if errors.Is(err, identity.ErrForbidden) {
				continue
			}
			return nil, err
		}
		source.Kind = sourceKind
		source.Direction = "SOURCE"
		source.Status = strings.ToUpper(strings.TrimSpace(sourceStatus))
		if sourceKindValue, ok := purchaseKindForDocumentType(source.DocumentTypeCode); ok {
			source.Kind = purchaseSourceKind(sourceKindValue)
			source.LifecycleStatus = purchaseLifecycleStatus(sourceKindValue, source.Status)
		} else {
			source.LifecycleStatus = source.Status
		}
		result = append(result, source)
	}
	return result, nil
}

func purchaseKindForDocumentType(documentTypeCode string) (PurchaseKind, bool) {
	switch strings.ToUpper(strings.TrimSpace(documentTypeCode)) {
	case "PURCHASE_ORDER":
		return PurchaseOrderKind, true
	case "PURCHASE_DELIVERY":
		return GoodsReceiptKind, true
	case "PURCHASE_INVOICE":
		return PurchaseInvoiceKind, true
	case "PURCHASE_RETURN_INVOICE":
		return PurchaseReturnKind, true
	default:
		return "", false
	}
}

func purchaseSourceKind(kind PurchaseKind) string {
	switch kind {
	case PurchaseOrderKind:
		return "ORDER"
	case GoodsReceiptKind:
		return "RECEIPT"
	case PurchaseInvoiceKind:
		return "INVOICE"
	case PurchaseReturnKind:
		return "RETURN"
	default:
		return ""
	}
}

func (s *Service) ensurePurchaseSourceScope(ctx context.Context, q purchaseSourceQuery, session identity.Session, documentTypeCode, sourceID, branchID string, warehouseID *string) error {
	if warehouseID == nil || strings.TrimSpace(*warehouseID) == "" {
		if err := ensurePurchaseReadBranch(ctx, q, session, branchID); err != nil {
			return err
		}
	} else if err := ensurePurchaseReadScope(ctx, q, session, branchID, *warehouseID); err != nil {
		return err
	}

	lineTable, parentColumn := "", ""
	switch strings.ToUpper(strings.TrimSpace(documentTypeCode)) {
	case "PURCHASE_ORDER":
		lineTable, parentColumn = "purchase_order_lines", "order_id"
	case "PURCHASE_DELIVERY":
		lineTable, parentColumn = "goods_receipt_lines", "receipt_id"
	case "PURCHASE_INVOICE":
		lineTable, parentColumn = "purchase_invoice_lines", "invoice_id"
	case "PURCHASE_RETURN_INVOICE":
		lineTable, parentColumn = "purchase_return_lines", "return_id"
	default:
		return nil
	}

	query := fmt.Sprintf("SELECT %s,COALESCE(warehouse_id::text,'') FROM %s WHERE company_id=$1 AND %s=$2 ORDER BY line_no", purchaseSourceLineTypeExpression(documentTypeCode), lineTable, parentColumn)
	rows, err := q.Query(ctx, query, session.CurrentCompanyID, sourceID)
	if err != nil {
		return err
	}
	// Buffer the line warehouses before any per-line scope query: the pinned
	// request connection cannot serve a sub-query while these rows are open.
	var lineWarehouses []string
	for rows.Next() {
		var lineType, lineWarehouseID string
		if err = rows.Scan(&lineType, &lineWarehouseID); err != nil {
			rows.Close()
			return err
		}
		if strings.EqualFold(strings.TrimSpace(lineType), "SERVICE") && lineWarehouseID == "" {
			continue
		}
		if lineWarehouseID == "" {
			rows.Close()
			return identity.ErrForbidden
		}
		lineWarehouses = append(lineWarehouses, lineWarehouseID)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, lineWarehouseID := range lineWarehouses {
		if err = ensurePurchaseReadScope(ctx, q, session, branchID, lineWarehouseID); err != nil {
			return err
		}
	}
	return nil
}

func purchaseSourceLineTypeExpression(documentTypeCode string) string {
	if strings.EqualFold(strings.TrimSpace(documentTypeCode), "PURCHASE_RETURN_INVOICE") || strings.EqualFold(strings.TrimSpace(documentTypeCode), "PURCHASE_DELIVERY") {
		return "'PRODUCT'::text"
	}
	return "line_type"
}

func PurchaseKindForResource(resource string) (PurchaseKind, bool) {
	switch strings.ToLower(strings.Trim(resource, "/")) {
	case "orders", "siparisler":
		return PurchaseOrderKind, true
	case "dispatches", "irsaliyeler", "receipts", "mal-kabulleri":
		return GoodsReceiptKind, true
	case "invoices", "faturalar":
		return PurchaseInvoiceKind, true
	case "returns", "iadeler":
		return PurchaseReturnKind, true
	default:
		return "", false
	}
}

func purchaseReadPermission(kind PurchaseKind) (string, bool) {
	switch kind {
	case PurchaseOrderKind:
		return "purchase.order.read", true
	case GoodsReceiptKind:
		return "purchase.receipt.post", true
	case PurchaseInvoiceKind:
		return "purchase.invoice.post", true
	case PurchaseReturnKind:
		return "purchase.return.post", true
	default:
		return "", false
	}
}

func (s *Service) ListPurchaseDocuments(ctx context.Context, session identity.Session, kind PurchaseKind, options PurchaseListOptions) (PurchaseListResult, error) {
	permission, ok := purchaseReadPermission(kind)
	if !ok {
		return PurchaseListResult{}, validation("satın alma belge türü geçersiz")
	}
	if err := s.authorizeReadPermission(session, permission); err != nil {
		return PurchaseListResult{}, err
	}
	if options.Limit < 1 || options.Limit > 100 {
		options.Limit = 50
	}
	if strings.TrimSpace(options.SupplierID) != "" && uuid.Validate(strings.TrimSpace(options.SupplierID)) != nil {
		return PurchaseListResult{}, validation("tedarikçi kimliği geçersiz")
	}
	if strings.TrimSpace(options.BranchID) != "" && uuid.Validate(strings.TrimSpace(options.BranchID)) != nil {
		return PurchaseListResult{}, validation("şube kimliği geçersiz")
	}
	if currency := strings.ToUpper(strings.TrimSpace(options.CurrencyCode)); currency != "" && !validCurrency(currency) {
		return PurchaseListResult{}, validation("para birimi geçersiz")
	}

	spec, ok := purchaseListSpec(kind)
	if !ok {
		return PurchaseListResult{}, validation("satın alma belge türü geçersiz")
	}
	args := []any{session.CurrentCompanyID, session.User.ID}
	query := purchaseListQuery(spec)
	query += purchaseReferenceListPredicateForTarget(options.ForReference, kind, options.ReferenceTarget)
	if status := strings.ToUpper(strings.TrimSpace(options.Status)); status != "" {
		args = append(args, status)
		query += fmt.Sprintf(" AND t.status=$%d", len(args))
	}
	if status := strings.TrimSpace(options.LifecycleStatus); status != "" {
		predicate, valid := purchaseLifecyclePredicate(kind, status)
		if !valid {
			return PurchaseListResult{}, validation("yaşam döngüsü durumu geçersiz")
		}
		query += " AND " + predicate
	}
	if status := strings.TrimSpace(options.FulfillmentStatus); status != "" {
		predicate, valid := purchaseFulfillmentPredicate(kind, status)
		if !valid {
			return PurchaseListResult{}, validation("karşılama durumu bu belge türünde kullanılamaz")
		}
		query += " AND " + predicate
	}
	if status := strings.TrimSpace(options.InvoicingStatus); status != "" {
		predicate, valid := purchaseInvoicingPredicate(kind, status)
		if !valid {
			return PurchaseListResult{}, validation("faturalama durumu bu belge türünde kullanılamaz")
		}
		query += " AND " + predicate
	}
	if status := strings.TrimSpace(options.PaymentStatus); status != "" {
		if kind != PurchaseInvoiceKind {
			return PurchaseListResult{}, validation("ödeme durumu bu belge türünde kullanılamaz")
		}
		predicate, valid := purchasePaymentPredicate(status)
		if !valid {
			return PurchaseListResult{}, validation("ödeme durumu geçersiz")
		}
		query += " AND " + predicate
	}
	if supplierID := strings.TrimSpace(options.SupplierID); supplierID != "" {
		args = append(args, supplierID)
		query += fmt.Sprintf(" AND t.supplier_id=$%d", len(args))
	}
	if branchID := strings.TrimSpace(options.BranchID); branchID != "" {
		args = append(args, branchID)
		query += fmt.Sprintf(" AND t.branch_id=$%d", len(args))
	}
	if currency := strings.ToUpper(strings.TrimSpace(options.CurrencyCode)); currency != "" {
		args = append(args, currency)
		query += fmt.Sprintf(" AND t.currency=$%d", len(args))
	}
	// Every token has to land somewhere on the row, so a two-word search narrows
	// the list instead of widening it.
	for _, token := range strings.Fields(strings.TrimSpace(options.Search)) {
		args = append(args, "%"+escapePurchaseSearchToken(token)+"%")
		param := len(args)
		query += fmt.Sprintf(" AND (t.%s ILIKE $%d ESCAPE '\\' OR p.code ILIKE $%d ESCAPE '\\' OR p.display_name ILIKE $%d ESCAPE '\\')", spec.documentNo, param, param, param)
	}
	if options.From != nil {
		args = append(args, *options.From)
		query += fmt.Sprintf(" AND t.%s >= $%d", spec.documentDate, len(args))
	}
	if options.To != nil {
		args = append(args, *options.To)
		query += fmt.Sprintf(" AND t.%s <= $%d", spec.documentDate, len(args))
	}
	sortExpr, sortDir := purchaseSortExpr(options.Sort, spec)
	offset := 0
	if sortExpr != "" {
		if cursor := strings.TrimSpace(options.Cursor); cursor != "" {
			parsed, convErr := strconv.Atoi(cursor)
			if convErr != nil || parsed < 0 || parsed > 20000 {
				return PurchaseListResult{}, validation("liste imleci geçersiz")
			}
			offset = parsed
		}
		args = append(args, options.Limit+1)
		query += fmt.Sprintf(" ORDER BY %s %s NULLS LAST,t.created_at DESC,t.id DESC LIMIT $%d OFFSET %d", sortExpr, sortDir, len(args), offset)
	} else {
		if cursor := strings.TrimSpace(options.Cursor); cursor != "" {
			parts := strings.SplitN(cursor, "|", 2)
			if len(parts) != 2 || uuid.Validate(parts[1]) != nil {
				return PurchaseListResult{}, validation("liste imleci geçersiz")
			}
			createdAt, err := time.Parse(time.RFC3339Nano, parts[0])
			if err != nil {
				return PurchaseListResult{}, validation("liste imleci geçersiz")
			}
			args = append(args, createdAt, parts[1])
			query += fmt.Sprintf(" AND (t.created_at,t.id) < ($%d,$%d)", len(args)-1, len(args))
		}
		args = append(args, options.Limit+1)
		// Newest first: the document a user just entered is at the top of the list.
		query += fmt.Sprintf(" ORDER BY t.created_at DESC,t.id DESC LIMIT $%d", len(args))
	}

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return PurchaseListResult{}, err
	}
	result := PurchaseListResult{Items: []PurchaseListItem{}}
	for rows.Next() {
		var item PurchaseListItem
		if err := rows.Scan(&item.ID, &item.CompanyID, &item.DocumentNo, &item.SupplierID, &item.SupplierCode, &item.SupplierName, &item.BranchID, &item.WarehouseID, &item.DocumentDate, &item.Currency, &item.Status, &item.Total, &item.TaxTotal, &item.GrandTotal, &item.PayableTotal, &item.Version, &item.CreatedAt); err != nil {
			rows.Close()
			return PurchaseListResult{}, err
		}
		item.Kind = kind
		result.Items = append(result.Items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return PurchaseListResult{}, err
	}
	rows.Close()
	for index := range result.Items {
		if err := s.applyPurchaseListStatuses(ctx, &result.Items[index]); err != nil {
			return PurchaseListResult{}, err
		}
	}
	if len(result.Items) > options.Limit {
		last := result.Items[options.Limit-1]
		result.Items = result.Items[:options.Limit]
		if sortExpr != "" {
			result.NextCursor = strconv.Itoa(offset + options.Limit)
		} else {
			result.NextCursor = last.CreatedAt.Format(time.RFC3339Nano) + "|" + last.ID
		}
	}
	return result, nil
}

func purchaseReferenceListPredicate(forReference bool) string {
	if !forReference {
		return ""
	}
	return " AND t.status <> 'DRAFT'"
}

func purchaseReferenceListPredicateForTarget(forReference bool, kind PurchaseKind, target string) string {
	if !forReference {
		return ""
	}
	switch kind {
	case PurchaseOrderKind:
		if strings.EqualFold(strings.TrimSpace(target), "invoices") {
			return " AND t.status IN ('CONFIRMED','PARTIALLY_FULFILLED','FULFILLED') AND EXISTS (SELECT 1 FROM purchase_order_lines l WHERE l.company_id=t.company_id AND l.order_id=t.id AND ((l.line_type='SERVICE' AND l.ordered_quantity>l.invoiced_quantity) OR (l.line_type='PRODUCT' AND l.received_quantity>l.invoiced_quantity)))"
		}
		return " AND t.status IN ('CONFIRMED','PARTIALLY_FULFILLED') AND EXISTS (SELECT 1 FROM purchase_order_lines l WHERE l.company_id=t.company_id AND l.order_id=t.id AND l.line_type='PRODUCT' AND l.ordered_quantity>l.received_quantity)"
	case GoodsReceiptKind:
		if strings.EqualFold(strings.TrimSpace(target), "returns") {
			return " AND t.status = 'POSTED'" + purchaseRemainingSourceClause("GOODS_RECEIPT", "RETURN")
		}
		return " AND t.status = 'POSTED'" + purchaseRemainingSourceClause("GOODS_RECEIPT", "INVOICING")
	case PurchaseInvoiceKind:
		return " AND t.status = 'POSTED'"
	case PurchaseReturnKind:
		return " AND t.status = 'POSTED'"
	default:
		return purchaseReferenceListPredicate(true)
	}
}

func purchaseRemainingSourceClause(aggregateType, allocationType string) string {
	return fmt.Sprintf(" AND EXISTS (SELECT 1 FROM commercial_line_registry sr WHERE sr.company_id=t.company_id AND sr.document_id=t.id AND sr.aggregate_type='%s' AND sr.base_quantity > COALESCE((SELECT SUM(a.base_quantity) FROM commercial_line_allocations a WHERE a.company_id=sr.company_id AND a.source_line_id=sr.line_id AND a.allocation_type='%s'),0))", aggregateType, allocationType)
}

func purchaseListQuery(spec purchaseListTableSpec) string {
	return fmt.Sprintf(`SELECT t.id,t.company_id,t.%s,t.supplier_id,COALESCE(p.code,''),COALESCE(p.display_name,''),t.branch_id,COALESCE(t.warehouse_id::text,''),t.%s,%s,t.status,(%s)::text AS total,(%s)::text AS tax_total,(%s)::text AS grand_total,(%s)::text AS payable_total,(%s)::bigint AS version,t.created_at
FROM %s t
JOIN parties p ON p.company_id=t.company_id AND p.id=t.supplier_id
WHERE t.company_id=$1
  AND EXISTS (SELECT 1 FROM branches b WHERE b.company_id=t.company_id AND b.id=t.branch_id AND b.is_active AND (NOT EXISTS (SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=t.company_id AND bs.user_id=$2) OR EXISTS (SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=t.company_id AND bs.user_id=$2 AND bs.branch_id=t.branch_id)))
  AND (t.warehouse_id IS NULL OR EXISTS (SELECT 1 FROM warehouses w WHERE w.company_id=t.company_id AND w.id=t.warehouse_id AND w.is_active AND (w.is_system OR w.branch_id IS NULL OR w.branch_id=t.branch_id) AND (w.is_system OR ((w.branch_id IS NULL OR NOT EXISTS (SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=t.company_id AND bs.user_id=$2) OR EXISTS (SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=t.company_id AND bs.user_id=$2 AND bs.branch_id=w.branch_id)) AND (NOT EXISTS (SELECT 1 FROM membership_warehouse_scopes ws WHERE ws.company_id=t.company_id AND ws.user_id=$2) OR EXISTS (SELECT 1 FROM membership_warehouse_scopes ws WHERE ws.company_id=t.company_id AND ws.user_id=$2 AND ws.warehouse_id=t.warehouse_id))))))
  AND NOT EXISTS (SELECT 1 FROM %s l WHERE l.company_id=t.company_id AND l.%s=t.id AND %s AND (l.warehouse_id IS NULL OR NOT EXISTS (SELECT 1 FROM warehouses lw WHERE lw.company_id=t.company_id AND lw.id=l.warehouse_id AND lw.is_active AND (lw.is_system OR lw.branch_id IS NULL OR lw.branch_id=t.branch_id) AND (lw.is_system OR ((lw.branch_id IS NULL OR NOT EXISTS (SELECT 1 FROM membership_branch_scopes lbs WHERE lbs.company_id=t.company_id AND lbs.user_id=$2) OR EXISTS (SELECT 1 FROM membership_branch_scopes lbs WHERE lbs.company_id=t.company_id AND lbs.user_id=$2 AND lbs.branch_id=lw.branch_id)) AND (NOT EXISTS (SELECT 1 FROM membership_warehouse_scopes lws WHERE lws.company_id=t.company_id AND lws.user_id=$2) OR EXISTS (SELECT 1 FROM membership_warehouse_scopes lws WHERE lws.company_id=t.company_id AND lws.user_id=$2 AND lws.warehouse_id=lw.id)))))))`, spec.documentNo, spec.documentDate, spec.currency, spec.total, spec.taxTotal, spec.grandTotal, spec.payableTotal, spec.version, spec.table, spec.lineTable, spec.lineParentColumn, spec.lineWarehousePredicate)
}

func escapePurchaseSearchToken(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	return strings.ReplaceAll(value, `_`, `\_`)
}

type purchaseListTableSpec struct {
	table                  string
	lineTable              string
	lineParentColumn       string
	lineWarehousePredicate string
	documentNo             string
	documentDate           string
	currency               string
	total                  string
	taxTotal               string
	grandTotal             string
	payableTotal           string
	version                string
}

func purchaseListSpec(kind PurchaseKind) (purchaseListTableSpec, bool) {
	switch kind {
	case PurchaseOrderKind:
		return purchaseListTableSpec{table: "purchase_orders", lineTable: "purchase_order_lines", lineParentColumn: "order_id", lineWarehousePredicate: "l.line_type='PRODUCT'", documentNo: "order_no", documentDate: "order_date", currency: "t.currency", total: "t.total", taxTotal: "CAST(0 AS numeric)", grandTotal: "t.total", payableTotal: "t.total", version: "t.version"}, true
	case GoodsReceiptKind:
		return purchaseListTableSpec{table: "goods_receipts", lineTable: "goods_receipt_lines", lineParentColumn: "receipt_id", lineWarehousePredicate: "TRUE", documentNo: "receipt_no", documentDate: "receipt_date", currency: "t.currency", total: "CAST(0 AS numeric)", taxTotal: "CAST(0 AS numeric)", grandTotal: "CAST(0 AS numeric)", payableTotal: "CAST(0 AS numeric)", version: "t.version"}, true
	case PurchaseInvoiceKind:
		return purchaseListTableSpec{table: "purchase_invoices", lineTable: "purchase_invoice_lines", lineParentColumn: "invoice_id", lineWarehousePredicate: "l.line_type='PRODUCT'", documentNo: "invoice_no", documentDate: "invoice_date", currency: "t.currency", total: "t.payable_total", taxTotal: "t.tax_total", grandTotal: "t.payable_total + COALESCE((SELECT SUM(l.withholding_amount) FROM purchase_invoice_lines l WHERE l.company_id=t.company_id AND l.invoice_id=t.id),0)", payableTotal: "t.payable_total", version: "t.version"}, true
	case PurchaseReturnKind:
		return purchaseListTableSpec{table: "purchase_returns", lineTable: "purchase_return_lines", lineParentColumn: "return_id", lineWarehousePredicate: "TRUE", documentNo: "return_no", documentDate: "return_date", currency: "t.currency", total: "t.total", taxTotal: "CAST(0 AS numeric)", grandTotal: "t.total", payableTotal: "t.total", version: "t.version"}, true
	default:
		return purchaseListTableSpec{}, false
	}
}

func (s *Service) authorizeReadPermission(session identity.Session, permission string) error {
	if identity.ValidateExternalActor(session) != nil {
		return identity.ErrForbidden
	}
	if session.HasPermission(permission) || session.HasPermission("purchase.order.manage") ||
		session.HasPermission("commercial.document.read") || session.HasPermission("commercial.document.manage") {
		return nil
	}
	// A ".draft" preparer can also read the document type they prepare.
	if draft := strings.TrimSuffix(permission, ".post") + ".draft"; draft != permission && session.HasPermission(draft) {
		return nil
	}
	return identity.ErrForbidden
}

func (s *Service) UpdatePurchaseOrder(ctx context.Context, session identity.Session, id string, expectedVersion int64, input PurchaseOrderInput, meta identity.RequestMeta) (PurchaseOrder, error) {
	if err := s.authorize(session, "purchase.order.manage"); err != nil {
		return PurchaseOrder{}, err
	}
	if expectedVersion < 1 {
		return PurchaseOrder{}, validation("taslak sürümü gereklidir")
	}
	if err := normalizePurchaseOrderInput(ctx, session, s, &input); err != nil {
		return PurchaseOrder{}, err
	}
	total := purchaseOrderTotal(input.Lines)
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return PurchaseOrder{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	replayID, replay, err := reserveCommand(ctx, tx, session, meta, "purchasing.purchase_order.update", map[string]any{"id": id, "version": expectedVersion, "input": input})
	if err != nil {
		return PurchaseOrder{}, err
	}
	if replay {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return s.GetPurchaseOrder(ctx, session, replayID)
	}
	var documentID, status string
	if err = tx.QueryRow(ctx, `SELECT COALESCE(document_id::text,''),status FROM purchase_orders WHERE company_id=$1 AND id=$2 AND version=$3 FOR UPDATE`, session.CurrentCompanyID, id, expectedVersion).Scan(&documentID, &status); errors.Is(err, pgx.ErrNoRows) {
		return PurchaseOrder{}, identity.ErrConflict
	} else if err != nil {
		return PurchaseOrder{}, err
	}
	if err = s.ensurePurchaseOrderScope(ctx, tx, session, id); err != nil {
		return PurchaseOrder{}, err
	}
	if status != "DRAFT" {
		return PurchaseOrder{}, validation("yalnız taslak alış siparişi düzenlenebilir")
	}
	var allocationCount int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM commercial_line_allocations a JOIN commercial_line_registry r ON r.company_id=a.company_id AND (r.line_id=a.source_line_id OR r.line_id=a.target_line_id) WHERE r.company_id=$1 AND r.document_id=$2`, session.CurrentCompanyID, id).Scan(&allocationCount); err != nil {
		return PurchaseOrder{}, err
	}
	if allocationCount > 0 {
		return PurchaseOrder{}, validation("kaynak belgeye bağlanmış alış siparişi düzenlenemez")
	}
	if _, err = tx.Exec(ctx, `UPDATE purchase_orders SET order_no=$1,supplier_id=$2,branch_id=$3,warehouse_id=$4,order_date=$5,currency=$6,over_delivery_policy=$7,notes=$8,total=$9,updated_by=$10,updated_at=now(),version=version+1 WHERE company_id=$11 AND id=$12 AND status='DRAFT' AND version=$13`, input.OrderNo, input.SupplierID, input.BranchID, input.WarehouseID, input.OrderDate, input.Currency, input.OverDeliveryPolicy, strings.TrimSpace(input.Notes), total, session.User.ID, session.CurrentCompanyID, id, expectedVersion); err != nil {
		return PurchaseOrder{}, err
	}
	if documentID != "" {
		if _, err = tx.Exec(ctx, `UPDATE documents SET document_no=$1,branch_id=$2,warehouse_id=$3,party_id=$4,document_date=$5,currency_code=$6,exchange_rate=$7,notes=$8,subtotal=$9,grand_total=$9,updated_by=$10,updated_at=now(),version=version+1 WHERE company_id=$11 AND id=$12 AND status='DRAFT'`, input.OrderNo, input.BranchID, input.WarehouseID, input.SupplierID, input.OrderDate, input.Currency, input.ExchangeRate, strings.TrimSpace(input.Notes), total, session.User.ID, session.CurrentCompanyID, documentID); err != nil {
			return PurchaseOrder{}, err
		}
	}
	if _, err = tx.Exec(ctx, `DELETE FROM purchase_order_lines WHERE company_id=$1 AND order_id=$2`, session.CurrentCompanyID, id); err != nil {
		return PurchaseOrder{}, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM commercial_line_registry WHERE company_id=$1 AND document_id=$2`, session.CurrentCompanyID, id); err != nil {
		return PurchaseOrder{}, err
	}
	for index := range input.Lines {
		line := &input.Lines[index]
		if err = ensurePurchaseProduct(ctx, tx, session.CurrentCompanyID, line.ProductID, line.VariantID, line.LineType); err != nil {
			return PurchaseOrder{}, err
		}
		line.WarehouseID = strings.TrimSpace(line.WarehouseID)
		if line.LineType == "SERVICE" {
			if line.WarehouseID != "" {
				return PurchaseOrder{}, validation("hizmet satırında depo bulunamaz")
			}
		} else {
			if line.WarehouseID == "" {
				line.WarehouseID = input.WarehouseID
			}
			if err = s.ensureScope(ctx, session, input.BranchID, line.WarehouseID); err != nil {
				return PurchaseOrder{}, err
			}
		}
		line.ID, line.LineNo = uuid.NewString(), index+1
		line.BaseQuantity, line.ConversionFactor, err = resolvePurchaseConversionTx(ctx, tx, session.CurrentCompanyID, line.ProductID, line.UnitCode, line.OrderedQuantity, line.BaseQuantity, line.ConversionFactor)
		if err != nil {
			return PurchaseOrder{}, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO purchase_order_lines(id,company_id,order_id,line_no,line_type,product_id,variant_id,warehouse_id,supplier_product_code_snapshot,product_code_snapshot,product_name_snapshot,unit_code,ordered_quantity,base_quantity,conversion_factor,unit_price,discount_amount,net_amount,currency,tax_snapshot) VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,'')::uuid,NULLIF($8,'')::uuid,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)`, line.ID, session.CurrentCompanyID, id, line.LineNo, line.LineType, line.ProductID, line.VariantID, line.WarehouseID, line.SupplierProductCodeSnapshot, line.ProductCodeSnapshot, line.ProductNameSnapshot, line.UnitCode, line.OrderedQuantity, line.BaseQuantity, line.ConversionFactor, line.UnitPrice, line.DiscountAmount, line.NetAmount, input.Currency, jsonObject(line.TaxSnapshot)); err != nil {
			return PurchaseOrder{}, err
		}
		if err = registerPurchaseLineTx(ctx, tx, session.CurrentCompanyID, "PURCHASE_ORDER", id, line.ID, line.LineNo, line.LineType, line.OrderedQuantity, line.BaseQuantity); err != nil {
			return PurchaseOrder{}, err
		}
	}
	if err = s.auditEventTx(ctx, tx, session, "PURCHASE_ORDER_UPDATED", "purchase.order.updated", id, meta, map[string]any{"version": expectedVersion + 1}); err != nil {
		return PurchaseOrder{}, err
	}
	if err = completeCommand(ctx, tx, session, meta, id); err != nil {
		return PurchaseOrder{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return PurchaseOrder{}, err
	}
	return s.GetPurchaseOrder(ctx, session, id)
}

func (s *Service) DeletePurchaseOrder(ctx context.Context, session identity.Session, id string, expectedVersion int64, meta identity.RequestMeta) error {
	if err := s.authorize(session, "purchase.order.manage"); err != nil {
		return err
	}
	if expectedVersion < 1 {
		return validation("taslak sürümü gereklidir")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	replayID, replay, err := reserveCommand(ctx, tx, session, meta, "purchasing.purchase_order.delete", map[string]any{"id": id, "version": expectedVersion})
	if err != nil {
		return err
	}
	if replay {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		_ = replayID
		return nil
	}
	var documentID, status string
	if err = tx.QueryRow(ctx, `SELECT COALESCE(document_id::text,''),status FROM purchase_orders WHERE company_id=$1 AND id=$2 AND version=$3 FOR UPDATE`, session.CurrentCompanyID, id, expectedVersion).Scan(&documentID, &status); errors.Is(err, pgx.ErrNoRows) {
		return identity.ErrConflict
	} else if err != nil {
		return err
	}
	if err = s.ensurePurchaseOrderScope(ctx, tx, session, id); err != nil {
		return err
	}
	if status != "DRAFT" {
		return validation("yalnız taslak alış siparişi silinebilir")
	}
	var allocationCount int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM commercial_line_allocations a JOIN commercial_line_registry r ON r.company_id=a.company_id AND (r.line_id=a.source_line_id OR r.line_id=a.target_line_id) WHERE r.company_id=$1 AND r.document_id=$2`, session.CurrentCompanyID, id).Scan(&allocationCount); err != nil {
		return err
	}
	if allocationCount > 0 {
		return validation("kaynak belgeye bağlanmış alış siparişi silinemez")
	}
	if _, err = tx.Exec(ctx, `DELETE FROM purchase_order_lines WHERE company_id=$1 AND order_id=$2`, session.CurrentCompanyID, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM commercial_line_registry WHERE company_id=$1 AND document_id=$2`, session.CurrentCompanyID, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM purchase_orders WHERE company_id=$1 AND id=$2 AND status='DRAFT' AND version=$3`, session.CurrentCompanyID, id, expectedVersion); err != nil {
		return err
	}
	if documentID != "" {
		if _, err = tx.Exec(ctx, `DELETE FROM documents WHERE company_id=$1 AND id=$2 AND status='DRAFT'`, session.CurrentCompanyID, documentID); err != nil {
			return err
		}
	}
	if err = s.auditEventTx(ctx, tx, session, "PURCHASE_ORDER_DELETED", "purchase.order.deleted", id, meta, nil); err != nil {
		return err
	}
	if err = completeCommand(ctx, tx, session, meta, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func normalizePurchaseOrderInput(ctx context.Context, session identity.Session, service *Service, input *PurchaseOrderInput) error {
	if input == nil {
		return validation("alış siparişi bilgileri gereklidir")
	}
	if input.OrderDate.IsZero() {
		input.OrderDate = time.Now().UTC().Truncate(24 * time.Hour)
	}
	input.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))
	input.OverDeliveryPolicy = strings.ToUpper(strings.TrimSpace(input.OverDeliveryPolicy))
	if input.OverDeliveryPolicy == "" {
		input.OverDeliveryPolicy = "WARN"
	}
	if !validCurrency(input.Currency) || !validPolicy(input.OverDeliveryPolicy) {
		return validation("sipariş tarihi, para birimi ve politika gereklidir")
	}
	if err := service.ensureExchangeRate(ctx, session, input.Currency, input.OrderDate, &input.ExchangeRate); err != nil {
		return err
	}
	if err := service.ensureScope(ctx, session, input.BranchID, input.WarehouseID); err != nil {
		return err
	}
	if err := service.ensureSupplier(ctx, session.CurrentCompanyID, input.SupplierID); err != nil {
		return err
	}
	for index := range input.Lines {
		if err := validateOrderLine(&input.Lines[index], input.Currency); err != nil {
			return err
		}
		if input.Lines[index].LineType == "SERVICE" && strings.TrimSpace(input.Lines[index].WarehouseID) != "" {
			return validation(fmt.Sprintf("%d. hizmet satırında depo bulunamaz", index+1))
		}
	}
	return nil
}

// purchaseOrderTotal sums the lines' net amounts, so a line discount reaches
// the header the same way it does on create.
func purchaseOrderTotal(lines []PurchaseOrderLine) string {
	total := "0"
	for _, line := range lines {
		total = add(total, subtract(multiply(line.OrderedQuantity, line.UnitPrice), zero(line.DiscountAmount)))
	}
	return total
}
