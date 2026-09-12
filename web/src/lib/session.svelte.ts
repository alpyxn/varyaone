/**
 * The signed-in session, fetched once and shared.
 *
 * Opening the workspace used to cost three identical `/session` calls: the
 * layout's access check, the app shell's sidebar and the home page each asked
 * for their own copy. They all want the same answer, so the first caller makes
 * the request and everybody else joins it.
 *
 * Nothing here survives a page load, and every action that changes what a
 * session means - picking another company, signing out, toggling a module -
 * already reloads the whole app, so the cache cannot go stale behind the user's
 * back. Callers that need a guaranteed-fresh copy ask for `reload()`.
 */
import { browser } from '$app/environment';
import { api, type Session } from '$lib/api';

class SessionStore {
  /** The last successful read, or null when there is none (yet). */
  current = $state<Session | null>(null);
  /** True once an attempt has finished, successfully or not. */
  settled = $state(false);

  /** Shared while a request is in flight, so concurrent callers make one call. */
  #pending: Promise<Session> | null = null;

  /**
   * Returns the session, reusing a cached or in-flight one. Rejects exactly the
   * way `api('/session')` does, so callers keep their own 401 handling. A
   * failure is never cached: the next caller tries again.
   */
  async load(): Promise<Session> {
    if (this.current) return this.current;
    if (this.#pending) return this.#pending;

    const request = api<Session>('/session');
    this.#pending = request;
    try {
      const session = await request;
      // Never hold a user's session in this module during SSR - it lives for
      // the life of the server process, not the request.
      if (browser) this.current = session;
      return session;
    } finally {
      if (this.#pending === request) this.#pending = null;
      if (browser) this.settled = true;
    }
  }

  /** Drops the cached session and fetches a fresh one. */
  async reload(): Promise<Session> {
    this.clear();
    return this.load();
  }

  /** Forgets the session without fetching another. */
  clear(): void {
    this.current = null;
    this.settled = false;
    this.#pending = null;
  }
}

export const session = new SessionStore();
