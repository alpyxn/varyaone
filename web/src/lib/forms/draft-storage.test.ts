import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  DRAFT_MAX_AGE_MS,
  DRAFT_SCHEMA_VERSION,
  clearDraft,
  clearDraftScope,
  loadDraft,
  saveDraft,
  scrubSensitive,
  sweepDrafts,
  tabID
} from './draft-storage';

const scope = { userID: 'u1', companyID: 'c1', formType: 'sales-invoices', recordID: 'doc-1' };

beforeEach(() => {
  localStorage.clear();
  sessionStorage.clear();
});

describe('draft storage', () => {
  it('round-trips a draft for the same user, company and record', () => {
    expect(saveDraft(scope, { notes: 'yarım kalan' }, 'v3')).toEqual({ ok: true });
    const found = loadDraft<{ notes: string }>(scope);
    expect(found?.data.notes).toBe('yarım kalan');
    expect(found?.recordVersion).toBe('v3');
    expect(found?.schema).toBe(DRAFT_SCHEMA_VERSION);
  });

  it('keeps a different user, company, form or record apart', () => {
    saveDraft(scope, { notes: 'a' });
    expect(loadDraft({ ...scope, userID: 'u2' })).toBeNull();
    expect(loadDraft({ ...scope, companyID: 'c2' })).toBeNull();
    expect(loadDraft({ ...scope, formType: 'purchase-invoices' })).toBeNull();
    expect(loadDraft({ ...scope, recordID: 'doc-2' })).toBeNull();
  });

  it('treats a new record as its own scope', () => {
    saveDraft({ ...scope, recordID: '' }, { notes: 'yeni' });
    expect(loadDraft<{ notes: string }>({ ...scope, recordID: '' })?.data.notes).toBe('yeni');
    expect(loadDraft(scope)).toBeNull();
  });

  it('does not let one tab overwrite another tab draft', () => {
    saveDraft(scope, { notes: 'birinci sekme' });
    const firstKey = Object.keys(localStorage).find((key) => key.includes(tabID()))!;
    // İkinci sekme: kendi sekme kimliğiyle yazar.
    sessionStorage.clear();
    localStorage.setItem(
      firstKey.replace(tabID(), 'other-tab'),
      JSON.stringify({
        schema: DRAFT_SCHEMA_VERSION,
        scope: { ...scope },
        tabID: 'other-tab',
        savedAt: new Date(Date.now() - 1000).toISOString(),
        recordVersion: '',
        data: { notes: 'ikinci sekme' }
      })
    );
    saveDraft(scope, { notes: 'birinci sekme, güncel' });
    const raw = localStorage.getItem(firstKey.replace(tabID(), 'other-tab'))!;
    expect(JSON.parse(raw).data.notes).toBe('ikinci sekme');
    // Açılışta en yeni taslak sunulur.
    expect(loadDraft<{ notes: string }>(scope)?.data.notes).toBe('birinci sekme, güncel');
  });

  it('clears only this tab draft but the whole scope on demand', () => {
    saveDraft(scope, { notes: 'a' });
    const key = Object.keys(localStorage)[0];
    localStorage.setItem(key.replace(tabID(), 'other-tab'), localStorage.getItem(key)!);
    clearDraft(scope);
    expect(loadDraft(scope)).not.toBeNull();
    clearDraftScope(scope);
    expect(loadDraft(scope)).toBeNull();
  });

  it('drops a draft past the seven day retention', () => {
    saveDraft(scope, { notes: 'eski' });
    const later = Date.now() + DRAFT_MAX_AGE_MS + 1000;
    expect(loadDraft(scope, later)).toBeNull();
    expect(localStorage.length).toBe(0);
  });

  it('sweeps expired and schema-incompatible drafts', () => {
    saveDraft(scope, { notes: 'güncel' });
    localStorage.setItem('varyaone:draft:0:u1:c1:old:doc-1:tab', '{"schema":0}');
    localStorage.setItem('unrelated-key', 'bırak');
    expect(sweepDrafts()).toBe(1);
    expect(localStorage.getItem('unrelated-key')).toBe('bırak');
    expect(loadDraft(scope)).not.toBeNull();
  });

  it('reports a storage failure instead of throwing', () => {
    const setItem = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new DOMException('quota', 'QuotaExceededError');
    });
    expect(saveDraft(scope, { notes: 'a' })).toEqual({ ok: false, reason: 'storage' });
    setItem.mockRestore();
  });

  it('refuses a draft that is too large to store', () => {
    expect(saveDraft(scope, { notes: 'x'.repeat(600_000) })).toEqual({
      ok: false,
      reason: 'too-large'
    });
  });

  it('keeps a corrupt entry from breaking the lookup', () => {
    saveDraft(scope, { notes: 'a' });
    const key = Object.keys(localStorage)[0];
    localStorage.setItem(key, '{ bozuk');
    expect(loadDraft(scope)).toBeNull();
    expect(localStorage.getItem(key)).toBeNull();
  });
});

describe('scrubSensitive', () => {
  it('drops passwords, one-time codes and identity numbers', () => {
    const clean = scrubSensitive({
      amount: '10',
      parola: 'gizli',
      password: 'gizli',
      otp_code: '123456',
      api_token: 'abc',
      tckn: '11111111111'
    }) as Record<string, unknown>;
    expect(clean).toEqual({ amount: '10' });
  });

  it('drops embedded file contents', () => {
    const clean = scrubSensitive({ attachment: 'data:application/pdf;base64,AAAA' }) as Record<
      string,
      unknown
    >;
    expect(clean.attachment).toBe('');
  });

  it('leaves ordinary nested form data alone', () => {
    const value = { lines: [{ productID: 'p1', quantity: '2' }], notes: 'not' };
    expect(scrubSensitive(value)).toEqual(value);
  });
});
