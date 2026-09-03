<script lang="ts">
  /**
   * The demo's only visible control: a reset action at the bottom of the
   * company settings page. Deliberately nothing else - the demo should look
   * like the product, not like a page explaining that it is a demo.
   *
   * On a normal installation the store never enables itself and nothing renders.
   */
  import { RotateCcw } from '@lucide/svelte';
  import { demo } from '$lib/demo.svelte';
</script>

{#if demo.enabled}
  <section class="demo-row">
    <div class="demo-text">
      <strong>Demoyu sıfırla</strong>
      <span>Örnek veriler baştan kurulur. Birkaç saniye sürer.</span>
    </div>
    <button type="button" class="demo-reset" onclick={() => demo.reset()} disabled={demo.busy}>
      <RotateCcw size={14} aria-hidden="true" />
      {demo.busy ? 'Sıfırlanıyor…' : 'Sıfırla'}
    </button>
  </section>
  {#if demo.message}<p class="demo-error" role="alert">{demo.message}</p>{/if}
{/if}

<style>
  .demo-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 16px;
    margin-top: 18px;
    padding: 14px 16px;
    border: 1px solid var(--border);
    border-radius: 12px;
    background: var(--surface);
  }
  .demo-text {
    display: grid;
    gap: 2px;
    font-size: 12px;
  }
  .demo-text strong {
    font-size: 13px;
  }
  .demo-text span {
    color: var(--text-muted);
    line-height: 1.45;
  }
  .demo-reset {
    display: inline-flex;
    flex: 0 0 auto;
    align-items: center;
    gap: 6px;
    height: 30px;
    padding: 0 14px;
    border: 1px solid var(--border);
    border-radius: 8px;
    background: transparent;
    color: inherit;
    font: inherit;
    font-size: 12px;
    cursor: pointer;
  }
  .demo-reset:hover:not(:disabled) {
    background: var(--surface-hover, color-mix(in srgb, currentColor 8%, transparent));
  }
  .demo-reset:disabled {
    opacity: 0.6;
    cursor: default;
  }
  .demo-error {
    margin: 8px 0 0;
    color: var(--danger, #f87171);
    font-size: 12px;
  }
</style>
