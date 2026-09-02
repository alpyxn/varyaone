<script lang="ts">
  import { formatDate, formatMoney } from '$lib/design/formatters';
  import {
    addSignedDecimalStrings,
    multiplyDecimalStrings,
    negateDecimalString
  } from '$lib/design/decimal';
  import { describeBalance } from '$lib/design/balance';
  import { StateBlock } from '$lib/components/varya/status';
  import { getPartyBalances, getPartyOpenItems } from './api';
  import type { PartyBalance, PartyBalanceList, PartyOpenItem } from './types';

  type Props = { partyID: string; canRead: boolean };
  let { partyID, canRead }: Props = $props();

  let balances = $state<PartyBalance[]>([]);
  let balanceSummary = $state<PartyBalanceList>();
  let openItems = $state<PartyOpenItem[]>([]);
  let loadState = $state<'loading' | 'ready' | 'error'>('loading');
  let errorMessage = $state('');
  let openItemsError = $state('');
  let requestSequence = 0;
  let activeRequest: AbortController | undefined;

  const baseCurrency = $derived(
    balanceSummary?.base_currency ||
      balances.find((b) => b.base_currency)?.base_currency ||
      openItems.find((item) => item.base_currency)?.base_currency ||
      balances[0]?.currency ||
      'TRY'
  );

  // Different currencies are never summed as raw numbers: each currency's
  // balance is converted to the company base currency via the rate recorded on
  // every entry, then combined into one figure the user can read.
  const combinedBase = $derived.by(() => {
    if (typeof balanceSummary?.base_balance === 'string') return balanceSummary.base_balance;
    if (balances.length === 0) return '0';
    if (balances.every((row) => typeof row.base_balance === 'string')) {
      return addSignedDecimalStrings(...balances.map((row) => row.base_balance)) ?? undefined;
    }
    if (balances.length === 1 && balances[0].currency === baseCurrency) return balances[0].balance;
    return undefined;
  });
  const combined = $derived(
    combinedBase === undefined ? undefined : describeBalance(combinedBase, baseCurrency)
  );
  const showBreakdown = $derived(
    balances.length > 1 || balances.some((b) => b.currency !== baseCurrency)
  );

  // Open items live in different currencies; each carries its invoice rate
  // snapshot, so convert every open amount to the base currency before summing.
  const openAmountInBase = (item: PartyOpenItem) => {
    if (item.currency === baseCurrency && !item.exchange_rate) return item.open_amount;
    if (item.currency !== baseCurrency && !item.exchange_rate) return undefined;
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
  const nearestDue = $derived(
    [...openItems]
      .filter((item) => item.due_date)
      .sort((a, b) => (a.due_date ?? '').localeCompare(b.due_date ?? ''))[0]?.due_date
  );

  async function load() {
    if (!canRead || !partyID) return;
    activeRequest?.abort();
    const request = new AbortController();
    activeRequest = request;
    const sequence = ++requestSequence;
    loadState = 'loading';
    errorMessage = '';
    openItemsError = '';
    try {
      const [balanceResult, openResult] = await Promise.allSettled([
        getPartyBalances(partyID, request.signal),
        getPartyOpenItems(partyID, new URLSearchParams(), request.signal)
      ]);
      if (request.signal.aborted || sequence !== requestSequence) return;
      if (balanceResult.status === 'rejected') throw balanceResult.reason;
      if (!Array.isArray(balanceResult.value.items)) {
        throw new Error('Cari bakiye yanıtı geçersiz.');
      }
      balanceSummary = balanceResult.value;
      balances = balanceResult.value.items;
      if (openResult.status === 'fulfilled' && Array.isArray(openResult.value.items)) {
        openItems = openResult.value.items;
      } else if (openResult.status === 'fulfilled') {
        openItems = [];
        openItemsError = 'Açık kalem yanıtı geçersiz.';
      } else {
        openItems = [];
        openItemsError =
          openResult.reason instanceof Error
            ? openResult.reason.message
            : 'Açık kalemler alınamadı.';
      }
      loadState = 'ready';
    } catch (cause) {
      if (request.signal.aborted || sequence !== requestSequence) return;
      errorMessage = cause instanceof Error ? cause.message : 'Cari finans özeti alınamadı.';
      loadState = 'error';
    }
  }

  $effect(() => {
    canRead;
    partyID;
    void load();
  });
</script>

{#if canRead}
  <aside class="panel balances">
    <h2>Cari finans özeti</h2>
    {#if loadState === 'loading'}
      <StateBlock loading loadingText="Cari finans özeti yükleniyor…" />
    {:else if loadState === 'error'}
      <StateBlock error={errorMessage} onRetry={load} />
    {:else}
      {#if combined}<div class="headline {combined.tone}">
          <strong>{combined.headline}</strong>
          <span>{combined.meaning}</span>
        </div>{:else}<div class="headline unavailable" role="status">
          <strong>Bakiye kullanılamıyor</strong><span
            >Şirket ana para birimi karşılığı alınamadı.</span
          >
        </div>{/if}

      <dl>
        {#if showBreakdown}
          {#each balances as row (row.currency)}
            {@const d = describeBalance(row.balance, row.currency)}
            <div>
              <dt>{row.currency}</dt>
              <dd class:negative={d.tone === 'credit'}>
                {d.amount}
                <span>{d.label}</span>
              </dd>
            </div>
          {/each}
        {/if}
        {#if balances.length === 0}
          <div>
            <dt>Bakiye</dt>
            <dd>0,00 {baseCurrency === 'TRY' ? '₺' : baseCurrency} — Hesap kapalı.</dd>
          </div>
        {/if}
        <div>
          <dt>
            Açık kalem
            <button
              type="button"
              class="info"
              title="Henüz tahsilat veya ödeme ile kapatılmamış fatura bakiyeleri. Sayı · ana para birimine çevrilmiş toplam."
              aria-label="Açık kalem nedir?">ⓘ</button
            >
          </dt>
          <dd>{openItemsError ? '—' : `${openItems.length} kalem`}</dd>
        </div>
        <div>
          <dt>Alacak açık toplamı</dt>
          <dd>
            {openItemsError || openReceivableTotal === undefined
              ? '—'
              : formatMoney(openReceivableTotal, baseCurrency)}
          </dd>
        </div>
        <div>
          <dt>Borç açık toplamı</dt>
          <dd>
            {openItemsError || openPayableTotal === undefined
              ? '—'
              : formatMoney(openPayableTotal, baseCurrency)}
          </dd>
        </div>
        <div>
          <dt>Açık neti</dt>
          <dd>
            {openItemsError || openNetTotal === undefined
              ? '—'
              : describeBalance(openNetTotal, baseCurrency).headline}
          </dd>
        </div>
        {#if openItemsError}<div class="inline-error" role="alert">
            {openItemsError}<button type="button" onclick={load}>Yeniden dene</button>
          </div>{/if}
        {#if openTotalsIncomplete}<div class="muted" role="status">
            Bazı açık kalemler ana para birimine çevrilemedi.
          </div>{/if}
        {#if nearestDue}
          <div>
            <dt>En yakın vade</dt>
            <dd>{formatDate(nearestDue)}</dd>
          </div>
        {/if}
      </dl>
      <a class="statement-link" href={`/cari/kartlar/${partyID}/ekstre`}>Ekstreyi aç →</a>
    {/if}
  </aside>
{/if}

<style>
  .balances {
    padding: 13px;
  }
  .balances h2 {
    margin: 0 0 10px;
    font-size: 13px;
  }
  .headline {
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    padding: 10px 12px;
    margin-bottom: 10px;
  }
  .headline strong {
    display: block;
    font-size: 16px;
    font-weight: 700;
  }
  .headline span {
    font-size: 11px;
    color: var(--text-muted);
  }
  .headline.debit {
    background: color-mix(in srgb, var(--success) 8%, var(--surface));
    border-color: color-mix(in srgb, var(--success) 30%, var(--border));
  }
  .headline.credit {
    background: color-mix(in srgb, var(--danger) 8%, var(--surface));
    border-color: color-mix(in srgb, var(--danger) 30%, var(--border));
  }
  .headline.unavailable {
    background: var(--surface-muted);
  }
  .info {
    border: 0;
    padding: 0;
    background: none;
    cursor: help;
    color: var(--text-muted);
    font-size: 10px;
    font: inherit;
  }
  .info:focus-visible {
    outline: 2px solid var(--focus);
    outline-offset: 2px;
  }
  .statement-link {
    display: inline-block;
    margin-top: 10px;
    font-size: 11px;
    color: var(--primary);
    text-decoration: none;
  }
  .balances dl {
    margin: 0;
  }
  .balances dl > div {
    display: flex;
    justify-content: space-between;
    gap: 10px;
    padding: 7px 0;
    border-bottom: 1px solid var(--border);
    font-size: 12px;
  }
  .balances dt {
    color: var(--text-muted);
  }
  .balances dd {
    margin: 0;
    font-weight: 700;
    text-align: right;
  }
  .balances dd span {
    display: block;
    font-weight: 400;
    font-size: 10px;
    color: var(--text-muted);
  }
  .negative {
    color: var(--danger);
  }
  .muted {
    color: var(--text-muted);
    font-size: 12px;
  }
  .inline-error {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    color: var(--danger);
    font-size: 11px;
  }
  .inline-error button {
    border: 0;
    background: none;
    color: var(--primary);
    cursor: pointer;
    font: inherit;
    text-decoration: underline;
  }
</style>
