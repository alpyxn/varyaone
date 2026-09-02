<script lang="ts">
  import { Input } from '$lib/components/ui/input';
  let {
    value = $bindable(''),
    currency = 'TRY',
    id,
    disabled = false,
    ariaLabel,
    ariaDescribedBy,
    ariaInvalid = false,
    oninput
  }: {
    value?: string;
    currency?: string;
    id?: string;
    disabled?: boolean;
    ariaLabel?: string;
    ariaDescribedBy?: string;
    ariaInvalid?: boolean;
    oninput?: (event: Event) => void;
  } = $props();
</script>

<div class="money-input">
  <Input
    class="money-control"
    {id}
    bind:value
    inputmode="decimal"
    {disabled}
    aria-label={ariaLabel}
    aria-describedby={ariaDescribedBy}
    aria-invalid={ariaInvalid}
    {oninput}
  /><span>{currency}</span>
</div>

<style>
  .money-input {
    position: relative;
  }
  .money-input :global(.money-control) {
    padding-right: 44px;
  }
  .money-input span {
    position: absolute;
    right: 9px;
    top: 50%;
    transform: translateY(-50%);
    color: var(--text-muted);
    font-size: 11px;
    font-weight: 700;
    pointer-events: none;
  }
</style>
