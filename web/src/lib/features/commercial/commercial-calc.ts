import { canonicalDecimal, trimDecimalZeros } from '$lib/design/decimal';

/**
 * Fixed-point decimal arithmetic at 8 fractional digits, matching the API's
 * storage scale. These are CLIENT-SIDE PREVIEW helpers only: the authoritative
 * tax and total figures are always computed by the server tax engine on save.
 * The document editor shows these values while the user types and replaces them
 * with the server's numbers after every save/command.
 */
const SCALE = 100000000n;

export function decimalParts(value: string): [bigint, bigint] {
  const normalized = canonicalDecimal(value) || '0';
  const negative = normalized.startsWith('-');
  const unsigned = negative ? normalized.slice(1) : normalized;
  const [whole = '0', fraction = ''] = unsigned.split('.');
  const digits = `${fraction}00000000`.slice(0, 8);
  const scaled = BigInt(whole.replace(/\D/g, '') || '0') * SCALE + BigInt(digits || '0');
  return [negative ? -scaled : scaled, SCALE];
}

export function decimalString(value: bigint): string {
  const negative = value < 0n;
  const absolute = negative ? -value : value;
  const whole = absolute / SCALE;
  const fraction = (absolute % SCALE).toString().padStart(8, '0').replace(/0+$/, '');
  return `${negative ? '-' : ''}${whole}${fraction ? `.${fraction}` : ''}`;
}

export function decimalAdd(left: string, right: string): string {
  return decimalString(decimalParts(left)[0] + decimalParts(right)[0]);
}

export function decimalSubtract(left: string, right: string): string {
  return decimalString(decimalParts(left)[0] - decimalParts(right)[0]);
}

export function decimalMultiply(left: string, right: string): string {
  return decimalString((decimalParts(left)[0] * decimalParts(right)[0]) / SCALE);
}

export function decimalDivide(left: string, right: string): string {
  const divisor = decimalParts(right)[0];
  if (divisor === 0n) return '0';
  return decimalString((decimalParts(left)[0] * SCALE) / divisor);
}

export type LineAmountInput = {
  quantity: string;
  unitPrice: string;
  discountRate: string;
  taxRate: string;
  taxIncluded: boolean;
};

export type LineAmounts = {
  gross: string;
  discount: string;
  taxBase: string;
  tax: string;
  total: string;
};

/** Preview line breakdown. Tax-inclusive prices back out the base from the
 *  gross-after-discount amount; tax-exclusive add tax on top. */
export function lineAmounts(line: LineAmountInput): LineAmounts {
  const gross = decimalMultiply(line.quantity, line.unitPrice || '0');
  const discount = decimalMultiply(gross, decimalDivide(line.discountRate || '0', '100'));
  const afterDiscount = decimalSubtract(gross, discount);
  let taxBase = afterDiscount;
  let tax = decimalMultiply(afterDiscount, decimalDivide(line.taxRate || '0', '100'));
  if (line.taxIncluded) {
    taxBase = decimalDivide(
      decimalMultiply(afterDiscount, '100'),
      decimalAdd('100', line.taxRate || '0')
    );
    tax = decimalSubtract(afterDiscount, taxBase);
  }
  return { gross, discount, taxBase, tax, total: decimalAdd(taxBase, tax) };
}

export type FormTotals = {
  subtotal: string;
  discountTotal: string;
  taxTotal: string;
  payableTotal: string;
};

export function formTotals(lines: LineAmountInput[]): FormTotals {
  let subtotal = '0';
  let discountTotal = '0';
  let taxTotal = '0';
  let payableTotal = '0';
  for (const line of lines) {
    const amounts = lineAmounts(line);
    subtotal = decimalAdd(subtotal, amounts.gross);
    discountTotal = decimalAdd(discountTotal, amounts.discount);
    taxTotal = decimalAdd(taxTotal, amounts.tax);
    payableTotal = decimalAdd(payableTotal, amounts.total);
  }
  return { subtotal, discountTotal, taxTotal, payableTotal };
}

/** Reconstruct a percentage discount rate from a persisted money discount so an
 *  edited draft does not silently drop its line discounts. */
export function discountRateFromAmounts(discountAmount: unknown, grossAmount: unknown): string {
  const discount = canonicalDecimal(String(discountAmount ?? '0')) || '0';
  const gross = canonicalDecimal(String(grossAmount ?? '0')) || '0';
  if (decimalParts(discount)[0] <= 0n || decimalParts(gross)[0] <= 0n) return '';
  return trimDecimalZeros(decimalMultiply(decimalDivide(discount, gross), '100')) || '';
}
