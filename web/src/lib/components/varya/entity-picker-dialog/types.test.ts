import { describe, expect, it } from 'vitest';
import { entityMetaText, entitySearchText, extractEntityOptions, type EntityOption } from './types';

const stock: EntityOption = {
  id: 'stok-1',
  title: 'STK-001 · Kırmızı Kupa',
  subtitle: 'Kupa / 330 ml',
  meta: ['Barkod: 869000000001', 'Ana depo']
};

describe('entity picker option helpers', () => {
  it('reads array and common API envelopes', () => {
    expect(extractEntityOptions<EntityOption>([stock])).toEqual([stock]);
    expect(extractEntityOptions<EntityOption>({ items: [stock] })).toEqual([stock]);
    expect(extractEntityOptions<EntityOption>({ data: { results: [stock] } })).toEqual([stock]);
  });

  it('creates Turkish-searchable text from all visible option fields', () => {
    expect(entityMetaText(stock.meta)).toBe('Barkod: 869000000001 · Ana depo');
    expect(entitySearchText(stock)).toContain('kırmızı kupa');
    expect(entitySearchText(stock)).toContain('ana depo');
  });
});
