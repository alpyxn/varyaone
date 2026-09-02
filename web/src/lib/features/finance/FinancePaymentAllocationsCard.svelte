<script lang="ts">
  import { page } from '$app/state';
  import { toast } from 'svelte-sonner';
  import { api, type Session } from '$lib/api';
  import { Button } from '$lib/components/ui/button';
  import { ConfirmDialog } from '$lib/components/varya/confirm-dialog';
  import { VaryaSheet } from '$lib/components/varya/sheet';
  import { formatDate, formatMoney } from '$lib/design/formatters';
  import { canonicalDecimal, decimalNumber } from '$lib/design/decimal';
  import AllocationTable from './AllocationTable.svelte';
  import { allocatePosted, loadOpenItems } from './allocation-api';
  import type { AllocationRow } from './allocation-calc';

  type Props = { kind: 'collection' | 'payment' };
  let { kind }: Props = $props();

  type Allocation = {
    id: string;
    amount: string;
    currency: string;
    open_item_id?: string;
    document_id?: string;
    due_date?: string;
    original_amount?: string;
    remaining_amount?: string;
    reversal_of_id?: string | null;
    allocated_at?: string;
  };
  type PaymentDetail = {
    id: string;
    party_id?: string;
    amount?: string;
    status?: string;
    reversal_of_id?: string | null;
    currency: string;
    unapplied_amount?: string;
    allocations?: Allocation[];
  };

  const endpointBase = $derived(
    kind === 'collection' ? '/finance/collections' : '/finance/payments'
  );
  let detail = $state<PaymentDetail | null>(null);
  let canManage = $state(false);
  let loading = $state(true);
  let busyID = $state('');
  let confirmOpen = $state(false);
  let target = $state<Allocation | null>(null);

  // Reversal rows and their originals net to zero; show only live allocations.
  const activeAllocations = $derived(
    (detail?.allocations ?? []).filter((a) => {
      if (a.reversal_of_id) return false;
      return !(detail?.allocations ?? []).some((r) => r.reversal_of_id === a.id);
    })
  );
  const locked = $derived(detail?.status === 'REVERSED' || Boolean(detail?.reversal_of_id));
  const unapplied = $derived(Number(detail?.unapplied_amount ?? '0'));
  let distributing = $state(false);

  let drawerOpen = $state(false);
  let drawerRows = $state<AllocationRow[]>([]);
  let drawerLoading = $state(false);
  let drawerSubmitting = $state(false);

  async function openDistributeDrawer() {
    if (!detail?.party_id) return;
    drawerOpen = true;
    drawerLoading = true;
    drawerRows = [];
    try {
      const items = await loadOpenItems({
        partyID: detail.party_id,
        currency: detail.currency,
        kind
      });
      drawerRows = items.map((item) => ({ ...item, applied: '0' }));
    } catch (cause) {
      toast.error(cause instanceof Error ? cause.message : 'Açık faturalar okunamadı.');
    } finally {
      drawerLoading = false;
    }
  }

  async function submitDistribute() {
    const id = page.params.id;
    if (!id || drawerSubmitting) return;
    const allocations = drawerRows
      .filter((row) => decimalNumber(row.applied) > 0)
      .map((row) => ({ open_item_id: row.id, amount: canonicalDecimal(row.applied) }));
    if (allocations.length === 0) {
      toast.error('En az bir faturaya tutar girin.');
      return;
    }
    drawerSubmitting = true;
    try {
      await allocatePosted(kind, id, allocations);
      toast.success('Faturalara dağıtıldı.');
      drawerOpen = false;
      await load();
    } catch (cause) {
      toast.error(cause instanceof Error ? cause.message : 'Dağıtım yapılamadı.');
    } finally {
      drawerSubmitting = false;
    }
  }

  async function autoDistribute() {
    const id = page.params.id;
    if (!id || distributing) return;
    distributing = true;
    try {
      await api(`${endpointBase}/${encodeURIComponent(id)}/allocations/auto`, {
        method: 'POST',
        headers: { 'Idempotency-Key': crypto.randomUUID() },
        body: JSON.stringify({})
      });
      toast.success('Avans en eski borçlara dağıtıldı.');
      await load();
    } catch (cause) {
      toast.error(cause instanceof Error ? cause.message : 'Otomatik dağıtım yapılamadı.');
    } finally {
      distributing = false;
    }
  }

  async function load() {
    const id = page.params.id;
    if (!id) return;
    try {
      const [sessionResult, detailResult] = await Promise.all([
        api<Session>('/session'),
        api<PaymentDetail>(`${endpointBase}/${encodeURIComponent(id)}`)
      ]);
      canManage = sessionResult.permissions.includes('finance.allocation.manage');
      detail = detailResult;
    } catch {
      detail = null;
    } finally {
      loading = false;
    }
  }

  async function unallocate() {
    const id = page.params.id;
    if (!id || !target) return;
    busyID = target.id;
    try {
      await api(`${endpointBase}/${encodeURIComponent(id)}/allocations/reverse`, {
        method: 'POST',
        body: JSON.stringify({ allocation_ids: [target.id] })
      });
      toast.success('Tahsis geri alındı.');
      await load();
    } catch (cause) {
      toast.error(cause instanceof Error ? cause.message : 'Tahsis geri alınamadı.');
    } finally {
      busyID = '';
      target = null;
    }
  }

  $effect(() => {
    void load();
  });
