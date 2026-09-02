<script lang="ts">
  import { Search, SlidersHorizontal, PackageOpen } from '@lucide/svelte';
  import { Badge } from '$lib/components/ui/badge';
  import { Input } from '$lib/components/ui/input';
  import { canonicalDecimal } from '$lib/design/decimal';
  import { formatQuantity } from '$lib/design/formatters';
  import { addPositiveDecimals, compareDecimal } from './transfer-validation';

  export type VariantStockMovementDirection = 'IN' | 'OUT';

  export type VariantStockMovementRow = {
    id: string;
    variant_id?: string;
    variant_code?: string;
    variant_name?: string;
    sku?: string;
    attributes?: Record<string, unknown>;
    variant_display?: Record<string, unknown>;
    available_quantity?: string | number;
    availableQuantity?: string | number;
    physical_quantity?: string | number;
    reserved_quantity?: string | number;
    quantity?: string;
    unit_cost?: string;
    is_active?: boolean;
  };

  type RowValidation = {
    quantityError: string;
    unitCostError: string;
    message: string;
  };

  type Props = {
    rows?: VariantStockMovementRow[];
    direction?: VariantStockMovementDirection;
    loading?: boolean;
    unit?: string;
    currency?: string;
    disabled?: boolean;
    error?: string;
    onQuantityChange?: (row: VariantStockMovementRow, value: string) => void;
    onUnitCostChange?: (row: VariantStockMovementRow, value: string) => void;
    onRowError?: (row: VariantStockMovementRow, message?: string) => void;
    onError?: (message?: string) => void;
    onValidationChange?: (errors: Record<string, string>, valid: boolean) => void;
  };

  let {
    rows = $bindable<VariantStockMovementRow[]>([]),
    direction = 'IN',
    loading = false,
    unit = 'ADET',
    currency = 'TRY',
    disabled = false,
    error = '',
    onQuantityChange,
    onUnitCostChange,
    onRowError,
    onError,
    onValidationChange
  }: Props = $props();

  let search = $state('');
  let showEnteredOnly = $state(false);

  const activeRows = $derived(rows.filter((row) => row.is_active !== false));

  const visibleRows = $derived.by(() => {
    const needle = search.trim().toLocaleLowerCase('tr-TR');

    return activeRows.filter((row) => {
      if (showEnteredOnly && !hasQuantity(row.quantity)) return false;
      if (!needle) return true;
      return variantSearchText(row).toLocaleLowerCase('tr-TR').includes(needle);
    });
  });

  const rowValidations = $derived.by(() => {
    const result = new Map<string, RowValidation>();
    for (const row of activeRows) result.set(row.id, validateRow(row));
    return result;
  });

  const enteredRows = $derived(activeRows.filter((row) => hasQuantity(row.quantity)));
  const invalidRows = $derived(
    activeRows.filter((row) => Boolean(rowValidations.get(row.id)?.message))
  );
  const totalQuantity = $derived.by(() => {
    return activeRows.reduce((total, row) => {
      const value = canonicalDecimal(row.quantity);
      if (!isPositiveDecimal(value)) return total;
      return addPositiveDecimals(total, value);
    }, '0');
  });

  const hasRows = $derived(activeRows.length > 0);
  const hasVisibleRows = $derived(visibleRows.length > 0);
  const formValid = $derived(
    activeRows.length > 0 && enteredRows.length > 0 && invalidRows.length === 0
  );

  $effect(() => {
    const errors: Record<string, string> = {};
    for (const row of invalidRows) {
      const message = rowValidations.get(row.id)?.message;
      if (message) errors[row.id] = message;
    }
    onValidationChange?.(errors, formValid);
    onError?.(Object.values(errors)[0]);
  });

  function hasQuantity(value?: string) {
    return Boolean(value?.trim());
  }

  function isPositiveDecimal(value: string) {
    return /^(?:\d+(?:\.\d+)?|\.\d+)$/.test(value) && compareDecimal(value, '0') > 0;
  }

  function isDecimal(value: string) {
    return /^(?:\d+(?:\.\d*)?|\.\d+)$/.test(value);
  }

  function availableFor(row: VariantStockMovementRow) {
    return String(row.available_quantity ?? row.availableQuantity ?? '');
  }

  function validateRow(row: VariantStockMovementRow): RowValidation {
    let quantityError = '';
    let unitCostError = '';
    const quantity = canonicalDecimal(row.quantity);

    if (hasQuantity(row.quantity)) {
      if (!isPositiveDecimal(quantity)) {
        quantityError = 'Geçerli bir miktar girin.';
      } else if (direction === 'OUT' && !String(availableFor(row)).trim()) {
        quantityError = 'Güncel kullanılabilir bakiye doğrulanamadı.';
      } else if (
        direction === 'OUT' &&
        compareDecimal(quantity, canonicalDecimal(availableFor(row))) > 0
      ) {
        quantityError = 'Kullanılabilir bakiyeyi aşamaz.';
      }
    }

    const unitCost = canonicalDecimal(row.unit_cost);
    if (direction === 'IN' && row.unit_cost?.trim()) {
      if (!isDecimal(unitCost) || compareDecimal(unitCost, '0') < 0) {
        unitCostError = 'Geçerli bir maliyet girin.';
      }
    }

    return {
      quantityError,
      unitCostError,
      message: quantityError || unitCostError
    };
  }

  function updateRow(
    row: VariantStockMovementRow,
    patch: Partial<Pick<VariantStockMovementRow, 'quantity' | 'unit_cost'>>
  ) {
    rows = rows.map((item) => (item.id === row.id ? { ...item, ...patch } : item));
    const updated = rows.find((item) => item.id === row.id) ?? { ...row, ...patch };
    const validation = validateRow(updated);
    onRowError?.(updated, validation.message || undefined);
    if ('quantity' in patch) onQuantityChange?.(updated, updated.quantity ?? '');
    if ('unit_cost' in patch) onUnitCostChange?.(updated, updated.unit_cost ?? '');
  }

  function variantAttributes(row: VariantStockMovementRow) {
    const source = row.variant_display ?? row.attributes ?? {};
    return Object.entries(source).filter(
      ([, value]) => value !== null && value !== undefined && value !== ''
    );
  }

  function attributeValue(value: unknown): string {
    if (Array.isArray(value)) return value.map((item) => attributeValue(item)).join(', ');
    if (typeof value === 'object' && value !== null) {
      const item = value as Record<string, unknown>;
      return String(item.name ?? item.label ?? item.value ?? item.code ?? '');
    }
    return String(value);
  }

  function variantLabel(row: VariantStockMovementRow) {
    const attributes = variantAttributes(row)
      .map(([key, value]) => `${key}: ${attributeValue(value)}`)
      .join(' · ');
    return attributes || row.variant_name || row.variant_code || row.sku || 'Tanımsız varyant';
  }

  function variantSearchText(row: VariantStockMovementRow) {
    return [variantLabel(row), row.variant_code, row.variant_name, row.sku]
      .filter(Boolean)
      .join(' ');
  }

  function rowIsUnavailable(row: VariantStockMovementRow) {
    return direction === 'OUT' && compareDecimal(canonicalDecimal(availableFor(row)), '0') <= 0;
  }
