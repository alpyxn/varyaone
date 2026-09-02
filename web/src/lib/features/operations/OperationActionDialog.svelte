<script lang="ts">
  import { LoaderCircle, Plus, Save, X } from '@lucide/svelte';
  import { api } from '$lib/api';
  import {
    addDecimalStrings,
    canonicalDecimal,
    decimalNumber,
    subtractDecimalStrings
  } from '$lib/design/decimal';
  import { formatMoney, formatQuantity } from '$lib/design/formatters';
  import { DateInput } from '$lib/components/varya/date-input';
  import {
    isActiveDestinationWarehouse,
    isActiveStandardWarehouse,
    warehouseOptionLabel,
    warehouseType,
    warehouseTypeLabel,
    type Warehouse
  } from '$lib/features/warehouses/types';
  import { listWarehouses } from '$lib/features/warehouses/api';
  import { type EntityOption } from '$lib/components/varya/entity-picker-dialog';
  import { EntityCombobox } from '$lib/components/varya/entity-combobox';
  import VariantStockMovementTable, {
    type VariantStockMovementRow
  } from './VariantStockMovementTable.svelte';
  import { compareDecimal, validateTransferQuantities } from './transfer-validation';
  import NegativeBalanceReason from '$lib/features/finance/NegativeBalanceReason.svelte';
  import { CurrencySelect } from '$lib/components/varya/currency-select';
  import { getExchangeRateDashboard } from '$lib/features/pricing/api';

  export type OperationActionKind =
    'manual' | 'collection' | 'payment' | 'stock-movement' | 'warehouse' | 'transfer' | 'count';

  type PartyOption = { id: string; code: string; display_name: string; default_currency?: string };
  type ProductOption = {
    id: string;
    sku?: string;
    code?: string;
    name: string;
    kind?: string;
    stock_unit?: string;
    available_quantity?: string;
    variants_enabled?: boolean;
  };
  type VariantOption = {
    id: string;
    variant_code: string;
    variant_name?: string;
    attributes?: Record<string, unknown>;
    values?: Array<{
      definition_id: string;
      option_id: string;
      definition_code?: string;
      definition_name?: string;
      option_code?: string;
      option_name?: string;
    }>;
    sku?: string;
    physical_quantity?: string;
    reserved_quantity?: string;
    available_quantity?: string;
    stock_unit?: string;
    is_active?: boolean;
  };
  type UnitOption = { code: string; name?: string; is_base?: boolean; conversion_factor?: string };
  type WarehouseOption = Warehouse;
  type PositionOption = {
    available_quantity?: string;
    physical_quantity?: string;
    reserved_quantity?: string;
  };
  type TransferLineDraft = {
    productID: string;
    variantID: string;
    variantRequired: boolean;
    quantity: string;
    availableQuantity: string;
    stockUnit: string;
    variants: VariantOption[];
    loading: boolean;
    error: string;
    metadataState: MetadataState;
    requestKey: string;
  };
  type MetadataState = 'idle' | 'loading' | 'ready' | 'error';
  type AccountOption = {
    id: string;
    code: string;
    name: string;
    account_type: string;
    currency: string;
  };
  type AllocationRow = {
    id: string;
    document_id?: string;
    document_no?: string;
    document_date: string;
    due_date?: string;
    open_amount: string;
    applied: string;
  };

  type ReasonOption = { value: string; label: string };

  // These are the reasons that make sense for a manual stock adjustment in
  // each direction.  The API still receives the stable reason code; the
  // direction-specific labels keep loss reasons out of inbound movements.
  const inboundReasons: ReasonOption[] = [
    { value: 'PURCHASE_RECEIPT', label: 'Alış / Mal kabul' },
    { value: 'SALES_RETURN', label: 'Satış iadesi' },
    { value: 'OPENING', label: 'Açılış' },
    { value: 'CORRECTION', label: 'Düzeltme' },
    { value: 'PROMOTION', label: 'Promosyon' },
    { value: 'OTHER', label: 'Diğer' }
  ];
  const outboundReasons: ReasonOption[] = [
    { value: 'SALES_DISPATCH', label: 'Satış / Sevk' },
    { value: 'PURCHASE_RETURN', label: 'Alış iadesi' },
    { value: 'CORRECTION', label: 'Düzeltme' },
    { value: 'DAMAGE', label: 'Hasar' },
    { value: 'WASTE', label: 'Fire / Zayi' },
    { value: 'SAMPLE', label: 'Numune' },
    { value: 'INTERNAL_USE', label: 'İç kullanım' },
    { value: 'OTHER', label: 'Diğer' }
  ];

  type Props = {
    kind: OperationActionKind;
    label: string;
    open?: boolean;
    productID?: string;
    selectedProductLabel?: string;
    paymentPrefill?: {
      partyID?: string;
      currency?: string;
      documentID?: string;
      method?: string;
      entryKind?: string;
    };
    onComplete?: () => void;
  };

  let {
    kind,
    label,
    open = $bindable(false),
    productID = '',
    selectedProductLabel = '',
    paymentPrefill,
    onComplete
  }: Props = $props();
  let submitting = $state(false);
  let loadingReferences = $state(false);
  let error = $state('');
  // Shown after the server reports a WARN-policy negative balance; the reason is
  // required and echoed back as override_reason on the retried command.
  let negativeBalancePrompt = $state(false);
  let overrideReason = $state('');
  let parties = $state<PartyOption[]>([]);
  let products = $state<ProductOption[]>([]);
  let units = $state<UnitOption[]>([]);
  let unitLoading = $state(false);
  let unitState = $state<MetadataState>('idle');
  let unitError = $state('');
  let unitRequestKey = '';
  let variants = $state<VariantOption[]>([]);
  let stockVariantRequired = $state(false);
  let variantLoading = $state(false);
  let variantState = $state<MetadataState>('idle');
  let variantError = $state('');
  let variantRequestKey = '';
  let stockAvailableQuantity = $state('');
  let stockPositionLoading = $state(false);
  let stockPositionError = $state('');
  let stockPositionRequestKey = '';
  let stockVariantRows = $state<VariantStockMovementRow[]>([]);
  let stockVariantValidationErrors = $state<Record<string, string>>({});
  let stockVariantTableValid = $state(false);
  let stockVariantPositionLoading = $state(false);
  let stockVariantPositionError = $state('');
  let stockVariantPositionRequestKey = '';
  let warehouses = $state<WarehouseOption[]>([]);
  let warehouseLoading = $state(false);
  let warehouseState = $state<MetadataState>('idle');
  let warehouseError = $state('');
  let warehouseRequestKey = 0;
  let stockWarehouseNotice = $state('');
  let accounts = $state<AccountOption[]>([]);
  let referencesLoaded = $state(false);
  // Current provider rates so a foreign-currency tahsilat/ödeme is not posted
  // at the default rate of 1. The field stays editable after prefill.
  let exchangeRates = $state<Record<string, string>>({});
  let exchangeBase = $state('TRY');

  function applyExchangeRate(target: { currency: string; exchangeRate: string }) {
    const code = target.currency.toUpperCase();
    if (!code || code === exchangeBase.toUpperCase() || code === 'TRY') {
      target.exchangeRate = '1';
      return;
    }
    const rate = exchangeRates[code];
    if (rate) target.exchangeRate = rate;
  }
  let allocationRows = $state<AllocationRow[]>([]);
  let transferCreateAttempt: { payload: string; key: string } | undefined;
  let financeCreateAttempt: { payload: string; key: string } | undefined;
  let allocationLoading = $state(false);
  let allocationRequestKey = '';
  let allocationNotice = $state('');
  // When true, the payment is posted with auto_allocate:true and the server
  // distributes it FIFO. Any manual edit to an "Uygulanacak" cell switches back
  // to an explicit allocations payload.
  let autoAllocateRequested = $state(false);
  let prefillAllocationAppliedKey = '';
  let transferProductsRequestKey = '';
  let transferLineRequestSequence = 0;

  const today = () => {
    const value = new Date();
    const month = String(value.getMonth() + 1).padStart(2, '0');
    const day = String(value.getDate()).padStart(2, '0');
    return `${value.getFullYear()}-${month}-${day}`;
  };

  const manual = $state({
    partyID: '',
    entryKind: 'DEBIT',
    currency: 'TRY',
    amount: '',
    exchangeRate: '1',
    description: '',
    transactionDate: today(),
    dueDate: '',
    referenceNo: ''
  });

  const payment = $state({
    partyID: '',
    method: 'CASH',
    accountID: '',
    currency: 'TRY',
    amount: '',
    exchangeRate: '1',
    referenceNo: '',
    description: '',
    transactionDate: today(),
    instrumentNo: '',
    instrumentDueDate: '',
    instrumentBankName: '',
    instrumentDrawerName: ''
  });

  const paymentAccounts = $derived(
    accounts.filter(
      (item) =>
        item.account_type === payment.method && item.currency === payment.currency.toUpperCase()
    )
  );

  const stock = $state({
    warehouseID: '',
    productID: '',
    variantID: '',
    direction: 'IN',
    quantity: '',
    unitCode: '',
    unitCost: '',
    currency: 'TRY',
    reasonCode: '',
    reasonDescription: ''
  });

  const warehouse = $state({
    code: '',
    name: '',
    type: 'STANDARD',
    address: ''
  });

  function newTransferLine(productID = ''): TransferLineDraft {
    return {
      productID,
      variantID: '',
      variantRequired: false,
      quantity: '',
      availableQuantity: '',
      stockUnit: '',
      variants: [],
      loading: false,
      error: '',
      metadataState: 'idle',
      requestKey: ''
    };
  }

  const transfer = $state<{
    transferNo: string;
    sourceWarehouseID: string;
    destinationWarehouseID: string;
    transferType: string;
    lines: TransferLineDraft[];
  }>({
    transferNo: '',
    sourceWarehouseID: '',
    destinationWarehouseID: '',
    transferType: 'QUICK',
    lines: [newTransferLine()]
  });

  const count = $state({ warehouseID: '' });

  function accountLabel(method: string) {
    if (method === 'CASH') return 'Kasa';
    if (method === 'BANK') return 'Banka hesabı';
    if (method === 'POS') return 'POS hesabı';
    return 'Hesap';
  }

  function idempotencyKey(prefix: string) {
    const random =
      typeof crypto !== 'undefined' && 'randomUUID' in crypto
        ? crypto.randomUUID()
        : `${Date.now()}-${Math.random().toString(36).slice(2)}`;
    return `${prefix}:${random}`;
  }

  function isValidDateValue(value: string) {
    const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value);
    if (!match) return false;
    const year = Number(match[1]);
    const month = Number(match[2]);
    const day = Number(match[3]);
    const parsed = new Date(Date.UTC(year, month - 1, day));
    return (
      parsed.getUTCFullYear() === year &&
      parsed.getUTCMonth() === month - 1 &&
      parsed.getUTCDate() === day
    );
  }

  function timestamp(value: string) {
    if (!isValidDateValue(value)) throw new Error('Geçerli bir tarih seçin.');
    return `${value}T00:00:00+03:00`;
  }

  function productEntity(item: ProductOption, availableQuantity?: string): EntityOption {
    const available = availableQuantity ?? item.available_quantity;
    return {
      id: item.id,
      title: item.name,
      subtitle: item.sku || item.code || 'Kod tanımsız',
      meta:
        available !== undefined
          ? `Kullanılabilir: ${formatQuantity(available)} ${item.stock_unit || ''}`.trim()
          : undefined
    };
  }

  function transferProductEntity(item: ProductOption, availableQuantity?: string): EntityOption {
    if (availableQuantity === undefined) {
      return { ...productEntity(item), meta: undefined };
    }
    return productEntity(item, availableQuantity);
  }

  function warehouseEntity(item: WarehouseOption): EntityOption {
    const meta = [item.address, warehouseType(item) === 'STANDARD' ? '' : warehouseTypeLabel(item)]
      .filter(Boolean)
      .join(' · ');
    const labelPrefix = item.code ? `${item.code} · ${item.name}` : item.name;
    return {
      id: item.id,
      title: item.name,
      subtitle: item.code || 'Kod tanımsız',
      meta: meta || warehouseOptionLabel(item).replace(labelPrefix, '').trim()
    };
  }

  function activeWarehouses() {
    return warehouses.filter(isActiveDestinationWarehouse);
  }

  function activeStandardWarehouses() {
    return warehouses.filter(isActiveStandardWarehouse);
  }

  function stockWarehouseOptions() {
    return stock.direction === 'OUT' ? activeStandardWarehouses() : activeWarehouses();
  }

  function warehouseReferencesBlocked() {
    return (
      (kind === 'stock-movement' || kind === 'transfer' || kind === 'count') &&
      warehouseState !== 'ready'
    );
  }

  function warehouseOption(id: string) {
    return warehouses.find((item) => item.id === id);
  }

  function partyEntity(item: PartyOption): EntityOption {
    return {
      id: item.id,
      title: item.display_name,
      subtitle: item.code || 'Kod tanımsız',
      meta: item.default_currency
    };
  }

  async function searchParties(query: string, signal: AbortSignal): Promise<EntityOption[]> {
    const result = await api<{ items?: PartyOption[] }>(
      `/parties?q=${encodeURIComponent(query)}&limit=30`,
      { signal }
    );
    return (result.items ?? []).map(partyEntity);
  }

  function isPhysicalProduct(item: ProductOption) {
    return String(item.kind ?? 'PHYSICAL').toUpperCase() !== 'SERVICE';
  }

  async function searchProducts(
    query: string,
    signal: AbortSignal,
    warehouseID = ''
  ): Promise<EntityOption[]> {
    const params = new URLSearchParams({ limit: '30' });
    if (query.trim()) params.set('q', query.trim());
    if (warehouseID) params.set('warehouse_id', warehouseID);
    const result = await api<{ items?: ProductOption[] }>(`/products?${params}`, { signal });
    return (result.items ?? []).filter(isPhysicalProduct).map((item) => productEntity(item));
  }

  function searchTransferProducts(query: string, signal: AbortSignal) {
    return searchProducts(query, signal, transfer.sourceWarehouseID).then((items) =>
      items.map((item) => ({ ...item, meta: undefined }))
    );
  }

  function transferProductOptions() {
    return products.filter(isPhysicalProduct);
  }

  function nextTransferLineRequestKey() {
    transferLineRequestSequence += 1;
    return `transfer-line:${transferLineRequestSequence}`;
  }

  function apiErrorCode(cause: unknown) {
    if (typeof cause === 'object' && cause && 'code' in cause) {
      return String((cause as { code?: unknown }).code ?? '');
    }
    return '';
  }

  function transferLinesLoading() {
    return transfer.lines.some((line) => line.loading);
  }

  function variantOptionLabel(variant: VariantOption) {
    const code = variant.variant_code.trim();
    const name = variant.variant_name?.trim();
    return name && name !== code ? `${code} · ${name}` : code;
  }

  function variantDisplay(variant: VariantOption): Record<string, unknown> {
    if (variant.values?.length) {
      return Object.fromEntries(
        variant.values.map((value) => [
          value.definition_name || value.definition_code || value.definition_id,
          value.option_name || value.option_code || value.option_id
        ])
      );
    }
    return variant.attributes ?? {};
  }

  function stockVariantRow(
    variant: VariantOption,
    previous?: VariantStockMovementRow
  ): VariantStockMovementRow {
    return {
      id: variant.id,
      variant_id: variant.id,
      variant_code: variant.variant_code,
      sku: variant.sku,
      attributes: variant.attributes,
      variant_display: variantDisplay(variant),
      physical_quantity: variant.physical_quantity ?? '0',
      reserved_quantity: variant.reserved_quantity ?? '0',
      available_quantity: variant.available_quantity ?? '0',
      quantity: previous?.quantity ?? '',
      unit_cost: previous?.unit_cost ?? '',
      is_active: variant.is_active
    };
  }

  function mapStockVariantRows(items: VariantOption[]) {
    const previousByID = new Map(stockVariantRows.map((row) => [row.id, row]));
    return items
      .filter((variant) => variant.is_active !== false)
      .map((variant) => stockVariantRow(variant, previousByID.get(variant.id)));
  }

  function transferLineProduct(line: TransferLineDraft) {
    return products.find((item) => item.id === line.productID);
  }

  function transferWarehouseLabel(item: WarehouseOption) {
    return warehouseOptionLabel(item);
  }

  async function loadTransferProducts(warehouseID: string) {
    if (kind !== 'transfer' || !warehouseID) return;
    const requestKey = warehouseID;
    transferProductsRequestKey = requestKey;
    try {
      const result = await api<{ items?: ProductOption[] }>(
        `/products?warehouse_id=${encodeURIComponent(warehouseID)}&limit=100`
      );
      if (transferProductsRequestKey === requestKey) {
        products = (result.items ?? []).filter(isPhysicalProduct);
      }
    } catch (cause) {
      if (transferProductsRequestKey === requestKey) {
        error = errorMessage(cause, 'Çıkış deposundaki stok kartları alınamadı.');
      }
    }
  }

  async function loadTransferLinePosition(line: TransferLineDraft) {
    const requestKey = nextTransferLineRequestKey();
    line.requestKey = requestKey;
    line.availableQuantity = '';
    line.error = '';
    line.loading = true;
    if (!transfer.sourceWarehouseID || !line.productID) {
      line.loading = false;
      return;
    }
    if (line.variantRequired && !line.variantID) {
      line.loading = false;
      return;
    }
    const params = new URLSearchParams({
      warehouse_id: transfer.sourceWarehouseID,
      product_id: line.productID
    });
    if (line.variantID) params.set('variant_id', line.variantID);
    try {
      const result = await api<PositionOption>(`/stock/positions?${params}`);
      if (line.requestKey === requestKey) {
        line.availableQuantity = result.available_quantity ?? '0';
      }
    } catch (cause) {
      if (line.requestKey === requestKey) {
        line.availableQuantity = '';
        line.error = errorMessage(cause, 'Bu stok için çıkış bakiyesi okunamadı.');
      }
    } finally {
      if (line.requestKey === requestKey) line.loading = false;
    }
  }

  async function loadTransferLineData(line: TransferLineDraft) {
    const requestKey = nextTransferLineRequestKey();
    line.requestKey = requestKey;
    line.availableQuantity = '';
    line.stockUnit = '';
    line.variants = [];
    line.variantRequired = false;
    line.metadataState = 'loading';
    line.loading = true;
    line.error = '';
    if (!transfer.sourceWarehouseID || !line.productID) {
      line.metadataState = 'idle';
      line.loading = false;
      return;
    }
    let variantMetadataLoaded = false;
    try {
      const productID = encodeURIComponent(line.productID);
      const warehouseID = encodeURIComponent(transfer.sourceWarehouseID);
      const variantResult = await api<{ items?: VariantOption[] }>(
        `/products/${productID}/variants`
      );
      if (line.requestKey !== requestKey) return;
      const selectedProduct = transferLineProduct(line);
      line.stockUnit = selectedProduct?.stock_unit ?? '';
      const allVariants = variantResult.items ?? [];
      variantMetadataLoaded = true;
      line.variantRequired = transferLineProduct(line)?.variants_enabled === true;
      line.variants = allVariants.filter((item) => item.is_active !== false);
      if (!line.variants.some((item) => item.id === line.variantID)) {
        line.variantID = '';
      }
      if (line.variantRequired && line.variants.length === 0) {
        line.error = 'Bu stok kartında kullanılabilir aktif varyant yok.';
        line.metadataState = 'ready';
        return;
      }
      if (line.variantRequired && !line.variantID) {
        line.metadataState = 'ready';
        return;
      }

      const params = new URLSearchParams({
        warehouse_id: transfer.sourceWarehouseID,
        product_id: line.productID
      });
      if (line.variantID) params.set('variant_id', line.variantID);
      const position = await api<PositionOption>(`/stock/positions?${params}`);
      if (line.requestKey === requestKey) {
        line.availableQuantity = position.available_quantity ?? '0';
        line.metadataState = 'ready';
      }
    } catch (cause) {
      if (line.requestKey === requestKey) {
        line.availableQuantity = '';
        line.variants = [];
        line.metadataState = variantMetadataLoaded ? 'ready' : 'error';
        line.error = variantMetadataLoaded
          ? errorMessage(cause, 'Bu stok için çıkış bakiyesi okunamadı.')
          : 'Varyant bilgisi alınamadı.';
      }
    } finally {
      if (line.requestKey === requestKey) line.loading = false;
    }
  }

  async function refreshTransferLineBalances() {
    await Promise.all(
      transfer.lines.filter((line) => line.productID).map((line) => loadTransferLinePosition(line))
    );
  }

  function selectTransferSource(value: string) {
    transfer.sourceWarehouseID = value;
    if (transfer.destinationWarehouseID === value) transfer.destinationWarehouseID = '';
    for (const line of transfer.lines) {
      line.productID = '';
      line.variantID = '';
      line.variantRequired = false;
      line.metadataState = 'idle';
      line.availableQuantity = '';
      line.stockUnit = '';
      line.variants = [];
      line.loading = false;
      line.error = '';
      line.requestKey = nextTransferLineRequestKey();
    }
    void loadTransferProducts(value);
  }

  function selectTransferDestination(value: string) {
    transfer.destinationWarehouseID = value === transfer.sourceWarehouseID ? '' : value;
  }

  function selectTransferProduct(line: TransferLineDraft, option: EntityOption) {
    line.productID = option.id;
    line.variantID = '';
    line.variantRequired = false;
    line.metadataState = 'idle';
    line.availableQuantity = '';
    line.error = '';
    const product = products.find((item) => item.id === option.id);
    if (!product) {
      products = [
        ...products,
        {
          id: option.id,
          name: option.title,
          sku: option.subtitle,
          kind: 'PHYSICAL'
        }
      ];
    }
    void loadTransferLineData(line);
  }

  function transferDestinationOptions() {
    return activeWarehouses().filter((item) => item.id !== transfer.sourceWarehouseID);
  }

  function stockReasons(direction = stock.direction) {
    return direction === 'OUT' ? outboundReasons : inboundReasons;
  }

  function resetStockMovementProductState(clearProduct = false) {
    error = '';
    if (clearProduct) stock.productID = '';
    stock.warehouseID = '';
    stock.variantID = '';
    stock.quantity = '';
    stock.unitCode = '';
    stock.unitCost = '';
    stockAvailableQuantity = '';
    stockPositionLoading = false;
    stockPositionError = '';
    stockPositionRequestKey = '';
    stockVariantRequired = false;
    stockVariantRows = [];
    stockVariantValidationErrors = {};
    stockVariantTableValid = false;
    stockVariantPositionLoading = false;
    stockVariantPositionError = '';
    stockVariantPositionRequestKey = '';
    units = [];
    unitLoading = false;
    unitState = 'idle';
    unitError = '';
    unitRequestKey = '';
    variants = [];
    variantLoading = false;
    variantState = 'idle';
    variantError = '';
    variantRequestKey = '';
    stockWarehouseNotice = '';
  }

  function selectStockProduct(option: EntityOption) {
    if (stock.productID === option.id) return;
    stock.productID = option.id;
    resetStockMovementProductState();
    if (!products.some((item) => item.id === option.id)) {
      products = [
        ...products,
        { id: option.id, name: option.title, sku: option.subtitle, kind: 'PHYSICAL' }
      ];
    }
  }

  function resetStockMovementWarehouseState() {
    stock.quantity = '';
    stock.unitCost = '';
    stockAvailableQuantity = '';
    stockPositionLoading = false;
    stockPositionError = '';
    stockPositionRequestKey = '';
    stockVariantPositionLoading = false;
    stockVariantPositionError = '';
    stockVariantPositionRequestKey = '';
    stockVariantRows = stockVariantRequired
      ? variants
          .filter((variant) => variant.is_active !== false)
          .map((variant) => ({
            ...stockVariantRow(variant),
            physical_quantity: '',
            reserved_quantity: '',
            available_quantity: ''
          }))
      : [];
    stockVariantValidationErrors = {};
    stockVariantTableValid = false;
  }

  function selectStockWarehouse(value: string) {
    if (stock.warehouseID === value) return;
    stock.warehouseID = value;
    stockWarehouseNotice = '';
    resetStockMovementWarehouseState();
  }

  function setStockDirection(direction: string) {
    const directionChanged = stock.direction !== direction;
    stock.direction = direction;
    if (
      direction === 'OUT' &&
      stock.warehouseID &&
      (!warehouseOption(stock.warehouseID) ||
        !isActiveStandardWarehouse(warehouseOption(stock.warehouseID)!))
    ) {
      stock.warehouseID = '';
      stockWarehouseNotice = 'Çıkış için aktif normal depo seçin.';
    } else if (directionChanged) {
      stockWarehouseNotice = '';
    }
    if (!stockReasons(direction).some((reason) => reason.value === stock.reasonCode)) {
      stock.reasonCode = '';
      stock.reasonDescription = '';
    } else if (directionChanged) {
      stock.reasonDescription = '';
    }
    if (direction === 'IN' && stockVariantRequired && variants.length > 0) {
      stockVariantPositionRequestKey = '';
      stockVariantRows = stockVariantRequired ? mapStockVariantRows(variants) : [];
      stockVariantPositionError = '';
    }
  }

  function applyWarehouseDefaults() {
    const standardWarehouses = activeStandardWarehouses();
    const destinationWarehouses = activeWarehouses();

    // Only auto-select when the eligible choice is unambiguous.
    if (kind === 'transfer') {
      if (!transfer.sourceWarehouseID && standardWarehouses.length === 1) {
        transfer.sourceWarehouseID = standardWarehouses[0].id;
      }
      const destinationOptions = destinationWarehouses.filter(
        (item) => item.id !== transfer.sourceWarehouseID
      );
      if (!transfer.destinationWarehouseID && destinationOptions.length === 1) {
        transfer.destinationWarehouseID = destinationOptions[0].id;
      }
    }
    if (kind === 'count' && !count.warehouseID && standardWarehouses.length === 1) {
      count.warehouseID = standardWarehouses[0].id;
    }
  }

  async function loadWarehouseReferences(force = false) {
    if (warehouseLoading || (!force && warehouseState === 'ready')) return;
    const requestKey = ++warehouseRequestKey;
    warehouseLoading = true;
    warehouseState = 'loading';
    warehouseError = '';
    try {
      const result = await listWarehouses();
      if (warehouseRequestKey !== requestKey) return;
      warehouses = (result.items || []).filter(isActiveDestinationWarehouse);
      warehouseState = 'ready';
      applyWarehouseDefaults();
    } catch (cause) {
      if (warehouseRequestKey !== requestKey) return;
      warehouseState = 'error';
      warehouseError = errorMessage(cause, 'Depolar alınamadı.');
    } finally {
      if (warehouseRequestKey === requestKey) warehouseLoading = false;
    }
  }

  async function loadProductReferences() {
    try {
      const result = await api<{ items: ProductOption[] }>('/products?limit=100');
      products = (result.items || []).filter(isPhysicalProduct);
    } catch (cause) {
      error = errorMessage(cause, 'Stok kartları alınamadı.');
    }
  }

  async function loadReferences() {
    if (referencesLoaded || loadingReferences) return;
    loadingReferences = true;
    error = '';
    try {
      const requests: Promise<void>[] = [];
      if (kind === 'manual' || kind === 'collection' || kind === 'payment') {
        requests.push(
          api<{ items: PartyOption[] }>('/parties?limit=100').then((result) => {
            parties = result.items || [];
          })
        );
      }
      if (kind === 'collection' || kind === 'payment' || kind === 'manual') {
        requests.push(
          getExchangeRateDashboard()
            .then((dashboard) => {
              exchangeBase = dashboard.base_currency || 'TRY';
              const next: Record<string, string> = {};
              for (const item of dashboard.items ?? [])
                next[item.currency_code] = item.rate_to_base;
              exchangeRates = next;
              applyExchangeRate(payment);
              applyExchangeRate(manual);
            })
            .catch(() => undefined)
        );
      }
      if (kind === 'collection' || kind === 'payment') {
        requests.push(
          Promise.all([
            api<{ items: AccountOption[] }>('/finance/cash-accounts?limit=100').catch(() => ({
              items: []
            })),
            api<{ items: AccountOption[] }>('/finance/bank-accounts?limit=100').catch(() => ({
              items: []
            }))
          ]).then(([cash, bank]) => {
            accounts = [...(cash.items || []), ...(bank.items || [])];
          })
        );
      }
      if (kind === 'stock-movement' || kind === 'transfer' || kind === 'count') {
        // Keep warehouse loading independent. A product or finance reference
        // failure must not make the warehouse picker look empty.
        requests.push(loadWarehouseReferences());
      }
      if (kind === 'stock-movement' || kind === 'transfer') {
        requests.push(loadProductReferences());
      }
      await Promise.allSettled(requests);
      if (!payment.accountID && paymentAccounts[0]) payment.accountID = paymentAccounts[0].id;
      if (kind === 'transfer' && transfer.sourceWarehouseID) {
        await loadTransferProducts(transfer.sourceWarehouseID);
      }
      referencesLoaded = true;
    } catch (cause) {
      error = errorMessage(cause, 'Referans kayıtları alınamadı.');
    } finally {
      loadingReferences = false;
    }
  }

  function addTransferLine() {
    transfer.lines = [...transfer.lines, newTransferLine()];
  }

  function removeTransferLine(index: number) {
    if (transfer.lines.length <= 1) return;
    transfer.lines = transfer.lines.filter((_, lineIndex) => lineIndex !== index);
  }

  async function loadOpenItems() {
    if (!payment.partyID || !payment.currency || !(kind === 'collection' || kind === 'payment')) {
      allocationRows = [];
      return;
    }
    const key = `${kind}:${payment.partyID}:${payment.currency}`;
    if (key === allocationRequestKey) return;
    allocationRequestKey = key;
    autoAllocateRequested = false;
    allocationNotice = '';
    allocationLoading = true;
    try {
      const side = kind === 'payment' ? 'PAYABLE' : 'RECEIVABLE';
      const result = await api<{ items?: Array<Record<string, unknown>> }>(
        `/invoice-open-items?party_id=${encodeURIComponent(payment.partyID)}&currency=${encodeURIComponent(payment.currency)}&side=${side}&limit=100`
      );
      allocationRows = (result.items ?? []).map((item) => ({
        id: String(item.id),
        document_id: item.document_id ? String(item.document_id) : undefined,
        document_no: item.document_no ? String(item.document_no) : undefined,
        document_date: String(item.document_date ?? ''),
        due_date: item.due_date ? String(item.due_date) : undefined,
        open_amount: String(item.open_amount ?? '0'),
        applied: '0'
      }));
      const prefillDocumentID = paymentPrefill?.documentID?.trim();
      const prefillKey = `${kind}:${prefillDocumentID ?? ''}`;
      if (prefillDocumentID && prefillKey !== prefillAllocationAppliedKey) {
        const invoice = allocationRows.find((row) => row.document_id === prefillDocumentID);
        if (invoice) {
          payment.amount = invoice.open_amount;
          invoice.applied = invoice.open_amount;
          prefillAllocationAppliedKey = prefillKey;
        }
      }
    } catch {
      allocationRows = [];
    } finally {
      allocationLoading = false;
    }
  }

  async function loadProductUnits(productID: string, force = false) {
    if (
      !productID ||
      kind !== 'stock-movement' ||
      (!force && productID === unitRequestKey && unitState !== 'error')
    )
      return;
    unitRequestKey = productID;
    unitLoading = true;
    unitState = 'loading';
    unitError = '';
    units = [];
    stock.unitCode = '';
    try {
      const result = await api<{ units?: UnitOption[] }>(
        `/products/${encodeURIComponent(productID)}`
      );
      if (unitRequestKey !== productID) return;
      units = result.units ?? [];
      if (units.length === 0) {
        unitState = 'error';
        unitError = 'Birim bilgisi alınamadı.';
        return;
      }
      stock.unitCode = units.find((unit) => unit.is_base)?.code ?? units[0].code;
      unitState = 'ready';
    } catch {
      if (unitRequestKey !== productID) return;
      units = [];
      unitState = 'error';
      unitError = 'Birim bilgisi alınamadı.';
    } finally {
      if (unitRequestKey === productID) unitLoading = false;
    }
  }

  async function loadStockVariantPositions() {
    const requestKey = `${stock.warehouseID}:${stock.productID}:${stock.direction}:${variants
      .map((variant) => variant.id)
      .join(',')}`;
    if (requestKey === stockVariantPositionRequestKey) return;
    stockVariantPositionRequestKey = requestKey;
    stockVariantPositionError = '';

    if (
      kind !== 'stock-movement' ||
      !stock.warehouseID ||
      !stock.productID ||
      variants.length === 0
    ) {
      stockVariantPositionLoading = false;
      return;
    }

    stockVariantPositionLoading = true;
    stockVariantRows = stockVariantRows.map((row) => ({
      ...row,
      physical_quantity: '',
      reserved_quantity: '',
      available_quantity: ''
    }));
    try {
      const positions = await Promise.all(
        variants
          .filter((variant) => variant.is_active !== false)
          .map(async (variant) => {
            const params = new URLSearchParams({
              warehouse_id: stock.warehouseID,
              product_id: stock.productID,
              variant_id: variant.id
            });
            const position = await api<PositionOption>(`/stock/positions?${params}`);
            return [variant.id, position] as const;
          })
      );
      if (stockVariantPositionRequestKey === requestKey) {
        const positionByVariant = new Map(positions);
        stockVariantRows = stockVariantRows.map((row) => {
          const position = positionByVariant.get(row.id);
          return position
            ? {
                ...row,
                physical_quantity: position.physical_quantity ?? '0',
                reserved_quantity: position.reserved_quantity ?? '0',
                available_quantity: position.available_quantity ?? '0'
              }
            : row;
        });
      }
    } catch (cause) {
      if (stockVariantPositionRequestKey === requestKey) {
        stockVariantPositionError = errorMessage(cause, 'Varyant çıkış bakiyeleri okunamadı.');
      }
    } finally {
      if (stockVariantPositionRequestKey === requestKey) stockVariantPositionLoading = false;
    }
  }

  async function loadProductVariants(productID: string, force = false) {
    if (
      !productID ||
      kind !== 'stock-movement' ||
      (!force && productID === variantRequestKey && variantState !== 'error')
    )
      return;
    variantRequestKey = productID;
    stockVariantPositionRequestKey = '';
    stockVariantPositionLoading = false;
    stockVariantRows = [];
    stockVariantValidationErrors = {};
    stockVariantTableValid = false;
    stockVariantRequired = false;
    variantLoading = true;
    variantState = 'loading';
    variantError = '';
    try {
      const encodedProductID = encodeURIComponent(productID);
      const [variantResult, productResult] = await Promise.all([
        api<{ items?: VariantOption[] }>(`/products/${encodedProductID}/variants`),
        api<Pick<ProductOption, 'variants_enabled'>>(`/products/${encodedProductID}`)
      ]);
      if (variantRequestKey !== productID) return;
      const allVariants = variantResult.items ?? [];
      variants = allVariants.filter((variant) => variant.is_active !== false);
      // The product response is authoritative. A stale or orphaned variant row
      // must never switch a simple product to operation-matrix mode.
      stockVariantRequired = productResult.variants_enabled === true;
      products = products.map((item) =>
        item.id === productID ? { ...item, variants_enabled: productResult.variants_enabled } : item
      );
      stockVariantRows = stockVariantRequired ? mapStockVariantRows(variants) : [];
      stockVariantValidationErrors = {};
      stockVariantTableValid = false;
      stockVariantPositionError = '';
      if (!variants.some((variant) => variant.id === stock.variantID)) {
        stock.variantID = '';
      }
      variantState = 'ready';
    } catch {
      if (variantRequestKey !== productID) return;
      variants = [];
      // Do not guess the product mode while the authoritative metadata is unavailable.
      // Submission remains blocked by variantState until the user retries.
      stockVariantRequired = false;
      variantState = 'error';
      variantError = 'Varyant bilgisi alınamadı.';
      stockVariantRows = [];
      stockVariantValidationErrors = {};
      stockVariantTableValid = false;
      stockVariantPositionError = '';
      stock.variantID = '';
    } finally {
      if (variantRequestKey === productID) variantLoading = false;
    }
  }

  async function loadStockPosition() {
    const requestKey = `${stock.warehouseID}:${stock.productID}:${stock.variantID}:${stock.direction}`;
    stockPositionRequestKey = requestKey;
    stockAvailableQuantity = '';
    stockPositionError = '';
    if (
      kind !== 'stock-movement' ||
      stock.direction !== 'OUT' ||
      !stock.warehouseID ||
      !stock.productID ||
      variantLoading ||
      (stockVariantRequired && !stock.variantID)
    ) {
      stockPositionLoading = false;
      return;
    }
    stockPositionLoading = true;
    try {
      const params = new URLSearchParams({
        warehouse_id: stock.warehouseID,
        product_id: stock.productID
      });
      if (stock.variantID) params.set('variant_id', stock.variantID);
      const result = await api<PositionOption>(`/stock/positions?${params}`);
      if (stockPositionRequestKey === requestKey) {
        stockAvailableQuantity = result.available_quantity ?? '0';
      }
    } catch (cause) {
      if (stockPositionRequestKey === requestKey) {
        stockAvailableQuantity = '';
        stockPositionError = errorMessage(cause, 'Çıkış bakiyesi okunamadı.');
      }
    } finally {
      if (stockPositionRequestKey === requestKey) stockPositionLoading = false;
    }
  }

  async function autoAllocate() {
    allocationNotice = '';
    if (!payment.partyID) {
      allocationNotice = 'Önce cari seçin.';
      return;
    }
    if (!payment.currency) {
      allocationNotice = 'Önce para birimi seçin.';
      return;
    }
    if (!payment.amount || decimalNumber(payment.amount) <= 0) {
      allocationNotice = 'Önce tutar girin.';
      return;
    }
    try {
      const result = await api<{
        allocations?: Array<{ open_item_id: string; amount: string }>;
        reason?: string;
      }>('/finance/allocation-preview', {
        method: 'POST',
        body: JSON.stringify({
          party_id: payment.partyID,
          currency: payment.currency,
          payment_kind: kind === 'payment' ? 'PAYMENT' : 'COLLECTION',
          amount: canonicalDecimal(payment.amount)
        })
      });
      if (result.reason === 'NO_OPEN_ITEMS' || (result.allocations ?? []).length === 0) {
        autoAllocateRequested = false;
        allocationNotice = `Bu cari ve ${payment.currency} para biriminde açık fatura yok. Tutar cari avansı olarak kalır.`;
        allocationRows = allocationRows.map((row) => ({ ...row, applied: '0' }));
        return;
      }
      const byID = new Map(
        (result.allocations ?? []).map((item) => [item.open_item_id, item.amount])
      );
      allocationRows = allocationRows.map((row) => ({ ...row, applied: byID.get(row.id) ?? '0' }));
      autoAllocateRequested = true;
      allocationNotice =
        'Kaydederken en eski borçlardan otomatik dağıtılacak. Bir satırı elle değiştirirseniz girdiğiniz tutarlar kullanılır.';
    } catch (cause) {
      autoAllocateRequested = false;
      const message = errorMessage(cause, 'Otomatik dağıtım önerisi alınamadı.');
      if (
        typeof cause === 'object' &&
        cause &&
        'code' in cause &&
        cause.code === 'ALLOCATION_CURRENCY_REQUIRED'
      ) {
        allocationNotice = 'Önce para birimi seçin.';
      } else {
        allocationNotice = message;
      }
    }
  }

  function onAllocationInput() {
    // A manual edit means the caller wants an explicit allocations payload.
    autoAllocateRequested = false;
  }

  function errorMessage(cause: unknown, fallback: string) {
    if (typeof cause === 'object' && cause && 'message' in cause) {
      const message = String((cause as { message: unknown }).message);
      if (message.trim()) return message;
    }
    return fallback;
  }

  function allocationTotal() {
    return allocationRows.reduce(
      (sum, row) => addDecimalStrings(sum, canonicalDecimal(row.applied) || '0'),
      '0'
    );
  }

  function unappliedAmount() {
    return subtractDecimalStrings(canonicalDecimal(payment.amount), allocationTotal());
  }

  async function post(path: string, body: Record<string, unknown>, key: string) {
    await api(path, {
      method: 'POST',
      headers: { 'Idempotency-Key': key },
      body: JSON.stringify({ ...body, idempotency_key: key })
    });
  }

  function stockMetadataBlocked() {
    if (kind !== 'stock-movement' || !stock.productID) return false;
    return unitState !== 'ready' || variantState !== 'ready';
  }

  function stockVariantSubmissionBlocked() {
    return (
      kind === 'stock-movement' &&
      stockVariantRequired &&
      (stockVariantPositionLoading ||
        Boolean(stockVariantPositionError) ||
        stockVariantRows.length === 0 ||
        !stockVariantTableValid)
    );
  }

  function transferMetadataError() {
    for (const [index, line] of transfer.lines.entries()) {
      if (!line.productID) continue;
      if (line.metadataState === 'loading') {
        return `${index + 1}. satırın varyant bilgisi yükleniyor.`;
      }
      if (line.metadataState !== 'ready') {
        return `${index + 1}. satır için Varyant bilgisi alınamadı. Yeniden deneyin.`;
      }
      if (line.error) {
        return `${index + 1}. satırın stok bakiyesi doğrulanamadı.`;
      }
    }
    return '';
  }

  async function submit() {
    if (submitting) return;
    submitting = true;
    error = '';
    try {
      if (kind === 'manual' && !manual.partyID) throw new Error('Cari kartı seçin.');
      if (kind === 'manual' && !isValidDateValue(manual.transactionDate)) {
        throw new Error('Geçerli bir işlem tarihi seçin.');
      }
      if ((kind === 'collection' || kind === 'payment') && !payment.partyID) {
        throw new Error('Cari kartı seçin.');
      }
      if (negativeBalancePrompt && !overrideReason.trim()) {
        throw new Error('Negatif bakiye için gerekçe zorunludur.');
      }
      if (
        (kind === 'collection' || kind === 'payment') &&
        !isValidDateValue(payment.transactionDate)
      ) {
        throw new Error('Geçerli bir işlem tarihi seçin.');
      }
      if (warehouseReferencesBlocked()) {
        if (warehouseState === 'loading') throw new Error('Depolar yükleniyor.');
        if (warehouseState === 'error') {
          throw new Error('Depolar alınamadı. Yeniden deneyin.');
        }
        throw new Error('Depo bilgileri henüz hazır değil.');
      }
      if (kind === 'stock-movement' && !stock.warehouseID) throw new Error('Depo seçin.');
      if (kind === 'stock-movement' && !stock.productID) throw new Error('Stok kartı seçin.');
      if (kind === 'stock-movement') {
        const selectedWarehouse = warehouseOption(stock.warehouseID);
        if (!selectedWarehouse || !isActiveDestinationWarehouse(selectedWarehouse)) {
          throw new Error('Pasif depolarda yeni stok hareketi oluşturulamaz.');
        }
        if (stock.direction === 'OUT' && !isActiveStandardWarehouse(selectedWarehouse)) {
          throw new Error('Stok çıkışı yalnızca aktif normal depolardan yapılabilir.');
        }
        if (variantLoading) throw new Error('Varyant bilgileri yükleniyor.');
        if (unitState === 'loading') throw new Error('Birim bilgisi yükleniyor.');
        if (unitState !== 'ready') {
          throw new Error('Birim bilgisi alınamadı. Yeniden deneyin.');
        }
        if (variantState !== 'ready') {
          throw new Error('Varyant bilgisi alınamadı. Yeniden deneyin.');
        }
        if (stockVariantRequired) {
          if (stockVariantPositionLoading) throw new Error('Varyant bakiyeleri yükleniyor.');
          if (stockVariantPositionError) throw new Error(stockVariantPositionError);
          if (stockVariantRows.length === 0) {
            throw new Error('Bu stok kartında kullanılabilir aktif varyant yok.');
          }
          if (!stockVariantTableValid) {
            throw new Error(
              Object.values(stockVariantValidationErrors)[0] ||
                'En az bir varyanta geçerli miktar girin.'
            );
          }
        }
        if (stock.direction === 'OUT' && !stockVariantRequired) {
          await loadStockPosition();
          if (stockPositionError) throw new Error(stockPositionError);
          if (!stockAvailableQuantity.trim()) {
            throw new Error('Güncel kullanılabilir bakiye doğrulanamadı.');
          }
          if (compareDecimal(canonicalDecimal(stock.quantity), stockAvailableQuantity) > 0) {
            throw new Error('Miktar çıkış deposundaki kullanılabilir bakiyeyi aşamaz.');
          }
        }
      }
      if (kind === 'manual') {
        const key = idempotencyKey('manual-entry');
        await post(
          '/finance/manual-entries',
          {
            party_id: manual.partyID,
            entry_kind: manual.entryKind,
            currency: manual.currency,
            amount: canonicalDecimal(manual.amount),
            exchange_rate: canonicalDecimal(manual.exchangeRate),
            description: manual.description,
            transaction_date: timestamp(manual.transactionDate),
            due_date: manual.dueDate ? timestamp(manual.dueDate) : undefined,
            reference_no: manual.referenceNo
          },
          key
        );
      } else if (kind === 'collection' || kind === 'payment') {
        const body = {
          party_id: payment.partyID,
          account_id: payment.accountID,
          payment_kind: kind === 'collection' ? 'COLLECTION' : 'PAYMENT',
          payment_method: payment.method,
          currency: payment.currency,
          amount: canonicalDecimal(payment.amount),
          exchange_rate: canonicalDecimal(payment.exchangeRate),
          reference_no: payment.referenceNo,
          description: payment.description,
          transaction_date: timestamp(payment.transactionDate),
          override_reason: negativeBalancePrompt ? overrideReason.trim() : undefined,
          auto_allocate: autoAllocateRequested ? true : undefined,
          allocations: autoAllocateRequested
            ? undefined
            : allocationRows
                .filter((row) => decimalNumber(row.applied) > 0)
                .map((row) => ({ open_item_id: row.id, amount: canonicalDecimal(row.applied) }))
        };
        const payload = JSON.stringify(body);
        if (!financeCreateAttempt || financeCreateAttempt.payload !== payload) {
          financeCreateAttempt = { payload, key: idempotencyKey(kind) };
        }
        await post(
          `/finance/${kind === 'collection' ? 'collections' : 'payments'}`,
          body,
          financeCreateAttempt.key
        );
        financeCreateAttempt = undefined;
      } else if (kind === 'stock-movement') {
        const key = idempotencyKey('stock-movement');
        if (stockVariantRequired) {
          await post(
            '/stock-movement-operations',
            {
              warehouse_id: stock.warehouseID,
              product_id: stock.productID,
              movement_type: 'MANUAL_ADJUSTMENT',
              direction: stock.direction,
              unit_code: stock.unitCode,
              ...(stock.direction === 'IN' ? { currency: stock.currency } : {}),
              reason_code: stock.reasonCode,
              reason_description: stock.reasonDescription,
              lines: stockVariantRows
                .filter((row) => row.quantity?.trim())
                .map((row) => ({
                  variant_id: row.variant_id ?? row.id,
                  quantity: canonicalDecimal(row.quantity),
                  ...(stock.direction === 'IN' && row.unit_cost?.trim()
                    ? { unit_cost: canonicalDecimal(row.unit_cost) }
                    : {})
                }))
            },
            key
          );
        } else {
          await post(
            '/stock-movements',
            {
              warehouse_id: stock.warehouseID,
              product_id: stock.productID,
              variant_id: stock.variantID,
              movement_type: 'MANUAL_ADJUSTMENT',
              direction: stock.direction,
              quantity: canonicalDecimal(stock.quantity),
              entered_quantity: canonicalDecimal(stock.quantity),
              unit_code: stock.unitCode,
              ...(stock.direction === 'IN' && stock.unitCost.trim()
                ? { unit_cost: canonicalDecimal(stock.unitCost), currency: stock.currency }
                : {}),
              reason_code: stock.reasonCode,
              reason_description: stock.reasonDescription,
              source_type: 'MANUAL_STOCK_MOVEMENT'
            },
            key
          );
        }
      } else if (kind === 'warehouse') {
        await api('/warehouses', {
          method: 'POST',
          body: JSON.stringify({
            code: warehouse.code,
            name: warehouse.name,
            type: warehouse.type,
            address: warehouse.address
          })
        });
      } else if (kind === 'transfer') {
        if (transfer.sourceWarehouseID === transfer.destinationWarehouseID) {
          throw new Error('Çıkış ve varış deposu aynı olamaz.');
        }
        const sourceWarehouse = warehouseOption(transfer.sourceWarehouseID);
        const destinationWarehouse = warehouseOption(transfer.destinationWarehouseID);
        if (!sourceWarehouse || !isActiveStandardWarehouse(sourceWarehouse)) {
          throw new Error('Transfer çıkışı yalnızca aktif normal depolardan yapılabilir.');
        }
        if (!destinationWarehouse || !isActiveDestinationWarehouse(destinationWarehouse)) {
          throw new Error('Transfer varış deposu aktif olmalıdır.');
        }
        if (transfer.lines.length === 0) throw new Error('En az bir stok satırı ekleyin.');
        const metadataError = transferMetadataError();
        if (metadataError) throw new Error(metadataError);
        // The balance shown while the dialog was open may be stale. Refresh it
        // immediately before the local guard so a newer receipt or reservation
        // cannot produce a false client-side rejection.
        await refreshTransferLineBalances();
        const quantityError = validateTransferQuantities(transfer.lines);
        if (quantityError) throw new Error(quantityError);
        const payload = JSON.stringify({
          transfer_no: transfer.transferNo,
          transfer_type: transfer.transferType,
          source_warehouse_id: transfer.sourceWarehouseID,
          destination_warehouse_id: transfer.destinationWarehouseID,
          lines: transfer.lines.map((line) => ({
            product_id: line.productID,
            variant_id: line.variantID,
            quantity: canonicalDecimal(line.quantity)
          }))
        });
        if (!transferCreateAttempt || transferCreateAttempt.payload !== payload) {
          transferCreateAttempt = {
            payload,
            key: idempotencyKey('warehouse-transfer')
          };
        }
        await api('/warehouse-transfers', {
          method: 'POST',
          headers: { 'Idempotency-Key': transferCreateAttempt.key },
          body: payload
        });
        transferCreateAttempt = undefined;
      } else if (kind === 'count') {
        const countWarehouse = warehouseOption(count.warehouseID);
        if (!countWarehouse || !isActiveStandardWarehouse(countWarehouse)) {
          throw new Error('Sayım yalnızca aktif normal depolarda başlatılabilir.');
        }
        await api('/stock-counts', {
          method: 'POST',
          headers: { 'Idempotency-Key': idempotencyKey('stock-count') },
          body: JSON.stringify({
            warehouse_id: count.warehouseID,
            movement_policy: 'CONTINUE'
          })
        });
      }
      if (kind === 'stock-movement') resetStockMovementProductState(true);
      open = false;
      onComplete?.();
    } catch (cause) {
      if (kind === 'transfer' && apiErrorCode(cause) === 'INSUFFICIENT_STOCK') {
        await refreshTransferLineBalances();
        error =
          'Stok başka bir işlemle değişti. Güncel kullanılabilir bakiye yenilendi; miktarı kontrol edin.';
      } else if (
        (kind === 'collection' || kind === 'payment') &&
        apiErrorCode(cause) === 'NEGATIVE_BALANCE_CONFIRMATION_REQUIRED'
      ) {
        negativeBalancePrompt = true;
        financeCreateAttempt = undefined;
        error = '';
      } else if (apiErrorCode(cause) === 'NEGATIVE_BALANCE_BLOCKED') {
        error = 'Hesap bakiyesi bu çıkış için yetersiz.';
      } else {
        error = errorMessage(cause, 'İşlem kaydedilemedi.');
      }
    } finally {
      submitting = false;
    }
  }

  function close() {
    if (!submitting) {
      error = '';
      negativeBalancePrompt = false;
      overrideReason = '';
      if (kind === 'stock-movement') resetStockMovementProductState(true);
      open = false;
    }
  }

  function handleWindowKeydown(event: KeyboardEvent) {
    if (!open || event.key !== 'Escape' || event.defaultPrevented || submitting) return;
    if (typeof document !== 'undefined' && document.querySelector('.entity-picker-dialog')) return;
    close();
  }

  $effect(() => {
    if (open) {
      void loadReferences();
    }
  });

  $effect(() => {
    if (open && (kind === 'collection' || kind === 'payment')) {
      if (paymentPrefill?.partyID) payment.partyID = paymentPrefill.partyID;
      if (paymentPrefill?.currency) payment.currency = paymentPrefill.currency;
      const prefillMethod = paymentPrefill?.method?.toUpperCase();
      if (prefillMethod === 'CASH' || prefillMethod === 'BANK') payment.method = prefillMethod;
      void loadOpenItems();
    }
  });

  $effect(() => {
    if (open && kind === 'manual') {
      if (paymentPrefill?.partyID) manual.partyID = paymentPrefill.partyID;
      if (paymentPrefill?.currency) manual.currency = paymentPrefill.currency;
      const prefillKind = paymentPrefill?.entryKind?.toUpperCase();
      if (prefillKind === 'DEBIT' || prefillKind === 'CREDIT') manual.entryKind = prefillKind;
    }
  });

  $effect(() => {
    if (!open || (kind !== 'collection' && kind !== 'payment')) return;
    payment.method;
    payment.currency;
    if (payment.accountID && !paymentAccounts.some((item) => item.id === payment.accountID)) {
      payment.accountID = '';
    }
  });

  $effect(() => {
    if (open && kind === 'stock-movement' && stock.productID) {
      void loadProductUnits(stock.productID);
      void loadProductVariants(stock.productID);
    }
  });

  $effect(() => {
    if (open && kind === 'stock-movement') {
      stock.warehouseID;
      stock.productID;
      stock.variantID;
      stock.direction;
      variants.length;
      variantLoading;
      void loadStockPosition();
      if (!variantLoading && stockVariantRequired && variants.length > 0) {
        void loadStockVariantPositions();
      }
    }
  });

  $effect(() => {
    if (open && kind === 'stock-movement' && productID && stock.productID !== productID) {
      stock.productID = productID;
      resetStockMovementProductState();
    }
  });
