import { afterEach, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import Sheet from './Sheet.svelte';
import { UnsavedChangesGuard } from '$lib/forms/unsaved-changes.svelte';

let component: ReturnType<typeof mount> | undefined;

afterEach(async () => {
  if (component) await unmount(component);
  component = undefined;
  document.body.innerHTML = '';
});

function mountSheet(props: Record<string, unknown>) {
  component = mount(Sheet, {
    target: document.body,
    props: { open: true, title: 'Test', ...props }
  });
  flushSync();
}

/** bits-ui bir katmanı kaldırmayı sonraki tur'a bırakabilir. */
async function waitGone(selector: string) {
  await vi.waitFor(() => {
    flushSync();
    expect(document.querySelector(selector)).toBeNull();
  });
}

function pressEscape() {
  document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
  flushSync();
}

it('closes straight away when no form guard is given', async () => {
  mountSheet({});
  document.querySelector<HTMLButtonElement>('.varya-sheet-close')!.click();
  flushSync();
  await waitGone('.varya-sheet');
});

it('routes X through the guard in form mode', () => {
  const closed = vi.fn();
  const guard = new UnsavedChangesGuard({ snapshot: () => ({}), onClose: closed });
  guard.reset();
  mountSheet({ guard });
  document.querySelector<HTMLButtonElement>('.varya-sheet-close')!.click();
  flushSync();
  expect(closed).toHaveBeenCalledWith('clean');
});

it('asks before closing a changed form', () => {
  const closed = vi.fn();
  const model = { note: '' };
  const guard = new UnsavedChangesGuard({ snapshot: () => ({ ...model }), onClose: closed });
  guard.reset();
  mountSheet({ guard });
  model.note = 'yazıldı';
  document.querySelector<HTMLButtonElement>('.varya-sheet-close')!.click();
  flushSync();
  expect(closed).not.toHaveBeenCalled();
  expect(document.querySelector('.unsaved-dialog')).not.toBeNull();
});

it('keeps the form open when Escape hits the exit confirmation', () => {
  const closed = vi.fn();
  const model = { note: '' };
  const guard = new UnsavedChangesGuard({ snapshot: () => ({ ...model }), onClose: closed });
  guard.reset();
  component = mount(Sheet, {
    target: document.body,
    props: { open: true, title: 'Test', guard }
  });
  flushSync();
  model.note = 'yazıldı';
  pressEscape();
  expect(document.querySelector('.unsaved-dialog')).not.toBeNull();
  pressEscape();
  expect(guard.confirmOpen).toBe(false);
  expect(document.querySelector('.varya-sheet')).not.toBeNull();
  expect(closed).not.toHaveBeenCalled();
});

it('blocks the close button while the form is saving', () => {
  const closed = vi.fn();
  const guard = new UnsavedChangesGuard({
    snapshot: () => ({}),
    isBusy: () => true,
    onClose: closed
  });
  guard.reset();
  mountSheet({ guard });
  const close = document.querySelector<HTMLButtonElement>('.varya-sheet-close')!;
  expect(close.disabled).toBe(true);
  close.click();
  pressEscape();
  flushSync();
  expect(closed).not.toHaveBeenCalled();
  expect(document.querySelector('.varya-sheet')).not.toBeNull();
});
