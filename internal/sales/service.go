// Package sales owns provider-neutral commercial documents.  Finance posting
// is injected through a transaction boundary so inventory and current account
// effects can commit atomically without making the sales domain depend on a
// storage implementation.
package sales

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/commerce"
	"github.com/alpyxn/varyaone/internal/finance"
	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/money"
	"github.com/alpyxn/varyaone/internal/platform/database"
	"github.com/alpyxn/varyaone/internal/platform/idempotency"
	"github.com/alpyxn/varyaone/internal/taxes"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type Service struct {
	pool         database.Querier
	finance      *finance.Service
	stockPoster  StockPoster
	rateResolver ExchangeRateResolver
	now          func() time.Time
}

// ExchangeRateResolver is implemented by the exchange bounded context. The
// sales package depends only on this narrow contract so provider details stay
// outside the commercial domain.
type ExchangeRateResolver interface {
	ResolveRate(context.Context, string, string, time.Time) (string, error)
}

// ensureDocumentScope applies the company member's optional branch and
// warehouse scope to a commercial document. Unscoped memberships retain
// company-wide access; once a scope exists, both references must be allowed.
func ensureDocumentScope(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, companyID, userID, branchID string, warehouseID *string) error {
	if strings.TrimSpace(userID) == "" {
		return identity.ErrForbidden
	}
	if uuid.Validate(strings.TrimSpace(userID)) != nil || uuid.Validate(strings.TrimSpace(branchID)) != nil {
		return identity.ErrForbidden
	}
	var branchAllowed bool
	if err := q.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM branches b
		WHERE b.company_id=$1 AND b.id=$2 AND b.is_active
		  AND (NOT EXISTS(SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=$1 AND bs.user_id=$3)
		       OR EXISTS(SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=$1 AND bs.user_id=$3 AND bs.branch_id=b.id))
	)`, companyID, branchID, userID).Scan(&branchAllowed); err != nil {
		return err
	}
	if !branchAllowed {
		return identity.ErrForbidden
	}
	if warehouseID == nil || strings.TrimSpace(*warehouseID) == "" {
		return nil
	}
	if uuid.Validate(strings.TrimSpace(*warehouseID)) != nil {
		return identity.ErrForbidden
	}
	var warehouseAllowed bool
	if err := q.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM warehouses w
		WHERE w.company_id=$1 AND w.id=$2 AND w.branch_id=$3 AND w.is_active
		  AND (NOT EXISTS(SELECT 1 FROM membership_warehouse_scopes ws WHERE ws.company_id=$1 AND ws.user_id=$4)
		       OR EXISTS(SELECT 1 FROM membership_warehouse_scopes ws WHERE ws.company_id=$1 AND ws.user_id=$4 AND ws.warehouse_id=w.id))
	)`, companyID, *warehouseID, branchID, userID).Scan(&warehouseAllowed); err != nil {
		return err
	}
	if !warehouseAllowed {
		return identity.ErrForbidden
	}
	return nil
}

func documentScopePredicate(parameter int) string {
	return fmt.Sprintf(` AND EXISTS (
		SELECT 1 FROM branches b
		WHERE b.company_id=d.company_id AND b.id=d.branch_id AND b.is_active
		  AND (NOT EXISTS(SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=d.company_id AND bs.user_id=$%d)
		       OR EXISTS(SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=d.company_id AND bs.user_id=$%d AND bs.branch_id=b.id))
		  AND (d.warehouse_id IS NULL OR EXISTS(
			SELECT 1 FROM warehouses w
			WHERE w.company_id=d.company_id AND w.id=d.warehouse_id AND w.branch_id=b.id AND w.is_active
			  AND (NOT EXISTS(SELECT 1 FROM membership_warehouse_scopes ws WHERE ws.company_id=d.company_id AND ws.user_id=$%d)
			       OR EXISTS(SELECT 1 FROM membership_warehouse_scopes ws WHERE ws.company_id=d.company_id AND ws.user_id=$%d AND ws.warehouse_id=w.id))
		  ))
	)`, parameter, parameter, parameter, parameter)
}

// StockPoster is intentionally small. Inventory can implement it without
// importing sales, while Post executes it on the same pgx transaction as the
// document and finance ledger writes.
type StockPoster interface {
	PostInvoiceTx(context.Context, pgx.Tx, identity.Session, StockPostingInput) error
}

// StockReverser is an optional extension implemented by the inventory
// bounded context. Keeping it separate from StockPoster preserves source
// compatibility for adapters that only support posting while allowing a
// posted invoice reversal to compensate its stock effects in the same
// transaction as the finance reversal.
type StockReverser interface {
	ReverseInvoiceTx(context.Context, pgx.Tx, identity.Session, StockReversalInput) error
}

// StockPosterFunc adapts an inventory transaction function without coupling
// this bounded context to the inventory package.
type StockPosterFunc func(context.Context, pgx.Tx, identity.Session, StockPostingInput) error

func (f StockPosterFunc) PostInvoiceTx(ctx context.Context, tx pgx.Tx, session identity.Session, input StockPostingInput) error {
	return f(ctx, tx, session, input)
}

type StockPostingInput struct {
	DocumentID   string
	DocumentType string
	WarehouseID  string
	DocumentDate time.Time
	Lines        []StockPostingLine
}

type StockPostingLine struct {
	LineID    string
	ProductID string
	VariantID string
	Quantity  string
	UnitCode  string
	// UnitCost/Currency are reserved for an explicitly supplied inventory
	// costing basis. The invoice line's sales/purchase price is not one.
	UnitCost string
	Currency string
}

func stockPostingLineForInvoice(line DocumentLine) StockPostingLine {
	productID := ""
	if line.ProductID != nil {
		productID = *line.ProductID
	}
	return StockPostingLine{
		LineID: line.ID, ProductID: productID, VariantID: nullableString(line.VariantID), Quantity: line.Quantity, UnitCode: line.UnitCode,
	}
}

type StockReversalInput struct {
	DocumentID   string
	DocumentType string
	WarehouseID  string
	ReversalKey  string
	Reason       string
}

// NewService keeps the one-argument constructor convenient for app wiring.
// Optional dependencies may be a *finance.Service and/or StockPoster.
func NewService(pool database.Querier, dependencies ...any) *Service {
	service := &Service{pool: pool, finance: finance.NewService(pool), now: time.Now}
	for _, dependency := range dependencies {
		switch value := dependency.(type) {
		case *finance.Service:
			if value != nil {
				service.finance = value
			}
		case StockPoster:
			service.stockPoster = value
		case ExchangeRateResolver:
			service.rateResolver = value
		}
	}
	return service
}

type Document struct {
	ID                 string         `json:"id"`
	CompanyID          string         `json:"company_id"`
	DocumentTypeCode   string         `json:"document_type_code"`
	DocumentNo         string         `json:"document_no"`
	BranchID           string         `json:"branch_id"`
	WarehouseID        *string        `json:"warehouse_id,omitempty"`
	PartyID            string         `json:"party_id"`
	DocumentDate       time.Time      `json:"document_date"`
	DueDate            *time.Time     `json:"due_date,omitempty"`
	CurrencyCode       string         `json:"currency_code"`
	ExchangeRate       string         `json:"exchange_rate"`
	Notes              string         `json:"notes"`
	Status             string         `json:"status"`
	Subtotal           string         `json:"subtotal"`
	DiscountTotal      string         `json:"discount_total"`
	TaxTotal           string         `json:"tax_total"`
	GrandTotal         string         `json:"grand_total"`
	PostIdempotencyKey *string        `json:"-"`
	PostedAt           *time.Time     `json:"posted_at,omitempty"`
	CancelledAt        *time.Time     `json:"cancelled_at,omitempty"`
	CancellationReason *string        `json:"cancellation_reason,omitempty"`
	CreatedBy          string         `json:"created_by"`
	UpdatedBy          string         `json:"updated_by"`
	CreatedAt          time.Time      `json:"created_at"`
	UpdatedAt          time.Time      `json:"updated_at"`
	Version            int64          `json:"version"`
	Lines              []DocumentLine `json:"lines"`
}

