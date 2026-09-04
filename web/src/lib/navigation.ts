import type { Component } from 'svelte';
import { isModuleEnabled, type ModuleCode } from '$lib/modules';
import {
  Boxes,
  BriefcaseBusiness,
  FileInput,
  FileOutput,
  Gauge,
  Landmark,
  Package,
  Settings,
  ShoppingCart,
  UsersRound
} from '@lucide/svelte';

export type NavigationState = 'active' | 'coming' | 'disabled';
export type NavigationChild = {
  label: string;
  href?: string;
  permission?: string;
  /** Visible when the session holds ANY of these permissions. Combines with `permission` (which is always required when set). */
  anyPermission?: string[];
  state?: NavigationState;
  detail?: string;
};
export type NavigationGroup = {
  label: string;
  icon: Component;
  href?: string;
  state?: NavigationState;
  detail?: string;
  /** When set, the group is hidden unless the feature module is enabled. */
  module?: ModuleCode;
  children?: NavigationChild[];
};

export function visibleNavigation(
  permissions?: readonly string[],
  permissionsReady = true,
  modules?: readonly string[]
): NavigationGroup[] {
  if (!permissionsReady) return [];
  return navigation
    .filter((group) => isModuleEnabled(group.module, modules))
    .map((group) => {
      if (group.href) return canOpenNavigation(group, permissions) ? group : undefined;
      const children = group.children?.filter((child) => canOpenNavigation(child, permissions));
      if (group.children && !children?.length) return undefined;
      return children && children.length ? { ...group, children } : group;
    })
    .filter((group): group is NavigationGroup => Boolean(group));
}

export const navigation: NavigationGroup[] = [
  { label: 'Ana Sayfa', icon: Gauge, href: '/' },
  {
    label: 'Cari',
    icon: UsersRound,
    module: 'preaccounting',
    children: [
      { label: 'Cari Kartlar', href: '/cari/kartlar', permission: 'party.read' },
      { label: 'Cari Hareketler', href: '/cari/hareketler', permission: 'party.ledger.read' },
      {
        label: 'Cari Yaşlandırma',
        href: '/cari/yaslandirma',
        anyPermission: ['finance.collection.read', 'finance.payment.read']
      },
      { label: 'Tahsilatlar', href: '/cari/tahsilatlar', permission: 'finance.collection.read' },
      { label: 'Ödemeler', href: '/cari/odemeler', permission: 'finance.payment.read' }
    ]
  },
  {
    label: 'Stok',
    icon: Boxes,
    state: 'active',
    module: 'preaccounting',
    children: [
      { label: 'Stok Kartları', href: '/stok/urunler', permission: 'product.read' },
      { label: 'Stok Hareketleri', href: '/stok/hareketler', permission: 'inventory.read' },
      { label: 'Depolar', href: '/stok/depolar', permission: 'inventory.read' },
      { label: 'Transferler', href: '/stok/transferler', permission: 'inventory.read' },
      { label: 'Sayım', href: '/stok/sayim', permission: 'inventory.read' },
      { label: 'Aktarımlar', href: '/aktarimlar', permission: 'inventory.read' }
    ]
  },
  {
    label: 'Satış',
    icon: ShoppingCart,
    state: 'active',
    module: 'preaccounting',
    children: [
      { label: 'Teklifler', href: '/satis/teklifler', permission: 'sales.quote.read' },
      { label: 'Siparişler', href: '/satis/siparisler', permission: 'sales.order.read' },
      { label: 'İrsaliyeler', href: '/satis/irsaliyeler', permission: 'sales.dispatch.read' },
      { label: 'Faturalar', href: '/satis/faturalar', permission: 'sales.invoice.read' },
      { label: 'İadeler', href: '/satis/iadeler', permission: 'sales.return.read' }
    ]
  },
  {
    label: 'Alış',
    icon: FileInput,
    state: 'active',
    module: 'preaccounting',
    children: [
      { label: 'Siparişler', href: '/alis/siparisler', permission: 'purchase.order.read' },
      { label: 'İrsaliyeler', href: '/alis/irsaliyeler', permission: 'purchase.receipt.post' },
      { label: 'Faturalar', href: '/alis/faturalar', permission: 'purchase.invoice.post' },
      { label: 'İadeler', href: '/alis/iadeler', permission: 'purchase.return.post' }
    ]
  },
  {
    label: 'Banka & Kasa',
    icon: Landmark,
    state: 'active',
    module: 'preaccounting',
    children: [
      {
        label: 'Hesaplar',
        href: '/finans/hesaplar',
        anyPermission: ['finance.cash_account.read', 'finance.bank_account.read']
      },
      {
        label: 'Hesap Hareketleri',
        href: '/finans/hareketler',
        anyPermission: ['finance.cash_movement.read', 'finance.bank_movement.read']
      },
      { label: 'Transferler', href: '/finans/transferler', permission: 'finance.transfer.read' }
    ]
  },
  {
    label: 'İnsan Kaynakları',
    icon: BriefcaseBusiness,
    state: 'active',
    module: 'hr',
    children: [
      { label: 'Çalışanlar', href: '/personel/calisanlar', permission: 'hr.employee.read' },
      { label: 'Avanslar', href: '/personel/avanslar', permission: 'hr.employee_advance.read' },
      { label: 'Çalışma Planı', href: '/personel/plan', permission: 'hr.schedule.read' },
      { label: 'İzin Türleri', href: '/personel/izinler', permission: 'hr.leave.read' },
      { label: 'Puantaj', href: '/personel/puantaj', permission: 'hr.timesheet.read' },
      { label: 'Bordro', href: '/personel/bordro', permission: 'hr.payroll.read' }
    ]
  },
  {
    label: 'Sabit Kıymetler',
    icon: Package,
    state: 'active',
    module: 'fixed_asset',
    children: [
      { label: 'Sabit Kıymetler', href: '/sabit-kiymetler', permission: 'fixed_asset.read' }
    ]
  },
  {
    label: 'e-Belge',
    icon: FileInput,
    href: '/e-belge',
    state: 'coming',
    detail: 'Yakında etkinleşecek'
  },
  {
    label: 'Raporlar',
    icon: FileOutput,
    state: 'active',
    module: 'preaccounting',
    children: [
      {
        label: 'Vadesi Geçen Alacaklar',
        href: '/raporlar/vadesi-gecen-alacaklar',
        permission: 'reporting.read'
      },
      {
        label: 'Vadesi Geçen Borçlar',
        href: '/raporlar/vadesi-gecen-borclar',
        permission: 'reporting.read'
      },
      { label: 'Stok Değerleme', href: '/raporlar/stok-degerleme', permission: 'reporting.read' },
      {
        label: 'En Çok Satan Ürünler',
        href: '/raporlar/en-cok-satanlar',
        permission: 'reporting.read'
      },
      {
        label: 'Satış Kârlılığı',
        href: '/raporlar/satis-karliligi',
        permission: 'sales.cost.read'
      },
      { label: 'Vergi Özeti', href: '/raporlar/vergi-ozeti', permission: 'reporting.read' }
    ]
  },
  {
    label: 'Ayarlar',
    icon: Settings,
    children: [
      { label: 'Şirket', href: '/ayarlar/firma' },
      { label: 'Tanımlar', href: '/ayarlar/tanimlar' },
      { label: 'Döviz Kurları', href: '/ayarlar/doviz-kurlari', permission: 'pricing.read' },
      { label: 'E-posta (SMTP)', href: '/ayarlar/e-posta', permission: 'settings.email.manage' },
      // E-posta taslakları yalnızca bordro akışında kullanılıyor; oradan
      // (/personel/bordro) düzenleniyor. /ayarlar/e-posta-taslaklari rotası
      // erişilebilir kalır ama Ayarlar menüsünde ve global aramada görünmez.
      {
        label: 'Kullanıcılar ve Roller',
        href: '/ayarlar/kullanicilar',
        permission: 'security.user.read'
      },
      {
        label: 'Modüller',
        href: '/ayarlar/moduller',
        permission: 'organization.module.manage'
      },
      { label: 'Güvenlik', href: '/ayarlar/guvenlik' },
      { label: 'Yedekleme', href: '/ayarlar/yedekleme', permission: 'system.backup.manage' },
      { label: 'Sistem Durumu', href: '/sistem-durumu' }
    ]
  }
];

