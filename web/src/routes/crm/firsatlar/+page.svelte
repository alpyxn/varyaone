<script lang="ts">
  import { DateInput } from '$lib/components/varya/date-input';
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/state';
  import { LoaderCircle, Plus, RefreshCw, Search, Settings2, X } from '@lucide/svelte';
  import { api, APIRequestError, type Session } from '$lib/api';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import * as Field from '$lib/components/ui/field';
  import ReasonDialog from '$lib/components/varya/reason-dialog/ReasonDialog.svelte';
  import { formatDate, formatMoney } from '$lib/design/formatters';
  import { SUPPORTED_CURRENCIES } from '$lib/design/currencies';
  import { listParties } from '$lib/features/parties/api';
  import {
    createOpportunity,
    listLeads,
    listOpportunities,
    listStages,
    moveOpportunity
  } from '$lib/features/crm/api';
  import { sumByCurrency, type Opportunity, type Stage } from '$lib/features/crm/types';

  type Tab = 'open' | 'won' | 'lost';
  type Target = { kind: 'lead' | 'party'; id: string; name: string };

  let session = $state<Session | null>(null);
  let denied = $state(false);
  let loading = $state(true);
  let error = $state('');
  let message = $state('');
  let stages = $state<Stage[]>([]);
  let items = $state<Opportunity[]>([]);
  let tab = $state<Tab>('open');
  let mine = $state(false);
  let search = $state('');

  const permissions = $derived(session?.permissions ?? []);
  const canEdit = $derived(permissions.includes('crm.edit'));
  const canManage = $derived(permissions.includes('crm.manage'));
  const canSearchParties = $derived(
    Boolean(session?.modules.includes('preaccounting') && permissions.includes('party.read'))
  );
  const openStages = $derived(stages.filter((s) => s.kind === 'OPEN' && s.is_active));
  const closingStages = $derived(stages.filter((s) => s.kind !== 'OPEN' && s.is_active));
  const columns = $derived(
    openStages.map((stage) => ({
      stage,
      items: items.filter((item) => item.stage_id === stage.id)
    }))
  );
  const boardTotals = $derived(sumByCurrency(items));

  async function load() {
    loading = true;
    error = '';
    try {
      const [loadedStages, loaded] = await Promise.all([
        listStages(),
        listOpportunities({
          state: tab,
          owner: mine ? 'me' : undefined,
          q: search.trim() || undefined
        })
      ]);
      stages = loadedStages.items;
      items = loaded.items;
    } catch (cause) {
      error = cause instanceof APIRequestError ? cause.message : 'Fırsatlar yüklenemedi.';
    } finally {
      loading = false;
    }
  }

  // --- taşıma ---
  let dragging = $state<string | null>(null);
  let dropTarget = $state<string | null>(null);
  let lostMove = $state<{ item: Opportunity; stage: Stage } | null>(null);
  let lostOpen = $state(false);

  async function move(item: Opportunity, stage: Stage, lostReason = '') {
    if (item.stage_id === stage.id) return;
    if (stage.kind === 'LOST' && !lostReason) {
      lostMove = { item, stage };
      lostOpen = true;
      return;
    }
    error = '';
    try {
      const updated = await moveOpportunity(item.id, item.version, stage.id, lostReason);
      items =
        updated.stage_kind === 'OPEN'
          ? items.map((i) => (i.id === updated.id ? updated : i))
          : items.filter((i) => i.id !== updated.id);
      if (updated.stage_kind === 'WON') message = `“${updated.title}” kazanıldı.`;
      if (updated.stage_kind === 'LOST') message = `“${updated.title}” kaybedildi olarak kapandı.`;
    } catch (cause) {
      error = cause instanceof APIRequestError ? cause.message : 'Fırsat taşınamadı.';
      await load();
    }
  }

  function onDrop(stage: Stage) {
    const item = items.find((i) => i.id === dragging);
    dragging = null;
    dropTarget = null;
    if (item) void move(item, stage);
  }

  // --- yeni fırsat ---
  let creating = $state(false);
  let saving = $state(false);
  let createError = $state('');
  let form = $state({
    title: '',
    amount: '',
    currency: 'TRY',
    expected_close_date: '',
    stage_id: ''
  });
  let target = $state<Target | null>(null);
  let targetQuery = $state('');
  let targetResults = $state<Target[]>([]);

  function openCreate() {
    form = {
      title: '',
      amount: '',
      currency: 'TRY',
      expected_close_date: '',
      stage_id: openStages[0]?.id ?? ''
    };
    target = null;
    targetQuery = '';
    targetResults = [];
    createError = '';
    creating = true;
  }

  let searchTimer: ReturnType<typeof setTimeout> | undefined;
  function searchTargets(term: string) {
    targetQuery = term;
    clearTimeout(searchTimer);
    if (term.trim().length < 2) {
      targetResults = [];
      return;
    }
    searchTimer = setTimeout(async () => {
      const results: Target[] = [];
      try {
        const leads = await listLeads({ q: term.trim(), limit: 8 });
        for (const lead of leads.items) {
          if (lead.status === 'CONVERTED' && lead.converted_party_id) continue;
          results.push({ kind: 'lead', id: lead.id, name: lead.display_name });
        }
      } catch {
        /* lead search is best-effort */
      }
      if (canSearchParties) {
        try {
          const parties = await listParties(new URLSearchParams({ q: term.trim(), limit: '8' }));
          for (const party of parties.items)
            results.push({ kind: 'party', id: party.id, name: party.display_name });
        } catch {
          /* cari search is best-effort */
        }
      }
      targetResults = results;
    }, 250);
  }

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    if (saving) return;
    if (!form.title.trim() || !target) {
      createError = 'Başlık girin ve bir aday ya da cari seçin.';
      return;
    }
    saving = true;
    createError = '';
    try {
      const created = await createOpportunity({
        title: form.title.trim(),
        lead_id: target.kind === 'lead' ? target.id : undefined,
        party_id: target.kind === 'party' ? target.id : undefined,
        stage_id: form.stage_id,
        amount: form.amount.trim() || '0',
        currency: form.currency,
        expected_close_date: form.expected_close_date,
        owner_user_id: '',
        notes: ''
      });
      await goto(`/crm/firsatlar/${created.id}`);
    } catch (cause) {
      createError = cause instanceof APIRequestError ? cause.message : 'Fırsat oluşturulamadı.';
    } finally {
      saving = false;
    }
  }

  onMount(async () => {
    try {
      session = await api<Session>('/session');
      denied = !session.permissions.includes('crm.read');
    } catch {
      denied = true;
    }
    if (denied) {
      loading = false;
      return;
    }
    await load();
    if (page.url.searchParams.get('yeni') === '1' && canEdit) openCreate();
  });
