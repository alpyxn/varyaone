import { expect, type Page } from '@playwright/test';
import type { ApiMonitor } from './monitors';

/**
 * What a route settled into.
 *
 * The old `settle()` waited for `networkidle` and swallowed the timeout, so a
 * login redirect, a 500 and a fully rendered screen were indistinguishable.
 * Every navigation now has to land in one of these three, and the caller says
 * which ones it accepts.
 */
export type RouteState = 'content' | 'empty' | 'error';

/** What `page.waitForURL` accepts. */
export type URLPredicate = string | RegExp | ((url: URL) => boolean);

/** The boot splash covers the first paint; nothing is decided until it goes. */
export async function waitForBoot(page: Page) {
  await expect(page.locator('.boot-splash')).toHaveCount(0, { timeout: 30_000 });
}

/**
 * Waits until the app shell is up with a real session behind it.
 *
 * `!url.startsWith('/giris')` is not enough on its own: the setup wizard and a
 * company-less session both leave the login screen too, and both used to count
 * as a successful sign-in.
 */
export async function waitForAppShell(page: Page) {
  await waitForBoot(page);
  await expect(page.locator('#main-content')).toBeVisible({ timeout: 30_000 });
  // Rendered only once the session request resolved, so it is the difference
  // between "the shell drew" and "the shell drew with a signed-in user".
  await expect(page.getByRole('button', { name: 'Hesap menüsü' })).toBeVisible({
    timeout: 30_000
  });
}

/**
 * Navigates to `route` and waits for it to reach a decided state.
 *
 * Returns which state it reached so the caller can insist on one. Nothing is
 * swallowed: a route that neither renders, nor says it is empty, nor shows an
 * error fails here rather than being measured half-drawn.
 */
export async function gotoRoute(
  page: Page,
  api: ApiMonitor,
  route: string,
  options: { expectURL?: URLPredicate; shell?: boolean } = {}
): Promise<RouteState> {
  await page.goto(route);
  return settleRoute(page, api, { ...options, route });
}

export async function settleRoute(
  page: Page,
  api: ApiMonitor,
  options: { expectURL?: URLPredicate; shell?: boolean; route?: string } = {}
): Promise<RouteState> {
  const { expectURL, shell = true, route } = options;
  const label = route ?? page.url();

  if (expectURL !== undefined) {
    await page.waitForURL(expectURL, { timeout: 30_000 });
  }
  if (shell) {
    await waitForAppShell(page);
  } else {
    await waitForBoot(page);
  }
  // Bounded, app-scoped and loud: see ApiMonitor.waitForIdle.
  await api.waitForIdle();
  return routeState(page, label);
}

/**
 * Reads the decided state off the page.
 *
 * The three markers come from `StateBlock` and `VaryaDataGrid`, which every
 * list and report screen renders through, so this is the app's own answer
 * rather than a guess made from pixel measurements.
 */
export async function routeState(page: Page, label: string): Promise<RouteState> {
  const loading = page.locator('.loading-state, [aria-busy="true"]');
  await expect(loading, `${label}: still loading after the API went quiet`).toHaveCount(0, {
    timeout: 20_000
  });

  if (await page.locator('.state-alert, [role="alert"].state-alert').count()) return 'error';
  if (await page.locator('.state-empty').count()) return 'empty';
  return 'content';
}

/** Asserts the route rendered something rather than an error or a blank. */
export async function expectRendered(state: RouteState, label: string) {
  expect(state, `${label}: route did not render content`).not.toBe('error');
}
