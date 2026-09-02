<script lang="ts">
  import { page } from '$app/state';
  import { ArrowLeft, Ban, Save } from '@lucide/svelte';
  import { onMount } from 'svelte';
  import { Button } from '$lib/components/ui/button';
  import { toast } from 'svelte-sonner';
  import { ErrorSummary } from '$lib/components/varya/status';
  import { DocumentStatus } from '$lib/components/varya/document-status';
  import PartyForm from '$lib/features/parties/PartyForm.svelte';
  import PartyQuickActions from '$lib/features/parties/PartyQuickActions.svelte';
  import PartyBalancesCard from '$lib/features/parties/PartyBalancesCard.svelte';
  import PartyRecentActivity from '$lib/features/parties/PartyRecentActivity.svelte';
  import { ConfirmDialog } from '$lib/components/varya/confirm-dialog';
  import { activateParty, deactivateParty, getParty, updateParty } from '$lib/features/parties/api';
  import { api, type Session } from '$lib/api';
  import {
    partyToInput,
    isPartyProvinceValidationMessage,
    normalizePartyInput,
    partyProvinceSelectionRequired,
    validatePartyInput,
    type Party,
    type PartyInput
  } from '$lib/features/parties/types';
  import { describeBalance } from '$lib/design/balance';
  import type { APIError } from '$lib/api';
  let party = $state<Party>();
  let form = $state<PartyInput>();
  let permissions = $state<string[]>([]);
  const canReadLedger = $derived(permissions.includes('party.ledger.read'));
  const detailBalance = $derived(
    party
      ? describeBalance(party.balance, party.balance_currency || party.default_currency || 'TRY')
      : undefined
  );
  let loading = $state(true);
  let saving = $state(false);
  let error = $state('');
  let message = $state('');
  let conflict = $state(false);
  let fieldErrors = $state<Record<string, string>>({});
  let activeTab = $state('basic');
  function reportValidation(message: string) {
    error = message;
    const id =
      (form && partyProvinceSelectionRequired(form)) || isPartyProvinceValidationMessage(message)
        ? 'party-province-0'
        : form?.kind === 'ORGANIZATION'
          ? 'party-legal-name'
          : 'party-first-name';
    fieldErrors = { [id]: message };
    activeTab = id === 'party-province-0' ? 'contact' : 'basic';
    setTimeout(() => document.getElementById(id)?.focus(), 0);
  }
  async function load() {
    loading = true;
    try {
      const id = page.params.id;
      if (!id) throw new Error('Cari kartı kimliği eksik.');
      const [loaded, session] = await Promise.all([
        getParty(id),
        api<Session>('/session').catch(() => null)
      ]);
      party = loaded;
      permissions = session?.permissions ?? [];
      form = partyToInput(party);
    } catch (cause) {
      error =
        typeof cause === 'object' && cause && 'message' in cause
          ? String(cause.message)
          : 'Cari kartı alınamadı.';
    } finally {
      loading = false;
    }
  }
  async function save() {
    if (!party || !form || saving) return;
    const normalized = normalizePartyInput(form);
    const validationMessage = validatePartyInput(normalized);
    if (validationMessage) {
      reportValidation(validationMessage);
      message = '';
      return;
    }
    saving = true;
    error = '';
    fieldErrors = {};
    message = '';
    conflict = false;
    try {
      party = normalized.is_active
        ? await updateParty(party.id, party.version, normalized)
        : await deactivateParty(party.id, party.version);
      form = partyToInput(party);
      message = normalized.is_active
        ? 'Cari kartı kaydedildi.'
        : 'Cari kartı pasifleştirildi. Geçmiş hareketleri korunuyor.';
      toast.success(message);
    } catch (cause) {
      conflict =
        typeof cause === 'object' &&
        cause !== null &&
        'code' in cause &&
        (cause as APIError).code === 'VERSION_CONFLICT';
      const errorMessage =
        typeof cause === 'object' && cause && 'message' in cause
          ? String(cause.message)
          : 'Cari kartı kaydedilemedi. Başka bir kullanıcı değiştirmiş olabilir; sayfayı yenileyin.';
      if (isPartyProvinceValidationMessage(errorMessage)) reportValidation(errorMessage);
      else error = errorMessage;
    } finally {
      saving = false;
    }
  }
  let confirmOpen = $state(false);
  let confirmState = $state<{
    title: string;
    description: string;
    confirmLabel: string;
    run: () => Promise<void>;
  } | null>(null);

  function askConfirm(config: NonNullable<typeof confirmState>) {
    confirmState = config;
    confirmOpen = true;
  }

  function deactivate() {
    if (!party) return;
    askConfirm({
      title: 'Cari kartı pasifleştir',
      description: 'Bu cari kartı pasifleştirilsin mi? Geçmiş hareketler korunur.',
      confirmLabel: 'Pasifleştir',
      run: runDeactivate
    });
  }

  async function runDeactivate() {
    if (!party) return;
    saving = true;
    try {
      party = await deactivateParty(party.id, party.version);
      form = partyToInput(party);
      message = 'Cari kartı pasifleştirildi. Geçmiş hareketleri korunuyor.';
    } catch (cause) {
      error =
        typeof cause === 'object' && cause && 'message' in cause
          ? String(cause.message)
          : 'Cari kartı pasifleştirilemedi.';
    } finally {
      saving = false;
    }
  }

  function activate() {
    if (!party) return;
    askConfirm({
      title: 'Cari kartı aktifleştir',
      description: 'Bu cari kartı yeniden aktifleştirilsin mi?',
      confirmLabel: 'Aktifleştir',
      run: runActivate
    });
  }

  async function runActivate() {
    if (!party) return;
    saving = true;
    error = '';
    try {
      party = await activateParty(party.id, party.version);
      form = partyToInput(party);
      message = 'Cari kartı aktifleştirildi.';
    } catch (cause) {
      error =
        typeof cause === 'object' && cause && 'message' in cause
          ? String(cause.message)
          : 'Cari kartı aktifleştirilemedi.';
    } finally {
      saving = false;
    }
  }
  onMount(() => {
    void load();
    const listener = (event: KeyboardEvent) => {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 's') {
        if (event.target instanceof Element && event.target.closest('[role="dialog"]')) return;
        event.preventDefault();
        void save();
      }
    };
    window.addEventListener('keydown', listener);
    return () => {
      window.removeEventListener('keydown', listener);
    };
  });
