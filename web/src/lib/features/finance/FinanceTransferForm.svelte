<script lang="ts">
  import { goto } from '$app/navigation';
  import { ArrowLeft, ArrowRightLeft } from '@lucide/svelte';
  import { toast } from 'svelte-sonner';
  import { api, APIRequestError, type Session } from '$lib/api';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import NegativeBalanceReason from './NegativeBalanceReason.svelte';

  type Account = {
    id: string;
    account_type: 'CASH' | 'BANK';
    code: string;
    name: string;
    currency: string;
    is_active: boolean;
  };

  let session = $state<Session | null>(null);
  let accounts = $state<Account[]>([]);
  let loading = $state(true);
  let saving = $state(false);
  let error = $state('');
  let needsOverride = $state(false);
  // The input snapshot that triggered the negative-balance prompt. Once the
  // user changes the account or amount the earlier confirmation no longer
  // applies, so the override requirement must clear itself.
  let overrideSignature = $state('');
  // Transfers happen "now"; the transaction date is not a user choice, it is
  // pinned to the day the transfer is created.
  let form = $state({
    from_account_id: '',
    to_account_id: '',
    amount: '',
    transaction_date: new Date().toISOString().slice(0, 10),
    description: '',
    external_reference: '',
    override_reason: ''
  });

  const canCreate = $derived(Boolean(session?.permissions.includes('finance.transfer.create')));
  const fromAccount = $derived(accounts.find((a) => a.id === form.from_account_id));
  // Paired ledger effects must share a currency; only same-currency targets are offered.
  const targetAccounts = $derived(
    fromAccount
      ? accounts.filter((a) => a.id !== fromAccount.id && a.currency === fromAccount.currency)
      : []
  );
  const accountLabel = (a: Account) =>
    `${a.account_type === 'BANK' ? 'Banka' : 'Kasa'} · ${a.code} — ${a.name} (${a.currency})`;

  $effect(() => {
    void (async () => {
      try {
        const [sessionResult, accountsResult] = await Promise.all([
          api<Session>('/session'),
          api<{ items?: Account[] }>('/finance/accounts?limit=200').catch(() => ({ items: [] }))
        ]);
        session = sessionResult;
        accounts = (accountsResult.items ?? []).filter((a) => a.is_active);
      } catch (cause) {
        error = cause instanceof Error ? cause.message : 'Transfer formu yüklenemedi.';
      } finally {
        loading = false;
      }
    })();
  });

  $effect(() => {
    if (
      form.from_account_id &&
      form.to_account_id &&
      !targetAccounts.some((a) => a.id === form.to_account_id)
    ) {
      form.to_account_id = '';
    }
  });

  $effect(() => {
    const signature = `${form.from_account_id}|${form.to_account_id}|${form.amount.trim()}`;
    if (needsOverride && signature !== overrideSignature) {
      needsOverride = false;
      form.override_reason = '';
      error = '';
    }
  });

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    if (saving) return;
    error = '';
    if (!form.from_account_id || !form.to_account_id) {
      error = 'Kaynak ve hedef hesap seçin.';
      return;
    }
    if (!/^\d+(\.\d{1,4})?$/.test(form.amount.trim()) || Number(form.amount) <= 0) {
      error = 'Geçerli bir tutar girin.';
      return;
    }
    if (needsOverride && !form.override_reason.trim()) {
      error = 'Negatif bakiye için gerekçe zorunludur.';
      return;
    }
    saving = true;
    try {
      const result = await api<{ id: string }>('/finance/transfers', {
        method: 'POST',
        body: JSON.stringify({
          from_account_id: form.from_account_id,
          to_account_id: form.to_account_id,
          amount: form.amount.trim(),
          transaction_date: `${form.transaction_date}T00:00:00+03:00`,
          description: form.description.trim(),
          external_reference: form.external_reference.trim(),
          override_reason: needsOverride ? form.override_reason.trim() : ''
        })
      });
      toast.success('Transfer kaydedildi.');
      await goto(`/finans/transferler/${encodeURIComponent(result.id)}`);
    } catch (cause) {
      if (
        cause instanceof APIRequestError &&
        cause.code === 'NEGATIVE_BALANCE_CONFIRMATION_REQUIRED'
      ) {
        needsOverride = true;
        overrideSignature = `${form.from_account_id}|${form.to_account_id}|${form.amount.trim()}`;
        error =
          'Kaynak hesap bu çıkış için negatife düşüyor. Yetkiliyseniz gerekçe girip yeniden deneyin.';
      } else if (cause instanceof APIRequestError && cause.code === 'NEGATIVE_BALANCE_BLOCKED') {
        error = 'Kaynak hesap bakiyesi bu transfer için yetersiz.';
      } else {
        error = cause instanceof Error ? cause.message : 'Transfer kaydedilemedi.';
      }
    } finally {
      saving = false;
    }
  }
