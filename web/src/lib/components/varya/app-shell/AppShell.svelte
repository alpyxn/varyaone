<script lang="ts">
  import { Menu, Moon, Sun } from '@lucide/svelte';
  import { page } from '$app/state';
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { toast } from 'svelte-sonner';
  import { api, type APIError, type Session } from '$lib/api';
  import { session as sessionStore } from '$lib/session.svelte';
  import { moduleForPath, MODULE_CATALOG } from '$lib/modules';
  import { Button } from '$lib/components/ui/button';
  import { densityPreference } from '$lib/design/density.svelte';
  import { themePreference } from '$lib/design/theme.svelte';
  import {
    dispatchVaryaShortcut,
    registerVaryaKeyboardShortcuts
  } from '$lib/components/varya/keyboard';
  import Sidebar from './Sidebar.svelte';
  import UserMenu from './UserMenu.svelte';
  import FeedbackDialog from './FeedbackDialog.svelte';
  import Calculator from './Calculator.svelte';
  import Calendar from './Calendar.svelte';
  import ToolsMenu from './ToolsMenu.svelte';
  import CompanySwitcher from './CompanySwitcher.svelte';
  import GlobalSearch from './GlobalSearch.svelte';
  import Breadcrumbs from './Breadcrumbs.svelte';
  import { Toaster } from '$lib/components/ui/sonner';
  import { mediaQuery, PHONE_QUERY } from '$lib/design/viewport.svelte';
  import { afterNavigate } from '$app/navigation';
  let { children }: { children: import('svelte').Snippet } = $props();
  let globalSearchOpen = $state(false);
  let feedbackOpen = $state(false);
  let menuOpen = $state(false);
  let calculatorOpen = $state(false);
  let calendarOpen = $state(false);
  // Below this width the topbar cannot hold theme + calculator + calendar next
  // to the company switcher; calculator and calendar are hidden there (mobile
  // has no room for them) and only the theme toggle survives, tucked into a
  // tools menu.
  const phone = mediaQuery(PHONE_QUERY);
  let topbarError = $state('');
  // The layout has normally already fetched this while the boot splash was up,
  // in which case the call below resolves from the shared store without a
  // request. A failure leaves `session` null and the shell renders with no
  // permissions, exactly as before.
  const session = $derived(sessionStore.current);
  const sessionReady = $derived(sessionStore.settled);
  $effect(() => {
    if (!sessionReady || !session) return;
    const required = moduleForPath(page.url.pathname);
    if (required && !session.modules.includes(required)) {
      const name = MODULE_CATALOG.find((m) => m.code === required)?.name ?? 'Bu modül';
      toast.error(`${name} modülü devre dışı.`);
      void goto('/');
    }
  });
  // A link inside the drawer closes it, but a redirect, a breadcrumb or the
  // browser's back button must close it too.
  afterNavigate(() => {
    menuOpen = false;
  });

  onMount(() => {
    densityPreference.load();
    themePreference.load();
    void sessionStore.load().catch(() => {});
    return registerVaryaKeyboardShortcuts({
      search: () => (globalSearchOpen = true),
      new: () => dispatchVaryaShortcut('new'),
      save: () => dispatchVaryaShortcut('save'),
      close: () => dispatchVaryaShortcut('close'),
      edit: () => dispatchVaryaShortcut('edit')
    });
  });
  async function selectCompany(companyID: string) {
    topbarError = '';
    try {
      await api<Session>('/session/company', {
        method: 'PUT',
        body: JSON.stringify({ company_id: companyID })
      });
      // Yeni şirkete geçince bağlam tamamen değişir; anasayfaya dönüp
      // oturumu baştan yükleyelim.
      sessionStore.clear();
      location.href = '/';
    } catch (cause) {
      topbarError =
        typeof cause === 'object' && cause && 'message' in cause
          ? String(cause.message)
          : 'Şirket değiştirilemedi.';
    }
  }
  async function logout() {
    topbarError = '';
    try {
      await api<void>('/auth/logout', { method: 'POST', body: '{}' });
      sessionStore.clear();
      location.href = '/giris';
    } catch (cause) {
      topbarError =
        typeof cause === 'object' && cause && 'message' in cause
          ? String((cause as APIError).message)
          : 'Oturum kapatılamadı.';
    }
  }
</script>

<div class="app-shell" data-density="compact">
  <a class="skip-link" href="#main-content">Ana içeriğe geç</a>
  <Sidebar
    bind:open={menuOpen}
    permissions={session?.permissions ?? []}
    permissionsReady={sessionReady}
    modules={session?.modules ?? []}
  />
  <section class="workspace">
    <header class="topbar">
      <Button
        class="mobile-menu"
        variant="ghost"
        size="icon"
        aria-label="Ana menüyü aç"
        aria-controls="app-sidebar"
        aria-expanded={menuOpen}
        onclick={() => (menuOpen = true)}><Menu size={19} /></Button
      >
      <CompanySwitcher {session} onchange={selectCompany} oncreate={() => goto('/firma-ekle')} />
      <GlobalSearch
        bind:open={globalSearchOpen}
        permissions={session?.permissions}
        permissionsReady={sessionReady}
        modules={session?.modules}
      />
      <div class="top-actions">
        {#if !phone.matches}
          <Button
            variant="ghost"
            size="icon"
            class="theme-toggle"
            aria-label={themePreference.value === 'dark' ? 'Açık temaya geç' : 'Koyu temaya geç'}
            title={themePreference.value === 'dark' ? 'Açık tema' : 'Koyu tema'}
            onclick={() => themePreference.toggle()}
            >{#if themePreference.value === 'dark'}<Sun size={17} />{:else}<Moon
                size={17}
              />{/if}</Button
          >
        {/if}
        {#if !phone.matches}
          <Calculator bind:open={calculatorOpen} />
          <Calendar bind:open={calendarOpen} />
        {/if}
        {#if phone.matches}
          <ToolsMenu />
        {/if}
        {#if session}<UserMenu
            displayName={session.user.display_name}
            onLogout={logout}
            onFeedback={() => (feedbackOpen = true)}
          />{:else}<a class="link-button" href="/giris">Giriş yap</a>{/if}
      </div>
      {#if topbarError}<div class="topbar-error" role="alert">{topbarError}</div>{/if}
    </header>
    <Breadcrumbs pathname={page.url.pathname} />
    <main class="main" id="main-content">{@render children()}</main>
  </section>
</div>
<FeedbackDialog bind:open={feedbackOpen} />
<Toaster position="bottom-right" />
