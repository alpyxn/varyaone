package finance

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type AccountBalance struct {
	AccountID string `json:"account_id"`
	Currency  string `json:"currency"`
	Balance   string `json:"balance"`
}

type AccountMovement struct {
	ID                string    `json:"id"`
	AccountID         string    `json:"account_id"`
	AccountType       string    `json:"account_type"`
	MovementKind      string    `json:"movement_kind"`
	Direction         string    `json:"direction"`
	Currency          string    `json:"currency"`
	Amount            string    `json:"amount"`
	TransactionDate   time.Time `json:"transaction_date"`
	SourceType        string    `json:"source_type"`
	SourceID          string    `json:"source_id"`
	Description       string    `json:"description"`
	ExternalReference string    `json:"external_reference,omitempty"`
	ReversalOfID      *string   `json:"reversal_of_id,omitempty"`
	ExchangeRate      string    `json:"exchange_rate"`
	BaseCurrency      *string   `json:"base_currency,omitempty"`
	BaseAmount        *string   `json:"base_amount,omitempty"`
	PostedAt          time.Time `json:"posted_at"`
	// ActorName is the display name (or email) of the user who posted the
	// movement, resolved on single-record reads for the detail page.
	ActorName string `json:"actor_name,omitempty"`
	// SourceLabel/SourceHref describe where the movement originated (a personel
	// avansı, a tahsilat/ödeme, a hesap transferi, a manuel hareket). They are
	// derived on read so the movement grid and detail page can link back to the
	// originating record instead of showing a raw source_type token.
	SourceLabel string `json:"source_label,omitempty"`
	SourceHref  string `json:"source_href,omitempty"`
}

type AccountMovementInput struct {
	AccountID         string    `json:"account_id"`
	Direction         string    `json:"direction"`
	Amount            string    `json:"amount"`
	TransactionDate   time.Time `json:"transaction_date"`
	Description       string    `json:"description"`
	ExternalReference string    `json:"external_reference,omitempty"`
	IdempotencyKey    string    `json:"idempotency_key,omitempty"`
	OverrideReason    string    `json:"override_reason,omitempty"`
}

// EmployeeAdvanceMovementInput is the deliberately narrow transaction port
// used by the HR employee-advance sub-ledger. The caller owns the surrounding
// transaction, so the cash/bank movement and HR ledger entry either commit or
// roll back together.
type EmployeeAdvanceMovementInput struct {
	AccountID         string
	Direction         string
	Amount            string
	TransactionDate   time.Time
	SourceID          string
	Description       string
	ExternalReference string
	IdempotencyKey    string
	OverrideReason    string
	ReversalOfID      *string
}

// PostEmployeeAdvanceMovementTx posts a TRY cash/bank movement inside the
// caller's transaction. Authorization for the HR command remains with HR;
// finance enforces account visibility and all posting invariants.
func (s *Service) PostEmployeeAdvanceMovementTx(ctx context.Context, tx pgx.Tx, session identity.Session, input EmployeeAdvanceMovementInput) (string, error) {
	if tx == nil {
		return "", fmt.Errorf("%w: finans işlemi için transaction gereklidir", identity.ErrValidation)
	}
	input.AccountID = strings.TrimSpace(input.AccountID)
	input.Direction = strings.ToUpper(strings.TrimSpace(input.Direction))
	input.Description = strings.TrimSpace(input.Description)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if uuid.Validate(input.AccountID) != nil || uuid.Validate(input.SourceID) != nil ||
		!contains([]string{"IN", "OUT"}, input.Direction) || input.TransactionDate.IsZero() || input.IdempotencyKey == "" {
		return "", fmt.Errorf("%w: personel avansı finans hareketi geçersiz", identity.ErrValidation)
	}
	amount, err := parsePositive(input.Amount, 2)
	if err != nil {
		return "", fmt.Errorf("%w: tutar iki ondalıklı pozitif TRY olmalıdır", identity.ErrValidation)
	}
	if err = ensureFinanceDate(ctx, tx, session.CurrentCompanyID, input.TransactionDate, s.now()); err != nil {
		return "", err
	}
	if err = ensurePeriodOpen(ctx, tx, session.CurrentCompanyID, input.TransactionDate); err != nil {
		return "", err
	}
	var accountType, currency string
	var active bool
	var branchID *string
	if err = tx.QueryRow(ctx, `SELECT account_type,currency,is_active,branch_id FROM finance_accounts WHERE company_id=$1 AND id=$2 FOR UPDATE`, session.CurrentCompanyID, input.AccountID).Scan(&accountType, &currency, &active, &branchID); errors.Is(err, pgx.ErrNoRows) {
		return "", identity.ErrForbidden
	} else if err != nil {
		return "", err
	}
	if !active {
		return "", domainError(ErrAccountInactive, "pasif hesaba hareket kaydedilemez")
	}
	if currency != "TRY" {
		return "", domainError(ErrCurrencyMismatch, "personel avansı yalnız TRY hesabıyla kaydedilebilir")
	}
	if err = ensureFinanceAccountAccess(ctx, tx, session, input.AccountID, branchID); err != nil {
		return "", err
	}
	if input.Direction == "OUT" {
		if err = enforceNegativeBalanceTx(ctx, tx, session, input.AccountID, amount, input.OverrideReason); err != nil {
			return "", err
		}
	}
	rate, baseCurrency, err := financeRateTx(ctx, tx, session.CurrentCompanyID, currency, input.TransactionDate)
	if err != nil {
		return "", err
	}
	id := uuid.NewString()
	snapshot := jsonBytes(map[string]any{"account_type": accountType, "currency": currency, "employee_advance": true})
	_, err = tx.Exec(ctx, `INSERT INTO finance_account_movements(id,company_id,account_id,movement_kind,direction,currency,amount,transaction_date,source_type,source_id,idempotency_key,description,external_reference,actor_user_id,snapshot,exchange_rate,base_currency,base_amount,reversal_of_id) VALUES($1,$2,$3,'EMPLOYEE_ADVANCE',$4,'TRY',$5,$6,'employee_advance_transaction',$7,$8,$9,$10,$11,$12,$13,$14,ROUND($5::numeric*$13::numeric,4),$15)`, id, session.CurrentCompanyID, input.AccountID, input.Direction, amountString(amount, 2), input.TransactionDate, input.SourceID, input.IdempotencyKey, input.Description, strings.TrimSpace(input.ExternalReference), nullableUUID(session.User.ID), snapshot, rate, baseCurrency, input.ReversalOfID)
	if err != nil {
		return "", mapFinanceConstraint(err)
	}
	return id, nil
}

// PayrollPaymentMovementInput is the narrow transaction port used by the HR
// payroll payment sub-ledger. The caller owns the surrounding transaction so
// the cash/bank movement and the payroll payment record commit or roll back
// together.
type PayrollPaymentMovementInput struct {
	AccountID         string
	Direction         string
	Amount            string
	TransactionDate   time.Time
	SourceID          string
	Description       string
	ExternalReference string
	IdempotencyKey    string
	OverrideReason    string
	ReversalOfID      *string
}

