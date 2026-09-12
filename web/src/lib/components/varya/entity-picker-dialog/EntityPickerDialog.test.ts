import { afterEach, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import EntityPickerDialog from './EntityPickerDialog.svelte';
let component: ReturnType<typeof mount>;
afterEach(async () => {
  if (component) await unmount(component);
  document.body.innerHTML = '';
  vi.useRealTimers();
});
it('does not let an old selection timer close a newly opened picker', () => {
  vi.useFakeTimers();
  const target = document.createElement('label');
  document.body.append(target);
  component = mount(EntityPickerDialog, {
    target,
    props: { results: [{ id: '1', title: 'Test cari' }] }
  });
  flushSync();
  const trigger = target.querySelector<HTMLButtonElement>('[aria-haspopup="dialog"]')!;
  trigger.click();
  flushSync();
  target.querySelector<HTMLButtonElement>('[role="option"]')!.click();
  flushSync();
  expect(target.querySelector('[role="dialog"]')).toBeNull();
  trigger.click();
  flushSync();
  const option = target.querySelector<HTMLElement>('[role="option"]')!;
  option.scrollIntoView = vi.fn();
  vi.runAllTimers();
  flushSync();
  expect(target.querySelector('[role="dialog"]')).not.toBeNull();
  expect(document.activeElement).toBe(target.querySelector('input'));
});
