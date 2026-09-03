// Package advance owns the immutable employee cash-advance sub-ledger. It is
// intentionally separate from both party current accounts and payroll.
package advance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/finance"
	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/platform/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrNotFound            = errors.New("EMPLOYEE_ADVANCE_NOT_FOUND")
	ErrTransactionNotFound = errors.New("EMPLOYEE_ADVANCE_TRANSACTION_NOT_FOUND")
	ErrClosed              = errors.New("EMPLOYEE_ADVANCE_CLOSED")
	ErrExceedsBalance      = errors.New("EMPLOYEE_ADVANCE_EXCEEDS_BALANCE")
	ErrHasDependencies     = errors.New("EMPLOYEE_ADVANCE_HAS_DEPENDENCIES")
	ErrAlreadyReversed     = errors.New("EMPLOYEE_ADVANCE_ALREADY_REVERSED")
	ErrIdempotencyConflict = errors.New("IDEMPOTENCY_CONFLICT")
)

type FinancePort interface {
	PostEmployeeAdvanceMovementTx(context.Context, pgx.Tx, identity.Session, finance.EmployeeAdvanceMovementInput) (string, error)
}

type Service struct {
	pool    database.Querier
	finance FinancePort
}

func NewService(pool database.Querier, financePort FinancePort) *Service {
	return &Service{pool: pool, finance: financePort}
}

type Advance struct {
	ID                          string        `json:"id"`
	EmployeeID                  string        `json:"employee_id"`
	EmployeeCode                string        `json:"employee_code"`
	EmployeeName                string        `json:"employee_name"`
	AccountID                   string        `json:"account_id"`
	AccountName                 string        `json:"account_name"`
	Currency                    string        `json:"currency"`
	OriginalAmount              string        `json:"original_amount"`
	RepaidAmount                string        `json:"repaid_amount"`
	WrittenOffAmount            string        `json:"written_off_amount"`
	OutstandingAmount           string        `json:"outstanding_amount"`
	Status                      string        `json:"status"`
	AdvanceDate                 string        `json:"advance_date"`
	ExpectedRepaymentDate       *string       `json:"expected_repayment_date,omitempty"`
	Description                 string        `json:"description"`
	Reference                   string        `json:"reference"`
	RequiresAccountingTaxReview bool          `json:"requires_accounting_tax_review"`
	CreatedAt                   time.Time     `json:"created_at"`
	Transactions                []Transaction `json:"transactions,omitempty"`
}

type Transaction struct {
	ID                string    `json:"id"`
	AdvanceID         string    `json:"advance_id"`
	Type              string    `json:"type"`
	Amount            string    `json:"amount"`
	TransactionDate   string    `json:"transaction_date"`
	AccountID         *string   `json:"account_id,omitempty"`
	FinanceMovementID *string   `json:"finance_movement_id,omitempty"`
	ReversalOfID      *string   `json:"reversal_of_id,omitempty"`
	Reason            string    `json:"reason,omitempty"`
	Description       string    `json:"description,omitempty"`
	ActorUserID       string    `json:"actor_user_id"`
	CreatedAt         time.Time `json:"created_at"`
}

type CreateInput struct {
	EmployeeID            string  `json:"employee_id"`
	Amount                string  `json:"amount"`
	AccountID             string  `json:"account_id"`
	Description           string  `json:"description"`
	Reference             string  `json:"reference,omitempty"`
	ExpectedRepaymentDate *string `json:"expected_repayment_date,omitempty"`
	IdempotencyKey        string  `json:"idempotency_key"`
	OverrideReason        string  `json:"override_reason,omitempty"`
}

type RepaymentInput struct {
	Amount          string `json:"amount"`
	TransactionDate string `json:"transaction_date"`
	AccountID       string `json:"account_id"`
	Description     string `json:"description,omitempty"`
	Reference       string `json:"reference,omitempty"`
	IdempotencyKey  string `json:"idempotency_key"`
}
type WriteOffInput struct {
	TransactionDate string `json:"transaction_date"`
	Reason          string `json:"reason"`
	IdempotencyKey  string `json:"idempotency_key"`
}
type ReverseInput struct {
	TransactionDate string `json:"transaction_date"`
	Reason          string `json:"reason"`
	IdempotencyKey  string `json:"idempotency_key"`
	OverrideReason  string `json:"override_reason,omitempty"`
}
type ListFilter struct {
	EmployeeID, Status, Query, Balance string
	From, To                           *time.Time
	Limit                              int
}
type Page struct {
	Items            []Advance `json:"items"`
	TotalOutstanding string    `json:"total_outstanding"`
}

