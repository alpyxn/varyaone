import { describe, expect, it } from 'vitest';
import {
  configFor,
  type Field,
  type OperationDetailKind,
  type RecordValue
} from './operation-detail-config';

const KINDS: OperationDetailKind[] = [
  'party-movement',
  'collection',
  'payment',
  'stock-movement',
  'warehouse',
  'transfer',
  'count',
  'lot',
  'serial',
  'account',
  'account-movement',
  'finance-transfer',
  'document'
];

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

function keysOf(field: Field) {
  return Array.isArray(field.key) ? field.key : [field.key];
}

function allFields(kind: OperationDetailKind) {
  const config = configFor(kind);
  return [...config.fields, ...(config.tables ?? []).flatMap((table) => table.columns)];
}

// Kimlik (UUID) taşıyan anahtarlar kullanıcıya metin olarak basılamaz.
// Yeni bir alan eklerken bu testler, id'ye düşen bir görünüm bırakılmasını
// engeller: id yalnızca 'ref' alanlarında, bağlantı kurmak için kullanılabilir.
const looksLikeIdentity = (key: string) => {
  const leaf = key.split('.').pop() ?? key;
  return leaf === 'id' || leaf.endsWith('_id');
};

describe('operation detail config', () => {
  it.each(KINDS)('%s başlığı id anahtarına düşmez', (kind) => {
    expect(configFor(kind).numberKeys.filter(looksLikeIdentity)).toEqual([]);
  });

  it.each(KINDS)('%s alt satırı id anahtarına düşmez', (kind) => {
    expect(configFor(kind).subjectKeys.filter(looksLikeIdentity)).toEqual([]);
  });

  it.each(KINDS)('%s alanlarında id yalnızca ref olarak kullanılır', (kind) => {
    const leaking = allFields(kind)
      .filter((field) => field.kind !== 'ref')
      .filter((field) => keysOf(field).some(looksLikeIdentity))
      .map((field) => field.label);
    expect(leaking).toEqual([]);
  });

  it.each(KINDS)('%s ref alanlarının bağlantı yolu ve metni vardır', (kind) => {
    const broken = allFields(kind)
      .filter((field) => field.kind === 'ref')
      .filter((field) => !field.linkPath || !field.refText)
      .map((field) => field.label);
    expect(broken).toEqual([]);
  });

  it.each(KINDS)('%s numarasız kayıtta bile UUID göstermez', (kind) => {
    const config = configFor(kind);
    // Numarası, adı, kodu olmayan; elinde yalnızca kimliği olan bir kayıt.
    const bare: RecordValue = {
      id: '9f1c2b3a-4d5e-6f70-8192-a3b4c5d6e7f8',
      amount: '1250.0000',
      currency: 'TRY',
      debit: '1250.0000',
      credit: '0.0000'
    };
    const heading = config.numberFallback?.(bare) ?? config.title;
    expect(heading).not.toMatch(UUID);
    expect(heading.trim()).not.toBe('');
  });

  it('cari hareket başlığında sıfır olan tarafı göstermez', () => {
    const fallback = configFor('party-movement').numberFallback;
    expect(fallback?.({ debit: '0.0000', credit: '450.0000', currency: 'TRY' })).toContain('450');
    expect(fallback?.({ debit: '1250.0000', credit: '0.0000', currency: 'TRY' })).toContain(
      '1.250'
    );
  });

  it('numarası olan kayıtta numara anahtarları önce gelir', () => {
    expect(configFor('collection').numberKeys[0]).toBe('document_no');
    expect(configFor('stock-movement').numberKeys[0]).toBe('movement_no');
  });
});
