package crm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/platform/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	maxListLimit     = 200
	maxActivityLimit = 200
	maxDuplicates    = 5
)

type Service struct{ pool database.Querier }

// reader is what lookups need; both the pool and an open transaction satisfy it.
type reader interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func NewService(pool database.Querier) *Service { return &Service{pool: pool} }

func errInvalid(message string) error { return fmt.Errorf("%w: %s", identity.ErrValidation, message) }

// DuplicateError is returned when a new lead's phone number already belongs to
// another lead. The caller can repeat the request with AllowDuplicate once the
// user has seen the matches; a match marked do-not-call is reported too, so a
// number that asked not to be called is not quietly imported again.
type DuplicateError struct{ Matches []LeadSummary }

func (e *DuplicateError) Error() string { return "CRM_LEAD_DUPLICATE_PHONE" }

type LeadSummary struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Phone       string `json:"phone"`
	Status      string `json:"status"`
	DoNotCall   bool   `json:"do_not_call"`
	OwnerName   string `json:"owner_name"`
}

type Lead struct {
	ID               string     `json:"id"`
	DisplayName      string     `json:"display_name"`
	ContactName      string     `json:"contact_name"`
	ContactTitle     string     `json:"contact_title"`
	Phone            string     `json:"phone"`
	Phone2           string     `json:"phone2"`
	Email            string     `json:"email"`
	City             string     `json:"city"`
	District         string     `json:"district"`
	Sector           string     `json:"sector"`
	Source           string     `json:"source"`
	Notes            string     `json:"notes"`
	Address          string     `json:"address"`
	Website          string     `json:"website"`
	Priority         string     `json:"priority"`
	Status           string     `json:"status"`
	OwnerUserID      *string    `json:"owner_user_id,omitempty"`
	OwnerName        string     `json:"owner_name"`
	NextActionAt     *time.Time `json:"next_action_at,omitempty"`
	DoNotCall        bool       `json:"do_not_call"`
	DoNotCallReason  string     `json:"do_not_call_reason"`
	DoNotCallAt      *time.Time `json:"do_not_call_at,omitempty"`
	PrivacyNoticeAt  *time.Time `json:"privacy_notice_at,omitempty"`
	CallAttempts     int        `json:"call_attempts"`
	LastCallAt       *time.Time `json:"last_call_at,omitempty"`
	LastCallOutcome  *string    `json:"last_call_outcome,omitempty"`
	ConvertedPartyID *string    `json:"converted_party_id,omitempty"`
	ConvertedAt      *time.Time `json:"converted_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	Version          int64      `json:"version"`
}

type LeadInput struct {
	DisplayName    string `json:"display_name"`
	ContactName    string `json:"contact_name"`
	ContactTitle   string `json:"contact_title"`
	Phone          string `json:"phone"`
	Phone2         string `json:"phone2"`
	Email          string `json:"email"`
	City           string `json:"city"`
	District       string `json:"district"`
	Sector         string `json:"sector"`
	Source         string `json:"source"`
	Notes          string `json:"notes"`
	Address        string `json:"address"`
	Website        string `json:"website"`
	Priority       string `json:"priority"`
	Status         string `json:"status"`
	OwnerUserID    string `json:"owner_user_id"`
	NextActionAt   string `json:"next_action_at"`
	PrivacyNotice  bool   `json:"privacy_notice"`
	AllowDuplicate bool   `json:"allow_duplicate"`
}

// LeadFilter narrows a lead listing. Owner is "me", "none", a user id, or empty
// for every lead the caller may see.
type LeadFilter struct {
	Query string
	// Status and Priority take one value or a comma-separated list
	// ("NEW,CONTACTED", "A,B"); priority "none" matches ungraded leads.
	Status   string
	Priority string
	Owner    string
	Source   string
	City     string
	District string
	// HideClosed leaves out leads that were ruled out (DISQUALIFIED, e.g.
	// "ilgilenmiyor") or asked not to be called, unless the status or
	// do-not-call filter asks for exactly those.
	HideClosed bool
	// NeverCalled keeps only leads nobody has called yet.
	NeverCalled bool
	// Callable keeps only leads a call flow may offer: not do-not-call, not
	// ruled out or converted, and either never called or with a callback that
	// is due. A lead already called never comes up again by itself.
	Callable  bool
	DueOnly   bool
	DoNotCall string // "", "only", "exclude"
	// Sort is "priority" (default: A first, then oldest), "newest" or "name".
	Sort string
}

type LeadList struct {
	Items      []Lead `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}

type Activity struct {
	ID              string     `json:"id"`
	LeadID          *string    `json:"lead_id,omitempty"`
	LeadName        string     `json:"lead_name,omitempty"`
	PartyID         *string    `json:"party_id,omitempty"`
	PartyName       string     `json:"party_name,omitempty"`
	OpportunityID   *string    `json:"opportunity_id,omitempty"`
	Kind            string     `json:"kind"`
	Subject         string     `json:"subject"`
	Body            string     `json:"body"`
	CallOutcome     *string    `json:"call_outcome,omitempty"`
	DurationSeconds *int       `json:"duration_seconds,omitempty"`
	OccurredAt      time.Time  `json:"occurred_at"`
	DueAt           *time.Time `json:"due_at,omitempty"`
	RemindMinutes   int        `json:"remind_minutes"`
	DoneAt          *time.Time `json:"done_at,omitempty"`
	AssignedUserID  *string    `json:"assigned_user_id,omitempty"`
	AssignedName    string     `json:"assigned_name"`
	CreatedBy       string     `json:"created_by"`
	CreatedByName   string     `json:"created_by_name"`
	CreatedAt       time.Time  `json:"created_at"`
	Version         int64      `json:"version"`
}

// ActivityInput records a note, meeting, e-mail, task or (on a cari) a call.
// Calls to a lead go through LogCall, which also moves the lead forward.
type ActivityInput struct {
	Kind            string `json:"kind"`
	Subject         string `json:"subject"`
	Body            string `json:"body"`
	CallOutcome     string `json:"call_outcome"`
	DurationSeconds *int   `json:"duration_seconds"`
	OccurredAt      string `json:"occurred_at"`
	DueAt           string `json:"due_at"`
	RemindMinutes   int    `json:"remind_minutes"`
	AssignedUserID  string `json:"assigned_user_id"`
}

