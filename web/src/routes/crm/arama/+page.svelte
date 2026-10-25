<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { page } from '$app/state';
  import { goto } from '$app/navigation';
  import { ArrowLeft, Headset, LoaderCircle, SkipForward } from '@lucide/svelte';
  import { api, APIRequestError, type Session } from '$lib/api';
  import { Button } from '$lib/components/ui/button';
  import ReasonDialog from '$lib/components/varya/reason-dialog/ReasonDialog.svelte';
  import { formatDate, formatRelative } from '$lib/design/formatters';
  import {
    claimNextCall,
    listCallLists,
    listLeadActivities,
    releaseCallItem,
    setLeadDoNotCall
  } from '$lib/features/crm/api';
  import ActivityTimeline from '$lib/features/crm/ActivityTimeline.svelte';
  import CallPanel from '$lib/features/crm/CallPanel.svelte';
  import {
    callOutcomeLabel,
    callOutcomeTone,
    leadStatusLabel,
    priorityLabel,
    websiteHref,
    type Activity,
    type CallList,
    type CallResult,
    type NextCall
  } from '$lib/features/crm/types';

  let session = $state<Session | null>(null);
  let lists = $state<CallList[]>([]);
  let loading = $state(true);
  let denied = $state(false);
  let error = $state('');
  let message = $state('');

  let current = $state<NextCall | null>(null);
  let activities = $state<Activity[]>([]);
  let claiming = $state(false);
  let finished = $state(false);
  let callsThisSession = $state(0);
  let dncOpen = $state(false);

  const listID = $derived(page.url.searchParams.get('liste') ?? '');
  const canEdit = $derived(session?.permissions.includes('crm.edit') ?? false);

  async function loadLists() {
    try {
      lists = (await listCallLists()).items;
    } catch (cause) {
      error = cause instanceof APIRequestError ? cause.message : 'Arama listeleri yüklenemedi.';
    }
  }

  async function next() {
    if (!listID || claiming) return;
    claiming = true;
    error = '';
    try {
      const result = await claimNextCall(listID);
      if ('empty' in result) {
        current = null;
        activities = [];
        finished = true;
        return;
      }
      finished = false;
      current = result;
      activities = (await listLeadActivities(result.lead.id)).items;
    } catch (cause) {
      error = cause instanceof APIRequestError ? cause.message : 'Sıradaki aday alınamadı.';
    } finally {
      claiming = false;
    }
  }

  async function onCallSaved(result: CallResult, goNext: boolean) {
    callsThisSession += 1;
    message = `${result.lead.display_name}: ${callOutcomeLabel(result.activity.call_outcome ?? '')}.`;
    if (goNext) {
      await next();
    } else if (current) {
      current = { ...current, lead: result.lead };
      activities = [...(result.task ? [result.task] : []), result.activity, ...activities];
    }
  }

  async function skip() {
    if (!current) return;
    try {
      await releaseCallItem(listID, current.item.id, true);
      message = `${current.lead.display_name} listeden atlandı.`;
      await next();
    } catch (cause) {
      error = cause instanceof APIRequestError ? cause.message : 'Aday atlanamadı.';
    }
  }

  async function markDoNotCall(reason: string) {
    if (!current) return;
    await setLeadDoNotCall(current.lead.id, true, reason);
    message = `${current.lead.display_name} “aranmasın” olarak işaretlendi.`;
    await next();
  }

  // Leaving the screen gives the held lead back to the team straight away
  // instead of waiting for the claim to expire.
  async function leave() {
    if (current) {
      const item = current.item;
      current = null;
      await releaseCallItem(listID, item.id, false).catch(() => {});
    }
    await goto('/crm/arama');
  }

  let startedFor = '';
  $effect(() => {
    if (listID && listID !== startedFor && session) {
      startedFor = listID;
      callsThisSession = 0;
      void next();
    }
  });

  onMount(async () => {
    try {
      session = await api<Session>('/session');
      denied = !session.permissions.includes('crm.read');
    } catch {
      denied = true;
    }
    if (!denied) await loadLists();
    loading = false;
  });

  onDestroy(() => {
    if (current && listID) void releaseCallItem(listID, current.item.id, false).catch(() => {});
  });
</script>

<svelte:head><title>Arama · Varya One</title></svelte:head>

