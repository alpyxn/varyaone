import { test as setup, expect } from './fixtures/test';
import { AUTH_STATE, E2E_COMPANY, E2E_EMAIL, E2E_PASSWORD } from './fixtures/env';
import { waitForAppShell, waitForBoot } from './fixtures/ready';

/**
 * Signs in once and saves the browser state every other project reuses.
 *
 * This is also the suite's login test. It runs the real form rather than
 * posting to the API, and it checks what the old helper did not: that the
 * session landed in the app shell, with the expected company selected. A
 * redirect to the setup wizard, to "add a company", or a shell drawn with no
 * session all used to satisfy "the URL is no longer /giris".
 */
setup('sign in and store the session', async ({ page, api }) => {
  // Signed out, the shell's own session probe answers 401. That is the state
  // the login screen exists for, so it is expected here and nowhere else — and
  // only that status: a 500 from the same path is still a failure.
  api.allow({ path: /^\/session$/, status: 401 });

  await page.goto('/giris');
  await waitForBoot(page);

  const form = page.getByRole('form', { name: 'Giriş formu' });
  await expect(form).toBeVisible();
  await page.getByLabel('E-posta').fill(E2E_EMAIL);
  await page.getByLabel('Parola').fill(E2E_PASSWORD);
  await page.getByRole('button', { name: /giriş yap/i }).click();

  await page.waitForURL((url) => url.pathname === '/', { timeout: 30_000 });
  await waitForAppShell(page);
  await api.waitForIdle();

  // The session points at the seeded company, not merely at *a* session.
  await expect(page.getByRole('button', { name: 'Aktif şirket' })).toContainText(E2E_COMPANY);

  await page.context().storageState({ path: AUTH_STATE });
});
