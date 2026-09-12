import { test as base, expect } from '@playwright/test';
import { ApiMonitor, PageErrorMonitor } from './monitors';
import { detectOverflow, type OverflowReport } from './overflow';

export type VaryaFixtures = {
  api: ApiMonitor;
  pageErrors: PageErrorMonitor;
};

/**
 * The suite's test object.
 *
 * Two things are attached to every test and checked when it ends: what the page
 * did to the API, and what it threw. Before this, a route could return 500 on
 * every request and still pass a layout assertion, because an error screen fits
 * its viewport perfectly.
 */
export const test = base.extend<VaryaFixtures>({
  api: async ({ page }, use) => {
    const monitor = new ApiMonitor(page);
    await use(monitor);
    monitor.dispose();
    // Bodies are read asynchronously; the verdict waits for them so a slow 500
    // cannot be recorded after the list has already been checked.
    await monitor.settle();
    const failures = monitor.unexpectedFailures;
    expect(
      failures.map(
        (f) =>
          `${f.method} ${f.path} → ${f.networkError ? f.networkError : `${f.status} ${f.body}`}`
      ),
      'the page made API requests that failed'
    ).toEqual([]);
    // A test that only looks at a screen must not change the data the next one
    // reads. Screens do write on their own — opening the timesheet on an empty
    // draft period generates it — so this is measured rather than assumed.
    expect(
      monitor.unexpectedWrites.map((w) => `${w.method} ${w.path}`),
      'this test wrote to the API without declaring it (see api.allowWrites)'
    ).toEqual([]);
  },
  pageErrors: async ({ page }, use) => {
    const monitor = new PageErrorMonitor(page);
    await use(monitor);
    monitor.dispose();
    expect(monitor.unexpected, 'the page raised errors').toEqual([]);
  }
});

export { expect };

/**
 * Asserts the page does not scroll sideways, and that nothing meaningful has
 * been pushed off its left edge.
 *
 * Polled rather than sampled once. Widths change with a transition, so a single
 * `evaluate` right after a resize measures the layout half way through the
 * slide and reports a drawer that is about to settle at zero. Polling converges
 * on the state the user actually sees, and still fails when the layout stays
 * broken — which a fixed sleep would only have made slower, not safer.
 */
export async function expectNoPageOverflow(
  page: import('@playwright/test').Page,
  label: string,
  timeout = 10_000
) {
  let report: OverflowReport | undefined;
  await expect
    .poll(
      async () => {
        report = await page.evaluate(detectOverflow);
        return overflowComplaints(report);
      },
      { message: `${label}: the page does not fit its viewport`, timeout }
    )
    .toEqual([]);
}

/** Everything wrong with one measurement, as lines a reader can act on. */
export function overflowComplaints(report: OverflowReport): string[] {
  const complaints: string[] = [];
  for (const o of report.offenders) {
    complaints.push(
      `overflows right: ${o.selector} [${o.left}…${o.right} of ${report.viewportWidth}]`
    );
  }
  for (const o of report.clippedLeft) {
    complaints.push(`cut off left: ${o.selector} [${o.left}…${o.right}]`);
  }
  if (report.documentScrollWidth > report.viewportWidth + 1) {
    complaints.push(
      `the document itself scrolls: ${report.documentScrollWidth} > ${report.viewportWidth}`
    );
  }
  return complaints;
}
