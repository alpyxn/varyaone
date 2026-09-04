import { api } from '$lib/api';

export type UpdateState = 'idle' | 'apply_requested' | 'in_progress' | 'done' | 'failed';

export type UpdateLatest = {
  version: string;
  notes_md?: string;
  mandatory: boolean;
  min_version?: string;
  published_at?: string;
};

export type UpdateProgress = {
  phase: string;
  message?: string;
  updated_at: string;
};

export type UpdateResult = {
  ok: boolean;
  error?: string;
  rolled_back: boolean;
  from_version?: string;
  to_version?: string;
  log_tail?: string;
  finished_at: string;
};

export type UpdateApplied = {
  version: string;
  notes_md?: string;
  at: string;
};

export type UpdateStatus = {
  current_version: string;
  channel: string;
  state: UpdateState;
  checked_at?: string;
  latest?: UpdateLatest;
  update_available: boolean;
  mandatory: boolean;
  snoozed: boolean;
  /** The operator turned update checking off on this screen. */
  checks_disabled: boolean;
  snooze_until?: string;
  progress?: UpdateProgress;
  result?: UpdateResult;
  applied?: UpdateApplied;
};

/** Human labels for the host agent's apply phases. */
export const PHASE_LABELS: Record<string, string> = {
  queued: 'Sıraya alındı',
  preflight: 'Ön kontroller (disk, ağ, sağlık)',
  snapshot: 'Geri dönüş noktası kaydediliyor',
  backup: 'Veritabanı yedeği alınıyor',
  fetch: 'Kaynak kod çekiliyor',
  build: 'Docker görüntüleri derleniyor',
  migrate: 'Veritabanı taşınıyor',
  restart: 'Servisler yeniden başlatılıyor',
  healthcheck: 'Sağlık kontrolü',
  verify: 'Doğrulama',
  rollback: 'Geri alınıyor — önceki sürüme dönülüyor',
  done: 'Tamamlandı'
};

export function phaseLabel(phase: string): string {
  return PHASE_LABELS[phase] ?? phase;
}

export function getUpdateStatus(): Promise<UpdateStatus> {
  return api<UpdateStatus>('/system/update');
}

/**
 * Contacts the release catalog now and returns the refreshed status. Reading
 * the status alone would only redraw what the worker last stored, which can be
 * hours old - and reads to the operator as "there is no update".
 */
export function checkForUpdates(): Promise<UpdateStatus> {
  return api<UpdateStatus>('/system/update/check', { method: 'POST', body: '{}' });
}

/**
 * Turns update checking on or off for this installation. While it is off
 * nothing contacts the catalog and no release is offered, so a deployment that
 * must not move stays where it is.
 */
export function setUpdateChecks(enabled: boolean): Promise<UpdateStatus> {
  return api<UpdateStatus>('/system/update/checks', {
    method: 'POST',
    body: JSON.stringify({ enabled })
  });
}

export function applyUpdate(): Promise<{ ok: boolean }> {
  return api('/system/update/apply', { method: 'POST', body: '{}' });
}

export function snoozeUpdate(): Promise<{ ok: boolean }> {
  return api('/system/update/snooze', { method: 'POST', body: '{}' });
}

export function ackUpdate(): Promise<{ ok: boolean }> {
  return api('/system/update/ack', { method: 'POST', body: '{}' });
}

/**
 * Minimal, safe Markdown -> HTML for release notes. Handles headings, bullet
 * and numbered lists, bold/italic/inline-code and links. Everything is escaped
 * first, so the output is safe to inject. This is deliberately tiny — release
 * notes are short and authored by us.
 */
export function renderNotes(md: string): string {
  const esc = (s: string) =>
    s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');

  const inline = (s: string) =>
    esc(s)
      .replace(/`([^`]+)`/g, '<code>$1</code>')
      .replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>')
      .replace(/(^|[^*])\*([^*\n]+)\*/g, '$1<em>$2</em>')
      .replace(
        /\[([^\]]+)\]\((https?:\/\/[^\s)]+)\)/g,
        '<a href="$2" target="_blank" rel="noopener noreferrer">$1</a>'
      );

  const lines = md.replace(/\r\n/g, '\n').split('\n');
  const out: string[] = [];
  let listType: 'ul' | 'ol' | null = null;

  const closeList = () => {
    if (listType) {
      out.push(`</${listType}>`);
      listType = null;
    }
  };

  for (const raw of lines) {
    const line = raw.trimEnd();
    const heading = /^(#{1,4})\s+(.*)$/.exec(line);
    const bullet = /^[-*]\s+(.*)$/.exec(line);
    const numbered = /^\d+\.\s+(.*)$/.exec(line);

    if (heading) {
      closeList();
      const level = Math.min(heading[1].length + 1, 5);
      out.push(`<h${level}>${inline(heading[2])}</h${level}>`);
    } else if (bullet) {
      if (listType !== 'ul') {
        closeList();
        out.push('<ul>');
        listType = 'ul';
      }
      out.push(`<li>${inline(bullet[1])}</li>`);
    } else if (numbered) {
      if (listType !== 'ol') {
        closeList();
        out.push('<ol>');
        listType = 'ol';
      }
      out.push(`<li>${inline(numbered[1])}</li>`);
    } else if (line === '') {
      closeList();
    } else {
      closeList();
      out.push(`<p>${inline(line)}</p>`);
    }
  }
  closeList();
  return out.join('\n');
}
