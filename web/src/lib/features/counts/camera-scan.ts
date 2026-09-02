/**
 * Values emitted by a barcode detector are intentionally represented without
 * importing any browser API. This keeps the scan decision deterministic and
 * easy to use with camera frames, keyboard wedges, and test doubles alike.
 */
export type DetectedBarcodeValue = string | { rawValue?: unknown } | null | undefined;

export type CameraScanState = {
  lastAcceptedKey: string;
  lastAcceptedAt: number;
};

export type CameraScanReason = 'accepted' | 'empty' | 'ambiguous' | 'cooldown';

export type CameraScanResult = {
  accepted: boolean;
  barcode?: string;
  reason: CameraScanReason;
  state: CameraScanState;
};

const DEFAULT_COOLDOWN_MS = 1500;

/** Returns a display-safe barcode value, or an empty string for invalid input. */
export function normalizeDetectedValue(value: DetectedBarcodeValue): string {
  const rawValue = typeof value === 'string' ? value : value?.rawValue;
  if (typeof rawValue !== 'string') return '';

  return rawValue.normalize('NFKC').trim();
}

function comparisonKey(value: string) {
  return value.toLocaleLowerCase('tr-TR');
}

/** Creates state for a camera session with no previously accepted frame. */
export function createCameraScanState(): CameraScanState {
  return { lastAcceptedKey: '', lastAcceptedAt: Number.NEGATIVE_INFINITY };
}

/**
 * Resolves one camera frame into an acceptance decision.
 *
 * A frame is accepted only when it contains exactly one unique, non-empty
 * barcode. Repeated detections of that barcode are ignored until cooldownMs
 * has elapsed. Rejected frames do not change the state, so an ambiguous or
 * empty frame cannot accidentally suppress the next valid detection.
 */
export function acceptCameraFrame(
  values: readonly DetectedBarcodeValue[],
  state: CameraScanState = createCameraScanState(),
  now = Date.now(),
  cooldownMs = DEFAULT_COOLDOWN_MS
): CameraScanResult {
  const normalized = values.map(normalizeDetectedValue).filter(Boolean);
  const unique = new Map<string, string>();

  for (const value of normalized) {
    const key = comparisonKey(value);
    if (!unique.has(key)) unique.set(key, value);
  }

  if (unique.size === 0) {
    return { accepted: false, reason: 'empty', state };
  }

  if (unique.size !== 1) {
    return { accepted: false, reason: 'ambiguous', state };
  }

  const [entry] = unique.entries();
  const [key, barcode] = entry;
  const safeNow = Number.isFinite(now) ? now : Date.now();
  const safeCooldown = Number.isFinite(cooldownMs) ? Math.max(0, cooldownMs) : DEFAULT_COOLDOWN_MS;
  const elapsed = safeNow - state.lastAcceptedAt;

  if (key === state.lastAcceptedKey && elapsed < safeCooldown) {
    return { accepted: false, barcode, reason: 'cooldown', state };
  }

  const nextState = { lastAcceptedKey: key, lastAcceptedAt: safeNow };
  return { accepted: true, barcode, reason: 'accepted', state: nextState };
}
