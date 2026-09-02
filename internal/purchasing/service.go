package purchasing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/finance"
	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/inventory"
	"github.com/alpyxn/varyaone/internal/platform/database"
	"github.com/alpyxn/varyaone/internal/platform/idempotency"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrNotFound                = errors.New("purchasing record not found")
	ErrOverDelivery            = errors.New("purchase receipt exceeds ordered quantity")
	ErrInvalidTransition       = errors.New("INVALID_STATE_TRANSITION")
	ErrVariantRequired         = errors.New("variant required")
	ErrExchangeRateUnavailable = errors.New("exchange rate unavailable")
	ErrProductInactive         = errors.New("PRODUCT_INACTIVE")
	ErrVariantInactive         = errors.New("VARIANT_INACTIVE")
	ErrSupplierInactive        = errors.New("PARTY_INACTIVE")
	ErrWarehouseInactive       = errors.New("WAREHOUSE_INACTIVE")
	ErrWarehouseRequired       = errors.New("WAREHOUSE_REQUIRED")
	ErrDocumentModified        = errors.New("DOCUMENT_MODIFIED")
	ErrInvalidStateTransition  = errors.New("INVALID_STATE_TRANSITION")
	ErrDocumentHasNoLines      = errors.New("DOCUMENT_HAS_NO_LINES")
)

type StockPoster interface {
	PostPurchaseReceiptMovementsTx(context.Context, pgx.Tx, identity.Session, inventory.PurchaseReceiptStockPostingInput) error
	PostPurchaseReturnMovementsTx(context.Context, pgx.Tx, identity.Session, inventory.PurchaseReturnStockPostingInput) error
}

// PurchaseInvoiceStockPoster is optional at the interface boundary so older
// composition roots can still compile; production wiring must provide it for
// direct purchase invoices because those invoices create inbound stock.
type PurchaseInvoiceStockPoster interface {
	PostPurchaseInvoiceMovementsTx(context.Context, pgx.Tx, identity.Session, inventory.PurchaseReceiptStockPostingInput) error
}

// PurchaseStockReverser is an optional composition-root extension. A posted
// receipt, direct invoice, or return can only be cancelled when its immutable
// stock movements are compensated in the same transaction.
type PurchaseStockReverser interface {
	ReversePurchaseMovementsTx(context.Context, pgx.Tx, identity.Session, inventory.PurchaseStockReversalInput) error
}

type FinancePoster interface {
	PostPurchaseInvoiceTx(context.Context, pgx.Tx, identity.Session, PurchaseInvoicePostingInput) (string, error)
	PostPurchaseReturnTx(context.Context, pgx.Tx, identity.Session, PurchaseInvoicePostingInput) (string, error)
}

type FinanceReverser interface {
	ReversePurchaseInvoiceTx(context.Context, pgx.Tx, identity.Session, string, string, string) (string, error)
	ReversePurchaseReturnTx(context.Context, pgx.Tx, identity.Session, string, string, string) (string, error)
}

type Service struct {
	pool         database.Querier
	stockPoster  StockPoster
	financePost  FinancePoster
	rateResolver ExchangeRateResolver
}

// ExchangeRateResolver keeps provider-specific rate retrieval outside the
// purchasing bounded context while allowing finance snapshots to use the
// authoritative document rate.
type ExchangeRateResolver interface {
	ResolveRate(context.Context, string, string, time.Time) (string, error)
}

func NewService(pool database.Querier, stockPoster StockPoster, financePoster FinancePoster, resolvers ...ExchangeRateResolver) *Service {
	service := &Service{pool: pool, stockPoster: stockPoster, financePost: financePoster}
	if len(resolvers) > 0 {
		service.rateResolver = resolvers[0]
	}
	return service
}

type PurchaseOrder struct {
	ID                 string                     `json:"id"`
	CompanyID          string                     `json:"company_id"`
	DocumentID         string                     `json:"document_id,omitempty"`
	OrderNo            string                     `json:"order_no"`
	SupplierID         string                     `json:"supplier_id"`
	SupplierName       string                     `json:"supplier_name,omitempty"`
	SupplierCode       string                     `json:"supplier_code,omitempty"`
	BranchID           string                     `json:"branch_id"`
	WarehouseID        string                     `json:"warehouse_id"`
	WarehouseName      string                     `json:"warehouse_name,omitempty"`
	WarehouseCode      string                     `json:"warehouse_code,omitempty"`
	OrderDate          time.Time                  `json:"order_date"`
	Currency           string                     `json:"currency"`
	ExchangeRate       string                     `json:"exchange_rate"`
	Status             string                     `json:"status"`
	LifecycleStatus    string                     `json:"lifecycle_status"`
	FulfillmentStatus  string                     `json:"fulfillment_status,omitempty"`
	FulfillmentAt      *time.Time                 `json:"fulfillment_at,omitempty"`
	InvoicingStatus    string                     `json:"invoicing_status,omitempty"`
	CancelledAt        *time.Time                 `json:"cancelled_at,omitempty"`
	CancellationReason *string                    `json:"cancellation_reason,omitempty"`
	OverDeliveryPolicy string                     `json:"over_delivery_policy"`
	Notes              string                     `json:"notes"`
	Total              string                     `json:"total"`
	Version            int64                      `json:"version"`
	SourceDocuments    []SourceDocumentReference  `json:"source_documents"`
	RelatedDocuments   []SourceDocumentReference  `json:"related_documents,omitempty"`
	AvailableActions   PurchaseActionAvailability `json:"available_actions"`
	Lines              []PurchaseOrderLine        `json:"lines"`
}

type PurchaseOrderLine struct {
	ID                          string         `json:"id"`
	LineNo                      int            `json:"line_no"`
	LineType                    string         `json:"line_type"`
	ProductID                   string         `json:"product_id"`
	VariantID                   string         `json:"variant_id,omitempty"`
	SupplierProductCodeSnapshot string         `json:"supplier_product_code_snapshot,omitempty"`
	ProductCodeSnapshot         string         `json:"product_code_snapshot,omitempty"`
	ProductNameSnapshot         string         `json:"product_name_snapshot"`
	WarehouseID                 string         `json:"warehouse_id,omitempty"`
	WarehouseName               string         `json:"warehouse_name,omitempty"`
	WarehouseCode               string         `json:"warehouse_code,omitempty"`
	UnitCode                    string         `json:"unit_code"`
	OrderedQuantity             string         `json:"ordered_quantity"`
	BaseQuantity                string         `json:"base_quantity"`
	ConversionFactor            string         `json:"conversion_factor"`
	ReceivedQuantity            string         `json:"received_quantity"`
	InvoicedQuantity            string         `json:"invoiced_quantity"`
	UnitPrice                   string         `json:"unit_price"`
	DiscountAmount              string         `json:"discount_amount,omitempty"`
	NetAmount                   string         `json:"net_amount,omitempty"`
	Currency                    string         `json:"currency"`
	TaxSnapshot                 map[string]any `json:"tax_snapshot,omitempty"`
}

type PurchaseOrderInput struct {
	OrderNo            string              `json:"order_no,omitempty"`
	SupplierID         string              `json:"supplier_id"`
	BranchID           string              `json:"branch_id"`
	WarehouseID        string              `json:"warehouse_id"`
	OrderDate          time.Time           `json:"order_date"`
	Currency           string              `json:"currency"`
	ExchangeRate       string              `json:"exchange_rate,omitempty"`
	OverDeliveryPolicy string              `json:"over_delivery_policy,omitempty"`
	Notes              string              `json:"notes,omitempty"`
	Lines              []PurchaseOrderLine `json:"lines"`
}

type GoodsReceipt struct {
	ID                  string                     `json:"id"`
	CompanyID           string                     `json:"company_id"`
	DocumentID          string                     `json:"document_id,omitempty"`
	ReceiptNo           string                     `json:"receipt_no"`
	PurchaseOrderID     *string                    `json:"purchase_order_id,omitempty"`
	SupplierID          string                     `json:"supplier_id"`
	SupplierName        string                     `json:"supplier_name,omitempty"`
	SupplierCode        string                     `json:"supplier_code,omitempty"`
	BranchID            string                     `json:"branch_id"`
	WarehouseID         string                     `json:"warehouse_id"`
	WarehouseName       string                     `json:"warehouse_name,omitempty"`
	WarehouseCode       string                     `json:"warehouse_code,omitempty"`
	ReceiptDate         time.Time                  `json:"receipt_date"`
	FulfillmentAt       *time.Time                 `json:"fulfillment_at,omitempty"`
	Currency            string                     `json:"currency"`
	ExchangeRate        string                     `json:"exchange_rate"`
	Status              string                     `json:"status"`
	LifecycleStatus     string                     `json:"lifecycle_status"`
	InvoicingStatus     string                     `json:"invoicing_status,omitempty"`
	Version             int64                      `json:"version"`
	CancelledAt         *time.Time                 `json:"cancelled_at,omitempty"`
	CancellationReason  *string                    `json:"cancellation_reason,omitempty"`
	OverDeliveryWarning bool                       `json:"over_delivery_warning"`
	Notes               string                     `json:"notes"`
	SourceDocuments     []SourceDocumentReference  `json:"source_documents"`
	RelatedDocuments    []SourceDocumentReference  `json:"related_documents,omitempty"`
	AvailableActions    PurchaseActionAvailability `json:"available_actions"`
	Lines               []GoodsReceiptLine         `json:"lines"`
}

type GoodsReceiptLine struct {
	ID                             string         `json:"id"`
	LineNo                         int            `json:"line_no"`
	PurchaseOrderLineID            *string        `json:"purchase_order_line_id,omitempty"`
	ProductID                      string         `json:"product_id"`
	VariantID                      string         `json:"variant_id,omitempty"`
	AcceptedQuantity               string         `json:"accepted_quantity"`
	DamagedQuantity                string         `json:"damaged_quantity"`
	RejectedQuantity               string         `json:"rejected_quantity"`
	WarehouseID                    string         `json:"warehouse_id"`
	WarehouseName                  string         `json:"warehouse_name,omitempty"`
	WarehouseCode                  string         `json:"warehouse_code,omitempty"`
	UnitCode                       string         `json:"unit_code"`
	BaseQuantity                   string         `json:"base_quantity"`
	ConversionFactor               string         `json:"conversion_factor"`
	RemainingInvoicingQuantity     string         `json:"remaining_invoicing_quantity,omitempty"`
	RemainingInvoicingBaseQuantity string         `json:"remaining_invoicing_base_quantity,omitempty"`
	RemainingReturnQuantity        string         `json:"remaining_return_quantity,omitempty"`
	RemainingReturnBaseQuantity    string         `json:"remaining_return_base_quantity,omitempty"`
	UnitCost                       string         `json:"unit_cost"`
	Currency                       string         `json:"currency"`
	LotSnapshot                    []any          `json:"lot_snapshot,omitempty"`
	SerialSnapshot                 []any          `json:"serial_snapshot,omitempty"`
	TaxSnapshot                    map[string]any `json:"tax_snapshot,omitempty"`
}

type GoodsReceiptInput struct {
	ReceiptNo       string             `json:"receipt_no,omitempty"`
	PurchaseOrderID string             `json:"purchase_order_id,omitempty"`
	SupplierID      string             `json:"supplier_id"`
	BranchID        string             `json:"branch_id"`
	WarehouseID     string             `json:"warehouse_id"`
	ReceiptDate     time.Time          `json:"receipt_date"`
	Currency        string             `json:"currency,omitempty"`
	ExchangeRate    string             `json:"exchange_rate,omitempty"`
	Notes           string             `json:"notes,omitempty"`
	Lines           []GoodsReceiptLine `json:"lines"`
}

type PurchaseInvoice struct {
	ID                 string                      `json:"id"`
	CompanyID          string                      `json:"company_id"`
	InvoiceNo          string                      `json:"invoice_no"`
	SupplierID         string                      `json:"supplier_id"`
	SupplierName       string                      `json:"supplier_name,omitempty"`
	SupplierCode       string                      `json:"supplier_code,omitempty"`
	BranchID           string                      `json:"branch_id"`
	WarehouseID        string                      `json:"warehouse_id,omitempty"`
	WarehouseName      string                      `json:"warehouse_name,omitempty"`
	WarehouseCode      string                      `json:"warehouse_code,omitempty"`
	PurchaseOrderID    *string                     `json:"purchase_order_id,omitempty"`
	GoodsReceiptID     *string                     `json:"goods_receipt_id,omitempty"`
	GoodsReceiptIDs    []string                    `json:"goods_receipt_ids,omitempty"`
	InvoiceDate        time.Time                   `json:"invoice_date"`
	DueDate            *time.Time                  `json:"due_date,omitempty"`
	Currency           string                      `json:"currency"`
	ExchangeRate       string                      `json:"exchange_rate"`
	Status             string                      `json:"status"`
	LifecycleStatus    string                      `json:"lifecycle_status"`
	PaymentStatus      string                      `json:"payment_status,omitempty"`
	Settlement         *finance.DocumentSettlement `json:"settlement,omitempty"`
	Version            int64                       `json:"version"`
	CancelledAt        *time.Time                  `json:"cancelled_at,omitempty"`
	CancellationReason *string                     `json:"cancellation_reason,omitempty"`
	Subtotal           string                      `json:"subtotal"`
	DiscountTotal      string                      `json:"discount_total"`
	TaxTotal           string                      `json:"tax_total"`
	GrandTotal         string                      `json:"grand_total"`
	PayableTotal       string                      `json:"payable_total"`
	FinancePostingID   *string                     `json:"finance_posting_id,omitempty"`
	SourceDocuments    []SourceDocumentReference   `json:"source_documents"`
	RelatedDocuments   []SourceDocumentReference   `json:"related_documents,omitempty"`
	AvailableActions   PurchaseActionAvailability  `json:"available_actions"`
	Lines              []PurchaseInvoiceLine       `json:"lines"`
}

type PurchaseInvoiceLine struct {
	ID                    string `json:"id"`
	LineNo                int    `json:"line_no"`
	LineType              string `json:"line_type"`
	PurchaseOrderLineID   string `json:"purchase_order_line_id,omitempty"`
	GoodsReceiptLineID    string `json:"goods_receipt_line_id,omitempty"`
	ProductID             string `json:"product_id"`
	VariantID             string `json:"variant_id,omitempty"`
	WarehouseID           string `json:"warehouse_id,omitempty"`
	WarehouseName         string `json:"warehouse_name,omitempty"`
	WarehouseCode         string `json:"warehouse_code,omitempty"`
	UnitCode              string `json:"unit_code"`
	BaseQuantity          string `json:"base_quantity"`
	ConversionFactor      string `json:"conversion_factor"`
	DescriptionSnapshot   string `json:"description_snapshot"`
	Quantity              string `json:"quantity"`
	UnitPrice             string `json:"unit_price"`
	GrossAmount           string `json:"gross_amount"`
	DiscountAmount        string `json:"discount_amount"`
	TaxBase               string `json:"tax_base"`
	TaxAmount             string `json:"tax_amount"`
	WithholdingAmount     string `json:"withholding_amount"`
	PayableAmount         string `json:"payable_amount"`
	TaxComponentsSnapshot []any  `json:"tax_components_snapshot,omitempty"`
}

type PurchaseInvoiceInput struct {
	InvoiceNo       string                `json:"invoice_no,omitempty"`
	SupplierID      string                `json:"supplier_id"`
	BranchID        string                `json:"branch_id"`
	WarehouseID     string                `json:"warehouse_id,omitempty"`
	PurchaseOrderID string                `json:"purchase_order_id,omitempty"`
	GoodsReceiptID  string                `json:"goods_receipt_id,omitempty"`
	GoodsReceiptIDs []string              `json:"goods_receipt_ids,omitempty"`
	Standalone      bool                  `json:"standalone,omitempty"`
	InvoiceDate     time.Time             `json:"invoice_date"`
	DueDate         *time.Time            `json:"due_date,omitempty"`
	Currency        string                `json:"currency"`
	ExchangeRate    string                `json:"exchange_rate,omitempty"`
	Lines           []PurchaseInvoiceLine `json:"lines"`
}

type PurchaseInvoicePostingInput struct {
	InvoiceID    string
	SupplierID   string
	Currency     string
	Amount       string
	ExchangeRate string
	InvoiceDate  time.Time
	DueDate      *time.Time
	Description  string
}

func purchaseInvoicePostsStock(line PurchaseInvoiceLine) bool {
	// Only a truly standalone product invoice receives stock. An order-linked
	// product must first be received and its invoice only records the already
	// received quantity; otherwise the invoice would bypass fulfillment and
	// add stock a second time.
	return normalizePurchaseLineType(line.LineType) == "PRODUCT" &&
		strings.TrimSpace(line.GoodsReceiptLineID) == "" &&
		strings.TrimSpace(line.PurchaseOrderLineID) == ""
}

