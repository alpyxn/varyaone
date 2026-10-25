<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/state';
  import { goto } from '$app/navigation';
  import {
    ArrowLeft,
    LoaderCircle,
    Pencil,
    PhoneOff,
    RefreshCw,
    SkipForward,
    UserCheck
  } from '@lucide/svelte';
  import { api, APIRequestError, type Session } from '$lib/api';
  import { Button } from '$lib/components/ui/button';
  import ReasonDialog from '$lib/components/varya/reason-dialog/ReasonDialog.svelte';
  import { formatDate, formatRelative } from '$lib/design/formatters';
  import {
    createLeadActivity,
    getLead,
    listCRMMembers,
    listLeadActivities,
    listLeads,
    setLeadDoNotCall,
    updateLead
  } from '$lib/features/crm/api';
  import ActivityTimeline from '$lib/features/crm/ActivityTimeline.svelte';
  import CallPanel from '$lib/features/crm/CallPanel.svelte';
  import LeadFields from '$lib/features/crm/LeadFields.svelte';
  import OpportunitiesPanel from '$lib/features/crm/OpportunitiesPanel.svelte';
  import {
    callOutcomeLabel,
    emptyLeadInput,
    fromLocalInput,
    leadContextFromParams,
    leadContextQuery,
    leadStatusLabel,
    priorityLabel,
    withLeadContext,
    telHref,
    websiteHref,
    toLeadInput,
    type Activity,
    type CallResult,
    type Lead,
    type LeadInput,
    type LeadSummary,
    type Member
  } from '$lib/features/crm/types';

  let lead = $state<Lead | null>(null);
  let activities = $state<Activity[]>([]);
  let members = $state<Member[]>([]);
  let session = $state<Session | null>(null);
  let loading = $state(true);
  let error = $state('');
  let message = $state('');

  const permissions = $derived(session?.permissions ?? []);
  const userID = $derived(session?.user.id ?? '');
  const canEdit = $derived(permissions.includes('crm.edit'));
  const canManage = $derived(permissions.includes('crm.manage'));
  // Called before and no callback due: a call flow would not offer it again,
  // so a second call from here must be a deliberate choice.
  const calledBefore = $derived(
    Boolean(
      lead &&
      lead.call_attempts > 0 &&
      !lead.do_not_call &&
      lead.status !== 'CONVERTED' &&
      !(lead.next_action_at && new Date(lead.next_action_at).getTime() <= Date.now())
    )
  );
  const callable = $derived(
    Boolean(lead && canEdit && !lead.do_not_call && lead.status !== 'CONVERTED')
  );
  // Turning a lead into a cari opens the regular cari form, so it needs the
  // pre-accounting module and the right to create cari cards.
  const canConvert = $derived(
    Boolean(
      lead &&
      canEdit &&
      lead.status !== 'CONVERTED' &&
      session?.modules.includes('preaccounting') &&
      permissions.includes('party.create')
    )
  );

  async function onCallSaved(result: CallResult, goNext: boolean) {
    lead = result.lead;
    activities = [...(result.task ? [result.task] : []), result.activity, ...activities];
    message = `Arama kaydedildi: ${callOutcomeLabel(result.activity.call_outcome ?? '')}.`;
    if (goNext) await openNext();
  }

  // The list this lead was opened from (filters and order travel in the URL);
  // a lead opened elsewhere walks the default list: callable leads, hottest
  // first.
  const listContext = $derived(leadContextFromParams(page.url.searchParams));
  let movingNext = $state(false);

  /**
   * Moves to the lead after this one in that list. The order is stable, so a
   * skipped lead is not offered again and two leads never alternate.
   */
  async function openNext() {
    if (!lead || movingNext) return;
    movingNext = true;
    try {
      const result = await listLeads({
        ...leadContextQuery(listContext),
        callable: true,
        cursor: lead.id,
        limit: 1
      });
      const next = result.items[0];
      if (next) {
        await goto(withLeadContext(`/crm/adaylar/${next.id}`, listContext));
      } else {
        message = 'Listenin sonuna gelindi; aranacak başka aday yok.';
      }
    } catch (cause) {
      error = cause instanceof APIRequestError ? cause.message : 'Sıradaki aday bulunamadı.';
    } finally {
      movingNext = false;
    }
  }

  // --- kart düzenleme ---
  let editing = $state(false);
  let editForm = $state<LeadInput>(emptyLeadInput());
  let editSaving = $state(false);
  let editError = $state('');
  let editDuplicates = $state<LeadSummary[]>([]);
  let editDuplicateConfirmed = $state(false);

  function openEdit() {
    if (!lead) return;
    editForm = toLeadInput(lead);
    editDuplicates = [];
    editDuplicateConfirmed = false;
    editError = '';
    editing = true;
  }

  async function saveEdit(event: SubmitEvent) {
    event.preventDefault();
    if (!lead || editSaving) return;
    editSaving = true;
    editError = '';
    try {
      lead = await updateLead(lead.id, lead.version, {
        ...editForm,
        next_action_at: fromLocalInput(editForm.next_action_at),
        allow_duplicate: editDuplicateConfirmed
      });
      editing = false;
      message = 'Aday kartı güncellendi.';
    } catch (cause) {
      if (cause instanceof APIRequestError && cause.code === 'CRM_LEAD_DUPLICATE_PHONE') {
        editDuplicates = (cause.details.matches as LeadSummary[] | undefined) ?? [];
        editDuplicateConfirmed = true;
        editError =
          'Bu numara başka bir adayda kayıtlı. Yine de kaydetmek için tekrar Kaydet’e basın.';
      } else {
        editError = cause instanceof APIRequestError ? cause.message : 'Aday güncellenemedi.';
      }
    } finally {
      editSaving = false;
    }
  }

  // --- aranmasın ---
  let dncOpen = $state(false);

  async function markDoNotCall(reason: string) {
    if (!lead) return;
    lead = await setLeadDoNotCall(lead.id, true, reason);
    message = 'Aday “aranmasın” olarak işaretlendi.';
  }

  async function clearDoNotCall() {
    if (!lead) return;
    try {
      lead = await setLeadDoNotCall(lead.id, false);
      message = '“Aranmasın” işareti kaldırıldı.';
    } catch (cause) {
      error = cause instanceof APIRequestError ? cause.message : 'İşlem tamamlanamadı.';
    }
  }

  async function createEntry(input: Parameters<typeof createLeadActivity>[1]) {
    if (!lead) throw new Error('Aday yüklenmedi.');
    const created = await createLeadActivity(lead.id, input);
    if (input.kind === 'MEETING' || input.kind === 'EMAIL') lead = await getLead(lead.id);
    return created;
  }

  async function load() {
    const id = page.params.id;
    if (!id) {
      error = 'Aday kimliği bulunamadı.';
      loading = false;
      return;
    }
    loading = true;
    error = '';
    message = '';
    editing = false;
    try {
      const [loaded, timeline] = await Promise.all([getLead(id), listLeadActivities(id)]);
      lead = loaded;
      activities = timeline.items;
    } catch (cause) {
      lead = null;
      error = cause instanceof APIRequestError ? cause.message : 'Aday yüklenemedi.';
    } finally {
      loading = false;
    }
  }

  // Sıradaki adaya geçişte aynı bileşen yeni kimlikle yeniden yüklenir.
  let loadedFor = '';
  $effect(() => {
    const id = page.params.id ?? '';
    if (id && id !== loadedFor && session) {
      loadedFor = id;
      void load();
    }
  });

  onMount(async () => {
    try {
      session = await api<Session>('/session');
    } catch {
      error = 'Oturum bilgisi alınamadı.';
      loading = false;
    }
    listCRMMembers()
      .then((r) => (members = r.items))
      .catch(() => (members = []));
  });
