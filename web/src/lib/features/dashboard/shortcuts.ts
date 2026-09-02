import type { Component } from 'svelte';
import { FilePlus2, PackagePlus, UserPlus } from '@lucide/svelte';
import { canOpenNavigation, navigation } from '$lib/navigation';
import { isModuleEnabled, type ModuleCode } from '$lib/modules';

export type Shortcut = {
  /** Stable key persisted in user_dashboard_preferences. */
  key: string;
  label: string;
  description: string;
  href: string;
  permission?: string;
  anyPermission?: string[];
  /** When set, the tile is hidden unless the feature module is enabled. */
  module?: ModuleCode;
  icon: Component;
  group: string;
};

/**
 * A flat launcher grid loses the "Satış > Faturalar" hierarchy, so labels that
 * repeat across modules (Faturalar, Siparişler, İrsaliyeler, İadeler,
 * Transferler…) must carry their own context.
 */
const labelOverrides: Record<string, string> = {
  '/satis/teklifler': 'Satış Teklifleri',
  '/satis/siparisler': 'Satış Siparişleri',
  '/satis/irsaliyeler': 'Satış İrsaliyeleri',
  '/satis/faturalar': 'Satış Faturaları',
  '/satis/iadeler': 'Satış İadeleri',
  '/alis/siparisler': 'Alış Siparişleri',
  '/alis/irsaliyeler': 'Alış İrsaliyeleri',
  '/alis/faturalar': 'Alış Faturaları',
  '/alis/iadeler': 'Alış İadeleri',
  '/stok/transferler': 'Depo Transferleri',
  '/finans/transferler': 'Hesap Transferleri',
  '/finans/hesaplar': 'Banka & Kasa Hesapları'
};

/** `/cari/kartlar/yeni` -> `cari.kartlar.yeni` */
function slug(href: string): string {
  return (
    href
      .replace(/^\//, '')
      .replace(/\//g, '.')
      .replace(/[^a-z0-9_.:-]/g, '-') || 'ana-sayfa'
  );
}

/** Create flows that have no sidebar entry but earn a launcher tile. */
const actionShortcuts: Shortcut[] = [
  {
    key: 'action.cari-yeni',
    label: 'Yeni Cari',
    description: 'Müşteri veya tedarikçi kartı aç',
    href: '/cari/kartlar/yeni',
    permission: 'party.create',
    module: 'preaccounting',
    icon: UserPlus,
    group: 'Hızlı işlem'
  },
  {
    key: 'action.stok-yeni',
    label: 'Yeni Stok Kartı',
    description: 'Ürün veya hizmet tanımla',
    href: '/stok/urunler/yeni',
    permission: 'product.create',
    module: 'preaccounting',
    icon: PackagePlus,
    group: 'Hızlı işlem'
  },
  {
    key: 'action.satis-fatura-yeni',
    label: 'Yeni Satış Faturası',
    description: 'Taslak satış faturası oluştur',
    href: '/satis/faturalar/yeni',
    permission: 'sales.invoice.draft',
    module: 'preaccounting',
    icon: FilePlus2,
    group: 'Hızlı işlem'
  },
  {
    key: 'action.satis-siparis-yeni',
    label: 'Yeni Satış Siparişi',
    description: 'Taslak satış siparişi oluştur',
    href: '/satis/siparisler/yeni',
    permission: 'sales.order.manage',
    module: 'preaccounting',
    icon: FilePlus2,
    group: 'Hızlı işlem'
  },
  {
    key: 'action.alis-siparis-yeni',
    label: 'Yeni Alış Siparişi',
    description: 'Taslak alış siparişi oluştur',
    href: '/alis/siparisler/yeni',
    permission: 'purchase.order.manage',
    module: 'preaccounting',
    icon: FilePlus2,
    group: 'Hızlı işlem'
  }
];

export const shortcutCatalog: Shortcut[] = [
  ...navigation.flatMap((group): Shortcut[] => {
    const entries: Shortcut[] = [];
    // The home page itself is never a useful launcher tile.
    if (group.href && group.href !== '/') {
      entries.push({
        key: slug(group.href),
        label: group.label,
        description: group.detail ?? group.label,
        href: group.href,
        module: group.module,
        icon: group.icon,
        group: group.label
      });
    }
    for (const child of group.children ?? []) {
      if (!child.href) continue;
      entries.push({
        key: slug(child.href),
        label: labelOverrides[child.href] ?? child.label,
        description: `${group.label} · ${child.label}`,
        href: child.href,
        permission: child.permission,
        anyPermission: child.anyPermission,
        module: group.module,
        icon: group.icon,
        group: group.label
      });
    }
    return entries;
  }),
  ...actionShortcuts
];

const catalogByKey = new Map(shortcutCatalog.map((shortcut) => [shortcut.key, shortcut]));

/** Default launcher layout for a user who has never customised it. */
export const DEFAULT_PINNED = [
  'cari.kartlar',
  'action.cari-yeni',
  'stok.urunler',
  'stok.hareketler',
  'satis.faturalar',
  'cari.tahsilatlar',
  'finans.hesaplar',
  'stok.sayim'
];

export function isShortcutVisible(
  shortcut: Shortcut,
  permissions?: readonly string[],
  modules?: readonly string[]
): boolean {
  return (
    isModuleEnabled(shortcut.module, modules) &&
    canOpenNavigation(
      { permission: shortcut.permission, anyPermission: shortcut.anyPermission },
      permissions
    )
  );
}

/** Ordered, permission-filtered pinned shortcuts. Falls back to DEFAULT_PINNED. */
export function resolvePinnedShortcuts(
  saved: readonly string[] | undefined,
  permissions?: readonly string[],
  modules?: readonly string[]
): Shortcut[] {
  const keys = saved && saved.length ? saved : DEFAULT_PINNED;
  const seen = new Set<string>();
  const result: Shortcut[] = [];
  for (const key of keys) {
    if (seen.has(key)) continue;
    seen.add(key);
    const shortcut = catalogByKey.get(key);
    if (shortcut && isShortcutVisible(shortcut, permissions, modules)) result.push(shortcut);
  }
  return result;
}

/** Every catalog entry the session may open, for the "Düzenle" picker. */
export function availableShortcuts(
  permissions?: readonly string[],
  modules?: readonly string[]
): Shortcut[] {
  return shortcutCatalog.filter((shortcut) => isShortcutVisible(shortcut, permissions, modules));
}

/** Fixed module order for the launcher: sidebar order, quick actions last. */
const groupOrder = [...navigation.map((group) => group.label), 'Hızlı işlem'];

export type ShortcutGroup = { group: string; items: Shortcut[] };

/**
 * Visible shortcuts bucketed by module and returned in a stable order so the
 * launcher never reshuffles — every module's tiles stay contiguous under their
 * own heading.
 */
export function groupedShortcuts(
  permissions?: readonly string[],
  modules?: readonly string[]
): ShortcutGroup[] {
  const buckets = new Map<string, Shortcut[]>();
  for (const shortcut of availableShortcuts(permissions, modules)) {
    const bucket = buckets.get(shortcut.group);
    if (bucket) bucket.push(shortcut);
    else buckets.set(shortcut.group, [shortcut]);
  }
  return groupOrder
    .filter((group) => buckets.has(group))
    .map((group) => ({ group, items: buckets.get(group) as Shortcut[] }));
}
