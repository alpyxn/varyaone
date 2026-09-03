package sales

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	LifecycleDraft     = "DRAFT"
	LifecycleOpen      = "OPEN"
	LifecycleFinalized = "FINALIZED"
	LifecycleCancelled = "CANCELLED"

	FulfillmentUnfulfilled = "UNFULFILLED"
	FulfillmentPartial     = "PARTIALLY_FULFILLED"
	FulfillmentFulfilled   = "FULFILLED"

	InvoicingUninvoiced = "UNINVOICED"
	InvoicingPartial    = "PARTIALLY_INVOICED"
	InvoicingInvoiced   = "INVOICED"

	PaymentUnpaid  = "UNPAID"
	PaymentPartial = "PARTIALLY_PAID"
	PaymentPaid    = "PAID"
)

func commercialLifecycleStatus(kind CommercialKind, status string) string {
	status = strings.ToUpper(strings.TrimSpace(status))
	if status == "DRAFT" {
		return LifecycleDraft
	}
	if status == "CANCELLED" {
		return LifecycleCancelled
	}
	if kind == SalesQuote {
		return status
	}
	if kind == SalesOrder {
		return LifecycleOpen
	}
	return LifecycleFinalized
}

func commercialFulfillmentStatus(status string) string {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "PARTIALLY_FULFILLED":
		return FulfillmentPartial
	case "FULFILLED":
		return FulfillmentFulfilled
	default:
		return FulfillmentUnfulfilled
	}
}

func commercialInvoicingStatus(lines []CommercialLine) string {
	return commercialInvoicingStatusForKind(lines, "")
}

func commercialInvoicingStatusForKind(lines []CommercialLine, kind CommercialKind) string {
	if len(lines) == 0 {
		return InvoicingUninvoiced
	}
	remaining := false
	invoiced := false
	for _, line := range lines {
		quantity := line.BaseQuantity
		if strings.TrimSpace(quantity) == "" {
			quantity = line.Quantity
		}
		remainingQuantity := line.RemainingInvoicingBaseQuantity
		if strings.TrimSpace(remainingQuantity) == "" {
			remainingQuantity = line.RemainingInvoicingQuantity
		}
		invoiceableQuantity := quantity
		if kind == SalesOrder && line.LineType == "PRODUCT" && decimalPositive(line.RemainingFulfillmentBaseQuantity) {
			invoiceableQuantity = decimalSubtractStatus(quantity, line.RemainingFulfillmentBaseQuantity)
		}
		if decimalPositive(remainingQuantity) {
			remaining = true
		}
		if decimalLess(remainingQuantity, invoiceableQuantity) {
			invoiced = true
		}
	}
	if !invoiced {
		return InvoicingUninvoiced
	}
	if !remaining {
		return InvoicingInvoiced
	}
	return InvoicingPartial
}

func decimalSubtractStatus(left, right string) string {
	a, aOK := new(big.Rat).SetString(strings.TrimSpace(left))
	b, bOK := new(big.Rat).SetString(strings.TrimSpace(right))
	if !aOK || !bOK {
		return "0"
	}
	return a.Sub(a, b).RatString()
}

func decimalPositive(value string) bool {
	ratio, ok := new(big.Rat).SetString(strings.TrimSpace(value))
	return ok && ratio.Sign() > 0
}

func decimalLess(left, right string) bool {
	a, aOK := new(big.Rat).SetString(strings.TrimSpace(left))
	b, bOK := new(big.Rat).SetString(strings.TrimSpace(right))
	return aOK && bOK && a.Cmp(b) < 0
}

func (s *Service) applyCommercialStatuses(ctx context.Context, item *CommercialDocument) error {
	if item == nil {
		return nil
	}
	item.LifecycleStatus = commercialLifecycleStatus(item.Kind, item.Status)
	if item.Kind == SalesOrder {
		item.FulfillmentStatus = commercialFulfillmentStatus(item.Status)
		item.InvoicingStatus = commercialInvoicingStatusForKind(item.Lines, item.Kind)
		var err error
		item.FulfillmentAt, err = s.commercialFulfillmentAt(ctx, item.CompanyID, item.Kind, item.ID, item.Status)
		if err != nil {
			return err
		}
	} else if item.Kind == SalesDispatch {
		item.InvoicingStatus = commercialInvoicingStatusForKind(item.Lines, item.Kind)
		var err error
		item.FulfillmentAt, err = s.commercialFulfillmentAt(ctx, item.CompanyID, item.Kind, item.ID, item.Status)
		if err != nil {
			return err
		}
	}
	if item.Kind == SalesInvoice && item.Status == "POSTED" {
		if s.finance != nil {
			settlement, err := s.finance.ReadDocumentSettlement(ctx, item.CompanyID, item.ID)
			if err != nil {
				return err
			}
			item.Settlement = &settlement
			item.PaymentStatus = settlement.PaymentStatus
		} else {
			status, err := s.commercialPaymentStatus(ctx, item.CompanyID, item.ID)
			if err != nil {
				return err
			}
			item.PaymentStatus = status
		}
	}
	return nil
}

