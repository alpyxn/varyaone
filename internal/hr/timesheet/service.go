package timesheet

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	ErrPeriodNotFound = errors.New("TIMESHEET_PERIOD_NOT_FOUND")
	ErrDayNotFound    = errors.New("TIMESHEET_DAY_NOT_FOUND")
	ErrFinalized      = errors.New("TIMESHEET_PERIOD_FINALIZED")
	ErrNotFinalized   = errors.New("TIMESHEET_PERIOD_NOT_FINALIZED")
	ErrUsedByPayroll  = errors.New("TIMESHEET_USED_BY_FINALIZED_PAYROLL")
	ErrPeriodExists   = errors.New("TIMESHEET_PERIOD_EXISTS")
)

type Service struct{ pool database.Querier }

func NewService(pool database.Querier) *Service { return &Service{pool: pool} }

type Period struct {
	ID          string      `json:"id"`
	PeriodYear  int         `json:"period_year"`
	PeriodMonth int         `json:"period_month"`
	Status      string      `json:"status"`
	Generation  int         `json:"generation"`
	Checksum    *string     `json:"checksum,omitempty"`
	FinalizedAt *time.Time  `json:"finalized_at,omitempty"`
	Days        []DayDetail `json:"days,omitempty"`
	Version     int64       `json:"version"`
}

type DayDetail struct {
	ID                   string  `json:"id"`
	EmployeeID           string  `json:"employee_id"`
	EmployeeName         string  `json:"employee_name"`
	WorkDate             string  `json:"work_date"`
	Source               string  `json:"source"`
	PlannedMinutes       int     `json:"planned_minutes"`
	WorkedMinutes        int     `json:"worked_minutes"`
	PaidLeaveMinutes     int     `json:"paid_leave_minutes"`
	UnpaidLeaveMinutes   int     `json:"unpaid_leave_minutes"`
	OvertimeMinutes      int     `json:"overtime_minutes"`
	WeekRestMinutes      int     `json:"week_rest_minutes"`
	PublicHolidayMinutes int     `json:"public_holiday_minutes"`
	AbsenceMinutes       int     `json:"absence_minutes"`
	Explanation          string  `json:"explanation"`
	LeaveTypeID          *string `json:"leave_type_id,omitempty"`
	LeaveCode            *string `json:"leave_code,omitempty"`
	LeaveName            *string `json:"leave_name,omitempty"`
	Version              int64   `json:"version"`
}

type PeriodInput struct {
	PeriodYear  int `json:"period_year"`
	PeriodMonth int `json:"period_month"`
}

type DayInput struct {
	WorkedMinutes        int     `json:"worked_minutes"`
	PaidLeaveMinutes     int     `json:"paid_leave_minutes"`
	UnpaidLeaveMinutes   int     `json:"unpaid_leave_minutes"`
	OvertimeMinutes      int     `json:"overtime_minutes"`
	WeekRestMinutes      int     `json:"week_rest_minutes"`
	PublicHolidayMinutes int     `json:"public_holiday_minutes"`
	AbsenceMinutes       int     `json:"absence_minutes"`
	Explanation          string  `json:"explanation"`
	LeaveTypeID          *string `json:"leave_type_id,omitempty"`
}