</script>

<svelte:head><title>Fırsatlar · Varya One</title></svelte:head>

{#if denied}
  <section class="panel error-panel" role="alert">
    <strong>Erişim yok.</strong>
    <span>CRM yetkiniz bulunmuyor.</span>
  </section>
{:else}
  <header class="page-header">
    <div>
      <h1>Fırsatlar</h1>
      <p class="meta">
        {items.length} fırsat{#each boardTotals as total (total.currency)}
          · {formatMoney(total.amount, total.currency)}{/each}
      </p>
    </div>
    <div class="page-actions">
      {#if canManage}
        <Button variant="outline" onclick={() => goto('/crm/asamalar')}>
          <Settings2 size={14} data-icon="inline-start" aria-hidden="true" />Aşamalar
        </Button>
      {/if}
      <Button variant="outline" onclick={() => void load()} disabled={loading}>
        <RefreshCw size={14} data-icon="inline-start" aria-hidden="true" />Yenile
      </Button>
      {#if canEdit}
        <Button onclick={openCreate}>
          <Plus size={14} data-icon="inline-start" aria-hidden="true" />Yeni Fırsat
        </Button>
      {/if}
    </div>
  </header>

  {#if creating}
    <section class="panel form-card" aria-labelledby="opp-create-title">
      <div class="form-head">
        <h2 id="opp-create-title">Yeni fırsat</h2>
        <button type="button" class="icon-x" onclick={() => (creating = false)} aria-label="Kapat">
          <X size={16} aria-hidden="true" />
        </button>
      </div>
      <form onsubmit={submit}>
        <div class="form-body grid">
          <Field.Field class="full">
            <Field.FieldLabel for="opp-title">Başlık</Field.FieldLabel>
            <Input
              id="opp-title"
              bind:value={form.title}
              maxlength={200}
              placeholder="Ne satılacak?"
              required
            />
          </Field.Field>
          <Field.Field class="full">
            <Field.FieldLabel for="opp-target">Aday ya da cari</Field.FieldLabel>
            {#if target}
              <div class="chosen">
                <span class="tag">{target.kind === 'lead' ? 'Aday' : 'Cari'}</span>
                {target.name}
                <button type="button" class="link" onclick={() => (target = null)}>Değiştir</button>
              </div>
            {:else}
              <Input
                id="opp-target"
                value={targetQuery}
                oninput={(e) => searchTargets(e.currentTarget.value)}
                placeholder={canSearchParties ? 'Aday ya da cari adı' : 'Aday adı'}
                autocomplete="off"
              />
              {#if targetResults.length}
                <ul class="lookup" role="listbox">
                  {#each targetResults as result (result.kind + result.id)}
                    <li>
                      <button
                        type="button"
                        onclick={() => {
                          target = result;
                          targetResults = [];
                          if (!form.title) form.title = result.name;
                        }}
                      >
                        <span class="tag">{result.kind === 'lead' ? 'Aday' : 'Cari'}</span>
                        {result.name}
                      </button>
                    </li>
                  {/each}
                </ul>
              {/if}
            {/if}
          </Field.Field>
          <Field.Field>
            <Field.FieldLabel for="opp-amount">Tahmini tutar</Field.FieldLabel>
            <Input id="opp-amount" bind:value={form.amount} inputmode="decimal" placeholder="0" />
          </Field.Field>
          <Field.Field>
            <Field.FieldLabel for="opp-currency">Para birimi</Field.FieldLabel>
            <select id="opp-currency" class="select" bind:value={form.currency}>
              {#each SUPPORTED_CURRENCIES as currency (currency.code)}
                <option value={currency.code}>{currency.code}</option>
              {/each}
            </select>
          </Field.Field>
          <Field.Field>
            <Field.FieldLabel for="opp-close">Beklenen kapanış</Field.FieldLabel>
            <DateInput
              id="opp-close"
              bind:value={form.expected_close_date}
              ariaLabel="Beklenen kapanış"
            />
          </Field.Field>
          <Field.Field>
            <Field.FieldLabel for="opp-stage">Aşama</Field.FieldLabel>
            <select id="opp-stage" class="select" bind:value={form.stage_id}>
              {#each openStages as stage (stage.id)}
                <option value={stage.id}>{stage.name}</option>
              {/each}
            </select>
          </Field.Field>
        </div>
        {#if createError}<p class="notice error" role="alert">{createError}</p>{/if}
        <div class="form-foot">
          <Button variant="ghost" type="button" onclick={() => (creating = false)}>Vazgeç</Button>
          <Button type="submit" disabled={saving}>
            {#if saving}<LoaderCircle data-icon="inline-start" aria-hidden="true" />{/if}Oluştur
          </Button>
        </div>
      </form>
    </section>
  {/if}

  <section class="panel filters">
    <div class="tabs" role="tablist" aria-label="Fırsat durumu">
      {#each [['open', 'Açık'], ['won', 'Kazanılan'], ['lost', 'Kaybedilen']] as [key, label] (key)}
        <button
          type="button"
          role="tab"
          aria-selected={tab === key}
          class:active={tab === key}
          onclick={() => {
            tab = key as Tab;
            void load();
          }}>{label}</button
        >
      {/each}
    </div>
    <form
      class="search"
      onsubmit={(e) => {
        e.preventDefault();
        void load();
      }}
    >
      <Search size={15} aria-hidden="true" />
      <Input bind:value={search} placeholder="Başlık, aday ya da cari" aria-label="Ara" />
      <label class="check">
        <input type="checkbox" bind:checked={mine} onchange={() => void load()} /> Yalnızca benimkiler
      </label>
    </form>
  </section>

  {#if error}<p class="notice error" role="alert">{error}</p>{/if}
  {#if message}<p class="notice success" role="status">{message}</p>{/if}

  {#if loading}
    <p class="state" role="status">Yükleniyor…</p>
  {:else if tab === 'open'}
    <div class="board" aria-label="Fırsat panosu">
      {#each columns as column (column.stage.id)}
        {@const totals = sumByCurrency(column.items)}
        <section
          class="column"
          class:drop={dropTarget === column.stage.id}
          aria-label={column.stage.name}
          ondragover={(e) => {
            if (!dragging) return;
            e.preventDefault();
            dropTarget = column.stage.id;
          }}
          ondragleave={() => (dropTarget = null)}
          ondrop={(e) => {
            e.preventDefault();
            onDrop(column.stage);
          }}
        >
          <header>
            <strong>{column.stage.name}</strong>
            <span class="count">{column.items.length}</span>
            <span class="prob">%{column.stage.probability}</span>
          </header>
          <p class="totals">
            {#each totals as total (total.currency)}<span
                >{formatMoney(total.amount, total.currency)}</span
              >{:else}—{/each}
          </p>
          {#each column.items as item (item.id)}
            <article
              class="card"
              draggable={canEdit}
              ondragstart={() => (dragging = item.id)}
              ondragend={() => {
                dragging = null;
                dropTarget = null;
              }}
            >
              <a class="title" href="/crm/firsatlar/{item.id}">{item.title}</a>
              <span class="who">{item.party_name || item.lead_name}</span>
              <span class="amount">{formatMoney(item.amount, item.currency)}</span>
              <span class="foot">
                {item.owner_name || 'Atanmamış'}
                {#if item.expected_close_date}
                  · <span
                    class:late={new Date(item.expected_close_date).getTime() <
                      Date.now() - 86400000}>{formatDate(item.expected_close_date)}</span
                  >
                {/if}
              </span>
              {#if canEdit}
                <select
                  class="move"
                  aria-label="{item.title} aşamasını değiştir"
                  value={item.stage_id}
                  onchange={(e) => {
                    const stage = stages.find((s) => s.id === e.currentTarget.value);
                    if (stage) void move(item, stage);
                  }}
                >
                  {#each stages.filter((s) => s.is_active) as stage (stage.id)}
                    <option value={stage.id}>{stage.name}</option>
                  {/each}
                </select>
              {/if}
            </article>
          {/each}
        </section>
      {/each}
      {#if canEdit}
        <div class="closing">
          {#each closingStages as stage (stage.id)}
            <section
              class="drop-zone kind-{stage.kind.toLowerCase()}"
              class:drop={dropTarget === stage.id}
              aria-label="{stage.name} — buraya bırakın"
              ondragover={(e) => {
                if (!dragging) return;
                e.preventDefault();
                dropTarget = stage.id;
              }}
              ondragleave={() => (dropTarget = null)}
              ondrop={(e) => {
                e.preventDefault();
                onDrop(stage);
              }}
            >
              {stage.name}
            </section>
          {/each}
        </div>
      {/if}
    </div>
  {:else if !items.length}
    <p class="state">Kayıt yok.</p>
  {:else}
    <section class="panel table-panel">
      <!-- svelte-ignore a11y_no_noninteractive_tabindex (a scrollable region must be reachable by keyboard) -->
      <div class="table-scroll" tabindex="0" role="region" aria-label="Kapanan fırsatlar">
        <table>
          <thead>
            <tr>
              <th>Fırsat</th><th>Aday / cari</th><th>Tutar</th><th>Kapanış</th><th>Sorumlu</th>
              {#if tab === 'lost'}<th>Neden</th>{/if}
            </tr>
          </thead>
          <tbody>
            {#each items as item (item.id)}
              <tr>
                <td><a href="/crm/firsatlar/{item.id}">{item.title}</a></td>
                <td>{item.party_name || item.lead_name}</td>
                <td class="num">{formatMoney(item.amount, item.currency)}</td>
                <td>{formatDate(item.closed_at ?? undefined)}</td>
                <td>{item.owner_name || '—'}</td>
                {#if tab === 'lost'}<td>{item.lost_reason}</td>{/if}
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    </section>
  {/if}
{/if}

<ReasonDialog
  bind:open={lostOpen}
  title="Fırsat neden kaybedildi?"
  description="Kayıp nedenleri raporlarda toplanır."
  label="Neden"
  placeholder="Örn. Fiyat yüksek bulundu"
  confirmLabel="Kaybedildi olarak kapat"
  onConfirm={async (reason) => {
    if (lostMove) await move(lostMove.item, lostMove.stage, reason);
    lostMove = null;
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
    flex-wrap: wrap;
  }
  .meta {
    margin: 4px 0 0;
    color: var(--text-muted);
    font-size: 12px;
  }
  .panel {
    margin-top: 16px;
  }
  .filters {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 10px;
    flex-wrap: wrap;
    padding: 10px 14px;
  }
  .tabs {
    display: flex;
    gap: 6px;
  }
  .tabs button {
    height: 28px;
    padding: 0 12px;
    border: 1px solid var(--border-strong);
    border-radius: 999px;
    background: var(--surface);
    color: var(--text);
    font-size: 12px;
    cursor: pointer;
  }
  .tabs button.active {
    border-color: var(--primary);
    background: color-mix(in srgb, var(--primary) 12%, transparent);
    color: var(--primary);
    font-weight: 600;
  }
  .search {
    display: flex;
    align-items: center;
    gap: 8px;
    color: var(--text-muted);
  }
  .check {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 12px;
    white-space: nowrap;
  }
  .form-card {
    overflow: hidden;
  }
  .form-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 12px 16px;
    border-bottom: 1px solid var(--border);
  }
  .form-head h2 {
    margin: 0;
    font-size: 15px;
  }
  .icon-x {
    display: grid;
    place-items: center;
    width: 28px;
    height: 28px;
    border: 0;
    border-radius: 6px;
    background: transparent;
    color: var(--text-muted);
    cursor: pointer;
  }
  .form-body {
    padding: 16px;
  }
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 200px), 1fr));
    gap: 14px 16px;
  }
  .grid :global(.full) {
    grid-column: 1 / -1;
  }
  .grid :global(input) {
    width: 100%;
    box-sizing: border-box;
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
  .select {
    height: var(--control-height);
    width: 100%;
    box-sizing: border-box;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    background: var(--surface);
    color: var(--text);
    padding: 0 10px;
    font-size: 13px;
  }
  .chosen {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 13px;
  }
  .link {
    border: 0;
    background: transparent;
    color: var(--primary);
    cursor: pointer;
    font-size: 12px;
  }
  .tag {
    padding: 1px 6px;
    border-radius: 4px;
    background: color-mix(in srgb, var(--text-muted) 14%, transparent);
    font-size: 10px;
    font-weight: 600;
    text-transform: uppercase;
  }
  .lookup {
    list-style: none;
    margin: 6px 0 0;
    padding: 0;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    max-height: 220px;
    overflow-y: auto;
  }
  .lookup button {
    display: flex;
    gap: 8px;
    align-items: center;
    width: 100%;
    padding: 8px 10px;
    border: 0;
    background: transparent;
    text-align: left;
    font-size: 12px;
    color: var(--text);
    cursor: pointer;
  }
  .lookup button:hover {
    background: var(--surface-hover, rgba(0, 0, 0, 0.05));
  }
  .board {
    display: flex;
    gap: 12px;
    margin-top: 16px;
    overflow-x: auto;
    padding-bottom: 8px;
    align-items: flex-start;
  }
  .column {
    flex: 0 0 260px;
    display: grid;
    gap: 8px;
    padding: 10px;
    border: 1px solid var(--border);
    border-radius: 10px;
    background: var(--surface-muted, var(--surface));
    min-height: 120px;
  }
  .column.drop,
  .drop-zone.drop {
    border-color: var(--primary);
    background: color-mix(in srgb, var(--primary) 8%, var(--surface));
  }
  .column header {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 13px;
  }
  .count {
    padding: 0 6px;
    border-radius: 999px;
    background: color-mix(in srgb, var(--text-muted) 15%, transparent);
    font-size: 11px;
  }
  .prob {
    margin-left: auto;
    color: var(--text-muted);
    font-size: 11px;
  }
  .totals {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    margin: 0;
    color: var(--text-muted);
    font-size: 11px;
    font-variant-numeric: tabular-nums;
  }
  .card {
    display: grid;
    gap: 3px;
    padding: 9px 10px;
    border: 1px solid var(--border);
    border-radius: 8px;
    background: var(--surface);
    font-size: 12px;
    cursor: grab;
  }
  .card .title {
    color: var(--text);
    font-weight: 600;
    font-size: 13px;
  }
  .who,
  .foot {
    color: var(--text-muted);
  }
  .amount {
    font-weight: 600;
    font-variant-numeric: tabular-nums;
  }
  .late {
    color: var(--danger);
  }
  .move {
    margin-top: 4px;
    height: 24px;
    border: 1px solid var(--border);
    border-radius: 6px;
    background: var(--surface);
    color: var(--text-muted);
    font-size: 11px;
  }
  .closing {
    flex: 0 0 160px;
    display: grid;
    gap: 8px;
  }
  .drop-zone {
    display: grid;
    place-items: center;
    min-height: 90px;
    border: 2px dashed var(--border-strong);
    border-radius: 10px;
    font-size: 13px;
    font-weight: 600;
  }
  .drop-zone.kind-won {
    color: var(--success);
  }
  .drop-zone.kind-lost {
    color: var(--danger);
  }
  .table-panel {
    padding: 12px 14px;
  }
  .table-scroll {
    overflow-x: auto;
  }
  table {
    width: 100%;
    border-collapse: collapse;
    font-size: 12px;
  }
  th,
  td {
    padding: 9px 10px;
    border-bottom: 1px solid var(--border);
    text-align: left;
    white-space: nowrap;
  }
  th {
    color: var(--text-muted);
    font-weight: 650;
  }
  td a {
    color: var(--text);
    font-weight: 600;
  }
  .num {
    text-align: right;
    font-variant-numeric: tabular-nums;
  }
  .state {
    margin: 0;
    padding: 16px 0;
    color: var(--text-muted);
    font-size: 13px;
    text-align: center;
  }
</style>
