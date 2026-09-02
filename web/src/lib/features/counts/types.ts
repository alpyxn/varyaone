export type CountMode = 'OPEN';

export type CountPass = {
  id: string;
  mode: CountMode;
  status: string;
  label?: string;
  submitted_at?: string;
};

export type CountLine = {
  id: string;
  line_no?: number;
  product_id?: string;
  variant_id?: string;
  barcode?: string;
  product_name: string;
  product_code?: string;
  variant_name?: string;
  unit?: string;
  expected_quantity?: string | number | null;
  counted_quantity?: string | number | null;
  difference?: string | number | null;
  status?: string;
  exception?: string;
  [key: string]: unknown;
};

export type CountView = {
  id: string;
  number: string;
  description: string;
  status: string;
  warehouse: string;
  warehouse_code?: string;
  warehouse_id?: string;
  scope_mode?: 'FULL' | 'PARTIAL';
  snapshot_at?: string;
  started_at?: string;
  finished_at?: string;
  version?: string | number;
  passes: CountPass[];
  lines: CountLine[];
  exceptions: CountException[];
  raw: Record<string, unknown>;
};

export type ScanEvent = {
  event_id: string;
  barcode: string;
  quantity: string;
  scanned_at: string;
};

export type CountException = {
  id: string;
  scope_id?: string;
  barcode?: string;
  status?: string;
  message: string;
  severity: 'warning' | 'error';
  created_at: string;
  details?: Record<string, unknown>;
};

export function asRecord(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}

export function firstValue(source: Record<string, unknown>, keys: string | string[]) {
  for (const key of Array.isArray(keys) ? keys : [keys]) {
    const found = key.split('.').reduce<unknown>((current, part) => {
      if (!current || typeof current !== 'object') return undefined;
      return (current as Record<string, unknown>)[part];
    }, source);
    if (found !== undefined && found !== null && found !== '') return found;
  }
  return undefined;
}

function quantityValue(value: unknown): string | number | null | undefined {
  return typeof value === 'string' || typeof value === 'number' || value === null
    ? value
    : undefined;
}

export function normalizePass(value: unknown, index: number): CountPass {
  const source = asRecord(value);
  const id = String(firstValue(source, ['id', 'pass_id']) ?? `pass-${index + 1}`);
  return {
    id,
    // Legacy rows are normalized to the only supported UI mode. New API
    // commands reject blind passes at the domain boundary.
    mode: 'OPEN',
    status: String(firstValue(source, ['status', 'state']) ?? 'IN_PROGRESS'),
    label: typeof source.label === 'string' ? source.label : undefined,
    submitted_at: typeof source.submitted_at === 'string' ? source.submitted_at : undefined
  };
}

export function normalizeLine(value: unknown, index: number): CountLine {
  const source = asRecord(value);
  const rawLineNo = firstValue(source, 'line_no');
  const parsedLineNo =
    typeof rawLineNo === 'number'
      ? rawLineNo
      : typeof rawLineNo === 'string'
        ? Number(rawLineNo)
        : NaN;
  return {
    ...source,
    id: String(firstValue(source, ['id', 'line_id', 'product_id']) ?? `line-${index + 1}`),
    line_no: Number.isInteger(parsedLineNo) && parsedLineNo > 0 ? parsedLineNo : undefined,
    barcode: String(firstValue(source, ['barcode', 'product_barcode', 'sku']) ?? '') || undefined,
    product_name: String(
      firstValue(source, ['product_name', 'product.name', 'name', 'product_code']) ??
        'Tanımsız stok'
    ),
    product_code:
      String(firstValue(source, ['product_code', 'product.code', 'sku']) ?? '') || undefined,
    variant_name:
      String(firstValue(source, ['variant_name', 'variant.name', 'variant_code']) ?? '') ||
      undefined,
    unit: String(firstValue(source, ['unit', 'unit_code', 'stock_unit']) ?? '') || undefined,
    expected_quantity: quantityValue(
      firstValue(source, ['expected_quantity', 'snapshot_quantity', 'book_quantity'])
    ),
    counted_quantity: quantityValue(
      firstValue(source, ['counted_quantity', 'counted', 'physical_quantity'])
    ),
    difference: quantityValue(firstValue(source, ['difference', 'quantity_difference', 'delta'])),
    status: String(firstValue(source, ['status', 'state']) ?? '') || undefined,
    exception: String(firstValue(source, ['exception', 'exception_message']) ?? '') || undefined
  };
}

