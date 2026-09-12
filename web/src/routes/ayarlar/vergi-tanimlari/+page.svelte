<script lang="ts">
  import { goto } from '$app/navigation';
  import { onMount } from 'svelte';
  import { api, type Session } from '$lib/api';
  import { formatQuantity } from '$lib/design/formatters';
  import {
    createTaxDefinition,
    listTaxDefinitions,
    updateTaxDefinition,
    deactivateTaxDefinition,
    activateTaxDefinition
  } from '$lib/features/taxes/api';
  import ConfirmDialog from '$lib/components/varya/confirm-dialog/ConfirmDialog.svelte';
  import type { TaxDefinition } from '$lib/features/taxes/types';

  let session = $state<Session | null>(null);
  let definitions = $state<TaxDefinition[]>([]);
  let loading = $state(true);
  let saving = $state(false);
  let editing = $state<TaxDefinition | null>(null);
  let statusTarget = $state<TaxDefinition | null>(null);
  let confirmOpen = $state(false);
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
      const updated = editing
        ? await updateTaxDefinition(editing.id, editing.version, {
            name: definitionForm.name,
            rate: definitionForm.rate,
            calculation_type: definitionForm.calculation_type,
            description: definitionForm.description,
            source: definitionForm.source,
            source_reference: editing.source_reference,
            source_version: editing.source_version,
            metadata: editing.metadata
          })
        : await createTaxDefinition(definitionForm);
      if (editing) {
        definitions = definitions.map((item) =>
          item.id === updated.id ? { ...item, ...updated } : item
        );
      } else {
        definitions = [...definitions, updated].sort((a, b) => a.code.localeCompare(b.code, 'tr'));
      }
      message = `${updated.name} vergi tanımı ${editing ? 'güncellendi' : 'oluşturuldu'}.`;
      resetForm();
    } catch (cause) {
      error = errorMessage(cause, 'Vergi tanımı kaydedilemedi.');
    } finally {
      saving = false;
    }
  }

  function resetForm() {
    editing = null;
    definitionForm = {
      code: '',
      name: '',
      description: '',
      source: '',
      calculation_type: 'PERCENTAGE',
      rate: ''
    };
  }

  function editDefinition(definition: TaxDefinition) {
    editing = definition;
    definitionForm = {
      code: definition.code,
      name: definition.name,
      description: definition.description,
      source: definition.source,
      calculation_type: definition.calculation_type ?? 'PERCENTAGE',
      rate: definition.rate ?? ''
    };
    error = '';
    message = '';
    document.getElementById('tax-definition-name')?.focus();
  }

  async function changeDefinitionStatus() {
    if (!statusTarget || !canManage || saving) return;
    saving = true;
    try {
      const updated = await (
        statusTarget.is_active ? deactivateTaxDefinition : activateTaxDefinition
      )(statusTarget.id, statusTarget.version);
      definitions = definitions.map((item) =>
        item.id === updated.id ? { ...item, ...updated } : item
      );
      if (editing?.id === updated.id) resetForm();
      error = '';
      message = `${updated.name} vergi tanımı ${updated.is_active ? 'aktife' : 'pasife'} alındı.`;
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
      <h2 class="panel-title">{editing ? 'Vergi tanımını düzenle' : 'Yeni vergi tanımı'}</h2>
      <label class="field"
        >Kod<input
          bind:value={definitionForm.code}
          readonly={Boolean(editing)}
          maxlength="40"
          required
          disabled={!canManage || saving}
        /></label
      >
      <label class="field"
        >Ad<input
          id="tax-definition-name"
          bind:value={definitionForm.name}
          maxlength="120"
          required
          disabled={!canManage || saving}
        /></label
      >
      <label class="field"
        >Açıklama<textarea
          bind:value={definitionForm.description}
          rows="3"
          disabled={!canManage || saving}
        ></textarea></label
      >
      <label class="field"
        >Kaynak<input
          bind:value={definitionForm.source}
          placeholder="Örn. GİB"
          required
          disabled={!canManage || saving}
        /></label
      >
      <label class="field"
        >Hesaplama türü<select
          bind:value={definitionForm.calculation_type}
          disabled={!canManage || saving}
        >
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
          disabled={!canManage || saving}
        /></label
      >
      {#if editing}<p class="lead">
          Yeni oran veya birim tutarı bugünden itibaren geçerlidir. Kesinleşmiş belgeler değişmez.
        </p>{/if}
      <div class="definition-actions">
        <button class="button" type="submit" disabled={!canManage || saving}
          >{saving ? 'Kaydediliyor…' : editing ? 'Değişiklikleri kaydet' : 'Tanım oluştur'}</button
        >
        {#if editing}<button
            class="button secondary"
            type="button"
            disabled={saving}
            onclick={resetForm}>Vazgeç</button
          >{/if}
      </div>
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
              >
              <div class="definition-actions">
                <span>{definition.is_active ? 'Aktif' : 'Pasif'}</span>
                {#if canManage}
                  <button
                    class="button secondary"
                    type="button"
                    disabled={saving}
                    aria-label={`${definition.name} düzenle`}
                    onclick={() => editDefinition(definition)}>Düzenle</button
                  >
                  <button
                    class="button secondary"
                    type="button"
                    disabled={saving}
                    aria-label={`${definition.name} ${definition.is_active ? 'pasife' : 'aktife'} al`}
                    onclick={() => {
                      statusTarget = definition;
                      confirmOpen = true;
                    }}>{definition.is_active ? 'Pasife al' : 'Aktife al'}</button
                  >
                {/if}
              </div>
            </div>{/each}
        </div>{/if}
    </section>
  </section>
{/if}

<ConfirmDialog
  bind:open={confirmOpen}
  title={statusTarget?.is_active ? 'Vergi tanımını pasife al' : 'Vergi tanımını aktife al'}
  description={`${statusTarget?.name ?? 'Vergi tanımı'} ${statusTarget?.is_active ? 'pasife' : 'aktife'} alınacak. Geçmiş belgeler korunur.`}
  confirmLabel={statusTarget?.is_active ? 'Pasife al' : 'Aktife al'}
  onConfirm={changeDefinitionStatus}
/>

<style>
  .definition-actions {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 8px;
  }
  .list-row {
    flex-wrap: wrap;
    gap: 12px;
  }
  .definition-actions > span {
    flex: 0 0 auto;
    color: var(--text-muted);
    font-weight: 650;
  }
</style>
