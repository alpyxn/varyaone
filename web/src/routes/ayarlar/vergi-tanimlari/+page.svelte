<script lang="ts">
  import { goto } from '$app/navigation';
  import { onMount } from 'svelte';
  import { api, type Session } from '$lib/api';
  import { formatQuantity } from '$lib/design/formatters';
  import { createTaxDefinition, listTaxDefinitions } from '$lib/features/taxes/api';
  import type { TaxDefinition } from '$lib/features/taxes/types';

  let session = $state<Session | null>(null);
  let definitions = $state<TaxDefinition[]>([]);
  let loading = $state(true);
  let saving = $state(false);
  let message = $state('');
  let error = $state('');
  let definitionForm = $state<{
    code: string;
    name: string;
    description: string;
    source: string;
    calculation_type: 'PERCENTAGE' | 'QUANTITY_BASED';
    rate: string;
  }>({
    code: '',
    name: '',
    description: '',
    source: '',
    calculation_type: 'PERCENTAGE',
    rate: ''
  });
  const canManage = $derived(Boolean(session?.permissions.includes('tax.manage')));

  function displayDecimal(value: string | number | null | undefined) {
    return formatQuantity(String(value ?? '0'));
  }

  function errorMessage(cause: unknown, fallback: string) {
    return typeof cause === 'object' && cause && 'message' in cause
      ? String(cause.message)
      : fallback;
  }
  async function load() {
    try {
      session = await api<Session>('/session');
      definitions = (await listTaxDefinitions()).items;
    } catch (cause) {
      if (!session) await goto('/giris');
      error = errorMessage(cause, 'Vergi tanımları okunamadı.');
    } finally {
      loading = false;
    }
  }
  async function saveDefinition(event: SubmitEvent) {
    event.preventDefault();
    if (!canManage || saving) return;
    saving = true;
    error = '';
    try {
      const created = await createTaxDefinition(definitionForm);
      definitions = [...definitions, created].sort((a, b) => a.code.localeCompare(b.code, 'tr'));
      definitionForm = {
        code: '',
        name: '',
        description: '',
        source: '',
        calculation_type: 'PERCENTAGE',
        rate: ''
      };
      message = `${created.name} vergi tanımı oluşturuldu.`;
    } catch (cause) {
      error = errorMessage(cause, 'Vergi tanımı oluşturulamadı.');
    } finally {
      saving = false;
    }
  }

  function sourceLabel(definition: TaxDefinition) {
    return definition.source === 'TR_TAX_LOCALIZATION' ? 'Hazır tanım' : definition.source;
  }

  function valueLabel(definition: TaxDefinition) {
    if (!definition.rate) return 'Değer tanımlı değil';
    return definition.calculation_type === 'QUANTITY_BASED'
      ? `Birim ${displayDecimal(definition.rate)}`
      : `%${displayDecimal(definition.rate)}`;
  }
  onMount(() => {
    void load();
  });
</script>

<svelte:head><title>Vergi Tanımları · Varya One</title></svelte:head>

<header class="page-header">
  <div>
    <h1>Vergi tanımları</h1>
    <p>Satış ve alış belgelerinde kullanılan KDV, ÖTV ve diğer vergi oranlarını tanımlayın.</p>
  </div>
</header>
<nav class="page-subnav" aria-label="Ayarlar bölümleri">
  <a class="button secondary" href="/ayarlar/tanimlar">Tüm tanımlar</a>
</nav>
{#if message}<div class="notice success" role="status">{message}</div>{/if}{#if error}<div
    class="notice error"
    role="alert"
  >
    {error}
  </div>{/if}
{#if loading}<div class="card">Vergi tanımları yükleniyor…</div>{:else}
  <section class="workspace-grid">
    <form class="card form" onsubmit={saveDefinition}>
      <h2 class="panel-title">Yeni vergi tanımı</h2>
      <label class="field"
        >Kod<input
          bind:value={definitionForm.code}
          maxlength="40"
          required
          disabled={!canManage}
        /></label
      >
      <label class="field"
        >Ad<input
          bind:value={definitionForm.name}
          maxlength="120"
          required
          disabled={!canManage}
        /></label
      >
      <label class="field"
        >Açıklama<textarea bind:value={definitionForm.description} rows="3" disabled={!canManage}
        ></textarea></label
      >
      <label class="field"
        >Kaynak<input
          bind:value={definitionForm.source}
          placeholder="Örn. GİB"
          required
          disabled={!canManage}
        /></label
      >
      <label class="field"
        >Hesaplama türü<select bind:value={definitionForm.calculation_type} disabled={!canManage}>
          <option value="PERCENTAGE">Oran (%)</option>
          <option value="QUANTITY_BASED">Birim tutarı</option>
        </select></label
      >
      <label class="field"
        >{definitionForm.calculation_type === 'QUANTITY_BASED' ? 'Birim tutarı' : 'Oran (%)'}<input
          bind:value={definitionForm.rate}
          inputmode="decimal"
          placeholder={definitionForm.calculation_type === 'QUANTITY_BASED'
            ? 'Örn. 2,50'
            : 'Örn. 20'}
          disabled={!canManage}
        /></label
      >
      <button class="button" type="submit" disabled={!canManage || saving}>Tanım oluştur</button>
    </form>
    <section class="card form">
      <h2 class="panel-title">Tanımlar</h2>
      {#if definitions.length === 0}<p class="lead">Kayıtlı vergi tanımı yok.</p>{:else}<div
          class="stack"
        >
          {#each definitions as definition}<div class="list-row">
              <span
                ><strong>{definition.code}</strong> · {definition.name}<small
                  >{valueLabel(definition)} · {sourceLabel(definition)}</small
                ></span
              ><span>{definition.is_active ? 'Aktif' : 'Pasif'}</span>
            </div>{/each}
        </div>{/if}
    </section>
  </section>
{/if}

<style>
  .list-row > span:last-child {
    flex: 0 0 auto;
    color: var(--text-muted);
    font-weight: 650;
  }
</style>
