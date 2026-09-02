package party

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
	"unicode"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/money"
	"github.com/alpyxn/varyaone/internal/platform/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type Service struct{ pool database.Querier }

func NewService(pool database.Querier) *Service { return &Service{pool: pool} }

type Party struct {
	ID                  string  `json:"id"`
	Code                string  `json:"code"`
	Kind                string  `json:"kind"`
	IsCustomer          bool    `json:"is_customer"`
	IsSupplier          bool    `json:"is_supplier"`
	DisplayName         string  `json:"display_name"`
	LegalName           *string `json:"legal_name,omitempty"`
	TradeName           *string `json:"trade_name,omitempty"`
	FirstName           *string `json:"first_name,omitempty"`
	LastName            *string `json:"last_name,omitempty"`
	TaxNumber           *string `json:"tax_number,omitempty"`
	IdentityNumber      *string `json:"identity_number,omitempty"`
	TaxOffice           *string `json:"tax_office,omitempty"`
	TaxOfficeID         *string `json:"tax_office_id,omitempty"`
	DefaultCurrency     string  `json:"default_currency"`
	PaymentTermID       *string `json:"payment_term_id,omitempty"`
	PriceListID         *string `json:"price_list_id,omitempty"`
	DefaultDiscountRate string  `json:"default_discount_rate"`
	SalesRepUserID      *string `json:"sales_rep_user_id,omitempty"`
	CreditLimit         string  `json:"credit_limit"`
	RiskLimit           string  `json:"risk_limit"`
	RiskPolicy          string  `json:"risk_policy"`
	Phone               string  `json:"phone"`
	Email               string  `json:"email"`
	City                string  `json:"city"`
	AddressSummary      string  `json:"address_summary"`
	ContactSummary      string  `json:"contact_summary"`
	GroupSummary        string  `json:"group_summary"`
	TagSummary          string  `json:"tag_summary"`
	CustomFieldSummary  string  `json:"custom_field_summary"`
	PaymentTermName     string  `json:"payment_term_name"`
	SalesRepName        string  `json:"sales_rep_name"`
	Balance             string  `json:"balance"`
	// BalanceCurrency is always the company's base currency.  A party can
	// transact in several currencies; the card balance must never silently
	// switch units when its default currency changes.
	BalanceCurrency string         `json:"balance_currency"`
	IsActive        bool           `json:"is_active"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	Version         int64          `json:"version"`
	Addresses       []Address      `json:"addresses"`
	Contacts        []Contact      `json:"contacts"`
	Groups          []string       `json:"group_ids"`
	Tags            []string       `json:"tags"`
	Warnings        []string       `json:"warnings,omitempty"`
	CustomFields    map[string]any `json:"custom_fields"`
}

type Address struct {
	ID               string `json:"id,omitempty"`
	AddressLine      string `json:"address_line"`
	ProvinceID       *int64 `json:"province_id,omitempty"`
	ProvinceName     string `json:"province_name,omitempty"`
	DistrictID       *int64 `json:"district_id,omitempty"`
	DistrictName     string `json:"district_name,omitempty"`
	NeighborhoodID   *int64 `json:"neighborhood_id,omitempty"`
	NeighborhoodName string `json:"neighborhood_name,omitempty"`
	// City, District and Neighborhood remain the write-compatible aliases used
	// by older clients. Hierarchical addresses return the canonical reference
	// names in both the aliases and the explicit *Name fields.
	District     string `json:"district"`
	City         string `json:"city"`
	Neighborhood string `json:"neighborhood,omitempty"`
	IsDefault    bool   `json:"is_default"`
}

type TurkishProvince struct {
	ID        int64  `json:"id"`
	PlateCode string `json:"plate_code"`
	Name      string `json:"name"`
}

type TurkishDistrict struct {
	ID         int64  `json:"id"`
	ProvinceID int64  `json:"province_id"`
	Name       string `json:"name"`
}

type TurkishNeighborhood struct {
	ID         int64  `json:"id"`
	DistrictID int64  `json:"district_id"`
	Name       string `json:"name"`
}

type AddressPreference struct {
	ProvinceID       *int64     `json:"province_id,omitempty"`
	ProvinceName     string     `json:"province_name,omitempty"`
	DistrictID       *int64     `json:"district_id,omitempty"`
	DistrictName     string     `json:"district_name,omitempty"`
	NeighborhoodID   *int64     `json:"neighborhood_id,omitempty"`
	NeighborhoodName string     `json:"neighborhood_name,omitempty"`
	Version          int64      `json:"version"`
	UpdatedAt        *time.Time `json:"updated_at,omitempty"`
}

type Contact struct {
	ID         string `json:"id,omitempty"`
	FullName   string `json:"full_name"`
	Title      string `json:"title"`
	Department string `json:"department"`
	Email      string `json:"email"`
	Phone      string `json:"phone"`
	IsPrimary  bool   `json:"is_primary"`
}

type Input struct {
	Code string `json:"code"`
	Kind string `json:"kind"`
	// IsActive is accepted so the card form can round-trip the master status.
	// Deactivation itself remains a separate permission-checked operation.
	IsActive            bool                       `json:"is_active"`
	IsCustomer          bool                       `json:"is_customer"`
	IsSupplier          bool                       `json:"is_supplier"`
	DisplayName         string                     `json:"display_name"`
	LegalName           string                     `json:"legal_name"`
	TradeName           string                     `json:"trade_name"`
	FirstName           string                     `json:"first_name"`
	LastName            string                     `json:"last_name"`
	TaxNumber           string                     `json:"tax_number"`
	IdentityNumber      string                     `json:"identity_number"`
	TaxOffice           string                     `json:"tax_office"`
	TaxOfficeID         string                     `json:"tax_office_id"`
	DefaultCurrency     string                     `json:"default_currency"`
	PaymentTermID       string                     `json:"payment_term_id"`
	PriceListID         string                     `json:"price_list_id"`
	DefaultDiscountRate string                     `json:"default_discount_rate"`
	SalesRepUserID      string                     `json:"sales_rep_user_id"`
	CreditLimit         string                     `json:"credit_limit"`
	RiskLimit           string                     `json:"risk_limit"`
	RiskPolicy          string                     `json:"risk_policy"`
	Addresses           []Address                  `json:"addresses"`
	Contacts            []Contact                  `json:"contacts"`
	GroupIDs            []string                   `json:"group_ids"`
	Tags                []string                   `json:"tags"`
	CustomFields        map[string]json.RawMessage `json:"custom_fields"`
}

type ListResult struct {
	Items      []Party `json:"items"`
	NextCursor string  `json:"next_cursor,omitempty"`
}

type Balance struct {
	Currency string `json:"currency"`
	Debit    string `json:"debit"`
	Credit   string `json:"credit"`
	Balance  string `json:"balance"`
	// BaseBalance is this currency's balance converted to the company base
	// currency using each entry's own recorded rate, so callers can present a
	// single combined figure without summing unlike currencies.
	BaseBalance  string `json:"base_balance"`
	BaseCurrency string `json:"base_currency"`
}

// BalanceResult is the canonical party balance response. Items retain the
// native currency breakdown while BaseBalance is the single company-base
// total used by cards, risk and list views.
type BalanceResult struct {
	Items        []Balance `json:"items"`
	BaseCurrency string    `json:"base_currency"`
	BaseBalance  string    `json:"base_balance"`
}

type LedgerEntry struct {
	ID         string `json:"id"`
	PartyID    string `json:"party_id"`
	PartyCode  string `json:"party_code,omitempty"`
	PartyName  string `json:"party_name,omitempty"`
	Currency   string `json:"currency"`
	EntryType  string `json:"entry_type"`
	SourceType string `json:"source_type"`
	SourceID   string `json:"source_id"`
	// SourceLabel/SourceHref name and (where possible) deep-link the record
	// this entry originated from, so the UI never has to show a bare UUID.
	SourceLabel    string         `json:"source_label,omitempty"`
	SourceHref     string         `json:"source_href,omitempty"`
	Status         string         `json:"status"`
	IdempotencyKey string         `json:"-"`
	Description    string         `json:"description"`
	Debit          string         `json:"debit"`
	Credit         string         `json:"credit"`
	ExchangeRate   string         `json:"exchange_rate"`
	BaseCurrency   *string        `json:"base_currency,omitempty"`
	BaseAmount     *string        `json:"base_amount,omitempty"`
	DocumentDate   time.Time      `json:"document_date"`
	DueDate        *time.Time     `json:"due_date,omitempty"`
	DocumentNo     string         `json:"document_no,omitempty"`
	ReferenceNo    string         `json:"reference_no,omitempty"`
	PostedAt       time.Time      `json:"posted_at"`
	ReversalOfID   *string        `json:"reversal_of_id,omitempty"`
	ActorUserID    *string        `json:"actor_user_id,omitempty"`
	ActorName      string         `json:"actor_name,omitempty"`
	Snapshot       map[string]any `json:"snapshot"`
	// RunningBalance is populated only by StatementReport: the party balance
	// (debit − credit, cumulative) as of this entry, including the opening
	// balance carried from before the report's start date.
	RunningBalance string `json:"running_balance,omitempty"`
}

// StatementReport is the cari ekstre read model: ordered entries (oldest first)
// each carrying a running balance, plus the period summary the header needs.
type StatementReport struct {
	Items          []LedgerEntry `json:"items"`
	Currency       string        `json:"currency,omitempty"`
	OpeningBalance string        `json:"opening_balance"`
	ClosingBalance string        `json:"closing_balance"`
	TotalDebit     string        `json:"total_debit"`
	TotalCredit    string        `json:"total_credit"`
	NextCursor     string        `json:"next_cursor,omitempty"`
}

type LedgerListResult struct {
	Items      []LedgerEntry `json:"items"`
	NextCursor string        `json:"next_cursor,omitempty"`
}

// LedgerEntryDetail is the read model for a single immutable cari hareket.
// The embedded entry keeps the response compatible with the list/create
// representation while exposing the append-only correction relationships.
type LedgerEntryDetail struct {
	LedgerEntry
	ReversalOf *LedgerEntry `json:"reversal_of,omitempty"`
	ReversedBy *LedgerEntry `json:"reversed_by,omitempty"`
}

// GetLedgerEntry returns one company-scoped immutable ledger entry and its
// correction links. Missing rows deliberately return ErrForbidden so callers
// cannot use this endpoint to distinguish another company's UUIDs.
func (s *Service) GetLedgerEntry(ctx context.Context, session identity.Session, id string) (LedgerEntryDetail, error) {
	if !authorized(session, "party.ledger.read") {
		return LedgerEntryDetail{}, identity.ErrForbidden
	}
	if _, err := uuid.Parse(strings.TrimSpace(id)); err != nil {
		return LedgerEntryDetail{}, fmt.Errorf("%w: cari hareket kimliği geçersiz", identity.ErrValidation)
	}

	entry, err := getLedgerEntryByID(ctx, s.pool, session.CurrentCompanyID, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return LedgerEntryDetail{}, identity.ErrForbidden
	}
	if err != nil {
		return LedgerEntryDetail{}, err
	}
	detail := LedgerEntryDetail{LedgerEntry: entry}
	if entry.ReversalOfID != nil {
		original, originalErr := getLedgerEntryByID(ctx, s.pool, session.CurrentCompanyID, *entry.ReversalOfID)
		if originalErr == nil {
			detail.ReversalOf = &original
		} else if !errors.Is(originalErr, pgx.ErrNoRows) {
			return LedgerEntryDetail{}, originalErr
		}
	}
	var reversalID *string
	err = s.pool.QueryRow(ctx, `SELECT id FROM party_ledger_entries WHERE company_id=$1 AND reversal_of_id=$2`, session.CurrentCompanyID, entry.ID).Scan(&reversalID)
	if errors.Is(err, pgx.ErrNoRows) {
		return detail, nil
	}
	if err != nil {
		return LedgerEntryDetail{}, err
	}
	if reversalID != nil {
		reversal, reversalErr := getLedgerEntryByID(ctx, s.pool, session.CurrentCompanyID, *reversalID)
		if reversalErr == nil {
			detail.ReversedBy = &reversal
		} else if !errors.Is(reversalErr, pgx.ErrNoRows) {
			return LedgerEntryDetail{}, reversalErr
		}
	}
	return detail, nil
}

func getLedgerEntryByID(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, companyID, id string) (LedgerEntry, error) {
	var item LedgerEntry
	var snapshot []byte
	err := q.QueryRow(ctx, `SELECT l.id,l.party_id,COALESCE(p.code,''),COALESCE(p.display_name,''),l.currency,l.entry_type,l.source_type,l.source_id,l.description,l.debit::text,l.credit::text,l.exchange_rate::text,l.base_currency,l.base_amount::text,l.document_date,l.posted_at,l.reversal_of_id,l.actor_user_id,COALESCE(u.display_name,u.email,''),l.snapshot FROM party_ledger_balance_effects l LEFT JOIN parties p ON p.company_id=l.company_id AND p.id=l.party_id LEFT JOIN users u ON u.id=l.actor_user_id WHERE l.company_id=$1 AND l.id=$2`, companyID, id).Scan(&item.ID, &item.PartyID, &item.PartyCode, &item.PartyName, &item.Currency, &item.EntryType, &item.SourceType, &item.SourceID, &item.Description, &item.Debit, &item.Credit, &item.ExchangeRate, &item.BaseCurrency, &item.BaseAmount, &item.DocumentDate, &item.PostedAt, &item.ReversalOfID, &item.ActorUserID, &item.ActorName, &snapshot)
	if err != nil {
		return LedgerEntry{}, err
	}
	hydrateLedgerMetadata(&item, snapshot)
	return item, nil
}

// hydrateLedgerMetadata restores fields that are intentionally kept inside
// the immutable snapshot so the legacy ledger table remains append-only and
// existing SQL projections stay compatible.
func hydrateLedgerMetadata(item *LedgerEntry, raw []byte) {
	if item == nil {
		return
	}
	if item.Status == "" {
		item.Status = "POSTED"
		if item.ReversalOfID != nil {
			item.Status = "REVERSED"
		}
	}
	_ = json.Unmarshal(raw, &item.Snapshot)
	if item.Snapshot == nil {
		item.Snapshot = map[string]any{}
	}
	if value, ok := item.Snapshot["reference_no"].(string); ok {
		item.ReferenceNo = strings.TrimSpace(value)
	}
	if value, ok := item.Snapshot["document_no"].(string); ok {
		item.DocumentNo = strings.TrimSpace(value)
	}
	if value, ok := item.Snapshot["due_date"].(string); ok && strings.TrimSpace(value) != "" {
		if parsed, err := time.Parse("2006-01-02", value); err == nil {
			item.DueDate = &parsed
		}
	}
	decorateLedgerSource(item)
}

// decorateLedgerSource fills SourceLabel/SourceHref from the entry's
// source_type + entry_type (+ the document type carried in the snapshot) so
// the UI can name and link the originating record instead of printing a UUID.
func decorateLedgerSource(item *LedgerEntry) {
	switch item.SourceType {
	case "document":
		docType, _ := item.Snapshot["document_type"].(string)
		label, base := "Belge", ""
		switch docType {
		case "SALES_INVOICE":
			label, base = "Satış faturası", "/satis/faturalar/"
		case "SALES_RETURN_INVOICE":
			label, base = "Satış iade faturası", "/satis/iadeler/"
		case "PURCHASE_INVOICE":
			label, base = "Alış faturası", "/alis/faturalar/"
		case "PURCHASE_RETURN_INVOICE":
			label, base = "Alış iade faturası", "/alis/iadeler/"
		}
		item.SourceLabel = label
		if base != "" && item.SourceID != "" {
			item.SourceHref = base + item.SourceID
		}
	case "finance_payment":
		switch item.EntryType {
		case "COLLECTION":
			item.SourceLabel, item.SourceHref = "Tahsilat", "/cari/tahsilatlar/"+item.SourceID
		case "PAYMENT":
			item.SourceLabel, item.SourceHref = "Ödeme", "/cari/odemeler/"+item.SourceID
		case "REVERSAL":
			item.SourceLabel = "Tahsilat / ödeme ters kaydı"
		default:
			item.SourceLabel = "Tahsilat / ödeme"
		}
	case "finance_manual_entry":
		item.SourceLabel = "Manuel cari kaydı"
	case "finance_party_transfer":
		item.SourceLabel = "Cari virman"
	}
}

func sameDueDate(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Format("2006-01-02") == right.Format("2006-01-02")
}

func sameLedgerLink(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

// canonicalDecimal keeps idempotency comparisons independent from the scale
// the caller happened to use ("10", "10.0" and "10.0000" are one amount).
func canonicalDecimal(value money.Decimal, scale int) string {
	r, ok := new(big.Rat).SetString(value.String())
	if !ok {
		return value.String()
	}
	return r.FloatString(scale)
}

// baseAmountRoundsToZero prevents a positive foreign-currency movement from
// being accepted when its 4-decimal base snapshot would become zero.
func baseAmountRoundsToZero(debit, credit, rate string) bool {
	amount := debit
	if amount == "" || amount == "0" || amount == "0.0" || amount == "0.00" || amount == "0.000" || amount == "0.0000" {
		amount = credit
	}
	a, ok := new(big.Rat).SetString(strings.TrimSpace(amount))
	if !ok {
		return false
	}
	r, ok := new(big.Rat).SetString(strings.TrimSpace(rate))
	if !ok {
		return false
	}
	product := new(big.Rat).Mul(a, r)
	return product.Sign() > 0 && product.FloatString(4) == "0.0000"
}

// ListLedgerEntries is the company-scoped read model used by the Cari
// hareketler screen. A party filter is optional for the list view; the
// running-balance UI still supplies one party and one currency separately.
func (s *Service) ListLedgerEntries(ctx context.Context, session identity.Session, partyID, currency, cursor string, limit int, from, to *time.Time) (LedgerListResult, error) {
	if !authorized(session, "party.ledger.read") {
		return LedgerListResult{}, identity.ErrForbidden
	}
	if limit < 1 || limit > 200 {
		limit = 50
	}
	partyID = strings.TrimSpace(partyID)
	if partyID != "" {
		partyUUID, parseErr := uuid.Parse(partyID)
		if parseErr != nil {
			return LedgerListResult{}, fmt.Errorf("%w: cari kimliği geçersiz", identity.ErrValidation)
		}
		partyID = partyUUID.String()
	}
	if from != nil && to != nil && to.Format("2006-01-02") < from.Format("2006-01-02") {
		return LedgerListResult{}, fmt.Errorf("%w: hareket tarih aralığı geçersiz", identity.ErrValidation)
	}
	args := []any{session.CurrentCompanyID}
	historyWhere := `e.company_id=$1`
	pagePredicates := make([]string, 0, 3)
	if partyID != "" {
		if err := s.ensurePartyCompany(ctx, session.CurrentCompanyID, partyID); err != nil {
			return LedgerListResult{}, err
		}
		args = append(args, partyID)
		historyWhere += fmt.Sprintf(` AND e.party_id=$%d`, len(args))
	}
	currencyValue := strings.ToUpper(strings.TrimSpace(currency))
	if currencyValue != "" {
		if len(currencyValue) != 3 || strings.Trim(currencyValue, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "" {
			return LedgerListResult{}, fmt.Errorf("%w: hareket para birimi geçersiz", identity.ErrValidation)
		}
		currency = currencyValue
		args = append(args, currencyValue)
		historyWhere += fmt.Sprintf(` AND e.currency=$%d`, len(args))
	}
	if from != nil {
		args = append(args, from.Format("2006-01-02"))
		pagePredicates = append(pagePredicates, fmt.Sprintf(`o.document_date >= $%d::date`, len(args)))
	}
	if to != nil {
		args = append(args, to.Format("2006-01-02"))
		pagePredicates = append(pagePredicates, fmt.Sprintf(`o.document_date <= $%d::date`, len(args)))
	}
	// The movement cursor includes all three ordering columns. A date/id-only
	// cursor can skip same-day rows when several entries share a timestamp.
	if cursor != "" {
		lastDate, lastPostedAt, lastID, err := decodeLedgerCursor(cursor)
		if err != nil {
			return LedgerListResult{}, fmt.Errorf("%w: hareket listesi cursor bilgisi geçersiz", identity.ErrValidation)
		}
		args = append(args, lastDate.Format("2006-01-02"), lastPostedAt, lastID)
		pagePredicates = append(pagePredicates, fmt.Sprintf(`(o.document_date,o.posted_at,o.id) < ($%d::date,$%d::timestamptz,$%d::uuid)`, len(args)-2, len(args)-1, len(args)))
	}
	args = append(args, limit+1)
	outerWhere := ""
	if len(pagePredicates) > 0 {
		outerWhere = " WHERE " + strings.Join(pagePredicates, " AND ")
	}
	// Native running values are meaningful only inside one currency. For an
	// unfiltered list we expose the company-base running balance instead.
	runningExpr := `SUM(e.signed_amount) OVER (PARTITION BY e.party_id,e.currency ORDER BY e.document_date,e.posted_at,e.id ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW)`
	if strings.TrimSpace(currency) == "" {
		runningExpr = `SUM(e.base_signed_amount) OVER (PARTITION BY e.party_id ORDER BY e.document_date,e.posted_at,e.id ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW)`
	}
	query := `WITH ordered AS (SELECT e.id,e.party_id,e.currency,e.entry_type,e.source_type,e.source_id,e.description,e.debit,e.credit,e.exchange_rate,e.base_currency,e.base_amount,e.document_date,e.posted_at,e.reversal_of_id,e.actor_user_id,e.snapshot,` + runningExpr + ` AS running_balance FROM party_ledger_balance_effects e WHERE ` + historyWhere + `) SELECT o.id,o.party_id,COALESCE(p.code,''),COALESCE(p.display_name,''),o.currency,o.entry_type,o.source_type,o.source_id,o.description,o.debit::text,o.credit::text,o.exchange_rate::text,o.base_currency,o.base_amount::text,o.document_date,o.posted_at,o.reversal_of_id,o.actor_user_id,COALESCE(u.display_name,u.email,''),o.snapshot,o.running_balance::text FROM ordered o LEFT JOIN parties p ON p.company_id=$1 AND p.id=o.party_id LEFT JOIN users u ON u.id=o.actor_user_id` + outerWhere + ` ORDER BY o.document_date DESC,o.posted_at DESC,o.id DESC LIMIT $` + fmt.Sprintf("%d", len(args))
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return LedgerListResult{}, err
	}
	defer rows.Close()
	result := LedgerListResult{Items: make([]LedgerEntry, 0, limit)}
	for rows.Next() {
		var item LedgerEntry
		var snapshot []byte
		if err = rows.Scan(&item.ID, &item.PartyID, &item.PartyCode, &item.PartyName, &item.Currency, &item.EntryType, &item.SourceType, &item.SourceID, &item.Description, &item.Debit, &item.Credit, &item.ExchangeRate, &item.BaseCurrency, &item.BaseAmount, &item.DocumentDate, &item.PostedAt, &item.ReversalOfID, &item.ActorUserID, &item.ActorName, &snapshot, &item.RunningBalance); err != nil {
			return LedgerListResult{}, err
		}
		hydrateLedgerMetadata(&item, snapshot)
		result.Items = append(result.Items, item)
	}
	if err = rows.Err(); err != nil {
		return LedgerListResult{}, err
	}
	if len(result.Items) > limit {
		last := result.Items[limit-1]
		result.Items = result.Items[:limit]
		result.NextCursor = encodeLedgerCursor(last.DocumentDate, last.PostedAt, last.ID)
	}
	return result, nil
}

func (s *Service) Create(ctx context.Context, session identity.Session, input Input, meta identity.RequestMeta) (Party, error) {
	if !authorized(session, "party.create") {
		return Party{}, identity.ErrForbidden
	}
	if err := normalizeAndValidate(&input); err != nil {
		return Party{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Party{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err = suppressPartySearch(ctx, tx); err != nil {
		return Party{}, err
	}
	if err = resolveTaxOfficeSelection(ctx, tx, &input, ""); err != nil {
		return Party{}, err
	}
	if err = lockPartyCodeSequence(ctx, tx, session.CurrentCompanyID); err != nil {
		return Party{}, err
	}
	if input.Code == "" {
		input.Code, err = nextCode(ctx, tx, session.CurrentCompanyID)
		if err != nil {
			return Party{}, err
		}
	}
	warnings, err := duplicateTaxWarnings(ctx, tx, session.CurrentCompanyID, "", input.TaxNumber, input.IdentityNumber)
	if err != nil {
		return Party{}, err
	}
	id := uuid.NewString()
	_, err = tx.Exec(ctx, `INSERT INTO parties(
		id,company_id,code,kind,is_customer,is_supplier,display_name,legal_name,trade_name,first_name,last_name,tax_number,identity_number,tax_office,tax_office_id,
		default_currency,payment_term_id,price_list_id,default_discount_rate,sales_rep_user_id,credit_limit,risk_limit,risk_policy)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23)`,
		id, session.CurrentCompanyID, input.Code, input.Kind, input.IsCustomer, input.IsSupplier, input.DisplayName,
		nullString(input.LegalName), nullString(input.TradeName), nullString(input.FirstName), nullString(input.LastName), nullString(input.TaxNumber), nullString(input.IdentityNumber), nullString(input.TaxOffice), nullString(input.TaxOfficeID),
		input.DefaultCurrency, nullString(input.PaymentTermID), nullString(input.PriceListID), input.DefaultDiscountRate, nullString(input.SalesRepUserID), input.CreditLimit, input.RiskLimit, input.RiskPolicy)
	if err != nil {
		return Party{}, mapConstraint(err)
	}
	if err = replaceDetails(ctx, tx, session.CurrentCompanyID, id, input); err != nil {
		return Party{}, mapConstraint(err)
	}
	if err = writeAuditAndEvent(ctx, tx, session, "PARTY_CREATED", "party.created", "party", id, map[string]any{"party_id": id}, meta); err != nil {
		return Party{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Party{}, err
	}
	result, err := s.Get(ctx, session, id)
	result.Warnings = warnings
	return result, err
}

func (s *Service) Update(ctx context.Context, session identity.Session, id string, expectedVersion int64, input Input, meta identity.RequestMeta) (Party, error) {
	if !authorized(session, "party.edit") {
		return Party{}, identity.ErrForbidden
	}
	if expectedVersion < 1 {
		return Party{}, fmt.Errorf("%w: geçerli If-Match sürümü gereklidir", identity.ErrValidation)
	}
	if err := normalizeAndValidate(&input); err != nil {
		return Party{}, err
	}
	if input.Code == "" {
		return Party{}, fmt.Errorf("%w: güncellemede cari kodu boş bırakılamaz", identity.ErrValidation)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Party{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err = suppressPartySearch(ctx, tx); err != nil {
		return Party{}, err
	}
	var currentCode, currentKind string
	var currentTaxOfficeID string
	if err = tx.QueryRow(ctx, `SELECT code,kind,COALESCE(tax_office_id::text,'') FROM parties WHERE company_id=$1 AND id=$2 FOR UPDATE`, session.CurrentCompanyID, id).Scan(&currentCode, &currentKind, &currentTaxOfficeID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Party{}, identity.ErrForbidden
		}
		return Party{}, err
	}
	if err = validateImmutableIdentity(input, currentCode, currentKind); err != nil {
		return Party{}, err
	}
	if err = resolveTaxOfficeSelection(ctx, tx, &input, currentTaxOfficeID); err != nil {
		return Party{}, err
	}
	warnings, err := duplicateTaxWarnings(ctx, tx, session.CurrentCompanyID, id, input.TaxNumber, input.IdentityNumber)
	if err != nil {
		return Party{}, err
	}
	tag, err := tx.Exec(ctx, `UPDATE parties SET code=$1,kind=$2,is_customer=$3,is_supplier=$4,display_name=$5,legal_name=$6,trade_name=$7,first_name=$8,last_name=$9,tax_number=$10,identity_number=$11,tax_office=$12,tax_office_id=$13,default_currency=$14,payment_term_id=$15,price_list_id=$16,default_discount_rate=$17,sales_rep_user_id=$18,credit_limit=$19,risk_limit=$20,risk_policy=$21,updated_at=now(),version=version+1 WHERE company_id=$22 AND id=$23 AND version=$24`,
		input.Code, input.Kind, input.IsCustomer, input.IsSupplier, input.DisplayName, nullString(input.LegalName), nullString(input.TradeName), nullString(input.FirstName), nullString(input.LastName), nullString(input.TaxNumber), nullString(input.IdentityNumber), nullString(input.TaxOffice), nullString(input.TaxOfficeID), input.DefaultCurrency, nullString(input.PaymentTermID), nullString(input.PriceListID), input.DefaultDiscountRate, nullString(input.SalesRepUserID), input.CreditLimit, input.RiskLimit, input.RiskPolicy, session.CurrentCompanyID, id, expectedVersion)
	if err != nil {
		return Party{}, mapConstraint(err)
	}
	if tag.RowsAffected() == 0 {
		return Party{}, identity.ErrConflict
	}
	if err = replaceDetails(ctx, tx, session.CurrentCompanyID, id, input); err != nil {
		return Party{}, mapConstraint(err)
	}
	if err = writeAuditAndEvent(ctx, tx, session, "PARTY_UPDATED", "party.updated", "party", id, map[string]any{"party_id": id}, meta); err != nil {
		return Party{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Party{}, err
	}
	result, err := s.Get(ctx, session, id)
	result.Warnings = warnings
	return result, err
}

func validateImmutableIdentity(input Input, currentCode, currentKind string) error {
	if input.Code != currentCode {
		return fmt.Errorf("%w: cari kodu oluşturulduktan sonra değiştirilemez", identity.ErrValidation)
	}
	if input.Kind != currentKind {
		return fmt.Errorf("%w: cari türü oluşturulduktan sonra değiştirilemez", identity.ErrValidation)
	}
	return nil
}

func (s *Service) Deactivate(ctx context.Context, session identity.Session, id string, expectedVersion int64, meta identity.RequestMeta) (Party, error) {
	if !authorized(session, "party.deactivate") {
		return Party{}, identity.ErrForbidden
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Party{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	tag, err := tx.Exec(ctx, `UPDATE parties SET is_active=false,deactivated_at=now(),updated_at=now(),version=version+1 WHERE company_id=$1 AND id=$2 AND version=$3 AND is_active`, session.CurrentCompanyID, id, expectedVersion)
	if err != nil {
		return Party{}, err
	}
	if tag.RowsAffected() == 0 {
		return Party{}, identity.ErrConflict
	}
	if err = writeAuditAndEvent(ctx, tx, session, "PARTY_DEACTIVATED", "party.deactivated", "party", id, map[string]any{"party_id": id}, meta); err != nil {
		return Party{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Party{}, err
	}
	return s.Get(ctx, session, id)
}

func (s *Service) Activate(ctx context.Context, session identity.Session, id string, expectedVersion int64, meta identity.RequestMeta) (Party, error) {
	if !authorized(session, "party.deactivate") {
		return Party{}, identity.ErrForbidden
	}
	if id == "" || expectedVersion < 1 {
		return Party{}, fmt.Errorf("%w: geçerli If-Match sürümü gereklidir", identity.ErrValidation)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Party{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	tag, err := tx.Exec(ctx, `UPDATE parties SET is_active=true,deactivated_at=NULL,updated_at=now(),version=version+1 WHERE company_id=$1 AND id=$2 AND version=$3 AND NOT is_active`, session.CurrentCompanyID, id, expectedVersion)
	if err != nil {
		return Party{}, err
	}
	if tag.RowsAffected() == 0 {
		return Party{}, identity.ErrConflict
	}
	if err = writeAuditAndEvent(ctx, tx, session, "PARTY_ACTIVATED", "party.activated", "party", id, map[string]any{"party_id": id}, meta); err != nil {
		return Party{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Party{}, err
	}
	return s.Get(ctx, session, id)
}

func (s *Service) Get(ctx context.Context, session identity.Session, id string) (Party, error) {
	if !authorized(session, "party.read") {
		return Party{}, identity.ErrForbidden
	}
	row := s.pool.QueryRow(ctx, partySelect+` WHERE p.company_id=$1 AND p.id=$2`, session.CurrentCompanyID, id)
	item, err := scanParty(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Party{}, identity.ErrForbidden
	}
	if err != nil {
		return Party{}, err
	}
	if err = s.loadDetails(ctx, session.CurrentCompanyID, &item); err != nil {
		return Party{}, err
	}
	return item, nil
}

// ListTurkishProvinces reads the embedded/global reference catalog. The
// company session is still required so an unauthorised caller cannot use the
// address endpoints as an anonymous data source.
func (s *Service) ListTurkishProvinces(ctx context.Context, session identity.Session) ([]TurkishProvince, error) {
	if !authorized(session, "party.read") {
		return nil, identity.ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `SELECT id,plate_code,name FROM turkish_provinces ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []TurkishProvince{}
	for rows.Next() {
		var item TurkishProvince
		if err = rows.Scan(&item.ID, &item.PlateCode, &item.Name); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) ListTurkishDistricts(ctx context.Context, session identity.Session, provinceID int64, query string, limit int) ([]TurkishDistrict, error) {
	if !authorized(session, "party.read") {
		return nil, identity.ErrForbidden
	}
	if provinceID < 1 || provinceID > 81 {
		return nil, fmt.Errorf("%w: geçersiz il", identity.ErrValidation)
	}
	limit = locationLimit(limit)
	query = strings.TrimSpace(query)
	args := []any{provinceID}
	where := ` WHERE province_id=$1`
	if query != "" {
		args = append(args, query)
		where += fmt.Sprintf(` AND name ILIKE '%%' || $%d || '%%'`, len(args))
	}
	args = append(args, limit)
	rows, err := s.pool.Query(ctx, `SELECT id,province_id,name FROM turkish_districts`+where+fmt.Sprintf(` ORDER BY lower(name),id LIMIT $%d`, len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []TurkishDistrict{}
	for rows.Next() {
		var item TurkishDistrict
		if err = rows.Scan(&item.ID, &item.ProvinceID, &item.Name); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) ListTurkishNeighborhoods(ctx context.Context, session identity.Session, districtID int64, query string, limit int) ([]TurkishNeighborhood, error) {
	if !authorized(session, "party.read") {
		return nil, identity.ErrForbidden
	}
	if districtID < 1 {
		return nil, fmt.Errorf("%w: geçersiz ilçe", identity.ErrValidation)
	}
	limit = locationLimit(limit)
	query = strings.TrimSpace(query)
	args := []any{districtID}
	where := ` WHERE district_id=$1`
	if query != "" {
		args = append(args, query)
		where += fmt.Sprintf(` AND name ILIKE '%%' || $%d || '%%'`, len(args))
	}
	args = append(args, limit)
	rows, err := s.pool.Query(ctx, `SELECT id,district_id,name FROM turkish_neighborhoods`+where+fmt.Sprintf(` ORDER BY lower(name),id LIMIT $%d`, len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []TurkishNeighborhood{}
	for rows.Next() {
		var item TurkishNeighborhood
		if err = rows.Scan(&item.ID, &item.DistrictID, &item.Name); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func locationLimit(limit int) int {
	if limit < 1 || limit > 2000 {
		return 1000
	}
	return limit
}

func (s *Service) GetAddressPreference(ctx context.Context, session identity.Session) (AddressPreference, error) {
	if session.CurrentCompanyID == "" || session.User.ID == "" {
		return AddressPreference{}, identity.ErrForbidden
	}
	var item AddressPreference
	var provinceID, districtID, neighborhoodID int64
	var updatedAt time.Time
	err := s.pool.QueryRow(ctx, `SELECT COALESCE(uap.province_id,0),COALESCE(NULLIF(uap.province_name,''),tp.name,''),COALESCE(uap.district_id,0),COALESCE(NULLIF(uap.district_name,''),td.name,''),COALESCE(uap.neighborhood_id,0),COALESCE(NULLIF(uap.neighborhood_name,''),tn.name,''),uap.version,uap.updated_at
		FROM user_address_preferences uap
		LEFT JOIN turkish_provinces tp ON tp.id=uap.province_id
		LEFT JOIN turkish_districts td ON td.province_id=uap.province_id AND td.id=uap.district_id
		LEFT JOIN turkish_neighborhoods tn ON tn.district_id=uap.district_id AND tn.id=uap.neighborhood_id
		WHERE uap.company_id=$1 AND uap.user_id=$2`, session.CurrentCompanyID, session.User.ID).
		Scan(&provinceID, &item.ProvinceName, &districtID, &item.DistrictName, &neighborhoodID, &item.NeighborhoodName, &item.Version, &updatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return AddressPreference{}, nil
	}
	if err != nil {
		return AddressPreference{}, err
	}
	if provinceID > 0 {
		item.ProvinceID = &provinceID
	}
	if districtID > 0 {
		item.DistrictID = &districtID
	}
	if neighborhoodID > 0 {
		item.NeighborhoodID = &neighborhoodID
	}
	item.UpdatedAt = &updatedAt
	return item, nil
}

func (s *Service) SaveAddressPreference(ctx context.Context, session identity.Session, input AddressPreference) (AddressPreference, error) {
	if session.CurrentCompanyID == "" || session.User.ID == "" {
		return AddressPreference{}, identity.ErrForbidden
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AddressPreference{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	provinceName := strings.TrimSpace(input.ProvinceName)
	districtName := strings.TrimSpace(input.DistrictName)
	neighborhoodName := strings.TrimSpace(input.NeighborhoodName)
	if districtName != "" && provinceName == "" {
		return AddressPreference{}, fmt.Errorf("%w: varsayılan ilçe için şehir gereklidir", identity.ErrValidation)
	}
	if neighborhoodName != "" && districtName == "" {
		return AddressPreference{}, fmt.Errorf("%w: varsayılan mahalle için ilçe gereklidir", identity.ErrValidation)
	}
	if input.ProvinceID != nil || input.DistrictID != nil || input.NeighborhoodID != nil {
		resolvedProvince, resolvedDistrict, resolvedNeighborhood, resolveErr := resolveLocationSelection(ctx, tx, input.ProvinceID, input.DistrictID, input.NeighborhoodID)
		if resolveErr != nil {
			return AddressPreference{}, resolveErr
		}
		provinceName, districtName, neighborhoodName = resolvedProvince, resolvedDistrict, resolvedNeighborhood
	}
	var provinceID, districtID, neighborhoodID any
	if input.ProvinceID != nil {
		provinceID = *input.ProvinceID
	}
	if input.DistrictID != nil {
		districtID = *input.DistrictID
	}
	if input.NeighborhoodID != nil {
		neighborhoodID = *input.NeighborhoodID
	}
	var item AddressPreference
	var savedProvinceID, savedDistrictID, savedNeighborhoodID int64
	var updatedAt time.Time
	err = tx.QueryRow(ctx, `INSERT INTO user_address_preferences(company_id,user_id,province_id,district_id,neighborhood_id,province_name,district_name,neighborhood_name)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT(company_id,user_id) DO UPDATE
		SET province_id=excluded.province_id,district_id=excluded.district_id,neighborhood_id=excluded.neighborhood_id,province_name=excluded.province_name,district_name=excluded.district_name,neighborhood_name=excluded.neighborhood_name,updated_at=now(),version=user_address_preferences.version+1
		RETURNING COALESCE(province_id,0),COALESCE(district_id,0),COALESCE(neighborhood_id,0),COALESCE(province_name,''),COALESCE(district_name,''),COALESCE(neighborhood_name,''),version,updated_at`, session.CurrentCompanyID, session.User.ID, provinceID, districtID, neighborhoodID, provinceName, districtName, neighborhoodName).
		Scan(&savedProvinceID, &savedDistrictID, &savedNeighborhoodID, &provinceName, &districtName, &neighborhoodName, &item.Version, &updatedAt)
	if err != nil {
		return AddressPreference{}, mapConstraint(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return AddressPreference{}, err
	}
	if savedProvinceID > 0 {
		item.ProvinceID = &savedProvinceID
	}
	if savedDistrictID > 0 {
		item.DistrictID = &savedDistrictID
	}
	if savedNeighborhoodID > 0 {
		item.NeighborhoodID = &savedNeighborhoodID
	}
	item.ProvinceName, item.DistrictName, item.NeighborhoodName = provinceName, districtName, neighborhoodName
	item.UpdatedAt = &updatedAt
	return item, nil
}

// List returns company-scoped cariler.  role is optional for callers that
// need the complete cari list; when supplied it is enforced in SQL so a
// customer picker cannot accidentally display supplier-only records (and
// vice versa).
func (s *Service) List(ctx context.Context, session identity.Session, query, cursor string, limit int, includeInactive bool, roles ...string) (ListResult, error) {
	if !authorized(session, "party.read") {
		return ListResult{}, identity.ErrForbidden
	}
	if limit < 1 || limit > 100 {
		limit = 25
	}
	afterName, afterID, err := decodeCursor(cursor)
	if err != nil {
		return ListResult{}, fmt.Errorf("%w: geçersiz cursor", identity.ErrValidation)
	}
	rawQuery := strings.TrimSpace(query)
	query = normalizePartySearchQuery(query)
	// A non-empty query made only of punctuation must remain a search that
	// matches nothing. Treating it as the empty-query path would silently show
	// every active cari and makes the search box look broken.
	if rawQuery != "" && query == "" {
		return ListResult{Items: []Party{}}, nil
	}
	listQuery := partySelect
	args := []any{session.CurrentCompanyID}
	if query != "" {
		// Keep the indexed search predicate separate from the empty-query path.
		// The OR form would make PostgreSQL consider a broad plan for both cases.
		listQuery += ` JOIN party_search_documents psd ON psd.company_id=p.company_id AND psd.party_id=p.id`
	}
	listQuery += ` WHERE p.company_id=$1`
	if !includeInactive {
		listQuery += ` AND p.is_active`
	}
	if len(roles) > 0 && strings.TrimSpace(roles[0]) != "" {
		role := strings.ToLower(strings.TrimSpace(roles[0]))
		switch role {
		case "customer":
			listQuery += ` AND p.is_customer`
		case "supplier":
			listQuery += ` AND p.is_supplier`
		default:
			return ListResult{}, fmt.Errorf("%w: geçersiz cari rolü filtresi", identity.ErrValidation)
		}
	}
	if query != "" {
		args = append(args, query)
		listQuery += fmt.Sprintf(` AND psd.search_vector @@ to_tsquery('simple', $%d)`, len(args))
	}
	nameParam := len(args) + 1
	idParam := nameParam + 1
	limitParam := idParam + 1
	listQuery += fmt.Sprintf(` AND (lower(p.display_name),p.id) > ($%d,$%d::uuid) ORDER BY lower(p.display_name),p.id LIMIT $%d`, nameParam, idParam, limitParam)
	args = append(args, afterName, afterID, limit+1)
	rows, err := s.pool.Query(ctx, listQuery, args...)
	if err != nil {
		return ListResult{}, err
	}
	defer rows.Close()
	items := []Party{}
	for rows.Next() {
		item, scanErr := scanParty(rows)
		if scanErr != nil {
			return ListResult{}, scanErr
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return ListResult{}, err
	}
	result := ListResult{Items: items}
	if len(items) > limit {
		last := items[limit-1]
		result.Items = items[:limit]
		result.NextCursor = encodeCursor(strings.ToLower(last.DisplayName), last.ID)
	}
	return result, nil
}

// Balances keeps the historical Go API (a native-currency slice) for callers
// that only need the breakdown. New consumers should use BalancesResult so
// the company-base total and its unit travel with the response.
func (s *Service) Balances(ctx context.Context, session identity.Session, partyID string) ([]Balance, error) {
	result, err := s.BalancesResult(ctx, session, partyID)
	if err != nil {
		return nil, err
	}
	return result.Items, nil
}

func (s *Service) BalancesResult(ctx context.Context, session identity.Session, partyID string) (BalanceResult, error) {
	result := BalanceResult{Items: []Balance{}}
	if !authorized(session, "party.ledger.read") {
		return result, identity.ErrForbidden
	}
	partyUUID, partyErr := uuid.Parse(strings.TrimSpace(partyID))
	if partyErr != nil {
		return result, fmt.Errorf("%w: cari kimliği geçersiz", identity.ErrValidation)
	}
	partyID = partyUUID.String()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err = ensurePartyCompanyQ(ctx, tx, session.CurrentCompanyID, partyID); err != nil {
		return result, err
	}
	if err = tx.QueryRow(ctx, `SELECT c.base_currency FROM parties p JOIN companies c ON c.id=p.company_id WHERE p.company_id=$1 AND p.id=$2`, session.CurrentCompanyID, partyID).Scan(&result.BaseCurrency); err != nil {
		return result, err
	}
	rows, err := tx.Query(ctx, `SELECT currency,COALESCE(SUM(debit),0)::text,COALESCE(SUM(credit),0)::text,COALESCE(SUM(signed_amount),0)::text,COALESCE(SUM(base_signed_amount),0)::text FROM party_ledger_balance_effects WHERE company_id=$1 AND party_id=$2 GROUP BY currency ORDER BY currency`, session.CurrentCompanyID, partyID)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var item Balance
		if err = rows.Scan(&item.Currency, &item.Debit, &item.Credit, &item.Balance, &item.BaseBalance); err != nil {
			return result, err
		}
		item.BaseCurrency = result.BaseCurrency
		result.Items = append(result.Items, item)
	}
	if err = rows.Err(); err != nil {
		return result, err
	}
	if err = tx.QueryRow(ctx, `SELECT COALESCE(SUM(base_signed_amount),0)::text FROM party_ledger_balance_effects WHERE company_id=$1 AND party_id=$2`, session.CurrentCompanyID, partyID).Scan(&result.BaseBalance); err != nil {
		return result, err
	}
	return result, nil
}

func (s *Service) Statement(ctx context.Context, session identity.Session, partyID string, from, to time.Time, limit int) ([]LedgerEntry, error) {
	if !authorized(session, "party.ledger.read") {
		return nil, identity.ErrForbidden
	}
	partyUUID, partyErr := uuid.Parse(strings.TrimSpace(partyID))
	if partyErr != nil {
		return nil, fmt.Errorf("%w: cari kimliği geçersiz", identity.ErrValidation)
	}
	partyID = partyUUID.String()
	if from.IsZero() || to.IsZero() || to.Format("2006-01-02") < from.Format("2006-01-02") {
		return nil, fmt.Errorf("%w: ekstre tarih aralığı geçersiz", identity.ErrValidation)
	}
	if err := s.ensurePartyCompany(ctx, session.CurrentCompanyID, partyID); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `SELECT id,party_id,currency,entry_type,source_type,source_id,description,debit::text,credit::text,exchange_rate::text,base_currency,base_amount::text,document_date,posted_at,reversal_of_id,snapshot FROM party_ledger_balance_effects WHERE company_id=$1 AND party_id=$2 AND document_date BETWEEN $3::date AND $4::date ORDER BY document_date DESC,posted_at DESC,id DESC LIMIT $5`, session.CurrentCompanyID, partyID, from.Format("2006-01-02"), to.Format("2006-01-02"), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []LedgerEntry{}
	for rows.Next() {
		var item LedgerEntry
		var snapshot []byte
		if err = rows.Scan(&item.ID, &item.PartyID, &item.Currency, &item.EntryType, &item.SourceType, &item.SourceID, &item.Description, &item.Debit, &item.Credit, &item.ExchangeRate, &item.BaseCurrency, &item.BaseAmount, &item.DocumentDate, &item.PostedAt, &item.ReversalOfID, &snapshot); err != nil {
			return nil, err
		}
		// DueDate is carried in the snapshot for manual entries; hydrateLedgerMetadata fills it.
		hydrateLedgerMetadata(&item, snapshot)
		items = append(items, item)
	}
	return items, rows.Err()
}

// StatementReport returns the complete first page of a cari ekstre. It keeps
// the original method signature while the HTTP endpoint uses
// StatementReportPage for explicit cursor/order control.
func (s *Service) StatementReport(ctx context.Context, session identity.Session, partyID string, from, to time.Time, currency string, limit int) (StatementReport, error) {
	return s.StatementReportPage(ctx, session, partyID, from, to, currency, "", "asc", limit)
}

// StatementReportPage calculates opening, period totals, closing and running
// lines from one repeatable-read snapshot. This prevents a concurrent post
// from producing a report whose header and rows disagree.
func (s *Service) StatementReportPage(ctx context.Context, session identity.Session, partyID string, from, to time.Time, currency, cursor, order string, limit int) (StatementReport, error) {
	if !authorized(session, "party.ledger.read") {
		return StatementReport{}, identity.ErrForbidden
	}
	partyUUID, partyErr := uuid.Parse(strings.TrimSpace(partyID))
	if partyErr != nil {
		return StatementReport{}, fmt.Errorf("%w: cari kimliği geçersiz", identity.ErrValidation)
	}
	partyID = partyUUID.String()
	if from.IsZero() || to.IsZero() {
		return StatementReport{}, fmt.Errorf("%w: ekstre tarih aralığı geçersiz", identity.ErrValidation)
	}
	fromDate, toDate := from.Format("2006-01-02"), to.Format("2006-01-02")
	if fromDate == "" || toDate == "" || toDate < fromDate {
		return StatementReport{}, fmt.Errorf("%w: ekstre tarih aralığı geçersiz", identity.ErrValidation)
	}
	if limit < 1 || limit > 1000 {
		limit = 500
	}
	order = strings.ToLower(strings.TrimSpace(order))
	if order == "" {
		order = "asc"
	}
	if order != "asc" && order != "desc" {
		return StatementReport{}, fmt.Errorf("%w: ekstre sıralaması asc veya desc olmalıdır", identity.ErrValidation)
	}
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency != "" && (len(currency) != 3 || strings.Trim(currency, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "") {
		return StatementReport{}, fmt.Errorf("%w: ekstre para birimi geçersiz", identity.ErrValidation)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return StatementReport{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err = ensurePartyCompanyQ(ctx, tx, session.CurrentCompanyID, partyID); err != nil {
		return StatementReport{}, err
	}
	report := StatementReport{Items: []LedgerEntry{}, Currency: currency}
	args := []any{session.CurrentCompanyID, partyID}
	where := `e.company_id=$1 AND e.party_id=$2`
	if currency != "" {
		args = append(args, currency)
		where += fmt.Sprintf(` AND e.currency=$%d`, len(args))
	} else if err = tx.QueryRow(ctx, `SELECT c.base_currency FROM parties p JOIN companies c ON c.id=p.company_id WHERE p.company_id=$1 AND p.id=$2`, session.CurrentCompanyID, partyID).Scan(&report.Currency); err != nil {
		return StatementReport{}, err
	}
	fromIdx := len(args) + 1
	toIdx := len(args) + 2
	args = append(args, fromDate, toDate)
	signedExpr, debitExpr, creditExpr := `e.signed_amount`, `e.debit`, `e.credit`
	if currency == "" {
		signedExpr = `e.base_signed_amount`
		debitExpr = `GREATEST(e.base_signed_amount,0)`
		creditExpr = `GREATEST(-e.base_signed_amount,0)`
	}
	summarySQL := `SELECT COALESCE(SUM(CASE WHEN e.document_date < $` + fmt.Sprintf("%d", fromIdx) + `::date THEN ` + signedExpr + ` ELSE 0 END),0)::text,COALESCE(SUM(CASE WHEN e.document_date BETWEEN $` + fmt.Sprintf("%d", fromIdx) + `::date AND $` + fmt.Sprintf("%d", toIdx) + `::date THEN ` + debitExpr + ` ELSE 0 END),0)::text,COALESCE(SUM(CASE WHEN e.document_date BETWEEN $` + fmt.Sprintf("%d", fromIdx) + `::date AND $` + fmt.Sprintf("%d", toIdx) + `::date THEN ` + creditExpr + ` ELSE 0 END),0)::text,COALESCE(SUM(CASE WHEN e.document_date <= $` + fmt.Sprintf("%d", toIdx) + `::date THEN ` + signedExpr + ` ELSE 0 END),0)::text FROM party_ledger_balance_effects e WHERE ` + where
	if err = tx.QueryRow(ctx, summarySQL, args...).Scan(&report.OpeningBalance, &report.TotalDebit, &report.TotalCredit, &report.ClosingBalance); err != nil {
		return StatementReport{}, err
	}
	lineArgs := append([]any{}, args...)
	lineWhere := where + fmt.Sprintf(` AND e.document_date BETWEEN $%d::date AND $%d::date`, fromIdx, toIdx)
	cursorWhere := ""
	openArg := len(lineArgs) + 1
	lineArgs = append(lineArgs, report.OpeningBalance)
	if cursor != "" {
		lastDate, lastPostedAt, lastID, decodeErr := decodeLedgerCursor(cursor)
		if decodeErr != nil {
			return StatementReport{}, fmt.Errorf("%w: ekstre cursor bilgisi geçersiz", identity.ErrValidation)
		}
		lineArgs = append(lineArgs, lastDate.Format("2006-01-02"), lastPostedAt, lastID)
		dateIdx, postedIdx, idIdx := len(lineArgs)-2, len(lineArgs)-1, len(lineArgs)
		operator := ">"
		if order == "desc" {
			operator = "<"
		}
		cursorWhere = fmt.Sprintf(` WHERE (o.document_date,o.posted_at,o.id) %s ($%d::date,$%d::timestamptz,$%d::uuid)`, operator, dateIdx, postedIdx, idIdx)
	}
	limitIdx := len(lineArgs) + 1
	lineArgs = append(lineArgs, limit+1)
	orderSQL := `ASC`
	if order == "desc" {
		orderSQL = `DESC`
	}
	// Running balance is computed in chronological order before pagination;
	// descending pages therefore still carry the balance as-of each row.
	lineSQL := `WITH ordered AS (SELECT e.id,e.party_id,e.currency,e.entry_type,e.source_type,e.source_id,e.description,e.debit,e.credit,e.exchange_rate,e.base_currency,e.base_amount,e.document_date,e.posted_at,e.reversal_of_id,e.actor_user_id,e.snapshot,` +
		`SUM(` + signedExpr + `) OVER (ORDER BY e.document_date,e.posted_at,e.id ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) AS period_running FROM party_ledger_balance_effects e WHERE ` + lineWhere + `) ` +
		`SELECT o.id,o.party_id,o.currency,o.entry_type,o.source_type,o.source_id,o.description,o.debit::text,o.credit::text,o.exchange_rate::text,o.base_currency,o.base_amount::text,o.document_date,o.posted_at,o.reversal_of_id,o.actor_user_id,o.snapshot,($` + fmt.Sprintf("%d", openArg) + `::numeric+o.period_running)::text FROM ordered o` + cursorWhere + ` ORDER BY o.document_date ` + orderSQL + `,o.posted_at ` + orderSQL + `,o.id ` + orderSQL + ` LIMIT $` + fmt.Sprintf("%d", limitIdx)
	rows, err := tx.Query(ctx, lineSQL, lineArgs...)
	if err != nil {
		return StatementReport{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var item LedgerEntry
		var snapshot []byte
		if err = rows.Scan(&item.ID, &item.PartyID, &item.Currency, &item.EntryType, &item.SourceType, &item.SourceID, &item.Description, &item.Debit, &item.Credit, &item.ExchangeRate, &item.BaseCurrency, &item.BaseAmount, &item.DocumentDate, &item.PostedAt, &item.ReversalOfID, &item.ActorUserID, &snapshot, &item.RunningBalance); err != nil {
			return StatementReport{}, err
		}
		hydrateLedgerMetadata(&item, snapshot)
		report.Items = append(report.Items, item)
	}
	if err = rows.Err(); err != nil {
		return StatementReport{}, err
	}
	if len(report.Items) > limit {
		last := report.Items[limit-1]
		report.Items = report.Items[:limit]
		report.NextCursor = encodeLedgerCursor(last.DocumentDate, last.PostedAt, last.ID)
	}
	return report, nil
}

func (s *Service) ensurePartyCompany(ctx context.Context, companyID, partyID string) error {
	return ensurePartyCompanyQ(ctx, s.pool, companyID, partyID)
}

func ensurePartyCompanyQ(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, companyID, partyID string) error {
	var exists bool
	if err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM parties WHERE company_id=$1 AND id=$2)`, companyID, partyID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return identity.ErrForbidden
	}
	return nil
}

// ensurePartyPostingDate mirrors the finance date/period invariants for
// direct cari movement posts. Without this guard a caller could bypass the
// invoice/payment paths and backdate into a locked or future period.
func ensurePartyPostingDate(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, companyID string, date time.Time) error {
	var timezone string
	if err := q.QueryRow(ctx, `SELECT timezone FROM companies WHERE id=$1 FOR SHARE`, companyID).Scan(&timezone); err != nil {
		return err
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return err
	}
	if date.Format("2006-01-02") > time.Now().In(location).Format("2006-01-02") {
		return fmt.Errorf("%w: gelecek tarihli cari hareket kaydedilemez", identity.ErrValidation)
	}
	var locked bool
	if err = q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM finance_period_locks WHERE company_id=$1 AND $2::date BETWEEN period_start AND period_end)`, companyID, date.Format("2006-01-02")).Scan(&locked); err != nil {
		return err
	}
	if locked {
		return fmt.Errorf("%w: işlem tarihi kilitli döneme ait", identity.ErrValidation)
	}
	return nil
}

// PostLedgerEntry is used by posting workflows. The ledger, audit and outbox writes share one transaction.
func (s *Service) PostLedgerEntry(ctx context.Context, session identity.Session, input LedgerEntry, meta identity.RequestMeta) (LedgerEntry, error) {
	if !authorized(session, "party.ledger.post") {
		return LedgerEntry{}, identity.ErrForbidden
	}
	input.PartyID = strings.TrimSpace(input.PartyID)
	input.EntryType = strings.ToUpper(strings.TrimSpace(input.EntryType))
	input.SourceType = strings.TrimSpace(input.SourceType)
	input.Description = strings.TrimSpace(input.Description)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	partyUUID, partyErr := uuid.Parse(input.PartyID)
	sourceUUID, sourceErr := uuid.Parse(strings.TrimSpace(input.SourceID))
	if partyErr != nil || sourceErr != nil || input.EntryType == "" || input.SourceType == "" || input.Description == "" {
		return LedgerEntry{}, fmt.Errorf("%w: cari hareket kaynak ve açıklama alanları geçersiz", identity.ErrValidation)
	}
	input.PartyID = partyUUID.String()
	input.SourceID = sourceUUID.String()
	if input.ReversalOfID != nil {
		if input.EntryType != "REVERSAL" {
			return LedgerEntry{}, fmt.Errorf("%w: ters kayıt hareket türü REVERSAL olmalıdır", identity.ErrValidation)
		}
		reversalUUID, reversalErr := uuid.Parse(strings.TrimSpace(*input.ReversalOfID))
		if reversalErr != nil {
			return LedgerEntry{}, fmt.Errorf("%w: ters kayıt kimliği geçersiz", identity.ErrValidation)
		}
		reversalID := reversalUUID.String()
		input.ReversalOfID = &reversalID
	}
	debit, debitErr := money.ParseDecimal(input.Debit, 4)
	credit, creditErr := money.ParseDecimal(input.Credit, 4)
	rateProvided := strings.TrimSpace(input.ExchangeRate) != ""
	var rate money.Decimal
	var rateErr error
	if rateProvided {
		rate, rateErr = money.ParseDecimal(input.ExchangeRate, 10)
	} else {
		rate, rateErr = money.ParseDecimal("1", 10)
	}
	if debitErr != nil || creditErr != nil || rateErr != nil || rate.Sign() <= 0 || (debit.Sign() > 0) == (credit.Sign() > 0) || debit.Sign() < 0 || credit.Sign() < 0 || strings.TrimSpace(input.IdempotencyKey) == "" || input.DocumentDate.IsZero() {
		return LedgerEntry{}, fmt.Errorf("%w: geçerli borç/alacak, kur, tarih ve idempotency anahtarı gereklidir", identity.ErrValidation)
	}
	debitText, creditText, rateText := canonicalDecimal(debit, 4), canonicalDecimal(credit, 4), canonicalDecimal(rate, 10)
	input.ReferenceNo = strings.TrimSpace(input.ReferenceNo)
	if input.DueDate != nil && input.DueDate.Format("2006-01-02") < input.DocumentDate.Format("2006-01-02") {
		return LedgerEntry{}, fmt.Errorf("%w: vade tarihi işlem tarihinden önce olamaz", identity.ErrValidation)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return LedgerEntry{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if existing, found, findErr := findLedgerByKey(ctx, tx, session.CurrentCompanyID, input.IdempotencyKey); findErr != nil {
		return LedgerEntry{}, findErr
	} else if found {
		currency := strings.ToUpper(strings.TrimSpace(input.Currency))
		if !rateProvided {
			// A foreign-currency replay must carry the same explicit rate as the
			// original command. Without the company-base lookup, a perfectly
			// valid foreign row whose rate happens to be 1 could be replayed as
			// though the omitted rate were a server default.
			var companyBaseCurrency string
			if err = tx.QueryRow(ctx, `SELECT base_currency FROM companies WHERE id=$1`, session.CurrentCompanyID).Scan(&companyBaseCurrency); err != nil {
				return LedgerEntry{}, err
			}
			if existing.Currency != companyBaseCurrency && input.ReversalOfID == nil {
				return LedgerEntry{}, fmt.Errorf("%w: yabancı para hareketinin tekrarı için kur gereklidir", identity.ErrValidation)
			}
		}
		// A reversal may omit its rate (the source rate is copied), but an
		// explicitly supplied rate is still part of the idempotent payload and
		// must match the persisted source snapshot.
		rateMatches := !rateProvided || existing.ExchangeRate == rateText
		if existing.PartyID != input.PartyID || (currency != "" && existing.Currency != currency) ||
			existing.EntryType != input.EntryType || existing.SourceType != input.SourceType || existing.SourceID != input.SourceID ||
			existing.Description != input.Description || existing.Debit != debitText || existing.Credit != creditText ||
			!rateMatches || existing.ReferenceNo != input.ReferenceNo || !sameDueDate(existing.DueDate, input.DueDate) ||
			existing.DocumentDate.Format("2006-01-02") != input.DocumentDate.Format("2006-01-02") || !sameLedgerLink(existing.ReversalOfID, input.ReversalOfID) {
			return LedgerEntry{}, fmt.Errorf("%w: aynı idempotency anahtarı farklı cari hareket verisiyle kullanıldı", identity.ErrConflict)
		}
		return existing, nil
	}
	var partyCurrency, baseCurrency string
	if err = tx.QueryRow(ctx, `SELECT p.default_currency,c.base_currency FROM parties p JOIN companies c ON c.id=p.company_id WHERE p.company_id=$1 AND p.id=$2 AND p.is_active FOR SHARE`, session.CurrentCompanyID, input.PartyID).Scan(&partyCurrency, &baseCurrency); err != nil {
		return LedgerEntry{}, identity.ErrForbidden
	}
	if err = ensurePartyPostingDate(ctx, tx, session.CurrentCompanyID, input.DocumentDate); err != nil {
		return LedgerEntry{}, err
	}
	input.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))
	if input.Currency == "" {
		input.Currency = partyCurrency
	}
	if len(input.Currency) != 3 || strings.Trim(input.Currency, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "" {
		return LedgerEntry{}, fmt.Errorf("%w: işlem para birimi geçersiz", identity.ErrValidation)
	}
	if input.Currency == baseCurrency {
		if input.ReversalOfID == nil {
			one, _ := money.ParseDecimal("1", 10)
			if !rateProvided {
				rate = one
				rateText = canonicalDecimal(rate, 10)
			}
			if !rate.Equal(one) {
				return LedgerEntry{}, fmt.Errorf("%w: temel para biriminde kur 1 olmalıdır", identity.ErrValidation)
			}
		}
	} else if !rateProvided && input.ReversalOfID == nil {
		return LedgerEntry{}, fmt.Errorf("%w: yabancı para hareketinde kur gereklidir", identity.ErrValidation)
	}
	if input.ReversalOfID != nil {
		var originalDebit, originalCredit, originalCurrency, originalRate string
		var originalDate time.Time
		var originalReversalID *string
		err = tx.QueryRow(ctx, `SELECT debit::text,credit::text,currency,exchange_rate::text,reversal_of_id,document_date FROM party_ledger_entries WHERE company_id=$1 AND id=$2 AND party_id=$3 FOR SHARE`, session.CurrentCompanyID, *input.ReversalOfID, input.PartyID).Scan(&originalDebit, &originalCredit, &originalCurrency, &originalRate, &originalReversalID, &originalDate)
		originalDebitValue, originalDebitErr := money.ParseDecimal(originalDebit, 4)
		originalCreditValue, originalCreditErr := money.ParseDecimal(originalCredit, 4)
		if err != nil || originalDebitErr != nil || originalCreditErr != nil || originalReversalID != nil || !debit.Equal(originalCreditValue) || !credit.Equal(originalDebitValue) || input.Currency != originalCurrency {
			return LedgerEntry{}, fmt.Errorf("%w: ters kayıt özgün hareketin tam karşılığı olmalıdır", identity.ErrValidation)
		}
		if input.DocumentDate.Format("2006-01-02") < originalDate.Format("2006-01-02") {
			return LedgerEntry{}, fmt.Errorf("%w: ters kayıt tarihi özgün hareketten önce olamaz", identity.ErrValidation)
		}
		// A reversal must mirror the source entry exactly, including its rate, so
		// the base-currency figures net to zero. The caller's rate is ignored.
		if originalRateValue, parseErr := money.ParseDecimal(originalRate, 10); parseErr == nil && originalRateValue.Sign() > 0 {
			if rateProvided && !rate.Equal(originalRateValue) {
				return LedgerEntry{}, fmt.Errorf("%w: ters kayıt kuru özgün hareketle eşleşmiyor", identity.ErrValidation)
			}
			rate = originalRateValue
			rateText = canonicalDecimal(rate, 10)
		}
	}
	if baseAmountRoundsToZero(debitText, creditText, rateText) {
		return LedgerEntry{}, fmt.Errorf("%w: temel para birimine çevrilen tutar dört ondalıkta sıfıra yuvarlanamaz", identity.ErrValidation)
	}
	input.ID = uuid.NewString()
	input.PostedAt = time.Now()
	if input.Snapshot == nil {
		input.Snapshot = map[string]any{}
	}
	// Base-currency identity and amount are database-owned immutable
	// snapshots. Never let an API caller smuggle a different currency through
	// the free-form JSON payload (the migration trigger also reasserts this for
	// direct SQL writers).
	delete(input.Snapshot, "base_currency")
	delete(input.Snapshot, "base_amount")
	if input.ReferenceNo != "" {
		input.Snapshot["reference_no"] = input.ReferenceNo
	}
	if input.DueDate != nil {
		input.Snapshot["due_date"] = input.DueDate.Format("2006-01-02")
	}
	snapshot, _ := json.Marshal(input.Snapshot)
	_, err = tx.Exec(ctx, `INSERT INTO party_ledger_entries(id,company_id,party_id,currency,entry_type,source_type,source_id,idempotency_key,description,debit,credit,exchange_rate,document_date,reversal_of_id,actor_user_id,snapshot) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`, input.ID, session.CurrentCompanyID, input.PartyID, input.Currency, input.EntryType, input.SourceType, input.SourceID, input.IdempotencyKey, input.Description, debitText, creditText, rateText, input.DocumentDate, input.ReversalOfID, session.User.ID, snapshot)
	if err != nil {
		return LedgerEntry{}, mapConstraint(err)
	}
	if err = writeAuditAndEvent(ctx, tx, session, "PARTY_LEDGER_POSTED", "party.ledger.posted", "party_ledger_entry", input.ID, map[string]any{"ledger_entry_id": input.ID, "party_id": input.PartyID}, meta); err != nil {
		return LedgerEntry{}, err
	}
	var storedBaseAmount string
	if err = tx.QueryRow(ctx, `SELECT base_amount::text FROM party_ledger_entries WHERE company_id=$1 AND id=$2`, session.CurrentCompanyID, input.ID).Scan(&storedBaseAmount); err != nil {
		return LedgerEntry{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return LedgerEntry{}, err
	}
	input.Debit, input.Credit, input.ExchangeRate = debitText, creditText, rateText
	input.BaseCurrency = &baseCurrency
	input.BaseAmount = &storedBaseAmount
	return input, nil
}

const partySelect = `SELECT p.id,p.code,p.kind,p.is_customer,p.is_supplier,p.display_name,p.legal_name,p.trade_name,p.first_name,p.last_name,p.tax_number,p.identity_number,p.tax_office,p.tax_office_id::text,p.default_currency,p.payment_term_id,p.price_list_id,p.default_discount_rate::text,p.sales_rep_user_id,p.credit_limit::text,p.risk_limit::text,p.risk_policy,
	COALESCE((SELECT NULLIF(c.phone,'') FROM party_contacts c WHERE c.company_id=p.company_id AND c.party_id=p.id ORDER BY c.is_primary DESC,c.created_at,c.id LIMIT 1),''),
	COALESCE((SELECT NULLIF(c.email,'') FROM party_contacts c WHERE c.company_id=p.company_id AND c.party_id=p.id ORDER BY c.is_primary DESC,c.created_at,c.id LIMIT 1),''),
	COALESCE((SELECT COALESCE(tp.name,a.city) FROM party_addresses a LEFT JOIN turkish_provinces tp ON tp.id=a.province_id WHERE a.company_id=p.company_id AND a.party_id=p.id ORDER BY a.is_default DESC,a.created_at,a.id LIMIT 1),''),
		COALESCE((SELECT concat_ws(' · ',NULLIF(a.address_line,''),NULLIF(COALESCE(tp.name,a.city),''),NULLIF(COALESCE(td.name,a.district),''),NULLIF(COALESCE(tn.name,a.neighborhood),'')) FROM party_addresses a LEFT JOIN turkish_provinces tp ON tp.id=a.province_id LEFT JOIN turkish_districts td ON td.province_id=a.province_id AND td.id=a.district_id LEFT JOIN turkish_neighborhoods tn ON tn.district_id=a.district_id AND tn.id=a.neighborhood_id WHERE a.company_id=p.company_id AND a.party_id=p.id ORDER BY a.is_default DESC,a.created_at,a.id LIMIT 1),''),
	COALESCE((SELECT string_agg(concat_ws(' · ',NULLIF(c.full_name,''),NULLIF(c.title,''),NULLIF(c.department,''),NULLIF(c.email,''),NULLIF(c.phone,'')),' | ' ORDER BY c.is_primary DESC,c.created_at,c.id) FROM party_contacts c WHERE c.company_id=p.company_id AND c.party_id=p.id),''),
	COALESCE((SELECT string_agg(pg.name,', ' ORDER BY pg.name) FROM party_group_memberships pgm JOIN party_groups pg ON pg.company_id=pgm.company_id AND pg.id=pgm.group_id WHERE pgm.company_id=p.company_id AND pgm.party_id=p.id),''),
	COALESCE((SELECT string_agg(pt.name,', ' ORDER BY pt.name) FROM party_tag_assignments pta JOIN party_tags pt ON pt.company_id=pta.company_id AND pt.id=pta.tag_id WHERE pta.company_id=p.company_id AND pta.party_id=p.id),''),
	COALESCE((SELECT string_agg(concat_ws(': ',d.name,CASE d.field_type WHEN 'TEXT' THEN v.text_value WHEN 'NUMBER' THEN v.number_value::text WHEN 'DATE' THEN v.date_value::text WHEN 'BOOLEAN' THEN v.boolean_value::text WHEN 'SELECT' THEN v.select_value END),', ' ORDER BY d.code) FROM party_custom_field_values v JOIN party_custom_field_definitions d ON d.company_id=v.company_id AND d.id=v.definition_id WHERE v.company_id=p.company_id AND v.party_id=p.id),''),
	COALESCE((SELECT concat_ws(' · ',pt.code,pt.name,pt.due_days::text || ' gün') FROM payment_terms pt WHERE pt.company_id=p.company_id AND pt.id=p.payment_term_id),''),
	COALESCE((SELECT u.display_name FROM users u WHERE u.id=p.sales_rep_user_id),''),
	COALESCE((SELECT sum(l.base_signed_amount)::text FROM party_ledger_balance_effects l WHERE l.company_id=p.company_id AND l.party_id=p.id),'0'),
	COALESCE((SELECT c.base_currency FROM companies c WHERE c.id=p.company_id),''),
	p.is_active,p.created_at,p.updated_at,p.version FROM parties p`

type scanner interface{ Scan(...any) error }

func scanParty(row scanner) (Party, error) {
	var item Party
	err := row.Scan(&item.ID, &item.Code, &item.Kind, &item.IsCustomer, &item.IsSupplier, &item.DisplayName, &item.LegalName, &item.TradeName, &item.FirstName, &item.LastName, &item.TaxNumber, &item.IdentityNumber, &item.TaxOffice, &item.TaxOfficeID, &item.DefaultCurrency, &item.PaymentTermID, &item.PriceListID, &item.DefaultDiscountRate, &item.SalesRepUserID, &item.CreditLimit, &item.RiskLimit, &item.RiskPolicy, &item.Phone, &item.Email, &item.City, &item.AddressSummary, &item.ContactSummary, &item.GroupSummary, &item.TagSummary, &item.CustomFieldSummary, &item.PaymentTermName, &item.SalesRepName, &item.Balance, &item.BalanceCurrency, &item.IsActive, &item.CreatedAt, &item.UpdatedAt, &item.Version)
	item.Addresses, item.Contacts, item.Groups, item.Tags = []Address{}, []Contact{}, []string{}, []string{}
	item.CustomFields = map[string]any{}
	return item, err
}

func normalizeAndValidate(input *Input) error {
	input.Code = strings.ToUpper(strings.TrimSpace(input.Code))
	input.Kind = strings.ToUpper(strings.TrimSpace(input.Kind))
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.LegalName, input.TradeName = strings.TrimSpace(input.LegalName), strings.TrimSpace(input.TradeName)
	input.FirstName, input.LastName = strings.TrimSpace(input.FirstName), strings.TrimSpace(input.LastName)
	// DisplayName is the list/search identity, not a third name field. Keep it
	// derived from the fields users actually maintain in the cari card so an API
	// client cannot make the table show an unrelated name.
	switch input.Kind {
	case "ORGANIZATION":
		if input.TradeName != "" {
			input.DisplayName = input.TradeName
		} else if input.LegalName != "" {
			input.DisplayName = input.LegalName
		}
	case "PERSON":
		if name := strings.TrimSpace(strings.Join([]string{input.FirstName, input.LastName}, " ")); name != "" {
			input.DisplayName = name
		}
	}
	input.TaxNumber, input.IdentityNumber, input.TaxOffice, input.TaxOfficeID = strings.TrimSpace(input.TaxNumber), strings.TrimSpace(input.IdentityNumber), strings.TrimSpace(input.TaxOffice), strings.TrimSpace(input.TaxOfficeID)
	input.DefaultCurrency = strings.ToUpper(strings.TrimSpace(input.DefaultCurrency))
	input.RiskPolicy = strings.ToUpper(strings.TrimSpace(input.RiskPolicy))
	if input.RiskPolicy == "" {
		input.RiskPolicy = "WARN"
	}
	if input.DefaultDiscountRate == "" {
		input.DefaultDiscountRate = "0"
	}
	if input.CreditLimit == "" {
		input.CreditLimit = "0"
	}
	if input.RiskLimit == "" {
		input.RiskLimit = "0"
	}
	discount, discountErr := money.ParseDecimal(input.DefaultDiscountRate, 6)
	credit, creditErr := money.ParseDecimal(input.CreditLimit, 4)
	risk, riskErr := money.ParseDecimal(input.RiskLimit, 4)
	if input.DisplayName == "" || (!input.IsCustomer && !input.IsSupplier) || len(input.DefaultCurrency) != 3 || discountErr != nil || creditErr != nil || riskErr != nil || discount.Sign() < 0 || credit.Sign() < 0 || risk.Sign() < 0 {
		return fmt.Errorf("%w: ad, rol, para birimi ve negatif olmayan limitler gereklidir", identity.ErrValidation)
	}
	if input.Kind != "PERSON" && input.Kind != "ORGANIZATION" {
		return fmt.Errorf("%w: cari türü kişi veya kurum olmalıdır", identity.ErrValidation)
	}
	if input.Kind == "PERSON" && (input.FirstName == "" || input.LastName == "") {
		return fmt.Errorf("%w: kişi carisi için ad ve soyad gereklidir", identity.ErrValidation)
	}
	if input.Kind == "ORGANIZATION" && input.LegalName == "" {
		return fmt.Errorf("%w: kurum carisi için resmî unvan gereklidir", identity.ErrValidation)
	}
	if input.RiskPolicy != "ALLOW" && input.RiskPolicy != "WARN" && input.RiskPolicy != "BLOCK" {
		return fmt.Errorf("%w: geçersiz risk politikası", identity.ErrValidation)
	}
	if len(input.Addresses) > 1 {
		return fmt.Errorf("%w: cari kartında yalnızca bir adres tutulabilir", identity.ErrValidation)
	}
	input.DefaultDiscountRate, input.CreditLimit, input.RiskLimit = discount.String(), credit.String(), risk.String()
	return nil
}

func normalizePartySearchQuery(value string) string {
	const maxSearchTokens = 16
	const maxSearchTokenLength = 64
	value = strings.NewReplacer(
		"ı", "i", "ğ", "g", "ü", "u", "ş", "s", "ö", "o", "ç", "c",
		"â", "a", "ä", "a", "à", "a", "á", "a", "ã", "a", "å", "a",
		"é", "e", "è", "e", "ë", "e", "ê", "e", "î", "i", "ï", "i", "ì", "i", "í", "i",
		"ñ", "n", "ó", "o", "ò", "o", "ô", "o", "õ", "o", "ú", "u", "ù", "u", "û", "u", "ý", "y", "ÿ", "y",
	).Replace(strings.ToLower(strings.TrimSpace(value)))
	var normalized strings.Builder
	for _, char := range value {
		if unicode.IsLetter(char) || unicode.IsNumber(char) {
			normalized.WriteRune(char)
		} else {
			normalized.WriteByte(' ')
		}
	}
	tokens := strings.Fields(normalized.String())
	if len(tokens) > maxSearchTokens {
		tokens = tokens[:maxSearchTokens]
	}
	for index := range tokens {
		if runes := []rune(tokens[index]); len(runes) > maxSearchTokenLength {
			tokens[index] = string(runes[:maxSearchTokenLength])
		}
		tokens[index] += ":*"
	}
	return strings.Join(tokens, " & ")
}

func authorized(session identity.Session, permission string) bool {
	return identity.ValidateExternalActor(session) == nil && session.HasPermission(permission)
}

// lockPartyCodeSequence serializes manual and automatic code writes for one
// company. The company row lock also keeps a code-prefix setting change from
// racing with an automatic code generated in the same company.
func lockPartyCodeSequence(ctx context.Context, tx pgx.Tx, companyID string) error {
	var lockedCompanyID string
	if err := tx.QueryRow(ctx, `SELECT id::text FROM companies WHERE id=$1 FOR UPDATE`, companyID).Scan(&lockedCompanyID); err != nil {
		return err
	}
	var nextNumber int64
	return tx.QueryRow(ctx, `INSERT INTO company_party_sequences(company_id,next_number) VALUES($1,1) ON CONFLICT(company_id) DO UPDATE SET next_number=company_party_sequences.next_number RETURNING next_number`, companyID).Scan(&nextNumber)
}

func nextCode(ctx context.Context, tx pgx.Tx, companyID string) (string, error) {
	var prefix string
	var digits int
	if err := tx.QueryRow(ctx, `SELECT party_code_prefix,party_code_digits FROM companies WHERE id=$1`, companyID).Scan(&prefix, &digits); err != nil {
		return "", err
	}
	for {
		var number int64
		if err := tx.QueryRow(ctx, `INSERT INTO company_party_sequences(company_id,next_number) VALUES($1,2) ON CONFLICT(company_id) DO UPDATE SET next_number=company_party_sequences.next_number+1 RETURNING next_number-1`, companyID).Scan(&number); err != nil {
			return "", err
		}
		code := fmt.Sprintf("%s%0*d", prefix, digits, number)
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM parties WHERE company_id=$1 AND lower(code)=lower($2))`, companyID, code).Scan(&exists); err != nil {
			return "", err
		}
		if !exists {
			return code, nil
		}
	}
}

func duplicateTaxWarnings(ctx context.Context, tx pgx.Tx, companyID, excludeID, taxNumber, identityNumber string) ([]string, error) {
	if taxNumber == "" && identityNumber == "" {
		return nil, nil
	}
	var policy string
	var duplicate bool
	if err := tx.QueryRow(ctx, `SELECT duplicate_party_tax_number_policy FROM companies WHERE id=$1`, companyID).Scan(&policy); err != nil {
		return nil, err
	}
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM parties WHERE company_id=$1 AND id<>COALESCE(NULLIF($2,''),'00000000-0000-0000-0000-000000000000')::uuid AND (($3<>'' AND tax_number=$3) OR ($4<>'' AND identity_number=$4)))`, companyID, excludeID, taxNumber, identityNumber).Scan(&duplicate); err != nil {
		return nil, err
	}
	if !duplicate || policy == "ALLOW" {
		return nil, nil
	}
	if policy == "BLOCK" {
		return nil, fmt.Errorf("%w: vergi veya T.C. kimlik numarası başka bir caride kayıtlı", identity.ErrValidation)
	}
	return []string{"Vergi veya T.C. kimlik numarası başka bir caride de kullanılıyor."}, nil
}

func replaceDetails(ctx context.Context, tx pgx.Tx, companyID, partyID string, input Input) error {
	providedCodes := make([]string, 0, len(input.CustomFields))
	for code := range input.CustomFields {
		providedCodes = append(providedCodes, code)
	}
	var missingRequired bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM party_custom_field_definitions WHERE company_id=$1 AND is_active AND is_required AND NOT (code=ANY($2)))`, companyID, providedCodes).Scan(&missingRequired); err != nil {
		return err
	}
	if missingRequired {
		return fmt.Errorf("%w: zorunlu cari özel alanları doldurulmalıdır", identity.ErrValidation)
	}
	for _, table := range []string{"party_addresses", "party_contacts", "party_group_memberships", "party_tag_assignments", "party_custom_field_values"} {
		if _, err := tx.Exec(ctx, `DELETE FROM `+table+` WHERE company_id=$1 AND party_id=$2`, companyID, partyID); err != nil {
			return err
		}
	}
	for _, address := range input.Addresses {
		if !addressHasAnyValue(address) {
			continue
		}
		if address.ID == "" {
			address.ID = uuid.NewString()
		}
		if err := resolveAddressReference(ctx, tx, &address); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO party_addresses(id,company_id,party_id,address_line,district,city,neighborhood,is_default,province_id,district_id,neighborhood_id) VALUES($1,$2,$3,NULLIF($4,''),$5,$6,$7,$8,$9,$10,$11)`, address.ID, companyID, partyID, strings.TrimSpace(address.AddressLine), nullString(address.District), strings.TrimSpace(address.City), nullString(address.Neighborhood), address.IsDefault, address.ProvinceID, address.DistrictID, address.NeighborhoodID); err != nil {
			return err
		}
	}
	for _, contact := range input.Contacts {
		if contact.ID == "" {
			contact.ID = uuid.NewString()
		}
		if _, err := tx.Exec(ctx, `INSERT INTO party_contacts(id,company_id,party_id,full_name,title,department,email,phone,is_primary) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, contact.ID, companyID, partyID, strings.TrimSpace(contact.FullName), nullString(contact.Title), nullString(contact.Department), nullString(contact.Email), nullString(contact.Phone), contact.IsPrimary); err != nil {
			return err
		}
	}
	for _, groupID := range unique(input.GroupIDs) {
		if _, err := tx.Exec(ctx, `INSERT INTO party_group_memberships(company_id,party_id,group_id) VALUES($1,$2,$3)`, companyID, partyID, groupID); err != nil {
			return err
		}
	}
	for _, name := range unique(input.Tags) {
		var tagID string
		if err := tx.QueryRow(ctx, `INSERT INTO party_tags(id,company_id,name) VALUES($1,$2,$3) ON CONFLICT(company_id,name) DO UPDATE SET name=excluded.name RETURNING id`, uuid.NewString(), companyID, name).Scan(&tagID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO party_tag_assignments(company_id,party_id,tag_id) VALUES($1,$2,$3)`, companyID, partyID, tagID); err != nil {
			return err
		}
	}
	for code, raw := range input.CustomFields {
		var definitionID, fieldType string
		var options []byte
		if err := tx.QueryRow(ctx, `SELECT id,field_type,select_options FROM party_custom_field_definitions WHERE company_id=$1 AND code=$2 AND is_active`, companyID, code).Scan(&definitionID, &fieldType, &options); err != nil {
			return fmt.Errorf("%w: bilinmeyen özel alan %s", identity.ErrValidation, code)
		}
		var textValue, numberValue, dateValue, selectValue any
		var booleanValue any
		switch fieldType {
		case "TEXT":
			var value string
			if json.Unmarshal(raw, &value) != nil {
				return fmt.Errorf("%w: %s metin olmalıdır", identity.ErrValidation, code)
			}
			textValue = value
		case "NUMBER":
			var value string
			if json.Unmarshal(raw, &value) != nil {
				return fmt.Errorf("%w: %s ondalık değer metni olmalıdır", identity.ErrValidation, code)
			}
			decimal, parseErr := money.ParseDecimal(value, 10)
			if parseErr != nil {
				return fmt.Errorf("%w: %s geçerli ondalık olmalıdır", identity.ErrValidation, code)
			}
			numberValue = decimal.String()
		case "DATE":
			var value string
			if json.Unmarshal(raw, &value) != nil {
				return fmt.Errorf("%w: %s tarih olmalıdır", identity.ErrValidation, code)
			}
			parsed, parseErr := time.Parse("2006-01-02", value)
			if parseErr != nil {
				return fmt.Errorf("%w: %s tarih olmalıdır", identity.ErrValidation, code)
			}
			dateValue = parsed
		case "BOOLEAN":
			var value bool
			if json.Unmarshal(raw, &value) != nil {
				return fmt.Errorf("%w: %s doğru/yanlış olmalıdır", identity.ErrValidation, code)
			}
			booleanValue = value
		case "SELECT":
			var value string
			if json.Unmarshal(raw, &value) != nil {
				return fmt.Errorf("%w: %s seçim olmalıdır", identity.ErrValidation, code)
			}
			var allowed []string
			_ = json.Unmarshal(options, &allowed)
			found := false
			for _, option := range allowed {
				if value == option {
					found = true
				}
			}
			if !found {
				return fmt.Errorf("%w: %s seçimi tanımlı değil", identity.ErrValidation, code)
			}
			selectValue = value
		}
		if _, err := tx.Exec(ctx, `INSERT INTO party_custom_field_values(company_id,party_id,definition_id,text_value,number_value,date_value,boolean_value,select_value)VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, companyID, partyID, definitionID, textValue, numberValue, dateValue, booleanValue, selectValue); err != nil {
			return err
		}
	}
	_, err := tx.Exec(ctx, `SELECT refresh_party_search_document($1,$2)`, companyID, partyID)
	return err
}

func resolveAddressReference(ctx context.Context, tx pgx.Tx, address *Address) error {
	address.AddressLine = strings.TrimSpace(address.AddressLine)
	address.City = strings.TrimSpace(address.City)
	address.District = strings.TrimSpace(address.District)
	address.Neighborhood = strings.TrimSpace(address.Neighborhood)
	address.ProvinceName = strings.TrimSpace(address.ProvinceName)
	address.DistrictName = strings.TrimSpace(address.DistrictName)
	address.NeighborhoodName = strings.TrimSpace(address.NeighborhoodName)
	if !addressHasAnyValue(*address) {
		return nil
	}
	if address.ProvinceID == nil {
		return fmt.Errorf("%w: adres bilgisi gönderildiğinde il seçimi gereklidir", identity.ErrValidation)
	}
	if address.DistrictID == nil && address.NeighborhoodID != nil {
		return fmt.Errorf("%w: mahalle için ilçe seçilmelidir", identity.ErrValidation)
	}
	if address.ProvinceID != nil {
		provinceName, districtName, neighborhoodName, err := resolveLocationSelection(ctx, tx, address.ProvinceID, address.DistrictID, address.NeighborhoodID)
		if err != nil {
			return err
		}
		address.City, address.ProvinceName = provinceName, provinceName
		if address.DistrictID != nil {
			address.District, address.DistrictName = districtName, districtName
		}
		if address.NeighborhoodID != nil {
			address.Neighborhood, address.NeighborhoodName = neighborhoodName, neighborhoodName
		}
	}
	return nil
}

func addressHasAnyValue(address Address) bool {
	return strings.TrimSpace(address.AddressLine) != "" ||
		strings.TrimSpace(address.City) != "" ||
		strings.TrimSpace(address.ProvinceName) != "" ||
		strings.TrimSpace(address.District) != "" ||
		strings.TrimSpace(address.DistrictName) != "" ||
		strings.TrimSpace(address.Neighborhood) != "" ||
		strings.TrimSpace(address.NeighborhoodName) != "" ||
		address.ProvinceID != nil || address.DistrictID != nil || address.NeighborhoodID != nil
}

func resolveLocationSelection(ctx context.Context, tx pgx.Tx, provinceID, districtID, neighborhoodID *int64) (string, string, string, error) {
	if provinceID == nil && (districtID != nil || neighborhoodID != nil) {
		return "", "", "", fmt.Errorf("%w: ilçe ve mahalle için il seçilmelidir", identity.ErrValidation)
	}
	if districtID == nil && neighborhoodID != nil {
		return "", "", "", fmt.Errorf("%w: mahalle için ilçe seçilmelidir", identity.ErrValidation)
	}
	if provinceID == nil {
		return "", "", "", nil
	}
	districtValue, neighborhoodValue := int64(0), int64(0)
	if districtID != nil {
		districtValue = *districtID
	}
	if neighborhoodID != nil {
		neighborhoodValue = *neighborhoodID
	}
	var provinceName, districtName, neighborhoodName string
	var districtFound, neighborhoodFound bool
	err := tx.QueryRow(ctx, `SELECT p.name,d.id IS NOT NULL,COALESCE(d.name,''),n.id IS NOT NULL,COALESCE(n.name,'')
		FROM turkish_provinces p
		LEFT JOIN turkish_districts d ON d.province_id=p.id AND d.id=NULLIF($2::bigint,0)
		LEFT JOIN turkish_neighborhoods n ON n.district_id=d.id AND n.id=NULLIF($3::bigint,0)
		WHERE p.id=$1`, *provinceID, districtValue, neighborhoodValue).Scan(&provinceName, &districtFound, &districtName, &neighborhoodFound, &neighborhoodName)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", fmt.Errorf("%w: il referans kaydı bulunamadı", identity.ErrValidation)
	}
	if err != nil {
		return "", "", "", err
	}
	if districtID != nil && !districtFound {
		return "", "", "", fmt.Errorf("%w: seçilen ilçe seçilen ile ait değil", identity.ErrValidation)
	}
	if neighborhoodID != nil && !neighborhoodFound {
		return "", "", "", fmt.Errorf("%w: seçilen mahalle seçilen ilçeye ait değil", identity.ErrValidation)
	}
	return provinceName, districtName, neighborhoodName, nil
}

func (s *Service) loadDetails(ctx context.Context, companyID string, item *Party) error {
	addresses, err := s.pool.Query(ctx, `SELECT a.id,COALESCE(a.address_line,''),COALESCE(a.province_id,0),COALESCE(tp.name,a.city),COALESCE(a.district_id,0),COALESCE(td.name,a.district,''),COALESCE(a.neighborhood_id,0),COALESCE(tn.name,a.neighborhood,''),a.is_default
		FROM party_addresses a
		LEFT JOIN turkish_provinces tp ON tp.id=a.province_id
		LEFT JOIN turkish_districts td ON td.province_id=a.province_id AND td.id=a.district_id
		LEFT JOIN turkish_neighborhoods tn ON tn.district_id=a.district_id AND tn.id=a.neighborhood_id
		WHERE a.company_id=$1 AND a.party_id=$2 ORDER BY a.is_default DESC,a.created_at,a.id LIMIT 1`, companyID, item.ID)
	if err != nil {
		return err
	}
	for addresses.Next() {
		var value Address
		var provinceID, districtID, neighborhoodID int64
		if err = addresses.Scan(&value.ID, &value.AddressLine, &provinceID, &value.City, &districtID, &value.District, &neighborhoodID, &value.Neighborhood, &value.IsDefault); err != nil {
			addresses.Close()
			return err
		}
		if provinceID > 0 {
			value.ProvinceID, value.ProvinceName = &provinceID, value.City
		}
		if districtID > 0 {
			value.DistrictID, value.DistrictName = &districtID, value.District
		}
		if neighborhoodID > 0 {
			value.NeighborhoodID, value.NeighborhoodName = &neighborhoodID, value.Neighborhood
		}
		item.Addresses = append(item.Addresses, value)
	}
	err = addresses.Err()
	addresses.Close()
	if err != nil {
		return err
	}
	contacts, err := s.pool.Query(ctx, `SELECT id,full_name,COALESCE(title,''),COALESCE(department,''),COALESCE(email,''),COALESCE(phone,''),is_primary FROM party_contacts WHERE company_id=$1 AND party_id=$2 ORDER BY is_primary DESC,full_name`, companyID, item.ID)
	if err != nil {
		return err
	}
	for contacts.Next() {
		var value Contact
		if err = contacts.Scan(&value.ID, &value.FullName, &value.Title, &value.Department, &value.Email, &value.Phone, &value.IsPrimary); err != nil {
			contacts.Close()
			return err
		}
		item.Contacts = append(item.Contacts, value)
	}
	err = contacts.Err()
	contacts.Close()
	if err != nil {
		return err
	}
	rows, err := s.pool.Query(ctx, `SELECT pgm.group_id::text FROM party_group_memberships pgm WHERE pgm.company_id=$1 AND pgm.party_id=$2 ORDER BY pgm.group_id`, companyID, item.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var v string
		if err = rows.Scan(&v); err != nil {
			rows.Close()
			return err
		}
		item.Groups = append(item.Groups, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	rows, err = s.pool.Query(ctx, `SELECT t.name FROM party_tag_assignments a JOIN party_tags t ON t.company_id=a.company_id AND t.id=a.tag_id WHERE a.company_id=$1 AND a.party_id=$2 ORDER BY t.name`, companyID, item.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var v string
		if err = rows.Scan(&v); err != nil {
			rows.Close()
			return err
		}
		item.Tags = append(item.Tags, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	rows, err = s.pool.Query(ctx, `SELECT d.code,d.field_type,v.text_value,v.number_value::text,v.date_value,v.boolean_value,v.select_value FROM party_custom_field_values v JOIN party_custom_field_definitions d ON d.company_id=v.company_id AND d.id=v.definition_id WHERE v.company_id=$1 AND v.party_id=$2 ORDER BY d.code`, companyID, item.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var code, fieldType string
		var textValue, numberValue, dateValue, selectValue *string
		var booleanValue *bool
		if err = rows.Scan(&code, &fieldType, &textValue, &numberValue, &dateValue, &booleanValue, &selectValue); err != nil {
			rows.Close()
			return err
		}
		switch fieldType {
		case "TEXT":
			if textValue != nil {
				item.CustomFields[code] = *textValue
			}
		case "NUMBER":
			if numberValue != nil {
				item.CustomFields[code] = *numberValue
			}
		case "DATE":
			if dateValue != nil {
				item.CustomFields[code] = *dateValue
			}
		case "BOOLEAN":
			if booleanValue != nil {
				item.CustomFields[code] = *booleanValue
			}
		case "SELECT":
			if selectValue != nil {
				item.CustomFields[code] = *selectValue
			}
		}
	}
	err = rows.Err()
	rows.Close()
	return err
}

func suppressPartySearch(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `SELECT set_config('varyaone.party_search_suppressed','on',true)`)
	return err
}

func writeAuditAndEvent(ctx context.Context, tx pgx.Tx, session identity.Session, eventType, outboxType, entityType, entityID string, payloadValue map[string]any, meta identity.RequestMeta) error {
	details, _ := json.Marshal(map[string]any{"version": 1})
	if _, err := tx.Exec(ctx, `INSERT INTO security_audit_events(id,company_id,actor_user_id,event_type,entity_type,entity_id,details,trace_id,source_ip,user_agent) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, uuid.NewString(), session.CurrentCompanyID, session.User.ID, eventType, entityType, entityID, details, meta.TraceID, meta.IP, truncate(meta.UserAgent, 512)); err != nil {
		return err
	}
	payload, _ := json.Marshal(payloadValue)
	_, err := tx.Exec(ctx, `INSERT INTO outbox_events(event_id,type,schema_version,company_id,trace_id,payload) VALUES($1,$2,1,$3,$4,$5)`, uuid.NewString(), outboxType, session.CurrentCompanyID, meta.TraceID, payload)
	return err
}

func findLedgerByKey(ctx context.Context, tx pgx.Tx, companyID, key string) (LedgerEntry, bool, error) {
	var item LedgerEntry
	var snapshot []byte
	err := tx.QueryRow(ctx, `SELECT id,party_id,currency,entry_type,source_type,source_id,description,debit::text,credit::text,exchange_rate::text,base_currency,base_amount::text,document_date,posted_at,reversal_of_id,snapshot FROM party_ledger_entries WHERE company_id=$1 AND idempotency_key=$2`, companyID, key).Scan(&item.ID, &item.PartyID, &item.Currency, &item.EntryType, &item.SourceType, &item.SourceID, &item.Description, &item.Debit, &item.Credit, &item.ExchangeRate, &item.BaseCurrency, &item.BaseAmount, &item.DocumentDate, &item.PostedAt, &item.ReversalOfID, &snapshot)
	if errors.Is(err, pgx.ErrNoRows) {
		return LedgerEntry{}, false, nil
	}
	if err != nil {
		return LedgerEntry{}, false, err
	}
	hydrateLedgerMetadata(&item, snapshot)
	return item, true, nil
}

func encodeCursor(name, id string) string {
	value, _ := json.Marshal([]string{name, id})
	return base64.RawURLEncoding.EncodeToString(value)
}
func decodeCursor(value string) (string, string, error) {
	if value == "" {
		return "", "00000000-0000-0000-0000-000000000000", nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return "", "", err
	}
	var parts []string
	if json.Unmarshal(raw, &parts) != nil || len(parts) != 2 || uuid.Validate(parts[1]) != nil {
		return "", "", errors.New("invalid cursor")
	}
	return parts[0], parts[1], nil
}

// ledgerCursor is deliberately independent from the party-card cursor. The
// ledger ordering has three dimensions and must remain stable across pages.
type ledgerCursor struct {
	Date     string `json:"date"`
	PostedAt string `json:"posted_at"`
	ID       string `json:"id"`
}

func encodeLedgerCursor(date, postedAt time.Time, id string) string {
	value, _ := json.Marshal(ledgerCursor{Date: date.Format("2006-01-02"), PostedAt: postedAt.UTC().Format(time.RFC3339Nano), ID: id})
	return base64.RawURLEncoding.EncodeToString(value)
}

func decodeLedgerCursor(value string) (time.Time, time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return time.Time{}, time.Time{}, "", err
	}
	var cursor ledgerCursor
	if err = json.Unmarshal(raw, &cursor); err != nil || uuid.Validate(cursor.ID) != nil {
		return time.Time{}, time.Time{}, "", errors.New("invalid cursor")
	}
	date, err := time.Parse("2006-01-02", cursor.Date)
	if err != nil {
		return time.Time{}, time.Time{}, "", err
	}
	postedAt, err := time.Parse(time.RFC3339Nano, cursor.PostedAt)
	if err != nil {
		return time.Time{}, time.Time{}, "", err
	}
	return date, postedAt, cursor.ID, nil
}
func unique(values []string) []string {
	seen := map[string]struct{}{}
	result := []string{}
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; !ok {
			seen[v] = struct{}{}
			result = append(result, v)
		}
	}
	return result
}
func nullString(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return value
}
func truncate(value string, length int) string {
	if len(value) <= length {
		return value
	}
	return value[:length]
}
func mapConstraint(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.ConstraintName {
		case "parties_tax_number_format":
			return fmt.Errorf("%w: vergi numarası 10 haneli olmalıdır", identity.ErrValidation)
		case "parties_identity_number_format":
			return fmt.Errorf("%w: T.C. kimlik numarası 11 haneli olmalıdır", identity.ErrValidation)
		case "parties_kind_fields":
			return fmt.Errorf("%w: cari türüne göre kurum resmî unvanı veya kişi adı ve soyadı gereklidir", identity.ErrValidation)
		case "parties_has_role":
			return fmt.Errorf("%w: en az bir cari rolü seçilmelidir", identity.ErrValidation)
		case "parties_company_code_unique":
			return fmt.Errorf("%w: cari kodu bu firmada zaten kullanılıyor", identity.ErrValidation)
		case "party_groups_company_id_code_key":
			return fmt.Errorf("%w: bu cari grup kodu zaten kullanılıyor", identity.ErrValidation)
		case "party_ledger_entries_company_id_idempotency_key_key":
			return fmt.Errorf("%w: aynı cari hareket anahtarı daha önce kullanılmış", identity.ErrConflict)
		case "party_ledger_entries_company_id_reversal_of_id_key":
			return fmt.Errorf("%w: cari hareket zaten ters kaydedilmiş", identity.ErrConflict)
		case "party_ledger_base_amount_check":
			return fmt.Errorf("%w: temel para birimine çevrilen tutar sıfıra yuvarlanamaz", identity.ErrValidation)
		}
	}
	if strings.Contains(err.Error(), "unique") || strings.Contains(err.Error(), "violates") || strings.Contains(err.Error(), "invalid input value") {
		return fmt.Errorf("%w: cari bilgileri mevcut kayıtlarla veya doğrulama kurallarıyla çakışıyor", identity.ErrValidation)
	}
	return err
}
