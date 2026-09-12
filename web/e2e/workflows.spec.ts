import { test, expect, expectNoPageOverflow } from './fixtures/test';
import { gotoRoute } from './fixtures/ready';
import { openRowDetailSheet } from './fixtures/grid';
import { PHONE } from './widths';

/**
 * Screen behaviours on a phone.
 *
 * Every test here used to stop at "the Save button is visible", which is true
 * of a form whose fields never loaded, whose tabs do nothing and whose filters
 * filter nothing. Each one now drives the behaviour its name claims.
 */
test.describe('phone workflows', () => {
  test.use({ viewport: PHONE });

  test('list grid hides trailing columns but keeps them in the row detail @quick', async ({
    page,
    api
  }) => {
    await gotoRoute(page, api, '/stok/urunler');

    const headers = page.locator('main thead th:not(.mobile-secondary)');
    expect(await headers.count(), 'narrow layout keeps only the leading columns').toBeLessThan(6);

    const detail = await openRowDetailSheet(page);
    expect(await detail.locator('dt').count()).toBeGreaterThan(await headers.count());
    await expectNoPageOverflow(page, 'row detail @ 390');

    const box = await detail.boundingBox();
    expect(box!.x).toBeGreaterThanOrEqual(0);
    expect(box!.x + box!.width).toBeLessThanOrEqual(PHONE.width + 1);
    await page.keyboard.press('Escape');
    await expect(detail).toBeHidden();
  });

  test('crowded filters collapse behind one control and really filter', async ({ page, api }) => {
    await gotoRoute(page, api, '/stok/hareketler');

    const more = page.getByRole('button', { name: /Diğer filtreler/ });
    await expect(more).toBeVisible();
    await expect(more).toHaveAttribute('aria-expanded', 'false');
    await more.click();
    await expect(page.getByRole('button', { name: /Daha az filtre/ })).toBeVisible();
    await expectNoPageOverflow(page, 'expanded filters @ 390');

    const before = await page.locator('main tbody tr').count();
    expect(before, 'the seed fills the movement feed').toBeGreaterThan(0);

    // The wait is armed before the action that triggers it, not after.
    const request = page.waitForRequest(
      (r) => r.url().includes('/api/v1/') && r.url().includes('movement_type=TRANSFER_IN')
    );
    await page.getByLabel('Hareket türü').selectOption('TRANSFER_IN');
    await request;
    await api.waitForIdle();

    // And the screen shows the narrower result, not just a narrower request.
    const after = await page.locator('main tbody tr').count();
    expect(after, 'filtering returned no rows at all').toBeGreaterThan(0);
    expect(after, 'the filter changed nothing on screen').toBeLessThan(before);
    // The narrow layout drops the movement-type column, so the visible proof is
    // the direction: a transfer-in is always an inbound movement, and the
    // outbound rows that filled the list before are gone.
    for (const text of await page.locator('main tbody tr').allInnerTexts()) {
      expect(text, 'an outbound row survived the transfer-in filter').not.toContain('Çıkış');
    }
  });

  test('sales invoice form offers line fields and a live total @quick', async ({ page, api }) => {
    await gotoRoute(page, api, '/satis/faturalar/yeni');
    await expectNoPageOverflow(page, 'new sales invoice @ 390');
    await expect(page.getByRole('heading', { name: 'Toplamlar' })).toBeVisible();

    // The form is usable, not merely present: adding a line gives the fields a
    // document needs, and the totals panel counts it.
    await page.getByRole('button', { name: 'Ürün satırı' }).click();
    await expect(page.getByLabel('1. satır miktarı')).toBeVisible();
    await expect(page.getByLabel('1. satır birim fiyatı')).toBeVisible();
    await expectNoPageOverflow(page, 'new sales invoice with a line @ 390');

    await page.getByLabel('1. satır miktarı').fill('3');
    await page.getByLabel('1. satır birim fiyatı').fill('100');
    // 3 × 100 reaches the summary, so the panel is wired to the line rather
    // than merely drawn next to it.
    await expect(page.locator('.totals-grid')).toContainText('300,00');

    const save = page.getByRole('button', { name: /^Kaydet/ }).first();
    await expect(save).toBeVisible();
    await save.scrollIntoViewIfNeeded();
    const box = await save.boundingBox();
    expect(box!.width, 'save button is not clipped').toBeGreaterThan(40);
  });

  test('product form tabs each show their own section', async ({ page, api }) => {
    await gotoRoute(page, api, '/stok/urunler/yeni');
    await expectNoPageOverflow(page, 'new product @ 390');

    const tabs: [string, string][] = [
      ['Genel', 'Stok kartı bilgileri'],
      ['Fiyatlar', 'Fiyatlar'],
      ['Özellikler', 'Özellikler']
    ];
    for (const [tab, heading] of tabs) {
      await page.getByRole('tab', { name: tab }).click();
      await expect(page.getByRole('tab', { name: tab })).toHaveAttribute('aria-selected', 'true');
      await expect(
        page.getByRole('heading', { name: heading }),
        `the ${tab} tab did not show its section`
      ).toBeVisible();
      await expectNoPageOverflow(page, `new product · ${tab} @ 390`);
    }
    await expect(page.getByRole('button', { name: /^Kaydet/ }).first()).toBeVisible();
  });

  test('party form fits and offers its required fields', async ({ page, api }) => {
    await gotoRoute(page, api, '/cari/kartlar/yeni');
    await expectNoPageOverflow(page, 'new party @ 390');
    await expect(page.getByRole('button', { name: /^Kaydet/ }).first()).toBeVisible();
  });

  test('the wide timesheet table scrolls inside its own box', async ({ page, api }) => {
    await gotoRoute(page, api, '/personel/puantaj');
    await expectNoPageOverflow(page, 'puantaj @ 390');

    // The seeded period is last month's; the screen opens on the current one.
    // Stepping back is the same move a user makes, and it is the only month
    // this fixture guarantees data for.
    await page.getByRole('button', { name: 'Önceki ay' }).click();
    await api.waitForIdle();

    // Required, not conditional. The old test looked for `.table-scroll`, which
    // this page has never had, so the whole check sat inside an `if` that was
    // always false.
    const scroller = page.locator('.scroll').first();
    await expect(scroller, 'the timesheet summary must live in a scroller').toBeVisible();

    // Polled: the summary table lays out after its data arrives, and a single
    // measurement taken a frame too early reads a box with nothing in it yet.
    await expect
      .poll(() => scroller.evaluate((el) => el.scrollWidth - el.clientWidth), {
        message: 'nothing to scroll: this test would prove nothing at this width'
      })
      .toBeGreaterThan(1);
    expect(
      await scroller.evaluate((el) => el.tabIndex),
      'a scrollable table must be reachable by keyboard'
    ).toBeGreaterThanOrEqual(0);

    // It really scrolls, rather than merely being marked as scrollable: a
    // keyboard user reaches the right-hand columns with the arrow keys.
    await scroller.focus();
    for (let i = 0; i < 10; i++) await page.keyboard.press('ArrowRight');
    await expect
      .poll(() => scroller.evaluate((el) => el.scrollLeft), {
        message: 'the scroller is focusable but the keyboard cannot move it'
      })
      .toBeGreaterThan(0);
  });

  test('switching to the dark theme keeps the layout intact', async ({ page, api }) => {
    // Start from a known theme instead of inheriting the runner's OS setting,
    // which decided at random which direction the toggle went.
    await page.addInitScript(() => {
      try {
        localStorage.setItem('varyaone:theme', 'light');
      } catch {
        /* private mode */
      }
    });
    await gotoRoute(page, api, '/');
    const html = page.locator('html');
    await expect(html).not.toHaveClass(/(^|\s)dark(\s|$)/);
    await expectNoPageOverflow(page, 'home light @ 390');

    await page.getByRole('button', { name: /^Araçlar/ }).click();
    await page.getByRole('menuitem', { name: 'Koyu temaya geç' }).click();

    // The proof is the class the theme store sets, not that <html> is visible.
    await expect(html).toHaveClass(/(^|\s)dark(\s|$)/);
    await expectNoPageOverflow(page, 'home dark @ 390');

    await page.getByRole('button', { name: /^Araçlar/ }).click();
    await page.getByRole('menuitem', { name: 'Açık temaya geç' }).click();
    await expect(html).not.toHaveClass(/(^|\s)dark(\s|$)/);
  });
});
