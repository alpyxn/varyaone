package crm

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrCallListNotFound = errors.New("CRM_CALL_LIST_NOT_FOUND")
	ErrCallItemNotFound = errors.New("CRM_CALL_ITEM_NOT_FOUND")
	ErrScriptNotFound   = errors.New("CRM_SCRIPT_NOT_FOUND")
)

// claimTTL is how long a rep holds the lead they opened from a list, as a
// PostgreSQL interval. A claim left behind (closed tab, lunch) expires on its
// own and the lead returns to the queue.
const claimTTL = "15 minutes"

// maxListAdd bounds one "add the filtered leads" request.
const maxListAdd = 10000

type CallScript struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Body      string    `json:"body"`
	IsActive  bool      `json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Version   int64     `json:"version"`
}

type CallScriptInput struct {
	Name     string `json:"name"`
	Body     string `json:"body"`
	IsActive *bool  `json:"is_active"`
}

type CallList struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	ScriptID     *string   `json:"script_id,omitempty"`
	ScriptName   string    `json:"script_name"`
	RetryMinutes int       `json:"retry_minutes"`
	MaxAttempts  int       `json:"max_attempts"`
	IsActive     bool      `json:"is_active"`
	Total        int       `json:"total"`
	Due          int       `json:"due"`
	Waiting      int       `json:"waiting"`
	Done         int       `json:"done"`
	Skipped      int       `json:"skipped"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	Version      int64     `json:"version"`
}

type CallListInput struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	ScriptID     string `json:"script_id"`
	RetryMinutes int    `json:"retry_minutes"`
	MaxAttempts  int    `json:"max_attempts"`
	IsActive     *bool  `json:"is_active"`
}

type CallListItem struct {
	ID           string     `json:"id"`
	ListID       string     `json:"list_id"`
	LeadID       string     `json:"lead_id"`
	LeadName     string     `json:"lead_name"`
	ContactName  string     `json:"contact_name"`
	Phone        string     `json:"phone"`
	LeadStatus   string     `json:"lead_status"`
	DoNotCall    bool       `json:"do_not_call"`
	Status       string     `json:"status"`
	Attempts     int        `json:"attempts"`
	AvailableAt  time.Time  `json:"available_at"`
	LastOutcome  *string    `json:"last_outcome,omitempty"`
	LastCalledAt *time.Time `json:"last_called_at,omitempty"`
	ClaimedBy    *string    `json:"claimed_by,omitempty"`
	ClaimedName  string     `json:"claimed_name"`
	Position     int64      `json:"position"`
}

// NextCall is what the call screen shows: the claimed item, its lead, the
// list's script and how many leads are still waiting to be called now.
type NextCall struct {
	List      CallList     `json:"list"`
	Item      CallListItem `json:"item"`
	Lead      Lead         `json:"lead"`
	Script    *CallScript  `json:"script,omitempty"`
	Remaining int          `json:"remaining"`
}

// AddLeadsInput picks leads for a call list: explicit ids, or every lead
// matching Filter. IncludeCalled deliberately takes leads that were already
// called (without a due callback); by default they are left out so nobody is
// called twice by accident. DryRun only counts.
type AddLeadsInput struct {
	LeadIDs       []string
	Filter        *LeadFilter
	IncludeCalled bool
	DryRun        bool
}

// AddItemsResult says how many matching leads went on the list and why the
// others did not.
type AddItemsResult struct {
	Matched       int `json:"matched"`
	Added         int `json:"added"`
	AlreadyInList int `json:"already_in_list"`
	InOtherList   int `json:"in_other_list"`
	CalledBefore  int `json:"called_before"`
	NotCallable   int `json:"not_callable"`
	// Skipped is kept for older clients: everything matched but not added.
	Skipped int `json:"skipped"`
}

// --- Scripts -------------------------------------------------------------

const scriptColumns = `id::text,name,body,is_active,created_at,updated_at,version`

func scanScript(row rowScanner) (CallScript, error) {
	var sc CallScript
	err := row.Scan(&sc.ID, &sc.Name, &sc.Body, &sc.IsActive, &sc.CreatedAt, &sc.UpdatedAt, &sc.Version)
	return sc, err
}

