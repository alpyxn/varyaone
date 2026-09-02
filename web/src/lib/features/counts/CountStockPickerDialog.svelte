<script lang="ts">
  import { ChevronDown, ChevronRight, LoaderCircle, Search, X } from '@lucide/svelte';
  import { onDestroy, untrack } from 'svelte';
  import {
    listProductBrands,
    listProductCategories,
    listProductVariants,
    listProducts
  } from '$lib/features/products/api';
  import type {
    Product,
    ProductBrand,
    ProductCategory,
    ProductVariant
  } from '$lib/features/products/types';
  import { formatQuantity } from '$lib/design/formatters';
  import { addDecimalStrings } from '$lib/design/decimal';

  export type CountStockPickerSelection = {
    productID: string;
    variantID?: string;
    productName: string;
    productCode: string;
    variantLabel?: string;
    barcode?: string;
    availableQuantity: string;
    unit?: string;
  };

  type SelectableRow = {
    key: string;
    selection: CountStockPickerSelection;
  };

  let {
    open = $bindable(false),
    warehouseID,
    onSelect
  }: {
    open?: boolean;
    warehouseID: string;
    onSelect?: (selection: CountStockPickerSelection) => void;
  } = $props();

  let products = $state<Product[]>([]);
  let categories = $state<ProductCategory[]>([]);
  let brands = $state<ProductBrand[]>([]);
  let variantsByProduct = $state<Record<string, ProductVariant[]>>({});
  let expandedProducts = $state<Record<string, boolean>>({});
  let variantsLoading = $state<Record<string, boolean>>({});
  let variantsError = $state<Record<string, string>>({});
  let search = $state('');
  let categoryID = $state('');
  let brandID = $state('');
  let loading = $state(false);
  let loadingReferences = $state(false);
  let error = $state('');
  let referenceError = $state('');
  let activeIndex = $state(0);
  let searchInput = $state<HTMLInputElement>();
  let dialogElement = $state<HTMLDivElement>();
  let previousFocus: HTMLElement | null = null;
  let productRequest: AbortController | undefined;
  let referenceRequest: AbortController | undefined;
  let variantRequests: Record<string, AbortController> = {};
  let requestSequence = 0;
  let searchTimer: ReturnType<typeof setTimeout> | undefined;

  const selectableRows = $derived<SelectableRow[]>(
    products.flatMap((product) => {
      if (!product.variants_enabled) {
        return [{ key: product.id, selection: productSelection(product) }];
      }
      if (!expandedProducts[product.id]) return [];
      return (variantsByProduct[product.id] ?? [])
        .filter((variant) => variant.is_active)
        .map((variant) => ({
          key: `${product.id}:${variant.id}`,
          selection: variantSelection(product, variant)
        }));
    })
  );
  const activeRow = $derived(selectableRows[activeIndex]);

  $effect(() => {
    if (activeIndex >= selectableRows.length) {
      activeIndex = Math.max(0, selectableRows.length - 1);
    }
  });

  $effect(() => {
    if (!open) {
      productRequest?.abort();
      referenceRequest?.abort();
      for (const controller of Object.values(variantRequests)) controller.abort();
      return;
    }

    previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const focusTimer = setTimeout(() => searchInput?.focus(), 0);
    untrack(() => {
      void loadReferences();
      runSearchSoon(0);
    });
    return () => clearTimeout(focusTimer);
  });

  onDestroy(() => {
    productRequest?.abort();
    referenceRequest?.abort();
    for (const controller of Object.values(variantRequests)) controller.abort();
    if (searchTimer) clearTimeout(searchTimer);
  });

  function messageFrom(errorValue: unknown, fallback: string) {
    return errorValue instanceof Error && errorValue.message ? errorValue.message : fallback;
  }

  async function loadReferences() {
    if (loadingReferences || (categories.length > 0 && brands.length > 0)) return;
    referenceRequest?.abort();
    const controller = new AbortController();
    referenceRequest = controller;
    loadingReferences = true;
    referenceError = '';
    try {
      const [categoryResult, brandResult] = await Promise.all([
        listProductCategories(controller.signal),
        listProductBrands(controller.signal)
      ]);
      if (controller.signal.aborted) return;
      categories = categoryResult.filter((item) => item.is_active);
      brands = brandResult.filter((item) => item.is_active);
    } catch (cause) {
      if (!controller.signal.aborted) referenceError = messageFrom(cause, 'Filtreler alınamadı.');
    } finally {
      if (!controller.signal.aborted) loadingReferences = false;
    }
  }

  function runSearchSoon(delay = 220) {
    if (searchTimer) clearTimeout(searchTimer);
    searchTimer = setTimeout(() => void searchProducts(), delay);
  }

  async function searchProducts() {
    const sequence = ++requestSequence;
    productRequest?.abort();
    const controller = new AbortController();
    productRequest = controller;
    loading = true;
    error = '';
    try {
      const params = new URLSearchParams({ warehouse_id: warehouseID, limit: '50' });
      if (search.trim()) params.set('q', search.trim());
      if (categoryID) params.set('category_id', categoryID);
      if (brandID) params.set('brand_id', brandID);
      const result = await listProducts(params, controller.signal);
      if (controller.signal.aborted || sequence !== requestSequence) return;
      products = result.items.filter((product) => product.kind === 'PHYSICAL' && product.is_active);
      activeIndex = 0;
      expandedProducts = {};
      variantsByProduct = {};
      variantsError = {};
    } catch (cause) {
      if (!controller.signal.aborted && sequence === requestSequence) {
        error = messageFrom(cause, 'Stok kartları alınamadı.');
        products = [];
      }
    } finally {
      if (!controller.signal.aborted && sequence === requestSequence) loading = false;
    }
  }

  async function toggleVariants(product: Product) {
    const nextExpanded = !expandedProducts[product.id];
    expandedProducts = { ...expandedProducts, [product.id]: nextExpanded };
    activeIndex = 0;
    if (!nextExpanded || variantsByProduct[product.id]) return;

    variantRequests[product.id]?.abort();
    const controller = new AbortController();
    variantRequests = { ...variantRequests, [product.id]: controller };
    variantsLoading = { ...variantsLoading, [product.id]: true };
    variantsError = { ...variantsError, [product.id]: '' };
    try {
      const result = await listProductVariants(product.id, controller.signal);
      if (controller.signal.aborted) return;
      variantsByProduct = {
        ...variantsByProduct,
        [product.id]: result.items.filter((variant) => variant.is_active)
      };
    } catch (cause) {
      if (!controller.signal.aborted) {
        variantsError = {
          ...variantsError,
          [product.id]: messageFrom(cause, 'Varyantlar alınamadı.')
        };
      }
    } finally {
      if (!controller.signal.aborted) {
        variantsLoading = { ...variantsLoading, [product.id]: false };
      }
    }
  }

  function productSelection(product: Product): CountStockPickerSelection {
    return {
      productID: product.id,
      productName: product.name,
      productCode: product.code,
      barcode: primaryBarcode(product.barcodes),
      availableQuantity: product.available_quantity || '0',
      unit: product.stock_unit
    };
  }

  function variantSelection(product: Product, variant: ProductVariant): CountStockPickerSelection {
    const positions = (variant.stock_positions ?? []).filter(
      (item) => item.warehouse_id === warehouseID
    );
    const warehouseAvailable = positions.reduce(
      (total, item) => addDecimalStrings(total, item.available_quantity),
      '0'
    );
    return {
      productID: product.id,
      variantID: variant.id,
      productName: product.name,
      productCode: product.code,
      variantLabel: variantLabel(variant),
      barcode: primaryBarcode(variant.barcodes),
      availableQuantity:
        positions.length > 0 ? warehouseAvailable : (variant.available_quantity ?? '0'),
      unit:
        positions.find((item) => item.stock_unit)?.stock_unit ||
        variant.stock_unit ||
        product.stock_unit
    };
  }

  function primaryBarcode(barcodes: { barcode: string; is_primary: boolean }[] | undefined) {
    return barcodes?.find((barcode) => barcode.is_primary)?.barcode || barcodes?.[0]?.barcode;
  }

  function variantLabel(variant: ProductVariant) {
    const values = (variant.values ?? [])
      .map((value) => value.option_name || value.option_code)
      .filter(Boolean);
    return values.length > 0 ? values.join(' / ') : variant.variant_code;
  }

  function selectRow(row: SelectableRow) {
    onSelect?.(row.selection);
    open = false;
  }

  function handleSearchKeydown(event: KeyboardEvent) {
    if (event.key === 'Enter') {
      event.preventDefault();
      if (activeRow) selectRow(activeRow);
      else void searchProducts();
      return;
    }
    if (!selectableRows.length) return;
    if (event.key === 'ArrowDown') {
      event.preventDefault();
      activeIndex = Math.min(selectableRows.length - 1, activeIndex + 1);
    } else if (event.key === 'ArrowUp') {
      event.preventDefault();
      activeIndex = Math.max(0, activeIndex - 1);
    }
  }

  function closeDialog() {
    open = false;
    setTimeout(() => previousFocus?.focus(), 0);
  }

  function handleWindowKeydown(event: KeyboardEvent) {
    if (!open) return;
    if (event.key === 'Escape') {
      event.preventDefault();
      closeDialog();
      return;
    }
    if (event.key !== 'Tab' || !dialogElement) return;
    const focusable = Array.from(
      dialogElement.querySelectorAll<HTMLElement>(
        'button:not(:disabled), input:not(:disabled), select:not(:disabled), [tabindex]:not([tabindex="-1"])'
      )
    );
    if (!focusable.length) return;
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
</script>

<svelte:window onkeydown={handleWindowKeydown} />

{#if open}
  <div
    class="picker-overlay"
    role="presentation"
    onclick={(event) => event.target === event.currentTarget && closeDialog()}
  >
    <div
      bind:this={dialogElement}
      class="picker-dialog"
      role="dialog"
      aria-modal="true"
      aria-labelledby="count-stock-picker-title"
      aria-describedby="count-stock-picker-description"
    >
      <header class="dialog-header">
        <div>
          <h2 id="count-stock-picker-title">Stok seç</h2>
          <p id="count-stock-picker-description" class="description">
            Sayıma eklenecek stok kartını veya varyantı seçin.
          </p>
        </div>
        <button class="icon-button" type="button" aria-label="Kapat" onclick={closeDialog}>
          <X size={19} aria-hidden="true" />
        </button>
      </header>

      <div class="toolbar">
        <div class="search-field">
          <Search size={18} aria-hidden="true" />
          <input
            bind:this={searchInput}
            bind:value={search}
            type="search"
            placeholder="Stok kodu, adı veya barkod ara"
            aria-label="Stok kodu, adı veya barkod ara"
            aria-controls="count-stock-picker-results"
            oninput={() => runSearchSoon()}
            onkeydown={handleSearchKeydown}
          />
          {#if search}
            <button
              class="clear-search"
              type="button"
              aria-label="Aramayı temizle"
              onclick={() => {
                search = '';
                runSearchSoon(0);
              }}
            >
              <X size={15} aria-hidden="true" />
            </button>
          {/if}
        </div>
        <div class="filter-row" aria-label="Stok filtreleri">
          <label>
            <span>Kategori</span>
            <select bind:value={categoryID} onchange={() => runSearchSoon(0)}>
              <option value="">Tüm kategoriler</option>
              {#each categories as category (category.id)}
                <option value={category.id}>{category.name}</option>
              {/each}
            </select>
          </label>
          <label>
            <span>Marka</span>
            <select bind:value={brandID} onchange={() => runSearchSoon(0)}>
              <option value="">Tüm markalar</option>
              {#each brands as brand (brand.id)}
                <option value={brand.id}>{brand.name}</option>
              {/each}
            </select>
          </label>
        </div>
      </div>

      {#if referenceError}
        <p class="inline-warning" role="status">
          {referenceError} Filtreler olmadan devam edebilirsiniz.
        </p>
      {/if}

      <div
        id="count-stock-picker-results"
        class="results"
        role="listbox"
        aria-label="Stok sonuçları"
      >
        {#if loading}
          <div class="state" role="status" aria-live="polite">
            <LoaderCircle class="spin" size={23} aria-hidden="true" />
            <span>Stok kartları aranıyor…</span>
          </div>
        {:else if error}
          <div class="state error-state" role="alert">
            <strong>Sonuçlar alınamadı</strong>
            <span>{error}</span>
            <button type="button" class="retry" onclick={() => void searchProducts()}
              >Tekrar dene</button
            >
          </div>
        {:else if products.length === 0}
          <div class="state" role="status" aria-live="polite">
            <Search size={22} aria-hidden="true" />
            <strong>Eşleşen stok bulunamadı</strong>
            <span>Arama veya filtreleri değiştirip tekrar deneyin.</span>
          </div>
        {:else}
          {#each products as product (product.id)}
            {#if product.variants_enabled}
              <section
                class="variant-group"
                role="group"
                aria-label={`${product.name} varyantları`}
              >
                <button
                  class="variant-group-toggle"
                  type="button"
                  aria-expanded={Boolean(expandedProducts[product.id])}
                  onclick={() => void toggleVariants(product)}
                >
                  {#if expandedProducts[product.id]}<ChevronDown
                      size={17}
                      aria-hidden="true"
                    />{:else}<ChevronRight size={17} aria-hidden="true" />{/if}
                  <span class="product-heading">
                    <strong>{product.name}</strong>
                    <small>{product.code} · Varyantlı stok</small>
                  </span>
                  <span class="variant-count"
                    >{product.variant_summary?.active_count ?? 0} varyant</span
                  >
                </button>

                {#if expandedProducts[product.id]}
                  {#if variantsLoading[product.id]}
                    <div class="substate" role="status">
                      <LoaderCircle class="spin" size={17} /> Varyantlar yükleniyor…
                    </div>
                  {:else if variantsError[product.id]}
                    <div class="substate error-state" role="alert">{variantsError[product.id]}</div>
                  {:else if (variantsByProduct[product.id] ?? []).length === 0}
                    <div class="substate">Aktif varyant bulunamadı.</div>
                  {:else}
                    {#each variantsByProduct[product.id] ?? [] as variant (variant.id)}
                      {@const row = {
                        key: `${product.id}:${variant.id}`,
                        selection: variantSelection(product, variant)
                      }}
                      {@const availability = row.selection.availableQuantity}
                      <button
                        id={`count-stock-option-${row.key}`}
                        class:selected={activeRow?.key === row.key}
                        class="stock-row variant-row"
                        type="button"
                        role="option"
                        aria-selected={activeRow?.key === row.key}
                        onclick={() => selectRow(row)}
                        onmouseenter={() =>
                          (activeIndex = selectableRows.findIndex((item) => item.key === row.key))}
                      >
                        <span class="row-copy">
                          <strong>{product.name}</strong>
                          <small>{product.code} · {row.selection.variantLabel}</small>
                          {#if row.selection.barcode}<small class="barcode"
                              >Barkod: {row.selection.barcode}</small
                            >{/if}
                        </span>
                        <span
                          class="availability"
                          aria-label={`Kullanılabilir ${formatQuantity(availability)} ${row.selection.unit || ''}`}
                        >
                          <small>Kullanılabilir</small>
                          <strong>{formatQuantity(availability)}</strong>
                          <small>{row.selection.unit || 'Birim'}</small>
                        </span>
                      </button>
                    {/each}
                  {/if}
                {/if}
              </section>
            {:else}
              {@const row = { key: product.id, selection: productSelection(product) }}
              {@const availability = row.selection.availableQuantity}
              <button
                id={`count-stock-option-${row.key}`}
                class:selected={activeRow?.key === row.key}
                class="stock-row"
                type="button"
                role="option"
                aria-selected={activeRow?.key === row.key}
                onclick={() => selectRow(row)}
                onmouseenter={() =>
                  (activeIndex = selectableRows.findIndex((item) => item.key === row.key))}
              >
                <span class="row-copy">
                  <strong>{product.name}</strong>
                  <small>{product.code} · Varyantsız stok</small>
                  {#if row.selection.barcode}<small class="barcode"
                      >Barkod: {row.selection.barcode}</small
                    >{/if}
                </span>
                <span
                  class="availability"
                  aria-label={`Kullanılabilir ${formatQuantity(availability)} ${row.selection.unit || ''}`}
                >
                  <small>Kullanılabilir</small>
                  <strong>{formatQuantity(availability)}</strong>
                  <small>{row.selection.unit || 'Birim'}</small>
                </span>
              </button>
            {/if}
          {/each}
        {/if}
      </div>

      <footer class="dialog-footer">
        <span
          >{products.length} stok kartı · Varyantlı kartlarda seçim varyant satırından yapılır.</span
        >
        <button type="button" class="cancel-button" onclick={closeDialog}>Vazgeç</button>
      </footer>
    </div>
  </div>
{/if}

<style>
  .picker-overlay {
    position: fixed;
    inset: 0;
    z-index: 300;
    background: rgb(2 6 23 / 54%);
  }

  .picker-dialog {
    position: fixed;
    top: 7vh;
    left: 50%;
    display: flex;
    width: min(760px, calc(100vw - 28px));
    max-height: 86vh;
    transform: translateX(-50%);
    flex-direction: column;
    overflow: hidden;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-panel);
    background: var(--surface);
    box-shadow: 0 24px 80px rgb(10 30 27 / 28%);
    color: var(--text);
  }

  .dialog-header,
  .dialog-footer {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 16px;
    padding: 18px 20px;
  }

  .dialog-header {
    border-bottom: 1px solid var(--border);
  }
  .dialog-footer {
    align-items: center;
    border-top: 1px solid var(--border);
    color: var(--text-muted);
    font-size: 11px;
  }
  .dialog-footer span {
    min-width: 0;
  }

  h2 {
    margin: 0;
    font-size: 18px;
  }
  .description {
    margin: 4px 0 0;
    color: var(--text-muted);
    font-size: 12px;
  }

  .icon-button,
  .clear-search {
    display: grid;
    place-items: center;
    border: 0;
    border-radius: var(--radius-control);
    background: transparent;
    color: var(--text-muted);
    cursor: pointer;
  }
  .icon-button {
    width: 32px;
    height: 32px;
  }
  .icon-button:hover,
  .clear-search:hover {
    background: var(--surface-muted);
    color: var(--text);
  }

  .toolbar {
    display: grid;
    gap: 10px;
    padding: 14px 20px 12px;
  }
  .search-field {
    display: flex;
    align-items: center;
    gap: 9px;
    min-height: 42px;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    background: var(--surface);
    padding: 0 11px;
    color: var(--text-muted);
  }
  .search-field:focus-within {
    border-color: var(--primary);
    box-shadow: 0 0 0 3px var(--focus);
  }
  .search-field input {
    min-width: 0;
    flex: 1;
    border: 0;
    outline: 0;
    background: transparent;
    color: var(--text);
    font-size: 13px;
  }
  .search-field input::placeholder {
    color: var(--text-muted);
  }
  .clear-search {
    width: 26px;
    height: 26px;
  }
  .filter-row {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 10px;
  }
  label {
    display: grid;
    gap: 4px;
    color: var(--text-muted);
    font-size: 11px;
  }
  select {
    width: 100%;
    min-height: 34px;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    background: var(--surface);
    color: var(--text);
    padding: 0 9px;
    font-size: 12px;
  }

  .inline-warning {
    margin: 0 20px 10px;
    border-radius: var(--radius-control);
    background: var(--warning-soft);
    color: var(--warning);
    padding: 8px 10px;
    font-size: 11px;
  }
  .results {
    min-height: 260px;
    overflow: auto;
    padding: 0 20px 14px;
  }
  .state {
    display: grid;
    place-items: center;
    gap: 7px;
    min-height: 260px;
    color: var(--text-muted);
    text-align: center;
    font-size: 12px;
  }
  .state strong {
    color: var(--text);
    font-size: 13px;
  }
  .error-state {
    color: var(--danger);
  }
  .error-state strong {
    color: var(--danger);
  }
  .retry {
    border: 0;
    background: transparent;
    color: var(--primary);
    cursor: pointer;
    font-size: 12px;
    font-weight: 700;
  }
  :global(.spin) {
    animation: spin 1s linear infinite;
  }
  @keyframes spin {
    to {
      transform: rotate(360deg);
    }
  }

  .variant-group {
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    background: var(--surface);
  }
  .variant-group + .variant-group,
  .variant-group + .stock-row,
  .stock-row + .variant-group {
    margin-top: 8px;
  }
  .variant-group-toggle {
    display: flex;
    align-items: center;
    width: 100%;
    gap: 8px;
    border: 0;
    background: transparent;
    color: var(--text);
    padding: 11px 12px;
    text-align: left;
    cursor: pointer;
  }
  .variant-group-toggle:hover {
    background: var(--surface-muted);
  }
  .product-heading {
    min-width: 0;
    flex: 1;
  }
  .product-heading strong,
  .product-heading small {
    display: block;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .product-heading strong {
    font-size: 13px;
  }
  .product-heading small {
    margin-top: 2px;
    color: var(--text-muted);
    font-size: 11px;
  }
  .variant-count {
    flex: 0 0 auto;
    color: var(--text-muted);
    font-size: 11px;
  }
  .substate {
    display: flex;
    align-items: center;
    gap: 8px;
    border-top: 1px solid var(--border);
    padding: 12px 14px;
    color: var(--text-muted);
    font-size: 11px;
  }

  .stock-row {
    display: flex;
    align-items: center;
    width: 100%;
    gap: 14px;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    background: var(--surface);
    color: var(--text);
    padding: 11px 12px;
    text-align: left;
    cursor: pointer;
  }
  .stock-row:hover,
  .stock-row.selected {
    border-color: var(--primary);
    background: var(--primary-soft);
  }
  .variant-row {
    border: 0;
    border-top: 1px solid var(--border);
    border-radius: 0;
    padding-left: 38px;
  }
  .row-copy {
    min-width: 0;
    flex: 1;
  }
  .row-copy strong,
  .row-copy small {
    display: block;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .row-copy strong {
    font-size: 13px;
  }
  .row-copy small {
    margin-top: 2px;
    color: var(--text-muted);
    font-size: 11px;
  }
  .row-copy .barcode {
    color: var(--text-subtle, var(--text-muted));
    font-size: 10px;
  }
  .availability {
    display: grid;
    min-width: 114px;
    justify-items: end;
    gap: 1px;
    border: 1px solid var(--success-border, var(--border));
    border-radius: var(--radius-control);
    background: var(--success-soft, var(--surface-muted));
    padding: 5px 8px;
    text-align: right;
  }
  .availability small {
    color: var(--text-muted);
    font-size: 10px;
    line-height: 1.1;
  }
  .availability strong {
    color: var(--success, var(--text));
    font-size: 14px;
    line-height: 1.1;
  }
  .cancel-button {
    min-height: 32px;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    background: var(--surface);
    color: var(--text);
    padding: 0 11px;
    cursor: pointer;
  }
  .cancel-button:hover {
    background: var(--surface-muted);
  }

  @media (max-width: 560px) {
    .picker-dialog {
      top: 2vh;
      max-height: 96vh;
    }
    .dialog-header,
    .dialog-footer,
    .toolbar {
      padding-left: 14px;
      padding-right: 14px;
    }
    .results {
      padding-left: 14px;
      padding-right: 14px;
    }
    .filter-row {
      grid-template-columns: 1fr;
    }
    .stock-row {
      align-items: stretch;
      flex-direction: column;
      gap: 8px;
    }
    .availability {
      width: fit-content;
      min-width: 130px;
      justify-items: start;
      text-align: left;
    }
    .variant-row {
      padding-left: 22px;
    }
    .dialog-footer span {
      max-width: 65%;
    }
  }
</style>
