<script lang="ts">
  import { Plus, Trash2 } from '@lucide/svelte';
  import { onMount } from 'svelte';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { formatMoney } from '$lib/design/formatters';
  import { listTaxDefinitions } from '$lib/features/taxes/api';
  import type { TaxDefinition } from '$lib/features/taxes/types';
  import VariantManager from './VariantManager.svelte';
  import { mergeUnitGroups } from './unit-catalog';
  import type {
    Product,
    ProductBrand,
    ProductCategory,
    ProductInput,
    ProductUnit,
    ProductVariant,
    ProductVariantConfig,
    VariantDefinition
  } from './types';
  import type { ProductVariantUpdate } from './api';

  type TaxSide = 'purchase' | 'sales';
  type TaxComponent = {
    tax_definition_id: string;
    tax_rate_id?: string;
    rate_id?: string;
    calculation_type?: 'PERCENTAGE' | 'FIXED_AMOUNT' | 'QUANTITY_BASED';
    included_in_tax_base?: boolean;
    metadata?: Record<string, unknown>;
  };
  type TaxProfile = {
    treatment?: 'STANDARD' | 'WITHHOLDING' | 'EXEMPT' | 'NOT_APPLICABLE';
    tax_definition_id?: string;
    tax_rate_id?: string;
    vat_rate_id?: string;
    tax_code?: string;
    rate?: string;
    tax_included?: boolean;
    components?: TaxComponent[];
  };

  let {
    value = $bindable<ProductInput>(),
    categories = [],
    brands = [],
    unitOptions = [],
    baseCurrency = 'TRY',
    priceEntries = [],
    disabled = false,
    unitLocked = false,
    onPriceChange,
    onBaseSalesPriceChange,
    variantConfig = $bindable<ProductVariantConfig>(),
    productID,
    variantDefinitions = [],
    variants = [],
    productStock,
    persisted = false,
    readOnly = false,
    variantLoading = false,
    variantError = '',
    variantDataReady = true,
    variantDataLoading = false,
    canManageVariants = false,
    generateLabel,
    onGenerateVariants,
    onRetryVariantData,
    onVariantSave
  }: {
    value?: ProductInput;
    categories?: ProductCategory[];
    brands?: ProductBrand[];
    unitOptions?: ProductUnit[];
    baseCurrency?: string;
    priceEntries?: Array<{
      price_list_id: string;
      entry_id?: string;
      list_name: string;
      list_code: string;
      currency_code: string;
      unit_price?: string;
      valid_from?: string;
      valid_to?: string;
      version?: number;
      applies_to_all_categories?: boolean;
      scope_category_id?: string;
    }>;
    disabled?: boolean;
    unitLocked?: boolean;
    onPriceChange?: (priceListID: string, value: string) => void;
    onBaseSalesPriceChange?: (value: string) => void;
    variantConfig?: ProductVariantConfig;
    productID?: string;
    variantDefinitions?: VariantDefinition[];
    variants?: ProductVariant[];
    productStock?: Pick<
      Product,
      'physical_quantity' | 'reserved_quantity' | 'available_quantity' | 'stock_unit'
    >;
    persisted?: boolean;
    readOnly?: boolean;
    variantLoading?: boolean;
    variantError?: string;
    variantDataReady?: boolean;
    variantDataLoading?: boolean;
    canManageVariants?: boolean;
    generateLabel?: string;
    onGenerateVariants?: () => Promise<void>;
    onRetryVariantData?: () => Promise<void>;
    onVariantSave?: (
      variantID: string,
      version: number,
      input: ProductVariantUpdate
    ) => Promise<void>;
  } = $props();

  const formDisabled = $derived(disabled || readOnly);

  const currentUnit = $derived(value.units.find((unit) => unit.is_base)?.code || value.base_unit);
  const unitChoices = $derived(
    mergeUnitGroups(value.kind, unitOptions, currentUnit).flatMap((group) => group.units)
  );

  let taxCatalog = $state<TaxDefinition[]>([]);
  let taxError = $state('');
  const formValue = () => value as ProductInput & Record<string, unknown>;

  function emptyProfile(): TaxProfile {
    return {
      treatment: 'STANDARD',
      vat_rate_id: '',
      components: []
    };
  }

  function profile(side: TaxSide): TaxProfile {
    const raw = formValue()[`${side}_tax_profile`];
    if (raw && typeof raw === 'object') {
      const item = raw as Partial<TaxProfile>;
      return {
        ...emptyProfile(),
        ...item,
        vat_rate_id: item.vat_rate_id ?? item.tax_rate_id ?? '',
        tax_code: item.tax_code ?? 'KDV',
        rate:
          item.treatment === 'NOT_APPLICABLE'
            ? ''
            : (item.rate ?? String(formValue()[`${side}_tax_rate`] ?? '')),
        components: Array.isArray(item.components) ? item.components : []
      } as TaxProfile;
    }
    const legacyType = String(formValue()[`${side}_tax_type`] ?? '').toUpperCase();
    const legacyTreatment: TaxProfile['treatment'] =
      legacyType === 'MUAF' || legacyType === 'İSTİSNA' || legacyType === 'ISTISNA'
        ? 'EXEMPT'
        : legacyType === 'KDV_TEVKIFAT'
          ? 'WITHHOLDING'
          : legacyType === 'NOT_APPLICABLE' || legacyType === 'YOK' || legacyType === 'NONE'
            ? 'NOT_APPLICABLE'
            : 'STANDARD';
    return {
      ...emptyProfile(),
      treatment: legacyTreatment,
      vat_rate_id: String(formValue()[`${side}_tax_rate_id`] ?? ''),
      tax_code: 'KDV',
      rate: String(formValue()[`${side}_tax_rate`] ?? '')
    };
  }

  function writeProfile(side: TaxSide, next: TaxProfile) {
    const manualRate = String(next.rate ?? formValue()[`${side}_tax_rate`] ?? '').trim();
    const hasVat = manualRate !== '';
    const treatment = hasVat ? 'STANDARD' : 'NOT_APPLICABLE';
    const taxIncluded = Boolean(next.tax_included);
    const nestedProfile = {
      ...next,
      treatment,
      tax_code: hasVat ? 'KDV' : 'YOK',
      rate: manualRate,
      tax_included: taxIncluded,
      tax_definition_id: '',
      tax_rate_id: '',
      vat_rate_id: '',
      components: next.components ?? []
    };
    const patch: Record<string, unknown> = {
      [`${side}_tax_profile`]: nestedProfile,
      [`${side}_tax_treatment`]: treatment,
      [`${side}_tax_definition_id`]: '',
      [`${side}_tax_rate_id`]: '',
      [`${side}_vat_rate_id`]: '',
      [`${side}_tax_type`]: hasVat ? 'KDV' : 'YOK',
      [`${side}_tax_rate`]: manualRate,
      [`${side}_tax_included`]: taxIncluded
    };
    value = { ...formValue(), ...patch } as ProductInput;
  }

  function isKDVDefinition(definition: TaxDefinition) {
    return (
      definition.code.toUpperCase().startsWith('KDV') ||
      definition.name.toLocaleUpperCase('tr-TR').includes('KDV')
    );
  }

  /** Additional (non-KDV) tax components declared on one side of the card. */
  function componentsFor(side: TaxSide) {
    return (profile(side).components ?? []).filter((component) => {
      const definition = taxCatalog.find((item) => item.id === component.tax_definition_id);
      return !definition || !isKDVDefinition(definition);
    });
  }

  function componentSignature(component: TaxComponent) {
    return [
      component.tax_definition_id,
      componentRate(component),
      component.calculation_type ?? 'PERCENTAGE',
      component.included_in_tax_base !== false
    ].join('|');
  }

  /** Whether the purchase and sales additional-tax lists are identical. Drives
   *  the initial state of the "same on both sides" checkbox. */
  function componentsSideMatch() {
    const purchase = componentsFor('purchase').map(componentSignature);
    const sales = componentsFor('sales').map(componentSignature);
    return purchase.length === sales.length && purchase.every((sig, i) => sig === sales[i]);
  }

  // When the card links both sides, `sales` is the single source of truth that
  // the shared list reads from and both sides are written to.
  const LINKED_SIDE: TaxSide = 'sales';
  let taxLinked = $state(componentsSideMatch());

  function displayComponents(side: TaxSide) {
    return componentsFor(taxLinked ? LINKED_SIDE : side);
  }

  function vatRate() {
    const sales = profile('sales').rate?.trim();
    return sales ? (profile('sales').rate ?? '') : (profile('purchase').rate ?? '');
  }

  function writeComponents(side: TaxSide, components: TaxComponent[]) {
    const target = taxLinked ? LINKED_SIDE : side;
    if (taxLinked) {
      writeProfile('purchase', { ...profile('purchase'), components });
      writeProfile('sales', { ...profile('sales'), components });
      return;
    }
    writeProfile(target, { ...profile(target), components });
  }

  function copySalesComponentsToPurchase() {
    writeProfile('purchase', {
      ...profile('purchase'),
      components: profile('sales').components ?? []
    });
  }

  function setTaxLinked(linked: boolean) {
    // Linking: the sales list becomes the single list. Unlinking: seed the
    // purchase side from the current (sales) list so it can diverge.
    copySalesComponentsToPurchase();
    taxLinked = linked;
  }

  async function loadTaxCatalog() {
    taxError = '';
    try {
      if (taxCatalog.length === 0) taxCatalog = (await listTaxDefinitions(false)).items;
    } catch {
      taxError = 'Vergi tanımları alınamadı.';
    }
  }

  function updateVatRate(rate: string) {
    // The KDV rate is product-bound, not direction-bound: always both sides.
    writeProfile('purchase', { ...profile('purchase'), rate });
    writeProfile('sales', { ...profile('sales'), rate });
  }

  function updateComponentDefinition(side: TaxSide, index: number, definitionID: string) {
    const components = [...displayComponents(side)];
    const definition = taxCatalog.find((item) => item.id === definitionID);
    components[index] = {
      ...components[index],
      tax_definition_id: definitionID,
      tax_rate_id: '',
      rate_id: '',
      calculation_type: definition?.calculation_type ?? 'PERCENTAGE',
      metadata: { ...(components[index].metadata ?? {}), rate: definition?.rate ?? '' }
    };
    writeComponents(side, components);
  }

  function componentRate(component: TaxComponent) {
    return String(component.metadata?.rate ?? '');
  }

  function componentValuePlaceholder(component: TaxComponent) {
    return component.calculation_type === 'QUANTITY_BASED' ? 'Birim tutar' : 'Oran (%)';
  }

  function updateComponentRate(side: TaxSide, index: number, rate: string) {
    const components = [...displayComponents(side)];
    components[index] = {
      ...components[index],
      tax_rate_id: '',
      rate_id: '',
      metadata: { ...(components[index].metadata ?? {}), rate }
    };
    writeComponents(side, components);
  }

  function updateComponentType(
    side: TaxSide,
    index: number,
    calculationType: TaxComponent['calculation_type']
  ) {
    const components = [...displayComponents(side)];
    components[index] = {
      ...components[index],
      calculation_type: calculationType || 'PERCENTAGE'
    };
    writeComponents(side, components);
  }

  function updateComponentBase(side: TaxSide, index: number, includedInTaxBase: boolean) {
    const components = [...displayComponents(side)];
    components[index] = { ...components[index], included_in_tax_base: includedInTaxBase };
    writeComponents(side, components);
  }

  function addComponent(side: TaxSide) {
    const definition = additionalTaxDefinitions()[0];
    writeComponents(side, [
      ...displayComponents(side),
      {
        tax_definition_id: definition?.id ?? '',
        tax_rate_id: '',
        calculation_type: definition?.calculation_type ?? 'PERCENTAGE',
        included_in_tax_base: true,
        metadata: { rate: definition?.rate ?? '' }
      }
    ]);
  }

  function removeComponent(side: TaxSide, index: number) {
    writeComponents(
      side,
      displayComponents(side).filter((_, componentIndex) => componentIndex !== index)
    );
  }

  function additionalTaxDefinitions() {
    return taxCatalog.filter((definition) => !isKDVDefinition(definition));
  }

  /** The additional taxes on one side of the card, split by whether they sit in
   *  the KDV base. */
  function additionalTaxTotals(side: TaxSide) {
    const totals = { percentage: 0, fixed: 0, basePercentage: 0, baseFixed: 0 };
    for (const component of componentsFor(side)) {
      const amount = Number(componentRate(component).replace(',', '.'));
      if (!Number.isFinite(amount)) continue;
      const inBase = component.included_in_tax_base !== false;
      if (component.calculation_type === 'PERCENTAGE') {
        if (inBase) totals.basePercentage += amount;
        else totals.percentage += amount;
      } else if (inBase) totals.baseFixed += amount;
      else totals.fixed += amount;
    }
    return totals;
  }

  function taxIncludedFor(side: TaxSide) {
    return Boolean(profile(side).tax_included ?? formValue()[`${side}_tax_included`]);
  }

  function setTaxIncluded(side: TaxSide, included: boolean) {
    writeProfile(side, { ...profile(side), tax_included: included });
  }

  function inclusivePrice(side: TaxSide) {
    const price = parsePriceNumber(String(formValue()[`${side}_price`] ?? '0'));
    if (!Number.isFinite(price) || price < 0) return '—';
    // A tax-inclusive price already carries every tax; show it verbatim, the
    // way the server treats it (products.service sales_tax_summary).
    if (taxIncludedFor(side)) return formatMoney(price.toFixed(2), baseCurrency);
    const vatRate = Number(
      String(profile(side).rate ?? formValue()[`${side}_tax_rate`] ?? '0').replace(',', '.') || 0
    );
    const additional = additionalTaxTotals(side);
    if (!Number.isFinite(vatRate) || vatRate < 0) return '—';
    // Taxes inside the KDV base are charged first and then carried into the
    // KDV base, which is how ÖTV works.
    const withBaseTaxes = price * (1 + additional.basePercentage / 100) + additional.baseFixed;
    return formatMoney(
      (withBaseTaxes * (1 + (vatRate + additional.percentage) / 100) + additional.fixed).toFixed(2),
      baseCurrency
    );
  }

  function parsePriceNumber(value: string) {
    const compact = value.trim().replace(/\s/g, '');
    const comma = compact.lastIndexOf(',');
    const dot = compact.lastIndexOf('.');
    const canonical =
      comma >= 0 && dot >= 0
        ? comma > dot
          ? compact.replace(/\./g, '').replace(',', '.')
          : compact.replace(/,/g, '')
        : comma >= 0
          ? compact.replace(',', '.')
          : compact;
    const parsed = Number(canonical || '0');
    return parsed;
  }

  onMount(() => {
    void loadTaxCatalog();
  });

  let activeTab = $state<'general' | 'prices' | 'features' | 'variants'>('general');

  function setField<K extends keyof ProductInput>(key: K, next: ProductInput[K]) {
    value = { ...value, [key]: next };
    if (key === 'sales_price') onBaseSalesPriceChange?.(String(next ?? ''));
  }

  function setBaseUnit(code: string, decimalScale?: number) {
    const option = unitOptions.find((item) => item.code === code);
    const previous = value.units.find((unit) => unit.code === code);
    value = {
      ...value,
      base_unit: code,
      units: [
        {
          code,
          is_base: true,
          conversion_factor: '1',
          decimal_scale: previous?.decimal_scale ?? decimalScale ?? option?.decimal_scale ?? 0
        }
      ]
    };
  }

  function setKind(kind: ProductInput['kind']) {
    setField('kind', kind);
    // Fiziksel ↔ hizmet geçişinde, seçili birim yeni tipin katalogunda yoksa
    // varsayılana (ADET) dön; aksi halde kartta yabancı bir birim asılı kalır.
    const current = value.units.find((unit) => unit.is_base)?.code || value.base_unit;
    const validCodes = new Set(
      mergeUnitGroups(kind).flatMap((group) => group.units.map((unit) => unit.code))
    );
    if (!current || !validCodes.has(current)) setBaseUnit('ADET');
  }

  function addBarcode() {
    value = {
      ...value,
      barcodes: [
        ...value.barcodes,
        { barcode: '', barcode_type: 'EAN', is_primary: value.barcodes.length === 0 }
      ]
    };
  }

  function updateBarcode(index: number, patch: Partial<ProductInput['barcodes'][number]>) {
    value = {
      ...value,
      barcodes: value.barcodes.map((item, itemIndex) =>
        itemIndex === index
          ? { ...item, ...patch }
          : { ...item, is_primary: patch.is_primary ? false : item.is_primary }
      )
    };
  }

  function removeBarcode(index: number) {
    value = { ...value, barcodes: value.barcodes.filter((_, itemIndex) => itemIndex !== index) };
  }

  function scopedPriceEntries() {
    return priceEntries.filter(
      (entry) =>
        entry.applies_to_all_categories !== false || entry.scope_category_id === value.category_id
    );
  }

  function variantPriceLists() {
    return priceEntries.map((entry) => ({
      price_list_id: entry.price_list_id,
      list_name: entry.list_name,
      list_code: entry.list_code,
      currency_code: entry.currency_code
    }));
  }
