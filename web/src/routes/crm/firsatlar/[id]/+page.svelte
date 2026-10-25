<script lang="ts">
  import { DateInput } from '$lib/components/varya/date-input';
  import { onMount } from 'svelte';
  import { page } from '$app/state';
  import { goto } from '$app/navigation';
  import { ArrowLeft, FileText, LoaderCircle, Pencil, UserCheck } from '@lucide/svelte';
  import { api, APIRequestError, type Session } from '$lib/api';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import * as Field from '$lib/components/ui/field';
  import ReasonDialog from '$lib/components/varya/reason-dialog/ReasonDialog.svelte';
  import { formatDate, formatMoney } from '$lib/design/formatters';
  import { SUPPORTED_CURRENCIES } from '$lib/design/currencies';
  import {
    createOpportunityActivity,
    getOpportunity,
    linkOpportunityQuote,
    listCRMMembers,
    listOpportunityActivities,
    listStages,
    moveOpportunity,
    updateOpportunity
  } from '$lib/features/crm/api';
  import ActivityTimeline from '$lib/features/crm/ActivityTimeline.svelte';
  import {
    quoteStatusLabel,
    stageKindLabel,
    type Activity,
    type Member,
    type Opportunity,
    type Stage
  } from '$lib/features/crm/types';

  type QuoteOption = {
    id: string;
    document_no: string;
    status: string;
    grand_total: string;
    currency_code: string;
  };

  let session = $state<Session | null>(null);
  let opportunity = $state<Opportunity | null>(null);
  let stages = $state<Stage[]>([]);
  let activities = $state<Activity[]>([]);
  let members = $state<Member[]>([]);
  let quotes = $state<QuoteOption[]>([]);
  let loading = $state(true);
  let error = $state('');
  let message = $state('');
  let moving = $state(false);

  const permissions = $derived(session?.permissions ?? []);
  const canEdit = $derived(permissions.includes('crm.edit'));
  const canManage = $derived(permissions.includes('crm.manage'));
  const salesEnabled = $derived(Boolean(session?.modules.includes('preaccounting')));
  const canQuote = $derived(salesEnabled && permissions.includes('sales.quote.manage'));
  const canReadQuotes = $derived(salesEnabled && permissions.includes('sales.quote.read'));
  const canConvert = $derived(salesEnabled && permissions.includes('party.create'));
  const activeStages = $derived(stages.filter((s) => s.is_active));

  async function load() {
    const id = page.params.id ?? '';
    loading = true;
    error = '';
    try {
      const [loaded, loadedStages, timeline] = await Promise.all([
        getOpportunity(id),
        listStages(),
        listOpportunityActivities(id)
      ]);
      opportunity = loaded;
      stages = loadedStages.items;
      activities = timeline.items;
      await loadQuotes();
    } catch (cause) {
      error = cause instanceof APIRequestError ? cause.message : 'Fırsat yüklenemedi.';
    } finally {
      loading = false;
    }
  }

  async function loadQuotes() {
    quotes = [];
    if (!opportunity?.party_id || !canReadQuotes) return;
    try {
      const result = await api<{ items: QuoteOption[] }>(
        `/sales/quotes?party_id=${encodeURIComponent(opportunity.party_id)}&limit=20`
      );
      quotes = result.items ?? [];
    } catch {
      quotes = [];
    }
  }

  // --- aşama ---
  let lostOpen = $state(false);
  let pendingLost = $state<Stage | null>(null);

  async function moveTo(stage: Stage, lostReason = '') {
    if (!opportunity || moving || stage.id === opportunity.stage_id) return;
    if (stage.kind === 'LOST' && !lostReason) {
      pendingLost = stage;
      lostOpen = true;
      return;
    }
    moving = true;
    error = '';
    try {
      opportunity = await moveOpportunity(
        opportunity.id,
        opportunity.version,
        stage.id,
        lostReason
      );
      message = `Aşama: ${opportunity.stage_name}.`;
    } catch (cause) {
      error = cause instanceof APIRequestError ? cause.message : 'Aşama değiştirilemedi.';
    } finally {
      moving = false;
    }
  }

  // --- düzenleme ---
  let editing = $state(false);
  let saving = $state(false);
  let form = $state({
    title: '',
    amount: '',
    currency: 'TRY',
    expected_close_date: '',
    owner_user_id: '',
    notes: ''
  });

  function openEdit() {
    if (!opportunity) return;
    form = {
      title: opportunity.title,
      amount: opportunity.amount,
      currency: opportunity.currency,
      expected_close_date: opportunity.expected_close_date ?? '',
      owner_user_id: opportunity.owner_user_id ?? '',
      notes: opportunity.notes
    };
    editing = true;
  }

  async function saveEdit(event: SubmitEvent) {
    event.preventDefault();
    if (!opportunity || saving) return;
    saving = true;
    error = '';
    try {
      opportunity = await updateOpportunity(opportunity.id, opportunity.version, {
        ...form,
        title: form.title.trim(),
        amount: form.amount.trim() || '0'
      });
      editing = false;
      message = 'Fırsat güncellendi.';
    } catch (cause) {
      error = cause instanceof APIRequestError ? cause.message : 'Fırsat güncellenemedi.';
    } finally {
      saving = false;
    }
  }

  // --- teklif ---
  let linkQuoteID = $state('');

  async function link() {
    if (!opportunity || !linkQuoteID) return;
    error = '';
    try {
      opportunity = await linkOpportunityQuote(opportunity.id, linkQuoteID);
      message = 'Teklif fırsata bağlandı.';
      linkQuoteID = '';
    } catch (cause) {
      error = cause instanceof APIRequestError ? cause.message : 'Teklif bağlanamadı.';
    }
  }

  onMount(async () => {
    try {
      session = await api<Session>('/session');
    } catch {
      error = 'Oturum bilgisi alınamadı.';
    }
    listCRMMembers()
      .then((r) => (members = r.items))
      .catch(() => (members = []));
    await load();
  });
