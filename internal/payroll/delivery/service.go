package delivery

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/email"
	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/payroll/payslip"
	"github.com/alpyxn/varyaone/internal/platform/database"
	"github.com/alpyxn/varyaone/internal/platform/secrets"
	"github.com/alpyxn/varyaone/internal/storage"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const templateVersion = "tr-payslip-v2"

var (
	ErrPayrollNotFinalized = errors.New("PAYROLL_NOT_FINALIZED")
	ErrPayslipNotFound     = errors.New("PAYSLIP_NOT_FOUND")
	ErrExportNotFound      = errors.New("PAYROLL_EXPORT_NOT_FOUND")
	ErrRunNotFinalized     = errors.New("PAYROLL_RUN_NOT_FINALIZED")
	ErrSMTPNotConfigured   = errors.New("SMTP_SETTINGS_NOT_FOUND")
	ErrNothingToSend       = errors.New("PAYROLL_EMAIL_NOTHING_TO_SEND")
)

type Service struct {
	pool      database.Querier
	provider  storage.StorageProvider
	box       *secrets.Box
	renderer  payslip.GoPDFRenderer
	templates *email.TemplateService
}

func NewService(pool database.Querier, provider storage.StorageProvider, masterKey []byte) (*Service, error) {
	box, err := secrets.NewBox(masterKey)
	if err != nil {
		return nil, err
	}
	return &Service{
		pool:      pool,
		provider:  provider,
		box:       box,
		renderer:  payslip.GoPDFRenderer{TemplateVersion: templateVersion},
		templates: email.NewTemplateService(pool),
	}, nil
}

type Payslip struct {
	ID                string    `json:"id"`
	EmployeePayrollID string    `json:"employee_payroll_id"`
	EmployeeName      string    `json:"employee_name"`
	TemplateVersion   string    `json:"template_version"`
	SizeBytes         int64     `json:"size_bytes"`
	SHA256            string    `json:"sha256"`
	CreatedAt         time.Time `json:"created_at"`
}

// ---- Payslips ----

