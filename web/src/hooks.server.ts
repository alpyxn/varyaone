import type { Handle } from '@sveltejs/kit';

/**
 * Baseline response hardening for every browser-facing response, including the
 * `/api/v1/*` proxy. Deliberately conservative: no Content-Security-Policy here
 * (that needs its own tested rollout), only headers that cannot change how a
 * correctly-typed response renders.
 *
 * - `X-Content-Type-Options: nosniff` — stop the browser from re-typing an
 *   attachment download (served with its stored Content-Type) as HTML/JS.
 * - `X-Frame-Options: SAMEORIGIN` — the app is never framed; block clickjacking.
 * - `Referrer-Policy` — do not leak full URLs to third parties.
 */
const SECURITY_HEADERS: Record<string, string> = {
  'X-Content-Type-Options': 'nosniff',
  'X-Frame-Options': 'SAMEORIGIN',
  'Referrer-Policy': 'strict-origin-when-cross-origin'
};

export const handle: Handle = async ({ event, resolve }) => {
  const response = await resolve(event);
  for (const [name, value] of Object.entries(SECURITY_HEADERS)) {
    if (!response.headers.has(name)) response.headers.set(name, value);
  }
  return response;
};
