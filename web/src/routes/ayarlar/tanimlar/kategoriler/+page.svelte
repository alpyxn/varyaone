<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { api, type Session } from '$lib/api';
  import {
    createProductCategory,
    listProductCategories,
    setProductReferenceActive
  } from '$lib/features/products/api';
  import type { ProductCategory } from '$lib/features/products/types';

  let session = $state<Session>();
  let items = $state<ProductCategory[]>([]);
  let name = $state('');
  let loading = $state(true);
  let saving = $state(false);
  let error = $state('');
  let message = $state('');
  const canManage = $derived(Boolean(session?.permissions.includes('product.reference.manage')));

  async function load() {
    try {
      session = await api<Session>('/session');
      items = await listProductCategories();
    } catch {
      await goto('/giris');
    } finally {
      loading = false;
    }
  }

  async function save(event: SubmitEvent) {
    event.preventDefault();
    if (!canManage || !name.trim()) return;
    saving = true;
    error = '';
    try {
      const item = await createProductCategory(name);
      items = [...items, item].sort((a, b) => a.name.localeCompare(b.name, 'tr'));
      name = '';
      message = 'Kategori eklendi.';
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Kategori eklenemedi.';
    } finally {
      saving = false;
    }
  }

  async function toggle(item: ProductCategory) {
    if (!canManage || saving) return;
    saving = true;
    error = '';
    try {
      const updated = (await setProductReferenceActive(
        'categories',
        item.id,
        item.version,
        !item.is_active
      )) as ProductCategory;
      items = items.map((x) => (x.id === item.id ? updated : x));
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Kategori durumu güncellenemedi.';
    } finally {
      saving = false;
    }
  }

  onMount(() => {
    void load();
  });
</script>

<svelte:head><title>Stok Kategorileri · Varya One</title></svelte:head>

<header class="page-header">
  <div>
    <h1>Stok kategorileri</h1>
    <p>Stok kartlarını gruplamak için kullanılan kategori tanımlarını yönetin.</p>
  </div>
</header>
<nav class="page-subnav" aria-label="Ayarlar bölümleri">
  <a class="button secondary" href="/ayarlar/tanimlar">Tüm tanımlar</a>
</nav>

{#if message}<div class="notice success" role="status">{message}</div>{/if}
{#if error}<div class="notice error" role="alert">{error}</div>{/if}

{#if loading}
  <div class="card">Kategoriler yükleniyor…</div>
{:else}
  <div class="workspace-grid">
    <form class="card form" onsubmit={save}>
      <h2 class="panel-title">Yeni kategori</h2>
      <label class="field"
        >Tanım adı<input bind:value={name} required maxlength="120" disabled={!canManage} /></label
      >
      <div class="form-actions">
        <button class="button" disabled={!canManage || saving}>Kategori ekle</button>
      </div>
    </form>
    <section class="card">
      <h2 class="panel-title">Tanımlı kategoriler</h2>
      {#if items.length === 0}
        <p class="hint">Henüz kategori tanımlanmadı.</p>
      {:else}
        <div class="stack">
          {#each items as item}
            <div class="list-row" class:inactive={!item.is_active}>
              <span
                ><strong>{item.name}</strong><small>{item.is_active ? 'Aktif' : 'Pasif'}</small
                ></span
              >
              <button
                class="link-button"
                type="button"
                disabled={!canManage || saving}
                onclick={() => void toggle(item)}
                >{item.is_active ? 'Pasifleştir' : 'Etkinleştir'}</button
              >
            </div>
          {/each}
        </div>
      {/if}
    </section>
  </div>
{/if}

<style>
  .list-row.inactive strong {
    color: var(--text-muted);
  }
</style>
