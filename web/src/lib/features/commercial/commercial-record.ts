import { trimDecimalZeros } from '$lib/design/decimal';
import { discountRateFromAmounts } from './commercial-calc';
import {
  commercialResource,
  commercialResourceFromReference,
  commercialDocumentHref,
  documentStatusLabel,
  type CommercialDirection,
  type CommercialLineType
} from './types';
import type {
  DocumentForm,
  DocumentLine,
  DocumentRecord,
  LineDraft,
  LineTaxComponent,
  ProductOption,
  SourceOption,
  WarehouseOption
} from './editor-types';

export function text(value: unknown, fallback = ''): string {
  return value === undefined || value === null ? fallback : String(value);
}

export function today(): string {
  const value = new Date();
  const year = value.getFullYear();
  const month = String(value.getMonth() + 1).padStart(2, '0');
  const day = String(value.getDate()).padStart(2, '0');
  return `${year}-${month}-${day}`;
}

export function dateOnly(value: unknown, fallback = today()): string {
  const raw = text(value);
  return raw ? raw.slice(0, 10) : fallback;
}

export function optionFromID(id: unknown, title: string): { id: string; title: string } | null {
  const value = text(id);
  return value ? { id: value, title } : null;
}

export function emptyForm(): DocumentForm {
  return {
    documentNo: '',
    party: null,
    branchID: '',
    branchName: '',
    defaultWarehouse: null,
    documentDate: today(),
    dueDate: '',
    validUntil: '',
    currency: 'TRY',
    exchangeRate: '1',
    notes: '',
    sourceDocumentID: '',
    sourceDocument: null,
    sourceDocuments: [],
    sourceKind: 'DIRECT',
    reason: ''
  };
}

export function emptyLine(
  lineType: CommercialLineType = 'PRODUCT',
  defaultWarehouse: WarehouseOption | null = null
): LineDraft {
  return {
    lineType,
    product: null,
    variant: null,
    variants: [],
    variantLoading: false,
    variantError: '',
    warehouse: lineType === 'PRODUCT' ? (defaultWarehouse ?? null) : null,
    unitCode: 'ADET',
    quantity: '1',
    conversionFactor: '1',
    unitPrice: lineType === 'SERVICE' ? '0' : '',
    baseUnitPrice: lineType === 'SERVICE' ? '0' : '',
    discountRate: '',
    taxRate: '0',
    taxIncluded: false,
    taxComponents: [],
    description: lineType === 'SERVICE' ? 'Hizmet' : '',
    manualPrice: false
  };
}

export function sourceDocumentNumber(value: {
  document_no?: unknown;
  order_no?: unknown;
  receipt_no?: unknown;
  invoice_no?: unknown;
  return_no?: unknown;
}): string {
  return text(
    value.document_no ?? value.order_no ?? value.receipt_no ?? value.invoice_no ?? value.return_no
  ).trim();
}

export function variantTitleFromSnapshot(
  code: string,
  attributes: Record<string, unknown>
): string {
  const labels = Object.entries(attributes)
    .map(([name, value]) => {
      const rendered =
        typeof value === 'object' && value !== null
          ? text(
              (value as Record<string, unknown>).name ??
                (value as Record<string, unknown>).label ??
                (value as Record<string, unknown>).value ??
                (value as Record<string, unknown>).code
            )
          : text(value);
      return rendered ? `${name}: ${rendered}` : '';
    })
    .filter(Boolean);
  return labels.join(' · ') || code || 'Seçili varyant';
}

export function contextualDocumentStatusLabel(status: unknown, isSales: boolean): string {
  if (!isSales && text(status).toUpperCase() === 'PARTIALLY_FULFILLED') return 'Kısmi teslim';
  return documentStatusLabel(status);
}

/** The additional taxes a persisted line carries. The stored breakdown holds
 *  the KDV entry first (flagged `primary`); everything else is an ÖTV-style
 *  tax. Entries written before that flag existed are recognised by their KDV
 *  code, and an entry with neither code nor name is the old rate-only KDV
 *  snapshot - both are skipped so KDV is never counted twice. */
