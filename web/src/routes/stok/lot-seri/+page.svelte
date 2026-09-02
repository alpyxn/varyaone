<script lang="ts">
  import OperationListPage, {
    type OperationColumn
  } from '$lib/features/operations/OperationListPage.svelte';

  let tab = $state<'lot' | 'serial'>('lot');
  const lotColumns: OperationColumn[] = [
    { id: 'lot_number', label: 'Lot No', sortable: true },
    { id: 'product_name', label: 'Stok Kartı', sortable: true },
    { id: 'warehouse_name', label: 'Depo' },
    { id: 'available_quantity', label: 'Kullanılabilir', align: 'right' },
    { id: 'expires_at', label: 'SKT', sortable: true },
    { id: 'status', label: 'Durum' }
  ];
  const serialColumns: OperationColumn[] = [
    { id: 'serial_number', label: 'Seri No', sortable: true },
    { id: 'product_name', label: 'Stok Kartı', sortable: true },
    { id: 'status', label: 'Durum' },
    { id: 'warehouse_name', label: 'Depo' },
    { id: 'created_at', label: 'Giriş Tarihi', sortable: true }
  ];
</script>

<svelte:head><title>Lot / Seri · Varya One</title></svelte:head>
<div class="tabs" role="tablist" aria-label="Lot ve seri görünümleri">
  <button
    class:active={tab === 'lot'}
    role="tab"
    aria-selected={tab === 'lot'}
    onclick={() => (tab = 'lot')}>Lot / Partiler</button
  >
  <button
    class:active={tab === 'serial'}
    role="tab"
    aria-selected={tab === 'serial'}
    onclick={() => (tab = 'serial')}>Seri Numaraları</button
  >
</div>
{#if tab === 'lot'}
  <OperationListPage
    title="Lot / Partiler"
    subtitle="Lot bakiyeleri, SKT ve depo dağılımını izleyin"
    requiredPermission="inventory.lot_serial.read"
    endpoint="/lots"
    columns={lotColumns}
    primaryLabel="Lot / Seri"
  />
{:else}
  <OperationListPage
    title="Seri Numaraları"
    subtitle="Seri konumu ve yaşam döngüsünü izleyin"
    requiredPermission="inventory.lot_serial.read"
    endpoint="/serial-numbers"
    columns={serialColumns}
    primaryLabel="Seri Numaraları"
  />
{/if}

<style>
  .tabs {
    display: flex;
    gap: 4px;
    margin-bottom: 10px;
    border-bottom: 1px solid var(--border);
  }
  .tabs button {
    padding: 8px 12px;
    border: 0;
    border-bottom: 2px solid transparent;
    background: transparent;
    color: var(--text-muted);
    font: inherit;
    font-size: 12px;
    cursor: pointer;
  }
  .tabs button.active {
    border-bottom-color: var(--primary);
    color: var(--text);
    font-weight: 700;
  }
</style>
