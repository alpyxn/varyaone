package inventory

import (
	"context"
	"fmt"
	"strings"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// CommercialStockLine is the provider-neutral stock effect of one product
// line.  Warehouse and conversion are line snapshots; a document header
// warehouse is never used to silently move a line to another warehouse.
type CommercialStockLine struct {
	LineID           string
	ProductID        string
	VariantID        string
	WarehouseID      string
	Quantity         string
	BaseQuantity     string
	ConversionFactor string
	UnitCode         string
	UnitCost         string
	Currency         string
}

type CommercialStockPostingInput struct {
	DocumentID   string
	DocumentType string
	Lines        []CommercialStockLine
}

// PostCommercialStockMovementsTx is the shared stock port for typed sales and
// purchase aggregates.  It deliberately reuses the immutable movement ledger,
// advisory identity lock, unit conversion and negative-stock policy in this
// package rather than maintaining a second posting implementation.
func (s *Service) PostCommercialStockMovementsTx(ctx context.Context, tx pgx.Tx, session identity.Session, input CommercialStockPostingInput) error {
	if identity.ValidateExternalActor(session) != nil || !session.HasPermission("inventory.movement.post") {
		return identity.ErrForbidden
	}
	companyID, err := requireUUID("company_id", strings.TrimSpace(session.CurrentCompanyID))
	if err != nil {
		return err
	}
	documentID, err := requireUUID("document_id", strings.TrimSpace(input.DocumentID))
	if err != nil {
		return err
	}
	documentType := strings.ToUpper(strings.TrimSpace(input.DocumentType))
	movementType, direction := commercialMovementType(documentType)
	if movementType == "" {
		return fmt.Errorf("%w: ticari belge stok etkisi geçersiz", identity.ErrValidation)
	}
	for index, line := range input.Lines {
		lineID, lineErr := requireUUID("line_id", strings.TrimSpace(line.LineID))
		if lineErr != nil {
			return lineErr
		}
		productID, productErr := requireUUID("product_id", strings.TrimSpace(line.ProductID))
		if productErr != nil {
			return productErr
		}
		warehouseID, warehouseErr := requireUUID("warehouse_id", strings.TrimSpace(line.WarehouseID))
		if warehouseErr != nil {
			return warehouseErr
		}
		if err = ensureWarehouseAccess(ctx, tx, companyID, session.User.ID, warehouseID); err != nil {
			return err
		}
		if strings.TrimSpace(line.Quantity) == "" {
			return fmt.Errorf("%w: ticari stok miktarı gereklidir", identity.ErrValidation)
		}
		// Keep a database-backed claim alongside the movement idempotency key.
		// The line registry is present for typed sales lines; purchase adapters
		// may post before their legacy line is registered, so the optional FK is
		// left NULL in that case while the document/line effect key remains unique.
		var registryLineID string
		if claimErr := tx.QueryRow(ctx, `SELECT line_id FROM commercial_line_registry WHERE company_id=$1 AND line_id=$2`, companyID, lineID).Scan(&registryLineID); errorsIsNoRows(claimErr) {
			registryLineID = ""
		} else if claimErr != nil {
			return claimErr
		}
		claimKey := fmt.Sprintf("commercial-stock:%s:%s", documentID, lineID)
		var claimID string
		claimErr := tx.QueryRow(ctx, `INSERT INTO commercial_effect_claims(id,company_id,effect_key,effect_type,document_id,line_id) VALUES($1,$2,$3,'STOCK',$4,NULLIF($5,'')::uuid) ON CONFLICT(company_id,effect_key) DO NOTHING RETURNING id`, uuid.NewString(), companyID, claimKey, documentID, registryLineID).Scan(&claimID)
		if errorsIsNoRows(claimErr) {
			continue
		}
		if claimErr != nil {
			return claimErr
		}
		metadata := map[string]any{
			"commercial_document_type":   documentType,
			"line_no":                    index + 1,
			"base_quantity_snapshot":     line.BaseQuantity,
			"conversion_factor_snapshot": line.ConversionFactor,
		}
		movement := MovementInput{
			ID:               uuid.NewString(),
			CompanyID:        companyID,
			WarehouseID:      warehouseID,
			ProductID:        productID,
			VariantID:        strings.TrimSpace(line.VariantID),
			MovementType:     movementType,
			Direction:        direction,
			Quantity:         line.Quantity,
			EnteredQuantity:  line.Quantity,
			UnitCode:         line.UnitCode,
			ConversionFactor: line.ConversionFactor,
			UnitCost:         line.UnitCost,
			Currency:         line.Currency,
			ReasonCode:       movementType,
			SourceType:       documentType,
			SourceID:         documentID,
			SourceLineID:     lineID,
			IdempotencyKey:   fmt.Sprintf("commercial:%s:%s:stock", documentID, lineID),
			ActorUserID:      session.User.ID,
			Metadata:         metadata,
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

func commercialMovementType(documentType string) (string, string) {
	switch documentType {
	case "SALES_DISPATCH", "SALES_DELIVERY", "SALES_INVOICE":
		return MovementSalesDispatch, DirectionOut
	case "SALES_RETURN", "SALES_RETURN_INVOICE":
		return MovementSalesReturn, DirectionIn
	case "PURCHASE_DELIVERY", "GOODS_RECEIPT", "PURCHASE_INVOICE":
		return MovementPurchaseReceipt, DirectionIn
	case "PURCHASE_RETURN", "PURCHASE_RETURN_INVOICE":
		return MovementPurchaseReturn, DirectionOut
	default:
		return "", ""
	}
}

type SalesReservationLine struct {
	OrderLineID string
	ProductID   string
	VariantID   string
	WarehouseID string
	Quantity    string
}

type SalesReservationInput struct {
	OrderID string
	Lines   []SalesReservationLine
}

// ReserveSalesOrderTx turns available stock into an order-line reservation.
// Rows are locked in deterministic input order by the caller, while the
// stock identity advisory lock in the movement subsystem protects concurrent
// physical posting against the same position.
func (s *Service) ReserveSalesOrderTx(ctx context.Context, tx pgx.Tx, session identity.Session, input SalesReservationInput) error {
	if identity.ValidateExternalActor(session) != nil || !session.HasPermission("inventory.movement.post") {
		return identity.ErrForbidden
	}
	companyID, err := requireUUID("company_id", session.CurrentCompanyID)
	if err != nil {
		return err
	}
	orderID, err := requireUUID("order_id", input.OrderID)
	if err != nil {
		return err
	}
	if len(input.Lines) == 0 {
		return fmt.Errorf("%w: rezervasyon satırı gereklidir", identity.ErrValidation)
	}
	for _, line := range input.Lines {
		lineID, lineErr := requireUUID("order_line_id", line.OrderLineID)
		if lineErr != nil {
			return lineErr
		}
		productID, productErr := requireUUID("product_id", line.ProductID)
		if productErr != nil {
			return productErr
		}
		warehouseID, warehouseErr := requireUUID("warehouse_id", line.WarehouseID)
		if warehouseErr != nil {
			return warehouseErr
		}
		if err = ensureWarehouseAccess(ctx, tx, companyID, session.User.ID, warehouseID); err != nil {
			return err
		}
		quantity := strings.TrimSpace(line.Quantity)
		if decimalCompare(quantity, "0") <= 0 {
			return fmt.Errorf("%w: rezervasyon miktarı geçersiz", identity.ErrValidation)
		}
		// A claim makes repeated confirmation safe even if the command layer is
		// retried after the response was lost.
		claimKey := "sales-order-reservation:" + orderID + ":" + lineID + ":" + warehouseID
		var claimID string
		claimErr := tx.QueryRow(ctx, `INSERT INTO commercial_effect_claims(id,company_id,effect_key,effect_type,document_id,line_id) VALUES($1,$2,$3,'RESERVATION',$4,$5) ON CONFLICT(company_id,effect_key) DO NOTHING RETURNING id`, uuid.NewString(), companyID, claimKey, orderID, lineID).Scan(&claimID)
		if errorsIsNoRows(claimErr) {
			continue
		}
		if claimErr != nil {
			return claimErr
		}
		var positionID, physical, reserved string
		if err = tx.QueryRow(ctx, `SELECT id,physical_quantity::text,reserved_quantity::text FROM stock_positions WHERE company_id=$1 AND warehouse_id=$2 AND product_id=$3 AND variant_id IS NOT DISTINCT FROM NULLIF($4,'')::uuid AND location_id IS NULL AND lot_id IS NULL AND serial_id IS NULL FOR UPDATE`, companyID, warehouseID, productID, strings.TrimSpace(line.VariantID)).Scan(&positionID, &physical, &reserved); errorsIsNoRows(err) {
			return ErrInsufficientStock
		} else if err != nil {
			return err
		}
		if decimalCompare(decimalSub(physical, reserved), quantity) < 0 {
			return ErrInsufficientStock
		}
		if _, err = tx.Exec(ctx, `UPDATE stock_positions SET reserved_quantity=reserved_quantity+$1,updated_at=now() WHERE company_id=$2 AND id=$3`, quantity, companyID, positionID); err != nil {
			return mapInventoryError(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO sales_order_reservations(id,company_id,order_id,order_line_id,warehouse_id,product_id,variant_id,quantity) VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,'')::uuid,$8)`, uuid.NewString(), companyID, orderID, lineID, warehouseID, productID, strings.TrimSpace(line.VariantID), quantity); err != nil {
			return mapInventoryError(err)
		}
	}
	return nil
}

type SalesReservationConsumption struct {
	OrderID     string
	OrderLineID string
	ProductID   string
	VariantID   string
	WarehouseID string
	Quantity    string
}

func (s *Service) ConsumeSalesOrderReservationsTx(ctx context.Context, tx pgx.Tx, session identity.Session, lines []SalesReservationConsumption) error {
	if identity.ValidateExternalActor(session) != nil || !session.HasPermission("inventory.movement.post") {
		return identity.ErrForbidden
	}
	companyID := strings.TrimSpace(session.CurrentCompanyID)
	for _, line := range lines {
		var id, warehouseID, productID, variantID, quantity, consumed, released string
		if err := tx.QueryRow(ctx, `SELECT id,warehouse_id,product_id,COALESCE(variant_id::text,''),quantity::text,consumed_quantity::text,released_quantity::text FROM sales_order_reservations WHERE company_id=$1 AND order_id=$2 AND order_line_id=$3 AND warehouse_id=$4 AND product_id=$5 AND variant_id IS NOT DISTINCT FROM NULLIF($6,'')::uuid AND status='ACTIVE' FOR UPDATE`, companyID, line.OrderID, line.OrderLineID, line.WarehouseID, line.ProductID, strings.TrimSpace(line.VariantID)).Scan(&id, &warehouseID, &productID, &variantID, &quantity, &consumed, &released); errorsIsNoRows(err) {
			return fmt.Errorf("%w: sipariş satırı rezervasyonu bulunamadı", identity.ErrValidation)
		} else if err != nil {
			return err
		}
		amount := strings.TrimSpace(line.Quantity)
		remaining := decimalSub(decimalSub(quantity, consumed), released)
		if decimalCompare(amount, "0") <= 0 || decimalCompare(amount, remaining) > 0 {
			return fmt.Errorf("%w: rezervasyon miktarı aşıldı", identity.ErrValidation)
		}
		if _, err := tx.Exec(ctx, `UPDATE sales_order_reservations SET consumed_quantity=consumed_quantity+$1,status=CASE WHEN consumed_quantity+$1+released_quantity>=quantity THEN 'CONSUMED' ELSE status END,updated_at=now() WHERE company_id=$2 AND id=$3`, amount, companyID, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE stock_positions SET reserved_quantity=GREATEST(0,reserved_quantity-$1),updated_at=now() WHERE company_id=$2 AND warehouse_id=$3 AND product_id=$4 AND variant_id IS NOT DISTINCT FROM NULLIF($5,'')::uuid AND location_id IS NULL AND lot_id IS NULL AND serial_id IS NULL`, amount, companyID, warehouseID, productID, variantID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) ReleaseSalesOrderReservationsTx(ctx context.Context, tx pgx.Tx, session identity.Session, orderID string) error {
	if identity.ValidateExternalActor(session) != nil || !session.HasPermission("inventory.movement.post") {
		return identity.ErrForbidden
	}
	companyID := strings.TrimSpace(session.CurrentCompanyID)
	rows, err := tx.Query(ctx, `SELECT id,warehouse_id,product_id,COALESCE(variant_id::text,''),quantity::text,consumed_quantity::text,released_quantity::text FROM sales_order_reservations WHERE company_id=$1 AND order_id=$2 AND status='ACTIVE' ORDER BY id FOR UPDATE`, companyID, orderID)
	if err != nil {
		return err
	}
	type reservation struct{ id, warehouse, product, variant, quantity, consumed, released string }
	items := make([]reservation, 0)
	for rows.Next() {
		var item reservation
		if err = rows.Scan(&item.id, &item.warehouse, &item.product, &item.variant, &item.quantity, &item.consumed, &item.released); err != nil {
			rows.Close()
			return err
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, item := range items {
		remaining := decimalSub(decimalSub(item.quantity, item.consumed), item.released)
		if decimalCompare(remaining, "0") > 0 {
			if _, err = tx.Exec(ctx, `UPDATE stock_positions SET reserved_quantity=GREATEST(0,reserved_quantity-$1),updated_at=now() WHERE company_id=$2 AND warehouse_id=$3 AND product_id=$4 AND variant_id IS NOT DISTINCT FROM NULLIF($5,'')::uuid AND location_id IS NULL AND lot_id IS NULL AND serial_id IS NULL`, remaining, companyID, item.warehouse, item.product, item.variant); err != nil {
				return err
			}
		}
		if _, err = tx.Exec(ctx, `UPDATE sales_order_reservations SET released_quantity=quantity-consumed_quantity,status='RELEASED',updated_at=now() WHERE company_id=$1 AND id=$2`, companyID, item.id); err != nil {
			return err
		}
	}
	return nil
}

// RestoreSalesOrderReservationsTx is the inverse of ConsumeSalesOrderReservationsTx:
// when a posted dispatch is cancelled and its stock movement reversed, the
// reservation it consumed is put back on hold so the order line can be
// dispatched again. It never restores more than was actually consumed.
func (s *Service) RestoreSalesOrderReservationsTx(ctx context.Context, tx pgx.Tx, session identity.Session, lines []SalesReservationConsumption) error {
	if identity.ValidateExternalActor(session) != nil || !session.HasPermission("inventory.movement.reverse") {
		return identity.ErrForbidden
	}
	companyID := strings.TrimSpace(session.CurrentCompanyID)
	for _, line := range lines {
		var id, warehouseID, productID, variantID, consumed string
		if err := tx.QueryRow(ctx, `SELECT id,warehouse_id,product_id,COALESCE(variant_id::text,''),consumed_quantity::text FROM sales_order_reservations WHERE company_id=$1 AND order_id=$2 AND order_line_id=$3 AND warehouse_id=$4 AND product_id=$5 AND variant_id IS NOT DISTINCT FROM NULLIF($6,'')::uuid AND status IN ('ACTIVE','CONSUMED') FOR UPDATE`, companyID, line.OrderID, line.OrderLineID, line.WarehouseID, line.ProductID, strings.TrimSpace(line.VariantID)).Scan(&id, &warehouseID, &productID, &variantID, &consumed); errorsIsNoRows(err) {
			continue
		} else if err != nil {
			return err
		}
		amount := strings.TrimSpace(line.Quantity)
		if decimalCompare(amount, consumed) > 0 {
			amount = consumed
		}
		if decimalCompare(amount, "0") <= 0 {
			continue
		}
		if _, err := tx.Exec(ctx, `UPDATE sales_order_reservations SET consumed_quantity=consumed_quantity-$1,status=CASE WHEN status='CONSUMED' THEN 'ACTIVE' ELSE status END,updated_at=now() WHERE company_id=$2 AND id=$3`, amount, companyID, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE stock_positions SET reserved_quantity=reserved_quantity+$1,updated_at=now() WHERE company_id=$2 AND warehouse_id=$3 AND product_id=$4 AND variant_id IS NOT DISTINCT FROM NULLIF($5,'')::uuid AND location_id IS NULL AND lot_id IS NULL AND serial_id IS NULL`, amount, companyID, warehouseID, productID, variantID); err != nil {
			return err
		}
	}
	return nil
}

func errorsIsNoRows(err error) bool { return err == pgx.ErrNoRows }
