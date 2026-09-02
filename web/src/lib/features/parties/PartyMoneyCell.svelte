<script lang="ts">
  import { formatMoney } from '$lib/design/formatters';
  import type { Party } from './types';

  let { value, row }: { value: unknown; row: Party } = $props();
</script>

<span
  >{typeof value === 'string' || typeof value === 'number'
    ? formatMoney(value, row.default_currency || 'TRY')
    : '—'}</span
>

<style>
  span {
    font-variant-numeric: tabular-nums;
  }
</style>
