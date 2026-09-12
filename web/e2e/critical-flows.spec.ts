import { test, expect } from './fixtures/test';
import { gotoRoute, settleRoute } from './fixtures/ready';
import { openRecordRow, pickFromCombobox } from './fixtures/grid';

/**
 * A few end-to-end flows: UI → API → a result that survives a reload.
 *
 * Deliberately few. Tax, rounding, costing, idempotency and the variations
 * around them belong in the Go and Vitest suites, which run them in seconds;
 * what only a browser can prove is that the screen is wired to the API and
 * that what it says was saved really was. So each test here ends by leaving
 * the page and coming back, never at a toast.
 *
 * These tests write. They run against the disposable stack described in
 * e2e/README.md, never a shared installation, and each one makes its own
 * records under a code unique to the worker so two in parallel cannot collide.
 */

/** A code no other test — or worker, or re-run — will also be using. */
function uniqueSuffix(testInfo: { parallelIndex: number }) {
  return `${testInfo.parallelIndex}${Date.now().toString().slice(-7)}`;
}

test.describe('critical flows', () => {
  test('create a party, find it in the list, read it back @quick', async ({ page, api }, info) => {
    api.allowWrites(/^\/parties$/);
    const suffix = uniqueSuffix(info);
    const legalName = `E2E Test Ticaret ${suffix} A.Ş.`;
    const code = `E2E-${suffix}`;

    await gotoRoute(page, api, '/cari/kartlar/yeni');
    await page.getByLabel('Cari kodu').fill(code);
    await page.getByLabel('Resmî unvan *').fill(legalName);
    await page
      .getByRole('button', { name: /^Kaydet/ })
      .first()
      .click();

    // Saving returns to the list. Find the new card by searching for its code,
    // rather than by hoping it is the first row.
    await page.waitForURL((url) => url.pathname === '/cari/kartlar', { timeout: 30_000 });
    await settleRoute(page, api);
    await page.getByPlaceholder(/ara/i).first().fill(legalName);
    await api.waitForIdle();
    const row = page.locator('main tbody tr').filter({ hasText: legalName });
    await expect(row, 'searching for the new card did not narrow to it').toHaveCount(1);

    // Open that row — not merely the first one — and read the values back from
    // the server rather than from the screen that saved them.
    const detail = /\/cari\/kartlar\/[0-9a-f-]{36}$/;
    await openRecordRow(page, row, page.viewportSize()?.width ?? 1440, detail);
    await page.waitForURL(detail, { timeout: 30_000 });
    await settleRoute(page, api);
    const recordURL = page.url();

    await page.goto(recordURL);
    await settleRoute(page, api);
    await expect(page.locator('#main-content')).toContainText(legalName);
    await expect(page.locator('#main-content')).toContainText(code);
  });

  test('draft a sales invoice with a line, then reopen it', async ({ page, api }, info) => {
    api.allowWrites(/^\/sales\/invoices$/);
    const suffix = uniqueSuffix(info);
    await gotoRoute(page, api, '/satis/faturalar/yeni');

    // Pick the party and the product the seed guarantees, by searching.
    const partyName = await pickFromCombobox(page, 'Müşteri cari', 'Anadolu');

    await page.getByRole('button', { name: 'Ürün satırı' }).click();
    // A product with no variant definition: a variant-tracked card would need
    // a second choice, which is a different flow and belongs in its own test.
    await pickFromCombobox(page, '1. satır kartı', 'Toner Kartuş');

    await page.getByLabel('1. satır miktarı').fill('4');
    await page.getByLabel('1. satır birim fiyatı').fill('250');
    // 4 × 250 before tax: the totals panel is computed from the line, so this
    // is the first place the form can be caught lying.
    await expect(page.locator('.totals-grid')).toContainText('1.000,00');

    const note = `E2E taslak ${suffix}`;
    await page.getByLabel('Açıklama').first().fill(note);

    await page.getByRole('button', { name: 'Taslağı kaydet' }).click();
    await page.waitForURL(/\/satis\/faturalar\/[0-9a-f-]{36}$/, { timeout: 30_000 });
    await settleRoute(page, api);
    const recordURL = page.url();

    // Reopened from the server rather than read off the screen that saved it.
    // The header fields are form inputs, so they are checked by value; text
    // assertions would pass on an empty form.
    await page.goto(recordURL);
    await settleRoute(page, api);
    await expect(page.getByRole('combobox', { name: 'Müşteri cari', exact: true })).toHaveValue(
      new RegExp(partyName.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'))
    );
    await expect(page.getByLabel('Açıklama').first()).toHaveValue(note);
    await expect(page.getByLabel('1. satır miktarı')).toHaveValue('4');
    // The server's own arithmetic, not the form's preview: 4 × 250 net, 20% VAT.
    const totals = page.locator('.totals-grid');
    await expect(totals).toContainText('1.000,00');
    await expect(totals).toContainText('1.200,00');
  });

  test('a collection reduces the open balance, and reversing puts it back', async ({
    page,
    api
  }, info) => {
    api.allowWrites(
      /^\/parties$/,
      /^\/finance\/manual-entries$/,
      /^\/finance\/collections$/,
      /^\/finance\/collections\/[0-9a-f-]{36}\/reverse$/
    );
    const suffix = uniqueSuffix(info);
    const legalName = `E2E Bakiye ${suffix} A.Ş.`;

    // The party is created here rather than borrowed from the seed: this test
    // moves a balance, and two workers sharing one customer would each see the
    // other's postings.
    await gotoRoute(page, api, '/cari/kartlar/yeni');
    await page.getByLabel('Resmî unvan *').fill(legalName);
    await page
      .getByRole('button', { name: /^Kaydet/ })
      .first()
      .click();
    await page.waitForURL((url) => url.pathname === '/cari/kartlar', { timeout: 30_000 });
    await settleRoute(page, api);

    await page.getByPlaceholder(/ara/i).first().fill(legalName);
    await api.waitForIdle();
    const row = page.locator('main tbody tr').filter({ hasText: legalName });
    await expect(row).toHaveCount(1);
    const detail = /\/cari\/kartlar\/[0-9a-f-]{36}$/;
    await openRecordRow(page, row, page.viewportSize()?.width ?? 1440, detail);
    await page.waitForURL(detail, { timeout: 30_000 });
    await settleRoute(page, api);
    const partyURL = page.url();

    // Owe us 1.000: a manual debit, which needs no invoice and no stock.
    await page.getByRole('button', { name: /Cariyi Borçlandır/ }).click();
    const debit = page.getByRole('dialog').filter({ hasText: 'Manuel Hareket' }).first();
    await debit.getByLabel('Tutar *').fill('1000');
    await debit.getByLabel('Açıklama *').fill(`E2E borç ${suffix}`);
    await debit.getByRole('button', { name: 'Kaydet' }).click();
    await expect(debit).toBeHidden();

    await page.goto(partyURL);
    await settleRoute(page, api);
    const summary = page.locator('#main-content');
    await expect(summary, 'the debit did not reach the balance').toContainText('1.000,00 ₺');

    // Collect 400 of it.
    await page.getByRole('button', { name: /Tahsilat Al/ }).click();
    const collect = page.getByRole('dialog').filter({ hasText: 'Tahsilat' }).first();
    await collect.getByLabel('Tutar *').fill('400');
    await collect.getByRole('button', { name: 'Kaydet' }).click();
    await expect(collect).toBeHidden();

    await page.goto(partyURL);
    await settleRoute(page, api);
    await expect(summary, 'the collection did not reduce the balance').toContainText('600,00 ₺');

    // Now undo it from the collection's own page, and the balance comes back.
    await gotoRoute(page, api, '/cari/tahsilatlar');
    await page.getByPlaceholder(/ara/i).first().fill(legalName);
    await api.waitForIdle();
    const receipt = page.locator('main tbody tr').filter({ hasText: legalName });
    await expect(receipt).toHaveCount(1);
    const receiptDetail = /\/cari\/tahsilatlar\/[0-9a-f-]{36}$/;
    await openRecordRow(page, receipt, page.viewportSize()?.width ?? 1440, receiptDetail);
    await page.waitForURL(receiptDetail, { timeout: 30_000 });
    await settleRoute(page, api);

    await page.getByRole('button', { name: /Ters\s+Kayıt Oluştur/ }).click();
    // The reversal asks for a reason first; its confirm label is set by the
    // screen, so the submit button is found by type rather than by wording.
    const reason = page.getByRole('dialog').first();
    await expect(reason).toBeVisible();
    await reason.locator('#operation-reason').fill(`E2E iptal ${suffix}`);
    await reason.locator('button[type="submit"]').click();
    await expect(reason).toBeHidden();

    await page.goto(partyURL);
    await settleRoute(page, api);
    await expect(summary, 'reversing the collection did not restore the balance').toContainText(
      '1.000,00 ₺'
    );
  });
});

/**
 * The three workflows a person actually does, each ending where the money and
 * the stock end up rather than at a toast.
 *
 * They write, so every one of them makes its own records under a code unique
 * to the worker. None of them touches the seeded balances the read-only tests
 * measure, and none depends on another having run first.
 */
test.describe('workflows', () => {
  /**
   * A sales order becomes an invoice, through the dispatch the product
   * requires in between.
   *
   * The order screen offers an invoice conversion only once the goods have
   * left, so the flow a person actually performs is order → dispatch →
   * invoice, and skipping the middle step is not something the app allows.
   * What matters at the end is that the invoice carries the order's party,
   * quantity and amount, and still points back at where it came from.
   */
  test('a confirmed sales order reaches an invoice through its dispatch', async ({
    page,
    api
  }, info) => {
    api.allowWrites(
      /^\/sales\/orders$/,
      /^\/sales\/orders\/[0-9a-f-]{36}\/(confirm|send|accept)$/,
      /^\/sales\/dispatches$/,
      /^\/sales\/dispatches\/[0-9a-f-]{36}\/(finalize|post)$/,
      /^\/sales\/invoices$/
    );
    const suffix = uniqueSuffix(info);

    await gotoRoute(page, api, '/satis/siparisler/yeni');
    const partyName = await pickFromCombobox(page, 'Müşteri cari', 'Anadolu');
    await page.getByRole('button', { name: 'Ürün satırı' }).click();
    await pickFromCombobox(page, '1. satır kartı', 'Toner Kartuş');
    await page.getByLabel('1. satır miktarı').fill('3');
    await page.getByLabel('1. satır birim fiyatı').fill('100');
    await page.getByLabel('Açıklama').first().fill(`E2E sipariş ${suffix}`);

    // Saved and confirmed in one step: an order has to be confirmed before
    // anything can be made from it, and that is the button the screen offers.
    await page.getByRole('button', { name: /^Kaydet ve / }).click();
    await page.waitForURL(/\/satis\/siparisler\/[0-9a-f-]{36}$/, { timeout: 30_000 });
    await settleRoute(page, api);
    const orderID = page.url().split('/').pop() ?? '';
    expect(orderID, 'the order has no id').toMatch(/^[0-9a-f-]{36}$/);

    // Step one: the dispatch. Its absence would mean the order never reached a
    // confirmed state, which is worth failing on rather than working around.
    const toDispatch = page.getByRole('button', { name: 'İrsaliye oluştur' });
    await expect(toDispatch, 'the confirmed order offers no dispatch').toBeVisible();
    await toDispatch.click();
    await page.waitForURL((url) => url.pathname === '/satis/irsaliyeler/yeni', { timeout: 30_000 });
    await settleRoute(page, api);
    await expect(
      page.getByLabel('1. satır miktarı'),
      'the order line did not carry into the dispatch'
    ).toHaveValue('3');

    // Posted, not drafted: only a posted dispatch can be invoiced.
    await page.getByRole('button', { name: /^Kaydet ve / }).click();
    await page.waitForURL(/\/satis\/irsaliyeler\/[0-9a-f-]{36}$/, { timeout: 30_000 });
    await settleRoute(page, api);

    // Step two: the invoice.
    const toInvoice = page.getByRole('button', { name: 'Fatura oluştur' });
    await expect(toInvoice, 'the posted dispatch offers no invoice').toBeVisible();
    await toInvoice.click();
    await page.waitForURL((url) => url.pathname === '/satis/faturalar/yeni', { timeout: 30_000 });
    await settleRoute(page, api);
    await expect(
      page.getByLabel('1. satır miktarı'),
      'the dispatch line did not carry into the invoice'
    ).toHaveValue('3');

    await page.getByRole('button', { name: 'Taslağı kaydet' }).click();
    await page.waitForURL(/\/satis\/faturalar\/[0-9a-f-]{36}$/, { timeout: 30_000 });
    await settleRoute(page, api);
    const invoiceURL = page.url();

    // Read back from the server, not from the screen that saved it.
    await page.goto(invoiceURL);
    await settleRoute(page, api);
    await expect(page.getByRole('combobox', { name: 'Müşteri cari', exact: true })).toHaveValue(
      new RegExp(partyName.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'))
    );
    await expect(page.getByLabel('1. satır miktarı')).toHaveValue('3');
    // 3 × 100 net, 20% VAT — the server's arithmetic, not the form's preview.
    const totals = page.locator('.totals-grid');
    await expect(totals).toContainText('300,00');
    await expect(totals).toContainText('360,00');

    // And the chain back to the order survived the save. An invoice that lost
    // it leaves the order permanently reported as uninvoiced.
    const invoiceID = invoiceURL.split('/').pop() ?? '';
    const invoice = await page.evaluate(async (id) => {
      const response = await fetch(`/api/v1/sales/invoices/${id}`, {
        headers: { accept: 'application/json' }
      });
      return { status: response.status, body: await response.text() };
    }, invoiceID);
    expect(invoice.status, 'reading the invoice back over the API failed').toBe(200);
    expect(
      invoice.body,
      'the invoice records no source document; the order stays uninvoiced forever'
    ).toMatch(/dispatch|source/i);
  });

  /**
   * A stock transfer moves quantity between two warehouses.
   *
   * Both sides are read back from the server's own stock position, because a
   * transfer that posted only the outgoing half looks exactly like a
   * successful one from the screen that submitted it — and so does one that
   * posted nothing at all and showed a toast.
   */
  test('a quick transfer moves stock out of one warehouse and into another', async ({
    page,
    api
  }) => {
    api.allowWrites(/^\/warehouse-transfers$/);

    await gotoRoute(page, api, '/stok/transferler');

    // The two warehouses and the product are resolved by id up front, so the
    // quantities below are read for exactly what the transfer moved rather
    // than for whatever happened to be named the same.
    const fixture = await page.evaluate(async () => {
      const read = async (path: string) => {
        const response = await fetch(path, { headers: { accept: 'application/json' } });
        return response.ok ? ((await response.json()) as { items?: unknown[] }) : { items: [] };
      };
      const warehouses = (await read('/api/v1/warehouses')).items ?? [];
      const products = (await read('/api/v1/products?q=Toner&limit=1')).items ?? [];
      return { warehouses, products };
    });
    const warehouses = (fixture.warehouses as Record<string, unknown>[]).filter(
      (warehouse) => warehouse.type === 'STANDARD' && warehouse.is_active && !warehouse.is_transit
    );
    expect(
      warehouses.length,
      'a transfer needs two standard warehouses; the seed provides fewer'
    ).toBeGreaterThanOrEqual(2);
    const product = (fixture.products as Record<string, unknown>[])[0];
    expect(product, 'the seeded product the transfer moves is missing').toBeTruthy();

    const source = warehouses[0];
    const destination = warehouses[1];
    const productID = String(product.id);
    const before = {
      source: await physicalQuantity(page, String(source.id), productID),
      destination: await physicalQuantity(page, String(destination.id), productID)
    };

    await page.getByRole('button', { name: /yeni transfer/i }).click();
    const dialog = page.getByRole('dialog').filter({ hasText: 'Yeni Transfer' }).first();
    await expect(dialog).toBeVisible();

    // Quick transfer: one step, no ship/receive cycle. The workflow variant is
    // a different flow and belongs in a test of its own.
    await pickFromCombobox(page, 'Çıkış deposu', String(source.name));
    await pickFromCombobox(page, 'Varış deposu', String(destination.name));

    // The dialog opens with one empty line already; adding another would only
    // produce "pick a product for line 2".
    const productName = await pickFromCombobox(page, 'Stok kartı 1', String(product.name));
    await dialog.getByLabel(`${productName} miktarı`).fill('2');

    await dialog.getByRole('button', { name: 'Transferi oluştur' }).click();
    // The dialog closing is the screen's opinion. The quantities below are the
    // server's.
    await expect(dialog).toBeHidden({ timeout: 30_000 });
    await settleRoute(page, api);

    const after = {
      source: await physicalQuantity(page, String(source.id), productID),
      destination: await physicalQuantity(page, String(destination.id), productID)
    };
    expect(
      after.source,
      `${source.name}: the transfer did not leave the source warehouse`
    ).toBeCloseTo(before.source - 2, 6);
    expect(
      after.destination,
      `${destination.name}: the transfer never arrived at the destination`
    ).toBeCloseTo(before.destination + 2, 6);

    // And the transfer is a record of its own, reopenable from the list.
    await gotoRoute(page, api, '/stok/transferler');
    await expect(
      page.locator('main tbody tr').first(),
      'the transfer list shows no record'
    ).toBeVisible();
  });

  /**
   * Another company's data is not reachable, by list or by URL.
   *
   * The isolation is enforced in the database, but a screen that asks for a
   * record it should not see has to be refused rather than shown an empty
   * form, and the API has to refuse it too. Both are checked, because the
   * screen alone would pass on a page that simply rendered nothing.
   */
  test('a record from another company is not reachable @quick', async ({ page, api }) => {
    // Reading a record that belongs to somebody else is expected to be
    // refused; the status says which kind of refusal.
    api.allow({ path: /^\/parties\//, status: [403, 404] });

    await gotoRoute(page, api, '/cari/kartlar');
    const rows = page.locator('main tbody tr');
    await expect(rows.first(), 'the seed guarantees a party here').toBeVisible();

    // An id that is syntactically valid and belongs to no company this session
    // can see. A refusal is the only correct answer; a rendered record, or an
    // empty form pretending the record exists, are both failures.
    const foreign = '00000000-0000-4000-8000-000000000001';
    const direct = await page.evaluate(async (id) => {
      const response = await fetch(`/api/v1/parties/${id}`, {
        headers: { accept: 'application/json' }
      });
      return response.status;
    }, foreign);
    expect([401, 403, 404], `the API answered ${direct} for another company's record`).toContain(
      direct
    );

    await page.goto(`/cari/kartlar/${foreign}`);
    await settleRoute(page, api);
    // Whatever the screen shows, it must not be a usable record: no save
    // button on a record this session may not have.
    await expect(
      page.getByRole('button', { name: /^Kaydet/ }),
      'the screen offered to save another company’s record'
    ).toHaveCount(0);
  });
});

/**
 * One product's physical quantity in one warehouse, read from the server.
 *
 * A warehouse holding none of it has no position row at all, which the API
 * answers with a 404; that is zero, not a failure.
 */
async function physicalQuantity(
  page: import('@playwright/test').Page,
  warehouseID: string,
  productID: string
): Promise<number> {
  const result = await page.evaluate(
    async ([warehouse, product]) => {
      const response = await fetch(
        `/api/v1/stock/positions?warehouse_id=${warehouse}&product_id=${product}`,
        { headers: { accept: 'application/json' } }
      );
      if (response.status === 404) return { status: 404, quantity: '0' };
      if (!response.ok) return { status: response.status, quantity: '0' };
      const body = (await response.json()) as { physical_quantity?: string };
      return { status: 200, quantity: body.physical_quantity ?? '0' };
    },
    [warehouseID, productID]
  );
  expect([200, 404], `reading the stock position answered ${result.status}`).toContain(
    result.status
  );
  return Number(result.quantity);
}
