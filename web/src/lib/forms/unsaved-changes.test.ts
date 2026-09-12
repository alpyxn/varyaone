import { describe, expect, it } from 'vitest';
import {
  UnsavedChangesGuard,
  busyGuardLabel,
  discardUnsavedChanges,
  formSignature,
  hasUnsavedChanges,
  registerUnsavedGuard
} from './unsaved-changes.svelte';

describe('formSignature', () => {
  it('treats empty, missing and whitespace-only fields as the same', () => {
    expect(formSignature({ a: '', b: '   ' })).toBe(formSignature({}));
    expect(formSignature({ a: null, b: undefined })).toBe(formSignature({}));
  });

  it('ignores key order', () => {
    expect(formSignature({ a: '1', b: '2' })).toBe(formSignature({ b: '2', a: '1' }));
  });

  it('keeps line order significant', () => {
    expect(formSignature({ lines: ['a', 'b'] })).not.toBe(formSignature({ lines: ['b', 'a'] }));
  });

  it('separates a number from the same-looking string', () => {
    expect(formSignature({ amount: 5 })).not.toBe(formSignature({ amount: '5' }));
  });

  it('compares dates by instant', () => {
    expect(formSignature({ at: new Date('2026-09-12T00:00:00Z') })).toBe(
      formSignature({ at: new Date('2026-09-12T00:00:00.000Z') })
    );
  });
});

function makeGuard(initial: Record<string, string>) {
  const model = { ...initial };
  let busy = false;
  const closed: string[] = [];
  const guard = new UnsavedChangesGuard({
    snapshot: () => ({ ...model }),
    isBusy: () => busy,
    onClose: (reason) => closed.push(reason)
  });
  return {
    guard,
    model,
    closed,
    setBusy(next: boolean) {
      busy = next;
    }
  };
}

describe('UnsavedChangesGuard', () => {
  it('stays clean before a baseline is taken', () => {
    const { guard, model } = makeGuard({ amount: '' });
    model.amount = '100';
    expect(guard.armed).toBe(false);
    expect(guard.isDirty).toBe(false);
  });

  it('closes an unchanged form without asking', () => {
    const { guard, closed } = makeGuard({ amount: '' });
    guard.reset();
    guard.requestClose();
    expect(guard.confirmOpen).toBe(false);
    expect(closed).toEqual(['clean']);
  });

  it('asks before closing a changed form', () => {
    const { guard, model, closed } = makeGuard({ amount: '' });
    guard.reset();
    model.amount = '100';
    expect(guard.isDirty).toBe(true);
    guard.requestClose();
    expect(guard.confirmOpen).toBe(true);
    expect(closed).toEqual([]);
  });

  it('keeps the data when the user continues editing', () => {
    const { guard, model, closed } = makeGuard({ amount: '' });
    guard.reset();
    model.amount = '100';
    guard.requestClose();
    guard.keepEditing();
    expect(guard.confirmOpen).toBe(false);
    expect(closed).toEqual([]);
    expect(guard.isDirty).toBe(true);
  });

  it('closes on discard', () => {
    const { guard, model, closed } = makeGuard({ amount: '' });
    guard.reset();
    model.amount = '100';
    guard.requestClose();
    guard.discardAndClose();
    expect(closed).toEqual(['discarded']);
    expect(guard.armed).toBe(false);
  });

  it('drops the warning when the value returns to its original', () => {
    const { guard, model } = makeGuard({ amount: '' });
    guard.reset();
    model.amount = '100';
    expect(guard.isDirty).toBe(true);
    model.amount = '';
    expect(guard.isDirty).toBe(false);
  });

  it('blocks every close path while saving', () => {
    const { guard, model, closed, setBusy } = makeGuard({ amount: '' });
    guard.reset();
    model.amount = '100';
    setBusy(true);
    guard.requestClose();
    expect(guard.confirmOpen).toBe(false);
    expect(closed).toEqual([]);
  });

  it('closes without a warning after a successful save', () => {
    const { guard, model, closed } = makeGuard({ amount: '' });
    guard.reset();
    model.amount = '100';
    guard.closeAfterSave();
    expect(closed).toEqual(['saved']);
    expect(guard.confirmOpen).toBe(false);
  });

  it('does not let a late default overwrite what the user typed', () => {
    const { guard, model } = makeGuard({ amount: '' });
    guard.reset();
    model.amount = '100';
    guard.noteUserInput();
    guard.captureInitial();
    expect(guard.isDirty).toBe(true);
  });

  it('takes the clean state from late-arriving defaults when untouched', () => {
    const { guard, model } = makeGuard({ amount: '' });
    guard.reset();
    model.amount = '100';
    guard.captureInitial();
    expect(guard.isDirty).toBe(false);
  });

  it('re-arms for the next record', () => {
    const { guard, model, closed } = makeGuard({ amount: '' });
    guard.reset();
    model.amount = '100';
    guard.discardAndClose();
    model.amount = '';
    guard.reset();
    model.amount = '250';
    guard.requestClose();
    expect(guard.confirmOpen).toBe(true);
    expect(closed).toEqual(['discarded']);
  });
});

