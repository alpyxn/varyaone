import { api } from '$lib/api';
import type { ReportDef, ReportFilterKind } from './registry';

export type ReportRow = Record<string, unknown>;

export type ReportFilterValues = {
  from?: string;
  to?: string;
  direction?: string;
};

const FILTER_PARAM: Record<ReportFilterKind, (keyof ReportFilterValues)[]> = {
  dateRange: ['from', 'to'],
  direction: ['direction']
};

export function buildReportQuery(def: ReportDef, values: ReportFilterValues): string {
  const params = new URLSearchParams();
  const allowed = new Set<keyof ReportFilterValues>();
  for (const kind of def.filters) for (const param of FILTER_PARAM[kind]) allowed.add(param);
  for (const param of allowed) {
    const raw = values[param];
    if (raw === undefined || raw === '') continue;
    params.set(param, String(raw));
  }
  return params.toString();
}

export async function fetchReport(
  def: ReportDef,
  values: ReportFilterValues,
  signal?: AbortSignal
): Promise<ReportRow[]> {
  const query = buildReportQuery(def, values);
  const rows = await api<ReportRow[] | null>(`${def.endpoint}${query ? `?${query}` : ''}`, {
    signal
  });
  return Array.isArray(rows) ? rows : [];
}
