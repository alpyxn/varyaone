import { describe, expect, it } from 'vitest';
import {
  buildSetupPayload,
  packageConflicts,
  selectedPackageDefinitions,
  SETUP_SECTOR_PACKAGES
} from './setup-options';

describe('ilk kurulum varyant paketleri', () => {
  it('paket, tanım ve seçenek kodları büyük harf ASCII (Türkçe karaktersiz) olmalı', () => {
    const codeRe = /^[A-Z0-9_]+$/;
    for (const pkg of SETUP_SECTOR_PACKAGES) {
      expect(pkg.id).toMatch(codeRe);
      for (const definition of pkg.definitions) {
        expect(definition.code, `${pkg.id} tanım kodu`).toMatch(codeRe);
        for (const option of definition.options) {
          expect(option.code, `${pkg.id}/${definition.code} seçenek kodu`).toMatch(codeRe);
        }
      }
    }
  });

  it('her pakette en az iki tanım ve her tanımda en az iki seçenek var', () => {
    for (const pkg of SETUP_SECTOR_PACKAGES) {
      expect(pkg.definitions.length).toBeGreaterThanOrEqual(2);
      for (const definition of pkg.definitions) {
        expect(definition.options.length).toBeGreaterThanOrEqual(2);
      }
    }
  });

  it('bir tanım kodu farklı paketlerde farklı ada sahip olamaz', () => {
    const names = new Map<string, string>();
    for (const pkg of SETUP_SECTOR_PACKAGES) {
      for (const definition of pkg.definitions) {
        const seen = names.get(definition.code);
        if (seen) expect(seen).toBe(definition.name);
        names.set(definition.code, definition.name);
      }
    }
  });

  it('birden fazla paketi birleştirir ve ortak tanımları tekilleştirir', () => {
    const definitions = selectedPackageDefinitions(['HAZIR_GIYIM', 'AYAKKABI']);
    const codes = definitions.map((definition) => definition.code);

    // RENK ve CINSIYET her iki pakette de var → tek tanım
    expect(codes.filter((code) => code === 'RENK')).toHaveLength(1);
    expect(codes).toContain('NUMARA');
    expect(codes).toContain('KUMAS');

    const numara = definitions.find((definition) => definition.code === 'NUMARA');
    expect(numara?.options.map((option) => option.code)).toEqual(
      expect.arrayContaining(['35', '40', '45'])
    );
  });

  it('katalogtaki hiçbir paket kombinasyonu tanım çakışması üretmez', () => {
    const ids = SETUP_SECTOR_PACKAGES.map((pkg) => pkg.id);
    expect(packageConflicts(ids)).toEqual([]);
  });

  it('kurulum payloadunda paket seçimini tekilleştirir', () => {
    const payload = buildSetupPayload(
      {
        admin_name: 'Yönetici',
        admin_email: 'admin@example.test',
        password: 'uzun-ve-guvenli-parola',
        legal_name: 'Varya AŞ',
        trade_name: 'Varya',
        entity_type: 'LEGAL_ENTITY'
      },
      ['GENEL', 'HAZIR_GIYIM', 'GENEL'],
      ['preaccounting', 'hr', 'preaccounting']
    );

    expect(payload.sector_packages).toEqual(['GENEL', 'HAZIR_GIYIM']);
    expect(payload.modules).toEqual(['preaccounting', 'hr']);
  });
});
