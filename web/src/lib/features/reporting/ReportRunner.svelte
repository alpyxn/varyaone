<script lang="ts">
  import { onMount } from 'svelte';
  import { RefreshCw, FileSpreadsheet, Printer, TriangleAlert } from '@lucide/svelte';
  import { api, APIRequestError, type Session } from '$lib/api';
  import { Button } from '$lib/components/ui/button';
  import { DateRangePicker } from '$lib/components/varya/date-range-picker';
  import { formatDate, formatMoney, formatQuantity } from '$lib/design/formatters';
  import { downloadXls } from '$lib/design/spreadsheet';
  import { printDocument, ph } from '$lib/design/print';
  import { printableCompany } from '$lib/features/settings/company-profile';
  import { fetchReport, type ReportFilterValues, type ReportRow } from './api';
  import type { ReportColumn, ReportDef } from './registry';

  let { def }: { def: ReportDef } = $props();

  function isoDaysAgo(days: number) {
    const d = new Date();
    d.setDate(d.getDate() - days);
    return d.toISOString().slice(0, 10);
  }

  let session = $state<Session | null>(null);
  let dateFrom = $state(isoDaysAgo(30));
  let dateTo = $state(isoDaysAgo(0));
  let direction = $state<'SALES' | 'PURCHASE'>('SALES');

  let rows = $state<ReportRow[]>([]);
  let loading = $state(false);
  let ran = $state(false);
  let error = $state('');
  let pageNumber = $state(1);
  const pageSize = 50;
  let sortKey = $state('');
  let sortDir = $state<1 | -1>(1);

  const hasDateRange = $derived(def.filters.includes('dateRange'));
  const hasDirection = $derived(def.filters.includes('direction'));

  const company = $derived(
    session?.companies.find((c) => c.id === session?.current_company_id) ?? session?.companies?.[0]
  );

  const sortedRows = $derived.by(() => {
    if (!sortKey) return rows;
    const factor = sortDir;
    return [...rows].sort((a, b) => {
      const av = a[sortKey];
      const bv = b[sortKey];
      const an = Number(av);
      const bn = Number(bv);
      if (av !== '' && bv !== '' && !Number.isNaN(an) && !Number.isNaN(bn))
        return (an - bn) * factor;
      return String(av ?? '').localeCompare(String(bv ?? ''), 'tr') * factor;
    });
  });
  const totalPages = $derived(Math.max(1, Math.ceil(sortedRows.length / pageSize)));
  const visibleRows = $derived(
    sortedRows.slice((pageNumber - 1) * pageSize, pageNumber * pageSize)
  );

  let inflight: AbortController | null = null;

  onMount(() => {
    void loadSession();
    void run();
    return () => inflight?.abort();
  });

  async function loadSession() {
    try {
      session = await api<Session>('/session');
    } catch {
      session = null;
    }
  }

  async function run() {
    inflight?.abort();
    const controller = new AbortController();
    inflight = controller;
    loading = true;
    error = '';
    ran = true;
    pageNumber = 1;
    const values: ReportFilterValues = { from: dateFrom, to: dateTo, direction };
    try {
      const result = await fetchReport(def, values, controller.signal);
      if (controller.signal.aborted) return;
      rows = result;
    } catch (err) {
      if (controller.signal.aborted) return;
      rows = [];
      error =
        err instanceof APIRequestError ? err.message : 'Rapor alınamadı. Lütfen tekrar deneyin.';
    } finally {
      if (inflight === controller) {
        inflight = null;
        loading = false;
      }
    }
  }

  function toggleSort(key: string) {
    if (sortKey === key) sortDir = sortDir === 1 ? -1 : 1;
    else {
      sortKey = key;
      sortDir = 1;
    }
    pageNumber = 1;
  }

  function currencyFor(row: ReportRow, col: ReportColumn): string {
    const raw = col.currencyKey ? row[col.currencyKey] : undefined;
    return typeof raw === 'string' && raw ? raw : (company?.base_currency ?? 'TRY');
  }

  function renderCell(row: ReportRow, col: ReportColumn): string {
    const raw = row[col.key];
    if (raw === null || raw === undefined || raw === '') return '—';
    switch (col.format) {
      case 'money':
        return formatMoney(raw as string, currencyFor(row, col));
      case 'qty':
        return formatQuantity(raw as string);
      case 'date':
        return formatDate(String(raw));
      default:
        return String(raw);
    }
  }

  function filterSummary(): string {
    const parts: string[] = [];
    if (hasDateRange) parts.push(`${formatDate(dateFrom)} – ${formatDate(dateTo)}`);
    if (hasDirection) parts.push(direction === 'PURCHASE' ? 'Alış' : 'Satış');
    return parts.join(' · ');
  }

  function exportXls() {
    const header = def.columns.map((c) => c.label);
    const body = sortedRows.map((row) =>
      def.columns.map((c) => {
        const raw = row[c.key];
        if (c.format === 'money' || c.format === 'qty')
          return raw == null || raw === '' ? '' : Number(raw);
        if (c.format === 'date') return raw ? formatDate(String(raw)) : '';
        return raw == null ? '' : String(raw);
      })
    );
    const stamp = hasDateRange ? `-${dateFrom}_${dateTo}` : '';
    downloadXls(`${def.id}${stamp}`, def.label, body, header);
  }

  async function printReport() {
    // The session's company record carries no logo or tax number; the printed
    // header wants both.
    const profile = (await printableCompany()) ?? company;
    const head = def.columns
      .map((c) => `<th${c.align === 'right' ? ' class="right"' : ''}>${ph(c.label)}</th>`)
      .join('');
    const bodyRows = sortedRows
      .map(
        (row) =>
          `<tr>${def.columns
            .map(
              (c) =>
                `<td${c.align === 'right' ? ' class="right"' : ''}>${ph(renderCell(row, c))}</td>`
            )
            .join('')}</tr>`
      )
      .join('');
    printDocument({
      title: def.label,
      subtitle: filterSummary(),
      company: {
        name: profile?.trade_name || profile?.legal_name || 'Şirket',
        logo: profile?.logo,
        taxNumber: profile?.tax_number
      },
      bodyHtml: `<table><thead><tr>${head}</tr></thead><tbody>${bodyRows}</tbody></table>`,
      bodyStyles:
        'table{width:100%;border-collapse:collapse}th,td{border:1px solid #ccc;padding:5px 7px;font-size:11px;text-align:left}th{background:#f2f2f2}.right{text-align:right}'
    });
  }
