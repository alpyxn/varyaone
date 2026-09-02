import { describe, expect, it } from 'vitest';
import { navigation, navigationSearchItems, visibleNavigation } from './navigation';

describe('Turkish ERP navigation', () => {
  it('keeps familiar primary terminology', () => {
    expect(navigation.map((item) => item.label)).toEqual(
      expect.arrayContaining([
        'Cari',
        'Stok',
        'Satış',
        'Alış',
        'Banka & Kasa',
        'e-Belge',
        'Raporlar',
        'Ayarlar'
      ])
    );
  });

  it('exposes typed sales and purchasing screens with permissions', () => {
    const businessLinks = navigation
      .filter((item) => item.label === 'Satış' || item.label === 'Alış')
      .flatMap((item) => item.children ?? []);
    expect(businessLinks.every((item) => item.state !== 'coming' && item.href)).toBe(true);
    expect(businessLinks.map((item) => item.href)).toEqual(
      expect.arrayContaining([
        '/satis/teklifler',
        '/satis/siparisler',
        '/satis/irsaliyeler',
        '/satis/faturalar',
        '/satis/iadeler',
        '/alis/siparisler',
        '/alis/irsaliyeler',
        '/alis/faturalar',
        '/alis/iadeler'
      ])
    );
    const finance = navigation.find((item) => item.label === 'Banka & Kasa');
    expect(finance?.state).toBe('active');
    expect(finance?.children).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          href: '/finans/hesaplar',
          anyPermission: ['finance.cash_account.read', 'finance.bank_account.read']
        }),
        expect.objectContaining({
          href: '/finans/transferler',
          permission: 'finance.transfer.read'
        })
      ])
    );
  });

  it('shows Banka & Kasa when the session holds only one account-type read permission', () => {
    const bankOnly = visibleNavigation(['finance.bank_account.read']);
    const group = bankOnly.find((item) => item.label === 'Banka & Kasa');
    expect(group?.children?.map((child) => child.label)).toContain('Hesaplar');
  });

  it('lets global search discover active and coming screens with safe routes', () => {
    const items = navigationSearchItems();
    const stock = items.find((item) => item.title === 'Stok Kartları');
    const parties = items.find((item) => item.title === 'Cari Kartlar');
    expect(stock).toMatchObject({ state: 'active', href: '/stok/urunler', type: 'Stok' });
    expect(parties).toMatchObject({ state: 'active', href: '/cari/kartlar' });
  });

  it('hides unauthorized records while keeping explained coming modules', () => {
    const visible = visibleNavigation(['party.read']);
    const labels = visible.flatMap((group) => group.children?.map((child) => child.label) ?? []);
    expect(labels).toContain('Cari Kartlar');
    expect(labels).not.toContain('Stok Kartları');
    expect(visible.find((group) => group.label === 'Stok')).toBeUndefined();
    expect(visible.find((group) => group.label === 'Satış')).toBeUndefined();

    const salesVisible = visibleNavigation(['sales.quote.read']);
    expect(salesVisible.find((group) => group.label === 'Satış')?.children).toEqual([
      expect.objectContaining({ label: 'Teklifler', href: '/satis/teklifler' })
    ]);

    const searchItems = navigationSearchItems(['party.read']);
    expect(searchItems.some((item) => item.title === 'Stok Kartları')).toBe(false);
    expect(searchItems.some((item) => item.title === 'Cari Kartlar')).toBe(true);
  });

  it('does not disclose navigation before session permissions are ready', () => {
    expect(visibleNavigation([], false)).toEqual([]);
  });

  it('hides module-scoped groups when the module is disabled', () => {
    const perms = navigation
      .flatMap((group) => group.children ?? [])
      .flatMap((child) => [child.permission, ...(child.anyPermission ?? [])])
      .filter((code): code is string => Boolean(code));
    const withoutHR = visibleNavigation(perms, true, ['preaccounting', 'fixed_asset']);
    expect(withoutHR.find((group) => group.label === 'İnsan Kaynakları')).toBeUndefined();
    expect(withoutHR.find((group) => group.label === 'Cari')).toBeDefined();

    const preAccountingOnly = visibleNavigation(perms, true, ['preaccounting']);
    expect(preAccountingOnly.find((group) => group.label === 'Sabit Kıymetler')).toBeUndefined();
    expect(preAccountingOnly.find((group) => group.label === 'Raporlar')).toBeDefined();

    // A missing module list (session not loaded) never hides anything.
    expect(
      visibleNavigation(perms, true).find((g) => g.label === 'İnsan Kaynakları')
    ).toBeDefined();

    expect(
      navigationSearchItems(perms, ['preaccounting']).some(
        (item) => item.type === 'İnsan Kaynakları'
      )
    ).toBe(false);
  });
});
