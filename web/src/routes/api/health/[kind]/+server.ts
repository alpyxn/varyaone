import { env } from '$env/dynamic/private';
import { error } from '@sveltejs/kit';
import type { RequestHandler } from './$types';

export const GET: RequestHandler = async ({ fetch, params }) => {
  if (params.kind !== 'live' && params.kind !== 'ready') {
    error(404, 'Bilinmeyen sağlık kontrolü');
  }
  const baseURL = env.VARYAONE_API_INTERNAL_URL || 'http://localhost:8080';
  try {
    const upstream = await fetch(`${baseURL}/health/${params.kind}`);
    const headers = new Headers({
      'content-type': upstream.headers.get('content-type') || 'application/json'
    });
    const requestID = upstream.headers.get('x-request-id');
    if (requestID) headers.set('x-request-id', requestID);
    return new Response(await upstream.text(), { status: upstream.status, headers });
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
