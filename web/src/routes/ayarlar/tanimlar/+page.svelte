<script lang="ts">
  import { goto } from '$app/navigation';
  import { onMount } from 'svelte';
  import { ChevronRight } from '@lucide/svelte';
  import { api, type Session } from '$lib/api';
  import { isModuleEnabled, type ModuleCode } from '$lib/modules';

  type DefinitionLink = {
    label: string;
    href: string;
    permission: string;
    alternatePermissions?: string[];
    module?: ModuleCode;
  };

  const definitionLinks: DefinitionLink[] = [
    {
      label: 'Cari Grupları',
      href: '/ayarlar/tanimlar/cari-gruplari',
      permission: 'party.read',
      module: 'preaccounting'
    },
    {
      label: 'Fiyat Tanımları',
      href: '/ayarlar/tanimlar/fiyat-listeleri',
      permission: 'pricing.read',
      module: 'preaccounting'
    },
    {
      label: 'Stok Kategorileri',
      href: '/ayarlar/tanimlar/kategoriler',
      permission: 'product.read',
      module: 'preaccounting'
    },
    {
      label: 'Stok Markaları',
      href: '/ayarlar/tanimlar/markalar',
      permission: 'product.read',
      module: 'preaccounting'
    },
    {
      label: 'Varyant Tanımları',
      href: '/ayarlar/tanimlar/varyantlar',
      permission: 'product.read',
      alternatePermissions: ['product.variant_definition.manage'],
      module: 'preaccounting'
    },
    {
      label: 'Vergi Tanımları',
      href: '/ayarlar/tanimlar/vergi-tanimlari',
      permission: 'tax.read',
      module: 'preaccounting'
    },
    {
      label: 'Sabit Kıymet Kategorileri',
      href: '/ayarlar/tanimlar/sabit-kiymet-kategorileri',
      permission: 'fixed_asset.read',
      module: 'fixed_asset'
    },
    {
      label: 'Asgari Ücret',
      href: '/ayarlar/tanimlar/asgari-ucret',
      permission: 'hr.legislation.read',
      module: 'hr'
    }
  ];

  let session = $state<Session>();
  let loading = $state(true);
  const visibleDefinitions = $derived(
    definitionLinks.filter(
      (item) =>
        isModuleEnabled(item.module, session?.modules) &&
        (session?.permissions.includes(item.permission) ||
          item.alternatePermissions?.some((permission) =>
            session?.permissions.includes(permission)
          ))
    )
  );

  async function load() {
    try {
      session = await api<Session>('/session');
    } catch {
      await goto('/giris');
    } finally {
      loading = false;
    }
  }

  onMount(() => {
    void load();
  });
</script>

<svelte:head><title>Tanımlar · Varya One</title></svelte:head>

<header class="page-header">
  <div>
    <h1>Tanımlar</h1>
  </div>
</header>

{#if !loading && visibleDefinitions.length === 0}
  <p class="notice" role="status">Tanım ekranlarını görüntüleme yetkiniz yok.</p>
{:else if !loading}
  <nav class="definition-grid" aria-label="Tanımlar">
    {#each visibleDefinitions as item}
      <a class="definition-card" href={item.href}>
        <strong>{item.label}</strong>
        <span>Aç <ChevronRight size={13} /></span>
      </a>
    {/each}
  </nav>
{/if}
