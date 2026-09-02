package purchasing

import (
	"context"
	"errors"
	"math/big"
	"strings"

	"github.com/alpyxn/varyaone/internal/identity"
)

// purchaseCanDraft reports whether the session can prepare/edit a draft of the
// given document kind (a ".draft" preparer or a ".post" holder).
func purchaseCanDraft(session identity.Session, kind PurchaseKind) bool {
	if session.HasPermission("commercial.document.manage") {
		return true
	}
	switch kind {
	case PurchaseOrderKind:
		return session.HasPermission("purchase.order.manage")
	case GoodsReceiptKind:
		return session.HasPermission("purchase.receipt.draft") || session.HasPermission("purchase.receipt.post")
	case PurchaseInvoiceKind:
		return session.HasPermission("purchase.invoice.draft") || session.HasPermission("purchase.invoice.post")
	case PurchaseReturnKind:
		return session.HasPermission("purchase.return.draft") || session.HasPermission("purchase.return.post")
	}
	return false
}

// purchaseCanPost reports whether the session can finalize/cancel the given kind.
func purchaseCanPost(session identity.Session, kind PurchaseKind) bool {
	if session.HasPermission("commercial.document.manage") {
		return true
	}
	switch kind {
	case PurchaseOrderKind:
		return session.HasPermission("purchase.order.manage")
	case GoodsReceiptKind:
		return session.HasPermission("purchase.receipt.post")
	case PurchaseInvoiceKind:
		return session.HasPermission("purchase.invoice.post")
	case PurchaseReturnKind:
		return session.HasPermission("purchase.return.post")
	}
	return false
}

func (s *Service) applyPurchaseActions(ctx context.Context, session identity.Session, itemKind PurchaseKind, status string, item any) error {
	canDraft := purchaseCanDraft(session, itemKind)
	canPost := purchaseCanPost(session, itemKind)
	actions := PurchaseActionAvailability{
		CanEdit:            canDraft && status == "DRAFT",
		CanDelete:          canDraft && status == "DRAFT",
		CanPost:            canPost && status == "DRAFT",
		CanCancel:          canPost && (status == "POSTED" || (itemKind == PurchaseOrderKind && (status == "CONFIRMED" || status == "PARTIALLY_FULFILLED"))),
		CanCreateEDocument: false, // actual provider transport is intentionally outside this release
	}
	switch value := item.(type) {
	case *PurchaseOrder:
		actions.CanCreateDispatch = purchaseCanDraft(session, GoodsReceiptKind) && status != "DRAFT" && status != "CANCELLED" && purchaseOrderHasRemaining(value.Lines, true)
		actions.CanCreateInvoice = purchaseCanDraft(session, PurchaseInvoiceKind) && status != "DRAFT" && status != "CANCELLED" && purchaseOrderHasRemaining(value.Lines, false)
		value.AvailableActions = actions
	case *GoodsReceipt:
		actions.CanCreateInvoice = purchaseCanDraft(session, PurchaseInvoiceKind) && status == "POSTED" && purchaseReceiptHasRemaining(value.Lines)
		value.AvailableActions = actions
	case *PurchaseInvoice:
		if status == "POSTED" {
			if settlement, available, err := s.purchaseSettlement(ctx, value.CompanyID, value.ID); err != nil {
				return err
			} else if available {
				actions.CanPay = session.HasPermission("finance.payment.post") && settlement.ReturnStatus != "FULL" && purchasingAmountPositive(settlement.AmountPayable)
				// A paid (fully or partially) invoice cannot be cancelled until
				// its allocated payments are reversed (finance enforces the same).
				if settlement.PaymentStatus == "PAID" || settlement.PaymentStatus == "PARTIALLY_PAID" {
					actions.CanCancel = false
				}
			}
			// Returns remain source-based in this release. A direct purchase
			// invoice without a receipt source must not advertise an action the
			// backend cannot complete.
			actions.CanCreateReturn = purchaseCanDraft(session, PurchaseReturnKind) && purchaseInvoiceHasReturnSource(value)
		}
		value.AvailableActions = actions
	case *PurchaseReturn:
		value.AvailableActions = actions
	}
	return nil
}

