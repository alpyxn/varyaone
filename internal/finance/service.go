// Package finance contains the pre-accounting command model.  It deliberately
// writes append-only rows: a correction is another row pointing at the
// original row and never an UPDATE/DELETE of posted history.
package finance

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/money"
	"github.com/alpyxn/varyaone/internal/platform/database"
	"github.com/alpyxn/varyaone/internal/platform/idempotency"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrInsufficientStock                  = errors.New("INSUFFICIENT_STOCK")
	ErrPaymentAlreadyPosted               = errors.New("PAYMENT_ALREADY_POSTED")
	ErrPaymentAllocationExceedsOpenAmount = errors.New("PAYMENT_ALLOCATION_EXCEEDS_OPEN_AMOUNT")
	ErrCurrencyMismatch                   = errors.New("CURRENCY_MISMATCH")
	ErrPeriodLocked                       = errors.New("PERIOD_LOCKED")
	ErrAlreadyReversed                    = errors.New("ALREADY_REVERSED")
	ErrIdempotencyConflict                = errors.New("IDEMPOTENCY_CONFLICT")
	ErrInvalidPaymentState                = errors.New("PAYMENT_INVALID_STATE")
	ErrInvoiceAlreadyPosted               = errors.New("INVOICE_ALREADY_POSTED")
	ErrInvoiceNotFound                    = errors.New("INVOICE_NOT_FOUND")
	ErrInvoiceHasDependencies             = errors.New("DOCUMENT_HAS_DEPENDENCIES")
	ErrExchangeRateRequired               = errors.New("EXCHANGE_RATE_REQUIRED")
	ErrFutureFinanceDateNotAllowed        = errors.New("FUTURE_FINANCE_DATE_NOT_ALLOWED")
	ErrNegativeBalanceBlocked             = errors.New("NEGATIVE_BALANCE_BLOCKED")
	ErrNegativeBalanceConfirmation        = errors.New("NEGATIVE_BALANCE_CONFIRMATION_REQUIRED")
	ErrAccountBranchImmutable             = errors.New("ACCOUNT_BRANCH_IMMUTABLE")
	ErrAccountInactive                    = errors.New("ACCOUNT_INACTIVE")
	ErrOpeningBalanceExists               = errors.New("OPENING_BALANCE_ALREADY_EXISTS")
)

// DomainError keeps a stable machine-readable code while retaining a short
// Turkish action message for HTTP handlers and UI mapping.
type DomainError struct {
	Code    string
	Message string
	Cause   error
}

func (e *DomainError) Error() string {
	if e.Message == "" {
		return e.Code
	}
	return e.Code + ": " + e.Message
}

func (e *DomainError) Unwrap() error { return e.Cause }

func domainError(code error, message string) error {
	return &DomainError{Code: code.Error(), Message: message, Cause: code}
}

// DomainErrorFor is exported for sibling bounded contexts that need to
// preserve a finance error code at their transaction boundary.
func DomainErrorFor(code error, message string) error { return domainError(code, message) }

// ErrorCode returns the stable error code used by API adapters.
func ErrorCode(err error) string {
	var domain *DomainError
	if errors.As(err, &domain) {
		return domain.Code
	}
	if errors.Is(err, ErrIdempotencyConflict) {
		return ErrIdempotencyConflict.Error()
	}
	return ""
}

// ErrorMessage returns the short Turkish action message carried by a finance
// DomainError, so an HTTP adapter can show the real reason instead of a generic
// "finance step failed" line. Empty when the error carries no domain message.
func ErrorMessage(err error) string {
	var domain *DomainError
	if errors.As(err, &domain) {
		return domain.Message
	}
	return ""
}

type Service struct {
	pool database.Querier
	now  func() time.Time
}

// ensureBranchAccess keeps finance accounts inside the same optional branch
// scope used by the rest of the application. A membership with no branch
// scope rows is unrestricted; once rows exist, a branch-bound account is
// visible only when the caller has that branch.
func ensureBranchAccess(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, companyID, userID, branchID string) error {
	if strings.TrimSpace(userID) == "" || strings.TrimSpace(branchID) == "" {
		return identity.ErrForbidden
	}
	if uuid.Validate(strings.TrimSpace(userID)) != nil || uuid.Validate(strings.TrimSpace(branchID)) != nil {
		return identity.ErrForbidden
	}
	var allowed bool
	if err := q.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM branches b
		WHERE b.company_id=$1 AND b.id=$2 AND b.is_active
		  AND (NOT EXISTS(SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=$1 AND bs.user_id=$3)
		       OR EXISTS(SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=$1 AND bs.user_id=$3 AND bs.branch_id=b.id))
	)`, companyID, branchID, userID).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return identity.ErrForbidden
	}
	return nil
}

// ensurePaymentAccountAccess applies an account's optional branch scope to
// every payment read/retry path. A company-scoped payment may still point at
// a branch-bound cash, bank or POS account, which must not be exposed by a
// direct id or an idempotent replay outside that branch.
func ensurePaymentAccountAccess(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, companyID, userID string, payment Payment) error {
	if payment.AccountID == nil || strings.TrimSpace(*payment.AccountID) == "" {
		return nil
	}
	var branchID *string
	if err := q.QueryRow(ctx, `SELECT branch_id FROM finance_accounts WHERE company_id=$1 AND id=$2`, companyID, *payment.AccountID).Scan(&branchID); errors.Is(err, pgx.ErrNoRows) {
		return identity.ErrForbidden
	} else if err != nil {
		return err
	}
	if branchID == nil {
		return nil
	}
	return ensureBranchAccess(ctx, q, companyID, userID, *branchID)
}

// NewService is the handler-facing constructor used by the application.
func NewService(pool database.Querier) *Service {
	return &Service{pool: pool, now: time.Now}
}

type Account struct {
	ID             string    `json:"id"`
	CompanyID      string    `json:"company_id"`
	AccountType    string    `json:"account_type"`
	Code           string    `json:"code"`
	Name           string    `json:"name"`
	Currency       string    `json:"currency"`
	BranchID       *string   `json:"branch_id,omitempty"`
	BankName       string    `json:"bank_name,omitempty"`
	BankBranchName string    `json:"bank_branch_name,omitempty"`
	BankBranchCode string    `json:"bank_branch_code,omitempty"`
	IBAN           string    `json:"iban,omitempty"`
	AccountNumber  string    `json:"account_number,omitempty"`
	Description    string    `json:"description,omitempty"`
	Notes          string    `json:"notes,omitempty"`
	IsActive       bool      `json:"is_active"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	Version        int64     `json:"version"`
}

type AccountInput struct {
	ID             string `json:"id,omitempty"`
	AccountType    string `json:"account_type"`
	Code           string `json:"code"`
	Name           string `json:"name"`
	Currency       string `json:"currency"`
	BranchID       string `json:"branch_id,omitempty"`
	BankName       string `json:"bank_name,omitempty"`
	BankBranchName string `json:"bank_branch_name,omitempty"`
	BankBranchCode string `json:"bank_branch_code,omitempty"`
	IBAN           string `json:"iban,omitempty"`
	AccountNumber  string `json:"account_number,omitempty"`
	Description    string `json:"description,omitempty"`
	Notes          string `json:"notes,omitempty"`
}

type Payment struct {
	ID                 string    `json:"id"`
	CompanyID          string    `json:"company_id"`
	PartyID            string    `json:"party_id"`
	PartyCode          string    `json:"party_code,omitempty"`
	PartyName          string    `json:"party_name,omitempty"`
	AccountID          *string   `json:"account_id,omitempty"`
	AccountCode        string    `json:"account_code,omitempty"`
	AccountName        string    `json:"account_name,omitempty"`
	MovementID         *string   `json:"movement_id,omitempty"`
	PartyLedgerEntryID string    `json:"party_ledger_entry_id"`
	PaymentKind        string    `json:"payment_kind"`
	PaymentMethod      string    `json:"payment_method"`
	MovementDirection  string    `json:"movement_direction"`
	Currency           string    `json:"currency"`
	Amount             string    `json:"amount"`
	ExchangeRate       string    `json:"exchange_rate"`
	BaseCurrency       *string   `json:"base_currency,omitempty"`
	BaseAmount         *string   `json:"base_amount,omitempty"`
	DocumentNo         string    `json:"document_no"`
	ReferenceNo        string    `json:"reference_no,omitempty"`
	Description        string    `json:"description"`
	Status             string    `json:"status"`
	TransactionDate    time.Time `json:"transaction_date"`
	IdempotencyKey     string    `json:"-"`
	InstrumentID       *string   `json:"instrument_id,omitempty"`
	ReversalOfID       *string   `json:"reversal_of_id,omitempty"`
	ActorUserID        *string   `json:"actor_user_id,omitempty"`
	ActorName          string    `json:"actor_name,omitempty"`
	PostedAt           time.Time `json:"posted_at"`
	// snapshot and requestHash stay internal. The database snapshot is the
	// immutable audit source used to compare a retried command's complete
	// payload (including allocations and instrument fields).
	snapshot    []byte
	requestHash string
}

// PaymentAllocationDetail is the immutable allocation read model shown on a
// tahsilat/ödeme detail card. Open-item values are snapshots/current
// projections; the allocation row itself remains append-only.
type PaymentAllocationDetail struct {
	Allocation
	DocumentID      *string    `json:"document_id,omitempty"`
	DocumentDate    *time.Time `json:"document_date,omitempty"`
	DueDate         *time.Time `json:"due_date,omitempty"`
	OriginalAmount  string     `json:"original_amount,omitempty"`
	RemainingAmount string     `json:"remaining_amount,omitempty"`
}

type PaymentDetail struct {
	Payment
	PartyCode       string                    `json:"party_code,omitempty"`
	PartyName       string                    `json:"party_name,omitempty"`
	AccountCode     string                    `json:"account_code,omitempty"`
	AccountName     string                    `json:"account_name,omitempty"`
	Allocations     []PaymentAllocationDetail `json:"allocations"`
	UnappliedAmount string                    `json:"unapplied_amount"`
}

type Allocation struct {
	ID             string    `json:"id"`
	PaymentID      string    `json:"payment_id"`
	PartyID        string    `json:"party_id"`
	OpenItemID     string    `json:"open_item_id"`
	TargetType     string    `json:"target_type"`
	TargetID       string    `json:"target_id"`
	Currency       string    `json:"currency"`
	Amount         string    `json:"amount"`
	IdempotencyKey string    `json:"-"`
	ReversalOfID   *string   `json:"reversal_of_id,omitempty"`
	AllocatedAt    time.Time `json:"allocated_at"`
}

type OpenItem struct {
	ID              string     `json:"id"`
	DocumentID      string     `json:"document_id"`
	DocumentNo      string     `json:"document_no,omitempty"`
	PartyID         string     `json:"party_id"`
	Side            string     `json:"side"`
	Currency        string     `json:"currency"`
	OriginalAmount  string     `json:"original_amount"`
	AllocatedAmount string     `json:"allocated_amount"`
	OpenAmount      string     `json:"open_amount"`
	ExchangeRate    string     `json:"exchange_rate,omitempty"`
	BaseCurrency    string     `json:"base_currency,omitempty"`
	DocumentDate    time.Time  `json:"document_date"`
	DueDate         *time.Time `json:"due_date,omitempty"`
}

type OpenItemListResult struct {
	Items      []OpenItem `json:"items"`
	NextCursor string     `json:"next_cursor,omitempty"`
}

type PaymentInput struct {
	ID              string            `json:"id,omitempty"`
	PartyID         string            `json:"party_id"`
	AccountID       string            `json:"account_id,omitempty"`
	PaymentKind     string            `json:"payment_kind"` // COLLECTION or PAYMENT
	PaymentMethod   string            `json:"payment_method"`
	Currency        string            `json:"currency"`
	Amount          string            `json:"amount"`
	ExchangeRate    string            `json:"exchange_rate"`
	DocumentNo      string            `json:"document_no,omitempty"`
	ReferenceNo     string            `json:"reference_no,omitempty"`
	Description     string            `json:"description"`
	TransactionDate time.Time         `json:"transaction_date"`
	IdempotencyKey  string            `json:"idempotency_key"`
	Instrument      *InstrumentInput  `json:"instrument,omitempty"`
	InstrumentID    string            `json:"instrument_id,omitempty"`
	Allocations     []AllocationInput `json:"allocations,omitempty"`
	// AutoAllocate distributes the payment across the party's oldest open
	// invoices (FIFO) inside the same transaction that posts the payment. It is
	// mutually exclusive with an explicit Allocations list; any amount that does
	// not fit the open items stays on the party ledger as an advance.
	AutoAllocate   bool   `json:"auto_allocate,omitempty"`
	OverrideReason string `json:"override_reason,omitempty"`
}

type AllocationInput struct {
	OpenItemID     string `json:"open_item_id"`
	Amount         string `json:"amount"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

type Instrument struct {
	ID             string     `json:"id"`
	InstrumentType string     `json:"instrument_type"`
	InstrumentNo   string     `json:"instrument_no"`
	PartyID        string     `json:"party_id"`
	Currency       string     `json:"currency"`
	Amount         string     `json:"amount"`
	IssueDate      time.Time  `json:"issue_date"`
	DueDate        *time.Time `json:"due_date,omitempty"`
	Status         string     `json:"status"`
}

type InstrumentInput struct {
	ID             string     `json:"id,omitempty"`
	InstrumentType string     `json:"instrument_type"`
	InstrumentNo   string     `json:"instrument_no"`
	Currency       string     `json:"currency"`
	Amount         string     `json:"amount"`
	IssueDate      time.Time  `json:"issue_date"`
	DueDate        *time.Time `json:"due_date,omitempty"`
	BankName       string     `json:"bank_name,omitempty"`
	DrawerName     string     `json:"drawer_name,omitempty"`
	Description    string     `json:"description,omitempty"`
}

type ManualEntryInput struct {
	PartyID         string     `json:"party_id"`
	EntryKind       string     `json:"entry_kind"` // DEBIT or CREDIT
	Currency        string     `json:"currency"`
	Amount          string     `json:"amount"`
	ExchangeRate    string     `json:"exchange_rate"`
	Description     string     `json:"description"`
	TransactionDate time.Time  `json:"transaction_date"`
	DueDate         *time.Time `json:"due_date,omitempty"`
	ReferenceNo     string     `json:"reference_no,omitempty"`
	DocumentNo      string     `json:"document_no,omitempty"`
	IdempotencyKey  string     `json:"idempotency_key"`
	ReversalOfID    string     `json:"reversal_of_id,omitempty"`
}

type ManualEntry struct {
	ID                 string     `json:"id"`
	PartyID            string     `json:"party_id"`
	PartyLedgerEntryID string     `json:"party_ledger_entry_id"`
	EntryKind          string     `json:"entry_kind"`
	Currency           string     `json:"currency"`
	Amount             string     `json:"amount"`
	ExchangeRate       string     `json:"exchange_rate"`
	Description        string     `json:"description"`
	TransactionDate    time.Time  `json:"transaction_date"`
	DueDate            *time.Time `json:"due_date,omitempty"`
	ReferenceNo        string     `json:"reference_no,omitempty"`
	DocumentNo         string     `json:"document_no"`
	ReversalOfID       *string    `json:"reversal_of_id,omitempty"`
}

type PartyTransferInput struct {
	FromPartyID     string    `json:"from_party_id"`
	ToPartyID       string    `json:"to_party_id"`
	Currency        string    `json:"currency"`
	Amount          string    `json:"amount"`
	ExchangeRate    string    `json:"exchange_rate,omitempty"`
	Description     string    `json:"description"`
	TransactionDate time.Time `json:"transaction_date"`
	IdempotencyKey  string    `json:"idempotency_key"`
}

type PartyTransfer struct {
	ID                  string `json:"id"`
	FromPartyID         string `json:"from_party_id"`
	ToPartyID           string `json:"to_party_id"`
	DebitLedgerEntryID  string `json:"debit_ledger_entry_id"`
	CreditLedgerEntryID string `json:"credit_ledger_entry_id"`
	Currency            string `json:"currency"`
	Amount              string `json:"amount"`
	ExchangeRate        string `json:"exchange_rate,omitempty"`
}

type InvoicePostingInput struct {
	DocumentID     string
	DocumentType   string
	PartyID        string
	Currency       string
	Amount         string
	ExchangeRate   string
	DocumentDate   time.Time
	DueDate        *time.Time
	Description    string
	IdempotencyKey string
}

type InvoicePosting struct {
	ID                 string `json:"id"`
	DocumentID         string `json:"document_id"`
	PartyLedgerEntryID string `json:"party_ledger_entry_id"`
	OpenItemID         string `json:"open_item_id"`
	Side               string `json:"side"`
	Amount             string `json:"amount"`
	Currency           string `json:"currency"`
}

func can(session identity.Session, permission string) bool {
	return identity.ValidateExternalActor(session) == nil && session.HasPermission(permission)
}

func (s *Service) CreateAccount(ctx context.Context, session identity.Session, input AccountInput, meta identity.RequestMeta) (Account, error) {
	input.AccountType = strings.ToUpper(strings.TrimSpace(input.AccountType))
	input.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))
	input.Code, input.Name = strings.TrimSpace(input.Code), strings.TrimSpace(input.Name)
	if !can(session, accountPermission(input.AccountType, "create")) {
		return Account{}, identity.ErrForbidden
	}
	var err error
	input, err = normalizeAccountInput(input)
	if err != nil {
		return Account{}, err
	}
	id := input.ID
	if id == "" {
		id = uuid.NewString()
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Account{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if input.BranchID != "" {
		if err = ensureBranchAccess(ctx, tx, session.CurrentCompanyID, session.User.ID, input.BranchID); err != nil {
			return Account{}, err
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO finance_accounts(id,company_id,account_type,code,name,currency,branch_id,bank_name,bank_branch_name,bank_branch_code,iban,account_number,description,notes) VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,'')::uuid,$8,$9,$10,$11,$12,$13,$14)`, id, session.CurrentCompanyID, input.AccountType, input.Code, input.Name, input.Currency, input.BranchID, input.BankName, input.BankBranchName, input.BankBranchCode, input.IBAN, input.AccountNumber, input.Description, input.Notes); err != nil {
		return Account{}, mapFinanceConstraint(err)
	}
	if err = writeAuditAndEventTx(ctx, tx, session, "FINANCE_ACCOUNT_CREATED", "finance.account.created", "finance_account", id, meta, map[string]any{"account_type": input.AccountType}); err != nil {
		return Account{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Account{}, err
	}
	return s.loadAccount(ctx, session, id, false)
}

func (s *Service) GetAccount(ctx context.Context, session identity.Session, id string) (Account, error) {
	return s.loadAccount(ctx, session, id, true)
}

// loadAccount is also used by a successful mutation response.  Mutation
// permission must not be coupled to read permission: otherwise a caller with
// create/edit rights could commit successfully and then receive a misleading
// post-commit 403 while decoding the response.
func (s *Service) loadAccount(ctx context.Context, session identity.Session, id string, requireRead bool) (Account, error) {
	var item Account
	var branchID *string
	err := s.pool.QueryRow(ctx, `SELECT id,company_id,account_type,code,name,currency,branch_id,bank_name,bank_branch_name,bank_branch_code,iban,account_number,description,notes,is_active,created_at,updated_at,version FROM finance_accounts WHERE company_id=$1 AND id=$2`, session.CurrentCompanyID, id).Scan(&item.ID, &item.CompanyID, &item.AccountType, &item.Code, &item.Name, &item.Currency, &branchID, &item.BankName, &item.BankBranchName, &item.BankBranchCode, &item.IBAN, &item.AccountNumber, &item.Description, &item.Notes, &item.IsActive, &item.CreatedAt, &item.UpdatedAt, &item.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, identity.ErrForbidden
	}
	if err != nil {
		return Account{}, err
	}
	if requireRead && !can(session, accountPermission(item.AccountType, "read")) {
		return Account{}, identity.ErrForbidden
	}
	if err = ensureFinanceAccountAccess(ctx, s.pool, session, item.ID, branchID); err != nil {
		return Account{}, err
	}
	item.BranchID = branchID
	return item, nil
}