</script>

<svelte:head><title>{lead ? lead.display_name : 'Aday'} · Varya One</title></svelte:head>

<header class="page-header">
  <div>
    <a class="back" href={withLeadContext('/crm/adaylar', listContext)}
      ><ArrowLeft size={14} aria-hidden="true" /> Adaylar</a
    >
    <h1>{lead?.display_name ?? 'Aday'}</h1>
    {#if lead}
      <p class="meta">
        {leadStatusLabel(lead.status)} · {lead.owner_name || 'Atanmamış'} · {lead.call_attempts} arama
        {#if lead.last_call_at}· son arama {formatRelative(lead.last_call_at)}{/if}
      </p>
    {/if}
  </div>
  <div class="page-actions">
    <Button variant="outline" onclick={() => void load()} disabled={loading}>
      <RefreshCw size={14} data-icon="inline-start" aria-hidden="true" />Yenile
    </Button>
    {#if lead && canEdit}
      <Button variant="outline" onclick={openEdit}>
        <Pencil size={14} data-icon="inline-start" aria-hidden="true" />Düzenle
      </Button>
      <Button variant="outline" onclick={() => void openNext()} disabled={movingNext}>
        <SkipForward size={14} data-icon="inline-start" aria-hidden="true" />Sıradaki aday
      </Button>
    {/if}
    {#if canConvert && lead}
      <Button onclick={() => goto(`/cari/kartlar/yeni?aday=${lead?.id}`)}>
        <UserCheck size={14} data-icon="inline-start" aria-hidden="true" />Cariye dönüştür
      </Button>
    {/if}
  </div>
</header>

{#if error}<p class="notice error" role="alert">{error}</p>{/if}
{#if message}<p class="notice success" role="status">{message}</p>{/if}

{#if loading && !lead}
  <p class="state" role="status">Yükleniyor…</p>
{:else if lead}
  {#if lead.status === 'CONVERTED' && lead.converted_party_id}
    <p class="notice success">
      Bu aday cariye dönüştü. <a href="/cari/kartlar/{lead.converted_party_id}">Cari kartını aç →</a
      >
    </p>
  {/if}
  {#if calledBefore && lead.last_call_at}
    <section class="panel called-banner" role="status">
      <strong>Bu aday daha önce aranmış.</strong>
      <span>
        {lead.call_attempts} kez · son sonuç {callOutcomeLabel(lead.last_call_outcome ?? '')}
        {formatRelative(lead.last_call_at)}. Otomatik sıralar onu tekrar getirmez; yeniden aramak
        bilinçli bir tercih olmalı.
      </span>
    </section>
  {/if}
  {#if lead.do_not_call}
    <section class="panel dnc-banner" role="status">
      <PhoneOff size={16} aria-hidden="true" />
      <div>
        <strong>Bu aday aranmak istemiyor.</strong>
        <span>
          {formatDate(lead.do_not_call_at ?? undefined, true)}{lead.do_not_call_reason
            ? ` · ${lead.do_not_call_reason}`
            : ''}
        </span>
      </div>
      {#if canManage}
        <Button variant="outline" size="sm" onclick={() => void clearDoNotCall()}
          >İşareti kaldır</Button
        >
      {/if}
    </section>
  {/if}

  {#if editing}
    <section class="panel form-card" aria-labelledby="lead-edit-title">
      <div class="form-head"><h2 id="lead-edit-title">Aday kartını düzenle</h2></div>
      <form onsubmit={saveEdit}>
        <div class="form-body">
          <LeadFields
            bind:value={editForm}
            {members}
            canAssign={canManage}
            showStatus={lead.status !== 'CONVERTED'}
            duplicates={editDuplicates}
          />
        </div>
        {#if editError}<p class="notice error" role="alert">{editError}</p>{/if}
        <div class="form-foot">
          <Button variant="ghost" type="button" onclick={() => (editing = false)}>Vazgeç</Button>
          <Button type="submit" disabled={editSaving}>
            {#if editSaving}<LoaderCircle data-icon="inline-start" aria-hidden="true" /> Kaydediliyor…{:else}Kaydet{/if}
          </Button>
        </div>
      </form>
    </section>
  {/if}

  <div class="layout">
    <div class="col">
      <section class="panel card" aria-labelledby="contact-title">
        <h2 id="contact-title">İletişim</h2>
        <dl>
          <dt>Yetkili</dt>
          <dd>
            {lead.contact_name || '—'}{lead.contact_title ? ` · ${lead.contact_title}` : ''}
          </dd>
          <dt>Telefon</dt>
          <dd>
            {#if lead.phone}
              {#if callable}<a href={telHref(lead.phone)}>{lead.phone}</a>{:else}{lead.phone}{/if}
            {:else}—{/if}
            {#if lead.phone2}
              ·
              {#if callable}<a href={telHref(lead.phone2)}>{lead.phone2}</a
                >{:else}{lead.phone2}{/if}
            {/if}
          </dd>
          <dt>E-posta</dt>
          <dd>
            {#if lead.email}<a href="mailto:{lead.email}">{lead.email}</a>{:else}—{/if}
          </dd>
          <dt>Öncelik</dt>
          <dd>{priorityLabel(lead.priority)}</dd>
          <dt>Konum</dt>
          <dd>{[lead.district, lead.city].filter(Boolean).join(', ') || '—'}</dd>
          {#if lead.address}
            <dt>Adres</dt>
            <dd>{lead.address}</dd>
          {/if}
          {#if lead.website}
            <dt>Web sitesi</dt>
            <dd>
              <a href={websiteHref(lead.website)} target="_blank" rel="noopener noreferrer"
                >{lead.website}</a
              >
            </dd>
          {/if}
          <dt>Sektör</dt>
          <dd>{lead.sector || '—'}</dd>
          <dt>Kaynak</dt>
          <dd>{lead.source || '—'}</dd>
          <dt>Sonraki işlem</dt>
          <dd
            class:overdue={lead.next_action_at &&
              new Date(lead.next_action_at).getTime() <= Date.now()}
          >
            {lead.next_action_at ? formatDate(lead.next_action_at, true) : '—'}
          </dd>
          <dt>KVKK</dt>
          <dd>
            {lead.privacy_notice_at
              ? `Bilgilendirildi (${formatDate(lead.privacy_notice_at)})`
              : 'Bilgilendirilmedi'}
          </dd>
        </dl>
        {#if lead.notes}<p class="notes">{lead.notes}</p>{/if}
      </section>

      {#if callable}
        <CallPanel {lead} onSaved={onCallSaved} onDoNotCall={() => (dncOpen = true)} />
      {/if}
      {#key lead.id}
        <OpportunitiesPanel
          leadId={lead.status === 'CONVERTED' ? '' : lead.id}
          partyId={lead.status === 'CONVERTED' ? (lead.converted_party_id ?? '') : ''}
          defaultTitle={lead.display_name}
          {canEdit}
        />
      {/key}
    </div>

    <div class="col">
      <ActivityTimeline
        bind:activities
        create={lead.status === 'CONVERTED' ? undefined : createEntry}
        {canEdit}
        {canManage}
        {userID}
        {members}
      />
    </div>
  </div>
{/if}

<ReasonDialog
  bind:open={dncOpen}
  title="Aranmasın olarak işaretle"
  description="Aday bütün arama akışlarından çıkar. İşareti yalnızca satış yöneticisi kaldırabilir."
  label="Neden"
  placeholder="Örn. Aranmak istemediğini söyledi"
  confirmLabel="İşaretle"
  onConfirm={markDoNotCall}
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
    flex-wrap: wrap;
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
  .panel {
    margin-top: 16px;
    padding: 14px 16px;
  }
  .panel h2 {
    margin: 0 0 10px;
    font-size: 14px;
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
    overflow-wrap: anywhere;
  }
  dd a {
    color: var(--primary);
  }
  .notes {
    margin: 12px 0 0;
    padding-top: 10px;
    border-top: 1px dashed var(--border);
    font-size: 13px;
    white-space: pre-wrap;
  }
  .overdue {
    color: var(--danger);
    font-weight: 600;
  }
  .called-banner {
    display: grid;
    gap: 2px;
    font-size: 13px;
    background: color-mix(in srgb, var(--warning) 12%, var(--surface));
  }
  .called-banner span {
    color: var(--text-muted);
    font-size: 12px;
  }
  .dnc-banner {
    display: flex;
    align-items: center;
    gap: 12px;
    background: color-mix(in srgb, var(--danger) 10%, var(--surface));
    color: var(--text);
  }
  .dnc-banner div {
    display: grid;
    flex: 1;
    font-size: 13px;
  }
  .dnc-banner span {
    color: var(--text-muted);
    font-size: 12px;
  }
  .form-card {
    padding: 0;
    overflow: hidden;
  }
  .form-head {
    padding: 14px 16px;
    border-bottom: 1px solid var(--border);
  }
  .form-head h2 {
    margin: 0;
  }
  .form-body {
    padding: 16px;
  }
  .form-foot {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
    padding: 12px 16px;
    border-top: 1px solid var(--border);
  }
  .form-card .notice {
    margin: 0 16px 12px;
  }
  .state {
    margin: 0;
    padding: 12px 0;
    color: var(--text-muted);
    font-size: 13px;
    text-align: center;
  }
</style>
