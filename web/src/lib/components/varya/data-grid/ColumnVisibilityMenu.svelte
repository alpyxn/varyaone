<script lang="ts" generics="T extends object">
  import { Check, PanelsTopLeft, RotateCcw } from '@lucide/svelte';
  import { onMount } from 'svelte';
  import { Button } from '$lib/components/ui/button';
  import type { ColumnVisibilityState, VaryaColumn } from './types';

  let {
    columns,
    value = $bindable<ColumnVisibilityState>({}),
    storageKey,
    disabled = false,
    onChange
  }: {
    columns: VaryaColumn<T>[];
    value?: ColumnVisibilityState;
    storageKey?: string;
    disabled?: boolean;
    onChange?: (visibility: ColumnVisibilityState) => void | Promise<void>;
  } = $props();

  let root = $state<HTMLDivElement>();
  let open = $state(false);
  let hydrated = $state(false);
  let panelId = $state('column-visibility-panel');
  const visibleCount = $derived(columns.filter((column) => value[column.id] !== false).length);

  function defaultVisibility(): ColumnVisibilityState {
    return Object.fromEntries(
      columns
        .filter((column) => column.hideable !== false && column.defaultVisible === false)
        .map((column) => [column.id, false])
    );
  }

  function persist() {
    if (!hydrated || !storageKey || typeof localStorage === 'undefined') return;
    const hidden = Object.fromEntries(
      columns.filter((column) => value[column.id] === false).map((column) => [column.id, false])
    );
    localStorage.setItem(storageKey, JSON.stringify(hidden));
  }

  function setVisibility(columnId: string, visible: boolean) {
    const column = columns.find((item) => item.id === columnId);
    if (column?.hideable === false) return;
    if (!visible && visibleCount <= 1) return;
    const next = { ...value, [columnId]: visible };
    value = next;
    void onChange?.(next);
    persist();
  }

  function reset() {
    const next = defaultVisibility();
    value = next;
    void onChange?.(next);
    persist();
  }

  function handleDocumentPointerDown(event: PointerEvent) {
    if (open && root && !root.contains(event.target as Node)) open = false;
  }

  function handleDocumentKeydown(event: KeyboardEvent) {
    if (open && event.key === 'Escape') {
      event.preventDefault();
      open = false;
    }
  }

  onMount(() => {
    panelId = `column-visibility-panel-${Math.random().toString(36).slice(2)}`;
    if (storageKey && typeof localStorage !== 'undefined') {
      try {
        const storedValue = localStorage.getItem(storageKey);
        if (storedValue === null) {
          value = defaultVisibility();
        } else {
          const stored = JSON.parse(storedValue) as Record<string, unknown>;
          const validHidden = Object.fromEntries(
            columns
              .filter((column) => column.hideable !== false && stored[column.id] === false)
              .map((column) => [column.id, false])
          );
          const hasVisibleColumn = columns.some(
            (column) => column.hideable === false || validHidden[column.id] !== false
          );
          value = hasVisibleColumn ? validHidden : defaultVisibility();
        }
      } catch {
        // A malformed preference must never stop the table from rendering.
        value = defaultVisibility();
      }
    }
    hydrated = true;
    document.addEventListener('pointerdown', handleDocumentPointerDown);
    document.addEventListener('keydown', handleDocumentKeydown);
    return () => {
      document.removeEventListener('pointerdown', handleDocumentPointerDown);
      document.removeEventListener('keydown', handleDocumentKeydown);
    };
  });
</script>

<div class="visibility-menu" bind:this={root}>
  <Button
    variant="outline"
    type="button"
    aria-haspopup="dialog"
    aria-expanded={open}
    aria-controls={panelId}
    {disabled}
    onclick={() => (open = !open)}><PanelsTopLeft size={14} />Görünüm</Button
  >
  {#if open}<div
      id={panelId}
      class="visibility-panel"
      role="dialog"
      aria-label="Tablo sütun görünümü"
    >
      <div class="panel-heading">
        <div><strong>Sütunlar</strong><small>Görmek istediğiniz alanları seçin.</small></div>
        <Button
          variant="ghost"
          size="icon"
          type="button"
          title="Varsayılan görünüme dön"
          onclick={reset}
          ><RotateCcw size={14} /><span class="sr-only">Varsayılan görünüme dön</span></Button
        >
      </div>
      <div class="column-options">
        {#each columns as column}
          {@const visible = value[column.id] !== false}
          {@const hideable = column.hideable !== false}
          <label class:only-visible={hideable && visible && visibleCount === 1}>
            <input
              type="checkbox"
              checked={visible}
              disabled={!hideable || (visible && visibleCount === 1)}
              onchange={(event) =>
                setVisibility(column.id, (event.currentTarget as HTMLInputElement).checked)}
            />
            <span>{column.header}</span>
            {#if visible}<Check size={14} />{/if}
          </label>
        {/each}
      </div>
    </div>{/if}
</div>

<style>
  .visibility-menu {
    position: relative;
  }
  .visibility-panel {
    position: absolute;
    z-index: 40;
    top: calc(100% + 5px);
    right: 0;
    width: 250px;
    padding: 10px;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-panel);
    background: var(--surface);
    box-shadow: 0 14px 32px rgb(15 23 42 / 16%);
  }
  .panel-heading {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 8px;
    padding: 1px 2px 8px;
    border-bottom: 1px solid var(--border);
  }
  .panel-heading strong,
  .panel-heading small {
    display: block;
  }
  .panel-heading strong {
    font-size: 12px;
  }
  .panel-heading small {
    margin-top: 2px;
    color: var(--text-muted);
    font-size: 10.5px;
  }
  .panel-heading :global(button) {
    width: 28px;
    height: 28px;
  }
  .column-options {
    display: grid;
    gap: 2px;
    padding-top: 7px;
  }
  .column-options label {
    min-height: 30px;
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 4px 6px;
    border-radius: 4px;
    color: var(--text);
    font-size: 12px;
    cursor: pointer;
  }
  .column-options label:hover {
    background: var(--surface-muted);
  }
  .column-options label.only-visible {
    color: var(--text-muted);
    cursor: not-allowed;
  }
  .column-options input {
    accent-color: var(--primary);
  }
  .column-options label :global(svg) {
    margin-left: auto;
    color: var(--primary);
  }
</style>
