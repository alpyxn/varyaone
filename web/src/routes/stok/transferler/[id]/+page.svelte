<script lang="ts">
  import { page } from '$app/state';
  import { goto } from '$app/navigation';
  import { onMount } from 'svelte';
  import { ArrowLeft, LoaderCircle, RefreshCw, X } from '@lucide/svelte';
  import { api, type Session } from '$lib/api';
  import { Button } from '$lib/components/ui/button';
  import { formatDate, formatQuantity } from '$lib/design/formatters';

  type RecordValue = Record<string, unknown>;
  let session = $state<Session | null>(null);
  let record = $state<RecordValue>();
  let loading = $state(true);
  let error = $state('');
  let actionError = $state('');
  let actionBusy = $state(false);
  let confirmAction = $state<'receive' | 'cancel' | ''>('');

  function first(item: RecordValue | undefined, keys: string | string[]) {
    if (!item) return undefined;
    for (const key of Array.isArray(keys) ? keys : [keys]) {
      const value = key.split('.').reduce<unknown>((current, part) => {
        if (!current || typeof current !== 'object') return undefined;
        return (current as RecordValue)[part];
      }, item);
      if (value !== undefined && value !== null && value !== '') return value;
    }
    return undefined;
  }

  function text(item: RecordValue | undefined, keys: string | string[], fallback = '—') {
    const value = first(item, keys);
    return value === undefined || value === null || value === '' ? fallback : String(value);
  }

  function normalize(payload: unknown): RecordValue {
    if (!payload || typeof payload !== 'object') return {};
    const value = payload as RecordValue;
    return value.item && typeof value.item === 'object' ? (value.item as RecordValue) : value;
  }

  function linesOf(item: RecordValue | undefined) {
    const lines = first(item, 'lines');
    return Array.isArray(lines)
      ? lines.filter((line): line is RecordValue => Boolean(line && typeof line === 'object'))
      : [];
  }

  function hasVariantIdentity(line: RecordValue) {
    return Boolean(
      first(line, [
        'variant_id',
        'variant_code',
        'variant_name',
        'variant.variant_code',
        'variant.name',
        'variant_display',
        'variant_attributes',
        'attributes'
      ])
    );
  }

  function variantLabel(line: RecordValue) {
    const raw = first(line, ['variant_display', 'variant_attributes', 'attributes']);
    if (raw && typeof raw === 'object' && !Array.isArray(raw)) {
      const attributes = Object.entries(raw as RecordValue)
        .filter(([, value]) => value !== undefined && value !== null && value !== '')
        .map(([key, value]) => `${key}: ${attributeText(value)}`)
        .join(' · ');
      if (attributes) return attributes;
    }
    return text(line, [
      'variant_name',
      'variant.name',
      'variant_code',
      'variant.variant_code',
      'variant_id'
    ]);
  }

  function attributeText(value: unknown): string {
    if (Array.isArray(value)) return value.map(attributeText).join(', ');
    if (value && typeof value === 'object') {
      const item = value as RecordValue;
      return String(item.name ?? item.label ?? item.value ?? item.code ?? '');
    }
    return String(value);
  }

  function statusLabel(value: unknown) {
    switch (String(value ?? '').toUpperCase()) {
      case 'DRAFT':
        return 'Taslak';
      case 'IN_TRANSIT':
      case 'PARTIALLY_RECEIVED':
      case 'REQUESTED':
      case 'APPROVED':
        return 'Sevk sırasında';
      case 'RECEIVED':
        return 'Teslim alındı';
      case 'CANCELLED':
        return 'Sevk iptal oldu';
      default:
        return text(record, 'state');
    }
  }

  function transferState() {
    return String(first(record, ['state', 'status']) ?? '').toUpperCase();
  }

  function transferTypeLabel() {
    return String(first(record, ['transfer_type', 'type']) ?? '').toUpperCase() === 'QUICK'
      ? 'Hızlı Transfer'
      : 'Sevk / Teslim';
  }

  function warehouseID(kind: 'source' | 'destination') {
    return String(
      first(
        record,
        kind === 'source'
          ? ['source_warehouse_id', 'source_warehouse.id']
          : ['destination_warehouse_id', 'destination_warehouse.id']
      ) ?? ''
    );
  }

  function warehouseName(kind: 'source' | 'destination') {
    return text(
      record,
      kind === 'source'
        ? ['source_warehouse_name', 'source_warehouse.name', 'source_warehouse_id']
        : ['destination_warehouse_name', 'destination_warehouse.name', 'destination_warehouse_id']
    );
  }

  function canReceive() {
    return (
      ['IN_TRANSIT', 'PARTIALLY_RECEIVED'].includes(transferState()) &&
      Boolean(session?.permissions.includes('inventory.transfer.receive'))
    );
  }

  function canCancel() {
    return (
      ['IN_TRANSIT', 'PARTIALLY_RECEIVED'].includes(transferState()) &&
      Boolean(session?.permissions.includes('inventory.transfer.request'))
    );
  }

  function openConfirm(action: 'receive' | 'cancel') {
    if ((action === 'receive' && !canReceive()) || (action === 'cancel' && !canCancel())) return;
    actionError = '';
    confirmAction = action;
  }

  function closeConfirm() {
    if (!actionBusy) confirmAction = '';
  }

  function actionDescription() {
    return confirmAction === 'receive'
      ? 'Transfer teslim alınacaktır. Devam etmek istiyor musunuz?'
      : 'Transfer iptal edilecektir. Çıkış bakiyesi geri döner. Devam etmek istiyor musunuz?';
  }

  async function runAction() {
    const id = page.params.id;
    if (!id || !confirmAction || actionBusy) return;
    actionBusy = true;
    actionError = '';
    try {
      const random =
        typeof crypto !== 'undefined' && 'randomUUID' in crypto
          ? crypto.randomUUID()
          : `${Date.now()}-${Math.random().toString(36).slice(2)}`;
      const action = confirmAction;
      const version = first(record, 'version');
      if (version === undefined) {
        actionError = 'Transfer bilgisi güncel değil. Lütfen sayfayı yenileyin.';
        return;
      }
      const response = await api<unknown>(
        `/warehouse-transfers/${encodeURIComponent(id)}/${action}`,
        {
          method: 'POST',
          headers: {
            'If-Match': `"${String(version)}"`,
            'Idempotency-Key': `transfer-${action}:${random}`
          },
          body: '{}'
        }
      );
      record = normalize(response);
      confirmAction = '';
    } catch (cause) {
      actionError = errorMessage(cause, 'Transfer işlemi tamamlanamadı.');
    } finally {
      actionBusy = false;
    }
  }

  function errorMessage(cause: unknown, fallback: string) {
    if (typeof cause === 'object' && cause && 'message' in cause) {
      const message = String((cause as { message?: unknown }).message ?? '').trim();
      if (message) return message;
    }
    return fallback;
  }

  async function load() {
    const id = page.params.id;
    if (!id) {
      error = 'Kayıt kimliği bulunamadı.';
      loading = false;
      return;
    }
    loading = true;
    error = '';
    try {
      const [sessionResult, transferResult] = await Promise.all([
        api<Session>('/session'),
        api<unknown>(`/warehouse-transfers/${encodeURIComponent(id)}`)
      ]);
      session = sessionResult;
      record = normalize(transferResult);
      if (!Object.keys(record).length) error = 'Transfer bulunamadı.';
    } catch (cause) {
      error = errorMessage(cause, 'Transfer okunamadı.');
    } finally {
      loading = false;
    }
  }

  onMount(() => {
    void load();
  });

  const transferLines = $derived(linesOf(record));
  const showVariants = $derived(transferLines.some(hasVariantIdentity));
