<script lang="ts">
  import { Input } from '$lib/components/ui/input';

  type Props = {
    /** Bound override reason text sent back to the server as `override_reason`. */
    reason: string;
    /**
     * True once the server answered NEGATIVE_BALANCE_CONFIRMATION_REQUIRED. The
     * server only returns that code to callers who may override, so the reason
     * field is always shown while active.
     */
    active: boolean;
  };
  let { reason = $bindable(), active }: Props = $props();
</script>

{#if active}
  <div class="negative-balance wide" role="alert">
    <p>
      Seçili hesap bu işlem sonrası negatif bakiyeye düşüyor. Devam etmek için gerekçe girip yeniden
      kaydedin.
    </p>
    <label>
      <span>Negatif bakiye gerekçesi <b>*</b></span>
      <Input bind:value={reason} maxlength={200} placeholder="Örn. onaylı avans ödemesi" />
    </label>
  </div>
{/if}

<style>
  .negative-balance {
    border: 1px solid color-mix(in srgb, var(--danger) 45%, var(--border));
    border-radius: var(--radius-control);
    background: color-mix(in srgb, var(--danger) 8%, var(--surface));
    padding: 10px 12px;
  }
  .negative-balance p {
    margin: 0 0 8px;
    font-size: 12px;
    color: var(--danger);
  }
  .negative-balance label {
    display: flex;
    flex-direction: column;
    gap: 3px;
    font-size: 11px;
  }
</style>
