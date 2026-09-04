package sales

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/commerce"
	"github.com/alpyxn/varyaone/internal/finance"
	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/platform/idempotency"
	"github.com/alpyxn/varyaone/internal/taxes"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// CommercialKind identifies a typed sales aggregate.  The database keeps
// each aggregate in its own table; this value is only the transport/service
// selector and is never a polymorphic persistence record.
type CommercialKind string

const (
	SalesQuote    CommercialKind = "SALES_QUOTE"
	SalesOrder    CommercialKind = "SALES_ORDER"
	SalesDispatch CommercialKind = "SALES_DISPATCH"
	SalesInvoice  CommercialKind = "SALES_INVOICE"
	SalesReturn   CommercialKind = "SALES_RETURN"
)

const (
	CommercialErrorInsufficientStock       = "INSUFFICIENT_AVAILABLE_STOCK"
	CommercialErrorOverFulfillment         = "ORDER_LINE_OVER_FULFILLMENT"
	CommercialErrorAlreadyPosted           = "DOCUMENT_ALREADY_POSTED"
	CommercialErrorNotEditable             = "DOCUMENT_NOT_EDITABLE"
	CommercialErrorPeriodLocked            = "DOCUMENT_PERIOD_LOCKED"
	CommercialErrorInvalidRelation         = "INVALID_DOCUMENT_RELATION"
	CommercialErrorInvalidPartyRole        = "INVALID_PARTY_ROLE"
	CommercialErrorWarehouseRequired       = "WAREHOUSE_REQUIRED"
	CommercialErrorWarehouseUnauthorized   = "WAREHOUSE_NOT_AUTHORIZED"
	CommercialErrorPriceRequired           = "PRICE_REQUIRED"
	CommercialErrorTaxProfileInvalid       = "TAX_PROFILE_INVALID"
	CommercialErrorPaymentUnavailable      = "PAYMENT_INTEGRATION_UNAVAILABLE"
	CommercialErrorExchangeRateUnavailable = "EXCHANGE_RATE_UNAVAILABLE"
	CommercialErrorVariantRequired         = "VARIANT_REQUIRED"
	CommercialErrorReturnReasonRequired    = "RETURN_REASON_REQUIRED"
	CommercialErrorProductInactive         = "PRODUCT_INACTIVE"
	CommercialErrorVariantInactive         = "VARIANT_INACTIVE"
	CommercialErrorPartyInactive           = "PARTY_INACTIVE"
	CommercialErrorWarehouseInactive       = "WAREHOUSE_INACTIVE"
	CommercialErrorRiskLimitExceeded       = "RISK_LIMIT_EXCEEDED"
	CommercialErrorDocumentModified        = "DOCUMENT_MODIFIED"
	CommercialErrorInvalidStateTransition  = "INVALID_STATE_TRANSITION"
	CommercialErrorCalculationChanged      = "CALCULATION_CHANGED"
	CommercialErrorSourceAlreadyConsumed   = "SOURCE_ALREADY_CONSUMED"
	CommercialErrorSourceCancelled         = "SOURCE_CANCELLED"
	CommercialErrorSourcePartyMismatch     = "SOURCE_PARTY_MISMATCH"
	CommercialErrorSourceCurrencyMismatch  = "SOURCE_CURRENCY_MISMATCH"
	CommercialErrorDocumentHasDependencies = "DOCUMENT_HAS_DEPENDENCIES"
	CommercialErrorDocumentHasNoLines      = "DOCUMENT_HAS_NO_LINES"
	CommercialErrorDuplicateDocumentNo     = "DUPLICATE_DOCUMENT_NO"
)

// CommercialError is intentionally stable at the HTTP boundary.  Field and
// line are kept here so handlers can return actionable details without
// exposing PostgreSQL or provider-specific errors.
type CommercialError struct {
	Code  string
	Field string
	Line  int
	Err   error
}

func (e *CommercialError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err == nil {
		return e.Code
	}
	return e.Code + ": " + e.Err.Error()
}

func (e *CommercialError) Unwrap() error { return e.Err }

func commercialError(code, message, field string, line int) error {
	return &CommercialError{Code: code, Err: errors.New(message), Field: field, Line: line}
}

type CommercialDiscountInput struct {
	Rate   string `json:"rate,omitempty"`
	Amount string `json:"amount,omitempty"`
}

type CommercialLineInput struct {
	ID                 string                    `json:"id,omitempty"`
	LineNo             int                       `json:"line_no,omitempty"`
	LineType           string                    `json:"line_type,omitempty"`
	ProductID          string                    `json:"product_id,omitempty"`
	VariantID          string                    `json:"variant_id,omitempty"`
	WarehouseID        string                    `json:"warehouse_id,omitempty"`
	UnitCode           string                    `json:"unit_code"`
	Quantity           string                    `json:"quantity"`
	BaseQuantity       string                    `json:"base_quantity,omitempty"`
	ConversionFactor   string                    `json:"conversion_factor,omitempty"`
	PriceSource        string                    `json:"price_source,omitempty"`
	UnitPrice          string                    `json:"unit_price"`
	PriceListSnapshot  map[string]any            `json:"price_list_snapshot,omitempty"`
	DiscountRate       string                    `json:"discount_rate,omitempty"`
	DiscountAmount     string                    `json:"discount_amount,omitempty"`
	DiscountComponents []CommercialDiscountInput `json:"discount_components,omitempty"`
	TaxRate            string                    `json:"tax_rate,omitempty"`
	TaxIncluded        bool                      `json:"tax_included,omitempty"`
	WithholdingRate    string                    `json:"withholding_rate,omitempty"`
	TaxSnapshot        map[string]any            `json:"tax_snapshot,omitempty"`
	Description        string                    `json:"description,omitempty"`
	SourceLineID       string                    `json:"source_line_id,omitempty"`
	// These fields are only set by server-side resolution
	// (resolveCommercialLineDefaults). A client cannot supply a tax
	// component list of its own; the product's directional tax profile is
	// the only source, exactly like TaxRate/WithholdingRate above.
	taxComponentsSnapshot []taxes.TaxComponent
	// These fields are only set by server-side document conversion. Client
	// payloads must not be able to forge a posted variant snapshot.
	variantCodeSnapshot       string
	variantAttributesSnapshot map[string]any
}

type CommercialAllocationInput struct {
	SourceLineID string `json:"source_line_id"`
	TargetLineID string `json:"target_line_id,omitempty"`
	Type         string `json:"type,omitempty"`
	Quantity     string `json:"quantity"`
	BaseQuantity string `json:"base_quantity,omitempty"`
}

type CommercialDocumentInput struct {
	ID                 string                      `json:"id,omitempty"`
	DocumentNo         string                      `json:"document_no,omitempty"`
	BranchID           string                      `json:"branch_id"`
	DefaultWarehouseID string                      `json:"default_warehouse_id,omitempty"`
	PartyID            string                      `json:"party_id"`
	PriceListID        string                      `json:"price_list_id,omitempty"`
	DocumentDate       time.Time                   `json:"document_date"`
	DueDate            *time.Time                  `json:"due_date,omitempty"`
	ValidUntil         *time.Time                  `json:"valid_until,omitempty"`
	CurrencyCode       string                      `json:"currency_code"`
	ExchangeRate       string                      `json:"exchange_rate,omitempty"`
	Notes              string                      `json:"notes,omitempty"`
	Reason             string                      `json:"reason,omitempty"`
	SourceKind         string                      `json:"source_kind,omitempty"`
	SourceDocumentID   string                      `json:"source_document_id,omitempty"`
	SourceDocumentIDs  []string                    `json:"source_document_ids,omitempty"`
	SalesRepUserID     string                      `json:"sales_rep_user_id,omitempty"`
	PaymentTermID      string                      `json:"payment_term_id,omitempty"`
	Lines              []CommercialLineInput       `json:"lines"`
	Allocations        []CommercialAllocationInput `json:"allocations,omitempty"`
	// preserveSnapshots is set only by an internal source-document conversion.
	// Client payloads must always go through the current server-side price and
	// tax resolver.
	preserveSnapshots bool
}

// invoicePostingDescription keeps the finance ledger description non-empty
// even when a user leaves the optional document notes blank. The document
// number gives the user a useful trace back to the source document without
// changing an explicitly entered note.
func invoicePostingDescription(documentType, documentNo, notes string) string {
	if value := strings.TrimSpace(notes); value != "" {
		return value
	}
	label := "Fatura"
	switch strings.ToUpper(strings.TrimSpace(documentType)) {
	case "SALES_INVOICE":
		label = "Satış faturası"
	case "SALES_RETURN_INVOICE":
		label = "Satış iadesi"
	}
	if number := strings.TrimSpace(documentNo); number != "" {
		return label + " " + number
	}
	return label
}

type CommercialLine struct {
	ID                 string                    `json:"id"`
	LineNo             int                       `json:"line_no"`
	LineType           string                    `json:"line_type"`
	ProductID          *string                   `json:"product_id,omitempty"`
	VariantID          *string                   `json:"variant_id,omitempty"`
	VariantCode        string                    `json:"variant_code,omitempty"`
	VariantAttributes  map[string]any            `json:"variant_attributes,omitempty"`
	WarehouseID        *string                   `json:"warehouse_id,omitempty"`
	WarehouseName      string                    `json:"warehouse_name,omitempty"`
	WarehouseCode      string                    `json:"warehouse_code,omitempty"`
	UnitCode           string                    `json:"unit_code"`
	Quantity           string                    `json:"quantity"`
	BaseQuantity       string                    `json:"base_quantity"`
	ConversionFactor   string                    `json:"conversion_factor"`
	PriceSource        string                    `json:"price_source"`
	PriceListSnapshot  map[string]any            `json:"price_list_snapshot,omitempty"`
	DiscountComponents []CommercialDiscountInput `json:"discount_components,omitempty"`
	TaxSnapshot        map[string]any            `json:"tax_snapshot,omitempty"`
	// TaxComponentsSnapshot is the per-line tax breakdown the engine produced:
	// the VAT component plus every additional tax (ÖTV, ÖİV, a company-defined
	// tax) with the base it was charged on and the amount it produced.
	TaxComponentsSnapshot            []taxes.TaxCalculationComponentResult `json:"tax_components_snapshot,omitempty"`
	Description                      string                                `json:"description"`
	SourceLineID                     *string                               `json:"source_line_id,omitempty"`
	RemainingFulfillmentQuantity     string                                `json:"remaining_fulfillment_quantity,omitempty"`
	RemainingFulfillmentBaseQuantity string                                `json:"remaining_fulfillment_base_quantity,omitempty"`
	RemainingInvoicingQuantity       string                                `json:"remaining_invoicing_quantity,omitempty"`
	RemainingInvoicingBaseQuantity   string                                `json:"remaining_invoicing_base_quantity,omitempty"`
	RemainingReturnQuantity          string                                `json:"remaining_return_quantity,omitempty"`
	RemainingReturnBaseQuantity      string                                `json:"remaining_return_base_quantity,omitempty"`
	UnitPrice                        string                                `json:"unit_price"`
	GrossAmount                      string                                `json:"gross_amount"`
	DiscountAmount                   string                                `json:"discount_amount"`
	NetAmount                        string                                `json:"net_amount"`
	TaxAmount                        string                                `json:"tax_amount"`
	WithholdingAmount                string                                `json:"withholding_amount"`
	LineTotal                        string                                `json:"line_total"`
	PayableAmount                    string                                `json:"payable_amount"`
}

type CommercialDocument struct {
	ID                   string         `json:"id"`
	CompanyID            string         `json:"company_id"`
	DocumentID           string         `json:"document_id"`
	Kind                 CommercialKind `json:"kind"`
	DocumentTypeCode     string         `json:"document_type_code"`
	DocumentNo           string         `json:"document_no"`
	BranchID             string         `json:"branch_id"`
	DefaultWarehouseID   *string        `json:"default_warehouse_id,omitempty"`
	DefaultWarehouseName string         `json:"default_warehouse_name,omitempty"`
	DefaultWarehouseCode string         `json:"default_warehouse_code,omitempty"`
	PartyID              string         `json:"party_id"`
	PartyCode            string         `json:"party_code,omitempty"`
	PartyName            string         `json:"party_name,omitempty"`
	DocumentDate         time.Time      `json:"document_date"`
	DueDate              *time.Time     `json:"due_date,omitempty"`
	ValidUntil           *time.Time     `json:"valid_until,omitempty"`
	CurrencyCode         string         `json:"currency_code"`
	ExchangeRate         string         `json:"exchange_rate"`
	Notes                string         `json:"notes"`
	Reason               string         `json:"reason,omitempty"`
	Status               string         `json:"status"`
	// Status is retained as the internal posting discriminator for old storage;
	// these axes are the public commercial state contract.
	LifecycleStatus    string                       `json:"lifecycle_status"`
	FulfillmentStatus  string                       `json:"fulfillment_status,omitempty"`
	InvoicingStatus    string                       `json:"invoicing_status,omitempty"`
	PaymentStatus      string                       `json:"payment_status,omitempty"`
	Settlement         *finance.DocumentSettlement  `json:"settlement,omitempty"`
	SourceKind         string                       `json:"source_kind"`
	SourceDocumentID   *string                      `json:"source_document_id,omitempty"`
	SourceDocumentIDs  []string                     `json:"source_document_ids,omitempty"`
	SourceDocuments    []SourceDocumentReference    `json:"source_documents"`
	RelatedDocuments   []SourceDocumentReference    `json:"related_documents,omitempty"`
	AvailableActions   CommercialActionAvailability `json:"available_actions"`
	Subtotal           string                       `json:"subtotal"`
	DiscountTotal      string                       `json:"discount_total"`
	TaxTotal           string                       `json:"tax_total"`
	WithholdingTotal   string                       `json:"withholding_total"`
	GrandTotal         string                       `json:"grand_total"`
	PayableTotal       string                       `json:"payable_total"`
	SalesRepUserID     *string                      `json:"sales_rep_user_id,omitempty"`
	PaymentTermID      *string                      `json:"payment_term_id,omitempty"`
	Risk               *RiskInfo                    `json:"risk,omitempty"`
	FinancePostingID   *string                      `json:"finance_posting_id,omitempty"`
	PostIdempotencyKey *string                      `json:"-"`
	PostedAt           *time.Time                   `json:"posted_at,omitempty"`
	FulfillmentAt      *time.Time                   `json:"fulfillment_at,omitempty"`
	CancelledAt        *time.Time                   `json:"cancelled_at,omitempty"`
	CancellationReason *string                      `json:"cancellation_reason,omitempty"`
	CreatedBy          string                       `json:"created_by"`
	UpdatedBy          string                       `json:"updated_by"`
	CreatedAt          time.Time                    `json:"created_at"`
	UpdatedAt          time.Time                    `json:"updated_at"`
	Version            int64                        `json:"version"`
	Lines              []CommercialLine             `json:"lines"`
}

// SourceDocumentReference is the read-only, company-scoped representation of
// a document relation.  Source document numbers are resolved from the
// authoritative document registry; clients cannot provide or override them.
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

