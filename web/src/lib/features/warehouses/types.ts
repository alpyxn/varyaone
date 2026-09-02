export const warehouseTypes = ['STANDARD', 'TRANSIT', 'QUARANTINE', 'RETURN'] as const;

export type WarehouseType = (typeof warehouseTypes)[number];

/**
 * The API historically called this field `type`, while some projections use
 * `warehouse_type`. Keep both spellings at the UI boundary so operation
 * pickers remain safe during the API transition.
 */
export type Warehouse = {
  id: string;
  company_id?: string;
  branch_id?: string | null;
  branch_name?: string;
  code: string;
  name: string;
  type?: string;
  warehouse_type?: string;
  address?: string;
  uses_locations?: boolean;
  is_transit?: boolean;
  is_system?: boolean;
  is_active?: boolean;
  can_delete?: boolean;
  version?: number;
  created_at?: string;
  updated_at?: string;
};

export type WarehouseInput = {
  code: string;
  name: string;
  type: WarehouseType;
  address: string;
  is_active: boolean;
};

export function warehouseType(warehouse: Pick<Warehouse, 'type' | 'warehouse_type'>) {
  return String(warehouse.type ?? warehouse.warehouse_type ?? 'STANDARD').toUpperCase();
}

export function isActiveWarehouse(warehouse: Pick<Warehouse, 'is_active'>) {
  return warehouse.is_active !== false && String(warehouse.is_active).toLowerCase() !== 'false';
}

export function isStandardWarehouse(warehouse: Pick<Warehouse, 'type' | 'warehouse_type'>) {
  return warehouseType(warehouse) === 'STANDARD';
}

export function isActiveStandardWarehouse(warehouse: Warehouse) {
  return isActiveWarehouse(warehouse) && isStandardWarehouse(warehouse);
}

export function isActiveDestinationWarehouse(warehouse: Warehouse) {
  // Transit is an internal transfer step. It must never be offered as a
  // manual inbound or transfer destination, even though its row is active.
  return (
    isActiveWarehouse(warehouse) &&
    warehouseType(warehouse) !== 'TRANSIT' &&
    warehouse.is_system !== true
  );
}

export function warehouseTypeLabel(warehouse: Pick<Warehouse, 'type' | 'warehouse_type'>) {
  switch (warehouseType(warehouse)) {
    case 'TRANSIT':
      return 'Transit';
    case 'QUARANTINE':
      return 'Karantina';
    case 'RETURN':
      return 'İade';
    default:
      return 'Standart';
  }
}

export function warehouseOptionLabel(warehouse: Warehouse) {
  const code = warehouse.code ? `${warehouse.code} · ` : '';
  const specialType = isStandardWarehouse(warehouse) ? '' : ` · ${warehouseTypeLabel(warehouse)}`;
  return `${code}${warehouse.name}${specialType}`;
}

export function warehouseUpdateInput(
  warehouse: Warehouse,
  isActive = isActiveWarehouse(warehouse)
) {
  const type = warehouseType(warehouse);
  return {
    code: warehouse.code.trim(),
    name: warehouse.name.trim(),
    type: warehouseTypes.includes(type as WarehouseType) ? (type as WarehouseType) : 'STANDARD',
    address: (warehouse.address ?? '').trim(),
    is_active: isActive
  } satisfies WarehouseInput;
}
