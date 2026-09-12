/** The environment the suite drives. Every value has a default that matches a
 * locally seeded stack, and CI overrides them for its own throwaway one. */
export const E2E_EMAIL = process.env.VARYAONE_E2E_EMAIL ?? 'demo@varyaone.com';
export const E2E_PASSWORD = process.env.VARYAONE_E2E_PASSWORD ?? 'varyaone-demo-2026';
/** The seeded company's trade name, asserted after sign-in so a session
 * pointing at the wrong (or no) company cannot pass as a successful login. */
export const E2E_COMPANY = process.env.VARYAONE_E2E_COMPANY ?? 'Varya Demo';
/** Where the signed-in browser state is cached between projects. */
export const AUTH_STATE = 'e2e/.auth/user.json';
