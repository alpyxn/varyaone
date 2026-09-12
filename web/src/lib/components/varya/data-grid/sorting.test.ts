// @vitest-environment node
import { describe, expect, it } from 'vitest';
import { withGridSorting, gridQueryToSearchParams, type VaryaGridQuery } from './query';

describe('shared grid sorting', () => {
  it.each(['asc', 'desc'] as const)('discards the old cursor when sorting %s', (direction) => {
    const query: VaryaGridQuery = {
      search: 'Ankara',
      filters: [{ field: 'is_active', operator: 'eq', value: 'true' }],
      sorting: [],
      pagination: { mode: 'cursor', cursor: 'old-page', pageSize: 50 }
    };
    const next = withGridSorting(query, [{ field: 'code', direction }]);
    expect(gridQueryToSearchParams(next).get('cursor')).toBeNull();
    expect(gridQueryToSearchParams(next).get('sort')).toBe(`code:${direction}`);
    expect(next.search).toBe('Ankara');
    expect(next.filters).toEqual(query.filters);
    expect(query.pagination).toHaveProperty('cursor', 'old-page');
  });
  it('returns to page one and resets sorting without losing page size', () => {
    const next = withGridSorting(
      {
        search: 'Ankara',
        filters: [],
        sorting: [{ field: 'code', direction: 'desc' }],
        pagination: { mode: 'page', page: 4, pageSize: 100 }
      },
      []
    );
    expect(next.pagination).toEqual({ mode: 'page', page: 1, pageSize: 100 });
    expect(gridQueryToSearchParams(next).has('sort')).toBe(false);
  });
});
