package inventory

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

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/money"
	"github.com/alpyxn/varyaone/internal/platform/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type Service struct{ pool database.Querier }

// NewService constructs the inventory service.  All command methods below
// use the pool only as a transaction boundary; posted ledger rows and their
// projections are committed together.
func NewService(pool database.Querier) *Service { return &Service{pool: pool} }

type txDB interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (s *Service) begin(ctx context.Context) (pgx.Tx, error) {
	if s == nil || s.pool == nil {
		return nil, errors.New("inventory service database is not configured")
	}
	return s.pool.Begin(ctx)
}

// ensureWarehouseAccess applies the same optional branch/warehouse scope
// rule used by the catalog. A membership with no scope rows is unrestricted;
// once a scope exists, every warehouse operation must match it.
func ensureWarehouseAccess(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, companyID, userID, warehouseID string) error {
	if strings.TrimSpace(userID) == "" {
		return identity.ErrForbidden
	}
	var allowed bool
	err := q.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM warehouses w
		WHERE w.company_id=$1 AND w.id=$2 AND w.is_active
		  AND (w.is_system OR (
				(w.branch_id IS NULL OR NOT EXISTS(SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=$1 AND bs.user_id=$3)
				 OR EXISTS(SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=$1 AND bs.user_id=$3 AND bs.branch_id=w.branch_id))
				AND (NOT EXISTS(SELECT 1 FROM membership_warehouse_scopes ws WHERE ws.company_id=$1 AND ws.user_id=$3)
				 OR EXISTS(SELECT 1 FROM membership_warehouse_scopes ws WHERE ws.company_id=$1 AND ws.user_id=$3 AND ws.warehouse_id=w.id))
		  ))
	)`, companyID, warehouseID, userID).Scan(&allowed)
	if err != nil {
		return err
	}
	if !allowed {
		return identity.ErrForbidden
	}
	return nil
}

// ensureWarehouseScope is the read-side variant used for immutable history.
// A passive warehouse is out of operation scope, but its historical rows are
// still company/branch/warehouse-authorized data.
func ensureWarehouseScope(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, companyID, userID, warehouseID string) error {
	if strings.TrimSpace(userID) == "" {
		return identity.ErrForbidden
	}
	var allowed bool
	err := q.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM warehouses w
		WHERE w.company_id=$1 AND w.id=$2
		  AND (w.is_system OR (
				(w.branch_id IS NULL OR NOT EXISTS(SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=$1 AND bs.user_id=$3)
				 OR EXISTS(SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=$1 AND bs.user_id=$3 AND bs.branch_id=w.branch_id))
				AND (NOT EXISTS(SELECT 1 FROM membership_warehouse_scopes ws WHERE ws.company_id=$1 AND ws.user_id=$3)
				 OR EXISTS(SELECT 1 FROM membership_warehouse_scopes ws WHERE ws.company_id=$1 AND ws.user_id=$3 AND ws.warehouse_id=w.id))
		  ))
	)`, companyID, warehouseID, userID).Scan(&allowed)
	if err != nil {
		return err
	}
	if !allowed {
		return identity.ErrForbidden
	}
	return nil
}

// ensureVisibleWarehouse is deliberately stricter than the internal access
// checks. System transit is usable by transfer commands, but it is not a
// warehouse or movement resource for callers.
func ensureVisibleWarehouse(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, companyID, userID, warehouseID string) error {
	var system bool
	if err := q.QueryRow(ctx, `SELECT is_system FROM warehouses WHERE company_id=$1 AND id=$2`, companyID, warehouseID).Scan(&system); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	} else if system {
		return ErrNotFound
	}
	return ensureWarehouseScope(ctx, q, companyID, userID, warehouseID)
}

// warehouseIsUsable is the single stock-balance rule. A warehouse can retain
// ledger history and even receive controlled stock while still being absent
// from ordinary stock availability.
func warehouseIsUsable(warehouseType string, active bool) bool {
	return active && warehouseType == WarehouseStandard
}

// warehouseAllowsMovement contains the direction/type part of the warehouse
// policy without database access, which keeps the invariant easy to test.
// Active non-standard warehouses may receive stock. Their outbound rows are
// limited to transit transfer resolution and immutable reversals.
func warehouseAllowsMovement(warehouseType string, active bool, movementType, direction string, controlledSystem, reversal bool) error {
	if !active {
		return codeError(ErrWarehouseInactive.Error(), ErrWarehouseInactive, "pasif depoda yeni hareket oluşturulamaz")
	}
	if (movementType == MovementTransferOut || movementType == MovementWaste) && direction != DirectionOut {
		return codeError(ErrWarehouseMovementNotAllowed.Error(), ErrWarehouseMovementNotAllowed, "bu hareket türü yalnızca çıkış yönünde kullanılabilir")
	}
	if movementType == MovementTransferIn && direction != DirectionIn {
		return codeError(ErrWarehouseMovementNotAllowed.Error(), ErrWarehouseMovementNotAllowed, "bu hareket türü yalnızca giriş yönünde kullanılabilir")
	}
	if movementType == MovementTransferOut && !controlledSystem {
		return codeError(ErrWarehouseMovementNotAllowed.Error(), ErrWarehouseMovementNotAllowed, "transfer çıkışı yalnızca kontrollü transfer akışında yapılabilir")
	}
	if warehouseType == WarehouseTransit && !controlledSystem {
		return codeError(ErrWarehouseSystem.Error(), ErrWarehouseSystem, "sistem transit deposuna yalnız transfer akışı dokunabilir")
	}
	if direction == DirectionIn || warehouseType == WarehouseStandard {
		return nil
	}
	if controlledSystem && (warehouseType == WarehouseTransit || (reversal && movementType == MovementReconciliation)) {
		return nil
	}
	return codeError(ErrWarehouseMovementNotAllowed.Error(), ErrWarehouseMovementNotAllowed, "özel depodan bu yönde hareket yapılamaz")
}

func ensureStandardWarehouse(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, companyID, userID, warehouseID string) error {
	if err := ensureWarehouseAccess(ctx, q, companyID, userID, warehouseID); err != nil {
		return err
	}
	var warehouseType string
	var active bool
	if err := q.QueryRow(ctx, `SELECT warehouse_type,is_active FROM warehouses WHERE company_id=$1 AND id=$2 FOR UPDATE`, companyID, warehouseID).Scan(&warehouseType, &active); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if !active {
		return codeError(ErrWarehouseInactive.Error(), ErrWarehouseInactive, "pasif depoda bu işlem yapılamaz")
	}
	if !warehouseIsUsable(warehouseType, active) {
		return codeError(ErrWarehouseNotStandard.Error(), ErrWarehouseNotStandard, "bu işlem yalnız aktif normal depoda yapılabilir")
	}
	return nil
}

// validateWarehouseMovementTx is called inside the posting transaction so a
// concurrent warehouse deactivation cannot race a movement insert.
func validateWarehouseMovementTx(ctx context.Context, tx txDB, input MovementInput) error {
	var warehouseType string
	var active bool
	if err := tx.QueryRow(ctx, `SELECT warehouse_type,is_active FROM warehouses WHERE company_id=$1 AND id=$2 FOR UPDATE`, input.CompanyID, input.WarehouseID).Scan(&warehouseType, &active); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}

	controlledSystem := false
	if input.SourceType == "WAREHOUSE_TRANSFER" && (input.MovementType == MovementTransferIn || input.MovementType == MovementTransferOut || input.MovementType == MovementWaste) {
		if err := tx.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM warehouse_transfers
			WHERE company_id=$1 AND id=$2 AND transit_warehouse_id=$3
		)`, input.CompanyID, input.SourceID, input.WarehouseID).Scan(&controlledSystem); err != nil {
			return err
		}
	}
	if input.SourceType == "WAREHOUSE_TRANSFER" && input.MovementType == MovementTransferOut && warehouseType == WarehouseStandard {
		var sourceControlled bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM warehouse_transfers
			WHERE company_id=$1 AND id=$2 AND source_warehouse_id=$3
		)`, input.CompanyID, input.SourceID, input.WarehouseID).Scan(&sourceControlled); err != nil {
			return err
		}
		controlledSystem = controlledSystem || sourceControlled
	}
	if input.ReversalOfID != "" && input.MovementType == MovementReconciliation {
		var originalDirection string
		if err := tx.QueryRow(ctx, `SELECT direction FROM stock_movements WHERE company_id=$1 AND id=$2 AND warehouse_id=$3`, input.CompanyID, input.ReversalOfID, input.WarehouseID).Scan(&originalDirection); err == nil {
			controlledSystem = controlledSystem || originalDirection == DirectionIn
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
	}
	return warehouseAllowsMovement(warehouseType, active, input.MovementType, input.Direction, controlledSystem, input.ReversalOfID != "")
}

func optionalActor(userIDs []string) string {
	if len(userIDs) == 0 {
		return ""
	}
	return strings.TrimSpace(userIDs[0])
}

// ensureTransferAccess locks the transfer's warehouse references through the
// same company/scope predicate used by movement commands. A transfer is
// visible or mutable only when the caller can access both physical endpoints.
func ensureTransferAccess(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, companyID, transferID, userID string) error {
	if userID == "" {
		return identity.ErrForbidden
	}
	var sourceID, destinationID, transitID string
	if err := q.QueryRow(ctx, `SELECT source_warehouse_id,destination_warehouse_id,transit_warehouse_id FROM warehouse_transfers WHERE company_id=$1 AND id=$2`, companyID, transferID).Scan(&sourceID, &destinationID, &transitID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if err := ensureStandardWarehouse(ctx, q, companyID, userID, sourceID); err != nil {
		return err
	}
	if err := ensureWarehouseAccess(ctx, q, companyID, userID, destinationID); err != nil {
		return err
	}
	var destinationType string
	if err := q.QueryRow(ctx, `SELECT warehouse_type FROM warehouses WHERE company_id=$1 AND id=$2`, companyID, destinationID).Scan(&destinationType); err != nil {
		return err
	}
	if destinationType == WarehouseTransit {
		return codeError(ErrWarehouseNotStandard.Error(), ErrWarehouseNotStandard, "transfer hedefi sistem transit deposu olamaz")
	}
	var transitValid bool
	if err := q.QueryRow(ctx, `SELECT is_active AND is_transit AND is_system AND warehouse_type='TRANSIT' FROM warehouses WHERE company_id=$1 AND id=$2`, companyID, transitID).Scan(&transitValid); err != nil {
		return err
	}
	if !transitValid {
		return codeError(ErrWarehouseSystem.Error(), ErrWarehouseSystem, "geçerli sistem transit deposu bulunamadı")
	}
	return nil
}

// ensureTransferReadAccess keeps historical transfer rows readable after a
// warehouse is made passive. Mutating commands continue to use
// ensureTransferAccess, which deliberately requires active warehouses.
func ensureTransferReadAccess(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, companyID, transferID, userID string) error {
	if userID == "" {
		return identity.ErrForbidden
	}
	var sourceID, destinationID, transitID string
	if err := q.QueryRow(ctx, `SELECT source_warehouse_id,destination_warehouse_id,transit_warehouse_id FROM warehouse_transfers WHERE company_id=$1 AND id=$2`, companyID, transferID).Scan(&sourceID, &destinationID, &transitID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if err := ensureWarehouseScope(ctx, q, companyID, userID, sourceID); err != nil {
		return err
	}
	if err := ensureWarehouseScope(ctx, q, companyID, userID, destinationID); err != nil {
		return err
	}
	return ensureWarehouseScope(ctx, q, companyID, userID, transitID)
}

func requireUUID(name, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", validationError(fmt.Sprintf("%s is required", name))
	}
	id, err := uuid.Parse(value)
	if err != nil {
		return "", validationError(fmt.Sprintf("%s is invalid: %v", name, err))
	}
	return id.String(), nil
}

func optionalUUID(value string) (any, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	id, err := uuid.Parse(value)
	if err != nil {
		return nil, validationError(fmt.Sprintf("UUID is invalid: %v", err))
	}
	return id.String(), nil
}

func cleanQuantity(name, value string, positive bool) (string, error) {
	parsed, err := money.ParseDecimal(strings.TrimSpace(value), 8)
	if err != nil || (positive && parsed.Sign() <= 0) || (!positive && parsed.Sign() < 0) {
		return "", validationError(fmt.Sprintf("%s geçerli bir miktar olmalıdır", name))
	}
	return parsed.String(), nil
}

func validationError(message string) error {
	return fmt.Errorf("%w: %s", identity.ErrValidation, message)
}

func validMovementType(value string) bool {
	switch value {
	case MovementPurchaseReceipt, MovementSalesDispatch, MovementSalesReturn,
		MovementPurchaseReturn, MovementTransferOut, MovementTransferIn,
		MovementCountAdjustment, MovementManualAdjustment, MovementDamage,
		MovementWaste, MovementReconciliation:
		return true
	default:
		return false
	}
}

// Manual adjustments expose a small, direction-aware reason catalogue.  The
// database keeps reason_code as a string so document-originated movements can
// continue to use their own codes, but a manual inbound movement must never
// be posted as damage/waste and an outbound movement must not be labelled as a
// purchase receipt.  This mirrors the operation dialog and protects callers
// that bypass the UI.
func validManualReason(direction, reason string) bool {
	switch direction {
	case DirectionIn:
		switch reason {
		case "PURCHASE_RECEIPT", "SALES_RETURN", "OPENING", "CORRECTION", "ADJUSTMENT", "PROMOTION", "OTHER":
			return true
		}
	case DirectionOut:
		switch reason {
		case "SALES_DISPATCH", "PURCHASE_RETURN", "CORRECTION", "ADJUSTMENT", "DAMAGE", "WASTE", "SAMPLE", "INTERNAL_USE", "OTHER":
			return true
		}
	}
	return false
}

func normalizeMovement(input MovementInput) (MovementInput, error) {
	input.CompanyID = strings.TrimSpace(input.CompanyID)
	var err error
	if input.CompanyID == "" {
		return MovementInput{}, validationError("company_id is required")
	}
	if _, err = requireUUID("company_id", input.CompanyID); err != nil {
		return MovementInput{}, err
	}
	if input.WarehouseID, err = requireUUID("warehouse_id", input.WarehouseID); err != nil {
		return MovementInput{}, err
	}
	if input.ProductID, err = requireUUID("product_id", input.ProductID); err != nil {
		return MovementInput{}, err
	}
	if input.WarehouseID == "" {
		return MovementInput{}, validationError("warehouse_id is required")
	}
	for name, value := range map[string]*string{
		"location_id": &input.LocationID, "variant_id": &input.VariantID,
		"lot_id": &input.LotID, "serial_id": &input.SerialID,
		"source_line_id": &input.SourceLineID, "actor_user_id": &input.ActorUserID,
	} {
		*value = strings.TrimSpace(*value)
		if *value != "" {
			if _, err = requireUUID(name, *value); err != nil {
				return MovementInput{}, err
			}
		}
	}
	input.Direction = strings.ToUpper(strings.TrimSpace(input.Direction))
	if input.Direction != DirectionIn && input.Direction != DirectionOut {
		return MovementInput{}, validationError("direction must be IN or OUT")
	}
	input.MovementType = strings.ToUpper(strings.TrimSpace(input.MovementType))
	if !validMovementType(input.MovementType) {
		return MovementInput{}, validationError("movement_type is invalid")
	}
	if input.Quantity, err = cleanQuantity("quantity", input.Quantity, true); err != nil {
		return MovementInput{}, err
	}
	input.UnitCode = strings.ToUpper(strings.TrimSpace(input.UnitCode))
	input.LotNumber = strings.TrimSpace(input.LotNumber)
	input.SupplierLotNo = strings.TrimSpace(input.SupplierLotNo)
	input.SerialNumber = strings.TrimSpace(input.SerialNumber)
	if input.LotNumber != "" && input.LotID != "" {
		return MovementInput{}, fmt.Errorf("%w: mevcut lot ile yeni lot aynı anda seçilemez", identity.ErrValidation)
	}
	if input.SerialNumber != "" && input.SerialID != "" {
		return MovementInput{}, fmt.Errorf("%w: mevcut seri ile yeni seri aynı anda seçilemez", identity.ErrValidation)
	}
	if (input.LotNumber != "" || input.SerialNumber != "") && input.Direction != DirectionIn {
		return MovementInput{}, fmt.Errorf("%w: yeni lot veya seri yalnız giriş hareketinde oluşturulabilir", identity.ErrValidation)
	}
	if input.EnteredQuantity == "" {
		input.EnteredQuantity = input.Quantity
	}
	if input.EnteredQuantity, err = cleanQuantity("entered_quantity", input.EnteredQuantity, true); err != nil {
		return MovementInput{}, err
	}
	input.ConversionFactor = strings.TrimSpace(input.ConversionFactor)
	if input.ConversionFactor != "" {
		if input.ConversionFactor, err = cleanQuantity("conversion_factor", input.ConversionFactor, true); err != nil {
			return MovementInput{}, err
		}
	}
	input.UnitCost = strings.TrimSpace(input.UnitCost)
	if input.UnitCost != "" {
		if input.UnitCost, err = cleanQuantity("unit_cost", input.UnitCost, false); err != nil {
			return MovementInput{}, err
		}
	}
	if input.Direction == DirectionOut && input.MovementType == MovementManualAdjustment && (input.UnitCost != "" || input.Currency != "") {
		return MovementInput{}, fmt.Errorf("%w: çıkış maliyeti değerleme motoru tarafından hesaplanır", identity.ErrValidation)
	}
	input.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))
	if input.Currency != "" && (len(input.Currency) != 3 || input.Currency < "A" || input.Currency > "ZZZ") {
		// The comparison above intentionally rejects non-ASCII values; the
		// database check remains the final authority for the format.
		return MovementInput{}, validationError("currency must be a three-letter code")
	}
	input.ReasonCode = strings.ToUpper(strings.TrimSpace(input.ReasonCode))
	input.ReasonDescription = strings.TrimSpace(input.ReasonDescription)
	if input.ReasonCode == "" {
		if input.MovementType == MovementManualAdjustment || input.MovementType == MovementDamage || input.MovementType == MovementWaste {
			return MovementInput{}, codeError(ErrInvalidReason.Error(), ErrInvalidReason, "manuel hareket için reason code gereklidir")
		}
		input.ReasonCode = input.MovementType
	}
	if input.ReasonCode == "OTHER" && input.ReasonDescription == "" {
		return MovementInput{}, codeError(ErrInvalidReason.Error(), ErrInvalidReason, "Diğer seçimi için açıklama gereklidir")
	}
	if input.MovementType == MovementManualAdjustment && !validManualReason(input.Direction, input.ReasonCode) {
		return MovementInput{}, codeError(ErrInvalidReason.Error(), ErrInvalidReason, "giriş ve çıkış için hareket nedeni uyumsuz")
	}
	input.SourceType = strings.TrimSpace(input.SourceType)
	if input.SourceType == "" {
		input.SourceType = "INVENTORY_COMMAND"
	}
	if input.ID == "" {
		input.ID = uuid.NewString()
	}
	if input.ID, err = requireUUID("id", input.ID); err != nil {
		return MovementInput{}, err
	}
	input.SourceID = strings.TrimSpace(input.SourceID)
	if input.SourceID == "" {
		input.SourceID = input.ID
	}
	if _, err = requireUUID("source_id", input.SourceID); err != nil {
		return MovementInput{}, err
	}
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if input.IdempotencyKey == "" {
		input.IdempotencyKey = "movement:" + input.ID
	}
	input.ReversalOfID = strings.TrimSpace(input.ReversalOfID)
	if input.ReversalOfID != "" {
		if _, err = requireUUID("reversal_of_id", input.ReversalOfID); err != nil {
			return MovementInput{}, err
		}
	}
	input.ExpiryOverrideReason = strings.TrimSpace(input.ExpiryOverrideReason)
	if input.ExpiryOverride && input.ExpiryOverrideReason == "" {
		return MovementInput{}, validationError("expiry override reason is required")
	}
	if input.LotManufacturedAt != nil && input.LotExpiresAt != nil && input.LotExpiresAt.Before(*input.LotManufacturedAt) {
		return MovementInput{}, validationError("lot son kullanma tarihi üretim tarihinden önce olamaz")
	}
	if input.Metadata == nil {
		input.Metadata = map[string]any{}
	}
	return input, nil
}

func movementHash(input MovementInput, sourceIDWasGenerated bool) []byte {
	copyInput := input
	copyInput.ID = ""
	if sourceIDWasGenerated {
		copyInput.SourceID = ""
	}
	payload, _ := json.Marshal(copyInput)
	hash := sha256.Sum256(payload)
	return hash[:]
}

func mapInventoryError(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		message := strings.ToUpper(pgErr.Message)
		switch {
		case strings.Contains(message, ErrInsufficientStock.Error()):
			return fmt.Errorf("%w: stok yetersiz", ErrInsufficientStock)
		case strings.Contains(message, ErrSerialAlreadyInStock.Error()):
			return fmt.Errorf("%w: seri numarası zaten stokta", ErrSerialAlreadyInStock)
		case strings.Contains(message, ErrSerialNotAvailable.Error()):
			return fmt.Errorf("%w: seri numarası kullanılabilir değil", ErrSerialNotAvailable)
		case strings.Contains(message, ErrLotExpired.Error()):
			return fmt.Errorf("%w: lotun son kullanma tarihi geçmiştir", ErrLotExpired)
		case strings.Contains(message, ErrWarehouseTransferInvalidState.Error()):
			return fmt.Errorf("%w: transfer durumu geçersiz", ErrWarehouseTransferInvalidState)
		case strings.Contains(message, ErrWarehouseInactive.Error()):
			return fmt.Errorf("%w: pasif depoda yeni hareket oluşturulamaz", ErrWarehouseInactive)
		case strings.Contains(message, ErrWarehouseMovementNotAllowed.Error()):
			return fmt.Errorf("%w: özel depodan bu yönde hareket yapılamaz", ErrWarehouseMovementNotAllowed)
		case strings.Contains(message, ErrVariantRequired.Error()):
			return fmt.Errorf("%w: varyantlı üründe aktif varyant seçilmelidir", ErrVariantRequired)
		case strings.Contains(message, ErrVariantInactive.Error()):
			return fmt.Errorf("%w: pasif varyant seçilemez", ErrVariantInactive)
		case strings.Contains(message, "VARIANT DOES NOT BELONG TO PRODUCT") || strings.Contains(message, "VARIANT IS NOT ENABLED FOR PRODUCT"):
			return fmt.Errorf("%w: varyant seçilen ürünle eşleşmiyor", ErrVariantProductMismatch)
		case strings.Contains(message, ErrWarehouseSystem.Error()):
			return fmt.Errorf("%w: sistem transit deposuna yalnız transfer akışı dokunabilir", ErrWarehouseSystem)
		case strings.Contains(message, ErrTransferSameWarehouse.Error()):
			return fmt.Errorf("%w: çıkış ve varış deposu aynı olamaz", ErrTransferSameWarehouse)
		case strings.Contains(message, "WAREHOUSE_TRANSFER_WAREHOUSE_RULE"):
			return fmt.Errorf("%w: transfer kaynağı aktif normal depo, hedefi aktif normal veya özel depo olmalıdır", ErrWarehouseNotStandard)
		case strings.Contains(message, "OTHER MOVEMENT REASON") || strings.Contains(message, "MANUAL STOCK MOVEMENT"):
			return fmt.Errorf("%w: hareket nedeni gereklidir", ErrInvalidReason)
		case pgErr.Code == "23505":
			return fmt.Errorf("%w: kayıt çakışması", ErrConflict)
		}
	}
	return err
}

const movementColumns = `id,company_id,warehouse_id,location_id,product_id,variant_id,lot_id,serial_id,
movement_type,direction,quantity::text,quantity_delta::text,unit_cost::text,currency,
reason_code,reason_description,source_type,source_id,source_line_id,idempotency_key,reversal_of_id,
expiry_override,expiry_override_reason,metadata,actor_user_id,posted_at`

func scanMovement(row interface{ Scan(...any) error }) (Movement, error) {
	var item Movement
	var metadata []byte
	var quantity, delta string
	var unitCost, currency *string
	if err := row.Scan(&item.ID, &item.CompanyID, &item.WarehouseID, &item.LocationID, &item.ProductID,
		&item.VariantID, &item.LotID, &item.SerialID, &item.MovementType, &item.Direction,
		&quantity, &delta, &unitCost, &currency, &item.ReasonCode, &item.ReasonDescription,
		&item.SourceType, &item.SourceID, &item.SourceLineID, &item.IdempotencyKey, &item.ReversalOfID,
		&item.ExpiryOverride, &item.ExpiryOverrideReason, &metadata, &item.ActorUserID, &item.PostedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Movement{}, ErrNotFound
		}
		return Movement{}, err
	}
	item.Quantity, item.QuantityDelta = quantity, delta
	item.BaseQuantity = quantity
	item.Status = "POSTED"
	if item.ReversalOfID != nil {
		item.Status = "REVERSED"
	}
	if unitCost != nil {
		item.UnitCost = unitCost
	}
	if currency != nil {
		item.Currency = currency
	}
	item.Metadata = map[string]any{}
	if len(metadata) > 0 {
		if err := json.Unmarshal(metadata, &item.Metadata); err != nil {
			return Movement{}, err
		}
	}
	if value, ok := item.Metadata["entered_quantity"].(string); ok {
		item.EnteredQuantity = value
	}
	if value, ok := item.Metadata["unit_code"].(string); ok {
		item.UnitCode = value
	}
	if value, ok := item.Metadata["conversion_factor"].(string); ok {
		item.ConversionFactor = value
	}
	return item, nil
}

func loadMovement(ctx context.Context, db txDB, companyID, id string, forUpdate bool) (Movement, error) {
	query := `SELECT ` + movementColumns + ` FROM stock_movements WHERE company_id=$1 AND id=$2`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	item, err := scanMovement(db.QueryRow(ctx, query, companyID, id))
	if err != nil {
		return Movement{}, err
	}
	if item.ActorUserID != nil {
		if nameErr := db.QueryRow(ctx, `SELECT COALESCE(display_name,email,'') FROM users WHERE id=$1`, *item.ActorUserID).Scan(&item.ActorName); nameErr != nil && !errors.Is(nameErr, pgx.ErrNoRows) {
			return Movement{}, nameErr
		}
	}
	return item, nil
}

