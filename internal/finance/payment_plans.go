package finance

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type PlanInstallmentInput struct {
	DueDate string `json:"due_date"`
	Amount  string `json:"amount"`
}
type PaymentPlanInput struct {
	PartyID        string                 `json:"party_id"`
	Side           string                 `json:"side"`
	Currency       string                 `json:"currency"`
	Description    string                 `json:"description"`
	OpenItemIDs    []string               `json:"open_item_ids"`
	Installments   []PlanInstallmentInput `json:"installments"`
	IdempotencyKey string                 `json:"-"`
}
type PlanInstallment struct {
	Number       int               `json:"number"`
	DueDate      string            `json:"due_date"`
	Amount       string            `json:"amount"`
	OpenAmount   string            `json:"open_amount"`
	ClosedAmount string            `json:"closed_amount"`
	Status       string            `json:"status"`
	Allocations  []AllocationInput `json:"allocations"`
}
type PaymentPlan struct {
	ID           string            `json:"id"`
	PartyID      string            `json:"party_id"`
	PartyName    string            `json:"party_name"`
	Side         string            `json:"side"`
	Currency     string            `json:"currency"`
	Description  string            `json:"description"`
	TotalAmount  string            `json:"total_amount"`
	CreatedAt    time.Time         `json:"created_at"`
	CancelledAt  *time.Time        `json:"cancelled_at,omitempty"`
	CancelReason *string           `json:"cancel_reason,omitempty"`
	Version      int64             `json:"version"`
	Installments []PlanInstallment `json:"installments"`
	Sources      []PlanSource      `json:"sources"`
}
type PlanSource struct {
	OpenItemID string `json:"open_item_id"`
	DocumentID string `json:"document_id"`
	DocumentNo string `json:"document_no"`
	Amount     string `json:"amount"`
}

func planReadAllowed(session identity.Session, side string) bool {
	return side == "RECEIVABLE" && can(session, "finance.collection.read") || side == "PAYABLE" && can(session, "finance.payment.read")
}

// All sources must be visible. Returning only the visible portion of a plan
// would leak its header total and misrepresent the installment amounts.
const planScopeSQL = `NOT EXISTS (
 SELECT 1 FROM finance_payment_plan_sources ps
 JOIN finance_invoice_open_items oi ON oi.company_id=ps.company_id AND oi.id=ps.open_item_id
 JOIN documents d ON d.company_id=oi.company_id AND d.id=oi.document_id
 WHERE ps.company_id=p.company_id AND ps.plan_id=p.id AND d.branch_id IS NOT NULL
 AND EXISTS(SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=p.company_id AND bs.user_id=$2)
 AND NOT EXISTS(SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=p.company_id AND bs.user_id=$2 AND bs.branch_id=d.branch_id))`

func validatePlan(input *PaymentPlanInput) (*big.Rat, error) {
	input.Description = strings.TrimSpace(input.Description)
	input.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))
	input.Side = strings.ToUpper(strings.TrimSpace(input.Side))
	if uuid.Validate(input.PartyID) != nil || input.Description == "" || len([]rune(input.Description)) > 500 ||
		len(input.Currency) != 3 || strings.Trim(input.Currency, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "" ||
		(input.Side != "RECEIVABLE" && input.Side != "PAYABLE") || len(input.OpenItemIDs) < 1 || len(input.OpenItemIDs) > 100 ||
		len(input.Installments) < 1 || len(input.Installments) > 120 || input.IdempotencyKey == "" || len(input.IdempotencyKey) > 200 {
		return nil, fmt.Errorf("%w: cari, yön, para birimi, açıklama, kaynak faturalar ve 1–120 taksit gereklidir", identity.ErrValidation)
	}
	sort.Strings(input.OpenItemIDs)
	for i, id := range input.OpenItemIDs {
		if uuid.Validate(id) != nil || i > 0 && id == input.OpenItemIDs[i-1] {
			return nil, fmt.Errorf("%w: kaynak faturalar geçerli ve benzersiz olmalıdır", identity.ErrValidation)
		}
	}
	total := new(big.Rat)
	previous := ""
	for i := range input.Installments {
		row := &input.Installments[i]
		date, err := time.Parse("2006-01-02", row.DueDate)
		if err != nil || date.Year() < 1900 || date.Year() > 9998 || row.DueDate < previous {
			return nil, fmt.Errorf("%w: taksit vadeleri geçerli ve tarih sırasıyla olmalıdır", identity.ErrValidation)
		}
		amount, err := parsePositive(row.Amount, 4)
		if err != nil {
			return nil, fmt.Errorf("%w: taksit tutarı pozitif ve en fazla dört ondalıklı olmalıdır", identity.ErrValidation)
		}
		row.Amount = amountString(amount, 4)
		total.Add(total, amount)
		previous = row.DueDate
	}
	return total, nil
}

