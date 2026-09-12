import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import EntityCombobox from './EntityCombobox.svelte';

let component: ReturnType<typeof mount>;
afterEach(async () => {
  if (component) await unmount(component);
  document.body.innerHTML = '';
  vi.useRealTimers();
});
function setup() {
  const target = document.createElement('div');
  document.body.append(target);
  const onSelect = vi.fn();
  component = mount(EntityCombobox, {
    target,
    props: { results: [{ id: 'cari-1', title: 'Test Cari' }], onSelect }
  });
  flushSync();
  const input = target.querySelector('input')!;
  input.focus();
  flushSync();
  return { target, input, onSelect };
}
describe('EntityCombobox dismissal', () => {
  it('keeps the list open when focus temporarily goes to the document during a pointer gesture', () => {
    vi.useFakeTimers();
    const { target, input, onSelect } = setup();
    input.blur();
    flushSync();
    vi.runAllTimers();
    flushSync();
    const option = target.querySelector<HTMLButtonElement>('[role="option"]');
    expect(option).not.toBeNull();
    option!.click();
    flushSync();
    expect(onSelect).toHaveBeenCalledOnce();
    expect(onSelect).toHaveBeenCalledWith({ id: 'cari-1', title: 'Test Cari' });
    expect(input.value).toBe('Test Cari');
    expect(target.querySelector('[role="listbox"]')).toBeNull();
  });
  it('closes on an actual outside pointer press, but not a press in the list', () => {
    const { target } = setup();
    target
      .querySelector('[role="listbox"]')!
      .dispatchEvent(new MouseEvent('pointerdown', { bubbles: true }));
    flushSync();
    expect(target.querySelector('[role="listbox"]')).not.toBeNull();
    document.body.dispatchEvent(new MouseEvent('pointerdown', { bubbles: true }));
    flushSync();
    expect(target.querySelector('[role="listbox"]')).toBeNull();
  });
  it('closes when focus moves to another control or Escape is pressed', () => {
    const { target, input } = setup();
    const outside = document.createElement('button');
    document.body.append(outside);
    outside.focus();
    flushSync();
    expect(target.querySelector('[role="listbox"]')).toBeNull();
    input.focus();
    flushSync();
    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    flushSync();
    expect(target.querySelector('[role="listbox"]')).toBeNull();
  });
});
