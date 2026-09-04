import { canonicalDecimal, trimDecimalZeros } from '$lib/design/decimal';
import type { LineTaxComponent } from './editor-types';

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
  /** Taxes charged besides KDV. Mirrors the server tax engine's components. */
  taxComponents?: LineTaxComponent[];
};

export type LineAmounts = {
  gross: string;
  discount: string;
  taxBase: string;
  /** Total tax on the line: KDV plus every additional tax. */
  tax: string;
  /** The KDV part of `tax`. */
  vat: string;
  /** Everything charged besides KDV (ÖTV, ÖİV, …). */
  additionalTax: string;
  total: string;
};

type ComponentLayer = { percentage: string; amount: string };

/** Split the additional taxes into the ones that belong to the KDV base and the
 *  ones charged next to it, summing percentages and fixed amounts separately. */
function componentLayers(quantity: string, components: LineTaxComponent[] = []) {
  const inner: ComponentLayer = { percentage: '0', amount: '0' };
  const outer: ComponentLayer = { percentage: '0', amount: '0' };
  for (const component of components) {
    const rate = canonicalDecimal(component.rate || '0') || '0';
    const layer = component.includedInBase ? inner : outer;
    if (component.calculationType === 'QUANTITY_BASED') {
      layer.amount = decimalAdd(layer.amount, decimalMultiply(quantity || '0', rate));
    } else if (component.calculationType === 'FIXED_AMOUNT') {
      layer.amount = decimalAdd(layer.amount, rate);
    } else {
      layer.percentage = decimalAdd(layer.percentage, rate);
    }
  }
  return { inner, outer };
}

/** Preview line breakdown, mirroring the server tax engine: taxes flagged as
 *  part of the tax base (ÖTV) are charged on the net amount and then join the
 *  base KDV is charged on. Tax-inclusive prices solve the net amount back out
 *  of that same cascade. */
export function lineAmounts(line: LineAmountInput): LineAmounts {
  const gross = decimalMultiply(line.quantity, line.unitPrice || '0');
  const discount = decimalMultiply(gross, decimalDivide(line.discountRate || '0', '100'));
  const afterDiscount = decimalSubtract(gross, discount);
  const { inner, outer } = componentLayers(line.quantity, line.taxComponents);
  const vatRate = canonicalDecimal(line.taxRate || '0') || '0';
  const innerMultiplier = decimalAdd('1', decimalDivide(inner.percentage, '100'));
  const outerMultiplier = decimalAdd(
    '1',
    decimalDivide(decimalAdd(vatRate, outer.percentage), '100')
  );

  let taxBase = afterDiscount;
  if (line.taxIncluded) {
    const constants = decimalAdd(decimalMultiply(inner.amount, outerMultiplier), outer.amount);
    taxBase = decimalDivide(
      decimalSubtract(afterDiscount, constants),
      decimalMultiply(innerMultiplier, outerMultiplier)
    );
  }
  const innerTax = decimalAdd(
    decimalMultiply(taxBase, decimalDivide(inner.percentage, '100')),
    inner.amount
  );
  const vatBase = decimalAdd(taxBase, innerTax);
  const vat = decimalMultiply(vatBase, decimalDivide(vatRate, '100'));
  const outerTax = decimalAdd(
    decimalMultiply(vatBase, decimalDivide(outer.percentage, '100')),
    outer.amount
  );
  const additionalTax = decimalAdd(innerTax, outerTax);
  const tax = decimalAdd(vat, additionalTax);
  return { gross, discount, taxBase, tax, vat, additionalTax, total: decimalAdd(taxBase, tax) };
}

export type ComponentAmount = LineTaxComponent & { amount: string };

/** What each additional tax on the line actually costs, on the base its profile
 *  puts it on: taxes inside the KDV base charge on the net amount, the rest on
 *  the net amount plus those taxes. */
export function lineComponentAmounts(line: LineAmountInput): ComponentAmount[] {
  const components = line.taxComponents ?? [];
  if (components.length === 0) return [];
  const { taxBase } = lineAmounts(line);
  const { inner } = componentLayers(line.quantity, components);
  const innerTax = decimalAdd(
    decimalMultiply(taxBase, decimalDivide(inner.percentage, '100')),
    inner.amount
  );
  const vatBase = decimalAdd(taxBase, innerTax);
  return components.map((component) => {
    const rate = canonicalDecimal(component.rate || '0') || '0';
    if (component.calculationType === 'QUANTITY_BASED') {
      return { ...component, amount: decimalMultiply(line.quantity || '0', rate) };
    }
    if (component.calculationType === 'FIXED_AMOUNT') {
      return { ...component, amount: rate };
    }
    return {
      ...component,
      amount: decimalMultiply(
        component.includedInBase ? taxBase : vatBase,
        decimalDivide(rate, '100')
      )
    };
  });
}

export type FormTotals = {
  subtotal: string;
  discountTotal: string;
  taxTotal: string;
  vatTotal: string;
  additionalTaxTotal: string;
  payableTotal: string;
};

export function formTotals(lines: LineAmountInput[]): FormTotals {
  let subtotal = '0';
  let discountTotal = '0';
  let taxTotal = '0';
  let vatTotal = '0';
  let additionalTaxTotal = '0';
  let payableTotal = '0';
  for (const line of lines) {
    const amounts = lineAmounts(line);
    subtotal = decimalAdd(subtotal, amounts.gross);
    discountTotal = decimalAdd(discountTotal, amounts.discount);
    taxTotal = decimalAdd(taxTotal, amounts.tax);
    vatTotal = decimalAdd(vatTotal, amounts.vat);
    additionalTaxTotal = decimalAdd(additionalTaxTotal, amounts.additionalTax);
    payableTotal = decimalAdd(payableTotal, amounts.total);
  }
  return { subtotal, discountTotal, taxTotal, vatTotal, additionalTaxTotal, payableTotal };
}

/** Reconstruct a percentage discount rate from a persisted money discount so an
 *  edited draft does not silently drop its line discounts. */
export function discountRateFromAmounts(discountAmount: unknown, grossAmount: unknown): string {
  const discount = canonicalDecimal(String(discountAmount ?? '0')) || '0';
  const gross = canonicalDecimal(String(grossAmount ?? '0')) || '0';
  if (decimalParts(discount)[0] <= 0n || decimalParts(gross)[0] <= 0n) return '';
  return trimDecimalZeros(decimalMultiply(decimalDivide(discount, gross), '100')) || '';
}
