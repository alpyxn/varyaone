<script lang="ts">
  import { formatQuantity } from '$lib/design/formatters';

  export type TransferMatrixVariant = {
    id: string;
    code?: string;
    name?: string;
    sku?: string;
    availableQuantity?: string;
    physicalQuantity?: string;
    reservedQuantity?: string;
    attributes?: Record<string, unknown>;
    barcodes?: Array<{ barcode?: string }>;
  };

  type Props = {
    productName: string;
    unit?: string;
    variantsEnabled: boolean;
    variants?: readonly TransferMatrixVariant[];
    quantity?: string;
    availableQuantity?: string;
    variantQuantities?: Readonly<Record<string, string>>;
    disabled?: boolean;
    loading?: boolean;
    error?: string;
    onQuantityChange?: (value: string) => void;
    onVariantQuantityChange?: (variantID: string, value: string) => void;
  };

  let {
    productName,
    unit = 'ADET',
    variantsEnabled,
    variants = [],
    quantity = '',
    availableQuantity = '',
    variantQuantities = {},
    disabled = false,
    loading = false,
    error = '',
    onQuantityChange,
    onVariantQuantityChange
  }: Props = $props();

  function displayVariant(variant: TransferMatrixVariant) {
    const attributes = Object.entries(variant.attributes ?? {})
      .filter(([, value]) => value !== undefined && value !== null && value !== '')
      .map(([key, value]) => `${key}: ${String(value)}`)
      .join(' · ');
    return attributes || variant.name || variant.code || variant.sku || 'Tanımsız varyant';
  }

  function variantDetails(variant: TransferMatrixVariant) {
    const attributes = Object.entries(variant.attributes ?? {})
      .filter(([, value]) => value !== undefined && value !== null && value !== '')
      .map(([key, value]) => `${key}: ${String(value)}`)
      .join(' · ');
    const identifiers = [variant.name, variant.code, variant.sku]
      .filter(Boolean)
      .map((value) => String(value).trim())
      .filter((value, index, values) => value && values.indexOf(value) === index);
    const secondary = [attributes, ...identifiers]
      .filter(Boolean)
      .filter((value) => value !== displayVariant(variant));
    return secondary.filter((value, index, values) => values.indexOf(value) === index).join(' · ');
  }

  function hasAvailableStock(variant: TransferMatrixVariant) {
    // An omitted balance means the source projection did not provide a
    // position; keep the field usable and let the backend remain authoritative.
    if (variant.availableQuantity === undefined || variant.availableQuantity.trim() === '') {
      return true;
    }
    const value = Number(variant.availableQuantity.replace(',', '.'));
    return Number.isFinite(value) && value > 0;
  }

  function hasBalance(variant: TransferMatrixVariant) {
    return variant.availableQuantity !== undefined && variant.availableQuantity.trim() !== '';
  }

  function availableLabel(variant: TransferMatrixVariant) {
    if (variant.availableQuantity === undefined || variant.availableQuantity.trim() === '') {
      return 'Bakiye bilgisi yok';
    }
    return hasAvailableStock(variant)
      ? `${formatQuantity(variant.availableQuantity)} ${unit} kullanılabilir`
      : 'Stok yok';
  }

  function valueFor(variantID: string) {
    return variantQuantities[variantID] ?? '';
  }
</script>