func (s *Service) PostMovement(ctx context.Context, input MovementInput) (Movement, error) {
	normalized, err := normalizeMovement(input)
	if err != nil {
		return Movement{}, err
	}
	sourceIDWasGenerated := strings.TrimSpace(input.SourceID) == ""
	tx, err := s.begin(ctx)
	if err != nil {
		return Movement{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err = ensureWarehouseAccess(ctx, tx, normalized.CompanyID, normalized.ActorUserID, normalized.WarehouseID); err != nil {
		return Movement{}, err
	}
	if err = applyUnitConversionTx(ctx, tx, &normalized); err != nil {
		return Movement{}, err
	}
	if err = ensureTrackingRecordsTx(ctx, tx, &normalized); err != nil {
		return Movement{}, err
	}
	hash := movementHash(normalized, sourceIDWasGenerated)
	item, err := postMovementTx(ctx, tx, normalized, hash)
	if err != nil {
		return Movement{}, mapInventoryError(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return Movement{}, err
	}
	return item, nil
}

// OpeningStockImportLine is the code-based boundary used by the common
// exchange engine. Resolution and posting happen in one transaction so a
// concurrent catalog or warehouse change cannot redirect a row silently.
type OpeningStockImportLine struct {
	WarehouseCode string
	ProductCode   string
	VariantCode   string
	Quantity      string
	RowNumber     int
}

// PostOpeningStockImport appends one immutable inbound movement per valid
// source row. The caller has already validated the file and supplies a stable
// import job UUID as sourceID; idempotency keys make retries no-ops.
func (s *Service) PostOpeningStockImport(ctx context.Context, companyID, actorID, sourceID string, lines []OpeningStockImportLine) error {
	var err error
	tx, err := s.begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err = PostOpeningStockImportTx(ctx, tx, companyID, actorID, sourceID, lines); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// PostOpeningStockImportTx is the transaction-compatible opening-stock
// adapter. Catalog imports use it after inserting a new product so the product
// card and its first immutable stock movement either commit together or roll
// back together.
func PostOpeningStockImportTx(ctx context.Context, tx pgx.Tx, companyID, actorID, sourceID string, lines []OpeningStockImportLine) error {
	var err error
	companyID, err = requireUUID("company_id", companyID)
	if err != nil {
		return err
	}
	actorID, err = requireUUID("actor_user_id", actorID)
	if err != nil {
		return err
	}
	sourceID, err = requireUUID("source_id", sourceID)
	if err != nil {
		return err
	}
	if tx == nil {
		return errors.New("inventory transaction is not configured")
	}
	if len(lines) == 0 {
		return validationError("açılış stoku satırı bulunamadı")
	}
	for _, line := range lines {
		if line.RowNumber < 1 {
			return validationError("açılış stoku satır numarası geçersiz")
		}
		var warehouseID, warehouseType string
		var warehouseActive bool
		if err = tx.QueryRow(ctx, `SELECT id,warehouse_type,is_active FROM warehouses WHERE company_id=$1 AND code=$2 FOR UPDATE`, companyID, strings.TrimSpace(line.WarehouseCode)).Scan(&warehouseID, &warehouseType, &warehouseActive); errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: depo kodu bulunamadı", identity.ErrValidation)
		} else if err != nil {
			return err
		}
		if warehouseType != WarehouseStandard || !warehouseActive {
			return fmt.Errorf("%w: açılış stoku yalnız aktif standart depoya aktarılabilir", identity.ErrValidation)
		}
		if err = ensureWarehouseAccess(ctx, tx, companyID, actorID, warehouseID); err != nil {
			return err
		}
		var productID, productKind string
		if err = tx.QueryRow(ctx, `SELECT id,kind::text FROM products WHERE company_id=$1 AND code=$2 FOR SHARE`, companyID, strings.TrimSpace(line.ProductCode)).Scan(&productID, &productKind); errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: ürün kodu bulunamadı", identity.ErrValidation)
		} else if err != nil {
			return err
		}
		if productKind != "PHYSICAL" {
			return fmt.Errorf("%w: açılış stoku hizmet ürününe aktarılamaz", identity.ErrValidation)
		}
		variantID := ""
		if code := strings.TrimSpace(line.VariantCode); code != "" {
			if err = tx.QueryRow(ctx, `SELECT id FROM product_variants WHERE company_id=$1 AND product_id=$2 AND variant_code=$3 FOR SHARE`, companyID, productID, code).Scan(&variantID); errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("%w: varyant kodu bulunamadı", identity.ErrValidation)
			} else if err != nil {
				return err
			}
		}
		input := MovementInput{
			CompanyID: companyID, WarehouseID: warehouseID, ProductID: productID, VariantID: variantID,
			MovementType: MovementManualAdjustment, Direction: DirectionIn, Quantity: strings.TrimSpace(line.Quantity),
			ReasonCode: "OPENING", ReasonDescription: "Açılış stok aktarımı", SourceType: "OPENING_STOCK_IMPORT",
			SourceID: sourceID, IdempotencyKey: fmt.Sprintf("opening-stock-import:%s:%d", sourceID, line.RowNumber), ActorUserID: actorID,
			Metadata: map[string]any{"import_row_number": line.RowNumber},
		}
		normalized, normalizeErr := normalizeMovement(input)
		if normalizeErr != nil {
			return normalizeErr
		}
		if err = applyUnitConversionTx(ctx, tx, &normalized); err != nil {
			return err
		}
		if err = ensureTrackingRecordsTx(ctx, tx, &normalized); err != nil {
			return err
		}
		if _, err = postMovementTx(ctx, tx, normalized, movementHash(normalized, false)); err != nil {
			return mapInventoryError(err)
		}
	}
	return nil
}

// ensureTrackingRecordsTx creates traceability identities only as part of an
// inbound movement. This keeps lot/serial creation in the stock ledger
// transaction while still allowing opening stock and goods-receipt screens to
// capture a new lot or a single serial number.
func ensureTrackingRecordsTx(ctx context.Context, tx pgx.Tx, input *MovementInput) error {
	if input == nil {
		return nil
	}
	if input.LotNumber != "" {
		var lotID string
		err := tx.QueryRow(ctx, `INSERT INTO lots(id,company_id,product_id,lot_number,manufactured_at,expires_at,supplier_reference) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(company_id,product_id,lot_number) DO UPDATE SET lot_number=EXCLUDED.lot_number RETURNING id`, uuid.NewString(), input.CompanyID, input.ProductID, input.LotNumber, input.LotManufacturedAt, input.LotExpiresAt, input.SupplierLotNo).Scan(&lotID)
		if err != nil {
			return mapInventoryError(err)
		}
		input.LotID = lotID
	}
	if input.SerialNumber != "" {
		quantity, ok := new(big.Rat).SetString(input.Quantity)
		if !ok || quantity.Cmp(big.NewRat(1, 1)) != 0 {
			return fmt.Errorf("%w: seri hareketinde miktar 1 olmalıdır", identity.ErrValidation)
		}
		var serialID string
		err := tx.QueryRow(ctx, `INSERT INTO serial_numbers(id,company_id,product_id,serial_number,status,active_warehouse_id) VALUES($1,$2,$3,$4,'DISPATCHED',NULL) ON CONFLICT(company_id,product_id,serial_number) DO UPDATE SET serial_number=EXCLUDED.serial_number RETURNING id`, uuid.NewString(), input.CompanyID, input.ProductID, input.SerialNumber).Scan(&serialID)
		if err != nil {
			return mapInventoryError(err)
		}
		input.SerialID = serialID
	}
	return nil
}

func applyUnitConversionTx(ctx context.Context, tx pgx.Tx, input *MovementInput) error {
	if input == nil || input.UnitCode == "" {
		return nil
	}
	var factor string
	if err := tx.QueryRow(ctx, `SELECT conversion_factor::text FROM product_units WHERE company_id=$1 AND product_id=$2 AND unit_code=$3`, input.CompanyID, input.ProductID, input.UnitCode).Scan(&factor); errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: ürün için %s birimi tanımlı değil", identity.ErrValidation, input.UnitCode)
	} else if err != nil {
		return err
	}
	if input.ConversionFactor != "" {
		factor = input.ConversionFactor
	}
	entered, ok := new(big.Rat).SetString(input.EnteredQuantity)
	if !ok {
		return fmt.Errorf("%w: girilen miktar geçersiz", identity.ErrValidation)
	}
	conversion, ok := new(big.Rat).SetString(factor)
	if !ok || conversion.Sign() <= 0 {
		return fmt.Errorf("%w: birim dönüşüm katsayısı geçersiz", identity.ErrValidation)
	}
	base := new(big.Rat).Mul(entered, conversion)
	input.Quantity = base.FloatString(8)
	input.ConversionFactor = factor
	input.Metadata["entered_quantity"] = input.EnteredQuantity
	input.Metadata["unit_code"] = input.UnitCode
	input.Metadata["conversion_factor"] = factor
	return nil
}

// PostStockMovement is the descriptive alias used by command handlers.
func (s *Service) PostStockMovement(ctx context.Context, input MovementInput) (Movement, error) {
	return s.PostMovement(ctx, input)
}

type InvoiceStockPostingInput struct {
	DocumentID   string
	DocumentType string
	WarehouseID  string
	Lines        []InvoiceStockLine
}

type InvoiceStockLine struct {
	LineID    string
	ProductID string
	VariantID string
	Quantity  string
	UnitCode  string
	UnitCost  string
	Currency  string
}

type PurchaseReceiptStockPostingInput struct {
	ReceiptID   string
	WarehouseID string
	Lines       []PurchaseStockLine
}

type PurchaseReturnStockPostingInput struct {
	ReturnID    string
	WarehouseID string
	Lines       []PurchaseStockLine
}

// PurchaseStockReversalInput identifies the immutable stock movements that
// belong to a posted purchasing aggregate.  The purchasing service supplies
// the source type so the same reversal implementation can compensate a
// receipt, direct invoice, or purchase return without rebuilding quantities
// from mutable projections.
type PurchaseStockReversalInput struct {
	DocumentID  string
	SourceType  string
	WarehouseID string
	ReversalKey string
	Reason      string
}

type PurchaseStockLine struct {
	LineID           string
	ProductID        string
	VariantID        string
	Quantity         string
	BaseQuantity     string
	ConversionFactor string
	UnitCode         string
	UnitCost         string
	Currency         string
	LotNumber        string
	SerialNumber     string
}

// PostPurchaseReceiptMovementsTx and PostPurchaseReturnMovementsTx are the
// provider-neutral inventory adapters used by the purchasing aggregate. They
// deliberately accept an existing transaction so receipt/return, stock,
// audit and outbox records cannot diverge.
func (s *Service) PostPurchaseReceiptMovementsTx(ctx context.Context, tx pgx.Tx, session identity.Session, input PurchaseReceiptStockPostingInput) error {
	return s.postPurchaseMovementsTx(ctx, tx, session, "GOODS_RECEIPT", input.ReceiptID, input.WarehouseID, MovementPurchaseReceipt, DirectionIn, input.Lines)
}

func (s *Service) PostPurchaseInvoiceMovementsTx(ctx context.Context, tx pgx.Tx, session identity.Session, input PurchaseReceiptStockPostingInput) error {
	return s.postPurchaseMovementsTx(ctx, tx, session, "PURCHASE_INVOICE", input.ReceiptID, input.WarehouseID, MovementPurchaseReceipt, DirectionIn, input.Lines)
}

func (s *Service) PostPurchaseReturnMovementsTx(ctx context.Context, tx pgx.Tx, session identity.Session, input PurchaseReturnStockPostingInput) error {
	return s.postPurchaseMovementsTx(ctx, tx, session, "PURCHASE_RETURN", input.ReturnID, input.WarehouseID, MovementPurchaseReturn, DirectionOut, input.Lines)
}

func (s *Service) postPurchaseMovementsTx(ctx context.Context, tx pgx.Tx, session identity.Session, sourceType, sourceID, warehouseID, movementType, direction string, lines []PurchaseStockLine) error {
	if identity.ValidateExternalActor(session) != nil || !session.HasPermission("inventory.movement.post") {
		return identity.ErrForbidden
	}
	if _, err := requireUUID("source_id", sourceID); err != nil {
		return err
	}
	if _, err := requireUUID("warehouse_id", warehouseID); err != nil {
		return err
	}
	companyID := strings.TrimSpace(session.CurrentCompanyID)
	if len(lines) == 0 {
		// A receipt containing only damaged/rejected quantities is still a
		// posted business record; it simply has no sellable stock effect.
		return nil
	}
	if err := ensureWarehouseAccess(ctx, tx, companyID, session.User.ID, warehouseID); err != nil {
		return err
	}
	for _, line := range lines {
		if strings.TrimSpace(line.LineID) == "" {
			return fmt.Errorf("%w: satın alma satır kimliği gereklidir", identity.ErrValidation)
		}
		// Keep a database-backed claim next to the movement idempotency key. This
		// protects the physical effect even when a caller retries with a new
		// movement id after a response timeout.
		var registryLineID string
		claimLookupErr := tx.QueryRow(ctx, `SELECT line_id FROM commercial_line_registry WHERE company_id=$1 AND line_id=$2`, companyID, line.LineID).Scan(&registryLineID)
		if errors.Is(claimLookupErr, pgx.ErrNoRows) {
			registryLineID = ""
		} else if claimLookupErr != nil {
			return claimLookupErr
		}
		claimKey := fmt.Sprintf("commercial-stock:%s:%s", sourceID, line.LineID)
		var claimID string
		claimErr := tx.QueryRow(ctx, `INSERT INTO commercial_effect_claims(id,company_id,effect_key,effect_type,document_id,line_id) VALUES($1,$2,$3,'STOCK',$4,NULLIF($5,'')::uuid) ON CONFLICT(company_id,effect_key) DO NOTHING RETURNING id`, uuid.NewString(), companyID, claimKey, sourceID, registryLineID).Scan(&claimID)
		if errors.Is(claimErr, pgx.ErrNoRows) {
			continue
		}
		if claimErr != nil {
			return claimErr
		}
		movement := MovementInput{
			ID: uuid.NewString(), CompanyID: companyID, WarehouseID: warehouseID,
			ProductID: line.ProductID, VariantID: line.VariantID, MovementType: movementType,
			Direction: direction, Quantity: line.Quantity, EnteredQuantity: line.Quantity,
			UnitCode: line.UnitCode, ConversionFactor: line.ConversionFactor, UnitCost: line.UnitCost, Currency: line.Currency,
			LotNumber: line.LotNumber, SerialNumber: line.SerialNumber, ReasonCode: movementType,
			SourceType: sourceType, SourceID: sourceID, SourceLineID: line.LineID,
			IdempotencyKey: strings.ToLower(strings.ReplaceAll(sourceType, "_", "-")) + ":" + sourceID + ":" + line.LineID,
			ActorUserID:    session.User.ID,
			Metadata:       map[string]any{"source_type": sourceType, "source_id": sourceID},
		}
		normalized, err := normalizeMovement(movement)
		if err != nil {
			return err
		}
		if err = applyUnitConversionTx(ctx, tx, &normalized); err != nil {
			return err
		}
		if err = ensureTrackingRecordsTx(ctx, tx, &normalized); err != nil {
			return err
		}
		if _, err = postMovementTx(ctx, tx, normalized, movementHash(normalized, false)); err != nil {
			return mapInventoryError(err)
		}
	}
	return nil
}

type InvoiceStockReversalInput struct {
	DocumentID   string
	DocumentType string
	WarehouseID  string
	ReversalKey  string
	Reason       string
}

// PostInvoiceMovementsTx is the inventory side of a sales/purchase invoice
// posting. The caller supplies its already-open transaction so every invoice
// line and its finance/current-account effects commit atomically.
func (s *Service) PostInvoiceMovementsTx(ctx context.Context, tx pgx.Tx, session identity.Session, input InvoiceStockPostingInput) error {
	if identity.ValidateExternalActor(session) != nil || !session.HasPermission("inventory.movement.post") {
		return identity.ErrForbidden
	}
	if strings.TrimSpace(input.DocumentID) == "" || strings.TrimSpace(input.WarehouseID) == "" || len(input.Lines) == 0 {
		return fmt.Errorf("%w: fatura stok posting alanları eksik", identity.ErrValidation)
	}
	documentType := strings.ToUpper(strings.TrimSpace(input.DocumentType))
	movementType, direction := MovementSalesDispatch, DirectionOut
	switch {
	case strings.HasPrefix(documentType, "SALES_RETURN_"):
		movementType, direction = MovementSalesReturn, DirectionIn
	case strings.HasPrefix(documentType, "PURCHASE_RETURN_"):
		movementType, direction = MovementPurchaseReturn, DirectionOut
	case strings.HasPrefix(documentType, "PURCHASE_"):
		movementType, direction = MovementPurchaseReceipt, DirectionIn
	case strings.HasPrefix(documentType, "SALES_"):
		movementType, direction = MovementSalesDispatch, DirectionOut
	default:
		return fmt.Errorf("%w: fatura tipi stok hareketi için geçersiz", identity.ErrValidation)
	}
	companyID := strings.TrimSpace(session.CurrentCompanyID)
	if _, err := requireUUID("company_id", companyID); err != nil {
		return err
	}
	warehouseID, err := requireUUID("warehouse_id", input.WarehouseID)
	if err != nil {
		return err
	}
	if err = ensureWarehouseAccess(ctx, tx, companyID, session.User.ID, warehouseID); err != nil {
		return err
	}
	documentID, err := requireUUID("document_id", input.DocumentID)
	if err != nil {
		return err
	}
	for index, line := range input.Lines {
		productID, productErr := requireUUID("product_id", line.ProductID)
		if productErr != nil {
			return productErr
		}
		lineID := strings.TrimSpace(line.LineID)
		if lineID == "" {
			return fmt.Errorf("%w: fatura satır kimliği gereklidir", identity.ErrValidation)
		}
		if _, lineErr := requireUUID("line_id", lineID); lineErr != nil {
			return lineErr
		}
		movement := MovementInput{
			ID:              uuid.NewString(),
			CompanyID:       companyID,
			WarehouseID:     warehouseID,
			ProductID:       productID,
			VariantID:       line.VariantID,
			MovementType:    movementType,
			Direction:       direction,
			Quantity:        line.Quantity,
			EnteredQuantity: line.Quantity,
			UnitCode:        line.UnitCode,
			ReasonCode:      movementType,
			SourceType:      documentType,
			SourceID:        documentID,
			SourceLineID:    lineID,
			IdempotencyKey:  fmt.Sprintf("invoice:%s:stock:%s", documentID, lineID),
			ActorUserID:     session.User.ID,
			UnitCost:        line.UnitCost,
			Currency:        line.Currency,
			Metadata: map[string]any{
				"document_type": documentType,
				"line_no":       index + 1,
				"unit_code":     line.UnitCode,
			},
		}
		normalized, normalizeErr := normalizeMovement(movement)
		if normalizeErr != nil {
			return normalizeErr
		}
		if conversionErr := applyUnitConversionTx(ctx, tx, &normalized); conversionErr != nil {
			return conversionErr
		}
		if _, postErr := postMovementTx(ctx, tx, normalized, movementHash(normalized, false)); postErr != nil {
			return mapInventoryError(postErr)
		}
	}
	return nil
}

