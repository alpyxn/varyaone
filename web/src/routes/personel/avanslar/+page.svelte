<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { ChevronRight, Plus, RefreshCw, Wallet, X } from '@lucide/svelte';
  import { api, APIRequestError, type Session } from '$lib/api';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { Badge } from '$lib/components/ui/badge';
  import { ConfirmDialog } from '$lib/components/varya/confirm-dialog';
  import { DateInput } from '$lib/components/varya/date-input';
  import { type EntityOption } from '$lib/components/varya/entity-picker-dialog';
  import { EntityCombobox } from '$lib/components/varya/entity-combobox';
  import { formatDate } from '$lib/design/formatters';
  import { money, type EmployeeAdvance } from '$lib/features/hr/types';
  import { advanceStatusLabel, normalizeTRYAmount, validTRYAmount } from '$lib/features/hr/advance';
  import { createEmployeeAdvance, listEmployeeAdvances, listEmployees } from '$lib/features/hr/api';

  type Account = {
    id: string;
    code: string;
    name: string;
    account_type: string;
    currency: string;
    is_active: boolean;
  };
  let permissions = $state<string[]>([]),
    denied = $state(false),
    loading = $state(true),
    saving = $state(false);
  let rows = $state<EmployeeAdvance[]>([]),
    total = $state('0.00'),
    error = $state(''),
    showCreate = $state(false);
  type EmployeeOption = EntityOption & { employeeID: string };
  let selectedEmployee = $state<EmployeeOption | null>(null),
    accounts = $state<Account[]>([]);
  let negativeBalanceOpen = $state(false),
    negativeBalanceReason = $state(''),
    overrideSignature = $state(''),
    commandKey = $state('');
  let filters = $state({ q: '', status: '', balance: '', from: '', to: '' });
  let form = $state({
    employee_id: '',
    amount: '',
    account_id: '',
    description: '',
    reference: '',
    expected_repayment_date: ''
  });
  const canPost = $derived(permissions.includes('hr.employee_advance.post'));
  const amountError = $derived(form.amount !== '' && !validTRYAmount(form.amount));
  const tone = (status: string) =>
    status === 'OPEN' ? 'warning' : status === 'CLOSED' ? 'success' : 'neutral';
  const formSignature = () =>
    [
      form.employee_id,
      form.amount.trim(),
      form.account_id,
      form.description.trim(),
      form.reference.trim(),
      form.expected_repayment_date
    ].join('|');

  $effect(() => {
    const signature = formSignature();
    if (negativeBalanceOpen && signature !== overrideSignature) {
      negativeBalanceOpen = false;
      negativeBalanceReason = '';
      commandKey = '';
    }
  });

  async function load() {
    if (denied) return;
    loading = true;
    error = '';
    try {
      const page = await listEmployeeAdvances(filters);
      rows = page.items;
      total = page.total_outstanding;
    } catch (cause) {
      error = cause instanceof APIRequestError ? cause.message : 'Avanslar yüklenemedi.';
    } finally {
      loading = false;
    }
  }
  async function openCreate() {
    form = {
      employee_id: '',
      amount: '',
      account_id: '',
      description: '',
      reference: '',
      expected_repayment_date: ''
    };
    selectedEmployee = null;
    negativeBalanceOpen = false;
    negativeBalanceReason = '';
    commandKey = '';
    showCreate = true;
    try {
      const accountPage = await api<{ items: Account[] }>('/finance/accounts?limit=200');
      accounts = (accountPage.items ?? []).filter((a) => a.currency === 'TRY' && a.is_active);
    } catch {
      error = 'Erişilebilir TRY hesapları yüklenemedi.';
    }
  }
  async function searchEmployees(query: string): Promise<EmployeeOption[]> {
    const page = await listEmployees({ q: query.trim() || undefined, status: 'ACTIVE' });
    return page.items.map((employee) => ({
      id: employee.id,
      employeeID: employee.id,
      title: `${employee.first_name} ${employee.last_name}`,
      subtitle: employee.employee_code,
      meta: employee.position_title || undefined
    }));
  }
  async function save(overrideReason = '', surfaceError = false) {
    const amount = normalizeTRYAmount(form.amount);
    if (!amount || !form.employee_id || !form.account_id || !form.description.trim()) return;
    saving = true;
    error = '';
    commandKey ||= crypto.randomUUID();
    try {
      const body: Record<string, unknown> = {
        ...form,
        amount,
        idempotency_key: commandKey
      };
      if (!form.expected_repayment_date) delete body.expected_repayment_date;
      if (overrideReason.trim()) body.override_reason = overrideReason.trim();
      const created = await createEmployeeAdvance(body);
      showCreate = false;
      negativeBalanceOpen = false;
      await goto(`/personel/avanslar/${created.id}`);
    } catch (cause) {
      if (
        !overrideReason &&
        cause instanceof APIRequestError &&
        cause.code === 'NEGATIVE_BALANCE_CONFIRMATION_REQUIRED'
      ) {
        overrideSignature = formSignature();
        negativeBalanceOpen = true;
        return;
      }
      if (surfaceError) throw cause;
      error = cause instanceof APIRequestError ? cause.message : 'Avans kaydedilemedi.';
    } finally {
      saving = false;
    }
  }
  onMount(async () => {
    try {
      permissions = (await api<Session>('/session')).permissions ?? [];
      denied = !permissions.includes('hr.employee_advance.read');
    } catch {
      denied = true;
    }
    await load();
  });
