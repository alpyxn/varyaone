<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/state';
  import { goto } from '$app/navigation';
  import {
    Archive,
    ArchiveRestore,
    ArrowLeft,
    Headset,
    ListPlus,
    LoaderCircle,
    Pencil,
    RefreshCw,
    Trash2
  } from '@lucide/svelte';
  import { ConfirmDialog } from '$lib/components/varya/confirm-dialog';
  import LeadSelector from '$lib/features/crm/LeadSelector.svelte';
  import { api, APIRequestError, type Session } from '$lib/api';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import * as Field from '$lib/components/ui/field';
  import { formatDate, formatRelative } from '$lib/design/formatters';
  import {
    addLeadsToList,
    archiveCallList,
    deleteCallList,
    getCallList,
    listCRMMembers,
    listCallListItems,
    listScripts,
    removeListItems,
    updateCallList
  } from '$lib/features/crm/api';
  import {
    callItemStatusLabel,
    callOutcomeLabel,
    callOutcomeTone,
    describeAddResult,
    formatMinutes,
    type LeadFilterInput,
    type Member,
    type CallList,
    type CallListItem,
    type CallScript
  } from '$lib/features/crm/types';

  let permissions = $state<string[]>([]);
  let list = $state<CallList | null>(null);
  let items = $state<CallListItem[]>([]);
  let scripts = $state<CallScript[]>([]);
  let statusFilter = $state('');
  let loading = $state(true);
  let error = $state('');
  let message = $state('');
  let selected = $state<string[]>([]);

  let editing = $state(false);
  let saving = $state(false);
  let form = $state({
    name: '',
    description: '',
    script_id: '',
    retry_minutes: 120,
    max_attempts: 5,
    is_active: true
  });

  const canManage = $derived(permissions.includes('crm.manage'));

  // --- aday ekleme ---
  let members = $state<Member[]>([]);
  let adding = $state(false);
  let addBusy = $state(false);
  let addFilter = $state<LeadFilterInput>({});
  let includeCalled = $state(false);

  function openAdd() {
    addFilter = {
      priority: '',
      status: 'NEW',
      city: '',
      district: '',
      source: '',
      owner: '',
      q: '',
      never_called: true
    };
    includeCalled = false;
    adding = true;
  }

  async function addLeads() {
    if (!list || addBusy) return;
    addBusy = true;
    error = '';
    try {
      const result = await addLeadsToList(list.id, {
        filter: $state.snapshot(addFilter),
        include_called: includeCalled
      });
      message = describeAddResult(result);
      adding = false;
      await load();
    } catch (cause) {
      error = cause instanceof APIRequestError ? cause.message : 'Adaylar eklenemedi.';
    } finally {
      addBusy = false;
    }
  }

  // --- arşiv / silme ---
  let deleteOpen = $state(false);

  async function setArchived(archived: boolean) {
    if (!list) return;
    error = '';
    try {
      list = await archiveCallList(list.id, archived);
      message = archived ? 'Liste arşive alındı.' : 'Liste arşivden çıkarıldı.';
    } catch (cause) {
      error = cause instanceof APIRequestError ? cause.message : 'İşlem tamamlanamadı.';
    }
  }

  async function confirmDelete() {
    if (!list) return;
    await deleteCallList(list.id);
    await goto('/crm/listeler');
  }
  const canEdit = $derived(permissions.includes('crm.edit'));

  async function load() {
    const id = page.params.id ?? '';
    loading = true;
    error = '';
    try {
      const [loaded, loadedItems] = await Promise.all([
        getCallList(id),
        listCallListItems(id, statusFilter)
      ]);
      list = loaded;
      items = loadedItems.items;
      selected = [];
    } catch (cause) {
      error = cause instanceof APIRequestError ? cause.message : 'Liste yüklenemedi.';
    } finally {
      loading = false;
    }
  }

  function openEdit() {
    if (!list) return;
    form = {
      name: list.name,
      description: list.description,
      script_id: list.script_id ?? '',
      retry_minutes: list.retry_minutes,
      max_attempts: list.max_attempts,
      is_active: list.is_active
    };
    editing = true;
  }

  async function saveEdit(event: SubmitEvent) {
    event.preventDefault();
    if (!list || saving) return;
    saving = true;
    error = '';
    try {
      list = await updateCallList(list.id, list.version, {
        ...form,
        retry_minutes: Number(form.retry_minutes),
        max_attempts: Number(form.max_attempts)
      });
      editing = false;
      message = 'Liste güncellendi.';
    } catch (cause) {
      error = cause instanceof APIRequestError ? cause.message : 'Liste güncellenemedi.';
    } finally {
      saving = false;
    }
  }

  async function removeSelected() {
    if (!list || !selected.length) return;
    try {
      const result = await removeListItems(list.id, selected);
      message = `${result.removed} aday listeden çıkarıldı.`;
      await load();
    } catch (cause) {
      error = cause instanceof APIRequestError ? cause.message : 'Adaylar çıkarılamadı.';
    }
  }

  function toggleAll(checked: boolean) {
    selected = checked ? items.map((item) => item.id) : [];
  }

  onMount(async () => {
    try {
      const session = await api<Session>('/session');
      permissions = session.permissions ?? [];
    } catch {
      permissions = [];
    }
    listScripts()
      .then((r) => (scripts = r.items))
      .catch(() => (scripts = []));
    listCRMMembers()
      .then((r) => (members = r.items))
      .catch(() => (members = []));
    await load();
  });
