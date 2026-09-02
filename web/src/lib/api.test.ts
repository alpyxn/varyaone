import { describe, expect, it, vi } from 'vitest';
import { api, APIRequestError } from './api';

describe('APIRequestError', () => {
  it('keeps the structured code and server message', () => {
    const error = new APIRequestError(
      {
        code: 'CSRF_REJECTED',
        message: 'Güvenlik doğrulaması başarısız.',
        details: { reason: 'token' },
        trace_id: 'trace-1'
      },
      403
    );

    expect(error).toBeInstanceOf(Error);
    expect(error.code).toBe('CSRF_REJECTED');
    expect(error.message).toBe('Güvenlik doğrulaması başarısız.');
    expect(error.status).toBe(403);
    expect(error.trace_id).toBe('trace-1');
  });
});

describe('api response errors', () => {
  it.each([
    { label: 'null JSON', body: 'null', contentType: 'application/json' },
    { label: 'array JSON', body: '[]', contentType: 'application/json' },
    { label: 'HTML', body: '<html>hata</html>', contentType: 'text/html' }
  ])('uses a safe fallback for $label', async ({ body, contentType }) => {
    const fetchMock = vi.fn(
      async () => new Response(body, { status: 422, headers: { 'content-type': contentType } })
    );
    vi.stubGlobal('fetch', fetchMock);

    await expect(
      api('/imports/import-1/commit', { method: 'POST', body: '{}' })
    ).rejects.toMatchObject({
      name: 'APIRequestError',
      code: 'REQUEST_FAILED',
      status: 422,
      message: 'İşlem tamamlanamadı.'
    });

    vi.unstubAllGlobals();
  });
});

describe('api CSRF recovery', () => {
  it('refreshes CSRF once and retries the original multipart request with the same idempotency key', async () => {
    document.cookie = 'varyaone_csrf=stale; path=/';
    const calls: Request[] = [];
    const rawHeaders: Headers[] = [];
    const bodyTypes: unknown[] = [];
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const { signal: _signal, ...requestInit } = init ?? {};
      const request = new Request(new URL(String(input), 'http://localhost'), requestInit);
      calls.push(request);
      rawHeaders.push(new Headers(init?.headers));
      bodyTypes.push(init?.body);
      if (calls.length === 1) {
        return new Response(
          JSON.stringify({
            code: 'CSRF_REJECTED',
            message: 'Güvenlik doğrulaması başarısız.',
            details: {},
            trace_id: 'trace-1'
          }),
          { status: 403, headers: { 'content-type': 'application/json' } }
        );
      }
      if (calls.length === 2) {
        return new Response(JSON.stringify({ csrf_token: 'fresh' }), {
          status: 200,
          headers: { 'content-type': 'application/json' }
        });
      }
      return new Response(JSON.stringify({ id: 'import-1' }), {
        status: 201,
        headers: { 'content-type': 'application/json' }
      });
    });
    vi.stubGlobal('fetch', fetchMock);

    const form = new FormData();
    form.append('file', new File(['Ürün Kodu\n'], 'urunler.csv', { type: 'text/csv' }));
    await api<{ id: string }>('/imports', { method: 'POST', body: form });

    expect(fetchMock).toHaveBeenCalledTimes(3);
    expect(calls[0].headers.get('x-csrf-token')).toBe('stale');
    expect(calls[2].headers.get('x-csrf-token')).toBe('fresh');
    expect(calls[0].headers.get('idempotency-key')).toBe(calls[2].headers.get('idempotency-key'));
    expect(rawHeaders[0].get('content-type')).toBeNull();
    expect(rawHeaders[2].get('content-type')).toBeNull();
    expect(bodyTypes[0]).toBeInstanceOf(FormData);
    expect(bodyTypes[2]).toBeInstanceOf(FormData);
    vi.unstubAllGlobals();
  });

  it('does not refresh twice after a second CSRF rejection', async () => {
    document.cookie = 'varyaone_csrf=stale-again; path=/';
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith('/session/csrf')) {
        document.cookie = 'varyaone_csrf=fresh-again; path=/';
        return new Response(JSON.stringify({ csrf_token: 'fresh-again' }), { status: 200 });
      }
      return new Response(
        JSON.stringify({ code: 'CSRF_REJECTED', message: 'csrf', details: {}, trace_id: '' }),
        { status: 403, headers: { 'content-type': 'application/json' } }
      );
    });
    vi.stubGlobal('fetch', fetchMock);

    await expect(api('/imports', { method: 'POST', body: '{}' })).rejects.toMatchObject({
      code: 'CSRF_REJECTED'
    });

    expect(fetchMock).toHaveBeenCalledTimes(3);
    expect(
      fetchMock.mock.calls.filter(([input]) => String(input).endsWith('/session/csrf'))
    ).toHaveLength(1);
    vi.unstubAllGlobals();
  });

  it('does not refresh or retry a forbidden response', async () => {
    const fetchMock = vi.fn(
      async () =>
        new Response(
          JSON.stringify({ code: 'FORBIDDEN', message: 'Yetki yok', details: {}, trace_id: '' }),
          { status: 403, headers: { 'content-type': 'application/json' } }
        )
    );
    vi.stubGlobal('fetch', fetchMock);

    await expect(api('/exports', { method: 'POST', body: '{}' })).rejects.toMatchObject({
      code: 'FORBIDDEN'
    });

    expect(fetchMock).toHaveBeenCalledTimes(1);
    vi.unstubAllGlobals();
  });
});
