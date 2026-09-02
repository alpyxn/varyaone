<script lang="ts">
  import { SUPPORTED_CURRENCIES } from '$lib/design/currencies';

  type Props = {
    /** Bound ISO currency code, e.g. "TRY". */
    value: string;
    id?: string;
    disabled?: boolean;
    required?: boolean;
    /** When set, prepends an "all currencies" choice with this label and value "". */
    allLabel?: string;
    ariaLabel?: string;
    onChange?: (code: string) => void;
  };
  let {
    value = $bindable(),
    id,
    disabled = false,
    required = false,
    allLabel,
    ariaLabel,
    onChange
  }: Props = $props();

  const codes = SUPPORTED_CURRENCIES.map((c) => c.code);
</script>

<select
  {id}
  {disabled}
  {required}
  aria-label={ariaLabel}
  bind:value
  onchange={(e) => onChange?.((e.currentTarget as HTMLSelectElement).value)}
>
  {#if allLabel}<option value="">{allLabel}</option>{/if}
  {#if value && !codes.includes(value)}<option {value}>{value}</option>{/if}
  {#each SUPPORTED_CURRENCIES as currency (currency.code)}
    <option value={currency.code}>{currency.code} · {currency.name}</option>
  {/each}
</select>

<style>
  select {
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    padding: 8px 10px;
    background: var(--surface);
    color: var(--text);
    font: inherit;
  }
</style>
