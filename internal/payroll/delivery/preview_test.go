package delivery

import "testing"

func TestPreviewNeverFallsBackAndBlocksDuplicates(t *testing.T) {
	p := BuildPreview([]Candidate{{EmployeeID: "a", PayrollEmail: "same@example.com", PayslipID: "p"}, {EmployeeID: "b", PayrollEmail: "SAME@example.com", PayslipID: "p"}, {EmployeeID: "c"}})
	if len(p.Duplicate) != 2 || len(p.Missing) != 1 || len(p.Ready) != 0 {
		t.Fatalf("preview=%+v", p)
	}
}
func TestAmbiguousDataAcceptanceIsNeverRetried(t *testing.T) {
	if got := ClassifySMTP(0, true, true, false); got != StatusUnknown {
		t.Fatalf("outcome=%s", got)
	}
}
