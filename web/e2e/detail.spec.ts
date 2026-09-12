import { test, expect, expectNoPageOverflow } from './fixtures/test';
import { gotoRoute, settleRoute } from './fixtures/ready';
import { ROUTE_WIDTHS } from './widths';
import { openFirstRecord } from './fixtures/grid';
import { DETAIL_SCENARIOS } from './detail-scenarios';

/**
 * Record pages, reached the way a user reaches them.
 *
 * Every entry here is a fixture the seed guarantees. There is no skip: a list
 * that has no record, or a record that will not open, is a failure. The old
 * version returned `false` for both "the list is empty" and "the click went
 * somewhere unexpected", then only checked that *some* list had opened — so
 * twenty-two of twenty-three could silently do nothing and the test still
 * passed. Four of them were doing exactly that, because their first row link
 * points at the related party rather than at the record.
 */
for (const width of ROUTE_WIDTHS) {
  test.describe(`${width}px`, () => {
    test.use({ viewport: { width, height: 900 } });

    for (const { list, detail } of DETAIL_SCENARIOS) {
      test(`${list} → record`, async ({ page, api }) => {
        // A movement that was not posted as a multi-line operation has no
        // operation record; the detail page probes for one and falls back to
        // the single movement. The 404 is the probe answering "no".
        if (list === '/stok/hareketler')
          api.allow({ path: /^\/stock-movement-operations\//, status: 404 });
        // Opening the count workspace claims a scanning session, by design:
        // that is what a counting device does when it picks up a count. It is
        // declared rather than "fixed" — but it is why this suite needs a
        // disposable stack, and why the old "these tests only read" note was
        // wrong about more than the puantaj screen.
        if (list === '/stok/sayim') {
          api.allowWrites(/^\/stock-counts\/[0-9a-f-]{36}\/(sessions|passes)$/);
        }

        await gotoRoute(page, api, list, { expectURL: (url) => url.pathname === list });
        const rows = page.locator('main tbody tr');
        await expect(rows.first(), `${list}: the seed guarantees a record here`).toBeVisible();

        await openFirstRecord(page, width, detail);
        await page.waitForURL(detail, { timeout: 15_000 });
        await settleRoute(page, api);

        await expect(
          page.locator('#main-content'),
          `${list}: the record page rendered nothing`
        ).not.toBeEmpty();
        await expectNoPageOverflow(
          page,
          `${page.url().replace(/^https?:\/\/[^/]+/, '')} @ ${width}`
        );
      });
    }
  });
}
