import { APIRequestError, type APIError } from '$lib/api';

export type RestoreResult = {
  operation_id: string;
  restored_from: string;
  migration_version: number;
  objects: number;
  restart_required: boolean;
};

export type OperationRecord = {
  id: string;
  kind: string;
  phase: string;
  started_at: string;
  updated_at: string;
  actor?: string;
  error?: string;
  resolved_by?: string;
};

export type OperationStatus = {
  serviceable: boolean;
  running: boolean;
  reason: string;
  operation: OperationRecord | null;
};

/** Trigger a browser download of the full-system `.varya` archive. */
export function downloadBackup(): void {
  window.location.assign('/api/v1/system/backup');
}

function csrfToken(): string {
  const raw = document.cookie
    .split('; ')
    .find((item) => item.startsWith('varyaone_csrf='))
    ?.slice('varyaone_csrf='.length);
  return raw ? decodeURIComponent(raw) : '';
}

/**
 * A key that identifies one restore attempt.
 *
 * A restore can take longer than any connection is guaranteed to live. When the
 * response is lost the user sees a spinner that never resolves and clicks
 * again — and without a key that second click is a second restore, applied on
 * top of the first one's result. The key makes the server answer "that is
 * already running, here is its id" instead.
 */
export function newOperationKey(): string {
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) return crypto.randomUUID();
  return `restore-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

async function readError(response: Response): Promise<APIRequestError> {
  let payload: APIError = {
    code: 'RESTORE_FAILED',
    message: 'Geri yükleme başarısız oldu.',
    details: {},
    trace_id: response.headers.get('x-request-id') ?? ''
  };
  try {
    payload = { ...payload, ...(await response.json()) };
  } catch {
    /* keep fallback */
  }
  return new APIRequestError(payload, response.status);
}

export type RestoreOutcome =
  { kind: 'done'; result: RestoreResult } | { kind: 'running'; operationId: string };

/**
 * Upload a `.varya` archive and restore the whole system from it.
 *
 * The request has no client timeout: a full restore can take hours, and
 * abandoning one half-way would be far worse than waiting. The operation id
 * comes back in a header before the body, so a caller that loses the connection
 * afterwards can still find out what happened.
 */
export async function restoreBackup(
  file: File,
  options: { force?: boolean; operationKey: string }
): Promise<RestoreOutcome> {
  const form = new FormData();
  form.append('file', file, file.name);
  if (options.force) form.append('force', 'true');

  const response = await fetch('/api/v1/system/backup/restore', {
    method: 'POST',
    body: form,
    credentials: 'same-origin',
    headers: { 'x-csrf-token': csrfToken(), 'Idempotency-Key': options.operationKey }
  });

  if (response.status === 202) {
    const payload = (await response.json()) as { operation: OperationRecord };
    return { kind: 'running', operationId: payload.operation.id };
  }
  if (!response.ok) throw await readError(response);
  return { kind: 'done', result: (await response.json()) as RestoreResult };
}

/** The installation's current operation state. */
export async function operationStatus(): Promise<OperationStatus> {
  const response = await fetch('/api/v1/system/operations/current', { credentials: 'same-origin' });
  if (!response.ok) throw await readError(response);
  return (await response.json()) as OperationStatus;
}

/** One operation's recorded state. */
export async function operation(id: string): Promise<OperationRecord> {
  const response = await fetch(`/api/v1/system/operations/${encodeURIComponent(id)}`, {
    credentials: 'same-origin'
  });
  if (!response.ok) throw await readError(response);
  return ((await response.json()) as { operation: OperationRecord }).operation;
}

/**
 * Resolve once the server answers again.
 *
 * After a restore the database underneath the server has been replaced, and on
 * some installations the service restarts too. `/setup` is public and reads the
 * database, so a successful answer means the restored system is serving.
 */
export async function waitForServer(signal?: AbortSignal): Promise<void> {
  for (;;) {
    if (signal?.aborted) throw new DOMException('aborted', 'AbortError');
    try {
      const response = await fetch('/api/v1/setup', {
        credentials: 'same-origin',
        cache: 'no-store'
      });
      if (response.ok) return;
    } catch {
      /* server still away */
    }
    await new Promise((resolve) => setTimeout(resolve, 2000));
  }
}

/** Phases from which an installation may serve traffic again. */
const RESOLVED_PHASES = ['COMMITTED', 'FAILED_UNCHANGED', 'ROLLED_BACK'];

export function isFinished(record: OperationRecord): boolean {
  return RESOLVED_PHASES.includes(record.phase);
}

export function needsRecovery(record: OperationRecord): boolean {
  return record.phase === 'RECOVERY_REQUIRED';
}

/**
 * Follow an operation to its end.
 *
 * Polling rather than holding a connection is the point: the answer lives in
 * the server's journal, so it survives the page being closed, the session
 * expiring and the user signing in again.
 */
export async function followOperation(
  id: string,
  onUpdate: (record: OperationRecord) => void,
  signal?: AbortSignal
): Promise<OperationRecord> {
  for (;;) {
    if (signal?.aborted) throw new DOMException('aborted', 'AbortError');
    const record = await operation(id);
    onUpdate(record);
    if (isFinished(record) || needsRecovery(record)) return record;
    await new Promise((resolve) => setTimeout(resolve, 3000));
  }
}