</script>

<svelte:head><title>{opportunity?.title ?? 'Fırsat'} · Varya One</title></svelte:head>

<header class="page-header">
  <div>
    <a class="back" href="/crm/firsatlar"><ArrowLeft size={14} aria-hidden="true" /> Fırsatlar</a>
    <h1>{opportunity?.title ?? 'Fırsat'}</h1>
    {#if opportunity}
      <p class="meta">
        {#if opportunity.party_id}
          <a href="/cari/kartlar/{opportunity.party_id}">{opportunity.party_name}</a>
        {:else if opportunity.lead_id}
          aday: <a href="/crm/adaylar/{opportunity.lead_id}">{opportunity.lead_name}</a>
        {/if}
        · {formatMoney(opportunity.amount, opportunity.currency)} · {opportunity.owner_name ||
          'Atanmamış'}
      </p>
    {/if}
  </div>
  {#if opportunity && canEdit}
    <div class="page-actions">
      <Button variant="outline" onclick={openEdit}>
        <Pencil size={14} data-icon="inline-start" aria-hidden="true" />Düzenle
      </Button>
    </div>
  {/if}
</header>

{#if error}<p class="notice error" role="alert">{error}</p>{/if}
{#if message}<p class="notice success" role="status">{message}</p>{/if}

{#if loading && !opportunity}
  <p class="state" role="status">Yükleniyor…</p>
{:else if opportunity}
  <section class="panel stages" aria-label="Aşama">
    {#each activeStages as stage (stage.id)}
      <button
        type="button"
        class="step kind-{stage.kind.toLowerCase()}"
        class:current={stage.id === opportunity.stage_id}
        disabled={!canEdit || moving}
        aria-pressed={stage.id === opportunity.stage_id}
        onclick={() => void moveTo(stage)}>{stage.name}</button
      >
    {/each}
  </section>
  {#if opportunity.stage_kind === 'LOST' && opportunity.lost_reason}
    <p class="notice error">Kaybedilme nedeni: {opportunity.lost_reason}</p>
  {/if}

  {#if editing}
    <section class="panel">
      <form class="grid" onsubmit={saveEdit}>
        <Field.Field class="full">
          <Field.FieldLabel for="e-title">Başlık</Field.FieldLabel>
          <Input id="e-title" bind:value={form.title} maxlength={200} required />
        </Field.Field>
        <Field.Field>
          <Field.FieldLabel for="e-amount">Tutar</Field.FieldLabel>
          <Input id="e-amount" bind:value={form.amount} inputmode="decimal" />
        </Field.Field>
        <Field.Field>
          <Field.FieldLabel for="e-currency">Para birimi</Field.FieldLabel>
          <select id="e-currency" class="select" bind:value={form.currency}>
            {#each SUPPORTED_CURRENCIES as currency (currency.code)}
              <option value={currency.code}>{currency.code}</option>
            {/each}
          </select>
        </Field.Field>
        <Field.Field>
          <Field.FieldLabel for="e-close">Beklenen kapanış</Field.FieldLabel>
          <DateInput
            id="e-close"
            bind:value={form.expected_close_date}
            ariaLabel="Beklenen kapanış"
          />
        </Field.Field>
        {#if canManage}
          <Field.Field>
            <Field.FieldLabel for="e-owner">Sorumlu</Field.FieldLabel>
            <select id="e-owner" class="select" bind:value={form.owner_user_id}>
              <option value="">Atanmamış</option>
              {#each members as member (member.user_id)}
                <option value={member.user_id}>{member.display_name}</option>
              {/each}
            </select>
          </Field.Field>
        {/if}
        <Field.Field class="full">
          <Field.FieldLabel for="e-notes">Not</Field.FieldLabel>
          <textarea id="e-notes" class="textarea" rows="3" maxlength={4000} bind:value={form.notes}
          ></textarea>
        </Field.Field>
        <div class="actions full">
          <Button variant="ghost" type="button" onclick={() => (editing = false)}>Vazgeç</Button>
          <Button type="submit" disabled={saving}>
            {#if saving}<LoaderCircle data-icon="inline-start" aria-hidden="true" />{/if}Kaydet
          </Button>
        </div>
      </form>
    </section>
  {/if}

  <div class="layout">
    <div class="col">
      <section class="panel" aria-labelledby="summary-title">
        <h2 id="summary-title">Özet</h2>
        <dl>
          <dt>Aşama</dt>
          <dd>
            {opportunity.stage_name}
            {#if opportunity.stage_kind !== 'OPEN'}({stageKindLabel(opportunity.stage_kind)}){/if}
          </dd>
          <dt>Olasılık</dt>
          <dd>%{opportunity.probability}</dd>
          <dt>Tutar</dt>
          <dd>{formatMoney(opportunity.amount, opportunity.currency)}</dd>
          <dt>Beklenen kapanış</dt>
          <dd>
            {opportunity.expected_close_date ? formatDate(opportunity.expected_close_date) : '—'}
          </dd>
          {#if opportunity.closed_at}
            <dt>Kapandı</dt>
            <dd>{formatDate(opportunity.closed_at, true)}</dd>
          {/if}
          <dt>Oluşturma</dt>
          <dd>{formatDate(opportunity.created_at)}</dd>
        </dl>
        {#if opportunity.notes}<p class="notes">{opportunity.notes}</p>{/if}
      </section>

      <section class="panel" aria-labelledby="quote-title">
        <h2 id="quote-title">Teklif</h2>
        {#if opportunity.quote_id}
          <p class="quote">
            <FileText size={14} aria-hidden="true" />
            {#if canReadQuotes}
              <a href="/satis/teklifler/{opportunity.quote_id}"
                >{opportunity.quote_no || 'Teklif'}</a
              >
            {:else}{opportunity.quote_no || 'Teklif'}{/if}
            {#if opportunity.quote_status}· {quoteStatusLabel(opportunity.quote_status)}{/if}
            {#if opportunity.quote_total}· {formatMoney(
                opportunity.quote_total,
                opportunity.quote_currency
              )}{/if}
          </p>
          <p class="hint">Teklif kabul edildiğinde fırsat kendiliğinden kazanılmış olur.</p>
        {:else if !salesEnabled}
          <p class="hint">Teklif için Ön Muhasebe modülü açık olmalıdır.</p>
        {:else if !opportunity.party_id}
          <p class="hint">Teklif hazırlamak için önce adayı cariye dönüştürün.</p>
          {#if canConvert && canEdit && opportunity.lead_id}
            <Button
              size="sm"
              onclick={() => goto(`/cari/kartlar/yeni?aday=${opportunity?.lead_id}`)}
            >
              <UserCheck size={14} data-icon="inline-start" aria-hidden="true" />Cariye dönüştür
            </Button>
          {/if}
        {:else}
          <div class="quote-actions">
            {#if canQuote && canEdit}
              <Button
                size="sm"
                onclick={() =>
                  goto(
                    `/satis/teklifler/yeni?party_id=${opportunity?.party_id}&crm_opportunity=${opportunity?.id}`
                  )}
              >
                <FileText size={14} data-icon="inline-start" aria-hidden="true" />Teklif hazırla
              </Button>
            {/if}
            {#if canEdit && quotes.length}
              <select class="select narrow" bind:value={linkQuoteID} aria-label="Mevcut teklif">
                <option value="">Mevcut teklifi bağla…</option>
                {#each quotes as quote (quote.id)}
                  <option value={quote.id}>
                    {quote.document_no} · {quoteStatusLabel(quote.status)}
                  </option>
                {/each}
              </select>
              <Button
                size="sm"
                variant="outline"
                disabled={!linkQuoteID}
                onclick={() => void link()}>Bağla</Button
              >
            {/if}
          </div>
        {/if}
      </section>
    </div>
    <div class="col">
      <ActivityTimeline
        bind:activities
        create={(input) => createOpportunityActivity(opportunity?.id ?? '', input)}
        {canEdit}
        {canManage}
        userID={session?.user.id ?? ''}
        {members}
        allowCall={Boolean(opportunity.party_id)}
      />
    </div>
  </div>
{/if}

<ReasonDialog
  bind:open={lostOpen}
  title="Fırsat neden kaybedildi?"
  description="Kayıp nedenleri raporlarda toplanır."
  label="Neden"
  placeholder="Örn. Rakip daha ucuz teklif verdi"
  confirmLabel="Kaybedildi olarak kapat"
  onConfirm={async (reason) => {
    if (pendingLost) await moveTo(pendingLost, reason);
    pendingLost = null;
  }}
/>

<style>
  .page-header {
    display: flex;
    justify-content: space-between;
    align-items: flex-start;
    gap: 12px;
    flex-wrap: wrap;
  }
  .page-actions {
    display: flex;
    gap: 8px;
  }
  .back {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    color: var(--text-muted);
    font-size: 12px;
    text-decoration: none;
  }
  .meta {
    margin: 4px 0 0;
    color: var(--text-muted);
    font-size: 12px;
  }
  .meta a {
    color: var(--primary);
  }
  .panel {
    margin-top: 16px;
    padding: 14px 16px;
  }
  .panel h2 {
    margin: 0 0 10px;
    font-size: 14px;
  }
  .stages {
    display: flex;
    gap: 4px;
    overflow-x: auto;
    padding: 8px;
  }
  .step {
    flex: 1 0 auto;
    padding: 8px 12px;
    border: 1px solid var(--border-strong);
    border-radius: 6px;
    background: var(--surface);
    color: var(--text);
    font-size: 12px;
    cursor: pointer;
    white-space: nowrap;
  }
  .step.current {
    border-color: var(--primary);
    background: color-mix(in srgb, var(--primary) 14%, transparent);
    color: var(--primary);
    font-weight: 700;
  }
  .step.kind-won.current {
    border-color: var(--success);
    background: color-mix(in srgb, var(--success) 16%, transparent);
    color: var(--success);
  }
  .step.kind-lost.current {
    border-color: var(--danger);
    background: color-mix(in srgb, var(--danger) 14%, transparent);
    color: var(--danger);
  }
  .step:disabled {
    cursor: default;
  }
  .layout {
    display: grid;
    grid-template-columns: minmax(0, 1fr);
    gap: 0 16px;
  }
  @media (min-width: 960px) {
    .layout {
      grid-template-columns: minmax(0, 5fr) minmax(0, 6fr);
    }
  }
  .col {
    min-width: 0;
  }
  dl {
    display: grid;
    grid-template-columns: max-content minmax(0, 1fr);
    gap: 6px 14px;
    margin: 0;
    font-size: 13px;
  }
  dt {
    color: var(--text-muted);
  }
  dd {
    margin: 0;
  }
  .notes {
    margin: 12px 0 0;
    padding-top: 10px;
    border-top: 1px dashed var(--border);
    font-size: 13px;
    white-space: pre-wrap;
  }
  .quote {
    display: flex;
    align-items: center;
    gap: 6px;
    margin: 0;
    font-size: 13px;
  }
  .quote a {
    color: var(--primary);
    font-weight: 600;
  }
  .quote-actions {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
  }
  .hint {
    margin: 6px 0 8px;
    color: var(--text-muted);
    font-size: 12px;
  }
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 200px), 1fr));
    gap: 14px 16px;
  }
  .grid :global(.full),
  .grid .full {
    grid-column: 1 / -1;
  }
  .grid :global(input) {
    width: 100%;
    box-sizing: border-box;
  }
  .actions {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
  }
  .select,
  .textarea {
    width: 100%;
    box-sizing: border-box;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    background: var(--surface);
    color: var(--text);
    font-size: 13px;
  }
  .select {
    height: var(--control-height);
    padding: 0 10px;
  }
  .select.narrow {
    width: auto;
    min-width: 200px;
  }
  .textarea {
    padding: 8px 10px;
    font: inherit;
    font-size: 13px;
    resize: vertical;
  }
  .state {
    margin: 0;
    padding: 16px 0;
    color: var(--text-muted);
    font-size: 13px;
    text-align: center;
  }
</style>
