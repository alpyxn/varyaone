import { type Page } from '@playwright/test';
import { test, expect } from './fixtures/test';
import { gotoRoute, settleRoute } from './fixtures/ready';
import { openFirstRecord } from './fixtures/grid';

/**
 * Veri giriş pencerelerinde kaydedilmemiş değişiklik koruması.
 *
 * Bu dosya da salt okurdur: her akış ya "Düzenlemeye devam et" ile geri döner
 * ya da "Değişiklikleri sil ve çık" ile kapanır. Hiçbir kayıt oluşturulmaz.
 */

const CONFIRM = 'Kaydedilmemiş değişiklikleriniz var.';

function confirmDialog(page: Page) {
  return page.locator('.unsaved-dialog');
}

/**
 * The dialog's amount field, by name.
 *
 * These tests used to type into `input` first(), which is the party picker's
 * trigger — a control that discards what is typed into it. Nothing was marked
 * changed, so every "it asks before discarding" assertion below was testing a
 * form that had nothing to discard.
 */
function amountField(dialog: ReturnType<Page['getByRole']>) {
  return dialog.getByLabel('Tutar *');
}

async function openOperationDialog(page: Page, api: import('./fixtures/monitors').ApiMonitor) {
  await gotoRoute(page, api, '/cari/tahsilatlar');
  await page.getByRole('button', { name: 'Yeni Tahsilat' }).click();
  const dialog = page.getByRole('dialog').filter({ hasText: 'Yeni Tahsilat' }).first();
  await expect(dialog).toBeVisible();
  return dialog;
}

test.describe('operation popup', () => {
  test('an unchanged form closes without asking', async ({ page, api }) => {
    const dialog = await openOperationDialog(page, api);
    await page.keyboard.press('Escape');
    await expect(confirmDialog(page)).toHaveCount(0);
    await expect(dialog).toBeHidden();
  });

  test('clicking outside never closes a data entry window', async ({ page, api }) => {
    const dialog = await openOperationDialog(page, api);
    await page.locator('.modal-backdrop').click({ position: { x: 5, y: 5 } });
    await expect(dialog).toBeVisible();
    await expect(confirmDialog(page)).toHaveCount(0);
  });

  test('every exit path asks once a field changed', async ({ page, api }) => {
    const dialog = await openOperationDialog(page, api);
    await amountField(dialog).fill('123');

    // X
    await dialog.getByRole('button', { name: 'Pencereyi kapat' }).click();
    await expect(confirmDialog(page)).toContainText(CONFIRM);
    await confirmDialog(page).getByRole('button', { name: 'Düzenlemeye devam et' }).click();
    await expect(dialog).toBeVisible();

    // Esc
    await page.keyboard.press('Escape');
    await expect(confirmDialog(page)).toContainText(CONFIRM);
    await confirmDialog(page).getByRole('button', { name: 'Düzenlemeye devam et' }).click();

    // Vazgeç
    await dialog.getByRole('button', { name: 'Vazgeç' }).click();
    await expect(confirmDialog(page)).toContainText(CONFIRM);
  });

  test('the first focus is on continuing to edit', async ({ page, api }) => {
    const dialog = await openOperationDialog(page, api);
    await amountField(dialog).fill('123');
    await page.keyboard.press('Escape');
    const keep = confirmDialog(page).getByRole('button', { name: 'Düzenlemeye devam et' });
    await expect(keep).toBeFocused();
  });

  test('continuing keeps the typed data, discarding clears it', async ({ page, api }) => {
    const dialog = await openOperationDialog(page, api);
    const amount = amountField(dialog);
    await amount.fill('123');

    await page.keyboard.press('Escape');
    await confirmDialog(page).getByRole('button', { name: 'Düzenlemeye devam et' }).click();
    await expect(amount).toHaveValue('123');

    await page.keyboard.press('Escape');
    await confirmDialog(page).getByRole('button', { name: 'Değişiklikleri sil ve çık' }).click();
    await expect(dialog).toBeHidden();

    await page.getByRole('button', { name: 'Yeni Tahsilat' }).click();
    await expect(
      amountField(page.getByRole('dialog').filter({ hasText: 'Yeni Tahsilat' }).first())
    ).toHaveValue('');
  });

  test('returning a field to its original value drops the warning', async ({ page, api }) => {
    const dialog = await openOperationDialog(page, api);
    const amount = amountField(dialog);
    const original = await amount.inputValue();
    await amount.fill('123');
    await amount.fill(original);
    await page.keyboard.press('Escape');
    await expect(confirmDialog(page)).toHaveCount(0);
    await expect(dialog).toBeHidden();
  });

  test('Escape inside a nested picker leaves the form in place', async ({ page, api }) => {
    const dialog = await openOperationDialog(page, api);
    // The party field is a combobox, not a button: the old selector matched
    // nothing, so this test never opened a picker to press Escape inside.
    const party = dialog.getByRole('combobox', { name: 'Cari' });
    await party.click();
    await expect(party).toHaveAttribute('aria-expanded', 'true');

    // Escape belongs to the open picker; the form behind it must stay put and
    // must not be treated as an attempt to leave.
    await page.keyboard.press('Escape');
    await expect(party).toHaveAttribute('aria-expanded', 'false');
    await expect(dialog).toBeVisible();
    await expect(confirmDialog(page)).toHaveCount(0);
  });
});