func (s *Service) GeneratePayslipsForRun(ctx context.Context, session identity.Session, runID string, meta identity.RequestMeta) ([]Payslip, error) {
	if !session.HasPermission("hr.payroll.payslip") {
		return nil, identity.ErrForbidden
	}
	var status string
	if err := s.pool.QueryRow(ctx, `SELECT status FROM payroll_runs WHERE company_id=$1 AND id=NULLIF($2,'')::uuid`, session.CurrentCompanyID, runID).Scan(&status); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrPayslipNotFound
	} else if err != nil {
		return nil, err
	}
	if status != "FINALIZED" {
		return nil, ErrRunNotFinalized
	}
	rows, err := s.pool.Query(ctx, `SELECT p.id::text FROM employee_payrolls p
 WHERE p.company_id=$1 AND p.payroll_run_id=NULLIF($2,'')::uuid AND p.status='FINALIZED' ORDER BY p.id`, session.CurrentCompanyID, runID)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	out := []Payslip{}
	for _, id := range ids {
		p, err := s.generatePayslip(ctx, session, id, meta)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func (s *Service) GeneratePayslip(ctx context.Context, session identity.Session, employeePayrollID string, meta identity.RequestMeta) (Payslip, error) {
	if !session.HasPermission("hr.payroll.payslip") {
		return Payslip{}, identity.ErrForbidden
	}
	return s.generatePayslip(ctx, session, employeePayrollID, meta)
}

func (s *Service) generatePayslip(ctx context.Context, session identity.Session, employeePayrollID string, meta identity.RequestMeta) (Payslip, error) {
	snapshot, checksum, employeeName, err := s.buildSnapshot(ctx, session.CurrentCompanyID, employeePayrollID)
	if err != nil {
		return Payslip{}, err
	}

	var existingID string
	err = s.pool.QueryRow(ctx, `SELECT id::text FROM payroll_payslips
 WHERE company_id=$1 AND employee_payroll_id=$2 AND template_version=$3 AND source_checksum=$4`,
		session.CurrentCompanyID, employeePayrollID, templateVersion, checksum).Scan(&existingID)
	if err == nil {
		return s.getPayslip(ctx, session.CurrentCompanyID, existingID)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Payslip{}, err
	}

	var buf bytes.Buffer
	md, err := s.renderer.Render(ctx, snapshot, &buf)
	if err != nil {
		if errors.Is(err, payslip.ErrPayrollNotFinalized) {
			return Payslip{}, ErrPayrollNotFinalized
		}
		return Payslip{}, err
	}
	payload := buf.Bytes()
	digest := sha256.Sum256(payload)
	id := uuid.NewString()
	key := fmt.Sprintf("companies/%s/payroll/payslips/%s.pdf", session.CurrentCompanyID, id)
	if _, err = storage.PutBytes(ctx, s.provider, key, payload, storage.PutOptions{ContentType: md.MIMEType, MaxBytes: 20 << 20}); err != nil {
		return Payslip{}, err
	}
	renderMeta, _ := json.Marshal(map[string]any{"template_version": templateVersion, "page_count": md.PageCount, "mime_type": md.MIMEType})

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		_ = s.provider.Delete(context.WithoutCancel(ctx), key)
		return Payslip{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	_, err = tx.Exec(ctx, `INSERT INTO payroll_payslips(id,company_id,employee_payroll_id,template_version,source_checksum,storage_key,sha256,size_bytes,render_metadata)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		id, session.CurrentCompanyID, employeePayrollID, templateVersion, checksum, key, hex.EncodeToString(digest[:]), len(payload), renderMeta)
	if err != nil {
		_ = s.provider.Delete(context.WithoutCancel(ctx), key)
		return Payslip{}, err
	}
	_, _ = tx.Exec(ctx, `INSERT INTO payroll_payslip_jobs(id,company_id,employee_payroll_id,template_version,status,payslip_id,started_at,completed_at)
 VALUES($1,$2,$3,$4,'SUCCEEDED',$5,now(),now())
 ON CONFLICT(company_id,employee_payroll_id,template_version) DO UPDATE SET status='SUCCEEDED',payslip_id=EXCLUDED.payslip_id,completed_at=now()`,
		uuid.NewString(), session.CurrentCompanyID, employeePayrollID, templateVersion, id)
	_ = writeEvent(ctx, tx, session, meta, "PAYSLIP_GENERATED", "hr.payslip.generated", employeePayrollID)
	if err = tx.Commit(ctx); err != nil {
		_ = s.provider.Delete(context.WithoutCancel(ctx), key)
		return Payslip{}, err
	}
	_ = employeeName
	return s.getPayslip(ctx, session.CurrentCompanyID, id)
}

func (s *Service) ListPayslips(ctx context.Context, session identity.Session, runID string) ([]Payslip, error) {
	if !session.HasPermission("hr.payroll.read") {
		return nil, identity.ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `SELECT ps.id::text,ps.employee_payroll_id::text,e.first_name||' '||e.last_name,ps.template_version,ps.size_bytes,ps.sha256,ps.created_at
 FROM payroll_payslips ps
 JOIN employee_payrolls ep ON ep.company_id=ps.company_id AND ep.id=ps.employee_payroll_id
 JOIN employees e ON e.company_id=ep.company_id AND e.id=ep.employee_id
 WHERE ps.company_id=$1 AND ep.payroll_run_id=NULLIF($2,'')::uuid AND ps.template_version=$3 ORDER BY e.first_name,e.last_name`,
		session.CurrentCompanyID, runID, templateVersion)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Payslip{}
	for rows.Next() {
		var p Payslip
		if err := rows.Scan(&p.ID, &p.EmployeePayrollID, &p.EmployeeName, &p.TemplateVersion, &p.SizeBytes, &p.SHA256, &p.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, p)
	}
	return items, rows.Err()
}

func (s *Service) getPayslip(ctx context.Context, companyID, id string) (Payslip, error) {
	var p Payslip
	err := s.pool.QueryRow(ctx, `SELECT ps.id::text,ps.employee_payroll_id::text,e.first_name||' '||e.last_name,ps.template_version,ps.size_bytes,ps.sha256,ps.created_at
 FROM payroll_payslips ps
 JOIN employee_payrolls ep ON ep.company_id=ps.company_id AND ep.id=ps.employee_payroll_id
 JOIN employees e ON e.company_id=ep.company_id AND e.id=ep.employee_id
 WHERE ps.company_id=$1 AND ps.id=$2`, companyID, id).
		Scan(&p.ID, &p.EmployeePayrollID, &p.EmployeeName, &p.TemplateVersion, &p.SizeBytes, &p.SHA256, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Payslip{}, ErrPayslipNotFound
	}
	return p, err
}

func (s *Service) DownloadPayslip(ctx context.Context, session identity.Session, payslipID string) (io.ReadCloser, storage.ObjectInfo, string, error) {
	if !session.HasPermission("hr.payroll.download") {
		return nil, storage.ObjectInfo{}, "", identity.ErrForbidden
	}
	var key, empCode string
	var periodYear, periodMonth int
	err := s.pool.QueryRow(ctx, `SELECT ps.storage_key,e.employee_code,r.period_year,r.period_month FROM payroll_payslips ps
 JOIN employee_payrolls ep ON ep.company_id=ps.company_id AND ep.id=ps.employee_payroll_id
 JOIN payroll_runs r ON r.company_id=ep.company_id AND r.id=ep.payroll_run_id
 JOIN employees e ON e.company_id=ep.company_id AND e.id=ep.employee_id
 WHERE ps.company_id=$1 AND ps.id=NULLIF($2,'')::uuid`, session.CurrentCompanyID, payslipID).Scan(&key, &empCode, &periodYear, &periodMonth)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, storage.ObjectInfo{}, "", ErrPayslipNotFound
	}
	if err != nil {
		return nil, storage.ObjectInfo{}, "", err
	}
	reader, info, err := s.provider.Open(ctx, key)
	if err != nil {
		return nil, storage.ObjectInfo{}, "", err
	}
	return reader, info, payslipFilename(empCode, periodYear, periodMonth), nil
}

var trMonths = [...]string{"", "Ocak", "Şubat", "Mart", "Nisan", "Mayıs", "Haziran",
	"Temmuz", "Ağustos", "Eylül", "Ekim", "Kasım", "Aralık"}

// componentLabels maps internal calculation component codes to employee-facing
// Turkish labels. Codes without an entry fall back to the stored component name.
var componentLabels = map[string]string{
	"BASE_WAGE":             "Asıl Ücret",
	"OVERTIME":              "Fazla Mesai",
	"WEEK_REST":             "Hafta Tatili Ücreti",
	"PUBLIC_HOLIDAY":        "Bayram / Genel Tatil Ücreti",
	"BONUS":                 "Prim",
	"PREMIUM":               "Prim / İkramiye",
	"MEAL":                  "Yemek Yardımı",
	"TRANSPORT":             "Yol Yardımı",
	"ADVANCE":               "Avans Mahsubu",
	"OTHER_DEDUCTION":       "Diğer Kesintiler",
	"SGK_EMPLOYEE":          "SGK İşçi Payı",
	"UNEMPLOYMENT_EMPLOYEE": "İşsizlik Sigortası İşçi Payı",
	"INCOME_TAX":            "Gelir Vergisi",
	"STAMP_TAX":             "Damga Vergisi",
	"GARNISHMENT":           "İcra Kesintisi",
	"ALIMONY":               "Nafaka",
}

func (s *Service) buildSnapshot(ctx context.Context, companyID, employeePayrollID string) (payslip.Snapshot, string, string, error) {
	var (
		status, checksum        string
		runNumber, paymentDate  string
		periodYear, periodMonth int
		gross, totalDeductions  string
		net                     string
		empCode, empName        string
		position                *string
		companyLegal            string
		companyLogo             *string
		paidDays, sgkDays       *string
		employeeSnapshot        []byte
	)
	err := s.pool.QueryRow(ctx, `SELECT ep.status,ep.employee_input_checksum,r.run_number,to_char(r.payment_date,'DD.MM.YYYY'),r.period_year,r.period_month,
 COALESCE(ep.gross,0)::text,COALESCE(ep.total_deductions,0)::text,COALESCE(ep.net,0)::text,
 e.employee_code,e.first_name||' '||e.last_name,e.position_title,c.legal_name,c.logo,
 ep.paid_days::text,ep.sgk_days::text,ep.employee_snapshot
 FROM employee_payrolls ep
 JOIN payroll_runs r ON r.company_id=ep.company_id AND r.id=ep.payroll_run_id
 JOIN employees e ON e.company_id=ep.company_id AND e.id=ep.employee_id
 JOIN companies c ON c.id=ep.company_id
 WHERE ep.company_id=$1 AND ep.id=NULLIF($2,'')::uuid`, companyID, employeePayrollID).
		Scan(&status, &checksum, &runNumber, &paymentDate, &periodYear, &periodMonth,
			&gross, &totalDeductions, &net, &empCode, &empName, &position, &companyLegal, &companyLogo,
			&paidDays, &sgkDays, &employeeSnapshot)
	if errors.Is(err, pgx.ErrNoRows) {
		return payslip.Snapshot{}, "", "", ErrPayslipNotFound
	}
	if err != nil {
		return payslip.Snapshot{}, "", "", err
	}
	if status != "FINALIZED" {
		return payslip.Snapshot{}, "", "", ErrPayrollNotFinalized
	}
	crows, err := s.pool.Query(ctx, `SELECT component_code,component_name,component_kind,amount::text FROM employee_payroll_components
 WHERE company_id=$1 AND employee_payroll_id=$2 ORDER BY calculation_order`, companyID, employeePayrollID)
	if err != nil {
		return payslip.Snapshot{}, "", "", err
	}
	defer crows.Close()

	var earnings, deductions []payslip.LineItem
	var earnSum, dedSum int64
	for crows.Next() {
		var code, name, kind, amount string
		if err := crows.Scan(&code, &name, &kind, &amount); err != nil {
			return payslip.Snapshot{}, "", "", err
		}
		label := componentLabels[code]
		if label == "" {
			label = name
		}
		switch kind {
		case "EARNING":
			earnings = append(earnings, payslip.LineItem{Label: label, Amount: formatTRY(amount)})
			earnSum += centsOf(amount)
		case "DEDUCTION":
			deductions = append(deductions, payslip.LineItem{Label: label, Amount: formatTRY(amount)})
			dedSum += centsOf(amount)
		}
		// EMPLOYER_COST and INFORMATION rows never reach the employee payslip.
	}
	if err := crows.Err(); err != nil {
		return payslip.Snapshot{}, "", "", err
	}

	// Reconcile the rendered rows against the canonical finalized totals. The
	// renderer must never show a number that disagrees with employee_payrolls.
	grossCents, dedCents, netCents := centsOf(gross), centsOf(totalDeductions), centsOf(net)
	if abs64(earnSum-grossCents) > 1 || abs64(dedSum-dedCents) > 1 || abs64(grossCents-dedCents-netCents) > 1 {
		return payslip.Snapshot{}, "", "", fmt.Errorf("payslip snapshot reconciliation failed for %s", employeePayrollID)
	}

	logo := ""
	if companyLogo != nil {
		logo = *companyLogo
	}
	work := []payslip.KeyValue{}
	if v := derefStr(paidDays); v != "" {
		work = append(work, payslip.KeyValue{Label: "Ücret Gün Sayısı", Value: trimZeros(v)})
	}
	if v := derefStr(sgkDays); v != "" {
		work = append(work, payslip.KeyValue{Label: "SGK Prim Gün Sayısı", Value: trimZeros(v)})
	}
	// The contractual wage comes from the snapshot frozen at calculation time. A
	// later raise must not rewrite a payslip that was already issued.
	var terms struct {
		GrossWage  string `json:"gross_wage"`
		WagePeriod string `json:"wage_period"`
	}
	_ = json.Unmarshal(employeeSnapshot, &terms)
	wageType, monthlyGross := "", ""
	if terms.WagePeriod == "MONTHLY" {
		wageType = "Aylık"
	}
	if centsOf(terms.GrossWage) > 0 {
		monthlyGross = formatTRY(terms.GrossWage)
	}

	snapshot := payslip.Snapshot{
		PayrollStatus: "FINALIZED",
		Run: payslip.RunSnapshot{
			Number:      runNumber,
			Period:      fmt.Sprintf("%s %d", trMonths[clampMonth(periodMonth)], periodYear),
			PaymentDate: paymentDate,
		},
		Company: payslip.CompanySnapshot{LegalName: companyLegal, LogoDataURI: logo},
		Employee: payslip.EmployeeSnapshot{
			Code: empCode, FullName: empName, PositionTitle: derefStr(position),
			WageType: wageType, MonthlyGross: monthlyGross,
		},
		Work:       work,
		Earnings:   earnings,
		Deductions: deductions,
		Totals: payslip.Totals{
			Gross:      formatTRY(gross),
			Deductions: formatTRY(totalDeductions),
			Net:        formatTRY(net),
		},
		SourceChecksum: checksum,
	}
	return snapshot, checksum, empName, nil
}

func clampMonth(m int) int {
	if m < 1 || m > 12 {
		return 0
	}
	return m
}

func derefStr(v *string) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(*v)
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// trimZeros turns "4.00" / "4" into "4" and keeps a fractional part otherwise.
func trimZeros(v string) string {
	if i := strings.IndexByte(v, '.'); i >= 0 {
		frac := strings.TrimRight(v[i+1:], "0")
		if frac == "" {
			return v[:i]
		}
		return v[:i] + "," + frac
	}
	return v
}

// centsOf parses a plain decimal string ("8333.33", "8333", "-12.5") into an
// integer number of kuruş. Values are engine-quantized to 2 dp so truncation of
// any extra digits is exact.
func centsOf(raw string) int64 {
	raw = strings.TrimSpace(raw)
	neg := strings.HasPrefix(raw, "-")
	raw = strings.TrimPrefix(strings.TrimPrefix(raw, "-"), "+")
	intPart, fracPart := raw, ""
	if i := strings.IndexByte(raw, '.'); i >= 0 {
		intPart, fracPart = raw[:i], raw[i+1:]
	}
	fracPart = (fracPart + "00")[:2]
	var n int64
	for _, c := range intPart {
		if c < '0' || c > '9' {
			continue
		}
		n = n*10 + int64(c-'0')
	}
	n *= 100
	if len(fracPart) == 2 && fracPart[0] >= '0' && fracPart[0] <= '9' && fracPart[1] >= '0' && fracPart[1] <= '9' {
		n += int64(fracPart[0]-'0')*10 + int64(fracPart[1]-'0')
	}
	if neg {
		return -n
	}
	return n
}

// payslipFilename builds "PRS-000111_Agustos-2026.pdf" (ASCII, no TCKN).
func payslipFilename(empCode string, year, month int) string {
	name := trMonths[clampMonth(month)]
	if name == "" {
		name = fmt.Sprintf("%02d", month)
	}
	folded := strings.NewReplacer(
		"ç", "c", "Ç", "C", "ğ", "g", "Ğ", "G", "ı", "i", "İ", "I",
		"ö", "o", "Ö", "O", "ş", "s", "Ş", "S", "ü", "u", "Ü", "U", " ", "-",
	).Replace(name)
	code := strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, empCode)
	if code == "" {
		code = "pusula"
	}
	return fmt.Sprintf("%s_%s-%d.pdf", code, folded, year)
}

// formatTRY renders "8333.33" as "8.333,33 TL".
func formatTRY(raw string) string {
	c := centsOf(raw)
	neg := c < 0
	c = abs64(c)
	whole := c / 100
	frac := c % 100
	digits := fmt.Sprintf("%d", whole)
	var grouped []byte
	for i := 0; i < len(digits); i++ {
		if i > 0 && (len(digits)-i)%3 == 0 {
			grouped = append(grouped, '.')
		}
		grouped = append(grouped, digits[i])
	}
	out := fmt.Sprintf("%s,%02d TL", string(grouped), frac)
	if neg {
		return "-" + out
	}
	return out
}

// ---- Exports ----

type Export struct {
	ID         string    `json:"id"`
	ExportType string    `json:"export_type"`
	Status     string    `json:"status"`
	SizeBytes  *int64    `json:"size_bytes,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

func (s *Service) CreateExport(ctx context.Context, session identity.Session, runID, exportType string, meta identity.RequestMeta) (Export, error) {
	if !session.HasPermission("hr.payroll.bulk_export") {
		return Export{}, identity.ErrForbidden
	}
	exportType = strings.ToUpper(strings.TrimSpace(exportType))
	if exportType != "PAYSLIP_ZIP" && exportType != "SUMMARY_CSV" {
		return Export{}, fmt.Errorf("%w: desteklenmeyen dışa aktarma türü (PAYSLIP_ZIP veya SUMMARY_CSV)", identity.ErrValidation)
	}
	var status, generationID string
	err := s.pool.QueryRow(ctx, `SELECT status,COALESCE(active_generation_id::text,'') FROM payroll_runs WHERE company_id=$1 AND id=NULLIF($2,'')::uuid`,
		session.CurrentCompanyID, runID).Scan(&status, &generationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Export{}, ErrExportNotFound
	}
	if err != nil {
		return Export{}, err
	}
	if status != "FINALIZED" || generationID == "" {
		return Export{}, ErrRunNotFinalized
	}

	var payload []byte
	var contentType string
	orderedPayslips := []struct{ ID, PayrollID string }{}
	if exportType == "PAYSLIP_ZIP" {
		payload, orderedPayslips, err = s.buildPayslipZip(ctx, session, runID)
		contentType = "application/zip"
	} else {
		payload, err = s.buildSummaryCSV(ctx, session.CurrentCompanyID, runID)
		contentType = "text/csv; charset=utf-8"
	}
	if err != nil {
		return Export{}, err
	}
	digest := sha256.Sum256(payload)
	id := uuid.NewString()
	ext := ".zip"
	if exportType == "SUMMARY_CSV" {
		ext = ".csv"
	}
	key := fmt.Sprintf("companies/%s/payroll/exports/%s%s", session.CurrentCompanyID, id, ext)
	if _, err = storage.PutBytes(ctx, s.provider, key, payload, storage.PutOptions{ContentType: contentType, MaxBytes: 100 << 20}); err != nil {
		return Export{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		_ = s.provider.Delete(context.WithoutCancel(ctx), key)
		return Export{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	_, err = tx.Exec(ctx, `INSERT INTO payroll_export_jobs(id,company_id,payroll_run_id,generation_id,export_type,status,storage_key,sha256,size_bytes,requested_by,started_at,completed_at)
 VALUES($1,$2,NULLIF($3,'')::uuid,$4,$5,'SUCCEEDED',$6,$7,$8,$9,now(),now())`,
		id, session.CurrentCompanyID, runID, generationID, exportType, key, hex.EncodeToString(digest[:]), len(payload), session.User.ID)
	if err != nil {
		_ = s.provider.Delete(context.WithoutCancel(ctx), key)
		return Export{}, err
	}
	for i, ps := range orderedPayslips {
		if _, err = tx.Exec(ctx, `INSERT INTO payroll_export_payslips(company_id,export_job_id,payslip_id,employee_payroll_id,document_order)
 VALUES($1,$2,NULLIF($3,'')::uuid,NULLIF($4,'')::uuid,$5)`,
			session.CurrentCompanyID, id, ps.ID, ps.PayrollID, i+1); err != nil {
			_ = s.provider.Delete(context.WithoutCancel(ctx), key)
			return Export{}, err
		}
	}
	_ = writeEvent(ctx, tx, session, meta, "PAYROLL_EXPORT_CREATED", "hr.payroll_export.created", runID)
	if err = tx.Commit(ctx); err != nil {
		_ = s.provider.Delete(context.WithoutCancel(ctx), key)
		return Export{}, err
	}
	size := int64(len(payload))
	return Export{ID: id, ExportType: exportType, Status: "SUCCEEDED", SizeBytes: &size, CreatedAt: time.Now()}, nil
}

func (s *Service) DownloadExport(ctx context.Context, session identity.Session, exportID string) (io.ReadCloser, storage.ObjectInfo, string, error) {
	if !session.HasPermission("hr.payroll.download") {
		return nil, storage.ObjectInfo{}, "", identity.ErrForbidden
	}
	var key, exportType string
	err := s.pool.QueryRow(ctx, `SELECT storage_key,export_type FROM payroll_export_jobs WHERE company_id=$1 AND id=NULLIF($2,'')::uuid AND status='SUCCEEDED'`,
		session.CurrentCompanyID, exportID).Scan(&key, &exportType)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, storage.ObjectInfo{}, "", ErrExportNotFound
	}
	if err != nil {
		return nil, storage.ObjectInfo{}, "", err
	}
	reader, info, err := s.provider.Open(ctx, key)
	if err != nil {
		return nil, storage.ObjectInfo{}, "", err
	}
	name := "bordro-export"
	if exportType == "PAYSLIP_ZIP" {
		name += ".zip"
	} else {
		name += ".csv"
	}
	return reader, info, name, nil
}

func (s *Service) buildPayslipZip(ctx context.Context, session identity.Session, runID string) ([]byte, []struct{ ID, PayrollID string }, error) {
	rows, err := s.pool.Query(ctx, `SELECT ps.id::text,ps.employee_payroll_id::text,ps.storage_key,e.employee_code,r.period_year,r.period_month
 FROM payroll_payslips ps
 JOIN employee_payrolls ep ON ep.company_id=ps.company_id AND ep.id=ps.employee_payroll_id
 JOIN payroll_runs r ON r.company_id=ep.company_id AND r.id=ep.payroll_run_id
 JOIN employees e ON e.company_id=ep.company_id AND e.id=ep.employee_id
 WHERE ps.company_id=$1 AND ep.payroll_run_id=NULLIF($2,'')::uuid ORDER BY e.employee_code`, session.CurrentCompanyID, runID)
	if err != nil {
		return nil, nil, err
	}
	type entry struct {
		ID, PayrollID, Key, Code string
		Year, Month              int
	}
	entries := []entry{}
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.ID, &e.PayrollID, &e.Key, &e.Code, &e.Year, &e.Month); err != nil {
			rows.Close()
			return nil, nil, err
		}
		entries = append(entries, e)
	}
	rows.Close()
	if len(entries) == 0 {
		return nil, nil, ErrPayslipNotFound
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	ordered := []struct{ ID, PayrollID string }{}
	for _, e := range entries {
		reader, _, err := s.provider.Open(ctx, e.Key)
		if err != nil {
			return nil, nil, err
		}
		w, err := zw.Create(payslipFilename(e.Code, e.Year, e.Month))
		if err != nil {
			reader.Close()
			return nil, nil, err
		}
		if _, err = io.Copy(w, reader); err != nil {
			reader.Close()
			return nil, nil, err
		}
		reader.Close()
		ordered = append(ordered, struct{ ID, PayrollID string }{e.ID, e.PayrollID})
	}
	if err := zw.Close(); err != nil {
		return nil, nil, err
	}
	return buf.Bytes(), ordered, nil
}

func (s *Service) buildSummaryCSV(ctx context.Context, companyID, runID string) ([]byte, error) {
	rows, err := s.pool.Query(ctx, `SELECT e.employee_code,e.first_name||' '||e.last_name,ep.status,
 COALESCE(ep.gross,0)::text,COALESCE(ep.employee_sgk,0)::text,COALESCE(ep.employee_unemployment,0)::text,
 COALESCE(ep.income_tax,0)::text,COALESCE(ep.stamp_tax,0)::text,COALESCE(ep.total_deductions,0)::text,
 COALESCE(ep.net,0)::text,COALESCE(ep.employer_cost,0)::text
 FROM employee_payrolls ep JOIN employees e ON e.company_id=ep.company_id AND e.id=ep.employee_id
 WHERE ep.company_id=$1 AND ep.payroll_run_id=NULLIF($2,'')::uuid ORDER BY e.employee_code`, companyID, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var b bytes.Buffer
	b.WriteString("\ufeff") // UTF-8 BOM for Excel
	b.WriteString("Kod;Ad;Durum;Brüt;SGK İşçi;İşsizlik İşçi;Gelir Vergisi;Damga Vergisi;Toplam Kesinti;Net;İşveren Maliyeti\n")
	for rows.Next() {
		cells := make([]string, 11)
		ptrs := make([]any, 11)
		for i := range cells {
			ptrs[i] = &cells[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		for i, c := range cells {
			if i > 0 {
				b.WriteString(";")
			}
			b.WriteString(strings.ReplaceAll(c, ";", ","))
		}
		b.WriteString("\n")
	}
	return b.Bytes(), rows.Err()
}

// ---- Email ----

// EmailRecipientRow is one payslip e-mail addressee shown in the composer.
type EmailRecipientRow struct {
	EmployeeID        string            `json:"employee_id"`
	EmployeePayrollID string            `json:"employee_payroll_id"`
	Name              string            `json:"name"`
	Email             string            `json:"email"`
	Status            string            `json:"status"`
	PayslipFilename   string            `json:"payslip_filename"`
	Variables         map[string]string `json:"variables"`
}

// EmailPreview extends the classification buckets with the editable defaults and
// the resolved recipient list for the generic composer.
type EmailPreview struct {
	Preview
	Recipients     []EmailRecipientRow `json:"recipients"`
	DefaultSubject string              `json:"default_subject"`
	DefaultBody    string              `json:"default_body"`
	Variables      []string            `json:"variables"`
}

const (
	fallbackPayslipSubject = "{{donem}} Ücret Hesap Pusulanız"
	fallbackPayslipBody    = "Sayın {{ad_soyad}},\n\n{{donem}} dönemine ait ücret hesap pusulanız ektedir.\n\nİyi çalışmalar,\n{{firma}}"
)

// payslipDefaults returns the effective subject/body: the active PAYROLL_PAYSLIP
// template if one exists, otherwise a built-in fallback.
func (s *Service) payslipDefaults(ctx context.Context, companyID string) (subject, body string) {
	subject, body = fallbackPayslipSubject, fallbackPayslipBody
	tpl, err := s.templates.DefaultForScope(ctx, companyID, "PAYROLL_PAYSLIP")
	if err == nil && tpl.ID != "" {
		if strings.TrimSpace(tpl.Subject) != "" {
			subject = tpl.Subject
		}
		if strings.TrimSpace(tpl.Body) != "" {
			body = tpl.Body
		}
	}
	return subject, body
}

func recipientStatuses(p Preview) map[string]string {
	out := map[string]string{}
	for _, id := range p.AlreadySent {
		out[id] = "already_sent"
	}
	for _, id := range p.Missing {
		out[id] = "missing"
	}
	for _, id := range p.Invalid {
		out[id] = "invalid"
	}
	for _, id := range p.Duplicate {
		out[id] = "duplicate"
	}
	for _, id := range p.MissingPayslip {
		out[id] = "missing_payslip"
	}
	for _, id := range p.Ready {
		out[id] = "ready"
	}
	return out
}

func (s *Service) PreviewEmail(ctx context.Context, session identity.Session, runID string) (EmailPreview, error) {
	if !session.HasPermission("hr.payroll.email") {
		return EmailPreview{}, identity.ErrForbidden
	}
	candidates, err := s.emailCandidates(ctx, session.CurrentCompanyID, runID)
	if err != nil {
		return EmailPreview{}, err
	}
	meta, err := s.runEmailMeta(ctx, session.CurrentCompanyID, runID)
	if err != nil {
		return EmailPreview{}, err
	}
	base := BuildPreview(candidates)
	statuses := recipientStatuses(base)
	subject, body := s.payslipDefaults(ctx, session.CurrentCompanyID)
	out := EmailPreview{
		Preview:        base,
		DefaultSubject: subject,
		DefaultBody:    body,
		Variables:      PayslipVariableKeys,
		Recipients:     make([]EmailRecipientRow, 0, len(candidates)),
	}
	for _, c := range candidates {
		out.Recipients = append(out.Recipients, EmailRecipientRow{
			EmployeeID:        c.EmployeeID,
			EmployeePayrollID: c.EmployeePayrollID,
			Name:              c.EmployeeName,
			Email:             c.PayrollEmail,
			Status:            statuses[c.EmployeeID],
			PayslipFilename:   payslipFilename(c.EmployeeCode, meta.PeriodYear, meta.PeriodMonth),
			Variables:         candidateVariables(c, meta),
		})
	}
	return out, nil
}

type EmailBatchResult struct {
	BatchID string  `json:"batch_id"`
	Status  string  `json:"status"`
	Sent    int     `json:"sent"`
	Failed  int     `json:"failed"`
	Skipped int     `json:"skipped"`
	Preview Preview `json:"preview"`
}

func (s *Service) SendEmailBatch(ctx context.Context, session identity.Session, runID string, isResend bool, subject, body string, meta identity.RequestMeta) (EmailBatchResult, error) {
	if !session.HasPermission("hr.payroll.email") {
		return EmailBatchResult{}, identity.ErrForbidden
	}
	settings, password, err := email.LoadSMTP(ctx, s.pool, s.box, session.CurrentCompanyID)
	if errors.Is(err, email.ErrSMTPNotConfigured) {
		return EmailBatchResult{}, ErrSMTPNotConfigured
	}
	if err != nil {
		return EmailBatchResult{}, err
	}
	runMeta, err := s.runEmailMeta(ctx, session.CurrentCompanyID, runID)
	if err != nil {
		return EmailBatchResult{}, err
	}
	defaultSubject, defaultBody := s.payslipDefaults(ctx, session.CurrentCompanyID)
	if strings.TrimSpace(subject) == "" {
		subject = defaultSubject
	}
	if strings.TrimSpace(body) == "" {
		body = defaultBody
	}
	candidates, err := s.emailCandidates(ctx, session.CurrentCompanyID, runID)
	if err != nil {
		return EmailBatchResult{}, err
	}
	// A resend re-sends the payslips that already went out, but it does not get
	// to skip the recipient checks: an address that has since become invalid, or
	// that is now shared with a second employee, must not receive a payslip.
	preview := BuildPreview(candidates)
	ready := map[string]bool{}
	for _, id := range preview.Ready {
		ready[id] = true
	}
	if isResend {
		for _, id := range preview.AlreadySent {
			ready[id] = true
		}
		for _, id := range append(append(append([]string{}, preview.Missing...), preview.Invalid...), preview.Duplicate...) {
			delete(ready, id)
		}
		for _, id := range preview.MissingPayslip {
			delete(ready, id)
		}
	}
	sendable := []Candidate{}
	for _, c := range candidates {
		if ready[c.EmployeeID] {
			sendable = append(sendable, c)
		}
	}
	if len(sendable) == 0 {
		return EmailBatchResult{}, ErrNothingToSend
	}

	batchID := uuid.NewString()
	idempotencyKey := runID + ":" + batchID
	if _, err = s.pool.Exec(ctx, `INSERT INTO payroll_email_batches(id,company_id,payroll_run_id,idempotency_key,status,is_resend,requested_by)
 VALUES($1,$2,NULLIF($3,'')::uuid,$4,'RUNNING',$5,$6)`,
		batchID, session.CurrentCompanyID, runID, idempotencyKey, isResend, session.User.ID); err != nil {
		return EmailBatchResult{}, err
	}

	// Every SMTP call happens outside a transaction and each delivery is recorded
	// on its own. Sending inside one long transaction meant a failure part-way
	// through rolled back the record of e-mails that had physically been sent,
	// and the next attempt mailed every payslip a second time.
	result := EmailBatchResult{BatchID: batchID, Preview: preview}
	for _, c := range sendable {
		vars := candidateVariables(c, runMeta)
		renderedSubject := email.RenderText(subject, vars)
		pdf, perr := s.payslipBytes(ctx, session.CurrentCompanyID, c.PayslipID)
		outcome := email.PermanentFailure
		var smtpCode int
		if perr == nil {
			smtpCode, outcome = email.Send(settings, password, email.Message{
				To:       strings.ToLower(strings.TrimSpace(c.PayrollEmail)),
				ToName:   c.EmployeeName,
				Subject:  renderedSubject,
				BodyText: email.RenderText(body, vars),
				Attachments: []email.Attachment{{
					Filename:    payslipFilename(c.EmployeeCode, runMeta.PeriodYear, runMeta.PeriodMonth),
					ContentType: "application/pdf",
					Data:        pdf,
				}},
			})
		}
		status, errCode := email.DeliveryStatus(outcome)
		// context.WithoutCancel: the mail is already out the door, so the record
		// of it must be written even if the caller has gone away.
		if _, derr := s.pool.Exec(context.WithoutCancel(ctx), `INSERT INTO payroll_email_deliveries(id,company_id,batch_id,employee_payroll_id,payslip_id,recipient_snapshot,subject_snapshot,status,attempt_count,smtp_response_code,error_code,sent_at)
 VALUES($1,$2,$3,NULLIF($4,'')::uuid,NULLIF($5,'')::uuid,$6,$7,$8,1,$9,$10,CASE WHEN $8='SENT' THEN now() ELSE NULL END)`,
			uuid.NewString(), session.CurrentCompanyID, batchID, c.EmployeePayrollID, c.PayslipID,
			strings.ToLower(strings.TrimSpace(c.PayrollEmail)), renderedSubject, status, nullInt(smtpCode), nullString(errCode)); derr != nil {
			return EmailBatchResult{}, derr
		}
		switch status {
		case "SENT":
			result.Sent++
		case "SKIPPED_NO_EMAIL":
			result.Skipped++
		default:
			result.Failed++
		}
	}

	batchStatus := "COMPLETED"
	if result.Sent == 0 {
		batchStatus = "FAILED"
	}
	closeCtx := context.WithoutCancel(ctx)
	tx, err := s.pool.Begin(closeCtx)
	if err != nil {
		return EmailBatchResult{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(closeCtx)) }()
	if _, err = tx.Exec(closeCtx, `UPDATE payroll_email_batches SET status=$2,completed_at=now() WHERE company_id=$1 AND id=$3`,
		session.CurrentCompanyID, batchStatus, batchID); err != nil {
		return EmailBatchResult{}, err
	}
	_ = writeEvent(closeCtx, tx, session, meta, "PAYROLL_EMAIL_BATCH_SENT", "hr.payroll_email.sent", runID)
	if err = tx.Commit(closeCtx); err != nil {
		return EmailBatchResult{}, err
	}
	result.Status = batchStatus
	return result, nil
}

func (s *Service) emailCandidates(ctx context.Context, companyID, runID string) ([]Candidate, error) {
	rows, err := s.pool.Query(ctx, `SELECT ep.employee_id::text,ep.id::text,COALESCE(ps.id::text,''),COALESCE(pp.payroll_email,''),
 e.first_name||' '||e.last_name,e.employee_code,COALESCE(ep.net,0)::text,
 EXISTS(SELECT 1 FROM payroll_email_deliveries d JOIN payroll_email_batches b ON b.company_id=d.company_id AND b.id=d.batch_id
        WHERE d.company_id=ep.company_id AND d.employee_payroll_id=ep.id AND d.status='SENT')
 FROM employee_payrolls ep
 JOIN employees e ON e.company_id=ep.company_id AND e.id=ep.employee_id
 LEFT JOIN payroll_payslips ps ON ps.company_id=ep.company_id AND ps.employee_payroll_id=ep.id AND ps.template_version=$3
 LEFT JOIN employee_private_profiles pp ON pp.company_id=ep.company_id AND pp.employee_id=ep.employee_id
 WHERE ep.company_id=$1 AND ep.payroll_run_id=NULLIF($2,'')::uuid AND ep.status='FINALIZED'
 ORDER BY e.first_name,e.last_name`,
		companyID, runID, templateVersion)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Candidate{}
	for rows.Next() {
		var c Candidate
		if err := rows.Scan(&c.EmployeeID, &c.EmployeePayrollID, &c.PayslipID, &c.PayrollEmail,
			&c.EmployeeName, &c.EmployeeCode, &c.Net, &c.AlreadySent); err != nil {
			return nil, err
		}
		items = append(items, c)
	}
	return items, rows.Err()
}

// runEmailMeta carries the run-level values shared by every recipient of a
// payslip e-mail batch (placeholder values + attachment naming).
type runEmailMeta struct {
	Period      string
	CompanyName string
	PaymentDate string
	PeriodYear  int
	PeriodMonth int
}

func (s *Service) runEmailMeta(ctx context.Context, companyID, runID string) (runEmailMeta, error) {
	var m runEmailMeta
	err := s.pool.QueryRow(ctx, `SELECT r.period_year,r.period_month,to_char(r.payment_date,'DD.MM.YYYY'),c.legal_name
 FROM payroll_runs r JOIN companies c ON c.id=r.company_id
 WHERE r.company_id=$1 AND r.id=NULLIF($2,'')::uuid`, companyID, runID).
		Scan(&m.PeriodYear, &m.PeriodMonth, &m.PaymentDate, &m.CompanyName)
	if errors.Is(err, pgx.ErrNoRows) {
		return runEmailMeta{}, ErrPayslipNotFound
	}
	if err != nil {
		return runEmailMeta{}, err
	}
	m.Period = fmt.Sprintf("%s %d", trMonths[clampMonth(m.PeriodMonth)], m.PeriodYear)
	return m, nil
}

func candidateVariables(c Candidate, m runEmailMeta) map[string]string {
	return map[string]string{
		"ad_soyad":     c.EmployeeName,
		"donem":        m.Period,
		"firma":        m.CompanyName,
		"net_maas":     formatTRY(c.Net),
		"odeme_tarihi": m.PaymentDate,
	}
}

// PayslipVariableKeys lists the placeholders available in a payslip e-mail body.
var PayslipVariableKeys = []string{"ad_soyad", "donem", "firma", "net_maas", "odeme_tarihi"}

func (s *Service) payslipBytes(ctx context.Context, companyID, payslipID string) ([]byte, error) {
	var key string
	if err := s.pool.QueryRow(ctx, `SELECT storage_key FROM payroll_payslips WHERE company_id=$1 AND id=NULLIF($2,'')::uuid`, companyID, payslipID).Scan(&key); err != nil {
		return nil, err
	}
	reader, _, err := s.provider.Open(ctx, key)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}

func nullInt(value int) any {
	if value == 0 {
		return nil
	}
	return value
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func writeEvent(ctx context.Context, tx pgx.Tx, session identity.Session, meta identity.RequestMeta, auditType, outboxType, entityID string) error {
	details, _ := json.Marshal(map[string]any{"entity_id": entityID})
	if _, err := tx.Exec(ctx, `INSERT INTO security_audit_events(id,company_id,actor_user_id,event_type,entity_type,entity_id,details,trace_id,source_ip,user_agent)
 VALUES($1,$2,$3,$4,'payroll_delivery',$5,$6,$7,$8,$9)`,
		uuid.NewString(), session.CurrentCompanyID, session.User.ID, auditType, entityID, details, meta.TraceID, meta.IP, meta.UserAgent); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]string{"entity_id": entityID})
	_, err := tx.Exec(ctx, `INSERT INTO outbox_events(event_id,type,schema_version,company_id,trace_id,payload) VALUES($1,$2,1,$3,$4,$5)`,
		uuid.NewString(), outboxType, session.CurrentCompanyID, meta.TraceID, payload)
	return err
}
