<script lang="ts">
  import { goto } from '$app/navigation';
  import { Plus } from '@lucide/svelte';
  import { Button } from '$lib/components/ui/button';
  import OperationListPage, {
    type OperationColumn,
    type OperationFilter
  } from '$lib/features/operations/OperationListPage.svelte';

  const columns: OperationColumn[] = [
    { id: 'code', label: 'Hesap Kodu', sortable: true },
    { id: 'name', label: 'Hesap Adı', sortable: true },
    { id: 'account_type', label: 'Tür' },
    { id: 'bank_name', label: 'Banka' },
    { id: 'iban', label: 'IBAN' },
    { id: 'currency', label: 'Para Birimi' },
    { id: 'is_active', label: 'Durum' }
  ];
  const filters: OperationFilter[] = [
    {
      field: 'type',
      label: 'Tür',
      kind: 'select',
      options: [
        { value: '', label: 'Kasa + Banka' },
        { value: 'CASH', label: 'Kasa' },
        { value: 'BANK', label: 'Banka' }
      ]
    }
  ];
  const enumLabels = { account_type: { CASH: 'Kasa', BANK: 'Banka' } };
</script>

<OperationListPage
  title="Banka & Kasa Hesapları"
  requiredPermission="finance.cash_account.read"
  anyPermission={['finance.bank_account.read']}
  endpoint="/finance/accounts"
  {columns}
  {filters}
  {enumLabels}
  includeInactiveFilter
  showPrimary={false}
>
  {#snippet primaryAction()}
    <Button onclick={() => goto('/finans/hesaplar/yeni')}><Plus size={14} />Yeni hesap</Button>
  {/snippet}
</OperationListPage>
