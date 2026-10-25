package crm

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/money"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrOpportunityNotFound = errors.New("CRM_OPPORTUNITY_NOT_FOUND")
	ErrStageNotFound       = errors.New("CRM_STAGE_NOT_FOUND")
	ErrQuoteNotFound       = errors.New("CRM_QUOTE_NOT_FOUND")
	ErrQuoteNeedsParty     = errors.New("CRM_QUOTE_NEEDS_PARTY")
)

// Stage kinds. A WON or LOST stage closes the opportunity.
const (
	StageOpen = "OPEN"
	StageWon  = "WON"
	StageLost = "LOST"
)

type Stage struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	SortOrder   int    `json:"sort_order"`
	Probability int    `json:"probability"`
	Kind        string `json:"kind"`
	IsActive    bool   `json:"is_active"`
	Version     int64  `json:"version"`
}

type StageInput struct {
	Name        string `json:"name"`
	SortOrder   int    `json:"sort_order"`
	Probability int    `json:"probability"`
	Kind        string `json:"kind"`
	IsActive    *bool  `json:"is_active"`
}

type Opportunity struct {
	ID                string     `json:"id"`
	Title             string     `json:"title"`
	LeadID            *string    `json:"lead_id,omitempty"`
	LeadName          string     `json:"lead_name"`
	PartyID           *string    `json:"party_id,omitempty"`
	PartyName         string     `json:"party_name"`
	StageID           string     `json:"stage_id"`
	StageName         string     `json:"stage_name"`
	StageKind         string     `json:"stage_kind"`
	Probability       int        `json:"probability"`
	Amount            string     `json:"amount"`
	Currency          string     `json:"currency"`
	ExpectedCloseDate *string    `json:"expected_close_date,omitempty"`
	OwnerUserID       *string    `json:"owner_user_id,omitempty"`
	OwnerName         string     `json:"owner_name"`
	Notes             string     `json:"notes"`
	LostReason        string     `json:"lost_reason"`
	QuoteID           *string    `json:"quote_id,omitempty"`
	QuoteNo           string     `json:"quote_no"`
	QuoteStatus       string     `json:"quote_status"`
	QuoteTotal        string     `json:"quote_total"`
	QuoteCurrency     string     `json:"quote_currency"`
	ClosedAt          *time.Time `json:"closed_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	Version           int64      `json:"version"`
}

type OpportunityInput struct {
	Title             string `json:"title"`
	LeadID            string `json:"lead_id"`
	PartyID           string `json:"party_id"`
	StageID           string `json:"stage_id"`
	Amount            string `json:"amount"`
	Currency          string `json:"currency"`
	ExpectedCloseDate string `json:"expected_close_date"`
	OwnerUserID       string `json:"owner_user_id"`
	Notes             string `json:"notes"`
}

// OpportunityFilter narrows the board. State is "open" (default), "won",
// "lost" or "all".
type OpportunityFilter struct {
	State   string
	Owner   string
	PartyID string
	LeadID  string
	Query   string
}

// defaultStages are created for a company the first time it touches the
// pipeline (migration 000164 seeded the companies that already existed).
var defaultStages = []Stage{
	{Name: "Yeni", SortOrder: 10, Probability: 10, Kind: StageOpen},
	{Name: "Görüşme", SortOrder: 20, Probability: 25, Kind: StageOpen},
	{Name: "Teklif", SortOrder: 30, Probability: 50, Kind: StageOpen},
	{Name: "Pazarlık", SortOrder: 40, Probability: 75, Kind: StageOpen},
	{Name: "Kazanıldı", SortOrder: 90, Probability: 100, Kind: StageWon},
	{Name: "Kaybedildi", SortOrder: 100, Probability: 0, Kind: StageLost},
}

type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func ensureStages(ctx context.Context, q execer, companyID string) error {
	var exists bool
	if err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM crm_pipeline_stages WHERE company_id=$1)`, companyID).Scan(&exists); err != nil || exists {
		return err
	}
	for _, stage := range defaultStages {
		if _, err := q.Exec(ctx, `INSERT INTO crm_pipeline_stages(id,company_id,name,sort_order,probability,kind)
 VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`,
			uuid.NewString(), companyID, stage.Name, stage.SortOrder, stage.Probability, stage.Kind); err != nil {
			return err
		}
	}
	return nil
}

