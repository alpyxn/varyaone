import { expect, type Locator, type Page } from '@playwright/test';

/** Below this width the grid drops its trailing columns and offers the row
 * detail sheet instead; it is the same breakpoint the component uses. */
const NARROW_MAX = 680;

/**
 * Opens the first row of a list the way a person would, and lands on `detail`.
 *
 * The destination is passed in rather than guessed, because "click the first
 * link in the row" is wrong on several grids: a ledger movement links to its
 * party, not to itself, so that shortcut navigated somewhere else and the old
 * helper read the mismatch as "this list has no records" and skipped. A link
 * that actually points at the record is used when the row offers one, and the
 * grid's own row-open gesture otherwise.
 *
 * `force` is never used. A row that cannot be clicked because something covers
 * it is a defect worth failing on, which is precisely what forcing hid.
 */
export async function openFirstRecord(page: Page, width: number, detail: RegExp) {
  await openRecordRow(page, page.locator('main tbody tr').first(), width, detail);
}

/** The same gestures, against a row the caller has already picked out. */
export async function openRecordRow(page: Page, row: Locator, width: number, detail: RegExp) {
  await expect(row).toBeVisible();
  await row.scrollIntoViewIfNeeded();

  // A *visible* link in the row that really goes to this record. Visibility
  // matters: the narrow layout keeps the trailing columns in the DOM but hides
  // them, and clicking one of those is not something a phone user can do.
  const links = row.locator('a[href]');
  for (let i = 0; i < (await links.count()); i++) {
    const link = links.nth(i);
    const href = await link.getAttribute('href');
    if (href && detail.test(href) && (await link.isVisible())) {
      await link.click();
      return;
    }
  }

  // The shared data grid: the row detail sheet on a phone, a double click on
  // the row's own box otherwise (it ignores the gesture on links and buttons).
  if (await row.locator('td.mobile-more').count()) {
    if (width <= NARROW_MAX) {
      await row.locator('td.mobile-more button').click();
      const sheet = page.getByRole('dialog', { name: 'Kayıt detayı' });
      await expect(sheet).toBeVisible();
      await sheet.getByRole('button', { name: 'Kaydı aç' }).click();
      return;
    }
    await row.dblclick({ position: { x: 4, y: 4 } });
    return;
  }

  // Hand-written tables (employees, fixed assets) navigate on a single click.
  await row.click({ position: { x: 4, y: 4 } });
}

/**
 * Opens the row detail sheet without opening the record. Used by the phone
 * workflow tests, which care about the sheet rather than the destination.
 */
export async function openRowDetailSheet(page: Page) {
  await page.locator('main tbody td.mobile-more button').first().click();
  const sheet = page.getByRole('dialog', { name: 'Kayıt detayı' });
  await expect(sheet).toBeVisible();
  return sheet;
}

/**
 * Types into a combobox and picks the first suggestion it offers.
 *
 * Scoped through the combobox's own `aria-controls` listbox on purpose:
 * `getByRole('option')` alone also matches the `<option>` elements of every
 * native `<select>` on the page, and the currency select is the one that
 * answers first.
 *
 * Returns the chosen entry's label, so a caller can assert the record came
 * back with the thing it actually picked.
 */
export async function pickFromCombobox(page: Page, name: string, query: string) {
  const combobox = page.getByRole('combobox', { name, exact: true }).first();
  await combobox.click();
  await combobox.fill(query);

  const listboxID = await combobox.getAttribute('aria-controls');
  expect(listboxID, `${name}: combobox declares no listbox`).toBeTruthy();

  // The option that actually matches the query, not merely the first one on
  // offer. These lists are filtered by an asynchronous search, so the list
  // still shows the previous, unfiltered results for a moment after typing --
  // and a test that clicks position one during that moment picks an unrelated
  // record and then measures it, perfectly green.
  const option = page.locator(`#${listboxID} [role="option"]`).filter({ hasText: query }).first();
  await expect(option, `${name}: "${query}" matched nothing`).toBeVisible();

  const label = (await option.innerText()).split('\n')[0].trim();
  await option.click();
  await expect(combobox).toHaveAttribute('aria-expanded', 'false');
  return label;
}
