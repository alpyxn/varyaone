<script module lang="ts">
  let comboboxSequence = 0;
</script>

<script lang="ts" generics="T extends import('../entity-picker-dialog/types').EntityOption">
  import { ChevronDown, LoaderCircle, Search, X } from '@lucide/svelte';
  import { EntityPickerDialog } from '../entity-picker-dialog';
  import {
    entityMetaText,
    entitySearchText,
    extractEntityOptions,
    type EntityOption,
    type EntitySearchHandler
  } from '../entity-picker-dialog/types';

  const fallbackID = `entity-combobox-${++comboboxSequence}`;

  let {
    selected = $bindable(),
    open = $bindable(false),
    id = fallbackID,
    results = [],
    endpoint,
    endpointQueryParam = 'q',
    onSearch,
    onSelect,
    onClear,
    title = 'Kart seç',
    description = 'Aramak istediğiniz kartı seçin.',
    triggerLabel = 'Kart seçici',
    triggerPlaceholder = 'Aramak için yazın',
    searchPlaceholder = 'Kod, ad veya barkod ara…',
    emptyText = 'Eşleşen kayıt bulunamadı.',
    initialEmptyText = 'Aramak için yazmaya başlayın.',
    loadingText = 'Yükleniyor…',
    errorText = 'Sonuçlar alınamadı.',
    retryText = 'Tekrar dene',
    resultLabel = 'Sonuçlar',
    disabled = false,
    loading = false,
    error,
    clearable = false,
    debounceMs = 220,
    minQueryLength = 0,
    advancedSearch = false,
    advancedSearchText = 'Bulamadınız mı? Tüm kayıtlarda ara'
  }: {
    selected?: T | null;
    open?: boolean;
    id?: string;
    results?: readonly T[];
    endpoint?: string;
    endpointQueryParam?: string;
    onSearch?: EntitySearchHandler<T>;
    onSelect?: (option: T) => void;
    onClear?: () => void;
    title?: string;
    description?: string;
    triggerLabel?: string;
    triggerPlaceholder?: string;
    searchPlaceholder?: string;
    emptyText?: string;
    initialEmptyText?: string;
    loadingText?: string;
    errorText?: string;
    retryText?: string;
    resultLabel?: string;
    disabled?: boolean;
    loading?: boolean;
    error?: string | null;
    clearable?: boolean;
    debounceMs?: number;
    minQueryLength?: number;
    advancedSearch?: boolean;
    advancedSearchText?: string;
  } = $props();

  let query = $state('');
  let editing = $state(false);
  let activeIndex = $state(0);
  let resolvedResults = $state<readonly T[] | null>(null);
  let searching = $state(false);
  let searchError = $state('');
  let retryToken = $state(0);
  let requestSequence = 0;
  let activeAbortController: AbortController | undefined;
  let inputElement = $state<HTMLInputElement>();
  let rootElement = $state<HTMLDivElement>();
  let dialogOpen = $state(false);

  const listboxID = $derived(`${id}-listbox`);
  const hasRemoteSearch = $derived(Boolean(endpoint || onSearch));
  const sourceResults = $derived(resolvedResults ?? results);
  const visibleResults = $derived(
    hasRemoteSearch
      ? sourceResults
      : results.filter(
          (option) =>
            !query.trim() ||
            entitySearchText(option).includes(query.trim().toLocaleLowerCase('tr-TR'))
        )
  );
  const effectiveLoading = $derived(Boolean(loading || searching));
  const effectiveError = $derived(error || searchError);
  const activeOption = $derived(visibleResults[activeIndex]);
  const inputValue = $derived(editing || !selected ? query : selected.title);

  $effect(() => {
    if (activeIndex >= visibleResults.length) activeIndex = Math.max(0, visibleResults.length - 1);
  });

  $effect(() => {
    const shouldSearch = open && hasRemoteSearch && query.trim().length >= minQueryLength;
    retryToken;
    if (!shouldSearch) return;

    const wait = query.trim().length === 0 ? 0 : Math.max(0, debounceMs);
    const timer = setTimeout(() => void runSearch(query.trim()), wait);
    return () => {
      clearTimeout(timer);
      activeAbortController?.abort();
      requestSequence += 1;
    };
  });

  async function runSearch(searchQuery: string) {
    const requestId = ++requestSequence;
    activeAbortController?.abort();
    const controller = new AbortController();
    activeAbortController = controller;
    searching = true;
    searchError = '';
    resolvedResults = null;

    try {
      let payload: unknown;
      if (onSearch) {
        payload = await onSearch(searchQuery, controller.signal);
      } else if (endpoint) {
        const url = new URL(endpoint, globalThis.location?.origin ?? 'http://localhost');
        url.searchParams.set(endpointQueryParam, searchQuery);
        const response = await fetch(url, { signal: controller.signal });
        if (!response.ok) throw new Error(errorText);
        payload = (await response.json()) as unknown;
      }

      if (requestId !== requestSequence || controller.signal.aborted) return;
      if (payload !== undefined) resolvedResults = extractEntityOptions<T>(payload);
    } catch (cause) {
      if (controller.signal.aborted || requestId !== requestSequence) return;
      searchError = cause instanceof Error && cause.message ? cause.message : errorText;
    } finally {
      if (requestId === requestSequence) searching = false;
    }
  }

  function openList() {
    if (disabled) return;
    editing = true;
    open = true;
  }

  function closeList() {
    open = false;
    editing = false;
    query = '';
    searchError = '';
  }

  function selectOption(option: T) {
    selected = option;
    query = '';
    editing = false;
    open = false;
    dialogOpen = false;
    searchError = '';
    onSelect?.(option);
  }

  function clearSelection() {
    selected = null;
    query = '';
    activeIndex = 0;
    onClear?.();
    inputElement?.focus();
    open = true;
  }

  function retrySearch() {
    searchError = '';
    retryToken += 1;
  }

  function openDialog() {
    open = false;
    dialogOpen = true;
  }

  function handleKeydown(event: KeyboardEvent) {
    if (event.key === 'Escape') {
      if (open) {
        event.preventDefault();
        event.stopPropagation();
        closeList();
      }
      return;
    }
    if (event.key === 'ArrowDown') {
      event.preventDefault();
      open = true;
      activeIndex = Math.min(visibleResults.length - 1, activeIndex + 1);
    } else if (event.key === 'ArrowUp') {
      event.preventDefault();
      activeIndex = Math.max(0, activeIndex - 1);
    } else if (event.key === 'Enter' && open && activeOption) {
      event.preventDefault();
      selectOption(activeOption);
    } else if (event.key === 'Tab') {
      open = false;
      editing = false;
    }
  }

  function handleFocusOut() {
    setTimeout(() => {
      if (dialogOpen) return;
      if (rootElement && rootElement.contains(document.activeElement)) return;
      open = false;
      editing = false;
      query = '';
    }, 0);
  }
