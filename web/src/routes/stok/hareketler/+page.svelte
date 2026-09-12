<script lang="ts">
  import ListFilters from '$lib/components/varya/list-filters/ListFilters.svelte';
  import type { ListFilter } from '$lib/components/varya/list-filters/types';
  import type { EntityOption } from '$lib/components/varya/entity-picker-dialog/types';
  import { readListState, writeListState } from '$lib/components/varya/list-filters/state';

  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { ChevronDown, ChevronRight, RefreshCw, Search, ExternalLink } from '@lucide/svelte';
  import { api, type Session } from '$lib/api';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { DocumentToolbar } from '$lib/components/varya/document-toolbar';
  import { formatDate, formatMoney, formatQuantityWithUnit } from '$lib/design/formatters';
  import { addDecimalStrings, canonicalDecimal } from '$lib/design/decimal';

  type Value = string | number | boolean | null | undefined;
  type Line = Record<string, unknown>;
  type MovementOperation = Record<string, unknown> & { lines?: Line[] };
  type ListResponse = { items?: MovementOperation[]; next_cursor?: string };

  let session = $state<Session | null>(null);
  let loading = $state(true);
  let error = $state('');
  let search = $state('');
  let filterValues = $state<Record<string, string>>({});
  let entities = $state<Record<string, EntityOption>>({});
  let loadedOperations = $state<MovementOperation[]>([]);
  const operations = $derived(loadedOperations);
  const visibleOperations = $derived(operations);
  let cursor = $state('');
  let nextCursor = $state('');
  let cursorHistory = $state<string[]>([]);
  let searchTimer: ReturnType<typeof setTimeout>;
  let activeRequest: AbortController | undefined;
  const filters: ListFilter[] = [
    {
      field: 'product_id',
      label: 'Ürün',
      kind: 'entity',
      entity: {
        title: 'Ürün seç',
        description: 'Ürün adı veya koduyla arayın.',
        triggerPlaceholder: 'Tüm ürünler',
        search: async (q, signal) => {
          const r = await api<{ items: { id: string; name: string; code: string }[] }>(
            `/products?${new URLSearchParams({ q, limit: '50', include_inactive: 'true' })}`,
            { signal }
          );
          return r.items.map((p) => ({ id: p.id, title: p.name, subtitle: p.code }));
        }
      }
    },
    {
      field: 'warehouse_id',
      label: 'Depo',
      kind: 'entity',
      entity: {
        title: 'Depo seç',
        description: 'Depo adı veya koduyla arayın.',
        triggerPlaceholder: 'Tüm depolar',
        search: async (q, signal) => {
          const r = await api<{ items: { id: string; name: string; code: string }[] }>(
            `/warehouses?${new URLSearchParams({ q, limit: '50', include_inactive: 'true' })}`,
            { signal }
          );
          return r.items.map((p) => ({ id: p.id, title: p.name, subtitle: p.code }));
        }
      }
    },
    {
      field: 'movement_type',
      label: 'Hareket türü',
      kind: 'select',
      options: [
        { value: 'PURCHASE_RECEIPT', label: 'Alış / mal kabul' },
        { value: 'SALES_DISPATCH', label: 'Satış / sevk' },
        { value: 'SALES_RETURN', label: 'Satış iadesi' },
        { value: 'PURCHASE_RETURN', label: 'Alış iadesi' },
        { value: 'TRANSFER_IN', label: 'Transfer giriş' },
        { value: 'TRANSFER_OUT', label: 'Transfer çıkış' },
        { value: 'COUNT_ADJUSTMENT', label: 'Sayım' },
        { value: 'MANUAL_ADJUSTMENT', label: 'Manuel düzeltme' },
        { value: 'DAMAGE', label: 'Hasar' },
        { value: 'WASTE', label: 'Fire' },
        { value: 'RECONCILIATION', label: 'Mutabakat' }
      ]
    },
    { field: 'from', label: 'Hareket tarihi başlangıç', kind: 'date' },
    { field: 'to', label: 'Hareket tarihi bitiş', kind: 'date' },
    {
      field: 'direction',
      label: 'Yön',
      kind: 'select',
      options: [
        { value: 'IN', label: 'Giriş' },
        { value: 'OUT', label: 'Çıkış' }
      ]
    }
  ];
  function resetList() {
    clearTimeout(searchTimer);
    cursor = '';
    cursorHistory = [];
    void load();
  }
  function setFilter(field: string, value: string) {
    filterValues = { ...filterValues, [field]: value };
    resetList();
  }
  function remember() {
    if (session)
      writeListState(session, 'stock-movement-feed', {
        search,
        filterValues,
        entities,
        cursor,
        cursorHistory
      });
  }
  let expanded = $state<Set<string>>(new Set());

  function value(item: Record<string, unknown>, keys: string | string[]): Value {
    for (const key of Array.isArray(keys) ? keys : [keys]) {
      const found = key.split('.').reduce<unknown>((current, part) => {
        if (!current || typeof current !== 'object') return undefined;
        return (current as Record<string, unknown>)[part];
      }, item);
      if (found !== undefined && found !== null && found !== '') return found as Value;
    }
    return undefined;
  }

  function text(item: Record<string, unknown>, keys: string | string[], fallback = '—') {
    const found = value(item, keys);
    return found === undefined || found === null || found === '' ? fallback : String(found);
  }

  function linesOf(operation: MovementOperation) {
    return Array.isArray(operation.lines)
      ? operation.lines.filter((line): line is Line => Boolean(line && typeof line === 'object'))
      : [];
  }

  function operationKey(operation: MovementOperation) {
    return text(operation, ['operation_id', 'id', 'movement_id'], JSON.stringify(operation));
  }

  function lineAttributes(line: Line) {
    const source = value(line, ['variant_display', 'attributes']);
    if (!source || typeof source !== 'object' || Array.isArray(source)) return [];
    return Object.entries(source as Record<string, unknown>).filter(
      ([, item]) => item !== undefined && item !== null && item !== ''
    );
  }

  function attributeText(item: unknown): string {
    if (Array.isArray(item)) return item.map(attributeText).join(', ');
    if (item && typeof item === 'object') {
      const object = item as Record<string, unknown>;
      return String(object.name ?? object.label ?? object.value ?? object.code ?? '');
    }
    return String(item);
  }

  function variantLabel(line: Line) {
    const attrs = lineAttributes(line)
      .map(([key, item]) => `${key}: ${attributeText(item)}`)
      .join(' · ');
    return attrs || text(line, ['variant_name', 'variant_code', 'sku', 'variant_id'], 'Ana stok');
  }

  function directionLabel(operation: MovementOperation) {
    const direction = text(operation, ['direction', 'movement_direction']);
    return direction === 'OUT' ? 'Çıkış' : direction === 'IN' ? 'Giriş' : direction;
  }

  function operationTitle(operation: MovementOperation) {
    return text(operation, ['product_name', 'product.name', 'product_code', 'product_id']);
  }

  function totalQuantity(operation: MovementOperation) {
    const rows = linesOf(operation);
    if (rows.length) {
      return rows.reduce(
        (total, line) =>
          addDecimalStrings(total, String(value(line, ['base_quantity', 'quantity']) ?? '0')),
        '0'
      );
    }
    return String(value(operation, ['base_quantity', 'quantity']) ?? '0');
  }

  function operationUnit(operation: MovementOperation) {
    return text(operation, ['stock_unit'], text(operation, ['unit_code', 'unit'], 'ADET'));
  }

  function sourceDocumentLabel(operation: MovementOperation) {
    const documentNo = text(operation, ['source_document_no', 'source.document_no'], '');
    if (!documentNo) return '—';
    const type = text(operation, ['source_document_type', 'source.document_type'], '');
    const typeLabels: Record<string, string> = {
      SALES_QUOTE: 'Satış teklifi',
      SALES_ORDER: 'Satış siparişi',
      SALES_DISPATCH: 'Satış irsaliyesi',
      SALES_DELIVERY: 'Satış irsaliyesi',
      SALES_INVOICE: 'Satış faturası',
      SALES_RETURN: 'Satış iadesi',
      SALES_RETURN_INVOICE: 'Satış iade faturası',
      PURCHASE_ORDER: 'Alış siparişi',
      GOODS_RECEIPT: 'Mal kabul',
      PURCHASE_DELIVERY: 'Alış irsaliyesi',
      PURCHASE_INVOICE: 'Alış faturası',
      PURCHASE_RETURN: 'Alış iadesi',
      PURCHASE_RETURN_INVOICE: 'Alış iade faturası'
    };
    return typeLabels[type.toUpperCase()]
      ? `${typeLabels[type.toUpperCase()]} · ${documentNo}`
      : documentNo;
  }

  function multiplyDecimalStrings(left: unknown, right: unknown) {
    const parse = (value: unknown) => {
      const normalized = canonicalDecimal(String(value ?? ''));
      const match = /^(\d+)(?:\.(\d+))?$/.exec(normalized);
      if (!match) return undefined;
      return { digits: BigInt(`${match[1]}${match[2] ?? ''}`), scale: (match[2] ?? '').length };
    };
    const a = parse(left);
    const b = parse(right);
    if (!a || !b) return '0';
    const scale = a.scale + b.scale;
    const digits = a.digits * b.digits;
    if (digits === 0n) return '0';
    const text = digits.toString().padStart(scale + 1, '0');
    if (scale === 0) return text;
    const integer = text.slice(0, -scale) || '0';
    const fraction = text.slice(-scale).replace(/0+$/, '');
    return fraction ? `${integer}.${fraction}` : integer;
  }

  function totalCost(operation: MovementOperation) {
    const rows = linesOf(operation);
    if (!rows.length) return String(value(operation, ['total_cost', 'total_amount']) ?? '0');
    return rows.reduce((total, line) => {
      const explicit = value(line, ['total_cost', 'total_amount']);
      if (explicit !== undefined && explicit !== null && explicit !== '') {
        return addDecimalStrings(total, canonicalDecimal(String(explicit)) || '0');
      }
      return addDecimalStrings(
        total,
        multiplyDecimalStrings(
          value(line, ['base_quantity', 'quantity']) ?? '0',
          value(line, ['unit_cost']) ?? '0'
        )
      );
    }, '0');
  }

  async function load() {
    activeRequest?.abort();
    const request = new AbortController();
    activeRequest = request;
    loading = true;
    error = '';
    try {
      const params = new URLSearchParams({ q: search, limit: '50', cursor });
      for (const [k, v] of Object.entries(filterValues)) if (v) params.set(k, v);
      const result = await api<ListResponse>(`/stock-movement-feed?${params}`, {
        signal: request.signal
      });
      if (request.signal.aborted) return;
      loadedOperations = result.items ?? [];
      nextCursor = result.next_cursor ?? '';
      expanded = new Set();
      remember();
    } catch (cause) {
      if (!request.signal.aborted)
        error = cause instanceof Error ? cause.message : 'Stok hareketleri alınamadı.';
    } finally {
      if (activeRequest === request) loading = false;
    }
  }

  function toggle(operation: MovementOperation) {
    const key = operationKey(operation);
    const next = new Set(expanded);
    if (next.has(key)) next.delete(key);
    else next.add(key);
    expanded = next;
  }

  function detailHref(operation: MovementOperation) {
    const id = value(operation, ['operation_id', 'id', 'movement_id']);
    return id ? `/stok/hareketler/${encodeURIComponent(String(id))}` : undefined;
  }

  function openDetail(operation: MovementOperation) {
    const href = detailHref(operation);
    if (href) void goto(href);
  }

  onMount(() => {
    let alive = true;
    void (async () => {
      try {
        session = await api<Session>('/session');
        if (!alive) return;
        const saved = readListState<{
          search: string;
          filterValues: Record<string, string>;
          entities: Record<string, EntityOption>;
          cursor: string;
          cursorHistory: string[];
        }>(session, 'stock-movement-feed');
        if (saved) {
          search = saved.search ?? '';
          filterValues = saved.filterValues ?? {};
          entities = saved.entities ?? {};
          cursor = saved.cursor ?? '';
          cursorHistory = saved.cursorHistory ?? [];
        }
        await load();
      } catch {
        if (alive) {
          error = 'Oturum bilgisi alınamadı.';
          loading = false;
        }
      }
    })();
    return () => {
      alive = false;
      clearTimeout(searchTimer);
      activeRequest?.abort();
    };
  });
