import { describe, it, expect } from 'vitest';
import { buildDocumentPayload, type PayloadContext } from './commercial-payload';
import type { DocumentForm, LineDraft } from './editor-types';

function form(overrides: Partial<DocumentForm> = {}): DocumentForm {
  return {
    documentNo: '',
    party: { id: 'party-1', title: 'Cari' },
    branchID: 'branch-1',
    branchName: 'Merkez',
    defaultWarehouse: { id: 'wh-1', title: 'Depo' },
    documentDate: '2026-08-29',
    dueDate: '',
    validUntil: '',
    currency: 'TRY',
    exchangeRate: '1',
    notes: '',
    sourceDocumentID: '',
    sourceDocument: null,
    sourceDocuments: [],
    sourceKind: 'DIRECT',
    reason: '',
    ...overrides
  };
}

function line(overrides: Partial<LineDraft> = {}): LineDraft {
  return {
    lineType: 'PRODUCT',
    product: { id: 'prod-1', title: 'Ürün' },
    variant: null,
    variants: [],
    variantLoading: false,
    variantError: '',
    warehouse: { id: 'wh-1', title: 'Depo' },
    unitCode: 'ADET',
    quantity: '2',
    conversionFactor: '1',
    unitPrice: '100',
    baseUnitPrice: '100',
    discountRate: '',
    taxRate: '20',
    taxIncluded: false,
    taxComponents: [],
    description: 'Ürün',
    manualPrice: false,
    ...overrides
  };
}

const ctx = (o: Partial<PayloadContext>): PayloadContext => ({
  form: form(),
  lines: [line()],
  isSales: false,
  isPurchaseOrder: false,
  resource: 'invoices',
  currency: 'TRY',
  ...o
});

describe('buildDocumentPayload', () => {
  it('builds a sales document with ISO dates and rate-based line discount', () => {
    const p = buildDocumentPayload(ctx({ isSales: true, resource: 'quotes' })) as Record<
      string,
      any
    >;
    expect(p.document_date).toBe('2026-08-29T00:00:00Z');
    expect(p.currency_code).toBe('TRY');
    expect(p.source_kind).toBe('DIRECT');
    expect(p.lines[0]).toMatchObject({
      product_id: 'prod-1',
      quantity: '2',
      unit_price: '100',
      tax_rate: '20'
    });
  });

  it('omits reason unless the sales document is a return', () => {
    expect(
      (buildDocumentPayload(ctx({ isSales: true, resource: 'quotes' })) as any).reason
    ).toBeUndefined();
    const ret = buildDocumentPayload(
      ctx({ isSales: true, resource: 'returns', form: form({ reason: 'Hasarlı' }) })
    ) as any;
    expect(ret.reason).toBe('Hasarlı');
  });

  it('builds a purchase order line with server-computed net amount', () => {
    const p = buildDocumentPayload(ctx({ isPurchaseOrder: true, resource: 'orders' })) as any;
    expect(p.over_delivery_policy).toBe('WARN');
    expect(p.lines[0]).toMatchObject({ ordered_quantity: '2', unit_price: '100', currency: 'TRY' });
  });

  it('builds a purchase dispatch line as accepted_quantity with zero damage/reject', () => {
    const p = buildDocumentPayload(ctx({ resource: 'dispatches' })) as any;
    expect(p.lines[0]).toMatchObject({
      accepted_quantity: '2',
      damaged_quantity: '0',
      rejected_quantity: '0'
    });
  });

  it('builds a purchase invoice line with a full money breakdown', () => {
    const p = buildDocumentPayload(ctx({ resource: 'invoices' })) as any;
    expect(p.standalone).toBe(true);
    expect(p.lines[0]).toMatchObject({
      quantity: '2',
      gross_amount: '200',
      tax_base: '200',
      tax_amount: '40',
      payable_amount: '240'
    });
  });

  it('wires a dispatches source into purchase_order_id / goods_receipt_id by kind', () => {
    const withOrder = buildDocumentPayload(
      ctx({
        resource: 'invoices',
        form: form({
          sourceDocumentID: 'src-1',
          sourceDocument: { id: 'src-1', title: 'SIP', kind: 'orders' }
        })
      })
    ) as any;
    expect(withOrder.purchase_order_id).toBe('src-1');
    expect(withOrder.goods_receipt_id).toBeUndefined();
    expect(withOrder.standalone).toBe(false);
  });
});