type DocumentLine struct {
	ID                             string          `json:"id"`
	LineNo                         int             `json:"line_no"`
	ProductID                      *string         `json:"product_id,omitempty"`
	VariantID                      *string         `json:"variant_id,omitempty"`
	ProductCodeSnapshot            string          `json:"product_code_snapshot"`
	VariantCodeSnapshot            string          `json:"variant_code_snapshot,omitempty"`
	VariantAttributesSnapshot      json.RawMessage `json:"variant_attributes_snapshot,omitempty"`
	ProductNameSnapshot            string          `json:"product_name_snapshot"`
	UnitCode                       string          `json:"unit_code"`
	Quantity                       string          `json:"quantity"`
	UnitPrice                      string          `json:"unit_price"`
	DiscountRate                   string          `json:"discount_rate"`
	TaxRate                        string          `json:"tax_rate"`
	TaxTreatment                   string          `json:"tax_treatment"`
	TaxDefinitionID                *string         `json:"tax_definition_id,omitempty"`
	TaxDefinitionCodeSnapshot      string          `json:"tax_definition_code_snapshot"`
	TaxDefinitionNameSnapshot      string          `json:"tax_definition_name_snapshot"`
	TaxCodeSnapshot                string          `json:"tax_code_snapshot"`
	TaxRateSnapshot                string          `json:"tax_rate_snapshot"`
	TaxCalculationTypeSnapshot     string          `json:"tax_calculation_type_snapshot"`
	TaxIncludedSnapshot            bool            `json:"tax_included_snapshot"`
	WithholdingCodeSnapshot        string          `json:"withholding_code_snapshot"`
	WithholdingRateSnapshot        string          `json:"withholding_rate_snapshot"`
	WithholdingNumeratorSnapshot   *int            `json:"withholding_numerator_snapshot,omitempty"`
	WithholdingDenominatorSnapshot *int            `json:"withholding_denominator_snapshot,omitempty"`
	ExemptionCodeSnapshot          string          `json:"exemption_code_snapshot"`
	ExemptionReasonSnapshot        string          `json:"exemption_reason_snapshot"`
	TaxNoteSnapshot                string          `json:"tax_note_snapshot"`
	TaxBaseSnapshot                string          `json:"tax_base_snapshot"`
	TaxAmountSnapshot              string          `json:"tax_amount_snapshot"`
	WithholdingAmountSnapshot      string          `json:"withholding_amount_snapshot"`
	PayableAmountSnapshot          string          `json:"payable_amount_snapshot"`
	TaxComponentsSnapshot          json.RawMessage `json:"tax_components_snapshot"`
	ProductTaxProfileVersion       int64           `json:"product_tax_profile_version"`
	GrossAmount                    string          `json:"gross_amount"`
	DiscountAmount                 string          `json:"discount_amount"`
	NetAmount                      string          `json:"net_amount"`
	TaxAmount                      string          `json:"tax_amount"`
	LineTotal                      string          `json:"line_total"`
}

type DocumentInput struct {
	ID               string              `json:"id,omitempty"`
	DocumentTypeCode string              `json:"document_type_code"`
	DocumentNo       string              `json:"document_no,omitempty"`
	BranchID         string              `json:"branch_id"`
	WarehouseID      string              `json:"warehouse_id,omitempty"`
	PartyID          string              `json:"party_id"`
	DocumentDate     time.Time           `json:"document_date"`
	DueDate          *time.Time          `json:"due_date,omitempty"`
	CurrencyCode     string              `json:"currency_code"`
	ExchangeRate     string              `json:"exchange_rate"`
	Notes            string              `json:"notes,omitempty"`
	Lines            []DocumentLineInput `json:"lines"`
}

type DocumentLineInput struct {
	ID                  string `json:"id,omitempty"`
	LineNo              int    `json:"line_no,omitempty"`
	ProductID           string `json:"product_id,omitempty"`
	VariantID           string `json:"variant_id,omitempty"`
	ProductCodeSnapshot string `json:"product_code_snapshot,omitempty"`
	ProductNameSnapshot string `json:"product_name_snapshot"`
	UnitCode            string `json:"unit_code"`
	Quantity            string `json:"quantity"`
	UnitPrice           string `json:"unit_price"`
	DiscountRate        string `json:"discount_rate"`
	TaxRate             string `json:"tax_rate"`
}

type DocumentListOptions struct {
	Status           string
	DocumentTypeCode string
	PartyID          string
	From             *time.Time
	To               *time.Time
	Cursor           string
	Limit            int
}

type DocumentListResult struct {
	Items      []Document `json:"items"`
	NextCursor string     `json:"next_cursor,omitempty"`
}

// ListDocumentsCursor is the richer cursor-based variant; the concise
// ListDocuments method below remains available for existing handlers.
func (s *Service) ListDocumentsCursor(ctx context.Context, session identity.Session, options DocumentListOptions) (DocumentListResult, error) {
	if identity.ValidateExternalActor(session) != nil || !session.HasPermission("document.read") {
		return DocumentListResult{}, identity.ErrForbidden
	}
	if options.Limit < 1 || options.Limit > 100 {
		options.Limit = 50
	}
	args := []any{session.CurrentCompanyID}
	query := `SELECT d.id,d.company_id,d.document_type_code,d.document_no,d.branch_id,d.warehouse_id,d.party_id,d.document_date,d.due_date::text,d.currency_code,d.exchange_rate::text,d.notes,d.status,d.subtotal::text,d.discount_total::text,d.tax_total::text,d.grand_total::text,d.post_idempotency_key,d.posted_at::text,d.cancelled_at::text,d.cancellation_reason,d.created_by,d.updated_by,d.created_at,d.updated_at,d.version FROM documents d WHERE d.company_id=$1`
	if session.User.ID != "" {
		args = append(args, session.User.ID)
		query += documentScopePredicate(len(args))
	}
	if value := strings.ToUpper(strings.TrimSpace(options.Status)); value != "" {
		if !contains([]string{"DRAFT", "POSTED", "CANCELLED"}, value) {
			return DocumentListResult{}, fmt.Errorf("%w: belge durumu geçersiz", identity.ErrValidation)
		}
		args = append(args, value)
		query += fmt.Sprintf(" AND d.status=$%d", len(args))
	}
	if value := strings.ToUpper(strings.TrimSpace(options.DocumentTypeCode)); value != "" {
		args = append(args, value)
		query += fmt.Sprintf(" AND d.document_type_code=$%d", len(args))
	}
	if options.PartyID != "" {
		args = append(args, options.PartyID)
		query += fmt.Sprintf(" AND d.party_id=$%d", len(args))
	}
	if options.From != nil {
		args = append(args, *options.From)
		query += fmt.Sprintf(" AND d.document_date >= $%d", len(args))
	}
	if options.To != nil {
		args = append(args, *options.To)
		query += fmt.Sprintf(" AND d.document_date <= $%d", len(args))
	}
	if options.Cursor != "" {
		parts := strings.SplitN(options.Cursor, "|", 2)
		if len(parts) != 2 {
			return DocumentListResult{}, fmt.Errorf("%w: belge listesi cursor bilgisi geçersiz", identity.ErrValidation)
		}
		cursorDate, parseErr := time.Parse("2006-01-02", parts[0])
		if parseErr != nil || uuid.Validate(parts[1]) != nil {
			return DocumentListResult{}, fmt.Errorf("%w: belge listesi cursor bilgisi geçersiz", identity.ErrValidation)
		}
		args = append(args, cursorDate, parts[1])
		query += fmt.Sprintf(" AND (d.document_date,d.id) < ($%d,$%d)", len(args)-1, len(args))
	}
	args = append(args, options.Limit+1)
	query += fmt.Sprintf(" ORDER BY d.document_date DESC,d.id DESC LIMIT $%d", len(args))
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return DocumentListResult{}, err
	}
	defer rows.Close()
	result := DocumentListResult{Items: []Document{}}
	for rows.Next() {
		item, scanErr := scanDocumentSummary(rows)
		if scanErr != nil {
			return DocumentListResult{}, scanErr
		}
		result.Items = append(result.Items, item)
	}
	if err = rows.Err(); err != nil {
		return DocumentListResult{}, err
	}
	if len(result.Items) > options.Limit {
		last := result.Items[options.Limit-1]
		result.Items = result.Items[:options.Limit]
		result.NextCursor = last.DocumentDate.Format("2006-01-02") + "|" + last.ID
	}
	return result, nil
}

func (s *Service) List(ctx context.Context, session identity.Session, options DocumentListOptions) (DocumentListResult, error) {
	return s.ListDocumentsCursor(ctx, session, options)
}