var tryAmount = regexp.MustCompile(`^(0|[1-9][0-9]*)\.[0-9]{2}$`)

func normalizeAmount(value string) (string, error) {
	value = strings.TrimSpace(value)
	if !tryAmount.MatchString(value) || value == "0.00" {
		return "", fmt.Errorf("%w: tutar iki ondalıklı pozitif TRY string'i olmalıdır", identity.ErrValidation)
	}
	return value, nil
}
func parseDate(value string) (time.Time, error) {
	t, err := time.Parse("2006-01-02", strings.TrimSpace(value))
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: tarih YYYY-MM-DD olmalıdır", identity.ErrValidation)
	}
	return t, nil
}
func payloadHash(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func requireUUID(value string) error {
	if uuid.Validate(strings.TrimSpace(value)) != nil {
		return fmt.Errorf("%w: kimlik geçersiz", identity.ErrValidation)
	}
	return nil
}

const advanceSelect = `SELECT a.id::text AS id,a.employee_id::text AS employee_id,e.employee_code,e.first_name||' '||e.last_name AS employee_name,a.account_id::text AS account_id,fa.name AS account_name,a.currency,
 a.original_amount::text AS original_amount,COALESCE(x.repaid,0)::numeric(20,2)::text AS repaid_amount,COALESCE(x.written_off,0)::numeric(20,2)::text AS written_off_amount,
 CASE WHEN x.disbursement_reversed THEN 0 ELSE a.original_amount-COALESCE(x.repaid,0)-COALESCE(x.written_off,0) END::numeric(20,2)::text AS outstanding_amount,
 CASE WHEN x.disbursement_reversed THEN 'REVERSED' WHEN a.original_amount-COALESCE(x.repaid,0)-COALESCE(x.written_off,0)>0 THEN 'OPEN' WHEN COALESCE(x.written_off,0)>0 THEN 'WRITTEN_OFF' ELSE 'CLOSED' END AS status,
 to_char(a.advance_date,'YYYY-MM-DD') AS advance_date,CASE WHEN a.expected_repayment_date IS NULL THEN NULL ELSE to_char(a.expected_repayment_date,'YYYY-MM-DD') END AS expected_repayment_date,
 a.description,a.reference,(COALESCE(x.written_off,0)>0) AS requires_accounting_tax_review,a.created_at
 FROM employee_advances a JOIN employees e ON e.company_id=a.company_id AND e.id=a.employee_id
 JOIN finance_accounts fa ON fa.company_id=a.company_id AND fa.id=a.account_id
 LEFT JOIN LATERAL (SELECT
  COALESCE(SUM(t.amount) FILTER(WHERE t.transaction_type IN ('REPAYMENT','PAYROLL_DEDUCTION') AND NOT EXISTS(SELECT 1 FROM employee_advance_transactions r WHERE r.company_id=t.company_id AND r.reversal_of_id=t.id)),0) repaid,
  COALESCE(SUM(t.amount) FILTER(WHERE t.transaction_type='WRITE_OFF' AND NOT EXISTS(SELECT 1 FROM employee_advance_transactions r WHERE r.company_id=t.company_id AND r.reversal_of_id=t.id)),0) written_off,
  EXISTS(SELECT 1 FROM employee_advance_transactions d JOIN employee_advance_transactions r ON r.company_id=d.company_id AND r.reversal_of_id=d.id WHERE d.company_id=a.company_id AND d.advance_id=a.id AND d.transaction_type='DISBURSEMENT') disbursement_reversed
  FROM employee_advance_transactions t WHERE t.company_id=a.company_id AND t.advance_id=a.id) x ON true`

type querier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}
type scanner interface{ Scan(...any) error }

