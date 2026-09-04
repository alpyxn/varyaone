import { api, type Company } from '$lib/api';

/**
 * The company records carried on `/session` are the switcher's list: identity
 * and currency only, deliberately without the logo, because a data URI logo is
 * up to 500 KB and the session is read on nearly every page. Printed documents
 * need the logo and the tax number, so they read the full profile from
 * `/company` at print time instead.
 *
 * The profile is fetched once per page load and reused, and a failure (a user
 * without `organization.company.read`, an offline moment) resolves to
 * `undefined` so printing still produces the document, only without the mark.
 */
let pending: Promise<Company | undefined> | undefined;

export function printableCompany(): Promise<Company | undefined> {
  pending ??= api<Company>('/company').catch(() => undefined);
  return pending;
}

/** Drop the cached profile so the next print re-reads it (after an edit). */
export function resetPrintableCompany(): void {
  pending = undefined;
}
