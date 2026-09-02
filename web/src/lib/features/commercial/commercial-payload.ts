import { canonicalDecimal } from '$lib/design/decimal';
import { lineAmounts } from './commercial-calc';
import type { DocumentForm, LineDraft } from './editor-types';
import type { CommercialResource } from './types';

export type PayloadContext = {
  form: DocumentForm;
  lines: LineDraft[];
  isSales: boolean;
  isPurchaseOrder: boolean;
  resource: CommercialResource | undefined;
  currency: string;
};

function isoDate(value: string): string | undefined {
  return value ? `${value}T00:00:00Z` : undefined;
}

function salesLinePayload(line: LineDraft) {
  return {
    id: line.id,
    line_type: line.lineType,
    product_id: line.product?.id || undefined,
    variant_id: line.variant?.id || undefined,
    warehouse_id: line.lineType === 'PRODUCT' ? line.warehouse?.id || undefined : undefined,
    unit_code: line.unitCode,
    quantity: canonicalDecimal(line.quantity),
    conversion_factor: canonicalDecimal(line.conversionFactor || '1'),
    unit_price: canonicalDecimal(line.unitPrice),
    price_source: line.manualPrice ? 'MANUAL' : undefined,
    discount_rate: line.discountRate || undefined,
    tax_rate: line.taxRate || undefined,
    tax_included: line.taxIncluded || undefined,
    description: line.description,
    source_line_id: line.sourceLineID
  };
}

function purchaseOrderLinePayload(line: LineDraft, currency: string) {
  return {
    id: line.id,
    line_type: line.lineType,
    product_id: line.product?.id,
    variant_id: line.variant?.id,
    product_code_snapshot: line.product?.code || line.product?.subtitle || '',
    product_name_snapshot: line.product?.title || line.description || 'Ürün',
    unit_code: line.unitCode,
    warehouse_id: line.lineType === 'PRODUCT' ? line.warehouse?.id : undefined,
    ordered_quantity: canonicalDecimal(line.quantity),
    unit_price: canonicalDecimal(line.unitPrice || '0'),
    currency,
    tax_snapshot: line.taxRate ? { rate: line.taxRate, included: line.taxIncluded } : {}
  };
}

function purchaseLinePayload(
  line: LineDraft,
  resource: CommercialResource | undefined,
  currency: string
) {
  const amounts = lineAmounts(line);
  if (resource === 'dispatches') {
    return {
      id: line.id,
      product_id: line.product?.id,
      variant_id: line.variant?.id,
      purchase_order_line_id: line.purchaseOrderLineID,
      accepted_quantity: canonicalDecimal(line.quantity),
      damaged_quantity: '0',
      rejected_quantity: '0',
      unit_code: line.unitCode,
      warehouse_id: line.warehouse?.id,
      unit_cost: canonicalDecimal(line.unitPrice || '0'),
      currency,
      tax_snapshot: line.taxRate ? { rate: line.taxRate, included: line.taxIncluded } : {}
    };
  }
  if (resource === 'returns') {
    return {
      id: line.id,
      product_id: line.product?.id,
      variant_id: line.variant?.id,
      source_receipt_line_id: line.goodsReceiptLineID || line.sourceLineID,
      warehouse_id: line.warehouse?.id,
      quantity: canonicalDecimal(line.quantity),
      unit_code: line.unitCode,
      unit_cost: canonicalDecimal(line.unitPrice || '0'),
      currency,
      reason: line.description
    };
  }
  return {
    id: line.id,
    line_type: line.lineType,
    purchase_order_line_id: line.purchaseOrderLineID,
    goods_receipt_line_id: line.goodsReceiptLineID,
    product_id: line.product?.id,
    variant_id: line.variant?.id,
    warehouse_id: line.lineType === 'PRODUCT' ? line.warehouse?.id : undefined,
    unit_code: line.unitCode,
    description_snapshot: line.description || line.product?.title || 'Ürün',
    quantity: canonicalDecimal(line.quantity),
    unit_price: canonicalDecimal(line.unitPrice || '0'),
    gross_amount: amounts.gross,
    discount_amount: amounts.discount,
    tax_base: amounts.taxBase,
    tax_amount: amounts.tax,
    withholding_amount: '0',
    payable_amount: amounts.total,
    tax_components_snapshot: line.taxRate
      ? [{ rate: line.taxRate, included: line.taxIncluded }]
      : []
  };
}

/** Build the create/update request body for the current document kind. Pure:
 *  the same form + lines always produce the same payload. */
export function buildDocumentPayload(ctx: PayloadContext): Record<string, unknown> {
  const { form, lines, isSales, isPurchaseOrder, resource, currency } = ctx;
  const partyID = form.party?.id || '';

  if (isSales) {
    return {
      document_no: form.documentNo || undefined,
      branch_id: form.branchID,
      default_warehouse_id: form.defaultWarehouse?.id || undefined,
      party_id: partyID,
      document_date: isoDate(form.documentDate),
      due_date: isoDate(form.dueDate),
      valid_until: isoDate(form.validUntil),
      currency_code: currency,
      exchange_rate: form.exchangeRate || '1',
      notes: form.notes,
      reason: resource === 'returns' ? form.reason : undefined,
      source_kind: form.sourceDocument ? form.sourceKind : 'DIRECT',
      source_document_id: form.sourceDocumentID || undefined,
      lines: lines.map(salesLinePayload)
    };
  }
  if (isPurchaseOrder) {
    return {
      order_no: form.documentNo || undefined,
      supplier_id: partyID,
      branch_id: form.branchID,
      warehouse_id: form.defaultWarehouse?.id || '',
      order_date: isoDate(form.documentDate),
      currency,
      over_delivery_policy: 'WARN',
      notes: form.notes,
      lines: lines.map((line) => purchaseOrderLinePayload(line, currency))
    };
  }
  if (resource === 'dispatches') {
    return {
      receipt_no: form.documentNo || undefined,
      purchase_order_id: form.sourceDocument?.kind === 'orders' ? form.sourceDocumentID : undefined,
      supplier_id: partyID,
      branch_id: form.branchID,
      warehouse_id: form.defaultWarehouse?.id || '',
      receipt_date: isoDate(form.documentDate),
      currency,
      notes: form.notes,
      lines: lines.map((line) => purchaseLinePayload(line, resource, currency))
    };
  }
  if (resource === 'returns') {
    return {
      return_no: form.documentNo || undefined,
      supplier_id: partyID,
      branch_id: form.branchID,
      warehouse_id: form.defaultWarehouse?.id || '',
      source_receipt_id:
        form.sourceDocument?.kind === 'dispatches' ? form.sourceDocumentID : undefined,
      return_date: isoDate(form.documentDate),
      currency,
      reason: form.reason,
      lines: lines.map((line) => purchaseLinePayload(line, resource, currency))
    };
  }
  return {
    invoice_no: form.documentNo || undefined,
    supplier_id: partyID,
    branch_id: form.branchID,
    warehouse_id: form.defaultWarehouse?.id || '',
    purchase_order_id: form.sourceDocument?.kind === 'orders' ? form.sourceDocumentID : undefined,
    goods_receipt_id:
      form.sourceDocument?.kind === 'dispatches' ? form.sourceDocumentID : undefined,
    standalone: !form.sourceDocumentID,
    invoice_date: isoDate(form.documentDate),
    due_date: isoDate(form.dueDate),
    currency,
    lines: lines.map((line) => purchaseLinePayload(line, resource, currency))
  };
}
