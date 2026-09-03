package run

import (
	"testing"

	"github.com/alpyxn/varyaone/internal/money"
	"github.com/alpyxn/varyaone/internal/payroll/calculation"
	"github.com/alpyxn/varyaone/internal/payroll/legislation"
)

// A month the timesheet says nothing usable about used to calculate a silent
// zero-lira payroll that could then be finalized and paid.
func TestCheckAttendanceRejectsAnEmptyMonth(t *testing.T) {
	cases := []struct {
		name                   string
		paid, unpaid, recorded int
		wantErr                bool
	}{
		{"no rows at all", 0, 0, 0, true},
		{"every row blank", 0, 0, 30, true},
		{"fully unpaid month", 0, 30, 30, false},
		{"normal month", 30, 0, 30, false},
		{"mid-month hire", 16, 0, 16, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := checkAttendance(c.paid, c.unpaid, c.recorded)
			if (err != nil) != c.wantErr {
				t.Fatalf("checkAttendance(%d,%d,%d) = %v, wantErr=%v", c.paid, c.unpaid, c.recorded, err, c.wantErr)
			}
			if err != nil && !calculation.ErrorIsCode(err, calculation.ErrPopulationNotSupported) {
				t.Fatalf("error is not a reportable calculation error: %v", err)
			}
		})
	}
}

// Overtime recorded on the puantaj has to reach the payslip: 10 hours at 1.5x on
// a 45000 monthly gross is 45000/225 * 1.5 * 10 = 3000.
func TestOvertimeComponentIsDerivedFromTimesheetMinutes(t *testing.T) {
	gross := mustDec("45000")
	days := mustDec("30")
	defs := map[string]legislation.ComponentDefinition{}

	component, ok := overtimeComponent(gross, 600, defs, days)
	if !ok {
		t.Fatal("no overtime component produced for 600 recorded minutes")
	}
	if component.Code != "OVERTIME" || component.Kind != "EARNING" || component.Ownership != "SYSTEM" {
		t.Fatalf("component = %+v", component)
	}
	want, _ := money.ParseDecimal("3000.00", 2)
	if component.Amount.Cmp(want) != 0 {
		t.Fatalf("overtime amount = %s, want %s", component.Amount, want)
	}
	if _, ok = overtimeComponent(gross, 0, defs, days); ok {
		t.Fatal("zero overtime minutes must not produce a component")
	}
}
