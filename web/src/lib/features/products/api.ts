import { api } from '$lib/api';
import type {
  Product,
  ProductBrand,
  ProductCategory,
  ProductInput,
  ProductList,
  ProductReferenceList,
  ProductUnit,
  ProductVariant,
  ProductVariantConfig,
  ProductVariantConfigResponse,
  ProductVariantList,
  VariantDefinition,
  VariantDefinitionList,
  VariantOption
} from './types';

export const listProducts = (params: URLSearchParams, signal?: AbortSignal) =>
  api<ProductList>(`/products?${params}`, { signal });

export const getProduct = (id: string) => api<Product>(`/products/${encodeURIComponent(id)}`);

export const createProduct = (input: ProductInput) =>
  api<Product>('/products', { method: 'POST', body: JSON.stringify(input) });

export const updateProduct = (id: string, version: number, input: ProductInput) =>
  api<Product>(`/products/${encodeURIComponent(id)}`, {
    method: 'PUT',
    headers: { 'If-Match': `"${version}"` },
    body: JSON.stringify(input)
  });

export const deactivateProduct = (id: string, version: number) =>
  api<Product>(`/products/${encodeURIComponent(id)}/deactivate`, {
    method: 'POST',
    headers: { 'If-Match': `"${version}"` },
    body: '{}'
  });

export const listProductUnits = (signal?: AbortSignal) =>
  api<ProductReferenceList<ProductUnit>>('/product-references/units', { signal }).then(
    (result) => result.items
  );

export const listProductCategories = (signal?: AbortSignal) =>
  api<ProductReferenceList<ProductCategory>>('/product-references/categories', { signal }).then(
    (result) => result.items
  );

export const listProductBrands = (signal?: AbortSignal) =>
  api<ProductReferenceList<ProductBrand>>('/product-references/brands', { signal }).then(
    (result) => result.items
  );

export const createProductCategory = (name: string) =>
  api<ProductCategory>('/product-references/categories', {
    method: 'POST',
    body: JSON.stringify({ name })
  });
export const createProductBrand = (name: string) =>
  api<ProductBrand>('/product-references/brands', {
    method: 'POST',
    body: JSON.stringify({ name })
  });
export const setProductReferenceActive = (
  kind: 'categories' | 'brands',
  id: string,
  version: number,
  active: boolean
) =>
  api<ProductCategory | ProductBrand>(
    `/product-references/${kind}/${encodeURIComponent(id)}/${active ? 'activate' : 'deactivate'}`,
    { method: 'POST', headers: { 'If-Match': `"${version}"` }, body: '{}' }
  );

export const listVariantDefinitions = (includeInactive = true, signal?: AbortSignal) => {
  const params = new URLSearchParams({
    include_inactive: String(includeInactive),
    include_options: 'true'
  });
  return api<VariantDefinitionList>(`/variant-definitions?${params}`, { signal });
};

export const createVariantDefinition = (
  input: Pick<VariantDefinition, 'code' | 'name'> & { description?: string }
) =>
  api<VariantDefinition>('/variant-definitions', { method: 'POST', body: JSON.stringify(input) });

export const updateVariantDefinition = (
  id: string,
  version: number,
  input: Pick<VariantDefinition, 'code' | 'name'> & { description?: string }
) =>
  api<VariantDefinition>(`/variant-definitions/${encodeURIComponent(id)}`, {
    method: 'PUT',
    headers: { 'If-Match': `"${version}"` },
    body: JSON.stringify(input)
  });

export const setVariantDefinitionActive = (id: string, version: number, active: boolean) =>
  api<VariantDefinition>(
    `/variant-definitions/${encodeURIComponent(id)}/${active ? 'activate' : 'deactivate'}`,
    { method: 'POST', headers: { 'If-Match': `"${version}"` }, body: '{}' }
  );

