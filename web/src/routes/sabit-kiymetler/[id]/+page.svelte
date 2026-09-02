<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/state';
  import { goto } from '$app/navigation';
  import { ArrowLeft, LoaderCircle, Pencil, RefreshCw } from '@lucide/svelte';
  import { VaryaSheet } from '$lib/components/varya/sheet';
  import { api, APIRequestError, type Session } from '$lib/api';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import * as Field from '$lib/components/ui/field';
  import { DateInput } from '$lib/components/varya/date-input';
  import { formatDate } from '$lib/design/formatters';
  import {
    assignFixedAsset,
    getFixedAsset,
    listAssetAssignments,
    listFixedAssetCategories,
    returnFixedAsset,
    updateFixedAsset
  } from '$lib/features/fixed-assets/api';
  import {
    assetStatusLabel,
    editableAssetStatuses,
    toAssetInput,
    type AssetAssignment,
    type FixedAsset
  } from '$lib/features/fixed-assets/types';

  type EmployeeOption = {
    id: string;
    employee_code: string;
    first_name: string;
    last_name: string;
  };

  let asset = $state<FixedAsset | null>(null);
  let assignments = $state<AssetAssignment[]>([]);
  let permissions = $state<string[]>([]);
  let loading = $state(true);
  let error = $state('');
  let actionError = $state('');
  let actionMessage = $state('');
  let busy = $state(false);

  let editing = $state(false);
  let form = $state({
    asset_code: '',
    name: '',
    category: '',
    serial_number: '',
    description: '',
    status: 'AVAILABLE' as FixedAsset['status']
  });

  let assignOpen = $state(false);
  let employeeQuery = $state('');
  let employeeResults = $state<EmployeeOption[]>([]);
  let selectedEmployee = $state<string | undefined>();
  let assignDate = $state(new Date().toISOString().slice(0, 10));
  let assignNote = $state('');

  let returnDate = $state(new Date().toISOString().slice(0, 10));
  let returnNote = $state('');
  let returnOpen = $state(false);

  const canEdit = $derived(permissions.includes('fixed_asset.edit'));
  const canAssign = $derived(permissions.includes('fixed_asset.assign'));

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
  // Kartın mevcut kategorisi listede yoksa (pasifleştirilmiş) yine de seçili kalsın.
  const catOptions = $derived(
    form.category && !categoryNames.includes(form.category)
      ? [form.category, ...categoryNames]
      : categoryNames
  );

  async function loadSession() {
    try {
      const session = await api<Session>('/session');
      permissions = session.permissions ?? [];
    } catch {
      permissions = [];
    }
  }

  async function load(preserve = false) {
    const id = page.params.id;
    if (!id) {
      error = 'Sabit kıymet kimliği bulunamadı.';
      loading = false;
      return;
    }
    if (!preserve) loading = true;
    error = '';
    try {
      asset = await getFixedAsset(id);
      hydrateForm(asset);
      const list = await listAssetAssignments(id);
      assignments = list.items ?? [];
    } catch (cause) {
      error = cause instanceof APIRequestError ? cause.message : 'Sabit kıymet yüklenemedi.';
    } finally {
      loading = false;
    }
  }

  function hydrateForm(value: FixedAsset) {
    form = {
      asset_code: value.asset_code,
      name: value.name,
      category: value.category,
      serial_number: value.serial_number ?? '',
      description: value.description ?? '',
      status: value.status === 'ASSIGNED' ? 'ASSIGNED' : value.status
    };
  }

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

  async function save(event: SubmitEvent) {
    event.preventDefault();
    if (!asset || busy) return;
    busy = true;
    actionError = '';
    actionMessage = '';
    try {
      asset = await updateFixedAsset(asset.id, asset.version, toAssetInput(form));
      hydrateForm(asset);
      editing = false;
      actionMessage = 'Sabit kıymet güncellendi.';
    } catch (cause) {
      actionError = cause instanceof APIRequestError ? cause.message : 'Güncellenemedi.';
    } finally {
      busy = false;
    }
  }

  async function submitAssign(event: SubmitEvent) {
    event.preventDefault();
    if (!asset || busy) return;
    if (!selectedEmployee) {
      actionError = 'Çalışan seçin.';
      return;
    }
    busy = true;
    actionError = '';
    actionMessage = '';
    try {
      asset = await assignFixedAsset(asset.id, {
        employee_id: selectedEmployee,
        assigned_at: assignDate,
        note: assignNote.trim()
      });
      assignOpen = false;
      selectedEmployee = undefined;
      employeeQuery = '';
      assignNote = '';
      actionMessage = 'Sabit kıymet zimmetlendi.';
      await load(true);
    } catch (cause) {
      actionError = cause instanceof APIRequestError ? cause.message : 'Zimmetlenemedi.';
    } finally {
      busy = false;
    }
  }

  async function submitReturn(event: SubmitEvent) {
    event.preventDefault();
    if (!asset?.assigned_to || busy) return;
    busy = true;
    actionError = '';
    actionMessage = '';
    try {
      asset = await returnFixedAsset(asset.id, asset.assigned_to.assignment_id, {
        returned_at: returnDate,
        note: returnNote.trim()
      });
      returnOpen = false;
      returnNote = '';
      actionMessage = 'Sabit kıymet iade alındı.';
      await load(true);
    } catch (cause) {
      actionError = cause instanceof APIRequestError ? cause.message : 'İade alınamadı.';
    } finally {
      busy = false;
    }
  }

  onMount(() => {
    void loadSession();
    void load();
    void loadCategories();
  });
