import type { Page, Request, Response } from '@playwright/test';

const API_PREFIX = '/api/v1/';

export type ApiFailure = {
  method: string;
  path: string;
  /** HTTP status, or 0 when the request never produced a response at all. */
  status: number;
  body: string;
  /** Set when the request failed at the network layer instead of answering. */
  networkError?: string;
};

/**
 * A failure a test knowingly accepts.
 *
 * The status is part of the declaration. Matching on the path alone let a 500
 * from an endpoint pass because a 404 from the same endpoint was expected —
 * "this endpoint may answer no" and "this endpoint may crash" are different
 * statements, and only the first is ever true.
 */
export type AllowedFailure = { path: RegExp; status: number | number[] | 'client-error' };

/**
 * The widest an allowance may ever be: the 4xx range.
 *
 * A designed "no" — not found, not permitted, not signed in — is a 4xx. A 5xx
 * is the server failing, and no test has a reason to expect one from a path it
 * calls on purpose. Matching on the path alone allowed both, so a 500 from an
 * endpoint slipped through because a 404 from the same endpoint was expected.
 */
function matchesAllowance(failure: ApiFailure, allowed: AllowedFailure) {
  if (!allowed.path.test(failure.path)) return false;
  // A request that never got an answer is never an expected failure.
  if (failure.status === 0) return false;
  if (allowed.status === 'client-error') return failure.status >= 400 && failure.status < 500;
  const statuses = Array.isArray(allowed.status) ? allowed.status : [allowed.status];
  return statuses.includes(failure.status);
}

export type WriteRequest = {
  method: string;
  path: string;
};

/**
 * The one failure every page produces on a normal installation.
 *
 * The shell asks once per load whether this deployment is the public demo. On
 * anything else the answer is a 404 — a designed "no", not a fault. It is
 * listed here rather than in each spec so the per-test allow lists stay short
 * enough to read, which is the point of not having a blanket ignore list.
 */
const BASELINE_ALLOWED: AllowedFailure[] = [{ path: /^\/demo\/state$/, status: 404 }];

/** Requests the app makes on its own that are allowed to be non-GET. Anything
 * else writing during a read-only test is a finding, not background noise. */
const EXPECTED_WRITES: RegExp[] = [
  /^\/auth\/login$/,
  /^\/auth\/logout$/,
  /^\/session\/csrf$/,
  /^\/session\/company$/
];

/**
 * Watches everything the page does to the API.
 *
 * It replaces two habits the suite used to rely on: `networkidle` (which waits
 * for unrelated traffic and hides failures behind a swallowed timeout) and a
 * fixed sleep. Because it only counts `/api/v1/*`, a slow font, an open HMR
 * socket or a five-minute poll can never make it wait or hang.
 */
export class ApiMonitor {
  readonly failures: ApiFailure[] = [];
  readonly writes: WriteRequest[] = [];
  /** Paths a single test knowingly accepts a failure from, e.g. an endpoint
   * the signed-in role is not allowed to call. Kept narrow on purpose: there
   * is no blanket ignore list. */
  readonly allowedFailures: AllowedFailure[] = [...BASELINE_ALLOWED];
  /** Bodies still being read. Checked before the verdict; see settle(). */
  readonly #pendingBodies: Promise<unknown>[] = [];
  /** Paths this test is allowed to write to; see allowWrites. */
  readonly allowedWrites: RegExp[] = [];

  // Tracked by identity rather than by count. A counter drifts: a request the
  // browser drops when a navigation replaces the document fires no
  // `requestfinished`, and the missing decrement then makes every later wait
  // time out on traffic that ended long ago.
  #inFlight = new Set<Request>();
  #settledAt = Date.now();

