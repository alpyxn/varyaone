<script lang="ts">
  import { page } from '$app/state';
  import OperationListPage, {
    type OperationColumn,
    type OperationFilter
  } from '$lib/features/operations/OperationListPage.svelte';
  import { financePartyFilter, financeAccountFilter } from '$lib/features/finance/list-filters';
  const columns: OperationColumn[] = [
    { id: 'transaction_date', label: 'Tarih', sortable: true },
    { id: 'party_name', label: 'Cari', sortable: true },
    { id: 'document_no', label: 'Makbuz No' },
    { id: 'payment_method', label: 'Yöntem' },
    { id: 'account_name', label: 'Hesap' },
    { id: 'amount', label: 'Tutar', align: 'right' },
    { id: 'status', label: 'Durum' }
  ];
  const filters: OperationFilter[] = [
    financePartyFilter(),
    financeAccountFilter(),
    {
      field: 'method',
      label: 'Yöntem',
      kind: 'select',
      options: [
        { value: 'CASH', label: 'Kasa' },
        { value: 'BANK', label: 'Banka' }
      ]
    },
    {
      field: 'status',
      label: 'Durum',
      kind: 'select',
      options: [
        { value: 'POSTED', label: 'İşlendi' },
        { value: 'REVERSED', label: 'Ters Kayıt' }
      ]
    },
    {
      field: 'amount_min',
      label: 'Min tutar',
      kind: 'text',
      inputMode: 'decimal',
      placeholder: '0'
    },
    {
      field: 'amount_max',
      label: 'Maks tutar',
      kind: 'text',
      inputMode: 'decimal',
      placeholder: '∞'
    },
    { field: 'from', label: 'Başlangıç', kind: 'date' },
    { field: 'to', label: 'Bitiş', kind: 'date' }
  ];
  const openAction = $derived(page.url.searchParams.get('auto_open') === 'true');
  const actionPrefill = $derived({
    partyID: page.url.searchParams.get('party_id') ?? undefined,
    currency: page.url.searchParams.get('currency') ?? undefined,
    documentID: page.url.searchParams.get('document_id') ?? undefined,
    method: page.url.searchParams.get('method') ?? undefined
  });
</script>

<OperationListPage
  title="Tahsilatlar"
  requiredPermission="finance.collection.read"
  endpoint="/finance/collections"
  {columns}
  {filters}
  primaryLabel="Yeni Tahsilat"
  {openAction}
  {actionPrefill}
/>
