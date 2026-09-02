package inventory

// This file is the domain boundary for the append-only count engine.  It is
// intentionally independent from the older mutable stock count commands in
// service.go so an HTTP migration can adopt it incrementally.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	StockCountEngineInProgress = "IN_PROGRESS"
	StockCountEngineReview     = "REVIEW"
	StockCountEnginePosted     = "POSTED"
	StockCountEngineCancelled  = "CANCELLED"

	StockCountEngineBlind = "BLIND"
	StockCountEngineOpen  = "OPEN"

	StockCountEngineContinue  = "CONTINUE"
	StockCountEngineLockScope = "LOCK_SCOPE"

	StockCountEngineScan       = "SCAN"
	StockCountEngineCorrection = "CORRECTION"
	StockCountEngineZero       = "ZERO"
)

var (
	ErrStockCountEngineReviewRequired = errors.New("STOCK_COUNT_ENGINE_REVIEW_REQUIRED")
	ErrStockCountEngineLocked         = errors.New("STOCK_COUNT_SCOPE_LOCKED")
)

type StockCountEngineScopeInput struct {
	ProductID  string `json:"product_id"`
	VariantID  string `json:"variant_id,omitempty"`
	LocationID string `json:"location_id,omitempty"`
	LotID      string `json:"lot_id,omitempty"`
	SerialID   string `json:"serial_id,omitempty"`
}

type StockCountEngineStartInput struct {
	ID             string                       `json:"id,omitempty"`
	CompanyID      string                       `json:"company_id"`
	WarehouseID    string                       `json:"warehouse_id"`
	Description    string                       `json:"description,omitempty"`
	MovementPolicy string                       `json:"movement_policy,omitempty"`
	BlindCount     bool                         `json:"blind_count"`
	IdempotencyKey string                       `json:"idempotency_key"`
	ActorUserID    string                       `json:"actor_user_id,omitempty"`
	Scopes         []StockCountEngineScopeInput `json:"scopes,omitempty"`
}

type StockCountEngineAddScopeInput struct {
	CompanyID   string `json:"company_id"`
	CountID     string `json:"count_id"`
	ProductID   string `json:"product_id"`
	VariantID   string `json:"variant_id,omitempty"`
	ActorUserID string `json:"actor_user_id"`
}

type StockCountEnginePassInput struct {
	CompanyID   string `json:"company_id,omitempty"`
	CountID     string `json:"count_id"`
	Mode        string `json:"mode"`
	ActorUserID string `json:"actor_user_id,omitempty"`
}

type StockCountEngineSessionInput struct {
	CompanyID       string `json:"company_id,omitempty"`
	CountID         string `json:"count_id"`
	PassID          string `json:"pass_id"`
	ClientSessionID string `json:"client_session_id"`
	ActorUserID     string `json:"actor_user_id"`
}

type StockCountEngineEventInput struct {
	CompanyID   string    `json:"company_id,omitempty"`
	EventID     string    `json:"event_id"`
	CountID     string    `json:"count_id"`
	PassID      string    `json:"pass_id"`
	SessionID   string    `json:"session_id,omitempty"`
	ScopeID     string    `json:"scope_id,omitempty"`
	Barcode     string    `json:"barcode,omitempty"`
	Reason      string    `json:"reason,omitempty"`
	EventType   string    `json:"event_type"`
	Quantity    string    `json:"quantity"`
	OccurredAt  time.Time `json:"occurred_at,omitempty"`
	ActorUserID string    `json:"actor_user_id,omitempty"`
}

type StockCountEngineBatchInput struct {
	CompanyID   string                       `json:"company_id,omitempty"`
	CountID     string                       `json:"count_id"`
	PassID      string                       `json:"pass_id"`
	SessionID   string                       `json:"session_id,omitempty"`
	ActorUserID string                       `json:"actor_user_id,omitempty"`
	Events      []StockCountEngineEventInput `json:"events"`
}

type StockCountEngineEvent struct {
	EventSeq         int64     `json:"event_seq"`
	ID               string    `json:"id"`
	EventID          string    `json:"event_id"`
	CountID          string    `json:"count_id"`
	PassID           string    `json:"pass_id"`
	SessionID        *string   `json:"session_id,omitempty"`
	ScopeID          *string   `json:"scope_id,omitempty"`
	EventType        string    `json:"event_type"`
	Barcode          string    `json:"barcode,omitempty"`
	Reason           string    `json:"reason,omitempty"`
	ResolutionStatus string    `json:"resolution_status"`
	Quantity         string    `json:"quantity"`
	RecordedAt       time.Time `json:"recorded_at"`
}

type StockCountEngineScope struct {
	ID               string  `json:"id"`
	LineNo           int     `json:"line_no"`
	ProductID        string  `json:"product_id"`
	ProductCode      string  `json:"product_code"`
	ProductName      string  `json:"product_name"`
	Barcode          string  `json:"barcode,omitempty"`
	UnitCode         string  `json:"unit_code,omitempty"`
	VariantID        *string `json:"variant_id,omitempty"`
	VariantCode      string  `json:"variant_code,omitempty"`
	WarehouseID      string  `json:"warehouse_id"`
	LocationID       *string `json:"location_id,omitempty"`
	LotID            *string `json:"lot_id,omitempty"`
	SerialID         *string `json:"serial_id,omitempty"`
	SnapshotQuantity *string `json:"snapshot_quantity,omitempty"`
	ExpectedQuantity *string `json:"expected_quantity,omitempty"`
	CountedQuantity  *string `json:"counted_quantity,omitempty"`
	Difference       *string `json:"difference,omitempty"`
	HasResponse      bool    `json:"has_response"`
}

type StockCountEnginePass struct {
	ID     string `json:"id"`
	PassNo int    `json:"pass_no"`
	Mode   string `json:"mode"`
	State  string `json:"state"`
}

type StockCountEngineException struct {
	ID            string         `json:"id"`
	ScopeID       *string        `json:"scope_id,omitempty"`
	ExceptionType string         `json:"exception_type"`
	Status        string         `json:"status"`
	Details       map[string]any `json:"details,omitempty"`
}

type StockCountEngine struct {
	ID             string                      `json:"id"`
	CompanyID      string                      `json:"company_id"`
	CountNo        string                      `json:"count_no"`
	Description    string                      `json:"description"`
	WarehouseID    string                      `json:"warehouse_id"`
	WarehouseCode  string                      `json:"warehouse_code"`
	WarehouseName  string                      `json:"warehouse_name"`
	State          string                      `json:"state"`
	MovementPolicy string                      `json:"movement_policy"`
	BlindCount     bool                        `json:"blind_count"`
	ScopeMode      string                      `json:"scope_mode"`
	SnapshotAt     time.Time                   `json:"snapshot_at"`
	StartedAt      time.Time                   `json:"started_at"`
	FinishedAt     *time.Time                  `json:"finished_at,omitempty"`
	Version        int64                       `json:"version"`
	Passes         []StockCountEnginePass      `json:"passes"`
	Scopes         []StockCountEngineScope     `json:"scopes"`
	Exceptions     []StockCountEngineException `json:"exceptions"`
}

type StockCountEnginePostInput struct {
	CompanyID       string `json:"company_id,omitempty"`
	CountID         string `json:"count_id"`
	IdempotencyKey  string `json:"idempotency_key"`
	ExpectedVersion int64  `json:"expected_version,omitempty"`
	ActorUserID     string `json:"actor_user_id"`
}

type StockCountEngineRecountInput struct {
	CompanyID       string `json:"company_id,omitempty"`
	CountID         string `json:"count_id"`
	ExpectedVersion int64  `json:"expected_version,omitempty"`
	IdempotencyKey  string `json:"idempotency_key"`
	ActorUserID     string `json:"actor_user_id"`
}

type StockCountEngineCancelInput struct {
	CompanyID       string `json:"company_id,omitempty"`
	CountID         string `json:"count_id"`
	IdempotencyKey  string `json:"idempotency_key"`
	ExpectedVersion int64  `json:"expected_version,omitempty"`
	Reason          string `json:"reason"`
	ActorUserID     string `json:"actor_user_id"`
}

type StockCountEngineSyncInput struct {
	CompanyID   string `json:"company_id,omitempty"`
	CountID     string `json:"count_id"`
	PassID      string `json:"pass_id,omitempty"`
	AfterSeq    int64  `json:"after_seq,omitempty"`
	ActorUserID string `json:"actor_user_id,omitempty"`
}

type StockCountEngineSyncResult struct {
	Events []StockCountEngineEvent `json:"events"`
}

func normalizeStockCountEngineStart(input StockCountEngineStartInput) (StockCountEngineStartInput, []byte, error) {
	var err error
	// The first release deliberately has one open, system-visible pass.  Keep
	// the field in the persisted contract for old rows, but reject attempts to
	// create a blind count at the domain boundary.
	if input.BlindCount {
		return StockCountEngineStartInput{}, nil, fmt.Errorf("%w: blind count is no longer supported", identity.ErrValidation)
	}
	input.CompanyID, err = requireUUID("company_id", strings.TrimSpace(input.CompanyID))
	if err != nil {
		return StockCountEngineStartInput{}, nil, err
	}
	input.WarehouseID, err = requireUUID("warehouse_id", input.WarehouseID)
	if err != nil {
		return StockCountEngineStartInput{}, nil, err
	}
	input.Description = strings.TrimSpace(input.Description)
	if utf8.RuneCountInString(input.Description) > 500 {
		return StockCountEngineStartInput{}, nil, fmt.Errorf("%w: açıklama en fazla 500 karakter olabilir", identity.ErrValidation)
	}
	if input.ID == "" {
		input.ID = uuid.NewString()
	}
	input.ID, err = requireUUID("id", input.ID)
	if err != nil {
		return StockCountEngineStartInput{}, nil, err
	}
	input.MovementPolicy = strings.ToUpper(strings.TrimSpace(input.MovementPolicy))
	if input.MovementPolicy == "" {
		input.MovementPolicy = StockCountEngineContinue
	}
	if input.MovementPolicy != StockCountEngineContinue && input.MovementPolicy != StockCountEngineLockScope {
		return StockCountEngineStartInput{}, nil, fmt.Errorf("%w: movement policy is invalid", identity.ErrValidation)
	}
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if input.IdempotencyKey == "" {
		input.IdempotencyKey = "count-engine:" + input.ID
	}
	if len(input.IdempotencyKey) > 255 {
		return StockCountEngineStartInput{}, nil, fmt.Errorf("%w: idempotency key is too long", identity.ErrValidation)
	}
	if input.ActorUserID != "" {
		input.ActorUserID, err = requireUUID("actor_user_id", input.ActorUserID)
		if err != nil {
			return StockCountEngineStartInput{}, nil, err
		}
	}
	seen := map[string]struct{}{}
	for i := range input.Scopes {
		input.Scopes[i].ProductID, err = requireUUID("product_id", input.Scopes[i].ProductID)
		if err != nil {
			return StockCountEngineStartInput{}, nil, err
		}
		for name, value := range map[string]*string{"variant_id": &input.Scopes[i].VariantID, "location_id": &input.Scopes[i].LocationID, "lot_id": &input.Scopes[i].LotID, "serial_id": &input.Scopes[i].SerialID} {
			*value = strings.TrimSpace(*value)
			if *value != "" {
				*value, err = requireUUID(name, *value)
				if err != nil {
					return StockCountEngineStartInput{}, nil, err
				}
			}
		}
		key := strings.Join([]string{input.Scopes[i].ProductID, input.Scopes[i].VariantID, input.Scopes[i].LocationID, input.Scopes[i].LotID, input.Scopes[i].SerialID}, ":")
		if _, ok := seen[key]; ok {
			return StockCountEngineStartInput{}, nil, fmt.Errorf("%w: duplicate count scope", identity.ErrValidation)
		}
		seen[key] = struct{}{}
	}
	sort.Slice(input.Scopes, func(i, j int) bool {
		left := strings.Join([]string{input.Scopes[i].ProductID, input.Scopes[i].VariantID, input.Scopes[i].LocationID, input.Scopes[i].LotID, input.Scopes[i].SerialID}, ":")
		right := strings.Join([]string{input.Scopes[j].ProductID, input.Scopes[j].VariantID, input.Scopes[j].LocationID, input.Scopes[j].LotID, input.Scopes[j].SerialID}, ":")
		return left < right
	})
	canonical := input
	canonical.ID = ""
	canonical.IdempotencyKey = ""
	data, err := json.Marshal(canonical)
	if err != nil {
		return StockCountEngineStartInput{}, nil, err
	}
	hash := sha256.Sum256(data)
	return input, hash[:], nil
}

