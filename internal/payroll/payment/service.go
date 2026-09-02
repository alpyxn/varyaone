// Package payment turns a finalized payroll run into a cash/bank disbursement:
// one TRY outflow movement for the run's total net, from a chosen account.
// The record is immutable once posted and can only be reversed.
package payment

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/finance"
	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/platform/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrRunNotFound     = errors.New("PAYROLL_RUN_NOT_FOUND")
	ErrRunNotFinalized = errors.New("PAYROLL_RUN_NOT_FINALIZED")
	ErrNothingToPay    = errors.New("PAYROLL_NOTHING_TO_PAY")
	ErrAlreadyPaid     = errors.New("PAYROLL_ALREADY_PAID")
	ErrPaymentNotFound = errors.New("PAYROLL_PAYMENT_NOT_FOUND")
	ErrNotReversible   = errors.New("PAYROLL_PAYMENT_NOT_REVERSIBLE")
)

// FinancePort is the narrow slice of the finance service this package needs.
type FinancePort interface {
	PostPayrollPaymentMovementTx(context.Context, pgx.Tx, identity.Session, finance.PayrollPaymentMovementInput) (string, error)
}

type Service struct {
	pool    database.Querier
	finance FinancePort
}

func NewService(pool database.Querier, financePort FinancePort) *Service {
	return &Service{pool: pool, finance: financePort}
}

type Payment struct {
	ID                string     `json:"id"`
	PayrollRunID      string     `json:"payroll_run_id"`
	AccountID         string     `json:"account_id"`
	AccountName       string     `json:"account_name"`
	AccountType       string     `json:"account_type"`
	Amount            string     `json:"amount"`
	Currency          string     `json:"currency"`
	PaymentDate       string     `json:"payment_date"`
	Status            string     `json:"status"`
	Description       string     `json:"description"`
	FinanceMovementID string     `json:"finance_movement_id"`
	ReversedAt        *time.Time `json:"reversed_at,omitempty"`
	ReversalReason    *string    `json:"reversal_reason,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
}

type CreateInput struct {
	AccountID      string `json:"account_id"`
	PaymentDate    string `json:"payment_date"`
	Description    string `json:"description"`
	OverrideReason string `json:"override_reason"`
}

const paymentSelect = `SELECT p.id::text,p.payroll_run_id::text,p.account_id::text,fa.name,fa.account_type,
 p.amount::text,p.currency,to_char(p.payment_date,'YYYY-MM-DD'),p.status,p.description,p.finance_movement_id::text,
 p.reversed_at,p.reversal_reason,p.created_at
 FROM payroll_payments p JOIN finance_accounts fa ON fa.company_id=p.company_id AND fa.id=p.account_id `

func scanPayment(row interface{ Scan(...any) error }) (Payment, error) {
	var p Payment
	err := row.Scan(&p.ID, &p.PayrollRunID, &p.AccountID, &p.AccountName, &p.AccountType, &p.Amount, &p.Currency,
		&p.PaymentDate, &p.Status, &p.Description, &p.FinanceMovementID, &p.ReversedAt, &p.ReversalReason, &p.CreatedAt)
	return p, err
}

// ForRun returns the payments recorded for a run, newest first (usually zero or
// one PAID row, plus any reversed history).
func (s *Service) ForRun(ctx context.Context, session identity.Session, runID string) ([]Payment, error) {
	if !session.HasPermission("hr.payroll.read") {
		return nil, identity.ErrForbidden
	}
	rows, err := s.pool.Query(ctx, paymentSelect+` WHERE p.company_id=$1 AND p.payroll_run_id=NULLIF($2,'')::uuid ORDER BY p.created_at DESC,p.id`,
		session.CurrentCompanyID, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Payment{}
	for rows.Next() {
		p, err := scanPayment(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, p)
	}
	return items, rows.Err()
}

func (s *Service) Create(ctx context.Context, session identity.Session, runID string, in CreateInput) (Payment, error) {
	if !session.HasPermission("hr.payroll.pay") {
		return Payment{}, identity.ErrForbidden
	}
	in.AccountID = strings.TrimSpace(in.AccountID)
	in.Description = strings.TrimSpace(in.Description)
	in.PaymentDate = strings.TrimSpace(in.PaymentDate)
	in.OverrideReason = strings.TrimSpace(in.OverrideReason)
	if uuid.Validate(in.AccountID) != nil {
		return Payment{}, fmt.Errorf("%w: kasa/banka hesabı seçilmelidir", identity.ErrValidation)
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Payment{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var status, totalNet, runPaymentDate string
	err = tx.QueryRow(ctx, `SELECT status,COALESCE(total_net::text,''),to_char(payment_date,'YYYY-MM-DD')
 FROM payroll_runs WHERE company_id=$1 AND id=NULLIF($2,'')::uuid FOR UPDATE`,
		session.CurrentCompanyID, runID).Scan(&status, &totalNet, &runPaymentDate)
	if errors.Is(err, pgx.ErrNoRows) {
		return Payment{}, ErrRunNotFound
	}
	if err != nil {
		return Payment{}, err
	}
	if status != "FINALIZED" {
		return Payment{}, ErrRunNotFinalized
	}
	if totalNet == "" || totalNet == "0" || totalNet == "0.00" {
		return Payment{}, ErrNothingToPay
	}

	var existing int
	if err = tx.QueryRow(ctx, `SELECT COUNT(*) FROM payroll_payments WHERE company_id=$1 AND payroll_run_id=$2 AND status='PAID'`,
		session.CurrentCompanyID, runID).Scan(&existing); err != nil {
		return Payment{}, err
	}
	if existing > 0 {
		return Payment{}, ErrAlreadyPaid
	}

	paymentDate := in.PaymentDate
	if paymentDate == "" {
		paymentDate = runPaymentDate
	}
	date, derr := time.Parse("2006-01-02", paymentDate)
	if derr != nil {
		return Payment{}, fmt.Errorf("%w: ödeme tarihi geçersiz", identity.ErrValidation)
	}
	description := in.Description
	if description == "" {
		description = "Bordro ödemesi"
	}

	paymentID := uuid.NewString()
	// Keyed by the (fresh) payment id, not the run, so a run whose earlier
	// payment was reversed can be paid again. The status='PAID' guard above is
	// what prevents a genuine double payment.
	idempotencyKey := "payroll-payment:" + paymentID
	movementID, err := s.finance.PostPayrollPaymentMovementTx(ctx, tx, session, finance.PayrollPaymentMovementInput{
		AccountID:       in.AccountID,
		Direction:       "OUT",
		Amount:          totalNet,
		TransactionDate: date,
		SourceID:        paymentID,
		Description:     description,
		IdempotencyKey:  idempotencyKey,
		OverrideReason:  in.OverrideReason,
	})
	if err != nil {
		return Payment{}, mapConstraint(err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO payroll_payments(id,company_id,payroll_run_id,account_id,amount,payment_date,description,finance_movement_id,idempotency_key,created_by)
 VALUES($1,$2,NULLIF($3,'')::uuid,$4,$5::numeric,$6::date,$7,$8,$9,$10)`,
		paymentID, session.CurrentCompanyID, runID, in.AccountID, totalNet, paymentDate, description, movementID, idempotencyKey, session.User.ID)
	if err != nil {
		return Payment{}, mapConstraint(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return Payment{}, err
	}
	return s.get(ctx, session, paymentID)
}

