export type FilterOperator = 'eq' | 'contains' | 'gte' | 'lte' | 'in';

export type FilterRule = {
  field: string;
  operator: FilterOperator;
  value: string | string[];
};

export type FilterEngineOptions<T> = {
  getSearchValues?: (row: T) => readonly unknown[];
  getFieldValue?: (row: T, field: string) => unknown;
};

export type FilterEngine<T> = {
  normalizeSearch: (query: string) => string;
  matches: (row: T, search?: string, filters?: readonly FilterRule[]) => boolean;
  filter: (rows: readonly T[], search?: string, filters?: readonly FilterRule[]) => T[];
};

const DECIMAL_PATTERN = /^(-?)(\d+)(?:[.,](\d+))?$/;

/**
 * Make user-entered text predictable for both local and server-side filtering.
 * Turkish letters are folded so `İstanbul`, `istanbul` and `ISTANBUL` behave
 * alike, while punctuation becomes a separator between search tokens.
 */
export function normalizeFilterText(value: unknown): string {
  if (value === null || value === undefined) return '';
  const text = String(value)
    .normalize('NFKD')
    .replace(/\p{M}/gu, '')
    .toLocaleLowerCase('tr-TR')
    .replace(/[ı]/g, 'i')
    .replace(/[ğ]/g, 'g')
    .replace(/[ç]/g, 'c')
    .replace(/[ş]/g, 's')
    .replace(/[ö]/g, 'o')
    .replace(/[ü]/g, 'u');
  return text
    .replace(/[^\p{L}\p{N}]+/gu, ' ')
    .trim()
    .replace(/\s+/g, ' ');
}

export function tokenizeFilterQuery(query: unknown): string[] {
  const normalized = normalizeFilterText(query);
  return normalized ? normalized.split(' ') : [];
}

function collectSearchableValues(value: unknown, output: string[], seen: Set<object>): void {
  if (value === null || value === undefined) return;
  if (typeof value === 'string' || typeof value === 'number' || typeof value === 'bigint') {
    output.push(String(value));
    return;
  }
  if (typeof value === 'boolean') {
    output.push(value ? 'true' : 'false');
    return;
  }
  if (value instanceof Date) {
    output.push(value.toISOString());
    return;
  }
  if (typeof value !== 'object') return;
  if (seen.has(value)) return;
  seen.add(value);
  if (Array.isArray(value)) {
    for (const item of value) collectSearchableValues(item, output, seen);
  } else {
    for (const item of Object.values(value)) collectSearchableValues(item, output, seen);
  }
  seen.delete(value);
}

export function searchableText(value: unknown): string {
  const values: string[] = [];
  collectSearchableValues(value, values, new Set());
  return normalizeFilterText(values.join(' '));
}

function matchesSearchTokens(value: unknown, tokens: readonly string[]): boolean {
  if (!tokens.length) return true;
  const haystack = searchableText(value);
  return tokens.every((token) => haystack.includes(token));
}

/** Every non-empty token must occur somewhere in the supplied value. */
export function matchesSearch(value: unknown, query: unknown): boolean {
  return matchesSearchTokens(value, tokenizeFilterQuery(query));
}

function getPathValue(row: unknown, field: string): unknown {
  return field.split('.').reduce<unknown>((current, part) => {
    if (current === null || current === undefined || typeof current !== 'object') return undefined;
    return (current as Record<string, unknown>)[part];
  }, row);
}

function decimalParts(
  value: unknown
): { negative: boolean; integer: string; fraction: string } | null {
  const raw = String(value ?? '').trim();
  const match = DECIMAL_PATTERN.exec(raw);
  if (!match) return null;
  return {
    negative: match[1] === '-',
    integer: match[2].replace(/^0+(?=\d)/, ''),
    fraction: (match[3] ?? '').replace(/0+$/, '')
  };
}

function compareDecimals(
  left: NonNullable<ReturnType<typeof decimalParts>>,
  right: NonNullable<ReturnType<typeof decimalParts>>
): number {
  if (left.negative !== right.negative) return left.negative ? -1 : 1;
  const sign = left.negative ? -1 : 1;
  if (left.integer.length !== right.integer.length) {
    return sign * (left.integer.length < right.integer.length ? -1 : 1);
  }
  if (left.integer !== right.integer) return sign * (left.integer < right.integer ? -1 : 1);
  const scale = Math.max(left.fraction.length, right.fraction.length);
  const leftFraction = left.fraction.padEnd(scale, '0');
  const rightFraction = right.fraction.padEnd(scale, '0');
  if (leftFraction === rightFraction) return 0;
  return sign * (leftFraction < rightFraction ? -1 : 1);
}

function compareValues(left: unknown, right: unknown): number {
  const leftDecimal = decimalParts(left);
  const rightDecimal = decimalParts(right);
  if (leftDecimal && rightDecimal) return compareDecimals(leftDecimal, rightDecimal);

  const leftDate = typeof left === 'string' ? Date.parse(left) : Number.NaN;
  const rightDate = typeof right === 'string' ? Date.parse(right) : Number.NaN;
  if (Number.isFinite(leftDate) && Number.isFinite(rightDate)) return leftDate - rightDate;

  const leftText = normalizeFilterText(left);
  const rightText = normalizeFilterText(right);
  return leftText === rightText ? 0 : leftText < rightText ? -1 : 1;
}

function valuesOf(value: unknown): unknown[] {
  return Array.isArray(value) ? value : [value];
}

function matchesRule(value: unknown, rule: FilterRule): boolean {
  const candidates = valuesOf(value);
  const expected = Array.isArray(rule.value) ? rule.value : [rule.value];
  if (!expected.length) return false;
  if (rule.operator === 'contains') {
    return expected.some((item) => matchesSearch(value, item));
  }

  if (rule.operator === 'in') {
    return candidates.some((candidate) =>
      expected.some((item) => compareValues(candidate, item) === 0)
    );
  }

  if (rule.operator === 'eq') {
    return candidates.some((candidate) =>
      expected.some((item) => compareValues(candidate, item) === 0)
    );
  }

  return candidates.some((candidate) =>
    expected.some((item) => {
      const comparison = compareValues(candidate, item);
      return rule.operator === 'gte' ? comparison >= 0 : comparison <= 0;
    })
  );
}

export function createFilterEngine<T>(options: FilterEngineOptions<T> = {}): FilterEngine<T> {
  const getSearchValues = options.getSearchValues ?? ((row: T) => [row]);
  const getField = options.getFieldValue ?? ((row: T, field: string) => getPathValue(row, field));
  const matchesWithTokens = (row: T, tokens: readonly string[], filters: readonly FilterRule[]) =>
    matchesSearchTokens(getSearchValues(row), tokens) &&
    filters.every((filter) => matchesRule(getField(row, filter.field), filter));

  return {
    normalizeSearch: (query) => normalizeFilterText(query),
    matches: (row, search = '', filters = []) =>
      matchesWithTokens(row, tokenizeFilterQuery(search), filters),
    filter: (rows, search = '', filters = []) => {
      const tokens = tokenizeFilterQuery(search);
      return rows.filter((row) => matchesWithTokens(row, tokens, filters));
    }
  };
}