</script>

<svelte:head
  ><title>{text(record, ['transfer_no', 'id'], 'Transfer')} · Varya One</title></svelte:head
>

{#if loading}
  <section class="state-card" role="status">
    <LoaderCircle class="spin" size={18} />Transfer yükleniyor…
  </section>
{:else if error}
  <section class="state-card error" role="alert">
    <strong>{error}</strong><Button variant="outline" onclick={() => void load()}
      ><RefreshCw size={14} />Tekrar dene</Button
    >
  </section>
{:else if record}
  <div class="page-toolbar">
    <Button variant="ghost" onclick={() => void goto('/stok/transferler')}
      ><ArrowLeft size={15} />Transferlere dön</Button
    >
    <Button variant="outline" onclick={() => void load()}><RefreshCw size={14} />Yenile</Button>
  </div>

  <header class="detail-header">
    <div>
      <h1>{text(record, ['transfer_no', 'business_number', 'id'])}</h1>
      <div class="warehouse-flow" aria-label="Transfer depoları">
        <div class="warehouse-cell">
          <span>Çıkış deposu</span>
          {#if warehouseID('source')}<a
              href={`/stok/depolar/${encodeURIComponent(warehouseID('source'))}`}
              >{warehouseName('source')}</a
            >{:else}<strong>{warehouseName('source')}</strong>{/if}
        </div>
        <span class="warehouse-arrow" aria-hidden="true">→</span>
        <div class="warehouse-cell">
          <span>Varış deposu</span>
          {#if warehouseID('destination')}<a
              href={`/stok/depolar/${encodeURIComponent(warehouseID('destination'))}`}
              >{warehouseName('destination')}</a
            >{:else}<strong>{warehouseName('destination')}</strong>{/if}
        </div>
      </div>
    </div>
    <div class="header-actions">
      <span class="status">{statusLabel(first(record, ['state', 'status']))}</span>
      {#if canReceive()}<Button onclick={() => openConfirm('receive')}>Teslim al</Button>{/if}
      {#if canCancel()}<Button variant="outline" onclick={() => openConfirm('cancel')}
          >Sevk iptal et</Button
        >{/if}
    </div>
  </header>

  {#if actionError}<div class="action-error" role="alert">{actionError}</div>{/if}

  <section class="summary-grid" aria-label="Transfer özeti">
    <div>
      <span>Transfer tipi</span><strong>{transferTypeLabel()}</strong>
    </div>
    <div>
      <span>Oluşturma tarihi</span><strong>{formatDate(text(record, 'created_at', ''))}</strong>
    </div>
    <div>
      <span>Varış tarihi</span><strong>{formatDate(text(record, 'arrival_at', ''))}</strong>
    </div>
    <div><span>Satır sayısı</span><strong>{transferLines.length}</strong></div>
  </section>

  <section class="panel">
    <div class="section-heading">
      <div>
        <h2>Stoklar</h2>
        <p>Transfer fişindeki çıkış pozisyonları</p>
      </div>
    </div>
    {#if transferLines.length === 0}
      <div class="empty-state">Bu transferde stok satırı bulunmuyor.</div>
    {:else}
      <div class="line-table" role="table" aria-label="Transfer stokları">
        <div class="line-table-head" role="row">
          <span role="columnheader">Stok kartı</span>
          <span role="columnheader">Varyant / SKU</span>
          <span role="columnheader">Miktar</span>
        </div>
        <div class="line-list" role="rowgroup">
          {#each transferLines as line}
            {@const productID = first(line, ['product_id', 'product.id'])}
            {@const productName = text(
              line,
              ['product_name', 'product.name', 'product_code', 'product_id'],
              'Stok kartı'
            )}
            <div class="line-summary" role="row">
              <div class="line-product" role="cell">
                {#if productID}<a href={`/stok/urunler/${encodeURIComponent(String(productID))}`}
                    >{productName}</a
                  >{:else}<strong>{productName}</strong>{/if}
                {#if showVariants && hasVariantIdentity(line)}<small>{variantLabel(line)}</small
                  >{/if}
              </div>
              <div class="line-variant" role="cell">
                {#if showVariants}{variantLabel(line)}{:else}—{/if}
              </div>
              <div class="line-quantity" role="cell">
                <span>Miktar</span>
                <strong>{formatQuantity(text(line, ['sent_quantity', 'quantity'], '0'))}</strong>
              </div>
            </div>
          {/each}
        </div>
      </div>
    {/if}
  </section>

  {#if confirmAction}
    <div
      class="dialog-backdrop"
      role="presentation"
      onclick={(event) => event.target === event.currentTarget && closeConfirm()}
    >
      <dialog open class="dialog" aria-labelledby="confirm-title">
        <header>
          <h2 id="confirm-title">Transfer işlemi</h2>
          <button class="close" type="button" aria-label="Kapat" onclick={closeConfirm}
            ><X size={18} /></button
          >
        </header>
        <p>{actionDescription()}</p>
        <footer>
          <Button variant="outline" disabled={actionBusy} onclick={closeConfirm}>Vazgeç</Button>
          <Button disabled={actionBusy} onclick={() => void runAction()}
            >{#if actionBusy}<span class="spin"><LoaderCircle size={15} /></span
              >{/if}{confirmAction === 'receive' ? 'Teslim al' : 'Sevk iptal et'}</Button
          >
        </footer>
      </dialog>
    </div>
  {/if}
{/if}

<style>
  .page-toolbar,
  .detail-header,
  .header-actions,
  .summary-grid,
  .section-heading,
  .dialog header,
  .dialog footer {
    display: flex;
    align-items: center;
  }
  .page-toolbar {
    justify-content: space-between;
    gap: 8px;
    margin-bottom: 12px;
  }
  .detail-header {
    justify-content: space-between;
    align-items: flex-start;
    gap: 16px;
    margin-bottom: 16px;
  }
  h1 {
    margin: 3px 0 0;
    font-size: 22px;
  }
  .warehouse-flow {
    display: flex;
    align-items: center;
    gap: 10px;
    margin-top: 10px;
  }
  .warehouse-cell {
    display: grid;
    min-width: 150px;
    gap: 3px;
    padding: 8px 10px;
    border: 1px solid var(--border);
    border-radius: 8px;
    background: var(--surface);
  }
  .warehouse-cell span,
  .line-quantity span {
    color: var(--text-muted);
    font-size: 10px;
    font-weight: 700;
    letter-spacing: 0.03em;
    text-transform: uppercase;
  }
  .warehouse-cell a,
  .warehouse-cell strong {
    overflow: hidden;
    color: var(--text);
    font-size: 12px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .warehouse-cell a:hover {
    color: var(--primary);
  }
  .warehouse-arrow {
    color: var(--text-muted);
    font-size: 18px;
  }
  .header-actions {
    flex-wrap: wrap;
    justify-content: flex-end;
    gap: 8px;
  }
  .status {
    padding: 6px 9px;
    border-radius: 999px;
    background: var(--background);
    color: var(--text-muted);
    font-size: 11px;
    font-weight: 700;
  }
  .summary-grid {
    display: grid;
    grid-template-columns: repeat(4, minmax(0, 1fr));
    gap: 1px;
    margin-bottom: 14px;
    border: 1px solid var(--border);
    border-radius: 9px;
    overflow: hidden;
    background: var(--border);
  }
  .summary-grid div {
    display: grid;
    gap: 4px;
    padding: 12px;
    background: var(--surface);
  }
  .summary-grid span,
  .section-heading p {
    color: var(--text-muted);
    font-size: 11px;
  }
  .panel {
    border: 1px solid var(--border);
    border-radius: 10px;
    background: var(--surface);
  }
  .section-heading {
    justify-content: space-between;
    padding: 14px 16px;
    border-bottom: 1px solid var(--border);
  }
  h2 {
    margin: 0;
    font-size: 15px;
  }
  .section-heading p {
    margin: 3px 0 0;
  }
  .line-list {
    display: grid;
  }
  .line-table-head {
    display: grid;
    grid-template-columns: minmax(0, 1.3fr) minmax(0, 1fr) minmax(86px, 0.35fr);
    gap: 16px;
    padding: 9px 16px;
    border-bottom: 1px solid var(--border);
    background: var(--surface-muted);
    color: var(--text-muted);
    font-size: 10px;
    font-weight: 750;
    letter-spacing: 0.03em;
    text-transform: uppercase;
  }
  .line-summary {
    display: grid;
    grid-template-columns: minmax(0, 1.3fr) minmax(0, 1fr) minmax(86px, 0.35fr);
    align-items: center;
    gap: 16px;
    padding: 12px 16px;
    border-bottom: 1px solid var(--border);
  }
  .line-summary:last-child {
    border-bottom: 0;
  }
  .line-product {
    display: grid;
    min-width: 0;
    gap: 4px;
  }
  .line-product small {
    overflow: hidden;
    color: var(--text-muted);
    font-size: 11px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .line-variant {
    overflow: hidden;
    color: var(--text-muted);
    font-size: 12px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .line-quantity {
    display: grid;
    flex: 0 0 auto;
    justify-items: end;
    gap: 3px;
  }
  a {
    color: var(--primary);
    text-decoration: none;
    font-weight: 650;
  }
  a:hover {
    text-decoration: underline;
  }
  .empty-state,
  .state-card,
  .action-error {
    display: flex;
    align-items: center;
    gap: 9px;
    padding: 16px;
    color: var(--text-muted);
    font-size: 12px;
  }
  .state-card {
    justify-content: space-between;
    border: 1px solid var(--border);
    border-radius: 10px;
    background: var(--surface);
  }
  .state-card.error,
  .action-error {
    color: var(--danger, #b42318);
  }
  .action-error {
    margin-bottom: 12px;
    border-radius: 8px;
    background: color-mix(in srgb, var(--danger, #b42318) 10%, var(--surface));
  }
  .dialog-backdrop {
    position: fixed;
    z-index: 50;
    inset: 0;
    display: grid;
    place-items: center;
    padding: 20px;
    background: rgb(15 23 42 / 42%);
  }
  .dialog {
    position: static;
    width: min(440px, 100%);
    margin: 0;
    border: 1px solid var(--border);
    border-radius: 12px;
    background: var(--surface);
    box-shadow: 0 20px 60px rgb(15 23 42 / 25%);
  }
  .dialog header,
  .dialog footer {
    justify-content: space-between;
    gap: 10px;
    padding: 14px 16px;
  }
  .dialog header {
    border-bottom: 1px solid var(--border);
  }
  .dialog footer {
    justify-content: flex-end;
    border-top: 1px solid var(--border);
  }
  .dialog h2 {
    font-size: 16px;
  }
  .dialog p {
    margin: 16px;
    color: var(--text-muted);
    font-size: 12px;
  }
  .close {
    border: 0;
    background: transparent;
    color: var(--text-muted);
    cursor: pointer;
  }
  .spin {
    display: inline-flex;
    animation: spin 0.9s linear infinite;
  }
  @keyframes spin {
    to {
      transform: rotate(360deg);
    }
  }
  @media (max-width: 720px) {
    .line-table-head,
    .line-summary {
      grid-template-columns: minmax(0, 1fr) minmax(86px, 0.45fr);
    }
    .line-table-head span:nth-child(2),
    .line-variant {
      display: none;
    }
    .detail-header {
      flex-direction: column;
    }
    .header-actions {
      justify-content: flex-start;
    }
    .warehouse-flow {
      align-items: stretch;
      flex-direction: column;
    }
    .warehouse-cell {
      min-width: 0;
    }
    .warehouse-arrow {
      align-self: center;
      transform: rotate(90deg);
    }
    .summary-grid {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
  }
</style>
