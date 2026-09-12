import { onMount } from 'svelte';

/** Reactive `matchMedia`. Returns `false` during SSR and until mounted, so
 * markup that depends on it must degrade to the desktop layout. */
export function mediaQuery(query: string): { readonly matches: boolean } {
  let matches = $state(false);
  onMount(() => {
    // jsdom and very old browsers have no matchMedia; the desktop layout is
    // the safe fallback, so just never update.
    if (typeof window.matchMedia !== 'function') return;
    const mq = window.matchMedia(query);
    const update = () => (matches = mq.matches);
    update();
    if (typeof mq.addEventListener === 'function') {
      mq.addEventListener('change', update);
      return () => mq.removeEventListener('change', update);
    }
    mq.addListener(update);
    return () => mq.removeListener(update);
  });
  return {
    get matches() {
      return matches;
    }
  };
}

/** The width at which the app shell switches to the drawer navigation. */
export const SHELL_MOBILE_QUERY = '(max-width: 980px)';
/** Phone-sized layouts: single column forms, sheet-style dialogs. */
export const PHONE_QUERY = '(max-width: 640px)';
