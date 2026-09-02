import { describe, it, expect } from 'vitest';
import {
  buildCommercialDocument,
  commercialDisclaimer,
  resolveCommercialStamp
} from './commercial-print';
import { commercialConfig } from './types';
import { printDocument, VARYA_ONE_FOOTER_TEXT } from '$lib/design/print';
import type { DocumentRecord, LineDraft } from './editor-types';

const config = (direction: 'sales' | 'purchases', resource: string) => {
  const value = commercialConfig(direction, resource);
  if (!value) throw new Error(`no config for ${direction}/${resource}`);
  return value;
};

const line = (over: Partial<LineDraft> = {}): LineDraft =>
  ({
    lineType: 'PRODUCT',
    product: { id: 'p1', title: 'Vidalı Bağlantı <A>' },
    variant: null,
    variants: [],
    variantLoading: false,
    variantError: '',
    warehouse: { id: 'w1', title: 'Merkez Depo' },
    unitCode: 'ADET',
    quantity: '2',
    conversionFactor: '1',
    unitPrice: '100',
    baseUnitPrice: '100',
    discountRate: '10',
    taxRate: '20',
    taxIncluded: false,
    description: '',
    manualPrice: false,
    ...over
  }) as LineDraft;

const totals = { subtotal: '200', discount: '20', tax: '36', grand: '216' };

describe('resolveCommercialStamp', () => {
  it('prefers İPTAL over everything', () => {
    const stamp = resolveCommercialStamp(config('sales', 'invoices'), {
      lifecycle_status: 'CANCELLED',
      payment_status: 'PAID'
    } as DocumentRecord);
    expect(stamp).toEqual({ label: 'İPTAL', tone: 'danger' });
  });

  it('marks drafts as TASLAK', () => {
    expect(
      resolveCommercialStamp(config('sales', 'quotes'), {
        lifecycle_status: 'DRAFT'
      } as DocumentRecord).label
    ).toBe('TASLAK');
  });

  it('marks a paid invoice as ÖDENDİ', () => {
    expect(
      resolveCommercialStamp(config('sales', 'invoices'), {
        lifecycle_status: 'FINALIZED',
        payment_status: 'PAID'
      } as DocumentRecord).label
    ).toBe('ÖDENDİ');
  });

  it('uses the document type otherwise', () => {
    const cases: Array<[string, string]> = [
      ['quotes', 'TEKLİF'],
      ['orders', 'SİPARİŞ'],
      ['dispatches', 'İRSALİYE'],
      ['invoices', 'FATURA'],
      ['returns', 'İADE']
    ];
    for (const [resource, label] of cases) {
      expect(
        resolveCommercialStamp(config('sales', resource), {
          lifecycle_status: 'OPEN'
        } as DocumentRecord).label
      ).toBe(label);
    }
  });
});

describe('buildCommercialDocument', () => {
  const record: DocumentRecord = {
    id: 'doc-1',
    lifecycle_status: 'FINALIZED',
    document_no: 'SF-2026-000042',
    document_date: '2026-09-01',
    due_date: '2026-09-30',
    currency_code: 'TRY',
    exchange_rate: '1',
    warehouse_name: 'Merkez Depo',
    party_name: 'ACME & Ortak',
    notes: 'Teslimat kapıda.'
  };

  const built = buildCommercialDocument({
    config: config('sales', 'invoices'),
    record,
    lines: [line()],
    currency: 'TRY',
    totals,
    company: { trade_name: 'Bizim Şirket', logo: '', tax_number: '1234567890' } as never,
    party: {
      id: 'party-1',
      code: 'M-001',
      tax_number: '9998887776',
      trade_name: 'ACME & Ortak'
    } as never
  });

  it('titles the document after the resource and stamps it FATURA', () => {
    expect(built.title).toBe('Satış Faturası');
    expect(built.stamp?.label).toBe('FATURA');
  });

  it('carries the meta grid and recipient', () => {
    expect(built.meta?.find((m) => m.label === 'Belge No')?.value).toBe('SF-2026-000042');
    expect(built.meta?.find((m) => m.label === 'Vade')?.value).toBe('30.09.2026');
    expect(built.recipient?.name).toBe('ACME & Ortak');
    expect(built.recipient?.taxNumber).toBe('9998887776');
  });

  it('escapes user-controlled text in the body', () => {
    expect(built.bodyHtml).toContain('Vidalı Bağlantı &lt;A&gt;');
    expect(built.bodyHtml).not.toContain('<A>');
    expect(built.bodyHtml).toContain('Teslimat kapıda.');
  });

  it('carries a legal disclaimer as the footer note', () => {
    expect(built.footerNote).toContain('resmi bir mali belge değildir');
    expect(commercialDisclaimer(config('sales', 'dispatches'))).toContain(
      'sevk irsaliyesi veya e-İrsaliye yerine geçmez'
    );
    expect(commercialDisclaimer(config('sales', 'invoices'))).toContain(
      'fatura, e-Fatura veya e-Arşiv'
    );
  });

  it('renders totals including the grand total', () => {
    expect(built.bodyHtml).toContain('Genel Toplam');
    expect(built.bodyHtml).toContain('216');
  });

  it('feeds a document that renders the permanent Varya One footer', () => {
    const opened: string[] = [];
    const originalOpen = window.open;
    window.open = (() => ({
      document: { open() {}, close() {}, write: (html: string) => opened.push(html) },
      focus() {},
      setTimeout() {},
      print() {}
    })) as unknown as typeof window.open;
    try {
      printDocument(built);
    } finally {
      window.open = originalOpen;
    }
    expect(opened[0]).toContain(VARYA_ONE_FOOTER_TEXT);
    expect(opened[0]).toContain('<svg');
    expect(opened[0]).toContain('class="doc-stamp"');
  });
});
