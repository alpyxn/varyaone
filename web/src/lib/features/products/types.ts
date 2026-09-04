export type ProductKind = 'PHYSICAL' | 'SERVICE';

export type ProductUnit = {
  code: string;
  name: string;
  is_base: boolean;
  conversion_factor: string;
  decimal_scale: number;
};

export type ProductBarcode = {
  id?: string;
  barcode: string;
  barcode_type: string;
  is_primary: boolean;
};

export const MAX_VARIANT_DIMENSIONS = 5;
export const MAX_VARIANT_COMBINATIONS = 1000;

export type VariantOption = {
  id: string;
  definition_id: string;
  code: string;
  name: string;
  sort_order?: number;
  is_active: boolean;
  version: number;
};

export type VariantDefinition = {
  id: string;
  company_id?: string;
  code: string;
  name: string;
  description?: string;
  is_active: boolean;
  version: number;
  options: VariantOption[];
};

export type ProductVariantDimension = {
  definition_id: string;
  definition_code?: string;
  definition_name?: string;
  option_ids: string[];
  options?: VariantOption[];
};

export type ProductVariantConfig = {
  product_id?: string;
  enabled: boolean;
  identity_locked?: boolean;
  movement_started?: boolean;
  version?: number;
  dimensions: ProductVariantDimension[];
};

export type VariantStockPosition = {
  warehouse_id?: string;
  warehouse_code?: string;
  warehouse_name?: string;
  location_id?: string;
  location_code?: string;
  location_name?: string;
  physical_quantity: string;
  reserved_quantity: string;
  available_quantity: string;
  stock_unit?: string;
};

export type ProductVariant = {
  id: string;
  product_id: string;
  variant_code: string;
  sku?: string;
  attributes: Record<string, unknown>;
  values?: Array<{
    definition_id: string;
    option_id: string;
    definition_code?: string;
    definition_name?: string;
    option_code?: string;
    option_name?: string;
  }>;
  barcodes: ProductBarcode[];
  is_active: boolean;
  identity_locked?: boolean;
  physical_quantity?: string;
  reserved_quantity?: string;
  available_quantity?: string;
  stock_unit?: string;
  stock_positions?: VariantStockPosition[];
  purchase_price_override?: string;
  sales_price_override?: string;
  purchase_price?: string;
  sales_price?: string;
  purchase_price_inherited?: boolean;
  sales_price_inherited?: boolean;
  price_entries?: Array<{
    price_list_id: string;
    entry_id?: string;
    unit_price?: string;
    valid_from?: string;
    valid_to?: string;
    version?: number;
  }>;
  created_at?: string;
  updated_at?: string;
  version: number;
};

export type ProductVariantList = { items: ProductVariant[] };
export type VariantDefinitionList = { items: VariantDefinition[] };
export type ProductVariantConfigResponse = ProductVariantConfig;

export function emptyVariantConfig(productID = ''): ProductVariantConfig {
  return { product_id: productID, enabled: false, dimensions: [] };
}

export type ProductCategory = {
  id: string;
  code: string;
  name: string;
  is_active: boolean;
  version: number;
};

export type ProductBrand = ProductCategory;

export type ProductTaxComponent = {
  tax_definition_id: string;
  tax_rate_id?: string;
  rate_id?: string;
  calculation_type?: 'PERCENTAGE' | 'FIXED_AMOUNT' | 'QUANTITY_BASED';
  included_in_tax_base?: boolean;
  metadata?: Record<string, unknown>;
};

export type ProductTaxProfile = {
  direction?: 'PURCHASE' | 'SALES';
  treatment: 'STANDARD' | 'WITHHOLDING' | 'EXEMPT' | 'NOT_APPLICABLE';
  tax_definition_id?: string;
  tax_rate_id?: string;
  vat_rate_id?: string;
  tax_included?: boolean;
  withholding_rule_id?: string;
  withholding_code?: string;
  withholding_rate?: string;
  withholding_numerator?: number;
  withholding_denominator?: number;
  exemption_id?: string;
  exemption_code?: string;
  tax_note?: string;
  components: ProductTaxComponent[];
  tax_code?: string;
  rate?: string;
  version?: number;
};

export type ProductInput = {
  code: string;
  sku?: string;
  name: string;
  kind: ProductKind;
  description: string;
  purchase_price: string;
  sales_price: string;
  custom_description_1: string;
  custom_description_2: string;
  custom_description_3: string;
  purchase_tax_type: string;
  sales_tax_type: string;
  purchase_tax_rate: string;
  sales_tax_rate: string;
  purchase_tax_included: boolean;
  sales_tax_included: boolean;
  excise_tax_rate: string;
  withholding_code: string;
  withholding_rate: string;
  exemption_code: string;
  tax_note: string;
  category_id: string;
  brand_id: string;
  is_active: boolean;
  base_unit: string;
  units: Array<{
    code: string;
    is_base: boolean;
    conversion_factor: string;
    decimal_scale: number;
  }>;
  barcodes: ProductBarcode[];
  purchase_tax_profile?: ProductTaxProfile;
  sales_tax_profile?: ProductTaxProfile;
};

