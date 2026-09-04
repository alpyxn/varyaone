package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alpyxn/varyaone/internal/hr/calendar"
	"github.com/alpyxn/varyaone/internal/hr/document"
	"github.com/alpyxn/varyaone/internal/hr/employee"
	"github.com/alpyxn/varyaone/internal/hr/employment"
	"github.com/alpyxn/varyaone/internal/hr/leave"
	hrschedule "github.com/alpyxn/varyaone/internal/hr/schedule"
	hrtimesheet "github.com/alpyxn/varyaone/internal/hr/timesheet"
	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/payroll/delivery"
	"github.com/alpyxn/varyaone/internal/payroll/legislation"
	payrollrun "github.com/alpyxn/varyaone/internal/payroll/run"
	"github.com/alpyxn/varyaone/internal/platform/migrations"
	"github.com/alpyxn/varyaone/internal/storage"
)

func TestHRPayrollEndToEnd(t *testing.T) {
	databaseURL := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool := httpPartyTestPool(t, ctx, databaseURL)
	if err := migrations.New(pool).Up(ctx); err != nil {
		t.Fatal(err)
	}
	masterKey := bytes.Repeat([]byte{7}, 32)
	identityService, err := identity.NewService(pool, masterKey)
	if err != nil {
		t.Fatal(err)
	}
	session, err := identityService.Setup(ctx, identity.SetupInput{AdminName: "Payroll Admin", AdminEmail: "pay@example.test", Password: "uzun-ve-guvenli-parola", LegalName: "Bordro AŞ", TradeName: "Bordro", EntityType: "LEGAL_ENTITY"}, identity.RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	legislationRepo := legislation.NewRepository(pool)
	employeeService, err := employee.NewService(pool, masterKey, legislationRepo)
	if err != nil {
		t.Fatal(err)
	}
	storageProvider, err := storage.NewLocalProvider(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	documentService := document.NewService(pool, storageProvider)
	deliveryService, err := delivery.NewService(pool, storageProvider, masterKey)
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(slog.New(slog.NewTextHandler(io.Discard, nil)), "test",
		readinessFunc(func(context.Context) error { return nil }),
		WithIdentity(identityService, false),
		WithHREmployee(employeeService),
		WithHREmployment(employment.NewService(pool, legislationRepo)),
		WithHRDocument(documentService),
		WithHRSchedule(hrschedule.NewService(pool)),
		WithHRLeave(leave.NewService(pool)),
		WithHRCalendar(calendar.NewService(pool)),
		WithHRTimesheet(hrtimesheet.NewService(pool)),
		WithPayrollLegislation(legislation.NewService(pool)),
		WithLegislationRepository(legislationRepo),
		WithPayrollRun(payrollrun.NewService(pool, legislationRepo)),
		WithPayrollDelivery(deliveryService))

	do := func(method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session.Token})
		request.Header.Set("X-CSRF-Token", session.CSRFToken)
		for k, v := range headers {
			request.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, request)
		return rec
	}
	mustJSON := func(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int) map[string]any {
		t.Helper()
		if rec.Code != wantStatus {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out
	}

	// An ACTIVE employee is created with their çalışma dönemi and ücret in one
	// request; without them the card would be invisible to the puantaj and
	// silently absent from the bordro.
	if bad := do(http.MethodPost, "/api/v1/hr/employees",
		`{"first_name":"Eksik","last_name":"Kayıt","status":"ACTIVE"}`, nil); bad.Code != http.StatusUnprocessableEntity {
		t.Fatalf("employee without employment: %d %s", bad.Code, bad.Body.String())
	}
	emp := mustJSON(t, do(http.MethodPost, "/api/v1/hr/employees",
		`{"employee_code":"","first_name":"Ali","last_name":"Veli","status":"ACTIVE","position_title":"Uzman","work_email":"","personal_email":"","phone":"",
 "employment":{"start_date":"2026-03-15","gross_wage":"40000"}}`, nil), http.StatusCreated)
	employeeID, _ := emp["id"].(string)

	employments := mustJSON(t, do(http.MethodGet, "/api/v1/hr/employees/"+employeeID+"/employments", "", nil), http.StatusOK)
	employmentRows, _ := employments["items"].([]any)
	if len(employmentRows) != 1 {
		t.Fatalf("expected the create to open one çalışma dönemi, got %s", toJSON(employments))
	}
	employmentRow, _ := employmentRows[0].(map[string]any)
	employmentID, _ := employmentRow["id"].(string)

	do(http.MethodPost, "/api/v1/hr/employees/"+employeeID+"/employments/"+employmentID+"/terms",
		`{"effective_from":"2026-01-05","gross_wage":"40000"}`, nil)
	// Second wage with only gross+work_type: previous open term must auto-close.
	if tr := do(http.MethodPost, "/api/v1/hr/employees/"+employeeID+"/employments/"+employmentID+"/terms",
		`{"effective_from":"2026-03-15","gross_wage":"50000.00","work_type":"FULL_TIME"}`, nil); tr.Code != http.StatusCreated {
		t.Fatalf("second wage term: %d %s", tr.Code, tr.Body.String())
	}
	termsList := mustJSON(t, do(http.MethodGet, "/api/v1/hr/employees/"+employeeID+"/employment-terms", "", nil), http.StatusOK)
	tItems, _ := termsList["items"].([]any)
	openCount := 0
	for _, it := range tItems {
		row, _ := it.(map[string]any)
		if row["effective_to"] == nil {
			openCount++
		}
	}
	if len(tItems) != 2 || openCount != 1 {
		t.Fatalf("expected 2 terms with 1 open, got %s", toJSON(termsList))
	}

	// Adding a second employment carries the latest wage term onto the new period.
	carryEmp := mustJSON(t, do(http.MethodPost, "/api/v1/hr/employees",
		`{"first_name":"Naz","last_name":"Kaya","status":"INACTIVE"}`, nil), http.StatusCreated)
	carryEmpID, _ := carryEmp["id"].(string)
	do(http.MethodPost, "/api/v1/hr/employees/"+carryEmpID+"/employments", `{"start_date":"2026-01-01"}`, nil)
	e1 := mustJSON(t, do(http.MethodGet, "/api/v1/hr/employees/"+carryEmpID+"/employments", "", nil), http.StatusOK)
	e1Row, _ := e1["items"].([]any)[0].(map[string]any)
	e1ID, _ := e1Row["id"].(string)
	e1Ver := int64(e1Row["version"].(float64))
	do(http.MethodPost, "/api/v1/hr/employees/"+carryEmpID+"/employments/"+e1ID+"/terms",
		`{"effective_from":"2026-01-01","gross_wage":"48000","work_type":"FULL_TIME"}`, nil)
	if tr := do(http.MethodPost, "/api/v1/hr/employees/"+carryEmpID+"/employments/"+e1ID+"/terminate",
		`{"end_date":"2026-05-31","termination_reason":"istifa"}`, map[string]string{"If-Match": itoaQuote(e1Ver)}); tr.Code != http.StatusOK {
		t.Fatalf("terminate first employment: %d %s", tr.Code, tr.Body.String())
	}
	if er := do(http.MethodPost, "/api/v1/hr/employees/"+carryEmpID+"/employments", `{"start_date":"2026-06-01"}`, nil); er.Code != http.StatusCreated {
		t.Fatalf("second employment: %d %s", er.Code, er.Body.String())
	}
	carryTerms := mustJSON(t, do(http.MethodGet, "/api/v1/hr/employees/"+carryEmpID+"/employment-terms", "", nil), http.StatusOK)
	cItems, _ := carryTerms["items"].([]any)
	var carried map[string]any
	for _, it := range cItems {
		row, _ := it.(map[string]any)
		if row["effective_from"] == "2026-06-01" {
			carried = row
		}
	}
	if carried == nil {
		t.Fatalf("expected a carried wage term effective 2026-06-01, got %s", toJSON(carryTerms))
	}
	if gw, _ := carried["gross_wage"].(string); gw != "48000.0000" {
		t.Fatalf("carried term gross wage = %q, want 48000.0000", carried["gross_wage"])
	}
	// Terminating the first employment closed its open term at the end date.
	// Adding the carried term is the only remaining open one for the employee.
	openCarry := 0
	for _, it := range cItems {
		row, _ := it.(map[string]any)
		if row["effective_to"] == nil {
			openCarry++
		}
		if row["effective_from"] == "2026-01-01" && row["effective_to"] != "2026-05-31" {
			t.Fatalf("first term should close at 2026-05-31 on termination, got %s", toJSON(row))
		}
	}
	if openCarry != 1 {
		t.Fatalf("expected exactly 1 open wage term for employee, got %d: %s", openCarry, toJSON(carryTerms))
	}

	tmpl := mustJSON(t, do(http.MethodPost, "/api/v1/hr/schedule-templates", `{"code":"STD","name":"Standart"}`, nil), http.StatusCreated)
	templateID, _ := tmpl["id"].(string)
	days := `[`
	for wd := 1; wd <= 7; wd++ {
		if wd > 1 {
			days += ","
		}
		if wd <= 5 {
			days += `{"weekday":` + itoa(wd) + `,"is_workday":true,"starts_at":"09:00","ends_at":"18:00","ends_next_day":false,"break_minutes":60,"planned_minutes":480}`
		} else {
			days += `{"weekday":` + itoa(wd) + `,"is_workday":false,"break_minutes":0,"planned_minutes":0}`
		}
	}
	days += `]`
	vr := do(http.MethodPost, "/api/v1/hr/schedule-templates/"+templateID+"/versions",
		`{"effective_from":"2026-03-01","effective_to":"","days":`+days+`}`, nil)
	if vr.Code != http.StatusCreated {
		t.Fatalf("schedule version: %d %s", vr.Code, vr.Body.String())
	}
	do(http.MethodPost, "/api/v1/hr/employees/"+employeeID+"/schedule-assignments",
		`{"template_id":"`+templateID+`","effective_from":"2026-03-15","effective_to":""}`, nil)

	// Schedule version can be deleted while unused.
	spare := mustJSON(t, do(http.MethodPost, "/api/v1/hr/schedule-templates", `{"code":"SPARE","name":"Yedek"}`, nil), http.StatusCreated)
	spareID, _ := spare["id"].(string)
	spareVer := mustJSON(t, do(http.MethodPost, "/api/v1/hr/schedule-templates/"+spareID+"/versions",
		`{"effective_from":"2026-04-01","effective_to":"","days":`+days+`}`, nil), http.StatusCreated)
	spareVerList, _ := spareVer["versions"].([]any)
	spareVerID, _ := spareVerList[0].(map[string]any)["id"].(string)
	delVer := do(http.MethodDelete, "/api/v1/hr/schedule-templates/"+spareID+"/versions/"+spareVerID, "", nil)
	delVerOut := mustJSON(t, delVer, http.StatusOK)
	if vers, _ := delVerOut["versions"].([]any); len(vers) != 0 {
		t.Fatalf("expected spare template to have no versions after delete, got %s", toJSON(delVer.Body.String()))
	}

	period := mustJSON(t, do(http.MethodPost, "/api/v1/hr/timesheet-periods", `{"period_year":2026,"period_month":3}`, nil), http.StatusCreated)
	periodID, _ := period["id"].(string)
	gen := do(http.MethodPost, "/api/v1/hr/timesheet-periods/"+periodID+"/generate", "", nil)
	if gen.Code != http.StatusOK {
		t.Fatalf("generate timesheet: %d %s", gen.Code, gen.Body.String())
	}
	// Manual calendar edits: mark a paid-leave day (picking a seeded PAID leave
	// type, as the puantaj UI does), then remove it again.
	leaveTypes := mustJSON(t, do(http.MethodGet, "/api/v1/hr/leave-types", "", nil), http.StatusOK)
	var paidLeaveTypeID string
	if items, _ := leaveTypes["items"].([]any); len(items) > 0 {
		for _, item := range items {
			row, _ := item.(map[string]any)
			if row["payroll_treatment"] == "PAID" {
				paidLeaveTypeID, _ = row["id"].(string)
				break
			}
		}
	}
	if paidLeaveTypeID == "" {
		t.Fatalf("no seeded PAID leave type found: %s", toJSON(leaveTypes))
	}
	up := do(http.MethodPut, "/api/v1/hr/timesheet-periods/"+periodID+"/days",
		`{"employee_id":"`+employeeID+`","work_date":"2026-03-09","kind":"PAID_LEAVE","leave_type_id":"`+paidLeaveTypeID+`"}`, nil)
	upOut := mustJSON(t, up, http.StatusOK)
	var manualDayID string
	upDays, _ := upOut["days"].([]any)
	for _, d := range upDays {
		row, _ := d.(map[string]any)
		if row["work_date"] == "2026-03-09" {
			if row["source"] != "MANUAL" || numberField(row, "paid_leave_minutes") <= 0 {
				t.Fatalf("manual paid-leave day not applied: %s", toJSON(row))
			}
			manualDayID, _ = row["id"].(string)
		}
	}
	if manualDayID == "" {
		t.Fatalf("manual day missing: %s", up.Body.String())
	}
	if bad := do(http.MethodPut, "/api/v1/hr/timesheet-periods/"+periodID+"/days",
		`{"employee_id":"`+employeeID+`","work_date":"2026-05-01","kind":"WORKED"}`, nil); bad.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for out-of-period day, got %d %s", bad.Code, bad.Body.String())
	}
	if del := do(http.MethodDelete, "/api/v1/hr/timesheet-periods/"+periodID+"/days/"+manualDayID, "", nil); del.Code != http.StatusOK {
		t.Fatalf("delete day: %d %s", del.Code, del.Body.String())
	}

	// A card with no çalışma dönemi may not be put on the puantaj at all: it used
	// to accept the days and then vanish from the bordro without a word.
	brokenEmp := mustJSON(t, do(http.MethodPost, "/api/v1/hr/employees",
		`{"first_name":"Bozuk","last_name":"Kart","status":"INACTIVE"}`, nil), http.StatusCreated)
	brokenID, _ := brokenEmp["id"].(string)
	brokenCode, _ := brokenEmp["employee_code"].(string)
	brokenVersion := int64(numberField(brokenEmp, "version"))
	if act := do(http.MethodPatch, "/api/v1/hr/employees/"+brokenID,
		`{"employee_code":"`+brokenCode+`","first_name":"Bozuk","last_name":"Kart","status":"ACTIVE"}`,
		map[string]string{"If-Match": itoaQuote(brokenVersion)}); act.Code != http.StatusOK {
		t.Fatalf("activate broken employee: %d %s", act.Code, act.Body.String())
	}
	notReady := do(http.MethodPut, "/api/v1/hr/timesheet-periods/"+periodID+"/days",
		`{"employee_id":"`+brokenID+`","work_date":"2026-03-10","kind":"WORKED"}`, nil)
	if notReady.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(notReady.Body.String(), "TIMESHEET_EMPLOYEE_NOT_READY") ||
		!strings.Contains(notReady.Body.String(), "İşe giriş tarihi") {
		t.Fatalf("expected the puantaj to name the missing işe giriş tarihi: %d %s", notReady.Code, notReady.Body.String())
	}
	readiness := mustJSON(t, do(http.MethodGet, "/api/v1/hr/timesheet-periods/"+periodID+"/readiness", "", nil), http.StatusOK)
	readyRows, _ := readiness["items"].([]any)
	var brokenRow map[string]any
	for _, row := range readyRows {
		item, _ := row.(map[string]any)
		if item["employee_id"] == brokenID {
			brokenRow = item
		}
	}
	if brokenRow == nil || brokenRow["timesheet_ready"] != false {
		t.Fatalf("readiness must flag the broken card: %s", toJSON(readiness))
	}
	// Park it again so the rest of the run keeps its single-employee population.
	brokenVersion = int64(numberField(mustJSON(t, do(http.MethodGet, "/api/v1/hr/employees/"+brokenID, "", nil), http.StatusOK), "version"))
	if off := do(http.MethodPatch, "/api/v1/hr/employees/"+brokenID,
		`{"employee_code":"`+brokenCode+`","first_name":"Bozuk","last_name":"Kart","status":"INACTIVE"}`,
		map[string]string{"If-Match": itoaQuote(brokenVersion)}); off.Code != http.StatusOK {
		t.Fatalf("deactivate broken employee: %d %s", off.Code, off.Body.String())
	}

	genOut := mustJSON(t, do(http.MethodGet, "/api/v1/hr/timesheet-periods/"+periodID, "", nil), http.StatusOK)
	pv := int64(numberField(genOut, "version"))
	fin := do(http.MethodPost, "/api/v1/hr/timesheet-periods/"+periodID+"/finalize", "", map[string]string{"If-Match": itoaQuote(pv)})
	if fin.Code != http.StatusOK || !strings.Contains(fin.Body.String(), `"status":"FINALIZED"`) {
		t.Fatalf("finalize timesheet: %d %s", fin.Code, fin.Body.String())
	}

	packs := mustJSON(t, do(http.MethodGet, "/api/v1/hr/legislation-packs", "", nil), http.StatusOK)
	packID := firstActivePackID(packs)
	if packID == "" {
		t.Fatalf("no active legislation pack: %s", toJSON(packs))
	}

	runResp := do(http.MethodPost, "/api/v1/hr/payroll-runs",
		`{"run_number":"","period_year":2026,"period_month":3,"payment_date":"2026-04-05","timesheet_period_id":"`+periodID+`","legislation_pack_id":"`+packID+`"}`, nil)
	runOut := mustJSON(t, runResp, http.StatusCreated)
	runID, _ := runOut["id"].(string)

	calc := do(http.MethodPost, "/api/v1/hr/payroll-runs/"+runID+"/calculate", "", nil)
	calcOut := mustJSON(t, calc, http.StatusOK)
	if calcOut["status"] != "CALCULATED" {
		t.Fatalf("calculate status=%v body=%s", calcOut["status"], calc.Body.String())
	}
	// A mid-month hire must use the term effective on the employment start date,
	// rather than disappearing because no term was effective on the first of the month.
	eps, _ := calcOut["employee_payrolls"].([]any)
	if len(eps) != 1 {
		t.Fatalf("expected 1 employee payroll, got %s", calc.Body.String())
	}
	if ep0, _ := eps[0].(map[string]any); ep0["status"] != "CALCULATED" {
		t.Fatalf("employee payroll not CALCULATED: %s", calc.Body.String())
	}

	rv := int64(numberField(calcOut, "version"))
	finRun := do(http.MethodPost, "/api/v1/hr/payroll-runs/"+runID+"/finalize", "", map[string]string{"If-Match": itoaQuote(rv)})
	finRunOut := mustJSON(t, finRun, http.StatusOK)
	if finRunOut["status"] != "FINALIZED" {
		t.Fatalf("finalize run: %s", finRun.Body.String())
	}

	slips := mustJSON(t, do(http.MethodPost, "/api/v1/hr/payroll-runs/"+runID+"/payslips", "", nil), http.StatusOK)
	items, _ := slips["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected 1 payslip: %s", toJSON(slips))
	}
	slip0, _ := items[0].(map[string]any)
	slipID, _ := slip0["id"].(string)
	dl := do(http.MethodGet, "/api/v1/hr/payslips/"+slipID+"/download", "", nil)
	if dl.Code != http.StatusOK || dl.Body.Len() == 0 {
		t.Fatalf("payslip download: %d len=%d", dl.Code, dl.Body.Len())
	}

	preview := do(http.MethodGet, "/api/v1/hr/payroll-runs/"+runID+"/email-preview", "", nil)
	if preview.Code != http.StatusOK || !strings.Contains(preview.Body.String(), `"missing"`) {
		t.Fatalf("email preview: %d %s", preview.Code, preview.Body.String())
	}
	previewOut := mustJSON(t, preview, http.StatusOK)
	recips, _ := previewOut["recipients"].([]any)
	if len(recips) == 0 {
		t.Fatalf("email preview has no recipients: %s", preview.Body.String())
	}
	r0, _ := recips[0].(map[string]any)
	vars0, _ := r0["variables"].(map[string]any)
	for _, key := range []string{"ad_soyad", "donem", "firma", "net_maas", "odeme_tarihi"} {
		if _, ok := vars0[key]; !ok {
			t.Fatalf("email preview recipient missing variable %q: %s", key, preview.Body.String())
		}
	}
	if s, _ := vars0["firma"].(string); s == "" {
		t.Fatalf("email preview {{firma}} variable is empty: %s", preview.Body.String())
	}

	// An employee employed this month with no ücret tanımı used to be dropped by
	// an inner join, so the bordro simply came out one person short. They now
	// appear on their own row and say what is missing.
	wagelessEmp := mustJSON(t, do(http.MethodPost, "/api/v1/hr/employees",
		`{"first_name":"Ücretsiz","last_name":"Kayıt","status":"INACTIVE"}`, nil), http.StatusCreated)
	wagelessID, _ := wagelessEmp["id"].(string)
	wagelessCode, _ := wagelessEmp["employee_code"].(string)
	if e := do(http.MethodPost, "/api/v1/hr/employees/"+wagelessID+"/employments",
		`{"start_date":"2026-03-01"}`, nil); e.Code != http.StatusCreated {
		t.Fatalf("wageless employment: %d %s", e.Code, e.Body.String())
	}
	wagelessVersion := int64(numberField(mustJSON(t, do(http.MethodGet, "/api/v1/hr/employees/"+wagelessID, "", nil), http.StatusOK), "version"))
	if act := do(http.MethodPatch, "/api/v1/hr/employees/"+wagelessID,
		`{"employee_code":"`+wagelessCode+`","first_name":"Ücretsiz","last_name":"Kayıt","status":"ACTIVE"}`,
		map[string]string{"If-Match": itoaQuote(wagelessVersion)}); act.Code != http.StatusOK {
		t.Fatalf("activate wageless employee: %d %s", act.Code, act.Body.String())
	}
	secondRun := mustJSON(t, do(http.MethodPost, "/api/v1/hr/payroll-runs",
		`{"run_number":"","period_year":2026,"period_month":3,"payment_date":"2026-04-05","timesheet_period_id":"`+periodID+`","legislation_pack_id":"`+packID+`"}`, nil),
		http.StatusCreated)
	secondRunID, _ := secondRun["id"].(string)
	secondCalc := do(http.MethodPost, "/api/v1/hr/payroll-runs/"+secondRunID+"/calculate", "", nil)
	secondOut := mustJSON(t, secondCalc, http.StatusOK)
	if secondOut["status"] != "CALCULATION_FAILED" {
		t.Fatalf("a wageless employee must fail the run, got %v: %s", secondOut["status"], secondCalc.Body.String())
	}
	secondPayrolls, _ := secondOut["employee_payrolls"].([]any)
	found := false
	for _, row := range secondPayrolls {
		item, _ := row.(map[string]any)
		if item["employee_id"] != wagelessID {
			continue
		}
		found = true
		if item["status"] != "FAILED" || !strings.Contains(toJSON(item["error_details"]), "employee_no_wage") {
			t.Fatalf("wageless employee row does not name the missing wage: %s", toJSON(item))
		}
	}
	if !found {
		t.Fatalf("wageless employee missing from the bordro entirely: %s", secondCalc.Body.String())
	}
}

func itoa(v int) string { return itoaQuoteRaw(int64(v)) }
func itoaQuoteRaw(v int64) string {
	digits := ""
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	for v > 0 {
		digits = string(rune('0'+v%10)) + digits
		v /= 10
	}
	if neg {
		digits = "-" + digits
	}
	return digits
}
func itoaQuote(v int64) string { return `"` + itoaQuoteRaw(v) + `"` }

func numberField(m map[string]any, key string) float64 {
	if v, ok := m[key].(float64); ok {
		return v
	}
	return 0
}

func toJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func firstActivePackID(m map[string]any) string {
	items, _ := m["items"].([]any)
	for _, it := range items {
		row, _ := it.(map[string]any)
		if row["status"] == "ACTIVE" {
			id, _ := row["id"].(string)
			return id
		}
	}
	return ""
}
