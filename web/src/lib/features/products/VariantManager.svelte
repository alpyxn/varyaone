<script module lang="ts">
  import type { ProductBarcode } from './types';

  /** Keep decimal values as strings while removing only insignificant fractional zeroes. */
  export function compactDecimal(value: string | null | undefined): string {
    const text = value?.trim() ?? '';
    if (!text) return '';
    return text
      .replace(/(\.\d*?[1-9])0+$/, '$1')
      .replace(/\.0+$/, '')
      .replace(/\.$/, '');
  }

  export function validateVariantBarcodes(barcodes: readonly ProductBarcode[]): string | undefined {
    const filled = barcodes.map((item) => item.barcode.trim());
    if (filled.some((barcode) => !barcode)) return 'Barkod değeri boş bırakılamaz.';
    if (new Set(filled).size !== filled.length)
      return 'Aynı varyantta aynı barkod birden fazla kullanılamaz.';
    if (barcodes.filter((item) => item.is_primary).length > 1) {
      return 'Yalnızca bir barkod ana barkod olabilir.';
    }
    if (filled.length > 0 && !barcodes.some((item) => item.is_primary)) {
      return 'Bir barkodu ana olarak seçin.';
    }
    return undefined;
  }

  export function normalizeVariantBarcodes(barcodes: readonly ProductBarcode[]): ProductBarcode[] {
    return barcodes.map((item) => ({
      ...(item.id ? { id: item.id } : {}),
      barcode: item.barcode.trim(),
      barcode_type: item.barcode_type.trim().toUpperCase() || 'EAN',
      is_primary: item.is_primary
    }));
  }

  /** Canonical dirty-check representation: server ids and editor order do not matter. */
  export function variantBarcodeSnapshot(barcodes: readonly ProductBarcode[]): string {
    return JSON.stringify(
      normalizeVariantBarcodes(barcodes)
        .map(({ barcode, barcode_type, is_primary }) => ({
          barcode,
          barcode_type,
          is_primary: Boolean(is_primary)
        }))
        .sort((left, right) =>
          `${left.barcode}\u0000${left.barcode_type}`.localeCompare(
            `${right.barcode}\u0000${right.barcode_type}`,
            'en'
          )
        )
    );
  }

  export function changedVariantIDs(
    previous: readonly { id: string; version: number }[],
    next: readonly { id: string; version: number }[]
  ): string[] {
    const previousVersions = new Map(previous.map((variant) => [variant.id, variant.version]));
    return next
      .filter(
        (variant) =>
          previousVersions.has(variant.id) && previousVersions.get(variant.id) !== variant.version
      )
      .map((variant) => variant.id);
  }

  export function variantBarcodeErrorMessage(code: string, message = ''): string | undefined {
    if (code === 'VARIANT_BARCODE_LIST_DUPLICATE') {
      return 'Aynı varyantta aynı barkod birden fazla kullanılamaz.';
    }
    if (
      code === 'BARCODE_CONFLICT' ||
      code === 'DUPLICATE_BARCODE' ||
      code === 'BARCODE_DUPLICATE' ||
      code === 'VARIANT_BARCODE_DUPLICATE' ||
      code === 'PRODUCT_BARCODE_CONFLICT'
    ) {
      const specificMessage = message.trim();
      if (/^(Aynı varyant barkodu|Barkod ")/i.test(specificMessage)) {
        return specificMessage;
      }
      return 'Bu barkod başka bir ürün veya varyantta kullanılıyor. Şirket içinde kullanılmamış bir barkod girin.';
    }
    if (code === 'VALIDATION_ERROR' && /barkod.*(başka|zaten kullanılıyor)/i.test(message)) {
      return 'Bu barkod başka bir ürün veya varyantta kullanılıyor. Şirket içinde kullanılmamış bir barkod girin.';
    }
    return undefined;
  }
</script>

<script lang="ts">
  import {
    AlertTriangle,
    Check,
    ChevronDown,
    LockKeyhole,
    Plus,
    Save,
    Trash2
  } from '@lucide/svelte';
  import { Button } from '$lib/components/ui/button';
  import * as Alert from '$lib/components/ui/alert';
  import { Input } from '$lib/components/ui/input';
  import { formatMoney, formatQuantity } from '$lib/design/formatters';
  import type {
    Product,
    ProductVariant,
    ProductVariantConfig,
    ProductVariantDimension,
    VariantDefinition,
    VariantOption
  } from './types';
  import {
    combinationCount,
    combinationCountLabel,
    combinationWarning,
    MAX_VARIANT_DIMENSIONS,
    variantConfigurationError
  } from './variant-utils';
  import { updateProductVariantBarcodes, type ProductVariantUpdate } from './api';

  type PriceListRow = {
    price_list_id: string;
    list_name: string;
    list_code: string;
    currency_code: string;
  };

  let {
    productCode = '',
    productID,
    productBarcodes = [],
    productStock,
    baseCurrency = 'TRY',
    parentPurchasePrice = '0',
    parentSalesPrice = '0',
    config = $bindable<ProductVariantConfig>(),
    definitions = [],
    variants = [],
    priceLists = [],
    persisted = false,
    variantDataReady = true,
    variantDataLoading = false,
    loading = false,
    disabled = false,
    canManage = true,
    error = '',
    generateLabel = 'Eksik kombinasyonları üret',
    onGenerate,
    onRetryData,
    onVariantSave
  }: {
    productCode?: string;
    productID?: string;
    productBarcodes?: ProductVariant['barcodes'];
    productStock?: Pick<
      Product,
      'physical_quantity' | 'reserved_quantity' | 'available_quantity' | 'stock_unit'
    >;
    baseCurrency?: string;
    parentPurchasePrice?: string;
    parentSalesPrice?: string;
    config?: ProductVariantConfig;
    definitions?: VariantDefinition[];
    variants?: ProductVariant[];
    priceLists?: PriceListRow[];
    persisted?: boolean;
    variantDataReady?: boolean;
    variantDataLoading?: boolean;
    loading?: boolean;
    disabled?: boolean;
    canManage?: boolean;
    error?: string;
    generateLabel?: string;
    onGenerate?: () => Promise<void>;
    onRetryData?: () => Promise<void>;
    onVariantSave?: (
      variantID: string,
      version: number,
      input: ProductVariantUpdate
    ) => Promise<void>;
  } = $props();

  let savingVariant = $state<string>();
  let localError = $state('');
  let drafts = $state<Record<string, ProductVariantUpdate>>({});
  let variantErrors = $state<Record<string, string>>({});
  let renderedVariants = $state<ProductVariant[]>([]);
  let sourceVariants: ProductVariant[] | undefined;

  const activeDefinitions = $derived(definitions.filter((definition) => definition.is_active));
  const dimensions = $derived(config?.dimensions ?? []);
  const selectedCount = $derived(combinationCount(dimensions));
  const configError = $derived(variantConfigurationError(dimensions));
  const configWarning = $derived(combinationWarning(dimensions));
  const identityLocked = $derived(Boolean(config?.identity_locked || config?.movement_started));
  const variantPriceLists = $derived(priceLists.filter((item) => item.price_list_id));

  $effect(() => {
    if (variants !== sourceVariants) {
      const previousVariants = sourceVariants ?? [];
      const nextByID = new Map(variants.map((variant) => [variant.id, variant]));
      const changedIDs = new Set(changedVariantIDs(previousVariants, variants));
      const nextDrafts = { ...drafts };
      const nextErrors = { ...variantErrors };
      for (const variantID of Object.keys(nextDrafts)) {
        const next = nextByID.get(variantID);
        if (!next || changedIDs.has(variantID)) {
          delete nextDrafts[variantID];
          delete nextErrors[variantID];
        }
      }
      for (const variantID of Object.keys(nextErrors)) {
        if (!nextByID.has(variantID) || changedIDs.has(variantID)) delete nextErrors[variantID];
      }
      drafts = nextDrafts;
      variantErrors = nextErrors;
      sourceVariants = variants;
      renderedVariants = variants;
    }
  });

  function definitionFor(dimension: ProductVariantDimension) {
    return definitions.find((definition) => definition.id === dimension.definition_id);
  }

  function selectedOptions(dimension: ProductVariantDimension): VariantOption[] {
    const selected = new Set(dimension.option_ids);
    return (definitionFor(dimension)?.options ?? []).filter((option) => selected.has(option.id));
  }

  function toggleDefinition(definitionID: string, checked: boolean) {
    if (!config || disabled || identityLocked) return;
    if (checked) {
      if (config.dimensions.length >= MAX_VARIANT_DIMENSIONS) {
        localError = `Bir üründe en fazla ${MAX_VARIANT_DIMENSIONS} boyut seçebilirsiniz.`;
        return;
      }
      config = {
        ...config,
        dimensions: [...config.dimensions, { definition_id: definitionID, option_ids: [] }]
      };
    } else {
      config = {
        ...config,
        dimensions: config.dimensions.filter(
          (dimension) => dimension.definition_id !== definitionID
        )
      };
    }
    localError = '';
  }

  function toggleOption(definitionID: string, optionID: string, checked: boolean) {
    if (!config || disabled || identityLocked) return;
    config = {
      ...config,
      dimensions: config.dimensions.map((dimension) => {
        if (dimension.definition_id !== definitionID) return dimension;
        const options = new Set(dimension.option_ids);
        checked ? options.add(optionID) : options.delete(optionID);
        return { ...dimension, option_ids: [...options] };
      })
    };
    localError = '';
  }

  function draftFor(variant: ProductVariant): ProductVariantUpdate {
    return (
      drafts[variant.id] ?? {
        variant_code: variant.variant_code,
        barcodes: variant.barcodes ?? [],
        is_active: variant.is_active,
        purchase_price_override: compactDecimal(variant.purchase_price_override),
        sales_price_override: compactDecimal(variant.sales_price_override),
        price_entries: variant.price_entries ?? []
      }
    );
  }

  function updateDraft(variant: ProductVariant, patch: ProductVariantUpdate) {
    drafts = { ...drafts, [variant.id]: { ...draftFor(variant), ...patch } };
  }

  function updateVariantBarcodes(variant: ProductVariant, next: ProductVariant['barcodes']) {
    updateDraft(variant, { barcodes: next });
    clearVariantError(variant.id);
  }

  function addVariantBarcode(variant: ProductVariant) {
    const existing = draftFor(variant).barcodes ?? [];
    updateVariantBarcodes(variant, [
      ...existing,
      {
        barcode: '',
        barcode_type: 'EAN',
        is_primary: existing.length === 0
      }
    ]);
  }

  function updateVariantBarcode(
    variant: ProductVariant,
    index: number,
    patch: Partial<ProductVariant['barcodes'][number]>
  ) {
    const existing = draftFor(variant).barcodes ?? [];
    updateVariantBarcodes(
      variant,
      existing.map((item, itemIndex) =>
        itemIndex === index
          ? { ...item, ...patch }
          : { ...item, is_primary: patch.is_primary ? false : item.is_primary }
      )
    );
  }

  function removeVariantBarcode(variant: ProductVariant, index: number) {
    const existing = draftFor(variant).barcodes ?? [];
    const next = existing.filter((_, itemIndex) => itemIndex !== index);
    if (existing[index]?.is_primary && next.length > 0) next[0] = { ...next[0], is_primary: true };
    updateVariantBarcodes(variant, next);
  }

  function clearVariantError(variantID: string) {
    if (!variantErrors[variantID]) return;
    const next = { ...variantErrors };
    delete next[variantID];
    variantErrors = next;
  }

  function setVariantError(variantID: string, message: string) {
    variantErrors = { ...variantErrors, [variantID]: message };
  }

  function focusVariantBarcode(variantID: string, index = 0) {
    if (typeof document === 'undefined') return;
    queueMicrotask(() => document.getElementById(variantBarcodeInputID(variantID, index))?.focus());
  }

  function variantBarcodeInputID(variantID: string, index: number) {
    return `variant-${variantID}-barcode-${index}`;
  }

  function barcodesChanged(variant: ProductVariant, draft: ProductVariantUpdate) {
    return (
      variantBarcodeSnapshot(draft.barcodes ?? []) !==
      variantBarcodeSnapshot(variant.barcodes ?? [])
    );
  }

  function priceEntriesSnapshot(entries: ProductVariant['price_entries']) {
    return JSON.stringify(
      (entries ?? [])
        .map((entry) => ({
          price_list_id: entry.price_list_id,
          unit_price: compactDecimal(entry.unit_price),
          valid_from: entry.valid_from ?? '',
          valid_to: entry.valid_to ?? ''
        }))
        .sort((left, right) =>
          `${left.price_list_id}\u0000${left.valid_from}`.localeCompare(
            `${right.price_list_id}\u0000${right.valid_from}`,
            'en'
          )
        )
    );
  }

  function otherVariantFieldsChanged(variant: ProductVariant, draft: ProductVariantUpdate) {
    return (
      (draft.variant_code ?? '').trim().toUpperCase() !==
        variant.variant_code.trim().toUpperCase() ||
      (draft.is_active ?? variant.is_active) !== variant.is_active ||
      compactDecimal(draft.purchase_price_override) !==
        compactDecimal(variant.purchase_price_override) ||
      compactDecimal(draft.sales_price_override) !== compactDecimal(variant.sales_price_override) ||
      priceEntriesSnapshot(draft.price_entries) !== priceEntriesSnapshot(variant.price_entries)
    );
  }

  function replaceRenderedVariant(updated: ProductVariant) {
    renderedVariants = renderedVariants.map((item) => (item.id === updated.id ? updated : item));
  }

  function updateVariantPrice(variant: ProductVariant, side: 'purchase' | 'sales', value: string) {
    updateDraft(variant, { [`${side}_price_override`]: value } as ProductVariantUpdate);
    clearVariantError(variant.id);
  }

  function compactVariantPrice(variant: ProductVariant, side: 'purchase' | 'sales') {
    const draft = draftFor(variant);
    const key = `${side}_price_override` as 'purchase_price_override' | 'sales_price_override';
    updateDraft(variant, { [key]: compactDecimal(draft[key]) } as ProductVariantUpdate);
  }

  function updatePriceListValue(variant: ProductVariant, priceListID: string, value: string) {
    const entries = [...(draftFor(variant).price_entries ?? [])];
    const index = entries.findIndex((entry) => entry.price_list_id === priceListID);
    const nextEntry = {
      price_list_id: priceListID,
      ...(index >= 0 ? entries[index] : {}),
      unit_price: value
    };
    if (index >= 0) entries[index] = nextEntry;
    else entries.push(nextEntry);
    updateDraft(variant, { price_entries: entries });
    clearVariantError(variant.id);
  }

  function compactPriceListValue(variant: ProductVariant, priceListID: string) {
    updatePriceListValue(variant, priceListID, compactDecimal(draftPrice(variant, priceListID)));
  }

  function draftPrice(variant: ProductVariant, priceListID: string) {
    const draft = drafts[variant.id];
    const draftEntry = draft?.price_entries?.find((entry) => entry.price_list_id === priceListID);
    if (draft && draft.price_entries !== variant.price_entries) return draftEntry?.unit_price ?? '';
    return compactDecimal(
      draftEntry?.unit_price ??
        variant.price_entries?.find((entry) => entry.price_list_id === priceListID)?.unit_price
    );
  }

  async function saveVariant(variant: ProductVariant) {
    if (
      (!onVariantSave && !(productID ?? config?.product_id)) ||
      savingVariant ||
      disabled ||
      !canManage
    )
      return;
    savingVariant = variant.id;
    localError = '';
    clearVariantError(variant.id);
    const draft = draftFor(variant);
    const barcodeInput = normalizeVariantBarcodes(draft.barcodes ?? []);
    const barcodeError = validateVariantBarcodes(barcodeInput);
    if (barcodeError) {
      setVariantError(variant.id, barcodeError);
      focusVariantBarcode(variant.id);
      savingVariant = undefined;
      return;
    }
    try {
      let version = variant.version;
      let baseline = variant;
      if (barcodesChanged(variant, draft)) {
        const authoritativeProductID = productID ?? config?.product_id;
        if (!authoritativeProductID) {
          throw { code: 'VARIANT_PRODUCT_REQUIRED', message: 'Varyant ürünü bulunamadı.' };
        }
        if (variant.product_id && variant.product_id !== authoritativeProductID) {
          throw {
            code: 'VARIANT_PRODUCT_MISMATCH',
            message: 'Varyant başka bir ürüne ait görünüyor. Sayfayı yenileyin.'
          };
        }
        const updated = await updateProductVariantBarcodes(
          authoritativeProductID,
          variant.id,
          version,
          {
            barcodes: barcodeInput
          }
        );
        replaceRenderedVariant(updated);
        version = updated.version;
        baseline = { ...variant, ...updated };
      }
      if (otherVariantFieldsChanged(baseline, draft)) {
        if (!onVariantSave) {
          throw {
            code: 'VARIANT_UPDATE_UNAVAILABLE',
            message: 'Varyant güncelleme bağlantısı bulunamadı.'
          };
        }
        const { barcodes: _barcodes, ...variantInput } = draft;
        await onVariantSave(variant.id, version, variantInput);
      }
      const next = { ...drafts };
      delete next[variant.id];
      drafts = next;
    } catch (cause) {
      localError = readableError(cause, 'Varyant güncellenemedi.');
      setVariantError(variant.id, localError);
      if (
        typeof cause === 'object' &&
        cause &&
        'code' in cause &&
        variantBarcodeErrorMessage(
          String(cause.code),
          'message' in cause ? String(cause.message ?? '') : ''
        )
      ) {
        focusVariantBarcode(variant.id);
      }
    } finally {
      savingVariant = undefined;
    }
  }

  function readableError(cause: unknown, fallback: string) {
    if (typeof cause === 'object' && cause && 'code' in cause) {
      const code = String(cause.code);
      const message = 'message' in cause && cause.message ? String(cause.message) : '';
      if (code === 'FORBIDDEN' || code === 'PERMISSION_DENIED')
        return 'Bu işlem için yetkiniz yok.';
      const barcodeError = variantBarcodeErrorMessage(code, message);
      if (barcodeError) return barcodeError;
      if (code === 'CONFLICT' || code === 'VERSION_CONFLICT' || code === 'STALE_VERSION')
        return 'Kayıt başka bir kullanıcı tarafından değiştirildi. Yenileyip tekrar deneyin.';
      if (code === 'IDENTITY_LOCKED' || code === 'VARIANT_IDENTITY_LOCKED')
        return 'Bu varyantın kimliği ilk stok hareketinden sonra kilitlendi.';
      if (code === 'VARIANT_PRODUCT_REQUIRED')
        return 'Varyant ürünü bulunamadı. Sayfayı yenileyin.';
      if (code === 'VARIANT_PRODUCT_MISMATCH')
        return 'Varyant başka bir ürüne ait görünüyor. Sayfayı yenileyin.';
      if (message) return message;
    }
    return cause instanceof Error ? cause.message : fallback;
  }

  function effectivePrice(variant: ProductVariant, side: 'purchase' | 'sales') {
    const draft = draftFor(variant);
    const override =
      side === 'purchase' ? draft.purchase_price_override : draft.sales_price_override;
    const current = side === 'purchase' ? variant.purchase_price : variant.sales_price;
    const parent = side === 'purchase' ? parentPurchasePrice : parentSalesPrice;
    return String(override || current || parent || '0');
  }

  function variantLabel(variant: ProductVariant) {
    if (variant.values?.length) {
      return variant.values
        .map((value) => value.option_name || value.option_code || value.definition_name)
        .filter(Boolean)
        .join(' / ');
    }
    return Object.values(variant.attributes ?? {})
      .filter(Boolean)
      .join(' / ');
  }
</script>

<section class="variant-section" aria-labelledby="variant-mode-heading">
  <div class="section-heading">
    <div>
      <h2 id="variant-mode-heading">Varyant yönetimi</h2>
      <p>Renk, beden ve benzeri tanımlı seçeneklerle ürün matrisi oluşturun.</p>
    </div>
    {#if identityLocked}<span class="lock-badge"><LockKeyhole size={13} />Kimlik kilitli</span>{/if}
  </div>

  {#if !canManage}<p class="permission-note" role="status">
      Varyant tanımlarını ve kartlarını düzenleme yetkiniz yok.
    </p>{/if}

  {#if config?.enabled && productBarcodes.length > 0}
    <Alert.Root variant="destructive" class="variant-barcode-transition-alert">
      <AlertTriangle aria-hidden="true" />
      <Alert.Title>Ürün barkodları varyant modunda kullanılamaz</Alert.Title>
      <Alert.Description>
        Genel sekmesinden ana ürün barkodlarını kaldırın; ardından barkodları ilgili varyant
        kartlarına ekleyin.
      </Alert.Description>
    </Alert.Root>
  {/if}

  <label class="variant-mode-toggle">
    <input
      type="checkbox"
      checked={config?.enabled ?? false}
      disabled={disabled || variantDataLoading || !variantDataReady || !canManage || identityLocked}
      onchange={(event) => {
        if (config) config = { ...config, enabled: event.currentTarget.checked };
      }}
    />
    <span>
      <strong>Varyantlı ürün</strong>
      <small>Stok, barkod, fiyat ve hareketler varyant bazında izlenir.</small>
    </span>
  </label>

  {#if !variantDataReady}
    <div class="variant-data-error" role="alert">
      <strong>Varyant bilgileri yüklenemedi.</strong>
      <span>{error || 'Tanımlar ve mevcut varyantlar alınamadı.'}</span>
      <Button
        type="button"
        variant="outline"
        size="sm"
        disabled={variantDataLoading}
        onclick={() => onRetryData?.()}
        >{variantDataLoading ? 'Yükleniyor…' : 'Yeniden dene'}</Button
      >
    </div>
  {:else if config?.enabled}
    {#if productStock}
      <div class="aggregate-stock" aria-label="Ana ürün stok özeti">
        <div>
          <span>Fiziksel</span><strong
            >{formatQuantity(productStock.physical_quantity)}
            {productStock.stock_unit || ''}</strong
          >
        </div>
        <div>
          <span>Rezerve</span><strong
            >{formatQuantity(productStock.reserved_quantity)}
            {productStock.stock_unit || ''}</strong
          >
        </div>
        <div>
          <span>Kullanılabilir</span><strong
            >{formatQuantity(productStock.available_quantity)}
            {productStock.stock_unit || ''}</strong
          >
        </div>
      </div>
    {/if}
    <div class="variant-definition-picker">
      <div class="picker-heading">
        <strong>Varyant boyutları</strong>
        <span>{dimensions.length}/{MAX_VARIANT_DIMENSIONS}</span>
      </div>
      {#if activeDefinitions.length === 0}
        <p class="empty-note">
          Henüz aktif varyant boyutu yok. <a href="/ayarlar/tanimlar/varyantlar">Tanım ekleyin.</a>
        </p>
      {:else}
        <div class="definition-checkboxes">
          {#each activeDefinitions as definition}
            {@const selected = dimensions.some(
              (dimension) => dimension.definition_id === definition.id
            )}
            <label class:selected>
              <input
                type="checkbox"
                checked={selected}
                disabled={disabled || !canManage || identityLocked}
                onchange={(event) => toggleDefinition(definition.id, event.currentTarget.checked)}
              />
              <span>{definition.name}<small>{definition.code}</small></span>
            </label>
          {/each}
        </div>
      {/if}

      <div class="option-groups">
        {#each dimensions as dimension}
          {@const definition = definitionFor(dimension)}
          {#if definition}
            <fieldset>
              <legend>{definition.name}</legend>
              <div class="option-checkboxes">
                {#each definition.options.filter((option) => option.is_active || dimension.option_ids.includes(option.id)) as option}
                  <label>
                    <input
                      type="checkbox"
                      checked={dimension.option_ids.includes(option.id)}
                      disabled={disabled || !canManage || identityLocked}
                      onchange={(event) =>
                        toggleOption(definition.id, option.id, event.currentTarget.checked)}
                    />
                    <span>{option.name}<small>{option.code}</small></span>
                  </label>
                {/each}
              </div>
            </fieldset>
          {/if}
        {/each}
      </div>

      <div class="combination-summary" role="status" aria-live="polite">
        <div>
          <strong>{combinationCountLabel(selectedCount)}</strong>
        </div>
        {#if configWarning}<span class:error={Boolean(configError)} class="warning-text"
            ><AlertTriangle size={14} />{configWarning}</span
          >{/if}
      </div>
      {#if localError}<p class="variant-error" role="alert">{localError}</p>{/if}
      {#if error}<p class="variant-error" role="alert">{error}</p>{/if}
      {#if !persisted}
        <p class="empty-note">Ürünü kaydettikten sonra kombinasyonları üretebilirsiniz.</p>
      {:else}
        <Button
          type="button"
          variant="outline"
          disabled={disabled ||
            loading ||
            !canManage ||
            Boolean(configError) ||
            selectedCount === 0}
          onclick={() => onGenerate?.()}
          ><Plus size={14} />{loading ? 'Üretiliyor…' : generateLabel}</Button
        >
      {/if}
    </div>

    {#if renderedVariants.length === 0}
      <div class="variant-empty" role="status">
        <strong>Henüz varyant yok</strong>
        <span>Boyut ve seçenekleri seçip eksik kombinasyonları üretin.</span>
      </div>
    {:else}
      <div class="variant-table-wrap">
        <table class="variant-table">
          <caption>Varyant matrisi</caption>
          <thead>
            <tr>
              <th scope="col">Özellikler</th>
              <th scope="col">SKU</th>
              <th scope="col">Barkod</th>
              <th scope="col">Alış fiyatı</th>
              <th scope="col">Satış fiyatı</th>
              <th scope="col">Stok</th>
              <th scope="col">Durum</th>
              <th scope="col"><span class="sr-only">İşlem</span></th>
            </tr>
          </thead>
          <tbody>
            {#each renderedVariants as variant (variant.id)}
              {@const draft = draftFor(variant)}
              {@const locked = Boolean(variant.identity_locked)}
              {@const barcodeError = variantErrors[variant.id]}
              <tr>
                <th scope="row">
                  <span class="variant-name">{variantLabel(variant) || 'Tanımsız varyant'}</span>
                  {#if locked}<small class="inline-lock"
                      ><LockKeyhole size={11} />Kimlik kilitli</small
                    >{/if}
                </th>
                <td
                  ><Input
                    value={draft.variant_code ?? ''}
                    disabled={disabled || !canManage || locked}
                    aria-label={`${variantLabel(variant)} SKU`}
                    oninput={(event) =>
                      updateDraft(variant, { variant_code: event.currentTarget.value })}
                  /></td
                >
                <td>
                  <div class="barcode-editor" aria-label={`${variantLabel(variant)} barkodları`}>
                    {#if (draft.barcodes ?? []).length === 0}
                      <small class="inherit-note">Barkod tanımlanmadı.</small>
                    {/if}
                    {#each draft.barcodes ?? [] as barcode, barcodeIndex (barcode.id ?? `${variant.id}-${barcodeIndex}`)}
                      <div class="barcode-editor-row">
                        <Input
                          id={variantBarcodeInputID(variant.id, barcodeIndex)}
                          value={barcode.barcode}
                          disabled={disabled || !canManage}
                          placeholder="Barkod"
                          aria-label={`${variantLabel(variant)} barkod ${barcodeIndex + 1}`}
                          aria-invalid={barcodeError ? 'true' : undefined}
                          aria-describedby={barcodeError
                            ? `variant-${variant.id}-barcode-error`
                            : undefined}
                          oninput={(event) =>
                            updateVariantBarcode(variant, barcodeIndex, {
                              barcode: event.currentTarget.value
                            })}
                        />
                        <select
                          value={barcode.barcode_type}
                          disabled={disabled || !canManage}
                          aria-label={`${variantLabel(variant)} barkod ${barcodeIndex + 1} türü`}
                          onchange={(event) =>
                            updateVariantBarcode(variant, barcodeIndex, {
                              barcode_type: event.currentTarget.value
                            })}
                        >
                          <option value="EAN">EAN</option>
                          <option value="UPC">UPC</option>
                          <option value="CODE128">Code 128</option>
                          <option value="OTHER">Diğer</option>
                        </select>
                        <label class="primary-barcode-choice">
                          <input
                            type="radio"
                            name={`${variant.id}-primary-barcode`}
                            checked={barcode.is_primary}
                            disabled={disabled || !canManage}
                            aria-label={`${variantLabel(variant)} barkod ${barcodeIndex + 1} ana barkod`}
                            onchange={() =>
                              updateVariantBarcode(variant, barcodeIndex, { is_primary: true })}
                          />
                          <span>Ana</span>
                        </label>
                        <Button
                          type="button"
                          variant="ghost"
                          size="icon"
                          disabled={disabled || !canManage}
                          aria-label={`${variantLabel(variant)} barkod ${barcodeIndex + 1} sil`}
                          onclick={() => removeVariantBarcode(variant, barcodeIndex)}
                          ><Trash2 size={13} /></Button
                        >
                      </div>
                    {/each}
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      disabled={disabled || !canManage}
                      aria-label={`${variantLabel(variant)} barkod ekle`}
                      onclick={() => addVariantBarcode(variant)}
                      ><Plus size={13} />Barkod ekle</Button
                    >
                    {#if barcodeError}<p
                        id={`variant-${variant.id}-barcode-error`}
                        class="field-error"
                        role="alert"
                      >
                        {barcodeError}
                      </p>{/if}
                  </div>
                </td>
                <td>
                  <Input
                    value={draft.purchase_price_override ?? ''}
                    disabled={disabled || !canManage}
                    inputmode="decimal"
                    placeholder={formatMoney(parentPurchasePrice || '0', baseCurrency)}
                    aria-label={`${variantLabel(variant)} alış fiyatı`}
                    oninput={(event) =>
                      updateVariantPrice(variant, 'purchase', event.currentTarget.value)}
                    onblur={() => compactVariantPrice(variant, 'purchase')}
                  />
                  {#if !draft.purchase_price_override}<small class="inherit-note"
                      >Üründen miras: {formatMoney(
                        effectivePrice(variant, 'purchase'),
                        baseCurrency
                      )}</small
                    >{/if}
                </td>
                <td>
                  <Input
                    value={draft.sales_price_override ?? ''}
                    disabled={disabled || !canManage}
                    inputmode="decimal"
                    placeholder={formatMoney(parentSalesPrice || '0', baseCurrency)}
                    aria-label={`${variantLabel(variant)} satış fiyatı`}
                    oninput={(event) =>
                      updateVariantPrice(variant, 'sales', event.currentTarget.value)}
                    onblur={() => compactVariantPrice(variant, 'sales')}
                  />
                  {#if !draft.sales_price_override}<small class="inherit-note"
                      >Üründen miras: {formatMoney(
                        effectivePrice(variant, 'sales'),
                        baseCurrency
                      )}</small
                    >{/if}
                </td>
                <td>
                  <span class="stock-summary"
                    >{formatQuantity(variant.available_quantity || '0')}
                    {variant.stock_unit || productStock?.stock_unit || ''}</span
                  >
                  {#if variant.stock_positions?.length}
                    <details class="stock-details">
                      <summary><ChevronDown size={12} />Depo detayı</summary>
                      <div class="stock-position-list">
                        {#each variant.stock_positions as position}
                          <div>
                            <span
                              >{position.warehouse_name ||
                                position.warehouse_code ||
                                'Depo'}{#if position.location_name}
                                · {position.location_name}{/if}</span
                            ><strong
                              >{formatQuantity(position.available_quantity)}
                              {position.stock_unit || variant.stock_unit || ''}</strong
                            >
                          </div>
                        {/each}
                      </div>
                    </details>
                  {/if}
                </td>
                <td>
                  <label class="status-toggle"
                    ><input
                      type="checkbox"
                      checked={draft.is_active ?? variant.is_active}
                      disabled={disabled || !canManage}
                      onchange={(event) =>
                        updateDraft(variant, { is_active: event.currentTarget.checked })}
                    /><span>{(draft.is_active ?? variant.is_active) ? 'Aktif' : 'Pasif'}</span
                    ></label
                  >
                </td>
                <td
                  ><Button
                    type="button"
                    size="sm"
                    variant="outline"
                    disabled={disabled || !canManage || savingVariant === variant.id}
                    onclick={() => void saveVariant(variant)}
                    ><Save size={13} />{savingVariant === variant.id
                      ? 'Kaydediliyor…'
                      : 'Kaydet'}</Button
                  ></td
                >
              </tr>
            {/each}
          </tbody>
        </table>
      </div>

      {#if variantPriceLists.length > 0}
        <section class="variant-price-matrix" aria-labelledby="variant-price-matrix-heading">
          <div class="section-heading compact-heading">
            <div>
              <h3 id="variant-price-matrix-heading">Varyant fiyat matrisi</h3>
              <p>Fiyat listesinde boş bırakılan satır ürün fiyatına döner.</p>
            </div>
          </div>
          <div class="variant-price-table-wrap">
            <table class="variant-price-table">
              <caption>Varyant fiyat listesi değerleri</caption>
              <thead
                ><tr
                  ><th scope="col">Varyant</th>{#each variantPriceLists as list}<th scope="col"
                      >{list.list_name}<small>{list.list_code}</small></th
                    >{/each}</tr
                ></thead
              >
              <tbody>
                {#each renderedVariants as variant (variant.id)}
                  <tr
                    ><th scope="row">{variant.variant_code}</th>{#each variantPriceLists as list}<td
                        ><Input
                          value={draftPrice(variant, list.price_list_id)}
                          disabled={disabled || !canManage}
                          inputmode="decimal"
                          aria-label={`${variant.variant_code} ${list.list_name} fiyatı`}
                          oninput={(event) =>
                            updatePriceListValue(
                              variant,
                              list.price_list_id,
                              event.currentTarget.value
                            )}
                          onblur={() => compactPriceListValue(variant, list.price_list_id)}
                        /></td
                      >{/each}</tr
                  >
                {/each}
              </tbody>
            </table>
          </div>
          <p class="matrix-note">
            <Check size={13} />Değişiklikleri ilgili varyant satırındaki Kaydet düğmesiyle kaydedin.
          </p>
        </section>
      {/if}
    {/if}
  {/if}
</section>

<style>
  .variant-section {
    padding: 18px 20px;
    border-bottom: 1px solid var(--border);
  }
  .variant-data-error {
    display: grid;
    gap: 7px;
    margin-top: 16px;
    padding: 12px;
    border: 1px solid color-mix(in srgb, var(--danger) 35%, var(--border));
    border-radius: var(--radius-control);
    background: color-mix(in srgb, var(--danger) 6%, var(--surface));
  }
  :global(.variant-barcode-transition-alert) {
    margin-top: 16px;
  }
  .variant-data-error span {
    color: var(--text-muted);
    font-size: 11px;
  }
  .section-heading,
  .picker-heading,
  .compact-heading {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 12px;
  }
  h2,
  h3 {
    margin: 0;
    font-size: 14px;
  }
  h3 {
    font-size: 12px;
  }
  p {
    margin: 4px 0 0;
    color: var(--text-muted);
    font-size: 11px;
  }
  .lock-badge,
  .inline-lock,
  .matrix-note {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    color: var(--text-muted);
    font-size: 10px;
  }
  .variant-mode-toggle {
    display: flex;
    align-items: flex-start;
    gap: 8px;
    margin-top: 16px;
    padding: 11px;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    background: var(--surface-muted);
  }
  .variant-mode-toggle span {
    display: grid;
    gap: 2px;
  }
  .variant-mode-toggle small,
  .definition-checkboxes small,
  .option-checkboxes small,
  .inherit-note,
  .variant-price-table small {
    display: block;
    color: var(--text-muted);
    font-size: 10px;
  }
  .variant-definition-picker {
    display: grid;
    gap: 12px;
    margin-top: 14px;
  }
  .aggregate-stock {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 8px;
    margin-top: 14px;
  }
  .aggregate-stock div {
    display: grid;
    gap: 3px;
    padding: 10px;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    background: var(--surface-muted);
  }
  .aggregate-stock span {
    color: var(--text-muted);
    font-size: 10px;
  }
  .aggregate-stock strong {
    font-size: 14px;
  }
  .picker-heading {
    align-items: center;
    color: var(--text-muted);
    font-size: 11px;
  }
  .definition-checkboxes,
  .option-checkboxes {
    display: flex;
    flex-wrap: wrap;
    gap: 7px;
  }
  .definition-checkboxes label,
  .option-checkboxes label {
    display: inline-flex;
    align-items: flex-start;
    gap: 7px;
    padding: 8px 9px;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    background: var(--surface);
    font-size: 11px;
  }
  .definition-checkboxes label.selected {
    border-color: var(--primary);
    background: color-mix(in srgb, var(--primary) 6%, var(--surface));
  }
  .definition-checkboxes span,
  .option-checkboxes span {
    display: grid;
    gap: 2px;
  }
  fieldset {
    min-width: 0;
    margin: 0;
    padding: 10px;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
  }
  legend {
    padding: 0 4px;
    color: var(--text);
    font-size: 11px;
    font-weight: 700;
  }
  .option-groups {
    display: grid;
    gap: 8px;
  }
  .combination-summary {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 10px;
    padding: 10px;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    background: var(--surface-muted);
  }
  .combination-summary div {
    display: grid;
    gap: 2px;
  }
  .combination-summary span:not(.warning-text) {
    color: var(--text-muted);
    font-size: 10px;
  }
  .warning-text {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    color: var(--warning, #a16207);
    font-size: 10px;
  }
  .warning-text.error,
  .variant-error {
    color: var(--danger);
  }
  .variant-error {
    margin: 0;
  }
  .empty-note,
  .permission-note,
  .variant-empty {
    padding: 10px;
    border: 1px dashed var(--border);
    border-radius: var(--radius-control);
  }
  .empty-note a {
    color: var(--primary);
  }
  .variant-empty {
    display: grid;
    gap: 2px;
    margin-top: 14px;
  }
  .variant-table-wrap,
  .variant-price-table-wrap {
    overflow-x: auto;
    margin-top: 14px;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
  }
  table {
    width: 100%;
    border-collapse: collapse;
    font-size: 11px;
  }
  caption {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip: rect(0 0 0 0);
  }
  th,
  td {
    min-width: 110px;
    padding: 8px;
    border-bottom: 1px solid var(--border);
    text-align: left;
    vertical-align: top;
  }
  th {
    color: var(--text-muted);
    font-size: 10px;
    font-weight: 700;
    white-space: nowrap;
  }
  tbody th {
    color: var(--text);
    font-weight: 600;
  }
  tbody tr:last-child th,
  tbody tr:last-child td {
    border-bottom: 0;
  }
  .variant-name {
    display: block;
    min-width: 120px;
    margin-bottom: 3px;
    color: var(--text);
  }
  .barcode-editor {
    display: grid;
    gap: 5px;
    min-width: 310px;
  }
  .barcode-editor-row {
    display: grid;
    grid-template-columns: minmax(120px, 1fr) 92px auto 28px;
    align-items: center;
    gap: 4px;
  }
  .barcode-editor-row select {
    min-height: 32px;
    padding: 0 5px;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    background: var(--surface);
    color: var(--text);
    font: inherit;
    font-size: 11px;
  }
  .primary-barcode-choice {
    display: inline-flex;
    align-items: center;
    gap: 3px;
    white-space: nowrap;
    color: var(--text-muted);
    font-size: 10px;
  }
  .field-error {
    margin: 0;
    color: var(--danger);
    font-size: 10px;
  }
  .inline-lock {
    color: var(--text-muted);
  }
  .stock-summary {
    display: block;
    min-width: 90px;
    white-space: nowrap;
  }
  .stock-details {
    margin-top: 5px;
  }
  .stock-details summary {
    display: inline-flex;
    align-items: center;
    gap: 2px;
    color: var(--primary);
    cursor: pointer;
    font-size: 10px;
    list-style: none;
  }
  .stock-details summary::-webkit-details-marker {
    display: none;
  }
  .stock-position-list {
    display: grid;
    gap: 4px;
    min-width: 180px;
    margin-top: 5px;
    padding: 6px;
    border: 1px solid var(--border);
    background: var(--surface-muted);
  }
  .stock-position-list div {
    display: flex;
    justify-content: space-between;
    gap: 8px;
  }
  .stock-position-list span {
    color: var(--text-muted);
  }
  .status-toggle {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    white-space: nowrap;
  }
  .variant-price-matrix {
    display: grid;
    gap: 8px;
    margin-top: 20px;
  }
  .variant-price-table th,
  .variant-price-table td {
    min-width: 150px;
  }
  .variant-price-table th small {
    margin-top: 2px;
    font-weight: 400;
  }
  .matrix-note {
    margin-top: 0;
  }
  .sr-only {
    position: absolute;
    width: 1px;
    height: 1px;
    padding: 0;
    margin: -1px;
    overflow: hidden;
    clip: rect(0, 0, 0, 0);
    white-space: nowrap;
    border: 0;
  }
  @media (max-width: 700px) {
    .combination-summary {
      align-items: flex-start;
      flex-direction: column;
    }
    .aggregate-stock {
      grid-template-columns: 1fr;
    }
  }
</style>
