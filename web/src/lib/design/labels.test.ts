import { describe, expect, it } from 'vitest';
import { localizedEnum, localizedPermission, localizedStatus } from './labels';

describe('localizedEnum', () => {
  it('translates stock movement codes', () => {
    expect(localizedEnum('MANUAL_ADJUSTMENT', 'movement_type')).toBe('Manuel stok düzeltmesi');
    expect(localizedEnum('TRANSFER_IN', ['direction', 'movement_type'])).toBe(
      'Depo transfer girişi'
    );
  });

  it('translates shared operation table codes', () => {
    expect(localizedEnum('finance_party_transfer', 'source_type')).toBe('Cari virman');
    expect(localizedEnum('WORKFLOW', 'transfer_type')).toBe('Onaylı transfer');
    expect(localizedEnum('SALES_INVOICE', 'document_type_code')).toBe('Satış faturası');
  });

  it('translates every account movement kind and source shown to users', () => {
    expect(localizedEnum('EMPLOYEE_ADVANCE', 'movement_kind')).toBe('Personel avansı');
    expect(localizedEnum('ADJUSTMENT', 'movement_kind')).toBe('Bakiye düzeltmesi');
    expect(localizedEnum('OTHER', 'movement_kind')).toBe('Diğer hesap hareketi');
    expect(localizedEnum('employee_advance_transaction', 'source_type')).toBe('Personel avansı');
    expect(localizedEnum('finance_account_movement', 'source_type')).toBe('Manuel hesap hareketi');
    expect(localizedEnum('finance_transfer', 'source_type')).toBe('Hesap transferi');
    expect(localizedEnum('finance_payment', 'source_type')).toBe('Tahsilat / ödeme');
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