</script>

<svelte:head><title>{party?.display_name ?? 'Cari Kartı'} · Varya One</title></svelte:head>

{#if confirmState}
  <ConfirmDialog
    bind:open={confirmOpen}
    title={confirmState.title}
    description={confirmState.description}
    confirmLabel={confirmState.confirmLabel}
    onConfirm={confirmState.run}
  />
{/if}
{#if loading}<div class="panel loading">Cari kartı yükleniyor…</div>{:else if !party || !form}<div
    class="notice error"
    role="alert"
  >
    {error || 'Cari kartı bulunamadı.'}
  </div>{:else}
  <header class="page-header">
    <div>
      <a class="back" href="/cari/kartlar"><ArrowLeft size={14} />Cari Kartlar</a>
      <div class="title-row">
        <h1>{party.display_name}</h1>
        <DocumentStatus status={party.is_active ? 'ACTIVE' : 'INACTIVE'} />
      </div>
      <p>
        {party.code} · {party.is_customer && party.is_supplier
          ? 'Müşteri + Tedarikçi'
          : party.is_customer
            ? 'Müşteri'
            : 'Tedarikçi'}
      </p>
    </div>
    <div class="page-actions">
      {#if party.is_active}<Button
          type="button"
          variant="outline"
          onclick={deactivate}
          disabled={saving}><Ban size={14} />Pasifleştir</Button
        ><Button type="submit" form="party-detail-form" disabled={saving}
          ><Save size={15} />{saving ? 'Kaydediliyor…' : 'Kaydet'}</Button
        >{/if}
      {#if !party.is_active}<Button type="button" onclick={activate} disabled={saving}
          >Aktifleştir</Button
        >{/if}
    </div>
  </header>
  {#if message}<div class="success-message" role="status">{message}</div>{/if}
  <ErrorSummary errors={fieldErrors} />
  {#if error && !Object.keys(fieldErrors).length}<div class="notice error" role="alert">
      {error}
      {#if conflict}<Button variant="outline" size="sm" onclick={() => load()}
          >Güncel kartı yükle</Button
        >{/if}
    </div>{/if}
  <div class="detail-layout">
    <form
      id="party-detail-form"
      class="panel form-panel"
      onsubmit={(event) => {
        event.preventDefault();
        void save();
      }}
    >
      <PartyForm
        bind:value={form}
        bind:activeTab
        disabled={saving || !party.is_active}
        errors={fieldErrors}
      />
    </form>
    <div class="side-column">
      <aside class="summary-panel panel">
        <h2>Cari özeti</h2>
        <dl>
          <div>
            <dt>Bakiye</dt>
            <dd class:negative={detailBalance?.tone === 'credit'}>
              <strong>{detailBalance?.headline ?? '—'}</strong>
              <small>{detailBalance?.meaning ?? 'Bakiye alınamadı.'}</small>
            </dd>
          </div>
          <div>
            <dt>Kod</dt>
            <dd>{party.code}</dd>
          </div>
          <div>
            <dt>Telefon</dt>
            <dd>{party.phone || '—'}</dd>
          </div>
          <div>
            <dt>E-posta</dt>
            <dd>{party.email || '—'}</dd>
          </div>
        </dl>
      </aside>
      <PartyBalancesCard partyID={party.id} canRead={canReadLedger} />
      <PartyQuickActions {party} {permissions} />
    </div>
  </div>
  <PartyRecentActivity partyID={party.id} canRead={canReadLedger} />
{/if}

<style>
  .back {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    margin-bottom: 5px;
    color: var(--primary);
    font-size: 11px;
    text-decoration: none;
  }
  .title-row {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .detail-layout {
    display: grid;
    grid-template-columns: minmax(0, 900px) 260px;
    gap: 12px;
  }
  .form-panel {
    padding: 0;
  }
  .side-column {
    display: flex;
    flex-direction: column;
    gap: 12px;
    align-self: start;
  }
  .side-column > :global(*) {
    width: 100%;
    margin: 0;
    box-sizing: border-box;
  }
  .summary-panel {
    padding: 13px;
  }
  .summary-panel h2 {
    margin: 0 0 10px;
    font-size: 13px;
  }
  .summary-panel dl {
    margin: 0;
  }
  .summary-panel dl > div {
    display: flex;
    justify-content: space-between;
    gap: 10px;
    padding: 8px 0;
    border-bottom: 1px solid var(--border);
    font-size: 12px;
  }
  .summary-panel dt {
    color: var(--text-muted);
  }
  .summary-panel dd {
    margin: 0;
    font-weight: 700;
  }
  .summary-panel dd small {
    display: block;
    color: var(--text-muted);
    font-size: 10px;
    font-weight: 400;
    margin-top: 2px;
  }
  .negative {
    color: var(--danger);
  }
  .loading {
    padding: 30px;
    color: var(--text-muted);
    text-align: center;
  }
  .success-message {
    margin: 0 0 10px;
    padding: 8px 10px;
    border: 1px solid color-mix(in srgb, var(--success) 35%, var(--border));
    border-radius: var(--radius-control);
    background: color-mix(in srgb, var(--success) 10%, var(--surface));
    color: var(--success);
    font-size: 12px;
  }
  @media (max-width: 860px) {
    .detail-layout {
      grid-template-columns: 1fr;
    }
  }
</style>
