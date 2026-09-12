// @vitest-environment node
import { describe, expect, it } from 'vitest';
import { equalInstallments, partialPlanAllocations, planUnits } from './payment-plans';

describe('vade ve taksit hesapları', () => {
  it('ay sonunu korur, kuruş farkını son taksite verir', () => {
    const rows = equalInstallments('100.00', 3, '2028-01-31');
    expect(rows).toEqual([
      { due_date: '2028-01-31', amount: '33.33' },
      { due_date: '2028-02-29', amount: '33.33' },
      { due_date: '2028-03-31', amount: '33.34' }
    ]);
    expect(rows.reduce((sum, row) => sum + planUnits(row.amount), 0n)).toBe(1000000n);
  });
  it('büyük ve dört ondalıklı tutarda hassasiyet kaybetmez', () => {
    const total = '900719925474099.1234';
    const rows = equalInstallments(total, 7, '2026-12-30');
    expect(rows.reduce((sum, row) => sum + planUnits(row.amount), 0n)).toBe(planUnits(total));
  });
  it('kısmi ödemeyi kaynak faturalara dağıtır ve fazla ödemeyi reddeder', () => {
    const rows = [
      { open_item_id: 'a', amount: '10' },
      { open_item_id: 'b', amount: '20' }
    ];
    expect(partialPlanAllocations(rows, '15')).toEqual([
      { open_item_id: 'a', amount: '10.0000' },
      { open_item_id: 'b', amount: '5.0000' }
    ]);
    expect(() => partialPlanAllocations(rows, '31')).toThrow();
  });
  it('geçersiz tarih ve sıfır tutarı reddeder', () => {
    expect(() => equalInstallments('5', 2, '2026-02-30')).toThrow();
    expect(() => equalInstallments('0', 2, '2026-01-01')).toThrow();
  });
});
