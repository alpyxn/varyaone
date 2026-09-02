<script lang="ts">
  import { Building2, Check, ChevronsUpDown, Loader2, Plus, Search } from '@lucide/svelte';
  import type { Session } from '$lib/api';

  let {
    session,
    onchange,
    oncreate
  }: {
    session: Session | null;
    onchange: (companyID: string) => void | Promise<void>;
    oncreate?: () => void;
  } = $props();

  let open = $state(false);
  let query = $state('');
  let switching = $state('');
  let wrap = $state<HTMLDivElement>();
  let searchInput = $state<HTMLInputElement>();

  const companies = $derived(session?.companies ?? []);
  const current = $derived(
    companies.find((company) => company.id === session?.current_company_id) ?? null
  );
  const showSearch = $derived(companies.length > 7);
  const filtered = $derived(
    query.trim()
      ? companies.filter((company) => {
          const needle = query.trim().toLocaleLowerCase('tr');
          return (
            company.trade_name.toLocaleLowerCase('tr').includes(needle) ||
            company.legal_name.toLocaleLowerCase('tr').includes(needle) ||
            (company.tax_number ?? '').includes(needle)
          );
        })
      : companies
  );

  function initials(name: string) {
    return name
      .split(/\s+/)
      .filter(Boolean)
      .slice(0, 2)
      .map((part) => part[0]?.toLocaleUpperCase('tr') ?? '')
      .join('');
  }

  function toggle() {
    open = !open;
    if (open) {
      query = '';
      queueMicrotask(() => searchInput?.focus());
    }
  }

  async function choose(companyID: string) {
    if (companyID === session?.current_company_id) {
      open = false;
      return;
    }
    switching = companyID;
    try {
      await onchange(companyID);
    } finally {
      switching = '';
      open = false;
    }
  }

  function onWindowPointer(event: PointerEvent) {
    if (open && wrap && !wrap.contains(event.target as Node)) open = false;
  }

  function onWindowKey(event: KeyboardEvent) {
    if (open && event.key === 'Escape') open = false;
  }
</script>

<svelte:window onpointerdown={onWindowPointer} onkeydown={onWindowKey} />

