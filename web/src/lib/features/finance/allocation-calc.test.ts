import { describe, expect, it } from 'vitest';
import { addDecimalStrings } from '$lib/design/decimal';
import {
  allocatedTotal,
  daysOverdue,
  isOverApplied,
  previewFifo,
  unappliedAmount
} from './allocation-calc';

describe('allocation-calc', () => {
  it('sums applied amounts and reports the advance remainder', () => {
    const rows = [{ applied: '100' }, { applied: '80,5' }, { applied: '' }];
    expect(allocatedTotal(rows)).toBe('180.5');
    expect(unappliedAmount('250', rows)).toBe('69.5');
  });

  it('clamps unapplied at zero and flags over-application', () => {
    expect(unappliedAmount('100', [{ applied: '150' }])).toBe('0');
    expect(isOverApplied('100', [{ applied: '150' }])).toBe(true);
    expect(isOverApplied('100', [{ applied: '80' }])).toBe(false);
  });

  it('distributes FIFO oldest due date first and leaves surplus unallocated', () => {
    const items = [
      { id: 'b', open_amount: '80', due_date: '2026-07-01' },
      { id: 'a', open_amount: '100', due_date: '2026-06-01' },
      { id: 'c', open_amount: '40' }
    ];
    expect(previewFifo(items, '250')).toEqual({ a: '100.0000', b: '80.0000', c: '40.0000' });
    expect(previewFifo(items, '150')).toEqual({ a: '100.0000', b: '50.0000' });
  });

  it('distributes without floating-point drift', () => {
    // 0.1 + 0.2 !== 0.3 in IEEE-754: the old float loop left a phantom
    // remainder on the last invoice (or overshot the payment by a kurus).
    const items = [
      { id: 'a', open_amount: '0.1', due_date: '2026-06-01' },
      { id: 'b', open_amount: '0.2', due_date: '2026-06-02' },
      { id: 'c', open_amount: '1', due_date: '2026-06-03' }
    ];
    expect(previewFifo(items, '0.3')).toEqual({ a: '0.1000', b: '0.2000' });

    const thirds = [
      { id: 'a', open_amount: '33.3333', due_date: '2026-06-01' },
      { id: 'b', open_amount: '33.3333', due_date: '2026-06-02' },
      { id: 'c', open_amount: '33.3334', due_date: '2026-06-03' }
    ];
    const spread = previewFifo(thirds, '100');
    expect(spread).toEqual({ a: '33.3333', b: '33.3333', c: '33.3334' });
    expect(Object.values(spread).reduce((sum, value) => addDecimalStrings(sum, value), '0')).toBe(
      '100'
    );
    expect(
      isOverApplied(
        '100',
        Object.values(spread).map((applied) => ({ applied }))
      )
    ).toBe(false);
  });

  it('places undated items after dated ones', () => {
    const items = [
      { id: 'undated', open_amount: '100' },
      { id: 'dated', open_amount: '100', due_date: '2026-06-01' }
    ];
    expect(previewFifo(items, '100')).toEqual({ dated: '100.0000' });
  });

  it('computes overdue days', () => {
    const now = new Date('2026-08-30T00:00:00Z');
    expect(daysOverdue('2026-08-20', now)).toBe(10);
    expect(daysOverdue('2026-09-10', now)).toBeLessThan(0);
    expect(daysOverdue(undefined, now)).toBe(0);
  });
});
