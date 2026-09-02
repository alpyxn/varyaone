<script lang="ts">
  import { page } from '$app/state';
  import PartyStatement from '$lib/features/parties/PartyStatement.svelte';
</script>

<svelte:head><title>Cari Ekstre · Varya One</title></svelte:head>

{#key page.params.id}
  <PartyStatement partyID={page.params.id ?? ''} />
{/key}
