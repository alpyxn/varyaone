<script lang="ts">
  import { goto } from '$app/navigation';
  import { onMount } from 'svelte';
  import { api, type Session } from '$lib/api';
  import {
    createPriceList,
    listPricingCurrencies,
    listPriceLists,
    setPriceListActive
  } from '$lib/features/pricing/api';
  import { listProductCategories } from '$lib/features/products/api';
  import type { ProductCategory } from '$lib/features/products/types';
  import type { PriceList, PricingCurrency } from '$lib/features/pricing/types';

  let session = $state<Session | null>(null);
  let lists = $state<PriceList[]>([]);
  let categories = $state<ProductCategory[]>([]);
  let currencies = $state<PricingCurrency[]>([]);
  let selectedListID = $state('');
  let loading = $state(true);
  let saving = $state(false);
  let message = $state('');
  let error = $state('');
  let listForm = $state<{
    name: string;
    description: string;
    applies_to_all_categories: boolean;
    scope_category_id: string;
    currency_code: string;
  }>({
    name: '',
    description: '',
    applies_to_all_categories: true,
    scope_category_id: '',
    currency_code: 'TRY'
  });

  const canManage = $derived(Boolean(session?.permissions.includes('pricing.manage')));
  const selectedList = $derived(lists.find((item) => item.id === selectedListID));

  function errorMessage(cause: unknown, fallback: string) {
    return typeof cause === 'object' && cause && 'message' in cause
      ? String(cause.message)
      : fallback;
  }

  async function load() {
    try {
      session = await api<Session>('/session');
      const [listResult, categoryResult, currencyResult] = await Promise.all([
        listPriceLists(),
        listProductCategories(),
        listPricingCurrencies(false)
      ]);
      lists = listResult.items;
      categories = categoryResult;
      currencies = currencyResult.items;
      if (!currencies.some((item) => item.code === listForm.currency_code) && currencies[0]) {
        listForm = { ...listForm, currency_code: currencies[0].code };
      }
      if (!selectedListID && lists.length) selectedListID = lists[0].id;
    } catch (cause) {
      if (!session) await goto('/giris');
      error = errorMessage(cause, 'Fiyat tanımları okunamadı.');
    } finally {
      loading = false;
    }
  }

  function selectList(id: string) {
    selectedListID = id;
  }

  async function saveList(event: SubmitEvent) {
    event.preventDefault();
    if (!canManage || saving) return;
    saving = true;
    error = '';
    try {
      const created = await createPriceList({
        name: listForm.name,
        description: listForm.description,
        applies_to_all_categories: listForm.applies_to_all_categories,
        scope_category_id: listForm.applies_to_all_categories
          ? undefined
          : listForm.scope_category_id,
        currency_code: listForm.currency_code
      });
      lists = [...lists, created].sort((a, b) => a.name.localeCompare(b.name, 'tr'));
      listForm = {
        name: '',
        description: '',
        applies_to_all_categories: true,
        scope_category_id: '',
        currency_code: listForm.currency_code
      };
      selectedListID = created.id;
      message = `${created.name} fiyat tanımı oluşturuldu.`;
    } catch (cause) {
      error = errorMessage(cause, 'Fiyat tanımı oluşturulamadı.');
    } finally {
      saving = false;
    }
  }

  async function toggleList(list: PriceList) {
    if (!canManage || saving) return;
    saving = true;
    error = '';
    try {
      const updated = await setPriceListActive(list.id, list.version, !list.is_active);
      lists = lists.map((item) => (item.id === updated.id ? updated : item));
      message = updated.is_active
        ? 'Fiyat tanımı etkinleştirildi.'
        : 'Fiyat tanımı pasifleştirildi.';
    } catch (cause) {
      error = errorMessage(cause, 'Fiyat tanımı güncellenemedi.');
    } finally {
      saving = false;
    }
  }

  onMount(() => {
    void load();
  });
</script>

<svelte:head><title>Fiyat Tanımları · Varya One</title></svelte:head>
<header class="page-header">
  <div>
    <h1>Fiyat tanımları</h1>
    <p>Satış ve alışta kullanılan fiyat listelerini ve kalemlerini yönetin.</p>
  </div>
  <div class="page-actions">
    <a class="button secondary" href="/ayarlar/tanimlar">Tüm tanımlar</a>
  </div>
