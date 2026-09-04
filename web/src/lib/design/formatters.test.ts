import { describe, expect, it } from 'vitest';
import {
  formatAmount,
  formatDate,
  formatMoney,
  formatUnitPrice,
  formatQuantity,
  formatQuantityWithUnit,
  isNegativeDecimal,
  isNonPositiveDecimal
} from './formatters';

describe('Turkish ERP formatters', () => {
  it('formats exact decimal strings without duplicating presentation rules', () => {
    expect(formatMoney('142450.20', 'TRY')).toMatch(/142\.450,20\s*₺/);
    // A money amount is read at the two decimals the currency is counted in;
    // the storage scale behind it is rounded away rather than printed.
    expect(formatMoney('10.12500000', 'TRY')).toMatch(/^10,13\s*₺/);
    expect(formatMoney('1234.5678', 'TRY')).toMatch(/^1\.234,57\s*₺/);
    expect(formatAmount('1234.5678')).toBe('1.234,57');
    // A unit price keeps the digits it was priced with.
    expect(formatUnitPrice('10.12500000', 'TRY')).toMatch(/^10,125\s*₺/);
    expect(formatUnitPrice('42.15340000', 'TRY')).toMatch(/^42,1534\s*₺/);
    expect(formatMoney('0.000000000000', 'TRY')).toMatch(/^0,00\s*₺/);
    expect(formatQuantity('1250.500')).toBe('1.250,5');
    expect(formatQuantity('1.00000000')).toBe('1');
    expect(formatQuantity('0.12345678')).toBe('0,12345678');
    expect(formatQuantity('0.00000000')).toBe('0');
    expect(formatQuantity('-23.00000000')).toBe('-23');
    expect(formatMoney('-0.00', 'TRY')).toMatch(/^0,00\s*₺/);
    expect(formatQuantity('-0.000000001')).toBe('0');
    expect(formatMoney('10', 'usd')).toMatch(/^10,00\s*\$/);
    expect(formatQuantityWithUnit('1.00000000', 'adet')).toBe('1 ADET');
    expect(formatQuantityWithUnit('10', 'ADET')).toBe('10 ADET');
    expect(formatQuantityWithUnit('99 ADET')).toBe('99 ADET');
  });
  it('uses Turkish date order', () => expect(formatDate('2026-08-21')).toBe('21.08.2026'));
  it('compares decimal strings without floating-point coercion', () => {
    expect(isNegativeDecimal('-0.00')).toBe(false);
    expect(isNegativeDecimal('-142450.20')).toBe(true);
    expect(isNonPositiveDecimal('0.000')).toBe(true);
    expect(isNonPositiveDecimal('0.001')).toBe(false);
  });
});
