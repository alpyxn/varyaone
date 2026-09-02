<script lang="ts">
  import { formatMoney, isNegativeDecimal } from '$lib/design/formatters';
  let { value, currency = 'TRY' }: { value: unknown; row?: unknown; currency?: string } = $props();
</script>

<span class="numeric" class:negative={isNegativeDecimal(value)}
  >{formatMoney(
    typeof value === 'string' || typeof value === 'number' ? value : '',
    currency
  )}</span
>

<style>
  .numeric {
    font-variant-numeric: tabular-nums;
    font-weight: 650;
  }
  .negative {
    color: var(--danger);
  }
</style>