</script>

<section class="report">
  <header>
    <h1>{def.label}</h1>
    {#if def.description}<p>{def.description}</p>{/if}
  </header>

  <div class="toolbar" class:bare={!hasDateRange && !hasDirection}>
    {#if hasDateRange}
      <div class="field">
        <span class="label">Tarih aralığı</span>
        <DateRangePicker bind:start={dateFrom} bind:end={dateTo} />
      </div>
    {/if}
    {#if hasDirection}
      <label class="field">
        <span class="label">Belge türü</span>
        <select bind:value={direction}>
          <option value="SALES">Satış</option>
          <option value="PURCHASE">Alış</option>
        </select>
      </label>
    {/if}
    <div class="actions">
      <Button onclick={run} disabled={loading}>
        <RefreshCw size={14} />
        {loading ? 'Yükleniyor…' : 'Uygula'}
      </Button>
      <Button variant="outline" onclick={exportXls} disabled={rows.length === 0}>
        <FileSpreadsheet size={14} />Excel
      </Button>
      <Button variant="outline" onclick={printReport} disabled={rows.length === 0}>
        <Printer size={14} />Yazdır
      </Button>
    </div>
  </div>

  {#if def.warnKey && rows.some((r) => r[def.warnKey as string])}
    <p class="notice"><TriangleAlert size={14} />{def.warnText}</p>
  {/if}

  {#if error}
    <p class="state error">{error}</p>
  {:else if ran && !loading && rows.length === 0}
    <p class="state">Bu ölçütlerle kayıt bulunamadı.</p>
  {:else if loading && rows.length === 0}
    <p class="state">Yükleniyor…</p>
  {:else if rows.length > 0}
    <div class="table-wrap">
      <table>
        <thead>
          <tr>
            {#each def.columns as col (col.key)}
              <th
                class:right={col.align === 'right'}
                aria-sort={sortKey === col.key
                  ? sortDir === 1
                    ? 'ascending'
                    : 'descending'
                  : 'none'}
              >
                <button type="button" onclick={() => toggleSort(col.key)}>
                  {col.label}{sortKey === col.key ? (sortDir === 1 ? ' ↑' : ' ↓') : ''}
                </button>
              </th>
            {/each}
          </tr>
        </thead>
        <tbody>
          {#each visibleRows as row, i (i)}
            <tr>
              {#each def.columns as col, ci (col.key)}
                <td class:right={col.align === 'right'}>
                  {#if ci === 0 && def.warnKey && row[def.warnKey]}
                    <span class="warn" title={def.warnText}><TriangleAlert size={12} /></span>
                  {/if}
                  {renderCell(row, col)}
                </td>
              {/each}
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
    <footer class="pager">
      <span>{sortedRows.length} kayıt</span>
      {#if totalPages > 1}
        <div>
          <Button
            variant="outline"
            size="sm"
            disabled={pageNumber <= 1}
            onclick={() => (pageNumber = Math.max(1, pageNumber - 1))}>Önceki</Button
          >
          <span>{pageNumber} / {totalPages}</span>
          <Button
            variant="outline"
            size="sm"
            disabled={pageNumber >= totalPages}
            onclick={() => (pageNumber = Math.min(totalPages, pageNumber + 1))}>Sonraki</Button
          >
        </div>
      {/if}
    </footer>
  {/if}
</section>

<style>
  .report {
    display: flex;
    flex-direction: column;
    gap: 16px;
  }
  header h1 {
    margin: 0;
    font-size: 1.25rem;
  }
  header p {
    margin: 3px 0 0;
    color: var(--text-muted);
    font-size: 0.86rem;
  }
  .toolbar {
    display: flex;
    flex-wrap: wrap;
    align-items: flex-end;
    gap: 14px 18px;
    padding: 14px 16px;
    border: 1px solid var(--border, #e4e4e7);
    border-radius: 12px;
    background: var(--surface-muted, #fafafa);
  }
  .toolbar.bare {
    padding: 0;
    border: 0;
    background: none;
  }
  .field {
    display: flex;
    flex-direction: column;
    gap: 5px;
  }
  .label {
    font-size: 0.74rem;
    font-weight: 500;
    color: var(--text-muted);
  }
  .field select {
    height: 34px;
    min-width: 130px;
    border: 1px solid var(--border, #d4d4d8);
    border-radius: 7px;
    padding: 0 9px;
    background: var(--surface, #fff);
    color: inherit;
  }
  .actions {
    display: flex;
    gap: 8px;
    margin-left: auto;
  }
  .notice {
    display: flex;
    align-items: center;
    gap: 8px;
    margin: 0;
    padding: 9px 12px;
    border-radius: 9px;
    background: color-mix(in srgb, var(--warning, #f59e0b) 14%, transparent);
    color: var(--warning-strong, #92400e);
    font-size: 0.83rem;
  }
  .table-wrap {
    overflow-x: auto;
    border: 1px solid var(--border, #e4e4e7);
    border-radius: 12px;
  }
  table {
    width: 100%;
    border-collapse: collapse;
    font-size: 0.86rem;
  }
  th,
  td {
    padding: 9px 12px;
    border-bottom: 1px solid var(--border, #efeff1);
    text-align: left;
    white-space: nowrap;
  }
  tbody tr:last-child td {
    border-bottom: 0;
  }
  tbody tr:hover {
    background: var(--surface-muted, #f7f7f8);
  }
  th {
    background: var(--surface-muted, #f5f5f6);
    position: sticky;
    top: 0;
  }
  th button {
    all: unset;
    cursor: pointer;
    font: inherit;
    font-weight: 600;
    display: inline-flex;
  }
  th button:focus-visible {
    outline: 2px solid var(--primary, #2563eb);
    outline-offset: 2px;
  }
  .right {
    text-align: right;
  }
  td.right {
    font-variant-numeric: tabular-nums;
  }
  .warn {
    color: var(--warning, #b45309);
    margin-right: 4px;
    vertical-align: -2px;
  }
  .state {
    padding: 32px;
    text-align: center;
    color: var(--text-muted);
    border: 1px dashed var(--border, #e4e4e7);
    border-radius: 12px;
  }
  .state.error {
    color: var(--danger, #b91c1c);
    border-color: color-mix(in srgb, var(--danger, #b91c1c) 40%, transparent);
  }
  .pager {
    display: flex;
    justify-content: space-between;
    align-items: center;
    color: var(--text-muted);
    font-size: 0.8rem;
  }
  .pager div {
    display: flex;
    align-items: center;
    gap: 10px;
  }
</style>
