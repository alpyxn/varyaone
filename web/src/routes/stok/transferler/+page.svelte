<script lang="ts">
  import { onMount } from 'svelte';
  import { LoaderCircle, Plus, ScanLine, X } from '@lucide/svelte';
  import { api, type Session } from '$lib/api';
  import { Button } from '$lib/components/ui/button';
  import { formatQuantity } from '$lib/design/formatters';
  import { type EntityOption } from '$lib/components/varya/entity-picker-dialog';
  import { EntityCombobox } from '$lib/components/varya/entity-combobox';
  import OperationListPage, {
    type OperationColumn,
    type OperationFilter
  } from '$lib/features/operations/OperationListPage.svelte';
  import TransferProductMatrix, {
    type TransferMatrixVariant
  } from '$lib/features/operations/TransferProductMatrix.svelte';
  import {
    buildTransferApiLines,
    validateTransferQuantities,
    type TransferEntryLine,
    type TransferQuantityLine
  } from '$lib/features/operations/transfer-validation';
  import { listWarehouses } from '$lib/features/warehouses/api';
  import {
    isActiveDestinationWarehouse,
    isActiveStandardWarehouse,
    type Warehouse
  } from '$lib/features/warehouses/types';

  type ProductOption = {
    id: string;
    code?: string;
    sku?: string;
    name: string;
    kind?: string;
    stock_unit?: string;
    available_quantity?: string;
    variants_enabled?: boolean;
    barcode_summary?: string;
  };
  type VariantOption = {
    id: string;
    variant_code?: string;
    variant_name?: string;
    sku?: string;
    attributes?: Record<string, unknown>;
    available_quantity?: string;
    physical_quantity?: string;
    reserved_quantity?: string;
    stock_positions?: Array<{
      warehouse_id?: string;
      available_quantity?: string;
      physical_quantity?: string;
      reserved_quantity?: string;
    }>;
    is_active?: boolean;
    barcodes?: Array<{ barcode?: string }>;
  };
  type StockPickerOption = EntityOption & { product: ProductOption };
  type WarehousePickerOption = EntityOption & { warehouse: Warehouse };
  type TransferDraftLine = TransferEntryLine & {
    variants: TransferMatrixVariant[];
    loading: boolean;
    error: string;
    stockUnit: string;
    variantQuantities: Record<string, string>;
    product?: ProductOption;
    selectedProduct?: StockPickerOption;
    pickerOpen: boolean;
    requestKey: number;
  };

  const columns: OperationColumn[] = [
    { id: 'transfer_no', label: 'Transfer No', sortable: true },
    { id: 'transfer_type', label: 'Transfer Tipi', sortable: true },
    { id: 'created_at', label: 'Oluşturma Tarihi', sortable: true },
    { id: 'arrival_at', label: 'Varış Tarihi', sortable: true },
    { id: 'source_warehouse_name', label: 'Çıkış Deposu' },
    { id: 'destination_warehouse_name', label: 'Varış Deposu' },
    { id: 'state', label: 'Sevk Durumu' }
  ];
  const filters: OperationFilter[] = [
    { field: 'from', label: 'Başlangıç', kind: 'date' },
    { field: 'to', label: 'Bitiş', kind: 'date' },
    {
      field: 'transfer_type',
      label: 'Transfer Tipi',
      kind: 'select',
      options: [
        { value: 'QUICK', label: 'Hızlı Transfer' },
        { value: 'WORKFLOW', label: 'Sevk / Teslim' }
      ]
    },
    {
      field: 'state',
      label: 'Sevk Durumu',
      kind: 'select',
      visibleWhen: { field: 'transfer_type', value: 'WORKFLOW' },
      options: [
        { value: 'IN_TRANSIT,PARTIALLY_RECEIVED', label: 'Sevk sırasında' },
        { value: 'RECEIVED', label: 'Teslim alındı' },
        { value: 'CANCELLED', label: 'Sevk iptal oldu' }
      ]
    },
    {
      field: 'warehouse_id',
      label: 'Depo',
      kind: 'entity',
      entity: {
        title: 'Transfer deposu seç',
        description: '',
        triggerPlaceholder: 'Depo seçin',
        searchPlaceholder: 'Depo kodu, adı veya adresi ara',
        search: async (query, signal) => {
          const result = await listWarehouses({ signal });
          const needle = query.trim().toLocaleLowerCase('tr-TR');
          return (result.items ?? [])
            .filter((warehouse) => {
              if (!needle) return true;
              return [warehouse.code, warehouse.name, warehouse.address]
                .filter(Boolean)
                .join(' ')
                .toLocaleLowerCase('tr-TR')
                .includes(needle);
            })
            .map((warehouse) => ({
              id: warehouse.id,
              title: warehouse.name,
              subtitle: warehouse.code || 'Kod tanımsız',
              meta: warehouse.address
            }));
        }
      }
    }
  ];

  let session = $state<Session | null>(null);
  let warehouses = $state<Warehouse[]>([]);
  let products = $state<ProductOption[]>([]);
  let sourceWarehouseID = $state('');
  let destinationWarehouseID = $state('');
  let sourceWarehousePickerOpen = $state(false);
  let destinationWarehousePickerOpen = $state(false);
  let selectedSourceWarehouse = $state<WarehousePickerOption | null>(null);
  let selectedDestinationWarehouse = $state<WarehousePickerOption | null>(null);
  let transferType = $state<'QUICK' | 'WORKFLOW'>('QUICK');
  let lines = $state<TransferDraftLine[]>([]);
  let dialogOpen = $state(false);
  let loadingReferences = $state(true);
  let loadingProducts = $state(false);
  let submitting = $state(false);
  let formError = $state('');
  let successMessage = $state('');
  let listVersion = $state(0);
  let requestSequence = 0;
  let productsRequestKey = 0;
  let transferScan = $state('');
  let dialogElement = $state<HTMLDivElement>();
  let restoreFocusElement = $state<HTMLElement | null>(null);

  const stockPickerOptions = $derived(products.map(stockPickerOption));

  function newLine(): TransferDraftLine {
    return {
      productID: '',
      variantRequired: false,
      quantity: '',
      variantQuantities: {},
      variants: [],
      loading: false,
      error: '',
      stockUnit: 'ADET',
      pickerOpen: false,
      requestKey: nextRequestKey()
    };
  }
  function resetForm() {
    sourceWarehouseID = '';
    destinationWarehouseID = '';
    sourceWarehousePickerOpen = false;
    destinationWarehousePickerOpen = false;
    selectedSourceWarehouse = null;
    selectedDestinationWarehouse = null;
    transferType = 'QUICK';
    products = [];
    lines = [newLine()];
    formError = '';
  }
  function openCreateDialog() {
    restoreFocusElement =
      document.activeElement instanceof HTMLElement ? document.activeElement : null;
    resetForm();
    dialogOpen = true;
    const standard = warehouses.filter(isActiveStandardWarehouse);
    if (standard.length === 1) selectSourceOption(warehousePickerOption(standard[0]));
    setTimeout(() => dialogElement?.focus(), 0);
  }
  function closeCreateDialog() {
    if (submitting) return;
    dialogOpen = false;
    formError = '';
    setTimeout(() => {
      if (restoreFocusElement && document.contains(restoreFocusElement))
        restoreFocusElement.focus();
      restoreFocusElement = null;
    }, 0);
  }
  function handleDialogKeydown(event: KeyboardEvent) {
    if (!dialogOpen) return;
    const nestedPicker = document.querySelector('.entity-picker-dialog');
    if (nestedPicker?.contains(event.target as Node)) return;
    if (event.key === 'Escape') {
      event.preventDefault();
      closeCreateDialog();
      return;
    }
    if (event.key !== 'Tab' || !dialogElement) return;
    const focusable = Array.from(
      dialogElement.querySelectorAll<HTMLElement>(
        'button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [href], [tabindex]:not([tabindex="-1"])'
      )
    );
    if (focusable.length === 0) {
      event.preventDefault();
      dialogElement.focus();
      return;
    }
    const first = focusable[0];
    const last = focusable[focusable.length - 1];
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  }
  function sourceOptions() {
    return warehouses.filter(isActiveStandardWarehouse);
  }
  function destinationOptions() {
    return warehouses.filter(
      (warehouse) => isActiveDestinationWarehouse(warehouse) && warehouse.id !== sourceWarehouseID
    );
  }
  function warehousePickerOption(warehouse: Warehouse): WarehousePickerOption {
    return {
      id: warehouse.id,
      title: warehouse.name,
      subtitle: warehouse.code || 'Kod tanımsız',
      meta: warehouse.address || undefined,
      warehouse
    };
  }
  function warehouseOptions(options: Warehouse[]) {
    return options.map(warehousePickerOption);
  }
  function searchWarehouseOptions(options: WarehousePickerOption[], query: string) {
    const needle = query.trim().toLocaleLowerCase('tr-TR');
    if (!needle) return options;
    return options.filter((option) =>
      [option.title, option.subtitle, option.meta]
        .filter(Boolean)
        .join(' ')
        .toLocaleLowerCase('tr-TR')
        .includes(needle)
    );
  }
  function selectSourceOption(option: WarehousePickerOption) {
    selectedSourceWarehouse = option;
    sourceWarehousePickerOpen = false;
    void selectSource(option.id);
  }
  function selectDestinationOption(option: WarehousePickerOption) {
    selectedDestinationWarehouse = option;
    destinationWarehouseID = option.id;
    destinationWarehousePickerOpen = false;
  }
  function nextRequestKey() {
    requestSequence += 1;
    return requestSequence;
  }
  function resetLine(line: TransferDraftLine, product?: ProductOption) {
    line.productID = product?.id ?? '';
    line.product = product;
    line.selectedProduct = product ? stockPickerOption(product) : undefined;
    line.pickerOpen = false;
    line.variantRequired = product?.variants_enabled === true;
    line.quantity = '';
    line.variantQuantities = {};
    line.variants = [];
    line.loading = false;
    line.error = '';
    line.stockUnit = product?.stock_unit || 'ADET';
    line.requestKey = nextRequestKey();
  }
  async function loadProducts(warehouseID: string) {
    const requestKey = ++productsRequestKey;
    loadingProducts = true;
    products = [];
    lines = lines.map((line) => {
      resetLine(line);
      return line;
    });
    if (!warehouseID) {
      loadingProducts = false;
      return;
    }
    try {
      const result = await api<{ items?: ProductOption[] }>(
        `/products?warehouse_id=${encodeURIComponent(warehouseID)}&limit=100`
      );
      if (requestKey === productsRequestKey) {
        products = (result.items ?? []).filter(isPhysical);
      }
    } catch (cause) {
      formError = errorMessage(cause, 'Çıkış deposundaki stok kartları alınamadı.');
    } finally {
      loadingProducts = false;
    }
  }
  function stockPickerOption(product: ProductOption): StockPickerOption {
    const meta = [
      product.barcode_summary ? `Barkod: ${product.barcode_summary}` : '',
      product.available_quantity !== undefined
        ? `Kullanılabilir: ${formatQuantity(product.available_quantity)} ${product.stock_unit || ''}`.trim()
        : ''
    ].filter(Boolean);
    return {
      id: product.id,
      title: product.name,
      subtitle: product.code || product.sku || 'Kod tanımsız',
      meta,
      product
    };
  }
  async function searchTransferProducts(
    query: string,
    signal: AbortSignal
  ): Promise<StockPickerOption[]> {
    const params = new URLSearchParams({ limit: '30' });
    if (query.trim()) params.set('q', query.trim());
    if (sourceWarehouseID) params.set('warehouse_id', sourceWarehouseID);
    const result = await api<{ items?: ProductOption[] }>(`/products?${params}`, { signal });
    return (result.items ?? []).filter(isPhysical).map(stockPickerOption);
  }
  function isPhysical(product: ProductOption) {
    return String(product.kind ?? 'PHYSICAL').toUpperCase() !== 'SERVICE';
  }
  async function selectSource(value: string) {
    sourceWarehouseID = value;
    selectedSourceWarehouse =
      warehouseOptions(sourceOptions()).find((option) => option.id === value) ?? null;
    if (destinationWarehouseID === value) {
      destinationWarehouseID = '';
      selectedDestinationWarehouse = null;
    }
    await loadProducts(value);
  }
  async function selectProduct(line: TransferDraftLine, productID: string) {
    const product = products.find((item) => item.id === productID);
    resetLine(line, product);
    if (!product || product.variants_enabled !== true) return;
    const requestKey = nextRequestKey();
    line.requestKey = requestKey;
    line.loading = true;
    try {
      const result = await api<{ items?: VariantOption[] }>(
        `/products/${encodeURIComponent(product.id)}/variants`
      );
      if (line.requestKey !== requestKey) return;
      line.variants = (result.items ?? [])
        .filter((variant) => variant.is_active !== false)
        .map((variant) => {
          const position = variant.stock_positions?.find(
            (item) => item.warehouse_id === sourceWarehouseID
          );
          return {
            id: variant.id,
            code: variant.variant_code,
            name: variant.variant_name,
            sku: variant.sku,
            attributes: variant.attributes,
            barcodes: variant.barcodes,
            availableQuantity: position?.available_quantity ?? variant.available_quantity ?? '0',
            physicalQuantity: position?.physical_quantity ?? variant.physical_quantity ?? '0',
            reservedQuantity: position?.reserved_quantity ?? variant.reserved_quantity ?? '0'
          };
        });
      if (line.variants.length === 0)
        line.error = 'Bu stok kartında kullanılabilir aktif varyant yok.';
    } catch (cause) {
      if (line.requestKey === requestKey)
        line.error = errorMessage(cause, 'Varyant bilgisi alınamadı.');
    } finally {
      if (line.requestKey === requestKey) line.loading = false;
    }
  }
  function selectProductOption(line: TransferDraftLine, option: StockPickerOption) {
    if (!products.some((product) => product.id === option.product.id)) {
      products = [...products, option.product];
    }
    line.selectedProduct = option;
    line.pickerOpen = false;
    void selectProduct(line, option.product.id);
  }
  function addLine() {
    lines = [...lines, newLine()];
  }
  function decimalSum(left: string, right: string) {
    const a = Number((left || '0').replace(',', '.'));
    const b = Number((right || '0').replace(',', '.'));
    return String((Number.isFinite(a) ? a : 0) + (Number.isFinite(b) ? b : 0));
  }
  async function scanTransferBarcode(event: SubmitEvent) {
    event.preventDefault();
    const barcode = transferScan.trim();
    if (!barcode) {
      formError = 'Barkod girin veya okutun.';
      return;
    }
    for (const line of lines) {
      const variant = line.variants.find((item) =>
        item.barcodes?.some((candidate) => candidate.barcode?.trim() === barcode)
      );
      if (variant) {
        line.variantQuantities = {
          ...line.variantQuantities,
          [variant.id]: decimalSum(line.variantQuantities[variant.id] ?? '', '1')
        };
        transferScan = '';
        formError = '';
        return;
      }
    }
    const product = products.find((item) =>
      [item.code, item.sku, ...(item.barcode_summary ?? '').split(',')].some(
        (candidate) => candidate?.trim() === barcode
      )
    );
    if (!product) {
      formError = 'Barkod çıkış deposundaki stoklarda bulunamadı.';
      return;
    }
    let line = lines.find((item) => item.productID === product.id);
    if (!line) {
      line = lines.find((item) => !item.productID) ?? newLine();
      if (!lines.includes(line)) lines = [...lines, line];
      await selectProduct(line, product.id);
    }
    if (line.variantRequired) {
      formError = 'Varyantlı stokta varyant barkodunu okutun.';
      return;
    }
    line.quantity = decimalSum(line.quantity ?? '', '1');
    transferScan = '';
    formError = '';
  }
  function removeLine(index: number) {
    if (lines.length <= 1) return;
    lines = lines.filter((_, lineIndex) => lineIndex !== index);
  }
  function validationLines(): TransferQuantityLine[] {
    const result: TransferQuantityLine[] = [];
    for (const line of lines) {
      if (line.variantRequired) {
        const entered = line.variants.filter((variant) =>
          (line.variantQuantities[variant.id] ?? '').trim()
        );
        if (entered.length === 0) {
          result.push({
            productID: line.productID,
            variantID: '',
            variantRequired: true,
            quantity: '',
            availableQuantity: '0',
            variants: [],
            loading: line.loading,
            error: line.error
          });
        } else {
          result.push(
            ...entered.map((variant) => ({
              productID: line.productID,
              variantID: variant.id,
              variantRequired: true,
              quantity: line.variantQuantities[variant.id] ?? '',
              availableQuantity: variant.availableQuantity ?? '0',
              variants: [],
              loading: line.loading,
              error: line.error
            }))
          );
        }
      } else {
        result.push({
          productID: line.productID,
          variantID: '',
          variantRequired: false,
          quantity: line.quantity ?? '',
          availableQuantity: line.product?.available_quantity ?? '0',
          variants: [],
          loading: line.loading,
          error: line.error
        });
      }
    }
    return result;
  }
  function hasEnteredQuantity() {
    return lines.some((line) =>
      line.variantRequired
        ? Object.values(line.variantQuantities).some((value) => value.trim())
        : Boolean(line.quantity?.trim())
    );
  }
  async function submitTransfer() {
    if (submitting) return;
    formError = '';
    if (!sourceWarehouseID || !destinationWarehouseID) {
      formError = 'Çıkış ve varış deposunu seçin.';
      return;
    }
    if (!hasEnteredQuantity()) {
      formError = 'En az bir stok miktarı girin.';
      return;
    }
    const quantityError = validateTransferQuantities(validationLines());
    if (quantityError) {
      formError = quantityError;
      return;
    }
    const apiLines = buildTransferApiLines(lines);
    if (apiLines.length === 0) {
      formError = 'En az bir pozitif miktar girin.';
      return;
    }
    submitting = true;
    try {
      const random =
        typeof crypto !== 'undefined' && 'randomUUID' in crypto
          ? crypto.randomUUID()
          : `${Date.now()}-${Math.random().toString(36).slice(2)}`;
      await api('/warehouse-transfers', {
        method: 'POST',
        headers: { 'Idempotency-Key': `warehouse-transfer:${random}` },
        body: JSON.stringify({
          transfer_type: transferType,
          source_warehouse_id: sourceWarehouseID,
          destination_warehouse_id: destinationWarehouseID,
          lines: apiLines
        })
      });
      successMessage =
        transferType === 'QUICK'
          ? 'Hızlı transfer teslim alındı.'
          : 'Transfer sevke çıkarıldı ve IN_TRANSIT durumuna alındı.';
      dialogOpen = false;
      listVersion += 1;
    } catch (cause) {
      formError = errorMessage(cause, 'Transfer oluşturulamadı.');
    } finally {
      submitting = false;
    }
  }
  function errorMessage(cause: unknown, fallback: string) {
    if (typeof cause === 'object' && cause && 'message' in cause) {
      const message = String((cause as { message?: unknown }).message ?? '').trim();
      if (message) return message;
    }
    return fallback;
  }
  onMount(async () => {
    try {
      const [sessionResult, warehouseResult] = await Promise.all([
        api<Session>('/session'),
        listWarehouses()
      ]);
      session = sessionResult;
      warehouses = warehouseResult.items ?? [];
    } catch (cause) {
      formError = errorMessage(cause, 'Transfer referansları alınamadı.');
    } finally {
      loadingReferences = false;
    }
  });