</script>

<svelte:head><title>Stok Hareketleri · Varya One</title></svelte:head>

<DocumentToolbar title="Stok Hareketleri">
  {#snippet tools()}
    <div class="toolbar-search">
      <Search size={15} aria-hidden="true" />
      <Input
        bind:value={search}
        aria-label="Stok hareketi ara"
        placeholder="Stok, varyant, belge veya depo ara"
        oninput={() => {
          clearTimeout(searchTimer);
          searchTimer = setTimeout(resetList, 250);
        }}
      />
    </div>
    <Button variant="outline" onclick={() => void load()} disabled={loading}>
      <RefreshCw size={14} />Yenile
    </Button>
  {/snippet}
</DocumentToolbar>

{#if !session && !loading}
  <section class="panel notice" role="alert">Oturum açmanız gerekiyor.</section>
{:else}
  <section class="panel movement-list">
    <div class="list-heading">
      <span>{operations.length} işlem</span>
    </div>
    <ListFilters
      {filters}
      values={filterValues}
      {entities}
      onChange={setFilter}
      onEntity={(filter, option) => {
        entities = { ...entities, [filter.field]: option };
        setFilter(filter.field, option.id);
      }}
      onClear={() => {
        filterValues = {};
        entities = {};
        resetList();
      }}
    />
    {#if search}<Button
        variant="ghost"
        size="sm"
        onclick={() => {
          search = '';
          resetList();
        }}>Aramayı temizle</Button
      >{/if}

    {#if error}
      <div class="inline-error" role="alert">{error}</div>
    {:else if loading}
      <div class="empty" role="status">Stok hareketleri yükleniyor…</div>
    {:else if operations.length === 0}
      <div class="empty">Kayıt bulunamadı.</div>
    {:else}
      <!-- svelte-ignore a11y_no_noninteractive_tabindex (a scrollable region must be reachable by keyboard) -->
      <div
        class="table-scroll"
        tabindex="0"
        role="region"
        aria-label="Tablo — yatay kaydırılabilir"
      >
        <table>
          <thead>
            <tr>
              <th aria-label="Aç"></th>
              <th>Stok kartı</th>
              <th>Kaynak belge</th>
              <th>Depo</th>
              <th>Tür</th>
              <th class="numeric">Toplam miktar</th>
              <th class="numeric">Toplam maliyet</th>
              <th>Tarih / saat</th>
              <th aria-label="Detay"></th>
            </tr>
          </thead>
          <tbody>
            {#each visibleOperations as operation (operationKey(operation))}
              {@const key = operationKey(operation)}
              {@const operationLines = linesOf(operation)}
              {@const hasLines = operationLines.length > 0}
              {@const href = detailHref(operation)}
              <tr
                class="operation-row"
                class:expanded={expanded.has(key)}
                class:hasVariantLines={hasLines}
                onclick={() => hasLines && toggle(operation)}
                ondblclick={() => openDetail(operation)}
              >
                <td class="expand-cell">
                  {#if hasLines}<button
                      type="button"
                      class="expand-button"
                      aria-label={expanded.has(key) ? 'Satırları kapat' : 'Satırları aç'}
                      onclick={(event) => {
                        event.stopPropagation();
                        toggle(operation);
                      }}
                    >
                      {#if expanded.has(key)}<ChevronDown size={16} />{:else}<ChevronRight
                          size={16}
                        />{/if}
                    </button>{/if}
                </td>
                <td>
                  {#if href}<a
                      class="primary-link"
                      {href}
                      onclick={(event) => event.stopPropagation()}>{operationTitle(operation)}</a
                    >{:else}{operationTitle(operation)}{/if}
                  {#if hasLines}<small>{operationLines.length} varyant satırı</small>{/if}
                </td>
                <td>{sourceDocumentLabel(operation)}</td>
                <td>{text(operation, ['warehouse_name', 'warehouse.name', 'warehouse_id'])}</td>
                <td
                  ><span class:out={directionLabel(operation) === 'Çıkış'} class="direction-badge"
                    >{directionLabel(operation)}</span
                  ></td
                >
                <td class="numeric"
                  >{formatQuantityWithUnit(totalQuantity(operation), operationUnit(operation))}</td
                >
                <td class="numeric"
                  >{formatMoney(
                    String(totalCost(operation) ?? 0),
                    text(operation, 'currency', 'TRY')
                  )}</td
                >
                <td
                  >{formatDate(
                    text(operation, ['posted_at', 'movement_date', 'created_at'], ''),
                    true
                  )}</td
                >
                <td>
                  {#if href}<a
                      class="detail-link"
                      {href}
                      onclick={(event) => event.stopPropagation()}
                      >Detay <ExternalLink size={13} /></a
                    >{/if}
                </td>
              </tr>
              {#if hasLines && expanded.has(key)}
                <tr class="line-heading"><td colspan="9">Varyant satırları</td></tr>
                {#each operationLines as line, index (String(value( line, ['id', 'line_id', 'variant_id'] ) ?? index))}
                  <tr class="variant-row">
                    <td></td>
                    <td>
                      <strong>{variantLabel(line)}</strong>
                      {#if value(line, ['variant_code', 'sku'])}<small
                          >{text(line, ['variant_code', 'sku'])}</small
                        >{/if}
                      {#if lineAttributes(line).length}<div class="attribute-list">
                          {#each lineAttributes(line) as [name, item]}<span class="attribute-badge"
                              >{name}: {attributeText(item)}</span
                            >{/each}
                        </div>{/if}
                    </td>
                    <td colspan="3">{operationUnit(operation)}</td>
                    <td class="numeric"
                      >{formatQuantityWithUnit(
                        String(value(line, ['base_quantity', 'quantity']) ?? 0),
                        operationUnit(operation)
                      )}</td
                    >
                    <td class="numeric"
                      >{formatMoney(
                        String(value(line, ['total_cost', 'total_amount', 'unit_cost']) ?? 0),
                        text(line, 'currency', text(operation, 'currency', 'TRY'))
                      )}</td
                    >
                    <td colspan="2"></td>
                  </tr>
                {/each}
              {/if}
            {/each}
          </tbody>
        </table>
      </div>
      <div class="pagination" aria-label="Stok hareketi sayfaları">
        <span>{cursorHistory.length + 1}. sayfa · {operations.length} işlem</span>
        <div>
          <Button
            variant="outline"
            size="sm"
            onclick={() => {
              cursor = cursorHistory.at(-1) ?? '';
              cursorHistory = cursorHistory.slice(0, -1);
              void load();
            }}
            disabled={loading || !cursorHistory.length}>Önceki</Button
          >
          <Button
            variant="outline"
            size="sm"
            onclick={() => {
              cursorHistory = [...cursorHistory, cursor];
              cursor = nextCursor;
              void load();
            }}
            disabled={loading || !nextCursor}>Sonraki</Button
          >
        </div>
      </div>
    {/if}
  </section>
{/if}

<style>
  .toolbar-search {
    display: flex;
    align-items: center;
    gap: 6px;
    min-width: min(360px, 42vw);
    color: var(--text-muted);
  }
  .toolbar-search :global(input) {
    min-width: 230px;
  }
  .movement-list {
    padding: 16px;
  }
  .list-heading {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 12px;
    margin-bottom: 12px;
  }
  .list-heading h2 {
    margin: 0;
    font-size: 15px;
  }
  .list-heading p {
    margin: 4px 0 0;
    color: var(--text-muted);
    font-size: 12px;
  }
  .list-heading > span {
    color: var(--text-muted);
    font-size: 11px;
    white-space: nowrap;
  }
  .table-scroll {
    overflow-x: auto;
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
  th,
  td {
    padding: 9px 10px;
    border-bottom: 1px solid var(--border);
    text-align: left;
    white-space: nowrap;
    vertical-align: middle;
  }
  th {
    color: var(--text-muted);
    font-size: 11px;
    font-weight: 700;
  }
  .numeric {
    text-align: right;
  }
  .operation-row {
    cursor: default;
  }
  .operation-row.hasVariantLines {
    cursor: pointer;
  }
  .operation-row.hasVariantLines.expanded {
    background: var(--surface-muted);
  }
  .expand-cell {
    width: 32px;
    padding-right: 0;
  }
  .expand-button {
    display: grid;
    place-items: center;
    border: 0;
    background: transparent;
    color: var(--text-muted);
    cursor: pointer;
  }
  .primary-link,
  .detail-link {
    color: var(--primary);
    text-decoration: none;
  }
  .primary-link:hover,
  .detail-link:hover {
    text-decoration: underline;
  }
  td small {
    display: block;
    margin-top: 3px;
    color: var(--text-muted);
    font-size: 10px;
  }
  .detail-link {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    font-size: 11px;
  }
  .direction-badge,
  .attribute-badge {
    display: inline-flex;
    align-items: center;
    border-radius: 999px;
  }
  .direction-badge {
    padding: 3px 8px;
    background: color-mix(in srgb, var(--success, #16845b) 12%, var(--surface));
    color: var(--success, #16845b);
    font-size: 11px;
    font-weight: 700;
  }
  .direction-badge.out {
    background: color-mix(in srgb, var(--danger, #c43d3d) 12%, var(--surface));
    color: var(--danger, #c43d3d);
  }
  .line-heading td {
    padding: 7px 10px 5px 52px;
    background: var(--surface-muted);
    color: var(--text-muted);
    font-size: 10px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.04em;
  }
  .variant-row {
    background: color-mix(in srgb, var(--surface-muted) 55%, var(--surface));
  }
  .variant-row td:first-child {
    border-left: 3px solid var(--primary);
  }
  .attribute-list {
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
    margin-top: 5px;
  }
  .attribute-badge {
    padding: 2px 6px;
    border: 1px solid var(--border);
    background: var(--surface);
    color: var(--text-muted);
    font-size: 10px;
  }
  .empty,
  .notice {
    padding: 24px;
    color: var(--text-muted);
    text-align: center;
  }
  .inline-error {
    margin-bottom: 12px;
    padding: 9px 10px;
    border: 1px solid color-mix(in srgb, var(--danger) 30%, var(--border));
    border-radius: var(--radius-control);
    color: var(--danger);
    font-size: 12px;
  }
  @media (max-width: 760px) {
    .toolbar-search {
      min-width: 0;
      flex: 1 1 220px;
    }
    .toolbar-search :global(input) {
      min-width: 0;
    }
  }
</style>
