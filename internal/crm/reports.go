package crm

import (
	"context"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/google/uuid"
)

// --- Reminders -------------------------------------------------------------

// DueReminders returns the caller's open tasks whose reminder has not rung yet
// and that fall due within the next day (or are at most a day late). The
// browser rings each one at due_at - remind_minutes and then reports it back
// through MarkRemindersNotified, so a reminder rings once across tabs and
// devices.
func (s *Service) DueReminders(ctx context.Context, session identity.Session) ([]Activity, error) {
	if !session.HasPermission("crm.read") {
		return nil, identity.ErrForbidden
	}
	rows, err := s.pool.Query(ctx, activitySelect+` WHERE a.company_id=$1`+activityVisible+` AND a.kind='TASK'
 AND a.assigned_user_id=$3::uuid AND a.done_at IS NULL AND a.notified_at IS NULL
 AND a.due_at BETWEEN now()-interval '1 day' AND now()+interval '1 day'
 ORDER BY a.due_at LIMIT 200`, session.CurrentCompanyID, canSeeAll(session), session.User.ID)
	if err != nil {
		return nil, err
	}
	return collectActivities(rows)
}

func (s *Service) MarkRemindersNotified(ctx context.Context, session identity.Session, ids []string) error {
	if !session.HasPermission("crm.read") {
		return identity.ErrForbidden
	}
	cleaned := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, err := uuid.Parse(strings.TrimSpace(id)); err == nil {
			cleaned = append(cleaned, strings.TrimSpace(id))
		}
	}
	if len(cleaned) == 0 {
		return nil
	}
	_, err := s.pool.Exec(ctx, `UPDATE crm_activities SET notified_at=now()
 WHERE company_id=$1 AND assigned_user_id=$2::uuid AND id=ANY($3::uuid[]) AND notified_at IS NULL`,
		session.CurrentCompanyID, session.User.ID, cleaned)
	return err
}

// --- Reports ---------------------------------------------------------------

type ReportFilter struct {
	From   time.Time
	To     time.Time
	UserID string
}

type RepStats struct {
	UserID       string `json:"user_id"`
	Name         string `json:"name"`
	Calls        int    `json:"calls"`
	Reached      int    `json:"reached"`
	Positive     int    `json:"positive"`
	Meetings     int    `json:"meetings"`
	TalkSeconds  int    `json:"talk_seconds"`
	LeadsCreated int    `json:"leads_created"`
	Won          int    `json:"won"`
}

type CountRow struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

type DayRow struct {
	Day     string `json:"day"`
	Calls   int    `json:"calls"`
	Reached int    `json:"reached"`
}

