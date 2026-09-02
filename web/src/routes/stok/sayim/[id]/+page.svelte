<script lang="ts">
  import { page } from '$app/state';
  import CountWorkspace from '$lib/features/counts/CountWorkspace.svelte';
</script>

<CountWorkspace id={page.params.id ?? ''} />
