import type { Component } from 'svelte';
import type { Density } from '$lib/design/density.svelte';
import type { VaryaGridQuery } from './query';

export type CellProps<T> = { value: unknown; row: T };
export type ColumnVisibilityState = Record<string, boolean>;
export type VaryaColumn<T> = {
  id: string;
  header: string;
  accessor: (row: T) => unknown;
  queryField?: string;
  width?: number;
  minWidth?: number;
  maxWidth?: number;
  sortable?: boolean;
  hideable?: boolean;
  /** Whether the column is visible when no saved visibility preference exists. */
  defaultVisible?: boolean;
  align?: 'left' | 'right' | 'center';
  /** Optional real link for code/name cells. Row navigation remains available
   * through double click and Enter when the cell is not activated. */
  link?: (row: T) => string | undefined;
  cell?: Component<CellProps<T>>;
};

/**
 * Returns the visibility state implied by column definitions.
 *
 * The state is intentionally sparse: visible columns are omitted and hidden
 * columns are represented by `false`. This keeps the value compatible with
 * TanStack Table and with the saved table-preference API.
 */
export function defaultColumnVisibility<T>(columns: VaryaColumn<T>[]): ColumnVisibilityState {
  return Object.fromEntries(
    columns
      .filter((column) => column.hideable !== false && column.defaultVisible === false)
      .map((column) => [column.id, false])
  );
}

/**
 * Makes externally supplied visibility safe for the grid.
 *
 * Non-hideable columns are always visible, column-definition defaults apply
 * when a preference has no opinion, and at least one column remains visible so
 * a malformed or stale saved preference cannot produce an unusable table.
 */
export function normalizeColumnVisibility<T>(
  columns: VaryaColumn<T>[],
  visibility: ColumnVisibilityState = {}
): ColumnVisibilityState {
  const next = { ...defaultColumnVisibility(columns), ...visibility };
  const hideableColumns = columns.filter((column) => column.hideable !== false);

  for (const column of columns) {
    if (column.hideable === false) delete next[column.id];
  }

  const visibleColumns = columns.filter(
    (column) => column.hideable === false || next[column.id] !== false
  );
  if (columns.length > 0 && visibleColumns.length === 0) {
    const fallback = hideableColumns[0] ?? columns[0];
    delete next[fallback.id];
  }

  return next;
}

export type VaryaDataGridProps<T> = {
  columns: VaryaColumn<T>[];
  data: T[];
  getRowId: (row: T) => string;
  density?: Density;
  selectable?: boolean;
  resizable?: boolean;
  stickyHeader?: boolean;
  virtualized?: boolean;
  loading?: boolean;
  error?: string;
  emptyTitle?: string;
  emptyDescription?: string;
  columnVisibility?: ColumnVisibilityState;
  onColumnVisibilityChange?: (visibility: ColumnVisibilityState) => void;
  query?: VaryaGridQuery;
  onQueryChange?: (query: VaryaGridQuery) => void;
  onRowOpen?: (row: T) => void;
  nextCursor?: string;
  onLoadMore?: () => void;
  previousPage?: boolean;
  onLoadPrevious?: () => void;
  pageLabel?: string;
  loadingMore?: boolean;
  onRetry?: () => void;
};
