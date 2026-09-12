import { describe, expect, it } from 'vitest';
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join, relative, sep } from 'node:path';
import { STATIC_ROUTES, PUBLIC_ROUTES, EXCLUDED_ROUTES } from '../../e2e/routes';
import { DETAIL_SCENARIOS, PENDING_DETAIL_ROUTES } from '../../e2e/detail-scenarios';
import { commercialResource } from './features/commercial/types';
import { DEFAULT_REPORT_ID, REPORTS } from './features/reporting/registry';

/**
 * The browser suite's route inventory against the routes that exist.
 *
 * This runs with the unit tests rather than in the browser, so a page added
 * without a matching entry fails in seconds instead of being quietly left
 * untested — which is how six "yeni" screens and the six kasa/banka aliases
 * came to sit outside the suite.
 */
const ROUTES_DIR = join(process.cwd(), 'src', 'routes');

function pageDirectories(dir: string, found: string[] = []): string[] {
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) {
      pageDirectories(full, found);
    } else if (entry === '+page.svelte' || entry === '+page.ts') {
      const route = '/' + relative(ROUTES_DIR, dir).split(sep).join('/');
      const normalized = route === '/.' || route === '/' ? '/' : route.replace(/\/$/, '');
      if (!found.includes(normalized)) found.push(normalized);
    }
  }
  return found;
}

