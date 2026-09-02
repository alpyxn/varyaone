import { describe, expect, it, vi } from 'vitest';

const { apiMock } = vi.hoisted(() => ({ apiMock: vi.fn() }));
vi.mock('$lib/api', () => ({ api: apiMock }));

import {
  buildImportMapping,
  commitImport,
  getImportCapabilities,
  isUploadSizeAllowed,
  selectableImportEntities,
  type ImportCapabilities
} from './api';

const capabilities: ImportCapabilities = {
  max_upload_bytes: 64 * 1024 * 1024,
  entities: [
    {
      type: 'PRODUCT',
      label: 'Ürünler',
      importable: true,
      exportable: true,
      fields: []
    },
    {
      type: 'VARIANT',
      label: 'Varyantlar',
      importable: true,
      exportable: true,
      fields: []
    },
    {
      type: 'BARCODE',
      label: 'Barkodlar',
      importable: true,
      exportable: true,
      fields: []
    },
    {
      type: 'PARTY',
      label: 'Cariler',
      importable: false,
      exportable: true,
      fields: []
    },
    {
      type: 'OPENING_STOCK',
      label: 'Açılış stoku',
      importable: true,
      exportable: true,
      fields: []
    }
  ]
};

describe('data exchange capabilities', () => {
  it('hides technical variant and standalone barcode imports while keeping exports', () => {
    expect(selectableImportEntities(capabilities, 'IMPORT').map((entity) => entity.type)).toEqual([
      'PRODUCT'
    ]);
    expect(selectableImportEntities(capabilities, 'EXPORT').map((entity) => entity.type)).toEqual([
      'PRODUCT',
      'VARIANT',
      'BARCODE',
      'PARTY'
    ]);
  });

  it('sends stable field IDs as mapping values, never display labels', () => {
    expect(
      buildImportMapping([
        { sourceColumn: 'Stok Kodu', targetField: 'product_code' },
        { sourceColumn: 'Stok Adı', targetField: 'product_name' },
        { sourceColumn: 'Alış Fiyatı', targetField: 'purchase_price' }
      ])
    ).toEqual({
      product_code: 'Stok Kodu',
      product_name: 'Stok Adı',
      purchase_price: 'Alış Fiyatı'
    });
  });

  it('uses the server-provided upload limit at the boundary', () => {
    expect(isUploadSizeAllowed(64 * 1024 * 1024, capabilities.max_upload_bytes)).toBe(true);
    expect(isUploadSizeAllowed(64 * 1024 * 1024 + 1, capabilities.max_upload_bytes)).toBe(false);
    expect(isUploadSizeAllowed(1, 0)).toBe(false);
    expect(isUploadSizeAllowed(-1, capabilities.max_upload_bytes)).toBe(false);
  });

  it('loads capabilities through the capability endpoint', async () => {
    apiMock.mockResolvedValueOnce(capabilities);

    await expect(getImportCapabilities()).resolves.toBe(capabilities);
    expect(apiMock).toHaveBeenCalledWith('/imports/capabilities');
  });

  it('sends the preview revision with commit', async () => {
    apiMock.mockResolvedValueOnce({ status: 'COMMITTED' });

    await commitImport('job-1', false, 'idem-1', 'revision-1');

    expect(apiMock).toHaveBeenCalledWith('/imports/job-1/commit', {
      method: 'POST',
      headers: { 'Idempotency-Key': 'idem-1' },
      body: JSON.stringify({ dry_run: false, analysis_revision: 'revision-1' })
    });
  });
});
