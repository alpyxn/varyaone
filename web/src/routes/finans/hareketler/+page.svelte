<script lang="ts">
  import OperationListPage, {
    type OperationColumn,
    type OperationFilter
  } from '$lib/features/operations/OperationListPage.svelte';

  const columns: OperationColumn[] = [
    { id: 'transaction_date', label: 'Tarih', sortable: true },
    { id: 'account_type', label: 'Hesap Türü' },
    { id: 'movement_kind', label: 'Hareket' },
    { id: 'source_label', label: 'Kaynak' },
    { id: 'direction', label: 'Yön' },
    { id: 'description', label: 'Açıklama' },
    { id: 'amount', label: 'Tutar', align: 'right' },
    { id: 'currency', label: 'Para Birimi' }
  ];
  const filters: OperationFilter[] = [
    {
      field: 'direction',
      label: 'Yön',
      kind: 'select',
      options: [
        { value: '', label: 'Tümü' },
        { value: 'IN', label: 'Giriş' },
        { value: 'OUT', label: 'Çıkış' }
      ]
    },
    { field: 'from', label: 'Başlangıç', kind: 'date' },
    { field: 'to', label: 'Bitiş', kind: 'date' }
  ];
  const enumLabels = {
    account_type: { CASH: 'Kasa', BANK: 'Banka' },
    direction: { IN: 'Giriş', OUT: 'Çıkış' }
  };
</script>

<OperationListPage
  title="Hesap Hareketleri"
  requiredPermission="finance.cash_movement.read"
  anyPermission={['finance.bank_movement.read']}
  endpoint="/finance/movements"
  {columns}
  {filters}
  {enumLabels}
  showPrimary={false}
/>
