package finance

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/google/uuid"
)

type PaymentPlanListOptions struct {
	PartyID, Side, Search, Status, Currency, DueFrom, DueTo, Preset, Sort, Cursor string
	Limit                                                                         int
}
type PaymentPlanSummary struct {
	ID            string    `json:"id"`
	PartyID       string    `json:"party_id"`
	PartyName     string    `json:"party_name"`
	Description   string    `json:"description"`
	Currency      string    `json:"currency"`
	TotalAmount   string    `json:"total_amount"`
	OpenAmount    string    `json:"open_amount"`
	OverdueAmount string    `json:"overdue_amount"`
	NextDueDate   *string   `json:"next_due_date,omitempty"`
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
}
type PaymentPlanListResult struct {
	Items      []PaymentPlanSummary `json:"items"`
	NextCursor string               `json:"next_cursor,omitempty"`
}
type paymentPlanCursor struct{ Key, ID, Scope string }

func normalizePaymentPlanList(o PaymentPlanListOptions) (PaymentPlanListOptions, error) {
	invalid := func() (PaymentPlanListOptions, error) {
		return o, fmt.Errorf("%w: plan filtreleri geçersiz", identity.ErrValidation)
	}
	o.Search = strings.TrimSpace(o.Search)
	if len([]rune(o.Search)) > 128 || o.PartyID != "" && uuid.Validate(o.PartyID) != nil {
		return invalid()
	}
	if o.Side != "RECEIVABLE" && o.Side != "PAYABLE" {
		return invalid()
	}
	if o.Status != "" && o.Status != "OPEN" && o.Status != "CLOSED" && o.Status != "CANCELLED" {
		return invalid()
	}
	o.Currency = strings.ToUpper(strings.TrimSpace(o.Currency))
	if o.Currency != "" && (len(o.Currency) != 3 || strings.Trim(o.Currency, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "") {
		return invalid()
	}
	for _, d := range []string{o.DueFrom, o.DueTo} {
		if d != "" {
			if _, err := time.Parse("2006-01-02", d); err != nil {
				return invalid()
			}
		}
	}
	if o.DueFrom != "" && o.DueTo != "" && o.DueFrom > o.DueTo {
		return invalid()
	}
	if o.Preset != "" && o.Preset != "OVERDUE" && o.Preset != "TODAY" && o.Preset != "NEXT_7_DAYS" {
		return invalid()
	}
	if o.Sort == "" {
		o.Sort = "next_due:asc"
	}
	if o.Sort != "next_due:asc" && o.Sort != "created_at:desc" && o.Sort != "created_at:asc" {
		return invalid()
	}
	if o.Limit <= 0 {
		o.Limit = 50
	}
	if o.Limit > 100 {
		o.Limit = 100
	}
	return o, nil
}

// ListPaymentPlanSummaries filters the complete visible set before keyset pagination.
// The common dues function is evaluated once; allocations and source details are
// fetched only when a user opens a plan.
func (s *Service) ListPaymentPlanSummaries(ctx context.Context, session identity.Session, options PaymentPlanListOptions) (PaymentPlanListResult, error) {
	result := PaymentPlanListResult{Items: []PaymentPlanSummary{}}
	o, err := normalizePaymentPlanList(options)
	if err != nil {
		return result, err
	}
	if identity.ValidateExternalActor(session) != nil || !planReadAllowed(session, o.Side) {
		return result, identity.ErrForbidden
	}
	today := s.now().Format("2006-01-02")
	fingerprintOptions := o
	fingerprintOptions.Cursor = ""
	raw, _ := json.Marshal([]any{session.CurrentCompanyID, session.User.ID, today, fingerprintOptions})
	scope := fmt.Sprintf("%x", sha256.Sum256(raw))
	cursor := paymentPlanCursor{}
	if o.Cursor != "" {
		data, e := base64.RawURLEncoding.DecodeString(o.Cursor)
		if e != nil || json.Unmarshal(data, &cursor) != nil || cursor.Scope != scope || uuid.Validate(cursor.ID) != nil || cursor.Key == "" {
			return result, fmt.Errorf("%w: sayfa bilgisi geçersiz; ilk sayfadan başlayın", identity.ErrValidation)
		}
	}
	args := []any{session.CurrentCompanyID, session.User.ID, o.Side, o.PartyID, today, o.Status, o.Currency, o.DueFrom, o.DueTo, o.Preset}
	query := `WITH visible AS MATERIALIZED (
 SELECT p.*,pt.display_name,pt.code AS party_code FROM finance_payment_plans p
 JOIN parties pt ON pt.company_id=p.company_id AND pt.id=p.party_id
 WHERE p.company_id=$1 AND p.side=$3 AND ($4='' OR p.party_id::text=$4)
 AND ($7='' OR p.currency=$7) AND ` + planScopeSQL
	for _, token := range strings.Fields(o.Search) {
		args = append(args, "%"+strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(token)+"%")
		n := len(args)
		query += fmt.Sprintf(` AND (p.description ILIKE $%[1]d OR pt.display_name ILIKE $%[1]d OR pt.code ILIKE $%[1]d OR EXISTS (
   SELECT 1 FROM finance_payment_plan_sources ps JOIN finance_invoice_open_items oi ON oi.company_id=ps.company_id AND oi.id=ps.open_item_id
   JOIN documents d ON d.company_id=oi.company_id AND d.id=oi.document_id
   WHERE ps.company_id=p.company_id AND ps.plan_id=p.id AND d.document_no ILIKE $%[1]d))`, n)
	}
	query += `), dues AS MATERIALIZED (
 SELECT d.* FROM finance_scheduled_dues($1,$5::date) d JOIN visible v ON v.id=d.plan_id WHERE v.cancelled_at IS NULL
 ), totals AS (
 SELECT v.id,COALESCE(sum(d.open_amount),0) AS remaining,
 COALESCE(sum(d.open_amount) FILTER(WHERE d.due_date<$5::date),0) AS overdue,
 min(d.due_date) FILTER(WHERE d.open_amount>0) AS next_due,
 COALESCE(bool_or(d.open_amount>0 AND (NULLIF($8,'')::date IS NULL OR d.due_date>=NULLIF($8,'')::date) AND (NULLIF($9,'')::date IS NULL OR d.due_date<=NULLIF($9,'')::date)),false) AS in_range,
 COALESCE(bool_or(d.open_amount>0 AND d.due_date=$5::date),false) AS today_due,
 COALESCE(bool_or(d.open_amount>0 AND d.due_date BETWEEN $5::date AND $5::date+6),false) AS week_due
 FROM visible v LEFT JOIN dues d ON d.plan_id=v.id GROUP BY v.id
 ), summaries AS (
 SELECT v.*,t.remaining,t.overdue,t.next_due,
 CASE WHEN v.cancelled_at IS NOT NULL THEN 'CANCELLED' WHEN t.remaining=0 THEN 'CLOSED' ELSE 'OPEN' END AS plan_status,
 `
	sortExpr := "v.created_at"
	direction := "ASC"
	comparison := ">"
	if o.Sort == "next_due:asc" {
		sortExpr = "COALESCE(t.next_due::timestamp AT TIME ZONE 'UTC','infinity'::timestamptz)"
	}
	if o.Sort == "created_at:desc" {
		direction = "DESC"
		comparison = "<"
	}
	query += sortExpr + ` AS sort_key FROM visible v JOIN totals t ON t.id=v.id
 WHERE (($8='' AND $9='') OR t.in_range)
 AND ($10='' OR ($10='OVERDUE' AND t.overdue>0) OR ($10='TODAY' AND t.today_due) OR ($10='NEXT_7_DAYS' AND t.week_due))
 ) SELECT id,party_id,display_name,description,currency,total_amount::text,remaining::text,overdue::text,next_due::text,plan_status,created_at,sort_key::text AS cursor_key
 FROM summaries WHERE ($6='' OR plan_status=$6)`
	if o.Cursor != "" {
		// Parse server-generated timestamps before passing them to PostgreSQL.
		if cursor.Key != "infinity" {
			if _, e := time.Parse("2006-01-02 15:04:05.999999999Z07:00", cursor.Key); e != nil {
				if _, e = time.Parse("2006-01-02 15:04:05.999999999Z07", cursor.Key); e != nil {
					return result, fmt.Errorf("%w: sayfa tarihi geçersiz", identity.ErrValidation)
				}
			}
		}
		args = append(args, cursor.Key, cursor.ID)
		query += fmt.Sprintf(" AND (sort_key,id) %s ($%d::timestamptz,$%d::uuid)", comparison, len(args)-1, len(args))
	}
	args = append(args, o.Limit+1)
	query += fmt.Sprintf(" ORDER BY sort_key %s,id %s LIMIT $%d", direction, direction, len(args))
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	keys := []string{}
	for rows.Next() {
		var item PaymentPlanSummary
		var key string
		if err = rows.Scan(&item.ID, &item.PartyID, &item.PartyName, &item.Description, &item.Currency, &item.TotalAmount, &item.OpenAmount, &item.OverdueAmount, &item.NextDueDate, &item.Status, &item.CreatedAt, &key); err != nil {
			return result, err
		}
		result.Items = append(result.Items, item)
		keys = append(keys, key)
	}
	if err = rows.Err(); err != nil {
		return result, err
	}
	if len(result.Items) > o.Limit {
		result.Items = result.Items[:o.Limit]
		last := result.Items[o.Limit-1]
		data, _ := json.Marshal(paymentPlanCursor{keys[o.Limit-1], last.ID, scope})
		result.NextCursor = base64.RawURLEncoding.EncodeToString(data)
	}
	return result, nil
}
