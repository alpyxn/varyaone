package purchasing

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/finance"
	"github.com/jackc/pgx/v5"
)

const (
	PurchaseLifecycleDraft     = "DRAFT"
	PurchaseLifecycleOpen      = "OPEN"
	PurchaseLifecycleFinalized = "FINALIZED"
	PurchaseLifecycleCancelled = "CANCELLED"

	PurchaseFulfillmentUnfulfilled = "UNFULFILLED"
	PurchaseFulfillmentPartial     = "PARTIALLY_FULFILLED"
	PurchaseFulfillmentFulfilled   = "FULFILLED"

	PurchaseInvoicingUninvoiced = "UNINVOICED"
	PurchaseInvoicingPartial    = "PARTIALLY_INVOICED"
	PurchaseInvoicingInvoiced   = "INVOICED"

	PurchasePaymentUnpaid  = "UNPAID"
	PurchasePaymentPartial = "PARTIALLY_PAID"
	PurchasePaymentPaid    = "PAID"
)

func purchaseLifecycleStatus(kind PurchaseKind, status string) string {
	status = strings.ToUpper(strings.TrimSpace(status))
	if status == "DRAFT" {
		return PurchaseLifecycleDraft
	}
	if status == "CANCELLED" {
		return PurchaseLifecycleCancelled
	}
	if kind == PurchaseOrderKind {
		return PurchaseLifecycleOpen
	}
	return PurchaseLifecycleFinalized
}

func purchaseFulfillmentStatus(lines []PurchaseOrderLine, status string) string {
	if len(lines) == 0 {
		switch status {
		case "PARTIALLY_FULFILLED":
			return PurchaseFulfillmentPartial
		case "FULFILLED":
			return PurchaseFulfillmentFulfilled
		default:
			return PurchaseFulfillmentUnfulfilled
		}
	}
	full, partial := 0, 0
	for _, line := range lines {
		fulfilled := line.ReceivedQuantity
		if normalizePurchaseLineType(line.LineType) == "SERVICE" {
			fulfilled = line.InvoicedQuantity
		}
		if decimalAtLeastPurchase(fulfilled, line.OrderedQuantity) {
			full++
		}
		if decimalPositivePurchase(fulfilled) {
			partial++
		}
	}
	if full == len(lines) {
		return PurchaseFulfillmentFulfilled
	}
	if partial > 0 {
		return PurchaseFulfillmentPartial
	}
	return PurchaseFulfillmentUnfulfilled
}

func purchaseInvoicingStatus(lines []PurchaseOrderLine) string {
	if len(lines) == 0 {
		return PurchaseInvoicingUninvoiced
	}
	full, partial := 0, 0
	for _, line := range lines {
		if decimalAtLeastPurchase(line.InvoicedQuantity, line.OrderedQuantity) {
			full++
		}
		if decimalPositivePurchase(line.InvoicedQuantity) {
			partial++
		}
	}
	if full == len(lines) {
		return PurchaseInvoicingInvoiced
	}
	if partial > 0 {
		return PurchaseInvoicingPartial
	}
	return PurchaseInvoicingUninvoiced
}

func decimalPositivePurchase(value string) bool {
	ratio, ok := new(big.Rat).SetString(strings.TrimSpace(value))
	return ok && ratio.Sign() > 0
}

func decimalAtLeastPurchase(left, right string) bool {
	a, aOK := new(big.Rat).SetString(strings.TrimSpace(left))
	b, bOK := new(big.Rat).SetString(strings.TrimSpace(right))
	return aOK && bOK && a.Cmp(b) >= 0
}

