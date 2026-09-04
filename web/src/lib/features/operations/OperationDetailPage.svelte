<script lang="ts">
  import { page } from '$app/state';
  import { goto } from '$app/navigation';
  import {
    ArrowLeft,
    ExternalLink,
    LoaderCircle,
    Lock,
    Pencil,
    Printer,
    RefreshCw,
    Save,
    Truck,
    Trash2,
    Undo2
  } from '@lucide/svelte';
  import { onMount } from 'svelte';
  import { api, type Company, type Session } from '$lib/api';
  import { Button } from '$lib/components/ui/button';
  import { DocumentStatus } from '$lib/components/varya/document-status';
  import { ReasonDialog } from '$lib/components/varya/reason-dialog';
  import { ConfirmDialog } from '$lib/components/varya/confirm-dialog';
  import {
    formatDate,
    formatMoney,
    formatQuantity,
    formatQuantityWithUnit,
    formatUnitPrice
  } from '$lib/design/formatters';
  import { localizedEnum } from '$lib/design/labels';
  import { ph, printDocument } from '$lib/design/print';
  import { deleteWarehouse, updateWarehouse } from '$lib/features/warehouses/api';
  import type { WarehouseType } from '$lib/features/warehouses/types';
  import {
    configFor,
    firstValue,
    hasValue,
    textValue,
    type Field,
    type OperationDetailKind,
    type RecordValue
  } from './operation-detail-config';

  type OperationListResponse = { items?: RecordValue[] };

  type Props = { kind: OperationDetailKind; endpoint?: string; listPath?: string; title?: string };
  let { kind, endpoint, listPath, title }: Props = $props();
  let record = $state<RecordValue>();
  let loading = $state(true);
  let error = $state('');
  let actionBusy = $state(false);
  let actionMessage = $state('');
  let reasonDialogOpen = $state(false);
  let permissions = $state<string[]>([]);
  let warehouseEditing = $state(false);
  let warehouseSaving = $state(false);
  let warehouseToggling = $state(false);
  let warehouseDeleting = $state(false);
  let warehouseError = $state('');
  let transferAction = $state<'complete' | ''>('');
  let completeTransferDialogOpen = $state(false);
  let cancelTransferDialogOpen = $state(false);
  let cancelTransferBusy = $state(false);
  let warehouseTransfers = $state<RecordValue[]>([]);
  let companyName = $state('');
  let company = $state<Company>();
  let warehouseForm = $state<{
    code: string;
    name: string;
    type: WarehouseType;
    address: string;
    is_active: boolean;
  }>({
    code: '',
    name: '',
    type: 'STANDARD',
    address: '',
    is_active: true
  });

  function hasStockMovementDetails(value: unknown) {
    if (!value || typeof value !== 'object') return false;
    const item = value as RecordValue;
    return Boolean(
      firstValue(item, ['product_id', 'product_name', 'warehouse_id', 'movement_type', 'lines'])
    );
  }

  function compactDecimal(value: unknown) {
    const text = String(value ?? '')
      .trim()
      .replace(',', '.');
    if (!text) return '—';
    return (
      text
        .replace(/(\.\d*?[1-9])0+$/, '$1')
        .replace(/\.0+$/, '')
        .replace(/\.$/, '') || '0'
    );
  }

  function fieldKeyList(field: Field) {
    return Array.isArray(field.key) ? field.key : [field.key];
  }

  function transferTypeLabel(value: unknown) {
    switch (String(value ?? '').toUpperCase()) {
      case 'QUICK':
        return 'Hızlı Transfer';
      case 'WORKFLOW':
        return 'Sevk / Teslim';
      default:
        return textValue(value);
    }
  }

  function transferStatusLabel(value: unknown) {
    switch (String(value ?? '').toUpperCase()) {
      case 'DRAFT':
        return 'Taslak';
      case 'REQUESTED':
      case 'APPROVED':
        return 'Sevk bekliyor';
      case 'IN_TRANSIT':
      case 'PARTIALLY_RECEIVED':
        return 'Sevk sırasında';
      case 'RECEIVED':
        return 'Başarıyla sevk edildi';
      case 'CANCELLED':
        return 'Sevk iptal oldu (Çıkış deposuna geri döndü)';
      default:
        return textValue(value);
    }
  }

  function fieldValue(item: RecordValue, field: Field) {
    if (kind === 'transfer' && fieldKeyList(field).includes('__transfer_status')) {
      return firstValue(record, ['state', 'status']);
    }
    return firstValue(item, field.key);
  }

  function isVariantActive(item: RecordValue) {
    const value = firstValue(item, ['variant_is_active', 'variant.is_active']);
    return value !== false && String(value).toLowerCase() !== 'false';
  }

  function fieldText(item: RecordValue, field: Field) {
    const value = fieldValue(item, field);
    if (value === undefined || value === null || value === '') return '—';
    const keys = fieldKeyList(field);
    if (kind === 'transfer' && keys.includes('transfer_type')) {
      return transferTypeLabel(value);
    }
    if (kind === 'transfer' && (keys.includes('__transfer_status') || keys.includes('state'))) {
      return transferStatusLabel(value);
    }
    if (field.kind === 'date') return formatDate(String(value));
    if (field.kind === 'datetime') return formatDate(String(value), true);
    if (field.kind === 'decimal') return compactDecimal(value);
    if (field.kind === 'money') {
      const currency = textValue(firstValue(item, field.currencyKey ?? 'currency'));
      return formatMoney(String(value), currency === '—' ? 'TRY' : currency);
    }
    if (field.kind === 'quantity') {
      const keys = fieldKeyList(field);
      if (
        kind === 'stock-movement' &&
        (keys.includes('base_quantity') || keys.includes('quantity_delta'))
      ) {
        return formatQuantityWithUnit(
          String(value),
          String(firstValue(item, ['stock_unit', 'unit_code', 'unit']) ?? 'ADET')
        );
      }
      return formatQuantity(String(value));
    }
    if (typeof value === 'boolean') {
      const keys = Array.isArray(field.key) ? field.key : [field.key];
      return value ? 'Evet' : 'Hayır';
    }
    const text = localizedEnum(value, field.key);
    if (field.appendInactiveLabel && !isVariantActive(item)) return `${text} · Pasif`;
    return text;
  }

  // Ref alanlarında değerin kendisi (UUID) değil, sabit bir ifade gösterilir.
  // Bağlantı kurulamadıysa gösterilecek bir şey yoktur.
  function cellText(item: RecordValue, field: Field, href: string | undefined) {
    if (field.kind === 'ref') return href ? (field.refText ?? 'Kaydı aç') : '—';
    return fieldText(item, field);
  }

  function linkAriaLabel(item: RecordValue, field: Field) {
    if (field.kind === 'ref') return `${field.label} bağlantısını aç`;
    return `${fieldText(item, field)} bağlantısını aç`;
  }

  function stockSourceDocumentHref(item: RecordValue) {
    const sourceID = firstValue(item, ['source_id', 'source.document_id', 'source.id']);
    const sourceType = String(
      firstValue(item, [
        'source_document_type',
        'source.document_type',
        'source_type',
        'source.type'
      ]) ?? ''
    ).toUpperCase();
    const paths: Record<string, string> = {
      SALES_QUOTE: '/satis/teklifler',
      SALES_ORDER: '/satis/siparisler',
      SALES_DISPATCH: '/satis/irsaliyeler',
      SALES_DELIVERY: '/satis/irsaliyeler',
      SALES_INVOICE: '/satis/faturalar',
      SALES_RETURN: '/satis/iadeler',
      SALES_RETURN_INVOICE: '/satis/iadeler',
      PURCHASE_ORDER: '/alis/siparisler',
      GOODS_RECEIPT: '/alis/irsaliyeler',
      PURCHASE_RECEIPT: '/alis/irsaliyeler',
      PURCHASE_DELIVERY: '/alis/irsaliyeler',
      PURCHASE_INVOICE: '/alis/faturalar',
      PURCHASE_RETURN: '/alis/iadeler',
      PURCHASE_RETURN_INVOICE: '/alis/iadeler'
    };
    const path = paths[sourceType];
    if (!path || sourceID === undefined || sourceID === null || sourceID === '') return undefined;
    return `${path}/${encodeURIComponent(String(sourceID))}`;
  }

  function hrefFor(item: RecordValue, field: Field) {
    if (kind === 'stock-movement' && fieldKeyList(field).includes('source_document_no')) {
      return stockSourceDocumentHref(item);
    }
    if (fieldKeyList(field).includes('source_label')) {
      const href = firstValue(item, ['source_href']);
      return typeof href === 'string' && href ? href : undefined;
    }
    if (!field.linkPath) return undefined;
    const value = firstValue(item, field.linkKey ?? field.key);
    if (value === undefined || value === null || value === '') return undefined;
    const href = field.linkPath.replace('{id}', encodeURIComponent(String(value)));
    if (!field.linkQueryKey) return href;
    const queryValue = firstValue(item, field.linkQueryKey);
    if (queryValue === undefined || queryValue === null || queryValue === '') return href;
    const queryKey = Array.isArray(field.linkQueryKey) ? field.linkQueryKey[0] : field.linkQueryKey;
    const separator = href.includes('?') ? '&' : '?';
    return `${href}${separator}${encodeURIComponent(queryKey)}=${encodeURIComponent(String(queryValue))}`;
  }

  const config = $derived.by(() => {
    const base = configFor(kind);
    return {
      ...base,
      endpoint: endpoint ?? base.endpoint,
      listPath: listPath ?? base.listPath,
      title: title ?? base.title
    };
  });

  function normalizePayload(payload: unknown): RecordValue {
    if (!payload || typeof payload !== 'object') return {};
    const value = payload as RecordValue;
    if (value.item && typeof value.item === 'object') return value.item as RecordValue;
    return value;
  }

  async function enrichTransferProductNames(value: RecordValue) {
    const lines = listRows(value, 'lines');
    const productIDs = [
      ...new Set(
        lines
          .map((line) => firstValue(line, 'product_id'))
          .filter(
            (productID): productID is string => typeof productID === 'string' && productID !== ''
          )
      )
    ];
    if (!productIDs.length) return value;

    const results = await Promise.allSettled(
      productIDs.map(async (productID) => {
        const [productResult, variantResult] = await Promise.allSettled([
          api<unknown>(`/products/${encodeURIComponent(productID)}`).then(normalizePayload),
          api<{ items?: RecordValue[] }>(`/products/${encodeURIComponent(productID)}/variants`)
        ]);
        return { productID, productResult, variantResult };
      })
    );
    const products = new Map<string, { name?: string; code?: string }>();
    const variants = new Map<string, RecordValue>();
    results.forEach((result, index) => {
      if (result.status !== 'fulfilled') return;
      const product = result.value.productResult;
      if (product.status === 'fulfilled') {
        const name = firstValue(product.value, 'name');
        const code = firstValue(product.value, ['code', 'sku']);
        products.set(productIDs[index], {
          name: typeof name === 'string' && name.trim() ? name.trim() : undefined,
          code: typeof code === 'string' && code.trim() ? code.trim() : undefined
        });
      }
      const variantResult = result.value.variantResult;
      if (variantResult.status === 'fulfilled') {
        for (const variant of variantResult.value.items ?? []) {
          const variantID = firstValue(variant, 'id');
          if (variantID) variants.set(`${productIDs[index]}:${String(variantID)}`, variant);
        }
      }
    });
    value.lines = lines.map((line) => {
      const productID = firstValue(line, 'product_id');
      const product = typeof productID === 'string' ? products.get(productID) : undefined;
      const variantID = firstValue(line, 'variant_id');
      const variant =
        typeof productID === 'string' && variantID
          ? variants.get(`${productID}:${String(variantID)}`)
          : undefined;
      const enriched = { ...line };
      if (product?.name) enriched.product_name = product.name;
      if (product?.code) enriched.product_code = product.code;
      if (variant) {
        const variantCode = firstValue(variant, 'variant_code');
        if (variantCode) enriched.variant_code = variantCode;
        const variantName = firstValue(variant, ['variant_name', 'name']);
        if (variantName) enriched.variant_name = variantName;
        enriched.variant_is_active = firstValue(variant, 'is_active') !== false;
        const attributes = firstValue(variant, 'attributes');
        if (attributes) enriched.variant_attributes = attributes;
      }
      return enriched;
    });
    return value;
  }

  function warehouseTypeValue(value: unknown): WarehouseType {
    const type = String(value ?? 'STANDARD').toUpperCase();
    return ['STANDARD', 'TRANSIT', 'QUARANTINE', 'RETURN'].includes(type)
      ? (type as WarehouseType)
      : 'STANDARD';
  }

  function warehouseTypeLabel(value: WarehouseType) {
    switch (value) {
      case 'TRANSIT':
        return 'Transit';
      case 'QUARANTINE':
        return 'Karantina';
      case 'RETURN':
        return 'İade';
      default:
        return 'Standart';
    }
  }

  function warehouseIsActive(value: RecordValue | undefined) {
    const active = firstValue(value, ['is_active', 'active']);
    return active !== false && String(active).toLowerCase() !== 'false';
  }

  function warehouseIsSystem(value: RecordValue | undefined) {
    const system = firstValue(value, 'is_system');
    return system === true || String(system).toLowerCase() === 'true';
  }

  function warehouseCanDelete(value: RecordValue | undefined) {
    const canDelete = firstValue(value, 'can_delete');
    return canDelete === true || String(canDelete).toLowerCase() === 'true';
  }

  function hydrateWarehouseForm(value: RecordValue) {
    warehouseForm = {
      code: String(firstValue(value, 'code') ?? ''),
      name: String(firstValue(value, 'name') ?? ''),
      type: warehouseTypeValue(firstValue(value, ['type', 'warehouse_type'])),
      address: String(firstValue(value, 'address') ?? ''),
      is_active: warehouseIsActive(value)
    };
  }

  type WarehouseAction = 'save' | 'lifecycle' | 'delete';

  function warehouseErrorCode(cause: unknown) {
    if (typeof cause !== 'object' || !cause) return '';
    const code = (cause as { code?: unknown }).code;
    return typeof code === 'string' ? code : '';
  }

  function warehouseActionError(
    cause: unknown,
    fallback: string,
    action: WarehouseAction = 'save'
  ) {
    const code = warehouseErrorCode(cause);
    if (code === 'WAREHOUSE_HAS_MOVEMENTS' || code === 'WAREHOUSE_HAS_HISTORY') {
      return action === 'delete'
        ? 'Bu depoda hareket bulunduğu için silinemez. Kullanılabilir stoktan çıkarmak için Pasife al seçeneğini kullanın.'
        : fallback;
    }
    if (
      code === 'WAREHOUSE_HAS_OPEN_TRANSFER' ||
      code === 'WAREHOUSE_HAS_DEPENDENCIES' ||
      code === 'WAREHOUSE_IN_USE'
    ) {
      if (action === 'lifecycle') {
        return 'Devam eden transferi bulunan depo pasife alınamaz. Önce transferi tamamlayın veya iptal edin.';
      }
      if (action === 'delete') {
        return 'Depo ilişkili kayıtlar nedeniyle silinemez. Kullanılabilir stoktan çıkarmak için Pasife al seçeneğini kullanın.';
      }
      return 'Depo ilişkili kayıtlar nedeniyle işlem tamamlanamadı.';
    }
    if (code === 'WAREHOUSE_TYPE_IMMUTABLE') return fallback;
    if (typeof cause === 'object' && cause) {
      const message = (cause as { message?: unknown }).message;
      if (typeof message === 'string' && message.trim()) return message;
    }
    return fallback;
  }

  function transferActionError(cause: unknown, fallback: string) {
    if (typeof cause !== 'object' || !cause) return fallback;
    const value = cause as { code?: unknown; message?: unknown };
    switch (value.code) {
      case 'INSUFFICIENT_STOCK':
      case 'SERIAL_NOT_AVAILABLE':
      case 'LOT_EXPIRED':
        return 'Çıkış deposunda yeterli kullanılabilir stok yok.';
      case 'WAREHOUSE_TRANSFER_INVALID_STATE':
        return 'Transfer başka bir kullanıcı tarafından ilerletilmiş olabilir. Sayfayı yenileyin.';
      case 'CONFLICT':
      case 'VERSION_CONFLICT':
      case 'INVENTORY_CONFLICT':
        return 'Transfer güncel değil. Sayfayı yenileyip tekrar deneyin.';
      case 'FORBIDDEN':
        return 'Bu transfer işlemi için yetkiniz yok.';
      default:
        return typeof value.message === 'string' && value.message.trim() ? value.message : fallback;
    }
  }

  function transferState() {
    return String(firstValue(record, ['state', 'status']) ?? '').toUpperCase();
  }

  function canTransferAction(action: 'complete') {
    if (kind !== 'transfer' || !record || transferAction) return false;
    if (action !== 'complete') return false;
    const state = transferState();
    if (state === 'DRAFT' || state === 'REQUESTED') {
      return (
        permissions.includes('inventory.transfer.approve') &&
        permissions.includes('inventory.transfer.ship') &&
        permissions.includes('inventory.transfer.receive')
      );
    }
    if (state === 'APPROVED') {
      return (
        permissions.includes('inventory.transfer.ship') &&
        permissions.includes('inventory.transfer.receive')
      );
    }
    return (
      (state === 'IN_TRANSIT' || state === 'PARTIALLY_RECEIVED') &&
      permissions.includes('inventory.transfer.receive')
    );
  }

  function canCancelTransfer() {
    if (kind !== 'transfer' || !record || transferAction || cancelTransferBusy) return false;
    if (!permissions.includes('inventory.transfer.request')) return false;
    return ['DRAFT', 'REQUESTED', 'APPROVED', 'IN_TRANSIT', 'PARTIALLY_RECEIVED'].includes(
      transferState()
    );
  }

  function transferSourceName() {
    return String(
      firstValue(record, [
        'source_warehouse_name',
        'from_warehouse_name',
        'source_warehouse.name'
      ]) ?? 'çıkış deposu'
    );
  }

  function transferActionLabel() {
    return ['IN_TRANSIT', 'PARTIALLY_RECEIVED'].includes(transferState())
      ? 'Teslim al'
      : 'Sevki tamamla';
  }

  function transferActionDescription() {
    return ['IN_TRANSIT', 'PARTIALLY_RECEIVED'].includes(transferState())
      ? 'Transfer teslim alınacaktır. Devam etmek istiyor musunuz?'
      : 'Sevk edilecektir. Devam etmek istiyor musunuz?';
  }

  function transferActionConfirmLabel() {
    return ['IN_TRANSIT', 'PARTIALLY_RECEIVED'].includes(transferState()) ? 'Teslim al' : 'Tamamla';
  }

  function openCompleteTransferDialog() {
    if (!canTransferAction('complete')) return;
    completeTransferDialogOpen = true;
  }

  let completeTransferIdempotencyKey = '';

  async function completeTransfer() {
    const id = page.params.id;
    if (!id || !record || !canTransferAction('complete')) return;
    transferAction = 'complete';
    actionMessage = '';
    try {
      const postTransferAction = async (
        nextAction: 'request' | 'approve' | 'ship' | 'receive',
        source: RecordValue
      ) => {
        const headers: Record<string, string> = {};
        const version = firstValue(source, 'version');
        if (!hasValue(version)) {
          throw new Error('Transfer bilgisi güncel değil. Lütfen sayfayı yenileyin.');
        }
        headers['If-Match'] = `"${String(version)}"`;
        let body: Record<string, unknown> = {};
        if (nextAction === 'receive') {
          if (!completeTransferIdempotencyKey) {
            completeTransferIdempotencyKey = actionIdempotencyKey(`transfer-complete:${id}`);
          }
          headers['Idempotency-Key'] = completeTransferIdempotencyKey;
          body = { lines: [] };
        }
        return normalizePayload(
          await api<unknown>(`/warehouse-transfers/${encodeURIComponent(id)}/${nextAction}`, {
            method: 'POST',
            headers,
            body: JSON.stringify(body)
          })
        );
      };

      let current = record;
      let state = transferState();
      if (state === 'DRAFT' || state === 'REQUESTED') {
        current = await postTransferAction('approve', current);
        state = String(firstValue(current, ['state', 'status']) ?? '').toUpperCase();
      }
      if (state === 'APPROVED') {
        current = await postTransferAction('ship', current);
        state = String(firstValue(current, ['state', 'status']) ?? '').toUpperCase();
      }
      if (state === 'IN_TRANSIT' || state === 'PARTIALLY_RECEIVED') {
        current = await postTransferAction('receive', current);
      }
      await load();
      const completedState = String(firstValue(current, ['state', 'status']) ?? '').toUpperCase();
      actionMessage = completedState === 'RECEIVED' ? 'Sevk tamamlandı.' : 'Sevk kısmi tamamlandı.';
      completeTransferIdempotencyKey = '';
    } catch (cause) {
      const message = transferActionError(cause, 'Sevk tamamlanamadı.');
      await load();
      actionMessage = message;
      throw new Error(message);
    } finally {
      transferAction = '';
    }
  }

  function openCancelTransferDialog() {
    if (!canCancelTransfer()) return;
    cancelTransferDialogOpen = true;
  }

  async function cancelTransfer() {
    const id = page.params.id;
    if (!id || !record || !canCancelTransfer()) return;
    cancelTransferBusy = true;
    actionMessage = '';
    try {
      const version = firstValue(record, 'version');
      if (!hasValue(version)) {
        throw new Error('Transfer bilgisi güncel değil. Lütfen sayfayı yenileyin.');
      }
      const headers: Record<string, string> = {
        'If-Match': `"${String(version)}"`
      };
      await api(`/warehouse-transfers/${encodeURIComponent(id)}/cancel`, {
        method: 'POST',
        headers,
        body: JSON.stringify({ reason: 'Sevk iptali' })
      });
      await load();
      actionMessage = 'Sevk iptal oldu. Çıkış deposuna geri döndü.';
    } catch (cause) {
      const message = transferActionError(cause, 'Sevk iptal edilemedi.');
      actionMessage = message;
      throw new Error(message);
    } finally {
      cancelTransferBusy = false;
    }
  }

  function warehouseTransferSourceName(transfer: RecordValue) {
    const directName = firstValue(transfer, [
      'source_warehouse_name',
      'from_warehouse_name',
      'source_warehouse.name',
      'from_warehouse.name'
    ]);
    if (hasValue(directName)) return String(directName);

    return undefined;
  }

  function warehouseTransferSourceLabel(transfer: RecordValue) {
    const sourceName = warehouseTransferSourceName(transfer);
    return sourceName ? `${sourceName} deposundan` : 'başka bir depodan';
  }

  function warehouseTransferHref(transfer: RecordValue) {
    const id = firstValue(transfer, 'id');
    return id ? `/stok/transferler/${encodeURIComponent(String(id))}` : undefined;
  }

  async function loadWarehouseTransfers(warehouseID: string) {
    warehouseTransfers = [];
    try {
      const result = await api<OperationListResponse>(
        `/warehouse-transfers?warehouse_id=${encodeURIComponent(warehouseID)}&state=IN_TRANSIT,PARTIALLY_RECEIVED&limit=100`
      );
      warehouseTransfers = result.items ?? [];
    } catch {
      // The warehouse card remains usable when the optional transfer notice is unavailable.
      warehouseTransfers = [];
    }
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
      let payload: unknown;
      try {
        payload = await api<unknown>(`${config.endpoint}/${encodeURIComponent(id)}`);
        if (kind === 'stock-movement' && !hasStockMovementDetails(payload)) {
          payload = await api<unknown>(`/stock-movements/${encodeURIComponent(id)}`);
        }
      } catch (cause) {
        if (kind !== 'stock-movement') throw cause;
        // Operasyon endpoint'i hazır olmayan eski tekil hareket kayıtlarını da aç.
        payload = await api<unknown>(`/stock-movements/${encodeURIComponent(id)}`);
      }
      record = normalizePayload(payload);
      if (kind === 'transfer') record = await enrichTransferProductNames(record);
      if (kind === 'warehouse') {
        hydrateWarehouseForm(record);
        if (Object.keys(record).length) void loadWarehouseTransfers(id);
      }
      if (!Object.keys(record).length) error = 'Kayıt bulunamadı.';
    } catch (cause) {
      error =
        typeof cause === 'object' && cause && 'message' in cause
          ? String(cause.message)
          : 'Kayıt okunamadı.';
    } finally {
      loading = false;
    }
  }

  function openWarehouseEditor() {
    if (kind !== 'warehouse' || !record) return;
    hydrateWarehouseForm(record);
    warehouseError = '';
    warehouseEditing = true;
  }

  async function saveWarehouse() {
    const id = page.params.id;
    if (
      kind !== 'warehouse' ||
      !id ||
      !record ||
      warehouseSaving ||
      warehouseToggling ||
      warehouseDeleting
    )
      return;
    if (!warehouseForm.name.trim()) {
      warehouseError = 'Depo adı gereklidir.';
      return;
    }
    const wasActive = warehouseIsActive(record);
    const nextActive = warehouseForm.is_active;
    const changesActiveState = wasActive !== nextActive;
    if (wasActive && !nextActive) {
      const confirmed = confirm('Depo pasife alınsın mı?');
      if (!confirmed) return;
    }
    if (!wasActive && nextActive) {
      const confirmed = confirm('Depo aktife alınsın mı?');
      if (!confirmed) return;
    }
    warehouseSaving = true;
    warehouseError = '';
    actionMessage = '';
    try {
      const updated = await updateWarehouse(id, Number(firstValue(record, 'version') ?? 0), {
        code: warehouseForm.code.trim(),
        name: warehouseForm.name.trim(),
        type: warehouseForm.type,
        address: warehouseForm.address.trim(),
        is_active: nextActive
      });
      record = normalizePayload(updated);
      hydrateWarehouseForm(record);
      warehouseEditing = false;
      actionMessage =
        wasActive && !nextActive
          ? 'Depo pasife alındı.'
          : !wasActive && nextActive
            ? 'Depo aktife alındı.'
            : 'Depo güncellendi.';
    } catch (cause) {
      warehouseError = warehouseActionError(
        cause,
        'Depo güncellenemedi.',
        changesActiveState ? 'lifecycle' : 'save'
      );
    } finally {
      warehouseSaving = false;
    }
  }

  let warehouseToggleDialogOpen = $state(false);

  function toggleWarehouseActive() {
    if (
      kind !== 'warehouse' ||
      !page.params.id ||
      !record ||
      warehouseSaving ||
      warehouseToggling ||
      warehouseDeleting
    )
      return;
    if (warehouseIsSystem(record)) {
      warehouseError = 'Sistem deposu pasifleştirilemez.';
      return;
    }
    warehouseToggleDialogOpen = true;
  }

  async function runToggleWarehouseActive() {
    const id = page.params.id;
    if (kind !== 'warehouse' || !id || !record) return;
    const nextActive = !warehouseIsActive(record);

    warehouseToggling = true;
    warehouseError = '';
    actionMessage = '';
    try {
      const updated = await updateWarehouse(id, Number(firstValue(record, 'version') ?? 0), {
        code: String(firstValue(record, 'code') ?? '').trim(),
        name: String(firstValue(record, 'name') ?? '').trim(),
        type: warehouseTypeValue(firstValue(record, ['type', 'warehouse_type'])),
        address: String(firstValue(record, 'address') ?? '').trim(),
        is_active: nextActive
      });
      record = normalizePayload(updated);
      hydrateWarehouseForm(record);
      actionMessage = nextActive ? 'Depo aktife alındı.' : 'Depo pasife alındı.';
    } catch (cause) {
      warehouseError = warehouseActionError(cause, 'Depo durumu güncellenemedi.', 'lifecycle');
    } finally {
      warehouseToggling = false;
    }
  }

  async function removeWarehouse() {
    const id = page.params.id;
    if (kind !== 'warehouse' || !id || !record || warehouseSaving || warehouseDeleting) return;
    if (warehouseIsSystem(record)) {
      warehouseError = 'Sistem deposu silinemez.';
      return;
    }
    if (!warehouseCanDelete(record)) {
      warehouseError = 'Bu depo kullanıldığı için silinemez.';
      return;
    }
    const confirmed = confirm(
      'Depo yalnızca hiç stok hareketi, bakiyesi veya bağımlı kaydı yoksa silinebilir. Silinsin mi?'
    );
    if (!confirmed) return;
    warehouseDeleting = true;
    warehouseError = '';
    actionMessage = '';
    try {
      await deleteWarehouse(id, Number(firstValue(record, 'version') ?? 0));
      await goto(config.listPath);
    } catch (cause) {
      const code = warehouseErrorCode(cause);
      const message = warehouseActionError(cause, 'Depo silinemedi.', 'delete');
      if (
        code === 'WAREHOUSE_HAS_MOVEMENTS' ||
        code === 'WAREHOUSE_HAS_HISTORY' ||
        code === 'WAREHOUSE_HAS_DEPENDENCIES' ||
        code === 'WAREHOUSE_IN_USE' ||
        code === 'VERSION_CONFLICT'
      ) {
        await load();
      }
      warehouseError = message;
      actionMessage = warehouseError;
    } finally {
      warehouseDeleting = false;
    }
  }

  function actionIdempotencyKey(prefix: string) {
    const random =
      typeof crypto !== 'undefined' && 'randomUUID' in crypto
        ? crypto.randomUUID()
        : `${Date.now()}-${Math.random().toString(36).slice(2)}`;
    return `${prefix}:${random}`;
  }

  // A stock movement tied to a commercial document (dispatch, goods receipt,
  // invoice, return) can only be undone by cancelling that source document; the
  // backend rejects a direct reverse. Movements without a linked document —
  // manual adjustments, opening-stock imports, warehouse transfers and
  // stock-count adjustments — offer the "Ters" action here.
  function isDocumentOriginMovement() {
    if (kind !== 'stock-movement') return false;
    return Boolean(firstValue(record, ['source_document_no', 'source.document_no']));
  }

  function supportsReversal() {
    const permission =
      kind === 'stock-movement'
        ? 'inventory.movement.reverse'
        : kind === 'finance-transfer'
          ? 'finance.transfer.reverse'
          : kind === 'collection'
            ? 'finance.collection.reverse'
            : 'finance.payment.reverse';
    return (
      (kind === 'collection' ||
        kind === 'payment' ||
        kind === 'stock-movement' ||
        kind === 'finance-transfer') &&
      permissions.includes(permission) &&
      String(firstValue(record, ['status']) ?? '') !== 'REVERSED' &&
      !firstValue(record, ['reversal_of_id', 'reversed_by_id']) &&
      !isDocumentOriginMovement()
    );
  }

  // A stock-movement record loaded from the operations endpoint is a manual
  // multi-variant operation: its reversal goes through the operation endpoint,
  // which compensates every line movement in one transaction. A plain single
  // movement uses the movement endpoint.
  function isStockOperationRecord() {
    if (kind !== 'stock-movement' || !record) return false;
    return stockMovementLines(record).some((line) => Boolean(firstValue(line, 'movement_id')));
  }

  async function reverseRecord(reason: string) {
    const id = page.params.id;
    if (!id || !supportsReversal() || actionBusy) return;
    actionBusy = true;
    actionMessage = '';
    try {
      const path =
        kind === 'stock-movement'
          ? isStockOperationRecord()
            ? `/stock-movement-operations/${encodeURIComponent(id)}/reverse`
            : `/stock-movements/${encodeURIComponent(id)}/reverse`
          : kind === 'finance-transfer'
            ? `/finance/transfers/${encodeURIComponent(id)}/reverse`
            : kind === 'collection'
              ? `/finance/collections/${encodeURIComponent(id)}/reverse`
              : `/finance/payments/${encodeURIComponent(id)}/reverse`;
      await api(path, {
        method: 'POST',
        headers: { 'Idempotency-Key': actionIdempotencyKey('reversal') },
        body: JSON.stringify({ reason: reason.trim() })
      });
      actionMessage = 'Ters kayıt oluşturuldu.';
      await load();
    } catch (cause) {
      const message =
        typeof cause === 'object' && cause && 'message' in cause
          ? String(cause.message)
          : 'Ters kayıt oluşturulamadı.';
      actionMessage = message;
      throw new Error(message);
    } finally {
      actionBusy = false;
    }
  }

  function printLabel() {
    if (kind === 'collection' || kind === 'payment') return 'Fiş Al';
    if (kind === 'transfer') return 'Transfer Fişi';
    if (kind === 'count') return 'Sayım Fişi';
    return 'Yazdır';
  }

  function receiptRow(label: string, value: string) {
    if (!value || value === '—') return '';
    return `<tr><th>${ph(label)}</th><td>${ph(value)}</td></tr>`;
  }

  function printReceipt() {
    if ((kind !== 'collection' && kind !== 'payment') || !record) {
      if (typeof window !== 'undefined') window.print();
      return;
    }
    const currency = textValue(firstValue(record, 'currency'));
    const amount = String(firstValue(record, 'amount') ?? '0');
    const rate = String(firstValue(record, 'exchange_rate') ?? '');
    const baseAmount = firstValue(record, ['base_amount', 'try_amount']);
    const baseCurrency = textValue(firstValue(record, 'base_currency'));
    const documentNo = textValue(firstValue(record, config.numberKeys));
    const isCollection = kind === 'collection';
    const heading = isCollection ? 'TAHSİLAT FİŞİ' : 'ÖDEME FİŞİ';
    const partyName = textValue(firstValue(record, ['party_name', 'party.display_name']));
    const directionNote = isCollection
      ? 'Yukarıda adı geçen cariden aşağıdaki tutar tahsil edilmiştir.'
      : 'Yukarıda adı geçen cariye aşağıdaki tutar ödenmiştir.';

    const bodyHtml = `
      <table class="kv">
        ${receiptRow('Fiş No', documentNo)}
        ${receiptRow('Tarih', formatDate(String(firstValue(record, ['transaction_date', 'document_date']) ?? '')))}
        ${receiptRow('Cari', partyName)}
        ${receiptRow('Cari Kodu', textValue(firstValue(record, ['party_code', 'party.code'])))}
        ${receiptRow('Yöntem', localizedEnum(firstValue(record, 'payment_method'), 'payment_method'))}
        ${receiptRow('Hesap', textValue(firstValue(record, ['account_name', 'account.name'])))}
        ${receiptRow('Referans No', textValue(firstValue(record, ['reference_no', 'reference'])))}
        ${receiptRow('Açıklama', textValue(firstValue(record, 'description')))}
      </table>
      <p class="note">${ph(directionNote)}</p>
      <table class="amount">
        <tr><td>Tutar</td><td class="right">${ph(formatMoney(amount, currency))}</td></tr>
        ${
          currency.toUpperCase() !== baseCurrency.toUpperCase() && baseAmount
            ? `<tr><td>Kur</td><td class="right">${ph(rate)}</td></tr>
               <tr><td>${ph(baseCurrency)} Karşılığı</td><td class="right">${ph(formatMoney(String(baseAmount), baseCurrency))}</td></tr>`
            : ''
        }
      </table>
      <div class="signatures">
        <div><span>Teslim Eden</span></div>
        <div><span>Teslim Alan</span></div>
      </div>`;

    printDocument({
      title: heading,
      subtitle: [partyName, documentNo].filter((part) => part && part !== '—').join(' · '),
      company: {
        name: company?.trade_name || company?.legal_name || companyName || 'Şirket',
        logo: company?.logo || undefined,
        taxNumber: company?.tax_number
      },
      bodyHtml,
      bodyStyles: `
        table.kv { max-width: 520px; }
        table.kv th { width: 150px; background: #f9fafb; text-transform: none; letter-spacing: 0; font-weight: 600; color: #374151; }
        p.note { margin: 16px 0 8px; font-size: 11.5px; }
        table.amount { max-width: 320px; margin-left: auto; }
        table.amount td { border-bottom: none; padding: 4px 8px; font-size: 12px; }
        table.amount tr:last-child td { border-top: 2px solid #111827; font-weight: 700; font-size: 13px; }
        .signatures { display: flex; justify-content: space-between; gap: 40px; margin-top: 48px; }
        .signatures div { flex: 1; border-top: 1px solid #9ca3af; padding-top: 6px; text-align: center; }
        .signatures span { font-size: 10.5px; color: #6b7280; }
      `
    });
  }

  function listRows(item: RecordValue, key: string) {
    const value = firstValue(item, key);
    return Array.isArray(value)
      ? value.filter((row): row is RecordValue => Boolean(row && typeof row === 'object'))
      : [];
  }

  function stockMovementLines(item: RecordValue) {
    const lines = firstValue(item, 'lines');
    if (Array.isArray(lines)) {
      return lines.filter((line): line is RecordValue => Boolean(line && typeof line === 'object'));
    }
    return [];
  }

  function variantPairs(item: RecordValue) {
    const raw = firstValue(item, [
      'variant_display',
      'variant.display',
      'variant_attributes',
      'attributes'
    ]);
    if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return [];
    return Object.entries(raw as RecordValue).map(([name, option]) => ({
      name,
      option: attributeText(option)
    }));
  }

  function attributeText(value: unknown): string {
    if (Array.isArray(value)) return value.map(attributeText).join(', ');
    if (value && typeof value === 'object') {
      const item = value as RecordValue;
      return String(item.name ?? item.label ?? item.value ?? item.code ?? '');
    }
    return String(value);
  }

  function stockCurrency(item: RecordValue, line?: RecordValue) {
    return String(firstValue(line, 'currency') ?? firstValue(item, 'currency') ?? 'TRY');
  }

  /** A unit cost keeps the decimals it was entered with; a line total is
   *  money and is shown at two. */
  function stockLineUnitCost(item: RecordValue, line: RecordValue, keys: string | string[]) {
    const amount = firstValue(line, keys);
    return amount === undefined || amount === null || amount === ''
      ? '—'
      : formatUnitPrice(String(amount), stockCurrency(item, line));
  }

  function stockLineTotalMoney(item: RecordValue, line: RecordValue) {
    const explicit = firstValue(line, ['total_cost', 'total_amount']);
    if (explicit !== undefined && explicit !== null && explicit !== '') {
      return formatMoney(String(explicit), stockCurrency(item, line));
    }
    const quantity = Number(firstValue(line, ['entered_quantity', 'quantity']) ?? 0);
    const unitCost = Number(firstValue(line, ['unit_cost', 'cost']) ?? 0);
    if (!Number.isFinite(quantity) || !Number.isFinite(unitCost) || unitCost === 0) return '—';
    return formatMoney(String(quantity * unitCost), stockCurrency(item, line));
  }

  function statusValue(item: RecordValue) {
    const value = firstValue(item, config.statusKeys);
    if (typeof value === 'boolean') return value ? 'ACTIVE' : 'INACTIVE';
    if (!value) return '';
    // Account movements use movement_kind as the header badge — localize it.
    if (kind === 'account-movement') return localizedEnum(value, 'movement_kind');
    return String(value);
  }

  // Başlık: numara varsa numara, yoksa kaydı tarif eden bir ifade. Asla UUID.
  function headingNumber(item: RecordValue) {
    const value = firstValue(item, config.numberKeys);
    if (hasValue(value)) return textValue(value);
    return config.numberFallback?.(item) || config.title;
  }

  // Alt satır: özne ve meta değerleri, boş olanlar atlanarak birleştirilir —
  // özne yoksa satır ayırıcıyla başlamaz.
  function metaLine(item: RecordValue) {
    const parts = [
      String(firstValue(item, config.subjectKeys) ?? ''),
      ...config.metaKeys.map((key) => metaValue(item, key))
    ];
    return parts.filter((part) => part && part !== '—').join(' · ');
  }

  function metaValue(item: RecordValue, key: string) {
    const value = firstValue(item, key);
    if (!value) return '';
    return key.includes('at') || key.includes('date')
      ? formatDate(String(value))
      : textValue(value);
  }

  onMount(() => {
    void load();
    void api<Session>('/session')
      .then((session) => {
        permissions = session.permissions ?? [];
        const company = session.companies?.find((item) => item.id === session.current_company_id);
        companyName = company?.trade_name || company?.legal_name || '';
      })
      .catch(() => {
        permissions = [];
        companyName = '';
      });
    if (config.print) {
      void api<Company>('/company')
        .then((loaded) => {
          company = loaded;
        })
        .catch(() => {
          company = undefined;
        });
    }
  });
