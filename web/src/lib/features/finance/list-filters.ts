import type { OperationFilter } from '$lib/features/operations/OperationListPage.svelte';
import { api } from '$lib/api';
import { listParties } from '$lib/features/parties/api';

/** Shared "Cari" entity filter for the tahsilat/ödeme list grids. */
export function financePartyFilter(): OperationFilter {
  return {
    field: 'party_id',
    label: 'Cari',
    kind: 'entity',
    entity: {
      title: 'Cari seç',
      description: 'Filtrelemek istediğiniz cariyi kodu veya adıyla arayın.',
      triggerPlaceholder: 'Cari seçin',
      searchPlaceholder: 'Cari kodu, unvan veya ad ara',
      search: async (query: string, signal?: AbortSignal) => {
        const params = new URLSearchParams({ q: query, limit: '50' });
        const result = await listParties(params, signal);
        return result.items.map((party) => ({
          id: party.id,
          title: party.display_name,
          subtitle: party.code,
          meta: [party.phone, party.email].filter(Boolean).join(' · ')
        }));
      }
    }
  };
}

type FinanceAccount = {
  id: string;
  code: string;
  name: string;
  account_type: string;
  currency: string;
};

/** Shared "Hesap" (kasa/banka) entity filter for the tahsilat/ödeme list grids. */
export function financeAccountFilter(): OperationFilter {
  return {
    field: 'account_id',
    label: 'Hesap',
    kind: 'entity',
    entity: {
      title: 'Kasa / Banka hesabı seç',
      description: 'Filtrelemek istediğiniz kasa veya banka hesabını arayın.',
      triggerPlaceholder: 'Hesap seçin',
      searchPlaceholder: 'Hesap kodu veya adı ara',
      search: async (query: string, signal?: AbortSignal) => {
        const result = await api<{ items: FinanceAccount[] }>('/finance/accounts?limit=100', {
          signal
        });
        const term = query.trim().toLocaleLowerCase('tr');
        return result.items
          .filter(
            (account) =>
              !term ||
              account.code.toLocaleLowerCase('tr').includes(term) ||
              account.name.toLocaleLowerCase('tr').includes(term)
          )
          .map((account) => ({
            id: account.id,
            title: account.name,
            subtitle: account.code,
            meta: `${account.account_type === 'BANK' ? 'Banka' : 'Kasa'} · ${account.currency}`
          }));
      }
    }
  };
}
