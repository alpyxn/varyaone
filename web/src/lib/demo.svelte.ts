/**
 * Client state for the public demo deployment.
 *
 * Everything here is inert on a normal installation: `/demo/state` is mounted
 * only when the server runs in demo mode, so a 404 simply means "not a demo"
 * and the banner never appears.
 */
import { api, APIRequestError, onDemoResetting, type Session } from '$lib/api';

export type DemoState = {
  status: 'READY' | 'RESETTING';
  last_reset_at?: string;
  next_reset_at?: string;
  /** The shared demo account, so the login form can arrive filled in. */
  email?: string;
  password?: string;
};

class DemoStore {
  /** True once the server has confirmed this installation is the demo. */
  enabled = $state(false);
  state = $state<DemoState | null>(null);
  /** Set while a rebuild is running, so the UI can take over the screen. */
  rebuilding = $state(false);
  busy = $state(false);
  message = $state('');

  /** The shared demo account the login form is prefilled with. */
  get credentials(): { email: string; password: string } | null {
    const { email, password } = this.state ?? {};
    return email && password ? { email, password } : null;
  }

  /** True when the form still holds the shared demo account, untouched. */
  isDemoAccount(email: string, password: string): boolean {
    const credentials = this.credentials;
    return Boolean(credentials && email === credentials.email && password === credentials.password);
  }

  get nextResetAt(): Date | null {
    const value = this.state?.next_reset_at;
    return value ? new Date(value) : null;
  }

  /** Asks the server whether this is the demo. Safe to call anywhere. */
  async load(): Promise<void> {
    try {
      this.state = await api<DemoState>('/demo/state');
      this.enabled = true;
      this.rebuilding = this.state.status === 'RESETTING';
    } catch {
      this.enabled = false;
      this.state = null;
    }
  }

  /** Signs the visitor in to the shared demo company - no credentials asked. */
  async startSession(): Promise<Session> {
    return api<Session>('/demo/session', { method: 'POST' });
  }

  /**
   * Puts the visitor back into the rebuilt demo and reloads so the app shell
   * rehydrates. A reset does not revoke sessions - it deletes the company they
   * pointed at - so recovery means opening a session on the new company; a new
   * session alone would leave the rendered page holding its empty state.
   *
   * Returns false when this is not a usable demo, so the caller can fall back
   * to the normal signed-out path. A timestamp in sessionStorage stops a
   * reload loop if sessions keep failing for some other reason.
   */
  async resume(): Promise<boolean> {
    const key = 'varyaone.demo.resumed';
    try {
      if (Date.now() - Number(sessionStorage.getItem(key) ?? 0) < 10_000) return false;
    } catch {
      // Storage is unavailable (private mode); one attempt is still worth it.
    }
    try {
      await this.startSession();
    } catch {
      return false;
    }
    try {
      sessionStorage.setItem(key, String(Date.now()));
    } catch {
      // Without storage the loop guard is gone, but a working demo reloads once
      // and finds a valid session.
    }
    location.reload();
    return true;
  }

  /** Rebuilds the demo now, for a visitor who found the data in a mess. */
  async reset(): Promise<void> {
    this.busy = true;
    this.message = '';
    this.rebuilding = true;
    try {
      this.state = await api<DemoState>('/demo/reset', { method: 'POST' });
      // The reset revoked every session, this visitor's included.
      await this.recover();
    } catch (error) {
      const failure = error as APIRequestError;
      this.message = failure.message || 'Demo sıfırlanamadı.';
      this.rebuilding = false;
    } finally {
      this.busy = false;
    }
  }

  /**
   * Waits for a running rebuild to finish, then puts the visitor into the new
   * company and reloads. Without this they would be left looking at a shell
   * whose company no longer exists.
   */
  async recover(): Promise<void> {
    this.rebuilding = true;
    for (let attempt = 0; attempt < 60; attempt++) {
      try {
        const state = await api<DemoState>('/demo/state');
        if (state.status === 'READY') {
          this.state = state;
          await this.startSession();
          location.reload();
          return;
        }
      } catch {
        // The API may refuse everything mid-rebuild; keep waiting.
      }
      await new Promise((resolve) => setTimeout(resolve, 1000));
    }
    this.message = 'Demo beklenenden uzun sürdü. Sayfayı yenileyin.';
    this.rebuilding = false;
  }
}

export const demo = new DemoStore();

/** Routes the API's "demo is rebuilding" signal into the store. */
export function watchDemoResets() {
  onDemoResetting(() => {
    if (!demo.enabled || demo.rebuilding) return;
    void demo.recover();
  });
}
