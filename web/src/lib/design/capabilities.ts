/**
 * İş alanı bazlı yetki kataloğu.
 *
 * Ham yetki kodları (ör. "finance.collection.post") son kullanıcıya anlamlı
 * gelmiyor. Bu katalog yetkileri "iş alanlarına" (cari, satış, ön muhasebe, İK…)
 * ve her alan içinde kademeli erişim seviyelerine ("Görüntüleme", "İşlem yapma",
 * "Tam yetki") gruplar. Rol oluştururken kullanıcı bu seviyeleri seçer; biz de
 * arka planda doğru yetki kümesini üretiriz.
 *
 * Seviyeler kümülatiftir: "operate" seviyesi "view" yetkilerini de içerir,
 * "full" seviyesi ise hepsini içerir.
 */

import { navigation, canOpenNavigation } from '$lib/navigation';

export type LevelKey = 'view' | 'operate' | 'full';

/** Segmented kontrolde gösterilen sade seviye adları. */
export const LEVEL_UI: Record<LevelKey | 'none', string> = {
  none: 'Kapalı',
  view: 'Görüntüler',
  operate: 'Yapabilir',
  full: 'Yetkili işlemler'
};

export type CapabilityLevel = {
  key: LevelKey;
  label: string;
  /** Seviye seçildiğinde eklenecek yetki kodları (yalnızca bu seviyeye özgü). */
  grants: string[];
  /** Rolün ne yaptığını anlatan cümlede kullanılacak kısa ifade. */
  phrase: string;
};

export type CapabilityDomain = {
  key: string;
  label: string;
  /** @lucide/svelte ikon adı. */
  icon: string;
  blurb: string;
  levels: CapabilityLevel[];
};