// PostPayrollPaymentMovementTx posts a TRY cash/bank movement for a payroll
// payment inside the caller's transaction. Authorization for the HR command
// remains with HR; finance enforces account visibility and posting invariants.
func (s *Service) PostPayrollPaymentMovementTx(ctx context.Context, tx pgx.Tx, session identity.Session, input PayrollPaymentMovementInput) (string, error) {
	if tx == nil {
		return "", fmt.Errorf("%w: finans işlemi için transaction gereklidir", identity.ErrValidation)
	}
	input.AccountID = strings.TrimSpace(input.AccountID)
	input.Direction = strings.ToUpper(strings.TrimSpace(input.Direction))
	input.Description = strings.TrimSpace(input.Description)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if uuid.Validate(input.AccountID) != nil || uuid.Validate(input.SourceID) != nil ||
		!contains([]string{"IN", "OUT"}, input.Direction) || input.TransactionDate.IsZero() || input.IdempotencyKey == "" {
		return "", fmt.Errorf("%w: bordro ödemesi finans hareketi geçersiz", identity.ErrValidation)
	}
	amount, err := parsePositive(input.Amount, 2)
	if err != nil {
		return "", fmt.Errorf("%w: tutar iki ondalıklı pozitif TRY olmalıdır", identity.ErrValidation)
	}
	if err = ensureFinanceDate(ctx, tx, session.CurrentCompanyID, input.TransactionDate, s.now()); err != nil {
		return "", err
	}
	if err = ensurePeriodOpen(ctx, tx, session.CurrentCompanyID, input.TransactionDate); err != nil {
		return "", err
	}
	var accountType, currency string
	var active bool
	var branchID *string
	if err = tx.QueryRow(ctx, `SELECT account_type,currency,is_active,branch_id FROM finance_accounts WHERE company_id=$1 AND id=$2 FOR UPDATE`, session.CurrentCompanyID, input.AccountID).Scan(&accountType, &currency, &active, &branchID); errors.Is(err, pgx.ErrNoRows) {
		return "", identity.ErrForbidden
	} else if err != nil {
		return "", err
	}
	if !active {
		return "", domainError(ErrAccountInactive, "pasif hesaba hareket kaydedilemez")
	}
	if currency != "TRY" {
		return "", domainError(ErrCurrencyMismatch, "bordro ödemesi yalnız TRY hesabıyla kaydedilebilir")
	}
	if err = ensureFinanceAccountAccess(ctx, tx, session, input.AccountID, branchID); err != nil {
		return "", err
	}
	if input.Direction == "OUT" {
		if err = enforceNegativeBalanceTx(ctx, tx, session, input.AccountID, amount, input.OverrideReason); err != nil {
			return "", err
		}
	}
	rate, baseCurrency, err := financeRateTx(ctx, tx, session.CurrentCompanyID, currency, input.TransactionDate)
	if err != nil {
		return "", err
	}
	id := uuid.NewString()
	snapshot := jsonBytes(map[string]any{"account_type": accountType, "currency": currency, "payroll_payment": true})
	_, err = tx.Exec(ctx, `INSERT INTO finance_account_movements(id,company_id,account_id,movement_kind,direction,currency,amount,transaction_date,source_type,source_id,idempotency_key,description,external_reference,actor_user_id,snapshot,exchange_rate,base_currency,base_amount,reversal_of_id) VALUES($1,$2,$3,'PAYROLL',$4,'TRY',$5,$6,'payroll_payment',$7,$8,$9,$10,$11,$12,$13,$14,ROUND($5::numeric*$13::numeric,4),$15)`, id, session.CurrentCompanyID, input.AccountID, input.Direction, amountString(amount, 2), input.TransactionDate, input.SourceID, input.IdempotencyKey, input.Description, strings.TrimSpace(input.ExternalReference), nullableUUID(session.User.ID), snapshot, rate, baseCurrency, input.ReversalOfID)
	if err != nil {
		return "", mapFinanceConstraint(err)
	}
	return id, nil
}

type AccountStatement struct {
	AccountID      string            `json:"account_id"`
	Currency       string            `json:"currency"`
	OpeningBalance string            `json:"opening_balance"`
	ClosingBalance string            `json:"closing_balance"`
	Items          []AccountMovement `json:"items"`
}

type FinanceTransferInput struct {
	FromAccountID     string    `json:"from_account_id"`
	ToAccountID       string    `json:"to_account_id"`
	Amount            string    `json:"amount"`
	TransactionDate   time.Time `json:"transaction_date"`
	Description       string    `json:"description"`
	ExternalReference string    `json:"external_reference,omitempty"`
	IdempotencyKey    string    `json:"idempotency_key,omitempty"`
	OverrideReason    string    `json:"override_reason,omitempty"`
}

type FinanceTransfer struct {
	ID                string    `json:"id"`
	FromAccountID     string    `json:"from_account_id"`
	FromAccountName   string    `json:"from_account_name,omitempty"`
	FromAccountCode   string    `json:"from_account_code,omitempty"`
	ToAccountID       string    `json:"to_account_id"`
	ToAccountName     string    `json:"to_account_name,omitempty"`
	ToAccountCode     string    `json:"to_account_code,omitempty"`
	OutMovementID     string    `json:"out_movement_id"`
	InMovementID      string    `json:"in_movement_id"`
	Currency          string    `json:"currency"`
	Amount            string    `json:"amount"`
	DocumentNo        string    `json:"document_no"`
	ExternalReference string    `json:"external_reference,omitempty"`
	Description       string    `json:"description"`
	TransactionDate   time.Time `json:"transaction_date"`
	Status            string    `json:"status"`
	ReversalOfID      *string   `json:"reversal_of_id,omitempty"`
	PostedAt          time.Time `json:"posted_at"`
}

func accountPermission(accountType, action string) string {
	prefix := "finance.cash_account."
	if strings.EqualFold(accountType, "BANK") {
		prefix = "finance.bank_account."
	}
	return prefix + action
}

func movementPermission(accountType, action string) string {
	prefix := "finance.cash_movement."
	if strings.EqualFold(accountType, "BANK") {
		prefix = "finance.bank_movement."
	}
	return prefix + action
}

func normalizeAccountInput(input AccountInput) (AccountInput, error) {
	input.AccountType = strings.ToUpper(strings.TrimSpace(input.AccountType))
	input.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))
	input.Code, input.Name = strings.TrimSpace(input.Code), strings.TrimSpace(input.Name)
	input.BankName = strings.TrimSpace(input.BankName)
	input.BankBranchName, input.BankBranchCode = strings.TrimSpace(input.BankBranchName), strings.TrimSpace(input.BankBranchCode)
	input.AccountNumber = strings.TrimSpace(input.AccountNumber)
	input.Description, input.Notes = strings.TrimSpace(input.Description), strings.TrimSpace(input.Notes)
	input.BranchID = strings.TrimSpace(input.BranchID)
	if !contains([]string{"CASH", "BANK"}, input.AccountType) || input.Code == "" || input.Name == "" || len(input.Currency) != 3 {
		return AccountInput{}, fmt.Errorf("%w: kasa veya banka hesabı ve geçerli alanlar gereklidir", identity.ErrValidation)
	}
	if input.BranchID != "" && uuid.Validate(input.BranchID) != nil {
		return AccountInput{}, fmt.Errorf("%w: şube kimliği geçersiz", identity.ErrValidation)
	}
	iban, err := NormalizeIBAN(input.IBAN)
	if err != nil {
		return AccountInput{}, err
	}
	input.IBAN = iban
	if input.AccountType == "CASH" && (input.BankName != "" || input.BankBranchName != "" || input.BankBranchCode != "" || input.IBAN != "" || input.AccountNumber != "") {
		return AccountInput{}, fmt.Errorf("%w: kasa hesabında banka bilgisi kullanılamaz", identity.ErrValidation)
	}
	return input, nil
}

// NormalizeIBAN performs country-neutral ISO 13616 normalization and Mod-97
// validation. It deliberately does not hard-code Turkish length or prefixes.
func NormalizeIBAN(value string) (string, error) {
	var normalized strings.Builder
	for _, char := range strings.ToUpper(strings.TrimSpace(value)) {
		if unicode.IsSpace(char) {
			continue
		}
		if !unicode.IsLetter(char) && !unicode.IsDigit(char) {
			return "", fmt.Errorf("%w: IBAN yalnız harf ve rakam içerebilir", identity.ErrValidation)
		}
		normalized.WriteRune(char)
	}
	iban := normalized.String()
	if iban == "" {
		return "", nil
	}
	if len(iban) < 15 || len(iban) > 34 || iban[0] < 'A' || iban[0] > 'Z' || iban[1] < 'A' || iban[1] > 'Z' || iban[2] < '0' || iban[2] > '9' || iban[3] < '0' || iban[3] > '9' {
		return "", fmt.Errorf("%w: IBAN biçimi geçersiz", identity.ErrValidation)
	}
	reordered := iban[4:] + iban[:4]
	remainder := 0
	for _, char := range reordered {
		if char >= '0' && char <= '9' {
			remainder = (remainder*10 + int(char-'0')) % 97
			continue
		}
		if char < 'A' || char > 'Z' {
			return "", fmt.Errorf("%w: IBAN biçimi geçersiz", identity.ErrValidation)
		}
		value := int(char-'A') + 10
		remainder = (remainder*100 + value) % 97
	}
	if remainder != 1 {
		return "", fmt.Errorf("%w: IBAN kontrol basamakları geçersiz", identity.ErrValidation)
	}
	return iban, nil
}

func ensureFinanceAccountAccess(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, session identity.Session, accountID string, branchID *string) error {
	if identity.ValidateExternalActor(session) != nil || uuid.Validate(accountID) != nil {
		return identity.ErrForbidden
	}
	if branchID != nil {
		if err := ensureBranchAccess(ctx, q, session.CurrentCompanyID, session.User.ID, *branchID); err != nil {
			return err
		}
	}
	var allowed bool
	if err := q.QueryRow(ctx, `SELECT NOT EXISTS(
		SELECT 1 FROM membership_finance_account_scopes WHERE company_id=$1 AND user_id=$2
	) OR EXISTS(
		SELECT 1 FROM membership_finance_account_scopes WHERE company_id=$1 AND user_id=$2 AND account_id=$3
	)`, session.CurrentCompanyID, session.User.ID, accountID).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return identity.ErrForbidden
	}
	return nil
}

