export type Company = {
  id: string;
  legal_name: string;
  trade_name: string;
  entity_type: 'LEGAL_ENTITY' | 'SOLE_PROPRIETOR';
  tax_number?: string;
  base_currency: string;
  timezone: string;
  /** Data URI (data:image/...) shown on printed documents. Empty when unset. */
  logo?: string;
  duplicate_party_tax_number_policy?: string;
  party_code_prefix?: string;
  party_code_digits?: number;
  version: number;
};

export type Session = {
  csrf_token: string;
  user: { id: string; email: string; display_name: string; totp_enabled: boolean };
  companies: Company[];
  current_company_id?: string;
  expires_at: string;
  permissions: string[];
  /** Feature modules enabled for the current company. Core areas are never listed. */
  modules: string[];
  /** True for the user who completed the one-time setup; only they may create companies. */
  is_instance_owner?: boolean;
};

export type RecentActivityEntry = {
  kind: 'ledger' | 'stock' | 'document';
  title_code: string;
  label: string;
  party_name?: string;
  amount?: string;
  currency?: string;
  direction?: string;
  occurred_at: string;
  ref_type?: string;
  ref_id?: string;
  entry_id: string;
};

export type DashboardShortcuts = {
  pinned_shortcuts: string[];
  version?: number;
  updated_at?: string;
};

export type APIError = {
  code: string;
  message: string;
  details: Record<string, unknown>;
  trace_id: string;
};

export class APIRequestError extends Error {
  readonly code: string;
  readonly details: Record<string, unknown>;
  readonly traceId: string;
  readonly trace_id: string;
  readonly status: number;

  constructor(error: APIError, status: number) {
    super(error.message || 'İşlem tamamlanamadı.');
    this.name = 'APIRequestError';
    this.code = error.code || 'REQUEST_FAILED';
    this.details = error.details ?? {};
    this.traceId = error.trace_id ?? '';
    this.trace_id = this.traceId;
    this.status = status;
  }
}

const API_REQUEST_TIMEOUT_MS = 15_000;

function readCSRFCookie(): string {
  if (typeof document === 'undefined') return '';
  return (
    document.cookie
      .split('; ')
      .find((item) => item.startsWith('varyaone_csrf='))
      ?.split('=')[1] || ''
  );
}

function writeCSRFCookie(token: string): void {
  if (typeof document === 'undefined') return;
  const secure = globalThis.location?.protocol === 'https:' ? '; Secure' : '';
  document.cookie = `varyaone_csrf=${encodeURIComponent(token)}; Path=/; SameSite=Strict${secure}`;
}

function fallbackError(response: Response): APIError {
  return {
    code: 'REQUEST_FAILED',
    message: 'İşlem tamamlanamadı.',
    details: {},
    trace_id: response.headers.get('x-request-id') || ''
  };
}

function isAPIError(value: unknown): value is APIError {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false;
  const candidate = value as Record<string, unknown>;
  return typeof candidate.code === 'string' && typeof candidate.message === 'string';
}

async function responseError(response: Response): Promise<APIRequestError> {
  const payload = await response.json().catch(() => null);
  const error = isAPIError(payload) ? payload : fallbackError(response);
  return new APIRequestError(error, response.status);
}

async function requestCSRFRefresh(signal?: AbortSignal): Promise<string> {
  const response = await fetch('/api/v1/session/csrf', {
    method: 'GET',
    signal,
    headers: { accept: 'application/json' },
    credentials: 'same-origin',
    cache: 'no-store'
  });
  if (!response.ok) throw await responseError(response);

  const payload = (await response.json().catch(() => null)) as {
    csrf_token?: unknown;
  } | null;
  if (typeof payload?.csrf_token !== 'string' || !payload.csrf_token) {
    throw new APIRequestError(
      {
        code: 'CSRF_REFRESH_FAILED',
        message: 'Güvenlik belirteci yenilenemedi.',
        details: {},
        trace_id: response.headers.get('x-request-id') || ''
      },
      response.status
    );
  }
  writeCSRFCookie(payload.csrf_token);
  return payload.csrf_token;
}

let csrfRefreshPromise: Promise<string> | null = null;

function refreshCSRF(signal?: AbortSignal): Promise<string> {
  if (!csrfRefreshPromise) {
    csrfRefreshPromise = requestCSRFRefresh(signal).finally(() => {
      csrfRefreshPromise = null;
    });
  }
  return csrfRefreshPromise;
}

export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const method = (init.method || 'GET').toUpperCase();
  const timeoutController = new AbortController();
  const abortFromCaller = () => timeoutController.abort(init.signal?.reason);
  if (init.signal?.aborted) abortFromCaller();
  else init.signal?.addEventListener('abort', abortFromCaller, { once: true });
  const timeout = setTimeout(
    () =>
      timeoutController.abort(
        new DOMException('İstek zaman aşımına uğradı. Lütfen tekrar deneyin.', 'TimeoutError')
      ),
    API_REQUEST_TIMEOUT_MS
  );
  const headers = new Headers(init.headers);
  if (method !== 'GET' && method !== 'HEAD' && !headers.has('Idempotency-Key')) {
    headers.set(
      'Idempotency-Key',
      globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random().toString(36).slice(2)}`
    );
  }

  const send = (csrfToken?: string) => {
    const requestHeaders = new Headers(headers);
    if (!(init.body instanceof FormData) && !requestHeaders.has('content-type')) {
      requestHeaders.set('content-type', 'application/json');
    }
    if (method !== 'GET' && method !== 'HEAD') {
      requestHeaders.set('x-csrf-token', csrfToken ?? decodeURIComponent(readCSRFCookie()));
    }
    return fetch(`/api/v1${path}`, {
      ...init,
      signal: timeoutController.signal,
      headers: requestHeaders,
      credentials: init.credentials ?? 'same-origin'
    });
  };

  let response: Response;
  try {
    response = await send();
    if (!response.ok && method !== 'GET' && method !== 'HEAD') {
      const error = await responseError(response);
      if (error.code === 'CSRF_REJECTED') {
        try {
          const csrfToken = await refreshCSRF(init.signal ?? undefined);
          response = await send(csrfToken);
        } catch {
          throw error;
        }
      } else {
        throw error;
      }
    }
  } finally {
    clearTimeout(timeout);
    init.signal?.removeEventListener('abort', abortFromCaller);
  }
  if (!response.ok) {
    const error = await responseError(response);
    // The public demo rebuilds itself on a timer; while that runs every API
    // call is refused. One hook here lets the demo banner take over the screen
    // instead of each page inventing its own error state.
    if (error.code === 'DEMO_RESETTING') notifyDemoResetting();
    throw error;
  }
  return (response.status === 204 ? undefined : await response.json()) as T;
}

let demoResettingHandler: (() => void) | null = null;

/** Registers the handler called when the API reports the demo is rebuilding. */
export function onDemoResetting(handler: (() => void) | null) {
  demoResettingHandler = handler;
}

function notifyDemoResetting() {
  demoResettingHandler?.();
}