func purchaseReceiptIDs(input PurchaseInvoiceInput) []string {
	result := make([]string, 0, 1+len(input.GoodsReceiptIDs))
	seen := make(map[string]struct{}, 1+len(input.GoodsReceiptIDs))
	for _, value := range append([]string{input.GoodsReceiptID}, input.GoodsReceiptIDs...) {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

type PurchaseReturn struct {
	ID                 string                     `json:"id"`
	CompanyID          string                     `json:"company_id"`
	ReturnNo           string                     `json:"return_no"`
	SupplierID         string                     `json:"supplier_id"`
	SupplierName       string                     `json:"supplier_name,omitempty"`
	SupplierCode       string                     `json:"supplier_code,omitempty"`
	BranchID           string                     `json:"branch_id"`
	WarehouseID        string                     `json:"warehouse_id"`
	WarehouseName      string                     `json:"warehouse_name,omitempty"`
	WarehouseCode      string                     `json:"warehouse_code,omitempty"`
	SourceReceiptID    *string                    `json:"source_receipt_id,omitempty"`
	ReturnDate         time.Time                  `json:"return_date"`
	Currency           string                     `json:"currency"`
	ExchangeRate       string                     `json:"exchange_rate"`
	Status             string                     `json:"status"`
	LifecycleStatus    string                     `json:"lifecycle_status"`
	Version            int64                      `json:"version"`
	CancelledAt        *time.Time                 `json:"cancelled_at,omitempty"`
	CancellationReason *string                    `json:"cancellation_reason,omitempty"`
	Total              string                     `json:"total"`
	Reason             string                     `json:"reason"`
	FinancePostingID   *string                    `json:"finance_posting_id,omitempty"`
	SourceDocuments    []SourceDocumentReference  `json:"source_documents"`
	RelatedDocuments   []SourceDocumentReference  `json:"related_documents,omitempty"`
	AvailableActions   PurchaseActionAvailability `json:"available_actions"`
	Lines              []PurchaseReturnLine       `json:"lines"`
}

// SourceDocumentReference is the read-only, company-scoped representation of
// a purchasing document relation. The number and status come from the
// authoritative document registry and typed aggregate, never from a request.
type SourceDocumentReference struct {
	ID               string `json:"id"`
	DocumentNo       string `json:"document_no"`
	DocumentTypeCode string `json:"document_type_code"`
	Kind             string `json:"kind"`
	RelationType     string `json:"relation_type"`
	Direction        string `json:"direction"`
	LifecycleStatus  string `json:"lifecycle_status"`
	Status           string `json:"status"`
}

// PurchaseActionAvailability is the server-authoritative action matrix for a
// purchasing detail. Commands still enforce state, scope and dependencies.
type PurchaseActionAvailability struct {
	CanEdit            bool `json:"can_edit"`
	CanDelete          bool `json:"can_delete"`
	CanPost            bool `json:"can_post"`
	CanCancel          bool `json:"can_cancel"`
	CanCreateReturn    bool `json:"can_create_return"`
	CanCollect         bool `json:"can_collect"`
	CanPay             bool `json:"can_pay"`
	CanCreateDispatch  bool `json:"can_create_dispatch"`
	CanCreateInvoice   bool `json:"can_create_invoice"`
	CanCreateEDocument bool `json:"can_create_edocument"`
}

type PurchaseReturnLine struct {
	ID                  string `json:"id"`
	LineNo              int    `json:"line_no"`
	SourceReceiptLineID string `json:"source_receipt_line_id,omitempty"`
	ProductID           string `json:"product_id"`
	VariantID           string `json:"variant_id,omitempty"`
	WarehouseID         string `json:"warehouse_id"`
	WarehouseName       string `json:"warehouse_name,omitempty"`
	WarehouseCode       string `json:"warehouse_code,omitempty"`
	Quantity            string `json:"quantity"`
	BaseQuantity        string `json:"base_quantity"`
	ConversionFactor    string `json:"conversion_factor"`
	UnitCode            string `json:"unit_code"`
	UnitCost            string `json:"unit_cost"`
	Currency            string `json:"currency"`
	Reason              string `json:"reason,omitempty"`
}

type PurchaseReturnInput struct {
	ReturnNo        string               `json:"return_no,omitempty"`
	SupplierID      string               `json:"supplier_id"`
	BranchID        string               `json:"branch_id"`
	WarehouseID     string               `json:"warehouse_id"`
	SourceReceiptID string               `json:"source_receipt_id,omitempty"`
	ReturnDate      time.Time            `json:"return_date"`
	Currency        string               `json:"currency"`
	ExchangeRate    string               `json:"exchange_rate,omitempty"`
	Reason          string               `json:"reason"`
	Lines           []PurchaseReturnLine `json:"lines"`
}

type SupplierProductReference struct {
	ID           string `json:"id"`
	SupplierID   string `json:"supplier_id"`
	ProductID    string `json:"product_id"`
	VariantID    string `json:"variant_id,omitempty"`
	SupplierCode string `json:"supplier_code,omitempty"`
	Barcode      string `json:"barcode,omitempty"`
	IsActive     bool   `json:"is_active"`
	Version      int64  `json:"version"`
}

func (s *Service) CreatePurchaseOrder(ctx context.Context, session identity.Session, input PurchaseOrderInput, meta identity.RequestMeta) (PurchaseOrder, error) {
	if err := s.authorize(session, "purchase.order.manage"); err != nil {
		return PurchaseOrder{}, err
	}
	if input.OrderDate.IsZero() {
		input.OrderDate = time.Now().UTC().Truncate(24 * time.Hour)
	}
	input.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))
	if err := s.ensureExchangeRate(ctx, session, input.Currency, input.OrderDate, &input.ExchangeRate); err != nil {
		return PurchaseOrder{}, err
	}
	input.OverDeliveryPolicy = strings.ToUpper(strings.TrimSpace(input.OverDeliveryPolicy))
	if input.OverDeliveryPolicy == "" {
		input.OverDeliveryPolicy = "WARN"
	}
	if !validCurrency(input.Currency) || !validPolicy(input.OverDeliveryPolicy) {
		return PurchaseOrder{}, validation("sipariş tarihi, para birimi ve politika gereklidir")
	}
	if err := s.ensureScope(ctx, session, input.BranchID, input.WarehouseID); err != nil {
		return PurchaseOrder{}, err
	}
	if err := s.ensureSupplier(ctx, session.CurrentCompanyID, input.SupplierID); err != nil {
		return PurchaseOrder{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return PurchaseOrder{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	replayID, replay, err := reserveCommand(ctx, tx, session, meta, "purchasing.purchase_order.create", input)
	if err != nil {
		return PurchaseOrder{}, err
	}
	if replay {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return s.GetPurchaseOrder(ctx, session, replayID)
	}
	orderID := uuid.NewString()
	orderNo, err := s.number(ctx, tx, session.CurrentCompanyID, "PURCHASE_ORDER", "PO", input.OrderNo, input.OrderDate.Year())
	if err != nil {
		return PurchaseOrder{}, err
	}
	total := "0"
	for index := range input.Lines {
		line := &input.Lines[index]
		if err = validateOrderLine(line, input.Currency); err != nil {
			return PurchaseOrder{}, err
		}
		line.WarehouseID = strings.TrimSpace(line.WarehouseID)
		if err = ensurePurchaseProduct(ctx, tx, session.CurrentCompanyID, line.ProductID, line.VariantID, line.LineType); err != nil {
			return PurchaseOrder{}, err
		}
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
		line.BaseQuantity, line.ConversionFactor, err = resolvePurchaseConversionTx(ctx, tx, session.CurrentCompanyID, line.ProductID, line.UnitCode, line.OrderedQuantity, line.BaseQuantity, line.ConversionFactor)
		if err != nil {
			return PurchaseOrder{}, err
		}
		lineID := uuid.NewString()
		lineNo := index + 1
		line.ID, line.LineNo = lineID, lineNo
		total = add(total, multiply(line.OrderedQuantity, line.UnitPrice))
	}
	if err = insertPurchaseDocumentAnchorTx(ctx, tx, session, orderID, "PURCHASE_ORDER", orderNo, input.BranchID, input.WarehouseID, input.SupplierID, input.OrderDate, nil, input.Currency, input.ExchangeRate, "Alış siparişi", total, "0", "0", total); err != nil {
		return PurchaseOrder{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO purchase_orders(id,company_id,document_id,order_no,supplier_id,branch_id,warehouse_id,order_date,currency,over_delivery_policy,notes,total,created_by,updated_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$13)`, orderID, session.CurrentCompanyID, orderID, orderNo, input.SupplierID, input.BranchID, input.WarehouseID, input.OrderDate, input.Currency, input.OverDeliveryPolicy, strings.TrimSpace(input.Notes), total, session.User.ID); err != nil {
		return PurchaseOrder{}, err
	}
	for _, line := range input.Lines {
		if _, err = tx.Exec(ctx, `INSERT INTO purchase_order_lines(id,company_id,order_id,line_no,line_type,product_id,variant_id,warehouse_id,supplier_product_code_snapshot,product_code_snapshot,product_name_snapshot,unit_code,ordered_quantity,base_quantity,conversion_factor,unit_price,discount_amount,net_amount,currency,tax_snapshot) VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,'')::uuid,NULLIF($8,'')::uuid,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)`, line.ID, session.CurrentCompanyID, orderID, line.LineNo, line.LineType, line.ProductID, line.VariantID, line.WarehouseID, line.SupplierProductCodeSnapshot, line.ProductCodeSnapshot, line.ProductNameSnapshot, line.UnitCode, line.OrderedQuantity, line.BaseQuantity, line.ConversionFactor, line.UnitPrice, line.DiscountAmount, line.NetAmount, input.Currency, jsonObject(line.TaxSnapshot)); err != nil {
			return PurchaseOrder{}, err
		}
		if err = registerPurchaseLineTx(ctx, tx, session.CurrentCompanyID, "PURCHASE_ORDER", orderID, line.ID, line.LineNo, line.LineType, line.OrderedQuantity, line.BaseQuantity); err != nil {
			return PurchaseOrder{}, err
		}
	}
	if err = s.auditEventTx(ctx, tx, session, "PURCHASE_ORDER_CREATED", "purchase.order.created", orderID, meta, map[string]any{"order_no": orderNo}); err != nil {
		return PurchaseOrder{}, err
	}
	if err = completeCommand(ctx, tx, session, meta, orderID); err != nil {
		return PurchaseOrder{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return PurchaseOrder{}, err
	}
	return s.GetPurchaseOrder(ctx, session, orderID)
}

func (s *Service) ConfirmPurchaseOrder(ctx context.Context, session identity.Session, id string, expectedVersion int64, meta identity.RequestMeta) (PurchaseOrder, error) {
	if err := s.authorize(session, "purchase.order.manage"); err != nil {
		return PurchaseOrder{}, err
	}
	if expectedVersion < 1 {
		return PurchaseOrder{}, validation("sipariş sürümü gereklidir")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return PurchaseOrder{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	replayID, replay, err := reserveCommand(ctx, tx, session, meta, "purchasing.purchase_order.confirm", map[string]any{"id": id, "version": expectedVersion})
	if err != nil {
		return PurchaseOrder{}, err
	}
	if replay {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return s.GetPurchaseOrder(ctx, session, replayID)
	}
	if err = s.ensurePurchaseOrderScope(ctx, tx, session, id); err != nil {
		return PurchaseOrder{}, err
	}
	result, err := tx.Exec(ctx, `UPDATE purchase_orders SET status='CONFIRMED',confirmed_at=now(),updated_by=$1,updated_at=now(),version=version+1 WHERE company_id=$2 AND id=$3 AND status='DRAFT' AND version=$4`, session.User.ID, session.CurrentCompanyID, id, expectedVersion)
	if err != nil {
		return PurchaseOrder{}, err
	}
	if result.RowsAffected() != 1 {
		return PurchaseOrder{}, identity.ErrConflict
	}
	result, err = tx.Exec(ctx, `UPDATE documents SET status='POSTED',posted_at=COALESCE(posted_at,now()),posted_by=$1,updated_by=$1,updated_at=now(),version=version+1 WHERE company_id=$2 AND id=$3 AND status='DRAFT'`, session.User.ID, session.CurrentCompanyID, id)
	if err != nil {
		return PurchaseOrder{}, err
	}
	if result.RowsAffected() != 1 {
		return PurchaseOrder{}, identity.ErrConflict
	}
	if err = s.auditEventTx(ctx, tx, session, "PURCHASE_ORDER_CONFIRMED", "purchase.order.confirmed", id, meta, nil); err != nil {
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

// CancelPurchaseOrder cancels a confirmed/partially fulfilled order without
// rewriting its lines. Already received quantities remain historical; the
// cancellation only stops further fulfillment and records the reason on both
// the typed order and its generic identity anchor.
func (s *Service) CancelPurchaseOrder(ctx context.Context, session identity.Session, id string, expectedVersion int64, reason string, meta identity.RequestMeta) (PurchaseOrder, error) {
	if err := s.authorize(session, "purchase.order.manage"); err != nil {
		return PurchaseOrder{}, err
	}
	if expectedVersion < 1 || strings.TrimSpace(reason) == "" {
		return PurchaseOrder{}, validation("sipariş sürümü ve iptal gerekçesi gereklidir")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return PurchaseOrder{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	replayID, replay, err := reserveCommand(ctx, tx, session, meta, "purchasing.purchase_order.cancel", map[string]any{"id": id, "version": expectedVersion, "reason": reason})
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
	if status != "CONFIRMED" && status != "PARTIALLY_FULFILLED" {
		return PurchaseOrder{}, ErrInvalidTransition
	}
	result, err := tx.Exec(ctx, `UPDATE purchase_orders SET status='CANCELLED',cancelled_at=now(),cancellation_reason=$1,updated_by=$2,updated_at=now(),version=version+1 WHERE company_id=$3 AND id=$4 AND status=$5 AND version=$6`, strings.TrimSpace(reason), session.User.ID, session.CurrentCompanyID, id, status, expectedVersion)
	if err != nil {
		return PurchaseOrder{}, err
	}
	if result.RowsAffected() != 1 {
		return PurchaseOrder{}, identity.ErrConflict
	}
	if documentID != "" {
		result, execErr := tx.Exec(ctx, `UPDATE documents SET status='CANCELLED',cancelled_at=now(),cancellation_reason=$1,updated_by=$2,updated_at=now(),version=version+1 WHERE company_id=$3 AND id=$4 AND status='POSTED'`, strings.TrimSpace(reason), session.User.ID, session.CurrentCompanyID, documentID)
		if execErr != nil {
			return PurchaseOrder{}, execErr
		}
		if result.RowsAffected() != 1 {
			return PurchaseOrder{}, identity.ErrConflict
		}
	}
	if err = s.auditEventTx(ctx, tx, session, "PURCHASE_ORDER_CANCELLED", "purchase.order.cancelled", id, meta, map[string]any{"reason": strings.TrimSpace(reason)}); err != nil {
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

// CancelGoodsReceipt compensates the immutable inbound stock movements before
// moving the typed receipt and its identity anchor to CANCELLED.
func (s *Service) CancelGoodsReceipt(ctx context.Context, session identity.Session, id string, expectedVersion int64, reason string, meta identity.RequestMeta) (GoodsReceipt, error) {
	if err := s.cancelPurchasePosting(ctx, session, GoodsReceiptKind, id, expectedVersion, reason, meta); err != nil {
		return GoodsReceipt{}, err
	}
	return s.GetGoodsReceipt(ctx, session, id)
}

// CancelPurchaseInvoice reverses both the supplier ledger posting and any
// direct inbound stock effect. Invoices created from receipts have no second
// stock movement and therefore only their finance posting is reversed.
func (s *Service) CancelPurchaseInvoice(ctx context.Context, session identity.Session, id string, expectedVersion int64, reason string, meta identity.RequestMeta) (PurchaseInvoice, error) {
	if err := s.cancelPurchasePosting(ctx, session, PurchaseInvoiceKind, id, expectedVersion, reason, meta); err != nil {
		return PurchaseInvoice{}, err
	}
	return s.GetPurchaseInvoice(ctx, session, id)
}

// CancelPurchaseReturn reverses the outbound return movement and its supplier
// ledger effect in one transaction.
func (s *Service) CancelPurchaseReturn(ctx context.Context, session identity.Session, id string, expectedVersion int64, reason string, meta identity.RequestMeta) (PurchaseReturn, error) {
	if err := s.cancelPurchasePosting(ctx, session, PurchaseReturnKind, id, expectedVersion, reason, meta); err != nil {
		return PurchaseReturn{}, err
	}
	return s.GetPurchaseReturn(ctx, session, id)
}

func (s *Service) cancelPurchasePosting(ctx context.Context, session identity.Session, kind PurchaseKind, id string, expectedVersion int64, reason string, meta identity.RequestMeta) error {
	permission, ok := purchaseReadPermission(kind)
	if !ok || kind == PurchaseOrderKind {
		return ErrInvalidTransition
	}
	if err := s.authorize(session, permission); err != nil {
		return err
	}
	if expectedVersion < 1 || strings.TrimSpace(reason) == "" {
		return validation("belge sürümü ve iptal gerekçesi gereklidir")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	replayID, replay, err := reserveCommand(ctx, tx, session, meta, "purchasing."+strings.ToLower(string(kind))+".cancel", map[string]any{"id": id, "version": expectedVersion, "reason": reason})
	if err != nil {
		return err
	}
	if replay {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		_ = replayID
		return nil
	}
	var documentID, status, warehouseID string
	var sourceType string
	var table string
	switch kind {
	case GoodsReceiptKind:
		table, sourceType = "goods_receipts", "GOODS_RECEIPT"
	case PurchaseInvoiceKind:
		table, sourceType = "purchase_invoices", "PURCHASE_INVOICE"
	case PurchaseReturnKind:
		table, sourceType = "purchase_returns", "PURCHASE_RETURN"
	}
	if err = tx.QueryRow(ctx, fmt.Sprintf(`SELECT COALESCE(document_id::text,''),status,COALESCE(warehouse_id::text,'') FROM %s WHERE company_id=$1 AND id=$2 AND version=$3 FOR UPDATE`, table), session.CurrentCompanyID, id, expectedVersion).Scan(&documentID, &status, &warehouseID); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if status != "POSTED" {
		return ErrInvalidTransition
	}
	if documentID == "" {
		documentID = id
	}
	// A goods receipt whose lines have already been invoiced cannot be
	// cancelled: rolling its received_quantity back would drop it below the
	// order line's invoiced_quantity. The invoice must be cancelled first.
	if kind == GoodsReceiptKind {
		var invoiced bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM commercial_line_allocations a JOIN commercial_line_registry r ON r.company_id=a.company_id AND r.line_id=a.source_line_id WHERE a.company_id=$1 AND a.allocation_type='INVOICING' AND r.aggregate_type='GOODS_RECEIPT' AND r.document_id=$2)`, session.CurrentCompanyID, id).Scan(&invoiced); err != nil {
			return err
		}
		if invoiced {
			return validation("faturalanmış mal kabul iptal edilemez; önce ilgili alış faturasını iptal edin")
		}
		// A goods receipt an active (POSTED) purchase return still draws from
		// cannot be cancelled either: reversing its +stock would leave the
		// return's -stock unbacked. Resolve the return first.
		var returned bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM commercial_line_allocations a
			JOIN commercial_line_registry r ON r.company_id=a.company_id AND r.line_id=a.source_line_id
			JOIN commercial_line_registry tg ON tg.company_id=a.company_id AND tg.line_id=a.target_line_id
			JOIN purchase_returns pr ON pr.company_id=tg.company_id AND pr.id=tg.document_id
			WHERE a.company_id=$1 AND a.allocation_type='RETURN' AND r.aggregate_type='GOODS_RECEIPT' AND r.document_id=$2 AND pr.status='POSTED')`, session.CurrentCompanyID, id).Scan(&returned); err != nil {
			return err
		}
		if returned {
			return validation("bağlı bir alış iadesi bulunan mal kabul iptal edilemez; önce ilgili iadeyi iptal edin")
		}
	}
	var branchID string
	if err = tx.QueryRow(ctx, fmt.Sprintf(`SELECT branch_id FROM %s WHERE company_id=$1 AND id=$2`, table), session.CurrentCompanyID, id).Scan(&branchID); err != nil {
		return err
	}
	if err = s.ensureBranch(ctx, session, branchID); err != nil {
		return err
	}
	if kind == PurchaseInvoiceKind {
		// Drain the line warehouses fully before calling ensureScope: that helper
		// queries the request-pinned connection, which is the same connection this
		// Rows is open on, so scoping inside the loop trips a "conn busy" error.
		rows, queryErr := tx.Query(ctx, `SELECT DISTINCT warehouse_id::text FROM purchase_invoice_lines WHERE company_id=$1 AND invoice_id=$2 AND warehouse_id IS NOT NULL`, session.CurrentCompanyID, id)
		if queryErr != nil {
			return queryErr
		}
		lineWarehouses := make([]string, 0)
		for rows.Next() {
			var lineWarehouse string
			if scanErr := rows.Scan(&lineWarehouse); scanErr != nil {
				rows.Close()
				return scanErr
			}
			lineWarehouses = append(lineWarehouses, lineWarehouse)
		}
		if rowsErr := rows.Err(); rowsErr != nil {
			rows.Close()
			return rowsErr
		}
		rows.Close()
		for _, lineWarehouse := range lineWarehouses {
			if scopeErr := s.ensureScope(ctx, session, branchID, lineWarehouse); scopeErr != nil {
				return scopeErr
			}
		}
	} else if err = s.ensureScope(ctx, session, branchID, warehouseID); err != nil {
		return err
	}
	stockEffect := kind != PurchaseInvoiceKind
	if kind == PurchaseInvoiceKind {
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM purchase_invoice_lines WHERE company_id=$1 AND invoice_id=$2 AND line_type='PRODUCT' AND goods_receipt_line_id IS NULL AND purchase_order_line_id IS NULL)`, session.CurrentCompanyID, id).Scan(&stockEffect); err != nil {
			return err
		}
	}
	if stockEffect {
		if reverser, ok := s.stockPoster.(PurchaseStockReverser); ok {
			if err = reverser.ReversePurchaseMovementsTx(ctx, tx, session, inventory.PurchaseStockReversalInput{DocumentID: documentID, SourceType: sourceType, WarehouseID: warehouseID, ReversalKey: meta.IdempotencyKey, Reason: strings.TrimSpace(reason)}); err != nil {
				return err
			}
		} else {
			return validation("satın alma belgesi stok ters kayıt servisi hazır değil")
		}
	}
	if kind == PurchaseInvoiceKind || kind == PurchaseReturnKind {
		reverser, ok := s.financePost.(FinanceReverser)
		if !ok {
			return validation("satın alma belgesi finans ters kayıt servisi hazır değil")
		}
		if kind == PurchaseInvoiceKind {
			if _, err = reverser.ReversePurchaseInvoiceTx(ctx, tx, session, documentID, meta.IdempotencyKey, strings.TrimSpace(reason)); err != nil {
				return err
			}
		} else if _, err = reverser.ReversePurchaseReturnTx(ctx, tx, session, documentID, meta.IdempotencyKey, strings.TrimSpace(reason)); err != nil {
			return err
		}
	}
	if err = s.rollbackPurchaseProjectionsTx(ctx, tx, session.CurrentCompanyID, kind, id); err != nil {
		return err
	}
	result, err := tx.Exec(ctx, fmt.Sprintf(`UPDATE %s SET status='CANCELLED',cancelled_at=now(),cancellation_reason=$1,version=version+1 WHERE company_id=$2 AND id=$3 AND status='POSTED' AND version=$4`, table), strings.TrimSpace(reason), session.CurrentCompanyID, id, expectedVersion)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return identity.ErrConflict
	}
	result, err = tx.Exec(ctx, `UPDATE documents SET status='CANCELLED',cancelled_at=now(),cancellation_reason=$1,updated_by=$2,updated_at=now(),version=version+1 WHERE company_id=$3 AND id=$4 AND status='POSTED'`, strings.TrimSpace(reason), session.User.ID, session.CurrentCompanyID, documentID)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return identity.ErrConflict
	}
	eventName := map[PurchaseKind]string{GoodsReceiptKind: "PURCHASE_RECEIPT", PurchaseInvoiceKind: "PURCHASE_INVOICE", PurchaseReturnKind: "PURCHASE_RETURN"}[kind]
	if err = s.auditEventTx(ctx, tx, session, eventName+"_CANCELLED", "purchase."+strings.ToLower(string(kind))+".cancelled", id, meta, map[string]any{"reason": strings.TrimSpace(reason)}); err != nil {
		return err
	}
	if err = completeCommand(ctx, tx, session, meta, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) CreateGoodsReceipt(ctx context.Context, session identity.Session, input GoodsReceiptInput, meta identity.RequestMeta) (GoodsReceipt, error) {
	if err := s.authorizeAny(session, "purchase.receipt.draft", "purchase.receipt.post"); err != nil {
		return GoodsReceipt{}, err
	}
	if input.ReceiptDate.IsZero() {
		input.ReceiptDate = time.Now().UTC().Truncate(24 * time.Hour)
	}
	if input.SupplierID == "" || input.BranchID == "" || input.WarehouseID == "" {
		return GoodsReceipt{}, validation("mal kabul için tedarikçi, şube ve depo gereklidir")
	}
	for index := range input.Lines {
		if err := validateReceiptLine(&input.Lines[index]); err != nil {
			return GoodsReceipt{}, err
		}
	}
	if err := validateGoodsReceiptSourceShape(input.PurchaseOrderID, input.Lines); err != nil {
		return GoodsReceipt{}, err
	}
	if err := s.ensureScope(ctx, session, input.BranchID, input.WarehouseID); err != nil {
		return GoodsReceipt{}, err
	}
	if err := s.ensureSupplier(ctx, session.CurrentCompanyID, input.SupplierID); err != nil {
		return GoodsReceipt{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return GoodsReceipt{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	replayID, replay, err := reserveCommand(ctx, tx, session, meta, "purchasing.goods_receipt.create", input)
	if err != nil {
		return GoodsReceipt{}, err
	}
	if replay {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return s.GetGoodsReceipt(ctx, session, replayID)
	}
	receiptID := uuid.NewString()
	receiptNo, err := s.number(ctx, tx, session.CurrentCompanyID, "PURCHASE_DELIVERY", "GR", input.ReceiptNo, input.ReceiptDate.Year())
	if err != nil {
		return GoodsReceipt{}, err
	}
	var orderID *string
	warning := false
	if strings.TrimSpace(input.PurchaseOrderID) != "" {
		var orderSupplier, orderBranch string
		var policy string
		var orderStatus string
		if err = tx.QueryRow(ctx, `SELECT supplier_id,branch_id,over_delivery_policy,status FROM purchase_orders WHERE company_id=$1 AND id=$2 FOR UPDATE`, session.CurrentCompanyID, input.PurchaseOrderID).Scan(&orderSupplier, &orderBranch, &policy, &orderStatus); errors.Is(err, pgx.ErrNoRows) {
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
		orderID = &input.PurchaseOrderID
		for index := range input.Lines {
			line := &input.Lines[index]
			if line.PurchaseOrderLineID == nil || *line.PurchaseOrderLineID == "" {
				return GoodsReceipt{}, validation("siparişli mal kabul satırları sipariş satırına bağlanmalıdır")
			}
			var ordered, received, orderWarehouse, orderLineType, orderProductID, orderVariantID string
			if err = tx.QueryRow(ctx, `SELECT product_id,COALESCE(variant_id::text,''),ordered_quantity::text,received_quantity::text,COALESCE(warehouse_id::text,''),line_type FROM purchase_order_lines WHERE company_id=$1 AND id=$2 AND order_id=$3 FOR UPDATE`, session.CurrentCompanyID, *line.PurchaseOrderLineID, input.PurchaseOrderID).Scan(&orderProductID, &orderVariantID, &ordered, &received, &orderWarehouse, &orderLineType); err != nil {
				return GoodsReceipt{}, ErrNotFound
			}
			if normalizePurchaseLineType(orderLineType) != "PRODUCT" {
				return GoodsReceipt{}, validation("hizmet satırı alış irsaliyesine dönüştürülemez")
			}
			if orderProductID != line.ProductID || orderVariantID != strings.TrimSpace(line.VariantID) {
				return GoodsReceipt{}, validation("mal kabul satırı sipariş satırıyla eşleşmiyor")
			}
			if strings.TrimSpace(line.WarehouseID) == "" {
				line.WarehouseID = orderWarehouse
			} else if line.WarehouseID != orderWarehouse {
				return GoodsReceipt{}, validation("mal kabul satır deposu sipariş satırıyla eşleşmelidir")
			}
			if compare(add(received, zero(line.AcceptedQuantity)), ordered) > 0 {
				if policy == "BLOCK" {
					return GoodsReceipt{}, ErrOverDelivery
				}
				warning = true
			}
		}
	}
	receiptCurrency := strings.ToUpper(strings.TrimSpace(input.Currency))
	if receiptCurrency != "" && !validCurrency(receiptCurrency) {
		return GoodsReceipt{}, validation("mal kabul para birimi geçersiz")
	}
	for _, candidate := range input.Lines {
		value := strings.ToUpper(strings.TrimSpace(candidate.Currency))
		if !validCurrency(value) {
			return GoodsReceipt{}, validation("mal kabul satır para birimi geçersiz")
		}
		if receiptCurrency != "" && value != receiptCurrency {
			return GoodsReceipt{}, validation("mal kabul satırlarının para birimi belgeyle eşleşmelidir")
		}
		if receiptCurrency == "" {
			receiptCurrency = value
		}
	}
	if receiptCurrency == "" {
		receiptCurrency = "TRY"
	}
	input.Currency = receiptCurrency
	if err = s.ensureExchangeRate(ctx, session, input.Currency, input.ReceiptDate, &input.ExchangeRate); err != nil {
		return GoodsReceipt{}, err
	}
	if err = insertPurchaseDocumentAnchorTx(ctx, tx, session, receiptID, "PURCHASE_DELIVERY", receiptNo, input.BranchID, input.WarehouseID, input.SupplierID, input.ReceiptDate, nil, receiptCurrency, input.ExchangeRate, "Alış irsaliyesi", "0", "0", "0", "0"); err != nil {
		return GoodsReceipt{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO goods_receipts(id,company_id,document_id,receipt_no,purchase_order_id,supplier_id,branch_id,warehouse_id,receipt_date,currency,status,over_delivery_warning,notes,created_by,posted_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'DRAFT',$11,$12,$13,NULL)`, receiptID, session.CurrentCompanyID, receiptID, receiptNo, orderID, input.SupplierID, input.BranchID, input.WarehouseID, input.ReceiptDate, receiptCurrency, warning, strings.TrimSpace(input.Notes), session.User.ID); err != nil {
		return GoodsReceipt{}, err
	}
	for index := range input.Lines {
		line := &input.Lines[index]
		if err = ensurePurchaseProduct(ctx, tx, session.CurrentCompanyID, line.ProductID, line.VariantID, "PRODUCT"); err != nil {
			return GoodsReceipt{}, err
		}
		// Only the accepted quantity enters the system: it is what advances the
		// order's receiving projection and what can later be invoiced or
		// returned. Damaged/rejected units are recorded on the line for the
		// record but never become stock, payable or returnable.
		accepted := zero(line.AcceptedQuantity)
		line.BaseQuantity, line.ConversionFactor, err = resolvePurchaseConversionTx(ctx, tx, session.CurrentCompanyID, line.ProductID, line.UnitCode, accepted, line.BaseQuantity, line.ConversionFactor)
		if err != nil {
			return GoodsReceipt{}, err
		}
		line.ID, line.LineNo = uuid.NewString(), index+1
		line.WarehouseID = strings.TrimSpace(line.WarehouseID)
		if line.WarehouseID == "" {
			line.WarehouseID = input.WarehouseID
		}
		if err = s.ensureScope(ctx, session, input.BranchID, line.WarehouseID); err != nil {
			return GoodsReceipt{}, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO goods_receipt_lines(id,company_id,receipt_id,purchase_order_line_id,line_no,product_id,variant_id,warehouse_id,accepted_quantity,damaged_quantity,rejected_quantity,unit_code,base_quantity,conversion_factor,unit_cost,currency,lot_snapshot,serial_snapshot,tax_snapshot) VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,'')::uuid,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`, line.ID, session.CurrentCompanyID, receiptID, line.PurchaseOrderLineID, line.LineNo, line.ProductID, line.VariantID, line.WarehouseID, zero(line.AcceptedQuantity), zero(line.DamagedQuantity), zero(line.RejectedQuantity), line.UnitCode, line.BaseQuantity, line.ConversionFactor, line.UnitCost, strings.ToUpper(line.Currency), jsonArray(line.LotSnapshot), jsonArray(line.SerialSnapshot), jsonObject(line.TaxSnapshot)); err != nil {
			return GoodsReceipt{}, err
		}
		if compare(accepted, "0") > 0 {
			if err = registerPurchaseLineTx(ctx, tx, session.CurrentCompanyID, "GOODS_RECEIPT", receiptID, line.ID, line.LineNo, "PRODUCT", accepted, line.BaseQuantity); err != nil {
				return GoodsReceipt{}, err
			}
		}
	}
	if err = s.auditEventTx(ctx, tx, session, "PURCHASE_RECEIPT_CREATED", "purchase.receipt.created", receiptID, meta, map[string]any{"receipt_no": receiptNo, "over_delivery_warning": warning}); err != nil {
		return GoodsReceipt{}, err
	}
	if err = completeCommand(ctx, tx, session, meta, receiptID); err != nil {
		return GoodsReceipt{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return GoodsReceipt{}, err
	}
	return s.GetGoodsReceipt(ctx, session, receiptID)
}

func (s *Service) CreatePurchaseInvoice(ctx context.Context, session identity.Session, input PurchaseInvoiceInput, meta identity.RequestMeta) (PurchaseInvoice, error) {
	if err := s.authorizeAny(session, "purchase.invoice.draft", "purchase.invoice.post"); err != nil {
		return PurchaseInvoice{}, err
	}
	if input.Standalone && !session.HasPermission("purchase.invoice.standalone") {
		return PurchaseInvoice{}, identity.ErrForbidden
	}
	receiptIDs := purchaseReceiptIDs(input)
	if input.Standalone && (input.PurchaseOrderID != "" || len(receiptIDs) > 0) {
		return PurchaseInvoice{}, validation("standalone alış faturası kaynak belge içeremez")
	}
	if !input.Standalone && len(receiptIDs) == 0 && input.PurchaseOrderID == "" {
		return PurchaseInvoice{}, validation("alış faturası siparişe veya mal kabule bağlanmalıdır")
	}
	if len(receiptIDs) > 0 {
		input.GoodsReceiptID = receiptIDs[0]
		input.GoodsReceiptIDs = receiptIDs
	}
	if input.InvoiceDate.IsZero() {
		input.InvoiceDate = time.Now().UTC().Truncate(24 * time.Hour)
	}
	input.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))
	if !validCurrency(input.Currency) {
		return PurchaseInvoice{}, validation("alış faturası para birimi geçerli olmalıdır")
	}
	if err := s.ensureExchangeRate(ctx, session, input.Currency, input.InvoiceDate, &input.ExchangeRate); err != nil {
		return PurchaseInvoice{}, err
	}
	if err := s.ensureBranch(ctx, session, input.BranchID); err != nil {
		return PurchaseInvoice{}, err
	}
	if err := s.ensureSupplier(ctx, session.CurrentCompanyID, input.SupplierID); err != nil {
		return PurchaseInvoice{}, err
	}
	if err := s.deriveSupplierDueDate(ctx, session.CurrentCompanyID, input.SupplierID, input.InvoiceDate, &input.DueDate); err != nil {
		return PurchaseInvoice{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return PurchaseInvoice{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	replayID, replay, err := reserveCommand(ctx, tx, session, meta, "purchasing.purchase_invoice.create", input)
	if err != nil {
		return PurchaseInvoice{}, err
	}
	if replay {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return s.GetPurchaseInvoice(ctx, session, replayID)
	}
	invoiceID := uuid.NewString()
	invoiceNo, err := s.number(ctx, tx, session.CurrentCompanyID, "PURCHASE_INVOICE", "PI", input.InvoiceNo, input.InvoiceDate.Year())
	if err != nil {
		return PurchaseInvoice{}, err
	}
	var subtotal, discount, tax, payable string
	hasLineSources := false
	for _, line := range input.Lines {
		if strings.TrimSpace(line.PurchaseOrderLineID) != "" || strings.TrimSpace(line.GoodsReceiptLineID) != "" {
			hasLineSources = true
			break
		}
	}
	if input.PurchaseOrderID != "" || len(receiptIDs) > 0 || hasLineSources {
		if input.PurchaseOrderID != "" {
			if err = s.ensurePurchaseOrderScope(ctx, tx, session, input.PurchaseOrderID); err != nil {
				return PurchaseInvoice{}, err
			}
		}
		for _, receiptID := range receiptIDs {
			if err = s.ensurePurchaseReceiptScope(ctx, tx, session, receiptID); err != nil {
				return PurchaseInvoice{}, err
			}
		}
		if err = s.lockInvoiceAllocationSourceTx(ctx, tx, session.CurrentCompanyID, &input); err != nil {
			return PurchaseInvoice{}, err
		}
	}
	defaultWarehouse := strings.TrimSpace(input.WarehouseID)
	headerWarehouse := defaultWarehouse
	for index := range input.Lines {
		line := &input.Lines[index]
		if input.Standalone && (line.PurchaseOrderLineID != "" || line.GoodsReceiptLineID != "") {
			return PurchaseInvoice{}, validation("standalone alış faturası kaynak belge satırı içeremez")
		}
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
		line.ID, line.LineNo = uuid.NewString(), index+1
		subtotal = add(subtotal, line.GrossAmount)
		discount = add(discount, line.DiscountAmount)
		tax = add(tax, line.TaxAmount)
		payable = add(payable, line.PayableAmount)
		line.WarehouseID = strings.TrimSpace(line.WarehouseID)
		if line.LineType == "SERVICE" {
			if line.WarehouseID != "" {
				return PurchaseInvoice{}, validation("alış faturası hizmet satırında depo bulunamaz")
			}
		} else {
			if line.WarehouseID == "" {
				line.WarehouseID = defaultWarehouse
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
				// A multi-warehouse invoice has no single default warehouse. Keep the
				// line snapshots authoritative and leave the header anchor nullable.
				headerWarehouse = ""
			}
		}
	}
	if err = insertPurchaseDocumentAnchorTx(ctx, tx, session, invoiceID, "PURCHASE_INVOICE", invoiceNo, input.BranchID, headerWarehouse, input.SupplierID, input.InvoiceDate, input.DueDate, input.Currency, input.ExchangeRate, "Alış faturası", subtotal, discount, tax, payable); err != nil {
		return PurchaseInvoice{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO purchase_invoices(id,company_id,invoice_no,supplier_id,branch_id,warehouse_id,document_id,purchase_order_id,goods_receipt_id,invoice_date,due_date,currency,status,subtotal,discount_total,tax_total,payable_total,finance_posting_id,created_by,posted_at) VALUES($1,$2,$3,$4,$5,NULLIF($6,'')::uuid,$7,NULLIF($8,'')::uuid,NULLIF($9,'')::uuid,$10,$11,$12,'DRAFT',$13,$14,$15,$16,NULL,$17,NULL)`, invoiceID, session.CurrentCompanyID, invoiceNo, input.SupplierID, input.BranchID, headerWarehouse, invoiceID, input.PurchaseOrderID, input.GoodsReceiptID, input.InvoiceDate, input.DueDate, input.Currency, subtotal, discount, tax, payable, session.User.ID); err != nil {
		return PurchaseInvoice{}, err
	}
	for _, line := range input.Lines {
		if _, err = tx.Exec(ctx, `INSERT INTO purchase_invoice_lines(id,company_id,invoice_id,purchase_order_line_id,goods_receipt_line_id,line_no,line_type,product_id,variant_id,warehouse_id,unit_code,base_quantity,conversion_factor,description_snapshot,quantity,unit_price,gross_amount,discount_amount,tax_base,tax_amount,withholding_amount,payable_amount,tax_components_snapshot) VALUES($1,$2,$3,NULLIF($4,'')::uuid,NULLIF($5,'')::uuid,$6,$7,$8,NULLIF($9,'')::uuid,NULLIF($10,'')::uuid,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23)`, line.ID, session.CurrentCompanyID, invoiceID, line.PurchaseOrderLineID, line.GoodsReceiptLineID, line.LineNo, line.LineType, line.ProductID, line.VariantID, line.WarehouseID, line.UnitCode, line.BaseQuantity, line.ConversionFactor, line.DescriptionSnapshot, line.Quantity, line.UnitPrice, line.GrossAmount, line.DiscountAmount, line.TaxBase, line.TaxAmount, line.WithholdingAmount, line.PayableAmount, jsonArray(line.TaxComponentsSnapshot)); err != nil {
			return PurchaseInvoice{}, err
		}
		if err = registerPurchaseLineTx(ctx, tx, session.CurrentCompanyID, "PURCHASE_INVOICE", invoiceID, line.ID, line.LineNo, line.LineType, line.Quantity, line.BaseQuantity); err != nil {
			return PurchaseInvoice{}, err
		}
	}
	for _, sourceID := range append([]string{input.PurchaseOrderID}, receiptIDs...) {
		if strings.TrimSpace(sourceID) == "" {
			continue
		}
		if _, err = tx.Exec(ctx, `INSERT INTO commercial_document_sources(company_id,document_id,source_document_id,relation_type) VALUES($1,$2,$3,'INVOICING') ON CONFLICT DO NOTHING`, session.CurrentCompanyID, invoiceID, sourceID); err != nil {
			return PurchaseInvoice{}, err
		}
	}
	if err = s.auditEventTx(ctx, tx, session, "PURCHASE_INVOICE_CREATED", "purchase.invoice.created", invoiceID, meta, map[string]any{"invoice_no": invoiceNo}); err != nil {
		return PurchaseInvoice{}, err
	}
	if err = completeCommand(ctx, tx, session, meta, invoiceID); err != nil {
		return PurchaseInvoice{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return PurchaseInvoice{}, err
	}
	return s.GetPurchaseInvoice(ctx, session, invoiceID)
}

func (s *Service) CreatePurchaseReturn(ctx context.Context, session identity.Session, input PurchaseReturnInput, meta identity.RequestMeta) (PurchaseReturn, error) {
	if err := s.authorizeAny(session, "purchase.return.draft", "purchase.return.post"); err != nil {
		return PurchaseReturn{}, err
	}
	if input.ReturnDate.IsZero() {
		input.ReturnDate = time.Now().UTC().Truncate(24 * time.Hour)
	}
	input.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))
	if input.Reason == "" || !validCurrency(input.Currency) {
		return PurchaseReturn{}, validation("satın alma iadesi gerekçe ve para birimi gerektirir")
	}
	if len(input.Lines) > 0 && strings.TrimSpace(input.SourceReceiptID) == "" {
		return PurchaseReturn{}, validation("satın alma iadesi kaynak mal kabul belgesine bağlanmalıdır")
	}
	if err := s.ensureExchangeRate(ctx, session, input.Currency, input.ReturnDate, &input.ExchangeRate); err != nil {
		return PurchaseReturn{}, err
	}
	for index := range input.Lines {
		if err := validateReturnLine(&input.Lines[index], input.Currency); err != nil {
			return PurchaseReturn{}, err
		}
	}
	if err := validatePurchaseReturnSourceShape(input.SourceReceiptID, input.Lines); err != nil {
		return PurchaseReturn{}, err
	}
	if err := s.ensureScope(ctx, session, input.BranchID, input.WarehouseID); err != nil {
		return PurchaseReturn{}, err
	}
	if err := s.ensureSupplier(ctx, session.CurrentCompanyID, input.SupplierID); err != nil {
		return PurchaseReturn{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return PurchaseReturn{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	replayID, replay, err := reserveCommand(ctx, tx, session, meta, "purchasing.purchase_return.create", input)
	if err != nil {
		return PurchaseReturn{}, err
	}
	if replay {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return s.GetPurchaseReturn(ctx, session, replayID)
	}
	returnID := uuid.NewString()
	returnNo, err := s.number(ctx, tx, session.CurrentCompanyID, "PURCHASE_RETURN_INVOICE", "PR", input.ReturnNo, input.ReturnDate.Year())
	if err != nil {
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
	if err = insertPurchaseDocumentAnchorTx(ctx, tx, session, returnID, "PURCHASE_RETURN_INVOICE", returnNo, input.BranchID, input.WarehouseID, input.SupplierID, input.ReturnDate, nil, input.Currency, input.ExchangeRate, "Satın alma iadesi", total, "0", "0", total); err != nil {
		return PurchaseReturn{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO purchase_returns(id,company_id,return_no,supplier_id,branch_id,warehouse_id,document_id,source_receipt_id,return_date,currency,status,total,reason,finance_posting_id,created_by,posted_at) VALUES($1,$2,$3,$4,$5,$6,$7,NULLIF($8,'')::uuid,$9,$10,'DRAFT',$11,$12,NULL,$13,NULL)`, returnID, session.CurrentCompanyID, returnNo, input.SupplierID, input.BranchID, input.WarehouseID, returnID, input.SourceReceiptID, input.ReturnDate, input.Currency, total, input.Reason, session.User.ID); err != nil {
		return PurchaseReturn{}, err
	}
	for _, line := range input.Lines {
		if _, err = tx.Exec(ctx, `INSERT INTO purchase_return_lines(id,company_id,return_id,source_receipt_line_id,line_no,product_id,variant_id,warehouse_id,quantity,base_quantity,conversion_factor,unit_code,unit_cost,currency,reason) VALUES($1,$2,$3,NULLIF($4,'')::uuid,$5,$6,NULLIF($7,'')::uuid,$8,$9,$10,$11,$12,$13,$14,$15)`, line.ID, session.CurrentCompanyID, returnID, line.SourceReceiptLineID, line.LineNo, line.ProductID, line.VariantID, line.WarehouseID, line.Quantity, line.BaseQuantity, line.ConversionFactor, line.UnitCode, line.UnitCost, input.Currency, line.Reason); err != nil {
			return PurchaseReturn{}, err
		}
		if err = registerPurchaseLineTx(ctx, tx, session.CurrentCompanyID, "PURCHASE_RETURN", returnID, line.ID, line.LineNo, "PRODUCT", line.Quantity, line.BaseQuantity); err != nil {
			return PurchaseReturn{}, err
		}
	}
	if input.SourceReceiptID != "" {
		if _, err = tx.Exec(ctx, `INSERT INTO commercial_document_sources(company_id,document_id,source_document_id,relation_type) VALUES($1,$2,$3,'RETURN') ON CONFLICT DO NOTHING`, session.CurrentCompanyID, returnID, input.SourceReceiptID); err != nil {
			return PurchaseReturn{}, err
		}
	}
	if err = s.auditEventTx(ctx, tx, session, "PURCHASE_RETURN_CREATED", "purchase.return.created", returnID, meta, map[string]any{"return_no": returnNo}); err != nil {
		return PurchaseReturn{}, err
	}
	if err = completeCommand(ctx, tx, session, meta, returnID); err != nil {
		return PurchaseReturn{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return PurchaseReturn{}, err
	}
	return s.GetPurchaseReturn(ctx, session, returnID)
}

// FinalizeGoodsReceipt is the only entry point that may create inbound stock
// or advance purchase-order receiving quantities. Creating a receipt stores a
// draft and its line snapshots; all source allocations, projections and stock
// effects happen atomically here.
func (s *Service) FinalizeGoodsReceipt(ctx context.Context, session identity.Session, id string, expectedVersion int64, meta identity.RequestMeta) (GoodsReceipt, error) {
	if err := s.authorize(session, "purchase.receipt.post"); err != nil {
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
	replayID, replay, err := reserveCommand(ctx, tx, session, meta, "purchasing.goods_receipt.finalize", map[string]any{"id": id, "version": expectedVersion})
	if err != nil {
		return GoodsReceipt{}, err
	}
	if replay {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return s.GetGoodsReceipt(ctx, session, replayID)
	}
	var receiptNo, supplierID, branchID, warehouseID, currency, status string
	var receiptDate time.Time
	var orderID *string
	var warning bool
	if err = tx.QueryRow(ctx, `SELECT receipt_no,supplier_id,branch_id,warehouse_id,receipt_date,currency,status,purchase_order_id,over_delivery_warning FROM goods_receipts WHERE company_id=$1 AND id=$2 AND version=$3 FOR UPDATE`, session.CurrentCompanyID, id, expectedVersion).Scan(&receiptNo, &supplierID, &branchID, &warehouseID, &receiptDate, &currency, &status, &orderID, &warning); errors.Is(err, pgx.ErrNoRows) {
		return GoodsReceipt{}, identity.ErrConflict
	} else if err != nil {
		return GoodsReceipt{}, err
	}
	if status != "DRAFT" {
		return GoodsReceipt{}, ErrInvalidTransition
	}
	if err = s.ensureBranch(ctx, session, branchID); err != nil {
		return GoodsReceipt{}, err
	}
	if err = s.ensureSupplier(ctx, session.CurrentCompanyID, supplierID); err != nil {
		return GoodsReceipt{}, err
	}
	var orderSupplier, orderBranch, orderStatus, policy string
	if orderID != nil {
		if err = tx.QueryRow(ctx, `SELECT supplier_id,branch_id,status,over_delivery_policy FROM purchase_orders WHERE company_id=$1 AND id=$2 FOR UPDATE`, session.CurrentCompanyID, *orderID).Scan(&orderSupplier, &orderBranch, &orderStatus, &policy); errors.Is(err, pgx.ErrNoRows) {
			return GoodsReceipt{}, ErrNotFound
		} else if err != nil {
			return GoodsReceipt{}, err
		}
		if err = s.ensurePurchaseOrderScope(ctx, tx, session, *orderID); err != nil {
			return GoodsReceipt{}, err
		}
		if orderSupplier != supplierID || orderBranch != branchID || (orderStatus != "CONFIRMED" && orderStatus != "PARTIALLY_FULFILLED") {
			return GoodsReceipt{}, validation("mal kabul siparişi tedarikçi veya durum açısından geçersiz")
		}
	}
	rows, err := tx.Query(ctx, `SELECT id,purchase_order_line_id,product_id,COALESCE(variant_id::text,''),warehouse_id,accepted_quantity::text,damaged_quantity::text,rejected_quantity::text,unit_code,base_quantity::text,conversion_factor::text,unit_cost::text,currency,lot_snapshot,serial_snapshot FROM goods_receipt_lines WHERE company_id=$1 AND receipt_id=$2 ORDER BY line_no`, session.CurrentCompanyID, id)
	if err != nil {
		return GoodsReceipt{}, err
	}
	stockLinesByWarehouse := make(map[string][]inventory.PurchaseStockLine)
	var lines []GoodsReceiptLine
	// Drain the line rows before issuing any further query on this transaction:
	// pgx binds a transaction to one connection, so an open Rows plus a nested
	// tx.QueryRow/Exec on the same tx fails with "conn busy".
	for rows.Next() {
		var line GoodsReceiptLine
		var lot, serial []byte
		if err = rows.Scan(&line.ID, &line.PurchaseOrderLineID, &line.ProductID, &line.VariantID, &line.WarehouseID, &line.AcceptedQuantity, &line.DamagedQuantity, &line.RejectedQuantity, &line.UnitCode, &line.BaseQuantity, &line.ConversionFactor, &line.UnitCost, &line.Currency, &lot, &serial); err != nil {
			rows.Close()
			return GoodsReceipt{}, err
		}
		line.LotSnapshot, line.SerialSnapshot = nil, nil
		_ = json.Unmarshal(lot, &line.LotSnapshot)
		_ = json.Unmarshal(serial, &line.SerialSnapshot)
		lines = append(lines, line)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return GoodsReceipt{}, err
	}
	if len(lines) == 0 {
		return GoodsReceipt{}, ErrDocumentHasNoLines
	}
	persistedLines := lines
	lines = lines[:0]
	for lineIndex := range persistedLines {
		line := persistedLines[lineIndex]
		// Only the accepted quantity advances the order projection, the
		// fulfillment allocation and (below) the stock movement. line.BaseQuantity
		// is already the accepted base quantity (set at draft time).
		accepted := zero(line.AcceptedQuantity)
		if orderID != nil {
			if line.PurchaseOrderLineID == nil || strings.TrimSpace(*line.PurchaseOrderLineID) == "" {
				return GoodsReceipt{}, validation("siparişli mal kabul satırları sipariş satırına bağlanmalıdır")
			}
			var ordered, received, orderWarehouse, orderLineType, orderProductID, orderVariantID string
			if err = tx.QueryRow(ctx, `SELECT product_id,COALESCE(variant_id::text,''),ordered_quantity::text,received_quantity::text,COALESCE(warehouse_id::text,''),line_type FROM purchase_order_lines WHERE company_id=$1 AND id=$2 AND order_id=$3 FOR UPDATE`, session.CurrentCompanyID, *line.PurchaseOrderLineID, *orderID).Scan(&orderProductID, &orderVariantID, &ordered, &received, &orderWarehouse, &orderLineType); errors.Is(err, pgx.ErrNoRows) {
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
		} else if line.PurchaseOrderLineID != nil && strings.TrimSpace(*line.PurchaseOrderLineID) != "" {
			return GoodsReceipt{}, validation("sipariş satırı bağlantısı için mal kabul başlığında kaynak sipariş seçilmelidir")
		}
		if orderID != nil && compare(accepted, "0") > 0 {
			if err = allocatePurchaseLinesTx(ctx, tx, session.CurrentCompanyID, *line.PurchaseOrderLineID, line.ID, "FULFILLMENT", accepted, line.BaseQuantity); err != nil {
				return GoodsReceipt{}, err
			}
		}
		if compare(accepted, "0") > 0 {
			stockLinesByWarehouse[line.WarehouseID] = append(stockLinesByWarehouse[line.WarehouseID], inventory.PurchaseStockLine{LineID: line.ID, ProductID: line.ProductID, VariantID: line.VariantID, Quantity: accepted, BaseQuantity: line.BaseQuantity, ConversionFactor: line.ConversionFactor, UnitCode: line.UnitCode, UnitCost: line.UnitCost, Currency: strings.ToUpper(line.Currency), LotNumber: lotNumber(line.LotSnapshot), SerialNumber: serialNumber(line.SerialSnapshot)})
		}
		if orderID != nil && compare(accepted, "0") > 0 {
			if _, err = tx.Exec(ctx, `UPDATE purchase_order_lines SET received_quantity=received_quantity+$1 WHERE company_id=$2 AND id=$3`, accepted, session.CurrentCompanyID, *line.PurchaseOrderLineID); err != nil {
				return GoodsReceipt{}, err
			}
		}
		lines = append(lines, line)
	}
	postingLines := make([]purchasePostingLine, 0, len(lines))
	for _, line := range lines {
		postingLines = append(postingLines, purchasePostingLine{LineNo: line.LineNo, LineType: "PRODUCT", ProductID: line.ProductID, VariantID: line.VariantID, WarehouseID: line.WarehouseID})
	}
	if err = validatePurchasePostingMastersTx(ctx, tx, session, branchID, supplierID, warehouseID, postingLines); err != nil {
		return GoodsReceipt{}, err
	}
	warehouseIDs := make([]string, 0, len(stockLinesByWarehouse))
	for warehouseID := range stockLinesByWarehouse {
		warehouseIDs = append(warehouseIDs, warehouseID)
	}
	sort.Strings(warehouseIDs)
	for _, lineWarehouseID := range warehouseIDs {
		if s.stockPoster == nil {
			return GoodsReceipt{}, validation("mal kabul için stok posting servisi hazır değil")
		}
		if err = s.stockPoster.PostPurchaseReceiptMovementsTx(ctx, tx, session, inventory.PurchaseReceiptStockPostingInput{ReceiptID: id, WarehouseID: lineWarehouseID, Lines: stockLinesByWarehouse[lineWarehouseID]}); err != nil {
			return GoodsReceipt{}, err
		}
	}
	if orderID != nil {
		if err = s.refreshOrderStatusTx(ctx, tx, session.CurrentCompanyID, *orderID); err != nil {
			return GoodsReceipt{}, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO commercial_document_sources(company_id,document_id,source_document_id,relation_type) VALUES($1,$2,$3,'FULFILLMENT') ON CONFLICT DO NOTHING`, session.CurrentCompanyID, id, *orderID); err != nil {
			return GoodsReceipt{}, err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE goods_receipts SET status='POSTED',posted_at=now(),over_delivery_warning=$1,version=version+1 WHERE company_id=$2 AND id=$3 AND status='DRAFT' AND version=$4`, warning, session.CurrentCompanyID, id, expectedVersion); err != nil {
		return GoodsReceipt{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE documents SET status='POSTED',posted_at=COALESCE(posted_at,now()),posted_by=$1,post_idempotency_key=$2,updated_by=$1,updated_at=now(),version=version+1 WHERE company_id=$3 AND id=$4 AND status='DRAFT'`, session.User.ID, meta.IdempotencyKey, session.CurrentCompanyID, id); err != nil {
		return GoodsReceipt{}, err
	}
	if err = s.auditEventTx(ctx, tx, session, "PURCHASE_RECEIPT_FINALIZED", "purchase.receipt.finalized", id, meta, map[string]any{"receipt_no": receiptNo, "line_count": len(lines), "over_delivery_warning": warning, "receipt_date": receiptDate}); err != nil {
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

// FinalizePurchaseInvoice applies the invoice's finance effect and, only for
// direct product lines, its inbound stock effect. Receipt-linked lines only
// allocate/invoice the already-posted receipt and therefore never add stock a
// second time.
func (s *Service) FinalizePurchaseInvoice(ctx context.Context, session identity.Session, id string, expectedVersion int64, meta identity.RequestMeta) (PurchaseInvoice, error) {
	if err := s.authorize(session, "purchase.invoice.post"); err != nil {
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
	replayID, replay, err := reserveCommand(ctx, tx, session, meta, "purchasing.purchase_invoice.finalize", map[string]any{"id": id, "version": expectedVersion})
	if err != nil {
		return PurchaseInvoice{}, err
	}
	if replay {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return s.GetPurchaseInvoice(ctx, session, replayID)
	}
	var invoiceNo, supplierID, branchID, warehouseID, currency, status, exchangeRate, payable string
	var purchaseOrderID, goodsReceiptID *string
	var invoiceDate time.Time
	var dueDate *time.Time
	if err = tx.QueryRow(ctx, `SELECT i.invoice_no,i.supplier_id,i.branch_id,COALESCE(i.warehouse_id::text,''),i.purchase_order_id,i.goods_receipt_id,i.invoice_date,i.due_date,i.currency,COALESCE(d.exchange_rate::text,'1'),i.status,i.payable_total::text FROM purchase_invoices i LEFT JOIN documents d ON d.company_id=i.company_id AND d.id=i.document_id WHERE i.company_id=$1 AND i.id=$2 AND i.version=$3 FOR UPDATE OF i`, session.CurrentCompanyID, id, expectedVersion).Scan(&invoiceNo, &supplierID, &branchID, &warehouseID, &purchaseOrderID, &goodsReceiptID, &invoiceDate, &dueDate, &currency, &exchangeRate, &status, &payable); errors.Is(err, pgx.ErrNoRows) {
		return PurchaseInvoice{}, identity.ErrConflict
	} else if err != nil {
		return PurchaseInvoice{}, err
	}
	if status != "DRAFT" {
		return PurchaseInvoice{}, ErrInvalidTransition
	}
	if err = s.ensureBranch(ctx, session, branchID); err != nil {
		return PurchaseInvoice{}, err
	}
	input := PurchaseInvoiceInput{InvoiceNo: invoiceNo, SupplierID: supplierID, BranchID: branchID, WarehouseID: warehouseID, InvoiceDate: invoiceDate, DueDate: dueDate, Currency: currency, ExchangeRate: exchangeRate, PurchaseOrderID: dereferenceString(purchaseOrderID), GoodsReceiptID: dereferenceString(goodsReceiptID)}
	// Only goods-receipt sources belong in GoodsReceiptIDs. A purchase-order
	// source is carried separately in input.PurchaseOrderID; if it leaks in here
	// lockInvoiceAllocationSourceTx looks it up in goods_receipts and returns
	// NOT_FOUND, which is why an order-sourced invoice could not be finalized.
	sourceRows, err := tx.Query(ctx, `SELECT s.source_document_id
		FROM commercial_document_sources s
		JOIN documents d ON d.company_id=s.company_id AND d.id=s.source_document_id
		WHERE s.company_id=$1 AND s.document_id=$2 AND s.relation_type='INVOICING'
		  AND d.document_type_code='PURCHASE_DELIVERY'
		ORDER BY s.created_at,s.source_document_id`, session.CurrentCompanyID, id)
	if err != nil {
		return PurchaseInvoice{}, err
	}
	for sourceRows.Next() {
		var sourceID string
		if err = sourceRows.Scan(&sourceID); err != nil {
			sourceRows.Close()
			return PurchaseInvoice{}, err
		}
		input.GoodsReceiptIDs = append(input.GoodsReceiptIDs, sourceID)
	}
	if err = sourceRows.Err(); err != nil {
		sourceRows.Close()
		return PurchaseInvoice{}, err
	}
	sourceRows.Close()
	input.GoodsReceiptIDs = purchaseReceiptIDs(input)
	rows, err := tx.Query(ctx, `SELECT id,line_no,line_type,COALESCE(purchase_order_line_id::text,''),COALESCE(goods_receipt_line_id::text,''),product_id,COALESCE(variant_id::text,''),COALESCE(warehouse_id::text,''),unit_code,base_quantity::text,conversion_factor::text,description_snapshot,quantity::text,unit_price::text,gross_amount::text,discount_amount::text,tax_base::text,tax_amount::text,withholding_amount::text,payable_amount::text FROM purchase_invoice_lines WHERE company_id=$1 AND invoice_id=$2 ORDER BY line_no`, session.CurrentCompanyID, id)
	if err != nil {
		return PurchaseInvoice{}, err
	}
	for rows.Next() {
		var line PurchaseInvoiceLine
		if err = rows.Scan(&line.ID, &line.LineNo, &line.LineType, &line.PurchaseOrderLineID, &line.GoodsReceiptLineID, &line.ProductID, &line.VariantID, &line.WarehouseID, &line.UnitCode, &line.BaseQuantity, &line.ConversionFactor, &line.DescriptionSnapshot, &line.Quantity, &line.UnitPrice, &line.GrossAmount, &line.DiscountAmount, &line.TaxBase, &line.TaxAmount, &line.WithholdingAmount, &line.PayableAmount); err != nil {
			rows.Close()
			return PurchaseInvoice{}, err
		}
		input.Lines = append(input.Lines, line)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return PurchaseInvoice{}, err
	}
	rows.Close()
	if len(input.Lines) == 0 {
		return PurchaseInvoice{}, ErrDocumentHasNoLines
	}
	postingLines := make([]purchasePostingLine, 0, len(input.Lines))
	for _, line := range input.Lines {
		postingLines = append(postingLines, purchasePostingLine{LineNo: line.LineNo, LineType: line.LineType, ProductID: line.ProductID, VariantID: line.VariantID, WarehouseID: line.WarehouseID})
	}
	if err = validatePurchasePostingMastersTx(ctx, tx, session, branchID, supplierID, warehouseID, postingLines); err != nil {
		return PurchaseInvoice{}, err
	}
	if input.PurchaseOrderID != "" || len(input.GoodsReceiptIDs) > 0 {
		if err = s.lockInvoiceAllocationSourceTx(ctx, tx, session.CurrentCompanyID, &input); err != nil {
			return PurchaseInvoice{}, err
		}
	}
	stockLinesByWarehouse := make(map[string][]inventory.PurchaseStockLine)
	orderIDs := make(map[string]struct{})
	for index := range input.Lines {
		line := &input.Lines[index]
		if line.PurchaseOrderLineID != "" {
			if err = s.allocateInvoiceQuantityTx(ctx, tx, session.CurrentCompanyID, line.PurchaseOrderLineID, line.LineType, line.Quantity); err != nil {
				return PurchaseInvoice{}, err
			}
			var orderID string
			if err = tx.QueryRow(ctx, `SELECT order_id FROM purchase_order_lines WHERE company_id=$1 AND id=$2`, session.CurrentCompanyID, line.PurchaseOrderLineID).Scan(&orderID); err != nil {
				return PurchaseInvoice{}, err
			}
			orderIDs[orderID] = struct{}{}
		}
		if purchaseInvoicePostsStock(*line) {
			if line.WarehouseID == "" {
				return PurchaseInvoice{}, validation("direkt alış faturası ürün satır deposu bulunamadı")
			}
			// Stock is valued at the discounted (net-of-discount, pre-tax)
			// unit cost, not the gross list price: a line's discount is part
			// of what the item actually cost, and VAT is never part of the
			// inventory cost basis. TaxBase already equals gross-discount and
			// is validated as such above.
			netUnitCost := divide(line.TaxBase, line.Quantity)
			stockLinesByWarehouse[line.WarehouseID] = append(stockLinesByWarehouse[line.WarehouseID], inventory.PurchaseStockLine{LineID: line.ID, ProductID: line.ProductID, VariantID: line.VariantID, Quantity: line.Quantity, BaseQuantity: line.BaseQuantity, ConversionFactor: line.ConversionFactor, UnitCode: line.UnitCode, UnitCost: netUnitCost, Currency: currency})
		}
		if line.GoodsReceiptLineID != "" {
			if err = allocatePurchaseLinesTx(ctx, tx, session.CurrentCompanyID, line.GoodsReceiptLineID, line.ID, "INVOICING", line.Quantity, line.BaseQuantity); err != nil {
				return PurchaseInvoice{}, err
			}
		} else if line.PurchaseOrderLineID != "" {
			if err = allocatePurchaseLinesTx(ctx, tx, session.CurrentCompanyID, line.PurchaseOrderLineID, line.ID, "INVOICING", line.Quantity, line.BaseQuantity); err != nil {
				return PurchaseInvoice{}, err
			}
		}
	}
	if s.financePost == nil {
		return PurchaseInvoice{}, validation("alış faturası için finans posting servisi hazır değil")
	}
	postingID, err := s.financePost.PostPurchaseInvoiceTx(ctx, tx, session, PurchaseInvoicePostingInput{InvoiceID: id, SupplierID: supplierID, Currency: currency, Amount: payable, ExchangeRate: exchangeRate, InvoiceDate: invoiceDate, DueDate: dueDate, Description: "Alış faturası " + invoiceNo})
	if err != nil {
		return PurchaseInvoice{}, err
	}
	warehouseIDs := make([]string, 0, len(stockLinesByWarehouse))
	for lineWarehouseID := range stockLinesByWarehouse {
		warehouseIDs = append(warehouseIDs, lineWarehouseID)
	}
	sort.Strings(warehouseIDs)
	for _, lineWarehouseID := range warehouseIDs {
		poster, ok := s.stockPoster.(PurchaseInvoiceStockPoster)
		if !ok {
			return PurchaseInvoice{}, validation("direkt alış faturası için stok posting servisi hazır değil")
		}
		if err = poster.PostPurchaseInvoiceMovementsTx(ctx, tx, session, inventory.PurchaseReceiptStockPostingInput{ReceiptID: id, WarehouseID: lineWarehouseID, Lines: stockLinesByWarehouse[lineWarehouseID]}); err != nil {
			return PurchaseInvoice{}, err
		}
	}
	for orderID := range orderIDs {
		if err = s.refreshOrderStatusTx(ctx, tx, session.CurrentCompanyID, orderID); err != nil {
			return PurchaseInvoice{}, err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE purchase_invoices SET status='POSTED',posted_at=now(),finance_posting_id=$1,version=version+1 WHERE company_id=$2 AND id=$3 AND status='DRAFT' AND version=$4`, postingID, session.CurrentCompanyID, id, expectedVersion); err != nil {
		return PurchaseInvoice{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE documents SET status='POSTED',posted_at=COALESCE(posted_at,now()),posted_by=$1,post_idempotency_key=$2,updated_by=$1,updated_at=now(),version=version+1 WHERE company_id=$3 AND id=$4 AND status='DRAFT'`, session.User.ID, meta.IdempotencyKey, session.CurrentCompanyID, id); err != nil {
		return PurchaseInvoice{}, err
	}
	if err = s.auditEventTx(ctx, tx, session, "PURCHASE_INVOICE_FINALIZED", "purchase.invoice.finalized", id, meta, map[string]any{"invoice_no": invoiceNo, "finance_posting_id": postingID}); err != nil {
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

// FinalizePurchaseReturn applies the supplier finance reversal and outbound
// stock movement only after the persisted draft is revalidated against the
// source receipt under row locks.
func (s *Service) FinalizePurchaseReturn(ctx context.Context, session identity.Session, id string, expectedVersion int64, meta identity.RequestMeta) (PurchaseReturn, error) {
	if err := s.authorize(session, "purchase.return.post"); err != nil {
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
	replayID, replay, err := reserveCommand(ctx, tx, session, meta, "purchasing.purchase_return.finalize", map[string]any{"id": id, "version": expectedVersion})
	if err != nil {
		return PurchaseReturn{}, err
	}
	if replay {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return s.GetPurchaseReturn(ctx, session, replayID)
	}
	var returnNo, supplierID, branchID, warehouseID, currency, reason, status, exchangeRate, total string
	var sourceReceiptID *string
	var returnDate time.Time
	if err = tx.QueryRow(ctx, `SELECT r.return_no,r.supplier_id,r.branch_id,r.warehouse_id,r.source_receipt_id,r.return_date,r.currency,COALESCE(d.exchange_rate::text,'1'),r.status,r.total::text,r.reason FROM purchase_returns r LEFT JOIN documents d ON d.company_id=r.company_id AND d.id=r.document_id WHERE r.company_id=$1 AND r.id=$2 AND r.version=$3 FOR UPDATE OF r`, session.CurrentCompanyID, id, expectedVersion).Scan(&returnNo, &supplierID, &branchID, &warehouseID, &sourceReceiptID, &returnDate, &currency, &exchangeRate, &status, &total, &reason); errors.Is(err, pgx.ErrNoRows) {
		return PurchaseReturn{}, identity.ErrConflict
	} else if err != nil {
		return PurchaseReturn{}, err
	}
	if status != "DRAFT" {
		return PurchaseReturn{}, ErrInvalidTransition
	}
	if err = s.ensureBranch(ctx, session, branchID); err != nil {
		return PurchaseReturn{}, err
	}
	input := PurchaseReturnInput{ReturnNo: returnNo, SupplierID: supplierID, BranchID: branchID, WarehouseID: warehouseID, SourceReceiptID: dereferenceString(sourceReceiptID), ReturnDate: returnDate, Currency: currency, ExchangeRate: exchangeRate, Reason: reason}
	rows, err := tx.Query(ctx, `SELECT id,line_no,COALESCE(source_receipt_line_id::text,''),product_id,COALESCE(variant_id::text,''),warehouse_id,quantity::text,base_quantity::text,conversion_factor::text,unit_code,unit_cost::text,currency,reason FROM purchase_return_lines WHERE company_id=$1 AND return_id=$2 ORDER BY line_no`, session.CurrentCompanyID, id)
	if err != nil {
		return PurchaseReturn{}, err
	}
	for rows.Next() {
		var line PurchaseReturnLine
		if err = rows.Scan(&line.ID, &line.LineNo, &line.SourceReceiptLineID, &line.ProductID, &line.VariantID, &line.WarehouseID, &line.Quantity, &line.BaseQuantity, &line.ConversionFactor, &line.UnitCode, &line.UnitCost, &line.Currency, &line.Reason); err != nil {
			rows.Close()
			return PurchaseReturn{}, err
		}
		input.Lines = append(input.Lines, line)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return PurchaseReturn{}, err
	}
	rows.Close()
	if len(input.Lines) == 0 {
		return PurchaseReturn{}, ErrDocumentHasNoLines
	}
	postingLines := make([]purchasePostingLine, 0, len(input.Lines))
	for _, line := range input.Lines {
		postingLines = append(postingLines, purchasePostingLine{LineNo: line.LineNo, LineType: "PRODUCT", ProductID: line.ProductID, VariantID: line.VariantID, WarehouseID: line.WarehouseID})
	}
	if err = validatePurchasePostingMastersTx(ctx, tx, session, branchID, supplierID, warehouseID, postingLines); err != nil {
		return PurchaseReturn{}, err
	}
	if err = validatePurchaseReturnSourceShape(dereferenceString(sourceReceiptID), input.Lines); err != nil {
		return PurchaseReturn{}, err
	}
	if len(input.Lines) > 0 && strings.TrimSpace(dereferenceString(sourceReceiptID)) == "" {
		return PurchaseReturn{}, validation("satın alma iadesi kaynak mal kabul belgesine bağlanmalıdır")
	}
	if input.SourceReceiptID != "" {
		if err = s.lockPurchaseReturnSourceTx(ctx, tx, session.CurrentCompanyID, &input); err != nil {
			return PurchaseReturn{}, err
		}
	}
	stockLinesByWarehouse := make(map[string][]inventory.PurchaseStockLine)
	for index := range input.Lines {
		line := &input.Lines[index]
		if input.SourceReceiptID != "" {
			if err = allocatePurchaseLinesTx(ctx, tx, session.CurrentCompanyID, line.SourceReceiptLineID, line.ID, "RETURN", line.Quantity, line.BaseQuantity); err != nil {
				return PurchaseReturn{}, err
			}
		}
		stockLinesByWarehouse[line.WarehouseID] = append(stockLinesByWarehouse[line.WarehouseID], inventory.PurchaseStockLine{LineID: line.ID, ProductID: line.ProductID, VariantID: line.VariantID, Quantity: line.Quantity, BaseQuantity: line.BaseQuantity, ConversionFactor: line.ConversionFactor, UnitCode: line.UnitCode, UnitCost: line.UnitCost, Currency: currency})
	}
	if s.financePost == nil {
		return PurchaseReturn{}, validation("satın alma iadesi için finans posting servisi hazır değil")
	}
	postingID, err := s.financePost.PostPurchaseReturnTx(ctx, tx, session, PurchaseInvoicePostingInput{InvoiceID: id, SupplierID: supplierID, Currency: currency, Amount: total, ExchangeRate: exchangeRate, InvoiceDate: returnDate, Description: "Satın alma iadesi " + returnNo})
	if err != nil {
		return PurchaseReturn{}, err
	}
	warehouseIDs := make([]string, 0, len(stockLinesByWarehouse))
	for lineWarehouseID := range stockLinesByWarehouse {
		warehouseIDs = append(warehouseIDs, lineWarehouseID)
	}
	sort.Strings(warehouseIDs)
	for _, lineWarehouseID := range warehouseIDs {
		if s.stockPoster == nil {
			return PurchaseReturn{}, validation("satın alma iadesi için stok posting servisi hazır değil")
		}
		if err = s.stockPoster.PostPurchaseReturnMovementsTx(ctx, tx, session, inventory.PurchaseReturnStockPostingInput{ReturnID: id, WarehouseID: lineWarehouseID, Lines: stockLinesByWarehouse[lineWarehouseID]}); err != nil {
			return PurchaseReturn{}, err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE purchase_returns SET status='POSTED',posted_at=now(),finance_posting_id=$1,version=version+1 WHERE company_id=$2 AND id=$3 AND status='DRAFT' AND version=$4`, postingID, session.CurrentCompanyID, id, expectedVersion); err != nil {
		return PurchaseReturn{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE documents SET status='POSTED',posted_at=COALESCE(posted_at,now()),posted_by=$1,post_idempotency_key=$2,updated_by=$1,updated_at=now(),version=version+1 WHERE company_id=$3 AND id=$4 AND status='DRAFT'`, session.User.ID, meta.IdempotencyKey, session.CurrentCompanyID, id); err != nil {
		return PurchaseReturn{}, err
	}
	if err = s.auditEventTx(ctx, tx, session, "PURCHASE_RETURN_FINALIZED", "purchase.return.finalized", id, meta, map[string]any{"return_no": returnNo, "finance_posting_id": postingID}); err != nil {
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

func dereferenceString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (s *Service) CreateSupplierProductReference(ctx context.Context, session identity.Session, input SupplierProductReference, meta identity.RequestMeta) (SupplierProductReference, error) {
	if err := s.authorize(session, "purchase.reference.manage"); err != nil {
		return SupplierProductReference{}, err
	}
	input.SupplierCode, input.Barcode = strings.TrimSpace(input.SupplierCode), strings.TrimSpace(input.Barcode)
	if input.SupplierID == "" || input.ProductID == "" || (input.SupplierCode == "" && input.Barcode == "") {
		return SupplierProductReference{}, validation("tedarikçi, ürün ve kod veya barkod gereklidir")
	}
	if err := s.ensureSupplier(ctx, session.CurrentCompanyID, input.SupplierID); err != nil {
		return SupplierProductReference{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return SupplierProductReference{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	replayID, replay, err := reserveCommand(ctx, tx, session, meta, "purchasing.supplier_product_reference.create", input)
	if err != nil {
		return SupplierProductReference{}, err
	}
	if replay {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		input.ID, input.IsActive, input.Version = replayID, true, 1
		return input, nil
	}
	input.ID, input.IsActive, input.Version = uuid.NewString(), true, 1
	if _, err = tx.Exec(ctx, `INSERT INTO supplier_product_references(id,company_id,supplier_id,product_id,variant_id,supplier_code,barcode) VALUES($1,$2,$3,$4,NULLIF($5,'')::uuid,$6,$7)`, input.ID, session.CurrentCompanyID, input.SupplierID, input.ProductID, input.VariantID, input.SupplierCode, input.Barcode); err != nil {
		return SupplierProductReference{}, err
	}
	if err = s.auditEventTx(ctx, tx, session, "SUPPLIER_PRODUCT_REFERENCE_CREATED", "supplier.product_reference.created", input.ID, meta, nil); err != nil {
		return SupplierProductReference{}, err
	}
	if err = completeCommand(ctx, tx, session, meta, input.ID); err != nil {
		return SupplierProductReference{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return SupplierProductReference{}, err
	}
	return input, nil
}

func (s *Service) GetPurchaseOrder(ctx context.Context, session identity.Session, id string) (PurchaseOrder, error) {
	if err := s.authorizeRead(session); err != nil {
		return PurchaseOrder{}, err
	}
	var item PurchaseOrder
	err := s.pool.QueryRow(ctx, `SELECT o.id,o.company_id,COALESCE(o.document_id::text,''),o.order_no,o.supplier_id,COALESCE(p.display_name,''),COALESCE(p.code,''),o.branch_id,o.warehouse_id,COALESCE(hw.name,''),COALESCE(hw.code,''),o.order_date,o.currency,COALESCE(d.exchange_rate::text,'1'),o.status,o.cancelled_at,o.cancellation_reason,o.over_delivery_policy,o.notes,o.total::text,o.version FROM purchase_orders o LEFT JOIN parties p ON p.company_id=o.company_id AND p.id=o.supplier_id LEFT JOIN documents d ON d.company_id=o.company_id AND d.id=o.document_id LEFT JOIN warehouses hw ON hw.company_id=o.company_id AND hw.id=o.warehouse_id WHERE o.company_id=$1 AND o.id=$2`, session.CurrentCompanyID, id).Scan(&item.ID, &item.CompanyID, &item.DocumentID, &item.OrderNo, &item.SupplierID, &item.SupplierName, &item.SupplierCode, &item.BranchID, &item.WarehouseID, &item.WarehouseName, &item.WarehouseCode, &item.OrderDate, &item.Currency, &item.ExchangeRate, &item.Status, &item.CancelledAt, &item.CancellationReason, &item.OverDeliveryPolicy, &item.Notes, &item.Total, &item.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return PurchaseOrder{}, ErrNotFound
	}
	if err != nil {
		return PurchaseOrder{}, err
	}
	if err := ensurePurchaseReadScope(ctx, s.pool, session, item.BranchID, item.WarehouseID); err != nil {
		return PurchaseOrder{}, err
	}
	item.SourceDocuments, err = s.loadPurchaseSourceDocuments(ctx, s.pool, session, id)
	if err != nil {
		return PurchaseOrder{}, err
	}
	rows, err := s.pool.Query(ctx, `SELECT id,line_no,line_type,product_id,COALESCE(variant_id::text,''),COALESCE(warehouse_id::text,''),COALESCE((SELECT w.name FROM warehouses w WHERE w.company_id=purchase_order_lines.company_id AND w.id=purchase_order_lines.warehouse_id),''),COALESCE((SELECT w.code FROM warehouses w WHERE w.company_id=purchase_order_lines.company_id AND w.id=purchase_order_lines.warehouse_id),''),supplier_product_code_snapshot,product_code_snapshot,product_name_snapshot,unit_code,ordered_quantity::text,base_quantity::text,conversion_factor::text,received_quantity::text,invoiced_quantity::text,unit_price::text,discount_amount::text,net_amount::text,currency,tax_snapshot FROM purchase_order_lines WHERE company_id=$1 AND order_id=$2 ORDER BY line_no`, session.CurrentCompanyID, id)
	if err != nil {
		return PurchaseOrder{}, err
	}
	defer rows.Close()
	item.Lines = []PurchaseOrderLine{}
	for rows.Next() {
		var line PurchaseOrderLine
		var snapshot []byte
		if err = rows.Scan(&line.ID, &line.LineNo, &line.LineType, &line.ProductID, &line.VariantID, &line.WarehouseID, &line.WarehouseName, &line.WarehouseCode, &line.SupplierProductCodeSnapshot, &line.ProductCodeSnapshot, &line.ProductNameSnapshot, &line.UnitCode, &line.OrderedQuantity, &line.BaseQuantity, &line.ConversionFactor, &line.ReceivedQuantity, &line.InvoicedQuantity, &line.UnitPrice, &line.DiscountAmount, &line.NetAmount, &line.Currency, &snapshot); err != nil {
			return PurchaseOrder{}, err
		}
		line.LineType = normalizePurchaseLineType(line.LineType)
		if line.LineType == "PRODUCT" {
			if line.WarehouseID == "" {
				return PurchaseOrder{}, validation("alış siparişi ürün satır deposu bulunamadı")
			}
		} else if line.WarehouseID != "" {
			return PurchaseOrder{}, validation("alış siparişi hizmet satırında depo bulunamaz")
		}
		_ = json.Unmarshal(snapshot, &line.TaxSnapshot)
		item.Lines = append(item.Lines, line)
	}
	if err = rows.Err(); err != nil {
		return PurchaseOrder{}, err
	}
	// Per-line warehouse scope checks issue their own queries; run them only after
	// the line rows are fully drained, otherwise the request-pinned connection
	// fails with "conn busy".
	for _, line := range item.Lines {
		if line.LineType == "PRODUCT" {
			if err = ensurePurchaseReadScope(ctx, s.pool, session, item.BranchID, line.WarehouseID); err != nil {
				return PurchaseOrder{}, err
			}
		}
	}
	if err = s.applyPurchaseStatuses(ctx, PurchaseOrderKind, item.Status, &item); err != nil {
		return PurchaseOrder{}, err
	}
	item.RelatedDocuments, err = s.loadPurchaseRelatedDocuments(ctx, s.pool, session, id)
	if err != nil {
		return PurchaseOrder{}, err
	}
	if err = s.applyPurchaseActions(ctx, session, PurchaseOrderKind, item.Status, &item); err != nil {
		return PurchaseOrder{}, err
	}
	return item, nil
}

func (s *Service) GetGoodsReceipt(ctx context.Context, session identity.Session, id string) (GoodsReceipt, error) {
	if err := s.authorizeRead(session); err != nil {
		return GoodsReceipt{}, err
	}
	var item GoodsReceipt
	var orderID *string
	if err := s.pool.QueryRow(ctx, `SELECT g.id,g.company_id,COALESCE(g.document_id::text,''),g.receipt_no,g.purchase_order_id,g.supplier_id,COALESCE(p.display_name,''),COALESCE(p.code,''),g.branch_id,g.warehouse_id,COALESCE(hw.name,''),COALESCE(hw.code,''),g.receipt_date,g.currency,COALESCE(d.exchange_rate::text,'1'),g.status,g.version,g.cancelled_at,g.cancellation_reason,g.over_delivery_warning,g.notes FROM goods_receipts g LEFT JOIN parties p ON p.company_id=g.company_id AND p.id=g.supplier_id LEFT JOIN documents d ON d.company_id=g.company_id AND d.id=g.document_id LEFT JOIN warehouses hw ON hw.company_id=g.company_id AND hw.id=g.warehouse_id WHERE g.company_id=$1 AND g.id=$2`, session.CurrentCompanyID, id).Scan(&item.ID, &item.CompanyID, &item.DocumentID, &item.ReceiptNo, &orderID, &item.SupplierID, &item.SupplierName, &item.SupplierCode, &item.BranchID, &item.WarehouseID, &item.WarehouseName, &item.WarehouseCode, &item.ReceiptDate, &item.Currency, &item.ExchangeRate, &item.Status, &item.Version, &item.CancelledAt, &item.CancellationReason, &item.OverDeliveryWarning, &item.Notes); errors.Is(err, pgx.ErrNoRows) {
		return GoodsReceipt{}, ErrNotFound
	} else if err != nil {
		return GoodsReceipt{}, err
	}
	if err := ensurePurchaseReadScope(ctx, s.pool, session, item.BranchID, item.WarehouseID); err != nil {
		return GoodsReceipt{}, err
	}
	item.PurchaseOrderID = orderID
	sourceDocuments, sourceErr := s.loadPurchaseSourceDocuments(ctx, s.pool, session, id)
	if sourceErr != nil {
		return GoodsReceipt{}, sourceErr
	}
	item.SourceDocuments = sourceDocuments
	rows, err := s.pool.Query(ctx, `SELECT id,line_no,purchase_order_line_id,product_id,COALESCE(variant_id::text,''),warehouse_id,COALESCE((SELECT w.name FROM warehouses w WHERE w.company_id=goods_receipt_lines.company_id AND w.id=goods_receipt_lines.warehouse_id),''),COALESCE((SELECT w.code FROM warehouses w WHERE w.company_id=goods_receipt_lines.company_id AND w.id=goods_receipt_lines.warehouse_id),''),accepted_quantity::text,damaged_quantity::text,rejected_quantity::text,unit_code,base_quantity::text,conversion_factor::text,
COALESCE(GREATEST(base_quantity-COALESCE((SELECT SUM(a.base_quantity) FROM commercial_line_allocations a WHERE a.company_id=goods_receipt_lines.company_id AND a.source_line_id=goods_receipt_lines.id AND a.allocation_type='INVOICING'),0),0)/NULLIF(conversion_factor,0),0)::text,
GREATEST(base_quantity-COALESCE((SELECT SUM(a.base_quantity) FROM commercial_line_allocations a WHERE a.company_id=goods_receipt_lines.company_id AND a.source_line_id=goods_receipt_lines.id AND a.allocation_type='INVOICING'),0),0)::text,
COALESCE(GREATEST(base_quantity-COALESCE((SELECT SUM(a.base_quantity) FROM commercial_line_allocations a WHERE a.company_id=goods_receipt_lines.company_id AND a.source_line_id=goods_receipt_lines.id AND a.allocation_type='RETURN'),0),0)/NULLIF(conversion_factor,0),0)::text,
GREATEST(base_quantity-COALESCE((SELECT SUM(a.base_quantity) FROM commercial_line_allocations a WHERE a.company_id=goods_receipt_lines.company_id AND a.source_line_id=goods_receipt_lines.id AND a.allocation_type='RETURN'),0),0)::text,
unit_cost::text,currency,lot_snapshot,serial_snapshot,tax_snapshot FROM goods_receipt_lines WHERE company_id=$1 AND receipt_id=$2 ORDER BY line_no`, session.CurrentCompanyID, id)
	if err != nil {
		return GoodsReceipt{}, err
	}
	defer rows.Close()
	item.Lines = []GoodsReceiptLine{}
	for rows.Next() {
		var l GoodsReceiptLine
		var poLine *string
		var lot, serial, tax []byte
		if err = rows.Scan(&l.ID, &l.LineNo, &poLine, &l.ProductID, &l.VariantID, &l.WarehouseID, &l.WarehouseName, &l.WarehouseCode, &l.AcceptedQuantity, &l.DamagedQuantity, &l.RejectedQuantity, &l.UnitCode, &l.BaseQuantity, &l.ConversionFactor, &l.RemainingInvoicingQuantity, &l.RemainingInvoicingBaseQuantity, &l.RemainingReturnQuantity, &l.RemainingReturnBaseQuantity, &l.UnitCost, &l.Currency, &lot, &serial, &tax); err != nil {
			return GoodsReceipt{}, err
		}
		l.PurchaseOrderLineID = poLine
		if l.WarehouseID == "" {
			return GoodsReceipt{}, validation("alış irsaliyesi satır deposu bulunamadı")
		}
		_ = json.Unmarshal(lot, &l.LotSnapshot)
		_ = json.Unmarshal(serial, &l.SerialSnapshot)
		_ = json.Unmarshal(tax, &l.TaxSnapshot)
		item.Lines = append(item.Lines, l)
	}
	if err = rows.Err(); err != nil {
		return GoodsReceipt{}, err
	}
	// Scope checks run their own queries; defer them until the line rows are
	// drained so the request-pinned connection is not left busy.
	for _, l := range item.Lines {
		if err = ensurePurchaseReadScope(ctx, s.pool, session, item.BranchID, l.WarehouseID); err != nil {
			return GoodsReceipt{}, err
		}
	}
	if err = s.applyPurchaseStatuses(ctx, GoodsReceiptKind, item.Status, &item); err != nil {
		return GoodsReceipt{}, err
	}
	item.RelatedDocuments, err = s.loadPurchaseRelatedDocuments(ctx, s.pool, session, id)
	if err != nil {
		return GoodsReceipt{}, err
	}
	if err = s.applyPurchaseActions(ctx, session, GoodsReceiptKind, item.Status, &item); err != nil {
		return GoodsReceipt{}, err
	}
	return item, nil
}

func (s *Service) GetPurchaseInvoice(ctx context.Context, session identity.Session, id string) (PurchaseInvoice, error) {
	if err := s.authorizeRead(session); err != nil {
		return PurchaseInvoice{}, err
	}
	var item PurchaseInvoice
	var po, gr, posting *string
	if err := s.pool.QueryRow(ctx, `SELECT i.id,i.company_id,i.invoice_no,i.supplier_id,COALESCE(p.display_name,''),COALESCE(p.code,''),i.branch_id,COALESCE(i.warehouse_id::text,''),COALESCE(hw.name,''),COALESCE(hw.code,''),i.purchase_order_id,i.goods_receipt_id,i.invoice_date,i.due_date,i.currency,COALESCE(d.exchange_rate::text,'1'),i.status,i.version,i.cancelled_at,i.cancellation_reason,i.subtotal::text,i.discount_total::text,i.tax_total::text,(i.payable_total + COALESCE((SELECT SUM(l.withholding_amount) FROM purchase_invoice_lines l WHERE l.company_id=i.company_id AND l.invoice_id=i.id),0))::text,i.payable_total::text,i.finance_posting_id FROM purchase_invoices i LEFT JOIN parties p ON p.company_id=i.company_id AND p.id=i.supplier_id LEFT JOIN documents d ON d.company_id=i.company_id AND d.id=i.document_id LEFT JOIN warehouses hw ON hw.company_id=i.company_id AND hw.id=i.warehouse_id WHERE i.company_id=$1 AND i.id=$2`, session.CurrentCompanyID, id).Scan(&item.ID, &item.CompanyID, &item.InvoiceNo, &item.SupplierID, &item.SupplierName, &item.SupplierCode, &item.BranchID, &item.WarehouseID, &item.WarehouseName, &item.WarehouseCode, &po, &gr, &item.InvoiceDate, &item.DueDate, &item.Currency, &item.ExchangeRate, &item.Status, &item.Version, &item.CancelledAt, &item.CancellationReason, &item.Subtotal, &item.DiscountTotal, &item.TaxTotal, &item.GrandTotal, &item.PayableTotal, &posting); errors.Is(err, pgx.ErrNoRows) {
		return PurchaseInvoice{}, ErrNotFound
	} else if err != nil {
		return PurchaseInvoice{}, err
	}
	if item.WarehouseID == "" {
		if err := s.ensureBranch(ctx, session, item.BranchID); err != nil {
			return PurchaseInvoice{}, err
		}
	} else if err := ensurePurchaseReadScope(ctx, s.pool, session, item.BranchID, item.WarehouseID); err != nil {
		return PurchaseInvoice{}, err
	}
	item.PurchaseOrderID, item.GoodsReceiptID, item.FinancePostingID = po, gr, posting
	item.GoodsReceiptIDs = []string{}
	sourceDocuments, sourceErr := s.loadPurchaseSourceDocuments(ctx, s.pool, session, id)
	if sourceErr != nil {
		return PurchaseInvoice{}, sourceErr
	}
	item.SourceDocuments = sourceDocuments
	sourceRows, err := s.pool.Query(ctx, `SELECT s.source_document_id FROM commercial_document_sources s JOIN documents d ON d.company_id=s.company_id AND d.id=s.source_document_id WHERE s.company_id=$1 AND s.document_id=$2 AND s.relation_type='INVOICING' AND d.document_type_code='PURCHASE_DELIVERY' ORDER BY s.created_at,s.source_document_id`, session.CurrentCompanyID, id)
	if err != nil {
		return PurchaseInvoice{}, err
	}
	for sourceRows.Next() {
		var sourceID string
		if err = sourceRows.Scan(&sourceID); err != nil {
			sourceRows.Close()
			return PurchaseInvoice{}, err
		}
		item.GoodsReceiptIDs = append(item.GoodsReceiptIDs, sourceID)
	}
	if err = sourceRows.Err(); err != nil {
		sourceRows.Close()
		return PurchaseInvoice{}, err
	}
	sourceRows.Close()
	rows, err := s.pool.Query(ctx, `SELECT id,line_no,line_type,COALESCE(purchase_order_line_id::text,''),COALESCE(goods_receipt_line_id::text,''),product_id,COALESCE(variant_id::text,''),COALESCE(warehouse_id::text,''),COALESCE((SELECT w.name FROM warehouses w WHERE w.company_id=purchase_invoice_lines.company_id AND w.id=purchase_invoice_lines.warehouse_id),''),COALESCE((SELECT w.code FROM warehouses w WHERE w.company_id=purchase_invoice_lines.company_id AND w.id=purchase_invoice_lines.warehouse_id),''),unit_code,base_quantity::text,conversion_factor::text,description_snapshot,quantity::text,unit_price::text,gross_amount::text,discount_amount::text,tax_base::text,tax_amount::text,withholding_amount::text,payable_amount::text,tax_components_snapshot FROM purchase_invoice_lines WHERE company_id=$1 AND invoice_id=$2 ORDER BY line_no`, session.CurrentCompanyID, id)
	if err != nil {
		return PurchaseInvoice{}, err
	}
	defer rows.Close()
	item.Lines = []PurchaseInvoiceLine{}
	for rows.Next() {
		var line PurchaseInvoiceLine
		var components []byte
		if err = rows.Scan(&line.ID, &line.LineNo, &line.LineType, &line.PurchaseOrderLineID, &line.GoodsReceiptLineID, &line.ProductID, &line.VariantID, &line.WarehouseID, &line.WarehouseName, &line.WarehouseCode, &line.UnitCode, &line.BaseQuantity, &line.ConversionFactor, &line.DescriptionSnapshot, &line.Quantity, &line.UnitPrice, &line.GrossAmount, &line.DiscountAmount, &line.TaxBase, &line.TaxAmount, &line.WithholdingAmount, &line.PayableAmount, &components); err != nil {
			return PurchaseInvoice{}, err
		}
		_ = json.Unmarshal(components, &line.TaxComponentsSnapshot)
		if normalizePurchaseLineType(line.LineType) == "PRODUCT" {
			if line.WarehouseID == "" {
				return PurchaseInvoice{}, validation("alış faturası ürün satır deposu bulunamadı")
			}
		} else if line.WarehouseID != "" {
			return PurchaseInvoice{}, validation("alış faturası hizmet satırında depo bulunamaz")
		}
		item.Lines = append(item.Lines, line)
	}
	if err = rows.Err(); err != nil {
		return PurchaseInvoice{}, err
	}
	// Scope checks run their own queries; defer them until the line rows are
	// drained so the request-pinned connection is not left busy.
	for _, line := range item.Lines {
		if normalizePurchaseLineType(line.LineType) == "PRODUCT" {
			if err = ensurePurchaseReadScope(ctx, s.pool, session, item.BranchID, line.WarehouseID); err != nil {
				return PurchaseInvoice{}, err
			}
		}
	}
	if err = s.applyPurchaseStatuses(ctx, PurchaseInvoiceKind, item.Status, &item); err != nil {
		return PurchaseInvoice{}, err
	}
	item.RelatedDocuments, err = s.loadPurchaseRelatedDocuments(ctx, s.pool, session, id)
	if err != nil {
		return PurchaseInvoice{}, err
	}
	if err = s.applyPurchaseActions(ctx, session, PurchaseInvoiceKind, item.Status, &item); err != nil {
		return PurchaseInvoice{}, err
	}
	return item, nil
}
func (s *Service) GetPurchaseReturn(ctx context.Context, session identity.Session, id string) (PurchaseReturn, error) {
	if err := s.authorizeRead(session); err != nil {
		return PurchaseReturn{}, err
	}
	var item PurchaseReturn
	var source *string
	var posting *string
	if err := s.pool.QueryRow(ctx, `SELECT r.id,r.company_id,r.return_no,r.supplier_id,COALESCE(p.display_name,''),COALESCE(p.code,''),r.branch_id,r.warehouse_id,COALESCE(hw.name,''),COALESCE(hw.code,''),r.source_receipt_id,r.return_date,r.currency,COALESCE(d.exchange_rate::text,'1'),r.status,r.version,r.cancelled_at,r.cancellation_reason,r.total::text,r.reason,r.finance_posting_id FROM purchase_returns r LEFT JOIN parties p ON p.company_id=r.company_id AND p.id=r.supplier_id LEFT JOIN documents d ON d.company_id=r.company_id AND d.id=r.document_id LEFT JOIN warehouses hw ON hw.company_id=r.company_id AND hw.id=r.warehouse_id WHERE r.company_id=$1 AND r.id=$2`, session.CurrentCompanyID, id).Scan(&item.ID, &item.CompanyID, &item.ReturnNo, &item.SupplierID, &item.SupplierName, &item.SupplierCode, &item.BranchID, &item.WarehouseID, &item.WarehouseName, &item.WarehouseCode, &source, &item.ReturnDate, &item.Currency, &item.ExchangeRate, &item.Status, &item.Version, &item.CancelledAt, &item.CancellationReason, &item.Total, &item.Reason, &posting); errors.Is(err, pgx.ErrNoRows) {
		return PurchaseReturn{}, ErrNotFound
	} else if err != nil {
		return PurchaseReturn{}, err
	}
	if err := ensurePurchaseReadScope(ctx, s.pool, session, item.BranchID, item.WarehouseID); err != nil {
		return PurchaseReturn{}, err
	}
	item.SourceReceiptID = source
	item.FinancePostingID = posting
	sourceDocuments, sourceErr := s.loadPurchaseSourceDocuments(ctx, s.pool, session, id)
	if sourceErr != nil {
		return PurchaseReturn{}, sourceErr
	}
	item.SourceDocuments = sourceDocuments
	rows, err := s.pool.Query(ctx, `SELECT id,line_no,COALESCE(source_receipt_line_id::text,''),product_id,COALESCE(variant_id::text,''),warehouse_id,COALESCE((SELECT w.name FROM warehouses w WHERE w.company_id=purchase_return_lines.company_id AND w.id=purchase_return_lines.warehouse_id),''),COALESCE((SELECT w.code FROM warehouses w WHERE w.company_id=purchase_return_lines.company_id AND w.id=purchase_return_lines.warehouse_id),''),quantity::text,base_quantity::text,conversion_factor::text,unit_code,unit_cost::text,currency,reason FROM purchase_return_lines WHERE company_id=$1 AND return_id=$2 ORDER BY line_no`, session.CurrentCompanyID, id)
	if err != nil {
		return PurchaseReturn{}, err
	}
	defer rows.Close()
	item.Lines = []PurchaseReturnLine{}
	for rows.Next() {
		var line PurchaseReturnLine
		if err = rows.Scan(&line.ID, &line.LineNo, &line.SourceReceiptLineID, &line.ProductID, &line.VariantID, &line.WarehouseID, &line.WarehouseName, &line.WarehouseCode, &line.Quantity, &line.BaseQuantity, &line.ConversionFactor, &line.UnitCode, &line.UnitCost, &line.Currency, &line.Reason); err != nil {
			return PurchaseReturn{}, err
		}
		if line.WarehouseID == "" {
			return PurchaseReturn{}, validation("alış iadesi satır deposu bulunamadı")
		}
		item.Lines = append(item.Lines, line)
	}
	if err = rows.Err(); err != nil {
		return PurchaseReturn{}, err
	}
	// Scope checks run their own queries; defer them until the line rows are
	// drained so the request-pinned connection is not left busy.
	for _, line := range item.Lines {
		if err = ensurePurchaseReadScope(ctx, s.pool, session, item.BranchID, line.WarehouseID); err != nil {
			return PurchaseReturn{}, err
		}
	}
	if err = s.applyPurchaseStatuses(ctx, PurchaseReturnKind, item.Status, &item); err != nil {
		return PurchaseReturn{}, err
	}
	item.RelatedDocuments, err = s.loadPurchaseRelatedDocuments(ctx, s.pool, session, id)
	if err != nil {
		return PurchaseReturn{}, err
	}
	if err = s.applyPurchaseActions(ctx, session, PurchaseReturnKind, item.Status, &item); err != nil {
		return PurchaseReturn{}, err
	}
	return item, nil
}

// rollbackPurchaseProjectionsTx reverses the order-line receiving/invoicing
// projections and the FULFILLMENT/INVOICING/RETURN allocations a now-cancelled
// posting document created, then recomputes the affected orders' fulfillment
// status. It is the mirror image of the projection writes in
// FinalizeGoodsReceipt / FinalizePurchaseInvoice / FinalizePurchaseReturn.
func (s *Service) rollbackPurchaseProjectionsTx(ctx context.Context, tx pgx.Tx, companyID string, kind PurchaseKind, documentID string) error {
	var allocationType, aggregateType string
	switch kind {
	case GoodsReceiptKind:
		allocationType, aggregateType = "FULFILLMENT", "GOODS_RECEIPT"
	case PurchaseInvoiceKind:
		allocationType, aggregateType = "INVOICING", "PURCHASE_INVOICE"
	case PurchaseReturnKind:
		allocationType, aggregateType = "RETURN", "PURCHASE_RETURN"
	default:
		return nil
	}

	// Capture the orders reached through this document's allocations before the
	// allocations are deleted.
	orderIDs := map[string]struct{}{}
	rows, err := tx.Query(ctx, `SELECT DISTINCT pol.order_id
		FROM commercial_line_registry r
		JOIN commercial_line_allocations a ON a.company_id=r.company_id AND a.target_line_id=r.line_id AND a.allocation_type=$3
		JOIN purchase_order_lines pol ON pol.company_id=a.company_id AND pol.id=a.source_line_id
		WHERE r.company_id=$1 AND r.document_id=$2 AND r.aggregate_type=$4`, companyID, documentID, allocationType, aggregateType)
	if err != nil {
		return err
	}
	for rows.Next() {
		var orderID string
		if err = rows.Scan(&orderID); err != nil {
			rows.Close()
			return err
		}
		if strings.TrimSpace(orderID) != "" {
			orderIDs[orderID] = struct{}{}
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}

	switch kind {
	case GoodsReceiptKind:
		if _, err = tx.Exec(ctx, `UPDATE purchase_order_lines pol
			SET received_quantity = pol.received_quantity - grl.accepted
			FROM (SELECT purchase_order_line_id, SUM(accepted_quantity) AS accepted
			      FROM goods_receipt_lines
			      WHERE company_id=$1 AND receipt_id=$2 AND purchase_order_line_id IS NOT NULL
			      GROUP BY purchase_order_line_id) grl
			WHERE pol.company_id=$1 AND pol.id=grl.purchase_order_line_id`, companyID, documentID); err != nil {
			return err
		}
	case PurchaseInvoiceKind:
		if _, err = tx.Exec(ctx, `UPDATE purchase_order_lines pol
			SET invoiced_quantity = pol.invoiced_quantity - pil.qty
			FROM (SELECT purchase_order_line_id, SUM(quantity) AS qty
			      FROM purchase_invoice_lines
			      WHERE company_id=$1 AND invoice_id=$2 AND purchase_order_line_id IS NOT NULL
			      GROUP BY purchase_order_line_id) pil
			WHERE pol.company_id=$1 AND pol.id=pil.purchase_order_line_id`, companyID, documentID); err != nil {
			return err
		}
	}

	if _, err = tx.Exec(ctx, `DELETE FROM commercial_line_allocations a
		USING commercial_line_registry r
		WHERE a.company_id=$1 AND a.allocation_type=$3
		  AND r.company_id=a.company_id AND r.line_id=a.target_line_id
		  AND r.aggregate_type=$4 AND r.document_id=$2`, companyID, documentID, allocationType, aggregateType); err != nil {
		return err
	}

	// A return never touched an order projection; freeing its RETURN
	// allocations above is the whole rollback.
	if kind == PurchaseReturnKind {
		return nil
	}

	var headerOrderID string
	switch kind {
	case GoodsReceiptKind:
		_ = tx.QueryRow(ctx, `SELECT COALESCE(purchase_order_id::text,'') FROM goods_receipts WHERE company_id=$1 AND id=$2`, companyID, documentID).Scan(&headerOrderID)
	case PurchaseInvoiceKind:
		_ = tx.QueryRow(ctx, `SELECT COALESCE(purchase_order_id::text,'') FROM purchase_invoices WHERE company_id=$1 AND id=$2`, companyID, documentID).Scan(&headerOrderID)
	}
	if strings.TrimSpace(headerOrderID) != "" {
		orderIDs[headerOrderID] = struct{}{}
	}
	for orderID := range orderIDs {
		if err = s.refreshOrderStatusTx(ctx, tx, companyID, orderID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) refreshOrderStatusTx(ctx context.Context, tx pgx.Tx, companyID, orderID string) error {
	var total, full, partial int
	if err := tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE (line_type='SERVICE' AND invoiced_quantity>=ordered_quantity) OR (line_type='PRODUCT' AND received_quantity>=ordered_quantity)),count(*) FILTER (WHERE (line_type='SERVICE' AND invoiced_quantity>0) OR (line_type='PRODUCT' AND received_quantity>0)) FROM purchase_order_lines WHERE company_id=$1 AND order_id=$2`, companyID, orderID).Scan(&total, &full, &partial); err != nil {
		return err
	}
	status := "CONFIRMED"
	if total > 0 && full == total {
		status = "FULFILLED"
	} else if partial > 0 {
		status = "PARTIALLY_FULFILLED"
	}
	// The fulfillment projection may move in either direction while the order
	// is OPEN (a cancelled downstream receipt/invoice lowers it); DRAFT and
	// CANCELLED orders are never touched here.
	_, err := tx.Exec(ctx, `UPDATE purchase_orders SET status=$1,updated_at=now(),version=version+1 WHERE company_id=$2 AND id=$3 AND status IN ('CONFIRMED','PARTIALLY_FULFILLED','FULFILLED')`, status, companyID, orderID)
	return err
}

func (s *Service) lockInvoiceAllocationSourceTx(ctx context.Context, tx pgx.Tx, companyID string, input *PurchaseInvoiceInput) error {
	if input == nil {
		return validation("alış faturası bağlantısı geçersiz")
	}
	receiptIDs := purchaseReceiptIDs(*input)
	receiptSet := make(map[string]struct{}, len(receiptIDs))
	for _, receiptID := range receiptIDs {
		if uuid.Validate(receiptID) != nil {
			return validation("alış faturası mal kabul bağlantısı geçersiz")
		}
		receiptSet[receiptID] = struct{}{}
	}
	if input.PurchaseOrderID != "" {
		var supplier, status, currency, branchID string
		if err := tx.QueryRow(ctx, `SELECT supplier_id,status,currency,branch_id FROM purchase_orders WHERE company_id=$1 AND id=$2 FOR UPDATE`, companyID, input.PurchaseOrderID).Scan(&supplier, &status, &currency, &branchID); errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		} else if supplier != input.SupplierID || branchID != input.BranchID || strings.ToUpper(currency) != input.Currency || (status != "CONFIRMED" && status != "PARTIALLY_FULFILLED" && status != "FULFILLED") {
			return validation("alış faturası sipariş bağlantısı geçersiz")
		}
	}
	for _, receiptID := range receiptIDs {
		var supplier, status, currency, branchID, warehouseID string
		var orderID *string
		if err := tx.QueryRow(ctx, `SELECT supplier_id,status,currency,branch_id,warehouse_id,purchase_order_id FROM goods_receipts WHERE company_id=$1 AND id=$2 FOR UPDATE`, companyID, receiptID).Scan(&supplier, &status, &currency, &branchID, &warehouseID, &orderID); errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		} else if supplier != input.SupplierID || status != "POSTED" || branchID != input.BranchID || strings.ToUpper(currency) != input.Currency || (input.PurchaseOrderID != "" && (orderID == nil || *orderID != input.PurchaseOrderID)) {
			return validation("alış faturası mal kabul bağlantısı geçersiz")
		}
		_ = warehouseID
	}
	for index := range input.Lines {
		line := &input.Lines[index]
		line.LineType = normalizePurchaseLineType(line.LineType)
		if !validPurchaseLineType(line.LineType) {
			return validation("alış faturası satır türü geçersiz")
		}
		if line.GoodsReceiptLineID != "" {
			if line.LineType != "PRODUCT" {
				return validation("hizmet satırı mal kabul bağlantılı faturaya eklenemez")
			}
			if len(receiptIDs) == 0 {
				return validation("mal kabul satırı için kaynak mal kabul seçilmelidir")
			}
			if line.GoodsReceiptLineID == "" {
				return validation("mal kabul bağlantılı fatura satırı mal kabul satırını belirtmelidir")
			}
			var productID, variantID, receiptID, receiptWarehouse string
			var orderLineID *string
			if err := tx.QueryRow(ctx, `SELECT l.product_id,COALESCE(l.variant_id::text,''),l.receipt_id,l.warehouse_id,l.purchase_order_line_id FROM goods_receipt_lines l JOIN goods_receipts g ON g.company_id=l.company_id AND g.id=l.receipt_id WHERE l.company_id=$1 AND l.id=$2 FOR UPDATE`, companyID, line.GoodsReceiptLineID).Scan(&productID, &variantID, &receiptID, &receiptWarehouse, &orderLineID); errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			} else if err != nil {
				return err
			} else if func() bool { _, ok := receiptSet[receiptID]; return !ok }() || productID != line.ProductID || variantID != line.VariantID {
				return validation("alış faturası satırı mal kabul satırıyla eşleşmiyor")
			}
			if line.WarehouseID != "" && line.WarehouseID != receiptWarehouse {
				return validation("alış faturası satırı mal kabul deposuyla eşleşmelidir")
			}
			line.WarehouseID = receiptWarehouse
			if orderLineID != nil {
				if line.PurchaseOrderLineID != "" && line.PurchaseOrderLineID != *orderLineID {
					return validation("alış faturası satırı sipariş satırıyla eşleşmiyor")
				}
				line.PurchaseOrderLineID = *orderLineID
			}
		} else if len(receiptIDs) > 0 && line.PurchaseOrderLineID == "" {
			return validation("mal kabul bağlantılı fatura satırı mal kabul satırını belirtmelidir")
		}
		if input.PurchaseOrderID != "" && line.PurchaseOrderLineID == "" {
			return validation("sipariş bağlantılı fatura satırı sipariş satırını belirtmelidir")
		}
		if line.PurchaseOrderLineID != "" {
			if input.PurchaseOrderID == "" && len(receiptIDs) == 0 {
				return validation("sipariş satırı için kaynak sipariş seçilmelidir")
			}
			var productID, variantID, orderID, orderLineType, orderWarehouse string
			if err := tx.QueryRow(ctx, `SELECT product_id,COALESCE(variant_id::text,''),order_id,line_type,COALESCE(warehouse_id::text,'') FROM purchase_order_lines WHERE company_id=$1 AND id=$2 FOR UPDATE`, companyID, line.PurchaseOrderLineID).Scan(&productID, &variantID, &orderID, &orderLineType, &orderWarehouse); errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			} else if err != nil {
				return err
			} else if (input.PurchaseOrderID != "" && input.PurchaseOrderID != orderID) || productID != line.ProductID || variantID != line.VariantID || normalizePurchaseLineType(orderLineType) != line.LineType {
				return validation("alış faturası satırı sipariş satırıyla eşleşmiyor")
			}
			if line.LineType == "SERVICE" {
				if line.WarehouseID != "" {
					return validation("alış faturası hizmet satırında depo bulunamaz")
				}
			} else if line.WarehouseID != "" && line.WarehouseID != orderWarehouse {
				return validation("alış faturası ürün satır deposu sipariş satırıyla eşleşmelidir")
			} else {
				line.WarehouseID = orderWarehouse
			}
		}
	}
	return nil
}

func (s *Service) allocateInvoiceQuantityTx(ctx context.Context, tx pgx.Tx, companyID, orderLineID, lineType, quantity string) error {
	var ordered, received, invoiced, storedLineType string
	if err := tx.QueryRow(ctx, `SELECT ordered_quantity::text,received_quantity::text,invoiced_quantity::text,line_type FROM purchase_order_lines WHERE company_id=$1 AND id=$2 FOR UPDATE`, companyID, orderLineID).Scan(&ordered, &received, &invoiced, &storedLineType); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if normalizePurchaseLineType(storedLineType) != normalizePurchaseLineType(lineType) {
		return validation("fatura satır türü sipariş satırıyla eşleşmiyor")
	}
	limit := received
	if normalizePurchaseLineType(lineType) == "SERVICE" {
		limit = ordered
	}
	if compare(add(invoiced, quantity), limit) > 0 {
		if normalizePurchaseLineType(lineType) == "SERVICE" {
			return validation("hizmet fatura miktarı sipariş miktarını aşamaz")
		}
		return validation("fatura miktarı kabul edilen miktarı aşamaz")
	}
	_, err := tx.Exec(ctx, `UPDATE purchase_order_lines SET invoiced_quantity=invoiced_quantity+$1 WHERE company_id=$2 AND id=$3`, quantity, companyID, orderLineID)
	return err
}

func (s *Service) lockPurchaseReturnSourceTx(ctx context.Context, tx pgx.Tx, companyID string, input *PurchaseReturnInput) error {
	var supplier, status, receiptCurrency string
	var branchID, warehouseID string
	if err := tx.QueryRow(ctx, `SELECT supplier_id,status,branch_id,warehouse_id,currency FROM goods_receipts WHERE company_id=$1 AND id=$2 FOR UPDATE`, companyID, input.SourceReceiptID).Scan(&supplier, &status, &branchID, &warehouseID, &receiptCurrency); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	} else if supplier != input.SupplierID || status != "POSTED" || branchID != input.BranchID || warehouseID != input.WarehouseID {
		return validation("satın alma iadesi kaynak mal kabulüyle eşleşmiyor")
	}
	// Return valuation and currency are server-authoritative: they must match
	// the source goods receipt so the supplier ledger and FIFO cost-out stay
	// consistent. A client-supplied unit_cost or currency is not trusted.
	if !strings.EqualFold(strings.TrimSpace(input.Currency), strings.TrimSpace(receiptCurrency)) {
		return validation("satın alma iadesi kaynak mal kabulün para birimini kullanmalıdır")
	}
	seen := map[string]struct{}{}
	for index := range input.Lines {
		line := &input.Lines[index]
		if line.SourceReceiptLineID == "" {
			return validation("kaynak mal kabul bağlı iade satırı mal kabul satırını belirtmelidir")
		}
		if _, exists := seen[line.SourceReceiptLineID]; exists {
			return validation("aynı mal kabul satırı bir iade içinde yalnızca bir kez kullanılabilir")
		}
		seen[line.SourceReceiptLineID] = struct{}{}
		var productID, variantID, receiptID, receiptWarehouse, accepted, receiptLineUnitCost string
		if err := tx.QueryRow(ctx, `SELECT product_id,COALESCE(variant_id::text,''),receipt_id,warehouse_id,accepted_quantity::text,unit_cost::text FROM goods_receipt_lines WHERE company_id=$1 AND id=$2 FOR UPDATE`, companyID, line.SourceReceiptLineID).Scan(&productID, &variantID, &receiptID, &receiptWarehouse, &accepted, &receiptLineUnitCost); errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		} else if receiptID != input.SourceReceiptID || productID != line.ProductID || variantID != line.VariantID {
			return validation("satın alma iade satırı kaynak mal kabul satırıyla eşleşmiyor")
		}
		// Override any client-supplied cost with the source receipt line's value.
		line.UnitCost = receiptLineUnitCost
		if strings.TrimSpace(line.WarehouseID) == "" {
			line.WarehouseID = receiptWarehouse
		} else if line.WarehouseID != receiptWarehouse {
			return validation("satın alma iade satır deposu kaynak mal kabul satırıyla eşleşmelidir")
		}
		var returned string
		if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(quantity),0)::text FROM purchase_return_lines prl JOIN purchase_returns pr ON pr.company_id=prl.company_id AND pr.id=prl.return_id WHERE prl.company_id=$1 AND prl.source_receipt_line_id=$2 AND pr.status='POSTED'`, companyID, line.SourceReceiptLineID).Scan(&returned); err != nil {
			return err
		}
		if compare(add(returned, line.Quantity), accepted) > 0 {
			return validation("iade miktarı kabul edilen miktarı aşamaz")
		}
	}
	return nil
}

func (s *Service) number(ctx context.Context, tx pgx.Tx, companyID, kind, prefix, supplied string, year int) (string, error) {
	if strings.TrimSpace(supplied) != "" {
		return strings.TrimSpace(supplied), nil
	}
	var number string
	if err := tx.QueryRow(ctx, `SELECT allocate_commercial_document_number($1,$2,$3)`, companyID, kind, year).Scan(&number); err != nil {
		return "", err
	}
	_ = prefix // The commercial sequence owns the canonical prefix.
	return number, nil
}

func registerPurchaseLineTx(ctx context.Context, tx pgx.Tx, companyID, aggregateType, documentID, lineID string, lineNo int, lineType, quantity, baseQuantity string) error {
	if uuid.Validate(strings.TrimSpace(lineID)) != nil || uuid.Validate(strings.TrimSpace(documentID)) != nil {
		return validation("satın alma satır kimliği geçersiz")
	}
	if lineType == "" {
		lineType = "PRODUCT"
	}
	_, err := tx.Exec(ctx, `INSERT INTO commercial_line_registry(company_id,line_id,aggregate_type,document_id,line_no,line_type,quantity,base_quantity) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, companyID, lineID, aggregateType, documentID, lineNo, lineType, quantity, baseQuantity)
	return err
}

func allocatePurchaseLinesTx(ctx context.Context, tx pgx.Tx, companyID, sourceLineID, targetLineID, allocationType, quantity, baseQuantity string) error {
	var sourceDocumentID, sourceType, sourceBaseQuantity, sourceLineType string
	if err := tx.QueryRow(ctx, `SELECT document_id,aggregate_type,base_quantity::text,line_type FROM commercial_line_registry WHERE company_id=$1 AND line_id=$2 FOR UPDATE`, companyID, sourceLineID).Scan(&sourceDocumentID, &sourceType, &sourceBaseQuantity, &sourceLineType); errors.Is(err, pgx.ErrNoRows) {
		return validation("kaynak satın alma satırı bulunamadı")
	} else if err != nil {
		return err
	}
	var targetDocumentID, targetType, targetLineType string
	if err := tx.QueryRow(ctx, `SELECT document_id,aggregate_type,line_type FROM commercial_line_registry WHERE company_id=$1 AND line_id=$2`, companyID, targetLineID).Scan(&targetDocumentID, &targetType, &targetLineType); errors.Is(err, pgx.ErrNoRows) {
		return validation("hedef satın alma satırı bulunamadı")
	} else if err != nil {
		return err
	}
	valid := (allocationType == "FULFILLMENT" && sourceType == "PURCHASE_ORDER" && targetType == "GOODS_RECEIPT") ||
		(allocationType == "INVOICING" && (sourceType == "PURCHASE_ORDER" || sourceType == "GOODS_RECEIPT") && targetType == "PURCHASE_INVOICE") ||
		(allocationType == "RETURN" && sourceType == "GOODS_RECEIPT" && targetType == "PURCHASE_RETURN")
	if !valid {
		return validation("satın alma satır ilişkisi geçersiz")
	}
	if normalizePurchaseLineType(sourceLineType) != normalizePurchaseLineType(targetLineType) {
		return validation("satın alma satır türleri eşleşmiyor")
	}
	if allocationType == "FULFILLMENT" && normalizePurchaseLineType(sourceLineType) != "PRODUCT" {
		return validation("yalnız ürün satırları mal kabuline tahsis edilebilir")
	}
	if _, err := commercialDecimalPurchase(quantity); err != nil {
		return validation("satın alma tahsis miktarı geçersiz")
	}
	if strings.TrimSpace(baseQuantity) == "" {
		baseQuantity = quantity
	}
	if _, err := commercialDecimalPurchase(baseQuantity); err != nil {
		return validation("satın alma tahsis baz miktarı geçersiz")
	}
	var allocated string
	if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(base_quantity),0)::text FROM commercial_line_allocations WHERE company_id=$1 AND source_line_id=$2 AND allocation_type=$3`, companyID, sourceLineID, allocationType).Scan(&allocated); err != nil {
		return err
	}
	if compare(add(allocated, baseQuantity), sourceBaseQuantity) > 0 {
		return ErrOverDelivery
	}
	_, err := tx.Exec(ctx, `INSERT INTO commercial_line_allocations(id,company_id,source_line_id,target_line_id,allocation_type,quantity,base_quantity) VALUES($1,$2,$3,$4,$5,$6,$7)`, uuid.NewString(), companyID, sourceLineID, targetLineID, allocationType, quantity, baseQuantity)
	return err
}

func commercialDecimalPurchase(value string) (*big.Rat, error) {
	ratio, err := parsePurchaseDecimal(value)
	if err != nil || ratio.Sign() <= 0 {
		return nil, errors.New("invalid decimal")
	}
	return ratio, nil
}

func validPurchaseDecimalLiteral(value string) bool {
	if value == "" {
		return false
	}
	if value[0] == '-' || value[0] == '+' {
		value = value[1:]
	}
	if value == "" {
		return false
	}
	digits := 0
	dot := false
	for _, ch := range value {
		switch {
		case ch >= '0' && ch <= '9':
			digits++
		case ch == '.' && !dot:
			dot = true
		default:
			return false
		}
	}
	return digits > 0
}

func resolvePurchaseConversionTx(ctx context.Context, tx pgx.Tx, companyID, productID, unitCode, quantity, suppliedBase, suppliedFactor string) (string, string, error) {
	var persistedFactor string
	if err := tx.QueryRow(ctx, `SELECT conversion_factor::text FROM product_units WHERE company_id=$1 AND product_id=$2 AND unit_code=$3`, companyID, productID, unitCode).Scan(&persistedFactor); errors.Is(err, pgx.ErrNoRows) {
		return "", "", validation("ürün birim dönüşümü bulunamadı")
	} else if err != nil {
		return "", "", err
	}
	factor := persistedFactor
	if strings.TrimSpace(suppliedFactor) != "" {
		providedFactor, providedErr := commercialDecimalPurchase(suppliedFactor)
		storedFactor, storedErr := commercialDecimalPurchase(persistedFactor)
		if providedErr != nil || storedErr != nil || providedFactor.Cmp(storedFactor) != 0 {
			return "", "", validation("birim dönüşüm katsayısı güncel ürün birimiyle eşleşmiyor")
		}
	}
	factorRat, err := commercialDecimalPurchase(factor)
	if err != nil {
		return "", "", validation("birim dönüşüm katsayısı geçersiz")
	}
	quantityRat, err := commercialDecimalPurchase(quantity)
	if err != nil {
		return "", "", validation("miktar geçersiz")
	}
	expectedBase := new(big.Rat).Mul(quantityRat, factorRat)
	base := expectedBase
	if strings.TrimSpace(suppliedBase) != "" {
		provided, providedErr := commercialDecimalPurchase(suppliedBase)
		if providedErr != nil || provided.Cmp(expectedBase) != 0 {
			return "", "", validation("baz miktar dönüşüm katsayısıyla eşleşmiyor")
		}
		base = provided
	}
	return canonical(base), canonical(factorRat), nil
}

func (s *Service) authorize(session identity.Session, permission string) error {
	if identity.ValidateExternalActor(session) != nil || !session.HasPermission(permission) {
		return identity.ErrForbidden
	}
	return nil
}

// authorizeAny passes when the session holds at least one of the permissions.
// Used where drafting is gated by a ".draft" permission but a ".post" holder is
// also allowed to draft.
func (s *Service) authorizeAny(session identity.Session, permissions ...string) error {
	if identity.ValidateExternalActor(session) != nil {
		return identity.ErrForbidden
	}
	for _, permission := range permissions {
		if session.HasPermission(permission) {
			return nil
		}
	}
	return identity.ErrForbidden
}
func (s *Service) authorizeRead(session identity.Session) error {
	if identity.ValidateExternalActor(session) != nil {
		return identity.ErrForbidden
	}
	for _, permission := range []string{
		"purchase.order.read", "purchase.order.manage",
		"purchase.receipt.post", "purchase.receipt.draft",
		"purchase.invoice.post", "purchase.invoice.draft",
		"purchase.return.post", "purchase.return.draft",
	} {
		if session.HasPermission(permission) {
			return nil
		}
	}
	return identity.ErrForbidden
}

// deriveSupplierDueDate fills a blank invoice due date from the supplier's
// own payment term, counted from the document date. An explicit due date is
// never overridden by the term.
func (s *Service) deriveSupplierDueDate(ctx context.Context, companyID, supplierID string, documentDate time.Time, dueDate **time.Time) error {
	if *dueDate != nil {
		return nil
	}
	var paymentTermID *string
	if err := s.pool.QueryRow(ctx, `SELECT payment_term_id::text FROM parties WHERE company_id=$1 AND id=$2`, companyID, supplierID).Scan(&paymentTermID); err != nil {
		return err
	}
	if paymentTermID == nil {
		return nil
	}
	var dueDays int
	if err := s.pool.QueryRow(ctx, `SELECT due_days FROM payment_terms WHERE company_id=$1 AND id=$2 AND is_active`, companyID, *paymentTermID).Scan(&dueDays); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	derived := documentDate.AddDate(0, 0, dueDays)
	*dueDate = &derived
	return nil
}

func (s *Service) ensureSupplier(ctx context.Context, companyID, supplierID string) error {
	var supplier, active bool
	err := s.pool.QueryRow(ctx, `SELECT is_supplier,is_active FROM parties WHERE company_id=$1 AND id=$2`, companyID, supplierID).Scan(&supplier, &active)
	if errors.Is(err, pgx.ErrNoRows) || !supplier {
		return identity.ErrForbidden
	}
	if err == nil && !active {
		return ErrSupplierInactive
	}
	return err
}
func (s *Service) ensureBranch(ctx context.Context, session identity.Session, branchID string) error {
	return ensurePurchaseBranch(ctx, s.pool, session, branchID)
}

func ensurePurchaseBranch(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, session identity.Session, branchID string) error {
	var ok bool
	err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM branches b WHERE b.company_id=$1 AND b.id=$2 AND b.is_active AND (NOT EXISTS(SELECT 1 FROM membership_branch_scopes s WHERE s.company_id=$1 AND s.user_id=$3) OR EXISTS(SELECT 1 FROM membership_branch_scopes s WHERE s.company_id=$1 AND s.user_id=$3 AND s.branch_id=b.id)))`, session.CurrentCompanyID, branchID, session.User.ID).Scan(&ok)
	if err != nil {
		return err
	}
	if !ok {
		return identity.ErrForbidden
	}
	return nil
}
func (s *Service) ensureScope(ctx context.Context, session identity.Session, branchID, warehouseID string) error {
	return ensurePurchaseScope(ctx, s.pool, session, branchID, warehouseID)
}

func ensurePurchaseScope(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, session identity.Session, branchID, warehouseID string) error {
	if err := ensurePurchaseBranch(ctx, q, session, branchID); err != nil {
		return err
	}
	var ok bool
	err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM warehouses w WHERE w.company_id=$1 AND w.id=$2 AND w.is_active AND (w.is_system OR w.branch_id IS NULL OR w.branch_id=$4) AND (w.is_system OR ((w.branch_id IS NULL OR NOT EXISTS(SELECT 1 FROM membership_branch_scopes b WHERE b.company_id=$1 AND b.user_id=$3) OR EXISTS(SELECT 1 FROM membership_branch_scopes b WHERE b.company_id=$1 AND b.user_id=$3 AND b.branch_id=w.branch_id)) AND (NOT EXISTS(SELECT 1 FROM membership_warehouse_scopes x WHERE x.company_id=$1 AND x.user_id=$3) OR EXISTS(SELECT 1 FROM membership_warehouse_scopes x WHERE x.company_id=$1 AND x.user_id=$3 AND x.warehouse_id=w.id)))))`, session.CurrentCompanyID, warehouseID, session.User.ID, branchID).Scan(&ok)
	if err != nil {
		return err
	}
	if !ok {
		return identity.ErrForbidden
	}
	return nil
}

// Read scope keeps historical drafts and posted documents visible after a
// branch or warehouse is deactivated. Mutating commands continue to use the
// active-only scope above.
func ensurePurchaseReadBranch(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, session identity.Session, branchID string) error {
	var ok bool
	err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM branches b WHERE b.company_id=$1 AND b.id=$2 AND (NOT EXISTS(SELECT 1 FROM membership_branch_scopes s WHERE s.company_id=$1 AND s.user_id=$3) OR EXISTS(SELECT 1 FROM membership_branch_scopes s WHERE s.company_id=$1 AND s.user_id=$3 AND s.branch_id=b.id)))`, session.CurrentCompanyID, branchID, session.User.ID).Scan(&ok)
	if err != nil {
		return err
	}
	if !ok {
		return identity.ErrForbidden
	}
	return nil
}

func ensurePurchaseReadScope(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, session identity.Session, branchID, warehouseID string) error {
	if err := ensurePurchaseReadBranch(ctx, q, session, branchID); err != nil {
		return err
	}
	if strings.TrimSpace(warehouseID) == "" {
		return nil
	}
	var ok bool
	err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM warehouses w WHERE w.company_id=$1 AND w.id=$2 AND (w.is_system OR w.branch_id IS NULL OR w.branch_id=$4) AND (w.is_system OR ((w.branch_id IS NULL OR NOT EXISTS(SELECT 1 FROM membership_branch_scopes b WHERE b.company_id=$1 AND b.user_id=$3) OR EXISTS(SELECT 1 FROM membership_branch_scopes b WHERE b.company_id=$1 AND b.user_id=$3 AND b.branch_id=w.branch_id)) AND (NOT EXISTS(SELECT 1 FROM membership_warehouse_scopes x WHERE x.company_id=$1 AND x.user_id=$3) OR EXISTS(SELECT 1 FROM membership_warehouse_scopes x WHERE x.company_id=$1 AND x.user_id=$3 AND x.warehouse_id=w.id)))))`, session.CurrentCompanyID, warehouseID, session.User.ID, branchID).Scan(&ok)
	if err != nil {
		return err
	}
	if !ok {
		return identity.ErrForbidden
	}
	return nil
}

func (s *Service) ensurePurchaseOrderScope(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, session identity.Session, orderID string) error {
	var branchID, warehouseID string
	if err := q.QueryRow(ctx, `SELECT branch_id,warehouse_id FROM purchase_orders WHERE company_id=$1 AND id=$2`, session.CurrentCompanyID, orderID).Scan(&branchID, &warehouseID); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if err := ensurePurchaseScope(ctx, q, session, branchID, warehouseID); err != nil {
		return err
	}
	rows, err := q.Query(ctx, `SELECT line_type,COALESCE(warehouse_id::text,'') FROM purchase_order_lines WHERE company_id=$1 AND order_id=$2`, session.CurrentCompanyID, orderID)
	if err != nil {
		return err
	}
	type purchaseOrderLineScope struct {
		lineType    string
		warehouseID string
	}
	lineScopes := make([]purchaseOrderLineScope, 0)
	for rows.Next() {
		var line purchaseOrderLineScope
		if err = rows.Scan(&line.lineType, &line.warehouseID); err != nil {
			rows.Close()
			return err
		}
		lineScopes = append(lineScopes, line)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, line := range lineScopes {
		if normalizePurchaseLineType(line.lineType) == "PRODUCT" {
			if line.warehouseID == "" {
				return validation("alış siparişi ürün satır deposu bulunamadı")
			}
			if err = ensurePurchaseScope(ctx, q, session, branchID, line.warehouseID); err != nil {
				return err
			}
		} else if line.warehouseID != "" {
			return validation("alış siparişi hizmet satırında depo bulunamaz")
		}
	}
	return nil
}

func (s *Service) ensurePurchaseReceiptScope(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, session identity.Session, receiptID string) error {
	var branchID, warehouseID string
	if err := q.QueryRow(ctx, `SELECT branch_id,warehouse_id FROM goods_receipts WHERE company_id=$1 AND id=$2`, session.CurrentCompanyID, receiptID).Scan(&branchID, &warehouseID); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if err := ensurePurchaseScope(ctx, q, session, branchID, warehouseID); err != nil {
		return err
	}
	rows, err := q.Query(ctx, `SELECT warehouse_id::text FROM goods_receipt_lines WHERE company_id=$1 AND receipt_id=$2`, session.CurrentCompanyID, receiptID)
	if err != nil {
		return err
	}
	lineWarehouses := make([]string, 0)
	for rows.Next() {
		var lineWarehouse string
		if err = rows.Scan(&lineWarehouse); err != nil {
			rows.Close()
			return err
		}
		lineWarehouses = append(lineWarehouses, lineWarehouse)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, lineWarehouse := range lineWarehouses {
		if err = ensurePurchaseScope(ctx, q, session, branchID, lineWarehouse); err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) auditEventTx(ctx context.Context, tx pgx.Tx, session identity.Session, eventType, outboxType, entityID string, meta identity.RequestMeta, extra map[string]any) error {
	payload := map[string]any{"schema_version": 1, "entity_id": entityID}
	for k, v := range extra {
		payload[k] = v
	}
	encoded, _ := json.Marshal(payload)
	if _, err := tx.Exec(ctx, `INSERT INTO security_audit_events(id,company_id,actor_user_id,event_type,entity_type,entity_id,details,trace_id,source_ip,user_agent) VALUES($1,$2,$3,$4,'purchasing',$5,$6,$7,$8,$9)`, uuid.NewString(), session.CurrentCompanyID, session.User.ID, eventType, entityID, encoded, meta.TraceID, meta.IP, meta.UserAgent); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO outbox_events(event_id,type,schema_version,company_id,trace_id,payload) VALUES($1,$2,1,$3,$4,$5)`, uuid.NewString(), outboxType, session.CurrentCompanyID, meta.TraceID, encoded)
	return err
}

func insertPurchaseDocumentAnchorTx(ctx context.Context, tx pgx.Tx, session identity.Session, id, documentType, documentNo, branchID, warehouseID, partyID string, documentDate time.Time, dueDate *time.Time, currency, exchangeRate, notes, subtotal, discount, tax, grandTotal string) error {
	_, err := tx.Exec(ctx, `INSERT INTO documents(id,company_id,document_type_code,document_no,branch_id,warehouse_id,party_id,document_date,due_date,currency_code,exchange_rate,notes,subtotal,discount_total,tax_total,grand_total,created_by,updated_by) VALUES($1,$2,$3,$4,$5,NULLIF($6,'')::uuid,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$17)`, id, session.CurrentCompanyID, documentType, documentNo, branchID, warehouseID, partyID, documentDate, dueDate, currency, exchangeRate, notes, subtotal, discount, tax, grandTotal, session.User.ID)
	return err
}

func (s *Service) companyBaseCurrency(ctx context.Context, companyID string) (string, error) {
	var baseCurrency string
	if err := s.pool.QueryRow(ctx, `SELECT base_currency::text FROM companies WHERE id=$1`, companyID).Scan(&baseCurrency); err != nil {
		return "", err
	}
	return baseCurrency, nil
}

func (s *Service) ensureExchangeRate(ctx context.Context, session identity.Session, currency string, on time.Time, target *string) error {
	if target == nil {
		return validation("kur alanı gereklidir")
	}
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if s.rateResolver != nil {
		rate, err := s.rateResolver.ResolveRate(ctx, session.CurrentCompanyID, currency, on)
		if err != nil {
			return fmt.Errorf("%w: belge para birimi için güncel kur alınamadı: %v", ErrExchangeRateUnavailable, err)
		}
		*target = rate
	} else {
		// No resolver wired: a foreign-currency document must not silently fall
		// back to rate 1. Only the company base currency is safe without a rate.
		base, err := s.companyBaseCurrency(ctx, session.CurrentCompanyID)
		if err != nil {
			return err
		}
		if !strings.EqualFold(currency, base) {
			return fmt.Errorf("%w: belge para birimi için güncel kur alınamadı", ErrExchangeRateUnavailable)
		}
	}
	*target = strings.TrimSpace(*target)
	if *target == "" {
		*target = "1"
	}
	if !validPurchaseDecimal(*target, false) || compare(*target, "0") <= 0 {
		return validation("belge kuru geçersiz")
	}
	return nil
}

func validateOrderLine(line *PurchaseOrderLine, currency string) error {
	line.UnitCode = strings.ToUpper(strings.TrimSpace(line.UnitCode))
	line.LineType = normalizePurchaseLineType(line.LineType)
	if !validPurchaseLineType(line.LineType) || uuid.Validate(line.ProductID) != nil || strings.TrimSpace(line.ProductNameSnapshot) == "" || strings.TrimSpace(line.UnitCode) == "" || !validPurchaseDecimal(line.OrderedQuantity, false) || !validPurchaseDecimal(line.UnitPrice, true) {
		return validation("satın alma sipariş satırı geçersiz")
	}
	line.DiscountAmount = zero(line.DiscountAmount)
	if !validPurchaseDecimal(line.DiscountAmount, true) {
		return validation("satın alma sipariş satırı indirimi geçersiz")
	}
	gross := multiply(line.OrderedQuantity, line.UnitPrice)
	if compare(line.DiscountAmount, gross) > 0 {
		return validation("satın alma sipariş satırı indirimi satır tutarını aşamaz")
	}
	line.NetAmount = subtract(gross, line.DiscountAmount)
	line.Currency = currency
	return nil
}
func validateReceiptLine(line *GoodsReceiptLine) error {
	line.AcceptedQuantity = zero(line.AcceptedQuantity)
	line.DamagedQuantity = zero(line.DamagedQuantity)
	line.RejectedQuantity = zero(line.RejectedQuantity)
	line.UnitCost = strings.TrimSpace(line.UnitCost)
	line.Currency = strings.ToUpper(strings.TrimSpace(line.Currency))
	if uuid.Validate(line.ProductID) != nil || strings.TrimSpace(line.UnitCode) == "" || !validPurchaseDecimal(zero(line.AcceptedQuantity), true) || !validPurchaseDecimal(zero(line.DamagedQuantity), true) || !validPurchaseDecimal(zero(line.RejectedQuantity), true) || !validPurchaseDecimal(line.UnitCost, true) || !validCurrency(line.Currency) {
		return validation("mal kabul satırı geçersiz")
	}
	if compare(add(add(zero(line.AcceptedQuantity), zero(line.DamagedQuantity)), zero(line.RejectedQuantity)), "0") <= 0 {
		return validation("mal kabul satırı geçersiz")
	}
	return nil
}

func validateGoodsReceiptSourceShape(orderID string, lines []GoodsReceiptLine) error {
	linked := strings.TrimSpace(orderID) != ""
	for _, line := range lines {
		lineLinked := line.PurchaseOrderLineID != nil && strings.TrimSpace(*line.PurchaseOrderLineID) != ""
		if linked != lineLinked {
			if linked {
				return validation("siparişli mal kabul satırları sipariş satırına bağlanmalıdır")
			}
			return validation("sipariş satırı bağlantısı için mal kabul başlığında kaynak sipariş seçilmelidir")
		}
	}
	return nil
}
func validateInvoiceLine(line *PurchaseInvoiceLine) error {
	line.UnitCode = strings.ToUpper(strings.TrimSpace(line.UnitCode))
	line.LineType = normalizePurchaseLineType(line.LineType)
	if uuid.Validate(line.ProductID) != nil || !validPurchaseLineType(line.LineType) || strings.TrimSpace(line.DescriptionSnapshot) == "" || !validPurchaseDecimal(line.Quantity, false) || !validPurchaseDecimal(line.UnitPrice, true) {
		return validation("alış faturası satırı geçersiz")
	}
	if strings.TrimSpace(line.UnitCode) == "" {
		line.UnitCode = "ADET"
	}
	if !validPurchaseDecimal(line.GrossAmount, true) || !validPurchaseDecimal(line.DiscountAmount, true) || !validPurchaseDecimal(line.TaxBase, true) || !validPurchaseDecimal(line.TaxAmount, true) || !validPurchaseDecimal(line.WithholdingAmount, true) || !validPurchaseDecimal(line.PayableAmount, true) {
		return validation("alış faturası satır tutarları geçersiz")
	}
	if compare(line.DiscountAmount, line.GrossAmount) > 0 {
		return validation("alış faturası indirimi satır tutarını aşamaz")
	}
	expectedGross := multiply(line.Quantity, line.UnitPrice)
	if compare(expectedGross, line.GrossAmount) != 0 {
		return validation("alış faturası brüt tutarı miktar ve fiyatla eşleşmiyor")
	}
	expectedTaxBase := subtract(line.GrossAmount, line.DiscountAmount)
	if compare(expectedTaxBase, line.TaxBase) != 0 {
		return validation("alış faturası vergi matrahı indirimle eşleşmiyor")
	}
	expectedPayable := subtract(add(line.TaxBase, line.TaxAmount), line.WithholdingAmount)
	if compare(expectedPayable, "0") < 0 || compare(expectedPayable, line.PayableAmount) != 0 {
		return validation("alış faturası ödenecek tutarı satır tutarlarıyla eşleşmiyor")
	}
	return nil
}

func normalizePurchaseLineType(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	if value == "" {
		return "PRODUCT"
	}
	return value
}

func validPurchaseLineType(value string) bool {
	return value == "PRODUCT" || value == "SERVICE"
}

func validPurchaseDecimal(value string, allowZero bool) bool {
	ratio, err := parsePurchaseDecimal(value)
	if err != nil {
		return false
	}
	if !allowZero && ratio.Sign() <= 0 {
		return false
	}
	return allowZero || ratio.Sign() > 0
}

func parsePurchaseDecimal(value string) (*big.Rat, error) {
	value = strings.TrimSpace(value)
	if value == "" || !validPurchaseDecimalLiteral(value) {
		return nil, errors.New("decimal required")
	}
	unsigned := value
	if unsigned[0] == '+' || unsigned[0] == '-' {
		unsigned = unsigned[1:]
	}
	if dot := strings.IndexByte(unsigned, '.'); dot >= 0 && len(unsigned)-dot-1 > 8 {
		return nil, errors.New("decimal scale exceeded")
	}
	ratio, ok := new(big.Rat).SetString(value)
	if !ok {
		return nil, errors.New("invalid decimal")
	}
	if ratio.Sign() < 0 {
		return nil, errors.New("negative decimal")
	}
	return ratio, nil
}

func ensurePurchaseProduct(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, companyID, productID, variantID, lineType string) error {
	var kind string
	var active, variantsEnabled bool
	if err := q.QueryRow(ctx, `SELECT kind::text,is_active,variants_enabled OR EXISTS(SELECT 1 FROM product_variants pv WHERE pv.company_id=products.company_id AND pv.product_id=products.id) FROM products WHERE company_id=$1 AND id=$2`, companyID, productID).Scan(&kind, &active, &variantsEnabled); errors.Is(err, pgx.ErrNoRows) {
		return identity.ErrForbidden
	} else if err != nil {
		return err
	}
	expectedKind := "PHYSICAL"
	if lineType == "SERVICE" {
		expectedKind = "SERVICE"
	}
	if !active {
		return ErrProductInactive
	}
	if kind != expectedKind {
		return validation("ürün kartı satır türüyle eşleşmiyor veya pasif")
	}
	variantID = strings.TrimSpace(variantID)
	if lineType == "SERVICE" && variantID != "" {
		return validation("hizmet satırında varyant bulunamaz")
	}
	if lineType == "PRODUCT" && variantsEnabled && variantID == "" {
		return fmt.Errorf("%w: varyantlı ürün için varyant seçilmelidir", ErrVariantRequired)
	}
	if variantID != "" {
		if uuid.Validate(variantID) != nil {
			return validation("varyant kimliği geçersiz")
		}
		var valid bool
		if err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_variants WHERE company_id=$1 AND id=$2 AND product_id=$3 AND is_active)`, companyID, variantID, productID).Scan(&valid); err != nil {
			return err
		}
		if !valid {
			var variantExists, variantActive bool
			if err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_variants WHERE company_id=$1 AND id=$2 AND product_id=$3),COALESCE((SELECT is_active FROM product_variants WHERE company_id=$1 AND id=$2 AND product_id=$3),false)`, companyID, variantID, productID).Scan(&variantExists, &variantActive); err != nil {
				return err
			}
			if variantExists && !variantActive {
				return ErrVariantInactive
			}
			return validation("varyant ürünle eşleşmiyor")
		}
	}
	return nil
}
func validateReturnLine(line *PurchaseReturnLine, currency string) error {
	line.UnitCode = strings.ToUpper(strings.TrimSpace(line.UnitCode))
	line.Quantity = strings.TrimSpace(line.Quantity)
	// unit_cost is server-authoritative: it is overwritten from the source
	// goods receipt line in lockPurchaseReturnSourceTx. An omitted value is
	// fine; a present-but-malformed value is still a client bug and rejected.
	line.UnitCost = strings.TrimSpace(line.UnitCost)
	if line.UnitCost == "" {
		line.UnitCost = "0"
	}
	if uuid.Validate(line.ProductID) != nil || strings.TrimSpace(line.UnitCode) == "" || !validPurchaseDecimal(line.Quantity, false) || !validPurchaseDecimal(line.UnitCost, true) {
		return validation("satın alma iade satırı geçersiz")
	}
	line.Currency = currency
	return nil
}