export function isNavigationActive(pathname: string, href?: string) {
  if (!href) return false;
  return href === '/' ? pathname === '/' : pathname === href || pathname.startsWith(`${href}/`);
}

export function canOpenNavigation(
  item: { permission?: string } | NavigationGroup | NavigationChild,
  permissions?: readonly string[]
) {
  const permission = 'permission' in item ? item.permission : undefined;
  const anyPermission = 'anyPermission' in item ? item.anyPermission : undefined;
  if (permissions === undefined) return true;
  if (permission && !permissions.includes(permission)) return false;
  if (
    anyPermission &&
    anyPermission.length > 0 &&
    !anyPermission.some((code) => permissions.includes(code))
  ) {
    return false;
  }
  return true;
}

export type NavigationSearchItem = {
  type: string;
  title: string;
  detail: string;
  href?: string;
  icon: Component;
  state: NavigationState;
};

/**
 * Keep global search tied to the same menu contract as the sidebar. A missing
 * href is deliberate: coming modules are discoverable but can never create a
 * broken route.
 */
export function navigationSearchItems(
  permissions?: readonly string[],
  modules?: readonly string[]
): NavigationSearchItem[] {
  const items: NavigationSearchItem[] = [];
  for (const group of navigation) {
    if (!isModuleEnabled(group.module, modules)) continue;
    const groupState = group.state ?? (group.href ? 'active' : 'coming');
    if (group.href || !group.children) {
      if (canOpenNavigation(group, permissions)) {
        items.push({
          type: 'Modül',
          title: group.label,
          detail:
            group.detail ?? (groupState === 'active' ? 'Çalışma alanı' : 'Yakında etkinleşecek'),
          href: groupState === 'active' ? group.href : undefined,
          icon: group.icon,
          state: groupState
        });
      }
      continue;
    }
    for (const child of group.children) {
      if (!canOpenNavigation(child, permissions)) continue;
      const state = child.state ?? (child.href ? 'active' : groupState);
      items.push({
        type: group.label,
        title: child.label,
        detail: child.detail ?? (state === 'active' ? 'İşlem ekranı' : 'Yakında etkinleşecek'),
        href: state === 'active' ? child.href : undefined,
        icon: group.icon,
        state
      });
    }
  }
  return items;
}
