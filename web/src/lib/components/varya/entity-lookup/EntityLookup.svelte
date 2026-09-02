<script lang="ts" generics="T extends { id: string }">
  import { Search, X } from '@lucide/svelte';
  import { Input } from '$lib/components/ui/input';
  let {
    value = $bindable(),
    query = $bindable(''),
    results = [],
    label,
    placeholder = 'Ara…',
    display,
    onSelect
  }: {
    value?: string;
    query?: string;
    results?: T[];
    label: string;
    placeholder?: string;
    display: (item: T) => string;
    onSelect?: (item: T) => void;
  } = $props();
  let open = $state(false);
  const selected = $derived(results.find((item) => item.id === value));
</script>

<div class="lookup">
  <span class="lookup-label">{label}</span>
  <div class="control">
    <span class="search-icon"><Search size={15} /></span><Input
      value={selected ? display(selected) : query}
      class="lookup-input"
      aria-label={label}
      onfocus={() => (open = true)}
      oninput={(event) => {
        query = event.currentTarget.value;
        value = undefined;
        open = true;
      }}
      {placeholder}
      aria-autocomplete="list"
    />{#if value}<button
        aria-label="Seçimi temizle"
        onclick={() => {
          value = undefined;
          query = '';
        }}><X size={14} /></button
      >{/if}
  </div>
  {#if open && results.length}<div class="results" role="listbox">
      {#each results as item}<button
          role="option"
          aria-selected={item.id === value}
          onclick={() => {
            value = item.id;
            query = display(item);
            open = false;
            onSelect?.(item);
          }}>{display(item)}</button
        >{/each}
    </div>{/if}
</div>

<style>
  .lookup {
    position: relative;
  }
  .lookup-label {
    display: block;
    margin-bottom: 4px;
    color: var(--text-subtle);
    font-size: 11px;
    font-weight: 650;
  }
  .control {
    position: relative;
    display: flex;
    align-items: center;
  }
  .search-icon {
    position: absolute;
    z-index: 1;
    left: 8px;
    color: var(--text-muted);
  }
  .control :global(.lookup-input) {
    padding-left: 29px;
  }
  .control button {
    position: absolute;
    right: 4px;
    border: 0;
    background: transparent;
  }
  .results {
    position: absolute;
    z-index: 30;
    top: calc(100% + 3px);
    left: 0;
    right: 0;
    max-height: 240px;
    overflow: auto;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    background: var(--surface);
    box-shadow: 0 12px 30px rgb(10 30 27 / 14%);
    padding: 4px;
  }
  .results button {
    width: 100%;
    min-height: 32px;
    border: 0;
    border-radius: 4px;
    background: transparent;
    padding: 5px 7px;
    text-align: left;
    font-size: 12px;
  }
  .results button:hover {
    background: var(--primary-soft);
  }
</style>
