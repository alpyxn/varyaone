<script lang="ts">
  import { AlertCircle, RefreshCw } from '@lucide/svelte';
  import { Button } from '$lib/components/ui/button';
  import * as Alert from '$lib/components/ui/alert';
  import * as Empty from '$lib/components/ui/empty';
  import { Skeleton } from '$lib/components/ui/skeleton';

  let {
    loading = false,
    error = '',
    empty = false,
    loadingText = 'Yükleniyor…',
    errorTitle = 'Veriler alınamadı',
    emptyTitle = 'Kayıt bulunamadı',
    emptyDescription = 'Arama veya filtre ölçütlerini değiştirin.',
    onRetry
  }: {
    loading?: boolean;
    error?: string;
    empty?: boolean;
    loadingText?: string;
    errorTitle?: string;
    emptyTitle?: string;
    emptyDescription?: string;
    onRetry?: () => void;
  } = $props();
</script>

{#if loading}
  <div class="state-block loading-state" role="status" aria-live="polite">
    <Skeleton class="state-skeleton" /><Skeleton class="state-skeleton short" />
    <span>{loadingText}</span>
  </div>
{:else if error}
  <Alert.Root variant="destructive" class="state-alert">
    <AlertCircle aria-hidden="true" />
    <div><Alert.Title>{errorTitle}</Alert.Title><Alert.Description>{error}</Alert.Description></div>
    {#if onRetry}<Button variant="outline" size="sm" onclick={onRetry}
        ><RefreshCw data-icon="inline-start" />Yeniden dene</Button
      >{/if}
  </Alert.Root>
{:else if empty}
  <Empty.Root class="state-empty"
    ><Empty.Header
      ><Empty.Title>{emptyTitle}</Empty.Title><Empty.Description
        >{emptyDescription}</Empty.Description
      ></Empty.Header
    ></Empty.Root
  >
{/if}

<style>
  .state-block,
  :global(.state-empty) {
    min-height: 150px;
  }
  .state-block {
    display: grid;
    place-items: center;
    gap: 8px;
    padding: 20px;
    color: var(--text-muted);
    font-size: 12px;
  }
  :global(.state-skeleton) {
    width: min(360px, 80%);
    height: 12px;
  }
  :global(.state-skeleton.short) {
    width: min(220px, 52%);
  }
  :global(.state-alert) {
    align-items: center;
    min-height: 78px;
    border-color: color-mix(in srgb, var(--danger) 35%, var(--border));
  }
  :global(.state-alert [data-slot='alert-title']) {
    font-weight: 700;
  }
  :global(.state-alert [data-slot='alert-description']) {
    color: var(--danger);
  }
</style>
