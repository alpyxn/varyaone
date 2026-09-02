import { describe, expect, it } from 'vitest';
import {
  isActiveDestinationWarehouse,
  isActiveStandardWarehouse,
  warehouseOptionLabel,
  warehouseTypeLabel
} from './types';

describe('warehouse visibility rules', () => {
  it('keeps only active standard warehouses for stock-out sources', () => {
    expect(
      isActiveStandardWarehouse({ id: 'standard', code: 'STD', name: 'Ana Depo', type: 'STANDARD' })
    ).toBe(true);
    expect(
      isActiveStandardWarehouse({
        id: 'quarantine',
        code: 'KRT',
        name: 'Karantina',
        type: 'QUARANTINE'
      })
    ).toBe(false);
    expect(
      isActiveStandardWarehouse({
        id: 'inactive',
        code: 'PAS',
        name: 'Pasif Depo',
        type: 'STANDARD',
        is_active: false
      })
    ).toBe(false);
  });

  it('allows active special warehouses as transfer destinations', () => {
    expect(
      isActiveDestinationWarehouse({
        id: 'return',
        code: 'IAD',
        name: 'İade Deposu',
        warehouse_type: 'RETURN'
      })
    ).toBe(true);
    expect(
      isActiveDestinationWarehouse({
        id: 'inactive',
        code: 'PAS',
        name: 'Pasif Depo',
        type: 'STANDARD',
        is_active: false
      })
    ).toBe(false);
    expect(
      isActiveDestinationWarehouse({
        id: 'transit',
        code: 'TRN',
        name: 'Sistem Transit',
        type: 'TRANSIT',
        is_active: true,
        is_system: true
      })
    ).toBe(false);
  });

  it('labels special warehouse types in destination selectors', () => {
    expect(warehouseTypeLabel({ type: 'QUARANTINE' })).toBe('Karantina');
    expect(
      warehouseOptionLabel({ id: 'return', code: 'IAD', name: 'İade Deposu', type: 'RETURN' })
    ).toBe('IAD · İade Deposu · İade');
  });
});