describe('page-level guard registry', () => {
  it('reports and clears unsaved forms', () => {
    let dirty = true;
    const unregister = registerUnsavedGuard({
      label: 'Test',
      isDirty: () => dirty,
      discard: () => {
        dirty = false;
      }
    });
    expect(hasUnsavedChanges()).toBe(true);
    discardUnsavedChanges();
    expect(hasUnsavedChanges()).toBe(false);
    unregister();
  });

  it('ignores a guard that throws while unmounting', () => {
    const unregister = registerUnsavedGuard({
      label: 'Broken',
      isDirty: () => {
        throw new Error('gone');
      },
      discard: () => {}
    });
    expect(hasUnsavedChanges()).toBe(false);
    unregister();
  });
});

/**
 * Sayfa denetimi ile pencere denetimi aynı kararı verir ama aynı işi yapmaz:
 * navigasyonda pencerenin kapanma yan etkileri (odak geri verme, üst bileşeni
 * kapatma) çalıştırılmaz, buna karşılık sayfanın kendi temizliği çalıştırılır.
 */
describe('sayfa denetimi', () => {
  it('ayrılırken sayfanın kendi temizliğini çalıştırır, onClose akışını değil', () => {
    let model = { note: '' };
    let closed = 0;
    let cleaned = 0;
    const guard = new UnsavedChangesGuard({
      snapshot: () => model,
      onClose: () => {
        closed += 1;
      }
    });
    guard.capture();
    const unregister = guard.registerPageGuard('Test', () => {
      cleaned += 1;
    });
    model = { note: 'değişti' };
    expect(hasUnsavedChanges()).toBe(true);

    discardUnsavedChanges();
    expect(cleaned).toBe(1);
    // Pencere kapatma akışı navigasyonda çalışmaz.
    expect(closed).toBe(0);
    expect(hasUnsavedChanges()).toBe(false);
    unregister();
  });

  it('temizlik hata verse de denetimi bırakır', () => {
    let model = { note: '' };
    const guard = new UnsavedChangesGuard({
      snapshot: () => model,
      onClose: () => {}
    });
    guard.capture();
    const unregister = guard.registerPageGuard('Test', () => {
      throw new Error('depolama yazılamadı');
    });
    model = { note: 'değişti' };

    discardUnsavedChanges();
    // Geçiş ikinci kez sorulmamalı; aksi hâlde kullanıcı sayfada kilitlenir.
    expect(hasUnsavedChanges()).toBe(false);
    unregister();
  });

  it('kayıt sürerken geçişi engeller ve silmeyi uygulamaz', () => {
    let model = { note: 'değişti' };
    let busy = true;
    let cleaned = 0;
    const guard = new UnsavedChangesGuard({
      snapshot: () => model,
      isBusy: () => busy,
      onClose: () => {}
    });
    guard.capture();
    model = { note: 'daha da değişti' };
    const unregister = guard.registerPageGuard('Yeni transfer', () => {
      cleaned += 1;
    });

    expect(busyGuardLabel()).toBe('Yeni transfer');
    // Süren isteği geri almanın yolu yok; "sil ve çık" onu iptal etmiş gibi
    // davranmamalı.
    discardUnsavedChanges();
    expect(cleaned).toBe(0);
    expect(hasUnsavedChanges()).toBe(true);

    busy = false;
    expect(busyGuardLabel()).toBeNull();
    discardUnsavedChanges();
    expect(cleaned).toBe(1);
    unregister();
  });
});