func (s *Service) ListScripts(ctx context.Context, session identity.Session) ([]CallScript, error) {
	if !session.HasPermission("crm.read") {
		return nil, identity.ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `SELECT `+scriptColumns+` FROM crm_call_scripts WHERE company_id=$1 ORDER BY is_active DESC,name,id`, session.CurrentCompanyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []CallScript{}
	for rows.Next() {
		item, err := scanScript(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) getScript(ctx context.Context, q reader, companyID, id string) (CallScript, error) {
	if _, err := uuid.Parse(strings.TrimSpace(id)); err != nil {
		return CallScript{}, ErrScriptNotFound
	}
	item, err := scanScript(q.QueryRow(ctx, `SELECT `+scriptColumns+` FROM crm_call_scripts WHERE company_id=$1 AND id=$2::uuid`, companyID, strings.TrimSpace(id)))
	if errors.Is(err, pgx.ErrNoRows) {
		return CallScript{}, ErrScriptNotFound
	}
	return item, err
}

func validateScript(input *CallScriptInput) error {
	input.Name = strings.TrimSpace(input.Name)
	input.Body = strings.TrimSpace(input.Body)
	if input.Name == "" || len([]rune(input.Name)) > 120 {
		return errInvalid("metin adı 1-120 karakter olmalıdır")
	}
	if len([]rune(input.Body)) > 20000 {
		return errInvalid("metin en fazla 20000 karakter olabilir")
	}
	return nil
}

func (s *Service) CreateScript(ctx context.Context, session identity.Session, input CallScriptInput, meta identity.RequestMeta) (CallScript, error) {
	if !session.HasPermission("crm.manage") {
		return CallScript{}, identity.ErrForbidden
	}
	if err := validateScript(&input); err != nil {
		return CallScript{}, err
	}
	id := uuid.NewString()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CallScript{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err = tx.Exec(ctx, `INSERT INTO crm_call_scripts(id,company_id,name,body,created_by) VALUES($1,$2,$3,$4,$5)`,
		id, session.CurrentCompanyID, input.Name, input.Body, session.User.ID); err != nil {
		return CallScript{}, mapConstraint(err)
	}
	if err = writeAudit(ctx, tx, session, meta, "CRM_SCRIPT_CREATED", "crm_call_script", id, map[string]any{"name": input.Name}); err != nil {
		return CallScript{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return CallScript{}, err
	}
	return s.getScript(ctx, s.pool, session.CurrentCompanyID, id)
}

func (s *Service) UpdateScript(ctx context.Context, session identity.Session, id string, version int64, input CallScriptInput, meta identity.RequestMeta) (CallScript, error) {
	if !session.HasPermission("crm.manage") {
		return CallScript{}, identity.ErrForbidden
	}
	if err := validateScript(&input); err != nil {
		return CallScript{}, err
	}
	current, err := s.getScript(ctx, s.pool, session.CurrentCompanyID, id)
	if err != nil {
		return CallScript{}, err
	}
	active := current.IsActive
	if input.IsActive != nil {
		active = *input.IsActive
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CallScript{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	tag, err := tx.Exec(ctx, `UPDATE crm_call_scripts SET name=$4,body=$5,is_active=$6,updated_at=now(),version=version+1
 WHERE company_id=$1 AND id=$2::uuid AND version=$3`, session.CurrentCompanyID, current.ID, version, input.Name, input.Body, active)
	if err != nil {
		return CallScript{}, mapConstraint(err)
	}
	if tag.RowsAffected() == 0 {
		return CallScript{}, identity.ErrConflict
	}
	if err = writeAudit(ctx, tx, session, meta, "CRM_SCRIPT_UPDATED", "crm_call_script", current.ID, map[string]any{"name": input.Name}); err != nil {
		return CallScript{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return CallScript{}, err
	}
	return s.getScript(ctx, s.pool, session.CurrentCompanyID, current.ID)
}

// --- Lists ---------------------------------------------------------------

// callListSelect counts items per state. "Due" is what a rep can call right
// now; "waiting" is queued for later (retry delay or a promised callback).
const callListSelect = `SELECT c.id::text,c.name,c.description,c.script_id::text,COALESCE(sc.name,''),c.retry_minutes,c.max_attempts,c.is_active,
 COALESCE(st.total,0),COALESCE(st.due,0),COALESCE(st.waiting,0),COALESCE(st.done,0),COALESCE(st.skipped,0),
 c.created_at,c.updated_at,c.version
 FROM crm_call_lists c
 LEFT JOIN crm_call_scripts sc ON sc.company_id=c.company_id AND sc.id=c.script_id
 LEFT JOIN LATERAL (SELECT count(*) AS total,
   count(*) FILTER (WHERE i.status IN ('PENDING','CALLBACK') AND i.available_at<=now()) AS due,
   count(*) FILTER (WHERE i.status IN ('PENDING','CALLBACK') AND i.available_at>now()) AS waiting,
   count(*) FILTER (WHERE i.status='DONE') AS done,
   count(*) FILTER (WHERE i.status='SKIPPED') AS skipped
   FROM crm_call_list_items i WHERE i.company_id=c.company_id AND i.list_id=c.id) st ON true `

func scanCallList(row rowScanner) (CallList, error) {
	var c CallList
	err := row.Scan(&c.ID, &c.Name, &c.Description, &c.ScriptID, &c.ScriptName, &c.RetryMinutes, &c.MaxAttempts, &c.IsActive,
		&c.Total, &c.Due, &c.Waiting, &c.Done, &c.Skipped, &c.CreatedAt, &c.UpdatedAt, &c.Version)
	return c, err
}

func (s *Service) ListCallLists(ctx context.Context, session identity.Session, includeInactive bool) ([]CallList, error) {
	if !session.HasPermission("crm.read") {
		return nil, identity.ErrForbidden
	}
	rows, err := s.pool.Query(ctx, callListSelect+` WHERE c.company_id=$1 AND ($2 OR c.is_active) ORDER BY c.is_active DESC,c.created_at DESC`,
		session.CurrentCompanyID, includeInactive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []CallList{}
	for rows.Next() {
		item, err := scanCallList(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) GetCallList(ctx context.Context, session identity.Session, id string) (CallList, error) {
	if !session.HasPermission("crm.read") {
		return CallList{}, identity.ErrForbidden
	}
	return s.getCallList(ctx, s.pool, session.CurrentCompanyID, id)
}

func (s *Service) getCallList(ctx context.Context, q reader, companyID, id string) (CallList, error) {
	if _, err := uuid.Parse(strings.TrimSpace(id)); err != nil {
		return CallList{}, ErrCallListNotFound
	}
	item, err := scanCallList(q.QueryRow(ctx, callListSelect+` WHERE c.company_id=$1 AND c.id=$2::uuid`, companyID, strings.TrimSpace(id)))
	if errors.Is(err, pgx.ErrNoRows) {
		return CallList{}, ErrCallListNotFound
	}
	return item, err
}

func (s *Service) validateCallList(ctx context.Context, companyID string, input *CallListInput) error {
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	input.ScriptID = strings.TrimSpace(input.ScriptID)
	if input.Name == "" || len([]rune(input.Name)) > 120 {
		return errInvalid("liste adı 1-120 karakter olmalıdır")
	}
	if len([]rune(input.Description)) > 2000 {
		return errInvalid("açıklama en fazla 2000 karakter olabilir")
	}
	if input.RetryMinutes == 0 {
		input.RetryMinutes = 120
	}
	if input.MaxAttempts == 0 {
		input.MaxAttempts = 5
	}
	if input.RetryMinutes < 5 || input.RetryMinutes > 10080 {
		return errInvalid("tekrar deneme süresi 5 dakika ile 7 gün arasında olmalıdır")
	}
	if input.MaxAttempts < 1 || input.MaxAttempts > 20 {
		return errInvalid("deneme sayısı 1-20 arasında olmalıdır")
	}
	if input.ScriptID != "" {
		if _, err := s.getScript(ctx, s.pool, companyID, input.ScriptID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) CreateCallList(ctx context.Context, session identity.Session, input CallListInput, meta identity.RequestMeta) (CallList, error) {
	if !session.HasPermission("crm.manage") {
		return CallList{}, identity.ErrForbidden
	}
	if err := s.validateCallList(ctx, session.CurrentCompanyID, &input); err != nil {
		return CallList{}, err
	}
	id := uuid.NewString()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CallList{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err = tx.Exec(ctx, `INSERT INTO crm_call_lists(id,company_id,name,description,script_id,retry_minutes,max_attempts,created_by)
 VALUES($1,$2,$3,$4,NULLIF($5,'')::uuid,$6,$7,$8)`,
		id, session.CurrentCompanyID, input.Name, input.Description, input.ScriptID, input.RetryMinutes, input.MaxAttempts, session.User.ID); err != nil {
		return CallList{}, mapConstraint(err)
	}
	if err = writeAudit(ctx, tx, session, meta, "CRM_CALL_LIST_CREATED", "crm_call_list", id, map[string]any{"name": input.Name}); err != nil {
		return CallList{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return CallList{}, err
	}
	return s.getCallList(ctx, s.pool, session.CurrentCompanyID, id)
}

func (s *Service) UpdateCallList(ctx context.Context, session identity.Session, id string, version int64, input CallListInput, meta identity.RequestMeta) (CallList, error) {
	if !session.HasPermission("crm.manage") {
		return CallList{}, identity.ErrForbidden
	}
	current, err := s.getCallList(ctx, s.pool, session.CurrentCompanyID, id)
	if err != nil {
		return CallList{}, err
	}
	if err = s.validateCallList(ctx, session.CurrentCompanyID, &input); err != nil {
		return CallList{}, err
	}
	active := current.IsActive
	if input.IsActive != nil {
		active = *input.IsActive
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CallList{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	tag, err := tx.Exec(ctx, `UPDATE crm_call_lists SET name=$4,description=$5,script_id=NULLIF($6,'')::uuid,retry_minutes=$7,max_attempts=$8,
 is_active=$9,updated_at=now(),version=version+1 WHERE company_id=$1 AND id=$2::uuid AND version=$3`,
		session.CurrentCompanyID, current.ID, version, input.Name, input.Description, input.ScriptID, input.RetryMinutes, input.MaxAttempts, active)
	if err != nil {
		return CallList{}, mapConstraint(err)
	}
	if tag.RowsAffected() == 0 {
		return CallList{}, identity.ErrConflict
	}
	if err = writeAudit(ctx, tx, session, meta, "CRM_CALL_LIST_UPDATED", "crm_call_list", current.ID, map[string]any{"name": input.Name, "active": active}); err != nil {
		return CallList{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return CallList{}, err
	}
	return s.getCallList(ctx, s.pool, session.CurrentCompanyID, current.ID)
}

// --- Items ---------------------------------------------------------------

const itemSelect = `SELECT i.id::text,i.list_id::text,i.lead_id::text,l.display_name,l.contact_name,l.phone,l.status,l.do_not_call,
 i.status,i.attempts,i.available_at,i.last_outcome,i.last_called_at,
 CASE WHEN i.claimed_until>now() THEN i.claimed_by::text END,
 CASE WHEN i.claimed_until>now() THEN COALESCE(u.display_name,'') ELSE '' END,i.position
 FROM crm_call_list_items i
 JOIN crm_leads l ON l.company_id=i.company_id AND l.id=i.lead_id
 LEFT JOIN users u ON u.id=i.claimed_by `

func scanItem(row rowScanner) (CallListItem, error) {
	var it CallListItem
	err := row.Scan(&it.ID, &it.ListID, &it.LeadID, &it.LeadName, &it.ContactName, &it.Phone, &it.LeadStatus, &it.DoNotCall,
		&it.Status, &it.Attempts, &it.AvailableAt, &it.LastOutcome, &it.LastCalledAt, &it.ClaimedBy, &it.ClaimedName, &it.Position)
	return it, err
}

// ListItems returns a list's items in queue order: callable now first, then
// waiting, then finished. Leads the caller cannot see are left out.
func (s *Service) ListItems(ctx context.Context, session identity.Session, listID, status string, limit int) ([]CallListItem, error) {
	if !session.HasPermission("crm.read") {
		return nil, identity.ErrForbidden
	}
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	list, err := s.getCallList(ctx, s.pool, session.CurrentCompanyID, listID)
	if err != nil {
		return nil, err
	}
	status = strings.ToUpper(strings.TrimSpace(status))
	rows, err := s.pool.Query(ctx, itemSelect+` WHERE i.company_id=$1 AND ($2::boolean OR l.owner_user_id IS NULL OR l.owner_user_id=$3::uuid)
 AND i.list_id=$4::uuid AND ($5='' OR i.status=$5)
 ORDER BY CASE WHEN i.status IN ('PENDING','CALLBACK') AND i.available_at<=now() THEN 0
               WHEN i.status IN ('PENDING','CALLBACK') THEN 1 ELSE 2 END,
          i.available_at,i.position LIMIT $6`,
		session.CurrentCompanyID, canSeeAll(session), session.User.ID, list.ID, status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []CallListItem{}
	for rows.Next() {
		item, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// AddLeads queues the chosen leads at the end of the list, hottest first.
// A lead is never queued when it cannot be called, is already on this list,
// is waiting in another open list (it would be called twice), or — unless
// IncludeCalled — was called before and has no due callback. With an empty
// listID only DryRun is allowed: it previews a list that is not created yet.
func (s *Service) AddLeads(ctx context.Context, session identity.Session, listID string, input AddLeadsInput, meta identity.RequestMeta) (AddItemsResult, error) {
	if !session.HasPermission("crm.manage") {
		return AddItemsResult{}, identity.ErrForbidden
	}
	if listID == "" && !input.DryRun {
		return AddItemsResult{}, ErrCallListNotFound
	}
	var list CallList
	if listID != "" {
		var err error
		if list, err = s.getCallList(ctx, s.pool, session.CurrentCompanyID, listID); err != nil {
			return AddItemsResult{}, err
		}
		if !list.IsActive && !input.DryRun {
			return AddItemsResult{}, errInvalid("arşivlenmiş listeye aday eklenemez; önce arşivden çıkarın")
		}
	}
	filter := LeadFilter{}
	if input.Filter != nil {
		filter = *input.Filter
		filter.Callable = false // classified below, so the user sees why a lead was left out
	}
	where, args, err := leadFilterSQL(session, filter)
	if err != nil {
		return AddItemsResult{}, err
	}
	if input.Filter == nil {
		ids := make([]string, 0, len(input.LeadIDs))
		for _, id := range input.LeadIDs {
			if _, err := uuid.Parse(strings.TrimSpace(id)); err == nil {
				ids = append(ids, strings.TrimSpace(id))
			}
		}
		if len(ids) == 0 {
			return AddItemsResult{}, errInvalid("eklenecek aday seçin")
		}
		if len(ids) > maxListAdd {
			return AddItemsResult{}, errInvalid("bir seferde en fazla 10000 aday eklenebilir")
		}
		where += ` AND l.id=ANY(` + args.add(ids) + `::uuid[])`
	}
	listParam := args.add(list.ID)
	includeCalled := args.add(input.IncludeCalled)
	classified := `WITH cand AS (
   SELECT l.id,l.created_at,COALESCE(NULLIF(l.priority,''),'Z') AS rank,l.do_not_call,l.status,l.call_attempts,l.next_action_at
   FROM crm_leads l WHERE l.company_id=$1 AND` + where + `
   ORDER BY rank,l.created_at,l.id LIMIT ` + strconv.Itoa(maxListAdd) + `),
 judged AS (
   SELECT c.*, CASE
     WHEN c.do_not_call OR c.status IN ('CONVERTED','DISQUALIFIED') THEN 'not_callable'
     WHEN EXISTS (SELECT 1 FROM crm_call_list_items x WHERE x.company_id=$1 AND x.lead_id=c.id
                  AND x.list_id=NULLIF(` + listParam + `,'')::uuid) THEN 'in_list'
     WHEN EXISTS (SELECT 1 FROM crm_call_list_items x JOIN crm_call_lists o ON o.company_id=x.company_id AND o.id=x.list_id
                  WHERE x.company_id=$1 AND x.lead_id=c.id AND o.is_active AND x.status IN ('PENDING','CALLBACK')
                  AND x.list_id IS DISTINCT FROM NULLIF(` + listParam + `,'')::uuid) THEN 'other_list'
     WHEN NOT ` + includeCalled + `::boolean AND c.call_attempts>0
          AND NOT (c.next_action_at IS NOT NULL AND c.next_action_at<=now()) THEN 'called'
     ELSE 'add' END AS verdict
   FROM cand c) `

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AddItemsResult{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if list.ID != "" && !input.DryRun {
		// Serialise appends to one list so positions stay unique and ordered.
		if _, err = tx.Exec(ctx, `SELECT 1 FROM crm_call_lists WHERE company_id=$1 AND id=$2::uuid FOR UPDATE`, session.CurrentCompanyID, list.ID); err != nil {
			return AddItemsResult{}, err
		}
	}
	result := AddItemsResult{}
	rows, err := tx.Query(ctx, classified+`SELECT verdict,count(*) FROM judged GROUP BY verdict`, args.values...)
	if err != nil {
		return AddItemsResult{}, err
	}
	counts := map[string]int{}
	for rows.Next() {
		var verdict string
		var count int
		if err := rows.Scan(&verdict, &count); err != nil {
			rows.Close()
			return AddItemsResult{}, err
		}
		counts[verdict] = count
		result.Matched += count
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return AddItemsResult{}, err
	}
	result.AlreadyInList, result.InOtherList = counts["in_list"], counts["other_list"]
	result.CalledBefore, result.NotCallable = counts["called"], counts["not_callable"]
	if input.Filter == nil {
		// Ids that matched nothing (not visible, wrong company) cannot be called either.
		result.NotCallable += len(input.LeadIDs) - result.Matched
	}
	if input.DryRun {
		result.Added = counts["add"]
		result.Skipped = result.Matched - result.Added
		return result, nil
	}
	tag, err := tx.Exec(ctx, classified+`INSERT INTO crm_call_list_items(id,company_id,list_id,lead_id,position)
 SELECT gen_random_uuid(),$1,`+listParam+`::uuid,j.id,
   COALESCE((SELECT max(position) FROM crm_call_list_items WHERE company_id=$1 AND list_id=`+listParam+`::uuid),0)
     + row_number() OVER (ORDER BY j.rank,j.created_at,j.id)
 FROM judged j WHERE j.verdict='add'`, args.values...)
	if err != nil {
		return AddItemsResult{}, mapConstraint(err)
	}
	result.Added = int(tag.RowsAffected())
	result.Skipped = result.Matched - result.Added
	if err = writeAudit(ctx, tx, session, meta, "CRM_CALL_LIST_LEADS_ADDED", "crm_call_list", list.ID, map[string]any{
		"added": result.Added, "in_other_list": result.InOtherList, "called_before": result.CalledBefore, "include_called": input.IncludeCalled,
	}); err != nil {
		return AddItemsResult{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return AddItemsResult{}, err
	}
	return result, nil
}

// ArchiveCallList takes a list out of use (or back). An archived list keeps
// its items and history but cannot be called or filled.
func (s *Service) ArchiveCallList(ctx context.Context, session identity.Session, id string, archived bool, meta identity.RequestMeta) (CallList, error) {
	if !session.HasPermission("crm.manage") {
		return CallList{}, identity.ErrForbidden
	}
	list, err := s.getCallList(ctx, s.pool, session.CurrentCompanyID, id)
	if err != nil {
		return CallList{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CallList{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err = tx.Exec(ctx, `UPDATE crm_call_lists SET is_active=$3,updated_at=now(),version=version+1 WHERE company_id=$1 AND id=$2::uuid`,
		session.CurrentCompanyID, list.ID, !archived); err != nil {
		return CallList{}, err
	}
	// Held leads go back to the team.
	if _, err = tx.Exec(ctx, `UPDATE crm_call_list_items SET claimed_by=NULL,claimed_until=NULL WHERE company_id=$1 AND list_id=$2::uuid`,
		session.CurrentCompanyID, list.ID); err != nil {
		return CallList{}, err
	}
	event := "CRM_CALL_LIST_ARCHIVED"
	if !archived {
		event = "CRM_CALL_LIST_RESTORED"
	}
	if err = writeAudit(ctx, tx, session, meta, event, "crm_call_list", list.ID, map[string]any{"name": list.Name}); err != nil {
		return CallList{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return CallList{}, err
	}
	return s.getCallList(ctx, s.pool, session.CurrentCompanyID, list.ID)
}

// DeleteCallList removes a list and its queue. Calls made from it stay on the
// leads' timelines; they just no longer point at the list.
func (s *Service) DeleteCallList(ctx context.Context, session identity.Session, id string, meta identity.RequestMeta) error {
	if !session.HasPermission("crm.manage") {
		return identity.ErrForbidden
	}
	list, err := s.getCallList(ctx, s.pool, session.CurrentCompanyID, id)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	for _, statement := range []string{
		`UPDATE crm_activities SET call_list_id=NULL WHERE company_id=$1 AND call_list_id=$2::uuid`,
		`DELETE FROM crm_call_list_items WHERE company_id=$1 AND list_id=$2::uuid`,
		`DELETE FROM crm_call_lists WHERE company_id=$1 AND id=$2::uuid`,
	} {
		if _, err = tx.Exec(ctx, statement, session.CurrentCompanyID, list.ID); err != nil {
			return err
		}
	}
	if err = writeAudit(ctx, tx, session, meta, "CRM_CALL_LIST_DELETED", "crm_call_list", list.ID, map[string]any{"name": list.Name, "items": list.Total}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RemoveItems takes items off a list. The calls already made stay on the
// lead's timeline.
func (s *Service) RemoveItems(ctx context.Context, session identity.Session, listID string, itemIDs []string, meta identity.RequestMeta) (int, error) {
	if !session.HasPermission("crm.manage") {
		return 0, identity.ErrForbidden
	}
	list, err := s.getCallList(ctx, s.pool, session.CurrentCompanyID, listID)
	if err != nil {
		return 0, err
	}
	ids := make([]string, 0, len(itemIDs))
	for _, id := range itemIDs {
		if _, err := uuid.Parse(strings.TrimSpace(id)); err == nil {
			ids = append(ids, strings.TrimSpace(id))
		}
	}
	if len(ids) == 0 {
		return 0, errInvalid("çıkarılacak kayıt seçin")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	tag, err := tx.Exec(ctx, `DELETE FROM crm_call_list_items WHERE company_id=$1 AND list_id=$2::uuid AND id=ANY($3::uuid[])`,
		session.CurrentCompanyID, list.ID, ids)
	if err != nil {
		return 0, err
	}
	if err = writeAudit(ctx, tx, session, meta, "CRM_CALL_LIST_ITEMS_REMOVED", "crm_call_list", list.ID, map[string]any{"removed": tag.RowsAffected()}); err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), tx.Commit(ctx)
}

// ClaimNext hands the caller the next lead to call from the list. A lead the
// caller already holds comes back first (a page refresh must not skip it);
// otherwise due callbacks come before fresh leads. SKIP LOCKED plus the claim
// columns keep two reps from ever getting the same lead. It returns
// ErrCallItemNotFound when nothing is callable right now.
func (s *Service) ClaimNext(ctx context.Context, session identity.Session, listID string) (NextCall, error) {
	if !session.HasPermission("crm.edit") {
		return NextCall{}, identity.ErrForbidden
	}
	list, err := s.getCallList(ctx, s.pool, session.CurrentCompanyID, listID)
	if err != nil {
		return NextCall{}, err
	}
	if !list.IsActive {
		return NextCall{}, errInvalid("arama listesi kapatılmış")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return NextCall{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var itemID string
	err = tx.QueryRow(ctx, `WITH next AS (
 SELECT i.id FROM crm_call_list_items i
 JOIN crm_leads l ON l.company_id=i.company_id AND l.id=i.lead_id
 WHERE i.company_id=$1 AND i.list_id=$4::uuid AND i.status IN ('PENDING','CALLBACK') AND i.available_at<=now()
   AND NOT l.do_not_call AND l.status NOT IN ('CONVERTED','DISQUALIFIED')
   AND ($2::boolean OR l.owner_user_id IS NULL OR l.owner_user_id=$3::uuid)
   AND (i.claimed_until IS NULL OR i.claimed_until<now() OR i.claimed_by=$3::uuid)
   AND NOT EXISTS (SELECT 1 FROM crm_call_list_items h WHERE h.company_id=i.company_id AND h.lead_id=i.lead_id
                   AND h.id<>i.id AND h.claimed_until>now() AND h.claimed_by<>$3::uuid)
 ORDER BY (i.claimed_by IS NOT DISTINCT FROM $3::uuid AND i.claimed_until>=now()) DESC,
          (i.status='CALLBACK') DESC,i.available_at,i.position
 LIMIT 1 FOR UPDATE OF i SKIP LOCKED)
UPDATE crm_call_list_items i SET claimed_by=$3::uuid,claimed_until=now()+$5::interval,updated_at=now()
FROM next WHERE i.company_id=$1 AND i.id=next.id RETURNING i.id::text`,
		session.CurrentCompanyID, canSeeAll(session), session.User.ID, list.ID, claimTTL).Scan(&itemID)
	if errors.Is(err, pgx.ErrNoRows) {
		return NextCall{}, ErrCallItemNotFound
	}
	if err != nil {
		return NextCall{}, err
	}
	// Holding one lead per list at a time: let go of anything else held here.
	if _, err = tx.Exec(ctx, `UPDATE crm_call_list_items SET claimed_by=NULL,claimed_until=NULL
 WHERE company_id=$1 AND list_id=$2::uuid AND claimed_by=$3::uuid AND id<>$4::uuid`,
		session.CurrentCompanyID, list.ID, session.User.ID, itemID); err != nil {
		return NextCall{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return NextCall{}, err
	}
	result := NextCall{List: list}
	if result.Item, err = scanItem(s.pool.QueryRow(ctx, itemSelect+` WHERE i.company_id=$1 AND i.id=$2::uuid`, session.CurrentCompanyID, itemID)); err != nil {
		return NextCall{}, err
	}
	if result.Lead, err = s.GetLead(ctx, session, result.Item.LeadID); err != nil {
		return NextCall{}, err
	}
	if list.ScriptID != nil {
		if script, err := s.getScript(ctx, s.pool, session.CurrentCompanyID, *list.ScriptID); err == nil && script.IsActive {
			result.Script = &script
		}
	}
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM crm_call_list_items i JOIN crm_leads l ON l.company_id=i.company_id AND l.id=i.lead_id
 WHERE i.company_id=$1 AND i.list_id=$4::uuid AND i.status IN ('PENDING','CALLBACK') AND i.available_at<=now()
   AND NOT l.do_not_call AND ($2::boolean OR l.owner_user_id IS NULL OR l.owner_user_id=$3::uuid)
   AND (i.claimed_until IS NULL OR i.claimed_until<now() OR i.claimed_by=$3::uuid)`,
		session.CurrentCompanyID, canSeeAll(session), session.User.ID, list.ID).Scan(&result.Remaining); err != nil {
		return NextCall{}, err
	}
	return result, nil
}

// ReleaseItem gives back a claimed lead without calling it (skip = false) or
// takes it off the queue for good (skip = true).
func (s *Service) ReleaseItem(ctx context.Context, session identity.Session, listID, itemID string, skip bool, meta identity.RequestMeta) error {
	if !session.HasPermission("crm.edit") {
		return identity.ErrForbidden
	}
	if _, err := uuid.Parse(strings.TrimSpace(itemID)); err != nil {
		return ErrCallItemNotFound
	}
	list, err := s.getCallList(ctx, s.pool, session.CurrentCompanyID, listID)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	tag, err := tx.Exec(ctx, `UPDATE crm_call_list_items SET claimed_by=NULL,claimed_until=NULL,
 status=CASE WHEN $5 THEN 'SKIPPED' ELSE status END,updated_at=now()
 WHERE company_id=$1 AND list_id=$2::uuid AND id=$3::uuid
   AND (($4::boolean AND status IN ('PENDING','CALLBACK')) OR claimed_by=$6::uuid)`,
		session.CurrentCompanyID, list.ID, strings.TrimSpace(itemID), session.HasPermission("crm.manage"), skip, session.User.ID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrCallItemNotFound
	}
	if skip {
		if err = writeAudit(ctx, tx, session, meta, "CRM_CALL_ITEM_SKIPPED", "crm_call_list", list.ID, map[string]any{"item_id": itemID}); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// advanceCallListItem applies a call's outcome to the list item it came from
// and returns the list id for the activity record. An empty itemID means the
// call was made outside any list.
func advanceCallListItem(ctx context.Context, tx pgx.Tx, session identity.Session, leadID, itemID, outcome string, now time.Time, callbackAt *time.Time) (string, error) {
	if itemID != "" {
		if _, err := uuid.Parse(itemID); err != nil {
			return "", ErrCallItemNotFound
		}
	}
	// A call made from one list (or from the lead card) counts in every list
	// the lead is waiting in, so no other list offers it again as if it had
	// never been called.
	rows, err := tx.Query(ctx, `SELECT i.id::text,i.list_id::text,i.attempts,c.max_attempts,c.retry_minutes
 FROM crm_call_list_items i JOIN crm_call_lists c ON c.company_id=i.company_id AND c.id=i.list_id
 WHERE i.company_id=$1 AND i.lead_id=$2::uuid AND i.status IN ('PENDING','CALLBACK')
 ORDER BY i.id FOR UPDATE OF i`, session.CurrentCompanyID, leadID)
	if err != nil {
		return "", err
	}
	type openItem struct {
		id, listID                         string
		attempts, maxAttempts, retryMinute int
	}
	var items []openItem
	for rows.Next() {
		var it openItem
		if err := rows.Scan(&it.id, &it.listID, &it.attempts, &it.maxAttempts, &it.retryMinute); err != nil {
			rows.Close()
			return "", err
		}
		items = append(items, it)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return "", err
	}
	listID, found := "", itemID == ""
	for _, it := range items {
		if it.id == itemID {
			listID, found = it.listID, true
		}
		state, availableAt := NextItemState(outcome, it.attempts+1, it.maxAttempts, time.Duration(it.retryMinute)*time.Minute, now, callbackAt)
		if _, err = tx.Exec(ctx, `UPDATE crm_call_list_items SET status=$3,attempts=attempts+1,available_at=$4,last_outcome=$5,last_called_at=$6,
 claimed_by=NULL,claimed_until=NULL,updated_at=now() WHERE company_id=$1 AND id=$2::uuid`,
			session.CurrentCompanyID, it.id, state, availableAt, outcome, now); err != nil {
			return "", err
		}
	}
	if !found {
		return "", ErrCallItemNotFound
	}
	return listID, nil
}

// closeLeadItems finishes every open queue entry of a lead, e.g. when it asks
// not to be called or becomes a cari.
func closeLeadItems(ctx context.Context, tx pgx.Tx, companyID, leadID, status string) error {
	_, err := tx.Exec(ctx, `UPDATE crm_call_list_items SET status=$3,claimed_by=NULL,claimed_until=NULL,updated_at=now()
 WHERE company_id=$1 AND lead_id=$2::uuid AND status IN ('PENDING','CALLBACK')`, companyID, leadID, status)
	return err
}
