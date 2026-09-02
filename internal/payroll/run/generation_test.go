package run

import "testing"

func TestAttendanceDays(t *testing.T) {
	cases := []struct {
		name                                      string
		paid, unpaid, recorded, daysInMonth, want int
		wantFull                                  bool
	}{
		// full-time generated month: 22 worked + 8 paid weekly rest = 30 paid, 30 rows
		{"full month", 30, 0, 30, 30, 30, true},
		{"full 31-day month", 31, 0, 31, 31, 30, true},
		// user marked 2 days worked, left 26 rows blank (no schedule)
		{"two worked, rest blank rows", 2, 0, 28, 30, 2, false},
		// user marked 2 worked and deleted the rest
		{"two worked, rest deleted", 2, 0, 2, 30, 2, false},
		// user marked 2 worked, rest of the weekdays absent
		{"two worked, weekdays absent", 2, 20, 30, 30, 2, false},
		// mid-month hire: 11 worked + 5 rest = 16 paid over 16 rows
		{"mid-month hire", 16, 0, 16, 30, 16, false},
		// full month minus 2 unpaid leave days
		{"full month minus 2 unpaid leave", 28, 2, 30, 30, 28, false},
		{"nothing recorded", 0, 0, 0, 30, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			work, full := attendanceDays(c.paid, c.unpaid, c.recorded, c.daysInMonth)
			if work != c.want || full != c.wantFull {
				t.Fatalf("attendanceDays(%d,%d,%d,%d)=(%d,%v) want (%d,%v)",
					c.paid, c.unpaid, c.recorded, c.daysInMonth, work, full, c.want, c.wantFull)
			}
		})
	}
}

func TestGenerationActivationRevalidatesAllInputs(t *testing.T) {
	fingerprint := Fingerprint{PopulationHash: "p", TimesheetChecksum: "t", InputChecksum: "i", LegislationPackID: "l", LegislationPackVersion: 1}
	generation := Generation{ID: "g", Status: "SUCCEEDED", Initial: fingerprint, EmployeeTotal: 2, EmployeeSucceeded: 2}
	changed := fingerprint
	changed.InputChecksum = "changed"
	generation, err := generation.Activate(changed)
	if err != ErrInputChanged || generation.Status != "FAILED" {
		t.Fatalf("generation=%+v error=%v", generation, err)
	}
}
func TestFinalizeIsIdempotentAfterSuccessfulLockValidation(t *testing.T) {
	fingerprint := Fingerprint{PopulationHash: "p", TimesheetChecksum: "t", InputChecksum: "i", LegislationPackID: "l", LegislationPackVersion: 1}
	generation := Generation{ID: "g", Status: "ACTIVE", Initial: fingerprint}
	run := PayrollRun{ID: "r", Status: "CALCULATED", ActiveGenerationID: "g"}
	run, generation, err := run.Finalize(generation, fingerprint)
	if err != nil || run.Status != "FINALIZED" || generation.Status != "FINALIZED" {
		t.Fatalf("run=%+v generation=%+v err=%v", run, generation, err)
	}
	if _, _, err = run.Finalize(generation, fingerprint); err != nil {
		t.Fatalf("retry=%v", err)
	}
}
