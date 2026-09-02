package purchasing

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// LandedCost is a freight/customs/insurance charge that belongs in the stock
// cost basis but arrives on a document separate from the goods receipt it
// applies to. Posting it never rewrites a stock_cost_layers.unit_cost; it
// appends a stock_cost_adjustments row (reason_code='LANDED_COST') against
// the layer each target receipt line's own IN movement opened.
type LandedCost struct {
	ID               string           `json:"id"`
	CompanyID        string           `json:"company_id"`
	DocumentNo       string           `json:"document_no"`
	GoodsReceiptID   string           `json:"goods_receipt_id"`
	SupplierID       string           `json:"supplier_id,omitempty"`
	Description      string           `json:"description,omitempty"`
	Amount           string           `json:"amount"`
	Currency         string           `json:"currency"`
	ExchangeRate     string           `json:"exchange_rate"`
	AllocationMethod string           `json:"allocation_method"`
	Status           string           `json:"status"`
	PostedAt         *time.Time       `json:"posted_at,omitempty"`
	CancelledAt      *time.Time       `json:"cancelled_at,omitempty"`
	CreatedBy        string           `json:"created_by"`
	UpdatedBy        string           `json:"updated_by"`
	Version          int64            `json:"version"`
	Lines            []LandedCostLine `json:"lines,omitempty"`
}

// LandedCostLine is the resolved share for one target goods receipt line.
// Amount is always server-computed from AllocationMethod; a client cannot
// submit its own split.
type LandedCostLine struct {
	GoodsReceiptLineID string `json:"goods_receipt_line_id"`
	AllocatedAmount    string `json:"allocated_amount"`
}

type LandedCostInput struct {
	DocumentNo       string `json:"document_no,omitempty"`
	GoodsReceiptID   string `json:"goods_receipt_id"`
	SupplierID       string `json:"supplier_id,omitempty"`
	Description      string `json:"description,omitempty"`
	Amount           string `json:"amount"`
	Currency         string `json:"currency"`
	ExchangeRate     string `json:"exchange_rate,omitempty"`
	AllocationMethod string `json:"allocation_method,omitempty"`
}

const (
	LandedCostByAmount   = "BY_AMOUNT"
	LandedCostByQuantity = "BY_QUANTITY"
)

var ErrLandedCostNoLines = errors.New("goods receipt has no product lines to allocate a landed cost across")

