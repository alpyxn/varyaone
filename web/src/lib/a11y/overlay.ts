/** Shared overlay behaviour for the app shell's mobile surfaces.
 *
 * The bits-ui dialogs already trap focus and lock scrolling for us. These
 * helpers cover the hand-rolled surfaces (the mobile sidebar, the topbar
 * tool popovers) so they behave the same way on a phone: no background
 * scrolling behind the panel, focus kept inside it, Escape closes, and the
 * focus goes back to whatever opened it.
 */

const FOCUSABLE =
  'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

let scrollLocks = 0;
let restoreOverflow = '';
let restorePadding = '';

/** Freeze background scrolling while an overlay is up. Reference counted so
 * two overlays open at once do not fight over the body style. */
export function lockBodyScroll(): () => void {
  if (typeof document === 'undefined') return () => {};
  if (scrollLocks === 0) {
    const body = document.body;
    restoreOverflow = body.style.overflow;
    restorePadding = body.style.paddingRight;
    // Desktop scrollbars are reclaimed when overflow goes hidden; pad the
    // gap back so the layout underneath does not jump.
    const gap = window.innerWidth - document.documentElement.clientWidth;
    if (gap > 0) body.style.paddingRight = `${gap}px`;
    body.style.overflow = 'hidden';
  }
  scrollLocks += 1;
  let released = false;
  return () => {
    if (released) return;
    released = true;
    scrollLocks -= 1;
    if (scrollLocks === 0) {
      document.body.style.overflow = restoreOverflow;
      document.body.style.paddingRight = restorePadding;
    }
  };
}

export function focusableWithin(node: HTMLElement): HTMLElement[] {
  // `offsetParent` is null for everything inside a `position: fixed` panel,
  // which is exactly what these overlays are — use the rendered boxes instead.
  return Array.from(node.querySelectorAll<HTMLElement>(FOCUSABLE)).filter(
    (el) => el.getClientRects().length > 0 || el === document.activeElement
  );
}

type TrapOptions = {
  /** When false the trap detaches without touching focus. */
  active?: boolean;
  /** Called on Escape and on a Tab that would leave the panel backwards. */
  onclose?: () => void;
  /** Skip moving focus into the panel on activation. */
  autofocus?: boolean;
};

/** Svelte action: keep Tab focus inside `node` while `active`, close on
 * Escape, and restore focus to the previously focused element on teardown. */
export function focusTrap(node: HTMLElement, options: TrapOptions = {}) {
  let current: TrapOptions = { active: true, autofocus: true, ...options };
  let opener: HTMLElement | null = null;
  let releaseScroll: (() => void) | null = null;

  function onKeydown(event: KeyboardEvent) {
    if (!current.active) return;
    if (event.key === 'Escape') {
      event.stopPropagation();
      current.onclose?.();
      return;
    }
    if (event.key !== 'Tab') return;
    const items = focusableWithin(node);
    if (!items.length) {
      event.preventDefault();
      node.focus();
      return;
    }
    const first = items[0];
    const last = items[items.length - 1];
    const active = document.activeElement as HTMLElement | null;
    if (event.shiftKey && (active === first || !node.contains(active))) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && active === last) {
      event.preventDefault();
      first.focus();
    }
  }

  function activate() {
    opener = document.activeElement as HTMLElement | null;
    releaseScroll = lockBodyScroll();
    node.addEventListener('keydown', onKeydown);
    if (current.autofocus === false) return;
    // The panel may still carry `inert` (and the slide-in transform) at this
    // point: the action runs before Svelte flushes the attribute change.
    // Nothing inert can take focus, so wait for the next frame.
    requestAnimationFrame(() => {
      if (!current.active || !document.contains(node)) return;
      const items = focusableWithin(node);
      (items[0] ?? node).focus({ preventScroll: true });
    });
  }

  function deactivate(restore: boolean) {
    node.removeEventListener('keydown', onKeydown);
    releaseScroll?.();
    releaseScroll = null;
    if (restore && opener && document.contains(opener)) opener.focus({ preventScroll: true });
    opener = null;
  }

  if (current.active !== false) activate();

  return {
    update(next: TrapOptions) {
      const wasActive = current.active !== false;
      current = { active: true, autofocus: true, ...next };
      const isActive = current.active !== false;
      if (!wasActive && isActive) activate();
      else if (wasActive && !isActive) deactivate(true);
    },
    destroy() {
      deactivate(true);
    }
  };
}
