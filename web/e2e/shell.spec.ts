import { test, expect, expectNoPageOverflow } from './fixtures/test';
import { gotoRoute } from './fixtures/ready';
import { WIDTHS, PHONE } from './widths';

test.describe('app shell', () => {
  for (const width of WIDTHS) {
    test(`no horizontal page overflow at ${width}px @quick`, async ({ page, api }) => {
      await page.setViewportSize({ width, height: 900 });
      await gotoRoute(page, api, '/');
      await expectNoPageOverflow(page, `home @ ${width}`);
    });
  }

  test('short viewport keeps the topbar and main content usable', async ({ page, api }) => {
    await page.setViewportSize({ width: 390, height: 420 });
    await gotoRoute(page, api, '/');
    await expectNoPageOverflow(page, 'home @ 390x420');
    await expect(page.getByRole('button', { name: 'Ana menüyü aç' })).toBeVisible();
  });

  test('mobile drawer traps focus in both directions @quick', async ({ page, api }) => {
    await page.setViewportSize(PHONE);
    await gotoRoute(page, api, '/');

    const trigger = page.getByRole('button', { name: 'Ana menüyü aç' });
    await expect(trigger).toHaveAttribute('aria-expanded', 'false');
    await expect(trigger).toHaveAttribute('aria-controls', 'app-sidebar');

    const sidebar = page.locator('#app-sidebar');
    await expect(sidebar).toHaveAttribute('inert', '');

    await trigger.click();
    await expect(trigger).toHaveAttribute('aria-expanded', 'true');
    await expect(sidebar).not.toHaveAttribute('inert', '');

    const focusInside = () =>
      page.evaluate(() => !!document.activeElement?.closest('#app-sidebar'));
    const focusLabel = () =>
      page.evaluate(() => {
        const el = document.activeElement as HTMLElement | null;
        if (!el) return 'none';
        return `${el.tagName.toLowerCase()}:${(el.getAttribute('aria-label') || el.textContent || '').trim().slice(0, 30)}`;
      });

    await expect.poll(focusInside).toBe(true);
    await expect
      .poll(() => page.evaluate(() => getComputedStyle(document.body).overflow))
      .toBe('hidden');

    // Checked at every step, not only after 25 presses. A trap that leaks on
    // one press and is pulled back by the next passed the old assertion.
    const forwardTrail: string[] = [];
    for (let i = 0; i < 30; i++) {
      await page.keyboard.press('Tab');
      forwardTrail.push(await focusLabel());
      expect(
        await focusInside(),
        `focus left the drawer on Tab #${i + 1}; trail: ${forwardTrail.join(' → ')}`
      ).toBe(true);
    }

    // And backwards: wrapping past the first element is the half that usually
    // escapes, because it is the one nobody tries by hand.
    const backTrail: string[] = [];
    for (let i = 0; i < 30; i++) {
      await page.keyboard.press('Shift+Tab');
      backTrail.push(await focusLabel());
      expect(
        await focusInside(),
        `focus left the drawer on Shift+Tab #${i + 1}; trail: ${backTrail.join(' → ')}`
      ).toBe(true);
    }

    await page.keyboard.press('Escape');
    await expect(trigger).toHaveAttribute('aria-expanded', 'false');
    await expect(trigger).toBeFocused();
    await expect(sidebar).toHaveAttribute('inert', '');
    await expect
      .poll(() => page.evaluate(() => getComputedStyle(document.body).overflow))
      .not.toBe('hidden');
  });

  test('drawer closes when a navigation happens', async ({ page, api }) => {
    await page.setViewportSize(PHONE);
    await gotoRoute(page, api, '/');
    await page.getByRole('button', { name: 'Ana menüyü aç' }).click();
    const sidebar = page.locator('#app-sidebar');
    await expect(sidebar).not.toHaveAttribute('inert', '');
    await sidebar.getByRole('button', { name: 'Stok' }).click();
    await sidebar.getByRole('link', { name: 'Stok Kartları' }).click();
    await page.waitForURL('**/stok/urunler');
    await expect(sidebar).toHaveAttribute('inert', '');
    // Focus must come back somewhere usable rather than being left on a node
    // that has just been made inert.
    await expect
      .poll(() => page.evaluate(() => !!document.activeElement?.closest('#app-sidebar')))
      .toBe(false);
  });

  test('growing back to desktop leaves no stale drawer state', async ({ page, api }) => {
    await page.setViewportSize(PHONE);
    await gotoRoute(page, api, '/');
    await page.getByRole('button', { name: 'Ana menüyü aç' }).click();
    await page.setViewportSize({ width: 1440, height: 900 });
    const sidebar = page.locator('#app-sidebar');
    await expect(sidebar).toBeVisible();
    await expect(sidebar).not.toHaveAttribute('inert', '');
    await expect
      .poll(() => page.evaluate(() => getComputedStyle(document.body).overflow))
      .not.toBe('hidden');
    await expectNoPageOverflow(page, 'home @ 1440 after drawer');
  });

  test('calculator and calendar stay reachable on a phone', async ({ page, api }) => {
    await page.setViewportSize(PHONE);
    await gotoRoute(page, api, '/');

    await expect(page.getByRole('button', { name: 'Hesap makinesi' })).toHaveCount(0);
    const tools = page.getByRole('button', { name: /^Araçlar/ });
    await expect(tools).toBeVisible();

    await tools.click();
    await page.getByRole('menuitem', { name: 'Hesap makinesi' }).click();
    const calculator = page.getByRole('dialog', { name: 'Hesap makinesi' });
    await expect(calculator).toBeVisible();
    await expectNoPageOverflow(page, 'calculator sheet @ 390');
    await page.keyboard.press('Escape');
    await expect(calculator).toBeHidden();

    await tools.click();
    await page.getByRole('menuitem', { name: 'Takvim' }).click();
    const calendar = page.getByRole('dialog', { name: 'Takvim' });
    await expect(calendar).toBeVisible();
    await expectNoPageOverflow(page, 'calendar sheet @ 390');
    await page.keyboard.press('Escape');
    await expect(calendar).toBeHidden();
  });

  test('desktop keeps the inline tools and the docked sidebar', async ({ page, api }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    await gotoRoute(page, api, '/');
    await expect(page.getByRole('button', { name: 'Hesap makinesi' })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Takvim' })).toBeVisible();
    await expect(page.getByRole('button', { name: /^Araçlar/ })).toHaveCount(0);
    await expect(page.getByRole('button', { name: 'Ana menüyü aç' })).toBeHidden();
  });
});