const stageColumns = `id::text,name,sort_order,probability,kind,is_active,version`

func scanStage(row rowScanner) (Stage, error) {
	var st Stage
	err := row.Scan(&st.ID, &st.Name, &st.SortOrder, &st.Probability, &st.Kind, &st.IsActive, &st.Version)
	return st, err
}

func (s *Service) ListStages(ctx context.Context, session identity.Session) ([]Stage, error) {
	if !session.HasPermission("crm.read") {
		return nil, identity.ErrForbidden
	}
	if err := ensureStages(ctx, s.pool, session.CurrentCompanyID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT `+stageColumns+` FROM crm_pipeline_stages WHERE company_id=$1 ORDER BY sort_order,name`, session.CurrentCompanyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Stage{}
	for rows.Next() {
		item, err := scanStage(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) getStage(ctx context.Context, q reader, companyID, id string) (Stage, error) {
	if _, err := uuid.Parse(strings.TrimSpace(id)); err != nil {
		return Stage{}, ErrStageNotFound
	}
	item, err := scanStage(q.QueryRow(ctx, `SELECT `+stageColumns+` FROM crm_pipeline_stages WHERE company_id=$1 AND id=$2::uuid`, companyID, strings.TrimSpace(id)))
	if errors.Is(err, pgx.ErrNoRows) {
		return Stage{}, ErrStageNotFound
	}
	return item, err
}

func validateStage(input *StageInput) error {
	input.Name = strings.TrimSpace(input.Name)
	input.Kind = strings.ToUpper(strings.TrimSpace(input.Kind))
	if input.Kind == "" {
		input.Kind = StageOpen
	}
	if input.Name == "" || len([]rune(input.Name)) > 80 {
		return errInvalid("aşama adı 1-80 karakter olmalıdır")
	}
	if input.Kind != StageOpen && input.Kind != StageWon && input.Kind != StageLost {
		return errInvalid("geçersiz aşama türü")
	}
	if input.Probability < 0 || input.Probability > 100 {
		return errInvalid("olasılık 0-100 arasında olmalıdır")
	}
	return nil
}

func (s *Service) CreateStage(ctx context.Context, session identity.Session, input StageInput, meta identity.RequestMeta) (Stage, error) {
	if !session.HasPermission("crm.manage") {
		return Stage{}, identity.ErrForbidden
	}
	if err := validateStage(&input); err != nil {
		return Stage{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Stage{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err = ensureStages(ctx, tx, session.CurrentCompanyID); err != nil {
		return Stage{}, err
	}
	id := uuid.NewString()
	if _, err = tx.Exec(ctx, `INSERT INTO crm_pipeline_stages(id,company_id,name,sort_order,probability,kind) VALUES($1,$2,$3,$4,$5,$6)`,
		id, session.CurrentCompanyID, input.Name, input.SortOrder, input.Probability, input.Kind); err != nil {
		return Stage{}, mapConstraint(err)
	}
	if err = writeAudit(ctx, tx, session, meta, "CRM_STAGE_CREATED", "crm_pipeline_stage", id, map[string]any{"name": input.Name}); err != nil {
		return Stage{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Stage{}, err
	}
	return s.getStage(ctx, s.pool, session.CurrentCompanyID, id)
}

// UpdateStage renames, reorders or retires a stage. Its kind is fixed once
// created: flipping an open stage to WON would silently close every
// opportunity sitting in it.
func (s *Service) UpdateStage(ctx context.Context, session identity.Session, id string, version int64, input StageInput, meta identity.RequestMeta) (Stage, error) {
	if !session.HasPermission("crm.manage") {
		return Stage{}, identity.ErrForbidden
	}
	current, err := s.getStage(ctx, s.pool, session.CurrentCompanyID, id)
	if err != nil {
		return Stage{}, err
	}
	input.Kind = current.Kind
	if err = validateStage(&input); err != nil {
		return Stage{}, err
	}
	active := current.IsActive
	if input.IsActive != nil {
		active = *input.IsActive
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Stage{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if !active && current.IsActive {
		var inUse bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM crm_opportunities WHERE company_id=$1 AND stage_id=$2::uuid AND closed_at IS NULL)`,
			session.CurrentCompanyID, current.ID).Scan(&inUse); err != nil {
			return Stage{}, err
		}
		if inUse {
			return Stage{}, errInvalid("bu aşamada açık fırsat var; önce fırsatları başka aşamaya taşıyın")
		}
	}
	tag, err := tx.Exec(ctx, `UPDATE crm_pipeline_stages SET name=$4,sort_order=$5,probability=$6,is_active=$7,updated_at=now(),version=version+1
 WHERE company_id=$1 AND id=$2::uuid AND version=$3`, session.CurrentCompanyID, current.ID, version, input.Name, input.SortOrder, input.Probability, active)
	if err != nil {
		return Stage{}, mapConstraint(err)
	}
	if tag.RowsAffected() == 0 {
		return Stage{}, identity.ErrConflict
	}
	if err = writeAudit(ctx, tx, session, meta, "CRM_STAGE_UPDATED", "crm_pipeline_stage", current.ID, map[string]any{"name": input.Name}); err != nil {
		return Stage{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Stage{}, err
	}
	return s.getStage(ctx, s.pool, session.CurrentCompanyID, current.ID)
}