</script>

<div
  class="entity-combobox"
  class:is-disabled={disabled}
  bind:this={rootElement}
  onfocusout={handleFocusOut}
>
  <div class="control">
    <span class="lead-icon">
      {#if effectiveLoading}
        <LoaderCircle class="entity-combobox-spin" size={15} aria-hidden="true" />
      {:else}
        <Search size={15} aria-hidden="true" />
      {/if}
    </span>
    <input
      bind:this={inputElement}
      {id}
      {disabled}
      class="combobox-input"
      type="text"
      role="combobox"
      autocomplete="off"
      spellcheck="false"
      aria-expanded={open}
      aria-controls={listboxID}
      aria-autocomplete="list"
      aria-label={triggerLabel}
      aria-activedescendant={open && activeOption ? `${id}-option-${activeIndex}` : undefined}
      placeholder={triggerPlaceholder}
      value={inputValue}
      onfocus={openList}
      onclick={openList}
      oninput={(event) => {
        query = event.currentTarget.value;
        editing = true;
        open = true;
        activeIndex = 0;
        if (selected) selected = null;
      }}
      onkeydown={handleKeydown}
    />
    {#if selected && clearable && !disabled}
      <button type="button" class="trailing" aria-label="Seçimi temizle" onclick={clearSelection}>
        <X size={14} aria-hidden="true" />
      </button>
    {:else}
      <span class="trailing chevron"><ChevronDown size={15} aria-hidden="true" /></span>
    {/if}
  </div>

  {#if open && !disabled}
    <div class="panel">
      {#if effectiveError}
        <div class="state state-error" role="alert">
          <span>{effectiveError}</span>
          <button
            type="button"
            class="retry"
            onmousedown={(event) => event.preventDefault()}
            onclick={retrySearch}>{retryText}</button
          >
        </div>
      {:else if effectiveLoading && visibleResults.length === 0}
        <div class="state" role="status" aria-live="polite">{loadingText}</div>
      {:else if visibleResults.length === 0}
        <div class="state" role="status" aria-live="polite">
          {query ? emptyText : initialEmptyText}
        </div>
      {:else}
        <div class="list" id={listboxID} role="listbox" aria-label={resultLabel}>
          {#each visibleResults as option, index (option.id)}
            {@const meta = entityMetaText(option.meta)}
            <button
              id={`${id}-option-${index}`}
              type="button"
              role="option"
              class="option"
              class:active={index === activeIndex}
              class:selected={selected?.id === option.id}
              aria-selected={selected?.id === option.id}
              onmouseenter={() => (activeIndex = index)}
              onmousedown={(event) => event.preventDefault()}
              onclick={() => selectOption(option)}
            >
              <span class="option-copy">
                <strong>{option.title}</strong>
                {#if option.subtitle}<small>{option.subtitle}</small>{/if}
              </span>
              {#if meta}<span class="option-meta">{meta}</span>{/if}
            </button>
          {/each}
        </div>
      {/if}

      {#if advancedSearch}
        <button
          type="button"
          class="advanced"
          onmousedown={(event) => event.preventDefault()}
          onclick={openDialog}
        >
          <Search size={13} aria-hidden="true" />
          <span>{advancedSearchText}</span>
        </button>
      {/if}
    </div>
  {/if}

  {#if advancedSearch}
    <EntityPickerDialog
      bind:open={dialogOpen}
      bind:selected
      id={`${id}-dialog`}
      {results}
      {endpoint}
      {endpointQueryParam}
      {onSearch}
      {title}
      {description}
      {searchPlaceholder}
      {emptyText}
      {initialEmptyText}
      {loadingText}
      {errorText}
      {retryText}
      {resultLabel}
      {clearable}
      {debounceMs}
      {minQueryLength}
      hideTrigger
      onSelect={(option) => selectOption(option)}
      onClear={clearSelection}
    />
  {/if}
</div>

<style>
  .entity-combobox {
    position: relative;
  }

  .control {
    position: relative;
    display: flex;
    align-items: center;
  }

  .lead-icon {
    position: absolute;
    z-index: 1;
    left: 9px;
    display: grid;
    place-items: center;
    color: var(--text-muted);
    pointer-events: none;
  }

  .combobox-input {
    width: 100%;
    min-height: var(--control-height);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    background: var(--surface);
    color: var(--text);
    padding: 5px 30px 5px 30px;
    font-size: 12px;
  }

  .combobox-input:focus {
    outline: 0;
    border-color: var(--primary);
    box-shadow: 0 0 0 3px var(--focus);
  }

  .combobox-input::placeholder {
    color: var(--text-subtle);
  }

  .is-disabled .combobox-input {
    cursor: not-allowed;
    opacity: 0.55;
  }

  .trailing {
    position: absolute;
    right: 6px;
    display: grid;
    place-items: center;
    width: 20px;
    height: 20px;
    border: 0;
    border-radius: 4px;
    background: transparent;
    color: var(--text-muted);
  }

  button.trailing {
    cursor: pointer;
  }

  button.trailing:hover {
    background: var(--surface-muted);
    color: var(--text);
  }

  .chevron {
    pointer-events: none;
  }

  .panel {
    position: absolute;
    z-index: 40;
    top: calc(100% + 3px);
    left: 0;
    right: 0;
    overflow: hidden;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    background: var(--surface);
    box-shadow: 0 14px 34px rgb(10 30 27 / 16%);
  }

  .list {
    max-height: 264px;
    overflow: auto;
    padding: 4px;
  }

  .option {
    display: flex;
    align-items: center;
    gap: 10px;
    width: 100%;
    min-height: 40px;
    border: 1px solid transparent;
    border-radius: 5px;
    background: transparent;
    padding: 6px 8px;
    color: var(--text);
    text-align: left;
    cursor: pointer;
  }

  .option.active {
    background: var(--surface-muted);
  }

  .option.selected {
    border-color: var(--primary);
    background: var(--primary-soft);
  }

  .option-copy {
    min-width: 0;
    flex: 1;
  }

  .option-copy strong,
  .option-copy small {
    display: block;
    overflow-wrap: anywhere;
  }

  .option-copy strong {
    font-size: 12px;
    font-weight: 600;
  }

  .option-copy small {
    margin-top: 1px;
    color: var(--text-muted);
    font-size: 10px;
  }

  .option-meta {
    flex: 0 0 auto;
    max-width: 34%;
    overflow-wrap: anywhere;
    color: var(--text-muted);
    font-size: 10px;
    text-align: right;
  }

  .state {
    padding: 14px 12px;
    color: var(--text-muted);
    text-align: center;
    font-size: 11px;
  }

  .state-error {
    display: grid;
    gap: 8px;
    color: var(--danger);
  }

  .retry {
    justify-self: center;
    min-height: 28px;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    background: var(--surface);
    padding: 0 10px;
    color: var(--text);
    font-size: 11px;
    cursor: pointer;
  }

  .advanced {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 6px;
    width: 100%;
    border: 0;
    border-top: 1px solid var(--border);
    background: var(--surface-subtle, var(--surface-muted));
    padding: 8px 10px;
    color: var(--primary);
    font-size: 11px;
    font-weight: 600;
    cursor: pointer;
  }

  .advanced:hover {
    background: var(--primary-soft);
  }

  :global(.entity-combobox-spin) {
    animation: entity-combobox-spin 0.9s linear infinite;
  }

  @keyframes entity-combobox-spin {
    to {
      transform: rotate(360deg);
    }
  }
</style>
