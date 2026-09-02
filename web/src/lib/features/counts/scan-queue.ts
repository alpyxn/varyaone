import type { ScanEvent } from './types';

export type QueueStatus = 'pending' | 'failed';
export type QueueEntry = {
  count_id: string;
  session_id: string;
  event: ScanEvent;
  status: QueueStatus;
  last_error?: string;
};
export type QueueSender = (sessionID: string, events: ScanEvent[]) => Promise<void>;

const DB_NAME = 'varyaone-stock-counts';
const STORE_NAME = 'pending-scan-events';
const memoryQueue = new Map<string, QueueEntry>();

function indexedDBAvailable() {
  return typeof indexedDB !== 'undefined';
}

function openDB(): Promise<IDBDatabase | undefined> {
  if (!indexedDBAvailable()) return Promise.resolve(undefined);
  return new Promise<IDBDatabase | undefined>((resolve, reject) => {
    const request = indexedDB.open(DB_NAME, 1);
    request.onupgradeneeded = () =>
      request.result.createObjectStore(STORE_NAME, { keyPath: 'event.event_id' });
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error);
  }).catch(() => undefined);
}

function memoryKey(entry: QueueEntry) {
  return entry.event.event_id;
}

export async function enqueueScanEvent(entry: QueueEntry) {
  const db = await openDB();
  if (!db) {
    memoryQueue.set(memoryKey(entry), entry);
    return entry;
  }
  await new Promise<void>((resolve, reject) => {
    const request = db.transaction(STORE_NAME, 'readwrite').objectStore(STORE_NAME).put(entry);
    request.onsuccess = () => resolve();
    request.onerror = () => reject(request.error);
  });
  db.close();
  return entry;
}

async function allEntries() {
  const db = await openDB();
  if (!db) return [...memoryQueue.values()];
  const rows = await new Promise<QueueEntry[]>((resolve, reject) => {
    const request = db.transaction(STORE_NAME, 'readonly').objectStore(STORE_NAME).getAll();
    request.onsuccess = () => resolve((request.result ?? []) as QueueEntry[]);
    request.onerror = () => reject(request.error);
  });
  db.close();
  return rows;
}

export async function readScanQueue(countID: string) {
  return (await allEntries()).filter((entry) => entry.count_id === countID);
}

async function remove(eventID: string) {
  const db = await openDB();
  if (!db) {
    memoryQueue.delete(eventID);
    return;
  }
  await new Promise<void>((resolve, reject) => {
    const request = db.transaction(STORE_NAME, 'readwrite').objectStore(STORE_NAME).delete(eventID);
    request.onsuccess = () => resolve();
    request.onerror = () => reject(request.error);
  });
  db.close();
}

export async function flushScanQueue(countID: string, send: QueueSender) {
  const entries = await readScanQueue(countID);
  const result = { sent: 0, failed: 0 };
  for (const entry of entries) {
    try {
      // One event per API batch keeps a partially accepted batch retry-safe. The same
      // event_id is retained if the request fails, so the server can deduplicate it.
      await send(entry.session_id, [entry.event]);
      await remove(entry.event.event_id);
      result.sent += 1;
    } catch (error) {
      result.failed += 1;
      await enqueueScanEvent({
        ...entry,
        status: 'failed',
        last_error: error instanceof Error ? error.message : 'Senkronizasyon başarısız.'
      });
    }
  }
  return result;
}

export async function clearScanQueueForTests() {
  memoryQueue.clear();
  const db = await openDB();
  if (!db) return;
  await new Promise<void>((resolve) => {
    const request = db.transaction(STORE_NAME, 'readwrite').objectStore(STORE_NAME).clear();
    request.onsuccess = () => resolve();
    request.onerror = () => resolve();
  });
  db.close();
}
