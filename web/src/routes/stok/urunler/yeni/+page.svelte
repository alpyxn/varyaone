<script lang="ts">
  import { goto } from '$app/navigation';
  import { ArrowLeft, Save } from '@lucide/svelte';
  import { onMount } from 'svelte';
  import { toast } from 'svelte-sonner';
  import { Button } from '$lib/components/ui/button';
  import { api, type Session } from '$lib/api';
  import {
    getExchangeRateDashboard,
    listPriceLists,
    createPriceEntry
  } from '$lib/features/pricing/api';
  import type { PriceList } from '$lib/features/pricing/types';
  import ProductForm from '$lib/features/products/ProductForm.svelte';
  import {
    createProduct,
    listVariantDefinitions,
    updateProductVariantConfig,
    listProductBrands,
    listProductCategories,
    listProductUnits
  } from '$lib/features/products/api';
  import {
    emptyProduct,
    normalizeProductInput,
    validateProductInput,
    emptyVariantConfig,
    type ProductBrand,
    type ProductCategory,
    type ProductInput,
    type ProductUnit,
    type ProductVariantConfig,
    type VariantDefinition
  } from '$lib/features/products/types';
  import { variantConfigurationError } from '$lib/features/products/variant-utils';
  import { divideDecimalStrings, trimDecimalZeros } from '$lib/design/decimal';

  let form = $state<ProductInput>(emptyProduct());
  let categories = $state<ProductCategory[]>([]);
  let brands = $state<ProductBrand[]>([]);
  let unitOptions = $state<ProductUnit[]>([
    { code: 'ADET', name: 'Adet', is_base: false, conversion_factor: '1', decimal_scale: 0 }
  ]);
  let loading = $state(true);
  let saving = $state(false);
  let error = $state('');
  let createdProductID = $state('');
  let createdProductVersion = $state<number>();
  let completedPriceEntryIDs = $state<string[]>([]);
  let variantConfigCompleted = $state(false);
  let referencesReady = $state(false);
  let variantDefinitionsReady = $state(true);
  let canManageVariants = $state(false);
  let baseCurrency = $state('TRY');
  let exchangeRates = $state<Record<string, string>>({ TRY: '1' });
  let manualPriceEntryIDs = $state<Set<string>>(new Set());
  type ProductPriceEntry = {
    price_list_id: string;
    list_name: string;
    list_code: string;
    currency_code: string;
    unit_price: string;
    valid_from: string;
    applies_to_all_categories: boolean;
    scope_category_id?: string;
  };
  let priceEntries = $state<ProductPriceEntry[]>([]);
  let variantDefinitions = $state<VariantDefinition[]>([]);
  let variantConfig = $state<ProductVariantConfig>(emptyVariantConfig());

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

  function syncPriceEntries(basePrice = form.sales_price || form.purchase_price) {
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

  async function loadReferences() {
    try {
      const [loadedUnits, loadedCategories, loadedBrands, loadedPriceLists] = await Promise.all([
        listProductUnits(),
        listProductCategories(),
        listProductBrands(),
        listPriceLists(false)
      ]);
      referencesReady = true;
      unitOptions = loadedUnits;
      categories = loadedCategories.filter((item) => item.is_active);
      brands = loadedBrands.filter((item) => item.is_active);
      priceEntries = loadedPriceLists.items.map((list: PriceList) => ({
        price_list_id: list.id,
        list_name: list.name,
        list_code: list.code,
        currency_code: list.currency_code,
        unit_price: '',
        valid_from: new Date().toISOString().slice(0, 10),
        applies_to_all_categories: list.applies_to_all_categories,
        scope_category_id: list.scope_category_id
      }));
      syncPriceEntries();
      await loadVariantDefinitions();
    } catch {
      referencesReady = false;
      error = 'Ürün referansları alınamadı. Tekrar deneyin.';
    } finally {
      loading = false;
    }
  }

  async function retryReferences() {
    loading = true;
    error = '';
    await loadReferences();
  }

  async function loadVariantDefinitions() {
    variantDefinitionsReady = false;
    try {
      variantDefinitions = (await listVariantDefinitions(false)).items;
      variantDefinitionsReady = true;
    } catch (cause) {
      error =
        typeof cause === 'object' && cause && 'message' in cause
          ? String(cause.message)
          : 'Varyant tanımları alınamadı. Tekrar deneyin.';
    }
  }

  async function loadPermissions() {
    try {
      const session = await api<Session>('/session');
      baseCurrency =
        session.companies.find((item) => item.id === session.current_company_id)?.base_currency ||
        'TRY';
      canManageVariants = session.permissions.includes('product.variant.manage');
    } catch {
      canManageVariants = false;
    }
  }

  async function save() {
    if (saving) return;
    if (!referencesReady) {
      error = 'Ürün referansları yüklenemedi. Tekrar deneyin.';
      return;
    }
    const normalized = normalizeProductInput(form);
    if (!createdProductID) {
      const validationMessage = validateProductInput(normalized);
      if (validationMessage) {
        error = validationMessage;
        return;
      }
    }
    if (variantConfig.enabled && !variantDefinitionsReady) {
      error = 'Varyant tanımları yüklenmeden stok kartı kaydedilemez.';
      return;
    }
    if (variantConfig.enabled) {
      const variantError = variantConfigurationError(variantConfig.dimensions);
      if (variantError) {
        error = variantError;
        return;
      }
    }
    saving = true;
    error = '';
    try {
      let productID = createdProductID;
      if (!productID) {
        const created = await createProduct(normalized);
        productID = created.id;
        createdProductID = created.id;
        createdProductVersion = created.version;
        toast.success('Stok kartı oluşturuldu.');
      }
      const applicable = priceEntries.filter(
        (entry) =>
          entry.applies_to_all_categories || entry.scope_category_id === normalized.category_id
      );
      for (const entry of applicable) {
        if (!entry.unit_price.trim()) continue;
        if (completedPriceEntryIDs.includes(entry.price_list_id)) continue;
        await createPriceEntry(entry.price_list_id, {
          item_id: productID,
          unit_price: canonicalAmount(entry.unit_price),
          valid_from: entry.valid_from
        });
        completedPriceEntryIDs = [...completedPriceEntryIDs, entry.price_list_id];
      }
      if (variantConfig.enabled && !variantConfigCompleted) {
        const savedConfig = await updateProductVariantConfig(productID, createdProductVersion, {
          ...variantConfig,
          product_id: productID
        });
        createdProductVersion = savedConfig.version;
        variantConfigCompleted = true;
        await goto(`/stok/urunler/${productID}`);
      } else {
        await goto('/stok/urunler');
      }
    } catch (cause) {
      error =
        typeof cause === 'object' && cause && 'message' in cause
          ? String(cause.message)
          : createdProductID
            ? 'Stok kartı oluşturuldu; eksik adımlar tamamlanamadı. Tekrar deneyin.'
            : 'Stok kartı oluşturulamadı.';
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

  onMount(() => {
    void loadReferences();
    void loadPermissions();
    void loadExchangeRates();
    const listener = (event: KeyboardEvent) => {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 's') {
        event.preventDefault();
        void save();
      }
    };
    window.addEventListener('keydown', listener);
    return () => window.removeEventListener('keydown', listener);
  });
</script>

<svelte:head><title>Yeni Stok Kartı · Varya One</title></svelte:head>
<header class="page-header">
  <div>
    <a class="back" href="/stok/urunler"><ArrowLeft size={14} />Stok Kartları</a>
    <h1>Yeni Stok Kartı</h1>
  </div>
  <div class="page-actions">
    <a class="link-button" href="/stok/urunler">Vazgeç</a><Button
      type="submit"
      form="new-product-form"
      disabled={saving || loading}><Save size={15} />{saving ? 'Kaydediliyor…' : 'Kaydet'}</Button
    >
  </div>
</header>
{#if error}
  <div class="notice error" role="alert">
    <span>{error}</span>
    {#if !referencesReady || !variantDefinitionsReady}
      <Button type="button" variant="outline" size="sm" onclick={() => void retryReferences()}
        >Tekrar dene</Button
      >
    {/if}
  </div>
{/if}
{#if createdProductID}
  <div class="notice partial-success" role="status">
    <strong>Stok kartı oluşturuldu.</strong>
    <span>Eksik adımlar için Kaydet ile kaldığınız yerden devam edin.</span>
    <a href={`/stok/urunler/${createdProductID}`}>Stok kartını aç</a>
  </div>
{/if}
<form
  id="new-product-form"
  class="panel form-panel"
  novalidate
  onsubmit={(event) => {
    event.preventDefault();
    void save();
  }}
>
  <ProductForm
    bind:value={form}
    {categories}
    {brands}
    {unitOptions}
    {baseCurrency}
    {priceEntries}
    onPriceChange={updatePriceEntryValue}
    onBaseSalesPriceChange={(value) => syncPriceEntries(value)}
    bind:variantConfig
    {variantDefinitions}
    {canManageVariants}
    disabled={saving || loading || Boolean(createdProductID)}
    variantDataReady={variantDefinitionsReady}
    onRetryVariantData={loadVariantDefinitions}
  />
</form>
<p class="shortcut"><kbd>Ctrl S</kbd> Kaydet</p>

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
  .form-panel {
    max-width: 980px;
    padding: 0;
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
  .partial-success {
    display: flex;
    flex-wrap: wrap;
    gap: 5px 10px;
    align-items: baseline;
  }
  .partial-success span {
    color: var(--text-muted);
  }
  .partial-success a {
    color: var(--primary);
    font-weight: 700;
  }
</style>
