<script lang="ts">
  import { afterNavigate, goto } from '$app/navigation';
  import { Download, Filter, Plus, Search, X } from '@lucide/svelte';
  import { onMount } from 'svelte';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { DocumentToolbar } from '$lib/components/varya/document-toolbar';
  import {
    ColumnVisibilityMenu,
    VaryaDataGrid,
    createGridQuery,
    gridQueryToSearchParams,
    type VaryaColumn,
    type VaryaGridQuery
  } from '$lib/components/varya/data-grid';
  import { createFilterEngine } from '$lib/filtering';
  import { DateCell, DocumentStatusCell, PartyCell } from '$lib/components/varya/data-grid/cells';
  import { getTablePreference, saveTablePreference } from '$lib/features/preferences/api';
  import { listParties } from '$lib/features/parties/api';
  import type { Party } from '$lib/features/parties/types';
  import PartyTypeCell from '$lib/features/parties/PartyTypeCell.svelte';
  import PartyBalanceCell from '$lib/features/parties/PartyBalanceCell.svelte';
  import PartyMoneyCell from '$lib/features/parties/PartyMoneyCell.svelte';
  import RiskCell from '$lib/features/parties/RiskCell.svelte';
  import { densityPreference } from '$lib/design/density.svelte';
  import { formatQuantity } from '$lib/design/formatters';
  const initialHiddenColumns: Record<string, boolean> = {
    first_name: false,
    last_name: false,
    contact_summary: false,
    group_summary: false,
    tag_summary: false,
    custom_field_summary: false,
    default_currency: false,
    payment_term: false,
    price_list: false,
    sales_rep: false,
    default_discount_rate: false,
    credit_limit: false,
    risk_limit: false,
    created_at: false,
    updated_at: false,
    version: false
  };
  let rows = $state<Party[]>([]);
  let loading = $state(true);
  let loadingMore = $state(false);
  let error = $state('');
  let nextCursor = $state<string>();
  let query = $state<VaryaGridQuery>(createGridQuery(50));
  let search = $state('');
  let includeInactive = $state(false);
  let columnVisibility = $state<Record<string, boolean>>({ ...initialHiddenColumns });
  let columnVisibilityLoaded = $state(false);
  let visibilitySaveError = $state('');
  let debounce: ReturnType<typeof setTimeout>;
  let visibilitySaveTimer: ReturnType<typeof setTimeout>;
  let visibilitySaveInFlight = false;
  let visibilitySavePending = false;
  let latestVisibility = {} as Record<string, boolean>;
  let visibilityRequest: AbortController | undefined;
  let activeRequest: AbortController | undefined;
  let requestSequence = 0;
  const partyFilterEngine = createFilterEngine<Party>();
  const text = (...values: Array<string | null | undefined>) =>
    values.find((value) => Boolean(value?.trim())) ?? '';
  const roleValue = (row: Party) =>
    row.is_customer && row.is_supplier ? 'both' : row.is_customer ? 'customer' : 'supplier';
  const rateValue = (value: string) => `${formatQuantity(value || '0')}%`;
  const columns: VaryaColumn<Party>[] = [
    { id: 'code', header: 'Cari Kodu', accessor: (row) => row.code, width: 120 },
    {
      id: 'trade_name',
      header: 'Ticari Ad',
      accessor: (row) => text(row.trade_name, row.display_name),
      width: 260,
      cell: PartyCell
    },
    {
      id: 'legal_name',
      header: 'Resmî Unvan',
      accessor: (row) => row.legal_name,
      width: 260
    },
    {
      id: 'first_name',
      header: 'Ad',
      accessor: (row) => row.first_name,
      width: 140,
      defaultVisible: false
    },
    {
      id: 'last_name',
      header: 'Soyad',
      accessor: (row) => row.last_name,
      width: 140,
      defaultVisible: false
    },
    {
      id: 'kind',
      header: 'Cari Türü',
      accessor: (row) => (row.kind === 'PERSON' ? 'Kişi' : 'Kurum'),
      width: 110
    },
    {
      id: 'roles',
      header: 'Cari Rolü',
      accessor: roleValue,
      width: 165,
      cell: PartyTypeCell
    },
    { id: 'tax_number', header: 'Vergi Numarası', accessor: (row) => row.tax_number, width: 135 },
    {
      id: 'identity_number',
      header: 'T.C. Kimlik No',
      accessor: (row) => row.identity_number,
      width: 135
    },
    { id: 'tax_office', header: 'Vergi Dairesi', accessor: (row) => row.tax_office, width: 150 },
    { id: 'phone', header: 'Telefon', accessor: (row) => row.phone, width: 130 },
    { id: 'email', header: 'E-posta', accessor: (row) => row.email, width: 220 },
    { id: 'address_summary', header: 'Adres', accessor: (row) => row.address_summary, width: 300 },
    { id: 'city', header: 'Şehir', accessor: (row) => row.city, width: 130 },
    {
      id: 'contact_summary',
      header: 'İletişim Detayı',
      accessor: (row) => row.contact_summary,
      width: 320,
      defaultVisible: false
    },
    {
      id: 'group_summary',
      header: 'Cari Grubu',
      accessor: (row) => row.group_summary,
      width: 180,
      defaultVisible: false
    },
    {
      id: 'tag_summary',
      header: 'Etiketler',
      accessor: (row) => row.tag_summary,
      width: 180,
      defaultVisible: false
    },
    {
      id: 'custom_field_summary',
      header: 'Özel Alanlar',
      accessor: (row) => row.custom_field_summary,
      width: 260,
      defaultVisible: false
    },
    {
      id: 'default_currency',
      header: 'Para Birimi',
      accessor: (row) => row.default_currency,
      width: 105,
      defaultVisible: false
    },
    {
      id: 'payment_term',
      header: 'Ödeme Koşulu',
      accessor: (row) => text(row.payment_term_name, row.payment_term_id) || 'Peşin',
      width: 190,
      defaultVisible: false
    },
    {
      id: 'price_list',
      header: 'Fiyat Listesi',
      accessor: (row) => row.price_list_id,
      width: 170,
      defaultVisible: false
    },
    {
      id: 'sales_rep',
      header: 'Satış Temsilcisi',
      accessor: (row) => text(row.sales_rep_name, row.sales_rep_user_id),
      width: 180,
      defaultVisible: false
    },
    {
      id: 'default_discount_rate',
      header: 'İskonto',
      accessor: (row) => rateValue(row.default_discount_rate),
      width: 95,
      align: 'right',
      defaultVisible: false
    },
    {
      id: 'credit_limit',
      header: 'Kredi Limiti',
      accessor: (row) => row.credit_limit,
      width: 135,
      align: 'right',
      cell: PartyMoneyCell,
      defaultVisible: false
    },
    {
      id: 'risk_limit',
      header: 'Risk Limiti',
      accessor: (row) => row.risk_limit,
      width: 135,
      align: 'right',
      cell: PartyMoneyCell,
      defaultVisible: false
    },
    {
      id: 'balance',
      header: 'Bakiye',
      accessor: (row) => row.balance,
      width: 135,
      align: 'right',
      cell: PartyBalanceCell
    },
    {
      id: 'risk_policy',
      header: 'Risk',
      accessor: (row) => row.risk_policy,
      width: 90,
      cell: RiskCell
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
      cell: DateCell,
      defaultVisible: false
    },
    {
      id: 'updated_at',
      header: 'Güncellenme',
      accessor: (row) => row.updated_at,
      width: 130,
      cell: DateCell,
      defaultVisible: false
    }
  ];
  async function load(append = false) {
    activeRequest?.abort();
    const request = new AbortController();
    activeRequest = request;
    const sequence = ++requestSequence;
    let timedOut = false;
    const timeout = setTimeout(() => {
      timedOut = true;
      request.abort();
    }, 15000);
    if (!append) nextCursor = undefined;
    append ? (loadingMore = true) : (loading = true);
    error = '';
    try {
      const params = gridQueryToSearchParams(query);
      if (includeInactive) params.set('include_inactive', 'true');
      const result = await listParties(params, request.signal);
      if (sequence !== requestSequence) return;
      rows = append ? [...rows, ...result.items] : result.items;
      nextCursor = result.next_cursor;
    } catch (cause) {
      if (cause instanceof DOMException && cause.name === 'AbortError' && !timedOut) return;
      if (sequence !== requestSequence) return;
      error = timedOut
        ? 'Cari listesi 15 saniye içinde alınamadı. Bağlantıyı kontrol edip tekrar deneyin.'
        : typeof cause === 'object' && cause && 'message' in cause
          ? String(cause.message)
          : 'Cari listesi alınamadı.';
    } finally {
      clearTimeout(timeout);
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
      const rawSearch = nextSearch.trim();
      const normalizedSearch = partyFilterEngine.normalizeSearch(nextSearch);
      query = {
        ...query,
        // Keep punctuation-only input in the request so the API can return an
        // empty result set instead of treating it as "no search".
        search: rawSearch && !normalizedSearch ? rawSearch : normalizedSearch || undefined,
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
  function updateQuery(next: VaryaGridQuery) {
    query = next;
    void load();
  }
  function toggleInactive() {
    includeInactive = !includeInactive;
    // Filter değişince eski cursor artık aynı veri kümesine ait olmayabilir.
    query = { ...query, pagination: { mode: 'cursor', pageSize: 50 } };
    void load();
  }
  function hasActiveFilters() {
    return includeInactive || Boolean(query.search) || query.filters.length > 0;
  }
  function clearAllFilters() {
    clearTimeout(debounce);
    search = '';
    includeInactive = false;
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
      const preference = await getTablePreference('cari-kartlar', signal);
      columnVisibility =
        preference.version > 0 ? preference.column_visibility || {} : { ...initialHiddenColumns };
    } catch (cause) {
      if (cause instanceof DOMException && cause.name === 'AbortError') return;
      // A missing/unavailable preference must not prevent cari cards from opening.
    } finally {
      if (!signal.aborted) columnVisibilityLoaded = true;
    }
  }

  function saveColumnVisibility(next: Record<string, boolean>) {
    columnVisibility = next;
    if (!columnVisibilityLoaded) return;
    latestVisibility = { ...next };
    visibilitySavePending = true;
    clearTimeout(visibilitySaveTimer);
    visibilitySaveTimer = setTimeout(() => void flushColumnVisibility(), 180);
  }

  async function flushColumnVisibility() {
    if (visibilitySaveInFlight || !visibilitySavePending) return;
    visibilitySaveInFlight = true;
    visibilitySavePending = false;
    const snapshot = { ...latestVisibility };
    try {
      await saveTablePreference('cari-kartlar', snapshot);
      visibilitySaveError = '';
    } catch {
      visibilitySaveError = 'Görünüm tercihi kaydedilemedi.';
    } finally {
      visibilitySaveInFlight = false;
      if (visibilitySavePending) void flushColumnVisibility();
    }
  }

  onMount(() => {
    void load();
    visibilityRequest = new AbortController();
    void loadColumnVisibility(visibilityRequest.signal);
    return () => {
      clearTimeout(debounce);
      clearTimeout(visibilitySaveTimer);
      activeRequest?.abort();
      visibilityRequest?.abort();
    };
  });
  afterNavigate(({ from, to }) => {
    if (to?.url.pathname === '/cari/kartlar' && from?.url.pathname.startsWith('/cari/kartlar/')) {
      void load();
    }
  });
</script>

<svelte:head><title>Cari Kartlar · Varya One</title></svelte:head>
<DocumentToolbar title="Cari Kartlar">
  {#snippet primary()}<a class="button" href="/cari/kartlar/yeni"><Plus size={15} />Yeni Cari</a
    >{/snippet}
  {#snippet tools()}<div class="search-box">
      <span class="search-icon"><Search size={15} /></span><Input
        class="party-search"
        bind:value={search}
        oninput={applySearch}
        onkeydown={(event) => event.key === 'Escape' && clearSearch()}
        maxlength={256}
        placeholder="Tüm alanlarda ara (ticari ad, kod, adres, iletişim…)"
        aria-label="Cari ara"
      />{#if search}<button
          class="clear-search"
          type="button"
          aria-label="Cari aramasını temizle"
          onclick={clearSearch}><X size={14} /></button
        >{/if}
    </div>
    <Button variant={includeInactive ? 'default' : 'outline'} onclick={toggleInactive}
      ><Filter size={14} />{includeInactive ? 'Pasifleri gizle' : 'Pasifleri göster'}</Button
    >{#if hasActiveFilters()}<Button
        variant="ghost"
        onclick={clearAllFilters}
        title="Arama ve tüm filtreleri kaldır"><X size={14} />Tümünü kaldır</Button
      >{/if}
    ><ColumnVisibilityMenu
      {columns}
      bind:value={columnVisibility}
      disabled={!columnVisibilityLoaded}
      onChange={saveColumnVisibility}
    /><Button variant="outline" disabled title="Dışa aktarma yakında kullanılacak"
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
  emptyTitle="Cari kartı yok"
  emptyDescription="İlk müşteri veya tedarikçi kartınızı oluşturun."
  {query}
  onQueryChange={updateQuery}
  onRetry={() => load()}
  onRowOpen={(row) => goto(`/cari/kartlar/${row.id}`)}
  {nextCursor}
  onLoadMore={loadMore}
  {loadingMore}
/>

<style>
  .search-box {
    position: relative;
    width: min(340px, 40vw);
    display: flex;
    align-items: center;
  }
  .search-icon {
    position: absolute;
    left: 8px;
    color: var(--text-muted);
    z-index: 1;
  }
  .search-box :global(.party-search) {
    padding-left: 29px;
    padding-right: 30px;
  }
  .preference-error {
    margin: -7px 2px 7px;
    color: var(--danger);
    font-size: 11px;
    text-align: right;
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
  .keyboard-hint {
    margin: 6px 2px;
    color: var(--text-muted);
    font-size: 10.5px;
  }
  @media (max-width: 640px) {
    .search-box {
      min-width: 230px;
      width: 70vw;
    }
  }
</style>
