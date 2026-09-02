<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { LoaderCircle, Package, Plus, RefreshCw, Search, X } from '@lucide/svelte';
  import { api, APIRequestError, type Session } from '$lib/api';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import * as Field from '$lib/components/ui/field';
  import { formatDate } from '$lib/design/formatters';
  import {
    assignFixedAsset,
    createFixedAsset,
    listFixedAssetCategories,
    listFixedAssets
  } from '$lib/features/fixed-assets/api';
  import {
    assetStatusLabel,
    editableAssetStatuses,
    type FixedAsset,
    type FixedAssetInput
  } from '$lib/features/fixed-assets/types';

  let session = $state<Session | null>(null);
  let permissions = $state<string[]>([]);
  let loading = $state(true);
  let denied = $state(false);
  let error = $state('');
  let rows = $state<FixedAsset[]>([]);
  let nextCursor = $state<string | undefined>();
  let search = $state('');
  let statusFilter = $state('');
  let loadingMore = $state(false);

  let creating = $state(false);
  let saving = $state(false);
  let createError = $state('');
  let form = $state<FixedAssetInput>({
    asset_code: '',
    name: '',
    category: '',
    serial_number: '',
    description: '',
    status: 'AVAILABLE'
  });

  const canEdit = $derived(permissions.includes('fixed_asset.edit'));
  const canAssign = $derived(permissions.includes('fixed_asset.assign'));

  type EmployeeOption = {
    id: string;
    employee_code: string;
    first_name: string;
    last_name: string;
  };
  let assignOnCreate = $state(false);
  let employeeQuery = $state('');
  let employeeResults = $state<EmployeeOption[]>([]);
  let selectedEmployee = $state<string | undefined>();

  async function searchEmployees(term: string) {
    employeeQuery = term;
    if (term.trim().length < 1) {
      employeeResults = [];
      return;
    }
    try {
      const result = await api<{ items: EmployeeOption[] }>(
        `/hr/employees?status=ACTIVE&q=${encodeURIComponent(term.trim())}&limit=10`
      );
      employeeResults = result.items ?? [];
    } catch {
      employeeResults = [];
    }
  }

  let categoryNames = $state<string[]>([]);
  async function loadCategories() {
    try {
      categoryNames = (await listFixedAssetCategories()).items
        .filter((c) => c.is_active)
        .map((c) => c.name);
    } catch {
      categoryNames = [];
    }
  }

  async function loadSession() {
    try {
      session = await api<Session>('/session');
      permissions = session?.permissions ?? [];
      denied = !permissions.includes('fixed_asset.read');
    } catch {
      denied = true;
    }
  }

  async function load(reset = true) {
    if (denied) return;
    if (reset) {
      loading = true;
      rows = [];
      nextCursor = undefined;
    } else {
      loadingMore = true;
    }
    error = '';
    try {
      const result = await listFixedAssets({
        q: search.trim() || undefined,
        status: statusFilter || undefined,
        cursor: reset ? undefined : nextCursor,
        limit: 50
      });
      rows = reset ? result.items : [...rows, ...result.items];
      nextCursor = result.next_cursor;
    } catch (cause) {
      error = cause instanceof APIRequestError ? cause.message : 'Sabit kıymetler yüklenemedi.';
    } finally {
      loading = false;
      loadingMore = false;
    }
  }

  function openCreate() {
    form = {
      asset_code: '',
      name: '',
      category: '',
      serial_number: '',
      description: '',
      status: 'AVAILABLE'
    };
    assignOnCreate = false;
    selectedEmployee = undefined;
    employeeQuery = '';
    employeeResults = [];
    createError = '';
    creating = true;
  }

  async function submitCreate(event: SubmitEvent) {
    event.preventDefault();
    if (saving) return;
    if (!form.name.trim() || !form.category.trim()) {
      createError = 'Ad ve kategori zorunludur.';
      return;
    }
    if (assignOnCreate && !selectedEmployee) {
      createError = 'Zimmet için çalışan seçin veya zimmetlemeyi kapatın.';
      return;
    }
    saving = true;
    createError = '';
    try {
      const created = await createFixedAsset({
        ...form,
        asset_code: form.asset_code.trim(),
        name: form.name.trim(),
        category: form.category.trim(),
        serial_number: form.serial_number.trim(),
        description: form.description.trim()
      });
      if (assignOnCreate && selectedEmployee) {
        await assignFixedAsset(created.id, {
          employee_id: selectedEmployee,
          assigned_at: new Date().toISOString().slice(0, 10),
          note: ''
        });
      }
      creating = false;
      await goto(`/sabit-kiymetler/${created.id}`);
    } catch (cause) {
      createError =
        cause instanceof APIRequestError ? cause.message : 'Sabit kıymet oluşturulamadı.';
    } finally {
      saving = false;
    }
  }

  onMount(async () => {
    await loadSession();
    await Promise.all([load(), loadCategories()]);
  });
