import { test, expect } from './fixtures/test';
import { gotoRoute } from './fixtures/ready';
import { openRowDetailSheet } from './fixtures/grid';

/**
 * Touch-target sizes.
 *
 * Only this file runs in the `touch` project. The 44px rules key off
 * `pointer: coarse`, which a desktop browser narrowed to 390px does not set —
 * which is why these checks lived behind a `test.skip` in the desktop project
 * and were reported as skipped on every run.
 */
const MIN_TARGET = 44;

async function undersizedTargets(page: import('@playwright/test').Page, scope: string) {
  return page.evaluate(
    ({ scope, min }) => {
      const root = document.querySelector(scope);
      if (!root) return ['scope not found: ' + scope];
      const bad: string[] = [];
      // Controls, not the prose. An inline link inside a record's field list
      // is text a reader taps through, and holding it to a 44px box would say
      // nothing about whether the screen's actions are reachable.
      for (const el of Array.from(
        root.querySelectorAll<HTMLElement>('button, select, [role="button"]')
      )) {
        const r = el.getBoundingClientRect();
        if (r.width === 0 && r.height === 0) continue;
        if (getComputedStyle(el).visibility === 'hidden') continue;
        if (r.height < min || r.width < min) {
          const label = (el.getAttribute('aria-label') || el.textContent || '').trim().slice(0, 24);
          bad.push(
            `${el.tagName.toLowerCase()} "${label}" ${Math.round(r.width)}×${Math.round(r.height)}`
          );
        }
      }
      return bad;
    },
    { scope, min: MIN_TARGET }
  );
}

test.beforeEach(async ({ page }) => {
  const coarse = await page
    .evaluate(() => matchMedia('(pointer: coarse)').matches)
    .catch(() => true);
  expect(coarse, 'the touch project must emulate a coarse pointer').not.toBe(false);
});

test('topbar controls meet the touch target size @quick', async ({ page, api }) => {
  await gotoRoute(page, api, '/');
  expect(await undersizedTargets(page, '.topbar'), 'topbar controls below 44px').toEqual([]);
});

test('the primary actions of a form meet the touch target size', async ({ page, api }) => {
  await gotoRoute(page, api, '/cari/kartlar/yeni');
  expect(
    await undersizedTargets(page, '.page-actions, .form-actions'),
    'form actions below 44px'
  ).toEqual([]);
});

test('a popup keeps its close and confirm controls tappable', async ({ page, api }) => {
  await gotoRoute(page, api, '/stok/urunler');
  const sheet = await openRowDetailSheet(page);
  await expect(sheet).toBeVisible();
  expect(
    await undersizedTargets(page, '[role="dialog"]'),
    'row detail sheet controls below 44px'
  ).toEqual([]);
});