func (s *Service) ListAccounts(ctx context.Context, session identity.Session, accountType string, includeInactive bool) ([]Account, error) {
	accountType = strings.ToUpper(strings.TrimSpace(accountType))
	if accountType != "" && !contains([]string{"CASH", "BANK"}, accountType) {
		return nil, identity.ErrForbidden
	}
	if accountType != "" && !can(session, accountPermission(accountType, "read")) {
		return nil, identity.ErrForbidden
	}
	if accountType == "" && !can(session, accountPermission("CASH", "read")) && !can(session, accountPermission("BANK", "read")) {
		return nil, identity.ErrForbidden
	}
	args := []any{session.CurrentCompanyID}
	query := `SELECT id,company_id,account_type,code,name,currency,branch_id,bank_name,bank_branch_name,bank_branch_code,iban,account_number,description,notes,is_active,created_at,updated_at,version FROM finance_accounts WHERE company_id=$1`
	if session.User.ID != "" {
		args = append(args, session.User.ID)
		userArg := len(args)
		query += fmt.Sprintf(` AND (branch_id IS NULL OR NOT EXISTS(SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=$1 AND bs.user_id=$%d) OR EXISTS(SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=$1 AND bs.user_id=$%d AND bs.branch_id=finance_accounts.branch_id))`, userArg, userArg)
		query += fmt.Sprintf(` AND (NOT EXISTS(SELECT 1 FROM membership_finance_account_scopes fas WHERE fas.company_id=$1 AND fas.user_id=$%d) OR EXISTS(SELECT 1 FROM membership_finance_account_scopes fas WHERE fas.company_id=$1 AND fas.user_id=$%d AND fas.account_id=finance_accounts.id))`, userArg, userArg)
	}
	if accountType != "" {
		args = append(args, accountType)
		query += fmt.Sprintf(` AND account_type=$%d`, len(args))
	} else {
		allowed := make([]string, 0, 2)
		if can(session, accountPermission("CASH", "read")) {
			allowed = append(allowed, "CASH")
		}
		if can(session, accountPermission("BANK", "read")) {
			allowed = append(allowed, "BANK")
		}
		args = append(args, allowed)
		query += fmt.Sprintf(` AND account_type = ANY($%d)`, len(args))
	}
	if !includeInactive {
		query += ` AND is_active`
	}
	query += ` ORDER BY lower(name),id`
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Account{}
	for rows.Next() {
		var item Account
		if err = rows.Scan(&item.ID, &item.CompanyID, &item.AccountType, &item.Code, &item.Name, &item.Currency, &item.BranchID, &item.BankName, &item.BankBranchName, &item.BankBranchCode, &item.IBAN, &item.AccountNumber, &item.Description, &item.Notes, &item.IsActive, &item.CreatedAt, &item.UpdatedAt, &item.Version); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) PostPayment(ctx context.Context, session identity.Session, input PaymentInput, meta identity.RequestMeta) (Payment, error) {
	permission := "finance.payment.post"
	if strings.EqualFold(input.PaymentKind, "COLLECTION") {
		permission = "finance.collection.post"
	}
	if !can(session, permission) {
		return Payment{}, identity.ErrForbidden
	}
	return s.postPayment(ctx, session, input, meta)
}

func (s *Service) PostCollection(ctx context.Context, session identity.Session, input PaymentInput, meta identity.RequestMeta) (Payment, error) {
	input.PaymentKind = "COLLECTION"
	return s.PostPayment(ctx, session, input, meta)
}

func (s *Service) PostPaymentCommand(ctx context.Context, session identity.Session, input PaymentInput, meta identity.RequestMeta) (Payment, error) {
	input.PaymentKind = "PAYMENT"
	return s.PostPayment(ctx, session, input, meta)
}

func (s *Service) postPayment(ctx context.Context, session identity.Session, input PaymentInput, meta identity.RequestMeta) (Payment, error) {
	input.PaymentKind = strings.ToUpper(strings.TrimSpace(input.PaymentKind))
	input.PaymentMethod = strings.ToUpper(strings.TrimSpace(input.PaymentMethod))
	input.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))
	input.PartyID, input.AccountID, input.InstrumentID = strings.TrimSpace(input.PartyID), strings.TrimSpace(input.AccountID), strings.TrimSpace(input.InstrumentID)
	input.DocumentNo, input.ReferenceNo, input.Description, input.IdempotencyKey, input.OverrideReason = strings.TrimSpace(input.DocumentNo), strings.TrimSpace(input.ReferenceNo), strings.TrimSpace(input.Description), strings.TrimSpace(input.IdempotencyKey), strings.TrimSpace(input.OverrideReason)
	partyUUID, partyErr := uuid.Parse(input.PartyID)
	if partyErr != nil || !contains([]string{"COLLECTION", "PAYMENT"}, input.PaymentKind) || !contains([]string{"CASH", "BANK", "POS", "CHECK", "PROMISSORY_NOTE", "OTHER"}, input.PaymentMethod) || input.IdempotencyKey == "" || input.TransactionDate.IsZero() {
		return Payment{}, fmt.Errorf("%w: ödeme türü, yöntemi, tarih ve idempotency anahtarı gereklidir", identity.ErrValidation)
	}
	input.PartyID = partyUUID.String()
	if input.AccountID != "" {
		accountUUID, accountErr := uuid.Parse(input.AccountID)
		if accountErr != nil {
			return Payment{}, fmt.Errorf("%w: hesap kimliği geçersiz", identity.ErrValidation)
		}
		input.AccountID = accountUUID.String()
	}
	if input.InstrumentID != "" {
		instrumentUUID, instrumentErr := uuid.Parse(input.InstrumentID)
		if instrumentErr != nil {
			return Payment{}, fmt.Errorf("%w: çek/senet kimliği geçersiz", identity.ErrValidation)
		}
		input.InstrumentID = instrumentUUID.String()
	}
	if input.AutoAllocate && len(input.Allocations) > 0 {
		return Payment{}, fmt.Errorf("%w: otomatik dağıtım ile elle tahsis birlikte kullanılamaz", identity.ErrValidation)
	}
	if len(input.Allocations) > 0 {
		normalizedAllocations, normalizeErr := normalizeAllocationInputs(input.Allocations)
		if normalizeErr != nil {
			return Payment{}, normalizeErr
		}
		input.Allocations = normalizedAllocations
	}
	amount, err := parsePositive(input.Amount, 4)
	if err != nil {
		return Payment{}, fmt.Errorf("%w: tutar geçersiz", identity.ErrValidation)
	}
	rate, err := parsePositiveDefault(input.ExchangeRate, 10)
	if err != nil {
		return Payment{}, fmt.Errorf("%w: kur geçersiz", identity.ErrValidation)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Payment{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if existing, found, findErr := findPaymentByKey(ctx, tx, session.CurrentCompanyID, input.IdempotencyKey); findErr != nil {
		return Payment{}, findErr
	} else if found {
		if err = ensurePaymentAccountAccess(ctx, tx, session.CurrentCompanyID, session.User.ID, existing); err != nil {
			return Payment{}, err
		}
		if strings.TrimSpace(input.ExchangeRate) == "" && existing.ReversalOfID == nil {
			baseCurrency := ""
			if existing.BaseCurrency != nil {
				baseCurrency = strings.TrimSpace(*existing.BaseCurrency)
			} else if baseErr := tx.QueryRow(ctx, `SELECT base_currency FROM companies WHERE id=$1`, session.CurrentCompanyID).Scan(&baseCurrency); baseErr != nil {
				return Payment{}, baseErr
			}
			if existing.Currency != baseCurrency {
				return Payment{}, domainError(ErrExchangeRateRequired, "yabancı para ödemenin tekrarı için kur gereklidir")
			}
		}
		// Description, document number, currency and (for a foreign payment)
		// exchange rate can be server-defaulted on the first request. Normalize a
		// retry against the persisted values before comparing the immutable hash.
		retryInput := input
		if retryInput.Currency == "" {
			retryInput.Currency = existing.Currency
		}
		if retryInput.Description == "" {
			retryInput.Description = existing.Description
		}
		if retryInput.DocumentNo == "" {
			retryInput.DocumentNo = existing.DocumentNo
		}
		retryRate := rate
		if strings.TrimSpace(retryInput.ExchangeRate) == "" {
			retryInput.ExchangeRate = existing.ExchangeRate
			if parsedRate, parseErr := parsePositive(existing.ExchangeRate, 10); parseErr == nil {
				retryRate = parsedRate
			}
		}
		// Pre-snapshot payments cannot prove that a retry's explicit allocation
		// or instrument payload matches the original command. Reject those rich
		// retries instead of silently accepting a different command under an old
		// idempotency key; simple legacy retries still use the historical field
		// comparison below.
		legacyPayloadComparable := existing.requestHash != "" || (len(retryInput.Allocations) == 0 && !retryInput.AutoAllocate && retryInput.Instrument == nil && retryInput.InstrumentID == "")
		retryHash := paymentRequestHash(retryInput, amount, retryRate)
		if !samePaymentInput(existing, retryInput, amount, retryRate) || !legacyPayloadComparable || (existing.requestHash != "" && existing.requestHash != retryHash) {
			return Payment{}, domainError(ErrIdempotencyConflict, "aynı idempotency anahtarı farklı ödeme verisiyle kullanıldı")
		}
		return existing, nil
	}
	if err = ensurePeriodOpen(ctx, tx, session.CurrentCompanyID, input.TransactionDate); err != nil {
		return Payment{}, err
	}
	if err = ensureFinanceDate(ctx, tx, session.CurrentCompanyID, input.TransactionDate, s.now()); err != nil {
		return Payment{}, err
	}
	var partyCurrency, baseCurrency, partyCode, partyName string
	if err = tx.QueryRow(ctx, `SELECT p.default_currency,c.base_currency,p.code,p.display_name FROM parties p JOIN companies c ON c.id=p.company_id WHERE p.company_id=$1 AND p.id=$2 AND p.is_active FOR UPDATE`, session.CurrentCompanyID, input.PartyID).Scan(&partyCurrency, &baseCurrency, &partyCode, &partyName); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Payment{}, identity.ErrForbidden
		}
		return Payment{}, err
	}
	if input.Currency == "" {
		input.Currency = partyCurrency
	}
	if len(input.Currency) != 3 || input.Currency != strings.ToUpper(input.Currency) {
		return Payment{}, fmt.Errorf("%w: işlem para birimi geçersiz", identity.ErrValidation)
	}
	if input.Currency == baseCurrency {
		if input.ExchangeRate != "" && rate.Cmp(big.NewRat(1, 1)) != 0 {
			return Payment{}, fmt.Errorf("%w: şirket para biriminde kur 1 olmalıdır", identity.ErrValidation)
		}
		rate = big.NewRat(1, 1)
	} else if strings.TrimSpace(input.ExchangeRate) == "" {
		return Payment{}, fmt.Errorf("%w: yabancı para işlemlerinde kur gereklidir", identity.ErrValidation)
	} else if err = ensureExchangeRateWithinTolerance(ctx, tx, session.CurrentCompanyID, input.Currency, input.TransactionDate, rate); err != nil {
		return Payment{}, err
	}
	var accountID, movementID *string
	var accountCurrency, accountCode, accountName string
	if input.AccountID != "" {
		if uuid.Validate(strings.TrimSpace(input.AccountID)) != nil {
			return Payment{}, fmt.Errorf("%w: hesap kimliği geçersiz", identity.ErrValidation)
		}
		var accountType string
		var active bool
		var branchID *string
		err = tx.QueryRow(ctx, `SELECT account_type,currency,is_active,branch_id,code,name FROM finance_accounts WHERE company_id=$1 AND id=$2 FOR UPDATE`, session.CurrentCompanyID, input.AccountID).Scan(&accountType, &accountCurrency, &active, &branchID, &accountCode, &accountName)
		if errors.Is(err, pgx.ErrNoRows) || !active {
			return Payment{}, domainError(ErrAccountInactive, "pasif veya erişilemeyen hesap kullanılamaz")
		}
		if err = ensureFinanceAccountAccess(ctx, tx, session, input.AccountID, branchID); err != nil {
			return Payment{}, err
		}
		if input.PaymentMethod == "OTHER" || input.PaymentMethod == "CHECK" || input.PaymentMethod == "PROMISSORY_NOTE" || accountType != input.PaymentMethod {
			return Payment{}, fmt.Errorf("%w: ödeme yöntemi ile hesap türü eşleşmiyor", identity.ErrValidation)
		}
		if accountCurrency != input.Currency {
			return Payment{}, domainError(ErrCurrencyMismatch, "hesap ve işlem para birimi aynı olmalıdır")
		}
		accountID = &input.AccountID
		if input.PaymentKind == "PAYMENT" {
			if err = enforceNegativeBalanceTx(ctx, tx, session, input.AccountID, amount, input.OverrideReason); err != nil {
				return Payment{}, err
			}
		}
	} else if contains([]string{"CASH", "BANK", "POS"}, input.PaymentMethod) {
		return Payment{}, fmt.Errorf("%w: nakit, banka ve POS ödemelerinde hesap seçilmelidir", identity.ErrValidation)
	}

	paymentID := input.ID
	if uuid.Validate(paymentID) != nil {
		paymentID = uuid.NewString()
	}
	// The payment id is known before an instrument is created. Carry it into
	// the event row so the instrument history has a real source reference
	// instead of a sentinel UUID.
	input.ID = paymentID
	if strings.TrimSpace(input.DocumentNo) == "" {
		prefix := "ODM"
		if input.PaymentKind == "COLLECTION" {
			prefix = "THS"
		}
		input.DocumentNo, err = nextPaymentNumberTx(ctx, tx, session.CurrentCompanyID, prefix, input.TransactionDate)
		if err != nil {
			return Payment{}, err
		}
	}
	// The party ledger and cash/bank movement rows require a non-empty
	// description. Description is optional on the request, so fall back to a
	// document-scoped default instead of failing the whole command.
	if strings.TrimSpace(input.Description) == "" {
		if input.PaymentKind == "COLLECTION" {
			input.Description = "Tahsilat " + input.DocumentNo
		} else {
			input.Description = "Ödeme " + input.DocumentNo
		}
	}
	// Hash the effective command, after server defaults have been materialized.
	// Computing this before document_no/description defaulting would make a
	// perfectly valid retry (which omits those optional fields) look like a
	// different payment even though the first request persisted the defaults.
	requestHash := paymentRequestHash(input, amount, rate)
	ledgerID := uuid.NewString()
	var instrumentID *string
	if contains([]string{"CHECK", "PROMISSORY_NOTE"}, input.PaymentMethod) {
		instrumentID, err = s.ensureInstrumentTx(ctx, tx, session, input, amount, meta)
		if err != nil {
			return Payment{}, err
		}
	}
	baseAmount := new(big.Rat).Mul(amount, rate)
	if amountString(baseAmount, 4) == "0.0000" {
		return Payment{}, fmt.Errorf("%w: temel para birimine çevrilen ödeme tutarı dört ondalıkta sıfıra yuvarlanamaz", identity.ErrValidation)
	}
	snapshot := jsonBytes(map[string]any{"transaction_currency": input.Currency, "amount": amountString(amount, 4), "base_currency": baseCurrency, "base_amount": amountString(baseAmount, 4), "exchange_rate": amountString(rate, 10), "payment_method": input.PaymentMethod, "override_reason": input.OverrideReason, "request_hash": requestHash, "party_code": partyCode, "party_name": partyName, "account_code": accountCode, "account_name": accountName})
	if accountID != nil {
		movementIDValue := uuid.NewString()
		movementID = &movementIDValue
		if _, err = tx.Exec(ctx, `INSERT INTO finance_account_movements(id,company_id,account_id,movement_kind,direction,currency,amount,transaction_date,source_type,source_id,idempotency_key,description,reversal_of_id,actor_user_id,snapshot,exchange_rate,base_currency,base_amount) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'finance_payment',$9,$10,$11,NULL,$12,$13,$14,$15,ROUND($7::numeric*$14::numeric,4))`, movementIDValue, session.CurrentCompanyID, *accountID, input.PaymentKind, paymentMovementDirection(input.PaymentKind), input.Currency, amountString(amount, 4), input.TransactionDate, paymentID, "payment:"+input.IdempotencyKey, input.Description, nullableUUID(session.User.ID), snapshot, amountString(rate, 10), baseCurrency); err != nil {
			return Payment{}, mapFinanceConstraint(err)
		}
	}
	debit, credit := "0", amountString(amount, 4)
	if input.PaymentKind == "PAYMENT" {
		debit, credit = amountString(amount, 4), "0"
	}
	if _, err = tx.Exec(ctx, `INSERT INTO party_ledger_entries(id,company_id,party_id,currency,entry_type,source_type,source_id,idempotency_key,description,debit,credit,exchange_rate,document_date,actor_user_id,snapshot) VALUES($1,$2,$3,$4,$5,'finance_payment',$6,$7,$8,$9,$10,$11,$12,$13,$14)`, ledgerID, session.CurrentCompanyID, input.PartyID, input.Currency, input.PaymentKind, paymentID, "payment:"+input.IdempotencyKey+":party", input.Description, debit, credit, amountString(rate, 10), input.TransactionDate, nullableUUID(session.User.ID), snapshot); err != nil {
		return Payment{}, mapFinanceConstraint(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO finance_payments(id,company_id,party_id,account_id,movement_id,party_ledger_entry_id,payment_kind,movement_direction,currency,amount,document_no,reference_no,description,transaction_date,idempotency_key,reversal_of_id,actor_user_id,snapshot,payment_method,exchange_rate,base_currency,base_amount,instrument_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,NULL,$16,$17,$18,$19,$20,ROUND($10::numeric*$19::numeric,4),$21)`, paymentID, session.CurrentCompanyID, input.PartyID, accountID, movementID, ledgerID, input.PaymentKind, paymentMovementDirection(input.PaymentKind), input.Currency, amountString(amount, 4), strings.TrimSpace(input.DocumentNo), strings.TrimSpace(input.ReferenceNo), input.Description, input.TransactionDate, input.IdempotencyKey, nullableUUID(session.User.ID), snapshot, input.PaymentMethod, amountString(rate, 10), baseCurrency, instrumentID); err != nil {
		return Payment{}, mapFinanceConstraint(err)
	}
	allocations := input.Allocations
	if input.AutoAllocate && len(allocations) == 0 {
		allocations, err = fifoOpenItemAllocationsTx(ctx, tx, session.CurrentCompanyID, input.PartyID, input.Currency, input.PaymentKind, amount)
		if err != nil {
			return Payment{}, err
		}
	}
	if _, err = s.applyAllocationsTx(ctx, tx, session, paymentID, allocations, meta); err != nil {
		return Payment{}, err
	}
	if err = writeAuditAndEventTx(ctx, tx, session, "FINANCE_PAYMENT_POSTED", "finance.payment.posted", "finance_payment", paymentID, meta, map[string]any{"payment_id": paymentID, "payment_kind": input.PaymentKind}); err != nil {
		return Payment{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Payment{}, err
	}
	return s.loadPaymentAfterMutation(ctx, session, paymentID)
}

// fifoOpenItemAllocationsTx selects the oldest matching invoice balances. It
// intentionally returns only the amount that can be applied; any excess
// payment remains on the party ledger as an advance and can be allocated by a
// later command.
func fifoOpenItemAllocationsTx(ctx context.Context, tx pgx.Tx, companyID, partyID, currency, paymentKind string, amount *big.Rat) ([]AllocationInput, error) {
	side := "RECEIVABLE"
	if paymentKind == "PAYMENT" {
		side = "PAYABLE"
	}
	rows, err := tx.Query(ctx, `SELECT oi.id,oi.document_id,oi.document_date,oi.due_date,oi.original_amount::text,
		COALESCE((SELECT r.amount FROM finance_invoice_open_item_reversals r WHERE r.company_id=oi.company_id AND r.open_item_id=oi.id),0)::text
		FROM finance_invoice_open_items oi
		JOIN documents d ON d.company_id=oi.company_id AND d.id=oi.document_id
		WHERE oi.company_id=$1 AND oi.party_id=$2 AND oi.currency=$3 AND oi.side=$4
		  AND d.document_type_code IN ('SALES_INVOICE','PURCHASE_INVOICE')
		ORDER BY COALESCE(oi.due_date,'9999-12-31'::date),oi.document_date,oi.created_at,oi.id FOR UPDATE`, companyID, partyID, currency, side)
	if err != nil {
		return nil, err
	}
	type openItemRow struct {
		id, documentID, original, reversed string
		documentDate                       time.Time
		dueDate                            *time.Time
	}
	openItemRows := make([]openItemRow, 0)
	for rows.Next() {
		var id, documentID, original, reversed string
		var documentDate time.Time
		var dueDate *time.Time
		if err = rows.Scan(&id, &documentID, &documentDate, &dueDate, &original, &reversed); err != nil {
			rows.Close()
			return nil, err
		}
		openItemRows = append(openItemRows, openItemRow{id: id, documentID: documentID, original: original, reversed: reversed, documentDate: documentDate, dueDate: dueDate})
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	items := make([]OpenItem, 0, len(openItemRows))
	for _, row := range openItemRows {
		allocated, allocationErr := allocationForOpenItem(ctx, tx, companyID, row.id)
		if allocationErr != nil {
			return nil, allocationErr
		}
		open := new(big.Rat).Sub(mustRat(row.original), mustRat(row.reversed))
		returned, returnErr := returnedAmountForDocumentTx(ctx, tx, companyID, row.documentID)
		if returnErr != nil {
			return nil, returnErr
		}
		open.Sub(open, returned)
		open.Sub(open, mustRat(allocated))
		if open.Sign() > 0 {
			items = append(items, OpenItem{ID: row.id, DocumentDate: row.documentDate, DueDate: row.dueDate, OpenAmount: amountString(open, 4)})
		}
	}
	return FIFOAllocations(items, amountString(amount, 4))
}

// nextPaymentNumberTx allocates a human-facing number while holding the
// company/year sequence row lock. It is intentionally separate from UUID
// identity and safe when several users post at the same time.
func nextPaymentNumberTx(ctx context.Context, tx pgx.Tx, companyID, prefix string, transactionDate time.Time) (string, error) {
	// transaction_date is a business date; preserve the caller/company
	// calendar instead of shifting midnight +03:00 into the previous UTC year.
	year := transactionDate.Year()
	if year < 2000 || year > 2200 {
		return "", fmt.Errorf("%w: işlem yılı geçersiz", identity.ErrValidation)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO company_business_sequences(company_id,sequence_key,sequence_year,next_value) VALUES($1,$2,$3,1) ON CONFLICT DO NOTHING`, companyID, prefix, year); err != nil {
		return "", err
	}
	var value int64
	if err := tx.QueryRow(ctx, `UPDATE company_business_sequences SET next_value=next_value+1 WHERE company_id=$1 AND sequence_key=$2 AND sequence_year=$3 RETURNING next_value-1`, companyID, prefix, year).Scan(&value); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-%04d-%06d", prefix, year, value), nil
}

