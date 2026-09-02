import { describe, expect, it } from 'vitest';
import {
  commercialConfig,
  commercialPath,
  commercialResource,
  commercialStatusOptions,
  documentStatusLabel,
  documentStatusTone
} from './types';

describe('typed commercial screen metadata', () => {
  it('keeps sales and purchasing resources on their typed API contracts', () => {
    expect(commercialConfig('sales', 'invoices')).toMatchObject({
      endpoint: '/sales/invoices',
      permission: 'sales.invoice.read'
    });
    expect(commercialConfig('purchases', 'dispatches')).toMatchObject({
      endpoint: '/purchases/dispatches',
      title: 'Alış İrsaliyesi'
    });
    expect(commercialConfig('purchases', 'quotes')).toBeUndefined();
  });

  it('normalizes Turkish route slugs before resolving the typed screen', () => {
    expect(commercialResource('irsaliyeler')).toBe('dispatches');
    expect(commercialResource('faturalar')).toBe('invoices');
    expect(commercialConfig('sales', 'irsaliyeler')).toMatchObject({
      endpoint: '/sales/dispatches',
      title: 'Satış İrsaliyesi'
    });
    expect(commercialConfig('purchases', 'iadeler')).toMatchObject({
      endpoint: '/purchases/returns',
      title: 'Alış İadesi'
    });
    expect(commercialPath('sales', 'irsaliyeler')).toBe('/satis/irsaliyeler');
    expect(commercialPath('purchases', 'faturalar')).toBe('/alis/faturalar');
    expect(commercialResource('bilinmeyen')).toBeUndefined();
  });

  it('maps internal workflow states to Turkish ERP language', () => {
    expect(documentStatusLabel('PARTIALLY_FULFILLED')).toBe('Kısmi karşılandı');
    expect(documentStatusTone('CANCELLED')).toBe('danger');
    expect(documentStatusLabel('UNKNOWN_STATE')).toBe('Bilinmiyor');
  });

  it('exposes document-specific status axes instead of one overloaded filter', () => {
    expect(commercialStatusOptions('orders', 'lifecycle_status').map((item) => item.value)).toEqual(
      ['DRAFT', 'OPEN', 'CANCELLED']
    );
    expect(commercialStatusOptions('invoices', 'payment_status').map((item) => item.label)).toEqual(
      ['Ödenmedi', 'Kısmi ödendi', 'Ödendi']
    );
    expect(commercialStatusOptions('quotes', 'payment_status')).toEqual([]);
  });
});