<div class="cs-root" bind:this={wrap}>
  <button
    type="button"
    class="company-switcher-wrap cs-trigger"
    aria-haspopup="listbox"
    aria-expanded={open}
    aria-label="Aktif şirket"
    disabled={!session}
    onclick={toggle}
  >
    <Building2 size={16} aria-hidden="true" />
    <span class="cs-label">
      {#if session}
        {current?.trade_name ?? 'Şirket seçin'}
      {:else}
        Şirket oturumu yok
      {/if}
    </span>
    <ChevronsUpDown size={14} aria-hidden="true" />
  </button>

  {#if open && session}
    <div class="cs-pop" role="listbox" aria-label="Şirketler">
      {#if showSearch}
        <div class="cs-search">
          <Search size={14} aria-hidden="true" />
          <input
            bind:this={searchInput}
            bind:value={query}
            type="text"
            placeholder="Şirket ara"
            autocomplete="off"
            spellcheck="false"
          />
        </div>
      {/if}

      <div class="cs-list">
        {#each filtered as company (company.id)}
          {@const active = company.id === session.current_company_id}
          <button
            type="button"
            role="option"
            aria-selected={active}
            class="cs-item"
            class:active
            disabled={!!switching}
            onclick={() => choose(company.id)}
          >
            <span class="cs-avatar" aria-hidden="true">{initials(company.trade_name)}</span>
            <span class="cs-item-text">
              <span class="cs-item-name">{company.trade_name}</span>
              {#if company.legal_name && company.legal_name !== company.trade_name}
                <span class="cs-item-sub">{company.legal_name}</span>
              {:else if company.tax_number}
                <span class="cs-item-sub">VKN {company.tax_number}</span>
              {/if}
            </span>
            {#if switching === company.id}
              <Loader2 size={15} class="cs-spin" aria-hidden="true" />
            {:else if active}
              <Check size={15} aria-hidden="true" />
            {/if}
          </button>
        {/each}
        {#if filtered.length === 0}
          <p class="cs-empty">Eşleşen şirket yok</p>
        {/if}
      </div>

      {#if session.is_instance_owner && oncreate}
        <button
          type="button"
          class="cs-add"
          onclick={() => {
            open = false;
            oncreate?.();
          }}
        >
          <Plus size={15} aria-hidden="true" />
          <span>Yeni firma ekle</span>
        </button>
      {/if}
    </div>
  {/if}
</div>

<style>
  .cs-root {
    position: relative;
    min-width: 0;
    display: flex;
  }
  .cs-trigger {
    flex: 1;
    min-width: 0;
    appearance: none;
    font: inherit;
    cursor: pointer;
    color: var(--text);
  }
  .cs-trigger:disabled {
    cursor: default;
    opacity: 0.7;
  }
  .cs-trigger:hover:not(:disabled) {
    background: var(--surface-muted);
  }
  .cs-label {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    text-align: left;
    font-weight: 650;
    font-size: 12px;
  }
  .cs-pop {
    position: absolute;
    top: calc(100% + 6px);
    left: 0;
    z-index: 40;
    width: max(260px, 100%);
    max-width: 340px;
    display: flex;
    flex-direction: column;
    padding: 5px;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-panel);
    background: var(--surface);
    box-shadow: 0 12px 34px rgb(2 6 23 / 18%);
  }
  .cs-search {
    display: flex;
    align-items: center;
    gap: 7px;
    padding: 6px 8px;
    margin-bottom: 4px;
    border-bottom: 1px solid var(--border);
    color: var(--text-muted);
  }
  .cs-search input {
    flex: 1;
    min-width: 0;
    border: 0;
    outline: 0;
    background: transparent;
    color: var(--text);
    font-size: 12px;
  }
  .cs-list {
    display: flex;
    flex-direction: column;
    gap: 2px;
    max-height: 320px;
    overflow-y: auto;
  }
  .cs-item {
    display: flex;
    align-items: center;
    gap: 9px;
    width: 100%;
    padding: 7px 8px;
    border: 0;
    border-radius: 7px;
    background: transparent;
    color: var(--text);
    text-align: left;
    cursor: pointer;
  }
  .cs-item:hover:not(:disabled),
  .cs-item:focus-visible {
    background: var(--surface-muted);
  }
  .cs-item:disabled {
    cursor: progress;
  }
  .cs-item.active {
    background: var(--surface-muted);
  }
  .cs-item.active .cs-item-name {
    color: var(--primary);
  }
  .cs-avatar {
    flex-shrink: 0;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 26px;
    height: 26px;
    border-radius: 7px;
    background: var(--surface-subtle);
    color: var(--text-muted);
    font-size: 10px;
    font-weight: 700;
    letter-spacing: 0.02em;
  }
  .cs-item.active .cs-avatar {
    background: var(--primary-soft);
    color: var(--primary);
  }
  .cs-item-text {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 1px;
  }
  .cs-item-name {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-size: 12px;
    font-weight: 600;
  }
  .cs-item-sub {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-size: 11px;
    color: var(--text-muted);
  }
  .cs-empty {
    margin: 0;
    padding: 12px 8px;
    text-align: center;
    font-size: 12px;
    color: var(--text-muted);
  }
  .cs-add {
    display: flex;
    align-items: center;
    gap: 8px;
    width: 100%;
    margin-top: 4px;
    padding: 8px;
    border: 0;
    border-top: 1px solid var(--border);
    border-radius: 0 0 6px 6px;
    background: transparent;
    color: var(--text-muted);
    font-size: 12px;
    font-weight: 600;
    text-align: left;
    cursor: pointer;
  }
  .cs-add:hover {
    color: var(--primary);
  }
  :global(.cs-spin) {
    animation: cs-spin 0.7s linear infinite;
  }
  @keyframes cs-spin {
    to {
      transform: rotate(360deg);
    }
  }
</style>