func normalizeStockCountEngineEvent(input StockCountEngineEventInput, countID, passID, sessionID, actorID string) (StockCountEngineEventInput, []byte, error) {
	var err error
	input.EventID = strings.TrimSpace(input.EventID)
	if input.EventID == "" || len(input.EventID) > 255 {
		return StockCountEngineEventInput{}, nil, fmt.Errorf("%w: event_id is required", identity.ErrValidation)
	}
	input.CountID, err = requireUUID("count_id", countID)
	if err != nil {
		return StockCountEngineEventInput{}, nil, err
	}
	input.PassID, err = requireUUID("pass_id", passID)
	if err != nil {
		return StockCountEngineEventInput{}, nil, err
	}
	input.SessionID = strings.TrimSpace(sessionID)
	if input.SessionID != "" {
		input.SessionID, err = requireUUID("session_id", input.SessionID)
		if err != nil {
			return StockCountEngineEventInput{}, nil, err
		}
	}
	input.ActorUserID = strings.TrimSpace(actorID)
	if input.ActorUserID != "" {
		input.ActorUserID, err = requireUUID("actor_user_id", input.ActorUserID)
		if err != nil {
			return StockCountEngineEventInput{}, nil, err
		}
	}
	input.EventType = strings.ToUpper(strings.TrimSpace(input.EventType))
	if input.EventType != StockCountEngineScan && input.EventType != StockCountEngineCorrection && input.EventType != StockCountEngineZero {
		return StockCountEngineEventInput{}, nil, fmt.Errorf("%w: event type is invalid", identity.ErrValidation)
	}
	input.Barcode = strings.TrimSpace(input.Barcode)
	input.Reason = strings.TrimSpace(input.Reason)
	input.ScopeID = strings.TrimSpace(input.ScopeID)
	if input.EventType == StockCountEngineScan && input.Barcode == "" {
		return StockCountEngineEventInput{}, nil, fmt.Errorf("%w: barcode is required", identity.ErrValidation)
	}
	if input.EventType != StockCountEngineScan {
		input.ScopeID, err = requireUUID("scope_id", input.ScopeID)
		if err != nil {
			return StockCountEngineEventInput{}, nil, err
		}
	}
	if input.EventType == StockCountEngineCorrection && input.Reason == "" {
		return StockCountEngineEventInput{}, nil, fmt.Errorf("%w: correction reason is required", identity.ErrValidation)
	}
	input.Quantity, err = parseNonNegative("quantity", strings.TrimSpace(input.Quantity))
	if err != nil {
		return StockCountEngineEventInput{}, nil, err
	}
	input.Quantity = trimEngineDecimal(input.Quantity)
	if input.EventType == StockCountEngineZero && input.Quantity != "0" {
		return StockCountEngineEventInput{}, nil, fmt.Errorf("%w: zero event quantity must be zero", identity.ErrValidation)
	}
	// Company binding is enforced by the batch transaction, not duplicated in
	// the client event payload hash.
	input.CompanyID = ""
	occurredAtProvided := !input.OccurredAt.IsZero()
	if input.OccurredAt.IsZero() {
		input.OccurredAt = time.Now().UTC()
	}
	canonical := input
	canonical.EventID = ""
	if !occurredAtProvided {
		// A retry that omitted occurred_at must hash identically even though the
		// server assigns a fresh recording timestamp for the first attempt.
		canonical.OccurredAt = time.Time{}
	}
	data, err := json.Marshal(canonical)
	if err != nil {
		return StockCountEngineEventInput{}, nil, err
	}
	hash := sha256.Sum256(data)
	return input, hash[:], nil
}

