package dataexchange

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ProcessRequest contains one normalized import input and its authorized job
// context. DryRun requests validate and summarize but never call Commit.
type ProcessRequest struct {
	Job     ImportJob
	Table   Table
	Mapping MappingOptions
	DryRun  bool
}

// Engine coordinates mapping, generic validation, adapter validation, preview,
// dry-run, and one-batch commit orchestration.
type Engine struct {
	adapter    Adapter
	fields     []FieldSpec
	validators []Validator
}

// NewEngine creates an import engine. The adapter is required even for
// previews because target-domain validation is part of the preview contract.
func NewEngine(adapter Adapter, fields []FieldSpec, validators ...Validator) (*Engine, error) {
	if adapter == nil {
		return nil, fmt.Errorf("%w: aktarım bağlayıcısı gereklidir", ErrInvalidJob)
	}
	if len(fields) == 0 {
		return nil, fmt.Errorf("%w: en az bir aktarım alanı gereklidir", ErrInvalidMapping)
	}
	seen := make(map[string]struct{}, len(fields))
	ownedFields := make([]FieldSpec, len(fields))
	for index, field := range fields {
		field.Name = strings.TrimSpace(field.Name)
		key := normalizeHeader(field.Name)
		if key == "" {
			return nil, fmt.Errorf("%w: %d numaralı aktarım alanı boş", ErrInvalidMapping, index+1)
		}
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("%w: %s aktarım alanı birden fazla tanımlanmış", ErrInvalidMapping, field.Name)
		}
		seen[key] = struct{}{}
		field.Aliases = append([]string(nil), field.Aliases...)
		ownedFields[index] = field
	}
	ownedValidators := make([]Validator, 0, len(validators))
	for _, validator := range validators {
		if validator != nil {
			ownedValidators = append(ownedValidators, validator)
		}
	}
	return &Engine{adapter: adapter, fields: ownedFields, validators: ownedValidators}, nil
}

// Preview maps and validates without attempting a commit.
func (e *Engine) Preview(ctx context.Context, request ProcessRequest) (Preview, error) {
	prepared, err := e.prepare(ctx, request)
	if err != nil {
		return Preview{}, err
	}
	return prepared.preview, nil
}

// Run executes the import. DryRun returns a completed preview and does not call
// the adapter commit hook. Non-dry-run validation failures return a result with
// no Go error so callers can present row-level correction feedback.
func (e *Engine) Run(ctx context.Context, request ProcessRequest) (RunResult, error) {
	prepared, err := e.prepare(ctx, request)
	if err != nil {
		return RunResult{}, err
	}

	if !prepared.preview.CanCommit {
		job, transitionErr := prepared.job.Transition(JobStateValidationFailed)
		if transitionErr != nil {
			return RunResult{}, transitionErr
		}
		prepared.preview.Job = job
		return RunResult{Job: job, Preview: prepared.preview}, nil
	}

	if request.DryRun {
		job, transitionErr := prepared.job.Transition(JobStateDryRunCompleted)
		if transitionErr != nil {
			return RunResult{}, transitionErr
		}
		prepared.preview.Job = job
		prepared.preview.DryRun = true
		return RunResult{Job: job, Preview: prepared.preview}, nil
	}

	job, transitionErr := prepared.job.Transition(JobStateReadyToCommit)
	if transitionErr != nil {
		return RunResult{}, transitionErr
	}
	if err := contextError(ctx); err != nil {
		return RunResult{}, err
	}
	if err := e.adapter.Commit(ctx, CommitInput{
		Job:     job,
		Mapping: prepared.mapping,
		Rows:    cloneMappedRows(prepared.rows),
	}); err != nil {
		failedJob, failedTransitionErr := job.Transition(JobStateFailed)
		if failedTransitionErr != nil {
			return RunResult{}, failedTransitionErr
		}
		prepared.preview.Job = failedJob
		return RunResult{Job: failedJob, Preview: prepared.preview}, &CommitError{Err: err}
	}

	job, transitionErr = job.Transition(JobStateCommitted)
	if transitionErr != nil {
		return RunResult{}, transitionErr
	}
	prepared.preview.Job = job
	return RunResult{Job: job, Preview: prepared.preview, Committed: true}, nil
}

type preparedRun struct {
	job     ImportJob
	mapping Mapping
	rows    []MappedRow
	preview Preview
}