// applyCommercialListStatuses fills the status axes for a list row. Unlike
// applyCommercialStatuses it never relies on item.Lines (list rows are scanned
// without their lines); invoicing status is resolved from the shared line
// registry/allocation projection so the displayed value matches the list
// filter predicate in commercialInvoicingPredicate.
func (s *Service) applyCommercialListStatuses(ctx context.Context, item *CommercialDocument) error {
	if item == nil {
		return nil
	}
	item.LifecycleStatus = commercialLifecycleStatus(item.Kind, item.Status)
	switch item.Kind {
	case SalesOrder:
		item.FulfillmentStatus = commercialFulfillmentStatus(item.Status)
		var err error
		if item.InvoicingStatus, err = s.commercialInvoicingStatusSQL(ctx, item.CompanyID, item.Kind, item.ID); err != nil {
			return err
		}
		if item.FulfillmentAt, err = s.commercialFulfillmentAt(ctx, item.CompanyID, item.Kind, item.ID, item.Status); err != nil {
			return err
		}
	case SalesDispatch:
		var err error
		if item.InvoicingStatus, err = s.commercialInvoicingStatusSQL(ctx, item.CompanyID, item.Kind, item.ID); err != nil {
			return err
		}
		if item.FulfillmentAt, err = s.commercialFulfillmentAt(ctx, item.CompanyID, item.Kind, item.ID, item.Status); err != nil {
			return err
		}
	case SalesInvoice:
		if item.Status == "POSTED" {
			if s.finance != nil {
				settlement, err := s.finance.ReadDocumentSettlement(ctx, item.CompanyID, item.ID)
				if err != nil {
					return err
				}
				item.PaymentStatus = settlement.PaymentStatus
			} else {
				status, err := s.commercialPaymentStatus(ctx, item.CompanyID, item.ID)
				if err != nil {
					return err
				}
				item.PaymentStatus = status
			}
		}
	}
	return nil
}

// commercialInvoicingStatusSQL derives the invoicing axis for an order or
// dispatch from the shared line registry and INVOICING allocations, matching
// the CASE expression used by commercialInvoicingPredicate.
func (s *Service) commercialInvoicingStatusSQL(ctx context.Context, companyID string, kind CommercialKind, documentID string) (string, error) {
	var status string
	err := s.pool.QueryRow(ctx, `WITH totals AS (
SELECT COALESCE(SUM(r.base_quantity),0) AS total,
COALESCE((SELECT SUM(a.base_quantity) FROM commercial_line_allocations a JOIN commercial_line_registry r2 ON r2.company_id=a.company_id AND r2.line_id=a.source_line_id WHERE a.company_id=$1 AND r2.document_id=$2 AND r2.aggregate_type=$3 AND a.allocation_type='INVOICING'),0) AS invoiced
FROM commercial_line_registry r WHERE r.company_id=$1 AND r.document_id=$2 AND r.aggregate_type=$3)
SELECT CASE WHEN total<=0 OR invoiced<=0 THEN 'UNINVOICED' WHEN invoiced>=total THEN 'INVOICED' ELSE 'PARTIALLY_INVOICED' END FROM totals`, companyID, documentID, string(kind)).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return InvoicingUninvoiced, nil
	}
	return status, err
}

func (s *Service) commercialFulfillmentAt(ctx context.Context, companyID string, kind CommercialKind, documentID, status string) (*time.Time, error) {
	var at *time.Time
	var err error
	switch kind {
	case SalesOrder:
		err = s.pool.QueryRow(ctx, `SELECT MAX(d.posted_at) FROM sales_dispatches d JOIN commercial_document_sources src ON src.company_id=d.company_id AND src.document_id=d.id AND src.source_document_id=$2 WHERE d.company_id=$1 AND d.status='POSTED'`, companyID, documentID).Scan(&at)
	case SalesDispatch:
		if strings.EqualFold(strings.TrimSpace(status), "POSTED") {
			err = s.pool.QueryRow(ctx, `SELECT posted_at FROM sales_dispatches WHERE company_id=$1 AND id=$2`, companyID, documentID).Scan(&at)
		}
	}
	return at, err
}

func (s *Service) commercialPaymentStatus(ctx context.Context, companyID, documentID string) (string, error) {
	var status string
	err := s.pool.QueryRow(ctx, `SELECT CASE WHEN oi.id IS NULL THEN 'UNPAID' WHEN oi.original_amount-COALESCE((SELECT amount FROM finance_invoice_open_item_reversals r WHERE r.company_id=oi.company_id AND r.open_item_id=oi.id),0)-COALESCE((SELECT SUM(CASE WHEN a.reversal_of_id IS NULL THEN a.amount ELSE -a.amount END) FROM finance_payment_allocations a WHERE a.company_id=oi.company_id AND a.open_item_id=oi.id),0) <= 0 THEN 'PAID' WHEN COALESCE((SELECT SUM(CASE WHEN a.reversal_of_id IS NULL THEN a.amount ELSE -a.amount END) FROM finance_payment_allocations a WHERE a.company_id=oi.company_id AND a.open_item_id=oi.id),0) > 0 THEN 'PARTIALLY_PAID' ELSE 'UNPAID' END FROM (SELECT id,company_id,original_amount FROM finance_invoice_open_items WHERE company_id=$1 AND document_id=$2 ORDER BY created_at DESC LIMIT 1) oi`, companyID, documentID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return PaymentUnpaid, nil
	}
	return status, err
}

