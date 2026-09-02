// Package leave owns leave types. Leave itself is marked directly on a
// puantaj (timesheet) day by picking one of these types — there is no
// separate request/approval flow (see internal/hr/timesheet).
package leave

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/platform/codeseq"
	"github.com/alpyxn/varyaone/internal/platform/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var ErrTypeNotFound = errors.New("LEAVE_TYPE_NOT_FOUND")

type Service struct{ pool database.Querier }

func NewService(pool database.Querier) *Service { return &Service{pool: pool} }

type LeaveType struct {
	ID               string `json:"id"`
	Code             string `json:"code"`
	Name             string `json:"name"`
	PayrollTreatment string `json:"payroll_treatment"`
	IsActive         bool   `json:"is_active"`
	Version          int64  `json:"version"`
}

type LeaveTypeInput struct {
	Code             string `json:"code"`
	Name             string `json:"name"`
	PayrollTreatment string `json:"payroll_treatment"`
	IsActive         *bool  `json:"is_active,omitempty"`
}

var validTreatment = map[string]bool{"PAID": true, "UNPAID": true, "SICK_REQUIRES_REVIEW": true}

func (s *Service) ListTypes(ctx context.Context, session identity.Session) ([]LeaveType, error) {
	if !session.HasPermission("hr.leave.read") {
		return nil, identity.ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `SELECT id::text,code,name,payroll_treatment,is_active,version FROM leave_types WHERE company_id=$1 ORDER BY code`, session.CurrentCompanyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []LeaveType{}
	for rows.Next() {
		var t LeaveType
		if err := rows.Scan(&t.ID, &t.Code, &t.Name, &t.PayrollTreatment, &t.IsActive, &t.Version); err != nil {
			return nil, err
		}
		items = append(items, t)
	}
	return items, rows.Err()
}

func (s *Service) CreateType(ctx context.Context, session identity.Session, input LeaveTypeInput, meta identity.RequestMeta) (LeaveType, error) {
	if !session.HasPermission("hr.leave.edit") {
		return LeaveType{}, identity.ErrForbidden
	}
	input.Code = strings.TrimSpace(input.Code)
	input.Name = strings.TrimSpace(input.Name)
	input.PayrollTreatment = strings.ToUpper(strings.TrimSpace(input.PayrollTreatment))
	if input.Name == "" || !validTreatment[input.PayrollTreatment] {
		return LeaveType{}, fmt.Errorf("%w: izin türü alanları geçersiz", identity.ErrValidation)
	}
	id := uuid.NewString()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return LeaveType{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if input.Code == "" {
		if input.Code, err = codeseq.Next(ctx, tx, session.CurrentCompanyID, "leave_types", "code", "IZIN"); err != nil {
			return LeaveType{}, err
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO leave_types(id,company_id,code,name,payroll_treatment) VALUES($1,$2,$3,$4,$5)`,
		id, session.CurrentCompanyID, input.Code, input.Name, input.PayrollTreatment); err != nil {
		return LeaveType{}, mapConstraint(err)
	}
	if err = writeEvent(ctx, tx, session, meta, "LEAVE_TYPE_CREATED", "hr.leave_type.created", id); err != nil {
		return LeaveType{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return LeaveType{}, err
	}
	return s.getType(ctx, session, id)
}

func (s *Service) UpdateType(ctx context.Context, session identity.Session, id string, version int64, input LeaveTypeInput, meta identity.RequestMeta) (LeaveType, error) {
	if !session.HasPermission("hr.leave.edit") {
		return LeaveType{}, identity.ErrForbidden
	}
	input.Name = strings.TrimSpace(input.Name)
	input.PayrollTreatment = strings.ToUpper(strings.TrimSpace(input.PayrollTreatment))
	if input.Name == "" || !validTreatment[input.PayrollTreatment] {
		return LeaveType{}, fmt.Errorf("%w: izin türü alanları geçersiz", identity.ErrValidation)
	}
	active := true
	if input.IsActive != nil {
		active = *input.IsActive
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return LeaveType{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	tag, err := tx.Exec(ctx, `UPDATE leave_types SET name=$3,payroll_treatment=$4,is_active=$5,updated_at=now(),version=version+1
 WHERE company_id=$1 AND id=NULLIF($2,'')::uuid AND version=$6`,
		session.CurrentCompanyID, id, input.Name, input.PayrollTreatment, active, version)
	if err != nil {
		return LeaveType{}, err
	}
	if tag.RowsAffected() == 0 {
		return LeaveType{}, ErrTypeNotFound
	}
	if err = writeEvent(ctx, tx, session, meta, "LEAVE_TYPE_UPDATED", "hr.leave_type.updated", id); err != nil {
		return LeaveType{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return LeaveType{}, err
	}
	return s.getType(ctx, session, id)
}

func (s *Service) getType(ctx context.Context, session identity.Session, id string) (LeaveType, error) {
	var t LeaveType
	err := s.pool.QueryRow(ctx, `SELECT id::text,code,name,payroll_treatment,is_active,version FROM leave_types WHERE company_id=$1 AND id=NULLIF($2,'')::uuid`,
		session.CurrentCompanyID, id).Scan(&t.ID, &t.Code, &t.Name, &t.PayrollTreatment, &t.IsActive, &t.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return LeaveType{}, ErrTypeNotFound
	}
	return t, err
}

func mapConstraint(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	switch {
	case pgErr.Code == "23505" && strings.Contains(pgErr.ConstraintName, "code"):
		return fmt.Errorf("%w: izin türü kodu zaten kullanımda", identity.ErrValidation)
	}
	return err
}

func writeEvent(ctx context.Context, tx pgx.Tx, session identity.Session, meta identity.RequestMeta, auditType, outboxType, entityID string) error {
	details, _ := json.Marshal(map[string]any{"entity_id": entityID})
	if _, err := tx.Exec(ctx, `INSERT INTO security_audit_events(id,company_id,actor_user_id,event_type,entity_type,entity_id,details,trace_id,source_ip,user_agent)
 VALUES($1,$2,$3,$4,'employee_leave',$5,$6,$7,$8,$9)`,
		uuid.NewString(), session.CurrentCompanyID, session.User.ID, auditType, entityID, details, meta.TraceID, meta.IP, meta.UserAgent); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]string{"entity_id": entityID})
	_, err := tx.Exec(ctx, `INSERT INTO outbox_events(event_id,type,schema_version,company_id,trace_id,payload) VALUES($1,$2,1,$3,$4,$5)`,
		uuid.NewString(), outboxType, session.CurrentCompanyID, meta.TraceID, payload)
	return err
}