func (s *Service) ensureAccountMovementAccess(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, session identity.Session, accountID string) error {
	var branchID *string
	if err := q.QueryRow(ctx, `SELECT branch_id FROM finance_accounts WHERE company_id=$1 AND id=$2`, session.CurrentCompanyID, accountID).Scan(&branchID); errors.Is(err, pgx.ErrNoRows) {
		return identity.ErrForbidden
	} else if err != nil {
		return err
	}
	return ensureFinanceAccountAccess(ctx, q, session, accountID, branchID)
}

func (s *Service) UpdateAccount(ctx context.Context, session identity.Session, id string, expectedVersion int64, input AccountInput, meta identity.RequestMeta) (Account, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Account{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var current Account
	var branchID *string
	err = tx.QueryRow(ctx, `SELECT id,company_id,account_type,code,name,currency,branch_id,bank_name,bank_branch_name,bank_branch_code,iban,account_number,description,notes,is_active,created_at,updated_at,version FROM finance_accounts WHERE company_id=$1 AND id=$2 FOR UPDATE`, session.CurrentCompanyID, id).Scan(&current.ID, &current.CompanyID, &current.AccountType, &current.Code, &current.Name, &current.Currency, &branchID, &current.BankName, &current.BankBranchName, &current.BankBranchCode, &current.IBAN, &current.AccountNumber, &current.Description, &current.Notes, &current.IsActive, &current.CreatedAt, &current.UpdatedAt, &current.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, identity.ErrForbidden
	}
	if err != nil {
		return Account{}, err
	}
	if !can(session, accountPermission(current.AccountType, "edit")) {
		return Account{}, identity.ErrForbidden
	}
	if err = ensureFinanceAccountAccess(ctx, tx, session, id, branchID); err != nil {
		return Account{}, err
	}
	if input.AccountType == "" {
		input.AccountType = current.AccountType
	}
	if input.Currency == "" {
		input.Currency = current.Currency
	}
	if current.AccountType == "BANK" && strings.TrimSpace(input.IBAN) == "" {
		input.IBAN = current.IBAN
	}
	input, err = normalizeAccountInput(input)
	if err != nil {
		return Account{}, err
	}
	if input.AccountType != current.AccountType || input.Currency != current.Currency {
		return Account{}, fmt.Errorf("%w: hesap türü ve para birimi değiştirilemez", identity.ErrValidation)
	}
	if input.BranchID != "" {
		if err = ensureBranchAccess(ctx, tx, session.CurrentCompanyID, session.User.ID, input.BranchID); err != nil {
			return Account{}, err
		}
	}
	currentBranch := ""
	if branchID != nil {
		currentBranch = *branchID
	}
	if currentBranch != input.BranchID {
		var moved bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM finance_account_movements WHERE company_id=$1 AND account_id=$2)`, session.CurrentCompanyID, id).Scan(&moved); err != nil {
			return Account{}, err
		}
		if moved {
			return Account{}, domainError(ErrAccountBranchImmutable, "hareket görmüş hesabın şubesi değiştirilemez")
		}
	}
	result, err := tx.Exec(ctx, `UPDATE finance_accounts SET code=$1,name=$2,branch_id=NULLIF($3,'')::uuid,bank_name=$4,bank_branch_name=$5,bank_branch_code=$6,iban=$7,account_number=$8,description=$9,notes=$10,updated_at=now(),version=version+1 WHERE company_id=$11 AND id=$12 AND version=$13`, input.Code, input.Name, input.BranchID, input.BankName, input.BankBranchName, input.BankBranchCode, input.IBAN, input.AccountNumber, input.Description, input.Notes, session.CurrentCompanyID, id, expectedVersion)
	if err != nil {
		return Account{}, mapFinanceConstraint(err)
	}
	if result.RowsAffected() != 1 {
		return Account{}, identity.ErrConflict
	}
	if err = writeAuditAndEventTx(ctx, tx, session, "FINANCE_ACCOUNT_UPDATED", "finance.account.updated", "finance_account", id, meta, map[string]any{"account_type": current.AccountType}); err != nil {
		return Account{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Account{}, err
	}
	return s.loadAccount(ctx, session, id, false)
}

func (s *Service) SetAccountActive(ctx context.Context, session identity.Session, id string, expectedVersion int64, active bool, meta identity.RequestMeta) (Account, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Account{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var accountType string
	var branchID *string
	err = tx.QueryRow(ctx, `SELECT account_type,branch_id FROM finance_accounts WHERE company_id=$1 AND id=$2 FOR UPDATE`, session.CurrentCompanyID, id).Scan(&accountType, &branchID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, identity.ErrForbidden
	}
	if err != nil {
		return Account{}, err
	}
	if !can(session, accountPermission(accountType, "deactivate")) {
		return Account{}, identity.ErrForbidden
	}
	if err = ensureFinanceAccountAccess(ctx, tx, session, id, branchID); err != nil {
		return Account{}, err
	}
	result, err := tx.Exec(ctx, `UPDATE finance_accounts SET is_active=$1,updated_at=now(),version=version+1 WHERE company_id=$2 AND id=$3 AND version=$4`, active, session.CurrentCompanyID, id, expectedVersion)
	if err != nil {
		return Account{}, err
	}
	if result.RowsAffected() != 1 {
		return Account{}, identity.ErrConflict
	}
	event := "FINANCE_ACCOUNT_DEACTIVATED"
	if active {
		event = "FINANCE_ACCOUNT_ACTIVATED"
	}
	if err = writeAuditAndEventTx(ctx, tx, session, event, strings.ToLower(strings.ReplaceAll(event, "_", ".")), "finance_account", id, meta, map[string]any{"active": active}); err != nil {
		return Account{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Account{}, err
	}
	return s.loadAccount(ctx, session, id, false)
}

func (s *Service) GetAccountBalance(ctx context.Context, session identity.Session, id string) (AccountBalance, error) {
	account, err := s.GetAccount(ctx, session, id)
	if err != nil {
		return AccountBalance{}, err
	}
	var balance string
	if err = s.pool.QueryRow(ctx, `SELECT COALESCE(SUM(CASE WHEN direction='IN' THEN amount ELSE -amount END),0)::text FROM finance_account_movements WHERE company_id=$1 AND account_id=$2`, session.CurrentCompanyID, id).Scan(&balance); err != nil {
		return AccountBalance{}, err
	}
	return AccountBalance{AccountID: id, Currency: account.Currency, Balance: amountString(mustRat(balance), 4)}, nil
}

func (s *Service) AccountStatement(ctx context.Context, session identity.Session, id string, from, to *time.Time, limit int) (AccountStatement, error) {
	account, err := s.GetAccount(ctx, session, id)
	if err != nil {
		return AccountStatement{}, err
	}
	if !can(session, movementPermission(account.AccountType, "read")) {
		return AccountStatement{}, identity.ErrForbidden
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	args := []any{session.CurrentCompanyID, id}
	query := accountMovementSelect + ` WHERE m.company_id=$1 AND m.account_id=$2`
	// Opening and closing are summed over the whole ledger in SQL, never over
	// the page below: a period with more movements than the page size would
	// otherwise report the balance after the first `limit` rows as the period
	// closing balance. This mirrors the cari ekstre (party.StatementReportPage).
	openingCondition, closingCondition := "false", "true"
	if from != nil {
		args = append(args, from.Format("2006-01-02"))
		openingCondition = fmt.Sprintf("m.transaction_date < $%d::date", len(args))
		query += fmt.Sprintf(` AND transaction_date >= $%d::date`, len(args))
	}
	if to != nil {
		args = append(args, to.Format("2006-01-02"))
		closingCondition = fmt.Sprintf("m.transaction_date <= $%d::date", len(args))
		query += fmt.Sprintf(` AND transaction_date <= $%d::date`, len(args))
	}
	const signedAmount = `CASE WHEN m.direction='IN' THEN m.amount ELSE -m.amount END`
	balanceQuery := fmt.Sprintf(`SELECT COALESCE(SUM(CASE WHEN %s THEN %s ELSE 0 END),0)::text,
		COALESCE(SUM(CASE WHEN %s THEN %s ELSE 0 END),0)::text
		FROM finance_account_movements m WHERE m.company_id=$1 AND m.account_id=$2`,
		openingCondition, signedAmount, closingCondition, signedAmount)
	var opening, closing string
	if err = s.pool.QueryRow(ctx, balanceQuery, args...).Scan(&opening, &closing); err != nil {
		return AccountStatement{}, err
	}
	query += fmt.Sprintf(` ORDER BY transaction_date,posted_at,m.id LIMIT $%d`, len(args)+1)
	args = append(args, limit)
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return AccountStatement{}, err
	}
	defer rows.Close()
	items := make([]AccountMovement, 0)
	for rows.Next() {
		var item AccountMovement
		var advanceID *string
		if err = rows.Scan(&item.ID, &item.AccountID, &item.AccountType, &item.MovementKind, &item.Direction, &item.Currency, &item.Amount, &item.TransactionDate, &item.SourceType, &item.SourceID, &item.Description, &item.ExternalReference, &item.ReversalOfID, &item.ExchangeRate, &item.BaseCurrency, &item.BaseAmount, &item.PostedAt, &advanceID); err != nil {
			return AccountStatement{}, err
		}
		decorateMovementSource(&item, advanceID)
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return AccountStatement{}, err
	}
	return AccountStatement{AccountID: id, Currency: account.Currency, OpeningBalance: amountString(mustRat(opening), 4), ClosingBalance: amountString(mustRat(closing), 4), Items: items}, nil
}

func ensureFinanceDate(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, companyID string, date time.Time, now time.Time) error {
	var timezone string
	if err := q.QueryRow(ctx, `SELECT timezone FROM companies WHERE id=$1`, companyID).Scan(&timezone); err != nil {
		return err
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return err
	}
	today := now.In(location).Format("2006-01-02")
	if date.Format("2006-01-02") > today {
		return domainError(ErrFutureFinanceDateNotAllowed, "gelecek tarihli finans hareketi kaydedilemez")
	}
	return nil
}

// maxFinanceRateAgeDays bounds how far a cash/bank movement may reach back for
// its rate. Documents resolve through exchange.ResolveRate, which refreshes and
// fails closed when a provider is down; this path reads the stored table
// directly, so without a bound a company whose rate feed had been dead for
// months would keep converting foreign movements at a months-old rate and never
// be told. The bound is deliberately far wider than any publishing gap -- TCMB
// skips weekends and public holidays, at worst about ten days around a bayram --
// so a healthy installation never meets it.
const maxFinanceRateAgeDays = 30

func financeRateTx(ctx context.Context, tx pgx.Tx, companyID, currency string, date time.Time) (string, string, error) {
	var base string
	if err := tx.QueryRow(ctx, `SELECT base_currency FROM companies WHERE id=$1`, companyID).Scan(&base); err != nil {
		return "", "", err
	}
	if currency == base {
		return "1.0000000000", base, nil
	}
	var rate string
	var ageDays int
	err := tx.QueryRow(ctx, `SELECT rate_to_base::text,($3::date - rate_date) FROM exchange_rates WHERE company_id=$1 AND currency_code=$2 AND rate_date <= $3::date ORDER BY rate_date DESC,fetched_at DESC LIMIT 1`, companyID, currency, date.Format("2006-01-02")).Scan(&rate, &ageDays)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", domainError(ErrExchangeRateRequired, "işlem tarihi için döviz kuru bulunamadı")
	}
	if err != nil {
		return "", "", err
	}
	if ageDays > maxFinanceRateAgeDays {
		return "", "", domainError(ErrExchangeRateRequired, fmt.Sprintf("%s kuru %d gündür güncellenmemiş; Ayarlar'dan kurları yenileyin", currency, ageDays))
	}
	return NormalizeRateText(rate), base, nil
}

func enforceNegativeBalanceTx(ctx context.Context, tx pgx.Tx, session identity.Session, accountID string, outAmount *big.Rat, reason string) error {
	var policy, balance string
	if err := tx.QueryRow(ctx, `SELECT c.finance_negative_balance_policy,COALESCE(SUM(CASE WHEN m.direction='IN' THEN m.amount ELSE -m.amount END),0)::text FROM companies c LEFT JOIN finance_account_movements m ON m.company_id=c.id AND m.account_id=$2 WHERE c.id=$1 GROUP BY c.finance_negative_balance_policy`, session.CurrentCompanyID, accountID).Scan(&policy, &balance); err != nil {
		return err
	}
	remaining := new(big.Rat).Sub(mustRat(balance), outAmount)
	if remaining.Sign() >= 0 || policy == "ALLOW" {
		return nil
	}
	if policy == "BLOCK" {
		return domainError(ErrNegativeBalanceBlocked, "hesap bakiyesi bu çıkış için yetersiz")
	}
	if !can(session, "finance.negative_balance.override") || strings.TrimSpace(reason) == "" {
		return domainError(ErrNegativeBalanceConfirmation, "negatif bakiye için yetkili onayı ve gerekçe gereklidir")
	}
	return nil
}

func (s *Service) PostOpeningBalance(ctx context.Context, session identity.Session, input AccountMovementInput, meta identity.RequestMeta) (AccountMovement, error) {
	return s.postAccountMovement(ctx, session, "OPENING_BALANCE", input, meta)
}

func (s *Service) PostManualAccountMovement(ctx context.Context, session identity.Session, input AccountMovementInput, meta identity.RequestMeta) (AccountMovement, error) {
	kind := "MANUAL_IN"
	if strings.EqualFold(input.Direction, "OUT") {
		kind = "MANUAL_OUT"
	}
	return s.postAccountMovement(ctx, session, kind, input, meta)
}

func (s *Service) postAccountMovement(ctx context.Context, session identity.Session, kind string, input AccountMovementInput, meta identity.RequestMeta) (AccountMovement, error) {
	input.AccountID, input.Direction = strings.TrimSpace(input.AccountID), strings.ToUpper(strings.TrimSpace(input.Direction))
	input.Description, input.ExternalReference, input.IdempotencyKey = strings.TrimSpace(input.Description), strings.TrimSpace(input.ExternalReference), strings.TrimSpace(input.IdempotencyKey)
	accountUUID, accountErr := uuid.Parse(input.AccountID)
	if accountErr != nil || !contains([]string{"IN", "OUT"}, input.Direction) || input.TransactionDate.IsZero() || input.IdempotencyKey == "" {
		return AccountMovement{}, fmt.Errorf("%w: hesap hareketi alanları geçersiz", identity.ErrValidation)
	}
	input.AccountID = accountUUID.String()
	if kind != "OPENING_BALANCE" && input.Description == "" {
		return AccountMovement{}, fmt.Errorf("%w: manuel hareket açıklaması zorunludur", identity.ErrValidation)
	}
	amount, err := parsePositive(input.Amount, 4)
	if err != nil {
		return AccountMovement{}, fmt.Errorf("%w: tutar geçersiz", identity.ErrValidation)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return AccountMovement{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var existing AccountMovement
	err = scanAccountMovement(tx.QueryRow(ctx, accountMovementSelect+` WHERE m.company_id=$1 AND m.idempotency_key=$2`, session.CurrentCompanyID, input.IdempotencyKey), &existing)
	if err == nil {
		// Replays must pass the same account permission and branch scope as new
		// posts; otherwise an idempotency key could be used to read a movement
		// from an account the caller cannot access.
		if !can(session, movementPermission(existing.AccountType, "post")) {
			return AccountMovement{}, identity.ErrForbidden
		}
		if accessErr := s.ensureAccountMovementAccess(ctx, tx, session, existing.AccountID); accessErr != nil {
			return AccountMovement{}, accessErr
		}
		if existing.AccountID != input.AccountID || existing.MovementKind != kind || existing.Direction != input.Direction || existing.Amount != amountString(amount, 4) || existing.TransactionDate.Format("2006-01-02") != input.TransactionDate.Format("2006-01-02") || existing.Description != input.Description || existing.ExternalReference != input.ExternalReference {
			return AccountMovement{}, domainError(ErrIdempotencyConflict, "aynı anahtar farklı hesap hareketi verisiyle kullanıldı")
		}
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return AccountMovement{}, err
	}
	if err = ensureFinanceDate(ctx, tx, session.CurrentCompanyID, input.TransactionDate, s.now()); err != nil {
		return AccountMovement{}, err
	}
	if err = ensurePeriodOpen(ctx, tx, session.CurrentCompanyID, input.TransactionDate); err != nil {
		return AccountMovement{}, err
	}
	var accountType, currency string
	var active bool
	var branchID *string
	if err = tx.QueryRow(ctx, `SELECT account_type,currency,is_active,branch_id FROM finance_accounts WHERE company_id=$1 AND id=$2 FOR UPDATE`, session.CurrentCompanyID, input.AccountID).Scan(&accountType, &currency, &active, &branchID); errors.Is(err, pgx.ErrNoRows) {
		return AccountMovement{}, identity.ErrForbidden
	}
	if err != nil {
		return AccountMovement{}, err
	}
	if !active {
		return AccountMovement{}, domainError(ErrAccountInactive, "pasif hesaba hareket kaydedilemez")
	}
	if !can(session, movementPermission(accountType, "post")) {
		return AccountMovement{}, identity.ErrForbidden
	}
	if err = ensureFinanceAccountAccess(ctx, tx, session, input.AccountID, branchID); err != nil {
		return AccountMovement{}, err
	}
	if kind == "OPENING_BALANCE" {
		var exists bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM finance_account_movements WHERE company_id=$1 AND account_id=$2)`, session.CurrentCompanyID, input.AccountID).Scan(&exists); err != nil {
			return AccountMovement{}, err
		}
		if exists {
			return AccountMovement{}, domainError(ErrOpeningBalanceExists, "açılış bakiyesi yalnız hareketsiz hesaba kaydedilebilir")
		}
	}
	if input.Direction == "OUT" {
		if err = enforceNegativeBalanceTx(ctx, tx, session, input.AccountID, amount, input.OverrideReason); err != nil {
			return AccountMovement{}, err
		}
	}
	rate, baseCurrency, err := financeRateTx(ctx, tx, session.CurrentCompanyID, currency, input.TransactionDate)
	if err != nil {
		return AccountMovement{}, err
	}
	rateValue, rateErr := parseRate(rate)
	if rateErr != nil {
		return AccountMovement{}, fmt.Errorf("%w: işlem tarihindeki kur değeri geçersiz", identity.ErrValidation)
	}
	if amountString(new(big.Rat).Mul(amount, rateValue), 4) == "0.0000" {
		return AccountMovement{}, fmt.Errorf("%w: temel para birimine çevrilen tutar dört ondalıkta sıfıra yuvarlanamaz", identity.ErrValidation)
	}
	id := uuid.NewString()
	snapshot := jsonBytes(map[string]any{"account_type": accountType, "currency": currency, "amount": amountString(amount, 4), "exchange_rate": rate, "override_reason": strings.TrimSpace(input.OverrideReason)})
	_, err = tx.Exec(ctx, `INSERT INTO finance_account_movements(id,company_id,account_id,movement_kind,direction,currency,amount,transaction_date,source_type,source_id,idempotency_key,description,external_reference,actor_user_id,snapshot,exchange_rate,base_currency,base_amount) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'finance_account_movement',$1,$9,$10,$11,$12,$13,$14,$15,ROUND($7::numeric*$14::numeric,4))`, id, session.CurrentCompanyID, input.AccountID, kind, input.Direction, currency, amountString(amount, 4), input.TransactionDate, input.IdempotencyKey, input.Description, input.ExternalReference, nullableUUID(session.User.ID), snapshot, rate, baseCurrency)
	if err != nil {
		return AccountMovement{}, mapFinanceConstraint(err)
	}
	if err = writeAuditAndEventTx(ctx, tx, session, "FINANCE_ACCOUNT_MOVEMENT_POSTED", movementOutboxType(accountType), "finance_account_movement", id, meta, map[string]any{"movement_kind": kind, "direction": input.Direction, "account_id": input.AccountID}); err != nil {
		return AccountMovement{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return AccountMovement{}, err
	}
	return s.loadAccountMovement(ctx, session, id, false)
}

