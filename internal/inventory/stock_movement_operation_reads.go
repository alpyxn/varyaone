package inventory

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/jackc/pgx/v5"
)

func scanStockMovementOperationHeader(row interface{ Scan(...any) error }) (StockMovementOperation, error) {
	var item StockMovementOperation
	var currency, actor *string
	if err := row.Scan(
		&item.ID, &item.CompanyID, &item.WarehouseID, &item.WarehouseCode, &item.WarehouseName,
		&item.ProductID, &item.ProductCode, &item.ProductName, &item.MovementType, &item.Direction,
		&item.UnitCode, &item.StockUnit, &currency, &item.ReasonCode, &item.ReasonDescription, &item.IdempotencyKey,
		&actor, &item.ActorName, &item.PostedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return StockMovementOperation{}, ErrNotFound
		}
		return StockMovementOperation{}, err
	}
	item.Currency = valueOrEmpty(currency)
	item.ActorUserID = actor
	return item, nil
}

// ListStockMovementOperations lists manual multi-variant operations inside
// the caller's company and warehouse scope. Product and warehouse names are
// joined here while variant labels come from immutable operation-line snapshots.
func (s *Service) ListStockMovementOperations(ctx context.Context, filter MovementListFilter) ([]StockMovementOperation, error) {
	filter, err := normalizeMovementListFilter(filter)
	if err != nil {
		return nil, err
	}

	args := []any{filter.CompanyID}
	query := `SELECT o.id,o.company_id,o.warehouse_id,COALESCE(w.code,''),COALESCE(w.name,''),
		o.product_id,COALESCE(p.code,''),COALESCE(p.name,''),o.movement_type,o.direction,
		COALESCE(NULLIF(o.unit_code,''),(SELECT pu.unit_code FROM product_units pu WHERE pu.company_id=o.company_id AND pu.product_id=o.product_id AND pu.is_base LIMIT 1),''),
		COALESCE((SELECT pu.unit_code FROM product_units pu WHERE pu.company_id=o.company_id AND pu.product_id=o.product_id AND pu.is_base LIMIT 1),''),o.currency,o.reason_code,o.reason_description,o.idempotency_key,
		o.actor_user_id,COALESCE(au.display_name,au.email,''),o.posted_at
		FROM stock_movement_operations o
		JOIN warehouses w ON w.company_id=o.company_id AND w.id=o.warehouse_id
		JOIN products p ON p.company_id=o.company_id AND p.id=o.product_id
		LEFT JOIN users au ON au.id=o.actor_user_id
		WHERE o.company_id=$1 AND NOT w.is_system`

	if filter.WarehouseID != "" {
		if err = ensureVisibleWarehouse(ctx, s.pool, filter.CompanyID, filter.UserID, filter.WarehouseID); err != nil {
			return nil, err
		}
		args = append(args, filter.WarehouseID)
		query += fmt.Sprintf(" AND o.warehouse_id=$%d", len(args))
	} else if filter.UserID != "" {
		allowedWarehouses, scopeErr := visibleOperationWarehouses(ctx, s, filter.CompanyID, filter.UserID)
		if scopeErr != nil {
			return nil, scopeErr
		}
		if len(allowedWarehouses) == 0 {
			return []StockMovementOperation{}, nil
		}
		args = append(args, allowedWarehouses)
		query += fmt.Sprintf(" AND o.warehouse_id=ANY($%d::uuid[])", len(args))
	}
	if filter.ProductID != "" {
		args = append(args, filter.ProductID)
		query += fmt.Sprintf(" AND o.product_id=$%d", len(args))
	}
	if filter.Direction != "" {
		args = append(args, filter.Direction)
		query += fmt.Sprintf(" AND o.direction=$%d", len(args))
	}
	for _, token := range strings.Fields(strings.ToLower(filter.Query)) {
		args = append(args, "%"+token+"%")
		param := len(args)
		query += fmt.Sprintf(` AND (
			lower(o.id::text) LIKE $%d OR lower(o.movement_type) LIKE $%d
			OR lower(o.direction) LIKE $%d OR lower(o.reason_code) LIKE $%d
			OR lower(o.reason_description) LIKE $%d OR lower(COALESCE(p.code,'')) LIKE $%d
			OR lower(COALESCE(p.name,'')) LIKE $%d OR lower(COALESCE(w.code,'')) LIKE $%d
			OR lower(COALESCE(w.name,'')) LIKE $%d
			OR EXISTS (SELECT 1 FROM stock_movement_operation_lines ol
				WHERE ol.company_id=o.company_id AND ol.operation_id=o.id
				AND (lower(ol.variant_code) LIKE $%d OR lower(ol.variant_display::text) LIKE $%d))
		)`, param, param, param, param, param, param, param, param, param, param, param)
	}
	if filter.PostedAtFrom != nil {
		args = append(args, *filter.PostedAtFrom)
		query += fmt.Sprintf(" AND o.posted_at >= $%d", len(args))
	}
	if filter.PostedAtTo != nil {
		args = append(args, *filter.PostedAtTo)
		query += fmt.Sprintf(" AND o.posted_at <= $%d", len(args))
	}
	args = append(args, filter.Limit)
	query += fmt.Sprintf(" ORDER BY o.posted_at DESC,o.id DESC LIMIT $%d", len(args))

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]StockMovementOperation, 0, filter.Limit)
	operationIDs := make([]string, 0, filter.Limit)
	for rows.Next() {
		item, scanErr := scanStockMovementOperationHeader(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, item)
		operationIDs = append(operationIDs, item.ID)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if err = loadStockMovementOperationLines(ctx, s.pool, result, operationIDs); err != nil {
		return nil, err
	}
	return result, nil
}