// --- Opportunities -------------------------------------------------------

const opportunitySelect = `SELECT o.id::text,o.title,o.lead_id::text,COALESCE(l.display_name,''),o.party_id::text,COALESCE(p.display_name,''),
 o.stage_id::text,st.name,st.kind,st.probability,o.amount::text,o.currency,to_char(o.expected_close_date,'YYYY-MM-DD'),
 o.owner_user_id::text,COALESCE(u.display_name,''),o.notes,o.lost_reason,
 o.quote_id::text,COALESCE(q.document_no,''),COALESCE(q.status,''),COALESCE(q.grand_total::text,''),COALESCE(q.currency_code,''),
 o.closed_at,o.created_at,o.updated_at,o.version
 FROM crm_opportunities o
 JOIN crm_pipeline_stages st ON st.company_id=o.company_id AND st.id=o.stage_id
 LEFT JOIN crm_leads l ON l.company_id=o.company_id AND l.id=o.lead_id
 LEFT JOIN parties p ON p.company_id=o.company_id AND p.id=o.party_id
 LEFT JOIN sales_quotes q ON q.company_id=o.company_id AND q.id=o.quote_id
 LEFT JOIN users u ON u.id=o.owner_user_id `

const opportunityVisible = ` AND ($2::boolean OR o.owner_user_id IS NULL OR o.owner_user_id=$3::uuid)`

func scanOpportunity(row rowScanner) (Opportunity, error) {
	var o Opportunity
	err := row.Scan(&o.ID, &o.Title, &o.LeadID, &o.LeadName, &o.PartyID, &o.PartyName,
		&o.StageID, &o.StageName, &o.StageKind, &o.Probability, &o.Amount, &o.Currency, &o.ExpectedCloseDate,
		&o.OwnerUserID, &o.OwnerName, &o.Notes, &o.LostReason,
		&o.QuoteID, &o.QuoteNo, &o.QuoteStatus, &o.QuoteTotal, &o.QuoteCurrency,
		&o.ClosedAt, &o.CreatedAt, &o.UpdatedAt, &o.Version)
	if err == nil {
		o.Amount = trimDecimal(o.Amount)
		o.QuoteTotal = trimDecimal(o.QuoteTotal)
	}
	return o, err
}

