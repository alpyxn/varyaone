package email

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/platform/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var ErrTemplateNotFound = errors.New("EMAIL_TEMPLATE_NOT_FOUND")

var validScopes = map[string]bool{"GENERIC": true, "PAYROLL_PAYSLIP": true}

type Template struct {
	ID          string     `json:"id"`
	Code        string     `json:"code"`
	Name        string     `json:"name"`
	Scope       string     `json:"scope"`
	Subject     string     `json:"subject"`
	Body        string     `json:"body"`
	Description string     `json:"description"`
	IsSystem    bool       `json:"is_system"`
	IsActive    bool       `json:"is_active"`
	ArchivedAt  *time.Time `json:"archived_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	Version     int64      `json:"version"`
}

type TemplateInput struct {
	Code        string `json:"code"`
	Name        string `json:"name"`
	Scope       string `json:"scope"`
	Subject     string `json:"subject"`
	Body        string `json:"body"`
	Description string `json:"description"`
}

type TemplateService struct{ pool database.Querier }

func NewTemplateService(pool database.Querier) *TemplateService { return &TemplateService{pool: pool} }

const templateColumns = `id::text,code,name,scope,subject,body,description,is_system,archived_at,created_at,version`

func scanTemplate(row pgx.Row) (Template, error) {
	var t Template
	err := row.Scan(&t.ID, &t.Code, &t.Name, &t.Scope, &t.Subject, &t.Body, &t.Description,
		&t.IsSystem, &t.ArchivedAt, &t.CreatedAt, &t.Version)
	t.IsActive = t.ArchivedAt == nil
	return t, err
}

func (s *TemplateService) List(ctx context.Context, session identity.Session, scope string, includeArchived bool) ([]Template, error) {
	if !session.HasPermission("communication.email.send") && !session.HasPermission("communication.email.template.manage") {
		return nil, identity.ErrForbidden
	}
	scope = strings.ToUpper(strings.TrimSpace(scope))
	rows, err := s.pool.Query(ctx, `SELECT `+templateColumns+` FROM email_templates
 WHERE company_id=$1 AND ($2 OR archived_at IS NULL) AND ($3='' OR scope=$3)
 ORDER BY archived_at IS NOT NULL, name`, session.CurrentCompanyID, includeArchived, scope)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Template{}
	for rows.Next() {
		t, err := scanTemplate(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, t)
	}
	return items, rows.Err()
}

func (s *TemplateService) Get(ctx context.Context, session identity.Session, id string) (Template, error) {
	if !session.HasPermission("communication.email.send") && !session.HasPermission("communication.email.template.manage") {
		return Template{}, identity.ErrForbidden
	}
	return s.get(ctx, session.CurrentCompanyID, id)
}

func (s *TemplateService) get(ctx context.Context, companyID, id string) (Template, error) {
	t, err := scanTemplate(s.pool.QueryRow(ctx, `SELECT `+templateColumns+` FROM email_templates
 WHERE company_id=$1 AND id=NULLIF($2,'')::uuid`, companyID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Template{}, ErrTemplateNotFound
	}
	return t, err
}

func normalizeTemplate(input *TemplateInput) {
	input.Code = strings.ToUpper(strings.TrimSpace(input.Code))
	input.Name = strings.TrimSpace(input.Name)
	input.Scope = strings.ToUpper(strings.TrimSpace(input.Scope))
	input.Subject = strings.TrimSpace(input.Subject)
	input.Description = strings.TrimSpace(input.Description)
	if input.Scope == "" {
		input.Scope = "GENERIC"
	}
}

func validateTemplate(input TemplateInput) error {
	if input.Name == "" {
		return fmt.Errorf("%w: taslak adı zorunlu", identity.ErrValidation)
	}
	if !validScopes[input.Scope] {
		return fmt.Errorf("%w: geçersiz taslak kapsamı", identity.ErrValidation)
	}
	if strings.TrimSpace(input.Subject) == "" && strings.TrimSpace(input.Body) == "" {
		return fmt.Errorf("%w: konu veya gövde girilmeli", identity.ErrValidation)
	}
	return nil
}

func (s *TemplateService) Create(ctx context.Context, session identity.Session, input TemplateInput, meta identity.RequestMeta) (Template, error) {
	if !session.HasPermission("communication.email.template.manage") {
		return Template{}, identity.ErrForbidden
	}
	normalizeTemplate(&input)
	if err := validateTemplate(input); err != nil {
		return Template{}, err
	}
	if input.Code == "" {
		input.Code = slugCode(input.Name)
	}
	id := uuid.NewString()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Template{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err = tx.Exec(ctx, `INSERT INTO email_templates(id,company_id,code,name,scope,subject,body,description,is_system)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,false)`,
		id, session.CurrentCompanyID, input.Code, input.Name, input.Scope, input.Subject, input.Body, input.Description); err != nil {
		return Template{}, mapTemplateConstraint(err)
	}
	if err = writeTemplateEvent(ctx, tx, session, meta, "EMAIL_TEMPLATE_CREATED", "email_template.created", id, map[string]any{"code": input.Code}); err != nil {
		return Template{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Template{}, err
	}
	return s.get(ctx, session.CurrentCompanyID, id)
}

func (s *TemplateService) Update(ctx context.Context, session identity.Session, id string, version int64, input TemplateInput, meta identity.RequestMeta) (Template, error) {
	if !session.HasPermission("communication.email.template.manage") {
		return Template{}, identity.ErrForbidden
	}
	normalizeTemplate(&input)
	if err := validateTemplate(input); err != nil {
		return Template{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Template{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	// System templates keep their code and scope; only the text is editable.
	tag, err := tx.Exec(ctx, `UPDATE email_templates SET
 name=$4,subject=$5,body=$6,description=$7,
 code=CASE WHEN is_system OR $8='' THEN code ELSE $8 END,
 scope=CASE WHEN is_system THEN scope ELSE $9 END,
 updated_at=now(),version=version+1
 WHERE company_id=$1 AND id=NULLIF($2,'')::uuid AND version=$3`,
		session.CurrentCompanyID, id, version, input.Name, input.Subject, input.Body, input.Description, input.Code, input.Scope)
	if err != nil {
		return Template{}, mapTemplateConstraint(err)
	}
	if tag.RowsAffected() == 0 {
		if _, gErr := s.get(ctx, session.CurrentCompanyID, id); errors.Is(gErr, ErrTemplateNotFound) {
			return Template{}, ErrTemplateNotFound
		}
		return Template{}, identity.ErrConflict
	}
	if err = writeTemplateEvent(ctx, tx, session, meta, "EMAIL_TEMPLATE_UPDATED", "email_template.updated", id, map[string]any{"name": input.Name}); err != nil {
		return Template{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Template{}, err
	}
	return s.get(ctx, session.CurrentCompanyID, id)
}

func (s *TemplateService) SetActive(ctx context.Context, session identity.Session, id string, active bool, meta identity.RequestMeta) (Template, error) {
	if !session.HasPermission("communication.email.template.manage") {
		return Template{}, identity.ErrForbidden
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Template{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	tag, err := tx.Exec(ctx, `UPDATE email_templates
 SET archived_at=CASE WHEN $3 THEN NULL ELSE now() END,updated_at=now(),version=version+1
 WHERE company_id=$1 AND id=NULLIF($2,'')::uuid`, session.CurrentCompanyID, id, active)
	if err != nil {
		return Template{}, err
	}
	if tag.RowsAffected() == 0 {
		return Template{}, ErrTemplateNotFound
	}
	event := "EMAIL_TEMPLATE_DEACTIVATED"
	if active {
		event = "EMAIL_TEMPLATE_ACTIVATED"
	}
	if err = writeTemplateEvent(ctx, tx, session, meta, event, "email_template.status_changed", id, map[string]any{"active": active}); err != nil {
		return Template{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Template{}, err
	}
	return s.get(ctx, session.CurrentCompanyID, id)
}

// DefaultForScope returns the first active template for a scope, or a zero
// Template (no error) when none exists.
func (s *TemplateService) DefaultForScope(ctx context.Context, companyID, scope string) (Template, error) {
	t, err := scanTemplate(s.pool.QueryRow(ctx, `SELECT `+templateColumns+` FROM email_templates
 WHERE company_id=$1 AND scope=$2 AND archived_at IS NULL
 ORDER BY is_system DESC, name LIMIT 1`, companyID, strings.ToUpper(scope)))
	if errors.Is(err, pgx.ErrNoRows) {
		return Template{}, nil
	}
	return t, err
}

func mapTemplateConstraint(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return fmt.Errorf("%w: bu taslak kodu zaten kullanımda", identity.ErrValidation)
	}
	return err
}

func slugCode(name string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(name) {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_' || r == '/':
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		out = "TASLAK_" + uuid.NewString()[:8]
	}
	if len(out) > 40 {
		out = out[:40]
	}
	return out
}

var placeholderRE = regexp.MustCompile(`\{\{\s*([a-zA-Z0-9_]+)\s*\}\}`)

// RenderText substitutes {{key}} placeholders with values from vars. Unknown
// placeholders are left untouched so a half-filled preview stays readable.
func RenderText(text string, vars map[string]string) string {
	return placeholderRE.ReplaceAllStringFunc(text, func(match string) string {
		key := placeholderRE.FindStringSubmatch(match)[1]
		if v, ok := vars[key]; ok {
			return v
		}
		return match
	})
}

func writeTemplateEvent(ctx context.Context, tx pgx.Tx, session identity.Session, meta identity.RequestMeta, auditType, outboxType, id string, details map[string]any) error {
	detailBytes, _ := json.Marshal(details)
	if _, err := tx.Exec(ctx, `INSERT INTO security_audit_events(id,company_id,actor_user_id,event_type,entity_type,entity_id,details,trace_id,source_ip,user_agent)
 VALUES($1,$2,$3,$4,'email_template',$5,$6,$7,$8,$9)`,
		uuid.NewString(), session.CurrentCompanyID, session.User.ID, auditType, id, detailBytes, meta.TraceID, meta.IP, meta.UserAgent); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]string{"template_id": id})
	_, err := tx.Exec(ctx, `INSERT INTO outbox_events(event_id,type,schema_version,company_id,trace_id,payload) VALUES($1,$2,1,$3,$4,$5)`,
		uuid.NewString(), outboxType, session.CurrentCompanyID, meta.TraceID, payload)
	return err
}
