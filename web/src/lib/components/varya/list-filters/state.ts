import type { Session } from '$lib/api';

type Scope = Pick<Session, 'current_company_id' | 'user'>;
function key(scope: Scope, screen: string) {
  return `varya:list:v2:${scope.user.id}:${scope.current_company_id}:${screen}`;
}
export function readListState<T>(scope: Scope, screen: string): T | undefined {
  try {
    const value = sessionStorage.getItem(key(scope, screen));
    return value ? (JSON.parse(value) as T) : undefined;
  } catch {
    return undefined;
  }
}
export function writeListState(scope: Scope, screen: string, state: unknown) {
  try {
    sessionStorage.setItem(key(scope, screen), JSON.stringify(state));
  } catch {
    /* Browsers may disable storage; filtering still works. */
  }
}
