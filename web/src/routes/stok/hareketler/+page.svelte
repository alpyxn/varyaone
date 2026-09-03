<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { ChevronDown, ChevronRight, RefreshCw, Search, ExternalLink } from '@lucide/svelte';
  import { api, type Session } from '$lib/api';
  import { matchesSearch } from '$lib/filtering';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { DateInput } from '$lib/components/varya/date-input';
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
  let dateFrom = $state('');
  let dateTo = $state('');
  let loadedOperations = $state<MovementOperation[]>([]);
  const operations = $derived(loadedOperations.filter(matches));
  let pageNumber = $state(1);
  const pageSize = 25;
  const totalPages = $derived(Math.max(1, Math.ceil(operations.length / pageSize)));
  // Narrowing the search can drop the result set below the current page, which
  // would otherwise leave the table blank until the user pages back.
  $effect(() => {
    if (pageNumber > totalPages) pageNumber = totalPages;
  });
  const visibleOperations = $derived(
    operations.slice((pageNumber - 1) * pageSize, pageNumber * pageSize)
  );
  let expanded = $state<Set<string>>(new Set());
  let loadSequence = 0;

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

  function normalizeList(payload: unknown): MovementOperation[] {
    if (!payload || typeof payload !== 'object') return [];
    const source = payload as ListResponse;
    return Array.isArray(source.items) ? source.items : [];
  }

  function operationKey(operation: MovementOperation) {
    return text(operation, ['operation_id', 'id', 'movement_id'], JSON.stringify(operation));
  }

  function movementTimestamp(operation: MovementOperation) {
    const raw = value(operation, ['posted_at', 'movement_date', 'created_at', 'updated_at']);
    if (!raw) return 0;
    const timestamp = Date.parse(String(raw));
    return Number.isNaN(timestamp) ? 0 : timestamp;
  }

  function newestFirst(left: MovementOperation, right: MovementOperation) {
    const byDate = movementTimestamp(right) - movementTimestamp(left);
    return byDate || operationKey(right).localeCompare(operationKey(left));
  }

  function isGroupedOperationDuplicate(movement: MovementOperation) {
    return (
      String(value(movement, 'source_type') ?? '').toUpperCase() === 'STOCK_MOVEMENT_OPERATION'
    );
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

  function matches(operation: MovementOperation) {
    if (!search.trim()) return true;
    const lineText = linesOf(operation)
      .flatMap((line) => [variantLabel(line), text(line, ['variant_code', 'sku'])])
      .join(' ');
    return matchesSearch(
      [
        operationTitle(operation),
        text(operation, ['operation_no', 'movement_no', 'document_no', 'id']),
        text(operation, ['warehouse_name', 'warehouse.name']),
        directionLabel(operation),
        sourceDocumentLabel(operation),
        lineText
      ].join(' '),
      search
    );
  }

  async function load() {
    const requestID = ++loadSequence;
    loading = true;
    error = '';
    try {
      const params = new URLSearchParams({ limit: '100' });
      if (dateFrom) params.set('from', dateFrom);
      if (dateTo) params.set('to', dateTo);
      const [operationResult, movementResult] = await Promise.allSettled([
        api<ListResponse>(`/stock-movement-operations?${params}`),
        api<ListResponse>(`/stock-movements?${params}`)
      ]);
      if (operationResult.status === 'rejected' && movementResult.status === 'rejected') {
        throw operationResult.reason;
      }

      const groupedOperations =
        operationResult.status === 'fulfilled' ? normalizeList(operationResult.value) : [];
      const standaloneMovements =
        movementResult.status === 'fulfilled'
          ? normalizeList(movementResult.value).filter(
              (movement) => !isGroupedOperationDuplicate(movement)
            )
          : [];
      if (requestID === loadSequence) {
        // A posted operation can be returned by both the grouped-operation
        // and raw-movement endpoints. Keep one row per operation so keyed
        // Svelte rows remain stable and the user never sees duplicate work.
        const unique = new Map<string, MovementOperation>();
        for (const operation of [...groupedOperations, ...standaloneMovements]) {
          const key = operationKey(operation);
          if (!unique.has(key)) unique.set(key, operation);
        }
        loadedOperations = [...unique.values()].sort(newestFirst);
        pageNumber = 1;
      }
    } catch (cause) {
      if (requestID === loadSequence) {
        error =
          typeof cause === 'object' && cause && 'message' in cause
            ? String((cause as { message?: unknown }).message)
            : 'Stok hareketleri alınamadı.';
      }
    } finally {
      if (requestID === loadSequence) loading = false;
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
    void api<Session>('/session')
      .then((result) => (session = result))
      .catch(() => (session = null));
    void load();
  });
</script>

<svelte:head><title>Stok Hareketleri · Varya One</title></svelte:head>

<DocumentToolbar title="Stok Hareketleri">
  {#snippet tools()}
    <div class="toolbar-search">
      <Search size={15} aria-hidden="true" />
      <Input bind:value={search} placeholder="Stok, varyant veya depo ara" />
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
    <div class="date-toolbar" aria-label="Stok hareketi tarih filtresi">
      <label><span>Başlangıç</span><DateInput bind:value={dateFrom} ariaLabel="Başlangıç" /></label>
      <label><span>Bitiş</span><DateInput bind:value={dateTo} ariaLabel="Bitiş" /></label>
      <Button variant="outline" size="sm" onclick={() => void load()} disabled={loading}
        >Tarih aralığını uygula</Button
      >
    </div>

    {#if error}
      <div class="inline-error" role="alert">{error}</div>
    {:else if loading}
      <div class="empty" role="status">Stok hareketleri yükleniyor…</div>
    {:else if operations.length === 0}
      <div class="empty">Kayıt bulunamadı.</div>
    {:else}
      <div class="table-scroll">
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
        <span>{pageNumber}. sayfa · {operations.length} işlem</span>
        <div>
          <Button
            variant="outline"
            size="sm"
            onclick={() => (pageNumber = Math.max(1, pageNumber - 1))}
            disabled={pageNumber === 1}>Önceki</Button
          >
          <Button
            variant="outline"
            size="sm"
            onclick={() => (pageNumber = Math.min(totalPages, pageNumber + 1))}
            disabled={pageNumber >= totalPages}>Sonraki</Button
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
  .date-toolbar,
  .pagination {
    display: flex;
    align-items: end;
    flex-wrap: wrap;
    gap: 8px;
    margin-bottom: 12px;
  }
  .date-toolbar label {
    display: grid;
    gap: 4px;
    color: var(--text-muted);
    font-size: 11px;
  }
  .date-toolbar :global(input) {
    min-height: var(--control-height);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    background: var(--surface);
    color: var(--text);
    padding: 0 8px;
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
