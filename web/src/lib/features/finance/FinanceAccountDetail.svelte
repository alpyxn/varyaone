<script lang="ts">
  import { page } from '$app/state';
  import { goto } from '$app/navigation';
  import { Dialog } from 'bits-ui';
  import {
    ArrowLeft,
    ArrowDownLeft,
    ArrowUpRight,
    Pencil,
    Power,
    RefreshCw,
    X
  } from '@lucide/svelte';
  import { toast } from 'svelte-sonner';
  import { api, APIRequestError, type Session } from '$lib/api';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { ConfirmDialog } from '$lib/components/varya/confirm-dialog';
  import NegativeBalanceReason from './NegativeBalanceReason.svelte';
  import { formatDate, formatMoney } from '$lib/design/formatters';
  import { parseMoneyInput } from '$lib/design/decimal';
  import { localizedEnum } from '$lib/design/labels';

  type Account = {
    id: string;
    account_type: 'CASH' | 'BANK';
    code: string;
    name: string;
    currency: string;
    branch_id?: string | null;
    bank_name?: string;
    bank_branch_name?: string;
    bank_branch_code?: string;
    iban?: string;
    account_number?: string;
    description?: string;
    notes?: string;
    is_active: boolean;
    version: number;
    created_at?: string;
  };

  type Movement = {
    id: string;
    movement_kind: string;
    direction: 'IN' | 'OUT';
    amount: string;
    currency: string;
    transaction_date: string;
    description?: string;
    source_label?: string;
    source_href?: string;
  };

  type Tab = 'genel' | 'hareketler' | 'acilis';

  let session = $state<Session | null>(null);
  let account = $state<Account | null>(null);
  let balance = $state<string>('');
  let movements = $state<Movement[]>([]);
  let loading = $state(true);
  let error = $state('');
  let tab = $state<Tab>('genel');
  let confirmActiveOpen = $state(false);
  let openingBusy = $state(false);
  let openingForm = $state({ direction: 'IN' as 'IN' | 'OUT', amount: '', transaction_date: '' });
  let movementOpen = $state(false);
  let movementBusy = $state(false);
  let movementForm = $state({
    direction: 'IN' as 'IN' | 'OUT',
    amount: '',
    description: '',
    override_reason: ''
  });
  let movementNeedsOverride = $state(false);
  let movementOverrideSignature = $state('');

  const accountId = $derived(page.params.id ?? '');
  const typeSlug = $derived(account?.account_type === 'BANK' ? 'bank' : 'cash');
  const kindLabel = $derived(account?.account_type === 'BANK' ? 'Banka hesabı' : 'Kasa hesabı');
  const canEdit = $derived(
    Boolean(
      session?.permissions.includes(
        account?.account_type === 'BANK' ? 'finance.bank_account.edit' : 'finance.cash_account.edit'
      )
    )
  );
  const canDeactivate = $derived(
    Boolean(
      session?.permissions.includes(
        account?.account_type === 'BANK'
          ? 'finance.bank_account.deactivate'
          : 'finance.cash_account.deactivate'
      )
    )
  );
  const canPostMovement = $derived(
    Boolean(
      session?.permissions.includes(
        account?.account_type === 'BANK'
          ? 'finance.bank_movement.post'
          : 'finance.cash_movement.post'
      )
    )
  );
  const hasMovements = $derived(movements.length > 0);
  function errorText(cause: unknown, fallback: string) {
    return cause instanceof Error && cause.message ? cause.message : fallback;
  }

  async function loadSession() {
    try {
      session = await api<Session>('/session');
    } catch (cause) {
      error = errorText(cause, 'Oturum bilgisi alınamadı.');
    }
  }

  async function loadAccount(preserveTab = false) {
    if (!accountId) {
      error = 'Hesap kimliği bulunamadı.';
      loading = false;
      return;
    }
    if (!preserveTab) loading = true;
    error = '';
    try {
      account = await api<Account>(`/finance/accounts/${encodeURIComponent(accountId)}`);
      const slug = account.account_type === 'BANK' ? 'bank' : 'cash';
      const [balanceResult, movementsResult] = await Promise.all([
        api<{ balance?: string }>(
          `/finance/${slug}-accounts/${encodeURIComponent(accountId)}/balance`
        ).catch(() => ({ balance: '' })),
        api<{ items?: Movement[] }>(
          `/finance/${slug}-movements?account_id=${encodeURIComponent(accountId)}&limit=200`
        ).catch(() => ({ items: [] }))
      ]);
      balance = balanceResult.balance ?? '';
      movements = movementsResult.items ?? [];
    } catch (cause) {
      error = errorText(cause, 'Hesap bilgileri alınamadı.');
    } finally {
      loading = false;
    }
  }

  async function toggleActive() {
    if (!account) return;
    const action = account.is_active ? 'deactivate' : 'activate';
    await api(`/finance/${typeSlug}-accounts/${encodeURIComponent(account.id)}/${action}`, {
      method: 'POST',
      headers: { 'If-Match': `"${account.version}"` }
    });
    toast.success(account.is_active ? 'Hesap pasifleştirildi.' : 'Hesap aktifleştirildi.');
    await loadAccount(true);
  }

  async function submitOpeningBalance(event: SubmitEvent) {
    event.preventDefault();
    if (!account || openingBusy) return;
    // Read in Turkish notation: "1.500,50" is fifteen hundred lira, not 1,5.
    const openingAmount = parseMoneyInput(openingForm.amount);
    if (!/^\d+(\.\d{1,4})?$/.test(openingAmount) || !openingForm.transaction_date) {
      toast.error('Geçerli tutar ve tarih girin.');
      return;
    }
    openingBusy = true;
    try {
      await api(`/finance/${typeSlug}-accounts/${encodeURIComponent(account.id)}/opening-balance`, {
        method: 'POST',
        body: JSON.stringify({
          account_id: account.id,
          direction: openingForm.direction,
          amount: openingAmount,
          transaction_date: `${openingForm.transaction_date}T00:00:00+03:00`,
          description: 'Açılış bakiyesi'
        })
      });
      toast.success('Açılış bakiyesi kaydedildi.');
      openingForm = { direction: 'IN', amount: '', transaction_date: '' };
      await loadAccount(true);
      tab = 'hareketler';
    } catch (cause) {
      toast.error(errorText(cause, 'Açılış bakiyesi kaydedilemedi.'));
    } finally {
      openingBusy = false;
    }
  }

  function openMovement(direction: 'IN' | 'OUT') {
    movementForm = { direction, amount: '', description: '', override_reason: '' };
    movementNeedsOverride = false;
    movementOverrideSignature = '';
    movementOpen = true;
  }

  async function submitMovement(event: SubmitEvent) {
    event.preventDefault();
    if (!account || movementBusy) return;
    const amount = parseMoneyInput(movementForm.amount);
    if (!/^\d+(\.\d{1,4})?$/.test(amount) || Number(amount) <= 0) {
      toast.error('Geçerli bir tutar girin.');
      return;
    }
    if (!movementForm.description.trim()) {
      toast.error('Açıklama zorunludur.');
      return;
    }
    // A changed amount invalidates a previously required override.
    if (movementNeedsOverride && amount !== movementOverrideSignature) {
      movementNeedsOverride = false;
      movementForm.override_reason = '';
    }
    if (movementNeedsOverride && !movementForm.override_reason.trim()) {
      toast.error('Negatif bakiye için gerekçe zorunludur.');
      return;
    }
    movementBusy = true;
    try {
      await api(`/finance/${typeSlug}-movements/manual`, {
        method: 'POST',
        body: JSON.stringify({
          account_id: account.id,
          direction: movementForm.direction,
          amount,
          transaction_date: `${new Date().toISOString().slice(0, 10)}T00:00:00+03:00`,
          description: movementForm.description.trim(),
          override_reason: movementNeedsOverride ? movementForm.override_reason.trim() : ''
        })
      });
      toast.success(movementForm.direction === 'IN' ? 'Giriş kaydedildi.' : 'Çıkış kaydedildi.');
      movementOpen = false;
      await loadAccount(true);
      tab = 'hareketler';
    } catch (cause) {
      if (
        cause instanceof APIRequestError &&
        cause.code === 'NEGATIVE_BALANCE_CONFIRMATION_REQUIRED'
      ) {
        movementNeedsOverride = true;
        movementOverrideSignature = amount;
        toast.error('Hesap bu çıkış sonrası negatife düşüyor. Gerekçe girip yeniden kaydedin.');
      } else if (cause instanceof APIRequestError && cause.code === 'NEGATIVE_BALANCE_BLOCKED') {
        toast.error('Hesap bakiyesi bu çıkış için yetersiz.');
      } else {
        toast.error(errorText(cause, 'Hareket kaydedilemedi.'));
      }
    } finally {
      movementBusy = false;
    }
  }

  $effect(() => {
    void loadSession();
    void loadAccount();
  });
