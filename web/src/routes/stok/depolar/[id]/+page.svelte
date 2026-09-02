<script lang="ts">
  import { page } from '$app/state';
  import { goto } from '$app/navigation';
  import { ArrowLeft, LoaderCircle, Pencil, RefreshCw, Save, Trash2 } from '@lucide/svelte';
  import { onMount } from 'svelte';
  import { api, APIRequestError, type Session } from '$lib/api';
  import * as Field from '$lib/components/ui/field';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { ConfirmDialog } from '$lib/components/varya/confirm-dialog';
  import { DocumentStatus } from '$lib/components/varya/document-status';
  import { formatDate, formatQuantity } from '$lib/design/formatters';
  import {
    isActiveWarehouse,
    warehouseTypeLabel,
    warehouseUpdateInput,
    type Warehouse
  } from '$lib/features/warehouses/types';
  import {
    deleteWarehouse,
    setWarehouseActive,
    updateWarehouse
  } from '$lib/features/warehouses/api';

  type StockPosition = {
    sku?: string;
    product_code?: string;
    product_name?: string;
    physical_quantity?: string | number;
    reserved_quantity?: string | number;
    available_quantity?: string | number;
  };

  type WarehouseView = Warehouse & {
    responsible_name?: string;
    responsible_user_name?: string;
    stock_positions?: StockPosition[];
  };

  type ConfirmAction = 'toggle' | 'delete' | null;

  let warehouse = $state<WarehouseView>();
  let loading = $state(true);
  let refreshing = $state(false);
  let error = $state('');
  let actionError = $state('');
  let actionMessage = $state('');
  let permissions = $state<string[]>([]);
  let editing = $state(false);
  let saving = $state(false);
  let actionBusy = $state<'' | 'toggle' | 'delete'>('');
  let confirmOpen = $state(false);
  let confirmAction = $state<ConfirmAction>(null);
  let form = $state({ code: '', name: '', address: '' });

  function normalizeWarehouse(payload: unknown): WarehouseView | undefined {
    if (!payload || typeof payload !== 'object') return undefined;
    const value = payload as Record<string, unknown>;
    const candidate =
      value.item && typeof value.item === 'object'
        ? (value.item as Record<string, unknown>)
        : value;
    if (typeof candidate.id !== 'string') return undefined;
    return candidate as WarehouseView;
  }

  function hydrateForm(value: WarehouseView) {
    form = {
      code: value.code ?? '',
      name: value.name ?? '',
      address: value.address ?? ''
    };
  }

  function errorText(cause: unknown) {
    return typeof cause === 'object' && cause && 'message' in cause ? String(cause.message) : '';
  }

  function errorCode(cause: unknown) {
    if (!cause || typeof cause !== 'object') return '';
    const code = (cause as { code?: unknown }).code;
    return typeof code === 'string' ? code : '';
  }

  function isVersionConflict(cause: unknown) {
    if (cause instanceof APIRequestError) {
      return ['CONFLICT', 'VERSION_CONFLICT', 'WAREHOUSE_VERSION_CONFLICT'].includes(cause.code);
    }
    const code = errorCode(cause);
    return ['CONFLICT', 'VERSION_CONFLICT', 'WAREHOUSE_VERSION_CONFLICT'].includes(code);
  }

  async function load(preserveView = false) {
    const id = page.params.id;
    if (!id) {
      error = 'Depo kimliği bulunamadı.';
      loading = false;
      return false;
    }
    if (preserveView) refreshing = true;
    else loading = true;
    error = '';
    try {
      const next = normalizeWarehouse(await api<unknown>(`/warehouses/${encodeURIComponent(id)}`));
      if (!next) throw new Error('Depo bulunamadı.');
      warehouse = next;
      hydrateForm(next);
      return true;
    } catch (cause) {
      if (!preserveView) error = errorText(cause) || 'Depo bilgileri alınamadı.';
      return false;
    } finally {
      if (preserveView) refreshing = false;
      else loading = false;
    }
  }

  async function loadSession() {
    try {
      const session = await api<Session>('/session');
      permissions = session.permissions ?? [];
    } catch {
      permissions = [];
    }
  }

  function currentVersion() {
    if (!warehouse || typeof warehouse.version !== 'number') {
      throw new Error('Depo bilgisi güncel değil. Sayfayı yenileyin.');
    }
    return warehouse.version;
  }

  async function explainActionError(
    cause: unknown,
    fallback: string,
    action: 'save' | 'toggle' | 'delete' = 'save'
  ) {
    if (isVersionConflict(cause)) {
      await load(true);
      return 'Depo bilgileri güncel değil. Son bilgiler yüklendi; işlemi tekrar deneyin.';
    }
    const code = errorCode(cause);
    if (code === 'WAREHOUSE_HAS_MOVEMENTS' || code === 'WAREHOUSE_HAS_HISTORY') {
      return action === 'delete'
        ? 'Bu depoda hareket bulunduğu için silinemez. Kullanılabilir stoktan çıkarmak için Pasifleştir seçeneğini kullanın.'
        : fallback;
    }
    if (
      code === 'WAREHOUSE_HAS_OPEN_TRANSFER' ||
      code === 'WAREHOUSE_HAS_DEPENDENCIES' ||
      code === 'WAREHOUSE_IN_USE'
    ) {
      if (action === 'toggle') {
        return 'Devam eden transferi bulunan depo pasifleştirilemez. Önce transferi tamamlayın veya iptal edin.';
      }
      if (action === 'delete') {
        await load(true);
        return 'Depo ilişkili kayıtlar nedeniyle silinemez. Kullanılabilir stoktan çıkarmak için Pasifleştir seçeneğini kullanın.';
      }
      return 'Depo ilişkili kayıtlar nedeniyle işlem tamamlanamadı.';
    }
    if (!cause || typeof cause !== 'object') return fallback;
    const value = cause as { message?: unknown };
    return typeof value.message === 'string' && value.message.trim() ? value.message : fallback;
  }

  function openEditor() {
    if (!warehouse || warehouse.is_system === true) return;
    hydrateForm(warehouse);
    actionError = '';
    editing = true;
  }

  async function save() {
    if (!warehouse || saving || actionBusy) return;
    if (!form.name.trim()) {
      actionError = 'Depo adı gereklidir.';
      return;
    }
    saving = true;
    actionError = '';
    actionMessage = '';
    try {
      const draft = { ...warehouse, ...form };
      const updated = await updateWarehouse(
        warehouse.id,
        currentVersion(),
        warehouseUpdateInput(draft, isActiveWarehouse(warehouse))
      );
      const next = normalizeWarehouse(updated) ?? ({ ...warehouse, ...form } as WarehouseView);
      warehouse = next;
      hydrateForm(next);
      editing = false;
      actionMessage = 'Depo güncellendi.';
    } catch (cause) {
      actionError = await explainActionError(cause, 'Depo güncellenemedi.', 'save');
    } finally {
      saving = false;
    }
  }

  function openToggleConfirm() {
    if (!warehouse || warehouse.is_system === true || actionBusy || saving) return;
    actionError = '';
    confirmAction = 'toggle';
    confirmOpen = true;
  }

  function openDeleteConfirm() {
    if (!warehouse || warehouse.is_system === true || !warehouse.can_delete || actionBusy || saving)
      return;
    actionError = '';
    confirmAction = 'delete';
    confirmOpen = true;
  }

  async function toggleActive() {
    if (!warehouse || actionBusy || saving) return;
    const nextActive = !isActiveWarehouse(warehouse);
    actionBusy = 'toggle';
    actionError = '';
    actionMessage = '';
    try {
      const updated = await setWarehouseActive(warehouse, currentVersion(), nextActive);
      const next = normalizeWarehouse(updated) ?? { ...warehouse, is_active: nextActive };
      warehouse = next;
      hydrateForm(next);
      actionMessage = nextActive ? 'Depo aktifleştirildi.' : 'Depo pasifleştirildi.';
    } catch (cause) {
      throw new Error(await explainActionError(cause, 'Depo durumu güncellenemedi.', 'toggle'));
    } finally {
      actionBusy = '';
    }
  }

  async function remove() {
    if (!warehouse || actionBusy || saving || !warehouse.can_delete) return;
    actionBusy = 'delete';
    actionError = '';
    actionMessage = '';
    try {
      await deleteWarehouse(warehouse.id, currentVersion());
      await goto('/stok/depolar');
    } catch (cause) {
      throw new Error(await explainActionError(cause, 'Depo silinemedi.', 'delete'));
    } finally {
      actionBusy = '';
    }
  }

  async function confirmActionHandler() {
    if (confirmAction === 'delete') await remove();
    else if (confirmAction === 'toggle') await toggleActive();
    confirmOpen = false;
    confirmAction = null;
  }

  const confirmTitle = $derived(confirmAction === 'delete' ? 'Depoyu sil' : 'Depo durumu');
  const confirmDescription = $derived(
    confirmAction === 'delete'
      ? 'Depo silinecektir. Silinsin mi?'
      : isActiveWarehouse(warehouse ?? { is_active: true })
        ? 'Depo pasifleştirilecek. Geçmiş stok hareketleri korunur.'
        : 'Depo yeniden aktifleştirilecek ve yeni stok işlemlerinde kullanılabilecektir.'
  );
  const confirmLabel = $derived(
    confirmAction === 'delete'
      ? 'Sil'
      : isActiveWarehouse(warehouse ?? { is_active: true })
        ? 'Pasifleştir'
        : 'Aktifleştir'
  );

  onMount(() => {
    void load();
    void loadSession();
  });