func (e *Engine) prepare(ctx context.Context, request ProcessRequest) (preparedRun, error) {
	if err := contextError(ctx); err != nil {
		return preparedRun{}, err
	}
	if request.Job.State != JobStatePending {
		return preparedRun{}, fmt.Errorf("%w: aktarım işi beklemede olmalıdır", ErrInvalidJob)
	}
	if strings.TrimSpace(request.Job.ID) == "" || strings.TrimSpace(request.Job.CompanyID) == "" {
		return preparedRun{}, fmt.Errorf("%w: aktarım ve şirket kimliği gereklidir", ErrInvalidJob)
	}
	if err := request.Table.validate(); err != nil {
		return preparedRun{}, err
	}

	job, err := request.Job.Transition(JobStateMapping)
	if err != nil {
		return preparedRun{}, err
	}
	mappingResult, err := ResolveMapping(request.Table, e.fields, request.Mapping)
	if err != nil {
		return preparedRun{}, err
	}

	rows := make([]MappedRow, len(request.Table.Rows))
	for index, sourceRow := range request.Table.Rows {
		sourceRow.Number = rowNumber(sourceRow, index)
		rows[index], err = mappingResult.Mapping.MapRow(sourceRow)
		if err != nil {
			return preparedRun{}, err
		}
	}
	if err := contextError(ctx); err != nil {
		return preparedRun{}, err
	}

	issues := copyIssues(mappingResult.Issues)
	if len(rows) == 0 {
		issues = append(issues, Issue{Code: "empty_input", Severity: SeverityError, Message: "en az bir veri satırı gereklidir"})
	}
	for _, validator := range e.validators {
		issues = append(issues, validator.Validate(cloneMappedRows(rows))...)
	}
	validationResult, err := e.adapter.Validate(ctx, ValidationInput{
		Job:     job,
		Mapping: mappingResult.Mapping,
		Rows:    cloneMappedRows(rows),
	})
	if err != nil {
		return preparedRun{}, &AdapterValidationError{Err: err}
	}
	issues = append(issues, validationResult.Issues...)
	if err := contextError(ctx); err != nil {
		return preparedRun{}, err
	}

	preview, err := buildPreview(job, mappingResult.Mapping, rows, issues)
	if err != nil {
		return preparedRun{}, err
	}
	job, err = job.Transition(JobStatePreviewed)
	if err != nil {
		return preparedRun{}, err
	}
	preview.Job = job
	return preparedRun{job: job, mapping: mappingResult.Mapping, rows: rows, preview: preview}, nil
}

func buildPreview(job ImportJob, mapping Mapping, rows []MappedRow, issues []Issue) (Preview, error) {
	byRow := make(map[int][]Issue, len(rows))
	knownRows := make(map[int]struct{}, len(rows))
	for _, row := range rows {
		knownRows[row.RowNumber] = struct{}{}
	}
	var globalIssues []Issue
	for _, issue := range issues {
		if issue.RowNumber == 0 {
			globalIssues = append(globalIssues, issue)
			continue
		}
		if _, known := knownRows[issue.RowNumber]; !known {
			return Preview{}, fmt.Errorf("%w: doğrulama kaydı bilinmeyen bir satıra başvuruyor", ErrInvalidJob)
		}
		byRow[issue.RowNumber] = append(byRow[issue.RowNumber], issue)
	}

	results := make([]RowResult, len(rows))
	validRows := 0
	invalidRows := 0
	warningRows := 0
	for index, row := range rows {
		rowIssues := copyIssues(byRow[row.RowNumber])
		status := RowStatusValid
		for _, issue := range rowIssues {
			if issue.IsError() {
				status = RowStatusInvalid
				break
			}
		}
		if status == RowStatusInvalid {
			invalidRows++
		} else {
			validRows++
			for _, issue := range rowIssues {
				if issue.IsWarning() {
					warningRows++
					break
				}
			}
		}
		results[index] = RowResult{
			RowNumber: row.RowNumber,
			Status:    status,
			Values:    cloneValues(row.Values),
			Issues:    rowIssues,
		}
	}

	canCommit := !hasError(globalIssues) && invalidRows == 0 && len(rows) > 0
	return Preview{
		Job:         job,
		Mapping:     mapping,
		Rows:        results,
		Issues:      globalIssues,
		TotalRows:   len(rows),
		ValidRows:   validRows,
		InvalidRows: invalidRows,
		WarningRows: warningRows,
		CanCommit:   canCommit,
	}, nil
}

// CommitError preserves the adapter failure for errors.Is/errors.As while
// keeping source data out of the error text produced by this core.
type CommitError struct {
	Err error
}

func (e *CommitError) Error() string {
	return ErrCommitFailed.Error()
}

func (e *CommitError) Unwrap() error {
	if e == nil {
		return ErrCommitFailed
	}
	return e.Err
}

func (e *CommitError) Is(target error) bool {
	if target == ErrCommitFailed {
		return true
	}
	return e != nil && errors.Is(e.Err, target)
}

// AdapterValidationError identifies a fatal adapter validation failure without
// copying arbitrary adapter text into the package's public error message.
type AdapterValidationError struct {
	Err error
}

func (e *AdapterValidationError) Error() string {
	return "dataexchange: adapter validation failed"
}

func (e *AdapterValidationError) Unwrap() error {
	if e == nil {
		return ErrInvalidJob
	}
	return e.Err
}

func (e *AdapterValidationError) Is(target error) bool {
	if target == ErrInvalidJob {
		return true
	}
	return e != nil && errors.Is(e.Err, target)
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func cloneValues(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	clone := make(map[string]string, len(values))
	for key, value := range values {
		clone[key] = value
	}
	return clone
}

func cloneMappedRows(rows []MappedRow) []MappedRow {
	if len(rows) == 0 {
		return nil
	}
	clone := make([]MappedRow, len(rows))
	for index, row := range rows {
		clone[index] = MappedRow{RowNumber: row.RowNumber, Values: cloneValues(row.Values)}
	}
	return clone
}
