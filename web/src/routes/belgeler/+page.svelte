<script lang="ts">
  import OperationListPage, {
    type OperationColumn
  } from '$lib/features/operations/OperationListPage.svelte';
  const columns: OperationColumn[] = [
    { id: 'document_no', label: 'Belge No', sortable: true },
    { id: 'document_type_code', label: 'Tür' },
    { id: 'document_date', label: 'Tarih', sortable: true },
    { id: 'status', label: 'Durum' }
  ];
</script>

<OperationListPage
  title="Belgeler"
  subtitle="Provider-neutral ticari belgeler"
  requiredPermission="document.read"
  endpoint="/documents"
  {columns}
  primaryLabel="Yeni Belge"
/>
