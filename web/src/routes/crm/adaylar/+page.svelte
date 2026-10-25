<script lang="ts">
  import { onMount } from 'svelte';
  import { goto, replaceState } from '$app/navigation';
  import { page } from '$app/state';
  import {
    FileUp,
    Headset,
    PhoneOutgoing,
    ListPlus,
    LoaderCircle,
    Phone,
    Plus,
    RefreshCw,
    Search,
    X
  } from '@lucide/svelte';
  import { api, APIRequestError, type Session } from '$lib/api';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { formatDate, formatRelative } from '$lib/design/formatters';
  import {
    addLeadsToList,
    createLead,
    findDuplicateLeads,
    listCallLists,
    listCRMMembers,
    listLeads
  } from '$lib/features/crm/api';
  import LeadFields from '$lib/features/crm/LeadFields.svelte';
  import {
    callOutcomeLabel,
    callOutcomeTone,
    describeAddResult,
    leadContextFromParams,
    leadContextQuery,
    withLeadContext,
    emptyLeadInput,
    fromLocalInput,
    leadPriorities,
    leadStatuses,
    leadStatusLabel,
    priorityLabel,
    telHref,
    type Lead,
    type LeadInput,
    type LeadSummary,
    type CallList,
    type LeadListContext,
    type LeadSort,
    type LeadView,
    type Member
  } from '$lib/features/crm/types';

  type QuickView = LeadView;

  // Filters start from the URL, so "Adaylar" from a lead card comes back to
  // the same list.
  const initial = leadContextFromParams(page.url.searchParams);

  let permissions = $state<string[]>([]);
  let loading = $state(true);
  let denied = $state(false);
  let error = $state('');
  let rows = $state<Lead[]>([]);
  let nextCursor = $state<string | undefined>();
  let loadingMore = $state(false);
  let search = $state(initial.q);
  let statusFilter = $state(initial.status);
  let priorityFilter = $state(initial.priority);
  // Leads that said they are not interested or asked not to be called are
  // hidden by default; the work list should only show who can still be called.
  let hideClosed = $state(initial.hideClosed);
  let cityFilter = $state(initial.city);
  let sourceFilter = $state(initial.source);
  let view = $state<QuickView>(initial.view);
  let sortOrder = $state<LeadSort>(initial.sort);

  const context = $derived<LeadListContext>({
    q: search,
    status: statusFilter,
    priority: priorityFilter,
    city: cityFilter,
    source: sourceFilter,
    view,
    hideClosed,
    sort: sortOrder
  });
  const leadHref = (id: string) => withLeadContext(`/crm/adaylar/${id}`, context);
  let starting = $state(false);

  /** Opens the first callable lead of the list as filtered and sorted now. */
  async function startCalling() {
    if (starting) return;
    starting = true;
    error = '';
    try {
      const first = await listLeads({ ...leadContextQuery(context), callable: true, limit: 1 });
      if (first.items.length) await goto(leadHref(first.items[0].id));
      else error = 'Bu filtrede aranabilecek aday yok.';
    } catch (cause) {
      error = cause instanceof APIRequestError ? cause.message : 'Liste başlatılamadı.';
    } finally {
      starting = false;
    }
  }
  let members = $state<Member[]>([]);

  let creating = $state(false);
  let saving = $state(false);
  let createError = $state('');
  let form = $state<LeadInput>(emptyLeadInput());
  let duplicates = $state<LeadSummary[]>([]);
  let duplicateConfirmed = $state(false);

  const canEdit = $derived(permissions.includes('crm.edit'));
  const canManage = $derived(permissions.includes('crm.manage'));

  // --- arama listesine ekleme (satış yöneticisi) ---
  let callLists = $state<CallList[]>([]);
  let targetList = $state(page.url.searchParams.get('liste') ?? '');
  let selected = $state<string[]>([]);
  let adding = $state(false);
  let addMessage = $state('');

  function currentFilter() {
    return {
      q: search.trim() || undefined,
      status: statusFilter || undefined,
      priority: priorityFilter || undefined,
      hide_closed: hideClosed,
      city: cityFilter.trim() || undefined,
      source: sourceFilter.trim() || undefined,
      owner: view === 'mine' ? 'me' : view === 'pool' ? 'none' : undefined,
      due: view === 'due',
      never_called: view === 'fresh',
      do_not_call: view === 'dnc' ? 'only' : 'exclude'
    };
  }
  // Leads that were already called stay out of a list unless chosen on purpose.
  let includeCalled = $state(false);

  async function addToList(all: boolean) {
    if (!targetList || adding) return;
    adding = true;
    addMessage = '';
    error = '';
    try {
      const result = await addLeadsToList(
        targetList,
        all
          ? { filter: currentFilter(), include_called: includeCalled }
          : { lead_ids: selected, include_called: includeCalled }
      );
      const name = callLists.find((l) => l.id === targetList)?.name ?? 'liste';
      addMessage = `“${name}”: ${describeAddResult(result)}`;
      selected = [];
    } catch (cause) {
      error = cause instanceof APIRequestError ? cause.message : 'Adaylar listeye eklenemedi.';
    } finally {
      adding = false;
    }
  }

  const views: { key: QuickView; label: string }[] = [
    { key: 'all', label: 'Tümü' },
    { key: 'fresh', label: 'Hiç aranmamış' },
    { key: 'mine', label: 'Bana ait' },
    { key: 'pool', label: 'Atanmamış' },
    { key: 'due', label: 'Araması gelen' },
    { key: 'dnc', label: 'Aranmasın' }
  ];

  async function loadSession() {
    try {
      const session = await api<Session>('/session');
      permissions = session?.permissions ?? [];
      denied = !permissions.includes('crm.read');
    } catch {
      denied = true;
    }
  }

  async function load(reset = true) {
    if (denied) return;
    if (reset) {
      loading = true;
      selected = [];
      nextCursor = undefined;
    } else {
      loadingMore = true;
    }
    error = '';
    try {
      if (reset) {
        const url = new URL(withLeadContext('/crm/adaylar', context), page.url.origin);
        if (targetList) url.searchParams.set('liste', targetList);
        replaceState(url, {});
      }
      const result = await listLeads({
        ...leadContextQuery(context),
        cursor: reset ? undefined : nextCursor,
        limit: 50
      });
      rows = reset ? result.items : [...rows, ...result.items];
      nextCursor = result.next_cursor;
    } catch (cause) {
      error = cause instanceof APIRequestError ? cause.message : 'Adaylar yüklenemedi.';
    } finally {
      loading = false;
      loadingMore = false;
    }
  }

  function setView(next: QuickView) {
    view = next;
    void load();
  }

  function openCreate() {
    form = emptyLeadInput();
    duplicates = [];
    duplicateConfirmed = false;
    createError = '';
    creating = true;
  }

  async function checkDuplicates() {
    duplicateConfirmed = false;
    if (form.phone.replace(/\D/g, '').length < 7) {
      duplicates = [];
      return;
    }
    try {
      duplicates = (await findDuplicateLeads(form.phone)).items;
    } catch {
      duplicates = [];
    }
  }

  async function submitCreate(event: SubmitEvent) {
    event.preventDefault();
    if (saving) return;
    if (!form.display_name.trim()) {
      createError = 'Firma ya da kişi adı zorunludur.';
      return;
    }
    if (!form.phone.trim() && !form.phone2.trim() && !form.email.trim()) {
      createError = 'Telefon ya da e-posta girin.';
      return;
    }
    if (duplicates.some((d) => d.do_not_call) && !duplicateConfirmed) {
      createError =
        'Bu numara daha önce "aranmasın" olarak işaretlenmiş. Yine de kaydetmek için tekrar Kaydet’e basın.';
      duplicateConfirmed = true;
      return;
    }
    saving = true;
    createError = '';
    try {
      const created = await createLead({
        ...form,
        next_action_at: fromLocalInput(form.next_action_at),
        allow_duplicate: duplicateConfirmed || duplicates.length > 0
      });
      creating = false;
      await goto(`/crm/adaylar/${created.id}`);
    } catch (cause) {
      if (cause instanceof APIRequestError && cause.code === 'CRM_LEAD_DUPLICATE_PHONE') {
        duplicates = (cause.details.matches as LeadSummary[] | undefined) ?? [];
        duplicateConfirmed = true;
        createError =
          'Bu numara başka bir adayda kayıtlı. Yine de kaydetmek için tekrar Kaydet’e basın.';
      } else {
        createError = cause instanceof APIRequestError ? cause.message : 'Aday kaydedilemedi.';
      }
    } finally {
      saving = false;
    }
  }

  onMount(async () => {
    await loadSession();
    if (denied) {
      loading = false;
      return;
    }
    await Promise.all([
      load(),
      listCRMMembers()
        .then((r) => (members = r.items))
        .catch(() => (members = [])),
      canManage
        ? listCallLists()
            .then((r) => (callLists = r.items))
            .catch(() => (callLists = []))
        : Promise.resolve()
    ]);
  });