  constructor(private readonly page: Page) {
    page.on('request', this.#onRequest);
    page.on('requestfinished', this.#onSettled);
    page.on('requestfailed', this.#onRequestFailed);
    page.on('response', this.#onResponse);
    page.on('framenavigated', this.#onNavigated);
  }

  dispose() {
    this.page.off('request', this.#onRequest);
    this.page.off('requestfinished', this.#onSettled);
    this.page.off('requestfailed', this.#onRequestFailed);
    this.page.off('response', this.#onResponse);
    this.page.off('framenavigated', this.#onNavigated);
  }

  /**
   * Accept a failure from these paths for the rest of the test.
   *
   * The status defaults to the 4xx range: an endpoint that answers "no" is
   * something a test may expect. Nothing widens it to 5xx, so a server error
   * from an endpoint a test knowingly calls is still a failure — which is what
   * matching on the path alone used to wave through.
   */
  allow(...patterns: (RegExp | AllowedFailure)[]) {
    for (const pattern of patterns) {
      this.allowedFailures.push(
        pattern instanceof RegExp ? { path: pattern, status: 'client-error' } : pattern
      );
    }
  }

  /**
   * Declare the writes this test means to make.
   *
   * Read-only tests declare none, and any non-GET request they provoke fails
   * them. Blocking every POST outright would be the wrong tool: sign-in is a
   * POST, and so is the company switch — what matters is that a test that only
   * looks at a screen does not quietly change the data underneath the next one.
   */
  allowWrites(...patterns: RegExp[]) {
    this.allowedWrites.push(...patterns);
  }

  get unexpectedFailures() {
    return this.failures.filter(
      (failure) => !this.allowedFailures.some((allowed) => matchesAllowance(failure, allowed))
    );
  }

  /**
   * Wait for every failure to be fully recorded before the verdict is read.
   *
   * A response arrives before its body does, and the body is read
   * asynchronously. Without this, a test that checked `unexpectedFailures` the
   * moment the page went quiet could read the list before a slow 500 had been
   * added to it — and pass.
   */
  async settle(timeout = 5_000) {
    // Bounded. A body whose document was replaced mid-read never resolves, and
    // an unbounded wait here turns a recorded failure into a teardown that
    // hangs until the test times out — which reports the wrong problem and
    // loses the failure it was waiting to describe.
    await Promise.race([
      Promise.allSettled(this.#pendingBodies),
      new Promise((resolve) => setTimeout(resolve, timeout))
    ]);
  }

  get unexpectedWrites() {
    return this.writes.filter(
      (w) =>
        !EXPECTED_WRITES.some((p) => p.test(w.path)) &&
        !this.allowedWrites.some((p) => p.test(w.path))
    );
  }

  /**
   * Resolves once no API request has been in flight for `quietFor` ms.
   *
   * Throws rather than swallowing: a route that never stops calling the API is
   * a defect in the page, and a test that waited for it and gave up silently is
   * exactly the false pass this suite is being cleaned up to remove.
   */
  async waitForIdle(timeout = 15_000, quietFor = 120) {
    const deadline = Date.now() + timeout;
    for (;;) {
      if (this.#inFlight.size === 0 && Date.now() - this.#settledAt >= quietFor) return;
      if (Date.now() > deadline) {
        const pending = [...this.#inFlight].map((r) => `${r.method()} ${this.#path(r.url())}`);
        throw new Error(
          `API traffic never settled within ${timeout}ms; still in flight: ${pending.join(', ')}`
        );
      }
      await this.page.waitForTimeout(40);
    }
  }

  #path(url: string) {
    const index = url.indexOf(API_PREFIX);
    return index === -1 ? '' : url.slice(index + API_PREFIX.length - 1);
  }

  #onRequest = (request: Request) => {
    if (!request.url().includes(API_PREFIX)) return;
    this.#inFlight.add(request);
    const method = request.method();
    if (method !== 'GET' && method !== 'HEAD') {
      this.writes.push({ method, path: this.#path(request.url()) });
    }
  };

  #onSettled = (request: Request) => {
    if (!this.#inFlight.delete(request)) return;
    this.#settledAt = Date.now();
  };

  /**
   * A request that never produced a response.
   *
   * Most of these are the browser discarding in-flight work because a
   * navigation replaced the document — normal, and not the page's fault. A
   * connection that was refused or reset is not: the API went away mid-test,
   * and a suite that only decremented a counter here would report that as a
   * quiet, healthy page.
   */
  #onRequestFailed = (request: Request) => {
    const wasTracked = this.#inFlight.has(request);
    this.#onSettled(request);
    if (!wasTracked) return;
    const failure = request.failure()?.errorText ?? '';
    // Chromium's words for "the document went away underneath this request".
    if (/ERR_ABORTED|net::ERR_ABORTED|NS_BINDING_ABORTED|context was destroyed/i.test(failure)) {
      return;
    }
    this.failures.push({
      method: request.method(),
      path: this.#path(request.url()),
      status: 0,
      body: '',
      networkError: failure || 'request failed with no response'
    });
  };

  // A full page load discards whatever the previous document had open.
  #onNavigated = (frame: { parentFrame(): unknown }) => {
    if (frame.parentFrame()) return;
    this.#inFlight.clear();
    this.#settledAt = Date.now();
  };

  #onResponse = (response: Response) => {
    const url = response.url();
    if (!url.includes(API_PREFIX)) return;
    // A response is the server's answer; whether the body has finished
    // streaming is not something a test should wait on.
    this.#onSettled(response.request());
    const status = response.status();
    if (status < 400) return;
    const path = this.#path(url);
    // Recorded now, not when the body arrives. The failure is already a fact;
    // the body is only the explanation, and waiting for it to exist before
    // admitting the failure is what let a slow 500 slip past the verdict.
    const failure: ApiFailure = {
      method: response.request().method(),
      path,
      status,
      body: ''
    };
    this.failures.push(failure);
    this.#pendingBodies.push(
      response
        .text()
        .then((body) => {
          failure.body = body.slice(0, 400);
        })
        .catch(() => {
          failure.body = '(gövde okunamadı)';
        })
    );
  };
}

/** Uncaught exceptions and console errors, collected for the same reason. */
export class PageErrorMonitor {
  readonly errors: string[] = [];
  readonly allowed: RegExp[] = [];

  constructor(private readonly page: Page) {
    page.on('pageerror', this.#onPageError);
    page.on('console', this.#onConsole);
  }

  dispose() {
    this.page.off('pageerror', this.#onPageError);
    this.page.off('console', this.#onConsole);
  }

  allow(...patterns: RegExp[]) {
    this.allowed.push(...patterns);
  }

  get unexpected() {
    return this.errors.filter((message) => !this.allowed.some((p) => p.test(message)));
  }

  #onPageError = (error: Error) => {
    this.errors.push(`pageerror: ${error.message}`);
  };

  #onConsole = (message: { type(): string; text(): string }) => {
    if (message.type() !== 'error') return;
    const text = message.text();
    // A failed fetch logs here as well as in the API monitor; reporting it
    // twice makes the real cause harder to read, not easier.
    if (/Failed to load resource/i.test(text)) return;
    this.errors.push(`console: ${text}`);
  };
}
