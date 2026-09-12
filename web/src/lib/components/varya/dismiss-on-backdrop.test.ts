import { describe, expect, it, vi } from 'vitest';
import { dismissOnBackdrop } from './dismiss-on-backdrop';
function pointer(target: HTMLElement, type: string) {
  target.dispatchEvent(new MouseEvent(type, { bubbles: true, button: 0 }));
}
describe('picker backdrop dismissal', () => {
  it('does not close after a drag starts inside, but closes a deliberate backdrop click', () => {
    const overlay = document.createElement('div');
    const dialog = document.createElement('div');
    overlay.append(dialog);
    const dismiss = vi.fn();
    const action = dismissOnBackdrop(overlay, dismiss);
    pointer(dialog, 'pointerdown');
    pointer(overlay, 'click');
    expect(dismiss).not.toHaveBeenCalled();
    pointer(overlay, 'pointerdown');
    pointer(overlay, 'click');
    expect(dismiss).toHaveBeenCalledOnce();
    action.destroy();
    pointer(overlay, 'pointerdown');
    pointer(overlay, 'click');
    expect(dismiss).toHaveBeenCalledOnce();
  });
  it('does not close after a cancelled pointer gesture', () => {
    const overlay = document.createElement('div');
    const dismiss = vi.fn();
    const action = dismissOnBackdrop(overlay, dismiss);
    pointer(overlay, 'pointerdown');
    pointer(overlay, 'pointercancel');
    pointer(overlay, 'click');
    expect(dismiss).not.toHaveBeenCalled();
    action.destroy();
  });
});
