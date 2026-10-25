<script lang="ts">
  import { goto } from '$app/navigation';
  import { page } from '$app/state';
  import { ArrowLeft, Save } from '@lucide/svelte';
  import { onMount } from 'svelte';
  import { Button } from '$lib/components/ui/button';
  import { toast } from 'svelte-sonner';
  import PartyForm from '$lib/features/parties/PartyForm.svelte';
  import { ErrorSummary } from '$lib/components/varya/status';
  import { createParty } from '$lib/features/parties/api';
  import { convertLead, getLead } from '$lib/features/crm/api';
  import type { Lead } from '$lib/features/crm/types';
  import {
    TURKEY_PROVINCES,
    emptyParty,
    isPartyProvinceValidationMessage,
    normalizePartyInput,
    partyProvinceSelectionRequired,
    validatePartyInput
  } from '$lib/features/parties/types';
  let form = $state(emptyParty());
  // Opened from a CRM lead ("Cariye dönüştür"): the form starts from the lead
  // and the lead is closed as converted once the card is saved.
  let lead = $state<Lead | null>(null);

  async function prefillFromLead(leadID: string) {
    try {
      lead = await getLead(leadID);
    } catch {
      return;
    }
    const next = emptyParty();
    next.kind = 'ORGANIZATION';
    next.is_customer = true;
    next.display_name = lead.display_name;
    next.legal_name = lead.display_name;
    next.sales_rep_user_id = lead.owner_user_id ?? '';
    next.contacts = [
      {
        full_name: lead.contact_name,
        title: lead.contact_title,
        department: '',
        email: lead.email,
        phone: lead.phone || lead.phone2,
        is_primary: true
      }
    ];
    const province = TURKEY_PROVINCES.find(
      (p) => p.name.toLocaleLowerCase('tr') === lead?.city.trim().toLocaleLowerCase('tr')
    );
    if (province) {
      next.addresses = [
        {
          ...next.addresses[0],
          city: province.name,
          province_id: province.id,
          province_name: province.name
        }
      ];
    }
    if (!lead.contact_name && !lead.email && !lead.phone && !lead.phone2) next.contacts = [];
    form = next;
  }
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
      const created = await createParty(normalized);
      toast.success('Cari kartı oluşturuldu.');
      if (lead) {
        try {
          await convertLead(lead.id, created.id);
          toast.success('Aday cariye dönüştürüldü.');
        } catch {
          toast.error('Cari açıldı ama aday dönüştürülemedi; aday kartından tekrar deneyin.');
        }
        await goto(`/cari/kartlar/${created.id}`);
        return;
      }
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
    const leadID = page.url.searchParams.get('aday');
    if (leadID) void prefillFromLead(leadID);
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
    {#if lead}
      <p class="lead-note">
        “{lead.display_name}” adayından oluşturuluyor; kaydedince aday bu cariye bağlanır.
      </p>
    {/if}
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
  .lead-note {
    margin: 4px 0 0;
    color: var(--text-muted);
    font-size: 12px;
  }
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