func (s *Service) ListOpportunities(ctx context.Context, session identity.Session, filter OpportunityFilter, limit int) ([]Opportunity, error) {
	if !session.HasPermission("crm.read") {
		return nil, identity.ErrForbidden
	}
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	state := strings.TrimSpace(filter.State)
	if state == "" {
		state = "open"
	}
	var stateSQL string
	switch state {
	case "open":
		stateSQL = `st.kind='OPEN'`
	case "won":
		stateSQL = `st.kind='WON'`
	case "lost":
		stateSQL = `st.kind='LOST'`
	case "all":
		stateSQL = `true`
	default:
		return nil, errInvalid("geçersiz fırsat durumu")
	}
	owner := strings.TrimSpace(filter.Owner)
	if owner == "me" {
		owner = session.User.ID
	}
	for _, id := range []string{owner, filter.PartyID, filter.LeadID} {
		if id = strings.TrimSpace(id); id != "" && id != "none" {
			if _, err := uuid.Parse(id); err != nil {
				return nil, errInvalid("geçersiz filtre")
			}
		}
	}
	rows, err := s.pool.Query(ctx, opportunitySelect+` WHERE o.company_id=$1`+opportunityVisible+` AND `+stateSQL+`
 AND ($4='' OR ($4='none' AND o.owner_user_id IS NULL) OR o.owner_user_id=NULLIF(NULLIF($4,''),'none')::uuid)
 AND ($5='' OR o.party_id=NULLIF($5,'')::uuid)
 AND ($6='' OR o.lead_id=NULLIF($6,'')::uuid)
 AND ($7='' OR o.title ILIKE '%'||$7||'%' OR l.display_name ILIKE '%'||$7||'%' OR p.display_name ILIKE '%'||$7||'%')
 ORDER BY st.sort_order,o.updated_at DESC LIMIT $8`,
		session.CurrentCompanyID, canSeeAll(session), session.User.ID, owner, strings.TrimSpace(filter.PartyID),
		strings.TrimSpace(filter.LeadID), strings.TrimSpace(filter.Query), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Opportunity{}
	for rows.Next() {
		item, err := scanOpportunity(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) GetOpportunity(ctx context.Context, session identity.Session, id string) (Opportunity, error) {
	if !session.HasPermission("crm.read") {
		return Opportunity{}, identity.ErrForbidden
	}
	return s.getOpportunity(ctx, s.pool, session, id)
}

func (s *Service) getOpportunity(ctx context.Context, q reader, session identity.Session, id string) (Opportunity, error) {
	if _, err := uuid.Parse(strings.TrimSpace(id)); err != nil {
		return Opportunity{}, ErrOpportunityNotFound
	}
	item, err := scanOpportunity(q.QueryRow(ctx, opportunitySelect+` WHERE o.company_id=$1`+opportunityVisible+` AND o.id=$4::uuid`,
		session.CurrentCompanyID, canSeeAll(session), session.User.ID, strings.TrimSpace(id)))
	if errors.Is(err, pgx.ErrNoRows) {
		return Opportunity{}, ErrOpportunityNotFound
	}
	return item, err
}

func normalizeOpportunity(input *OpportunityInput) error {
	input.Title = strings.TrimSpace(input.Title)
	input.LeadID = strings.TrimSpace(input.LeadID)
	input.PartyID = strings.TrimSpace(input.PartyID)
	input.StageID = strings.TrimSpace(input.StageID)
	input.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))
	input.ExpectedCloseDate = strings.TrimSpace(input.ExpectedCloseDate)
	input.OwnerUserID = strings.TrimSpace(input.OwnerUserID)
	input.Notes = strings.TrimSpace(input.Notes)
	input.Amount = strings.ReplaceAll(strings.TrimSpace(input.Amount), ",", ".")
	if input.Amount == "" {
		input.Amount = "0"
	}
	if input.Currency == "" {
		input.Currency = "TRY"
	}
	if input.Title == "" || len([]rune(input.Title)) > 200 {
		return errInvalid("fırsat başlığı 1-200 karakter olmalıdır")
	}
	if len([]rune(input.Notes)) > 4000 {
		return errInvalid("not en fazla 4000 karakter olabilir")
	}
	amount, err := money.ParseDecimal(input.Amount, 4)
	if err != nil || amount.Sign() < 0 || len(strings.Split(input.Amount, ".")[0]) > 16 {
		return errInvalid("geçersiz tutar")
	}
	if len(input.Currency) != 3 || strings.Trim(input.Currency, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "" {
		return errInvalid("geçersiz para birimi")
	}
	if input.ExpectedCloseDate != "" {
		if _, err := time.Parse("2006-01-02", input.ExpectedCloseDate); err != nil {
			return errInvalid("geçersiz kapanış tarihi")
		}
	}
	if input.OwnerUserID != "" {
		if _, err := uuid.Parse(input.OwnerUserID); err != nil {
			return ErrOwnerNotMember
		}
	}
	return nil
}

func (s *Service) CreateOpportunity(ctx context.Context, session identity.Session, input OpportunityInput, meta identity.RequestMeta) (Opportunity, error) {
	if !session.HasPermission("crm.edit") {
		return Opportunity{}, identity.ErrForbidden
	}
	if err := normalizeOpportunity(&input); err != nil {
		return Opportunity{}, err
	}
	if (input.LeadID == "") == (input.PartyID == "") {
		return Opportunity{}, errInvalid("fırsat bir adaya ya da bir cariye bağlanmalıdır")
	}
	if input.OwnerUserID == "" {
		input.OwnerUserID = session.User.ID
	}
	if err := checkOwnerChange(session, nil, input.OwnerUserID); err != nil {
		return Opportunity{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Opportunity{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err = ensureStages(ctx, tx, session.CurrentCompanyID); err != nil {
		return Opportunity{}, err
	}
	if input.LeadID != "" {
		lead, err := s.getLead(ctx, tx, session, input.LeadID)
		if err != nil {
			return Opportunity{}, err
		}
		// A converted lead's business now lives on its cari.
		if lead.ConvertedPartyID != nil {
			input.LeadID, input.PartyID = "", *lead.ConvertedPartyID
		}
	}
	if input.PartyID != "" {
		if err = s.ensureParty(ctx, tx, session, input.PartyID); err != nil {
			return Opportunity{}, err
		}
	}
	var stage Stage
	if input.StageID == "" {
		stage, err = scanStage(tx.QueryRow(ctx, `SELECT `+stageColumns+` FROM crm_pipeline_stages
 WHERE company_id=$1 AND kind='OPEN' AND is_active ORDER BY sort_order LIMIT 1`, session.CurrentCompanyID))
		if errors.Is(err, pgx.ErrNoRows) {
			return Opportunity{}, ErrStageNotFound
		}
	} else {
		stage, err = s.getStage(ctx, tx, session.CurrentCompanyID, input.StageID)
	}
	if err != nil {
		return Opportunity{}, err
	}
	if !stage.IsActive || stage.Kind != StageOpen {
		return Opportunity{}, errInvalid("yeni fırsat açık bir aşamada başlamalıdır")
	}
	id := uuid.NewString()
	if _, err = tx.Exec(ctx, `INSERT INTO crm_opportunities(id,company_id,title,lead_id,party_id,stage_id,amount,currency,expected_close_date,owner_user_id,notes,created_by)
 VALUES($1,$2,$3,NULLIF($4,'')::uuid,NULLIF($5,'')::uuid,$6::uuid,$7::numeric,$8,NULLIF($9,'')::date,NULLIF($10,'')::uuid,$11,$12)`,
		id, session.CurrentCompanyID, input.Title, input.LeadID, input.PartyID, stage.ID, input.Amount, input.Currency,
		input.ExpectedCloseDate, input.OwnerUserID, input.Notes, session.User.ID); err != nil {
		return Opportunity{}, mapConstraint(err)
	}
	if err = writeAudit(ctx, tx, session, meta, "CRM_OPPORTUNITY_CREATED", "crm_opportunity", id, map[string]any{"title": input.Title}); err != nil {
		return Opportunity{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Opportunity{}, err
	}
	return s.GetOpportunity(ctx, session, id)
}

func (s *Service) UpdateOpportunity(ctx context.Context, session identity.Session, id string, version int64, input OpportunityInput, meta identity.RequestMeta) (Opportunity, error) {
	if !session.HasPermission("crm.edit") {
		return Opportunity{}, identity.ErrForbidden
	}
	if err := normalizeOpportunity(&input); err != nil {
		return Opportunity{}, err
	}
	current, err := s.GetOpportunity(ctx, session, id)
	if err != nil {
		return Opportunity{}, err
	}
	if err = checkOwnerChange(session, current.OwnerUserID, input.OwnerUserID); err != nil {
		return Opportunity{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Opportunity{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	tag, err := tx.Exec(ctx, `UPDATE crm_opportunities SET title=$4,amount=$5::numeric,currency=$6,expected_close_date=NULLIF($7,'')::date,
 owner_user_id=NULLIF($8,'')::uuid,notes=$9,updated_at=now(),version=version+1
 WHERE company_id=$1 AND id=$2::uuid AND version=$3`,
		session.CurrentCompanyID, current.ID, version, input.Title, input.Amount, input.Currency, input.ExpectedCloseDate, input.OwnerUserID, input.Notes)
	if err != nil {
		return Opportunity{}, mapConstraint(err)
	}
	if tag.RowsAffected() == 0 {
		return Opportunity{}, identity.ErrConflict
	}
	if err = writeAudit(ctx, tx, session, meta, "CRM_OPPORTUNITY_UPDATED", "crm_opportunity", current.ID, map[string]any{"title": input.Title}); err != nil {
		return Opportunity{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Opportunity{}, err
	}
	return s.GetOpportunity(ctx, session, current.ID)
}

// MoveStage moves an opportunity on the board. A lost reason is required for
// a LOST stage; moving back to an open stage reopens the opportunity.
func (s *Service) MoveStage(ctx context.Context, session identity.Session, id string, version int64, stageID, lostReason string, meta identity.RequestMeta) (Opportunity, error) {
	if !session.HasPermission("crm.edit") {
		return Opportunity{}, identity.ErrForbidden
	}
	current, err := s.GetOpportunity(ctx, session, id)
	if err != nil {
		return Opportunity{}, err
	}
	stage, err := s.getStage(ctx, s.pool, session.CurrentCompanyID, stageID)
	if err != nil {
		return Opportunity{}, err
	}
	if !stage.IsActive {
		return Opportunity{}, errInvalid("aşama kullanımdan kaldırılmış")
	}
	lostReason = strings.TrimSpace(lostReason)
	if stage.Kind == StageLost && lostReason == "" {
		return Opportunity{}, errInvalid("kaybedilen fırsat için neden girin")
	}
	if len([]rune(lostReason)) > 500 {
		return Opportunity{}, errInvalid("neden en fazla 500 karakter olabilir")
	}
	if stage.Kind != StageLost {
		lostReason = ""
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Opportunity{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	tag, err := tx.Exec(ctx, `UPDATE crm_opportunities SET stage_id=$4::uuid,lost_reason=$5,
 closed_at=CASE WHEN $6='OPEN' THEN NULL ELSE COALESCE(CASE WHEN stage_id=$4::uuid THEN closed_at END,now()) END,
 updated_at=now(),version=version+1 WHERE company_id=$1 AND id=$2::uuid AND version=$3`,
		session.CurrentCompanyID, current.ID, version, stage.ID, lostReason, stage.Kind)
	if err != nil {
		return Opportunity{}, mapConstraint(err)
	}
	if tag.RowsAffected() == 0 {
		return Opportunity{}, identity.ErrConflict
	}
	if err = writeAudit(ctx, tx, session, meta, "CRM_OPPORTUNITY_STAGE_CHANGED", "crm_opportunity", current.ID,
		map[string]any{"from": current.StageName, "to": stage.Name, "lost_reason": lostReason}); err != nil {
		return Opportunity{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Opportunity{}, err
	}
	return s.GetOpportunity(ctx, session, current.ID)
}

// LinkQuote ties a sales quote to the opportunity. The quote must belong to
// the opportunity's cari, so a lead has to become a cari first.
func (s *Service) LinkQuote(ctx context.Context, session identity.Session, id, quoteID string, meta identity.RequestMeta) (Opportunity, error) {
	if !session.HasPermission("crm.edit") {
		return Opportunity{}, identity.ErrForbidden
	}
	current, err := s.GetOpportunity(ctx, session, id)
	if err != nil {
		return Opportunity{}, err
	}
	if current.PartyID == nil {
		return Opportunity{}, ErrQuoteNeedsParty
	}
	quoteID = strings.TrimSpace(quoteID)
	if _, err = uuid.Parse(quoteID); err != nil {
		return Opportunity{}, ErrQuoteNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Opportunity{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var quoteParty, quoteStatus string
	err = tx.QueryRow(ctx, `SELECT party_id::text,status FROM sales_quotes WHERE company_id=$1 AND id=$2::uuid`, session.CurrentCompanyID, quoteID).Scan(&quoteParty, &quoteStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return Opportunity{}, ErrQuoteNotFound
	}
	if err != nil {
		return Opportunity{}, err
	}
	if quoteParty != *current.PartyID {
		return Opportunity{}, errInvalid("teklif bu fırsatın carisine ait değil")
	}
	if _, err = tx.Exec(ctx, `UPDATE crm_opportunities SET quote_id=$3::uuid,updated_at=now(),version=version+1 WHERE company_id=$1 AND id=$2::uuid`,
		session.CurrentCompanyID, current.ID, quoteID); err != nil {
		return Opportunity{}, err
	}
	// An already accepted quote wins the opportunity straight away, as the
	// outbox handler would have done had the link existed at acceptance.
	if quoteStatus == "ACCEPTED" {
		if err = winOpportunitiesForQuote(ctx, tx, session.CurrentCompanyID, quoteID); err != nil {
			return Opportunity{}, err
		}
	}
	if err = writeAudit(ctx, tx, session, meta, "CRM_OPPORTUNITY_QUOTE_LINKED", "crm_opportunity", current.ID, map[string]any{"quote_id": quoteID}); err != nil {
		return Opportunity{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Opportunity{}, err
	}
	return s.GetOpportunity(ctx, session, current.ID)
}

// HandleQuoteAccepted is the outbox handler for commercial.sales_quote.accept:
// every open opportunity tied to the accepted quote moves to the company's
// first WON stage. Re-delivery is harmless because closed opportunities are
// left alone.
func (s *Service) HandleQuoteAccepted(ctx context.Context, companyID string, payload json.RawMessage) error {
	var body struct {
		EntityID string `json:"entity_id"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return err
	}
	if _, err := uuid.Parse(body.EntityID); err != nil {
		return err
	}
	if _, err := uuid.Parse(companyID); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err = winOpportunitiesForQuote(ctx, tx, companyID, body.EntityID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func winOpportunitiesForQuote(ctx context.Context, tx pgx.Tx, companyID, quoteID string) error {
	_, err := tx.Exec(ctx, `UPDATE crm_opportunities o SET stage_id=w.id,lost_reason='',closed_at=now(),updated_at=now(),version=o.version+1
 FROM (SELECT id FROM crm_pipeline_stages WHERE company_id=$1 AND kind='WON' AND is_active ORDER BY sort_order LIMIT 1) w,
      crm_pipeline_stages cur
 WHERE o.company_id=$1 AND o.quote_id=$2::uuid AND cur.company_id=o.company_id AND cur.id=o.stage_id AND cur.kind='OPEN'`,
		companyID, quoteID)
	return err
}

// ConvertLead records that a lead became the given cari: the lead closes as
// CONVERTED, its open opportunities move to the cari and it leaves every call
// list. The cari itself is created beforehand through the regular cari form,
// so all of its own validation applies.
func (s *Service) ConvertLead(ctx context.Context, session identity.Session, leadID, partyID string, meta identity.RequestMeta) (Lead, error) {
	if !session.HasPermission("crm.edit") {
		return Lead{}, identity.ErrForbidden
	}
	partyID = strings.TrimSpace(partyID)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Lead{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	lead, err := s.getLead(ctx, tx, session, leadID)
	if err != nil {
		return Lead{}, err
	}
	if err = s.ensureParty(ctx, tx, session, partyID); err != nil {
		return Lead{}, err
	}
	tag, err := tx.Exec(ctx, `UPDATE crm_leads SET status='CONVERTED',converted_party_id=$3::uuid,converted_at=now(),next_action_at=NULL,
 updated_at=now(),version=version+1 WHERE company_id=$1 AND id=$2::uuid AND status<>'CONVERTED'`, session.CurrentCompanyID, lead.ID, partyID)
	if err != nil {
		return Lead{}, mapConstraint(err)
	}
	if tag.RowsAffected() == 0 {
		return Lead{}, ErrLeadConverted
	}
	if _, err = tx.Exec(ctx, `UPDATE crm_opportunities SET party_id=$3::uuid,lead_id=NULL,updated_at=now(),version=version+1
 WHERE company_id=$1 AND lead_id=$2::uuid`, session.CurrentCompanyID, lead.ID, partyID); err != nil {
		return Lead{}, err
	}
	if err = closeLeadItems(ctx, tx, session.CurrentCompanyID, lead.ID, ItemDone); err != nil {
		return Lead{}, err
	}
	if err = writeAudit(ctx, tx, session, meta, "CRM_LEAD_CONVERTED", "crm_lead", lead.ID, map[string]any{"party_id": partyID}); err != nil {
		return Lead{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Lead{}, err
	}
	return s.GetLead(ctx, session, lead.ID)
}

// trimDecimal drops trailing fraction zeros from a numeric's text form.
func trimDecimal(value string) string {
	if !strings.Contains(value, ".") {
		return value
	}
	value = strings.TrimRight(value, "0")
	return strings.TrimSuffix(value, ".")
}
