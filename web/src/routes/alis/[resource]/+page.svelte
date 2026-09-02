<script lang="ts">
  import CommercialListPage from '$lib/features/commercial/CommercialListPage.svelte';
  import { page } from '$app/state';
  import { commercialResource } from '$lib/features/commercial/types';

  const resource = $derived(commercialResource(page.params.resource));
</script>

<CommercialListPage direction="purchases" {resource} />
