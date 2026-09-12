<script lang="ts">
  import '$lib/styles.css';
  import { installTurkishFormValidation } from '$lib/form-validation';
  import { onMount } from 'svelte';
  import { beforeNavigate, goto } from '$app/navigation';
  import { page } from '$app/state';
  import { api, APIRequestError } from '$lib/api';
  import { session as sessionStore } from '$lib/session.svelte';
  import { AppShell } from '$lib/components/varya/app-shell';
  import DemoResetCurtain from '$lib/components/varya/demo/DemoResetCurtain.svelte';
  import Logo from '$lib/components/varya/Logo.svelte';
  import { demo, watchDemoResets } from '$lib/demo.svelte';
  import {
    busyGuardLabel,
    discardUnsavedChanges,
    hasUnsavedChanges
  } from '$lib/forms/unsaved-changes.svelte';
  import { UnsavedChangesDialog } from '$lib/components/varya/unsaved-changes-dialog';

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
        const session = await sessionStore.load();
        // A demo reset deletes the company its visitors were working in and
        // builds a new one; their sessions survive but point at nothing. Left
        // alone that renders an empty shell with no company selected, so put
        // them back into the rebuilt demo instead.
        if (demo.enabled && !session.current_company_id && (await demo.resume())) return;
        // Already signed in: skip the login screen.
        if (path === '/giris') await goto('/', { replaceState: true });
      } catch (error) {
        const status = error instanceof APIRequestError ? error.status : 0;
        // No auto sign-in here: a visitor with no session belongs on the login
        // screen, where the demo account is already filled in for them. Only a
        // session left company-less by a reset (handled above) is recovered
        // silently.
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

  // Açık ve değiştirilmiş bir form varken menü, geri tuşu veya başka bir kayda
  // geçiş aynı kayıp denetiminden geçer.
  let unsavedNavOpen = $state(false);
  let pendingNavURL = $state<string | null>(null);
  // "Değişiklikleri sil ve çık" sonrası geçişin kendisi yeniden sorulmamalı.
  let leaving = false;

  // Süren bir kayıt varken geçiş hiç sorulmadan durur: silinecek bir şey yok,
  // sonucu beklenen bir istek var ve o istek geri alınamaz.
  let busyNavLabel = $state<string | null>(null);

  beforeNavigate((navigation) => {
    if (leaving) return;
    const busy = busyGuardLabel();
    if (busy) {
      navigation.cancel();
      if (navigation.type === 'leave') return;
      busyNavLabel = busy;
      return;
    }
    if (!hasUnsavedChanges()) return;
    navigation.cancel();
    // Sekme kapatma/yenilemede tarayıcının kendi uyarısı kullanılır.
    if (navigation.type === 'leave') return;
    pendingNavURL = navigation.to?.url.href ?? null;
    unsavedNavOpen = true;
  });

  async function leaveWithoutSaving() {
    const target = pendingNavURL;
    unsavedNavOpen = false;
    pendingNavURL = null;
    discardUnsavedChanges();
    if (!target) return;
    leaving = true;
    try {
      await goto(target);
    } finally {
      leaving = false;
    }
  }

  onMount(() => {
    // beforeunload yalnızca kaydedilmemiş veri varken etkin olur; tarayıcı
    // kendi metnini gösterir, özel metin garanti edilmez.
    const onBeforeUnload = (event: BeforeUnloadEvent) => {
      if (!busyGuardLabel() && !hasUnsavedChanges()) return;
      event.preventDefault();
      event.returnValue = '';
    };
    window.addEventListener('beforeunload', onBeforeUnload);
    return () => window.removeEventListener('beforeunload', onBeforeUnload);
  });

  onMount(() => installTurkishFormValidation(document));

  onMount(() => {
    void (async () => {
      // Ask once whether this installation is the public demo; on every normal
      // installation the answer is no and nothing below changes. The access
      // check waits for the answer, because on the demo an expired session is
      // recoverable rather than a reason to show the login screen.
      await demo.load();
      watchDemoResets();
      await enforceAccess();
    })();
  });
</script>

<svelte:head>
  <title>Varya One</title>
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

<UnsavedChangesDialog
  bind:open={unsavedNavOpen}
  onKeepEditing={() => {
    unsavedNavOpen = false;
    pendingNavURL = null;
  }}
  onDiscard={() => void leaveWithoutSaving()}
  description="Ayrılırsanız bu sayfada yaptığınız değişiklikler silinecek."
  discardLabel="Değişiklikleri sil ve ayrıl"
/>

{#if busyNavLabel}
  <div class="busy-nav" role="alertdialog" aria-modal="true" aria-labelledby="busy-nav-title">
    <div class="busy-nav-card">
      <h2 id="busy-nav-title">Kayıt sürüyor</h2>
      <p>{busyNavLabel} kaydediliyor. İşlem bitmeden sayfadan ayrılamazsınız.</p>
      <button type="button" onclick={() => (busyNavLabel = null)}>Tamam</button>
    </div>
  </div>
{/if}

<!-- Only the rebuild curtain is app-wide; the demo's information card lives at
     the bottom of the company settings page. -->
<DemoResetCurtain />

<style>
  /* Süren bir kayıt yüzünden durdurulan geçişin bildirimi. */
  .busy-nav {
    position: fixed;
    inset: 0;
    display: grid;
    place-items: center;
    padding: 16px;
    background: color-mix(in srgb, var(--foreground, #111) 45%, transparent);
    z-index: 120;
  }
  .busy-nav-card {
    width: min(420px, 100%);
    display: grid;
    gap: 12px;
    padding: 20px;
    border-radius: 12px;
    background: var(--surface, #fff);
    box-shadow: 0 12px 32px rgb(0 0 0 / 25%);
  }
  .busy-nav-card h2 {
    margin: 0;
    font-size: 1.05rem;
  }
  .busy-nav-card p {
    margin: 0;
    color: var(--muted-foreground, #555);
  }
  .busy-nav-card button {
    justify-self: end;
    min-height: 44px;
    padding: 0 20px;
    border: 1px solid var(--border, #d4d4d8);
    border-radius: 8px;
    background: var(--surface, #fff);
    cursor: pointer;
  }
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
