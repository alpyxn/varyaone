<script lang="ts">
  import { Check, ChevronDown, LoaderCircle, MapPin, Search, X } from '@lucide/svelte';
  import { Badge } from '$lib/components/ui/badge';
  import { listDistricts, listTaxOfficeReferences } from '$lib/features/parties/api';
  import type { LocationOption, TaxOfficeReference } from '$lib/features/parties/types';

  let {
    open = $bindable(false),
    selectedId = '',
    selectedName = '',
    initialProvinceId = '',
    initialDistrictName = '',
    provinces = [],
    disabled = false,
    onSelect,
    onClear
  }: {
    open?: boolean;
    selectedId?: string;
    selectedName?: string;
    initialProvinceId?: string;
    initialDistrictName?: string;
    provinces?: readonly LocationOption[];
    disabled?: boolean;
    onSelect?: (reference: TaxOfficeReference) => void;
    onClear?: () => void;
  } = $props();

  let provinceID = $state('');
  let districtName = $state('');
  let districts = $state<LocationOption[]>([]);
  let query = $state('');
  let results = $state<TaxOfficeReference[]>([]);
  let activeIndex = $state(0);
  let loading = $state(false);
  let searchError = $state('');
  let districtError = $state('');
  let districtsLoading = $state(false);
  let retryToken = $state(0);
  let filterReady = $state(false);
  let wasOpen = false;
  let requestSequence = 0;
  let activeAbortController: AbortController | undefined;
  let districtAbortController: AbortController | undefined;
  let districtRequestSequence = 0;
  let searchInput = $state<HTMLInputElement>();
  let dialogElement = $state<HTMLDivElement>();
  let triggerButton = $state<HTMLButtonElement>();
  let restoreFocusElement: HTMLElement | null = null;

  const activeResult = $derived(results[activeIndex]);
  const selectedLabel = $derived(selectedName || (selectedId ? 'Seçili vergi dairesi' : ''));

  $effect(() => {
    if (activeIndex >= results.length) activeIndex = Math.max(0, results.length - 1);
  });

  $effect(() => {
    if (open && !wasOpen) {
      wasOpen = true;
      filterReady = false;
      provinceID = initialProvinceId || '';
      districtName = '';
      districts = [];
      query = '';
      results = [];
      activeIndex = 0;
      searchError = '';
      districtError = '';
      districtsLoading = false;
      restoreFocusElement =
        document.activeElement instanceof HTMLElement
          ? document.activeElement
          : (triggerButton ?? null);
      void prepareDistricts(provinceID, initialDistrictName);
      const focusTimer = setTimeout(() => searchInput?.focus(), 0);
      return () => clearTimeout(focusTimer);
    }
    if (!open && wasOpen) {
      wasOpen = false;
      filterReady = false;
      activeAbortController?.abort();
      activeAbortController = undefined;
      districtAbortController?.abort();
      districtAbortController = undefined;
      requestSequence += 1;
      districtRequestSequence += 1;
      loading = false;
      districtsLoading = false;
      results = [];
      searchError = '';
      const focusTarget = restoreFocusElement;
      restoreFocusElement = null;
      if (focusTarget && document.contains(focusTarget)) {
        const focusTimer = setTimeout(() => focusTarget.focus(), 0);
        return () => clearTimeout(focusTimer);
      }
    }
  });

  async function prepareDistricts(currentProvinceID: string, preferredName = '') {
    const requestID = ++districtRequestSequence;
    districtAbortController?.abort();
    districtAbortController = undefined;
    districts = [];
    districtError = '';
    districtName = '';

    if (!currentProvinceID) {
      districtsLoading = false;
      if (open && wasOpen && requestID === districtRequestSequence) filterReady = true;
      return;
    }

    const controller = new AbortController();
    districtAbortController = controller;
    districtsLoading = true;
    try {
      const items = await listDistricts(currentProvinceID, controller.signal);
      if (controller.signal.aborted || requestID !== districtRequestSequence) return;
      districts = items;
      const preferred = preferredName.trim().toLocaleLowerCase('tr-TR');
      districtName =
        items.find((item) => item.name.trim().toLocaleLowerCase('tr-TR') === preferred)?.name ?? '';
    } catch (cause) {
      if (controller.signal.aborted || requestID !== districtRequestSequence) return;
      districtError =
        cause instanceof Error && cause.message ? cause.message : 'İlçeler alınamadı.';
    } finally {
      if (requestID === districtRequestSequence) {
        districtsLoading = false;
        filterReady = true;
      }
    }
  }

  $effect(() => {
    const currentProvinceID = provinceID;
    const currentDistrictName = districtName;
    const currentQuery = query;
    retryToken;
    if (!open || !filterReady) return;
    const timer = setTimeout(
      () => void runSearch(currentProvinceID, currentDistrictName, currentQuery),
      currentQuery.trim() ? 220 : 0
    );
    return () => {
      clearTimeout(timer);
      activeAbortController?.abort();
      requestSequence += 1;
    };
  });

  async function runSearch(
    currentProvinceID: string,
    currentDistrictName: string,
    currentQuery: string
  ) {
    const requestID = ++requestSequence;
    activeAbortController?.abort();
    const controller = new AbortController();
    activeAbortController = controller;
    loading = true;
    searchError = '';
    results = [];
    try {
      const response = await listTaxOfficeReferences(
        {
          province_id: currentProvinceID || undefined,
          district_name: currentDistrictName.trim() || undefined,
          q: currentQuery.trim() || undefined,
          limit: 2000
        },
        controller.signal
      );
      if (requestID !== requestSequence || controller.signal.aborted) return;
      results = response.items;
      activeIndex = 0;
    } catch (cause) {
      if (controller.signal.aborted || requestID !== requestSequence) return;
      searchError =
        cause instanceof Error && cause.message ? cause.message : 'Vergi daireleri alınamadı.';
    } finally {
      if (requestID === requestSequence) loading = false;
    }
  }

  function resultID(index: number) {
    return `tax-office-result-${index}`;
  }

  function selectOption(reference: TaxOfficeReference) {
    if (!reference.is_active) return;
    onSelect?.(reference);
    open = false;
    setTimeout(() => (open = false), 0);
  }

  function clearSelection(event?: MouseEvent) {
    event?.stopPropagation();
    onClear?.();
  }

  function retrySearch() {
    searchError = '';
    retryToken += 1;
  }

  function retryDistricts() {
    void prepareDistricts(provinceID);
  }

  function handleSearchKeydown(event: KeyboardEvent) {
    if (event.key === 'ArrowDown') {
      event.preventDefault();
      activeIndex = Math.min(results.length - 1, activeIndex + 1);
    } else if (event.key === 'ArrowUp') {
      event.preventDefault();
      activeIndex = Math.max(0, activeIndex - 1);
    } else if (event.key === 'Home') {
      event.preventDefault();
      activeIndex = 0;
    } else if (event.key === 'End') {
      event.preventDefault();
      activeIndex = Math.max(0, results.length - 1);
    } else if (event.key === 'Enter' && activeResult?.is_active) {
      event.preventDefault();
      selectOption(activeResult);
    }
  }

  function handleDialogKeydown(event: KeyboardEvent) {
    if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 's') {
      event.preventDefault();
      event.stopPropagation();
      event.stopImmediatePropagation();
      return;
    }
    if (open && event.key === 'Escape') {
      event.preventDefault();
      event.stopPropagation();
      open = false;
      return;
    }
    if (!open || event.key !== 'Tab' || !dialogElement) return;
    const focusable = Array.from(
      dialogElement.querySelectorAll<HTMLElement>(
        'button:not([disabled]), input:not([disabled]), select:not([disabled]), [href], [tabindex]:not([tabindex="-1"])'
      )
    );
    if (!focusable.length) {
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

<div class="picker-control">
  <button
    bind:this={triggerButton}
    class="picker-trigger"
    type="button"
    {disabled}
    aria-label={selectedLabel ? `Vergi dairesi: ${selectedLabel}` : 'Vergi dairesi seçin'}
    aria-haspopup="dialog"
    aria-expanded={open}
    onclick={() => (open = true)}
  >
    <Search size={16} aria-hidden="true" />
    <span class="trigger-copy">
      {#if selectedLabel}
        <strong>{selectedLabel}</strong>
        <small>Vergi dairesi</small>
      {:else}
        <span class="placeholder">Vergi dairesi seçin</span>
      {/if}
    </span>
    <ChevronDown class="trigger-chevron" size={16} aria-hidden="true" />
  </button>
  {#if selectedLabel}
    <button
      class="clear-trigger"
      type="button"
      {disabled}
      aria-label="Vergi dairesi seçimini kaldır"
      title="Seçimi kaldır"
      onclick={clearSelection}><X size={16} aria-hidden="true" /></button
    >
  {/if}
</div>

{#if open}
  <div
    class="picker-overlay"
    role="presentation"
    onclick={(event) => {
      if (event.target === event.currentTarget) open = false;
    }}
  >
    <div
      bind:this={dialogElement}
      class="picker-dialog"
      role="dialog"
      aria-modal="true"
      aria-labelledby="tax-office-picker-title"
      aria-describedby="tax-office-picker-description"
      tabindex="-1"
      onkeydown={handleDialogKeydown}
    >
      <div class="dialog-heading">
        <div>
          <h2 id="tax-office-picker-title">Vergi dairesi seç</h2>
        </div>
        <button class="icon-button" type="button" aria-label="Kapat" onclick={() => (open = false)}
          ><X size={19} /></button
        >
      </div>

      {#if selectedLabel}
        <div class="selected-summary" aria-label={`Mevcut vergi dairesi: ${selectedLabel}`}>
          <span class="selected-mark"><Check size={15} aria-hidden="true" /></span>
          <span class="selected-copy"
            ><small>Seçili vergi dairesi</small><strong>{selectedLabel}</strong></span
          >
        </div>
      {/if}

      <div class="filters" aria-label="Vergi dairesi filtreleri">
        <label class="filter-field">
          <span>İl</span>
          <select
            value={provinceID}
            aria-label="Vergi dairesi ili"
            onchange={(event) => {
              const nextProvinceID = (event.currentTarget as HTMLSelectElement).value;
              provinceID = nextProvinceID;
              activeIndex = 0;
              filterReady = false;
              void prepareDistricts(nextProvinceID);
            }}
          >
            <option value="">Türkiye geneli</option>
            {#each provinces as province}<option value={province.id}>{province.name}</option>{/each}
          </select>
        </label>
        <label class="filter-field">
          <span>İlçe</span>
          <select
            value={districtName}
            aria-label="Vergi dairesi ilçesi"
            disabled={!provinceID || districtsLoading || Boolean(districtError)}
            aria-busy={districtsLoading}
            onchange={(event) => {
              districtName = (event.currentTarget as HTMLSelectElement).value;
              activeIndex = 0;
            }}
          >
            <option value=""
              >{!provinceID
                ? 'Önce il seçin'
                : districtsLoading
                  ? 'İlçeler yükleniyor…'
                  : 'Tüm ilçeler'}</option
            >
            {#each districts as district}<option value={district.name}>{district.name}</option
              >{/each}
          </select>
          {#if districtError}
            <small class="filter-error" role="alert">{districtError}</small>
            <button class="filter-retry" type="button" onclick={retryDistricts}
              >İlçeleri yeniden yükle</button
            >
          {/if}
        </label>
        <label class="filter-field search-field">
          <span>Arama</span>
          <div class="input-with-action">
            <Search class="input-icon" size={16} aria-hidden="true" />
            <input
              bind:this={searchInput}
              bind:value={query}
              class="filter-input"
              type="search"
              placeholder="Ad, kod, il veya ilçe ara…"
              aria-label="Vergi dairesi ara"
              aria-controls="tax-office-results"
              aria-autocomplete="list"
              aria-activedescendant={activeResult ? resultID(activeIndex) : undefined}
              oninput={() => (activeIndex = 0)}
              onkeydown={handleSearchKeydown}
            />
            {#if query}<button
                class="input-clear"
                type="button"
                aria-label="Aramayı temizle"
                onclick={() => (query = '')}><X size={15} /></button
              >{/if}
          </div>
        </label>
      </div>

      <div class="dialog-body">
        {#if searchError}
          <div class="state error-state" role="alert">
            <strong>Sonuçlar alınamadı</strong>
            <span>{searchError}</span>
            <button class="retry-button" type="button" onclick={retrySearch}>Tekrar dene</button>
          </div>
        {:else if loading || districtsLoading}
          <div class="state" role="status" aria-live="polite">
            <LoaderCircle class="spinner" size={23} aria-hidden="true" /><span
              >{districtsLoading ? 'İlçeler hazırlanıyor…' : 'Vergi daireleri yükleniyor…'}</span
            >
          </div>
        {:else if results.length === 0}
          <div class="state" role="status" aria-live="polite">
            <MapPin size={23} aria-hidden="true" />
            <strong
              >{query || districtName || provinceID
                ? 'Eşleşen vergi dairesi bulunamadı.'
                : 'Vergi dairesi aramak için filtre seçin veya bir kelime yazın.'}</strong
            >
            <span>Filtreleri temizleyip Türkiye genelinde arayabilirsiniz.</span>
          </div>
        {:else}
          <div
            id="tax-office-results"
            class="result-list"
            role="listbox"
            aria-label="Vergi dairesi sonuçları"
          >
            {#each results as reference, index}
              <button
                id={resultID(index)}
                class:active={index === activeIndex}
                class:selected={selectedId === reference.id}
                class:inactive={!reference.is_active}
                type="button"
                role="option"
                aria-selected={selectedId === reference.id}
                aria-disabled={!reference.is_active}
                disabled={!reference.is_active}
                onclick={(event) => {
                  event.stopPropagation();
                  selectOption(reference);
                }}
                onmouseenter={() => (activeIndex = index)}
              >
                <span class="result-mark"
                  >{#if selectedId === reference.id}<Check
                      size={15}
                      aria-hidden="true"
                    />{:else}{index + 1}{/if}</span
                >
                <span class="result-copy">
                  <strong>{reference.name}</strong>
                  <small>{reference.province_name} · {reference.district_name}</small>
                </span>
                <span class="result-meta">
                  {#if reference.code}<span class="office-code">{reference.code}</span>{:else}<span
                      class="office-code muted">Kod yok</span
                    >{/if}
                  <Badge tone={reference.is_active ? 'info' : 'neutral'}
                    >{reference.office_type}</Badge
                  >
                  {#if !reference.is_active}<Badge tone="warning">Pasif</Badge>{/if}
                </span>
              </button>
            {/each}
          </div>
        {/if}
      </div>

      <div class="dialog-footer">
        <span role="status" aria-live="polite"
          >{loading || districtsLoading ? 'Aranıyor…' : `${results.length} kayıt`}</span
        >
        <button class="cancel-button" type="button" onclick={() => (open = false)}>Vazgeç</button>
      </div>
    </div>
  </div>
{/if}

<style>
  .picker-control {
    display: flex;
    gap: 6px;
    min-width: 0;
  }
  .picker-trigger {
    display: flex;
    align-items: center;
    gap: 8px;
    width: 100%;
    min-width: 0;
    min-height: var(--control-height);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    background: var(--surface);
    color: var(--text);
    padding: 5px 9px;
    text-align: left;
    cursor: pointer;
  }
  .picker-trigger:hover:not(:disabled) {
    border-color: var(--primary);
    background: var(--surface-muted);
  }
  .picker-trigger:focus-visible,
  .clear-trigger:focus-visible,
  .icon-button:focus-visible,
  .input-clear:focus-visible,
  .retry-button:focus-visible,
  .cancel-button:focus-visible,
  .result-list button:focus-visible {
    outline: 2px solid var(--focus);
    outline-offset: 2px;
  }
  .picker-trigger:disabled,
  .clear-trigger:disabled {
    cursor: not-allowed;
    opacity: 0.55;
  }
  .trigger-copy,
  .selected-copy,
  .result-copy {
    min-width: 0;
    flex: 1;
  }
  .trigger-copy strong,
  .trigger-copy small,
  .selected-copy strong,
  .selected-copy small,
  .result-copy strong,
  .result-copy small {
    display: block;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
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
    color: var(--text-muted);
    font-size: 12px;
  }
  :global(.trigger-chevron) {
    flex: 0 0 auto;
    color: var(--text-muted);
  }
  .clear-trigger,
  .icon-button,
  .input-clear {
    display: grid;
    place-items: center;
    flex: 0 0 auto;
    min-width: 44px;
    min-height: 44px;
    border: 0;
    border-radius: var(--radius-control);
    background: transparent;
    color: var(--text-muted);
    cursor: pointer;
  }
  .clear-trigger:hover:not(:disabled),
  .icon-button:hover,
  .input-clear:hover {
    background: var(--surface-muted);
    color: var(--text);
  }
  .picker-overlay {
    position: fixed;
    inset: 0;
    z-index: 200;
    background: rgb(2 6 23 / 54%);
  }
  .picker-dialog {
    position: fixed;
    top: 8vh;
    left: 50%;
    z-index: 201;
    display: flex;
    width: min(760px, calc(100vw - 28px));
    max-height: min(820px, 84vh);
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
  h2 {
    margin: 0;
    font-size: 16px;
    font-weight: 750;
  }
  .dialog-heading p {
    margin: 4px 0 0;
    color: var(--text-muted);
    font-size: 12px;
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
  .selected-mark,
  .result-mark {
    display: grid;
    place-items: center;
    flex: 0 0 auto;
    width: 28px;
    height: 28px;
    border-radius: 50%;
    background: var(--primary);
    color: var(--primary-foreground);
  }
  .selected-copy small {
    margin-bottom: 2px;
    color: var(--text-muted);
    font-size: 10px;
  }
  .selected-copy strong {
    font-size: 12px;
  }
  .filters {
    display: grid;
    grid-template-columns: minmax(150px, 0.7fr) minmax(150px, 0.8fr) minmax(230px, 1.4fr);
    gap: 10px;
    padding: 0 18px 13px;
    border-bottom: 1px solid var(--border);
  }
  .filter-field {
    display: flex;
    min-width: 0;
    flex-direction: column;
    gap: 4px;
  }
  .filter-field > span {
    color: var(--text-subtle);
    font-size: 11px;
    font-weight: 650;
  }
  .filter-error {
    color: var(--danger);
    font-size: 10px;
  }
  .filter-retry {
    align-self: flex-start;
    border: 0;
    background: transparent;
    color: var(--primary);
    padding: 0;
    cursor: pointer;
    font: inherit;
    font-size: 10px;
    font-weight: 650;
  }
  .filter-field select {
    width: 100%;
    min-height: var(--control-height);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    background: var(--surface);
    color: var(--text);
    padding: 0 8px;
    font-size: 13px;
  }
  .filter-field select:focus-visible {
    outline: 2px solid var(--focus);
    outline-offset: 1px;
  }
  .input-with-action {
    position: relative;
    display: flex;
    align-items: center;
    min-width: 0;
  }
  .input-with-action :global(input) {
    padding-right: 42px;
  }
  .search-field .input-with-action :global(input) {
    padding-left: 31px;
  }
  :global(.input-icon) {
    position: absolute;
    left: 9px;
    z-index: 1;
    color: var(--text-muted);
    pointer-events: none;
  }
  .input-clear {
    position: absolute;
    right: 0;
    min-width: 36px;
    min-height: 34px;
  }
  .dialog-body {
    min-height: 180px;
    overflow: hidden;
  }
  .result-list {
    max-height: min(520px, 52vh);
    overflow-y: auto;
    overscroll-behavior: contain;
    padding: 7px 10px;
  }
  .result-list button {
    display: grid;
    grid-template-columns: 32px minmax(0, 1fr) auto;
    align-items: center;
    gap: 10px;
    width: 100%;
    min-height: 60px;
    border: 1px solid transparent;
    border-bottom-color: var(--border);
    background: var(--surface);
    color: var(--text);
    padding: 7px 8px;
    text-align: left;
    cursor: pointer;
  }
  .result-list button:last-child {
    border-bottom-color: transparent;
  }
  .result-list button:hover:not(:disabled),
  .result-list button.active {
    border-radius: var(--radius-control);
    background: var(--surface-muted);
  }
  .result-list button.selected {
    border-color: var(--primary);
    background: var(--primary-soft);
  }
  .result-list button.inactive {
    cursor: not-allowed;
    opacity: 0.62;
  }
  .result-mark {
    width: 28px;
    height: 28px;
    background: var(--surface-muted);
    color: var(--text-muted);
    font-size: 11px;
    font-weight: 700;
  }
  .selected .result-mark {
    background: var(--primary);
    color: var(--primary-foreground);
  }
  .result-copy strong {
    font-size: 12px;
  }
  .result-copy small {
    margin-top: 3px;
    color: var(--text-muted);
    font-size: 10.5px;
  }
  .result-meta {
    display: flex;
    align-items: center;
    justify-content: flex-end;
    gap: 5px;
    flex-wrap: wrap;
    max-width: 260px;
  }
  .office-code {
    color: var(--primary);
    font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
    font-size: 11px;
    font-weight: 750;
  }
  .office-code.muted {
    color: var(--text-muted);
    font-family: inherit;
    font-weight: 600;
  }
  .state {
    display: grid;
    place-items: center;
    gap: 8px;
    min-height: 220px;
    padding: 24px;
    color: var(--text-muted);
    font-size: 12px;
    text-align: center;
  }
  .state strong {
    color: var(--text);
    font-size: 13px;
  }
  .state span {
    max-width: 420px;
  }
  .error-state strong {
    color: var(--danger);
  }
  .retry-button,
  .cancel-button {
    min-height: 44px;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    background: var(--surface);
    color: var(--text);
    padding: 0 13px;
    cursor: pointer;
    font: inherit;
    font-size: 12px;
    font-weight: 650;
  }
  .retry-button:hover,
  .cancel-button:hover {
    background: var(--surface-muted);
  }
  .dialog-footer {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    min-height: 58px;
    border-top: 1px solid var(--border);
    padding: 8px 18px;
    color: var(--text-muted);
    font-size: 11px;
  }
  .cancel-button {
    color: var(--primary);
  }
  :global(.spinner) {
    animation: tax-office-spin 0.9s linear infinite;
  }
  @keyframes tax-office-spin {
    to {
      transform: rotate(360deg);
    }
  }
  @media (max-width: 640px) {
    .picker-dialog {
      top: 3vh;
      width: calc(100vw - 20px);
      max-height: 94vh;
    }
    .dialog-heading,
    .selected-summary,
    .filters,
    .dialog-footer {
      padding-left: 13px;
      padding-right: 13px;
    }
    .selected-summary {
      margin-left: 13px;
      margin-right: 13px;
    }
    .filters {
      grid-template-columns: 1fr;
      gap: 9px;
    }
    .filter-field select,
    .filter-field :global(input) {
      min-height: 44px;
    }
    .dialog-body {
      min-height: 160px;
    }
    .result-list {
      max-height: 54vh;
      padding: 5px 6px;
    }
    .result-list button {
      grid-template-columns: 32px minmax(0, 1fr);
      min-height: 64px;
    }
    .result-meta {
      grid-column: 2;
      justify-content: flex-start;
      max-width: none;
    }
  }
  @media (prefers-reduced-motion: reduce) {
    :global(.spinner) {
      animation: none;
    }
  }
</style>