func sameDate(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Format("2006-01-02") == right.Format("2006-01-02")
}

func (s *Service) GetPayment(ctx context.Context, session identity.Session, id string) (Payment, error) {
	return s.loadPayment(ctx, session, id, true)
}

func (s *Service) loadPaymentAfterMutation(ctx context.Context, session identity.Session, id string) (Payment, error) {
	return s.loadPayment(ctx, session, id, false)
}

func (s *Service) loadPayment(ctx context.Context, session identity.Session, id string, requireRead bool) (Payment, error) {
	item, err := getPayment(ctx, s.pool, session.CurrentCompanyID, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Payment{}, identity.ErrForbidden
	}
	if err != nil {
		return Payment{}, err
	}
	permission := "finance.payment.read"
	if item.PaymentKind == "COLLECTION" {
		permission = "finance.collection.read"
	}
	if requireRead && !can(session, permission) {
		return Payment{}, identity.ErrForbidden
	}
	if err = ensurePaymentAccountAccess(ctx, s.pool, session.CurrentCompanyID, session.User.ID, item); err != nil {
		return Payment{}, err
	}
	return item, nil
}

// GetPaymentDetail keeps GetPayment's stable signature while exposing the
// joined names and immutable allocation rows needed by payment detail cards.
// All reads are constrained by the current company and the same account
// scope check used by GetPayment.
func (s *Service) GetPaymentDetail(ctx context.Context, session identity.Session, id string) (PaymentDetail, error) {
	payment, err := s.GetPayment(ctx, session, id)
	if err != nil {
		return PaymentDetail{}, err
	}
	detail := PaymentDetail{Payment: payment, Allocations: []PaymentAllocationDetail{}}
	if err = s.pool.QueryRow(ctx, `SELECT p.code,p.display_name,COALESCE(a.code,''),COALESCE(a.name,'') FROM parties p LEFT JOIN finance_accounts a ON a.company_id=$1 AND a.id=$2 WHERE p.company_id=$1 AND p.id=$3`, session.CurrentCompanyID, payment.AccountID, payment.PartyID).Scan(&detail.PartyCode, &detail.PartyName, &detail.AccountCode, &detail.AccountName); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PaymentDetail{}, identity.ErrForbidden
		}
		return PaymentDetail{}, err
	}
	detail.PartyCode, detail.PartyName, detail.AccountCode, detail.AccountName = payment.PartyCode, payment.PartyName, payment.AccountCode, payment.AccountName

	rows, err := s.pool.Query(ctx, `
		SELECT a.id,a.payment_id,a.party_id,COALESCE(a.open_item_id::text,''),a.target_type,a.target_id,a.currency,a.amount::text,a.reversal_of_id,a.allocated_at,
		       oi.document_id,oi.document_date,oi.due_date,COALESCE(oi.original_amount::text,''),
		       COALESCE((oi.original_amount
		           - COALESCE((SELECT r.amount FROM finance_invoice_open_item_reversals r WHERE r.company_id=oi.company_id AND r.open_item_id=oi.id),0)
		           - COALESCE((SELECT SUM(CASE WHEN pa.reversal_of_id IS NULL THEN pa.amount ELSE -pa.amount END) FROM finance_payment_allocations pa WHERE pa.company_id=oi.company_id AND pa.open_item_id=oi.id),0))::text,'')
		FROM finance_payment_allocations a
		LEFT JOIN finance_invoice_open_items oi ON oi.company_id=a.company_id AND oi.id=a.open_item_id
		WHERE a.company_id=$1 AND a.payment_id=$2
		ORDER BY a.allocated_at,a.id`, session.CurrentCompanyID, payment.ID)
	if err != nil {
		return PaymentDetail{}, err
	}
	defer rows.Close()
	applied := new(big.Rat)
	for rows.Next() {
		var item PaymentAllocationDetail
		var openItemID, targetID string
		var reversalID *string
		var documentID *string
		var dueDate *time.Time
		if err = rows.Scan(&item.ID, &item.PaymentID, &item.PartyID, &openItemID, &item.TargetType, &targetID, &item.Currency, &item.Amount, &reversalID, &item.AllocatedAt, &documentID, &item.DocumentDate, &dueDate, &item.OriginalAmount, &item.RemainingAmount); err != nil {
			return PaymentDetail{}, err
		}
		item.OpenItemID, item.TargetID = openItemID, targetID
		item.DocumentID, item.DueDate = documentID, dueDate
		item.ReversalOfID = reversalID
		amount, parseErr := parseRat(item.Amount)
		if parseErr != nil {
			return PaymentDetail{}, parseErr
		}
		if item.ReversalOfID != nil {
			applied.Sub(applied, amount)
		} else {
			applied.Add(applied, amount)
		}
		detail.Allocations = append(detail.Allocations, item)
	}
	if err = rows.Err(); err != nil {
		return PaymentDetail{}, err
	}
	remaining := new(big.Rat).Sub(mustRat(payment.Amount), applied)
	if remaining.Sign() < 0 {
		remaining.SetInt64(0)
	}
	detail.UnappliedAmount = amountString(remaining, 4)
	return detail, nil
}

type PaymentListOptions struct {
	Kind      string
	PartyID   string
	Method    string // CASH or BANK
	Status    string // POSTED or REVERSED
	AccountID string
	AmountMin string
	AmountMax string
	From      *time.Time
	To        *time.Time
	Cursor    string
	Limit     int
}

type PaymentListResult struct {
	Items      []Payment `json:"items"`
	NextCursor string    `json:"next_cursor,omitempty"`
}

// ListPayments is a company-scoped read model for collections/payment grids.
func (s *Service) ListPayments(ctx context.Context, session identity.Session, options PaymentListOptions) ([]Payment, error) {
	result, err := s.ListPaymentsPaged(ctx, session, options)
	return result.Items, err
}

func (s *Service) ListPaymentsPaged(ctx context.Context, session identity.Session, options PaymentListOptions) (PaymentListResult, error) {
	requestedKind := strings.ToUpper(strings.TrimSpace(options.Kind))
	if requestedKind == "COLLECTION" && !can(session, "finance.collection.read") {
		return PaymentListResult{}, identity.ErrForbidden
	}
	if requestedKind == "PAYMENT" && !can(session, "finance.payment.read") {
		return PaymentListResult{}, identity.ErrForbidden
	}
	if requestedKind == "" && !can(session, "finance.collection.read") && !can(session, "finance.payment.read") {
		return PaymentListResult{}, identity.ErrForbidden
	}
	if options.Limit < 1 || options.Limit > 500 {
		options.Limit = 100
	}
	if strings.TrimSpace(options.PartyID) != "" {
		partyUUID, partyErr := uuid.Parse(strings.TrimSpace(options.PartyID))
		if partyErr != nil {
			return PaymentListResult{}, fmt.Errorf("%w: cari kimliği geçersiz", identity.ErrValidation)
		}
		options.PartyID = partyUUID.String()
	}
	if options.From != nil && options.To != nil && options.To.Format("2006-01-02") < options.From.Format("2006-01-02") {
		return PaymentListResult{}, fmt.Errorf("%w: ödeme tarih aralığı geçersiz", identity.ErrValidation)
	}
	args := []any{session.CurrentCompanyID}
	query := `SELECT id FROM finance_payments WHERE company_id=$1`
	if session.User.ID != "" {
		args = append(args, session.User.ID)
		userArg := len(args)
		query += fmt.Sprintf(` AND (account_id IS NULL OR EXISTS(
			SELECT 1 FROM finance_accounts fa
			WHERE fa.company_id=finance_payments.company_id AND fa.id=finance_payments.account_id
			  AND (fa.branch_id IS NULL OR NOT EXISTS(SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=fa.company_id AND bs.user_id=$%d)
			       OR EXISTS(SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=fa.company_id AND bs.user_id=$%d AND bs.branch_id=fa.branch_id))
			  AND (NOT EXISTS(SELECT 1 FROM membership_finance_account_scopes fas WHERE fas.company_id=fa.company_id AND fas.user_id=$%d)
			       OR EXISTS(SELECT 1 FROM membership_finance_account_scopes fas WHERE fas.company_id=fa.company_id AND fas.user_id=$%d AND fas.account_id=fa.id))
		))`, userArg, userArg, userArg, userArg)
	}
	if value := requestedKind; value != "" {
		if !contains([]string{"COLLECTION", "PAYMENT"}, value) {
			return PaymentListResult{}, fmt.Errorf("%w: ödeme türü geçersiz", identity.ErrValidation)
		}
		args = append(args, value)
		query += fmt.Sprintf(" AND payment_kind=$%d", len(args))
	} else if can(session, "finance.collection.read") != can(session, "finance.payment.read") {
		allowedKind := "PAYMENT"
		if can(session, "finance.collection.read") {
			allowedKind = "COLLECTION"
		}
		args = append(args, allowedKind)
		query += fmt.Sprintf(" AND payment_kind=$%d", len(args))
	}
	if options.PartyID != "" {
		args = append(args, options.PartyID)
		query += fmt.Sprintf(" AND party_id=$%d", len(args))
	}
	if method := strings.ToUpper(strings.TrimSpace(options.Method)); method != "" {
		if !contains([]string{"CASH", "BANK"}, method) {
			return PaymentListResult{}, fmt.Errorf("%w: ödeme yöntemi geçersiz", identity.ErrValidation)
		}
		args = append(args, method)
		query += fmt.Sprintf(" AND payment_method=$%d", len(args))
	}
	if options.AccountID != "" {
		if uuid.Validate(strings.TrimSpace(options.AccountID)) != nil {
			return PaymentListResult{}, fmt.Errorf("%w: hesap kimliği geçersiz", identity.ErrValidation)
		}
		args = append(args, strings.TrimSpace(options.AccountID))
		query += fmt.Sprintf(" AND account_id=$%d", len(args))
	}
	if raw := strings.TrimSpace(options.AmountMin); raw != "" {
		value, parseErr := parsePositive(raw, 4)
		if parseErr != nil {
			return PaymentListResult{}, fmt.Errorf("%w: en düşük tutar geçersiz", identity.ErrValidation)
		}
		args = append(args, amountString(value, 4))
		query += fmt.Sprintf(" AND amount >= $%d", len(args))
	}
	if raw := strings.TrimSpace(options.AmountMax); raw != "" {
		value, parseErr := parsePositive(raw, 4)
		if parseErr != nil {
			return PaymentListResult{}, fmt.Errorf("%w: en yüksek tutar geçersiz", identity.ErrValidation)
		}
		args = append(args, amountString(value, 4))
		query += fmt.Sprintf(" AND amount <= $%d", len(args))
	}
	switch strings.ToUpper(strings.TrimSpace(options.Status)) {
	case "":
	case "REVERSED":
		query += " AND EXISTS(SELECT 1 FROM finance_payments r WHERE r.company_id=finance_payments.company_id AND r.reversal_of_id=finance_payments.id)"
	case "POSTED":
		query += " AND reversal_of_id IS NULL AND NOT EXISTS(SELECT 1 FROM finance_payments r WHERE r.company_id=finance_payments.company_id AND r.reversal_of_id=finance_payments.id)"
	default:
		return PaymentListResult{}, fmt.Errorf("%w: ödeme durumu geçersiz", identity.ErrValidation)
	}
	if options.From != nil {
		args = append(args, options.From.Format("2006-01-02"))
		query += fmt.Sprintf(" AND transaction_date >= $%d::date", len(args))
	}
	if options.To != nil {
		args = append(args, options.To.Format("2006-01-02"))
		query += fmt.Sprintf(" AND transaction_date <= $%d::date", len(args))
	}
	if options.Cursor != "" {
		lastDate, lastPostedAt, lastID, err := decodePaymentCursor(options.Cursor)
		if err != nil {
			return PaymentListResult{}, fmt.Errorf("%w: ödeme listesi cursor bilgisi geçersiz", identity.ErrValidation)
		}
		args = append(args, lastDate.Format("2006-01-02"), lastPostedAt, lastID)
		query += fmt.Sprintf(" AND (transaction_date,posted_at,id) < ($%d::date,$%d::timestamptz,$%d::uuid)", len(args)-2, len(args)-1, len(args))
	}
	args = append(args, options.Limit+1)
	query += fmt.Sprintf(" ORDER BY transaction_date DESC,posted_at DESC,id DESC LIMIT $%d", len(args))
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return PaymentListResult{}, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return PaymentListResult{}, err
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return PaymentListResult{}, err
	}
	rows.Close()
	// getPayment and the access check issue their own queries; run them only
	// after the id rows are drained, otherwise the request-pinned connection
	// fails with "conn busy".
	items := []Payment{}
	for _, id := range ids {
		item, getErr := getPayment(ctx, s.pool, session.CurrentCompanyID, id)
		if getErr != nil {
			return PaymentListResult{}, getErr
		}
		if getErr = ensurePaymentAccountAccess(ctx, s.pool, session.CurrentCompanyID, session.User.ID, item); getErr != nil {
			return PaymentListResult{}, getErr
		}
		items = append(items, item)
	}
	result := PaymentListResult{Items: items}
	if len(items) > options.Limit {
		last := items[options.Limit-1]
		result.Items = items[:options.Limit]
		result.NextCursor = encodePaymentCursor(last.TransactionDate, last.PostedAt, last.ID)
	}
	return result, nil
}

