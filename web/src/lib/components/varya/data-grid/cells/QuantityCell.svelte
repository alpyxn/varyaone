<script lang="ts">
  import { formatQuantity } from '$lib/design/formatters';
  let { value }: { value: unknown; row?: unknown } = $props();
</script>

<span class="numeric"
  >{formatQuantity(typeof value === 'string' || typeof value === 'number' ? value : '')}</span
>

<style>
  .numeric {
    font-variant-numeric: tabular-nums;
  }
</style>
