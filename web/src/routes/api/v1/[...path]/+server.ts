import { env } from '$env/dynamic/private';
import type { RequestHandler } from './$types';

const forward: RequestHandler = async ({ request, params, fetch, getClientAddress }) => {
  const baseURL = env.VARYAONE_API_INTERNAL_URL || 'http://localhost:8080';
  const headers = new Headers();
  for (const name of [
    'authorization',
    'content-type',
    'cookie',
    'x-csrf-token',
    'if-match',
    'idempotency-key',
    'user-agent',
    'x-request-id'
  ]) {
    const value = request.headers.get(name);
    if (value) headers.set(name, value);
  }
  headers.set('origin', new URL(baseURL).origin);
  // Every browser request reaches the API through this single proxy, so
  // without this the API sees one shared RemoteAddr for all users — login
  // rate limiting (and its IP-scoped bucket) would then lock out everyone
  // whenever any one visitor mistyped a password.
  try {
    headers.set('x-forwarded-for', getClientAddress());
  } catch {
    // Client address unavailable (e.g. during tests) — API falls back to its
    // own RemoteAddr.
  }
  try {
    const upstreamURL = new URL(`/api/v1/${params.path}`, baseURL);
    // Preserve q, cursor, limit and every endpoint-specific query parameter.
    // Dropping this string made cari search look functional in the browser
    // while the API always received an unfiltered list request.
    upstreamURL.search = new URL(request.url).search;
    const hasBody = request.method !== 'GET' && request.method !== 'HEAD';
    const upstream = await fetch(upstreamURL, {
      method: request.method,
      headers,
      signal: request.signal,
      // Stream the request body straight through instead of buffering it. Full
      // `.varya` restore uploads can be hundreds of MB; `arrayBuffer()` would
      // hold the whole archive in the frontend process and trip the adapter's
      // body-size limit.
      body: hasBody ? request.body : undefined,
      ...(hasBody ? { duplex: 'half' } : {}),
      redirect: 'manual'
    } as RequestInit & { duplex?: 'half' });
    const responseHeaders = new Headers({
      'content-type': upstream.headers.get('content-type') || 'application/json'
    });
    for (const name of ['content-disposition', 'x-request-id', 'retry-after']) {
      const value = upstream.headers.get(name);
      if (value) responseHeaders.set(name, value);
    }
    const cookieHeaders =
      (upstream.headers as Headers & { getSetCookie?: () => string[] }).getSetCookie?.() || [];
    if (cookieHeaders.length) {
      for (const cookie of cookieHeaders) responseHeaders.append('set-cookie', cookie);
    } else {
      const cookie = upstream.headers.get('set-cookie');
      if (cookie) responseHeaders.append('set-cookie', cookie);
    }
    return new Response(upstream.status === 204 ? null : upstream.body, {
      status: upstream.status,
      headers: responseHeaders
    });
  } catch {
    return Response.json(
      {
        code: 'API_UNAVAILABLE',
        message: 'Varya One API erişilebilir değil.',
        details: {},
        trace_id: ''
      },
      { status: 503 }
    );
  }
};

export const GET = forward;
export const POST = forward;
export const PUT = forward;
export const PATCH = forward;
export const DELETE = forward;
