package legislation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/money"
	"github.com/alpyxn/varyaone/internal/platform/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrNotFound  = errors.New("PAYROLL_LEGISLATION_PACK_NOT_FOUND")
	ErrNotDraft  = errors.New("PAYROLL_LEGISLATION_PACK_NOT_DRAFT")
	ErrImmutable = errors.New("PAYROLL_LEGISLATION_PACK_IMMUTABLE")
	ErrOverlap   = errors.New("PAYROLL_LEGISLATION_ACTIVE_OVERLAP")
)

type Service struct{ pool database.Querier }

func NewService(pool database.Querier) *Service { return &Service{pool: pool} }

type PackSummary struct {
	ID                  string `json:"id"`
	Code                string `json:"code"`
	Version             int    `json:"version"`
	Status              string `json:"status"`
	EffectiveFrom       string `json:"effective_from"`
	EffectiveTo         string `json:"effective_to"`
	MinimumMonthlyGross string `json:"minimum_monthly_gross"`
}

type PackDetail struct {
	PackSummary
	MinimumMonthlyGross string         `json:"minimum_monthly_gross"`
	SGKDailyFloor       string         `json:"sgk_daily_floor"`
	SGKDailyCeiling     string         `json:"sgk_daily_ceiling"`
	StampTaxRate        string         `json:"stamp_tax_rate"`
	IncomeTaxBrackets   []BracketRow   `json:"income_tax_brackets"`
	ContributionSchemes []SchemeRow    `json:"contribution_schemes"`
	Components          []ComponentRow `json:"components"`
}

type BracketRow struct {
	Sequence   int     `json:"sequence"`
	UpperBound *string `json:"upper_bound,omitempty"`
	Rate       string  `json:"rate"`
}

type SchemeRow struct {
	Code                     string `json:"code"`
	Name                     string `json:"name"`
	EmployeeSGKRate          string `json:"employee_sgk_rate"`
	EmployeeUnemploymentRate string `json:"employee_unemployment_rate"`
	EmployerSGKRate          string `json:"employer_sgk_rate"`
	EmployerUnemploymentRate string `json:"employer_unemployment_rate"`
}

type ComponentRow struct {
	Code          string `json:"code"`
	Name          string `json:"name"`
	Ownership     string `json:"ownership"`
	ComponentKind string `json:"component_kind"`
	PaymentForm   string `json:"payment_form"`
	IsActive      bool   `json:"is_active"`
}

