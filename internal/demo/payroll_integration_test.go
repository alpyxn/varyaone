package demo

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/payroll/legislation"
	"github.com/alpyxn/varyaone/internal/payroll/run"
	"github.com/alpyxn/varyaone/internal/platform/database"
	"github.com/alpyxn/varyaone/internal/platform/migrations"
)

// TestSeedProducesRunnablePayroll drives a full demo build and then runs
// payroll for real against the seeded company: every employee needs an
// employment period and a wage term, and a finalized timesheet period, before
// a payroll run can even be created. This is the regression test for the
// showcase shipping with employees that had none of that and so could never
// produce a payslip.
func TestSeedProducesRunnablePayroll(t *testing.T) {
	databaseURL := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	pool, maintenanceDSN := isolatedDemo(t, ctx, databaseURL)
	if err := migrations.New(pool).Up(ctx); err != nil {
		t.Fatal(err)
	}

	seededAt := time.Date(2026, 6, 15, 9, 0, 0, 0, time.UTC)
	runner := New(pool, Options{
		MaintenanceDSN: maintenanceDSN,
		MasterKey:      bytes.Repeat([]byte{9}, 32),
		Email:          "demo@varyaone.test",
		Password:       "varyaone-demo-2026",
		Now:            func() time.Time { return seededAt },
	})
	if err := runner.Ensure(ctx); err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	var employmentTerms, employeeCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM employment_terms WHERE company_id=$1`, CompanyID).Scan(&employmentTerms); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM employees WHERE company_id=$1`, CompanyID).Scan(&employeeCount); err != nil {
		t.Fatal(err)
	}
	if employmentTerms < employeeCount || employeeCount == 0 {
		t.Fatalf("expected every employee to have a wage term: %d employees, %d terms", employeeCount, employmentTerms)
	}

	var timesheetPeriodID, timesheetStatus string
	if err := pool.QueryRow(ctx, `SELECT id::text,status FROM timesheet_periods WHERE company_id=$1 ORDER BY period_year DESC,period_month DESC LIMIT 1`,
		CompanyID).Scan(&timesheetPeriodID, &timesheetStatus); err != nil {
		t.Fatalf("read seeded timesheet period: %v", err)
	}
	if timesheetStatus != "FINALIZED" {
		t.Fatalf("expected the seeded timesheet period to be finalized, got %q", timesheetStatus)
	}

	// Drive an actual payroll run through the domain service, exactly as a user
	// would from the UI, to prove the seeded data is enough to produce a payslip.
	pooled := database.NewScoped(pool)
	runService := run.NewService(pooled, legislation.NewRepository(pooled))
	identityService, err := identity.NewService(pool, bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatalf("build identity service: %v", err)
	}
	session, err := identityService.Login(ctx, "demo@varyaone.test", "varyaone-demo-2026", "", identity.RequestMeta{TraceID: "payroll-test"})
	if err != nil {
		t.Fatalf("login as demo user: %v", err)
	}

	created, err := runService.Create(ctx, session, run.RunInput{TimesheetPeriodID: timesheetPeriodID}, identity.RequestMeta{TraceID: "payroll-test", IdempotencyKey: "payroll-test:create"})
	if err != nil {
		t.Fatalf("create payroll run: %v", err)
	}
	calculated, err := runService.Calculate(ctx, session, created.ID, identity.RequestMeta{TraceID: "payroll-test", IdempotencyKey: "payroll-test:calculate"})
	if err != nil {
		t.Fatalf("calculate payroll run: %v", err)
	}
	if len(calculated.EmployeePayrolls) < employeeCount {
		t.Fatalf("expected a payslip per employee: got %d for %d employees", len(calculated.EmployeePayrolls), employeeCount)
	}
	for _, p := range calculated.EmployeePayrolls {
		if p.Status == "ERROR" {
			t.Errorf("employee %s payroll errored: %v", p.EmployeeName, p.ErrorDetails)
		}
		if p.Gross == nil || *p.Gross == "" || *p.Gross == "0.00" {
			t.Errorf("employee %s has no gross pay computed", p.EmployeeName)
		}
	}
}
