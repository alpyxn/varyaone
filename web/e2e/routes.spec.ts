import { test, expect, expectNoPageOverflow } from './fixtures/test';
import { gotoRoute, waitForBoot } from './fixtures/ready';
import { HEADLESS_ROUTES, PUBLIC_ROUTES, STATIC_ROUTES, type RouteSpec } from './routes';
import { ROUTE_WIDTHS } from './widths';

/**
 * Every parameterless route, at the three reference widths.
 *
 * One test per route and width. The suite used to walk all of them inside a
 * single 180-second test per width, so one slow screen could time out and take
 * the other seventy with it, and a failure named the walk rather than the page.
 */

async function expectRouteIdentity(page: import('@playwright/test').Page, spec: RouteSpec) {
  const headless = HEADLESS_ROUTES[spec.path];
  if (headless) {
    await expect(
      page.getByRole(headless.role, { name: headless.name }),
      `${spec.path}: identifying element missing`
    ).toBeVisible();
    return;
  }
  await expect(
    page.locator('#main-content h1').first(),
    `${spec.path}: wrong page, or the page did not render`
  ).toHaveText(spec.heading);
}

for (const width of ROUTE_WIDTHS) {
  test.describe(`${width}px`, () => {
    test.use({ viewport: { width, height: 900 } });

    for (const spec of PUBLIC_ROUTES) {
      test(`${spec.path} (signed out)`, async ({ page, api }) => {
        api.allow({ path: /^\/session$/, status: 401 });
        await page.context().clearCookies();
        await page.goto(spec.path);
        await waitForBoot(page);
        await expect(page.getByRole('heading', { name: spec.heading })).toBeVisible();
        await api.waitForIdle();
        await expectNoPageOverflow(page, `${spec.path} @ ${width}`);
      });
    }

    for (const spec of STATIC_ROUTES) {
      test(`${spec.path}`, async ({ page, api }) => {
        // The backup routes are only mounted where postgresql-client exists,
        // and the SMTP settings row does not exist until somebody saves one.
        // Both answer 404 by design rather than by fault.
        api.allow(
          { path: /^\/settings\/email$/, status: 404 },
          // The backup screen asks the coordinator what it is doing. Those
          // routes are only mounted where postgresql-client exists, so on an
          // installation without it the answer is a designed 404 — the comment
          // above said so, but only the SMTP row was actually allowed.
          { path: /^\/system\/operations\//, status: 404 },
          { path: /^\/system\/backups/, status: 404 }
        );

        const state = await gotoRoute(page, api, spec.path, {
          shell: !spec.chromeless,
          expectURL: (url) => {
            const landed = url.pathname + url.search;
            return spec.lands ? landed === spec.lands : url.pathname === spec.path;
          }
        });
        expect(state, `${spec.path}: rendered its error state`).not.toBe('error');
        await expectRouteIdentity(page, spec);
        await expectNoPageOverflow(page, `${spec.path} @ ${width}`);
      });
    }
  });
}
