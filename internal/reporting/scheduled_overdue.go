package reporting

import (
	"context"
	"github.com/alpyxn/varyaone/internal/identity"
	"time"
)

// Overdue reports and party aging share the dated installment projection.
func (s *Service) scheduledOverdue(ctx context.Context, session identity.Session, asOf time.Time, side string) ([]OverdueRow, error) {
	if !canRead(session) {
		return nil, identity.ErrForbidden
	}
	if asOf.IsZero() {
		asOf = time.Now().UTC()
	}
	rows, err := s.pool.Query(ctx, `SELECT COALESCE(oi.document_id::text,''),COALESCE(d.document_no,me.document_no,''),pt.display_name,sd.due_date::text,
   ($2::date-sd.due_date)::integer,sd.open_amount::numeric(24,4)::text,oi.currency
 FROM finance_scheduled_dues($1,$2::date) sd
 JOIN finance_invoice_open_items oi ON oi.company_id=$1 AND oi.id=sd.open_item_id
 LEFT JOIN documents d ON d.company_id=oi.company_id AND d.id=oi.document_id AND d.document_type_code IN ('SALES_INVOICE','PURCHASE_INVOICE')
 LEFT JOIN finance_manual_entries me ON me.company_id=oi.company_id AND me.id=oi.manual_entry_id
 JOIN parties pt ON pt.company_id=oi.company_id AND pt.id=oi.party_id
 WHERE oi.side=$3 AND sd.open_amount>0 AND sd.due_date<$2::date
 AND (d.id IS NOT NULL OR me.id IS NOT NULL)
 AND (d.branch_id IS NULL OR NOT EXISTS(SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=$1 AND bs.user_id=$4)
 OR EXISTS(SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=$1 AND bs.user_id=$4 AND bs.branch_id=d.branch_id))
 ORDER BY sd.due_date,COALESCE(d.document_no,me.document_no),sd.installment_no`, session.CurrentCompanyID, asOf.Format("2006-01-02"), side, session.User.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []OverdueRow{}
	for rows.Next() {
		var row OverdueRow
		if err = rows.Scan(&row.DocumentID, &row.DocumentNo, &row.PartyName, &row.DueDate, &row.DaysOverdue, &row.AmountDue, &row.Currency); err != nil {
			return nil, err
		}
		items = append(items, row)
	}
	return items, rows.Err()
}