</script>

<svelte:head><title>{warehouse?.name ?? 'Depo'} · Varya One</title></svelte:head>

{#if loading}
  <div class="panel loading" role="status">Depo yükleniyor…</div>
{:else if !warehouse}
  <section class="panel error-panel" role="alert">
    <strong>Depo açılamadı.</strong>
    <span>{error || 'Depo bulunamadı.'}</span>
    <div class="error-actions">
      <Button variant="outline" onclick={() => void load()}>
        <RefreshCw data-icon="inline-start" aria-hidden="true" />Yeniden dene
      </Button>
      <a class="button secondary" href="/stok/depolar">Listeye dön</a>
    </div>
  </section>
{:else}
  <header class="page-header">
    <div>
      <a class="back" href="/stok/depolar">
        <ArrowLeft data-icon="inline-start" aria-hidden="true" />Depolar listesi
      </a>
      <div class="title-row">
        <h1>{warehouse.code || warehouse.name}</h1>
        <DocumentStatus status={isActiveWarehouse(warehouse) ? 'ACTIVE' : 'INACTIVE'} />
      </div>
      <p class="meta">
        {warehouse.name} · {warehouseTypeLabel(warehouse)}
      </p>
    </div>
    {#if permissions.includes('organization.warehouse.manage') && warehouse.is_system !== true}
      <div class="page-actions">
        <Button
          variant="outline"
          disabled={saving || Boolean(actionBusy) || refreshing}
          onclick={openEditor}
        >
          <Pencil size={14} data-icon="inline-start" aria-hidden="true" />Düzenle
        </Button>
        <Button
          variant="outline"
          disabled={saving || Boolean(actionBusy) || refreshing}
          onclick={openToggleConfirm}
        >
          {#if actionBusy === 'toggle'}<LoaderCircle
              data-icon="inline-start"
              aria-hidden="true"
            />{/if}
          {isActiveWarehouse(warehouse) ? 'Pasifleştir' : 'Aktifleştir'}
        </Button>
        {#if warehouse.can_delete}
          <Button
            variant="danger"
            disabled={saving || Boolean(actionBusy) || refreshing}
            onclick={openDeleteConfirm}
          >
            <Trash2 data-icon="inline-start" aria-hidden="true" />Sil
          </Button>
        {/if}
      </div>
    {/if}
  </header>

  <ConfirmDialog
    bind:open={confirmOpen}
    title={confirmTitle}
    description={confirmDescription}
    {confirmLabel}
    onConfirm={confirmActionHandler}
  />

  {#if actionMessage}<p class="notice success" role="status">{actionMessage}</p>{/if}
  {#if actionError}<p class="notice error" role="alert">{actionError}</p>{/if}

  {#if editing}
    <section class="panel editor" aria-labelledby="warehouse-editor-title">
      <div class="section-heading">
        <div>
          <h2 id="warehouse-editor-title">Depo bilgilerini düzenle</h2>
        </div>
        <Button variant="ghost" onclick={() => (editing = false)}>Vazgeç</Button>
      </div>
      <form
        onsubmit={(event) => {
          event.preventDefault();
          void save();
        }}
      >
        <Field.FieldGroup class="warehouse-form-grid">
          <Field.Field>
            <Field.FieldLabel for="warehouse-code">Depo kodu</Field.FieldLabel>
            <Input id="warehouse-code" bind:value={form.code} maxlength={40} />
          </Field.Field>
          <Field.Field>
            <Field.FieldLabel for="warehouse-name">Depo adı</Field.FieldLabel>
            <Input id="warehouse-name" bind:value={form.name} required />
          </Field.Field>
          <Field.Field>
            <Field.FieldLabel for="warehouse-type">Depo türü</Field.FieldLabel>
            <Input id="warehouse-type" value={warehouseTypeLabel(warehouse)} readonly />
          </Field.Field>
          <Field.Field>
            <Field.FieldLabel for="warehouse-address">Adres</Field.FieldLabel>
            <Input id="warehouse-address" bind:value={form.address} />
          </Field.Field>
          <div class="form-actions">
            <Button type="submit" disabled={saving || Boolean(actionBusy)}>
              {#if saving}<LoaderCircle data-icon="inline-start" aria-hidden="true" /> Kaydediliyor…{:else}<Save
                  data-icon="inline-start"
                  aria-hidden="true"
                /> Kaydet{/if}
            </Button>
          </div>
        </Field.FieldGroup>
      </form>
    </section>
  {/if}

  {#if refreshing}<p class="refreshing" role="status">Güncel depo bilgileri yükleniyor…</p>{/if}

  <section class="panel detail-panel" aria-labelledby="warehouse-details-title">
    <div class="section-heading">
      <div>
        <h2 id="warehouse-details-title">Depo bilgileri</h2>
      </div>
      <span class="status-text">{isActiveWarehouse(warehouse) ? 'Aktif' : 'Pasif'}</span>
    </div>
    <dl class="field-grid">
      <div class="field">
        <dt>Depo kodu</dt>
        <dd>{warehouse.code || '—'}</dd>
      </div>
      <div class="field">
        <dt>Depo adı</dt>
        <dd>{warehouse.name || '—'}</dd>
      </div>
      <div class="field">
        <dt>Depo türü</dt>
        <dd>{warehouseTypeLabel(warehouse)}</dd>
      </div>
      <div class="field">
        <dt>Durum</dt>
        <dd>{isActiveWarehouse(warehouse) ? 'Aktif' : 'Pasif'}</dd>
      </div>
      <div class="field">
        <dt>Sorumlu</dt>
        <dd>{warehouse.responsible_name ?? warehouse.responsible_user_name ?? '—'}</dd>
      </div>
      <div class="field">
        <dt>Adres</dt>
        <dd>{warehouse.address || '—'}</dd>
      </div>
      <div class="field">
        <dt>Oluşturma zamanı</dt>
        <dd>{warehouse.created_at ? formatDate(warehouse.created_at, true) : '—'}</dd>
      </div>
      <div class="field">
        <dt>Son güncelleme</dt>
        <dd>{warehouse.updated_at ? formatDate(warehouse.updated_at, true) : '—'}</dd>
      </div>
    </dl>
  </section>

  {#if warehouse.stock_positions?.length}
    <section class="panel table-panel" aria-labelledby="stock-status-title">
      <div class="section-heading">
        <div>
          <h2 id="stock-status-title">Stok durumu</h2>
          <p>Bu depodaki stok bakiyeleri.</p>
        </div>
        <span>{warehouse.stock_positions.length} kayıt</span>
      </div>
      <div class="table-scroll">
        <table>
          <thead>
            <tr><th>SKU</th><th>Stok</th><th>Fiziki</th><th>Rezerve</th><th>Kullanılabilir</th></tr>
          </thead>
          <tbody>
            {#each warehouse.stock_positions as position}
              <tr>
                <td>{position.sku ?? position.product_code ?? '—'}</td>
                <td>{position.product_name ?? '—'}</td>
                <td>{formatQuantity(String(position.physical_quantity ?? '0'))}</td>
                <td>{formatQuantity(String(position.reserved_quantity ?? '0'))}</td>
                <td>{formatQuantity(String(position.available_quantity ?? '0'))}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    </section>
  {/if}
{/if}

<style>
  .back {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    margin-bottom: 6px;
    color: var(--primary);
    font-size: 12px;
    text-decoration: none;
  }
  .title-row,
  .page-actions,
  .section-heading,
  .error-actions,
  .form-actions {
    display: flex;
    align-items: center;
    gap: 10px;
  }
  .title-row {
    flex-wrap: wrap;
  }
  .meta,
  .section-heading p,
  .refreshing {
    margin: 5px 0 0;
    color: var(--text-muted);
    font-size: 12px;
  }
  .page-actions {
    flex-wrap: wrap;
    justify-content: flex-end;
  }
  .notice {
    margin: 14px 0;
    padding: 10px 12px;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    font-size: 12px;
  }
  .notice.success {
    color: var(--success);
  }
  .notice.error {
    color: var(--danger);
  }
  .editor,
  .detail-panel,
  .table-panel {
    margin-top: 16px;
  }
  .section-heading {
    justify-content: space-between;
    align-items: flex-start;
    margin-bottom: 16px;
  }
  .section-heading h2 {
    margin: 0;
    font-size: 16px;
  }
  .section-heading > span,
  .status-text {
    color: var(--text-muted);
    font-size: 12px;
  }
  .status-text {
    color: var(--primary);
    font-weight: 700;
  }
  :global(.warehouse-form-grid) {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 14px;
  }
  .form-actions {
    grid-column: 1 / -1;
    justify-content: flex-end;
  }
  .field-grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 14px 24px;
    margin: 0;
  }
  .field {
    min-width: 0;
  }
  .field dt {
    margin-bottom: 4px;
    color: var(--text-muted);
    font-size: 11px;
  }
  .field dd {
    margin: 0;
    overflow-wrap: anywhere;
    color: var(--text);
    font-size: 13px;
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
  @media (max-width: 700px) {
    .page-header,
    .page-actions,
    :global(.warehouse-form-grid),
    .field-grid {
      grid-template-columns: 1fr;
    }
    .page-header,
    .page-actions {
      align-items: stretch;
    }
    .page-actions {
      justify-content: flex-start;
    }
    .form-actions {
      grid-column: auto;
      justify-content: stretch;
    }
  }
</style>
