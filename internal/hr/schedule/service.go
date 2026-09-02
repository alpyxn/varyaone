package schedule

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/platform/codeseq"
	"github.com/alpyxn/varyaone/internal/platform/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrNotFound       = errors.New("WORK_SCHEDULE_TEMPLATE_NOT_FOUND")
	ErrOverlap        = errors.New("WORK_SCHEDULE_OVERLAP")
	ErrEmployeeGone   = errors.New("WORK_SCHEDULE_EMPLOYEE_NOT_FOUND")
	ErrDaysInvalid    = errors.New("WORK_SCHEDULE_DAYS_INVALID")
	ErrVersionInUse   = errors.New("WORK_SCHEDULE_VERSION_IN_USE")
	ErrAssignmentGone = errors.New("WORK_SCHEDULE_ASSIGNMENT_NOT_FOUND")
)

type Service struct{ pool database.Querier }

func NewService(pool database.Querier) *Service { return &Service{pool: pool} }

type Template struct {
	ID       string    `json:"id"`
	Code     string    `json:"code"`
	Name     string    `json:"name"`
	IsActive bool      `json:"is_active"`
	Versions []Version `json:"versions,omitempty"`
	Version  int64     `json:"version"`
}

type Version struct {
	ID            string    `json:"id"`
	TemplateID    string    `json:"template_id"`
	EffectiveFrom string    `json:"effective_from"`
	EffectiveTo   *string   `json:"effective_to,omitempty"`
	Days          []DayRow  `json:"days"`
	CreatedAt     time.Time `json:"created_at"`
}

type DayRow struct {
	Weekday        int     `json:"weekday"`
	IsWorkday      bool    `json:"is_workday"`
	StartsAt       *string `json:"starts_at,omitempty"`
	EndsAt         *string `json:"ends_at,omitempty"`
	EndsNextDay    bool    `json:"ends_next_day"`
	BreakMinutes   int     `json:"break_minutes"`
	PlannedMinutes int     `json:"planned_minutes"`
}

type TemplateInput struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

type VersionInput struct {
	EffectiveFrom string   `json:"effective_from"`
	EffectiveTo   string   `json:"effective_to"`
	Days          []DayRow `json:"days"`
}

type Assignment struct {
	ID            string  `json:"id"`
	EmployeeID    string  `json:"employee_id"`
	TemplateID    string  `json:"template_id"`
	TemplateCode  string  `json:"template_code"`
	TemplateName  string  `json:"template_name"`
	EffectiveFrom string  `json:"effective_from"`
	EffectiveTo   *string `json:"effective_to,omitempty"`
	Version       int64   `json:"version"`
}

type AssignmentInput struct {
	TemplateID    string `json:"template_id"`
	EffectiveFrom string `json:"effective_from"`
	EffectiveTo   string `json:"effective_to"`
}

