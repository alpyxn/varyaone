import { defineConfig, devices } from '@playwright/test';
import { AUTH_STATE } from './e2e/fixtures/env';

/**
 * End-to-end suite.
 *
 * It drives a full stack of its own — PostgreSQL, the API and the frontend,
 * all seeded from scratch — rather than a shared demo installation. Tests both
 * read and write, so the stack must be disposable; see e2e/README.md.
 *
 * VARYAONE_E2E_BASE_URL points it at an already running frontend. Without it,
 * a dev server is started for local work only; CI always serves the production
 * build, because that is what ships.
 */
const baseURL = process.env.VARYAONE_E2E_BASE_URL ?? 'http://127.0.0.1:5199';
const apiURL = process.env.VARYAONE_E2E_API_URL ?? 'http://127.0.0.1:18081';

/**
 * Which layer to run: `quick` is the `@quick`-tagged subset — sign-in, the
 * shell, the busiest forms and the critical flows — and `full` is everything
 * else. CI runs them as two reported steps so the first answer arrives long
 * before the whole route sweep finishes.
 *
 * The filter is applied per project rather than globally on purpose. A global
 * `--grep` also filters the `setup` project, and a run whose sign-in step was
 * filtered out starts every other test with no session.
 */
const layer = process.env.VARYAONE_E2E_LAYER;
const layerFilter =
  layer === 'quick' ? { grep: /@quick/ } : layer === 'full' ? { grepInvert: /@quick/ } : {};

/**
 * Each layer reports into its own directory.
 *
 * Both used to write to `playwright-report`, so the second run replaced the
 * first one's HTML, JSON and traces. When the quick layer was the one that
 * failed, the evidence explaining it was gone by the time anybody downloaded
 * the artifact — and the run that overwrote it was the one that had passed.
 */
const reportDir = layer ? `playwright-report/${layer}` : 'playwright-report';
const outputDir = layer ? `test-results/${layer}` : 'test-results';

export default defineConfig({
  testDir: 'e2e',
  timeout: 60_000,
  expect: { timeout: 15_000 },
  fullyParallel: true,
  // Measured, not guessed: see e2e/README.md. Raise it only against a number.
  workers: process.env.VARYAONE_E2E_WORKERS ? Number(process.env.VARYAONE_E2E_WORKERS) : 2,
  // A retry that turns red into green hides exactly the flakiness this suite
  // exists to remove. Failures are diagnosed from the trace, not re-rolled.
  retries: 0,
  forbidOnly: !!process.env.CI,
  outputDir,
  reporter: process.env.CI
    ? [
        ['list'],
        ['html', { open: 'never', outputFolder: reportDir }],
        ['json', { outputFile: `${reportDir}/results.json` }]
      ]
    : [['list']],
  use: {
    baseURL,
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure'
  },
  projects: [
    { name: 'setup', testMatch: /auth\.setup\.ts/ },
    {
      name: 'chromium',
      ...layerFilter,
      dependencies: ['setup'],
      testIgnore: /(auth\.setup|touch\.spec)\.ts/,
      use: { ...devices['Desktop Chrome'], storageState: AUTH_STATE }
    },
    // Touch emulation flips `pointer: coarse`, which the 44px touch-target
    // rules key off; a desktop browser narrowed to 390px does not. Only the
    // tests that depend on it are repeated here — running whole workflows
    // twice buys nothing and doubles the bill.
    {
      name: 'touch',
      ...layerFilter,
      dependencies: ['setup'],
      testMatch: /touch\.spec\.ts/,
      use: {
        ...devices['Pixel 7'],
        viewport: { width: 390, height: 844 },
        storageState: AUTH_STATE
      }
    }
  ],
  webServer: process.env.VARYAONE_E2E_BASE_URL
    ? undefined
    : {
        command: 'npm run dev -- --port 5199 --host 127.0.0.1',
        url: `${baseURL}/api/health/ready`,
        // Reusing a server locally is a convenience. In CI it would let a stale
        // process from another job answer the tests, so it is never allowed.
        reuseExistingServer: !process.env.CI,
        timeout: 120_000,
        env: { VARYAONE_API_INTERNAL_URL: apiURL }
      }
});
