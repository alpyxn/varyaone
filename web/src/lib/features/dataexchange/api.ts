import { api } from '$lib/api';

export type ImportEntity =
  | 'PRODUCT'
  | 'VARIANT'
  | 'BARCODE'
  | 'WAREHOUSE'
  | 'PARTY'
  | 'PRICE_LIST'
  | 'OPENING_STOCK'
  | 'STOCK_COUNT';

export type ImportCapabilityField = {
  name: string;
  label: string;
  type: string;
  required: boolean;
  example: string;
};

export type ImportCapabilityEntity = {
  type: ImportEntity;
  label: string;
  importable: boolean;
  exportable: boolean;
  fields: ImportCapabilityField[];
};

export type ImportCapabilities = {
  max_upload_bytes: number;
  entities: ImportCapabilityEntity[];
};

export type ImportJob = {
  id: string;
  entity_type: ImportEntity;
  direction: 'IMPORT' | 'EXPORT';
  status: string;
  commit_mode: 'ATOMIC' | 'PARTIAL';
  dry_run: boolean;
  source_filename?: string;
  row_count: number;
  error_count: number;
  warning_count: number;
  created_at: string;
  updated_at: string;
  state?: string;
  analysis_revision?: string;
};

export type ImportAnalysis = {
  job: ImportJob;
  analysis_revision?: string;
  mapping: Array<{ source_column: string; field: string; method: string }>;
  preview: {
    total_rows: number;
    valid_rows: number;
    warning_rows: number;
    invalid_rows: number;
    can_commit: boolean;
    rows: Array<{
      row_number: number;
      values: Record<string, string>;
      status: string;
      issues?: Array<{ field?: string; code: string; message: string; severity: string }> | null;
    }>;
  };
  error_report?: {
    csv_url?: string;
    xlsx_url?: string;
  };
};

export type ExportJob = {
  id: string;
  entity_type: string;
  format: 'CSV' | 'XLSX';
  status: string;
  filename?: string;
  row_count: number;
};

export type ExportFormat = 'CSV' | 'XLSX';

export type MappingInput = {
  sourceColumn: string;
  targetField: string;
};

/**
 * The API accepts stable target field IDs as keys and source column names as
 * values. Keeping this conversion here prevents UI labels from becoming
 * target identities in the wire contract.
 */
export function buildImportMapping(rows: MappingInput[]) {
  return Object.fromEntries(
    rows
      .map(({ sourceColumn, targetField }) => [targetField.trim(), sourceColumn.trim()] as const)
      .filter(([targetField, sourceColumn]) => Boolean(sourceColumn && targetField))
  );
}

export function isUploadSizeAllowed(fileSize: number, maxUploadBytes: number) {
  return (
    Number.isFinite(fileSize) &&
    fileSize >= 0 &&
    Number.isFinite(maxUploadBytes) &&
    maxUploadBytes > 0 &&
    fileSize <= maxUploadBytes
  );
}

export function selectableImportEntities(
  capabilities: ImportCapabilities,
  direction: 'IMPORT' | 'EXPORT'
) {
  const separateWorkflowEntities = new Set<ImportEntity>([
    'PRICE_LIST',
    'OPENING_STOCK',
    'STOCK_COUNT'
  ]);
  return capabilities.entities.filter((entity) => {
    if (
      separateWorkflowEntities.has(entity.type) ||
      (direction === 'IMPORT' && ['VARIANT', 'BARCODE'].includes(entity.type))
    ) {
      return false;
    }
    return direction === 'IMPORT' ? entity.importable : entity.exportable;
  });
}

export function getImportCapabilities() {
  return api<ImportCapabilities>('/imports/capabilities');
}

export function uploadImport(file: File, entityType: ImportEntity, targetID = '') {
  const form = new FormData();
  form.append('file', file, file.name);
  form.append('entity_type', entityType);
  form.append('commit_mode', 'ATOMIC');
  if (targetID) form.append('target_id', targetID);
  return api<ImportJob>('/imports', { method: 'POST', body: form });
}

export function analyzeImport(id: string, mapping: Record<string, string> = {}) {
  return api<ImportAnalysis>(`/imports/${encodeURIComponent(id)}/analyze`, {
    method: 'POST',
    body: JSON.stringify({ mapping })
  });
}

export function getImportStatus(id: string) {
  return api<ImportJob>(`/imports/${encodeURIComponent(id)}`);
}

export function commitImport(
  id: string,
  dryRun = false,
  idempotencyKey?: string,
  analysisRevision?: string
) {
  return api<{ dry_run?: boolean; analysis?: ImportAnalysis }>(
    `/imports/${encodeURIComponent(id)}/commit`,
    {
      method: 'POST',
      headers: idempotencyKey ? { 'Idempotency-Key': idempotencyKey } : undefined,
      body: JSON.stringify({ dry_run: dryRun, analysis_revision: analysisRevision ?? '' })
    }
  );
}

export function listImports() {
  return api<{ items: ImportJob[] }>('/imports');
}

export function createExport(entityType: ImportEntity, targetID: string, format: ExportFormat) {
  return api<ExportJob>('/exports', {
    method: 'POST',
    body: JSON.stringify({ entity_type: entityType, target_id: targetID, format })
  });
}
