package sales

import (
	"context"
	"errors"
	"math/big"
	"strings"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/jackc/pgx/v5"
)

func (s *Service) applyCommercialActions(ctx context.Context, session identity.Session, item *CommercialDocument) error {
	if item == nil {
		return nil
	}
	spec, ok := commercialSpecFor(item.Kind)
	if !ok {
		return nil
	}
	// canDraft: prepare/edit/delete a draft (a ".draft" preparer or a poster).
	// canPost: finalize or cancel (a poster only).
	canDraft := session.HasPermission(spec.managePerm) || session.HasPermission(spec.draftPerm) || session.HasPermission("commercial.document.manage") || session.HasPermission("document.create")
	canPost := session.HasPermission(spec.postPerm) || session.HasPermission("commercial.document.manage") || session.HasPermission("document.post")
	item.AvailableActions = CommercialActionAvailability{
		CanEdit:   canDraft && item.Status == "DRAFT",
		CanDelete: canDraft && item.Status == "DRAFT",
		// Quotes use the primary post action slot for both DRAFT -> SENT
		// (send) and SENT -> ACCEPTED (accept). Keeping SENT out of this
		// server-authoritative matrix leaves quotes permanently stuck in SENT,
		// which also makes them impossible to select as order sources.
		CanPost:            canPost && commercialPrimaryTransitionAvailable(item.Kind, item.Status),
		CanCancel:          canPost && (item.Status == "POSTED" || (item.Kind == SalesOrder && (item.Status == "CONFIRMED" || item.Status == "PARTIALLY_FULFILLED")) || (item.Kind == SalesQuote && (item.Status == "SENT" || item.Status == "ACCEPTED"))),
		CanCreateEDocument: false, // provider-neutral e-document workflow is not a commercial command in this release
	}
	// A dispatch/invoice a finalized downstream document still depends on cannot
	// be cancelled directly (mirrors assertNoActiveDownstreamTx). Surface that in
	// the action matrix so the UI disables the button before the command fails.
	if item.AvailableActions.CanCancel && item.Status == "POSTED" && (item.Kind == SalesDispatch || item.Kind == SalesInvoice) {
		var hasDownstream bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM commercial_document_sources s
			JOIN documents d ON d.company_id=s.company_id AND d.id=s.document_id
			WHERE s.company_id=$1 AND s.source_document_id=$2 AND d.status='POSTED')`,
			session.CurrentCompanyID, item.ID).Scan(&hasDownstream); err != nil {
			return err
		}
		if hasDownstream {
			item.AvailableActions.CanCancel = false
		}
	}
	if item.Kind == SalesOrder && (item.Status == "CONFIRMED" || item.Status == "PARTIALLY_FULFILLED") {
		item.AvailableActions.CanCreateDispatch = salesActionPermission(session, "sales.dispatch.manage", "commercial.document.manage") && salesHasRemaining(item.Lines, true)
	}
	if (item.Kind == SalesOrder || item.Kind == SalesDispatch) && item.Status != "DRAFT" && item.Status != "CANCELLED" {
		item.AvailableActions.CanCreateInvoice = salesActionPermission(session, "sales.invoice.manage", "commercial.document.manage") && salesHasRemaining(item.Lines, false)
	}
	if item.Kind == SalesInvoice && item.Status == "POSTED" {
		item.AvailableActions.CanCreateReturn = salesActionPermission(session, "sales.return.manage", "commercial.document.manage") && salesHasReturnable(item.Lines)
		if s.finance != nil {
			settlement, err := s.finance.ReadDocumentSettlement(ctx, item.CompanyID, item.ID)
			if err != nil {
				return err
			}
			item.AvailableActions.CanCollect = session.HasPermission("finance.payment.post") && settlement.ReturnStatus != "FULL" && commercialAmountPositive(settlement.AmountDue)
			// A collected (fully or partially) invoice cannot be cancelled until
			// its allocated collections are reversed; the finance layer enforces
			// the same rule (ErrInvoiceHasDependencies).
			if settlement.PaymentStatus == "PAID" || settlement.PaymentStatus == "PARTIALLY_PAID" {
				item.AvailableActions.CanCancel = false
			}
		}
	}
	return nil
}

func commercialPrimaryTransitionAvailable(kind CommercialKind, status string) bool {
	status = strings.ToUpper(strings.TrimSpace(status))
	return status == "DRAFT" || (kind == SalesQuote && status == "SENT")
}

func commercialAmountPositive(value string) bool {
	ratio, ok := new(big.Rat).SetString(strings.TrimSpace(value))
	return ok && ratio.Sign() > 0
}

func salesActionPermission(session identity.Session, permission, fallback string) bool {
	return session.HasPermission(permission) || session.HasPermission(fallback)
}

func salesHasRemaining(lines []CommercialLine, fulfillment bool) bool {
	for _, line := range lines {
		// Service lines never ship, so they never carry a dispatch/fulfillment
		// remainder — only their invoicing remainder counts.
		if fulfillment && strings.EqualFold(line.LineType, "SERVICE") {
			continue
		}
		value := line.RemainingInvoicingBaseQuantity
		if fulfillment {
			value = line.RemainingFulfillmentBaseQuantity
		}
		if decimalPositive(value) {
			return true
		}
	}
	return false
}

func salesHasReturnable(lines []CommercialLine) bool {
	for _, line := range lines {
		if decimalPositive(line.RemainingReturnBaseQuantity) || decimalPositive(line.RemainingReturnQuantity) {
			return true
		}
	}
	return false
}

func loadCommercialRelatedDocuments(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}, session identity.Session, documentID string) ([]SourceDocumentReference, error) {
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
		ref                          SourceDocumentReference
		branchID, warehouseID, state string
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
		if err = ensureCommercialReadScope(ctx, q, session, branchID, nullableCommercialWarehouse(warehouseID)); err != nil {
			if errors.Is(err, identity.ErrForbidden) {
				continue
			}
			return nil, err
		}
		ref.Kind = sourceKindForCommercialDocument(ref.DocumentTypeCode)
		ref.Direction = "TARGET"
		ref.RelationType = "RELATED"
		ref.Status = strings.ToUpper(strings.TrimSpace(status))
		if spec, exists := commercialSpecForSourceType(ref.DocumentTypeCode); exists {
			ref.LifecycleStatus = commercialLifecycleStatus(spec.kind, ref.Status)
			// A related header is only visible when every physical line is
			// readable in the caller's warehouse scope. This mirrors source
			// selection and prevents leaking a multi-warehouse document number.
			lines, lineErr := loadCommercialLines(ctx, q, session.CurrentCompanyID, spec, ref.ID)
			if lineErr != nil {
				return nil, lineErr
			}
			if lineErr = ensureCommercialLineReadScopes(ctx, q, session, branchID, lines); lineErr != nil {
				if errors.Is(lineErr, identity.ErrForbidden) {
					continue
				}
				return nil, lineErr
			}
		} else {
			ref.LifecycleStatus = ref.Status
		}
		result = append(result, ref)
	}
	return result, nil
}

func nullableCommercialWarehouse(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return &value
}

func sourceKindForCommercialDocument(code string) string {
	if kind, ok := commercialSpecForSourceType(code); ok {
		return sourceKindForCommercial(kind.kind)
	}
	return ""
}
