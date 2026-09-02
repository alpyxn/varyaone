<script lang="ts">
  import CommercialDetailPage from '$lib/features/commercial/CommercialDetailPage.svelte';
  import { page } from '$app/state';
  import { commercialResource } from '$lib/features/commercial/types';

  const resource = $derived(commercialResource(page.params.resource));
</script>

<CommercialDetailPage direction="sales" {resource} mode="new" />