</script>

{#if !loading && detail && canManage}
  <section class="card">
    <header>
      <h2>Tahsis yönetimi</h2>
      {#if detail.unapplied_amount}
        <span class="unapplied"
          >Uygulanmamış: {formatMoney(detail.unapplied_amount, detail.currency)}</span
        >
      {/if}
      {#if !locked && unapplied > 0}
        <div class="header-actions">
          <Button variant="outline" size="sm" disabled={distributing} onclick={autoDistribute}>
            {distributing ? 'Dağıtılıyor…' : 'En eski borçlara dağıt'}
          </Button>
          <Button variant="outline" size="sm" onclick={openDistributeDrawer}>Faturaya dağıt</Button>
        </div>
      {/if}
    </header>

    {#if locked}
      <p class="muted">Ters kaydedilmiş işlemin tahsisleri değiştirilemez.</p>
    {:else if activeAllocations.length === 0}
      <p class="muted">Bu işlemde aktif tahsis yok. Uygulanmamış tutar cari avans olarak durur.</p>
    {:else}
      <table class="grid-table">
        <thead>
          <tr
            ><th>Tarih</th><th>Vade</th><th class="right">Fatura tutarı</th><th class="right"
              >Uygulanan</th
            ><th class="right">Kalan</th><th></th></tr
          >
        </thead>
        <tbody>
          {#each activeAllocations as allocation (allocation.id)}
            <tr>
              <td>{formatDate(allocation.allocated_at)}</td>
              <td>{allocation.due_date ? formatDate(allocation.due_date) : '—'}</td>
              <td class="right"
                >{allocation.original_amount
                  ? formatMoney(allocation.original_amount, allocation.currency)
                  : '—'}</td
              >
              <td class="right">{formatMoney(allocation.amount, allocation.currency)}</td>
              <td class="right"
                >{allocation.remaining_amount
                  ? formatMoney(allocation.remaining_amount, allocation.currency)
                  : '—'}</td
              >
              <td class="right">
                <Button
                  variant="outline"
                  size="sm"
                  disabled={busyID === allocation.id}
                  onclick={() => {
                    target = allocation;
                    confirmOpen = true;
                  }}
                >
                  Geri al
                </Button>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
      <p class="hint">
        Yeniden dağıtım için önce tahsisi geri alın, sonra yeni tahsilat/ödeme akışından dağıtın.
      </p>
    {/if}
  </section>

  <ConfirmDialog
    bind:open={confirmOpen}
    title="Tahsisi geri al"
    description="Bu tahsis geri alınacak; bağlı fatura yeniden açık kalem olur. İşlem değişmez bir ters kayıt satırıyla yapılır."
    confirmLabel="Geri al"
    onConfirm={unallocate}
  />

  <VaryaSheet
    bind:open={drawerOpen}
    title="Faturaya dağıt"
    description="Uygulanmamış tutarı seçtiğiniz açık faturalara dağıtın."
  >
    {#if drawerLoading}
      <p class="muted">Açık faturalar yükleniyor…</p>
    {:else}
      <AllocationTable
        bind:rows={drawerRows}
        currency={detail.currency}
        amount={detail.unapplied_amount ?? '0'}
        side={kind === 'payment' ? 'purchase' : 'sales'}
      />
      <div class="drawer-actions">
        <Button variant="outline" size="sm" onclick={() => (drawerOpen = false)}>Vazgeç</Button>
        <Button size="sm" disabled={drawerSubmitting} onclick={submitDistribute}>
          {drawerSubmitting ? 'Kaydediliyor…' : 'Dağıt'}
        </Button>
      </div>
    {/if}
  </VaryaSheet>
{/if}

<style>
  .card {
    max-width: 1040px;
    margin: 16px auto 0;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius-panel);
    padding: 20px;
  }
  header {
    display: flex;
    justify-content: space-between;
    align-items: baseline;
    gap: 12px;
    flex-wrap: wrap;
    margin-bottom: 12px;
  }
  h2 {
    margin: 0;
    font-size: 1rem;
  }
  .unapplied {
    color: var(--text-muted);
    font-size: 0.85rem;
    font-weight: 600;
  }
  .muted,
  .hint {
    color: var(--text-muted);
  }
  .hint {
    font-size: 0.8rem;
    margin: 10px 0 0;
  }
  .grid-table {
    width: 100%;
    border-collapse: collapse;
    font-size: 0.86rem;
  }
  .grid-table th,
  .grid-table td {
    padding: 8px 10px;
    border-bottom: 1px solid var(--border);
    text-align: left;
  }
  .grid-table th.right,
  .grid-table td.right {
    text-align: right;
  }
  .header-actions {
    display: flex;
    gap: 6px;
    flex-wrap: wrap;
  }
  .drawer-actions {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
    margin-top: 16px;
  }
</style>
