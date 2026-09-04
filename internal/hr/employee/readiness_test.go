package employee

import "testing"

// A half-filled employee card used to fail silently: no çalışma dönemi meant the
// puantaj generator skipped the person, and no ücret tanımı meant the bordro
// dropped them. Both now have to come back as a named issue.
func TestReadinessNamesWhatIsMissing(t *testing.T) {
	full := employeeFacts{
		EmployeeID: "e1", EmployeeCode: "P-1", Name: "Ayşe Yılmaz",
		HasEmployment: true, InPeriod: true, HasWageTerm: true,
		GrossWage: "45000.00", Currency: "TRY", WorkType: "FULL_TIME",
		SchemeCode: "NO_DISCOUNT", SgkStatus: "4A", WageType: "GROSS", WagePeriod: "MONTHLY",
	}
	cases := []struct {
		name          string
		mutate        func(*employeeFacts)
		wantCode      string
		wantTimesheet bool
	}{
		{"complete card", func(*employeeFacts) {}, "", true},
		{"no employment at all", func(f *employeeFacts) { f.HasEmployment, f.InPeriod, f.HasWageTerm = false, false, false },
			"EMPLOYEE_NO_EMPLOYMENT", false},
		{"employed, but not this month", func(f *employeeFacts) { f.InPeriod, f.HasWageTerm = false, false },
			"EMPLOYEE_NOT_EMPLOYED_IN_PERIOD", false},
		{"no wage", func(f *employeeFacts) { f.HasWageTerm = false }, "EMPLOYEE_NO_WAGE", true},
		{"zero wage", func(f *employeeFacts) { f.GrossWage = "0.00" }, "EMPLOYEE_WAGE_ZERO", true},
		{"foreign currency", func(f *employeeFacts) { f.Currency = "EUR" }, "EMPLOYEE_WAGE_CURRENCY", true},
		{"part time", func(f *employeeFacts) { f.WorkType = "PART_TIME" }, "EMPLOYEE_WORK_TYPE", true},
		{"no scheme", func(f *employeeFacts) { f.SchemeCode = "" }, "EMPLOYEE_NO_SCHEME", true},
		{"bağ-kur", func(f *employeeFacts) { f.SgkStatus = "4B" }, "EMPLOYEE_SGK_STATUS", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			facts := full
			c.mutate(&facts)
			got := readinessOf(facts)
			if got.Timesheet != c.wantTimesheet {
				t.Fatalf("Timesheet = %v, want %v (issues %+v)", got.Timesheet, c.wantTimesheet, got.Issues)
			}
			if c.wantCode == "" {
				if len(got.Issues) != 0 || !got.Payroll {
					t.Fatalf("a complete card reported %+v", got.Issues)
				}
				return
			}
			if got.Payroll {
				t.Fatal("Payroll must be false while an issue stands")
			}
			if len(got.Issues) == 0 || got.Issues[0].Code != c.wantCode {
				t.Fatalf("issues = %+v, want first code %s", got.Issues, c.wantCode)
			}
			if got.Issues[0].Message == "" {
				t.Fatal("an issue with no message tells the user nothing")
			}
		})
	}
}

// A timesheet blocker has to be reportable on its own: the puantaj refuses the
// day with exactly this sentence.
func TestTimesheetBlockerIsOnlyTheTimesheetIssue(t *testing.T) {
	noEmployment := readinessOf(employeeFacts{EmployeeID: "e1", Name: "Ayşe Yılmaz"})
	if noEmployment.TimesheetBlocker() == "" {
		t.Fatal("no employment must block the puantaj")
	}
	noWage := readinessOf(employeeFacts{EmployeeID: "e2", HasEmployment: true, InPeriod: true})
	if noWage.TimesheetBlocker() != "" {
		t.Fatalf("a missing wage must not block the puantaj: %q", noWage.TimesheetBlocker())
	}
	if noWage.Payroll {
		t.Fatal("a missing wage must block the bordro")
	}
}

func TestIsZeroWage(t *testing.T) {
	for _, zero := range []string{"", " ", "0", "0.00", "0.0000", "-1200.00"} {
		if !isZeroWage(zero) {
			t.Fatalf("isZeroWage(%q) = false", zero)
		}
	}
	for _, paid := range []string{"0.01", "45000.00", "17002.12"} {
		if isZeroWage(paid) {
			t.Fatalf("isZeroWage(%q) = true", paid)
		}
	}
}
