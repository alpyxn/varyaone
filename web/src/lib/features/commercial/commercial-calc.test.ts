import { describe, it, expect } from 'vitest';
import {
  decimalAdd,
  decimalSubtract,
  decimalMultiply,
  decimalDivide,
  lineAmounts,
  lineComponentAmounts,
  formTotals,
  discountRateFromAmounts
} from './commercial-calc';
import type { LineTaxComponent } from './editor-types';

describe('fixed-point decimal helpers', () => {
  it('adds and subtracts without floating-point drift', () => {
    expect(decimalAdd('0.1', '0.2')).toBe('0.3');
    expect(decimalSubtract('0', '0.00000001')).toBe('-0.00000001');
    expect(decimalAdd('9007199254740993.12345678', '0.87654322')).toBe('9007199254740994');
  });

  it('multiplies and divides at 8-digit scale', () => {
    expect(decimalMultiply('3', '100')).toBe('300');
    expect(decimalMultiply('2.5', '2.5')).toBe('6.25');
    expect(decimalDivide('10', '100')).toBe('0.1');
    expect(decimalDivide('1', '3')).toBe('0.33333333');
  });

  it('treats divide-by-zero as zero rather than throwing', () => {
    expect(decimalDivide('5', '0')).toBe('0');
  });
});

const otv = (rate: string): LineTaxComponent => ({
  code: 'OTV',
  name: 'ÖTV',
  calculationType: 'PERCENTAGE',
  rate,
  includedInBase: true
});

describe('lineAmounts', () => {
  const base = {
    quantity: '10',
    unitPrice: '100',
    discountRate: '',
    taxRate: '',
    taxIncluded: false
  };

  it('computes a plain tax-exclusive line', () => {
    expect(lineAmounts({ ...base, taxRate: '20' })).toEqual({
      gross: '1000',
      discount: '0',
      taxBase: '1000',
      tax: '200',
      vat: '200',
      additionalTax: '0',
      total: '1200'
    });
  });

  it('applies a percentage discount before tax', () => {
    expect(lineAmounts({ ...base, discountRate: '10', taxRate: '20' })).toEqual({
      gross: '1000',
      discount: '100',
      taxBase: '900',
      tax: '180',
      vat: '180',
      additionalTax: '0',
      total: '1080'
    });
  });

  it('charges KDV on top of an ÖTV that belongs to the KDV base', () => {
    const amounts = lineAmounts({
      ...base,
      quantity: '1',
      taxRate: '20',
      taxComponents: [otv('10')]
    });
    expect(amounts.taxBase).toBe('100');
    expect(amounts.additionalTax).toBe('10');
    expect(amounts.vat).toBe('22');
    expect(amounts.total).toBe('132');
  });

  it('keeps a tax charged next to KDV out of the KDV base', () => {
    const amounts = lineAmounts({
      ...base,
      quantity: '1',
      taxRate: '20',
      taxComponents: [{ ...otv('10'), code: 'TRT', name: 'TRT Payı', includedInBase: false }]
    });
    expect(amounts.vat).toBe('20');
    expect(amounts.additionalTax).toBe('10');
    expect(amounts.total).toBe('130');
  });

  it('backs a tax-inclusive price out of the KDV and ÖTV cascade', () => {
    const amounts = lineAmounts({
      ...base,
      quantity: '1',
      unitPrice: '132',
      taxRate: '20',
      taxIncluded: true,
      taxComponents: [otv('10')]
    });
    expect(amounts.taxBase).toBe('100');
    expect(amounts.additionalTax).toBe('10');
    expect(amounts.vat).toBe('22');
    expect(amounts.total).toBe('132');
  });

  it('charges a quantity-based tax once per unit', () => {
    const amounts = lineAmounts({
      ...base,
      quantity: '3',
      unitPrice: '100',
      taxRate: '20',
      taxComponents: [{ ...otv('5'), calculationType: 'QUANTITY_BASED' }]
    });
    expect(amounts.additionalTax).toBe('15');
    expect(amounts.vat).toBe('63');
  });

  it('reports what each additional tax costs on its own base', () => {
    const components = lineComponentAmounts({
      ...base,
      quantity: '1',
      taxRate: '20',
      taxComponents: [otv('10'), { ...otv('5'), code: 'TRT', name: 'TRT', includedInBase: false }]
    });
    expect(components.map((component) => [component.code, component.amount])).toEqual([
      ['OTV', '10'],
      ['TRT', '5.5']
    ]);
  });

  it('backs the base out of a tax-inclusive price', () => {
    const r = lineAmounts({
      quantity: '1',
      unitPrice: '120',
      discountRate: '',
      taxRate: '20',
      taxIncluded: true
    });
    expect(r.taxBase).toBe('100');
    expect(r.tax).toBe('20');
    expect(r.total).toBe('120');
  });

  it('handles zero quantity and zero price', () => {
    expect(lineAmounts({ ...base, quantity: '0' }).total).toBe('0');
    expect(lineAmounts({ ...base, unitPrice: '0' }).total).toBe('0');
  });

  it('handles a negative quantity (a correction line)', () => {
    expect(lineAmounts({ ...base, quantity: '-2', taxRate: '20' })).toEqual({
      gross: '-200',
      discount: '0',
      taxBase: '-200',
      tax: '-40',
      vat: '-40',
      additionalTax: '0',
      total: '-240'
    });
  });
});

describe('formTotals', () => {
  it('sums line breakdowns', () => {
    const totals = formTotals([
      { quantity: '2', unitPrice: '50', discountRate: '', taxRate: '20', taxIncluded: false },
      { quantity: '1', unitPrice: '100', discountRate: '10', taxRate: '10', taxIncluded: false }
    ]);
    expect(totals.subtotal).toBe('200');
    expect(totals.discountTotal).toBe('10');
    expect(totals.taxTotal).toBe('29');
    expect(totals.payableTotal).toBe('219');
  });

  it('is zero for an empty document', () => {
    expect(formTotals([])).toEqual({
      subtotal: '0',
      discountTotal: '0',
      taxTotal: '0',
      vatTotal: '0',
      additionalTaxTotal: '0',
      payableTotal: '0'
    });
  });
});

describe('discountRateFromAmounts', () => {
  it('reconstructs a clean percentage', () => {
    expect(discountRateFromAmounts('100', '1000')).toBe('10');
    expect(discountRateFromAmounts('0', '1000')).toBe('');
    expect(discountRateFromAmounts('50', '0')).toBe('');
    expect(discountRateFromAmounts(undefined, undefined)).toBe('');
  });
});
