/**
 * Record pages and the list each one is reached from.
 *
 * This is the manifest `detail.spec.ts` runs and `routes-inventory.test.ts`
 * checks against `src/routes`. The two used to disagree silently: the
 * inventory accepted any route containing `[id]` as covered, so a record page
 * with no scenario at all counted as tested. A pattern is covered here only
 * when a scenario actually opens it in a browser; everything else is listed as
 * pending, with what is missing.
 */

export type DetailScenario = {
  /** The list page the user starts from. */
  list: string;
  /** The route pattern in `src/routes` that the opened record belongs to. */
  route: string;
  /** The URL the record page must land on. */
  detail: RegExp;
};

const UUID = '[0-9a-f-]{36}';

export const DETAIL_SCENARIOS: DetailScenario[] = [
  { list: '/cari/kartlar', route: '/cari/kartlar/[id]', detail: /\/cari\/kartlar\/[0-9a-f-]{36}$/ },
  {
    list: '/cari/hareketler',
    route: '/cari/hareketler/[id]',
    detail: /\/cari\/hareketler\/[0-9a-f-]{36}$/
  },
  {
    list: '/cari/tahsilatlar',
    route: '/cari/tahsilatlar/[id]',
    detail: /\/cari\/tahsilatlar\/[0-9a-f-]{36}$/
  },
  {
    list: '/cari/odemeler',
    route: '/cari/odemeler/[id]',
    detail: /\/cari\/odemeler\/[0-9a-f-]{36}$/
  },
  { list: '/stok/urunler', route: '/stok/urunler/[id]', detail: /\/stok\/urunler\/[0-9a-f-]{36}$/ },
  {
    list: '/stok/hareketler',
    route: '/stok/hareketler/[id]',
    detail: /\/stok\/hareketler\/[0-9a-f-]{36}$/
  },
  { list: '/stok/depolar', route: '/stok/depolar/[id]', detail: /\/stok\/depolar\/[0-9a-f-]{36}$/ },
  {
    list: '/stok/transferler',
    route: '/stok/transferler/[id]',
    detail: /\/stok\/transferler\/[0-9a-f-]{36}$/
  },
  { list: '/stok/sayim', route: '/stok/sayim/[id]', detail: /\/stok\/sayim\/[0-9a-f-]{36}$/ },
  {
    list: '/satis/faturalar',
    route: '/satis/[resource]/[id]',
    detail: /\/satis\/faturalar\/[0-9a-f-]{36}$/
  },
  {
    list: '/satis/siparisler',
    route: '/satis/[resource]/[id]',
    detail: /\/satis\/siparisler\/[0-9a-f-]{36}$/
  },
  {
    list: '/satis/teklifler',
    route: '/satis/[resource]/[id]',
    detail: /\/satis\/teklifler\/[0-9a-f-]{36}$/
  },
  {
    list: '/satis/irsaliyeler',
    route: '/satis/[resource]/[id]',
    detail: /\/satis\/irsaliyeler\/[0-9a-f-]{36}$/
  },
  {
    list: '/satis/iadeler',
    route: '/satis/[resource]/[id]',
    detail: /\/satis\/iadeler\/[0-9a-f-]{36}$/
  },
  {
    list: '/alis/faturalar',
    route: '/alis/[resource]/[id]',
    detail: /\/alis\/faturalar\/[0-9a-f-]{36}$/
  },
  {
    list: '/alis/siparisler',
    route: '/alis/[resource]/[id]',
    detail: /\/alis\/siparisler\/[0-9a-f-]{36}$/
  },
  {
    list: '/alis/irsaliyeler',
    route: '/alis/[resource]/[id]',
    detail: /\/alis\/irsaliyeler\/[0-9a-f-]{36}$/
  },
  {
    list: '/alis/iadeler',
    route: '/alis/[resource]/[id]',
    detail: /\/alis\/iadeler\/[0-9a-f-]{36}$/
  },
  {
    list: '/finans/hesaplar',
    route: '/finans/hesaplar/[id]',
    detail: /\/finans\/hesaplar\/[0-9a-f-]{36}$/
  },
  {
    list: '/finans/hareketler',
    route: '/finans/hareketler/[id]',
    detail: /\/finans\/hareketler\/[0-9a-f-]{36}$/
  },
  {
    list: '/finans/transferler',
    route: '/finans/transferler/[id]',
    detail: /\/finans\/transferler\/[0-9a-f-]{36}$/
  },
  {
    list: '/personel/calisanlar',
    route: '/personel/calisanlar/[id]',
    detail: /\/personel\/calisanlar\/[0-9a-f-]{36}$/
  },
  {
    list: '/sabit-kiymetler',
    route: '/sabit-kiymetler/[id]',
    detail: /\/sabit-kiymetler\/[0-9a-f-]{36}$/
  }
];

/**
 * Record pages that exist but no scenario opens, and what each one is waiting
 * for. A pending entry is a gap, not a pass: nothing here is counted as
 * covered, and the inventory reports these separately from the working ones.
 */
export const PENDING_DETAIL_ROUTES: Record<string, string> = {
  '/personel/avanslar/[id]':
    'fixture bekliyor: seed hiç avans vermiyor. Avans veren bir fixture eklenince ' +
    'senaryo /personel/avanslar listesinden açılacak.',
  '/personel/bordro/[id]':
    'fixture bekliyor: seed bordro çalıştırmıyor. Bir dönem bordrosu üreten fixture gerekiyor.',
  '/stok/lot-seri/lot/[id]':
    'fixture bekliyor: seed ürünleri lot takipli değil. Lot takipli bir ürün ve girişi gerekiyor.',
  '/stok/lot-seri/seri/[id]':
    'fixture bekliyor: seed ürünleri seri takipli değil. Seri takipli bir ürün ve girişi gerekiyor.',
  '/cari/kartlar/[id]/ekstre':
    'fixture bekliyor: cari kart detayından ekstreye geçen ayrı bir senaryo yok. ' +
    'Kart detayının açılması bu iç sayfayı gezmez.',
  '/finans/hesaplar/[id]/duzenle':
    'fixture bekliyor: hesap düzenleme ekranına giden ayrı bir senaryo yok. ' +
    'Hesap detayının açılması bu iç sayfayı gezmez.'
};

/**
 * Lists the seed leaves empty, with the reason. Kept next to the pending
 * record pages they explain.
 */
export const UNSEEDED_LISTS: Record<string, string> = {
  '/personel/avanslar': 'the seed grants no advances; /personel/avanslar/[id] is uncovered',
  '/personel/bordro': 'the seed runs no payroll; /personel/bordro/[id] is uncovered',
  '/stok/lot-seri': 'the seeded products are not lot- or serial-tracked'
};

/** Every record route a scenario actually opens. */
export function coveredDetailRoutes(): Set<string> {
  return new Set(DETAIL_SCENARIOS.map((scenario) => scenario.route));
}

export { UUID };
