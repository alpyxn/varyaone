<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { toast } from 'svelte-sonner';
  import {
    Archive,
    ArchiveRestore,
    ListChecks,
    LoaderCircle,
    Plus,
    RefreshCw,
    Trash2,
    X
  } from '@lucide/svelte';
  import { api, APIRequestError, type Session } from '$lib/api';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import * as Field from '$lib/components/ui/field';
  import { ConfirmDialog } from '$lib/components/varya/confirm-dialog';
  import { formatDate } from '$lib/design/formatters';
  import {
    addLeadsToList,
    archiveCallList,
    createCallList,
    deleteCallList,
    listCallLists,
    listCRMMembers,
    listScripts,
    previewListSelection
  } from '$lib/features/crm/api';
  import LeadSelector from '$lib/features/crm/LeadSelector.svelte';
  import {
    describeAddResult,
    formatMinutes,
    type CallList,
    type CallScript,
    type LeadFilterInput,
    type Member
  } from '$lib/features/crm/types';

  let permissions = $state<string[]>([]);
  let denied = $state(false);
  let loading = $state(true);
  let error = $state('');
  let message = $state('');
  let lists = $state<CallList[]>([]);
  let scripts = $state<CallScript[]>([]);
  let members = $state<Member[]>([]);
  let tab = $state<'active' | 'archived'>('active');

  const canManage = $derived(permissions.includes('crm.manage'));
  const canEdit = $derived(permissions.includes('crm.edit'));
  const shown = $derived(lists.filter((list) => (tab === 'active') === list.is_active));
  const archivedCount = $derived(lists.filter((list) => !list.is_active).length);

  // --- yeni liste ---
  let creating = $state(false);
  let saving = $state(false);
  let createError = $state('');
  let form = $state({
    name: '',
    description: '',
    script_id: '',
    retry_minutes: 120,
    max_attempts: 5
  });
  let withLeads = $state(true);
  const freshFilter = (): LeadFilterInput => ({
    priority: '',
    status: 'NEW',
    city: '',
    district: '',
    source: '',
    owner: '',
    q: '',
    never_called: true
  });
  let filter = $state<LeadFilterInput>(freshFilter());
  let includeCalled = $state(false);

  async function load() {
    loading = true;
    error = '';
    try {
      lists = (await listCallLists(true)).items;
    } catch (cause) {
      error = cause instanceof APIRequestError ? cause.message : 'Arama listeleri yüklenemedi.';
    } finally {
      loading = false;
    }
  }

  function openCreate() {
    form = { name: '', description: '', script_id: '', retry_minutes: 120, max_attempts: 5 };
    filter = freshFilter();
    includeCalled = false;
    withLeads = true;
    createError = '';
    creating = true;
  }

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    if (saving) return;
    if (!form.name.trim()) {
      createError = 'Liste adı girin.';
      return;
    }
    saving = true;
    createError = '';
    try {
      const created = await createCallList({
        ...form,
        name: form.name.trim(),
        description: form.description.trim(),
        retry_minutes: Number(form.retry_minutes),
        max_attempts: Number(form.max_attempts)
      });
      if (withLeads) {
        const result = await addLeadsToList(created.id, {
          filter: $state.snapshot(filter),
          include_called: includeCalled
        });
        toast.success(`“${created.name}” oluşturuldu`, { description: describeAddResult(result) });
      }
      await goto(`/crm/listeler/${created.id}`);
    } catch (cause) {
      createError = cause instanceof APIRequestError ? cause.message : 'Liste oluşturulamadı.';
    } finally {
      saving = false;
    }
  }

  // --- arşiv / silme ---
  async function setArchived(list: CallList, archived: boolean) {
    error = '';
    try {
      await archiveCallList(list.id, archived);
      message = archived ? `“${list.name}” arşive alındı.` : `“${list.name}” arşivden çıkarıldı.`;
      await load();
    } catch (cause) {
      error = cause instanceof APIRequestError ? cause.message : 'İşlem tamamlanamadı.';
    }
  }

  let deleting = $state<CallList | null>(null);
  let deleteOpen = $state(false);

  async function confirmDelete() {
    if (!deleting) return;
    await deleteCallList(deleting.id);
    message = `“${deleting.name}” silindi.`;
    deleting = null;
    await load();
  }

  onMount(async () => {
    try {
      const session = await api<Session>('/session');
      permissions = session.permissions ?? [];
      denied = !permissions.includes('crm.read');
    } catch {
      denied = true;
    }
    if (denied) {
      loading = false;
      return;
    }
    await Promise.all([
      load(),
      listScripts()
        .then((r) => (scripts = r.items.filter((s) => s.is_active)))
        .catch(() => (scripts = [])),
      listCRMMembers()
        .then((r) => (members = r.items))
        .catch(() => (members = []))
    ]);
  });