func scanAdvance(row scanner) (Advance, error) {
	var a Advance
	err := row.Scan(&a.ID, &a.EmployeeID, &a.EmployeeCode, &a.EmployeeName, &a.AccountID, &a.AccountName, &a.Currency, &a.OriginalAmount, &a.RepaidAmount, &a.WrittenOffAmount, &a.OutstandingAmount, &a.Status, &a.AdvanceDate, &a.ExpectedRepaymentDate, &a.Description, &a.Reference, &a.RequiresAccountingTaxReview, &a.CreatedAt)
	return a, err
}
func load(ctx context.Context, q querier, session identity.Session, id string, withTransactions bool) (Advance, error) {
	a, err := scanAdvance(q.QueryRow(ctx, advanceSelect+` WHERE a.company_id=$1 AND a.id=$2 AND (NOT EXISTS(SELECT 1 FROM membership_finance_account_scopes s WHERE s.company_id=a.company_id AND s.user_id=$3) OR EXISTS(SELECT 1 FROM membership_finance_account_scopes s WHERE s.company_id=a.company_id AND s.user_id=$3 AND s.account_id=a.account_id))`, session.CurrentCompanyID, id, session.User.ID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Advance{}, ErrNotFound
	}
	if err != nil {
		return Advance{}, err
	}
	if withTransactions {
		rows, e := q.Query(ctx, `SELECT id::text,advance_id::text,transaction_type,amount::text,to_char(transaction_date,'YYYY-MM-DD'),account_id::text,finance_movement_id::text,reversal_of_id::text,reason,description,actor_user_id::text,created_at FROM employee_advance_transactions WHERE company_id=$1 AND advance_id=$2 ORDER BY transaction_date,created_at,id`, session.CurrentCompanyID, id)
		if e != nil {
			return Advance{}, e
		}
		defer rows.Close()
		a.Transactions = []Transaction{}
		for rows.Next() {
			var t Transaction
			if e = rows.Scan(&t.ID, &t.AdvanceID, &t.Type, &t.Amount, &t.TransactionDate, &t.AccountID, &t.FinanceMovementID, &t.ReversalOfID, &t.Reason, &t.Description, &t.ActorUserID, &t.CreatedAt); e != nil {
				return Advance{}, e
			}
			a.Transactions = append(a.Transactions, t)
		}
		if e = rows.Err(); e != nil {
			return Advance{}, e
		}
	}
	return a, nil
}

func (s *Service) Get(ctx context.Context, session identity.Session, id string) (Advance, error) {
	if !session.HasPermission("hr.employee_advance.read") {
		return Advance{}, identity.ErrForbidden
	}
	if requireUUID(id) != nil {
		return Advance{}, ErrNotFound
	}
	return load(ctx, s.pool, session, id, true)
}

func (s *Service) List(ctx context.Context, session identity.Session, f ListFilter) (Page, error) {
	if !session.HasPermission("hr.employee_advance.read") {
		return Page{}, identity.ErrForbidden
	}
	if f.EmployeeID != "" && requireUUID(f.EmployeeID) != nil {
		return Page{}, identity.ErrValidation
	}
	f.Status = strings.ToUpper(strings.TrimSpace(f.Status))
	if f.Status != "" && !contains([]string{"OPEN", "CLOSED", "WRITTEN_OFF", "REVERSED"}, f.Status) {
		return Page{}, identity.ErrValidation
	}
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 100
	}
	f.Balance = strings.ToUpper(strings.TrimSpace(f.Balance))
	if f.Balance != "" && !contains([]string{"OPEN", "CLOSED"}, f.Balance) {
		return Page{}, identity.ErrValidation
	}
	query := `SELECT * FROM (` + advanceSelect + ` WHERE a.company_id=$1 AND (NOT EXISTS(SELECT 1 FROM membership_finance_account_scopes s WHERE s.company_id=a.company_id AND s.user_id=$2) OR EXISTS(SELECT 1 FROM membership_finance_account_scopes s WHERE s.company_id=a.company_id AND s.user_id=$2 AND s.account_id=a.account_id))) q WHERE ($3='' OR q.employee_id::text=$3) AND ($4='' OR q.status=$4) AND ($5='' OR lower(q.employee_name||' '||q.employee_code) LIKE '%'||lower($5)||'%') AND ($6='' OR ($6='OPEN' AND q.outstanding_amount::numeric>0) OR ($6='CLOSED' AND q.outstanding_amount::numeric=0)) AND ($7::date IS NULL OR q.advance_date::date >= $7) AND ($8::date IS NULL OR q.advance_date::date <= $8) ORDER BY q.advance_date DESC,q.id LIMIT $9`
	var from, to any
	if f.From != nil {
		from = f.From.Format("2006-01-02")
	}
	if f.To != nil {
		to = f.To.Format("2006-01-02")
	}
	rows, err := s.pool.Query(ctx, query, session.CurrentCompanyID, session.User.ID, f.EmployeeID, f.Status, strings.TrimSpace(f.Query), f.Balance, from, to, f.Limit)
	if err != nil {
		return Page{}, err
	}
	defer rows.Close()
	p := Page{Items: []Advance{}, TotalOutstanding: "0.00"}
	for rows.Next() {
		a, e := scanAdvance(rows)
		if e != nil {
			return Page{}, e
		}
		p.Items = append(p.Items, a)
	}
	if err = rows.Err(); err != nil {
		return Page{}, err
	}
	if err = s.pool.QueryRow(ctx, `SELECT COALESCE(SUM(outstanding_amount::numeric),0)::numeric(20,2)::text FROM (`+advanceSelect+` WHERE a.company_id=$1 AND (NOT EXISTS(SELECT 1 FROM membership_finance_account_scopes s WHERE s.company_id=a.company_id AND s.user_id=$2) OR EXISTS(SELECT 1 FROM membership_finance_account_scopes s WHERE s.company_id=a.company_id AND s.user_id=$2 AND s.account_id=a.account_id))) z`, session.CurrentCompanyID, session.User.ID).Scan(&p.TotalOutstanding); err != nil {
		return Page{}, err
	}
	return p, nil
}
func contains(items []string, v string) bool {
	for _, x := range items {
		if x == v {
			return true
		}
	}
	return false
}

