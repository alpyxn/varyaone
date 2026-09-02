<script lang="ts">
  import { formatDate, formatMoney } from '$lib/design/formatters';
  import {
    allocatedTotal,
    daysOverdue,
    isOverApplied,
    previewFifo,
    unappliedAmount,
    type AllocationRow
  } from './allocation-calc';

  type Props = {
    rows: AllocationRow[];
    currency: string;
    amount: string;
    readonly?: boolean;
    /** 'sales' → link to /satis/faturalar, 'purchase' → /alis/faturalar */
    side?: 'sales' | 'purchase';
    onChange?: (rows: AllocationRow[]) => void;
  };
  let {
    rows = $bindable(),
    currency,
    amount,
    readonly = false,
    side = 'sales',
    onChange
  }: Props = $props();

  const invoiceBase = $derived(side === 'purchase' ? '/alis/faturalar' : '/satis/faturalar');

  const total = $derived(allocatedTotal(rows));
  const advance = $derived(unappliedAmount(amount, rows));
  const over = $derived(isOverApplied(amount, rows));

  function fillFifo() {
    const preview = previewFifo(rows, amount);
    rows = rows.map((row) => ({ ...row, applied: preview[row.id] ?? '0' }));
    onChange?.(rows);
  }
  function clearAll() {
    rows = rows.map((row) => ({ ...row, applied: '0' }));
    onChange?.(rows);
  }
</script>

<div class="allocation-table">
  <div class="toolbar">
    <button type="button" onclick={fillFifo} disabled={readonly || !rows.length}>
      En eskiden doldur
    </button>
    <button type="button" onclick={clearAll} disabled={readonly || !rows.length}>Temizle</button>
  </div>

  {#if rows.length === 0}
    <p class="muted">Bu cari ve para biriminde açık fatura yok.</p>
  {:else}
    <table class="grid">
      <thead>
        <tr>
          <th>Belge</th><th>Tarih</th><th>Vade</th><th class="right">Açık</th><th class="right"
            >Uygulanacak</th
          >
        </tr>
      </thead>
      <tbody>
        {#each rows as row (row.id)}
          {@const late = daysOverdue(row.due_date)}
          <tr>
            <td>
              {#if row.document_id}
                <a
                  href={`${invoiceBase}/${row.document_id}`}
                  target="_blank"
                  rel="noopener"
                  title="Faturayı yeni sekmede aç">{row.document_no ?? 'Fatura'}</a
                >
              {:else}
                {row.document_no ?? 'Fatura'}
              {/if}
            </td>
            <td>{row.document_date ? formatDate(row.document_date) : '—'}</td>
            <td class:late={late > 0}>
              {row.due_date ? formatDate(row.due_date) : '—'}
              {#if late > 0}<span>({late}g)</span>{/if}
            </td>
            <td class="right">{formatMoney(row.open_amount, currency)}</td>
            <td class="right">
              <input
                bind:value={row.applied}
                oninput={() => onChange?.(rows)}
                inputmode="decimal"
                disabled={readonly}
                aria-label={`${row.document_no ?? 'Fatura'} uygulanacak tutar`}
              />
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}

  <dl class="summary" class:over>
    <div>
      <dt>İşlem tutarı</dt>
      <dd>{formatMoney(amount || '0', currency)}</dd>
    </div>
    <div>
      <dt>Dağıtılan</dt>
      <dd>{formatMoney(total, currency)}</dd>
    </div>
    <div>
      <dt>{over ? 'Aşım' : 'Avans kalan'}</dt>
      <dd>{formatMoney(advance, currency)}</dd>
    </div>
  </dl>
  {#if over}<p class="error">Dağıtılan tutar işlem tutarını aşıyor.</p>{/if}
</div>

<style>
  .toolbar {
    display: flex;
    gap: 6px;
    margin-bottom: 8px;
  }
  .toolbar button {
    background: var(--surface-muted);
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    padding: 4px 8px;
    font-size: 11px;
    cursor: pointer;
  }
  .grid {
    width: 100%;
    border-collapse: collapse;
    font-size: 0.8rem;
  }
  .grid th,
  .grid td {
    padding: 5px 6px;
    border-bottom: 1px solid var(--border);
    text-align: left;
  }
  .grid th.right,
  .grid td.right {
    text-align: right;
  }
  .grid td a {
    color: var(--primary);
    font-weight: 600;
    text-decoration: none;
  }
  .grid td a:hover {
    text-decoration: underline;
  }
  .grid input {
    width: 92px;
    text-align: right;
  }
  .late {
    color: var(--danger);
  }
  .late span {
    font-size: 10px;
  }
  .summary {
    display: flex;
    justify-content: space-between;
    gap: 10px;
    margin: 10px 0 0;
    font-size: 12px;
  }
  .summary dt {
    color: var(--text-muted);
  }
  .summary dd {
    margin: 2px 0 0;
    font-weight: 700;
  }
  .summary.over dd {
    color: var(--danger);
  }
  .muted {
    color: var(--text-muted);
    font-size: 12px;
  }
  .error {
    color: var(--danger);
    font-size: 11px;
    margin: 6px 0 0;
  }
</style>
