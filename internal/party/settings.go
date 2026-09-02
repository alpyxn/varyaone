package party

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type PaymentTerm struct {
	ID       string `json:"id"`
	Code     string `json:"code"`
	Name     string `json:"name"`
	DueDays  int    `json:"due_days"`
	IsActive bool   `json:"is_active"`
	Version  int64  `json:"version"`
}

type Group struct {
	ID       string `json:"id"`
	Code     string `json:"code"`
	Name     string `json:"name"`
	IsActive bool   `json:"is_active"`
	Version  int64  `json:"version"`
}

type CustomFieldDefinition struct {
	ID            string   `json:"id"`
	Code          string   `json:"code"`
	Name          string   `json:"name"`
	FieldType     string   `json:"field_type"`
	SelectOptions []string `json:"select_options"`
	IsRequired    bool     `json:"is_required"`
	IsActive      bool     `json:"is_active"`
}

func (s *Service) ListPaymentTerms(ctx context.Context, session identity.Session) ([]PaymentTerm, error) {
	if !authorized(session, "party.read") {
		return nil, identity.ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `SELECT id,code,name,due_days,is_active,version FROM payment_terms WHERE company_id=$1 ORDER BY code`, session.CurrentCompanyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []PaymentTerm{}
	for rows.Next() {
		var item PaymentTerm
		if err = rows.Scan(&item.ID, &item.Code, &item.Name, &item.DueDays, &item.IsActive, &item.Version); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) CreatePaymentTerm(ctx context.Context, session identity.Session, input PaymentTerm, meta identity.RequestMeta) (PaymentTerm, error) {
	if !authorized(session, "party.edit") {
		return PaymentTerm{}, identity.ErrForbidden
	}
	input.Code = strings.ToUpper(strings.TrimSpace(input.Code))
	input.Name = strings.TrimSpace(input.Name)
	if input.Code == "" || input.Name == "" || input.DueDays < 0 {
		return PaymentTerm{}, fmt.Errorf("%w: vade kodu, adı ve negatif olmayan gün sayısı gereklidir", identity.ErrValidation)
	}
	input.ID = uuid.NewString()
	input.IsActive = true
	input.Version = 1
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PaymentTerm{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err = tx.Exec(ctx, `INSERT INTO payment_terms(id,company_id,code,name,due_days) VALUES($1,$2,$3,$4,$5)`, input.ID, session.CurrentCompanyID, input.Code, input.Name, input.DueDays); err != nil {
		return PaymentTerm{}, mapConstraint(err)
	}
	if err = writeSettingsEvent(ctx, tx, session, "PAYMENT_TERM_CREATED", "party.payment_term.created", "payment_term", input.ID, meta); err != nil {
		return PaymentTerm{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return PaymentTerm{}, err
	}
	return input, nil
}

func (s *Service) ListGroups(ctx context.Context, session identity.Session) ([]Group, error) {
	if !authorized(session, "party.read") {
		return nil, identity.ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id,code,name,is_active,version
		FROM party_groups
		WHERE company_id=$1
		ORDER BY CASE code
			WHEN 'PERAKENDE' THEN 1
			WHEN 'TOPTAN' THEN 2
			WHEN 'BAYI' THEN 3
			WHEN 'HIZMET_TED' THEN 4
			WHEN 'MALZEME_TED' THEN 5
			ELSE 100
		END, code`, session.CurrentCompanyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Group{}
	for rows.Next() {
		var item Group
		if err = rows.Scan(&item.ID, &item.Code, &item.Name, &item.IsActive, &item.Version); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
func (s *Service) CreateGroup(ctx context.Context, session identity.Session, input Group, meta identity.RequestMeta) (Group, error) {
	if !authorized(session, "party.edit") {
		return Group{}, identity.ErrForbidden
	}
	input.Code = strings.ToUpper(strings.TrimSpace(input.Code))
	input.Name = strings.TrimSpace(input.Name)
	if input.Code == "" || input.Name == "" {
		return Group{}, fmt.Errorf("%w: grup kodu ve adı gereklidir", identity.ErrValidation)
	}
	input.ID = uuid.NewString()
	input.IsActive = true
	input.Version = 1
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Group{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err = tx.Exec(ctx, `INSERT INTO party_groups(id,company_id,code,name)VALUES($1,$2,$3,$4)`, input.ID, session.CurrentCompanyID, input.Code, input.Name); err != nil {
		return Group{}, mapConstraint(err)
	}
	if err = writeSettingsEvent(ctx, tx, session, "PARTY_GROUP_CREATED", "party.group.created", "party_group", input.ID, meta); err != nil {
		return Group{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Group{}, err
	}
	return input, nil
}

func (s *Service) UpdateGroup(ctx context.Context, session identity.Session, id string, expectedVersion int64, input Group, meta identity.RequestMeta) (Group, error) {
	if !authorized(session, "party.edit") {
		return Group{}, identity.ErrForbidden
	}
	input.Code = strings.ToUpper(strings.TrimSpace(input.Code))
	input.Name = strings.TrimSpace(input.Name)
	if id == "" || expectedVersion < 1 || input.Code == "" || input.Name == "" {
		return Group{}, fmt.Errorf("%w: geçerli If-Match sürümü, grup kodu ve adı gereklidir", identity.ErrValidation)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Group{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var result Group
	err = tx.QueryRow(ctx, `
		UPDATE party_groups
		SET code=$1,name=$2,updated_at=now(),version=version+1
		WHERE company_id=$3 AND id=$4 AND version=$5
		RETURNING id,code,name,is_active,version`, input.Code, input.Name, session.CurrentCompanyID, id, expectedVersion).
		Scan(&result.ID, &result.Code, &result.Name, &result.IsActive, &result.Version)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Group{}, identity.ErrConflict
		}
		return Group{}, mapConstraint(err)
	}
	if err = writeSettingsEvent(ctx, tx, session, "PARTY_GROUP_UPDATED", "party.group.updated", "party_group", id, meta); err != nil {
		return Group{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Group{}, err
	}
	return result, nil
}

func (s *Service) DeactivateGroup(ctx context.Context, session identity.Session, id string, expectedVersion int64, meta identity.RequestMeta) (Group, error) {
	if !authorized(session, "party.edit") {
		return Group{}, identity.ErrForbidden
	}
	if id == "" || expectedVersion < 1 {
		return Group{}, fmt.Errorf("%w: geçerli If-Match sürümü gereklidir", identity.ErrValidation)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Group{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var result Group
	err = tx.QueryRow(ctx, `
		UPDATE party_groups
		SET is_active=false,updated_at=now(),version=version+1
		WHERE company_id=$1 AND id=$2 AND version=$3 AND is_active
		RETURNING id,code,name,is_active,version`, session.CurrentCompanyID, id, expectedVersion).
		Scan(&result.ID, &result.Code, &result.Name, &result.IsActive, &result.Version)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Group{}, identity.ErrConflict
		}
		return Group{}, err
	}
	if err = writeSettingsEvent(ctx, tx, session, "PARTY_GROUP_DEACTIVATED", "party.group.deactivated", "party_group", id, meta); err != nil {
		return Group{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Group{}, err
	}
	return result, nil
}

func (s *Service) ActivateGroup(ctx context.Context, session identity.Session, id string, expectedVersion int64, meta identity.RequestMeta) (Group, error) {
	if !authorized(session, "party.edit") {
		return Group{}, identity.ErrForbidden
	}
	if id == "" || expectedVersion < 1 {
		return Group{}, fmt.Errorf("%w: geçerli If-Match sürümü gereklidir", identity.ErrValidation)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Group{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var result Group
	err = tx.QueryRow(ctx, `
		UPDATE party_groups
		SET is_active=true,updated_at=now(),version=version+1
		WHERE company_id=$1 AND id=$2 AND version=$3 AND NOT is_active
		RETURNING id,code,name,is_active,version`, session.CurrentCompanyID, id, expectedVersion).
		Scan(&result.ID, &result.Code, &result.Name, &result.IsActive, &result.Version)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Group{}, identity.ErrConflict
		}
		return Group{}, err
	}
	if err = writeSettingsEvent(ctx, tx, session, "PARTY_GROUP_ACTIVATED", "party.group.activated", "party_group", id, meta); err != nil {
		return Group{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Group{}, err
	}
	return result, nil
}

func (s *Service) ListCustomFields(ctx context.Context, session identity.Session) ([]CustomFieldDefinition, error) {
	if !authorized(session, "party.read") {
		return nil, identity.ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `SELECT id,code,name,field_type,select_options,is_required,is_active FROM party_custom_field_definitions WHERE company_id=$1 ORDER BY code`, session.CurrentCompanyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []CustomFieldDefinition{}
	for rows.Next() {
		var item CustomFieldDefinition
		var options []byte
		if err = rows.Scan(&item.ID, &item.Code, &item.Name, &item.FieldType, &options, &item.IsRequired, &item.IsActive); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(options, &item.SelectOptions)
		items = append(items, item)
	}
	return items, rows.Err()
}
func (s *Service) CreateCustomField(ctx context.Context, session identity.Session, input CustomFieldDefinition, meta identity.RequestMeta) (CustomFieldDefinition, error) {
	if !authorized(session, "party.edit") {
		return CustomFieldDefinition{}, identity.ErrForbidden
	}
	input.Code = strings.ToLower(strings.TrimSpace(input.Code))
	input.Name = strings.TrimSpace(input.Name)
	input.FieldType = strings.ToUpper(strings.TrimSpace(input.FieldType))
	allowed := map[string]bool{"TEXT": true, "NUMBER": true, "DATE": true, "BOOLEAN": true, "SELECT": true}
	if input.Code == "" || input.Name == "" || !allowed[input.FieldType] || (input.FieldType == "SELECT" && len(input.SelectOptions) == 0) {
		return CustomFieldDefinition{}, fmt.Errorf("%w: özel alan kodu, adı, türü ve seçim seçenekleri geçerli olmalıdır", identity.ErrValidation)
	}
	input.SelectOptions = unique(input.SelectOptions)
	input.ID = uuid.NewString()
	input.IsActive = true
	options, _ := json.Marshal(input.SelectOptions)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CustomFieldDefinition{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err = tx.Exec(ctx, `INSERT INTO party_custom_field_definitions(id,company_id,code,name,field_type,select_options,is_required)VALUES($1,$2,$3,$4,$5,$6,$7)`, input.ID, session.CurrentCompanyID, input.Code, input.Name, input.FieldType, options, input.IsRequired); err != nil {
		return CustomFieldDefinition{}, mapConstraint(err)
	}
	if err = writeSettingsEvent(ctx, tx, session, "PARTY_CUSTOM_FIELD_CREATED", "party.custom_field.created", "party_custom_field", input.ID, meta); err != nil {
		return CustomFieldDefinition{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return CustomFieldDefinition{}, err
	}
	return input, nil
}

func writeSettingsEvent(ctx context.Context, tx pgx.Tx, session identity.Session, eventType, outboxType, entityType, entityID string, meta identity.RequestMeta) error {
	details := []byte(`{"version":1}`)
	if _, err := tx.Exec(ctx, `INSERT INTO security_audit_events(id,company_id,actor_user_id,event_type,entity_type,entity_id,details,trace_id,source_ip,user_agent)VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, uuid.NewString(), session.CurrentCompanyID, session.User.ID, eventType, entityType, entityID, details, meta.TraceID, meta.IP, truncate(meta.UserAgent, 512)); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{"id": entityID})
	_, err := tx.Exec(ctx, `INSERT INTO outbox_events(event_id,type,schema_version,company_id,trace_id,payload)VALUES($1,$2,1,$3,$4,$5)`, uuid.NewString(), outboxType, session.CurrentCompanyID, meta.TraceID, payload)
	return err
}
