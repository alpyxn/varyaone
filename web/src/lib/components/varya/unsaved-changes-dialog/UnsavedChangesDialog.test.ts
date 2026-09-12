import { afterEach, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import UnsavedChangesDialog from './UnsavedChangesDialog.svelte';

let component: ReturnType<typeof mount> | undefined;

afterEach(async () => {
  if (component) await unmount(component);
  component = undefined;
  document.body.innerHTML = '';
});

function open(handlers: { onKeepEditing: () => void; onDiscard: () => void }) {
  component = mount(UnsavedChangesDialog, {
    target: document.body,
    props: { open: true, ...handlers }
  });
  flushSync();
  return document.querySelector<HTMLElement>('.unsaved-dialog')!;
}

it('shows the shared wording and both exits', () => {
  const dialog = open({ onKeepEditing: vi.fn(), onDiscard: vi.fn() });
  expect(dialog.textContent).toContain('Kaydedilmemiş değişiklikleriniz var.');
  expect(dialog.textContent).toContain(
    'Çıkarsanız bu pencerede yaptığınız değişiklikler silinecek.'
  );
  expect(dialog.querySelector('.unsaved-dialog-keep')!.textContent).toContain(
    'Düzenlemeye devam et'
  );
  expect(dialog.querySelector('.unsaved-dialog-discard')!.textContent).toContain(
    'Değişiklikleri sil ve çık'
  );
});

it('puts the first focus on continuing to edit', () => {
  const dialog = open({ onKeepEditing: vi.fn(), onDiscard: vi.fn() });
  expect(document.activeElement).toBe(dialog.querySelector('.unsaved-dialog-keep'));
});

it('returns to editing on Escape instead of closing the form', () => {
  const onKeepEditing = vi.fn();
  const onDiscard = vi.fn();
  open({ onKeepEditing, onDiscard });
  document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
  flushSync();
  expect(onKeepEditing).toHaveBeenCalled();
  expect(onDiscard).not.toHaveBeenCalled();
});

it('reports each button separately', () => {
  const onKeepEditing = vi.fn();
  const onDiscard = vi.fn();
  const dialog = open({ onKeepEditing, onDiscard });
  dialog.querySelector<HTMLButtonElement>('.unsaved-dialog-discard')!.click();
  flushSync();
  expect(onDiscard).toHaveBeenCalledTimes(1);
  expect(onKeepEditing).not.toHaveBeenCalled();
});
