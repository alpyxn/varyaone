import { api } from '$lib/api';
import type { ColumnVisibilityState } from '$lib/components/varya/data-grid';

export type TablePreference = {
  table_key: string;
  column_visibility: ColumnVisibilityState;
  version: number;
  updated_at?: string;
};

export const getTablePreference = (tableKey: string, signal?: AbortSignal) =>
  api<TablePreference>(`/preferences/tables/${encodeURIComponent(tableKey)}`, { signal });

export const saveTablePreference = (tableKey: string, columnVisibility: ColumnVisibilityState) =>
  api<TablePreference>(`/preferences/tables/${encodeURIComponent(tableKey)}`, {
    method: 'PUT',
    body: JSON.stringify({ column_visibility: columnVisibility })
  });
