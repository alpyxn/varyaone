<script lang="ts">
  import { CheckCircle2 } from '@lucide/svelte';
  import Logo from '$lib/components/varya/Logo.svelte';

  let {
    children,
    wide = false,
    tagline = 'Modern işletmeler için açık kaynak ön muhasebe ve ERP',
    highlights = [
      'Stok, depo ve varyant yönetimi',
      'Cari hesaplar, kasa ve banka',
      'Fatura, irsaliye ve e-belge akışı',
      'Anlık raporlar ve karar panoları'
    ]
  }: {
    children: import('svelte').Snippet;
    wide?: boolean;
    tagline?: string;
    highlights?: string[];
  } = $props();
</script>

<div class="auth-shell">
  <aside class="auth-brand-pane" aria-hidden="true">
    <div class="auth-brand-motif">
      <svg viewBox="0 0 220 220" xmlns="http://www.w3.org/2000/svg">
        <g fill="#C1272D">
          <rect x="20" y="100" width="30" height="90" rx="3" transform="rotate(-26 35 190)" />
          <rect x="70" y="55" width="30" height="135" rx="3" transform="rotate(-12 85 190)" />
          <rect x="120" y="35" width="30" height="155" rx="3" transform="rotate(-4 135 190)" />
        </g>
        <rect x="170" y="10" width="30" height="180" rx="3" fill="#f4f1ec" />
      </svg>
    </div>

    <div class="auth-brand-top">
      <span class="auth-brand-logo"><Logo size={44} variant="full" /></span>
      <p class="auth-brand-tagline">{tagline}</p>
    </div>

    <ul class="auth-brand-list">
      {#each highlights as item}
        <li><CheckCircle2 size={17} aria-hidden="true" />{item}</li>
      {/each}
    </ul>

    <p class="auth-brand-foot">© {new Date().getFullYear()} Varya One · Açık kaynak ERP</p>
  </aside>

  <main class="auth-form-pane" id="main-content">
    <div class="auth-form-inner" class:wide>
      <span class="auth-form-logo"><Logo size={34} variant="full" /></span>
      {@render children()}
    </div>
  </main>
</div>

<style>
  .auth-shell {
    display: grid;
    grid-template-columns: 1.05fr 1fr;
    min-height: 100vh;
    min-height: 100dvh;
    background: var(--surface);
  }

  /* ---- Brand pane ---- */
  .auth-brand-pane {
    position: relative;
    overflow: hidden;
    display: flex;
    flex-direction: column;
    gap: 34px;
    padding: clamp(32px, 5vw, 64px);
    color: #f4f1ec;
    background: radial-gradient(120% 90% at 15% 0%, #2a2a2a 0%, #1a1a1a 55%, #121212 100%);
  }
  .auth-brand-motif {
    position: absolute;
    right: -8%;
    bottom: -12%;
    width: min(58%, 460px);
    opacity: 0.14;
    pointer-events: none;
  }
  .auth-brand-motif svg {
    display: block;
    width: 100%;
    height: auto;
  }
  .auth-brand-top {
    position: relative;
    margin-top: auto;
  }
  .auth-brand-logo {
    display: inline-flex;
    color: #f4f1ec;
  }
  .auth-brand-logo :global(.varya-logo-word) {
    font-weight: 600;
  }
  .auth-brand-tagline {
    max-width: 26ch;
    margin: 22px 0 0;
    font-size: clamp(22px, 2.6vw, 30px);
    font-weight: 600;
    line-height: 1.28;
    letter-spacing: -0.02em;
  }
  .auth-brand-list {
    position: relative;
    display: grid;
    gap: 13px;
    margin: 0;
    padding: 0;
    list-style: none;
  }
  .auth-brand-list li {
    display: flex;
    align-items: center;
    gap: 10px;
    font-size: 14px;
    color: rgba(244, 241, 236, 0.82);
  }
  .auth-brand-list :global(svg) {
    flex: none;
    color: #e0555a;
  }
  .auth-brand-foot {
    position: relative;
    margin: auto 0 0;
    font-size: 12px;
    color: rgba(244, 241, 236, 0.5);
  }

  /* ---- Form pane ---- */
  .auth-form-pane {
    display: flex;
    align-items: center;
    justify-content: center;
    padding: clamp(28px, 5vw, 56px);
    overflow-y: auto;
  }
  .auth-form-inner {
    width: 100%;
    max-width: 400px;
  }
  .auth-form-inner.wide {
    max-width: 620px;
  }
  .auth-form-logo {
    display: none;
    margin-bottom: 22px;
  }

  @media (max-width: 900px) {
    .auth-shell {
      grid-template-columns: 1fr;
    }
    .auth-brand-pane {
      display: none;
    }
    .auth-form-pane {
      align-items: flex-start;
    }
    .auth-form-logo {
      display: inline-flex;
    }
    .auth-form-inner {
      max-width: 440px;
      margin: 0 auto;
    }
  }
</style>