export const CAPABILITY_DOMAINS: CapabilityDomain[] = [
  {
    key: 'party',
    label: 'Cari',
    icon: 'Contact',
    blurb: 'Müşteri/tedarikçi kartları ve ekstre.',
    levels: [
      {
        key: 'view',
        label: 'Görüntüleme',
        phrase: 'cari kartları görüntüler',
        grants: ['party.read', 'party.ledger.read']
      },
      {
        key: 'operate',
        label: 'Kart yönetimi',
        phrase: 'cari kartları açıp düzenler',
        grants: ['party.create', 'party.edit', 'party.deactivate']
      },
      {
        key: 'full',
        label: 'Cari hesap kaydı',
        phrase: 'carilere manuel hesap kaydı girer',
        grants: ['party.ledger.post']
      }
    ]
  },
  {
    key: 'product',
    label: 'Stok & fiyat',
    icon: 'Package',
    blurb: 'Ürün kartları, varyant, fiyat ve vergi.',
    levels: [
      {
        key: 'view',
        label: 'Görüntüleme',
        phrase: 'stok kartlarını ve fiyatları görüntüler',
        grants: [
          'product.read',
          'product.image.read',
          'product.attachment.read',
          'pricing.read',
          'tax.read'
        ]
      },
      {
        key: 'operate',
        label: 'Kart yönetimi',
        phrase: 'stok kartlarını ve varyantları yönetir',
        grants: [
          'product.create',
          'product.edit',
          'product.deactivate',
          'product.image.manage',
          'product.attachment.manage',
          'product.reference.manage',
          'product.variant.manage'
        ]
      },
      {
        key: 'full',
        label: 'Fiyat & tanım yönetimi',
        phrase: 'fiyat listelerini, vergi ve varyant tanımlarını yönetir',
        grants: ['pricing.manage', 'tax.manage', 'product.variant_definition.manage']
      }
    ]
  },
  {
    key: 'inventory',
    label: 'Depo & stok',
    icon: 'Warehouse',
    blurb: 'Stok hareketleri, transfer ve sayım.',
    levels: [
      {
        key: 'view',
        label: 'Görüntüleme',
        phrase: 'stok seviyelerini görüntüler',
        grants: ['inventory.read', 'inventory.lot_serial.read']
      },
      {
        key: 'operate',
        label: 'Günlük depo işlemleri',
        phrase: 'stok hareketi girer ve transfer/sayım yapar',
        grants: [
          'inventory.movement.post',
          'inventory.transfer.request',
          'inventory.transfer.receive',
          'inventory.count.post'
        ]
      },
      {
        key: 'full',
        label: 'Depo yönetimi',
        phrase: 'depoları yönetir, hareketleri ters çevirir ve transfer onaylar',
        grants: [
          'inventory.movement.reverse',
          'inventory.transfer.approve',
          'inventory.transfer.ship',
          'inventory.transfer.reconcile',
          'inventory.warehouse.manage',
          'inventory.fefo.override',
          'organization.warehouse.manage'
        ]
      }
    ]
  },
  {
    key: 'sales',
    label: 'Satış',
    icon: 'ShoppingCart',
    blurb: 'Teklif, sipariş, irsaliye ve fatura.',
    levels: [
      {
        key: 'view',
        label: 'Görüntüleme',
        phrase: 'satış belgelerini görüntüler',
        grants: [
          'sales.quote.read',
          'sales.order.read',
          'sales.dispatch.read',
          'sales.invoice.read',
          'sales.return.read',
          'commercial.document.read',
          'document.read'
        ]
      },
      {
        key: 'operate',
        label: 'Satış işlemleri',
        phrase: 'satış teklifi, sipariş ve fatura keser',
        grants: [
          'sales.quote.manage',
          'sales.order.manage',
          'sales.dispatch.draft',
          'sales.dispatch.post',
          'sales.invoice.draft',
          'sales.invoice.post',
          'sales.return.draft',
          'sales.return.post',
          'commercial.document.manage',
          'document.create',
          'document.edit',
          'document.post',
          'document.invoice.post'
        ]
      },
      {
        key: 'full',
        label: 'Yetkili satış',
        phrase: 'fiyat/iskonto geçersiz kılar, maliyet görür ve belge iptal eder',
        grants: [
          'sales.price.override',
          'sales.tax.override',
          'sales.risk.override',
          'sales.cost.read',
          'document.cancel',
          'document.invoice.reverse'
        ]
      }
    ]
  },
  {
    key: 'purchase',
    label: 'Satın alma',
    icon: 'Truck',
    blurb: 'Sipariş, mal kabul, fatura ve iade.',
    levels: [
      {
        key: 'view',
        label: 'Görüntüleme',
        phrase: 'satın alma belgelerini görüntüler',
        grants: ['purchase.order.read', 'commercial.document.read', 'document.read']
      },
      {
        key: 'operate',
        label: 'Satın alma işlemleri',
        phrase: 'alış siparişi verir, mal kabul ve fatura kaydeder',
        grants: [
          'purchase.order.manage',
          'purchase.receipt.draft',
          'purchase.receipt.post',
          'purchase.invoice.draft',
          'purchase.invoice.post',
          'purchase.return.draft',
          'purchase.return.post',
          'purchase.landed_cost.manage',
          'purchase.landed_cost.post',
          'purchase.reference.manage',
          'commercial.document.manage',
          'document.create',
          'document.edit',
          'document.post',
          'document.invoice.post'
        ]
      },
      {
        key: 'full',
        label: 'Yetkili satın alma',
        phrase: 'fiyat/vergi geçersiz kılar ve bağımsız fatura girer',
        grants: [
          'purchase.price.override',
          'purchase.tax.override',
          'purchase.invoice.standalone',
          'document.cancel',
          'document.invoice.reverse'
        ]
      }
    ]
  },
  {
    key: 'finance',
    label: 'Ön muhasebe',
    icon: 'Wallet',
    blurb: 'Kasa, banka, tahsilat, ödeme ve virman.',
    levels: [
      {
        key: 'view',
        label: 'Görüntüleme',
        phrase: 'kasa/banka ve tahsilatları görüntüler',
        grants: [
          'finance.read',
          'finance.bank_account.read',
          'finance.bank_movement.read',
          'finance.cash_account.read',
          'finance.cash_movement.read',
          'finance.collection.read',
          'finance.payment.read',
          'finance.transfer.read'
        ]
      },
      {
        key: 'operate',
        label: 'Ön muhasebe işlemleri',
        phrase: 'tahsilat/ödeme alır, virman ve kasa-banka hareketi girer',
        grants: [
          'finance.collection.create',
          'finance.collection.post',
          'finance.payment.create',
          'finance.payment.post',
          'finance.transfer.create',
          'finance.transfer.post',
          'finance.cash_movement.post',
          'finance.bank_movement.post',
          'finance.manual.post',
          'finance.allocation.manage',
          'finance.cash_account.create',
          'finance.cash_account.edit',
          'finance.bank_account.create',
          'finance.bank_account.edit',
          'party.ledger.post'
        ]
      },
      {
        key: 'full',
        label: 'Muhasebe sorumlusu',
        phrase: 'kayıtları ters çevirir, dönem kilitler ve hesap yönetir',
        grants: [
          'finance.collection.reverse',
          'finance.payment.reverse',
          'finance.transfer.reverse',
          'finance.cash_movement.reverse',
          'finance.bank_movement.reverse',
          'finance.account.manage',
          'finance.period.manage',
          'finance.instrument.manage',
          'finance.negative_balance.override',
          'finance.bank_account.deactivate',
          'finance.cash_account.deactivate'
        ]
      }
    ]
  },
  {
    key: 'hr',
    label: 'İK & bordro',
    icon: 'HeartHandshake',
    blurb: 'Personel, izin, puantaj, bordro ve avans.',
    levels: [
      {
        key: 'view',
        label: 'Görüntüleme',
        phrase: 'personel ve bordro kayıtlarını görüntüler',
        grants: [
          'hr.employee.read',
          'hr.leave.read',
          'hr.schedule.read',
          'hr.timesheet.read',
          'hr.payroll.read',
          'hr.legislation.read',
          'hr.employee_document.read',
          'hr.employee_advance.read',
          'fixed_asset.read'
        ]
      },
      {
        key: 'operate',
        label: 'İK işlemleri',
        phrase: 'izin/puantaj yönetir ve bordro hesaplar',
        grants: [
          'hr.employee.edit',
          'hr.leave.edit',
          'hr.leave.approve',
          'hr.schedule.edit',
          'hr.timesheet.edit',
          'hr.timesheet.finalize',
          'hr.employee_document.edit',
          'hr.employee_advance.post',
          'hr.employee_advance.collect',
          'hr.payroll.calculate',
          'hr.payroll.payslip',
          'hr.payroll.download',
          'hr.payroll.email',
          'fixed_asset.edit',
          'fixed_asset.assign'
        ]
      },
      {
        key: 'full',
        label: 'İK yöneticisi',
        phrase: 'bordroyu kesinleştirir ve özlük/sağlık verilerini görür',
        grants: [
          'hr.payroll.finalize',
          'hr.payroll.bulk_export',
          'hr.payroll.pay',
          'hr.timesheet.reopen',
          'hr.legislation.manage',
          'hr.employee_private.read',
          'hr.employee_private.edit',
          'hr.health_document.read',
          'hr.employee_advance.reverse',
          'hr.employee_advance.writeoff'
        ]
      }
    ]
  },
  {
    key: 'reporting',
    label: 'Raporlar',
    icon: 'BarChart3',
    blurb: 'Satış, stok, finans ve İK raporları.',
    levels: [
      {
        key: 'view',
        label: 'Raporları görebilir',
        phrase: 'raporları çalıştırır',
        grants: ['reporting.read']
      }
    ]
  },
  {
    key: 'system',
    label: 'Sistem',
    icon: 'Settings',
    blurb: 'Şirket, kullanıcı/rol, e-posta ve yedek.',
    levels: [
      {
        key: 'view',
        label: 'Görüntüleme',
        phrase: 'şirket ayarlarını ve denetim kaydını görüntüler',
        grants: ['organization.company.read', 'security.user.read', 'security.audit.read']
      },
      {
        key: 'operate',
        label: 'Ayar yönetimi',
        phrase: 'kullanıcı/rolleri ve şirket ayarlarını yönetir',
        grants: [
          'organization.company.edit',
          'organization.branch.manage',
          'security.user.manage',
          'security.role.manage',
          'settings.email.manage',
          'settings.email.test',
          'communication.email.send',
          'communication.email.template.manage',
          'document.type.manage'
        ]
      },
      {
        key: 'full',
        label: 'Sistem yöneticisi',
        phrase: 'modülleri, API anahtarlarını ve yedeklemeyi yönetir',
        grants: ['organization.module.manage', 'security.token.manage', 'system.backup.manage']
      }
    ]
  }
];

