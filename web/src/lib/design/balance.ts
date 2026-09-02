/**
 * Plain-language cari (account) balance phrasing for users who do not read
 * accounting. The ledger sign convention is: positive = the party owes us
 * (borç / receivable), negative = we owe the party (alacak / payable).
 *
 * Callers never show the raw minus sign — always the absolute amount plus a
 * "borç" / "alacak" label and a one-line explanation of who owes whom.
 */
import { formatMoney } from './formatters';

export type BalanceTone = 'debit' | 'credit' | 'zero';

export type BalanceDescription = {
  tone: BalanceTone;
  /** Absolute amount + currency, e.g. "2.456,00 ₺". */
  amount: string;
  /** "borç" | "alacak" | "" (zero). */
  label: string;
  /** "Bu cari size borçlu." | "Bu cariye borcunuz var." | "Hesap kapalı." */
  meaning: string;
  /** Amount + label, e.g. "2.456,00 ₺ borç". */
  headline: string;
};

function stripSign(value: string): string {
  const trimmed = value.trim();
  return trimmed.startsWith('-') ? trimmed.slice(1) : trimmed.replace(/^\+/, '');
}

function sign(value: string | number): number {
  const text = String(value ?? '').trim();
  if (!text || text === '-' || /^-?0*(?:[.,]0+)?$/.test(text)) return 0;
  return text.startsWith('-') ? -1 : 1;
}

export function describeBalance(value: string | number, currency = 'TRY'): BalanceDescription {
  const direction = sign(value);
  const amount = formatMoney(stripSign(String(value ?? '0')) || '0', currency);
  if (direction > 0) {
    return {
      tone: 'debit',
      amount,
      label: 'borç',
      meaning: 'Bu cari size borçlu.',
      headline: `${amount} borç`
    };
  }
  if (direction < 0) {
    return {
      tone: 'credit',
      amount,
      label: 'alacak',
      meaning: 'Bu cariye borcunuz var.',
      headline: `${amount} alacak`
    };
  }
  return {
    tone: 'zero',
    amount,
    label: '',
    meaning: 'Hesap kapalı.',
    headline: amount
  };
}
