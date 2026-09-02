import { describe, expect, it } from 'vitest';
import { foldUnitCode, mergeUnitGroups } from './unit-catalog';
import type { ProductUnit } from './types';

const codes = (kind: 'PHYSICAL' | 'SERVICE', extra: ProductUnit[] = [], ...always: string[]) =>
  mergeUnitGroups(kind, extra, ...always).flatMap((group) => group.units.map((u) => u.code));

describe('unit-catalog', () => {
  it('folds ASCII/Turkish/superscript variants to one canonical code', () => {
    expect(foldUnitCode('GUN')).toBe(foldUnitCode('GÜN'));
    expect(foldUnitCode('KOLI')).toBe(foldUnitCode('KOLİ'));
    expect(foldUnitCode('m2')).toBe(foldUnitCode('M²'));
  });

  it('keeps physical and service birim lists separate', () => {
    const physical = codes('PHYSICAL');
    expect(physical).toContain('ADET');
    expect(physical).toContain('KG');
    expect(physical).not.toContain('SAAT');
    expect(physical).not.toContain('SEANS');

    const service = codes('SERVICE');
    expect(service).toContain('SAAT');
    expect(service).not.toContain('KUTU');
  });

  it('does not leak backend units that match the other kind (even ASCII variants)', () => {
    const backend = [
      { code: 'SAAT', name: 'Saat', is_base: false, conversion_factor: '1', decimal_scale: 2 },
      { code: 'GUN', name: 'Gün', is_base: false, conversion_factor: '1', decimal_scale: 2 },
      { code: 'KOLI', name: 'Koli', is_base: false, conversion_factor: '1', decimal_scale: 3 },
      { code: 'M2', name: 'Metrekare', is_base: false, conversion_factor: '1', decimal_scale: 3 }
    ];
    const groups = mergeUnitGroups('PHYSICAL', backend);
    expect(groups.find((g) => g.id === 'custom')).toBeUndefined();
  });

  it('adds a genuinely custom backend unit under "Diğer birimler"', () => {
    const backend = [
      { code: 'DEMET', name: 'Demet', is_base: false, conversion_factor: '1', decimal_scale: 0 }
    ];
    expect(codes('PHYSICAL', backend)).toContain('DEMET');
  });
});