func (s *Service) ListPeriods(ctx context.Context, session identity.Session) ([]Period, error) {
	if !session.HasPermission("hr.timesheet.read") {
		return nil, identity.ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `SELECT id::text,period_year,period_month,status,generation,checksum,finalized_at,version
 FROM timesheet_periods WHERE company_id=$1 ORDER BY period_year DESC,period_month DESC`, session.CurrentCompanyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Period{}
	for rows.Next() {
		var p Period
		if err := rows.Scan(&p.ID, &p.PeriodYear, &p.PeriodMonth, &p.Status, &p.Generation, &p.Checksum, &p.FinalizedAt, &p.Version); err != nil {
			return nil, err
		}
		items = append(items, p)
	}
	return items, rows.Err()
}

func (s *Service) GetPeriod(ctx context.Context, session identity.Session, id string) (Period, error) {
	if !session.HasPermission("hr.timesheet.read") {
		return Period{}, identity.ErrForbidden
	}
	var p Period
	err := s.pool.QueryRow(ctx, `SELECT id::text,period_year,period_month,status,generation,checksum,finalized_at,version
 FROM timesheet_periods WHERE company_id=$1 AND id=NULLIF($2,'')::uuid`, session.CurrentCompanyID, id).
		Scan(&p.ID, &p.PeriodYear, &p.PeriodMonth, &p.Status, &p.Generation, &p.Checksum, &p.FinalizedAt, &p.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return Period{}, ErrPeriodNotFound
	}
	if err != nil {
		return Period{}, err
	}
	drows, err := s.pool.Query(ctx, `SELECT d.id::text,d.employee_id::text,e.first_name||' '||e.last_name,to_char(d.work_date,'YYYY-MM-DD'),d.source,
 d.planned_minutes,d.worked_minutes,d.paid_leave_minutes,d.unpaid_leave_minutes,d.overtime_minutes,d.week_rest_minutes,d.public_holiday_minutes,d.absence_minutes,d.explanation,
 d.leave_type_id::text,t.code,t.name,d.version
 FROM timesheet_days d JOIN employees e ON e.company_id=d.company_id AND e.id=d.employee_id
 LEFT JOIN leave_types t ON t.company_id=d.company_id AND t.id=d.leave_type_id
 WHERE d.company_id=$1 AND d.period_id=$2 ORDER BY e.first_name,e.last_name,d.work_date`, session.CurrentCompanyID, p.ID)
	if err != nil {
		return Period{}, err
	}
	defer drows.Close()
	for drows.Next() {
		var d DayDetail
		var leaveTypeID, leaveCode, leaveName *string
		if err := drows.Scan(&d.ID, &d.EmployeeID, &d.EmployeeName, &d.WorkDate, &d.Source, &d.PlannedMinutes, &d.WorkedMinutes,
			&d.PaidLeaveMinutes, &d.UnpaidLeaveMinutes, &d.OvertimeMinutes, &d.WeekRestMinutes, &d.PublicHolidayMinutes, &d.AbsenceMinutes, &d.Explanation,
			&leaveTypeID, &leaveCode, &leaveName, &d.Version); err != nil {
			return Period{}, err
		}
		d.LeaveTypeID, d.LeaveCode, d.LeaveName = leaveTypeID, leaveCode, leaveName
		p.Days = append(p.Days, d)
	}
	return p, drows.Err()
}

func (s *Service) CreatePeriod(ctx context.Context, session identity.Session, input PeriodInput, meta identity.RequestMeta) (Period, error) {
	if !session.HasPermission("hr.timesheet.edit") {
		return Period{}, identity.ErrForbidden
	}
	if input.PeriodYear < 2000 || input.PeriodYear > 2200 || input.PeriodMonth < 1 || input.PeriodMonth > 12 {
		return Period{}, fmt.Errorf("%w: dönem yıl/ay geçersiz", identity.ErrValidation)
	}
	id := uuid.NewString()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Period{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err = tx.Exec(ctx, `INSERT INTO timesheet_periods(id,company_id,period_year,period_month) VALUES($1,$2,$3,$4)`,
		id, session.CurrentCompanyID, input.PeriodYear, input.PeriodMonth); err != nil {
		return Period{}, mapConstraint(err)
	}
	if err = writeEvent(ctx, tx, session, meta, "TIMESHEET_PERIOD_CREATED", "hr.timesheet_period.created", id); err != nil {
		return Period{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Period{}, err
	}
	return s.GetPeriod(ctx, session, id)
}

// Generate builds GENERATED attendance days for every active employee whose
// employment intersects the period month. MANUAL days are preserved.
func (s *Service) Generate(ctx context.Context, session identity.Session, periodID string, meta identity.RequestMeta) (Period, error) {
	if !session.HasPermission("hr.timesheet.edit") {
		return Period{}, identity.ErrForbidden
	}
	period, err := s.GetPeriod(ctx, session, periodID)
	if err != nil {
		return Period{}, err
	}
	if period.Status == "FINALIZED" {
		return Period{}, ErrFinalized
	}
	year, month := period.PeriodYear, time.Month(period.PeriodMonth)
	monthStart := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
	monthEnd := monthStart.AddDate(0, 1, -1)

	holidays, err := s.loadHolidays(ctx, session.CurrentCompanyID, year)
	if err != nil {
		return Period{}, err
	}

	employees, err := s.loadEmployments(ctx, session.CurrentCompanyID, monthStart, monthEnd)
	if err != nil {
		return Period{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Period{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if _, err = tx.Exec(ctx, `DELETE FROM timesheet_days WHERE company_id=$1 AND period_id=NULLIF($2,'')::uuid AND source='GENERATED'`,
		session.CurrentCompanyID, periodID); err != nil {
		return Period{}, mapConstraint(err)
	}

	for _, emp := range employees {
		schedule, scheduleVersionID, err := s.loadSchedule(ctx, session.CurrentCompanyID, emp.EmployeeID, monthStart)
		if err != nil {
			return Period{}, err
		}
		manual, err := s.loadManualDays(ctx, tx, session.CurrentCompanyID, periodID, emp.EmployeeID)
		if err != nil {
			return Period{}, err
		}
		result, err := Generate(Input{
			Year: year, Month: month,
			EmploymentFrom: emp.From, EmploymentTo: emp.To,
			Schedule:       schedule,
			Holidays:       holidays,
			ExistingManual: manual,
		})
		if err != nil {
			return Period{}, err
		}
		for _, day := range result.Days {
			if day.Source != "GENERATED" {
				continue
			}
			if _, err = tx.Exec(ctx, `INSERT INTO timesheet_days(id,company_id,period_id,employee_id,work_date,source,schedule_version_id,
 planned_minutes,worked_minutes,paid_leave_minutes,unpaid_leave_minutes,public_holiday_minutes,week_rest_minutes)
 VALUES($1,$2,NULLIF($3,'')::uuid,NULLIF($4,'')::uuid,$5::date,'GENERATED',$6,$7,$8,$9,$10,$11,$12)`,
				uuid.NewString(), session.CurrentCompanyID, periodID, emp.EmployeeID, day.Date, nullableUUID(scheduleVersionID),
				day.PlannedMinutes, day.WorkedMinutes, day.PaidLeaveMinutes, day.UnpaidLeaveMinutes, day.PublicHolidayMinutes, day.WeekRestMinutes); err != nil {
				return Period{}, mapConstraint(err)
			}
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE timesheet_periods SET generation=generation+1,updated_at=now(),version=version+1 WHERE company_id=$1 AND id=NULLIF($2,'')::uuid`,
		session.CurrentCompanyID, periodID); err != nil {
		return Period{}, err
	}
	if err = writeEvent(ctx, tx, session, meta, "TIMESHEET_GENERATED", "hr.timesheet.generated", periodID); err != nil {
		return Period{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Period{}, err
	}
	return s.GetPeriod(ctx, session, periodID)
}

func (s *Service) UpdateDay(ctx context.Context, session identity.Session, periodID, dayID string, version int64, input DayInput, meta identity.RequestMeta) (Period, error) {
	if !session.HasPermission("hr.timesheet.edit") {
		return Period{}, identity.ErrForbidden
	}
	for _, v := range []int{input.WorkedMinutes, input.PaidLeaveMinutes, input.UnpaidLeaveMinutes, input.OvertimeMinutes, input.WeekRestMinutes, input.PublicHolidayMinutes, input.AbsenceMinutes} {
		if v < 0 || v > 1440 {
			return Period{}, fmt.Errorf("%w: dakika değeri 0-1440 aralığında olmalı", identity.ErrValidation)
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Period{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	leaveTypeID := ""
	if input.LeaveTypeID != nil {
		leaveTypeID = strings.TrimSpace(*input.LeaveTypeID)
	}
	tag, err := tx.Exec(ctx, `UPDATE timesheet_days SET source='MANUAL',worked_minutes=$4,paid_leave_minutes=$5,unpaid_leave_minutes=$6,
 overtime_minutes=$7,week_rest_minutes=$8,public_holiday_minutes=$9,absence_minutes=$10,explanation=$11,leave_type_id=NULLIF($13,'')::uuid,updated_at=now(),version=version+1
 WHERE company_id=$1 AND period_id=NULLIF($2,'')::uuid AND id=NULLIF($3,'')::uuid AND version=$12`,
		session.CurrentCompanyID, periodID, dayID, input.WorkedMinutes, input.PaidLeaveMinutes, input.UnpaidLeaveMinutes,
		input.OvertimeMinutes, input.WeekRestMinutes, input.PublicHolidayMinutes, input.AbsenceMinutes, strings.TrimSpace(input.Explanation), version, leaveTypeID)
	if err != nil {
		return Period{}, mapConstraint(err)
	}
	if tag.RowsAffected() == 0 {
		return Period{}, ErrDayNotFound
	}
	if err = writeEvent(ctx, tx, session, meta, "TIMESHEET_DAY_UPDATED", "hr.timesheet_day.updated", periodID); err != nil {
		return Period{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Period{}, err
	}
	return s.GetPeriod(ctx, session, periodID)
}

// DayUpsertInput marks one calendar day for one employee. Kind is the simple
// state the UI works with; Minutes optionally overrides the day length,
// otherwise the work schedule (or a full 8h day) is used.
type DayUpsertInput struct {
	EmployeeID  string  `json:"employee_id"`
	WorkDate    string  `json:"work_date"`
	Kind        string  `json:"kind"`
	Minutes     int     `json:"minutes"`
	Explanation string  `json:"explanation"`
	LeaveTypeID *string `json:"leave_type_id,omitempty"`
}

var dayKinds = map[string]bool{
	"WORKED": true, "HALF_DAY": true, "PAID_LEAVE": true, "UNPAID_LEAVE": true,
	"PUBLIC_HOLIDAY": true, "ABSENT": true, "WEEK_REST": true,
}

var leaveDayKinds = map[string]bool{"PAID_LEAVE": true, "UNPAID_LEAVE": true}

func bucketsForKind(kind string, planned int, leaveTypeID string) DayInput {
	d := DayInput{}
	switch kind {
	case "WORKED":
		d.WorkedMinutes = planned
	case "HALF_DAY":
		d.WorkedMinutes = planned / 2
	case "PAID_LEAVE":
		d.PaidLeaveMinutes = planned
	case "UNPAID_LEAVE":
		d.UnpaidLeaveMinutes = planned
	case "PUBLIC_HOLIDAY":
		d.PublicHolidayMinutes = planned
	case "ABSENT":
		d.AbsenceMinutes = planned
	case "WEEK_REST":
		// no paid minutes on a rest day
	}
	if leaveDayKinds[kind] && leaveTypeID != "" {
		d.LeaveTypeID = &leaveTypeID
	}
	return d
}

// UpsertDay creates or replaces a MANUAL timesheet day. The period must be DRAFT.
func (s *Service) UpsertDay(ctx context.Context, session identity.Session, periodID string, input DayUpsertInput, meta identity.RequestMeta) (Period, error) {
	if !session.HasPermission("hr.timesheet.edit") {
		return Period{}, identity.ErrForbidden
	}
	period, err := s.GetPeriod(ctx, session, periodID)
	if err != nil {
		return Period{}, err
	}
	if period.Status == "FINALIZED" {
		return Period{}, ErrFinalized
	}
	input.EmployeeID = strings.TrimSpace(input.EmployeeID)
	kind := strings.ToUpper(strings.TrimSpace(input.Kind))
	if input.EmployeeID == "" || !dayKinds[kind] {
		return Period{}, fmt.Errorf("%w: çalışan ve gün türü zorunlu", identity.ErrValidation)
	}
	date, derr := time.Parse("2006-01-02", strings.TrimSpace(input.WorkDate))
	if derr != nil || date.Year() != period.PeriodYear || int(date.Month()) != period.PeriodMonth {
		return Period{}, fmt.Errorf("%w: gün, puantaj dönemi içinde olmalı", identity.ErrValidation)
	}

	leaveTypeID := ""
	if input.LeaveTypeID != nil {
		leaveTypeID = strings.TrimSpace(*input.LeaveTypeID)
	}
	if leaveDayKinds[kind] {
		if leaveTypeID == "" {
			return Period{}, fmt.Errorf("%w: izin günü için izin türü zorunlu", identity.ErrValidation)
		}
		var treatment string
		err := s.pool.QueryRow(ctx, `SELECT payroll_treatment FROM leave_types WHERE company_id=$1 AND id=NULLIF($2,'')::uuid`,
			session.CurrentCompanyID, leaveTypeID).Scan(&treatment)
		if errors.Is(err, pgx.ErrNoRows) {
			return Period{}, fmt.Errorf("%w: izin türü bulunamadı", identity.ErrValidation)
		}
		if err != nil {
			return Period{}, err
		}
		wantUnpaid := treatment == "UNPAID"
		if wantUnpaid != (kind == "UNPAID_LEAVE") {
			return Period{}, fmt.Errorf("%w: izin türü ve gün durumu uyuşmuyor", identity.ErrValidation)
		}
	} else {
		leaveTypeID = ""
	}

	planned := input.Minutes
	if planned <= 0 {
		_ = s.pool.QueryRow(ctx, `SELECT planned_minutes FROM timesheet_days
 WHERE company_id=$1 AND period_id=NULLIF($2,'')::uuid AND employee_id=NULLIF($3,'')::uuid AND work_date=$4::date`,
			session.CurrentCompanyID, periodID, input.EmployeeID, input.WorkDate).Scan(&planned)
	}
	if planned <= 0 {
		if sched, _, serr := s.loadSchedule(ctx, session.CurrentCompanyID, input.EmployeeID, date); serr == nil {
			planned = sched[date.Weekday()].PlannedMinutes
		}
	}
	if planned <= 0 {
		planned = 480
	}
	if planned > 1440 {
		planned = 1440
	}
	d := bucketsForKind(kind, planned, leaveTypeID)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Period{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err = tx.Exec(ctx, `INSERT INTO timesheet_days(id,company_id,period_id,employee_id,work_date,source,
 planned_minutes,worked_minutes,paid_leave_minutes,unpaid_leave_minutes,week_rest_minutes,public_holiday_minutes,absence_minutes,explanation,leave_type_id)
 VALUES($1,$2,NULLIF($3,'')::uuid,NULLIF($4,'')::uuid,$5::date,'MANUAL',$6,$7,$8,$9,$10,$11,$12,$13,NULLIF($14,'')::uuid)
 ON CONFLICT (company_id,period_id,employee_id,work_date) DO UPDATE SET source='MANUAL',
 planned_minutes=EXCLUDED.planned_minutes,worked_minutes=EXCLUDED.worked_minutes,paid_leave_minutes=EXCLUDED.paid_leave_minutes,
 unpaid_leave_minutes=EXCLUDED.unpaid_leave_minutes,week_rest_minutes=EXCLUDED.week_rest_minutes,
 public_holiday_minutes=EXCLUDED.public_holiday_minutes,absence_minutes=EXCLUDED.absence_minutes,leave_type_id=EXCLUDED.leave_type_id,
 overtime_minutes=0,explanation=EXCLUDED.explanation,updated_at=now(),version=timesheet_days.version+1`,
		uuid.NewString(), session.CurrentCompanyID, periodID, input.EmployeeID, input.WorkDate,
		planned, d.WorkedMinutes, d.PaidLeaveMinutes, d.UnpaidLeaveMinutes, d.WeekRestMinutes, d.PublicHolidayMinutes, d.AbsenceMinutes,
		strings.TrimSpace(input.Explanation), leaveTypeID); err != nil {
		return Period{}, mapConstraint(err)
	}
	if err = writeEvent(ctx, tx, session, meta, "TIMESHEET_DAY_UPDATED", "hr.timesheet_day.updated", periodID); err != nil {
		return Period{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Period{}, err
	}
	return s.GetPeriod(ctx, session, periodID)
}

// DeleteDay removes a timesheet day (the day is then treated as unrecorded).
func (s *Service) DeleteDay(ctx context.Context, session identity.Session, periodID, dayID string, meta identity.RequestMeta) (Period, error) {
	if !session.HasPermission("hr.timesheet.edit") {
		return Period{}, identity.ErrForbidden
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Period{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	tag, err := tx.Exec(ctx, `DELETE FROM timesheet_days
 WHERE company_id=$1 AND period_id=NULLIF($2,'')::uuid AND id=NULLIF($3,'')::uuid`,
		session.CurrentCompanyID, periodID, dayID)
	if err != nil {
		return Period{}, mapConstraint(err)
	}
	if tag.RowsAffected() == 0 {
		return Period{}, ErrDayNotFound
	}
	if err = writeEvent(ctx, tx, session, meta, "TIMESHEET_DAY_UPDATED", "hr.timesheet_day.updated", periodID); err != nil {
		return Period{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Period{}, err
	}
	return s.GetPeriod(ctx, session, periodID)
}

func (s *Service) Finalize(ctx context.Context, session identity.Session, periodID string, version int64, meta identity.RequestMeta) (Period, error) {
	if !session.HasPermission("hr.timesheet.finalize") {
		return Period{}, identity.ErrForbidden
	}
	period, err := s.GetPeriod(ctx, session, periodID)
	if err != nil {
		return Period{}, err
	}
	if period.Status == "FINALIZED" {
		return period, nil
	}
	checksum := checksumDays(period.Days)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Period{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	tag, err := tx.Exec(ctx, `UPDATE timesheet_periods SET status='FINALIZED',checksum=$3,finalized_at=now(),finalized_by=$4,updated_at=now(),version=version+1
 WHERE company_id=$1 AND id=NULLIF($2,'')::uuid AND status='DRAFT' AND version=$5`,
		session.CurrentCompanyID, periodID, checksum, session.User.ID, version)
	if err != nil {
		return Period{}, mapConstraint(err)
	}
	if tag.RowsAffected() == 0 {
		return Period{}, identity.ErrConflict
	}
	if err = writeEvent(ctx, tx, session, meta, "TIMESHEET_FINALIZED", "hr.timesheet.finalized", periodID); err != nil {
		return Period{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Period{}, err
	}
	return s.GetPeriod(ctx, session, periodID)
}

func (s *Service) Reopen(ctx context.Context, session identity.Session, periodID string, version int64, reason string, meta identity.RequestMeta) (Period, error) {
	if !session.HasPermission("hr.timesheet.reopen") {
		return Period{}, identity.ErrForbidden
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return Period{}, fmt.Errorf("%w: yeniden açma gerekçesi zorunlu", identity.ErrValidation)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Period{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	tag, err := tx.Exec(ctx, `UPDATE timesheet_periods SET status='DRAFT',checksum=NULL,finalized_at=NULL,finalized_by=NULL,
 reopen_reason=$3,reopened_at=now(),reopened_by=$4,updated_at=now(),version=version+1
 WHERE company_id=$1 AND id=NULLIF($2,'')::uuid AND status='FINALIZED' AND version=$5`,
		session.CurrentCompanyID, periodID, reason, session.User.ID, version)
	if err != nil {
		return Period{}, mapConstraint(err)
	}
	if tag.RowsAffected() == 0 {
		return Period{}, ErrNotFinalized
	}
	if err = writeEvent(ctx, tx, session, meta, "TIMESHEET_REOPENED", "hr.timesheet.reopened", periodID); err != nil {
		return Period{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Period{}, err
	}
	return s.GetPeriod(ctx, session, periodID)
}

// ---- loaders ----

type employmentSpan struct {
	EmployeeID string
	From       time.Time
	To         *time.Time
}

func (s *Service) loadEmployments(ctx context.Context, companyID string, monthStart, monthEnd time.Time) ([]employmentSpan, error) {
	rows, err := s.pool.Query(ctx, `SELECT e.id::text,min(x.start_date),max(COALESCE(x.end_date,DATE '9999-12-31'))
 FROM employees e JOIN employments x ON x.company_id=e.company_id AND x.employee_id=e.id
 WHERE e.company_id=$1 AND e.status='ACTIVE'
   AND daterange(x.start_date,COALESCE(x.end_date,'infinity'::date),'[]') && daterange($2::date,$3::date,'[]')
 GROUP BY e.id ORDER BY e.id`, companyID, monthStart.Format("2006-01-02"), monthEnd.Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	spans := []employmentSpan{}
	for rows.Next() {
		var span employmentSpan
		var from, to time.Time
		if err := rows.Scan(&span.EmployeeID, &from, &to); err != nil {
			return nil, err
		}
		span.From = from
		if to.Year() < 9999 {
			t := to
			span.To = &t
		}
		spans = append(spans, span)
	}
	return spans, rows.Err()
}

func (s *Service) loadSchedule(ctx context.Context, companyID, employeeID string, monthStart time.Time) (map[time.Weekday]ScheduleDay, string, error) {
	var templateID string
	err := s.pool.QueryRow(ctx, `SELECT template_id::text FROM employee_schedule_assignments
 WHERE company_id=$1 AND employee_id=NULLIF($2,'')::uuid
   AND daterange(effective_from,COALESCE(effective_to,'infinity'::date),'[]') @> $3::date LIMIT 1`,
		companyID, employeeID, monthStart.Format("2006-01-02")).Scan(&templateID)
	if errors.Is(err, pgx.ErrNoRows) {
		return map[time.Weekday]ScheduleDay{}, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	var versionID string
	err = s.pool.QueryRow(ctx, `SELECT id::text FROM work_schedule_template_versions
 WHERE company_id=$1 AND template_id=NULLIF($2,'')::uuid
   AND daterange(effective_from,COALESCE(effective_to,'infinity'::date),'[]') @> $3::date LIMIT 1`,
		companyID, templateID, monthStart.Format("2006-01-02")).Scan(&versionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return map[time.Weekday]ScheduleDay{}, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	rows, err := s.pool.Query(ctx, `SELECT weekday,planned_minutes FROM work_schedule_days WHERE company_id=$1 AND schedule_version_id=$2`, companyID, versionID)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	schedule := map[time.Weekday]ScheduleDay{}
	for rows.Next() {
		var weekday, planned int
		if err := rows.Scan(&weekday, &planned); err != nil {
			return nil, "", err
		}
		schedule[isoWeekdayToTime(weekday)] = ScheduleDay{PlannedMinutes: planned}
	}
	return schedule, versionID, rows.Err()
}

func (s *Service) loadManualDays(ctx context.Context, tx pgx.Tx, companyID, periodID, employeeID string) ([]Day, error) {
	rows, err := tx.Query(ctx, `SELECT to_char(work_date,'YYYY-MM-DD'),planned_minutes,worked_minutes,paid_leave_minutes,unpaid_leave_minutes,public_holiday_minutes
 FROM timesheet_days WHERE company_id=$1 AND period_id=NULLIF($2,'')::uuid AND employee_id=NULLIF($3,'')::uuid AND source='MANUAL'`,
		companyID, periodID, employeeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	days := []Day{}
	for rows.Next() {
		var d Day
		d.Source = "MANUAL"
		if err := rows.Scan(&d.Date, &d.PlannedMinutes, &d.WorkedMinutes, &d.PaidLeaveMinutes, &d.UnpaidLeaveMinutes, &d.PublicHolidayMinutes); err != nil {
			return nil, err
		}
		days = append(days, d)
	}
	return days, rows.Err()
}

// loadHolidays returns any active TR public-holiday calendar entries for the
// year. A missing calendar is not an error: holidays are otherwise marked by
// hand on the timesheet.
func (s *Service) loadHolidays(ctx context.Context, companyID string, year int) ([]Holiday, error) {
	var calendarID string
	err := s.pool.QueryRow(ctx, `SELECT id::text FROM public_holiday_calendars
 WHERE company_id=$1 AND country_code='TR' AND calendar_year=$2 AND status='ACTIVE'`, companyID, year).Scan(&calendarID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT holiday_date,duration FROM public_holidays WHERE company_id=$1 AND calendar_id=$2`, companyID, calendarID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	holidays := []Holiday{}
	for rows.Next() {
		var date time.Time
		var duration string
		if err := rows.Scan(&date, &duration); err != nil {
			return nil, err
		}
		holidays = append(holidays, Holiday{Date: date, HalfDay: duration == "HALF_DAY_AFTERNOON"})
	}
	return holidays, rows.Err()
}

func isoWeekdayToTime(weekday int) time.Weekday {
	if weekday == 7 {
		return time.Sunday
	}
	return time.Weekday(weekday)
}

func nullableUUID(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func checksumDays(days []DayDetail) string {
	type row struct {
		E, D, S                      string
		P, W, PL, UL, OT, WR, PH, AB int
		X                            string
	}
	rows := make([]row, 0, len(days))
	for _, d := range days {
		rows = append(rows, row{d.EmployeeID, d.WorkDate, d.Source, d.PlannedMinutes, d.WorkedMinutes, d.PaidLeaveMinutes,
			d.UnpaidLeaveMinutes, d.OvertimeMinutes, d.WeekRestMinutes, d.PublicHolidayMinutes, d.AbsenceMinutes, d.Explanation})
	}
	payload, _ := json.Marshal(rows)
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func mapConstraint(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	switch {
	case pgErr.Code == "55000" && strings.Contains(pgErr.ConstraintName, "finalized_immutable"):
		return ErrFinalized
	case pgErr.Code == "55000" && strings.Contains(pgErr.ConstraintName, "used_by_finalized_payroll"):
		return ErrUsedByPayroll
	case pgErr.Code == "23505" && strings.Contains(pgErr.ConstraintName, "period_year"):
		return ErrPeriodExists
	case pgErr.Code == "23505" && strings.Contains(pgErr.ConstraintName, "timesheet_periods"):
		return ErrPeriodExists
	case pgErr.Code == "23514":
		return fmt.Errorf("%w: %s", identity.ErrValidation, pgErr.Message)
	}
	return err
}

func writeEvent(ctx context.Context, tx pgx.Tx, session identity.Session, meta identity.RequestMeta, auditType, outboxType, entityID string) error {
	details, _ := json.Marshal(map[string]any{"period_id": entityID})
	if _, err := tx.Exec(ctx, `INSERT INTO security_audit_events(id,company_id,actor_user_id,event_type,entity_type,entity_id,details,trace_id,source_ip,user_agent)
 VALUES($1,$2,$3,$4,'timesheet_period',$5,$6,$7,$8,$9)`,
		uuid.NewString(), session.CurrentCompanyID, session.User.ID, auditType, entityID, details, meta.TraceID, meta.IP, meta.UserAgent); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]string{"period_id": entityID})
	_, err := tx.Exec(ctx, `INSERT INTO outbox_events(event_id,type,schema_version,company_id,trace_id,payload) VALUES($1,$2,1,$3,$4,$5)`,
		uuid.NewString(), outboxType, session.CurrentCompanyID, meta.TraceID, payload)
	return err
}
