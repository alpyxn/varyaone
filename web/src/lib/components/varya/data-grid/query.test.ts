import { describe, expect, it } from 'vitest';
import { gridQueryToSearchParams, type VaryaGridQuery } from './query';

describe('Varya grid server query model', () => {
  it('keeps UI engine state out of the REST contract', () => {
    const query: VaryaGridQuery = {
      search: 'ankara',
      sorting: [{ field: 'date', direction: 'desc' }],
      filters: [{ field: 'warehouse_id', operator: 'eq', value: 'depo-1' }],
      pagination: { mode: 'cursor', cursor: 'opaque', pageSize: 50 }
    };
    expect(gridQueryToSearchParams(query).toString()).toBe(
      'q=ankara&sort=date%3Adesc&warehouse_id=depo-1&limit=50&cursor=opaque'
    );
  });
  it('supports page based backends without changing the grid API', () => {
    const params = gridQueryToSearchParams({
      sorting: [],
      filters: [],
      pagination: { mode: 'page', page: 3, pageSize: 100 }
    });
    expect(params.get('page')).toBe('3');
    expect(params.get('page_size')).toBe('100');
  });
});