const LEVEL_ORDER: LevelKey[] = ['view', 'operate', 'full'];

/** Bir alanın belirli seviyesi için kümülatif yetki kümesi. */
export function grantsFor(domain: CapabilityDomain, level: LevelKey): string[] {
  const maxIndex = LEVEL_ORDER.indexOf(level);
  const out: string[] = [];
  for (const lvl of domain.levels) {
    if (LEVEL_ORDER.indexOf(lvl.key) <= maxIndex) out.push(...lvl.grants);
  }
  return out;
}

export type DomainSelection = Record<string, LevelKey | 'none'>;

/** Alan seçimlerinden nihai yetki kodu listesi üretir. */
export function permissionsFromSelection(
  selection: DomainSelection,
  extraPermissions: string[] = []
): string[] {
  const set = new Set<string>(extraPermissions);
  for (const domain of CAPABILITY_DOMAINS) {
    const level = selection[domain.key];
    if (!level || level === 'none') continue;
    for (const code of grantsFor(domain, level)) set.add(code);
  }
  return [...set].sort();
}

/** Katalogda herhangi bir alana/seviyeye bağlı olan tüm yetki kodları. */
export const CATALOGUED_PERMISSIONS = new Set<string>(
  CAPABILITY_DOMAINS.flatMap((d) => d.levels.flatMap((l) => l.grants))
);

