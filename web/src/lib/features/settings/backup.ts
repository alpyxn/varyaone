import { APIRequestError, type APIError } from '$lib/api';

export type RestoreResult = {
  restored_from: string;
  migration_version: number;
  objects: number;
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
 * Upload a `.varya` archive and restore the whole system from it. This is a
 * destructive operation; the caller must confirm with the user first. The
 * request has no client timeout because a full restore can take minutes.
 */
export async function restoreBackup(file: File, force = false): Promise<RestoreResult> {
  const form = new FormData();
  form.append('file', file, file.name);
  if (force) form.append('force', 'true');

  const response = await fetch('/api/v1/system/backup/restore', {
    method: 'POST',
    body: form,
    credentials: 'same-origin',
    headers: { 'x-csrf-token': csrfToken() }
  });

  if (!response.ok) {
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
    throw new APIRequestError(payload, response.status);
  }
  return (await response.json()) as RestoreResult;
}
