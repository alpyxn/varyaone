<script lang="ts">
  import OperationListPage, {
    type OperationColumn
  } from '$lib/features/operations/OperationListPage.svelte';
  const columns: OperationColumn[] = [
    { id: 'code', label: 'Depo Kodu', sortable: true },
    { id: 'name', label: 'Depo Adı', sortable: true },
    { id: 'warehouse_type', label: 'Tür' },
    { id: 'address', label: 'Adres' },
    { id: 'is_active', label: 'Durum' }
  ];
</script>

<OperationListPage
  title="Depolar"
  requiredPermission="inventory.read"
  endpoint="/warehouses"
  includeInactiveFilter
  {columns}
  searchPlaceholder="Kod veya adres ara"
  primaryLabel="Yeni Depo"
/>