export type DomainSummary = {
  domain: CapabilityDomain;
  level: LevelKey | 'partial';
  levelLabel: string;
};

/**
 * Bir yetki listesini alan bazlı özete çevirir: her alan için, yetkileri tam
 * karşılanan en yüksek seviye. Hiç biri tam değilse ama bazı yetkiler varsa
 * "partial" (kısmi).
 */
export function summarizeRole(permissions: string[]): DomainSummary[] {
  const owned = new Set(permissions);
  const summaries: DomainSummary[] = [];
  for (const domain of CAPABILITY_DOMAINS) {
    let best: LevelKey | null = null;
    let touched = false;
    for (const lvl of domain.levels) {
      const cumulative = grantsFor(domain, lvl.key);
      if (cumulative.some((c) => owned.has(c))) touched = true;
      if (cumulative.every((c) => owned.has(c))) best = lvl.key;
    }
    if (best) {
      const label = domain.levels.find((l) => l.key === best)?.label ?? '';
      summaries.push({ domain, level: best, levelLabel: label });
    } else if (touched) {
      summaries.push({ domain, level: 'partial', levelLabel: 'Kısmi erişim' });
    }
  }
  return summaries;
}

/** Yetki listesinden en yakın alan seçimini çıkarır (rol düzenlerken). */
export function selectionFromPermissions(permissions: string[]): DomainSelection {
  const owned = new Set(permissions);
  const selection: DomainSelection = {};
  for (const domain of CAPABILITY_DOMAINS) {
    let best: LevelKey | 'none' = 'none';
    for (const lvl of domain.levels) {
      if (grantsFor(domain, lvl.key).every((c) => owned.has(c))) best = lvl.key;
    }
    selection[domain.key] = best;
  }
  return selection;
}

/** Katalog seviyelerine girmeyen, elle verilmiş "gelişmiş" yetkiler. */
export function uncataloguedPermissions(permissions: string[]): string[] {
  return permissions.filter((p) => !CATALOGUED_PERMISSIONS.has(p)).sort();
}