export type Product = ProductInput & {
  id: string;
  sku: string;
  category_name: string;
  brand_name: string;
  barcode_summary: string;
  unit_summary: string;
  units: ProductUnit[];
  is_active: boolean;
  variants_enabled: boolean;
  variant_summary: { count: number; active_count: number };
  physical_quantity: string;
  reserved_quantity: string;
  available_quantity: string;
  stock_unit: string;
  net_price: string;
  version: number;
  created_at?: string;
  updated_at?: string;
};

export type ProductList = { items: Product[]; next_cursor?: string };
export type ProductReferenceList<T> = { items: T[] };

export function emptyProduct(): ProductInput {
  return {
    code: '',
    sku: '',
    name: '',
    kind: 'PHYSICAL',
    description: '',
    purchase_price: '',
    sales_price: '',
    custom_description_1: '',
    custom_description_2: '',
    custom_description_3: '',
    purchase_tax_type: 'KDV',
    sales_tax_type: 'KDV',
    purchase_tax_rate: '0',
    sales_tax_rate: '0',
    purchase_tax_included: false,
    sales_tax_included: false,
    excise_tax_rate: '0',
    withholding_code: '',
    withholding_rate: '0',
    exemption_code: '',
    tax_note: '',
    category_id: '',
    brand_id: '',
    is_active: true,
    base_unit: 'ADET',
    units: [{ code: 'ADET', is_base: true, conversion_factor: '1', decimal_scale: 0 }],
    barcodes: []
  };
}

export function normalizeProductInput(input: ProductInput): ProductInput {
  const units = input.units
    .map((unit) => ({
      ...unit,
      code: unit.code.trim().toUpperCase(),
      conversion_factor: normalizeDecimal(unit.conversion_factor) || '1',
      decimal_scale: Number.isFinite(unit.decimal_scale)
        ? Math.max(0, Math.min(8, unit.decimal_scale))
        : 3
    }))
    .filter((unit) => unit.code);
  const baseUnit =
    input.base_unit.trim().toUpperCase() || units.find((unit) => unit.is_base)?.code || '';
  const normalizeTaxProfile = (profile?: ProductTaxProfile, taxIncludedFallback = false) => {
    if (!profile) return profile;
    const components = (profile.components ?? [])
      .filter((component) => {
        const manualRate = String(component.metadata?.rate ?? '').trim();
        return Boolean(component.tax_rate_id || component.rate_id || manualRate);
      })
      .map((component) => ({
        tax_definition_id: component.tax_definition_id,
        ...(component.tax_rate_id ? { tax_rate_id: component.tax_rate_id } : {}),
        ...(component.rate_id ? { rate_id: component.rate_id } : {}),
        calculation_type: component.calculation_type || 'PERCENTAGE',
        included_in_tax_base: Boolean(component.included_in_tax_base),
        ...(component.metadata ? { metadata: component.metadata } : {})
      }));
    return {
      treatment: profile.treatment,
      ...(profile.tax_definition_id ? { tax_definition_id: profile.tax_definition_id } : {}),
      ...(profile.tax_rate_id || profile.vat_rate_id
        ? { tax_rate_id: profile.tax_rate_id || profile.vat_rate_id }
        : {}),
      ...(profile.tax_code ? { tax_code: profile.tax_code } : {}),
      components,
      rate: normalizePrice(profile.rate ?? ''),
      tax_included: Boolean(profile.tax_included ?? taxIncludedFallback),
      ...(profile.withholding_rule_id ? { withholding_rule_id: profile.withholding_rule_id } : {}),
      ...(profile.withholding_code ? { withholding_code: profile.withholding_code } : {}),
      withholding_rate: normalizePrice(profile.withholding_rate ?? ''),
      ...(profile.withholding_numerator !== undefined
        ? { withholding_numerator: profile.withholding_numerator }
        : {}),
      ...(profile.withholding_denominator !== undefined
        ? { withholding_denominator: profile.withholding_denominator }
        : {}),
      ...(profile.exemption_id ? { exemption_id: profile.exemption_id } : {}),
      ...(profile.exemption_code ? { exemption_code: profile.exemption_code } : {}),
      ...(profile.tax_note ? { tax_note: profile.tax_note } : {})
    };
  };
  return {
    code: input.code.trim().toUpperCase(),
    sku: input.code.trim().toUpperCase(),
    name: input.name.trim(),
    kind: input.kind,
    description: input.description.trim(),
    purchase_price: normalizePrice(input.purchase_price),
    sales_price: normalizePrice(input.sales_price),
    custom_description_1: input.custom_description_1.trim(),
    custom_description_2: input.custom_description_2.trim(),
    custom_description_3: input.custom_description_3.trim(),
    purchase_tax_type: input.purchase_tax_type || 'KDV',
    sales_tax_type: input.sales_tax_type || 'KDV',
    purchase_tax_rate: normalizePrice(input.purchase_tax_rate),
    sales_tax_rate: normalizePrice(input.sales_tax_rate),
    purchase_tax_included: Boolean(input.purchase_tax_included),
    sales_tax_included: Boolean(input.sales_tax_included),
    excise_tax_rate: normalizePrice(input.excise_tax_rate),
    withholding_code: input.withholding_code.trim(),
    withholding_rate: normalizePrice(input.withholding_rate),
    exemption_code: input.exemption_code.trim(),
    tax_note: input.tax_note.trim(),
    category_id: input.category_id || '',
    brand_id: input.brand_id || '',
    is_active: Boolean(input.is_active),
    base_unit: baseUnit,
    units: units.map((unit) => ({ ...unit, is_base: unit.code === baseUnit })),
    barcodes: input.barcodes.map((item) => ({
      barcode: item.barcode.trim(),
      barcode_type: item.barcode_type,
      is_primary: Boolean(item.is_primary)
    })),
    purchase_tax_profile: normalizeTaxProfile(
      input.purchase_tax_profile,
      Boolean(input.purchase_tax_included)
    ),
    sales_tax_profile: normalizeTaxProfile(
      input.sales_tax_profile,
      Boolean(input.sales_tax_included)
    )
  };
}

