<script lang="ts">
  import { goto } from '$app/navigation';
  import { onMount } from 'svelte';
  import {
    Ban,
    Building2,
    Check,
    Pencil,
    Plus,
    RefreshCw,
    Search,
    TriangleAlert,
    X
  } from '@lucide/svelte';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { api, type Session } from '$lib/api';
  import { matchesSearch } from '$lib/filtering';
  import {
    activatePartyGroup,
    createPartyGroup,
    deactivatePartyGroup,
    listPartyGroups,
    updatePartyGroup,
    type PartyGroup
  } from '$lib/features/parties/api';
  import { sortPartyGroups } from '$lib/features/parties/types';
  import { ConfirmDialog } from '$lib/components/varya/confirm-dialog';

  let session = $state<Session | null>(null);
  let groups = $state<PartyGroup[]>([]);
  let search = $state('');
  let code = $state('');
  let name = $state('');
  let loading = $state(true);
  let refreshing = $state(false);
  let saving = $state(false);
  let hasLoaded = $state(false);
  let error = $state('');
  let formError = $state('');
  let message = $state('');
  let editingGroup = $state<PartyGroup>();
  let editCode = $state('');
  let editName = $state('');
  let editSaving = $state(false);
  let activeRequest: AbortController | undefined;
  let requestSequence = 0;

  const canEdit = $derived(Boolean(session?.permissions.includes('party.edit')));
  const filteredGroups = $derived(
    groups.filter((group) => matchesSearch(`${group.code} ${group.name}`, search))
  );
  const activeCount = $derived(groups.filter((group) => group.is_active).length);
  const inactiveCount = $derived(groups.length - activeCount);

  function errorMessage(cause: unknown, fallback: string) {
    return typeof cause === 'object' && cause && 'message' in cause
      ? String(cause.message)
      : fallback;
  }

  async function load() {
    activeRequest?.abort();
    const request = new AbortController();
    activeRequest = request;
    const sequence = ++requestSequence;
    let timedOut = false;
    const timeout = setTimeout(() => {
      timedOut = true;
      request.abort();
    }, 15000);

    if (hasLoaded) refreshing = true;
    else loading = true;
    error = '';

    try {
      if (!session) {
        session = await api<Session>('/session', { signal: request.signal });
      }
      const result = await listPartyGroups(request.signal);
      if (sequence !== requestSequence) return;
      groups = sortPartyGroups(result.items);
      hasLoaded = true;
    } catch (cause) {
      if (sequence !== requestSequence) return;
      if (timedOut) {
        error = 'Cari grupları 15 saniye içinde alınamadı. Bağlantıyı kontrol edip tekrar deneyin.';
      } else if (cause instanceof DOMException && cause.name === 'AbortError') {
        return;
      } else if (!session) {
        await goto('/giris');
      } else {
        error = errorMessage(cause, 'Cari grupları okunamadı.');
      }
    } finally {
      clearTimeout(timeout);
      if (sequence === requestSequence) {
        loading = false;
        refreshing = false;
      }
    }
  }

  async function createGroup(event: SubmitEvent) {
    event.preventDefault();
    if (saving || !canEdit) return;

    const nextCode = code.trim().toUpperCase();
    const nextName = name.trim();
    if (!nextCode || !nextName) {
      formError = 'Kod ve ad zorunludur.';
      return;
    }

    saving = true;
    formError = '';
    message = '';
    try {
      const created = await createPartyGroup({ code: nextCode, name: nextName });
      groups = sortPartyGroups([...groups, created]);
      code = '';
      name = '';
      message = `${created.name} cari grubu oluşturuldu.`;
    } catch (cause) {
      formError = errorMessage(cause, 'Cari grubu oluşturulamadı.');
    } finally {
      saving = false;
    }
  }

  function beginEdit(group: PartyGroup) {
    editingGroup = group;
    editCode = group.code;
    editName = group.name;
    formError = '';
  }

  function cancelEdit() {
    editingGroup = undefined;
    editCode = '';
    editName = '';
  }

  async function saveEdit(event: SubmitEvent) {
    event.preventDefault();
    if (!editingGroup || editSaving || !canEdit) return;
    const nextCode = editCode.trim().toUpperCase();
    const nextName = editName.trim();
    if (!nextCode || !nextName) {
      formError = 'Kod ve ad zorunludur.';
      return;
    }
    editSaving = true;
    formError = '';
    try {
      const updated = await updatePartyGroup(editingGroup.id, editingGroup.version, {
        code: nextCode,
        name: nextName
      });
      groups = sortPartyGroups(groups.map((group) => (group.id === updated.id ? updated : group)));
      message = `${updated.name} cari grubu güncellendi.`;
      cancelEdit();
    } catch (cause) {
      formError = errorMessage(
        cause,
        'Cari grubu güncellenemedi. Listeyi yenileyip tekrar deneyin.'
      );
    } finally {
      editSaving = false;
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

  function deactivateGroup(group: PartyGroup) {
    if (!canEdit || editSaving) return;
    askConfirm({
      title: 'Cari grubunu pasifleştir',
      description: `“${group.name}” grubu pasifleştirilsin mi? Mevcut cari ilişkileri korunur.`,
      confirmLabel: 'Pasifleştir',
      run: () => runDeactivateGroup(group)
    });
  }

  async function runDeactivateGroup(group: PartyGroup) {
    editSaving = true;
    formError = '';
    try {
      const updated = await deactivatePartyGroup(group.id, group.version);
      groups = groups.map((item) => (item.id === updated.id ? updated : item));
      message = `${updated.name} cari grubu pasifleştirildi.`;
      if (editingGroup?.id === updated.id) cancelEdit();
    } catch (cause) {
      formError = errorMessage(
        cause,
        'Cari grubu pasifleştirilemedi. Listeyi yenileyip tekrar deneyin.'
      );
    } finally {
      editSaving = false;
    }
  }

  function activateGroup(group: PartyGroup) {
    if (!canEdit || editSaving) return;
    askConfirm({
      title: 'Cari grubunu aktifleştir',
      description: `“${group.name}” grubu yeniden aktifleştirilsin mi?`,
      confirmLabel: 'Aktifleştir',
      run: () => runActivateGroup(group)
    });
  }

  async function runActivateGroup(group: PartyGroup) {
    editSaving = true;
    formError = '';
    try {
      const updated = await activatePartyGroup(group.id, group.version);
      groups = groups.map((item) => (item.id === updated.id ? updated : item));
      message = `${updated.name} cari grubu aktifleştirildi.`;
    } catch (cause) {
      formError = errorMessage(
        cause,
        'Cari grubu aktifleştirilemedi. Listeyi yenileyip tekrar deneyin.'
      );
    } finally {
      editSaving = false;
    }
  }

  onMount(() => {
    void load();
    return () => activeRequest?.abort();
  });
</script>

<svelte:head><title>Cari Grupları · Varya One</title></svelte:head>

{#if confirmState}
  <ConfirmDialog
    bind:open={confirmOpen}
    title={confirmState.title}
    description={confirmState.description}
    confirmLabel={confirmState.confirmLabel}
    onConfirm={confirmState.run}
  />
{/if}

<header class="page-header">
  <div>
    <h1>Cari grupları</h1>
    <p>Cari kartlarını sınıflandırmak için kullanılan grup tanımlarını yönetin.</p>
  </div>
  <div class="page-actions">
    <a class="button secondary" href="/ayarlar/tanimlar">Tüm tanımlar</a>
    <a class="button secondary" href="/cari/kartlar">Cari kartları</a>
  </div>
</header>

{#if message}<div class="notice success" role="status"><Check size={15} />{message}</div>{/if}
{#if !canEdit && session}
  <div class="notice" role="status">
    <TriangleAlert size={15} />Düzenleme yetkiniz yok.
  </div>
{/if}

<section class="metrics" aria-label="Cari grubu özeti">
  <article class="card metric-card">
    <span>Toplam grup</span><strong>{groups.length}</strong>
  </article>
  <article class="card metric-card"><span>Aktif grup</span><strong>{activeCount}</strong></article>
  <article class="card metric-card">
    <span>Pasif grup</span><strong>{inactiveCount}</strong>
  </article>
</section>

<div class="workspace-grid">
  <section class="card create-card" aria-labelledby="new-group-heading">
    <div class="card-heading">
      <div class="icon-box"><Plus size={16} /></div>
      <div>
        <h2 id="new-group-heading">Yeni cari grubu</h2>
      </div>
    </div>
    <form class="form" onsubmit={createGroup}>
      <label class="field"
        ><span>Kod</span><Input
          bind:value={code}
          maxlength={50}
          placeholder="Örn. PERAKENDE"
          disabled={!canEdit || saving}
        /></label
      >
      <label class="field"
        ><span>Ad</span><Input
          bind:value={name}
          maxlength={120}
          placeholder="Perakende müşteriler"
          disabled={!canEdit || saving}
        /></label
      >
      {#if formError}<div class="inline-error" role="alert">{formError}</div>{/if}
      <Button type="submit" disabled={!canEdit || saving || !code.trim() || !name.trim()}>
        {#if saving}<span class="spinner" aria-hidden="true"></span>Kaydediliyor…{:else}<Plus
            size={15}
          />Grup oluştur{/if}
      </Button>
    </form>
    {#if editingGroup}
      <form class="form edit-form" onsubmit={saveEdit} aria-label="Grubu düzenle">
        <div class="edit-heading">
          <strong>{editingGroup.name}</strong><button
            type="button"
            class="icon-button"
            aria-label="Düzenlemeyi iptal et"
            onclick={cancelEdit}><X size={14} /></button
          >
        </div>
        <label class="field"
          ><span>Kod</span><Input
            bind:value={editCode}
            maxlength={50}
            disabled={editSaving}
          /></label
        >
        <label class="field"
          ><span>Ad</span><Input
            bind:value={editName}
            maxlength={120}
            disabled={editSaving}
          /></label
        >
        {#if formError}<div class="inline-error" role="alert">{formError}</div>{/if}
        <Button type="submit" disabled={editSaving || !editCode.trim() || !editName.trim()}>
          {editSaving ? 'Kaydediliyor…' : 'Kaydet'}
        </Button>
      </form>
    {/if}
  </section>

  <section class="card list-card" aria-labelledby="groups-heading">
    <div class="list-heading">
      <div>
        <h2 id="groups-heading">Tanımlı gruplar</h2>
        <p>{filteredGroups.length} grup gösteriliyor.</p>
      </div>
      <Button
        variant="outline"
        type="button"
        onclick={() => void load()}
        disabled={loading || refreshing}
      >
        <span class:spin={refreshing} class="refresh-icon"><RefreshCw size={14} /></span>{refreshing
          ? 'Yenileniyor…'
          : 'Yenile'}
      </Button>
    </div>
    <div class="search-box">
      <Search size={15} aria-hidden="true" /><Input
        bind:value={search}
        placeholder="Kod veya adla ara"
        aria-label="Cari gruplarında ara"
      />
    </div>

    {#if loading}
      <div class="state" role="status">
        <span class="spinner large" aria-hidden="true"></span><strong
          >Cari grupları yükleniyor…</strong
        >
      </div>
    {:else if error}
      <div class="state error-state" role="alert">
        <TriangleAlert size={22} /><strong>{error}</strong><Button
          type="button"
          variant="outline"
          onclick={() => void load()}>Yeniden dene</Button
        >
      </div>
    {:else if filteredGroups.length === 0}
      <div class="state">
        <Building2 size={24} /><strong>{search ? 'Eşleşen grup yok.' : 'Henüz grup yok.'}</strong
        ><small>{search ? 'Arama ölçütünü değiştirin.' : 'Yeni grup ekleyin.'}</small>
      </div>
    {:else}
      <div class="table-wrap">
        <table>
          <thead
            ><tr
              ><th>Kod</th><th>Grup</th><th>Durum</th>{#if canEdit}<th>İşlem</th>{/if}</tr
            ></thead
          >
          <tbody>
            {#each filteredGroups as group (group.id)}
              <tr>
                <td><code>{group.code}</code></td>
                <td><strong>{group.name}</strong></td>
                <td>
                  <span class:inactive={!group.is_active} class="status">
                    {group.is_active ? 'Aktif' : 'Pasif'}
                  </span>
                </td>
                {#if canEdit}
                  <td class="actions">
                    <button
                      type="button"
                      class="icon-button"
                      aria-label={`${group.name} grubunu düzenle`}
                      onclick={() => beginEdit(group)}><Pencil size={14} /></button
                    >
                    {#if group.is_active}
                      <button
                        type="button"
                        class="icon-button danger"
                        aria-label={`${group.name} grubunu pasifleştir`}
                        onclick={() => void deactivateGroup(group)}><Ban size={14} /></button
                      >
                    {:else}
                      <button
                        type="button"
                        class="icon-button"
                        aria-label={`${group.name} grubunu aktifleştir`}
                        onclick={() => void activateGroup(group)}><RefreshCw size={14} /></button
                      >
                    {/if}
                  </td>
                {/if}
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </section>
</div>

<style>
  .notice {
    display: flex;
    align-items: center;
    gap: 7px;
  }
  .notice.success {
    border-color: color-mix(in srgb, var(--success, #2f855a) 35%, var(--border));
    background: color-mix(in srgb, var(--success, #2f855a) 9%, var(--surface));
    color: var(--success, #2f855a);
  }
  .metrics {
    grid-template-columns: repeat(3, minmax(0, 1fr));
    margin: 0 0 10px;
  }
  .metric-card {
    padding: 11px 13px;
  }
  .metric-card span {
    display: block;
    color: var(--text-muted);
    font-size: 11px;
  }
  .metric-card strong {
    display: block;
    margin-top: 4px;
    font-size: 20px;
  }
  .workspace-grid {
    display: grid;
    grid-template-columns: minmax(245px, 0.75fr) minmax(0, 1.7fr);
    gap: 10px;
  }
  .create-card,
  .list-card {
    min-width: 0;
    padding: 14px;
  }
  .card-heading,
  .list-heading {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 10px;
  }
  .card-heading {
    justify-content: flex-start;
  }
  .card-heading h2,
  .list-heading h2 {
    margin: 0;
    font-size: 14px;
  }
  .list-heading p {
    margin: 3px 0 0;
    color: var(--text-muted);
    font-size: 11px;
    line-height: 1.45;
  }
  .icon-box {
    display: grid;
    flex: 0 0 auto;
    place-items: center;
    width: 30px;
    height: 30px;
    border-radius: 7px;
    background: var(--primary-soft);
    color: var(--primary);
  }
  .form {
    display: grid;
    gap: 10px;
    margin-top: 16px;
  }
  .edit-form {
    padding-top: 14px;
    border-top: 1px solid var(--border);
  }
  .edit-heading {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    color: var(--text);
    font-size: 12px;
  }
  .field {
    display: grid;
    gap: 5px;
    color: var(--text-subtle);
    font-size: 11px;
    font-weight: 650;
  }
  .inline-error {
    color: var(--danger);
    font-size: 11px;
  }
  .search-box {
    position: relative;
    display: flex;
    align-items: center;
    margin: 14px 0 10px;
    color: var(--text-muted);
  }
  .search-box :global(svg) {
    position: absolute;
    left: 9px;
    z-index: 1;
  }
  .search-box :global(input) {
    padding-left: 30px;
  }
  .table-wrap {
    overflow-x: auto;
    border: 1px solid var(--border);
    border-radius: var(--radius-panel);
    background: var(--surface);
  }
  table {
    width: 100%;
    min-width: 520px;
    border-collapse: separate;
    border-spacing: 0;
    font-size: 12px;
  }
  th,
  td {
    height: 40px;
    padding: 0 9px;
    text-align: left;
    white-space: nowrap;
  }
  th {
    height: 34px;
    border-bottom: 1px solid var(--border-strong);
  }
  tbody tr:not(:last-child) td {
    border-bottom: 0;
  }
  .actions {
    display: flex;
    gap: 4px;
  }
  .icon-button {
    display: inline-grid;
    place-items: center;
    width: 27px;
    height: 27px;
    padding: 0;
    border: 1px solid var(--border);
    border-radius: 5px;
    background: var(--surface);
    color: var(--text-muted);
    cursor: pointer;
  }
  .icon-button:hover {
    border-color: var(--primary);
    color: var(--primary);
  }
  .icon-button.danger:hover {
    border-color: var(--danger);
    color: var(--danger);
  }
  th {
    color: var(--text-muted);
    background: var(--surface-muted);
    font-size: 10px;
    font-weight: 750;
  }
  code {
    color: var(--primary);
    font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
    font-size: 11px;
  }
  .status {
    display: inline-flex;
    padding: 3px 7px;
    border-radius: 999px;
    background: color-mix(in srgb, var(--success, #2f855a) 12%, var(--surface));
    color: var(--success, #2f855a);
    font-size: 10px;
    font-weight: 750;
  }
  .status.inactive {
    background: color-mix(in srgb, var(--text-muted) 13%, var(--surface));
    color: var(--text-muted);
  }
  .state {
    display: grid;
    justify-items: center;
    gap: 7px;
    min-height: 210px;
    padding: 34px 16px;
    align-content: center;
    color: var(--text-muted);
    text-align: center;
  }
  .state strong {
    color: var(--text);
    font-size: 12px;
  }
  .state small {
    max-width: 310px;
    font-size: 11px;
    line-height: 1.45;
  }
  .error-state {
    color: var(--danger);
  }
  .error-state strong {
    color: var(--danger);
  }
  .spinner {
    display: inline-block;
    width: 14px;
    height: 14px;
    border: 2px solid currentColor;
    border-right-color: transparent;
    border-radius: 50%;
    animation: spin 0.75s linear infinite;
  }
  .spinner.large {
    width: 22px;
    height: 22px;
  }
  .spin {
    animation: spin 0.8s linear infinite;
  }
  @keyframes spin {
    to {
      transform: rotate(360deg);
    }
  }
  @media (max-width: 760px) {
    .workspace-grid {
      grid-template-columns: 1fr;
    }
    .metrics {
      grid-template-columns: repeat(3, minmax(0, 1fr));
    }
  }
  @media (max-width: 430px) {
    .metrics {
      grid-template-columns: 1fr;
    }
  }
</style>