func (s *Service) StartStockCountEngine(ctx context.Context, input StockCountEngineStartInput) (StockCountEngine, error) {
	normalized, payloadHash, err := normalizeStockCountEngineStart(input)
	if err != nil {
		return StockCountEngine{}, err
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return StockCountEngine{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err = ensureStandardWarehouse(ctx, tx, normalized.CompanyID, normalized.ActorUserID, normalized.WarehouseID); err != nil {
		return StockCountEngine{}, err
	}
	var existingHash []byte
	var existingID string
	err = tx.QueryRow(ctx, `SELECT id,start_payload_hash FROM stock_count_engine_counts WHERE company_id=$1 AND start_idempotency_key=$2`, normalized.CompanyID, normalized.IdempotencyKey).Scan(&existingID, &existingHash)
	if err == nil {
		if !bytes.Equal(existingHash, payloadHash) {
			return StockCountEngine{}, codeError(ErrIdempotencyConflict.Error(), ErrIdempotencyConflict, "same start idempotency key has different payload")
		}
		if err = tx.Commit(ctx); err != nil {
			return StockCountEngine{}, err
		}
		return s.GetStockCountEngine(ctx, normalized.CompanyID, existingID, normalized.ActorUserID)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return StockCountEngine{}, err
	}
	countNo, err := nextStockCountEngineNumber(ctx, tx, normalized.CompanyID)
	if err != nil {
		return StockCountEngine{}, err
	}
	scopeMode := "FULL"
	if len(normalized.Scopes) > 0 {
		scopeMode = "PARTIAL"
	}
	if _, err = tx.Exec(ctx, `INSERT INTO stock_count_engine_counts(id,company_id,count_no,description,warehouse_id,state,movement_policy,blind_count,scope_mode,start_idempotency_key,start_payload_hash) VALUES($1,$2,$3,$4,$5,'IN_PROGRESS',$6,$7,$8,$9,$10)`, normalized.ID, normalized.CompanyID, countNo, normalized.Description, normalized.WarehouseID, normalized.MovementPolicy, normalized.BlindCount, scopeMode, normalized.IdempotencyKey, payloadHash); err != nil {
		return StockCountEngine{}, mapInventoryError(err)
	}
	if len(normalized.Scopes) == 0 {
		// Read the ledger at the captured instant.  Reading the live projection
		// here would include a movement committed after snapshot_at and that
		// movement would then be reconciled a second time during posting.
		rows, queryErr := tx.Query(ctx, `SELECT product_id,variant_id,location_id,lot_id,serial_id,SUM(quantity_delta)::text FROM stock_movements WHERE company_id=$1 AND warehouse_id=$2 AND posted_at <= (SELECT snapshot_at FROM stock_count_engine_counts WHERE company_id=$1 AND id=$3) GROUP BY product_id,variant_id,location_id,lot_id,serial_id HAVING SUM(quantity_delta) <> 0 ORDER BY product_id,variant_id NULLS FIRST,location_id NULLS FIRST,lot_id NULLS FIRST,serial_id NULLS FIRST`, normalized.CompanyID, normalized.WarehouseID, normalized.ID)
		if queryErr != nil {
			return StockCountEngine{}, queryErr
		}
		type engineSnapshotLine struct {
			scope    StockCountEngineScopeInput
			quantity string
		}
		snapshotLines := make([]engineSnapshotLine, 0)
		for rows.Next() {
			var scope StockCountEngineScopeInput
			var variantID, locationID, lotID, serialID *string
			var quantity string
			if err = rows.Scan(&scope.ProductID, &variantID, &locationID, &lotID, &serialID, &quantity); err != nil {
				rows.Close()
				return StockCountEngine{}, err
			}
			scope.VariantID, scope.LocationID, scope.LotID, scope.SerialID = valueOrEmpty(variantID), valueOrEmpty(locationID), valueOrEmpty(lotID), valueOrEmpty(serialID)
			snapshotLines = append(snapshotLines, engineSnapshotLine{scope: scope, quantity: quantity})
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return StockCountEngine{}, err
		}
		// A pgx transaction has one connection. Finish and close the snapshot
		// reader before issuing scope inserts on that same connection.
		rows.Close()
		for index, line := range snapshotLines {
			if err = insertEngineScope(ctx, tx, normalized.ID, normalized.CompanyID, normalized.WarehouseID, index+1, line.scope, line.quantity); err != nil {
				return StockCountEngine{}, err
			}
		}
	} else {
		for line, scope := range normalized.Scopes {
			if _, err = validateInventoryVariantTx(ctx, tx, normalized.CompanyID, scope.ProductID, scope.VariantID); err != nil {
				return StockCountEngine{}, err
			}
			quantity, qErr := snapshotScopeQuantity(ctx, tx, normalized.CompanyID, normalized.WarehouseID, scope)
			if qErr != nil {
				return StockCountEngine{}, qErr
			}
			if err = insertEngineScope(ctx, tx, normalized.ID, normalized.CompanyID, normalized.WarehouseID, line+1, scope, quantity); err != nil {
				return StockCountEngine{}, err
			}
		}
	}
	if _, err = insertEnginePass(ctx, tx, normalized.CompanyID, normalized.ID, 1, map[bool]string{true: StockCountEngineBlind, false: StockCountEngineOpen}[normalized.BlindCount], normalized.ActorUserID); err != nil {
		return StockCountEngine{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return StockCountEngine{}, err
	}
	return s.GetStockCountEngine(ctx, normalized.CompanyID, normalized.ID, normalized.ActorUserID)
}

func nextStockCountEngineNumber(ctx context.Context, tx txDB, companyID string) (string, error) {
	if _, err := tx.Exec(ctx, `INSERT INTO stock_count_engine_number_sequences(company_id,next_number) VALUES($1,1) ON CONFLICT (company_id) DO NOTHING`, companyID); err != nil {
		return "", err
	}
	var value int64
	if err := tx.QueryRow(ctx, `UPDATE stock_count_engine_number_sequences SET next_number=next_number+1 WHERE company_id=$1 RETURNING next_number-1`, companyID).Scan(&value); err != nil {
		return "", err
	}
	return fmt.Sprintf("SAY-%06d", value), nil
}

func snapshotScopeQuantity(ctx context.Context, tx txDB, companyID, warehouseID string, scope StockCountEngineScopeInput) (string, error) {
	if _, err := validateInventoryVariantTx(ctx, tx, companyID, scope.ProductID, scope.VariantID); err != nil {
		return "", err
	}
	checks := []struct {
		id, query, message string
	}{
		{scope.LocationID, `SELECT EXISTS(SELECT 1 FROM locations WHERE company_id=$1 AND warehouse_id=$2 AND id=$3)`, "lokasyon seçilen depoya ait değil"},
		{scope.LotID, `SELECT EXISTS(SELECT 1 FROM lots WHERE company_id=$1 AND product_id=$2 AND id=$3)`, "lot seçilen stok kartına ait değil"},
		{scope.SerialID, `SELECT EXISTS(SELECT 1 FROM serial_numbers WHERE company_id=$1 AND product_id=$2 AND id=$3)`, "seri numarası seçilen stok kartına ait değil"},
	}
	for index, check := range checks {
		if strings.TrimSpace(check.id) == "" {
			continue
		}
		var exists bool
		second := scope.ProductID
		if index == 0 {
			second = warehouseID
		}
		if err := tx.QueryRow(ctx, check.query, companyID, second, check.id).Scan(&exists); err != nil {
			return "", err
		}
		if !exists {
			return "", fmt.Errorf("%w: %s", identity.ErrValidation, check.message)
		}
	}
	var quantity string
	args := []any{companyID, warehouseID, scope.ProductID, nullableEngineUUID(scope.VariantID), nullableEngineUUID(scope.LocationID), nullableEngineUUID(scope.LotID), nullableEngineUUID(scope.SerialID)}
	err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(physical_quantity),0)::text FROM stock_positions WHERE company_id=$1 AND warehouse_id=$2 AND product_id=$3 AND variant_id IS NOT DISTINCT FROM $4::uuid AND location_id IS NOT DISTINCT FROM $5::uuid AND lot_id IS NOT DISTINCT FROM $6::uuid AND serial_id IS NOT DISTINCT FROM $7::uuid`, args...).Scan(&quantity)
	return quantity, err
}

func insertEngineScope(ctx context.Context, tx txDB, countID, companyID, warehouseID string, line int, scope StockCountEngineScopeInput, quantity string) error {
	_, err := tx.Exec(ctx, `INSERT INTO stock_count_engine_scopes(id,company_id,count_id,line_no,product_id,variant_id,warehouse_id,location_id,lot_id,serial_id,snapshot_quantity) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, uuid.NewString(), companyID, countID, line, scope.ProductID, nullableEngineUUID(scope.VariantID), warehouseID, nullableEngineUUID(scope.LocationID), nullableEngineUUID(scope.LotID), nullableEngineUUID(scope.SerialID), quantity)
	return mapInventoryError(err)
}

func insertEnginePass(ctx context.Context, tx txDB, companyID, countID string, passNo int, mode, actor string) (string, error) {
	id := uuid.NewString()
	actorValue := nullableEngineUUID(actor)
	_, err := tx.Exec(ctx, `INSERT INTO stock_count_engine_passes(id,company_id,count_id,pass_no,mode,created_by) VALUES($1,$2,$3,$4,$5,$6)`, id, companyID, countID, passNo, mode, actorValue)
	return id, err
}

func nullableEngineUUID(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func (s *Service) StartStockCountPass(ctx context.Context, input StockCountEnginePassInput) (StockCountEnginePass, error) {
	countID, err := requireUUID("count_id", input.CountID)
	if err != nil {
		return StockCountEnginePass{}, err
	}
	mode := strings.ToUpper(strings.TrimSpace(input.Mode))
	if mode != StockCountEngineOpen {
		return StockCountEnginePass{}, fmt.Errorf("%w: pass mode is invalid", identity.ErrValidation)
	}
	actor := strings.TrimSpace(input.ActorUserID)
	if actor != "" {
		actor, err = requireUUID("actor_user_id", actor)
		if err != nil {
			return StockCountEnginePass{}, err
		}
	}
	requestedCompany := strings.TrimSpace(input.CompanyID)
	if requestedCompany != "" {
		requestedCompany, err = requireUUID("company_id", requestedCompany)
		if err != nil {
			return StockCountEnginePass{}, err
		}
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return StockCountEnginePass{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var companyID, warehouseID, state string
	if requestedCompany != "" {
		err = tx.QueryRow(ctx, `SELECT company_id,warehouse_id,state FROM stock_count_engine_counts WHERE company_id=$1 AND id=$2 FOR UPDATE`, requestedCompany, countID).Scan(&companyID, &warehouseID, &state)
	} else {
		err = tx.QueryRow(ctx, `SELECT company_id,warehouse_id,state FROM stock_count_engine_counts WHERE id=$1 FOR UPDATE`, countID).Scan(&companyID, &warehouseID, &state)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return StockCountEnginePass{}, ErrNotFound
	} else if err != nil {
		return StockCountEnginePass{}, err
	}
	if state != StockCountEngineInProgress {
		return StockCountEnginePass{}, fmt.Errorf("%w: pass cannot be started in state %s", identity.ErrValidation, state)
	}
	if err = ensureWarehouseAccess(ctx, tx, companyID, actor, warehouseID); err != nil {
		return StockCountEnginePass{}, err
	}
	// A count has exactly one active open pass.  Retrying the old pass-create
	// endpoint is therefore safe and does not create a second mutable stream.
	var existing StockCountEnginePass
	if err = tx.QueryRow(ctx, `SELECT id,pass_no,mode,state FROM stock_count_engine_passes WHERE company_id=$1 AND count_id=$2 AND state='IN_PROGRESS' ORDER BY pass_no LIMIT 1`, companyID, countID).Scan(&existing.ID, &existing.PassNo, &existing.Mode, &existing.State); err == nil {
		if existing.Mode != StockCountEngineOpen {
			return StockCountEnginePass{}, fmt.Errorf("%w: blind count pass is not supported", identity.ErrValidation)
		}
		if err = tx.Commit(ctx); err != nil {
			return StockCountEnginePass{}, err
		}
		return existing, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return StockCountEnginePass{}, err
	}
	var passNo int
	if err = tx.QueryRow(ctx, `SELECT COALESCE(MAX(pass_no),0)+1 FROM stock_count_engine_passes WHERE company_id=$1 AND count_id=$2`, companyID, countID).Scan(&passNo); err != nil {
		return StockCountEnginePass{}, err
	}
	id, err := insertEnginePass(ctx, tx, companyID, countID, passNo, mode, actor)
	if err != nil {
		return StockCountEnginePass{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return StockCountEnginePass{}, err
	}
	return StockCountEnginePass{ID: id, PassNo: passNo, Mode: mode, State: "IN_PROGRESS"}, nil
}

func (s *Service) SubmitStockCountPass(ctx context.Context, countID, passID, actor string) (StockCountEnginePass, error) {
	countID, err := requireUUID("count_id", countID)
	if err != nil {
		return StockCountEnginePass{}, err
	}
	passID, err = requireUUID("pass_id", passID)
	if err != nil {
		return StockCountEnginePass{}, err
	}
	actor = strings.TrimSpace(actor)
	if actor != "" {
		actor, err = requireUUID("actor_user_id", actor)
		if err != nil {
			return StockCountEnginePass{}, err
		}
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return StockCountEnginePass{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var companyID, warehouseID, countState string
	if err = tx.QueryRow(ctx, `SELECT c.company_id,c.warehouse_id,c.state FROM stock_count_engine_counts c JOIN stock_count_engine_passes p ON p.company_id=c.company_id AND p.count_id=c.id WHERE c.id=$1 AND p.id=$2 FOR UPDATE`, countID, passID).Scan(&companyID, &warehouseID, &countState); errors.Is(err, pgx.ErrNoRows) {
		return StockCountEnginePass{}, ErrNotFound
	} else if err != nil {
		return StockCountEnginePass{}, err
	}
	if countState != StockCountEngineInProgress {
		return StockCountEnginePass{}, fmt.Errorf("%w: count is not in progress", identity.ErrValidation)
	}
	if err = ensureWarehouseAccess(ctx, tx, companyID, actor, warehouseID); err != nil {
		return StockCountEnginePass{}, err
	}
	var pass StockCountEnginePass
	if err = tx.QueryRow(ctx, `UPDATE stock_count_engine_passes SET state='COMPLETED',completed_at=now() WHERE company_id=$1 AND count_id=$2 AND id=$3 AND state='IN_PROGRESS' RETURNING id,pass_no,mode,state`, companyID, countID, passID).Scan(&pass.ID, &pass.PassNo, &pass.Mode, &pass.State); errors.Is(err, pgx.ErrNoRows) {
		return StockCountEnginePass{}, ErrNotFound
	} else if err != nil {
		return StockCountEnginePass{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return StockCountEnginePass{}, err
	}
	return pass, nil
}

// SubmitStockCountPassAndReview completes the open pass and evaluates the
// review in one transaction. A database failure therefore rolls back both the
// pass completion and any review facts. Once the count is already in REVIEW,
// the same valid pass/count pair is a safe retry and returns the current
// review result instead of falling through to a false not-found response.
func (s *Service) SubmitStockCountPassAndReview(ctx context.Context, countID, passID, actor string) (StockCountEngine, error) {
	return s.submitStockCountPassAndReview(ctx, "", countID, passID, actor)
}

// SubmitStockCountPassAndReviewForCompany applies the same atomic command while
// binding the count to the caller's current company context.
func (s *Service) SubmitStockCountPassAndReviewForCompany(ctx context.Context, companyID, countID, passID, actor string) (StockCountEngine, error) {
	return s.submitStockCountPassAndReview(ctx, companyID, countID, passID, actor)
}

func (s *Service) submitStockCountPassAndReview(ctx context.Context, requestedCompany, countID, passID, actor string) (StockCountEngine, error) {
	countID, err := requireUUID("count_id", countID)
	if err != nil {
		return StockCountEngine{}, err
	}
	passID, err = requireUUID("pass_id", passID)
	if err != nil {
		return StockCountEngine{}, err
	}
	actor = strings.TrimSpace(actor)
	if actor != "" {
		actor, err = requireUUID("actor_user_id", actor)
		if err != nil {
			return StockCountEngine{}, err
		}
	}
	requestedCompany = strings.TrimSpace(requestedCompany)
	if requestedCompany != "" {
		requestedCompany, err = requireUUID("company_id", requestedCompany)
		if err != nil {
			return StockCountEngine{}, err
		}
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return StockCountEngine{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var companyID, warehouseID, countState, passState, passMode string
	var version int64
	if requestedCompany != "" {
		err = tx.QueryRow(ctx, `SELECT c.company_id,c.warehouse_id,c.state,c.version,p.state,p.mode
			FROM stock_count_engine_counts c
			JOIN stock_count_engine_passes p ON p.company_id=c.company_id AND p.count_id=c.id
			WHERE c.company_id=$1 AND c.id=$2 AND p.id=$3
			FOR UPDATE`, requestedCompany, countID, passID).Scan(&companyID, &warehouseID, &countState, &version, &passState, &passMode)
	} else {
		err = tx.QueryRow(ctx, `SELECT c.company_id,c.warehouse_id,c.state,c.version,p.state,p.mode
			FROM stock_count_engine_counts c
			JOIN stock_count_engine_passes p ON p.company_id=c.company_id AND p.count_id=c.id
			WHERE c.id=$1 AND p.id=$2
			FOR UPDATE`, countID, passID).Scan(&companyID, &warehouseID, &countState, &version, &passState, &passMode)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return StockCountEngine{}, ErrNotFound
	}
	if err != nil {
		return StockCountEngine{}, err
	}
	if passMode != StockCountEngineOpen {
		return StockCountEngine{}, fmt.Errorf("%w: kör sayım turu desteklenmiyor", identity.ErrValidation)
	}
	if countState != StockCountEngineInProgress && countState != StockCountEngineReview {
		return StockCountEngine{}, fmt.Errorf("%w: sayım gönderilemez", identity.ErrValidation)
	}
	if err = ensureWarehouseAccess(ctx, tx, companyID, actor, warehouseID); err != nil {
		return StockCountEngine{}, err
	}
	if countState == StockCountEngineInProgress {
		if passState != "IN_PROGRESS" {
			return StockCountEngine{}, fmt.Errorf("%w: sayım turu açık değil", identity.ErrValidation)
		}
		missing, missingErr := hasUncheckedStockCountScopesTx(ctx, tx, companyID, countID)
		if missingErr != nil {
			return StockCountEngine{}, missingErr
		}
		if missing {
			return StockCountEngine{}, ErrStockCountEngineReviewRequired
		}
		if _, err = tx.Exec(ctx, `UPDATE stock_count_engine_passes SET state='COMPLETED',completed_at=now() WHERE company_id=$1 AND count_id=$2 AND id=$3 AND state='IN_PROGRESS'`, companyID, countID, passID); err != nil {
			return StockCountEngine{}, err
		}
	}
	incomplete, err := s.evaluateEngineTx(ctx, tx, companyID, countID, true, actor)
	if err != nil {
		return StockCountEngine{}, err
	}
	if countState == StockCountEngineInProgress {
		if _, err = tx.Exec(ctx, `UPDATE stock_count_engine_counts SET state='REVIEW',version=version+1,updated_at=now() WHERE company_id=$1 AND id=$2 AND version=$3`, companyID, countID, version); err != nil {
			return StockCountEngine{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return StockCountEngine{}, err
	}
	item, err := s.GetStockCountEngine(ctx, companyID, countID, actor)
	if err != nil {
		return StockCountEngine{}, err
	}
	if incomplete {
		return item, ErrStockCountEngineReviewRequired
	}
	return item, nil
}

func (s *Service) StartStockCountSession(ctx context.Context, input StockCountEngineSessionInput) (string, error) {
	countID, err := requireUUID("count_id", input.CountID)
	if err != nil {
		return "", err
	}
	passID, err := requireUUID("pass_id", input.PassID)
	if err != nil {
		return "", err
	}
	actor, err := requireUUID("actor_user_id", input.ActorUserID)
	if err != nil {
		return "", err
	}
	requestedCompany := strings.TrimSpace(input.CompanyID)
	if requestedCompany != "" {
		requestedCompany, err = requireUUID("company_id", requestedCompany)
		if err != nil {
			return "", err
		}
	}
	client := strings.TrimSpace(input.ClientSessionID)
	if client == "" {
		return "", fmt.Errorf("%w: client session is required", identity.ErrValidation)
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var companyID, warehouseID string
	if requestedCompany != "" {
		err = tx.QueryRow(ctx, `SELECT c.company_id,c.warehouse_id FROM stock_count_engine_counts c JOIN stock_count_engine_passes p ON p.company_id=c.company_id AND p.count_id=c.id WHERE c.company_id=$1 AND c.id=$2 AND p.id=$3 AND c.state='IN_PROGRESS' AND p.state='IN_PROGRESS'`, requestedCompany, countID, passID).Scan(&companyID, &warehouseID)
	} else {
		err = tx.QueryRow(ctx, `SELECT c.company_id,c.warehouse_id FROM stock_count_engine_counts c JOIN stock_count_engine_passes p ON p.company_id=c.company_id AND p.count_id=c.id WHERE c.id=$1 AND p.id=$2 AND c.state='IN_PROGRESS' AND p.state='IN_PROGRESS'`, countID, passID).Scan(&companyID, &warehouseID)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	} else if err != nil {
		return "", err
	}
	if err = ensureWarehouseAccess(ctx, tx, companyID, actor, warehouseID); err != nil {
		return "", err
	}
	var id string
	err = tx.QueryRow(ctx, `INSERT INTO stock_count_engine_sessions(id,company_id,count_id,pass_id,user_id,client_session_id) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(company_id,pass_id,client_session_id) DO UPDATE SET client_session_id=EXCLUDED.client_session_id WHERE stock_count_engine_sessions.user_id=EXCLUDED.user_id RETURNING id`, uuid.NewString(), companyID, countID, passID, actor, client).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", identity.ErrConflict
	}
	if err != nil {
		return "", err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", err
	}
	return id, nil
}

func ensureFullCountScopeTx(ctx context.Context, tx txDB, companyID, countID, warehouseID, scopeMode, productID, variantID string, snapshotAt time.Time) (string, string, error) {
	var scopeID string
	var matches int
	if err := tx.QueryRow(ctx, `SELECT
		COALESCE((SELECT id::text FROM stock_count_engine_scopes WHERE company_id=$1 AND count_id=$2 AND product_id=$3 AND variant_id IS NOT DISTINCT FROM $4::uuid ORDER BY id LIMIT 1),''),
		(SELECT COUNT(*) FROM stock_count_engine_scopes WHERE company_id=$1 AND count_id=$2 AND product_id=$3 AND variant_id IS NOT DISTINCT FROM $4::uuid)`, companyID, countID, productID, nullableEngineUUID(variantID)).Scan(&scopeID, &matches); err != nil {
		return "", "", err
	}
	if matches == 1 {
		return scopeID, "ACCEPTED", nil
	}
	if matches > 1 || scopeMode != "FULL" {
		return "", "OUT_OF_SCOPE", nil
	}
	if _, err := validateInventoryVariantTx(ctx, tx, companyID, productID, variantID); err != nil {
		return "", "", err
	}
	var dimensional bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM stock_movements
		WHERE company_id=$1 AND warehouse_id=$2 AND product_id=$3
		  AND variant_id IS NOT DISTINCT FROM $4::uuid AND posted_at<=$5
		  AND (location_id IS NOT NULL OR lot_id IS NOT NULL OR serial_id IS NOT NULL)
	)`, companyID, warehouseID, productID, nullableEngineUUID(variantID), snapshotAt).Scan(&dimensional); err != nil {
		return "", "", err
	}
	if dimensional {
		return "", "OUT_OF_SCOPE", nil
	}
	var snapshotQuantity string
	if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(quantity_delta),0)::text FROM stock_movements WHERE company_id=$1 AND warehouse_id=$2 AND product_id=$3 AND variant_id IS NOT DISTINCT FROM $4::uuid AND posted_at<=$5`, companyID, warehouseID, productID, nullableEngineUUID(variantID), snapshotAt).Scan(&snapshotQuantity); err != nil {
		return "", "", err
	}
	var lineNo int
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(line_no),0)+1 FROM stock_count_engine_scopes WHERE company_id=$1 AND count_id=$2`, companyID, countID).Scan(&lineNo); err != nil {
		return "", "", err
	}
	scopeID = uuid.NewString()
	if _, err := tx.Exec(ctx, `INSERT INTO stock_count_engine_scopes(id,company_id,count_id,line_no,product_id,variant_id,warehouse_id,snapshot_quantity) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, scopeID, companyID, countID, lineNo, productID, nullableEngineUUID(variantID), warehouseID, snapshotQuantity); err != nil {
		return "", "", mapInventoryError(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE stock_count_engine_counts SET version=version+1,updated_at=now() WHERE company_id=$1 AND id=$2`, companyID, countID); err != nil {
		return "", "", err
	}
	return scopeID, "ACCEPTED", nil
}

func (s *Service) AddStockCountEngineScope(ctx context.Context, input StockCountEngineAddScopeInput) (StockCountEngine, error) {
	requestedCompanyID, err := requireUUID("company_id", input.CompanyID)
	if err != nil {
		return StockCountEngine{}, err
	}
	countID, err := requireUUID("count_id", input.CountID)
	if err != nil {
		return StockCountEngine{}, err
	}
	productID, err := requireUUID("product_id", input.ProductID)
	if err != nil {
		return StockCountEngine{}, err
	}
	variantID := strings.TrimSpace(input.VariantID)
	if variantID != "" {
		variantID, err = requireUUID("variant_id", variantID)
		if err != nil {
			return StockCountEngine{}, err
		}
	}
	actor, err := requireUUID("actor_user_id", input.ActorUserID)
	if err != nil {
		return StockCountEngine{}, err
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return StockCountEngine{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var companyID, warehouseID, state, scopeMode string
	var snapshotAt time.Time
	if err = tx.QueryRow(ctx, `SELECT company_id,warehouse_id,state,scope_mode,snapshot_at FROM stock_count_engine_counts WHERE company_id=$1 AND id=$2 FOR UPDATE`, requestedCompanyID, countID).Scan(&companyID, &warehouseID, &state, &scopeMode, &snapshotAt); errors.Is(err, pgx.ErrNoRows) {
		return StockCountEngine{}, ErrNotFound
	} else if err != nil {
		return StockCountEngine{}, err
	}
	if state != StockCountEngineInProgress {
		return StockCountEngine{}, fmt.Errorf("%w: count is not in progress", identity.ErrValidation)
	}
	if err = ensureWarehouseAccess(ctx, tx, companyID, actor, warehouseID); err != nil {
		return StockCountEngine{}, err
	}
	if scopeMode != "FULL" {
		return StockCountEngine{}, fmt.Errorf("%w: partial count scope cannot be expanded", identity.ErrValidation)
	}
	if _, status, scopeErr := ensureFullCountScopeTx(ctx, tx, companyID, countID, warehouseID, scopeMode, productID, variantID, snapshotAt); scopeErr != nil {
		return StockCountEngine{}, scopeErr
	} else if status != "ACCEPTED" {
		return StockCountEngine{}, fmt.Errorf("%w: stok birden fazla lokasyon, lot veya seri satırına ayrılmış", identity.ErrValidation)
	}
	if err = tx.Commit(ctx); err != nil {
		return StockCountEngine{}, err
	}
	return s.GetStockCountEngine(ctx, companyID, countID, actor)
}

func (s *Service) BatchScanStockCount(ctx context.Context, input StockCountEngineBatchInput) ([]StockCountEngineEvent, error) {
	countID, err := requireUUID("count_id", input.CountID)
	if err != nil {
		return nil, err
	}
	passID, err := requireUUID("pass_id", input.PassID)
	if err != nil {
		return nil, err
	}
	if len(input.Events) == 0 {
		return []StockCountEngineEvent{}, nil
	}
	requestedCompany := strings.TrimSpace(input.CompanyID)
	if requestedCompany != "" {
		requestedCompany, err = requireUUID("company_id", requestedCompany)
		if err != nil {
			return nil, err
		}
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var companyID, warehouseID, state, scopeMode string
	var snapshotAt time.Time
	if requestedCompany != "" {
		err = tx.QueryRow(ctx, `SELECT company_id,warehouse_id,state,scope_mode,snapshot_at FROM stock_count_engine_counts WHERE company_id=$1 AND id=$2 FOR UPDATE`, requestedCompany, countID).Scan(&companyID, &warehouseID, &state, &scopeMode, &snapshotAt)
	} else {
		err = tx.QueryRow(ctx, `SELECT company_id,warehouse_id,state,scope_mode,snapshot_at FROM stock_count_engine_counts WHERE id=$1 FOR UPDATE`, countID).Scan(&companyID, &warehouseID, &state, &scopeMode, &snapshotAt)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	if state != StockCountEngineInProgress {
		return nil, fmt.Errorf("%w: count is not in progress", identity.ErrValidation)
	}
	actor := strings.TrimSpace(input.ActorUserID)
	if actor != "" {
		actor, err = requireUUID("actor_user_id", actor)
		if err != nil {
			return nil, err
		}
		if err = ensureWarehouseAccess(ctx, tx, companyID, actor, warehouseID); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(input.SessionID) != "" {
		var sessionOK bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM stock_count_engine_sessions WHERE company_id=$1 AND count_id=$2 AND pass_id=$3 AND id=$4 AND user_id=$5 AND closed_at IS NULL)`, companyID, countID, passID, input.SessionID, actor).Scan(&sessionOK); err != nil {
			return nil, err
		}
		if !sessionOK {
			return nil, identity.ErrForbidden
		}
	}
	var passState string
	if err = tx.QueryRow(ctx, `SELECT state FROM stock_count_engine_passes WHERE company_id=$1 AND count_id=$2 AND id=$3`, companyID, countID, passID).Scan(&passState); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	if passState != "IN_PROGRESS" {
		return nil, fmt.Errorf("%w: pass is closed", identity.ErrValidation)
	}
	result := make([]StockCountEngineEvent, 0, len(input.Events))
	for _, raw := range input.Events {
		event, hash, normErr := normalizeStockCountEngineEvent(raw, countID, passID, input.SessionID, actor)
		if normErr != nil {
			return nil, normErr
		}
		var existingSeq int64
		var existingHash []byte
		var existingEventID, existingResolution string
		var existingScopeID *string
		var existingRecordedAt time.Time
		existingErr := tx.QueryRow(ctx, `SELECT event_seq,payload_hash,event_id,resolution_status,scope_id,recorded_at FROM stock_count_engine_events WHERE company_id=$1 AND event_id=$2`, companyID, event.EventID).Scan(&existingSeq, &existingHash, &existingEventID, &existingResolution, &existingScopeID, &existingRecordedAt)
		if existingErr == nil {
			if !bytes.Equal(existingHash, hash) {
				return nil, codeError(ErrIdempotencyConflict.Error(), ErrIdempotencyConflict, "same event_id has different payload")
			}
			result = append(result, StockCountEngineEvent{
				EventSeq: existingSeq, EventID: existingEventID, CountID: countID, PassID: passID,
				ScopeID: existingScopeID, EventType: event.EventType, Barcode: event.Barcode,
				Reason: event.Reason, ResolutionStatus: existingResolution, Quantity: event.Quantity,
				RecordedAt: existingRecordedAt,
			})
			continue
		}
		if !errors.Is(existingErr, pgx.ErrNoRows) {
			return nil, existingErr
		}
		var scopeID any
		status := "ACCEPTED"
		var resolvedScope string
		if event.EventType == StockCountEngineScan {
			rows, lookupErr := tx.Query(ctx, `SELECT pb.product_id::text,pb.variant_id::text FROM product_barcodes pb JOIN products p ON p.company_id=pb.company_id AND p.id=pb.product_id WHERE pb.company_id=$1 AND pb.barcode=$2 GROUP BY pb.product_id,pb.variant_id`, companyID, event.Barcode)
			if lookupErr != nil {
				return nil, lookupErr
			}
			var productID string
			var variantID *string
			matches := 0
			for rows.Next() {
				var candidateProduct string
				var candidateVariant *string
				if lookupErr = rows.Scan(&candidateProduct, &candidateVariant); lookupErr != nil {
					rows.Close()
					return nil, lookupErr
				}
				matches++
				if matches == 1 {
					productID, variantID = candidateProduct, candidateVariant
				}
			}
			if lookupErr = rows.Err(); lookupErr != nil {
				rows.Close()
				return nil, lookupErr
			}
			rows.Close()
			switch {
			case matches == 0:
				status = "UNKNOWN"
			case matches > 1:
				status = "AMBIGUOUS"
			default:
				resolvedScope, status, err = ensureFullCountScopeTx(ctx, tx, companyID, countID, warehouseID, scopeMode, productID, valueOrEmpty(variantID), snapshotAt)
				if err != nil {
					return nil, err
				}
			}
			if err != nil {
				return nil, err
			}
		} else {
			resolvedScope = event.ScopeID
			var exists bool
			if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM stock_count_engine_scopes WHERE company_id=$1 AND count_id=$2 AND id=$3)`, companyID, countID, resolvedScope).Scan(&exists); err != nil {
				return nil, err
			}
			if !exists {
				return nil, ErrNotFound
			}
		}
		if resolvedScope != "" {
			scopeID = resolvedScope
		}
		var eventID, resolution string
		var seq int64
		var recorded time.Time
		err = tx.QueryRow(ctx, `INSERT INTO stock_count_engine_events(id,company_id,count_id,pass_id,session_id,scope_id,event_id,event_type,barcode,reason,resolution_status,quantity,payload_hash,actor_user_id,occurred_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15) ON CONFLICT(company_id,event_id) DO NOTHING RETURNING event_seq,event_id,resolution_status,recorded_at`, uuid.NewString(), companyID, countID, passID, nullableEngineUUID(event.SessionID), scopeID, event.EventID, event.EventType, event.Barcode, event.Reason, status, event.Quantity, hash, nullableEngineUUID(actor), event.OccurredAt).Scan(&seq, &eventID, &resolution, &recorded)
		if errors.Is(err, pgx.ErrNoRows) {
			var oldHash []byte
			var storedScopeID *string
			err = tx.QueryRow(ctx, `SELECT event_seq,payload_hash,event_id,resolution_status,scope_id,recorded_at FROM stock_count_engine_events WHERE company_id=$1 AND event_id=$2`, companyID, event.EventID).Scan(&seq, &oldHash, &eventID, &resolution, &storedScopeID, &recorded)
			if err != nil {
				return nil, err
			}
			if !bytes.Equal(oldHash, hash) {
				return nil, codeError(ErrIdempotencyConflict.Error(), ErrIdempotencyConflict, "same event_id has different payload")
			}
			resolvedScope = valueOrEmpty(storedScopeID)
		} else if err != nil {
			return nil, mapInventoryError(err)
		}
		output := StockCountEngineEvent{EventSeq: seq, EventID: eventID, CountID: countID, PassID: passID, EventType: event.EventType, Barcode: event.Barcode, Reason: event.Reason, ResolutionStatus: resolution, Quantity: event.Quantity, RecordedAt: recorded}
		if resolvedScope != "" {
			output.ScopeID = &resolvedScope
		}
		result = append(result, output)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Service) CorrectStockCount(ctx context.Context, input StockCountEngineEventInput) ([]StockCountEngineEvent, error) {
	input.EventType = StockCountEngineCorrection
	return s.BatchScanStockCount(ctx, StockCountEngineBatchInput{CompanyID: input.CompanyID, CountID: input.CountID, PassID: input.PassID, SessionID: input.SessionID, ActorUserID: input.ActorUserID, Events: []StockCountEngineEventInput{input}})
}

// StockCountImportLine is the append-only boundary for a validated count
// spreadsheet. Import adapters resolve the human line number to ScopeID before
// calling this method; callers cannot create or rename a scope here.
type StockCountImportLine struct {
	ScopeID   string
	Quantity  string
	RowNumber int
}

func (s *Service) ApplyStockCountImport(ctx context.Context, companyID, countID, actor string, lines []StockCountImportLine) error {
	companyID, err := requireUUID("company_id", companyID)
	if err != nil {
		return err
	}
	countID, err = requireUUID("count_id", countID)
	if err != nil {
		return err
	}
	actor, err = requireUUID("actor_user_id", actor)
	if err != nil {
		return err
	}
	if len(lines) == 0 {
		return nil
	}
	passID, err := s.CurrentStockCountPassID(ctx, companyID, countID, "")
	if err != nil {
		return err
	}
	events := make([]StockCountEngineEventInput, 0, len(lines))
	for _, line := range lines {
		if line.RowNumber < 1 {
			return fmt.Errorf("%w: sayım satır numarası geçersiz", identity.ErrValidation)
		}
		events = append(events, StockCountEngineEventInput{
			EventID: fmt.Sprintf("stock-count-import:%s:%d", countID, line.RowNumber), CountID: countID, PassID: passID,
			ScopeID: line.ScopeID, EventType: StockCountEngineCorrection, Quantity: line.Quantity,
			Reason: "Sayım Excel aktarımı", ActorUserID: actor,
		})
	}
	_, err = s.BatchScanStockCount(ctx, StockCountEngineBatchInput{CompanyID: companyID, CountID: countID, PassID: passID, ActorUserID: actor, Events: events})
	return err
}

func (s *Service) ConfirmStockCountZero(ctx context.Context, input StockCountEngineEventInput) ([]StockCountEngineEvent, error) {
	input.EventType = StockCountEngineZero
	input.Quantity = "0"
	return s.BatchScanStockCount(ctx, StockCountEngineBatchInput{CompanyID: input.CompanyID, CountID: input.CountID, PassID: input.PassID, SessionID: input.SessionID, ActorUserID: input.ActorUserID, Events: []StockCountEngineEventInput{input}})
}

type engineEffective struct {
	quantity     string
	hasResponse  bool
	lastRecorded time.Time
}

func calculateEngineEffective(events []struct {
	scopeID, eventType, quantity string
	recorded                     time.Time
}) map[string]engineEffective {
	result := map[string]engineEffective{}
	for _, event := range events {
		current := result[event.scopeID]
		if event.eventType == StockCountEngineCorrection || event.eventType == StockCountEngineZero {
			current.quantity = event.quantity
		} else {
			current.quantity = engineDecimalAdd(current.quantity, event.quantity)
		}
		current.hasResponse = true
		current.lastRecorded = event.recorded
		result[event.scopeID] = current
	}
	return result
}

func engineDecimalAdd(left, right string) string {
	if strings.TrimSpace(left) == "" {
		left = "0"
	}
	if strings.TrimSpace(right) == "" {
		right = "0"
	}
	a, ok := new(big.Rat).SetString(left)
	if !ok {
		return right
	}
	b, ok := new(big.Rat).SetString(right)
	if !ok {
		return left
	}
	return trimEngineDecimal(a.Add(a, b).FloatString(8))
}

// trimEngineDecimal keeps the exact decimal value while removing only scale
// padding produced by PostgreSQL numeric columns and big.Rat formatting.
func trimEngineDecimal(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "0"
	}
	negative := strings.HasPrefix(value, "-")
	unsigned := strings.TrimPrefix(strings.TrimPrefix(value, "+"), "-")
	parts := strings.SplitN(unsigned, ".", 2)
	integer := strings.TrimLeft(parts[0], "0")
	if integer == "" {
		integer = "0"
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = strings.TrimRight(parts[1], "0")
	}
	if fraction == "" {
		if integer == "0" {
			return "0"
		}
		if negative {
			return "-" + integer
		}
		return integer
	}
	result := integer + "." + fraction
	if negative && result != "0" {
		return "-" + result
	}
	return result
}

func (s *Service) evaluateEngineTx(ctx context.Context, tx txDB, companyID, countID string, createExceptions bool, actor string) (bool, error) {
	var snapshot time.Time
	var warehouseID, policy string
	if err := tx.QueryRow(ctx, `SELECT snapshot_at,warehouse_id,movement_policy FROM stock_count_engine_counts WHERE company_id=$1 AND id=$2`, companyID, countID).Scan(&snapshot, &warehouseID, &policy); err != nil {
		return false, err
	}
	rows, err := tx.Query(ctx, `SELECT scope_id,event_type,quantity::text,recorded_at FROM stock_count_engine_events WHERE company_id=$1 AND count_id=$2 AND resolution_status='ACCEPTED' ORDER BY event_seq`, companyID, countID)
	if err != nil {
		return false, err
	}
	events := make([]struct {
		scopeID, eventType, quantity string
		recorded                     time.Time
	}, 0)
	for rows.Next() {
		var e struct {
			scopeID, eventType, quantity string
			recorded                     time.Time
		}
		if err = rows.Scan(&e.scopeID, &e.eventType, &e.quantity, &e.recorded); err != nil {
			rows.Close()
			return false, err
		}
		events = append(events, e)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return false, err
	}
	effective := calculateEngineEffective(events)
	incomplete := false
	scopeRows, err := tx.Query(ctx, `SELECT id,product_id,variant_id,location_id,lot_id,serial_id FROM stock_count_engine_scopes WHERE company_id=$1 AND count_id=$2 ORDER BY line_no FOR UPDATE`, companyID, countID)
	if err != nil {
		return false, err
	}
	type engineScope struct {
		id, productID                          string
		variantID, locationID, lotID, serialID *string
	}
	scopes := make([]engineScope, 0)
	for scopeRows.Next() {
		var scopeID, productID string
		var variantID, locationID, lotID, serialID *string
		if err = scopeRows.Scan(&scopeID, &productID, &variantID, &locationID, &lotID, &serialID); err != nil {
			scopeRows.Close()
			return false, err
		}
		scopes = append(scopes, engineScope{id: scopeID, productID: productID, variantID: variantID, locationID: locationID, lotID: lotID, serialID: serialID})
	}
	if err = scopeRows.Err(); err != nil {
		scopeRows.Close()
		return false, err
	}
	// A pgx transaction has one connection. Finish and close the scope reader
	// before issuing the per-scope queries and exception inserts below.
	scopeRows.Close()
	for _, scope := range scopes {
		current, ok := effective[scope.id]
		if !ok {
			incomplete = true
			if createExceptions {
				if err = addEngineException(ctx, tx, companyID, countID, scope.id, "UNCHECKED", map[string]any{"reason": "required scan, correction, or zero confirmation is missing"}); err != nil {
					return false, err
				}
			}
			continue
		}
		if policy == StockCountEngineContinue {
			var movementCount int
			last := current.lastRecorded
			if err = tx.QueryRow(ctx, `SELECT COUNT(*) FROM stock_movements WHERE company_id=$1 AND warehouse_id=$2 AND product_id=$3 AND posted_at>$4 AND posted_at>$5 AND variant_id IS NOT DISTINCT FROM $6::uuid AND location_id IS NOT DISTINCT FROM $7::uuid AND lot_id IS NOT DISTINCT FROM $8::uuid AND serial_id IS NOT DISTINCT FROM $9::uuid`, companyID, warehouseID, scope.productID, snapshot, last, scope.variantID, scope.locationID, scope.lotID, scope.serialID).Scan(&movementCount); err != nil {
				return false, err
			}
			if movementCount > 0 {
				incomplete = true
				if createExceptions {
					if err = addEngineException(ctx, tx, companyID, countID, scope.id, "MOVEMENT_OVERLAP", map[string]any{"movement_count": movementCount}); err != nil {
						return false, err
					}
				}
			}
		}
	}
	var unknown int
	if err = tx.QueryRow(ctx, `SELECT COUNT(*) FROM stock_count_engine_events WHERE company_id=$1 AND count_id=$2 AND resolution_status='UNKNOWN'`, companyID, countID).Scan(&unknown); err != nil {
		return false, err
	}
	if unknown > 0 {
		var openUnknown bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM stock_count_engine_review_exceptions WHERE company_id=$1 AND count_id=$2 AND exception_type='UNKNOWN_BARCODE' AND status='OPEN')`, companyID, countID).Scan(&openUnknown); err != nil {
			return false, err
		}
		if !openUnknown && createExceptions {
			if err = addEngineException(ctx, tx, companyID, countID, "", "UNKNOWN_BARCODE", map[string]any{"event_count": unknown}); err != nil {
				return false, err
			}
			openUnknown = true
		}
		if openUnknown {
			incomplete = true
		}
	}
	var outOfScope int
	if err = tx.QueryRow(ctx, `SELECT COUNT(*) FROM stock_count_engine_events WHERE company_id=$1 AND count_id=$2 AND resolution_status='OUT_OF_SCOPE'`, companyID, countID).Scan(&outOfScope); err != nil {
		return false, err
	}
	if outOfScope > 0 {
		var openOutOfScope bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM stock_count_engine_review_exceptions WHERE company_id=$1 AND count_id=$2 AND exception_type='OUT_OF_SCOPE' AND status='OPEN')`, companyID, countID).Scan(&openOutOfScope); err != nil {
			return false, err
		}
		if !openOutOfScope && createExceptions {
			if err = addEngineException(ctx, tx, companyID, countID, "", "OUT_OF_SCOPE", map[string]any{"event_count": outOfScope}); err != nil {
				return false, err
			}
			openOutOfScope = true
		}
		if openOutOfScope {
			incomplete = true
		}
	}
	var ambiguous int
	if err = tx.QueryRow(ctx, `SELECT COUNT(*) FROM stock_count_engine_events WHERE company_id=$1 AND count_id=$2 AND resolution_status='AMBIGUOUS'`, companyID, countID).Scan(&ambiguous); err != nil {
		return false, err
	}
	if ambiguous > 0 {
		var openAmbiguous bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM stock_count_engine_review_exceptions WHERE company_id=$1 AND count_id=$2 AND exception_type='AMBIGUOUS_BARCODE' AND status='OPEN')`, companyID, countID).Scan(&openAmbiguous); err != nil {
			return false, err
		}
		if !openAmbiguous && createExceptions {
			if err = addEngineException(ctx, tx, companyID, countID, "", "AMBIGUOUS_BARCODE", map[string]any{"event_count": ambiguous}); err != nil {
				return false, err
			}
			openAmbiguous = true
		}
		if openAmbiguous {
			incomplete = true
		}
	}
	if !incomplete {
		if _, err = tx.Exec(ctx, `UPDATE stock_count_engine_review_exceptions SET status='RESOLVED',resolved_at=now(),resolved_by=$1 WHERE company_id=$2 AND count_id=$3 AND status='OPEN'`, nullableEngineUUID(actor), companyID, countID); err != nil {
			return false, err
		}
	}
	return incomplete, nil
}

func addEngineException(ctx context.Context, tx txDB, companyID, countID, scopeID, kind string, details map[string]any) error {
	payload, _ := json.Marshal(details)
	_, err := tx.Exec(ctx, `INSERT INTO stock_count_engine_review_exceptions(id,company_id,count_id,scope_id,exception_type,details) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, uuid.NewString(), companyID, countID, nullableEngineUUID(scopeID), kind, payload)
	return err
}

func hasUncheckedStockCountScopesTx(ctx context.Context, tx txDB, companyID, countID string) (bool, error) {
	var missing bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM stock_count_engine_scopes scope
			WHERE scope.company_id=$1 AND scope.count_id=$2
			  AND NOT EXISTS(
				SELECT 1
				FROM stock_count_engine_events event
				WHERE event.company_id=scope.company_id
				  AND event.count_id=scope.count_id
				  AND event.scope_id=scope.id
				  AND event.resolution_status='ACCEPTED'
			  )
		)`, companyID, countID).Scan(&missing)
	return missing, err
}

func (s *Service) SubmitStockCountReview(ctx context.Context, companyID, countID, actor string, expectedVersion int64) (StockCountEngine, error) {
	companyID, err := requireUUID("company_id", companyID)
	if err != nil {
		return StockCountEngine{}, err
	}
	countID, err = requireUUID("count_id", countID)
	if err != nil {
		return StockCountEngine{}, err
	}
	actor = strings.TrimSpace(actor)
	if actor != "" {
		actor, err = requireUUID("actor_user_id", actor)
		if err != nil {
			return StockCountEngine{}, err
		}
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return StockCountEngine{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var state string
	var version int64
	var warehouseID string
	if err = tx.QueryRow(ctx, `SELECT state,version,warehouse_id FROM stock_count_engine_counts WHERE company_id=$1 AND id=$2 FOR UPDATE`, companyID, countID).Scan(&state, &version, &warehouseID); errors.Is(err, pgx.ErrNoRows) {
		return StockCountEngine{}, ErrNotFound
	} else if err != nil {
		return StockCountEngine{}, err
	}
	if state != StockCountEngineInProgress && state != StockCountEngineReview {
		return StockCountEngine{}, fmt.Errorf("%w: count cannot be submitted", identity.ErrValidation)
	}
	if expectedVersion > 0 && expectedVersion != version {
		return StockCountEngine{}, ErrConflict
	}
	if err = ensureWarehouseAccess(ctx, tx, companyID, actor, warehouseID); err != nil {
		return StockCountEngine{}, err
	}
	if state == StockCountEngineInProgress {
		missing, missingErr := hasUncheckedStockCountScopesTx(ctx, tx, companyID, countID)
		if missingErr != nil {
			return StockCountEngine{}, missingErr
		}
		if missing {
			return StockCountEngine{}, ErrStockCountEngineReviewRequired
		}
	}
	incomplete, err := s.evaluateEngineTx(ctx, tx, companyID, countID, true, actor)
	if err != nil {
		return StockCountEngine{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE stock_count_engine_counts SET state='REVIEW',version=version+1,updated_at=now() WHERE company_id=$1 AND id=$2 AND version=$3`, companyID, countID, version); err != nil {
		return StockCountEngine{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return StockCountEngine{}, err
	}
	item, err := s.GetStockCountEngine(ctx, companyID, countID, actor)
	if err != nil {
		return StockCountEngine{}, err
	}
	if incomplete {
		return item, ErrStockCountEngineReviewRequired
	}
	return item, nil
}

func (s *Service) ReviewStockCountEngine(ctx context.Context, companyID, countID, actor string, expectedVersion int64) (StockCountEngine, error) {
	item, err := s.SubmitStockCountReview(ctx, companyID, countID, actor, expectedVersion)
	if err != nil && !errors.Is(err, ErrStockCountEngineReviewRequired) {
		return item, err
	}
	return item, err
}

// ReopenStockCountEngineForRecount returns a review count to the counting
// state. The previous pass, events and review exceptions remain immutable
// history; a new open pass is created for the additional count work.
func (s *Service) ReopenStockCountEngineForRecount(ctx context.Context, input StockCountEngineRecountInput) (StockCountEngine, error) {
	countID, err := requireUUID("count_id", input.CountID)
	if err != nil {
		return StockCountEngine{}, err
	}
	companyID, err := requireUUID("company_id", input.CompanyID)
	if err != nil {
		return StockCountEngine{}, err
	}
	actor, err := requireUUID("actor_user_id", input.ActorUserID)
	if err != nil {
		return StockCountEngine{}, err
	}
	key := strings.TrimSpace(input.IdempotencyKey)
	if key == "" || len(key) > 255 {
		return StockCountEngine{}, fmt.Errorf("%w: recount idempotency key is required", identity.ErrValidation)
	}

	payload, _ := json.Marshal(input)
	payloadHash := sha256.Sum256(payload)
	tx, err := s.begin(ctx)
	if err != nil {
		return StockCountEngine{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var warehouseID, state string
	var version int64
	if err = tx.QueryRow(ctx, `SELECT warehouse_id,state,version FROM stock_count_engine_counts WHERE company_id=$1 AND id=$2 FOR UPDATE`, companyID, countID).Scan(&warehouseID, &state, &version); errors.Is(err, pgx.ErrNoRows) {
		return StockCountEngine{}, ErrNotFound
	} else if err != nil {
		return StockCountEngine{}, err
	}
	if err = ensureWarehouseAccess(ctx, tx, companyID, actor, warehouseID); err != nil {
		return StockCountEngine{}, err
	}

	var commandHash []byte
	var completed bool
	err = tx.QueryRow(ctx, `SELECT payload_hash,completed_at IS NOT NULL FROM stock_count_engine_commands WHERE company_id=$1 AND count_id=$2 AND command_name='RECOUNT' AND idempotency_key=$3 FOR UPDATE`, companyID, countID, key).Scan(&commandHash, &completed)
	if err == nil {
		if !bytes.Equal(commandHash, payloadHash[:]) {
			return StockCountEngine{}, codeError(ErrIdempotencyConflict.Error(), ErrIdempotencyConflict, "same recount idempotency key has different payload")
		}
		if completed {
			if err = tx.Commit(ctx); err != nil {
				return StockCountEngine{}, err
			}
			return s.GetStockCountEngine(ctx, companyID, countID, actor)
		}
	} else if errors.Is(err, pgx.ErrNoRows) {
		if _, err = tx.Exec(ctx, `INSERT INTO stock_count_engine_commands(id,company_id,count_id,command_name,idempotency_key,payload_hash,actor_user_id) VALUES($1,$2,$3,'RECOUNT',$4,$5,$6)`, uuid.NewString(), companyID, countID, key, payloadHash[:], actor); err != nil {
			return StockCountEngine{}, err
		}
	} else {
		return StockCountEngine{}, err
	}

	if input.ExpectedVersion > 0 && input.ExpectedVersion != version {
		return StockCountEngine{}, ErrConflict
	}
	if state != StockCountEngineReview || !CanTransitionStockCountEngine(state, StockCountEngineInProgress) {
		return StockCountEngine{}, fmt.Errorf("%w: yalnızca incelemedeki sayım yeniden sayıma alınabilir", identity.ErrValidation)
	}
	var activePass bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM stock_count_engine_passes WHERE company_id=$1 AND count_id=$2 AND state='IN_PROGRESS')`, companyID, countID).Scan(&activePass); err != nil {
		return StockCountEngine{}, err
	}
	if activePass {
		return StockCountEngine{}, fmt.Errorf("%w: sayımda zaten açık bir tur var", identity.ErrValidation)
	}
	var passNo int
	if err = tx.QueryRow(ctx, `SELECT COALESCE(MAX(pass_no),0)+1 FROM stock_count_engine_passes WHERE company_id=$1 AND count_id=$2`, companyID, countID).Scan(&passNo); err != nil {
		return StockCountEngine{}, err
	}
	if _, err = insertEnginePass(ctx, tx, companyID, countID, passNo, StockCountEngineOpen, actor); err != nil {
		return StockCountEngine{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE stock_count_engine_sessions SET closed_at=COALESCE(closed_at,now()) WHERE company_id=$1 AND count_id=$2 AND closed_at IS NULL`, companyID, countID); err != nil {
		return StockCountEngine{}, err
	}
	commandResult, _ := json.Marshal(map[string]string{"count_id": countID})
	updateResult, err := tx.Exec(ctx, `UPDATE stock_count_engine_counts SET state='IN_PROGRESS',version=version+1,updated_at=now() WHERE company_id=$1 AND id=$2 AND version=$3`, companyID, countID, version)
	if err != nil {
		return StockCountEngine{}, err
	}
	if updateResult.RowsAffected() != 1 {
		return StockCountEngine{}, ErrConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE stock_count_engine_commands SET completed_at=now(),result=$1 WHERE company_id=$2 AND count_id=$3 AND command_name='RECOUNT' AND idempotency_key=$4`, commandResult, companyID, countID, key); err != nil {
		return StockCountEngine{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return StockCountEngine{}, err
	}
	return s.GetStockCountEngine(ctx, companyID, countID, actor)
}

// ResolveStockCountEngineException is an explicit review decision. Facts in
// the scan/event ledger remain immutable; only the review workflow status is
// closed, with a reason retained in the exception details.
func (s *Service) ResolveStockCountEngineException(ctx context.Context, companyID, countID, exceptionID, actor, reason string) (StockCountEngine, error) {
	companyID, err := requireUUID("company_id", companyID)
	if err != nil {
		return StockCountEngine{}, err
	}
	countID, err = requireUUID("count_id", countID)
	if err != nil {
		return StockCountEngine{}, err
	}
	exceptionID, err = requireUUID("exception_id", exceptionID)
	if err != nil {
		return StockCountEngine{}, err
	}
	actor, err = requireUUID("actor_user_id", actor)
	if err != nil {
		return StockCountEngine{}, err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return StockCountEngine{}, fmt.Errorf("%w: exception resolution reason is required", identity.ErrValidation)
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return StockCountEngine{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var state, warehouseID string
	if err = tx.QueryRow(ctx, `SELECT state,warehouse_id FROM stock_count_engine_counts WHERE company_id=$1 AND id=$2 FOR UPDATE`, companyID, countID).Scan(&state, &warehouseID); errors.Is(err, pgx.ErrNoRows) {
		return StockCountEngine{}, ErrNotFound
	} else if err != nil {
		return StockCountEngine{}, err
	}
	if state != StockCountEngineReview {
		return StockCountEngine{}, fmt.Errorf("%w: exception can only be resolved in review", identity.ErrValidation)
	}
	if err = ensureWarehouseAccess(ctx, tx, companyID, actor, warehouseID); err != nil {
		return StockCountEngine{}, err
	}
	result, err := tx.Exec(ctx, `UPDATE stock_count_engine_review_exceptions SET status='RESOLVED',resolved_at=now(),resolved_by=$1,details=details || jsonb_build_object('resolution_reason',$2) WHERE company_id=$3 AND count_id=$4 AND id=$5 AND status='OPEN'`, actor, reason, companyID, countID, exceptionID)
	if err != nil {
		return StockCountEngine{}, err
	}
	if result.RowsAffected() == 0 {
		return StockCountEngine{}, ErrNotFound
	}
	if _, err = tx.Exec(ctx, `UPDATE stock_count_engine_counts SET version=version+1,updated_at=now() WHERE company_id=$1 AND id=$2`, companyID, countID); err != nil {
		return StockCountEngine{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return StockCountEngine{}, err
	}
	return s.GetStockCountEngine(ctx, companyID, countID, actor)
}

func (s *Service) PostStockCountEngine(ctx context.Context, input StockCountEnginePostInput) (StockCountEngine, error) {
	countID, err := requireUUID("count_id", input.CountID)
	if err != nil {
		return StockCountEngine{}, err
	}
	actor, err := requireUUID("actor_user_id", input.ActorUserID)
	if err != nil {
		return StockCountEngine{}, err
	}
	requestedCompany := strings.TrimSpace(input.CompanyID)
	if requestedCompany != "" {
		requestedCompany, err = requireUUID("company_id", requestedCompany)
		if err != nil {
			return StockCountEngine{}, err
		}
	}
	key := strings.TrimSpace(input.IdempotencyKey)
	if key == "" || len(key) > 255 {
		return StockCountEngine{}, fmt.Errorf("%w: post idempotency key is required", identity.ErrValidation)
	}
	payload, _ := json.Marshal(input)
	ph := sha256.Sum256(payload)
	tx, err := s.begin(ctx)
	if err != nil {
		return StockCountEngine{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var companyID, warehouseID, state string
	var version int64
	if requestedCompany != "" {
		err = tx.QueryRow(ctx, `SELECT company_id,warehouse_id,state,version FROM stock_count_engine_counts WHERE company_id=$1 AND id=$2 FOR UPDATE`, requestedCompany, countID).Scan(&companyID, &warehouseID, &state, &version)
	} else {
		err = tx.QueryRow(ctx, `SELECT company_id,warehouse_id,state,version FROM stock_count_engine_counts WHERE id=$1 FOR UPDATE`, countID).Scan(&companyID, &warehouseID, &state, &version)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return StockCountEngine{}, ErrNotFound
	} else if err != nil {
		return StockCountEngine{}, err
	}
	if err = ensureWarehouseAccess(ctx, tx, companyID, actor, warehouseID); err != nil {
		return StockCountEngine{}, err
	}
	var commandHash []byte
	var completed bool
	err = tx.QueryRow(ctx, `SELECT payload_hash,completed_at IS NOT NULL FROM stock_count_engine_commands WHERE company_id=$1 AND count_id=$2 AND command_name='POST' AND idempotency_key=$3 FOR UPDATE`, companyID, countID, key).Scan(&commandHash, &completed)
	if err == nil {
		if !bytes.Equal(commandHash, ph[:]) {
			return StockCountEngine{}, codeError(ErrIdempotencyConflict.Error(), ErrIdempotencyConflict, "same post idempotency key has different payload")
		}
		if completed {
			if err = tx.Commit(ctx); err != nil {
				return StockCountEngine{}, err
			}
			return s.GetStockCountEngine(ctx, companyID, countID, actor)
		}
	} else if errors.Is(err, pgx.ErrNoRows) {
		if _, err = tx.Exec(ctx, `INSERT INTO stock_count_engine_commands(id,company_id,count_id,command_name,idempotency_key,payload_hash,actor_user_id) VALUES($1,$2,$3,'POST',$4,$5,$6)`, uuid.NewString(), companyID, countID, key, ph[:], actor); err != nil {
			return StockCountEngine{}, err
		}
	} else {
		return StockCountEngine{}, err
	}
	if state != StockCountEngineReview {
		return StockCountEngine{}, fmt.Errorf("%w: count must be in review before posting", identity.ErrValidation)
	}
	if input.ExpectedVersion > 0 && input.ExpectedVersion != version {
		return StockCountEngine{}, ErrConflict
	}
	incomplete, err := s.evaluateEngineTx(ctx, tx, companyID, countID, true, actor)
	if err != nil {
		return StockCountEngine{}, err
	}
	if incomplete {
		return StockCountEngine{}, ErrStockCountEngineReviewRequired
	}
	var snapshot time.Time
	if err = tx.QueryRow(ctx, `SELECT snapshot_at FROM stock_count_engine_counts WHERE company_id=$1 AND id=$2`, companyID, countID).Scan(&snapshot); err != nil {
		return StockCountEngine{}, err
	}
	rows, err := tx.Query(ctx, `SELECT id,warehouse_id,product_id,variant_id,location_id,lot_id,serial_id,snapshot_quantity::text FROM stock_count_engine_scopes WHERE company_id=$1 AND count_id=$2 ORDER BY product_id,variant_id NULLS FIRST,warehouse_id,location_id NULLS FIRST,lot_id NULLS FIRST,serial_id NULLS FIRST FOR UPDATE`, companyID, countID)
	if err != nil {
		return StockCountEngine{}, err
	}
	var scopeRows []enginePostScope
	for rows.Next() {
		var row enginePostScope
		if err = rows.Scan(&row.id, &row.warehouseID, &row.productID, &row.variantID, &row.locationID, &row.lotID, &row.serialID, &row.snapshot); err != nil {
			rows.Close()
			return StockCountEngine{}, err
		}
		scopeRows = append(scopeRows, row)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return StockCountEngine{}, err
	}
	events, err := loadEngineEventsTx(ctx, tx, companyID, countID)
	if err != nil {
		return StockCountEngine{}, err
	}
	effective := calculateEngineEffective(events)
	for _, row := range scopeRows {
		current := effective[row.id]
		lockInput := MovementInput{CompanyID: companyID, WarehouseID: row.warehouseID, ProductID: row.productID, VariantID: valueOrEmpty(row.variantID), LocationID: valueOrEmpty(row.locationID), LotID: valueOrEmpty(row.lotID), SerialID: valueOrEmpty(row.serialID)}
		if err = lockStockIdentityTx(ctx, tx, lockInput); err != nil {
			return StockCountEngine{}, err
		}
		expected, difference, reconErr := engineExpectedDifference(ctx, tx, companyID, row.warehouseID, row.productID, row.variantID, row.locationID, row.lotID, row.serialID, snapshot, row.snapshot, current.quantity)
		if reconErr != nil {
			return StockCountEngine{}, reconErr
		}
		if difference == "0" {
			continue
		}
		direction := DirectionIn
		if decimalCompare(difference, "0") < 0 {
			direction = DirectionOut
		}
		movement := MovementInput{ID: uuid.NewString(), CompanyID: companyID, WarehouseID: row.warehouseID, ProductID: row.productID, VariantID: valueOrEmpty(row.variantID), LocationID: valueOrEmpty(row.locationID), LotID: valueOrEmpty(row.lotID), SerialID: valueOrEmpty(row.serialID), MovementType: MovementCountAdjustment, Direction: direction, Quantity: decimalAbs(difference), ReasonCode: "COUNT", ReasonDescription: "Sayım farkı", SourceType: "STOCK_COUNT_ENGINE", SourceID: countID, SourceLineID: row.id, IdempotencyKey: "count-engine:" + countID + ":" + row.id, ActorUserID: actor, Metadata: map[string]any{"snapshot_quantity": row.snapshot, "expected_quantity": expected, "counted_quantity": current.quantity}}
		normalized, normErr := normalizeMovement(movement)
		if normErr != nil {
			return StockCountEngine{}, normErr
		}
		if _, err = postMovementTx(ctx, tx, normalized, movementHash(normalized, false)); err != nil {
			return StockCountEngine{}, mapInventoryError(err)
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE stock_count_engine_counts SET state='POSTED',posted_at=now(),posted_by=$1,version=version+1,updated_at=now() WHERE company_id=$2 AND id=$3 AND version=$4`, actor, companyID, countID, version); err != nil {
		return StockCountEngine{}, mapInventoryError(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE stock_count_engine_commands SET completed_at=now(),result=$1 WHERE company_id=$2 AND count_id=$3 AND command_name='POST' AND idempotency_key=$4`, json.RawMessage(`{"count_id":"`+countID+`"}`), companyID, countID, key); err != nil {
		return StockCountEngine{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return StockCountEngine{}, err
	}
	return s.GetStockCountEngine(ctx, companyID, countID, actor)
}

type enginePostScope struct {
	id, warehouseID, productID             string
	variantID, locationID, lotID, serialID *string
	snapshot                               string
}

func loadEngineEventsTx(ctx context.Context, tx txDB, companyID, countID string) ([]struct {
	scopeID, eventType, quantity string
	recorded                     time.Time
}, error) {
	rows, err := tx.Query(ctx, `SELECT scope_id,event_type,quantity::text,recorded_at FROM stock_count_engine_events WHERE company_id=$1 AND count_id=$2 AND resolution_status='ACCEPTED' ORDER BY event_seq`, companyID, countID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]struct {
		scopeID, eventType, quantity string
		recorded                     time.Time
	}, 0)
	for rows.Next() {
		var e struct {
			scopeID, eventType, quantity string
			recorded                     time.Time
		}
		if err = rows.Scan(&e.scopeID, &e.eventType, &e.quantity, &e.recorded); err != nil {
			return nil, err
		}
		result = append(result, e)
	}
	return result, rows.Err()
}
func engineExpectedDifference(ctx context.Context, tx txDB, companyID, warehouseID, productID string, variantID, locationID, lotID, serialID *string, snapshotAt time.Time, snapshot, counted string) (string, string, error) {
	var movement string
	err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(quantity_delta),0)::text FROM stock_movements WHERE company_id=$1 AND warehouse_id=$2 AND product_id=$3 AND posted_at>$4 AND variant_id IS NOT DISTINCT FROM $5::uuid AND location_id IS NOT DISTINCT FROM $6::uuid AND lot_id IS NOT DISTINCT FROM $7::uuid AND serial_id IS NOT DISTINCT FROM $8::uuid`, companyID, warehouseID, productID, snapshotAt, variantID, locationID, lotID, serialID).Scan(&movement)
	if err != nil {
		return "", "", err
	}
	expected := engineDecimalAdd(snapshot, movement)
	return expected, engineDecimalAdd(counted, "-"+expected), nil
}

func (s *Service) CancelStockCountEngine(ctx context.Context, input StockCountEngineCancelInput) (StockCountEngine, error) {
	countID, err := requireUUID("count_id", input.CountID)
	if err != nil {
		return StockCountEngine{}, err
	}
	actor, err := requireUUID("actor_user_id", input.ActorUserID)
	if err != nil {
		return StockCountEngine{}, err
	}
	requestedCompany := strings.TrimSpace(input.CompanyID)
	if requestedCompany != "" {
		requestedCompany, err = requireUUID("company_id", requestedCompany)
		if err != nil {
			return StockCountEngine{}, err
		}
	}
	if strings.TrimSpace(input.Reason) == "" || strings.TrimSpace(input.IdempotencyKey) == "" {
		return StockCountEngine{}, fmt.Errorf("%w: cancellation reason and idempotency key are required", identity.ErrValidation)
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return StockCountEngine{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var companyID, warehouseID, state string
	var version int64
	if requestedCompany != "" {
		err = tx.QueryRow(ctx, `SELECT company_id,warehouse_id,state,version FROM stock_count_engine_counts WHERE company_id=$1 AND id=$2 FOR UPDATE`, requestedCompany, countID).Scan(&companyID, &warehouseID, &state, &version)
	} else {
		err = tx.QueryRow(ctx, `SELECT company_id,warehouse_id,state,version FROM stock_count_engine_counts WHERE id=$1 FOR UPDATE`, countID).Scan(&companyID, &warehouseID, &state, &version)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return StockCountEngine{}, ErrNotFound
	} else if err != nil {
		return StockCountEngine{}, err
	}
	if err = ensureWarehouseAccess(ctx, tx, companyID, actor, warehouseID); err != nil {
		return StockCountEngine{}, err
	}
	if input.ExpectedVersion > 0 && input.ExpectedVersion != version {
		return StockCountEngine{}, ErrConflict
	}
	ph := sha256.Sum256([]byte(input.CountID + "|" + input.Reason))
	var oldHash []byte
	var done bool
	err = tx.QueryRow(ctx, `SELECT payload_hash,completed_at IS NOT NULL FROM stock_count_engine_commands WHERE company_id=$1 AND count_id=$2 AND command_name='CANCEL' AND idempotency_key=$3 FOR UPDATE`, companyID, countID, input.IdempotencyKey).Scan(&oldHash, &done)
	if err == nil {
		if !bytes.Equal(oldHash, ph[:]) {
			return StockCountEngine{}, ErrIdempotencyConflict
		}
		if done {
			if err = tx.Commit(ctx); err != nil {
				return StockCountEngine{}, err
			}
			return s.GetStockCountEngine(ctx, companyID, countID, actor)
		}
	} else if errors.Is(err, pgx.ErrNoRows) {
		if _, err = tx.Exec(ctx, `INSERT INTO stock_count_engine_commands(id,company_id,count_id,command_name,idempotency_key,payload_hash,actor_user_id) VALUES($1,$2,$3,'CANCEL',$4,$5,$6)`, uuid.NewString(), companyID, countID, input.IdempotencyKey, ph[:], actor); err != nil {
			return StockCountEngine{}, err
		}
	} else {
		return StockCountEngine{}, err
	}
	if state == StockCountEnginePosted || state == StockCountEngineCancelled {
		return StockCountEngine{}, ErrStockCountAlreadyPosted
	}
	if _, err = tx.Exec(ctx, `UPDATE stock_count_engine_counts SET state='CANCELLED',cancelled_at=now(),cancellation_reason=$1,version=version+1,updated_at=now() WHERE company_id=$2 AND id=$3 AND version=$4`, input.Reason, companyID, countID, version); err != nil {
		return StockCountEngine{}, mapInventoryError(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE stock_count_engine_commands SET completed_at=now(),result=$1 WHERE company_id=$2 AND count_id=$3 AND command_name='CANCEL' AND idempotency_key=$4`, json.RawMessage(`{"count_id":"`+countID+`"}`), companyID, countID, input.IdempotencyKey); err != nil {
		return StockCountEngine{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return StockCountEngine{}, err
	}
	return s.GetStockCountEngine(ctx, companyID, countID, actor)
}

func (s *Service) SyncStockCountEngine(ctx context.Context, input StockCountEngineSyncInput) (StockCountEngineSyncResult, error) {
	countID, err := requireUUID("count_id", input.CountID)
	if err != nil {
		return StockCountEngineSyncResult{}, err
	}
	pass := strings.TrimSpace(input.PassID)
	if pass != "" {
		pass, err = requireUUID("pass_id", pass)
		if err != nil {
			return StockCountEngineSyncResult{}, err
		}
	}
	actor := strings.TrimSpace(input.ActorUserID)
	if actor != "" {
		actor, err = requireUUID("actor_user_id", actor)
		if err != nil {
			return StockCountEngineSyncResult{}, err
		}
	}
	requestedCompany := strings.TrimSpace(input.CompanyID)
	if requestedCompany != "" {
		requestedCompany, err = requireUUID("company_id", requestedCompany)
		if err != nil {
			return StockCountEngineSyncResult{}, err
		}
	}
	var companyID, warehouseID string
	if requestedCompany != "" {
		err = s.pool.QueryRow(ctx, `SELECT company_id,warehouse_id FROM stock_count_engine_counts WHERE company_id=$1 AND id=$2`, requestedCompany, countID).Scan(&companyID, &warehouseID)
	} else if actor != "" {
		err = s.pool.QueryRow(ctx, `SELECT company_id,warehouse_id FROM stock_count_engine_counts WHERE id=$1`, countID).Scan(&companyID, &warehouseID)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return StockCountEngineSyncResult{}, ErrNotFound
	}
	if err != nil {
		return StockCountEngineSyncResult{}, err
	}
	if actor != "" {
		if err = ensureWarehouseScope(ctx, s.pool, companyID, actor, warehouseID); err != nil {
			return StockCountEngineSyncResult{}, err
		}
	}
	var args []any
	var query string
	if companyID != "" {
		args = []any{companyID, countID, input.AfterSeq}
		query = `SELECT event_seq,id,event_id,count_id,pass_id,session_id,scope_id,event_type,barcode,resolution_status,quantity::text,recorded_at FROM stock_count_engine_events WHERE company_id=$1 AND count_id=$2 AND event_seq>$3`
	} else {
		args = []any{countID, input.AfterSeq}
		query = `SELECT event_seq,id,event_id,count_id,pass_id,session_id,scope_id,event_type,barcode,resolution_status,quantity::text,recorded_at FROM stock_count_engine_events WHERE count_id=$1 AND event_seq>$2`
	}
	if pass != "" {
		args = append(args, pass)
		query += fmt.Sprintf(" AND pass_id=$%d", len(args))
	}
	query += ` ORDER BY event_seq`
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return StockCountEngineSyncResult{}, err
	}
	defer rows.Close()
	result := StockCountEngineSyncResult{Events: []StockCountEngineEvent{}}
	for rows.Next() {
		var e StockCountEngineEvent
		if err = rows.Scan(&e.EventSeq, &e.ID, &e.EventID, &e.CountID, &e.PassID, &e.SessionID, &e.ScopeID, &e.EventType, &e.Barcode, &e.ResolutionStatus, &e.Quantity, &e.RecordedAt); err != nil {
			return StockCountEngineSyncResult{}, err
		}
		result.Events = append(result.Events, e)
	}
	if err = rows.Err(); err != nil {
		return StockCountEngineSyncResult{}, err
	}
	return result, nil
}

func (s *Service) GetStockCountEngine(ctx context.Context, companyID, countID string, actor ...string) (StockCountEngine, error) {
	companyID, err := requireUUID("company_id", companyID)
	if err != nil {
		return StockCountEngine{}, err
	}
	countID, err = requireUUID("count_id", countID)
	if err != nil {
		return StockCountEngine{}, err
	}
	var item StockCountEngine
	var postedAt, cancelledAt *time.Time
	if err = s.pool.QueryRow(ctx, `SELECT c.id,c.company_id,c.count_no,c.description,c.warehouse_id,w.code,w.name,c.state,c.movement_policy,c.blind_count,c.scope_mode,c.snapshot_at,c.posted_at,c.cancelled_at,c.version
		FROM stock_count_engine_counts c
		JOIN warehouses w ON w.company_id=c.company_id AND w.id=c.warehouse_id
		WHERE c.company_id=$1 AND c.id=$2`, companyID, countID).Scan(&item.ID, &item.CompanyID, &item.CountNo, &item.Description, &item.WarehouseID, &item.WarehouseCode, &item.WarehouseName, &item.State, &item.MovementPolicy, &item.BlindCount, &item.ScopeMode, &item.SnapshotAt, &postedAt, &cancelledAt, &item.Version); errors.Is(err, pgx.ErrNoRows) {
		return StockCountEngine{}, ErrNotFound
	} else if err != nil {
		return StockCountEngine{}, err
	}
	item.StartedAt = item.SnapshotAt
	if item.State == StockCountEnginePosted {
		item.FinishedAt = postedAt
	} else if item.State == StockCountEngineCancelled {
		item.FinishedAt = cancelledAt
	}
	if user := optionalActor(actor); user != "" {
		if err = ensureWarehouseScope(ctx, s.pool, companyID, user, item.WarehouseID); err != nil {
			return StockCountEngine{}, err
		}
	}
	passRows, err := s.pool.Query(ctx, `SELECT id,pass_no,mode,state FROM stock_count_engine_passes WHERE company_id=$1 AND count_id=$2 ORDER BY pass_no`, companyID, countID)
	if err != nil {
		return StockCountEngine{}, err
	}
	for passRows.Next() {
		var pass StockCountEnginePass
		if err = passRows.Scan(&pass.ID, &pass.PassNo, &pass.Mode, &pass.State); err != nil {
			passRows.Close()
			return StockCountEngine{}, err
		}
		item.Passes = append(item.Passes, pass)
	}
	passRows.Close()
	if err = passRows.Err(); err != nil {
		return StockCountEngine{}, err
	}
	scopeRows, err := s.pool.Query(ctx, `SELECT s.id,s.line_no,s.product_id,p.code,p.name,
		COALESCE((SELECT pb.barcode FROM product_barcodes pb WHERE pb.company_id=s.company_id AND pb.product_id=s.product_id AND pb.variant_id IS NOT DISTINCT FROM s.variant_id ORDER BY pb.is_primary DESC,pb.barcode LIMIT 1),''),
		COALESCE((SELECT pu.unit_code FROM product_units pu WHERE pu.company_id=s.company_id AND pu.product_id=s.product_id AND pu.is_base LIMIT 1),''),
		s.variant_id,COALESCE(pv.variant_code,''),s.warehouse_id,s.location_id,s.lot_id,s.serial_id,s.snapshot_quantity::text
		FROM stock_count_engine_scopes s
		JOIN products p ON p.company_id=s.company_id AND p.id=s.product_id
		LEFT JOIN product_variants pv ON pv.company_id=s.company_id AND pv.id=s.variant_id
		WHERE s.company_id=$1 AND s.count_id=$2 ORDER BY s.line_no`, companyID, countID)
	if err != nil {
		return StockCountEngine{}, err
	}
	scopes := []StockCountEngineScope{}
	for scopeRows.Next() {
		var scope StockCountEngineScope
		if err = scopeRows.Scan(&scope.ID, &scope.LineNo, &scope.ProductID, &scope.ProductCode, &scope.ProductName, &scope.Barcode, &scope.UnitCode, &scope.VariantID, &scope.VariantCode, &scope.WarehouseID, &scope.LocationID, &scope.LotID, &scope.SerialID, &scope.SnapshotQuantity); err != nil {
			scopeRows.Close()
			return StockCountEngine{}, err
		}
		if scope.SnapshotQuantity != nil {
			trimmed := trimEngineDecimal(*scope.SnapshotQuantity)
			scope.SnapshotQuantity = &trimmed
		}
		scopes = append(scopes, scope)
	}
	scopeRows.Close()
	if err = scopeRows.Err(); err != nil {
		return StockCountEngine{}, err
	}
	eventRows, err := s.pool.Query(ctx, `SELECT scope_id,event_type,quantity::text,recorded_at FROM stock_count_engine_events WHERE company_id=$1 AND count_id=$2 AND resolution_status='ACCEPTED' ORDER BY event_seq`, companyID, countID)
	if err != nil {
		return StockCountEngine{}, err
	}
	events := []struct {
		scopeID, eventType, quantity string
		recorded                     time.Time
	}{}
	for eventRows.Next() {
		var e struct {
			scopeID, eventType, quantity string
			recorded                     time.Time
		}
		if err = eventRows.Scan(&e.scopeID, &e.eventType, &e.quantity, &e.recorded); err != nil {
			eventRows.Close()
			return StockCountEngine{}, err
		}
		events = append(events, e)
	}
	eventRows.Close()
	if err = eventRows.Err(); err != nil {
		return StockCountEngine{}, err
	}
	effective := calculateEngineEffective(events)
	var activePassMode string
	if err = s.pool.QueryRow(ctx, `SELECT mode FROM stock_count_engine_passes WHERE company_id=$1 AND count_id=$2 AND state='IN_PROGRESS' ORDER BY pass_no DESC LIMIT 1`, companyID, countID).Scan(&activePassMode); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return StockCountEngine{}, err
	}
	// New counts are always open.  Do not hide the system quantity even if a
	// legacy row still carries the former blind flag or pass mode.
	_ = activePassMode
	for i := range scopes {
		current, ok := effective[scopes[i].ID]
		scopes[i].HasResponse = ok
		if ok {
			scopes[i].CountedQuantity = &current.quantity
		}
		var expected string
		if err = s.pool.QueryRow(ctx, `SELECT COALESCE(SUM(quantity_delta),0)::text FROM stock_movements WHERE company_id=$1 AND warehouse_id=$2 AND product_id=$3 AND posted_at>$4 AND variant_id IS NOT DISTINCT FROM $5::uuid AND location_id IS NOT DISTINCT FROM $6::uuid AND lot_id IS NOT DISTINCT FROM $7::uuid AND serial_id IS NOT DISTINCT FROM $8::uuid`, companyID, scopes[i].WarehouseID, scopes[i].ProductID, item.SnapshotAt, scopes[i].VariantID, scopes[i].LocationID, scopes[i].LotID, scopes[i].SerialID).Scan(&expected); err != nil {
			return StockCountEngine{}, err
		}
		expected = engineDecimalAdd(valueOrEmpty(scopes[i].SnapshotQuantity), expected)
		difference := engineDecimalAdd(valueOrEmpty(scopes[i].CountedQuantity), "-"+expected)
		scopes[i].ExpectedQuantity = &expected
		scopes[i].Difference = &difference
		if !ok {
			scopes[i].Difference = nil
		}
	}
	item.Scopes = scopes
	exceptionRows, err := s.pool.Query(ctx, `SELECT id,scope_id,exception_type,status,details FROM stock_count_engine_review_exceptions WHERE company_id=$1 AND count_id=$2 ORDER BY created_at,id`, companyID, countID)
	if err != nil {
		return StockCountEngine{}, err
	}
	for exceptionRows.Next() {
		var exception StockCountEngineException
		var details []byte
		if err = exceptionRows.Scan(&exception.ID, &exception.ScopeID, &exception.ExceptionType, &exception.Status, &details); err != nil {
			exceptionRows.Close()
			return StockCountEngine{}, err
		}
		exception.Details = map[string]any{}
		_ = json.Unmarshal(details, &exception.Details)
		item.Exceptions = append(item.Exceptions, exception)
	}
	exceptionRows.Close()
	return item, exceptionRows.Err()
}

type StockCountEngineListResult struct {
	Items      []StockCountEngine `json:"items"`
	NextCursor string             `json:"next_cursor,omitempty"`
}

func (s *Service) ListStockCountEngines(ctx context.Context, companyID, state string, limit int, actor string, from, to *time.Time, cursor string) (StockCountEngineListResult, error) {
	companyID, err := requireUUID("company_id", companyID)
	if err != nil {
		return StockCountEngineListResult{}, err
	}
	if limit < 1 || limit > 100 {
		limit = 50
	}
	args := []any{companyID}
	query := `SELECT c.id,c.snapshot_at FROM stock_count_engine_counts c JOIN warehouses w ON w.company_id=c.company_id AND w.id=c.warehouse_id WHERE c.company_id=$1 AND w.is_active AND w.warehouse_type='STANDARD'`
	if state = strings.ToUpper(strings.TrimSpace(state)); state != "" {
		args = append(args, state)
		query += fmt.Sprintf(" AND c.state=$%d", len(args))
	}
	if from != nil {
		args = append(args, from.UTC())
		query += fmt.Sprintf(" AND c.snapshot_at >= $%d", len(args))
	}
	if to != nil {
		args = append(args, to.UTC())
		query += fmt.Sprintf(" AND c.snapshot_at <= $%d", len(args))
	}
	if cursor != "" {
		lastSnapshot, lastID, decodeErr := decodeStockCountCursor(cursor)
		if decodeErr != nil {
			return StockCountEngineListResult{}, fmt.Errorf("%w: sayım listesi cursor bilgisi geçersiz", identity.ErrValidation)
		}
		args = append(args, lastSnapshot, lastID)
		query += fmt.Sprintf(" AND (c.snapshot_at,c.id) < ($%d,$%d::uuid)", len(args)-1, len(args))
	}
	args = append(args, limit+1)
	query += fmt.Sprintf(" ORDER BY c.snapshot_at DESC,c.id DESC LIMIT $%d", len(args))
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return StockCountEngineListResult{}, err
	}
	defer rows.Close()
	ids := make([]string, 0)
	snapshots := make(map[string]time.Time)
	for rows.Next() {
		var id string
		var snapshot time.Time
		if err = rows.Scan(&id, &snapshot); err != nil {
			return StockCountEngineListResult{}, err
		}
		ids = append(ids, id)
		snapshots[id] = snapshot
	}
	if err = rows.Err(); err != nil {
		return StockCountEngineListResult{}, err
	}
	items := make([]StockCountEngine, 0, len(ids))
	for _, id := range ids {
		item, getErr := s.GetStockCountEngine(ctx, companyID, id, actor)
		if getErr != nil {
			if errors.Is(getErr, identity.ErrForbidden) || errors.Is(getErr, ErrNotFound) {
				continue
			}
			return StockCountEngineListResult{}, getErr
		}
		items = append(items, item)
	}
	result := StockCountEngineListResult{Items: items}
	if len(items) > limit {
		result.Items = items[:limit]
		last := result.Items[len(result.Items)-1]
		result.NextCursor = encodeStockCountCursor(snapshots[last.ID], last.ID)
	}
	return result, nil
}

func encodeStockCountCursor(snapshot time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(snapshot.UTC().Format(time.RFC3339Nano) + "|" + id))
}

func decodeStockCountCursor(value string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return time.Time{}, "", err
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 2 || strings.TrimSpace(parts[1]) == "" {
		return time.Time{}, "", fmt.Errorf("invalid cursor")
	}
	snapshot, err := time.Parse(time.RFC3339Nano, parts[0])
	return snapshot, parts[1], err
}

func (s *Service) CurrentStockCountPassID(ctx context.Context, companyID, countID, sessionID string) (string, error) {
	companyID, err := requireUUID("company_id", companyID)
	if err != nil {
		return "", err
	}
	countID, err = requireUUID("count_id", countID)
	if err != nil {
		return "", err
	}
	var id string
	if strings.TrimSpace(sessionID) != "" {
		sessionID, err = requireUUID("session_id", sessionID)
		if err != nil {
			return "", err
		}
		err = s.pool.QueryRow(ctx, `SELECT pass_id FROM stock_count_engine_sessions WHERE company_id=$1 AND count_id=$2 AND id=$3 AND closed_at IS NULL`, companyID, countID, sessionID).Scan(&id)
	} else {
		err = s.pool.QueryRow(ctx, `SELECT id FROM stock_count_engine_passes WHERE company_id=$1 AND count_id=$2 AND state='IN_PROGRESS' ORDER BY pass_no DESC LIMIT 1`, companyID, countID).Scan(&id)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return id, err
}

func (s *Service) StartCountEngine(ctx context.Context, input StockCountEngineStartInput) (StockCountEngine, error) {
	return s.StartStockCountEngine(ctx, input)
}
