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
    align-items: center;
    gap: 5px;
    min-height: 30px;
    padding: 6px var(--page-gutter) 0;
    color: var(--text-muted);
    font-size: 11px;
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
    .breadcrumbs {
      overflow: hidden;
      white-space: nowrap;
    }
    .breadcrumbs span {
      overflow: hidden;
      text-overflow: ellipsis;
    }
  }
</style>
