/**
 * Bridge to the Varya One Windows desktop client (cmd/varyaone-client).
 *
 * The client injects its host functions into every page it shows, before the
 * page's own scripts run. A normal browser has none of them, so their presence
 * is what tells the two apart — the same server serves both.
 */

export type ZoomState = {
  /** 1 = %100 */
  factor: number;
  min: number;
  max: number;
  default: number;
};

type DesktopHost = {
  hostGetZoom?: () => Promise<ZoomState>;
  hostSetZoom?: (factor: number) => Promise<ZoomState>;
};

function host(): DesktopHost | undefined {
  return typeof window === 'undefined' ? undefined : (window as unknown as DesktopHost);
}

/** True inside a desktop client new enough to offer the program settings. */
export function isDesktopClient(): boolean {
  return typeof host()?.hostGetZoom === 'function';
}

export const desktopZoom = {
  async get(): Promise<ZoomState> {
    const fn = host()?.hostGetZoom;
    if (!fn) throw new Error('Masaüstü uygulaması bulunamadı.');
    return fn();
  },
  async set(factor: number): Promise<ZoomState> {
    const fn = host()?.hostSetZoom;
    if (!fn) throw new Error('Masaüstü uygulaması bulunamadı.');
    return fn(factor);
  }
};