func (s *Service) CreateDocument(ctx context.Context, session identity.Session, input DocumentInput, meta identity.RequestMeta) (Document, error) {
	if identity.ValidateExternalActor(session) != nil || !session.HasPermission("document.create") {
		return Document{}, identity.ErrForbidden
	}
	normalized, docType, stockEffect, err := s.normalizeInput(ctx, session, input)
	if err != nil {
		return Document{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Document{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	payload, _ := json.Marshal(input)
	reservation, err := idempotency.ReserveTx(ctx, tx, session.CurrentCompanyID, meta.IdempotencyKey, "sales.document.create", payload, session.User.ID, meta.TraceID)
	if err != nil {
		return Document{}, err
	}
	if reservation.Completed {
		var replay struct {
			DocumentID string `json:"document_id"`
		}
		if err := json.Unmarshal(reservation.ResponseBody, &replay); err != nil || uuid.Validate(replay.DocumentID) != nil {
			return Document{}, idempotency.ErrCommandInProgress
		}
		if err := tx.Commit(ctx); err != nil {
			return Document{}, err
		}
		return s.GetDocument(ctx, session, replay.DocumentID)
	}
	if normalized.DocumentNo == "" {
		normalized.DocumentNo, err = nextDocumentNo(ctx, tx, session.CurrentCompanyID, normalized.DocumentTypeCode)
		if err != nil {
			return Document{}, err
		}
	}
	documentID := normalized.ID
	if uuid.Validate(documentID) != nil {
		documentID = uuid.NewString()
	}
	if err = validateDocumentReferencesTx(ctx, tx, session.CurrentCompanyID, session.User.ID, normalized); err != nil {
		return Document{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO documents(id,company_id,document_type_code,document_no,branch_id,warehouse_id,party_id,document_date,due_date,currency_code,exchange_rate,notes,created_by,updated_by) VALUES($1,$2,$3,$4,$5,NULLIF($6,'')::uuid,$7,$8,$9,$10,$11,$12,$13,$13)`, documentID, session.CurrentCompanyID, normalized.DocumentTypeCode, normalized.DocumentNo, normalized.BranchID, normalized.WarehouseID, normalized.PartyID, normalized.DocumentDate, normalized.DueDate, normalized.CurrencyCode, normalized.ExchangeRate, normalized.Notes, nullableUUID(session.User.ID)); err != nil {
		return Document{}, mapSalesConstraint(err)
	}
	for index, line := range normalized.Lines {
		if err = s.insertLineTx(ctx, tx, session.CurrentCompanyID, documentID, index+1, stockEffect, line); err != nil {
			return Document{}, err
		}
	}
	if err = updateTotalsTx(ctx, tx, session.CurrentCompanyID, documentID); err != nil {
		return Document{}, err
	}
	if err = insertStatusHistoryTx(ctx, tx, session, documentID, "", "DRAFT", "Taslak oluşturuldu"); err != nil {
		return Document{}, err
	}
	if err = writeAuditAndEventTx(ctx, tx, session, "DOCUMENT_CREATED", "document.created", "document", documentID, meta, map[string]any{"document_type": docType}); err != nil {
		return Document{}, err
	}
	if err = idempotency.CompleteTx(ctx, tx, session.CurrentCompanyID, meta.IdempotencyKey, http.StatusCreated, map[string]string{"document_id": documentID}); err != nil {
		return Document{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Document{}, err
	}
	return s.GetDocument(ctx, session, documentID)
}

// Create is a concise alias used by handlers.
func (s *Service) Create(ctx context.Context, session identity.Session, input DocumentInput, meta identity.RequestMeta) (Document, error) {
	return s.CreateDocument(ctx, session, input, meta)
}

func (s *Service) GetDocument(ctx context.Context, session identity.Session, id string) (Document, error) {
	if identity.ValidateExternalActor(session) != nil || !session.HasPermission("document.read") {
		return Document{}, identity.ErrForbidden
	}
	if err := ensureDocumentAccess(ctx, s.pool, session.CurrentCompanyID, session.User.ID, id); err != nil {
		return Document{}, err
	}
	item, err := loadDocument(ctx, s.pool, session.CurrentCompanyID, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Document{}, identity.ErrForbidden
	}
	return item, err
}

// ListDocuments provides the server-side list used by invoice screens. The
// query is company-scoped and uses a bounded page; callers can add cursoring
// at the transport layer without exposing an unbounded document scan.
func (s *Service) ListDocuments(ctx context.Context, session identity.Session, status, documentType, partyID string, limit int) ([]Document, error) {
	if !session.HasPermission("document.read") {
		return nil, identity.ErrForbidden
	}
	if limit < 1 || limit > 200 {
		limit = 50
	}
	args := []any{session.CurrentCompanyID}
	query := `SELECT d.id FROM documents d WHERE d.company_id=$1`
	if session.User.ID != "" {
		args = append(args, session.User.ID)
		query += documentScopePredicate(len(args))
	}
	if value := strings.ToUpper(strings.TrimSpace(status)); value != "" {
		if !contains([]string{"DRAFT", "POSTED", "CANCELLED"}, value) {
			return nil, fmt.Errorf("%w: belge durumu geçersiz", identity.ErrValidation)
		}
		args = append(args, value)
		query += fmt.Sprintf(" AND d.status=$%d", len(args))
	}
	if value := strings.ToUpper(strings.TrimSpace(documentType)); value != "" {
		args = append(args, value)
		query += fmt.Sprintf(" AND d.document_type_code=$%d", len(args))
	}
	if value := strings.TrimSpace(partyID); value != "" {
		args = append(args, value)
		query += fmt.Sprintf(" AND d.party_id=$%d", len(args))
	}
	args = append(args, limit)
	query += fmt.Sprintf(" ORDER BY d.document_date DESC,d.id DESC LIMIT $%d", len(args))
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]string, 0, limit)
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	items := make([]Document, 0, len(ids))
	for _, id := range ids {
		item, loadErr := loadDocument(ctx, s.pool, session.CurrentCompanyID, id)
		if loadErr != nil {
			return nil, loadErr
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *Service) UpdateDraft(ctx context.Context, session identity.Session, id string, expectedVersion int64, input DocumentInput, meta identity.RequestMeta) (Document, error) {
	if identity.ValidateExternalActor(session) != nil || !session.HasPermission("document.edit") {
		return Document{}, identity.ErrForbidden
	}
	if expectedVersion < 1 {
		return Document{}, fmt.Errorf("%w: geçerli taslak sürümü gereklidir", identity.ErrValidation)
	}
	normalized, _, stockEffect, err := s.normalizeInput(ctx, session, input)
	if err != nil {
		return Document{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Document{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	payload, _ := json.Marshal(map[string]any{"document_id": id, "expected_version": expectedVersion, "input": input})
	reservation, err := idempotency.ReserveTx(ctx, tx, session.CurrentCompanyID, meta.IdempotencyKey, "sales.document.update", payload, session.User.ID, meta.TraceID)
	if err != nil {
		return Document{}, err
	}
	if reservation.Completed {
		var response struct {
			DocumentID string `json:"document_id"`
		}
		if json.Unmarshal(reservation.ResponseBody, &response) != nil || uuid.Validate(response.DocumentID) != nil {
			return Document{}, idempotency.ErrCommandInProgress
		}
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return s.GetDocument(ctx, session, response.DocumentID)
	}
	var status string
	var docType string
	if err = tx.QueryRow(ctx, `SELECT status,document_type_code FROM documents WHERE company_id=$1 AND id=$2 AND version=$3 FOR UPDATE`, session.CurrentCompanyID, id, expectedVersion).Scan(&status, &docType); errors.Is(err, pgx.ErrNoRows) {
		return Document{}, identity.ErrConflict
	} else if err != nil {
		return Document{}, err
	}
	if status != "DRAFT" || (normalized.DocumentTypeCode != "" && normalized.DocumentTypeCode != docType) {
		return Document{}, fmt.Errorf("%w: yalnız taslak belge düzenlenebilir", identity.ErrValidation)
	}
	if normalized.DocumentTypeCode == "" {
		normalized.DocumentTypeCode = docType
	}
	if err = validateDocumentReferencesTx(ctx, tx, session.CurrentCompanyID, session.User.ID, normalized); err != nil {
		return Document{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE documents SET branch_id=$1,warehouse_id=NULLIF($2,'')::uuid,party_id=$3,document_date=$4,due_date=$5,currency_code=$6,exchange_rate=$7,notes=$8,updated_by=$9,updated_at=now(),version=version+1 WHERE company_id=$10 AND id=$11 AND version=$12`, normalized.BranchID, normalized.WarehouseID, normalized.PartyID, normalized.DocumentDate, normalized.DueDate, normalized.CurrencyCode, normalized.ExchangeRate, normalized.Notes, nullableUUID(session.User.ID), session.CurrentCompanyID, id, expectedVersion); err != nil {
		return Document{}, mapSalesConstraint(err)
	}
	if input.Lines != nil {
		if _, err = tx.Exec(ctx, `DELETE FROM document_lines WHERE company_id=$1 AND document_id=$2`, session.CurrentCompanyID, id); err != nil {
			return Document{}, err
		}
		for index, line := range normalized.Lines {
			if err = s.insertLineTx(ctx, tx, session.CurrentCompanyID, id, index+1, stockEffect, line); err != nil {
				return Document{}, err
			}
		}
	}
	if err = updateTotalsTx(ctx, tx, session.CurrentCompanyID, id); err != nil {
		return Document{}, err
	}
	if err = writeAuditAndEventTx(ctx, tx, session, "DOCUMENT_UPDATED", "document.updated", "document", id, meta, nil); err != nil {
		return Document{}, err
	}
	if err = idempotency.CompleteTx(ctx, tx, session.CurrentCompanyID, meta.IdempotencyKey, http.StatusOK, map[string]string{"document_id": id}); err != nil {
		return Document{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Document{}, err
	}
	return s.GetDocument(ctx, session, id)
}

func (s *Service) PostInvoice(ctx context.Context, session identity.Session, id, idempotencyKey string, meta identity.RequestMeta) (Document, error) {
	if identity.ValidateExternalActor(session) != nil || (!session.HasPermission("document.post") && !session.HasPermission("document.invoice.post")) {
		return Document{}, identity.ErrForbidden
	}
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" {
		return Document{}, fmt.Errorf("%w: fatura post işlemi için Idempotency-Key gereklidir", identity.ErrValidation)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Document{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	item, docType, stockEffect, financeEffect, err := loadDocumentForUpdate(ctx, tx, session.CurrentCompanyID, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Document{}, identity.ErrForbidden
		}
		return Document{}, err
	}
	// Scope must be checked before the idempotent replay branch as well. A
	// caller who knows a document id/key may retry a command, but that retry
	// must not become a side channel around branch or warehouse membership.
	if err = ensureDocumentScope(ctx, tx, session.CurrentCompanyID, session.User.ID, item.BranchID, item.WarehouseID); err != nil {
		return Document{}, err
	}
	if item.Status == "POSTED" {
		if item.PostIdempotencyKey != nil && *item.PostIdempotencyKey == idempotencyKey {
			return item, nil
		}
		return Document{}, finance.DomainErrorFor(finance.ErrInvoiceAlreadyPosted, "fatura daha önce post edilmiş")
	}
	if item.Status != "DRAFT" || docType != "INVOICE" || !strings.HasSuffix(item.DocumentTypeCode, "INVOICE") || financeEffect == "NONE" {
		return Document{}, fmt.Errorf("%w: yalnız satış veya alış taslak faturası post edilebilir", identity.ErrValidation)
	}
	if len(item.Lines) == 0 || item.GrandTotal == "0" {
		return Document{}, fmt.Errorf("%w: fatura en az bir satır ve pozitif toplam içermelidir", identity.ErrValidation)
	}
	if err = validateStockLinesTx(ctx, tx, session.CurrentCompanyID, stockEffect, item.Lines); err != nil {
		return Document{}, err
	}
	posting, err := s.finance.PostInvoiceTx(ctx, tx, session, finance.InvoicePostingInput{DocumentID: id, DocumentType: item.DocumentTypeCode, DocumentNo: item.DocumentNo, PartyID: item.PartyID, Currency: item.CurrencyCode, Amount: item.GrandTotal, ExchangeRate: item.ExchangeRate, DocumentDate: item.DocumentDate, DueDate: item.DueDate, Description: invoicePostingDescription(item.DocumentTypeCode, item.DocumentNo, item.Notes), IdempotencyKey: idempotencyKey})
	if err != nil {
		return Document{}, err
	}
	if stockEffect != "NONE" && s.stockPoster != nil {
		stockInput := StockPostingInput{DocumentID: id, DocumentType: item.DocumentTypeCode, DocumentDate: item.DocumentDate}
		if item.WarehouseID != nil {
			stockInput.WarehouseID = *item.WarehouseID
		}
		for _, line := range item.Lines {
			if line.ProductID == nil {
				continue
			}
			// Invoice prices are commercial prices, not an inventory costing
			// basis. Leave the cost empty here; inventory may populate it only
			// from an explicit UnitCost or an existing FIFO transfer basis.
			stockLine := stockPostingLineForInvoice(line)
			stockInput.Lines = append(stockInput.Lines, stockLine)
		}
		if err = s.stockPoster.PostInvoiceTx(ctx, tx, session, stockInput); err != nil {
			return Document{}, err
		}
	} else if stockEffect != "NONE" {
		// A stock-affecting invoice must never become financially posted while
		// its inventory side is unavailable.  Returning before the transaction
		// commits keeps the finance/open-item write atomic with this guard.
		return Document{}, fmt.Errorf("%w: fatura stok posting sağlayıcısı hazır değil", identity.ErrValidation)
	}
	now := s.now()
	if _, err = tx.Exec(ctx, `UPDATE documents SET status='POSTED',posted_at=$1,posted_by=$2,post_idempotency_key=$3,updated_by=$2,updated_at=$1,version=version+1 WHERE company_id=$4 AND id=$5 AND status='DRAFT'`, now, nullableUUID(session.User.ID), idempotencyKey, session.CurrentCompanyID, id); err != nil {
		return Document{}, mapSalesConstraint(err)
	}
	if err = insertStatusHistoryTx(ctx, tx, session, id, "DRAFT", "POSTED", "Fatura post edildi"); err != nil {
		return Document{}, err
	}
	if err = writeAuditAndEventTx(ctx, tx, session, "DOCUMENT_POSTED", "document.posted", "document", id, meta, map[string]any{"posting_id": posting.ID, "finance_effect": financeEffect}); err != nil {
		return Document{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Document{}, err
	}
	return s.GetDocument(ctx, session, id)
}

func (s *Service) Post(ctx context.Context, session identity.Session, id, idempotencyKey string, meta identity.RequestMeta) (Document, error) {
	return s.PostInvoice(ctx, session, id, idempotencyKey, meta)
}

func (s *Service) ReverseInvoice(ctx context.Context, session identity.Session, id, idempotencyKey, reason string, meta identity.RequestMeta) (Document, error) {
	if identity.ValidateExternalActor(session) != nil || (!session.HasPermission("document.invoice.reverse") && !session.HasPermission("document.cancel")) {
		return Document{}, identity.ErrForbidden
	}
	if strings.TrimSpace(idempotencyKey) == "" || strings.TrimSpace(reason) == "" {
		return Document{}, fmt.Errorf("%w: ters kayıt anahtarı ve gerekçesi gereklidir", identity.ErrValidation)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Document{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	item, _, stockEffect, _, loadErr := loadDocumentForUpdate(ctx, tx, session.CurrentCompanyID, id)
	if errors.Is(loadErr, pgx.ErrNoRows) {
		return Document{}, identity.ErrForbidden
	} else if loadErr != nil {
		return Document{}, loadErr
	}
	if err = ensureDocumentScope(ctx, tx, session.CurrentCompanyID, session.User.ID, item.BranchID, item.WarehouseID); err != nil {
		return Document{}, err
	}
	if item.Status == "CANCELLED" {
		var existingKey, existingReason string
		err = tx.QueryRow(ctx, `SELECT COALESCE(idempotency_key,''),reason FROM document_reversals WHERE company_id=$1 AND original_document_id=$2`, session.CurrentCompanyID, id).Scan(&existingKey, &existingReason)
		if err == nil && existingKey == idempotencyKey && existingReason == reason {
			return item, nil
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return Document{}, finance.DomainErrorFor(finance.ErrAlreadyReversed, "fatura zaten ters kaydedilmiş")
		}
		if err != nil {
			return Document{}, err
		}
		return Document{}, finance.DomainErrorFor(finance.ErrAlreadyReversed, "fatura zaten ters kaydedilmiş")
	}
	if item.Status != "POSTED" {
		return Document{}, fmt.Errorf("%w: yalnız post edilmiş fatura terslenebilir", identity.ErrValidation)
	}
	reversalID, err := s.finance.ReverseInvoiceTx(ctx, tx, session, id, idempotencyKey, reason)
	if err != nil {
		return Document{}, err
	}
	if stockEffect != "NONE" {
		if reverser, ok := s.stockPoster.(StockReverser); ok {
			warehouseID := ""
			if item.WarehouseID != nil {
				warehouseID = *item.WarehouseID
			}
			if err = reverser.ReverseInvoiceTx(ctx, tx, session, StockReversalInput{
				DocumentID: id, DocumentType: item.DocumentTypeCode, WarehouseID: warehouseID,
				ReversalKey: idempotencyKey, Reason: reason,
			}); err != nil {
				return Document{}, err
			}
		} else {
			// Cancelling a stock-affecting invoice without its compensating
			// movement would leave the stock ledger and cari ledger divergent.
			return Document{}, fmt.Errorf("%w: fatura stok ters kayıt sağlayıcısı hazır değil", identity.ErrValidation)
		}
	}
	now := s.now()
	if _, err = tx.Exec(ctx, `UPDATE documents SET status='CANCELLED',cancelled_at=$1,cancellation_reason=$2,updated_by=$3,updated_at=$1,version=version+1 WHERE company_id=$4 AND id=$5 AND status='POSTED'`, now, reason, nullableUUID(session.User.ID), session.CurrentCompanyID, id); err != nil {
		return Document{}, mapSalesConstraint(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO document_reversals(id,company_id,original_document_id,finance_reversal_id,reason,idempotency_key,actor_user_id) VALUES($1,$2,$3,$4,$5,$6,$7)`, uuid.NewString(), session.CurrentCompanyID, id, reversalID, reason, idempotencyKey, nullableUUID(session.User.ID)); err != nil {
		return Document{}, mapSalesConstraint(err)
	}
	if err = insertStatusHistoryTx(ctx, tx, session, id, "POSTED", "CANCELLED", reason); err != nil {
		return Document{}, err
	}
	if err = writeAuditAndEventTx(ctx, tx, session, "DOCUMENT_REVERSED", "document.reversed", "document", id, meta, map[string]any{"finance_reversal_id": reversalID, "reason": reason}); err != nil {
		return Document{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Document{}, err
	}
	return s.GetDocument(ctx, session, id)
}

func (s *Service) Cancel(ctx context.Context, session identity.Session, id, reason string, meta identity.RequestMeta) (Document, error) {
	return s.CancelWithKey(ctx, session, id, "", reason, meta)
}

// CancelWithKey keeps cancellation on the same explicit idempotent reversal
// path as invoice reversal. Callers must provide a stable key; a derived key
// would make two different cancel payloads indistinguishable.
func (s *Service) CancelWithKey(ctx context.Context, session identity.Session, id, idempotencyKey, reason string, meta identity.RequestMeta) (Document, error) {
	if !session.HasPermission("document.cancel") {
		return Document{}, identity.ErrForbidden
	}
	return s.ReverseInvoice(ctx, session, id, idempotencyKey, reason, meta)
}

func (s *Service) normalizeInput(ctx context.Context, session identity.Session, input DocumentInput) (DocumentInput, string, string, error) {
	input.DocumentTypeCode = strings.ToUpper(strings.TrimSpace(input.DocumentTypeCode))
	input.CurrencyCode = strings.ToUpper(strings.TrimSpace(input.CurrencyCode))
	if input.DocumentDate.IsZero() {
		input.DocumentDate = s.now()
	}
	if input.ExchangeRate == "" {
		input.ExchangeRate = "1"
	}
	if input.BranchID == "" || input.PartyID == "" || input.DocumentTypeCode == "" || len(input.CurrencyCode) != 3 {
		return DocumentInput{}, "", "", fmt.Errorf("%w: fatura firma, şube, cari, tip ve para birimi gerektirir", identity.ErrValidation)
	}
	if _, err := money.ParseDecimal(input.ExchangeRate, 10); err != nil {
		return DocumentInput{}, "", "", fmt.Errorf("%w: kur geçersiz", identity.ErrValidation)
	}
	var kind, direction, stockEffect string
	if err := s.pool.QueryRow(ctx, `SELECT kind,direction,stock_effect FROM document_types WHERE (company_id=$1 OR company_id IS NULL) AND code=$2 ORDER BY company_id NULLS LAST LIMIT 1`, session.CurrentCompanyID, input.DocumentTypeCode).Scan(&kind, &direction, &stockEffect); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return DocumentInput{}, "", "", fmt.Errorf("%w: belge tipi bulunamadı", identity.ErrValidation)
		}
		return DocumentInput{}, "", "", err
	}
	if kind != "INVOICE" || !contains([]string{"SALES", "PURCHASE"}, direction) {
		return DocumentInput{}, "", "", fmt.Errorf("%w: yalnız satış ve alış faturası kullanılabilir", identity.ErrValidation)
	}
	for index := range input.Lines {
		if input.Lines[index].LineNo < 1 {
			input.Lines[index].LineNo = index + 1
		}
		if input.Lines[index].DiscountRate == "" {
			input.Lines[index].DiscountRate = "0"
		}
		if input.Lines[index].TaxRate == "" {
			input.Lines[index].TaxRate = "0"
		}
		if err := validateLineInput(input.Lines[index]); err != nil {
			return DocumentInput{}, "", "", err
		}
	}
	return input, input.DocumentTypeCode, stockEffect, nil
}

func validateLineInput(line DocumentLineInput) error {
	if strings.TrimSpace(line.UnitCode) == "" || (strings.TrimSpace(line.ProductNameSnapshot) == "" && strings.TrimSpace(line.ProductID) == "") {
		return fmt.Errorf("%w: satır ürün adı ve birim gerektirir", identity.ErrValidation)
	}
	for index, value := range []struct {
		raw      string
		scale    int
		positive bool
	}{{line.Quantity, 8, true}, {line.UnitPrice, 8, false}, {line.DiscountRate, 8, false}, {line.TaxRate, 8, false}} {
		parsed, err := money.ParseDecimal(value.raw, value.scale)
		rateTooHigh := false
		if index >= 2 {
			rate, _ := new(big.Rat).SetString(parsed.String())
			rateTooHigh = rate == nil || rate.Cmp(big.NewRat(100, 1)) > 0
		}
		if err != nil || (value.positive && parsed.Sign() <= 0) || (!value.positive && parsed.Sign() < 0) || rateTooHigh {
			return fmt.Errorf("%w: fatura satırındaki sayı geçersiz", identity.ErrValidation)
		}
	}
	return nil
}

func validateStockLineInput(stockEffect string, line DocumentLineInput) error {
	if stockEffect == "NONE" {
		if strings.TrimSpace(line.ProductID) == "" && strings.TrimSpace(line.VariantID) != "" {
			return fmt.Errorf("%w: varyant için ürün gereklidir", identity.ErrValidation)
		}
		return nil
	}
	if strings.TrimSpace(line.ProductID) == "" {
		return fmt.Errorf("%w: stok etkili belge satırı ürün gerektirir", identity.ErrValidation)
	}
	if strings.TrimSpace(line.VariantID) == "" {
		return fmt.Errorf("%w: stok etkili belge satırında varyant zorunludur", identity.ErrValidation)
	}
	return nil
}

type documentLineTaxSnapshot struct {
	TaxTreatment                   string
	TaxDefinitionID                *string
	TaxDefinitionCodeSnapshot      string
	TaxDefinitionNameSnapshot      string
	TaxCodeSnapshot                string
	TaxRateSnapshot                string
	TaxCalculationTypeSnapshot     string
	TaxIncludedSnapshot            bool
	WithholdingCodeSnapshot        string
	WithholdingRateSnapshot        string
	WithholdingNumeratorSnapshot   *int
	WithholdingDenominatorSnapshot *int
	ExemptionCodeSnapshot          string
	ExemptionReasonSnapshot        string
	TaxNoteSnapshot                string
	TaxAmountSnapshot              string
	TaxComponentsSnapshot          []byte
	ProductTaxProfileVersion       int64
	// AdditionalComponents are the product's non-VAT taxes (ÖTV, ÖİV, a
	// company-defined tax) resolved against the document date, so they are
	// priced with the line instead of being dropped.
	AdditionalComponents []taxes.TaxComponent
}

func loadProductTaxSnapshot(ctx context.Context, tx pgx.Tx, companyID, documentID, productID, variantID, stockEffect string, code, name, variantCode *string, variantAttributes *[]byte, snapshot *documentLineTaxSnapshot) error {
	var direction, documentDate string
	if err := tx.QueryRow(ctx, `SELECT dt.direction,d.document_date::text FROM documents d JOIN document_types dt ON dt.code=d.document_type_code AND (dt.company_id IS NULL OR dt.company_id=d.company_id) WHERE d.company_id=$1 AND d.id=$2`, companyID, documentID).Scan(&direction, &documentDate); err != nil {
		return err
	}
	if stockEffect != "NONE" && strings.TrimSpace(variantID) == "" {
		return fmt.Errorf("%w: stok etkili belge satırında varyant zorunludur", identity.ErrValidation)
	}
	var taxDefinitionID, taxRateID, withholdingRuleID, exemptionID *string
	var taxIncluded bool
	var version int64
	var variantsEnabled bool
	err := tx.QueryRow(ctx, `SELECT p.code,p.name,COALESCE(p.variants_enabled,false),COALESCE(ptp.treatment,'STANDARD'),ptp.tax_definition_id,COALESCE(td.code,''),COALESCE(td.name,''),ptp.tax_rate_id,COALESCE(ptp.tax_code,''),COALESCE(ptp.rate,'0')::text,COALESCE(ptp.tax_included,false),ptp.withholding_rule_id,COALESCE(NULLIF(ptp.withholding_code,''),wr.code,''),COALESCE(ptp.withholding_rate,'0')::text,COALESCE(ptp.withholding_numerator,wr.ratio_numerator),COALESCE(ptp.withholding_denominator,wr.ratio_denominator),ptp.exemption_id,COALESCE(ptp.exemption_code,''),COALESCE(ptp.tax_note,''),COALESCE(ptp.version,1)
FROM products p
LEFT JOIN product_tax_profiles ptp ON ptp.company_id=p.company_id AND ptp.product_id=p.id AND ptp.direction=$3
LEFT JOIN tax_definitions td ON td.company_id=ptp.company_id AND td.id=ptp.tax_definition_id
LEFT JOIN tax_withholding_rules wr ON wr.company_id=ptp.company_id AND wr.id=ptp.withholding_rule_id
		WHERE p.company_id=$1 AND p.id=$2`, companyID, productID, direction).Scan(code, name, &variantsEnabled, &snapshot.TaxTreatment, &taxDefinitionID, &snapshot.TaxDefinitionCodeSnapshot, &snapshot.TaxDefinitionNameSnapshot, &taxRateID, &snapshot.TaxCodeSnapshot, &snapshot.TaxRateSnapshot, &taxIncluded, &withholdingRuleID, &snapshot.WithholdingCodeSnapshot, &snapshot.WithholdingRateSnapshot, &snapshot.WithholdingNumeratorSnapshot, &snapshot.WithholdingDenominatorSnapshot, &exemptionID, &snapshot.ExemptionCodeSnapshot, &snapshot.TaxNoteSnapshot, &version)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return identity.ErrForbidden
		}
		return err
	}
	if stockEffect == "NONE" && variantsEnabled && strings.TrimSpace(variantID) == "" {
		return fmt.Errorf("%w: varyantlı ürün satırında varyant zorunludur", identity.ErrValidation)
	}
	*variantAttributes = []byte(`{}`)
	if strings.TrimSpace(variantID) != "" {
		var active bool
		var attributes []byte
		if err := tx.QueryRow(ctx, `SELECT pv.variant_code,COALESCE((SELECT jsonb_object_agg(d.code,o.name) FROM product_variant_values vv JOIN variant_definitions d ON d.company_id=vv.company_id AND d.id=vv.definition_id JOIN variant_definition_options o ON o.company_id=vv.company_id AND o.definition_id=vv.definition_id AND o.id=vv.option_id WHERE vv.company_id=pv.company_id AND vv.variant_id=pv.id),'{}'::jsonb),pv.is_active FROM product_variants pv WHERE pv.company_id=$1 AND pv.id=$2 AND pv.product_id=$3`, companyID, variantID, productID).Scan(variantCode, &attributes, &active); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("%w: varyant ürünle eşleşmiyor", identity.ErrValidation)
			}
			return err
		}
		if !active {
			return fmt.Errorf("%w: pasif varyant kullanılamaz", identity.ErrValidation)
		}
		if len(attributes) > 0 {
			*variantAttributes = attributes
		}
	}
	snapshot.TaxDefinitionID = taxDefinitionID
	snapshot.TaxIncludedSnapshot = taxIncluded
	snapshot.ExemptionReasonSnapshot = snapshot.TaxNoteSnapshot
	snapshot.TaxCalculationTypeSnapshot = "PERCENTAGE"
	snapshot.TaxComponentsSnapshot = []byte("[]")
	// Components are optional for a profile. They are captured as JSON so a
	// later catalog change cannot alter a posted line's tax composition.
	var components []byte
	if err := tx.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(jsonb_build_object('tax_definition_id',c.tax_definition_id,'tax_definition_code',td.code,'tax_definition_name',td.name,'tax_rate_id',c.tax_rate_id,'calculation_type',c.calculation_type,'included_in_tax_base',c.included_in_tax_base,'metadata',c.metadata) ORDER BY c.sequence),'[]'::jsonb) FROM product_tax_profile_components c JOIN tax_definitions td ON td.company_id=c.company_id AND td.id=c.tax_definition_id WHERE c.company_id=$1 AND c.product_id=$2 AND c.direction=$3`, companyID, productID, direction).Scan(&components); err == nil && len(components) > 0 {
		snapshot.TaxComponentsSnapshot = components
	}
	snapshot.ProductTaxProfileVersion = version
	additional, err := commerce.AdditionalComponents(ctx, tx, companyID, productID, direction, documentDate)
	if err != nil {
		return err
	}
	snapshot.AdditionalComponents = additional
	return nil
}

func (s *Service) insertLineTx(ctx context.Context, tx pgx.Tx, companyID, documentID string, lineNo int, stockEffect string, line DocumentLineInput) error {
	if err := validateLineInput(line); err != nil {
		return err
	}
	if err := validateStockLineInput(stockEffect, line); err != nil {
		return err
	}
	lineID := line.ID
	if uuid.Validate(lineID) != nil {
		lineID = uuid.NewString()
	}
	productID := nullableUUID(line.ProductID)
	variantID := nullableUUID(line.VariantID)
	code, name := line.ProductCodeSnapshot, line.ProductNameSnapshot
	variantCode := ""
	variantAttributes := []byte(`{}`)
	taxSnapshot := documentLineTaxSnapshot{TaxTreatment: "STANDARD", TaxRateSnapshot: line.TaxRate, TaxCalculationTypeSnapshot: "PERCENTAGE", TaxComponentsSnapshot: []byte("[]"), ProductTaxProfileVersion: 1}
	if line.ProductID != "" {
		if err := loadProductTaxSnapshot(ctx, tx, companyID, documentID, line.ProductID, line.VariantID, stockEffect, &code, &name, &variantCode, &variantAttributes, &taxSnapshot); err != nil {
			return err
		}
	}
	// A line may omit a tax rate; in that case the directional product profile
	// is the default. The value is copied into both the calculation column and
	// the immutable snapshot so later product edits cannot affect the document.
	calculationTaxRate := line.TaxRate
	if line.ProductID != "" && calculationTaxRate == "0" && taxSnapshot.TaxRateSnapshot != "" {
		calculationTaxRate = taxSnapshot.TaxRateSnapshot
	}
	if calculationTaxRate == "" {
		calculationTaxRate = "0"
	}
	// A permitted line override is part of the line snapshot as well.
	taxSnapshot.TaxRateSnapshot = calculationTaxRate
	components := []taxes.TaxComponent{{Code: taxSnapshot.TaxCodeSnapshot, Name: "KDV", Primary: true, CalculationType: taxes.TaxPercentage, Rate: calculationTaxRate, Withholding: taxSnapshot.WithholdingCodeSnapshot != "", WithholdingNumerator: taxSnapshot.WithholdingNumeratorSnapshot, WithholdingDenominator: taxSnapshot.WithholdingDenominatorSnapshot, Exempt: taxSnapshot.TaxTreatment == "EXEMPT" || taxSnapshot.TaxTreatment == "NOT_APPLICABLE"}}
	// The product's additional taxes are charged alongside VAT; the engine
	// puts each one on the base its profile says it belongs to.
	components = append(components, taxSnapshot.AdditionalComponents...)
	taxMode := taxes.TaxModeExclusive
	if taxSnapshot.TaxIncludedSnapshot {
		taxMode = taxes.TaxModeInclusive
	}
	calculation, calcErr := taxes.Calculate(taxes.TaxCalculationInput{Lines: []taxes.TaxCalculationLine{{UnitPrice: line.UnitPrice, Quantity: line.Quantity, Discount: taxes.Discount{Kind: taxes.DiscountPercent, Amount: line.DiscountRate}, Components: components}}, TaxMode: taxMode, RoundScale: 8, RoundPolicy: taxes.RoundHalfUp})
	if calcErr != nil {
		return fmt.Errorf("%w: vergi hesaplaması geçersiz", identity.ErrValidation)
	}
	lineResult := calculation.Lines[0]
	calculatedComponents, _ := json.Marshal(lineResult.Components)
	_, err := tx.Exec(ctx, `INSERT INTO document_lines(id,company_id,document_id,line_no,product_id,variant_id,product_code_snapshot,variant_code_snapshot,variant_attributes_snapshot,product_name_snapshot,unit_code,quantity,unit_price,discount_rate,tax_rate,tax_treatment,tax_definition_id,tax_definition_code_snapshot,tax_definition_name_snapshot,tax_code_snapshot,tax_rate_snapshot,tax_calculation_type_snapshot,tax_included_snapshot,withholding_code_snapshot,withholding_rate_snapshot,exemption_code_snapshot,exemption_reason_snapshot,tax_note_snapshot,tax_base_snapshot,tax_amount_snapshot,withholding_amount_snapshot,payable_amount_snapshot,tax_components_snapshot,product_tax_profile_version,gross_amount,discount_amount,net_amount,tax_amount,line_total)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$33,$34,$35,$36,$37,$38,$39)`, lineID, companyID, documentID, lineNo, productID, variantID, code, variantCode, variantAttributes, name, strings.ToUpper(line.UnitCode), line.Quantity, line.UnitPrice, line.DiscountRate, calculationTaxRate, taxSnapshot.TaxTreatment, taxSnapshot.TaxDefinitionID, taxSnapshot.TaxDefinitionCodeSnapshot, taxSnapshot.TaxDefinitionNameSnapshot, taxSnapshot.TaxCodeSnapshot, taxSnapshot.TaxRateSnapshot, taxSnapshot.TaxCalculationTypeSnapshot, taxSnapshot.TaxIncludedSnapshot, taxSnapshot.WithholdingCodeSnapshot, taxSnapshot.WithholdingRateSnapshot, taxSnapshot.ExemptionCodeSnapshot, taxSnapshot.ExemptionReasonSnapshot, taxSnapshot.TaxNoteSnapshot, lineResult.TaxableAmount, lineResult.TaxAmount, lineResult.WithholdingAmount, lineResult.PayableAmount, calculatedComponents, taxSnapshot.ProductTaxProfileVersion, lineResult.GrossAmount, lineResult.DiscountAmount, lineResult.TaxableAmount, lineResult.TaxAmount, lineResult.TotalAmount)
	return mapSalesConstraint(err)
}

func validateStockLinesTx(ctx context.Context, tx pgx.Tx, companyID, stockEffect string, lines []DocumentLine) error {
	if stockEffect == "NONE" {
		return nil
	}
	for _, line := range lines {
		if line.ProductID == nil || strings.TrimSpace(*line.ProductID) == "" {
			return fmt.Errorf("%w: stok etkili belge satırı ürün gerektirir", identity.ErrValidation)
		}
		if line.VariantID == nil || strings.TrimSpace(*line.VariantID) == "" {
			return fmt.Errorf("%w: stok etkili belge satırında varyant zorunludur", identity.ErrValidation)
		}
		var active bool
		err := tx.QueryRow(ctx, `SELECT v.is_active FROM product_variants v WHERE v.company_id=$1 AND v.id=$2 AND v.product_id=$3`, companyID, *line.VariantID, *line.ProductID).Scan(&active)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: varyant ürünle eşleşmiyor", identity.ErrValidation)
		}
		if err != nil {
			return err
		}
		if !active {
			return fmt.Errorf("%w: pasif varyant post edilemez", identity.ErrValidation)
		}
	}
	return nil
}

func updateTotalsTx(ctx context.Context, tx pgx.Tx, companyID, documentID string) error {
	_, err := tx.Exec(ctx, `UPDATE documents d SET subtotal=x.subtotal,discount_total=x.discount_total,tax_total=x.tax_total,grand_total=x.grand_total,updated_at=now() FROM (SELECT company_id,document_id,COALESCE(SUM(net_amount+discount_amount),0) AS subtotal,COALESCE(SUM(discount_amount),0) AS discount_total,COALESCE(SUM(tax_amount),0) AS tax_total,COALESCE(SUM(payable_amount_snapshot),0) AS grand_total FROM document_lines WHERE company_id=$1 AND document_id=$2 GROUP BY company_id,document_id) x WHERE d.company_id=x.company_id AND d.id=x.document_id`, companyID, documentID)
	return err
}

func nextDocumentNo(ctx context.Context, tx pgx.Tx, companyID, documentType string) (string, error) {
	var number int64
	if err := tx.QueryRow(ctx, `SELECT allocate_document_number($1,$2)`, companyID, documentType).Scan(&number); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-%06d", documentType, number), nil
}

func validateDocumentReferencesTx(ctx context.Context, tx pgx.Tx, companyID, actorID string, input DocumentInput) error {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM branches WHERE company_id=$1 AND id=$2 AND is_active)`, companyID, input.BranchID).Scan(&exists); err != nil || !exists {
		return identity.ErrForbidden
	}
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM parties WHERE company_id=$1 AND id=$2 AND is_active)`, companyID, input.PartyID).Scan(&exists); err != nil || !exists {
		return identity.ErrForbidden
	}
	if input.WarehouseID != "" {
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM warehouses WHERE company_id=$1 AND id=$2 AND branch_id=$3 AND is_active)`, companyID, input.WarehouseID, input.BranchID).Scan(&exists); err != nil || !exists {
			return identity.ErrForbidden
		}
	}
	warehouseID := (*string)(nil)
	if input.WarehouseID != "" {
		warehouseID = &input.WarehouseID
	}
	return ensureDocumentScope(ctx, tx, companyID, actorID, input.BranchID, warehouseID)
}

func ensureDocumentAccess(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, companyID, userID, documentID string) error {
	var branchID string
	var warehouseID *string
	err := q.QueryRow(ctx, `SELECT branch_id,warehouse_id FROM documents WHERE company_id=$1 AND id=$2`, companyID, documentID).Scan(&branchID, &warehouseID)
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.ErrForbidden
	}
	if err != nil {
		return err
	}
	return ensureDocumentScope(ctx, q, companyID, userID, branchID, warehouseID)
}

func loadDocument(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, companyID, id string) (Document, error) {
	item, _, _, _, err := loadDocumentQuery(ctx, q, companyID, id, false)
	return item, err
}

func scanDocumentSummary(row interface{ Scan(...any) error }) (Document, error) {
	var item Document
	var warehouseID, dueDate, postKey, postedAt, cancelledAt, cancellationReason *string
	if err := row.Scan(&item.ID, &item.CompanyID, &item.DocumentTypeCode, &item.DocumentNo, &item.BranchID, &warehouseID, &item.PartyID, &item.DocumentDate, &dueDate, &item.CurrencyCode, &item.ExchangeRate, &item.Notes, &item.Status, &item.Subtotal, &item.DiscountTotal, &item.TaxTotal, &item.GrandTotal, &postKey, &postedAt, &cancelledAt, &cancellationReason, &item.CreatedBy, &item.UpdatedBy, &item.CreatedAt, &item.UpdatedAt, &item.Version); err != nil {
		return Document{}, err
	}
	item.WarehouseID, item.PostIdempotencyKey, item.CancellationReason = warehouseID, postKey, cancellationReason
	if dueDate != nil {
		if value, err := parseDateValue(*dueDate); err == nil {
			item.DueDate = &value
		}
	}
	if postedAt != nil {
		if value, err := parseDateValue(*postedAt); err == nil {
			item.PostedAt = &value
		}
	}
	if cancelledAt != nil {
		if value, err := parseDateValue(*cancelledAt); err == nil {
			item.CancelledAt = &value
		}
	}
	item.Lines = []DocumentLine{}
	return item, nil
}

func loadDocumentForUpdate(ctx context.Context, tx pgx.Tx, companyID, id string) (Document, string, string, string, error) {
	return loadDocumentQuery(ctx, tx, companyID, id, true)
}

func loadDocumentQuery(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, companyID, id string, lock bool) (Document, string, string, string, error) {
	lockSQL := ""
	if lock {
		lockSQL = " FOR UPDATE"
	}
	var item Document
	var warehouseID, dueDate, postKey, postedAt, cancelledAt, cancellationReason *string
	var documentType, stockEffect, financeEffect string
	query := `SELECT d.id,d.company_id,d.document_type_code,d.document_no,d.branch_id,d.warehouse_id,d.party_id,d.document_date,d.due_date::text,d.currency_code,d.exchange_rate::text,d.notes,d.status,d.subtotal::text,d.discount_total::text,d.tax_total::text,d.grand_total::text,d.post_idempotency_key,d.posted_at::text,d.cancelled_at::text,d.cancellation_reason,d.created_by,d.updated_by,d.created_at,d.updated_at,d.version,t.kind,t.stock_effect,t.finance_effect FROM documents d JOIN document_types t ON t.code=d.document_type_code WHERE d.company_id=$1 AND d.id=$2` + lockSQL
	err := q.QueryRow(ctx, query, companyID, id).Scan(&item.ID, &item.CompanyID, &item.DocumentTypeCode, &item.DocumentNo, &item.BranchID, &warehouseID, &item.PartyID, &item.DocumentDate, &dueDate, &item.CurrencyCode, &item.ExchangeRate, &item.Notes, &item.Status, &item.Subtotal, &item.DiscountTotal, &item.TaxTotal, &item.GrandTotal, &postKey, &postedAt, &cancelledAt, &cancellationReason, &item.CreatedBy, &item.UpdatedBy, &item.CreatedAt, &item.UpdatedAt, &item.Version, &documentType, &stockEffect, &financeEffect)
	if err != nil {
		return Document{}, "", "", "", err
	}
	item.WarehouseID = warehouseID
	item.PostIdempotencyKey = postKey
	if dueDate != nil {
		if value, parseErr := parseDateValue(*dueDate); parseErr == nil {
			item.DueDate = &value
		}
	}
	if postedAt != nil {
		if value, parseErr := time.Parse(time.RFC3339, *postedAt); parseErr == nil {
			item.PostedAt = &value
		}
	}
	if cancelledAt != nil {
		if value, parseErr := parseDateValue(*cancelledAt); parseErr == nil {
			item.CancelledAt = &value
		}
	}
	item.CancellationReason = cancellationReason
	rows, err := q.(interface {
		Query(context.Context, string, ...any) (pgx.Rows, error)
	}).Query(ctx, `SELECT id,line_no,product_id,variant_id,product_code_snapshot,variant_code_snapshot,variant_attributes_snapshot,product_name_snapshot,unit_code,quantity::text,unit_price::text,discount_rate::text,tax_rate::text,tax_treatment,tax_definition_id,tax_definition_code_snapshot,tax_definition_name_snapshot,tax_code_snapshot,tax_rate_snapshot::text,tax_calculation_type_snapshot,tax_included_snapshot,withholding_code_snapshot,withholding_rate_snapshot::text,withholding_numerator_snapshot,withholding_denominator_snapshot,exemption_code_snapshot,exemption_reason_snapshot,tax_note_snapshot,tax_base_snapshot::text,tax_amount_snapshot::text,withholding_amount_snapshot::text,payable_amount_snapshot::text,tax_components_snapshot,product_tax_profile_version,gross_amount::text,discount_amount::text,net_amount::text,tax_amount::text,line_total::text FROM document_lines WHERE company_id=$1 AND document_id=$2 ORDER BY line_no`, companyID, id)
	if err != nil {
		return Document{}, "", "", "", err
	}
	defer rows.Close()
	item.Lines = []DocumentLine{}
	for rows.Next() {
		var line DocumentLine
		if err = rows.Scan(&line.ID, &line.LineNo, &line.ProductID, &line.VariantID, &line.ProductCodeSnapshot, &line.VariantCodeSnapshot, &line.VariantAttributesSnapshot, &line.ProductNameSnapshot, &line.UnitCode, &line.Quantity, &line.UnitPrice, &line.DiscountRate, &line.TaxRate, &line.TaxTreatment, &line.TaxDefinitionID, &line.TaxDefinitionCodeSnapshot, &line.TaxDefinitionNameSnapshot, &line.TaxCodeSnapshot, &line.TaxRateSnapshot, &line.TaxCalculationTypeSnapshot, &line.TaxIncludedSnapshot, &line.WithholdingCodeSnapshot, &line.WithholdingRateSnapshot, &line.WithholdingNumeratorSnapshot, &line.WithholdingDenominatorSnapshot, &line.ExemptionCodeSnapshot, &line.ExemptionReasonSnapshot, &line.TaxNoteSnapshot, &line.TaxBaseSnapshot, &line.TaxAmountSnapshot, &line.WithholdingAmountSnapshot, &line.PayableAmountSnapshot, &line.TaxComponentsSnapshot, &line.ProductTaxProfileVersion, &line.GrossAmount, &line.DiscountAmount, &line.NetAmount, &line.TaxAmount, &line.LineTotal); err != nil {
			return Document{}, "", "", "", err
		}
		item.Lines = append(item.Lines, line)
	}
	return item, documentType, stockEffect, financeEffect, rows.Err()
}

func parseDateValue(value string) (time.Time, error) {
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed, nil
	}
	return time.Parse("2006-01-02", value)
}

func insertStatusHistoryTx(ctx context.Context, tx pgx.Tx, session identity.Session, documentID, from, to, reason string) error {
	_, err := tx.Exec(ctx, `INSERT INTO document_status_history(id,company_id,document_id,from_status,to_status,actor_user_id,reason) VALUES($1,$2,$3,NULLIF($4,''),$5,$6,$7)`, uuid.NewString(), session.CurrentCompanyID, documentID, from, to, nullableUUID(session.User.ID), reason)
	return err
}

func writeAuditAndEventTx(ctx context.Context, tx pgx.Tx, session identity.Session, eventType, outboxType, entityType, entityID string, meta identity.RequestMeta, payload map[string]any) error {
	if payload == nil {
		payload = map[string]any{}
	}
	payload["schema_version"] = 1
	payload["entity_id"] = entityID
	if _, err := tx.Exec(ctx, `INSERT INTO security_audit_events(id,company_id,actor_user_id,event_type,entity_type,entity_id,details,trace_id,source_ip,user_agent) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, uuid.NewString(), session.CurrentCompanyID, nullableUUID(session.User.ID), eventType, entityType, nullableUUID(entityID), jsonBytes(payload), meta.TraceID, meta.IP, truncate(meta.UserAgent, 512)); err != nil {
		return err
	}
	encoded := []byte(`{}`)
	if len(payload) > 0 {
		encoded = jsonBytes(payload)
	}
	_, err := tx.Exec(ctx, `INSERT INTO outbox_events(event_id,type,schema_version,company_id,trace_id,payload) VALUES($1,$2,1,$3,$4,$5)`, uuid.NewString(), outboxType, session.CurrentCompanyID, meta.TraceID, encoded)
	return err
}

// mapSalesConstraint turns provider-specific PostgreSQL integrity failures into
// stable domain errors so handlers do not surface a 500 for user-correctable
// input. A duplicate document number is the common case: the user typed a
// belge numarası that already exists for this company/document type.
func mapSalesConstraint(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && strings.Contains(pgErr.ConstraintName, "document_no") {
		return &CommercialError{
			Code:  CommercialErrorDuplicateDocumentNo,
			Field: "document_no",
			Err:   errors.New("bu belge numarası zaten kullanılıyor"),
		}
	}
	return err
}

func nullableUUID(value string) any {
	if uuid.Validate(value) != nil {
		return nil
	}
	return value
}

func nullableString(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func jsonBytes(value any) []byte {
	encoded, _ := json.Marshal(value)
	return encoded
}

func truncate(value string, max int) string {
	if len(value) > max {
		return value[:max]
	}
	return value
}

func contains(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}