{#if !variantsEnabled}
  <div class="simple-product-row">
    <div class="product-copy">
      <div class="variant-title">
        <strong>{productName}</strong>
        <span class:empty={Number(availableQuantity || '0') <= 0} class="available-badge">
          {Number(availableQuantity || '0') > 0
            ? `${formatQuantity(availableQuantity || '0')} ${unit} kullanılabilir`
            : 'Stok yok'}
        </span>
      </div>
      <small>Varyantsız stok</small>
    </div>
    <label>
      <span>Miktar</span>
      <div class="quantity-input">
        <input
          aria-label={`${productName} miktarı`}
          type="text"
          inputmode="decimal"
          value={quantity}
          disabled={disabled || loading}
          oninput={(event) => onQuantityChange?.(event.currentTarget.value)}
        />
        <small>{unit}</small>
      </div>
    </label>
  </div>
{:else}
  <section class="matrix" aria-label={`${productName} varyant miktarları`}>
    <div class="matrix-heading">
      <div>
        <strong>{productName}</strong>
        <p>Yalnızca transfer edilecek varyantların miktarını girin.</p>
      </div>
      <span class="matrix-summary">{variants.length} varyant</span>
    </div>

    {#if loading}
      <div class="state" role="status">Varyantlar ve depo bakiyeleri yükleniyor…</div>
    {:else if error}
      <div class="state error" role="alert">{error}</div>
    {:else if variants.length === 0}
      <div class="state" role="status">Bu stok kartında aktif varyant bulunamadı.</div>
    {:else}
      <div class="table-wrap">
        <table>
          <caption class="sr-only">{productName} varyant stok matrisi</caption>
          <thead>
            <tr>
              <th scope="col">Varyant</th>
              <th scope="col" class="quantity-heading">Transfer miktarı</th>
            </tr>
          </thead>
          <tbody>
            {#each variants as variant (variant.id)}
              <tr>
                <th scope="row">
                  <div class="variant-title">
                    <strong>{displayVariant(variant)}</strong>
                    <span
                      class:empty={hasBalance(variant) && !hasAvailableStock(variant)}
                      class:unknown={!hasBalance(variant)}
                      class="available-badge"
                    >
                      {availableLabel(variant)}
                    </span>
                  </div>
                  {#if variantDetails(variant)}<small>{variantDetails(variant)}</small>{/if}
                </th>
                <td>
                  <div class="quantity-input">
                    <input
                      aria-label={`${displayVariant(variant)} miktarı`}
                      type="text"
                      inputmode="decimal"
                      value={valueFor(variant.id)}
                      disabled={disabled || !hasAvailableStock(variant)}
                      oninput={(event) =>
                        onVariantQuantityChange?.(variant.id, event.currentTarget.value)}
                    />
                    <small>{unit}</small>
                  </div>
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </section>
{/if}

<style>
  .simple-product-row,
  .matrix {
    border: 1px solid var(--border);
    border-radius: 10px;
    background: var(--surface);
  }
  .simple-product-row {
    display: grid;
    grid-template-columns: minmax(0, 1fr) minmax(150px, 220px);
    gap: 16px;
    align-items: end;
    padding: 14px;
  }
  .product-copy,
  label,
  th,
  td {
    display: grid;
    gap: 4px;
  }
  .product-copy small,
  label > span,
  th small,
  .matrix-heading p,
  .matrix-summary,
  .quantity-input small {
    color: var(--text-muted);
    font-size: 11px;
  }
  label > span {
    font-weight: 600;
  }
  .quantity-input {
    display: flex;
    align-items: center;
    gap: 6px;
    justify-content: flex-end;
  }
  .quantity-input input {
    max-width: 150px;
  }
  input:disabled {
    cursor: not-allowed;
    background: var(--surface-muted);
    color: var(--text-muted);
    opacity: 0.7;
  }
  .variant-title {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 10px;
  }
  .available-badge {
    flex: 0 0 auto;
    border-radius: 999px;
    padding: 3px 7px;
    background: color-mix(in srgb, var(--success, #18794e) 10%, transparent);
    color: var(--success, #18794e);
    font-size: 10px;
    font-weight: 700;
  }
  .available-badge.empty {
    background: color-mix(in srgb, var(--danger, #b42318) 8%, transparent);
    color: var(--danger, #b42318);
  }
  .available-badge.unknown {
    background: var(--surface-muted);
    color: var(--text-muted);
  }
  input {
    width: 100%;
    min-width: 0;
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 8px 9px;
    background: var(--background);
    color: var(--text);
    font: inherit;
  }
  input:focus {
    outline: 2px solid color-mix(in srgb, var(--primary) 35%, transparent);
    outline-offset: 1px;
  }
  .matrix-heading {
    display: flex;
    justify-content: space-between;
    gap: 12px;
    padding: 14px 16px 10px;
  }
  .matrix-heading p {
    margin: 4px 0 0;
  }
  .matrix-summary {
    white-space: nowrap;
  }
  .table-wrap {
    overflow-x: auto;
  }
  table {
    width: 100%;
    min-width: 420px;
    border-collapse: collapse;
    font-size: 12px;
  }
  th,
  td {
    border-top: 1px solid var(--border);
    padding: 10px 12px;
    text-align: left;
    vertical-align: middle;
  }
  thead th {
    color: var(--text-muted);
    font-size: 11px;
    font-weight: 700;
  }
  .quantity-heading {
    width: 190px;
    text-align: right;
  }
  .state {
    padding: 16px;
    color: var(--text-muted);
    font-size: 12px;
  }
  .state.error {
    color: var(--danger, #b42318);
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
  @media (max-width: 640px) {
    .simple-product-row {
      grid-template-columns: 1fr;
    }
    .matrix-heading {
      align-items: flex-start;
    }
    .quantity-heading {
      width: 150px;
    }
    th,
    td {
      padding: 9px 10px;
    }
  }
</style>