// CreateLandedCost drafts a landed cost against a goods receipt's product
// lines. The draft can be recomputed and re-saved freely; nothing outside
// this table is touched until PostLandedCost.
func (s *Service) CreateLandedCost(ctx context.Context, session identity.Session, input LandedCostInput, meta identity.RequestMeta) (LandedCost, error) {
	if identity.ValidateExternalActor(session) != nil || !session.HasPermission("purchase.landed_cost.manage") {
		return LandedCost{}, identity.ErrForbidden
	}
	input.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))
	input.AllocationMethod = strings.ToUpper(strings.TrimSpace(input.AllocationMethod))
	if input.AllocationMethod == "" {
		input.AllocationMethod = LandedCostByAmount
	}
	if uuid.Validate(strings.TrimSpace(input.GoodsReceiptID)) != nil || !validCurrency(input.Currency) ||
		(input.AllocationMethod != LandedCostByAmount && input.AllocationMethod != LandedCostByQuantity) ||
		!validPurchaseDecimal(input.Amount, false) || compare(zero(input.Amount), "0") <= 0 {
		return LandedCost{}, validation("masraf dağıtımı geçersiz")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return LandedCost{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	replayID, replay, err := reserveCommand(ctx, tx, session, meta, "purchasing.landed_cost.create", input)
	if err != nil {
		return LandedCost{}, err
	}
	if replay {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return s.GetLandedCost(ctx, session, replayID)
	}

	var branchID, receiptStatus string
	var receiptDate time.Time
	if err = tx.QueryRow(ctx, `SELECT branch_id,status,receipt_date FROM goods_receipts WHERE company_id=$1 AND id=$2 FOR SHARE`, session.CurrentCompanyID, input.GoodsReceiptID).Scan(&branchID, &receiptStatus, &receiptDate); errors.Is(err, pgx.ErrNoRows) {
		return LandedCost{}, ErrNotFound
	} else if err != nil {
		return LandedCost{}, err
	}
	if receiptStatus != "POSTED" {
		return LandedCost{}, validation("masraf dağıtımı yalnızca sonlandırılmış mal kabule uygulanabilir")
	}
	if err = s.ensureExchangeRate(ctx, session, input.Currency, receiptDate, &input.ExchangeRate); err != nil {
		return LandedCost{}, err
	}
	if err = ensurePurchaseBranch(ctx, tx, session, branchID); err != nil {
		return LandedCost{}, err
	}
	if strings.TrimSpace(input.SupplierID) != "" {
		if err = s.ensureSupplier(ctx, session.CurrentCompanyID, input.SupplierID); err != nil {
			return LandedCost{}, err
		}
	}

	documentNo, err := s.number(ctx, tx, session.CurrentCompanyID, "PURCHASE_LANDED_COST", "MASRAF", input.DocumentNo, time.Now().Year())
	if err != nil {
		return LandedCost{}, err
	}
	id := uuid.NewString()
	if _, err = tx.Exec(ctx, `INSERT INTO purchase_landed_costs(id,company_id,document_no,goods_receipt_id,supplier_id,description,amount,currency,exchange_rate,allocation_method,created_by,updated_by)
		VALUES($1,$2,$3,$4,NULLIF($5,'')::uuid,$6,$7,$8,$9,$10,$11,$11)`,
		id, session.CurrentCompanyID, documentNo, input.GoodsReceiptID, input.SupplierID, strings.TrimSpace(input.Description), input.Amount, input.Currency, input.ExchangeRate, input.AllocationMethod, session.User.ID); err != nil {
		return LandedCost{}, err
	}
	var baseCurrency string
	if err = tx.QueryRow(ctx, `SELECT base_currency::text FROM companies WHERE id=$1`, session.CurrentCompanyID).Scan(&baseCurrency); err != nil {
		return LandedCost{}, err
	}
	rate, _ := new(big.Rat).SetString(input.ExchangeRate)
	if rate == nil || rate.Sign() <= 0 {
		rate = big.NewRat(1, 1)
	}
	sameCurrency := strings.EqualFold(input.Currency, baseCurrency)
	lines, err := allocateLandedCostLinesTx(ctx, tx, session.CurrentCompanyID, input.GoodsReceiptID, input.Amount, input.AllocationMethod)
	if err != nil {
		return LandedCost{}, err
	}
	for _, line := range lines {
		baseAmount := line.AllocatedAmount
		if !sameCurrency {
			if alloc, ok := new(big.Rat).SetString(line.AllocatedAmount); ok {
				baseAmount = new(big.Rat).Quo(alloc, rate).FloatString(8)
			}
		}
		if _, err = tx.Exec(ctx, `INSERT INTO purchase_landed_cost_lines(id,company_id,landed_cost_id,goods_receipt_line_id,allocated_amount,base_allocated_amount,base_currency)
			VALUES($1,$2,$3,$4,$5,$6,$7)`, uuid.NewString(), session.CurrentCompanyID, id, line.GoodsReceiptLineID, line.AllocatedAmount, baseAmount, baseCurrency); err != nil {
			return LandedCost{}, err
		}
	}
	if err = s.auditEventTx(ctx, tx, session, "PURCHASE_LANDED_COST_CREATED", "purchase.landed_cost.created", id, meta, map[string]any{"document_no": documentNo, "goods_receipt_id": input.GoodsReceiptID}); err != nil {
		return LandedCost{}, err
	}
	if err = completeCommand(ctx, tx, session, meta, id); err != nil {
		return LandedCost{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return LandedCost{}, err
	}
	return s.GetLandedCost(ctx, session, id)
}

// allocateLandedCostLinesTx computes the deterministic, exactly-reconciling
// split of amount across a goods receipt's product lines. BY_AMOUNT weighs
// each line by accepted_quantity*unit_cost (what it's worth); BY_QUANTITY
// weighs it by accepted_quantity alone. A truncate-then-largest-remainder
// pass hands out the last kuruş so the shares always sum to amount exactly,
// never a rounded approximation of it.
func allocateLandedCostLinesTx(ctx context.Context, tx pgx.Tx, companyID, goodsReceiptID, amount, method string) ([]LandedCostLine, error) {
	rows, err := tx.Query(ctx, `
		SELECT l.id, l.accepted_quantity::text, l.unit_cost::text
		  FROM goods_receipt_lines l
		 WHERE l.company_id=$1 AND l.receipt_id=$2 AND l.accepted_quantity > 0
		 ORDER BY l.line_no`, companyID, goodsReceiptID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type candidate struct {
		lineID string
		weight *big.Rat
	}
	candidates := make([]candidate, 0, 8)
	total := new(big.Rat)
	for rows.Next() {
		var lineID, quantityText, unitCostText string
		if err = rows.Scan(&lineID, &quantityText, &unitCostText); err != nil {
			return nil, err
		}
		quantity, _ := new(big.Rat).SetString(quantityText)
		if quantity == nil {
			continue
		}
		weight := new(big.Rat).Set(quantity)
		if method == LandedCostByAmount {
			unitCost, _ := new(big.Rat).SetString(unitCostText)
			if unitCost != nil {
				weight.Mul(weight, unitCost)
			}
		}
		if weight.Sign() <= 0 {
			continue
		}
		candidates = append(candidates, candidate{lineID: lineID, weight: weight})
		total.Add(total, weight)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(candidates) == 0 || total.Sign() <= 0 {
		return nil, ErrLandedCostNoLines
	}

	target, ok := new(big.Rat).SetString(amount)
	if !ok {
		return nil, validation("masraf tutarı geçersiz")
	}
	const scale = 8
	factor := new(big.Rat).SetInt64(1)
	for i := 0; i < scale; i++ {
		factor.Mul(factor, big.NewRat(10, 1))
	}
	shares := make([]*big.Rat, len(candidates))
	remainders := make([]*big.Rat, len(candidates))
	assigned := new(big.Rat)
	for i, c := range candidates {
		exact := new(big.Rat).Quo(new(big.Rat).Mul(target, c.weight), total)
		scaled := new(big.Rat).Mul(exact, factor)
		truncatedInt := new(big.Int).Quo(scaled.Num(), scaled.Denom())
		truncated := new(big.Rat).Quo(new(big.Rat).SetInt(truncatedInt), factor)
		shares[i] = truncated
		remainders[i] = new(big.Rat).Sub(exact, truncated)
		assigned.Add(assigned, truncated)
	}
	unit := new(big.Rat).Quo(big.NewRat(1, 1), factor)
	leftover := new(big.Rat).Sub(target, assigned)
	for leftover.Sign() > 0 {
		best := -1
		for i := range candidates {
			if best == -1 || remainders[i].Cmp(remainders[best]) > 0 {
				best = i
			}
		}
		if best == -1 {
			break
		}
		shares[best].Add(shares[best], unit)
		remainders[best] = new(big.Rat)
		leftover.Sub(leftover, unit)
	}

	result := make([]LandedCostLine, len(candidates))
	for i, c := range candidates {
		result[i] = LandedCostLine{GoodsReceiptLineID: c.lineID, AllocatedAmount: shares[i].FloatString(scale)}
	}
	return result, nil
}

// PostLandedCost is the only irreversible step: it locks the target layers,
// appends one stock_cost_adjustments row per line and marks the document
// POSTED. A layer's own unit_cost is never rewritten -- valuation is the
// layer plus every adjustment against it.
func (s *Service) PostLandedCost(ctx context.Context, session identity.Session, id string, expectedVersion int64, meta identity.RequestMeta) (LandedCost, error) {
	if identity.ValidateExternalActor(session) != nil || !session.HasPermission("purchase.landed_cost.post") {
		return LandedCost{}, identity.ErrForbidden
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return LandedCost{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	replayID, replay, err := reserveCommand(ctx, tx, session, meta, "purchasing.landed_cost.post", map[string]any{"id": id, "version": expectedVersion})
	if err != nil {
		return LandedCost{}, err
	}
	if replay {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return s.GetLandedCost(ctx, session, replayID)
	}

	var status, currency, amount, exchangeRate string
	var branchID, receiptStatus string
	if err = tx.QueryRow(ctx, `SELECT lc.status,lc.currency,lc.amount::text,lc.exchange_rate::text,gr.branch_id,gr.status
		FROM purchase_landed_costs lc JOIN goods_receipts gr ON gr.company_id=lc.company_id AND gr.id=lc.goods_receipt_id
		WHERE lc.company_id=$1 AND lc.id=$2 FOR UPDATE OF lc`, session.CurrentCompanyID, id).Scan(&status, &currency, &amount, &exchangeRate, &branchID, &receiptStatus); errors.Is(err, pgx.ErrNoRows) {
		return LandedCost{}, ErrNotFound
	} else if err != nil {
		return LandedCost{}, err
	}
	if status != "DRAFT" {
		return LandedCost{}, ErrInvalidTransition
	}
	if receiptStatus != "POSTED" {
		return LandedCost{}, validation("masraf dağıtımı yalnızca sonlandırılmış mal kabule uygulanabilir")
	}
	if err = ensurePurchaseBranch(ctx, tx, session, branchID); err != nil {
		return LandedCost{}, err
	}

	var baseCurrency string
	if err = tx.QueryRow(ctx, `SELECT base_currency::text FROM companies WHERE id=$1`, session.CurrentCompanyID).Scan(&baseCurrency); err != nil {
		return LandedCost{}, err
	}
	rows, err := tx.Query(ctx, `SELECT lcl.goods_receipt_line_id, lcl.allocated_amount::text
		FROM purchase_landed_cost_lines lcl WHERE lcl.company_id=$1 AND lcl.landed_cost_id=$2`, session.CurrentCompanyID, id)
	if err != nil {
		return LandedCost{}, err
	}
	type target struct{ receiptLineID, allocated string }
	targets := make([]target, 0, 8)
	for rows.Next() {
		var t target
		if err = rows.Scan(&t.receiptLineID, &t.allocated); err != nil {
			rows.Close()
			return LandedCost{}, err
		}
		targets = append(targets, t)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return LandedCost{}, err
	}

	rate, _ := new(big.Rat).SetString(exchangeRate)
	if rate == nil || rate.Sign() <= 0 {
		rate = big.NewRat(1, 1)
	}
	sameCurrency := strings.EqualFold(currency, baseCurrency)
	for _, t := range targets {
		var layerID string
		if err = tx.QueryRow(ctx, `SELECT scl.id FROM stock_cost_layers scl
			JOIN stock_movements m ON m.company_id=scl.company_id AND m.id=scl.source_movement_id
			WHERE scl.company_id=$1 AND m.source_line_id=$2 AND m.direction='IN' FOR UPDATE`, session.CurrentCompanyID, t.receiptLineID).Scan(&layerID); errors.Is(err, pgx.ErrNoRows) {
			// Every stored allocation line has accepted_quantity > 0, so a
			// POSTED goods receipt must have opened a cost layer for it. A
			// missing layer means the charge would silently vanish -- fail
			// closed instead of posting a zero-effect document.
			return LandedCost{}, validation("mal kabul satırı için stok maliyet katmanı bulunamadı")
		} else if err != nil {
			return LandedCost{}, err
		}
		allocated, _ := new(big.Rat).SetString(t.allocated)
		if allocated == nil {
			continue
		}
		baseAllocated := allocated
		if !sameCurrency {
			baseAllocated = new(big.Rat).Quo(allocated, rate)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO stock_cost_adjustments(id,company_id,layer_id,reason_code,amount,currency,base_amount,base_currency,source_type,source_id,created_by)
			VALUES($1,$2,$3,'LANDED_COST',$4,$5,$6,$7,'PURCHASE_LANDED_COST',$8,$9)`,
			uuid.NewString(), session.CurrentCompanyID, layerID, t.allocated, currency, baseAllocated.FloatString(8), baseCurrency, id, session.User.ID); err != nil {
			return LandedCost{}, err
		}
	}

	tag, err := tx.Exec(ctx, `UPDATE purchase_landed_costs SET status='POSTED', posted_at=now(), updated_by=$1, version=version+1
		WHERE company_id=$2 AND id=$3 AND version=$4`, session.User.ID, session.CurrentCompanyID, id, expectedVersion)
	if err != nil {
		return LandedCost{}, err
	}
	if tag.RowsAffected() == 0 {
		return LandedCost{}, identity.ErrConflict
	}
	if err = s.auditEventTx(ctx, tx, session, "PURCHASE_LANDED_COST_POSTED", "purchase.landed_cost.posted", id, meta, map[string]any{"amount": amount, "currency": currency}); err != nil {
		return LandedCost{}, err
	}
	if err = completeCommand(ctx, tx, session, meta, id); err != nil {
		return LandedCost{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return LandedCost{}, err
	}
	return s.GetLandedCost(ctx, session, id)
}

func (s *Service) GetLandedCost(ctx context.Context, session identity.Session, id string) (LandedCost, error) {
	if !session.HasPermission("purchase.landed_cost.manage") && !session.HasPermission("purchase.landed_cost.post") {
		return LandedCost{}, identity.ErrForbidden
	}
	var item LandedCost
	var supplierID *string
	var postedAt, cancelledAt *time.Time
	var receiptBranchID, receiptWarehouseID string
	err := s.pool.QueryRow(ctx, `SELECT lc.id,lc.company_id,lc.document_no,lc.goods_receipt_id,lc.supplier_id,lc.description,lc.amount::text,lc.currency,lc.exchange_rate::text,lc.allocation_method,lc.status,lc.posted_at,lc.cancelled_at,lc.created_by,lc.updated_by,lc.version,gr.branch_id,COALESCE(gr.warehouse_id::text,'')
		FROM purchase_landed_costs lc JOIN goods_receipts gr ON gr.company_id=lc.company_id AND gr.id=lc.goods_receipt_id
		WHERE lc.company_id=$1 AND lc.id=$2`, session.CurrentCompanyID, id).Scan(
		&item.ID, &item.CompanyID, &item.DocumentNo, &item.GoodsReceiptID, &supplierID, &item.Description, &item.Amount, &item.Currency, &item.ExchangeRate, &item.AllocationMethod, &item.Status, &postedAt, &cancelledAt, &item.CreatedBy, &item.UpdatedBy, &item.Version, &receiptBranchID, &receiptWarehouseID)
	if errors.Is(err, pgx.ErrNoRows) {
		return LandedCost{}, ErrNotFound
	}
	if err != nil {
		return LandedCost{}, err
	}
	if err = ensurePurchaseReadScope(ctx, s.pool, session, receiptBranchID, receiptWarehouseID); err != nil {
		return LandedCost{}, err
	}
	if supplierID != nil {
		item.SupplierID = *supplierID
	}
	item.PostedAt, item.CancelledAt = postedAt, cancelledAt

	rows, err := s.pool.Query(ctx, `SELECT goods_receipt_line_id, allocated_amount::text FROM purchase_landed_cost_lines WHERE company_id=$1 AND landed_cost_id=$2 ORDER BY goods_receipt_line_id`, session.CurrentCompanyID, id)
	if err != nil {
		return LandedCost{}, err
	}
	defer rows.Close()
	item.Lines = []LandedCostLine{}
	for rows.Next() {
		var line LandedCostLine
		if err = rows.Scan(&line.GoodsReceiptLineID, &line.AllocatedAmount); err != nil {
			return LandedCost{}, err
		}
		item.Lines = append(item.Lines, line)
	}
	return item, rows.Err()
}