</script>

<svelte:window onkeydown={handleWindowKeydown} />

{#if open}
  <div
    class="modal-backdrop"
    role="presentation"
    onclick={(event) => {
      if (event.target === event.currentTarget) close();
    }}
  >
    <dialog class="modal" open aria-modal="true" aria-labelledby="operation-dialog-title">
      <header class="modal-header">
        <div>
          <h2 id="operation-dialog-title">{label}</h2>
        </div>
        <button class="close" type="button" aria-label="Pencereyi kapat" onclick={close}
          ><X size={18} /></button
        >
      </header>

      {#if error}<p class="notice error" role="alert">{error}</p>{/if}
      {#if loadingReferences}<p class="loading" role="status">
          <LoaderCircle class="spin" size={15} /> Referanslar yükleniyor…
        </p>{/if}

      <form
        class="form-grid"
        onsubmit={(event) => {
          event.preventDefault();
          void submit();
        }}
      >
        {#if kind === 'manual'}
          {@const selectedManualParty = parties.find((item) => item.id === manual.partyID)}
          <label
            ><span>Cari <b>*</b></span><EntityCombobox
              selected={selectedManualParty ? partyEntity(selectedManualParty) : undefined}
              results={parties.map(partyEntity)}
              onSearch={searchParties}
              title="Cari kartı seç"
              description="İşlemin bağlanacağı cari kartı kod veya ad ile arayın."
              triggerLabel="Cari"
              triggerPlaceholder="Cari kodu veya ad ara…"
              advancedSearch
              onSelect={(option) => {
                manual.partyID = option.id;
                if (!parties.some((item) => item.id === option.id)) {
                  parties = [
                    ...parties,
                    { id: option.id, code: option.subtitle ?? '', display_name: option.title }
                  ];
                }
              }}
            /></label
          >
          <p class="field-help wide">
            Bu işlem yalnız cari bakiyesini etkiler. Kasa veya banka hareketi oluşturmaz.
          </p>
          <label
            ><span>Hareket türü <b>*</b></span><select bind:value={manual.entryKind}
              ><option value="DEBIT">Cariyi borçlandır</option><option value="CREDIT"
                >Cariyi alacaklandır</option
              ></select
            ></label
          >
          <label
            ><span>Para birimi <b>*</b></span><CurrencySelect
              bind:value={manual.currency}
              required
              ariaLabel="Para birimi"
              onChange={() => applyExchangeRate(manual)}
            /></label
          >
          <label
            ><span>Tutar <b>*</b></span><input
              bind:value={manual.amount}
              inputmode="decimal"
              required
            /></label
          >
          {#if manual.currency.toUpperCase() !== 'TRY'}<label
              ><span>Kur <b>*</b></span><input
                bind:value={manual.exchangeRate}
                inputmode="decimal"
                required
              /></label
            >{/if}
          >
          <label
            ><span>Tarih <b>*</b></span><DateInput
              bind:value={manual.transactionDate}
              ariaLabel="Tarih"
              required
            /></label
          >
          <label
            ><span>Vade tarihi</span><DateInput
              bind:value={manual.dueDate}
              ariaLabel="Vade tarihi"
            /></label
          >
          <label><span>Belge / Referans no</span><input bind:value={manual.referenceNo} /></label>
          <label class="wide"
            ><span>Açıklama <b>*</b></span><textarea
              bind:value={manual.description}
              rows="3"
              required
            ></textarea></label
          >
        {:else if kind === 'collection' || kind === 'payment'}
          {@const selectedPaymentParty = parties.find((item) => item.id === payment.partyID)}
          <label
            ><span>Cari <b>*</b></span><EntityCombobox
              selected={selectedPaymentParty ? partyEntity(selectedPaymentParty) : undefined}
              results={parties.map(partyEntity)}
              onSearch={searchParties}
              title="Cari kartı seç"
              description="Tahsilat veya ödemenin bağlanacağı cari kartı arayın."
              triggerLabel="Cari"
              triggerPlaceholder="Cari kodu veya ad ara…"
              advancedSearch
              onSelect={(option) => {
                payment.partyID = option.id;
                if (!parties.some((item) => item.id === option.id)) {
                  parties = [
                    ...parties,
                    { id: option.id, code: option.subtitle ?? '', display_name: option.title }
                  ];
                }
                const selected = parties.find((item) => item.id === option.id);
                if (selected?.default_currency) {
                  payment.currency = selected.default_currency;
                  applyExchangeRate(payment);
                }
              }}
            /></label
          >
          <label
            ><span>Yöntem <b>*</b></span><select bind:value={payment.method}
              ><option value="CASH">Nakit</option><option value="BANK">Banka</option></select
            ></label
          >
          {#if ['CASH', 'BANK'].includes(payment.method)}<label
              ><span>{accountLabel(payment.method)} <b>*</b></span><select
                bind:value={payment.accountID}
                required
                ><option value="">Hesap seçin</option>{#each paymentAccounts as item}<option
                    value={item.id}>{item.code} · {item.name} ({item.currency})</option
                  >{/each}</select
              ></label
            >{/if}
          <label
            ><span>Para birimi <b>*</b></span><CurrencySelect
              bind:value={payment.currency}
              required
              ariaLabel="Para birimi"
              onChange={() => {
                applyExchangeRate(payment);
                allocationRequestKey = '';
                void loadOpenItems();
              }}
            /></label
          >
          <label
            ><span>Tutar <b>*</b></span><input
              bind:value={payment.amount}
              inputmode="decimal"
              required
            /></label
          >
          {#if payment.currency.toUpperCase() !== 'TRY'}<label
              ><span>Kur <b>*</b></span><input
                bind:value={payment.exchangeRate}
                inputmode="decimal"
                required
              /></label
            >{/if}
          <label><span>Makbuz no</span><input value="Otomatik oluşturulacak" disabled /></label>
          <label><span>Referans no</span><input bind:value={payment.referenceNo} /></label>
          <label
            ><span>Tarih <b>*</b></span><DateInput
              bind:value={payment.transactionDate}
              ariaLabel="Tarih"
              required
            /></label
          >
          <label class="wide"
            ><span>Açıklama</span><textarea bind:value={payment.description} rows="3"
            ></textarea></label
          >
          <NegativeBalanceReason bind:reason={overrideReason} active={negativeBalancePrompt} />
          {#if kind === 'collection' && ['CHECK', 'PROMISSORY_NOTE'].includes(payment.method)}
            <section class="wide instrument-section" aria-label="Çek veya senet bilgileri">
              <strong>{payment.method === 'CHECK' ? 'Çek bilgileri' : 'Senet bilgileri'}</strong>
              <div class="instrument-grid">
                <label
                  ><span>{payment.method === 'CHECK' ? 'Çek no' : 'Senet no'} <b>*</b></span><input
                    bind:value={payment.instrumentNo}
                    required
                  /></label
                >
                <label
                  ><span>Vade tarihi</span><DateInput
                    bind:value={payment.instrumentDueDate}
                    ariaLabel="Vade tarihi"
                  /></label
                >
                {#if payment.method === 'CHECK'}<label
                    ><span>Banka</span><input bind:value={payment.instrumentBankName} /></label
                  >{/if}
                <label
                  ><span>Keşideci</span><input bind:value={payment.instrumentDrawerName} /></label
                >
              </div>
            </section>
          {/if}
        {:else if kind === 'stock-movement'}
          <section class="wide movement-intro" aria-label="Stok hareketi açıklaması">
            <strong>Stok miktarını güncelle</strong>
            <span
              >Giriş stoğu artırır, çıkış stoğu azaltır. İşlenen hareket sonradan silinmez;
              gerekirse ters kayıt oluşturulur.</span
            >
          </section>
          {@const selectedWarehouse = warehouses.find((item) => item.id === stock.warehouseID)}
          <div class="picker-field">
            <span>Depo <b>*</b></span><EntityCombobox
              selected={selectedWarehouse ? warehouseEntity(selectedWarehouse) : undefined}
              results={stockWarehouseOptions().map(warehouseEntity)}
              title="Depo seç"
              description="Giriş için aktif depolardan, çıkış için yalnızca aktif normal depolardan seçim yapın."
              triggerLabel="Depo"
              triggerPlaceholder="Depo seçin"
              searchPlaceholder="Kod, ad veya adres ara"
              loading={warehouseLoading}
              disabled={warehouseLoading || warehouseState === 'error'}
              onSelect={(option) => {
                selectStockWarehouse(option.id);
              }}
            />
          </div>
          {#if warehouseState === 'error'}<div class="line-state error wide" role="alert">
              <span>{warehouseError || 'Depolar alınamadı.'}</span>
              <button
                class="line-add"
                type="button"
                onclick={() => void loadWarehouseReferences(true)}
                disabled={warehouseLoading}
              >
                Yeniden dene
              </button>
            </div>{:else if warehouseState === 'loading'}<p class="field-help wide" role="status">
              Depolar yükleniyor…
            </p>{:else if stockWarehouseNotice}<p class="line-state error wide" role="alert">
              {stockWarehouseNotice}
            </p>{:else if warehouseState === 'ready' && stockWarehouseOptions().length === 0}<p
              class="line-state error wide"
              role="alert"
            >
              Bu işlem için uygun aktif depo bulunamadı.
            </p>{/if}
          <p class="field-help wide">
            Aktif özel depolara yalnızca giriş yapılabilir. Stok çıkışında pasif ve özel depolar
            listelenmez.
          </p>
          {#if productID}
            <label
              ><span>Stok kartı <b>*</b></span><input
                value={selectedProductLabel || productID}
                disabled
                aria-label="Stok kartı"
              /></label
            >
          {:else}
            {@const selectedProduct = products.find((item) => item.id === stock.productID)}
            <label
              ><span>Stok kartı <b>*</b></span><EntityCombobox
                selected={selectedProduct ? productEntity(selectedProduct) : undefined}
                results={products.map((item) => productEntity(item))}
                onSearch={searchProducts}
                title="Stok kartı seç"
                description="Hareket oluşturacağınız stok kartını kod, ad veya barkodla arayın."
                triggerLabel="Stok kartı"
                triggerPlaceholder="Stok kartı seçin"
                onSelect={(option) => {
                  selectStockProduct(option);
                }}
              /></label
            >
          {/if}
          {#if variantLoading && stock.productID}<p class="field-help wide" role="status">
              Varyantlar yükleniyor…
            </p>{:else if variantState === 'error'}<div class="line-state error wide" role="alert">
              <span>{variantError || 'Varyant bilgisi alınamadı.'}</span>
              <button
                class="line-add"
                type="button"
                onclick={() => void loadProductVariants(stock.productID, true)}
                disabled={variantLoading}
              >
                Yeniden dene
              </button>
            </div>{:else if stockVariantRequired && variants.length > 0}<div class="wide">
              <VariantStockMovementTable
                bind:rows={stockVariantRows}
                direction={stock.direction as 'IN' | 'OUT'}
                unit={stock.unitCode}
                currency={stock.currency}
                loading={stockVariantPositionLoading}
                error={stockVariantPositionError}
                disabled={submitting}
                onValidationChange={(errors, valid) => {
                  stockVariantValidationErrors = errors;
                  stockVariantTableValid = valid;
                }}
              />
            </div>{:else if stockVariantRequired}<p class="line-state error wide" role="alert">
              Bu stok kartında kullanılabilir aktif varyant yok.
            </p>{/if}
          <label
            ><span>Hareket <b>*</b></span><select
              value={stock.direction}
              onchange={(event) => setStockDirection(event.currentTarget.value)}
              ><option value="IN">Giriş</option><option value="OUT">Çıkış</option></select
            ></label
          >
          {#if !stockVariantRequired}<label
              ><span>Miktar <b>*</b></span><input
                bind:value={stock.quantity}
                inputmode="decimal"
                placeholder="Örn. 10"
                required
                disabled={stockMetadataBlocked() || submitting}
              /></label
            >{/if}
          {#if stock.direction === 'OUT' && stock.productID && !stockVariantRequired}
            {#if stockPositionLoading}<p class="field-help wide" role="status">
                Güncel kullanılabilir bakiye kontrol ediliyor…
              </p>
            {:else if stockPositionError}<p class="line-state error wide" role="alert">
                {stockPositionError}
              </p>
            {:else if stockAvailableQuantity.trim()}<p class="field-help wide">
                Kullanılabilir bakiye: <strong>{formatQuantity(stockAvailableQuantity)}</strong>
              </p>{/if}
          {/if}
          <label
            ><span>Birim <b>*</b></span>{#if !stock.productID}<input
                value="Stok kartı seçin"
                disabled
              />{:else if unitState === 'loading'}<input
                value="Birimler yükleniyor…"
                disabled
                aria-busy="true"
              />{:else if unitState === 'ready' && units.length > 0}<select
                bind:value={stock.unitCode}
                required
                aria-busy={unitLoading}
              >
                {#each units as unit}<option value={unit.code}
                    >{unit.code}{unit.name ? ` · ${unit.name}` : ''}</option
                  >{/each}
              </select>{:else}<div class="unit-error">
                <span class="line-state error" role="alert"
                  >{unitError || 'Birim bilgisi alınamadı.'}</span
                >
                <button
                  class="line-add"
                  type="button"
                  onclick={() => void loadProductUnits(stock.productID, true)}
                  disabled={unitLoading}
                >
                  Yeniden dene
                </button>
              </div>{/if}</label
          >
          {#if stock.direction === 'IN'}{#if !stockVariantRequired}<label
                ><span>Birim maliyet <small>(isteğe bağlı)</small></span><input
                  bind:value={stock.unitCost}
                  inputmode="decimal"
                  placeholder="Otomatik atanmaz"
                  aria-describedby="stock-cost-help"
                /></label
              >{/if}<label
              ><span>Para birimi</span><select
                bind:value={stock.currency}
                disabled={!stockVariantRequired && !stock.unitCost.trim()}
              >
                <option value="TRY">TRY · Türk lirası</option>
                <option value="EUR">EUR · Euro</option>
                <option value="USD">USD · ABD doları</option>
              </select></label
            >
            <p id="stock-cost-help" class="field-help wide">
              Alış fiyatı stok maliyetine otomatik aktarılmaz. Gerekiyorsa maliyet temelini burada
              açıkça girin.
            </p>{/if}
          <label
            ><span>Neden <b>*</b></span><select bind:value={stock.reasonCode} required
              ><option value="">Neden seçin</option>{#each stockReasons() as reason}<option
                  value={reason.value}>{reason.label}</option
                >{/each}</select
            ></label
          >
          {#if ['OTHER', 'DAMAGE', 'WASTE'].includes(stock.reasonCode)}<label class="wide"
              ><span>Açıklama <b>*</b></span><textarea
                bind:value={stock.reasonDescription}
                rows="3"
                required
              ></textarea></label
            >{/if}
        {:else if kind === 'warehouse'}
          <label><span>Depo kodu</span><input bind:value={warehouse.code} maxlength="40" /></label>
          <label><span>Depo adı <b>*</b></span><input bind:value={warehouse.name} required /></label
          >
          <label
            ><span>Tür <b>*</b></span><select bind:value={warehouse.type}
              ><option value="STANDARD">Normal</option><option value="QUARANTINE">Karantina</option
              ><option value="RETURN">İade</option></select
            ></label
          >
          <label><span>Adres</span><input bind:value={warehouse.address} /></label>
        {:else if kind === 'transfer'}
          <label><span>Transfer no</span><input value="Otomatik oluşturulacak" disabled /></label>
          <label
            ><span>Transfer tipi <b>*</b></span><select bind:value={transfer.transferType}
              ><option value="QUICK">Hızlı Transfer</option><option value="WORKFLOW"
                >Sevk / Teslim</option
              ></select
            ></label
          >
          <label
            ><span>Çıkış deposu <b>*</b></span><select
              value={transfer.sourceWarehouseID}
              required
              disabled={warehouseLoading || warehouseState !== 'ready'}
              onchange={(event) => selectTransferSource(event.currentTarget.value)}
              ><option value="">Depo seçin</option>{#each activeStandardWarehouses() as item}<option
                  value={item.id}>{item.code} · {item.name}</option
                >{/each}</select
            ></label
          >
          <label
            ><span>Varış deposu <b>*</b></span><select
              value={transfer.destinationWarehouseID}
              required
              disabled={warehouseLoading || warehouseState !== 'ready'}
              onchange={(event) => selectTransferDestination(event.currentTarget.value)}
              ><option value="">Depo seçin</option
              >{#each transferDestinationOptions() as item}<option value={item.id}
                  >{transferWarehouseLabel(item)}</option
                >{/each}</select
            ></label
          >
          {#if warehouseState === 'error'}<div class="line-state error wide" role="alert">
              <span>{warehouseError || 'Depolar alınamadı.'}</span>
              <button
                class="line-add"
                type="button"
                onclick={() => void loadWarehouseReferences(true)}
                disabled={warehouseLoading}
              >
                Yeniden dene
              </button>
            </div>{:else if warehouseState === 'loading'}<p class="field-help wide" role="status">
              Depolar yükleniyor…
            </p>{:else if warehouseState === 'ready' && activeStandardWarehouses().length === 0}<p
              class="line-state error wide"
              role="alert"
            >
              Çıkış için aktif normal depo bulunamadı.
            </p>{/if}
          <div class="wide transfer-lines">
            <div class="line-heading">
              <div>
                <strong>Stoklar</strong>
                <span
                  >Stok seçimi çıkış deposundaki bakiye ve varyant bilgisine göre doğrulanır.</span
                >
              </div>
              <button class="line-add" type="button" onclick={addTransferLine}
                ><Plus size={14} strokeWidth={2.25} />Satır ekle</button
              >
            </div>
            {#each transfer.lines as line, index}
              {@const selectedProduct = transferLineProduct(line)}
              <div class="transfer-line" class:has-variant={line.variants.length > 0}>
                <label class="transfer-product-field">
                  <span>Stok <b>*</b></span>
                  <EntityCombobox
                    selected={selectedProduct
                      ? line.availableQuantity
                        ? productEntity(selectedProduct, line.availableQuantity)
                        : transferProductEntity(selectedProduct)
                      : undefined}
                    results={transferProductOptions().map((item) => transferProductEntity(item))}
                    onSearch={searchTransferProducts}
                    title="Transfer stoğu seç"
                    description="Yalnızca çıkış deposunda kullanılabilir fiziksel stokları seçin."
                    triggerLabel={`Stok ${index + 1}`}
                    triggerPlaceholder="Stok kartı seçin"
                    searchPlaceholder="Stok kodu, adı veya barkod ara"
                    emptyText="Bu depoda eşleşen kullanılabilir stok bulunamadı."
                    initialEmptyText="Stok kodu, adı veya barkod yazın."
                    loading={loadingReferences || line.loading}
                    onSelect={(option) => selectTransferProduct(line, option)}
                  />
                </label>
                <label>
                  <span>Miktar <b>*</b></span>
                  <input
                    bind:value={line.quantity}
                    inputmode="decimal"
                    placeholder="Miktar"
                    required
                    disabled={Boolean(line.productID) &&
                      (line.loading || line.metadataState !== 'ready')}
                    aria-label={`Miktar ${index + 1}`}
                  />
                </label>
                {#if line.variantRequired}<label>
                    <span>Varyant <b>*</b></span><select
                      value={line.variantID}
                      required
                      disabled={line.loading || line.metadataState !== 'ready'}
                      onchange={(event) => {
                        line.variantID = event.currentTarget.value;
                        void loadTransferLinePosition(line);
                      }}
                    >
                      <option value="">Varyant seçin</option>
                      {#each line.variants as variant}<option value={variant.id}
                          >{variantOptionLabel(variant)}</option
                        >{/each}
                    </select>
                  </label>{/if}
                {#if line.productID && line.loading}<p class="line-state" role="status">
                    <LoaderCircle class="spin" size={13} /> Stok bakiyesi yükleniyor…
                  </p>
                {:else if line.metadataState === 'error'}<div class="line-state error" role="alert">
                    <span>{line.error || 'Varyant bilgisi alınamadı.'}</span>
                    <button
                      class="line-add"
                      type="button"
                      onclick={() => void loadTransferLineData(line)}
                      disabled={line.loading}
                    >
                      Yeniden dene
                    </button>
                  </div>
                {:else if line.error}<p class="line-state error" role="alert">{line.error}</p>
                {:else if line.productID && line.variantRequired && line.variants.length === 0}<p
                    class="line-state error"
                    role="alert"
                  >
                    Bu stok kartında kullanılabilir aktif varyant yok.
                  </p>{:else if line.productID && line.variantRequired && !line.variantID}<p
                    class="line-state error"
                    role="alert"
                  >
                    Devam etmek için varyant seçin.
                  </p>
                {:else if line.productID && compareDecimal(line.availableQuantity || '0', '0') <= 0}
                  <p class="line-state error" role="alert">
                    Bu stok çıkış deposunda kullanılabilir değil.
                  </p>
                {:else if line.productID}<p class="line-state">
                    Kullanılabilir: <strong>{formatQuantity(line.availableQuantity || '0')}</strong>
                    {line.stockUnit || selectedProduct?.stock_unit || ''}
                  </p>
                {/if}
                <button
                  class="line-remove"
                  type="button"
                  aria-label="Satırı kaldır"
                  onclick={() => removeTransferLine(index)}
                  ><X size={15} strokeWidth={2.25} /></button
                >
              </div>
            {/each}
          </div>
        {:else if kind === 'count'}
          <label
            ><span>Depo <b>*</b></span><select
              bind:value={count.warehouseID}
              required
              disabled={warehouseLoading || warehouseState !== 'ready'}
              ><option value="">Depo seçin</option>{#each activeStandardWarehouses() as item}<option
                  value={item.id}>{item.code} · {item.name}</option
                >{/each}</select
            ></label
          >
          {#if warehouseState === 'error'}<div class="line-state error wide" role="alert">
              <span>{warehouseError || 'Depolar alınamadı.'}</span>
              <button
                class="line-add"
                type="button"
                onclick={() => void loadWarehouseReferences(true)}
                disabled={warehouseLoading}
              >
                Yeniden dene
              </button>
            </div>{:else if warehouseState === 'loading'}<p class="field-help wide" role="status">
              Depolar yükleniyor…
            </p>{/if}
        {/if}

        {#if (kind === 'collection' || kind === 'payment') && payment.partyID}
          <section class="wide allocation-section" aria-labelledby="open-invoices-title">
            <div class="section-heading">
              <div>
                <strong id="open-invoices-title">Açık Faturalar</strong><span
                  >Dağıtım kaydetmeden önce gözden geçirilir.</span
                >
              </div>
              <button
                class="line-add"
                type="button"
                onclick={autoAllocate}
                disabled={allocationLoading}>En eski borçlardan otomatik dağıt</button
              >
            </div>
            {#if allocationNotice}<p
                class="field-help wide"
                class:success={autoAllocateRequested}
                role="status"
              >
                {allocationNotice}
              </p>{/if}
            {#if allocationLoading}<p class="loading" role="status">Açık faturalar yükleniyor…</p>
            {:else if allocationRows.length === 0}<p class="empty-inline">
                Açık fatura bulunamadı. Tutar cari avansı olarak kalır.
                {#if payment.partyID}
                  <a
                    href={`/${kind === 'payment' ? 'alis' : 'satis'}/irsaliyeler?party_id=${encodeURIComponent(payment.partyID)}`}
                    target="_blank"
                    rel="noopener">Faturalanmamış irsaliyeleri gör</a
                  >
                {/if}
              </p>
            {:else}<div class="allocation-table">
                <div class="allocation-row allocation-header">
                  <span>Belge</span><span>Vade</span><span>Açık Tutar</span><span>Uygulanacak</span>
                </div>
                {#each allocationRows as row}
                  <div class="allocation-row">
                    {#if row.document_id}
                      <a
                        class="doc-link"
                        href={`/${kind === 'payment' ? 'alis' : 'satis'}/faturalar/${row.document_id}`}
                        target="_blank"
                        rel="noopener"
                        title="Faturayı yeni sekmede aç">{row.document_no ?? 'Fatura'}</a
                      >
                    {:else}
                      <span>{row.document_no ?? 'Fatura'}</span>
                    {/if}
                    <span>{row.due_date ? row.due_date.slice(0, 10) : '—'}</span>
                    <span>{formatMoney(row.open_amount, payment.currency)}</span>
                    <input
                      bind:value={row.applied}
                      oninput={onAllocationInput}
                      inputmode="decimal"
                      aria-label={`${row.document_no ?? 'Fatura'} uygulanacak tutar`}
                    />
                  </div>
                {/each}
              </div>{/if}
            <div class="allocation-summary">
              <span
                >Tahsilat / Ödeme: <strong
                  >{formatMoney(payment.amount || '0', payment.currency)}</strong
                ></span
              ><span
                >Faturalara dağıtılan: <strong
                  >{formatMoney(allocationTotal(), payment.currency)}</strong
                ></span
              ><span
                >Dağıtılmamış: <strong>{formatMoney(unappliedAmount(), payment.currency)}</strong
                ></span
              >
            </div>
          </section>
        {/if}

        <div class="form-actions">
          <button class="button secondary" type="button" onclick={close} disabled={submitting}
            >Vazgeç</button
          >
          <button
            class="button"
            type="submit"
            disabled={submitting ||
              loadingReferences ||
              warehouseReferencesBlocked() ||
              (kind === 'transfer' &&
                (transferLinesLoading() || Boolean(transferMetadataError()))) ||
              stockMetadataBlocked() ||
              stockVariantSubmissionBlocked()}
          >
            {#if submitting}<LoaderCircle class="spin" size={15} /> Kaydediliyor…{:else}<Save
                size={15}
              /> Kaydet{/if}
          </button>
        </div>
      </form>
    </dialog>
  </div>
{/if}

<style>
  .modal-backdrop {
    position: fixed;
    z-index: 100;
    inset: 0;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 20px;
    background: rgb(10 30 27 / 42%);
  }
  .modal {
    position: relative;
    inset: auto;
    margin: 0;
    width: min(720px, 100%);
    max-height: min(760px, calc(100vh - 40px));
    overflow: auto;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-panel);
    background: var(--surface);
    box-shadow: 0 24px 70px rgb(10 30 27 / 24%);
    padding: 20px;
  }
  .modal-header {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 16px;
    margin-bottom: 16px;
  }
  h2 {
    margin: 3px 0 0;
    font-size: 18px;
  }
  .close {
    display: grid;
    place-items: center;
    width: 32px;
    height: 32px;
    border: 0;
    border-radius: 50%;
    background: transparent;
    color: var(--text-muted);
  }
  .close:hover {
    background: var(--surface-muted);
    color: var(--text);
  }
  .form-grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 12px;
  }
  label {
    display: grid;
    gap: 5px;
    color: var(--text-subtle);
    font-size: 11px;
    font-weight: 650;
  }
  .picker-field {
    display: grid;
    gap: 5px;
    color: var(--text-subtle);
    font-size: 11px;
    font-weight: 650;
  }
  .picker-field > span b {
    color: var(--danger);
  }
  label > span b {
    color: var(--danger);
  }
  label > span small,
  .field-help {
    color: var(--text-muted);
    font-size: 10px;
    font-weight: 500;
  }
  .field-help {
    margin: -3px 0 0;
    line-height: 1.45;
  }
  input,
  select,
  textarea {
    width: 100%;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    background: var(--surface);
    color: var(--text);
    font: inherit;
    padding: 8px 9px;
  }
  input,
  select {
    height: var(--control-height);
  }
  textarea {
    resize: vertical;
  }
  .wide {
    grid-column: 1 / -1;
  }
  .form-actions {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
    grid-column: 1 / -1;
    margin-top: 5px;
  }
  .allocation-section,
  .transfer-lines,
  .instrument-section,
  .movement-intro {
    display: grid;
    gap: 8px;
    padding: 10px;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    background: var(--surface-muted);
  }
  .movement-intro strong {
    color: var(--text);
    font-size: 12px;
  }
  .movement-intro span {
    color: var(--text-muted);
    font-size: 11px;
    font-weight: 500;
  }
  .section-heading,
  .line-heading {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
  }
  .section-heading div {
    display: grid;
    gap: 2px;
  }
  .instrument-section {
    display: grid;
    gap: 9px;
  }
  .instrument-section > strong {
    color: var(--text);
    font-size: 12px;
  }
  .instrument-grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 10px;
  }
  .section-heading span,
  .empty-inline {
    color: var(--text-muted);
    font-size: 11px;
  }
  .line-heading > div {
    display: grid;
    gap: 2px;
  }
  .line-heading > div > strong {
    color: var(--text);
    font-size: 12px;
  }
  .line-heading > div > span {
    color: var(--text-muted);
    font-size: 10px;
    font-weight: 500;
  }
  .line-add,
  .line-remove {
    font: inherit;
    font-size: 11px;
    cursor: pointer;
  }
  .line-add {
    display: inline-flex;
    flex: 0 0 auto;
    align-items: center;
    justify-content: center;
    gap: 5px;
    min-height: 28px;
    padding: 5px 9px;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    background: var(--surface);
    color: var(--primary);
    font-weight: 650;
    white-space: nowrap;
  }
  .line-add:hover {
    background: var(--surface);
    border-color: var(--primary);
  }
  .unit-error {
    display: flex;
    align-items: center;
    gap: 7px;
  }
  .unit-error .line-state {
    flex: 1;
  }
  .line-remove {
    display: inline-grid;
    place-items: center;
    grid-column: -1;
    grid-row: 1;
    align-self: end;
    justify-self: end;
    width: 28px;
    height: var(--control-height);
    padding: 0;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    background: var(--surface);
    color: var(--danger);
    line-height: 1;
  }
  .line-remove:hover {
    border-color: var(--danger);
    background: var(--surface);
  }
  .transfer-line,
  .allocation-row {
    display: grid;
    grid-template-columns: minmax(0, 1fr) 130px 28px;
    align-items: center;
    gap: 7px;
  }
  .transfer-line {
    grid-template-columns: minmax(220px, 1.6fr) minmax(110px, 130px) 28px;
    align-items: end;
  }
  .transfer-line.has-variant {
    grid-template-columns: minmax(220px, 1.6fr) minmax(110px, 130px) minmax(140px, 1fr) 28px;
  }
  .transfer-line > label {
    min-width: 0;
  }
  .transfer-line .line-state {
    grid-column: 1 / -1;
    display: flex;
    align-items: center;
    gap: 4px;
    margin: -2px 0 0;
    color: var(--text-muted);
    font-size: 10px;
    font-weight: 500;
  }
  .transfer-line .line-state strong {
    color: var(--text);
  }
  .transfer-line .line-state.error {
    color: var(--danger);
  }
  #open-invoices-title {
    color: var(--text);
    font-size: 12px;
    font-weight: 700;
  }
  .allocation-row {
    grid-template-columns: minmax(0, 1fr) 100px 120px 120px;
    font-size: 11px;
  }
  .allocation-row > span {
    color: var(--text);
  }
  .allocation-header {
    color: var(--text-muted);
    font-weight: 700;
  }
  .allocation-header > span {
    color: var(--text-muted);
  }
  .doc-link {
    color: var(--primary);
    font-weight: 600;
    text-decoration: none;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .doc-link:hover {
    text-decoration: underline;
  }
  .allocation-summary {
    display: flex;
    flex-wrap: wrap;
    justify-content: flex-end;
    gap: 14px;
    color: var(--text-muted);
    font-size: 11px;
  }
  .allocation-summary strong {
    color: var(--text);
  }
  .button {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 6px;
    min-height: var(--control-height);
    border: 1px solid var(--primary);
    border-radius: var(--radius-control);
    background: var(--primary);
    color: var(--primary-foreground);
    padding: 0 13px;
    font: inherit;
    font-size: 12px;
    font-weight: 650;
  }
  .button.secondary {
    border-color: var(--border-strong);
    background: var(--surface);
    color: var(--text);
  }
  .button:disabled {
    cursor: not-allowed;
    opacity: 0.55;
  }
  .notice.error {
    margin: 0 0 12px;
  }
  .loading {
    display: flex;
    align-items: center;
    gap: 6px;
    color: var(--text-muted);
    font-size: 11px;
  }
  :global(.spin) {
    animation: spin 1s linear infinite;
  }
  @keyframes spin {
    to {
      transform: rotate(360deg);
    }
  }
  @media (max-width: 640px) {
    .form-grid {
      grid-template-columns: 1fr;
    }
    .wide {
      grid-column: auto;
    }
    .form-actions {
      grid-column: auto;
    }
    .transfer-line {
      grid-template-columns: minmax(0, 1fr) 28px;
    }
    .transfer-line.has-variant {
      grid-template-columns: minmax(0, 1fr) 28px;
    }
    .transfer-line > label {
      grid-column: 1;
    }
    .transfer-line > .line-remove {
      grid-column: 2;
      grid-row: 1;
    }
  }
</style>