</script>

<svelte:head><title>{account ? account.name : 'Finans hesabı'} · Varya One</title></svelte:head>

<main class="page-shell">
  <Button variant="ghost" size="sm" onclick={() => goto('/finans/hesaplar')}>
    <ArrowLeft size={15} />Hesaplara dön
  </Button>

  {#if loading}
    <p class="muted">Hesap yükleniyor…</p>
  {:else if error}
    <div class="error" role="alert">{error}</div>
  {:else if account}
    <header class="detail-header">
      <div>
        <h1>{account.name}</h1>
        <p class="meta">
          {account.currency} · {account.is_active ? 'Aktif' : 'Pasif'}
          {#if account.branch_id}· Şube bağlı{/if}
        </p>
      </div>
      <div class="header-actions">
        <div class="balance">
          <span>Güncel bakiye</span>
          <strong class:negative={balance.startsWith('-')}>
            {balance === '' ? '—' : formatMoney(balance, account.currency)}
          </strong>
        </div>
        <div class="buttons">
          <Button variant="outline" size="sm" onclick={() => loadAccount(true)}>
            <RefreshCw size={14} />Yenile
          </Button>
          {#if canEdit}
            <Button
              variant="outline"
              size="sm"
              onclick={() =>
                account && goto(`/finans/hesaplar/${encodeURIComponent(account.id)}/duzenle`)}
            >
              <Pencil size={14} />Düzenle
            </Button>
          {/if}
          {#if canDeactivate}
            <Button variant="outline" size="sm" onclick={() => (confirmActiveOpen = true)}>
              <Power size={14} />{account.is_active ? 'Pasifleştir' : 'Aktifleştir'}
            </Button>
          {/if}
          {#if canPostMovement && account.is_active}
            <Button variant="outline" size="sm" onclick={() => openMovement('IN')}>
              <ArrowDownLeft size={14} />{account.account_type === 'BANK' ? 'Bankaya' : 'Kasaya'} giriş
              ekle
            </Button>
            <Button variant="outline" size="sm" onclick={() => openMovement('OUT')}>
              <ArrowUpRight size={14} />{account.account_type === 'BANK' ? 'Bankadan' : 'Kasadan'} çıkış
              ekle
            </Button>
          {/if}
        </div>
      </div>
    </header>

    <nav class="tabs">
      <button class:active={tab === 'genel'} onclick={() => (tab = 'genel')}>Genel</button>
      <button class:active={tab === 'hareketler'} onclick={() => (tab = 'hareketler')}>
        Hareketler ({movements.length})
      </button>
      <button class:active={tab === 'acilis'} onclick={() => (tab = 'acilis')}
        >Açılış Bakiyesi</button
      >
    </nav>

    {#if tab === 'genel'}
      <section class="card fields">
        <dl>
          <div>
            <dt>Hesap kodu</dt>
            <dd>{account.code}</dd>
          </div>
          <div>
            <dt>Hesap adı</dt>
            <dd>{account.name}</dd>
          </div>
          <div>
            <dt>Tür</dt>
            <dd>{kindLabel}</dd>
          </div>
          <div>
            <dt>Para birimi</dt>
            <dd>{account.currency}</dd>
          </div>
          {#if account.account_type === 'BANK'}
            <div>
              <dt>Banka</dt>
              <dd>{account.bank_name || '—'}</dd>
            </div>
            <div>
              <dt>Banka şubesi</dt>
              <dd>{account.bank_branch_name || '—'}</dd>
            </div>
            <div>
              <dt>Şube kodu</dt>
              <dd>{account.bank_branch_code || '—'}</dd>
            </div>
            <div>
              <dt>IBAN</dt>
              <dd>{account.iban || '—'}</dd>
            </div>
            <div>
              <dt>Hesap no</dt>
              <dd>{account.account_number || '—'}</dd>
            </div>
          {/if}
          <div>
            <dt>Durum</dt>
            <dd>{account.is_active ? 'Aktif' : 'Pasif'}</dd>
          </div>
          <div>
            <dt>Oluşturma</dt>
            <dd>{formatDate(account.created_at, true)}</dd>
          </div>
          <div class="wide">
            <dt>Açıklama</dt>
            <dd>{account.description || '—'}</dd>
          </div>
          <div class="wide">
            <dt>Notlar</dt>
            <dd>{account.notes || '—'}</dd>
          </div>
        </dl>
      </section>
    {:else if tab === 'hareketler'}
      <section class="card">
        {#if !hasMovements}
          <p class="muted">Bu hesapta henüz hareket yok.</p>
        {:else}
          <table class="grid-table">
            <thead>
              <tr
                ><th>Tarih</th><th>Hareket</th><th>Kaynak</th><th>Yön</th><th>Açıklama</th><th
                  class="right">Tutar</th
                ></tr
              >
            </thead>
            <tbody>
              {#each movements as movement (movement.id)}
                <tr>
                  <td>{formatDate(movement.transaction_date)}</td>
                  <td>{localizedEnum(movement.movement_kind, 'movement_kind')}</td>
                  <td>
                    {#if movement.source_href}<a href={movement.source_href}
                        >{movement.source_label}</a
                      >{:else}{movement.source_label || '—'}{/if}
                  </td>
                  <td>{movement.direction === 'IN' ? 'Giriş' : 'Çıkış'}</td>
                  <td>{movement.description || '—'}</td>
                  <td class="right" class:negative={movement.direction === 'OUT'}>
                    {movement.direction === 'OUT' ? '-' : ''}{formatMoney(
                      movement.amount,
                      movement.currency
                    )}
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        {/if}
      </section>
    {:else}
      <section class="card">
        {#if hasMovements}
          <p class="muted">
            Açılış bakiyesi yalnızca hareketsiz hesaba girilebilir. Bu hesabın hareketleri var.
          </p>
        {:else if !canPostMovement}
          <p class="muted">Açılış bakiyesi girme yetkiniz yok.</p>
        {:else}
          <form class="opening-form" onsubmit={submitOpeningBalance}>
            <p class="muted">
              Açılış bakiyesi değişmez bir harekettir; kaydedildikten sonra düzeltme ancak ters
              kayıtla yapılır.
            </p>
            <div class="opening-grid">
              <label>
                <span>Yön</span>
                <select bind:value={openingForm.direction}>
                  <option value="IN">Giriş (borç bakiyesi)</option>
                  <option value="OUT">Çıkış (alacak bakiyesi)</option>
                </select>
              </label>
              <label
                ><span>Tutar</span><Input
                  bind:value={openingForm.amount}
                  placeholder="0.00"
                  inputmode="decimal"
                /></label
              >
              <label
                ><span>İşlem tarihi</span><Input
                  type="date"
                  bind:value={openingForm.transaction_date}
                /></label
              >
            </div>
            <Button type="submit" disabled={openingBusy}>
              {openingBusy ? 'Kaydediliyor…' : 'Açılış bakiyesini kaydet'}
            </Button>
          </form>
        {/if}
      </section>
    {/if}
  {/if}
</main>

{#if account}
  <Dialog.Root
    bind:open={movementOpen}
    onOpenChange={(next) => !next && !movementBusy && (movementOpen = false)}
  >
    <Dialog.Portal>
      <Dialog.Overlay class="dialog-overlay" />
      <Dialog.Content class="movement-dialog" aria-describedby="movement-dialog-description">
        <div class="dialog-heading">
          <div>
            <Dialog.Title>
              {movementForm.direction === 'IN' ? 'Giriş ekle' : 'Çıkış ekle'} · {account.name}
            </Dialog.Title>
            <Dialog.Description id="movement-dialog-description">
              Manuel {movementForm.direction === 'IN' ? 'giriş' : 'çıkış'} hareketi kaydedilir. Düzeltme
              ancak ters kayıtla yapılır.
            </Dialog.Description>
          </div>
          <Dialog.Close class="close-button" aria-label="Kapat" disabled={movementBusy}>
            <X size={17} />
          </Dialog.Close>
        </div>
        <form class="movement-form" onsubmit={submitMovement}>
          <label>
            <span>Tutar ({account.currency})</span>
            <Input
              bind:value={movementForm.amount}
              placeholder="0.00"
              inputmode="decimal"
              disabled={movementBusy}
            />
          </label>
          <label>
            <span>Açıklama</span>
            <Input
              bind:value={movementForm.description}
              maxlength={500}
              autocomplete="off"
              disabled={movementBusy}
            />
          </label>
          <NegativeBalanceReason
            bind:reason={movementForm.override_reason}
            active={movementNeedsOverride}
          />
          <div class="dialog-actions">
            <Dialog.Close type="button" class="cancel-button" disabled={movementBusy}
              >Vazgeç</Dialog.Close
            >
            <Button type="submit" disabled={movementBusy}>
              {movementBusy ? 'Kaydediliyor…' : 'Kaydet'}
            </Button>
          </div>
        </form>
      </Dialog.Content>
    </Dialog.Portal>
  </Dialog.Root>

  <ConfirmDialog
    bind:open={confirmActiveOpen}
    title={account.is_active ? 'Hesabı pasifleştir' : 'Hesabı aktifleştir'}
    description={account.is_active
      ? 'Pasif hesaba yeni hareket kaydedilemez. Geçmiş hareketler korunur.'
      : 'Hesap yeniden aktifleştirilecek.'}
    confirmLabel={account.is_active ? 'Pasifleştir' : 'Aktifleştir'}
    onConfirm={toggleActive}
  />
{/if}

<style>
  .page-shell {
    max-width: 1080px;
    margin: 0 auto;
    padding: 24px;
    display: grid;
    gap: 18px;
  }
  .page-shell :global(button) {
    justify-self: start;
  }
  h1 {
    margin: 0;
    font-size: clamp(1.35rem, 3vw, 1.9rem);
  }
  .meta,
  .muted {
    color: var(--text-muted);
    margin: 5px 0 0;
  }
  .detail-header {
    display: flex;
    flex-wrap: wrap;
    gap: 18px;
    justify-content: space-between;
    align-items: flex-start;
  }
  .header-actions {
    display: grid;
    gap: 10px;
    justify-items: end;
  }
  .balance {
    display: grid;
    gap: 2px;
    justify-items: end;
  }
  .balance span {
    color: var(--text-muted);
    font-size: 0.78rem;
  }
  .balance strong {
    font-size: 1.25rem;
  }
  .buttons {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
  }
  .buttons :global(button) {
    justify-self: auto;
  }
  .negative {
    color: var(--danger);
  }
  .tabs {
    display: flex;
    gap: 4px;
    border-bottom: 1px solid var(--border);
  }
  .tabs button {
    justify-self: auto;
    background: none;
    border: none;
    border-bottom: 2px solid transparent;
    padding: 9px 14px;
    font: inherit;
    font-weight: 600;
    color: var(--text-muted);
    cursor: pointer;
  }
  .tabs button.active {
    color: var(--text);
    border-bottom-color: var(--primary);
  }
  .card {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius-panel);
    padding: 20px;
  }
  dl {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 14px 24px;
    margin: 0;
  }
  dl .wide {
    grid-column: 1 / -1;
  }
  dt {
    color: var(--text-muted);
    font-size: 0.78rem;
    margin-bottom: 3px;
  }
  dd {
    margin: 0;
    font-weight: 600;
  }
  .grid-table {
    width: 100%;
    border-collapse: collapse;
    font-size: 0.88rem;
  }
  .grid-table th,
  .grid-table td {
    text-align: left;
    padding: 9px 10px;
    border-bottom: 1px solid var(--border);
  }
  .grid-table th.right,
  .grid-table td.right {
    text-align: right;
  }
  .opening-form {
    display: grid;
    gap: 14px;
  }
  .opening-grid {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 14px;
  }
  .opening-form label {
    display: grid;
    gap: 6px;
    font-size: 0.85rem;
    font-weight: 650;
  }
  select {
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    padding: 8px 10px;
    background: var(--surface);
    color: var(--text);
    font: inherit;
  }
  .error {
    padding: 11px 13px;
    border: 1px solid color-mix(in srgb, var(--danger) 35%, var(--border));
    border-radius: var(--radius-control);
    background: color-mix(in srgb, var(--danger) 8%, var(--surface));
    color: var(--danger);
  }
  :global(.movement-dialog) {
    position: fixed;
    z-index: 61;
    top: 50%;
    left: 50%;
    width: min(460px, calc(100vw - 32px));
    transform: translate(-50%, -50%);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-panel);
    background: var(--surface);
    box-shadow: 0 20px 60px rgb(10 30 27 / 22%);
    padding: 18px;
  }
  .dialog-heading {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 12px;
    margin-bottom: 16px;
  }
  .dialog-heading :global(h2) {
    margin: 0;
    font-size: 16px;
  }
  .dialog-heading :global([data-dialog-description]) {
    display: block;
    margin-top: 4px;
    color: var(--text-muted);
    font-size: 12px;
  }
  :global(.movement-dialog .close-button) {
    display: inline-grid;
    place-items: center;
    width: 28px;
    height: 28px;
    border: 0;
    border-radius: var(--radius-control);
    background: transparent;
    color: var(--text-muted);
  }
  :global(.movement-dialog .close-button:hover) {
    background: var(--surface-muted);
    color: var(--text);
  }
  .movement-form {
    display: grid;
    gap: 14px;
  }
  .movement-form label {
    display: grid;
    gap: 6px;
    font-size: 0.85rem;
    font-weight: 650;
  }
  .dialog-actions {
    display: flex;
    justify-content: flex-end;
    gap: 10px;
    margin-top: 4px;
  }
  :global(.movement-dialog .cancel-button) {
    display: inline-flex;
    height: var(--control-height);
    align-items: center;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    background: transparent;
    color: var(--text);
    padding: 0 12px;
    font-size: 12px;
  }
  :global(.movement-dialog .cancel-button:hover) {
    background: var(--surface-muted);
  }
  @media (max-width: 720px) {
    dl,
    .opening-grid {
      grid-template-columns: 1fr;
    }
  }
</style>
