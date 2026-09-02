import { api } from '$lib/api';
import type { CountMode, ScanEvent } from './types';

const countPath = (id: string) => `/stock-counts/${encodeURIComponent(id)}`;

export function listCounts(query = '') {
  return api<{ items?: Record<string, unknown>[]; next_cursor?: string }>(
    `/stock-counts${query ? `?${query}` : ''}`
  );
}

export function getCount(id: string) {
  return api<Record<string, unknown>>(countPath(id));
}

export function createCount(warehouseID: string, description = '') {
  return api<Record<string, unknown>>('/stock-counts', {
    method: 'POST',
    headers: { 'Idempotency-Key': `stock-count:${crypto.randomUUID()}` },
    body: JSON.stringify({ warehouse_id: warehouseID, description })
  });
}

export function createPass(id: string, mode: CountMode) {
  return api<Record<string, unknown>>(`${countPath(id)}/passes`, {
    method: 'POST',
    body: JSON.stringify({ mode })
  });
}

export function createSession(id: string, passID: string, deviceID: string) {
  return api<Record<string, unknown>>(`${countPath(id)}/sessions`, {
    method: 'POST',
    body: JSON.stringify({ pass_id: passID, device_id: deviceID })
  });
}

export function addCountLine(id: string, productID: string, variantID = '') {
  return api<Record<string, unknown>>(`${countPath(id)}/lines`, {
    method: 'POST',
    body: JSON.stringify({ product_id: productID, variant_id: variantID })
  });
}

export function sendScanEvents(id: string, sessionID: string, events: ScanEvent[]) {
  return api<Record<string, unknown>>(`${countPath(id)}/scan-events/batch`, {
    method: 'POST',
    body: JSON.stringify({ session_id: sessionID, events })
  });
}

export function correctLine(
  id: string,
  lineID: string,
  passID: string,
  eventID: string,
  quantity: string,
  reason: string
) {
  return api<Record<string, unknown>>(
    `${countPath(id)}/lines/${encodeURIComponent(lineID)}/corrections`,
    {
      method: 'POST',
      body: JSON.stringify({ pass_id: passID, event_id: eventID, quantity, reason })
    }
  );
}

export function confirmZero(id: string, lineID: string, passID: string, eventID: string) {
  return api<Record<string, unknown>>(
    `${countPath(id)}/lines/${encodeURIComponent(lineID)}/confirm-zero`,
    {
      method: 'POST',
      body: JSON.stringify({ pass_id: passID, event_id: eventID })
    }
  );
}

export function submitPass(id: string, passID: string) {
  return api<Record<string, unknown>>(
    `${countPath(id)}/passes/${encodeURIComponent(passID)}/submit`,
    {
      method: 'POST',
      body: '{}'
    }
  );
}

export function recountCount(
  id: string,
  version: string | number | undefined,
  idempotencyKey: string
) {
  return api<Record<string, unknown>>(`${countPath(id)}/recount`, {
    method: 'POST',
    headers: {
      ...(version === undefined ? {} : { 'If-Match': `"${String(version)}"` }),
      'Idempotency-Key': idempotencyKey
    },
    body: '{}'
  });
}

export function syncCount(id: string, cursor = '') {
  return api<Record<string, unknown>>(
    `${countPath(id)}/sync${cursor ? `?cursor=${encodeURIComponent(cursor)}` : ''}`
  );
}

export function postCount(
  id: string,
  version: string | number | undefined,
  idempotencyKey: string
) {
  return api<Record<string, unknown>>(`${countPath(id)}/post`, {
    method: 'POST',
    headers: {
      ...(version === undefined ? {} : { 'If-Match': `"${String(version)}"` }),
      'Idempotency-Key': idempotencyKey
    },
    body: '{}'
  });
}

export function cancelCount(id: string, version: string | number | undefined) {
  return api<Record<string, unknown>>(`${countPath(id)}/cancel`, {
    method: 'POST',
    headers: version === undefined ? {} : { 'If-Match': `"${String(version)}"` },
    body: '{}'
  });
}
