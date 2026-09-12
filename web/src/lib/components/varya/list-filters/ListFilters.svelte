<script lang="ts">
  import { X, SlidersHorizontal } from '@lucide/svelte';
  import { Button } from '$lib/components/ui/button';
  import { CurrencySelect } from '$lib/components/varya/currency-select';
  import { DateInput } from '$lib/components/varya/date-input';
  import { EntityCombobox } from '$lib/components/varya/entity-combobox';
  import type { EntityOption } from '$lib/components/varya/entity-picker-dialog/types';
  import type { ListFilter } from './types';
  import { mediaQuery, PHONE_QUERY } from '$lib/design/viewport.svelte';
  let {
    filters,
    values,
    entities = {},
    onChange,
    onEntity,
    onClear,
    disabled = false,
    primaryCount = 3
  }: {
    filters: ListFilter[];
    values: Record<string, string>;
    entities?: Record<string, EntityOption | undefined>;
    onChange: (field: string, value: string) => void;
    onEntity?: (filter: ListFilter, option: EntityOption) => void;
    onClear: () => void;
    disabled?: boolean;
    primaryCount?: number;
  } = $props();
  let expanded = $state(false);
  // A phone has room for one filter next to the search box; the rest stay one
  // tap away behind "Diğer filtreler" rather than pushing the list off screen.
  const phone = mediaQuery(PHONE_QUERY);
  const inlineCount = $derived(phone.matches ? Math.min(primaryCount, 1) : primaryCount);
  const visible = $derived(
    filters.filter((f) => !f.visibleWhen || values[f.visibleWhen.field] === f.visibleWhen.value)
  );
  const active = $derived(visible.filter((f) => values[f.field]));
  function label(filter: ListFilter) {
    const value = values[filter.field];
    return (
      entities[filter.field]?.title ??
      filter.options?.find((o) => o.value === value)?.label ??
      value
    );
  }
</script>

{#if filters.length}
  <div class="list-filters" role="group" aria-label="Liste filtreleri">
    <div class="fields">
      {#each visible as filter, i}
        {#if expanded || i < inlineCount}
          <div class="field">
            <span>{filter.label}</span>
            {#if filter.kind === 'select'}
              <select
                aria-label={filter.label}
                {disabled}
                value={values[filter.field] ?? ''}
                onchange={(e) => onChange(filter.field, e.currentTarget.value)}
              >
                <option value="">Tümü</option>
                {#each filter.options ?? [] as option}<option value={option.value}
                    >{option.label}</option
                  >{/each}
              </select>
            {:else if filter.kind === 'currency'}
              <CurrencySelect
                value={values[filter.field] ?? ''}
                ariaLabel={filter.label}
                allLabel="Tüm para birimleri"
                {disabled}
                onChange={(value) => onChange(filter.field, value)}
              />
            {:else if filter.kind === 'date'}
              <DateInput
                value={values[filter.field] ?? ''}
                ariaLabel={filter.label}
                {disabled}
                onValueChange={(v) => onChange(filter.field, v)}
              />
            {:else if filter.kind === 'entity' && filter.entity}
              <EntityCombobox
                selected={values[filter.field] ? entities[filter.field] : undefined}
                onSearch={filter.entity.search}
                title={filter.entity.title}
                description={filter.entity.description}
                triggerLabel={filter.label}
                triggerPlaceholder={filter.entity.triggerPlaceholder}
                searchPlaceholder={filter.entity.searchPlaceholder}
                {disabled}
                clearable
                onClear={() => onChange(filter.field, '')}
                onSelect={(option) =>
                  onEntity ? onEntity(filter, option) : onChange(filter.field, option.id)}
              />
            {:else}
              <input
                aria-label={filter.label}
                {disabled}
                value={values[filter.field] ?? ''}
                inputmode={filter.inputMode ?? 'text'}
                placeholder={filter.placeholder}
                onchange={(e) => onChange(filter.field, e.currentTarget.value)}
              />
            {/if}
          </div>
        {/if}
      {/each}
      {#if visible.length > inlineCount}
        <Button
          variant="outline"
          size="sm"
          aria-expanded={expanded}
          onclick={() => (expanded = !expanded)}
        >
          <SlidersHorizontal size={14} />{expanded ? 'Daha az filtre' : 'Diğer filtreler'}
          {#if !expanded && visible.slice(inlineCount).some((f) => values[f.field])}
            ({visible.slice(inlineCount).filter((f) => values[f.field]).length}){/if}
        </Button>
      {/if}
    </div>
    {#if active.length}
      <div class="chips" aria-label="Etkin filtreler">
        {#each active as filter}
          <button
            type="button"
            {disabled}
            onclick={() => onChange(filter.field, '')}
            aria-label={`${filter.label} filtresini temizle`}
          >
            {filter.label}: {label(filter)}
            <X size={12} />
          </button>
        {/each}
        <Button variant="ghost" size="sm" {disabled} onclick={onClear}>Filtreleri temizle</Button>
      </div>
    {/if}
  </div>
{/if}

<style>
  .list-filters {
    width: 100%;
    display: grid;
    gap: 8px;
  }
  .fields,
  .chips {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    align-items: end;
  }
  .field {
    display: grid;
    gap: 4px;
    flex: 1 1 145px;
    min-width: 0;
    max-width: 260px;
  }
  .field > span {
    font-size: 11px;
    color: var(--muted-foreground);
  }
  .field select,
  .field input {
    height: 34px;
    min-width: 0;
    width: 100%;
    border: 1px solid var(--border);
    border-radius: 6px;
    background: var(--background);
    padding: 0 9px;
    font-size: 12px;
  }
  .chips {
    align-items: center;
  }
  .chips > button {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    border: 1px solid var(--border);
    border-radius: 16px;
    padding: 4px 9px;
    font-size: 11px;
    background: var(--muted);
    max-width: 100%;
    overflow-wrap: anywhere;
  }
  @media (max-width: 640px) {
    .field {
      flex: 1 1 140px;
      max-width: 100%;
    }
    .field select,
    .field input {
      height: 44px;
      /* Under 16px iOS zooms the page when the control takes focus. */
      font-size: 16px;
    }
    .chips > button {
      min-height: 32px;
    }
  }
</style>