export function validateProductInput(
  input: ProductInput,
  options: { allowBlankCode?: boolean } = {}
): string | undefined {
  const normalized = normalizeProductInput(input);
  if (!normalized.name) return 'Stok/hizmet adı gereklidir.';
  if (options.allowBlankCode === false && !normalized.code) {
    return 'Mevcut stok kartında stok kodu boş bırakılamaz.';
  }
  if (normalized.purchase_price === '' || normalized.sales_price === '') {
    return 'Alış ve satış fiyatı sıfır veya pozitif olmalıdır.';
  }
  if (!normalized.base_unit) return 'Temel birim seçin.';
  if (normalized.units.length > 1) {
    return 'Bir stok kartında yalnızca bir stok birimi kullanılabilir.';
  }
  if (
    normalized.units.length === 0 ||
    normalized.units.filter((unit) => unit.is_base).length !== 1
  ) {
    return 'Tam olarak bir temel birim seçin.';
  }
  if (
    normalized.units.some((unit) => !unit.conversion_factor || Number(unit.conversion_factor) <= 0)
  ) {
    return 'Birim dönüşüm katsayıları sıfırdan büyük olmalıdır.';
  }
  const barcodes = normalized.barcodes.map((item) => item.barcode.trim()).filter(Boolean);
  if (new Set(barcodes).size !== barcodes.length) return 'Aynı barkod birden fazla eklenemez.';
  if (normalized.barcodes.filter((item) => item.is_primary && item.barcode.trim()).length > 1) {
    return 'Yalnızca bir barkod ana barkod olabilir.';
  }
  for (const [label, profile] of [
    ['alış', normalized.purchase_tax_profile],
    ['satış', normalized.sales_tax_profile]
  ] as const) {
    if (!profile) continue;
    const rateID = profile.tax_rate_id || profile.vat_rate_id || '';
    if (profile.treatment === 'STANDARD' && !rateID && !String(profile.rate ?? '').trim())
      return `${label} vergi profili için KDV oranı girin.`;
    if (profile.treatment === 'WITHHOLDING' && (!rateID || !profile.withholding_rule_id)) {
      return `${label} vergi profili için KDV oranı ve tevkifat tanımı seçin.`;
    }
    if (profile.treatment === 'EXEMPT' && !profile.exemption_id) {
      return `${label} vergi profili için istisna/muafiyet seçin.`;
    }
  }
  return undefined;
}

function normalizeDecimal(value: string): string | undefined {
  const canonical = value.trim().replace(/\s/g, '').replace(',', '.');
  const match = /^(\d+)(?:\.(\d{1,8}))?$/.exec(canonical);
  if (!match) return undefined;
  return canonical.replace(/(\.\d*?)0+$/, '$1').replace(/\.$/, '');
}

function normalizePrice(value: string): string {
  const compact = value.trim().replace(/\s/g, '');
  const comma = compact.lastIndexOf(',');
  const dot = compact.lastIndexOf('.');
  let canonical = compact;
  if (comma >= 0 && dot >= 0) {
    // Turkish input uses the comma for decimals and the dot for thousands;
    // also accept the reverse form when a value comes from an API/export.
    canonical =
      comma > dot ? compact.replace(/\./g, '').replace(',', '.') : compact.replace(/,/g, '');
  } else if (comma >= 0) {
    canonical = compact.replace(',', '.');
  }
  if (!canonical) return '0';
  const match = /^(\d+)(?:\.(\d{1,8}))?$/.exec(canonical);
  if (!match) return '';
  const normalized = canonical.replace(/(\.\d*?)0+$/, '$1').replace(/\.$/, '');
  return normalized || '0';
}