</script>

<section class="variant-movement" aria-labelledby="variant-movement-title">
  <div class="section-heading">
    <div>
      <h3 id="variant-movement-title">Varyant stokları</h3>
      <p>Her varyant için hareket miktarını ayrı ayrı girin.</p>
    </div>
    <div class="summary" aria-live="polite">
      <span class="summary-label">Toplam hareket</span>
      <strong>{formatQuantity(totalQuantity)} {unit}</strong>
      <small>{enteredRows.length} satır girildi</small>
    </div>
  </div>

  {#if loading}
    <div class="state empty-state" role="status" aria-live="polite">
      <span class="loading-mark" aria-hidden="true"></span>
      <strong>Varyant stokları yükleniyor…</strong>
      <span>Güncel bakiyeler kontrol ediliyor.</span>
    </div>
  {:else if !hasRows}
    <div class="state empty-state" role="status">
      <PackageOpen size={22} aria-hidden="true" />
      <strong>Aktif varyant bulunamadı</strong>
      <span>Bu stok kartına önce aktif varyant ekleyin.</span>
    </div>
  {:else}
    <div class="table-toolbar">
      <label class="search-field">
        <span class="sr-only">Varyantlarda ara</span>
        <Search size={15} aria-hidden="true" />
        <Input bind:value={search} type="search" placeholder="Varyant, özellik veya SKU ara" />
      </label>
      <label class="filter-toggle">
        <input type="checkbox" bind:checked={showEnteredOnly} />
        <SlidersHorizontal size={14} aria-hidden="true" />
        <span>Yalnız girilenleri göster</span>
      </label>
    </div>

    {#if hasVisibleRows}
      <div class="table-wrap">
        <table>
          <caption class="sr-only">Varyant bazlı stok hareketi</caption>
          <thead>
            <tr>
              <th scope="col">Varyant</th>
              <th scope="col" class="numeric">Fiziksel</th>
              <th scope="col" class="numeric">Rezerve</th>
              <th scope="col" class="numeric">Kullanılabilir</th>
              <th scope="col" class="quantity-column">Hareket miktarı</th>
              {#if direction === 'IN'}<th scope="col" class="cost-column">Birim maliyet</th>{/if}
            </tr>
          </thead>
          <tbody>
            {#each visibleRows as row (row.id)}
              {@const validation = rowValidations.get(row.id) ?? {
                quantityError: '',
                unitCostError: '',
                message: ''
              }}
              {@const unavailable = rowIsUnavailable(row)}
              <tr class:unavailable class:has-error={Boolean(validation.message)}>
                <th scope="row" class="variant-cell">
                  <div class="variant-title-row">
                    <span class="variant-title">{variantLabel(row)}</span>
                    {#if unavailable}<Badge tone="neutral">Stok yok</Badge>{/if}
                  </div>
                  {#if row.variant_code || row.sku}<small>{row.variant_code || row.sku}</small>{/if}
                  {#if variantAttributes(row).length > 0}
                    <div class="attribute-list" aria-label="Varyant özellikleri">
                      {#each variantAttributes(row) as [key, value]}
                        <Badge tone="info">{key}: {attributeValue(value)}</Badge>
                      {/each}
                    </div>
                  {/if}
                </th>
                <td class="numeric muted-value">{formatQuantity(row.physical_quantity ?? '0')}</td>
                <td class="numeric muted-value">{formatQuantity(row.reserved_quantity ?? '0')}</td>
                <td class="numeric available-value">
                  <strong>{formatQuantity(availableFor(row))}</strong> <small>{unit}</small>
                </td>
                <td class="input-cell">
                  <div class="input-with-unit">
                    <Input
                      value={row.quantity ?? ''}
                      disabled={disabled || unavailable}
                      aria-label={`${variantLabel(row)} hareket miktarı`}
                      aria-invalid={Boolean(validation.quantityError)}
                      inputmode="decimal"
                      placeholder={unavailable ? 'Stok yok' : '0'}
                      oninput={(event) => updateRow(row, { quantity: event.currentTarget.value })}
                    />
                    <span>{unit}</span>
                  </div>
                  {#if validation.quantityError}<small class="field-error" role="alert"
                      >{validation.quantityError}</small
                    >{/if}
                </td>
                {#if direction === 'IN'}
                  <td class="input-cell">
                    <div class="input-with-unit">
                      <Input
                        value={row.unit_cost ?? ''}
                        {disabled}
                        aria-label={`${variantLabel(row)} birim maliyeti`}
                        aria-invalid={Boolean(validation.unitCostError)}
                        inputmode="decimal"
                        placeholder="İsteğe bağlı"
                        oninput={(event) =>
                          updateRow(row, { unit_cost: event.currentTarget.value })}
                      />
                      <span>{currency}</span>
                    </div>
                    {#if validation.unitCostError}<small class="field-error" role="alert"
                        >{validation.unitCostError}</small
                      >{/if}
                  </td>
                {/if}
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {:else}
      <div class="state filtered-empty" role="status">
        <Search size={19} aria-hidden="true" />
        <strong>Eşleşen varyant bulunamadı</strong>
        <span>Arama metnini veya filtreyi değiştirin.</span>
      </div>
    {/if}
  {/if}

  {#if error}<p class="general-error" role="alert">{error}</p>{/if}
  {#if !loading && hasRows && enteredRows.length === 0}
    <p class="hint" role="status">Devam etmek için en az bir varyanta miktar girin.</p>
  {:else if !loading && hasRows && invalidRows.length > 0}
    <p class="hint error-hint" role="alert">
      {invalidRows.length} satırdaki bilgileri kontrol edin.
    </p>
  {/if}
</section>

<style>
  .variant-movement {
    display: grid;
    gap: 14px;
    min-width: 0;
  }

  .section-heading {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 18px;
  }

  h3 {
    margin: 2px 0 3px;
    color: var(--text);
    font-size: 15px;
  }

  .section-heading p,
  .state span,
  .hint {
    margin: 0;
    color: var(--text-muted);
    font-size: 12px;
  }

  .summary {
    display: grid;
    min-width: 145px;
    padding: 9px 11px;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    background: var(--surface-muted);
    text-align: right;
  }

  .summary-label,
  .summary small {
    color: var(--text-muted);
    font-size: 11px;
  }

  .summary strong {
    margin: 2px 0;
    color: var(--text);
    font-size: 15px;
  }

  .table-toolbar {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    padding: 9px 10px;
    border: 1px solid var(--border);
    border-bottom: 0;
    border-radius: var(--radius-control) var(--radius-control) 0 0;
    background: var(--surface-muted);
  }

  .search-field {
    display: flex;
    align-items: center;
    gap: 7px;
    width: min(100%, 330px);
    color: var(--text-muted);
  }

  .filter-toggle {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    color: var(--text-subtle);
    font-size: 12px;
    font-weight: 650;
    white-space: nowrap;
  }

  .filter-toggle input {
    accent-color: var(--primary);
  }

  .table-wrap {
    overflow-x: auto;
    border: 1px solid var(--border);
    border-radius: 0 0 var(--radius-control) var(--radius-control);
    background: var(--surface);
  }

  table {
    width: 100%;
    min-width: 780px;
    border-collapse: collapse;
    font-size: 12px;
  }

  th,
  td {
    padding: 10px 11px;
    border-bottom: 1px solid var(--border);
    vertical-align: top;
  }

  thead th {
    background: var(--surface-muted);
    color: var(--text-muted);
    font-size: 10px;
    font-weight: 750;
    letter-spacing: 0.03em;
    text-align: left;
    text-transform: uppercase;
    white-space: nowrap;
  }

  tbody tr:last-child th,
  tbody tr:last-child td {
    border-bottom: 0;
  }

  tbody tr:hover {
    background: color-mix(in srgb, var(--primary) 3%, var(--surface));
  }

  tbody tr.unavailable {
    background: var(--surface-muted);
  }

  tbody tr.unavailable .variant-title,
  tbody tr.unavailable .available-value {
    color: var(--text-muted);
  }

  tbody tr.has-error {
    box-shadow: inset 3px 0 0 var(--danger);
  }

  .numeric {
    text-align: right;
    white-space: nowrap;
  }

  .quantity-column {
    min-width: 155px;
  }

  .cost-column {
    min-width: 160px;
  }

  .variant-cell {
    min-width: 245px;
    color: var(--text);
    font-weight: 650;
    text-align: left;
  }

  .variant-title-row {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 6px;
  }

  .variant-cell small {
    display: block;
    margin-top: 3px;
    color: var(--text-muted);
    font-size: 11px;
    font-weight: 500;
  }

  .attribute-list {
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
    margin-top: 7px;
  }

  .muted-value {
    color: var(--text-muted);
  }

  .available-value {
    color: var(--text);
  }

  .available-value small {
    color: var(--text-muted);
    font-size: 10px;
    font-weight: 600;
  }

  .input-cell {
    min-width: 145px;
  }

  .input-with-unit {
    display: flex;
    align-items: center;
    gap: 5px;
  }

  .input-with-unit :global(input) {
    min-width: 0;
    text-align: right;
  }

  .input-with-unit span {
    color: var(--text-muted);
    font-size: 11px;
    font-weight: 700;
  }

  .field-error {
    display: block;
    margin-top: 4px;
    color: var(--danger);
    font-size: 10px;
    font-weight: 650;
    line-height: 1.3;
  }

  .state {
    display: grid;
    justify-items: center;
    gap: 6px;
    padding: 30px 16px;
    border: 1px dashed var(--border-strong);
    border-radius: var(--radius-control);
    color: var(--text-muted);
    text-align: center;
  }

  .state strong {
    color: var(--text);
    font-size: 13px;
  }

  .filtered-empty {
    padding: 23px 16px;
  }

  .loading-mark {
    width: 20px;
    height: 20px;
    border: 2px solid var(--border-strong);
    border-top-color: var(--primary);
    border-radius: 50%;
    animation: spin 0.8s linear infinite;
  }

  .general-error,
  .error-hint {
    margin: 0;
    color: var(--danger);
    font-size: 12px;
    font-weight: 650;
  }

  .hint {
    padding: 8px 10px;
    border-radius: var(--radius-control);
    background: var(--surface-muted);
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

  @keyframes spin {
    to {
      transform: rotate(360deg);
    }
  }

  @media (max-width: 700px) {
    .section-heading,
    .table-toolbar {
      align-items: stretch;
      flex-direction: column;
    }

    .summary {
      text-align: left;
    }

    .search-field {
      width: 100%;
    }

    .filter-toggle {
      white-space: normal;
    }
  }
</style>
