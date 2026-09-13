<script lang="ts">
  import { ChevronRight, Home } from '@lucide/svelte';

  let { pathname = '/' }: { pathname?: string } = $props();

  const labels: Record<string, string> = {
    satis: 'Satış',
    alis: 'Alış',
    teklifler: 'Teklifler',
    siparisler: 'Siparişler',
    irsaliyeler: 'İrsaliyeler',
    faturalar: 'Faturalar',
    iadeler: 'İadeler',
    cari: 'Cari',
    kartlar: 'Cari Kartlar',
    hareketler: 'Hareketler',
    tahsilatlar: 'Tahsilatlar',
    odemeler: 'Ödemeler',
    'vade-planlari': 'Taksit Planları',
    stok: 'Stok',
    urunler: 'Stok Kartları',
    depolar: 'Depolar',
    transferler: 'Transferler',
    sayim: 'Sayım',
    belgeler: 'Belgeler',
    finans: 'Finans',
    hesaplar: 'Finans Hesapları',
    ayarlar: 'Ayarlar',
    firma: 'Şirket',
    tanimlar: 'Tanımlar',
    yeni: 'Yeni',
    raporlar: 'Raporlar',
    'vadesi-gecen-alacaklar': 'Vadesi Geçen Alacaklar',
    'vadesi-gecen-borclar': 'Vadesi Geçen Borçlar',
    'stok-degerleme': 'Stok Değerleme',
    'en-cok-satanlar': 'En Çok Satan Ürünler',
    'satis-karliligi': 'Satış Kârlılığı',
    'vergi-ozeti': 'Vergi Özeti'
  };

  const crumbs = $derived(
    pathname
      .split('/')
      .filter(Boolean)
      .map((segment) => ({
        label: labels[segment] ?? (segment.length > 20 ? 'Detay' : segment),
        href: undefined
      }))
  );
</script>

{#if crumbs.length}
  <nav class="breadcrumbs" aria-label="Konum">
    <a href="/" aria-label="Ana sayfa"><Home size={13} aria-hidden="true" /></a>
    {#each crumbs as crumb, index}
      <ChevronRight size={13} aria-hidden="true" />
      {#if index === crumbs.length - 1}<span aria-current="page">{crumb.label}</span>{:else}<span
          >{crumb.label}</span
        >{/if}
    {/each}
  </nav>
{/if}

<style>
  .breadcrumbs {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 2px 5px;
    min-height: 30px;
    padding: 6px max(var(--page-gutter), env(safe-area-inset-right, 0px)) 0
      max(var(--page-gutter), env(safe-area-inset-left, 0px));
    color: var(--text-muted);
    font-size: 11px;
  }
  .breadcrumbs span {
    min-width: 0;
    overflow-wrap: anywhere;
  }
  .breadcrumbs a {
    display: inline-grid;
    min-width: 24px;
    min-height: 24px;
    place-items: center;
    border-radius: 4px;
    color: var(--text-muted);
  }
  .breadcrumbs a:hover {
    background: var(--surface-muted);
    color: var(--primary);
  }
  .breadcrumbs [aria-current='page'] {
    color: var(--text);
    font-weight: 650;
  }
  @media (max-width: 640px) {
    /* Wrap rather than clip: the last crumb is the current page and is the
       one a phone user most needs to read. */
    .breadcrumbs {
      row-gap: 0;
    }
    .breadcrumbs :not([aria-current='page']) {
      max-width: 12ch;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }
  }
</style>
