package finance

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// DueSchedule augments an invoice without changing its identity or balance.
// Statements flatten these rows; payments consolidate back to that invoice.
type OpenItemDue struct {
	PlanID        *string    `json:"plan_id,omitempty"`
	InstallmentNo int        `json:"installment_no"`
	DueDate       *time.Time `json:"due_date,omitempty"`
	OpenAmount    string     `json:"open_amount"`
}

func attachOpenItemDues(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, companyID string, asOf time.Time, items []OpenItem) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]string, len(items))
	indices := map[string]int{}
	for i, item := range items {
		ids[i] = item.ID
		indices[item.ID] = i
	}
	rows, err := q.Query(ctx, `SELECT ids.id,sd.plan_id,sd.installment_no,sd.due_date,sd.open_amount::numeric(24,4)::text
 FROM unnest($3::uuid[]) ids(id)
 JOIN LATERAL finance_scheduled_dues($1,$2::date,ids.id) sd ON true
 WHERE sd.open_amount>0 AND EXISTS(SELECT 1 FROM finance_payment_plan_sources ps WHERE ps.company_id=$1 AND ps.open_item_id=ids.id AND ps.active)
 ORDER BY ids.id,sd.due_date NULLS LAST,sd.installment_no`, companyID, asOf.Format("2006-01-02"), ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var due OpenItemDue
		if err = rows.Scan(&id, &due.PlanID, &due.InstallmentNo, &due.DueDate, &due.OpenAmount); err != nil {
			return err
		}
		i := indices[id]
		items[i].DueSchedule = append(items[i].DueSchedule, due)
	}
	return rows.Err()
}