type CallInput struct {
	// CallListItemID is set when the call came from a call list; the item is
	// then moved on by the list's retry rules.
	CallListItemID  string `json:"call_list_item_id"`
	Outcome         string `json:"outcome"`
	Note            string `json:"note"`
	DurationSeconds *int   `json:"duration_seconds"`
	CallbackAt      string `json:"callback_at"`
	RemindMinutes   int    `json:"remind_minutes"`
}

type CallResult struct {
	Lead     Lead      `json:"lead"`
	Activity Activity  `json:"activity"`
	Task     *Activity `json:"task,omitempty"`
}

// TaskFilter selects tasks. Scope is "mine" (default) or "all" (needs
// crm.view_all); State is "open" (default), "overdue", "today" or "done".
type TaskFilter struct {
	Scope string
	State string
}

type Member struct {
	UserID      string `json:"user_id"`
	DisplayName string `json:"display_name"`
}

func canSeeAll(session identity.Session) bool { return session.HasPermission("crm.view_all") }

// --- Leads -----------------------------------------------------------------

const leadSelect = `SELECT l.id::text,l.display_name,l.contact_name,l.contact_title,l.phone,l.phone2,l.email,
 l.city,l.district,l.sector,l.source,l.notes,l.address,l.website,l.priority,l.status,l.owner_user_id::text,COALESCE(u.display_name,''),
 l.next_action_at,l.do_not_call,l.do_not_call_reason,l.do_not_call_at,l.privacy_notice_at,
 l.call_attempts,l.last_call_at,l.last_call_outcome,l.converted_party_id::text,l.converted_at,
 l.created_at,l.updated_at,l.version
 FROM crm_leads l
 LEFT JOIN company_memberships m ON m.company_id=l.company_id AND m.user_id=l.owner_user_id
 LEFT JOIN users u ON u.id=m.user_id `

// leadVisible restricts a lead query to what the caller may see: everything
// with crm.view_all, otherwise their own leads and the unassigned pool.
const leadVisible = ` AND ($2::boolean OR l.owner_user_id IS NULL OR l.owner_user_id=$3::uuid)`

// sqlArgs numbers query parameters as they are added, so optional filter
// clauses never have to agree on fixed positions.
type sqlArgs struct{ values []any }

func (a *sqlArgs) add(value any) string {
	a.values = append(a.values, value)
	return "$" + strconv.Itoa(len(a.values))
}

// callableCondition is the rule every call flow applies before offering a
// lead: it can be called at all, and it has not been called before unless a
// callback for it is due.
const callableCondition = ` AND NOT l.do_not_call AND l.status NOT IN ('CONVERTED','DISQUALIFIED')
 AND (l.call_attempts=0 OR (l.next_action_at IS NOT NULL AND l.next_action_at<=now()))`

// leadFilterSQL renders the visibility rule and filter as a WHERE condition on
// crm_leads l. $1 is the company, $2/$3 drive visibility; callers add their
// own parameters through the returned sqlArgs.
func leadFilterSQL(session identity.Session, filter LeadFilter) (string, *sqlArgs, error) {
	args := &sqlArgs{}
	args.add(session.CurrentCompanyID)
	args.add(canSeeAll(session))
	args.add(session.User.ID)
	var b strings.Builder
	b.WriteString(` l.company_id=$1` + leadVisible)

	statuses, err := listParam(filter.Status, func(v string) bool { return v == StatusConverted || editableStatuses[v] })
	if err != nil {
		return "", nil, errInvalid("geçersiz durum filtresi")
	}
	if len(statuses) > 0 {
		b.WriteString(` AND l.status=ANY(` + args.add(statuses) + `::text[])`)
	}
	priorities, err := listParam(filter.Priority, func(v string) bool { return v == "NONE" || (v != "" && validPriority(v)) })
	if err != nil {
		return "", nil, errInvalid("geçersiz öncelik filtresi")
	}
	if len(priorities) > 0 {
		for index, value := range priorities {
			if value == "NONE" {
				priorities[index] = ""
			}
		}
		b.WriteString(` AND l.priority=ANY(` + args.add(priorities) + `::text[])`)
	}
	switch owner := strings.TrimSpace(filter.Owner); owner {
	case "":
	case "none":
		b.WriteString(` AND l.owner_user_id IS NULL`)
	case "me":
		b.WriteString(` AND l.owner_user_id=` + args.add(session.User.ID) + `::uuid`)
	default:
		if _, err := uuid.Parse(owner); err != nil {
			return "", nil, errInvalid("geçersiz sorumlu filtresi")
		}
		b.WriteString(` AND l.owner_user_id=` + args.add(owner) + `::uuid`)
	}
	for _, exact := range []struct{ column, value string }{
		{"l.source", filter.Source}, {"l.city", filter.City}, {"l.district", filter.District},
	} {
		if value := strings.TrimSpace(exact.value); value != "" {
			b.WriteString(` AND ` + exact.column + ` ILIKE ` + args.add(value))
		}
	}
	if query := strings.TrimSpace(filter.Query); query != "" {
		q := args.add(query)
		phone := NormalizePhone(query)
		phoneClause := ""
		if len(phone) >= 4 {
			phoneClause = ` OR l.phone_normalized LIKE '%'||` + args.add(phone) + `||'%'`
		}
		b.WriteString(` AND (l.display_name ILIKE '%'||` + q + `||'%' OR l.contact_name ILIKE '%'||` + q + `||'%' OR l.email ILIKE '%'||` + q + `||'%'` + phoneClause + `)`)
	}
	if filter.DueOnly {
		b.WriteString(` AND l.next_action_at IS NOT NULL AND l.next_action_at<=now()`)
	}
	doNotCall := strings.TrimSpace(filter.DoNotCall)
	switch doNotCall {
	case "":
	case "only":
		b.WriteString(` AND l.do_not_call`)
	case "exclude":
		b.WriteString(` AND NOT l.do_not_call`)
	default:
		return "", nil, errInvalid("geçersiz aranmasın filtresi")
	}
	askedForClosed := doNotCall == "only"
	for _, status := range statuses {
		askedForClosed = askedForClosed || status == StatusDisqualified
	}
	if filter.HideClosed && !askedForClosed {
		b.WriteString(` AND l.status<>'DISQUALIFIED' AND NOT l.do_not_call`)
	}
	if filter.NeverCalled {
		b.WriteString(` AND l.call_attempts=0`)
	}
	if filter.Callable {
		b.WriteString(callableCondition)
	}
	return b.String(), args, nil
}