</script>

<svelte:window onkeydown={handleDialogKeydown} />

<svelte:head><title>Depo Transferleri · Varya One</title></svelte:head>

{#if successMessage}
  <div class="success-message" role="status">
    <span>{successMessage}</span>
    <button type="button" aria-label="Mesajı kapat" onclick={() => (successMessage = '')}
      ><X size={14} /></button
    >
  </div>
{/if}

{#key listVersion}
  <OperationListPage
    title="Depo Transferleri"
    requiredPermission="inventory.transfer.request"
    endpoint="/warehouse-transfers"
    {columns}
    {filters}
    showPrimary={false}
    clientSearchFields={[
      'transfer_no',
      'transfer_type',
      'state',
      'from_warehouse_name',
      'to_warehouse_name',
      'source_warehouse_name',
      'destination_warehouse_name'
    ]}
    searchPlaceholder="Transfer no, depo veya durum ara"
  >
    {#snippet primaryAction()}
      <Button
        disabled={loadingReferences || !session?.permissions.includes('inventory.transfer.request')}
        onclick={openCreateDialog}><Plus size={14} />Yeni transfer</Button
      >
    {/snippet}
  </OperationListPage>
{/key}

{#if dialogOpen}
  <div
    class="dialog-backdrop"
    role="presentation"
    onclick={(event) => event.target === event.currentTarget && closeCreateDialog()}
  >
    <div
      bind:this={dialogElement}
      class="dialog"
      role="dialog"
      aria-modal="true"
      aria-labelledby="transfer-dialog-title"
      tabindex="-1"
    >
      <header>
        <div>
          <h2 id="transfer-dialog-title">Yeni Transfer</h2>
        </div>
        <button class="close" type="button" aria-label="Kapat" onclick={closeCreateDialog}
          ><X size={18} /></button
        >
      </header>
      <div class="dialog-body">
        {#if formError}<div class="form-error" role="alert">{formError}</div>{/if}
        <div class="form-grid">
          <div class="field">
            <span>Transfer tipi</span>
            <select bind:value={transferType}>
              <option value="QUICK">Hızlı Transfer</option>
              <option value="WORKFLOW">Sevk / Teslim</option>
            </select>
          </div>
          <div class="field">
            <span>Çıkış deposu</span>
            <EntityCombobox
              bind:open={sourceWarehousePickerOpen}
              bind:selected={selectedSourceWarehouse}
              results={warehouseOptions(sourceOptions())}
              onSearch={(query) => searchWarehouseOptions(warehouseOptions(sourceOptions()), query)}
              onSelect={selectSourceOption}
              title="Çıkış deposu seç"
              description="Stokların düşüleceği aktif standart depoyu seçin."
              triggerLabel="Çıkış deposu"
              triggerPlaceholder="Çıkış deposu seçin"
              searchPlaceholder="Depo kodu, adı veya adresi ara"
              initialEmptyText="Aktif çıkış depoları yükleniyor…"
              emptyText="Bu aramayla eşleşen çıkış deposu yok."
              resultLabel="Çıkış depoları"
              disabled={loadingReferences}
            />
          </div>
          <div class="field">
            <span>Varış deposu</span>
            <EntityCombobox
              bind:open={destinationWarehousePickerOpen}
              bind:selected={selectedDestinationWarehouse}
              results={warehouseOptions(destinationOptions())}
              onSearch={(query) =>
                searchWarehouseOptions(warehouseOptions(destinationOptions()), query)}
              onSelect={selectDestinationOption}
              title="Varış deposu seç"
              description="Transferin teslim edileceği aktif depoyu seçin."
              triggerLabel="Varış deposu"
              triggerPlaceholder="Varış deposu seçin"
              searchPlaceholder="Depo kodu, adı veya adresi ara"
              initialEmptyText="Önce çıkış deposunu seçin."
              emptyText="Bu aramayla eşleşen varış deposu yok."
              resultLabel="Varış depoları"
              disabled={!sourceWarehouseID}
            />
          </div>
        </div>
        <div class="line-heading">
          <div>
            <h3>Stoklar</h3>
          </div>
          <Button variant="outline" size="sm" disabled={!sourceWarehouseID} onclick={addLine}
            ><Plus size={14} />Stok ekle</Button
          >
        </div>
        <form
          class="transfer-scan"
          aria-label="Transfer barkod girişi"
          onsubmit={scanTransferBarcode}
        >
          <ScanLine size={17} aria-hidden="true" />
          <input
            bind:value={transferScan}
            placeholder="Barkodu okutun veya yazın"
            autocomplete="off"
          />
          <Button type="submit" variant="outline" size="sm" disabled={!sourceWarehouseID}
            >Ekle · Enter · 1 birim</Button
          >
        </form>
        {#if loadingProducts}
          <div class="loading-state" role="status">
            <LoaderCircle class="spin" size={16} />Stok kartları yükleniyor…
          </div>
        {:else}
          <div class="lines">
            {#each lines as line, index (line.requestKey)}
              <div class="line-card">
                <div class="line-topline">
                  <div class="product-select">
                    <span>Stok kartı {index + 1}</span>
                    <EntityCombobox
                      bind:open={line.pickerOpen}
                      selected={line.selectedProduct}
                      results={stockPickerOptions}
                      onSearch={searchTransferProducts}
                      onSelect={(option) => selectProductOption(line, option)}
                      title="Transfer için stok kartı seç"
                      description="Çıkış deposundaki stok kartını adı, kodu veya barkoduyla arayın."
                      triggerLabel={`Stok kartı ${index + 1}`}
                      triggerPlaceholder="Stok kartı seçin"
                      searchPlaceholder="Stok adı, kodu veya barkodu ara"
                      initialEmptyText="Stok kartlarını görmek için arama yapın veya listeden seçin."
                      emptyText="Bu aramayla eşleşen stok kartı yok."
                      disabled={!sourceWarehouseID}
                    />
                  </div>
                  {#if lines.length > 1}<button
                      class="remove"
                      type="button"
                      aria-label={`${index + 1}. satırı kaldır`}
                      onclick={() => removeLine(index)}><X size={16} /></button
                    >{/if}
                </div>
                {#if line.product}
                  <TransferProductMatrix
                    productName={line.product.name}
                    unit={line.stockUnit}
                    variantsEnabled={line.variantRequired}
                    variants={line.variants}
                    quantity={line.quantity}
                    availableQuantity={line.product.available_quantity}
                    variantQuantities={line.variantQuantities}
                    loading={line.loading}
                    error={line.error}
                    onQuantityChange={(value) => (line.quantity = value)}
                    onVariantQuantityChange={(variantID, value) =>
                      (line.variantQuantities = { ...line.variantQuantities, [variantID]: value })}
                  />
                {:else}{/if}
              </div>
            {/each}
          </div>
        {/if}
      </div>
      <footer>
        <Button variant="outline" disabled={submitting} onclick={closeCreateDialog}>Vazgeç</Button>
        <Button
          disabled={submitting || loadingProducts || !sourceWarehouseID}
          onclick={() => void submitTransfer()}
        >
          {#if submitting}<span class="spin"><LoaderCircle size={15} /></span>{/if}Transferi oluştur
        </Button>
      </footer>
    </div>
  </div>
{/if}

<style>
  .line-heading div,
  header div {
    display: grid;
    gap: 3px;
  }
  .line-heading span,
  header p,
  .line-empty,
  .loading-state {
    color: var(--text-muted);
    font-size: 12px;
  }
  .transfer-scan {
    display: grid;
    grid-template-columns: auto minmax(180px, 1fr) auto;
    gap: 8px;
    align-items: center;
    margin-bottom: 10px;
    padding: 9px 11px;
    border: 1px solid var(--border);
    border-radius: 8px;
    background: var(--surface-muted);
  }
  .transfer-scan > input {
    min-height: var(--control-height);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    padding: 0 9px;
    background: var(--surface);
    color: var(--text);
  }
  .product-select {
    display: grid;
    min-width: 0;
    flex: 1;
    gap: 5px;
    color: var(--text-muted);
    font-size: 11px;
    font-weight: 600;
  }
  .success-message,
  .form-error {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    margin-bottom: 10px;
    padding: 9px 11px;
    border-radius: 8px;
    font-size: 12px;
  }
  .success-message {
    background: color-mix(in srgb, var(--success, #067647) 12%, var(--surface));
    color: var(--success, #067647);
  }
  .form-error {
    background: color-mix(in srgb, var(--danger, #b42318) 10%, var(--surface));
    color: var(--danger, #b42318);
  }
  .success-message button,
  .close,
  .remove {
    display: inline-flex;
    border: 0;
    background: transparent;
    color: inherit;
    cursor: pointer;
  }
  .dialog-backdrop {
    position: fixed;
    z-index: 50;
    inset: 0;
    display: grid;
    place-items: center;
    overflow: auto;
    padding: 20px;
    background: rgb(15 23 42 / 42%);
  }
  .dialog {
    position: relative;
    inset: auto;
    display: flex;
    flex-direction: column;
    justify-self: center;
    align-self: center;
    box-sizing: border-box;
    width: min(980px, 100%);
    max-width: calc(100vw - 40px);
    max-height: min(900px, calc(100dvh - 40px));
    overflow: hidden;
    margin: 0;
    padding: 0;
    border: 1px solid var(--border);
    border-radius: 12px;
    background: var(--surface);
    box-shadow: 0 20px 60px rgb(15 23 42 / 25%);
  }
  .dialog > header,
  .dialog > footer {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    padding: 16px 18px;
  }
  .dialog > header {
    border-bottom: 1px solid var(--border);
  }
  .dialog > header h2 {
    margin: 0;
    font-size: 18px;
  }
  header p {
    margin: 3px 0 0;
  }
  .dialog > footer {
    justify-content: flex-end;
    border-top: 1px solid var(--border);
    flex: 0 0 auto;
    background: var(--surface);
  }
  .dialog-body {
    min-height: 0;
    overflow: auto;
    padding: 0 18px 18px;
  }
  .form-error,
  .form-grid,
  .line-heading,
  .lines,
  .loading-state {
    margin-right: 0;
    margin-left: 0;
  }
  .form-error {
    margin-top: 14px;
    margin-bottom: 0;
  }
  .form-grid {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 12px;
    padding-top: 16px;
  }
  .field {
    display: grid;
    gap: 5px;
    min-width: 0;
    color: var(--text-muted);
    font-size: 11px;
    font-weight: 600;
  }
  select {
    min-width: 0;
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 8px 9px;
    background: var(--background);
    color: var(--text);
    font: inherit;
    font-size: 12px;
  }
  .line-heading {
    display: flex;
    align-items: end;
    justify-content: space-between;
    gap: 12px;
    margin-top: 22px;
    margin-bottom: 9px;
  }
  .line-heading h3 {
    margin: 0;
    font-size: 14px;
  }
  .line-card {
    display: grid;
    gap: 10px;
    min-width: 0;
    margin-bottom: 10px;
    padding: 10px;
    border: 1px solid var(--border);
    border-radius: 9px;
  }
  .line-topline {
    display: flex;
    align-items: end;
    gap: 8px;
    min-width: 0;
  }
  .remove {
    padding: 8px;
    color: var(--danger, #b42318);
  }
  .line-empty,
  .loading-state {
    padding: 12px;
    border-radius: 7px;
    background: var(--background);
  }
  .loading-state {
    display: flex;
    align-items: center;
    gap: 8px;
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
    .dialog-backdrop {
      place-items: stretch;
      padding: 8px;
    }
    .dialog {
      width: 100%;
      max-width: none;
      max-height: calc(100dvh - 16px);
      border-radius: 10px;
    }
    .dialog > header {
      align-items: flex-start;
      flex-direction: column;
    }
    .dialog > header,
    .dialog > footer {
      padding: 13px 14px;
    }
    .dialog-body {
      padding: 0 14px 14px;
    }
    .dialog > footer {
      flex-wrap: wrap;
    }
    .form-grid {
      grid-template-columns: 1fr;
    }
    .transfer-scan {
      grid-template-columns: 1fr;
    }
  }
</style>
