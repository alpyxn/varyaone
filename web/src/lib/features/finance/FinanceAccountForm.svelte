<script lang="ts">
  import { goto } from '$app/navigation';
  import { ArrowLeft, Save } from '@lucide/svelte';
  import { api, APIRequestError, type Session } from '$lib/api';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { CurrencySelect } from '$lib/components/varya/currency-select';
  import { parseMoneyInput } from '$lib/design/decimal';

  export type FinanceAccountType = 'CASH' | 'BANK';

  type Props =
    | { mode?: 'create'; accountType?: FinanceAccountType; accountId?: undefined }
    | { mode: 'edit'; accountId: string; accountType?: undefined };
  let { mode = 'create', accountType, accountId }: Props = $props();

  let session = $state<Session | null>(null);
  let loading = $state(true);
  let saving = $state(false);
  let error = $state('');
  let type = $state<FinanceAccountType>(accountType ?? 'CASH');
  let version = $state<number>(0);
  let hasMovements = $state(false);
  let form = $state({
    code: '',
    name: '',
    currency: 'TRY',
    branch_id: '',
    bank_name: '',
    bank_branch_name: '',
    bank_branch_code: '',
    iban: '',
    account_number: '',
    description: '',
    notes: ''
  });
  let openingAmount = $state('');
  // Set once the account itself is created. A failed opening balance must not
  // make the next attempt create a second account, so a retry posts only the
  // movement.
  let createdAccountId = $state('');
  let openingError = $state('');

  const isEdit = $derived(mode === 'edit');
  const typeEndpoint = $derived(`/finance/${type === 'BANK' ? 'bank' : 'cash'}-accounts`);
  const title = $derived(
    isEdit ? 'Hesabı düzenle' : type === 'BANK' ? 'Yeni banka hesabı' : 'Yeni kasa hesabı'
  );
  const permission = $derived(
    isEdit
      ? type === 'BANK'
        ? 'finance.bank_account.edit'
        : 'finance.cash_account.edit'
      : type === 'BANK'
        ? 'finance.bank_account.create'
        : 'finance.cash_account.create'
  );
  const canSubmit = $derived(Boolean(session?.permissions.includes(permission)));

  async function loadSession() {
    try {
      const loaded = await api<Session>('/session');
      session = loaded;
      if (!isEdit) {
        const company = loaded.companies.find((item) => item.id === loaded.current_company_id);
        if (company?.base_currency) form.currency = company.base_currency;
      }
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Oturum bilgisi alınamadı.';
    }
  }

  async function loadAccount() {
    if (!isEdit || !accountId) return;
    try {
      const account = await api<Record<string, unknown>>(
        `/finance/accounts/${encodeURIComponent(accountId)}`
      );
      type = String(account.account_type) === 'BANK' ? 'BANK' : 'CASH';
      version = typeof account.version === 'number' ? account.version : 0;
      form = {
        code: String(account.code ?? ''),
        name: String(account.name ?? ''),
        currency: String(account.currency ?? 'TRY'),
        branch_id: String(account.branch_id ?? ''),
        bank_name: String(account.bank_name ?? ''),
        bank_branch_name: String(account.bank_branch_name ?? ''),
        bank_branch_code: String(account.bank_branch_code ?? ''),
        iban: String(account.iban ?? ''),
        account_number: String(account.account_number ?? ''),
        description: String(account.description ?? ''),
        notes: String(account.notes ?? '')
      };
      const balance = await api<{ balance?: string }>(
        `/finance/${type === 'BANK' ? 'bank' : 'cash'}-accounts/${encodeURIComponent(accountId)}/balance`
      ).catch(() => ({ balance: undefined }));
      // A non-empty statement or opening balance means the branch is locked.
      const movements = await api<{ items?: unknown[] }>(
        `/finance/${type === 'BANK' ? 'bank' : 'cash'}-movements?account_id=${encodeURIComponent(accountId)}&limit=1`
      ).catch(() => ({ items: [] }));
      hasMovements =
        Boolean(movements.items && movements.items.length > 0) || balance.balance !== '0.0000';
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Hesap bilgileri alınamadı.';
    }
  }

  async function save() {
    if (saving) return;
    error = '';
    if (!form.code.trim() || !form.name.trim() || !/^[A-Za-z]{3}$/.test(form.currency.trim())) {
      error = 'Kod, ad ve üç harfli para birimi zorunludur.';
      return;
    }
    // Read in Turkish notation: "1.500,50" is fifteen hundred lira, not 1,5.
    const opening = parseMoneyInput(openingAmount);
    if (!isEdit && opening) {
      if (!/^\d+(\.\d{1,4})?$/.test(opening) || Number(opening) <= 0) {
        error = 'Açılış bakiyesi tutarı geçersiz.';
        return;
      }
    }
    if (!canSubmit) {
      error = 'Bu işlem için yetkiniz yok.';
      return;
    }
    saving = true;
    try {
      const body = JSON.stringify({
        ...form,
        account_type: type,
        currency: form.currency.trim().toUpperCase()
      });
      if (isEdit && accountId) {
        await api(`${typeEndpoint}/${encodeURIComponent(accountId)}`, {
          method: 'PUT',
          headers: { 'If-Match': `"${version}"` },
          body
        });
        await goto(`/finans/hesaplar/${encodeURIComponent(accountId)}`);
      } else {
        const id =
          createdAccountId ||
          (await api<{ id: string }>(typeEndpoint, { method: 'POST', body })).id;
        createdAccountId = id;
        if (opening) {
          try {
            await api(`${typeEndpoint}/${encodeURIComponent(id)}/opening-balance`, {
              method: 'POST',
              body: JSON.stringify({
                account_id: id,
                direction: 'IN',
                amount: opening,
                transaction_date: `${new Date().toISOString().slice(0, 10)}T00:00:00+03:00`,
                description: 'Açılış bakiyesi'
              })
            });
          } catch (cause) {
            // The account exists either way; navigating on would throw the
            // reason away and the balance would look silently ignored.
            openingError = describeOpeningFailure(cause);
            return;
          }
        }
        await goto(`/finans/hesaplar/${encodeURIComponent(id)}`);
      }
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Hesap kaydedilemedi.';
    } finally {
      saving = false;
    }
  }

  /**
   * A foreign-currency account needs a company exchange rate on the movement
   * date before any amount can be posted, so the missing rate is named along
   * with where the user fixes it.
   */
  function describeOpeningFailure(cause: unknown): string {
    const currency = form.currency.trim().toUpperCase();
    if (cause instanceof APIRequestError && cause.code === 'EXCHANGE_RATE_REQUIRED') {
      return `Hesap oluşturuldu, açılış bakiyesi kaydedilemedi: bugün için ${currency} kuru yok. Ayarlar > Döviz Kurları ekranından kuru güncelleyip tekrar deneyin.`;
    }
    const message = cause instanceof Error ? cause.message : 'Açılış bakiyesi kaydedilemedi.';
    return `Hesap oluşturuldu, açılış bakiyesi kaydedilemedi: ${message}`;
  }

  function skipOpening() {
    if (!createdAccountId) return;
    void goto(`/finans/hesaplar/${encodeURIComponent(createdAccountId)}`);
  }

  function back() {
    void goto(
      isEdit && accountId ? `/finans/hesaplar/${encodeURIComponent(accountId)}` : '/finans/hesaplar'
    );
  }

  $effect(() => {
    void (async () => {
      await Promise.all([loadSession(), loadAccount()]);
      loading = false;
    })();
  });
