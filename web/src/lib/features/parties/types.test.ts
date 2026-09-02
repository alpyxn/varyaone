import { describe, expect, it } from 'vitest';
import {
  emptyParty,
  normalizeDecimal,
  normalizePartyInput,
  partyProvinceSelectionRequired,
  partyToInput,
  sortPartyGroups,
  validatePartyInput,
  type Party
} from './types';

describe('cari grubu sıralaması', () => {
  it('shows customer groups before dealer and supplier groups', () => {
    const groups = [
      { code: 'MALZEME_TED' },
      { code: 'BAYI' },
      { code: 'PERAKENDE' },
      { code: 'DIGER' },
      { code: 'HIZMET_TED' },
      { code: 'TOPTAN' }
    ];

    expect(sortPartyGroups(groups).map((group) => group.code)).toEqual([
      'PERAKENDE',
      'TOPTAN',
      'BAYI',
      'HIZMET_TED',
      'MALZEME_TED',
      'DIGER'
    ]);
  });
});

describe('party input validation', () => {
  it('normalizes Turkish decimal notation without floating point conversion', () => {
    expect(normalizeDecimal('1.250,50', 4)).toBe('1250.50');
    expect(normalizeDecimal(' 0,00 ', 4)).toBe('0.00');
    expect(normalizeDecimal('1.00001', 4)).toBeUndefined();
  });

  it('accepts a complete organization with Turkish decimal limits', () => {
    const input = emptyParty();
    input.legal_name = 'Test Kurum Resmî Unvan';
    input.credit_limit = '1.250,50';
    expect(validatePartyInput(input)).toBeUndefined();
  });

  it('derives the display name from the visible organization field', () => {
    const input = emptyParty();
    input.legal_name = 'ABC Makine Ltd. Şti.';
    expect(validatePartyInput(input)).toBeUndefined();
  });

  it('keeps the visible organization title in sync when an existing card changes', () => {
    const input = emptyParty();
    input.display_name = 'Eski Ünvan';
    input.legal_name = 'Yeni Ünvan';
    expect(normalizePartyInput(input).display_name).toBe('Yeni Ünvan');
  });

  it('keeps only the primary address for a cari card', () => {
    const input = emptyParty();
    input.addresses[0].address_line = 'Birinci adres';
    input.addresses[0].is_default = false;
    input.addresses.push({
      ...input.addresses[0],
      address_line: 'Varsayılan adres',
      is_default: true
    });

    const normalized = normalizePartyInput(input);
    expect(normalized.addresses).toHaveLength(1);
    expect(normalized.addresses[0].address_line).toBe('Varsayılan adres');
  });

  it('rejects negative or malformed decimal limits before POST', () => {
    const input = emptyParty();
    input.display_name = 'Test Kurum';
    input.legal_name = 'Test Kurum Resmî Unvan';
    input.credit_limit = '-1';
    expect(validatePartyInput(input)).toBe('Kredi limiti negatif olamaz.');
  });

  it('reports Turkish tax identifier length before POST', () => {
    const input = emptyParty();
    input.legal_name = 'Test Kurum Resmî Unvan';
    input.tax_number = 'sdf';
    expect(validatePartyInput(input)).toBe('Vergi numarası 10 haneli olmalıdır.');
  });

  it('validates optional contact e-mail without making it required', () => {
    const input = emptyParty();
    input.legal_name = 'Test Kurum Resmî Unvan';
    input.contacts[0].email = 'yanlış-adres';
    expect(validatePartyInput(input)).toBe('E-posta adresi geçerli değil.');
    input.contacts[0].email = '';
    expect(validatePartyInput(input)).toBeUndefined();
  });

  it('requires only the province when an address has data and accepts province-only addresses', () => {
    const input = emptyParty();
    input.legal_name = 'Test Kurum Resmî Unvan';
    input.addresses[0].address_line = 'Örnek Sokak 1';
    expect(partyProvinceSelectionRequired(input)).toBe(true);
    expect(validatePartyInput(input)).toBe('Adres bilgisi gönderildiğinde il seçimi zorunludur.');

    input.addresses[0].address_line = '';
    input.addresses[0].city_id = '34';
    input.addresses[0].city = 'İstanbul';
    expect(partyProvinceSelectionRequired(input)).toBe(false);
    expect(validatePartyInput(input)).toBeUndefined();
  });

  it('normalizes nullable fields returned by the party API before editing', () => {
    const fromApi = {
      ...emptyParty(),
      id: 'party-1',
      is_active: true,
      version: 1,
      phone: '',
      city: '',
      balance: '0',
      kind: 'PERSON',
      display_name: 'Sdf Sdf',
      legal_name: null,
      trade_name: null,
      first_name: 'Sdf',
      last_name: 'Sdf',
      tax_number: null,
      identity_number: null,
      tax_office: null,
      tax_office_id: null,
      payment_term_id: null,
      price_list_id: null,
      sales_rep_user_id: null,
      default_discount_rate: null,
      credit_limit: null,
      risk_limit: null,
      contacts: null,
      group_ids: null,
      tags: null,
      custom_fields: null
    } as unknown as Party;

    const input = partyToInput(fromApi);
    expect(input.legal_name).toBe('');
    expect(input.is_active).toBe(true);
    expect(input.contacts).toHaveLength(1);
    expect(() => validatePartyInput(input)).not.toThrow();
    expect(validatePartyInput(input)).toBeUndefined();
  });

  it('keeps the optional tax-office identity and canonical text together', () => {
    const input = emptyParty();
    input.legal_name = 'Kataloglu Kurum AŞ';
    input.tax_office_id = 'tax-office-1';
    input.tax_office = 'Kanona göre vergi dairesi';

    expect(normalizePartyInput(input)).toMatchObject({
      tax_office_id: 'tax-office-1',
      tax_office: 'Kanona göre vergi dairesi'
    });
  });
});
