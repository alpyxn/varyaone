/**
 * Every page a signed-in user can reach without a record id.
 *
 * Each entry carries what the old list did not: the URL the browser must
 * actually end up on, and the heading that page must show. Measuring layout on
 * whatever rendered was the suite's biggest blind spot — a login redirect, an
 * alias sending the visitor somewhere else and an error screen all fit their
 * viewport perfectly.
 *
 * `routes.inventory.test.ts` compares this list against `src/routes`, so a new
 * page cannot quietly fall outside the suite.
 */
export type RouteSpec = {
  /** The path the test navigates to. */
  path: string;
  /** Where it must end up. Set only when the app redirects. */
  lands?: string;
  /** Text of the page's `<h1>`, or of the element named below. */
  heading: string;
  /** Rendered without the app shell (the one-time flows). */
  chromeless?: boolean;
};

/**
 * Pages with no `<h1>` of their own, and the element that identifies them.
 * The dashboard is the only one: it leads with the logo and groups its
 * shortcuts under section headings.
 */
export const HEADLESS_ROUTES: Record<string, { role: 'region' | 'heading'; name: string }> = {
  '/': { role: 'region', name: 'Kısayollar' }
};

export const STATIC_ROUTES: RouteSpec[] = [
  // Identified by HEADLESS_ROUTES: the workspace has no <h1>.
  { path: '/', heading: '' },
  { path: '/aktarimlar', heading: 'Aktarımlar' },
  { path: '/e-belge', heading: 'Üzerinde çalışıyoruz' },
  { path: '/firma-ekle', heading: 'Yeni firma oluştur', chromeless: true },

  { path: '/cari/kartlar', heading: 'Cari Kartlar' },
  { path: '/cari/kartlar/yeni', heading: 'Yeni Cari' },
  { path: '/cari/hareketler', heading: 'Cari Hareketler' },
  { path: '/cari/tahsilatlar', heading: 'Tahsilatlar' },
  { path: '/cari/odemeler', heading: 'Ödemeler' },
  { path: '/cari/vade-planlari', heading: 'Taksit Planları' },
  { path: '/cari/yaslandirma', heading: 'Cari Yaşlandırma' },

  { path: '/stok/urunler', heading: 'Stok Kartları' },
  { path: '/stok/urunler/yeni', heading: 'Yeni Stok Kartı' },
  { path: '/stok/hareketler', heading: 'Stok Hareketleri' },
  { path: '/stok/depolar', heading: 'Depolar' },
  { path: '/stok/transferler', heading: 'Depo Transferleri' },
  { path: '/stok/lot-seri', heading: 'Lot / Partiler' },
  { path: '/stok/sayim', heading: 'Stok sayımları' },

  { path: '/satis/teklifler', heading: 'Satış Teklifleri' },
  { path: '/satis/siparisler', heading: 'Satış Siparişleri' },
  { path: '/satis/irsaliyeler', heading: 'Satış İrsaliyeleri' },
  { path: '/satis/faturalar', heading: 'Satış Faturaları' },
  { path: '/satis/iadeler', heading: 'Satış İadeleri' },
  { path: '/satis/teklifler/yeni', heading: 'Yeni Satış Teklifi' },
  { path: '/satis/siparisler/yeni', heading: 'Yeni Satış Siparişi' },
  { path: '/satis/irsaliyeler/yeni', heading: 'Yeni Satış İrsaliyesi' },
  { path: '/satis/faturalar/yeni', heading: 'Yeni Satış Faturası' },
  { path: '/satis/iadeler/yeni', heading: 'Yeni Satış İadesi' },

  { path: '/alis/siparisler', heading: 'Alış Siparişleri' },
  { path: '/alis/irsaliyeler', heading: 'Alış İrsaliyeleri' },
  { path: '/alis/faturalar', heading: 'Alış Faturaları' },
  { path: '/alis/iadeler', heading: 'Alış İadeleri' },
  { path: '/alis/siparisler/yeni', heading: 'Yeni Alış Siparişi' },
  { path: '/alis/irsaliyeler/yeni', heading: 'Yeni Alış İrsaliyesi' },
  { path: '/alis/faturalar/yeni', heading: 'Yeni Alış Faturası' },
  { path: '/alis/iadeler/yeni', heading: 'Yeni Alış İadesi' },

  { path: '/finans/hesaplar', heading: 'Banka & Kasa Hesapları' },
  { path: '/finans/hesaplar/yeni', heading: 'Yeni kasa hesabı' },
  { path: '/finans/hareketler', heading: 'Hesap Hareketleri' },
  { path: '/finans/transferler', heading: 'Hesap Transferleri' },
  { path: '/finans/transferler/yeni', heading: 'Yeni hesap transferi' },

  // The kasa/banka paths are 308 aliases onto the finance screens. The suite
  // used to walk them without noticing they never stayed where they were sent.
  {
    path: '/kasa/hesaplar',
    lands: '/finans/hesaplar?type=CASH',
    heading: 'Banka & Kasa Hesapları'
  },
  { path: '/kasa/hareketler', lands: '/finans/hareketler', heading: 'Hesap Hareketleri' },
  { path: '/kasa/transferler', lands: '/finans/transferler', heading: 'Hesap Transferleri' },
  {
    path: '/banka/hesaplar',
    lands: '/finans/hesaplar?type=BANK',
    heading: 'Banka & Kasa Hesapları'
  },
  { path: '/banka/hareketler', lands: '/finans/hareketler', heading: 'Hesap Hareketleri' },
  { path: '/banka/transferler', lands: '/finans/transferler', heading: 'Hesap Transferleri' },

  { path: '/personel/calisanlar', heading: 'Çalışanlar' },
  { path: '/personel/calisanlar/yeni', heading: 'Yeni çalışan' },
  { path: '/personel/avanslar', heading: 'Personel Avansları' },
  { path: '/personel/izinler', heading: 'İzin Türleri' },
  { path: '/personel/plan', heading: 'Çalışma Planı' },
  { path: '/personel/puantaj', heading: 'Puantaj' },
  { path: '/personel/bordro', heading: 'Bordro' },

  { path: '/sabit-kiymetler', heading: 'Sabit Kıymetler' },

  {
    path: '/raporlar',
    lands: '/raporlar/vadesi-gecen-alacaklar',
    heading: 'Vadesi Geçen Alacaklar'
  },
  { path: '/raporlar/vadesi-gecen-alacaklar', heading: 'Vadesi Geçen Alacaklar' },
  { path: '/raporlar/vadesi-gecen-borclar', heading: 'Vadesi Geçen Borçlar' },
  { path: '/raporlar/stok-degerleme', heading: 'Stok Değerleme' },
  { path: '/raporlar/en-cok-satanlar', heading: 'En Çok Satan Ürünler' },
  { path: '/raporlar/satis-karliligi', heading: 'Satış Kârlılığı' },
  { path: '/raporlar/vergi-ozeti', heading: 'Vergi Özeti' },

  { path: '/ayarlar/firma', heading: 'Şirket bilgileri' },
  { path: '/ayarlar/kullanicilar', heading: 'Kullanıcılar ve roller' },
  { path: '/ayarlar/guvenlik', heading: 'Oturum ve entegrasyon güvenliği' },
  { path: '/ayarlar/moduller', heading: 'Modüller' },
  { path: '/ayarlar/tanimlar', heading: 'Tanımlar' },
  { path: '/ayarlar/tanimlar/kategoriler', heading: 'Stok kategorileri' },
  { path: '/ayarlar/tanimlar/markalar', heading: 'Stok markaları' },
  { path: '/ayarlar/tanimlar/varyantlar', heading: 'Varyant tanımları' },
  { path: '/ayarlar/tanimlar/cari-gruplari', heading: 'Cari grupları' },
  { path: '/ayarlar/tanimlar/fiyat-listeleri', heading: 'Fiyat tanımları' },
  { path: '/ayarlar/tanimlar/vergi-tanimlari', heading: 'Vergi tanımları' },
  {
    path: '/ayarlar/tanimlar/sabit-kiymet-kategorileri',
    heading: 'Sabit kıymet kategorileri'
  },
  { path: '/ayarlar/tanimlar/asgari-ucret', heading: 'Asgari ücret' },
  {
    path: '/ayarlar/cari-gruplari',
    lands: '/ayarlar/tanimlar/cari-gruplari',
    heading: 'Cari grupları'
  },
  {
    path: '/ayarlar/fiyat-listeleri',
    lands: '/ayarlar/tanimlar/fiyat-listeleri',
    heading: 'Fiyat tanımları'
  },
  {
    path: '/ayarlar/vergi-tanimlari',
    lands: '/ayarlar/tanimlar/vergi-tanimlari',
    heading: 'Vergi tanımları'
  },
  { path: '/ayarlar/doviz-kurlari', heading: 'Döviz kurları' },
  { path: '/ayarlar/e-posta', heading: 'E-posta Ayarları' },
  { path: '/ayarlar/e-posta-taslaklari', heading: 'E-posta taslakları' },
  { path: '/ayarlar/yedekleme', heading: 'Yedekleme' },
  { path: '/ayarlar/program', heading: 'Program ayarları' }
];

