package commerce

import (
	"context"
	"errors"
	"math/big"
	"strings"
)

// RiskDecision is what a customer's credit/risk limit check resolved to.
type RiskDecision string

const (
	RiskAllow RiskDecision = "ALLOW"
	RiskWarn  RiskDecision = "WARN"
	RiskBlock RiskDecision = "BLOCK"
)

// RiskEvaluation is a snapshot of one customer's exposure against their own
// limits, in the company's base currency. CurrentBalance is derived from the
// same canonical signed projection used by the cari balance endpoints, so a
// risk check can never see a number the cari ekstre itself would disagree with.
type RiskEvaluation struct {
	Decision         RiskDecision
	CurrentBalance   string
	AdditionalAmount string
	ProjectedBalance string
	CreditLimit      string
	RiskLimit        string
	BaseCurrency     string
}

// EvaluateSalesRisk projects a party's receivable exposure with
// additionalAmount added and resolves it against credit_limit / risk_limit
// under the party's own risk_policy. A limit of zero is read as "no limit"
// -- most companies leave it unset for most customers, and a strict zero
// ceiling would block every sale for them by default.
func EvaluateSalesRisk(ctx context.Context, q Querier, companyID, partyID, additionalAmount string) (RiskEvaluation, error) {
	var creditLimit, riskLimit, policy string
	// The party row is the serialization point for risk checks and the
	// ledger-writing commands which take the same lock.  Without this lock two
	// concurrent sales could both pass against the same remaining limit.
	if err := q.QueryRow(ctx, `SELECT credit_limit::text, risk_limit::text, risk_policy::text FROM parties WHERE company_id=$1 AND id=$2 FOR UPDATE`,
		companyID, partyID).Scan(&creditLimit, &riskLimit, &policy); err != nil {
		return RiskEvaluation{}, err
	}
	var baseCurrency string
	if err := q.QueryRow(ctx, `SELECT base_currency::text FROM companies WHERE id=$1`, companyID).Scan(&baseCurrency); err != nil {
		return RiskEvaluation{}, err
	}
	var currentBalanceText string
	if err := q.QueryRow(ctx, `SELECT COALESCE(SUM(base_signed_amount),0)::text
		FROM party_ledger_balance_effects WHERE company_id=$1 AND party_id=$2`,
		companyID, partyID).Scan(&currentBalanceText); err != nil {
		return RiskEvaluation{}, err
	}
	var committedOrderText string
	if err := q.QueryRow(ctx, `SELECT COALESCE(SUM(ROUND(
		(l.payable_amount / NULLIF(l.base_quantity,0)) *
		(CASE WHEN l.line_type='PRODUCT' THEN
			GREATEST(
				COALESCE((SELECT SUM(f.base_quantity) FROM commercial_line_allocations f
					WHERE f.company_id=l.company_id AND f.source_line_id=l.id
					  AND f.allocation_type='FULFILLMENT' AND f.status='CONSUMED'),0)
				-
				(COALESCE((SELECT SUM(a.base_quantity) FROM commercial_line_allocations a
					WHERE a.company_id=l.company_id AND a.source_line_id=l.id
					  AND a.allocation_type='INVOICING' AND a.status='CONSUMED'),0)
				 + COALESCE((SELECT SUM(a2.base_quantity) FROM commercial_line_allocations a2
					JOIN commercial_line_allocations f2 ON f2.company_id=a2.company_id
					 AND f2.target_line_id=a2.source_line_id
					 AND f2.allocation_type='FULFILLMENT' AND f2.status='CONSUMED'
					WHERE f2.company_id=l.company_id AND f2.source_line_id=l.id
					  AND a2.allocation_type='INVOICING' AND a2.status='CONSUMED'),0)),0)
		 ELSE GREATEST(l.base_quantity-COALESCE((SELECT SUM(a.base_quantity)
			FROM commercial_line_allocations a WHERE a.company_id=l.company_id
			 AND a.source_line_id=l.id AND a.allocation_type='INVOICING'
			 AND a.status='CONSUMED'),0),0) END) * o.exchange_rate,4)),0)::text
		FROM sales_orders o JOIN sales_order_lines l ON l.company_id=o.company_id AND l.document_id=o.id
		WHERE o.company_id=$1 AND o.party_id=$2 AND o.status IN ('CONFIRMED','PARTIALLY_FULFILLED','FULFILLED')`,
		companyID, partyID).Scan(&committedOrderText); err != nil {
		return RiskEvaluation{}, err
	}

	current, _ := new(big.Rat).SetString(currentBalanceText)
	if current == nil {
		current = new(big.Rat)
	}
	additional, ok := new(big.Rat).SetString(strings.TrimSpace(additionalAmount))
	if !ok {
		additional = new(big.Rat)
	}
	committed, ok := new(big.Rat).SetString(committedOrderText)
	if !ok {
		committed = new(big.Rat)
	}
	projected := new(big.Rat).Add(new(big.Rat).Add(current, committed), additional)

	eval := RiskEvaluation{
		CurrentBalance:   current.FloatString(4),
		AdditionalAmount: additional.FloatString(4),
		ProjectedBalance: projected.FloatString(4),
		CreditLimit:      creditLimit,
		RiskLimit:        riskLimit,
		BaseCurrency:     baseCurrency,
	}

	exceeds := func(limitText string) bool {
		limit, ok := new(big.Rat).SetString(limitText)
		return ok && limit.Sign() > 0 && projected.Cmp(limit) > 0
	}
	if !exceeds(creditLimit) && !exceeds(riskLimit) {
		eval.Decision = RiskAllow
		return eval, nil
	}
	switch strings.ToUpper(strings.TrimSpace(policy)) {
	case "BLOCK":
		eval.Decision = RiskBlock
	case "ALLOW":
		eval.Decision = RiskAllow
	default:
		eval.Decision = RiskWarn
	}
	return eval, nil
}

// ErrRiskLimitExceeded is returned when a BLOCK policy stops a command; the
// caller maps it to the risk override permission check.
var ErrRiskLimitExceeded = errors.New("commerce: customer risk or credit limit exceeded")