const accountMovementSelect = `SELECT m.id,m.account_id,a.account_type,m.movement_kind,m.direction,m.currency,m.amount::text,m.transaction_date,m.source_type,m.source_id,m.description,m.external_reference,m.reversal_of_id,m.exchange_rate::text,m.base_currency,m.base_amount::text,m.posted_at,(SELECT eat.advance_id::text FROM employee_advance_transactions eat WHERE eat.company_id=m.company_id AND eat.id=m.source_id AND m.source_type='employee_advance_transaction') AS source_advance_id FROM finance_account_movements m JOIN finance_accounts a ON a.company_id=m.company_id AND a.id=m.account_id`

func scanAccountMovement(row pgx.Row, item *AccountMovement) error {
	var advanceID *string
	if err := row.Scan(&item.ID, &item.AccountID, &item.AccountType, &item.MovementKind, &item.Direction, &item.Currency, &item.Amount, &item.TransactionDate, &item.SourceType, &item.SourceID, &item.Description, &item.ExternalReference, &item.ReversalOfID, &item.ExchangeRate, &item.BaseCurrency, &item.BaseAmount, &item.PostedAt, &advanceID); err != nil {
		return err
	}
	decorateMovementSource(item, advanceID)
	return nil
}

// decorateMovementSource fills SourceLabel/SourceHref from the movement's
// source_type + movement_kind so the UI can name and link the originating
// record. advanceID is the resolved employee_advance id for advance movements.
func decorateMovementSource(m *AccountMovement, advanceID *string) {
	switch m.SourceType {
	case "employee_advance_transaction":
		m.SourceLabel = "Personel avansı"
		if advanceID != nil && *advanceID != "" {
			m.SourceHref = "/personel/avanslar/" + *advanceID
		}
	case "finance_payment":
		switch m.MovementKind {
		case "COLLECTION":
			m.SourceLabel, m.SourceHref = "Tahsilat", "/cari/tahsilatlar/"+m.SourceID
		case "PAYMENT":
			m.SourceLabel, m.SourceHref = "Ödeme", "/cari/odemeler/"+m.SourceID
		default:
			m.SourceLabel = "Tahsilat / ödeme ters kaydı"
		}
	case "finance_transfer":
		m.SourceLabel, m.SourceHref = "Hesap transferi", "/finans/transferler/"+m.SourceID
	case "payroll_payment":
		// SourceID is the payroll_payments row; the link to the bordro run is
		// resolved on single-record reads (see loadAccountMovement).
		if m.MovementKind == "REVERSAL" {
			m.SourceLabel = "Bordro ödemesi ters kaydı"
		} else {
			m.SourceLabel = "Bordro ödemesi"
		}
	case "finance_account_movement":
		if m.MovementKind == "OPENING_BALANCE" {
			m.SourceLabel = "Açılış bakiyesi"
		} else {
			m.SourceLabel = "Manuel hareket"
		}
	}
}

