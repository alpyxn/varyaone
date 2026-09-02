import type { ProductKind, ProductUnit } from './types';

export type UnitCatalogEntry = {
  code: string;
  name: string;
  /** Sensible default decimal precision when this unit becomes the base unit. */
  decimal_scale: number;
};

export type UnitCatalogGroup = {
  id: string;
  label: string;
  units: UnitCatalogEntry[];
};

const count = (code: string, name: string): UnitCatalogEntry => ({ code, name, decimal_scale: 0 });
const measure = (code: string, name: string): UnitCatalogEntry => ({
  code,
  name,
  decimal_scale: 3
});

const PHYSICAL_GROUPS: UnitCatalogGroup[] = [
  {
    id: 'count',
    label: 'Sayı / paketleme',
    units: [
      count('ADET', 'Adet'),
      count('ÇİFT', 'Çift'),
      count('TAKIM', 'Takım'),
      count('SET', 'Set'),
      count('PAKET', 'Paket'),
      count('KUTU', 'Kutu'),
      count('KOLİ', 'Koli'),
      count('KASA', 'Kasa'),
      count('PALET', 'Palet'),
      count('TORBA', 'Torba'),
      count('ÇUVAL', 'Çuval'),
      count('RULO', 'Rulo'),
      count('ŞİŞE', 'Şişe'),
      count('BİDON', 'Bidon'),
      count('VARİL', 'Varil'),
      count('TÜP', 'Tüp'),
      count('TABAKA', 'Tabaka'),
      count('LEVHA', 'Levha'),
      count('BOBİN', 'Bobin')
    ]
  },
  {
    id: 'weight',
    label: 'Ağırlık',
    units: [measure('GR', 'Gram'), measure('KG', 'Kilogram'), measure('TON', 'Ton')]
  },
  {
    id: 'length',
    label: 'Uzunluk',
    units: [
      measure('MM', 'Milimetre'),
      measure('CM', 'Santimetre'),
      measure('MT', 'Metre'),
      measure('KM', 'Kilometre')
    ]
  },
  {
    id: 'area',
    label: 'Alan',
    units: [measure('CM²', 'Santimetrekare'), measure('M²', 'Metrekare')]
  },
  {
    id: 'volume',
    label: 'Hacim',
    units: [measure('ML', 'Mililitre'), measure('LT', 'Litre'), measure('M³', 'Metreküp')]
  }
];

const SERVICE_GROUPS: UnitCatalogGroup[] = [
  {
    id: 'count',
    label: 'Sayı',
    units: [count('ADET', 'Adet'), count('PAKET', 'Paket'), count('SET', 'Set')]
  },
  {
    id: 'time',
    label: 'Zaman',
    units: [
      measure('SAAT', 'Saat'),
      measure('DAKİKA', 'Dakika'),
      count('GÜN', 'Gün'),
      count('HAFTA', 'Hafta'),
      count('AY', 'Ay'),
      count('YIL', 'Yıl')
    ]
  },
  {
    id: 'work',
    label: 'İş / kapsam',
    units: [
      count('İŞ', 'İş'),
      count('İŞLEM', 'İşlem'),
      count('PROJE', 'Proje'),
      count('SEANS', 'Seans'),
      count('DERS', 'Ders'),
      count('ZİYARET', 'Ziyaret')
    ]
  },
  {
    id: 'subscription',
    label: 'Kişi / abonelik',
    units: [
      count('KİŞİ', 'Kişi'),
      count('KULLANICI', 'Kullanıcı'),
      count('LİSANS', 'Lisans'),
      count('ABONELİK', 'Abonelik')
    ]
  },
  {
    id: 'logistics',
    label: 'Lojistik',
    units: [count('GÖNDERİ', 'Gönderi'), count('SEFER', 'Sefer')]
  },
  {
    id: 'measure',
    label: 'Ölçü',
    units: [
      measure('KM', 'Kilometre'),
      measure('MT', 'Metre'),
      measure('M²', 'Metrekare'),
      measure('M³', 'Metreküp'),
      measure('KG', 'Kilogram'),
      measure('TON', 'Ton'),
      measure('LT', 'Litre')
    ]
  }
];

export function unitCatalog(kind: ProductKind): UnitCatalogGroup[] {
  return kind === 'SERVICE' ? SERVICE_GROUPS : PHYSICAL_GROUPS;
}

/** Flat lookup of every catalog entry for the given product kind. */
export function unitCatalogIndex(kind: ProductKind): Map<string, UnitCatalogEntry> {
  const index = new Map<string, UnitCatalogEntry>();
  for (const group of unitCatalog(kind)) {
    for (const unit of group.units) index.set(unit.code, unit);
  }
  return index;
}

const OTHER_KIND: Record<ProductKind, ProductKind> = {
  PHYSICAL: 'SERVICE',
  SERVICE: 'PHYSICAL'
};

/**
 * Canonical form for comparing unit codes. The backend `units` table stores
 * ASCII/legacy variants (GUN, KOLI, M2, M3) of codes the catalog writes with
 * Turkish letters or superscripts (GÜN, KOLİ, M², M³). Folding both sides lets
 * us recognise them as the same unit instead of leaking duplicates into the
 * "Diğer birimler" grubu.
 */
export function foldUnitCode(code: string): string {
  return code
    .trim()
    .toLocaleUpperCase('tr-TR')
    .replace(/[İIı]/g, 'I')
    .replace(/Ü/g, 'U')
    .replace(/Ö/g, 'O')
    .replace(/Ç/g, 'C')
    .replace(/Ş/g, 'S')
    .replace(/Ğ/g, 'G')
    .replace(/²/g, '2')
    .replace(/³/g, '3');
}

/**
 * Merge the built-in catalog for this product kind with genuinely custom units.
 *
 * The picker only ever shows one kind's birim list: a physical card never lists
 * hizmet birimleri and vice versa. Units the backend reports that already belong
 * to the *other* kind's built-in catalog are ignored here so they don't leak
 * into "Diğer birimler". `alwaysInclude` (e.g. the code currently saved on the
 * card) is kept regardless so switching a card's type never loses its value.
 */
export function mergeUnitGroups(
  kind: ProductKind,
  extra: readonly ProductUnit[] = [],
  ...alwaysInclude: string[]
): UnitCatalogGroup[] {
  const groups = unitCatalog(kind).map((group) => ({ ...group, units: [...group.units] }));
  const known = new Set(
    groups.flatMap((group) => group.units.map((unit) => foldUnitCode(unit.code)))
  );
  const otherKindCodes = new Set(
    unitCatalog(OTHER_KIND[kind]).flatMap((group) =>
      group.units.map((unit) => foldUnitCode(unit.code))
    )
  );
  const custom: UnitCatalogEntry[] = [];

  const push = (code: string, name: string, decimalScale: number) => {
    const normalized = code.trim().toUpperCase();
    const folded = foldUnitCode(code);
    if (!folded || known.has(folded)) return;
    if (otherKindCodes.has(folded)) return;
    known.add(folded);
    custom.push({ code: normalized, name: name.trim() || normalized, decimal_scale: decimalScale });
  };

  for (const unit of extra) push(unit.code, unit.name, unit.decimal_scale ?? 0);
  for (const code of alwaysInclude) push(code, code, 0);

  if (custom.length > 0) {
    groups.push({ id: 'custom', label: 'Diğer birimler', units: custom });
  }
  return groups;
}
