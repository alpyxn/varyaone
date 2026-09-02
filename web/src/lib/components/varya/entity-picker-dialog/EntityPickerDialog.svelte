<script module lang="ts">
  let pickerSequence = 0;
</script>

<script lang="ts" generics="T extends import('./types').EntityOption">
  import { Check, ChevronDown, LoaderCircle, Search, X } from '@lucide/svelte';
  import {
    entityMetaText,
    entitySearchText,
    extractEntityOptions,
    type EntitySearchHandler,
    type EntityOption
  } from './types';

  const fallbackPickerID = `entity-picker-${++pickerSequence}`;

  let {
    open = $bindable(false),
    selected = $bindable(),
    id = fallbackPickerID,
    results = [],
    endpoint,
    endpointQueryParam = 'q',
    onSearch,
    onSelect,
    title = 'Kart seç',
    description = 'Aramak istediğiniz kartı seçin.',
    triggerLabel = 'Kart seçici',
    triggerPlaceholder = 'Kart seçin',
    showSelectedSubtitle = true,
    searchPlaceholder = 'Kod, ad veya barkod ara…',
    emptyText = 'Eşleşen kart bulunamadı.',
    initialEmptyText = 'Aramak için bir kelime yazın veya listeden seçim yapın.',
    loadingText = 'Kartlar yükleniyor…',
    errorText = 'Kartlar yüklenirken bir hata oluştu.',
    retryText = 'Tekrar dene',
    cancelText = 'Vazgeç',
    resultLabel = 'Kart sonuçları',
    disabled = false,
    loading = false,
    error,
    clearable = false,
    clearText = 'Seçimi temizle',
    onClear,
    debounceMs = 220,
    minQueryLength = 0,
    hideTrigger = false
  }: {
    open?: boolean;
    selected?: T | null;
    id?: string;
    results?: readonly T[];
    endpoint?: string;
    endpointQueryParam?: string;
    onSearch?: EntitySearchHandler<T>;
    onSelect?: (option: T) => void;
    title?: string;
    description?: string;
    triggerLabel?: string;
    triggerPlaceholder?: string;
    showSelectedSubtitle?: boolean;
    searchPlaceholder?: string;
    emptyText?: string;
    initialEmptyText?: string;
    loadingText?: string;
    errorText?: string;
    retryText?: string;
    cancelText?: string;
    resultLabel?: string;
    disabled?: boolean;
    loading?: boolean;
    error?: string | null;
    clearable?: boolean;
    clearText?: string;
    onClear?: () => void;
    debounceMs?: number;
    minQueryLength?: number;
    hideTrigger?: boolean;
  } = $props();

  let query = $state('');
  let activeIndex = $state(0);
  let resolvedResults = $state<readonly T[] | null>(null);
  let searching = $state(false);
  let searchError = $state('');
  let retryToken = $state(0);
  let requestSequence = 0;
  let activeAbortController: AbortController | undefined;
  let searchInput = $state<HTMLInputElement>();
  let dialogElement = $state<HTMLDivElement>();
  let triggerButton = $state<HTMLButtonElement>();
  let restoreFocusElement: HTMLElement | null = null;

  const dialogTitleID = $derived(`${id}-title`);
  const dialogDescriptionID = $derived(`${id}-description`);
  const resultsID = $derived(`${id}-results`);

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

  $effect(() => {
    if (activeIndex >= visibleResults.length) activeIndex = Math.max(0, visibleResults.length - 1);
  });

  $effect(() => {
    const currentId = activeOption ? resultId(activeIndex) : '';
    if (!open || !currentId || typeof document === 'undefined') return;

    const scrollTimer = setTimeout(() => {
      document.getElementById(currentId)?.scrollIntoView({ block: 'nearest' });
    }, 0);
    return () => clearTimeout(scrollTimer);
  });

  $effect(() => {
    if (!open) {
      query = '';
      activeIndex = 0;
      resolvedResults = null;
      searchError = '';
      searching = false;
      activeAbortController?.abort();
      if (restoreFocusElement && document.contains(restoreFocusElement)) {
        restoreFocusElement.focus();
      }
      restoreFocusElement = null;
      return;
    }

    restoreFocusElement =
      document.activeElement instanceof HTMLElement
        ? document.activeElement
        : (triggerButton ?? null);
    const focusTimer = setTimeout(() => searchInput?.focus(), 0);
    return () => clearTimeout(focusTimer);
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
      let payload;
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

  function resultId(index: number) {
    return `${resultsID}-result-${index}`;
  }

  function selectOption(option: T) {
    selected = option;
    open = false;
    onSelect?.(option);
    // A parent selection handler can synchronously rerender the trigger while
    // the option click is still bubbling. Close once more after that update so
    // the same click cannot leave the picker open on the newly selected item.
    setTimeout(() => (open = false), 0);
  }

  function clearQuery() {
    query = '';
    activeIndex = 0;
    searchError = '';
  }

  function clearSelection() {
    selected = null;
    onClear?.();
    open = false;
  }

  function retrySearch() {
    searchError = '';
    retryToken += 1;
  }

  function handleInputKeydown(event: KeyboardEvent) {
    if (!visibleResults.length) return;

    if (event.key === 'ArrowDown') {
      event.preventDefault();
      activeIndex = Math.min(visibleResults.length - 1, activeIndex + 1);
    } else if (event.key === 'ArrowUp') {
      event.preventDefault();
      activeIndex = Math.max(0, activeIndex - 1);
    } else if (event.key === 'Home') {
      event.preventDefault();
      activeIndex = 0;
    } else if (event.key === 'End') {
      event.preventDefault();
      activeIndex = visibleResults.length - 1;
    } else if (event.key === 'Enter' && activeOption) {
      event.preventDefault();
      selectOption(activeOption);
    }
  }

  function handleDialogKeydown(event: KeyboardEvent) {
    if (open && event.key === 'Escape') {
      event.preventDefault();
      open = false;
      return;
    }
    if (!open || event.key !== 'Tab' || !dialogElement) return;
    const focusable = Array.from(
      dialogElement.querySelectorAll<HTMLElement>(
        'button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [href], [tabindex]:not([tabindex="-1"])'
      )
    );
    if (focusable.length === 0) {
      event.preventDefault();
      dialogElement.focus();
      return;
    }
    const first = focusable[0];
    const last = focusable[focusable.length - 1];
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  }
</script>

<svelte:window onkeydown={handleDialogKeydown} />

{#if !hideTrigger}
  <button
    bind:this={triggerButton}
    class="entity-picker-trigger"
    type="button"
    {id}
    {disabled}
    aria-label={selected ? `${triggerLabel}: ${selected.title}` : triggerLabel}
    aria-haspopup="dialog"
    aria-expanded={open}
    onclick={() => (open = true)}
  >
    <Search size={16} aria-hidden="true" />
    <span class="trigger-copy">
      {#if selected}
        <strong>{selected.title}</strong>
        {#if showSelectedSubtitle && selected.subtitle}<small>{selected.subtitle}</small>{/if}
      {:else}
        <span class="placeholder">{triggerPlaceholder}</span>
      {/if}
    </span>
    <ChevronDown class="trigger-chevron" size={16} aria-hidden="true" />
  </button>
{/if}

{#if open}
  <div
    class="entity-picker-overlay"
    role="presentation"
    onclick={(event) => {
      if (event.target === event.currentTarget) open = false;
    }}
  >
    <div
      bind:this={dialogElement}
      class="entity-picker-dialog"
      role="dialog"
      aria-modal="true"
      aria-labelledby={dialogTitleID}
      aria-describedby={dialogDescriptionID}
      tabindex="-1"
      onkeydown={handleDialogKeydown}
    >
      <div class="dialog-heading">
        <div>
          <h2 id={dialogTitleID} class="dialog-title">{title}</h2>
          <p id={dialogDescriptionID} class="dialog-description">{description}</p>
        </div>
        <button class="dialog-close" type="button" aria-label="Kapat" onclick={() => (open = false)}
          ><X size={18} /></button
        >
      </div>

      {#if selected}
        <div class="selected-summary" aria-label={`Seçili kart: ${selected.title}`}>
          <div class="selected-check"><Check size={15} aria-hidden="true" /></div>
          <div class="selected-copy">
            <span class="summary-label">Seçili kart</span>
            <strong>{selected.title}</strong>
            {#if selected.subtitle}<small>{selected.subtitle}</small>{/if}
          </div>
          {#if entityMetaText(selected.meta)}<span class="selected-meta"
              >{entityMetaText(selected.meta)}</span
            >{/if}
          {#if clearable}
            <button class="clear-selection-button" type="button" onclick={clearSelection}
              >{clearText}</button
            >
          {/if}
        </div>
      {/if}

      <div class="search-row">
        <Search size={17} aria-hidden="true" />
        <input
          bind:this={searchInput}
          bind:value={query}
          class="search-input"
          type="search"
          placeholder={searchPlaceholder}
          aria-label={searchPlaceholder}
          aria-controls={resultsID}
          aria-busy={effectiveLoading}
          aria-autocomplete="list"
          aria-activedescendant={activeOption ? resultId(activeIndex) : undefined}
          oninput={() => (activeIndex = 0)}
          onkeydown={handleInputKeydown}
        />
        {#if query}<button
            class="clear-button"
            type="button"
            aria-label="Aramayı temizle"
            onclick={clearQuery}><X size={15} /></button
          >{/if}
      </div>

      <div class="dialog-body">
        {#if effectiveError}
          <div class="state state-error" role="alert">
            <strong>Sonuçlar alınamadı</strong>
            <span>{effectiveError}</span>
            <button type="button" class="retry-button" onclick={retrySearch}>{retryText}</button>
          </div>
        {:else if effectiveLoading}
          <div class="state" role="status" aria-live="polite">
            <LoaderCircle class="spinner" size={22} aria-hidden="true" />
            <span>{loadingText}</span>
          </div>
        {:else if visibleResults.length === 0}
          <div class="state" role="status" aria-live="polite">
            <Search size={22} aria-hidden="true" />
            <strong>{query ? emptyText : initialEmptyText}</strong>
          </div>
        {:else}
          <div id={resultsID} class="result-list" role="listbox" aria-label={resultLabel}>
            {#each visibleResults as option, index}
              {@const optionId = resultId(index)}
              {@const meta = entityMetaText(option.meta)}
              <button
                id={optionId}
                class={`result-option${index === activeIndex ? ' active' : ''}${selected?.id === option.id ? ' selected' : ''}`}
                type="button"
                role="option"
                aria-selected={selected?.id === option.id}
                onclick={(event) => {
                  event.stopPropagation();
                  selectOption(option);
                }}
                onmouseenter={() => (activeIndex = index)}
              >
                <span class="result-mark">
                  {#if selected?.id === option.id}<Check size={15} aria-hidden="true" />{:else}<span
                      >{index + 1}</span
                    >{/if}
                </span>
                <span class="result-copy">
                  <strong>{option.title}</strong>
                  {#if option.subtitle}<small>{option.subtitle}</small>{/if}
                </span>
                {#if meta}<span class="result-meta">{meta}</span>{/if}
              </button>
            {/each}
          </div>
        {/if}
      </div>

      <div class="dialog-footer">
        <span>{visibleResults.length} kayıt</span>
        <button class="cancel-button" type="button" onclick={() => (open = false)}
          >{cancelText}</button
        >
      </div>
    </div>
  </div>
{/if}

<style>
  :global(.entity-picker-trigger) {
    display: flex;
    align-items: center;
    gap: 8px;
    width: 100%;
    min-height: var(--control-height);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    background: var(--surface);
    color: var(--text);
    padding: 5px 9px;
    text-align: left;
    cursor: pointer;
  }

  :global(.entity-picker-trigger:hover) {
    border-color: var(--primary);
    background: var(--surface-muted);
  }

  :global(.entity-picker-trigger:disabled) {
    cursor: not-allowed;
    opacity: 0.55;
  }

  .trigger-copy {
    min-width: 0;
    flex: 1;
  }

  .trigger-copy strong,
  .trigger-copy small {
    display: block;
    overflow-wrap: anywhere;
  }

  .trigger-copy strong {
    font-size: 12px;
  }

  .trigger-copy small {
    margin-top: 1px;
    color: var(--text-muted);
    font-size: 10px;
  }

  .placeholder {
    display: block;
    min-width: 0;
    overflow-wrap: anywhere;
    color: var(--text-muted);
    font-size: 12px;
    line-height: 1.25;
  }

  :global(.trigger-chevron) {
    flex: 0 0 auto;
    color: var(--text-muted);
  }

  :global(.entity-picker-overlay) {
    position: fixed;
    inset: 0;
    z-index: 200;
    background: rgb(2 6 23 / 54%);
  }

  :global(.entity-picker-dialog) {
    position: fixed;
    top: 10vh;
    left: 50%;
    z-index: 201;
    display: flex;
    width: min(680px, calc(100vw - 28px));
    max-height: min(760px, 80vh);
    transform: translateX(-50%);
    flex-direction: column;
    overflow: hidden;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-panel);
    background: var(--surface);
    box-shadow: 0 22px 70px rgb(10 30 27 / 25%);
    color: var(--text);
  }

  .dialog-heading {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 16px;
    padding: 18px 18px 13px;
  }

  :global(.dialog-title) {
    margin: 0;
    font-size: 16px;
    font-weight: 750;
  }

  :global(.dialog-description) {
    margin: 4px 0 0;
    color: var(--text-muted);
    font-size: 12px;
  }

  :global(.dialog-close),
  .clear-button {
    display: grid;
    place-items: center;
    flex: 0 0 auto;
    width: 30px;
    height: 30px;
    border: 0;
    border-radius: 5px;
    background: transparent;
    color: var(--text-muted);
    cursor: pointer;
  }

  :global(.dialog-close:hover),
  .clear-button:hover {
    background: var(--surface-muted);
    color: var(--text);
  }

  .selected-summary {
    display: flex;
    align-items: center;
    gap: 10px;
    margin: 0 18px 12px;
    border: 1px solid var(--primary);
    border-radius: var(--radius-control);
    background: var(--primary-soft);
    padding: 9px 10px;
  }

  .selected-check,
  .result-mark {
    display: grid;
    place-items: center;
    flex: 0 0 auto;
    width: 26px;
    height: 26px;
    border-radius: 50%;
    background: var(--primary);
    color: var(--primary-foreground);
  }

  .selected-copy {
    min-width: 0;
    flex: 1;
  }

  .selected-copy strong,
  .selected-copy small,
  .summary-label {
    display: block;
  }

  .summary-label {
    margin-bottom: 2px;
    color: var(--text-muted);
    font-size: 10px;
    font-weight: 650;
    text-transform: uppercase;
    letter-spacing: 0.04em;
  }

  .selected-copy strong {
    overflow-wrap: anywhere;
    font-size: 12px;
  }

  .selected-copy small,
  .selected-meta {
    color: var(--text-muted);
    font-size: 10px;
  }

  .selected-meta {
    max-width: 35%;
    overflow-wrap: anywhere;
  }

  .clear-selection-button {
    flex: 0 0 auto;
    min-height: 30px;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    background: var(--surface);
    color: var(--text-muted);
    padding: 4px 8px;
    cursor: pointer;
  }

  .clear-selection-button:hover {
    border-color: var(--danger);
    color: var(--danger);
  }

  .search-row {
    display: flex;
    align-items: center;
    gap: 8px;
    margin: 0 18px 12px;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    background: var(--surface);
    padding: 3px 5px 3px 10px;
    color: var(--text-muted);
  }

  .search-row:focus-within {
    border-color: var(--primary);
    box-shadow: 0 0 0 3px var(--focus);
  }

  .search-input {
    min-width: 0;
    flex: 1;
    height: 34px;
    border: 0;
    outline: 0;
    background: transparent;
    color: var(--text);
    font-size: 13px;
  }

  .search-input::placeholder {
    color: var(--text-subtle);
  }

  .dialog-body {
    min-height: 190px;
    overflow: auto;
    border-top: 1px solid var(--border);
    border-bottom: 1px solid var(--border);
    padding: 8px;
  }

  .result-list {
    display: grid;
    gap: 3px;
  }

  :global(.result-option) {
    display: flex;
    align-items: center;
    gap: 10px;
    width: 100%;
    min-height: 56px;
    border: 1px solid transparent;
    border-radius: var(--radius-control);
    background: transparent;
    padding: 8px 9px;
    color: var(--text);
    text-align: left;
    cursor: pointer;
  }

  :global(.result-option:hover),
  :global(.result-option.active) {
    border-color: var(--border);
    background: var(--surface-muted);
  }

  :global(.result-option.selected) {
    border-color: var(--primary);
    background: var(--primary-soft);
  }

  .result-mark {
    width: 25px;
    height: 25px;
    background: var(--surface-subtle);
    color: var(--text-muted);
    font-size: 10px;
    font-weight: 700;
  }

  :global(.result-option.selected) .result-mark {
    background: var(--primary);
    color: var(--primary-foreground);
  }

  .result-copy {
    min-width: 0;
    flex: 1;
  }

  .result-copy strong,
  .result-copy small {
    display: block;
    overflow-wrap: anywhere;
  }

  .result-copy strong {
    font-size: 12px;
  }

  .result-copy small,
  .result-meta {
    margin-top: 2px;
    color: var(--text-muted);
    font-size: 10px;
  }

  .result-meta {
    max-width: 30%;
    overflow-wrap: anywhere;
    text-align: right;
  }

  .state {
    display: grid;
    min-height: 174px;
    place-items: center;
    align-content: center;
    gap: 8px;
    padding: 24px;
    color: var(--text-muted);
    text-align: center;
    font-size: 12px;
  }

  .state strong {
    color: var(--text);
    font-size: 12px;
  }

  .state-error strong {
    color: var(--danger);
  }

  .state-error span {
    max-width: 440px;
  }

  .retry-button,
  :global(.cancel-button) {
    min-height: 32px;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    background: var(--surface);
    padding: 0 10px;
    color: var(--text);
    font-size: 11px;
    cursor: pointer;
  }

  .retry-button:hover,
  :global(.cancel-button:hover) {
    background: var(--surface-muted);
  }

  .dialog-footer {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    padding: 10px 18px;
    color: var(--text-muted);
    font-size: 10px;
  }

  :global(.spinner) {
    animation: entity-picker-spin 0.9s linear infinite;
  }

  @keyframes entity-picker-spin {
    to {
      transform: rotate(360deg);
    }
  }

  @media (max-width: 520px) {
    :global(.entity-picker-dialog) {
      top: 5vh;
      max-height: 90vh;
    }

    .dialog-heading,
    .dialog-footer {
      padding-left: 13px;
      padding-right: 13px;
    }

    .selected-summary,
    .search-row {
      margin-left: 13px;
      margin-right: 13px;
    }

    .result-meta,
    .selected-meta {
      display: none;
    }
  }
</style>
