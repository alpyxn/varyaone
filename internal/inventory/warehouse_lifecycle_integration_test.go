package inventory

import (
	"errors"
	"strings"
	"testing"
)

const warehouseLifecycleTestActor = "10000000-0000-4000-8000-000000000099"

func TestWarehouseDeletionStatusCoversWarehouseDependencies(t *testing.T) {
	expression := warehouseDeletionStatusExpression("w")
	if strings.Contains(expression, "%!") {
		t.Fatalf("warehouse deletion status expression formatting failed: %s", expression)
	}
	for _, fragment := range []string{
		"w.is_system",
		"stock_movements sm",
		"stock_positions sp",
		"warehouse_transfers wt",
		"locations l",
		"stock_counts sc",
		"stock_cost_layers scl",
		"serial_numbers sn",
		"serial_number_events sne",
		"documents d",
		"membership_warehouse_scopes mws",
		"api_token_warehouse_scopes atws",
		"warehouse_transfer_reservations wtr",
		"stock_movement_operations smo",
		"stock_count_engine_counts sce",
		"stock_count_engine_scopes scs",
		"purchase_orders po",
		"goods_receipts gr",
		"purchase_returns pr",
		"'HISTORY'",
		"'DEPENDENCY'",
		"'DELETABLE'",
	} {
		if !strings.Contains(expression, fragment) {
			t.Fatalf("warehouse deletion status expression is missing %q", fragment)
		}
	}
}

func TestWarehouseLifecycleHidesUsedDeleteAndExcludesInactiveStock(t *testing.T) {
	fixture := newTransferStockFixture(t, "10", "0")
	warehouse, err := fixture.service.GetWarehouse(fixture.ctx, transferTestCompany, transferTestSource, warehouseLifecycleTestActor)
	if err != nil {
		t.Fatal(err)
	}
	if warehouse.CanDelete {
		t.Fatal("warehouse with a stock position is reported as deletable")
	}

	updated, err := fixture.service.UpdateWarehouse(fixture.ctx, transferTestCompany, transferTestSource, warehouse.Version, WarehouseUpdateInput{
		Code:     warehouse.Code,
		Name:     warehouse.Name,
		Type:     warehouse.Type,
		Address:  warehouse.Address,
		IsActive: boolPointer(false),
	}, warehouseLifecycleTestActor)
	if err != nil {
		t.Fatal(err)
	}
	if updated.IsActive {
		t.Fatal("warehouse deactivation did not persist")
	}

	position, err := fixture.service.GetPosition(fixture.ctx, transferTestCompany, transferTestSource, transferTestProduct, "", "", "", "", warehouseLifecycleTestActor)
	if err != nil {
		t.Fatal(err)
	}
	assertQuantity(t, position.PhysicalQuantity, "0", "inactive warehouse physical quantity")
	assertQuantity(t, position.AvailableQuantity, "0", "inactive warehouse available quantity")

	if err = fixture.service.DeleteWarehouse(fixture.ctx, transferTestCompany, transferTestSource, updated.Version, warehouseLifecycleTestActor); !errors.Is(err, ErrWarehouseHasHistory) {
		t.Fatalf("used warehouse delete error=%v, want %v", err, ErrWarehouseHasHistory)
	}
}

func TestUnusedWarehouseCanBeDeleted(t *testing.T) {
	fixture := newTransferStockFixture(t, "0", "0")
	warehouse, err := fixture.service.CreateWarehouse(fixture.ctx, WarehouseInput{
		CompanyID:   transferTestCompany,
		Code:        "BOS",
		Name:        "Boş Depo",
		Type:        WarehouseStandard,
		ActorUserID: warehouseLifecycleTestActor,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !warehouse.CanDelete {
		t.Fatal("unused warehouse is not reported as deletable")
	}

	items, err := fixture.service.ListWarehouses(fixture.ctx, transferTestCompany, true, warehouseLifecycleTestActor)
	if err != nil {
		t.Fatal(err)
	}
	var listedCanDelete *bool
	for _, item := range items {
		if item.ID == warehouse.ID {
			value := item.CanDelete
			listedCanDelete = &value
			break
		}
	}
	if listedCanDelete == nil || !*listedCanDelete {
		t.Fatal("warehouse list did not preserve can_delete=true for an unused warehouse")
	}

	if err = fixture.service.DeleteWarehouse(fixture.ctx, transferTestCompany, warehouse.ID, warehouse.Version, warehouseLifecycleTestActor); err != nil {
		t.Fatal(err)
	}
}

func boolPointer(value bool) *bool {
	return &value
}
