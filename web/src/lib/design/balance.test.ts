import { describe, expect, it } from 'vitest';
import { describeBalance } from './balance';

describe('describeBalance', () => {
  it('positive balance means the party owes us', () => {
    const d = describeBalance('2456', 'TRY');
    expect(d.tone).toBe('debit');
    expect(d.headline).toBe('2.456,00 ₺ borç');
    expect(d.meaning).toBe('Bu cari size borçlu.');
  });

  it('negative balance means we owe the party, with no raw minus sign', () => {
    const d = describeBalance('-1624', 'TRY');
    expect(d.tone).toBe('credit');
    expect(d.headline).toBe('1.624,00 ₺ alacak');
    expect(d.headline).not.toContain('-');
    expect(d.meaning).toBe('Bu cariye borcunuz var.');
  });

  it('zero balance means the account is settled', () => {
    const d = describeBalance('0', 'TRY');
    expect(d.tone).toBe('zero');
    expect(d.label).toBe('');
    expect(d.meaning).toBe('Hesap kapalı.');
  });

  it('keeps the currency symbol', () => {
    expect(describeBalance('-9.1534', 'GBP').headline).toBe('9,15 £ alacak');
  });

  it('normalizes negative zero and preserves very large exact amounts', () => {
    expect(describeBalance('-0.00', 'TRY')).toMatchObject({
      tone: 'zero',
      headline: '0,00 ₺',
      meaning: 'Hesap kapalı.'
    });
    expect(describeBalance('9007199254740993.25', 'USD').headline).toBe(
      '9.007.199.254.740.993,25 $ borç'
    );
  });
});
