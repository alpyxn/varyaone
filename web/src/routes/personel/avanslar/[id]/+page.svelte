<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/state';
  import { ArrowLeft } from '@lucide/svelte';
  import { api, APIRequestError, type Session } from '$lib/api';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { Badge } from '$lib/components/ui/badge';
  import { DateInput } from '$lib/components/varya/date-input';
  import ReasonDialog from '$lib/components/varya/reason-dialog/ReasonDialog.svelte';
  import { formatDate } from '$lib/design/formatters';
  import { money, type EmployeeAdvance } from '$lib/features/hr/types';
  import {
    advanceActionVisibility,
    advanceStatusLabel,
    advanceTransactionLabel,
    localTodayISO,
    normalizeTRYAmount,
    validTRYAmount
  } from '$lib/features/hr/advance';
  import {
    collectEmployeeAdvance,
    getEmployeeAdvance,
    reverseEmployeeAdvanceTransaction,
    writeOffEmployeeAdvance
  } from '$lib/features/hr/api';
  let item = $state<EmployeeAdvance | null>(null),
    permissions = $state<string[]>([]),
    loading = $state(true),
    saving = $state(false),
    error = $state(''),
    action = $state<'repay' | 'writeoff' | null>(null);
  let repayment = $state({
    amount: '',
    transaction_date: localTodayISO(),
    account_id: '',
    description: ''
  });
  let writeoff = $state({ transaction_date: localTodayISO(), reason: '' });
  const visibility = $derived(advanceActionVisibility(permissions, item?.status ?? ''));
  const reversedIDs = $derived(
    new Set((item?.transactions ?? []).filter((t) => t.reversal_of_id).map((t) => t.reversal_of_id))
  );
  const tone = (status: string) =>
    status === 'OPEN' ? 'warning' : status === 'CLOSED' ? 'success' : 'neutral';
  async function load() {
    loading = true;
    error = '';
    try {
      item = await getEmployeeAdvance(page.params.id ?? '');
      if (item) repayment.account_id = item.account_id;
    } catch (cause) {
      error = cause instanceof APIRequestError ? cause.message : 'Avans yüklenemedi.';
    } finally {
      loading = false;
    }
  }
  async function submitRepayment() {
    const amount = normalizeTRYAmount(repayment.amount);
    if (!item || !amount) return;
    saving = true;
    try {
      item = await collectEmployeeAdvance(item.id, {
        ...repayment,
        amount,
        idempotency_key: crypto.randomUUID()
      });
      action = null;
    } catch (cause) {
      error = cause instanceof APIRequestError ? cause.message : 'Geri ödeme alınamadı.';
    } finally {
      saving = false;
    }
  }
  async function submitWriteoff() {
    if (!item || !writeoff.reason.trim()) return;
    saving = true;
    try {
      item = await writeOffEmployeeAdvance(item.id, {
        ...writeoff,
        idempotency_key: crypto.randomUUID()
      });
      action = null;
    } catch (cause) {
      error =
        cause instanceof APIRequestError ? cause.message : 'Alacaktan vazgeçme kaydedilemedi.';
    } finally {
      saving = false;
    }
  }
  let reverseOpen = $state(false);
  let reverseTargetId = $state<string | null>(null);

  function reverse(id: string) {
    reverseTargetId = id;
    reverseOpen = true;
  }

  async function runReverse(reason: string) {
    if (!reverseTargetId) return;
    saving = true;
    try {
      item = await reverseEmployeeAdvanceTransaction(reverseTargetId, {
        transaction_date: localTodayISO(),
        reason,
        idempotency_key: crypto.randomUUID()
      });
    } catch (cause) {
      error = cause instanceof APIRequestError ? cause.message : 'Ters kayıt oluşturulamadı.';
    } finally {
      saving = false;
    }
  }
  onMount(async () => {
    try {
      permissions = (await api<Session>('/session')).permissions ?? [];
    } catch {
      permissions = [];
    }
    await load();
  });
</script>

<svelte:head><title>Avans Detayı · Varya One</title></svelte:head>