</script>

<svelte:head><title>Adaylar · Varya One</title></svelte:head>

{#if denied}
  <section class="panel error-panel" role="alert">
    <strong>Erişim yok.</strong>
    <span>Adayları görüntüleme yetkiniz bulunmuyor.</span>
  </section>
{:else}
  <header class="page-header">
    <div>
      <h1>Adaylar</h1>
      <p class="meta">Henüz cari olmayan potansiyel müşteriler ve arama takibi.</p>
    </div>
    <div class="page-actions">
      <Button variant="outline" onclick={() => void load()} disabled={loading}>
        <RefreshCw size={14} data-icon="inline-start" aria-hidden="true" />Yenile
      </Button>
      {#if canEdit}
        <Button onclick={() => void startCalling()} disabled={starting || loading}>
          <PhoneOutgoing size={14} data-icon="inline-start" aria-hidden="true" />Bu listeyi ara
        </Button>
        <Button variant="outline" onclick={() => goto('/crm/adaylar/ice-aktar')}>
          <FileUp size={14} data-icon="inline-start" aria-hidden="true" />İçe aktar
        </Button>
        <Button variant="outline" onclick={openCreate}>
          <Plus size={14} data-icon="inline-start" aria-hidden="true" />Yeni Aday
        </Button>
      {/if}
    </div>
  </header>

  {#if creating}
    <section class="panel form-card" aria-labelledby="lead-create-title">
      <div class="form-head">
        <div class="form-head-title">
          <span class="form-head-icon"><Headset size={16} aria-hidden="true" /></span>
          <div>
            <h2 id="lead-create-title">Yeni aday</h2>
            <p>Aranacak bir firma ya da kişi ekleyin.</p>
          </div>
        </div>
        <button type="button" class="icon-x" onclick={() => (creating = false)} aria-label="Kapat">
          <X size={16} aria-hidden="true" />
        </button>
      </div>
      <form onsubmit={submitCreate}>
        <div class="form-body">
          <LeadFields
            bind:value={form}
            {members}
            canAssign={canManage}
            showStatus={false}
            {duplicates}
            onPhoneBlur={() => void checkDuplicates()}
          />
        </div>
        {#if createError}<p class="notice error" role="alert">{createError}</p>{/if}
        <div class="form-foot">
          <Button variant="ghost" type="button" onclick={() => (creating = false)}>Vazgeç</Button>
          <Button type="submit" disabled={saving}>
            {#if saving}<LoaderCircle data-icon="inline-start" aria-hidden="true" /> Kaydediliyor…{:else}Kaydet{/if}
          </Button>
        </div>
      </form>
    </section>
  {/if}

  <section class="panel filters">
    <div class="views" role="tablist" aria-label="Hızlı görünüm">
      {#each views as item (item.key)}
        <button
          type="button"
          role="tab"
          aria-selected={view === item.key}
          class:active={view === item.key}
          onclick={() => setView(item.key)}>{item.label}</button
        >
      {/each}
    </div>
    <form
      class="search"
      onsubmit={(event) => {
        event.preventDefault();
        void load();
      }}
    >
      <span class="search-icon"><Search size={15} aria-hidden="true" /></span>
      <Input bind:value={search} placeholder="Ad, yetkili, telefon veya e-posta" aria-label="Ara" />
      <select
        bind:value={statusFilter}
        onchange={() => void load()}
        class="select"
        aria-label="Durum"
      >
        <option value="">Tüm durumlar</option>
        {#each leadStatuses as status (status)}
          <option value={status}>{leadStatusLabel(status)}</option>
        {/each}
      </select>
      <select
        bind:value={priorityFilter}
        onchange={() => void load()}
        class="select"
        aria-label="Öncelik"
      >
        <option value="">Tüm öncelikler</option>
        {#each leadPriorities as priority (priority)}
          <option value={priority}>{priorityLabel(priority)}</option>
        {/each}
        <option value="none">Derecesiz</option>
      </select>
      <select
        bind:value={sortOrder}
        onchange={() => void load()}
        class="select"
        aria-label="Sıralama"
      >
        <option value="priority">Önce sıcaklar (A→E)</option>
        <option value="newest">En yeni eklenen</option>
        <option value="name">Ada göre</option>
      </select>
      <Input class="narrow-input" bind:value={cityFilter} placeholder="İl" aria-label="İl" />
      <Input
        class="narrow-input"
        bind:value={sourceFilter}
        placeholder="Kaynak"
        aria-label="Kaynak"
      />
      <Button type="submit" variant="outline">Ara</Button>
    </form>
    <label class="hide-closed">
      <input type="checkbox" bind:checked={hideClosed} onchange={() => void load()} />
      İlgilenmeyenleri ve aranmasınları gizle
    </label>
  </section>

  {#if error}<p class="notice error" role="alert">{error}</p>{/if}
  {#if addMessage}<p class="notice success" role="status">{addMessage}</p>{/if}

  {#if canManage && callLists.length}
    <section class="panel add-bar" aria-label="Arama listesine ekle">
      <ListPlus size={15} aria-hidden="true" />
      <select class="select" bind:value={targetList} aria-label="Arama listesi">
        <option value="">Arama listesi seçin…</option>
        {#each callLists as list (list.id)}
          <option value={list.id}>{list.name}</option>
        {/each}
      </select>
      <Button
        size="sm"
        variant="outline"
        disabled={!targetList || !selected.length || adding}
        onclick={() => void addToList(false)}>Seçilen {selected.length} adayı ekle</Button
      >
      <Button
        size="sm"
        variant="outline"
        disabled={!targetList || adding}
        onclick={() => void addToList(true)}>Filtreye uyan tüm adayları ekle</Button
      >
      <label class="deliberate">
        <input type="checkbox" bind:checked={includeCalled} />
        Daha önce aranmışları da bilerek ekle
      </label>
    </section>
  {/if}

  <section class="panel table-panel">
    {#if loading}
      <p class="state" role="status">Yükleniyor…</p>
    {:else if !rows.length}
      <p class="state">Kayıt bulunamadı.</p>
    {:else}
      <!-- svelte-ignore a11y_no_noninteractive_tabindex (a scrollable region must be reachable by keyboard) -->
      <div
        class="table-scroll"
        tabindex="0"
        role="region"
        aria-label="Tablo — yatay kaydırılabilir"
      >
        <table>
          <thead>
            <tr>
              {#if canManage && callLists.length}
                <th>
                  <input
                    type="checkbox"
                    aria-label="Sayfadakilerin tümünü seç"
                    checked={rows.length > 0 && selected.length === rows.length}
                    onchange={(e) =>
                      (selected = e.currentTarget.checked ? rows.map((r) => r.id) : [])}
                  />
                </th>
              {/if}
              <th>Ad</th><th>Öncelik</th><th>Yetkili</th><th>Telefon</th><th>İl / İlçe</th>
              <th>Durum</th><th>Son arama</th>
              <th>Deneme</th><th>Sonraki işlem</th><th>Sorumlu</th>
            </tr>
          </thead>
          <tbody>
            {#each rows as lead (lead.id)}
              <tr onclick={() => goto(leadHref(lead.id))} class="row">
                {#if canManage && callLists.length}
                  <td onclick={(e) => e.stopPropagation()}>
                    <input
                      type="checkbox"
                      aria-label="{lead.display_name} seç"
                      checked={selected.includes(lead.id)}
                      onchange={(e) =>
                        (selected = e.currentTarget.checked
                          ? [...selected, lead.id]
                          : selected.filter((id) => id !== lead.id))}
                    />
                  </td>
                {/if}
                <td class="name">
                  <a href={leadHref(lead.id)} onclick={(e) => e.stopPropagation()}
                    >{lead.display_name}</a
                  >
                  {#if lead.do_not_call}<span class="pill negative">Aranmasın</span>{/if}
                </td>
                <td>
                  {#if lead.priority}<span class="grade grade-{lead.priority}">{lead.priority}</span
                    >{:else}—{/if}
                </td>
                <td>{lead.contact_name || '—'}</td>
                <td>
                  {#if lead.phone && !lead.do_not_call}
                    <a
                      class="tel"
                      href={telHref(lead.phone)}
                      onclick={(e) => e.stopPropagation()}
                      aria-label="{lead.display_name} ara"
                      ><Phone size={12} aria-hidden="true" />{lead.phone}</a
                    >
                  {:else}
                    {lead.phone || '—'}
                  {/if}
                </td>
                <td>{[lead.city, lead.district].filter(Boolean).join(' / ') || '—'}</td>
                <td>{leadStatusLabel(lead.status)}</td>
                <td>
                  {#if lead.last_call_outcome}
                    <span class="pill {callOutcomeTone(lead.last_call_outcome)}"
                      >{callOutcomeLabel(lead.last_call_outcome)}</span
                    >
                    <span class="muted">{formatRelative(lead.last_call_at ?? undefined)}</span>
                  {:else}—{/if}
                </td>
                <td>{lead.call_attempts}</td>
                <td
                  class:overdue={lead.next_action_at &&
                    new Date(lead.next_action_at).getTime() <= Date.now()}
                >
                  {lead.next_action_at ? formatDate(lead.next_action_at, true) : '—'}
                </td>
                <td>{lead.owner_name || 'Atanmamış'}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
      {#if nextCursor}
        <div class="more">
          <Button variant="outline" onclick={() => void load(false)} disabled={loadingMore}>
            {loadingMore ? 'Yükleniyor…' : 'Daha fazla'}
          </Button>
        </div>
      {/if}
    {/if}
  </section>
{/if}

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
  .filters,
  .table-panel {
    padding: 12px 14px;
  }
  .filters {
    display: grid;
    gap: 10px;
  }
  .views {
    display: flex;
    gap: 6px;
    flex-wrap: wrap;
  }
  .views button {
    height: 28px;
    padding: 0 12px;
    border: 1px solid var(--border-strong);
    border-radius: 999px;
    background: var(--surface);
    color: var(--text);
    font-size: 12px;
    cursor: pointer;
  }
  .views button.active {
    border-color: var(--primary);
    background: color-mix(in srgb, var(--primary) 12%, transparent);
    color: var(--primary);
    font-weight: 600;
  }
  .form-card {
    overflow: hidden;
  }
  .form-head {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 12px;
    padding: 14px 16px;
    border-bottom: 1px solid var(--border);
    background: var(--surface-muted, var(--surface));
  }
  .form-head-title {
    display: flex;
    gap: 10px;
    align-items: flex-start;
  }
  .form-head-icon {
    display: grid;
    place-items: center;
    width: 30px;
    height: 30px;
    border-radius: 8px;
    background: color-mix(in srgb, var(--primary) 12%, transparent);
    color: var(--primary);
    flex-shrink: 0;
  }
  .form-head h2 {
    margin: 0;
    font-size: 15px;
  }
  .form-head p {
    margin: 2px 0 0;
    color: var(--text-muted);
    font-size: 12px;
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
  .icon-x:hover {
    background: var(--surface-hover, rgba(0, 0, 0, 0.05));
    color: var(--text);
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
    background: var(--surface-muted, var(--surface));
  }
  .form-card .notice {
    margin: 0 16px 12px;
  }
  .select {
    height: var(--control-height);
    box-sizing: border-box;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    background: var(--surface);
    color: var(--text);
    padding: 0 10px;
    font-size: 13px;
  }
  .hide-closed {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 12px;
    color: var(--text-muted);
    cursor: pointer;
  }
  .add-bar {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
    padding: 10px 14px;
  }
  .deliberate {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 12px;
    color: var(--warning);
    cursor: pointer;
  }
  .add-bar .select {
    width: auto;
    min-width: 200px;
  }
  .search {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
  }
  .search :global(.narrow-input) {
    width: 110px;
  }
  .grade {
    display: inline-grid;
    place-items: center;
    width: 22px;
    height: 22px;
    border-radius: 6px;
    font-weight: 700;
    font-size: 11px;
    background: color-mix(in srgb, var(--text-muted) 14%, transparent);
  }
  .grade-A {
    background: color-mix(in srgb, var(--danger) 16%, transparent);
    color: var(--danger);
  }
  .grade-B {
    background: color-mix(in srgb, var(--warning) 18%, transparent);
    color: var(--warning);
  }
  .grade-C {
    background: color-mix(in srgb, var(--primary) 14%, transparent);
    color: var(--primary);
  }
  .search-icon {
    color: var(--text-muted);
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
  .row {
    cursor: pointer;
  }
  .row:hover {
    background: var(--surface-hover, rgba(0, 0, 0, 0.03));
  }
  .name a {
    color: var(--text);
    font-weight: 600;
    text-decoration: none;
  }
  .tel {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    color: var(--primary);
    text-decoration: none;
  }
  .muted {
    color: var(--text-muted);
    margin-left: 4px;
  }
  .overdue {
    color: var(--danger);
    font-weight: 600;
  }
  .pill {
    display: inline-block;
    padding: 1px 7px;
    border-radius: 999px;
    font-size: 11px;
    font-weight: 600;
    background: color-mix(in srgb, var(--text-muted) 14%, transparent);
    color: var(--text);
  }
  .pill.positive {
    background: color-mix(in srgb, var(--success) 16%, transparent);
    color: var(--success);
  }
  .name .pill {
    margin-left: 6px;
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
    margin: 0;
    padding: 16px 0;
    color: var(--text-muted);
    font-size: 13px;
    text-align: center;
  }
  .more {
    margin-top: 12px;
    display: flex;
    justify-content: center;
  }
</style>
