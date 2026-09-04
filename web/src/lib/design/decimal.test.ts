import { describe, expect, it } from 'vitest';
import {
  addDecimalStrings,
  addSignedDecimalStrings,
  canonicalDecimal,
  decimalNumber,
  isZeroDecimal,
  multiplyDecimalStrings,
  negateDecimalString,
  parseMoneyInput,
  subtractDecimalStrings,
  trimDecimalZeros
} from './decimal';

describe('decimal input helpers', () => {
  it.each([
    ['600', '600'],
    ['6000', '6000'],
    ['600,50', '600.50'],
    ['1.250,50', '1250.50'],
    ['1,250.50', '1250.50']
  ])('preserves the magnitude of %s', (input, expected) => {
    expect(canonicalDecimal(input)).toBe(expected);
  });

  it.each([
    ['1.500', '1500'],
    ['1.234.567', '1234567'],
    ['12.345', '12345'],
    ['1.500,50', '1500.50'],
    ['1500,50', '1500.50'],
    ['1500', '1500'],
    ['-2.500', '-2500'],
    // Not thousands grouping: a zero lead, or a group that is not three long.
    ['0.500', '0.500'],
    ['1234.5678', '1234.5678'],
    ['1.5', '1.5'],
    ['', '']
  ])('reads the money field entry %s the way it is typed in Turkish', (input, expected) => {
    expect(parseMoneyInput(input)).toBe(expected);
  });

  it('returns zero only for invalid numeric conversion', () => {
    expect(decimalNumber('600,50')).toBe(600.5);
    expect(decimalNumber('not-a-number')).toBe(0);
  });

  it('detects zero values without converting large amounts to Number', () => {
    expect(isZeroDecimal('-0.0000')).toBe(true);
    expect(isZeroDecimal('0.0001')).toBe(false);
    expect(isZeroDecimal('900719925474099200000.00')).toBe(false);
  });

  it('adds stock quantities exactly and trims scale padding', () => {
    expect(addDecimalStrings('3.00000000', '0.12500000')).toBe('3.125');
    expect(addDecimalStrings('0.1', '0.2')).toBe('0.3');
  });

  it('subtracts money amounts exactly and clamps negative results', () => {
    expect(subtractDecimalStrings('10.00', '3.25')).toBe('6.75');
    expect(subtractDecimalStrings('0.3', '0.2')).toBe('0.1');
    expect(subtractDecimalStrings('1', '2')).toBe('0');
  });

  it('adds signed money and converts amounts without floating-point drift', () => {
    expect(addSignedDecimalStrings('9007199254740992.25', '0.75')).toBe('9007199254740993');
    expect(addSignedDecimalStrings('10.125', '-3.25', '-0.875')).toBe('6');
    expect(multiplyDecimalStrings('123456789012345678.1250', '1.25', 4)).toBe(
      '154320986265432097.6563'
    );
    expect(negateDecimalString('-0.00')).toBe('0');
  });

  it.each([
    ['400.00000000', '400'],
    ['20.00000000', '20'],
    ['10.12500000', '10.125'],
    ['0.00000000', '0'],
    ['1,250.5000', '1250.5']
  ])('removes insignificant scale padding from %s', (input, expected) => {
    expect(trimDecimalZeros(input)).toBe(expected);
  });
});
