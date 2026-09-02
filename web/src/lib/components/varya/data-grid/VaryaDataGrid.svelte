<script lang="ts" generics="T extends object">
  import {
    ArrowDown,
    ArrowUp,
    ArrowUpDown,
    ChevronRight,
    LoaderCircle,
    RotateCcw
  } from '@lucide/svelte';
  import {
    createTable,
    tableFeatures,
    rowSortingFeature,
    rowSelectionFeature,
    columnSizingFeature,
    columnResizingFeature,
    columnVisibilityFeature,
    columnOrderingFeature,
    columnPinningFeature,
    type ColumnDef,
    type SortingState,
    type RowSelectionState
  } from '@tanstack/svelte-table';
  import { createVirtualizer } from '@tanstack/svelte-virtual';
  import { Button } from '$lib/components/ui/button';
  import { normalizeColumnVisibility, type VaryaDataGridProps } from './types';

  let {
    columns,
    data,
    getRowId,
    density = 'compact',
    selectable = false,
    resizable = true,
    stickyHeader = true,
    virtualized = true,
    loading = false,
    error,
    emptyTitle = 'Kayıt bulunamadı',
    emptyDescription = 'Arama veya filtre ölçütlerini değiştirin.',
    columnVisibility = {},
    onColumnVisibilityChange,
    query,
    onQueryChange,
    onRowOpen,
    nextCursor,
    onLoadMore,
    previousPage = false,
    onLoadPrevious,
    pageLabel,
    loadingMore = false,
    onRetry
  }: VaryaDataGridProps<T> = $props();
  let scrollElement = $state<HTMLDivElement>();
  let activeRow = $state(0);
  let activeColumnId = $state('');
  let selected = $state<RowSelectionState>({});
  const currentColumnVisibility = $derived(
    normalizeColumnVisibility(columns, columnVisibility ?? {})
  );
  const features = tableFeatures({
    rowSortingFeature,
    rowSelectionFeature,
    columnSizingFeature,
    columnResizingFeature,
    columnVisibilityFeature,
    columnOrderingFeature,
    columnPinningFeature
  });
  const tableColumns = $derived(
    columns.map((column): ColumnDef<typeof features, T> => ({
      id: column.id,
      header: column.header,
      accessorFn: column.accessor,
      size: column.width ?? 160,
      minSize: column.minWidth ?? 70,
      maxSize: column.maxWidth ?? 700,
      enableSorting: column.sortable ?? false,
      enableHiding: column.hideable ?? true,
      enableResizing: resizable
    }))
  );
  const sorting = $derived<SortingState>(
    (query?.sorting ?? []).map((item) => ({
      id:
        columns.find((column) => (column.queryField ?? column.id) === item.field)?.id ?? item.field,
      desc: item.direction === 'desc'
    }))
  );
  const table = createTable({
    features,
    get data() {
      return data;
    },
    get columns() {
      return tableColumns;
    },
    get getRowId() {
      return getRowId;
    },
    manualSorting: true,
    get enableRowSelection() {
      return selectable;
    },
    state: {
      get sorting() {
        return sorting;
      },
      get rowSelection() {
        return selected;
      },
      get columnVisibility() {
        return currentColumnVisibility;
      }
    },
    onRowSelectionChange: (updater) => {
      selected = typeof updater === 'function' ? updater(selected) : updater;
    },
    onColumnVisibilityChange: (updater) => {
      updateColumnVisibility(updater);
    },
    onSortingChange: updateSorting,
    columnResizeMode: 'onChange'
  });
  const rows = $derived(table.getRowModel().rows);
  const rowSize = () => 40;
  const virtualizer = createVirtualizer({
    count: 0,
    getScrollElement: () => scrollElement ?? null,
    estimateSize: rowSize,
    overscan: 8
  });
  let virtualizerCount = -1;
  let virtualizerElement: HTMLDivElement | null | undefined;
  let virtualizerDensity: typeof density | undefined;
  $effect(() => {
    const count = virtualized ? rows.length : 0;
    const element = scrollElement ?? null;
    if (
      virtualizerCount === count &&
      virtualizerElement === element &&
      virtualizerDensity === density
    )
      return;
    virtualizerCount = count;
    virtualizerElement = element;
    virtualizerDensity = density;
    $virtualizer.setOptions({
      count,
      getScrollElement: () => scrollElement ?? null,
      estimateSize: rowSize
    });
  });
  $effect(() => {
    if (activeRow >= rows.length) activeRow = Math.max(0, rows.length - 1);
    if (!data.length) selected = {};
  });
  $effect(() => {
    const visible = table.getVisibleLeafColumns();
    if (!activeColumnId || !visible.some((column) => column.id === activeColumnId)) {
      activeColumnId = visible[0]?.id ?? '';
    }
  });
  const virtualRows = $derived(
    virtualized
      ? $virtualizer.getVirtualItems().filter((item) => item.index < rows.length)
      : rows.map((_, index) => ({
          index,
          start: index * rowSize(),
          size: rowSize(),
          key: index,
          end: 0,
          lane: 0
        }))
  );
  const totalSize = $derived(
    virtualized ? $virtualizer.getTotalSize() : virtualRows.length * rowSize()
  );
  const visibleColumnCount = $derived(table.getVisibleLeafColumns().length);
  function updateSorting(updater: SortingState | ((old: SortingState) => SortingState)) {
    if (!query || !onQueryChange) return;
    const next = typeof updater === 'function' ? updater(sorting) : updater;
    onQueryChange({
      ...query,
      sorting: next.map((sort) => {
        const column = columns.find((item) => item.id === sort.id);
        return { field: column?.queryField ?? sort.id, direction: sort.desc ? 'desc' : 'asc' };
      })
    });
  }

  function updateColumnVisibility(
    updater: Record<string, boolean> | ((old: Record<string, boolean>) => Record<string, boolean>)
  ) {
    const next = typeof updater === 'function' ? updater(currentColumnVisibility) : updater;
    onColumnVisibilityChange?.(normalizeColumnVisibility(columns, next));
  }

  function revealActiveCell(attempt = 0) {
    if (typeof window === 'undefined') return;
    requestAnimationFrame(() => {
      if (!scrollElement) return;
      const cell = Array.from(scrollElement.querySelectorAll<HTMLElement>('[data-grid-cell]')).find(
        (element) =>
          element.dataset.rowIndex === String(activeRow) &&
          element.dataset.gridColumn === activeColumnId
      );

      if (!cell) {
        if (attempt < 1) {
          $virtualizer.scrollToIndex(activeRow, { align: 'auto' });
          revealActiveCell(attempt + 1);
        }
        return;
      }

      cell.scrollIntoView({ block: 'nearest', inline: 'nearest' });
    });
  }

  function activateRow(index: number, event: MouseEvent) {
    activeRow = index;
    const cell =
      event.target instanceof Element
        ? event.target.closest<HTMLElement>('[data-grid-column]')
        : undefined;
    if (cell?.dataset.gridColumn) activeColumnId = cell.dataset.gridColumn;
    revealActiveCell();
  }

  function keydown(event: KeyboardEvent) {
    if (isInteractiveTarget(event.target)) return;
    if (!rows.length) return;
    if (event.key === 'ArrowDown') {
      event.preventDefault();
      activeRow = Math.min(rows.length - 1, activeRow + 1);
      revealActiveCell();
    } else if (event.key === 'ArrowUp') {
      event.preventDefault();
      activeRow = Math.max(0, activeRow - 1);
      revealActiveCell();
    } else if (event.key === 'ArrowLeft' || event.key === 'ArrowRight') {
      const visible = table.getVisibleLeafColumns();
      if (!visible.length) return;
      event.preventDefault();
      const current = Math.max(
        0,
        visible.findIndex((column) => column.id === activeColumnId)
      );
      const next =
        event.key === 'ArrowLeft'
          ? Math.max(0, current - 1)
          : Math.min(visible.length - 1, current + 1);
      activeColumnId = visible[next]?.id ?? activeColumnId;
      revealActiveCell();
    } else if (event.key === 'Enter' && onRowOpen) {
      event.preventDefault();
      onRowOpen(rows[activeRow].original);
    }
  }

  function isInteractiveTarget(target: EventTarget | null) {
    return (
      target instanceof Element &&
      Boolean(target.closest('a,button,input,select,textarea,[role="button"],[data-grid-no-open]'))
    );
  }

  function openRow(event: MouseEvent, original: T) {
    if (!onRowOpen || isInteractiveTarget(event.target)) return;
    onRowOpen(original);
  }
