// Package calendar owns public holiday calendars used by timesheet generation.
package calendar

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/platform/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrNotFound      = errors.New("PUBLIC_HOLIDAY_CALENDAR_NOT_FOUND")
	ErrNotDraft      = errors.New("PUBLIC_HOLIDAY_CALENDAR_NOT_DRAFT")
	ErrAlreadyActive = errors.New("PUBLIC_HOLIDAY_CALENDAR_ACTIVE_EXISTS")
)

type Service struct{ pool database.Querier }

func NewService(pool database.Querier) *Service { return &Service{pool: pool} }

type Calendar struct {
	ID           string    `json:"id"`
	CountryCode  string    `json:"country_code"`
	CalendarYear int       `json:"calendar_year"`
	Version      int       `json:"version"`
	Status       string    `json:"status"`
	SourceName   string    `json:"source_name"`
	SourceURL    string    `json:"source_url"`
	Holidays     []Holiday `json:"holidays,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

type Holiday struct {
	ID          string `json:"id"`
	HolidayDate string `json:"holiday_date"`
	Name        string `json:"name"`
	Duration    string `json:"duration"`
}

type CalendarInput struct {
	CountryCode  string `json:"country_code"`
	CalendarYear int    `json:"calendar_year"`
	SourceName   string `json:"source_name"`
	SourceURL    string `json:"source_url"`
}

type HolidayInput struct {
	HolidayDate string `json:"holiday_date"`
	Name        string `json:"name"`
	Duration    string `json:"duration"`
}

var validDuration = map[string]bool{"FULL_DAY": true, "HALF_DAY_AFTERNOON": true}

func (s *Service) List(ctx context.Context, session identity.Session, year int) ([]Calendar, error) {
	if !session.HasPermission("hr.timesheet.read") {
		return nil, identity.ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `SELECT id::text,country_code,calendar_year,version,status,source_name,source_url,created_at
 FROM public_holiday_calendars WHERE company_id=$1 AND ($2=0 OR calendar_year=$2)
 ORDER BY calendar_year DESC,country_code,version DESC`, session.CurrentCompanyID, year)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Calendar{}
	for rows.Next() {
		var c Calendar
		if err := rows.Scan(&c.ID, &c.CountryCode, &c.CalendarYear, &c.Version, &c.Status, &c.SourceName, &c.SourceURL, &c.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, c)
	}
	return items, rows.Err()
}

func (s *Service) Get(ctx context.Context, session identity.Session, id string) (Calendar, error) {
	if !session.HasPermission("hr.timesheet.read") {
		return Calendar{}, identity.ErrForbidden
	}
	var c Calendar
	err := s.pool.QueryRow(ctx, `SELECT id::text,country_code,calendar_year,version,status,source_name,source_url,created_at
 FROM public_holiday_calendars WHERE company_id=$1 AND id=NULLIF($2,'')::uuid`, session.CurrentCompanyID, id).
		Scan(&c.ID, &c.CountryCode, &c.CalendarYear, &c.Version, &c.Status, &c.SourceName, &c.SourceURL, &c.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Calendar{}, ErrNotFound
	}
	if err != nil {
		return Calendar{}, err
	}
	hrows, err := s.pool.Query(ctx, `SELECT id::text,to_char(holiday_date,'YYYY-MM-DD'),name,duration FROM public_holidays
 WHERE company_id=$1 AND calendar_id=$2 ORDER BY holiday_date`, session.CurrentCompanyID, c.ID)
	if err != nil {
		return Calendar{}, err
	}
	defer hrows.Close()
	for hrows.Next() {
		var h Holiday
		if err := hrows.Scan(&h.ID, &h.HolidayDate, &h.Name, &h.Duration); err != nil {
			return Calendar{}, err
		}
		c.Holidays = append(c.Holidays, h)
	}
	return c, hrows.Err()
}

func (s *Service) CreateDraft(ctx context.Context, session identity.Session, input CalendarInput, meta identity.RequestMeta) (Calendar, error) {
	if !session.HasPermission("hr.timesheet.edit") {
		return Calendar{}, identity.ErrForbidden
	}
	input.CountryCode = strings.ToUpper(strings.TrimSpace(input.CountryCode))
	input.SourceName = strings.TrimSpace(input.SourceName)
	input.SourceURL = strings.TrimSpace(input.SourceURL)
	if len(input.CountryCode) != 2 || input.CalendarYear < 2000 || input.CalendarYear > 2200 || input.SourceName == "" || input.SourceURL == "" {
		return Calendar{}, fmt.Errorf("%w: takvim alanları geçersiz", identity.ErrValidation)
	}
	id := uuid.NewString()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Calendar{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var nextVersion int
	if err = tx.QueryRow(ctx, `SELECT COALESCE(MAX(version),0)+1 FROM public_holiday_calendars WHERE company_id=$1 AND country_code=$2 AND calendar_year=$3`,
		session.CurrentCompanyID, input.CountryCode, input.CalendarYear).Scan(&nextVersion); err != nil {
		return Calendar{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO public_holiday_calendars(id,company_id,country_code,calendar_year,version,status,source_name,source_url)
 VALUES($1,$2,$3,$4,$5,'DRAFT',$6,$7)`,
		id, session.CurrentCompanyID, input.CountryCode, input.CalendarYear, nextVersion, input.SourceName, input.SourceURL); err != nil {
		return Calendar{}, mapConstraint(err)
	}
	if err = writeEvent(ctx, tx, session, meta, "PUBLIC_HOLIDAY_CALENDAR_CREATED", "hr.holiday_calendar.created", id); err != nil {
		return Calendar{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Calendar{}, err
	}
	return s.Get(ctx, session, id)
}

