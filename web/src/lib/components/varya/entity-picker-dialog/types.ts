export type EntityMeta = string | readonly string[];

/**
 * The deliberately small shape shared by stock, current account, warehouse
 * and other selectable cards.
 */
export type EntityOption = {
  id: string;
  title: string;
  subtitle?: string;
  meta?: EntityMeta;
};

export type EntitySearchPayload<T extends EntityOption> =
  readonly T[] | { items: readonly T[] } | { data: readonly T[] } | { results: readonly T[] };

export type EntitySearchHandler<T extends EntityOption> = (
  query: string,
  signal: AbortSignal
) => EntitySearchPayload<T> | undefined | Promise<EntitySearchPayload<T> | undefined>;

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null;
}

/** Normalizes common API response envelopes without coupling the picker to a domain API. */
export function extractEntityOptions<T extends EntityOption>(payload: unknown): readonly T[] {
  if (Array.isArray(payload)) return payload as T[];
  if (!isRecord(payload)) return [];

  for (const key of ['items', 'data', 'results']) {
    if (key in payload) return extractEntityOptions<T>(payload[key]);
  }

  return [];
}

export function entityMetaText(meta: EntityMeta | undefined): string {
  if (typeof meta !== 'string' && meta) return meta.filter(Boolean).join(' · ');
  return meta ?? '';
}

export function entitySearchText(option: EntityOption): string {
  return [option.id, option.title, option.subtitle, entityMetaText(option.meta)]
    .filter(Boolean)
    .join(' ')
    .toLocaleLowerCase('tr-TR');
}