func validatePurchaseReturnSourceShape(sourceReceiptID string, lines []PurchaseReturnLine) error {
	linked := strings.TrimSpace(sourceReceiptID) != ""
	for _, line := range lines {
		lineLinked := strings.TrimSpace(line.SourceReceiptLineID) != ""
		if linked != lineLinked {
			if linked {
				return validation("kaynak mal kabul bağlı iade satırları mal kabul satırını belirtmelidir")
			}
			return validation("mal kabul satırı bağlantısı için iade başlığında kaynak mal kabul seçilmelidir")
		}
	}
	return nil
}
func validCurrency(value string) bool {
	return len(value) == 3 && value[0] >= 'A' && value[0] <= 'Z' && value[1] >= 'A' && value[1] <= 'Z' && value[2] >= 'A' && value[2] <= 'Z'
}
func validPolicy(value string) bool   { return value == "ALLOW" || value == "WARN" || value == "BLOCK" }
func validation(message string) error { return fmt.Errorf("%w: %s", identity.ErrValidation, message) }

func reserveCommand(ctx context.Context, tx pgx.Tx, session identity.Session, meta identity.RequestMeta, command string, input any) (string, bool, error) {
	payload, err := json.Marshal(input)
	if err != nil {
		return "", false, err
	}
	reservation, err := idempotency.ReserveTx(ctx, tx, session.CurrentCompanyID, meta.IdempotencyKey, command, payload, session.User.ID, meta.TraceID)
	if err != nil {
		return "", false, err
	}
	if !reservation.Completed {
		return "", false, nil
	}
	var response struct {
		EntityID string `json:"entity_id"`
	}
	if err := json.Unmarshal(reservation.ResponseBody, &response); err != nil || uuid.Validate(response.EntityID) != nil {
		return "", false, idempotency.ErrCommandInProgress
	}
	return response.EntityID, true, nil
}

