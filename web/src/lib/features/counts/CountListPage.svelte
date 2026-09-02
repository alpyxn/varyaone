<script lang="ts">
  import { goto } from '$app/navigation';
  import { onMount } from 'svelte';
  import { Plus, RefreshCw, Search } from '@lucide/svelte';
  import { api, type Session } from '$lib/api';
  import { Button } from '$lib/components/ui/button';
  import { type EntityOption } from '$lib/components/varya/entity-picker-dialog';
  import { EntityCombobox } from '$lib/components/varya/entity-combobox';
  import { DateInput } from '$lib/components/varya/date-input';
  import { listWarehouses } from '$lib/features/warehouses/api';
  import { isActiveStandardWarehouse, type Warehouse } from '$lib/features/warehouses/types';
  import { formatDate, formatQuantity } from '$lib/design/formatters';
  import { createCount, listCounts } from './api';

  type Row = Record<string, unknown>;
  let session = $state<Session | null>(null);
  let rows = $state<Row[]>([]);
  let search = $state('');
  let loading = $state(true);
  let saving = $state(false);
  let error = $state('');
  let showCreate = $state(false);
  let warehouseID = $state('');
  let description = $state('');
  let warehouses = $state<Warehouse[]>([]);
  let warehouseLoading = $state(false);
  let warehouseError = $state('');
  let dateFrom = $state('');
  let dateTo = $state('');
  let filterError = $state('');
  let pageNumber = $state(1);
  const pageSize = 25;

  type WarehouseOption = EntityOption & { warehouse: Warehouse };

  const text = (row: Row, keys: string | string[], fallback = '—') => {
    for (const key of Array.isArray(keys) ? keys : [keys]) {
      const found = key.split('.').reduce<unknown>((current, part) => {
        if (!current || typeof current !== 'object') return undefined;
        return (current as Row)[part];
      }, row);
      if (found !== undefined && found !== null && found !== '') return String(found);
    }
    return fallback;
  };

  const filteredRows = $derived(
    rows.filter((row) => {
      const needle = search.trim().toLocaleLowerCase('tr-TR');
      if (!needle) return true;
      return [
        text(row, ['count_no', 'document_no', 'business_number']),
        text(
          row,
          ['warehouse_name', 'warehouse.name', 'warehouse_code', 'warehouse.code'],
          'Depo belirtilmemiş'
        ),
        text(row, 'description'),
        text(row, ['status', 'state'])
      ]
        .join(' ')
        .toLocaleLowerCase('tr-TR')
        .includes(needle);
    })
  );
  const totalPages = $derived(Math.max(1, Math.ceil(filteredRows.length / pageSize)));
  const visibleRows = $derived(
    filteredRows.slice((pageNumber - 1) * pageSize, pageNumber * pageSize)
  );
  const hasActiveFilters = $derived(Boolean(search.trim() || dateFrom || dateTo));

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

  function statusLabel(value: string) {
    return (
      (
        {
          DRAFT: 'Taslak',
          IN_PROGRESS: 'Sayım sürüyor',
          COUNTED: 'Sayıldı',
          REVIEW: 'Kontrol bekliyor',
          POSTED: 'İşlendi',
          CANCELLED: 'İptal'
        } as Record<string, string>
      )[value.toUpperCase()] ?? value
    );
  }

  function lineCount(row: Row) {
    const lines = row.lines;
    return Array.isArray(lines) ? lines.length : text(row, ['line_count', 'lines_count'], '0');
  }

  function warehouseIDOf(row: Row) {
    return text(row, ['warehouse_id', 'warehouse.id'], '');
  }

  function warehouseNameOf(row: Row) {
    return text(row, ['warehouse_name', 'warehouse.name'], 'Depo belirtilmemiş');
  }

  function warehouseCodeOf(row: Row) {
    return text(row, ['warehouse_code', 'warehouse.code'], '');
  }

  function dateOf(row: Row, keys: string | string[]) {
    return text(row, keys, '');
  }

  function dateLabel(row: Row, keys: string | string[]) {
    return formatDate(dateOf(row, keys), true);
  }

  function lineSummary(row: Row, kind: 'uncounted' | 'difference') {
    if (!Array.isArray(row.lines)) return '0';
    return String(
      row.lines.filter((line) => {
        if (!line || typeof line !== 'object') return false;
        const item = line as Row;
        return kind === 'uncounted'
          ? item.has_response !== true && item.counted_quantity == null
          : item.difference != null && formatQuantity(String(item.difference)) !== '0';
      }).length
    );
  }

  async function load() {
    loading = true;
    error = '';
    try {
      const params = new URLSearchParams({ limit: '100' });
      if (dateFrom) params.set('from', dateFrom);
      if (dateTo) params.set('to', dateTo);
      const result = await listCounts(params.toString());
      rows = Array.isArray(result.items) ? result.items : [];
      pageNumber = 1;
    } catch {
      error = 'Sayım listesi alınamadı. API bağlantısını kontrol edip yeniden deneyin.';
    } finally {
      loading = false;
    }
  }

  function applyFilters(event: SubmitEvent) {
    event.preventDefault();
    filterError = '';
    if (dateFrom && dateTo && dateFrom > dateTo) {
      filterError = 'Tarih aralığında ilk tarih, son tarihten sonra olamaz.';
      return;
    }
    void load();
  }

  function clearFilters() {
    search = '';
    dateFrom = '';
    dateTo = '';
    filterError = '';
    void load();
  }

  async function loadWarehouses() {
    warehouseLoading = true;
    warehouseError = '';
    try {
      const result = await listWarehouses();
      warehouses = (result.items ?? []).filter(isActiveStandardWarehouse);
      if (warehouseID && !warehouses.some((warehouse) => warehouse.id === warehouseID)) {
        warehouseID = '';
      }
    } catch {
      warehouseError = 'Depolar alınamadı. Yeniden deneyin.';
    } finally {
      warehouseLoading = false;
    }
  }

  async function initialize() {
    try {
      session = await api<Session>('/session');
      if (!session.permissions.includes('inventory.count.post')) {
        error = 'Sayım çalışma alanı için yetkiniz bulunmuyor.';
        loading = false;
        return;
      }
      await Promise.all([load(), loadWarehouses()]);
    } catch {
      error = 'Oturum bilgisi alınamadı.';
      loading = false;
    }
  }

  async function saveNewCount(event: SubmitEvent) {
    event.preventDefault();
    if (!warehouseID.trim()) {
      error = 'Yeni sayım için aktif bir standart depo seçin.';
      return;
    }
    saving = true;
    error = '';
    try {
      const created = await createCount(warehouseID.trim(), description.trim());
      const id = text(created, ['id', 'count_id']);
      if (id === '—') throw new Error('Sayım kimliği alınamadı.');
      await goto('/stok/sayim/' + encodeURIComponent(id));
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Yeni sayım oluşturulamadı.';
    } finally {
      saving = false;
    }
  }

  onMount(() => {
    void initialize();
  });
