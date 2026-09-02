<script lang="ts">
  import { Input } from '$lib/components/ui/input';
  let {
    value = $bindable(''),
    unit,
    id,
    disabled = false,
    ariaLabel,
    ariaDescribedBy,
    ariaInvalid = false
  }: {
    value?: string;
    unit?: string;
    id?: string;
    disabled?: boolean;
    ariaLabel?: string;
    ariaDescribedBy?: string;
    ariaInvalid?: boolean;
  } = $props();
</script>

<div class="quantity-input">
  <Input
    {id}
    bind:value
    inputmode="decimal"
    {disabled}
    aria-label={ariaLabel}
    aria-describedby={ariaDescribedBy}
    aria-invalid={ariaInvalid}
  />{#if unit}<span>{unit}</span>{/if}
</div>

<style>
  .quantity-input {
    display: flex;
    align-items: center;
    gap: 6px;
    flex-wrap: wrap;
  }
  .quantity-input :global(input) {
    min-width: 0;
    flex: 1 1 auto;
  }
  .quantity-input span {
    color: var(--text-muted);
    font-size: 11px;
    font-weight: 650;
    flex-shrink: 0;
    white-space: nowrap;
  }
</style>