</script>

<svelte:head><title>{title} · Varya One</title></svelte:head>

<main class="page-shell">
  <header class="page-header">
    <Button variant="ghost" size="sm" onclick={back}><ArrowLeft size={15} />Geri dön</Button>
    <div>
      <h1>{title}</h1>
    </div>
  </header>

  {#if error}<div class="error" role="alert">{error}</div>{/if}
  {#if openingError}
    <div class="error" role="alert">
      <p>{openingError}</p>
      <div class="error-actions">
        <Button type="button" variant="outline" size="sm" onclick={skipOpening}>
          Açılış bakiyesiz devam et
        </Button>
        <a class="error-link" href="/ayarlar/doviz-kurlari">Döviz Kurları</a>
      </div>
    </div>
  {/if}

  {#if loading}
    <p class="muted">Hesap formu hazırlanıyor…</p>
  {:else}
    <form
      class="form-card"
      onsubmit={(event) => {
        event.preventDefault();
        void save();
      }}
    >
      <div class="grid">
        {#if !isEdit}
          <fieldset class="wide type-toggle">
            <legend>Hesap türü</legend>
            <label class="radio"
              ><input type="radio" name="account-type" value="CASH" bind:group={type} />Kasa</label
            >
            <label class="radio"
              ><input type="radio" name="account-type" value="BANK" bind:group={type} />Banka</label
            >
          </fieldset>
        {/if}
        <label><span>Kod</span><Input bind:value={form.code} maxlength={40} required /></label>
        <label><span>Ad</span><Input bind:value={form.name} maxlength={120} required /></label>
        <label>
          <span>Para birimi</span>
          <CurrencySelect
            bind:value={form.currency}
            required
            disabled={isEdit}
            ariaLabel="Para birimi"
          />
        </label>
        <label>
          <span>Şube kimliği (opsiyonel)</span>
          <Input bind:value={form.branch_id} placeholder="UUID" disabled={isEdit && hasMovements} />
          {#if isEdit && hasMovements}<small class="hint"
              >Hareket görmüş hesabın şubesi değiştirilemez.</small
            >{/if}
        </label>
        {#if type === 'BANK'}
          <label><span>Banka</span><Input bind:value={form.bank_name} maxlength={120} /></label>
          <label
            ><span>Banka şubesi</span><Input
              bind:value={form.bank_branch_name}
              maxlength={120}
            /></label
          >
          <label
            ><span>Şube kodu</span><Input
              bind:value={form.bank_branch_code}
              maxlength={40}
            /></label
          >
          <label
            ><span>IBAN</span><Input
              bind:value={form.iban}
              maxlength={34}
              autocomplete="off"
            /></label
          >
          <label
            ><span>Hesap numarası</span><Input
              bind:value={form.account_number}
              maxlength={80}
            /></label
          >
        {/if}
        {#if !isEdit}
          <fieldset class="wide opening">
            <legend>Açılış bakiyesi (opsiyonel)</legend>
            <label
              ><span>Tutar</span><Input
                bind:value={openingAmount}
                placeholder="0.00"
                inputmode="decimal"
              /></label
            >
          </fieldset>
        {/if}
        <label class="wide"
          ><span>Açıklama</span><textarea bind:value={form.description} maxlength="500" rows="3"
          ></textarea></label
        >
        <label class="wide"
          ><span>Notlar</span><textarea bind:value={form.notes} maxlength="1000" rows="3"
          ></textarea></label
        >
      </div>
      <footer>
        <Button type="button" variant="outline" onclick={back}>Vazgeç</Button>
        <Button type="submit" disabled={saving || !canSubmit}>
          <Save size={15} />{saving
            ? 'Kaydediliyor…'
            : isEdit
              ? 'Değişiklikleri kaydet'
              : 'Hesabı oluştur'}
        </Button>
      </footer>
    </form>
  {/if}
</main>

<style>
  .page-shell {
    max-width: 980px;
    margin: 0 auto;
    padding: 24px;
  }
  .page-header {
    display: grid;
    gap: 12px;
    margin-bottom: 20px;
  }
  .page-header :global(button) {
    justify-self: start;
  }
  h1 {
    margin: 0;
    font-size: clamp(1.35rem, 3vw, 1.9rem);
  }
  .subtitle,
  .muted {
    color: var(--text-muted);
    margin: 5px 0 0;
  }
  .form-card {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius-panel);
    padding: 20px;
  }
  .grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 16px;
  }
  label {
    display: grid;
    gap: 6px;
    color: var(--text);
    font-size: 0.85rem;
    font-weight: 650;
  }
  .wide {
    grid-column: 1 / -1;
  }
  .type-toggle {
    display: flex;
    gap: 20px;
    align-items: center;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    padding: 10px 14px;
  }
  .type-toggle legend {
    padding: 0 6px;
    font-size: 0.8rem;
    font-weight: 650;
  }
  .radio {
    display: flex;
    align-items: center;
    gap: 7px;
    font-weight: 600;
  }
  .hint {
    color: var(--text-muted);
    font-weight: 500;
  }
  .opening {
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    padding: 12px 14px 14px;
    display: grid;
    gap: 10px;
  }
  .opening legend {
    padding: 0 6px;
    font-size: 0.8rem;
    font-weight: 650;
  }
  .opening .hint {
    margin: 0;
  }
  textarea {
    width: 100%;
    resize: vertical;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    padding: 9px 11px;
    background: var(--surface);
    color: var(--text);
    font: inherit;
  }
  footer {
    display: flex;
    justify-content: flex-end;
    gap: 10px;
    margin-top: 22px;
  }
  .error {
    margin-bottom: 16px;
    padding: 11px 13px;
    border: 1px solid color-mix(in srgb, var(--danger) 35%, var(--border));
    border-radius: var(--radius-control);
    background: color-mix(in srgb, var(--danger) 8%, var(--surface));
    color: var(--danger);
  }
  .error p {
    margin: 0;
  }
  .error-actions {
    display: flex;
    align-items: center;
    gap: 12px;
    margin-top: 10px;
  }
  .error-link {
    color: inherit;
    font-size: 12px;
    text-decoration: underline;
  }
  @media (max-width: 640px) {
    .page-shell {
      padding: 16px;
    }
    .grid {
      grid-template-columns: 1fr;
    }
    .wide {
      grid-column: auto;
    }
    footer {
      flex-direction: column-reverse;
    }
    footer :global(button) {
      width: 100%;
    }
  }
</style>