</script>

<svelte:head><title>{list?.name ?? 'Arama listesi'} · Varya One</title></svelte:head>

<header class="page-header">
  <div>
    <a class="back" href="/crm/listeler"
      ><ArrowLeft size={14} aria-hidden="true" /> Arama listeleri</a
    >
    <h1>{list?.name ?? 'Arama listesi'}</h1>
    {#if list}
      <p class="meta">
        {list.due} aranacak · {list.waiting} bekleyen · {list.done} tamamlandı · {list.skipped} atlandı
        · ulaşılamazsa {formatMinutes(list.retry_minutes)} sonra, en fazla {list.max_attempts} deneme
        {#if list.script_name}· metin: {list.script_name}{/if}
        {#if !list.is_active}· <strong>arşivde</strong>{/if}
      </p>
    {/if}
  </div>
  <div class="page-actions">
    <Button variant="outline" onclick={() => void load()} disabled={loading}>
      <RefreshCw size={14} data-icon="inline-start" aria-hidden="true" />Yenile
    </Button>
    {#if canManage && list}
      <Button variant="outline" onclick={openEdit}>
        <Pencil size={14} data-icon="inline-start" aria-hidden="true" />Düzenle
      </Button>
      {#if list.is_active}
        <Button variant="outline" onclick={openAdd}>
          <ListPlus size={14} data-icon="inline-start" aria-hidden="true" />Aday ekle
        </Button>
        <Button variant="outline" onclick={() => void setArchived(true)}>
          <Archive size={14} data-icon="inline-start" aria-hidden="true" />Arşive al
        </Button>
      {:else}
        <Button variant="outline" onclick={() => void setArchived(false)}>
          <ArchiveRestore size={14} data-icon="inline-start" aria-hidden="true" />Arşivden çıkar
        </Button>
      {/if}
      <Button variant="outline" onclick={() => (deleteOpen = true)}>
        <Trash2 size={14} data-icon="inline-start" aria-hidden="true" />Sil
      </Button>
    {/if}
    {#if canEdit && list?.is_active && list.due > 0}
      <Button onclick={() => goto(`/crm/arama?liste=${list?.id}`)}>
        <Headset size={14} data-icon="inline-start" aria-hidden="true" />Aramaya başla
      </Button>
    {/if}
  </div>
</header>

{#if error}<p class="notice error" role="alert">{error}</p>{/if}
{#if message}<p class="notice success" role="status">{message}</p>{/if}

{#if adding && list}
  <section class="panel add-panel" aria-labelledby="add-title">
    <h2 id="add-title">“{list.name}” listesine aday ekle</h2>
    <LeadSelector
      bind:filter={addFilter}
      bind:includeCalled
      {members}
      preview={(f, include) =>
        addLeadsToList(list?.id ?? '', { filter: f, include_called: include, dry_run: true })}
    />
    <div class="add-foot">
      <Button variant="ghost" onclick={() => (adding = false)}>Vazgeç</Button>
      <Button onclick={() => void addLeads()} disabled={addBusy}>
        {#if addBusy}<LoaderCircle data-icon="inline-start" aria-hidden="true" />{/if}Ekle
      </Button>
    </div>
  </section>
{/if}

{#if editing}
  <section class="panel form-card">
    <form onsubmit={saveEdit}>
      <div class="form-body grid">
        <Field.Field>
          <Field.FieldLabel for="edit-name">Ad</Field.FieldLabel>
          <Input id="edit-name" bind:value={form.name} maxlength={120} required />
        </Field.Field>
        <Field.Field>
          <Field.FieldLabel for="edit-script">Konuşma metni</Field.FieldLabel>
          <select id="edit-script" class="select" bind:value={form.script_id}>
            <option value="">Yok</option>
            {#each scripts as script (script.id)}
              <option value={script.id}>{script.name}{script.is_active ? '' : ' (kapalı)'}</option>
            {/each}
          </select>
        </Field.Field>
        <Field.Field>
          <Field.FieldLabel for="edit-retry">Tekrar deneme (dakika)</Field.FieldLabel>
          <Input
            id="edit-retry"
            type="number"
            min={5}
            max={10080}
            bind:value={form.retry_minutes}
          />
        </Field.Field>
        <Field.Field>
          <Field.FieldLabel for="edit-attempts">En fazla deneme</Field.FieldLabel>
          <Input id="edit-attempts" type="number" min={1} max={20} bind:value={form.max_attempts} />
        </Field.Field>
        <Field.Field class="full">
          <Field.FieldLabel for="edit-desc">Açıklama</Field.FieldLabel>
          <Input id="edit-desc" bind:value={form.description} maxlength={2000} />
        </Field.Field>
        <label class="check full">
          <input type="checkbox" bind:checked={form.is_active} /> Liste açık (aranabilir)
        </label>
      </div>
      <div class="form-foot">
        <Button variant="ghost" type="button" onclick={() => (editing = false)}>Vazgeç</Button>
        <Button type="submit" disabled={saving}>
          {#if saving}<LoaderCircle data-icon="inline-start" aria-hidden="true" />{/if}Kaydet
        </Button>
      </div>
    </form>
  </section>
{/if}

<section class="panel table-panel">
  <div class="toolbar">
    <select
      class="select narrow"
      bind:value={statusFilter}
      onchange={() => void load()}
      aria-label="Durum"
    >
      <option value="">Tüm adaylar</option>
      <option value="PENDING">Aranacak / tekrar denenecek</option>
      <option value="CALLBACK">Geri aranacak</option>
      <option value="DONE">Tamamlanan</option>
      <option value="SKIPPED">Atlanan</option>
    </select>
    {#if canManage && selected.length}
      <Button variant="outline" size="sm" onclick={() => void removeSelected()}>
        <Trash2 size={13} data-icon="inline-start" aria-hidden="true" />{selected.length} adayı listeden
        çıkar
      </Button>
    {/if}
  </div>
  {#if loading}
    <p class="state" role="status">Yükleniyor…</p>
  {:else if !items.length}
    <p class="state">Listede aday yok.</p>
  {:else}
    <!-- svelte-ignore a11y_no_noninteractive_tabindex (a scrollable region must be reachable by keyboard) -->
    <div class="table-scroll" tabindex="0" role="region" aria-label="Tablo — yatay kaydırılabilir">
      <table>
        <thead>
          <tr>
            {#if canManage}
              <th>
                <input
                  type="checkbox"
                  aria-label="Tümünü seç"
                  checked={selected.length === items.length}
                  onchange={(e) => toggleAll(e.currentTarget.checked)}
                />
              </th>
            {/if}
            <th>Aday</th><th>Telefon</th><th>Durum</th><th>Deneme</th><th>Son sonuç</th>
            <th>Sıradaki zaman</th><th>Şu an arayan</th>
          </tr>
        </thead>
        <tbody>
          {#each items as item (item.id)}
            <tr>
              {#if canManage}
                <td>
                  <input
                    type="checkbox"
                    aria-label="{item.lead_name} seç"
                    checked={selected.includes(item.id)}
                    onchange={(e) =>
                      (selected = e.currentTarget.checked
                        ? [...selected, item.id]
                        : selected.filter((id) => id !== item.id))}
                  />
                </td>
              {/if}
              <td class="name">
                <a href="/crm/adaylar/{item.lead_id}">{item.lead_name}</a>
                {#if item.do_not_call}<span class="pill negative">Aranmasın</span>{/if}
              </td>
              <td>{item.phone || '—'}</td>
              <td>{callItemStatusLabel(item.status, item.available_at)}</td>
              <td class="num">{item.attempts}</td>
              <td>
                {#if item.last_outcome}
                  <span class="pill {callOutcomeTone(item.last_outcome)}"
                    >{callOutcomeLabel(item.last_outcome)}</span
                  >
                  <span class="muted">{formatRelative(item.last_called_at ?? undefined)}</span>
                {:else}—{/if}
              </td>
              <td>
                {item.status === 'PENDING' || item.status === 'CALLBACK'
                  ? formatDate(item.available_at, true)
                  : '—'}
              </td>
              <td>{item.claimed_name || '—'}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</section>

<ConfirmDialog
  bind:open={deleteOpen}
  title="Arama listesi silinsin mi?"
  description={list
    ? `“${list.name}” ve içindeki ${list.total} sıra kaydı silinecek. Yapılan aramalar adayların geçmişinde kalır. Bu işlem geri alınamaz; listeyi saklamak için arşive alabilirsiniz.`
    : ''}
  confirmLabel="Sil"
  onConfirm={confirmDelete}
/>

<style>
  .add-panel {
    padding: 14px 16px;
  }
  .add-panel h2 {
    margin: 0 0 12px;
    font-size: 14px;
  }
  .add-foot {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
    margin-top: 12px;
  }
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
  }
  .table-panel {
    padding: 12px 14px;
  }
  .form-card {
    overflow: hidden;
  }
  .form-body {
    padding: 16px;
  }
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 220px), 1fr));
    gap: 14px 16px;
  }
  .grid :global(.full),
  .grid .full {
    grid-column: 1 / -1;
  }
  .grid :global(input:not([type='checkbox'])) {
    width: 100%;
    box-sizing: border-box;
  }
  .check {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 13px;
  }
  .form-foot {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
    padding: 12px 16px;
    border-top: 1px solid var(--border);
  }
  .toolbar {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
    margin-bottom: 8px;
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
  .select.narrow {
    width: auto;
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
  .num {
    text-align: right;
    font-variant-numeric: tabular-nums;
  }
  .name a {
    color: var(--text);
    font-weight: 600;
  }
  .muted {
    color: var(--text-muted);
    margin-left: 4px;
  }
  .pill {
    display: inline-block;
    padding: 1px 7px;
    border-radius: 999px;
    font-size: 11px;
    font-weight: 600;
    background: color-mix(in srgb, var(--text-muted) 14%, transparent);
  }
  .name .pill {
    margin-left: 6px;
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
    margin: 0;
    padding: 16px 0;
    color: var(--text-muted);
    font-size: 13px;
    text-align: center;
  }
</style>