/** A route whose only `+page.ts` redirects elsewhere, and where it goes. */
function redirectTarget(route: string): string | undefined {
  const file = join(ROUTES_DIR, route === '/' ? '' : route, '+page.ts');
  let source: string;
  try {
    source = readFileSync(file, 'utf8');
  } catch {
    return undefined;
  }
  const match = source.match(/redirect\(\s*30[178]\s*,\s*[`'"]([^`'"]*)/);
  if (!match) return undefined;
  // The reports index interpolates the registry's default id.
  return match[1].replace('${DEFAULT_REPORT_ID}', DEFAULT_REPORT_ID);
}

/**
 * How a route is accounted for. Exactly one of these applies to every page in
 * `src/routes`; "several ways it might be fine" is what let record pages go
 * untested while the inventory stayed green.
 */
type Accounting =
  | { kind: 'static' }
  | { kind: 'public' }
  | { kind: 'excluded'; reason: string }
  | { kind: 'expansion'; into: string[] }
  | { kind: 'detail'; list: string }
  | { kind: 'pending'; reason: string }
  | { kind: 'unaccounted' };

const staticPaths = new Set(STATIC_ROUTES.map((r) => r.path));
const publicPaths = new Set(PUBLIC_ROUTES.map((r) => r.path));
const detailByRoute = new Map(DETAIL_SCENARIOS.map((s) => [s.route, s.list]));

/** The concrete paths a parameterised directory stands for, if any. */
function expansionOf(route: string): string[] {
  if (!route.includes('[resource]') && !route.includes('[report]')) return [];
  const pattern = new RegExp('^' + route.replace(/\[[^\]]+\]/g, '[^/]+') + '$');
  return [...staticPaths].filter((path) => pattern.test(path));
}

function account(route: string): Accounting {
  if (staticPaths.has(route)) return { kind: 'static' };
  if (publicPaths.has(route)) return { kind: 'public' };
  if (route in EXCLUDED_ROUTES) return { kind: 'excluded', reason: EXCLUDED_ROUTES[route] };
  const list = detailByRoute.get(route);
  if (list) return { kind: 'detail', list };
  if (route in PENDING_DETAIL_ROUTES) {
    return { kind: 'pending', reason: PENDING_DETAIL_ROUTES[route] };
  }
  const into = expansionOf(route);
  if (into.length > 0) return { kind: 'expansion', into };
  return { kind: 'unaccounted' };
}

describe('the browser suite covers every route', () => {
  it('leaves no page undeclared', () => {
    // No route is accepted because of the shape of its name. A `[id]` page is
    // covered when a scenario opens it and pending when one does not; both are
    // written down, and anything else is a gap.
    const missing = pageDirectories(ROUTES_DIR).filter(
      (route) => account(route).kind === 'unaccounted'
    );
    expect(
      missing,
      'add these to e2e/routes.ts, to EXCLUDED_ROUTES with the reason, or — for a ' +
        'record page — to DETAIL_SCENARIOS with the list it opens from, or to ' +
        'PENDING_DETAIL_ROUTES with what fixture it is waiting for'
    ).toEqual([]);
  });

  it('counts a new record page as a gap, not as covered', () => {
    // The regression this file exists for: the old inventory accepted any path
    // containing `[id]`, so adding a record page with no scenario changed
    // nothing. This is that page.
    expect(account('/uydurma/[id]')).toEqual({ kind: 'unaccounted' });
    expect(account('/uydurma/[resource]')).toEqual({ kind: 'unaccounted' });
    expect(account('/uydurma/[report]')).toEqual({ kind: 'unaccounted' });
    // And an inner record page is its own case, not covered by its parent.
    expect(account('/cari/kartlar/[id]').kind).toBe('detail');
    expect(account('/cari/kartlar/[id]/ekstre').kind).toBe('pending');
  });

  it('declares no scenario for a record page that does not exist', () => {
    const actual = new Set(pageDirectories(ROUTES_DIR));
    const stale = [...detailByRoute.keys(), ...Object.keys(PENDING_DETAIL_ROUTES)].filter(
      (route) => !actual.has(route)
    );
    expect(stale, 'these record routes are declared but have no page').toEqual([]);
  });

  it('opens every record page it claims to cover from a real list', () => {
    // Being written down is not coverage. Each covered record route must name
    // a list page that exists and is itself visited by the suite.
    const unreachable = DETAIL_SCENARIOS.filter((s) => !staticPaths.has(s.list));
    expect(
      unreachable.map((s) => `${s.route} ← ${s.list}`),
      'these scenarios start from a list the suite does not visit'
    ).toEqual([]);
  });

  it('reports the real state of every route', () => {
    // The summary the acceptance criterion asks for: what works, what is a
    // recorded exception, and what is waiting for a fixture — never one number
    // covering all three.
    const tally = { static: 0, public: 0, detail: 0, expansion: 0, excluded: 0, pending: 0 };
    const pending: string[] = [];
    for (const route of pageDirectories(ROUTES_DIR)) {
      const entry = account(route);
      if (entry.kind === 'unaccounted') continue;
      tally[entry.kind] += 1;
      if (entry.kind === 'pending') pending.push(route);
    }
    // Pending entries are gaps with names. They are not allowed to be silent,
    // and they are not allowed to grow without someone writing the reason.
    expect(pending.sort()).toEqual(Object.keys(PENDING_DETAIL_ROUTES).sort());
    expect(tally.detail).toBe(new Set(DETAIL_SCENARIOS.map((s) => s.route)).size);
  });

  it('declares no route that no longer exists', () => {
    const actual = pageDirectories(ROUTES_DIR);
    // A declared path may be the expansion of a parameterised directory, so
    // match against both the literal routes and the patterns they stand for.
    const patterns = actual
      .filter((route) => route.includes('['))
      .map((route) => new RegExp('^' + route.replace(/\[[^\]]+\]/g, '[^/]+') + '$'));
    const literal = new Set(actual);
    const stale = [...STATIC_ROUTES, ...PUBLIC_ROUTES]
      .map((r) => r.path)
      .filter((route) => !literal.has(route) && !patterns.some((p) => p.test(route)));
    expect(stale, 'these are in e2e/routes.ts but have no page').toEqual([]);
  });

  it('expands every sales and purchasing resource', () => {
    const salesSlugs = ['teklifler', 'siparisler', 'irsaliyeler', 'faturalar', 'iadeler'];
    // Purchasing has no quote screen; the config refuses that combination.
    const purchaseSlugs = salesSlugs.filter((slug) => slug !== 'teklifler');
    const paths = new Set(STATIC_ROUTES.map((r) => r.path));
    for (const slug of salesSlugs) {
      expect(commercialResource(slug), `${slug} is not a commercial resource`).toBeTruthy();
      expect(paths.has(`/satis/${slug}`), `/satis/${slug} is not covered`).toBe(true);
      expect(paths.has(`/satis/${slug}/yeni`), `/satis/${slug}/yeni is not covered`).toBe(true);
    }
    for (const slug of purchaseSlugs) {
      expect(paths.has(`/alis/${slug}`), `/alis/${slug} is not covered`).toBe(true);
      expect(paths.has(`/alis/${slug}/yeni`), `/alis/${slug}/yeni is not covered`).toBe(true);
    }
  });

  it('expands every registered report', () => {
    const paths = new Set(STATIC_ROUTES.map((r) => r.path));
    for (const report of REPORTS) {
      expect(paths.has(`/raporlar/${report.id}`), `${report.id} is not covered`).toBe(true);
    }
  });

  it('records where each redirect actually lands', () => {
    for (const spec of STATIC_ROUTES) {
      const target = redirectTarget(spec.path);
      if (!target) continue;
      expect(spec.lands, `${spec.path} redirects to ${target} but routes.ts does not say so`).toBe(
        target
      );
    }
  });
});