</script>

<svelte:head><title>{asset?.name ?? 'Sabit Kıymet'} · Varya One</title></svelte:head>

{#if loading}
  <div class="panel state" role="status">Yükleniyor…</div>
{:else if !asset}
  <section class="panel error-panel" role="alert">
    <strong>Sabit kıymet açılamadı.</strong>
    <span>{error || 'Kayıt bulunamadı.'}</span>
    <div class="row-actions">
      <Button variant="outline" onclick={() => void load()}>
        <RefreshCw size={14} data-icon="inline-start" aria-hidden="true" />Yeniden dene
      </Button>
      <a class="button secondary" href="/sabit-kiymetler">Listeye dön</a>
    </div>
  </section>
{:else}
  <header class="page-header">
    <div>
      <a class="back" href="/sabit-kiymetler">
        <ArrowLeft size={13} data-icon="inline-start" aria-hidden="true" />Sabit Kıymetler
      </a>
      <div class="title-row">
        <h1>{asset.asset_code} · {asset.name}</h1>
        <span class="status-pill" data-status={asset.status}>{assetStatusLabel(asset.status)}</span>
      </div>
      <p class="meta">
        {asset.category}{asset.serial_number ? ` · SN ${asset.serial_number}` : ''}
      </p>
    </div>
  </header>

  {#if actionMessage}<p class="notice success" role="status">{actionMessage}</p>{/if}
  {#if actionError}<p class="notice error" role="alert">{actionError}</p>{/if}

  <section class="panel zimmet-card">
    <div class="zimmet-info">
      {#if asset.assigned_to}
        <span class="zimmet-dot assigned" aria-hidden="true"></span>
        <div>
          <strong>{asset.assigned_to.employee_name}</strong> zimmetinde
          <span class="muted"
            >({asset.assigned_to.employee_code} · {formatDate(asset.assigned_to.assigned_at)})</span
          >
        </div>
      {:else if asset.status === 'AVAILABLE'}
        <span class="zimmet-dot free" aria-hidden="true"></span>
        <div>Boşta — kimseye zimmetli değil.</div>
      {:else}
        <span class="zimmet-dot other" aria-hidden="true"></span>
        <div>{assetStatusLabel(asset.status)} — zimmetlenemez.</div>
      {/if}
    </div>
    {#if canAssign}
      <div class="zimmet-actions">
        {#if asset.assigned_to}
          <Button onclick={() => (returnOpen = true)} disabled={busy}>İade Al</Button>
        {:else if asset.status === 'AVAILABLE'}
          <Button onclick={() => (assignOpen = true)} disabled={busy}>Zimmetle</Button>
        {/if}
      </div>
    {/if}
  </section>

  <section class="panel">
    <div class="section-heading">
      <h2>Kart bilgileri</h2>
      {#if canEdit && !editing}
        <Button variant="outline" onclick={() => (editing = true)} disabled={busy}>
          <Pencil size={14} data-icon="inline-start" aria-hidden="true" />Düzenle
        </Button>
      {/if}
    </div>
    {#if editing}
      <form onsubmit={save}>
        <Field.FieldGroup class="grid2">
          <Field.Field>
            <Field.FieldLabel for="f-code">Kod</Field.FieldLabel>
            <Input id="f-code" bind:value={form.asset_code} required />
          </Field.Field>
          <Field.Field>
            <Field.FieldLabel for="f-name">Ad</Field.FieldLabel>
            <Input id="f-name" bind:value={form.name} required />
          </Field.Field>
          <Field.Field>
            <Field.FieldLabel for="f-cat">Kategori</Field.FieldLabel>
            <select id="f-cat" bind:value={form.category} class="select" required>
              <option value="" disabled>Kategori seçin</option>
              {#each catOptions as c}<option value={c}>{c}</option>{/each}
            </select>
            <Field.FieldDescription>
              <a href="/ayarlar/tanimlar/sabit-kiymet-kategorileri" target="_blank"
                >Kategori tanımlarını yönet →</a
              >
            </Field.FieldDescription>
          </Field.Field>
          <Field.Field>
            <Field.FieldLabel for="f-serial">Seri No</Field.FieldLabel>
            <Input id="f-serial" bind:value={form.serial_number} />
          </Field.Field>
          <Field.Field>
            <Field.FieldLabel for="f-status">Durum</Field.FieldLabel>
            <select
              id="f-status"
              bind:value={form.status}
              class="select"
              disabled={asset.status === 'ASSIGNED'}
            >
              {#if asset.status === 'ASSIGNED'}
                <option value="ASSIGNED">Zimmetli</option>
              {:else}
                {#each editableAssetStatuses as status}
                  <option value={status}>{assetStatusLabel(status)}</option>
                {/each}
              {/if}
            </select>
          </Field.Field>
          <Field.Field class="full">
            <Field.FieldLabel for="f-desc">Açıklama</Field.FieldLabel>
            <Input id="f-desc" bind:value={form.description} />
          </Field.Field>
          <div class="form-actions full">
            <Button
              type="button"
              variant="ghost"
              disabled={busy}
              onclick={() => {
                editing = false;
                if (asset) hydrateForm(asset);
              }}>Vazgeç</Button
            >
            <Button type="submit" disabled={busy}>
              {#if busy}<LoaderCircle data-icon="inline-start" aria-hidden="true" /> Kaydediliyor…{:else}Kaydet{/if}
            </Button>
          </div>
        </Field.FieldGroup>
      </form>
    {:else}
      <dl class="field-grid">
        <div>
          <dt>Kod</dt>
          <dd>{asset.asset_code}</dd>
        </div>
        <div>
          <dt>Ad</dt>
          <dd>{asset.name}</dd>
        </div>
        <div>
          <dt>Kategori</dt>
          <dd>{asset.category}</dd>
        </div>
        <div>
          <dt>Seri No</dt>
          <dd>{asset.serial_number || '—'}</dd>
        </div>
        <div>
          <dt>Açıklama</dt>
          <dd>{asset.description || '—'}</dd>
        </div>
        <div>
          <dt>Son güncelleme</dt>
          <dd>{formatDate(asset.updated_at, true)}</dd>
        </div>
      </dl>
    {/if}
  </section>

  <VaryaSheet
    bind:open={assignOpen}
    title="Zimmetle"
    description="Bu sabit kıymeti bir çalışana teslim edin."
  >
    <form class="sheet-form" onsubmit={submitAssign}>
      <Field.Field>
        <Field.FieldLabel for="a-emp">Çalışan</Field.FieldLabel>
        <Input
          id="a-emp"
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
      </Field.Field>
      <Field.Field>
        <Field.FieldLabel for="a-date">Zimmet tarihi</Field.FieldLabel>
        <DateInput id="a-date" bind:value={assignDate} ariaLabel="Zimmet tarihi" />
      </Field.Field>
      <Field.Field>
        <Field.FieldLabel for="a-note">Not (isteğe bağlı)</Field.FieldLabel>
        <Input id="a-note" bind:value={assignNote} />
      </Field.Field>
      {#if actionError}<p class="notice error">{actionError}</p>{/if}
      <div class="sheet-actions">
        <Button type="button" variant="ghost" onclick={() => (assignOpen = false)}>Vazgeç</Button>
        <Button type="submit" disabled={busy}>Zimmetle</Button>
      </div>
    </form>
  </VaryaSheet>

  <VaryaSheet
    bind:open={returnOpen}
    title="İade Al"
    description={asset.assigned_to
      ? `${asset.assigned_to.employee_name} zimmetinden geri alınır.`
      : ''}
  >
    <form class="sheet-form" onsubmit={submitReturn}>
      <Field.Field>
        <Field.FieldLabel for="r-date">İade tarihi</Field.FieldLabel>
        <DateInput id="r-date" bind:value={returnDate} ariaLabel="İade tarihi" />
      </Field.Field>
      <Field.Field>
        <Field.FieldLabel for="r-note">Not (isteğe bağlı)</Field.FieldLabel>
        <Input id="r-note" bind:value={returnNote} />
      </Field.Field>
      {#if actionError}<p class="notice error">{actionError}</p>{/if}
      <div class="sheet-actions">
        <Button type="button" variant="ghost" onclick={() => (returnOpen = false)}>Vazgeç</Button>
        <Button type="submit" disabled={busy}>İade Al</Button>
      </div>
    </form>
  </VaryaSheet>

  <section class="panel">
    <div class="section-heading">
      <h2>Zimmet geçmişi</h2>
      <span class="count">{assignments.length} kayıt</span>
    </div>
    {#if !assignments.length}
      <p class="state">Zimmet kaydı yok.</p>
    {:else}
      <div class="table-scroll">
        <table>
          <thead>
            <tr
              ><th>Çalışan</th><th>Zimmet</th><th>İade</th><th>Zimmet notu</th><th>İade notu</th
              ></tr
            >
          </thead>
          <tbody>
            {#each assignments as row}
              <tr>
                <td>{row.employee_code} · {row.employee_name}</td>
                <td>{formatDate(row.assigned_at)}</td>
                <td>{row.returned_at ? formatDate(row.returned_at) : 'Açık'}</td>
                <td>{row.assignment_note || '—'}</td>
                <td>{row.return_note || '—'}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
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
  .back {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    margin-bottom: 6px;
    color: var(--primary);
    font-size: 12px;
    text-decoration: none;
  }
  .title-row {
    display: flex;
    align-items: center;
    gap: 10px;
    flex-wrap: wrap;
  }
  .status-pill {
    padding: 2px 9px;
    border: 1px solid currentColor;
    border-radius: 999px;
    font-size: 11px;
    font-weight: 650;
    color: var(--text-muted);
    background: color-mix(in srgb, currentColor 10%, transparent);
  }
  .status-pill[data-status='AVAILABLE'] {
    color: var(--success, #15803d);
  }
  .status-pill[data-status='ASSIGNED'] {
    color: var(--primary);
  }
  .status-pill[data-status='MAINTENANCE'] {
    color: #b45309;
  }
  .status-pill[data-status='RETIRED'] {
    color: var(--text-muted);
  }
  .meta {
    margin: 5px 0 0;
    color: var(--text-muted);
    font-size: 12px;
  }
  .panel {
    margin-top: 16px;
    padding: 14px 16px;
  }
  .zimmet-card {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    flex-wrap: wrap;
  }
  .zimmet-info {
    display: flex;
    align-items: center;
    gap: 10px;
    font-size: 13px;
  }
  .zimmet-dot {
    width: 9px;
    height: 9px;
    border-radius: 999px;
    flex-shrink: 0;
    background: var(--text-muted);
  }
  .zimmet-dot.assigned {
    background: var(--primary);
  }
  .zimmet-dot.free {
    background: var(--success, #15803d);
  }
  .zimmet-dot.other {
    background: #b45309;
  }
  .muted {
    color: var(--text-muted);
  }
  .sheet-form {
    display: flex;
    flex-direction: column;
    gap: 14px;
  }
  .sheet-actions {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
    margin-top: 4px;
    padding-top: 12px;
    border-top: 1px solid var(--border);
  }
  .section-heading {
    display: flex;
    justify-content: space-between;
    align-items: center;
    margin-bottom: 14px;
  }
  .section-heading h2 {
    margin: 0;
    font-size: 15px;
  }
  .count {
    color: var(--text-muted);
    font-size: 12px;
  }
  :global(.grid2) {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 240px), 1fr));
    gap: 14px 16px;
  }
  :global(.grid2) > :global(*) {
    min-width: 0;
  }
  :global(.grid2) :global(input),
  :global(.grid2) .select {
    width: 100%;
    box-sizing: border-box;
  }
  :global(.grid2 .full) {
    grid-column: 1 / -1;
  }
  .form-actions {
    display: flex;
    justify-content: flex-end;
    flex-wrap: wrap;
    gap: 8px;
    margin-top: 4px;
    padding-top: 14px;
    border-top: 1px solid var(--border);
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
  .select {
    height: var(--control-height);
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
  .field-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 240px), 1fr));
    gap: 0 28px;
    margin: 0;
  }
  .field-grid > div {
    display: flex;
    justify-content: space-between;
    gap: 16px;
    padding: 9px 0;
    border-bottom: 1px solid var(--border);
  }
  .field-grid dt {
    color: var(--text-muted);
    font-size: 12px;
    flex-shrink: 0;
  }
  .field-grid dd {
    margin: 0;
    color: var(--text);
    font-size: 13px;
    text-align: right;
    overflow-wrap: anywhere;
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
  .state {
    margin: 0;
    padding: 16px 0;
    color: var(--text-muted);
    font-size: 13px;
    text-align: center;
  }
  .row-actions {
    display: flex;
    gap: 10px;
    align-items: center;
    margin-top: 8px;
  }
  @media (max-width: 700px) {
    .field-grid {
      grid-template-columns: 1fr;
    }
  }
</style>
