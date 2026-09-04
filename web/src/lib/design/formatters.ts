/** Round a digit-string magnitude half-up to `scale` fraction digits. Cutting
 *  the extra digits off instead would show 1,999999 as 1,99. */
function roundMagnitude(integer: string, fraction: string, scale: number) {
  if (fraction.length <= scale) return { integer, fraction };
  const kept = BigInt(`${integer}${fraction.slice(0, scale)}`);
  const rounded = fraction.charCodeAt(scale) - 48 >= 5 ? kept + 1n : kept;
  const digits = rounded.toString().padStart(scale + 1, '0');
  return {
    integer: scale === 0 ? digits : digits.slice(0, -scale),
    fraction: scale === 0 ? '' : digits.slice(-scale)
  };
}

function exactDecimal(
  value: string | number,
  minimumFractionDigits: number,
  maximumFractionDigits: number
) {
  const match = /^(-?)(\d+)(?:\.(\d+))?$/.exec(String(value).trim());
  if (!match) return '—';
  const rounded = roundMagnitude(match[2], match[3] ?? '', maximumFractionDigits);
  const integerDigits = rounded.integer.replace(/^0+/, '') || '0';
  const integer = integerDigits.replace(/\B(?=(\d{3})+(?!\d))/g, '.');
  let fraction = rounded.fraction.replace(/0+$/, '');
  const hasDisplayedMagnitude = integerDigits !== '0' || fraction !== '';
  if (fraction.length < minimumFractionDigits)
    fraction = fraction.padEnd(minimumFractionDigits, '0');
  return `${match[1] === '-' && hasDisplayedMagnitude ? '-' : ''}${integer}${fraction ? `,${fraction}` : ''}`;
}

function currencySuffix(currency: string) {
  const code = String(currency).trim().toUpperCase() || 'TRY';
  const symbols: Record<string, string> = { TRY: '₺', USD: '$', EUR: '€', GBP: '£' };
  return symbols[code] ?? code;
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

/** A money amount, always with the two decimals the currency is counted in.
 *  The API stores money at a much larger scale, and printing those digits
 *  ("1.234,5678 ₺") is read as a different number than the amount actually
 *  is - so an amount is rounded for display, never shown at storage scale. */
export function formatMoney(value: string | number, currency = 'TRY') {
  const amount = formatAmount(value);
  if (amount === '—') return amount;
  return `${amount} ${currencySuffix(currency)}`;
}

/** A money amount without the currency, for places that print their own. */
export function formatAmount(value: string | number) {
  return exactDecimal(value, 2, 2);
}

/** A unit price or an exchange rate, where digits past the second are a real
 *  part of the figure rather than storage padding. Only those fields may use
 *  this - a total, a balance or a paid amount is money and belongs in
 *  `formatMoney`, which is read at the two decimals money is counted in. */
export function formatUnitPrice(value: string | number, currency = 'TRY') {
  const amount = exactDecimal(value, 2, 8);
  if (amount === '—') return amount;
  return `${amount} ${currencySuffix(currency)}`;
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