func replay(ctx context.Context, tx pgx.Tx, companyID, key, hash string) (string, bool, error) {
	var advanceID, stored string
	err := tx.QueryRow(ctx, `SELECT advance_id::text,payload_hash FROM employee_advance_transactions WHERE company_id=$1 AND idempotency_key=$2 FOR UPDATE`, companyID, key).Scan(&advanceID, &stored)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if stored != hash {
		return "", false, ErrIdempotencyConflict
	}
	return advanceID, true, nil
}

func (s *Service) Create(ctx context.Context, session identity.Session, in CreateInput) (Advance, error) {
	if !session.HasPermission("hr.employee_advance.post") {
		return Advance{}, identity.ErrForbidden
	}
	in.EmployeeID = strings.TrimSpace(in.EmployeeID)
	in.AccountID = strings.TrimSpace(in.AccountID)
	in.Description = strings.TrimSpace(in.Description)
	in.Reference = strings.TrimSpace(in.Reference)
	in.IdempotencyKey = strings.TrimSpace(in.IdempotencyKey)
	if requireUUID(in.EmployeeID) != nil || requireUUID(in.AccountID) != nil || in.Description == "" || in.IdempotencyKey == "" {
		return Advance{}, fmt.Errorf("%w: zorunlu avans alanları eksik", identity.ErrValidation)
	}
	amount, err := normalizeAmount(in.Amount)
	if err != nil {
		return Advance{}, err
	}
	hash := payloadHash(in)
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Advance{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if id, ok, e := replay(ctx, tx, session.CurrentCompanyID, in.IdempotencyKey, hash); e != nil {
		return Advance{}, e
	} else if ok {
		return load(ctx, tx, session, id, true)
	}
	var dateText string
	if err = tx.QueryRow(ctx, `SELECT to_char(now() AT TIME ZONE timezone,'YYYY-MM-DD') FROM companies WHERE id=$1`, session.CurrentCompanyID).Scan(&dateText); err != nil {
		return Advance{}, err
	}
	date, err := parseDate(dateText)
	if err != nil {
		return Advance{}, err
	}
	if in.ExpectedRepaymentDate != nil {
		d, e := parseDate(*in.ExpectedRepaymentDate)
		if e != nil || d.Before(date) {
			return Advance{}, fmt.Errorf("%w: beklenen geri ödeme tarihi geçersiz", identity.ErrValidation)
		}
	}
	var active bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM employments WHERE company_id=$1 AND employee_id=$2 AND start_date<=$3 AND (end_date IS NULL OR end_date>=$3))`, session.CurrentCompanyID, in.EmployeeID, date).Scan(&active); err != nil {
		return Advance{}, err
	}
	if !active {
		return Advance{}, fmt.Errorf("%w: avans tarihinde aktif çalışma ilişkisi bulunmalıdır", identity.ErrValidation)
	}
	advanceID, transactionID := uuid.NewString(), uuid.NewString()
	var expected any
	if in.ExpectedRepaymentDate != nil {
		expected = *in.ExpectedRepaymentDate
	}
	_, err = tx.Exec(ctx, `INSERT INTO employee_advances(id,company_id,employee_id,account_id,original_amount,advance_date,expected_repayment_date,description,reference,created_by) VALUES($1,$2,$3,$4,$5,$6,$7::date,$8,$9,$10)`, advanceID, session.CurrentCompanyID, in.EmployeeID, in.AccountID, amount, date, expected, in.Description, in.Reference, session.User.ID)
	if err != nil {
		return Advance{}, err
	}
	movementID, err := s.finance.PostEmployeeAdvanceMovementTx(ctx, tx, session, finance.EmployeeAdvanceMovementInput{AccountID: in.AccountID, Direction: "OUT", Amount: amount, TransactionDate: date, SourceID: transactionID, Description: in.Description, ExternalReference: in.Reference, IdempotencyKey: "employee-advance:" + in.IdempotencyKey, OverrideReason: in.OverrideReason})
	if err != nil {
		return Advance{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO employee_advance_transactions(id,company_id,advance_id,transaction_type,amount,transaction_date,account_id,finance_movement_id,description,idempotency_key,payload_hash,actor_user_id) VALUES($1,$2,$3,'DISBURSEMENT',$4,$5,$6,$7,$8,$9,$10,$11)`, transactionID, session.CurrentCompanyID, advanceID, amount, date, in.AccountID, movementID, in.Description, in.IdempotencyKey, hash, session.User.ID)
	if err != nil {
		return Advance{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Advance{}, err
	}
	return load(ctx, s.pool, session, advanceID, true)
}

