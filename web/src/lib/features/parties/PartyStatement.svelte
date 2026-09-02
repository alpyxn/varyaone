<script lang="ts">
  import { ArrowLeft, Download, Printer } from '@lucide/svelte';
  import { untrack } from 'svelte';
  import { api, type Company } from '$lib/api';
  import { Button } from '$lib/components/ui/button';
  import { StateBlock } from '$lib/components/varya/status';
  import { CurrencySelect } from '$lib/components/varya/currency-select';
  import { DateInput } from '$lib/components/varya/date-input';
  import { formatDate, formatMoney } from '$lib/design/formatters';
  import { describeBalance } from '$lib/design/balance';
  import {
    addSignedDecimalStrings,
    isZeroDecimal,
    multiplyDecimalStrings,
    negateDecimalString
  } from '$lib/design/decimal';
  import { downloadXls } from '$lib/design/spreadsheet';
  import { ph, printDocument } from '$lib/design/print';
  import { getParty, getPartyOpenItems, getPartyStatementReport } from './api';
  import type { Party, PartyOpenItem, PartyStatementReport } from './types';

  type Props = { partyID: string };
  let { partyID }: Props = $props();

  function isoDaysAgo(days: number) {
    const d = new Date();
    d.setDate(d.getDate() - days);
    return d.toISOString().slice(0, 10);
  }

  let party = $state<Party>();
  let report = $state<PartyStatementReport>();
  let openItems = $state<PartyOpenItem[]>([]);
  let company = $state<Company>();
  let loadState = $state<'loading' | 'ready' | 'error'>('loading');
  let errorMessage = $state('');
  let from = $state(isoDaysAgo(90));
  let to = $state(new Date().toISOString().slice(0, 10));
  let currency = $state('');
  let tab = $state<'movements' | 'open' | 'due'>('movements');
  let openItemsError = $state('');
  let paginationError = $state('');
  let nextCursor = $state<string>();
  let loadingMore = $state(false);
  let activeRequest: AbortController | undefined;
  let requestSequence = 0;

  async function load(append = false) {
    if (append && (!nextCursor || loadingMore)) return;
    activeRequest?.abort();
    const request = new AbortController();
    activeRequest = request;
    const sequence = ++requestSequence;
    if (append) {
      loadingMore = true;
      paginationError = '';
    } else {
      loadState = 'loading';
      errorMessage = '';
      openItemsError = '';
      paginationError = '';
      nextCursor = undefined;
    }
    try {
      const params = new URLSearchParams({ from, to });
      if (currency.trim()) params.set('currency', currency.trim().toUpperCase());
      if (append && nextCursor) params.set('cursor', nextCursor);
      const [partyResult, reportResult, openResult, companyResult] = await Promise.allSettled([
        getParty(partyID, request.signal),
        getPartyStatementReport(partyID, params, request.signal),
        getPartyOpenItems(partyID, new URLSearchParams(), request.signal),
        api<Company>('/company', { signal: request.signal })
      ]);
      if (request.signal.aborted || sequence !== requestSequence) return;
      if (partyResult.status === 'rejected') throw partyResult.reason;
      if (reportResult.status === 'rejected') throw reportResult.reason;
      if (!Array.isArray(reportResult.value.items)) throw new Error('Ekstre yanıtı geçersiz.');
      party = partyResult.value;
      report =
        append && report
          ? { ...reportResult.value, items: [...report.items, ...reportResult.value.items] }
          : reportResult.value;
      nextCursor = reportResult.value.next_cursor;
      if (openResult.status === 'fulfilled' && Array.isArray(openResult.value.items)) {
        openItems = openResult.value.items;
      } else {
        openItems = [];
        openItemsError =
          openResult.status === 'rejected' && openResult.reason instanceof Error
            ? openResult.reason.message
            : 'Açık kalem yanıtı geçersiz.';
      }
      if (companyResult.status === 'fulfilled') company = companyResult.value;
      loadState = 'ready';
    } catch (cause) {
      if (request.signal.aborted || sequence !== requestSequence) return;
      const message = cause instanceof Error ? cause.message : 'Ekstre alınamadı.';
      if (append) paginationError = message;
      else {
        errorMessage = message;
        loadState = 'error';
      }
    } finally {
      if (sequence === requestSequence) loadingMore = false;
    }
  }

  $effect(() => {
    partyID;
    untrack(() => void load());
  });

  const overdue = $derived(
    [...openItems].sort((a, b) => (a.due_date ?? '9999').localeCompare(b.due_date ?? '9999'))
  );
  const openBaseCurrency = $derived(
    openItems.find((i) => i.base_currency)?.base_currency ||
      report?.currency ||
      party?.default_currency ||
      'TRY'
  );
  const openAmountInBase = (item: PartyOpenItem) => {
    if (item.currency === openBaseCurrency && !item.exchange_rate) return item.open_amount;
    if (item.currency !== openBaseCurrency && !item.exchange_rate) return undefined;
    return multiplyDecimalStrings(item.open_amount, item.exchange_rate || '1', 4);
  };
  const totalOpenSide = (side: 'RECEIVABLE' | 'PAYABLE') =>
    openItems
      .filter((item) => item.side === side)
      .map(openAmountInBase)
      .reduce<string | undefined>(
        (sum, value) =>
          sum === undefined || value === undefined
            ? undefined
            : addSignedDecimalStrings(sum, value),
        '0'
      );
  const openReceivableTotal = $derived(totalOpenSide('RECEIVABLE'));
  const openPayableTotal = $derived(totalOpenSide('PAYABLE'));
  const openNetTotal = $derived(
    openReceivableTotal === undefined || openPayableTotal === undefined
      ? undefined
      : addSignedDecimalStrings(openReceivableTotal, negateDecimalString(openPayableTotal))
  );
  const openTotalsIncomplete = $derived(
    openItems.length > 0 && (openReceivableTotal === undefined || openPayableTotal === undefined)
  );

  function daysLate(due?: string) {
    if (!due) return '';
    const diff = Math.floor((Date.now() - new Date(due).getTime()) / 86400000);
    return diff > 0 ? `${diff} gün` : '';
  }

  const periodLabel = $derived(`${formatDate(from)} – ${formatDate(to)}`);
  // When no single currency is filtered the backend converts every figure to
  // the company base currency; summary + running balance are shown in it.
  const reportCurrency = $derived(report?.currency || currency || party?.default_currency || 'TRY');
  const converted = $derived(
    !currency.trim() && report?.items?.some((e) => e.currency !== reportCurrency)
  );

  /** Deep link from a ledger row to its source document / finance record. */
  function sourceLink(entry: { source_href?: string }): string | undefined {
    return entry.source_href || undefined;
  }

  function exportExcel() {
    if (!report) return;
    const rows = report.items.map((e) => [
      formatDate(e.document_date),
      e.due_date ? formatDate(e.due_date) : '',
      e.document_no ?? '',
      e.description,
      e.currency,
      e.debit ? formatMoney(e.debit, e.currency) : '—',
      e.credit ? formatMoney(e.credit, e.currency) : '—',
      e.running_balance && reportCurrency
        ? describeBalance(e.running_balance, reportCurrency).headline
        : '—'
    ]);
    downloadXls(
      `ekstre-${party?.code ?? partyID}-${from}_${to}`,
      `Ekstre ${party?.code ?? ''}`.trim(),
      rows,
      [
        'Tarih',
        'Vade',
        'Belge No',
        'Açıklama',
        'Para',
        'Borç',
        'Alacak',
        `Bakiye (${reportCurrency || '—'})`
      ]
    );
  }

  function printStatement(openItemsOnly = false) {
    if (!report || !party) return;
    const cur = reportCurrency;
    const openingB = describeBalance(report.opening_balance, cur);
    const closingB = describeBalance(report.closing_balance, cur);
    let bodyHtml = `<div class="summary-grid">
      <div class="cell"><span>Açılış bakiyesi</span><strong>${ph(openingB.headline)}</strong></div>
      <div class="cell"><span>Dönem borç</span><strong>${ph(formatMoney(report.total_debit, cur))}</strong></div>
      <div class="cell"><span>Dönem alacak</span><strong>${ph(formatMoney(report.total_credit, cur))}</strong></div>
      <div class="cell"><span>Kapanış bakiyesi</span><strong>${ph(closingB.headline)}</strong><span>${ph(closingB.meaning)}</span></div>
    </div>`;

    if (openItemsOnly) {
      bodyHtml += `<h3>Açık Borç/Alacak Dökümü</h3><table>
        <tr><th>Belge</th><th>Yön</th><th>Tarih</th><th>Vade</th><th>Para</th><th class="right">Orijinal</th><th class="right">Açık</th></tr>
        ${openItems
          .map(
            (i) =>
              `<tr><td>${ph(i.document_no ?? '—')}</td><td>${ph(i.side === 'RECEIVABLE' ? 'Alacak' : i.side === 'PAYABLE' ? 'Borç' : '—')}</td><td>${ph(formatDate(i.document_date))}</td><td>${ph(i.due_date ? formatDate(i.due_date) : '—')}</td><td>${ph(i.currency)}</td><td class="right">${ph(formatMoney(i.original_amount, i.currency))}</td><td class="right">${ph(formatMoney(i.open_amount, i.currency))}</td></tr>`
          )
          .join('')}
      </table><p>Net açık (${ph(openBaseCurrency)}): ${ph(openNetTotal === undefined ? '—' : describeBalance(openNetTotal, openBaseCurrency).headline)}</p>`;
    } else {
      bodyHtml += `<table>
        <tr><th>Tarih</th><th>Vade</th><th>Belge</th><th>Açıklama</th><th>Para</th><th class="right">Borç</th><th class="right">Alacak</th><th class="right">Bakiye (${ph(cur)})</th></tr>
        ${report.items
          .map((e) => {
            const rb = describeBalance(e.running_balance ?? '0', cur);
            return `<tr><td>${ph(formatDate(e.document_date))}</td><td>${ph(e.due_date ? formatDate(e.due_date) : '—')}</td><td>${ph(e.document_no ?? '—')}</td><td>${ph(e.description)}</td><td>${ph(e.currency)}</td><td class="right">${ph(isZeroDecimal(e.debit) ? '—' : formatMoney(e.debit, e.currency))}</td><td class="right">${ph(isZeroDecimal(e.credit) ? '—' : formatMoney(e.credit, e.currency))}</td><td class="right">${ph(e.running_balance ? rb.amount + (rb.label ? ' ' + rb.label : '') : '—')}</td></tr>`;
          })
          .join('')}
      </table>`;
      if (converted) {
        bodyHtml += `<p style="font-size:10px;color:#6b7280;margin-top:6px">Borç/alacak sütunları işlemin kendi para biriminde, bakiye ${ph(cur)} karşılığındadır.</p>`;
      }
    }

    printDocument({
      title: openItemsOnly ? 'Açık Borç/Alacak Dökümü' : 'Cari Ekstre',
      subtitle: `${party.display_name} (${party.code}) · ${periodLabel}`,
      company: {
        name: company?.trade_name || company?.legal_name || 'Şirket',
        logo: company?.logo || undefined,
        taxNumber: company?.tax_number
      },
      bodyHtml,
      footerNote: 'Bu döküm Varya One tarafından oluşturulmuştur.'
    });
  }