// listParam splits a comma-separated filter value into upper-case items and
// checks each one.
func listParam(raw string, valid func(string) bool) ([]string, error) {
	items := []string{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.ToUpper(strings.TrimSpace(part))
		if part == "" {
			continue
		}
		if !valid(part) {
			return nil, identity.ErrValidation
		}
		items = append(items, part)
	}
	return items, nil
}

// leadSorts are the orders a lead list can be walked in. Each key is stable
// (it does not change when a lead is called), so "next lead" moves forward
// through the list and never bounces between two leads.
var leadSorts = map[string]struct{ order, after string }{
	"priority": {
		order: `COALESCE(NULLIF(l.priority,''),'Z'),l.created_at,l.id`,
		after: `(COALESCE(NULLIF(l.priority,''),'Z'),l.created_at,l.id) > (SELECT COALESCE(NULLIF(c.priority,''),'Z'),c.created_at,c.id FROM crm_leads c WHERE c.company_id=$1 AND c.id=%s::uuid)`,
	},
	"newest": {
		order: `l.created_at DESC,l.id DESC`,
		after: `(l.created_at,l.id) < (SELECT c.created_at,c.id FROM crm_leads c WHERE c.company_id=$1 AND c.id=%s::uuid)`,
	},
	"name": {
		order: `lower(l.display_name),l.id`,
		after: `(lower(l.display_name),l.id) > (SELECT lower(c.display_name),c.id FROM crm_leads c WHERE c.company_id=$1 AND c.id=%s::uuid)`,
	},
}

// ListLeads returns a page of leads in filter.Sort order (priority first by
// default). cursor is the id of the last lead already seen, so the same call
// pages through the list and finds the lead after the one on screen.
func (s *Service) ListLeads(ctx context.Context, session identity.Session, filter LeadFilter, cursor string, limit int) (LeadList, error) {
	if !session.HasPermission("crm.read") {
		return LeadList{}, identity.ErrForbidden
	}
	if limit <= 0 || limit > maxListLimit {
		limit = 50
	}
	where, args, err := leadFilterSQL(session, filter)
	if err != nil {
		return LeadList{}, err
	}
	sortKey := strings.TrimSpace(filter.Sort)
	if sortKey == "" {
		sortKey = "priority"
	}
	sort, ok := leadSorts[sortKey]
	if !ok {
		return LeadList{}, errInvalid("geçersiz sıralama")
	}
	after := strings.TrimSpace(cursor)
	afterSQL := ""
	if after != "" {
		if _, err = uuid.Parse(after); err != nil {
			return LeadList{}, errInvalid("geçersiz sayfa imleci")
		}
		afterSQL = ` AND ` + fmt.Sprintf(sort.after, args.add(after))
	}
	rows, err := s.pool.Query(ctx, leadSelect+` WHERE`+where+afterSQL+`
 ORDER BY `+sort.order+` LIMIT `+args.add(limit+1), args.values...)
	if err != nil {
		return LeadList{}, err
	}
	defer rows.Close()
	result := LeadList{Items: []Lead{}}
	for rows.Next() {
		item, err := scanLead(rows)
		if err != nil {
			return LeadList{}, err
		}
		result.Items = append(result.Items, item)
	}
	if err = rows.Err(); err != nil {
		return LeadList{}, err
	}
	if len(result.Items) > limit {
		result.Items = result.Items[:limit]
		result.NextCursor = result.Items[limit-1].ID
	}
	return result, nil
}

func (s *Service) GetLead(ctx context.Context, session identity.Session, id string) (Lead, error) {
	if !session.HasPermission("crm.read") {
		return Lead{}, identity.ErrForbidden
	}
	return s.getLead(ctx, s.pool, session, id)
}

func (s *Service) getLead(ctx context.Context, q reader, session identity.Session, id string) (Lead, error) {
	if _, err := uuid.Parse(strings.TrimSpace(id)); err != nil {
		return Lead{}, ErrLeadNotFound
	}
	item, err := scanLead(q.QueryRow(ctx, leadSelect+` WHERE l.company_id=$1`+leadVisible+` AND l.id=$4::uuid`,
		session.CurrentCompanyID, canSeeAll(session), session.User.ID, strings.TrimSpace(id)))
	if errors.Is(err, pgx.ErrNoRows) {
		return Lead{}, ErrLeadNotFound
	}
	return item, err
}

// FindDuplicates returns leads sharing the phone number, ignoring excludeID.
// Visibility is deliberately not applied: a rep must learn that a number is
// already somebody else's lead, or asked not to be called, before calling it.
func (s *Service) FindDuplicates(ctx context.Context, session identity.Session, phone, excludeID string) ([]LeadSummary, error) {
	if !session.HasPermission("crm.read") {
		return nil, identity.ErrForbidden
	}
	return s.findDuplicates(ctx, s.pool, session.CurrentCompanyID, NormalizePhone(phone), excludeID)
}

func (s *Service) findDuplicates(ctx context.Context, q reader, companyID, normalized, excludeID string) ([]LeadSummary, error) {
	matches := []LeadSummary{}
	if len(normalized) < 7 {
		return matches, nil
	}
	rows, err := q.Query(ctx, `SELECT l.id::text,l.display_name,l.phone,l.status,l.do_not_call,COALESCE(u.display_name,'')
 FROM crm_leads l LEFT JOIN users u ON u.id=l.owner_user_id
 WHERE l.company_id=$1 AND l.phone_normalized=$2 AND ($3='' OR l.id<>NULLIF($3,'')::uuid)
 ORDER BY l.do_not_call DESC,l.created_at LIMIT $4`, companyID, normalized, strings.TrimSpace(excludeID), maxDuplicates)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var m LeadSummary
		if err := rows.Scan(&m.ID, &m.DisplayName, &m.Phone, &m.Status, &m.DoNotCall, &m.OwnerName); err != nil {
			return nil, err
		}
		matches = append(matches, m)
	}
	return matches, rows.Err()
}

