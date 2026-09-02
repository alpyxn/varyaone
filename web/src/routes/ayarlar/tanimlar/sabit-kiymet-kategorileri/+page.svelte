<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { api, type Session } from '$lib/api';
  import {
    createFixedAssetCategory,
    listFixedAssetCategories,
    setFixedAssetCategoryActive,
    updateFixedAssetCategory
  } from '$lib/features/fixed-assets/api';
  import type { FixedAssetCategory } from '$lib/features/fixed-assets/types';

  let session = $state<Session>();
  let items = $state<FixedAssetCategory[]>([]);
  let loading = $state(true);
  let saving = $state(false);
  let error = $state('');
  let message = $state('');

  let name = $state('');
  let code = $state('');
  let description = $state('');

  let editing = $state<string | null>(null);
  let editName = $state('');
  let editDescription = $state('');

  const canManage = $derived(Boolean(session?.permissions.includes('fixed_asset.edit')));
  const active = $derived(items.filter((c) => c.is_active));
  const passive = $derived(items.filter((c) => !c.is_active));

  async function load() {
    try {
      session = await api<Session>('/session');
      await refresh();
    } catch {
      await goto('/giris');
    } finally {
      loading = false;
    }
  }

  async function refresh() {
    items = (await listFixedAssetCategories(true)).items;
  }

  async function guard(fn: () => Promise<unknown>, ok: string) {
    if (!canManage || saving) return;
    saving = true;
    error = '';
    message = '';
    try {
      await fn();
      await refresh();
      message = ok;
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'İşlem tamamlanamadı.';
    } finally {
      saving = false;
    }
  }

  function create(event: SubmitEvent) {
    event.preventDefault();
    if (!name.trim()) return;
    void guard(
      () =>
        createFixedAssetCategory({
          name: name.trim(),
          code: code.trim(),
          description: description.trim()
        }).then(() => {
          name = '';
          code = '';
          description = '';
        }),
      'Kategori eklendi.'
    );
  }

  function startEdit(item: FixedAssetCategory) {
    editing = item.id;
    editName = item.name;
    editDescription = item.description;
    error = '';
  }

  function saveEdit(item: FixedAssetCategory) {
    if (!editName.trim()) return;
    void guard(
      () =>
        updateFixedAssetCategory(item.id, item.version, {
          name: editName.trim(),
          code: '',
          description: editDescription.trim()
        }).then(() => {
          editing = null;
        }),
      'Kategori güncellendi.'
    );
  }

  function toggle(item: FixedAssetCategory) {
    void guard(
      () => setFixedAssetCategoryActive(item.id, !item.is_active),
      item.is_active ? 'Kategori pasifleştirildi.' : 'Kategori aktifleştirildi.'
    );
  }

  onMount(() => {
    void load();
  });
</script>

<svelte:head><title>Sabit Kıymet Kategorileri · Varya One</title></svelte:head>

<header class="page-header">
  <div>
    <h1>Sabit kıymet kategorileri</h1>
    <p>Demirbaş ve sabit kıymet kartlarını sınıflandırmak için kullanılan kategoriler.</p>
  </div>
  <div class="page-actions">
    <a class="button secondary" href="/ayarlar/tanimlar">Tüm tanımlar</a>
  </div>
</header>

{#if message}<div class="notice success" role="status">{message}</div>{/if}
{#if error}<div class="notice error" role="alert">{error}</div>{/if}

{#if loading}
  <div class="card">Kategoriler yükleniyor…</div>
{:else}
  <div class="workspace-grid">
    <form class="card form" onsubmit={create}>
      <h2 class="panel-title">Yeni kategori</h2>
      <label class="field"
        >Kategori adı
        <input bind:value={name} required maxlength="120" disabled={!canManage} />
      </label>
      <label class="field"
        >Kod (opsiyonel)
        <input
          bind:value={code}
          maxlength="40"
          placeholder="Boş bırakırsanız otomatik üretilir"
          disabled={!canManage}
        />
      </label>
      <label class="field"
        >Açıklama (opsiyonel)
        <input bind:value={description} maxlength="240" disabled={!canManage} />
      </label>
      <div class="form-actions">
        <button class="button" disabled={!canManage || saving}>Kategori ekle</button>
      </div>
    </form>

    <section class="card">
      <h2 class="panel-title">Kategoriler</h2>
      {#if items.length === 0}
        <p class="hint">Kategori tanımlı değil.</p>
      {:else}
        <div class="stack">
          {#each active as item (item.id)}
            {@render row(item)}
          {/each}
          {#if passive.length}
            <p class="group-label">Pasif kategoriler</p>
            {#each passive as item (item.id)}
              {@render row(item)}
            {/each}
          {/if}
        </div>
      {/if}
    </section>
  </div>
{/if}

{#snippet row(item: FixedAssetCategory)}
  <div class="list-row" class:passive={!item.is_active}>
    {#if editing === item.id}
      <div class="edit">
        <input bind:value={editName} maxlength="120" />
        <input bind:value={editDescription} maxlength="240" placeholder="Açıklama" />
      </div>
      <div class="row-actions">
        <button class="link-button" type="button" onclick={() => saveEdit(item)}>Kaydet</button>
        <button class="link-button" type="button" onclick={() => (editing = null)}>Vazgeç</button>
      </div>
    {:else}
      <span>
        <strong>{item.name}</strong>
        <small
          >{item.code}{item.is_system ? ' · varsayılan' : ''}{item.description
            ? ` · ${item.description}`
            : ''}</small
        >
      </span>
      {#if canManage}
        <div class="row-actions">
          {#if item.is_active}
            <button class="link-button" type="button" onclick={() => startEdit(item)}
              >Düzenle</button
            >
          {/if}
          <button class="link-button" type="button" disabled={saving} onclick={() => toggle(item)}
            >{item.is_active ? 'Pasifleştir' : 'Aktifleştir'}</button
          >
        </div>
      {/if}
    {/if}
  </div>
{/snippet}

<style>
  .group-label {
    margin: 0.75rem 0 0;
    font-size: 11px;
    font-weight: 700;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: var(--text-muted);
  }
  .list-row.passive {
    opacity: 0.55;
  }
  .row-actions {
    display: flex;
    gap: 0.75rem;
    flex-shrink: 0;
  }
  .edit {
    display: grid;
    gap: 4px;
    flex: 1;
  }
</style>
