export const themes = ['light', 'dark'] as const;
export type Theme = (typeof themes)[number];

const STORAGE_KEY = 'varyaone:theme';

class ThemePreference {
  value = $state<Theme>('light');

  load() {
    if (typeof window === 'undefined') return;
    let stored: string | null = null;
    let prefersDark = false;
    try {
      stored = localStorage.getItem(STORAGE_KEY);
      prefersDark = window.matchMedia?.('(prefers-color-scheme: dark)').matches ?? false;
    } catch {
      // Storage and media-query access can be blocked in privacy-restricted browsers.
    }
    this.set(stored === 'dark' || (!stored && prefersDark) ? 'dark' : 'light', false);
  }

  set(value: Theme, persist = true) {
    this.value = value;
    if (typeof document !== 'undefined')
      document.documentElement.classList.toggle('dark', value === 'dark');
    if (persist && typeof localStorage !== 'undefined') {
      try {
        localStorage.setItem(STORAGE_KEY, value);
      } catch {
        // Theme still applies for the current session when persistence is unavailable.
      }
    }
  }

  toggle() {
    this.set(this.value === 'dark' ? 'light' : 'dark');
  }
}

export const themePreference = new ThemePreference();