func (s *Service) ListTemplates(ctx context.Context, session identity.Session) ([]Template, error) {
	if !session.HasPermission("hr.schedule.read") {
		return nil, identity.ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `SELECT id::text,code,name,is_active,version FROM work_schedule_templates WHERE company_id=$1 ORDER BY code`, session.CurrentCompanyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Template{}
	for rows.Next() {
		var t Template
		if err := rows.Scan(&t.ID, &t.Code, &t.Name, &t.IsActive, &t.Version); err != nil {
			return nil, err
		}
		items = append(items, t)
	}
	return items, rows.Err()
}

func (s *Service) GetTemplate(ctx context.Context, session identity.Session, id string) (Template, error) {
	if !session.HasPermission("hr.schedule.read") {
		return Template{}, identity.ErrForbidden
	}
	var t Template
	err := s.pool.QueryRow(ctx, `SELECT id::text,code,name,is_active,version FROM work_schedule_templates WHERE company_id=$1 AND id=NULLIF($2,'')::uuid`,
		session.CurrentCompanyID, id).Scan(&t.ID, &t.Code, &t.Name, &t.IsActive, &t.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return Template{}, ErrNotFound
	}
	if err != nil {
		return Template{}, err
	}
	vrows, err := s.pool.Query(ctx, `SELECT id::text,template_id::text,to_char(effective_from,'YYYY-MM-DD'),
 CASE WHEN effective_to IS NULL THEN NULL ELSE to_char(effective_to,'YYYY-MM-DD') END,created_at
 FROM work_schedule_template_versions WHERE company_id=$1 AND template_id=$2 ORDER BY effective_from DESC`,
		session.CurrentCompanyID, t.ID)
	if err != nil {
		return Template{}, err
	}
	defer vrows.Close()
	for vrows.Next() {
		var v Version
		if err := vrows.Scan(&v.ID, &v.TemplateID, &v.EffectiveFrom, &v.EffectiveTo, &v.CreatedAt); err != nil {
			return Template{}, err
		}
		t.Versions = append(t.Versions, v)
	}
	if err := vrows.Err(); err != nil {
		return Template{}, err
	}
	for i := range t.Versions {
		days, err := s.loadDays(ctx, session.CurrentCompanyID, t.Versions[i].ID)
		if err != nil {
			return Template{}, err
		}
		t.Versions[i].Days = days
	}
	return t, nil
}

func (s *Service) loadDays(ctx context.Context, companyID, versionID string) ([]DayRow, error) {
	rows, err := s.pool.Query(ctx, `SELECT weekday,is_workday,to_char(starts_at,'HH24:MI'),to_char(ends_at,'HH24:MI'),ends_next_day,break_minutes,planned_minutes
 FROM work_schedule_days WHERE company_id=$1 AND schedule_version_id=$2 ORDER BY weekday`, companyID, versionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	days := []DayRow{}
	for rows.Next() {
		var d DayRow
		if err := rows.Scan(&d.Weekday, &d.IsWorkday, &d.StartsAt, &d.EndsAt, &d.EndsNextDay, &d.BreakMinutes, &d.PlannedMinutes); err != nil {
			return nil, err
		}
		days = append(days, d)
	}
	return days, rows.Err()
}

func (s *Service) CreateTemplate(ctx context.Context, session identity.Session, input TemplateInput, meta identity.RequestMeta) (Template, error) {
	if !session.HasPermission("hr.schedule.edit") {
		return Template{}, identity.ErrForbidden
	}
	input.Code = strings.TrimSpace(input.Code)
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		return Template{}, fmt.Errorf("%w: ad zorunlu", identity.ErrValidation)
	}
	id := uuid.NewString()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Template{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if input.Code == "" {
		if input.Code, err = codeseq.Next(ctx, tx, session.CurrentCompanyID, "work_schedule_templates", "code", "PLAN"); err != nil {
			return Template{}, err
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO work_schedule_templates(id,company_id,code,name) VALUES($1,$2,$3,$4)`,
		id, session.CurrentCompanyID, input.Code, input.Name); err != nil {
		return Template{}, mapConstraint(err)
	}
	if err = writeEvent(ctx, tx, session, meta, "WORK_SCHEDULE_TEMPLATE_CREATED", "hr.schedule_template.created", id); err != nil {
		return Template{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Template{}, err
	}
	return s.GetTemplate(ctx, session, id)
}

func (s *Service) AddVersion(ctx context.Context, session identity.Session, templateID string, input VersionInput, meta identity.RequestMeta) (Template, error) {
	if !session.HasPermission("hr.schedule.edit") {
		return Template{}, identity.ErrForbidden
	}
	if err := validateDays(input.Days); err != nil {
		return Template{}, err
	}
	if _, err := parseDate(input.EffectiveFrom); err != nil {
		return Template{}, fmt.Errorf("%w: geçerlilik başlangıcı geçersiz", identity.ErrValidation)
	}
	if strings.TrimSpace(input.EffectiveTo) != "" {
		if _, err := parseDate(input.EffectiveTo); err != nil {
			return Template{}, fmt.Errorf("%w: geçerlilik bitişi geçersiz", identity.ErrValidation)
		}
	}
	versionID := uuid.NewString()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Template{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM work_schedule_templates WHERE company_id=$1 AND id=NULLIF($2,'')::uuid)`, session.CurrentCompanyID, templateID).Scan(&exists); err != nil {
		return Template{}, err
	}
	if !exists {
		return Template{}, ErrNotFound
	}
	if _, err = tx.Exec(ctx, `INSERT INTO work_schedule_template_versions(id,company_id,template_id,effective_from,effective_to,created_by)
 VALUES($1,$2,NULLIF($3,'')::uuid,NULLIF($4,'')::date,NULLIF($5,'')::date,$6)`,
		versionID, session.CurrentCompanyID, templateID, input.EffectiveFrom, input.EffectiveTo, session.User.ID); err != nil {
		return Template{}, mapConstraint(err)
	}
	for _, d := range input.Days {
		if _, err = tx.Exec(ctx, `INSERT INTO work_schedule_days(company_id,schedule_version_id,template_id,weekday,is_workday,starts_at,ends_at,ends_next_day,break_minutes,planned_minutes)
 VALUES($1,$2,NULLIF($3,'')::uuid,$4,$5,$6::time,$7::time,$8,$9,$10)`,
			session.CurrentCompanyID, versionID, templateID, d.Weekday, d.IsWorkday, timeOrNil(d.StartsAt), timeOrNil(d.EndsAt), d.EndsNextDay, d.BreakMinutes, d.PlannedMinutes); err != nil {
			return Template{}, mapConstraint(err)
		}
	}
	if err = writeEvent(ctx, tx, session, meta, "WORK_SCHEDULE_VERSION_CREATED", "hr.schedule_version.created", templateID); err != nil {
		return Template{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Template{}, err
	}
	return s.GetTemplate(ctx, session, templateID)
}

func (s *Service) DeleteVersion(ctx context.Context, session identity.Session, templateID, versionID string, meta identity.RequestMeta) (Template, error) {
	if !session.HasPermission("hr.schedule.edit") {
		return Template{}, identity.ErrForbidden
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Template{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM work_schedule_template_versions
 WHERE company_id=$1 AND id=NULLIF($2,'')::uuid AND template_id=NULLIF($3,'')::uuid)`,
		session.CurrentCompanyID, versionID, templateID).Scan(&exists); err != nil {
		return Template{}, err
	}
	if !exists {
		return Template{}, ErrNotFound
	}
	var used bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM timesheet_days WHERE company_id=$1 AND schedule_version_id=NULLIF($2,'')::uuid)`,
		session.CurrentCompanyID, versionID).Scan(&used); err != nil {
		return Template{}, err
	}
	if used {
		return Template{}, ErrVersionInUse
	}
	if _, err = tx.Exec(ctx, `DELETE FROM work_schedule_days WHERE company_id=$1 AND schedule_version_id=NULLIF($2,'')::uuid`,
		session.CurrentCompanyID, versionID); err != nil {
		return Template{}, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM work_schedule_template_versions WHERE company_id=$1 AND id=NULLIF($2,'')::uuid`,
		session.CurrentCompanyID, versionID); err != nil {
		return Template{}, mapConstraint(err)
	}
	if err = writeEvent(ctx, tx, session, meta, "WORK_SCHEDULE_VERSION_DELETED", "hr.schedule_version.deleted", templateID); err != nil {
		return Template{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Template{}, err
	}
	return s.GetTemplate(ctx, session, templateID)
}

func (s *Service) DeleteAssignment(ctx context.Context, session identity.Session, employeeID, assignmentID string, meta identity.RequestMeta) error {
	if !session.HasPermission("hr.schedule.edit") {
		return identity.ErrForbidden
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	tag, err := tx.Exec(ctx, `DELETE FROM employee_schedule_assignments
 WHERE company_id=$1 AND id=NULLIF($2,'')::uuid AND employee_id=NULLIF($3,'')::uuid`,
		session.CurrentCompanyID, assignmentID, employeeID)
	if err != nil {
		return mapConstraint(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrAssignmentGone
	}
	if err = writeEvent(ctx, tx, session, meta, "EMPLOYEE_SCHEDULE_UNASSIGNED", "hr.schedule_assignment.deleted", employeeID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) ListAssignments(ctx context.Context, session identity.Session, employeeID string) ([]Assignment, error) {
	if !session.HasPermission("hr.schedule.read") {
		return nil, identity.ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `SELECT a.id::text,a.employee_id::text,a.template_id::text,t.code,t.name,to_char(a.effective_from,'YYYY-MM-DD'),
 CASE WHEN a.effective_to IS NULL THEN NULL ELSE to_char(a.effective_to,'YYYY-MM-DD') END,a.version
 FROM employee_schedule_assignments a JOIN work_schedule_templates t ON t.company_id=a.company_id AND t.id=a.template_id
 WHERE a.company_id=$1 AND a.employee_id=NULLIF($2,'')::uuid ORDER BY a.effective_from DESC`,
		session.CurrentCompanyID, employeeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Assignment{}
	for rows.Next() {
		var a Assignment
		if err := rows.Scan(&a.ID, &a.EmployeeID, &a.TemplateID, &a.TemplateCode, &a.TemplateName, &a.EffectiveFrom, &a.EffectiveTo, &a.Version); err != nil {
			return nil, err
		}
		items = append(items, a)
	}
	return items, rows.Err()
}

func (s *Service) AssignToEmployee(ctx context.Context, session identity.Session, employeeID string, input AssignmentInput, meta identity.RequestMeta) (Assignment, error) {
	if !session.HasPermission("hr.schedule.edit") {
		return Assignment{}, identity.ErrForbidden
	}
	input.TemplateID = strings.TrimSpace(input.TemplateID)
	if _, err := parseDate(input.EffectiveFrom); err != nil {
		return Assignment{}, fmt.Errorf("%w: geçerlilik başlangıcı geçersiz", identity.ErrValidation)
	}
	if strings.TrimSpace(input.EffectiveTo) != "" {
		if _, err := parseDate(input.EffectiveTo); err != nil {
			return Assignment{}, fmt.Errorf("%w: geçerlilik bitişi geçersiz", identity.ErrValidation)
		}
	}
	id := uuid.NewString()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Assignment{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	_, err = tx.Exec(ctx, `INSERT INTO employee_schedule_assignments(id,company_id,employee_id,template_id,effective_from,effective_to)
 VALUES($1,$2,NULLIF($3,'')::uuid,NULLIF($4,'')::uuid,NULLIF($5,'')::date,NULLIF($6,'')::date)`,
		id, session.CurrentCompanyID, employeeID, input.TemplateID, input.EffectiveFrom, input.EffectiveTo)
	if err != nil {
		return Assignment{}, mapConstraint(err)
	}
	if err = writeEvent(ctx, tx, session, meta, "EMPLOYEE_SCHEDULE_ASSIGNED", "hr.schedule_assignment.created", employeeID); err != nil {
		return Assignment{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Assignment{}, err
	}
	items, err := s.ListAssignments(ctx, session, employeeID)
	if err != nil {
		return Assignment{}, err
	}
	for _, a := range items {
		if a.ID == id {
			return a, nil
		}
	}
	return Assignment{}, ErrNotFound
}

func validateDays(days []DayRow) error {
	if len(days) != 7 {
		return fmt.Errorf("%w: 7 gün girişi zorunlu", ErrDaysInvalid)
	}
	seen := map[int]bool{}
	for _, d := range days {
		if d.Weekday < 1 || d.Weekday > 7 || seen[d.Weekday] {
			return fmt.Errorf("%w: gün numarası geçersiz veya tekrar ediyor", ErrDaysInvalid)
		}
		seen[d.Weekday] = true
		if !d.IsWorkday {
			continue
		}
		if d.StartsAt == nil || d.EndsAt == nil {
			return fmt.Errorf("%w: çalışma günü için başlangıç ve bitiş saati zorunlu", ErrDaysInvalid)
		}
		start, err1 := parseClock(*d.StartsAt)
		end, err2 := parseClock(*d.EndsAt)
		if err1 != nil || err2 != nil {
			return fmt.Errorf("%w: saat biçimi HH:MM olmalı", ErrDaysInvalid)
		}
		minutes := end - start
		if d.EndsNextDay {
			minutes += 24 * 60
		}
		minutes -= d.BreakMinutes
		if minutes <= 0 || minutes != d.PlannedMinutes {
			return fmt.Errorf("%w: planlanan dakika saat aralığıyla uyuşmuyor", ErrDaysInvalid)
		}
	}
	return nil
}

func parseClock(value string) (int, error) {
	t, err := time.Parse("15:04", strings.TrimSpace(value))
	if err != nil {
		return 0, err
	}
	return t.Hour()*60 + t.Minute(), nil
}

func parseDate(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, errors.New("empty")
	}
	return time.Parse("2006-01-02", value)
}

func timeOrNil(value *string) any {
	if value == nil || strings.TrimSpace(*value) == "" {
		return nil
	}
	return strings.TrimSpace(*value)
}

func mapConstraint(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	switch {
	case pgErr.Code == "23P01":
		return ErrOverlap
	case pgErr.Code == "23505" && strings.Contains(pgErr.ConstraintName, "code"):
		return fmt.Errorf("%w: şablon kodu zaten kullanımda", identity.ErrValidation)
	case pgErr.Code == "23514":
		return fmt.Errorf("%w: %s", ErrDaysInvalid, pgErr.Message)
	case pgErr.Code == "23503":
		return ErrEmployeeGone
	}
	return err
}

func writeEvent(ctx context.Context, tx pgx.Tx, session identity.Session, meta identity.RequestMeta, auditType, outboxType, entityID string) error {
	details, _ := json.Marshal(map[string]any{"entity_id": entityID})
	if _, err := tx.Exec(ctx, `INSERT INTO security_audit_events(id,company_id,actor_user_id,event_type,entity_type,entity_id,details,trace_id,source_ip,user_agent)
 VALUES($1,$2,$3,$4,'work_schedule',$5,$6,$7,$8,$9)`,
		uuid.NewString(), session.CurrentCompanyID, session.User.ID, auditType, entityID, details, meta.TraceID, meta.IP, meta.UserAgent); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]string{"entity_id": entityID})
	_, err := tx.Exec(ctx, `INSERT INTO outbox_events(event_id,type,schema_version,company_id,trace_id,payload) VALUES($1,$2,1,$3,$4,$5)`,
		uuid.NewString(), outboxType, session.CurrentCompanyID, meta.TraceID, payload)
	return err
}