{#if denied}
  <section class="panel error-panel" role="alert">
    <strong>Erişim yok.</strong>
    <span>CRM yetkiniz bulunmuyor.</span>
  </section>
{:else if !listID}
  <header class="page-header">
    <div>
      <h1>Arama</h1>
      <p class="meta">Bir arama listesi seçin; adaylar sırayla önünüze gelir.</p>
    </div>
    <div class="page-actions">
      <Button variant="outline" onclick={() => goto('/crm/listeler')}>Arama listeleri</Button>
    </div>
  </header>
  {#if error}<p class="notice error" role="alert">{error}</p>{/if}
  {#if loading}
    <p class="state" role="status">Yükleniyor…</p>
  {:else if !lists.length}
    <section class="panel empty">
      <Headset size={22} aria-hidden="true" />
      <p>
        Henüz arama listesi yok. Satış yöneticiniz bir liste oluşturup aday eklediğinde burada
        görünür.
      </p>
    </section>
  {:else}
    <div class="lists">
      {#each lists as list (list.id)}
        <button
          type="button"
          class="list-card panel"
          disabled={!canEdit || list.due === 0}
          onclick={() => goto(`/crm/arama?liste=${list.id}`)}
        >
          <strong>{list.name}</strong>
          {#if list.description}<span class="desc">{list.description}</span>{/if}
          <span class="counts">
            <b>{list.due}</b> aranacak · {list.waiting} bekleyen · {list.done} tamamlandı
          </span>
          {#if list.script_name}<span class="desc">Metin: {list.script_name}</span>{/if}
        </button>
      {/each}
    </div>
  {/if}
{:else}
  <header class="page-header">
    <div>
      <button type="button" class="back" onclick={() => void leave()}>
        <ArrowLeft size={14} aria-hidden="true" /> Listeler
      </button>
      <h1>{current?.list.name ?? lists.find((l) => l.id === listID)?.name ?? 'Arama'}</h1>
      <p class="meta">
        Bu oturumda {callsThisSession} arama{#if current}
          · {current.remaining} aday sırada{/if}
      </p>
    </div>
    {#if current}
      <div class="page-actions">
        <Button variant="outline" onclick={() => void skip()}>
          <SkipForward size={14} data-icon="inline-start" aria-hidden="true" />Atla
        </Button>
        <Button variant="outline" onclick={() => goto(`/crm/adaylar/${current?.lead.id}`)}
          >Kartı aç</Button
        >
      </div>
    {/if}
  </header>

  {#if error}<p class="notice error" role="alert">{error}</p>{/if}
  {#if message}<p class="notice success" role="status">{message}</p>{/if}

  {#if claiming && !current}
    <p class="state" role="status"><LoaderCircle size={14} aria-hidden="true" /> Sıradaki aday…</p>
  {:else if finished}
    <section class="panel empty">
      <Headset size={22} aria-hidden="true" />
      <p>Şu an aranacak aday kalmadı. Tekrar denenecek adaylar zamanı gelince sıraya döner.</p>
      <div class="empty-actions">
        <Button variant="outline" onclick={() => void next()}>Tekrar bak</Button>
        <Button onclick={() => void leave()}>Listelere dön</Button>
      </div>
    </section>
  {:else if current}
    <div class="layout">
      <div class="col">
        <section class="panel lead-card" aria-labelledby="lead-title">
          <div class="lead-head">
            <h2 id="lead-title">{current.lead.display_name}</h2>
            <span class="badge">{leadStatusLabel(current.lead.status)}</span>
            {#if current.lead.priority}<span class="badge"
                >{priorityLabel(current.lead.priority)}</span
              >{/if}
          </div>
          <dl>
            <dt>Yetkili</dt>
            <dd>
              {current.lead.contact_name || '—'}{current.lead.contact_title
                ? ` · ${current.lead.contact_title}`
                : ''}
            </dd>
            <dt>Telefon</dt>
            <dd class="phone">
              {current.lead.phone || '—'}{current.lead.phone2 ? ` · ${current.lead.phone2}` : ''}
            </dd>
            <dt>Konum</dt>
            <dd>
              {[current.lead.district, current.lead.city].filter(Boolean).join(', ') || '—'}
            </dd>
            {#if current.lead.address}
              <dt>Adres</dt>
              <dd>{current.lead.address}</dd>
            {/if}
            <dt>Web sitesi</dt>
            <dd>
              {#if current.lead.website}
                <a
                  href={websiteHref(current.lead.website)}
                  target="_blank"
                  rel="noopener noreferrer">{current.lead.website}</a
                >
              {:else}Yok{/if}
            </dd>
            {#if current.lead.sector}
              <dt>Sektör</dt>
              <dd>{current.lead.sector}</dd>
            {/if}
            <dt>Bu listede</dt>
            <dd>
              {current.item.attempts}. deneme
              {#if current.item.last_outcome}
                · son sonuç
                <span class="pill {callOutcomeTone(current.item.last_outcome)}"
                  >{callOutcomeLabel(current.item.last_outcome)}</span
                >
                {formatRelative(current.item.last_called_at ?? undefined)}
              {/if}
            </dd>
            {#if current.lead.next_action_at}
              <dt>Sözleşilen</dt>
              <dd>{formatDate(current.lead.next_action_at, true)}</dd>
            {/if}
          </dl>
          {#if current.lead.notes}<p class="notes">{current.lead.notes}</p>{/if}
        </section>

        <CallPanel
          lead={current.lead}
          callListItemId={current.item.id}
          onSaved={onCallSaved}
          onDoNotCall={() => (dncOpen = true)}
        />
      </div>
      <div class="col">
        {#if current.script}
          <section class="panel script" aria-labelledby="script-title">
            <h2 id="script-title">{current.script.name}</h2>
            <div class="script-body">{current.script.body}</div>
          </section>
        {/if}
        <ActivityTimeline bind:activities title="Önceki görüşmeler" />
      </div>
    </div>
  {/if}
{/if}

<ReasonDialog
  bind:open={dncOpen}
  title="Aranmasın olarak işaretle"
  description="Aday bütün arama listelerinden çıkar."
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
    padding: 0;
    border: 0;
    background: transparent;
    color: var(--text-muted);
    font-size: 12px;
    cursor: pointer;
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
  .lists {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(min(100%, 280px), 1fr));
    gap: 12px;
  }
  .list-card {
    display: grid;
    gap: 6px;
    text-align: left;
    border: 1px solid var(--border);
    color: var(--text);
    cursor: pointer;
  }
  .list-card:hover:not(:disabled) {
    border-color: var(--primary);
  }
  .list-card:disabled {
    cursor: default;
    opacity: 0.65;
  }
  .desc {
    color: var(--text-muted);
    font-size: 12px;
  }
  .counts {
    font-size: 12px;
  }
  .counts b {
    font-size: 16px;
    color: var(--primary);
  }
  .empty {
    display: grid;
    justify-items: center;
    gap: 10px;
    text-align: center;
    color: var(--text-muted);
    font-size: 13px;
  }
  .empty-actions {
    display: flex;
    gap: 8px;
  }
  .layout {
    display: grid;
    grid-template-columns: minmax(0, 1fr);
    gap: 0 16px;
  }
  @media (min-width: 960px) {
    .layout {
      grid-template-columns: minmax(0, 6fr) minmax(0, 5fr);
    }
  }
  .col {
    min-width: 0;
  }
  .lead-head {
    display: flex;
    align-items: center;
    gap: 10px;
    flex-wrap: wrap;
  }
  .lead-head h2 {
    margin: 0;
    font-size: 18px;
  }
  .badge {
    padding: 2px 8px;
    border-radius: 999px;
    background: color-mix(in srgb, var(--primary) 12%, transparent);
    color: var(--primary);
    font-size: 11px;
    font-weight: 600;
  }
  dl {
    display: grid;
    grid-template-columns: max-content minmax(0, 1fr);
    gap: 6px 14px;
    margin: 12px 0 0;
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
  .phone {
    font-size: 15px;
    font-weight: 600;
    font-variant-numeric: tabular-nums;
  }
  .notes {
    margin: 12px 0 0;
    padding-top: 10px;
    border-top: 1px dashed var(--border);
    font-size: 13px;
    white-space: pre-wrap;
  }
  .script-body {
    max-height: 420px;
    overflow-y: auto;
    font-size: 13px;
    line-height: 1.55;
    white-space: pre-wrap;
  }
  .pill {
    display: inline-block;
    padding: 1px 7px;
    border-radius: 999px;
    font-size: 11px;
    font-weight: 600;
    background: color-mix(in srgb, var(--text-muted) 14%, transparent);
  }
  .pill.positive {
    background: color-mix(in srgb, var(--success) 16%, transparent);
    color: var(--success);
  }
  .pill.negative {
    background: color-mix(in srgb, var(--danger) 14%, transparent);
    color: var(--danger);
  }
  .pill.pending {
    background: color-mix(in srgb, var(--warning) 16%, transparent);
    color: var(--warning);
  }
  .state {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 6px;
    margin: 0;
    padding: 16px 0;
    color: var(--text-muted);
    font-size: 13px;
  }
</style>
