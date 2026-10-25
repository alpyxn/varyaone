<script lang="ts">
  import { DateInput } from '$lib/components/varya/date-input';
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { LoaderCircle, Plus, X } from '@lucide/svelte';
  import { APIRequestError } from '$lib/api';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { formatDate, formatMoney } from '$lib/design/formatters';
  import { SUPPORTED_CURRENCIES } from '$lib/design/currencies';
  import { createOpportunity, listOpportunities } from './api';
  import { stageKindLabel, type Opportunity } from './types';

  let {
    leadId = '',
    partyId = '',
    defaultTitle = '',
    canEdit = false
  }: { leadId?: string; partyId?: string; defaultTitle?: string; canEdit?: boolean } = $props();

  const id = $props.id();
  let items = $state<Opportunity[]>([]);
  let loading = $state(true);
  let error = $state('');
  let creating = $state(false);
  let saving = $state(false);
  let form = $state({ title: '', amount: '', currency: 'TRY', expected_close_date: '' });

  async function load() {
    loading = true;
    error = '';
    try {
      items = (
        await listOpportunities({
          state: 'all',
          lead_id: leadId || undefined,
          party_id: partyId || undefined
        })
      ).items;
    } catch (cause) {
      error = cause instanceof APIRequestError ? cause.message : 'Fırsatlar yüklenemedi.';
    } finally {
      loading = false;
    }
  }

  function open() {
    form = { title: defaultTitle, amount: '', currency: 'TRY', expected_close_date: '' };
    error = '';
    creating = true;
  }

  async function save(event: SubmitEvent) {
    event.preventDefault();
    if (saving) return;
    if (!form.title.trim()) {
      error = 'Fırsat başlığı girin.';
      return;
    }
    saving = true;
    error = '';
    try {
      const created = await createOpportunity({
        title: form.title.trim(),
        lead_id: leadId || undefined,
        party_id: partyId || undefined,
        amount: form.amount.trim() || '0',
        currency: form.currency,
        expected_close_date: form.expected_close_date,
        owner_user_id: '',
        notes: ''
      });
      creating = false;
      await goto(`/crm/firsatlar/${created.id}`);
    } catch (cause) {
      error = cause instanceof APIRequestError ? cause.message : 'Fırsat oluşturulamadı.';
    } finally {
      saving = false;
    }
  }

  onMount(load);
</script>

<section class="panel" aria-labelledby="{id}-title">
  <div class="head">
    <h2 id="{id}-title">Fırsatlar</h2>
    {#if canEdit && !creating}
      <Button variant="outline" size="sm" onclick={open}>
        <Plus size={13} data-icon="inline-start" aria-hidden="true" />Fırsat
      </Button>
    {/if}
  </div>
  {#if creating}
    <form class="quick" onsubmit={save}>
      <Input
        bind:value={form.title}
        maxlength={200}
        placeholder="Ne satılacak?"
        aria-label="Başlık"
      />
      <div class="row">
        <Input
          bind:value={form.amount}
          inputmode="decimal"
          placeholder="Tutar"
          aria-label="Tutar"
        />
        <select class="select" bind:value={form.currency} aria-label="Para birimi">
          {#each SUPPORTED_CURRENCIES as currency (currency.code)}
            <option value={currency.code}>{currency.code}</option>
          {/each}
        </select>
        <DateInput bind:value={form.expected_close_date} ariaLabel="Beklenen kapanış tarihi" />
      </div>
      <div class="foot">
        <Button variant="ghost" type="button" size="sm" onclick={() => (creating = false)}>
          <X size={13} data-icon="inline-start" aria-hidden="true" />Vazgeç
        </Button>
        <Button type="submit" size="sm" disabled={saving}>
          {#if saving}<LoaderCircle data-icon="inline-start" aria-hidden="true" />{/if}Oluştur
        </Button>
      </div>
    </form>
  {/if}
  {#if error}<p class="notice error" role="alert">{error}</p>{/if}
  {#if loading}
    <p class="state" role="status">Yükleniyor…</p>
  {:else if !items.length}
    <p class="state">Fırsat yok.</p>
  {:else}
    <ul>
      {#each items as item (item.id)}
        <li>
          <a href="/crm/firsatlar/{item.id}">{item.title}</a>
          <span class="stage kind-{item.stage_kind.toLowerCase()}">
            {item.stage_kind === 'OPEN' ? item.stage_name : stageKindLabel(item.stage_kind)}
          </span>
          <span class="amount">{formatMoney(item.amount, item.currency)}</span>
          {#if item.expected_close_date && item.stage_kind === 'OPEN'}
            <span class="muted">{formatDate(item.expected_close_date)}</span>
          {/if}
        </li>
      {/each}
    </ul>
  {/if}
</section>

<style>
  .panel {
    margin-top: 16px;
    padding: 14px 16px;
  }
  .head {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 8px;
    margin-bottom: 8px;
  }
  .head h2 {
    margin: 0;
    font-size: 14px;
  }
  .quick {
    display: grid;
    gap: 8px;
    margin-bottom: 10px;
  }
  .row {
    display: grid;
    grid-template-columns: minmax(0, 2fr) minmax(0, 1fr) minmax(0, 2fr);
    gap: 8px;
  }
  .foot {
    display: flex;
    justify-content: flex-end;
    gap: 6px;
  }
  .select {
    height: var(--control-height);
    width: 100%;
    box-sizing: border-box;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    background: var(--surface);
    color: var(--text);
    padding: 0 8px;
    font-size: 13px;
  }
  ul {
    list-style: none;
    margin: 0;
    padding: 0;
  }
  li {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: 8px;
    padding: 8px 0;
    border-bottom: 1px solid var(--border);
    font-size: 13px;
  }
  li a {
    color: var(--text);
    font-weight: 600;
  }
  .stage {
    padding: 1px 7px;
    border-radius: 999px;
    font-size: 11px;
    background: color-mix(in srgb, var(--primary) 12%, transparent);
    color: var(--primary);
  }
  .stage.kind-won {
    background: color-mix(in srgb, var(--success) 16%, transparent);
    color: var(--success);
  }
  .stage.kind-lost {
    background: color-mix(in srgb, var(--danger) 14%, transparent);
    color: var(--danger);
  }
  .amount {
    margin-left: auto;
    font-variant-numeric: tabular-nums;
  }
  .muted {
    color: var(--text-muted);
    font-size: 12px;
  }
  .state {
    margin: 0;
    padding: 8px 0;
    color: var(--text-muted);
    font-size: 13px;
  }
</style>
