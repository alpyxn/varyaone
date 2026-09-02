import { describe, expect, it } from 'vitest';
import {
  combinationCount,
  combinationWarning,
  normalizeVariantCodePart,
  suggestedVariantSku,
  variantConfigurationError
} from './variant-utils';

const dimension = (option_ids: string[]) => ({ definition_id: 'color', option_ids });

describe('variant configuration helpers', () => {
  it('calculates matrix combinations', () => {
    expect(combinationCount([dimension(['red', 'blue']), dimension(['s', 'm', 'l'])])).toBe(6);
  });

  it('rejects empty dimensions and the technical upper limit', () => {
    expect(variantConfigurationError([dimension([])])).toBe(
      'Her boyut için en az bir seçenek seçin.'
    );
    expect(
      variantConfigurationError(
        Array.from({ length: 2 }, (_, index) =>
          dimension(Array.from({ length: index ? 501 : 3 }, (_, item) => String(item)))
        )
      )
    ).toBe('Kombinasyon sayısı 1.000 sınırını aşıyor.');
  });

  it('warns before a large but valid generation', () => {
    expect(
      combinationWarning([dimension(Array.from({ length: 300 }, (_, index) => String(index)))])
    ).toContain('300 kombinasyon');
  });

  it('normalizes Turkish SKU parts and suggests a SKU', () => {
    expect(normalizeVariantCodePart('Siyah / Büyük')).toBe('SIYAH-BUYUK');
    expect(
      suggestedVariantSku(' tsh ', [
        { id: '1', definition_id: 'r', code: 'blk', name: 'Siyah', is_active: true, version: 1 }
      ])
    ).toBe('TSH-BLK');
  });
});
