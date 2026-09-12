/** Dismiss only a click that both starts and finishes on the backdrop.
 * Releasing a drag/scroll from inside a picker must not cancel the selection.
 *
 * Selectors only. A data-entry window must not be wired to this helper: there,
 * clicking outside does nothing, and X/Esc/Vazgeç go through the shared
 * UnsavedChangesGuard instead (see $lib/forms/unsaved-changes.svelte.ts).
 */
export function dismissOnBackdrop(node: HTMLElement, dismiss: () => void) {
  let startedOnBackdrop = false;
  const down = (event: PointerEvent) => {
    startedOnBackdrop = event.target === node && event.button === 0;
  };
  const cancel = () => {
    startedOnBackdrop = false;
  };
  const click = (event: MouseEvent) => {
    const shouldDismiss = startedOnBackdrop && event.target === node;
    startedOnBackdrop = false;
    if (!shouldDismiss) return;
    event.preventDefault();
    event.stopPropagation();
    dismiss();
  };
  node.addEventListener('pointerdown', down);
  node.addEventListener('pointercancel', cancel);
  node.addEventListener('click', click);
  return {
    destroy() {
      node.removeEventListener('pointerdown', down);
      node.removeEventListener('pointercancel', cancel);
      node.removeEventListener('click', click);
    }
  };
}
