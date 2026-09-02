<script lang="ts">
  import { MoreHorizontal } from '@lucide/svelte';
  let { label = 'Satır işlemleri' }: { value?: unknown; row?: unknown; label?: string } = $props();
</script>

<button aria-label={label} title={label}><MoreHorizontal size={16} /></button>

<style>
  button {
    display: grid;
    place-items: center;
    width: 26px;
    height: 26px;
    border: 0;
    border-radius: 5px;
    background: transparent;
    color: var(--text-muted);
  }
  button:hover {
    background: var(--surface-subtle);
    color: var(--text);
  }
</style>
