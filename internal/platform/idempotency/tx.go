package idempotency

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type Tx interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

// TxReservation is the transaction-local result of reserving a command key.
// A completed reservation is a replay and must not execute business writes.
type TxReservation struct {
	Inserted       bool
	Completed      bool
	ResponseStatus int
	ResponseBody   json.RawMessage
}

// ReserveTx makes the idempotency record part of the caller's business
// transaction. This is the primitive used by create commands whose response
// is only known after the aggregate has been written.
func ReserveTx(ctx context.Context, tx interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}, companyID, key, command string, payload []byte, actorUserID, traceID string) (TxReservation, error) {
	key, err := NormalizeKey(key)
	if err != nil {
		return TxReservation{}, err
	}
	if command == "" {
		return TxReservation{}, fmt.Errorf("%w: command is required", ErrKeyRequired)
	}
	hash := PayloadHash(payload)
	var storedCommand, storedHash, status string
	var responseStatus *int
	var response []byte
	insertResult, err := tx.Exec(ctx, `
		INSERT INTO command_idempotency_records(company_id,idempotency_key,command_name,payload_sha256,actor_user_id,trace_id)
		VALUES($1,$2,$3,$4,NULLIF($5,'')::uuid,NULLIF($6,''))
		ON CONFLICT (company_id,idempotency_key) DO NOTHING`, companyID, key, command, hash, actorUserID, traceID)
	if err != nil {
		return TxReservation{}, err
	}
	inserted := insertResult.RowsAffected() == 1
	// Read the winning row in a separate statement. A data-modifying CTE's
	// sibling SELECT uses the statement snapshot and can miss the row inserted
	// by the CTE, incorrectly reporting the first request as in progress.
	row := tx.QueryRow(ctx, `
		SELECT command_name,payload_sha256,status,response_status,response_body
		FROM command_idempotency_records
		WHERE company_id=$1 AND idempotency_key=$2
		FOR UPDATE`, companyID, key)
	if err := row.Scan(&storedCommand, &storedHash, &status, &responseStatus, &response); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return TxReservation{}, ErrCommandInProgress
		}
		return TxReservation{}, err
	}
	if storedCommand != command || storedHash != hash {
		return TxReservation{}, ErrPayloadConflict
	}
	reservation := TxReservation{Inserted: inserted, ResponseBody: json.RawMessage(response)}
	if responseStatus != nil {
		reservation.ResponseStatus = *responseStatus
	}
	if !inserted {
		if status == "IN_PROGRESS" {
			return TxReservation{}, ErrCommandInProgress
		}
		reservation.Completed = status == "COMPLETED"
	}
	return reservation, nil
}

func CompleteTx(ctx context.Context, tx interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, companyID, key string, status int, body any) error {
	key, err := NormalizeKey(key)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `UPDATE command_idempotency_records SET status='COMPLETED',response_status=$3,response_body=$4,completed_at=now() WHERE company_id=$1 AND idempotency_key=$2 AND status='IN_PROGRESS'`, companyID, key, status, encoded)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrCommandInProgress
	}
	return nil
}