</header>
{#if message}<div class="notice success" role="status">{message}</div>{/if}
{#if error}<div class="notice error" role="alert">{error}</div>{/if}
{#if loading}<div class="card">Fiyat listeleri yükleniyor…</div>{:else}
  <section class="workspace-grid">
    <form class="card form" onsubmit={saveList}>
      <h2 class="panel-title">Yeni fiyat tanımı</h2>
      <label class="field"
        >Tanım adı<input
          bind:value={listForm.name}
          maxlength="120"
          required
          disabled={!canManage}
        /></label
      >
      <label class="field"
        >Tanım açıklaması<textarea
          rows="3"
          bind:value={listForm.description}
          maxlength="500"
          disabled={!canManage}
        ></textarea></label
      >
      <label class="field"
        >Para birimi<select bind:value={listForm.currency_code} disabled={!canManage}>
          {#if currencies.length === 0}<option value="TRY">TRY · Türk lirası</option>{/if}
          {#each currencies as currency}<option value={currency.code}
              >{currency.code} · {currency.name}</option
            >{/each}
        </select></label
      >
      <fieldset class="scope-fieldset" disabled={!canManage}>
        <legend>Uygulama kapsamı</legend>
        <label class="radio-line"
          ><input
            type="radio"
            name="price-scope"
            checked={listForm.applies_to_all_categories}
            onchange={() =>
              (listForm = { ...listForm, applies_to_all_categories: true, scope_category_id: '' })}
          />Tüm stok kategorileri</label
        >
        <label class="radio-line"
          ><input
            type="radio"
            name="price-scope"
            checked={!listForm.applies_to_all_categories}
            onchange={() => (listForm = { ...listForm, applies_to_all_categories: false })}
          />Seçili stok kategorisi</label
        >
        {#if !listForm.applies_to_all_categories}
          <select
            value={listForm.scope_category_id}
            required
            onchange={(event) =>
              (listForm = { ...listForm, scope_category_id: event.currentTarget.value })}
          >
            <option value="">Kategori seçin</option>
            {#each categories.filter((item) => item.is_active) as category}<option
                value={category.id}>{category.name}</option
              >{/each}
          </select>
        {/if}
      </fieldset>
      <button class="button" type="submit" disabled={!canManage || saving}>Tanımı oluştur</button>
    </form>
  </section>
  <section class="card form">
    <h2 class="panel-title">Tanımlı listeler</h2>
    {#if lists.length === 0}<p class="lead">Kayıtlı fiyat listesi yok.</p>{:else}<div class="stack">
        {#each lists as list}<div
            class:selected={list.id === selectedListID}
            class="list-row"
            onclick={() => selectList(list.id)}
            role="button"
            tabindex="0"
            onkeydown={(event) => {
              if (event.key === 'Enter' || event.key === ' ') {
                event.preventDefault();
                void selectList(list.id);
              }
            }}
          >
            <span
              ><strong>{list.name}</strong><small
                >{list.code} · {list.currency_code}{#if list.description}
                  · {list.description}{/if}</small
              ></span
            ><span
              >{list.is_active ? 'Aktif' : 'Pasif'}
              <button
                class="link-button"
                type="button"
                onclick={(event) => {
                  event.stopPropagation();
                  void toggleList(list);
                }}>{list.is_active ? 'Pasifleştir' : 'Etkinleştir'}</button
              ></span
            >
          </div>{/each}
      </div>{/if}
  </section>
  {#if selectedList}
    <section class="card form">
      <h2 class="panel-title">{selectedList.name}</h2>
      <p class="lead">{selectedList.description || 'Açıklama girilmedi.'}</p>
      <dl class="scope-summary">
        <div>
          <dt>Uygulama kapsamı</dt>
          <dd>
            {selectedList.applies_to_all_categories
              ? 'Tüm stok kategorileri'
              : categories.find((item) => item.id === selectedList.scope_category_id)?.name ||
                'Seçili kategori'}
          </dd>
        </div>
        <div>
          <dt>Durum</dt>
          <dd>{selectedList.is_active ? 'Aktif' : 'Pasif'}</dd>
        </div>
        <div>
          <dt>Para birimi</dt>
          <dd>{selectedList.currency_code}</dd>
        </div>
      </dl>
    </section>
  {/if}
{/if}

<style>
  .stack {
    display: grid;
    gap: 0.5rem;
  }
  .list-row {
    display: flex;
    justify-content: space-between;
    gap: 1rem;
    width: 100%;
    border: 1px solid var(--border);
    background: var(--surface);
    padding: 0.7rem;
    text-align: left;
    color: var(--text);
    cursor: pointer;
  }
  .list-row small {
    display: block;
    margin-top: 3px;
    color: var(--text-muted);
    font-size: 11px;
  }
  .list-row.selected {
    border-color: var(--primary);
    background: var(--primary-soft);
  }
  .scope-fieldset {
    display: grid;
    gap: 8px;
    margin: 0;
    padding: 10px;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
  }
  .scope-fieldset legend {
    padding: 0 4px;
    color: var(--text-muted);
    font-size: 11px;
    font-weight: 700;
  }
  .radio-line {
    display: flex;
    align-items: center;
    gap: 7px;
    color: var(--text);
    font-size: 12px;
  }
  .scope-summary {
    display: grid;
    gap: 8px;
    margin: 16px 0 0;
  }
  .scope-summary div {
    display: flex;
    justify-content: space-between;
    gap: 12px;
    padding-bottom: 8px;
    border-bottom: 1px solid var(--border);
  }
  .scope-summary dt {
    color: var(--text-muted);
    font-size: 11px;
  }
  .scope-summary dd {
    margin: 0;
    font-size: 12px;
    font-weight: 650;
  }
</style>