/** Rolün ne yaptığını düz Türkçe bir cümleyle anlatır. */
export function describeRole(permissions: string[]): string {
  const parts = summarizeRole(permissions).map((s) => {
    if (s.level === 'partial')
      return `${s.domain.label.toLocaleLowerCase('tr')} alanında kısmi erişimi var`;
    const lvl = s.domain.levels.find((l) => l.key === s.level);
    return lvl?.phrase ?? '';
  });
  const clean = parts.filter(Boolean);
  if (clean.length === 0) return 'Henüz hiçbir yetkisi yok.';
  if (clean.length === 1) return capitalize(clean[0]) + '.';
  const last = clean.pop()!;
  return capitalize(clean.join(', ') + ' ve ' + last) + '.';
}

function capitalize(text: string): string {
  return text.charAt(0).toLocaleUpperCase('tr') + text.slice(1);
}

/**
 * Rolü iki listeye ayırır: yalnızca görüntüleyebildiği alanlar ve fiilen
 * işlem yapabildiği alanlar. Sayfada "Görüntüleyebilir / Yapabilir" başlıkları
 * altında gösterilir.
 */
export function capabilityLists(permissions: string[]): { view: string[]; operate: string[] } {
  const view: string[] = [];
  const operate: string[] = [];
  for (const s of summarizeRole(permissions)) {
    if (s.level === 'view' || s.level === 'partial') view.push(s.domain.label);
    else operate.push(s.domain.label);
  }
  return { view, operate };
}

/** Bu rolün soldaki menüde göreceği grup/öğe listesi (modül filtresi hariç). */
export function menusForPermissions(permissions: string[]): { group: string; items: string[] }[] {
  const out: { group: string; items: string[] }[] = [];
  for (const group of navigation) {
    if (group.href) {
      if (canOpenNavigation(group, permissions)) out.push({ group: group.label, items: [] });
      continue;
    }
    const items = (group.children ?? [])
      .filter((child) => child.href && canOpenNavigation(child, permissions))
      .map((child) => child.label);
    if (items.length) out.push({ group: group.label, items });
  }
  return out;
}

/** Hazır rol şablonları. */
export type RolePreset = {
  key: string;
  name: string;
  icon: string;
  description: string;
  selection: DomainSelection;
};

const none: DomainSelection = Object.fromEntries(
  CAPABILITY_DOMAINS.map((d) => [d.key, 'none' as const])
);

export const ROLE_PRESETS: RolePreset[] = [
  {
    key: 'on-muhasebe',
    name: 'Ön Muhasebeci',
    icon: 'Wallet',
    description: 'Kasa-banka, tahsilat/ödeme ve cari hareketlerle ilgilenir; belgeleri görür.',
    selection: {
      ...none,
      finance: 'operate',
      party: 'full',
      sales: 'view',
      purchase: 'view',
      reporting: 'view'
    }
  },
  {
    key: 'muhasebe-mudur',
    name: 'Ön muhasebe · yetkili',
    icon: 'Wallet',
    description: 'Ön muhasebede her şeyi yapar + ters kayıt, dönem kilidi, hesap yönetimi.',
    selection: {
      ...none,
      finance: 'full',
      party: 'full',
      sales: 'view',
      purchase: 'view',
      reporting: 'view'
    }
  },
  {
    key: 'satis-temsilcisi',
    name: 'Satış Temsilcisi',
    icon: 'ShoppingCart',
    description: 'Satış teklifi, sipariş ve fatura keser; cari ve stok kartlarını görür.',
    selection: {
      ...none,
      sales: 'operate',
      party: 'operate',
      product: 'view',
      inventory: 'view',
      reporting: 'view'
    }
  },
  {
    key: 'satinalma-sorumlusu',
    name: 'Satın Alma Sorumlusu',
    icon: 'Truck',
    description: 'Alış siparişi, mal kabul ve alış faturası; tedarikçi ve stok kartları.',
    selection: {
      ...none,
      purchase: 'operate',
      party: 'operate',
      product: 'view',
      inventory: 'operate',
      reporting: 'view'
    }
  },
  {
    key: 'depo-sorumlusu',
    name: 'Depo Sorumlusu',
    icon: 'Warehouse',
    description: 'Stok hareketleri, depo transferleri ve sayım; stok kartlarını görür.',
    selection: { ...none, inventory: 'full', product: 'view', reporting: 'view' }
  },
  {
    key: 'ik-uzmani',
    name: 'İK Uzmanı',
    icon: 'HeartHandshake',
    description: 'Personel, izin ve puantaj yönetir; bordro hesaplar ve bordro raporlarını görür.',
    selection: { ...none, hr: 'operate', reporting: 'view' }
  },
  {
    key: 'ik-yoneticisi',
    name: 'İK · yetkili',
    icon: 'HeartHandshake',
    description: 'İK’da her şeyi yapar + bordro kesinleştirme ve özlük/sağlık verileri.',
    selection: { ...none, hr: 'full', reporting: 'view' }
  },
  {
    key: 'denetci',
    name: 'Denetçi (salt okunur)',
    icon: 'BarChart3',
    description: 'Her şeyi görür, hiçbir şeyi değiştiremez. Raporlar ve denetim kaydı dahil.',
    selection: Object.fromEntries(
      CAPABILITY_DOMAINS.map((d) => [
        d.key,
        d.levels.some((l) => l.key === 'view') ? 'view' : 'none'
      ])
    ) as DomainSelection
  },
  {
    key: 'yonetici',
    name: 'Tam yetki',
    icon: 'Settings',
    description: 'Her alanda yapabilme + kullanıcı/rol yönetimi, modüller, yedekleme.',
    selection: Object.fromEntries(
      CAPABILITY_DOMAINS.map((d) => [d.key, d.levels[d.levels.length - 1].key])
    ) as DomainSelection
  }
];

