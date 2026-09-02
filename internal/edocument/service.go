package edocument

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/platform/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Service struct {
	pool     database.Querier
	provider Provider
}

func NewService(pool database.Querier, provider Provider) *Service {
	if provider == nil {
		provider = NewMockProvider()
	}
	return &Service{pool: pool, provider: provider}
}

func (s *Service) Create(ctx context.Context, session identity.Session, input CreateInput, meta identity.RequestMeta) (Document, error) {
	if !scoped(session) {
		return Document{}, identity.ErrForbidden
	}
	if err := validateCreate(input); err != nil {
		return Document{}, err
	}
	if input.ProviderKey != s.provider.Key() {
		return Document{}, fmt.Errorf("%w: %s", ErrProviderUnavailable, input.ProviderKey)
	}
	payload, err := json.Marshal(input.Payload)
	if err != nil {
		return Document{}, err
	}
	id := uuid.NewString()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Document{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	_, err = tx.Exec(ctx, `INSERT INTO edocuments(id,company_id,provider_key,status,payload,created_by_user_id)
		VALUES($1,$2,$3,$4,$5,$6)`, id, session.CurrentCompanyID, input.ProviderKey, Draft, payload, session.User.ID)
	if err != nil {
		return Document{}, err
	}
	if err = appendEvent(ctx, tx, session, id, "CREATED", Draft, "", "", meta); err != nil {
		return Document{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Document{}, err
	}
	return s.Get(ctx, session, id)
}

func (s *Service) Get(ctx context.Context, session identity.Session, id string) (Document, error) {
	if !scoped(session) {
		return Document{}, identity.ErrForbidden
	}
	return s.get(ctx, session, id, false)
}

func (s *Service) List(ctx context.Context, session identity.Session, options ListOptions) (ListResult, error) {
	if !scoped(session) {
		return ListResult{}, identity.ErrForbidden
	}
	if options.Limit <= 0 || options.Limit > 200 {
		options.Limit = 50
	}
	args := []any{session.CurrentCompanyID}
	query := `SELECT id,company_id,provider_key,status,version,created_at,updated_at,payload,provider_result
		FROM edocuments WHERE company_id=$1`
	if options.Status != "" {
		args = append(args, options.Status)
		query += fmt.Sprintf(" AND status=$%d", len(args))
	}
	if options.Type != "" {
		args = append(args, options.Type)
		query += fmt.Sprintf(" AND payload->>'document_type'=$%d", len(args))
	}
	if options.Direction != "" {
		args = append(args, options.Direction)
		query += fmt.Sprintf(" AND payload->>'direction'=$%d", len(args))
	}
	args = append(args, options.Limit)
	query += fmt.Sprintf(" ORDER BY created_at DESC,id DESC LIMIT $%d", len(args))
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return ListResult{}, err
	}
	defer rows.Close()
	result := ListResult{Items: []Document{}}
	for rows.Next() {
		item, err := scanDocument(rows)
		if err != nil {
			return ListResult{}, err
		}
		result.Items = append(result.Items, item)
	}
	return result, rows.Err()
}

func (s *Service) Queue(ctx context.Context, session identity.Session, id string, expectedVersion int64, meta identity.RequestMeta) (Document, error) {
	return s.transition(ctx, session, id, expectedVersion, Queued, "QUEUED", "", "", meta)
}

func (s *Service) Submit(ctx context.Context, session identity.Session, id string, expectedVersion int64, meta identity.RequestMeta) (Document, error) {
	if !scoped(session) {
		return Document{}, identity.ErrForbidden
	}
	current, err := s.get(ctx, session, id, true)
	if err != nil {
		return Document{}, err
	}
	if current.Version != expectedVersion {
		return Document{}, identity.ErrConflict
	}
	if current.Status != Queued && current.Status != Failed && current.Status != Rejected {
		return Document{}, fmt.Errorf("%w: %s -> SUBMITTING", ErrInvalidTransition, current.Status)
	}
	started, err := s.transition(ctx, session, id, expectedVersion, Submitting, "SUBMITTING", "", "", meta)
	if err != nil {
		return Document{}, err
	}
	result, providerErr := s.provider.Submit(ctx, started.Payload)
	if providerErr != nil {
		return s.transition(ctx, session, id, started.Version, Failed, "SUBMIT_FAILED", "", safeError(providerErr), meta)
	}
	status := result.Status
	if status != Accepted && status != Rejected && status != Submitted {
		status = Submitted
	}
	return s.transition(ctx, session, id, started.Version, status, "PROVIDER_RESULT", result.ExternalID, safeMessage(result.Message), meta)
}

func (s *Service) Cancel(ctx context.Context, session identity.Session, id string, expectedVersion int64, reason string, meta identity.RequestMeta) (Document, error) {
	if !scoped(session) {
		return Document{}, identity.ErrForbidden
	}
	current, err := s.get(ctx, session, id, true)
	if err != nil {
		return Document{}, err
	}
	if current.Version != expectedVersion {
		return Document{}, identity.ErrConflict
	}
	if !canTransition(current.Status, CancelRequested) {
		return Document{}, fmt.Errorf("%w: %s -> CANCEL_REQUESTED", ErrInvalidTransition, current.Status)
	}
	requested, err := s.transition(ctx, session, id, expectedVersion, CancelRequested, "CANCEL_REQUESTED", "", safeMessage(reason), meta)
	if err != nil {
		return Document{}, err
	}
	result, providerErr := s.provider.Cancel(ctx, requested, reason)
	if providerErr != nil {
		return s.transition(ctx, session, id, requested.Version, Failed, "CANCEL_FAILED", "", safeError(providerErr), meta)
	}
	return s.transitionWithResult(ctx, session, id, requested.Version, result.Status, "PROVIDER_CANCEL_RESULT", result.ExternalID, safeMessage(result.Message), meta)
}

func (s *Service) transition(ctx context.Context, session identity.Session, id string, expectedVersion int64, to Status, event, externalID, message string, meta identity.RequestMeta) (Document, error) {
	if !scoped(session) {
		return Document{}, identity.ErrForbidden
	}
	return s.transitionWithResult(ctx, session, id, expectedVersion, to, event, externalID, message, meta)
}

func (s *Service) transitionWithResult(ctx context.Context, session identity.Session, id string, expectedVersion int64, to Status, event, externalID, message string, meta identity.RequestMeta) (Document, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Document{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var from Status
	if err = tx.QueryRow(ctx, `SELECT status FROM edocuments WHERE company_id=$1 AND id=$2 FOR UPDATE`, session.CurrentCompanyID, id).Scan(&from); errors.Is(err, pgx.ErrNoRows) {
		return Document{}, ErrNotFound
	} else if err != nil {
		return Document{}, err
	}
	if !canTransition(from, to) {
		return Document{}, fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, from, to)
	}
	var result Document
	var payload, providerResult []byte
	err = tx.QueryRow(ctx, `UPDATE edocuments SET status=$1,provider_external_id=NULLIF($2,''),provider_result=CASE WHEN $3='' THEN provider_result ELSE jsonb_build_object('provider_key',provider_key,'external_id',$3,'status',$1,'message',$4) END,updated_at=now(),version=version+1 WHERE company_id=$5 AND id=$6 AND version=$7 RETURNING id,company_id,provider_key,status,version,created_at,updated_at,payload,provider_result`, to, externalID, externalID, message, session.CurrentCompanyID, id, expectedVersion).Scan(&result.ID, &result.CompanyID, &result.ProviderKey, &result.Status, &result.Version, &result.CreatedAt, &result.UpdatedAt, &payload, &providerResult)
	if errors.Is(err, pgx.ErrNoRows) {
		return Document{}, identity.ErrConflict
	}
	if err != nil {
		return Document{}, err
	}
	if err = json.Unmarshal(payload, &result.Payload); err != nil {
		return Document{}, err
	}
	if len(providerResult) > 0 {
		result.ProviderResult = &ProviderResult{}
		_ = json.Unmarshal(providerResult, result.ProviderResult)
	}
	if err = appendEvent(ctx, tx, session, id, event, to, externalID, message, meta); err != nil {
		return Document{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Document{}, err
	}
	return result, nil
}

func (s *Service) get(ctx context.Context, session identity.Session, id string, forUpdate bool) (Document, error) {
	query := `SELECT id,company_id,provider_key,status,version,created_at,updated_at,payload,provider_result FROM edocuments WHERE company_id=$1 AND id=$2`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	return scanDocument(s.pool.QueryRow(ctx, query, session.CurrentCompanyID, id))
}

type scanner interface{ Scan(...any) error }

func scanDocument(row scanner) (Document, error) {
	var item Document
	var payload, providerResult []byte
	if err := row.Scan(&item.ID, &item.CompanyID, &item.ProviderKey, &item.Status, &item.Version, &item.CreatedAt, &item.UpdatedAt, &payload, &providerResult); errors.Is(err, pgx.ErrNoRows) {
		return Document{}, ErrNotFound
	} else if err != nil {
		return Document{}, err
	}
	if err := json.Unmarshal(payload, &item.Payload); err != nil {
		return Document{}, err
	}
	if len(providerResult) > 0 {
		item.ProviderResult = &ProviderResult{}
		_ = json.Unmarshal(providerResult, item.ProviderResult)
	}
	return item, nil
}

func appendEvent(ctx context.Context, tx pgx.Tx, session identity.Session, id, event string, status Status, externalID, message string, meta identity.RequestMeta) error {
	_, err := tx.Exec(ctx, `INSERT INTO edocument_events(id,company_id,edocument_id,event_type,status,provider_external_id,message,actor_user_id,trace_id)
		VALUES($1,$2,$3,$4,$5,NULLIF($6,''),NULLIF($7,''),$8,$9)`, uuid.NewString(), session.CurrentCompanyID, id, event, status, externalID, message, session.User.ID, meta.TraceID)
	return err
}

func scoped(session identity.Session) bool { return strings.TrimSpace(session.CurrentCompanyID) != "" }

func safeMessage(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 500 {
		value = value[:500]
	}
	return strings.ReplaceAll(strings.ReplaceAll(value, "\n", " "), "\r", " ")
}

func safeError(err error) string {
	if err == nil {
		return ""
	}
	return safeMessage(err.Error())
}
