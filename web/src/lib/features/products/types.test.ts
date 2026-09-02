import { describe, expect, it } from 'vitest';
import { emptyProduct, normalizeProductInput, validateProductInput } from './types';

describe('product code flow', () => {
  it('accepts a blank code for a new product', () => {
    const input = emptyProduct();
    input.name = 'Kod Sonra Verilecek';

    expect(validateProductInput(input)).toBeUndefined();
    expect(normalizeProductInput(input).code).toBe('');
  });

  it('keeps an explicitly entered code', () => {
    const input = emptyProduct();
    input.name = 'Elle Kodlanan Ürün';
    input.code = ' urun-7 ';

    expect(validateProductInput(input)).toBeUndefined();
    expect(normalizeProductInput(input).code).toBe('URUN-7');
  });

  it('requires the existing code during an update', () => {
    const input = emptyProduct();
    input.name = 'Mevcut Ürün';

    expect(validateProductInput(input, { allowBlankCode: false })).toBe(
      'Mevcut stok kartında stok kodu boş bırakılamaz.'
    );
  });

  it('allows only one stock unit', () => {
    const input = emptyProduct();
    input.name = 'Tek Birimli Ürün';
    input.units.push({
      code: 'KOLI',
      is_base: false,
      conversion_factor: '12',
      decimal_scale: 0
    });

    expect(validateProductInput(input)).toBe(
      'Bir stok kartında yalnızca bir stok birimi kullanılabilir.'
    );
  });

  it('normalizes product prices and keeps custom feature descriptions', () => {
    const input = emptyProduct();
    input.name = 'Fiyatlı Ürün';
    input.purchase_price = ' 1.250,50 ';
    input.sales_price = '2.000,00';
    input.custom_description_1 = '  Teknik özellik  ';
    input.custom_description_2 = 'Renk: Siyah';
    input.custom_description_3 = 'Garanti: 2 yıl';

    const normalized = normalizeProductInput(input);

    expect(normalized.purchase_price).toBe('1250.5');
    expect(normalized.sales_price).toBe('2000');
    expect(normalized.custom_description_1).toBe('Teknik özellik');
    expect(validateProductInput(normalized)).toBeUndefined();
  });

  it('preserves zeros in the integer part of product prices and rates', () => {
    const input = emptyProduct();
    input.name = 'Tam sayı fiyatı';
    input.purchase_price = '600';
    input.sales_price = '6000';
    input.purchase_tax_rate = '20';
    input.sales_tax_rate = '10';

    const normalized = normalizeProductInput(input);

    expect(normalized.purchase_price).toBe('600');
    expect(normalized.sales_price).toBe('6000');
    expect(normalized.purchase_tax_rate).toBe('20');
    expect(normalized.sales_tax_rate).toBe('10');
  });

  it('strips response-only fields before sending an update', () => {
    const input = emptyProduct();
    input.name = 'Yanıt alanları temizlenen ürün';
    input.sales_tax_profile = Object.assign(
      {
        treatment: 'STANDARD' as const,
        tax_rate_id: 'rate-id',
        components: [
          Object.assign(
            {
              tax_definition_id: 'definition-id',
              tax_rate_id: 'component-rate-id',
              calculation_type: 'PERCENTAGE' as const
            },
            { tax_definition_code: 'OTV', tax_definition_name: 'ÖTV' }
          )
        ]
      },
      { direction: 'SALES' as const, version: 3 }
    );
    input.barcodes = [
      Object.assign(
        { barcode: '869000000001', barcode_type: 'EAN', is_primary: true },
        { id: 'barcode-id' }
      )
    ];
    Object.assign(input, { sales_tax_treatment: 'STANDARD', sales_vat_rate_id: 'rate-id' });

    const payload = JSON.parse(JSON.stringify(normalizeProductInput(input))) as Record<string, any>;

    expect(payload.sales_tax_profile.direction).toBeUndefined();
    expect(payload.sales_tax_profile.version).toBeUndefined();
    expect(payload.sales_tax_profile.components[0].tax_definition_code).toBeUndefined();
    expect(payload.barcodes[0].id).toBeUndefined();
    expect(payload.sales_tax_treatment).toBeUndefined();
    expect(payload.sales_tax_profile.tax_rate_id).toBe('rate-id');
  });
});