func (s *Service) AddHoliday(ctx context.Context, session identity.Session, calendarID string, input HolidayInput, meta identity.RequestMeta) (Calendar, error) {
	if !session.HasPermission("hr.timesheet.edit") {
		return Calendar{}, identity.ErrForbidden
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Duration = strings.ToUpper(strings.TrimSpace(input.Duration))
	if input.Duration == "" {
		input.Duration = "FULL_DAY"
	}
	date, err := time.Parse("2006-01-02", strings.TrimSpace(input.HolidayDate))
	if err != nil || input.Name == "" || !validDuration[input.Duration] {
		return Calendar{}, fmt.Errorf("%w: tatil alanları geçersiz", identity.ErrValidation)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Calendar{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var status string
	var year int
	err = tx.QueryRow(ctx, `SELECT status,calendar_year FROM public_holiday_calendars WHERE company_id=$1 AND id=NULLIF($2,'')::uuid`,
		session.CurrentCompanyID, calendarID).Scan(&status, &year)
	if errors.Is(err, pgx.ErrNoRows) {
		return Calendar{}, ErrNotFound
	}
	if err != nil {
		return Calendar{}, err
	}
	if status != "DRAFT" {
		return Calendar{}, ErrNotDraft
	}
	if date.Year() != year {
		return Calendar{}, fmt.Errorf("%w: tatil tarihi takvim yılına ait değil", identity.ErrValidation)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO public_holidays(id,company_id,calendar_id,calendar_year,holiday_date,name,duration)
 VALUES($1,$2,NULLIF($3,'')::uuid,$4,$5::date,$6,$7)`,
		uuid.NewString(), session.CurrentCompanyID, calendarID, year, input.HolidayDate, input.Name, input.Duration); err != nil {
		return Calendar{}, mapConstraint(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return Calendar{}, err
	}
	return s.Get(ctx, session, calendarID)
}

func (s *Service) Activate(ctx context.Context, session identity.Session, calendarID string, meta identity.RequestMeta) (Calendar, error) {
	if !session.HasPermission("hr.timesheet.edit") {
		return Calendar{}, identity.ErrForbidden
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Calendar{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var status, country string
	var year int
	err = tx.QueryRow(ctx, `SELECT status,country_code,calendar_year FROM public_holiday_calendars WHERE company_id=$1 AND id=NULLIF($2,'')::uuid FOR UPDATE`,
		session.CurrentCompanyID, calendarID).Scan(&status, &country, &year)
	if errors.Is(err, pgx.ErrNoRows) {
		return Calendar{}, ErrNotFound
	}
	if err != nil {
		return Calendar{}, err
	}
	if status != "DRAFT" {
		return Calendar{}, ErrNotDraft
	}
	if _, err = tx.Exec(ctx, `UPDATE public_holiday_calendars SET status='SUPERSEDED'
 WHERE company_id=$1 AND country_code=$2 AND calendar_year=$3 AND status='ACTIVE'`,
		session.CurrentCompanyID, country, year); err != nil {
		return Calendar{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE public_holiday_calendars SET status='ACTIVE',activated_at=now() WHERE company_id=$1 AND id=NULLIF($2,'')::uuid`,
		session.CurrentCompanyID, calendarID); err != nil {
		return Calendar{}, mapConstraint(err)
	}
	if err = writeEvent(ctx, tx, session, meta, "PUBLIC_HOLIDAY_CALENDAR_ACTIVATED", "hr.holiday_calendar.activated", calendarID); err != nil {
		return Calendar{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Calendar{}, err
	}
	return s.Get(ctx, session, calendarID)
}

func mapConstraint(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	if pgErr.Code == "23505" && strings.Contains(pgErr.ConstraintName, "one_active") {
		return ErrAlreadyActive
	}
	if pgErr.Code == "23514" || pgErr.Code == "23505" {
		return fmt.Errorf("%w: %s", identity.ErrValidation, pgErr.Message)
	}
	return err
}

func writeEvent(ctx context.Context, tx pgx.Tx, session identity.Session, meta identity.RequestMeta, auditType, outboxType, entityID string) error {
	details, _ := json.Marshal(map[string]any{"calendar_id": entityID})
	if _, err := tx.Exec(ctx, `INSERT INTO security_audit_events(id,company_id,actor_user_id,event_type,entity_type,entity_id,details,trace_id,source_ip,user_agent)
 VALUES($1,$2,$3,$4,'public_holiday_calendar',$5,$6,$7,$8,$9)`,
		uuid.NewString(), session.CurrentCompanyID, session.User.ID, auditType, entityID, details, meta.TraceID, meta.IP, meta.UserAgent); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]string{"calendar_id": entityID})
	_, err := tx.Exec(ctx, `INSERT INTO outbox_events(event_id,type,schema_version,company_id,trace_id,payload) VALUES($1,$2,1,$3,$4,$5)`,
		uuid.NewString(), outboxType, session.CurrentCompanyID, meta.TraceID, payload)
	return err
}
