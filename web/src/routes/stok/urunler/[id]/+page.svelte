<script lang="ts">
  import { ArrowLeft, Ban, Check, Plus, Save } from '@lucide/svelte';
  import { api, type Session } from '$lib/api';
  import { onMount } from 'svelte';
  import { page } from '$app/state';
  import { Button } from '$lib/components/ui/button';
  import { DocumentStatus } from '$lib/components/varya/document-status';
  import ProductForm from '$lib/features/products/ProductForm.svelte';
  import {
    createPriceEntry,
    getExchangeRateDashboard,
    listPriceEntries,
    listPriceLists,
    updatePriceEntry
  } from '$lib/features/pricing/api';
  import OperationActionDialog from '$lib/features/operations/OperationActionDialog.svelte';
  import { ConfirmDialog } from '$lib/components/varya/confirm-dialog';
  import {
    deactivateProduct,
    getProduct,
    generateProductVariants,
    getProductVariantConfig,
    listProductVariants,
    listVariantDefinitions,
    listProductBrands,
    listProductCategories,
    listProductUnits,
    updateProductVariant,
    updateProductVariantConfig,
    updateProduct
  } from '$lib/features/products/api';
  import type { ProductVariantUpdate } from '$lib/features/products/api';
  import {
    normalizeProductInput,
    validateProductInput,
    emptyVariantConfig,
    type Product,
    type ProductBrand,
    type ProductCategory,
    type ProductInput,
    type ProductUnit,
    type ProductVariant,
    type ProductVariantConfig,
    type VariantDefinition
  } from '$lib/features/products/types';
  import { divideDecimalStrings, trimDecimalZeros } from '$lib/design/decimal';

  type AdditionalPriceEntry = {
    price_list_id: string;
    entry_id?: string;
    list_name: string;
    list_code: string;
    currency_code: string;
    unit_price: string;
    valid_from: string;
    valid_to?: string;
    version?: number;
    applies_to_all_categories: boolean;
    scope_category_id?: string;
  };

  let product = $state<Product>();
  let form = $state<ProductInput>();
  let categories = $state<ProductCategory[]>([]);
  let brands = $state<ProductBrand[]>([]);
  let unitOptions = $state<ProductUnit[]>([]);
  let loading = $state(true);
  let saving = $state(false);
  let error = $state('');
  let permissions = $state<string[]>([]);
  let baseCurrency = $state('TRY');
  let exchangeRates = $state<Record<string, string>>({ TRY: '1' });
  let manualPriceEntryIDs = $state<Set<string>>(new Set());
  let movementOpen = $state(false);
  let priceEntries = $state<AdditionalPriceEntry[]>([]);
  let variantDefinitions = $state<VariantDefinition[]>([]);
  let variantConfig = $state<ProductVariantConfig>(emptyVariantConfig());
  let savedVariantConfigSignature = $state('');
  let variants = $state<ProductVariant[]>([]);
  let variantLoading = $state(false);
  let variantDataLoading = $state(false);
  let variantDataReady = $state(false);
  let variantError = $state('');
  let message = $state('');

  const canEditProduct = $derived(permissions.includes('product.edit'));

  const canManageVariants = $derived(permissions.includes('product.variant.manage'));
  const variantConfigDirty = $derived(
    variantConfigSignature(variantConfig) !== savedVariantConfigSignature
  );

  function variantConfigSignature(config: ProductVariantConfig) {
    return JSON.stringify({
      enabled: config.enabled,
      dimensions: config.dimensions.map((dimension, index) => ({
        definition_id: dimension.definition_id,
        position: index + 1,
        option_ids: [...dimension.option_ids].sort()
      }))
    });
  }

  function updatePriceEntryValue(priceListID: string, unitPrice: string) {
    manualPriceEntryIDs = new Set(manualPriceEntryIDs).add(priceListID);
    priceEntries = priceEntries.map((entry) =>
      entry.price_list_id === priceListID ? { ...entry, unit_price: unitPrice } : entry
    );
  }

  function priceFromBase(basePrice: string, currencyCode: string) {
    const base = trimDecimalZeros(basePrice);
    const target = currencyCode.toUpperCase();
    if (!base) return '';
    if (target === baseCurrency) return base;
    return divideDecimalStrings(base, exchangeRates[target] ?? '');
  }

  function syncPriceEntries(basePrice = form?.sales_price || form?.purchase_price || '') {
    const source = trimDecimalZeros(basePrice);
    priceEntries = priceEntries.map((entry) =>
      manualPriceEntryIDs.has(entry.price_list_id)
        ? entry
        : { ...entry, unit_price: priceFromBase(source, entry.currency_code) }
    );
  }

  async function loadExchangeRates() {
    try {
      const result = await getExchangeRateDashboard();
      const resolvedBase = (result.base_currency || baseCurrency).toUpperCase();
      const nextRates: Record<string, string> = { [resolvedBase]: '1' };
      for (const item of result.items ?? [])
        nextRates[item.currency_code.toUpperCase()] = item.rate_to_base;
      baseCurrency = resolvedBase;
      exchangeRates = nextRates;
      syncPriceEntries();
    } catch {
      // The server remains authoritative; without a cached rate we leave the
      // foreign-currency price empty instead of relabelling a TRY amount.
    }
  }

  function compactDecimal(value?: string) {
    const text = String(value ?? '')
      .trim()
      .replace(',', '.');
    if (!text) return '0';
    return (
      text
        .replace(/(\.\d*?[1-9])0+$/, '$1')
        .replace(/\.0+$/, '')
        .replace(/\.$/, '') || '0'
    );
  }

  function localISODate(date = new Date()) {
    const year = date.getFullYear();
    const month = String(date.getMonth() + 1).padStart(2, '0');
    const day = String(date.getDate()).padStart(2, '0');
    return `${year}-${month}-${day}`;
  }

  function productToInput(item: Product): ProductInput {
    const units = Array.isArray(item.units) ? item.units : [];
    const barcodes = Array.isArray(item.barcodes) ? item.barcodes : [];
    const baseUnit = units.find((unit) => unit.is_base) ?? units[0];
    return {
      code: item.code,
      sku: item.sku,
      name: item.name,
      kind: item.kind,
      description: item.description,
      // PostgreSQL numeric values may arrive padded to the column scale
      // (for example `125.000000000000`). Keep the editable card readable
      // while preserving the exact value when it is sent back to the API.
      purchase_price: compactDecimal(item.purchase_price),
      sales_price: compactDecimal(item.sales_price),
      custom_description_1: item.custom_description_1 || '',
      custom_description_2: item.custom_description_2 || '',
      custom_description_3: item.custom_description_3 || '',
      purchase_tax_type: item.purchase_tax_type || 'KDV',
      sales_tax_type: item.sales_tax_type || 'KDV',
      purchase_tax_rate: compactDecimal(item.purchase_tax_rate),
      sales_tax_rate: compactDecimal(item.sales_tax_rate),
      purchase_tax_included: Boolean(item.purchase_tax_included),
      sales_tax_included: Boolean(item.sales_tax_included),
      excise_tax_rate: compactDecimal(item.excise_tax_rate),
      withholding_code: item.withholding_code || '',
      withholding_rate: compactDecimal(item.withholding_rate),
      exemption_code: item.exemption_code || '',
      tax_note: item.tax_note || '',
      ...(item.purchase_tax_profile &&
      (item.purchase_tax_profile.treatment ||
        item.purchase_tax_profile.rate !== undefined ||
        item.purchase_tax_profile.tax_rate_id ||
        item.purchase_tax_profile.exemption_id ||
        item.purchase_tax_profile.withholding_rule_id ||
        item.purchase_tax_profile.components?.length)
        ? {
            purchase_tax_profile: {
              ...item.purchase_tax_profile,
              rate: compactDecimal(item.purchase_tax_profile.rate),
              components: item.purchase_tax_profile.components || []
            }
          }
        : {}),
      ...(item.sales_tax_profile &&
      (item.sales_tax_profile.treatment ||
        item.sales_tax_profile.rate !== undefined ||
        item.sales_tax_profile.tax_rate_id ||
        item.sales_tax_profile.exemption_id ||
        item.sales_tax_profile.withholding_rule_id ||
        item.sales_tax_profile.components?.length)
        ? {
            sales_tax_profile: {
              ...item.sales_tax_profile,
              rate: compactDecimal(item.sales_tax_profile.rate),
              components: item.sales_tax_profile.components || []
            }
          }
        : {}),
      category_id: item.category_id || '',
      brand_id: item.brand_id || '',
      is_active: item.is_active,
      base_unit: baseUnit?.code || '',
      // Legacy cards may contain conversion units. The UI deliberately
      // exposes one stock unit and normalizes the card on the next save.
      units: baseUnit
        ? [
            {
              code: baseUnit.code,
              is_base: true,
              conversion_factor: '1',
              decimal_scale: baseUnit.decimal_scale
            }
          ]
        : [],
      barcodes
    };
  }

  async function loadAdditionalPrices(productID: string) {
    try {
      const lists = (await listPriceLists(false)).items;
      const on = localISODate();
      const resolved = await Promise.all(
        lists.map(async (list) => {
          const entries = (await listPriceEntries(list.id, productID, on)).items;
          const entry = entries[0];
          return {
            price_list_id: list.id,
            entry_id: entry?.id,
            list_name: list.name,
            list_code: list.code,
            currency_code: list.currency_code,
            unit_price: entry?.unit_price ? compactDecimal(entry.unit_price) : '',
            valid_from: entry?.valid_from ?? on,
            valid_to: entry?.valid_to,
            version: entry?.version,
            applies_to_all_categories: list.applies_to_all_categories,
            scope_category_id: list.scope_category_id
          };
        })
      );
      manualPriceEntryIDs = new Set(
        resolved.filter((entry) => entry.entry_id).map((entry) => entry.price_list_id)
      );
      priceEntries = resolved;
      syncPriceEntries();
    } catch {
      priceEntries = [];
    }
  }

  async function load() {
    const id = page.params.id;
    if (!id) {
      error = 'Stok kartı kimliği bulunamadı.';
      loading = false;
      return;
    }
    try {
      const [loaded, loadedUnits, loadedCategories, loadedBrands, loadedSession] =
        await Promise.all([
          getProduct(id),
          listProductUnits(),
          listProductCategories(),
          listProductBrands(),
          api<Session>('/session')
        ]);
      permissions = loadedSession.permissions;
      baseCurrency =
        loadedSession.companies.find((item) => item.id === loadedSession.current_company_id)
          ?.base_currency || 'TRY';
      product = loaded;
      form = productToInput(loaded);
      unitOptions = loadedUnits;
      categories = loadedCategories.filter(
        (item) => item.is_active || item.id === loaded.category_id
      );
      brands = loadedBrands.filter((item) => item.is_active || item.id === loaded.brand_id);
      variantDefinitions = [];
      variantConfig = { ...emptyVariantConfig(loaded.id), enabled: loaded.variants_enabled };
      savedVariantConfigSignature = variantConfigSignature(variantConfig);
      variants = [];
      variantDataReady = false;
      void loadVariantData(loaded.id);
      void loadAdditionalPrices(loaded.id);
      void loadExchangeRates();
    } catch (cause) {
      error =
        typeof cause === 'object' && cause && 'message' in cause
          ? String(cause.message)
          : 'Stok kartı okunamadı.';
    } finally {
      loading = false;
    }
  }

  async function loadVariantData(productID: string) {
    variantDataLoading = true;
    variantError = '';
    variantDataReady = false;
    try {
      const [loadedDefinitions, loadedConfig, loadedVariants] = await Promise.all([
        listVariantDefinitions(false),
        getProductVariantConfig(productID),
        listProductVariants(productID)
      ]);
      variantDefinitions = loadedDefinitions.items;
      variantConfig = { ...emptyVariantConfig(productID), ...loadedConfig };
      savedVariantConfigSignature = variantConfigSignature(variantConfig);
      variants = loadedVariants.items;
      variantDataReady = true;
    } catch (cause) {
      variantError = readableVariantError(cause, 'Varyant tanımları ve mevcut kayıtlar alınamadı.');
    } finally {
      variantDataLoading = false;
    }
  }

  async function save() {
    if (!product || !form || saving || !canEditProduct) return;
    const normalized = normalizeProductInput(form);
    const validationMessage = validateProductInput(normalized, { allowBlankCode: false });
    if (validationMessage) {
      error = validationMessage;
      return;
    }
    saving = true;
    error = '';
    message = '';
    try {
      product = await updateProduct(product.id, product.version, normalized);
      if (
        canManageVariants &&
        variantConfigSignature(variantConfig) !== savedVariantConfigSignature
      ) {
        variantConfig = await updateProductVariantConfig(
          product.id,
          product.version,
          variantConfig
        );
        savedVariantConfigSignature = variantConfigSignature(variantConfig);
        if (variantConfig.version !== undefined) {
          product = { ...product, version: variantConfig.version };
        }
      }
      await savePriceEntries(product.id, normalized.category_id);
      message = 'Stok kartı kaydedildi.';
    } catch (cause) {
      error =
        typeof cause === 'object' && cause && 'message' in cause
          ? String(cause.message)
          : 'Stok kartı güncellenemedi.';
    } finally {
      saving = false;
    }
  }

  function canonicalAmount(value: string) {
    const compact = value.trim().replace(/\s/g, '');
    const comma = compact.lastIndexOf(',');
    const dot = compact.lastIndexOf('.');
    return comma >= 0 && dot >= 0
      ? comma > dot
        ? compact.replace(/\./g, '').replace(',', '.')
        : compact.replace(/,/g, '')
      : comma >= 0
        ? compact.replace(',', '.')
        : compact;
  }

  async function savePriceEntries(productID: string, categoryID: string) {
    const applicable = priceEntries.filter(
      (entry) => entry.applies_to_all_categories || entry.scope_category_id === categoryID
    );
    for (const entry of applicable) {
      if (!entry.unit_price.trim()) continue;
      const input = {
        item_id: productID,
        unit_price: canonicalAmount(entry.unit_price),
        valid_from: entry.valid_from || localISODate(),
        valid_to: entry.valid_to || undefined
      };
      if (entry.entry_id && entry.version) {
        await updatePriceEntry(entry.price_list_id, entry.entry_id, entry.version, input);
      } else {
        await createPriceEntry(entry.price_list_id, input);
      }
    }
  }

  async function refreshAfterMovement() {
    if (!product) return;
    message = '';
    error = '';
    try {
      const [refreshedProduct, refreshedVariants] = await Promise.all([
        getProduct(product.id),
        listProductVariants(product.id)
      ]);
      product = refreshedProduct;
      form = productToInput(refreshedProduct);
      variants = refreshedVariants.items;
      message = 'Stok hareketi kaydedildi.';
    } catch (cause) {
      const details =
        typeof cause === 'object' && cause && 'message' in cause
          ? String((cause as { message?: unknown }).message ?? '')
          : '';
      error = details
        ? `Stok hareketi kaydedildi ancak stok bilgileri yenilenemedi: ${details}`
        : 'Stok hareketi kaydedildi ancak stok bilgileri yenilenemedi.';
    }
  }

  async function generateVariants() {
    if (
      !product ||
      !variantConfig.enabled ||
      variantLoading ||
      !canManageVariants ||
      !variantDataReady
    )
      return;
    variantLoading = true;
    variantError = '';
    try {
      if (variantConfigDirty) {
        variantConfig = await updateProductVariantConfig(
          product.id,
          product.version,
          variantConfig
        );
        savedVariantConfigSignature = variantConfigSignature(variantConfig);
        if (variantConfig.version !== undefined) {
          product = { ...product, version: variantConfig.version };
        }
      }
      variants = (await generateProductVariants(product.id)).items;
    } catch (cause) {
      variantError = readableVariantError(cause, 'Eksik varyantlar üretilemedi.');
    } finally {
      variantLoading = false;
    }
  }

  async function saveVariant(variantID: string, version: number, input: ProductVariantUpdate) {
    if (!product || !canManageVariants) return;
    variantError = '';
    try {
      const updated = await updateProductVariant(product.id, variantID, version, input);
      variants = variants.map((item) => (item.id === variantID ? updated : item));
    } catch (cause) {
      variantError = readableVariantError(cause, 'Varyant güncellenemedi.');
      throw cause;
    }
  }

  function readableVariantError(cause: unknown, fallback: string) {
    if (typeof cause === 'object' && cause && 'code' in cause) {
      const code = String(cause.code);
      if (code === 'FORBIDDEN' || code === 'PERMISSION_DENIED')
        return 'Bu işlem için yetkiniz yok.';
      if (code === 'CONFLICT' || code === 'VERSION_CONFLICT')
        return 'Varyant başka bir kullanıcı tarafından değiştirildi. Sayfayı yenileyin.';
      if (code === 'IDENTITY_LOCKED')
        return 'Bu varyantın kimliği ilk stok hareketinden sonra kilitlendi.';
      if ('message' in cause && cause.message) return String(cause.message);
    }
    return cause instanceof Error ? cause.message : fallback;
  }

  let confirmOpen = $state(false);
  let confirmState = $state<{
    title: string;
    description: string;
    confirmLabel: string;
    run: () => Promise<void>;
  } | null>(null);

  function deactivate() {
    if (!product || saving || !canEditProduct) return;
    confirmState = {
      title: 'Stok kartı pasifleştir',
      description: 'Bu stok kartı pasifleştirilsin mi? Geçmiş kullanım kayıtları korunur.',
      confirmLabel: 'Pasifleştir',
      run: runDeactivate
    };
    confirmOpen = true;
  }

  async function runDeactivate() {
    if (!product) return;
    saving = true;
    error = '';
    try {
      product = await deactivateProduct(product.id, product.version);
      form = productToInput(product);
    } catch (cause) {
      error =
        typeof cause === 'object' && cause && 'message' in cause
          ? String(cause.message)
          : 'Stok kartı pasifleştirilemedi.';
    } finally {
      saving = false;
    }
  }

  async function activate() {
    if (!form || saving || !canEditProduct) return;
    form = { ...form, is_active: true };
    await save();
  }

  onMount(() => {
    void load();
    const listener = (event: KeyboardEvent) => {
      if (canEditProduct && (event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 's') {
        event.preventDefault();
        void save();
      }
    };
    window.addEventListener('keydown', listener);
    return () => window.removeEventListener('keydown', listener);
  });
</script>

<svelte:head><title>{product?.name ?? 'Stok Kartı'} · Varya One</title></svelte:head>

{#if confirmState}
  <ConfirmDialog
    bind:open={confirmOpen}
    title={confirmState.title}
    description={confirmState.description}
    confirmLabel={confirmState.confirmLabel}
    onConfirm={confirmState.run}
  />
{/if}
{#if loading}<div class="panel loading">Stok kartı yükleniyor…</div>{:else if !product || !form}<div
    class="panel error-panel"
  >
    <strong>Stok kartı açılamadı.</strong>{#if error}<span>{error}</span>{/if}<a
      class="button"
      href="/stok/urunler">Listeye dön</a
    >
  </div>{:else}<header class="page-header">
    <div>
      <a class="back" href="/stok/urunler"><ArrowLeft size={14} />Stok Kartları</a>
      <div class="title-line">
        <h1>{product.name}</h1>
        <DocumentStatus status={product.is_active ? 'ACTIVE' : 'INACTIVE'} />
      </div>
      <p class="meta">
        {product.code} · {product.kind === 'SERVICE' ? 'Hizmet' : 'Fiziksel Ürün'}
      </p>
    </div>
    <div class="page-actions">
      {#if canEditProduct && product.is_active}
        <Button variant="outline" disabled={saving} onclick={deactivate}
          ><Ban size={15} />Pasifleştir</Button
        >
      {:else if canEditProduct}
        <Button variant="outline" disabled={saving} onclick={() => void activate()}
          ><Check size={15} />Aktifleştir</Button
        >
      {/if}
      {#if canEditProduct}<Button type="submit" form="product-detail-form" disabled={saving}
          ><Save size={15} />{saving ? 'Kaydediliyor…' : 'Kaydet'}</Button
        >{:else}<span class="read-only-badge" role="status">Salt okunur</span>{/if}
      {#if product.is_active && permissions.includes('inventory.movement.post')}
        <Button
          type="button"
          variant="outline"
          disabled={saving}
          onclick={() => (movementOpen = true)}><Plus size={15} />Stok Hareketi Ekle</Button
        >
      {/if}
    </div>
  </header>
  {#if error}<div class="notice error" role="alert">{error}</div>{/if}
  {#if message}<div class="notice success" role="status">{message}</div>{/if}
  <form
    id="product-detail-form"
    class="panel form-panel"
    novalidate
    onsubmit={(event) => {
      event.preventDefault();
      void save();
    }}
  >
    <ProductForm
      bind:value={form}
      productID={product.id}
      {categories}
      {brands}
      {unitOptions}
      {baseCurrency}
      {priceEntries}
      onPriceChange={updatePriceEntryValue}
      onBaseSalesPriceChange={(value) => syncPriceEntries(value)}
      bind:variantConfig
      {variantDefinitions}
      {variants}
      productStock={product}
      persisted
      {variantLoading}
      {variantDataReady}
      {variantDataLoading}
      {variantError}
      {canManageVariants}
      readOnly={!canEditProduct}
      onGenerateVariants={generateVariants}
      generateLabel={variantConfigDirty ? 'Kaydet ve kombinasyonları üret' : undefined}
      onRetryVariantData={() => (product ? loadVariantData(product.id) : Promise.resolve())}
      onVariantSave={saveVariant}
      unitLocked
      disabled={saving}
    />
  </form>
  <p class="shortcut"><kbd>Ctrl S</kbd> Kaydet</p>{/if}

{#if product && movementOpen}
  <OperationActionDialog
    bind:open={movementOpen}
    kind="stock-movement"
    label="Stok Hareketi Ekle"
    productID={product.id}
    selectedProductLabel={`${product.code} · ${product.name}`}
    onComplete={refreshAfterMovement}
  />
{/if}

<style>
  .back {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    margin-bottom: 5px;
    color: var(--primary);
    font-size: 11px;
    text-decoration: none;
  }
  .title-line {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .title-line h1 {
    margin: 0;
  }
  .meta {
    margin: 4px 0 0;
    color: var(--text-muted);
    font-size: 11px;
  }
  .read-only-badge {
    display: inline-flex;
    align-items: center;
    min-height: var(--control-height);
    padding: 0 9px;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    color: var(--text-muted);
    font-size: 11px;
    font-weight: 700;
  }
  .form-panel {
    max-width: 980px;
    padding: 0;
  }
  .notice.success {
    border-color: color-mix(in srgb, var(--success, #15803d) 45%, var(--border));
    color: var(--success, #15803d);
  }
  .shortcut {
    color: var(--text-muted);
    font-size: 10.5px;
  }
  .shortcut kbd {
    border: 1px solid var(--border);
    border-radius: 4px;
    padding: 1px 4px;
    background: var(--surface);
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
</style>
