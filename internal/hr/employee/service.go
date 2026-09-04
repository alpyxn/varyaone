// Package employee owns company-scoped employee cards and private profiles.
package employee

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/hr/employment"
	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/payroll/legislation"
	"github.com/alpyxn/varyaone/internal/platform/codeseq"
	"github.com/alpyxn/varyaone/internal/platform/database"
	"github.com/alpyxn/varyaone/internal/platform/secrets"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrNotFound           = errors.New("EMPLOYEE_NOT_FOUND")
	ErrInvalidPeriod      = errors.New("EMPLOYEE_PERIOD_INVALID")
	ErrLegislationMissing = errors.New("PAYROLL_LEGISLATION_NOT_FOUND")
)

var occupationCodePattern = regexp.MustCompile(`^[0-9]{4}\.[0-9]{2}$`)

type Service struct {
	pool        database.Querier
	box         *secrets.Box
	legislation *legislation.Repository
}

func NewService(pool database.Querier, masterKey []byte, legislationRepo *legislation.Repository) (*Service, error) {
	box, err := secrets.NewBox(masterKey)
	if err != nil {
		return nil, err
	}
	return &Service{pool: pool, box: box, legislation: legislationRepo}, nil
}

type Employee struct {
	ID                   string  `json:"id"`
	EmployeeCode         string  `json:"employee_code"`
	FirstName            string  `json:"first_name"`
	LastName             string  `json:"last_name"`
	Status               string  `json:"status"`
	PositionTitle        string  `json:"position_title"`
	WorkEmail            *string `json:"work_email,omitempty"`
	PersonalEmail        *string `json:"personal_email,omitempty"`
	Phone                *string `json:"phone,omitempty"`
	OccupationCode       *string `json:"occupation_code,omitempty"`
	OccupationName       *string `json:"occupation_name,omitempty"`
	HireDate             *string `json:"hire_date,omitempty"`
	TerminationDate      *string `json:"termination_date,omitempty"`
	CreatedAt, UpdatedAt time.Time
	Version              int64 `json:"version"`
}
type Input struct {
	EmployeeCode   string `json:"employee_code"`
	FirstName      string `json:"first_name"`
	LastName       string `json:"last_name"`
	Status         string `json:"status"`
	PositionTitle  string `json:"position_title"`
	WorkEmail      string `json:"work_email"`
	PersonalEmail  string `json:"personal_email"`
	Phone          string `json:"phone"`
	OccupationCode string `json:"occupation_code"`
	// Employment is the çalışma dönemi + ücret the card starts with. It is
	// mandatory for an ACTIVE employee: without it the employee is invisible to
	// the puantaj generator and silently absent from every bordro, which is only
	// noticed when a payslip goes missing.
	Employment *EmploymentSetup `json:"employment,omitempty"`
}

// EmploymentSetup carries the first employment period and wage of a new
// employee. It is written in the same transaction as the card, so a half-made
// employee can never exist.
type EmploymentSetup struct {
	StartDate              string `json:"start_date"`
	IsMinimumWage          bool   `json:"is_minimum_wage"`
	GrossWage              string `json:"gross_wage"`
	Currency               string `json:"currency"`
	WorkType               string `json:"work_type"`
	WeeklyMinutes          int    `json:"weekly_minutes"`
	ContributionSchemeCode string `json:"contribution_scheme_code"`
	PriorEmployerTaxPolicy string `json:"prior_employer_tax_policy"`
	SgkStatus              string `json:"sgk_status"`
}

type OccupationCode struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