func (s *Service) CreateLead(ctx context.Context, session identity.Session, input LeadInput, meta identity.RequestMeta) (Lead, error) {
	if !session.HasPermission("crm.edit") {
		return Lead{}, identity.ErrForbidden
	}
	normalizeLead(&input)
	if input.Status == "" {
		input.Status = StatusNew
	}
	if err := validateLead(input); err != nil {
		return Lead{}, err
	}
	nextAction, err := optionalTime(input.NextActionAt, "geçersiz sonraki işlem zamanı")
	if err != nil {
		return Lead{}, err
	}
	// A new lead belongs to whoever created it unless a manager assigns it.
	if input.OwnerUserID == "" && !session.HasPermission("crm.manage") {
		input.OwnerUserID = session.User.ID
	}
	if err = checkOwnerChange(session, nil, input.OwnerUserID); err != nil {
		return Lead{}, err
	}
	normalized := NormalizePhone(input.Phone)
	id := uuid.NewString()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Lead{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if !input.AllowDuplicate {
		matches, err := s.findDuplicates(ctx, tx, session.CurrentCompanyID, normalized, "")
		if err != nil {
			return Lead{}, err
		}
		if len(matches) > 0 {
			return Lead{}, &DuplicateError{Matches: matches}
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO crm_leads(id,company_id,display_name,contact_name,contact_title,phone,phone_normalized,phone2,
 email,city,district,sector,source,notes,status,owner_user_id,next_action_at,privacy_notice_at,created_by,address,website,priority)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,NULLIF($16,'')::uuid,$17,CASE WHEN $18 THEN now() END,$19,$20,$21,$22)`,
		id, session.CurrentCompanyID, input.DisplayName, input.ContactName, input.ContactTitle, input.Phone, normalized,
		input.Phone2, input.Email, input.City, input.District, input.Sector, input.Source, input.Notes, input.Status,
		input.OwnerUserID, nextAction, input.PrivacyNotice, session.User.ID, input.Address, input.Website, input.Priority)
	if err != nil {
		return Lead{}, mapConstraint(err)
	}
	if err = writeAudit(ctx, tx, session, meta, "CRM_LEAD_CREATED", "crm_lead", id, map[string]any{"display_name": input.DisplayName}); err != nil {
		return Lead{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Lead{}, err
	}
	return s.GetLead(ctx, session, id)
}

func (s *Service) UpdateLead(ctx context.Context, session identity.Session, id string, version int64, input LeadInput, meta identity.RequestMeta) (Lead, error) {
	if !session.HasPermission("crm.edit") {
		return Lead{}, identity.ErrForbidden
	}
	normalizeLead(&input)
	nextAction, err := optionalTime(input.NextActionAt, "geçersiz sonraki işlem zamanı")
	if err != nil {
		return Lead{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Lead{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	current, err := s.getLead(ctx, tx, session, id)
	if err != nil {
		return Lead{}, err
	}
	// A converted lead keeps its status; only its details may be corrected.
	checked := input
	if current.Status == StatusConverted {
		input.Status, checked.Status = StatusConverted, StatusNew
	}
	if err = validateLead(checked); err != nil {
		return Lead{}, err
	}
	if err = checkOwnerChange(session, current.OwnerUserID, input.OwnerUserID); err != nil {
		return Lead{}, err
	}
	normalized := NormalizePhone(input.Phone)
	if !input.AllowDuplicate && normalized != NormalizePhone(current.Phone) {
		matches, err := s.findDuplicates(ctx, tx, session.CurrentCompanyID, normalized, current.ID)
		if err != nil {
			return Lead{}, err
		}
		if len(matches) > 0 {
			return Lead{}, &DuplicateError{Matches: matches}
		}
	}
	tag, err := tx.Exec(ctx, `UPDATE crm_leads SET display_name=$4,contact_name=$5,contact_title=$6,phone=$7,phone_normalized=$8,
 phone2=$9,email=$10,city=$11,district=$12,sector=$13,source=$14,notes=$15,status=$16,owner_user_id=NULLIF($17,'')::uuid,
 next_action_at=$18,privacy_notice_at=CASE WHEN $19 THEN COALESCE(privacy_notice_at,now()) END,
 address=$20,website=$21,priority=$22,
 updated_at=now(),version=version+1
 WHERE company_id=$1 AND id=$2::uuid AND version=$3`,
		session.CurrentCompanyID, current.ID, version, input.DisplayName, input.ContactName, input.ContactTitle, input.Phone,
		normalized, input.Phone2, input.Email, input.City, input.District, input.Sector, input.Source, input.Notes,
		input.Status, input.OwnerUserID, nextAction, input.PrivacyNotice, input.Address, input.Website, input.Priority)
	if err != nil {
		return Lead{}, mapConstraint(err)
	}
	if tag.RowsAffected() == 0 {
		return Lead{}, identity.ErrConflict
	}
	if err = writeAudit(ctx, tx, session, meta, "CRM_LEAD_UPDATED", "crm_lead", current.ID, map[string]any{"status": input.Status}); err != nil {
		return Lead{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Lead{}, err
	}
	return s.GetLead(ctx, session, current.ID)
}

// SetDoNotCall marks a lead as not to be called, or lifts the mark. Lifting it
// needs crm.manage: the request usually came from the person called.
func (s *Service) SetDoNotCall(ctx context.Context, session identity.Session, id string, doNotCall bool, reason string, meta identity.RequestMeta) (Lead, error) {
	if !session.HasPermission("crm.edit") || (!doNotCall && !session.HasPermission("crm.manage")) {
		return Lead{}, identity.ErrForbidden
	}
	reason = strings.TrimSpace(reason)
	if len([]rune(reason)) > 500 {
		return Lead{}, errInvalid("açıklama en fazla 500 karakter olabilir")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Lead{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	current, err := s.getLead(ctx, tx, session, id)
	if err != nil {
		return Lead{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE crm_leads SET do_not_call=$3,
 do_not_call_at=CASE WHEN $3 THEN COALESCE(do_not_call_at,now()) END,
 do_not_call_reason=CASE WHEN $3 THEN $4 ELSE '' END,
 next_action_at=CASE WHEN $3 THEN NULL ELSE next_action_at END,
 updated_at=now(),version=version+1
 WHERE company_id=$1 AND id=$2::uuid`, session.CurrentCompanyID, current.ID, doNotCall, reason); err != nil {
		return Lead{}, err
	}
	if doNotCall {
		if err = closeLeadItems(ctx, tx, session.CurrentCompanyID, current.ID, ItemSkipped); err != nil {
			return Lead{}, err
		}
	}
	event := "CRM_LEAD_DO_NOT_CALL_SET"
	if !doNotCall {
		event = "CRM_LEAD_DO_NOT_CALL_CLEARED"
	}
	if err = writeAudit(ctx, tx, session, meta, event, "crm_lead", current.ID, map[string]any{"reason": reason}); err != nil {
		return Lead{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Lead{}, err
	}
	return s.GetLead(ctx, session, current.ID)
}

// LogCall records a call to a lead and applies its outcome in one
// transaction: the lead row is locked so two reps finishing a call at the same
// moment cannot lose an attempt. A callback outcome also opens a follow-up task
// for the caller.
func (s *Service) LogCall(ctx context.Context, session identity.Session, leadID string, input CallInput, meta identity.RequestMeta) (CallResult, error) {
	if !session.HasPermission("crm.edit") {
		return CallResult{}, identity.ErrForbidden
	}
	input.Outcome = strings.ToUpper(strings.TrimSpace(input.Outcome))
	input.Note = strings.TrimSpace(input.Note)
	if len([]rune(input.Note)) > 4000 {
		return CallResult{}, errInvalid("not en fazla 4000 karakter olabilir")
	}
	if input.DurationSeconds != nil && (*input.DurationSeconds < 0 || *input.DurationSeconds > 86400) {
		return CallResult{}, errInvalid("geçersiz görüşme süresi")
	}
	if input.RemindMinutes < 0 || input.RemindMinutes > 10080 {
		return CallResult{}, errInvalid("geçersiz hatırlatma süresi")
	}
	callbackAt, err := optionalTime(input.CallbackAt, "geçersiz geri arama zamanı")
	if err != nil {
		return CallResult{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CallResult{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	lead, err := s.getLead(ctx, tx, session, leadID)
	if err != nil {
		return CallResult{}, err
	}
	state := LeadCallState{}
	err = tx.QueryRow(ctx, `SELECT status,do_not_call,call_attempts,last_call_at,last_call_outcome,next_action_at
 FROM crm_leads WHERE company_id=$1 AND id=$2::uuid FOR UPDATE`, session.CurrentCompanyID, lead.ID).
		Scan(&state.Status, &state.DoNotCall, &state.CallAttempts, &state.LastCallAt, &state.LastOutcome, &state.NextActionAt)
	if err != nil {
		return CallResult{}, err
	}
	now := time.Now().UTC()
	next, err := ApplyCall(state, input.Outcome, now, callbackAt)
	if err != nil {
		return CallResult{}, err
	}
	reason := ""
	if input.Outcome == OutcomeDoNotCall {
		reason = input.Note
		if len([]rune(reason)) > 500 {
			reason = string([]rune(reason)[:500])
		}
	}
	// An unassigned lead becomes the caller's once they have worked it.
	if _, err = tx.Exec(ctx, `UPDATE crm_leads SET status=$3,do_not_call=$4,
 do_not_call_at=CASE WHEN $4 THEN COALESCE(do_not_call_at,now()) END,
 do_not_call_reason=CASE WHEN $4 AND NOT do_not_call THEN $5 ELSE do_not_call_reason END,
 call_attempts=$6,last_call_at=$7,last_call_outcome=$8,next_action_at=$9,
 owner_user_id=COALESCE(owner_user_id,$10::uuid),updated_at=now(),version=version+1
 WHERE company_id=$1 AND id=$2::uuid`,
		session.CurrentCompanyID, lead.ID, next.Status, next.DoNotCall, reason, next.CallAttempts,
		next.LastCallAt, next.LastOutcome, next.NextActionAt, session.User.ID); err != nil {
		return CallResult{}, err
	}
	listID, err := advanceCallListItem(ctx, tx, session, lead.ID, strings.TrimSpace(input.CallListItemID), input.Outcome, now, callbackAt)
	if err != nil {
		return CallResult{}, err
	}
	if next.DoNotCall {
		if err = closeLeadItems(ctx, tx, session.CurrentCompanyID, lead.ID, ItemSkipped); err != nil {
			return CallResult{}, err
		}
	}
	activityID := uuid.NewString()
	if _, err = tx.Exec(ctx, `INSERT INTO crm_activities(id,company_id,lead_id,kind,body,call_outcome,duration_seconds,occurred_at,created_by,call_list_id)
 VALUES($1,$2,$3::uuid,'CALL',$4,$5,$6,$7,$8,NULLIF($9,'')::uuid)`,
		activityID, session.CurrentCompanyID, lead.ID, input.Note, input.Outcome, input.DurationSeconds, now, session.User.ID, listID); err != nil {
		return CallResult{}, mapConstraint(err)
	}
	var taskID string
	if input.Outcome == OutcomeCallback && callbackAt != nil {
		taskID = uuid.NewString()
		if _, err = tx.Exec(ctx, `INSERT INTO crm_activities(id,company_id,lead_id,kind,subject,body,due_at,remind_minutes,assigned_user_id,created_by)
 VALUES($1,$2,$3::uuid,'TASK',$4,$5,$6,$7,$8::uuid,$8::uuid)`,
			taskID, session.CurrentCompanyID, lead.ID, truncate("Geri ara: "+lead.DisplayName, 200), input.Note,
			*callbackAt, input.RemindMinutes, session.User.ID); err != nil {
			return CallResult{}, mapConstraint(err)
		}
	}
	if err = writeAudit(ctx, tx, session, meta, "CRM_CALL_LOGGED", "crm_lead", lead.ID, map[string]any{"outcome": input.Outcome}); err != nil {
		return CallResult{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return CallResult{}, err
	}
	result := CallResult{}
	if result.Lead, err = s.GetLead(ctx, session, lead.ID); err != nil {
		return CallResult{}, err
	}
	if result.Activity, err = s.getActivity(ctx, session, activityID); err != nil {
		return CallResult{}, err
	}
	if taskID != "" {
		task, err := s.getActivity(ctx, session, taskID)
		if err != nil {
			return CallResult{}, err
		}
		result.Task = &task
	}
	return result, nil
}

// --- Activities --------------------------------------------------------------

const activitySelect = `SELECT a.id::text,a.lead_id::text,COALESCE(l.display_name,''),a.party_id::text,COALESCE(p.display_name,''),a.opportunity_id::text,
 a.kind,a.subject,a.body,a.call_outcome,a.duration_seconds,a.occurred_at,a.due_at,a.remind_minutes,a.done_at,
 a.assigned_user_id::text,COALESCE(au.display_name,''),a.created_by::text,COALESCE(cu.display_name,''),a.created_at,a.version
 FROM crm_activities a
 LEFT JOIN crm_leads l ON l.company_id=a.company_id AND l.id=a.lead_id
 LEFT JOIN parties p ON p.company_id=a.company_id AND p.id=a.party_id
 LEFT JOIN users au ON au.id=a.assigned_user_id
 LEFT JOIN users cu ON cu.id=a.created_by `

// activityVisible hides activities on leads the caller cannot see. Activities
// on a cari are visible to every crm.read holder, like the cari itself.
const activityVisible = ` AND (a.lead_id IS NULL OR $2::boolean OR l.owner_user_id IS NULL OR l.owner_user_id=$3::uuid)`

// ListActivities returns the timeline of a lead (target "lead") or a cari
// (target "party"), newest first.
func (s *Service) ListActivities(ctx context.Context, session identity.Session, target, targetID string, limit int) ([]Activity, error) {
	if !session.HasPermission("crm.read") {
		return nil, identity.ErrForbidden
	}
	if limit <= 0 || limit > maxActivityLimit {
		limit = maxActivityLimit
	}
	if _, err := targetColumn(target); err != nil {
		return nil, err
	}
	if _, err := uuid.Parse(strings.TrimSpace(targetID)); err != nil {
		return nil, notFoundFor(target)
	}
	var condition string
	switch target {
	case "lead":
		if _, err := s.getLead(ctx, s.pool, session, targetID); err != nil {
			return nil, err
		}
		condition = `a.lead_id=$4::uuid`
	case "opportunity":
		if _, err := s.getOpportunity(ctx, s.pool, session, targetID); err != nil {
			return nil, err
		}
		condition = `a.opportunity_id=$4::uuid`
	default:
		// A cari's history includes what was done while it was still a lead.
		condition = `(a.party_id=$4::uuid OR a.lead_id IN (SELECT cl.id FROM crm_leads cl WHERE cl.company_id=$1 AND cl.converted_party_id=$4::uuid))`
	}
	rows, err := s.pool.Query(ctx, activitySelect+` WHERE a.company_id=$1`+activityVisible+` AND `+condition+`
 ORDER BY COALESCE(a.due_at,a.occurred_at) DESC,a.created_at DESC LIMIT $5`,
		session.CurrentCompanyID, canSeeAll(session), session.User.ID, strings.TrimSpace(targetID), limit)
	if err != nil {
		return nil, err
	}
	return collectActivities(rows)
}

func (s *Service) CreateActivity(ctx context.Context, session identity.Session, target, targetID string, input ActivityInput, meta identity.RequestMeta) (Activity, error) {
	if !session.HasPermission("crm.edit") {
		return Activity{}, identity.ErrForbidden
	}
	if _, err := targetColumn(target); err != nil {
		return Activity{}, err
	}
	input.Kind = strings.ToUpper(strings.TrimSpace(input.Kind))
	input.Subject = strings.TrimSpace(input.Subject)
	input.Body = strings.TrimSpace(input.Body)
	input.CallOutcome = strings.ToUpper(strings.TrimSpace(input.CallOutcome))
	input.AssignedUserID = strings.TrimSpace(input.AssignedUserID)
	switch input.Kind {
	case "NOTE", "MEETING", "EMAIL", "TASK":
		input.CallOutcome = ""
	case "CALL":
		if !ValidOutcome(input.CallOutcome) {
			return Activity{}, errInvalid("geçersiz arama sonucu")
		}
	default:
		return Activity{}, errInvalid("geçersiz kayıt türü")
	}
	if len([]rune(input.Subject)) > 200 || len([]rune(input.Body)) > 4000 {
		return Activity{}, errInvalid("konu en fazla 200, açıklama en fazla 4000 karakter olabilir")
	}
	if input.Kind != "TASK" && input.Subject == "" && input.Body == "" {
		return Activity{}, errInvalid("konu ya da açıklama girin")
	}
	if input.DurationSeconds != nil && (*input.DurationSeconds < 0 || *input.DurationSeconds > 86400) {
		return Activity{}, errInvalid("geçersiz görüşme süresi")
	}
	if input.RemindMinutes < 0 || input.RemindMinutes > 10080 {
		return Activity{}, errInvalid("geçersiz hatırlatma süresi")
	}
	occurredAt, err := optionalTime(input.OccurredAt, "geçersiz tarih")
	if err != nil {
		return Activity{}, err
	}
	if occurredAt == nil {
		now := time.Now().UTC()
		occurredAt = &now
	}
	var dueAt *time.Time
	assigned := ""
	if input.Kind == "TASK" {
		if input.Subject == "" {
			return Activity{}, errInvalid("görev için konu girin")
		}
		if dueAt, err = optionalTime(input.DueAt, "geçersiz görev tarihi"); err != nil {
			return Activity{}, err
		}
		if dueAt == nil {
			return Activity{}, errInvalid("görev için tarih seçin")
		}
		assigned = input.AssignedUserID
		if assigned == "" {
			assigned = session.User.ID
		}
		if assigned != session.User.ID && !session.HasPermission("crm.manage") {
			return Activity{}, identity.ErrForbidden
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Activity{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var leadID, partyID, opportunityID string
	switch target {
	case "lead":
		lead, err := s.getLead(ctx, tx, session, targetID)
		if err != nil {
			return Activity{}, err
		}
		leadID = lead.ID
	case "opportunity":
		opportunity, err := s.getOpportunity(ctx, tx, session, targetID)
		if err != nil {
			return Activity{}, err
		}
		opportunityID = opportunity.ID
		if opportunity.LeadID != nil {
			leadID = *opportunity.LeadID
		} else if opportunity.PartyID != nil {
			partyID = *opportunity.PartyID
		}
	default:
		partyID = strings.TrimSpace(targetID)
		if err = s.ensureParty(ctx, tx, session, partyID); err != nil {
			return Activity{}, err
		}
	}
	if input.Kind == "CALL" && leadID != "" {
		return Activity{}, errInvalid("adayla yapılan arama, arama sonucu ekranından kaydedilir")
	}
	id := uuid.NewString()
	_, err = tx.Exec(ctx, `INSERT INTO crm_activities(id,company_id,lead_id,party_id,opportunity_id,kind,subject,body,call_outcome,duration_seconds,
 occurred_at,due_at,remind_minutes,assigned_user_id,created_by)
 VALUES($1,$2,NULLIF($3,'')::uuid,NULLIF($14,'')::uuid,NULLIF($15,'')::uuid,$4,$5,$6,NULLIF($7,''),$8,$9,$10,$11,NULLIF($12,'')::uuid,$13)`,
		id, session.CurrentCompanyID, leadID, input.Kind, input.Subject, input.Body, input.CallOutcome,
		input.DurationSeconds, *occurredAt, dueAt, input.RemindMinutes, assigned, session.User.ID, partyID, opportunityID)
	if err != nil {
		return Activity{}, mapConstraint(err)
	}
	if leadID != "" {
		// Any recorded contact moves a fresh lead out of NEW.
		if input.Kind == "MEETING" || input.Kind == "EMAIL" {
			if _, err = tx.Exec(ctx, `UPDATE crm_leads SET status='CONTACTED',updated_at=now(),version=version+1
 WHERE company_id=$1 AND id=$2::uuid AND status='NEW'`, session.CurrentCompanyID, leadID); err != nil {
				return Activity{}, err
			}
		}
	}
	if err = writeAudit(ctx, tx, session, meta, "CRM_ACTIVITY_CREATED", "crm_activity", id, map[string]any{"kind": input.Kind, "target": target}); err != nil {
		return Activity{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Activity{}, err
	}
	return s.getActivity(ctx, session, id)
}

// ListTasks returns follow-up tasks, soonest due first (most recently done
// first for the done list).
func (s *Service) ListTasks(ctx context.Context, session identity.Session, filter TaskFilter, limit int) ([]Activity, error) {
	if !session.HasPermission("crm.read") {
		return nil, identity.ErrForbidden
	}
	if limit <= 0 || limit > maxActivityLimit {
		limit = maxActivityLimit
	}
	scope := strings.TrimSpace(filter.Scope)
	if scope == "" {
		scope = "mine"
	}
	if scope != "mine" && scope != "all" {
		return nil, errInvalid("geçersiz görev kapsamı")
	}
	if scope == "all" && !canSeeAll(session) {
		return nil, identity.ErrForbidden
	}
	state := strings.TrimSpace(filter.State)
	if state == "" {
		state = "open"
	}
	var condition, order string
	switch state {
	case "open":
		condition, order = `a.done_at IS NULL`, `a.due_at ASC`
	case "overdue":
		condition, order = `a.done_at IS NULL AND a.due_at < now()`, `a.due_at ASC`
	case "today":
		condition, order = `a.done_at IS NULL AND a.due_at < date_trunc('day', now()) + interval '1 day'`, `a.due_at ASC`
	case "done":
		condition, order = `a.done_at IS NOT NULL`, `a.done_at DESC`
	default:
		return nil, errInvalid("geçersiz görev durumu")
	}
	rows, err := s.pool.Query(ctx, activitySelect+` WHERE a.company_id=$1`+activityVisible+` AND a.kind='TASK'
 AND ($4::boolean OR a.assigned_user_id=$3::uuid) AND `+condition+`
 ORDER BY `+order+`,a.created_at LIMIT $5`,
		session.CurrentCompanyID, canSeeAll(session), session.User.ID, scope == "all", limit)
	if err != nil {
		return nil, err
	}
	return collectActivities(rows)
}

// SetTaskDone completes a task or reopens it. The assignee may always do so;
// anybody else needs crm.manage.
func (s *Service) SetTaskDone(ctx context.Context, session identity.Session, id string, done bool, meta identity.RequestMeta) (Activity, error) {
	if !session.HasPermission("crm.edit") {
		return Activity{}, identity.ErrForbidden
	}
	task, err := s.getActivity(ctx, session, id)
	if err != nil {
		return Activity{}, err
	}
	if task.Kind != "TASK" {
		return Activity{}, ErrActivityNotFound
	}
	if (task.AssignedUserID == nil || *task.AssignedUserID != session.User.ID) && !session.HasPermission("crm.manage") {
		return Activity{}, identity.ErrForbidden
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Activity{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err = tx.Exec(ctx, `UPDATE crm_activities SET done_at=CASE WHEN $3 THEN COALESCE(done_at,now()) END,
 updated_at=now(),version=version+1 WHERE company_id=$1 AND id=$2::uuid`, session.CurrentCompanyID, task.ID, done); err != nil {
		return Activity{}, err
	}
	if err = writeAudit(ctx, tx, session, meta, "CRM_TASK_DONE_CHANGED", "crm_activity", task.ID, map[string]any{"done": done}); err != nil {
		return Activity{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Activity{}, err
	}
	return s.getActivity(ctx, session, task.ID)
}

func (s *Service) getActivity(ctx context.Context, session identity.Session, id string) (Activity, error) {
	if _, err := uuid.Parse(strings.TrimSpace(id)); err != nil {
		return Activity{}, ErrActivityNotFound
	}
	item, err := scanActivity(s.pool.QueryRow(ctx, activitySelect+` WHERE a.company_id=$1`+activityVisible+` AND a.id=$4::uuid`,
		session.CurrentCompanyID, canSeeAll(session), session.User.ID, strings.TrimSpace(id)))
	if errors.Is(err, pgx.ErrNoRows) {
		return Activity{}, ErrActivityNotFound
	}
	return item, err
}

// ListMembers returns the company's active users, for picking an owner or an
// assignee. It needs only crm.read: a sales rep must be able to hand a task to
// a colleague's name without the user-administration permission.
func (s *Service) ListMembers(ctx context.Context, session identity.Session) ([]Member, error) {
	if !session.HasPermission("crm.read") {
		return nil, identity.ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `SELECT u.id::text,u.display_name FROM company_memberships m JOIN users u ON u.id=m.user_id
 WHERE m.company_id=$1 AND m.is_active ORDER BY u.display_name,u.id`, session.CurrentCompanyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	members := []Member{}
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.UserID, &m.DisplayName); err != nil {
			return nil, err
		}
		members = append(members, m)
	}
	return members, rows.Err()
}

func (s *Service) ensureParty(ctx context.Context, q reader, session identity.Session, partyID string) error {
	if _, err := uuid.Parse(partyID); err != nil {
		return ErrPartyNotFound
	}
	var exists bool
	if err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM parties WHERE company_id=$1 AND id=$2::uuid)`,
		session.CurrentCompanyID, partyID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrPartyNotFound
	}
	return nil
}

// --- helpers -----------------------------------------------------------------

type rowScanner interface{ Scan(...any) error }

func scanLead(row rowScanner) (Lead, error) {
	var l Lead
	err := row.Scan(&l.ID, &l.DisplayName, &l.ContactName, &l.ContactTitle, &l.Phone, &l.Phone2, &l.Email,
		&l.City, &l.District, &l.Sector, &l.Source, &l.Notes, &l.Address, &l.Website, &l.Priority, &l.Status, &l.OwnerUserID, &l.OwnerName,
		&l.NextActionAt, &l.DoNotCall, &l.DoNotCallReason, &l.DoNotCallAt, &l.PrivacyNoticeAt,
		&l.CallAttempts, &l.LastCallAt, &l.LastCallOutcome, &l.ConvertedPartyID, &l.ConvertedAt,
		&l.CreatedAt, &l.UpdatedAt, &l.Version)
	return l, err
}

func scanActivity(row rowScanner) (Activity, error) {
	var a Activity
	err := row.Scan(&a.ID, &a.LeadID, &a.LeadName, &a.PartyID, &a.PartyName, &a.OpportunityID, &a.Kind, &a.Subject, &a.Body,
		&a.CallOutcome, &a.DurationSeconds, &a.OccurredAt, &a.DueAt, &a.RemindMinutes, &a.DoneAt,
		&a.AssignedUserID, &a.AssignedName, &a.CreatedBy, &a.CreatedByName, &a.CreatedAt, &a.Version)
	return a, err
}

func collectActivities(rows pgx.Rows) ([]Activity, error) {
	defer rows.Close()
	items := []Activity{}
	for rows.Next() {
		item, err := scanActivity(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func targetColumn(target string) (string, error) {
	switch target {
	case "lead":
		return "lead_id", nil
	case "party":
		return "party_id", nil
	case "opportunity":
		return "opportunity_id", nil
	}
	return "", errInvalid("geçersiz kayıt hedefi")
}

func notFoundFor(target string) error {
	switch target {
	case "party":
		return ErrPartyNotFound
	case "opportunity":
		return ErrOpportunityNotFound
	}
	return ErrLeadNotFound
}

// checkOwnerChange allows taking an unassigned lead or keeping its owner; any
// other assignment is a manager's call.
func checkOwnerChange(session identity.Session, current *string, next string) error {
	currentID := ""
	if current != nil {
		currentID = *current
	}
	if next == currentID || session.HasPermission("crm.manage") {
		return nil
	}
	if next == session.User.ID && currentID == "" {
		return nil
	}
	return identity.ErrForbidden
}

func normalizeLead(input *LeadInput) {
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.ContactName = strings.TrimSpace(input.ContactName)
	input.ContactTitle = strings.TrimSpace(input.ContactTitle)
	input.Phone = strings.TrimSpace(input.Phone)
	input.Phone2 = strings.TrimSpace(input.Phone2)
	input.Email = strings.TrimSpace(input.Email)
	input.City = strings.TrimSpace(input.City)
	input.District = strings.TrimSpace(input.District)
	input.Sector = strings.TrimSpace(input.Sector)
	input.Source = strings.TrimSpace(input.Source)
	input.Notes = strings.TrimSpace(input.Notes)
	input.Address = strings.TrimSpace(input.Address)
	input.Website = strings.TrimSpace(input.Website)
	input.Priority = strings.ToUpper(strings.TrimSpace(input.Priority))
	input.Status = strings.ToUpper(strings.TrimSpace(input.Status))
	input.OwnerUserID = strings.TrimSpace(input.OwnerUserID)
	input.NextActionAt = strings.TrimSpace(input.NextActionAt)
}

func validateLead(input LeadInput) error {
	if input.DisplayName == "" || len([]rune(input.DisplayName)) > 200 {
		return errInvalid("aday adı 1-200 karakter olmalıdır")
	}
	if input.Phone == "" && input.Phone2 == "" && input.Email == "" {
		return errInvalid("telefon ya da e-posta girin")
	}
	if input.Email != "" {
		if _, err := mail.ParseAddress(input.Email); err != nil {
			return errInvalid("geçersiz e-posta adresi")
		}
	}
	if !editableStatuses[input.Status] {
		return errInvalid("geçersiz aday durumu")
	}
	if !validPriority(input.Priority) {
		return errInvalid("öncelik A ile E arasında bir harf olmalıdır")
	}
	if input.OwnerUserID != "" {
		if _, err := uuid.Parse(input.OwnerUserID); err != nil {
			return ErrOwnerNotMember
		}
	}
	for _, field := range []struct {
		value string
		max   int
	}{
		{input.ContactName, 200}, {input.ContactTitle, 120}, {input.Phone, 40}, {input.Phone2, 40},
		{input.Email, 254}, {input.City, 80}, {input.District, 80}, {input.Sector, 120},
		{input.Source, 120}, {input.Notes, 4000}, {input.Address, 500}, {input.Website, 200},
	} {
		if len([]rune(field.value)) > field.max {
			return errInvalid("alanlardan biri izin verilen uzunluğu aşıyor")
		}
	}
	return nil
}

func optionalTime(value, message string) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.Parse(layout, value); err == nil {
			t = t.UTC()
			return &t, nil
		}
	}
	return nil, errInvalid(message)
}

func truncate(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max])
}

func mapConstraint(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	switch pgErr.Code {
	case "23503":
		switch {
		case strings.Contains(pgErr.ConstraintName, "owner_user_id"), strings.Contains(pgErr.ConstraintName, "assigned_user_id"):
			return ErrOwnerNotMember
		case strings.Contains(pgErr.ConstraintName, "party_id"):
			return ErrPartyNotFound
		case strings.Contains(pgErr.ConstraintName, "lead_id"):
			return ErrLeadNotFound
		case strings.Contains(pgErr.ConstraintName, "opportunity_id"):
			return ErrOpportunityNotFound
		case strings.Contains(pgErr.ConstraintName, "stage_id"):
			return ErrStageNotFound
		case strings.Contains(pgErr.ConstraintName, "script_id"):
			return ErrScriptNotFound
		}
	case "23505":
		return errInvalid("aynı adla bir kayıt zaten var")
	case "23514":
		return errInvalid("kayıt bilgileri geçersiz")
	}
	return err
}

func writeAudit(ctx context.Context, tx pgx.Tx, session identity.Session, meta identity.RequestMeta, eventType, entityType, entityID string, details map[string]any) error {
	detailBytes, _ := json.Marshal(details)
	_, err := tx.Exec(ctx, `INSERT INTO security_audit_events(id,company_id,actor_user_id,event_type,entity_type,entity_id,details,trace_id,source_ip,user_agent)
 VALUES($1,$2,$3,$4,$5,NULLIF($6,'')::uuid,$7,$8,$9,$10)`,
		uuid.NewString(), session.CurrentCompanyID, session.User.ID, eventType, entityType, entityID, detailBytes, meta.TraceID, meta.IP, meta.UserAgent)
	return err
}

// validPriority accepts an empty priority or one letter from A (hottest) to E.
func validPriority(priority string) bool {
	return priority == "" || (len(priority) == 1 && priority[0] >= 'A' && priority[0] <= 'E')
}
