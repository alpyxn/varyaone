import { describe, expect, it } from 'vitest';
import { findLineByBarcode, parseWedgeInput, zeroConfirmationText } from './scan-utils';
import type { CountLine } from './types';

const line: CountLine = {
  id: 'line-1',
  barcode: '8690001',
  product_name: 'Karton Kutu',
  expected_quantity: '12',
  counted_quantity: '3',
  difference: '-9'
};

describe('stock count scan helpers', () => {
  it('parses keyboard wedge Enter input and scanner quantity suffixes', () => {
    expect(parseWedgeInput('8690001\r\n')).toEqual({ barcode: '8690001' });
    expect(parseWedgeInput('8690001*2')).toEqual({ barcode: '8690001', quantity: '2' });
    expect(parseWedgeInput('8690001 x 1,5')).toEqual({ barcode: '8690001', quantity: '1.5' });
    expect(parseWedgeInput('')).toBeNull();
  });

  it('does not resolve unknown barcodes to a line', () => {
    expect(findLineByBarcode([line], 'unknown')).toBeUndefined();
    expect(zeroConfirmationText(line)).toContain('Karton Kutu');
    expect(zeroConfirmationText(line)).not.toContain('tüm');
  });
});