</script>

<svelte:head><title>Stok Sayımları · Varya One</title></svelte:head>

<div class="page-header">
  <div>
    <h1>Stok sayımları</h1>
  </div>
  <div class="page-actions">
    <button class="button secondary" type="button" onclick={() => void load()} disabled={loading}>
      <RefreshCw size={14} /> Yenile
    </button>
    <button
      class="button"
      type="button"
      onclick={() => (showCreate = !showCreate)}
      disabled={!session}
    >
      <Plus size={14} /> Yeni sayım
    </button>
  </div>
</div>

{#if error}<div class="notice error" role="alert">{error}</div>{/if}
{#if showCreate}
  <form class="card create-form" aria-label="Yeni sayım oluştur" onsubmit={saveNewCount}>
    <div>
      <h2>Yeni sayım fişi</h2>
      <p>Stoklu ürün ve varyantlar otomatik olarak açık sayım satırlarına eklenir.</p>
    </div>
    <div class="field">
      <span>Depo</span>
      <EntityCombobox
        selected={selectedWarehouse}
        results={warehouseOptions}
        title="Depo seç"
        description="Sayımı başlatacağınız aktif standart depoyu seçin."
        triggerLabel="Depo"
        triggerPlaceholder="Depo seçin"
        searchPlaceholder="Depo adı veya kodu ara"
        emptyText="Aramanızla eşleşen depo bulunamadı."
        initialEmptyText="Aktif standart depolar listeleniyor."
        loadingText="Depolar yükleniyor…"
        errorText="Depolar alınamadı."
        loading={warehouseLoading}
        disabled={warehouseLoading || Boolean(warehouseError) || warehouseOptions.length === 0}
        onSelect={(option) => (warehouseID = option.id)}
      />
      {#if warehouseLoading}
        <p class="field-help" role="status">Depolar yükleniyor…</p>
      {:else if warehouseError}
        <div class="field-state error" role="alert">
          <span>{warehouseError}</span>
          <button class="link-button" type="button" onclick={() => void loadWarehouses()}
            >Yeniden dene</button
          >
        </div>
      {:else if warehouseOptions.length === 0}
        <p class="field-state error" role="alert">Kullanılabilir aktif standart depo bulunamadı.</p>
      {/if}
    </div>
    <label class="field description-field" for="count-description">
      <span>Açıklama <small>(isteğe bağlı)</small></span>
      <textarea
        id="count-description"
        bind:value={description}
        maxlength="500"
        rows="2"
        placeholder="Örn. Dönem sonu depo sayımı"
      ></textarea>
    </label>
    <button
      class="button"
      type="submit"
      disabled={saving || warehouseLoading || Boolean(warehouseError) || !warehouseID}
      >{saving ? 'Oluşturuluyor…' : 'Sayımı başlat'}</button
    >
  </form>
{/if}

<section class="panel list-panel" aria-labelledby="count-list-heading">
  <div class="list-heading">
    <div>
      <h2 id="count-list-heading">Sayım fişleri</h2>
    </div>
    <label class="search-field">
      <span class="sr-only">Sayım ara</span><Search size={15} aria-hidden="true" />
      <input bind:value={search} placeholder="Sayım no, depo, açıklama veya durum ara" />
    </label>
  </div>
  <form class="count-filters" aria-label="Sayım tarih filtreleri" onsubmit={applyFilters}>
    <div class="date-filter-group">
      <span class="filter-group-label">Tarih aralığı</span>
      <label class="date-filter">
        <span>İlk tarih</span>
        <DateInput bind:value={dateFrom} ariaLabel="Tarih aralığı ilk tarih" />
      </label>
      <label class="date-filter">
        <span>Son tarih</span>
        <DateInput bind:value={dateTo} ariaLabel="Tarih aralığı son tarih" />
      </label>
    </div>
    <div class="filter-actions">
      <Button type="submit" size="sm" disabled={loading}>Filtrele</Button>
      <Button
        type="button"
        size="sm"
        variant="outline"
        onclick={clearFilters}
        disabled={loading || !hasActiveFilters}>Filtreleri temizle</Button
      >
    </div>
  </form>
  {#if filterError}<div class="filter-error" role="alert">{filterError}</div>{/if}
  {#if loading}
    <div class="empty" role="status">Sayım fişleri yükleniyor…</div>
  {:else if filteredRows.length === 0}
    <div class="empty">
      {hasActiveFilters ? 'Seçilen filtrelerde sayım bulunamadı.' : 'Henüz sayım bulunamadı.'}
    </div>
  {:else}
    <div class="table-scroll">
      <table>
        <thead
          ><tr
            ><th scope="col">Sayım no</th><th scope="col">Depo</th><th scope="col">Açıklama</th><th
              scope="col">Başlangıç</th
            ><th scope="col">Bitiş</th><th scope="col">Satır</th><th scope="col">Sayılmayan</th><th
              scope="col">Fark</th
            ><th scope="col">Durum</th></tr
          ></thead
        >
        <tbody>
          {#each visibleRows as row (text(row, ['id', 'count_no']))}
            {@const id = text(row, ['id', 'count_id'])}
            <tr
              class="count-row"
              onclick={() => id !== '—' && goto('/stok/sayim/' + encodeURIComponent(id))}
            >
              <td
                ><a
                  href={'/stok/sayim/' + encodeURIComponent(id)}
                  onclick={(event) => event.stopPropagation()}
                  >{text(row, ['count_no', 'document_no', 'business_number'], 'Sayımsız')}</a
                ></td
              >
              <td>
                {#if warehouseIDOf(row)}
                  <a
                    href={'/stok/depolar/' + encodeURIComponent(warehouseIDOf(row))}
                    onclick={(event) => event.stopPropagation()}
                    >{warehouseNameOf(row)}{warehouseCodeOf(row)
                      ? ` · ${warehouseCodeOf(row)}`
                      : ''}</a
                  >
                {:else}
                  {warehouseNameOf(row)}{warehouseCodeOf(row) ? ` · ${warehouseCodeOf(row)}` : ''}
                {/if}
              </td>
              <td class="description-cell" title={text(row, 'description', '—')}
                >{text(row, 'description', '—')}</td
              >
              <td class="date-cell">
                {dateLabel(row, ['started_at', 'snapshot_at', 'created_at'])}
              </td>
              <td class="date-cell">
                {dateLabel(row, ['finished_at', 'posted_at', 'cancelled_at'])}
              </td>
              <td class="numeric">{lineCount(row)}</td>
              <td class="numeric">{lineSummary(row, 'uncounted')}</td>
              <td class="numeric">{lineSummary(row, 'difference')}</td>
              <td
                ><span class="status-badge">{statusLabel(text(row, ['status', 'state']))}</span></td
              >
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
    <div class="pagination" aria-label="Sayım sayfaları">
      <span>{pageNumber}. sayfa · {filteredRows.length} kayıt</span>
      <div>
        <button
          class="button secondary"
          type="button"
          onclick={() => (pageNumber = Math.max(1, pageNumber - 1))}
          disabled={pageNumber === 1}>Önceki</button
        >
        <button
          class="button secondary"
          type="button"
          onclick={() => (pageNumber = Math.min(totalPages, pageNumber + 1))}
          disabled={pageNumber >= totalPages}>Sonraki</button
        >
      </div>
    </div>
  {/if}
</section>

<style>
  .create-form {
    display: grid;
    grid-template-columns: minmax(220px, 1fr) minmax(260px, 1fr) minmax(240px, 1fr) auto;
    align-items: end;
    gap: 14px;
    margin: 14px 0;
  }
  .create-form h2,
  .list-heading h2 {
    margin: 0;
    font-size: 15px;
  }
  .create-form p,
  .list-heading p {
    margin: 4px 0 0;
    color: var(--text-muted);
    font-size: 12px;
  }
  .field {
    min-width: 0;
  }
  .field > span {
    display: block;
    margin-bottom: 5px;
    color: var(--text-muted);
    font-size: 11px;
    font-weight: 600;
  }
  .field > span small {
    font-weight: 400;
  }
  .description-field textarea {
    box-sizing: border-box;
    width: 100%;
    min-height: 58px;
    resize: vertical;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    padding: 8px 9px;
    background: var(--surface);
    color: var(--text);
    font: inherit;
  }
  .description-field textarea:focus,
  .search-field input:focus {
    border-color: var(--primary);
    outline: 2px solid var(--focus);
    outline-offset: 1px;
  }
  .field-help,
  .field-state {
    align-self: center;
    margin: 0;
    color: var(--text-muted);
    font-size: 11px;
  }
  .field-state.error {
    color: var(--danger, #b42318);
  }
  .field-state {
    display: grid;
    gap: 4px;
  }
  .link-button {
    width: fit-content;
    padding: 0;
    border: 0;
    background: transparent;
    color: var(--primary);
    font: inherit;
    font-weight: 700;
    cursor: pointer;
  }
  .list-panel {
    padding: 16px;
  }
  .list-heading {
    display: flex;
    justify-content: space-between;
    align-items: flex-start;
    gap: 14px;
    margin-bottom: 12px;
  }
  .count-filters {
    display: flex;
    align-items: end;
    flex-wrap: wrap;
    gap: 12px;
    margin-bottom: 14px;
    padding: 12px;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    background: var(--surface-muted);
  }
  .date-filter-group {
    display: grid;
    grid-template-columns: repeat(2, minmax(132px, 1fr));
    gap: 7px;
    min-width: min(100%, 290px);
  }
  .filter-group-label {
    grid-column: 1 / -1;
    color: var(--text-muted);
    font-size: 11px;
    font-weight: 700;
  }
  .date-filter {
    display: grid;
    gap: 4px;
    min-width: 0;
  }
  .date-filter > span {
    color: var(--text-muted);
    font-size: 11px;
  }
  .filter-actions {
    display: flex;
    flex-wrap: wrap;
    gap: 7px;
    margin-left: auto;
  }
  .filter-error {
    margin: -2px 0 12px;
    color: var(--danger, #b42318);
    font-size: 12px;
  }
  .search-field {
    display: flex;
    align-items: center;
    gap: 7px;
    min-width: min(340px, 42vw);
    color: var(--text-muted);
  }
  .search-field input {
    width: 100%;
    min-height: var(--control-height);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    padding: 0 9px;
    background: var(--surface);
    color: var(--text);
  }
  .table-scroll {
    overflow-x: auto;
  }
  .pagination {
    display: flex;
    align-items: end;
    flex-wrap: wrap;
    gap: 8px;
    margin-bottom: 12px;
  }
  .pagination {
    align-items: center;
    justify-content: space-between;
    margin: 12px 0 0;
    color: var(--text-muted);
    font-size: 11px;
  }
  .pagination > div {
    display: flex;
    gap: 6px;
  }
  table {
    width: 100%;
    border-collapse: collapse;
    font-size: 12px;
  }
  th {
    padding: 9px 10px;
    border-bottom: 1px solid var(--border-strong);
    color: var(--text-muted);
    text-align: left;
    font-size: 11px;
    font-weight: 700;
  }
  td {
    padding: 10px;
    border-bottom: 1px solid var(--border);
    white-space: nowrap;
  }
  .count-row {
    cursor: pointer;
  }
  td a {
    color: var(--primary);
    font-weight: 700;
    text-decoration: none;
  }
  .description-cell {
    max-width: 280px;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .date-cell {
    color: var(--text-muted);
    font-variant-numeric: tabular-nums;
  }
  .numeric {
    text-align: right;
  }
  .status-badge {
    display: inline-flex;
    border-radius: 999px;
    padding: 3px 7px;
    background: var(--surface-muted);
    color: var(--text-muted);
    font-size: 11px;
  }
  .empty {
    padding: 40px 10px;
    color: var(--text-muted);
    text-align: center;
    font-size: 13px;
  }
  @media (max-width: 760px) {
    .create-form,
    .list-heading {
      grid-template-columns: 1fr;
      display: grid;
    }
    .search-field {
      min-width: 0;
    }
    .count-filters {
      align-items: stretch;
      flex-direction: column;
    }
    .date-filter-group {
      width: 100%;
    }
    .filter-actions {
      margin-left: 0;
    }
  }
</style>
