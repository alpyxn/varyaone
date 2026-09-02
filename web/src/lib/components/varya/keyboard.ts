export type VaryaShortcut = 'search' | 'new' | 'save' | 'close' | 'edit';

export type VaryaKeyboardHandlers = Partial<Record<VaryaShortcut, (event: KeyboardEvent) => void>>;

export const VARYA_SHORTCUT_EVENT = 'varya:shortcut';

function asElement(target: EventTarget | null): Element | null {
  return target instanceof Element ? target : null;
}

export function isEditableKeyboardTarget(target: EventTarget | null): boolean {
  const element = asElement(target);
  if (!element) return false;
  if (element.matches('input, textarea, select, [contenteditable="true"]')) return true;
  return Boolean(element.closest('input, textarea, select, [contenteditable="true"]'));
}

export function isInsideModal(target: EventTarget | null): boolean {
  const element = asElement(target);
  return Boolean(element?.closest('[role="dialog"], [aria-modal="true"], [data-varya-modal]'));
}

/** Return the shared action without performing side effects. */
export function resolveVaryaShortcut(event: KeyboardEvent): VaryaShortcut | null {
  if (event.defaultPrevented || event.isComposing) return null;

  const key = event.key.toLowerCase();
  const modifier = event.ctrlKey || event.metaKey;
  if (key === 'escape') {
    // Dialog primitives own Escape inside a modal. Text fields also retain
    // Escape for their own clear/cancel behavior.
    return isEditableKeyboardTarget(event.target) || isInsideModal(event.target) ? null : 'close';
  }
  if (isEditableKeyboardTarget(event.target)) return null;
  if (modifier && key === 'k') return 'search';
  if (modifier && key === 'n') return 'new';
  if (modifier && key === 's') return 'save';
  if (!modifier && key === 'f2') return 'edit';
  return null;
}

export function registerVaryaKeyboardShortcuts(handlers: VaryaKeyboardHandlers): () => void {
  const listener = (event: KeyboardEvent) => {
    const shortcut = resolveVaryaShortcut(event);
    const handler = shortcut ? handlers[shortcut] : undefined;
    if (!handler) return;
    event.preventDefault();
    handler(event);
  };
  window.addEventListener('keydown', listener);
  return () => window.removeEventListener('keydown', listener);
}

export function dispatchVaryaShortcut(action: VaryaShortcut): void {
  if (typeof window === 'undefined') return;
  window.dispatchEvent(new CustomEvent(VARYA_SHORTCUT_EVENT, { detail: { action } }));
}

export function listenVaryaShortcut(
  action: VaryaShortcut,
  handler: (event: CustomEvent<{ action: VaryaShortcut }>) => void
): () => void {
  const listener = (event: Event) => {
    const customEvent = event as CustomEvent<{ action: VaryaShortcut }>;
    if (customEvent.detail?.action === action) handler(customEvent);
  };
  window.addEventListener(VARYA_SHORTCUT_EVENT, listener);
  return () => window.removeEventListener(VARYA_SHORTCUT_EVENT, listener);
}