<ReasonDialog
  bind:open={reverseOpen}
  title="Ters kayıt oluştur"
  description="Bu hareket için ters kayıt oluşturulacak. Gerekçe zorunludur."
  label="Ters kayıt gerekçesi"
  confirmLabel="Ters kayıt oluştur"
  onConfirm={runReverse}
/>
{#if loading}<section class="card">Yükleniyor…</section>{:else if !item}<section
    class="card"
    role="alert"
  >
    {error || 'Avans bulunamadı.'}
  </section>{:else}
  <header class="page-header">
    <div>
      <a class="back" href="/personel/avanslar"><ArrowLeft size={13} />Avanslar</a>
      <h1>{item.employee_name}</h1>
      <p>{item.employee_code} · {formatDate(item.advance_date)} · {item.account_name}</p>
    </div>
    <Badge tone={tone(item.status)}>{advanceStatusLabel(item.status)}</Badge>
  </header>
  {#if error}<p class="notice error" role="alert">{error}</p>{/if}
  <section class="balances">
    <div><span>Verilen</span><strong>{money(item.original_amount)} ₺</strong></div>
    <div><span>Tahsil edilen</span><strong>{money(item.repaid_amount)} ₺</strong></div>
    <div><span>Vazgeçilen</span><strong>{money(item.written_off_amount)} ₺</strong></div>
    <div class="outstanding">
      <span>Kalan bakiye</span><strong>{money(item.outstanding_amount)} ₺</strong>
    </div>
  </section>
  <section class="card detail">
    <div><span>Açıklama</span><strong>{item.description}</strong></div>
    <div><span>Referans</span><strong>{item.reference || '—'}</strong></div>
    <div>
      <span>Beklenen geri ödeme</span><strong
        >{item.expected_repayment_date ? formatDate(item.expected_repayment_date) : '—'}</strong
      >
    </div>
  </section>
  {#if item.requires_accounting_tax_review}<p class="warning" role="alert">
      <strong>Muhasebe/vergi değerlendirmesi gerekli.</strong> Sistem vazgeçilen tutar için vergi veya
      SGK hesaplamaz.
    </p>{/if}
  <div class="actions">
    {#if visibility.collect}<Button onclick={() => (action = action === 'repay' ? null : 'repay')}
        >Geri ödeme al</Button
      >{/if}{#if visibility.writeOff}<Button
        variant="outline"
        onclick={() => (action = action === 'writeoff' ? null : 'writeoff')}
        >Alacaktan vazgeç</Button
      >{/if}
  </div>
  {#if action === 'repay'}<section class="card action-card">
      <h2>Geri ödeme al</h2>
      <form
        onsubmit={(e) => {
          e.preventDefault();
          void submitRepayment();
        }}
      >
        <label
          >Tutar<Input
            bind:value={repayment.amount}
            inputmode="decimal"
            placeholder="0,00"
            aria-invalid={repayment.amount !== '' && !validTRYAmount(repayment.amount)}
          /></label
        ><label
          >İşlem tarihi<DateInput
            bind:value={repayment.transaction_date}
            ariaLabel="Geri ödeme işlem tarihi"
            required
          /></label
        ><label
          >Hesap<input
            class="readonly-field"
            type="text"
            value={item.account_name}
            readonly
          /></label
        ><label>Açıklama<Input bind:value={repayment.description} /></label><Button
          type="submit"
          disabled={saving || !validTRYAmount(repayment.amount)}>Tahsilatı kesinleştir</Button
        >
      </form>
    </section>{/if}
  {#if action === 'writeoff'}<section class="card action-card danger">
      <h2>Alacaktan vazgeç</h2>
      <p>
        Bu işlem kalan <strong>{money(item.outstanding_amount)} ₺</strong> tutarın tamamını kapatır ve
        nakit hareketi oluşturmaz. Muhasebe/vergi değerlendirmesi ayrıca yapılmalıdır.
      </p>
      <form
        onsubmit={(e) => {
          e.preventDefault();
          void submitWriteoff();
        }}
      >
        <label
          >İşlem tarihi<DateInput
            bind:value={writeoff.transaction_date}
            ariaLabel="Alacaktan vazgeçme işlem tarihi"
            required
          /></label
        ><label>Zorunlu gerekçe<Input bind:value={writeoff.reason} /></label><Button
          type="submit"
          disabled={saving || !writeoff.reason.trim()}>Tamamından vazgeç</Button
        >
      </form>
    </section>{/if}
  <section class="card">
    <h2>Hareket geçmişi</h2>
    <div class="timeline">
      {#each item.transactions ?? [] as transaction}<article>
          <div class="dot"></div>
          <div>
            <header>
              <strong>{advanceTransactionLabel(transaction.type)}</strong><span
                >{formatDate(transaction.transaction_date)} · {money(transaction.amount)} ₺</span
              >
            </header>
            <p>{transaction.description || transaction.reason || '—'}</p>
            <div class="tx-foot">
              <small>{formatDate(transaction.created_at, true)}</small>
              {#if visibility.reverse && transaction.type !== 'REVERSAL' && !reversedIDs.has(transaction.id)}<Button
                  variant="outline"
                  size="sm"
                  onclick={() => reverse(transaction.id)}
                  disabled={saving}>Ters kayıt</Button
                >{/if}
            </div>
          </div>
        </article>{/each}
    </div>
  </section>
{/if}

<style>
  .back {
    display: flex;
    align-items: center;
    gap: 4px;
    color: var(--text-muted);
    font-size: 13px;
  }
  .balances {
    display: grid;
    grid-template-columns: repeat(4, 1fr);
    gap: 10px;
    margin: 14px 0;
  }
  .balances div,
  .card {
    padding: 16px;
  }
  .balances div {
    border: 1px solid var(--border);
    border-radius: var(--radius-card);
    background: var(--surface);
    display: grid;
    gap: 6px;
  }
  .balances span,
  .detail span {
    color: var(--text-muted);
    font-size: 12px;
  }
  .balances strong {
    font-size: 19px;
  }
  .balances .outstanding {
    border-color: var(--warning);
  }
  .detail {
    display: flex;
    gap: 30px;
    flex-wrap: wrap;
  }
  .detail div {
    display: grid;
    gap: 4px;
  }
  .warning {
    padding: 14px;
    border: 1px solid var(--warning);
    border-radius: var(--radius-card);
    background: var(--warning-soft);
    font-size: 13px;
  }
  .actions {
    display: flex;
    gap: 8px;
    margin: 14px 0;
  }
  .action-card {
    margin-bottom: 14px;
  }
  .action-card h2,
  .card h2 {
    margin-top: 0;
  }
  .action-card form {
    display: flex;
    align-items: end;
    gap: 10px;
    flex-wrap: wrap;
  }
  .action-card label {
    display: grid;
    gap: 5px;
    font-size: 12px;
  }
  .readonly-field {
    height: var(--control-height, 34px);
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    background: var(--surface-muted, var(--surface));
    color: var(--text-muted);
    padding: 0 9px;
    font-size: 13px;
  }
  .danger {
    border-color: var(--danger);
  }
  .timeline {
    display: grid;
  }
  .timeline article {
    display: grid;
    grid-template-columns: 14px 1fr;
    gap: 10px;
    padding: 12px 0;
    border-bottom: 1px solid var(--border);
  }
  .dot {
    width: 9px;
    height: 9px;
    border-radius: 50%;
    background: var(--primary);
    margin-top: 5px;
  }
  .timeline header {
    display: flex;
    justify-content: space-between;
    gap: 12px;
  }
  .timeline p {
    margin: 5px 0;
    color: var(--text-muted);
  }
  .timeline small {
    color: var(--text-muted);
  }
  .tx-foot {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    margin-top: 8px;
  }
  @media (max-width: 800px) {
    .balances {
      grid-template-columns: 1fr 1fr;
    }
  }
  @media (max-width: 500px) {
    .balances {
      grid-template-columns: 1fr;
    }
  }
</style>
