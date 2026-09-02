package run

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/money"
	"github.com/alpyxn/varyaone/internal/payroll/calculation"
	"github.com/alpyxn/varyaone/internal/payroll/legislation"
	"github.com/alpyxn/varyaone/internal/platform/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrRunNotFound        = errors.New("PAYROLL_RUN_NOT_FOUND")
	ErrRunNotDraft        = errors.New("PAYROLL_RUN_NOT_DRAFT")
	ErrTimesheetNotFinal  = errors.New("PAYROLL_TIMESHEET_NOT_FINALIZED")
	ErrLegislationMissing = errors.New("PAYROLL_LEGISLATION_NOT_FOUND")
	ErrManualNotFound     = errors.New("PAYROLL_MANUAL_COMPONENT_NOT_FOUND")
	ErrRunExists          = errors.New("PAYROLL_RUN_EXISTS")
	ErrNoActiveGen        = ErrNoActiveGeneration
	ErrEmployeeGone       = errors.New("PAYROLL_EMPLOYEE_NOT_FOUND")
)

type Service struct {
	pool database.Querier
	repo *legislation.Repository
}

func NewService(pool database.Querier, repo *legislation.Repository) *Service {
	return &Service{pool: pool, repo: repo}
}

// ---- API types ----

type Run struct {
	ID                 string               `json:"id"`
	RunNumber          string               `json:"run_number"`
	RunType            string               `json:"run_type"`
	PeriodYear         int                  `json:"period_year"`
	PeriodMonth        int                  `json:"period_month"`
	PaymentDate        string               `json:"payment_date"`
	TimesheetPeriodID  string               `json:"timesheet_period_id"`
	LegislationPackID  string               `json:"legislation_pack_id"`
	Status             string               `json:"status"`
	ActiveGenerationID *string              `json:"active_generation_id,omitempty"`
	TotalGross         *string              `json:"total_gross,omitempty"`
	TotalNet           *string              `json:"total_net,omitempty"`
	TotalEmployerCost  *string              `json:"total_employer_cost,omitempty"`
	FinalizedAt        *time.Time           `json:"finalized_at,omitempty"`
	Version            int64                `json:"version"`
	Generations        []GenerationRow      `json:"generations,omitempty"`
	EmployeePayrolls   []EmployeePayrollRow `json:"employee_payrolls,omitempty"`
}

type GenerationRow struct {
	ID           string          `json:"id"`
	GenerationNo int             `json:"generation_no"`
	Status       string          `json:"status"`
	ErrorSummary json.RawMessage `json:"error_summary"`
	StartedAt    time.Time       `json:"started_at"`
}

type EmployeePayrollRow struct {
	ID           string          `json:"id"`
	EmployeeID   string          `json:"employee_id"`
	EmployeeName string          `json:"employee_name"`
	Status       string          `json:"status"`
	Gross        *string         `json:"gross,omitempty"`
	Net          *string         `json:"net,omitempty"`
	EmployerCost *string         `json:"employer_cost,omitempty"`
	ErrorDetails json.RawMessage `json:"error_details"`
	Components   []ComponentRow  `json:"components,omitempty"`
}

type ComponentRow struct {
	Code   string `json:"component_code"`
	Name   string `json:"component_name"`
	Kind   string `json:"component_kind"`
	Amount string `json:"amount"`
	Order  int    `json:"calculation_order"`
}

type RunInput struct {
	RunNumber         string `json:"run_number"`
	PeriodYear        int    `json:"period_year"`
	PeriodMonth       int    `json:"period_month"`
	PaymentDate       string `json:"payment_date"`
	TimesheetPeriodID string `json:"timesheet_period_id"`
	LegislationPackID string `json:"legislation_pack_id"`
}

type ManualComponent struct {
	ID                    string `json:"id"`
	EmployeeID            string `json:"employee_id"`
	EmployeeName          string `json:"employee_name"`
	ComponentDefinitionID string `json:"component_definition_id"`
	ComponentCode         string `json:"component_code"`
	Amount                string `json:"amount"`
	Explanation           string `json:"explanation"`
	Version               int64  `json:"version"`
}