export function normalizeCount(value: unknown, fallbackID = ''): CountView {
  const source = asRecord(value);
  const rawLines = Array.isArray(source.lines)
    ? source.lines
    : Array.isArray(asRecord(source.data).lines)
      ? (asRecord(source.data).lines as unknown[])
      : [];
  const rawPasses = Array.isArray(source.passes)
    ? source.passes
    : Array.isArray(asRecord(source.data).passes)
      ? (asRecord(source.data).passes as unknown[])
      : [];
  const rawExceptions = Array.isArray(source.exceptions)
    ? source.exceptions
    : Array.isArray(asRecord(source.data).exceptions)
      ? (asRecord(source.data).exceptions as unknown[])
      : [];
  const exceptions = rawExceptions.map((value, index) => {
    const exception = asRecord(value);
    const severity = String(firstValue(exception, ['severity', 'level']) ?? 'error').toLowerCase();
    return {
      id: String(firstValue(exception, ['id', 'exception_id']) ?? `exception-${index + 1}`),
      scope_id: String(firstValue(exception, ['scope_id', 'line_id']) ?? '') || undefined,
      barcode: String(firstValue(exception, ['barcode']) ?? '') || undefined,
      status: String(firstValue(exception, ['status', 'state']) ?? 'OPEN').toUpperCase(),
      message: String(firstValue(exception, ['message', 'reason']) ?? 'İnceleme gerekli'),
      severity: severity === 'warning' ? 'warning' : 'error',
      created_at: String(firstValue(exception, ['created_at', 'occurred_at']) ?? ''),
      details: asRecord(exception.details)
    } satisfies CountException;
  });
  const lines = rawLines.map(normalizeLine).map((line) => {
    const scopedExceptions = exceptions.filter((item) => item.scope_id === line.id);
    const exception = scopedExceptions.find(
      (item) => item.status !== 'RESOLVED' && item.severity === 'error' && item.scope_id
    );
    if (exception && !line.exception) return { ...line, exception: exception.message };
    if (
      line.exception &&
      scopedExceptions.length > 0 &&
      scopedExceptions.every((item) => item.status === 'RESOLVED')
    ) {
      return { ...line, exception: undefined };
    }
    return line;
  });
  return {
    id: String(firstValue(source, ['id', 'count_id']) ?? fallbackID),
    number: String(
      firstValue(source, ['count_no', 'document_no', 'business_number']) ?? 'Sayımsız'
    ),
    description: String(firstValue(source, 'description') ?? ''),
    status: String(firstValue(source, ['status', 'state']) ?? 'DRAFT'),
    warehouse: String(
      firstValue(source, [
        'warehouse_name',
        'warehouse.name',
        'warehouse_code',
        'warehouse.code'
      ]) ?? 'Depo belirtilmemiş'
    ),
    warehouse_code:
      String(firstValue(source, ['warehouse_code', 'warehouse.code']) ?? '') || undefined,
    warehouse_id: String(firstValue(source, ['warehouse_id', 'warehouse.id']) ?? '') || undefined,
    scope_mode:
      String(firstValue(source, 'scope_mode') ?? 'FULL').toUpperCase() === 'PARTIAL'
        ? 'PARTIAL'
        : 'FULL',
    snapshot_at:
      String(firstValue(source, ['snapshot_at', 'started_at', 'created_at']) ?? '') || undefined,
    started_at:
      String(firstValue(source, ['started_at', 'snapshot_at', 'created_at']) ?? '') || undefined,
    finished_at:
      String(firstValue(source, ['finished_at', 'posted_at', 'cancelled_at']) ?? '') || undefined,
    version: firstValue(source, 'version') as string | number | undefined,
    passes: rawPasses.map(normalizePass),
    lines,
    exceptions,
    raw: source
  };
}