</script>

<svelte:head><title>{record ? headingNumber(record) : config.title} · Varya One</title></svelte:head
>

{#if loading}
  <div class="panel loading" role="status">{config.title} yükleniyor…</div>
{:else if error || !record}
  <section class="panel error-panel" role="alert">
    <strong>{config.title} açılamadı.</strong>
    <span>{error || 'Kayıt bulunamadı.'}</span>
    <div class="error-actions">
      <Button variant="outline" onclick={() => void load()}
        ><RefreshCw size={14} />Yeniden dene</Button
      >
      <a class="button-link" href={config.listPath}>Listeye dön</a>
    </div>
  </section>
{:else}
  {@const status = statusValue(record)}
  {@const number = headingNumber(record)}
  {@const meta = metaLine(record)}
  {@const incomingTransfers = kind === 'warehouse' ? warehouseTransfers : []}
  <header class="page-header">
    <div>
      {#if kind === 'transfer' && companyName}<div class="company-heading">
          <span>Şirket</span><strong>{companyName}</strong>
        </div>{/if}
      <a class="back" href={config.listPath}><ArrowLeft size={14} />{config.title} listesi</a>
      <div class="title-row">
        <h1>{number}</h1>
        {#if status}
          {#if kind === 'transfer'}<span class="transfer-status-badge"
              >{transferStatusLabel(status)}</span
            >{:else}<DocumentStatus {status} />{/if}
        {/if}
      </div>
      {#if meta}<p class="meta">{meta}</p>{/if}
    </div>
    <div class="page-actions">
      {#if kind === 'warehouse' && permissions.includes('organization.warehouse.manage') && !warehouseIsSystem(record)}
        <Button
          variant="outline"
          disabled={warehouseSaving || warehouseToggling || warehouseDeleting}
          onclick={openWarehouseEditor}
          ><Pencil size={14} data-icon="inline-start" aria-hidden="true" />Düzenle</Button
        >
        {#if !warehouseIsSystem(record)}<Button
            variant="outline"
            disabled={warehouseSaving || warehouseToggling || warehouseDeleting}
            onclick={() => void toggleWarehouseActive()}
            >{#if warehouseToggling}<LoaderCircle class="spin" size={14} />{/if}{warehouseIsActive(
              record
            )
              ? 'Pasife al'
              : 'Aktife al'}</Button
          >{/if}
        {#if warehouseCanDelete(record)}<Button
            variant="outline"
            disabled={warehouseSaving || warehouseToggling || warehouseDeleting}
            title="Depoyu sil"
            onclick={() => void removeWarehouse()}><Trash2 size={14} />Sil</Button
          >{/if}
      {/if}
      {#if canTransferAction('complete')}<Button
          variant="default"
          disabled={Boolean(transferAction) || actionBusy}
          onclick={openCompleteTransferDialog}
          >{#if transferAction === 'complete'}<LoaderCircle class="spin" size={14} />{:else}<Truck
              size={14}
            />{/if}{transferActionLabel()}</Button
        >{/if}
      {#if canCancelTransfer()}<Button
          variant="outline"
          disabled={cancelTransferBusy || Boolean(transferAction) || actionBusy}
          onclick={openCancelTransferDialog}
          >{#if cancelTransferBusy}<LoaderCircle class="spin" size={14} />{:else}<Undo2
              size={14}
            />{/if}Sevk iptal et</Button
        >{/if}
      {#if config.print}<Button variant="outline" onclick={printReceipt}
          ><Printer size={14} />{printLabel()}</Button
        >{/if}
      {#if supportsReversal()}<Button
          variant="outline"
          disabled={actionBusy}
          onclick={() => (reasonDialogOpen = true)}
        >
          {#if actionBusy}<LoaderCircle class="spin" size={14} />{:else}<Undo2 size={14} />{/if}Ters
          Kayıt Oluştur
        </Button>{/if}
      {#if kind === 'stock-movement' && firstValue(record, ['reversed_by_id'])}
        <p class="document-origin-note">Bu stok hareketi ters kayıtla geri alınmış.</p>
      {:else if kind === 'stock-movement' && firstValue(record, ['reversal_of_id'])}
        <p class="document-origin-note">
          Bu bir ters kayıt hareketidir ve yeniden ters çevrilemez.
        </p>
      {:else if kind === 'stock-movement' && isDocumentOriginMovement()}
        {@const sourceNo = firstValue(record, ['source_document_no'])}
        <p class="document-origin-note">
          <!-- Belge no bloğunun kendi boşluklarını taşıması gerekiyor: blok
               etiketine bitişik yazılan boşluk render'da kayboluyor. -->
          {#if sourceNo}Bu stok hareketi <strong>{sourceNo}</strong> belgesinden oluşturuldu.{:else}Bu
            stok hareketi bir belgeden oluşturuldu.{/if} Hareketi geri almak için kaynak belgeyi iptal
          edin.
        </p>
      {/if}
    </div>
  </header>

  <ReasonDialog
    bind:open={reasonDialogOpen}
    title="Ters kayıt oluştur"
    description="Bu hareketi ters çevirmek için nedeni kayda ekleyin."
    initialValue="Düzeltme"
    confirmLabel="Ters kaydı oluştur"
    onConfirm={reverseRecord}
  />

  <ConfirmDialog
    bind:open={warehouseToggleDialogOpen}
    title={record && warehouseIsActive(record) ? 'Depoyu pasife al' : 'Depoyu aktife al'}
    description={record && warehouseIsActive(record)
      ? 'Depo pasife alınsın mı? Geçmiş hareketleri korunur, yeni operasyonlarda kullanılamaz.'
      : 'Depo yeniden aktife alınsın mı?'}
    confirmLabel={record && warehouseIsActive(record) ? 'Pasife al' : 'Aktife al'}
    onConfirm={runToggleWarehouseActive}
  />

  <ConfirmDialog
    bind:open={completeTransferDialogOpen}
    title={transferActionLabel()}
    description={transferActionDescription()}
    confirmLabel={transferActionConfirmLabel()}
    onConfirm={completeTransfer}
  />

  <ConfirmDialog
    bind:open={cancelTransferDialogOpen}
    title="Sevki iptal et"
    description={`Sevk iptal edilecektir. ${transferSourceName()} deposuna geri döndürülecektir.`}
    confirmLabel="Sevk iptal et"
    onConfirm={cancelTransfer}
  />

  {#if actionMessage}<p
      class:success={actionMessage === 'Ters kayıt oluşturuldu.' ||
        actionMessage === 'Depo güncellendi.' ||
        actionMessage === 'Depo pasife alındı.' ||
        actionMessage === 'Depo aktife alındı.' ||
        actionMessage === 'Sevk tamamlandı.' ||
        actionMessage === 'Sevk kısmi tamamlandı.' ||
        actionMessage === 'Sevk iptal oldu. Çıkış deposuna geri döndü.'}
      class="action-message"
      role="status"
    >
      {actionMessage}
    </p>{/if}
  {#if warehouseError && !warehouseEditing}
    <p class="notice error" role="alert">{warehouseError}</p>
  {/if}

  {#if incomingTransfers.length}
    <section
      class="panel warehouse-transfer-banner"
      aria-labelledby="warehouse-transfer-banner-title"
    >
      <div class="warehouse-transfer-banner-heading">
        <Truck size={17} aria-hidden="true" />
        <div>
          <h2 id="warehouse-transfer-banner-title">Sevk / teslim süreci</h2>
          <p>Bu depoya devam eden transferleri görüntüleyin.</p>
        </div>
      </div>
      <div class="warehouse-transfer-links">
        {#each incomingTransfers as transfer}
          {@const href = warehouseTransferHref(transfer)}
          {#if href}<a class="warehouse-transfer-link" {href}>
              <span>Bu depoya {warehouseTransferSourceLabel(transfer)} sevk/teslim süreci var.</span
              ><span class="warehouse-transfer-link-action"
                >Detayına git <ExternalLink size={13} aria-hidden="true" /></span
              >
            </a>{/if}
        {/each}
      </div>
    </section>
  {/if}

  {#if kind === 'warehouse' && warehouseEditing}
    <section class="panel warehouse-editor" aria-labelledby="warehouse-editor-title">
      <div class="section-heading">
        <div>
          <h2 id="warehouse-editor-title">Depo bilgilerini düzenle</h2>
        </div>
        <button class="button-link" type="button" onclick={() => (warehouseEditing = false)}>
          Vazgeç
        </button>
      </div>
      {#if warehouseError}<p class="notice error" role="alert">{warehouseError}</p>{/if}
      <form
        class="warehouse-form"
        onsubmit={(event) => {
          event.preventDefault();
          void saveWarehouse();
        }}
      >
        <label><span>Depo kodu</span><input bind:value={warehouseForm.code} maxlength="40" /></label
        >
        <label
          ><span>Depo adı <b>*</b></span><input bind:value={warehouseForm.name} required /></label
        >
        <label class="warehouse-type-field">
          <span>Depo türü</span>
          <div class="warehouse-type-value">
            <Lock size={14} aria-hidden="true" />
            <span>{warehouseTypeLabel(warehouseForm.type)}</span>
          </div>
        </label>
        <label
          ><span>Durum <b>*</b></span><select
            value={warehouseForm.is_active ? 'true' : 'false'}
            onchange={(event) => (warehouseForm.is_active = event.currentTarget.value === 'true')}
          >
            <option value="true">Aktif</option>
            <option value="false">Pasif</option>
          </select></label
        >
        <label class="wide"><span>Adres</span><input bind:value={warehouseForm.address} /></label>
        <div class="form-actions wide">
          <Button type="submit" disabled={warehouseSaving || warehouseToggling || warehouseDeleting}
            >{#if warehouseSaving}<LoaderCircle class="spin" size={14} /> Kaydediliyor…{:else}<Save
                size={14}
              /> Kaydet{/if}</Button
          >
        </div>
      </form>
    </section>
  {/if}

  <section class="panel detail-panel">
    <div class="field-grid">
      {#each config.fields as field}
        {@const value = fieldValue(record, field)}
        {@const href = hrefFor(record, field)}
        {#if hasValue(value) && (field.kind !== 'ref' || href)}<div
            class="field"
            class:print-hidden={field.hideOnPrint}
          >
            <dt>{field.label}</dt>
            <dd>
              {#if href}<a {href} aria-label={linkAriaLabel(record, field)}
                  ><span>{cellText(record, field, href)}</span><ExternalLink
                    size={12}
                    aria-hidden="true"
                  /></a
                >
              {:else}{cellText(record, field, href)}{/if}
            </dd>
          </div>{/if}
      {/each}
    </div>
  </section>

  {#if kind === 'stock-movement' && stockMovementLines(record).length > 0}
    {@const stockLines = stockMovementLines(record)}
    <section class="panel table-panel stock-variant-panel">
      <div class="section-heading">
        <div>
          <h2>Varyant hareketleri</h2>
          <p>Bu stok operasyonundaki her varyantın miktar ve maliyet dağılımı.</p>
        </div>
        <span>{stockLines.length} satır</span>
      </div>
      <div class="table-scroll">
        <table>
          <thead>
            <tr>
              <th>Varyant</th>
              <th>Varyant kodu</th>
              <th class="numeric">Stok miktarı</th>
              <th>Stok kartı birimi</th>
              <th class="numeric">Birim maliyeti</th>
              <th class="numeric">Toplam maliyet</th>
              <th>Stok etkisi</th>
            </tr>
          </thead>
          <tbody>
            {#each stockLines as line}
              {@const pairs = variantPairs(line)}
              <tr>
                <td>
                  <div class="variant-display">
                    {#if pairs.length}
                      {#each pairs as pair}
                        <span class="variant-chip"><b>{pair.name}</b>{pair.option}</span>
                      {/each}
                    {:else}
                      <span>Ana stok</span>
                    {/if}
                  </div>
                </td>
                <td>{textValue(firstValue(line, ['variant_code', 'variant.sku', 'sku']))}</td>
                <td class="numeric">
                  {formatQuantityWithUnit(
                    String(firstValue(line, ['base_quantity', 'quantity']) ?? '0'),
                    String(
                      firstValue(line, ['stock_unit', 'unit_code', 'unit']) ??
                        firstValue(record, ['stock_unit', 'unit_code', 'unit']) ??
                        'ADET'
                    )
                  )}
                </td>
                <td
                  >{textValue(
                    firstValue(line, ['stock_unit', 'unit_code', 'unit']) ??
                      firstValue(record, ['stock_unit', 'unit_code', 'unit']) ??
                      'ADET'
                  )}</td
                >
                <td class="numeric">{stockLineUnitCost(record, line, ['unit_cost', 'cost'])}</td>
                <td class="numeric">{stockLineTotalMoney(record, line)}</td>
                <td>
                  {formatQuantityWithUnit(
                    String(
                      firstValue(line, ['quantity_delta', 'stock_effect']) ??
                        firstValue(line, ['base_quantity', 'quantity']) ??
                        '0'
                    ),
                    String(
                      firstValue(line, ['stock_unit', 'unit_code', 'unit']) ??
                        firstValue(record, ['stock_unit', 'unit_code', 'unit']) ??
                        'ADET'
                    )
                  )}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    </section>
  {/if}

  {#each config.tables ?? [] as table}
    {@const rows = listRows(record, table.key)}
    {#if rows.length}
      <section class="panel table-panel" class:print-hidden={table.hideOnPrint}>
        <div class="section-heading">
          <h2>{table.title}</h2>
          <span>{rows.length} kayıt</span>
        </div>
        <div class="table-scroll">
          <table>
            <thead
              ><tr
                >{#each table.columns as column}<th class:print-hidden={column.hideOnPrint}
                    >{column.label}</th
                  >{/each}</tr
              ></thead
            >
            <tbody
              >{#each rows as row}<tr
                  >{#each table.columns as column}{@const href = hrefFor(row, column)}<td
                      class:print-hidden={column.hideOnPrint}
                    >
                      {#if href}<a {href} aria-label={linkAriaLabel(row, column)}
                          >{cellText(row, column, href)}</a
                        >{:else}{cellText(row, column, href)}{/if}
                    </td>{/each}</tr
                >{/each}</tbody
            >
          </table>
        </div>
      </section>
    {/if}
  {/each}
{/if}

<style>
  .company-heading {
    display: inline-flex;
    align-items: baseline;
    gap: 7px;
    margin-bottom: 5px;
    color: var(--text-muted);
    font-size: 11px;
  }
  .company-heading strong {
    color: var(--text);
    font-size: 14px;
    font-weight: 750;
  }
  .transfer-status-badge {
    display: inline-flex;
    align-items: center;
    min-height: 24px;
    padding: 0 9px;
    border: 1px solid color-mix(in srgb, var(--primary) 35%, var(--border));
    border-radius: 999px;
    background: color-mix(in srgb, var(--primary) 10%, var(--surface));
    color: var(--primary);
    font-size: 11px;
    font-weight: 700;
    white-space: nowrap;
  }
  .back {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    margin-bottom: 5px;
    color: var(--primary);
    font-size: 11px;
    text-decoration: none;
  }
  .title-row {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .title-row h1 {
    margin: 0;
  }
  .meta {
    margin: 4px 0 0;
    color: var(--text-muted);
    font-size: 11px;
  }
  .detail-panel,
  .table-panel {
    padding: 16px;
  }
  .stock-variant-panel {
    margin-top: 12px;
  }
  .stock-variant-panel p {
    margin: 3px 0 0;
    color: var(--text-muted);
    font-size: 11px;
  }
  .variant-display {
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
    min-width: 190px;
    white-space: normal;
  }
  .variant-chip {
    display: inline-flex;
    gap: 4px;
    padding: 3px 6px;
    border: 1px solid color-mix(in srgb, var(--primary) 25%, var(--border));
    border-radius: 999px;
    background: color-mix(in srgb, var(--primary) 7%, var(--surface));
    color: var(--text);
    font-size: 11px;
  }
  .variant-chip b {
    color: var(--primary);
    font-weight: 700;
  }
  .field-grid {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 14px 22px;
  }
  .field {
    min-width: 0;
  }
  dt {
    color: var(--text-muted);
    font-size: 11px;
  }
  dd {
    margin: 3px 0 0;
    color: var(--text);
    font-size: 13px;
    overflow-wrap: anywhere;
  }
  dd a,
  td a {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    color: var(--primary);
    text-decoration: none;
  }
  dd a:hover,
  td a:hover {
    text-decoration: underline;
  }
  .section-heading {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 10px;
  }
  h2 {
    margin: 0;
    font-size: 14px;
  }
  .section-heading span {
    color: var(--text-muted);
    font-size: 11px;
  }
  .table-scroll {
    overflow-x: auto;
  }
  .warehouse-editor {
    margin-bottom: 12px;
    padding: 16px;
  }
  .warehouse-form {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 12px 16px;
  }
  .warehouse-form label {
    display: grid;
    gap: 4px;
  }
  .warehouse-form label > span {
    color: var(--text-muted);
    font-size: 11px;
  }
  .warehouse-type-value {
    display: flex;
    align-items: center;
    gap: 7px;
    min-height: 34px;
    padding: 6px 8px;
    border: 1px dashed var(--border-strong);
    border-radius: var(--radius-control);
    background: var(--surface-muted);
    color: var(--text-muted);
    font-size: 12px;
    cursor: not-allowed;
    opacity: 0.78;
  }
  .warehouse-type-value :global(svg) {
    flex: 0 0 auto;
  }
  .warehouse-transfer-banner {
    display: grid;
    gap: 10px;
    margin-bottom: 12px;
    padding: 12px 16px;
    border-left: 3px solid var(--primary);
    background: var(--surface-muted);
  }
  .warehouse-transfer-banner-heading {
    display: flex;
    align-items: flex-start;
    gap: 8px;
    color: var(--primary);
  }
  .warehouse-transfer-banner-heading :global(svg) {
    flex: 0 0 auto;
    margin-top: 1px;
  }
  .warehouse-transfer-banner h2 {
    color: var(--text);
    font-size: 13px;
  }
  .warehouse-transfer-banner p {
    margin: 3px 0 0;
    color: var(--text-muted);
    font-size: 11px;
  }
  .warehouse-transfer-links {
    display: grid;
    gap: 6px;
  }
  .warehouse-transfer-link {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    padding: 8px 10px;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    background: var(--surface);
    color: var(--text);
    font-size: 12px;
    text-decoration: none;
  }
  .warehouse-transfer-link:hover {
    border-color: var(--primary);
  }
  .warehouse-transfer-link-action {
    display: inline-flex;
    flex: 0 0 auto;
    align-items: center;
    gap: 4px;
    color: var(--primary);
    font-size: 11px;
    font-weight: 600;
  }
  .warehouse-form input,
  .warehouse-form select {
    min-height: 34px;
    padding: 6px 8px;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    background: var(--surface);
    color: var(--text);
    font: inherit;
    font-size: 12px;
  }
  .warehouse-form .wide {
    grid-column: 1 / -1;
  }
  .warehouse-form .form-actions {
    display: flex;
    justify-content: flex-end;
    margin-top: 2px;
  }
  table {
    width: 100%;
    border-collapse: collapse;
    font-size: 12px;
  }
  th,
  td {
    padding: 8px 9px;
    border-bottom: 1px solid var(--border);
    text-align: left;
    white-space: nowrap;
  }
  th {
    color: var(--text-muted);
    font-size: 11px;
    font-weight: 700;
  }
  .error-panel {
    display: grid;
    gap: 8px;
    max-width: 540px;
  }
  .error-panel span {
    color: var(--danger);
    font-size: 12px;
  }
  .error-actions {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .action-message {
    margin: 0 0 12px;
    color: var(--danger);
    font-size: 12px;
  }
  .document-origin-note {
    margin: 0;
    max-width: 320px;
    color: var(--muted-foreground, #666);
    font-size: 12px;
    line-height: 1.4;
  }
  .action-message.success {
    color: var(--success, var(--primary));
  }
  .button-link {
    display: inline-flex;
    align-items: center;
    min-height: 32px;
    padding: 0 12px;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    color: var(--text);
    font-size: 12px;
    text-decoration: none;
  }
  @media (max-width: 760px) {
    .field-grid {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
  }
  @media (max-width: 480px) {
    .field-grid {
      grid-template-columns: 1fr;
    }
  }
  @media print {
    :global(.skip-link),
    :global(.sidebar),
    :global(.topbar) {
      display: none !important;
    }
    :global(.app-shell) {
      display: block;
      min-height: auto;
    }
    :global(.workspace),
    :global(.main) {
      width: auto;
      max-width: none;
      margin: 0;
      padding: 0;
    }
    .back,
    .page-actions,
    .warehouse-transfer-banner,
    .error-actions,
    .print-hidden {
      display: none;
    }
    .detail-panel,
    .table-panel {
      border: 0;
      box-shadow: none;
      padding: 8px 0;
    }
  }
</style>