export const createVariantOption = (
  definitionID: string,
  input: Pick<VariantOption, 'code' | 'name'> & { sort_order?: number }
) =>
  api<VariantOption>(`/variant-definitions/${encodeURIComponent(definitionID)}/options`, {
    method: 'POST',
    body: JSON.stringify(input)
  });

export const updateVariantOption = (
  definitionID: string,
  optionID: string,
  version: number,
  input: Pick<VariantOption, 'code' | 'name'> & { sort_order?: number }
) =>
  api<VariantOption>(
    `/variant-definitions/${encodeURIComponent(definitionID)}/options/${encodeURIComponent(optionID)}`,
    {
      method: 'PUT',
      headers: { 'If-Match': `"${version}"` },
      body: JSON.stringify(input)
    }
  );

export const setVariantOptionActive = (
  definitionID: string,
  optionID: string,
  version: number,
  active: boolean
) =>
  api<VariantOption>(
    `/variant-definitions/${encodeURIComponent(definitionID)}/options/${encodeURIComponent(optionID)}/${active ? 'activate' : 'deactivate'}`,
    { method: 'POST', headers: { 'If-Match': `"${version}"` }, body: '{}' }
  );

type VariantConfigWire = {
  product_id?: string;
  variants_enabled: boolean;
  identity_locked?: boolean;
  movement_started?: boolean;
  version?: number;
  definitions: Array<{
    definition_id: string;
    code: string;
    name: string;
    position: number;
    options: VariantOption[];
  }>;
  combination_count?: number;
};

type ProductVariantConfigInput = {
  variants_enabled: boolean;
  definitions: Array<{
    definition_id: string;
    position: number;
    option_ids: string[];
  }>;
};

function fromVariantConfigWire(wire: VariantConfigWire): ProductVariantConfigResponse {
  return {
    product_id: wire.product_id,
    enabled: wire.variants_enabled,
    identity_locked: wire.identity_locked,
    movement_started: wire.movement_started,
    version: wire.version,
    dimensions: wire.definitions.map((definition) => ({
      definition_id: definition.definition_id,
      definition_code: definition.code,
      definition_name: definition.name,
      option_ids: definition.options.map((option) => option.id),
      options: definition.options
    }))
  };
}

function toVariantConfigWire(input: ProductVariantConfig): ProductVariantConfigInput {
  return {
    variants_enabled: input.enabled,
    definitions: input.dimensions.map((dimension, index) => ({
      definition_id: dimension.definition_id,
      position: index + 1,
      option_ids: dimension.option_ids
    }))
  };
}

export const getProductVariantConfig = (productID: string) =>
  api<VariantConfigWire>(`/products/${encodeURIComponent(productID)}/variant-config`).then(
    fromVariantConfigWire
  );

export const updateProductVariantConfig = (
  productID: string,
  version: number | undefined,
  input: ProductVariantConfig
) =>
  api<VariantConfigWire>(`/products/${encodeURIComponent(productID)}/variant-config`, {
    method: 'PUT',
    ...(version === undefined ? {} : { headers: { 'If-Match': `"${version}"` } }),
    body: JSON.stringify(toVariantConfigWire(input))
  }).then(fromVariantConfigWire);

export const listProductVariants = (productID: string, signal?: AbortSignal) =>
  api<ProductVariantList>(`/products/${encodeURIComponent(productID)}/variants`, { signal });

export const generateProductVariants = (productID: string) =>
  api<ProductVariantList>(`/products/${encodeURIComponent(productID)}/variants/generate`, {
    method: 'POST',
    body: '{}'
  });

export type ProductVariantUpdate = {
  variant_code?: string;
  barcodes?: ProductVariant['barcodes'];
  is_active?: boolean;
  purchase_price_override?: string | null;
  sales_price_override?: string | null;
  price_entries?: ProductVariant['price_entries'];
};

export type ProductVariantBarcodeUpdate = {
  barcodes: ProductVariant['barcodes'];
};

type VariantBarcodeWire = {
  barcode?: string;
  barcode_type?: string;
  is_primary?: boolean;
};

