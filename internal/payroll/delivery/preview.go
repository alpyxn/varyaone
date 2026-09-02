// Package delivery validates privacy-preserving payslip email batches.
package delivery

import (
	"net/mail"
	"strings"
)

type Candidate struct {
	EmployeeID, EmployeePayrollID, PayslipID, PayrollEmail string
	EmployeeName, EmployeeCode, Net                        string
	AlreadySent                                            bool
}
type Preview struct{ Ready, Missing, Invalid, Duplicate, MissingPayslip, AlreadySent []string }

func BuildPreview(candidates []Candidate) Preview {
	result := Preview{}
	counts := map[string]int{}
	for _, candidate := range candidates {
		email := normalize(candidate.PayrollEmail)
		if email != "" {
			counts[email]++
		}
	}
	for _, candidate := range candidates {
		email := normalize(candidate.PayrollEmail)
		switch {
		case candidate.AlreadySent:
			result.AlreadySent = append(result.AlreadySent, candidate.EmployeeID)
		case email == "":
			result.Missing = append(result.Missing, candidate.EmployeeID)
		case !valid(email):
			result.Invalid = append(result.Invalid, candidate.EmployeeID)
		case counts[email] > 1:
			result.Duplicate = append(result.Duplicate, candidate.EmployeeID)
		case candidate.PayslipID == "":
			result.MissingPayslip = append(result.MissingPayslip, candidate.EmployeeID)
		default:
			result.Ready = append(result.Ready, candidate.EmployeeID)
		}
	}
	return result
}

type Outcome string

const (
	Retry            Outcome = "RETRY"
	PermanentFailure Outcome = "PERMANENT_FAILURE"
	Sent             Outcome = "SENT"
	StatusUnknown    Outcome = "DELIVERY_STATUS_UNKNOWN"
)

func ClassifySMTP(code int, connected, dataSubmitted, acceptanceKnown bool) Outcome {
	if dataSubmitted && !acceptanceKnown {
		return StatusUnknown
	}
	if code == 250 && acceptanceKnown {
		return Sent
	}
	if !connected || code >= 400 && code < 500 {
		return Retry
	}
	return PermanentFailure
}
func normalize(value string) string { return strings.ToLower(strings.TrimSpace(value)) }
func valid(value string) bool {
	address, err := mail.ParseAddress(value)
	return err == nil && address.Address == value
}