func (s *Service) Reverse(ctx context.Context, session identity.Session, paymentID, reason string) (Payment, error) {
	if !session.HasPermission("hr.payroll.pay") {
		return Payment{}, identity.ErrForbidden
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return Payment{}, fmt.Errorf("%w: geri alma gerekçesi zorunlu", identity.ErrValidation)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Payment{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var status, accountID, amount, movementID, paymentDate string
	err = tx.QueryRow(ctx, `SELECT status,account_id::text,amount::text,finance_movement_id::text,to_char(payment_date,'YYYY-MM-DD')
 FROM payroll_payments WHERE company_id=$1 AND id=NULLIF($2,'')::uuid FOR UPDATE`,
		session.CurrentCompanyID, paymentID).Scan(&status, &accountID, &amount, &movementID, &paymentDate)
	if errors.Is(err, pgx.ErrNoRows) {
		return Payment{}, ErrPaymentNotFound
	}
	if err != nil {
		return Payment{}, err
	}
	if status != "PAID" {
		return Payment{}, ErrNotReversible
	}
	date, _ := time.Parse("2006-01-02", paymentDate)
	reversalID := uuid.NewString()
	reverseMovementID, err := s.finance.PostPayrollPaymentMovementTx(ctx, tx, session, finance.PayrollPaymentMovementInput{
		AccountID:       accountID,
		Direction:       "IN",
		Amount:          amount,
		TransactionDate: date,
		SourceID:        reversalID,
		Description:     "Bordro ödemesi geri alındı: " + reason,
		IdempotencyKey:  "payroll-payment-reverse:" + paymentID,
		ReversalOfID:    &movementID,
	})
	if err != nil {
		return Payment{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE payroll_payments SET status='REVERSED',reversal_movement_id=$3,reversed_at=now(),reversed_by=$4,reversal_reason=$5
 WHERE company_id=$1 AND id=$2`,
		session.CurrentCompanyID, paymentID, reverseMovementID, session.User.ID, reason); err != nil {
		return Payment{}, mapConstraint(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return Payment{}, err
	}
	return s.get(ctx, session, paymentID)
}

func (s *Service) get(ctx context.Context, session identity.Session, id string) (Payment, error) {
	p, err := scanPayment(s.pool.QueryRow(ctx, paymentSelect+` WHERE p.company_id=$1 AND p.id=$2`, session.CurrentCompanyID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Payment{}, ErrPaymentNotFound
	}
	return p, err
}

func mapConstraint(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	switch {
	case pgErr.Code == "23505" && strings.Contains(pgErr.ConstraintName, "one_active_per_run"):
		return ErrAlreadyPaid
	case pgErr.Code == "23505" && strings.Contains(pgErr.ConstraintName, "idempotency_key"):
		return ErrAlreadyPaid
	case pgErr.Code == "55000":
		return ErrNotReversible
	}
	return err
}
