<script lang="ts">
  import { goto } from '$app/navigation';
  import { Plus } from '@lucide/svelte';
  import { Button } from '$lib/components/ui/button';
  import OperationListPage, {
    type OperationColumn,
    type OperationFilter
  } from '$lib/features/operations/OperationListPage.svelte';

  const columns: OperationColumn[] = [
    { id: 'transaction_date', label: 'Tarih', sortable: true },
    { id: 'document_no', label: 'Belge No' },
    { id: 'from_account_name', label: 'Kaynak' },
    { id: 'to_account_name', label: 'Hedef' },
    { id: 'description', label: 'Açıklama' },
    { id: 'amount', label: 'Tutar', align: 'right' },
    { id: 'currency', label: 'Para Birimi' },
    { id: 'status', label: 'Durum' }
  ];
  const filters: OperationFilter[] = [
    { field: 'from', label: 'Başlangıç', kind: 'date' },
    { field: 'to', label: 'Bitiş', kind: 'date' }
  ];
  const enumLabels = { status: { POSTED: 'İşlendi', REVERSED: 'Ters Kayıt' } };
</script>

<OperationListPage
  title="Hesap Transferleri"
  requiredPermission="finance.transfer.read"
  endpoint="/finance/transfers"
  {columns}
  {filters}
  {enumLabels}
  showPrimary={false}
>
  {#snippet primaryAction()}
    <Button onclick={() => goto('/finans/transferler/yeni')}><Plus size={14} />Yeni transfer</Button
    >
  {/snippet}
</OperationListPage>
