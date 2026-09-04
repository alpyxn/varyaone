<script lang="ts">
  import { goto } from '$app/navigation';
  import { Download, Filter, Plus, Search, X } from '@lucide/svelte';
  import { onMount } from 'svelte';
  import { Button } from '$lib/components/ui/button';
  import { api, type Session } from '$lib/api';
  import { Input } from '$lib/components/ui/input';
  import { DocumentToolbar } from '$lib/components/varya/document-toolbar';
  import { type EntityOption } from '$lib/components/varya/entity-picker-dialog';
  import { EntityCombobox } from '$lib/components/varya/entity-combobox';
  import {
    ColumnVisibilityMenu,
    VaryaDataGrid,
    createGridQuery,
    gridQueryToSearchParams,
    type VaryaColumn,
    type VaryaGridQuery
  } from '$lib/components/varya/data-grid';
  import { DocumentStatusCell } from '$lib/components/varya/data-grid/cells';
  import { getTablePreference, saveTablePreference } from '$lib/features/preferences/api';
  import { listProducts } from '$lib/features/products/api';
  import type { Product } from '$lib/features/products/types';
  import { densityPreference } from '$lib/design/density.svelte';
  import { formatQuantity, formatUnitPrice } from '$lib/design/formatters';
  import { listWarehouses } from '$lib/features/warehouses/api';
  import { isActiveStandardWarehouse, type Warehouse } from '$lib/features/warehouses/types';

  type WarehouseOption = EntityOption & { warehouse: Warehouse };

  const initialHiddenColumns: Record<string, boolean> = {
    description: false,
    created_at: false,
    updated_at: false,
    version: false
  };
  let rows = $state<Product[]>([]);
  let baseCurrency = $state('TRY');
  let loading = $state(true);
  let loadingMore = $state(false);
  let error = $state('');
  let search = $state('');
  let includeInactive = $state(false);
  let warehouseID = $state('');
  let warehouses = $state<Warehouse[]>([]);
  let warehouseLoading = $state(true);
  let nextCursor = $state<string>();
  let query = $state<VaryaGridQuery>(createGridQuery(50));
  let columnVisibility = $state<Record<string, boolean>>({ ...initialHiddenColumns });
  let columnVisibilityLoaded = $state(false);
  let visibilitySaveError = $state('');
  let activeRequest: AbortController | undefined;
  let visibilityRequest: AbortController | undefined;
  let warehouseRequest: AbortController | undefined;
  let debounce: ReturnType<typeof setTimeout>;
  let requestSequence = 0;

  const warehouseOptions = $derived<WarehouseOption[]>(
    warehouses.map((warehouse) => ({
      id: warehouse.id,
      title: warehouse.name,
      subtitle: warehouse.code || 'Kod tanımsız',
      meta: warehouse.address || undefined,
      warehouse
    }))
  );
  const selectedWarehouse = $derived(
    warehouseOptions.find((warehouse) => warehouse.id === warehouseID)
  );

  const columns: VaryaColumn<Product>[] = [
    {
      id: 'code',
      header: 'Stok Kodu',
      accessor: (row) => row.code,
      width: 145,
      sortable: true,
      link: (row) => `/stok/urunler/${row.id}`
    },
    {
      id: 'name',
      header: 'Stok / Hizmet Adı',
      accessor: (row) => row.name,
      width: 260,
      sortable: true,
      link: (row) => `/stok/urunler/${row.id}`
    },
    {
      id: 'kind',
      header: 'Kart Türü',
      accessor: (row) => (row.kind === 'SERVICE' ? 'Hizmet' : 'Fiziksel Ürün'),
      width: 135
    },
    {
      id: 'purchase_price',
      header: 'Alış Fiyatı',
      accessor: (row) => formatUnitPrice(row.purchase_price || '0', baseCurrency),
      width: 125,
      align: 'right'
    },
    {
      id: 'net_price',
      header: 'Net Fiyat',
      accessor: (row) => formatUnitPrice(row.net_price || row.sales_price || '0', baseCurrency),
      width: 125,
      align: 'right'
    },
    { id: 'category_name', header: 'Kategori', accessor: (row) => row.category_name, width: 150 },
    { id: 'brand_name', header: 'Marka', accessor: (row) => row.brand_name, width: 140 },
    {
      id: 'barcode_summary',
      header: 'Barkodlar',
      accessor: (row) => row.barcode_summary,
      width: 190
    },
    {
      id: 'available_quantity',
      header: 'Kullanılabilir Stok',
      accessor: (row) =>
        row.kind === 'SERVICE'
          ? '—'
          : `${formatQuantity(row.available_quantity || '0')} ${row.stock_unit || ''}`.trim(),
      width: 155,
      align: 'right'
    },
    {
      id: 'stock_unit',
      header: 'Stok Birimi',
      accessor: (row) => (row.kind === 'SERVICE' ? '—' : row.stock_unit || '—'),
      width: 100
    },
    {
      id: 'variants',
      header: 'Varyant',
      accessor: (row) =>
        row.variants_enabled
          ? `${row.variant_summary?.active_count ?? 0} aktif / ${row.variant_summary?.count ?? 0}`
          : 'Yok',
      width: 125,
      align: 'center'
    },
    {
      id: 'description',
      header: 'Açıklama',
      accessor: (row) => row.description,
      width: 260,
      defaultVisible: false
    },
    {
      id: 'status',
      header: 'Durum',
      accessor: (row) => (row.is_active ? 'ACTIVE' : 'INACTIVE'),
      width: 90,
      cell: DocumentStatusCell
    },
    {
      id: 'created_at',
      header: 'Oluşturulma',
      accessor: (row) => row.created_at,
      width: 130,
      defaultVisible: false
    },
    {
      id: 'updated_at',
      header: 'Güncellenme',
      accessor: (row) => row.updated_at,
      width: 130,
      defaultVisible: false
    }
  ];

  async function load(append = false) {
    activeRequest?.abort();
    const request = new AbortController();
    activeRequest = request;
    const sequence = ++requestSequence;
    append ? (loadingMore = true) : (loading = true);
    error = '';
    if (!append) nextCursor = undefined;
    try {
      const params = gridQueryToSearchParams(query);
      if (includeInactive) params.set('include_inactive', 'true');
      if (warehouseID) params.set('warehouse_id', warehouseID);
      const result = await listProducts(params, request.signal);
      if (sequence !== requestSequence) return;
      rows = append ? [...rows, ...result.items] : result.items;
      nextCursor = result.next_cursor;
    } catch (cause) {
      if (cause instanceof DOMException && cause.name === 'AbortError') return;
      if (sequence !== requestSequence) return;
      error =
        typeof cause === 'object' && cause && 'message' in cause
          ? String(cause.message)
          : 'Stok kartları alınamadı.';
    } finally {
      if (sequence === requestSequence) {
        loading = false;
        loadingMore = false;
      }
    }
  }

  function applySearch(event: Event) {
    const nextSearch = (event.currentTarget as HTMLInputElement).value;
    search = nextSearch;
    clearTimeout(debounce);
    debounce = setTimeout(() => {
      query = {
        ...query,
        search: nextSearch.trim() || undefined,
        pagination: { mode: 'cursor', pageSize: 50 }
      };
      void load();
    }, 250);
  }

  function clearSearch() {
    clearTimeout(debounce);
    search = '';
    query = { ...query, search: undefined, pagination: { mode: 'cursor', pageSize: 50 } };
    void load();
  }

  function toggleInactive() {
    includeInactive = !includeInactive;
    query = { ...query, pagination: { mode: 'cursor', pageSize: 50 } };
    void load();
  }

  function selectWarehouse(option: WarehouseOption) {
    warehouseID = option.id;
    query = { ...query, pagination: { mode: 'cursor', pageSize: 50 } };
    void load();
  }

  function clearWarehouse() {
    warehouseID = '';
    query = { ...query, pagination: { mode: 'cursor', pageSize: 50 } };
    void load();
  }

  function clearAllFilters() {
    clearTimeout(debounce);
    search = '';
    includeInactive = false;
    warehouseID = '';
    query = {
      ...query,
      search: undefined,
      filters: [],
      pagination: { mode: 'cursor', pageSize: 50 }
    };
    void load();
  }

  function loadMore() {
    if (!nextCursor || query.pagination.mode !== 'cursor') return;
    query = { ...query, pagination: { ...query.pagination, cursor: nextCursor } };
    void load(true);
  }

  async function loadColumnVisibility(signal: AbortSignal) {
    try {
      const preference = await getTablePreference('stok-urunler', signal);
      columnVisibility =
        preference.version > 0 ? preference.column_visibility || {} : { ...initialHiddenColumns };
    } catch (cause) {
      if (cause instanceof DOMException && cause.name === 'AbortError') return;
    } finally {
      if (!signal.aborted) columnVisibilityLoaded = true;
    }
  }

  async function loadWarehouseOptions(signal: AbortSignal) {
    warehouseLoading = true;
    try {
      const result = await listWarehouses({ signal });
      warehouses = (result.items ?? []).filter(isActiveStandardWarehouse);
    } catch (cause) {
      if (!(cause instanceof DOMException && cause.name === 'AbortError')) warehouses = [];
    } finally {
      if (!signal.aborted) warehouseLoading = false;
    }
  }

  async function saveColumnVisibility(next: Record<string, boolean>) {
    columnVisibility = next;
    if (!columnVisibilityLoaded) return;
    try {
      await saveTablePreference('stok-urunler', next);
      visibilitySaveError = '';
    } catch {
      visibilitySaveError = 'Görünüm tercihi kaydedilemedi.';
    }
  }

  onMount(() => {
    void (async () => {
      try {
        const session = await api<Session>('/session');
        baseCurrency =
          session.companies.find((item) => item.id === session.current_company_id)?.base_currency ||
          'TRY';
      } catch {
        // The product API remains authoritative; TRY is only the display fallback.
      }
    })();
    void load();
    visibilityRequest = new AbortController();
    warehouseRequest = new AbortController();
    void loadColumnVisibility(visibilityRequest.signal);
    void loadWarehouseOptions(warehouseRequest.signal);
    return () => {
      clearTimeout(debounce);
      activeRequest?.abort();
      visibilityRequest?.abort();
      warehouseRequest?.abort();
    };
  });
