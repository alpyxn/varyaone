package finance

import (
	"context"
	"math/big"
	"strings"

	"github.com/jackc/pgx/v5"
)

// DocumentSettlement is a read model derived from immutable invoice,
// return, allocation and reversal rows. It is deliberately not persisted as a
// mutable balance field.
type DocumentSettlement struct {
	InvoiceTotal          string `json:"invoice_total"`
	ReturnedTotal         string `json:"returned_total"`
	EffectiveInvoiceTotal string `json:"effective_invoice_total"`
	CollectedTotal        string `json:"collected_total,omitempty"`
	PaidTotal             string `json:"paid_total,omitempty"`
	AmountDue             string `json:"amount_due,omitempty"`
	AmountPayable         string `json:"amount_payable,omitempty"`
	CustomerCredit        string `json:"customer_credit,omitempty"`
	SupplierCredit        string `json:"supplier_credit,omitempty"`
	PaymentStatus         string `json:"payment_status"`
	ReturnStatus          string `json:"return_status"`
	// Payments lists the collections/payments whose allocations still cover part
	// of this invoice (net of allocation reversals). It lets the UI point the
	// user at the exact records they must resolve before the invoice can be
	// cancelled. Empty when nothing is allocated.
	Payments []SettlementPayment `json:"payments,omitempty"`
}

// SettlementPayment is one collection/payment currently allocated to an invoice.
type SettlementPayment struct {
	ID              string `json:"id"`
	DocumentNo      string `json:"document_no"`
	PaymentKind     string `json:"payment_kind"` // COLLECTION | PAYMENT
	TransactionDate string `json:"transaction_date"`
	Currency        string `json:"currency"`
	AllocatedAmount string `json:"allocated_amount"`
}

// ReadDocumentSettlement reads the current settlement projection for a
// posted commercial invoice. companyID is supplied by the already-authorized
// commercial service; it is never accepted from a client payload.
func (s *Service) ReadDocumentSettlement(ctx context.Context, companyID, documentID string) (DocumentSettlement, error) {
	const query = `
WITH invoice AS (
    SELECT oi.id,
           oi.original_amount::text AS original_amount,
           oi.side,
           COALESCE((SELECT r.amount::text
                     FROM finance_invoice_open_item_reversals r
                     WHERE r.company_id=oi.company_id AND r.open_item_id=oi.id), '0') AS reversed_amount,
           COALESCE((SELECT SUM(CASE WHEN a.reversal_of_id IS NULL THEN a.amount ELSE -a.amount END)::text
                     FROM finance_payment_allocations a
                     WHERE a.company_id=oi.company_id AND a.open_item_id=oi.id), '0') AS allocated_amount
    FROM finance_invoice_open_items oi
    WHERE oi.company_id=$1 AND oi.document_id=$2
    ORDER BY oi.created_at DESC, oi.id DESC
    LIMIT 1
), related_sources AS (
    SELECT $2::uuid AS id
    UNION
    SELECT s.source_document_id
    FROM commercial_document_sources s
    WHERE s.company_id=$1 AND s.document_id=$2
), return_documents AS (
    SELECT DISTINCT relation.document_id
    FROM commercial_document_sources relation
    JOIN related_sources rs ON rs.id=relation.source_document_id
    JOIN documents d
      ON d.company_id=relation.company_id AND d.id=relation.document_id
    WHERE relation.company_id=$1
      AND relation.relation_type='RETURN'
      AND d.status='POSTED'
      AND d.document_type_code IN ('SALES_RETURN_INVOICE', 'PURCHASE_RETURN_INVOICE')
), returned AS (
    SELECT COALESCE(SUM(GREATEST(oi.original_amount - COALESCE(r.amount, 0), 0)), 0)::text AS amount
    FROM return_documents rd
    JOIN finance_invoice_open_items oi
      ON oi.company_id=$1 AND oi.document_id=rd.document_id
    LEFT JOIN finance_invoice_open_item_reversals r
      ON r.company_id=oi.company_id AND r.open_item_id=oi.id
)
SELECT COALESCE(i.original_amount, '0'),
       COALESCE(i.reversed_amount, '0'),
       COALESCE(returned.amount, '0'),
       COALESCE(i.allocated_amount, '0'),
       COALESCE(i.side, '')
FROM (SELECT 1) anchor
LEFT JOIN invoice i ON TRUE
CROSS JOIN returned`

	var invoiceAmount, reversedAmount, returnedAmount, allocatedAmount, side string
	if err := s.pool.QueryRow(ctx, query, companyID, documentID).Scan(&invoiceAmount, &reversedAmount, &returnedAmount, &allocatedAmount, &side); err != nil {
		if err == pgx.ErrNoRows {
			return DocumentSettlement{}, nil
		}
		return DocumentSettlement{}, err
	}
	settlement := deriveDocumentSettlement(invoiceAmount, reversedAmount, returnedAmount, allocatedAmount, side)
	if settlement.PaymentStatus != "UNPAID" {
		payments, err := s.settlementPayments(ctx, companyID, documentID)
		if err != nil {
			return DocumentSettlement{}, err
		}
		settlement.Payments = payments
	}
	return settlement, nil
}

