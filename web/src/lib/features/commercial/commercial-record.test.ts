import { describe, it, expect } from 'vitest';
import {
  lineFromRecord,
  sourceReferences,
  sourceOptionFromReference,
  variantTitleFromSnapshot,
  contextualDocumentStatusLabel,
  dateOnly
} from './commercial-record';
import type { DocumentLine, DocumentRecord, WarehouseOption } from './editor-types';

const ctx = { direction: 'purchases' as const, isSales: false };
const salesCtx = { direction: 'sales' as const, isSales: true };
const resolveWh = (id: unknown): WarehouseOption | null =>
  id ? { id: String(id), title: 'Depo ' + id } : null;

describe('lineFromRecord', () => {
  it('reconstructs a percentage discount from persisted money amounts', () => {
    const line: DocumentLine = {
      id: 'l1',
      line_type: 'PRODUCT',
      product_id: 'p1',
      product_name_snapshot: 'Ürün',
      warehouse_id: 'wh1',
      quantity: '10.00000000',
      unit_price: '100.00000000',
      gross_amount: '1000',
      discount_amount: '100',
      tax_rate: '20'
    };
    const draft = lineFromRecord(line, resolveWh);
    expect(draft.discountRate).toBe('10');
    expect(draft.quantity).toBe('10');
    expect(draft.unitPrice).toBe('100');
    expect(draft.warehouse?.id).toBe('wh1');
    expect(draft.taxRate).toBe('20');
  });

  it('leaves discount empty when there was none', () => {
    const draft = lineFromRecord(
      { line_type: 'SERVICE', product_id: 'p2', quantity: '1', unit_price: '50' },
      resolveWh
    );
    expect(draft.discountRate).toBe('');
    expect(draft.lineType).toBe('SERVICE');
    expect(draft.warehouse).toBeNull();
  });

  it('reads a variant from the snapshot without a live fetch', () => {
    const draft = lineFromRecord(
      {
        line_type: 'PRODUCT',
        product_id: 'p3',
        variant_id: 'v3',
        variant_code_snapshot: 'RED-L',
        quantity: '1',
        unit_price: '10'
      },
      resolveWh
    );
    expect(draft.variant?.id).toBe('v3');
    expect(draft.variantSnapshot).toBe(true);
  });
});

describe('sourceReferences', () => {
  it('dedupes typed source_documents and legacy id fields', () => {
    const record: DocumentRecord = {
      id: 'doc1',
      source_documents: [
        {
          id: 'src-order',
          document_no: 'SIP-1',
          document_type_code: 'PURCHASE_ORDER',
          lifecycle_status: 'OPEN'
        }
      ],
      purchase_order_id: 'src-order',
      goods_receipt_id: 'src-gr'
    };
    const refs = sourceReferences(record, ctx);
    expect(refs.map((r) => r.id).sort()).toEqual(['src-gr', 'src-order']);
    expect(refs.find((r) => r.id === 'src-order')?.documentNo).toBe('SIP-1');
  });

  it('returns nothing for a document with no source', () => {
    expect(sourceReferences({ id: 'x' }, ctx)).toEqual([]);
  });
});

describe('helpers', () => {
  it('sourceOptionFromReference needs an id', () => {
    expect(sourceOptionFromReference({}, ctx)).toBeNull();
    expect(sourceOptionFromReference({ id: 'a', document_no: 'B' }, ctx)?.title).toBe('B');
  });

  it('variantTitleFromSnapshot joins attributes then falls back to code', () => {
    expect(variantTitleFromSnapshot('', { Renk: 'Kırmızı', Beden: 'L' })).toBe(
      'Renk: Kırmızı · Beden: L'
    );
    expect(variantTitleFromSnapshot('RED-L', {})).toBe('RED-L');
  });

  it('contextualDocumentStatusLabel renames PARTIALLY_FULFILLED for purchasing only', () => {
    expect(contextualDocumentStatusLabel('PARTIALLY_FULFILLED', false)).toBe('Kısmi teslim');
    expect(contextualDocumentStatusLabel('PARTIALLY_FULFILLED', true)).toBe('Kısmi karşılandı');
  });

  it('dateOnly slices an ISO timestamp and honours the fallback', () => {
    expect(dateOnly('2026-08-29T10:00:00Z')).toBe('2026-08-29');
    expect(dateOnly('', '')).toBe('');
  });
});
