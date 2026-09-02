import { canonicalDecimal } from '$lib/design/decimal';

export type TransferQuantityLine = {
  productID: string;
  variantID: string;
  variantRequired?: boolean;
  quantity: string;
  availableQuantity: string;
  variants: readonly unknown[];
  loading: boolean;
  error: string;
};

export type TransferEntryLine = {
  productID: string;
  variantRequired: boolean;
  quantity?: string;
  variantQuantities?: Readonly<Record<string, string>>;
};

export type TransferApiLine = {
  product_id: string;
  quantity: string;
  variant_id?: string;
};

function normalize(value: string) {
  const canonical = canonicalDecimal(value);
  const [integer = '0', fraction = ''] = canonical.replace(/^-/, '').split('.');
  return {
    negative: canonical.startsWith('-'),
    integer: integer.replace(/^0+(?=\d)/, '') || '0',
    fraction: fraction.replace(/0+$/, '')
  };
}

export function compareDecimal(left: string, right: string) {
  const a = normalize(left);
  const b = normalize(right);
  if (a.negative !== b.negative) return a.negative ? -1 : 1;
  const sign = a.negative ? -1 : 1;
  if (a.integer.length !== b.integer.length)
    return sign * (a.integer.length > b.integer.length ? 1 : -1);
  if (a.integer !== b.integer) return sign * (a.integer > b.integer ? 1 : -1);
  const scale = Math.max(a.fraction.length, b.fraction.length);
  const af = a.fraction.padEnd(scale, '0');
  const bf = b.fraction.padEnd(scale, '0');
  if (af === bf) return 0;
  return sign * (af > bf ? 1 : -1);
}

export function addPositiveDecimals(left: string, right: string) {
  const values = [canonicalDecimal(left) || '0', canonicalDecimal(right) || '0'];
  const parts = values.map((value) => {
    const [integer = '0', fraction = ''] = value.split('.');
    return { integer: integer.replace(/^0+(?=\d)/, '') || '0', fraction };
  });
  const scale = Math.max(...parts.map((part) => part.fraction.length));
  const units = parts.reduce((sum, part) => {
    const raw = scale === 0 ? part.integer : `${part.integer}${part.fraction.padEnd(scale, '0')}`;
    return sum + BigInt(raw);
  }, 0n);
  if (scale === 0) return units.toString();
  const raw = units.toString().padStart(scale + 1, '0');
  const fraction = raw.slice(-scale).replace(/0+$/, '');
  return fraction ? `${raw.slice(0, -scale)}.${fraction}` : raw.slice(0, -scale);
}

function isPositiveQuantity(value: string | undefined) {
  const quantity = canonicalDecimal(value ?? '');
  return /^(?:\d+(?:\.\d+)?|\.\d+)$/.test(quantity) && compareDecimal(quantity, '0') > 0;
}

/**
 * Converts the UI's product-centric transfer lines into the API's position
 * lines. Empty variant IDs are deliberately omitted for non-variant products,
 * and zero/blank matrix cells never become transfer lines.
 */
export function buildTransferApiLines(lines: readonly TransferEntryLine[]): TransferApiLine[] {
  return lines.flatMap((line) => {
    if (!line.productID) return [];
    if (!line.variantRequired) {
      if (!isPositiveQuantity(line.quantity)) return [];
      return [{ product_id: line.productID, quantity: canonicalDecimal(line.quantity ?? '') }];
    }

    return Object.entries(line.variantQuantities ?? {}).flatMap(([variantID, value]) => {
      if (!variantID || !isPositiveQuantity(value)) return [];
      return [
        {
          product_id: line.productID,
          variant_id: variantID,
          quantity: canonicalDecimal(value)
        }
      ];
    });
  });
}

/**
 * Validates the quantity invariant used by the transfer form. The available
 * balance belongs to the exact product/variant position shown on the line;
 * repeated lines for that same position must be checked as one total.
 */
export function validateTransferQuantities(lines: readonly TransferQuantityLine[]) {
  const requestedByPosition = new Map<string, string>();

  for (const [index, line] of lines.entries()) {
    if (!line.productID) return `${index + 1}. satır için stok seçin.`;
    if (line.loading) return `${index + 1}. satırın stok bilgileri yükleniyor.`;
    if (line.error) return `${index + 1}. satırın stok bilgisi doğrulanamadı.`;
    if (line.variantRequired && !line.variantID) {
      return `${index + 1}. satır için varyant seçin.`;
    }

    const quantity = canonicalDecimal(line.quantity);
    if (!/^(?:\d+(?:\.\d+)?|\.\d+)$/.test(quantity) || compareDecimal(quantity, '0') <= 0) {
      return `${index + 1}. satır için geçerli bir miktar girin.`;
    }
    if (!line.availableQuantity.trim()) {
      return `${index + 1}. satırın güncel stok bakiyesi doğrulanamadı.`;
    }

    const positionKey = `${line.productID}:${line.variantID}`;
    const requested = addPositiveDecimals(requestedByPosition.get(positionKey) ?? '0', quantity);
    requestedByPosition.set(positionKey, requested);
    if (compareDecimal(requested, line.availableQuantity) > 0) {
      return `${index + 1}. satırdaki toplam miktar çıkış deposundaki kullanılabilir bakiyeyi aşamaz.`;
    }
  }

  return undefined;
}
