function exactDecimal(
  value: string | number,
  minimumFractionDigits: number,
  maximumFractionDigits: number
) {
  const match = /^(-?)(\d+)(?:\.(\d+))?$/.exec(String(value).trim());
  if (!match) return '—';
  const integerDigits = match[2].replace(/^0+/, '') || '0';
  const integer = integerDigits.replace(/\B(?=(\d{3})+(?!\d))/g, '.');
  let fraction = (match[3] ?? '').slice(0, maximumFractionDigits).replace(/0+$/, '');
  const hasDisplayedMagnitude = integerDigits !== '0' || fraction !== '';
  if (fraction.length < minimumFractionDigits)
    fraction = fraction.padEnd(minimumFractionDigits, '0');
  return `${match[1] === '-' && hasDisplayedMagnitude ? '-' : ''}${integer}${fraction ? `,${fraction}` : ''}`;
}

function decimalMagnitude(value: unknown) {
  const match = /^(-?)(\d+)(?:\.(\d+))?$/.exec(String(value ?? '').trim());
  if (!match) return undefined;
  const integer = match[2].replace(/^0+/, '') || '0';
  const fraction = (match[3] ?? '').replace(/0+$/, '');
  return {
    negative: match[1] === '-' && (integer !== '0' || fraction !== ''),
    zero: integer === '0' && fraction === ''
  };
}

export function isNegativeDecimal(value: unknown) {
  return decimalMagnitude(value)?.negative ?? false;
}

export function isNonPositiveDecimal(value: unknown) {
  const parsed = decimalMagnitude(value);
  return parsed ? parsed.negative || parsed.zero : false;
}

export function formatMoney(value: string | number, currency = 'TRY') {
  // Money can carry more than two meaningful decimal places in the API
  // (for example, unit prices). Keep those digits while hiding scale padding.
  const amount = exactDecimal(value, 2, 8);
  if (amount === '—') return amount;
  const code = String(currency).trim().toUpperCase() || 'TRY';
  const symbols: Record<string, string> = { TRY: '₺', USD: '$', EUR: '€', GBP: '£' };
  return `${amount} ${symbols[code] ?? code}`;
}

export function formatQuantity(value: string | number, fractionDigits = 8) {
  return exactDecimal(value, 0, fractionDigits);
}

export function formatQuantityWithUnit(
  value: string | number,
  unit?: string | null,
  fractionDigits = 8
) {
  const raw = String(value).trim();
  const embedded = /^([+-]?(?:\d+(?:[.,]\d+)?|[.,]\d+))\s+(.+)$/.exec(raw);
  const quantity = embedded ? canonicalDecimal(embedded[1]) : canonicalDecimal(raw);
  const formatted = formatQuantity(quantity, fractionDigits);
  if (formatted === '—') return formatted;
  const displayedUnit = String(unit ?? '').trim() || embedded?.[2]?.trim() || 'ADET';
  return `${formatted} ${displayedUnit.toUpperCase()}`;
}

export function formatDate(value?: string | Date, includeTime = false) {
  if (!value) return '—';
  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.valueOf())) return '—';
  return new Intl.DateTimeFormat(
    'tr-TR',
    includeTime ? { dateStyle: 'short', timeStyle: 'short' } : { dateStyle: 'short' }
  ).format(date);
}

export function formatRelative(value?: string | Date) {
  if (!value) return '—';
  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.valueOf())) return '—';
  const diffMs = date.valueOf() - Date.now();
  const abs = Math.abs(diffMs);
  const minute = 60_000;
  const hour = 60 * minute;
  const day = 24 * hour;
  if (abs < minute) return 'az önce';
  const rtf = new Intl.RelativeTimeFormat('tr-TR', { numeric: 'auto' });
  if (abs < hour) return rtf.format(Math.round(diffMs / minute), 'minute');
  if (abs < day) return rtf.format(Math.round(diffMs / hour), 'hour');
  if (abs < 7 * day) return rtf.format(Math.round(diffMs / day), 'day');
  return formatDate(date, true);
}

export function formatCurrency(currency: string) {
  try {
    return new Intl.DisplayNames(['tr'], { type: 'currency' }).of(currency) ?? currency;
  } catch {
    return currency;
  }
}
import { canonicalDecimal } from './decimal';
