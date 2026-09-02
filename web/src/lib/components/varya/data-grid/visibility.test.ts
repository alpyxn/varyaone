import { describe, expect, it } from 'vitest';
import { defaultColumnVisibility, normalizeColumnVisibility, type VaryaColumn } from './types';

type Row = { id: string };

const columns: VaryaColumn<Row>[] = [
  { id: 'code', header: 'Kod', accessor: (row) => row.id, hideable: false },
  { id: 'name', header: 'Ad', accessor: (row) => row.id },
  {
    id: 'description',
    header: 'Açıklama',
    accessor: (row) => row.id,
    defaultVisible: false
  }
];

describe('data-grid column visibility', () => {
  it('creates sparse defaults from column definitions', () => {
    expect(defaultColumnVisibility(columns)).toEqual({ description: false });
  });

  it('keeps non-hideable columns visible and applies default visibility', () => {
    expect(normalizeColumnVisibility(columns, { code: false })).toEqual({
      description: false
    });
  });

  it('allows an explicit preference to override a default', () => {
    expect(normalizeColumnVisibility(columns, { description: true })).toEqual({
      description: true
    });
  });

  it('restores the first column when a stale preference hides everything', () => {
    const allHideable = columns.filter((column) => column.id !== 'code');
    expect(normalizeColumnVisibility(allHideable, { name: false, description: false })).toEqual({
      description: false
    });
  });
});