func (s *Service) CreatePaymentPlan(ctx context.Context, session identity.Session, input PaymentPlanInput, meta identity.RequestMeta) (result PaymentPlan, resultErr error) {
	defer func() { resultErr = mapFinanceConstraint(resultErr) }()
	total, err := validatePlan(&input)
	if err != nil {
		return PaymentPlan{}, err
	}
	if !can(session, "finance.allocation.manage") || !planReadAllowed(session, input.Side) {
		return PaymentPlan{}, identity.ErrForbidden
	}
	raw, _ := json.Marshal(input)
	hash := fmt.Sprintf("%x", sha256.Sum256(raw))
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return PaymentPlan{}, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	var existingID, existingHash string
	err = tx.QueryRow(ctx, `SELECT id,request_hash FROM finance_payment_plans WHERE company_id=$1 AND idempotency_key=$2`, session.CurrentCompanyID, input.IdempotencyKey).Scan(&existingID, &existingHash)
	if err == nil {
		if existingHash != hash {
			return PaymentPlan{}, domainError(ErrIdempotencyConflict, "Aynı işlem anahtarı farklı planla kullanıldı.")
		}
		_ = tx.Rollback(ctx)
		return s.GetPaymentPlan(ctx, session, existingID)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return PaymentPlan{}, err
	}
	// Sorted invoice locks serialize plan creation with financial allocations.
	type source struct {
		id     string
		amount *big.Rat
	}
	sources := make([]source, 0, len(input.OpenItemIDs))
	openTotal := new(big.Rat)
	for _, id := range input.OpenItemIDs {
		var partyID, side, currency string
		var active bool
		err = tx.QueryRow(ctx, `SELECT oi.party_id,oi.side,oi.currency,pt.is_active
   FROM finance_invoice_open_items oi JOIN documents d ON d.company_id=oi.company_id AND d.id=oi.document_id
   JOIN parties pt ON pt.company_id=oi.company_id AND pt.id=oi.party_id
   WHERE oi.company_id=$1 AND oi.id=$2 AND d.status='POSTED' AND d.document_type_code IN ('SALES_INVOICE','PURCHASE_INVOICE')
    AND (d.branch_id IS NULL OR NOT EXISTS(SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=d.company_id AND bs.user_id=$3)
     OR EXISTS(SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=d.company_id AND bs.user_id=$3 AND bs.branch_id=d.branch_id)) FOR UPDATE OF oi`, session.CurrentCompanyID, id, session.User.ID).Scan(&partyID, &side, &currency, &active)
		if errors.Is(err, pgx.ErrNoRows) {
			return PaymentPlan{}, identity.ErrForbidden
		}
		if err != nil {
			return PaymentPlan{}, err
		}
		if partyID != input.PartyID || side != input.Side || currency != input.Currency || !active {
			return PaymentPlan{}, fmt.Errorf("%w: faturalar aynı aktif cari, yön ve para biriminde olmalıdır", identity.ErrValidation)
		}
		var hasPlan bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM finance_payment_plan_sources WHERE company_id=$1 AND open_item_id=$2 AND active)`, session.CurrentCompanyID, id).Scan(&hasPlan); err != nil {
			return PaymentPlan{}, err
		}
		if hasPlan {
			return PaymentPlan{}, fmt.Errorf("%w: seçilen faturanın aktif bir planı var; yeniden planlamadan önce mevcut planı iptal edin", identity.ErrConflict)
		}
		var amount string
		if err = tx.QueryRow(ctx, `SELECT COALESCE(sum(open_amount),0)::text FROM finance_scheduled_dues($1,$2,$3)`, session.CurrentCompanyID, s.now().Format("2006-01-02"), id).Scan(&amount); err != nil {
			return PaymentPlan{}, err
		}
		a := mustRat(amount)
		if a.Sign() <= 0 {
			return PaymentPlan{}, fmt.Errorf("%w: kapanmış fatura planlanamaz", identity.ErrValidation)
		}
		sources = append(sources, source{id, a})
		openTotal.Add(openTotal, a)
	}
	if total.Cmp(openTotal) != 0 {
		return PaymentPlan{}, fmt.Errorf("%w: taksit toplamı güncel açık tutara (%s %s) eşit olmalıdır; faturaları yenileyin", identity.ErrValidation, amountString(openTotal, 4), input.Currency)
	}
	id := uuid.NewString()
	_, err = tx.Exec(ctx, `INSERT INTO finance_payment_plans(id,company_id,party_id,side,currency,description,total_amount,idempotency_key,request_hash,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, id, session.CurrentCompanyID, input.PartyID, input.Side, input.Currency, input.Description, amountString(total, 4), input.IdempotencyKey, hash, session.User.ID)
	if err != nil {
		return PaymentPlan{}, mapFinanceConstraint(err)
	}
	for _, src := range sources {
		if _, err = tx.Exec(ctx, `INSERT INTO finance_payment_plan_sources(company_id,plan_id,open_item_id,amount) VALUES($1,$2,$3,$4)`, session.CurrentCompanyID, id, src.id, amountString(src.amount, 4)); err != nil {
			return PaymentPlan{}, mapFinanceConstraint(err)
		}
	}
	// Partition source intervals among chronological installments with exact decimals.
	index := 0
	used := new(big.Rat)
	for i, row := range input.Installments {
		left := mustRat(row.Amount)
		for left.Sign() > 0 {
			src := sources[index]
			available := new(big.Rat).Sub(src.amount, used)
			part := new(big.Rat).Set(left)
			if part.Cmp(available) > 0 {
				part.Set(available)
			}
			_, err = tx.Exec(ctx, `INSERT INTO finance_payment_plan_parts(company_id,plan_id,open_item_id,installment_no,due_date,amount,start_amount) VALUES($1,$2,$3,$4,$5,$6,$7)`, session.CurrentCompanyID, id, src.id, i+1, row.DueDate, amountString(part, 4), amountString(used, 4))
			if err != nil {
				return PaymentPlan{}, err
			}
			left.Sub(left, part)
			used.Add(used, part)
			if used.Cmp(src.amount) == 0 {
				index++
				used = new(big.Rat)
			}
		}
	}
	if err = writeAuditAndEventTx(ctx, tx, session, "FINANCE_PAYMENT_PLAN_CREATED", "finance.payment_plan.created", "finance_payment_plan", id, meta, map[string]any{"input": input}); err != nil {
		return PaymentPlan{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return PaymentPlan{}, mapFinanceConstraint(err)
	}
	return s.GetPaymentPlan(ctx, session, id)
}

func (s *Service) GetPaymentPlan(ctx context.Context, session identity.Session, id string) (PaymentPlan, error) {
	p := PaymentPlan{Sources: []PlanSource{}, Installments: []PlanInstallment{}}
	if identity.ValidateExternalActor(session) != nil {
		return p, identity.ErrForbidden
	}
	if uuid.Validate(id) != nil {
		return p, fmt.Errorf("%w: plan kimliği geçersiz", identity.ErrValidation)
	}
	err := s.pool.QueryRow(ctx, `SELECT p.id,p.party_id,pt.display_name,p.side,p.currency,p.description,p.total_amount::text,p.created_at,p.cancelled_at,p.cancel_reason,p.version
  FROM finance_payment_plans p JOIN parties pt ON pt.company_id=p.company_id AND pt.id=p.party_id
  WHERE p.company_id=$1 AND p.id=$3 AND `+planScopeSQL, session.CurrentCompanyID, session.User.ID, id).Scan(&p.ID, &p.PartyID, &p.PartyName, &p.Side, &p.Currency, &p.Description, &p.TotalAmount, &p.CreatedAt, &p.CancelledAt, &p.CancelReason, &p.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, identity.ErrForbidden
	}
	if err != nil {
		return p, err
	}
	if !planReadAllowed(session, p.Side) {
		return PaymentPlan{}, identity.ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `SELECT ps.open_item_id,oi.document_id,d.document_no,ps.amount::text FROM finance_payment_plan_sources ps
  JOIN finance_invoice_open_items oi ON oi.company_id=ps.company_id AND oi.id=ps.open_item_id
  JOIN documents d ON d.company_id=oi.company_id AND d.id=oi.document_id WHERE ps.company_id=$1 AND ps.plan_id=$2 ORDER BY d.document_no`, session.CurrentCompanyID, id)
	if err != nil {
		return p, err
	}
	for rows.Next() {
		var src PlanSource
		if err = rows.Scan(&src.OpenItemID, &src.DocumentID, &src.DocumentNo, &src.Amount); err != nil {
			rows.Close()
			return p, err
		}
		p.Sources = append(p.Sources, src)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return p, err
	}
	rows, err = s.pool.Query(ctx, `SELECT d.installment_no,d.due_date::text,sum(d.amount)::text,
  sum(CASE WHEN $4 THEN d.amount ELSE COALESCE(sd.open_amount,0) END)::text
  FROM finance_payment_plan_parts d LEFT JOIN finance_scheduled_dues($1,$3,NULL,$2) sd
   ON sd.plan_id=d.plan_id AND sd.open_item_id=d.open_item_id AND sd.installment_no=d.installment_no
  WHERE d.company_id=$1 AND d.plan_id=$2 GROUP BY d.installment_no,d.due_date ORDER BY d.installment_no`, session.CurrentCompanyID, id, s.now().Format("2006-01-02"), p.CancelledAt != nil)
	if err != nil {
		return p, err
	}
	for rows.Next() {
		var row PlanInstallment
		if err = rows.Scan(&row.Number, &row.DueDate, &row.Amount, &row.OpenAmount); err != nil {
			rows.Close()
			return p, err
		}
		row.ClosedAmount = amountString(new(big.Rat).Sub(mustRat(row.Amount), mustRat(row.OpenAmount)), 4)
		row.Status = "PENDING"
		if mustRat(row.OpenAmount).Sign() == 0 {
			row.Status = "CLOSED"
		} else if row.DueDate < s.now().Format("2006-01-02") {
			row.Status = "OVERDUE"
		} else if mustRat(row.ClosedAmount).Sign() > 0 {
			row.Status = "PARTIAL"
		}
		if p.CancelledAt != nil {
			row.Status = "CANCELLED"
		}
		row.Allocations = []AllocationInput{}
		p.Installments = append(p.Installments, row)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return p, err
	}
	if p.CancelledAt == nil {
		rows, err = s.pool.Query(ctx, `SELECT installment_no,open_item_id,open_amount::text FROM finance_scheduled_dues($1,$2,NULL,$3) WHERE plan_id=$3 AND open_amount>0 ORDER BY installment_no,open_item_id`, session.CurrentCompanyID, s.now().Format("2006-01-02"), id)
		if err != nil {
			return p, err
		}
		defer rows.Close()
		for rows.Next() {
			var n int
			var a AllocationInput
			if err = rows.Scan(&n, &a.OpenItemID, &a.Amount); err != nil {
				return p, err
			}
			for i := range p.Installments {
				if p.Installments[i].Number == n {
					p.Installments[i].Allocations = append(p.Installments[i].Allocations, a)
				}
			}
		}
		if err = rows.Err(); err != nil {
			return p, err
		}
	}
	return p, nil
}

func (s *Service) CancelPaymentPlan(ctx context.Context, session identity.Session, id string, version int64, reason string, meta identity.RequestMeta) (result PaymentPlan, resultErr error) {
	defer func() { resultErr = mapFinanceConstraint(resultErr) }()
	if !can(session, "finance.allocation.manage") {
		return PaymentPlan{}, identity.ErrForbidden
	}
	p, err := s.GetPaymentPlan(ctx, session, id)
	if err != nil {
		return p, err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" || len([]rune(reason)) > 500 {
		return p, fmt.Errorf("%w: iptal gerekçesi gereklidir (en fazla 500 karakter)", identity.ErrValidation)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return p, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	tag, err := tx.Exec(ctx, `UPDATE finance_payment_plans SET cancelled_at=now(),cancel_reason=$4,version=version+1 WHERE company_id=$1 AND id=$2 AND version=$3 AND cancelled_at IS NULL`, session.CurrentCompanyID, id, version, reason)
	if err != nil {
		return p, err
	}
	if tag.RowsAffected() != 1 {
		return p, identity.ErrConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE finance_payment_plan_sources SET active=false WHERE company_id=$1 AND plan_id=$2`, session.CurrentCompanyID, id); err != nil {
		return p, err
	}
	if err = writeAuditAndEventTx(ctx, tx, session, "FINANCE_PAYMENT_PLAN_CANCELLED", "finance.payment_plan.cancelled", "finance_payment_plan", id, meta, map[string]any{"reason": reason}); err != nil {
		return p, err
	}
	if err = tx.Commit(ctx); err != nil {
		return p, mapFinanceConstraint(err)
	}
	return s.GetPaymentPlan(ctx, session, id)
}

// A plan payment uses ordinary immutable payments and allocations, while
// checking the selected installment in that same serializable transaction.
func (s *Service) validateInstallmentPaymentTx(ctx context.Context, tx pgx.Tx, session identity.Session, input PaymentInput, amount *big.Rat) error {
	if uuid.Validate(input.PaymentPlanID) != nil || input.InstallmentNo < 1 || input.AutoAllocate || len(input.Allocations) == 0 {
		return fmt.Errorf("%w: taksit ve fatura eşleştirmeleri gereklidir", identity.ErrValidation)
	}
	var partyID, side, currency string
	var created time.Time
	err := tx.QueryRow(ctx, `SELECT p.party_id,p.side,p.currency,p.created_at FROM finance_payment_plans p WHERE p.company_id=$1 AND p.id=$3 AND p.cancelled_at IS NULL AND `+planScopeSQL+` FOR SHARE OF p`, session.CurrentCompanyID, session.User.ID, input.PaymentPlanID).Scan(&partyID, &side, &currency, &created)
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.ErrForbidden
	}
	if err != nil {
		return err
	}
	if partyID != input.PartyID || currency != input.Currency || (side == "RECEIVABLE") != (input.PaymentKind == "COLLECTION") || input.TransactionDate.Format("2006-01-02") < created.Format("2006-01-02") {
		return fmt.Errorf("%w: ödeme cari, yön, para birimi veya tarihi planla uyumlu değil", identity.ErrValidation)
	}
	var first int
	if err = tx.QueryRow(ctx, `SELECT COALESCE(min(installment_no),0) FROM finance_scheduled_dues($1,$2,NULL,$3) WHERE plan_id=$3 AND open_amount>0`, session.CurrentCompanyID, s.now().Format("2006-01-02"), input.PaymentPlanID).Scan(&first); err != nil {
		return err
	}
	if first != input.InstallmentNo {
		return fmt.Errorf("%w: önce en eski açık taksiti kapatın; planı yenileyin", identity.ErrValidation)
	}
	sum := new(big.Rat)
	for _, a := range input.Allocations {
		var remaining string
		err = tx.QueryRow(ctx, `SELECT open_amount::text FROM finance_scheduled_dues($1,$2,NULL,$3) WHERE plan_id=$3 AND installment_no=$4 AND open_item_id=$5`, session.CurrentCompanyID, s.now().Format("2006-01-02"), input.PaymentPlanID, input.InstallmentNo, a.OpenItemID).Scan(&remaining)
		if errors.Is(err, pgx.ErrNoRows) {
			return identity.ErrForbidden
		}
		if err != nil {
			return err
		}
		value, e := parsePositive(a.Amount, 4)
		if e != nil || value.Cmp(mustRat(remaining)) > 0 {
			return domainError(ErrPaymentAllocationExceedsOpenAmount, "Taksitin kalan tutarı değişti; planı yenileyin.")
		}
		sum.Add(sum, value)
	}
	if sum.Cmp(amount) != 0 {
		return fmt.Errorf("%w: taksit eşleştirme toplamı ödeme tutarına eşit olmalıdır", identity.ErrValidation)
	}
	return nil
}
