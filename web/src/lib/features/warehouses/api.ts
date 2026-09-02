import { api } from '$lib/api';
import { warehouseUpdateInput, type Warehouse, type WarehouseInput } from './types';

type WarehouseList = { items?: Warehouse[] };

export const listWarehouses = (
  options: { includeInactive?: boolean; signal?: AbortSignal } = {}
) => {
  const params = new URLSearchParams();
  if (options.includeInactive) params.set('include_inactive', 'true');
  const query = params.toString();
  return api<WarehouseList>(`/warehouses${query ? `?${query}` : ''}`, {
    signal: options.signal
  });
};

export const updateWarehouse = (id: string, version: number, input: WarehouseInput) =>
  api<Warehouse>(`/warehouses/${encodeURIComponent(id)}`, {
    method: 'PUT',
    headers: { 'If-Match': `"${version}"` },
    body: JSON.stringify(input)
  });

export const setWarehouseActive = (warehouse: Warehouse, version: number, isActive: boolean) =>
  updateWarehouse(warehouse.id, version, warehouseUpdateInput(warehouse, isActive));

export const deleteWarehouse = (id: string, version: number) =>
  api<void>(`/warehouses/${encodeURIComponent(id)}`, {
    method: 'DELETE',
    headers: { 'If-Match': `"${version}"` }
  });
