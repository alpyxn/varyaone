<script lang="ts">
  import { goto } from '$app/navigation';
  import { onMount } from 'svelte';
  import type { Snippet } from 'svelte';
  import { Download, Plus, RefreshCw, Search, SlidersHorizontal, X } from '@lucide/svelte';
  import { api, type Session } from '$lib/api';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { DocumentToolbar } from '$lib/components/varya/document-toolbar';
  import { DateInput } from '$lib/components/varya/date-input';
  import { formatDate, formatMoney, formatQuantity } from '$lib/design/formatters';
  import { localizedEnum } from '$lib/design/labels';
  import { warehouseType } from '$lib/features/warehouses/types';
  import OperationActionDialog, { type OperationActionKind } from './OperationActionDialog.svelte';
  import {
    VaryaDataGrid,
    ColumnVisibilityMenu,
    createGridQuery,
    gridQueryToSearchParams,
    type VaryaColumn,
    type VaryaGridQuery,
    type ColumnVisibilityState
  } from '$lib/components/varya/data-grid';
  import { densityPreference } from '$lib/design/density.svelte';
  import {
    type EntityOption,
    type EntitySearchHandler
  } from '$lib/components/varya/entity-picker-dialog';
  import { EntityCombobox } from '$lib/components/varya/entity-combobox';

  export type OperationColumn = {
    id: string;
    label: string;
    sortable?: boolean;
    align?: 'left' | 'right' | 'center';
    hideable?: boolean;
    defaultVisible?: boolean;
  };

  export type OperationFilter = {
    field: string;
    label: string;
    kind: 'date' | 'select' | 'entity' | 'text';
    inputMode?: 'text' | 'decimal';
    visibleWhen?: { field: string; value: string };
    options?: { value: string; label: string }[];
    placeholder?: string;
    entity?: {
      title: string;
      description: string;
      triggerPlaceholder: string;
      searchPlaceholder?: string;
      search: EntitySearchHandler<EntityOption>;
    };
  };

  type OperationRow = Record<string, unknown> & { id?: string };
  type OperationList = { items?: OperationRow[]; next_cursor?: string };
  type Props = {
    title: string;
    subtitle?: string;
    requiredPermission: string;
    /** When set, access is granted if the session holds `requiredPermission` OR any of these. */
    anyPermission?: string[];
    endpoint: string;
    columns: OperationColumn[];
    filters?: OperationFilter[];
    searchPlaceholder?: string;
    primaryLabel?: string;
    showPrimary?: boolean;
    preserveListState?: boolean;
    includeInactiveFilter?: boolean;
    openAction?: boolean;
    actionPrefill?: {
      partyID?: string;
      currency?: string;
      documentID?: string;
      method?: string;
      entryKind?: string;
    };
    statusLabels?: Record<string, string>;
    enumLabels?: Record<string, Record<string, string>>;
    detailPath?: (row: OperationRow) => string | undefined;
    primaryAction?: Snippet;
  };

  let {
    title,
    subtitle = '',
    requiredPermission,
    anyPermission,
    endpoint,
    columns,
    filters = [],
    searchPlaceholder = 'Belge, cari veya kod ara',
    primaryLabel = 'Yeni Kayıt',
    showPrimary = true,
    preserveListState = true,
    includeInactiveFilter = false,
    openAction = false,
    actionPrefill,
    statusLabels = {},
    enumLabels = {},
    detailPath,
    primaryAction
  }: Props = $props();
  const actionKind = $derived(actionKindForEndpoint(endpoint));
  const actionPermission = $derived(actionKind ? actionPermissionForKind(actionKind) : undefined);
  let session = $state<Session | null>(null);
  let loading = $state(true);
  let loadingMore = $state(false);
  let denied = $state(false);
  let error = $state('');
  let search = $state('');
  let rows = $state<OperationRow[]>([]);
  let nextCursor = $state<string>();
  let query = $state<VaryaGridQuery>(createGridQuery(50));
  let cursorHistory = $state<string[]>([]);
  let showInactive = $state(false);
  let columnVisibility = $state<ColumnVisibilityState>({});
  let actionOpen = $state(false);
  let entitySelections = $state<Record<string, EntityOption | undefined>>({});
  let activeRequest: AbortController | undefined;
  const SEARCH_DEBOUNCE_MS = 250;
  let searchTimer: ReturnType<typeof setTimeout> | undefined;
  $effect(() => () => clearTimeout(searchTimer));
  const activeFilterCount = $derived(query.filters.length);

  function baseEndpoint() {
    return endpoint.split('?')[0];
  }

  function rowValue(row: OperationRow, key: string) {
    return key.split('.').reduce<unknown>((current, part) => {
      if (!current || typeof current !== 'object') return undefined;
      return (current as Record<string, unknown>)[part];
    }, row);
  }

  function columnRawValue(row: OperationRow, columnID: string) {
    const aliases: Record<string, string[]> = {
      document_no: [
        'document_no',
        'order_no',
        'receipt_no',
        'invoice_no',
        'return_no',
        'transfer_no',
        'business_number'
      ],
      document_date: ['document_date', 'order_date', 'receipt_date', 'invoice_date', 'return_date'],
      currency_code: ['currency_code', 'currency'],
      party_name: ['party_name', 'supplier_name'],
      party_code: ['party_code', 'supplier_code'],
      grand_total: ['grand_total', 'payable_total', 'total', 'amount'],
      transfer_no: ['transfer_no', 'business_number', 'document_no'],
      transfer_type: ['transfer_type', 'type'],
      status: ['status', 'state'],
      from_warehouse_name: [
        'from_warehouse_name',
        'source_warehouse_name',
        'source_warehouse.name'
      ],
      to_warehouse_name: [
        'to_warehouse_name',
        'destination_warehouse_name',
        'destination_warehouse.name'
      ]
    };
    for (const key of aliases[columnID] ?? [columnID]) {
      const value = rowValue(row, key);
      if (value !== undefined && value !== null && value !== '') return value;
    }
    return undefined;
  }

  function actionKindForEndpoint(value: string): OperationActionKind | undefined {
    switch (value.split('?')[0]) {
      case '/party-movements':
        return 'manual';
      case '/finance/collections':
        return 'collection';
      case '/finance/payments':
        return 'payment';
      case '/stock-movements':
        return 'stock-movement';
      case '/warehouses':
        return 'warehouse';
      case '/warehouse-transfers':
        return 'transfer';
      case '/stock-counts':
        return 'count';
      case '/lots':
        // Lot and seri identities are created by an inbound stock movement;
        // the list is a traceability surface, not an independent master CRUD.
        return undefined;
      default:
        return undefined;
    }
  }

  function actionPermissionForKind(kind: OperationActionKind) {
    switch (kind) {
      case 'manual':
        return 'finance.manual.post';
      case 'collection':
        return 'finance.collection.post';
      case 'payment':
        return 'finance.payment.post';
      case 'stock-movement':
        return 'inventory.movement.post';
      case 'warehouse':
        return 'organization.warehouse.manage';
      case 'transfer':
        return 'inventory.transfer.request';
      case 'count':
        return 'inventory.count.post';
    }
  }

  function linkForColumn(columnID: string, row: OperationRow) {
    const id = row.id;
    if (
      ['document_no', 'transfer_no', 'count_no', 'code', 'lot_number', 'serial_number'].includes(
        columnID
      )
    ) {
      return id ? (detailPath?.(row) ?? defaultDetailPath(row)) : undefined;
    }
    const relationID = (key: string) => {
      const value = row[key];
      return value === undefined || value === null || value === '' ? undefined : String(value);
    };
    if (
      ['party_name', 'party_code'].includes(columnID) &&
      ['/party-movements', '/finance/collections', '/finance/payments'].includes(baseEndpoint())
    ) {
      const partyID = relationID('party_id');
      return partyID ? `/cari/kartlar/${encodeURIComponent(partyID)}` : undefined;
    }
    if (
      ['product_name', 'product_code', 'sku'].includes(columnID) &&
      ['/stock-movements', '/lots', '/serial-numbers'].includes(baseEndpoint())
    ) {
      const productID = relationID('product_id');
      return productID ? `/stok/urunler/${encodeURIComponent(productID)}` : undefined;
    }
    if (['warehouse_name', 'from_warehouse_name', 'to_warehouse_name'].includes(columnID)) {
      const warehouseID = relationID(
        columnID === 'from_warehouse_name'
          ? 'source_warehouse_id'
          : columnID === 'to_warehouse_name'
            ? 'destination_warehouse_id'
            : 'warehouse_id'
      );
      return warehouseID ? `/stok/depolar/${encodeURIComponent(warehouseID)}` : undefined;
    }
    return undefined;
  }

  function displayCell(columnID: string, row: OperationRow) {
    const raw =
      columnRawValue(row, columnID) ?? (columnID === 'warehouse_type' ? row.type : undefined);
    if (raw === undefined || raw === null || raw === '') return '—';
    if (columnID === 'is_active' || columnID === 'active')
      return raw === true || String(raw).toLowerCase() === 'true' ? 'Aktif' : 'Pasif';
    const value = String(raw);
    if (columnID === 'transfer_type' && baseEndpoint() === '/warehouse-transfers') {
      const transferTypeLabels: Record<string, string> = {
        QUICK: 'Hızlı Transfer',
        WORKFLOW: 'Sevk / Teslim'
      };
      return transferTypeLabels[value] ?? value;
    }
    if (columnID === 'status' || columnID === 'state' || columnID.endsWith('_status')) {
      if (enumLabels[columnID]?.[value]) return enumLabels[columnID][value];
      if (baseEndpoint() === '/warehouse-transfers') {
        const transferStateLabels: Record<string, string> = {
          DRAFT: 'Taslak',
          REQUESTED: 'Sevk bekliyor',
          APPROVED: 'Sevk bekliyor',
          IN_TRANSIT: 'Sevk sırasında',
          PARTIALLY_RECEIVED: 'Sevk sırasında',
          RECEIVED: 'Başarıyla sevk edildi',
          CANCELLED: 'Sevk iptal oldu'
        };
        return transferStateLabels[value] ?? value;
      }
      if (statusLabels[value]) return statusLabels[value];
      const labels: Record<string, string> = {
        DRAFT: 'Taslak',
        REQUESTED: 'Talep Edildi',
        APPROVED: 'Onaylandı',
        IN_TRANSIT: 'Yolda',
        PARTIALLY_RECEIVED: 'Kısmi Teslim',
        RECEIVED: 'Teslim Alındı',
        COMPLETED: 'Tamamlandı',
        CANCELLED: 'İptal',
        IN_PROGRESS: 'Sayımda',
        COUNTED: 'Sayıldı',
        REVIEW: 'Kontrol',
        POSTED: 'İşlendi',
        REVERSED: 'Ters Kayıt',
        ACTIVE: 'Aktif',
        INACTIVE: 'Pasif',
        IN_STOCK: 'Stokta',
        RESERVED: 'Rezerve',
        SOLD: 'Satıldı / Sevk Edildi',
        RETURNED: 'İade',
        QUARANTINED: 'Karantina',
        SCRAPPED: 'Hurda',
        DISPATCHED: 'Sevk edildi',
        SENT: 'Gönderildi',
        ACCEPTED: 'Kabul edildi',
        REJECTED: 'Reddedildi',
        EXPIRED: 'Süresi doldu',
        CONFIRMED: 'Onaylandı',
        PARTIALLY_FULFILLED: 'Kısmi karşılandı',
        FULFILLED: 'Tamamlandı',
        UNFULFILLED: 'Karşılanmadı',
        OPEN: 'Açık',
        FINALIZED: 'Sonlandırıldı',
        UNINVOICED: 'Faturalanmadı',
        PARTIALLY_INVOICED: 'Kısmi faturalandı',
        INVOICED: 'Faturalandı',
        UNPAID: 'Ödenmedi',
        PARTIALLY_PAID: 'Kısmi ödendi',
        PAID: 'Ödendi'
      };
      return labels[value] ?? value;
    }
    if (
      [
        'document_date',
        'transaction_date',
        'requested_at',
        'arrival_at',
        'shipped_at',
        'received_at',
        'posted_at',
        'fulfillment_at',
        'movement_date',
        'snapshot_at',
        'created_at',
        'updated_at'
      ].includes(columnID)
    ) {
      return formatDate(value, columnID.endsWith('_at'));
    }
    if (
      [
        'debit',
        'credit',
        'balance',
        'amount',
        'unit_cost',
        'total_cost',
        'total_amount',
        'stock_value',
        'tax_total',
        'grand_total',
        'payable_total',
        'total'
      ].includes(columnID)
    ) {
      return formatMoney(value, String(row.currency_code ?? row.currency ?? 'TRY'));
    }
    if (
      [
        'quantity',
        'available_quantity',
        'in_transit_quantity',
        'line_count',
        'physical_quantity',
        'reserved_quantity'
      ].includes(columnID)
    ) {
      return formatQuantity(value);
    }
    return localizedEnum(raw, columnID);
  }

  const gridColumns = $derived<VaryaColumn<OperationRow>[]>(
    columns.map((column) => ({
      id: column.id,
      header: column.label,
      accessor: (row) => displayCell(column.id, row),
      queryField: column.id,
      sortable: column.sortable ?? false,
      hideable: column.hideable ?? true,
      defaultVisible: column.defaultVisible,
      align: column.align,
      width: column.align === 'right' ? 130 : 180,
      link: (row) => linkForColumn(column.id, row)
    }))
  );

  async function load(append = false) {
    activeRequest?.abort();
    const request = new AbortController();
    activeRequest = request;
    if (append) loadingMore = true;
    else loading = true;
    error = '';
    try {
      const params = gridQueryToSearchParams(query);
      if (search.trim()) params.set('q', search.trim());
      const requestPath = requestEndpoint();
      const separator = requestPath.includes('?') ? '&' : '?';
      const result = await api<OperationList>(`${requestPath}${separator}${params}`, {
        signal: request.signal
      });
      const items = (Array.isArray(result.items) ? result.items : []).filter(trackingRowIsUsable);
      rows = append ? [...rows, ...items] : items;
      nextCursor = result.next_cursor;
    } catch (cause) {
      if (cause instanceof DOMException && cause.name === 'AbortError') return;
      error = 'Veriler alınamadı. API bağlantısını kontrol edip yeniden deneyin.';
    } finally {
      if (!request.signal.aborted) {
        loading = false;
        loadingMore = false;
      }
    }
  }

  async function initialize() {
    loading = true;
    try {
      session = await api<Session>('/session');
      const held = session.permissions;
      denied =
        !held.includes(requiredPermission) &&
        !(anyPermission ?? []).some((code) => held.includes(code));
      if (!denied) {
        await load();
        if (
          openAction &&
          actionKind &&
          actionPermission &&
          session.permissions.includes(actionPermission)
        ) {
          actionOpen = true;
        }
      }
    } catch {
      error = 'Oturum bilgisi alınamadı.';
    } finally {
      loading = false;
    }
  }

  function reloadFromFirstPage() {
    query = { ...query, pagination: { mode: 'cursor', pageSize: 50 } };
    cursorHistory = [];
    void load();
  }

  // The search box hits the API, so wait for a pause in typing instead of
  // firing (and aborting) a request per keystroke.
  function applySearch(event: Event) {
    search = (event.currentTarget as HTMLInputElement).value;
    clearTimeout(searchTimer);
    searchTimer = setTimeout(reloadFromFirstPage, SEARCH_DEBOUNCE_MS);
  }

  function clearSearch() {
    if (!search) return;
    search = '';
    clearTimeout(searchTimer);
    reloadFromFirstPage();
  }

  function filterValue(field: string) {
    const filter = query.filters.find((item) => item.field === field);
    if (!filter) return '';
    return Array.isArray(filter.value) ? filter.value.join(',') : filter.value;
  }

  function isFilterVisible(filter: OperationFilter) {
    if (!filter.visibleWhen) return true;
    return filterValue(filter.visibleWhen.field) === filter.visibleWhen.value;
  }

  function setFilter(field: string, value: string) {
    const nextFilters = query.filters.filter((item) => {
      if (item.field === field) return false;
      const definition = filters.find((filter) => filter.field === item.field);
      if (!definition?.visibleWhen) return true;
      const controllingValue =
        definition.visibleWhen.field === field
          ? value.trim()
          : filterValue(definition.visibleWhen.field);
      return controllingValue === definition.visibleWhen.value;
    });
    if (value.trim()) nextFilters.push({ field, operator: 'eq', value: value.trim() });
    query = {
      ...query,
      filters: nextFilters,
      pagination: { mode: 'cursor', pageSize: query.pagination.pageSize }
    };
    cursorHistory = [];
    void load();
  }

  function selectEntity(filter: OperationFilter, option: EntityOption) {
    entitySelections[filter.field] = option;
    setFilter(filter.field, option.id);
  }

  function clearFilter(filter: OperationFilter) {
    entitySelections[filter.field] = undefined;
    setFilter(filter.field, '');
  }

  function loadMore() {
    if (!nextCursor || query.pagination.mode !== 'cursor') return;
    cursorHistory = [...cursorHistory, query.pagination.cursor ?? ''];
    query = { ...query, pagination: { ...query.pagination, cursor: nextCursor } };
    void load(true);
  }

  function loadPrevious() {
    if (!cursorHistory.length || query.pagination.mode !== 'cursor') return;
    const previousCursor = cursorHistory.at(-1) || undefined;
    cursorHistory = cursorHistory.slice(0, -1);
    query = {
      ...query,
      pagination: { mode: 'cursor', pageSize: query.pagination.pageSize, cursor: previousCursor }
    };
    void load();
  }

  function rowKey(row: OperationRow) {
    return String(row.id ?? row.document_no ?? row.code ?? row.transfer_no ?? JSON.stringify(row));
  }

  function trackingRowIsUsable(row: OperationRow) {
    if (!['/lots', '/serial-numbers'].includes(baseEndpoint())) return true;
    const nestedWarehouse = row.warehouse;
    const nested =
      nestedWarehouse && typeof nestedWarehouse === 'object'
        ? (nestedWarehouse as Record<string, unknown>)
        : undefined;
    const type = String(
      row.warehouse_type ?? row.warehouseType ?? nested?.type ?? nested?.warehouse_type ?? ''
    );
    const active = row.warehouse_is_active ?? nested?.is_active;
    return active !== false && (!type || warehouseType({ type }) === 'STANDARD');
  }

  function defaultDetailPath(row: OperationRow) {
    const id = row.id;
    if (!id) return undefined;
    switch (baseEndpoint()) {
      case '/party-movements':
        return `/cari/hareketler/${id}`;
      case '/finance/collections':
        return `/cari/tahsilatlar/${id}`;
      case '/finance/payments':
        return `/cari/odemeler/${id}`;
      case '/finance/accounts':
        return `/finans/hesaplar/${id}`;
      case '/finance/movements':
        return `/finans/hareketler/${id}`;
      case '/finance/transfers':
        return `/finans/transferler/${id}`;
      case '/documents':
        return `/belgeler/${id}`;
      case '/stock-movements':
        return `/stok/hareketler/${id}`;
      case '/warehouses':
        return `/stok/depolar/${id}`;
      case '/warehouse-transfers':
        return `/stok/transferler/${id}`;
      case '/stock-counts':
        return `/stok/sayim/${id}`;
      case '/lots':
        return String(row.tracking_type ?? '').toUpperCase() === 'SERIAL'
          ? `/stok/lot-seri/seri/${id}`
          : `/stok/lot-seri/lot/${id}`;
      case '/serial-numbers':
        return `/stok/lot-seri/seri/${id}`;
      default:
        return undefined;
    }
  }

  function openRow(row: OperationRow) {
    const href = detailPath?.(row) ?? defaultDetailPath(row);
    if (href) {
      if (preserveListState) rememberListState();
      void goto(href);
    }
  }

  function listStateKey() {
    return `varya:list-state:${window.location.pathname}`;
  }

  function rememberListState() {
    try {
      sessionStorage.setItem(listStateKey(), JSON.stringify({ search, query }));
    } catch {
      // A privacy-restricted browser may disable session storage. Navigation
      // remains fully functional; state preservation is best effort.
    }
  }

  function restoreListState() {
    try {
      const raw = sessionStorage.getItem(listStateKey());
      if (!raw) return;
      const saved = JSON.parse(raw) as { search?: unknown; query?: VaryaGridQuery };
      if (typeof saved.search === 'string') search = saved.search;
      if (saved.query && typeof saved.query === 'object') query = saved.query;
    } catch {
      // Ignore malformed/stale state from an older client build.
    }
  }

  function requestEndpoint() {
    if (!includeInactiveFilter) return endpoint;
    const [path, rawQuery = ''] = endpoint.split('?');
    const params = new URLSearchParams(rawQuery);
    if (showInactive) params.set('include_inactive', 'true');
    else params.delete('include_inactive');
    const queryString = params.toString();
    return `${path}${queryString ? `?${queryString}` : ''}`;
  }

  function toggleInactive() {
    if (!includeInactiveFilter || loading || loadingMore) return;
    showInactive = !showInactive;
    query = { ...query, pagination: { mode: 'cursor', pageSize: query.pagination.pageSize } };
    cursorHistory = [];
    void load();
  }

  onMount(() => {
    if (preserveListState) restoreListState();
    else {
      try {
        sessionStorage.removeItem(listStateKey());
      } catch {
        // Ignore storage restrictions; a clean in-memory query is still used.
      }
    }
    void initialize();
    return () => activeRequest?.abort();
  });