// settlementPayments lists every collection/payment whose net (allocation minus
// allocation-reversal) still covers part of this invoice's open item.
func (s *Service) settlementPayments(ctx context.Context, companyID, documentID string) ([]SettlementPayment, error) {
	rows, err := s.pool.Query(ctx, `
SELECT p.id::text, COALESCE(p.document_no, ''), p.payment_kind,
       to_char(p.transaction_date, 'YYYY-MM-DD'), COALESCE(p.currency, ''),
       SUM(CASE WHEN a.reversal_of_id IS NULL THEN a.amount ELSE -a.amount END)::text
  FROM finance_invoice_open_items oi
  JOIN finance_payment_allocations a ON a.company_id = oi.company_id AND a.open_item_id = oi.id
  JOIN finance_payments p ON p.company_id = a.company_id AND p.id = a.payment_id
 WHERE oi.company_id = $1 AND oi.document_id = $2
 GROUP BY p.id, p.document_no, p.payment_kind, p.transaction_date, p.currency
HAVING SUM(CASE WHEN a.reversal_of_id IS NULL THEN a.amount ELSE -a.amount END) > 0
 ORDER BY p.transaction_date, p.id`, companyID, documentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	payments := make([]SettlementPayment, 0)
	for rows.Next() {
		var p SettlementPayment
		if err = rows.Scan(&p.ID, &p.DocumentNo, &p.PaymentKind, &p.TransactionDate, &p.Currency, &p.AllocatedAmount); err != nil {
			return nil, err
		}
		payments = append(payments, p)
	}
	return payments, rows.Err()
}

func deriveDocumentSettlement(invoiceAmount, reversedAmount, returnedAmount, allocatedAmount, side string) DocumentSettlement {
	original := settlementRat(invoiceAmount)
	reversed := settlementRat(reversedAmount)
	returned := settlementRat(returnedAmount)
	allocated := settlementRat(allocatedAmount)
	postedTotal := new(big.Rat).Sub(original, reversed)
	if postedTotal.Sign() < 0 {
		postedTotal.SetInt64(0)
	}
	effective := new(big.Rat).Sub(postedTotal, returned)
	if effective.Sign() < 0 {
		effective.SetInt64(0)
	}
	due := new(big.Rat).Sub(effective, allocated)
	if due.Sign() < 0 {
		due.SetInt64(0)
	}
	credit := new(big.Rat).Sub(allocated, effective)
	if credit.Sign() < 0 {
		credit.SetInt64(0)
	}

	paymentStatus := "UNPAID"
	// A return can reduce the amount that still needs collection/payment. A
	// payment status therefore becomes PAID when the effective amount is
	// covered, while a fully returned invoice with no payment remains UNPAID.
	if allocated.Cmp(postedTotal) >= 0 || (effective.Sign() > 0 && allocated.Cmp(effective) >= 0) {
		paymentStatus = "PAID"
	} else if allocated.Sign() > 0 {
		paymentStatus = "PARTIALLY_PAID"
	}
	returnStatus := "NONE"
	if returned.Sign() > 0 {
		if returned.Cmp(postedTotal) >= 0 && postedTotal.Sign() > 0 {
			returnStatus = "FULL"
		} else {
			returnStatus = "PARTIAL"
		}
	}

	result := DocumentSettlement{
		InvoiceTotal:          settlementAmount(original),
		ReturnedTotal:         settlementAmount(returned),
		EffectiveInvoiceTotal: settlementAmount(effective),
		PaymentStatus:         paymentStatus,
		ReturnStatus:          returnStatus,
	}
	if strings.EqualFold(strings.TrimSpace(side), "PAYABLE") {
		result.PaidTotal = settlementAmount(allocated)
		result.AmountPayable = settlementAmount(due)
		result.SupplierCredit = settlementAmount(credit)
	} else {
		result.CollectedTotal = settlementAmount(allocated)
		result.AmountDue = settlementAmount(due)
		result.CustomerCredit = settlementAmount(credit)
	}
	return result
}

func settlementRat(value string) *big.Rat {
	ratio, ok := new(big.Rat).SetString(strings.TrimSpace(value))
	if !ok {
		return new(big.Rat)
	}
	return ratio
}

func settlementAmount(value *big.Rat) string { return amountString(value, 4) }

// returnedAmountForDocumentTx is the command-side counterpart of the
// settlement projection. Return rows are append-only and may point at an
// invoice directly or through one of its source documents. DISTINCT keeps a
// multi-source relation from counting the same return twice.
func returnedAmountForDocumentTx(ctx context.Context, tx pgx.Tx, companyID, documentID string) (*big.Rat, error) {
	var amount string
	err := tx.QueryRow(ctx, `
WITH related_sources AS (
    SELECT $2::uuid AS id
    UNION
    SELECT source_document_id
    FROM commercial_document_sources
    WHERE company_id=$1 AND document_id=$2
), return_documents AS (
    SELECT DISTINCT relation.document_id
    FROM commercial_document_sources relation
    JOIN related_sources source ON source.id=relation.source_document_id
    JOIN documents d ON d.company_id=relation.company_id AND d.id=relation.document_id
    WHERE relation.company_id=$1
      AND relation.relation_type='RETURN'
      AND d.status='POSTED'
      AND d.document_type_code IN ('SALES_RETURN_INVOICE','PURCHASE_RETURN_INVOICE')
)
SELECT COALESCE(SUM(GREATEST(oi.original_amount-COALESCE(reversal.amount,0),0)),0)::text
FROM return_documents rd
JOIN finance_invoice_open_items oi ON oi.company_id=$1 AND oi.document_id=rd.document_id
LEFT JOIN finance_invoice_open_item_reversals reversal
  ON reversal.company_id=oi.company_id AND reversal.open_item_id=oi.id`, companyID, documentID).Scan(&amount)
	if err != nil {
		return nil, err
	}
	return settlementRat(amount), nil
}
