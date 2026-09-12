import { beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync } from 'svelte';
import { DRAFT_DEBOUNCE_MS, DraftRecovery } from './draft-recovery.svelte';
import { loadDraft, saveDraft } from './draft-storage';

const scope = { userID: 'u1', companyID: 'c1', formType: 'stock-transfer', recordID: '' };

function makeRecovery(initial: Record<string, string> = {}, version = '') {
  const model = { ...initial };
  let enabled = true;
  const recovery = new DraftRecovery<Record<string, string>>({
    scope: () => scope,
    data: () => ({ ...model }),
    recordVersion: () => version,
    enabled: () => enabled
  });
  return {
    recovery,
    model,
    setEnabled(next: boolean) {
      enabled = next;
    }
  };
}

beforeEach(() => {
  localStorage.clear();
  sessionStorage.clear();
  vi.useFakeTimers();
});

describe('DraftRecovery', () => {
  it('writes only after editing stops', () => {
    const { recovery, model } = makeRecovery();
    recovery.start();
    model.note = 'a';
    recovery.note();
    vi.advanceTimersByTime(DRAFT_DEBOUNCE_MS - 1);
    expect(loadDraft(scope)).toBeNull();
    model.note = 'ab';
    recovery.note();
    vi.advanceTimersByTime(DRAFT_DEBOUNCE_MS);
    expect(loadDraft<Record<string, string>>(scope)?.data.note).toBe('ab');
  });

  it('offers a draft found at startup and restores it once', () => {
    saveDraft(scope, { note: 'kurtarılan' });
    const { recovery } = makeRecovery();
    recovery.start();
    flushSync();
    expect(recovery.found).not.toBeNull();
    expect(recovery.savedAt).toBeInstanceOf(Date);
    expect(recovery.restore()).toEqual({ note: 'kurtarılan' });
    expect(recovery.found).toBeNull();
    expect(recovery.restore()).toBeNull();
  });

  it('does not overwrite a pending draft before the user decides', () => {
    saveDraft(scope, { note: 'kurtarılan' });
    const { recovery, model } = makeRecovery();
    recovery.start();
    model.note = 'yeni yazılan';
    recovery.note();
    vi.advanceTimersByTime(DRAFT_DEBOUNCE_MS * 2);
    expect(loadDraft<Record<string, string>>(scope)?.data.note).toBe('kurtarılan');
  });

  it('flags a draft taken from another version of the record', () => {
    saveDraft({ ...scope, recordID: 'doc-1' }, { note: 'a' }, 'v1');
    const model = { note: 'a' };
    const recovery = new DraftRecovery<typeof model>({
      scope: () => ({ ...scope, recordID: 'doc-1' }),
      data: () => model,
      recordVersion: () => 'v2'
    });
    recovery.start();
    flushSync();
    expect(recovery.staleVersion).toBe(true);
  });

  it('does not flag a draft from the same version', () => {
    saveDraft({ ...scope, recordID: 'doc-1' }, { note: 'a' }, 'v2');
    const recovery = new DraftRecovery<{ note: string }>({
      scope: () => ({ ...scope, recordID: 'doc-1' }),
      data: () => ({ note: 'a' }),
      recordVersion: () => 'v2'
    });
    recovery.start();
    flushSync();
    expect(recovery.staleVersion).toBe(false);
  });

  it('deletes the draft on discard and on clear', () => {
    saveDraft(scope, { note: 'a' });
    const { recovery } = makeRecovery();
    recovery.start();
    recovery.discard();
    expect(loadDraft(scope)).toBeNull();
    expect(recovery.found).toBeNull();

    saveDraft(scope, { note: 'b' });
    recovery.clear();
    expect(loadDraft(scope)).toBeNull();
  });

  it('drops a pending write when the form is cleared', () => {
    const { recovery, model } = makeRecovery();
    recovery.start();
    model.note = 'a';
    recovery.note();
    recovery.clear();
    vi.advanceTimersByTime(DRAFT_DEBOUNCE_MS * 2);
    expect(loadDraft(scope)).toBeNull();
  });

  it('writes nothing while drafting is switched off', () => {
    const { recovery, model, setEnabled } = makeRecovery();
    recovery.start();
    setEnabled(false);
    model.note = 'a';
    recovery.note();
    vi.advanceTimersByTime(DRAFT_DEBOUNCE_MS * 2);
    expect(loadDraft(scope)).toBeNull();
  });

  it('surfaces a storage failure without breaking the form', () => {
    const setItem = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new DOMException('quota', 'QuotaExceededError');
    });
    const { recovery, model } = makeRecovery();
    recovery.start();
    model.note = 'a';
    recovery.note();
    vi.advanceTimersByTime(DRAFT_DEBOUNCE_MS);
    flushSync();
    expect(recovery.error).toBe('Taslak kaydedilemedi.');
    setItem.mockRestore();
  });

  it('stops writing once destroyed', () => {
    const { recovery, model } = makeRecovery();
    recovery.start();
    model.note = 'a';
    recovery.note();
    recovery.destroy();
    vi.advanceTimersByTime(DRAFT_DEBOUNCE_MS * 2);
    expect(loadDraft(scope)).toBeNull();
  });
});