type VariantPriceEntryWire = {
  price_list_id?: string;
  entry_id?: string;
  unit_price?: string;
  valid_from?: string;
  valid_to?: string | null;
  version?: number;
};

type VariantInputWire = {
  variant_code?: string;
  barcodes?: VariantBarcodeWire[];
  is_active?: boolean;
  purchase_price_override?: string | null;
  sales_price_override?: string | null;
  price_entries?: VariantPriceEntryWire[];
};

type VariantBarcodeUpdateWire = {
  barcodes: VariantBarcodeWire[];
};

function setDefined(target: Record<string, unknown>, key: string, value: unknown): void {
  if (value !== undefined) target[key] = value;
}

function toVariantBarcodeWire(barcode: ProductVariant['barcodes'][number]): VariantBarcodeWire {
  const wire: Record<string, unknown> = {};
  setDefined(wire, 'barcode', barcode.barcode);
  setDefined(wire, 'barcode_type', barcode.barcode_type);
  setDefined(wire, 'is_primary', barcode.is_primary);
  return wire as VariantBarcodeWire;
}

export function toVariantBarcodeUpdateWire(
  input: ProductVariantBarcodeUpdate
): VariantBarcodeUpdateWire {
  return {
    barcodes: input.barcodes.map(toVariantBarcodeWire)
  };
}

function toVariantPriceEntryWire(
  entry: NonNullable<ProductVariant['price_entries']>[number]
): VariantPriceEntryWire {
  const wire: Record<string, unknown> = {};
  setDefined(wire, 'price_list_id', entry.price_list_id);
  setDefined(wire, 'entry_id', entry.entry_id);
  setDefined(wire, 'unit_price', entry.unit_price);
  setDefined(wire, 'valid_from', entry.valid_from);
  setDefined(wire, 'valid_to', entry.valid_to);
  setDefined(wire, 'version', entry.version);
  return wire as VariantPriceEntryWire;
}

export function toVariantInputWire(input: ProductVariantUpdate): VariantInputWire {
  const wire: VariantInputWire = {};
  if (input.variant_code !== undefined) wire.variant_code = input.variant_code;
  if (input.barcodes !== undefined) {
    wire.barcodes = input.barcodes.map(toVariantBarcodeWire);
  }
  if (input.is_active !== undefined) wire.is_active = input.is_active;
  if (input.purchase_price_override !== undefined) {
    wire.purchase_price_override = input.purchase_price_override;
  }
  if (input.sales_price_override !== undefined) {
    wire.sales_price_override = input.sales_price_override;
  }
  if (input.price_entries !== undefined) {
    wire.price_entries = input.price_entries.map(toVariantPriceEntryWire);
  }
  return wire;
}

export const updateProductVariant = (
  productID: string,
  variantID: string,
  version: number,
  input: ProductVariantUpdate
) =>
  api<ProductVariant>(
    `/products/${encodeURIComponent(productID)}/variants/${encodeURIComponent(variantID)}`,
    {
      method: 'PUT',
      headers: { 'If-Match': `"${version}"` },
      body: JSON.stringify(toVariantInputWire(input))
    }
  );

export const updateProductVariantBarcodes = (
  productID: string,
  variantID: string,
  version: number,
  input: ProductVariantBarcodeUpdate
) =>
  api<ProductVariant>(
    `/products/${encodeURIComponent(productID)}/variants/${encodeURIComponent(variantID)}/barcodes`,
    {
      method: 'PUT',
      headers: { 'If-Match': `"${version}"` },
      body: JSON.stringify(toVariantBarcodeUpdateWire(input))
    }
  );

export const deactivateProductVariant = (productID: string, variantID: string, version: number) =>
  api<ProductVariant>(
    `/products/${encodeURIComponent(productID)}/variants/${encodeURIComponent(variantID)}/deactivate`,
    { method: 'POST', headers: { 'If-Match': `"${version}"` }, body: '{}' }
  );
