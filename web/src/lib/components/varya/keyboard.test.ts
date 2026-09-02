import { describe, expect, it } from 'vitest';
import { resolveVaryaShortcut } from './keyboard';

function key(key: string, options: Partial<KeyboardEventInit> = {}) {
  return new KeyboardEvent('keydown', { key, bubbles: true, ...options });
}

describe('Varya One keyboard contract', () => {
  it('maps global shortcuts outside editable controls', () => {
    expect(resolveVaryaShortcut(key('k', { ctrlKey: true }))).toBe('search');
    expect(resolveVaryaShortcut(key('n', { ctrlKey: true }))).toBe('new');
    expect(resolveVaryaShortcut(key('s', { ctrlKey: true }))).toBe('save');
    expect(resolveVaryaShortcut(key('F2'))).toBe('edit');
    expect(resolveVaryaShortcut(key('Escape'))).toBe('close');
  });

  it('does not steal typing shortcuts from inputs and textareas', () => {
    const input = document.createElement('input');
    document.body.append(input);
    const event = key('s', { ctrlKey: true });
    Object.defineProperty(event, 'target', { value: input });
    expect(resolveVaryaShortcut(event)).toBeNull();
    input.remove();
  });

  it('leaves Escape to the dialog primitive inside a modal', () => {
    const dialog = document.createElement('div');
    dialog.setAttribute('role', 'dialog');
    document.body.append(dialog);
    const event = key('Escape');
    Object.defineProperty(event, 'target', { value: dialog });
    expect(resolveVaryaShortcut(event)).toBeNull();
    dialog.remove();
  });

  it('ignores composed or already handled events', () => {
    const composed = key('k', { ctrlKey: true, isComposing: true });
    const handled = key('k', { ctrlKey: true });
    Object.defineProperty(handled, 'defaultPrevented', { value: true });
    expect(resolveVaryaShortcut(composed)).toBeNull();
    expect(resolveVaryaShortcut(handled)).toBeNull();
  });
});
