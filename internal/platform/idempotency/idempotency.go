// Package idempotency contains the shared command replay contract.
package idempotency

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/alpyxn/varyaone/internal/platform/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrKeyRequired       = errors.New("idempotency key is required")
	ErrPayloadConflict   = errors.New("idempotency key payload conflict")
	ErrCommandInProgress = errors.New("idempotent command is already in progress")
)

// PayloadHash returns the lower-case SHA-256 digest used by the database
// contract. The raw request bytes are hashed, so semantically equivalent JSON
// with different whitespace remains an intentionally different command.
func PayloadHash(payload []byte) string {
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func NormalizeKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" || len(key) > 255 {
		return "", ErrKeyRequired
	}
	return key, nil
}

type Result struct {
	CompanyID      string
	Key            string
	Command        string
	PayloadSHA256  string
	Status         string
	ResponseStatus int
	ResponseBody   json.RawMessage
	CompletedAt    *time.Time
}

// Store persists idempotency records. It deliberately does not own the
// business transaction: callers reserve a key in the same transaction as the
// business write and complete the row before committing.
type Store struct{ pool database.Querier }

func NewStore(pool database.Querier) *Store { return &Store{pool: pool} }

// Reserve atomically inserts an in-progress record. Existing completed rows
// are returned for replay. Existing rows with a different command or payload
// are rejected with ErrPayloadConflict.
func (s *Store) Reserve(ctx context.Context, companyID, key, command string, payload []byte, actorUserID, traceID string) (Result, bool, error) {
	key, err := NormalizeKey(key)
	if err != nil {
		return Result{}, false, err
	}
	if strings.TrimSpace(command) == "" {
		return Result{}, false, fmt.Errorf("%w: command is required", ErrKeyRequired)
	}
	company, err := uuid.Parse(companyID)
	if err != nil {
		return Result{}, false, fmt.Errorf("invalid company id: %w", err)
	}
	hash := PayloadHash(payload)
	var actor any
	if actorUserID != "" {
		actor = actorUserID
	}
	var result Result
	var response []byte
	var responseStatus *int
	var completed *time.Time
	insertResult, err := s.pool.Exec(ctx, `
		INSERT INTO command_idempotency_records(company_id,idempotency_key,command_name,payload_sha256,actor_user_id,trace_id)
		VALUES($1,$2,$3,$4,$5,$6)
		ON CONFLICT (company_id,idempotency_key) DO NOTHING`, company, key, command, hash, actor, traceID)
	if err != nil {
		return Result{}, false, err
	}
	inserted := insertResult.RowsAffected() == 1
	// Keep INSERT and SELECT as separate statements. PostgreSQL's statement
	// snapshot does not expose a data-modifying CTE's new row to a sibling
	// SELECT, which would turn every first reservation into a false conflict.
	row := s.pool.QueryRow(ctx, `
		SELECT command_name,payload_sha256,status,response_status,response_body,completed_at
		FROM command_idempotency_records
		WHERE company_id=$1 AND idempotency_key=$2
		FOR UPDATE`, company, key)
	if err = row.Scan(&result.Command, &result.PayloadSHA256, &result.Status, &responseStatus, &response, &completed); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Result{}, false, ErrCommandInProgress
		}
		return Result{}, false, err
	}
	if responseStatus != nil {
		result.ResponseStatus = *responseStatus
	}
	if result.Command != command || result.PayloadSHA256 != hash {
		return result, false, ErrPayloadConflict
	}
	result.CompanyID, result.Key, result.ResponseBody, result.CompletedAt = companyID, key, json.RawMessage(response), completed
	if !inserted && result.Status == "IN_PROGRESS" {
		return result, false, ErrCommandInProgress
	}
	return result, !inserted && result.Status == "COMPLETED", nil
}

func (s *Store) Complete(ctx context.Context, companyID, key string, status int, body any) error {
	key, err := NormalizeKey(key)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	result, err := s.pool.Exec(ctx, `UPDATE command_idempotency_records SET status='COMPLETED',response_status=$3,response_body=$4,completed_at=now() WHERE company_id=$1 AND idempotency_key=$2 AND status='IN_PROGRESS'`, companyID, key, status, encoded)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrCommandInProgress
	}
	return nil
}

// MemoryStore is useful for command handlers that need the same semantics in
// unit tests without a PostgreSQL dependency. It also provides a tiny
// reference implementation for adapters (S3/event consumers) that cannot use
// the SQL Store directly.
type MemoryStore struct {
	mu      sync.Mutex
	records map[string]Result
}

func NewMemoryStore() *MemoryStore { return &MemoryStore{records: map[string]Result{}} }

func (s *MemoryStore) Reserve(companyID, key, command string, payload []byte) (Result, bool, error) {
	key, err := NormalizeKey(key)
	if err != nil {
		return Result{}, false, err
	}
	hash := PayloadHash(payload)
	s.mu.Lock()
	defer s.mu.Unlock()
	lookup := companyID + "\x00" + key
	if current, ok := s.records[lookup]; ok {
		if current.Command != command || current.PayloadSHA256 != hash {
			return current, false, ErrPayloadConflict
		}
		if current.Status == "IN_PROGRESS" {
			return current, false, ErrCommandInProgress
		}
		return current, true, nil
	}
	result := Result{CompanyID: companyID, Key: key, Command: command, PayloadSHA256: hash, Status: "IN_PROGRESS"}
	s.records[lookup] = result
	return result, false, nil
}

func (s *MemoryStore) Complete(companyID, key string, status int, body any) error {
	key, err := NormalizeKey(key)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	lookup := companyID + "\x00" + key
	current, ok := s.records[lookup]
	if !ok || current.Status != "IN_PROGRESS" {
		return ErrCommandInProgress
	}
	now := time.Now().UTC()
	current.Status, current.ResponseStatus, current.ResponseBody, current.CompletedAt = "COMPLETED", status, encoded, &now
	s.records[lookup] = current
	return nil
}

func SamePayload(left, right []byte) bool { return bytes.Equal(left, right) }