</script>

<header class="page-header print-hidden">
  <div>
    <a class="back" href={`/cari/kartlar/${partyID}`}><ArrowLeft size={14} />Cari Kartı</a>
    <h1>{party?.display_name ?? 'Cari'} · Ekstre</h1>
  </div>
  <div class="actions">
    <Button variant="outline" size="sm" onclick={exportExcel}><Download size={14} />Excel</Button>
    <Button variant="outline" size="sm" onclick={() => printStatement(false)}>
      <Printer size={14} />Yazdır
    </Button>
    <Button variant="outline" size="sm" onclick={() => printStatement(true)}>
      Açık Borç/Alacak Dökümü
    </Button>
  </div>
</header>

<div class="filters print-hidden">
  <label><span>Başlangıç</span><DateInput bind:value={from} ariaLabel="Başlangıç" /></label>
  <label><span>Bitiş</span><DateInput bind:value={to} ariaLabel="Bitiş" /></label>
  <label
    ><span>Para birimi</span><CurrencySelect
      bind:value={currency}
      allLabel="Tümü"
      ariaLabel="Para birimi"
    /></label
  >
  <Button size="sm" onclick={() => void load()}>Uygula</Button>
</div>

<StateBlock
  loading={loadState === 'loading' && !loadingMore}
  error={loadState === 'error' ? errorMessage : ''}
  onRetry={load}
