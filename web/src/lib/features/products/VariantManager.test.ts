import { describe, expect, it, vi } from 'vitest';

vi.mock('@lucide/svelte', () => ({
  AlertTriangle: () => {},
  Check: () => {},
  ChevronDown: () => {},
  LockKeyhole: () => {},
  Plus: () => {},
  Save: () => {},
  Trash2: () => {}
}));

import {
  compactDecimal,
  changedVariantIDs,
  normalizeVariantBarcodes,
  variantBarcodeErrorMessage,
  validateVariantBarcodes,
  variantBarcodeSnapshot
} from './VariantManager.svelte';

describe('compactDecimal', () => {
  it.each([
    ['', ''],
    ['250.00000000', '250'],
    ['250.12000000', '250.12'],
    ['0.00000001', '0.00000001']
  ])('formats %j as %j without numeric conversion', (input, expected) => {
    const result = compactDecimal(input);

    expect(result).toBe(expected);
    expect(typeof result).toBe('string');
  });
});

describe('variant barcode editor helpers', () => {
  it('detects server-refreshed variants whose local drafts are no longer current', () => {
    expect(
      changedVariantIDs(
        [
          { id: 'variant-1', version: 3 },
          { id: 'variant-2', version: 4 }
        ],
        [
          { id: 'variant-1', version: 4 },
          { id: 'variant-2', version: 4 }
        ]
      )
    ).toEqual(['variant-1']);
  });

  it('only uses the company-duplicate message for duplicate conflict codes', () => {
    expect(variantBarcodeErrorMessage('VARIANT_BARCODE_DUPLICATE')).toContain('başka bir ürün');
    expect(variantBarcodeErrorMessage('VARIANT_BARCODE_LIST_DUPLICATE')).toBe(
      'Aynı varyantta aynı barkod birden fazla kullanılamaz.'
    );
    expect(
      variantBarcodeErrorMessage(
        'VARIANT_BARCODE_DUPLICATE',
        'Barkod "55d" başka bir ürün veya varyantta kullanılıyor.'
      )
    ).toContain('55d');
    expect(
      variantBarcodeErrorMessage(
        'VALIDATION_ERROR',
        'Aynı varyant barkodu listede birden fazla kullanılamaz'
      )
    ).toBeUndefined();
  });

  it('validates and normalizes a complete multi-barcode draft', () => {
    expect(
      validateVariantBarcodes([
        { barcode: ' 869000000001 ', barcode_type: 'ean', is_primary: true },
        { barcode: '869000000002', barcode_type: 'CODE128', is_primary: false }
      ])
    ).toBeUndefined();
    expect(
      normalizeVariantBarcodes([
        { id: 'barcode-1', barcode: ' 869000000001 ', barcode_type: 'ean', is_primary: true }
      ])
    ).toEqual([
      { id: 'barcode-1', barcode: '869000000001', barcode_type: 'EAN', is_primary: true }
    ]);
  });

  it('uses an order- and id-independent barcode snapshot for dirty checks', () => {
    expect(
      variantBarcodeSnapshot([
        { id: 'second', barcode: 'B-2', barcode_type: 'EAN', is_primary: false },
        { id: 'first', barcode: 'A-1', barcode_type: 'ean', is_primary: true }
      ])
    ).toBe(
      variantBarcodeSnapshot([
        { id: 'new-a', barcode: 'A-1', barcode_type: 'EAN', is_primary: true },
        { id: 'new-b', barcode: 'B-2', barcode_type: 'EAN', is_primary: false }
      ])
    );
  });

  it.each([
    ['Barkod değeri boş bırakılamaz.', [{ barcode: '', barcode_type: 'EAN', is_primary: true }]],
    [
      'Aynı varyantta aynı barkod birden fazla kullanılamaz.',
      [
        { barcode: '869000000001', barcode_type: 'EAN', is_primary: true },
        { barcode: '869000000001', barcode_type: 'UPC', is_primary: false }
      ]
    ],
    [
      'Bir barkodu ana olarak seçin.',
      [{ barcode: '869000000001', barcode_type: 'EAN', is_primary: false }]
    ]
  ])('reports barcode validation error %j', (message, barcodes) => {
    expect(validateVariantBarcodes(barcodes)).toBe(message);
  });
});