test.describe('sheet', () => {
  // Eskiden bu test `main tbody tr a` arıyordu; sabit kıymet listesinde satır
  // linki yok, dolayısıyla her koşuda sessizce atlanıyordu. Kayıt seed'in
  // garantisi: bulunamaması artık hata.
  test('a sheet form asks before throwing away what was typed', async ({ page, api }) => {
    await gotoRoute(page, api, '/sabit-kiymetler');
    await openFirstRecord(
      page,
      page.viewportSize()?.width ?? 1280,
      /\/sabit-kiymetler\/[0-9a-f-]{36}$/
    );
    await page.waitForURL(/\/sabit-kiymetler\/[0-9a-f-]{36}$/);
    await settleRoute(page, api);

    const assign = page.getByRole('button', { name: 'Zimmetle' });
    await expect(assign, 'seeded fixed asset must be assignable').toBeVisible();
    await assign.click();

    const sheet = page.locator('.varya-sheet');
    await expect(sheet).toBeVisible();
    await sheet.locator('input').last().fill('test notu');
    await sheet.getByRole('button', { name: 'Kapat' }).click();
    await expect(confirmDialog(page)).toContainText(CONFIRM);
    await confirmDialog(page).getByRole('button', { name: 'Değişiklikleri sil ve çık' }).click();
    await expect(sheet).toBeHidden();
  });
});

test.describe('local draft recovery', () => {
  test('a refreshed transfer form offers what was typed back', async ({ page, api }) => {
    await gotoRoute(page, api, '/stok/transferler');
    await page.getByRole('button', { name: /yeni transfer/i }).click();

    const dialog = page.locator('.dialog', { hasText: 'Yeni Transfer' });
    await expect(dialog).toBeVisible();
    await dialog.locator('select').first().selectOption('WORKFLOW');
    // Taslak, düzenleme durduktan ~1 sn sonra yazılır.
    await page.waitForTimeout(1500);

    await page.reload();
    await settleRoute(page, api);
    await page.getByRole('button', { name: /yeni transfer/i }).click();

    const notice = page.locator('.draft-notice');
    await expect(notice).toContainText('Kaydedilmemiş taslak bulundu');
    await notice.getByRole('button', { name: 'Taslağı geri yükle' }).click();
    await expect(dialog.locator('select').first()).toHaveValue('WORKFLOW');
  });

  test('deleting the draft keeps it from coming back', async ({ page, api }) => {
    await gotoRoute(page, api, '/stok/transferler');
    await page.getByRole('button', { name: /yeni transfer/i }).click();
    await page.locator('.dialog select').first().selectOption('WORKFLOW');
    await page.waitForTimeout(1500);

    await page.reload();
    await settleRoute(page, api);
    await page.getByRole('button', { name: /yeni transfer/i }).click();
    await page.locator('.draft-notice').getByRole('button', { name: 'Taslağı sil' }).click();
    await expect(page.locator('.draft-notice')).toHaveCount(0);

    await page.reload();
    await settleRoute(page, api);
    await page.getByRole('button', { name: /yeni transfer/i }).click();
    await expect(page.locator('.draft-notice')).toHaveCount(0);
  });

  test('a draft belongs to one user and company only', async ({ page, api }) => {
    await gotoRoute(page, api, '/stok/transferler');
    await page.getByRole('button', { name: /yeni transfer/i }).click();
    await page.locator('.dialog select').first().selectOption('WORKFLOW');
    await page.waitForTimeout(1500);

    const keys = await page.evaluate(() =>
      Object.keys(localStorage).filter((key) => key.startsWith('varyaone:draft:'))
    );
    expect(keys.length, 'taslak kullanıcı ve şirketle anahtarlanır').toBeGreaterThan(0);
    for (const key of keys) expect(key.split(':').length).toBeGreaterThanOrEqual(7);
  });

  test('a storage failure leaves the form and Save working', async ({ page, api }) => {
    await gotoRoute(page, api, '/stok/transferler');
    await page.evaluate(() => {
      Storage.prototype.setItem = () => {
        throw new DOMException('quota', 'QuotaExceededError');
      };
    });
    await page.getByRole('button', { name: /yeni transfer/i }).click();
    const dialog = page.locator('.dialog', { hasText: 'Yeni Transfer' });
    await dialog.locator('select').first().selectOption('WORKFLOW');
    await page.waitForTimeout(1500);
    await expect(dialog).toContainText('Taslak kaydedilemedi.');
    await expect(dialog.getByRole('button', { name: /Transferi oluştur/ })).toBeVisible();
    await expect(dialog.locator('select').first()).toHaveValue('WORKFLOW');
  });
});
