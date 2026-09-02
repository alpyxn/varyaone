<script lang="ts">
  import { page } from '$app/state';
  import OperationListPage, {
    type OperationColumn,
    type OperationFilter
  } from '$lib/features/operations/OperationListPage.svelte';
  import { listParties } from '$lib/features/parties/api';
  const columns: OperationColumn[] = [
    { id: 'document_date', label: 'Tarih', sortable: true },
    { id: 'party_name', label: 'Cari', sortable: true },
    { id: 'source_type', label: 'İşlem Türü' },
    { id: 'document_no', label: 'Belge No' },
    { id: 'debit', label: 'Borç', align: 'right' },
    { id: 'credit', label: 'Alacak', align: 'right' },
    { id: 'currency', label: 'Para' }
  ];
  const filters: OperationFilter[] = [
    { field: 'from', label: 'Başlangıç', kind: 'date' },
    { field: 'to', label: 'Bitiş', kind: 'date' },
    {
      field: 'party_id',
      label: 'Cari',
      kind: 'entity',
      entity: {
        title: 'Cari seç',
        description: 'Ekstresini görmek istediğiniz cariyi kodu veya adıyla arayın.',
        triggerPlaceholder: 'Cari seçin',
        searchPlaceholder: 'Cari kodu, unvan veya ad ara',
        search: async (query, signal) => {
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
    }
  ];
  const openAction = $derived(page.url.searchParams.get('auto_open') === 'true');
  const actionPrefill = $derived({
    partyID: page.url.searchParams.get('party_id') ?? undefined,
    currency: page.url.searchParams.get('currency') ?? undefined,
    entryKind: page.url.searchParams.get('entry_kind') ?? undefined
  });
</script>

<OperationListPage
  title="Cari Hareketler"
  requiredPermission="party.ledger.read"
  endpoint="/party-movements"
  {columns}
  {filters}
  primaryLabel="Manuel Hareket"
  {openAction}
  {actionPrefill}
/>