func outstandingTx(ctx context.Context, tx pgx.Tx, companyID, advanceID string) (string, error) {
	var value string
	err := tx.QueryRow(ctx, `SELECT CASE WHEN EXISTS(
	 SELECT 1 FROM employee_advance_transactions d JOIN employee_advance_transactions r ON r.company_id=d.company_id AND r.reversal_of_id=d.id
	 WHERE d.company_id=a.company_id AND d.advance_id=a.id AND d.transaction_type='DISBURSEMENT'
	) THEN 0 ELSE a.original_amount-COALESCE(SUM(t.amount) FILTER(WHERE t.transaction_type IN ('REPAYMENT','PAYROLL_DEDUCTION','WRITE_OFF') AND NOT EXISTS(SELECT 1 FROM employee_advance_transactions r WHERE r.company_id=t.company_id AND r.reversal_of_id=t.id)),0) END::numeric(20,2)::text
	FROM employee_advances a LEFT JOIN employee_advance_transactions t ON t.company_id=a.company_id AND t.advance_id=a.id
	WHERE a.company_id=$1 AND a.id=$2 GROUP BY a.company_id,a.id,a.original_amount`, companyID, advanceID).Scan(&value)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return value, err
}

func (s *Service) Repay(ctx context.Context, session identity.Session, advanceID string, in RepaymentInput) (Advance, error) {
	if !session.HasPermission("hr.employee_advance.collect") {
		return Advance{}, identity.ErrForbidden
	}
	if requireUUID(advanceID) != nil || requireUUID(in.AccountID) != nil {
		return Advance{}, identity.ErrValidation
	}
	amount, err := normalizeAmount(in.Amount)
	if err != nil {
		return Advance{}, err
	}
	date, err := parseDate(in.TransactionDate)
	if err != nil {
		return Advance{}, err
	}
	in.IdempotencyKey = strings.TrimSpace(in.IdempotencyKey)
	if in.IdempotencyKey == "" {
		return Advance{}, identity.ErrValidation
	}
	hash := payloadHash(struct {
		Advance string
		Input   RepaymentInput
	}{advanceID, in})
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Advance{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if id, ok, e := replay(ctx, tx, session.CurrentCompanyID, in.IdempotencyKey, hash); e != nil {
		return Advance{}, e
	} else if ok {
		return load(ctx, tx, session, id, true)
	}
	if err = tx.QueryRow(ctx, `SELECT id FROM employee_advances WHERE company_id=$1 AND id=$2 FOR UPDATE`, session.CurrentCompanyID, advanceID).Scan(new(string)); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Advance{}, ErrNotFound
		}
		return Advance{}, err
	}
	out, err := outstandingTx(ctx, tx, session.CurrentCompanyID, advanceID)
	if err != nil {
		return Advance{}, err
	}
	if out == "0.00" {
		return Advance{}, ErrClosed
	}
	var exceeds bool
	if err = tx.QueryRow(ctx, `SELECT $1::numeric>$2::numeric`, amount, out).Scan(&exceeds); err != nil {
		return Advance{}, err
	}
	if exceeds {
		return Advance{}, ErrExceedsBalance
	}
	transactionID := uuid.NewString()
	desc := strings.TrimSpace(in.Description)
	if desc == "" {
		desc = "Personel avansı geri ödemesi"
	}
	movementID, err := s.finance.PostEmployeeAdvanceMovementTx(ctx, tx, session, finance.EmployeeAdvanceMovementInput{AccountID: in.AccountID, Direction: "IN", Amount: amount, TransactionDate: date, SourceID: transactionID, Description: desc, ExternalReference: strings.TrimSpace(in.Reference), IdempotencyKey: "employee-advance:" + in.IdempotencyKey})
	if err != nil {
		return Advance{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO employee_advance_transactions(id,company_id,advance_id,transaction_type,amount,transaction_date,account_id,finance_movement_id,description,idempotency_key,payload_hash,actor_user_id) VALUES($1,$2,$3,'REPAYMENT',$4,$5,$6,$7,$8,$9,$10,$11)`, transactionID, session.CurrentCompanyID, advanceID, amount, date, in.AccountID, movementID, desc, in.IdempotencyKey, hash, session.User.ID)
	if err != nil {
		return Advance{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Advance{}, err
	}
	return load(ctx, s.pool, session, advanceID, true)
}

func (s *Service) WriteOff(ctx context.Context, session identity.Session, advanceID string, in WriteOffInput) (Advance, error) {
	if !session.HasPermission("hr.employee_advance.writeoff") {
		return Advance{}, identity.ErrForbidden
	}
	if requireUUID(advanceID) != nil {
		return Advance{}, identity.ErrValidation
	}
	in.Reason = strings.TrimSpace(in.Reason)
	in.IdempotencyKey = strings.TrimSpace(in.IdempotencyKey)
	if in.Reason == "" || in.IdempotencyKey == "" {
		return Advance{}, fmt.Errorf("%w: vazgeçme gerekçesi ve idempotency_key zorunludur", identity.ErrValidation)
	}
	date, err := parseDate(in.TransactionDate)
	if err != nil {
		return Advance{}, err
	}
	hash := payloadHash(struct {
		Advance string
		Input   WriteOffInput
	}{advanceID, in})
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Advance{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if id, ok, e := replay(ctx, tx, session.CurrentCompanyID, in.IdempotencyKey, hash); e != nil {
		return Advance{}, e
	} else if ok {
		return load(ctx, tx, session, id, true)
	}
	if err = tx.QueryRow(ctx, `SELECT id FROM employee_advances WHERE company_id=$1 AND id=$2 FOR UPDATE`, session.CurrentCompanyID, advanceID).Scan(new(string)); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Advance{}, ErrNotFound
		}
		return Advance{}, err
	}
	out, err := outstandingTx(ctx, tx, session.CurrentCompanyID, advanceID)
	if err != nil {
		return Advance{}, err
	}
	if out == "0.00" {
		return Advance{}, ErrClosed
	}
	_, err = tx.Exec(ctx, `INSERT INTO employee_advance_transactions(id,company_id,advance_id,transaction_type,amount,transaction_date,reason,description,idempotency_key,payload_hash,actor_user_id) VALUES($1,$2,$3,'WRITE_OFF',$4,$5,$6,'Muhasebe/vergi değerlendirmesi gerekli',$7,$8,$9)`, uuid.NewString(), session.CurrentCompanyID, advanceID, out, date, in.Reason, in.IdempotencyKey, hash, session.User.ID)
	if err != nil {
		return Advance{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Advance{}, err
	}
	return load(ctx, s.pool, session, advanceID, true)
}

func (s *Service) Reverse(ctx context.Context, session identity.Session, transactionID string, in ReverseInput) (Advance, error) {
	if !session.HasPermission("hr.employee_advance.reverse") {
		return Advance{}, identity.ErrForbidden
	}
	if requireUUID(transactionID) != nil {
		return Advance{}, identity.ErrValidation
	}
	in.Reason = strings.TrimSpace(in.Reason)
	in.IdempotencyKey = strings.TrimSpace(in.IdempotencyKey)
	if in.Reason == "" || in.IdempotencyKey == "" {
		return Advance{}, fmt.Errorf("%w: ters kayıt gerekçesi ve idempotency_key zorunludur", identity.ErrValidation)
	}
	date, err := parseDate(in.TransactionDate)
	if err != nil {
		return Advance{}, err
	}
	hash := payloadHash(struct {
		Transaction string
		Input       ReverseInput
	}{transactionID, in})
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Advance{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if id, ok, e := replay(ctx, tx, session.CurrentCompanyID, in.IdempotencyKey, hash); e != nil {
		return Advance{}, e
	} else if ok {
		return load(ctx, tx, session, id, true)
	}
	var advanceID, typ, amount string
	var accountID, movementID *string
	err = tx.QueryRow(ctx, `SELECT advance_id::text,transaction_type,amount::text,account_id::text,finance_movement_id::text FROM employee_advance_transactions WHERE company_id=$1 AND id=$2 FOR UPDATE`, session.CurrentCompanyID, transactionID).Scan(&advanceID, &typ, &amount, &accountID, &movementID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Advance{}, ErrTransactionNotFound
	}
	if err != nil {
		return Advance{}, err
	}
	if typ == "REVERSAL" {
		return Advance{}, fmt.Errorf("%w: ters kayıt yeniden ters çevrilemez", identity.ErrValidation)
	}
	// A payroll-settled deduction belongs to a finalized payroll run; unwinding it
	// here would silently disagree with the payslip that produced it.
	if typ == "PAYROLL_DEDUCTION" {
		return Advance{}, fmt.Errorf("%w: bordrodan mahsup edilen avans kaydı buradan ters çevrilemez", identity.ErrValidation)
	}
	var reversed bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM employee_advance_transactions WHERE company_id=$1 AND reversal_of_id=$2)`, session.CurrentCompanyID, transactionID).Scan(&reversed); err != nil {
		return Advance{}, err
	}
	if reversed {
		return Advance{}, ErrAlreadyReversed
	}
	if typ == "DISBURSEMENT" {
		var deps bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM employee_advance_transactions t WHERE t.company_id=$1 AND t.advance_id=$2 AND t.transaction_type IN ('REPAYMENT','PAYROLL_DEDUCTION','WRITE_OFF') AND NOT EXISTS(SELECT 1 FROM employee_advance_transactions r WHERE r.company_id=t.company_id AND r.reversal_of_id=t.id))`, session.CurrentCompanyID, advanceID).Scan(&deps); err != nil {
			return Advance{}, err
		}
		if deps {
			return Advance{}, ErrHasDependencies
		}
	}
	reversalID := uuid.NewString()
	var reverseMovement *string
	if typ != "WRITE_OFF" {
		direction := "IN"
		if typ == "REPAYMENT" {
			direction = "OUT"
		}
		mid, e := s.finance.PostEmployeeAdvanceMovementTx(ctx, tx, session, finance.EmployeeAdvanceMovementInput{AccountID: *accountID, Direction: direction, Amount: amount, TransactionDate: date, SourceID: reversalID, Description: "Personel avansı ters kayıt: " + in.Reason, IdempotencyKey: "employee-advance:" + in.IdempotencyKey, OverrideReason: in.OverrideReason, ReversalOfID: movementID})
		if e != nil {
			return Advance{}, e
		}
		reverseMovement = &mid
	}
	_, err = tx.Exec(ctx, `INSERT INTO employee_advance_transactions(id,company_id,advance_id,transaction_type,amount,transaction_date,account_id,finance_movement_id,reversal_of_id,reason,description,idempotency_key,payload_hash,actor_user_id) VALUES($1,$2,$3,'REVERSAL',$4,$5,$6,$7,$8,$9,'Personel avansı ters kayıt',$10,$11,$12)`, reversalID, session.CurrentCompanyID, advanceID, amount, date, accountID, reverseMovement, transactionID, in.Reason, in.IdempotencyKey, hash, session.User.ID)
	if err != nil {
		return Advance{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Advance{}, err
	}
	return load(ctx, s.pool, session, advanceID, true)
}