type ManualComponentInput struct {
	EmployeeID            string `json:"employee_id"`
	ComponentDefinitionID string `json:"component_definition_id"`
	Amount                string `json:"amount"`
	Explanation           string `json:"explanation"`
}

// ---- CRUD ----

func (s *Service) List(ctx context.Context, session identity.Session) ([]Run, error) {
	if !session.HasPermission("hr.payroll.read") {
		return nil, identity.ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `SELECT id::text,run_number,run_type,period_year,period_month,to_char(payment_date,'YYYY-MM-DD'),
 timesheet_period_id::text,legislation_pack_id::text,status,active_generation_id::text,total_gross::text,total_net::text,total_employer_cost::text,finalized_at,version
 FROM payroll_runs WHERE company_id=$1 ORDER BY period_year DESC,period_month DESC,created_at DESC`, session.CurrentCompanyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Run{}
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, r)
	}
	return items, rows.Err()
}

func (s *Service) Get(ctx context.Context, session identity.Session, id string) (Run, error) {
	if !session.HasPermission("hr.payroll.read") {
		return Run{}, identity.ErrForbidden
	}
	r, err := scanRun(s.pool.QueryRow(ctx, `SELECT id::text,run_number,run_type,period_year,period_month,to_char(payment_date,'YYYY-MM-DD'),
 timesheet_period_id::text,legislation_pack_id::text,status,active_generation_id::text,total_gross::text,total_net::text,total_employer_cost::text,finalized_at,version
 FROM payroll_runs WHERE company_id=$1 AND id=NULLIF($2,'')::uuid`, session.CurrentCompanyID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, ErrRunNotFound
	}
	if err != nil {
		return Run{}, err
	}
	grows, err := s.pool.Query(ctx, `SELECT id::text,generation_no,status,error_summary,started_at FROM payroll_calculation_generations
 WHERE company_id=$1 AND payroll_run_id=$2 ORDER BY generation_no DESC`, session.CurrentCompanyID, r.ID)
	if err != nil {
		return Run{}, err
	}
	for grows.Next() {
		var g GenerationRow
		if err := grows.Scan(&g.ID, &g.GenerationNo, &g.Status, &g.ErrorSummary, &g.StartedAt); err != nil {
			grows.Close()
			return Run{}, err
		}
		r.Generations = append(r.Generations, g)
	}
	grows.Close()
	if r.ActiveGenerationID != nil && *r.ActiveGenerationID != "" {
		payrolls, err := s.employeePayrolls(ctx, session.CurrentCompanyID, *r.ActiveGenerationID)
		if err != nil {
			return Run{}, err
		}
		r.EmployeePayrolls = payrolls
	} else if len(r.Generations) > 0 {
		payrolls, err := s.employeePayrolls(ctx, session.CurrentCompanyID, r.Generations[0].ID)
		if err != nil {
			return Run{}, err
		}
		r.EmployeePayrolls = payrolls
	}
	return r, nil
}

func (s *Service) employeePayrolls(ctx context.Context, companyID, generationID string) ([]EmployeePayrollRow, error) {
	rows, err := s.pool.Query(ctx, `SELECT p.id::text,p.employee_id::text,e.first_name||' '||e.last_name,p.status,p.gross::text,p.net::text,p.employer_cost::text,p.error_details
 FROM employee_payrolls p JOIN employees e ON e.company_id=p.company_id AND e.id=p.employee_id
 WHERE p.company_id=$1 AND p.generation_id=$2 ORDER BY e.first_name,e.last_name`, companyID, generationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []EmployeePayrollRow{}
	for rows.Next() {
		var p EmployeePayrollRow
		if err := rows.Scan(&p.ID, &p.EmployeeID, &p.EmployeeName, &p.Status, &p.Gross, &p.Net, &p.EmployerCost, &p.ErrorDetails); err != nil {
			return nil, err
		}
		items = append(items, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range items {
		crows, err := s.pool.Query(ctx, `SELECT component_code,component_name,component_kind,amount::text,calculation_order
 FROM employee_payroll_components WHERE company_id=$1 AND employee_payroll_id=$2 ORDER BY calculation_order`, companyID, items[i].ID)
		if err != nil {
			return nil, err
		}
		for crows.Next() {
			var c ComponentRow
			if err := crows.Scan(&c.Code, &c.Name, &c.Kind, &c.Amount, &c.Order); err != nil {
				crows.Close()
				return nil, err
			}
			items[i].Components = append(items[i].Components, c)
		}
		crows.Close()
	}
	return items, nil
}

func (s *Service) Create(ctx context.Context, session identity.Session, input RunInput, meta identity.RequestMeta) (Run, error) {
	if !session.HasPermission("hr.payroll.calculate") {
		return Run{}, identity.ErrForbidden
	}
	input.RunNumber = strings.TrimSpace(input.RunNumber)
	input.TimesheetPeriodID = strings.TrimSpace(input.TimesheetPeriodID)
	input.LegislationPackID = strings.TrimSpace(input.LegislationPackID)
	if input.TimesheetPeriodID == "" {
		return Run{}, fmt.Errorf("%w: puantaj dönemi zorunlu", identity.ErrValidation)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Run{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	// Dönem (yıl/ay) puantaj döneminden türetilir; kullanıcı ayrıca girmez.
	var tsStatus string
	err = tx.QueryRow(ctx, `SELECT status,period_year,period_month FROM timesheet_periods WHERE company_id=$1 AND id=NULLIF($2,'')::uuid`,
		session.CurrentCompanyID, input.TimesheetPeriodID).Scan(&tsStatus, &input.PeriodYear, &input.PeriodMonth)
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, fmt.Errorf("%w: puantaj dönemi bulunamadı", identity.ErrValidation)
	}
	if err != nil {
		return Run{}, err
	}
	if tsStatus != "FINALIZED" {
		return Run{}, ErrTimesheetNotFinal
	}
	if input.PeriodYear < 2000 || input.PeriodMonth < 1 || input.PeriodMonth > 12 {
		return Run{}, fmt.Errorf("%w: puantaj döneminin tarihi geçersiz", identity.ErrValidation)
	}
	// Ödeme tarihi verilmemişse dönemin son gününü kullan.
	if strings.TrimSpace(input.PaymentDate) == "" {
		input.PaymentDate = time.Date(input.PeriodYear, time.Month(input.PeriodMonth)+1, 0, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
	}
	if _, err := time.Parse("2006-01-02", strings.TrimSpace(input.PaymentDate)); err != nil {
		return Run{}, fmt.Errorf("%w: ödeme tarihi geçersiz", identity.ErrValidation)
	}
	// Mevzuat paketi verilmemişse dönemde aktif olan tek paketi seç.
	if input.LegislationPackID == "" {
		periodStart := fmt.Sprintf("%04d-%02d-01", input.PeriodYear, input.PeriodMonth)
		if err = tx.QueryRow(ctx, `SELECT id::text FROM payroll_legislation_packs
 WHERE company_id=$1 AND status='ACTIVE' AND effective_from<=$2::date AND effective_to>=$2::date
 ORDER BY effective_from DESC LIMIT 1`, session.CurrentCompanyID, periodStart).Scan(&input.LegislationPackID); errors.Is(err, pgx.ErrNoRows) {
			return Run{}, ErrLegislationMissing
		} else if err != nil {
			return Run{}, err
		}
	}
	var packStatus string
	err = tx.QueryRow(ctx, `SELECT status FROM payroll_legislation_packs WHERE company_id=$1 AND id=NULLIF($2,'')::uuid`,
		session.CurrentCompanyID, input.LegislationPackID).Scan(&packStatus)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && packStatus != "ACTIVE") {
		return Run{}, ErrLegislationMissing
	}
	if err != nil {
		return Run{}, err
	}
	if input.RunNumber == "" {
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, session.CurrentCompanyID+":payroll_runs:run_number"); err != nil {
			return Run{}, err
		}
		base := fmt.Sprintf("BORDRO-%04d-%02d", input.PeriodYear, input.PeriodMonth)
		var taken int
		_ = tx.QueryRow(ctx, `SELECT COUNT(*) FROM payroll_runs WHERE company_id=$1 AND run_number LIKE $2`,
			session.CurrentCompanyID, base+"%").Scan(&taken)
		if taken == 0 {
			input.RunNumber = base
		} else {
			input.RunNumber = fmt.Sprintf("%s-%d", base, taken+1)
		}
	}
	id := uuid.NewString()
	_, err = tx.Exec(ctx, `INSERT INTO payroll_runs(id,company_id,run_number,run_type,period_year,period_month,payment_date,timesheet_period_id,legislation_pack_id)
 VALUES($1,$2,$3,'REGULAR',$4,$5,$6::date,NULLIF($7,'')::uuid,NULLIF($8,'')::uuid)`,
		id, session.CurrentCompanyID, input.RunNumber, input.PeriodYear, input.PeriodMonth, input.PaymentDate, input.TimesheetPeriodID, input.LegislationPackID)
	if err != nil {
		return Run{}, mapConstraint(err)
	}
	if err = writeEvent(ctx, tx, session, meta, "PAYROLL_RUN_CREATED", "hr.payroll_run.created", id); err != nil {
		return Run{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Run{}, err
	}
	return s.Get(ctx, session, id)
}

// ---- Manual components ----

func (s *Service) ListManualComponents(ctx context.Context, session identity.Session, runID string) ([]ManualComponent, error) {
	if !session.HasPermission("hr.payroll.read") {
		return nil, identity.ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `SELECT m.id::text,m.employee_id::text,e.first_name||' '||e.last_name,m.component_definition_id::text,d.code,m.amount::text,m.explanation,m.version
 FROM payroll_manual_components m
 JOIN employees e ON e.company_id=m.company_id AND e.id=m.employee_id
 JOIN payroll_component_definitions d ON d.company_id=m.company_id AND d.id=m.component_definition_id
 WHERE m.company_id=$1 AND m.payroll_run_id=NULLIF($2,'')::uuid AND m.archived_at IS NULL ORDER BY e.first_name,d.code`,
		session.CurrentCompanyID, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ManualComponent{}
	for rows.Next() {
		var m ManualComponent
		if err := rows.Scan(&m.ID, &m.EmployeeID, &m.EmployeeName, &m.ComponentDefinitionID, &m.ComponentCode, &m.Amount, &m.Explanation, &m.Version); err != nil {
			return nil, err
		}
		items = append(items, m)
	}
	return items, rows.Err()
}

func (s *Service) AddManualComponent(ctx context.Context, session identity.Session, runID string, input ManualComponentInput, meta identity.RequestMeta) (ManualComponent, error) {
	if !session.HasPermission("hr.payroll.calculate") {
		return ManualComponent{}, identity.ErrForbidden
	}
	input.Explanation = strings.TrimSpace(input.Explanation)
	if strings.TrimSpace(input.EmployeeID) == "" || strings.TrimSpace(input.ComponentDefinitionID) == "" || input.Explanation == "" {
		return ManualComponent{}, fmt.Errorf("%w: manuel bileşen alanları eksik", identity.ErrValidation)
	}
	if _, err := money.ParseDecimal(strings.TrimSpace(input.Amount), 2); err != nil {
		return ManualComponent{}, fmt.Errorf("%w: tutar geçersiz", identity.ErrValidation)
	}
	id := uuid.NewString()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ManualComponent{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var status string
	if err = tx.QueryRow(ctx, `SELECT status FROM payroll_runs WHERE company_id=$1 AND id=NULLIF($2,'')::uuid`, session.CurrentCompanyID, runID).Scan(&status); errors.Is(err, pgx.ErrNoRows) {
		return ManualComponent{}, ErrRunNotFound
	} else if err != nil {
		return ManualComponent{}, err
	}
	if status == "FINALIZED" {
		return ManualComponent{}, ErrRunNotDraft
	}
	_, err = tx.Exec(ctx, `INSERT INTO payroll_manual_components(id,company_id,payroll_run_id,employee_id,component_definition_id,amount,explanation)
 VALUES($1,$2,NULLIF($3,'')::uuid,NULLIF($4,'')::uuid,NULLIF($5,'')::uuid,$6::numeric,$7)`,
		id, session.CurrentCompanyID, runID, input.EmployeeID, input.ComponentDefinitionID, strings.TrimSpace(input.Amount), input.Explanation)
	if err != nil {
		return ManualComponent{}, mapConstraint(err)
	}
	if err = writeEvent(ctx, tx, session, meta, "PAYROLL_MANUAL_COMPONENT_ADDED", "hr.payroll_manual_component.added", runID); err != nil {
		return ManualComponent{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return ManualComponent{}, err
	}
	for _, m := range mustList(s.ListManualComponents(ctx, session, runID)) {
		if m.ID == id {
			return m, nil
		}
	}
	return ManualComponent{}, ErrManualNotFound
}

func (s *Service) ArchiveManualComponent(ctx context.Context, session identity.Session, runID, componentID string, meta identity.RequestMeta) error {
	if !session.HasPermission("hr.payroll.calculate") {
		return identity.ErrForbidden
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	tag, err := tx.Exec(ctx, `UPDATE payroll_manual_components SET archived_at=now(),updated_at=now(),version=version+1
 WHERE company_id=$1 AND payroll_run_id=NULLIF($2,'')::uuid AND id=NULLIF($3,'')::uuid AND archived_at IS NULL`,
		session.CurrentCompanyID, runID, componentID)
	if err != nil {
		return mapConstraint(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrManualNotFound
	}
	if err = writeEvent(ctx, tx, session, meta, "PAYROLL_MANUAL_COMPONENT_ARCHIVED", "hr.payroll_manual_component.archived", runID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ---- helpers ----

func scanRun(row interface{ Scan(...any) error }) (Run, error) {
	var r Run
	err := row.Scan(&r.ID, &r.RunNumber, &r.RunType, &r.PeriodYear, &r.PeriodMonth, &r.PaymentDate, &r.TimesheetPeriodID,
		&r.LegislationPackID, &r.Status, &r.ActiveGenerationID, &r.TotalGross, &r.TotalNet, &r.TotalEmployerCost, &r.FinalizedAt, &r.Version)
	return r, err
}

func mustList(items []ManualComponent, err error) []ManualComponent {
	if err != nil {
		return nil
	}
	return items
}

func hashStrings(parts ...string) string {
	sort.Strings(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

func mapConstraint(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	switch {
	case pgErr.Code == "23505" && strings.Contains(pgErr.ConstraintName, "run_number"):
		return ErrRunExists
	case pgErr.Code == "23505" && strings.Contains(pgErr.ConstraintName, "one_running"):
		return ErrJobInProgress
	case pgErr.Code == "55000":
		return fmt.Errorf("%w: kesinleşmiş bordro değiştirilemez", identity.ErrValidation)
	case pgErr.Code == "23503":
		return ErrEmployeeGone
	case pgErr.Code == "23514":
		return fmt.Errorf("%w: %s", identity.ErrValidation, pgErr.Message)
	}
	return err
}

func writeEvent(ctx context.Context, tx pgx.Tx, session identity.Session, meta identity.RequestMeta, auditType, outboxType, runID string) error {
	details, _ := json.Marshal(map[string]any{"payroll_run_id": runID})
	if _, err := tx.Exec(ctx, `INSERT INTO security_audit_events(id,company_id,actor_user_id,event_type,entity_type,entity_id,details,trace_id,source_ip,user_agent)
 VALUES($1,$2,$3,$4,'payroll_run',$5,$6,$7,$8,$9)`,
		uuid.NewString(), session.CurrentCompanyID, session.User.ID, auditType, runID, details, meta.TraceID, meta.IP, meta.UserAgent); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]string{"payroll_run_id": runID})
	_, err := tx.Exec(ctx, `INSERT INTO outbox_events(event_id,type,schema_version,company_id,trace_id,payload) VALUES($1,$2,1,$3,$4,$5)`,
		uuid.NewString(), outboxType, session.CurrentCompanyID, meta.TraceID, payload)
	return err
}

var _ = calculation.EngineVersion
