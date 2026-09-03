/** Convert a user-entered Turkish decimal into the API's dot-decimal form. */
export function canonicalDecimal(value: string | number | null | undefined): string {
  const compact = String(value ?? '')
    .trim()
    .replace(/[\s\u00a0]/g, '');
  if (!compact) return '';
  const comma = compact.lastIndexOf(',');
  const dot = compact.lastIndexOf('.');
  if (comma >= 0 && dot >= 0) {
    return comma > dot ? compact.replace(/\./g, '').replace(',', '.') : compact.replace(/,/g, '');
  }
  return comma >= 0 ? compact.replace(',', '.') : compact;
}

/** Keep a decimal input exact while removing insignificant fractional zeros. */
export function trimDecimalZeros(value: string | number | null | undefined): string {
  const normalized = canonicalDecimal(value);
  if (!normalized) return '';
  const match = /^([+-]?)(\d+)(?:\.(\d+))?$/.exec(normalized);
  if (!match) return normalized;
  const fraction = (match[3] ?? '').replace(/0+$/, '');
  return `${match[1]}${match[2]}${fraction ? `.${fraction}` : ''}`;
}

export function decimalNumber(value: string | number | null | undefined): number {
  const parsed = Number(canonicalDecimal(value));
  return Number.isFinite(parsed) ? parsed : 0;
}

/** Check whether a decimal string is exactly zero without using IEEE-754. */
export function isZeroDecimal(value: string | number | null | undefined): boolean {
  const normalized = canonicalDecimal(value);
  return /^[+-]?(?:0+|0*\.0+)$/.test(normalized);
}

/** Divide non-negative decimal strings exactly and return at most eight decimals. */
export function divideDecimalStrings(
  left: string | number | null | undefined,
  right: string | number | null | undefined,
  scale = 8
): string {
  const parse = (value: string | number | null | undefined) => {
    const normalized = canonicalDecimal(value);
    const match = /^(\d+)(?:\.(\d+))?$/.exec(normalized);
    if (!match) return null;
    return { digits: BigInt(`${match[1]}${match[2] ?? ''}`), decimals: (match[2] ?? '').length };
  };
  const numerator = parse(left);
  const denominator = parse(right);
  if (!numerator || !denominator || denominator.digits === 0n || scale < 0 || scale > 18) return '';
  const power = (exponent: number) => 10n ** BigInt(exponent);
  const scaled =
    (numerator.digits * power(denominator.decimals) * power(scale)) /
    (denominator.digits * power(numerator.decimals));
  if (scaled === 0n) return '0';
  const whole = scaled / power(scale);
  const fraction = scale > 0 ? scaled % power(scale) : 0n;
  const fractionText = fraction.toString().padStart(scale, '0').replace(/0+$/, '');
  return fractionText ? `${whole}.${fractionText}` : whole.toString();
}

/** Add non-negative decimal quantities without passing through IEEE-754. */
export function addDecimalStrings(
  left: string | null | undefined,
  right: string | null | undefined
) {
  const values = [canonicalDecimal(left), canonicalDecimal(right)].map((value) =>
    value.replace(/^\+/, '')
  );
  const parts = values.map((value) => {
    const [integer = '0', fraction = ''] = value.split('.', 2);
    return { integer: integer || '0', fraction };
  });
  const scale = Math.max(...parts.map((part) => part.fraction.length));
  const total = parts.reduce((sum, part) => {
    const scaled = BigInt(`${part.integer}${part.fraction.padEnd(scale, '0') || ''}` || '0');
    return sum + scaled;
  }, 0n);
  if (total === 0n) return '0';
  const digits = total.toString().padStart(scale + 1, '0');
  if (scale === 0) return digits;
  const integer = digits.slice(0, -scale) || '0';
  const fraction = digits.slice(-scale).replace(/0+$/, '');
  return fraction ? `${integer}.${fraction}` : integer;
}