</script>

<div class="product-tabs" aria-label="Stok kartı bölümleri" role="tablist">
  <button
    type="button"
    class:active={activeTab === 'general'}
    role="tab"
    aria-selected={activeTab === 'general'}
    onclick={() => (activeTab = 'general')}>Genel</button
  >
  <button
    type="button"
    class:active={activeTab === 'prices'}
    role="tab"
    aria-selected={activeTab === 'prices'}
    onclick={() => (activeTab = 'prices')}>Fiyatlar</button
  >
  <button
    type="button"
    class:active={activeTab === 'features'}
    role="tab"
    aria-selected={activeTab === 'features'}
    onclick={() => (activeTab = 'features')}>Özellikler</button
  >
  {#if value.kind === 'PHYSICAL'}
    <button
      type="button"
      class:active={activeTab === 'variants'}
      role="tab"
      aria-selected={activeTab === 'variants'}
      onclick={() => (activeTab = 'variants')}>Varyantlar</button
    >
  {/if}
</div>

{#if activeTab === 'general'}
  <fieldset class="field-group" disabled={formDisabled}>
    <section class="form-section" aria-labelledby="product-main-heading">
      <h2 id="product-main-heading">Stok kartı bilgileri</h2>
      <div class="form-grid">
        <label
          ><span>Stok / hizmet adı <b>*</b></span><Input
            value={value.name}
            {disabled}
            oninput={(event) => setField('name', event.currentTarget.value)}
          /></label
        >
        <label
          ><span>Kart türü <b>*</b></span><select
            value={value.kind}
            {disabled}
            onchange={(event) => setKind(event.currentTarget.value as ProductInput['kind'])}
            ><option value="PHYSICAL">Fiziksel ürün</option><option value="SERVICE">Hizmet</option
            ></select
          ></label
        >
        <label class="code-field"
          ><span>Stok kodu / SKU</span><Input
            value={value.code}
            {disabled}
            placeholder="Boş bırakırsanız otomatik üretilir"
            oninput={(event) => setField('code', event.currentTarget.value)}
          /></label
        >
        <label class="checkbox-line"
          ><input
            type="checkbox"
            checked={value.is_active}
            {disabled}
            onchange={(event) => setField('is_active', event.currentTarget.checked)}
          /><span>Aktif kart</span></label
        >
      </div>
      <label class="wide"
        ><span>Açıklama</span><textarea
          rows="3"
          {disabled}
          value={value.description}
          oninput={(event) => setField('description', event.currentTarget.value)}
        ></textarea></label
      >
    </section>

    <section class="form-section" aria-labelledby="product-unit-heading">
      <h2 id="product-unit-heading">Stok birimi</h2>
      <label class="wide unit-field"
        ><span>Stok birimi <b>*</b></span><select
          value={value.units.find((unit) => unit.is_base)?.code || value.base_unit}
          disabled={disabled || unitLocked}
          required
          onchange={(event) => {
            const unit = unitChoices.find((item) => item.code === event.currentTarget.value);
            setBaseUnit(event.currentTarget.value, unit?.decimal_scale);
          }}
          >{#each unitChoices as option (option.code)}<option value={option.code}
              >{option.name}</option
            >{/each}</select
        >{#if unitLocked}<small class="unit-lock-note"
            >Kart tanımlandıktan sonra stok birimi değiştirilemez.</small
          >{/if}</label
      >
    </section>

    {#if !variantConfig?.enabled}
      <section class="form-section" aria-labelledby="product-barcode-heading">
        <div class="section-heading">
          <div>
            <h2 id="product-barcode-heading">Barkodlar</h2>
            <p>
              Birden fazla barkod tanımlanabilir; şirket içinde aynı barkod tekrar kullanılamaz.
            </p>
          </div>
          <Button type="button" variant="outline" size="sm" {disabled} onclick={addBarcode}
            ><Plus size={14} />Barkod ekle</Button
          >
        </div>
        {#if value.barcodes.length === 0}<p class="muted">Bu kart için barkod tanımlanmadı.</p>{/if}
        <div class="barcode-list">
          {#each value.barcodes as barcode, index}<div class="barcode-row">
              <Input
                value={barcode.barcode}
                {disabled}
                placeholder="Barkod"
                oninput={(event) => updateBarcode(index, { barcode: event.currentTarget.value })}
              /><select
                value={barcode.barcode_type}
                {disabled}
                onchange={(event) =>
                  updateBarcode(index, { barcode_type: event.currentTarget.value })}
                ><option value="EAN">EAN</option><option value="UPC">UPC</option><option
                  value="CODE128">Code 128</option
                ><option value="OTHER">Diğer</option></select
              ><label class="base-choice"
                ><input
                  type="radio"
                  name="primary-barcode"
                  checked={barcode.is_primary}
                  {disabled}
                  onchange={() => updateBarcode(index, { is_primary: true })}
                /><span>Ana</span></label
              ><Button
                type="button"
                variant="ghost"
                size="icon"
                {disabled}
                aria-label="Barkodu kaldır"
                onclick={() => removeBarcode(index)}><Trash2 size={14} /></Button
              >
            </div>{/each}
        </div>
      </section>
    {/if}
  </fieldset>
{:else if activeTab === 'prices'}
  <fieldset class="field-group" disabled={formDisabled}>
    <section class="form-section" aria-labelledby="product-price-heading">
      <h2 id="product-price-heading">Fiyatlar</h2>
      <p>
        Fiyatları vergiler hariç girin. Vergi oranına göre vergiler dahil tutar otomatik hesaplanır.
      </p>
      {#snippet taxRows(side: TaxSide)}
        {#if displayComponents(side).length === 0}<p class="muted">
            KDV dışında ek vergi yok.
          </p>{/if}
        {#each displayComponents(side) as component, componentIndex}
          <div class="additional-tax-row">
            <select
              value={component.tax_definition_id}
              {disabled}
              aria-label="Vergi türü"
              onchange={(event) =>
                updateComponentDefinition(side, componentIndex, event.currentTarget.value)}
            >
              <option value="">Vergi türü seçin</option>
              {#each additionalTaxDefinitions() as definition}<option value={definition.id}
                  >{definition.name}</option
                >{/each}
            </select>
            <Input
              value={componentRate(component)}
              {disabled}
              inputmode="decimal"
              placeholder={componentValuePlaceholder(component)}
              aria-label={component.calculation_type === 'QUANTITY_BASED'
                ? 'Ek vergi birim tutarı'
                : 'Ek vergi oranı'}
              oninput={(event) =>
                updateComponentRate(side, componentIndex, event.currentTarget.value)}
            />
            <select
              value={component.calculation_type || 'PERCENTAGE'}
              {disabled}
              aria-label="Ek vergi hesaplama yöntemi"
              onchange={(event) =>
                updateComponentType(
                  side,
                  componentIndex,
                  event.currentTarget.value as TaxComponent['calculation_type']
                )}
            >
              <option value="PERCENTAGE">Oran</option>
              <option value="QUANTITY_BASED">Miktar bazlı</option>
              <option value="FIXED_AMOUNT">Sabit tutar</option>
            </select>
            <label class="base-choice"
              ><input
                type="checkbox"
                checked={component.included_in_tax_base !== false}
                {disabled}
                onchange={(event) =>
                  updateComponentBase(side, componentIndex, event.currentTarget.checked)}
              /><span>KDV matrahına dahil</span></label
            >
            <Button
              type="button"
              variant="ghost"
              size="icon"
              {disabled}
              aria-label="Ek vergiyi kaldır"
              onclick={() => removeComponent(side, componentIndex)}><Trash2 size={14} /></Button
            >
          </div>
        {/each}
      {/snippet}
      {#snippet addTaxButton(side: TaxSide)}
        <Button
          type="button"
          variant="outline"
          size="sm"
          {disabled}
          onclick={() => addComponent(side)}><Plus size={13} />Vergi ekle</Button
        >
      {/snippet}
      <div class="price-grid">
        <label
          ><span
            >Alış fiyatı ({baseCurrency}, {taxIncludedFor('purchase')
              ? 'vergiler dahil'
              : 'vergiler hariç'})</span
          ><Input
            value={value.purchase_price}
            {disabled}
            inputmode="decimal"
            placeholder="0,00"
            oninput={(event) => setField('purchase_price', event.currentTarget.value)}
          /></label
        >
        <label class="checkbox-line"
          ><input
            type="checkbox"
            checked={taxIncludedFor('purchase')}
            {disabled}
            onchange={(event) => setTaxIncluded('purchase', event.currentTarget.checked)}
          /><span>Alış fiyatı KDV dahil</span></label
        >
        <label
          ><span
            >Satış fiyatı ({baseCurrency}, {taxIncludedFor('sales')
              ? 'vergiler dahil'
              : 'vergiler hariç'})</span
          ><Input
            value={value.sales_price}
            {disabled}
            inputmode="decimal"
            placeholder="0,00"
            oninput={(event) => setField('sales_price', event.currentTarget.value)}
          /></label
        >
        <label class="checkbox-line"
          ><input
            type="checkbox"
            checked={taxIncludedFor('sales')}
            {disabled}
            onchange={(event) => setTaxIncluded('sales', event.currentTarget.checked)}
          /><span>Satış fiyatı KDV dahil</span></label
        >
        <label
          ><span>KDV oranı (%)</span><Input
            value={vatRate()}
            {disabled}
            inputmode="decimal"
            placeholder="Boş bırakılırsa KDV uygulanmaz"
            oninput={(event) => updateVatRate(event.currentTarget.value)}
          /></label
        >
      </div>
      <div class="additional-tax-grid">
        <div class="additional-tax-card">
          <div class="additional-tax-heading">
            <strong>Ek vergiler</strong>
            <label class="base-choice"
              ><input
                type="checkbox"
                checked={taxLinked}
                {disabled}
                onchange={(event) => setTaxLinked(event.currentTarget.checked)}
              /><span>Alış ve satışta aynı</span></label
            >
          </div>
          {#if taxLinked}
            <div class="additional-tax-subheading">
              <span></span>{@render addTaxButton('sales')}
            </div>
            {@render taxRows('sales')}
          {:else}
            <div class="additional-tax-subheading">
              <strong>Alışta</strong>{@render addTaxButton('purchase')}
            </div>
            {@render taxRows('purchase')}
            <div class="additional-tax-subheading">
              <strong>Satışta</strong>{@render addTaxButton('sales')}
            </div>
            {@render taxRows('sales')}
          {/if}
          {#if additionalTaxDefinitions().length === 0}<a
              class="definition-link"
              href="/ayarlar/vergi-tanimlari">Ek vergi tanımla</a
            >{/if}
        </div>
      </div>
      {#if taxError}<p class="tax-error" role="alert">{taxError}</p>{/if}
      <div class="additional-prices">
        <h3>Diğer fiyat tanımları</h3>
        {#if scopedPriceEntries().length > 0}
          <div class="price-list-grid">
            {#each scopedPriceEntries() as entry}
              <label class="price-list-row">
                <span
                  ><strong>{entry.list_name}</strong><small
                    >{entry.list_code} · {entry.currency_code}</small
                  ></span
                >
                <Input
                  value={entry.unit_price ?? ''}
                  {disabled}
                  inputmode="decimal"
                  placeholder="0,00"
                  aria-label={`${entry.list_name} fiyatı`}
                  oninput={(event) =>
                    onPriceChange?.(entry.price_list_id, event.currentTarget.value)}
                />
              </label>
            {/each}
          </div>
        {:else}
          <p class="muted">Bu ürünün kategorisine uygulanabilir başka fiyat tanımı bulunmuyor.</p>
          <a class="definition-link" href="/ayarlar/fiyat-listeleri">Fiyat tanımı oluştur</a>
        {/if}
      </div>
      <div class="inclusive-prices" aria-label="Net fiyatlar">
        <div>
          <span>Net alış fiyatı (vergiler dahil)</span><strong>{inclusivePrice('purchase')}</strong>
        </div>
        <div>
          <span>Net satış fiyatı (vergiler dahil)</span><strong>{inclusivePrice('sales')}</strong>
        </div>
      </div>
    </section>
  </fieldset>
{:else if activeTab === 'features'}
  <fieldset class="field-group" disabled={formDisabled}>
    <section class="form-section" aria-labelledby="product-feature-heading">
      <h2 id="product-feature-heading">Özellikler</h2>
      <div class="form-grid">
        <label
          ><span>Kategori</span><select
            value={value.category_id}
            {disabled}
            onchange={(event) => setField('category_id', event.currentTarget.value)}
            ><option value="">Kategori seçin</option>{#each categories as item}<option
                value={item.id}>{item.name}</option
              >{/each}</select
          ><a class="definition-link" href="/ayarlar/tanimlar/kategoriler">Yeni kategori tanımla</a
          ></label
        >
        <label
          ><span>Marka</span><select
            value={value.brand_id}
            {disabled}
            onchange={(event) => setField('brand_id', event.currentTarget.value)}
            ><option value="">Marka seçin</option>{#each brands as item}<option value={item.id}
                >{item.name}</option
              >{/each}</select
          ><a class="definition-link" href="/ayarlar/tanimlar/markalar">Yeni marka tanımla</a
          ></label
        >
      </div>
      <div class="custom-descriptions">
        <label class="wide"
          ><span>Özel açıklama 1</span><textarea
            rows="2"
            {disabled}
            value={value.custom_description_1}
            oninput={(event) => setField('custom_description_1', event.currentTarget.value)}
          ></textarea></label
        >
        <label class="wide"
          ><span>Özel açıklama 2</span><textarea
            rows="2"
            {disabled}
            value={value.custom_description_2}
            oninput={(event) => setField('custom_description_2', event.currentTarget.value)}
          ></textarea></label
        >
        <label class="wide"
          ><span>Özel açıklama 3</span><textarea
            rows="2"
            {disabled}
            value={value.custom_description_3}
            oninput={(event) => setField('custom_description_3', event.currentTarget.value)}
          ></textarea></label
        >
      </div>
    </section>
  </fieldset>
{:else if activeTab === 'variants' && value.kind === 'PHYSICAL'}
  <VariantManager
    productCode={value.code}
    {productID}
    productBarcodes={value.barcodes}
    {baseCurrency}
    parentPurchasePrice={value.purchase_price}
    parentSalesPrice={value.sales_price}
    bind:config={variantConfig}
    definitions={variantDefinitions}
    {variants}
    priceLists={variantPriceLists()}
    {productStock}
    {persisted}
    {variantDataReady}
    {variantDataLoading}
    loading={variantLoading}
    {disabled}
    canManage={canManageVariants}
    {generateLabel}
    error={variantError}
    onGenerate={onGenerateVariants}
    onRetryData={onRetryVariantData}
    {onVariantSave}
  />
{/if}

<style>
  .field-group {
    min-inline-size: 0;
    margin: 0;
    padding: 0;
    border: 0;
  }
  .form-section {
    padding: 18px 20px;
    border-bottom: 1px solid var(--border);
  }
  .form-section:last-child {
    border-bottom: 0;
  }
  h2 {
    margin: 0;
    font-size: 14px;
  }
  p {
    margin: 4px 0 0;
    color: var(--text-muted);
    font-size: 11px;
  }
  .form-grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 12px;
    margin-top: 14px;
  }
  label > span {
    display: block;
    margin-bottom: 4px;
    color: var(--text-muted);
    font-size: 11px;
  }
  label b {
    color: var(--danger);
  }
  select,
  textarea {
    width: 100%;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    background: var(--surface);
    color: var(--text);
    min-height: var(--control-height);
    padding: 0 10px;
    font-size: 13px;
  }
  textarea {
    padding: 9px 10px;
    resize: vertical;
  }
  .wide {
    display: block;
    margin-top: 12px;
  }
  .price-grid {
    display: grid;
    grid-template-columns: minmax(220px, 520px);
    gap: 12px;
    max-width: 520px;
  }
  .inclusive-prices {
    display: grid;
    grid-template-columns: minmax(220px, 520px);
    gap: 8px;
    max-width: 520px;
    margin-top: 16px;
  }
  .inclusive-prices > div {
    display: flex;
    justify-content: space-between;
    align-items: baseline;
    gap: 12px;
    padding: 11px 12px;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    background: var(--surface-muted);
  }
  .inclusive-prices span {
    color: var(--text-muted);
    font-size: 11px;
  }
  .inclusive-prices strong {
    font-size: 14px;
  }
  .additional-tax-grid {
    display: grid;
    grid-template-columns: minmax(0, 760px);
    gap: 12px;
    max-width: 760px;
    margin-top: 16px;
  }
  .additional-tax-card {
    display: grid;
    gap: 8px;
    padding: 11px;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    background: var(--surface-muted);
  }
  .additional-tax-heading {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
  }
  .additional-tax-heading strong {
    font-size: 11px;
  }
  .additional-tax-subheading {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    margin-top: 4px;
  }
  .additional-tax-subheading strong {
    color: var(--text-muted);
    font-size: 11px;
  }
  .additional-tax-row {
    display: grid;
    grid-template-columns:
      minmax(0, 1fr) minmax(110px, 0.7fr) minmax(125px, 0.7fr)
      minmax(150px, auto) auto;
    gap: 6px;
    align-items: center;
  }
  .additional-tax-row .base-choice {
    align-self: center;
    white-space: nowrap;
  }
  .tax-error {
    margin: 9px 0 0;
    color: var(--danger);
  }
  .additional-prices {
    margin-top: 22px;
  }
  .additional-prices h3 {
    margin: 0 0 8px;
    font-size: 12px;
  }
  .price-list-grid {
    display: grid;
    gap: 6px;
  }
  .price-list-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 16px;
    padding: 9px 10px;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    background: var(--surface-muted);
    color: var(--text);
    font-size: 12px;
  }
  .price-list-row span:first-child {
    display: grid;
    gap: 2px;
  }
  .price-list-row small {
    color: var(--text-muted);
    font-size: 10px;
  }
  .price-list-row :global(input) {
    max-width: 180px;
    text-align: right;
  }
  .custom-descriptions {
    display: grid;
    gap: 12px;
    margin-top: 14px;
  }
  .definition-link {
    display: inline-block;
    margin-top: 5px;
    color: var(--primary);
    font-size: 11px;
    text-decoration: none;
  }
  .definition-link:hover {
    text-decoration: underline;
  }
  .checkbox-line,
  .base-choice {
    display: flex;
    align-items: center;
    gap: 7px;
    align-self: end;
    min-height: var(--control-height);
    color: var(--text);
    font-size: 12px;
  }
  .checkbox-line span,
  .base-choice span {
    margin: 0;
    color: var(--text);
    font-size: 12px;
  }
  .section-heading {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 12px;
  }
  .barcode-list {
    display: grid;
    gap: 7px;
    margin-top: 12px;
  }
  .barcode-row {
    display: grid;
    grid-template-columns: minmax(160px, 1fr) 120px auto auto;
    gap: 7px;
    align-items: center;
  }
  .muted {
    color: var(--text-muted);
  }
  .unit-lock-note {
    display: block;
    margin-top: 5px;
    color: var(--text-muted);
    font-size: 11px;
    font-weight: 500;
  }
  @media (max-width: 700px) {
    .form-grid,
    .barcode-row {
      grid-template-columns: 1fr;
    }
    .price-grid,
    .inclusive-prices,
    .additional-tax-grid {
      grid-template-columns: 1fr;
    }
    .additional-tax-row {
      grid-template-columns: 1fr;
    }
    .checkbox-line,
    .base-choice {
      align-self: start;
    }
  }
</style>
