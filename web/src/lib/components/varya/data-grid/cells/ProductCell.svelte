<script lang="ts">
  let { value }: { value: unknown; row?: unknown } = $props();
</script>

<span class="entity">{String(value ?? '—')}</span>

<style>
  .entity {
    overflow: hidden;
    text-overflow: ellipsis;
    font-weight: 650;
  }
</style>