func purchaseInvoiceHasReturnSource(invoice *PurchaseInvoice) bool {
	if invoice == nil {
		return false
	}
	return (invoice.GoodsReceiptID != nil && strings.TrimSpace(*invoice.GoodsReceiptID) != "") || len(invoice.GoodsReceiptIDs) > 0
}

func purchasingAmountPositive(value string) bool {
	ratio, ok := new(big.Rat).SetString(strings.TrimSpace(value))
	return ok && ratio.Sign() > 0
}

func purchaseOrderHasRemaining(lines []PurchaseOrderLine, receiving bool) bool {
	for _, line := range lines {
		if normalizePurchaseLineType(line.LineType) == "SERVICE" && receiving {
			continue
		}
		used := line.InvoicedQuantity
		if receiving {
			used = line.ReceivedQuantity
		}
		if decimalPositivePurchase(subtractPurchaseDecimal(line.OrderedQuantity, used)) {
			return true
		}
	}
	return false
}

func purchaseReceiptHasRemaining(lines []GoodsReceiptLine) bool {
	for _, line := range lines {
		if decimalPositivePurchase(line.RemainingInvoicingBaseQuantity) || decimalPositivePurchase(line.RemainingInvoicingQuantity) {
			return true
		}
	}
	return false
}

func subtractPurchaseDecimal(left, right string) string {
	leftValue, leftOK := new(big.Rat).SetString(strings.TrimSpace(left))
	rightValue, rightOK := new(big.Rat).SetString(strings.TrimSpace(right))
	if !leftOK || !rightOK {
		return "0"
	}
	return leftValue.Sub(leftValue, rightValue).RatString()
}

func (s *Service) loadPurchaseRelatedDocuments(ctx context.Context, q purchaseSourceQuery, session identity.Session, documentID string) ([]SourceDocumentReference, error) {
	rows, err := q.Query(ctx, `
		SELECT rel.document_id,d.document_no,d.document_type_code,d.branch_id,d.warehouse_id,d.status
		  FROM commercial_document_sources rel
		  JOIN documents d ON d.company_id=rel.company_id AND d.id=rel.document_id
		 WHERE rel.company_id=$1 AND rel.source_document_id=$2
		 ORDER BY rel.created_at,rel.document_id,rel.relation_type`, session.CurrentCompanyID, documentID)
	if err != nil {
		return nil, err
	}
	// Buffer every row before running any dependent query. The request is pinned
	// to a single database connection, so issuing a sub-query while these rows are
	// still open fails with "conn busy".
	type relatedRow struct {
		ref         SourceDocumentReference
		branchID    string
		warehouseID *string
		state       string
	}
	var buffered []relatedRow
	for rows.Next() {
		var rowData relatedRow
		if err = rows.Scan(&rowData.ref.ID, &rowData.ref.DocumentNo, &rowData.ref.DocumentTypeCode, &rowData.branchID, &rowData.warehouseID, &rowData.state); err != nil {
			rows.Close()
			return nil, err
		}
		buffered = append(buffered, rowData)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	result := []SourceDocumentReference{}
	for _, rowData := range buffered {
		ref := rowData.ref
		branchID := rowData.branchID
		warehouseID := rowData.warehouseID
		status := rowData.state
		if err = s.ensurePurchaseSourceScope(ctx, q, session, ref.DocumentTypeCode, ref.ID, branchID, warehouseID); err != nil {
			if errors.Is(err, identity.ErrForbidden) {
				continue
			}
			return nil, err
		}
		ref.Direction = "TARGET"
		ref.RelationType = "RELATED"
		ref.Status = strings.ToUpper(strings.TrimSpace(status))
		if kind, ok := purchaseKindForDocumentType(ref.DocumentTypeCode); ok {
			ref.Kind = purchaseSourceKind(kind)
			ref.LifecycleStatus = purchaseLifecycleStatus(kind, ref.Status)
		} else {
			ref.LifecycleStatus = ref.Status
		}
		result = append(result, ref)
	}
	return result, nil
}
