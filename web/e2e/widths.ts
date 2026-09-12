/** Widths the suite measures at.
 *
 * The full ladder runs against the shell, where a regression is a CSS problem
 * rather than a per-page one; every route is checked at the three starred
 * widths: a small phone, a tablet, and a laptop. */
export const WIDTHS = [320, 360, 390, 430, 640, 768, 820, 980, 1024, 1280, 1440, 1920] as const;
export const ROUTE_WIDTHS = [390, 768, 1440] as const;
export const PHONE = { width: 390, height: 844 } as const;
