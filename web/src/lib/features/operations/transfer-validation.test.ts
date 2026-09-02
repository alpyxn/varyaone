import { describe, expect, it } from 'vitest';
import {
  buildTransferApiLines,
  validateTransferQuantities,
  type TransferQuantityLine
} from './transfer-validation';

function line(overrides: Partial<TransferQuantityLine> = {}): TransferQuantityLine {
  return {
    productID: 'product-1',
    variantID: '',
    variantRequired: false,
    quantity: '1',
    availableQuantity: '10',
    variants: [],
    loading: false,
    error: '',
    ...overrides
  };
}

describe('transfer quantity validation', () => {
  it('allows a quantity equal to the current available balance', () => {
    expect(validateTransferQuantities([line({ quantity: '10,00000000' })])).toBeUndefined();
  });

  it('checks repeated lines against one position total', () => {
    const result = validateTransferQuantities([line({ quantity: '6' }), line({ quantity: '5' })]);

    expect(result).toContain('2. satırdaki toplam miktar');
  });

  it('keeps different variants as separate positions', () => {
    expect(
      validateTransferQuantities([
        line({ variantID: 'variant-a', quantity: '10' }),
        line({ variantID: 'variant-b', quantity: '10' })
      ])
    ).toBeUndefined();
  });

  it('requires a variant when the product is variant-enabled', () => {
    expect(validateTransferQuantities([line({ variantRequired: true })])).toContain(
      '1. satır için varyant seçin.'
    );
  });

  it('builds one API line per positive variant quantity', () => {
    expect(
      buildTransferApiLines([
        {
          productID: 'product-variant',
          variantRequired: true,
          variantQuantities: { 'variant-red': '2,50', 'variant-blue': '0', '': '4' }
        }
      ])
    ).toEqual([{ product_id: 'product-variant', variant_id: 'variant-red', quantity: '2.50' }]);
  });

  it('omits variant_id for a non-variant product', () => {
    expect(
      buildTransferApiLines([
        { productID: 'product-simple', variantRequired: false, quantity: '3,000' }
      ])
    ).toEqual([{ product_id: 'product-simple', quantity: '3.000' }]);
  });
});
