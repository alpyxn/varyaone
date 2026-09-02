export type VaryaSort = { field: string; direction: 'asc' | 'desc' };
export type VaryaFilter = {
  field: string;
  operator: 'eq' | 'contains' | 'gte' | 'lte' | 'in';
  value: string | string[];
};
export type VaryaPagination =
  | { mode: 'cursor'; cursor?: string; pageSize: number }
  | { mode: 'page'; page: number; pageSize: number };
export type VaryaGridQuery = {
  search?: string;
  sorting: VaryaSort[];
  filters: VaryaFilter[];
  pagination: VaryaPagination;
};

export function createGridQuery(pageSize = 50): VaryaGridQuery {
  return { sorting: [], filters: [], pagination: { mode: 'cursor', pageSize } };
}

export function gridQueryToSearchParams(query: VaryaGridQuery) {
  const params = new URLSearchParams();
  if (query.search) params.set('q', query.search);
  if (query.sorting.length)
    params.set('sort', query.sorting.map((sort) => `${sort.field}:${sort.direction}`).join(','));
  for (const filter of query.filters)
    params.append(
      filter.field,
      Array.isArray(filter.value) ? filter.value.join(',') : filter.value
    );
  if (query.pagination.mode === 'cursor') {
    params.set('limit', String(query.pagination.pageSize));
    if (query.pagination.cursor) params.set('cursor', query.pagination.cursor);
  } else {
    params.set('page', String(query.pagination.page));
    params.set('page_size', String(query.pagination.pageSize));
  }
  return params;
}