func encodePaymentCursor(transactionDate, postedAt time.Time, id string) string {
	value := transactionDate.Format("2006-01-02") + "|" + postedAt.UTC().Format(time.RFC3339Nano) + "|" + id
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func decodePaymentCursor(value string) (time.Time, time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return time.Time{}, time.Time{}, "", err
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 3 {
		return time.Time{}, time.Time{}, "", fmt.Errorf("invalid cursor")
	}
	transactionDate, err := time.Parse("2006-01-02", parts[0])
	if err != nil {
		return time.Time{}, time.Time{}, "", err
	}
	postedAt, err := time.Parse(time.RFC3339Nano, parts[1])
	if err != nil || strings.TrimSpace(parts[2]) == "" {
		return time.Time{}, time.Time{}, "", fmt.Errorf("invalid cursor")
	}
	return transactionDate, postedAt, parts[2], nil
}

func encodeOpenItemCursor(due string, documentDate, createdAt time.Time, id string) string {
	value := strings.Join([]string{due, documentDate.Format("2006-01-02"), createdAt.UTC().Format(time.RFC3339Nano), id}, "|")
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func decodeOpenItemCursor(value string) (string, time.Time, time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return "", time.Time{}, time.Time{}, "", err
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 4 {
		return "", time.Time{}, time.Time{}, "", fmt.Errorf("invalid cursor")
	}
	if parts[0] == "" {
		parts[0] = "9999-12-31"
	}
	due, err := time.Parse("2006-01-02", parts[0])
	if err != nil {
		return "", time.Time{}, time.Time{}, "", err
	}
	documentDate, err := time.Parse("2006-01-02", parts[1])
	if err != nil {
		return "", time.Time{}, time.Time{}, "", err
	}
	createdAt, err := time.Parse(time.RFC3339Nano, parts[2])
	if err != nil || uuid.Validate(parts[3]) != nil {
		return "", time.Time{}, time.Time{}, "", fmt.Errorf("invalid cursor")
	}
	return due.Format("2006-01-02"), documentDate, createdAt, parts[3], nil
}

// ListOpenItems keeps the original slice API for FIFO callers. The HTTP
// endpoint uses ListOpenItemsPage so a large open-item set is never silently
// truncated at the default page size.
func (s *Service) ListOpenItems(ctx context.Context, session identity.Session, partyID, currency, side string, limit int) ([]OpenItem, error) {
	result, err := s.ListOpenItemsPage(ctx, session, partyID, currency, side, "", limit)
	if err != nil {
		return nil, err
	}
	return result.Items, nil
}

// ListOpenItemsPage returns the FIFO-ready projection; allocated and reversed
// amounts are derived from immutable allocation/reversal rows at read time.
// Cursor order is due date (NULLS LAST), document date, creation timestamp and
// UUID, matching the deterministic FIFO order.
func (s *Service) ListOpenItemsPage(ctx context.Context, session identity.Session, partyID, currency, side, cursor string, limit int) (OpenItemListResult, error) {
	result := OpenItemListResult{Items: []OpenItem{}}
	side = strings.ToUpper(strings.TrimSpace(side))
	partyID = strings.TrimSpace(partyID)
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if partyID != "" {
		if _, err := uuid.Parse(partyID); err != nil {
			return result, fmt.Errorf("%w: cari kimliği geçersiz", identity.ErrValidation)
		}
	}
	if currency != "" && (len(currency) != 3 || strings.Trim(currency, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "") {
		return result, fmt.Errorf("%w: açık kalem para birimi geçersiz", identity.ErrValidation)
	}
	if side != "" && side != "RECEIVABLE" && side != "PAYABLE" {
		return result, fmt.Errorf("%w: açık kalem yönü geçersiz", identity.ErrValidation)
	}
	canReceivable, canPayable := can(session, "finance.collection.read"), can(session, "finance.payment.read")
	if side == "RECEIVABLE" && !canReceivable {
		return result, identity.ErrForbidden
	}
	if side == "PAYABLE" && !canPayable {
		return result, identity.ErrForbidden
	}
	if side == "" && !canReceivable && !canPayable {
		return result, identity.ErrForbidden
	}
	// A caller with only one directional read permission must not receive the
	// other side merely by omitting the filter.
	if side == "" && canReceivable != canPayable {
		if canReceivable {
			side = "RECEIVABLE"
		} else {
			side = "PAYABLE"
		}
	}
	if limit < 1 || limit > 500 {
		limit = 100
	}
	args := []any{session.CurrentCompanyID}
	query := `SELECT oi.id,oi.document_id,COALESCE(d.document_no,''),oi.party_id,oi.side,oi.currency,oi.original_amount::text,
COALESCE((SELECT SUM(CASE WHEN a.reversal_of_id IS NULL THEN a.amount ELSE -a.amount END) FROM finance_payment_allocations a WHERE a.company_id=oi.company_id AND a.open_item_id=oi.id),0)::text,
COALESCE((SELECT r.amount FROM finance_invoice_open_item_reversals r WHERE r.company_id=oi.company_id AND r.open_item_id=oi.id),0)::text,
COALESCE(returns.returned_amount,0)::text,oi.document_date,oi.due_date::text,oi.exchange_rate::text,oi.base_currency,oi.created_at
FROM finance_invoice_open_items oi
JOIN documents d ON d.company_id=oi.company_id AND d.id=oi.document_id AND d.document_type_code IN ('SALES_INVOICE','PURCHASE_INVOICE')
LEFT JOIN LATERAL (
    SELECT COALESCE(SUM(GREATEST(return_item.original_amount-COALESCE(return_reversal.amount,0),0)),0) AS returned_amount
    FROM (
        SELECT DISTINCT relation.document_id
        FROM commercial_document_sources relation
        JOIN documents return_document ON return_document.company_id=relation.company_id AND return_document.id=relation.document_id
        WHERE relation.company_id=oi.company_id
          AND relation.relation_type='RETURN'
          AND return_document.status='POSTED'
          AND return_document.document_type_code IN ('SALES_RETURN_INVOICE','PURCHASE_RETURN_INVOICE')
          AND (relation.source_document_id=oi.document_id OR relation.source_document_id IN (SELECT source_document_id FROM commercial_document_sources source WHERE source.company_id=oi.company_id AND source.document_id=oi.document_id))
    ) return_documents
    JOIN finance_invoice_open_items return_item ON return_item.company_id=oi.company_id AND return_item.document_id=return_documents.document_id
    LEFT JOIN finance_invoice_open_item_reversals return_reversal ON return_reversal.company_id=return_item.company_id AND return_reversal.open_item_id=return_item.id
) returns ON TRUE
WHERE oi.company_id=$1`
	if session.User.ID != "" {
		args = append(args, session.User.ID)
		query += fmt.Sprintf(` AND (d.branch_id IS NULL OR NOT EXISTS(SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=d.company_id AND bs.user_id=$%d) OR EXISTS(SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=d.company_id AND bs.user_id=$%d AND bs.branch_id=d.branch_id))`, len(args), len(args))
	}
	if partyID != "" {
		args = append(args, partyID)
		query += fmt.Sprintf(" AND oi.party_id=$%d", len(args))
	}
	if currency != "" {
		args = append(args, currency)
		query += fmt.Sprintf(" AND oi.currency=$%d", len(args))
	}
	if side != "" {
		args = append(args, side)
		query += fmt.Sprintf(" AND oi.side=$%d", len(args))
	}
	if cursor != "" {
		lastDue, lastDocument, lastCreated, lastID, cursorErr := decodeOpenItemCursor(cursor)
		if cursorErr != nil {
			return result, fmt.Errorf("%w: açık kalem cursor bilgisi geçersiz", identity.ErrValidation)
		}
		args = append(args, lastDue, lastDocument, lastCreated, lastID)
		query += fmt.Sprintf(" AND (COALESCE(oi.due_date,'9999-12-31'::date),oi.document_date,oi.created_at,oi.id) > ($%d::date,$%d::date,$%d::timestamptz,$%d::uuid)", len(args)-3, len(args)-2, len(args)-1, len(args))
	}
	query += ` AND oi.original_amount - COALESCE((SELECT SUM(CASE WHEN a.reversal_of_id IS NULL THEN a.amount ELSE -a.amount END) FROM finance_payment_allocations a WHERE a.company_id=oi.company_id AND a.open_item_id=oi.id),0) - COALESCE((SELECT r.amount FROM finance_invoice_open_item_reversals r WHERE r.company_id=oi.company_id AND r.open_item_id=oi.id),0) - COALESCE(returns.returned_amount,0) > 0`
	// Fetch one sentinel row so the response can expose a cursor without
	// dropping the fact that more open items exist.  A LIMIT equal to the
	// requested page size can never prove that another page is available.
	args = append(args, limit+1)
	query += fmt.Sprintf(" ORDER BY COALESCE(oi.due_date,'9999-12-31'::date),oi.document_date,oi.created_at,oi.id LIMIT $%d", len(args))
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	var lastCreatedAt time.Time
	var lastDue string
	for rows.Next() {
		var item OpenItem
		var original, allocated, reversed, returned string
		var dueDate *string
		var createdAt time.Time
		if err = rows.Scan(&item.ID, &item.DocumentID, &item.DocumentNo, &item.PartyID, &item.Side, &item.Currency, &original, &allocated, &reversed, &returned, &item.DocumentDate, &dueDate, &item.ExchangeRate, &item.BaseCurrency, &createdAt); err != nil {
			return result, err
		}
		item.OriginalAmount = original
		item.AllocatedAmount = allocated
		open := new(big.Rat).Sub(mustRat(item.OriginalAmount), mustRat(reversed))
		open.Sub(open, mustRat(returned))
		open.Sub(open, mustRat(item.AllocatedAmount))
		if open.Sign() < 0 {
			open.SetInt64(0)
		}
		item.OpenAmount = amountString(open, 4)
		if dueDate != nil {
			if value, parseErr := time.Parse("2006-01-02", *dueDate); parseErr == nil {
				item.DueDate = &value
			}
		}
		if len(result.Items) < limit {
			lastCreatedAt = createdAt
			lastDue = "9999-12-31"
			if item.DueDate != nil {
				lastDue = item.DueDate.Format("2006-01-02")
			}
		}
		result.Items = append(result.Items, item)
	}
	if err = rows.Err(); err != nil {
		return result, err
	}
	if len(result.Items) > limit {
		last := result.Items[limit-1]
		result.Items = result.Items[:limit]
		result.NextCursor = encodeOpenItemCursor(lastDue, last.DocumentDate, lastCreatedAt, last.ID)
	}
	return result, nil
}

func (s *Service) AllocatePayment(ctx context.Context, session identity.Session, paymentID string, inputs []AllocationInput, meta identity.RequestMeta) ([]Allocation, error) {
	if !can(session, "finance.allocation.manage") {
		return nil, identity.ErrForbidden
	}
	paymentUUID, paymentErr := uuid.Parse(strings.TrimSpace(paymentID))
	if paymentErr != nil {
		return nil, fmt.Errorf("%w: ödeme kimliği geçersiz", identity.ErrValidation)
	}
	paymentID = paymentUUID.String()
	if len(inputs) == 0 {
		return nil, fmt.Errorf("%w: en az bir tahsis seçilmelidir", identity.ErrValidation)
	}
	normalizedInputs, normalizeErr := normalizeAllocationInputs(inputs)
	if normalizeErr != nil {
		return nil, normalizeErr
	}
	inputs = normalizedInputs
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	payload, _ := json.Marshal(struct {
		PaymentID   string            `json:"payment_id"`
		Allocations []AllocationInput `json:"allocations"`
	}{paymentID, inputs})
	reservation, err := idempotency.ReserveTx(ctx, tx, session.CurrentCompanyID, meta.IdempotencyKey, "finance.payment.allocate", payload, session.User.ID, meta.TraceID)
	if err != nil {
		return nil, err
	}
	if reservation.Completed {
		var replay []Allocation
		if err = json.Unmarshal(reservation.ResponseBody, &replay); err != nil {
			return nil, err
		}
		return replay, nil
	}
	var lockedPaymentID string
	if err = tx.QueryRow(ctx, `SELECT id FROM finance_payments WHERE company_id=$1 AND id=$2 FOR UPDATE`, session.CurrentCompanyID, paymentID).Scan(&lockedPaymentID); errors.Is(err, pgx.ErrNoRows) {
		return nil, identity.ErrForbidden
	} else if err != nil {
		return nil, err
	}
	lockedPayment, err := getPayment(ctx, tx, session.CurrentCompanyID, lockedPaymentID)
	if err != nil {
		return nil, err
	}
	if err = ensurePaymentAccountAccess(ctx, tx, session.CurrentCompanyID, session.User.ID, lockedPayment); err != nil {
		return nil, err
	}
	if lockedPayment.Status == "REVERSED" || lockedPayment.ReversalOfID != nil {
		return nil, domainError(ErrInvalidPaymentState, "ters kaydedilmiş işlem tahsis edilemez")
	}
	items, err := s.applyAllocationsTx(ctx, tx, session, paymentID, inputs, meta)
	if err != nil {
		return nil, err
	}
	if err = writeAuditAndEventTx(ctx, tx, session, "FINANCE_PAYMENT_ALLOCATED", "finance.payment.allocated", "finance_payment", paymentID, meta, map[string]any{"payment_id": paymentID, "allocation_count": len(items)}); err != nil {
		return nil, err
	}
	if err = idempotency.CompleteTx(ctx, tx, session.CurrentCompanyID, meta.IdempotencyKey, 200, items); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return items, nil
}

// AllocatePaymentFIFO distributes a posted tahsilat/ödeme's still-unapplied
// amount across the party's oldest open invoices (FIFO), inside one serializable
// transaction that locks the open item rows. It is the post-hoc counterpart of
// PaymentInput.AutoAllocate and never moves an existing allocation: it only
// consumes the advance that is currently sitting on the party ledger.
func (s *Service) AllocatePaymentFIFO(ctx context.Context, session identity.Session, paymentID string, meta identity.RequestMeta) ([]Allocation, error) {
	if !can(session, "finance.allocation.manage") {
		return nil, identity.ErrForbidden
	}
	if strings.TrimSpace(meta.IdempotencyKey) == "" {
		return nil, fmt.Errorf("%w: idempotency anahtarı gereklidir", identity.ErrValidation)
	}
	paymentUUID, paymentErr := uuid.Parse(strings.TrimSpace(paymentID))
	if paymentErr != nil {
		return nil, fmt.Errorf("%w: ödeme kimliği geçersiz", identity.ErrValidation)
	}
	paymentID = paymentUUID.String()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	payload, _ := json.Marshal(struct {
		PaymentID string `json:"payment_id"`
		Strategy  string `json:"strategy"`
	}{paymentID, "FIFO"})
	reservation, err := idempotency.ReserveTx(ctx, tx, session.CurrentCompanyID, meta.IdempotencyKey, "finance.payment.allocate_fifo", payload, session.User.ID, meta.TraceID)
	if err != nil {
		return nil, err
	}
	if reservation.Completed {
		var replay []Allocation
		if err = json.Unmarshal(reservation.ResponseBody, &replay); err != nil {
			return nil, err
		}
		return replay, nil
	}
	var lockedPaymentID string
	if err = tx.QueryRow(ctx, `SELECT id FROM finance_payments WHERE company_id=$1 AND id=$2 FOR UPDATE`, session.CurrentCompanyID, paymentID).Scan(&lockedPaymentID); errors.Is(err, pgx.ErrNoRows) {
		return nil, identity.ErrForbidden
	} else if err != nil {
		return nil, err
	}
	lockedPayment, err := getPayment(ctx, tx, session.CurrentCompanyID, lockedPaymentID)
	if err != nil {
		return nil, err
	}
	if err = ensurePaymentAccountAccess(ctx, tx, session.CurrentCompanyID, session.User.ID, lockedPayment); err != nil {
		return nil, err
	}
	if lockedPayment.Status == "REVERSED" || lockedPayment.ReversalOfID != nil {
		return nil, domainError(ErrInvalidPaymentState, "ters kaydedilmiş işlem tahsis edilemez")
	}
	var usedText string
	if err = tx.QueryRow(ctx, `SELECT COALESCE(SUM(CASE WHEN reversal_of_id IS NULL THEN amount ELSE -amount END),0)::text FROM finance_payment_allocations WHERE company_id=$1 AND payment_id=$2`, session.CurrentCompanyID, paymentID).Scan(&usedText); err != nil {
		return nil, err
	}
	unapplied := new(big.Rat).Sub(mustRat(lockedPayment.Amount), mustRat(usedText))
	if unapplied.Sign() <= 0 {
		return nil, domainError(ErrInvalidPaymentState, "dağıtılacak avans tutarı yok")
	}
	inputs, err := fifoOpenItemAllocationsTx(ctx, tx, session.CurrentCompanyID, lockedPayment.PartyID, lockedPayment.Currency, lockedPayment.PaymentKind, unapplied)
	if err != nil {
		return nil, err
	}
	if len(inputs) == 0 {
		return nil, domainError(ErrPaymentAllocationExceedsOpenAmount, "bu cari ve para biriminde açık fatura yok")
	}
	items, err := s.applyAllocationsTx(ctx, tx, session, paymentID, inputs, meta)
	if err != nil {
		return nil, err
	}
	if err = writeAuditAndEventTx(ctx, tx, session, "FINANCE_PAYMENT_ALLOCATED", "finance.payment.allocated", "finance_payment", paymentID, meta, map[string]any{"payment_id": paymentID, "allocation_count": len(items), "strategy": "FIFO"}); err != nil {
		return nil, err
	}
	if err = idempotency.CompleteTx(ctx, tx, session.CurrentCompanyID, meta.IdempotencyKey, 200, items); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return items, nil
}

// UnallocatePayment appends immutable reversal rows for the given allocations,
// releasing the linked invoice open items without deleting history. The reversal
// rows stay on the original payment so its unapplied amount grows back and the
// freed capacity can be re-allocated with AllocatePayment. Reallocation is the
// caller's two-step flow (unallocate then allocate), never a silent move.
func (s *Service) UnallocatePayment(ctx context.Context, session identity.Session, paymentID string, allocationIDs []string, meta identity.RequestMeta) ([]Allocation, error) {
	if !can(session, "finance.allocation.manage") {
		return nil, identity.ErrForbidden
	}
	cleaned := make([]string, 0, len(allocationIDs))
	seen := map[string]bool{}
	for _, id := range allocationIDs {
		parsedID, parseErr := uuid.Parse(strings.TrimSpace(id))
		if parseErr != nil {
			return nil, fmt.Errorf("%w: tahsis kimliği geçersiz", identity.ErrValidation)
		}
		id = parsedID.String()
		if !seen[id] {
			seen[id] = true
			cleaned = append(cleaned, id)
		}
	}
	if len(cleaned) == 0 {
		return nil, fmt.Errorf("%w: geri alınacak tahsis seçilmedi", identity.ErrValidation)
	}
	paymentUUID, paymentErr := uuid.Parse(strings.TrimSpace(paymentID))
	if paymentErr != nil {
		return nil, fmt.Errorf("%w: ödeme kimliği geçersiz", identity.ErrValidation)
	}
	paymentID = paymentUUID.String()
	sort.Strings(cleaned)
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	payload, _ := json.Marshal(struct {
		PaymentID     string   `json:"payment_id"`
		AllocationIDs []string `json:"allocation_ids"`
	}{paymentID, cleaned})
	reservation, err := idempotency.ReserveTx(ctx, tx, session.CurrentCompanyID, meta.IdempotencyKey, "finance.payment.unallocate", payload, session.User.ID, meta.TraceID)
	if err != nil {
		return nil, err
	}
	if reservation.Completed {
		var replay []Allocation
		if err = json.Unmarshal(reservation.ResponseBody, &replay); err != nil {
			return nil, err
		}
		return replay, nil
	}
	var lockedPaymentID string
	if err = tx.QueryRow(ctx, `SELECT id FROM finance_payments WHERE company_id=$1 AND id=$2 FOR UPDATE`, session.CurrentCompanyID, paymentID).Scan(&lockedPaymentID); errors.Is(err, pgx.ErrNoRows) {
		return nil, identity.ErrForbidden
	} else if err != nil {
		return nil, err
	}
	lockedPayment, err := getPayment(ctx, tx, session.CurrentCompanyID, lockedPaymentID)
	if err != nil {
		return nil, err
	}
	if err = ensurePaymentAccountAccess(ctx, tx, session.CurrentCompanyID, session.User.ID, lockedPayment); err != nil {
		return nil, err
	}
	if lockedPayment.Status == "REVERSED" || lockedPayment.ReversalOfID != nil {
		return nil, domainError(ErrInvalidPaymentState, "ters kaydedilmiş işlemin tahsisi geri alınamaz")
	}
	result := make([]Allocation, 0, len(cleaned))
	for _, allocationID := range cleaned {
		var existingID string
		err = tx.QueryRow(ctx, `SELECT id FROM finance_payment_allocations WHERE company_id=$1 AND id=$2 AND payment_id=$3 AND reversal_of_id IS NULL FOR UPDATE`, session.CurrentCompanyID, allocationID, paymentID).Scan(&existingID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domainError(ErrInvalidPaymentState, "tahsis bulunamadı veya zaten ters kayıt")
		}
		if err != nil {
			return nil, err
		}
		var already bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM finance_payment_allocations WHERE company_id=$1 AND reversal_of_id=$2)`, session.CurrentCompanyID, allocationID).Scan(&already); err != nil {
			return nil, err
		}
		if already {
			return nil, domainError(ErrAlreadyReversed, "tahsis zaten geri alınmış")
		}
		var created Allocation
		created.ID = uuid.NewString()
		key := "unallocate:" + meta.IdempotencyKey + ":" + allocationID
		if err = tx.QueryRow(ctx, `INSERT INTO finance_payment_allocations(id,company_id,payment_id,party_id,target_type,target_id,open_item_id,currency,amount,idempotency_key,reversal_of_id,actor_user_id,snapshot)
			SELECT $1,company_id,payment_id,party_id,target_type,target_id,open_item_id,currency,amount,$2,$3,$4,snapshot
			FROM finance_payment_allocations WHERE company_id=$5 AND id=$3 RETURNING id,payment_id,party_id,COALESCE(open_item_id::text,''),target_type,target_id,currency,amount::text,reversal_of_id,allocated_at`,
			created.ID, key, allocationID, nullableUUID(session.User.ID), session.CurrentCompanyID).Scan(&created.ID, &created.PaymentID, &created.PartyID, &created.OpenItemID, &created.TargetType, &created.TargetID, &created.Currency, &created.Amount, &created.ReversalOfID, &created.AllocatedAt); err != nil {
			return nil, mapFinanceConstraint(err)
		}
		result = append(result, created)
	}
	if err = writeAuditAndEventTx(ctx, tx, session, "FINANCE_PAYMENT_UNALLOCATED", "finance.payment.unallocated", "finance_payment", paymentID, meta, map[string]any{"payment_id": paymentID, "allocation_count": len(result)}); err != nil {
		return nil, err
	}
	if err = idempotency.CompleteTx(ctx, tx, session.CurrentCompanyID, meta.IdempotencyKey, 200, result); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Service) ReversePayment(ctx context.Context, session identity.Session, paymentID, idempotencyKey, reason string, transactionDate time.Time, meta identity.RequestMeta) (Payment, error) {
	paymentUUID, paymentErr := uuid.Parse(strings.TrimSpace(paymentID))
	idempotencyKey, reason = strings.TrimSpace(idempotencyKey), strings.TrimSpace(reason)
	if paymentErr != nil || idempotencyKey == "" || reason == "" {
		return Payment{}, fmt.Errorf("%w: ters kayıt anahtarı ve gerekçesi gereklidir", identity.ErrValidation)
	}
	paymentID = paymentUUID.String()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Payment{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var lockedPaymentID string
	if err = tx.QueryRow(ctx, `SELECT id FROM finance_payments WHERE company_id=$1 AND id=$2 FOR UPDATE`, session.CurrentCompanyID, paymentID).Scan(&lockedPaymentID); errors.Is(err, pgx.ErrNoRows) {
		return Payment{}, identity.ErrForbidden
	}
	if err != nil {
		return Payment{}, err
	}
	original, err := getPayment(ctx, tx, session.CurrentCompanyID, lockedPaymentID)
	if err != nil {
		return Payment{}, err
	}
	// Keep payment reversals in the same party serialization lane as new
	// payments, invoices, and risk checks.  This prevents a risk snapshot from
	// racing an append-only reversal for the same cari.
	var lockedPartyID string
	if err = tx.QueryRow(ctx, `SELECT id FROM parties WHERE company_id=$1 AND id=$2 FOR UPDATE`, session.CurrentCompanyID, original.PartyID).Scan(&lockedPartyID); err != nil {
		return Payment{}, err
	}
	permission := "finance.payment.reverse"
	if original.PaymentKind == "COLLECTION" {
		permission = "finance.collection.reverse"
	}
	if !can(session, permission) {
		return Payment{}, identity.ErrForbidden
	}
	if err = ensurePaymentAccountAccess(ctx, tx, session.CurrentCompanyID, session.User.ID, original); err != nil {
		return Payment{}, err
	}
	// A retry with the same reversal key returns the already-posted reversal;
	// a key bound to another payment is a payload conflict.
	if existing, existingErr := getPaymentByKey(ctx, tx, session.CurrentCompanyID, idempotencyKey); existingErr == nil {
		if existing.ReversalOfID != nil && *existing.ReversalOfID == paymentID && existing.Description == "Ters kayıt: "+reason {
			return existing, nil
		}
		return Payment{}, domainError(ErrIdempotencyConflict, "aynı ters kayıt anahtarı farklı ödeme verisiyle kullanıldı")
	} else if !errors.Is(existingErr, pgx.ErrNoRows) {
		return Payment{}, existingErr
	}
	if original.ReversalOfID != nil {
		return Payment{}, domainError(ErrInvalidPaymentState, "ters kayıt yeniden terslenemez")
	}
	var already bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM finance_payments WHERE company_id=$1 AND reversal_of_id=$2)`, session.CurrentCompanyID, paymentID).Scan(&already); err != nil {
		return Payment{}, err
	}
	if already {
		return Payment{}, domainError(ErrAlreadyReversed, "ödeme zaten ters kaydedilmiş")
	}
	if transactionDate.IsZero() {
		transactionDate = s.now()
	}
	if transactionDate.Format("2006-01-02") < original.TransactionDate.Format("2006-01-02") {
		return Payment{}, fmt.Errorf("%w: ters kayıt tarihi özgün ödemeden önce olamaz", identity.ErrValidation)
	}
	if err = ensureFinanceDate(ctx, tx, session.CurrentCompanyID, transactionDate, s.now()); err != nil {
		return Payment{}, err
	}
	if err = ensurePeriodOpen(ctx, tx, session.CurrentCompanyID, transactionDate); err != nil {
		return Payment{}, err
	}
	reversalID, ledgerID := uuid.NewString(), uuid.NewString()
	documentPrefix := "ODM"
	if original.PaymentKind == "COLLECTION" {
		documentPrefix = "THS"
	}
	reversalDocumentNo, err := nextPaymentNumberTx(ctx, tx, session.CurrentCompanyID, documentPrefix, transactionDate)
	if err != nil {
		return Payment{}, err
	}
	var movementID *string
	if original.MovementID != nil {
		var accountID, direction string
		var branchID *string
		if err = tx.QueryRow(ctx, `SELECT account_id,direction,branch_id FROM finance_account_movements m JOIN finance_accounts a ON a.company_id=m.company_id AND a.id=m.account_id WHERE m.company_id=$1 AND m.id=$2 FOR UPDATE OF a`, session.CurrentCompanyID, *original.MovementID).Scan(&accountID, &direction, &branchID); err != nil {
			return Payment{}, err
		}
		if err = ensureFinanceAccountAccess(ctx, tx, session, accountID, branchID); err != nil {
			return Payment{}, err
		}
		if direction == "IN" {
			if err = enforceNegativeBalanceTx(ctx, tx, session, accountID, mustRat(original.Amount), "payment reversal: "+strings.TrimSpace(reason)); err != nil {
				return Payment{}, err
			}
		}
		mv := uuid.NewString()
		movementID = &mv
		if _, err = tx.Exec(ctx, `INSERT INTO finance_account_movements(id,company_id,account_id,movement_kind,direction,currency,amount,transaction_date,source_type,source_id,idempotency_key,description,reversal_of_id,actor_user_id,snapshot,exchange_rate,base_currency,base_amount) SELECT $1,company_id,account_id,'REVERSAL',CASE WHEN direction='IN' THEN 'OUT' ELSE 'IN' END,currency,amount,$2,'finance_payment',$3,$4,$5,$6,$7,snapshot,exchange_rate,base_currency,base_amount FROM finance_account_movements WHERE company_id=$8 AND id=$9`, mv, transactionDate, reversalID, "payment:"+idempotencyKey+":movement", "Ters kayıt: "+reason, *original.MovementID, nullableUUID(session.User.ID), session.CurrentCompanyID, *original.MovementID); err != nil {
			return Payment{}, mapFinanceConstraint(err)
		}
	}
	// A reversal must negate the original party-ledger side. Collections
	// originally credit the receivable; their reversal debits it back. Payments
	// originally debit the payable; their reversal credits it back.
	debit, credit := reversalPartyLedgerAmounts(original.PaymentKind, original.Amount)
	snapshot := jsonBytes(map[string]any{"reversal_of": paymentID, "reason": reason, "transaction_currency": original.Currency, "amount": original.Amount, "exchange_rate": original.ExchangeRate})
	if _, err = tx.Exec(ctx, `INSERT INTO party_ledger_entries(id,company_id,party_id,currency,entry_type,source_type,source_id,idempotency_key,description,debit,credit,exchange_rate,document_date,reversal_of_id,actor_user_id,snapshot) VALUES($1,$2,$3,$4,'REVERSAL','finance_payment',$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`, ledgerID, session.CurrentCompanyID, original.PartyID, original.Currency, reversalID, "payment:"+idempotencyKey+":party", "Ters kayıt: "+reason, debit, credit, original.ExchangeRate, transactionDate, original.PartyLedgerEntryID, nullableUUID(session.User.ID), snapshot); err != nil {
		return Payment{}, mapFinanceConstraint(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO finance_payments(id,company_id,party_id,account_id,movement_id,party_ledger_entry_id,payment_kind,movement_direction,currency,amount,document_no,description,transaction_date,idempotency_key,reversal_of_id,actor_user_id,snapshot,payment_method,exchange_rate,base_currency,base_amount,instrument_id) VALUES($1,$2,$3,$4,$5,$6,$7,CASE WHEN $7='COLLECTION' THEN 'OUT' ELSE 'IN' END,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)`, reversalID, session.CurrentCompanyID, original.PartyID, original.AccountID, movementID, ledgerID, original.PaymentKind, original.Currency, original.Amount, reversalDocumentNo, "Ters kayıt: "+reason, transactionDate, idempotencyKey, paymentID, nullableUUID(session.User.ID), snapshot, original.PaymentMethod, original.ExchangeRate, original.BaseCurrency, original.BaseAmount, original.InstrumentID); err != nil {
		return Payment{}, mapFinanceConstraint(err)
	}
	// Allocation reversals are immutable rows on the reversal payment. They
	// release each invoice open item without deleting the original allocation.
	rows, qerr := tx.Query(ctx, `SELECT a.id,a.open_item_id,a.target_id,a.currency,a.amount
		FROM finance_payment_allocations a
		WHERE a.company_id=$1 AND a.payment_id=$2 AND a.reversal_of_id IS NULL
		  AND NOT EXISTS (SELECT 1 FROM finance_payment_allocations r
			WHERE r.company_id=a.company_id AND r.reversal_of_id=a.id)`, session.CurrentCompanyID, paymentID)
	if qerr != nil {
		return Payment{}, qerr
	}
	allocationIDs := make([]string, 0)
	for rows.Next() {
		var allocationID, targetID, currency, allocationAmount string
		var openItemID *string
		if err = rows.Scan(&allocationID, &openItemID, &targetID, &currency, &allocationAmount); err != nil {
			rows.Close()
			return Payment{}, err
		}
		allocationIDs = append(allocationIDs, allocationID)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return Payment{}, err
	}
	for _, allocationID := range allocationIDs {
		if _, err = tx.Exec(ctx, `INSERT INTO finance_payment_allocations(id,company_id,payment_id,party_id,target_type,target_id,open_item_id,currency,amount,idempotency_key,reversal_of_id,actor_user_id,snapshot) SELECT $1,company_id,$2,party_id,target_type,target_id,open_item_id,currency,amount,$3,$4,$5,$6 FROM finance_payment_allocations WHERE company_id=$7 AND id=$8`, uuid.NewString(), reversalID, "allocation:"+idempotencyKey+":"+allocationID, allocationID, nullableUUID(session.User.ID), snapshot, session.CurrentCompanyID, allocationID); err != nil {
			return Payment{}, mapFinanceConstraint(err)
		}
	}
	if original.InstrumentID != nil {
		// Instrument rows are immutable; the reversal is represented by an
		// append-only event rather than mutating the instrument snapshot.
		if _, err = tx.Exec(ctx, `INSERT INTO finance_instrument_events(id,company_id,instrument_id,event_type,event_date,description,source_type,source_id,actor_user_id) VALUES($1,$2,$3,'CANCELLED',$4,$5,'finance_payment',$6,$7)`, uuid.NewString(), session.CurrentCompanyID, *original.InstrumentID, transactionDate, "Ters kayıt: "+reason, reversalID, nullableUUID(session.User.ID)); err != nil {
			return Payment{}, mapFinanceConstraint(err)
		}
	}
	if err = writeAuditAndEventTx(ctx, tx, session, "FINANCE_PAYMENT_REVERSED", "finance.payment.reversed", "finance_payment", reversalID, meta, map[string]any{"payment_id": reversalID, "reversal_of_id": paymentID, "reason": reason}); err != nil {
		return Payment{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Payment{}, err
	}
	return s.loadPaymentAfterMutation(ctx, session, reversalID)
}

func reversalPartyLedgerAmounts(paymentKind, amount string) (debit, credit string) {
	if paymentKind == "COLLECTION" {
		return amount, "0"
	}
	return "0", amount
}

func (s *Service) PostManualEntry(ctx context.Context, session identity.Session, input ManualEntryInput, meta identity.RequestMeta) (ManualEntry, error) {
	if !can(session, "finance.manual.post") {
		return ManualEntry{}, identity.ErrForbidden
	}
	input.EntryKind, input.Currency, input.IdempotencyKey = strings.ToUpper(strings.TrimSpace(input.EntryKind)), strings.ToUpper(strings.TrimSpace(input.Currency)), strings.TrimSpace(input.IdempotencyKey)
	input.PartyID = strings.TrimSpace(input.PartyID)
	input.ReferenceNo, input.DocumentNo, input.Description = strings.TrimSpace(input.ReferenceNo), strings.TrimSpace(input.DocumentNo), strings.TrimSpace(input.Description)
	partyUUID, partyErr := uuid.Parse(input.PartyID)
	if partyErr != nil || !contains([]string{"DEBIT", "CREDIT"}, input.EntryKind) || input.Currency == "" || input.IdempotencyKey == "" || input.Description == "" || input.TransactionDate.IsZero() {
		return ManualEntry{}, fmt.Errorf("%w: manuel hareket alanları eksik", identity.ErrValidation)
	}
	input.PartyID = partyUUID.String()
	if len(input.Currency) != 3 || strings.Trim(input.Currency, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "" {
		return ManualEntry{}, fmt.Errorf("%w: işlem para birimi geçersiz", identity.ErrValidation)
	}
	if input.ReversalOfID != "" {
		reversalUUID, reversalErr := uuid.Parse(strings.TrimSpace(input.ReversalOfID))
		if reversalErr != nil {
			return ManualEntry{}, fmt.Errorf("%w: ters kayıt kimliği geçersiz", identity.ErrValidation)
		}
		input.ReversalOfID = reversalUUID.String()
	}
	if input.DueDate != nil && input.DueDate.Format("2006-01-02") < input.TransactionDate.Format("2006-01-02") {
		return ManualEntry{}, fmt.Errorf("%w: vade tarihi işlem tarihinden önce olamaz", identity.ErrValidation)
	}
	amount, err := parsePositive(input.Amount, 4)
	if err != nil {
		return ManualEntry{}, fmt.Errorf("%w: tutar geçersiz", identity.ErrValidation)
	}
	rateProvided := strings.TrimSpace(input.ExchangeRate) != ""
	rate, err := parsePositiveDefault(input.ExchangeRate, 10)
	if err != nil {
		return ManualEntry{}, fmt.Errorf("%w: kur geçersiz", identity.ErrValidation)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return ManualEntry{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var existing ManualEntry
	var reversal *string
	var existingBaseCurrency *string
	err = tx.QueryRow(ctx, `SELECT id,party_id,party_ledger_entry_id,entry_kind,currency,amount::text,exchange_rate::text,base_currency,description,transaction_date,due_date,reference_no,document_no,reversal_of_id FROM finance_manual_entries WHERE company_id=$1 AND idempotency_key=$2`, session.CurrentCompanyID, input.IdempotencyKey).Scan(&existing.ID, &existing.PartyID, &existing.PartyLedgerEntryID, &existing.EntryKind, &existing.Currency, &existing.Amount, &existing.ExchangeRate, &existingBaseCurrency, &existing.Description, &existing.TransactionDate, &existing.DueDate, &existing.ReferenceNo, &existing.DocumentNo, &reversal)
	if err == nil {
		if !rateProvided && input.ReversalOfID == "" {
			baseCurrency := ""
			if existingBaseCurrency != nil {
				baseCurrency = *existingBaseCurrency
			} else if baseErr := tx.QueryRow(ctx, `SELECT base_currency FROM companies WHERE id=$1`, session.CurrentCompanyID).Scan(&baseCurrency); baseErr != nil {
				return ManualEntry{}, baseErr
			}
			if existing.Currency != baseCurrency {
				return ManualEntry{}, domainError(ErrExchangeRateRequired, "yabancı para manuel hareketinin tekrarı için kur gereklidir")
			}
		}
		if existing.PartyID != input.PartyID || existing.EntryKind != input.EntryKind || existing.Currency != input.Currency || existing.Amount != amountString(amount, 4) || (rateProvided && existing.ExchangeRate != amountString(rate, 10)) || existing.Description != input.Description || existing.TransactionDate.Format("2006-01-02") != input.TransactionDate.Format("2006-01-02") || existing.ReferenceNo != input.ReferenceNo || (input.DocumentNo != "" && existing.DocumentNo != input.DocumentNo) || !sameDate(existing.DueDate, input.DueDate) || (reversal == nil) != (input.ReversalOfID == "") || reversal != nil && *reversal != input.ReversalOfID {
			return ManualEntry{}, domainError(ErrIdempotencyConflict, "aynı manuel hareket anahtarı farklı veriyle kullanıldı")
		}
		existing.ReversalOfID = reversal
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ManualEntry{}, err
	}
	if err = ensurePeriodOpen(ctx, tx, session.CurrentCompanyID, input.TransactionDate); err != nil {
		return ManualEntry{}, err
	}
	if err = ensureFinanceDate(ctx, tx, session.CurrentCompanyID, input.TransactionDate, s.now()); err != nil {
		return ManualEntry{}, err
	}
	id, ledgerID := uuid.NewString(), uuid.NewString()
	debit, credit := "0", amountString(amount, 4)
	if input.EntryKind == "DEBIT" {
		debit, credit = amountString(amount, 4), "0"
	}
	var partyCurrency, baseCurrency string
	if err = tx.QueryRow(ctx, `SELECT p.default_currency,c.base_currency FROM parties p JOIN companies c ON c.id=p.company_id WHERE p.company_id=$1 AND p.id=$2 AND p.is_active FOR UPDATE`, session.CurrentCompanyID, input.PartyID).Scan(&partyCurrency, &baseCurrency); err != nil {
		return ManualEntry{}, identity.ErrForbidden
	}
	var originalManualID, originalLedgerID string
	if input.ReversalOfID != "" {
		var originalParty, originalKind, originalCurrency, originalAmount, originalRate string
		var originalDate time.Time
		var originalReversalID *string
		if err = tx.QueryRow(ctx, `SELECT id,party_id,party_ledger_entry_id,entry_kind,currency,amount::text,exchange_rate::text,transaction_date,reversal_of_id FROM finance_manual_entries WHERE company_id=$1 AND id=$2 FOR SHARE`, session.CurrentCompanyID, input.ReversalOfID).Scan(&originalManualID, &originalParty, &originalLedgerID, &originalKind, &originalCurrency, &originalAmount, &originalRate, &originalDate, &originalReversalID); errors.Is(err, pgx.ErrNoRows) {
			return ManualEntry{}, identity.ErrForbidden
		} else if err != nil {
			return ManualEntry{}, err
		}
		if originalReversalID != nil {
			return ManualEntry{}, domainError(ErrAlreadyReversed, "manuel hareket daha önce ters kaydedilmiş")
		}
		if originalParty != input.PartyID || originalCurrency != input.Currency || originalAmount != amountString(amount, 4) {
			return ManualEntry{}, domainError(ErrCurrencyMismatch, "manuel ters kayıt özgün hareketle eşleşmiyor")
		}
		if input.TransactionDate.Format("2006-01-02") < originalDate.Format("2006-01-02") {
			return ManualEntry{}, fmt.Errorf("%w: ters kayıt tarihi özgün hareketten önce olamaz", identity.ErrValidation)
		}
		originalRateValue, parseErr := parsePositive(originalRate, 10)
		if parseErr != nil {
			return ManualEntry{}, fmt.Errorf("%w: özgün ters kayıt kuru geçersiz", identity.ErrValidation)
		}
		if rateProvided && rate.Cmp(originalRateValue) != 0 {
			return ManualEntry{}, fmt.Errorf("%w: ters kayıt kuru özgün hareketle eşleşmiyor", identity.ErrValidation)
		}
		rate = originalRateValue
		if input.EntryKind == originalKind {
			return ManualEntry{}, fmt.Errorf("%w: manuel ters kayıt karşıt yönde olmalıdır", identity.ErrValidation)
		}
	}
	if input.Currency == baseCurrency {
		if rateProvided && input.ReversalOfID == "" && rate.Cmp(big.NewRat(1, 1)) != 0 {
			return ManualEntry{}, fmt.Errorf("%w: şirket para biriminde kur 1 olmalıdır", identity.ErrValidation)
		}
		if input.ReversalOfID == "" {
			rate = big.NewRat(1, 1)
		}
	} else if !rateProvided && input.ReversalOfID == "" {
		return ManualEntry{}, domainError(ErrExchangeRateRequired, "yabancı para manuel hareketinde kur gereklidir")
	} else if input.ReversalOfID == "" {
		if err = ensureExchangeRateWithinTolerance(ctx, tx, session.CurrentCompanyID, input.Currency, input.TransactionDate, rate); err != nil {
			return ManualEntry{}, err
		}
	}
	if amountString(new(big.Rat).Mul(amount, rate), 4) == "0.0000" {
		return ManualEntry{}, fmt.Errorf("%w: temel para birimine çevrilen tutar dört ondalıkta sıfıra yuvarlanamaz", identity.ErrValidation)
	}
	if input.DocumentNo == "" {
		input.DocumentNo, err = nextPaymentNumberTx(ctx, tx, session.CurrentCompanyID, "CH", input.TransactionDate)
		if err != nil {
			return ManualEntry{}, err
		}
	}
	snapshotValues := map[string]any{"transaction_currency": input.Currency, "amount": amountString(amount, 4), "base_currency": baseCurrency, "exchange_rate": amountString(rate, 10), "document_no": input.DocumentNo, "reference_no": input.ReferenceNo}
	if input.DueDate != nil {
		snapshotValues["due_date"] = input.DueDate.Format("2006-01-02")
	}
	snapshot := jsonBytes(snapshotValues)
	if _, err = tx.Exec(ctx, `INSERT INTO party_ledger_entries(id,company_id,party_id,currency,entry_type,source_type,source_id,idempotency_key,description,debit,credit,exchange_rate,document_date,reversal_of_id,actor_user_id,snapshot) VALUES($1,$2,$3,$4,'MANUAL_ENTRY','finance_manual_entry',$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`, ledgerID, session.CurrentCompanyID, input.PartyID, input.Currency, id, "manual:"+input.IdempotencyKey, input.Description, debit, credit, amountString(rate, 10), input.TransactionDate, nullableUUID(originalLedgerID), nullableUUID(session.User.ID), snapshot); err != nil {
		return ManualEntry{}, mapFinanceConstraint(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO finance_manual_entries(id,company_id,party_id,party_ledger_entry_id,entry_kind,currency,amount,exchange_rate,base_currency,base_amount,description,transaction_date,due_date,document_no,reference_no,idempotency_key,reversal_of_id,actor_user_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,ROUND($7::numeric*$8::numeric,4),$10,$11,$12,$13,$14,$15,NULLIF($16,'')::uuid,$17)`, id, session.CurrentCompanyID, input.PartyID, ledgerID, input.EntryKind, input.Currency, amountString(amount, 4), amountString(rate, 10), baseCurrency, input.Description, input.TransactionDate, input.DueDate, input.DocumentNo, input.ReferenceNo, input.IdempotencyKey, input.ReversalOfID, nullableUUID(session.User.ID)); err != nil {
		return ManualEntry{}, mapFinanceConstraint(err)
	}
	if err = writeAuditAndEventTx(ctx, tx, session, "FINANCE_MANUAL_ENTRY_POSTED", "finance.manual_entry.posted", "finance_manual_entry", id, meta, nil); err != nil {
		return ManualEntry{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return ManualEntry{}, err
	}
	var reversalOf *string
	if originalManualID != "" {
		reversalOf = &originalManualID
	}
	return ManualEntry{ID: id, PartyID: input.PartyID, PartyLedgerEntryID: ledgerID, EntryKind: input.EntryKind, Currency: input.Currency, Amount: amountString(amount, 4), ExchangeRate: amountString(rate, 10), Description: input.Description, TransactionDate: input.TransactionDate, DueDate: input.DueDate, ReferenceNo: input.ReferenceNo, DocumentNo: input.DocumentNo, ReversalOfID: reversalOf}, nil
}

func (s *Service) PostPartyTransfer(ctx context.Context, session identity.Session, input PartyTransferInput, meta identity.RequestMeta) (PartyTransfer, error) {
	if !can(session, "finance.transfer.post") {
		return PartyTransfer{}, identity.ErrForbidden
	}
	fromUUID, fromErr := uuid.Parse(strings.TrimSpace(input.FromPartyID))
	toUUID, toErr := uuid.Parse(strings.TrimSpace(input.ToPartyID))
	input.Currency, input.IdempotencyKey, input.Description = strings.ToUpper(strings.TrimSpace(input.Currency)), strings.TrimSpace(input.IdempotencyKey), strings.TrimSpace(input.Description)
	if fromErr != nil || toErr != nil || fromUUID == toUUID || input.Currency == "" || input.IdempotencyKey == "" || input.Description == "" || input.TransactionDate.IsZero() {
		return PartyTransfer{}, fmt.Errorf("%w: virman tarafları ve alanları geçersiz", identity.ErrValidation)
	}
	input.FromPartyID, input.ToPartyID = fromUUID.String(), toUUID.String()
	if len(input.Currency) != 3 || strings.Trim(input.Currency, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "" {
		return PartyTransfer{}, fmt.Errorf("%w: işlem para birimi geçersiz", identity.ErrValidation)
	}
	amount, err := parsePositive(input.Amount, 4)
	if err != nil {
		return PartyTransfer{}, fmt.Errorf("%w: tutar geçersiz", identity.ErrValidation)
	}
	rateProvided := strings.TrimSpace(input.ExchangeRate) != ""
	rate, err := parsePositiveDefault(input.ExchangeRate, 10)
	if err != nil {
		return PartyTransfer{}, fmt.Errorf("%w: kur geçersiz", identity.ErrValidation)
	}
	amountText, rateText := amountString(amount, 4), amountString(rate, 10)
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return PartyTransfer{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var companyBaseCurrency string
	if err = tx.QueryRow(ctx, `SELECT base_currency FROM companies WHERE id=$1 FOR SHARE`, session.CurrentCompanyID).Scan(&companyBaseCurrency); err != nil {
		return PartyTransfer{}, err
	}
	var existing PartyTransfer
	var existingDescription string
	var existingDate time.Time
	err = tx.QueryRow(ctx, `SELECT t.id,t.from_party_id,t.to_party_id,t.debit_ledger_entry_id,t.credit_ledger_entry_id,t.currency,t.amount::text, t.description,t.transaction_date,l.exchange_rate::text FROM finance_party_transfers t JOIN party_ledger_entries l ON l.company_id=t.company_id AND l.id=t.debit_ledger_entry_id WHERE t.company_id=$1 AND t.idempotency_key=$2`, session.CurrentCompanyID, input.IdempotencyKey).Scan(&existing.ID, &existing.FromPartyID, &existing.ToPartyID, &existing.DebitLedgerEntryID, &existing.CreditLedgerEntryID, &existing.Currency, &existing.Amount, &existingDescription, &existingDate, &existing.ExchangeRate)
	if err == nil {
		if !rateProvided && existing.Currency != companyBaseCurrency {
			return PartyTransfer{}, domainError(ErrExchangeRateRequired, "yabancı para virmanının tekrarı için kur gereklidir")
		}
		if existing.FromPartyID != input.FromPartyID || existing.ToPartyID != input.ToPartyID || existing.Currency != input.Currency || existing.Amount != amountText || existingDescription != input.Description || existingDate.Format("2006-01-02") != input.TransactionDate.Format("2006-01-02") || existing.ExchangeRate != rateText {
			return PartyTransfer{}, domainError(ErrIdempotencyConflict, "aynı virman anahtarı farklı veriyle kullanıldı")
		}
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return PartyTransfer{}, err
	}
	if err = ensurePeriodOpen(ctx, tx, session.CurrentCompanyID, input.TransactionDate); err != nil {
		return PartyTransfer{}, err
	}
	if err = ensureFinanceDate(ctx, tx, session.CurrentCompanyID, input.TransactionDate, s.now()); err != nil {
		return PartyTransfer{}, err
	}
	// Lock in UUID order so opposite-direction concurrent transfers cannot
	// deadlock while checking both parties.
	first, second := input.FromPartyID, input.ToPartyID
	if first > second {
		first, second = second, first
	}
	rows, err := tx.Query(ctx, `SELECT p.id,p.default_currency,p.is_active,c.base_currency FROM parties p JOIN companies c ON c.id=p.company_id WHERE p.company_id=$1 AND p.id IN ($2,$3) ORDER BY p.id FOR UPDATE`, session.CurrentCompanyID, first, second)
	if err != nil {
		return PartyTransfer{}, err
	}
	currencies := map[string]string{}
	active := map[string]bool{}
	baseCurrency := ""
	for rows.Next() {
		var id, currency, rowBase string
		var isActive bool
		if err = rows.Scan(&id, &currency, &isActive, &rowBase); err != nil {
			rows.Close()
			return PartyTransfer{}, err
		}
		currencies[id] = currency
		active[id] = isActive
		baseCurrency = rowBase
	}
	rows.Close()
	if len(currencies) != 2 {
		return PartyTransfer{}, identity.ErrForbidden
	}
	if !active[input.FromPartyID] || !active[input.ToPartyID] {
		return PartyTransfer{}, domainError(ErrAccountInactive, "pasif cari ile virman yapılamaz")
	}
	if input.Currency == baseCurrency {
		if rateProvided && rate.Cmp(big.NewRat(1, 1)) != 0 {
			return PartyTransfer{}, fmt.Errorf("%w: şirket para biriminde kur 1 olmalıdır", identity.ErrValidation)
		}
		rate = big.NewRat(1, 1)
		rateText = amountString(rate, 10)
	} else if strings.TrimSpace(input.ExchangeRate) == "" {
		return PartyTransfer{}, domainError(ErrExchangeRateRequired, "yabancı para virmanında kur gereklidir")
	}
	if currencies[input.FromPartyID] != input.Currency || currencies[input.ToPartyID] != input.Currency {
		return PartyTransfer{}, domainError(ErrCurrencyMismatch, "virman taraflarının para birimleri eşleşmiyor")
	}
	if amountString(new(big.Rat).Mul(amount, rate), 4) == "0.0000" {
		return PartyTransfer{}, fmt.Errorf("%w: temel para birimine çevrilen tutar dört ondalıkta sıfıra yuvarlanamaz", identity.ErrValidation)
	}
	id, fromLedgerID, toLedgerID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	snapshot := jsonBytes(map[string]any{"transfer_id": id, "currency": input.Currency, "amount": amountText, "base_currency": baseCurrency, "exchange_rate": rateText})
	if _, err = tx.Exec(ctx, `INSERT INTO party_ledger_entries(id,company_id,party_id,currency,entry_type,source_type,source_id,idempotency_key,description,debit,credit,exchange_rate,document_date,actor_user_id,snapshot) VALUES($1,$2,$3,$4,'CREDIT_TRANSFER','finance_party_transfer',$5,$6,$7,0,$8,$9,$10,$11,$12)`, fromLedgerID, session.CurrentCompanyID, input.FromPartyID, input.Currency, id, "transfer:"+input.IdempotencyKey+":from", input.Description, amountText, rateText, input.TransactionDate, nullableUUID(session.User.ID), snapshot); err != nil {
		return PartyTransfer{}, mapFinanceConstraint(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO party_ledger_entries(id,company_id,party_id,currency,entry_type,source_type,source_id,idempotency_key,description,debit,credit,exchange_rate,document_date,actor_user_id,snapshot) VALUES($1,$2,$3,$4,'DEBIT_TRANSFER','finance_party_transfer',$5,$6,$7,$8,0,$9,$10,$11,$12)`, toLedgerID, session.CurrentCompanyID, input.ToPartyID, input.Currency, id, "transfer:"+input.IdempotencyKey+":to", input.Description, amountText, rateText, input.TransactionDate, nullableUUID(session.User.ID), snapshot); err != nil {
		return PartyTransfer{}, mapFinanceConstraint(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO finance_party_transfers(id,company_id,from_party_id,to_party_id,debit_ledger_entry_id,credit_ledger_entry_id,currency,amount,description,transaction_date,idempotency_key,actor_user_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, id, session.CurrentCompanyID, input.FromPartyID, input.ToPartyID, toLedgerID, fromLedgerID, input.Currency, amountText, input.Description, input.TransactionDate, input.IdempotencyKey, nullableUUID(session.User.ID)); err != nil {
		return PartyTransfer{}, mapFinanceConstraint(err)
	}
	if err = writeAuditAndEventTx(ctx, tx, session, "FINANCE_TRANSFER_POSTED", "finance.transfer.posted", "finance_party_transfer", id, meta, nil); err != nil {
		return PartyTransfer{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return PartyTransfer{}, err
	}
	return PartyTransfer{ID: id, FromPartyID: input.FromPartyID, ToPartyID: input.ToPartyID, DebitLedgerEntryID: toLedgerID, CreditLedgerEntryID: fromLedgerID, Currency: input.Currency, Amount: amountText, ExchangeRate: rateText}, nil
}

// PostInvoiceTx is the finance side of sales/purchase invoice posting. The
// caller owns the transaction and can therefore post stock and finance as one
// atomic unit without coupling the bounded contexts at the pool level.
func (s *Service) PostInvoiceTx(ctx context.Context, tx pgx.Tx, session identity.Session, input InvoicePostingInput) (InvoicePosting, error) {
	input.DocumentType = strings.ToUpper(strings.TrimSpace(input.DocumentType))
	input.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))
	input.DocumentID, input.PartyID = strings.TrimSpace(input.DocumentID), strings.TrimSpace(input.PartyID)
	input.Description, input.IdempotencyKey = strings.TrimSpace(input.Description), strings.TrimSpace(input.IdempotencyKey)
	documentUUID, documentErr := uuid.Parse(input.DocumentID)
	partyUUID, partyErr := uuid.Parse(input.PartyID)
	if tx == nil || documentErr != nil || partyErr != nil || !contains([]string{"SALES_INVOICE", "PURCHASE_INVOICE", "SALES_RETURN_INVOICE", "PURCHASE_RETURN_INVOICE"}, input.DocumentType) || input.DocumentDate.IsZero() || input.IdempotencyKey == "" || input.Description == "" {
		return InvoicePosting{}, fmt.Errorf("%w: fatura posting alanları eksik", identity.ErrValidation)
	}
	input.DocumentID, input.PartyID = documentUUID.String(), partyUUID.String()
	if input.DueDate != nil && input.DueDate.Format("2006-01-02") < input.DocumentDate.Format("2006-01-02") {
		return InvoicePosting{}, fmt.Errorf("%w: vade tarihi fatura tarihinden önce olamaz", identity.ErrValidation)
	}
	// Document lines keep eight decimal places; finance ledgers snapshot money
	// at four places, rounding HALF_UP at this bounded-context boundary.
	amount, err := parsePositive(input.Amount, 8)
	if err != nil {
		return InvoicePosting{}, fmt.Errorf("%w: fatura tutarı geçersiz", identity.ErrValidation)
	}
	// Parse a provisional rate before the idempotency lookup so a replay is
	// compared against the original immutable posting snapshot. Foreign
	// currency blank-rate rejection is applied after company currency lookup.
	rate, err := parsePositiveDefault(input.ExchangeRate, 10)
	if err != nil {
		return InvoicePosting{}, fmt.Errorf("%w: fatura kuru geçersiz", identity.ErrValidation)
	}
	var existing InvoicePosting
	var existingPartyID, existingRate, existingBaseCurrency, existingDocumentType, existingDescription string
	var existingDate time.Time
	var existingDueDate *time.Time
	err = tx.QueryRow(ctx, `SELECT p.id,p.document_id,p.party_ledger_entry_id,p.open_item_id,oi.party_id,oi.side,oi.original_amount::text,oi.currency,oi.exchange_rate::text,oi.base_currency,oi.document_date,oi.due_date,d.document_type_code,l.description
		FROM finance_invoice_postings p
		JOIN finance_invoice_open_items oi ON oi.company_id=p.company_id AND oi.id=p.open_item_id
		JOIN documents d ON d.company_id=p.company_id AND d.id=p.document_id
		JOIN party_ledger_entries l ON l.company_id=p.company_id AND l.id=p.party_ledger_entry_id
		WHERE p.company_id=$1 AND p.idempotency_key=$2`, session.CurrentCompanyID, input.IdempotencyKey).Scan(&existing.ID, &existing.DocumentID, &existing.PartyLedgerEntryID, &existing.OpenItemID, &existingPartyID, &existing.Side, &existing.Amount, &existing.Currency, &existingRate, &existingBaseCurrency, &existingDate, &existingDueDate, &existingDocumentType, &existingDescription)
	if err == nil {
		if strings.TrimSpace(input.ExchangeRate) == "" && existing.Currency != existingBaseCurrency {
			return InvoicePosting{}, domainError(ErrExchangeRateRequired, "yabancı para fatura postunun tekrarı için kur gereklidir")
		}
		if existing.DocumentID != input.DocumentID || existingPartyID != input.PartyID || existingDocumentType != input.DocumentType || existingDescription != input.Description || existing.Amount != amountString(amount, 4) || existingRate != amountString(rate, 10) || existingDate.Format("2006-01-02") != input.DocumentDate.Format("2006-01-02") || !sameDate(existingDueDate, input.DueDate) || input.Currency != "" && existing.Currency != input.Currency {
			return InvoicePosting{}, domainError(ErrIdempotencyConflict, "aynı fatura posting anahtarı farklı veriyle kullanıldı")
		}
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return InvoicePosting{}, err
	}
	// The finance projection is not allowed to invent a different invoice
	// identity from the immutable commercial document. Adapters normally pass
	// these fields from their locked header, but re-checking here closes the
	// direct-call/API edge where a mismatched amount, party, currency or date
	// could otherwise create a corrupt open item under a valid document FK.
	var storedType, storedPartyID, storedCurrency, storedAmount, storedRate, storedStatus string
	var storedDocumentDate time.Time
	var storedDueDate *time.Time
	err = tx.QueryRow(ctx, `SELECT d.document_type_code,d.party_id,d.currency_code,
		COALESCE(CASE d.document_type_code
			WHEN 'SALES_INVOICE' THEN si.payable_total
			WHEN 'SALES_RETURN_INVOICE' THEN sr.payable_total
			WHEN 'PURCHASE_INVOICE' THEN pi.payable_total
			WHEN 'PURCHASE_RETURN_INVOICE' THEN pr.total
		END,d.grand_total)::text,
		d.exchange_rate::text,d.document_date,d.due_date,d.status
		FROM documents d
		LEFT JOIN sales_invoices si ON si.company_id=d.company_id AND si.document_id=d.id
		LEFT JOIN sales_returns sr ON sr.company_id=d.company_id AND sr.document_id=d.id
		LEFT JOIN purchase_invoices pi ON pi.company_id=d.company_id AND pi.document_id=d.id
		LEFT JOIN purchase_returns pr ON pr.company_id=d.company_id AND pr.document_id=d.id
		WHERE d.company_id=$1 AND d.id=$2`, session.CurrentCompanyID, input.DocumentID).Scan(&storedType, &storedPartyID, &storedCurrency, &storedAmount, &storedRate, &storedDocumentDate, &storedDueDate, &storedStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return InvoicePosting{}, domainError(ErrInvoiceNotFound, "fatura belgesi bulunamadı")
	}
	if err != nil {
		return InvoicePosting{}, err
	}
	if storedStatus != "DRAFT" {
		return InvoicePosting{}, domainError(ErrInvoiceAlreadyPosted, "fatura taslak durumda değil")
	}
	storedAmountValue, storedAmountErr := parsePositive(storedAmount, 8)
	storedRateValue, storedRateErr := parsePositive(storedRate, 10)
	if storedAmountErr != nil || storedRateErr != nil || storedType != input.DocumentType || storedPartyID != input.PartyID || amountString(storedAmountValue, 4) != amountString(amount, 4) || storedDocumentDate.Format("2006-01-02") != input.DocumentDate.Format("2006-01-02") || !sameDate(storedDueDate, input.DueDate) {
		return InvoicePosting{}, fmt.Errorf("%w: fatura finans alanları belgeyle eşleşmiyor", identity.ErrValidation)
	}
	if input.Currency != "" && storedCurrency != input.Currency {
		return InvoicePosting{}, domainError(ErrCurrencyMismatch, "fatura para birimi belgeyle eşleşmiyor")
	}
	input.Currency = storedCurrency
	// A caller-supplied rate must match the document; when omitted the effective
	// rate is re-derived below from the currency (base = 1, foreign = required).
	if strings.TrimSpace(input.ExchangeRate) != "" && storedRateValue.Cmp(rate) != 0 {
		return InvoicePosting{}, fmt.Errorf("%w: fatura kuru belgeyle eşleşmiyor", identity.ErrValidation)
	}
	var existingDocument string
	err = tx.QueryRow(ctx, `SELECT id FROM finance_invoice_postings WHERE company_id=$1 AND document_id=$2`, session.CurrentCompanyID, input.DocumentID).Scan(&existingDocument)
	if err == nil {
		return InvoicePosting{}, domainError(ErrInvoiceAlreadyPosted, "fatura daha önce post edilmiş")
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return InvoicePosting{}, err
	}
	if err = ensurePeriodOpen(ctx, tx, session.CurrentCompanyID, input.DocumentDate); err != nil {
		return InvoicePosting{}, err
	}
	if err = ensureFinanceDate(ctx, tx, session.CurrentCompanyID, input.DocumentDate, s.now()); err != nil {
		return InvoicePosting{}, err
	}
	var baseCurrency, partyCurrency string
	if err = tx.QueryRow(ctx, `SELECT c.base_currency,p.default_currency FROM companies c JOIN parties p ON p.company_id=c.id WHERE c.id=$1 AND p.id=$2 FOR UPDATE`, session.CurrentCompanyID, input.PartyID).Scan(&baseCurrency, &partyCurrency); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return InvoicePosting{}, identity.ErrForbidden
		}
		return InvoicePosting{}, err
	}
	if input.Currency == "" {
		input.Currency = partyCurrency
	}
	if len(input.Currency) != 3 || input.Currency != strings.ToUpper(input.Currency) {
		return InvoicePosting{}, fmt.Errorf("%w: fatura para birimi geçersiz", identity.ErrValidation)
	}
	if input.Currency == baseCurrency {
		if strings.TrimSpace(input.ExchangeRate) == "" {
			rate = big.NewRat(1, 1)
		} else {
			rate, err = parsePositive(input.ExchangeRate, 10)
			if err != nil || rate.Cmp(big.NewRat(1, 1)) != 0 {
				return InvoicePosting{}, fmt.Errorf("%w: şirket para biriminde kur 1 olmalıdır", identity.ErrValidation)
			}
		}
	} else {
		if strings.TrimSpace(input.ExchangeRate) == "" {
			return InvoicePosting{}, domainError(ErrExchangeRateRequired, "yabancı para fatura postu için kur gereklidir")
		}
		rate, err = parsePositive(input.ExchangeRate, 10)
		if err != nil {
			return InvoicePosting{}, fmt.Errorf("%w: fatura kuru geçersiz", identity.ErrValidation)
		}
	}
	if input.Currency != baseCurrency {
		if err = ensureExchangeRateWithinTolerance(ctx, tx, session.CurrentCompanyID, input.Currency, input.DocumentDate, rate); err != nil {
			return InvoicePosting{}, err
		}
	}
	if amountString(amount, 4) == "0.0000" || amountString(new(big.Rat).Mul(amount, rate), 4) == "0.0000" {
		return InvoicePosting{}, fmt.Errorf("%w: fatura tutarı finansın dört ondalık sınırında sıfıra yuvarlanamaz", identity.ErrValidation)
	}
	side := "RECEIVABLE"
	debit, credit := amountString(amount, 4), "0"
	if strings.HasPrefix(input.DocumentType, "PURCHASE_") {
		side, debit, credit = "PAYABLE", "0", amountString(amount, 4)
	}
	if strings.HasPrefix(input.DocumentType, "SALES_RETURN_") {
		// A sales return is a customer credit: it reduces the receivable.
		debit, credit = "0", amountString(amount, 4)
	}
	if strings.HasPrefix(input.DocumentType, "PURCHASE_RETURN_") {
		// A purchase return reduces the supplier payable.
		side, debit, credit = "PAYABLE", amountString(amount, 4), "0"
	}
	ledgerID, openItemID, postingID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	snapshot := jsonBytes(map[string]any{"document_id": input.DocumentID, "document_type": input.DocumentType, "transaction_currency": input.Currency, "amount": amountString(amount, 4), "base_currency": baseCurrency, "exchange_rate": amountString(rate, 10)})
	if _, err = tx.Exec(ctx, `INSERT INTO party_ledger_entries(id,company_id,party_id,currency,entry_type,source_type,source_id,idempotency_key,description,debit,credit,exchange_rate,document_date,actor_user_id,snapshot) VALUES($1,$2,$3,$4,$5,'document',$6,$7,$8,$9,$10,$11,$12,$13,$14)`, ledgerID, session.CurrentCompanyID, input.PartyID, input.Currency, side, input.DocumentID, "invoice:"+input.IdempotencyKey+":party", input.Description, debit, credit, amountString(rate, 10), input.DocumentDate, nullableUUID(session.User.ID), snapshot); err != nil {
		return InvoicePosting{}, mapFinanceConstraint(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO finance_invoice_open_items(id,company_id,document_id,party_id,party_ledger_entry_id,side,currency,original_amount,exchange_rate,base_currency,base_amount,document_date,due_date) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,ROUND($8::numeric*$9::numeric,4),$11,$12)`, openItemID, session.CurrentCompanyID, input.DocumentID, input.PartyID, ledgerID, side, input.Currency, amountString(amount, 4), amountString(rate, 10), baseCurrency, input.DocumentDate, input.DueDate); err != nil {
		return InvoicePosting{}, mapFinanceConstraint(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO finance_invoice_postings(id,company_id,document_id,party_ledger_entry_id,open_item_id,idempotency_key,posted_by) VALUES($1,$2,$3,$4,$5,$6,$7)`, postingID, session.CurrentCompanyID, input.DocumentID, ledgerID, openItemID, input.IdempotencyKey, nullableUUID(session.User.ID)); err != nil {
		return InvoicePosting{}, mapFinanceConstraint(err)
	}
	return InvoicePosting{ID: postingID, DocumentID: input.DocumentID, PartyLedgerEntryID: ledgerID, OpenItemID: openItemID, Side: side, Amount: amountString(amount, 4), Currency: input.Currency}, nil
}

// ReverseInvoiceTx creates the opposite party movement and closes the open
// item through an append-only reversal projection.
func (s *Service) ReverseInvoiceTx(ctx context.Context, tx pgx.Tx, session identity.Session, documentID, reversalKey, reason string) (string, error) {
	documentID = strings.TrimSpace(documentID)
	reversalKey, reason = strings.TrimSpace(reversalKey), strings.TrimSpace(reason)
	if tx == nil || uuid.Validate(documentID) != nil || reversalKey == "" || reason == "" {
		return "", fmt.Errorf("%w: fatura ters kayıt anahtarı ve gerekçesi gereklidir", identity.ErrValidation)
	}
	var postingID, openItemID, partyLedgerID, partyID, currency, side, amount, rate, originalDebit, originalCredit string
	var documentDate time.Time
	err := tx.QueryRow(ctx, `SELECT p.id,p.open_item_id,p.party_ledger_entry_id,oi.party_id,oi.currency,oi.side,oi.original_amount::text,oi.exchange_rate::text,oi.document_date,l.debit::text,l.credit::text
		FROM finance_invoice_postings p
		JOIN finance_invoice_open_items oi ON oi.company_id=p.company_id AND oi.id=p.open_item_id
		JOIN party_ledger_entries l ON l.company_id=p.company_id AND l.id=p.party_ledger_entry_id
		WHERE p.company_id=$1 AND p.document_id=$2
		FOR UPDATE OF p,oi,l`, session.CurrentCompanyID, documentID).Scan(&postingID, &openItemID, &partyLedgerID, &partyID, &currency, &side, &amount, &rate, &documentDate, &originalDebit, &originalCredit)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domainError(ErrInvoiceNotFound, "fatura posting kaydı bulunamadı")
	}
	if err != nil {
		return "", err
	}
	var lockedPartyID string
	if err = tx.QueryRow(ctx, `SELECT id FROM parties WHERE company_id=$1 AND id=$2 FOR UPDATE`, session.CurrentCompanyID, partyID).Scan(&lockedPartyID); err != nil {
		return "", err
	}
	// Check the immutable party ledger key before the open-item state. This is
	// what makes a repeated invoice reversal command a safe replay instead of
	// an ALREADY_REVERSED error.
	var existingReversalID, existingDescription string
	var existingOriginalID *string
	if keyErr := tx.QueryRow(ctx, `SELECT id,reversal_of_id,description FROM party_ledger_entries WHERE company_id=$1 AND idempotency_key=$2`, session.CurrentCompanyID, "invoice-reversal:"+reversalKey).Scan(&existingReversalID, &existingOriginalID, &existingDescription); keyErr == nil {
		if existingOriginalID != nil && *existingOriginalID == partyLedgerID && existingDescription == "Fatura ters kayıt: "+reason {
			return existingReversalID, nil
		}
		return "", domainError(ErrIdempotencyConflict, "aynı fatura ters kayıt anahtarı farklı veriyle kullanıldı")
	} else if !errors.Is(keyErr, pgx.ErrNoRows) {
		return "", keyErr
	}
	var allocatedAmount string
	if err = tx.QueryRow(ctx, `SELECT COALESCE(SUM(CASE WHEN reversal_of_id IS NULL THEN amount ELSE -amount END),0)::text FROM finance_payment_allocations WHERE company_id=$1 AND open_item_id=$2`, session.CurrentCompanyID, openItemID).Scan(&allocatedAmount); err != nil {
		return "", err
	}
	if allocated := mustRat(allocatedAmount); allocated.Sign() > 0 {
		return "", domainError(ErrInvoiceHasDependencies, "tahsis edilmiş tahsilat veya ödeme bulunduğu için belge ters kaydedilemez")
	}
	var activeReturn bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1
		  FROM commercial_document_sources rel
		  JOIN documents d ON d.company_id=rel.company_id AND d.id=rel.document_id
		  LEFT JOIN finance_invoice_open_items roi ON roi.company_id=rel.company_id AND roi.document_id=rel.document_id
		  LEFT JOIN finance_invoice_open_item_reversals rr ON rr.company_id=roi.company_id AND rr.open_item_id=roi.id
		 WHERE rel.company_id=$1
		   AND rel.relation_type='RETURN'
		   AND rel.document_id <> $2
		   AND d.document_type_code IN ('SALES_RETURN_INVOICE','PURCHASE_RETURN_INVOICE')
		   AND d.status='POSTED'
		   AND (rel.source_document_id=$2 OR rel.source_document_id IN (
			   SELECT source_document_id
			   FROM commercial_document_sources src
			   WHERE src.company_id=$1 AND src.document_id=$2
		   ))
		   AND rr.id IS NULL
	)`, session.CurrentCompanyID, documentID).Scan(&activeReturn); err != nil {
		return "", err
	}
	if activeReturn {
		return "", domainError(ErrInvoiceHasDependencies, "aktif iade bulunduğu için belge ters kaydedilemez")
	}
	if err = ensurePeriodOpen(ctx, tx, session.CurrentCompanyID, documentDate); err != nil {
		return "", err
	}
	if err = ensureFinanceDate(ctx, tx, session.CurrentCompanyID, documentDate, s.now()); err != nil {
		return "", err
	}
	var already bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM finance_invoice_open_item_reversals WHERE company_id=$1 AND open_item_id=$2)`, session.CurrentCompanyID, openItemID).Scan(&already); err != nil {
		return "", err
	}
	if already {
		return "", domainError(ErrAlreadyReversed, "fatura zaten ters kaydedilmiş")
	}
	reversalID := uuid.NewString()
	// The open-item side alone is not enough to determine the correction
	// direction: sales/purchase returns use the same side as their invoice but
	// carry the opposite ledger sign. Always negate the persisted source row so
	// both normal invoices and returns net to zero.
	debit, credit := originalCredit, originalDebit
	snapshot := jsonBytes(map[string]any{"reversal_of_document": documentID, "reason": reason, "amount": amount, "currency": currency})
	if _, err = tx.Exec(ctx, `INSERT INTO party_ledger_entries(id,company_id,party_id,currency,entry_type,source_type,source_id,idempotency_key,description,debit,credit,exchange_rate,document_date,reversal_of_id,actor_user_id,snapshot) SELECT $1,company_id,party_id,currency,'REVERSAL','document',$2,$3,$4,$5,$6,exchange_rate,$7,$8,$9,$10 FROM party_ledger_entries WHERE company_id=$11 AND id=$12`, reversalID, documentID, "invoice-reversal:"+reversalKey, "Fatura ters kayıt: "+reason, debit, credit, documentDate, partyLedgerID, nullableUUID(session.User.ID), snapshot, session.CurrentCompanyID, partyLedgerID); err != nil {
		return "", mapFinanceConstraint(err)
	}
	reversalProjectionID := uuid.NewString()
	if _, err = tx.Exec(ctx, `INSERT INTO finance_invoice_open_item_reversals(id,company_id,open_item_id,document_id,reversal_ledger_entry_id,amount) VALUES($1,$2,$3,$4,$5,$6)`, reversalProjectionID, session.CurrentCompanyID, openItemID, documentID, reversalID, amount); err != nil {
		return "", mapFinanceConstraint(err)
	}
	return reversalID, nil
}

func (s *Service) AddPeriodLock(ctx context.Context, session identity.Session, start, end time.Time, reason string, meta identity.RequestMeta) error {
	if !can(session, "finance.period.manage") {
		return identity.ErrForbidden
	}
	if start.IsZero() || end.IsZero() || end.Before(start) || strings.TrimSpace(reason) == "" {
		return fmt.Errorf("%w: geçerli dönem ve gerekçe gereklidir", identity.ErrValidation)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err = tx.QueryRow(ctx, `SELECT id FROM companies WHERE id=$1 FOR UPDATE`, session.CurrentCompanyID).Scan(new(string)); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO finance_period_locks(id,company_id,period_start,period_end,reason,locked_by) VALUES($1,$2,$3,$4,$5,$6)`, uuid.NewString(), session.CurrentCompanyID, start, end, reason, nullableUUID(session.User.ID)); err != nil {
		return mapFinanceConstraint(err)
	}
	if err = writeAuditAndEventTx(ctx, tx, session, "FINANCE_PERIOD_LOCKED", "finance.period.locked", "finance_period_lock", uuid.NewString(), meta, nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) ensureInstrumentTx(ctx context.Context, tx pgx.Tx, session identity.Session, input PaymentInput, amount *big.Rat, meta identity.RequestMeta) (*string, error) {
	typeName := input.PaymentMethod
	if input.InstrumentID != "" {
		var partyID, currency, instrumentType, instrumentAmount string
		if err := tx.QueryRow(ctx, `SELECT party_id,currency,instrument_type,amount::text FROM finance_instruments WHERE company_id=$1 AND id=$2 FOR SHARE`, session.CurrentCompanyID, input.InstrumentID).Scan(&partyID, &currency, &instrumentType, &instrumentAmount); err != nil {
			return nil, identity.ErrForbidden
		}
		if partyID != input.PartyID || currency != input.Currency || instrumentType != typeName {
			return nil, domainError(ErrCurrencyMismatch, "çek/senet cari veya para birimiyle eşleşmiyor")
		}
		persistedAmount, amountErr := parsePositive(instrumentAmount, 4)
		if amountErr != nil || persistedAmount.Cmp(amount) != 0 {
			return nil, fmt.Errorf("%w: çek/senet tutarı ödeme tutarıyla eşleşmelidir", identity.ErrValidation)
		}
		return &input.InstrumentID, nil
	}
	if input.Instrument == nil || strings.TrimSpace(input.Instrument.InstrumentNo) == "" {
		return nil, fmt.Errorf("%w: çek veya senet numarası gereklidir", identity.ErrValidation)
	}
	// Work on a value copy so defaulting instrument fields does not mutate the
	// caller's request object. Reusing the same command for an idempotent retry
	// must produce the same request hash.
	in := *input.Instrument
	instrumentID := uuid.NewString()
	issueDate := in.IssueDate
	if issueDate.IsZero() {
		issueDate = input.TransactionDate
	}
	in.InstrumentType = strings.ToUpper(strings.TrimSpace(in.InstrumentType))
	in.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
	if in.Currency == "" {
		in.Currency = input.Currency
	}
	if in.Amount == "" {
		in.Amount = amountString(amount, 4)
	}
	if strings.ToUpper(in.InstrumentType) != typeName || strings.ToUpper(in.Currency) != input.Currency {
		return nil, domainError(ErrCurrencyMismatch, "çek/senet alanları ödeme ile eşleşmiyor")
	}
	instrumentAmount, amountErr := parsePositive(in.Amount, 4)
	if amountErr != nil || instrumentAmount.Cmp(amount) != 0 {
		return nil, fmt.Errorf("%w: çek/senet tutarı ödeme tutarıyla eşleşmelidir", identity.ErrValidation)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO finance_instruments(id,company_id,instrument_type,instrument_no,party_id,currency,amount,issue_date,due_date,bank_name,drawer_name,description,status,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,'RECEIVED',$13)`, instrumentID, session.CurrentCompanyID, typeName, in.InstrumentNo, input.PartyID, input.Currency, in.Amount, issueDate, in.DueDate, in.BankName, in.DrawerName, in.Description, nullableUUID(session.User.ID)); err != nil {
		return nil, mapFinanceConstraint(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO finance_instrument_events(id,company_id,instrument_id,event_type,event_date,description,source_type,source_id,actor_user_id) VALUES($1,$2,$3,'RECEIVED',$4,$5,'finance_payment',$6,$7)`, uuid.NewString(), session.CurrentCompanyID, instrumentID, input.TransactionDate, in.Description, nullableUUID(input.ID), nullableUUID(session.User.ID)); err != nil {
		return nil, mapFinanceConstraint(err)
	}
	_ = meta
	return &instrumentID, nil
}

func normalizeAllocationInputs(inputs []AllocationInput) ([]AllocationInput, error) {
	result := append([]AllocationInput(nil), inputs...)
	for index := range result {
		openItemID, parseErr := uuid.Parse(strings.TrimSpace(result[index].OpenItemID))
		if parseErr != nil {
			return nil, fmt.Errorf("%w: açık kalem kimliği geçersiz", identity.ErrValidation)
		}
		amount, amountErr := parsePositive(result[index].Amount, 4)
		if amountErr != nil {
			return nil, fmt.Errorf("%w: tahsis tutarı geçersiz", identity.ErrValidation)
		}
		result[index].OpenItemID = openItemID.String()
		result[index].Amount = amountString(amount, 4)
		result[index].IdempotencyKey = strings.TrimSpace(result[index].IdempotencyKey)
	}
	sort.Slice(result, func(left, right int) bool { return result[left].OpenItemID < result[right].OpenItemID })
	return result, nil
}

func (s *Service) applyAllocationsTx(ctx context.Context, tx pgx.Tx, session identity.Session, paymentID string, inputs []AllocationInput, meta identity.RequestMeta) ([]Allocation, error) {
	if len(inputs) == 0 {
		return []Allocation{}, nil
	}
	normalizedInputs, normalizeErr := normalizeAllocationInputs(inputs)
	if normalizeErr != nil {
		return nil, normalizeErr
	}
	inputs = normalizedInputs
	for index := 1; index < len(inputs); index++ {
		if inputs[index-1].OpenItemID == inputs[index].OpenItemID {
			return nil, fmt.Errorf("%w: aynı açık kalem bir komutta bir kez tahsis edilebilir", identity.ErrValidation)
		}
	}
	var paymentParty, paymentCurrency, paymentKind, paymentAmount string
	var err error
	if err := tx.QueryRow(ctx, `SELECT party_id,currency,payment_kind,amount::text FROM finance_payments WHERE company_id=$1 AND id=$2 FOR UPDATE`, session.CurrentCompanyID, paymentID).Scan(&paymentParty, &paymentCurrency, &paymentKind, &paymentAmount); err != nil {
		return nil, err
	}
	var usedText string
	if err = tx.QueryRow(ctx, `SELECT COALESCE(SUM(CASE WHEN reversal_of_id IS NULL THEN amount ELSE -amount END),0)::text FROM finance_payment_allocations WHERE company_id=$1 AND payment_id=$2`, session.CurrentCompanyID, paymentID).Scan(&usedText); err != nil {
		return nil, err
	}
	used, _ := parseRat(usedText)
	paymentRat, _ := parseRat(paymentAmount)
	result := make([]Allocation, 0, len(inputs))
	for _, input := range inputs {
		amount, parseErr := parsePositive(input.Amount, 4)
		if parseErr != nil {
			return nil, fmt.Errorf("%w: tahsis tutarı geçersiz", identity.ErrValidation)
		}
		key := strings.TrimSpace(input.IdempotencyKey)
		if key == "" {
			key = fmt.Sprintf("%s:%s", paymentID, input.OpenItemID)
		}
		var existing Allocation
		var existingReversal *string
		err = tx.QueryRow(ctx, `SELECT id,payment_id,party_id,open_item_id,target_type,target_id,currency,amount::text,reversal_of_id,allocated_at FROM finance_payment_allocations WHERE company_id=$1 AND idempotency_key=$2`, session.CurrentCompanyID, key).Scan(&existing.ID, &existing.PaymentID, &existing.PartyID, &existing.OpenItemID, &existing.TargetType, &existing.TargetID, &existing.Currency, &existing.Amount, &existingReversal, &existing.AllocatedAt)
		if err == nil {
			existing.ReversalOfID = existingReversal
			if existingReversal != nil {
				return nil, domainError(ErrInvalidPaymentState, "ters kaydedilmiş tahsis yeniden kullanılamaz")
			}
			if existing.PaymentID != paymentID || existing.PartyID != paymentParty || existing.Currency != paymentCurrency || existing.OpenItemID != input.OpenItemID || existing.Amount != amountString(amount, 4) {
				return nil, domainError(ErrIdempotencyConflict, "aynı tahsis anahtarı farklı veriyle kullanıldı")
			}
			result = append(result, existing)
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		if new(big.Rat).Add(used, amount).Cmp(paymentRat) > 0 {
			return nil, domainError(ErrPaymentAllocationExceedsOpenAmount, "tahsis toplamı ödeme tutarını aşamaz")
		}
		var openParty, side, currency, original, documentID, documentDate, dueDate string
		var reversalAmount string
		err = tx.QueryRow(ctx, `SELECT oi.party_id,oi.side,oi.currency,oi.original_amount::text,oi.document_id,oi.document_date::text,COALESCE(oi.due_date::text,''),COALESCE((SELECT amount FROM finance_invoice_open_item_reversals r WHERE r.company_id=oi.company_id AND r.open_item_id=oi.id),'0')::text FROM finance_invoice_open_items oi JOIN documents d ON d.company_id=oi.company_id AND d.id=oi.document_id AND d.document_type_code IN ('SALES_INVOICE','PURCHASE_INVOICE') WHERE oi.company_id=$1 AND oi.id=$2 AND (d.branch_id IS NULL OR NOT EXISTS(SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=d.company_id AND bs.user_id=$3) OR EXISTS(SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=d.company_id AND bs.user_id=$3 AND bs.branch_id=d.branch_id)) FOR UPDATE`, session.CurrentCompanyID, input.OpenItemID, session.User.ID).Scan(&openParty, &side, &currency, &original, &documentID, &documentDate, &dueDate, &reversalAmount)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, identity.ErrForbidden
		}
		if err != nil {
			return nil, err
		}
		if openParty != paymentParty || currency != paymentCurrency || (paymentKind == "COLLECTION" && side != "RECEIVABLE") || (paymentKind == "PAYMENT" && side != "PAYABLE") {
			return nil, domainError(ErrCurrencyMismatch, "tahsis cari, para birimi veya yönü eşleşmiyor")
		}
		allocatedText, scanErr := allocationForOpenItem(ctx, tx, session.CurrentCompanyID, input.OpenItemID)
		if scanErr != nil {
			return nil, scanErr
		}
		returnedAmount, returnErr := returnedAmountForDocumentTx(ctx, tx, session.CurrentCompanyID, documentID)
		if returnErr != nil {
			return nil, returnErr
		}
		openAmount := new(big.Rat).Sub(mustRat(original), mustRat(reversalAmount))
		openAmount.Sub(openAmount, returnedAmount)
		openAmount.Sub(openAmount, mustRat(allocatedText))
		if amount.Cmp(openAmount) > 0 {
			return nil, domainError(ErrPaymentAllocationExceedsOpenAmount, "tahsis açık fatura tutarını aşamaz")
		}
		var allocation Allocation
		allocation.ID = uuid.NewString()
		if err = tx.QueryRow(ctx, `INSERT INTO finance_payment_allocations(id,company_id,payment_id,party_id,target_type,target_id,open_item_id,currency,amount,idempotency_key,actor_user_id,snapshot) VALUES($1,$2,$3,$4,'DOCUMENT',$5,$6,$7,$8,$9,$10,$11) RETURNING allocated_at`, allocation.ID, session.CurrentCompanyID, paymentID, paymentParty, documentID, input.OpenItemID, paymentCurrency, amountString(amount, 4), key, nullableUUID(session.User.ID), jsonBytes(map[string]any{"document_date": documentDate, "due_date": dueDate})).Scan(&allocation.AllocatedAt); err != nil {
			return nil, mapFinanceConstraint(err)
		}
		allocation.PaymentID, allocation.PartyID, allocation.OpenItemID, allocation.TargetType, allocation.TargetID, allocation.Currency, allocation.Amount, allocation.IdempotencyKey = paymentID, paymentParty, input.OpenItemID, "DOCUMENT", documentID, paymentCurrency, amountString(amount, 4), key
		result = append(result, allocation)
		used.Add(used, amount)
	}
	_ = meta
	return result, nil
}

// FIFOAllocations returns oldest open items first. It is pure and therefore
// also used by tests and report-preview code before the command locks rows.
func FIFOAllocations(items []OpenItem, amount string) ([]AllocationInput, error) {
	remaining, err := parsePositive(amount, 4)
	if err != nil {
		return nil, fmt.Errorf("%w: FIFO tutarı geçersiz", identity.ErrValidation)
	}
	ordered := append([]OpenItem(nil), items...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].DueDate != nil || ordered[j].DueDate != nil {
			if ordered[i].DueDate == nil {
				return false
			}
			if ordered[j].DueDate == nil {
				return true
			}
			if !ordered[i].DueDate.Equal(*ordered[j].DueDate) {
				return ordered[i].DueDate.Before(*ordered[j].DueDate)
			}
		}
		if ordered[i].DocumentDate.Equal(ordered[j].DocumentDate) {
			return ordered[i].ID < ordered[j].ID
		}
		return ordered[i].DocumentDate.Before(ordered[j].DocumentDate)
	})
	result := []AllocationInput{}
	for _, item := range ordered {
		open, parseErr := parsePositive(item.OpenAmount, 4)
		if parseErr != nil || open.Sign() <= 0 {
			continue
		}
		part := open
		if part.Cmp(remaining) > 0 {
			part = new(big.Rat).Set(remaining)
		}
		result = append(result, AllocationInput{OpenItemID: item.ID, Amount: amountString(part, 4)})
		remaining.Sub(remaining, part)
		if remaining.Sign() == 0 {
			break
		}
	}
	return result, nil
}

// AllocateFIFO is the exported spelling used by command handlers and tests.
func AllocateFIFO(items []OpenItem, amount string) ([]AllocationInput, error) {
	return FIFOAllocations(items, amount)
}

// FormatAmount validates and rounds a decimal at a domain boundary using
// HALF_UP semantics. It is useful to adapters that must echo canonical money
// without converting through float64.
func FormatAmount(value string, scale int) (string, error) {
	r, err := parseRat(value)
	if err != nil {
		return "", err
	}
	return amountString(r, scale), nil
}

func allocationForOpenItem(ctx context.Context, tx pgx.Tx, companyID, openItemID string) (string, error) {
	var value string
	err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(CASE WHEN reversal_of_id IS NULL THEN amount ELSE -amount END),0)::text FROM finance_payment_allocations WHERE company_id=$1 AND open_item_id=$2`, companyID, openItemID).Scan(&value)
	return value, err
}

// fxRateTolerance is the maximum fraction a user-supplied foreign-currency
// rate may deviate from the reference rate in exchange_rates before a
// payment / invoice / manual entry is rejected. It guards against a fat-finger
// rate (e.g. 1.0 for GBP into a TRY-base company) silently corrupting the
// base-currency balance. Reversals are exempt: they copy the source rate.
var fxRateTolerance = big.NewRat(20, 100) // ±20%

// ensureExchangeRateWithinTolerance rejects a foreign-currency rate that
// deviates more than fxRateTolerance from the reference rate stored for the
// transaction date. A missing reference rate (an unmanaged currency, or a
// date before the first fetch) skips the check -- the caller still enforces
// "a rate is required" for foreign currency. It reads exchange_rates directly
// (no provider refresh) so it stays inside the caller's transaction.
func ensureExchangeRateWithinTolerance(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, companyID, currency string, date time.Time, rate *big.Rat) error {
	if rate == nil || rate.Sign() <= 0 {
		return nil
	}
	var refText string
	err := q.QueryRow(ctx, `SELECT rate_to_base::text FROM exchange_rates
		WHERE company_id=$1 AND currency_code=$2 AND rate_date <= $3::date
		ORDER BY rate_date DESC, fetched_at DESC LIMIT 1`,
		companyID, strings.ToUpper(strings.TrimSpace(currency)), date.Format("2006-01-02")).Scan(&refText)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	ref, ok := new(big.Rat).SetString(refText)
	if !ok || ref.Sign() <= 0 {
		return nil
	}
	diff := new(big.Rat).Sub(rate, ref)
	diff.Abs(diff)
	if diff.Cmp(new(big.Rat).Mul(ref, fxRateTolerance)) > 0 {
		return fmt.Errorf("%w: girilen kur (%s) o güne ait referans kurdan (%s) çok farklı", identity.ErrValidation, rate.FloatString(6), ref.FloatString(6))
	}
	return nil
}

func ensurePeriodOpen(ctx context.Context, tx pgx.Tx, companyID string, date time.Time) error {
	var lockedCompanyID string
	if err := tx.QueryRow(ctx, `SELECT id FROM companies WHERE id=$1 FOR SHARE`, companyID).Scan(&lockedCompanyID); err != nil {
		return err
	}
	var locked bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM finance_period_locks WHERE company_id=$1 AND $2::date BETWEEN period_start AND period_end)`, companyID, date.Format("2006-01-02")).Scan(&locked); err != nil {
		return err
	}
	if locked {
		return domainError(ErrPeriodLocked, "işlem tarihi kilitli döneme ait")
	}
	return nil
}

// EnsurePeriodOpenTx lets another bounded context validate the same finance
// period invariant while it still owns the enclosing transaction. It does not
// grant posting permission; callers must perform their own authorization.
func (s *Service) EnsurePeriodOpenTx(ctx context.Context, tx pgx.Tx, companyID string, date time.Time) error {
	if tx == nil {
		return fmt.Errorf("%w: finans işlemi için transaction gereklidir", identity.ErrValidation)
	}
	return ensurePeriodOpen(ctx, tx, companyID, date)
}

func getPayment(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, companyID, id string) (Payment, error) {
	var item Payment
	var accountID, movementID, instrumentID, reversalID, baseCurrency, baseAmount *string
	var snapshot []byte
	err := q.QueryRow(ctx, `SELECT p.id,p.company_id,p.party_id,p.account_id,p.movement_id,p.party_ledger_entry_id,p.payment_kind,p.payment_method,p.movement_direction,p.currency,p.amount::text,p.exchange_rate::text,p.base_currency,p.base_amount::text,p.document_no,p.reference_no,p.description,p.transaction_date,p.idempotency_key,p.instrument_id,p.reversal_of_id,p.actor_user_id,COALESCE(u.display_name,u.email,''),p.posted_at,p.snapshot FROM finance_payments p LEFT JOIN users u ON u.id=p.actor_user_id WHERE p.company_id=$1 AND p.id=$2`, companyID, id).Scan(&item.ID, &item.CompanyID, &item.PartyID, &accountID, &movementID, &item.PartyLedgerEntryID, &item.PaymentKind, &item.PaymentMethod, &item.MovementDirection, &item.Currency, &item.Amount, &item.ExchangeRate, &baseCurrency, &baseAmount, &item.DocumentNo, &item.ReferenceNo, &item.Description, &item.TransactionDate, &item.IdempotencyKey, &instrumentID, &reversalID, &item.ActorUserID, &item.ActorName, &item.PostedAt, &snapshot)
	item.AccountID, item.MovementID, item.InstrumentID, item.ReversalOfID, item.BaseCurrency, item.BaseAmount = accountID, movementID, instrumentID, reversalID, baseCurrency, baseAmount
	item.Status = "POSTED"
	var reversed bool
	if item.ReversalOfID == nil && err == nil {
		if reverseErr := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM finance_payments WHERE company_id=$1 AND reversal_of_id=$2)`, companyID, id).Scan(&reversed); reverseErr != nil {
			return Payment{}, reverseErr
		}
	}
	if reversed {
		item.Status = "REVERSED"
	}
	if err == nil {
		var accountArg any
		if accountID != nil {
			accountArg = *accountID
		}
		if nameErr := q.QueryRow(ctx, `SELECT COALESCE(p.code,''),COALESCE(p.display_name,''),COALESCE(a.code,''),COALESCE(a.name,'') FROM parties p LEFT JOIN finance_accounts a ON a.company_id=p.company_id AND a.id=$2::uuid WHERE p.company_id=$1 AND p.id=$3`, companyID, accountArg, item.PartyID).Scan(&item.PartyCode, &item.PartyName, &item.AccountCode, &item.AccountName); nameErr != nil {
			return Payment{}, nameErr
		}
	}
	item.snapshot, item.requestHash = snapshot, snapshotRequestHash(snapshot)
	applyPaymentSnapshotNames(&item, snapshot)
	return item, err
}

func applyPaymentSnapshotNames(item *Payment, snapshot []byte) {
	if item == nil || len(snapshot) == 0 {
		return
	}
	var names struct {
		PartyCode   string `json:"party_code"`
		PartyName   string `json:"party_name"`
		AccountCode string `json:"account_code"`
		AccountName string `json:"account_name"`
	}
	if json.Unmarshal(snapshot, &names) != nil {
		return
	}
	if names.PartyCode != "" {
		item.PartyCode = names.PartyCode
	}
	if names.PartyName != "" {
		item.PartyName = names.PartyName
	}
	if names.AccountCode != "" {
		item.AccountCode = names.AccountCode
	}
	if names.AccountName != "" {
		item.AccountName = names.AccountName
	}
}

func findPaymentByKey(ctx context.Context, tx pgx.Tx, companyID, key string) (Payment, bool, error) {
	item, err := getPaymentByKey(ctx, tx, companyID, key)
	if errors.Is(err, pgx.ErrNoRows) {
		return Payment{}, false, nil
	}
	return item, err == nil, err
}

func getPaymentByKey(ctx context.Context, tx pgx.Tx, companyID, key string) (Payment, error) {
	var item Payment
	var accountID, movementID, instrumentID, reversalID, baseCurrency, baseAmount *string
	var snapshot []byte
	err := tx.QueryRow(ctx, `SELECT id,company_id,party_id,account_id,movement_id,party_ledger_entry_id,payment_kind,payment_method,movement_direction,currency,amount::text,exchange_rate::text,base_currency,base_amount::text,document_no,reference_no,description,transaction_date,idempotency_key,instrument_id,reversal_of_id,posted_at,snapshot FROM finance_payments WHERE company_id=$1 AND idempotency_key=$2`, companyID, key).Scan(&item.ID, &item.CompanyID, &item.PartyID, &accountID, &movementID, &item.PartyLedgerEntryID, &item.PaymentKind, &item.PaymentMethod, &item.MovementDirection, &item.Currency, &item.Amount, &item.ExchangeRate, &baseCurrency, &baseAmount, &item.DocumentNo, &item.ReferenceNo, &item.Description, &item.TransactionDate, &item.IdempotencyKey, &instrumentID, &reversalID, &item.PostedAt, &snapshot)
	item.AccountID, item.MovementID, item.InstrumentID, item.ReversalOfID, item.BaseCurrency, item.BaseAmount = accountID, movementID, instrumentID, reversalID, baseCurrency, baseAmount
	item.Status = "POSTED"
	var reversed bool
	if item.ReversalOfID == nil && err == nil {
		if reverseErr := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM finance_payments WHERE company_id=$1 AND reversal_of_id=$2)`, companyID, item.ID).Scan(&reversed); reverseErr != nil {
			return Payment{}, reverseErr
		}
	}
	if reversed {
		item.Status = "REVERSED"
	}
	item.snapshot, item.requestHash = snapshot, snapshotRequestHash(snapshot)
	return item, err
}

// paymentRequestHash covers fields that are not represented by the compact
// payment row alone, notably allocation order and check/senet input. It is
// stored inside the immutable snapshot so a retry with the same key cannot
// silently change the command while still matching the posted amount/account.
func paymentRequestHash(input PaymentInput, amount, rate *big.Rat) string {
	// Hash the business representation rather than transport formatting. This
	// makes retries stable across currency/amount casing, allocation order and
	// date timezone offsets while still retaining every material field.
	if input.Instrument != nil {
		instrument := *input.Instrument
		instrument.ID = strings.TrimSpace(instrument.ID)
		instrument.InstrumentType = strings.ToUpper(strings.TrimSpace(instrument.InstrumentType))
		instrument.InstrumentNo = strings.TrimSpace(instrument.InstrumentNo)
		instrument.Currency = strings.ToUpper(strings.TrimSpace(instrument.Currency))
		instrument.BankName, instrument.DrawerName, instrument.Description = strings.TrimSpace(instrument.BankName), strings.TrimSpace(instrument.DrawerName), strings.TrimSpace(instrument.Description)
		if instrument.Amount != "" {
			if value, parseErr := parsePositive(instrument.Amount, 4); parseErr == nil {
				instrument.Amount = amountString(value, 4)
			}
		}
		if instrument.IssueDate.IsZero() {
			instrument.IssueDate = input.TransactionDate
		}
		instrument.IssueDate = businessDateUTC(instrument.IssueDate)
		if instrument.DueDate != nil {
			value := businessDateUTC(*instrument.DueDate)
			instrument.DueDate = &value
		}
		input.Instrument = &instrument
	}
	if len(input.Allocations) > 0 {
		if normalized, normalizeErr := normalizeAllocationInputs(input.Allocations); normalizeErr == nil {
			input.Allocations = normalized
		}
	}
	payload := struct {
		PartyID         string            `json:"party_id"`
		AccountID       string            `json:"account_id,omitempty"`
		PaymentKind     string            `json:"payment_kind"`
		PaymentMethod   string            `json:"payment_method"`
		Currency        string            `json:"currency"`
		Amount          string            `json:"amount"`
		ExchangeRate    string            `json:"exchange_rate"`
		DocumentNo      string            `json:"document_no,omitempty"`
		ReferenceNo     string            `json:"reference_no,omitempty"`
		Description     string            `json:"description"`
		TransactionDate string            `json:"transaction_date"`
		InstrumentID    string            `json:"instrument_id,omitempty"`
		Instrument      *InstrumentInput  `json:"instrument,omitempty"`
		Allocations     []AllocationInput `json:"allocations,omitempty"`
		AutoAllocate    bool              `json:"auto_allocate,omitempty"`
		OverrideReason  string            `json:"override_reason,omitempty"`
	}{
		PartyID: input.PartyID, AccountID: input.AccountID, PaymentKind: input.PaymentKind,
		PaymentMethod: input.PaymentMethod, Currency: input.Currency,
		Amount: amountString(amount, 4), ExchangeRate: amountString(rate, 10),
		DocumentNo: input.DocumentNo, ReferenceNo: input.ReferenceNo, Description: input.Description,
		// Payments are posted on a business date. Time-of-day and timezone
		// formatting must not make an otherwise identical retry conflict.
		TransactionDate: input.TransactionDate.Format("2006-01-02"),
		InstrumentID:    input.InstrumentID, Instrument: input.Instrument, Allocations: input.Allocations,
		AutoAllocate: input.AutoAllocate, OverrideReason: strings.TrimSpace(input.OverrideReason),
	}
	encoded, _ := json.Marshal(payload)
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("%x", digest[:])
}

func businessDateUTC(value time.Time) time.Time {
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
}

func snapshotRequestHash(snapshot []byte) string {
	if len(snapshot) == 0 {
		return ""
	}
	var value struct {
		RequestHash string `json:"request_hash"`
	}
	if err := json.Unmarshal(snapshot, &value); err != nil {
		return ""
	}
	return strings.TrimSpace(value.RequestHash)
}

func samePaymentInput(existing Payment, input PaymentInput, amount, rate *big.Rat) bool {
	currencyMatches := input.Currency == "" || existing.Currency == input.Currency
	rateMatches := input.ExchangeRate == "" || existing.ExchangeRate == amountString(rate, 10)
	dateMatches := existing.TransactionDate.Format("2006-01-02") == input.TransactionDate.Format("2006-01-02")
	if existing.PartyID != input.PartyID || existing.PaymentKind != input.PaymentKind || existing.PaymentMethod != input.PaymentMethod || !currencyMatches || !rateMatches || !dateMatches || existing.Amount != amountString(amount, 4) || (input.DocumentNo != "" && existing.DocumentNo != input.DocumentNo) || existing.ReferenceNo != input.ReferenceNo || existing.Description != input.Description || existing.AccountID == nil && input.AccountID != "" {
		return false
	}
	if input.InstrumentID != "" && (existing.InstrumentID == nil || *existing.InstrumentID != input.InstrumentID) {
		return false
	}
	return (existing.AccountID == nil && input.AccountID == "") || (existing.AccountID != nil && *existing.AccountID == input.AccountID)
}

func paymentMovementDirection(kind string) string {
	if kind == "COLLECTION" {
		return "IN"
	}
	return "OUT"
}

func writeAuditAndEventTx(ctx context.Context, tx pgx.Tx, session identity.Session, eventType, outboxType, entityType, entityID string, meta identity.RequestMeta, payload map[string]any) error {
	if payload == nil {
		payload = map[string]any{}
	}
	payload["schema_version"] = 1
	payload["entity_id"] = entityID
	details, _ := json.Marshal(map[string]any{"version": 1, "event": eventType})
	if _, err := tx.Exec(ctx, `INSERT INTO security_audit_events(id,company_id,actor_user_id,event_type,entity_type,entity_id,details,trace_id,source_ip,user_agent) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, uuid.NewString(), session.CurrentCompanyID, nullableUUID(session.User.ID), eventType, entityType, nullableUUID(entityID), details, meta.TraceID, meta.IP, truncate(meta.UserAgent, 512)); err != nil {
		return err
	}
	encoded, _ := json.Marshal(payload)
	_, err := tx.Exec(ctx, `INSERT INTO outbox_events(event_id,type,schema_version,company_id,trace_id,payload) VALUES($1,$2,1,$3,$4,$5)`, uuid.NewString(), outboxType, session.CurrentCompanyID, meta.TraceID, encoded)
	return err
}

func parsePositive(value string, scale int) (*big.Rat, error) {
	parsed, err := money.ParseDecimal(value, scale)
	if err != nil || parsed.Sign() <= 0 {
		return nil, money.ErrInvalidDecimal
	}
	return parseRat(parsed.String())
}

func parsePositiveDefault(value string, scale int) (*big.Rat, error) {
	if strings.TrimSpace(value) == "" {
		value = "1"
	}
	return parsePositive(value, scale)
}

func parseRat(value string) (*big.Rat, error) {
	r, ok := new(big.Rat).SetString(strings.TrimSpace(value))
	if !ok {
		return nil, money.ErrInvalidDecimal
	}
	return r, nil
}

func mustRat(value string) *big.Rat {
	r, _ := parseRat(value)
	if r == nil {
		return new(big.Rat)
	}
	return r
}

func amountString(value *big.Rat, scale int) string {
	if value == nil {
		return "0"
	}
	negative := value.Sign() < 0
	numerator := new(big.Int).Mul(new(big.Int).Abs(value.Num()), pow10(scale))
	denominator := new(big.Int).Set(value.Denom())
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(numerator, denominator, remainder)
	if new(big.Int).Lsh(new(big.Int).Set(remainder), 1).Cmp(denominator) >= 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	digits := quotient.String()
	if scale > 0 {
		if len(digits) <= scale {
			digits = strings.Repeat("0", scale+1-len(digits)) + digits
		}
		digits = digits[:len(digits)-scale] + "." + digits[len(digits)-scale:]
	}
	if negative && quotient.Sign() != 0 {
		return "-" + digits
	}
	return digits
}

func pow10(scale int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(scale)), nil)
}

func nullableUUID(value string) any {
	if uuid.Validate(value) != nil {
		return nil
	}
	return value
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

func mapFinanceConstraint(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.ConstraintName {
		case "finance_payments_company_id_idempotency_key_key":
			return domainError(ErrPaymentAlreadyPosted, "ödeme daha önce kaydedilmiş")
		case "finance_payments_company_id_reversal_of_id_key":
			return domainError(ErrAlreadyReversed, "ödeme zaten ters kaydedilmiş")
		case "finance_manual_entries_company_id_idempotency_key_key":
			return domainError(ErrIdempotencyConflict, "aynı manuel hareket anahtarı daha önce kullanılmış")
		case "finance_manual_entries_company_id_reversal_of_id_key":
			return domainError(ErrAlreadyReversed, "manuel hareket zaten ters kaydedilmiş")
		case "finance_party_transfers_company_id_idempotency_key_key":
			return domainError(ErrIdempotencyConflict, "aynı virman anahtarı daha önce kullanılmış")
		case "finance_party_transfers_company_id_reversal_of_id_key":
			return domainError(ErrAlreadyReversed, "virman zaten ters kaydedilmiş")
		case "finance_invoice_postings_company_id_idempotency_key_key":
			return domainError(ErrIdempotencyConflict, "aynı fatura posting anahtarı daha önce kullanılmış")
		case "finance_invoice_postings_company_id_document_id_key", "finance_invoice_open_items_company_id_document_id_key":
			return domainError(ErrInvoiceAlreadyPosted, "fatura daha önce post edilmiş")
		case "party_ledger_entries_company_id_idempotency_key_key":
			return domainError(ErrIdempotencyConflict, "aynı cari hareket anahtarı daha önce kullanılmış")
		case "party_ledger_entries_company_id_reversal_of_id_key":
			return domainError(ErrAlreadyReversed, "cari hareket zaten ters kaydedilmiş")
		case "party_ledger_base_amount_check", "finance_payments_base_amount_check", "finance_manual_entries_base_amount_check":
			return fmt.Errorf("%w: temel para birimine çevrilen tutar sıfıra yuvarlanamaz", identity.ErrValidation)
		case "finance_account_movements_one_opening":
			return domainError(ErrOpeningBalanceExists, "açılış bakiyesi daha önce kaydedilmiş")
		case "finance_account_identity_immutable":
			return fmt.Errorf("%w: hesap türü ve para birimi değiştirilemez", identity.ErrValidation)
		case "finance_account_branch_after_movement":
			return domainError(ErrAccountBranchImmutable, "hareket görmüş hesabın şubesi değiştirilemez")
		case "finance_accounts_no_delete":
			return domainError(ErrAccountInactive, "finans hesabı silinemez; pasifleştirilmelidir")
		case "finance_transfers_company_id_idempotency_key_key", "finance_transfers_company_id_reversal_of_id_key":
			return domainError(ErrIdempotencyConflict, "transfer daha önce kaydedilmiş")
		case "finance_account_movements_account_currency_fk", "finance_transfer_movement_pair", "finance_payment_movement_account":
			return domainError(ErrCurrencyMismatch, "hesap, hareket ve para birimi ilişkisi geçersiz")
		}
		if pgErr.Code == "40001" || pgErr.Code == "40P01" {
			return domainError(ErrIdempotencyConflict, "eşzamanlı finans işlemi nedeniyle yeniden deneyin")
		}
	}
	return err
}
