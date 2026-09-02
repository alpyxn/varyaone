<script lang="ts">
  import { describeBalance } from '$lib/design/balance';
  import type { Party } from './types';
  let { value, row }: { value: unknown; row: Party } = $props();
</script>

{#if typeof value !== 'string' && typeof value !== 'number'}
  <span><strong>—</strong><small>Bakiye alınamadı.</small></span>
{:else}
  {@const balance = describeBalance(value, row.balance_currency || row.default_currency || 'TRY')}
  <span class:negative={balance.tone === 'credit'}>
    <strong>{balance.headline}</strong>
    <small>{balance.meaning}</small>
  </span>
{/if}

<style>
  span {
    display: inline-flex;
    flex-direction: column;
    align-items: flex-end;
    gap: 2px;
    font-variant-numeric: tabular-nums;
    font-weight: 700;
  }
  small {
    color: var(--text-muted);
    font-size: 10px;
    font-weight: 400;
    white-space: nowrap;
  }
  .negative {
    color: var(--danger);
  }
</style>
