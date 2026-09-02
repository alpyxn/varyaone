<script lang="ts">
  import '$lib/styles.css';
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/state';
  import { api, APIRequestError } from '$lib/api';
  import { AppShell } from '$lib/components/varya/app-shell';
  import Logo from '$lib/components/varya/Logo.svelte';

  let { children } = $props();

  const PUBLIC_ROUTES = new Set(['/giris', '/kurulum']);
  const publicRoute = $derived(PUBLIC_ROUTES.has(page.url.pathname));
  // Authenticated but rendered without the app shell, so the flow feels like the
  // one-time setup screen.
  const CHROMELESS_ROUTES = new Set(['/firma-ekle']);
  const chromelessRoute = $derived(CHROMELESS_ROUTES.has(page.url.pathname));

  // Block the first paint until we know whether the visitor may stay on this
  // route; subsequent client navigations render immediately.
  let checking = $state(true);

  async function enforceAccess() {
    const path = page.url.pathname;
    try {
      const setup = await api<{ complete: boolean }>('/setup');

      if (!setup.complete) {
        // Nothing is usable until the first admin/company exists.
        if (path !== '/kurulum') await goto('/kurulum', { replaceState: true });
        return;
      }

      if (path === '/kurulum') {
        // Setup already done — send them to sign in.
        await goto('/giris', { replaceState: true });
        return;
      }

      try {
        await api('/session');
        // Already signed in: skip the login screen.
        if (path === '/giris') await goto('/', { replaceState: true });
      } catch (error) {
        const status = error instanceof APIRequestError ? error.status : 0;
        if (!path.startsWith('/giris') && (status === 401 || status === 403 || status === 0)) {
          await goto('/giris', { replaceState: true });
        }
      }
    } catch {
      // /setup itself is unreachable — let the page render its own error state.
    } finally {
      checking = false;
    }
  }

  onMount(enforceAccess);
</script>

<svelte:head>
  <title>Varya One</title>
  <meta name="description" content="Modern işletmeler için açık kaynak ön muhasebe ve ERP" />
</svelte:head>

{#if checking}
  <div class="boot-splash">
    <Logo size={60} variant="full" />
    <span class="boot-splash-bar" aria-hidden="true"></span>
    <span class="sr-only">Yükleniyor…</span>
  </div>
{:else if publicRoute || chromelessRoute}
  {@render children()}
{:else}
  <AppShell>{@render children()}</AppShell>
{/if}

<style>
  .boot-splash {
    position: fixed;
    inset: 0;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 20px;
    background: var(--surface);
    z-index: 100;
  }
  .boot-splash-bar {
    width: 128px;
    height: 3px;
    border-radius: 3px;
    background: linear-gradient(90deg, transparent, #c1272d, transparent);
    background-size: 200% 100%;
    animation: boot-splash-slide 1.1s ease-in-out infinite;
  }
  @keyframes boot-splash-slide {
    0% {
      background-position: 150% 0;
    }
    100% {
      background-position: -50% 0;
    }
  }
  @media (prefers-reduced-motion: reduce) {
    .boot-splash-bar {
      animation: none;
    }
  }
</style>
