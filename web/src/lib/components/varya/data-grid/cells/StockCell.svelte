<script lang="ts">
  import { formatQuantity, isNonPositiveDecimal } from '$lib/design/formatters';
  let { value }: { value: unknown; row?: unknown } = $props();
</script>

<span class:warning={isNonPositiveDecimal(value)}
  >{formatQuantity(typeof value === 'string' || typeof value === 'number' ? value : '')}</span
>

<style>
  span {
    font-variant-numeric: tabular-nums;
    font-weight: 650;
  }
  .warning {
    color: var(--danger);
  }
</style>
