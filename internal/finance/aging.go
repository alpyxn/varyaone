package finance

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/google/uuid"
)

// PartyAgingRow is one party's open (unsettled) invoice balance in one
// currency, split into due-date buckets measured against the report's asOf
// date. Amounts stay in the item's own transaction currency -- a party with
// invoices in two currencies produces two rows, so nothing is silently
// converted with a rate the report never shows.
type PartyAgingRow struct {
	PartyID      string `json:"party_id"`
	PartyCode    string `json:"party_code"`
	PartyName    string `json:"party_name"`
	Side         string `json:"side"`
	Currency     string `json:"currency"`
	NotDue       string `json:"not_due"`
	Bucket0To30  string `json:"days_0_30"`
	Bucket31To60 string `json:"days_31_60"`
	Bucket61To90 string `json:"days_61_90"`
	Bucket90Plus string `json:"days_90_plus"`
	Overdue      string `json:"overdue_total"`
	Total        string `json:"total"`
}

// PartyAgingReport is the aging read model: rows plus per-currency totals.
type PartyAgingReport struct {
	AsOf  string          `json:"as_of"`
	Items []PartyAgingRow `json:"items"`
}

// PartyAging groups every still-open invoice item into due-date buckets per
// party and currency. The open amount uses the very same derivation as
// ListOpenItemsPage (original minus allocations, minus open-item reversal,
// minus returns); a second, diverging definition of "borç" would be worse
// than no report at all.
//
// Every component is taken as of asOf on its own business date -- the invoice's
// document date, the payment's transaction date behind an allocation, the
// ledger date of an open-item reversal, the return's document date. Reading
// today's allocations into a backdated report would show an invoice as
// collected months before the collection existed. Finance refuses future-dated
// postings, so for asOf = today (the default) these filters match everything
// and the report is unchanged.
func (s *Service) PartyAging(ctx context.Context, session identity.Session, asOf time.Time, partyID, currency, side string) (PartyAgingReport, error) {
	report := PartyAgingReport{Items: []PartyAgingRow{}}
	currency = strings.ToUpper(strings.TrimSpace(currency))
	side = strings.ToUpper(strings.TrimSpace(side))
	if partyID != "" {
		if _, err := uuid.Parse(partyID); err != nil {
			return report, fmt.Errorf("%w: cari kimliği geçersiz", identity.ErrValidation)
		}
	}
	if currency != "" && (len(currency) != 3 || strings.Trim(currency, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "") {
		return report, fmt.Errorf("%w: yaşlandırma para birimi geçersiz", identity.ErrValidation)
	}
	if side != "" && side != "RECEIVABLE" && side != "PAYABLE" {
		return report, fmt.Errorf("%w: yaşlandırma yönü geçersiz", identity.ErrValidation)
	}
	canReceivable, canPayable := can(session, "finance.collection.read"), can(session, "finance.payment.read")
	if side == "RECEIVABLE" && !canReceivable {
		return report, identity.ErrForbidden
	}
	if side == "PAYABLE" && !canPayable {
		return report, identity.ErrForbidden
	}
	if side == "" && !canReceivable && !canPayable {
		return report, identity.ErrForbidden
	}
	// A caller holding only one directional permission must not receive the
	// other side merely by omitting the filter.
	if side == "" && canReceivable != canPayable {
		if canReceivable {
			side = "RECEIVABLE"
		} else {
			side = "PAYABLE"
		}
	}
	if asOf.IsZero() {
		asOf = time.Now().UTC()
	}

	args := []any{session.CurrentCompanyID, asOf.Format("2006-01-02")}
	query := `
WITH open_items AS (
    SELECT oi.party_id, oi.side, oi.currency,
           GREATEST(oi.original_amount
                    - COALESCE((SELECT SUM(CASE WHEN a.reversal_of_id IS NULL THEN a.amount ELSE -a.amount END)
                                  FROM finance_payment_allocations a
                                  JOIN finance_payments p ON p.company_id=a.company_id AND p.id=a.payment_id
                                 WHERE a.company_id=oi.company_id AND a.open_item_id=oi.id
                                   AND p.transaction_date <= $2::date), 0)
                    - COALESCE((SELECT r.amount FROM finance_invoice_open_item_reversals r
                                  JOIN party_ledger_entries l ON l.company_id=r.company_id AND l.id=r.reversal_ledger_entry_id
                                 WHERE r.company_id=oi.company_id AND r.open_item_id=oi.id
                                   AND l.document_date <= $2::date), 0)
                    - COALESCE(returns.returned_amount, 0), 0) AS open_amount,
           COALESCE(oi.due_date, oi.document_date) AS effective_due
      FROM finance_invoice_open_items oi
      JOIN documents d ON d.company_id=oi.company_id AND d.id=oi.document_id
       AND d.document_type_code IN ('SALES_INVOICE','PURCHASE_INVOICE')
      LEFT JOIN LATERAL (
          SELECT COALESCE(SUM(attribution.amount),0) AS returned_amount
            FROM finance_invoice_return_attributions attribution
           WHERE attribution.company_id=oi.company_id
             AND attribution.document_id=oi.document_id
             AND attribution.return_document_date <= $2::date
      ) returns ON TRUE
     WHERE oi.company_id=$1 AND oi.document_date <= $2::date`
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
	query += `
), aged AS (
    SELECT party_id, side, currency, open_amount,
           ($2::date - effective_due) AS days_late
      FROM open_items
     WHERE open_amount > 0
)
SELECT a.party_id::text, COALESCE(p.code,''), COALESCE(p.display_name,''), a.side, a.currency,
       SUM(CASE WHEN a.days_late <= 0 THEN a.open_amount ELSE 0 END)::numeric(24,4)::text,
       SUM(CASE WHEN a.days_late BETWEEN 1 AND 30 THEN a.open_amount ELSE 0 END)::numeric(24,4)::text,
       SUM(CASE WHEN a.days_late BETWEEN 31 AND 60 THEN a.open_amount ELSE 0 END)::numeric(24,4)::text,
       SUM(CASE WHEN a.days_late BETWEEN 61 AND 90 THEN a.open_amount ELSE 0 END)::numeric(24,4)::text,
       SUM(CASE WHEN a.days_late > 90 THEN a.open_amount ELSE 0 END)::numeric(24,4)::text,
       SUM(CASE WHEN a.days_late > 0 THEN a.open_amount ELSE 0 END)::numeric(24,4)::text,
       SUM(a.open_amount)::numeric(24,4)::text
  FROM aged a
  JOIN parties p ON p.company_id=$1 AND p.id=a.party_id
 GROUP BY a.party_id, p.code, p.display_name, a.side, a.currency
 ORDER BY p.display_name, a.side, a.currency`

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return report, err
	}
	defer rows.Close()
	for rows.Next() {
		var row PartyAgingRow
		if err = rows.Scan(&row.PartyID, &row.PartyCode, &row.PartyName, &row.Side, &row.Currency,
			&row.NotDue, &row.Bucket0To30, &row.Bucket31To60, &row.Bucket61To90, &row.Bucket90Plus,
			&row.Overdue, &row.Total); err != nil {
			return report, err
		}
		report.Items = append(report.Items, row)
	}
	if err = rows.Err(); err != nil {
		return report, err
	}
	report.AsOf = asOf.Format("2006-01-02")
	return report, nil
}
