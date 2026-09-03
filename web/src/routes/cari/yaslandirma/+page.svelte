<script lang="ts">
  import { onMount } from 'svelte';
  import { FileSpreadsheet, Printer, RefreshCw } from '@lucide/svelte';
  import { api, APIRequestError, type Company } from '$lib/api';
  import { Button } from '$lib/components/ui/button';
  import { CurrencySelect } from '$lib/components/varya/currency-select';
  import { DateInput } from '$lib/components/varya/date-input';
  import { formatDate, formatMoney } from '$lib/design/formatters';
  import { addSignedDecimalStrings } from '$lib/design/decimal';
  import { downloadXls } from '$lib/design/spreadsheet';
  import { ph, printDocument } from '$lib/design/print';
  import { getPartyAging } from '$lib/features/parties/api';
  import type { PartyAgingRow } from '$lib/features/parties/types';

  /** Bucket columns, in the order they are shown, exported and printed. */
  const BUCKETS = [
    { key: 'not_due', label: 'Vadesi Gelmemiş' },
    { key: 'days_0_30', label: '1-30 Gün' },
    { key: 'days_31_60', label: '31-60 Gün' },
    { key: 'days_61_90', label: '61-90 Gün' },
    { key: 'days_90_plus', label: '90+ Gün' },
    { key: 'total', label: 'Toplam' }
  ] as const;

  const today = new Date().toISOString().slice(0, 10);
  let asOf = $state(today);
  let side = $state<'RECEIVABLE' | 'PAYABLE'>('RECEIVABLE');
  let currency = $state('');

  let rows = $state<PartyAgingRow[]>([]);
  let company = $state<Company>();
  let loading = $state(true);
  let error = $state('');
  let inflight: AbortController | undefined;

  const sideLabel = $derived(side === 'RECEIVABLE' ? 'Alacaklar' : 'Borçlar');
  const partyLabel = $derived(side === 'RECEIVABLE' ? 'Müşteri' : 'Tedarikçi');

  /** One table per currency; amounts are never converted, so they never mix. */
  const groups = $derived.by(() => {
    const byCurrency = new Map<string, PartyAgingRow[]>();
    for (const row of rows) {
      const bucket = byCurrency.get(row.currency);
      if (bucket) bucket.push(row);
      else byCurrency.set(row.currency, [row]);
    }
    return [...byCurrency.entries()]
      .sort((left, right) => left[0].localeCompare(right[0]))
      .map(([code, items]) => ({ currency: code, items, totals: sumRows(items) }));
  });

  // An unparseable figure must not silently vanish into a wrong total, so the
  // sum stays undefined and the cell prints an em dash instead of a number.
  function sumRows(items: PartyAgingRow[]): Record<string, string | undefined> {
    const totals: Record<string, string | undefined> = {};
    for (const bucket of BUCKETS) {
      totals[bucket.key] = items.reduce<string | undefined>(
        (sum, row) =>
          sum === undefined ? undefined : addSignedDecimalStrings(sum, row[bucket.key] || '0'),
        '0'
      );
    }
    return totals;
  }

  const totalText = (value: string | undefined, code: string) =>
    value === undefined ? '—' : formatMoney(value, code);

  async function load() {
    inflight?.abort();
    const request = new AbortController();
    inflight = request;
    loading = true;
    error = '';
    try {
      const params = new URLSearchParams({ as_of: asOf, side });
      if (currency.trim()) params.set('currency', currency.trim().toUpperCase());
      const [report, companyResult] = await Promise.allSettled([
        getPartyAging(params, request.signal),
        api<Company>('/company', { signal: request.signal })
      ]);
      if (request.signal.aborted) return;
      if (report.status === 'rejected') throw report.reason;
      rows = Array.isArray(report.value.items) ? report.value.items : [];
      if (companyResult.status === 'fulfilled') company = companyResult.value;
    } catch (cause) {
      if (request.signal.aborted) return;
      rows = [];
      error =
        cause instanceof APIRequestError
          ? cause.message
          : 'Cari yaşlandırma alınamadı. Lütfen tekrar deneyin.';
    } finally {
      if (inflight === request) {
        inflight = undefined;
        loading = false;
      }
    }
  }

  onMount(() => {
    void load();
    return () => inflight?.abort();
  });

  function exportExcel() {
    const header = ['Cari Kodu', partyLabel, 'Para', ...BUCKETS.map((bucket) => bucket.label)];
    const body = rows.map((row) => [
      row.party_code,
      row.party_name,
      row.currency,
      ...BUCKETS.map((bucket) => Number(row[bucket.key] || '0'))
    ]);
    downloadXls(
      `cari-yaslandirma-${side.toLowerCase()}-${asOf}`,
      `Yaşlandırma ${asOf}`,
      body,
      header
    );
  }

  function printReport() {
    const head = ['Cari Kodu', partyLabel, 'Para', ...BUCKETS.map((bucket) => bucket.label)]
      .map((label, index) => `<th${index > 2 ? ' class="right"' : ''}>${ph(label)}</th>`)
      .join('');
    const bodyHtml = groups
      .map((group) => {
        const body = group.items
          .map(
            (row) =>
              `<tr><td>${ph(row.party_code)}</td><td>${ph(row.party_name)}</td><td>${ph(row.currency)}</td>${BUCKETS.map(
                (bucket) =>
                  `<td class="right">${ph(formatMoney(row[bucket.key] || '0', row.currency))}</td>`
              ).join('')}</tr>`
          )
          .join('');
        const totals = `<tr class="total"><td colspan="3">Toplam (${ph(group.currency)})</td>${BUCKETS.map(
          (bucket) =>
            `<td class="right">${ph(totalText(group.totals[bucket.key], group.currency))}</td>`
        ).join('')}</tr>`;
        return `<table><thead><tr>${head}</tr></thead><tbody>${body}${totals}</tbody></table>`;
      })
      .join('');
    printDocument({
      title: `Cari Yaşlandırma · ${sideLabel}`,
      subtitle: `${formatDate(asOf)} tarihine göre`,
      company: {
        name: company?.trade_name || company?.legal_name || 'Şirket',
        logo: company?.logo || undefined,
        taxNumber: company?.tax_number
      },
      bodyHtml,
      bodyStyles:
        'table{width:100%;border-collapse:collapse;margin-bottom:14px}th,td{border:1px solid #ccc;padding:5px 7px;font-size:11px;text-align:left}th{background:#f2f2f2}.right{text-align:right}.total td{font-weight:700;background:#fafafa}',
      footerNote: 'Bu döküm Varya One tarafından oluşturulmuştur.'
    });
  }