/** İnsan-okunur yetki adları (gelişmiş görünüm için). */
export const PERMISSION_LABELS: Record<string, string> = {
  'organization.company.read': 'Şirket bilgilerini görüntüleme',
  'organization.company.edit': 'Şirket bilgilerini düzenleme',
  'organization.branch.manage': 'Şube yönetimi',
  'organization.warehouse.manage': 'Depo tanımı yönetimi',
  'organization.module.manage': 'Modül yönetimi',
  'party.read': 'Cari kart görüntüleme',
  'party.create': 'Cari kart oluşturma',
  'party.edit': 'Cari kart düzenleme',
  'party.deactivate': 'Cari kart pasifleştirme',
  'party.ledger.read': 'Cari ekstre görüntüleme',
  'party.ledger.post': 'Cari hesabına manuel kayıt',
  'product.read': 'Stok kartı görüntüleme',
  'product.create': 'Stok kartı oluşturma',
  'product.edit': 'Stok kartı düzenleme',
  'product.deactivate': 'Stok kartı pasifleştirme',
  'product.image.read': 'Ürün görseli görüntüleme',
  'product.image.manage': 'Ürün görseli yönetimi',
  'product.attachment.read': 'Ürün eki görüntüleme',
  'product.attachment.manage': 'Ürün eki yönetimi',
  'product.reference.manage': 'Stok tanımı yönetimi',
  'product.variant.manage': 'Varyant yönetimi',
  'product.variant_definition.manage': 'Varyant tanımı yönetimi',
  'pricing.read': 'Fiyat listesi görüntüleme',
  'pricing.manage': 'Fiyat listesi yönetimi',
  'tax.read': 'Vergi tanımı görüntüleme',
  'tax.manage': 'Vergi tanımı yönetimi',
  'inventory.read': 'Stok seviyesi görüntüleme',
  'inventory.lot_serial.read': 'Parti/seri görüntüleme',
  'inventory.movement.post': 'Stok hareketi işleme',
  'inventory.movement.reverse': 'Stok hareketi ters çevirme',
  'inventory.transfer.request': 'Depo transferi oluşturma',
  'inventory.transfer.approve': 'Depo transferi onaylama',
  'inventory.transfer.ship': 'Depo transferi sevk',
  'inventory.transfer.receive': 'Depo transferi teslim alma',
  'inventory.transfer.reconcile': 'Transfer farkı çözümleme',
  'inventory.count.post': 'Stok sayımı kaydetme',
  'inventory.warehouse.manage': 'Depo yönetimi',
  'inventory.fefo.override': 'FEFO (SKT) geçersiz kılma',
  'sales.quote.read': 'Satış teklifi görüntüleme',
  'sales.quote.manage': 'Satış teklifi yönetimi',
  'sales.order.read': 'Satış siparişi görüntüleme',
  'sales.order.manage': 'Satış siparişi yönetimi',
  'sales.dispatch.read': 'Satış irsaliyesi görüntüleme',
  'sales.dispatch.draft': 'Satış irsaliyesi taslağı',
  'sales.dispatch.post': 'Satış irsaliyesi kesme',
  'sales.invoice.read': 'Satış faturası görüntüleme',
  'sales.invoice.draft': 'Satış faturası taslağı',
  'sales.invoice.post': 'Satış faturası kesme',
  'sales.return.read': 'Satış iadesi görüntüleme',
  'sales.return.draft': 'Satış iadesi taslağı',
  'sales.return.post': 'Satış iadesi kesme',
  'sales.price.override': 'Satışta fiyat geçersiz kılma',
  'sales.tax.override': 'Satışta vergi geçersiz kılma',
  'sales.risk.override': 'Satışta risk limiti geçersiz kılma',
  'sales.cost.read': 'Satışta maliyet görme',
  'purchase.order.read': 'Alış siparişi görüntüleme',
  'purchase.order.manage': 'Alış siparişi yönetimi',
  'purchase.receipt.draft': 'Mal kabul taslağı',
  'purchase.receipt.post': 'Mal kabul kaydetme',
  'purchase.invoice.draft': 'Alış faturası taslağı',
  'purchase.invoice.post': 'Alış faturası kaydetme',
  'purchase.invoice.standalone': 'Bağımsız alış faturası',
  'purchase.return.draft': 'Alış iadesi taslağı',
  'purchase.return.post': 'Alış iadesi kaydetme',
  'purchase.landed_cost.manage': 'Navlun/masraf yönetimi',
  'purchase.landed_cost.post': 'Navlun/masraf dağıtımı',
  'purchase.reference.manage': 'Tedarikçi ürün eşleştirme',
  'purchase.price.override': 'Alışta fiyat geçersiz kılma',
  'purchase.tax.override': 'Alışta vergi geçersiz kılma',
  'commercial.document.read': 'Ticari belge görüntüleme',
  'commercial.document.manage': 'Ticari belge yönetimi',
  'document.read': 'Belge görüntüleme',
  'document.create': 'Belge oluşturma',
  'document.edit': 'Belge düzenleme',
  'document.post': 'Belge işleme',
  'document.cancel': 'Belge iptal etme',
  'document.invoice.post': 'Belgeden fatura kesme',
  'document.invoice.reverse': 'Fatura ters çevirme',
  'document.type.manage': 'Belge türü yönetimi',
  'finance.read': 'Finans görüntüleme',
  'finance.bank_account.read': 'Banka hesabı görüntüleme',
  'finance.bank_account.create': 'Banka hesabı ekleme',
  'finance.bank_account.edit': 'Banka hesabı düzenleme',
  'finance.bank_account.deactivate': 'Banka hesabı kapatma',
  'finance.bank_movement.read': 'Banka hareketi görüntüleme',
  'finance.bank_movement.post': 'Banka hareketi girme',
  'finance.bank_movement.reverse': 'Banka hareketi ters çevirme',
  'finance.cash_account.read': 'Kasa görüntüleme',
  'finance.cash_account.create': 'Kasa ekleme',
  'finance.cash_account.edit': 'Kasa düzenleme',
  'finance.cash_account.deactivate': 'Kasa kapatma',
  'finance.cash_movement.read': 'Kasa hareketi görüntüleme',
  'finance.cash_movement.post': 'Kasa hareketi girme',
  'finance.cash_movement.reverse': 'Kasa hareketi ters çevirme',
  'finance.collection.read': 'Tahsilat görüntüleme',
  'finance.collection.create': 'Tahsilat oluşturma',
  'finance.collection.post': 'Tahsilat kaydetme',
  'finance.collection.reverse': 'Tahsilat ters çevirme',
  'finance.payment.read': 'Ödeme görüntüleme',
  'finance.payment.create': 'Ödeme oluşturma',
  'finance.payment.post': 'Ödeme kaydetme',
  'finance.payment.reverse': 'Ödeme ters çevirme',
  'finance.transfer.read': 'Virman görüntüleme',
  'finance.transfer.create': 'Virman oluşturma',
  'finance.transfer.post': 'Virman kaydetme',
  'finance.transfer.reverse': 'Virman ters çevirme',
  'finance.manual.post': 'Manuel finans kaydı',
  'finance.allocation.manage': 'Tahsilat eşleştirme yönetimi',
  'finance.account.manage': 'Finans hesabı yönetimi',
  'finance.period.manage': 'Dönem kilidi yönetimi',
  'finance.instrument.manage': 'Çek/senet yönetimi',
  'finance.negative_balance.override': 'Negatif bakiye geçersiz kılma',
  'hr.employee.read': 'Personel kartı görüntüleme',
  'hr.employee.edit': 'Personel kartı düzenleme',
  'hr.employee_private.read': 'Özlük verisi görüntüleme',
  'hr.employee_private.edit': 'Özlük verisi düzenleme',
  'hr.employee_document.read': 'Personel belgesi görüntüleme',
  'hr.employee_document.edit': 'Personel belgesi düzenleme',
  'hr.health_document.read': 'Sağlık belgesi görüntüleme',
  'hr.leave.read': 'İzin görüntüleme',
  'hr.leave.edit': 'İzin düzenleme',
  'hr.leave.approve': 'İzin onaylama',
  'hr.schedule.read': 'Vardiya görüntüleme',
  'hr.schedule.edit': 'Vardiya düzenleme',
  'hr.timesheet.read': 'Puantaj görüntüleme',
  'hr.timesheet.edit': 'Puantaj düzenleme',
  'hr.timesheet.finalize': 'Puantaj kesinleştirme',
  'hr.timesheet.reopen': 'Puantaj yeniden açma',
  'hr.payroll.read': 'Bordro görüntüleme',
  'hr.payroll.calculate': 'Bordro hesaplama',
  'hr.payroll.finalize': 'Bordro kesinleştirme',
  'hr.payroll.payslip': 'Maaş pusulası oluşturma',
  'hr.payroll.download': 'Bordro indirme',
  'hr.payroll.email': 'Bordro e-posta gönderme',
  'hr.payroll.bulk_export': 'Toplu bordro dışa aktarma',
  'hr.payroll.pay': 'Bordro ödemesi oluşturma ve geri alma',
  'hr.legislation.read': 'Mevzuat parametreleri görüntüleme',
  'hr.legislation.manage': 'Mevzuat parametreleri yönetimi',
  'hr.employee_advance.read': 'Personel avansı görüntüleme',
  'hr.employee_advance.post': 'Personel avansı verme',
  'hr.employee_advance.collect': 'Personel avansı tahsil etme',
  'hr.employee_advance.reverse': 'Personel avansı ters çevirme',
  'hr.employee_advance.writeoff': 'Personel avansı silme',
  'fixed_asset.read': 'Sabit kıymet görüntüleme',
  'fixed_asset.edit': 'Sabit kıymet düzenleme',
  'fixed_asset.assign': 'Sabit kıymet zimmetleme',
  'reporting.read': 'Rapor çalıştırma',
  'security.user.read': 'Kullanıcı görüntüleme',
  'security.user.manage': 'Kullanıcı yönetimi',
  'security.role.manage': 'Rol yönetimi',
  'security.token.manage': 'API anahtarı yönetimi',
  'security.audit.read': 'Denetim kaydı görüntüleme',
  'settings.email.manage': 'E-posta ayarları yönetimi',
  'settings.email.test': 'E-posta ayarı test etme',
  'communication.email.send': 'E-posta gönderme',
  'communication.email.template.manage': 'E-posta şablonu yönetimi',
  'system.backup.manage': 'Sistem yedekleme yönetimi'
};

export function permissionLabel(code: string): string {
  return PERMISSION_LABELS[code] ?? code;
}