export function lineTaxComponents(line: DocumentLine): LineTaxComponent[] {
  const components: LineTaxComponent[] = [];
  for (const entry of line.tax_components_snapshot ?? []) {
    if (!entry || typeof entry !== 'object') continue;
    const record = entry as Record<string, unknown>;
    if (record.primary === true) continue;
    const code = text(record.code);
    const name = text(record.name) || code;
    if (!code && !name) continue;
    if (isVatCode(code) || isVatCode(name)) continue;
    const calculationType = text(record.calculation_type).toUpperCase();
    components.push({
      code,
      name,
      calculationType:
        calculationType === 'QUANTITY_BASED' || calculationType === 'FIXED_AMOUNT'
          ? calculationType
          : 'PERCENTAGE',
      rate: trimDecimalZeros(text(record.rate, '0')) || '0',
      includedInBase: record.included_in_base === true
    });
  }
  return components;
}

function isVatCode(value: string) {
  return value.toLocaleUpperCase('tr-TR').startsWith('KDV');
}

function lineTaxSnapshot(line: DocumentLine) {
  if (line.tax_snapshot && typeof line.tax_snapshot === 'object') return line.tax_snapshot;
  return (
    line.tax_components_snapshot?.find((component) => component && typeof component === 'object') ??
    {}
  );
}

export function productOption(line: DocumentLine): ProductOption | null {
  const id = text(line.product_id);
  if (!id) return null;
  const taxSnapshot = lineTaxSnapshot(line);
  return {
    id,
    title: text(
      line.product_name_snapshot ?? line.description_snapshot ?? line.description,
      'Ürün satırı'
    ),
    subtitle: text(line.product_code_snapshot),
    kind: text(line.line_type).toUpperCase() === 'SERVICE' ? 'SERVICE' : 'PHYSICAL',
    unit: text(line.unit_code, 'ADET'),
    taxRate: trimDecimalZeros(text(taxSnapshot.rate ?? line.tax_rate, '0')) || '0',
    taxIncluded:
      taxSnapshot.included === true || String(taxSnapshot.included).toLowerCase() === 'true',
    taxComponents: lineTaxComponents(line),
    variantsEnabled: Boolean(text(line.variant_id))
  };
}

/** Map a persisted document line to the editable draft shape. `resolveWarehouse`
 *  turns a warehouse id into the option the picker shows. */
export function lineFromRecord(
  line: DocumentLine,
  resolveWarehouse: (id: unknown, name?: string, code?: string) => WarehouseOption | null
): LineDraft {
  const lineType = text(line.line_type).toUpperCase() === 'SERVICE' ? 'SERVICE' : 'PRODUCT';
  const taxSnapshot = lineTaxSnapshot(line);
  const variantCode = text(line.variant_code_snapshot ?? line.variant_code);
  const variantAttributes = line.variant_attributes_snapshot ?? line.variant_attributes ?? {};
  const variantSnapshot = Boolean(variantCode) || Object.keys(variantAttributes).length > 0;
  return {
    id: text(line.id) || undefined,
    lineType,
    product: productOption(line),
    variant: text(line.variant_id)
      ? {
          id: text(line.variant_id),
          title: variantTitleFromSnapshot(variantCode, variantAttributes),
          subtitle: variantCode,
          meta: variantCode
        }
      : null,
    variants: [],
    variantLoading: false,
    variantError: '',
    variantSnapshot,
    warehouse:
      lineType === 'PRODUCT'
        ? resolveWarehouse(line.warehouse_id, line.warehouse_name, line.warehouse_code)
        : null,
    unitCode: text(line.unit_code, 'ADET'),
    quantity:
      trimDecimalZeros(
        text(line.quantity ?? line.ordered_quantity ?? line.accepted_quantity, '1')
      ) || '1',
    conversionFactor: trimDecimalZeros(text(line.conversion_factor, '1')) || '1',
    unitPrice: trimDecimalZeros(text(line.unit_price ?? line.unit_cost, '0')) || '0',
    baseUnitPrice: '',
    discountRate: discountRateFromAmounts(line.discount_amount, line.gross_amount),
    taxRate: trimDecimalZeros(text(taxSnapshot.rate ?? line.tax_rate, '0')) || '0',
    taxIncluded:
      taxSnapshot.included === true || String(taxSnapshot.included).toLowerCase() === 'true',
    taxComponents: lineTaxComponents(line),
    persistedTotal: trimDecimalZeros(text(line.line_total ?? line.payable_amount)) || undefined,
    description: text(line.description ?? line.description_snapshot ?? line.product_name_snapshot),
    orderedQuantity: text(line.ordered_quantity) || undefined,
    receivedQuantity: text(line.received_quantity ?? line.accepted_quantity) || undefined,
    invoicedQuantity: text(line.invoiced_quantity) || undefined,
    acceptedQuantity: text(line.accepted_quantity) || undefined,
    remainingFulfillmentQuantity: text(line.remaining_fulfillment_quantity) || undefined,
    remainingInvoicingQuantity: text(line.remaining_invoicing_quantity) || undefined,
    sourceLineID: text(line.source_line_id ?? line.source_receipt_line_id) || undefined,
    purchaseOrderLineID: text(line.purchase_order_line_id) || undefined,
    goodsReceiptLineID:
      text(line.goods_receipt_line_id ?? line.source_receipt_line_id) || undefined,
    manualPrice: false
  };
}