// ReverseInvoiceMovementsTx compensates every immutable stock movement
// created by a posted invoice. It deliberately reads the original ledger
// rows, rather than rebuilding quantities from mutable document lines, so a
// later draft edit can never change the historical reversal basis.
func (s *Service) ReverseInvoiceMovementsTx(ctx context.Context, tx pgx.Tx, session identity.Session, input InvoiceStockReversalInput) error {
	if identity.ValidateExternalActor(session) != nil || !session.HasPermission("inventory.movement.reverse") {
		return identity.ErrForbidden
	}
	companyID, err := requireUUID("company_id", session.CurrentCompanyID)
	if err != nil {
		return err
	}
	documentID, err := requireUUID("document_id", input.DocumentID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(input.WarehouseID) != "" {
		if _, err = requireUUID("warehouse_id", input.WarehouseID); err != nil {
			return err
		}
		if err = ensureWarehouseAccess(ctx, tx, companyID, session.User.ID, input.WarehouseID); err != nil {
			return err
		}
	}
	documentType := strings.ToUpper(strings.TrimSpace(input.DocumentType))
	if documentType == "" || strings.TrimSpace(input.ReversalKey) == "" || strings.TrimSpace(input.Reason) == "" {
		return fmt.Errorf("%w: fatura stok ters kayıt alanları eksik", identity.ErrValidation)
	}
	rows, err := tx.Query(ctx, `SELECT `+movementColumns+` FROM stock_movements WHERE company_id=$1 AND source_id=$2 AND source_type=$3 ORDER BY posted_at,id FOR UPDATE`, companyID, documentID, documentType)
	if err != nil {
		return err
	}
	originals := make([]Movement, 0)
	for rows.Next() {
		original, scanErr := scanMovement(rows)
		if scanErr != nil {
			rows.Close()
			return scanErr
		}
		originals = append(originals, original)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, original := range originals {
		var existingID string
		if queryErr := tx.QueryRow(ctx, `SELECT id FROM stock_movements WHERE company_id=$1 AND reversal_of_id=$2`, companyID, original.ID).Scan(&existingID); queryErr == nil {
			continue
		} else if !errors.Is(queryErr, pgx.ErrNoRows) {
			return queryErr
		}
		lineKey := original.ID
		if original.SourceLineID != nil {
			lineKey = *original.SourceLineID
		}
		unitCost, currency := valueOrEmpty(original.UnitCost), valueOrEmpty(original.Currency)
		if original.Direction == DirectionOut {
			// Reversing an outbound movement must restore the consumed FIFO cost
			// layer when it has a single, known source currency. A selling
			// invoice's transaction currency is not a cost conversion.
			if cost, costCurrency, costErr := transferCostBasis(ctx, tx, companyID, original.ID); costErr != nil {
				return costErr
			} else if cost != "" && costCurrency != "" {
				unitCost, currency = cost, costCurrency
			}
		}
		movement := MovementInput{
			ID: uuid.NewString(), CompanyID: companyID, WarehouseID: original.WarehouseID,
			LocationID: valueOrEmpty(original.LocationID), ProductID: original.ProductID,
			VariantID: valueOrEmpty(original.VariantID), LotID: valueOrEmpty(original.LotID),
			SerialID: valueOrEmpty(original.SerialID), MovementType: MovementReconciliation,
			Direction: reverseDirection(original.Direction), Quantity: original.Quantity,
			UnitCost: unitCost, Currency: currency,
			ReasonCode: "REVERSAL", ReasonDescription: strings.TrimSpace(input.Reason),
			SourceType: documentType + "_REVERSAL", SourceID: documentID, SourceLineID: valueOrEmpty(original.SourceLineID),
			IdempotencyKey: fmt.Sprintf("invoice:%s:stock-reversal:%s:%s", documentID, input.ReversalKey, lineKey),
			ReversalOfID:   original.ID, ActorUserID: session.User.ID,
			ExpiryOverride: true, ExpiryOverrideReason: "Fatura ters kaydı",
			Metadata: map[string]any{"document_id": documentID, "document_type": documentType, "original_movement_id": original.ID, "reason": input.Reason},
		}
		normalized, normalizeErr := normalizeMovement(movement)
		if normalizeErr != nil {
			return normalizeErr
		}
		if _, postErr := postMovementTx(ctx, tx, normalized, movementHash(normalized, false)); postErr != nil {
			return mapInventoryError(postErr)
		}
	}
	return nil
}

func postMovementTx(ctx context.Context, tx txDB, input MovementInput, hash []byte) (Movement, error) {
	if strings.TrimSpace(input.ID) == "" {
		normalized, normalizeErr := normalizeMovement(input)
		if normalizeErr != nil {
			return Movement{}, normalizeErr
		}
		input = normalized
		if len(hash) == 0 {
			hash = movementHash(input, false)
		}
	}
	if err := lockStockIdentityTx(ctx, tx, input); err != nil {
		return Movement{}, err
	}
	existing, err := loadMovementByIdempotency(ctx, tx, input.CompanyID, input.IdempotencyKey, true)
	if err == nil {
		persisted, hashErr := persistedHash(ctx, tx, input.CompanyID, input.IdempotencyKey)
		if hashErr != nil {
			return Movement{}, hashErr
		}
		if !bytes.Equal(persisted, hash) {
			return Movement{}, codeError(ErrIdempotencyConflict.Error(), ErrIdempotencyConflict, "aynı Idempotency-Key farklı içerikle kullanıldı")
		}
		return existing, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Movement{}, err
	}
	if err = validateWarehouseMovementTx(ctx, tx, input); err != nil {
		return Movement{}, err
	}
	if _, err = validateInventoryVariantTx(ctx, tx, input.CompanyID, input.ProductID, input.VariantID); err != nil {
		return Movement{}, err
	}
	metadata, _ := json.Marshal(input.Metadata)
	locationID, _ := optionalUUID(input.LocationID)
	variantID, _ := optionalUUID(input.VariantID)
	lotID, _ := optionalUUID(input.LotID)
	serialID, _ := optionalUUID(input.SerialID)
	sourceLineID, _ := optionalUUID(input.SourceLineID)
	var transferID, transferLineID any
	if input.SourceType == "WAREHOUSE_TRANSFER" {
		transferID, err = optionalUUID(input.SourceID)
		if err != nil {
			return Movement{}, err
		}
		transferLineID, err = optionalUUID(input.SourceLineID)
		if err != nil {
			return Movement{}, err
		}
	}
	reversalID, _ := optionalUUID(input.ReversalOfID)
	actorID, _ := optionalUUID(input.ActorUserID)
	unitCost := any(nil)
	if input.UnitCost != "" {
		unitCost = input.UnitCost
	}
	currency := any(nil)
	if input.Currency != "" {
		currency = input.Currency
	}
	var item Movement
	var returnedUnitCost, returnedCurrency *string
	query := `INSERT INTO stock_movements(
		id,company_id,warehouse_id,location_id,product_id,variant_id,lot_id,serial_id,
		movement_type,direction,quantity,unit_cost,currency,reason_code,reason_description,
		source_type,source_id,source_line_id,transfer_id,transfer_line_id,idempotency_key,payload_hash,reversal_of_id,
		expiry_override,expiry_override_reason,metadata,actor_user_id)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27)
		RETURNING ` + movementColumns
	err = tx.QueryRow(ctx, query, input.ID, input.CompanyID, input.WarehouseID, locationID, input.ProductID,
		variantID, lotID, serialID, input.MovementType, input.Direction, input.Quantity, unitCost, currency,
		input.ReasonCode, input.ReasonDescription, input.SourceType, input.SourceID, sourceLineID,
		transferID, transferLineID, input.IdempotencyKey, hash, reversalID, input.ExpiryOverride, input.ExpiryOverrideReason,
		metadata, actorID).Scan(&item.ID, &item.CompanyID, &item.WarehouseID, &item.LocationID, &item.ProductID,
		&item.VariantID, &item.LotID, &item.SerialID, &item.MovementType, &item.Direction, new(string), new(string),
		&returnedUnitCost, &returnedCurrency, &item.ReasonCode, &item.ReasonDescription, &item.SourceType, &item.SourceID,
		&item.SourceLineID, &item.IdempotencyKey, &item.ReversalOfID, &item.ExpiryOverride,
		&item.ExpiryOverrideReason, &metadata, &item.ActorUserID, &item.PostedAt)
	if err != nil {
		if mapped := mapInventoryError(err); mapped != err {
			if errors.Is(mapped, ErrConflict) {
				if replay, replayErr := loadMovementByIdempotency(ctx, tx, input.CompanyID, input.IdempotencyKey, true); replayErr == nil {
					persisted, hashErr := persistedHash(ctx, tx, input.CompanyID, input.IdempotencyKey)
					if hashErr != nil {
						return Movement{}, hashErr
					}
					if !bytes.Equal(persisted, hash) {
						return Movement{}, codeError(ErrIdempotencyConflict.Error(), ErrIdempotencyConflict, "aynı Idempotency-Key farklı içerikle kullanıldı")
					}
					return replay, nil
				}
			}
			return Movement{}, mapped
		}
		return Movement{}, err
	}
	// RETURNING values are rescanned with typed strings to preserve the exact
	// decimal text.  The insert has already validated all JSON and UUID values.
	loaded, loadErr := loadMovement(ctx, tx, input.CompanyID, item.ID, false)
	if loadErr != nil {
		return Movement{}, loadErr
	}
	eventType := "INVENTORY_MOVEMENT_POSTED"
	if input.ReversalOfID != "" {
		eventType = "INVENTORY_MOVEMENT_REVERSED"
	}
	if auditErr := writeInventoryAuditTx(ctx, tx, input.CompanyID, input.ActorUserID, eventType, loaded.ID, map[string]any{
		"movement_type": input.MovementType,
		"direction":     input.Direction,
		"quantity":      input.Quantity,
		"source_type":   input.SourceType,
		"source_id":     input.SourceID,
	}); auditErr != nil {
		return Movement{}, auditErr
	}
	return loaded, nil
}

// lockStockIdentityTx serializes ledger writes with count reconciliation for
// the same company/depot/product dimension.  It is transaction-scoped and
// does not expose infrastructure details to domain callers.
func lockStockIdentityTx(ctx context.Context, tx txDB, input MovementInput) error {
	key := strings.Join([]string{
		input.CompanyID,
		input.WarehouseID,
		input.ProductID,
		input.VariantID,
		input.LocationID,
		input.LotID,
		input.SerialID,
	}, "|")
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, key)
	return err
}

func writeInventoryAuditTx(ctx context.Context, tx txDB, companyID, actorUserID, eventType, entityID string, details map[string]any) error {
	actor, _ := optionalUUID(actorUserID)
	encoded, _ := json.Marshal(details)
	if _, err := tx.Exec(ctx, `INSERT INTO security_audit_events(id,company_id,actor_user_id,event_type,entity_type,entity_id,details,trace_id,source_ip,user_agent) VALUES($1,$2,$3,$4,'stock_movement',$5,$6,'','','')`, uuid.NewString(), companyID, actor, eventType, entityID, encoded); err != nil {
		return err
	}
	payload := map[string]any{"schema_version": 1, "entity_id": entityID}
	for key, value := range details {
		payload[key] = value
	}
	eventPayload, _ := json.Marshal(payload)
	_, err := tx.Exec(ctx, `INSERT INTO outbox_events(event_id,type,schema_version,company_id,trace_id,payload) VALUES($1,$2,1,$3,'',$4)`, uuid.NewString(), inventoryOutboxType(eventType), companyID, eventPayload)
	return err
}

func inventoryOutboxType(eventType string) string {
	return "inventory." + strings.ToLower(strings.ReplaceAll(strings.TrimPrefix(eventType, "INVENTORY_"), "_", "."))
}

func loadMovementByIdempotency(ctx context.Context, db txDB, companyID, key string, forUpdate bool) (Movement, error) {
	query := `SELECT ` + movementColumns + ` FROM stock_movements WHERE company_id=$1 AND idempotency_key=$2`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	return scanMovement(db.QueryRow(ctx, query, companyID, key))
}

// PostMovement's replay path compares the persisted payload hash directly.
// Keeping that query separate avoids exposing an internal checksum in public
// JSON DTOs.
func persistedHash(ctx context.Context, db txDB, companyID, key string) ([]byte, error) {
	var hash []byte
	err := db.QueryRow(ctx, `SELECT payload_hash FROM stock_movements WHERE company_id=$1 AND idempotency_key=$2`, companyID, key).Scan(&hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return hash, err
}

func (s *Service) ReverseMovement(ctx context.Context, companyID, movementID, reasonDescription, idempotencyKey, actorUserID string) (Movement, error) {
	reasonDescription = strings.TrimSpace(reasonDescription)
	if reasonDescription == "" {
		return Movement{}, fmt.Errorf("%w: stok ters kayıt gerekçesi gereklidir", identity.ErrValidation)
	}
	companyID, err := requireUUID("company_id", companyID)
	if err != nil {
		return Movement{}, err
	}
	movementID, err = requireUUID("movement_id", movementID)
	if err != nil {
		return Movement{}, err
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return Movement{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	item, err := reverseMovementTx(ctx, tx, companyID, movementID, reasonDescription, idempotencyKey, actorUserID)
	if err != nil {
		return Movement{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Movement{}, err
	}
	return item, nil
}

// reverseMovementTx posts the compensating movement for one stock movement
// inside an existing transaction. ReverseMovement wraps it for the single-row
// endpoint; ReverseStockMovementOperation calls it once per operation line so
// the whole multi-variant operation is undone atomically.
func reverseMovementTx(ctx context.Context, tx txDB, companyID, movementID, reasonDescription, idempotencyKey, actorUserID string) (Movement, error) {
	reasonDescription = strings.TrimSpace(reasonDescription)
	if reasonDescription == "" {
		return Movement{}, fmt.Errorf("%w: stok ters kayıt gerekçesi gereklidir", identity.ErrValidation)
	}
	movementID, err := requireUUID("movement_id", movementID)
	if err != nil {
		return Movement{}, err
	}
	original, err := loadMovement(ctx, tx, companyID, movementID, true)
	if err != nil {
		return Movement{}, err
	}
	if err = ensureWarehouseAccess(ctx, tx, companyID, actorUserID, original.WarehouseID); err != nil {
		return Movement{}, err
	}
	if err = assertManuallyReversible(ctx, tx, companyID, original); err != nil {
		return Movement{}, err
	}
	if idempotencyKey == "" {
		idempotencyKey = "reverse:" + movementID
	}
	var existingID, existingKey, existingReason string
	if err = tx.QueryRow(ctx, `SELECT id,idempotency_key,reason_description FROM stock_movements WHERE company_id=$1 AND reversal_of_id=$2`, companyID, movementID).Scan(&existingID, &existingKey, &existingReason); err == nil {
		if existingKey == idempotencyKey && existingReason == reasonDescription {
			return loadMovement(ctx, tx, companyID, existingID, false)
		}
		if existingKey == idempotencyKey {
			return Movement{}, codeError(ErrIdempotencyConflict.Error(), ErrIdempotencyConflict, "aynı ters kayıt anahtarı farklı gerekçeyle kullanıldı")
		}
		return Movement{}, fmt.Errorf("%w: hareket zaten ters kayda alınmış", ErrMovementAlreadyReversed)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return Movement{}, err
	}
	reasonCode := "REVERSAL"
	direction := reverseDirection(original.Direction)
	unitCost, currency := valueOrEmpty(original.UnitCost), valueOrEmpty(original.Currency)
	if original.Direction == DirectionOut {
		if cost, costCurrency, costErr := transferCostBasis(ctx, tx, companyID, original.ID); costErr != nil {
			return Movement{}, costErr
		} else if cost != "" && costCurrency != "" {
			unitCost, currency = cost, costCurrency
		}
	}
	input := MovementInput{
		CompanyID: companyID, WarehouseID: original.WarehouseID,
		LocationID: valueOrEmpty(original.LocationID), ProductID: original.ProductID,
		VariantID: valueOrEmpty(original.VariantID), LotID: valueOrEmpty(original.LotID),
		SerialID: valueOrEmpty(original.SerialID), MovementType: MovementReconciliation,
		Direction: direction, Quantity: original.Quantity, UnitCost: unitCost,
		Currency: currency, ReasonCode: reasonCode,
		ReasonDescription: reasonDescription, SourceType: "STOCK_MOVEMENT_REVERSAL",
		SourceID: original.ID, IdempotencyKey: idempotencyKey, ReversalOfID: original.ID,
		ActorUserID: actorUserID, Metadata: map[string]any{"original_movement_id": original.ID},
	}
	if original.Direction == DirectionOut {
		input.Direction = DirectionIn
	}
	normalized, err := normalizeMovement(input)
	if err != nil {
		return Movement{}, err
	}
	// REVERSAL is a system reason and is accepted by the immutable correction
	// flow even though manual adjustments require an explicit user reason.
	hash := movementHash(normalized, false)
	item, err := postMovementTx(ctx, tx, normalized, hash)
	if err != nil {
		return Movement{}, err
	}
	return item, nil
}

// ReverseStockMovementOperation posts a compensating movement for every line of
// a manual multi-variant operation in a single transaction, so a mixed-variant
// entry is undone as one unit. Replaying the same idempotency key returns
// without creating duplicates.
func (s *Service) ReverseStockMovementOperation(ctx context.Context, companyID, operationID, reason, idempotencyKey, actorUserID string) (StockMovementOperation, error) {
	companyID, err := requireUUID("company_id", companyID)
	if err != nil {
		return StockMovementOperation{}, err
	}
	operationID, err = requireUUID("operation_id", operationID)
	if err != nil {
		return StockMovementOperation{}, err
	}
	if strings.TrimSpace(reason) == "" {
		return StockMovementOperation{}, fmt.Errorf("%w: stok ters kayıt gerekçesi gereklidir", identity.ErrValidation)
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return StockMovementOperation{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	rows, err := tx.Query(ctx, `SELECT movement_id FROM stock_movement_operation_lines WHERE company_id=$1 AND operation_id=$2 ORDER BY line_no`, companyID, operationID)
	if err != nil {
		return StockMovementOperation{}, err
	}
	movementIDs := make([]string, 0)
	for rows.Next() {
		var movementID string
		if scanErr := rows.Scan(&movementID); scanErr != nil {
			rows.Close()
			return StockMovementOperation{}, scanErr
		}
		movementIDs = append(movementIDs, movementID)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return StockMovementOperation{}, err
	}
	rows.Close()
	if len(movementIDs) == 0 {
		return StockMovementOperation{}, ErrNotFound
	}

	if strings.TrimSpace(idempotencyKey) == "" {
		idempotencyKey = "reverse-operation:" + operationID
	}
	for _, movementID := range movementIDs {
		if _, err = reverseMovementTx(ctx, tx, companyID, movementID, reason, idempotencyKey+":"+movementID, actorUserID); err != nil {
			return StockMovementOperation{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return StockMovementOperation{}, err
	}
	return s.GetStockMovementOperation(ctx, companyID, operationID, actorUserID)
}

// assertManuallyReversible enforces the invariant that a stock movement tied to
// a commercial document (dispatch, goods receipt, invoice, return) owns its
// lifecycle through that document; its stock effect can only be undone by
// cancelling the source document. Movements without a linked document — manual
// adjustments, opening-stock imports, warehouse transfers and stock-count
// adjustments — can be compensated directly from the generic reverse endpoint.
// A reversal entry itself is never reversible again. This is also guarded at the
// database level (migrations 000111 / 000132) so a direct API call cannot bypass
// it.
func assertManuallyReversible(ctx context.Context, db txDB, companyID string, original Movement) error {
	switch {
	case original.MovementType == MovementManualAdjustment:
		return nil
	case original.SourceType == "STOCK_MOVEMENT_OPERATION":
		return nil
	case original.ReversalOfID != nil && strings.TrimSpace(*original.ReversalOfID) != "",
		strings.HasSuffix(original.SourceType, "_REVERSAL"):
		return codeError(ErrDocumentOriginMovement.Error(), ErrDocumentOriginMovement,
			"Ters kayıt hareketi yeniden ters çevrilemez.")
	}
	reference := strings.TrimSpace(original.SourceID)
	var documentNo string
	if reference != "" {
		if err := db.QueryRow(ctx, `SELECT document_no FROM documents WHERE company_id=$1 AND id=$2`, companyID, reference).Scan(&documentNo); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
	}
	if documentNo != "" {
		return codeError(ErrDocumentOriginMovement.Error(), ErrDocumentOriginMovement,
			"Bu stok hareketi %s belgesinden oluşturuldu. Hareketi geri almak için kaynak belgeyi iptal edin.", documentNo)
	}
	return nil
}

func reverseDirection(direction string) string {
	if direction == DirectionOut {
		return DirectionIn
	}
	return DirectionOut
}

func (s *Service) ReverseStockMovement(ctx context.Context, companyID, movementID, reasonDescription, idempotencyKey, actorUserID string) (Movement, error) {
	return s.ReverseMovement(ctx, companyID, movementID, reasonDescription, idempotencyKey, actorUserID)
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func valueOrEmptyAny(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

func (s *Service) GetPosition(ctx context.Context, companyID, warehouseID, productID string, variantID, locationID, lotID, serialID string, userIDs ...string) (Position, error) {
	for name, value := range map[string]string{"company_id": companyID, "warehouse_id": warehouseID, "product_id": productID} {
		if _, err := requireUUID(name, value); err != nil {
			return Position{}, err
		}
	}
	if err := ensureWarehouseScope(ctx, s.pool, companyID, optionalActor(userIDs), warehouseID); err != nil {
		return Position{}, err
	}
	parsedVariant, err := optionalUUID(variantID)
	if err != nil {
		return Position{}, err
	}
	parsedLocation, err := optionalUUID(locationID)
	if err != nil {
		return Position{}, err
	}
	parsedLot, err := optionalUUID(lotID)
	if err != nil {
		return Position{}, err
	}
	parsedSerial, err := optionalUUID(serialID)
	if err != nil {
		return Position{}, err
	}
	variantID = strings.TrimSpace(variantID)
	if variantID != "" {
		if _, err = validateInventoryVariantTx(ctx, s.pool, companyID, productID, variantID); err != nil {
			return Position{}, err
		}
	}
	var hasVariants bool
	if err = s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_variants WHERE company_id=$1 AND product_id=$2)`, companyID, productID).Scan(&hasVariants); err != nil {
		return Position{}, err
	}
	if hasVariants && variantID == "" {
		var physical, reserved, available string
		if err = s.pool.QueryRow(ctx, `SELECT COALESCE(SUM(physical_quantity),0)::text,
			COALESCE(SUM(reserved_quantity),0)::text,
			COALESCE(SUM(available_quantity),0)::text
			FROM stock_positions
			WHERE company_id=$1 AND warehouse_id=$2 AND product_id=$3
			AND variant_id IS NOT NULL
			AND EXISTS (SELECT 1 FROM warehouses w WHERE w.company_id=stock_positions.company_id AND w.id=stock_positions.warehouse_id AND w.is_active AND w.warehouse_type='STANDARD')
			AND location_id IS NOT DISTINCT FROM $4::uuid
			AND lot_id IS NOT DISTINCT FROM $5::uuid
			AND serial_id IS NOT DISTINCT FROM $6::uuid`, companyID, warehouseID, productID, parsedLocation, parsedLot, parsedSerial).Scan(&physical, &reserved, &available); err != nil {
			return Position{}, err
		}
		return Position{
			CompanyID: companyID, WarehouseID: warehouseID, ProductID: productID,
			PhysicalQuantity: physical, ReservedQuantity: reserved, AvailableQuantity: available,
			IsAggregate: true,
		}, nil
	}
	query := `SELECT id,company_id,warehouse_id,location_id,product_id,variant_id,lot_id,serial_id,
		physical_quantity::text,reserved_quantity::text,available_quantity::text
		FROM stock_positions WHERE company_id=$1 AND warehouse_id=$2 AND product_id=$3
		AND EXISTS (SELECT 1 FROM warehouses w WHERE w.company_id=stock_positions.company_id AND w.id=stock_positions.warehouse_id AND w.is_active AND w.warehouse_type='STANDARD')
		AND variant_id IS NOT DISTINCT FROM $4::uuid AND location_id IS NOT DISTINCT FROM $5::uuid
		AND lot_id IS NOT DISTINCT FROM $6::uuid AND serial_id IS NOT DISTINCT FROM $7::uuid`
	args := []any{companyID, warehouseID, productID, parsedVariant, parsedLocation, parsedLot, parsedSerial}
	var item Position
	err = s.pool.QueryRow(ctx, query, args...).Scan(&item.ID, &item.CompanyID, &item.WarehouseID, &item.LocationID,
		&item.ProductID, &item.VariantID, &item.LotID, &item.SerialID, &item.PhysicalQuantity,
		&item.ReservedQuantity, &item.AvailableQuantity)
	if errors.Is(err, pgx.ErrNoRows) {
		item = Position{CompanyID: companyID, WarehouseID: warehouseID, ProductID: productID, PhysicalQuantity: "0", ReservedQuantity: "0", AvailableQuantity: "0"}
	} else if err != nil {
		return Position{}, err
	}
	if variantID != "" {
		presentations, presentationErr := s.variantPresentations(ctx, companyID, []string{variantID})
		if presentationErr != nil {
			return Position{}, presentationErr
		}
		if variant, ok := presentations[variantID]; ok {
			item.VariantCode, item.VariantDisplay = variant.Code, variant.Display
		}
	}
	return item, nil
}

// MovementListFilter is the optional, read-only filter set for the stock
// movement ledger. PostedAtTo is inclusive so date-only UI filters can cover
// the whole selected day without relying on database/session time zones.
type MovementListFilter struct {
	CompanyID    string
	WarehouseID  string
	ProductID    string
	Query        string
	Direction    string
	PostedAtFrom *time.Time
	PostedAtTo   *time.Time
	Limit        int
	UserID       string
}

func normalizeMovementListFilter(filter MovementListFilter) (MovementListFilter, error) {
	filter.CompanyID = strings.TrimSpace(filter.CompanyID)
	if _, err := requireUUID("company_id", filter.CompanyID); err != nil {
		return MovementListFilter{}, err
	}
	filter.WarehouseID = strings.TrimSpace(filter.WarehouseID)
	if filter.WarehouseID != "" {
		if _, err := requireUUID("warehouse_id", filter.WarehouseID); err != nil {
			return MovementListFilter{}, err
		}
	}
	filter.ProductID = strings.TrimSpace(filter.ProductID)
	if filter.ProductID != "" {
		if _, err := requireUUID("product_id", filter.ProductID); err != nil {
			return MovementListFilter{}, err
		}
	}
	filter.Query = strings.TrimSpace(filter.Query)
	if len(filter.Query) > 128 {
		return MovementListFilter{}, fmt.Errorf("%w: arama metni çok uzun", identity.ErrValidation)
	}
	filter.Direction = strings.ToUpper(strings.TrimSpace(filter.Direction))
	if filter.Direction != "" && filter.Direction != DirectionIn && filter.Direction != DirectionOut {
		return MovementListFilter{}, fmt.Errorf("%w: direction IN veya OUT olmalıdır", identity.ErrValidation)
	}
	if filter.PostedAtFrom != nil {
		value := filter.PostedAtFrom.UTC()
		filter.PostedAtFrom = &value
	}
	if filter.PostedAtTo != nil {
		value := filter.PostedAtTo.UTC()
		filter.PostedAtTo = &value
	}
	if filter.PostedAtFrom != nil && filter.PostedAtTo != nil && filter.PostedAtTo.Before(*filter.PostedAtFrom) {
		return MovementListFilter{}, fmt.Errorf("%w: tarih aralığı geçersiz", identity.ErrValidation)
	}
	if filter.Limit < 1 || filter.Limit > 200 {
		filter.Limit = 50
	}
	filter.UserID = strings.TrimSpace(filter.UserID)
	return filter, nil
}

// ListMovements keeps the original service signature for existing callers.
func (s *Service) ListMovements(ctx context.Context, companyID, warehouseID, productID string, limit int, userIDs ...string) ([]Movement, error) {
	return s.ListMovementsFiltered(ctx, MovementListFilter{
		CompanyID: companyID, WarehouseID: warehouseID, ProductID: productID,
		Limit: limit, UserID: optionalActor(userIDs),
	})
}

// ListMovementsFiltered lists only movements inside the company and the
// caller's warehouse scope. Optional posted_at, direction and product filters
// are applied in SQL so pagination/limit cannot leak out-of-scope rows.
func (s *Service) ListMovementsFiltered(ctx context.Context, filter MovementListFilter) ([]Movement, error) {
	filter, err := normalizeMovementListFilter(filter)
	if err != nil {
		return nil, err
	}
	args := []any{filter.CompanyID}
	query := `SELECT ` + movementColumns + ` FROM stock_movements WHERE company_id=$1 AND NOT EXISTS (SELECT 1 FROM warehouses hidden_w WHERE hidden_w.company_id=stock_movements.company_id AND hidden_w.id=stock_movements.warehouse_id AND hidden_w.is_system)`
	if filter.WarehouseID != "" {
		args = append(args, filter.WarehouseID)
		query += fmt.Sprintf(" AND warehouse_id=$%d", len(args))
		if err := ensureVisibleWarehouse(ctx, s.pool, filter.CompanyID, filter.UserID, filter.WarehouseID); err != nil {
			return nil, err
		}
	} else if filter.UserID != "" {
		args = append(args, filter.UserID)
		query += fmt.Sprintf(` AND warehouse_id IN (SELECT w.id FROM warehouses w WHERE w.company_id=$1 AND NOT w.is_system AND ((w.branch_id IS NULL OR NOT EXISTS (SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=$1 AND bs.user_id=$%d) OR EXISTS (SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=$1 AND bs.user_id=$%d AND bs.branch_id=w.branch_id)) AND (NOT EXISTS (SELECT 1 FROM membership_warehouse_scopes ws WHERE ws.company_id=$1 AND ws.user_id=$%d) OR EXISTS (SELECT 1 FROM membership_warehouse_scopes ws WHERE ws.company_id=$1 AND ws.user_id=$%d AND ws.warehouse_id=w.id))))`, len(args), len(args), len(args), len(args))
	}
	if filter.ProductID != "" {
		args = append(args, filter.ProductID)
		query += fmt.Sprintf(" AND product_id=$%d", len(args))
	}
	if filter.Query != "" {
		for _, token := range strings.Fields(strings.ToLower(filter.Query)) {
			args = append(args, "%"+token+"%")
			param := len(args)
			query += fmt.Sprintf(` AND (
				lower(CAST(movement_type AS text)) LIKE $%d
				OR lower(CASE movement_type
					WHEN 'MANUAL_ADJUSTMENT' THEN 'manuel düzeltme'
					WHEN 'PURCHASE_RECEIPT' THEN 'alış mal kabul'
					WHEN 'SALES_DISPATCH' THEN 'satış sevk'
					WHEN 'SALES_RETURN' THEN 'satış iadesi'
					WHEN 'PURCHASE_RETURN' THEN 'alış iadesi'
					WHEN 'TRANSFER_IN' THEN 'transfer giriş'
					WHEN 'TRANSFER_OUT' THEN 'transfer çıkış'
					WHEN 'COUNT_ADJUSTMENT' THEN 'sayım düzeltmesi'
					WHEN 'DAMAGE' THEN 'hasar'
					WHEN 'WASTE' THEN 'fire zayi'
					ELSE '' END) LIKE $%d
				OR lower(CAST(direction AS text)) LIKE $%d
				OR lower(CASE direction WHEN 'IN' THEN 'giriş' WHEN 'OUT' THEN 'çıkış' ELSE '' END) LIKE $%d
				OR lower(COALESCE(metadata->>'unit_code', '')) LIKE $%d
				OR lower(CAST(reason_code AS text)) LIKE $%d
				OR lower(CASE reason_code
					WHEN 'PURCHASE_RECEIPT' THEN 'alış mal kabul'
					WHEN 'SALES_DISPATCH' THEN 'satış sevk'
					WHEN 'SALES_RETURN' THEN 'satış iadesi'
					WHEN 'PURCHASE_RETURN' THEN 'alış iadesi'
					WHEN 'CORRECTION' THEN 'düzeltme'
					WHEN 'DAMAGE' THEN 'hasar'
					WHEN 'WASTE' THEN 'fire zayi'
					WHEN 'OPENING' THEN 'açılış'
					ELSE '' END) LIKE $%d
				OR lower(COALESCE(reason_description, '')) LIKE $%d
				OR CAST(quantity AS text) LIKE $%d
				OR EXISTS (
					SELECT 1 FROM products p
					WHERE p.company_id=stock_movements.company_id AND p.id=stock_movements.product_id
					AND (lower(COALESCE(p.code, '')) LIKE $%d OR lower(COALESCE(p.name, '')) LIKE $%d)
				)
				OR EXISTS (
					SELECT 1 FROM warehouses w
					WHERE w.company_id=stock_movements.company_id AND w.id=stock_movements.warehouse_id
					AND (lower(COALESCE(w.code, '')) LIKE $%d OR lower(COALESCE(w.name, '')) LIKE $%d)
				)
			)`, param, param, param, param, param, param, param, param, param, param, param, param, param)
		}
	}
	if filter.Direction != "" {
		args = append(args, filter.Direction)
		query += fmt.Sprintf(" AND direction=$%d", len(args))
	}
	if filter.PostedAtFrom != nil {
		args = append(args, *filter.PostedAtFrom)
		query += fmt.Sprintf(" AND posted_at >= $%d", len(args))
	}
	if filter.PostedAtTo != nil {
		args = append(args, *filter.PostedAtTo)
		query += fmt.Sprintf(" AND posted_at <= $%d", len(args))
	}
	args = append(args, filter.Limit)
	query += fmt.Sprintf(" ORDER BY posted_at DESC,id DESC LIMIT $%d", len(args))
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Movement, 0)
	for rows.Next() {
		item, scanErr := scanMovement(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := s.enrichMovementReferences(ctx, filter.CompanyID, result); err != nil {
		return nil, err
	}
	return result, nil
}

// GetMovement returns one immutable stock ledger row.  The company predicate
// is deliberately repeated here (rather than relying on the UUID alone), and
// warehouse scope is checked before the row is exposed to a caller.
func (s *Service) GetMovement(ctx context.Context, companyID, movementID string, userIDs ...string) (Movement, error) {
	if _, err := requireUUID("company_id", companyID); err != nil {
		return Movement{}, err
	}
	if _, err := requireUUID("movement_id", movementID); err != nil {
		return Movement{}, err
	}
	item, err := loadMovement(ctx, s.pool, companyID, movementID, false)
	if err != nil {
		return Movement{}, err
	}
	if err = ensureVisibleWarehouse(ctx, s.pool, companyID, optionalActor(userIDs), item.WarehouseID); err != nil {
		return Movement{}, err
	}
	decorated := []Movement{item}
	if err = s.enrichMovementReferences(ctx, companyID, decorated); err != nil {
		return Movement{}, err
	}
	item = decorated[0]
	return item, nil
}

// enrichMovementReferences adds display data for the immutable movement
// without denormalizing names into the stock ledger. Every relation is joined
// within the current company boundary; missing optional references remain
// empty and are omitted by the detail UI.
func (s *Service) enrichMovementReferences(ctx context.Context, companyID string, items []Movement) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]string, 0, len(items))
	byID := make(map[string]int, len(items))
	for index := range items {
		ids = append(ids, items[index].ID)
		byID[items[index].ID] = index
	}
	rows, err := s.pool.Query(ctx, `
		SELECT sm.id,
		       COALESCE(p.code, ''), COALESCE(p.name, ''),
		       COALESCE(v.variant_code, ''),
		       COALESCE((SELECT jsonb_object_agg(d.code, o.name)
		                   FROM product_variant_values vv
		                   JOIN variant_definitions d
		                     ON d.company_id = vv.company_id AND d.id = vv.definition_id
		                   JOIN variant_definition_options o
		                     ON o.company_id = vv.company_id
		                    AND o.definition_id = vv.definition_id
		                    AND o.id = vv.option_id
		                  WHERE vv.company_id = v.company_id AND vv.variant_id = v.id), '{}'::jsonb),
		       COALESCE(w.code, ''), COALESCE(w.name, ''),
		       COALESCE(lc.code, ''), COALESCE(lc.name, ''),
		       COALESCE(lot.lot_number, ''),
		       COALESCE(sn.serial_number, ''),
		       COALESCE((SELECT pu.unit_code
		                   FROM product_units pu
		                  WHERE pu.company_id = sm.company_id
		                    AND pu.product_id = sm.product_id
		                    AND pu.is_base
		                  LIMIT 1), '')
		       ,COALESCE(d.document_no, '')
		       ,COALESCE(d.document_type_code, '')
		       ,COALESCE((SELECT r.id::text FROM stock_movements r
		                  WHERE r.company_id = sm.company_id AND r.reversal_of_id = sm.id
		                  LIMIT 1), '')
		FROM stock_movements sm
		LEFT JOIN products p
		  ON p.company_id = sm.company_id AND p.id = sm.product_id
		LEFT JOIN product_variants v
		  ON v.company_id = sm.company_id AND v.id = sm.variant_id
		LEFT JOIN warehouses w
		  ON w.company_id = sm.company_id AND w.id = sm.warehouse_id
		LEFT JOIN locations lc
		  ON lc.company_id = sm.company_id AND lc.id = sm.location_id
		LEFT JOIN lots lot
		  ON lot.company_id = sm.company_id AND lot.id = sm.lot_id
		LEFT JOIN serial_numbers sn
		  ON sn.company_id = sm.company_id AND sn.id = sm.serial_id
		LEFT JOIN documents d
		  ON d.company_id = sm.company_id AND d.id = sm.source_id
		WHERE sm.company_id = $1 AND sm.id = ANY($2::uuid[])`, companyID, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var productCode, productName, variantCode string
		var variantAttributes []byte
		var warehouseCode, warehouseName, locationCode, locationName string
		var lotNumber, serialNumber, productBaseUnit, sourceDocumentNo, sourceDocumentType, reversedByID string
		if err := rows.Scan(&id, &productCode, &productName, &variantCode, &variantAttributes, &warehouseCode, &warehouseName, &locationCode, &locationName, &lotNumber, &serialNumber, &productBaseUnit, &sourceDocumentNo, &sourceDocumentType, &reversedByID); err != nil {
			return err
		}
		index, ok := byID[id]
		if !ok {
			continue
		}
		items[index].ProductCode = productCode
		items[index].ProductName = productName
		items[index].VariantCode = variantCode
		items[index].VariantDisplay = decodeVariantDisplay(variantAttributes)
		items[index].WarehouseCode = warehouseCode
		items[index].WarehouseName = warehouseName
		items[index].LocationCode = locationCode
		items[index].LocationName = locationName
		items[index].LotNumber = lotNumber
		items[index].SerialNumber = serialNumber
		items[index].StockUnit = productBaseUnit
		items[index].SourceDocumentNo = sourceDocumentNo
		items[index].SourceDocumentType = sourceDocumentType
		if reversedByID != "" {
			value := reversedByID
			items[index].ReversedByID = &value
		}
		if strings.TrimSpace(items[index].UnitCode) == "" {
			items[index].UnitCode = productBaseUnit
		}
	}
	return rows.Err()
}

func (s *Service) CreateWarehouse(ctx context.Context, input WarehouseInput) (Warehouse, error) {
	if uuid.Validate(strings.TrimSpace(input.ActorUserID)) != nil {
		return Warehouse{}, identity.ErrForbidden
	}
	companyID, err := requireUUID("company_id", input.CompanyID)
	if err != nil {
		return Warehouse{}, err
	}
	input.Code, input.Name, input.Address = strings.TrimSpace(input.Code), strings.TrimSpace(input.Name), strings.TrimSpace(input.Address)
	if input.Name == "" {
		return Warehouse{}, validationError("depo adı gereklidir")
	}
	if input.BranchID != "" {
		if _, err := requireUUID("branch_id", input.BranchID); err != nil {
			return Warehouse{}, err
		}
		if strings.TrimSpace(input.ActorUserID) != "" {
			var allowed bool
			if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM branches b WHERE b.company_id=$1 AND b.id=$2 AND b.is_active AND (NOT EXISTS(SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=$1 AND bs.user_id=$3) OR EXISTS(SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=$1 AND bs.user_id=$3 AND bs.branch_id=b.id)))`, companyID, input.BranchID, input.ActorUserID).Scan(&allowed); err != nil {
				return Warehouse{}, err
			} else if !allowed {
				return Warehouse{}, identity.ErrForbidden
			}
		}
	}
	input.Type = strings.ToUpper(strings.TrimSpace(input.Type))
	if input.Type == "" {
		input.Type = WarehouseStandard
	}
	if input.Type != WarehouseStandard && input.Type != WarehouseQuarantine && input.Type != WarehouseTransit && input.Type != WarehouseReturn {
		return Warehouse{}, validationError("geçersiz depo türü")
	}
	if input.Type == WarehouseTransit {
		return Warehouse{}, codeError(ErrWarehouseSystem.Error(), ErrWarehouseSystem, "sistem transit deposu kullanıcı tarafından oluşturulamaz")
	}
	if input.Code == "" {
		tx, txErr := s.begin(ctx)
		if txErr != nil {
			return Warehouse{}, txErr
		}
		defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
		input.Code, txErr = nextWarehouseCodeTx(ctx, tx, companyID)
		if txErr != nil {
			return Warehouse{}, txErr
		}
		branchID, branchErr := optionalUUID(input.BranchID)
		if branchErr != nil {
			return Warehouse{}, branchErr
		}
		responsibleID, responsibleErr := optionalUUID(input.ResponsibleUserID)
		if responsibleErr != nil {
			return Warehouse{}, responsibleErr
		}
		id := uuid.NewString()
		active := true
		if input.IsActive != nil {
			active = *input.IsActive
		}
		if _, txErr = tx.Exec(ctx, `INSERT INTO warehouses(id,company_id,branch_id,code,name,warehouse_type,address,responsible_user_id,uses_locations,is_transit,is_system,is_active) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, id, companyID, branchID, input.Code, input.Name, input.Type, input.Address, responsibleID, input.UsesLocations, input.Type == WarehouseTransit, input.Type == WarehouseTransit, active); txErr != nil {
			return Warehouse{}, mapInventoryError(txErr)
		}
		if txErr = tx.Commit(ctx); txErr != nil {
			return Warehouse{}, txErr
		}
		return s.getWarehouse(ctx, companyID, id)
	}
	branchID, err := optionalUUID(input.BranchID)
	if err != nil {
		return Warehouse{}, err
	}
	responsibleID, err := optionalUUID(input.ResponsibleUserID)
	if err != nil {
		return Warehouse{}, err
	}
	id := uuid.NewString()
	active := true
	if input.IsActive != nil {
		active = *input.IsActive
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO warehouses(id,company_id,branch_id,code,name,warehouse_type,address,responsible_user_id,uses_locations,is_transit,is_system,is_active)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, id, companyID, branchID, input.Code, input.Name, input.Type, input.Address, responsibleID, input.UsesLocations, input.Type == WarehouseTransit, input.Type == WarehouseTransit, active)
	if err != nil {
		return Warehouse{}, mapInventoryError(err)
	}
	return s.getWarehouse(ctx, companyID, id)
}

func nextWarehouseCodeTx(ctx context.Context, tx pgx.Tx, companyID string) (string, error) {
	year := time.Now().UTC().Year()
	if _, err := tx.Exec(ctx, `INSERT INTO company_business_sequences(company_id,sequence_key,sequence_year,next_value) VALUES($1,'DEP',$2,1) ON CONFLICT DO NOTHING`, companyID, year); err != nil {
		return "", err
	}
	var value int64
	if err := tx.QueryRow(ctx, `UPDATE company_business_sequences SET next_value=next_value+1 WHERE company_id=$1 AND sequence_key='DEP' AND sequence_year=$2 RETURNING next_value-1`, companyID, year).Scan(&value); err != nil {
		return "", err
	}
	return fmt.Sprintf("DEP-%06d", value), nil
}

const warehouseDeletionStatusTemplate = `CASE
	WHEN {{warehouse}}.is_system THEN 'SYSTEM'
	WHEN EXISTS (SELECT 1 FROM stock_movements sm WHERE sm.company_id={{warehouse}}.company_id AND sm.warehouse_id={{warehouse}}.id)
		OR EXISTS (SELECT 1 FROM stock_positions sp WHERE sp.company_id={{warehouse}}.company_id AND sp.warehouse_id={{warehouse}}.id)
		THEN 'HISTORY'
	WHEN EXISTS (SELECT 1 FROM warehouse_transfers wt WHERE wt.company_id={{warehouse}}.company_id AND ({{warehouse}}.id IN (wt.source_warehouse_id,wt.destination_warehouse_id,wt.transit_warehouse_id)))
		OR EXISTS (SELECT 1 FROM locations l WHERE l.company_id={{warehouse}}.company_id AND l.warehouse_id={{warehouse}}.id)
		OR EXISTS (SELECT 1 FROM stock_counts sc WHERE sc.company_id={{warehouse}}.company_id AND sc.warehouse_id={{warehouse}}.id)
		OR EXISTS (SELECT 1 FROM stock_cost_layers scl WHERE scl.company_id={{warehouse}}.company_id AND scl.warehouse_id={{warehouse}}.id)
		OR EXISTS (SELECT 1 FROM serial_numbers sn WHERE sn.company_id={{warehouse}}.company_id AND sn.active_warehouse_id={{warehouse}}.id)
		OR EXISTS (SELECT 1 FROM serial_number_events sne WHERE sne.company_id={{warehouse}}.company_id AND ({{warehouse}}.id IN (sne.from_warehouse_id,sne.to_warehouse_id)))
		OR EXISTS (SELECT 1 FROM documents d WHERE d.company_id={{warehouse}}.company_id AND d.warehouse_id={{warehouse}}.id)
		OR EXISTS (SELECT 1 FROM membership_warehouse_scopes mws WHERE mws.company_id={{warehouse}}.company_id AND mws.warehouse_id={{warehouse}}.id)
		OR EXISTS (SELECT 1 FROM api_token_warehouse_scopes atws WHERE atws.company_id={{warehouse}}.company_id AND atws.warehouse_id={{warehouse}}.id)
		OR EXISTS (SELECT 1 FROM warehouse_transfer_reservations wtr WHERE wtr.company_id={{warehouse}}.company_id AND wtr.warehouse_id={{warehouse}}.id)
		OR EXISTS (SELECT 1 FROM stock_movement_operations smo WHERE smo.company_id={{warehouse}}.company_id AND smo.warehouse_id={{warehouse}}.id)
		OR EXISTS (SELECT 1 FROM stock_count_engine_counts sce WHERE sce.company_id={{warehouse}}.company_id AND sce.warehouse_id={{warehouse}}.id)
		OR EXISTS (SELECT 1 FROM stock_count_engine_scopes scs WHERE scs.company_id={{warehouse}}.company_id AND scs.warehouse_id={{warehouse}}.id)
		OR EXISTS (SELECT 1 FROM purchase_orders po WHERE po.company_id={{warehouse}}.company_id AND po.warehouse_id={{warehouse}}.id)
		OR EXISTS (SELECT 1 FROM goods_receipts gr WHERE gr.company_id={{warehouse}}.company_id AND gr.warehouse_id={{warehouse}}.id)
		OR EXISTS (SELECT 1 FROM purchase_returns pr WHERE pr.company_id={{warehouse}}.company_id AND pr.warehouse_id={{warehouse}}.id)
		THEN 'DEPENDENCY'
	ELSE 'DELETABLE'
END`

// warehouseDeletionStatusExpression is the single source of truth for the
// deletion signal shown to clients and the server-side delete guard. The
// warehouse row is correlated instead of using positional parameters so the
// same expression can be used by both detail and list projections.
func warehouseDeletionStatusExpression(alias string) string {
	return strings.ReplaceAll(warehouseDeletionStatusTemplate, "{{warehouse}}", alias)
}

func (s *Service) getWarehouse(ctx context.Context, companyID, id string) (Warehouse, error) {
	var item Warehouse
	var branchID, responsibleID *string
	err := s.pool.QueryRow(ctx, `SELECT w.id,w.company_id,w.branch_id,w.code,w.name,w.warehouse_type,w.address,w.responsible_user_id,w.uses_locations,w.is_transit,w.is_system,w.is_active,w.version,w.created_at,w.updated_at,
		(`+warehouseDeletionStatusExpression("w")+`) = 'DELETABLE' AS can_delete
		FROM warehouses w WHERE w.company_id=$1 AND w.id=$2`, companyID, id).Scan(&item.ID, &item.CompanyID, &branchID, &item.Code, &item.Name, &item.Type, &item.Address, &responsibleID, &item.UsesLocations, &item.IsTransit, &item.IsSystem, &item.IsActive, &item.Version, &item.CreatedAt, &item.UpdatedAt, &item.CanDelete)
	item.BranchID, item.ResponsibleUserID = branchID, responsibleID
	if errors.Is(err, pgx.ErrNoRows) {
		return Warehouse{}, ErrNotFound
	}
	return item, err
}

// GetWarehouse is the company- and warehouse-scope-aware read surface used by
// detail cards.  getWarehouse remains private so internal create flows can
// re-read their just-created row without manufacturing an actor context.
func (s *Service) GetWarehouse(ctx context.Context, companyID, id string, userIDs ...string) (Warehouse, error) {
	if _, err := requireUUID("company_id", companyID); err != nil {
		return Warehouse{}, err
	}
	if _, err := requireUUID("warehouse_id", id); err != nil {
		return Warehouse{}, err
	}
	item, err := s.getWarehouse(ctx, companyID, id)
	if err != nil {
		return Warehouse{}, err
	}
	if item.IsSystem && item.IsTransit {
		return Warehouse{}, ErrNotFound
	}
	if err = ensureWarehouseScope(ctx, s.pool, companyID, optionalActor(userIDs), id); err != nil {
		return Warehouse{}, err
	}
	return item, nil
}

func normalizeWarehouseUpdate(input WarehouseUpdateInput) (WarehouseUpdateInput, error) {
	input.Code = strings.TrimSpace(input.Code)
	input.Name = strings.TrimSpace(input.Name)
	input.Type = strings.ToUpper(strings.TrimSpace(input.Type))
	input.Address = strings.TrimSpace(input.Address)
	if input.Code == "" || input.Name == "" {
		return WarehouseUpdateInput{}, fmt.Errorf("%w: depo kodu ve adı gereklidir", identity.ErrValidation)
	}
	switch input.Type {
	case WarehouseStandard, WarehouseQuarantine, WarehouseTransit, WarehouseReturn:
		return input, nil
	default:
		return WarehouseUpdateInput{}, fmt.Errorf("%w: geçersiz depo türü", identity.ErrValidation)
	}
}

func validateWarehouseTypeUnchanged(currentType, requestedType string) error {
	if currentType == requestedType {
		return nil
	}
	return codeError(ErrWarehouseTypeImmutable.Error(), ErrWarehouseTypeImmutable, "depo türü oluşturulduktan sonra değiştirilemez")
}

// UpdateWarehouse changes only lifecycle/master fields. Branch and location
// settings stay untouched by design. The row lock plus version predicate make
// reactivation/deactivation safe against concurrent edits and deletes.
func (s *Service) UpdateWarehouse(ctx context.Context, companyID, id string, expectedVersion int64, input WarehouseUpdateInput, actorUserID string) (Warehouse, error) {
	companyID, err := requireUUID("company_id", companyID)
	if err != nil {
		return Warehouse{}, err
	}
	id, err = requireUUID("warehouse_id", id)
	if err != nil {
		return Warehouse{}, err
	}
	if expectedVersion < 1 {
		return Warehouse{}, ErrConflict
	}
	input, err = normalizeWarehouseUpdate(input)
	if err != nil {
		return Warehouse{}, err
	}
	if err = ensureWarehouseScope(ctx, s.pool, companyID, actorUserID, id); err != nil {
		return Warehouse{}, err
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return Warehouse{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var currentType string
	var currentActive, isSystem, isTransit bool
	var currentVersion int64
	if err = tx.QueryRow(ctx, `SELECT warehouse_type,is_active,is_system,is_transit,version FROM warehouses WHERE company_id=$1 AND id=$2 FOR UPDATE`, companyID, id).Scan(&currentType, &currentActive, &isSystem, &isTransit, &currentVersion); errors.Is(err, pgx.ErrNoRows) {
		return Warehouse{}, ErrNotFound
	} else if err != nil {
		return Warehouse{}, err
	}
	if currentVersion != expectedVersion {
		return Warehouse{}, ErrConflict
	}
	if isSystem || isTransit {
		return Warehouse{}, codeError(ErrWarehouseSystem.Error(), ErrWarehouseSystem, "sistem transit deposu değiştirilemez")
	}
	if err = validateWarehouseTypeUnchanged(currentType, input.Type); err != nil {
		return Warehouse{}, err
	}
	active := currentActive
	if input.IsActive != nil {
		active = *input.IsActive
	}
	if isSystem && (!active || input.Type != WarehouseTransit) {
		return Warehouse{}, codeError(ErrWarehouseSystem.Error(), ErrWarehouseSystem, "sistem transit deposu pasifleştirilemez veya türü değiştirilemez")
	}
	if input.Type == WarehouseTransit && !isTransit {
		return Warehouse{}, codeError(ErrWarehouseSystem.Error(), ErrWarehouseSystem, "yeni sistem transit deposu bu işlemden oluşturulamaz")
	}
	if isTransit && input.Type != WarehouseTransit {
		return Warehouse{}, codeError(ErrWarehouseSystem.Error(), ErrWarehouseSystem, "sistem transit deposunun türü değiştirilemez")
	}
	if currentActive && !active {
		var hasOpenTransfer bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM warehouse_transfers
			WHERE company_id=$1
			  AND state IN ('DRAFT','REQUESTED','APPROVED','IN_TRANSIT','PARTIALLY_RECEIVED')
			  AND $2 IN (source_warehouse_id,destination_warehouse_id,transit_warehouse_id)
		)`, companyID, id).Scan(&hasOpenTransfer); err != nil {
			return Warehouse{}, err
		}
		if hasOpenTransfer {
			return Warehouse{}, codeError(ErrWarehouseHasOpenTransfer.Error(), ErrWarehouseHasOpenTransfer, "devam eden transferi bulunan depo pasife alınamaz")
		}
	}
	result, err := tx.Exec(ctx, `UPDATE warehouses
		SET code=$1,name=$2,warehouse_type=$3,address=$4,is_active=$5,updated_at=now(),version=version+1
		WHERE company_id=$6 AND id=$7 AND version=$8`, input.Code, input.Name, input.Type, input.Address, active, companyID, id, expectedVersion)
	if err != nil {
		return Warehouse{}, mapInventoryError(err)
	}
	if result.RowsAffected() == 0 {
		return Warehouse{}, ErrConflict
	}
	if err = tx.Commit(ctx); err != nil {
		return Warehouse{}, err
	}
	return s.getWarehouse(ctx, companyID, id)
}

// DeleteWarehouse is intentionally stricter than deactivation. It locks the
// warehouse row before checking every known dependent table; PostgreSQL's FK
// key-share locking then closes the check/delete race with new references.
func (s *Service) DeleteWarehouse(ctx context.Context, companyID, id string, expectedVersion int64, actorUserID string) error {
	companyID, err := requireUUID("company_id", companyID)
	if err != nil {
		return err
	}
	id, err = requireUUID("warehouse_id", id)
	if err != nil {
		return err
	}
	if expectedVersion < 1 {
		return ErrConflict
	}
	if err = ensureWarehouseScope(ctx, s.pool, companyID, actorUserID, id); err != nil {
		return err
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var isSystem bool
	var version int64
	if err = tx.QueryRow(ctx, `SELECT is_system,version FROM warehouses WHERE company_id=$1 AND id=$2 FOR UPDATE`, companyID, id).Scan(&isSystem, &version); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if version != expectedVersion {
		return ErrConflict
	}
	if isSystem {
		return codeError(ErrWarehouseSystem.Error(), ErrWarehouseSystem, "sistem deposu silinemez")
	}
	var deletionStatus string
	if err = tx.QueryRow(ctx, `SELECT `+warehouseDeletionStatusExpression("w")+` FROM warehouses w WHERE w.company_id=$1 AND w.id=$2`, companyID, id).Scan(&deletionStatus); err != nil {
		return err
	}
	switch deletionStatus {
	case "HISTORY":
		return codeError(ErrWarehouseHasHistory.Error(), ErrWarehouseHasHistory, "bu depoda hareket bulunduğu için silinemez")
	case "DEPENDENCY":
		return codeError(ErrWarehouseInUse.Error(), ErrWarehouseInUse, "depo ilişkili kayıtlar nedeniyle silinemez")
	}
	result, err := tx.Exec(ctx, `DELETE FROM warehouses WHERE company_id=$1 AND id=$2 AND version=$3`, companyID, id, expectedVersion)
	if err != nil {
		return mapInventoryError(err)
	}
	if result.RowsAffected() == 0 {
		return ErrConflict
	}
	return tx.Commit(ctx)
}

func (s *Service) ListWarehouses(ctx context.Context, companyID string, includeInactive bool, userIDs ...string) ([]Warehouse, error) {
	return s.ListWarehousesFiltered(ctx, companyID, includeInactive, "", userIDs...)
}

// ListWarehousesFiltered lists company-scoped warehouses and applies the
// optional code/name/address search before ordering the result.
func (s *Service) ListWarehousesFiltered(ctx context.Context, companyID string, includeInactive bool, search string, userIDs ...string) ([]Warehouse, error) {
	return s.listWarehousesFiltered(ctx, companyID, includeInactive, search, "", userIDs...)
}

// ListWarehousesFilteredForBranch adds an explicit branch scope to the
// company/user-authorized warehouse list.  The old method remains available
// for screens that intentionally list all accessible branches.
func (s *Service) ListWarehousesFilteredForBranch(ctx context.Context, companyID string, includeInactive bool, search, branchID string, userIDs ...string) ([]Warehouse, error) {
	return s.listWarehousesFiltered(ctx, companyID, includeInactive, search, branchID, userIDs...)
}

func (s *Service) listWarehousesFiltered(ctx context.Context, companyID string, includeInactive bool, search, branchID string, userIDs ...string) ([]Warehouse, error) {
	if _, err := requireUUID("company_id", companyID); err != nil {
		return nil, err
	}
	branchID = strings.TrimSpace(branchID)
	if branchID != "" {
		if _, err := requireUUID("branch_id", branchID); err != nil {
			return nil, err
		}
	}
	search = strings.TrimSpace(search)
	if len(search) > 128 {
		return nil, fmt.Errorf("%w: arama metni çok uzun", identity.ErrValidation)
	}
	args := []any{companyID}
	query := `SELECT w.id,w.company_id,w.branch_id,w.code,w.name,w.warehouse_type,w.address,w.responsible_user_id,w.uses_locations,w.is_transit,w.is_system,w.is_active,w.version,w.created_at,w.updated_at,
		(` + warehouseDeletionStatusExpression("w") + `) = 'DELETABLE' AS can_delete
		FROM warehouses w WHERE w.company_id=$1 AND NOT w.is_system`
	if !includeInactive {
		query += ` AND w.is_active`
	}
	if branchID != "" {
		args = append(args, branchID)
		query += fmt.Sprintf(` AND w.branch_id=$%d::uuid`, len(args))
	}
	for _, token := range strings.Fields(strings.ToLower(search)) {
		args = append(args, "%"+token+"%")
		param := len(args)
		query += fmt.Sprintf(` AND (
			w.code ILIKE $%d
			OR w.name ILIKE $%d
			OR COALESCE(w.address, '') ILIKE $%d
		)`, param, param, param)
	}
	if userID := optionalActor(userIDs); userID != "" {
		args = append(args, userID)
		query += fmt.Sprintf(` AND ((w.branch_id IS NULL OR NOT EXISTS(SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=$1 AND bs.user_id=$%d) OR EXISTS(SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=$1 AND bs.user_id=$%d AND bs.branch_id=w.branch_id)) AND (NOT EXISTS(SELECT 1 FROM membership_warehouse_scopes ws WHERE ws.company_id=$1 AND ws.user_id=$%d) OR EXISTS(SELECT 1 FROM membership_warehouse_scopes ws WHERE ws.company_id=$1 AND ws.user_id=$%d AND ws.warehouse_id=w.id)))`, len(args), len(args), len(args), len(args))
	}
	query += ` ORDER BY lower(name),id`
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Warehouse{}
	for rows.Next() {
		var item Warehouse
		if err = rows.Scan(&item.ID, &item.CompanyID, &item.BranchID, &item.Code, &item.Name, &item.Type, &item.Address, &item.ResponsibleUserID, &item.UsesLocations, &item.IsTransit, &item.IsSystem, &item.IsActive, &item.Version, &item.CreatedAt, &item.UpdatedAt, &item.CanDelete); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Service) CreateLocation(ctx context.Context, input LocationInput) (Location, error) {
	companyID, err := requireUUID("company_id", input.CompanyID)
	if err != nil {
		return Location{}, err
	}
	warehouseID, err := requireUUID("warehouse_id", input.WarehouseID)
	if err != nil {
		return Location{}, err
	}
	if err = ensureWarehouseAccess(ctx, s.pool, companyID, input.ActorUserID, warehouseID); err != nil {
		return Location{}, err
	}
	parentID, err := optionalUUID(input.ParentID)
	if err != nil {
		return Location{}, err
	}
	input.Code, input.Name = strings.TrimSpace(input.Code), strings.TrimSpace(input.Name)
	if input.Code == "" || input.Name == "" {
		return Location{}, validationError("lokasyon kodu ve adı gereklidir")
	}
	active := true
	if input.IsActive != nil {
		active = *input.IsActive
	}
	id := uuid.NewString()
	_, err = s.pool.Exec(ctx, `INSERT INTO locations(id,company_id,warehouse_id,parent_id,code,name,is_active) VALUES($1,$2,$3,$4,$5,$6,$7)`, id, companyID, warehouseID, parentID, input.Code, input.Name, active)
	if err != nil {
		return Location{}, mapInventoryError(err)
	}
	return s.getLocation(ctx, companyID, id)
}

func (s *Service) getLocation(ctx context.Context, companyID, id string) (Location, error) {
	var item Location
	err := s.pool.QueryRow(ctx, `SELECT id,company_id,warehouse_id,parent_id,code,name,is_active,version,created_at,updated_at FROM locations WHERE company_id=$1 AND id=$2`, companyID, id).Scan(&item.ID, &item.CompanyID, &item.WarehouseID, &item.ParentID, &item.Code, &item.Name, &item.IsActive, &item.Version, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Location{}, ErrNotFound
	}
	return item, err
}

func (s *Service) ListLocations(ctx context.Context, companyID, warehouseID string, includeInactive bool, userIDs ...string) ([]Location, error) {
	if _, err := requireUUID("company_id", companyID); err != nil {
		return nil, err
	}
	if _, err := requireUUID("warehouse_id", warehouseID); err != nil {
		return nil, err
	}
	if err := ensureWarehouseAccess(ctx, s.pool, companyID, optionalActor(userIDs), warehouseID); err != nil {
		return nil, err
	}
	query := `SELECT l.id,l.company_id,l.warehouse_id,l.parent_id,l.code,l.name,l.is_active,l.version,l.created_at,l.updated_at
		FROM locations l JOIN warehouses w ON w.company_id=l.company_id AND w.id=l.warehouse_id
		WHERE l.company_id=$1 AND l.warehouse_id=$2 AND w.is_active`
	if !includeInactive {
		query += ` AND l.is_active`
	}
	query += ` ORDER BY l.parent_id NULLS FIRST,lower(l.name),l.id`
	rows, err := s.pool.Query(ctx, query, companyID, warehouseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Location{}
	for rows.Next() {
		var item Location
		if err = rows.Scan(&item.ID, &item.CompanyID, &item.WarehouseID, &item.ParentID, &item.Code, &item.Name, &item.IsActive, &item.Version, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Service) EnsureTransitWarehouse(ctx context.Context, companyID, branchID string) (Warehouse, error) {
	companyID, err := requireUUID("company_id", companyID)
	if err != nil {
		return Warehouse{}, err
	}
	branch, err := optionalUUID(branchID)
	if err != nil {
		return Warehouse{}, err
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return Warehouse{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var id string
	if err = tx.QueryRow(ctx, `SELECT id FROM warehouses WHERE company_id=$1 AND is_transit AND is_system AND warehouse_type='TRANSIT' AND is_active FOR UPDATE`, companyID).Scan(&id); err == nil {
		if err = tx.Commit(ctx); err != nil {
			return Warehouse{}, err
		}
		return s.getWarehouse(ctx, companyID, id)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return Warehouse{}, err
	}
	id = uuid.NewString()
	code := "SYS-TRANSIT"
	if _, err = tx.Exec(ctx, `SELECT set_config('varyaone.allow_system_transit','on',true)`); err != nil {
		return Warehouse{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO warehouses(id,company_id,branch_id,code,name,warehouse_type,address,uses_locations,is_transit,is_system)
		VALUES($1,$2,$3,$4,'Sistem Transit Deposu','TRANSIT','',false,true,true)`, id, companyID, branch, code)
	if err != nil {
		// A user may already have used the conventional code.  is_transit is
		// still unique, so retry with a UUID-derived code inside this transaction.
		if strings.Contains(strings.ToLower(err.Error()), "warehouses_company_id_code") || strings.Contains(strings.ToLower(err.Error()), "duplicate key") {
			code = "SYS-TRANSIT-" + strings.ToUpper(strings.ReplaceAll(id[:8], "-", ""))
			if _, err = tx.Exec(ctx, `INSERT INTO warehouses(id,company_id,branch_id,code,name,warehouse_type,address,uses_locations,is_transit,is_system)
				VALUES($1,$2,$3,$4,'Sistem Transit Deposu','TRANSIT','',false,true,true)`, id, companyID, branch, code); err != nil {
				return Warehouse{}, mapInventoryError(err)
			}
		} else {
			return Warehouse{}, mapInventoryError(err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return Warehouse{}, err
	}
	return s.getWarehouse(ctx, companyID, id)
}

func parseNonNegative(name, value string) (string, error) { return cleanQuantity(name, value, false) }

func decimalCompare(left, right string) int {
	l, lok := new(big.Rat).SetString(left)
	r, rok := new(big.Rat).SetString(right)
	if !lok || !rok {
		return 0
	}
	return l.Cmp(r)
}

func decimalAbs(value string) string {
	rat, ok := new(big.Rat).SetString(value)
	if !ok {
		return value
	}
	rat.Abs(rat)
	return formatRat(rat)
}

func movementForTransfer(companyID, warehouseID, locationID, productID, variantID, lotID, serialID, movementType, direction, quantity, transferID, lineID, key string) MovementInput {
	return MovementInput{
		CompanyID: companyID, WarehouseID: warehouseID, LocationID: locationID, ProductID: productID,
		VariantID: variantID, LotID: lotID, SerialID: serialID, MovementType: movementType, Direction: direction,
		Quantity: quantity, ReasonCode: "TRANSFER", ReasonDescription: "Depo transferi", SourceType: "WAREHOUSE_TRANSFER",
		SourceID: transferID, SourceLineID: lineID, IdempotencyKey: key, Metadata: map[string]any{"transfer_id": transferID, "transfer_line_id": lineID},
	}
}

// transferCostBasis carries FIFO's consumed cost into the next physical
// position. A transfer may consume more than one layer; a weighted average is
// used only when all consumed layers share one currency. Mixed-currency layers
// remain explicitly unpriced rather than producing a misleading conversion.
func transferCostBasis(ctx context.Context, tx txDB, companyID, movementID string) (string, string, error) {
	var unitCost, currency *string
	err := tx.QueryRow(ctx, `SELECT
		CASE WHEN COUNT(*) > 0 AND COUNT(DISTINCT currency) = 1
			THEN ROUND(SUM(quantity * unit_cost) / NULLIF(SUM(quantity), 0), 8)::text END,
		CASE WHEN COUNT(*) > 0 AND COUNT(DISTINCT currency) = 1 THEN MIN(currency) END
		FROM stock_cost_consumptions WHERE company_id=$1 AND movement_id=$2`, companyID, movementID).Scan(&unitCost, &currency)
	if err != nil {
		return "", "", err
	}
	if unitCost == nil || currency == nil {
		return "", "", nil
	}
	return *unitCost, *currency, nil
}

// reserveTransferCreateCommand makes transfer creation retry-safe. This is
// essential for QUICK because creation also ships stock; an HTTP timeout must
// not create a second aggregate and withdraw the source quantity twice.
func reserveTransferCreateCommand(ctx context.Context, tx pgx.Tx, input TransferInput) (string, bool, error) {
	key := strings.TrimSpace(input.IdempotencyKey)
	if key == "" {
		return "", false, nil
	}
	if len(key) > 255 {
		return "", false, fmt.Errorf("%w: idempotency anahtarı çok uzun", identity.ErrValidation)
	}
	payloadInput := input
	payloadInput.IdempotencyKey = ""
	payload, err := json.Marshal(payloadInput)
	if err != nil {
		return "", false, err
	}
	digest := sha256.Sum256(payload)
	command := "inventory.transfer.create"
	actor, err := optionalUUID(input.RequestedBy)
	if err != nil {
		return "", false, err
	}
	result, err := tx.Exec(ctx, `INSERT INTO command_idempotency_records(
		company_id,idempotency_key,command_name,payload_sha256,actor_user_id)
		VALUES($1,$2,$3,$4,$5)
		ON CONFLICT (company_id,idempotency_key) DO NOTHING`,
		input.CompanyID, key, command, fmt.Sprintf("%x", digest[:]), actor)
	if err != nil {
		return "", false, err
	}
	inserted := result.RowsAffected() == 1
	var existingCommand, existingHash, status string
	var responseBody []byte
	if err = tx.QueryRow(ctx, `SELECT command_name,payload_sha256,status,response_body
		FROM command_idempotency_records
		WHERE company_id=$1 AND idempotency_key=$2
		FOR UPDATE`, input.CompanyID, key).Scan(&existingCommand, &existingHash, &status, &responseBody); err != nil {
		return "", false, err
	}
	if existingCommand != command || existingHash != fmt.Sprintf("%x", digest[:]) {
		return "", false, codeError(ErrIdempotencyConflict.Error(), ErrIdempotencyConflict, "aynı Idempotency-Key farklı içerikle kullanıldı")
	}
	if inserted {
		return "", false, nil
	}
	if status != "COMPLETED" {
		return "", false, codeError(ErrConflict.Error(), ErrConflict, "aynı transfer oluşturma komutu devam ediyor")
	}
	var response struct {
		TransferID string `json:"transfer_id"`
	}
	if err = json.Unmarshal(responseBody, &response); err != nil || strings.TrimSpace(response.TransferID) == "" {
		return "", false, codeError(ErrConflict.Error(), ErrConflict, "transfer oluşturma komutunun sonucu okunamadı")
	}
	return response.TransferID, true, nil
}

func (s *Service) CreateTransfer(ctx context.Context, input TransferInput) (Transfer, error) {
	companyID, err := requireUUID("company_id", input.CompanyID)
	if err != nil {
		return Transfer{}, err
	}
	sourceWarehouse, err := requireUUID("source_warehouse_id", input.SourceWarehouseID)
	if err != nil {
		return Transfer{}, err
	}
	destinationWarehouse, err := requireUUID("destination_warehouse_id", input.DestinationWarehouseID)
	if err != nil {
		return Transfer{}, err
	}
	if sourceWarehouse == destinationWarehouse || len(input.Lines) == 0 {
		if sourceWarehouse == destinationWarehouse {
			return Transfer{}, codeError(ErrTransferSameWarehouse.Error(), ErrTransferSameWarehouse, "çıkış ve varış deposu aynı olamaz")
		}
		return Transfer{}, fmt.Errorf("%w: transfer kaynağı, hedefi ve en az bir satırı gereklidir", identity.ErrValidation)
	}
	transferType := strings.ToUpper(strings.TrimSpace(input.TransferType))
	if transferType == "" {
		transferType = TransferTypeWorkflow
	}
	if !validTransferType(transferType) {
		return Transfer{}, fmt.Errorf("%w: transfer tipi geçersiz", identity.ErrValidation)
	}
	requestedBy, err := optionalUUID(input.RequestedBy)
	if err != nil {
		return Transfer{}, err
	}
	if err = ensureStandardWarehouse(ctx, s.pool, companyID, input.RequestedBy, sourceWarehouse); err != nil {
		return Transfer{}, err
	}
	if err = ensureWarehouseAccess(ctx, s.pool, companyID, input.RequestedBy, destinationWarehouse); err != nil {
		return Transfer{}, err
	}
	var destinationType string
	if err = s.pool.QueryRow(ctx, `SELECT warehouse_type FROM warehouses WHERE company_id=$1 AND id=$2`, companyID, destinationWarehouse).Scan(&destinationType); err != nil {
		return Transfer{}, err
	}
	if destinationType == WarehouseTransit {
		return Transfer{}, codeError(ErrWarehouseNotStandard.Error(), ErrWarehouseNotStandard, "transfer hedefi sistem transit deposu olamaz")
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return Transfer{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	replayID, replay, err := reserveTransferCreateCommand(ctx, tx, input)
	if err != nil {
		return Transfer{}, err
	}
	if replay {
		if err = tx.Rollback(context.WithoutCancel(ctx)); err != nil {
			return Transfer{}, err
		}
		return s.GetTransfer(ctx, companyID, replayID, input.RequestedBy)
	}
	transitWarehouse := strings.TrimSpace(input.TransitWarehouseID)
	if transitWarehouse == "" {
		transitWarehouse, err = ensureTransitTx(ctx, tx, companyID, "")
	} else {
		transitWarehouse, err = requireUUID("transit_warehouse_id", transitWarehouse)
		if err == nil {
			var valid bool
			err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM warehouses WHERE company_id=$1 AND id=$2 AND is_active AND is_transit AND is_system AND warehouse_type='TRANSIT')`, companyID, transitWarehouse).Scan(&valid)
			if err == nil && !valid {
				err = fmt.Errorf("%w: geçerli sistem transit deposu bulunamadı", identity.ErrValidation)
			}
		}
	}
	if err != nil {
		return Transfer{}, err
	}
	if err = ensureWarehouseAccess(ctx, tx, companyID, input.RequestedBy, transitWarehouse); err != nil {
		return Transfer{}, err
	}
	id := uuid.NewString()
	transferNo := strings.TrimSpace(input.TransferNo)
	if transferNo == "" {
		transferNo, err = nextInventoryNumberTx(ctx, tx, companyID, "TRF", time.Now().UTC())
		if err != nil {
			return Transfer{}, err
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO warehouse_transfers(id,company_id,transfer_no,transfer_type,source_warehouse_id,destination_warehouse_id,transit_warehouse_id,requested_by)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, id, companyID, transferNo, transferType, sourceWarehouse, destinationWarehouse, transitWarehouse, requestedBy)
	if err != nil {
		return Transfer{}, mapInventoryError(err)
	}
	for index, line := range input.Lines {
		productID, parseErr := requireUUID("product_id", line.ProductID)
		if parseErr != nil {
			return Transfer{}, parseErr
		}
		quantity, parseErr := cleanQuantity("quantity", line.Quantity, true)
		if parseErr != nil {
			return Transfer{}, parseErr
		}
		variantID, parseErr := optionalUUID(line.VariantID)
		if parseErr != nil {
			return Transfer{}, fmt.Errorf("variant_id is invalid: %w", parseErr)
		}
		if _, parseErr = validateInventoryVariantTx(ctx, tx, companyID, productID, valueOrEmptyAny(variantID)); parseErr != nil {
			return Transfer{}, parseErr
		}
		lotID, parseErr := optionalUUID(line.LotID)
		if parseErr != nil {
			return Transfer{}, fmt.Errorf("lot_id is invalid: %w", parseErr)
		}
		serialID, parseErr := optionalUUID(line.SerialID)
		if parseErr != nil {
			return Transfer{}, fmt.Errorf("serial_id is invalid: %w", parseErr)
		}
		sourceLocation, parseErr := optionalUUID(line.SourceLocationID)
		if parseErr != nil {
			return Transfer{}, fmt.Errorf("source_location_id is invalid: %w", parseErr)
		}
		destinationLocation, parseErr := optionalUUID(line.DestinationLocationID)
		if parseErr != nil {
			return Transfer{}, fmt.Errorf("destination_location_id is invalid: %w", parseErr)
		}
		lineID := uuid.NewString()
		_, err = tx.Exec(ctx, `INSERT INTO warehouse_transfer_lines(id,company_id,transfer_id,line_no,product_id,variant_id,lot_id,serial_id,source_location_id,destination_location_id,quantity)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, lineID, companyID, id, index+1, productID, variantID, lotID, serialID, sourceLocation, destinationLocation, quantity)
		if err != nil {
			return Transfer{}, mapInventoryError(err)
		}
	}
	if transferType == TransferTypeQuick {
		if err = postQuickTransferTx(ctx, tx, companyID, id, sourceWarehouse, destinationWarehouse, transitWarehouse, input.RequestedBy); err != nil {
			return Transfer{}, err
		}
	} else if transferType == TransferTypeWorkflow {
		// The user-facing workflow starts when the transfer is created: reserve
		// and ship the source quantity atomically, then leave delivery for the
		// explicit receive command. APPROVED is used only as an internal,
		// transaction-local state so the movement guard can validate the source
		// shipment; it is never committed as the created transfer state.
		if _, err = tx.Exec(ctx, `UPDATE warehouse_transfers
			SET state='APPROVED',approved_by=COALESCE(approved_by,NULLIF($2,'')::uuid),approved_at=now(),version=version+1,updated_at=now()
			WHERE company_id=$1 AND id=$3 AND state='DRAFT'`, companyID, input.RequestedBy, id); err != nil {
			return Transfer{}, mapInventoryError(err)
		}
		if err = s.postWorkflowShipmentTx(ctx, tx, companyID, id, input.RequestedBy); err != nil {
			return Transfer{}, err
		}
	}
	if err = writeInventoryAuditTx(ctx, tx, companyID, input.RequestedBy, "INVENTORY_TRANSFER_CREATED", id, map[string]any{"transfer_type": transferType, "line_count": len(input.Lines)}); err != nil {
		return Transfer{}, err
	}
	if strings.TrimSpace(input.IdempotencyKey) != "" {
		result, updateErr := tx.Exec(ctx, `UPDATE command_idempotency_records
			SET status='COMPLETED',response_status=201,
				response_body=jsonb_build_object('transfer_id',$3::text),completed_at=now()
			WHERE company_id=$1 AND idempotency_key=$2 AND status='IN_PROGRESS'`,
			companyID, strings.TrimSpace(input.IdempotencyKey), id)
		if updateErr != nil {
			return Transfer{}, updateErr
		}
		if result.RowsAffected() != 1 {
			return Transfer{}, ErrConflict
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return Transfer{}, err
	}
	return s.GetTransfer(ctx, companyID, id, input.RequestedBy)
}

// postQuickTransferTx completes a quick transfer while the creation
// transaction is still open. Quick transfers have no separate delivery step:
// source -> transit -> destination is posted atomically and the aggregate is
// returned as RECEIVED.
func postQuickTransferTx(ctx context.Context, tx pgx.Tx, companyID, transferID, sourceWarehouse, destinationWarehouse, transitWarehouse, actor string) error {
	rows, err := tx.Query(ctx, `SELECT id,product_id,variant_id,lot_id,serial_id,source_location_id,destination_location_id,quantity::text
		FROM warehouse_transfer_lines WHERE company_id=$1 AND transfer_id=$2 ORDER BY line_no FOR UPDATE`, companyID, transferID)
	if err != nil {
		return err
	}
	type line struct {
		id, productID, quantity                                             string
		variantID, lotID, serialID, sourceLocationID, destinationLocationID *string
	}
	lines := make([]line, 0)
	for rows.Next() {
		var item line
		if err = rows.Scan(&item.id, &item.productID, &item.variantID, &item.lotID, &item.serialID, &item.sourceLocationID, &item.destinationLocationID, &item.quantity); err != nil {
			rows.Close()
			return err
		}
		lines = append(lines, item)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	// QUICK uses the same ledger guards as workflow transfers. Move the
	// aggregate to APPROVED before posting stock so database movement guards can
	// verify that a source withdrawal is an authorized shipment. The surrounding
	// transaction still makes every lifecycle and movement change atomic.
	if _, err = tx.Exec(ctx, `UPDATE warehouse_transfers SET state='REQUESTED',requested_by=COALESCE(requested_by,NULLIF($3,'')::uuid),requested_at=now(),version=version+1,updated_at=now() WHERE company_id=$1 AND id=$2`, companyID, transferID, actor); err != nil {
		return mapInventoryError(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE warehouse_transfers SET state='APPROVED',approved_by=COALESCE(approved_by,NULLIF($3,'')::uuid),approved_at=now(),version=version+1,updated_at=now() WHERE company_id=$1 AND id=$2`, companyID, transferID, actor); err != nil {
		return mapInventoryError(err)
	}
	for _, line := range lines {
		out := movementForTransfer(companyID, sourceWarehouse, valueOrEmpty(line.sourceLocationID), line.productID, valueOrEmpty(line.variantID), valueOrEmpty(line.lotID), valueOrEmpty(line.serialID), MovementTransferOut, DirectionOut, line.quantity, transferID, line.id, "transfer:"+transferID+":"+line.id+":quick-out")
		out.ActorUserID = actor
		outMovement, postErr := postMovementTx(ctx, tx, out, movementHash(out, false))
		if postErr != nil {
			return mapInventoryError(postErr)
		}
		in := movementForTransfer(companyID, transitWarehouse, "", line.productID, valueOrEmpty(line.variantID), valueOrEmpty(line.lotID), valueOrEmpty(line.serialID), MovementTransferIn, DirectionIn, line.quantity, transferID, line.id, "transfer:"+transferID+":"+line.id+":quick-transit-in")
		in.ActorUserID = actor
		in.UnitCost, in.Currency, err = transferCostBasis(ctx, tx, companyID, outMovement.ID)
		if err != nil {
			return err
		}
		if _, err = postMovementTx(ctx, tx, in, movementHash(in, false)); err != nil {
			return mapInventoryError(err)
		}
		if _, err = tx.Exec(ctx, `UPDATE warehouse_transfer_lines SET shipped_quantity=quantity WHERE company_id=$1 AND id=$2`, companyID, line.id); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE warehouse_transfers SET state='IN_TRANSIT',shipped_at=now(),version=version+1,updated_at=now() WHERE company_id=$1 AND id=$2`, companyID, transferID); err != nil {
		return mapInventoryError(err)
	}
	for _, line := range lines {
		out := movementForTransfer(companyID, transitWarehouse, "", line.productID, valueOrEmpty(line.variantID), valueOrEmpty(line.lotID), valueOrEmpty(line.serialID), MovementTransferOut, DirectionOut, line.quantity, transferID, line.id, "transfer:"+transferID+":"+line.id+":quick-destination-out")
		out.ActorUserID = actor
		outMovement, postErr := postMovementTx(ctx, tx, out, movementHash(out, false))
		if postErr != nil {
			return mapInventoryError(postErr)
		}
		in := movementForTransfer(companyID, destinationWarehouse, valueOrEmpty(line.destinationLocationID), line.productID, valueOrEmpty(line.variantID), valueOrEmpty(line.lotID), valueOrEmpty(line.serialID), MovementTransferIn, DirectionIn, line.quantity, transferID, line.id, "transfer:"+transferID+":"+line.id+":quick-destination-in")
		in.ActorUserID = actor
		in.UnitCost, in.Currency, err = transferCostBasis(ctx, tx, companyID, outMovement.ID)
		if err != nil {
			return err
		}
		if _, err = postMovementTx(ctx, tx, in, movementHash(in, false)); err != nil {
			return mapInventoryError(err)
		}
		if _, err = tx.Exec(ctx, `UPDATE warehouse_transfer_lines SET received_quantity=quantity WHERE company_id=$1 AND id=$2`, companyID, line.id); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE warehouse_transfers SET state='RECEIVED',received_at=now(),version=version+1,updated_at=now() WHERE company_id=$1 AND id=$2`, companyID, transferID); err != nil {
		return mapInventoryError(err)
	}
	return nil
}

func nextInventoryNumberTx(ctx context.Context, tx pgx.Tx, companyID, prefix string, date time.Time) (string, error) {
	year := date.Year()
	if _, err := tx.Exec(ctx, `INSERT INTO company_business_sequences(company_id,sequence_key,sequence_year,next_value) VALUES($1,$2,$3,1) ON CONFLICT DO NOTHING`, companyID, prefix, year); err != nil {
		return "", err
	}
	var value int64
	if err := tx.QueryRow(ctx, `UPDATE company_business_sequences SET next_value=next_value+1 WHERE company_id=$1 AND sequence_key=$2 AND sequence_year=$3 RETURNING next_value-1`, companyID, prefix, year).Scan(&value); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-%04d-%06d", prefix, year, value), nil
}

func ensureTransitTx(ctx context.Context, tx txDB, companyID, branchID string) (string, error) {
	var id string
	if err := tx.QueryRow(ctx, `SELECT id FROM warehouses WHERE company_id=$1 AND is_transit AND is_system AND warehouse_type='TRANSIT' AND is_active FOR UPDATE`, companyID).Scan(&id); err == nil {
		return id, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	branch, err := optionalUUID(branchID)
	if err != nil {
		return "", err
	}
	id = uuid.NewString()
	code := "SYS-TRANSIT"
	if _, err = tx.Exec(ctx, `SELECT set_config('varyaone.allow_system_transit','on',true)`); err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx, `INSERT INTO warehouses(id,company_id,branch_id,code,name,warehouse_type,address,uses_locations,is_transit,is_system)
		VALUES($1,$2,$3,$4,'Sistem Transit Deposu','TRANSIT','',false,true,true)`, id, companyID, branch, code)
	if err != nil {
		code = "SYS-TRANSIT-" + strings.ToUpper(id[:8])
		if _, err = tx.Exec(ctx, `INSERT INTO warehouses(id,company_id,branch_id,code,name,warehouse_type,address,uses_locations,is_transit,is_system)
			VALUES($1,$2,$3,$4,'Sistem Transit Deposu','TRANSIT','',false,true,true)`, id, companyID, branch, code); err != nil {
			return "", mapInventoryError(err)
		}
	}
	return id, nil
}

func (s *Service) GetTransfer(ctx context.Context, companyID, id string, userIDs ...string) (Transfer, error) {
	if _, err := requireUUID("company_id", companyID); err != nil {
		return Transfer{}, err
	}
	if _, err := requireUUID("transfer_id", id); err != nil {
		return Transfer{}, err
	}
	if err := ensureTransferReadAccess(ctx, s.pool, companyID, id, optionalActor(userIDs)); err != nil {
		return Transfer{}, err
	}
	var item Transfer
	err := s.pool.QueryRow(ctx, `SELECT t.id,t.company_id,t.transfer_no,t.transfer_type,t.source_warehouse_id,sw.name,t.destination_warehouse_id,dw.name,t.transit_warehouse_id,t.state,t.version,t.requested_at,t.approved_at,t.shipped_at,t.received_at,t.created_at,t.updated_at FROM warehouse_transfers t JOIN warehouses sw ON sw.company_id=t.company_id AND sw.id=t.source_warehouse_id JOIN warehouses dw ON dw.company_id=t.company_id AND dw.id=t.destination_warehouse_id WHERE t.company_id=$1 AND t.id=$2`, companyID, id).Scan(&item.ID, &item.CompanyID, &item.TransferNo, &item.TransferType, &item.SourceWarehouseID, &item.SourceWarehouseName, &item.DestinationWarehouseID, &item.DestinationWarehouseName, &item.TransitWarehouseID, &item.State, &item.Version, &item.RequestedAt, &item.ApprovedAt, &item.ShippedAt, &item.ReceivedAt, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Transfer{}, ErrNotFound
	}
	if err != nil {
		return Transfer{}, err
	}
	if item.TransferType == TransferTypeQuick {
		item.ArrivalAt = &item.CreatedAt
	} else {
		item.ArrivalAt = item.ShippedAt
	}
	rows, err := s.pool.Query(ctx, `SELECT l.id,l.line_no,l.product_id,l.product_code_snapshot,l.product_name_snapshot,l.variant_id,l.variant_code_snapshot,l.variant_description_snapshot,l.lot_id,l.serial_id,l.source_location_id,l.destination_location_id,l.quantity::text,l.shipped_quantity::text,l.received_quantity::text,l.damaged_quantity::text,COALESCE((SELECT SUM(r.quantity) FROM warehouse_transfer_discrepancy_resolutions r WHERE r.company_id=l.company_id AND r.transfer_line_id=l.id),0)::text,l.discrepancy_reason FROM warehouse_transfer_lines l WHERE l.company_id=$1 AND l.transfer_id=$2 ORDER BY l.line_no`, companyID, id)
	if err != nil {
		return Transfer{}, err
	}
	defer rows.Close()
	item.Lines = []TransferLine{}
	for rows.Next() {
		var line TransferLine
		if err = rows.Scan(&line.ID, &line.LineNo, &line.ProductID, &line.ProductCode, &line.ProductName, &line.VariantID, &line.VariantCode, &line.VariantDescription, &line.LotID, &line.SerialID, &line.SourceLocationID, &line.DestinationLocationID, &line.Quantity, &line.ShippedQuantity, &line.ReceivedQuantity, &line.DamagedQuantity, &line.ResolvedQuantity, &line.DiscrepancyReason); err != nil {
			return Transfer{}, err
		}
		item.Lines = append(item.Lines, line)
	}
	if err = rows.Err(); err != nil {
		return Transfer{}, err
	}
	return item, nil
}

type TransferListFilter struct {
	CompanyID     string
	WarehouseID   string
	ProductID     string
	State         string
	States        []string
	TransferType  string
	ActiveOnly    bool
	Query         string
	CreatedAtFrom *time.Time
	CreatedAtTo   *time.Time
	Cursor        string
	Limit         int
	UserID        string
}

func escapeSearchToken(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	return strings.ReplaceAll(value, `_`, `\_`)
}

func normalizeTransferListFilter(filter TransferListFilter) (TransferListFilter, error) {
	filter.CompanyID = strings.TrimSpace(filter.CompanyID)
	if _, err := requireUUID("company_id", filter.CompanyID); err != nil {
		return TransferListFilter{}, err
	}
	filter.WarehouseID = strings.TrimSpace(filter.WarehouseID)
	if filter.WarehouseID != "" {
		if _, err := requireUUID("warehouse_id", filter.WarehouseID); err != nil {
			return TransferListFilter{}, err
		}
	}
	filter.ProductID = strings.TrimSpace(filter.ProductID)
	if filter.ProductID != "" {
		if _, err := requireUUID("product_id", filter.ProductID); err != nil {
			return TransferListFilter{}, err
		}
	}
	filter.State = strings.ToUpper(strings.TrimSpace(filter.State))
	filter.States = filter.States[:0]
	if filter.State != "" {
		for _, value := range strings.Split(filter.State, ",") {
			value = strings.ToUpper(strings.TrimSpace(value))
			if value == "" {
				continue
			}
			if !validTransferState(value) {
				return TransferListFilter{}, fmt.Errorf("%w: transfer durum filtresi geçersiz", identity.ErrValidation)
			}
			filter.States = append(filter.States, value)
		}
		if len(filter.States) == 0 {
			return TransferListFilter{}, fmt.Errorf("%w: transfer durum filtresi geçersiz", identity.ErrValidation)
		}
	}
	if filter.ActiveOnly {
		filter.States = []string{TransferRequested, TransferApproved, TransferInTransit, TransferPartiallyReceived}
		filter.State = strings.Join(filter.States, ",")
	}
	if len(filter.States) > 0 && !filter.ActiveOnly {
		filter.State = strings.Join(filter.States, ",")
	}
	filter.TransferType = strings.ToUpper(strings.TrimSpace(filter.TransferType))
	if filter.TransferType != "" && !validTransferType(filter.TransferType) {
		return TransferListFilter{}, fmt.Errorf("%w: transfer tipi filtresi geçersiz", identity.ErrValidation)
	}
	filter.Query = strings.TrimSpace(filter.Query)
	if len(filter.Query) > 128 {
		return TransferListFilter{}, fmt.Errorf("%w: arama metni çok uzun", identity.ErrValidation)
	}
	if filter.CreatedAtFrom != nil {
		value := filter.CreatedAtFrom.UTC()
		filter.CreatedAtFrom = &value
	}
	if filter.CreatedAtTo != nil {
		value := filter.CreatedAtTo.UTC()
		filter.CreatedAtTo = &value
	}
	if filter.CreatedAtFrom != nil && filter.CreatedAtTo != nil && filter.CreatedAtTo.Before(*filter.CreatedAtFrom) {
		return TransferListFilter{}, fmt.Errorf("%w: transfer tarih aralığı geçersiz", identity.ErrValidation)
	}
	if filter.Limit < 1 || filter.Limit > 100 {
		filter.Limit = 50
	}
	filter.UserID = strings.TrimSpace(filter.UserID)
	return filter, nil
}

// ListTransfers keeps the original service signature for existing callers.
func (s *Service) ListTransfers(ctx context.Context, companyID, state string, limit int, userIDs ...string) ([]Transfer, error) {
	return s.ListTransfersFiltered(ctx, TransferListFilter{
		CompanyID: companyID, State: state, Limit: limit, UserID: optionalActor(userIDs),
	})
}

// ListTransfersFiltered applies state and transfer-type filters before the
// limit. Read scope intentionally does not require active warehouses, so old
// transfers remain visible after a warehouse is retired.
func (s *Service) ListTransfersFiltered(ctx context.Context, filter TransferListFilter) ([]Transfer, error) {
	result, err := s.ListTransfersPaged(ctx, filter)
	return result.Items, err
}

type TransferListResult struct {
	Items      []Transfer `json:"items"`
	NextCursor string     `json:"next_cursor,omitempty"`
}

func (s *Service) ListTransfersPaged(ctx context.Context, filter TransferListFilter) (TransferListResult, error) {
	filter, err := normalizeTransferListFilter(filter)
	if err != nil {
		return TransferListResult{}, err
	}
	args := []any{filter.CompanyID}
	query := `SELECT t.id,t.created_at FROM warehouse_transfers t WHERE t.company_id=$1`
	if filter.WarehouseID != "" {
		args = append(args, filter.WarehouseID)
		query += fmt.Sprintf(` AND (t.source_warehouse_id=$%d OR t.destination_warehouse_id=$%d)`, len(args), len(args))
	}
	if filter.ProductID != "" {
		args = append(args, filter.ProductID)
		query += fmt.Sprintf(` AND EXISTS (
			SELECT 1 FROM warehouse_transfer_lines pl
			WHERE pl.company_id=t.company_id AND pl.transfer_id=t.id AND pl.product_id=$%d
		)`, len(args))
	}
	if filter.UserID != "" {
		args = append(args, filter.UserID)
		userParam := len(args)
		warehouseScope := func(alias string) string {
			return fmt.Sprintf(`%s.company_id=$1 AND (%s.is_system OR ((%s.branch_id IS NULL OR NOT EXISTS(SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=$1 AND bs.user_id=$%d) OR EXISTS(SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=$1 AND bs.user_id=$%d AND bs.branch_id=%s.branch_id)) AND (NOT EXISTS(SELECT 1 FROM membership_warehouse_scopes ws WHERE ws.company_id=$1 AND ws.user_id=$%d) OR EXISTS(SELECT 1 FROM membership_warehouse_scopes ws WHERE ws.company_id=$1 AND ws.user_id=$%d AND ws.warehouse_id=%s.id))))`, alias, alias, alias, userParam, userParam, alias, userParam, userParam, alias)
		}
		query += fmt.Sprintf(` AND t.id IN (
			SELECT t.id FROM warehouse_transfers t
			WHERE t.company_id=$1
			  AND EXISTS (SELECT 1 FROM warehouses sw WHERE sw.id=t.source_warehouse_id AND %s)
			  AND EXISTS (SELECT 1 FROM warehouses dw WHERE dw.id=t.destination_warehouse_id AND %s)
			  AND EXISTS (SELECT 1 FROM warehouses tw WHERE tw.id=t.transit_warehouse_id AND %s)
		)`, warehouseScope("sw"), warehouseScope("dw"), warehouseScope("tw"))
	}
	for _, token := range strings.Fields(filter.Query) {
		args = append(args, "%"+escapeSearchToken(token)+"%")
		param := len(args)
		query += fmt.Sprintf(` AND (
			 t.transfer_no ILIKE $%d ESCAPE '\'
			 OR t.transfer_type ILIKE $%d ESCAPE '\'
			 OR t.state ILIKE $%d ESCAPE '\'
			 OR CASE t.state
				WHEN 'REQUESTED' THEN 'talep bekliyor'
				WHEN 'APPROVED' THEN 'onaylandı'
				WHEN 'IN_TRANSIT' THEN 'sevk yolda'
				WHEN 'PARTIALLY_RECEIVED' THEN 'kısmi teslim'
				WHEN 'RECEIVED' THEN 'teslim edildi'
				WHEN 'CANCELLED' THEN 'iptal'
				ELSE 'taslak' END ILIKE $%d ESCAPE '\'
			 OR EXISTS (SELECT 1 FROM warehouses swq WHERE swq.company_id=t.company_id AND swq.id=t.source_warehouse_id AND (swq.code ILIKE $%d ESCAPE '\' OR swq.name ILIKE $%d ESCAPE '\'))
			 OR EXISTS (SELECT 1 FROM warehouses dwq WHERE dwq.company_id=t.company_id AND dwq.id=t.destination_warehouse_id AND (dwq.code ILIKE $%d ESCAPE '\' OR dwq.name ILIKE $%d ESCAPE '\'))
		)`, param, param, param, param, param, param, param, param)
	}
	if len(filter.States) > 0 {
		args = append(args, filter.States)
		query += fmt.Sprintf(` AND t.state=ANY($%d::text[])`, len(args))
	}
	if filter.TransferType != "" {
		args = append(args, filter.TransferType)
		query += fmt.Sprintf(` AND t.transfer_type=$%d`, len(args))
	}
	if filter.CreatedAtFrom != nil {
		args = append(args, *filter.CreatedAtFrom)
		query += fmt.Sprintf(` AND t.created_at >= $%d`, len(args))
	}
	if filter.CreatedAtTo != nil {
		args = append(args, *filter.CreatedAtTo)
		query += fmt.Sprintf(` AND t.created_at <= $%d`, len(args))
	}
	if filter.Cursor != "" {
		lastCreatedAt, lastID, decodeErr := decodeTransferCursor(filter.Cursor)
		if decodeErr != nil {
			return TransferListResult{}, fmt.Errorf("%w: transfer listesi cursor bilgisi geçersiz", identity.ErrValidation)
		}
		args = append(args, lastCreatedAt, lastID)
		query += fmt.Sprintf(` AND (t.created_at,t.id) < ($%d,$%d::uuid)`, len(args)-1, len(args))
	}
	args = append(args, filter.Limit+1)
	query += fmt.Sprintf(` ORDER BY t.created_at DESC,t.id DESC LIMIT $%d`, len(args))
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return TransferListResult{}, err
	}
	type transferRef struct {
		id        string
		createdAt time.Time
	}
	refs := make([]transferRef, 0)
	for rows.Next() {
		var ref transferRef
		if err = rows.Scan(&ref.id, &ref.createdAt); err != nil {
			rows.Close()
			return TransferListResult{}, err
		}
		refs = append(refs, ref)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return TransferListResult{}, err
	}
	rows.Close()
	// GetTransfer issues its own queries; run it only after the id rows are
	// drained, otherwise the request-pinned connection fails with "conn busy".
	items := make([]Transfer, 0, len(refs))
	createdAtByID := make(map[string]time.Time)
	for _, ref := range refs {
		item, getErr := s.GetTransfer(ctx, filter.CompanyID, ref.id, filter.UserID)
		if getErr != nil {
			return TransferListResult{}, getErr
		}
		items = append(items, item)
		createdAtByID[ref.id] = ref.createdAt
	}
	result := TransferListResult{Items: items}
	if len(items) > filter.Limit {
		last := items[filter.Limit-1]
		result.Items = items[:filter.Limit]
		result.NextCursor = encodeTransferCursor(createdAtByID[last.ID], last.ID)
	}
	return result, nil
}

func encodeTransferCursor(createdAt time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(createdAt.UTC().Format(time.RFC3339Nano) + "|" + id))
}

func decodeTransferCursor(value string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return time.Time{}, "", err
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 2 || strings.TrimSpace(parts[1]) == "" {
		return time.Time{}, "", fmt.Errorf("invalid cursor")
	}
	createdAt, err := time.Parse(time.RFC3339Nano, parts[0])
	return createdAt, parts[1], err
}

func transferTimestampColumn(to string) string {
	switch to {
	case TransferRequested:
		return "requested_at"
	case TransferApproved:
		return "approved_at"
	case TransferInTransit:
		return "shipped_at"
	case TransferReceived:
		return "received_at"
	default:
		return ""
	}
}

type transferReservationLine struct {
	ID, ProductID, WarehouseID, Quantity   string
	VariantID, LocationID, LotID, SerialID *string
}

func reservationPositionKey(line transferReservationLine) string {
	return strings.Join([]string{
		line.WarehouseID,
		line.ProductID,
		valueOrEmpty(line.VariantID),
		valueOrEmpty(line.LocationID),
		valueOrEmpty(line.LotID),
		valueOrEmpty(line.SerialID),
	}, ":")
}

func nullableString(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

// reserveTransferLinesTx claims source stock at REQUESTED/APPROVED time. The
// advisory key is the same key used by the stock projection and movement
// guards, and lines are acquired in a deterministic position order so two
// reciprocal transfers cannot deadlock while reserving shared stock.
func reserveTransferLinesTx(ctx context.Context, tx txDB, companyID, transferID string) error {
	if _, err := tx.Exec(ctx, `SELECT set_config('varyaone.allow_transfer_reservation_change','on',true)`); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT id,product_id,source_location_id,variant_id,lot_id,serial_id,quantity::text
		FROM warehouse_transfer_lines
		WHERE company_id=$1 AND transfer_id=$2
		ORDER BY line_no
		FOR UPDATE`, companyID, transferID)
	if err != nil {
		return err
	}
	lines := make([]transferReservationLine, 0)
	for rows.Next() {
		var line transferReservationLine
		if err = rows.Scan(&line.ID, &line.ProductID, &line.LocationID, &line.VariantID, &line.LotID, &line.SerialID, &line.Quantity); err != nil {
			rows.Close()
			return err
		}
		line.WarehouseID = ""
		lines = append(lines, line)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	// Materialize and close the line reader before validating variants. A pgx
	// transaction uses one connection, so issuing a validation query while
	// rows is still open returns conn busy and masks the domain error as 500.
	for _, line := range lines {
		if _, err = validateInventoryVariantTx(ctx, tx, companyID, line.ProductID, valueOrEmpty(line.VariantID)); err != nil {
			return err
		}
	}
	var sourceWarehouseID string
	if err = tx.QueryRow(ctx, `SELECT source_warehouse_id FROM warehouse_transfers WHERE company_id=$1 AND id=$2`, companyID, transferID).Scan(&sourceWarehouseID); err != nil {
		return err
	}
	for index := range lines {
		lines[index].WarehouseID = sourceWarehouseID
	}
	sort.Slice(lines, func(i, j int) bool {
		left, right := reservationPositionKey(lines[i]), reservationPositionKey(lines[j])
		if left == right {
			return lines[i].ID < lines[j].ID
		}
		return left < right
	})

	for _, line := range lines {
		var existingState string
		reservationErr := tx.QueryRow(ctx, `SELECT state FROM warehouse_transfer_reservations WHERE company_id=$1 AND transfer_line_id=$2 FOR UPDATE`, companyID, line.ID).Scan(&existingState)
		if reservationErr == nil {
			if existingState == "ACTIVE" {
				continue
			}
			return codeError(ErrConflict.Error(), ErrConflict, "transfer satırı için stok rezervasyonu artık kullanılamaz")
		}
		if !errors.Is(reservationErr, pgx.ErrNoRows) {
			return reservationErr
		}

		positionKey := reservationPositionKey(line)
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,731))`, positionKey); err != nil {
			return err
		}
		var positionID, physical, reserved string
		positionErr := tx.QueryRow(ctx, `SELECT id,physical_quantity::text,reserved_quantity::text
			FROM stock_positions
			WHERE company_id=$1 AND warehouse_id=$2 AND product_id=$3
			  AND variant_id IS NOT DISTINCT FROM $4::uuid
			  AND location_id IS NOT DISTINCT FROM $5::uuid
			  AND lot_id IS NOT DISTINCT FROM $6::uuid
			  AND serial_id IS NOT DISTINCT FROM $7::uuid
			FOR UPDATE`, companyID, sourceWarehouseID, line.ProductID,
			nullableString(line.VariantID), nullableString(line.LocationID), nullableString(line.LotID), nullableString(line.SerialID)).Scan(&positionID, &physical, &reserved)
		if errors.Is(positionErr, pgx.ErrNoRows) {
			return ErrInsufficientStock
		}
		if positionErr != nil {
			return positionErr
		}
		if decimalCompare(decimalSub(physical, reserved), line.Quantity) < 0 {
			return ErrInsufficientStock
		}
		if _, err = tx.Exec(ctx, `UPDATE stock_positions SET reserved_quantity=reserved_quantity+$1,updated_at=now() WHERE company_id=$2 AND id=$3`, line.Quantity, companyID, positionID); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO warehouse_transfer_reservations(
			id,company_id,transfer_id,transfer_line_id,warehouse_id,product_id,
			variant_id,location_id,lot_id,serial_id,quantity)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, uuid.NewString(), companyID, transferID,
			line.ID, sourceWarehouseID, line.ProductID, nullableString(line.VariantID), nullableString(line.LocationID),
			nullableString(line.LotID), nullableString(line.SerialID), line.Quantity); err != nil {
			return mapInventoryError(err)
		}
	}
	return nil
}

func consumeTransferReservationsTx(ctx context.Context, tx txDB, companyID, transferID string) error {
	if _, err := tx.Exec(ctx, `SELECT set_config('varyaone.allow_transfer_reservation_change','on',true)`); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT transfer_line_id,warehouse_id,product_id,variant_id,location_id,lot_id,serial_id,quantity::text
		FROM warehouse_transfer_reservations
		WHERE company_id=$1 AND transfer_id=$2 AND state='ACTIVE'
		ORDER BY warehouse_id,product_id,variant_id NULLS FIRST,location_id NULLS FIRST,lot_id NULLS FIRST,serial_id NULLS FIRST,transfer_line_id
		FOR UPDATE`, companyID, transferID)
	if err != nil {
		return err
	}
	type reservation struct {
		line     transferReservationLine
		quantity string
	}
	reservations := make([]reservation, 0)
	for rows.Next() {
		var item reservation
		if err = rows.Scan(&item.line.ID, &item.line.WarehouseID, &item.line.ProductID, &item.line.VariantID, &item.line.LocationID, &item.line.LotID, &item.line.SerialID, &item.quantity); err != nil {
			rows.Close()
			return err
		}
		reservations = append(reservations, item)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, item := range reservations {
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,731))`, reservationPositionKey(item.line)); err != nil {
			return err
		}
		result, updateErr := tx.Exec(ctx, `UPDATE stock_positions
			SET reserved_quantity=reserved_quantity-$1,updated_at=now()
			WHERE company_id=$2 AND warehouse_id=$3 AND product_id=$4
			  AND variant_id IS NOT DISTINCT FROM $5::uuid
			  AND location_id IS NOT DISTINCT FROM $6::uuid
			  AND lot_id IS NOT DISTINCT FROM $7::uuid
			  AND serial_id IS NOT DISTINCT FROM $8::uuid
			  AND reserved_quantity >= $1`, item.quantity, companyID, item.line.WarehouseID, item.line.ProductID,
			nullableString(item.line.VariantID), nullableString(item.line.LocationID), nullableString(item.line.LotID), nullableString(item.line.SerialID))
		if updateErr != nil {
			return updateErr
		}
		if result.RowsAffected() != 1 {
			return codeError(ErrConflict.Error(), ErrConflict, "stok rezervasyonu tüketilemedi")
		}
		if _, err = tx.Exec(ctx, `UPDATE warehouse_transfer_reservations SET state='CONSUMED',consumed_at=now() WHERE company_id=$1 AND transfer_line_id=$2 AND state='ACTIVE'`, companyID, item.line.ID); err != nil {
			return err
		}
	}
	return nil
}

func releaseTransferReservationsTx(ctx context.Context, tx txDB, companyID, transferID string) error {
	if _, err := tx.Exec(ctx, `SELECT set_config('varyaone.allow_transfer_reservation_change','on',true)`); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT transfer_line_id,warehouse_id,product_id,variant_id,location_id,lot_id,serial_id,quantity::text
		FROM warehouse_transfer_reservations
		WHERE company_id=$1 AND transfer_id=$2 AND state='ACTIVE'
		ORDER BY warehouse_id,product_id,variant_id NULLS FIRST,location_id NULLS FIRST,lot_id NULLS FIRST,serial_id NULLS FIRST,transfer_line_id
		FOR UPDATE`, companyID, transferID)
	if err != nil {
		return err
	}
	type reservation struct {
		line     transferReservationLine
		quantity string
	}
	reservations := make([]reservation, 0)
	for rows.Next() {
		var item reservation
		if err = rows.Scan(&item.line.ID, &item.line.WarehouseID, &item.line.ProductID, &item.line.VariantID, &item.line.LocationID, &item.line.LotID, &item.line.SerialID, &item.quantity); err != nil {
			rows.Close()
			return err
		}
		reservations = append(reservations, item)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, item := range reservations {
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,731))`, reservationPositionKey(item.line)); err != nil {
			return err
		}
		result, updateErr := tx.Exec(ctx, `UPDATE stock_positions
			SET reserved_quantity=reserved_quantity-$1,updated_at=now()
			WHERE company_id=$2 AND warehouse_id=$3 AND product_id=$4
			  AND variant_id IS NOT DISTINCT FROM $5::uuid
			  AND location_id IS NOT DISTINCT FROM $6::uuid
			  AND lot_id IS NOT DISTINCT FROM $7::uuid
			  AND serial_id IS NOT DISTINCT FROM $8::uuid
			  AND reserved_quantity >= $1`, item.quantity, companyID, item.line.WarehouseID, item.line.ProductID,
			nullableString(item.line.VariantID), nullableString(item.line.LocationID), nullableString(item.line.LotID), nullableString(item.line.SerialID))
		if updateErr != nil {
			return updateErr
		}
		if result.RowsAffected() != 1 {
			return codeError(ErrConflict.Error(), ErrConflict, "stok rezervasyonu bırakılamadı")
		}
		if _, err = tx.Exec(ctx, `UPDATE warehouse_transfer_reservations SET state='RELEASED',released_at=now() WHERE company_id=$1 AND transfer_line_id=$2 AND state='ACTIVE'`, companyID, item.line.ID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) transitionTransfer(ctx context.Context, companyID, id, to, actor string, expectedVersion int64) (Transfer, error) {
	companyID, err := requireUUID("company_id", companyID)
	if err != nil {
		return Transfer{}, err
	}
	id, err = requireUUID("transfer_id", id)
	if err != nil {
		return Transfer{}, err
	}
	actorID, err := optionalUUID(actor)
	if err != nil {
		return Transfer{}, err
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return Transfer{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var from, transferType string
	var currentVersion int64
	if err = tx.QueryRow(ctx, `SELECT state,transfer_type,version FROM warehouse_transfers WHERE company_id=$1 AND id=$2 FOR UPDATE`, companyID, id).Scan(&from, &transferType, &currentVersion); errors.Is(err, pgx.ErrNoRows) {
		return Transfer{}, ErrNotFound
	} else if err != nil {
		return Transfer{}, err
	}
	if err = ensureTransferAccess(ctx, tx, companyID, id, actor); err != nil {
		return Transfer{}, err
	}
	if err = ValidateTransferTransition(from, to); err != nil {
		return Transfer{}, err
	}
	if expectedVersion < 1 {
		// The row is already locked above. When the caller omits If-Match,
		// use that locked version while still retaining optimistic locking for
		// clients that do send the header.
		expectedVersion = currentVersion
	}
	if transferType == TransferTypeWorkflow && (to == TransferRequested || to == TransferApproved) {
		if err = reserveTransferLinesTx(ctx, tx, companyID, id); err != nil {
			return Transfer{}, mapInventoryError(err)
		}
	}
	column := transferTimestampColumn(to)
	args := []any{to, actorID, companyID, id, expectedVersion}
	query := `UPDATE warehouse_transfers SET state=$1,requested_by=COALESCE(requested_by,$2),version=version+1,updated_at=now() WHERE company_id=$3 AND id=$4 AND version=$5`
	if to == TransferApproved {
		query = `UPDATE warehouse_transfers SET state=$1,approved_by=COALESCE(approved_by,$2),approved_at=now(),version=version+1,updated_at=now() WHERE company_id=$3 AND id=$4 AND version=$5`
	} else if column != "" {
		query = fmt.Sprintf(`UPDATE warehouse_transfers SET state=$1,%s=now(),version=version+1,updated_at=now() WHERE company_id=$3 AND id=$4 AND version=$5`, column)
	}
	result, err := tx.Exec(ctx, query, args...)
	if err != nil {
		return Transfer{}, mapInventoryError(err)
	}
	if result.RowsAffected() == 0 {
		return Transfer{}, ErrConflict
	}
	if err = writeInventoryAuditTx(ctx, tx, companyID, actor, "INVENTORY_TRANSFER_"+to, id, map[string]any{"from": from, "to": to}); err != nil {
		return Transfer{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Transfer{}, err
	}
	return s.GetTransfer(ctx, companyID, id, actor)
}

func (s *Service) RequestTransfer(ctx context.Context, companyID, id, actor string, expectedVersion int64) (Transfer, error) {
	return s.transitionTransfer(ctx, companyID, id, TransferRequested, actor, expectedVersion)
}
func (s *Service) ApproveTransfer(ctx context.Context, companyID, id, actor string, expectedVersion int64) (Transfer, error) {
	return s.transitionTransfer(ctx, companyID, id, TransferApproved, actor, expectedVersion)
}

// postWorkflowShipmentTx consumes the source reservation and posts the
// source->transit legs without committing the transaction. It is used both by
// the new create-and-ship workflow and by the compatibility ship endpoint for
// older APPROVED transfers.
func (s *Service) postWorkflowShipmentTx(ctx context.Context, tx txDB, companyID, id, actor string) error {
	var transferState string
	var transferVersion int64
	var sourceWarehouseID, transitWarehouseID string
	if err := tx.QueryRow(ctx, `SELECT state,version,source_warehouse_id,transit_warehouse_id
		FROM warehouse_transfers WHERE company_id=$1 AND id=$2 FOR UPDATE`, companyID, id).Scan(
		&transferState, &transferVersion, &sourceWarehouseID, &transitWarehouseID); err != nil {
		return err
	}
	if transferState != TransferApproved {
		return ValidateTransferTransition(transferState, TransferInTransit)
	}
	if err := reserveTransferLinesTx(ctx, tx, companyID, id); err != nil {
		return mapInventoryError(err)
	}
	if err := consumeTransferReservationsTx(ctx, tx, companyID, id); err != nil {
		return mapInventoryError(err)
	}
	rows, err := tx.Query(ctx, `SELECT id,product_id,variant_id,lot_id,serial_id,source_location_id,quantity::text
		FROM warehouse_transfer_lines WHERE company_id=$1 AND transfer_id=$2 ORDER BY line_no FOR UPDATE`, companyID, id)
	if err != nil {
		return err
	}
	lines := []TransferLine{}
	for rows.Next() {
		var line TransferLine
		if err = rows.Scan(&line.ID, &line.ProductID, &line.VariantID, &line.LotID, &line.SerialID, &line.SourceLocationID, &line.Quantity); err != nil {
			rows.Close()
			return err
		}
		lines = append(lines, line)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, line := range lines {
		out := movementForTransfer(companyID, sourceWarehouseID, valueOrEmpty(line.SourceLocationID), line.ProductID, valueOrEmpty(line.VariantID), valueOrEmpty(line.LotID), valueOrEmpty(line.SerialID), MovementTransferOut, DirectionOut, line.Quantity, id, line.ID, "transfer:"+id+":"+line.ID+":out")
		out.ActorUserID = actor
		outMovement, postErr := postMovementTx(ctx, tx, out, movementHash(out, false))
		if postErr != nil {
			return mapInventoryError(postErr)
		}
		in := movementForTransfer(companyID, transitWarehouseID, "", line.ProductID, valueOrEmpty(line.VariantID), valueOrEmpty(line.LotID), valueOrEmpty(line.SerialID), MovementTransferIn, DirectionIn, line.Quantity, id, line.ID, "transfer:"+id+":"+line.ID+":transit-in")
		in.ActorUserID = actor
		in.UnitCost, in.Currency, err = transferCostBasis(ctx, tx, companyID, outMovement.ID)
		if err != nil {
			return err
		}
		if _, err = postMovementTx(ctx, tx, in, movementHash(in, false)); err != nil {
			return mapInventoryError(err)
		}
		if _, err = tx.Exec(ctx, `UPDATE warehouse_transfer_lines SET shipped_quantity=quantity WHERE company_id=$1 AND id=$2`, companyID, line.ID); err != nil {
			return err
		}
	}
	result, err := tx.Exec(ctx, `UPDATE warehouse_transfers
		SET state='IN_TRANSIT',shipped_at=now(),version=version+1,updated_at=now()
		WHERE company_id=$1 AND id=$2 AND version=$3`, companyID, id, transferVersion)
	if err != nil {
		return mapInventoryError(err)
	}
	if result.RowsAffected() == 0 {
		return ErrConflict
	}
	if err = writeInventoryAuditTx(ctx, tx, companyID, actor, "INVENTORY_TRANSFER_IN_TRANSIT", id, nil); err != nil {
		return err
	}
	return nil
}

func (s *Service) ShipTransfer(ctx context.Context, companyID, id, actor string, expectedVersion int64) (Transfer, error) {
	companyID, err := requireUUID("company_id", companyID)
	if err != nil {
		return Transfer{}, err
	}
	id, err = requireUUID("transfer_id", id)
	if err != nil {
		return Transfer{}, err
	}
	if _, err := optionalUUID(actor); err != nil {
		return Transfer{}, err
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return Transfer{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var transfer Transfer
	err = tx.QueryRow(ctx, `SELECT id,company_id,transfer_no,source_warehouse_id,destination_warehouse_id,transit_warehouse_id,state,version FROM warehouse_transfers WHERE company_id=$1 AND id=$2 FOR UPDATE`, companyID, id).Scan(&transfer.ID, &transfer.CompanyID, &transfer.TransferNo, &transfer.SourceWarehouseID, &transfer.DestinationWarehouseID, &transfer.TransitWarehouseID, &transfer.State, &transfer.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return Transfer{}, ErrNotFound
	}
	if err != nil {
		return Transfer{}, err
	}
	if err = ensureStandardWarehouse(ctx, tx, companyID, actor, transfer.SourceWarehouseID); err != nil {
		return Transfer{}, err
	}
	if err = ensureWarehouseAccess(ctx, tx, companyID, actor, transfer.DestinationWarehouseID); err != nil {
		return Transfer{}, err
	}
	if err = ensureWarehouseAccess(ctx, tx, companyID, actor, transfer.TransitWarehouseID); err != nil {
		return Transfer{}, err
	}
	if err = ValidateTransferTransition(transfer.State, TransferInTransit); err != nil {
		return Transfer{}, err
	}
	if expectedVersion > 0 && expectedVersion != transfer.Version {
		return Transfer{}, ErrConflict
	}
	if err = s.postWorkflowShipmentTx(ctx, tx, companyID, id, actor); err != nil {
		return Transfer{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Transfer{}, err
	}
	return s.GetTransfer(ctx, companyID, id, actor)
}

func (s *Service) ReceiveTransfer(ctx context.Context, companyID, id, actor string, expectedVersion int64, receives []ReceiveLineInput) (Transfer, error) {
	return s.receiveTransferWithKey(ctx, companyID, id, actor, expectedVersion, receives, "")
}

// receiveTransfer contains the command implementation. The public method is
// kept source-compatible with existing callers; HTTP callers may opt into a
// command idempotency key through ReceiveTransferWithKey.
func (s *Service) receiveTransferWithKey(ctx context.Context, companyID, id, actor string, expectedVersion int64, receives []ReceiveLineInput, idempotencyKey string) (Transfer, error) {
	companyID, err := requireUUID("company_id", companyID)
	if err != nil {
		return Transfer{}, err
	}
	id, err = requireUUID("transfer_id", id)
	if err != nil {
		return Transfer{}, err
	}
	actorID, err := optionalUUID(actor)
	if err != nil {
		return Transfer{}, err
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return Transfer{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var transfer Transfer
	err = tx.QueryRow(ctx, `SELECT id,company_id,source_warehouse_id,destination_warehouse_id,transit_warehouse_id,state,version FROM warehouse_transfers WHERE company_id=$1 AND id=$2 FOR UPDATE`, companyID, id).Scan(&transfer.ID, &transfer.CompanyID, &transfer.SourceWarehouseID, &transfer.DestinationWarehouseID, &transfer.TransitWarehouseID, &transfer.State, &transfer.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return Transfer{}, ErrNotFound
	}
	if err != nil {
		return Transfer{}, err
	}
	if err = ensureStandardWarehouse(ctx, tx, companyID, actor, transfer.SourceWarehouseID); err != nil {
		return Transfer{}, err
	}
	if err = ensureWarehouseAccess(ctx, tx, companyID, actor, transfer.DestinationWarehouseID); err != nil {
		return Transfer{}, err
	}
	if err = ensureWarehouseAccess(ctx, tx, companyID, actor, transfer.TransitWarehouseID); err != nil {
		return Transfer{}, err
	}
	rows, err := tx.Query(ctx, `SELECT id,product_id,variant_id,lot_id,serial_id,destination_location_id,quantity::text,shipped_quantity::text,received_quantity::text,damaged_quantity::text,COALESCE((SELECT SUM(r.quantity) FROM warehouse_transfer_discrepancy_resolutions r WHERE r.company_id=warehouse_transfer_lines.company_id AND r.transfer_line_id=warehouse_transfer_lines.id),0)::text FROM warehouse_transfer_lines WHERE company_id=$1 AND transfer_id=$2 ORDER BY line_no FOR UPDATE`, companyID, id)
	if err != nil {
		return Transfer{}, err
	}
	lineByID := map[string]TransferLine{}
	for rows.Next() {
		var line TransferLine
		if err = rows.Scan(&line.ID, &line.ProductID, &line.VariantID, &line.LotID, &line.SerialID, &line.DestinationLocationID, &line.Quantity, &line.ShippedQuantity, &line.ReceivedQuantity, &line.DamagedQuantity, &line.ResolvedQuantity); err != nil {
			rows.Close()
			return Transfer{}, err
		}
		lineByID[line.ID] = line
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return Transfer{}, err
	}
	requestedReceives := append([]ReceiveLineInput(nil), receives...)
	if len(receives) == 0 {
		for lineID, line := range lineByID {
			receives = append(receives, ReceiveLineInput{LineID: lineID, ReceivedQuantity: formatRatMust(new(big.Rat).SetInt64(0))})
			receives[len(receives)-1].ReceivedQuantity = decimalSub(decimalSub(line.ShippedQuantity, line.ReceivedQuantity), decimalMax(line.DamagedQuantity, line.ResolvedQuantity))
		}
	}
	replay, err := reserveTransferReceiveCommand(ctx, tx, companyID, id, expectedVersion, requestedReceives, idempotencyKey, actor)
	if err != nil {
		return Transfer{}, err
	}
	if replay {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return s.GetTransfer(ctx, companyID, id, actor)
	}
	if transfer.State != TransferInTransit && transfer.State != TransferPartiallyReceived {
		return Transfer{}, ValidateTransferTransition(transfer.State, TransferPartiallyReceived)
	}
	if expectedVersion > 0 && expectedVersion != transfer.Version {
		return Transfer{}, ErrConflict
	}
	for _, receive := range receives {
		lineID, parseErr := requireUUID("line_id", receive.LineID)
		if parseErr != nil {
			return Transfer{}, parseErr
		}
		line, ok := lineByID[lineID]
		if !ok {
			return Transfer{}, ErrNotFound
		}
		// A resolution consumes an outstanding transit discrepancy. Damaged
		// quantity is already counted on the line, so use the larger of damaged
		// and resolved rather than subtracting both and double-counting it.
		remaining := decimalSub(decimalSub(line.ShippedQuantity, line.ReceivedQuantity), decimalMax(line.DamagedQuantity, line.ResolvedQuantity))
		received, parseErr := parseNonNegative("received_quantity", receive.ReceivedQuantity)
		if parseErr != nil {
			return Transfer{}, parseErr
		}
		damaged := "0"
		if strings.TrimSpace(receive.DamagedQuantity) != "" {
			damaged, parseErr = parseNonNegative("damaged_quantity", receive.DamagedQuantity)
			if parseErr != nil {
				return Transfer{}, parseErr
			}
		}
		if decimalCompare(decimalAdd(received, damaged), remaining) > 0 {
			return Transfer{}, validationError("teslim miktarı yoldaki miktarı aşamaz")
		}
		if decimalCompare(decimalAdd(received, damaged), remaining) < 0 && strings.TrimSpace(receive.Reason) == "" {
			return Transfer{}, validationError("eksik teslim için açık gerekçe gereklidir")
		}
		operationKey := fmt.Sprintf("transfer:%s:%s:receive:%s:%s:%s", id, line.ID, line.ReceivedQuantity, received, damaged)
		if decimalCompare(received, "0") > 0 {
			out := movementForTransfer(companyID, transfer.TransitWarehouseID, "", line.ProductID, valueOrEmpty(line.VariantID), valueOrEmpty(line.LotID), valueOrEmpty(line.SerialID), MovementTransferOut, DirectionOut, received, id, line.ID, operationKey+":out")
			out.ActorUserID = valueOrEmptyAny(actorID)
			outMovement, postErr := postMovementTx(ctx, tx, out, movementHash(out, false))
			if postErr != nil {
				err = postErr
				return Transfer{}, mapInventoryError(err)
			}
			in := movementForTransfer(companyID, transfer.DestinationWarehouseID, valueOrEmpty(line.DestinationLocationID), line.ProductID, valueOrEmpty(line.VariantID), valueOrEmpty(line.LotID), valueOrEmpty(line.SerialID), MovementTransferIn, DirectionIn, received, id, line.ID, operationKey+":in")
			in.ActorUserID = valueOrEmptyAny(actorID)
			in.UnitCost, in.Currency, err = transferCostBasis(ctx, tx, companyID, outMovement.ID)
			if err != nil {
				return Transfer{}, err
			}
			if _, err = postMovementTx(ctx, tx, in, movementHash(in, false)); err != nil {
				return Transfer{}, mapInventoryError(err)
			}
		}
		if decimalCompare(damaged, "0") > 0 {
			if strings.TrimSpace(receive.Reason) == "" {
				return Transfer{}, codeError(ErrInvalidReason.Error(), ErrInvalidReason, "hasarlı miktar için gerekçe gereklidir")
			}
			// Damaged stock is deliberately not removed here. It remains a real
			// transit position until ResolveTransferDiscrepancy records an
			// explicit delivery, return or waste decision.
		}
		if _, err = tx.Exec(ctx, `UPDATE warehouse_transfer_lines SET received_quantity=received_quantity+$1,damaged_quantity=damaged_quantity+$2,discrepancy_reason=CASE WHEN $3<>'' THEN $3 ELSE discrepancy_reason END WHERE company_id=$4 AND id=$5`, received, damaged, receive.Reason, companyID, line.ID); err != nil {
			return Transfer{}, err
		}
	}
	var incomplete bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM warehouse_transfer_lines l WHERE l.company_id=$1 AND l.transfer_id=$2 AND l.received_quantity + COALESCE((SELECT SUM(r.quantity) FROM warehouse_transfer_discrepancy_resolutions r WHERE r.company_id=l.company_id AND r.transfer_line_id=l.id),0) < l.shipped_quantity)`, companyID, id).Scan(&incomplete); err != nil {
		return Transfer{}, err
	}
	state := TransferReceived
	if incomplete {
		state = TransferPartiallyReceived
	}
	result, err := tx.Exec(ctx, `UPDATE warehouse_transfers SET state=$1,received_at=CASE WHEN $1='RECEIVED' THEN now() ELSE received_at END,version=version+1,updated_at=now() WHERE company_id=$2 AND id=$3 AND version=$4`, state, companyID, id, transfer.Version)
	if err != nil {
		return Transfer{}, mapInventoryError(err)
	}
	if result.RowsAffected() == 0 {
		return Transfer{}, ErrConflict
	}
	if strings.TrimSpace(idempotencyKey) != "" {
		if _, err = tx.Exec(ctx, `UPDATE command_idempotency_records SET status='COMPLETED',response_status=200,response_body=$3,completed_at=now() WHERE company_id=$1 AND idempotency_key=$2 AND status='IN_PROGRESS'`, companyID, strings.TrimSpace(idempotencyKey), json.RawMessage(fmt.Sprintf(`{"transfer_id":%q}`, id))); err != nil {
			return Transfer{}, err
		}
	}
	if err = writeInventoryAuditTx(ctx, tx, companyID, actor, "INVENTORY_TRANSFER_"+state, id, map[string]any{"partial": incomplete}); err != nil {
		return Transfer{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Transfer{}, err
	}
	return s.GetTransfer(ctx, companyID, id, actor)
}

func reserveTransferReceiveCommand(ctx context.Context, tx pgx.Tx, companyID, transferID string, expectedVersion int64, receives []ReceiveLineInput, key, actor string) (bool, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return false, nil
	}
	if len(key) > 255 {
		return false, fmt.Errorf("%w: idempotency anahtarı çok uzun", identity.ErrValidation)
	}
	canonical := append([]ReceiveLineInput(nil), receives...)
	sort.SliceStable(canonical, func(i, j int) bool { return canonical[i].LineID < canonical[j].LineID })
	payload, err := json.Marshal(struct {
		TransferID      string             `json:"transfer_id"`
		ExpectedVersion int64              `json:"expected_version"`
		Lines           []ReceiveLineInput `json:"lines"`
	}{transferID, expectedVersion, canonical})
	if err != nil {
		return false, err
	}
	digest := sha256.Sum256(payload)
	command := "inventory.transfer.receive"
	actorValue, _ := optionalUUID(actor)
	var existingCommand, existingHash, status string
	result, err := tx.Exec(ctx, `INSERT INTO command_idempotency_records(
		company_id,idempotency_key,command_name,payload_sha256,actor_user_id)
		VALUES($1,$2,$3,$4,$5)
		ON CONFLICT (company_id,idempotency_key) DO NOTHING`,
		companyID, key, command, fmt.Sprintf("%x", digest[:]), actorValue)
	if err != nil {
		return false, err
	}
	inserted := result.RowsAffected() == 1
	// The INSERT's unique-index wait serializes concurrent users of the same
	// key. Read and lock the winning row separately: a data-modifying CTE's
	// sibling SELECT can use the statement snapshot and miss its own new row.
	if err = tx.QueryRow(ctx, `SELECT command_name,payload_sha256,status
		FROM command_idempotency_records
		WHERE company_id=$1 AND idempotency_key=$2
		FOR UPDATE`, companyID, key).Scan(&existingCommand, &existingHash, &status); err != nil {
		return false, err
	}
	if existingCommand != command || existingHash != fmt.Sprintf("%x", digest[:]) {
		return false, codeError(ErrIdempotencyConflict.Error(), ErrIdempotencyConflict, "aynı Idempotency-Key farklı içerikle kullanıldı")
	}
	if !inserted {
		if status == "COMPLETED" {
			return true, nil
		}
		return false, codeError(ErrConflict.Error(), ErrConflict, "aynı transfer teslim komutu devam ediyor")
	}
	return false, nil
}

// ReceiveTransferWithKey applies a transfer receipt with an atomic command
// idempotency record. Replaying the same key and payload returns the current
// transfer without posting the transit movements a second time.
func (s *Service) ReceiveTransferWithKey(ctx context.Context, companyID, id, actor string, expectedVersion int64, receives []ReceiveLineInput, idempotencyKey string) (Transfer, error) {
	return s.receiveTransferWithKey(ctx, companyID, id, actor, expectedVersion, receives, idempotencyKey)
}

// ResolveTransferDiscrepancy keeps a damaged or missing quantity in the
// transit warehouse until an explicit business decision is recorded. DELIVER
// moves it to the destination, RETURN sends it back to the source and WASTE
// removes it from transit. Every decision is an immutable row with a reason.
func (s *Service) ResolveTransferDiscrepancy(ctx context.Context, companyID, transferID, actor string, input TransferResolutionInput) (Transfer, error) {
	companyID, err := requireUUID("company_id", companyID)
	if err != nil {
		return Transfer{}, err
	}
	transferID, err = requireUUID("transfer_id", transferID)
	if err != nil {
		return Transfer{}, err
	}
	actorID, err := optionalUUID(actor)
	if err != nil {
		return Transfer{}, err
	}
	lineID, err := requireUUID("line_id", input.LineID)
	if err != nil {
		return Transfer{}, err
	}
	input.ResolutionType = strings.ToUpper(strings.TrimSpace(input.ResolutionType))
	if input.ResolutionType != TransferResolutionDeliver && input.ResolutionType != TransferResolutionReturn && input.ResolutionType != TransferResolutionWaste {
		return Transfer{}, fmt.Errorf("%w: transfer çözüm türü geçersiz", identity.ErrValidation)
	}
	input.Reason = strings.TrimSpace(input.Reason)
	if input.Reason == "" {
		return Transfer{}, codeError(ErrInvalidReason.Error(), ErrInvalidReason, "transfer farkı için açık gerekçe gereklidir")
	}
	quantity, err := cleanQuantity("quantity", input.Quantity, true)
	if err != nil {
		return Transfer{}, err
	}
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if input.IdempotencyKey == "" {
		reasonDigest := sha256.Sum256([]byte(input.Reason))
		input.IdempotencyKey = fmt.Sprintf("transfer:%s:%s:resolution:%s:%s:%x", transferID, lineID, input.ResolutionType, quantity, reasonDigest[:8])
	}

	tx, err := s.begin(ctx)
	if err != nil {
		return Transfer{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var transfer Transfer
	err = tx.QueryRow(ctx, `SELECT id,company_id,source_warehouse_id,destination_warehouse_id,transit_warehouse_id,state,version FROM warehouse_transfers WHERE company_id=$1 AND id=$2 FOR UPDATE`, companyID, transferID).Scan(&transfer.ID, &transfer.CompanyID, &transfer.SourceWarehouseID, &transfer.DestinationWarehouseID, &transfer.TransitWarehouseID, &transfer.State, &transfer.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return Transfer{}, ErrNotFound
	}
	if err != nil {
		return Transfer{}, err
	}
	if err = ensureTransferAccess(ctx, tx, companyID, transferID, actor); err != nil {
		return Transfer{}, err
	}
	var existingTransferID, existingType, existingQuantity, existingReason, existingLineID string
	err = tx.QueryRow(ctx, `SELECT transfer_id,resolution_type,quantity::text,reason,transfer_line_id FROM warehouse_transfer_discrepancy_resolutions WHERE company_id=$1 AND idempotency_key=$2`, companyID, input.IdempotencyKey).Scan(&existingTransferID, &existingType, &existingQuantity, &existingReason, &existingLineID)
	if err == nil {
		if existingTransferID != transferID || existingType != input.ResolutionType || existingQuantity != quantity || existingReason != input.Reason || existingLineID != lineID {
			return Transfer{}, codeError(ErrIdempotencyConflict.Error(), ErrIdempotencyConflict, "aynı çözüm anahtarı farklı veriyle kullanıldı")
		}
		if err = tx.Rollback(context.WithoutCancel(ctx)); err != nil {
			return Transfer{}, err
		}
		return s.GetTransfer(ctx, companyID, transferID, actor)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Transfer{}, err
	}
	if transfer.State != TransferInTransit && transfer.State != TransferPartiallyReceived {
		return Transfer{}, ValidateTransferTransition(transfer.State, TransferPartiallyReceived)
	}

	var line TransferLine
	err = tx.QueryRow(ctx, `SELECT id,product_id,variant_id,lot_id,serial_id,source_location_id,destination_location_id,quantity::text,shipped_quantity::text,received_quantity::text,damaged_quantity::text FROM warehouse_transfer_lines WHERE company_id=$1 AND transfer_id=$2 AND id=$3 FOR UPDATE`, companyID, transferID, lineID).Scan(&line.ID, &line.ProductID, &line.VariantID, &line.LotID, &line.SerialID, &line.SourceLocationID, &line.DestinationLocationID, &line.Quantity, &line.ShippedQuantity, &line.ReceivedQuantity, &line.DamagedQuantity)
	if errors.Is(err, pgx.ErrNoRows) {
		return Transfer{}, ErrNotFound
	}
	if err != nil {
		return Transfer{}, err
	}
	var resolved string
	if err = tx.QueryRow(ctx, `SELECT COALESCE(SUM(quantity),0)::text FROM warehouse_transfer_discrepancy_resolutions WHERE company_id=$1 AND transfer_line_id=$2`, companyID, lineID).Scan(&resolved); err != nil {
		return Transfer{}, err
	}
	if decimalCompare(quantity, decimalSub(decimalSub(line.ShippedQuantity, line.ReceivedQuantity), resolved)) > 0 {
		return Transfer{}, validationError("çözüm miktarı transit farkını aşamaz")
	}

	resolutionID := uuid.NewString()
	if _, err = tx.Exec(ctx, `INSERT INTO warehouse_transfer_discrepancy_resolutions(id,company_id,transfer_id,transfer_line_id,resolution_type,quantity,reason,idempotency_key,actor_user_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, resolutionID, companyID, transferID, lineID, input.ResolutionType, quantity, input.Reason, input.IdempotencyKey, actorID); err != nil {
		return Transfer{}, mapInventoryError(err)
	}

	operationKey := "transfer:" + transferID + ":" + lineID + ":resolution:" + resolutionID
	out := movementForTransfer(companyID, transfer.TransitWarehouseID, "", line.ProductID, valueOrEmpty(line.VariantID), valueOrEmpty(line.LotID), valueOrEmpty(line.SerialID), MovementTransferOut, DirectionOut, quantity, transferID, lineID, operationKey+":out")
	if input.ResolutionType == TransferResolutionWaste {
		out.MovementType = MovementWaste
		out.ReasonCode = "OTHER"
		out.ReasonDescription = input.Reason
	}
	out.ActorUserID = valueOrEmptyAny(actorID)
	outMovement, postErr := postMovementTx(ctx, tx, out, movementHash(out, false))
	if postErr != nil {
		return Transfer{}, mapInventoryError(postErr)
	}
	if input.ResolutionType != TransferResolutionWaste {
		targetWarehouse, targetLocation := transfer.DestinationWarehouseID, valueOrEmpty(line.DestinationLocationID)
		if input.ResolutionType == TransferResolutionReturn {
			targetWarehouse, targetLocation = transfer.SourceWarehouseID, valueOrEmpty(line.SourceLocationID)
		}
		in := movementForTransfer(companyID, targetWarehouse, targetLocation, line.ProductID, valueOrEmpty(line.VariantID), valueOrEmpty(line.LotID), valueOrEmpty(line.SerialID), MovementTransferIn, DirectionIn, quantity, transferID, lineID, operationKey+":in")
		in.ActorUserID = valueOrEmptyAny(actorID)
		in.UnitCost, in.Currency, err = transferCostBasis(ctx, tx, companyID, outMovement.ID)
		if err != nil {
			return Transfer{}, err
		}
		if _, err = postMovementTx(ctx, tx, in, movementHash(in, false)); err != nil {
			return Transfer{}, mapInventoryError(err)
		}
	}

	var incomplete bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM warehouse_transfer_lines l WHERE l.company_id=$1 AND l.transfer_id=$2 AND l.received_quantity + COALESCE((SELECT SUM(r.quantity) FROM warehouse_transfer_discrepancy_resolutions r WHERE r.company_id=l.company_id AND r.transfer_line_id=l.id),0) < l.shipped_quantity)`, companyID, transferID).Scan(&incomplete); err != nil {
		return Transfer{}, err
	}
	state := TransferReceived
	if incomplete {
		state = TransferPartiallyReceived
	}
	if _, err = tx.Exec(ctx, `UPDATE warehouse_transfers SET state=$1,received_at=CASE WHEN $1='RECEIVED' THEN now() ELSE received_at END,version=version+1,updated_at=now() WHERE company_id=$2 AND id=$3 AND version=$4`, state, companyID, transferID, transfer.Version); err != nil {
		return Transfer{}, mapInventoryError(err)
	}
	if err = writeInventoryAuditTx(ctx, tx, companyID, actor, "INVENTORY_TRANSFER_"+state, transferID, map[string]any{"partial": incomplete}); err != nil {
		return Transfer{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Transfer{}, err
	}
	return s.GetTransfer(ctx, companyID, transferID, actor)
}

func decimalSub(left, right string) string {
	l, lok := new(big.Rat).SetString(left)
	r, rok := new(big.Rat).SetString(right)
	if !lok || !rok {
		return "0"
	}
	return formatRat(new(big.Rat).Sub(l, r))
}

func decimalMax(left, right string) string {
	l, lok := new(big.Rat).SetString(left)
	r, rok := new(big.Rat).SetString(right)
	if !lok {
		return right
	}
	if !rok || l.Cmp(r) >= 0 {
		return left
	}
	return right
}

func decimalAdd(left, right string) string {
	l, lok := new(big.Rat).SetString(left)
	r, rok := new(big.Rat).SetString(right)
	if !lok || !rok {
		return "0"
	}
	return formatRat(new(big.Rat).Add(l, r))
}

// transferOutstandingQuantity is the quantity still owned by the transit
// warehouse. Received quantity has already been posted to the destination;
// every discrepancy resolution has already removed its quantity from
// transit. Cancellation returns only this remainder to the source.
func transferOutstandingQuantity(shipped, received, resolved string) string {
	remaining := decimalSub(decimalSub(shipped, received), resolved)
	if decimalCompare(remaining, "0") <= 0 {
		return "0"
	}
	return remaining
}

func formatRatMust(value *big.Rat) string { return formatRat(value) }

func (s *Service) CancelTransfer(ctx context.Context, companyID, id, reason, actor string, expectedVersion int64) (Transfer, error) {
	if strings.TrimSpace(reason) == "" {
		return Transfer{}, validationError("transfer iptal gerekçesi gereklidir")
	}
	companyID, err := requireUUID("company_id", companyID)
	if err != nil {
		return Transfer{}, err
	}
	id, err = requireUUID("transfer_id", id)
	if err != nil {
		return Transfer{}, err
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return Transfer{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var transfer Transfer
	var version int64
	if err = tx.QueryRow(ctx, `SELECT id,company_id,source_warehouse_id,destination_warehouse_id,transit_warehouse_id,state,version FROM warehouse_transfers WHERE company_id=$1 AND id=$2 FOR UPDATE`, companyID, id).Scan(&transfer.ID, &transfer.CompanyID, &transfer.SourceWarehouseID, &transfer.DestinationWarehouseID, &transfer.TransitWarehouseID, &transfer.State, &version); errors.Is(err, pgx.ErrNoRows) {
		return Transfer{}, ErrNotFound
	} else if err != nil {
		return Transfer{}, err
	}
	// A retry after the atomic cancellation commit is safe and must not post
	// another return movement. RECEIVED remains non-cancellable below because
	// destination stock has already been accepted as delivered.
	if transfer.State == TransferCancelled {
		if err = ensureTransferReadAccess(ctx, tx, companyID, id, actor); err != nil {
			return Transfer{}, err
		}
		if err = tx.Rollback(context.WithoutCancel(ctx)); err != nil {
			return Transfer{}, err
		}
		return s.GetTransfer(ctx, companyID, id, actor)
	}
	if err = ensureTransferAccess(ctx, tx, companyID, id, actor); err != nil {
		return Transfer{}, err
	}
	if expectedVersion > 0 && expectedVersion != version {
		return Transfer{}, ErrConflict
	}
	if !CanTransitionTransfer(transfer.State, TransferCancelled) {
		return Transfer{}, ValidateTransferTransition(transfer.State, TransferCancelled)
	}
	if transfer.State == TransferRequested || transfer.State == TransferApproved {
		if err = releaseTransferReservationsTx(ctx, tx, companyID, id); err != nil {
			return Transfer{}, mapInventoryError(err)
		}
	}

	if transfer.State == TransferInTransit || transfer.State == TransferPartiallyReceived {
		rows, queryErr := tx.Query(ctx, `
			SELECT l.id,l.product_id,l.variant_id,l.lot_id,l.serial_id,
			       l.source_location_id,l.shipped_quantity::text,
			       l.received_quantity::text,
			       COALESCE((SELECT SUM(r.quantity)
			           FROM warehouse_transfer_discrepancy_resolutions r
			          WHERE r.company_id=l.company_id AND r.transfer_line_id=l.id),0)::text
			  FROM warehouse_transfer_lines l
			 WHERE l.company_id=$1 AND l.transfer_id=$2
			 ORDER BY l.line_no
			 FOR UPDATE`, companyID, id)
		if queryErr != nil {
			return Transfer{}, queryErr
		}
		type cancellationLine struct {
			id, productID, shipped, received, resolved   string
			variantID, lotID, serialID, sourceLocationID *string
		}
		lines := make([]cancellationLine, 0)
		for rows.Next() {
			var line cancellationLine
			if err = rows.Scan(&line.id, &line.productID, &line.variantID, &line.lotID, &line.serialID, &line.sourceLocationID, &line.shipped, &line.received, &line.resolved); err != nil {
				rows.Close()
				return Transfer{}, err
			}
			lines = append(lines, line)
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return Transfer{}, err
		}
		rows.Close()

		for _, line := range lines {
			quantity := transferOutstandingQuantity(line.shipped, line.received, line.resolved)
			if quantity == "0" {
				continue
			}
			operationKey := "transfer:" + id + ":" + line.id + ":cancel-return"
			out := movementForTransfer(companyID, transfer.TransitWarehouseID, "", line.productID, valueOrEmpty(line.variantID), valueOrEmpty(line.lotID), valueOrEmpty(line.serialID), MovementTransferOut, DirectionOut, quantity, id, line.id, operationKey+":out")
			out.ActorUserID = actor
			out.ReasonDescription = "İptal edilen sevkiyatın transit depodan geri çıkışı"
			outMovement, postErr := postMovementTx(ctx, tx, out, movementHash(out, false))
			if postErr != nil {
				return Transfer{}, mapInventoryError(postErr)
			}
			in := movementForTransfer(companyID, transfer.SourceWarehouseID, valueOrEmpty(line.sourceLocationID), line.productID, valueOrEmpty(line.variantID), valueOrEmpty(line.lotID), valueOrEmpty(line.serialID), MovementTransferIn, DirectionIn, quantity, id, line.id, operationKey+":in")
			in.ActorUserID = actor
			in.ReasonDescription = "İptal edilen sevkiyatın çıkış deposuna geri girişi"
			in.UnitCost, in.Currency, err = transferCostBasis(ctx, tx, companyID, outMovement.ID)
			if err != nil {
				return Transfer{}, err
			}
			if _, err = postMovementTx(ctx, tx, in, movementHash(in, false)); err != nil {
				return Transfer{}, mapInventoryError(err)
			}
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE warehouse_transfers SET state='CANCELLED',cancellation_reason=$1,cancelled_at=now(),version=version+1,updated_at=now() WHERE company_id=$2 AND id=$3 AND version=$4`, reason, companyID, id, version); err != nil {
		return Transfer{}, mapInventoryError(err)
	}
	if err = writeInventoryAuditTx(ctx, tx, companyID, actor, "INVENTORY_TRANSFER_CANCELLED", id, map[string]any{"reason": reason, "returned_to_source": transfer.State == TransferInTransit || transfer.State == TransferPartiallyReceived}); err != nil {
		return Transfer{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Transfer{}, err
	}
	return s.GetTransfer(ctx, companyID, id, actor)
}

func (s *Service) StartStockCount(ctx context.Context, input StockCountInput) (StockCount, error) {
	if input.BlindCount {
		return StockCount{}, fmt.Errorf("%w: blind count is no longer supported", identity.ErrValidation)
	}
	companyID, err := requireUUID("company_id", input.CompanyID)
	if err != nil {
		return StockCount{}, err
	}
	warehouseID, err := requireUUID("warehouse_id", input.WarehouseID)
	if err != nil {
		return StockCount{}, err
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return StockCount{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err = ensureStandardWarehouse(ctx, tx, companyID, input.PostedBy, warehouseID); err != nil {
		return StockCount{}, err
	}
	id := uuid.NewString()
	var snapshotAt time.Time
	err = tx.QueryRow(ctx, `INSERT INTO stock_counts(id,company_id,warehouse_id,state,blind_count,snapshot_at) VALUES($1,$2,$3,'IN_PROGRESS',$4,clock_timestamp()) RETURNING snapshot_at`, id, companyID, warehouseID, input.BlindCount).Scan(&snapshotAt)
	if err != nil {
		return StockCount{}, mapInventoryError(err)
	}
	// Build the snapshot from ledger rows at snapshot_at, rather than from the
	// live projection. A concurrent movement committed after snapshot creation
	// must be included only by the post-time reconciliation query.
	rows, err := tx.Query(ctx, `SELECT product_id,
		COALESCE(variant_id::text,''),COALESCE(location_id::text,''),COALESCE(lot_id::text,''),COALESCE(serial_id::text,''),
		SUM(quantity_delta)::text
		FROM stock_movements WHERE company_id=$1 AND warehouse_id=$2 AND posted_at <= $3
		GROUP BY product_id,variant_id,location_id,lot_id,serial_id
		HAVING SUM(quantity_delta)<>0
		ORDER BY product_id,variant_id NULLS FIRST,location_id NULLS FIRST,lot_id NULLS FIRST,serial_id NULLS FIRST`, companyID, warehouseID, snapshotAt)
	if err != nil {
		return StockCount{}, err
	}
	type countSnapshotLine struct {
		productID  string
		variantID  string
		locationID string
		lotID      string
		serialID   string
		quantity   string
	}
	snapshotLines := make([]countSnapshotLine, 0)
	for rows.Next() {
		var line countSnapshotLine
		if err = rows.Scan(&line.productID, &line.variantID, &line.locationID, &line.lotID, &line.serialID, &line.quantity); err != nil {
			rows.Close()
			return StockCount{}, err
		}
		snapshotLines = append(snapshotLines, line)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return StockCount{}, err
	}
	// pgx does not allow another query on a transaction connection while its
	// current Rows is open. Materialize the immutable snapshot first, then close
	// the reader before validating and inserting the count lines.
	rows.Close()
	for index, line := range snapshotLines {
		if _, err = validateInventoryVariantTx(ctx, tx, companyID, line.productID, line.variantID); err != nil {
			return StockCount{}, err
		}
		lineID := uuid.NewString()
		if _, err = tx.Exec(ctx, `INSERT INTO stock_count_lines(id,company_id,count_id,line_no,product_id,variant_id,location_id,lot_id,serial_id,snapshot_quantity) VALUES($1,$2,$3,$4,$5,NULLIF($6,'')::uuid,NULLIF($7,'')::uuid,NULLIF($8,'')::uuid,NULLIF($9,'')::uuid,$10)`, lineID, companyID, id, index+1, line.productID, line.variantID, line.locationID, line.lotID, line.serialID, line.quantity); err != nil {
			return StockCount{}, mapInventoryError(err)
		}
	}
	if err = writeInventoryAuditTx(ctx, tx, companyID, input.PostedBy, "INVENTORY_COUNT_STARTED", id, map[string]any{"blind_count": input.BlindCount}); err != nil {
		return StockCount{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return StockCount{}, err
	}
	return s.GetStockCount(ctx, companyID, id)
}

func (s *Service) StartCount(ctx context.Context, input StockCountInput) (StockCount, error) {
	return s.StartStockCount(ctx, input)
}

func (s *Service) SetCountedQuantity(ctx context.Context, companyID, countID, lineID, quantity string, actors ...string) error {
	companyID, err := requireUUID("company_id", companyID)
	if err != nil {
		return err
	}
	if _, err = requireUUID("count_id", countID); err != nil {
		return err
	}
	if _, err = requireUUID("line_id", lineID); err != nil {
		return err
	}
	quantity, err = parseNonNegative("counted_quantity", quantity)
	if err != nil {
		return err
	}
	if len(actors) > 0 && strings.TrimSpace(actors[0]) != "" {
		var warehouseID string
		if err = s.pool.QueryRow(ctx, `SELECT warehouse_id FROM stock_counts WHERE company_id=$1 AND id=$2`, companyID, countID).Scan(&warehouseID); errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		if err = ensureWarehouseAccess(ctx, s.pool, companyID, strings.TrimSpace(actors[0]), warehouseID); err != nil {
			return err
		}
	}
	result, err := s.pool.Exec(ctx, `UPDATE stock_count_lines l SET counted_quantity=$1,counted_at=now() FROM stock_counts c WHERE l.company_id=$2 AND l.id=$3 AND c.company_id=l.company_id AND c.id=l.count_id AND c.id=$4 AND c.state IN ('IN_PROGRESS','COUNTED','REVIEW')`, quantity, companyID, lineID, countID)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		var state string
		if scanErr := s.pool.QueryRow(ctx, `SELECT state FROM stock_counts WHERE company_id=$1 AND id=$2`, companyID, countID).Scan(&state); scanErr == nil && (state == CountPosted || state == CountCancelled) {
			return codeError(ErrStockCountAlreadyPosted.Error(), ErrStockCountAlreadyPosted, "post edilmiş sayım değiştirilemez")
		}
		return ErrNotFound
	}
	return nil
}

func (s *Service) GetStockCount(ctx context.Context, companyID, id string, userIDs ...string) (StockCount, error) {
	if _, err := requireUUID("company_id", companyID); err != nil {
		return StockCount{}, err
	}
	if _, err := requireUUID("count_id", id); err != nil {
		return StockCount{}, err
	}
	if userID := optionalActor(userIDs); userID != "" {
		var warehouseID string
		if err := s.pool.QueryRow(ctx, `SELECT warehouse_id FROM stock_counts WHERE company_id=$1 AND id=$2`, companyID, id).Scan(&warehouseID); errors.Is(err, pgx.ErrNoRows) {
			return StockCount{}, ErrNotFound
		} else if err != nil {
			return StockCount{}, err
		} else if err = ensureWarehouseAccess(ctx, s.pool, companyID, userID, warehouseID); err != nil {
			return StockCount{}, err
		}
	}
	var item StockCount
	err := s.pool.QueryRow(ctx, `SELECT id,company_id,warehouse_id,state,blind_count,snapshot_at,posted_at,version FROM stock_counts WHERE company_id=$1 AND id=$2`, companyID, id).Scan(&item.ID, &item.CompanyID, &item.WarehouseID, &item.State, &item.BlindCount, &item.SnapshotAt, &item.PostedAt, &item.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return StockCount{}, ErrNotFound
	}
	if err != nil {
		return StockCount{}, err
	}
	rows, err := s.pool.Query(ctx, `SELECT id,line_no,product_id,variant_id,location_id,lot_id,serial_id,snapshot_quantity::text,counted_quantity::text,expected_quantity::text,difference_quantity::text FROM stock_count_lines WHERE company_id=$1 AND count_id=$2 ORDER BY line_no`, companyID, id)
	if err != nil {
		return StockCount{}, err
	}
	defer rows.Close()
	item.Lines = []StockCountLine{}
	for rows.Next() {
		var line StockCountLine
		if err = rows.Scan(&line.ID, &line.LineNo, &line.ProductID, &line.VariantID, &line.LocationID, &line.LotID, &line.SerialID, &line.SnapshotQuantity, &line.CountedQuantity, &line.ExpectedQuantity, &line.DifferenceQuantity); err != nil {
			return StockCount{}, err
		}
		if item.BlindCount && item.State != CountReview && item.State != CountPosted {
			// Blind count must not leak the snapshot or the derived expected
			// quantity before review. The values remain in the database for the
			// immutable reconciliation; only this read model is redacted.
			line.SnapshotQuantity = nil
			line.ExpectedQuantity = nil
			line.DifferenceQuantity = nil
		}
		item.Lines = append(item.Lines, line)
	}
	if err = rows.Err(); err != nil {
		return StockCount{}, err
	}
	if err = s.enrichStockCountLines(ctx, companyID, item.Lines); err != nil {
		return StockCount{}, err
	}
	return item, nil
}

func (s *Service) ListStockCounts(ctx context.Context, companyID, state string, limit int, userIDs ...string) ([]StockCount, error) {
	if _, err := requireUUID("company_id", companyID); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 100 {
		limit = 50
	}
	query, args := stockCountsListQuery(companyID, state, limit, userIDs...)
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	// GetStockCount issues its own queries; run it only after the id rows are
	// drained, otherwise the request-pinned connection fails with "conn busy".
	items := make([]StockCount, 0, len(ids))
	for _, id := range ids {
		item, getErr := s.GetStockCount(ctx, companyID, id, optionalActor(userIDs))
		if getErr != nil {
			return nil, getErr
		}
		items = append(items, item)
	}
	return items, nil
}

func stockCountsListQuery(companyID, state string, limit int, userIDs ...string) (string, []any) {
	args := []any{companyID}
	query := `SELECT c.id FROM stock_counts c JOIN warehouses w ON w.company_id=c.company_id AND w.id=c.warehouse_id WHERE c.company_id=$1 AND w.is_active AND w.warehouse_type='STANDARD'`
	if userID := optionalActor(userIDs); userID != "" {
		args = append(args, userID)
		query += fmt.Sprintf(` AND c.warehouse_id IN (SELECT w.id FROM warehouses w WHERE w.company_id=$1 AND w.is_active AND (w.branch_id IS NULL OR NOT EXISTS (SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=$1 AND bs.user_id=$%d) OR EXISTS (SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=$1 AND bs.user_id=$%d AND bs.branch_id=w.branch_id)) AND (NOT EXISTS (SELECT 1 FROM membership_warehouse_scopes ws WHERE ws.company_id=$1 AND ws.user_id=$%d) OR EXISTS (SELECT 1 FROM membership_warehouse_scopes ws WHERE ws.company_id=$1 AND ws.user_id=$%d AND ws.warehouse_id=w.id)))`, len(args), len(args), len(args), len(args))
	}
	if state = strings.TrimSpace(state); state != "" {
		args = append(args, strings.ToUpper(state))
		query += fmt.Sprintf(` AND c.state=$%d`, len(args))
	}
	args = append(args, limit)
	query += fmt.Sprintf(` ORDER BY c.created_at DESC,c.id DESC LIMIT $%d`, len(args))
	return query, args
}

func (s *Service) CreateLot(ctx context.Context, input LotInput) (Lot, error) {
	if uuid.Validate(strings.TrimSpace(input.ActorUserID)) != nil {
		return Lot{}, identity.ErrForbidden
	}
	companyID, err := requireUUID("company_id", input.CompanyID)
	if err != nil {
		return Lot{}, err
	}
	productID, err := requireUUID("product_id", input.ProductID)
	if err != nil {
		return Lot{}, err
	}
	input.LotNumber = strings.TrimSpace(input.LotNumber)
	if input.LotNumber == "" {
		return Lot{}, validationError("lot numarası gereklidir")
	}
	if input.ManufacturedAt != nil && input.ExpiresAt != nil && input.ExpiresAt.Before(*input.ManufacturedAt) {
		return Lot{}, validationError("lot son kullanma tarihi üretim tarihinden önce olamaz")
	}
	if input.Metadata == nil {
		input.Metadata = map[string]any{}
	}
	metadata, err := json.Marshal(input.Metadata)
	if err != nil {
		return Lot{}, err
	}
	id := uuid.NewString()
	if _, err = s.pool.Exec(ctx, `INSERT INTO lots(id,company_id,product_id,lot_number,manufactured_at,expires_at,supplier_reference,metadata) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, id, companyID, productID, input.LotNumber, input.ManufacturedAt, input.ExpiresAt, strings.TrimSpace(input.SupplierReference), metadata); err != nil {
		return Lot{}, mapInventoryError(err)
	}
	var result Lot
	var resultMetadata []byte
	err = s.pool.QueryRow(ctx, `SELECT id,company_id,product_id,lot_number,manufactured_at,expires_at,supplier_reference,metadata,created_at FROM lots WHERE company_id=$1 AND id=$2`, companyID, id).Scan(&result.ID, &result.CompanyID, &result.ProductID, &result.LotNumber, &result.ManufacturedAt, &result.ExpiresAt, &result.SupplierReference, &resultMetadata, &result.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Lot{}, ErrNotFound
	}
	if err != nil {
		return Lot{}, err
	}
	result.Metadata = map[string]any{}
	if len(resultMetadata) > 0 {
		_ = json.Unmarshal(resultMetadata, &result.Metadata)
	}
	return result, nil
}

func (s *Service) CreateSerialNumber(ctx context.Context, input SerialNumberInput) (SerialNumber, error) {
	if uuid.Validate(strings.TrimSpace(input.ActorUserID)) != nil {
		return SerialNumber{}, identity.ErrForbidden
	}
	companyID, err := requireUUID("company_id", input.CompanyID)
	if err != nil {
		return SerialNumber{}, err
	}
	productID, err := requireUUID("product_id", input.ProductID)
	if err != nil {
		return SerialNumber{}, err
	}
	input.SerialNumber = strings.TrimSpace(input.SerialNumber)
	if input.SerialNumber == "" {
		return SerialNumber{}, validationError("seri numarası gereklidir")
	}
	status := strings.ToUpper(strings.TrimSpace(input.Status))
	if status == "" {
		status = "IN_STOCK"
	}
	validStatuses := map[string]bool{"IN_STOCK": true, "RESERVED": true, "DISPATCHED": true, "RETURNED": true, "QUARANTINED": true}
	if !validStatuses[status] {
		return SerialNumber{}, validationError("seri durumu geçersiz")
	}
	warehouseID, err := optionalUUID(input.ActiveWarehouseID)
	if err != nil {
		return SerialNumber{}, err
	}
	if status == "DISPATCHED" && warehouseID != nil {
		return SerialNumber{}, fmt.Errorf("%w: sevk edilmiş seri aktif depoda tutulamaz", identity.ErrValidation)
	}
	if status != "DISPATCHED" && warehouseID == nil {
		return SerialNumber{}, fmt.Errorf("%w: aktif seri için depo gereklidir", identity.ErrValidation)
	}
	if input.ActiveWarehouseID != "" {
		if err = ensureWarehouseAccess(ctx, s.pool, companyID, input.ActorUserID, input.ActiveWarehouseID); err != nil {
			return SerialNumber{}, err
		}
	}
	locationID, err := optionalUUID(input.ActiveLocationID)
	if err != nil {
		return SerialNumber{}, err
	}
	if input.ActiveLocationID != "" && input.ActiveWarehouseID == "" {
		return SerialNumber{}, validationError("aktif lokasyon için depo gereklidir")
	}
	id := uuid.NewString()
	if _, err = s.pool.Exec(ctx, `INSERT INTO serial_numbers(id,company_id,product_id,serial_number,status,active_warehouse_id,active_location_id) VALUES($1,$2,$3,$4,$5,$6,$7)`, id, companyID, productID, input.SerialNumber, status, warehouseID, locationID); err != nil {
		return SerialNumber{}, mapInventoryError(err)
	}
	var result SerialNumber
	err = s.pool.QueryRow(ctx, `SELECT id,company_id,product_id,serial_number,status,active_warehouse_id,active_location_id,created_at,updated_at FROM serial_numbers WHERE company_id=$1 AND id=$2`, companyID, id).Scan(&result.ID, &result.CompanyID, &result.ProductID, &result.SerialNumber, &result.Status, &result.ActiveWarehouseID, &result.ActiveLocationID, &result.CreatedAt, &result.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return SerialNumber{}, ErrNotFound
	}
	return result, err
}

func (s *Service) ListLots(ctx context.Context, companyID, productID, search string, limit int, warehouseIDs ...string) ([]Lot, error) {
	if _, err := requireUUID("company_id", companyID); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 200 {
		limit = 50
	}
	warehouseID := ""
	if len(warehouseIDs) > 0 {
		warehouseID = strings.TrimSpace(warehouseIDs[0])
	}
	userID := ""
	if len(warehouseIDs) > 1 {
		userID = strings.TrimSpace(warehouseIDs[1])
	}
	var userArg any
	if userID != "" {
		if _, err := requireUUID("user_id", userID); err != nil {
			return nil, err
		}
		userArg = userID
	}
	if warehouseID != "" {
		if _, err := requireUUID("warehouse_id", warehouseID); err != nil {
			return nil, err
		}
	}
	var warehouseArg any
	if warehouseID != "" {
		warehouseArg = warehouseID
		if err := ensureStandardWarehouse(ctx, s.pool, companyID, userID, warehouseID); err != nil {
			return nil, err
		}
	}
	args := []any{companyID, warehouseArg, userArg}
	query := `SELECT l.id,l.company_id,l.product_id,l.lot_number,
		COALESCE(
			SUM(
				CASE
					WHEN ($2::uuid IS NULL OR sm.warehouse_id=$2::uuid)
					 AND EXISTS (
						SELECT 1 FROM warehouses sw0
						WHERE sw0.company_id=sm.company_id AND sw0.id=sm.warehouse_id
						  AND sw0.is_active AND sw0.warehouse_type='STANDARD'
					 )
					 AND (
						$3::uuid IS NULL
						OR EXISTS (
							SELECT 1
							FROM warehouses sw
								WHERE sw.company_id=l.company_id
								  AND sw.id=sm.warehouse_id
								  AND sw.is_active
								  AND sw.warehouse_type='STANDARD'
							  AND (
								 sw.is_system
								 OR (
									 (sw.branch_id IS NULL
									  OR NOT EXISTS (
										 SELECT 1 FROM membership_branch_scopes bs
										 WHERE bs.company_id=l.company_id AND bs.user_id=$3::uuid
									  )
									  OR EXISTS (
										 SELECT 1 FROM membership_branch_scopes bs
										 WHERE bs.company_id=l.company_id AND bs.user_id=$3::uuid AND bs.branch_id=sw.branch_id
									  ))
									 AND (NOT EXISTS (
										 SELECT 1 FROM membership_warehouse_scopes ws
										 WHERE ws.company_id=l.company_id AND ws.user_id=$3::uuid
									  )
									  OR EXISTS (
										 SELECT 1 FROM membership_warehouse_scopes ws
										 WHERE ws.company_id=l.company_id AND ws.user_id=$3::uuid AND ws.warehouse_id=sw.id
									  ))
								 )
							  )
						)
					THEN sm.quantity_delta
					ELSE 0
				END
			),0)::text,
		l.manufactured_at,l.expires_at,l.supplier_reference,l.metadata,l.created_at
		FROM lots l LEFT JOIN stock_movements sm ON sm.company_id=l.company_id AND sm.lot_id=l.id
		WHERE l.company_id=$1`
	if strings.TrimSpace(productID) != "" {
		if _, err := requireUUID("product_id", productID); err != nil {
			return nil, err
		}
		args = append(args, productID)
		query += fmt.Sprintf(` AND l.product_id=$%d`, len(args))
	}
	search = strings.TrimSpace(search)
	if len(search) > 128 {
		return nil, fmt.Errorf("%w: arama metni çok uzun", identity.ErrValidation)
	}
	for _, token := range strings.Fields(search) {
		args = append(args, "%"+escapeSearchToken(token)+"%")
		param := len(args)
		query += fmt.Sprintf(` AND (
			l.lot_number ILIKE $%d ESCAPE '\'
			OR COALESCE(l.supplier_reference,'') ILIKE $%d ESCAPE '\'
			OR EXISTS (SELECT 1 FROM products pq WHERE pq.company_id=l.company_id AND pq.id=l.product_id AND (pq.code ILIKE $%d ESCAPE '\' OR pq.name ILIKE $%d ESCAPE '\'))
		)`, param, param, param, param)
	}
	args = append(args, limit)
	query += fmt.Sprintf(` GROUP BY l.id,l.company_id,l.product_id,l.lot_number,l.manufactured_at,l.expires_at,l.supplier_reference,l.metadata,l.created_at
		HAVING COALESCE(SUM(CASE
			WHEN ($2::uuid IS NULL OR sm.warehouse_id=$2::uuid)
			 AND EXISTS (
				SELECT 1 FROM warehouses sw0
				WHERE sw0.company_id=sm.company_id AND sw0.id=sm.warehouse_id
				  AND sw0.is_active AND sw0.warehouse_type='STANDARD'
			 )
			 AND (
				$3::uuid IS NULL
				OR EXISTS (
					SELECT 1 FROM warehouses sw
					WHERE sw.company_id=l.company_id AND sw.id=sm.warehouse_id
					  AND sw.is_active AND sw.warehouse_type='STANDARD'
					  AND (
						 sw.is_system
						 OR (
							 (sw.branch_id IS NULL OR NOT EXISTS (
								 SELECT 1 FROM membership_branch_scopes bs
								 WHERE bs.company_id=l.company_id AND bs.user_id=$3::uuid
							 ) OR EXISTS (
								 SELECT 1 FROM membership_branch_scopes bs
								 WHERE bs.company_id=l.company_id AND bs.user_id=$3::uuid AND bs.branch_id=sw.branch_id
							 ))
							 AND (NOT EXISTS (
								 SELECT 1 FROM membership_warehouse_scopes ws
								 WHERE ws.company_id=l.company_id AND ws.user_id=$3::uuid
							 ) OR EXISTS (
								 SELECT 1 FROM membership_warehouse_scopes ws
								 WHERE ws.company_id=l.company_id AND ws.user_id=$3::uuid AND ws.warehouse_id=sw.id
							 ))
						 )
					  )
				)
			 )
			THEN sm.quantity_delta ELSE 0 END),0) > 0
		ORDER BY l.expires_at NULLS LAST,l.lot_number,l.id LIMIT $%d`, len(args))
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Lot, 0)
	for rows.Next() {
		var item Lot
		var metadata []byte
		if err = rows.Scan(&item.ID, &item.CompanyID, &item.ProductID, &item.LotNumber, &item.AvailableQuantity, &item.ManufacturedAt, &item.ExpiresAt, &item.SupplierReference, &metadata, &item.CreatedAt); err != nil {
			return nil, err
		}
		item.Metadata = map[string]any{}
		if len(metadata) != 0 {
			_ = json.Unmarshal(metadata, &item.Metadata)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// GetLot returns a lot together with its ledger-derived available quantity.
// When a caller has warehouse scopes, quantities from inaccessible warehouses
// are excluded and a lot with no visible movement is not disclosed.
func (s *Service) GetLot(ctx context.Context, companyID, lotID string, userIDs ...string) (Lot, error) {
	if _, err := requireUUID("company_id", companyID); err != nil {
		return Lot{}, err
	}
	if _, err := requireUUID("lot_id", lotID); err != nil {
		return Lot{}, err
	}
	var item Lot
	var metadata []byte
	err := s.pool.QueryRow(ctx, `SELECT id,company_id,product_id,lot_number,manufactured_at,expires_at,supplier_reference,metadata,created_at
		FROM lots WHERE company_id=$1 AND id=$2`, companyID, lotID).Scan(
		&item.ID, &item.CompanyID, &item.ProductID, &item.LotNumber, &item.ManufacturedAt,
		&item.ExpiresAt, &item.SupplierReference, &metadata, &item.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Lot{}, ErrNotFound
	}
	if err != nil {
		return Lot{}, err
	}
	item.Metadata = map[string]any{}
	if len(metadata) > 0 {
		if err = json.Unmarshal(metadata, &item.Metadata); err != nil {
			return Lot{}, err
		}
	}

	userID := optionalActor(userIDs)
	rows, err := s.pool.Query(ctx, `SELECT sm.warehouse_id,SUM(sm.quantity_delta)::text
		FROM stock_movements sm
		JOIN warehouses w ON w.company_id=sm.company_id AND w.id=sm.warehouse_id
		WHERE sm.company_id=$1 AND sm.lot_id=$2 AND w.is_active AND w.warehouse_type='STANDARD'
		GROUP BY sm.warehouse_id`, companyID, lotID)
	if err != nil {
		return Lot{}, err
	}
	type warehouseDelta struct{ warehouseID, quantity string }
	deltas := make([]warehouseDelta, 0)
	for rows.Next() {
		var d warehouseDelta
		if err = rows.Scan(&d.warehouseID, &d.quantity); err != nil {
			rows.Close()
			return Lot{}, err
		}
		deltas = append(deltas, d)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return Lot{}, err
	}
	rows.Close()
	// ensureWarehouseScope issues its own query; run it only after the movement
	// rows are drained, otherwise the request-pinned connection fails with
	// "conn busy".
	available := "0"
	hasMovement, hasVisibleMovement := false, false
	for _, d := range deltas {
		hasMovement = true
		if userID != "" {
			if accessErr := ensureWarehouseScope(ctx, s.pool, companyID, userID, d.warehouseID); accessErr != nil {
				if errors.Is(accessErr, identity.ErrForbidden) {
					continue
				}
				return Lot{}, accessErr
			}
		}
		hasVisibleMovement = true
		available = decimalAdd(available, d.quantity)
	}
	if userID != "" && hasMovement && !hasVisibleMovement {
		return Lot{}, identity.ErrForbidden
	}
	item.AvailableQuantity = available
	return item, nil
}

func (s *Service) ListSerialNumbers(ctx context.Context, companyID, productID, search string, limit int, warehouseIDs ...string) ([]SerialNumber, error) {
	if _, err := requireUUID("company_id", companyID); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 200 {
		limit = 50
	}
	warehouseID := ""
	if len(warehouseIDs) > 0 {
		warehouseID = strings.TrimSpace(warehouseIDs[0])
	}
	userID := ""
	if len(warehouseIDs) > 1 {
		userID = strings.TrimSpace(warehouseIDs[1])
	}
	if userID != "" {
		if _, err := requireUUID("user_id", userID); err != nil {
			return nil, err
		}
	}
	if warehouseID != "" {
		if _, err := requireUUID("warehouse_id", warehouseID); err != nil {
			return nil, err
		}
		if err := ensureStandardWarehouse(ctx, s.pool, companyID, userID, warehouseID); err != nil {
			return nil, err
		}
	}
	args := []any{companyID, warehouseID, userID}
	query := `SELECT sn.id,sn.company_id,sn.product_id,sn.serial_number,sn.status,sn.active_warehouse_id,sn.active_location_id,sn.created_at,sn.updated_at
		FROM serial_numbers sn LEFT JOIN warehouses aw ON aw.company_id=sn.company_id AND aw.id=sn.active_warehouse_id
		WHERE sn.company_id=$1 AND (sn.active_warehouse_id IS NULL OR (aw.is_active AND aw.warehouse_type='STANDARD'))`
	if warehouseID != "" {
		query += ` AND sn.active_warehouse_id=$2`
	}
	if userID != "" && warehouseID == "" {
		query += ` AND sn.active_warehouse_id IN (SELECT w.id FROM warehouses w WHERE w.company_id=$1 AND w.is_active AND w.warehouse_type='STANDARD' AND (w.is_system OR ((w.branch_id IS NULL OR NOT EXISTS (SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=$1 AND bs.user_id=$3) OR EXISTS (SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=$1 AND bs.user_id=$3 AND bs.branch_id=w.branch_id)) AND (NOT EXISTS (SELECT 1 FROM membership_warehouse_scopes ws WHERE ws.company_id=$1 AND ws.user_id=$3) OR EXISTS (SELECT 1 FROM membership_warehouse_scopes ws WHERE ws.company_id=$1 AND ws.user_id=$3 AND ws.warehouse_id=w.id)))))`
	}
	if strings.TrimSpace(productID) != "" {
		if _, err := requireUUID("product_id", productID); err != nil {
			return nil, err
		}
		args = append(args, productID)
		query += fmt.Sprintf(` AND sn.product_id=$%d`, len(args))
	}
	search = strings.TrimSpace(search)
	if len(search) > 128 {
		return nil, fmt.Errorf("%w: arama metni çok uzun", identity.ErrValidation)
	}
	for _, token := range strings.Fields(search) {
		args = append(args, "%"+escapeSearchToken(token)+"%")
		param := len(args)
		query += fmt.Sprintf(` AND (
			sn.serial_number ILIKE $%d ESCAPE '\'
			OR sn.status ILIKE $%d ESCAPE '\'
			OR EXISTS (SELECT 1 FROM products pq WHERE pq.company_id=sn.company_id AND pq.id=sn.product_id AND (pq.code ILIKE $%d ESCAPE '\' OR pq.name ILIKE $%d ESCAPE '\'))
		)`, param, param, param, param)
	}
	args = append(args, limit)
	query += fmt.Sprintf(` ORDER BY serial_number,id LIMIT $%d`, len(args))
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]SerialNumber, 0)
	for rows.Next() {
		var item SerialNumber
		if err = rows.Scan(&item.ID, &item.CompanyID, &item.ProductID, &item.SerialNumber, &item.Status, &item.ActiveWarehouseID, &item.ActiveLocationID, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// GetSerialNumber returns one serial projection while applying the current
// warehouse scope.  Sold/dispatched serials have no active warehouse; their
// historical movement warehouses are used as the visibility boundary.
func (s *Service) GetSerialNumber(ctx context.Context, companyID, serialID string, userIDs ...string) (SerialNumber, error) {
	if _, err := requireUUID("company_id", companyID); err != nil {
		return SerialNumber{}, err
	}
	if _, err := requireUUID("serial_id", serialID); err != nil {
		return SerialNumber{}, err
	}
	var item SerialNumber
	err := s.pool.QueryRow(ctx, `SELECT id,company_id,product_id,serial_number,status,active_warehouse_id,active_location_id,created_at,updated_at
		FROM serial_numbers WHERE company_id=$1 AND id=$2`, companyID, serialID).Scan(
		&item.ID, &item.CompanyID, &item.ProductID, &item.SerialNumber, &item.Status,
		&item.ActiveWarehouseID, &item.ActiveLocationID, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return SerialNumber{}, ErrNotFound
	}
	if err != nil {
		return SerialNumber{}, err
	}
	userID := optionalActor(userIDs)
	if userID == "" {
		return item, nil
	}
	if item.ActiveWarehouseID != nil {
		if err = ensureVisibleWarehouse(ctx, s.pool, companyID, userID, *item.ActiveWarehouseID); err != nil {
			return SerialNumber{}, err
		}
		return item, nil
	}

	rows, err := s.pool.Query(ctx, `SELECT DISTINCT warehouse_id FROM stock_movements WHERE company_id=$1 AND serial_id=$2`, companyID, serialID)
	if err != nil {
		return SerialNumber{}, err
	}
	warehouseIDs := make([]string, 0)
	for rows.Next() {
		var warehouseID string
		if err = rows.Scan(&warehouseID); err != nil {
			rows.Close()
			return SerialNumber{}, err
		}
		warehouseIDs = append(warehouseIDs, warehouseID)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return SerialNumber{}, err
	}
	rows.Close()
	// ensureVisibleWarehouse issues its own query; run it only after the movement
	// rows are drained, otherwise the request-pinned connection fails with
	// "conn busy".
	hasMovement, hasVisibleMovement := false, false
	for _, warehouseID := range warehouseIDs {
		hasMovement = true
		if accessErr := ensureVisibleWarehouse(ctx, s.pool, companyID, userID, warehouseID); accessErr != nil {
			if errors.Is(accessErr, identity.ErrForbidden) || errors.Is(accessErr, ErrNotFound) {
				continue
			}
			return SerialNumber{}, accessErr
		}
		hasVisibleMovement = true
	}
	if hasMovement && !hasVisibleMovement {
		return SerialNumber{}, identity.ErrForbidden
	}
	return item, nil
}

func (s *Service) PostStockCount(ctx context.Context, companyID, countID, actor string, expectedVersion int64) (StockCount, error) {
	companyID, err := requireUUID("company_id", companyID)
	if err != nil {
		return StockCount{}, err
	}
	actorID, err := optionalUUID(actor)
	if err != nil {
		return StockCount{}, err
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return StockCount{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var count StockCount
	err = tx.QueryRow(ctx, `SELECT id,company_id,warehouse_id,state,blind_count,snapshot_at,version FROM stock_counts WHERE company_id=$1 AND id=$2 FOR UPDATE`, companyID, countID).Scan(&count.ID, &count.CompanyID, &count.WarehouseID, &count.State, &count.BlindCount, &count.SnapshotAt, &count.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return StockCount{}, ErrNotFound
	}
	if err != nil {
		return StockCount{}, err
	}
	if err = ensureStandardWarehouse(ctx, tx, companyID, actor, count.WarehouseID); err != nil {
		return StockCount{}, err
	}
	if count.State == CountPosted || count.State == CountCancelled {
		return StockCount{}, codeError(ErrStockCountAlreadyPosted.Error(), ErrStockCountAlreadyPosted, "post edilmiş sayım tekrar işlenemez")
	}
	if count.State != Counted && count.State != CountReview && count.State != CountInProgress {
		return StockCount{}, ValidateCountTransition(count.State, CountPosted)
	}
	if expectedVersion > 0 && expectedVersion != count.Version {
		return StockCount{}, ErrConflict
	}
	if count.State == CountInProgress {
		// A handler may post immediately after the last counted quantity is
		// entered.  Preserve the documented state machine by recording the
		// implicit COUNTED transition in the same transaction.
		if _, err = tx.Exec(ctx, `UPDATE stock_counts SET state='COUNTED',version=version+1,updated_at=now() WHERE company_id=$1 AND id=$2 AND version=$3`, companyID, countID, count.Version); err != nil {
			return StockCount{}, mapInventoryError(err)
		}
		count.State = Counted
		count.Version++
	}
	if count.SnapshotAt == nil {
		return StockCount{}, validationError("sayım snapshot zamanı bulunamadı")
	}
	rows, err := tx.Query(ctx, `SELECT id,product_id,variant_id,location_id,lot_id,serial_id,snapshot_quantity::text,counted_quantity::text FROM stock_count_lines WHERE company_id=$1 AND count_id=$2 ORDER BY line_no FOR UPDATE`, companyID, countID)
	if err != nil {
		return StockCount{}, err
	}
	type countLine struct {
		id, productID, snapshot                string
		variantID, locationID, lotID, serialID *string
		counted                                *string
	}
	lines := []countLine{}
	for rows.Next() {
		var line countLine
		if err = rows.Scan(&line.id, &line.productID, &line.variantID, &line.locationID, &line.lotID, &line.serialID, &line.snapshot, &line.counted); err != nil {
			rows.Close()
			return StockCount{}, err
		}
		if line.counted == nil {
			rows.Close()
			return StockCount{}, validationError("tüm sayım satırları sayılmalıdır")
		}
		lines = append(lines, line)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return StockCount{}, err
	}
	for _, line := range lines {
		var movementSince string
		query := `SELECT COALESCE(SUM(quantity_delta),0)::text FROM stock_movements WHERE company_id=$1 AND warehouse_id=$2 AND product_id=$3 AND posted_at>$4 AND variant_id IS NOT DISTINCT FROM $5::uuid AND location_id IS NOT DISTINCT FROM $6::uuid AND lot_id IS NOT DISTINCT FROM $7::uuid AND serial_id IS NOT DISTINCT FROM $8::uuid`
		args := []any{companyID, count.WarehouseID, line.productID, *count.SnapshotAt, line.variantID, line.locationID, line.lotID, line.serialID}
		if err = tx.QueryRow(ctx, query, args...).Scan(&movementSince); err != nil {
			return StockCount{}, err
		}
		expected, difference, reconErr := ReconcileSnapshot(line.snapshot, []string{movementSince}, *line.counted)
		if reconErr != nil {
			return StockCount{}, reconErr
		}
		if _, err = tx.Exec(ctx, `UPDATE stock_count_lines SET expected_quantity=$1,difference_quantity=$2 WHERE company_id=$3 AND id=$4`, expected, difference, companyID, line.id); err != nil {
			return StockCount{}, err
		}
		if decimalCompare(difference, "0") != 0 {
			direction := DirectionIn
			if decimalCompare(difference, "0") < 0 {
				direction = DirectionOut
			}
			movement := MovementInput{CompanyID: companyID, WarehouseID: count.WarehouseID, ProductID: line.productID, VariantID: valueOrEmpty(line.variantID), LocationID: valueOrEmpty(line.locationID), LotID: valueOrEmpty(line.lotID), SerialID: valueOrEmpty(line.serialID), MovementType: MovementCountAdjustment, Direction: direction, Quantity: decimalAbs(difference), ReasonCode: "COUNT", ReasonDescription: "Sayım farkı", SourceType: "STOCK_COUNT", SourceID: countID, SourceLineID: line.id, IdempotencyKey: "count:" + countID + ":" + line.id, ActorUserID: valueOrEmptyAny(actorID), Metadata: map[string]any{"snapshot_quantity": line.snapshot, "expected_quantity": expected, "counted_quantity": *line.counted}}
			if _, err = postMovementTx(ctx, tx, movement, movementHash(movement, false)); err != nil {
				return StockCount{}, mapInventoryError(err)
			}
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE stock_counts SET state='POSTED',posted_at=now(),posted_by=$1,version=version+1,updated_at=now() WHERE company_id=$2 AND id=$3 AND version=$4`, actorID, companyID, countID, count.Version); err != nil {
		return StockCount{}, mapInventoryError(err)
	}
	if err = writeInventoryAuditTx(ctx, tx, companyID, actor, "INVENTORY_COUNT_POSTED", countID, nil); err != nil {
		return StockCount{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return StockCount{}, err
	}
	return s.GetStockCount(ctx, companyID, countID)
}

func (s *Service) PostCount(ctx context.Context, companyID, countID, actor string, expectedVersion int64) (StockCount, error) {
	return s.PostStockCount(ctx, companyID, countID, actor, expectedVersion)
}

func (s *Service) SuggestFEFO(ctx context.Context, companyID, warehouseID, productID string, limit int, userIDs ...string) ([]LotSuggestion, error) {
	for name, value := range map[string]string{"company_id": companyID, "warehouse_id": warehouseID, "product_id": productID} {
		if _, err := requireUUID(name, value); err != nil {
			return nil, err
		}
	}
	if err := ensureStandardWarehouse(ctx, s.pool, companyID, optionalActor(userIDs), warehouseID); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `SELECT l.id,l.lot_number,l.expires_at,SUM(sm.quantity_delta)::text
		FROM lots l JOIN stock_movements sm ON sm.company_id=l.company_id AND sm.lot_id=l.id AND sm.warehouse_id=$2
		JOIN warehouses w ON w.company_id=sm.company_id AND w.id=sm.warehouse_id AND w.is_active AND w.warehouse_type='STANDARD'
		WHERE l.company_id=$1 AND l.product_id=$3 AND (l.expires_at IS NULL OR l.expires_at >= CURRENT_DATE)
		GROUP BY l.id,l.lot_number,l.expires_at
		HAVING SUM(sm.quantity_delta)>0
		ORDER BY l.expires_at NULLS LAST,l.id LIMIT $4`, companyID, warehouseID, productID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []LotSuggestion{}
	for rows.Next() {
		var item LotSuggestion
		if err = rows.Scan(&item.LotID, &item.LotNumber, &item.ExpiresAt, &item.AvailableQty); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}