type StageRow struct {
	StageID     string `json:"stage_id"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Probability int    `json:"probability"`
	Currency    string `json:"currency"`
	Count       int    `json:"count"`
	Amount      string `json:"amount"`
	Weighted    string `json:"weighted"`
}

type MoneyRow struct {
	Currency string `json:"currency"`
	Count    int    `json:"count"`
	Amount   string `json:"amount"`
}

type Report struct {
	From        string     `json:"from"`
	To          string     `json:"to"`
	Reps        []RepStats `json:"reps"`
	Outcomes    []CountRow `json:"outcomes"`
	Days        []DayRow   `json:"days"`
	LeadStatus  []CountRow `json:"lead_status"`
	Sources     []CountRow `json:"sources"`
	Pipeline    []StageRow `json:"pipeline"`
	Won         []MoneyRow `json:"won"`
	LostReasons []CountRow `json:"lost_reasons"`
}

// reachedOutcomes is the SQL list matching Reached().
const reachedOutcomes = `('CALLBACK','NOT_INTERESTED','INTERESTED','MEETING_SET','DO_NOT_CALL')`

// reportZone groups calls into calendar days the way the team lives them.
const reportZone = "Europe/Istanbul"

// BuildReport summarises call work and the pipeline for [From, To). Without
// crm.view_all the report is limited to the caller's own work.
func (s *Service) BuildReport(ctx context.Context, session identity.Session, filter ReportFilter) (Report, error) {
	if !session.HasPermission("crm.read") {
		return Report{}, identity.ErrForbidden
	}
	if filter.To.IsZero() || filter.From.IsZero() || !filter.To.After(filter.From) {
		return Report{}, errInvalid("geçersiz tarih aralığı")
	}
	if filter.To.Sub(filter.From) > 367*24*time.Hour {
		return Report{}, errInvalid("rapor aralığı en fazla bir yıl olabilir")
	}
	userID := strings.TrimSpace(filter.UserID)
	if !canSeeAll(session) {
		userID = session.User.ID
	}
	if userID != "" {
		if _, err := uuid.Parse(userID); err != nil {
			return Report{}, errInvalid("geçersiz kullanıcı")
		}
	}
	report := Report{
		From: filter.From.Format(time.RFC3339), To: filter.To.Format(time.RFC3339),
		Reps: []RepStats{}, Outcomes: []CountRow{}, Days: []DayRow{}, LeadStatus: []CountRow{},
		Sources: []CountRow{}, Pipeline: []StageRow{}, Won: []MoneyRow{}, LostReasons: []CountRow{},
	}
	args := []any{session.CurrentCompanyID, filter.From, filter.To, userID}

	// Per rep: calls, conversations and talk time in the range, leads they
	// created and opportunities they won.
	rows, err := s.pool.Query(ctx, `WITH calls AS (
   SELECT a.created_by AS user_id,count(*) AS calls,
          count(*) FILTER (WHERE a.call_outcome IN `+reachedOutcomes+`) AS reached,
          count(*) FILTER (WHERE a.call_outcome IN ('INTERESTED','MEETING_SET')) AS positive,
          count(*) FILTER (WHERE a.call_outcome='MEETING_SET') AS meetings,
          COALESCE(sum(a.duration_seconds),0) AS talk
   FROM crm_activities a WHERE a.company_id=$1 AND a.kind='CALL' AND a.occurred_at>=$2 AND a.occurred_at<$3
     AND ($4='' OR a.created_by=NULLIF($4,'')::uuid) GROUP BY a.created_by),
 leads AS (
   SELECT l.created_by AS user_id,count(*) AS created FROM crm_leads l
   WHERE l.company_id=$1 AND l.created_at>=$2 AND l.created_at<$3 AND ($4='' OR l.created_by=NULLIF($4,'')::uuid) GROUP BY l.created_by),
 won AS (
   SELECT o.owner_user_id AS user_id,count(*) AS won FROM crm_opportunities o
   JOIN crm_pipeline_stages st ON st.company_id=o.company_id AND st.id=o.stage_id
   WHERE o.company_id=$1 AND st.kind='WON' AND o.closed_at>=$2 AND o.closed_at<$3 AND o.owner_user_id IS NOT NULL
     AND ($4='' OR o.owner_user_id=NULLIF($4,'')::uuid) GROUP BY o.owner_user_id),
 people AS (SELECT user_id FROM calls UNION SELECT user_id FROM leads UNION SELECT user_id FROM won)
 SELECT p.user_id::text,COALESCE(u.display_name,''),COALESCE(c.calls,0),COALESCE(c.reached,0),COALESCE(c.positive,0),
        COALESCE(c.meetings,0),COALESCE(c.talk,0),COALESCE(l.created,0),COALESCE(w.won,0)
 FROM people p LEFT JOIN users u ON u.id=p.user_id
 LEFT JOIN calls c ON c.user_id=p.user_id LEFT JOIN leads l ON l.user_id=p.user_id LEFT JOIN won w ON w.user_id=p.user_id
 ORDER BY COALESCE(c.calls,0) DESC,u.display_name`, args...)
	if err != nil {
		return Report{}, err
	}
	for rows.Next() {
		var r RepStats
		if err := rows.Scan(&r.UserID, &r.Name, &r.Calls, &r.Reached, &r.Positive, &r.Meetings, &r.TalkSeconds, &r.LeadsCreated, &r.Won); err != nil {
			rows.Close()
			return Report{}, err
		}
		report.Reps = append(report.Reps, r)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return Report{}, err
	}

	if report.Outcomes, err = s.countRows(ctx, `SELECT a.call_outcome,count(*) FROM crm_activities a
 WHERE a.company_id=$1 AND a.kind='CALL' AND a.occurred_at>=$2 AND a.occurred_at<$3 AND ($4='' OR a.created_by=NULLIF($4,'')::uuid)
 GROUP BY a.call_outcome ORDER BY count(*) DESC`, args...); err != nil {
		return Report{}, err
	}

	rows, err = s.pool.Query(ctx, `SELECT to_char((a.occurred_at AT TIME ZONE '`+reportZone+`')::date,'YYYY-MM-DD'),count(*),
   count(*) FILTER (WHERE a.call_outcome IN `+reachedOutcomes+`)
 FROM crm_activities a WHERE a.company_id=$1 AND a.kind='CALL' AND a.occurred_at>=$2 AND a.occurred_at<$3
   AND ($4='' OR a.created_by=NULLIF($4,'')::uuid)
 GROUP BY 1 ORDER BY 1`, args...)
	if err != nil {
		return Report{}, err
	}
	for rows.Next() {
		var d DayRow
		if err := rows.Scan(&d.Day, &d.Calls, &d.Reached); err != nil {
			rows.Close()
			return Report{}, err
		}
		report.Days = append(report.Days, d)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return Report{}, err
	}

	// Lead funnel and sources describe the current lead book, not the range.
	if report.LeadStatus, err = s.countRows(ctx, `SELECT l.status,count(*) FROM crm_leads l
 WHERE l.company_id=$1 AND ($2::timestamptz IS NOT NULL) AND ($3::timestamptz IS NOT NULL)
   AND ($4='' OR l.owner_user_id=NULLIF($4,'')::uuid) GROUP BY l.status`, args...); err != nil {
		return Report{}, err
	}
	if report.Sources, err = s.countRows(ctx, `SELECT COALESCE(NULLIF(l.source,''),'Belirtilmemiş'),count(*) FROM crm_leads l
 WHERE l.company_id=$1 AND l.created_at>=$2 AND l.created_at<$3 AND ($4='' OR l.owner_user_id=NULLIF($4,'')::uuid)
 GROUP BY 1 ORDER BY count(*) DESC LIMIT 20`, args...); err != nil {
		return Report{}, err
	}

	if err = ensureStages(ctx, s.pool, session.CurrentCompanyID); err != nil {
		return Report{}, err
	}
	rows, err = s.pool.Query(ctx, `SELECT st.id::text,st.name,st.kind,st.probability,COALESCE(o.currency,''),count(o.id),
   COALESCE(sum(o.amount),0)::text,COALESCE(round(sum(o.amount*st.probability/100.0),2),0)::text
 FROM crm_pipeline_stages st
 LEFT JOIN crm_opportunities o ON o.company_id=st.company_id AND o.stage_id=st.id
   AND ($4='' OR o.owner_user_id=NULLIF($4,'')::uuid)
   AND (st.kind='OPEN' OR (o.closed_at>=$2 AND o.closed_at<$3))
 WHERE st.company_id=$1 AND st.is_active
 GROUP BY st.id,st.name,st.kind,st.probability,st.sort_order,o.currency
 ORDER BY st.sort_order,o.currency NULLS FIRST`, args...)
	if err != nil {
		return Report{}, err
	}
	for rows.Next() {
		var r StageRow
		if err := rows.Scan(&r.StageID, &r.Name, &r.Kind, &r.Probability, &r.Currency, &r.Count, &r.Amount, &r.Weighted); err != nil {
			rows.Close()
			return Report{}, err
		}
		r.Amount, r.Weighted = trimDecimal(r.Amount), trimDecimal(r.Weighted)
		report.Pipeline = append(report.Pipeline, r)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return Report{}, err
	}

	rows, err = s.pool.Query(ctx, `SELECT o.currency,count(*),sum(o.amount)::text FROM crm_opportunities o
 JOIN crm_pipeline_stages st ON st.company_id=o.company_id AND st.id=o.stage_id
 WHERE o.company_id=$1 AND st.kind='WON' AND o.closed_at>=$2 AND o.closed_at<$3 AND ($4='' OR o.owner_user_id=NULLIF($4,'')::uuid)
 GROUP BY o.currency ORDER BY sum(o.amount) DESC`, args...)
	if err != nil {
		return Report{}, err
	}
	for rows.Next() {
		var r MoneyRow
		if err := rows.Scan(&r.Currency, &r.Count, &r.Amount); err != nil {
			rows.Close()
			return Report{}, err
		}
		r.Amount = trimDecimal(r.Amount)
		report.Won = append(report.Won, r)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return Report{}, err
	}

	if report.LostReasons, err = s.countRows(ctx, `SELECT o.lost_reason,count(*) FROM crm_opportunities o
 JOIN crm_pipeline_stages st ON st.company_id=o.company_id AND st.id=o.stage_id
 WHERE o.company_id=$1 AND st.kind='LOST' AND o.closed_at>=$2 AND o.closed_at<$3 AND ($4='' OR o.owner_user_id=NULLIF($4,'')::uuid)
 GROUP BY o.lost_reason ORDER BY count(*) DESC LIMIT 20`, args...); err != nil {
		return Report{}, err
	}
	return report, nil
}

func (s *Service) countRows(ctx context.Context, sql string, args ...any) ([]CountRow, error) {
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []CountRow{}
	for rows.Next() {
		var r CountRow
		if err := rows.Scan(&r.Key, &r.Count); err != nil {
			return nil, err
		}
		items = append(items, r)
	}
	return items, rows.Err()
}