func movementOutboxType(accountType string) string {
	if accountType == "BANK" {
		return "finance.bank_movement.posted"
	}
	return "finance.cash_movement.posted"
}

func (s *Service) GetAccountMovement(ctx context.Context, session identity.Session, id string) (AccountMovement, error) {
	return s.loadAccountMovement(ctx, session, id, true)
}

func (s *Service) loadAccountMovement(ctx context.Context, session identity.Session, id string, requireRead bool) (AccountMovement, error) {
	var item AccountMovement
	err := scanAccountMovement(s.pool.QueryRow(ctx, accountMovementSelect+` WHERE m.company_id=$1 AND m.id=$2`, session.CurrentCompanyID, id), &item)
	if errors.Is(err, pgx.ErrNoRows) {
		return AccountMovement{}, identity.ErrForbidden
	}
	if err != nil {
		return AccountMovement{}, err
	}
	if requireRead && !can(session, movementPermission(item.AccountType, "read")) {
		return AccountMovement{}, identity.ErrForbidden
	}
	_ = s.pool.QueryRow(ctx, `SELECT COALESCE(u.display_name,u.email,'') FROM finance_account_movements m LEFT JOIN users u ON u.id=m.actor_user_id WHERE m.company_id=$1 AND m.id=$2`, session.CurrentCompanyID, id).Scan(&item.ActorName)
	if item.SourceType == "payroll_payment" && item.SourceID != "" {
		var runID string
		if s.pool.QueryRow(ctx, `SELECT payroll_run_id::text FROM payroll_payments WHERE company_id=$1 AND id=$2`, session.CurrentCompanyID, item.SourceID).Scan(&runID) == nil && runID != "" {
			item.SourceHref = "/personel/bordro/" + runID
		}
	}
	account, err := s.loadAccount(ctx, session, item.AccountID, requireRead)
	if err != nil || account.ID == "" {
		return AccountMovement{}, identity.ErrForbidden
	}
	return item, nil
}