func (s *Service) ListPacks(ctx context.Context, session identity.Session) ([]PackSummary, error) {
	if !session.HasPermission("hr.legislation.read") {
		return nil, identity.ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `SELECT id::text,code,version,status,to_char(effective_from,'YYYY-MM-DD'),to_char(effective_to,'YYYY-MM-DD'),minimum_monthly_gross::text
 FROM payroll_legislation_packs WHERE company_id=$1 AND country_code='TR' ORDER BY version DESC,effective_from DESC`, session.CurrentCompanyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []PackSummary{}
	for rows.Next() {
		var p PackSummary
		if err := rows.Scan(&p.ID, &p.Code, &p.Version, &p.Status, &p.EffectiveFrom, &p.EffectiveTo, &p.MinimumMonthlyGross); err != nil {
			return nil, err
		}
		items = append(items, p)
	}
	return items, rows.Err()
}

func (s *Service) GetPack(ctx context.Context, session identity.Session, id string) (PackDetail, error) {
	if !session.HasPermission("hr.legislation.read") {
		return PackDetail{}, identity.ErrForbidden
	}
	var d PackDetail
	err := s.pool.QueryRow(ctx, `SELECT id::text,code,version,status,to_char(effective_from,'YYYY-MM-DD'),to_char(effective_to,'YYYY-MM-DD'),
 minimum_monthly_gross::text,sgk_daily_floor::text,sgk_daily_ceiling::text,stamp_tax_rate::text
 FROM payroll_legislation_packs WHERE company_id=$1 AND id=NULLIF($2,'')::uuid`, session.CurrentCompanyID, id).
		Scan(&d.ID, &d.Code, &d.Version, &d.Status, &d.EffectiveFrom, &d.EffectiveTo,
			&d.MinimumMonthlyGross, &d.SGKDailyFloor, &d.SGKDailyCeiling, &d.StampTaxRate)
	if errors.Is(err, pgx.ErrNoRows) {
		return PackDetail{}, ErrNotFound
	}
	if err != nil {
		return PackDetail{}, err
	}
	brows, err := s.pool.Query(ctx, `SELECT sequence,upper_bound::text,rate::text FROM payroll_income_tax_brackets
 WHERE company_id=$1 AND pack_id=$2 ORDER BY sequence`, session.CurrentCompanyID, d.ID)
	if err != nil {
		return PackDetail{}, err
	}
	for brows.Next() {
		var b BracketRow
		if err := brows.Scan(&b.Sequence, &b.UpperBound, &b.Rate); err != nil {
			brows.Close()
			return PackDetail{}, err
		}
		d.IncomeTaxBrackets = append(d.IncomeTaxBrackets, b)
	}
	brows.Close()
	srows, err := s.pool.Query(ctx, `SELECT code,name,employee_sgk_rate::text,employee_unemployment_rate::text,employer_sgk_rate::text,employer_unemployment_rate::text
 FROM payroll_contribution_schemes WHERE company_id=$1 AND pack_id=$2 ORDER BY code`, session.CurrentCompanyID, d.ID)
	if err != nil {
		return PackDetail{}, err
	}
	for srows.Next() {
		var sr SchemeRow
		if err := srows.Scan(&sr.Code, &sr.Name, &sr.EmployeeSGKRate, &sr.EmployeeUnemploymentRate, &sr.EmployerSGKRate, &sr.EmployerUnemploymentRate); err != nil {
			srows.Close()
			return PackDetail{}, err
		}
		d.ContributionSchemes = append(d.ContributionSchemes, sr)
	}
	srows.Close()
	crows, err := s.pool.Query(ctx, `SELECT code,name,ownership,component_kind,payment_form,is_active FROM payroll_component_definitions
 WHERE company_id=$1 AND pack_id=$2 ORDER BY component_kind,code`, session.CurrentCompanyID, d.ID)
	if err != nil {
		return PackDetail{}, err
	}
	defer crows.Close()
	for crows.Next() {
		var cr ComponentRow
		if err := crows.Scan(&cr.Code, &cr.Name, &cr.Ownership, &cr.ComponentKind, &cr.PaymentForm, &cr.IsActive); err != nil {
			return PackDetail{}, err
		}
		d.Components = append(d.Components, cr)
	}
	return d, crows.Err()
}

// PayrollSettings carries company-wide payroll defaults that are not part of a
// legislation pack.
type PayrollSettings struct {
	DefaultContributionSchemeCode string `json:"default_contribution_scheme_code"`
}

// GetPayrollSettings reads the company payroll defaults.
func (s *Service) GetPayrollSettings(ctx context.Context, session identity.Session) (PayrollSettings, error) {
	if !session.HasPermission("hr.legislation.read") {
		return PayrollSettings{}, identity.ErrForbidden
	}
	var out PayrollSettings
	var code *string
	if err := s.pool.QueryRow(ctx, `SELECT hr_default_contribution_scheme_code FROM companies WHERE id=$1`, session.CurrentCompanyID).Scan(&code); err != nil {
		return PayrollSettings{}, err
	}
	if code != nil {
		out.DefaultContributionSchemeCode = *code
	}
	return out, nil
}

// SetDefaultContributionScheme stores the company-wide default SGK indirim /
// teşvik paketi. An empty code clears the default. A non-empty code must exist in
// the company's ACTIVE legislation pack.
func (s *Service) SetDefaultContributionScheme(ctx context.Context, session identity.Session, code string) (PayrollSettings, error) {
	if !session.HasPermission("hr.legislation.manage") {
		return PayrollSettings{}, identity.ErrForbidden
	}
	code = strings.TrimSpace(code)
	if code != "" {
		var exists bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS(
 SELECT 1 FROM payroll_contribution_schemes cs
 JOIN payroll_legislation_packs p ON p.company_id=cs.company_id AND p.id=cs.pack_id
 WHERE cs.company_id=$1 AND cs.code=$2 AND p.status='ACTIVE')`, session.CurrentCompanyID, code).Scan(&exists); err != nil {
			return PayrollSettings{}, err
		}
		if !exists {
			return PayrollSettings{}, fmt.Errorf("%w: SGK indirim kodu aktif mevzuat paketinde yok", identity.ErrValidation)
		}
	}
	if _, err := s.pool.Exec(ctx, `UPDATE companies SET hr_default_contribution_scheme_code=NULLIF($2,'') WHERE id=$1`, session.CurrentCompanyID, code); err != nil {
		return PayrollSettings{}, err
	}
	return s.GetPayrollSettings(ctx, session)
}

// ManualComponents returns MANUAL, active earning/deduction component codes for a
// pack, used by the payroll run UI to offer manual component entries.
func (s *Service) ManualComponents(ctx context.Context, session identity.Session, packID string) ([]ComponentRow, error) {
	if !session.HasPermission("hr.payroll.read") {
		return nil, identity.ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `SELECT code,name,ownership,component_kind,payment_form,is_active FROM payroll_component_definitions
 WHERE company_id=$1 AND pack_id=NULLIF($2,'')::uuid AND ownership='MANUAL' AND is_active ORDER BY component_kind,code`,
		session.CurrentCompanyID, packID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ComponentRow{}
	for rows.Next() {
		var cr ComponentRow
		if err := rows.Scan(&cr.Code, &cr.Name, &cr.Ownership, &cr.ComponentKind, &cr.PaymentForm, &cr.IsActive); err != nil {
			return nil, err
		}
		items = append(items, cr)
	}
	return items, rows.Err()
}

type DraftPackInput struct {
	Code                string `json:"code"`
	EffectiveFrom       string `json:"effective_from"`
	EffectiveTo         string `json:"effective_to"`
	MinimumMonthlyGross string `json:"minimum_monthly_gross"`
	SGKDailyFloor       string `json:"sgk_daily_floor"`
	SGKDailyCeiling     string `json:"sgk_daily_ceiling"`
	StampTaxRate        string `json:"stamp_tax_rate"`
	SupersedesPackID    string `json:"supersedes_pack_id"`
	ChangeReason        string `json:"change_reason"`
}

func (s *Service) CreateDraft(ctx context.Context, session identity.Session, input DraftPackInput, meta identity.RequestMeta) (PackDetail, error) {
	if !session.HasPermission("hr.legislation.manage") {
		return PackDetail{}, identity.ErrForbidden
	}
	input.Code = strings.TrimSpace(input.Code)
	input.EffectiveFrom = strings.TrimSpace(input.EffectiveFrom)
	input.EffectiveTo = strings.TrimSpace(input.EffectiveTo)
	supersedesID := strings.TrimSpace(input.SupersedesPackID)
	// The simplified minimum-wage panel does not collect a code or a validity
	// window: inherit the code and start date from the period being replaced
	// (so the timeline stays fully covered by one active period), and leave the
	// end date open.
	if (input.Code == "" || input.EffectiveFrom == "") && supersedesID != "" {
		var prevCode, prevFrom string
		if err := s.pool.QueryRow(ctx, `SELECT code,to_char(effective_from,'YYYY-MM-DD')
 FROM payroll_legislation_packs WHERE company_id=$1 AND id=NULLIF($2,'')::uuid`,
			session.CurrentCompanyID, supersedesID).Scan(&prevCode, &prevFrom); err == nil {
			if input.Code == "" {
				input.Code = prevCode
			}
			if input.EffectiveFrom == "" {
				input.EffectiveFrom = prevFrom
			}
		}
	}
	if input.Code == "" {
		input.Code = "TR"
	}
	if input.EffectiveFrom == "" {
		input.EffectiveFrom = time.Now().UTC().Format("2006-01-02")
	}
	if input.EffectiveTo == "" {
		input.EffectiveTo = "9999-12-31"
	}
	if _, err := time.Parse("2006-01-02", input.EffectiveFrom); err != nil {
		return PackDetail{}, fmt.Errorf("%w: geçerlilik başlangıcı geçersiz", identity.ErrValidation)
	}
	if _, err := time.Parse("2006-01-02", input.EffectiveTo); err != nil {
		return PackDetail{}, fmt.Errorf("%w: geçerlilik bitişi geçersiz", identity.ErrValidation)
	}
	id := uuid.NewString()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PackDetail{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var nextVersion int
	if err = tx.QueryRow(ctx, `SELECT COALESCE(MAX(version),0)+1 FROM payroll_legislation_packs WHERE company_id=$1 AND country_code='TR' AND code=$2`,
		session.CurrentCompanyID, input.Code).Scan(&nextVersion); err != nil {
		return PackDetail{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO payroll_legislation_packs(id,company_id,country_code,code,version,status,effective_from,effective_to,
 supersedes_pack_id,change_reason,minimum_monthly_gross,sgk_daily_floor,sgk_daily_ceiling,stamp_tax_rate,created_by)
 VALUES($1,$2,'TR',$3,$4,'DRAFT',$5::date,$6::date,NULLIF($7,'')::uuid,NULLIF($8,''),$9::numeric,$10::numeric,$11::numeric,$12::numeric,$13)`,
		id, session.CurrentCompanyID, input.Code, nextVersion, input.EffectiveFrom, input.EffectiveTo,
		supersedesID, strings.TrimSpace(input.ChangeReason),
		input.MinimumMonthlyGross, input.SGKDailyFloor, input.SGKDailyCeiling, input.StampTaxRate, session.User.ID)
	if err != nil {
		return PackDetail{}, mapConstraint(err)
	}
	// A routine update (asgari ücret dönemi değişikliği vb.) only changes the
	// pack-level figures; the panel form does not collect income-tax brackets,
	// contribution schemes or component definitions, so when superseding an
	// existing pack we carry those over unchanged from the source.
	if supersedesID != "" {
		if err = copyPackContent(ctx, tx, session.CurrentCompanyID, supersedesID, id); err != nil {
			return PackDetail{}, mapConstraint(err)
		}
	}
	if err = writeEvent(ctx, tx, session, meta, "PAYROLL_LEGISLATION_PACK_CREATED", "hr.legislation_pack.created", id); err != nil {
		return PackDetail{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return PackDetail{}, err
	}
	return s.GetPack(ctx, session, id)
}

// MinimumWageInput is what the simplified "Asgari ücret" panel collects. Only
// the gross is required; SGK daily floor/ceiling and the stamp-tax rate are
// derived from the previous period when left blank.
type MinimumWageInput struct {
	MinimumMonthlyGross string `json:"minimum_monthly_gross"`
	SGKDailyFloor       string `json:"sgk_daily_floor"`
	SGKDailyCeiling     string `json:"sgk_daily_ceiling"`
	StampTaxRate        string `json:"stamp_tax_rate"`
	ChangeReason        string `json:"change_reason"`
}

// ReplaceMinimumWage creates a fresh legislation period from the current active
// one — changing only the minimum-wage figures — and activates it in one step.
// The previous period becomes SUPERSEDED. Returns the now-active period.
func (s *Service) ReplaceMinimumWage(ctx context.Context, session identity.Session, input MinimumWageInput, meta identity.RequestMeta) (PackDetail, error) {
	if !session.HasPermission("hr.legislation.manage") {
		return PackDetail{}, identity.ErrForbidden
	}
	input.MinimumMonthlyGross = strings.TrimSpace(input.MinimumMonthlyGross)
	gross, err := dec(input.MinimumMonthlyGross)
	if err != nil || gross.Sign() <= 0 {
		return PackDetail{}, fmt.Errorf("%w: aylık brüt asgari ücret geçersiz", identity.ErrValidation)
	}
	var activeID, activeCode, prevFloor, prevCeiling, prevStamp string
	err = s.pool.QueryRow(ctx, `SELECT id::text,code,sgk_daily_floor::text,sgk_daily_ceiling::text,stamp_tax_rate::text
 FROM payroll_legislation_packs WHERE company_id=$1 AND country_code='TR' AND status='ACTIVE' LIMIT 1`,
		session.CurrentCompanyID).Scan(&activeID, &activeCode, &prevFloor, &prevCeiling, &prevStamp)
	if errors.Is(err, pgx.ErrNoRows) {
		return PackDetail{}, ErrNotFound
	}
	if err != nil {
		return PackDetail{}, err
	}

	floor := strings.TrimSpace(input.SGKDailyFloor)
	ceiling := strings.TrimSpace(input.SGKDailyCeiling)
	stamp := strings.TrimSpace(input.StampTaxRate)
	if floor == "" {
		// Günlük taban = aylık brüt / 30.
		f, derr := gross.Div(decMust("30"), 2)
		if derr != nil {
			return PackDetail{}, derr
		}
		floor = f.String()
	}
	if ceiling == "" {
		// Tavan/taban oranını önceki dönemden koru.
		pf, _ := dec(prevFloor)
		pc, _ := dec(prevCeiling)
		fdec, _ := dec(floor)
		if pf.Sign() > 0 {
			ratio, derr := pc.Div(pf, 8)
			if derr != nil {
				return PackDetail{}, derr
			}
			c, cerr := fdec.Mul(ratio).Quantize(2, money.HalfUp)
			if cerr != nil {
				return PackDetail{}, cerr
			}
			ceiling = c.String()
		} else {
			ceiling = prevCeiling
		}
	}
	if stamp == "" {
		stamp = prevStamp
	}

	draft, err := s.CreateDraft(ctx, session, DraftPackInput{
		Code:                activeCode,
		MinimumMonthlyGross: input.MinimumMonthlyGross,
		SGKDailyFloor:       floor,
		SGKDailyCeiling:     ceiling,
		StampTaxRate:        stamp,
		SupersedesPackID:    activeID,
		ChangeReason:        strings.TrimSpace(input.ChangeReason),
	}, meta)
	if err != nil {
		return PackDetail{}, err
	}
	return s.Activate(ctx, session, draft.ID, meta)
}

func decMust(value string) money.Decimal {
	d, err := money.ParseDecimal(value, 12)
	if err != nil {
		panic(err)
	}
	return d
}

func (s *Service) Activate(ctx context.Context, session identity.Session, id string, meta identity.RequestMeta) (PackDetail, error) {
	if !session.HasPermission("hr.legislation.manage") {
		return PackDetail{}, identity.ErrForbidden
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PackDetail{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var status string
	var supersedes *string
	err = tx.QueryRow(ctx, `SELECT status,supersedes_pack_id::text FROM payroll_legislation_packs WHERE company_id=$1 AND id=NULLIF($2,'')::uuid FOR UPDATE`,
		session.CurrentCompanyID, id).Scan(&status, &supersedes)
	if errors.Is(err, pgx.ErrNoRows) {
		return PackDetail{}, ErrNotFound
	}
	if err != nil {
		return PackDetail{}, err
	}
	if status != "DRAFT" {
		return PackDetail{}, ErrNotDraft
	}
	if supersedes != nil && *supersedes != "" {
		if _, err = tx.Exec(ctx, `UPDATE payroll_legislation_packs SET status='SUPERSEDED' WHERE company_id=$1 AND id=NULLIF($2,'')::uuid AND status='ACTIVE'`,
			session.CurrentCompanyID, *supersedes); err != nil {
			return PackDetail{}, mapConstraint(err)
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE payroll_legislation_packs SET status='ACTIVE',activated_at=now() WHERE company_id=$1 AND id=NULLIF($2,'')::uuid`,
		session.CurrentCompanyID, id); err != nil {
		return PackDetail{}, mapConstraint(err)
	}
	if err = writeEvent(ctx, tx, session, meta, "PAYROLL_LEGISLATION_PACK_ACTIVATED", "hr.legislation_pack.activated", id); err != nil {
		return PackDetail{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return PackDetail{}, err
	}
	return s.GetPack(ctx, session, id)
}

// copyPackContent duplicates income-tax brackets, contribution schemes and
// component definitions (with their levy treatments) from sourcePackID into
// targetPackID, generating fresh ids/foreign keys for every row.
func copyPackContent(ctx context.Context, tx pgx.Tx, companyID, sourcePackID, targetPackID string) error {
	if _, err := tx.Exec(ctx, `INSERT INTO payroll_income_tax_brackets(company_id,pack_id,country_code,sequence,upper_bound,rate)
 SELECT company_id,$3,country_code,sequence,upper_bound,rate FROM payroll_income_tax_brackets
 WHERE company_id=$1 AND pack_id=$2`, companyID, sourcePackID, targetPackID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO payroll_contribution_schemes(id,company_id,pack_id,code,name,
 employee_sgk_rate,employee_unemployment_rate,employer_sgk_rate,employer_unemployment_rate,sgdp_employee_rate,sgdp_employer_rate)
 SELECT gen_random_uuid(),company_id,$3,code,name,employee_sgk_rate,employee_unemployment_rate,employer_sgk_rate,employer_unemployment_rate,
   sgdp_employee_rate,sgdp_employer_rate
 FROM payroll_contribution_schemes WHERE company_id=$1 AND pack_id=$2`, companyID, sourcePackID, targetPackID); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT id::text,code,name,ownership,component_kind,payment_form,is_active
 FROM payroll_component_definitions WHERE company_id=$1 AND pack_id=$2`, companyID, sourcePackID)
	if err != nil {
		return err
	}
	type sourceComponent struct {
		id, code, name, ownership, kind, form string
		active                                bool
	}
	components := []sourceComponent{}
	for rows.Next() {
		var c sourceComponent
		if err := rows.Scan(&c.id, &c.code, &c.name, &c.ownership, &c.kind, &c.form, &c.active); err != nil {
			rows.Close()
			return err
		}
		components = append(components, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, c := range components {
		newID := uuid.NewString()
		if _, err := tx.Exec(ctx, `INSERT INTO payroll_component_definitions(id,company_id,pack_id,code,name,ownership,component_kind,payment_form,is_active)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, newID, companyID, targetPackID, c.code, c.name, c.ownership, c.kind, c.form, c.active); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO payroll_component_treatments(company_id,component_definition_id,levy,treatment,limit_value,qualification)
 SELECT company_id,$3,levy,treatment,limit_value,qualification FROM payroll_component_treatments
 WHERE company_id=$1 AND component_definition_id=$2`, companyID, c.id, newID); err != nil {
			return err
		}
	}
	return nil
}

func mapConstraint(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	switch {
	case pgErr.Code == "23P01":
		return ErrOverlap
	case pgErr.Code == "55000":
		return ErrImmutable
	case pgErr.Code == "23505":
		return fmt.Errorf("%w: bu kod/sürüm zaten var", identity.ErrValidation)
	case pgErr.Code == "23514":
		return fmt.Errorf("%w: %s", identity.ErrValidation, pgErr.Message)
	}
	return err
}

func writeEvent(ctx context.Context, tx pgx.Tx, session identity.Session, meta identity.RequestMeta, auditType, outboxType, entityID string) error {
	details, _ := json.Marshal(map[string]any{"pack_id": entityID})
	if _, err := tx.Exec(ctx, `INSERT INTO security_audit_events(id,company_id,actor_user_id,event_type,entity_type,entity_id,details,trace_id,source_ip,user_agent)
 VALUES($1,$2,$3,$4,'payroll_legislation_pack',$5,$6,$7,$8,$9)`,
		uuid.NewString(), session.CurrentCompanyID, session.User.ID, auditType, entityID, details, meta.TraceID, meta.IP, meta.UserAgent); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]string{"pack_id": entityID})
	_, err := tx.Exec(ctx, `INSERT INTO outbox_events(event_id,type,schema_version,company_id,trace_id,payload) VALUES($1,$2,1,$3,$4,$5)`,
		uuid.NewString(), outboxType, session.CurrentCompanyID, meta.TraceID, payload)
	return err
}
