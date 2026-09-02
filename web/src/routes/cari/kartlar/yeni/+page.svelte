<script lang="ts">
  import { goto } from '$app/navigation';
  import { ArrowLeft, Save } from '@lucide/svelte';
  import { onMount } from 'svelte';
  import { Button } from '$lib/components/ui/button';
  import { toast } from 'svelte-sonner';
  import PartyForm from '$lib/features/parties/PartyForm.svelte';
  import { ErrorSummary } from '$lib/components/varya/status';
  import { createParty } from '$lib/features/parties/api';
  import {
    emptyParty,
    isPartyProvinceValidationMessage,
    normalizePartyInput,
    partyProvinceSelectionRequired,
    validatePartyInput
  } from '$lib/features/parties/types';
  let form = $state(emptyParty());
  let saving = $state(false);
  let error = $state('');
  let fieldErrors = $state<Record<string, string>>({});
  function reportValidation(message: string) {
    error = message;
    const id =
      partyProvinceSelectionRequired(form) || isPartyProvinceValidationMessage(message)
        ? 'party-province-0'
        : form.kind === 'ORGANIZATION'
          ? 'party-legal-name'
          : 'party-first-name';
    fieldErrors = { [id]: message };
    activeTab = id === 'party-province-0' ? 'contact' : 'basic';
    setTimeout(() => document.getElementById(id)?.focus(), 0);
  }
  let activeTab = $state('basic');
  async function save() {
    if (saving) return;
    const normalized = normalizePartyInput(form);
    const validationMessage = validatePartyInput(normalized);
    if (validationMessage) {
      reportValidation(validationMessage);
      return;
    }
    saving = true;
    error = '';
    fieldErrors = {};
    try {
      await createParty(normalized);
      toast.success('Cari kartı oluşturuldu.');
      // Yeni kart kaydedildiği anda listeye dön; liste sayfası sunucudan
      // yeniden okuyarak kartı tabloya otomatik olarak ekler.
      await goto('/cari/kartlar');
    } catch (cause) {
      const message =
        typeof cause === 'object' && cause && 'message' in cause
          ? String(cause.message)
          : 'Cari kartı oluşturulamadı.';
      if (isPartyProvinceValidationMessage(message)) reportValidation(message);
      else error = message;
    } finally {
      saving = false;
    }
  }
  onMount(() => {
    const listener = (event: KeyboardEvent) => {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 's') {
        if (event.target instanceof Element && event.target.closest('[role="dialog"]')) return;
        event.preventDefault();
        void save();
      }
    };
    window.addEventListener('keydown', listener);
    return () => window.removeEventListener('keydown', listener);
  });
</script>

<svelte:head><title>Yeni Cari · Varya One</title></svelte:head>
<header class="page-header">
  <div>
    <a class="back" href="/cari/kartlar"><ArrowLeft size={14} />Cari Kartlar</a>
    <h1>Yeni Cari</h1>
  </div>
  <div class="page-actions">
    <a class="link-button" href="/cari/kartlar">Vazgeç</a><Button
      type="submit"
      form="new-party-form"
      disabled={saving}><Save size={15} />{saving ? 'Kaydediliyor…' : 'Kaydet'}</Button
    >
  </div>
</header>
<ErrorSummary errors={fieldErrors} />
{#if error && !Object.keys(fieldErrors).length}<div class="notice error" role="alert">
    {error}
  </div>{/if}
<form
  id="new-party-form"
  class="panel form-panel"
  novalidate
  onsubmit={(event) => {
    event.preventDefault();
    void save();
  }}
>
  <PartyForm bind:value={form} bind:activeTab disabled={saving} newRecord errors={fieldErrors} />
</form>
<p class="shortcut"><kbd>Ctrl S</kbd> Kaydet</p>

<style>
  .back {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    margin-bottom: 5px;
    color: var(--primary);
    font-size: 11px;
    text-decoration: none;
  }
  .form-panel {
    max-width: 900px;
    padding: 0;
  }
  .shortcut {
    color: var(--text-muted);
    font-size: 10.5px;
  }
  .shortcut kbd {
    border: 1px solid var(--border);
    border-radius: 4px;
    padding: 1px 4px;
    background: var(--surface);
  }
</style>