export type SourceRefContext = { direction: CommercialDirection; isSales: boolean };

export function sourceOptionFromReference(
  value: unknown,
  ctx: SourceRefContext,
  fallbackResource?: string,
  fallbackDocumentNo = ''
): SourceOption | null {
  const candidate = value && typeof value === 'object' ? (value as Record<string, unknown>) : {};
  const id = text(candidate.id).trim();
  if (!id) return null;
  const resource =
    commercialResourceFromReference(candidate) ?? commercialResource(fallbackResource);
  const documentNo = sourceDocumentNumber(candidate) || fallbackDocumentNo.trim();
  const status = text(candidate.lifecycle_status ?? candidate.status).toUpperCase();
  const subtitle = text(candidate.party_name ?? candidate.supplier_name);
  return {
    id,
    title: documentNo || 'Seçili kaynak belge',
    documentNo,
    subtitle,
    kind: resource,
    resource,
    href: commercialDocumentHref(ctx.direction, resource, id),
    relationType: text(candidate.relation_type),
    status,
    meta: status ? contextualDocumentStatusLabel(status, ctx.isSales) : undefined
  };
}

export function sourceReferences(next: DocumentRecord, ctx: SourceRefContext): SourceOption[] {
  const references: SourceOption[] = [];
  const seen = new Set<string>();
  const add = (reference: SourceOption | null) => {
    if (!reference || seen.has(reference.id)) return;
    seen.add(reference.id);
    references.push(reference);
  };

  for (const reference of next.source_documents ?? [])
    add(sourceOptionFromReference(reference, ctx));

  const sourceKindResource = commercialResourceFromReference({ kind: next.source_kind });
  const legacyIDs = [
    ...((next.source_document_ids ?? []).map((id) => ({ id, resource: sourceKindResource })) ?? []),
    { id: next.source_document_id, resource: sourceKindResource },
    { id: next.source_receipt_id, resource: 'dispatches' },
    { id: next.purchase_order_id, resource: 'orders' },
    { id: next.goods_receipt_id, resource: 'dispatches' },
    ...((next.goods_receipt_ids ?? []).map((id) => ({ id, resource: 'dispatches' })) ?? [])
  ];
  for (const [index, legacy] of legacyIDs.entries()) {
    add(
      sourceOptionFromReference(
        legacy,
        ctx,
        legacy.resource ?? undefined,
        index === 0 ? text(next.source_document_no ?? next.source_order_no) : ''
      )
    );
  }
  return references;
}

export function sourceReferenceLabel(source: SourceOption): string {
  return source.documentNo || source.title || 'Kaynak belge';
}
