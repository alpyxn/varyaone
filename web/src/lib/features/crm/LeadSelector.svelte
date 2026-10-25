<script lang="ts">
  import { untrack } from 'svelte';
  import { LoaderCircle } from '@lucide/svelte';
  import { APIRequestError } from '$lib/api';
  import { Input } from '$lib/components/ui/input';
  import {
    describeAddResult,
    leadPriorities,
    priorityLabel,
    type AddLeadsResult,
    type LeadFilterInput,
    type Member
  } from './types';

  // Picks leads for a call list by filter and keeps a live count of what
  // would go on the list and why the rest would not.
  let {
    filter = $bindable(),
    includeCalled = $bindable(false),
    members = [],
    preview
  }: {
    filter: LeadFilterInput;
    includeCalled?: boolean;
    members?: Member[];
    preview: (filter: LeadFilterInput, includeCalled: boolean) => Promise<AddLeadsResult>;
  } = $props();

  const id = $props.id();
  const statusChoices = [
    { value: 'NEW', label: 'Yeni' },
    { value: 'CONTACTED', label: 'Görüşüldü' },
    { value: 'QUALIFIED', label: 'İlgili' }
  ];
  const priorityChoices = [...leadPriorities, 'NONE'] as const;

  let result = $state<AddLeadsResult | null>(null);
  let counting = $state(false);
  let error = $state('');
  let timer: ReturnType<typeof setTimeout> | undefined;

  const selectedPriorities = $derived((filter.priority ?? '').split(',').filter(Boolean));
  const selectedStatuses = $derived((filter.status ?? '').split(',').filter(Boolean));

  function toggle(list: string[], value: string, on: boolean) {
    const next = on ? [...new Set([...list, value])] : list.filter((v) => v !== value);
    return next.join(',');
  }

  async function count() {
    counting = true;
    error = '';
    try {
      result = await preview($state.snapshot(filter), includeCalled);
    } catch (cause) {
      result = null;
      error = cause instanceof APIRequestError ? cause.message : 'Sayılamadı.';
    } finally {
      counting = false;
    }
  }

  // Re-count shortly after the selection settles.
  $effect(() => {
    JSON.stringify(filter);
    void includeCalled;
    clearTimeout(timer);
    timer = setTimeout(() => untrack(() => void count()), 350);
    return () => clearTimeout(timer);
  });
</script>

<div class="selector">
  <fieldset>
    <legend>Öncelik</legend>
    <div class="chips">
      {#each priorityChoices as priority (priority)}
        <label class="chip" class:on={selectedPriorities.includes(priority)}>
          <input
            type="checkbox"
            checked={selectedPriorities.includes(priority)}
            onchange={(e) =>
              (filter.priority = toggle(selectedPriorities, priority, e.currentTarget.checked))}
          />
          {priority === 'NONE' ? 'Derecesiz' : priorityLabel(priority)}
        </label>
      {/each}
    </div>
    <small>Hiçbiri seçilmezse tüm öncelikler.</small>
  </fieldset>

  <fieldset>
    <legend>Durum</legend>
    <div class="chips">
      {#each statusChoices as status (status.value)}
        <label class="chip" class:on={selectedStatuses.includes(status.value)}>
          <input
            type="checkbox"
            checked={selectedStatuses.includes(status.value)}
            onchange={(e) =>
              (filter.status = toggle(selectedStatuses, status.value, e.currentTarget.checked))}
          />
          {status.label}
        </label>
      {/each}
    </div>
    <small>Elenen, aranmasın ve cariye dönüşen adaylar hiçbir zaman eklenmez.</small>
  </fieldset>

  <div class="fields">
    <label for="{id}-city">İl <Input id="{id}-city" bind:value={filter.city} /></label>
    <label for="{id}-district">İlçe <Input id="{id}-district" bind:value={filter.district} /></label
    >
    <label for="{id}-source">Kaynak <Input id="{id}-source" bind:value={filter.source} /></label>
    <label for="{id}-owner">
      Sorumlu
      <select id="{id}-owner" class="select" bind:value={filter.owner}>
        <option value="">Hepsi</option>
        <option value="none">Atanmamış (ortak havuz)</option>
        {#each members as member (member.user_id)}
          <option value={member.user_id}>{member.display_name}</option>
        {/each}
      </select>
    </label>
    <label for="{id}-q">Ad / telefon içinde <Input id="{id}-q" bind:value={filter.q} /></label>
  </div>

  <div class="checks">
    <label>
      <input type="checkbox" bind:checked={filter.never_called} />
      Yalnızca hiç aranmamış adaylar
    </label>
    <label class="deliberate">
      <input type="checkbox" bind:checked={includeCalled} />
      Daha önce aranmış adayları da bilerek ekle
    </label>
  </div>

  <p class="count" role="status" aria-live="polite">
    {#if counting && !result}
      <LoaderCircle size={14} aria-hidden="true" /> Sayılıyor…
    {:else if error}
      <span class="error">{error}</span>
    {:else if result}
      <strong>{describeAddResult(result, true)}</strong>
    {/if}
  </p>
</div>

<style>
  .selector {
    display: grid;
    gap: 12px;
  }
  fieldset {
    margin: 0;
    padding: 0;
    border: 0;
    display: grid;
    gap: 6px;
  }
  legend {
    margin-bottom: 6px;
    font-size: 12px;
    font-weight: 600;
    color: var(--text-muted);
  }
  small {
    color: var(--text-muted);
    font-size: 11px;
  }
  .chips {
    display: flex;
    gap: 6px;
    flex-wrap: wrap;
  }
  .chip {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    height: 28px;
    padding: 0 10px;
    border: 1px solid var(--border-strong);
    border-radius: 999px;
    font-size: 12px;
    cursor: pointer;
  }
  .chip input {
    margin: 0;
  }
  .chip.on {
    border-color: var(--primary);
    background: color-mix(in srgb, var(--primary) 12%, transparent);
    color: var(--primary);
    font-weight: 600;
  }
  .fields {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(min(100%, 180px), 1fr));
    gap: 10px 12px;
  }
  .fields label {
    display: grid;
    gap: 4px;
    font-size: 12px;
    color: var(--text-muted);
  }
  .fields :global(input) {
    width: 100%;
    box-sizing: border-box;
  }
  .select {
    height: var(--control-height);
    width: 100%;
    box-sizing: border-box;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    background: var(--surface);
    color: var(--text);
    padding: 0 10px;
    font-size: 13px;
  }
  .checks {
    display: grid;
    gap: 6px;
    font-size: 13px;
  }
  .checks label {
    display: flex;
    align-items: center;
    gap: 8px;
    cursor: pointer;
  }
  .deliberate {
    color: var(--warning);
  }
  .count {
    display: flex;
    align-items: center;
    gap: 6px;
    margin: 0;
    padding: 10px 12px;
    border-radius: var(--radius-control);
    background: color-mix(in srgb, var(--primary) 8%, var(--surface));
    font-size: 13px;
  }
  .error {
    color: var(--danger);
  }
</style>
