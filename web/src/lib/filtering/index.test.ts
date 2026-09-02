import { describe, expect, it } from 'vitest';
import { createFilterEngine, matchesSearch, normalizeFilterText, searchableText } from './index';

describe('shared filter engine', () => {
  it('normalizes Turkish text and punctuation into stable search tokens', () => {
    expect(normalizeFilterText('İstanbul / Çağrı Şirketi')).toBe('istanbul cagri sirketi');
  });

  it('searches nested arrays and objects with all query tokens', () => {
    const party = {
      display_name: 'Varya Makine',
      contacts: [{ full_name: 'Ayşe Demir', email: 'ayse@ornek.test' }],
      addresses: [{ city: 'İstanbul', address_line: 'Organize Sanayi' }],
      custom_fields: { bolge: 'Marmara' }
    };

    expect(matchesSearch(party, 'AYSE İstanbul')).toBe(true);
    expect(matchesSearch(party, 'ayse Ankara')).toBe(false);
    expect(searchableText(party)).toContain('marmara');
  });

  it('supports reusable field filters without floating point conversion', () => {
    const engine = createFilterEngine<{ code: string; limit: string; tags: string[] }>();
    const rows = [
      { code: 'CAR-1', limit: '100.1250', tags: ['öncelikli'] },
      { code: 'CAR-2', limit: '9.99', tags: ['standart'] }
    ];

    expect(
      engine.filter(rows, '', [{ field: 'limit', operator: 'gte', value: '100.1250' }])
    ).toEqual([rows[0]]);
    expect(
      engine.matches(rows[0], '', [{ field: 'limit', operator: 'eq', value: '100.125' }])
    ).toBe(true);
    expect(
      engine.matches(rows[0], '', [{ field: 'tags', operator: 'contains', value: 'öncelikli' }])
    ).toBe(true);
  });
});