// GetStockMovementOperation returns one operation after checking its
// warehouse against the caller's visible warehouse scope.
func (s *Service) GetStockMovementOperation(ctx context.Context, companyID, operationID string, userIDs ...string) (StockMovementOperation, error) {
	companyID, err := requireUUID("company_id", companyID)
	if err != nil {
		return StockMovementOperation{}, err
	}
	operationID, err = requireUUID("operation_id", operationID)
	if err != nil {
		return StockMovementOperation{}, err
	}
	var item StockMovementOperation
	var warehouseID string
	var actor *string
	var currency *string
	err = s.pool.QueryRow(ctx, `SELECT o.id,o.company_id,o.warehouse_id,COALESCE(w.code,''),COALESCE(w.name,''),
		o.product_id,COALESCE(p.code,''),COALESCE(p.name,''),o.movement_type,o.direction,COALESCE(NULLIF(o.unit_code,''),(SELECT pu.unit_code FROM product_units pu WHERE pu.company_id=o.company_id AND pu.product_id=o.product_id AND pu.is_base LIMIT 1),''),COALESCE((SELECT pu.unit_code FROM product_units pu WHERE pu.company_id=o.company_id AND pu.product_id=o.product_id AND pu.is_base LIMIT 1),''),o.currency,
		o.reason_code,o.reason_description,o.idempotency_key,o.actor_user_id,COALESCE(au.display_name,au.email,''),o.posted_at
		FROM stock_movement_operations o
		JOIN warehouses w ON w.company_id=o.company_id AND w.id=o.warehouse_id
		JOIN products p ON p.company_id=o.company_id AND p.id=o.product_id
		LEFT JOIN users au ON au.id=o.actor_user_id
		WHERE o.company_id=$1 AND o.id=$2 AND NOT w.is_system`, companyID, operationID).Scan(
		&item.ID, &item.CompanyID, &warehouseID, &item.WarehouseCode, &item.WarehouseName,
		&item.ProductID, &item.ProductCode, &item.ProductName, &item.MovementType, &item.Direction,
		&item.UnitCode, &item.StockUnit, &currency, &item.ReasonCode, &item.ReasonDescription, &item.IdempotencyKey,
		&actor, &item.ActorName, &item.PostedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return StockMovementOperation{}, ErrNotFound
		}
		return StockMovementOperation{}, err
	}
	item.WarehouseID = warehouseID
	item.Currency = valueOrEmpty(currency)
	item.ActorUserID = actor
	if err = ensureVisibleWarehouse(ctx, s.pool, companyID, optionalActor(userIDs), warehouseID); err != nil {
		return StockMovementOperation{}, err
	}
	operations := []StockMovementOperation{item}
	if err = loadStockMovementOperationLines(ctx, s.pool, operations, []string{item.ID}); err != nil {
		return StockMovementOperation{}, err
	}
	return operations[0], nil
}

func visibleOperationWarehouses(ctx context.Context, s *Service, companyID, userID string) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT id FROM warehouses WHERE company_id=$1 AND NOT is_system ORDER BY id`, companyID)
	if err != nil {
		return nil, err
	}
	candidates := make([]string, 0)
	for rows.Next() {
		var warehouseID string
		if err = rows.Scan(&warehouseID); err != nil {
			rows.Close()
			return nil, err
		}
		candidates = append(candidates, warehouseID)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	// ensureWarehouseScope issues its own query; run it only after the warehouse
	// rows are drained, otherwise the request-pinned connection fails with
	// "conn busy".
	allowed := make([]string, 0, len(candidates))
	for _, warehouseID := range candidates {
		if scopeErr := ensureWarehouseScope(ctx, s.pool, companyID, userID, warehouseID); scopeErr != nil {
			if errors.Is(scopeErr, identity.ErrForbidden) {
				continue
			}
			return nil, scopeErr
		}
		allowed = append(allowed, warehouseID)
	}
	return allowed, nil
}

func loadStockMovementOperationLines(ctx context.Context, db txDB, operations []StockMovementOperation, operationIDs []string) error {
	if len(operationIDs) == 0 {
		return nil
	}
	byID := make(map[string]int, len(operations))
	for index := range operations {
		byID[operations[index].ID] = index
	}
	rows, err := db.Query(ctx, `SELECT operation_id,id,line_no,movement_id,variant_id,variant_code,variant_display,
		quantity::text,base_quantity::text,unit_cost::text
		FROM stock_movement_operation_lines
		WHERE company_id=$1 AND operation_id=ANY($2::uuid[])
		ORDER BY operation_id,line_no`, operations[0].CompanyID, operationIDs)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var operationID string
		var line StockMovementOperationResult
		var display []byte
		var unitCost *string
		if err = rows.Scan(&operationID, &line.ID, &line.LineNo, &line.MovementID, &line.VariantID,
			&line.VariantCode, &display, &line.Quantity, &line.BaseQuantity, &unitCost); err != nil {
			return err
		}
		index, ok := byID[operationID]
		if !ok {
			continue
		}
		line.VariantDisplay = decodeVariantDisplay(display)
		line.UnitCost = unitCost
		if operations[index].Currency != "" {
			currency := operations[index].Currency
			line.Currency = &currency
		}
		operations[index].Lines = append(operations[index].Lines, line)
	}
	return rows.Err()
}