/** Subtract non-negative decimal strings without passing through IEEE-754. */
export function subtractDecimalStrings(
  left: string | null | undefined,
  right: string | null | undefined
) {
  const values = [canonicalDecimal(left), canonicalDecimal(right)];
  if (!values.every((value) => /^(?:\d+(?:\.\d+)?|\.\d+)$/.test(value))) return '0';
  const parts = values.map((value) => {
    const [integer = '0', fraction = ''] = value.split('.', 2);
    return { integer: integer || '0', fraction };
  });
  const scale = Math.max(...parts.map((part) => part.fraction.length));
  const units = parts.map((part) =>
    BigInt(`${part.integer}${part.fraction.padEnd(scale, '0') || ''}` || '0')
  );
  if (units[0] <= units[1]) return '0';
  const raw = (units[0] - units[1]).toString().padStart(scale + 1, '0');
  if (scale === 0) return raw;
  const fraction = raw.slice(-scale).replace(/0+$/, '');
  return fraction ? `${raw.slice(0, -scale)}.${fraction}` : raw.slice(0, -scale);
}

type SignedDecimal = { units: bigint; scale: number };

function parseSignedDecimal(value: string | number | null | undefined): SignedDecimal | undefined {
  const normalized = canonicalDecimal(value).replace(/^\+/, '');
  const match = /^(-?)(\d+)(?:\.(\d+))?$/.exec(normalized);
  if (!match) return undefined;
  const fraction = match[3] ?? '';
  const magnitude = BigInt(`${match[2]}${fraction}` || '0');
  return { units: match[1] === '-' ? -magnitude : magnitude, scale: fraction.length };
}

function signedDecimalText(units: bigint, scale: number): string {
  if (units === 0n) return '0';
  const negative = units < 0n;
  const digits = (negative ? -units : units).toString().padStart(scale + 1, '0');
  if (scale === 0) return `${negative ? '-' : ''}${digits}`;
  const fraction = digits.slice(-scale).replace(/0+$/, '');
  const integer = digits.slice(0, -scale) || '0';
  return `${negative ? '-' : ''}${integer}${fraction ? `.${fraction}` : ''}`;
}

/** Add signed exact decimal strings without passing through IEEE-754. */
export function addSignedDecimalStrings(
  ...values: Array<string | number | null | undefined>
): string | undefined {
  const parsed = values.map(parseSignedDecimal);
  if (parsed.some((value) => !value)) return undefined;
  const scale = Math.max(...parsed.map((value) => value?.scale ?? 0), 0);
  const units = parsed.reduce(
    (sum, value) => sum + (value?.units ?? 0n) * 10n ** BigInt(scale - (value?.scale ?? 0)),
    0n
  );
  return signedDecimalText(units, scale);
}

/** Multiply exact decimal strings, rounding half-up to the requested scale. */
export function multiplyDecimalStrings(
  left: string | number | null | undefined,
  right: string | number | null | undefined,
  scale = 4
): string | undefined {
  const first = parseSignedDecimal(left);
  const second = parseSignedDecimal(right);
  if (!first || !second || scale < 0 || scale > 18) return undefined;
  const product = first.units * second.units;
  const sourceScale = first.scale + second.scale;
  if (sourceScale <= scale)
    return signedDecimalText(product * 10n ** BigInt(scale - sourceScale), scale);
  const divisor = 10n ** BigInt(sourceScale - scale);
  const sign = product < 0n ? -1n : 1n;
  const magnitude = product < 0n ? -product : product;
  const rounded = (magnitude + divisor / 2n) / divisor;
  return signedDecimalText(sign * rounded, scale);
}

/** Return the exact opposite of a decimal string, or undefined when invalid. */
export function negateDecimalString(value: string | number | null | undefined): string | undefined {
  const parsed = parseSignedDecimal(value);
  return parsed ? signedDecimalText(-parsed.units, parsed.scale) : undefined;
}

/**
 * Compare exact decimal strings: negative when left < right, 0 when equal,
 * positive when left > right. Comparing through Number() would let a value the
 * user typed exactly ("0.1") lose to its binary approximation, which is how a
 * money total silently drifts.
 */
export function compareDecimalStrings(
  left: string | number | null | undefined,
  right: string | number | null | undefined
): number {
  const first = parseSignedDecimal(left);
  const second = parseSignedDecimal(right);
  if (!first || !second) return 0;
  const scale = Math.max(first.scale, second.scale);
  const scaled = (value: SignedDecimal) => value.units * 10n ** BigInt(scale - value.scale);
  const difference = scaled(first) - scaled(second);
  return difference === 0n ? 0 : difference < 0n ? -1 : 1;
}