func (s *Service) SearchOccupationCodes(ctx context.Context, session identity.Session, query string, limit int) ([]OccupationCode, error) {
	if !session.HasPermission("hr.employee.read") {
		return nil, identity.ErrForbidden
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	query = strings.TrimSpace(query)
	rows, err := s.pool.Query(ctx, `SELECT code,name FROM sgk_occupation_codes
 WHERE $1='' OR code LIKE $1||'%' OR lower(name) LIKE '%'||lower($1)||'%'
 ORDER BY (code LIKE $1||'%') DESC, code LIMIT $2`, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []OccupationCode{}
	for rows.Next() {
		var oc OccupationCode
		if err := rows.Scan(&oc.Code, &oc.Name); err != nil {
			return nil, err
		}
		items = append(items, oc)
	}
	return items, rows.Err()
}

type ListResult struct {
	Items      []Employee `json:"items"`
	NextCursor string     `json:"next_cursor,omitempty"`
}
type PrivateProfile struct {
	BirthDate             *string `json:"birth_date,omitempty"`
	EmergencyContactName  *string `json:"emergency_contact_name,omitempty"`
	EmergencyContactPhone *string `json:"emergency_contact_phone,omitempty"`
	PayrollEmail          *string `json:"payroll_email,omitempty"`
	BankName              *string `json:"bank_name,omitempty"`
	TCKN                  string  `json:"tckn,omitempty"`
	IBAN                  string  `json:"iban,omitempty"`
	MaskedTCKN            string  `json:"masked_tckn,omitempty"`
	MaskedIBAN            string  `json:"masked_iban,omitempty"`
	HasTCKN               bool    `json:"has_tckn"`
	HasIBAN               bool    `json:"has_iban"`
	Version               int64   `json:"version"`
}
type PrivateProfileInput struct {
	TCKN                  *string `json:"tckn,omitempty"`
	IBAN                  *string `json:"iban,omitempty"`
	BirthDate             *string `json:"birth_date,omitempty"`
	EmergencyContactName  *string `json:"emergency_contact_name,omitempty"`
	EmergencyContactPhone *string `json:"emergency_contact_phone,omitempty"`
	PayrollEmail          *string `json:"payroll_email,omitempty"`
	BankName              *string `json:"bank_name,omitempty"`
}

const employeeSelect = `SELECT e.id::text,e.employee_code,e.first_name,e.last_name,e.status,e.position_title,e.work_email,e.personal_email,e.phone,
 e.occupation_code,oc.name,
 CASE WHEN history.hire_date IS NULL THEN NULL ELSE to_char(history.hire_date,'YYYY-MM-DD') END,
 CASE WHEN history.has_open THEN NULL WHEN history.termination_date IS NULL THEN NULL ELSE to_char(history.termination_date,'YYYY-MM-DD') END,
 e.created_at,e.updated_at,e.version FROM employees e
 LEFT JOIN sgk_occupation_codes oc ON oc.code=e.occupation_code
 LEFT JOIN LATERAL(
 SELECT min(start_date) hire_date,max(end_date) termination_date,bool_or(end_date IS NULL) has_open FROM employments x WHERE x.company_id=e.company_id AND x.employee_id=e.id
 ) history ON true `

func (s *Service) List(ctx context.Context, session identity.Session, query, status, cursor string, limit int) (ListResult, error) {
	if !session.HasPermission("hr.employee.read") {
		return ListResult{}, identity.ErrForbidden
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	cursorCode, cursorID := decodeCursor(cursor)
	rows, err := s.pool.Query(ctx, employeeSelect+` WHERE e.company_id=$1 AND ($2='' OR e.status=$2) AND ($3='' OR e.employee_code ILIKE '%'||$3||'%' OR e.first_name ILIKE '%'||$3||'%' OR e.last_name ILIKE '%'||$3||'%') AND ($4='' OR (e.employee_code,e.id)>( $4,NULLIF($5,'')::uuid)) ORDER BY e.employee_code,e.id LIMIT $6`, session.CurrentCompanyID, strings.ToUpper(strings.TrimSpace(status)), strings.TrimSpace(query), cursorCode, cursorID, limit+1)
	if err != nil {
		return ListResult{}, err
	}
	defer rows.Close()
	result := ListResult{Items: []Employee{}}
	for rows.Next() {
		item, err := scanEmployee(rows)
		if err != nil {
			return ListResult{}, err
		}
		result.Items = append(result.Items, item)
	}
	if err = rows.Err(); err != nil {
		return ListResult{}, err
	}
	if len(result.Items) > limit {
		last := result.Items[limit-1]
		result.Items = result.Items[:limit]
		result.NextCursor = encodeCursor(last.EmployeeCode, last.ID)
	}
	return result, nil
}
func (s *Service) Get(ctx context.Context, session identity.Session, id string) (Employee, error) {
	if !session.HasPermission("hr.employee.read") {
		return Employee{}, identity.ErrForbidden
	}
	item, err := scanEmployee(s.pool.QueryRow(ctx, employeeSelect+` WHERE e.company_id=$1 AND e.id=NULLIF($2,'')::uuid`, session.CurrentCompanyID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Employee{}, ErrNotFound
	}
	return item, err
}
func (s *Service) Create(ctx context.Context, session identity.Session, input Input, meta identity.RequestMeta) (Employee, error) {
	if !session.HasPermission("hr.employee.edit") {
		return Employee{}, identity.ErrForbidden
	}
	normalizeInput(&input)
	// The employee code is optional on create; a blank one is generated below.
	if err := validateInput(input, input.EmployeeCode == ""); err != nil {
		return Employee{}, err
	}
	term, startDate, err := s.resolveSetup(ctx, session.CurrentCompanyID, input)
	if err != nil {
		return Employee{}, err
	}
	id := uuid.NewString()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Employee{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if input.EmployeeCode == "" {
		if input.EmployeeCode, err = codeseq.NextWidth(ctx, tx, session.CurrentCompanyID, "employees", "employee_code", "P", 5); err != nil {
			return Employee{}, err
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO employees(id,company_id,employee_code,first_name,last_name,status,position_title,work_email,personal_email,phone,occupation_code) VALUES($1,$2,$3,$4,$5,$6,$7,NULLIF($8,''),NULLIF($9,''),NULLIF($10,''),NULLIF($11,''))`, id, session.CurrentCompanyID, input.EmployeeCode, input.FirstName, input.LastName, input.Status, input.PositionTitle, input.WorkEmail, input.PersonalEmail, input.Phone, input.OccupationCode)
	if err != nil {
		return Employee{}, mapOccupationFK(err)
	}
	if term != nil {
		employmentID := uuid.NewString()
		if _, err = tx.Exec(ctx, `INSERT INTO employments(id,company_id,employee_id,start_date) VALUES($1,$2,$3::uuid,$4::date)`,
			employmentID, session.CurrentCompanyID, id, startDate); err != nil {
			return Employee{}, mapConstraint(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO employment_terms(id,company_id,employment_id,employee_id,effective_from,
 wage_type,wage_period,gross_wage,currency,work_type,weekly_minutes,contribution_scheme_code,prior_employer_tax_policy,sgk_status,is_minimum_wage)
 VALUES($1,$2,$3::uuid,$4::uuid,$5::date,'GROSS','MONTHLY',$6::numeric,$7,$8,$9,$10,$11,$12,$13)`,
			uuid.NewString(), session.CurrentCompanyID, employmentID, id, startDate,
			term.GrossWage, term.Currency, term.WorkType, term.WeeklyMinutes,
			term.ContributionSchemeCode, term.PriorEmployerTaxPolicy, term.SgkStatus, input.Employment.IsMinimumWage); err != nil {
			return Employee{}, mapConstraint(err)
		}
	}
	if err = writeEvent(ctx, tx, session, meta, "EMPLOYEE_CREATED", "hr.employee.created", id, []string{"employee_code", "name", "status"}); err != nil {
		return Employee{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Employee{}, err
	}
	return s.Get(ctx, session, id)
}

// resolveSetup validates the çalışma dönemi + ücret block a new employee is
// created with. An ACTIVE employee must have one: an employee with no employment
// never reaches the puantaj, and one with no ücret tanımı is dropped from every
// bordro without a word. INACTIVE/ARCHIVED cards (historical records) may skip it.
func (s *Service) resolveSetup(ctx context.Context, companyID string, input Input) (*employment.TermInput, string, error) {
	setup := input.Employment
	if setup == nil {
		if input.Status == "ACTIVE" {
			return nil, "", fmt.Errorf("%w: aktif çalışan için işe giriş tarihi ve brüt ücret zorunlu", identity.ErrValidation)
		}
		return nil, "", nil
	}
	startDate := strings.TrimSpace(setup.StartDate)
	start, err := time.Parse("2006-01-02", startDate)
	if err != nil {
		return nil, "", fmt.Errorf("%w: işe giriş tarihi geçersiz", identity.ErrValidation)
	}
	term := &employment.TermInput{
		EffectiveFrom:          startDate,
		GrossWage:              strings.TrimSpace(setup.GrossWage),
		Currency:               setup.Currency,
		WorkType:               setup.WorkType,
		WeeklyMinutes:          setup.WeeklyMinutes,
		ContributionSchemeCode: setup.ContributionSchemeCode,
		PriorEmployerTaxPolicy: setup.PriorEmployerTaxPolicy,
		SgkStatus:              setup.SgkStatus,
		IsMinimumWage:          setup.IsMinimumWage,
	}
	if setup.IsMinimumWage {
		// Never trust a client-sent gross for a "minimum wage" term — pin it to
		// the legislation pack covering the hire date.
		if s.legislation == nil {
			return nil, "", fmt.Errorf("%w: asgari ücret bilgisi okunamadı", identity.ErrValidation)
		}
		pack, err := s.legislation.ActivePack(ctx, companyID, start)
		if errors.Is(err, legislation.ErrPackNotFound) {
			return nil, "", ErrLegislationMissing
		}
		if err != nil {
			return nil, "", err
		}
		term.GrossWage = pack.MinimumMonthlyGross.String()
	}
	// employment.NormalizeTerm fills the defaults (TRY, tam zamanlı, 4A,
	// NO_DISCOUNT, 45 saat) and rejects the rest, so the wage a new employee
	// starts with obeys exactly the same rules as one entered from the Ücret tab.
	if err := employment.NormalizeTerm(term); err != nil {
		return nil, "", err
	}
	return term, startDate, nil
}

func (s *Service) Update(ctx context.Context, session identity.Session, id string, version int64, input Input, meta identity.RequestMeta) (Employee, error) {
	if !session.HasPermission("hr.employee.edit") {
		return Employee{}, identity.ErrForbidden
	}
	normalizeInput(&input)
	if err := validateInput(input, false); err != nil {
		return Employee{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Employee{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	tag, err := tx.Exec(ctx, `UPDATE employees SET employee_code=$4,first_name=$5,last_name=$6,status=$7,position_title=$8,work_email=NULLIF($9,''),personal_email=NULLIF($10,''),phone=NULLIF($11,''),occupation_code=NULLIF($12,''),updated_at=now(),version=version+1 WHERE company_id=$1 AND id=NULLIF($2,'')::uuid AND version=$3`, session.CurrentCompanyID, id, version, input.EmployeeCode, input.FirstName, input.LastName, input.Status, input.PositionTitle, input.WorkEmail, input.PersonalEmail, input.Phone, input.OccupationCode)
	if err != nil {
		return Employee{}, mapOccupationFK(err)
	}
	if tag.RowsAffected() == 0 {
		return Employee{}, identity.ErrConflict
	}
	if err = writeEvent(ctx, tx, session, meta, "EMPLOYEE_UPDATED", "hr.employee.updated", id, []string{"employee_code", "name", "status", "contact"}); err != nil {
		return Employee{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Employee{}, err
	}
	return s.Get(ctx, session, id)
}

func (s *Service) GetPrivate(ctx context.Context, session identity.Session, employeeID string) (PrivateProfile, error) {
	if !session.HasPermission("hr.employee_private.read") {
		return PrivateProfile{}, identity.ErrForbidden
	}
	var item PrivateProfile
	var tckn, iban []byte
	err := s.pool.QueryRow(ctx, `SELECT tckn_ciphertext,iban_ciphertext,CASE WHEN birth_date IS NULL THEN NULL ELSE to_char(birth_date,'YYYY-MM-DD') END,emergency_contact_name,emergency_contact_phone,payroll_email,bank_name,version FROM employee_private_profiles WHERE company_id=$1 AND employee_id=NULLIF($2,'')::uuid`, session.CurrentCompanyID, employeeID).Scan(&tckn, &iban, &item.BirthDate, &item.EmergencyContactName, &item.EmergencyContactPhone, &item.PayrollEmail, &item.BankName, &item.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return PrivateProfile{}, ErrNotFound
	}
	if err != nil {
		return PrivateProfile{}, err
	}
	if len(tckn) > 0 {
		plain, err := s.box.Open(session.CurrentCompanyID, "employee_tckn", tckn)
		if err != nil {
			return PrivateProfile{}, err
		}
		item.HasTCKN = true
		item.TCKN = string(plain)
		item.MaskedTCKN = mask(string(plain))
		clear(plain)
	}
	if len(iban) > 0 {
		plain, err := s.box.Open(session.CurrentCompanyID, "employee_iban", iban)
		if err != nil {
			return PrivateProfile{}, err
		}
		item.HasIBAN = true
		item.IBAN = string(plain)
		item.MaskedIBAN = mask(string(plain))
		clear(plain)
	}
	return item, nil
}
func (s *Service) PutPrivate(ctx context.Context, session identity.Session, employeeID string, version int64, input PrivateProfileInput, meta identity.RequestMeta) (PrivateProfile, error) {
	if !session.HasPermission("hr.employee_private.edit") {
		return PrivateProfile{}, identity.ErrForbidden
	}
	normalizePrivate(&input)
	if err := validatePrivate(input); err != nil {
		return PrivateProfile{}, err
	}
	var tcknCipher, ibanCipher, tcknHash []byte
	var err error
	if input.TCKN != nil && *input.TCKN != "" {
		tcknCipher, err = s.box.Seal(session.CurrentCompanyID, "employee_tckn", []byte(*input.TCKN))
		if err != nil {
			return PrivateProfile{}, err
		}
		sum := sha256.Sum256([]byte(*input.TCKN))
		tcknHash = sum[:]
	}
	if input.IBAN != nil && *input.IBAN != "" {
		ibanCipher, err = s.box.Seal(session.CurrentCompanyID, "employee_iban", []byte(*input.IBAN))
		if err != nil {
			return PrivateProfile{}, err
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PrivateProfile{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var newVersion int64
	if version == 0 {
		err = tx.QueryRow(ctx, `INSERT INTO employee_private_profiles(company_id,employee_id,tckn_ciphertext,tckn_sha256,iban_ciphertext,birth_date,emergency_contact_name,emergency_contact_phone,payroll_email,bank_name) SELECT $1,e.id,$3,$4,$5,NULLIF($6,'')::date,NULLIF($7,''),NULLIF($8,''),NULLIF($9,''),NULLIF($10,'') FROM employees e WHERE e.company_id=$1 AND e.id=NULLIF($2,'')::uuid ON CONFLICT(company_id,employee_id) DO NOTHING RETURNING version`, session.CurrentCompanyID, employeeID, tcknCipher, tcknHash, ibanCipher, value(input.BirthDate), value(input.EmergencyContactName), value(input.EmergencyContactPhone), value(input.PayrollEmail), value(input.BankName)).Scan(&newVersion)
	} else {
		err = tx.QueryRow(ctx, `UPDATE employee_private_profiles SET tckn_ciphertext=CASE WHEN $4::boolean THEN $5 ELSE tckn_ciphertext END,tckn_sha256=CASE WHEN $4 THEN $6 ELSE tckn_sha256 END,iban_ciphertext=CASE WHEN $7::boolean THEN $8 ELSE iban_ciphertext END,birth_date=CASE WHEN $9::boolean THEN NULLIF($10,'')::date ELSE birth_date END,emergency_contact_name=CASE WHEN $11::boolean THEN NULLIF($12,'') ELSE emergency_contact_name END,emergency_contact_phone=CASE WHEN $13::boolean THEN NULLIF($14,'') ELSE emergency_contact_phone END,payroll_email=CASE WHEN $15::boolean THEN NULLIF($16,'') ELSE payroll_email END,bank_name=CASE WHEN $17::boolean THEN NULLIF($18,'') ELSE bank_name END,updated_at=now(),version=version+1 WHERE company_id=$1 AND employee_id=NULLIF($2,'')::uuid AND version=$3 RETURNING version`, session.CurrentCompanyID, employeeID, version, input.TCKN != nil, tcknCipher, tcknHash, input.IBAN != nil, ibanCipher, input.BirthDate != nil, value(input.BirthDate), input.EmergencyContactName != nil, value(input.EmergencyContactName), input.EmergencyContactPhone != nil, value(input.EmergencyContactPhone), input.PayrollEmail != nil, value(input.PayrollEmail), input.BankName != nil, value(input.BankName)).Scan(&newVersion)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return PrivateProfile{}, identity.ErrConflict
	}
	if err != nil {
		return PrivateProfile{}, err
	}
	if err = writeEvent(ctx, tx, session, meta, "EMPLOYEE_PRIVATE_PROFILE_UPDATED", "hr.employee.private_profile.updated", employeeID, []string{"private_profile"}); err != nil {
		return PrivateProfile{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return PrivateProfile{}, err
	}
	return s.GetPrivate(ctx, session, employeeID)
}

type Address struct {
	AddressLine      string  `json:"address_line"`
	PostalCode       *string `json:"postal_code,omitempty"`
	ProvinceID       *int    `json:"province_id,omitempty"`
	ProvinceName     *string `json:"province_name,omitempty"`
	DistrictID       *int64  `json:"district_id,omitempty"`
	DistrictName     *string `json:"district_name,omitempty"`
	NeighborhoodID   *int64  `json:"neighborhood_id,omitempty"`
	NeighborhoodName *string `json:"neighborhood_name,omitempty"`
	Neighborhood     *string `json:"neighborhood,omitempty"`
	Version          int64   `json:"version"`
}

type AddressInput struct {
	AddressLine    string `json:"address_line"`
	PostalCode     string `json:"postal_code"`
	ProvinceID     *int   `json:"province_id"`
	DistrictID     *int64 `json:"district_id"`
	NeighborhoodID *int64 `json:"neighborhood_id"`
	Neighborhood   string `json:"neighborhood"`
}

func (s *Service) GetAddress(ctx context.Context, session identity.Session, employeeID string) (Address, error) {
	if !session.HasPermission("hr.employee.read") {
		return Address{}, identity.ErrForbidden
	}
	var a Address
	err := s.pool.QueryRow(ctx, `SELECT a.address_line,a.postal_code,a.province_id,p.name,a.district_id,d.name,a.neighborhood_id,n.name,a.neighborhood,a.version
 FROM employee_addresses a
 LEFT JOIN turkish_provinces p ON p.id=a.province_id
 LEFT JOIN turkish_districts d ON d.id=a.district_id
 LEFT JOIN turkish_neighborhoods n ON n.id=a.neighborhood_id
 WHERE a.company_id=$1 AND a.employee_id=NULLIF($2,'')::uuid`, session.CurrentCompanyID, employeeID).
		Scan(&a.AddressLine, &a.PostalCode, &a.ProvinceID, &a.ProvinceName, &a.DistrictID, &a.DistrictName, &a.NeighborhoodID, &a.NeighborhoodName, &a.Neighborhood, &a.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return Address{Version: 0}, nil
	}
	return a, err
}

func (s *Service) PutAddress(ctx context.Context, session identity.Session, employeeID string, version int64, input AddressInput, meta identity.RequestMeta) (Address, error) {
	if !session.HasPermission("hr.employee.edit") {
		return Address{}, identity.ErrForbidden
	}
	input.AddressLine = strings.TrimSpace(input.AddressLine)
	input.PostalCode = strings.TrimSpace(input.PostalCode)
	input.Neighborhood = strings.TrimSpace(input.Neighborhood)
	if input.PostalCode != "" && (len(input.PostalCode) != 5 || strings.Trim(input.PostalCode, "0123456789") != "") {
		return Address{}, fmt.Errorf("%w: posta kodu 5 rakam olmalıdır", identity.ErrValidation)
	}
	// Enforce the province⇒district⇒neighbourhood hierarchy client-side too so the
	// FK/CHECK failure surfaces as a clean validation error.
	if input.DistrictID != nil && input.ProvinceID == nil {
		return Address{}, fmt.Errorf("%w: ilçe için önce il seçilmelidir", identity.ErrValidation)
	}
	if input.NeighborhoodID != nil && input.DistrictID == nil {
		return Address{}, fmt.Errorf("%w: mahalle için önce ilçe seçilmelidir", identity.ErrValidation)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Address{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var newVersion int64
	if version == 0 {
		err = tx.QueryRow(ctx, `INSERT INTO employee_addresses(company_id,employee_id,address_line,postal_code,province_id,district_id,neighborhood_id,neighborhood)
 SELECT $1,e.id,$3,NULLIF($4,''),$5,$6,$7,NULLIF($8,'') FROM employees e WHERE e.company_id=$1 AND e.id=NULLIF($2,'')::uuid
 ON CONFLICT(company_id,employee_id) DO NOTHING RETURNING version`,
			session.CurrentCompanyID, employeeID, input.AddressLine, input.PostalCode, input.ProvinceID, input.DistrictID, input.NeighborhoodID, input.Neighborhood).Scan(&newVersion)
	} else {
		err = tx.QueryRow(ctx, `UPDATE employee_addresses SET address_line=$3,postal_code=NULLIF($4,''),province_id=$5,district_id=$6,neighborhood_id=$7,neighborhood=NULLIF($8,''),updated_at=now(),version=version+1
 WHERE company_id=$1 AND employee_id=NULLIF($2,'')::uuid AND version=$9 RETURNING version`,
			session.CurrentCompanyID, employeeID, input.AddressLine, input.PostalCode, input.ProvinceID, input.DistrictID, input.NeighborhoodID, input.Neighborhood, version).Scan(&newVersion)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return Address{}, identity.ErrConflict
	}
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && (pgErr.Code == "23503" || pgErr.Code == "23514") {
			return Address{}, fmt.Errorf("%w: seçilen il/ilçe/mahalle geçersiz", identity.ErrValidation)
		}
		return Address{}, err
	}
	if err = writeEvent(ctx, tx, session, meta, "EMPLOYEE_UPDATED", "hr.employee.updated", employeeID, []string{"address"}); err != nil {
		return Address{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Address{}, err
	}
	return s.GetAddress(ctx, session, employeeID)
}

type rowScanner interface{ Scan(...any) error }

func scanEmployee(row rowScanner) (Employee, error) {
	var item Employee
	err := row.Scan(&item.ID, &item.EmployeeCode, &item.FirstName, &item.LastName, &item.Status, &item.PositionTitle, &item.WorkEmail, &item.PersonalEmail, &item.Phone, &item.OccupationCode, &item.OccupationName, &item.HireDate, &item.TerminationDate, &item.CreatedAt, &item.UpdatedAt, &item.Version)
	return item, err
}
func normalizeInput(input *Input) {
	input.EmployeeCode = strings.TrimSpace(input.EmployeeCode)
	input.FirstName = strings.TrimSpace(input.FirstName)
	input.LastName = strings.TrimSpace(input.LastName)
	input.Status = strings.ToUpper(strings.TrimSpace(input.Status))
	if input.Status == "" {
		input.Status = "ACTIVE"
	}
	input.PositionTitle = strings.TrimSpace(input.PositionTitle)
	input.WorkEmail = strings.ToLower(strings.TrimSpace(input.WorkEmail))
	input.PersonalEmail = strings.ToLower(strings.TrimSpace(input.PersonalEmail))
	input.Phone = strings.TrimSpace(input.Phone)
	input.OccupationCode = strings.TrimSpace(input.OccupationCode)
}
func validateInput(input Input, allowBlankCode bool) error {
	if (!allowBlankCode && input.EmployeeCode == "") || input.FirstName == "" || input.LastName == "" || (input.Status != "ACTIVE" && input.Status != "INACTIVE" && input.Status != "ARCHIVED") {
		return fmt.Errorf("%w: çalışan kartı alanları geçersiz", identity.ErrValidation)
	}
	if input.OccupationCode != "" && !occupationCodePattern.MatchString(input.OccupationCode) {
		return fmt.Errorf("%w: meslek kodu biçimi geçersiz", identity.ErrValidation)
	}
	return nil
}
func normalizePrivate(input *PrivateProfileInput) {
	for _, field := range []*string{input.TCKN, input.IBAN, input.BirthDate, input.EmergencyContactName, input.EmergencyContactPhone, input.PayrollEmail, input.BankName} {
		if field != nil {
			*field = strings.TrimSpace(*field)
		}
	}
	if input.TCKN != nil {
		*input.TCKN = strings.ReplaceAll(*input.TCKN, " ", "")
	}
	if input.IBAN != nil {
		*input.IBAN = strings.ToUpper(strings.ReplaceAll(*input.IBAN, " ", ""))
	}
	if input.PayrollEmail != nil {
		*input.PayrollEmail = strings.ToLower(*input.PayrollEmail)
	}
}
func validatePrivate(input PrivateProfileInput) error {
	if input.TCKN != nil && *input.TCKN != "" && (len(*input.TCKN) != 11 || strings.Trim(*input.TCKN, "0123456789") != "") {
		return fmt.Errorf("%w: TCKN 11 rakam olmalıdır", identity.ErrValidation)
	}
	if input.IBAN != nil && *input.IBAN != "" && (!strings.HasPrefix(*input.IBAN, "TR") || len(*input.IBAN) != 26) {
		return fmt.Errorf("%w: Türkiye IBAN değeri geçersiz", identity.ErrValidation)
	}
	if input.BirthDate != nil && *input.BirthDate != "" {
		if _, err := time.Parse("2006-01-02", *input.BirthDate); err != nil {
			return fmt.Errorf("%w: doğum tarihi geçersiz", identity.ErrValidation)
		}
	}
	if input.PayrollEmail != nil && *input.PayrollEmail != "" {
		address, err := mail.ParseAddress(*input.PayrollEmail)
		if err != nil || address.Address != *input.PayrollEmail {
			return fmt.Errorf("%w: bordro e-postası geçersiz", identity.ErrValidation)
		}
	}
	return nil
}

// mapConstraint turns the employment/term constraint failures raised while
// creating an employee into validation errors.
func mapConstraint(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	switch {
	case pgErr.Code == "23P01" || pgErr.Code == "23505":
		return fmt.Errorf("%w: çalışma dönemi ya da ücret kaydı çakışıyor", identity.ErrValidation)
	case pgErr.Code == "23514":
		return fmt.Errorf("%w: %s", identity.ErrValidation, pgErr.Message)
	}
	return err
}

func mapOccupationFK(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" && strings.Contains(pgErr.ConstraintName, "occupation_code") {
		return fmt.Errorf("%w: seçilen meslek kodu tanımlı değil", identity.ErrValidation)
	}
	return err
}

func value(input *string) string {
	if input == nil {
		return ""
	}
	return *input
}
func mask(value string) string {
	runes := []rune(value)
	if len(runes) <= 4 {
		return strings.Repeat("*", len(runes))
	}
	return strings.Repeat("*", len(runes)-4) + string(runes[len(runes)-4:])
}
func encodeCursor(code, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(code + "\x00" + id))
}
func decodeCursor(cursor string) (string, string) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return "", ""
	}
	parts := strings.SplitN(string(raw), "\x00", 2)
	if len(parts) != 2 {
		return "", ""
	}
	return parts[0], parts[1]
}
func writeEvent(ctx context.Context, tx pgx.Tx, session identity.Session, meta identity.RequestMeta, auditType, outboxType, employeeID string, fields []string) error {
	details, _ := json.Marshal(map[string]any{"employee_id": employeeID, "changed_fields": fields})
	if _, err := tx.Exec(ctx, `INSERT INTO security_audit_events(id,company_id,actor_user_id,event_type,entity_type,entity_id,details,trace_id,source_ip,user_agent) VALUES($1,$2,$3,$4,'employee',$5,$6,$7,$8,$9)`, uuid.NewString(), session.CurrentCompanyID, session.User.ID, auditType, employeeID, details, meta.TraceID, meta.IP, meta.UserAgent); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]string{"employee_id": employeeID})
	_, err := tx.Exec(ctx, `INSERT INTO outbox_events(event_id,type,schema_version,company_id,trace_id,payload) VALUES($1,$2,1,$3,$4,$5)`, uuid.NewString(), outboxType, session.CurrentCompanyID, meta.TraceID, payload)
	return err
}