</script>

<svelte:head><title>Sabit Kıymetler · Varya One</title></svelte:head>

{#if denied}
  <section class="panel error-panel" role="alert">
    <strong>Erişim yok.</strong>
    <span>Sabit kıymetleri görüntüleme yetkiniz bulunmuyor.</span>
  </section>
{:else}
  <header class="page-header">
    <div>
      <h1>Sabit Kıymetler</h1>
    </div>
    <div class="page-actions">
      <Button variant="outline" onclick={() => void load()} disabled={loading}>
        <RefreshCw size={14} data-icon="inline-start" aria-hidden="true" />Yenile
      </Button>
      {#if canEdit}
        <Button onclick={openCreate}>
          <Plus size={14} data-icon="inline-start" aria-hidden="true" />Yeni Sabit Kıymet
        </Button>
      {/if}
    </div>
  </header>

  {#if creating}
    <section class="panel form-card" aria-labelledby="asset-create-title">
      <div class="form-head">
        <div class="form-head-title">
          <span class="form-head-icon"><Package size={16} aria-hidden="true" /></span>
          <div>
            <h2 id="asset-create-title">Yeni sabit kıymet</h2>
            <p>Zimmet takibi için bir varlık kartı oluşturun.</p>
          </div>
        </div>
        <button type="button" class="icon-x" onclick={() => (creating = false)} aria-label="Kapat">
          <X size={16} aria-hidden="true" />
        </button>
      </div>
      <form onsubmit={submitCreate}>
        <div class="form-body">
          <Field.FieldGroup class="asset-form-grid">
            <Field.Field>
              <Field.FieldLabel for="asset-name">Ad</Field.FieldLabel>
              <Input
                id="asset-name"
                bind:value={form.name}
                placeholder="Örn. Dizüstü bilgisayar"
                required
              />
            </Field.Field>
            <Field.Field>
              <Field.FieldLabel for="asset-category">Kategori</Field.FieldLabel>
              <select id="asset-category" bind:value={form.category} class="select" required>
                <option value="" disabled>Kategori seçin</option>
                {#each categoryNames as c}<option value={c}>{c}</option>{/each}
              </select>
              <Field.FieldDescription>
                {#if !categoryNames.length}Henüz kategori yok.
                {/if}<a href="/ayarlar/tanimlar/sabit-kiymet-kategorileri" target="_blank"
                  >Kategori tanımlarını yönet →</a
                >
              </Field.FieldDescription>
            </Field.Field>
            <Field.Field>
              <Field.FieldLabel for="asset-code">Sabit kıymet kodu</Field.FieldLabel>
              <Input
                id="asset-code"
                bind:value={form.asset_code}
                maxlength={40}
                placeholder="Boş bırakırsanız otomatik üretilir"
              />
            </Field.Field>
            <Field.Field>
              <Field.FieldLabel for="asset-serial">Seri No</Field.FieldLabel>
              <Input id="asset-serial" bind:value={form.serial_number} placeholder="İsteğe bağlı" />
            </Field.Field>
            <Field.Field>
              <Field.FieldLabel for="asset-status">Durum</Field.FieldLabel>
              <select id="asset-status" bind:value={form.status} class="select">
                {#each editableAssetStatuses as status}
                  <option value={status}>{assetStatusLabel(status)}</option>
                {/each}
              </select>
            </Field.Field>
            <Field.Field class="full">
              <Field.FieldLabel for="asset-description">Açıklama</Field.FieldLabel>
              <Input
                id="asset-description"
                bind:value={form.description}
                placeholder="İsteğe bağlı not"
              />
            </Field.Field>
          </Field.FieldGroup>

          {#if canAssign}
            <div class="assign-block">
              <label class="assign-toggle">
                <input type="checkbox" bind:checked={assignOnCreate} />
                Oluşturur oluşturmaz bir çalışana zimmetle
              </label>
              {#if assignOnCreate}
                <div class="assign-fields">
                  <Field.Field class="full">
                    <Field.FieldLabel for="ac-emp">Çalışan</Field.FieldLabel>
                    <Input
                      id="ac-emp"
                      placeholder="Ad veya kod ara"
                      value={employeeQuery}
                      oninput={(event) => void searchEmployees(event.currentTarget.value)}
                      autocomplete="off"
                    />
                    {#if employeeResults.length}
                      <ul class="lookup-results" role="listbox">
                        {#each employeeResults as item}
                          <li>
                            <button
                              type="button"
                              class:selected={selectedEmployee === item.id}
                              onclick={() => {
                                selectedEmployee = item.id;
                                employeeQuery = `${item.employee_code} · ${item.first_name} ${item.last_name}`;
                                employeeResults = [];
                              }}
                            >
                              {item.employee_code} · {item.first_name}
                              {item.last_name}
                            </button>
                          </li>
                        {/each}
                      </ul>
                    {/if}
                    <Field.FieldDescription>
                      Zimmet tarihi, kaydın oluşturulduğu bugünün tarihi olarak alınır.
                    </Field.FieldDescription>
                  </Field.Field>
                </div>
              {/if}
            </div>
          {/if}
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
    <form
      class="search"
      onsubmit={(event) => {
        event.preventDefault();
        void load();
      }}
    >
      <span class="search-icon"><Search size={15} aria-hidden="true" /></span>
      <Input bind:value={search} placeholder="Kod, ad veya seri no ara" aria-label="Ara" />
      <select
        bind:value={statusFilter}
        onchange={() => void load()}
        class="select"
        aria-label="Durum"
      >
        <option value="">Tüm durumlar</option>
        <option value="AVAILABLE">Uygun</option>
        <option value="ASSIGNED">Zimmetli</option>
        <option value="MAINTENANCE">Bakımda</option>
        <option value="RETIRED">Hurdaya ayrıldı</option>
      </select>
      <Button type="submit" variant="outline">Ara</Button>
    </form>
  </section>

  {#if error}<p class="notice error" role="alert">{error}</p>{/if}

  <section class="panel table-panel">
    {#if loading}
      <p class="state" role="status">Yükleniyor…</p>
    {:else if !rows.length}
      <p class="state">Kayıt bulunamadı.</p>
    {:else}
      <div class="table-scroll">
        <table>
          <thead>
            <tr>
              <th>Kod</th><th>Ad</th><th>Kategori</th><th>Seri No</th><th>Durum</th>
              <th>Zimmetli</th><th>Güncelleme</th>
            </tr>
          </thead>
          <tbody>
            {#each rows as asset}
              <tr onclick={() => goto(`/sabit-kiymetler/${asset.id}`)} class="row">
                <td>{asset.asset_code}</td>
                <td>{asset.name}</td>
                <td>{asset.category}</td>
                <td>{asset.serial_number || '—'}</td>
                <td>{assetStatusLabel(asset.status)}</td>
                <td>{asset.assigned_to?.employee_name ?? '—'}</td>
                <td>{formatDate(asset.updated_at, true)}</td>
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
  :global(.asset-form-grid) {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 240px), 1fr));
    gap: 14px 16px;
  }
  :global(.asset-form-grid) > :global(*) {
    min-width: 0;
  }
  :global(.asset-form-grid .full) {
    grid-column: 1 / -1;
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
  .select:focus {
    outline: none;
    border-color: var(--primary);
    box-shadow: 0 0 0 2px var(--focus);
  }
  .assign-block {
    margin-top: 16px;
    padding-top: 14px;
    border-top: 1px dashed var(--border);
  }
  .assign-toggle {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 13px;
    color: var(--text);
    cursor: pointer;
  }
  .assign-fields {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 240px), 1fr));
    gap: 14px 16px;
    margin-top: 12px;
  }
  .assign-fields :global(.full) {
    grid-column: 1 / -1;
  }
  .assign-fields :global(input),
  .assign-fields :global(.select) {
    width: 100%;
    box-sizing: border-box;
  }
  .lookup-results {
    list-style: none;
    margin: 6px 0 0;
    padding: 0;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    max-height: 200px;
    overflow-y: auto;
  }
  .lookup-results button {
    display: block;
    width: 100%;
    padding: 8px 10px;
    border: 0;
    background: transparent;
    text-align: left;
    font-size: 12px;
    cursor: pointer;
    color: var(--text);
  }
  .lookup-results button:hover,
  .lookup-results button.selected {
    background: var(--surface-hover, rgba(0, 0, 0, 0.05));
  }
  .search {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
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
  :global(.asset-form-grid) :global(input),
  :global(.asset-form-grid) .select {
    width: 100%;
    box-sizing: border-box;
  }
</style>