</script>

<div class="varya-grid" data-density={density}>
  <div
    class="grid-scroll"
    bind:this={scrollElement}
    tabindex="0"
    role="grid"
    aria-rowcount={rows.length}
    aria-colcount={visibleColumnCount}
    aria-busy={loading}
    aria-label="Kayıt tablosu"
    onkeydown={keydown}
  >
    <table style:width={`${table.getTotalSize() + (selectable ? 38 : 0)}px`}>
      <caption class="sr-only">Kayıt tablosu</caption>
      <thead class:sticky={stickyHeader}
        >{#each table.getHeaderGroups() as headerGroup}<tr>
            {#if selectable}<th class="select-cell"
                ><input
                  type="checkbox"
                  aria-label="Görünen kayıtların tümünü seç"
                  checked={table.getIsAllRowsSelected()}
                  onchange={table.getToggleAllRowsSelectedHandler()}
                /></th
              >{/if}
            {#each headerGroup.headers as header, headerIndex}{@const sortState =
                header.column.getIsSorted()}<th
                class:mobile-secondary={headerIndex >= 3}
                style:width={`${header.getSize()}px`}
                class:align-right={columns.find((column) => column.id === header.id)?.align ===
                  'right'}
              >
                {#if header.column.getCanSort()}<button
                    class="sort-button"
                    onclick={header.column.getToggleSortingHandler()}
                    >{columns.find((column) => column.id === header.id)
                      ?.header}{#if sortState === 'asc'}<ArrowUp
                        size={13}
                      />{:else if sortState === 'desc'}<ArrowDown size={13} />{:else}<ArrowUpDown
                        size={13}
                      />{/if}</button
                  >{:else}{columns.find((column) => column.id === header.id)?.header}{/if}
                {#if header.column.getCanResize()}<button
                    class="resize-handle"
                    aria-label={`${columns.find((column) => column.id === header.id)?.header} sütununu yeniden boyutlandır`}
                    onpointerdown={header.getResizeHandler()}
                    ondblclick={() => header.column.resetSize()}
                  ></button>{/if}
              </th>{/each}
          </tr>{/each}</thead
      >
      <tbody style:height={`${totalSize}px`}>
        {#if loading}<tr class="state-row"
            ><td colspan={visibleColumnCount + (selectable ? 1 : 0)}
              ><LoaderCircle class="spin" size={20} /><span role="status">Kayıtlar yükleniyor…</span
              ></td
            ></tr
          >
        {:else if error}<tr class="state-row error"
            ><td colspan={visibleColumnCount + (selectable ? 1 : 0)}
              ><strong>Veriler alınamadı</strong><span>{error}</span><Button
                variant="outline"
                size="sm"
                onclick={() => (onRetry ? onRetry() : query && onQueryChange?.(query))}
                ><RotateCcw size={13} />Yeniden dene</Button
              ></td
            ></tr
          >
        {:else if rows.length === 0}<tr class="state-row"
            ><td colspan={visibleColumnCount + (selectable ? 1 : 0)}
              ><strong>{emptyTitle}</strong><span role="status">{emptyDescription}</span></td
            ></tr
          >
        {:else}{#each virtualRows as virtualRow (virtualRow.key)}{@const row =
              rows[virtualRow.index]}<tr
              class:active={activeRow === virtualRow.index}
              class:selected={row.getIsSelected()}
              aria-rowindex={virtualRow.index + 2}
              aria-selected={row.getIsSelected() || activeRow === virtualRow.index}
              style:transform={`translateY(${virtualRow.start}px)`}
              style:height={`${virtualRow.size}px`}
              onclick={(event) => activateRow(virtualRow.index, event)}
              ondblclick={(event) => openRow(event, row.original)}
            >
              {#if selectable}<td class="select-cell"
                  ><input
                    type="checkbox"
                    aria-label="Kaydı seç"
                    checked={row.getIsSelected()}
                    onchange={row.getToggleSelectedHandler()}
                  /></td
                >{/if}
              {#each row.getVisibleCells() as cell, cellIndex}{@const definition = columns.find(
                  (column) => column.id === cell.column.id
                )}{@const value =
                  cell.getValue() == null || cell.getValue() === ''
                    ? '—'
                    : String(cell.getValue())}{@const link = definition?.link?.(row.original)}<td
                  class:align-right={definition?.align === 'right'}
                  class:align-center={definition?.align === 'center'}
                  class:active-cell={activeRow === virtualRow.index &&
                    activeColumnId === cell.column.id}
                  class:mobile-secondary={cellIndex >= 3}
                  aria-colindex={cellIndex + 1 + (selectable ? 1 : 0)}
                  data-grid-cell
                  data-row-index={virtualRow.index}
                  data-grid-column={cell.column.id}
                  style:width={`${cell.column.getSize()}px`}
                  >{#if link}<a
                      class="grid-link"
                      href={link}
                      onclick={(event) => event.stopPropagation()}>{value}</a
                    >{:else if definition?.cell}{@const Cell = definition.cell}<Cell
                      value={cell.getValue()}
                      row={row.original}
                    />{:else}{value}{/if}{#if onRowOpen && cell.column.id === row
                        .getVisibleCells()
                        .at(-1)?.column.id}<ChevronRight class="row-open" size={14} />{/if}</td
                >{/each}
              <td class="mobile-more">
                <details>
                  <summary>Detay</summary>
                  <dl>
                    {#each row.getVisibleCells().slice(3) as detailCell}
                      {@const detailColumn = columns.find(
                        (column) => column.id === detailCell.column.id
                      )}
                      <div>
                        <dt>{detailColumn?.header}</dt>
                        <dd>{detailCell.getValue() ?? '—'}</dd>
                      </div>
                    {/each}
                  </dl>
                </details>
              </td>
            </tr>{/each}{/if}
      </tbody>
    </table>
  </div>
  {#if (nextCursor && onLoadMore) || (previousPage && onLoadPrevious)}<div class="grid-footer">
      <span>{pageLabel ?? `${rows.length} kayıt`}</span>
      <div class="grid-pagination-actions">
        {#if previousPage && onLoadPrevious}<Button
            variant="outline"
            size="sm"
            disabled={loading || loadingMore}
            onclick={onLoadPrevious}>Önceki sayfa</Button
          >{/if}
        {#if nextCursor && onLoadMore}<Button
            variant="outline"
            size="sm"
            disabled={loading || loadingMore}
            onclick={onLoadMore}>{loadingMore ? 'Yükleniyor…' : 'Sonraki sayfa'}</Button
          >{/if}
      </div>
    </div>{/if}
</div>

<style>
  .varya-grid {
    overflow: hidden;
    border: 1px solid var(--border);
    border-radius: var(--radius-panel);
    background: var(--surface);
  }
  .grid-scroll {
    position: relative;
    max-height: min(68vh, 680px);
    overflow: auto;
    overscroll-behavior: contain;
    scrollbar-width: thin;
    outline: 0;
  }
  .grid-scroll:focus-visible {
    box-shadow: inset 0 0 0 2px var(--focus);
  }
  table {
    min-width: 100%;
    border-collapse: separate;
    border-spacing: 0;
    table-layout: fixed;
    font-size: 12.5px;
  }
  thead {
    z-index: 5;
    background: var(--surface-muted);
    color: var(--text-subtle);
    font-size: 11px;
    text-transform: none;
  }
  thead.sticky {
    position: sticky;
    top: 0;
  }
  th {
    position: relative;
    height: 34px;
    padding: 0 9px;
    border-bottom: 1px solid var(--border-strong);
    text-align: left;
    font-weight: 700;
    white-space: nowrap;
  }
  tbody {
    position: relative;
    display: block;
  }
  thead,
  tr {
    width: 100%;
  }
  thead tr {
    display: flex;
  }
  tbody tr {
    position: absolute;
    left: 0;
    right: 0;
    display: flex;
  }
  td {
    position: relative;
    display: flex;
    align-items: center;
    min-width: 0;
    height: 100%;
    padding: 0 9px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  td.active-cell {
    z-index: 1;
    background: color-mix(in srgb, var(--primary-soft) 72%, var(--surface));
    box-shadow: inset 0 0 0 1px var(--primary);
  }
  .mobile-more {
    display: none;
  }
  .mobile-more details {
    width: 100%;
  }
  .mobile-more summary {
    color: var(--primary);
    cursor: pointer;
    font-size: 11px;
    font-weight: 700;
  }
  .mobile-more dl {
    display: grid;
    gap: 5px;
    margin: 7px 0 0;
    white-space: normal;
  }
  .mobile-more dl > div {
    display: grid;
    grid-template-columns: minmax(80px, 0.45fr) minmax(0, 1fr);
    gap: 8px;
  }
  .mobile-more dt {
    color: var(--text-muted);
  }
  .mobile-more dd {
    margin: 0;
    color: var(--text);
    overflow-wrap: anywhere;
  }
  .grid-link {
    overflow: hidden;
    color: var(--primary-hover);
    text-overflow: ellipsis;
    text-decoration: none;
  }
  .grid-link:hover {
    text-decoration: underline;
  }
  tbody tr.active {
    background: var(--primary-soft);
  }
  tbody tr.selected {
    background: color-mix(in srgb, var(--primary) 12%, var(--surface));
  }
  tbody tr.selected.active {
    background: color-mix(in srgb, var(--primary) 18%, var(--surface));
  }
  .align-right {
    justify-content: flex-end;
    text-align: right;
  }
  .align-center {
    justify-content: center;
    text-align: center;
  }
  .select-cell {
    width: 38px !important;
    flex: 0 0 38px;
    justify-content: center;
    padding: 0;
  }
  .select-cell input {
    accent-color: var(--primary);
  }
  .sort-button {
    width: 100%;
    height: 100%;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 5px;
    border: 0;
    background: transparent;
    color: inherit;
    font: inherit;
    font-weight: inherit;
  }
  .resize-handle {
    position: absolute;
    top: 0;
    right: -3px;
    z-index: 3;
    width: 7px;
    height: 100%;
    border: 0;
    background: transparent;
    cursor: col-resize;
  }
  .resize-handle:hover {
    background: var(--primary);
  }
  :global(.row-open) {
    position: absolute;
    right: 4px;
    color: var(--text-muted);
  }
  .state-row {
    position: relative !important;
    display: table-row !important;
    height: 180px !important;
  }
  .state-row td {
    width: 100% !important;
    display: flex;
    flex-direction: column;
    justify-content: center;
    gap: 6px;
    color: var(--text-muted);
    text-align: center;
  }
  .state-row strong {
    color: var(--text);
  }
  .state-row.error span {
    color: var(--danger);
  }
  :global(.spin) {
    animation: spin 1s linear infinite;
  }
  .grid-footer {
    min-height: 40px;
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 5px 9px;
    border-top: 1px solid var(--border);
    color: var(--text-muted);
    font-size: 11px;
  }
  .grid-pagination-actions {
    display: flex;
    align-items: center;
    gap: 6px;
  }
  @media (max-width: 680px) {
    .grid-scroll {
      max-height: min(70vh, 640px);
    }
    .mobile-secondary {
      display: none !important;
    }
    .mobile-more {
      display: flex;
      width: 76px !important;
      flex: 0 0 76px;
      height: auto;
      padding: 4px 6px;
      overflow: visible;
      white-space: normal;
    }
  }
  @keyframes spin {
    to {
      transform: rotate(360deg);
    }
  }
</style>