// CommercialActionAvailability is the server-authoritative action matrix for
// a commercial detail. It is advisory for rendering; every command validates
// the same transition and scope again at the backend boundary.
type CommercialActionAvailability struct {
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

type CommercialListOptions struct {
	Status            string
	LifecycleStatus   string
	FulfillmentStatus string
	InvoicingStatus   string
	PaymentStatus     string
	PartyID           string
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

// commercialSortColumns whitelists the columns a list can be sorted by from the
// grid header. Anything else falls back to the default newest-first order.
var commercialSortColumns = map[string]string{
	"document_no":   "t.document_no",
	"document_date": "t.document_date",
	"party_name":    "p.display_name",
	"grand_total":   "t.grand_total",
	"payable_total": "t.payable_total",
	"tax_total":     "t.tax_total",
	"currency_code": "t.currency_code",
	"created_at":    "t.created_at",
}

// parseListSort turns a "field:dir" grid sort into a safe ORDER BY expression
// plus direction, or ("","") when the sort is absent or not allowed.
func parseListSort(sort string, columns map[string]string) (string, string) {
	sort = strings.TrimSpace(sort)
	if sort == "" {
		return "", ""
	}
	field, direction, _ := strings.Cut(sort, ":")
	expr, ok := columns[strings.TrimSpace(field)]
	if !ok {
		return "", ""
	}
	if strings.EqualFold(strings.TrimSpace(direction), "asc") {
		return expr, "ASC"
	}
	return expr, "DESC"
}

type CommercialListResult struct {
	Items      []CommercialDocument `json:"items"`
	NextCursor string               `json:"next_cursor,omitempty"`
}

type commercialSpec struct {
	kind       CommercialKind
	table      string
	lineTable  string
	typeCode   string
	readPerm   string
	managePerm string
	postPerm   string
	// draftPerm gates preparing/editing/deleting a draft. For quotes and orders
	// it equals managePerm; for the posting documents it is a separate ".draft"
	// permission so an admin can build a preparer role that cannot finalize.
	draftPerm string
}

func commercialSpecFor(kind CommercialKind) (commercialSpec, bool) {
	switch kind {
	case SalesQuote:
		return commercialSpec{kind, "sales_quotes", "sales_quote_lines", "SALES_QUOTE", "sales.quote.read", "sales.quote.manage", "sales.quote.manage", "sales.quote.manage"}, true
	case SalesOrder:
		return commercialSpec{kind, "sales_orders", "sales_order_lines", "SALES_ORDER", "sales.order.read", "sales.order.manage", "sales.order.manage", "sales.order.manage"}, true
	case SalesDispatch:
		return commercialSpec{kind, "sales_dispatches", "sales_dispatch_lines", "SALES_DELIVERY", "sales.dispatch.read", "sales.dispatch.post", "sales.dispatch.post", "sales.dispatch.draft"}, true
	case SalesInvoice:
		return commercialSpec{kind, "sales_invoices", "sales_invoice_lines", "SALES_INVOICE", "sales.invoice.read", "sales.invoice.post", "sales.invoice.post", "sales.invoice.draft"}, true
	case SalesReturn:
		return commercialSpec{kind, "sales_returns", "sales_return_lines", "SALES_RETURN_INVOICE", "sales.return.read", "sales.return.post", "sales.return.post", "sales.return.draft"}, true
	default:
		return commercialSpec{}, false
	}
}

// CommercialKindForResource is used by HTTP and UI adapters to keep route
// names and aggregate selectors in one place.
func CommercialKindForResource(resource string) (CommercialKind, bool) {
	switch strings.ToLower(strings.Trim(resource, "/")) {
	case "quotes", "teklifler":
		return SalesQuote, true
	case "orders", "siparisler":
		return SalesOrder, true
	case "dispatches", "irsaliyeler":
		return SalesDispatch, true
	case "invoices", "faturalar":
		return SalesInvoice, true
	case "returns", "iadeler":
		return SalesReturn, true
	default:
		return "", false
	}
}

type CommercialStockPostingInput struct {
	DocumentID   string
	DocumentType string
	Lines        []CommercialStockPostingLine
}

type CommercialStockPostingLine struct {
	LineID           string
	ProductID        string
	VariantID        string
	WarehouseID      string
	Quantity         string
	BaseQuantity     string
	ConversionFactor string
	UnitCode         string
	UnitCost         string
	Currency         string
}

type CommercialStockPoster interface {
	PostCommercialStockTx(context.Context, pgx.Tx, identity.Session, CommercialStockPostingInput) error
}

type CommercialStockReversalInput struct {
	DocumentID   string
	DocumentType string
	ReversalKey  string
	Reason       string
}

type CommercialStockReverser interface {
	ReverseCommercialStockTx(context.Context, pgx.Tx, identity.Session, CommercialStockReversalInput) error
}

type CommercialReservationer interface {
	ReserveSalesOrderTx(context.Context, pgx.Tx, identity.Session, SalesReservationInput) error
	ConsumeSalesOrderReservationsTx(context.Context, pgx.Tx, identity.Session, []SalesReservationConsumption) error
	ReleaseSalesOrderReservationsTx(context.Context, pgx.Tx, identity.Session, string) error
	RestoreSalesOrderReservationsTx(context.Context, pgx.Tx, identity.Session, []SalesReservationConsumption) error
}

type SalesReservationInput struct {
	OrderID string
	Lines   []SalesReservationLine
}

type SalesReservationLine struct {
	OrderLineID string
	ProductID   string
	VariantID   string
	WarehouseID string
	Quantity    string
}

type SalesReservationConsumption struct {
	OrderID     string
	OrderLineID string
	ProductID   string
	VariantID   string
	WarehouseID string
	Quantity    string
}

// RiskInfo is the customer risk/credit limit snapshot a document detail
// response can show alongside its totals.
type RiskInfo struct {
	Decision         string `json:"decision"`
	CurrentBalance   string `json:"current_balance"`
	ProjectedBalance string `json:"projected_balance"`
	CreditLimit      string `json:"credit_limit"`
	RiskLimit        string `json:"risk_limit"`
	BaseCurrency     string `json:"base_currency"`
}

// checkCommercialRiskTx evaluates a customer's projected exposure with this
// document's own payable total added, and stops a BLOCK-policy customer's
// order confirmation or invoice posting unless the actor holds
// sales.risk.override. A WARN-policy customer is never stopped here; the
// evaluation still rides along on the document response so the UI can show
// the warning without a second round trip.
func checkCommercialRiskTx(ctx context.Context, tx pgx.Tx, session identity.Session, partyID, currency, exchangeRate, payableTotal string) error {
	var baseCurrency string
	if err := tx.QueryRow(ctx, `SELECT base_currency FROM companies WHERE id=$1`, session.CurrentCompanyID).Scan(&baseCurrency); err != nil {
		return err
	}
	if strings.EqualFold(strings.TrimSpace(currency), strings.TrimSpace(baseCurrency)) {
		rateText := strings.TrimSpace(exchangeRate)
		rate, ok := new(big.Rat).SetString(rateText)
		if rateText != "" && (!ok || rate.Cmp(big.NewRat(1, 1)) != 0) {
			return fmt.Errorf("%w: şirket para biriminde kur 1 olmalıdır", identity.ErrValidation)
		}
		if rateText == "" {
			exchangeRate = "1"
		}
	}
	additionalBase, err := commercialBaseEquivalent(payableTotal, exchangeRate)
	if err != nil {
		return err
	}
	evaluation, err := commerce.EvaluateSalesRisk(ctx, tx, session.CurrentCompanyID, partyID, additionalBase)
	if err != nil {
		return err
	}
	if evaluation.Decision == commerce.RiskBlock && !session.HasPermission("sales.risk.override") {
		return commercialError(CommercialErrorRiskLimitExceeded, "müşteri risk/kredi limiti aşılıyor", "party_id", 0)
	}
	return nil
}

// commercialBaseEquivalent converts a document amount with its immutable
// document-time rate.  Rates are decimal strings, so all arithmetic stays in
// big.Rat and the finance boundary's four-place HALF-UP representation.
func commercialBaseEquivalent(amount, exchangeRate string) (string, error) {
	a, ok := new(big.Rat).SetString(strings.TrimSpace(amount))
	if !ok || a.Sign() < 0 {
		return "", fmt.Errorf("%w: ticari tutar geçersiz", identity.ErrValidation)
	}
	r, ok := new(big.Rat).SetString(strings.TrimSpace(exchangeRate))
	if !ok || r.Sign() <= 0 {
		return "", fmt.Errorf("%w: ticari kur geçersiz", identity.ErrValidation)
	}
	return new(big.Rat).Mul(a, r).FloatString(4), nil
}

func (s *Service) hasCommercialPermission(session identity.Session, permission string, manage bool) error {
	if identity.ValidateExternalActor(session) != nil {
		return identity.ErrForbidden
	}
	if session.HasPermission(permission) || session.HasPermission("commercial.document.manage") ||
		(session.HasPermission("document.create") && manage) || session.HasPermission("document.read") && !manage {
		return nil
	}
	return identity.ErrForbidden
}

// hasCommercialDraftPermission gates preparing/editing/deleting a draft: the
// spec's ".draft" permission or its ".post" permission (a poster can also
// prepare) both pass.
func (s *Service) hasCommercialDraftPermission(session identity.Session, spec commercialSpec) error {
	if identity.ValidateExternalActor(session) != nil {
		return identity.ErrForbidden
	}
	if session.HasPermission(spec.draftPerm) || session.HasPermission(spec.postPerm) ||
		session.HasPermission("commercial.document.manage") || session.HasPermission("document.create") {
		return nil
	}
	return identity.ErrForbidden
}

// hasCommercialReadPermission lets a reader, a ".draft" preparer or a ".post"
// holder read the document type.
func (s *Service) hasCommercialReadPermission(session identity.Session, spec commercialSpec) error {
	if identity.ValidateExternalActor(session) != nil {
		return identity.ErrForbidden
	}
	if session.HasPermission(spec.readPerm) || session.HasPermission(spec.draftPerm) || session.HasPermission(spec.postPerm) ||
		session.HasPermission("commercial.document.read") || session.HasPermission("commercial.document.manage") || session.HasPermission("document.read") {
		return nil
	}
	return identity.ErrForbidden
}

func (s *Service) CreateCommercialDraft(ctx context.Context, session identity.Session, kind CommercialKind, input CommercialDocumentInput, meta identity.RequestMeta) (CommercialDocument, error) {
	spec, ok := commercialSpecFor(kind)
	if !ok {
		return CommercialDocument{}, commercialError(CommercialErrorInvalidRelation, "ticari belge türü geçersiz", "kind", 0)
	}
	if err := s.hasCommercialDraftPermission(session, spec); err != nil {
		return CommercialDocument{}, err
	}
	if err := s.normalizeCommercialInput(ctx, session, spec, &input); err != nil {
		return CommercialDocument{}, err
	}
	lines, totals, err := normalizeCommercialLines(input.Lines, input.DefaultWarehouseID, input.CurrencyCode)
	if err != nil {
		return CommercialDocument{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return CommercialDocument{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	payload, marshalErr := json.Marshal(input)
	if marshalErr != nil {
		return CommercialDocument{}, marshalErr
	}
	reservation, err := idempotency.ReserveTx(ctx, tx, session.CurrentCompanyID, meta.IdempotencyKey, "commercial."+strings.ToLower(string(kind))+".create", payload, session.User.ID, meta.TraceID)
	if err != nil {
		return CommercialDocument{}, err
	}
	if reservation.Completed {
		var replay struct {
			DocumentID string `json:"document_id"`
		}
		if json.Unmarshal(reservation.ResponseBody, &replay) != nil || uuid.Validate(replay.DocumentID) != nil {
			return CommercialDocument{}, idempotency.ErrCommandInProgress
		}
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return s.GetCommercialDocument(ctx, session, kind, replay.DocumentID)
	}
	// Line IDs are target-document identities. A client may send a source line
	// ID in the create payload (the old UI did this), but reusing it would
	// collide with commercial_line_registry and make source allocations point
	// back to the source line. Keep the idempotency payload unchanged above,
	// then assign fresh target IDs before persistence and remap explicit target
	// allocations to those IDs.
	if err = assignCommercialCreateLineIDs(&input, lines); err != nil {
		return CommercialDocument{}, err
	}
	documentID := strings.TrimSpace(input.ID)
	if uuid.Validate(documentID) != nil {
		documentID = uuid.NewString()
	}
	documentNo := strings.TrimSpace(input.DocumentNo)
	if documentNo == "" {
		if err = tx.QueryRow(ctx, `SELECT allocate_commercial_document_number($1,$2,$3)`, session.CurrentCompanyID, spec.typeCode, input.DocumentDate.Year()).Scan(&documentNo); err != nil {
			return CommercialDocument{}, err
		}
	}
	if err = insertCommercialAnchorTx(ctx, tx, session, spec, documentID, documentNo, input, totals); err != nil {
		return CommercialDocument{}, mapSalesConstraint(err)
	}
	if err = insertCommercialHeaderTx(ctx, tx, session, spec, documentID, documentNo, input, totals); err != nil {
		return CommercialDocument{}, mapSalesConstraint(err)
	}
	if err = insertPartySnapshotTx(ctx, tx, session.CurrentCompanyID, documentID, input.PartyID); err != nil {
		return CommercialDocument{}, err
	}
	for index := range lines {
		line := &lines[index]
		if err = insertCommercialLineTx(ctx, tx, session.CurrentCompanyID, spec, documentID, line); err != nil {
			return CommercialDocument{}, mapSalesConstraint(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO commercial_line_registry(company_id,line_id,aggregate_type,document_id,line_no,line_type,quantity,base_quantity) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, session.CurrentCompanyID, line.ID, spec.kind, documentID, line.LineNo, line.LineType, line.Quantity, line.BaseQuantity); err != nil {
			return CommercialDocument{}, mapSalesConstraint(err)
		}
	}
	if err = insertCommercialSourcesAndAllocationsTx(ctx, tx, session.CurrentCompanyID, documentID, kind, input, lines); err != nil {
		return CommercialDocument{}, err
	}
	if err = insertCommercialStatusHistoryTx(ctx, tx, session, spec.kind, documentID, "", "DRAFT", "Taslak oluşturuldu"); err != nil {
		return CommercialDocument{}, err
	}
	if err = writeAuditAndEventTx(ctx, tx, session, "COMMERCIAL_"+string(kind)+"_CREATED", "commercial."+strings.ToLower(string(kind))+".created", string(kind), documentID, meta, map[string]any{"document_no": documentNo, "aggregate_type": kind}); err != nil {
		return CommercialDocument{}, err
	}
	if err = idempotency.CompleteTx(ctx, tx, session.CurrentCompanyID, meta.IdempotencyKey, http.StatusCreated, map[string]string{"document_id": documentID}); err != nil {
		return CommercialDocument{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return CommercialDocument{}, err
	}
	return s.GetCommercialDocument(ctx, session, kind, documentID)
}

func (s *Service) CreateSalesQuote(ctx context.Context, session identity.Session, input CommercialDocumentInput, meta identity.RequestMeta) (CommercialDocument, error) {
	return s.CreateCommercialDraft(ctx, session, SalesQuote, input, meta)
}

func (s *Service) CreateSalesOrder(ctx context.Context, session identity.Session, input CommercialDocumentInput, meta identity.RequestMeta) (CommercialDocument, error) {
	return s.CreateCommercialDraft(ctx, session, SalesOrder, input, meta)
}

func (s *Service) CreateSalesDispatch(ctx context.Context, session identity.Session, input CommercialDocumentInput, meta identity.RequestMeta) (CommercialDocument, error) {
	return s.CreateCommercialDraft(ctx, session, SalesDispatch, input, meta)
}

func (s *Service) CreateSalesInvoice(ctx context.Context, session identity.Session, input CommercialDocumentInput, meta identity.RequestMeta) (CommercialDocument, error) {
	return s.CreateCommercialDraft(ctx, session, SalesInvoice, input, meta)
}

func (s *Service) CreateSalesReturn(ctx context.Context, session identity.Session, input CommercialDocumentInput, meta identity.RequestMeta) (CommercialDocument, error) {
	return s.CreateCommercialDraft(ctx, session, SalesReturn, input, meta)
}

// ConvertCommercial creates the next typed aggregate from an existing
// document. It copies immutable snapshots and only the unallocated quantity;
// the target is still a draft and must be posted through its normal command.
func (s *Service) ConvertCommercial(ctx context.Context, session identity.Session, targetKind CommercialKind, sourceID string, expectedVersion int64, meta identity.RequestMeta, reason string) (CommercialDocument, error) {
	if expectedVersion < 1 {
		return CommercialDocument{}, commercialError(CommercialErrorNotEditable, "kaynak belge sürümü gereklidir", "version", 0)
	}
	sourceKind, ok := commercialKindFromDocumentID(ctx, s.pool, session.CurrentCompanyID, sourceID)
	if !ok {
		return CommercialDocument{}, ErrCommercialNotFound
	}
	source, err := s.GetCommercialDocument(ctx, session, sourceKind, sourceID)
	if err != nil {
		return CommercialDocument{}, err
	}
	if source.Version != expectedVersion {
		return CommercialDocument{}, identity.ErrConflict
	}
	sourceKindForTarget, valid := conversionSourceKind(targetKind, source.Kind, source.Status)
	if !valid {
		return CommercialDocument{}, commercialError(CommercialErrorInvalidRelation, "belge bu durumda dönüştürülemez", "source_document_id", 0)
	}
	lines := make([]CommercialLineInput, 0, len(source.Lines))
	allocationType := relationToAllocation(targetKind)
	for index, line := range source.Lines {
		quantity, baseQuantity, remainingErr := s.remainingCommercialLine(ctx, session.CurrentCompanyID, line, allocationType, targetKind, source.Kind)
		if remainingErr != nil {
			return CommercialDocument{}, remainingErr
		}
		if quantity == "" {
			continue
		}
		lineInput := CommercialLineInput{
			LineNo:             index + 1,
			LineType:           line.LineType,
			UnitCode:           line.UnitCode,
			Quantity:           quantity,
			BaseQuantity:       baseQuantity,
			ConversionFactor:   line.ConversionFactor,
			PriceSource:        line.PriceSource,
			PriceListSnapshot:  cloneCommercialMap(line.PriceListSnapshot),
			DiscountComponents: conversionDiscountComponents(line),
			Description:        line.Description,
			UnitPrice:          line.UnitPrice,
			TaxSnapshot:        cloneCommercialMap(line.TaxSnapshot),
			TaxRate:            commercialSnapshotString(line.TaxSnapshot, "rate"),
			TaxIncluded:        commercialSnapshotBool(line.TaxSnapshot, "included"),
			SourceLineID:       line.ID,
			// A conversion keeps the source line's tax profile, so the target
			// charges the same ÖTV-style components instead of collapsing back
			// onto the bare KDV rate.
			taxComponentsSnapshot:     componentsFromSnapshot(line.TaxComponentsSnapshot),
			WithholdingRate:           commercialSnapshotString(line.TaxSnapshot, "withholding_rate"),
			variantCodeSnapshot:       line.VariantCode,
			variantAttributesSnapshot: cloneCommercialMap(line.VariantAttributes),
		}
		if line.ProductID != nil {
			lineInput.ProductID = *line.ProductID
		}
		if line.VariantID != nil {
			lineInput.VariantID = *line.VariantID
		}
		if line.WarehouseID != nil {
			lineInput.WarehouseID = *line.WarehouseID
		}
		lines = append(lines, lineInput)
	}
	if len(lines) == 0 {
		return CommercialDocument{}, commercialError(CommercialErrorSourceAlreadyConsumed, "kaynak belgenin kullanılabilir satırı kalmadı", "source_document_id", 0)
	}
	return s.CreateCommercialDraft(ctx, session, targetKind, CommercialDocumentInput{
		BranchID:           source.BranchID,
		DefaultWarehouseID: valueOrEmptyCommercial(source.DefaultWarehouseID),
		PartyID:            source.PartyID,
		DocumentDate:       s.now(),
		DueDate:            source.DueDate,
		CurrencyCode:       source.CurrencyCode,
		ExchangeRate:       source.ExchangeRate,
		Notes:              source.Notes,
		Reason:             strings.TrimSpace(reason),
		SourceKind:         sourceKindForTarget,
		SourceDocumentID:   source.DocumentID,
		Lines:              lines,
		preserveSnapshots:  true,
	}, meta)
}

func commercialKindFromDocumentID(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, companyID, id string) (CommercialKind, bool) {
	var code string
	if err := q.QueryRow(ctx, `SELECT document_type_code FROM documents WHERE company_id=$1 AND id=$2`, companyID, id).Scan(&code); err != nil {
		return "", false
	}
	switch code {
	case "SALES_QUOTE":
		return SalesQuote, true
	case "SALES_ORDER":
		return SalesOrder, true
	case "SALES_DELIVERY":
		return SalesDispatch, true
	case "SALES_INVOICE":
		return SalesInvoice, true
	case "SALES_RETURN_INVOICE":
		return SalesReturn, true
	default:
		return "", false
	}
}

func conversionSourceKind(targetKind, sourceKind CommercialKind, sourceStatus string) (string, bool) {
	sourceStatus = strings.ToUpper(strings.TrimSpace(sourceStatus))
	switch targetKind {
	case SalesOrder:
		return "QUOTE", sourceKind == SalesQuote && sourceStatus == "ACCEPTED"
	case SalesDispatch:
		return "ORDER", sourceKind == SalesOrder && (sourceStatus == "CONFIRMED" || sourceStatus == "PARTIALLY_FULFILLED")
	case SalesInvoice:
		if sourceKind == SalesDispatch && sourceStatus == "POSTED" {
			return "DISPATCH", true
		}
		return "ORDER", sourceKind == SalesOrder && (sourceStatus == "CONFIRMED" || sourceStatus == "PARTIALLY_FULFILLED" || sourceStatus == "FULFILLED")
	case SalesReturn:
		if sourceKind == SalesInvoice && sourceStatus == "POSTED" {
			return "INVOICE", true
		}
		return "DISPATCH", sourceKind == SalesDispatch && sourceStatus == "POSTED"
	default:
		return "", false
	}
}

// salesCommercialPostsStock keeps the invoice stock rule explicit: direct
// invoices and returns own their stock effect; dispatch- and order-sourced
// invoices only document an already-existing fulfillment effect.
func salesCommercialPostsStock(kind CommercialKind, sourceKind string) bool {
	sourceKind = strings.ToUpper(strings.TrimSpace(sourceKind))
	// An order-sourced invoice only documents quantities already fulfilled by
	// a dispatch. The dispatch owns the stock effect; posting the invoice must
	// not consume a reservation or post a second stock movement.
	return kind == SalesDispatch || (kind == SalesInvoice && sourceKind != "DISPATCH" && sourceKind != "ORDER") || kind == SalesReturn
}

func (s *Service) remainingCommercialLine(ctx context.Context, companyID string, line CommercialLine, allocationType string, targetKind, sourceKind CommercialKind) (string, string, error) {
	if targetKind == SalesOrder {
		return line.Quantity, line.BaseQuantity, nil
	}
	if targetKind == SalesDispatch && sourceKind == SalesOrder && line.LineType != "PRODUCT" {
		return "", "", nil
	}
	var allocatedBase string
	if err := s.pool.QueryRow(ctx, `SELECT COALESCE(SUM(a.base_quantity),0)::text FROM commercial_line_allocations a WHERE a.company_id=$1 AND a.source_line_id=$2 AND a.allocation_type=$3 AND a.status='CONSUMED'`, companyID, line.ID, allocationType).Scan(&allocatedBase); err != nil {
		return "", "", err
	}
	remainingBase := new(big.Rat)
	base, baseOK := new(big.Rat).SetString(line.BaseQuantity)
	allocated, allocatedOK := new(big.Rat).SetString(allocatedBase)
	factor, factorOK := new(big.Rat).SetString(line.ConversionFactor)
	if !baseOK || !allocatedOK || !factorOK || factor.Sign() <= 0 {
		return "", "", commercialError(CommercialErrorInvalidRelation, "kaynak satır dönüşümü geçersiz", "source_line_id", 0)
	}
	remainingBase.Sub(base, allocated)
	if targetKind == SalesInvoice && sourceKind == SalesOrder && line.LineType == "PRODUCT" {
		var fulfilledBaseText string
		if err := s.pool.QueryRow(ctx, `SELECT COALESCE(SUM(a.base_quantity),0)::text FROM commercial_line_allocations a WHERE a.company_id=$1 AND a.source_line_id=$2 AND a.allocation_type='FULFILLMENT' AND a.status='CONSUMED'`, companyID, line.ID).Scan(&fulfilledBaseText); err != nil {
			return "", "", err
		}
		fulfilledBase, ok := new(big.Rat).SetString(fulfilledBaseText)
		if !ok {
			return "", "", commercialError(CommercialErrorInvalidRelation, "kaynak satır sevk miktarı geçersiz", "source_line_id", 0)
		}
		invoicedBase := new(big.Rat).Sub(base, remainingBase)
		remainingBase = new(big.Rat).Sub(fulfilledBase, invoicedBase)
		if remainingBase.Sign() < 0 {
			remainingBase.SetInt64(0)
		}
	}
	if remainingBase.Sign() <= 0 {
		return "", "", nil
	}
	remainingQuantity := new(big.Rat).Quo(remainingBase, factor)
	return rat8(remainingQuantity), rat8(remainingBase), nil
}

func commercialSnapshotString(snapshot map[string]any, key string) string {
	value, ok := snapshot[key]
	if !ok {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}

func commercialSnapshotBool(snapshot map[string]any, key string) bool {
	value, ok := snapshot[key].(bool)
	return ok && value
}

func (s *Service) GetCommercialDocument(ctx context.Context, session identity.Session, kind CommercialKind, id string) (CommercialDocument, error) {
	spec, ok := commercialSpecFor(kind)
	if !ok {
		return CommercialDocument{}, commercialError(CommercialErrorInvalidRelation, "ticari belge türü geçersiz", "kind", 0)
	}
	if err := s.hasCommercialReadPermission(session, spec); err != nil {
		return CommercialDocument{}, err
	}
	if uuid.Validate(strings.TrimSpace(id)) != nil {
		return CommercialDocument{}, commercialError(CommercialErrorInvalidRelation, "belge kimliği geçersiz", "id", 0)
	}
	query := fmt.Sprintf(`SELECT t.id,t.company_id,t.document_id,t.document_no,t.branch_id,t.default_warehouse_id,t.party_id,COALESCE(p.code,''),COALESCE(p.display_name,''),t.document_date,t.due_date,t.valid_until,t.currency_code,t.exchange_rate::text,t.notes,%s,t.status,t.source_kind,t.source_document_id,t.subtotal::text,t.discount_total::text,t.tax_total::text,t.withholding_total::text,t.grand_total::text,t.payable_total::text,t.sales_rep_user_id,t.payment_term_id,t.finance_posting_id,t.post_idempotency_key,t.posted_at,t.cancelled_at,t.cancellation_reason,t.created_by,t.updated_by,t.created_at,t.updated_at,t.version,COALESCE((SELECT w.name FROM warehouses w WHERE w.company_id=t.company_id AND w.id=t.default_warehouse_id),''),COALESCE((SELECT w.code FROM warehouses w WHERE w.company_id=t.company_id AND w.id=t.default_warehouse_id),'') FROM %s t JOIN parties p ON p.company_id=t.company_id AND p.id=t.party_id WHERE t.company_id=$1 AND t.id=$2`, commercialReasonExpression(spec), spec.table)
	item, err := scanCommercialHeader(txQueryRow{s.pool.QueryRow(ctx, query, session.CurrentCompanyID, id)}, spec)
	if errors.Is(err, pgx.ErrNoRows) {
		return CommercialDocument{}, ErrCommercialNotFound
	}
	if err != nil {
		return CommercialDocument{}, err
	}
	if err = ensureCommercialReadScope(ctx, s.pool, session, item.BranchID, item.DefaultWarehouseID); err != nil {
		return CommercialDocument{}, err
	}
	item.Lines, err = loadCommercialLines(ctx, s.pool, session.CurrentCompanyID, spec, item.ID)
	if err != nil {
		return CommercialDocument{}, err
	}
	if err = ensureCommercialLineReadScopes(ctx, s.pool, session, item.BranchID, item.Lines); err != nil {
		return CommercialDocument{}, err
	}
	item.SourceDocuments, err = loadCommercialSources(ctx, s.pool, session, item.ID)
	if err != nil {
		return item, err
	}
	item.RelatedDocuments, err = loadCommercialRelatedDocuments(ctx, s.pool, session, item.ID)
	if err != nil {
		return item, err
	}
	item.SourceDocumentIDs = make([]string, 0, len(item.SourceDocuments))
	for _, source := range item.SourceDocuments {
		item.SourceDocumentIDs = append(item.SourceDocumentIDs, source.ID)
	}
	if err = s.applyCommercialStatuses(ctx, &item); err != nil {
		return CommercialDocument{}, err
	}
	if err = s.applyCommercialActions(ctx, session, &item); err != nil {
		return CommercialDocument{}, err
	}
	if (kind == SalesOrder || kind == SalesInvoice) && item.Status != "CANCELLED" {
		if additionalBase, baseErr := commercialBaseEquivalent(item.PayableTotal, item.ExchangeRate); baseErr == nil {
			if evaluation, riskErr := commerce.EvaluateSalesRisk(ctx, s.pool, session.CurrentCompanyID, item.PartyID, additionalBase); riskErr == nil {
				item.Risk = &RiskInfo{
					Decision: string(evaluation.Decision), CurrentBalance: evaluation.CurrentBalance,
					ProjectedBalance: evaluation.ProjectedBalance, CreditLimit: evaluation.CreditLimit,
					RiskLimit: evaluation.RiskLimit, BaseCurrency: evaluation.BaseCurrency,
				}
			}
		}
	}
	return item, nil
}

// ErrCommercialNotFound intentionally does not distinguish a missing record
// from a company-scoped record in the HTTP layer.
var ErrCommercialNotFound = errors.New("commercial document not found")

type txQueryRow struct{ row pgx.Row }

func (q txQueryRow) Scan(dest ...any) error { return q.row.Scan(dest...) }

func escapeCommercialSearchToken(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	return strings.ReplaceAll(value, `_`, `\_`)
}

func (s *Service) ListCommercialDocuments(ctx context.Context, session identity.Session, kind CommercialKind, options CommercialListOptions) (CommercialListResult, error) {
	spec, ok := commercialSpecFor(kind)
	if !ok {
		return CommercialListResult{}, commercialError(CommercialErrorInvalidRelation, "ticari belge türü geçersiz", "kind", 0)
	}
	if err := s.hasCommercialReadPermission(session, spec); err != nil {
		return CommercialListResult{}, err
	}
	if options.Limit < 1 || options.Limit > 100 {
		options.Limit = 50
	}
	args := []any{session.CurrentCompanyID, session.User.ID}
	// A missing line warehouse is an incomplete draft, not an authorization
	// failure. Physical lines are required to have a warehouse at POST time;
	// keeping them readable here is necessary for draft recovery and for using
	// accepted quotes as order sources. An assigned warehouse remains strictly
	// company/branch/user scoped.
	query := fmt.Sprintf(`SELECT t.id,t.company_id,t.document_id,t.document_no,t.branch_id,t.default_warehouse_id,t.party_id,COALESCE(p.code,''),COALESCE(p.display_name,''),t.document_date,t.due_date,t.valid_until,t.currency_code,t.exchange_rate::text,t.notes,%s,t.status,t.source_kind,t.source_document_id,t.subtotal::text,t.discount_total::text,t.tax_total::text,t.withholding_total::text,t.grand_total::text,t.payable_total::text,t.sales_rep_user_id,t.payment_term_id,t.finance_posting_id,t.post_idempotency_key,t.posted_at,t.cancelled_at,t.cancellation_reason,t.created_by,t.updated_by,t.created_at,t.updated_at,t.version,COALESCE((SELECT w.name FROM warehouses w WHERE w.company_id=t.company_id AND w.id=t.default_warehouse_id),''),COALESCE((SELECT w.code FROM warehouses w WHERE w.company_id=t.company_id AND w.id=t.default_warehouse_id),'') FROM %s t JOIN parties p ON p.company_id=t.company_id AND p.id=t.party_id WHERE t.company_id=$1 AND EXISTS (SELECT 1 FROM branches b WHERE b.company_id=t.company_id AND b.id=t.branch_id AND b.is_active AND (NOT EXISTS (SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=t.company_id AND bs.user_id=$2) OR EXISTS (SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=t.company_id AND bs.user_id=$2 AND bs.branch_id=t.branch_id))) AND (t.default_warehouse_id IS NULL OR EXISTS (SELECT 1 FROM warehouses w WHERE w.company_id=t.company_id AND w.id=t.default_warehouse_id AND w.branch_id=t.branch_id AND w.is_active AND (NOT EXISTS (SELECT 1 FROM membership_warehouse_scopes ws WHERE ws.company_id=t.company_id AND ws.user_id=$2) OR EXISTS (SELECT 1 FROM membership_warehouse_scopes ws WHERE ws.company_id=t.company_id AND ws.user_id=$2 AND ws.warehouse_id=w.id)))) AND NOT EXISTS (SELECT 1 FROM %s l WHERE l.company_id=t.company_id AND l.document_id=t.id AND l.line_type='PRODUCT' AND (%s))`, commercialReasonExpression(spec), spec.table, spec.lineTable, commercialLineWarehouseReadPredicate())
	query += commercialReferenceListPredicateForTarget(options.ForReference, kind, options.ReferenceTarget)
	if value := strings.ToUpper(strings.TrimSpace(options.Status)); value != "" {
		args = append(args, value)
		query += fmt.Sprintf(" AND t.status=$%d", len(args))
	}
	if value := strings.TrimSpace(options.LifecycleStatus); value != "" {
		predicate, valid := commercialLifecyclePredicate(kind, value)
		if !valid {
			return CommercialListResult{}, commercialError(CommercialErrorInvalidRelation, "yaşam döngüsü durumu geçersiz", "lifecycle_status", 0)
		}
		query += " AND " + predicate
	}
	if value := strings.TrimSpace(options.FulfillmentStatus); value != "" {
		predicate, valid := commercialFulfillmentPredicate(kind, value)
		if !valid {
			return CommercialListResult{}, commercialError(CommercialErrorInvalidRelation, "karşılama durumu bu belge türünde kullanılamaz", "fulfillment_status", 0)
		}
		query += " AND " + predicate
	}
	if value := strings.TrimSpace(options.InvoicingStatus); value != "" {
		predicate, valid := commercialInvoicingPredicate(kind, value)
		if !valid {
			return CommercialListResult{}, commercialError(CommercialErrorInvalidRelation, "faturalama durumu bu belge türünde kullanılamaz", "invoicing_status", 0)
		}
		query += " AND " + predicate
	}
	if value := strings.TrimSpace(options.PaymentStatus); value != "" {
		if kind != SalesInvoice {
			return CommercialListResult{}, commercialError(CommercialErrorInvalidRelation, "ödeme durumu bu belge türünde kullanılamaz", "payment_status", 0)
		}
		predicate, valid := commercialPaymentPredicate(value)
		if !valid {
			return CommercialListResult{}, commercialError(CommercialErrorInvalidRelation, "ödeme durumu geçersiz", "payment_status", 0)
		}
		query += " AND " + predicate
	}
	if value := strings.TrimSpace(options.PartyID); value != "" {
		if uuid.Validate(value) != nil {
			return CommercialListResult{}, commercialError(CommercialErrorInvalidRelation, "cari kimliği geçersiz", "party_id", 0)
		}
		args = append(args, value)
		query += fmt.Sprintf(" AND t.party_id=$%d", len(args))
	}
	if value := strings.TrimSpace(options.BranchID); value != "" {
		if uuid.Validate(value) != nil {
			return CommercialListResult{}, commercialError(CommercialErrorInvalidRelation, "şube kimliği geçersiz", "branch_id", 0)
		}
		args = append(args, value)
		query += fmt.Sprintf(" AND t.branch_id=$%d", len(args))
	}
	if value := strings.ToUpper(strings.TrimSpace(options.CurrencyCode)); value != "" {
		if !validCommercialCurrency(value) {
			return CommercialListResult{}, commercialError(CommercialErrorInvalidRelation, "para birimi geçersiz", "currency_code", 0)
		}
		args = append(args, value)
		query += fmt.Sprintf(" AND t.currency_code=$%d", len(args))
	}
	// Every token has to land somewhere on the row, so a two-word search narrows
	// the list instead of widening it.
	for _, token := range strings.Fields(strings.TrimSpace(options.Search)) {
		args = append(args, "%"+escapeCommercialSearchToken(token)+"%")
		param := len(args)
		query += fmt.Sprintf(" AND (t.document_no ILIKE $%d ESCAPE '\\' OR t.notes ILIKE $%d ESCAPE '\\' OR p.code ILIKE $%d ESCAPE '\\' OR p.display_name ILIKE $%d ESCAPE '\\')", param, param, param, param)
	}
	if options.From != nil {
		args = append(args, *options.From)
		query += fmt.Sprintf(" AND t.document_date >= $%d", len(args))
	}
	if options.To != nil {
		args = append(args, *options.To)
		query += fmt.Sprintf(" AND t.document_date <= $%d", len(args))
	}
	sortExpr, sortDir := parseListSort(options.Sort, commercialSortColumns)
	offset := 0
	if sortExpr != "" {
		// A user-chosen sort has no stable keyset; use bounded offset paging.
		if options.Cursor != "" {
			parsed, convErr := strconv.Atoi(strings.TrimSpace(options.Cursor))
			if convErr != nil || parsed < 0 || parsed > 20000 {
				return CommercialListResult{}, commercialError(CommercialErrorInvalidRelation, "liste imleci geçersiz", "cursor", 0)
			}
			offset = parsed
		}
		args = append(args, options.Limit+1)
		query += fmt.Sprintf(" ORDER BY %s %s NULLS LAST,t.created_at DESC,t.id DESC LIMIT $%d OFFSET %d", sortExpr, sortDir, len(args), offset)
	} else {
		if options.Cursor != "" {
			parts := strings.SplitN(options.Cursor, "|", 2)
			if len(parts) != 2 || uuid.Validate(parts[1]) != nil {
				return CommercialListResult{}, commercialError(CommercialErrorInvalidRelation, "liste imleci geçersiz", "cursor", 0)
			}
			cursorCreatedAt, parseErr := time.Parse(time.RFC3339Nano, parts[0])
			if parseErr != nil {
				return CommercialListResult{}, commercialError(CommercialErrorInvalidRelation, "liste imleci geçersiz", "cursor", 0)
			}
			args = append(args, cursorCreatedAt, parts[1])
			query += fmt.Sprintf(" AND (t.created_at,t.id) < ($%d,$%d)", len(args)-1, len(args))
		}
		args = append(args, options.Limit+1)
		// Newest first: the document a user just created is at the top of the list.
		query += fmt.Sprintf(" ORDER BY t.created_at DESC,t.id DESC LIMIT $%d", len(args))
	}
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return CommercialListResult{}, err
	}
	result := CommercialListResult{Items: []CommercialDocument{}}
	for rows.Next() {
		item, scanErr := scanCommercialHeader(rows, spec)
		if scanErr != nil {
			rows.Close()
			return CommercialListResult{}, scanErr
		}
		result.Items = append(result.Items, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return CommercialListResult{}, err
	}
	rows.Close()
	for index := range result.Items {
		if err = s.applyCommercialListStatuses(ctx, &result.Items[index]); err != nil {
			return CommercialListResult{}, err
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

func commercialReferenceListPredicate(forReference bool) string {
	if !forReference {
		return ""
	}
	return " AND t.status <> 'DRAFT'"
}

func commercialReferenceListPredicateForTarget(forReference bool, kind CommercialKind, target string) string {
	if !forReference {
		return ""
	}
	switch kind {
	case SalesQuote:
		return " AND t.status = 'ACCEPTED'" + commercialRemainingSourceClause("SALES_QUOTE", "FULFILLMENT")
	case SalesOrder:
		if strings.EqualFold(strings.TrimSpace(target), "invoices") {
			return " AND t.status IN ('CONFIRMED','PARTIALLY_FULFILLED','FULFILLED')" + commercialOrderInvoiceRemainingClause()
		}
		return " AND t.status IN ('CONFIRMED','PARTIALLY_FULFILLED')" + commercialRemainingSourceClauseForLine("SALES_ORDER", "FULFILLMENT", "PRODUCT")
	case SalesDispatch:
		return " AND t.status = 'POSTED'" + commercialDispatchInvoiceRemainingClause()
	case SalesInvoice:
		return " AND t.status = 'POSTED'" + commercialRemainingSourceClause("SALES_INVOICE", "RETURN")
	case SalesReturn:
		return " AND t.status = 'POSTED'"
	default:
		return commercialReferenceListPredicate(true)
	}
}

// commercialLineWarehouseReadPredicate hides product lines only when an
// explicitly assigned warehouse is outside the caller's scope. A nil
// warehouse remains readable because warehouse presence is enforced when a
// stock-affecting document is posted, not when a draft is recovered or used
// as a quote source.
func commercialLineWarehouseReadPredicate() string {
	return `l.warehouse_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM warehouses lw WHERE lw.company_id=t.company_id AND lw.id=l.warehouse_id AND lw.branch_id=t.branch_id AND lw.is_active AND (NOT EXISTS (SELECT 1 FROM membership_warehouse_scopes lws WHERE lws.company_id=t.company_id AND lws.user_id=$2) OR EXISTS (SELECT 1 FROM membership_warehouse_scopes lws WHERE lws.company_id=t.company_id AND lws.user_id=$2 AND lws.warehouse_id=lw.id)))`
}

func commercialRemainingSourceClause(aggregateType, allocationType string) string {
	return fmt.Sprintf(" AND EXISTS (SELECT 1 FROM commercial_line_registry sr WHERE sr.company_id=t.company_id AND sr.document_id=t.id AND sr.aggregate_type='%s' AND sr.base_quantity > COALESCE((SELECT SUM(a.base_quantity) FROM commercial_line_allocations a WHERE a.company_id=sr.company_id AND a.source_line_id=sr.line_id AND a.allocation_type='%s' AND a.status='CONSUMED'),0))", aggregateType, allocationType)
}

func commercialRemainingSourceClauseForLine(aggregateType, allocationType, lineType string) string {
	return fmt.Sprintf(" AND EXISTS (SELECT 1 FROM commercial_line_registry sr WHERE sr.company_id=t.company_id AND sr.document_id=t.id AND sr.aggregate_type='%s' AND sr.line_type='%s' AND sr.base_quantity > COALESCE((SELECT SUM(a.base_quantity) FROM commercial_line_allocations a WHERE a.company_id=sr.company_id AND a.source_line_id=sr.line_id AND a.allocation_type='%s' AND a.status='CONSUMED'),0))", aggregateType, lineType, allocationType)
}

func commercialOrderInvoiceRemainingClause() string {
	// A product line's invoiced total must include invoices sourced from a
	// dispatch of that line, not just invoices sourced from the order
	// directly -- see loadCommercialLines for why.
	invoicedViaAnySource := "(COALESCE((SELECT SUM(a.base_quantity) FROM commercial_line_allocations a WHERE a.company_id=sr.company_id AND a.source_line_id=sr.line_id AND a.allocation_type='INVOICING' AND a.status='CONSUMED'),0)+COALESCE((SELECT SUM(a2.base_quantity) FROM commercial_line_allocations a2 JOIN commercial_line_allocations f ON f.company_id=a2.company_id AND f.target_line_id=a2.source_line_id AND f.allocation_type='FULFILLMENT' AND f.status='CONSUMED' WHERE f.company_id=sr.company_id AND f.source_line_id=sr.line_id AND a2.allocation_type='INVOICING' AND a2.status='CONSUMED'),0))"
	return " AND EXISTS (SELECT 1 FROM commercial_line_registry sr WHERE sr.company_id=t.company_id AND sr.document_id=t.id AND sr.aggregate_type='SALES_ORDER' AND ((sr.line_type='PRODUCT' AND COALESCE((SELECT SUM(a.base_quantity) FROM commercial_line_allocations a WHERE a.company_id=sr.company_id AND a.source_line_id=sr.line_id AND a.allocation_type='FULFILLMENT' AND a.status='CONSUMED'),0)>" + invoicedViaAnySource + ") OR (sr.line_type='SERVICE' AND sr.base_quantity>COALESCE((SELECT SUM(a.base_quantity) FROM commercial_line_allocations a WHERE a.company_id=sr.company_id AND a.source_line_id=sr.line_id AND a.allocation_type='INVOICING' AND a.status='CONSUMED'),0))))"
}

func commercialDispatchInvoiceRemainingClause() string {
	// A dispatch line's invoiced total must also count an invoice sourced
	// directly from the order line it fulfilled (dl.source_line_id) -- the
	// same shared ledger as commercialOrderInvoiceRemainingClause, just
	// resolved the other way around. Without this, a dispatch whose order
	// line was invoiced directly from the order still offers itself as an
	// invoice source for the same shipped quantity.
	invoicedViaAnySource := "(COALESCE((SELECT SUM(a.base_quantity) FROM commercial_line_allocations a WHERE a.company_id=sr.company_id AND a.source_line_id=dl.source_line_id AND a.allocation_type='INVOICING' AND a.status='CONSUMED'),0)+COALESCE((SELECT SUM(a2.base_quantity) FROM commercial_line_allocations a2 JOIN commercial_line_allocations f ON f.company_id=a2.company_id AND f.target_line_id=a2.source_line_id AND f.allocation_type='FULFILLMENT' AND f.status='CONSUMED' WHERE f.company_id=sr.company_id AND f.source_line_id=dl.source_line_id AND a2.allocation_type='INVOICING' AND a2.status='CONSUMED'),0))"
	return " AND EXISTS (SELECT 1 FROM commercial_line_registry sr JOIN sales_dispatch_lines dl ON dl.company_id=sr.company_id AND dl.id=sr.line_id WHERE sr.company_id=t.company_id AND sr.document_id=t.id AND sr.aggregate_type='SALES_DISPATCH' AND (dl.source_line_id IS NULL AND sr.base_quantity>COALESCE((SELECT SUM(a.base_quantity) FROM commercial_line_allocations a WHERE a.company_id=sr.company_id AND a.source_line_id=sr.line_id AND a.allocation_type='INVOICING' AND a.status='CONSUMED'),0) OR dl.source_line_id IS NOT NULL AND sr.base_quantity>" + invoicedViaAnySource + "))"
}

func (s *Service) UpdateCommercialDraft(ctx context.Context, session identity.Session, kind CommercialKind, id string, expectedVersion int64, input CommercialDocumentInput, meta identity.RequestMeta) (CommercialDocument, error) {
	spec, ok := commercialSpecFor(kind)
	if !ok {
		return CommercialDocument{}, commercialError(CommercialErrorInvalidRelation, "ticari belge türü geçersiz", "kind", 0)
	}
	if err := s.hasCommercialDraftPermission(session, spec); err != nil {
		return CommercialDocument{}, err
	}
	if expectedVersion < 1 {
		return CommercialDocument{}, commercialError(CommercialErrorNotEditable, "taslak sürümü gereklidir", "version", 0)
	}
	input.ID = id
	if err := s.normalizeCommercialInput(ctx, session, spec, &input); err != nil {
		return CommercialDocument{}, err
	}
	lines, totals, err := normalizeCommercialLines(input.Lines, input.DefaultWarehouseID, input.CurrencyCode)
	if err != nil {
		return CommercialDocument{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return CommercialDocument{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	payload, marshalErr := json.Marshal(input)
	if marshalErr != nil {
		return CommercialDocument{}, marshalErr
	}
	reservation, err := idempotency.ReserveTx(ctx, tx, session.CurrentCompanyID, meta.IdempotencyKey, "commercial."+strings.ToLower(string(kind))+".update", payload, session.User.ID, meta.TraceID)
	if err != nil {
		return CommercialDocument{}, err
	}
	if reservation.Completed {
		var replay struct {
			DocumentID string `json:"document_id"`
		}
		if json.Unmarshal(reservation.ResponseBody, &replay) != nil || uuid.Validate(replay.DocumentID) != nil {
			return CommercialDocument{}, idempotency.ErrCommandInProgress
		}
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return s.GetCommercialDocument(ctx, session, kind, replay.DocumentID)
	}
	var currentStatus, anchorID, currentDocumentNo string
	if err = tx.QueryRow(ctx, fmt.Sprintf(`SELECT status,document_id,document_no FROM %s WHERE company_id=$1 AND id=$2 AND version=$3 FOR UPDATE`, spec.table), session.CurrentCompanyID, id, expectedVersion).Scan(&currentStatus, &anchorID, &currentDocumentNo); errors.Is(err, pgx.ErrNoRows) {
		return CommercialDocument{}, identity.ErrConflict
	} else if err != nil {
		return CommercialDocument{}, err
	}
	if currentStatus != "DRAFT" {
		return CommercialDocument{}, commercialError(CommercialErrorNotEditable, "yalnız taslak belge düzenlenebilir", "status", 0)
	}
	currentItem, loadErr := loadCommercialHeaderTx(ctx, tx, spec, session.CurrentCompanyID, id, true)
	if loadErr != nil {
		return CommercialDocument{}, loadErr
	}
	currentItem.Lines, loadErr = loadCommercialLines(ctx, tx, session.CurrentCompanyID, spec, id)
	if loadErr != nil {
		return CommercialDocument{}, loadErr
	}
	// The client sends only the variant ID. Preserve the existing draft's
	// server-owned snapshot when the same variant remains on the line; a draft
	// edit must not silently replace its displayed variant identity because a
	// catalog row changed while the editor was open.
	previousVariantSnapshots := make(map[string]CommercialLine, len(currentItem.Lines))
	for _, previousLine := range currentItem.Lines {
		if previousLine.VariantID != nil && strings.TrimSpace(*previousLine.VariantID) != "" {
			previousVariantSnapshots[previousLine.ID] = previousLine
		}
	}
	for index := range lines {
		previous, ok := previousVariantSnapshots[lines[index].ID]
		if !ok || previous.VariantID == nil || lines[index].VariantID == nil || *previous.VariantID != *lines[index].VariantID {
			continue
		}
		lines[index].VariantCode = previous.VariantCode
		lines[index].VariantAttributes = cloneCommercialMap(previous.VariantAttributes)
	}
	if loadErr = ensureCommercialScope(ctx, tx, session, currentItem.BranchID, currentItem.DefaultWarehouseID); loadErr != nil {
		return CommercialDocument{}, loadErr
	}
	if loadErr = ensureCommercialLineScopes(ctx, tx, session, currentItem.BranchID, currentItem.Lines); loadErr != nil {
		return CommercialDocument{}, loadErr
	}
	if strings.TrimSpace(input.DocumentNo) == "" {
		input.DocumentNo = currentDocumentNo
	}
	updateQuery := fmt.Sprintf(`UPDATE %s SET document_no=COALESCE(NULLIF($1,''),document_no),branch_id=$2,default_warehouse_id=NULLIF($3,'')::uuid,party_id=$4,document_date=$5,due_date=$6,valid_until=$7,currency_code=$8,exchange_rate=$9,notes=$10,source_kind=$11,source_document_id=NULLIF($12,'')::uuid,subtotal=$13,discount_total=$14,tax_total=$15,withholding_total=$16,grand_total=$17,payable_total=$18,sales_rep_user_id=NULLIF($19,'')::uuid,payment_term_id=NULLIF($20,'')::uuid,updated_by=$21,updated_at=now(),version=version+1 WHERE company_id=$22 AND id=$23 AND status='DRAFT' AND version=$24`, spec.table)
	updateArgs := []any{input.DocumentNo, input.BranchID, input.DefaultWarehouseID, input.PartyID, input.DocumentDate, input.DueDate, input.ValidUntil, input.CurrencyCode, input.ExchangeRate, input.Notes, input.SourceKind, input.SourceDocumentID, totals.Subtotal, totals.DiscountTotal, totals.TaxTotal, totals.WithholdingTotal, totals.GrandTotal, totals.PayableTotal, input.SalesRepUserID, input.PaymentTermID, session.User.ID, session.CurrentCompanyID, id, expectedVersion}
	if spec.kind == SalesReturn {
		updateQuery = fmt.Sprintf(`UPDATE %s SET document_no=COALESCE(NULLIF($1,''),document_no),branch_id=$2,default_warehouse_id=NULLIF($3,'')::uuid,party_id=$4,document_date=$5,due_date=$6,valid_until=$7,currency_code=$8,exchange_rate=$9,notes=$10,reason=$11,source_kind=$12,source_document_id=NULLIF($13,'')::uuid,subtotal=$14,discount_total=$15,tax_total=$16,withholding_total=$17,grand_total=$18,payable_total=$19,sales_rep_user_id=NULLIF($20,'')::uuid,payment_term_id=NULLIF($21,'')::uuid,updated_by=$22,updated_at=now(),version=version+1 WHERE company_id=$23 AND id=$24 AND status='DRAFT' AND version=$25`, spec.table)
		updateArgs = []any{input.DocumentNo, input.BranchID, input.DefaultWarehouseID, input.PartyID, input.DocumentDate, input.DueDate, input.ValidUntil, input.CurrencyCode, input.ExchangeRate, input.Notes, input.Reason, input.SourceKind, input.SourceDocumentID, totals.Subtotal, totals.DiscountTotal, totals.TaxTotal, totals.WithholdingTotal, totals.GrandTotal, totals.PayableTotal, input.SalesRepUserID, input.PaymentTermID, session.User.ID, session.CurrentCompanyID, id, expectedVersion}
	}
	if _, err = tx.Exec(ctx, updateQuery, updateArgs...); err != nil {
		return CommercialDocument{}, mapSalesConstraint(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE documents SET document_no=$1,branch_id=$2,warehouse_id=NULLIF($3,'')::uuid,party_id=$4,document_date=$5,due_date=$6,currency_code=$7,exchange_rate=$8,notes=$9,subtotal=$10,discount_total=$11,tax_total=$12,grand_total=$13,updated_by=$14,updated_at=now(),version=version+1 WHERE company_id=$15 AND id=$16 AND status='DRAFT'`, input.DocumentNo, input.BranchID, input.DefaultWarehouseID, input.PartyID, input.DocumentDate, input.DueDate, input.CurrencyCode, input.ExchangeRate, input.Notes, totals.Subtotal, totals.DiscountTotal, totals.TaxTotal, totals.GrandTotal, session.User.ID, session.CurrentCompanyID, anchorID); err != nil {
		return CommercialDocument{}, mapSalesConstraint(err)
	}
	if _, err = tx.Exec(ctx, `DELETE FROM commercial_line_allocations WHERE company_id=$1 AND target_line_id IN (SELECT id FROM `+spec.lineTable+` WHERE company_id=$1 AND document_id=$2)`, session.CurrentCompanyID, id); err != nil {
		return CommercialDocument{}, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM `+spec.lineTable+` WHERE company_id=$1 AND document_id=$2`, session.CurrentCompanyID, id); err != nil {
		return CommercialDocument{}, mapSalesConstraint(err)
	}
	if _, err = tx.Exec(ctx, `DELETE FROM commercial_line_registry WHERE company_id=$1 AND document_id=$2`, session.CurrentCompanyID, id); err != nil {
		return CommercialDocument{}, err
	}
	for index := range lines {
		line := &lines[index]
		if err = insertCommercialLineTx(ctx, tx, session.CurrentCompanyID, spec, id, line); err != nil {
			return CommercialDocument{}, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO commercial_line_registry(company_id,line_id,aggregate_type,document_id,line_no,line_type,quantity,base_quantity) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, session.CurrentCompanyID, line.ID, spec.kind, id, line.LineNo, line.LineType, line.Quantity, line.BaseQuantity); err != nil {
			return CommercialDocument{}, mapSalesConstraint(err)
		}
	}
	if _, err = tx.Exec(ctx, `DELETE FROM commercial_document_sources WHERE company_id=$1 AND document_id=$2`, session.CurrentCompanyID, id); err != nil {
		return CommercialDocument{}, err
	}
	if err = insertCommercialSourcesAndAllocationsTx(ctx, tx, session.CurrentCompanyID, id, kind, input, lines); err != nil {
		return CommercialDocument{}, err
	}
	if err = insertPartySnapshotTx(ctx, tx, session.CurrentCompanyID, id, input.PartyID); err != nil {
		return CommercialDocument{}, err
	}
	if err = writeAuditAndEventTx(ctx, tx, session, "COMMERCIAL_"+string(kind)+"_UPDATED", "commercial."+strings.ToLower(string(kind))+".updated", string(kind), id, meta, map[string]any{"version": expectedVersion + 1}); err != nil {
		return CommercialDocument{}, err
	}
	if err = idempotency.CompleteTx(ctx, tx, session.CurrentCompanyID, meta.IdempotencyKey, http.StatusOK, map[string]string{"document_id": id}); err != nil {
		return CommercialDocument{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return CommercialDocument{}, err
	}
	return s.GetCommercialDocument(ctx, session, kind, id)
}

func (s *Service) DeleteCommercialDraft(ctx context.Context, session identity.Session, kind CommercialKind, id string, expectedVersion int64, meta identity.RequestMeta) error {
	spec, ok := commercialSpecFor(kind)
	if !ok {
		return commercialError(CommercialErrorInvalidRelation, "ticari belge türü geçersiz", "kind", 0)
	}
	if err := s.hasCommercialDraftPermission(session, spec); err != nil {
		return err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	currentItem, err := loadCommercialHeaderTx(ctx, tx, spec, session.CurrentCompanyID, id, true)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrCommercialNotFound
	}
	if err != nil {
		return err
	}
	currentItem.Lines, err = loadCommercialLines(ctx, tx, session.CurrentCompanyID, spec, id)
	if err != nil {
		return err
	}
	if err = ensureCommercialScope(ctx, tx, session, currentItem.BranchID, currentItem.DefaultWarehouseID); err != nil {
		return err
	}
	if err = ensureCommercialLineScopes(ctx, tx, session, currentItem.BranchID, currentItem.Lines); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM commercial_line_allocations WHERE company_id=$1 AND target_line_id IN (SELECT id FROM `+spec.lineTable+` WHERE company_id=$1 AND document_id=$2)`, session.CurrentCompanyID, id); err != nil {
		return err
	}
	// The lines go first, while the header still exists and still says DRAFT.
	// Left to the header's ON DELETE CASCADE they would be removed after the
	// parent row is gone, and the line immutability trigger reads that parent's
	// status: it finds nothing and refuses the delete as if the document were
	// posted. Purchasing deletes its lines the same way.
	if _, err = tx.Exec(ctx, `DELETE FROM `+spec.lineTable+` WHERE company_id=$1 AND document_id=$2`, session.CurrentCompanyID, id); err != nil {
		return mapSalesConstraint(err)
	}
	result, err := tx.Exec(ctx, fmt.Sprintf(`DELETE FROM %s WHERE company_id=$1 AND id=$2 AND status='DRAFT' AND version=$3`, spec.table), session.CurrentCompanyID, id, expectedVersion)
	if err != nil {
		return mapSalesConstraint(err)
	}
	if result.RowsAffected() != 1 {
		return commercialError(CommercialErrorNotEditable, "yalnız taslak belge silinebilir", "status", 0)
	}
	if _, err = tx.Exec(ctx, `DELETE FROM commercial_line_registry WHERE company_id=$1 AND document_id=$2`, session.CurrentCompanyID, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM commercial_document_sources WHERE company_id=$1 AND document_id=$2`, session.CurrentCompanyID, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM commercial_party_snapshots WHERE company_id=$1 AND document_id=$2`, session.CurrentCompanyID, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM commercial_status_history WHERE company_id=$1 AND document_id=$2`, session.CurrentCompanyID, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM documents WHERE company_id=$1 AND id=$2 AND status='DRAFT'`, session.CurrentCompanyID, id); err != nil {
		return mapSalesConstraint(err)
	}
	return tx.Commit(ctx)
}

func (s *Service) TransitionCommercial(ctx context.Context, session identity.Session, kind CommercialKind, id, command string, expectedVersion int64, meta identity.RequestMeta, reason string) (CommercialDocument, error) {
	spec, ok := commercialSpecFor(kind)
	if !ok {
		return CommercialDocument{}, commercialError(CommercialErrorInvalidRelation, "ticari belge türü geçersiz", "kind", 0)
	}
	command = strings.ToLower(strings.TrimSpace(command))
	if command == "cancel" {
		return s.cancelCommercial(ctx, session, spec, id, expectedVersion, reason, meta)
	}
	if err := s.hasCommercialPermission(session, spec.postPerm, true); err != nil {
		return CommercialDocument{}, err
	}
	if expectedVersion < 1 {
		return CommercialDocument{}, commercialError(CommercialErrorNotEditable, "güncel belge sürümü gereklidir", "version", 0)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return CommercialDocument{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	commandPayload, marshalErr := json.Marshal(map[string]any{"id": id, "version": expectedVersion})
	if marshalErr != nil {
		return CommercialDocument{}, marshalErr
	}
	reservation, err := idempotency.ReserveTx(ctx, tx, session.CurrentCompanyID, meta.IdempotencyKey, "commercial."+strings.ToLower(string(kind))+"."+command, commandPayload, session.User.ID, meta.TraceID)
	if err != nil {
		return CommercialDocument{}, err
	}
	if reservation.Completed {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return s.GetCommercialDocument(ctx, session, kind, id)
	}
	item, err := loadCommercialHeaderTx(ctx, tx, spec, session.CurrentCompanyID, id, true)
	if errors.Is(err, pgx.ErrNoRows) {
		return CommercialDocument{}, ErrCommercialNotFound
	}
	if err != nil {
		return CommercialDocument{}, err
	}
	item.Lines, err = loadCommercialLines(ctx, tx, session.CurrentCompanyID, spec, item.ID)
	if err != nil {
		return CommercialDocument{}, err
	}
	if err = ensureCommercialScope(ctx, tx, session, item.BranchID, item.DefaultWarehouseID); err != nil {
		return CommercialDocument{}, err
	}
	if item.Version != expectedVersion {
		return CommercialDocument{}, identity.ErrConflict
	}
	if item.Status == "POSTED" {
		if item.PostIdempotencyKey != nil && *item.PostIdempotencyKey == meta.IdempotencyKey {
			_ = tx.Rollback(context.WithoutCancel(ctx))
			return s.GetCommercialDocument(ctx, session, kind, id)
		}
		return CommercialDocument{}, commercialError(CommercialErrorAlreadyPosted, "belge daha önce işlendi", "status", 0)
	}
	if item.Status == "CANCELLED" {
		return CommercialDocument{}, commercialError(CommercialErrorInvalidStateTransition, "iptal edilmiş belge değiştirilemez", "status", 0)
	}
	if command == "send" || command == "accept" || command == "reject" {
		if kind != SalesQuote {
			return CommercialDocument{}, commercialError(CommercialErrorInvalidRelation, "yalnız satış teklifi gönderilebilir, kabul veya reddedilebilir", "command", 0)
		}
		to, valid := quoteTransition(item.Status, command)
		if !valid {
			return CommercialDocument{}, commercialError(CommercialErrorInvalidStateTransition, "teklif durum geçişi geçersiz", "status", 0)
		}
		if err = transitionCommercialStatusTx(ctx, tx, spec, session.CurrentCompanyID, item.ID, item.Status, to, expectedVersion, "", "", ""); err != nil {
			return CommercialDocument{}, err
		}
		if err = insertCommercialStatusHistoryTx(ctx, tx, session, kind, item.ID, item.Status, to, reason); err != nil {
			return CommercialDocument{}, err
		}
		if err = writeAuditAndEventTx(ctx, tx, session, "COMMERCIAL_"+string(kind)+"_"+strings.ToUpper(command), "commercial."+strings.ToLower(string(kind))+"."+command, string(kind), id, meta, map[string]any{"from_status": item.Status, "to_status": to}); err != nil {
			return CommercialDocument{}, err
		}
		if err = completeCommercialCommand(ctx, tx, session, meta, id, "commercial."+strings.ToLower(string(kind))+"."+command, map[string]any{"id": id, "version": expectedVersion}); err != nil {
			return CommercialDocument{}, err
		}
		if err = tx.Commit(ctx); err != nil {
			return CommercialDocument{}, err
		}
		return s.GetCommercialDocument(ctx, session, kind, id)
	}

	if command != "confirm" && command != "post" {
		return CommercialDocument{}, commercialError(CommercialErrorInvalidRelation, "ticari belge işlemi geçersiz", "command", 0)
	}
	if command == "confirm" && kind != SalesOrder {
		return CommercialDocument{}, commercialError(CommercialErrorInvalidRelation, "yalnız satış siparişi onaylanabilir", "command", 0)
	}
	if command == "post" && (kind == SalesQuote || kind == SalesOrder) {
		return CommercialDocument{}, commercialError(CommercialErrorInvalidRelation, "bu belge türü post edilemez", "command", 0)
	}
	if command == "confirm" {
		if item.Status != "DRAFT" {
			return CommercialDocument{}, commercialError(CommercialErrorInvalidStateTransition, "sipariş yalnız taslaktan onaylanabilir", "status", 0)
		}
		if len(item.Lines) == 0 {
			return CommercialDocument{}, commercialError(CommercialErrorDocumentHasNoLines, "belge kesinleştirilmeden önce en az bir satır eklenmelidir", "lines", 0)
		}
	}
	if command == "post" {
		if item.Status != "DRAFT" {
			return CommercialDocument{}, commercialError(CommercialErrorInvalidStateTransition, "belge yalnız taslaktan post edilebilir", "status", 0)
		}
		if len(item.Lines) == 0 {
			return CommercialDocument{}, commercialError(CommercialErrorDocumentHasNoLines, "belge kesinleştirilmeden önce en az bir satır eklenmelidir", "lines", 0)
		}
	}
	if command == "confirm" || command == "post" {
		// Detail reads intentionally continue to show historical references to
		// masters that were later deactivated.  A state-changing command,
		// however, must revalidate current eligibility and every physical line
		// inside the same transaction as its effects.
		if err = validateCommercialPostMastersTx(ctx, tx, session, item); err != nil {
			return CommercialDocument{}, err
		}
	} else if err = ensureCommercialLineScopes(ctx, tx, session, item.BranchID, item.Lines); err != nil {
		return CommercialDocument{}, err
	}
	if command == "confirm" {
		if kind == SalesOrder {
			if riskErr := checkCommercialRiskTx(ctx, tx, session, item.PartyID, item.CurrencyCode, item.ExchangeRate, item.PayableTotal); riskErr != nil {
				return CommercialDocument{}, riskErr
			}
		}
		reservationer, available := s.stockPoster.(CommercialReservationer)
		productLines := reservationLines(item.Lines)
		if len(productLines) > 0 && !available {
			return CommercialDocument{}, commercialError(CommercialErrorInsufficientStock, "sipariş rezervasyon servisi hazır değil", "lines", 0)
		}
		if len(productLines) > 0 {
			if err = reservationer.ReserveSalesOrderTx(ctx, tx, session, SalesReservationInput{OrderID: item.ID, Lines: productLines}); err != nil {
				return CommercialDocument{}, mapCommercialError(err)
			}
		}
		if err = transitionCommercialStatusTx(ctx, tx, spec, session.CurrentCompanyID, item.ID, item.Status, "CONFIRMED", expectedVersion, "", "", ""); err != nil {
			return CommercialDocument{}, err
		}
		if err = insertCommercialStatusHistoryTx(ctx, tx, session, kind, item.ID, item.Status, "CONFIRMED", reason); err != nil {
			return CommercialDocument{}, err
		}
		if err = writeAuditAndEventTx(ctx, tx, session, "SALES_ORDER_CONFIRMED", "sales.order.confirmed", string(kind), id, meta, nil); err != nil {
			return CommercialDocument{}, err
		}
	} else {
		// Invoice risk and allocation consumption share one party lock.  Taking
		// it before source-line locks gives order confirmation and invoice post
		// a deterministic lock order under concurrent exposure checks.
		if kind == SalesInvoice {
			var lockedParty string
			if err = tx.QueryRow(ctx, `SELECT id FROM parties WHERE company_id=$1 AND id=$2 FOR UPDATE`, session.CurrentCompanyID, item.PartyID).Scan(&lockedParty); err != nil {
				return CommercialDocument{}, err
			}
		}
		if kind == SalesDispatch || kind == SalesInvoice || kind == SalesReturn {
			// Lock the source lines and promote this document's RESERVED
			// allocations to CONSUMED before validating: two concurrent posts
			// against the same source line serialize here, and the capacity
			// guard (DB trigger) re-checks the CONSUMED total on the UPDATE.
			if err = consumeCommercialAllocationsTx(ctx, tx, session.CurrentCompanyID, item.ID, kind); err != nil {
				return CommercialDocument{}, mapCommercialError(err)
			}
			if err = validateCommercialAllocationsTx(ctx, tx, session.CurrentCompanyID, item.ID, kind); err != nil {
				return CommercialDocument{}, err
			}
		}
		if kind == SalesInvoice {
			if riskErr := checkCommercialRiskTx(ctx, tx, session, item.PartyID, item.CurrencyCode, item.ExchangeRate, item.PayableTotal); riskErr != nil {
				return CommercialDocument{}, riskErr
			}
		}
		if s.finance != nil && (kind == SalesDispatch || kind == SalesInvoice || kind == SalesReturn) {
			if err = s.finance.EnsurePeriodOpenTx(ctx, tx, session.CurrentCompanyID, item.DocumentDate); err != nil {
				return CommercialDocument{}, mapCommercialError(err)
			}
		}
		if kind == SalesDispatch {
			if err = s.consumeCommercialReservationsTx(ctx, tx, session, item.ID, kind); err != nil {
				return CommercialDocument{}, mapCommercialError(err)
			}
		}
		if salesCommercialPostsStock(kind, item.SourceKind) {
			poster, available := s.stockPoster.(CommercialStockPoster)
			stockLines, stockLinesErr := s.commercialStockLinesTx(ctx, tx, session, kind, item.Lines)
			if stockLinesErr != nil {
				return CommercialDocument{}, stockLinesErr
			}
			if len(stockLines) > 0 && !available {
				return CommercialDocument{}, commercialError(CommercialErrorInsufficientStock, "stok posting servisi hazır değil", "lines", 0)
			}
			if len(stockLines) > 0 {
				if err = poster.PostCommercialStockTx(ctx, tx, session, CommercialStockPostingInput{DocumentID: item.DocumentID, DocumentType: spec.typeCode, Lines: stockLines}); err != nil {
					return CommercialDocument{}, mapCommercialError(err)
				}
			}
		}
		var postingID string
		if kind == SalesInvoice || kind == SalesReturn {
			if s.finance == nil {
				return CommercialDocument{}, commercialError(CommercialErrorPaymentUnavailable, "fatura için finans posting servisi hazır değil", "posting", 0)
			}
			financeType := spec.typeCode
			if kind == SalesReturn {
				financeType = "SALES_RETURN_INVOICE"
			}
			posting, postErr := s.finance.PostInvoiceTx(ctx, tx, session, finance.InvoicePostingInput{DocumentID: item.DocumentID, DocumentType: financeType, DocumentNo: item.DocumentNo, PartyID: item.PartyID, Currency: item.CurrencyCode, Amount: item.PayableTotal, ExchangeRate: item.ExchangeRate, DocumentDate: item.DocumentDate, DueDate: item.DueDate, Description: invoicePostingDescription(item.DocumentTypeCode, item.DocumentNo, item.Notes), IdempotencyKey: meta.IdempotencyKey})
			if postErr != nil {
				return CommercialDocument{}, mapCommercialError(postErr)
			}
			postingID = posting.ID
		}
		if err = transitionCommercialStatusTx(ctx, tx, spec, session.CurrentCompanyID, item.ID, item.Status, "POSTED", expectedVersion, meta.IdempotencyKey, postingID, ""); err != nil {
			return CommercialDocument{}, err
		}
		if err = updateCommercialAnchorStatusTx(ctx, tx, session.CurrentCompanyID, session.User.ID, item.DocumentID, "POSTED", meta.IdempotencyKey, ""); err != nil {
			return CommercialDocument{}, err
		}
		if err = insertCommercialStatusHistoryTx(ctx, tx, session, kind, item.ID, item.Status, "POSTED", reason); err != nil {
			return CommercialDocument{}, err
		}
		if err = writeAuditAndEventTx(ctx, tx, session, "COMMERCIAL_"+string(kind)+"_POSTED", "sales."+strings.ToLower(string(kind))+".posted", string(kind), id, meta, map[string]any{"posting_id": postingID}); err != nil {
			return CommercialDocument{}, err
		}
	}
	if err = idempotency.CompleteTx(ctx, tx, session.CurrentCompanyID, meta.IdempotencyKey, http.StatusOK, map[string]string{"document_id": id}); err != nil {
		return CommercialDocument{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return CommercialDocument{}, err
	}
	return s.GetCommercialDocument(ctx, session, kind, id)
}

func (s *Service) ConfirmSalesOrder(ctx context.Context, session identity.Session, id string, expectedVersion int64, meta identity.RequestMeta) (CommercialDocument, error) {
	return s.TransitionCommercial(ctx, session, SalesOrder, id, "confirm", expectedVersion, meta, "")
}

func (s *Service) PostSalesDispatch(ctx context.Context, session identity.Session, id string, expectedVersion int64, meta identity.RequestMeta) (CommercialDocument, error) {
	return s.TransitionCommercial(ctx, session, SalesDispatch, id, "post", expectedVersion, meta, "")
}

func (s *Service) PostSalesInvoice(ctx context.Context, session identity.Session, id string, expectedVersion int64, meta identity.RequestMeta) (CommercialDocument, error) {
	return s.TransitionCommercial(ctx, session, SalesInvoice, id, "post", expectedVersion, meta, "")
}

func (s *Service) PostSalesReturn(ctx context.Context, session identity.Session, id string, expectedVersion int64, meta identity.RequestMeta) (CommercialDocument, error) {
	return s.TransitionCommercial(ctx, session, SalesReturn, id, "post", expectedVersion, meta, "")
}

func (s *Service) cancelCommercial(ctx context.Context, session identity.Session, spec commercialSpec, id string, expectedVersion int64, reason string, meta identity.RequestMeta) (CommercialDocument, error) {
	if err := s.hasCommercialPermission(session, spec.postPerm, true); err != nil {
		return CommercialDocument{}, err
	}
	if strings.TrimSpace(reason) == "" {
		return CommercialDocument{}, commercialError(CommercialErrorInvalidRelation, "iptal gerekçesi gereklidir", "reason", 0)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return CommercialDocument{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	commandPayload, marshalErr := json.Marshal(map[string]any{"id": id, "version": expectedVersion, "reason": reason})
	if marshalErr != nil {
		return CommercialDocument{}, marshalErr
	}
	reservation, err := idempotency.ReserveTx(ctx, tx, session.CurrentCompanyID, meta.IdempotencyKey, "commercial."+strings.ToLower(string(spec.kind))+".cancel", commandPayload, session.User.ID, meta.TraceID)
	if err != nil {
		return CommercialDocument{}, err
	}
	if reservation.Completed {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return s.GetCommercialDocument(ctx, session, spec.kind, id)
	}
	item, err := loadCommercialHeaderTx(ctx, tx, spec, session.CurrentCompanyID, id, true)
	if errors.Is(err, pgx.ErrNoRows) {
		return CommercialDocument{}, ErrCommercialNotFound
	}
	if err != nil {
		return CommercialDocument{}, err
	}
	item.Lines, err = loadCommercialLines(ctx, tx, session.CurrentCompanyID, spec, item.ID)
	if err != nil {
		return CommercialDocument{}, err
	}
	if err = ensureCommercialScope(ctx, tx, session, item.BranchID, item.DefaultWarehouseID); err != nil {
		return CommercialDocument{}, err
	}
	if err = ensureCommercialLineScopes(ctx, tx, session, item.BranchID, item.Lines); err != nil {
		return CommercialDocument{}, err
	}
	if item.Version != expectedVersion {
		return CommercialDocument{}, identity.ErrConflict
	}
	if item.Status == "CANCELLED" {
		return CommercialDocument{}, commercialError(CommercialErrorInvalidStateTransition, "iptal edilmiş belge değiştirilemez", "status", 0)
	}
	canCancelDraftWorkflow := (spec.kind == SalesOrder && (item.Status == "CONFIRMED" || item.Status == "PARTIALLY_FULFILLED")) ||
		(spec.kind == SalesQuote && (item.Status == "SENT" || item.Status == "ACCEPTED"))
	if item.Status != "POSTED" && !canCancelDraftWorkflow {
		return CommercialDocument{}, commercialError(CommercialErrorInvalidStateTransition, "belge bu durumda iptal edilemez", "status", 0)
	}
	// A posting document that a finalized downstream document still depends on
	// cannot be cancelled directly: cancelling it would reverse a physical or
	// financial effect the downstream document is still bound to. The user must
	// resolve the most-downstream document first. Cascade cancellation is never
	// performed.
	if item.Status == "POSTED" && (spec.kind == SalesDispatch || spec.kind == SalesInvoice) {
		if err = assertNoActiveDownstreamTx(ctx, tx, session.CurrentCompanyID, spec.kind, item.ID); err != nil {
			return CommercialDocument{}, err
		}
	}
	if spec.kind == SalesOrder {
		if releaser, ok := s.stockPoster.(CommercialReservationer); ok {
			if err = releaser.ReleaseSalesOrderReservationsTx(ctx, tx, session, id); err != nil {
				return CommercialDocument{}, mapCommercialError(err)
			}
		}
	} else {
		if spec.kind == SalesInvoice || spec.kind == SalesReturn {
			if s.finance == nil {
				return CommercialDocument{}, commercialError(CommercialErrorPaymentUnavailable, "finans entegrasyonu kullanılamıyor", "finance", 0)
			}
			if _, err = s.finance.ReverseInvoiceTx(ctx, tx, session, item.DocumentID, meta.IdempotencyKey, reason); err != nil {
				return CommercialDocument{}, mapCommercialError(err)
			}
		}
		stockEffect := spec.kind == SalesDispatch || (spec.kind == SalesInvoice && item.SourceKind != "DISPATCH" && item.SourceKind != "ORDER") || spec.kind == SalesReturn
		if stockEffect {
			if reverser, ok := s.stockPoster.(CommercialStockReverser); ok {
				if err = reverser.ReverseCommercialStockTx(ctx, tx, session, CommercialStockReversalInput{DocumentID: item.DocumentID, DocumentType: spec.typeCode, ReversalKey: meta.IdempotencyKey, Reason: reason}); err != nil {
					return CommercialDocument{}, mapCommercialError(err)
				}
			} else {
				return CommercialDocument{}, commercialError(CommercialErrorInvalidRelation, "stok ters kayıt servisi hazır değil", "stock", 0)
			}
		}
		// Return the source order's fulfillment/invoicing projection to what it
		// was before this document posted: restore the reservations a dispatch
		// consumed, drop this document's allocations, and recompute the orders'
		// fulfillment status.
		if err = s.rollbackCommercialProjectionsTx(ctx, tx, session, spec, item.ID); err != nil {
			return CommercialDocument{}, mapCommercialError(err)
		}
	}
	if err = transitionCommercialStatusTx(ctx, tx, spec, session.CurrentCompanyID, id, item.Status, "CANCELLED", expectedVersion, item.PostIdempotencyKeyValue(), "", reason); err != nil {
		return CommercialDocument{}, err
	}
	if spec.kind == SalesDispatch || spec.kind == SalesInvoice || spec.kind == SalesReturn {
		if err = updateCommercialAnchorStatusTx(ctx, tx, session.CurrentCompanyID, session.User.ID, item.DocumentID, "CANCELLED", item.PostIdempotencyKeyValue(), reason); err != nil {
			return CommercialDocument{}, err
		}
	}
	if err = insertCommercialStatusHistoryTx(ctx, tx, session, spec.kind, id, item.Status, "CANCELLED", reason); err != nil {
		return CommercialDocument{}, err
	}
	if err = writeAuditAndEventTx(ctx, tx, session, "COMMERCIAL_"+string(spec.kind)+"_CANCELLED", "commercial."+strings.ToLower(string(spec.kind))+".cancelled", string(spec.kind), id, meta, map[string]any{"reason": reason}); err != nil {
		return CommercialDocument{}, err
	}
	if err = idempotency.CompleteTx(ctx, tx, session.CurrentCompanyID, meta.IdempotencyKey, http.StatusOK, map[string]string{"document_id": id}); err != nil {
		return CommercialDocument{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return CommercialDocument{}, err
	}
	return s.GetCommercialDocument(ctx, session, spec.kind, id)
}

func (d CommercialDocument) PostIdempotencyKeyValue() string {
	if d.PostIdempotencyKey == nil {
		return ""
	}
	return *d.PostIdempotencyKey
}

func (s *Service) consumeCommercialReservationsTx(ctx context.Context, tx pgx.Tx, session identity.Session, documentID string, kind CommercialKind) error {
	if kind != SalesDispatch {
		return nil
	}
	allocationType := relationToAllocation(kind)
	rows, err := tx.Query(ctx, commercialReservationConsumptionQuery(), session.CurrentCompanyID, documentID, allocationType)
	if err != nil {
		return err
	}
	defer rows.Close()
	consumptions := make([]SalesReservationConsumption, 0)
	for rows.Next() {
		var lineID, quantity, orderID, productID, variantID, warehouseID string
		if err = rows.Scan(&lineID, &quantity, &orderID, &productID, &variantID, &warehouseID); err != nil {
			return err
		}
		consumptions = append(consumptions, SalesReservationConsumption{OrderID: orderID, OrderLineID: lineID, ProductID: productID, VariantID: variantID, WarehouseID: warehouseID, Quantity: quantity})
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if len(consumptions) == 0 {
		return nil
	}
	reservationer, ok := s.stockPoster.(CommercialReservationer)
	if !ok {
		return commercialError(CommercialErrorInsufficientStock, "sipariş rezervasyon servisi hazır değil", "allocations", 0)
	}
	if err = reservationer.ConsumeSalesOrderReservationsTx(ctx, tx, session, consumptions); err != nil {
		return err
	}
	if kind == SalesDispatch {
		_, err = tx.Exec(ctx, `UPDATE sales_orders o SET status=CASE WHEN NOT EXISTS (
			SELECT 1 FROM commercial_line_registry ol
			WHERE ol.company_id=o.company_id AND ol.aggregate_type='SALES_ORDER' AND ol.document_id=o.id
			  AND ol.base_quantity > COALESCE((SELECT SUM(a.base_quantity) FROM commercial_line_allocations a WHERE a.company_id=ol.company_id AND a.source_line_id=ol.line_id AND a.allocation_type='FULFILLMENT' AND a.status='CONSUMED'),0)
		) THEN 'FULFILLED' ELSE 'PARTIALLY_FULFILLED' END,updated_at=now(),version=version+1
		WHERE o.company_id=$1 AND o.id IN (
			SELECT DISTINCT source.document_id FROM commercial_line_allocations a
			JOIN commercial_line_registry target ON target.company_id=a.company_id AND target.line_id=a.target_line_id
			JOIN commercial_line_registry source ON source.company_id=a.company_id AND source.line_id=a.source_line_id
			WHERE a.company_id=$1 AND target.document_id=$2 AND a.allocation_type='FULFILLMENT' AND a.status='CONSUMED' AND source.aggregate_type='SALES_ORDER'
		) AND o.status IN ('CONFIRMED','PARTIALLY_FULFILLED')`, session.CurrentCompanyID, documentID)
		if err != nil {
			return err
		}
	}
	return nil
}

// rollbackCommercialProjectionsTx is the mirror of the fulfillment/invoicing
// projection writes a posted dispatch/invoice/return makes: it restores any
// reservations a dispatch consumed, deletes this document's allocations, and
// recomputes the source sales orders' fulfillment status.
func (s *Service) rollbackCommercialProjectionsTx(ctx context.Context, tx pgx.Tx, session identity.Session, spec commercialSpec, documentID string) error {
	if spec.kind != SalesDispatch && spec.kind != SalesInvoice && spec.kind != SalesReturn {
		return nil
	}
	orderRows, err := tx.Query(ctx, `SELECT DISTINCT source.document_id
		FROM commercial_line_allocations a
		JOIN commercial_line_registry target ON target.company_id=a.company_id AND target.line_id=a.target_line_id
		JOIN commercial_line_registry source ON source.company_id=a.company_id AND source.line_id=a.source_line_id
		WHERE a.company_id=$1 AND target.document_id=$2 AND source.aggregate_type='SALES_ORDER'`,
		session.CurrentCompanyID, documentID)
	if err != nil {
		return err
	}
	var orderIDs []string
	for orderRows.Next() {
		var orderID string
		if err = orderRows.Scan(&orderID); err != nil {
			orderRows.Close()
			return err
		}
		orderIDs = append(orderIDs, orderID)
	}
	orderRows.Close()
	if err = orderRows.Err(); err != nil {
		return err
	}

	if spec.kind == SalesDispatch {
		reservationer, ok := s.stockPoster.(CommercialReservationer)
		if !ok {
			return commercialError(CommercialErrorInsufficientStock, "sipariş rezervasyon servisi hazır değil", "allocations", 0)
		}
		rows, qErr := tx.Query(ctx, commercialReservationConsumptionQuery(), session.CurrentCompanyID, documentID, "FULFILLMENT")
		if qErr != nil {
			return qErr
		}
		var consumptions []SalesReservationConsumption
		for rows.Next() {
			var lineID, quantity, orderID, productID, variantID, warehouseID string
			if err = rows.Scan(&lineID, &quantity, &orderID, &productID, &variantID, &warehouseID); err != nil {
				rows.Close()
				return err
			}
			consumptions = append(consumptions, SalesReservationConsumption{OrderID: orderID, OrderLineID: lineID, ProductID: productID, VariantID: variantID, WarehouseID: warehouseID, Quantity: quantity})
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return err
		}
		if len(consumptions) > 0 {
			if err = reservationer.RestoreSalesOrderReservationsTx(ctx, tx, session, consumptions); err != nil {
				return err
			}
		}
	}

	if _, err = tx.Exec(ctx, `DELETE FROM commercial_line_allocations WHERE company_id=$1 AND target_line_id IN (SELECT id FROM `+spec.lineTable+` WHERE company_id=$1 AND document_id=$2)`, session.CurrentCompanyID, documentID); err != nil {
		return err
	}

	for _, orderID := range orderIDs {
		if _, err = tx.Exec(ctx, `UPDATE sales_orders o SET status=(CASE
			WHEN NOT EXISTS (SELECT 1 FROM commercial_line_allocations a
				JOIN commercial_line_registry src ON src.company_id=a.company_id AND src.line_id=a.source_line_id
				WHERE a.company_id=o.company_id AND src.aggregate_type='SALES_ORDER' AND src.document_id=o.id AND a.allocation_type='FULFILLMENT' AND a.status='CONSUMED')
			THEN 'CONFIRMED'
			WHEN NOT EXISTS (SELECT 1 FROM commercial_line_registry ol
				WHERE ol.company_id=o.company_id AND ol.aggregate_type='SALES_ORDER' AND ol.document_id=o.id
				  AND ol.base_quantity > COALESCE((SELECT SUM(a.base_quantity) FROM commercial_line_allocations a WHERE a.company_id=ol.company_id AND a.source_line_id=ol.line_id AND a.allocation_type='FULFILLMENT' AND a.status='CONSUMED'),0))
			THEN 'FULFILLED'
			ELSE 'PARTIALLY_FULFILLED' END),updated_at=now(),version=version+1
			WHERE o.company_id=$1 AND o.id=$2 AND o.status IN ('CONFIRMED','PARTIALLY_FULFILLED','FULFILLED')`, session.CurrentCompanyID, orderID); err != nil {
			return err
		}
	}
	return nil
}

func commercialReservationConsumptionQuery() string {
	return `SELECT a.source_line_id,COALESCE(SUM(a.base_quantity),0)::text,source.document_id,ol.product_id,COALESCE(ol.variant_id::text,''),ol.warehouse_id
FROM commercial_line_allocations a
JOIN commercial_line_registry target ON target.company_id=a.company_id AND target.line_id=a.target_line_id
JOIN commercial_line_registry source ON source.company_id=a.company_id AND source.line_id=a.source_line_id
JOIN sales_order_lines ol ON ol.company_id=source.company_id AND ol.id=source.line_id AND ol.document_id=source.document_id
WHERE a.company_id=$1 AND target.document_id=$2 AND a.allocation_type=$3 AND a.status='CONSUMED' AND source.aggregate_type='SALES_ORDER' AND source.line_type='PRODUCT'
GROUP BY a.source_line_id,source.document_id,ol.product_id,ol.variant_id,ol.warehouse_id ORDER BY a.source_line_id`
}

// assertNoActiveDownstreamTx blocks cancelling a dispatch or invoice while a
// still-active (POSTED) downstream document was created from it. commercial
// document relations are recorded in commercial_document_sources with the
// downstream document as document_id and this document as source_document_id.
func assertNoActiveDownstreamTx(ctx context.Context, tx pgx.Tx, companyID string, kind CommercialKind, documentID string) error {
	rows, err := tx.Query(ctx, `SELECT d.document_no,d.document_type_code
		FROM commercial_document_sources s
		JOIN documents d ON d.company_id=s.company_id AND d.id=s.document_id
		WHERE s.company_id=$1 AND s.source_document_id=$2 AND d.status='POSTED'
		ORDER BY d.created_at,d.id`, companyID, documentID)
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		var documentNo, typeCode string
		if err = rows.Scan(&documentNo, &typeCode); err != nil {
			return err
		}
		label := "bağlı belge"
		switch typeCode {
		case "SALES_INVOICE":
			label = "fatura"
		case "SALES_RETURN_INVOICE":
			label = "iade"
		case "SALES_DELIVERY":
			label = "irsaliye"
		}
		sourceLabel := "irsaliye"
		if kind == SalesInvoice {
			sourceLabel = "fatura"
		}
		return commercialError(CommercialErrorDocumentHasDependencies,
			fmt.Sprintf("bu %s %s %s belgesinde kullanıldığı için iptal edilemez; önce bağlı belgeyi iptal edin", sourceLabel, label, documentNo),
			"status", 0)
	}
	return rows.Err()
}

// consumeCommercialAllocationsTx promotes a posting document's RESERVED source
// allocations to CONSUMED. It first locks every distinct source registry line
// FOR UPDATE in deterministic order so concurrent posts against the same source
// serialize, then revalidates that the finalized (CONSUMED) total plus this
// document's reservation still fits the source line before flipping the rows.
func consumeCommercialAllocationsTx(ctx context.Context, tx pgx.Tx, companyID, documentID string, kind CommercialKind) error {
	allocationType := relationToAllocation(kind)
	rows, err := tx.Query(ctx, `SELECT DISTINCT a.source_line_id
		FROM commercial_line_allocations a
		JOIN commercial_line_registry tr ON tr.company_id=a.company_id AND tr.line_id=a.target_line_id
		WHERE a.company_id=$1 AND tr.document_id=$2 AND a.status='RESERVED' AND a.allocation_type=$3
		ORDER BY a.source_line_id`, companyID, documentID, allocationType)
	if err != nil {
		return err
	}
	var sourceLineIDs []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		sourceLineIDs = append(sourceLineIDs, id)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, sourceLineID := range sourceLineIDs {
		var sourceBase string
		if err = tx.QueryRow(ctx, `SELECT base_quantity::text FROM commercial_line_registry WHERE company_id=$1 AND line_id=$2 FOR UPDATE`, companyID, sourceLineID).Scan(&sourceBase); err != nil {
			return err
		}
		var consumed, reserved string
		if err = tx.QueryRow(ctx, `SELECT
			COALESCE(SUM(a.base_quantity) FILTER (WHERE a.status='CONSUMED'),0)::text,
			COALESCE(SUM(a.base_quantity) FILTER (WHERE a.status='RESERVED' AND tr.document_id=$2),0)::text
			FROM commercial_line_allocations a
			LEFT JOIN commercial_line_registry tr ON tr.company_id=a.company_id AND tr.line_id=a.target_line_id
			WHERE a.company_id=$1 AND a.source_line_id=$3 AND a.allocation_type=$4`, companyID, documentID, sourceLineID, allocationType).Scan(&consumed, &reserved); err != nil {
			return err
		}
		if decimalCompareCommercial(addCommercial(consumed, reserved), sourceBase) > 0 {
			return commercialError(CommercialErrorOverFulfillment, "kaynak satırın kalan miktarı bu belge için yetersiz", "allocations", 0)
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE commercial_line_allocations a SET status='CONSUMED'
		FROM commercial_line_registry tr
		WHERE a.company_id=$1 AND tr.company_id=a.company_id AND tr.line_id=a.target_line_id
		AND tr.document_id=$2 AND a.status='RESERVED'`, companyID, documentID); err != nil {
		return mapSalesConstraint(err)
	}
	return nil
}

func quoteTransition(from, command string) (string, bool) {
	switch command {
	case "send":
		return "SENT", from == "DRAFT"
	case "accept":
		return "ACCEPTED", from == "SENT"
	case "reject":
		return "REJECTED", from == "SENT"
	default:
		return "", false
	}
}

func transitionCommercialStatusTx(ctx context.Context, tx pgx.Tx, spec commercialSpec, companyID, id, from, to string, expectedVersion int64, postKey, postingID, cancellationReason string) error {
	result, err := tx.Exec(ctx, fmt.Sprintf(`UPDATE %s SET status=$1,post_idempotency_key=COALESCE(NULLIF($2,''),post_idempotency_key),finance_posting_id=COALESCE(NULLIF($3,'')::uuid,finance_posting_id),posted_at=CASE WHEN $1='POSTED' THEN COALESCE(posted_at,now()) ELSE posted_at END,cancelled_at=CASE WHEN $1='CANCELLED' THEN now() ELSE cancelled_at END,cancellation_reason=CASE WHEN $1='CANCELLED' THEN NULLIF($8,'') ELSE cancellation_reason END,updated_at=now(),version=version+1 WHERE company_id=$4 AND id=$5 AND status=$6 AND version=$7`, spec.table), to, postKey, postingID, companyID, id, from, expectedVersion, cancellationReason)
	if err == nil && result.RowsAffected() == 0 {
		return identity.ErrConflict
	}
	return err
}

type commercialTotals struct {
	Subtotal         string
	DiscountTotal    string
	TaxTotal         string
	WithholdingTotal string
	GrandTotal       string
	PayableTotal     string
}

func (s *Service) normalizeCommercialInput(ctx context.Context, session identity.Session, spec commercialSpec, input *CommercialDocumentInput) error {
	if input == nil {
		return commercialError(CommercialErrorInvalidRelation, "belge bilgileri gereklidir", "", 0)
	}
	input.BranchID = strings.TrimSpace(input.BranchID)
	input.PartyID = strings.TrimSpace(input.PartyID)
	input.PriceListID = strings.TrimSpace(input.PriceListID)
	input.DefaultWarehouseID = strings.TrimSpace(input.DefaultWarehouseID)
	input.CurrencyCode = strings.ToUpper(strings.TrimSpace(input.CurrencyCode))
	input.ExchangeRate = strings.TrimSpace(input.ExchangeRate)
	input.Reason = strings.TrimSpace(input.Reason)
	input.SourceKind = strings.ToUpper(strings.TrimSpace(input.SourceKind))
	if input.SourceKind == "" {
		input.SourceKind = "DIRECT"
	}
	if input.CurrencyCode == "" {
		input.CurrencyCode = "TRY"
	}
	if !validCommercialCurrency(input.CurrencyCode) {
		return commercialError(CommercialErrorInvalidRelation, "para birimi geçersiz", "currency_code", 0)
	}
	if input.ExchangeRate == "" {
		input.ExchangeRate = "1"
	}
	if input.DocumentDate.IsZero() {
		input.DocumentDate = s.now()
	}
	if input.DocumentDate.IsZero() {
		return commercialError(CommercialErrorInvalidRelation, "belge tarihi gereklidir", "document_date", 0)
	}
	if !input.preserveSnapshots {
		if s.rateResolver != nil {
			rate, err := s.rateResolver.ResolveRate(ctx, session.CurrentCompanyID, input.CurrencyCode, input.DocumentDate)
			if err != nil {
				return commercialError(CommercialErrorExchangeRateUnavailable, "belge para birimi için güncel kur alınamadı", "currency_code", 0)
			}
			input.ExchangeRate = strings.TrimSpace(rate)
		} else {
			// No resolver wired: a foreign-currency document must not silently
			// fall back to rate 1. Only the company base currency is safe.
			base, err := s.companyBaseCurrency(ctx, session.CurrentCompanyID)
			if err != nil {
				return err
			}
			if !strings.EqualFold(input.CurrencyCode, base) {
				return commercialError(CommercialErrorExchangeRateUnavailable, "belge para birimi için güncel kur alınamadı", "currency_code", 0)
			}
		}
	}
	if uuid.Validate(input.BranchID) != nil {
		return commercialError(CommercialErrorInvalidRelation, "şube geçersiz", "branch_id", 0)
	}
	if uuid.Validate(input.PartyID) != nil {
		return commercialError(CommercialErrorInvalidPartyRole, "cari geçersiz", "party_id", 0)
	}
	if input.PriceListID != "" && uuid.Validate(input.PriceListID) != nil {
		return commercialError(CommercialErrorInvalidRelation, "fiyat listesi geçersiz", "price_list_id", 0)
	}
	if _, err := commercialDecimal(input.ExchangeRate, false); err != nil {
		return commercialError(CommercialErrorInvalidRelation, "kur geçersiz", "exchange_rate", 0)
	}
	if input.SourceKind != "DIRECT" && input.SourceKind != "QUOTE" && input.SourceKind != "ORDER" && input.SourceKind != "DISPATCH" && input.SourceKind != "RECEIPT" && input.SourceKind != "INVOICE" {
		return commercialError(CommercialErrorInvalidRelation, "kaynak belge türü geçersiz", "source_kind", 0)
	}
	if err := validateCommercialReturnReason(spec.kind, input.Reason); err != nil {
		return err
	}
	if err := ensureCommercialScope(ctx, s.pool, session, input.BranchID, optionalCommercialID(input.DefaultWarehouseID)); err != nil {
		if input.DefaultWarehouseID != "" {
			return &CommercialError{Code: CommercialErrorWarehouseUnauthorized, Field: "default_warehouse_id", Err: err}
		}
		return err
	}
	if input.PriceListID != "" {
		var active bool
		var priceCurrency string
		if err := s.pool.QueryRow(ctx, `SELECT is_active,currency_code FROM price_lists WHERE company_id=$1 AND id=$2`, session.CurrentCompanyID, input.PriceListID).Scan(&active, &priceCurrency); errors.Is(err, pgx.ErrNoRows) {
			return commercialError(CommercialErrorInvalidRelation, "fiyat listesi bulunamadı", "price_list_id", 0)
		} else if err != nil {
			return err
		} else if !active || strings.ToUpper(priceCurrency) != input.CurrencyCode {
			return commercialError(CommercialErrorInvalidRelation, "fiyat listesi belge para birimiyle eşleşmiyor", "price_list_id", 0)
		}
	}
	var partyActive, partyCustomer bool
	var partyPaymentTermID, partySalesRepID *string
	var partyDefaultDiscount string
	if err := s.pool.QueryRow(ctx, `SELECT is_active,is_customer,payment_term_id::text,sales_rep_user_id::text,default_discount_rate::text FROM parties WHERE company_id=$1 AND id=$2`, session.CurrentCompanyID, input.PartyID).Scan(&partyActive, &partyCustomer, &partyPaymentTermID, &partySalesRepID, &partyDefaultDiscount); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return commercialError(CommercialErrorInvalidPartyRole, "satış carisi bulunamadı", "party_id", 0)
		}
		return err
	}
	if !partyActive {
		return commercialError(CommercialErrorPartyInactive, "cari pasif olduğu için belge kaydedilemez", "party_id", 0)
	}
	if !partyCustomer {
		return commercialError(CommercialErrorInvalidPartyRole, "satış işlemi için müşteri carisi gereklidir", "party_id", 0)
	}
	// A blank sales rep or payment term always falls back to the customer
	// card's own default; an explicit input value is never overridden.
	if input.SalesRepUserID == "" && partySalesRepID != nil {
		input.SalesRepUserID = *partySalesRepID
	}
	if input.PaymentTermID == "" && partyPaymentTermID != nil {
		input.PaymentTermID = *partyPaymentTermID
	}
	// A blank due date is derived from the resolved payment term's due_days,
	// counted from the document date; an explicitly supplied due date is
	// never overridden by the term.
	if input.DueDate == nil && input.PaymentTermID != "" {
		var dueDays int
		if err := s.pool.QueryRow(ctx, `SELECT due_days FROM payment_terms WHERE company_id=$1 AND id=$2 AND is_active`, session.CurrentCompanyID, input.PaymentTermID).Scan(&dueDays); err == nil {
			derived := input.DocumentDate.AddDate(0, 0, dueDays)
			input.DueDate = &derived
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
	}
	for index := range input.Lines {
		line := &input.Lines[index]
		// A customer's own default discount is the first link in the line's
		// discount chain, exactly as if the user had typed it; it only
		// applies when the line carries no discount of its own, and a line
		// can still override or clear it explicitly.
		if line.DiscountRate == "" && line.DiscountAmount == "" && len(line.DiscountComponents) == 0 {
			if rate, rateErr := commercialDecimal(partyDefaultDiscount, true); rateErr == nil && rate.Sign() > 0 {
				line.DiscountComponents = []CommercialDiscountInput{{Rate: partyDefaultDiscount}}
			}
		}
		line.WarehouseID = strings.TrimSpace(line.WarehouseID)
		line.UnitCode = strings.ToUpper(strings.TrimSpace(line.UnitCode))
		line.LineType = strings.ToUpper(strings.TrimSpace(line.LineType))
		if line.LineType == "" {
			if strings.TrimSpace(line.ProductID) == "" {
				line.LineType = "SERVICE"
			} else {
				line.LineType = "PRODUCT"
			}
		}
		if line.LineType == "PRODUCT" {
			if line.WarehouseID == "" {
				line.WarehouseID = input.DefaultWarehouseID
			}
			if line.WarehouseID == "" {
				return commercialError(CommercialErrorWarehouseRequired, "ürün satırı için depo gereklidir", "warehouse_id", index+1)
			}
			if err := ensureCommercialScope(ctx, s.pool, session, input.BranchID, optionalCommercialID(line.WarehouseID)); err != nil {
				return &CommercialError{Code: CommercialErrorWarehouseUnauthorized, Field: "warehouse_id", Line: index + 1, Err: err}
			}
		} else if line.LineType == "SERVICE" {
			line.WarehouseID = ""
		} else {
			return commercialError(CommercialErrorInvalidRelation, "satır türü geçersiz", "line_type", index+1)
		}
		if strings.TrimSpace(line.ProductID) != "" {
			if uuid.Validate(strings.TrimSpace(line.ProductID)) != nil {
				return commercialError(CommercialErrorInvalidRelation, "ürün geçersiz", "product_id", index+1)
			}
			line.ProductID = strings.TrimSpace(line.ProductID)
			var productKind string
			var active, variantsEnabled bool
			if err := s.pool.QueryRow(ctx, `SELECT kind::text,is_active,variants_enabled OR EXISTS(SELECT 1 FROM product_variants pv WHERE pv.company_id=products.company_id AND pv.product_id=products.id) FROM products WHERE company_id=$1 AND id=$2`, session.CurrentCompanyID, line.ProductID).Scan(&productKind, &active, &variantsEnabled); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return commercialError(CommercialErrorInvalidRelation, "ürün bulunamadı", "product_id", index+1)
				}
				return err
			}
			kindMatches := (line.LineType == "PRODUCT" && productKind == "PHYSICAL") || (line.LineType == "SERVICE" && productKind == "SERVICE")
			if !active {
				return commercialError(CommercialErrorProductInactive, "ürün pasif olduğu için belge kaydedilemez", "product_id", index+1)
			}
			if !kindMatches {
				return commercialError(CommercialErrorInvalidRelation, "ürün satır türüyle eşleşmiyor", "product_id", index+1)
			}
			line.VariantID = strings.TrimSpace(line.VariantID)
			if line.LineType == "SERVICE" && line.VariantID != "" {
				return commercialError(CommercialErrorInvalidRelation, "hizmet satırında varyant bulunamaz", "variant_id", index+1)
			}
			if line.LineType == "PRODUCT" && variantsEnabled && line.VariantID == "" {
				return commercialError(CommercialErrorVariantRequired, "varyantlı ürün için varyant seçilmelidir", "variant_id", index+1)
			}
			if !input.preserveSnapshots {
				if err := s.resolveCommercialLineDefaults(ctx, session, input, line, index+1); err != nil {
					return err
				}
			}
		} else if line.LineType == "PRODUCT" {
			return commercialError(CommercialErrorInvalidRelation, "ürün satırı ürün gerektirir", "product_id", index+1)
		}
		line.VariantID = strings.TrimSpace(line.VariantID)
		if line.VariantID != "" && uuid.Validate(line.VariantID) != nil {
			return commercialError(CommercialErrorInvalidRelation, "varyant geçersiz", "variant_id", index+1)
		}
		if line.VariantID != "" {
			var variantExists, variantActive bool
			if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_variants WHERE company_id=$1 AND id=$2 AND product_id=$3),COALESCE((SELECT is_active FROM product_variants WHERE company_id=$1 AND id=$2 AND product_id=$3),false)`, session.CurrentCompanyID, line.VariantID, line.ProductID).Scan(&variantExists, &variantActive); err != nil {
				return err
			}
			if !variantExists {
				return commercialError(CommercialErrorInvalidRelation, "varyant ürünle eşleşmiyor", "variant_id", index+1)
			}
			if !variantActive {
				return commercialError(CommercialErrorVariantInactive, "varyant pasif olduğu için belge kaydedilemez", "variant_id", index+1)
			}
		}
		if line.LineNo < 1 {
			line.LineNo = index + 1
		}
	}
	for _, sourceID := range commercialSourceIDs(*input) {
		if uuid.Validate(sourceID) != nil {
			return commercialError(CommercialErrorInvalidRelation, "kaynak belge geçersiz", "source_document_id", 0)
		}
	}
	return nil
}

func validateCommercialReturnReason(kind CommercialKind, reason string) error {
	if kind == SalesReturn && strings.TrimSpace(reason) == "" {
		return commercialError(CommercialErrorReturnReasonRequired, "iade gerekçesi gereklidir", "reason", 0)
	}
	return nil
}

// resolveCommercialLineDefaults is the server-side price and tax resolver for
// typed sales lines. The resolved values are copied into the line snapshots by
// normalizeCommercialLines; the client never becomes the source of truth for
// a posted amount or tax profile. Arithmetic and catalog resolution live in
// internal/commerce so purchasing resolves prices and taxes the same way.
func (s *Service) companyBaseCurrency(ctx context.Context, companyID string) (string, error) {
	var baseCurrency string
	if err := s.pool.QueryRow(ctx, `SELECT base_currency::text FROM companies WHERE id=$1`, companyID).Scan(&baseCurrency); err != nil {
		return "", err
	}
	return baseCurrency, nil
}

func (s *Service) resolveCommercialLineDefaults(ctx context.Context, session identity.Session, input *CommercialDocumentInput, line *CommercialLineInput, lineNumber int) error {
	baseCurrency, err := s.companyBaseCurrency(ctx, session.CurrentCompanyID)
	if err != nil {
		return err
	}
	doc := commerce.DocumentContext{
		Direction:    commerce.DirectionSales,
		CompanyID:    session.CurrentCompanyID,
		PartyID:      input.PartyID,
		CurrencyCode: input.CurrencyCode,
		BaseCurrency: baseCurrency,
		ExchangeRate: input.ExchangeRate,
		DocumentDate: input.DocumentDate.Format("2006-01-02"),
		PriceListID:  input.PriceListID,
	}
	explicitManual := line.PriceSource == "MANUAL"
	defaults, err := commerce.ResolveLineDefaults(ctx, s.pool, doc,
		commerce.LineContext{ProductID: line.ProductID, VariantID: line.VariantID, UnitCode: line.UnitCode},
		line.UnitPrice)
	if err != nil {
		switch {
		case errors.Is(err, commerce.ErrExchangeRateUnavailable):
			return commercialError(CommercialErrorExchangeRateUnavailable, "temel fiyat belge para birimine çevrilemedi", "currency_code", lineNumber)
		case errors.Is(err, commerce.ErrPriceUnavailable):
			return commercialError(CommercialErrorPriceRequired, "satış fiyatı bulunamadı", "unit_price", lineNumber)
		default:
			return err
		}
	}

	line.UnitPrice = defaults.UnitPrice
	line.PriceSource = defaults.PriceSource
	// An explicit client-marked MANUAL price stays MANUAL even when its
	// amount happens to equal a catalog candidate; the user's own choice is
	// not silently reclassified.
	if explicitManual {
		line.PriceSource = commerce.PriceSourceManual
	}
	line.PriceListSnapshot = cloneCommercialMap(line.PriceListSnapshot)
	line.PriceListSnapshot["source"] = line.PriceSource
	if defaults.PriceListID != "" {
		line.PriceListSnapshot["price_list_id"] = defaults.PriceListID
	}
	if line.PriceSource == commerce.PriceSourceManual && !session.HasPermission(commerce.DirectionSales.PriceOverridePermission()) {
		return identity.ErrForbidden
	}

	if line.TaxRate == "" {
		line.TaxRate = defaults.TaxRate
	} else if strings.TrimSpace(line.TaxRate) != strings.TrimSpace(defaults.TaxRate) && !session.HasPermission(commerce.DirectionSales.TaxOverridePermission()) {
		return commercialError(CommercialErrorTaxProfileInvalid, "vergi profili değişikliği için yetki gereklidir", "tax_rate", lineNumber)
	}
	// Whether the price already contains tax is a property of the card, not of
	// the posted line: a client that omits the flag must not turn a
	// tax-included price into a tax-exclusive one.
	line.TaxIncluded = defaults.TaxIncluded
	if line.WithholdingRate == "" {
		line.WithholdingRate = defaults.WithholdingRate
	}
	if line.ConversionFactor == "" && defaults.ConversionFactor != "" {
		line.ConversionFactor = defaults.ConversionFactor
	}
	line.TaxSnapshot = cloneCommercialMap(line.TaxSnapshot)
	line.TaxSnapshot["source"] = "PRODUCT_TAX_PROFILE"
	line.TaxSnapshot["treatment"] = defaults.Treatment
	line.TaxSnapshot["tax_code"] = defaults.TaxCode
	line.TaxSnapshot["withholding_code"] = defaults.WithholdingCode
	line.TaxSnapshot["exemption_code"] = defaults.ExemptionCode
	line.TaxSnapshot["tax_note"] = defaults.TaxNote
	line.TaxSnapshot["profile_version"] = defaults.ProfileVersion
	// Components carries the product's full directional tax profile
	// (VAT plus any excise/quantity-based component) so ÖTV-style taxes
	// survive onto the document instead of collapsing into a single rate.
	line.taxComponentsSnapshot = defaults.Components
	return nil
}

func cloneCommercialMap(input map[string]any) map[string]any {
	result := make(map[string]any, len(input)+4)
	for key, value := range input {
		result[key] = value
	}
	return result
}

func normalizeCommercialLines(inputs []CommercialLineInput, defaultWarehouse, currency string) ([]CommercialLine, commercialTotals, error) {
	lines := make([]CommercialLine, 0, len(inputs))
	totals := commercialTotals{}
	for index, input := range inputs {
		priceListSnapshot := input.PriceListSnapshot
		if priceListSnapshot == nil {
			priceListSnapshot = map[string]any{}
		}
		discountComponents := input.DiscountComponents
		if discountComponents == nil {
			discountComponents = []CommercialDiscountInput{}
		}
		taxSnapshot := input.TaxSnapshot
		if taxSnapshot == nil {
			taxSnapshot = map[string]any{}
		}
		line := CommercialLine{ID: strings.TrimSpace(input.ID), LineNo: input.LineNo, LineType: strings.ToUpper(strings.TrimSpace(input.LineType)), UnitCode: strings.ToUpper(strings.TrimSpace(input.UnitCode)), Quantity: strings.TrimSpace(input.Quantity), BaseQuantity: strings.TrimSpace(input.BaseQuantity), ConversionFactor: strings.TrimSpace(input.ConversionFactor), PriceSource: strings.ToUpper(strings.TrimSpace(input.PriceSource)), PriceListSnapshot: priceListSnapshot, DiscountComponents: discountComponents, TaxSnapshot: taxSnapshot, TaxComponentsSnapshot: componentSnapshot(input.taxComponentsSnapshot), Description: strings.TrimSpace(input.Description), UnitPrice: strings.TrimSpace(input.UnitPrice), VariantCode: strings.TrimSpace(input.variantCodeSnapshot), VariantAttributes: cloneCommercialMap(input.variantAttributesSnapshot)}
		if line.ID == "" || uuid.Validate(line.ID) != nil {
			line.ID = uuid.NewString()
		}
		if line.LineNo < 1 {
			line.LineNo = index + 1
		}
		if line.LineType == "" {
			if strings.TrimSpace(input.ProductID) == "" {
				line.LineType = "SERVICE"
			} else {
				line.LineType = "PRODUCT"
			}
		}
		if line.UnitCode == "" {
			return nil, commercialTotals{}, commercialError(CommercialErrorInvalidRelation, "birim gereklidir", "unit_code", index+1)
		}
		if strings.TrimSpace(input.ProductID) != "" {
			value := strings.TrimSpace(input.ProductID)
			line.ProductID = &value
		}
		if strings.TrimSpace(input.VariantID) != "" {
			value := strings.TrimSpace(input.VariantID)
			line.VariantID = &value
		}
		warehouse := strings.TrimSpace(input.WarehouseID)
		if line.LineType == "PRODUCT" {
			if warehouse == "" {
				warehouse = defaultWarehouse
			}
			line.WarehouseID = &warehouse
		}
		if strings.TrimSpace(input.SourceLineID) != "" {
			value := strings.TrimSpace(input.SourceLineID)
			line.SourceLineID = &value
		}
		if line.PriceSource == "" {
			line.PriceSource = "DEFAULT"
		}
		if line.Description == "" {
			line.Description = "Ticari belge satırı"
		}
		if strings.TrimSpace(line.UnitPrice) == "" {
			return nil, commercialTotals{}, commercialError(CommercialErrorPriceRequired, "satır fiyatı gereklidir", "unit_price", index+1)
		}
		quantity, err := commercialDecimal(line.Quantity, true)
		if err != nil {
			return nil, commercialTotals{}, commercialError(CommercialErrorInvalidRelation, "miktar geçersiz", "quantity", index+1)
		}
		unitPrice, err := commercialDecimal(line.UnitPrice, false)
		if err != nil {
			return nil, commercialTotals{}, commercialError(CommercialErrorInvalidRelation, "fiyat geçersiz", "unit_price", index+1)
		}
		factor := line.ConversionFactor
		if factor == "" {
			factor = "1"
		}
		factorRat, err := commercialDecimal(factor, true)
		if err != nil {
			return nil, commercialTotals{}, commercialError(CommercialErrorInvalidRelation, "dönüşüm katsayısı geçersiz", "conversion_factor", index+1)
		}
		line.ConversionFactor = rat8(factorRat)
		expectedBase := new(big.Rat).Mul(quantity, factorRat)
		if line.BaseQuantity == "" {
			line.BaseQuantity = rat8(expectedBase)
		} else {
			providedBase, baseErr := commercialDecimal(line.BaseQuantity, true)
			if baseErr != nil || providedBase.Cmp(expectedBase) != 0 {
				return nil, commercialTotals{}, commercialError(CommercialErrorInvalidRelation, "baz miktar dönüşüm katsayısıyla eşleşmiyor", "base_quantity", index+1)
			}
			line.BaseQuantity = rat8(providedBase)
		}
		if _, err = commercialDecimal(line.BaseQuantity, true); err != nil {
			return nil, commercialTotals{}, commercialError(CommercialErrorInvalidRelation, "baz miktar geçersiz", "base_quantity", index+1)
		}
		gross := new(big.Rat).Mul(quantity, unitPrice)
		net := new(big.Rat).Set(gross)
		discount := new(big.Rat)
		if input.DiscountAmount != "" {
			discount, err = commercialDecimal(input.DiscountAmount, false)
			if err != nil || discount.Sign() < 0 || discount.Cmp(gross) > 0 {
				return nil, commercialTotals{}, commercialError(CommercialErrorInvalidRelation, "indirim geçersiz", "discount_amount", index+1)
			}
			net.Sub(net, discount)
		}
		if input.DiscountRate != "" {
			rate, rateErr := commercialDecimal(input.DiscountRate, false)
			if rateErr != nil || rate.Sign() < 0 || rate.Cmp(big.NewRat(100, 1)) > 0 {
				return nil, commercialTotals{}, commercialError(CommercialErrorInvalidRelation, "indirim oranı geçersiz", "discount_rate", index+1)
			}
			d := new(big.Rat).Mul(net, rate)
			d.Quo(d, big.NewRat(100, 1))
			discount.Add(discount, d)
			net.Sub(net, d)
		}
		for _, component := range input.DiscountComponents {
			var d *big.Rat
			if strings.TrimSpace(component.Amount) != "" {
				d, err = commercialDecimal(component.Amount, false)
			} else {
				var rate *big.Rat
				rate, err = commercialDecimal(component.Rate, false)
				if err == nil && rate.Cmp(big.NewRat(100, 1)) <= 0 {
					d = new(big.Rat).Mul(net, rate)
					d.Quo(d, big.NewRat(100, 1))
				} else if err == nil {
					err = errors.New("discount rate is greater than one hundred")
				}
			}
			if err != nil || d.Sign() < 0 || d.Cmp(net) > 0 {
				return nil, commercialTotals{}, commercialError(CommercialErrorInvalidRelation, "indirim bileşeni geçersiz", "discount_components", index+1)
			}
			discount.Add(discount, d)
			net.Sub(net, d)
		}
		taxRate := "0"
		if input.TaxRate != "" {
			taxRate = input.TaxRate
		}
		tax := new(big.Rat)
		if commercialLineNeedsTaxEngine(input.taxComponentsSnapshot) {
			// A resolved profile with more than one component, or a
			// QUANTITY_BASED component (excise-style ÖTV/ÖİV), cannot be
			// expressed as the single flat rate below; the shared engine
			// computes gross/discount/net/tax from the same inputs so the
			// two paths never disagree on a plain single-rate line.
			engineResult, calcErr := taxes.Calculate(taxes.TaxCalculationInput{
				Lines: []taxes.TaxCalculationLine{{
					UnitPrice:  line.UnitPrice,
					Quantity:   line.Quantity,
					Discounts:  commercialDiscountChain(input),
					Components: input.taxComponentsSnapshot,
				}},
				TaxMode:     commercialTaxMode(input.TaxIncluded),
				RoundScale:  8,
				RoundPolicy: taxes.RoundHalfUp,
			})
			if calcErr != nil {
				return nil, commercialTotals{}, mapCommercialTaxEngineError(calcErr, index+1)
			}
			engineLine := engineResult.Lines[0]
			gross = mustCommercialRat(engineLine.GrossAmount)
			discount = mustCommercialRat(engineLine.DiscountAmount)
			net = mustCommercialRat(engineLine.TaxableAmount)
			tax = mustCommercialRat(engineLine.TaxAmount)
			line.TaxComponentsSnapshot = engineLine.Components
			if rate, ok := primaryComponentRate(input.taxComponentsSnapshot); ok {
				taxRate = rate
			}
		} else {
			rate, rateErr := commercialDecimal(taxRate, false)
			if rateErr != nil || rate.Sign() < 0 || rate.Cmp(big.NewRat(100, 1)) > 0 {
				return nil, commercialTotals{}, commercialError(CommercialErrorTaxProfileInvalid, "vergi oranı geçersiz", "tax_rate", index+1)
			}
			if input.TaxIncluded {
				denom := new(big.Rat).Add(big.NewRat(100, 1), rate)
				denom.Quo(denom, big.NewRat(100, 1))
				base := new(big.Rat).Quo(net, denom)
				tax.Sub(net, base)
				net = base
			} else {
				tax.Mul(net, rate)
				tax.Quo(tax, big.NewRat(100, 1))
			}
		}
		// A line that did not go through the engine has exactly one component,
		// so its breakdown is the line's own tax.
		if len(line.TaxComponentsSnapshot) == 1 && line.TaxComponentsSnapshot[0].Amount == "" {
			line.TaxComponentsSnapshot[0].BaseAmount = rat8(net)
			line.TaxComponentsSnapshot[0].Amount = rat8(tax)
		}
		withholding := new(big.Rat)
		if input.WithholdingRate != "" {
			wr, werr := commercialDecimal(input.WithholdingRate, false)
			if werr != nil || wr.Sign() < 0 || wr.Cmp(big.NewRat(100, 1)) > 0 {
				return nil, commercialTotals{}, commercialError(CommercialErrorTaxProfileInvalid, "tevkifat oranı geçersiz", "withholding_rate", index+1)
			}
			// Tevkifat is withheld from VAT, never from ÖTV-style additional
			// taxes, so a line with extra components withholds on the VAT
			// component alone.
			withholding.Mul(primaryComponentAmount(line.TaxComponentsSnapshot, tax), wr)
			withholding.Quo(withholding, big.NewRat(100, 1))
		}
		line.GrossAmount, line.DiscountAmount, line.NetAmount, line.TaxAmount, line.WithholdingAmount = rat8(gross), rat8(discount), rat8(net), rat8(tax), rat8(withholding)
		line.LineTotal = rat8(new(big.Rat).Add(net, tax))
		line.PayableAmount = rat8(new(big.Rat).Sub(new(big.Rat).Add(net, tax), withholding))
		if line.TaxSnapshot == nil {
			line.TaxSnapshot = map[string]any{}
		}
		line.TaxSnapshot["rate"] = taxRate
		line.TaxSnapshot["included"] = input.TaxIncluded
		line.TaxSnapshot["withholding_rate"] = input.WithholdingRate
		line.TaxSnapshot["currency"] = currency
		lines = append(lines, line)
		totals.Subtotal = addCommercial(totals.Subtotal, line.GrossAmount)
		totals.DiscountTotal = addCommercial(totals.DiscountTotal, line.DiscountAmount)
		totals.TaxTotal = addCommercial(totals.TaxTotal, line.TaxAmount)
		totals.WithholdingTotal = addCommercial(totals.WithholdingTotal, line.WithholdingAmount)
		totals.GrandTotal = addCommercial(totals.GrandTotal, line.LineTotal)
		totals.PayableTotal = addCommercial(totals.PayableTotal, line.PayableAmount)
	}
	return lines, totals, nil
}

func assignCommercialCreateLineIDs(input *CommercialDocumentInput, lines []CommercialLine) error {
	remapped := make(map[string]string, len(lines))
	duplicates := make(map[string]struct{})
	for index := range lines {
		oldID := strings.TrimSpace(lines[index].ID)
		newID := uuid.NewString()
		if oldID != "" {
			if _, exists := remapped[oldID]; exists {
				duplicates[oldID] = struct{}{}
			} else {
				remapped[oldID] = newID
			}
		}
		lines[index].ID = newID
	}
	for index := range input.Allocations {
		targetID := strings.TrimSpace(input.Allocations[index].TargetLineID)
		if targetID == "" {
			continue
		}
		if _, duplicate := duplicates[targetID]; duplicate {
			return commercialError(CommercialErrorInvalidRelation, "hedef satır kimliği birden fazla satırda kullanılamaz", "target_line_id", index+1)
		}
		if newID, ok := remapped[targetID]; ok {
			input.Allocations[index].TargetLineID = newID
		}
	}
	return nil
}

func commercialDecimal(value string, allowZero bool) (*big.Rat, error) {
	value = strings.TrimSpace(value)
	if value == "" || !validCommercialDecimalLiteral(value) || commercialDecimalScale(value) > 8 {
		return nil, errors.New("decimal required")
	}
	r, ok := new(big.Rat).SetString(value)
	if !ok || (!allowZero && r.Sign() < 0) || (allowZero && r.Sign() <= 0) {
		return nil, errors.New("invalid decimal")
	}
	return r, nil
}

func commercialDecimalScale(value string) int {
	if len(value) > 0 && (value[0] == '-' || value[0] == '+') {
		value = value[1:]
	}
	if decimalPoint := strings.IndexByte(value, '.'); decimalPoint >= 0 {
		return len(value) - decimalPoint - 1
	}
	return 0
}

func validCommercialDecimalLiteral(value string) bool {
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
	decimalPoint := false
	for _, ch := range value {
		switch {
		case ch >= '0' && ch <= '9':
			digits++
		case ch == '.' && !decimalPoint:
			decimalPoint = true
		default:
			return false
		}
	}
	return digits > 0
}

func validCommercialCurrency(value string) bool {
	if len(value) != 3 {
		return false
	}
	for _, ch := range value {
		if ch < 'A' || ch > 'Z' {
			return false
		}
	}
	return true
}

func conversionDiscountComponents(line CommercialLine) []CommercialDiscountInput {
	gross, grossOK := new(big.Rat).SetString(strings.TrimSpace(line.GrossAmount))
	discount, discountOK := new(big.Rat).SetString(strings.TrimSpace(line.DiscountAmount))
	if !grossOK || !discountOK || gross.Sign() <= 0 || discount.Sign() <= 0 {
		return nil
	}
	rate := new(big.Rat).Quo(discount, gross)
	rate.Mul(rate, big.NewRat(100, 1))
	return []CommercialDiscountInput{{Rate: rat8(rate)}}
}

// componentsFromSnapshot turns a persisted breakdown back into engine input,
// dropping the amounts it was calculated with.
func componentsFromSnapshot(snapshot []taxes.TaxCalculationComponentResult) []taxes.TaxComponent {
	components := make([]taxes.TaxComponent, 0, len(snapshot))
	for _, entry := range snapshot {
		if strings.TrimSpace(entry.Rate) == "" {
			continue
		}
		components = append(components, taxes.TaxComponent{
			Code: entry.Code, Name: entry.Name, Primary: entry.Primary,
			IncludedInBase: entry.IncludedInBase, CalculationType: entry.CalculationType,
			Rate: entry.Rate, Withholding: entry.Withholding, Exempt: entry.Exempt,
		})
	}
	return components
}

// componentSnapshot is the pre-calculation view of a line's components: the
// resolved profile with no amounts yet. A line whose tax runs through the
// engine replaces it with the computed breakdown.
func componentSnapshot(components []taxes.TaxComponent) []taxes.TaxCalculationComponentResult {
	snapshot := make([]taxes.TaxCalculationComponentResult, 0, len(components))
	for _, component := range components {
		snapshot = append(snapshot, taxes.TaxCalculationComponentResult{
			Code: component.Code, Name: component.Name, Primary: component.Primary,
			IncludedInBase: component.IncludedInBase, CalculationType: component.CalculationType,
			Rate: component.Rate, Withholding: component.Withholding, Exempt: component.Exempt,
		})
	}
	return snapshot
}

// primaryComponentRate is the VAT rate of a resolved profile: the rate that
// belongs in the line's tax_rate column, never an additional tax's rate.
func primaryComponentRate(components []taxes.TaxComponent) (string, bool) {
	for _, component := range components {
		if component.Primary {
			return component.Rate, true
		}
	}
	return "", false
}

// primaryComponentAmount is the VAT part of a computed breakdown, falling back
// to the line's whole tax amount when no component is marked primary.
func primaryComponentAmount(components []taxes.TaxCalculationComponentResult, fallback *big.Rat) *big.Rat {
	for _, component := range components {
		if !component.Primary || strings.TrimSpace(component.Amount) == "" {
			continue
		}
		if amount, err := commercialDecimal(component.Amount, true); err == nil {
			return amount
		}
	}
	return fallback
}

// commercialLineNeedsTaxEngine reports whether a line's resolved tax profile
// cannot be expressed as this file's single flat-rate formula: more than one
// component (VAT plus an excise-style component), or any QUANTITY_BASED
// component (a per-unit amount, not a percentage).
func commercialLineNeedsTaxEngine(components []taxes.TaxComponent) bool {
	if len(components) > 1 {
		return true
	}
	for _, component := range components {
		if component.CalculationType != taxes.TaxPercentage || component.IncludedInBase {
			return true
		}
	}
	return false
}

// commercialDiscountChain rebuilds the same discount order this file applies
// by hand (fixed amount, then percent, then each named component) as the
// engine's cascading chain, so both paths take an identical amount off an
// identical line.
func commercialDiscountChain(input CommercialLineInput) []taxes.TaxDiscount {
	chain := make([]taxes.TaxDiscount, 0, 2+len(input.DiscountComponents))
	if input.DiscountAmount != "" {
		chain = append(chain, taxes.TaxDiscount{Kind: taxes.DiscountFixed, Amount: input.DiscountAmount})
	}
	if input.DiscountRate != "" {
		chain = append(chain, taxes.TaxDiscount{Kind: taxes.DiscountPercent, Amount: input.DiscountRate})
	}
	for _, component := range input.DiscountComponents {
		if strings.TrimSpace(component.Amount) != "" {
			chain = append(chain, taxes.TaxDiscount{Kind: taxes.DiscountFixed, Amount: component.Amount})
		} else {
			chain = append(chain, taxes.TaxDiscount{Kind: taxes.DiscountPercent, Amount: component.Rate})
		}
	}
	return chain
}

func commercialTaxMode(taxIncluded bool) taxes.TaxMode {
	if taxIncluded {
		return taxes.TaxInclusive
	}
	return taxes.TaxExclusive
}

func mustCommercialRat(value string) *big.Rat {
	r, ok := new(big.Rat).SetString(value)
	if !ok {
		return new(big.Rat)
	}
	return r
}

func mapCommercialTaxEngineError(err error, lineNumber int) error {
	switch {
	case errors.Is(err, taxes.ErrDiscountExceedsTaxBase):
		return commercialError(CommercialErrorInvalidRelation, "indirim satır tutarını aşamaz", "discount_amount", lineNumber)
	default:
		return commercialError(CommercialErrorTaxProfileInvalid, "vergi hesabı geçersiz", "tax_rate", lineNumber)
	}
}

func rat8(value *big.Rat) string {
	if value == nil {
		return "0.00000000"
	}
	return value.FloatString(8)
}
func addCommercial(left, right string) string {
	if strings.TrimSpace(left) == "" {
		left = "0"
	}
	a, _ := new(big.Rat).SetString(left)
	b, _ := new(big.Rat).SetString(right)
	return rat8(new(big.Rat).Add(a, b))
}
func optionalCommercialID(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func insertCommercialAnchorTx(ctx context.Context, tx pgx.Tx, session identity.Session, spec commercialSpec, id, documentNo string, input CommercialDocumentInput, totals commercialTotals) error {
	_, err := tx.Exec(ctx, `INSERT INTO documents(id,company_id,document_type_code,document_no,branch_id,warehouse_id,party_id,document_date,due_date,currency_code,exchange_rate,notes,subtotal,discount_total,tax_total,grand_total,created_by,updated_by) VALUES($1,$2,$3,$4,$5,NULLIF($6,'')::uuid,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$17)`, id, session.CurrentCompanyID, spec.typeCode, documentNo, input.BranchID, input.DefaultWarehouseID, input.PartyID, input.DocumentDate, input.DueDate, input.CurrencyCode, input.ExchangeRate, input.Notes, totals.Subtotal, totals.DiscountTotal, totals.TaxTotal, totals.GrandTotal, session.User.ID)
	return err
}

func insertCommercialHeaderTx(ctx context.Context, tx pgx.Tx, session identity.Session, spec commercialSpec, id, documentNo string, input CommercialDocumentInput, totals commercialTotals) error {
	query := fmt.Sprintf(`INSERT INTO %s(id,company_id,document_id,document_no,branch_id,default_warehouse_id,party_id,document_date,due_date,valid_until,currency_code,exchange_rate,notes,status,source_kind,source_document_id,subtotal,discount_total,tax_total,withholding_total,grand_total,payable_total,sales_rep_user_id,payment_term_id,created_by,updated_by) VALUES($1,$2,$3,$4,$5,NULLIF($6,'')::uuid,$7,$8,$9,$10,$11,$12,$13,'DRAFT',$14,NULLIF($15,'')::uuid,$16,$17,$18,$19,$20,$21,NULLIF($22,'')::uuid,NULLIF($23,'')::uuid,$24,$24)`, spec.table)
	args := []any{id, session.CurrentCompanyID, id, documentNo, input.BranchID, input.DefaultWarehouseID, input.PartyID, input.DocumentDate, input.DueDate, input.ValidUntil, input.CurrencyCode, input.ExchangeRate, input.Notes, input.SourceKind, input.SourceDocumentID, totals.Subtotal, totals.DiscountTotal, totals.TaxTotal, totals.WithholdingTotal, totals.GrandTotal, totals.PayableTotal, input.SalesRepUserID, input.PaymentTermID, session.User.ID}
	if spec.kind == SalesReturn {
		query = `INSERT INTO sales_returns(id,company_id,document_id,document_no,branch_id,default_warehouse_id,party_id,document_date,due_date,valid_until,currency_code,exchange_rate,notes,reason,status,source_kind,source_document_id,subtotal,discount_total,tax_total,withholding_total,grand_total,payable_total,sales_rep_user_id,payment_term_id,created_by,updated_by) VALUES($1,$2,$3,$4,$5,NULLIF($6,'')::uuid,$7,$8,$9,$10,$11,$12,$13,$14,'DRAFT',$15,NULLIF($16,'')::uuid,$17,$18,$19,$20,$21,$22,NULLIF($23,'')::uuid,NULLIF($24,'')::uuid,$25,$25)`
		args = []any{id, session.CurrentCompanyID, id, documentNo, input.BranchID, input.DefaultWarehouseID, input.PartyID, input.DocumentDate, input.DueDate, input.ValidUntil, input.CurrencyCode, input.ExchangeRate, input.Notes, input.Reason, input.SourceKind, input.SourceDocumentID, totals.Subtotal, totals.DiscountTotal, totals.TaxTotal, totals.WithholdingTotal, totals.GrandTotal, totals.PayableTotal, input.SalesRepUserID, input.PaymentTermID, session.User.ID}
	}
	_, err := tx.Exec(ctx, query, args...)
	return err
}

func insertPartySnapshotTx(ctx context.Context, tx pgx.Tx, companyID, documentID, partyID string) error {
	var code, displayName, legalName, taxNumber, identityNumber, taxOffice, taxOfficeCode string
	if err := tx.QueryRow(ctx, `SELECT p.code,p.display_name,COALESCE(p.legal_name,''),COALESCE(p.tax_number,''),COALESCE(p.identity_number,''),COALESCE(NULLIF(p.tax_office,''),t.name,''),COALESCE(t.code,'') FROM parties p LEFT JOIN turkish_tax_offices t ON t.id=p.tax_office_id WHERE p.company_id=$1 AND p.id=$2 AND p.is_active`, companyID, partyID).Scan(&code, &displayName, &legalName, &taxNumber, &identityNumber, &taxOffice, &taxOfficeCode); errors.Is(err, pgx.ErrNoRows) {
		return commercialError(CommercialErrorInvalidPartyRole, "cari bulunamadı", "party_id", 0)
	} else if err != nil {
		return err
	}
	var addresses []byte
	if err := tx.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(a) ORDER BY a.is_default DESC,a.id),'[]'::jsonb) FROM party_addresses a WHERE a.company_id=$1 AND a.party_id=$2`, companyID, partyID).Scan(&addresses); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO commercial_party_snapshots(id,company_id,document_id,party_code,display_name,legal_name,tax_number,identity_number,tax_office,tax_office_code,addresses) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT(company_id,document_id) DO UPDATE SET party_code=EXCLUDED.party_code,display_name=EXCLUDED.display_name,legal_name=EXCLUDED.legal_name,tax_number=EXCLUDED.tax_number,identity_number=EXCLUDED.identity_number,tax_office=EXCLUDED.tax_office,tax_office_code=EXCLUDED.tax_office_code,addresses=EXCLUDED.addresses,snapshot_at=now()`, uuid.NewString(), companyID, documentID, code, displayName, legalName, taxNumber, identityNumber, taxOffice, taxOfficeCode, addresses)
	return err
}

func insertCommercialLineTx(ctx context.Context, tx pgx.Tx, companyID string, spec commercialSpec, documentID string, line *CommercialLine) error {
	if line == nil {
		return commercialError(CommercialErrorInvalidRelation, "satır geçersiz", "lines", 0)
	}
	productID, variantID, warehouseID, sourceLineID := "", "", "", ""
	if line.ProductID != nil {
		productID = *line.ProductID
	}
	if line.VariantID != nil {
		variantID = *line.VariantID
	}
	if line.WarehouseID != nil {
		warehouseID = *line.WarehouseID
	}
	if line.SourceLineID != nil {
		sourceLineID = *line.SourceLineID
	}
	if variantID != "" && strings.TrimSpace(line.VariantCode) == "" {
		var attributes []byte
		if err := tx.QueryRow(ctx, `SELECT pv.variant_code,COALESCE((SELECT jsonb_object_agg(d.code,o.name) FROM product_variant_values vv JOIN variant_definitions d ON d.company_id=vv.company_id AND d.id=vv.definition_id JOIN variant_definition_options o ON o.company_id=vv.company_id AND o.definition_id=vv.definition_id AND o.id=vv.option_id WHERE vv.company_id=pv.company_id AND vv.variant_id=pv.id),'{}'::jsonb) FROM product_variants pv WHERE pv.company_id=$1 AND pv.id=$2 AND pv.product_id=$3`, companyID, variantID, productID).Scan(&line.VariantCode, &attributes); err != nil {
			return err
		}
		if err := json.Unmarshal(attributes, &line.VariantAttributes); err != nil {
			return err
		}
	}
	if line.VariantAttributes == nil {
		line.VariantAttributes = map[string]any{}
	}
	components := line.TaxComponentsSnapshot
	if components == nil {
		components = []taxes.TaxCalculationComponentResult{}
	}
	_, err := tx.Exec(ctx, fmt.Sprintf(`INSERT INTO %s(id,company_id,document_id,line_no,line_type,product_id,variant_id,variant_code_snapshot,variant_attributes_snapshot,warehouse_id,unit_code,quantity,base_quantity,conversion_factor,price_source,price_list_snapshot,discount_components_snapshot,tax_snapshot,tax_components_snapshot,description_snapshot,source_line_id,unit_price,gross_amount,discount_amount,net_amount,tax_amount,withholding_amount,line_total,payable_amount) VALUES($1,$2,$3,$4,$5,NULLIF($6,'')::uuid,NULLIF($7,'')::uuid,$8,$9,NULLIF($10,'')::uuid,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,NULLIF($21,'')::uuid,$22,$23,$24,$25,$26,$27,$28,$29)`, spec.lineTable), line.ID, companyID, documentID, line.LineNo, line.LineType, productID, variantID, line.VariantCode, jsonBytes(line.VariantAttributes), warehouseID, line.UnitCode, line.Quantity, line.BaseQuantity, line.ConversionFactor, line.PriceSource, jsonBytes(line.PriceListSnapshot), jsonBytes(line.DiscountComponents), jsonBytes(line.TaxSnapshot), jsonBytes(components), line.Description, sourceLineID, line.UnitPrice, line.GrossAmount, line.DiscountAmount, line.NetAmount, line.TaxAmount, line.WithholdingAmount, line.LineTotal, line.PayableAmount)
	return err
}

func insertCommercialSourcesAndAllocationsTx(ctx context.Context, tx pgx.Tx, companyID, documentID string, kind CommercialKind, input CommercialDocumentInput, lines []CommercialLine) error {
	sourceIDs := commercialSourceIDs(input)
	relation := "CONVERSION"
	if kind == SalesDispatch {
		relation = "FULFILLMENT"
	}
	if kind == SalesInvoice {
		relation = "INVOICING"
	}
	if kind == SalesReturn {
		relation = "RETURN"
	}
	if input.SourceKind == "DIRECT" && len(sourceIDs) > 0 {
		return commercialError(CommercialErrorInvalidRelation, "doğrudan belgede kaynak belge bulunamaz", "source_document_id", 0)
	}
	if kind == SalesReturn && len(lines) > 0 && (input.SourceKind == "DIRECT" || len(sourceIDs) == 0) {
		return commercialError(CommercialErrorInvalidRelation, "satış iadesi için kaynak belge gereklidir", "source_document_id", 0)
	}
	if input.SourceKind != "DIRECT" && len(sourceIDs) == 0 {
		return commercialError(CommercialErrorInvalidRelation, "kaynak belge gereklidir", "source_document_id", 0)
	}
	for _, sourceID := range sourceIDs {
		var branchID, partyID, currency, sourceType, sourceStatus string
		if err := tx.QueryRow(ctx, `SELECT branch_id,party_id,currency_code,document_type_code,status FROM documents WHERE company_id=$1 AND id=$2`, companyID, sourceID).Scan(&branchID, &partyID, &currency, &sourceType, &sourceStatus); errors.Is(err, pgx.ErrNoRows) {
			return commercialError(CommercialErrorInvalidRelation, "kaynak belge bulunamadı", "source_document_id", 0)
		} else if err != nil {
			return err
		}
		if sourceID == documentID || !commercialSourceTypeAllowed(kind, input.SourceKind, sourceType) {
			return commercialError(CommercialErrorInvalidRelation, "kaynak belge bu ticari belge türüyle eşleşmiyor", "source_document_id", 0)
		}
		if sourceStatus == "CANCELLED" {
			return commercialError(CommercialErrorSourceCancelled, "iptal edilmiş kaynak belge kullanılamaz", "source_document_id", 0)
		}
		if valid, stateErr := commercialSourceStateAllowed(ctx, tx, companyID, sourceID, kind, sourceType); stateErr != nil {
			return stateErr
		} else if !valid {
			return commercialError(CommercialErrorSourceAlreadyConsumed, "kaynak belgenin kullanılabilir miktarı kalmadı", "source_document_id", 0)
		}
		// Source documents may belong to another typed table, but a conversion
		// cannot cross branch, party or currency boundaries.
		var currentBranch, currentParty, currentCurrency string
		if err := tx.QueryRow(ctx, `SELECT branch_id,party_id,currency_code FROM documents WHERE company_id=$1 AND id=$2`, companyID, documentID).Scan(&currentBranch, &currentParty, &currentCurrency); err != nil {
			return err
		}
		if branchID != currentBranch {
			return commercialError(CommercialErrorInvalidRelation, "kaynak belgeler aynı şubeye ait olmalıdır", "source_document_id", 0)
		}
		if partyID != currentParty {
			return commercialError(CommercialErrorSourcePartyMismatch, "kaynak belgeler aynı cariye ait olmalıdır", "source_document_id", 0)
		}
		if currency != currentCurrency {
			return commercialError(CommercialErrorSourceCurrencyMismatch, "kaynak belgeler aynı para biriminde olmalıdır", "source_document_id", 0)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO commercial_document_sources(company_id,document_id,source_document_id,relation_type) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, companyID, documentID, sourceID, relation); err != nil {
			return err
		}
	}
	allocations := append([]CommercialAllocationInput(nil), input.Allocations...)
	for _, line := range lines {
		if line.SourceLineID != nil {
			duplicate := false
			for _, allocation := range allocations {
				if strings.TrimSpace(allocation.SourceLineID) == *line.SourceLineID && (strings.TrimSpace(allocation.TargetLineID) == "" || strings.TrimSpace(allocation.TargetLineID) == line.ID) && strings.ToUpper(strings.TrimSpace(allocation.Type)) == relationToAllocation(kind) {
					duplicate = true
					break
				}
			}
			if !duplicate {
				allocations = append(allocations, CommercialAllocationInput{SourceLineID: *line.SourceLineID, TargetLineID: line.ID, Type: relationToAllocation(kind), Quantity: line.Quantity, BaseQuantity: line.BaseQuantity})
			}
		}
	}
	for index, allocation := range allocations {
		sourceID := strings.TrimSpace(allocation.SourceLineID)
		targetID := strings.TrimSpace(allocation.TargetLineID)
		if targetID == "" && len(lines) == 1 {
			targetID = lines[0].ID
		}
		if uuid.Validate(sourceID) != nil || uuid.Validate(targetID) != nil {
			return commercialError(CommercialErrorInvalidRelation, "satır tahsisi kimliği geçersiz", "allocations", index+1)
		}
		allocationType := strings.ToUpper(strings.TrimSpace(allocation.Type))
		if allocationType == "" {
			allocationType = relationToAllocation(kind)
		}
		if allocationType != relationToAllocation(kind) {
			return commercialError(CommercialErrorInvalidRelation, "satır tahsis türü belge türüyle eşleşmiyor", "type", index+1)
		}
		quantity := strings.TrimSpace(allocation.Quantity)
		if quantity == "" {
			return commercialError(CommercialErrorInvalidRelation, "tahsis miktarı gereklidir", "quantity", index+1)
		}
		if _, err := commercialDecimal(quantity, true); err != nil {
			return commercialError(CommercialErrorInvalidRelation, "tahsis miktarı geçersiz", "quantity", index+1)
		}
		var targetDocumentID, targetQuantity, targetBaseQuantity, targetLineType, targetAggregateType string
		if err := tx.QueryRow(ctx, `SELECT document_id,quantity::text,base_quantity::text,line_type,aggregate_type FROM commercial_line_registry WHERE company_id=$1 AND line_id=$2`, companyID, targetID).Scan(&targetDocumentID, &targetQuantity, &targetBaseQuantity, &targetLineType, &targetAggregateType); errors.Is(err, pgx.ErrNoRows) {
			return commercialError(CommercialErrorInvalidRelation, "hedef satır bulunamadı", "target_line_id", index+1)
		} else if err != nil {
			return err
		}
		if targetDocumentID != documentID {
			return commercialError(CommercialErrorInvalidRelation, "hedef satır mevcut belgeye ait değil", "target_line_id", index+1)
		}
		baseQuantity := strings.TrimSpace(allocation.BaseQuantity)
		if baseQuantity == "" {
			if quantity == targetQuantity {
				baseQuantity = targetBaseQuantity
			} else {
				return commercialError(CommercialErrorInvalidRelation, "kısmi tahsis için baz miktar gereklidir", "base_quantity", index+1)
			}
		}
		if _, err := commercialDecimal(baseQuantity, true); err != nil {
			return commercialError(CommercialErrorInvalidRelation, "tahsis baz miktarı geçersiz", "base_quantity", index+1)
		}
		var sourceQuantity, sourceBaseQuantity, sourceDocumentID, sourceLineType, sourceAggregateType string
		if err := tx.QueryRow(ctx, `SELECT document_id,quantity::text,base_quantity::text,line_type,aggregate_type FROM commercial_line_registry WHERE company_id=$1 AND line_id=$2`, companyID, sourceID).Scan(&sourceDocumentID, &sourceQuantity, &sourceBaseQuantity, &sourceLineType, &sourceAggregateType); errors.Is(err, pgx.ErrNoRows) {
			return commercialError(CommercialErrorInvalidRelation, "kaynak satır bulunamadı", "source_line_id", index+1)
		} else if err != nil {
			return err
		}
		if !commercialSourceLineBelongsToDocuments(sourceDocumentID, sourceIDs) {
			return commercialError(CommercialErrorInvalidRelation, "kaynak satır kaynak belgeye ait değil", "source_line_id", index+1)
		}
		if !strings.EqualFold(strings.TrimSpace(sourceLineType), strings.TrimSpace(targetLineType)) {
			return commercialError(CommercialErrorInvalidRelation, "kaynak ve hedef satır türleri eşleşmiyor", "allocations", index+1)
		}
		sourceProductID, sourceVariantID, productErr := commercialLineProduct(ctx, tx, companyID, sourceAggregateType, sourceID)
		if productErr != nil {
			return productErr
		}
		targetProductID, targetVariantID, productErr := commercialLineProduct(ctx, tx, companyID, targetAggregateType, targetID)
		if productErr != nil {
			return productErr
		}
		if sourceProductID != targetProductID || sourceVariantID != targetVariantID {
			return commercialError(CommercialErrorInvalidRelation, "kaynak ve hedef ürün kartları eşleşmiyor", "allocations", index+1)
		}
		if kind == SalesDispatch && sourceAggregateType == string(SalesOrder) && sourceLineType != "PRODUCT" {
			return commercialError(CommercialErrorInvalidRelation, "satış irsaliyesine yalnızca ürün sipariş satırları taşınabilir", "allocations", index+1)
		}
		var alreadyAllocated string
		if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(base_quantity),0)::text FROM commercial_line_allocations WHERE company_id=$1 AND source_line_id=$2 AND allocation_type=$3 AND status='CONSUMED'`, companyID, sourceID, allocationType).Scan(&alreadyAllocated); err != nil {
			return err
		}
		if decimalCompareCommercial(addCommercial(alreadyAllocated, baseQuantity), sourceBaseQuantity) > 0 {
			return commercialError(CommercialErrorOverFulfillment, "kaynak satır miktarı aşıldı", "quantity", index+1)
		}
		if kind == SalesInvoice && sourceLineType == "PRODUCT" && (sourceAggregateType == string(SalesOrder) || sourceAggregateType == string(SalesDispatch)) {
			orderLineID := sourceID
			if sourceAggregateType == string(SalesDispatch) {
				if resolved, resolveErr := commercialLineSourceLineID(ctx, tx, companyID, sourceAggregateType, sourceID); resolveErr != nil {
					return resolveErr
				} else if resolved != "" {
					orderLineID = resolved
				}
			}
			var fulfilledQuantity string
			if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(base_quantity),0)::text FROM commercial_line_allocations WHERE company_id=$1 AND source_line_id=$2 AND allocation_type='FULFILLMENT' AND status='CONSUMED'`, companyID, orderLineID).Scan(&fulfilledQuantity); err != nil {
				return err
			}
			invoicedTotal, err := commercialOrderLineInvoicedTotal(ctx, tx, companyID, orderLineID)
			if err != nil {
				return err
			}
			if decimalCompareCommercial(addCommercial(invoicedTotal, baseQuantity), fulfilledQuantity) > 0 {
				return commercialError(CommercialErrorOverFulfillment, "siparişin karşılanmış miktarı faturalanabilir miktarı aşamaz", "quantity", index+1)
			}
		}
		// Validate the target side while the draft is created. Waiting until
		// posting makes a malformed source relation look valid in the UI and
		// produces the generic 500 reported by callers.
		var targetAllocated string
		if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(base_quantity),0)::text FROM commercial_line_allocations WHERE company_id=$1 AND target_line_id=$2`, companyID, targetID).Scan(&targetAllocated); err != nil {
			return err
		}
		if decimalCompareCommercial(addCommercial(targetAllocated, baseQuantity), targetBaseQuantity) > 0 {
			return commercialError(CommercialErrorInvalidRelation, "hedef satır miktarı aşıldı", "quantity", index+1)
		}
		// Draft allocations are RESERVED: they trace the source relation and feed
		// the editor's remaining calculation but do not bind the source line. The
		// post transaction (consumeCommercialAllocationsTx) flips them to CONSUMED
		// under a source-line lock.
		if _, err := tx.Exec(ctx, `INSERT INTO commercial_line_allocations(id,company_id,source_line_id,target_line_id,allocation_type,quantity,base_quantity,status) VALUES($1,$2,$3,$4,$5,$6,$7,'RESERVED')`, uuid.NewString(), companyID, sourceID, targetID, allocationType, quantity, baseQuantity); err != nil {
			return mapSalesConstraint(err)
		}
	}
	return nil
}

func commercialSourceTypeAllowed(kind CommercialKind, sourceKind, documentType string) bool {
	sourceKind = strings.ToUpper(strings.TrimSpace(sourceKind))
	documentType = strings.ToUpper(strings.TrimSpace(documentType))
	switch kind {
	case SalesOrder:
		return sourceKind == "QUOTE" && documentType == "SALES_QUOTE"
	case SalesDispatch:
		return sourceKind == "ORDER" && documentType == "SALES_ORDER"
	case SalesInvoice:
		return (sourceKind == "DISPATCH" && documentType == "SALES_DELIVERY") || (sourceKind == "ORDER" && documentType == "SALES_ORDER")
	case SalesReturn:
		return (sourceKind == "INVOICE" && documentType == "SALES_INVOICE") || (sourceKind == "DISPATCH" && documentType == "SALES_DELIVERY")
	default:
		return false
	}
}

func commercialSourceStateAllowed(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, companyID, sourceID string, targetKind CommercialKind, documentType string) (bool, error) {
	var status string
	var err error
	switch strings.ToUpper(strings.TrimSpace(documentType)) {
	case "SALES_QUOTE":
		err = q.QueryRow(ctx, `SELECT status FROM sales_quotes WHERE company_id=$1 AND id=$2`, companyID, sourceID).Scan(&status)
		if err == nil {
			return status == "ACCEPTED", nil
		}
	case "SALES_ORDER":
		err = q.QueryRow(ctx, `SELECT status FROM sales_orders WHERE company_id=$1 AND id=$2`, companyID, sourceID).Scan(&status)
		if err == nil {
			if targetKind == SalesInvoice {
				return status == "CONFIRMED" || status == "PARTIALLY_FULFILLED" || status == "FULFILLED", nil
			}
			return status == "CONFIRMED" || status == "PARTIALLY_FULFILLED", nil
		}
	case "SALES_DELIVERY":
		err = q.QueryRow(ctx, `SELECT status FROM sales_dispatches WHERE company_id=$1 AND id=$2`, companyID, sourceID).Scan(&status)
		if err == nil {
			return status == "POSTED", nil
		}
	case "SALES_INVOICE":
		err = q.QueryRow(ctx, `SELECT status FROM sales_invoices WHERE company_id=$1 AND id=$2`, companyID, sourceID).Scan(&status)
		if err == nil {
			return status == "POSTED", nil
		}
	default:
		return false, commercialError(CommercialErrorInvalidRelation, "kaynak belge türü tanınmıyor", "source_document_id", 0)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return false, commercialError(CommercialErrorInvalidRelation, "kaynak typed belge bulunamadı", "source_document_id", 0)
	}
	return false, err
}

func commercialSourceLineBelongsToDocuments(documentID string, sourceIDs []string) bool {
	for _, sourceID := range sourceIDs {
		if documentID == sourceID {
			return true
		}
	}
	return false
}

func commercialSourceIDs(input CommercialDocumentInput) []string {
	result := make([]string, 0, 1+len(input.SourceDocumentIDs))
	seen := map[string]struct{}{}
	for _, value := range append([]string{input.SourceDocumentID}, input.SourceDocumentIDs...) {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func relationToAllocation(kind CommercialKind) string {
	switch kind {
	case SalesDispatch:
		return "FULFILLMENT"
	case SalesInvoice:
		return "INVOICING"
	case SalesReturn:
		return "RETURN"
	default:
		return "FULFILLMENT"
	}
}

func insertCommercialStatusHistoryTx(ctx context.Context, tx pgx.Tx, session identity.Session, kind CommercialKind, id, from, to, reason string) error {
	_, err := tx.Exec(ctx, `INSERT INTO commercial_status_history(id,company_id,aggregate_type,document_id,from_status,to_status,reason,actor_user_id) VALUES($1,$2,$3,$4,NULLIF($5,''),$6,$7,$8)`, uuid.NewString(), session.CurrentCompanyID, kind, id, from, to, strings.TrimSpace(reason), session.User.ID)
	return err
}

func scanCommercialHeader(row interface{ Scan(...any) error }, spec commercialSpec) (CommercialDocument, error) {
	var item CommercialDocument
	var warehouseID, sourceID, financeID, postKey, cancellationReason *string
	if err := row.Scan(&item.ID, &item.CompanyID, &item.DocumentID, &item.DocumentNo, &item.BranchID, &warehouseID, &item.PartyID, &item.PartyCode, &item.PartyName, &item.DocumentDate, &item.DueDate, &item.ValidUntil, &item.CurrencyCode, &item.ExchangeRate, &item.Notes, &item.Reason, &item.Status, &item.SourceKind, &sourceID, &item.Subtotal, &item.DiscountTotal, &item.TaxTotal, &item.WithholdingTotal, &item.GrandTotal, &item.PayableTotal, &item.SalesRepUserID, &item.PaymentTermID, &financeID, &postKey, &item.PostedAt, &item.CancelledAt, &cancellationReason, &item.CreatedBy, &item.UpdatedBy, &item.CreatedAt, &item.UpdatedAt, &item.Version, &item.DefaultWarehouseName, &item.DefaultWarehouseCode); err != nil {
		return CommercialDocument{}, err
	}
	item.Kind, item.DocumentTypeCode = spec.kind, spec.typeCode
	item.DefaultWarehouseID, item.SourceDocumentID, item.FinancePostingID, item.PostIdempotencyKey, item.CancellationReason = warehouseID, sourceID, financeID, postKey, cancellationReason
	item.Lines = []CommercialLine{}
	return item, nil
}

func commercialReasonExpression(spec commercialSpec) string {
	if spec.kind == SalesReturn {
		return "t.reason"
	}
	return "''::text"
}

func loadCommercialHeaderTx(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, spec commercialSpec, companyID, id string, forUpdate bool) (CommercialDocument, error) {
	query := fmt.Sprintf(`SELECT t.id,t.company_id,t.document_id,t.document_no,t.branch_id,t.default_warehouse_id,t.party_id,COALESCE(p.code,''),COALESCE(p.display_name,''),t.document_date,t.due_date,t.valid_until,t.currency_code,t.exchange_rate::text,t.notes,%s,t.status,t.source_kind,t.source_document_id,t.subtotal::text,t.discount_total::text,t.tax_total::text,t.withholding_total::text,t.grand_total::text,t.payable_total::text,t.sales_rep_user_id,t.payment_term_id,t.finance_posting_id,t.post_idempotency_key,t.posted_at,t.cancelled_at,t.cancellation_reason,t.created_by,t.updated_by,t.created_at,t.updated_at,t.version,COALESCE((SELECT w.name FROM warehouses w WHERE w.company_id=t.company_id AND w.id=t.default_warehouse_id),''),COALESCE((SELECT w.code FROM warehouses w WHERE w.company_id=t.company_id AND w.id=t.default_warehouse_id),'') FROM %s t JOIN parties p ON p.company_id=t.company_id AND p.id=t.party_id WHERE t.company_id=$1 AND t.id=$2`, commercialReasonExpression(spec), spec.table)
	if forUpdate {
		query += " FOR UPDATE"
	}
	return scanCommercialHeader(q.QueryRow(ctx, query, companyID, id), spec)
}

func loadCommercialLines(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, companyID string, spec commercialSpec, documentID string) ([]CommercialLine, error) {
	invoicingBase := `GREATEST(l.base_quantity-COALESCE((SELECT SUM(a.base_quantity) FROM commercial_line_allocations a WHERE a.company_id=l.company_id AND a.source_line_id=l.id AND a.allocation_type='INVOICING' AND a.status='CONSUMED'),0),0)`
	fulfillmentBase := `GREATEST(l.base_quantity-COALESCE((SELECT SUM(a.base_quantity) FROM commercial_line_allocations a WHERE a.company_id=l.company_id AND a.source_line_id=l.id AND a.allocation_type='FULFILLMENT' AND a.status='CONSUMED'),0),0)`
	if spec.kind == SalesOrder {
		// Product quantities become invoiceable only after fulfillment. Service
		// quantities do not require a stock fulfillment and remain invoiceable
		// against the ordered quantity. A product's "already invoiced" total
		// must include invoices sourced from a dispatch of this line, not just
		// invoices sourced from the order directly -- otherwise invoicing from
		// the dispatch leaves the order looking un-invoiced and offers the
		// same shipped quantity again.
		invoicedViaAnySource := `(COALESCE((SELECT SUM(a.base_quantity) FROM commercial_line_allocations a WHERE a.company_id=l.company_id AND a.source_line_id=l.id AND a.allocation_type='INVOICING' AND a.status='CONSUMED'),0)+COALESCE((SELECT SUM(a2.base_quantity) FROM commercial_line_allocations a2 JOIN commercial_line_allocations f ON f.company_id=a2.company_id AND f.target_line_id=a2.source_line_id AND f.allocation_type='FULFILLMENT' AND f.status='CONSUMED' WHERE f.company_id=l.company_id AND f.source_line_id=l.id AND a2.allocation_type='INVOICING' AND a2.status='CONSUMED'),0))`
		invoicingBase = `(CASE WHEN l.line_type='PRODUCT' THEN GREATEST(COALESCE((SELECT SUM(a.base_quantity) FROM commercial_line_allocations a WHERE a.company_id=l.company_id AND a.source_line_id=l.id AND a.allocation_type='FULFILLMENT' AND a.status='CONSUMED'),0)-` + invoicedViaAnySource + `,0) ELSE ` + invoicingBase + ` END)`
		// Service lines never ship, so they never carry a dispatch remainder —
		// otherwise the editor would offer to convert them into a delivery note,
		// which the allocation layer then rejects.
		fulfillmentBase = `(CASE WHEN l.line_type='SERVICE' THEN 0 ELSE ` + fulfillmentBase + ` END)`
	} else if spec.kind == SalesDispatch {
		// A dispatch line's own "already invoiced" status must also count an
		// invoice sourced directly from the order line it fulfilled -- the
		// same shared ledger used above, just resolved through the dispatch
		// line's own source_line_id (the order line). Without this, invoicing
		// from the order leaves the dispatch looking un-invoiced and still
		// offering itself as an invoice source for the same shipped quantity.
		invoicedViaAnySource := `(COALESCE((SELECT SUM(a.base_quantity) FROM commercial_line_allocations a WHERE a.company_id=l.company_id AND a.source_line_id=l.source_line_id AND a.allocation_type='INVOICING' AND a.status='CONSUMED'),0)+COALESCE((SELECT SUM(a2.base_quantity) FROM commercial_line_allocations a2 JOIN commercial_line_allocations f ON f.company_id=a2.company_id AND f.target_line_id=a2.source_line_id AND f.allocation_type='FULFILLMENT' AND f.status='CONSUMED' WHERE f.company_id=l.company_id AND f.source_line_id=l.source_line_id AND a2.allocation_type='INVOICING' AND a2.status='CONSUMED'),0))`
		invoicingBase = `(CASE WHEN l.source_line_id IS NULL THEN ` + invoicingBase + ` ELSE GREATEST(l.base_quantity-` + invoicedViaAnySource + `,0) END)`
	}
	rows, err := q.Query(ctx, fmt.Sprintf(`SELECT l.id,l.line_no,l.line_type,l.product_id,l.variant_id,l.variant_code_snapshot,l.variant_attributes_snapshot,l.warehouse_id,COALESCE(lw.name,''),COALESCE(lw.code,''),l.unit_code,l.quantity::text,l.base_quantity::text,l.conversion_factor::text,
COALESCE((%s)/NULLIF(l.conversion_factor,0),0)::text,
COALESCE((%s),0)::text,
COALESCE((%s)/NULLIF(l.conversion_factor,0),0)::text,
COALESCE((%s),0)::text,
COALESCE(GREATEST(l.base_quantity-COALESCE((SELECT SUM(a.base_quantity) FROM commercial_line_allocations a WHERE a.company_id=l.company_id AND a.source_line_id=l.id AND a.allocation_type='RETURN' AND a.status='CONSUMED'),0),0)/NULLIF(l.conversion_factor,0),0)::text,
COALESCE(GREATEST(l.base_quantity-COALESCE((SELECT SUM(a.base_quantity) FROM commercial_line_allocations a WHERE a.company_id=l.company_id AND a.source_line_id=l.id AND a.allocation_type='RETURN' AND a.status='CONSUMED'),0),0),0)::text,
l.price_source,l.price_list_snapshot,l.discount_components_snapshot,l.tax_snapshot,l.tax_components_snapshot,l.description_snapshot,l.source_line_id,l.unit_price::text,l.gross_amount::text,l.discount_amount::text,l.net_amount::text,l.tax_amount::text,l.withholding_amount::text,l.line_total::text,l.payable_amount::text FROM %s l LEFT JOIN warehouses lw ON lw.company_id=l.company_id AND lw.id=l.warehouse_id WHERE l.company_id=$1 AND l.document_id=$2 ORDER BY l.line_no`, fulfillmentBase, fulfillmentBase, invoicingBase, invoicingBase, spec.lineTable), companyID, documentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []CommercialLine{}
	for rows.Next() {
		var line CommercialLine
		var productID, variantID, warehouseID, sourceLineID *string
		var priceList, discounts, tax, taxComponents, variantAttributes []byte
		if err = rows.Scan(&line.ID, &line.LineNo, &line.LineType, &productID, &variantID, &line.VariantCode, &variantAttributes, &warehouseID, &line.WarehouseName, &line.WarehouseCode, &line.UnitCode, &line.Quantity, &line.BaseQuantity, &line.ConversionFactor, &line.RemainingFulfillmentQuantity, &line.RemainingFulfillmentBaseQuantity, &line.RemainingInvoicingQuantity, &line.RemainingInvoicingBaseQuantity, &line.RemainingReturnQuantity, &line.RemainingReturnBaseQuantity, &line.PriceSource, &priceList, &discounts, &tax, &taxComponents, &line.Description, &sourceLineID, &line.UnitPrice, &line.GrossAmount, &line.DiscountAmount, &line.NetAmount, &line.TaxAmount, &line.WithholdingAmount, &line.LineTotal, &line.PayableAmount); err != nil {
			return nil, err
		}
		line.ProductID, line.VariantID, line.WarehouseID, line.SourceLineID = productID, variantID, warehouseID, sourceLineID
		_ = json.Unmarshal(priceList, &line.PriceListSnapshot)
		_ = json.Unmarshal(discounts, &line.DiscountComponents)
		_ = json.Unmarshal(tax, &line.TaxSnapshot)
		_ = json.Unmarshal(taxComponents, &line.TaxComponentsSnapshot)
		_ = json.Unmarshal(variantAttributes, &line.VariantAttributes)
		if line.VariantAttributes == nil {
			line.VariantAttributes = map[string]any{}
		}
		result = append(result, line)
	}
	return result, rows.Err()
}

func loadCommercialSources(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}, session identity.Session, documentID string) ([]SourceDocumentReference, error) {
	rows, err := q.Query(ctx, `
		SELECT s.source_document_id,
		       d.document_no,
		       d.document_type_code,
		       CASE d.document_type_code
		           WHEN 'SALES_QUOTE' THEN 'QUOTE'
		           WHEN 'SALES_ORDER' THEN 'ORDER'
		           WHEN 'SALES_DELIVERY' THEN 'DISPATCH'
		           WHEN 'SALES_INVOICE' THEN 'INVOICE'
		           WHEN 'SALES_RETURN_INVOICE' THEN 'RETURN'
		           ELSE dt.kind
		       END AS source_kind,
		       s.relation_type,
		       d.branch_id,
		       d.warehouse_id,
		       COALESCE(sq.status, so.status, sd.status, si.status, sr.status, d.status) AS source_status
		  FROM commercial_document_sources s
		  JOIN documents d
		    ON d.company_id=s.company_id AND d.id=s.source_document_id
		  JOIN document_types dt ON dt.code=d.document_type_code
		  LEFT JOIN sales_quotes sq
		    ON sq.company_id=d.company_id AND sq.document_id=d.id AND d.document_type_code='SALES_QUOTE'
		  LEFT JOIN sales_orders so
		    ON so.company_id=d.company_id AND so.document_id=d.id AND d.document_type_code='SALES_ORDER'
		  LEFT JOIN sales_dispatches sd
		    ON sd.company_id=d.company_id AND sd.document_id=d.id AND d.document_type_code='SALES_DELIVERY'
		  LEFT JOIN sales_invoices si
		    ON si.company_id=d.company_id AND si.document_id=d.id AND d.document_type_code='SALES_INVOICE'
		  LEFT JOIN sales_returns sr
		    ON sr.company_id=d.company_id AND sr.document_id=d.id AND d.document_type_code='SALES_RETURN_INVOICE'
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
		if err := ensureCommercialReadScope(ctx, q, session, branchID, warehouseID); err != nil {
			if errors.Is(err, identity.ErrForbidden) {
				continue
			}
			return nil, err
		}
		source.Kind = sourceKind
		source.Direction = "SOURCE"
		source.Status = strings.ToUpper(strings.TrimSpace(sourceStatus))
		if sourceSpec, ok := commercialSpecForSourceType(source.DocumentTypeCode); ok {
			source.Kind = sourceKindForCommercial(sourceSpec.kind)
			source.LifecycleStatus = commercialLifecycleStatus(sourceSpec.kind, source.Status)
			// A source detail is only advertised when the caller can also read
			// its line-level warehouse scope. This avoids leaking a source
			// document number for an otherwise inaccessible multi-warehouse
			// document.
			sourceLines, lineErr := loadCommercialLines(ctx, q, session.CurrentCompanyID, sourceSpec, source.ID)
			if lineErr != nil {
				return nil, lineErr
			}
			if lineErr = ensureCommercialLineReadScopes(ctx, q, session, branchID, sourceLines); lineErr != nil {
				if errors.Is(lineErr, identity.ErrForbidden) {
					continue
				}
				return nil, lineErr
			}
		} else {
			source.LifecycleStatus = source.Status
		}
		result = append(result, source)
	}
	return result, nil
}

func commercialSpecForSourceType(documentTypeCode string) (commercialSpec, bool) {
	switch strings.ToUpper(strings.TrimSpace(documentTypeCode)) {
	case "SALES_QUOTE":
		return commercialSpecFor(SalesQuote)
	case "SALES_ORDER":
		return commercialSpecFor(SalesOrder)
	case "SALES_DELIVERY":
		return commercialSpecFor(SalesDispatch)
	case "SALES_INVOICE":
		return commercialSpecFor(SalesInvoice)
	case "SALES_RETURN_INVOICE":
		return commercialSpecFor(SalesReturn)
	default:
		return commercialSpec{}, false
	}
}

func sourceKindForCommercial(kind CommercialKind) string {
	switch kind {
	case SalesQuote:
		return "QUOTE"
	case SalesOrder:
		return "ORDER"
	case SalesDispatch:
		return "DISPATCH"
	case SalesInvoice:
		return "INVOICE"
	case SalesReturn:
		return "RETURN"
	default:
		return ""
	}
}

func decimalCompareCommercial(left, right string) int {
	a, aok := new(big.Rat).SetString(strings.TrimSpace(left))
	b, bok := new(big.Rat).SetString(strings.TrimSpace(right))
	if !aok || !bok {
		return 0
	}
	return a.Cmp(b)
}

func ensureCommercialScope(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, session identity.Session, branchID string, warehouseID *string) error {
	if uuid.Validate(strings.TrimSpace(branchID)) != nil {
		return identity.ErrForbidden
	}
	var branchOK bool
	if err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM branches b WHERE b.company_id=$1 AND b.id=$2 AND b.is_active AND (NOT EXISTS(SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=$1 AND bs.user_id=$3) OR EXISTS(SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=$1 AND bs.user_id=$3 AND bs.branch_id=b.id)))`, session.CurrentCompanyID, branchID, session.User.ID).Scan(&branchOK); err != nil {
		return err
	}
	if !branchOK {
		return identity.ErrForbidden
	}
	if warehouseID == nil || strings.TrimSpace(*warehouseID) == "" {
		return nil
	}
	if uuid.Validate(strings.TrimSpace(*warehouseID)) != nil {
		return identity.ErrForbidden
	}
	var warehouseOK bool
	if err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM warehouses w WHERE w.company_id=$1 AND w.id=$2 AND w.branch_id=$3 AND w.is_active AND (NOT EXISTS(SELECT 1 FROM membership_warehouse_scopes ws WHERE ws.company_id=$1 AND ws.user_id=$4) OR EXISTS(SELECT 1 FROM membership_warehouse_scopes ws WHERE ws.company_id=$1 AND ws.user_id=$4 AND ws.warehouse_id=w.id)))`, session.CurrentCompanyID, *warehouseID, branchID, session.User.ID).Scan(&warehouseOK); err != nil {
		return err
	}
	if !warehouseOK {
		return identity.ErrForbidden
	}
	return nil
}

// Read scope intentionally ignores active flags. Existing drafts and posted
// documents remain visible after a master is archived; active eligibility is
// enforced by create/update/post commands.
func ensureCommercialReadScope(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, session identity.Session, branchID string, warehouseID *string) error {
	if uuid.Validate(strings.TrimSpace(branchID)) != nil {
		return identity.ErrForbidden
	}
	var branchOK bool
	if err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM branches b WHERE b.company_id=$1 AND b.id=$2 AND (NOT EXISTS(SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=$1 AND bs.user_id=$3) OR EXISTS(SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=$1 AND bs.user_id=$3 AND bs.branch_id=b.id)))`, session.CurrentCompanyID, branchID, session.User.ID).Scan(&branchOK); err != nil {
		return err
	}
	if !branchOK {
		return identity.ErrForbidden
	}
	if warehouseID == nil || strings.TrimSpace(*warehouseID) == "" {
		return nil
	}
	if uuid.Validate(strings.TrimSpace(*warehouseID)) != nil {
		return identity.ErrForbidden
	}
	var warehouseOK bool
	if err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM warehouses w WHERE w.company_id=$1 AND w.id=$2 AND w.branch_id=$3 AND (NOT EXISTS(SELECT 1 FROM membership_warehouse_scopes ws WHERE ws.company_id=$1 AND ws.user_id=$4) OR EXISTS(SELECT 1 FROM membership_warehouse_scopes ws WHERE ws.company_id=$1 AND ws.user_id=$4 AND ws.warehouse_id=w.id)))`, session.CurrentCompanyID, *warehouseID, branchID, session.User.ID).Scan(&warehouseOK); err != nil {
		return err
	}
	if !warehouseOK {
		return identity.ErrForbidden
	}
	return nil
}

func ensureCommercialLineScopes(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, session identity.Session, branchID string, lines []CommercialLine) error {
	for _, line := range lines {
		if strings.ToUpper(strings.TrimSpace(line.LineType)) != "PRODUCT" {
			continue
		}
		if line.WarehouseID == nil || strings.TrimSpace(*line.WarehouseID) == "" {
			return commercialError(CommercialErrorWarehouseRequired, "ürün satırı için depo gereklidir", "warehouse_id", line.LineNo)
		}
		if err := ensureCommercialScope(ctx, q, session, branchID, line.WarehouseID); err != nil {
			return &CommercialError{Code: CommercialErrorWarehouseUnauthorized, Field: "warehouse_id", Line: line.LineNo, Err: err}
		}
	}
	return nil
}

func ensureCommercialLineReadScopes(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, session identity.Session, branchID string, lines []CommercialLine) error {
	for _, line := range lines {
		if strings.ToUpper(strings.TrimSpace(line.LineType)) != "PRODUCT" {
			continue
		}
		if line.WarehouseID == nil || strings.TrimSpace(*line.WarehouseID) == "" {
			// Read access must not turn an incomplete draft into an invisible
			// document. Warehouse presence is a POST invariant; an assigned
			// warehouse is still checked below for company/branch/user scope.
			continue
		}
		if err := ensureCommercialReadScope(ctx, q, session, branchID, line.WarehouseID); err != nil {
			return &CommercialError{Code: CommercialErrorWarehouseUnauthorized, Field: "warehouse_id", Line: line.LineNo, Err: err}
		}
	}
	return nil
}

func validateCommercialPostMastersTx(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, session identity.Session, item CommercialDocument) error {
	var partyActive, partyRole bool
	if err := q.QueryRow(ctx, `SELECT is_active,is_customer FROM parties WHERE company_id=$1 AND id=$2 FOR UPDATE`, session.CurrentCompanyID, item.PartyID).Scan(&partyActive, &partyRole); errors.Is(err, pgx.ErrNoRows) {
		return commercialError(CommercialErrorInvalidPartyRole, "cari bulunamadı", "party_id", 0)
	} else if err != nil {
		return err
	}
	if !partyActive {
		return commercialError(CommercialErrorPartyInactive, "cari pasif olduğu için belge kesinleştirilemez", "party_id", 0)
	}
	if !partyRole {
		return commercialError(CommercialErrorInvalidPartyRole, "satış işlemi için müşteri carisi gereklidir", "party_id", 0)
	}

	for _, line := range item.Lines {
		lineType := strings.ToUpper(strings.TrimSpace(line.LineType))
		if line.ProductID != nil && strings.TrimSpace(*line.ProductID) != "" {
			var productKind string
			var active, hasVariants bool
			if err := q.QueryRow(ctx, `SELECT kind::text,is_active,variants_enabled OR EXISTS(SELECT 1 FROM product_variants pv WHERE pv.company_id=products.company_id AND pv.product_id=products.id) FROM products WHERE company_id=$1 AND id=$2 FOR SHARE`, session.CurrentCompanyID, *line.ProductID).Scan(&productKind, &active, &hasVariants); errors.Is(err, pgx.ErrNoRows) {
				return commercialError(CommercialErrorInvalidRelation, "ürün bulunamadı", "product_id", line.LineNo)
			} else if err != nil {
				return err
			}
			if !active {
				return commercialError(CommercialErrorProductInactive, "ürün pasif olduğu için belge kesinleştirilemez", "product_id", line.LineNo)
			}
			expectedKind := "PHYSICAL"
			if lineType == "SERVICE" {
				expectedKind = "SERVICE"
			}
			if productKind != expectedKind {
				return commercialError(CommercialErrorInvalidRelation, "ürün satır türüyle eşleşmiyor", "product_id", line.LineNo)
			}
			if lineType == "PRODUCT" && hasVariants && (line.VariantID == nil || strings.TrimSpace(*line.VariantID) == "") {
				return commercialError(CommercialErrorVariantRequired, "varyantlı ürün için varyant seçilmelidir", "variant_id", line.LineNo)
			}
		}
		if line.VariantID != nil && strings.TrimSpace(*line.VariantID) != "" {
			var variantActive bool
			if err := q.QueryRow(ctx, `SELECT is_active FROM product_variants WHERE company_id=$1 AND id=$2 AND product_id=$3 FOR SHARE`, session.CurrentCompanyID, *line.VariantID, valueOrEmptyCommercial(line.ProductID)).Scan(&variantActive); errors.Is(err, pgx.ErrNoRows) {
				return commercialError(CommercialErrorInvalidRelation, "varyant ürünle eşleşmiyor", "variant_id", line.LineNo)
			} else if err != nil {
				return err
			}
			if !variantActive {
				return commercialError(CommercialErrorVariantInactive, "varyant pasif olduğu için belge kesinleştirilemez", "variant_id", line.LineNo)
			}
		}
		if lineType != "PRODUCT" {
			continue
		}
		if line.WarehouseID == nil || strings.TrimSpace(*line.WarehouseID) == "" {
			return commercialError(CommercialErrorWarehouseRequired, "ürün satırı için depo gereklidir", "warehouse_id", line.LineNo)
		}
		var warehouseActive bool
		if err := q.QueryRow(ctx, `SELECT is_active FROM warehouses WHERE company_id=$1 AND id=$2 AND branch_id=$3 FOR SHARE`, session.CurrentCompanyID, *line.WarehouseID, item.BranchID).Scan(&warehouseActive); errors.Is(err, pgx.ErrNoRows) {
			return &CommercialError{Code: CommercialErrorWarehouseUnauthorized, Field: "warehouse_id", Line: line.LineNo, Err: identity.ErrForbidden}
		} else if err != nil {
			return err
		}
		if !warehouseActive {
			return commercialError(CommercialErrorWarehouseInactive, "depo pasif olduğu için belge kesinleştirilemez", "warehouse_id", line.LineNo)
		}
		if err := ensureCommercialScope(ctx, q, session, item.BranchID, line.WarehouseID); err != nil {
			return &CommercialError{Code: CommercialErrorWarehouseUnauthorized, Field: "warehouse_id", Line: line.LineNo, Err: err}
		}
	}
	return nil
}

func commercialLineProduct(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, companyID, aggregateType, lineID string) (string, string, error) {
	table := ""
	switch strings.ToUpper(strings.TrimSpace(aggregateType)) {
	case string(SalesQuote):
		table = "sales_quote_lines"
	case string(SalesOrder):
		table = "sales_order_lines"
	case string(SalesDispatch):
		table = "sales_dispatch_lines"
	case string(SalesInvoice):
		table = "sales_invoice_lines"
	case string(SalesReturn):
		table = "sales_return_lines"
	default:
		return "", "", commercialError(CommercialErrorInvalidRelation, "satır türü tanınmıyor", "allocations", 0)
	}
	var productID, variantID *string
	if err := q.QueryRow(ctx, fmt.Sprintf("SELECT product_id,variant_id FROM %s WHERE company_id=$1 AND id=$2", table), companyID, lineID).Scan(&productID, &variantID); errors.Is(err, pgx.ErrNoRows) {
		return "", "", commercialError(CommercialErrorInvalidRelation, "tahsis satırı bulunamadı", "allocations", 0)
	} else if err != nil {
		return "", "", err
	}
	return valueOrEmptyCommercial(productID), valueOrEmptyCommercial(variantID), nil
}

// commercialLineSourceLineID returns the line's own source_line_id (the line
// it was created from, one hop back), or "" when it has none. Used to walk a
// dispatch line back to the order line it fulfilled, so invoicing-from-order
// and invoicing-from-dispatch are checked against one shared ledger instead of
// two independent ones.
func commercialLineSourceLineID(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, companyID, aggregateType, lineID string) (string, error) {
	table := ""
	switch strings.ToUpper(strings.TrimSpace(aggregateType)) {
	case string(SalesQuote):
		table = "sales_quote_lines"
	case string(SalesOrder):
		table = "sales_order_lines"
	case string(SalesDispatch):
		table = "sales_dispatch_lines"
	case string(SalesInvoice):
		table = "sales_invoice_lines"
	case string(SalesReturn):
		table = "sales_return_lines"
	default:
		return "", commercialError(CommercialErrorInvalidRelation, "satır türü tanınmıyor", "allocations", 0)
	}
	var sourceLineID *string
	if err := q.QueryRow(ctx, fmt.Sprintf("SELECT source_line_id FROM %s WHERE company_id=$1 AND id=$2", table), companyID, lineID).Scan(&sourceLineID); errors.Is(err, pgx.ErrNoRows) {
		return "", commercialError(CommercialErrorInvalidRelation, "tahsis satırı bulunamadı", "allocations", 0)
	} else if err != nil {
		return "", err
	}
	return valueOrEmptyCommercial(sourceLineID), nil
}

// commercialOrderLineInvoicedTotal sums every INVOICING allocation that
// ultimately bills against orderLineID -- both invoices sourced directly from
// the order line and invoices sourced from a dispatch line that was fulfilled
// from it. A dispatch-sourced invoice's allocation is recorded against the
// dispatch line, not the order line, so without the second term this total
// would miss it entirely and let the same shipped quantity be invoiced twice
// (once from the dispatch, once again from the order, or vice versa).
func commercialOrderLineInvoicedTotal(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, companyID, orderLineID string) (string, error) {
	var total string
	if err := q.QueryRow(ctx, `SELECT (
		COALESCE((SELECT SUM(a.base_quantity) FROM commercial_line_allocations a WHERE a.company_id=$1 AND a.source_line_id=$2 AND a.allocation_type='INVOICING' AND a.status='CONSUMED'),0)
		+ COALESCE((SELECT SUM(a2.base_quantity) FROM commercial_line_allocations a2 JOIN commercial_line_allocations f ON f.company_id=a2.company_id AND f.target_line_id=a2.source_line_id AND f.allocation_type='FULFILLMENT' AND f.status='CONSUMED' WHERE f.company_id=$1 AND f.source_line_id=$2 AND a2.allocation_type='INVOICING' AND a2.status='CONSUMED'),0)
	)::text`, companyID, orderLineID).Scan(&total); err != nil {
		return "", err
	}
	return total, nil
}

// commercialStockLines builds the stock posting lines for one document,
// excluding service lines. An outbound line (dispatch/invoice) carries no
// cost of its own -- the FIFO layer trigger derives it from whichever layers
// it consumes, which is correct; commercialStockLinesTx fills in a sales
// return's cost afterwards.
func commercialStockLines(lines []CommercialLine) []CommercialStockPostingLine {
	result := make([]CommercialStockPostingLine, 0, len(lines))
	for _, line := range lines {
		if line.LineType != "PRODUCT" || line.ProductID == nil || line.WarehouseID == nil {
			continue
		}
		result = append(result, CommercialStockPostingLine{LineID: line.ID, ProductID: *line.ProductID, VariantID: valueOrEmptyCommercial(line.VariantID), WarehouseID: *line.WarehouseID, Quantity: line.Quantity, BaseQuantity: line.BaseQuantity, ConversionFactor: line.ConversionFactor, UnitCode: line.UnitCode, UnitCost: "", Currency: ""})
	}
	return result
}

// commercialStockLinesTx adds a physical sales return's cost to the lines
// commercialStockLines produced. A sales return always carries a source
// invoice/dispatch line (CONTEXT.md: "Kaynak fatura veya irsaliye
// ilişkisini ... koruyan"), so the original OUT movement's FIFO consumption
// is the return's cost -- the quantity-weighted average of whatever layers
// that sale actually drew from. Leaving UnitCost empty here, as this used to,
// meant apply_stock_cost_layer() opened no cost layer at all for the
// returned quantity, and the FIFO ledger permanently diverged from the
// physical stock ledger the moment a later sale consumed past what the
// (missing) layer accounted for.
func (s *Service) commercialStockLinesTx(ctx context.Context, tx pgx.Tx, session identity.Session, kind CommercialKind, lines []CommercialLine) ([]CommercialStockPostingLine, error) {
	postings := commercialStockLines(lines)
	// Line-level provenance defence: even if the header-level salesCommercialPostsStock
	// rule is ever relaxed, an invoice line that traces back to a dispatch/order
	// line must never post a second physical movement -- the dispatch already
	// owns that effect. A direct invoice line has no source and is kept.
	if kind == SalesInvoice && len(postings) > 0 {
		kept := postings[:0]
		for _, posting := range postings {
			var sourceAggregate string
			err := tx.QueryRow(ctx, `SELECT r.aggregate_type
				FROM sales_invoice_lines l
				JOIN commercial_line_registry r ON r.company_id=l.company_id AND r.line_id=l.source_line_id
				WHERE l.company_id=$1 AND l.id=$2 AND l.source_line_id IS NOT NULL`,
				session.CurrentCompanyID, posting.LineID).Scan(&sourceAggregate)
			if errors.Is(err, pgx.ErrNoRows) {
				kept = append(kept, posting)
				continue
			}
			if err != nil {
				return nil, err
			}
			if sourceAggregate != string(SalesDispatch) && sourceAggregate != string(SalesOrder) {
				kept = append(kept, posting)
			}
		}
		postings = kept
	}
	if kind != SalesReturn {
		return postings, nil
	}
	bySourceLine := make(map[string]*string, len(lines))
	for _, line := range lines {
		bySourceLine[line.ID] = line.SourceLineID
	}
	for index := range postings {
		unitCost, currency, err := resolveSalesReturnLineCostTx(ctx, tx, session.CurrentCompanyID, bySourceLine[postings[index].LineID])
		if err != nil {
			return nil, err
		}
		postings[index].UnitCost, postings[index].Currency = unitCost, currency
	}
	return postings, nil
}

func reservationLines(lines []CommercialLine) []SalesReservationLine {
	result := make([]SalesReservationLine, 0, len(lines))
	for _, line := range lines {
		if line.LineType != "PRODUCT" || line.ProductID == nil || line.WarehouseID == nil {
			continue
		}
		result = append(result, SalesReservationLine{OrderLineID: line.ID, ProductID: *line.ProductID, VariantID: valueOrEmptyCommercial(line.VariantID), WarehouseID: *line.WarehouseID, Quantity: line.BaseQuantity})
	}
	return result
}

func valueOrEmptyCommercial(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func mapCommercialError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, identity.ErrForbidden) {
		return &CommercialError{Code: CommercialErrorWarehouseUnauthorized, Err: err}
	}
	if errors.Is(err, finance.ErrInvoiceAlreadyPosted) {
		return commercialError(CommercialErrorAlreadyPosted, "belge daha önce işlendi", "status", 0)
	}
	if strings.Contains(err.Error(), "commercial allocation exceeds source line capacity") {
		return commercialError(CommercialErrorOverFulfillment, "kaynak satırın kalan miktarı bu belge için yetersiz", "allocations", 0)
	}
	if errors.Is(err, finance.ErrPeriodLocked) {
		return commercialError(CommercialErrorPeriodLocked, "belge tarihi kilitli dönemdedir", "document_date", 0)
	}
	if strings.Contains(strings.ToUpper(err.Error()), "INSUFFICIENT_STOCK") {
		return commercialError(CommercialErrorInsufficientStock, "kullanılabilir stok yetersiz", "lines", 0)
	}
	if strings.Contains(strings.ToUpper(err.Error()), "ALREADY_POSTED") {
		return commercialError(CommercialErrorAlreadyPosted, "belge daha önce işlendi", "status", 0)
	}
	if strings.Contains(strings.ToUpper(err.Error()), "VARIANT_REQUIRED") {
		return commercialError(CommercialErrorVariantRequired, "varyantlı ürün için varyant seçilmelidir", "variant_id", 0)
	}
	if strings.Contains(strings.ToUpper(err.Error()), "VARIANT_INACTIVE") {
		return commercialError(CommercialErrorVariantInactive, "pasif varyant stok işleminde seçilemez", "variant_id", 0)
	}
	if strings.Contains(strings.ToUpper(err.Error()), "VARIANT_PRODUCT_MISMATCH") {
		return commercialError(CommercialErrorInvalidRelation, "varyant seçilen ürünle eşleşmiyor", "variant_id", 0)
	}
	return err
}

func completeCommercialCommand(ctx context.Context, tx pgx.Tx, session identity.Session, meta identity.RequestMeta, id, command string, payload any) error {
	if err := writeAuditAndEventTx(ctx, tx, session, "COMMERCIAL_COMMAND_COMPLETED", command+".completed", "commercial_document", id, meta, payload.(map[string]any)); err != nil {
		return err
	}
	return idempotency.CompleteTx(ctx, tx, session.CurrentCompanyID, meta.IdempotencyKey, http.StatusOK, map[string]string{"document_id": id})
}

func validateCommercialAllocationsTx(ctx context.Context, tx pgx.Tx, companyID, documentID string, kind CommercialKind) error {
	var hasSources bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM commercial_document_sources WHERE company_id=$1 AND document_id=$2)`, companyID, documentID).Scan(&hasSources); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT a.source_line_id,a.target_line_id,COALESCE(SUM(a.base_quantity),0)::text,r.base_quantity::text,r.aggregate_type,r.line_type,t.line_type,t.aggregate_type,a.allocation_type,t.base_quantity::text
FROM commercial_line_allocations a
JOIN commercial_line_registry t ON t.company_id=a.company_id AND t.line_id=a.target_line_id
JOIN commercial_line_registry r ON r.company_id=a.company_id AND r.line_id=a.source_line_id
WHERE a.company_id=$1 AND t.document_id=$2 AND a.status='CONSUMED'
GROUP BY a.source_line_id,a.target_line_id,r.base_quantity,r.aggregate_type,r.line_type,t.line_type,t.aggregate_type,a.allocation_type,t.base_quantity`, companyID, documentID)
	if err != nil {
		return err
	}
	type allocationRow struct {
		sourceID, targetID, allocated, sourceQuantity          string
		sourceType, sourceLineType, targetLineType, targetType string
		allocationType, targetQuantity                         string
	}
	allocations := make([]allocationRow, 0)
	for rows.Next() {
		var allocation allocationRow
		if err = rows.Scan(&allocation.sourceID, &allocation.targetID, &allocation.allocated, &allocation.sourceQuantity, &allocation.sourceType, &allocation.sourceLineType, &allocation.targetLineType, &allocation.targetType, &allocation.allocationType, &allocation.targetQuantity); err != nil {
			rows.Close()
			return err
		}
		allocations = append(allocations, allocation)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	allocatedByTarget := map[string]string{}
	targetBaseByID := map[string]string{}
	for _, allocation := range allocations {
		var sourceAllocated string
		if err = tx.QueryRow(ctx, `SELECT COALESCE(SUM(base_quantity),0)::text FROM commercial_line_allocations WHERE company_id=$1 AND source_line_id=$2 AND allocation_type=$3 AND status='CONSUMED'`, companyID, allocation.sourceID, allocation.allocationType).Scan(&sourceAllocated); err != nil {
			return err
		}
		if decimalCompareCommercial(sourceAllocated, allocation.sourceQuantity) > 0 {
			return commercialError(CommercialErrorOverFulfillment, "kaynak satır miktarı aşıldı", "allocations", 0)
		}
		if kind == SalesInvoice && allocation.sourceLineType == "PRODUCT" && (allocation.sourceType == string(SalesOrder) || allocation.sourceType == string(SalesDispatch)) {
			orderLineID := allocation.sourceID
			if allocation.sourceType == string(SalesDispatch) {
				if resolved, resolveErr := commercialLineSourceLineID(ctx, tx, companyID, allocation.sourceType, allocation.sourceID); resolveErr != nil {
					return resolveErr
				} else if resolved != "" {
					orderLineID = resolved
				}
			}
			var fulfilledQuantity string
			if err = tx.QueryRow(ctx, `SELECT COALESCE(SUM(base_quantity),0)::text FROM commercial_line_allocations WHERE company_id=$1 AND source_line_id=$2 AND allocation_type='FULFILLMENT' AND status='CONSUMED'`, companyID, orderLineID).Scan(&fulfilledQuantity); err != nil {
				return err
			}
			invoicedTotal, err := commercialOrderLineInvoicedTotal(ctx, tx, companyID, orderLineID)
			if err != nil {
				return err
			}
			if decimalCompareCommercial(invoicedTotal, fulfilledQuantity) > 0 {
				return commercialError(CommercialErrorOverFulfillment, "siparişin sevk edilmiş miktarı tekrar faturalanamaz", "allocations", 0)
			}
		}
		if allocation.allocationType != relationToAllocation(kind) {
			return commercialError(CommercialErrorInvalidRelation, "satır tahsis türü belge türüyle eşleşmiyor", "allocations", 0)
		}
		if !strings.EqualFold(strings.TrimSpace(allocation.sourceLineType), strings.TrimSpace(allocation.targetLineType)) {
			return commercialError(CommercialErrorInvalidRelation, "kaynak ve hedef satır türleri eşleşmiyor", "allocations", 0)
		}
		sourceProductID, sourceVariantID, productErr := commercialLineProduct(ctx, tx, companyID, allocation.sourceType, allocation.sourceID)
		if productErr != nil {
			return productErr
		}
		targetProductID, targetVariantID, productErr := commercialLineProduct(ctx, tx, companyID, allocation.targetType, allocation.targetID)
		if productErr != nil {
			return productErr
		}
		if sourceProductID != targetProductID || sourceVariantID != targetVariantID {
			return commercialError(CommercialErrorInvalidRelation, "kaynak ve hedef ürün kartları eşleşmiyor", "allocations", 0)
		}
		if kind == SalesOrder && allocation.sourceType != string(SalesQuote) {
			return commercialError(CommercialErrorInvalidRelation, "sipariş yalnız satış teklifinden oluşturulabilir", "allocations", 0)
		}
		if kind == SalesDispatch && allocation.sourceType != string(SalesOrder) {
			return commercialError(CommercialErrorInvalidRelation, "irsaliye yalnız satış siparişi satırına bağlanabilir", "allocations", 0)
		}
		if kind == SalesInvoice && allocation.sourceType != string(SalesDispatch) && allocation.sourceType != string(SalesOrder) {
			return commercialError(CommercialErrorInvalidRelation, "fatura kaynak belgesi geçersiz", "allocations", 0)
		}
		if kind == SalesReturn && allocation.sourceType != string(SalesDispatch) && allocation.sourceType != string(SalesInvoice) {
			return commercialError(CommercialErrorInvalidRelation, "iade kaynak belgesi geçersiz", "allocations", 0)
		}
		allocatedByTarget[allocation.targetID] = addCommercial(allocatedByTarget[allocation.targetID], allocation.allocated)
		targetBaseByID[allocation.targetID] = allocation.targetQuantity
	}
	if len(allocations) == 0 {
		if hasSources {
			return commercialError(CommercialErrorInvalidRelation, "kaynak belge satır tahsisi içermelidir", "allocations", 0)
		}
		return nil
	}
	targetRows, err := tx.Query(ctx, `SELECT line_id,base_quantity::text FROM commercial_line_registry WHERE company_id=$1 AND document_id=$2`, companyID, documentID)
	if err != nil {
		return err
	}
	defer targetRows.Close()
	for targetRows.Next() {
		var targetID, targetQuantity string
		if err = targetRows.Scan(&targetID, &targetQuantity); err != nil {
			return err
		}
		if targetBaseByID[targetID] == "" {
			return commercialError(CommercialErrorInvalidRelation, "her hedef satır kaynak belgeye tahsis edilmelidir", "allocations", 0)
		}
		if decimalCompareCommercial(allocatedByTarget[targetID], targetQuantity) != 0 {
			return commercialError(CommercialErrorInvalidRelation, "hedef satır tahsisi satır miktarını karşılamıyor", "allocations", 0)
		}
	}
	return targetRows.Err()
}

func updateCommercialAnchorStatusTx(ctx context.Context, tx pgx.Tx, companyID, actorUserID, documentID, status, postKey, reason string) error {
	var result pgconn.CommandTag
	var err error
	if status == "POSTED" {
		result, err = tx.Exec(ctx, `UPDATE documents SET status='POSTED',posted_at=COALESCE(posted_at,now()),posted_by=NULLIF($1,'')::uuid,post_idempotency_key=NULLIF($2,''),updated_at=now(),version=version+1 WHERE company_id=$4 AND id=$3 AND status='DRAFT'`, actorUserID, postKey, documentID, companyID)
	} else if status == "CANCELLED" {
		result, err = tx.Exec(ctx, `UPDATE documents SET status='CANCELLED',cancelled_at=now(),cancellation_reason=$1,updated_at=now(),version=version+1 WHERE company_id=$3 AND id=$2 AND status='POSTED'`, reason, documentID, companyID)
	} else {
		return commercialError(CommercialErrorInvalidRelation, "belge ankraj durumu geçersiz", "status", 0)
	}
	if err == nil && result.RowsAffected() != 1 {
		return identity.ErrConflict
	}
	return mapSalesConstraint(err)
}