</script>

<svelte:head><title>Arama Listeleri · Varya One</title></svelte:head>

{#if denied}
  <section class="panel error-panel" role="alert">
    <strong>Erişim yok.</strong>
    <span>CRM yetkiniz bulunmuyor.</span>
  </section>
{:else}
  <header class="page-header">
    <div>
      <h1>Arama Listeleri</h1>
      <p class="meta">
        Sırayla aranacak aday grupları. Bir aday aynı anda yalnızca bir açık listede bekler ve
        aranmış adaylar siz istemedikçe yeni listeye girmez.
      </p>
    </div>
    <div class="page-actions">
      <Button variant="outline" onclick={() => goto('/crm/metinler')}>Konuşma metinleri</Button>
      <Button variant="outline" onclick={() => void load()} disabled={loading}>
        <RefreshCw size={14} data-icon="inline-start" aria-hidden="true" />Yenile
      </Button>
      {#if canManage}
        <Button onclick={openCreate}>
          <Plus size={14} data-icon="inline-start" aria-hidden="true" />Yeni Liste
        </Button>
      {/if}
    </div>
  </header>

  {#if creating}
    <section class="panel form-card" aria-labelledby="list-create-title">
      <div class="form-head">
        <div class="form-head-title">
          <span class="form-head-icon"><ListChecks size={16} aria-hidden="true" /></span>
          <div>
            <h2 id="list-create-title">Yeni arama listesi</h2>
            <p>Listeyi adlandırın ve hangi adayların gireceğini seçin.</p>
          </div>
        </div>
        <button type="button" class="icon-x" onclick={() => (creating = false)} aria-label="Kapat">
          <X size={16} aria-hidden="true" />
        </button>
      </div>
      <form onsubmit={submit}>
        <div class="form-body grid">
          <Field.Field>
            <Field.FieldLabel for="list-name">Ad</Field.FieldLabel>
            <Input
              id="list-name"
              bind:value={form.name}
              maxlength={120}
              placeholder="Örn. Antalya A-B sıcaklar"
              required
            />
          </Field.Field>
          <Field.Field>
            <Field.FieldLabel for="list-script">Konuşma metni</Field.FieldLabel>
            <select id="list-script" class="select" bind:value={form.script_id}>
              <option value="">Yok</option>
              {#each scripts as script (script.id)}
                <option value={script.id}>{script.name}</option>
              {/each}
            </select>
          </Field.Field>
          <Field.Field>
            <Field.FieldLabel for="list-retry">Ulaşılamazsa tekrar ara</Field.FieldLabel>
            <select id="list-retry" class="select" bind:value={form.retry_minutes}>
              <option value={30}>30 dakika sonra</option>
              <option value={60}>1 saat sonra</option>
              <option value={120}>2 saat sonra</option>
              <option value={240}>4 saat sonra</option>
              <option value={1440}>Ertesi gün</option>
              <option value={2880}>2 gün sonra</option>
            </select>
          </Field.Field>
          <Field.Field>
            <Field.FieldLabel for="list-attempts">En fazla deneme</Field.FieldLabel>
            <Input
              id="list-attempts"
              type="number"
              min={1}
              max={20}
              bind:value={form.max_attempts}
            />
          </Field.Field>
          <Field.Field class="full">
            <Field.FieldLabel for="list-desc">Açıklama</Field.FieldLabel>
            <Input id="list-desc" bind:value={form.description} maxlength={2000} />
          </Field.Field>
        </div>
        <div class="form-body leads">
          <label class="with-leads">
            <input type="checkbox" bind:checked={withLeads} />
            <strong>Adayları şimdi ekle</strong>
          </label>
          {#if withLeads}
            <LeadSelector
              bind:filter
              bind:includeCalled
              {members}
              preview={(f, include) => previewListSelection({ filter: f, include_called: include })}
            />
          {/if}
        </div>
        {#if createError}<p class="notice error" role="alert">{createError}</p>{/if}
        <div class="form-foot">
          <Button variant="ghost" type="button" onclick={() => (creating = false)}>Vazgeç</Button>
          <Button type="submit" disabled={saving}>
            {#if saving}<LoaderCircle data-icon="inline-start" aria-hidden="true" />{/if}
            {withLeads ? 'Oluştur ve adayları ekle' : 'Boş liste oluştur'}
          </Button>
        </div>
      </form>
    </section>
  {/if}

  {#if error}<p class="notice error" role="alert">{error}</p>{/if}
  {#if message}<p class="notice success" role="status">{message}</p>{/if}

  <section class="panel table-panel">
    <div class="tabs" role="tablist" aria-label="Liste durumu">
      <button
        type="button"
        role="tab"
        aria-selected={tab === 'active'}
        class:active={tab === 'active'}
        onclick={() => (tab = 'active')}>Aktif</button
      >
      <button
        type="button"
        role="tab"
        aria-selected={tab === 'archived'}
        class:active={tab === 'archived'}
        onclick={() => (tab = 'archived')}>Arşiv ({archivedCount})</button
      >
    </div>
    {#if loading}
      <p class="state" role="status">Yükleniyor…</p>
    {:else if !shown.length}
      <p class="state">{tab === 'active' ? 'Aktif arama listesi yok.' : 'Arşivde liste yok.'}</p>
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
              <th>Ad</th><th>Aranacak</th><th>Bekleyen</th><th>Tamamlanan</th><th>Atlanan</th>
              <th>Toplam</th><th>Tekrar</th><th>Metin</th><th>Oluşturma</th><th></th>
            </tr>
          </thead>
          <tbody>
            {#each shown as list (list.id)}
              <tr>
                <td class="name"><a href="/crm/listeler/{list.id}">{list.name}</a></td>
                <td class="num strong">{list.due}</td>
                <td class="num">{list.waiting}</td>
                <td class="num">{list.done}</td>
                <td class="num">{list.skipped}</td>
                <td class="num">{list.total}</td>
                <td>{formatMinutes(list.retry_minutes)} · {list.max_attempts} kez</td>
                <td>{list.script_name || '—'}</td>
                <td>{formatDate(list.created_at)}</td>
                <td class="actions">
                  {#if canEdit && list.is_active && list.due > 0}
                    <Button size="sm" onclick={() => goto(`/crm/arama?liste=${list.id}`)}
                      >Aramaya başla</Button
                    >
                  {/if}
                  {#if canManage}
                    {#if list.is_active}
                      <Button
                        size="sm"
                        variant="ghost"
                        title="Arşive al"
                        onclick={() => void setArchived(list, true)}
                      >
                        <Archive size={14} aria-hidden="true" /><span class="sr">Arşive al</span>
                      </Button>
                    {:else}
                      <Button
                        size="sm"
                        variant="ghost"
                        title="Arşivden çıkar"
                        onclick={() => void setArchived(list, false)}
                      >
                        <ArchiveRestore size={14} aria-hidden="true" /><span class="sr"
                          >Arşivden çıkar</span
                        >
                      </Button>
                    {/if}
                    <Button
                      size="sm"
                      variant="ghost"
                      title="Sil"
                      onclick={() => {
                        deleting = list;
                        deleteOpen = true;
                      }}
                    >
                      <Trash2 size={14} aria-hidden="true" /><span class="sr">Sil</span>
                    </Button>
                  {/if}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </section>
{/if}

<ConfirmDialog
  bind:open={deleteOpen}
  title="Arama listesi silinsin mi?"
  description={deleting
    ? `“${deleting.name}” ve içindeki ${deleting.total} sıra kaydı silinecek. Yapılan aramalar adayların geçmişinde kalır. Bu işlem geri alınamaz; listeyi saklamak için arşive alabilirsiniz.`
    : ''}
  confirmLabel="Sil"
  onConfirm={confirmDelete}
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
    max-width: 720px;
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
  .form-head {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 12px;
    padding: 14px 16px;
    border-bottom: 1px solid var(--border);
  }
  .form-head-title {
    display: flex;
    gap: 10px;
  }
  .form-head-icon {
    display: grid;
    place-items: center;
    width: 30px;
    height: 30px;
    border-radius: 8px;
    background: color-mix(in srgb, var(--primary) 12%, transparent);
    color: var(--primary);
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
  .form-body {
    padding: 16px;
  }
  .leads {
    display: grid;
    gap: 12px;
    border-top: 1px dashed var(--border);
  }
  .with-leads {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 13px;
    cursor: pointer;
  }
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 220px), 1fr));
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
  .tabs {
    display: flex;
    gap: 6px;
    margin-bottom: 10px;
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
  .strong {
    font-weight: 700;
    color: var(--primary);
  }
  .name a {
    color: var(--text);
    font-weight: 600;
  }
  .actions {
    display: flex;
    gap: 4px;
    align-items: center;
  }
  .sr {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip: rect(0 0 0 0);
  }
  .state {
    margin: 0;
    padding: 16px 0;
    color: var(--text-muted);
    font-size: 13px;
    text-align: center;
  }
</style>