func commercialLifecyclePredicate(kind CommercialKind, requested string) (string, bool) {
	requested = strings.ToUpper(strings.TrimSpace(requested))
	if requested == LifecycleDraft {
		return "t.status='DRAFT'", true
	}
	if requested == LifecycleCancelled {
		return "t.status='CANCELLED'", true
	}
	if kind == SalesQuote {
		switch requested {
		case "SENT", "ACCEPTED", "REJECTED", "EXPIRED":
			return "t.status=" + quoteStatusLiteral(requested), true
		}
		return "", false
	}
	if requested == LifecycleOpen {
		if kind == SalesOrder {
			return "t.status IN ('CONFIRMED','PARTIALLY_FULFILLED','FULFILLED')", true
		}
		return "1=0", true
	}
	if requested == LifecycleFinalized {
		if kind == SalesOrder {
			return "1=0", true
		}
		return "t.status='POSTED'", true
	}
	return "", false
}

func quoteStatusLiteral(value string) string {
	// Values reach this helper only after the explicit allow-list above.
	return "'" + value + "'"
}

func commercialFulfillmentPredicate(kind CommercialKind, requested string) (string, bool) {
	if kind != SalesOrder {
		return "", false
	}
	switch strings.ToUpper(strings.TrimSpace(requested)) {
	case FulfillmentUnfulfilled:
		return "t.status IN ('DRAFT','CONFIRMED')", true
	case FulfillmentPartial:
		return "t.status='PARTIALLY_FULFILLED'", true
	case FulfillmentFulfilled:
		return "t.status='FULFILLED'", true
	default:
		return "", false
	}
}

func commercialInvoicingPredicate(kind CommercialKind, requested string) (string, bool) {
	if kind != SalesOrder && kind != SalesDispatch {
		return "", false
	}
	requested = strings.ToUpper(strings.TrimSpace(requested))
	if requested != InvoicingUninvoiced && requested != InvoicingPartial && requested != InvoicingInvoiced {
		return "", false
	}
	aggregateType := string(kind)
	total := fmt.Sprintf("COALESCE((SELECT SUM(r.base_quantity) FROM commercial_line_registry r WHERE r.company_id=t.company_id AND r.document_id=t.id AND r.aggregate_type='%s'),0)", aggregateType)
	invoiced := fmt.Sprintf("COALESCE((SELECT SUM(a.base_quantity) FROM commercial_line_allocations a JOIN commercial_line_registry r ON r.company_id=a.company_id AND r.line_id=a.source_line_id WHERE a.company_id=t.company_id AND r.document_id=t.id AND r.aggregate_type='%s' AND a.allocation_type='INVOICING'),0)", aggregateType)
	caseExpression := fmt.Sprintf("CASE WHEN %s<=0 OR %s<=0 THEN 'UNINVOICED' WHEN %s>=%s THEN 'INVOICED' ELSE 'PARTIALLY_INVOICED' END", total, invoiced, invoiced, total)
	return fmt.Sprintf("(%s)='%s'", caseExpression, requested), true
}

func commercialPaymentPredicate(requested string) (string, bool) {
	requested = strings.ToUpper(strings.TrimSpace(requested))
	if requested != PaymentUnpaid && requested != PaymentPartial && requested != PaymentPaid {
		return "", false
	}
	// Same source of truth as finance's settlement/open-item projections, so a
	// grid filtered by "Ödendi" can never disagree with the invoice card.
	returnedAmount := `COALESCE((SELECT SUM(ra.amount) FROM finance_invoice_return_attributions ra WHERE ra.company_id=t.company_id AND ra.document_id=t.id),0)`
	allocatedAmount := `COALESCE((SELECT SUM(CASE WHEN a.reversal_of_id IS NULL THEN a.amount ELSE -a.amount END) FROM finance_payment_allocations a WHERE a.company_id=oi.company_id AND a.open_item_id=oi.id),0)`
	postedAmount := `oi.original_amount-COALESCE((SELECT amount FROM finance_invoice_open_item_reversals r WHERE r.company_id=oi.company_id AND r.open_item_id=oi.id),0)`
	effectiveAmount := fmt.Sprintf(`(%s-%s)`, postedAmount, returnedAmount)
	statusExpression := fmt.Sprintf(`CASE WHEN %s>=%s OR (%s>0 AND %s>=%s) THEN 'PAID' WHEN %s>0 THEN 'PARTIALLY_PAID' ELSE 'UNPAID' END`, allocatedAmount, postedAmount, effectiveAmount, allocatedAmount, effectiveAmount, allocatedAmount)
	return fmt.Sprintf("t.status='POSTED' AND COALESCE((SELECT %s FROM finance_invoice_open_items oi WHERE oi.company_id=t.company_id AND oi.document_id=t.id ORDER BY oi.created_at DESC LIMIT 1),'UNPAID')='%s'", statusExpression, requested), true
}
