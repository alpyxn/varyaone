<script lang="ts">
  import { onMount } from 'svelte';
  import { toast } from 'svelte-sonner';
  import { Check, Pencil, Pin, PinOff } from '@lucide/svelte';
  import { api, type DashboardShortcuts, type RecentActivityEntry, type Session } from '$lib/api';
  import { StateBlock } from '$lib/components/varya/status';
  import Logo from '$lib/components/varya/Logo.svelte';
  import {
    availableShortcuts,
    DEFAULT_PINNED,
    groupedShortcuts,
    resolvePinnedShortcuts,
    shortcutCatalog
  } from '$lib/features/dashboard/shortcuts';
  import { toRecentActivityView } from '$lib/features/dashboard/recent';

  const catalogKeys = new Set(shortcutCatalog.map((shortcut) => shortcut.key));

  let session = $state<Session>();
  let loading = $state(true);
  let error = $state('');
  let recent = $state<RecentActivityEntry[]>([]);
  let pinnedKeys = $state<string[]>([]);
  let editing = $state(false);

  const permissions = $derived(session?.permissions);
  const modules = $derived(session?.modules);
  // Every recent-activity kind (ledger, stock, document) belongs to Ön Muhasebe.
  const recentEnabled = $derived(!modules || modules.includes('preaccounting'));
  const pinnedShortcuts = $derived(resolvePinnedShortcuts(pinnedKeys, permissions, modules));
  const available = $derived(availableShortcuts(permissions, modules));
  // One flat grid, but every module's tiles stay contiguous and in a fixed order.
  const launcherItems = $derived(
    groupedShortcuts(permissions, modules).flatMap((bucket) => bucket.items)
  );
  const pinnedSet = $derived(new Set(pinnedKeys));
  const recentViews = $derived(recentEnabled ? recent.map(toRecentActivityView) : []);

  function initialPinned(saved: string[] | undefined): string[] {
    const keys = saved && saved.length ? saved : DEFAULT_PINNED;
    return keys.filter((key) => catalogKeys.has(key));
  }

  async function persist(next: string[]) {
    const previous = pinnedKeys;
    pinnedKeys = next;
    try {
      await api<DashboardShortcuts>('/dashboard/shortcuts', {
        method: 'PUT',
        body: JSON.stringify({ pinned_shortcuts: next })
      });
    } catch (cause) {
      pinnedKeys = previous;
      toast.error(
        typeof cause === 'object' && cause && 'message' in cause
          ? String(cause.message)
          : 'Kısayol kaydedilemedi.'
      );
    }
  }

  function togglePin(key: string) {
    void persist(
      pinnedKeys.includes(key) ? pinnedKeys.filter((item) => item !== key) : [...pinnedKeys, key]
    );
  }

  async function load() {
    loading = true;
    error = '';
    try {
      const [sessionResult, shortcuts, activity] = await Promise.all([
        api<Session>('/session'),
        api<DashboardShortcuts>('/dashboard/shortcuts').catch(() => null),
        api<{ entries: RecentActivityEntry[] }>('/dashboard/recent-activity?limit=15').catch(
          () => null
        )
      ]);
      session = sessionResult;
      pinnedKeys = initialPinned(shortcuts?.pinned_shortcuts);
      recent = activity?.entries ?? [];
    } catch (cause) {
      error =
        typeof cause === 'object' && cause && 'message' in cause
          ? String(cause.message)
          : 'Çalışma alanı yüklenemedi.';
    } finally {
      loading = false;
    }
  }

  onMount(() => void load());
</script>

<svelte:head><title>Çalışma Alanı · Varya One</title></svelte:head>

<header class="home-hero">
  <Logo size={72} variant="full" />
</header>

