<script lang="ts">
  import { Menu, Moon, Sun } from '@lucide/svelte';
  import { page } from '$app/state';
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { toast } from 'svelte-sonner';
  import { api, type APIError, type Session } from '$lib/api';
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
  import CompanySwitcher from './CompanySwitcher.svelte';
  import GlobalSearch from './GlobalSearch.svelte';
  import Breadcrumbs from './Breadcrumbs.svelte';
  import { Toaster } from '$lib/components/ui/sonner';
  let { children }: { children: import('svelte').Snippet } = $props();
  let session = $state<Session | null>(null);
  let globalSearchOpen = $state(false);
  let feedbackOpen = $state(false);
  let menuOpen = $state(false);
  let topbarError = $state('');
  let sessionReady = $state(false);
  async function loadSession() {
    try {
      session = await api<Session>('/session');
    } catch {
      session = null;
    } finally {
      sessionReady = true;
    }
  }
  $effect(() => {
    if (!sessionReady || !session) return;
    const required = moduleForPath(page.url.pathname);
    if (required && !session.modules.includes(required)) {
      const name = MODULE_CATALOG.find((m) => m.code === required)?.name ?? 'Bu modül';
      toast.error(`${name} modülü devre dışı.`);
      void goto('/');
    }
  });
  onMount(() => {
    densityPreference.load();
    themePreference.load();
    void loadSession();
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
        <Calculator />
        <Calendar />
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