/>

{#if loadState === 'ready' && report}
  {@const openingBalance = describeBalance(report.opening_balance, reportCurrency)}
  {@const closingBalance = describeBalance(report.closing_balance, reportCurrency)}
  <section class="summary">
    <div>
      <span>Açılış bakiyesi</span>
      <strong>{openingBalance.headline}</strong>
    </div>
    <div>
      <span>Dönem borç</span><strong>{formatMoney(report.total_debit, reportCurrency)}</strong>
    </div>
    <div>
      <span>Dönem alacak</span><strong>{formatMoney(report.total_credit, reportCurrency)}</strong>
    </div>
    <div class="closing {closingBalance.tone}">
      <span>Kapanış bakiyesi</span>
      <strong>{closingBalance.headline}</strong>
      <small>{closingBalance.meaning}</small>
    </div>
  </section>
  {#if converted}
    <p class="convert-note">
      Para birimi seçilmedi: tutarlar işlemin kendi para biriminde, özet ve bakiye {reportCurrency} karşılığında
      gösteriliyor.
    </p>
  {/if}
  {#if openItemsError}
    <div class="statement-error" role="alert">
      Açık kalemler alınamadı: {openItemsError}<Button
        variant="outline"
        size="sm"
        onclick={() => void load()}>Yeniden dene</Button
      >
    </div>
  {/if}

  <div class="tabs print-hidden" role="tablist" aria-label="Cari ekstre bölümleri">
    <button
      id="tab-movements"
      role="tab"
      aria-selected={tab === 'movements'}
      aria-controls="panel-movements"
      class:active={tab === 'movements'}
      onclick={() => (tab = 'movements')}>Hareketler</button
    >
    <button
      id="tab-open"
      role="tab"
      aria-selected={tab === 'open'}
      aria-controls="panel-open"
      class:active={tab === 'open'}
      onclick={() => (tab = 'open')}
      >Açık Kalemler ({openItemsError ? '—' : openItems.length})</button
    >
    <button
      id="tab-due"
      role="tab"
      aria-selected={tab === 'due'}
      aria-controls="panel-due"
      class:active={tab === 'due'}
      onclick={() => (tab = 'due')}>Vadeler</button
    >
  </div>

  {#if tab === 'movements'}
    <div id="panel-movements" role="tabpanel" aria-labelledby="tab-movements" tabindex="0">
      <table class="grid-table">
        <caption class="sr-only">Cari hareketleri</caption>
        <thead>
          <tr
            ><th scope="col">Tarih</th><th scope="col">Vade</th><th scope="col">Belge</th><th
              scope="col">Açıklama</th
            ><th scope="col">Para</th><th scope="col" class="right">Borç</th><th
              scope="col"
              class="right">Alacak</th
            ><th scope="col" class="right">Bakiye ({reportCurrency})</th></tr
          >
        </thead>
        <tbody>
          {#each report.items as e (e.id)}
            {@const link = sourceLink(e)}
            {@const rb = describeBalance(e.running_balance ?? '0', reportCurrency)}
            <tr>
              <td>{formatDate(e.document_date)}</td>
              <td>{e.due_date ? formatDate(e.due_date) : '—'}</td>
              <td>{e.document_no ?? '—'}</td>
              <td>
                {#if link}
                  <a href={link}>{e.description}</a>
                {:else}
                  {e.description}
                {/if}
              </td>
              <td>{e.currency}</td>
              <td class="right"
                >{isZeroDecimal(e.debit) ? '—' : formatMoney(e.debit, e.currency)}</td
              >
              <td class="right"
                >{isZeroDecimal(e.credit) ? '—' : formatMoney(e.credit, e.currency)}</td
              >
              <td class="right" class:negative={rb.tone === 'credit'}
                >{e.running_balance ? `${rb.amount}${rb.label ? ' ' + rb.label : ''}` : '—'}</td
              >
            </tr>
          {/each}
          {#if report.items.length === 0}
            <tr><td colspan="8" class="muted">Bu aralıkta hareket yok.</td></tr>
          {/if}
        </tbody>
      </table>
      {#if nextCursor || loadingMore || paginationError}
        <div class="pagination" aria-live="polite">
          {#if paginationError}
            <div class="statement-error" role="alert">
              {paginationError}<Button variant="outline" size="sm" onclick={() => load(true)}
                >Yeniden dene</Button
              >
            </div>
          {:else if nextCursor}
            <Button variant="outline" size="sm" disabled={loadingMore} onclick={() => load(true)}
              >{loadingMore ? 'Hareketler yükleniyor…' : 'Daha fazla hareket'}</Button
            >
          {/if}
        </div>
      {/if}
    </div>
  {:else if tab === 'open'}
    <div id="panel-open" role="tabpanel" aria-labelledby="tab-open" tabindex="0">
      <div class="open-summary" aria-label="Açık kalem toplamları">
        <div>
          <span>Alacak açık toplamı</span><strong
            >{openItemsError || openReceivableTotal === undefined
              ? '—'
              : formatMoney(openReceivableTotal, openBaseCurrency)}</strong
          >
        </div>
        <div>
          <span>Borç açık toplamı</span><strong
            >{openItemsError || openPayableTotal === undefined
              ? '—'
              : formatMoney(openPayableTotal, openBaseCurrency)}</strong
          >
        </div>
        <div>
          <span>Açık neti</span><strong
            >{openItemsError || openNetTotal === undefined
              ? '—'
              : describeBalance(openNetTotal, openBaseCurrency).headline}</strong
          >
        </div>
      </div>
      {#if openTotalsIncomplete}<p class="muted" role="status">
          Bazı açık kalemler ana para birimine çevrilemedi.
        </p>{/if}
      <table class="grid-table">
        <caption class="sr-only">Açık cari kalemleri</caption>
        <thead>
          <tr
            ><th scope="col">Belge</th><th scope="col">Yön</th><th scope="col">Tarih</th><th
              scope="col">Vade</th
            ><th scope="col">Para</th><th scope="col" class="right">Orijinal</th><th
              scope="col"
              class="right">Açık</th
            ><th scope="col" class="right">Açık ({openBaseCurrency})</th></tr
          >
        </thead>
        <tbody>
          {#each openItems as item (item.id)}
            <tr>
              <td>
                {#if item.document_id}
                  <a
                    href={`/${item.side === 'PAYABLE' ? 'alis' : 'satis'}/faturalar/${item.document_id}`}
                    >{item.document_no ?? 'Fatura'}</a
                  >
                {:else}
                  {item.document_no ?? '—'}
                {/if}
              </td>
              <td
                >{item.side === 'RECEIVABLE'
                  ? 'Alacak'
                  : item.side === 'PAYABLE'
                    ? 'Borç'
                    : '—'}</td
              >
              <td>{formatDate(item.document_date)}</td>
              <td>{item.due_date ? formatDate(item.due_date) : '—'}</td>
              <td>{item.currency}</td>
              <td class="right">{formatMoney(item.original_amount, item.currency)}</td>
              <td class="right">{formatMoney(item.open_amount, item.currency)}</td>
              <td class="right"
                >{openAmountInBase(item) === undefined
                  ? '—'
                  : formatMoney(openAmountInBase(item)!, openBaseCurrency)}</td
              >
            </tr>
          {/each}
          {#if openItemsError}
            <tr><td colspan="8" class="muted">Açık kalemler görüntülenemedi.</td></tr>
          {:else if openItems.length === 0}
            <tr><td colspan="8" class="muted">Açık kalem yok.</td></tr>
          {/if}
        </tbody>
        {#if openItems.length}
          <tfoot>
            <tr
              ><td colspan="7" class="right"><strong>Net açık ({openBaseCurrency})</strong></td><td
                class="right"
                ><strong
                  >{openNetTotal === undefined
                    ? '—'
                    : describeBalance(openNetTotal, openBaseCurrency).headline}</strong
                ></td
              ></tr
            >
          </tfoot>
        {/if}
      </table>
    </div>
  {:else}
    <div id="panel-due" role="tabpanel" aria-labelledby="tab-due" tabindex="0">
      <table class="grid-table">
        <caption class="sr-only">Vadesi gelen cari kalemleri</caption>
        <thead>
          <tr
            ><th scope="col">Vade</th><th scope="col">Belge</th><th scope="col">Yön</th><th
              scope="col">Para</th
            ><th scope="col" class="right">Açık</th><th scope="col">Gecikme</th></tr
          >
        </thead>
        <tbody>
          {#each overdue as item (item.id)}
            <tr>
              <td>{item.due_date ? formatDate(item.due_date) : 'Vadesiz'}</td>
              <td>{item.document_no ?? '—'}</td>
              <td
                >{item.side === 'RECEIVABLE'
                  ? 'Alacak'
                  : item.side === 'PAYABLE'
                    ? 'Borç'
                    : '—'}</td
              >
              <td>{item.currency || '—'}</td>
              <td class="right">{formatMoney(item.open_amount, item.currency)}</td>
              <td>{daysLate(item.due_date) || '—'}</td>
            </tr>
          {/each}
          {#if openItemsError}
            <tr><td colspan="6" class="muted">Açık kalemler görüntülenemedi.</td></tr>
          {:else if overdue.length === 0}
            <tr><td colspan="6" class="muted">Açık kalem yok.</td></tr>
          {/if}
        </tbody>
      </table>
    </div>
  {/if}
{/if}

<style>
  .page-header {
    display: flex;
    justify-content: space-between;
    align-items: flex-start;
    gap: 12px;
    margin-bottom: 12px;
  }
  .page-header h1 {
    margin: 4px 0 0;
    font-size: 18px;
  }
  .back {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    color: var(--primary);
    font-size: 11px;
    text-decoration: none;
  }
  .actions {
    display: flex;
    gap: 6px;
  }
  .filters {
    display: flex;
    gap: 10px;
    align-items: flex-end;
    flex-wrap: wrap;
    margin-bottom: 14px;
  }
  /* The shared calendar popover is right-aligned by default and would open
     toward the sidebar for these left-edge filter fields. Anchor it to the
     field's left edge on desktop; the component keeps its own fixed layout
     on narrow screens. */
  @media (min-width: 641px) {
    .filters :global(.calendar-popover) {
      right: auto;
      left: 0;
    }
  }
  .filters label {
    display: flex;
    flex-direction: column;
    gap: 3px;
    font-size: 11px;
    color: var(--text-muted);
    min-width: 150px;
  }
  .summary {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(140px, 1fr));
    gap: 10px;
    margin-bottom: 14px;
  }
  .summary div {
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    padding: 8px 10px;
  }
  .summary span {
    display: block;
    font-size: 11px;
    color: var(--text-muted);
  }
  .summary strong {
    font-size: 14px;
  }
  .summary .closing small {
    display: block;
    margin-top: 2px;
    font-size: 11px;
    color: var(--text-muted);
  }
  .summary .closing.debit {
    background: color-mix(in srgb, var(--success) 8%, var(--surface));
    border-color: color-mix(in srgb, var(--success) 30%, var(--border));
  }
  .summary .closing.credit {
    background: color-mix(in srgb, var(--danger) 8%, var(--surface));
    border-color: color-mix(in srgb, var(--danger) 30%, var(--border));
  }
  .convert-note {
    margin: -6px 0 14px;
    font-size: 11px;
    color: var(--text-muted);
  }
  .tabs {
    display: flex;
    gap: 4px;
    margin-bottom: 10px;
  }
  .tabs button {
    background: none;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    padding: 5px 10px;
    font-size: 12px;
    cursor: pointer;
  }
  .tabs button.active {
    background: var(--surface-muted);
    font-weight: 600;
  }
  .tabs button:focus-visible,
  [role='tabpanel']:focus-visible {
    outline: 2px solid var(--focus);
    outline-offset: 2px;
  }
  .open-summary {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
    gap: 8px;
    margin-bottom: 10px;
  }
  .open-summary div {
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    padding: 8px 10px;
  }
  .open-summary span,
  .open-summary strong {
    display: block;
  }
  .open-summary span {
    color: var(--text-muted);
    font-size: 11px;
  }
  .open-summary strong {
    margin-top: 3px;
    font-size: 13px;
  }
  .statement-error {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    margin-bottom: 10px;
    color: var(--danger);
    font-size: 12px;
  }
  .grid-table {
    width: 100%;
    border-collapse: collapse;
    font-size: 0.85rem;
  }
  .grid-table th,
  .grid-table td {
    padding: 7px 9px;
    border-bottom: 1px solid var(--border);
    text-align: left;
  }
  .grid-table th.right,
  .grid-table td.right {
    text-align: right;
  }
  .grid-table td.negative {
    color: var(--danger);
  }
  .muted {
    color: var(--text-muted);
    text-align: center;
  }
  .grid-table td a {
    color: var(--primary);
    text-decoration: none;
  }
  .grid-table td a:hover {
    text-decoration: underline;
  }
</style>