func (s *Service) applyPurchaseStatuses(ctx context.Context, itemKind PurchaseKind, status string, item any) error {
	switch value := item.(type) {
	case *PurchaseOrder:
		value.LifecycleStatus = purchaseLifecycleStatus(itemKind, status)
		value.FulfillmentStatus = purchaseFulfillmentStatus(value.Lines, status)
		value.InvoicingStatus = purchaseInvoicingStatus(value.Lines)
		var err error
		value.FulfillmentAt, err = s.purchaseFulfillmentAt(ctx, value.CompanyID, itemKind, value.ID, status)
		if err != nil {
			return err
		}
	case *GoodsReceipt:
		value.LifecycleStatus = purchaseLifecycleStatus(itemKind, status)
		value.InvoicingStatus, _ = s.purchaseReceiptInvoicingStatus(ctx, value.CompanyID, value.ID)
		var err error
		value.FulfillmentAt, err = s.purchaseFulfillmentAt(ctx, value.CompanyID, itemKind, value.ID, status)
		if err != nil {
			return err
		}
	case *PurchaseInvoice:
		value.LifecycleStatus = purchaseLifecycleStatus(itemKind, status)
		if status == "POSTED" {
			if settlement, available, err := s.purchaseSettlement(ctx, value.CompanyID, value.ID); err != nil {
				return err
			} else if available {
				value.Settlement = &settlement
				value.PaymentStatus = settlement.PaymentStatus
			} else {
				var err error
				value.PaymentStatus, err = s.purchasePaymentStatus(ctx, value.CompanyID, value.ID)
				if err != nil {
					return err
				}
			}
		}
	case *PurchaseReturn:
		value.LifecycleStatus = purchaseLifecycleStatus(itemKind, status)
	}
	return nil
}

// purchaseSettlement is intentionally an optional read-side extension. The
// purchasing write boundary only needs FinancePoster; the composition root
// supplies the finance reader in production so settlement remains a single
// deterministic projection shared by sales and purchasing.
func (s *Service) purchaseSettlement(ctx context.Context, companyID, documentID string) (finance.DocumentSettlement, bool, error) {
	reader, ok := s.financePost.(interface {
		ReadDocumentSettlement(context.Context, string, string) (finance.DocumentSettlement, error)
	})
	if !ok || reader == nil {
		return finance.DocumentSettlement{}, false, nil
	}
	settlement, err := reader.ReadDocumentSettlement(ctx, companyID, documentID)
	return settlement, true, err
}

