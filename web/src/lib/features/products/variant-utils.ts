import type {
  ProductVariant,
  ProductVariantDimension,
  VariantDefinition,
  VariantOption
} from './types';
import { MAX_VARIANT_COMBINATIONS, MAX_VARIANT_DIMENSIONS } from './types';

export { MAX_VARIANT_COMBINATIONS, MAX_VARIANT_DIMENSIONS } from './types';

export function combinationCount(dimensions: ProductVariantDimension[]): number {
  if (dimensions.length === 0) return 0;
  return dimensions.reduce((total, dimension) => total * dimension.option_ids.length, 1);
}

export function combinationCountLabel(count: number): string {
  return count === 0 ? 'Seçenek bekleniyor' : `${count.toLocaleString('tr-TR')} kombinasyon`;
}

export function variantConfigurationError(
  dimensions: ProductVariantDimension[]
): string | undefined {
  if (dimensions.length > MAX_VARIANT_DIMENSIONS) {
    return `Bir üründe en fazla ${MAX_VARIANT_DIMENSIONS} varyant boyutu seçebilirsiniz.`;
  }
  if (dimensions.some((dimension) => dimension.option_ids.length === 0)) {
    return 'Her boyut için en az bir seçenek seçin.';
  }
  const count = combinationCount(dimensions);
  if (count > MAX_VARIANT_COMBINATIONS) {
    return `Kombinasyon sayısı ${MAX_VARIANT_COMBINATIONS.toLocaleString('tr-TR')} sınırını aşıyor.`;
  }
  return undefined;
}

export function combinationWarning(dimensions: ProductVariantDimension[]): string | undefined {
  const error = variantConfigurationError(dimensions);
  if (error) return error;
  const count = combinationCount(dimensions);
  if (count === 0) return 'Kombinasyon üretmek için boyut ve seçenek seçin.';
  if (count > 250) {
    return `${combinationCountLabel(count)} üretilecek. İşlem kısa sürebilir.`;
  }
  return undefined;
}

export function selectedOptions(
  definition: VariantDefinition,
  dimension: ProductVariantDimension
): VariantOption[] {
  const selected = new Set(dimension.option_ids);
  return definition.options.filter((option) => selected.has(option.id));
}

export function normalizeVariantCodePart(value: string): string {
  return value
    .normalize('NFKD')
    .replace(/[\u0300-\u036f]/g, '')
    .toLocaleUpperCase('tr-TR')
    .replace(/İ/g, 'I')
    .replace(/Ş/g, 'S')
    .replace(/Ğ/g, 'G')
    .replace(/Ü/g, 'U')
    .replace(/Ö/g, 'O')
    .replace(/Ç/g, 'C')
    .replace(/[^A-Z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 24);
}

export function suggestedVariantSku(productCode: string, options: VariantOption[]): string {
  const parts = [productCode, ...options.map((option) => option.code || option.name)]
    .map(normalizeVariantCodePart)
    .filter(Boolean);
  return parts.join('-');
}

export function variantLabel(variant: Pick<ProductVariant, 'values' | 'attributes'>): string {
  if (variant.values?.length) {
    return variant.values
      .map((value) => value?.option_name || value?.option_code || value?.option_id)
      .filter(Boolean)
      .join(' / ');
  }
  const attributes = variant.attributes ?? {};
  return Object.values(attributes)
    .map((value) => (typeof value === 'string' ? value : String(value ?? '')))
    .filter(Boolean)
    .join(' / ');
}
