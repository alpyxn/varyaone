<script lang="ts">
  import { AlertCircle } from '@lucide/svelte';
  import * as Alert from '$lib/components/ui/alert';

  let {
    errors = {},
    title = 'Formu kontrol edin'
  }: { errors?: Record<string, string>; title?: string } = $props();
  const entries = $derived(Object.entries(errors).filter(([, message]) => Boolean(message)));
</script>

{#if entries.length}
  <Alert.Root
    variant="destructive"
    class="error-summary"
    tabindex={-1}
    role="alert"
    aria-live="assertive"
  >
    <AlertCircle aria-hidden="true" />
    <div>
      <Alert.Title>{title}</Alert.Title>
      <ul>
        {#each entries as [id, message]}
          <li><a href={`#${id}`}>{message}</a></li>
        {/each}
      </ul>
    </div>
  </Alert.Root>
{/if}

<style>
  :global(.error-summary) {
    margin-bottom: 12px;
    border-color: color-mix(in srgb, var(--danger) 35%, var(--border));
  }
  :global(.error-summary ul) {
    display: grid;
    gap: 4px;
    margin: 5px 0 0;
    padding-left: 17px;
  }
  :global(.error-summary a) {
    color: var(--danger);
    text-decoration: underline;
  }
</style>
