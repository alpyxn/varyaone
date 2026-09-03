<script lang="ts">
  /**
   * Covers the screen while the demo is being rebuilt. A reset deletes every
   * session, so without this the visitor would be dropped onto a login screen
   * mid-click; instead they wait a couple of seconds and carry on.
   *
   * Unlike the information card this must be mounted app-wide: a reset can be
   * triggered by the timer while the visitor is anywhere.
   */
  import { demo } from '$lib/demo.svelte';
</script>

{#if demo.rebuilding}
  <div class="demo-curtain" role="alertdialog" aria-live="assertive">
    <div class="demo-curtain-card">
      <span class="demo-curtain-bar" aria-hidden="true"></span>
      <h2>Demo yenileniyor</h2>
      <p>Örnek veriler baştan kuruluyor. Birkaç saniye sürer, sonra otomatik devam edersiniz.</p>
      {#if demo.message}<p class="demo-curtain-error">{demo.message}</p>{/if}
    </div>
  </div>
{/if}

<style>
  .demo-curtain {
    position: fixed;
    inset: 0;
    z-index: 120;
    display: flex;
    align-items: center;
    justify-content: center;
    background: rgb(0 0 0 / 72%);
    backdrop-filter: blur(2px);
  }
  .demo-curtain-card {
    max-width: 380px;
    padding: 26px 28px;
    border: 1px solid var(--border, #33333a);
    border-radius: 14px;
    background: var(--surface, #17171b);
    text-align: center;
  }
  .demo-curtain-card h2 {
    margin: 14px 0 6px;
    font-size: 17px;
  }
  .demo-curtain-card p {
    margin: 0;
    color: var(--text-muted, #a1a1aa);
    font-size: 13px;
    line-height: 1.5;
  }
  .demo-curtain-error {
    margin-top: 10px !important;
    color: var(--danger, #f87171) !important;
  }
  .demo-curtain-bar {
    display: block;
    width: 118px;
    height: 3px;
    margin: 0 auto;
    border-radius: 3px;
    background: linear-gradient(90deg, transparent, #c1272d, transparent);
    background-size: 200% 100%;
    animation: demo-curtain-slide 1.1s ease-in-out infinite;
  }
  @keyframes demo-curtain-slide {
    0% {
      background-position: 150% 0;
    }
    100% {
      background-position: -50% 0;
    }
  }
  @media (prefers-reduced-motion: reduce) {
    .demo-curtain-bar {
      animation: none;
    }
  }
</style>
