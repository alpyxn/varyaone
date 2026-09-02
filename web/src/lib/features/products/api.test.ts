import { describe, expect, it, vi } from 'vitest';

const { apiMock } = vi.hoisted(() => ({ apiMock: vi.fn() }));
vi.mock('$lib/api', () => ({ api: apiMock }));

import {
  toVariantBarcodeUpdateWire,
  toVariantInputWire,
  updateProductVariantBarcodes,
  type ProductVariantUpdate
} from './api';

describe('variant update serializer', () => {
  it('serializes the atomic barcode update without response-only fields', () => {
    expect(
      toVariantBarcodeUpdateWire({
        barcodes: [
          {
            id: 'barcode-id',
            barcode: '869000000001',
            barcode_type: 'EAN',
            is_primary: true
          }
        ]
      })
    ).toEqual({
      barcodes: [{ barcode: '869000000001', barcode_type: 'EAN', is_primary: true }]
    });
  });

  it('calls the dedicated barcode endpoint with optimistic locking', async () => {
    apiMock.mockResolvedValueOnce({ id: 'variant-id', version: 8, barcodes: [] });

    await updateProductVariantBarcodes('product/id', 'variant/id', 7, {
      barcodes: [{ barcode: '869000000001', barcode_type: 'EAN', is_primary: true }]
    });

    expect(apiMock).toHaveBeenCalledWith(
      '/products/product%2Fid/variants/variant%2Fid/barcodes',
      expect.objectContaining({
        method: 'PUT',
        headers: { 'If-Match': '"7"' },
        body: JSON.stringify({
          barcodes: [{ barcode: '869000000001', barcode_type: 'EAN', is_primary: true }]
        })
      })
    );
  });

  it('serializes the user payload into the strict VariantInput shape', () => {
    const input = {
      variant_code: 'TSH-BLK-M',
      barcodes: [
        {
          id: 'barcode-id',
          variant_id: 'variant-id',
          barcode: '869000000001',
          barcode_type: 'EAN',
          is_primary: true
        }
      ],
      is_active: true,
      purchase_price_override: '1250.50',
      sales_price_override: '2000.00',
      price_entries: [
        {
          id: 'response-only-entry-id',
          price_list_id: 'price-list-id',
          entry_id: 'entry-id',
          unit_price: '1750.00',
          valid_from: '2026-01-01',
          valid_to: null,
          version: 3,
          created_at: '2026-01-01T00:00:00Z'
        }
      ]
    } as unknown as ProductVariantUpdate;

    const serialized = toVariantInputWire(input);

    expect(serialized).toEqual({
      variant_code: 'TSH-BLK-M',
      barcodes: [{ barcode: '869000000001', barcode_type: 'EAN', is_primary: true }],
      is_active: true,
      purchase_price_override: '1250.50',
      sales_price_override: '2000.00',
      price_entries: [
        {
          price_list_id: 'price-list-id',
          entry_id: 'entry-id',
          unit_price: '1750.00',
          valid_from: '2026-01-01',
          valid_to: null,
          version: 3
        }
      ]
    });
    expect(JSON.stringify(serialized)).not.toContain('undefined');
  });

  it('keeps blank overrides as empty strings so the backend restores inheritance', () => {
    const input: ProductVariantUpdate = {
      variant_code: 'TSH-BLK-M',
      purchase_price_override: '',
      sales_price_override: ''
    };

    expect(toVariantInputWire(input)).toEqual({
      variant_code: 'TSH-BLK-M',
      purchase_price_override: '',
      sales_price_override: ''
    });
  });

  it('does not mutate the caller input', () => {
    const input: ProductVariantUpdate = {
      variant_code: 'TSH-BLK-M',
      barcodes: [{ barcode: '869000000001', barcode_type: 'EAN', is_primary: true }],
      price_entries: [{ price_list_id: 'price-list-id', unit_price: '100' }]
    };
    const before = structuredClone(input);

    toVariantInputWire(input);

    expect(input).toEqual(before);
  });
});
