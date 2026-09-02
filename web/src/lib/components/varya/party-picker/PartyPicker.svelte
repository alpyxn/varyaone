<script lang="ts">
  import { EntityLookup } from '$lib/components/varya/entity-lookup';
  export type PartyOption = { id: string; code: string; display_name: string };
  let {
    value = $bindable(),
    query = $bindable(''),
    results = []
  }: { value?: string; query?: string; results?: PartyOption[] } = $props();
</script>

<EntityLookup
  bind:value
  bind:query
  {results}
  label="Cari"
  placeholder="Cari kodu veya ünvan ara"
  display={(party) => `${party.code} · ${party.display_name}`}
/>