/** Reachable without a session. */
export const PUBLIC_ROUTES: RouteSpec[] = [{ path: '/giris', heading: 'Tekrar hoş geldiniz' }];

/**
 * Pages the suite deliberately leaves out, each with the reason.
 *
 * The inventory test reads this, so "not covered" is a decision recorded in one
 * place rather than an omission nobody notices. It is not a skip: these pages
 * are never visited, and the list is short on purpose.
 */
export const EXCLUDED_ROUTES: Record<string, string> = {
  '/giris': 'public route; covered by PUBLIC_ROUTES and auth.setup.ts',
  '/kurulum':
    'the one-time setup wizard. It is only reachable before an installation has ' +
    'a company, which is the one state the seeded environment never has.',
  '/belgeler':
    'calls GET /api/v1/documents, which the backend does not implement. The page ' +
    'ships ahead of its API; covering it would add a permanent red to CI for a ' +
    'feature that has not been built.',
  '/belgeler/[id]': 'see /belgeler',
  '/raporlar/[report]': 'expanded to its six registered report ids in STATIC_ROUTES',
  '/satis/[resource]': 'expanded to its five sales slugs in STATIC_ROUTES',
  '/satis/[resource]/yeni': 'expanded to its five sales slugs in STATIC_ROUTES',
  '/alis/[resource]': 'expanded to its four purchasing slugs in STATIC_ROUTES',
  '/alis/[resource]/yeni': 'expanded to its four purchasing slugs in STATIC_ROUTES'
};

/**
 * Record pages are declared in `detail-scenarios.ts`, next to the scenario
 * that opens each one. Listing them here as well let a pattern count as
 * covered because it was written down, which is not the same thing as a
 * browser having visited it.
 */
export { DETAIL_SCENARIOS, PENDING_DETAIL_ROUTES, coveredDetailRoutes } from './detail-scenarios';