</script>

<svelte:head><title>Personel Avansları · Varya One</title></svelte:head>
{#if denied}<section class="card" role="alert">
    Personel avanslarını görüntüleme yetkiniz yok.
  </section>
{:else}
  <header class="page-header">
    <div>
      <h1>Personel Avansları</h1>
    </div>
    <div class="actions">
      <Button variant="outline" onclick={load}><RefreshCw size={14} />Yenile</Button
      >{#if canPost}<Button onclick={openCreate}><Plus size={14} />Avans ver</Button>{/if}
    </div>
  </header>
  {#if error}<p class="notice error" role="alert">{error}</p>{/if}

  <section class="summary">
    <div class="summary-icon"><Wallet size={20} aria-hidden="true" /></div>
    <div class="summary-copy">
      <span>Toplam açık avans</span>
      <strong>{money(total)} ₺</strong>
    </div>
    <div class="summary-count">
      <span>Kayıt</span>
      <strong>{rows.length}</strong>
    </div>
  </section>

  <section class="card list-card">
    <form
      class="filters"
      onsubmit={(e) => {
        e.preventDefault();
        void load();
      }}
    >
      <Input bind:value={filters.q} placeholder="Çalışan adı veya kodu" aria-label="Çalışan ara" />
      <select bind:value={filters.status} aria-label="Durum"
        ><option value="">Tüm durumlar</option><option value="OPEN">Açık</option><option
          value="CLOSED">Kapalı</option
        ><option value="WRITTEN_OFF">Vazgeçildi</option><option value="REVERSED">Ters kayıt</option
        ></select
      >
      <select bind:value={filters.balance} aria-label="Bakiye"
        ><option value="">Tüm bakiyeler</option><option value="OPEN">Açık bakiye</option><option
          value="CLOSED">Kapalı bakiye</option
        ></select
      >
      <DateInput bind:value={filters.from} ariaLabel="Başlangıç tarihi" /><DateInput
        bind:value={filters.to}
        ariaLabel="Bitiş tarihi"
      /><Button type="submit" variant="outline">Filtrele</Button>
    </form>
    {#if loading}<p class="state">Yükleniyor…</p>{:else if !rows.length}<p class="state">
        Kayıt bulunamadı.
      </p>{:else}<div class="scroll">
        <table>
          <thead
            ><tr
              ><th>Çalışan</th><th>Tarih</th><th>Durum</th><th class="num">Verilen</th><th
                class="num">Kalan</th
              ><th aria-label="Detay"></th></tr
            ></thead
          ><tbody
            >{#each rows as row}<tr onclick={() => goto(`/personel/avanslar/${row.id}`)}
                ><td><strong>{row.employee_name}</strong><small>{row.employee_code}</small></td><td
                  class="muted">{formatDate(row.advance_date)}</td
                ><td><Badge tone={tone(row.status)}>{advanceStatusLabel(row.status)}</Badge></td><td
                  class="num">{money(row.original_amount)} ₺</td
                ><td class="num"><strong>{money(row.outstanding_amount)} ₺</strong></td><td
                  class="go"
                  ><a href={`/personel/avanslar/${row.id}`} onclick={(e) => e.stopPropagation()}
                    >Detay<ChevronRight size={13} /></a
                  ></td
                ></tr
              >{/each}</tbody
          >
        </table>
      </div>{/if}
  </section>
{/if}

{#if showCreate}<div class="overlay" role="presentation">
    <div class="dialog" role="dialog" aria-modal="true" aria-labelledby="new-title">
      <header>
        <div>
          <h2 id="new-title">Personel avansı ver</h2>
        </div>
        <button class="icon" aria-label="Kapat" onclick={() => (showCreate = false)}
          ><X size={18} /></button
        >
      </header>
      <form
        onsubmit={(e) => {
          e.preventDefault();
          void save();
        }}
      >
        <div class="form-field span-2">
          <span>Çalışan</span>
          <EntityCombobox
            id="advance-employee"
            bind:selected={selectedEmployee}
            title="Çalışan seç"
            description="Aktif çalışanlar arasında ad veya personel koduyla arayın."
            triggerLabel="Çalışan"
            triggerPlaceholder="Çalışan adı veya kodu ara…"
            searchPlaceholder="Çalışan adı veya kodu ara…"
            onSearch={searchEmployees}
            minQueryLength={0}
            onSelect={(employee) => (form.employee_id = employee.employeeID)}
            clearable
            onClear={() => (form.employee_id = '')}
          />
        </div>
        <label
          >TRY tutarı<Input
            bind:value={form.amount}
            inputmode="decimal"
            placeholder="0,00"
            aria-invalid={amountError}
          /></label
        >
        <label
          >Kasa / banka hesabı<select bind:value={form.account_id} required
            ><option value="">Seçin</option>{#each accounts as account}<option value={account.id}
                >{account.code} · {account.name} ({account.account_type === 'CASH'
                  ? 'Kasa'
                  : 'Banka'})</option
              >{/each}</select
          ></label
        >
        <label class="span-2">Açıklama<Input bind:value={form.description} required /></label><label
          >Referans<Input bind:value={form.reference} /></label
        ><label
          >Beklenen geri ödeme<DateInput
            bind:value={form.expected_repayment_date}
            ariaLabel="Beklenen geri ödeme tarihi"
          /></label
        >
        <footer>
          <Button type="button" variant="outline" onclick={() => (showCreate = false)}
            >Vazgeç</Button
          ><Button
            type="submit"
            disabled={saving ||
              amountError ||
              !form.amount ||
              !form.employee_id ||
              !form.account_id ||
              !form.description.trim()}>{saving ? 'Kaydediliyor…' : 'Kesinleştir'}</Button
          >
        </footer>
      </form>
    </div>
  </div>{/if}

<ConfirmDialog
  bind:open={negativeBalanceOpen}
  title="Negatif bakiye onayı"
  description="Seçili hesap avans ödemesinden sonra negatif bakiyeye düşecek. Devam etmek için gerekçenizi yazın."
  confirmLabel="Gerekçeyle devam et"
  onConfirm={async () => {
    if (!negativeBalanceReason.trim()) throw new Error('Negatif bakiye gerekçesi zorunludur.');
    await save(negativeBalanceReason, true);
  }}
>
  <label class="confirm-reason">
    <span>Gerekçe</span>
    <Input
      bind:value={negativeBalanceReason}
      maxlength={200}
      placeholder="Negatif bakiye gerekçesini yazın"
    />
  </label>
</ConfirmDialog>

<style>
  .actions,
  .filters,
  footer {
    display: flex;
    gap: 8px;
    flex-wrap: wrap;
  }

  .summary {
    display: flex;
    align-items: center;
    gap: 16px;
    margin: 14px 0;
    padding: 16px 18px;
    border: 1px solid var(--border);
    border-radius: var(--radius-panel);
    background: linear-gradient(180deg, var(--primary-soft) 0%, var(--surface) 78%);
  }
  .summary-icon {
    display: grid;
    place-items: center;
    width: 40px;
    height: 40px;
    flex: 0 0 auto;
    border-radius: 10px;
    background: var(--primary);
    color: var(--primary-foreground);
  }
  .summary-copy {
    flex: 1;
    display: grid;
    gap: 2px;
  }
  .summary-count {
    display: grid;
    gap: 2px;
    text-align: right;
    padding-left: 16px;
    border-left: 1px solid var(--border);
  }
  .summary span {
    color: var(--text-muted);
    font-size: 11px;
    font-weight: 650;
    text-transform: uppercase;
    letter-spacing: 0.05em;
  }
  .summary-copy strong {
    font-size: 24px;
    font-weight: 750;
  }
  .summary-count strong {
    font-size: 16px;
    font-weight: 700;
  }

  .list-card {
    padding: 0;
    overflow: hidden;
  }
  .filters {
    align-items: center;
    margin: 0;
    padding: 12px 14px;
    border-bottom: 1px solid var(--border);
    background: var(--surface-muted);
  }
  .filters :global(input) {
    width: auto;
  }
  select {
    height: var(--control-height);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    background: var(--surface);
    color: var(--text);
    padding: 0 9px;
    font-size: 12px;
  }

  .scroll {
    overflow: auto;
  }
  table {
    width: 100%;
    border-collapse: collapse;
    font-size: 13px;
  }
  thead th {
    position: sticky;
    top: 0;
    z-index: 1;
    padding: 9px 12px;
    background: var(--surface);
    border-bottom: 1px solid var(--border-strong);
    color: var(--text-muted);
    font-size: 10px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    text-align: left;
  }
  tbody td {
    padding: 11px 12px;
    border-bottom: 1px solid var(--border);
    text-align: left;
  }
  tbody tr:last-child td {
    border-bottom: 0;
  }
  tbody tr {
    cursor: pointer;
    transition: background 0.12s ease;
  }
  tbody tr:hover {
    background: var(--surface-muted);
  }
  td small {
    display: block;
    margin-top: 1px;
    color: var(--text-muted);
    font-size: 11px;
  }
  td.muted {
    color: var(--text-muted);
  }
  .num {
    text-align: right;
    font-variant-numeric: tabular-nums;
  }
  td.go {
    text-align: right;
    width: 1%;
    white-space: nowrap;
  }
  td.go a {
    display: inline-flex;
    align-items: center;
    gap: 2px;
    color: var(--primary);
    text-decoration: none;
    font-weight: 650;
  }
  td.go a:hover {
    text-decoration: underline;
  }
  .state {
    text-align: center;
    color: var(--text-muted);
    padding: 40px 24px;
    font-size: 13px;
  }

  .overlay {
    position: fixed;
    inset: 0;
    background: rgb(2 6 23 / 55%);
    display: grid;
    place-items: center;
    padding: 20px;
    z-index: 50;
  }
  .dialog {
    width: min(560px, 100%);
    max-height: 90vh;
    overflow: auto;
    background: var(--surface);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-panel);
    box-shadow: 0 22px 70px rgb(10 30 27 / 25%);
    padding: 20px;
  }
  .dialog header {
    display: flex;
    justify-content: space-between;
    align-items: flex-start;
    gap: 12px;
    padding-bottom: 14px;
    margin-bottom: 16px;
    border-bottom: 1px solid var(--border);
  }
  .dialog h2 {
    margin: 0;
    font-size: 16px;
    font-weight: 750;
  }
  .dialog header p {
    color: var(--text-muted);
    margin: 4px 0 0;
    font-size: 12px;
  }
  .dialog form {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 12px 14px;
  }
  .dialog label,
  .form-field {
    display: grid;
    gap: 5px;
    font-size: 12px;
    font-weight: 650;
    color: var(--text-muted);
  }
  .span-2 {
    grid-column: 1 / -1;
  }
  .confirm-reason {
    display: grid;
    gap: 6px;
    font-size: 13px;
    font-weight: 600;
  }
  .dialog footer {
    grid-column: 1 / -1;
    justify-content: flex-end;
    padding-top: 14px;
    margin-top: 4px;
    border-top: 1px solid var(--border);
  }
  .icon {
    display: grid;
    place-items: center;
    width: 30px;
    height: 30px;
    border: 0;
    border-radius: 6px;
    background: transparent;
    color: var(--text-muted);
    cursor: pointer;
  }
  .icon:hover {
    background: var(--surface-muted);
    color: var(--text);
  }

  @media (max-width: 560px) {
    .dialog form {
      grid-template-columns: 1fr;
    }
    .summary {
      flex-wrap: wrap;
    }
  }
</style>
