<script lang="ts">
  import { goto } from '$app/navigation';
  import { Plus } from '@lucide/svelte';
  import { Button } from '$lib/components/ui/button';
  import OperationListPage, {
    type OperationColumn,
    type OperationFilter
  } from '$lib/features/operations/OperationListPage.svelte';
  import {
    commercialPath,
    commercialConfig,
    commercialStatusLabels,
    commercialStatusOptions,
    type CommercialDirection,
    type CommercialResource
  } from './types';

  let {
    direction,
    resource
  }: { direction: CommercialDirection; resource: CommercialResource | undefined } = $props();
  const config = $derived(commercialConfig(direction, resource));
  const routePath = $derived(commercialPath(direction, resource));

  const lifecycleOptions = $derived(
    config ? commercialStatusOptions(config.resource, 'lifecycle_status') : []
  );
  const fulfillmentOptions = $derived(
    config ? commercialStatusOptions(config.resource, 'fulfillment_status') : []
  );
  const invoicingOptions = $derived(
    config ? commercialStatusOptions(config.resource, 'invoicing_status') : []
  );
  const paymentOptions = $derived(
    config ? commercialStatusOptions(config.resource, 'payment_status') : []
  );
  const columns = $derived<OperationColumn[]>([
    { id: 'document_no', label: 'Belge No', sortable: true },
    { id: 'party_name', label: direction === 'sales' ? 'Cari' : 'Tedarikçi' },
    { id: 'document_date', label: 'Tarih', sortable: true },
    { id: 'lifecycle_status', label: 'Belge durumu' },
    ...(fulfillmentOptions.length ? [{ id: 'fulfillment_status', label: 'Karşılama' }] : []),
    ...(resource === 'orders' || resource === 'dispatches'
      ? [{ id: 'fulfillment_at', label: 'Karşılama zamanı', sortable: true }]
      : []),
    ...(invoicingOptions.length ? [{ id: 'invoicing_status', label: 'Faturalama' }] : []),
    ...(paymentOptions.length ? [{ id: 'payment_status', label: 'Ödeme' }] : []),
    ...(resource === 'quotes' || resource === 'orders' || resource === 'invoices'
      ? [{ id: 'tax_total', label: 'KDV', align: 'right' as const, sortable: true }]
      : []),
    ...(resource === 'quotes' ||
    resource === 'orders' ||
    resource === 'invoices' ||
    resource === 'returns'
      ? [{ id: 'grand_total', label: 'Vergili toplam', align: 'right' as const, sortable: true }]
      : []),
    ...(resource === 'invoices'
      ? [{ id: 'payable_total', label: 'Borç toplamı', align: 'right' as const, sortable: true }]
      : []),
    { id: 'currency_code', label: 'PB', align: 'center' as const }
  ]);
  const filters = $derived<OperationFilter[]>([
    { field: 'lifecycle_status', label: 'Belge durumu', kind: 'select', options: lifecycleOptions },
    ...(fulfillmentOptions.length
      ? [
          {
            field: 'fulfillment_status',
            label: 'Karşılama',
            kind: 'select' as const,
            options: fulfillmentOptions
          }
        ]
      : []),
    ...(invoicingOptions.length
      ? [
          {
            field: 'invoicing_status',
            label: 'Faturalama',
            kind: 'select' as const,
            options: invoicingOptions
          }
        ]
      : []),
    ...(paymentOptions.length
      ? [
          {
            field: 'payment_status',
            label: 'Ödeme',
            kind: 'select' as const,
            options: paymentOptions
          }
        ]
      : []),
    { field: 'from', label: 'Başlangıç', kind: 'date' },
    { field: 'to', label: 'Bitiş', kind: 'date' }
  ]);
</script>

{#if config}
  {#key `${direction}:${resource ?? ''}`}
    <OperationListPage
      title={config.listTitle}
      subtitle={config.subtitle}
      requiredPermission={config.permission}
      endpoint={config.endpoint}
      {columns}
      {filters}
      enumLabels={commercialStatusLabels}
      showPrimary={false}
      searchPlaceholder="Belge no, cari veya not ara"
      detailPath={(row) => `${routePath}/${encodeURIComponent(String(row.id ?? ''))}`}
    >
      {#snippet primaryAction()}
        <Button onclick={() => goto(`${routePath}/yeni`)}>
          <Plus size={14} aria-hidden="true" />{config.primaryLabel}
        </Button>
      {/snippet}
    </OperationListPage>
  {/key}
{:else}
  <section class="route-error" role="alert">Bu belge ekranı bulunamadı.</section>
{/if}

<style>
  .route-error {
    padding: 28px;
    border: 1px solid var(--border);
    border-radius: var(--radius-panel);
    background: var(--surface);
    color: var(--danger);
  }
</style>
