<script lang="ts">
  let { value }: { value: unknown; row?: unknown } = $props();
</script>

<span class="currency">{String(value ?? '—')}</span>

<style>
  .currency {
    font-size: 11px;
    font-weight: 750;
    letter-spacing: 0.04em;
  }
</style>