func (s *Service) ListAccountMovements(ctx context.Context, session identity.Session, accountType, accountID string, from, to *time.Time, limit int) ([]AccountMovement, error) {
	accountType = strings.ToUpper(strings.TrimSpace(accountType))
	if !contains([]string{"CASH", "BANK"}, accountType) || !can(session, movementPermission(accountType, "read")) {
		return nil, identity.ErrForbidden
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	args := []any{session.CurrentCompanyID, accountType, session.User.ID}
	query := accountMovementSelect + ` WHERE m.company_id=$1 AND a.account_type=$2
		AND (a.branch_id IS NULL OR NOT EXISTS(SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=$1 AND bs.user_id=$3) OR EXISTS(SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=$1 AND bs.user_id=$3 AND bs.branch_id=a.branch_id))
		AND (NOT EXISTS(SELECT 1 FROM membership_finance_account_scopes fas WHERE fas.company_id=$1 AND fas.user_id=$3) OR EXISTS(SELECT 1 FROM membership_finance_account_scopes fas WHERE fas.company_id=$1 AND fas.user_id=$3 AND fas.account_id=a.id))`
	if strings.TrimSpace(accountID) != "" {
		args = append(args, strings.TrimSpace(accountID))
		query += fmt.Sprintf(` AND m.account_id=$%d`, len(args))
	}
	if from != nil {
		args = append(args, from.Format("2006-01-02"))
		query += fmt.Sprintf(` AND m.transaction_date >= $%d::date`, len(args))
	}
	if to != nil {
		args = append(args, to.Format("2006-01-02"))
		query += fmt.Sprintf(` AND m.transaction_date <= $%d::date`, len(args))
	}
	args = append(args, limit)
	query += fmt.Sprintf(` ORDER BY m.transaction_date DESC,m.posted_at DESC,m.id DESC LIMIT $%d`, len(args))
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]AccountMovement, 0)
	for rows.Next() {
		var item AccountMovement
		var advanceID *string
		if err = rows.Scan(&item.ID, &item.AccountID, &item.AccountType, &item.MovementKind, &item.Direction, &item.Currency, &item.Amount, &item.TransactionDate, &item.SourceType, &item.SourceID, &item.Description, &item.ExternalReference, &item.ReversalOfID, &item.ExchangeRate, &item.BaseCurrency, &item.BaseAmount, &item.PostedAt, &advanceID); err != nil {
			return nil, err
		}
		decorateMovementSource(&item, advanceID)
		items = append(items, item)
	}
	return items, rows.Err()
}

// AccountMovementPage is the cursor-paginated read model for the unified
// "Banka & Kasa" movement grid.
type AccountMovementPage struct {
	Items      []AccountMovement `json:"items"`
	NextCursor string            `json:"next_cursor,omitempty"`
}

// ListAllAccountMovements is the unified "Banka & Kasa" movement grid. It merges
// cash and bank movements the caller is allowed to read into one list and keeps
// the same branch/account scope filters as ListAccountMovements. The scope
// filters live in SQL, so a stable (transaction_date, posted_at, id) cursor is
// safe here.
func (s *Service) ListAllAccountMovements(ctx context.Context, session identity.Session, accountID, direction, search, cursor string, from, to *time.Time, limit int) (AccountMovementPage, error) {
	allowed := make([]string, 0, 2)
	if can(session, movementPermission("CASH", "read")) {
		allowed = append(allowed, "CASH")
	}
	if can(session, movementPermission("BANK", "read")) {
		allowed = append(allowed, "BANK")
	}
	if len(allowed) == 0 {
		return AccountMovementPage{}, identity.ErrForbidden
	}
	direction = strings.ToUpper(strings.TrimSpace(direction))
	if direction != "" && !contains([]string{"IN", "OUT"}, direction) {
		return AccountMovementPage{}, fmt.Errorf("%w: hareket yönü geçersiz", identity.ErrValidation)
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	args := []any{session.CurrentCompanyID, allowed, session.User.ID}
	query := accountMovementSelect + ` WHERE m.company_id=$1 AND a.account_type = ANY($2)
		AND (a.branch_id IS NULL OR NOT EXISTS(SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=$1 AND bs.user_id=$3) OR EXISTS(SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=$1 AND bs.user_id=$3 AND bs.branch_id=a.branch_id))
		AND (NOT EXISTS(SELECT 1 FROM membership_finance_account_scopes fas WHERE fas.company_id=$1 AND fas.user_id=$3) OR EXISTS(SELECT 1 FROM membership_finance_account_scopes fas WHERE fas.company_id=$1 AND fas.user_id=$3 AND fas.account_id=a.id))`
	if strings.TrimSpace(accountID) != "" {
		args = append(args, strings.TrimSpace(accountID))
		query += fmt.Sprintf(` AND m.account_id=$%d`, len(args))
	}
	if direction != "" {
		args = append(args, direction)
		query += fmt.Sprintf(` AND m.direction=$%d`, len(args))
	}
	if from != nil {
		args = append(args, from.Format("2006-01-02"))
		query += fmt.Sprintf(` AND m.transaction_date >= $%d::date`, len(args))
	}
	if to != nil {
		args = append(args, to.Format("2006-01-02"))
		query += fmt.Sprintf(` AND m.transaction_date <= $%d::date`, len(args))
	}
	patterns, patternErr := searchTokens(search)
	if patternErr != nil {
		return AccountMovementPage{}, patternErr
	}
	for _, pattern := range patterns {
		args = append(args, pattern)
		param := len(args)
		query += fmt.Sprintf(` AND (
			m.description ILIKE $%d ESCAPE '\'
			OR COALESCE(m.external_reference,'') ILIKE $%d ESCAPE '\'
			OR m.movement_kind ILIKE $%d ESCAPE '\'
			OR m.currency ILIKE $%d ESCAPE '\'
			OR a.code ILIKE $%d ESCAPE '\'
			OR a.name ILIKE $%d ESCAPE '\'
		)`, param, param, param, param, param, param)
	}
	if strings.TrimSpace(cursor) != "" {
		lastDate, lastPostedAt, lastID, decodeErr := decodePaymentCursor(cursor)
		if decodeErr != nil {
			return AccountMovementPage{}, fmt.Errorf("%w: hareket listesi cursor bilgisi geçersiz", identity.ErrValidation)
		}
		args = append(args, lastDate.Format("2006-01-02"), lastPostedAt, lastID)
		query += fmt.Sprintf(` AND (m.transaction_date,m.posted_at,m.id) < ($%d::date,$%d::timestamptz,$%d::uuid)`, len(args)-2, len(args)-1, len(args))
	}
	args = append(args, limit+1)
	query += fmt.Sprintf(` ORDER BY m.transaction_date DESC,m.posted_at DESC,m.id DESC LIMIT $%d`, len(args))
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return AccountMovementPage{}, err
	}
	defer rows.Close()
	items := make([]AccountMovement, 0)
	for rows.Next() {
		var item AccountMovement
		var advanceID *string
		if err = rows.Scan(&item.ID, &item.AccountID, &item.AccountType, &item.MovementKind, &item.Direction, &item.Currency, &item.Amount, &item.TransactionDate, &item.SourceType, &item.SourceID, &item.Description, &item.ExternalReference, &item.ReversalOfID, &item.ExchangeRate, &item.BaseCurrency, &item.BaseAmount, &item.PostedAt, &advanceID); err != nil {
			return AccountMovementPage{}, err
		}
		decorateMovementSource(&item, advanceID)
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return AccountMovementPage{}, err
	}
	page := AccountMovementPage{Items: items}
	if len(items) > limit {
		last := items[limit-1]
		page.Items = items[:limit]
		page.NextCursor = encodePaymentCursor(last.TransactionDate, last.PostedAt, last.ID)
	}
	return page, nil
}

func (s *Service) PostFinanceTransfer(ctx context.Context, session identity.Session, input FinanceTransferInput, meta identity.RequestMeta) (FinanceTransfer, error) {
	return s.postFinanceTransfer(ctx, session, input, nil, meta)
}