/**
 * Sayfadan ayrılırken "sil ve çık", penceredeki "sil ve çık" ile aynı sonucu
 * vermek zorunda: kullanıcının verdiği karar aynı karardır ve hangi yoldan
 * verildiğine göre farklı davranan bir silme, sildiğini sanan kullanıcıya
 * verisini bir sonraki açılışta geri teklif eder.
 */
describe('sayfadan ayrılırken silme', () => {
  it('taslağı siler ve bekleyen yazmayı iptal eder', () => {
    const { recovery, model } = makeRecovery();
    recovery.start();
    model.note = 'yazıldı';
    recovery.note();
    vi.advanceTimersByTime(DRAFT_DEBOUNCE_MS);
    expect(loadDraft(scope)).not.toBeNull();

    model.note = 'daha fazla';
    recovery.note();
    recovery.discardForNavigation();
    expect(loadDraft(scope)).toBeNull();

    // Sırada duran debounce silinen taslağı geri yazmamalı.
    vi.advanceTimersByTime(DRAFT_DEBOUNCE_MS * 2);
    expect(loadDraft(scope)).toBeNull();
  });

  it('silmeden sonraki düzenleme taslağı yeniden oluşturmuyor', () => {
    const { recovery, model } = makeRecovery();
    recovery.start();
    model.note = 'a';
    recovery.note();
    vi.advanceTimersByTime(DRAFT_DEBOUNCE_MS);
    recovery.discardForNavigation();

    model.note = 'ayrılırken hâlâ dolu olan form modeli';
    recovery.note();
    vi.advanceTimersByTime(DRAFT_DEBOUNCE_MS * 2);
    expect(loadDraft(scope)).toBeNull();
  });

  it('şirket asenkron değişse bile eski şirketin taslağını siler', () => {
    // Şirket değişimi: "sil ve çık" ile gerçek geçiş arasında oturumun
    // şirketi değişir. Silinmesi gereken, silme kararı verildiği andaki
    // şirketin taslağıdır.
    let companyID = 'c1';
    const model: Record<string, string> = {};
    const recovery = new DraftRecovery<Record<string, string>>({
      scope: () => ({ userID: 'u1', companyID, formType: 'stock-transfer', recordID: '' }),
      data: () => ({ ...model })
    });
    recovery.start();
    model.note = 'eski şirkete ait';
    recovery.note();
    vi.advanceTimersByTime(DRAFT_DEBOUNCE_MS);

    const other = { userID: 'u1', companyID: 'c2', formType: 'stock-transfer', recordID: '' };
    saveDraft(other, { note: 'yeni şirkete ait' });

    companyID = 'c2';
    recovery.discardForNavigation();

    expect(
      loadDraft({ userID: 'u1', companyID: 'c1', formType: 'stock-transfer', recordID: '' })
    ).toBeNull();
    expect(loadDraft(other)).not.toBeNull();
  });
});

/**
 * Açılışta bulunan taslak başka bir sekmenin olabilir. "Sil" o taslağı siler —
 * kapsamın tamamını değil, çünkü üçüncü bir sekme aynı anda çalışıyor olabilir.
 */
describe('sekme kapsamı', () => {
  it('yalnız gösterilen taslağı siler', () => {
    const other = { ...scope };
    // Başka bir sekmenin taslağı: aynı kapsam, farklı sekme kimliği.
    sessionStorage.setItem('varyaone:draft-tab', 'sekme-a');
    saveDraft(other, { note: 'a sekmesi' });
    localStorage.setItem(
      'varyaone:draft:1:u1:c1:stock-transfer:new:sekme-b',
      JSON.stringify({
        schema: 1,
        scope: { ...other },
        tabID: 'sekme-b',
        savedAt: new Date(Date.now() + 1000).toISOString(),
        recordVersion: '',
        data: { note: 'b sekmesi' }
      })
    );

    const { recovery } = makeRecovery();
    recovery.start();
    // En yeni olan b sekmesininki bulunur.
    expect(recovery.found?.tabID).toBe('sekme-b');
    recovery.discard();

    expect(localStorage.getItem('varyaone:draft:1:u1:c1:stock-transfer:new:sekme-b')).toBeNull();
    // A sekmesi çalışmaya devam ediyor olabilir; onun taslağı durur.
    expect(loadDraft(other)).not.toBeNull();
  });
});
