<script lang="ts">
  import { EntityLookup } from '$lib/components/varya/entity-lookup';
  export type ProductOption = { id: string; sku: string; name: string };
  let {
    value = $bindable(),
    query = $bindable(''),
    results = []
  }: { value?: string; query?: string; results?: ProductOption[] } = $props();
</script>

<EntityLookup
  bind:value
  bind:query
  {results}
  label="Stok / Hizmet"
  placeholder="Kod, ad veya barkod ara"
  display={(product) => `${product.sku} · ${product.name}`}
/>