</script>

<svelte:head><title>Cari Yaşlandırma · Varya One</title></svelte:head>

<section class="aging">
  <header>
    <h1>Cari Yaşlandırma</h1>
  </header>

  <div class="toolbar">
    <label class="field">
      <span class="label">Tarih</span>
      <DateInput bind:value={asOf} ariaLabel="Yaşlandırma tarihi" />
    </label>
    <label class="field">
      <span class="label">Tür</span>
      <select bind:value={side}>
        <option value="RECEIVABLE">Alacaklar (müşteri)</option>
        <option value="PAYABLE">Borçlar (tedarikçi)</option>
      </select>
    </label>
    <label class="field">
      <span class="label">Para birimi</span>
      <CurrencySelect bind:value={currency} allLabel="Tümü" ariaLabel="Para birimi" />
    </label>
    <div class="actions">
      <Button onclick={load} disabled={loading}>
        <RefreshCw size={14} />{loading ? 'Yükleniyor…' : 'Uygula'}
      </Button>
      <Button variant="outline" onclick={exportExcel} disabled={rows.length === 0}>
        <FileSpreadsheet size={14} />Excel
      </Button>
      <Button variant="outline" onclick={printReport} disabled={rows.length === 0}>
        <Printer size={14} />Yazdır
      </Button>
    </div>
  </div>

  {#if error}
    <p class="state error" role="alert">{error}</p>
  {:else if loading && rows.length === 0}
    <p class="state">Yükleniyor…</p>
  {:else if rows.length === 0}
    <p class="state">{formatDate(asOf)} tarihinde açık {sideLabel.toLowerCase()} bulunmuyor.</p>
  {:else}
    {#each groups as group (group.currency)}
      <div class="table-wrap">
        <table>
          <caption>{group.currency}</caption>
          <thead>
            <tr>
              <th>Cari Kodu</th>
              <th>{partyLabel}</th>
              {#each BUCKETS as bucket (bucket.key)}
                <th class="right">{bucket.label}</th>
              {/each}
            </tr>
          </thead>
          <tbody>
            {#each group.items as row (row.party_id + row.currency)}
              <tr>
                <td>{row.party_code || '—'}</td>
                <td>
                  <a href={`/cari/kartlar/${row.party_id}/ekstre`}>{row.party_name || '—'}</a>
                </td>
                {#each BUCKETS as bucket (bucket.key)}
                  <td class="right" class:strong={bucket.key === 'total'}>
                    {formatMoney(row[bucket.key] || '0', row.currency)}
                  </td>
                {/each}
              </tr>
            {/each}
          </tbody>
          <tfoot>
            <tr>
              <td colspan="2">Toplam</td>
              {#each BUCKETS as bucket (bucket.key)}
                <td class="right">{totalText(group.totals[bucket.key], group.currency)}</td>
              {/each}
            </tr>
          </tfoot>
        </table>
      </div>
    {/each}
  {/if}
</section>

<style>
  .aging {
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
  /* Matches CurrencySelect next to it. The explicit text color matters: the
     global `.field` rule paints the label with --text-subtle, and an inherited
     color made the control look disabled. */
  .field select {
    min-width: 180px;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    padding: 8px 10px;
    background: var(--surface);
    color: var(--text);
    font: inherit;
    cursor: pointer;
  }
  .field select:focus {
    border-color: var(--primary);
    box-shadow: 0 0 0 3px var(--focus);
    outline: none;
  }
  .actions {
    display: flex;
    gap: 8px;
    margin-left: auto;
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
  caption {
    padding: 10px 12px;
    text-align: left;
    font-weight: 600;
    color: var(--text-muted);
  }
  th,
  td {
    padding: 9px 12px;
    border-bottom: 1px solid var(--border, #efeff1);
    text-align: left;
    white-space: nowrap;
  }
  th {
    background: var(--surface-muted, #f5f5f6);
  }
  .right {
    text-align: right;
  }
  td.right {
    font-variant-numeric: tabular-nums;
  }
  td.strong {
    font-weight: 600;
  }
  tbody tr:hover {
    background: var(--surface-muted, #f7f7f8);
  }
  tfoot td {
    font-weight: 600;
    border-top: 1px solid var(--border, #e4e4e7);
    border-bottom: 0;
    background: var(--surface-muted, #fafafa);
  }
  a {
    color: inherit;
    text-decoration: none;
  }
  a:hover {
    text-decoration: underline;
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
</style>