{#if loading}
  <StateBlock loading loadingText="Çalışma alanı hazırlanıyor…" />
{:else if error}
  <StateBlock {error} onRetry={load} />
{:else if !available.length}
  <StateBlock
    empty
    emptyTitle="Henüz işlem yetkiniz yok"
    emptyDescription="Şirket yöneticinizden ilgili çalışma alanı yetkilerini isteyin."
  />
{:else}
  <section class="launcher" aria-label="Kısayollar">
    {#if !editing && pinnedShortcuts.length}
      <div class="launcher-block">
        <h2 class="section-note">Sabitlenenler</h2>
        <div class="launcher-grid">
          {#each pinnedShortcuts as shortcut (shortcut.key)}
            {@const Icon = shortcut.icon}
            <a class="launcher-tile featured" href={shortcut.href}>
              <Icon size={26} aria-hidden="true" />
              <span>{shortcut.label}</span>
            </a>
          {/each}
        </div>
      </div>
    {/if}

    <div class="launcher-block">
      <div class="launcher-block-head">
        <h2 class="section-note">
          {editing ? 'Sabitlemek için bir kutucuğa dokunun' : 'Tüm modüller'}
        </h2>
        {#if available.length}
          <button
            type="button"
            class="button secondary"
            aria-pressed={editing}
            onclick={() => (editing = !editing)}
          >
            {#if editing}<Check size={14} aria-hidden="true" />Bitti{:else}<Pencil
                size={14}
                aria-hidden="true"
              />Düzenle{/if}
          </button>
        {/if}
      </div>
      <div class="launcher-grid">
        {#each launcherItems as shortcut (shortcut.key)}
          {@const Icon = shortcut.icon}
          {@const isPinned = pinnedSet.has(shortcut.key)}
          {#if editing}
            <button
              type="button"
              class="launcher-tile"
              class:featured={isPinned}
              aria-pressed={isPinned}
              onclick={() => togglePin(shortcut.key)}
            >
              <Icon size={26} aria-hidden="true" />
              <span>{shortcut.label}</span>
              {#if isPinned}<PinOff size={13} aria-hidden="true" />{:else}<Pin
                  size={13}
                  aria-hidden="true"
                />{/if}
            </button>
          {:else}
            <a class="launcher-tile" href={shortcut.href}>
              <Icon size={26} aria-hidden="true" />
              <span>{shortcut.label}</span>
            </a>
          {/if}
        {/each}
      </div>
    </div>
  </section>

  {#if recentEnabled}
    <section aria-label="Son işlemler">
      <article class="card">
        <div class="section-heading">
          <div>
            <h2 class="panel-title">Son işlemler</h2>
            <p class="section-note">Şirket genelinde en son kaydedilen hareketler.</p>
          </div>
        </div>
        {#if !recentViews.length}
          <StateBlock
            empty
            emptyTitle="Henüz hareket yok"
            emptyDescription="Fatura, tahsilat veya stok hareketi kaydettikçe burada listelenir."
          />
        {:else}
          <ul class="recent-list">
            {#each recentViews as item}
              {@const Icon = item.icon}
              <li>
                {#if item.href}
                  <a class="recent-row" href={item.href}>
                    <Icon size={16} aria-hidden="true" />
                    <span class="recent-body">
                      <strong>{item.title}</strong>
                      <small>{item.subtitle}</small>
                    </span>
                    <span class="recent-meta">
                      {#if item.amount}<span class="recent-amount">{item.amount}</span>{/if}
                      <small>{item.when}</small>
                    </span>
                  </a>
                {:else}
                  <div class="recent-row">
                    <Icon size={16} aria-hidden="true" />
                    <span class="recent-body">
                      <strong>{item.title}</strong>
                      <small>{item.subtitle}</small>
                    </span>
                    <span class="recent-meta">
                      {#if item.amount}<span class="recent-amount">{item.amount}</span>{/if}
                      <small>{item.when}</small>
                    </span>
                  </div>
                {/if}
              </li>
            {/each}
          </ul>
        {/if}
      </article>
    </section>
  {/if}
{/if}

<style>
  .home-hero {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 12px;
    padding: 28px 16px 32px;
    margin-bottom: 8px;
  }
  .home-hero :global(.varya-logo-word) {
    font-weight: 600;
  }
  .launcher-block-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    margin-bottom: 10px;
  }
  .launcher-block-head .section-note {
    margin: 0;
  }
  @media (max-width: 560px) {
    .home-hero {
      padding: 16px 12px 22px;
    }
  }
</style>
