import { describe, expect, it } from 'vitest';
import { assetStatusLabel, editableAssetStatuses, toAssetInput } from './types';

describe('fixed-asset types', () => {
  it('labels every status in Turkish', () => {
    expect(assetStatusLabel('AVAILABLE')).toBe('Uygun');
    expect(assetStatusLabel('ASSIGNED')).toBe('Zimmetli');
    expect(assetStatusLabel('MAINTENANCE')).toBe('Bakımda');
    expect(assetStatusLabel('RETIRED')).toBe('Hurdaya ayrıldı');
  });

  it('never offers ASSIGNED as a directly editable status', () => {
    expect(editableAssetStatuses).not.toContain('ASSIGNED');
  });

  it('trims free-text fields when building an input', () => {
    const input = toAssetInput({
      asset_code: '  A1 ',
      name: ' Laptop ',
      category: ' BT ',
      serial_number: ' SN1 ',
      description: '  ',
      status: 'AVAILABLE'
    });
    expect(input).toEqual({
      asset_code: 'A1',
      name: 'Laptop',
      category: 'BT',
      serial_number: 'SN1',
      description: '',
      status: 'AVAILABLE'
    });
  });
});
