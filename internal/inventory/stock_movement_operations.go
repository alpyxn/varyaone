package inventory

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const stockMovementOperationSourceType = "STOCK_MOVEMENT_OPERATION"

// PostStockMovementOperation posts every line of a manual multi-variant
// operation in one transaction. The existing PostMovement command remains
// the single-line primitive used by documents and transfer workflows.
func (s *Service) PostStockMovementOperation(ctx context.Context, input StockMovementOperationInput) (StockMovementOperation, error) {
	normalized, lines, payloadHash, err := normalizeStockMovementOperation(input)
	if err != nil {
		return StockMovementOperation{}, err
	}

	tx, err := s.begin(ctx)
	if err != nil {
		return StockMovementOperation{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if err = ensureWarehouseAccess(ctx, tx, normalized.CompanyID, normalized.ActorUserID, normalized.WarehouseID); err != nil {
		return StockMovementOperation{}, err
	}

	inserted, err := reserveStockMovementOperation(ctx, tx, normalized, payloadHash)
	if err != nil {
		return StockMovementOperation{}, err
	}
	if !inserted {
		result, loadErr := loadStockMovementOperationTx(ctx, tx, normalized.CompanyID, normalized.IdempotencyKey)
		if loadErr != nil {
			return StockMovementOperation{}, loadErr
		}
		if err = tx.Commit(ctx); err != nil {
			return StockMovementOperation{}, err
		}
		return result, nil
	}

	// Validate every variant before inserting any ledger row. The loop is
	// already sorted by variant id, so all concurrent commands acquire variant
	// and position locks in the same order.
	for _, line := range lines {
		if _, err = validateInventoryVariantTx(ctx, tx, normalized.CompanyID, normalized.ProductID, line.VariantID); err != nil {
			return StockMovementOperation{}, err
		}
	}

	warehouseInput := MovementInput{
		CompanyID: normalized.CompanyID, WarehouseID: normalized.WarehouseID, ProductID: normalized.ProductID,
		MovementType: normalized.MovementType, Direction: normalized.Direction,
		ReasonCode: normalized.ReasonCode, ReasonDescription: normalized.ReasonDescription,
		SourceType: stockMovementOperationSourceType, SourceID: normalized.ID,
		ActorUserID: normalized.ActorUserID,
	}
	if err = validateWarehouseMovementTx(ctx, tx, warehouseInput); err != nil {
		return StockMovementOperation{}, err
	}

	for index, line := range lines {
		variant, variantErr := validateInventoryVariantTx(ctx, tx, normalized.CompanyID, normalized.ProductID, line.VariantID)
		if variantErr != nil {
			return StockMovementOperation{}, variantErr
		}
		movement := MovementInput{
			ID: uuid.NewString(), CompanyID: normalized.CompanyID, WarehouseID: normalized.WarehouseID,
			ProductID: normalized.ProductID, VariantID: line.VariantID,
			MovementType: normalized.MovementType, Direction: normalized.Direction,
			Quantity: line.Quantity, EnteredQuantity: line.Quantity, UnitCode: normalized.UnitCode,
			UnitCost: line.UnitCost, Currency: normalized.Currency,
			ReasonCode: normalized.ReasonCode, ReasonDescription: normalized.ReasonDescription,
			SourceType: stockMovementOperationSourceType, SourceID: normalized.ID, SourceLineID: line.ID,
			IdempotencyKey: operationLineIdempotencyKey(normalized.IdempotencyKey, line.ID),
			ActorUserID:    normalized.ActorUserID,
			Metadata: map[string]any{
				"operation_id": normalized.ID, "operation_line_id": line.ID,
				"operation_line_no": index + 1, "unit_code": normalized.UnitCode,
			},
		}
		normalizedMovement, normalizeErr := normalizeMovement(movement)
		if normalizeErr != nil {
			return StockMovementOperation{}, normalizeErr
		}
		if err = applyUnitConversionTx(ctx, tx, &normalizedMovement); err != nil {
			return StockMovementOperation{}, err
		}
		posted, postErr := postStockMovementOperationLineTx(ctx, tx, normalizedMovement)
		if postErr != nil {
			return StockMovementOperation{}, mapInventoryError(postErr)
		}
		enteredQuantity, baseQuantity := operationLineQuantities(normalizedMovement)
		if _, err = tx.Exec(ctx, `INSERT INTO stock_movement_operation_lines(
			id,company_id,operation_id,line_no,variant_id,variant_code,variant_display,
			quantity,base_quantity,unit_cost,movement_id)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
			line.ID, normalized.CompanyID, normalized.ID, index+1, variant.ID, variant.Code,
			variantDisplayJSON(variant.Display), enteredQuantity, baseQuantity,
			nullableText(line.UnitCost), posted.ID); err != nil {
			return StockMovementOperation{}, err
		}
	}

	result, err := loadStockMovementOperationTx(ctx, tx, normalized.CompanyID, normalized.IdempotencyKey)
	if err != nil {
		return StockMovementOperation{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return StockMovementOperation{}, err
	}
	return result, nil
}

func operationLineQuantities(movement MovementInput) (string, string) {
	if strings.TrimSpace(movement.EnteredQuantity) != "" {
		return movement.EnteredQuantity, movement.Quantity
	}
	return movement.Quantity, movement.Quantity
}

// CreateStockMovementOperation is a descriptive alias for command handlers.
func (s *Service) CreateStockMovementOperation(ctx context.Context, input StockMovementOperationInput) (StockMovementOperation, error) {
	return s.PostStockMovementOperation(ctx, input)
}

func normalizeStockMovementOperation(input StockMovementOperationInput) (StockMovementOperationInput, []StockMovementOperationLine, []byte, error) {
	input.CompanyID = strings.TrimSpace(input.CompanyID)
	if _, err := requireUUID("company_id", input.CompanyID); err != nil {
		return StockMovementOperationInput{}, nil, nil, err
	}
	var err error
	if input.WarehouseID, err = requireUUID("warehouse_id", input.WarehouseID); err != nil {
		return StockMovementOperationInput{}, nil, nil, err
	}
	if input.ProductID, err = requireUUID("product_id", input.ProductID); err != nil {
		return StockMovementOperationInput{}, nil, nil, err
	}
	if input.ID == "" {
		input.ID = uuid.NewString()
	}
	if input.ID, err = requireUUID("id", input.ID); err != nil {
		return StockMovementOperationInput{}, nil, nil, err
	}
	input.MovementType = strings.ToUpper(strings.TrimSpace(input.MovementType))
	if input.MovementType == "" {
		input.MovementType = MovementManualAdjustment
	}
	if input.MovementType != MovementManualAdjustment {
		return StockMovementOperationInput{}, nil, nil, fmt.Errorf("%w: toplu operasyon yalnız manuel stok hareketi için kullanılabilir", identity.ErrValidation)
	}
	input.Direction = strings.ToUpper(strings.TrimSpace(input.Direction))
	if input.Direction != DirectionIn && input.Direction != DirectionOut {
		return StockMovementOperationInput{}, nil, nil, fmt.Errorf("%w: yön IN veya OUT olmalıdır", identity.ErrValidation)
	}
	input.UnitCode = strings.ToUpper(strings.TrimSpace(input.UnitCode))
	input.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))
	if input.Currency != "" && (len(input.Currency) != 3 || input.Currency < "A" || input.Currency > "ZZZ") {
		return StockMovementOperationInput{}, nil, nil, fmt.Errorf("%w: para birimi üç harfli olmalıdır", identity.ErrValidation)
	}
	input.ReasonCode = strings.ToUpper(strings.TrimSpace(input.ReasonCode))
	input.ReasonDescription = strings.TrimSpace(input.ReasonDescription)
	if input.ReasonCode == "" {
		return StockMovementOperationInput{}, nil, nil, codeError(ErrInvalidReason.Error(), ErrInvalidReason, "manuel hareket için reason code gereklidir")
	}
	if input.ReasonCode == "OTHER" && input.ReasonDescription == "" {
		return StockMovementOperationInput{}, nil, nil, codeError(ErrInvalidReason.Error(), ErrInvalidReason, "Diğer seçimi için açıklama gereklidir")
	}
	if !validManualReason(input.Direction, input.ReasonCode) {
		return StockMovementOperationInput{}, nil, nil, codeError(ErrInvalidReason.Error(), ErrInvalidReason, "giriş ve çıkış için hareket nedeni uyumsuz")
	}
	if len(input.Lines) == 0 {
		return StockMovementOperationInput{}, nil, nil, fmt.Errorf("%w: en az bir varyant satırı gereklidir", identity.ErrValidation)
	}
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		input.IdempotencyKey = "stock-operation:" + input.ID
	}
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if len(input.IdempotencyKey) > 255 {
		return StockMovementOperationInput{}, nil, nil, fmt.Errorf("%w: idempotency anahtarı çok uzun", identity.ErrValidation)
	}
	if input.ActorUserID != "" {
		if input.ActorUserID, err = requireUUID("actor_user_id", input.ActorUserID); err != nil {
			return StockMovementOperationInput{}, nil, nil, err
		}
	}

	lines := append([]StockMovementOperationLine(nil), input.Lines...)
	seen := make(map[string]struct{}, len(lines))
	for i := range lines {
		if lines[i].VariantID, err = requireUUID("variant_id", lines[i].VariantID); err != nil {
			return StockMovementOperationInput{}, nil, nil, err
		}
		if _, exists := seen[lines[i].VariantID]; exists {
			return StockMovementOperationInput{}, nil, nil, fmt.Errorf("%w: aynı varyant operasyon içinde iki kez kullanılamaz", identity.ErrValidation)
		}
		seen[lines[i].VariantID] = struct{}{}
		if lines[i].Quantity, err = cleanQuantity("quantity", lines[i].Quantity, true); err != nil {
			return StockMovementOperationInput{}, nil, nil, err
		}
		if strings.TrimSpace(lines[i].UnitCost) != "" {
			if lines[i].UnitCost, err = cleanQuantity("unit_cost", lines[i].UnitCost, false); err != nil {
				return StockMovementOperationInput{}, nil, nil, err
			}
		}
		if input.Direction == DirectionOut && lines[i].UnitCost != "" {
			return StockMovementOperationInput{}, nil, nil, fmt.Errorf("%w: çıkış maliyeti değerleme motoru tarafından hesaplanır", identity.ErrValidation)
		}
		if lines[i].ID == "" {
			lines[i].ID = uuid.NewString()
		}
		if lines[i].ID, err = requireUUID("line_id", lines[i].ID); err != nil {
			return StockMovementOperationInput{}, nil, nil, err
		}
	}
	sort.Slice(lines, func(i, j int) bool { return lines[i].VariantID < lines[j].VariantID })
	for i := range lines {
		for j := 0; j < i; j++ {
			if lines[i].ID == lines[j].ID {
				return StockMovementOperationInput{}, nil, nil, fmt.Errorf("%w: operasyon satır kimlikleri benzersiz olmalıdır", identity.ErrValidation)
			}
		}
	}
	canonical := input
	canonical.IdempotencyKey = ""
	canonical.ID = ""
	canonical.Lines = append([]StockMovementOperationLine(nil), lines...)
	for i := range canonical.Lines {
		canonical.Lines[i].ID = ""
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return StockMovementOperationInput{}, nil, nil, err
	}
	hash := sha256.Sum256(encoded)
	return input, lines, hash[:], nil
}

func reserveStockMovementOperation(ctx context.Context, tx pgx.Tx, input StockMovementOperationInput, payloadHash []byte) (bool, error) {
	result, err := tx.Exec(ctx, `INSERT INTO stock_movement_operations(
		id,company_id,warehouse_id,product_id,movement_type,direction,unit_code,currency,
		reason_code,reason_description,idempotency_key,payload_hash,actor_user_id)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		ON CONFLICT (company_id,idempotency_key) DO NOTHING`,
		input.ID, input.CompanyID, input.WarehouseID, input.ProductID, input.MovementType, input.Direction,
		input.UnitCode, nullableText(input.Currency), input.ReasonCode, input.ReasonDescription,
		input.IdempotencyKey, payloadHash, nullableText(input.ActorUserID))
	if err != nil {
		return false, mapInventoryError(err)
	}
	if result.RowsAffected() == 1 {
		return true, nil
	}
	var persisted []byte
	err = tx.QueryRow(ctx, `SELECT payload_hash FROM stock_movement_operations WHERE company_id=$1 AND idempotency_key=$2 FOR UPDATE`, input.CompanyID, input.IdempotencyKey).Scan(&persisted)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, err
	}
	if !bytes.Equal(persisted, payloadHash) {
		return false, codeError(ErrIdempotencyConflict.Error(), ErrIdempotencyConflict, "aynı Idempotency-Key farklı içerikle kullanıldı")
	}
	return false, nil
}

func postStockMovementOperationLineTx(ctx context.Context, tx pgx.Tx, movement MovementInput) (Movement, error) {
	return postMovementTx(ctx, tx, movement, movementHash(movement, false))
}

func loadStockMovementOperationTx(ctx context.Context, tx txDB, companyID, idempotencyKey string) (StockMovementOperation, error) {
	var result StockMovementOperation
	var currency, actor *string
	if err := tx.QueryRow(ctx, `SELECT id,company_id,warehouse_id,product_id,movement_type,direction,unit_code,currency,reason_code,reason_description,idempotency_key,actor_user_id,posted_at
		FROM stock_movement_operations WHERE company_id=$1 AND idempotency_key=$2`, companyID, idempotencyKey).Scan(
		&result.ID, &result.CompanyID, &result.WarehouseID, &result.ProductID, &result.MovementType, &result.Direction,
		&result.UnitCode, &currency, &result.ReasonCode, &result.ReasonDescription, &result.IdempotencyKey, &actor, &result.PostedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return StockMovementOperation{}, ErrNotFound
		}
		return StockMovementOperation{}, err
	}
	result.Currency = valueOrEmpty(currency)
	result.ActorUserID = actor
	if actor != nil {
		if nameErr := tx.QueryRow(ctx, `SELECT COALESCE(display_name,email,'') FROM users WHERE id=$1`, *actor).Scan(&result.ActorName); nameErr != nil && !errors.Is(nameErr, pgx.ErrNoRows) {
			return StockMovementOperation{}, nameErr
		}
	}
	rows, err := tx.Query(ctx, `SELECT id,line_no,movement_id,variant_id,variant_code,variant_display,quantity::text,base_quantity::text,unit_cost::text
		FROM stock_movement_operation_lines WHERE company_id=$1 AND operation_id=$2 ORDER BY line_no`, result.CompanyID, result.ID)
	if err != nil {
		return StockMovementOperation{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var line StockMovementOperationResult
		var display []byte
		var unitCost *string
		if err = rows.Scan(&line.ID, &line.LineNo, &line.MovementID, &line.VariantID, &line.VariantCode, &display, &line.Quantity, &line.BaseQuantity, &unitCost); err != nil {
			return StockMovementOperation{}, err
		}
		line.VariantDisplay = decodeVariantDisplay(display)
		line.UnitCost = unitCost
		if currency != nil {
			value := *currency
			line.Currency = &value
		}
		result.Lines = append(result.Lines, line)
	}
	if err = rows.Err(); err != nil {
		return StockMovementOperation{}, err
	}
	return result, nil
}

func operationLineIdempotencyKey(operationKey, lineID string) string {
	key := "stock-operation-line:" + operationKey + ":" + lineID
	if len(key) <= 255 {
		return key
	}
	// The operation key is already bounded by the operation table. Hashing only
	// the overlong suffix keeps line keys deterministic and within the ledger
	// limit without weakening operation-level idempotency.
	digest := sha256.Sum256([]byte(key))
	return "stock-operation-line:" + fmt.Sprintf("%x", digest[:])
}

func variantDisplayJSON(display map[string]any) []byte {
	if len(display) == 0 {
		return []byte(`{}`)
	}
	encoded, err := json.Marshal(display)
	if err != nil {
		return []byte(`{}`)
	}
	return encoded
}

func nullableText(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}
