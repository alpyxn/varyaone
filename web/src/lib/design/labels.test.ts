import { describe, expect, it } from 'vitest';
import { localizedEnum, localizedPermission, localizedStatus } from './labels';

describe('localizedEnum', () => {
  it('translates stock movement codes', () => {
    expect(localizedEnum('MANUAL_ADJUSTMENT', 'movement_type')).toBe('Manuel stok düzeltmesi');
    expect(localizedEnum('TRANSFER_IN', ['direction', 'movement_type'])).toBe(
      'Depo transfer girişi'
    );
  });

  it('keeps unknown values intact', () => {
    expect(localizedEnum('CUSTOM_VALUE', 'movement_type')).toBe('CUSTOM_VALUE');
    expect(localizedEnum('CUSTOM_VALUE', 'status')).toBe('Bilinmeyen durum');
  });

  it('normalizes status and permission codes for user-facing labels', () => {
    expect(localizedStatus(' in_transit ')).toBe('Sevk sırasında');
    expect(localizedEnum('cancelled', 'document.status')).toBe('İptal');
    expect(localizedPermission('inventory.transfer.receive')).toBe('Depo transferi teslim alma');
    expect(localizedPermission('future.permission')).toBe('future.permission');
  });
});