</script>

<svelte:head><title>Yeni transfer · Varya One</title></svelte:head>

<main class="page-shell">
  <Button variant="ghost" size="sm" onclick={() => goto('/finans/transferler')}>
    <ArrowLeft size={15} />Transferlere dön
  </Button>
  <header>
    <h1>Yeni hesap transferi</h1>
    <p class="subtitle">Kasa ve banka hesapları arasında eşleşik giriş/çıkış hareketi oluşturur.</p>
  </header>

  {#if error}<div class="error" role="alert">{error}</div>{/if}

  {#if loading}
    <p class="muted">Yükleniyor…</p>
  {:else if !canCreate}
    <p class="muted">Transfer oluşturma yetkiniz yok.</p>
  {:else}
    <form class="form-card" onsubmit={submit}>
      <label>
        <span>Kaynak hesap</span>
        <select bind:value={form.from_account_id}>
          <option value="" disabled>Seçin</option>
          {#each accounts as account (account.id)}<option value={account.id}
              >{accountLabel(account)}</option
            >{/each}
        </select>
      </label>
      <label>
        <span>Hedef hesap</span>
        <select bind:value={form.to_account_id} disabled={!form.from_account_id}>
          <option value="" disabled>Seçin</option>
          {#each targetAccounts as account (account.id)}<option value={account.id}
              >{accountLabel(account)}</option
            >{/each}
        </select>
        {#if form.from_account_id && targetAccounts.length === 0}
          <small class="hint">Aynı para birimine sahip başka aktif hesap yok.</small>
        {/if}
      </label>
      <label
        ><span>Tutar {fromAccount ? `(${fromAccount.currency})` : ''}</span><Input
          bind:value={form.amount}
          inputmode="decimal"
          placeholder="0.00"
        /></label
      >
      <label class="wide"
        ><span>Açıklama</span><Input bind:value={form.description} maxlength={200} /></label
      >
      <NegativeBalanceReason bind:reason={form.override_reason} active={needsOverride} />
      <footer>
        <Button type="button" variant="outline" onclick={() => goto('/finans/transferler')}
          >Vazgeç</Button
        >
        <Button type="submit" disabled={saving}>
          <ArrowRightLeft size={15} />{saving ? 'Kaydediliyor…' : 'Transferi oluştur'}
        </Button>
      </footer>
    </form>
  {/if}
</main>

<style>
  .page-shell {
    max-width: 760px;
    margin: 0 auto;
    padding: 24px;
    display: grid;
    gap: 16px;
  }
  .page-shell > :global(button) {
    justify-self: start;
  }
  h1 {
    margin: 0;
    font-size: clamp(1.35rem, 3vw, 1.9rem);
  }
  .subtitle,
  .muted,
  .hint {
    color: var(--text-muted);
  }
  .form-card {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 16px;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius-panel);
    padding: 20px;
  }
  label {
    display: grid;
    gap: 6px;
    font-size: 0.85rem;
    font-weight: 650;
  }
  .wide {
    grid-column: 1 / -1;
  }
  select {
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    padding: 8px 10px;
    background: var(--surface);
    color: var(--text);
    font: inherit;
  }
  footer {
    grid-column: 1 / -1;
    display: flex;
    justify-content: flex-end;
    gap: 10px;
    margin-top: 6px;
  }
  .error {
    padding: 11px 13px;
    border: 1px solid color-mix(in srgb, var(--danger) 35%, var(--border));
    border-radius: var(--radius-control);
    background: color-mix(in srgb, var(--danger) 8%, var(--surface));
    color: var(--danger);
  }
  @media (max-width: 620px) {
    .form-card {
      grid-template-columns: 1fr;
    }
  }
</style>
