<script lang="ts">
  import { goto } from '$app/navigation';
  import { Button } from '$lib/components/ui/button';
  import { formatDate, formatMoney } from '$lib/design/formatters';
  import { describeBalance } from '$lib/design/balance';
  import { localizedEnum } from '$lib/design/labels';
  import { isZeroDecimal } from '$lib/design/decimal';
  import { StateBlock } from '$lib/components/varya/status';
  import { getPartyStatementReport } from './api';
  import type { PartyLedgerEntry } from './types';

  type Props = { partyID: string; canRead: boolean };
  let { partyID, canRead }: Props = $props();

  let rows = $state<PartyLedgerEntry[]>([]);
  let loadState = $state<'loading' | 'ready' | 'error'>('loading');
  let errorMessage = $state('');
  let reportCurrency = $state('');
  let requestSequence = 0;
  let activeRequest: AbortController | undefined;

  function sourceLink(entry: PartyLedgerEntry): string | undefined {
    return entry.source_href || undefined;
  }

  function label(entry: PartyLedgerEntry): string {
    return entry.source_label || localizedEnum(entry.entry_type, 'entry_type');
  }

  async function load() {
    if (!canRead || !partyID) return;
    activeRequest?.abort();
    const request = new AbortController();
    activeRequest = request;
    const sequence = ++requestSequence;
    loadState = 'loading';
    errorMessage = '';
    try {
      const now = new Date();
      const from = new Date(now);
      from.setFullYear(from.getFullYear() - 1);
      const params = new URLSearchParams({
        from: from.toISOString().slice(0, 10),
        to: now.toISOString().slice(0, 10),
        order: 'desc',
        limit: '8'
      });
      const report = await getPartyStatementReport(partyID, params, request.signal);
      if (request.signal.aborted || sequence !== requestSequence) return;
      // Ask the report endpoint for the newest page directly. Slicing an
      // ascending 500-row page would show stale activity once a party has
      // more than 500 movements.
      rows = report.items.slice(0, 8);
      const currencies = new Set(report.items.map((entry) => entry.currency));
      reportCurrency = report.currency || (currencies.size === 1 ? [...currencies][0] : '');
      loadState = 'ready';
    } catch (cause) {
      if (request.signal.aborted || sequence !== requestSequence) return;
      rows = [];
      errorMessage = cause instanceof Error ? cause.message : 'Son işlemler alınamadı.';
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
  <section class="panel activity">
    <header>
      <h2>Son İşlemler</h2>
      <Button variant="outline" size="sm" onclick={() => goto(`/cari/kartlar/${partyID}/ekstre`)}>
        Tümünü gör
      </Button>
    </header>
    {#if loadState === 'loading'}
      <StateBlock loading loadingText="Son işlemler yükleniyor…" />
    {:else if loadState === 'error'}
      <StateBlock error={errorMessage} onRetry={load} />
    {:else if rows.length === 0}
      <p class="muted">Kayıtlı hareket yok.</p>
    {:else}
      <table class="grid-table">
        <caption class="sr-only">Son cari işlemleri</caption>
        <thead>
          <tr
            ><th scope="col">Tarih</th><th scope="col">Tür</th><th scope="col">Belge</th><th
              scope="col">Açıklama</th
            ><th scope="col">Para</th><th scope="col" class="right">Borç</th><th
              scope="col"
              class="right">Alacak</th
            ><th scope="col" class="right">Bakiye{reportCurrency ? ` (${reportCurrency})` : ''}</th
            ></tr
          >
        </thead>
        <tbody>
          {#each rows as entry (entry.id)}
            <tr>
              <td>{formatDate(entry.document_date)}</td>
              <td>{label(entry)}</td>
              <td>
                {#if sourceLink(entry)}
                  <a href={sourceLink(entry)}>{entry.document_no ?? '—'}</a>
                {:else}
                  {entry.document_no ?? '—'}
                {/if}
              </td>
              <td>{entry.description}</td>
              <td>{entry.currency || '—'}</td>
              <td class="right"
                >{isZeroDecimal(entry.debit) ? '—' : formatMoney(entry.debit, entry.currency)}</td
              >
              <td class="right"
                >{isZeroDecimal(entry.credit) ? '—' : formatMoney(entry.credit, entry.currency)}</td
              >
              <td class="right"
                >{entry.running_balance && reportCurrency
                  ? describeBalance(entry.running_balance, reportCurrency).headline
                  : '—'}</td
              >
            </tr>
          {/each}
        </tbody>
      </table>
    {/if}
  </section>
{/if}

<style>
  .activity {
    margin-top: 12px;
    padding: 13px;
  }
  .activity header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    margin-bottom: 10px;
  }
  .activity h2 {
    margin: 0;
    font-size: 13px;
  }
  .grid-table {
    width: 100%;
    border-collapse: collapse;
    font-size: 0.82rem;
  }
  .grid-table th,
  .grid-table td {
    padding: 6px 8px;
    border-bottom: 1px solid var(--border);
    text-align: left;
  }
  .grid-table th.right,
  .grid-table td.right {
    text-align: right;
  }
  .muted {
    color: var(--text-muted);
    font-size: 12px;
  }
</style>