</script>

<svelte:head><title>Stok Kartları · Varya One</title></svelte:head>
<DocumentToolbar title="Stok Kartları">
  {#snippet primary()}<a class="button" href="/stok/urunler/yeni"
      ><Plus size={15} />Yeni Stok Kartı</a
    >{/snippet}
  {#snippet tools()}<div class="search-box">
      <span class="search-icon"><Search size={15} /></span><Input
        class="product-search"
        bind:value={search}
        oninput={applySearch}
        onkeydown={(event) => event.key === 'Escape' && clearSearch()}
        maxlength={256}
        placeholder="Tüm alanlarda ara (kod, ad, barkod, kategori…)"
        aria-label="Stok kartı ara"
      />{#if search}<button
          class="clear-search"
          type="button"
          aria-label="Stok kartı aramasını temizle"
          onclick={clearSearch}><X size={14} /></button
        >{/if}
    </div>
    <div class="warehouse-filter">
      <EntityCombobox
        selected={selectedWarehouse}
        results={warehouseOptions}
        onSelect={selectWarehouse}
        title="Depoya göre filtrele"
        description="Stok kartlarının seçilen depodaki kullanılabilir miktarını gösterir."
        triggerLabel="Depo filtresi"
        triggerPlaceholder="Tüm depolar"
        searchPlaceholder="Depo adı, kodu veya adresi ara"
        initialEmptyText="Yetkili depolar yükleniyor…"
        emptyText="Eşleşen depo bulunamadı."
        loading={warehouseLoading}
        disabled={warehouseLoading}
      />
      {#if warehouseID}<Button variant="ghost" size="sm" onclick={clearWarehouse}
          ><X size={14} />Depo filtresini kaldır</Button
        >{/if}
    </div>
    <Button variant={includeInactive ? 'default' : 'outline'} onclick={toggleInactive}
      ><Filter size={14} />{includeInactive ? 'Pasifleri gizle' : 'Pasifleri göster'}</Button
    >{#if includeInactive || search || warehouseID || query.filters.length}<Button
        variant="ghost"
        onclick={clearAllFilters}
        title="Arama ve filtreleri kaldır"><X size={14} />Tümünü kaldır</Button
      >{/if}<ColumnVisibilityMenu
      {columns}
      bind:value={columnVisibility}
      disabled={!columnVisibilityLoaded}
      onChange={saveColumnVisibility}
    /><Button variant="outline" disabled title="Dışa aktarma ilerleyen fazda açılacak"
      ><Download size={14} />Dışa Aktar</Button
    >{/snippet}
</DocumentToolbar>
{#if visibilitySaveError}<p class="preference-error" role="status">{visibilitySaveError}</p>{/if}
<VaryaDataGrid
  {columns}
  data={rows}
  getRowId={(row) => row.id}
  density={densityPreference.value}
  resizable
  stickyHeader
  virtualized
  {columnVisibility}
  onColumnVisibilityChange={saveColumnVisibility}
  {loading}
  {error}
  emptyTitle="Stok kartı yok"
  emptyDescription={search || query.filters.length
    ? 'Arama veya filtreye uyan stok kartı bulunamadı.'
    : 'İlk ürün veya hizmet kartınızı oluşturun.'}
  {query}
  onQueryChange={(next) => {
    query = next;
    void load();
  }}
  onRetry={() => load()}
  onRowOpen={(row) => goto(`/stok/urunler/${row.id}`)}
  {nextCursor}
  onLoadMore={loadMore}
  {loadingMore}
/>

<style>
  .search-box {
    position: relative;
    width: min(360px, 40vw);
    display: flex;
    align-items: center;
  }
  .warehouse-filter {
    display: flex;
    align-items: center;
    gap: 4px;
    min-width: 190px;
  }
  .warehouse-filter :global(.entity-combobox) {
    flex: 1;
    min-width: 170px;
  }
  .search-icon {
    position: absolute;
    left: 8px;
    color: var(--text-muted);
    z-index: 1;
  }
  .search-box :global(.product-search) {
    padding-left: 29px;
    padding-right: 30px;
  }
  .clear-search {
    position: absolute;
    right: 5px;
    display: grid;
    place-items: center;
    width: 24px;
    height: 24px;
    border: 0;
    border-radius: 4px;
    background: transparent;
    color: var(--text-muted);
    cursor: pointer;
  }
  .clear-search:hover {
    background: var(--surface-muted);
    color: var(--text);
  }
  .preference-error,
  .keyboard-hint {
    color: var(--text-muted);
    font-size: 10.5px;
  }
  .preference-error {
    margin: -7px 2px 7px;
    color: var(--danger);
    text-align: right;
  }
  @media (max-width: 640px) {
    .search-box {
      min-width: 230px;
      width: 70vw;
    }
  }
</style>
