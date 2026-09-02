// Package run owns payroll-run and immutable calculation-generation lifecycle rules.
package run

import "errors"

var (
	ErrJobInProgress      = errors.New("PAYROLL_JOB_IN_PROGRESS")
	ErrGenerationFailed   = errors.New("PAYROLL_GENERATION_FAILED")
	ErrInputChanged       = errors.New("PAYROLL_INPUT_CHANGED")
	ErrNoActiveGeneration = errors.New("PAYROLL_ACTIVE_GENERATION_NOT_FOUND")
)

type Fingerprint struct {
	PopulationHash, TimesheetChecksum, InputChecksum string
	LegislationPackID                                string
	LegislationPackVersion                           int
	ManualInputVersion                               int64
}

func (f Fingerprint) Equal(other Fingerprint) bool { return f == other }

type Generation struct {
	ID                               string
	Number                           int
	Status                           string
	Initial                          Fingerprint
	EmployeeTotal, EmployeeSucceeded int
}

func (g Generation) Activate(current Fingerprint) (Generation, error) {
	if g.Status == "RUNNING" && g.EmployeeSucceeded != g.EmployeeTotal {
		return g, ErrGenerationFailed
	}
	if g.Status != "SUCCEEDED" && g.Status != "RUNNING" {
		return g, ErrGenerationFailed
	}
	if g.EmployeeTotal < 0 || g.EmployeeSucceeded != g.EmployeeTotal {
		return g, ErrGenerationFailed
	}
	if !g.Initial.Equal(current) {
		g.Status = "FAILED"
		return g, ErrInputChanged
	}
	g.Status = "ACTIVE"
	return g, nil
}

type PayrollRun struct {
	ID, Status, ActiveGenerationID string
	FinalFingerprint               Fingerprint
}

func (r PayrollRun) Finalize(generation Generation, current Fingerprint) (PayrollRun, Generation, error) {
	if r.Status == "FINALIZED" {
		return r, generation, nil
	}
	if r.ActiveGenerationID == "" || generation.ID != r.ActiveGenerationID || generation.Status != "ACTIVE" {
		return r, generation, ErrNoActiveGeneration
	}
	if !generation.Initial.Equal(current) {
		return r, generation, ErrInputChanged
	}
	r.Status = "FINALIZED"
	r.FinalFingerprint = current
	generation.Status = "FINALIZED"
	return r, generation, nil
}