</script>

<svelte:head><title>{title} · Varya One</title></svelte:head>

<DocumentToolbar {title} {subtitle}>
  {#snippet primary()}{#if primaryAction}{@render primaryAction()}{:else if actionKind && showPrimary}<Button
        variant="default"
        disabled={loading ||
          denied ||
          !session ||
          !actionPermission ||
          !session.permissions.includes(actionPermission)}
        title={`Gerekli yetki: ${actionPermission}`}
        onclick={() => (actionOpen = true)}><Plus size={14} />{primaryLabel}</Button
      >{/if}{/snippet}
  {#snippet tools()}<div class="search-box">
      <Search size={15} aria-hidden="true" /><Input
        value={search}
        oninput={applySearch}
        onkeydown={(event) => {
          if (event.key === 'Escape') clearSearch();
          else if (event.key === 'Enter') {
            event.preventDefault();
            clearTimeout(searchTimer);
            reloadFromFirstPage();
          }
        }}
        aria-label={`${title} ara`}
        placeholder={searchPlaceholder}
        maxlength={128}
      />{#if search}<button
          class="clear-search"
          type="button"
          aria-label="Aramayı temizle"
          title="Aramayı temizle"
          onclick={clearSearch}><X size={14} /></button
        >{/if}
    </div>
    {#if filters.length}<div class="filter-bar" aria-label="Liste filtreleri">
        <span class="filter-title"><SlidersHorizontal size={14} />Filtreler</span>
        {#each filters as filter}
          {#if isFilterVisible(filter)}
            {@const entity = filter.entity}
            <label class="filter-field">
              <span>{filter.label}</span>
              {#if filter.kind === 'select'}
                <select
                  value={filterValue(filter.field)}
                  aria-label={filter.label}
                  onchange={(event) =>
                    setFilter(filter.field, (event.currentTarget as HTMLSelectElement).value)}
                >
                  <option value="">Tümü</option>
                  {#each filter.options ?? [] as option}
                    <option value={option.value}>{option.label}</option>
                  {/each}
                </select>
              {:else if filter.kind === 'date'}
                <DateInput
                  value={filterValue(filter.field)}
                  ariaLabel={filter.label}
                  onValueChange={(value) => setFilter(filter.field, value)}
                />
              {:else if filter.kind === 'text'}
                <input
                  value={filterValue(filter.field)}
                  aria-label={filter.label}
                  inputmode={filter.inputMode ?? 'text'}
                  placeholder={filter.placeholder}
                  onchange={(event) =>
                    setFilter(filter.field, (event.currentTarget as HTMLInputElement).value)}
                />
              {:else if entity}
                <EntityCombobox
                  selected={entitySelections[filter.field]}
                  onSearch={entity.search}
                  title={entity.title}
                  description={entity.description}
                  triggerLabel={filter.label}
                  triggerPlaceholder={entity.triggerPlaceholder}
                  searchPlaceholder={entity.searchPlaceholder}
                  onSelect={(option) => selectEntity(filter, option)}
                />
              {/if}
            </label>
            {#if filterValue(filter.field)}<Button
                variant="ghost"
                size="icon"
                class="filter-clear"
                title={`${filter.label} filtresini temizle`}
                aria-label={`${filter.label} filtresini temizle`}
                onclick={() => clearFilter(filter)}><X size={14} /></Button
              >{/if}
          {/if}
        {/each}
        {#if activeFilterCount > 1}<Button
            variant="ghost"
            size="sm"
            onclick={() => {
              entitySelections = {};
              query = {
                ...query,
                filters: [],
                pagination: { mode: 'cursor', pageSize: query.pagination.pageSize }
              };
              void load();
            }}>Filtreleri temizle</Button
          >{/if}
      </div>{/if}
    {#if includeInactiveFilter}<button
        class="inactive-filter-toggle"
        type="button"
        aria-pressed={showInactive}
        disabled={loading || loadingMore}
        onclick={toggleInactive}>{showInactive ? 'Pasifleri gizle' : 'Pasifleri göster'}</button
      >{/if}
    <ColumnVisibilityMenu
      columns={gridColumns}
      bind:value={columnVisibility}
      storageKey={`varya:operation-columns:${endpoint}`}
    />
    <Button variant="outline" onclick={() => load()} title="Listeyi yenile"
      ><RefreshCw size={14} />Yenile</Button
    ><Button variant="outline" disabled title="Dışa aktarma ilerleyen fazda açılacak"
      ><Download size={14} />Dışa Aktar</Button
    >{/snippet}
</DocumentToolbar>

{#if actionKind && showPrimary}
  <OperationActionDialog
    bind:open={actionOpen}
    kind={actionKind}
    label={primaryLabel}
    paymentPrefill={actionPrefill}
    onComplete={() => void load()}
  />
{/if}

{#if denied}
  <section class="permission-card" role="alert">
    <strong>Bu ekran için yetkiniz yok.</strong>
    <span>Gerekli yetki: {requiredPermission}</span>
  </section>
{:else if !session && !loading}
  <section class="permission-card" role="alert">
    <strong>Oturum açmanız gerekiyor.</strong>
    <a href="/giris">Giriş ekranına git</a>
  </section>
{:else}
  <VaryaDataGrid
    columns={gridColumns}
    data={rows}
    getRowId={rowKey}
    density={densityPreference.value}
    resizable
    stickyHeader
    virtualized
    {loading}
    {error}
    emptyTitle="Kayıt bulunamadı"
    emptyDescription="Filtreleri değiştirerek tekrar deneyin."
    {query}
    {columnVisibility}
    onColumnVisibilityChange={(next) => (columnVisibility = next)}
    onQueryChange={(next) => {
      // Any sort/filter change returns to the first page: drop the stale cursor.
      query = {
        ...next,
        pagination:
          next.pagination.mode === 'cursor'
            ? { ...next.pagination, cursor: undefined }
            : next.pagination
      };
      cursorHistory = [];
      void load();
    }}
    {nextCursor}
    onRowOpen={openRow}
    onLoadMore={loadMore}
    previousPage={cursorHistory.length > 0}
    onLoadPrevious={loadPrevious}
    pageLabel={`${cursorHistory.length + 1}. sayfa · ${rows.length} kayıt`}
    {loadingMore}
    onRetry={() => load()}
  />
{/if}

<style>
  .search-box {
    position: relative;
    display: flex;
    align-items: center;
    gap: 6px;
    min-width: min(360px, 42vw);
    flex: 1 1 320px;
    color: var(--text-muted);
  }
  .search-box :global(input) {
    min-width: 230px;
    padding-right: 32px;
  }
  .clear-search {
    position: absolute;
    right: 4px;
    display: grid;
    place-items: center;
    width: 26px;
    height: 26px;
    border: 0;
    border-radius: 4px;
    background: transparent;
    color: var(--text-muted);
  }
  .clear-search:hover {
    background: var(--surface-muted);
    color: var(--text);
  }
  .inactive-filter-toggle {
    display: inline-flex;
    min-width: 0;
    min-height: 44px;
    flex: 0 1 auto;
    align-items: center;
    justify-content: center;
    padding: 8px 12px;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    background: var(--surface);
    color: var(--text);
    font: inherit;
    font-size: 12px;
    font-weight: 650;
    line-height: 1.2;
    cursor: pointer;
    touch-action: manipulation;
  }
  .inactive-filter-toggle:hover {
    background: var(--surface-muted);
  }
  .inactive-filter-toggle:focus-visible {
    outline: 2px solid var(--focus);
    outline-offset: 2px;
  }
  .inactive-filter-toggle:disabled {
    cursor: not-allowed;
    opacity: 0.6;
  }
  .filter-bar {
    display: flex;
    flex: 1 0 100%;
    order: 10;
    flex-wrap: wrap;
    align-items: end;
    gap: 7px;
    padding-top: 4px;
    border-top: 1px solid var(--border);
  }
  .filter-title {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    align-self: center;
    color: var(--text-muted);
    font-size: 11px;
    font-weight: 650;
  }
  .filter-field {
    display: grid;
    min-width: 175px;
    gap: 3px;
  }
  .filter-field > span {
    color: var(--text-subtle);
    font-size: 10px;
    font-weight: 650;
  }
  .filter-field select,
  .filter-field :global(input) {
    height: 32px;
    min-width: 160px;
    padding-top: 4px;
    padding-bottom: 4px;
    font-size: 11px;
  }
  :global(.filter-clear) {
    align-self: end;
    margin-bottom: 1px;
  }
  .permission-card {
    display: grid;
    gap: 5px;
    padding: 24px;
    border: 1px solid var(--border);
    border-radius: var(--radius-panel);
    background: var(--surface);
    color: var(--text-muted);
  }
  .permission-card strong {
    color: var(--text);
  }
  .permission-card a {
    width: fit-content;
    color: var(--primary);
  }
  .scope-hint {
    margin: 8px 2px 0;
    color: var(--text-muted);
    font-size: 11px;
  }
  @media (max-width: 640px) {
    .search-box {
      min-width: 230px;
      flex-basis: 100%;
    }
    .inactive-filter-toggle {
      width: 100%;
      flex-basis: 100%;
    }
    .filter-bar {
      width: 100%;
    }
    .filter-field {
      flex: 1 1 140px;
    }
  }
</style>
