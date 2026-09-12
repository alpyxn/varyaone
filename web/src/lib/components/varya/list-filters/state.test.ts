import { beforeEach, expect, it } from 'vitest';
import { readListState, writeListState } from './state';
const scope = {
  user: { id: 'user-1', email: 'test@example.test', display_name: 'Test', totp_enabled: false },
  current_company_id: 'company-1'
};
beforeEach(() => sessionStorage.clear());
it('restores filters for the same screen without sharing them across users or companies', () => {
  const state = {
    search: 'Eski fatura',
    filters: { currency: 'EUR' },
    cursorHistory: ['', 'cursor-1']
  };
  writeListState(scope, 'plans', state);
  expect(readListState(scope, 'plans')).toEqual(state);
  expect(readListState({ ...scope, current_company_id: 'company-2' }, 'plans')).toBeUndefined();
  expect(
    readListState({ ...scope, user: { ...scope.user, id: 'user-2' } }, 'plans')
  ).toBeUndefined();
  expect(readListState(scope, 'payments')).toBeUndefined();
});
