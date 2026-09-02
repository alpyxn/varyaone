// Package dataexchange provides dependency-free primitives for processing
// normalized tabular imports. Adapters translate file formats and target-domain
// commands at the package boundary.
package dataexchange

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrInvalidJob     = errors.New("dataexchange: invalid import job")
	ErrInvalidTable   = errors.New("dataexchange: invalid table")
	ErrInvalidMapping = errors.New("dataexchange: invalid mapping")
	ErrCommitFailed   = errors.New("dataexchange: commit failed")
)

// JobState is the lifecycle state of an import job as observed by this core.
type JobState string

const (
	JobStatePending          JobState = "PENDING"
	JobStateMapping          JobState = "MAPPING"
	JobStatePreviewed        JobState = "PREVIEWED"
	JobStateDryRunCompleted  JobState = "DRY_RUN_COMPLETED"
	JobStateReadyToCommit    JobState = "READY_TO_COMMIT"
	JobStateValidationFailed JobState = "VALIDATION_FAILED"
	JobStateCommitted        JobState = "COMMITTED"
	JobStateFailed           JobState = "FAILED"
)

// ImportJob identifies one company-scoped import. The core does not authorize
// the company; callers must provide an already-authorized company context.
type ImportJob struct {
	ID        string   `json:"id"`
	CompanyID string   `json:"company_id"`
	State     JobState `json:"state"`
}

// NewImportJob creates a job in the pending state.
func NewImportJob(id, companyID string) (ImportJob, error) {
	id = strings.TrimSpace(id)
	companyID = strings.TrimSpace(companyID)
	if id == "" || companyID == "" {
		return ImportJob{}, fmt.Errorf("%w: aktarım ve şirket kimliği gereklidir", ErrInvalidJob)
	}
	return ImportJob{ID: id, CompanyID: companyID, State: JobStatePending}, nil
}

// Transition returns a copy of the job in next state. Job transitions are
// deliberately explicit so callers cannot accidentally skip validation or
// commit boundaries.
func (j ImportJob) Transition(next JobState) (ImportJob, error) {
	if strings.TrimSpace(j.ID) == "" || strings.TrimSpace(j.CompanyID) == "" {
		return ImportJob{}, fmt.Errorf("%w: aktarım ve şirket kimliği gereklidir", ErrInvalidJob)
	}
	if !validJobState(j.State) || !validJobState(next) {
		return ImportJob{}, fmt.Errorf("%w: bilinmeyen durum geçişi", ErrInvalidJob)
	}
	if !allowedTransition(j.State, next) {
		return ImportJob{}, fmt.Errorf("%w: %s durumundan %s durumuna geçilemez", ErrInvalidJob, j.State, next)
	}
	j.State = next
	return j, nil
}

func validJobState(state JobState) bool {
	switch state {
	case JobStatePending, JobStateMapping, JobStatePreviewed,
		JobStateDryRunCompleted, JobStateReadyToCommit,
		JobStateValidationFailed, JobStateCommitted, JobStateFailed:
		return true
	default:
		return false
	}
}

func allowedTransition(from, to JobState) bool {
	switch from {
	case JobStatePending:
		return to == JobStateMapping
	case JobStateMapping:
		return to == JobStatePreviewed || to == JobStateFailed
	case JobStatePreviewed:
		return to == JobStateDryRunCompleted || to == JobStateReadyToCommit ||
			to == JobStateValidationFailed || to == JobStateFailed
	case JobStateReadyToCommit:
		return to == JobStateCommitted || to == JobStateFailed
	default:
		return false
	}
}

// Severity describes the effect of an issue on a row or preview.
type Severity string

const (
	SeverityError   Severity = "ERROR"
	SeverityWarning Severity = "WARNING"
)

// Issue is a safe, structured validation or mapping message. It intentionally
// contains no source value, file contents, credentials, or authorization data.
type Issue struct {
	RowNumber int      `json:"row_number,omitempty"`
	Field     string   `json:"field,omitempty"`
	Code      string   `json:"code"`
	Severity  Severity `json:"severity"`
	Message   string   `json:"message"`
}

func (i Issue) IsError() bool {
	return i.Severity == SeverityError
}

func (i Issue) IsWarning() bool {
	return i.Severity == SeverityWarning
}

// RowStatus is the outcome of all validators for one source row.
type RowStatus string

const (
	RowStatusValid   RowStatus = "VALID"
	RowStatusInvalid RowStatus = "INVALID"
)

// RowResult is the row-level result shown by preview and dry-run operations.
type RowResult struct {
	RowNumber int               `json:"row_number"`
	Status    RowStatus         `json:"status"`
	Values    map[string]string `json:"values"`
	Issues    []Issue           `json:"issues,omitempty"`
}

func (r RowResult) HasWarnings() bool {
	for _, issue := range r.Issues {
		if issue.IsWarning() {
			return true
		}
	}
	return false
}

// Preview is the complete non-mutating result of mapping and validation.
type Preview struct {
	Job         ImportJob   `json:"job"`
	Mapping     Mapping     `json:"mapping"`
	Rows        []RowResult `json:"rows"`
	Issues      []Issue     `json:"issues,omitempty"`
	TotalRows   int         `json:"total_rows"`
	ValidRows   int         `json:"valid_rows"`
	InvalidRows int         `json:"invalid_rows"`
	WarningRows int         `json:"warning_rows"`
	CanCommit   bool        `json:"can_commit"`
	DryRun      bool        `json:"dry_run,omitempty"`
}

// RunResult is returned by Run. A validation failure is represented in the
// result with no commit and a validation-failed job state; it is not returned
// as a Go error because callers need the row-level issues.
type RunResult struct {
	Job       ImportJob
	Preview   Preview
	Committed bool
}

// ValidationInput is passed to an adapter's read-only validation hook.
type ValidationInput struct {
	Job     ImportJob
	Mapping Mapping
	Rows    []MappedRow
}

// ValidationResult contains adapter-specific row or job issues. RowNumber zero
// denotes a job-level issue.
type ValidationResult struct {
	Issues []Issue
}

// CommitInput is passed once, with every valid row, to the adapter. The
// adapter must implement this call as one atomic transaction in its own
// storage boundary; the core never commits one row at a time.
type CommitInput struct {
	Job     ImportJob
	Mapping Mapping
	Rows    []MappedRow
}

// Adapter bridges this format-independent core to a target domain. Validate
// must be read-only. Commit receives the complete valid batch exactly once and
// must leave the target unchanged when it returns an error.
type Adapter interface {
	Validate(ctx context.Context, input ValidationInput) (ValidationResult, error)
	Commit(ctx context.Context, input CommitInput) error
}

// Validator is a dependency-free generic validation hook. Validators may
// inspect all rows, which allows duplicate detection without adapter knowledge.
type Validator interface {
	Validate(rows []MappedRow) []Issue
}

// ValidatorFunc adapts a function into a Validator.
type ValidatorFunc func(rows []MappedRow) []Issue

func (f ValidatorFunc) Validate(rows []MappedRow) []Issue {
	if f == nil {
		return nil
	}
	return f(rows)
}

func copyIssues(issues []Issue) []Issue {
	if len(issues) == 0 {
		return nil
	}
	return append([]Issue(nil), issues...)
}