func (s *Service) applyPurchaseListStatuses(ctx context.Context, item *PurchaseListItem) error {
	if item == nil {
		return nil
	}
	item.LifecycleStatus = purchaseLifecycleStatus(item.Kind, item.Status)
	switch item.Kind {
	case PurchaseOrderKind:
		item.FulfillmentStatus = purchaseFulfillmentStatus(nil, item.Status)
		var err error
		if item.InvoicingStatus, err = s.purchaseOrderInvoicingStatus(ctx, item.CompanyID, item.ID); err != nil {
			return err
		}
		item.FulfillmentAt, err = s.purchaseFulfillmentAt(ctx, item.CompanyID, item.Kind, item.ID, item.Status)
		if err != nil {
			return err
		}
	case GoodsReceiptKind:
		var err error
		item.InvoicingStatus, err = s.purchaseReceiptInvoicingStatus(ctx, item.CompanyID, item.ID)
		if err != nil {
			return err
		}
		item.FulfillmentAt, err = s.purchaseFulfillmentAt(ctx, item.CompanyID, item.Kind, item.ID, item.Status)
		if err != nil {
			return err
		}
	case PurchaseInvoiceKind:
		if item.Status == "POSTED" {
			var err error
			item.PaymentStatus, err = s.purchasePaymentStatus(ctx, item.CompanyID, item.ID)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) purchaseFulfillmentAt(ctx context.Context, companyID string, kind PurchaseKind, documentID, status string) (*time.Time, error) {
	var at *time.Time
	var err error
	switch kind {
	case PurchaseOrderKind:
		err = s.pool.QueryRow(ctx, `SELECT MAX(g.posted_at) FROM goods_receipts g WHERE g.company_id=$1 AND g.purchase_order_id=$2 AND g.status='POSTED'`, companyID, documentID).Scan(&at)
	case GoodsReceiptKind:
		if strings.EqualFold(strings.TrimSpace(status), "POSTED") {
			err = s.pool.QueryRow(ctx, `SELECT posted_at FROM goods_receipts WHERE company_id=$1 AND id=$2`, companyID, documentID).Scan(&at)
		}
	}
	return at, err
}

// purchaseOrderInvoicingStatus derives the invoicing axis for an order list row
// from purchase_order_lines, matching purchaseInvoicingStatus which the detail
// path computes from the loaded lines.
func (s *Service) purchaseOrderInvoicingStatus(ctx context.Context, companyID, orderID string) (string, error) {
	var total, full, partial int
	err := s.pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE invoiced_quantity>=ordered_quantity),count(*) FILTER (WHERE invoiced_quantity>0) FROM purchase_order_lines WHERE company_id=$1 AND order_id=$2`, companyID, orderID).Scan(&total, &full, &partial)
	if errors.Is(err, pgx.ErrNoRows) || total == 0 || partial == 0 {
		return PurchaseInvoicingUninvoiced, nil
	}
	if err != nil {
		return "", err
	}
	if full == total {
		return PurchaseInvoicingInvoiced, nil
	}
	return PurchaseInvoicingPartial, nil
}

func (s *Service) purchaseReceiptInvoicingStatus(ctx context.Context, companyID, documentID string) (string, error) {
	var status string
	err := s.pool.QueryRow(ctx, `WITH totals AS (SELECT COALESCE(SUM(r.base_quantity),0) AS total,COALESCE(SUM((SELECT SUM(a.base_quantity) FROM commercial_line_allocations a WHERE a.company_id=r.company_id AND a.source_line_id=r.line_id AND a.allocation_type='INVOICING')),0) AS invoiced FROM commercial_line_registry r WHERE r.company_id=$1 AND r.document_id=$2 AND r.aggregate_type='GOODS_RECEIPT') SELECT CASE WHEN total <= 0 OR invoiced <= 0 THEN 'UNINVOICED' WHEN invoiced >= total THEN 'INVOICED' ELSE 'PARTIALLY_INVOICED' END FROM totals`, companyID, documentID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return PurchaseInvoicingUninvoiced, nil
	}
	return status, err
}

func (s *Service) purchasePaymentStatus(ctx context.Context, companyID, documentID string) (string, error) {
	var status string
	err := s.pool.QueryRow(ctx, `SELECT CASE WHEN oi.id IS NULL THEN 'UNPAID' WHEN oi.original_amount-COALESCE((SELECT amount FROM finance_invoice_open_item_reversals r WHERE r.company_id=oi.company_id AND r.open_item_id=oi.id),0)-COALESCE((SELECT SUM(CASE WHEN a.reversal_of_id IS NULL THEN a.amount ELSE -a.amount END) FROM finance_payment_allocations a WHERE a.company_id=oi.company_id AND a.open_item_id=oi.id),0) <= 0 THEN 'PAID' WHEN COALESCE((SELECT SUM(CASE WHEN a.reversal_of_id IS NULL THEN a.amount ELSE -a.amount END) FROM finance_payment_allocations a WHERE a.company_id=oi.company_id AND a.open_item_id=oi.id),0) > 0 THEN 'PARTIALLY_PAID' ELSE 'UNPAID' END FROM (SELECT id,company_id,original_amount FROM finance_invoice_open_items WHERE company_id=$1 AND document_id=$2 ORDER BY created_at DESC LIMIT 1) oi`, companyID, documentID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return PurchasePaymentUnpaid, nil
	}
	return status, err
}

func purchaseLifecyclePredicate(kind PurchaseKind, status string) (string, bool) {
	status = strings.ToUpper(strings.TrimSpace(status))
	switch status {
	case PurchaseLifecycleDraft:
		return "t.status='DRAFT'", true
	case PurchaseLifecycleCancelled:
		return "t.status='CANCELLED'", true
	case PurchaseLifecycleOpen:
		if kind == PurchaseOrderKind {
			return "t.status IN ('CONFIRMED','PARTIALLY_FULFILLED','FULFILLED')", true
		}
		return "1=0", true
	case PurchaseLifecycleFinalized:
		if kind == PurchaseOrderKind {
			return "1=0", true
		}
		return "t.status='POSTED'", true
	default:
		return "", false
	}
}

func purchaseFulfillmentPredicate(kind PurchaseKind, status string) (string, bool) {
	if kind != PurchaseOrderKind {
		return "", false
	}
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case PurchaseFulfillmentUnfulfilled:
		return "t.status IN ('DRAFT','CONFIRMED')", true
	case PurchaseFulfillmentPartial:
		return "t.status='PARTIALLY_FULFILLED'", true
	case PurchaseFulfillmentFulfilled:
		return "t.status='FULFILLED'", true
	default:
		return "", false
	}
}

func purchaseInvoicingPredicate(kind PurchaseKind, status string) (string, bool) {
	if kind != PurchaseOrderKind && kind != GoodsReceiptKind {
		return "", false
	}
	status = strings.ToUpper(strings.TrimSpace(status))
	if status != PurchaseInvoicingUninvoiced && status != PurchaseInvoicingPartial && status != PurchaseInvoicingInvoiced {
		return "", false
	}
	aggregateType := string(kind)
	total := fmt.Sprintf("COALESCE((SELECT SUM(r.base_quantity) FROM commercial_line_registry r WHERE r.company_id=t.company_id AND r.document_id=t.id AND r.aggregate_type='%s'),0)", aggregateType)
	invoiced := fmt.Sprintf("COALESCE((SELECT SUM(a.base_quantity) FROM commercial_line_allocations a JOIN commercial_line_registry r ON r.company_id=a.company_id AND r.line_id=a.source_line_id WHERE a.company_id=t.company_id AND r.document_id=t.id AND r.aggregate_type='%s' AND a.allocation_type='INVOICING'),0)", aggregateType)
	caseExpression := fmt.Sprintf("CASE WHEN %s<=0 OR %s<=0 THEN 'UNINVOICED' WHEN %s>=%s THEN 'INVOICED' ELSE 'PARTIALLY_INVOICED' END", total, invoiced, invoiced, total)
	return fmt.Sprintf("(%s)='%s'", caseExpression, status), true
}

func purchasePaymentPredicate(status string) (string, bool) {
	status = strings.ToUpper(strings.TrimSpace(status))
	if status != PurchasePaymentUnpaid && status != PurchasePaymentPartial && status != PurchasePaymentPaid {
		return "", false
	}
	// Same source of truth as finance's settlement/open-item projections, so a
	// grid filtered by "Ödendi" can never disagree with the invoice card.
	returnedAmount := `COALESCE((SELECT SUM(ra.amount) FROM finance_invoice_return_attributions ra WHERE ra.company_id=t.company_id AND ra.document_id=t.id),0)`
	allocatedAmount := `COALESCE((SELECT SUM(CASE WHEN a.reversal_of_id IS NULL THEN a.amount ELSE -a.amount END) FROM finance_payment_allocations a WHERE a.company_id=oi.company_id AND a.open_item_id=oi.id),0)`
	postedAmount := `oi.original_amount-COALESCE((SELECT amount FROM finance_invoice_open_item_reversals r WHERE r.company_id=oi.company_id AND r.open_item_id=oi.id),0)`
	effectiveAmount := fmt.Sprintf(`(%s-%s)`, postedAmount, returnedAmount)
	expression := fmt.Sprintf(`CASE WHEN %s>=%s OR (%s>0 AND %s>=%s) THEN 'PAID' WHEN %s>0 THEN 'PARTIALLY_PAID' ELSE 'UNPAID' END`, allocatedAmount, postedAmount, effectiveAmount, allocatedAmount, effectiveAmount, allocatedAmount)
	return fmt.Sprintf("t.status='POSTED' AND COALESCE((SELECT %s FROM finance_invoice_open_items oi WHERE oi.company_id=t.company_id AND oi.document_id=t.id ORDER BY oi.created_at DESC LIMIT 1),'UNPAID')='%s'", expression, status), true
}