func (s *Service) postFinanceTransfer(ctx context.Context, session identity.Session, input FinanceTransferInput, reversalOf *string, meta identity.RequestMeta) (FinanceTransfer, error) {
	input.FromAccountID, input.ToAccountID = strings.TrimSpace(input.FromAccountID), strings.TrimSpace(input.ToAccountID)
	input.IdempotencyKey, input.Description, input.ExternalReference = strings.TrimSpace(input.IdempotencyKey), strings.TrimSpace(input.Description), strings.TrimSpace(input.ExternalReference)
	fromUUID, fromErr := uuid.Parse(input.FromAccountID)
	toUUID, toErr := uuid.Parse(input.ToAccountID)
	// A reversal is an append-only correction and is authorized by the
	// dedicated reverse permission; it must not accidentally require the
	// broader create/post permission as well.  Normal transfers still require
	// finance.transfer.post.
	transferPermission := "finance.transfer.post"
	if reversalOf != nil {
		transferPermission = "finance.transfer.reverse"
	}
	if !can(session, transferPermission) || fromErr != nil || toErr != nil {
		return FinanceTransfer{}, identity.ErrForbidden
	}
	input.FromAccountID, input.ToAccountID = fromUUID.String(), toUUID.String()
	// Description backs a non-empty CHECK on the paired movement rows; default it
	// rather than rejecting an otherwise valid transfer.
	if input.Description == "" {
		input.Description = "Hesaplar arası transfer"
	}
	if input.FromAccountID == input.ToAccountID || input.IdempotencyKey == "" || input.TransactionDate.IsZero() {
		return FinanceTransfer{}, fmt.Errorf("%w: transfer alanları geçersiz", identity.ErrValidation)
	}
	amount, err := parsePositive(input.Amount, 4)
	if err != nil {
		return FinanceTransfer{}, fmt.Errorf("%w: tutar geçersiz", identity.ErrValidation)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return FinanceTransfer{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	existing, err := getFinanceTransferByKey(ctx, tx, session.CurrentCompanyID, input.IdempotencyKey)
	if err == nil {
		if existing.FromAccountID != input.FromAccountID || existing.ToAccountID != input.ToAccountID || existing.Amount != amountString(amount, 4) || existing.Description != input.Description || existing.ExternalReference != input.ExternalReference || existing.TransactionDate.Format("2006-01-02") != input.TransactionDate.Format("2006-01-02") {
			return FinanceTransfer{}, domainError(ErrIdempotencyConflict, "aynı anahtar farklı transfer verisiyle kullanıldı")
		}
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return FinanceTransfer{}, err
	}
	if err = ensureFinanceDate(ctx, tx, session.CurrentCompanyID, input.TransactionDate, s.now()); err != nil {
		return FinanceTransfer{}, err
	}
	if err = ensurePeriodOpen(ctx, tx, session.CurrentCompanyID, input.TransactionDate); err != nil {
		return FinanceTransfer{}, err
	}
	ids := []string{input.FromAccountID, input.ToAccountID}
	sort.Strings(ids)
	type lockedAccount struct {
		accountType, currency string
		branchID              *string
		active                bool
	}
	accounts := map[string]lockedAccount{}
	rows, err := tx.Query(ctx, `SELECT id,account_type,currency,is_active,branch_id FROM finance_accounts WHERE company_id=$1 AND id = ANY($2) ORDER BY id FOR UPDATE`, session.CurrentCompanyID, ids)
	if err != nil {
		return FinanceTransfer{}, err
	}
	for rows.Next() {
		var id string
		var item lockedAccount
		if err = rows.Scan(&id, &item.accountType, &item.currency, &item.active, &item.branchID); err != nil {
			rows.Close()
			return FinanceTransfer{}, err
		}
		accounts[id] = item
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return FinanceTransfer{}, err
	}
	if len(accounts) != 2 {
		return FinanceTransfer{}, identity.ErrForbidden
	}
	from, to := accounts[input.FromAccountID], accounts[input.ToAccountID]
	if !from.active || !to.active {
		return FinanceTransfer{}, domainError(ErrAccountInactive, "pasif hesap transferde kullanılamaz")
	}
	if from.currency != to.currency {
		return FinanceTransfer{}, domainError(ErrCurrencyMismatch, "transfer hesaplarının para birimleri aynı olmalıdır")
	}
	if err = ensureFinanceAccountAccess(ctx, tx, session, input.FromAccountID, from.branchID); err != nil {
		return FinanceTransfer{}, err
	}
	if err = ensureFinanceAccountAccess(ctx, tx, session, input.ToAccountID, to.branchID); err != nil {
		return FinanceTransfer{}, err
	}
	if err = enforceNegativeBalanceTx(ctx, tx, session, input.FromAccountID, amount, input.OverrideReason); err != nil {
		return FinanceTransfer{}, err
	}
	var rate, baseCurrency string
	if reversalOf != nil {
		// A transfer reversal must use the immutable source rate. Looking up a
		// rate for the reversal date would leave a base-currency residual when
		// the FX rate changed between the original and its correction (and would
		// incorrectly reject a valid reversal if today's rate is unavailable).
		var originalCurrency string
		err = tx.QueryRow(ctx, `SELECT m.exchange_rate::text,m.currency,COALESCE(NULLIF(m.base_currency,''),c.base_currency)
			FROM finance_transfers t
			JOIN finance_account_movements m ON m.company_id=t.company_id AND m.id=t.out_movement_id
			JOIN companies c ON c.id=t.company_id
			WHERE t.company_id=$1 AND t.id=$2`, session.CurrentCompanyID, *reversalOf).Scan(&rate, &originalCurrency, &baseCurrency)
		if errors.Is(err, pgx.ErrNoRows) {
			return FinanceTransfer{}, identity.ErrForbidden
		}
		if err != nil {
			return FinanceTransfer{}, err
		}
		if originalCurrency != from.currency {
			return FinanceTransfer{}, domainError(ErrCurrencyMismatch, "transfer ters kaydı özgün para birimiyle eşleşmiyor")
		}
	} else {
		rate, baseCurrency, err = financeRateTx(ctx, tx, session.CurrentCompanyID, from.currency, input.TransactionDate)
		if err != nil {
			return FinanceTransfer{}, err
		}
	}
	rateValue, rateErr := parsePositive(rate, 10)
	if rateErr != nil || amountString(new(big.Rat).Mul(amount, rateValue), 4) == "0.0000" {
		return FinanceTransfer{}, fmt.Errorf("%w: temel para birimine çevrilen tutar dört ondalıkta sıfıra yuvarlanamaz", identity.ErrValidation)
	}
	id, outID, inID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	documentNo, err := nextPaymentNumberTx(ctx, tx, session.CurrentCompanyID, "TRF", input.TransactionDate)
	if err != nil {
		return FinanceTransfer{}, err
	}
	snapshot := jsonBytes(map[string]any{"from_account_id": input.FromAccountID, "to_account_id": input.ToAccountID, "currency": from.currency, "amount": amountString(amount, 4), "exchange_rate": rate, "override_reason": strings.TrimSpace(input.OverrideReason)})
	for _, movement := range []struct{ id, accountID, direction string }{{outID, input.FromAccountID, "OUT"}, {inID, input.ToAccountID, "IN"}} {
		_, err = tx.Exec(ctx, `INSERT INTO finance_account_movements(id,company_id,account_id,movement_kind,direction,currency,amount,transaction_date,source_type,source_id,idempotency_key,description,external_reference,actor_user_id,snapshot,exchange_rate,base_currency,base_amount) VALUES($1,$2,$3,'TRANSFER',$4,$5,$6,$7,'finance_transfer',$8,$9,$10,$11,$12,$13,$14,$15,ROUND($6::numeric*$14::numeric,4))`, movement.id, session.CurrentCompanyID, movement.accountID, movement.direction, from.currency, amountString(amount, 4), input.TransactionDate, id, "transfer:"+input.IdempotencyKey+":"+strings.ToLower(movement.direction), input.Description, input.ExternalReference, nullableUUID(session.User.ID), snapshot, rate, baseCurrency)
		if err != nil {
			return FinanceTransfer{}, mapFinanceConstraint(err)
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO finance_transfers(id,company_id,from_account_id,to_account_id,out_movement_id,in_movement_id,currency,amount,document_no,external_reference,description,transaction_date,idempotency_key,reversal_of_id,actor_user_id,snapshot) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`, id, session.CurrentCompanyID, input.FromAccountID, input.ToAccountID, outID, inID, from.currency, amountString(amount, 4), documentNo, input.ExternalReference, input.Description, input.TransactionDate, input.IdempotencyKey, reversalOf, nullableUUID(session.User.ID), snapshot)
	if err != nil {
		return FinanceTransfer{}, mapFinanceConstraint(err)
	}
	event := "FinanceTransferPosted"
	audit := "FINANCE_TRANSFER_POSTED"
	if reversalOf != nil {
		event = "FinanceTransferReversed"
		audit = "FINANCE_TRANSFER_REVERSED"
	}
	if err = writeAuditAndEventTx(ctx, tx, session, audit, event, "finance_transfer", id, meta, map[string]any{"transfer_id": id, "reversal_of_id": reversalOf}); err != nil {
		return FinanceTransfer{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return FinanceTransfer{}, err
	}
	return s.loadFinanceTransfer(ctx, session, id, false)
}

func (s *Service) ReverseFinanceTransfer(ctx context.Context, session identity.Session, id, key, reason string, date time.Time, meta identity.RequestMeta) (FinanceTransfer, error) {
	if !can(session, "finance.transfer.reverse") || strings.TrimSpace(reason) == "" || strings.TrimSpace(key) == "" {
		return FinanceTransfer{}, identity.ErrForbidden
	}
	original, err := s.loadFinanceTransfer(ctx, session, id, false)
	if err != nil {
		return FinanceTransfer{}, err
	}
	// A retry with the same reversal key is an idempotent replay. Check the key
	// before the source's already-reversed guard so a successful first request
	// is returned instead of being reported as a new duplicate reversal.
	if existing, existingErr := getFinanceTransferByKey(ctx, s.pool, session.CurrentCompanyID, strings.TrimSpace(key)); existingErr == nil {
		if existing.ReversalOfID != nil && *existing.ReversalOfID == id && existing.Description == "Ters kayıt: "+strings.TrimSpace(reason) {
			return existing, nil
		}
		return FinanceTransfer{}, domainError(ErrIdempotencyConflict, "aynı ters kayıt anahtarı farklı transfer verisiyle kullanıldı")
	} else if !errors.Is(existingErr, pgx.ErrNoRows) {
		return FinanceTransfer{}, existingErr
	}
	if original.ReversalOfID != nil {
		return FinanceTransfer{}, domainError(ErrAlreadyReversed, "ters kaydın ters kaydı oluşturulamaz")
	}
	var already bool
	if err = s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM finance_transfers WHERE company_id=$1 AND reversal_of_id=$2)`, session.CurrentCompanyID, id).Scan(&already); err != nil {
		return FinanceTransfer{}, err
	}
	if already {
		return FinanceTransfer{}, domainError(ErrAlreadyReversed, "transfer daha önce ters kaydedilmiş")
	}
	if date.IsZero() {
		date = s.now()
	}
	if date.Format("2006-01-02") < original.TransactionDate.Format("2006-01-02") {
		return FinanceTransfer{}, fmt.Errorf("%w: ters kayıt tarihi özgün transferden önce olamaz", identity.ErrValidation)
	}
	reversalID := id
	return s.postFinanceTransfer(ctx, session, FinanceTransferInput{FromAccountID: original.ToAccountID, ToAccountID: original.FromAccountID, Amount: original.Amount, TransactionDate: date, Description: "Ters kayıt: " + strings.TrimSpace(reason), ExternalReference: original.ExternalReference, IdempotencyKey: strings.TrimSpace(key)}, &reversalID, meta)
}

func getFinanceTransferByKey(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, companyID, key string) (FinanceTransfer, error) {
	return scanFinanceTransfer(q.QueryRow(ctx, `SELECT id,from_account_id,to_account_id,out_movement_id,in_movement_id,currency,amount::text,document_no,external_reference,description,transaction_date,reversal_of_id,posted_at FROM finance_transfers WHERE company_id=$1 AND idempotency_key=$2`, companyID, key))
}

func scanFinanceTransfer(row pgx.Row) (FinanceTransfer, error) {
	var item FinanceTransfer
	err := row.Scan(&item.ID, &item.FromAccountID, &item.ToAccountID, &item.OutMovementID, &item.InMovementID, &item.Currency, &item.Amount, &item.DocumentNo, &item.ExternalReference, &item.Description, &item.TransactionDate, &item.ReversalOfID, &item.PostedAt)
	return item, err
}

func (s *Service) GetFinanceTransfer(ctx context.Context, session identity.Session, id string) (FinanceTransfer, error) {
	return s.loadFinanceTransfer(ctx, session, id, true)
}

func (s *Service) loadFinanceTransfer(ctx context.Context, session identity.Session, id string, requireRead bool) (FinanceTransfer, error) {
	if requireRead && !can(session, "finance.transfer.read") {
		return FinanceTransfer{}, identity.ErrForbidden
	}
	item, err := scanFinanceTransfer(s.pool.QueryRow(ctx, `SELECT id,from_account_id,to_account_id,out_movement_id,in_movement_id,currency,amount::text,document_no,external_reference,description,transaction_date,reversal_of_id,posted_at FROM finance_transfers WHERE company_id=$1 AND id=$2`, session.CurrentCompanyID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return FinanceTransfer{}, identity.ErrForbidden
	}
	if err != nil {
		return FinanceTransfer{}, err
	}
	item.Status = "POSTED"
	if item.ReversalOfID == nil {
		var reversed bool
		if err = s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM finance_transfers WHERE company_id=$1 AND reversal_of_id=$2)`, session.CurrentCompanyID, item.ID).Scan(&reversed); err != nil {
			return FinanceTransfer{}, err
		}
		if reversed {
			item.Status = "REVERSED"
		}
	}
	fromAccount, err := s.loadAccount(ctx, session, item.FromAccountID, requireRead)
	if err != nil {
		return FinanceTransfer{}, err
	}
	toAccount, err := s.loadAccount(ctx, session, item.ToAccountID, requireRead)
	if err != nil {
		return FinanceTransfer{}, err
	}
	item.FromAccountName, item.FromAccountCode = fromAccount.Name, fromAccount.Code
	item.ToAccountName, item.ToAccountCode = toAccount.Name, toAccount.Code
	return item, nil
}

func (s *Service) ListFinanceTransfers(ctx context.Context, session identity.Session, accountID, search string, from, to *time.Time, limit int) ([]FinanceTransfer, error) {
	if !can(session, "finance.transfer.read") {
		return nil, identity.ErrForbidden
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	args := []any{session.CurrentCompanyID}
	query := `SELECT id,from_account_id,to_account_id,out_movement_id,in_movement_id,currency,amount::text,document_no,external_reference,description,transaction_date,reversal_of_id,posted_at FROM finance_transfers WHERE company_id=$1`
	if strings.TrimSpace(accountID) != "" {
		args = append(args, strings.TrimSpace(accountID))
		query += fmt.Sprintf(` AND (from_account_id=$%d OR to_account_id=$%d)`, len(args), len(args))
	}
	if from != nil {
		args = append(args, from.Format("2006-01-02"))
		query += fmt.Sprintf(` AND transaction_date >= $%d::date`, len(args))
	}
	if to != nil {
		args = append(args, to.Format("2006-01-02"))
		query += fmt.Sprintf(` AND transaction_date <= $%d::date`, len(args))
	}
	patterns, patternErr := searchTokens(search)
	if patternErr != nil {
		return nil, patternErr
	}
	for _, pattern := range patterns {
		args = append(args, pattern)
		param := len(args)
		query += fmt.Sprintf(` AND (
			document_no ILIKE $%d ESCAPE '\'
			OR COALESCE(external_reference,'') ILIKE $%d ESCAPE '\'
			OR description ILIKE $%d ESCAPE '\'
			OR currency ILIKE $%d ESCAPE '\'
			OR EXISTS(SELECT 1 FROM finance_accounts aq WHERE aq.company_id=finance_transfers.company_id AND aq.id IN (finance_transfers.from_account_id, finance_transfers.to_account_id) AND (aq.code ILIKE $%d ESCAPE '\' OR aq.name ILIKE $%d ESCAPE '\'))
		)`, param, param, param, param, param, param)
	}
	args = append(args, limit)
	query += fmt.Sprintf(` ORDER BY transaction_date DESC,posted_at DESC,id DESC LIMIT $%d`, len(args))
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	scanned := make([]FinanceTransfer, 0)
	for rows.Next() {
		item, scanErr := scanFinanceTransfer(rows)
		if scanErr != nil {
			rows.Close()
			return nil, scanErr
		}
		scanned = append(scanned, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	// The per-row account lookups and the reversal probe issue their own queries;
	// run them only after the transfer rows are drained, otherwise the
	// request-pinned connection fails with "conn busy".
	items := make([]FinanceTransfer, 0, len(scanned))
	for _, item := range scanned {
		fromAccount, accErr := s.GetAccount(ctx, session, item.FromAccountID)
		if accErr != nil {
			continue
		}
		toAccount, accErr := s.GetAccount(ctx, session, item.ToAccountID)
		if accErr != nil {
			continue
		}
		item.FromAccountName, item.FromAccountCode = fromAccount.Name, fromAccount.Code
		item.ToAccountName, item.ToAccountCode = toAccount.Name, toAccount.Code
		item.Status = "POSTED"
		if item.ReversalOfID == nil {
			var reversed bool
			if err = s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM finance_transfers WHERE company_id=$1 AND reversal_of_id=$2)`, session.CurrentCompanyID, item.ID).Scan(&reversed); err != nil {
				return nil, err
			}
			if reversed {
				item.Status = "REVERSED"
			}
		}
		items = append(items, item)
	}
	return items, nil
}

// rateScale is the number of fraction digits every exchange rate is carried
// with once it leaves storage. exchange_rates.rate_to_base is numeric(38,18),
// so a raw read renders eighteen digits -- more than the money parser accepts
// (scale 10) and more than any movement column keeps (numeric(20,10)). Eight
// digits is well past what TCMB and ECB publish (four to six), so trimming to
// it loses no value while keeping every stored rate usable.
const rateScale = 8

// NormalizeRateText renders a stored rate in the canonical form the rest of
// the system speaks: at most rateScale fraction digits, no trailing zeros.
// A value that is not a decimal at all is returned untouched so the caller's
// own validation still reports it.
func NormalizeRateText(value string) string {
	trimmed := strings.TrimSpace(value)
	// big.Rat reads exponent notation, which the money parser deliberately
	// refuses; normalizing "1e5" into a plain number here would widen what the
	// API accepts, so such a value is passed through to fail validation.
	if strings.ContainsAny(trimmed, "eE") {
		return trimmed
	}
	parsed, ok := new(big.Rat).SetString(trimmed)
	if !ok || parsed.Sign() <= 0 {
		return trimmed
	}
	formatted := strings.TrimRight(strings.TrimRight(parsed.FloatString(rateScale), "0"), ".")
	if formatted == "" || formatted == "0" {
		return trimmed
	}
	return formatted
}