func completeCommand(ctx context.Context, tx pgx.Tx, session identity.Session, meta identity.RequestMeta, entityID string) error {
	return idempotency.CompleteTx(ctx, tx, session.CurrentCompanyID, meta.IdempotencyKey, 201, map[string]string{"entity_id": entityID})
}

func zero(value string) string {
	if strings.TrimSpace(value) == "" {
		return "0"
	}
	return value
}
func jsonObject(value map[string]any) []byte {
	if value == nil {
		return []byte(`{}`)
	}
	encoded, _ := json.Marshal(value)
	return encoded
}
func jsonArray(value []any) []byte {
	if value == nil {
		return []byte(`[]`)
	}
	encoded, _ := json.Marshal(value)
	return encoded
}
func compare(left, right string) int {
	a, ea := new(big.Rat).SetString(zero(left))
	b, eb := new(big.Rat).SetString(zero(right))
	if !ea || !eb {
		return 0
	}
	return a.Cmp(b)
}
func add(left, right string) string {
	a, _ := new(big.Rat).SetString(zero(left))
	b, _ := new(big.Rat).SetString(zero(right))
	return canonical(new(big.Rat).Add(a, b))
}
func subtract(left, right string) string {
	a, _ := new(big.Rat).SetString(zero(left))
	b, _ := new(big.Rat).SetString(zero(right))
	return canonical(new(big.Rat).Sub(a, b))
}
func multiply(left, right string) string {
	a, _ := new(big.Rat).SetString(zero(left))
	b, _ := new(big.Rat).SetString(zero(right))
	return canonical(new(big.Rat).Mul(a, b))
}
func divide(left, right string) string {
	a, _ := new(big.Rat).SetString(zero(left))
	b, ok := new(big.Rat).SetString(zero(right))
	if !ok || b.Sign() == 0 {
		return "0"
	}
	return canonical(new(big.Rat).Quo(a, b))
}
func canonical(value *big.Rat) string { return value.FloatString(8) }
func lotNumber(value []any) string {
	if len(value) == 0 {
		return ""
	}
	if item, ok := value[0].(map[string]any); ok {
		if v, ok := item["lot_number"].(string); ok {
			return v
		}
	}
	return ""
}
func serialNumber(value []any) string {
	if len(value) == 0 {
		return ""
	}
	if item, ok := value[0].(map[string]any); ok {
		if v, ok := item["serial_number"].(string); ok {
			return v
		}
	}
	return ""
}
