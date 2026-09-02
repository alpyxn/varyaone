<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { LayoutGrid, TriangleAlert } from '@lucide/svelte';
  import { api, APIRequestError, type Session } from '$lib/api';

  type ModuleState = {
    code: string;
    name: string;
    description: string;
    enabled: boolean;
    version: number;
  };

  let loading = $state(true);
  let denied = $state(false);
  let error = $state('');
  let modules = $state<ModuleState[]>([]);
  let pending = $state<string | null>(null);

  async function load() {
    const response = await api<{ modules: ModuleState[] }>('/modules');
    modules = response.modules;
  }

  onMount(async () => {
    try {
      const session = await api<Session>('/session');
      denied = !(session.permissions ?? []).includes('organization.module.manage');
      if (!denied) await load();
    } catch (cause) {
      if (cause instanceof APIRequestError && cause.status === 401) {
        await goto('/giris');
        return;
      }
      error = cause instanceof Error ? cause.message : 'Modüller yüklenemedi.';
    } finally {
      loading = false;
    }
  });

  async function toggle(item: ModuleState) {
    pending = item.code;
    error = '';
    try {
      await api<ModuleState>(`/modules/${item.code}`, {
        method: 'PUT',
        headers: { 'If-Match': `"${item.version}"` },
        body: JSON.stringify({ enabled: !item.enabled })
      });
      // Reload the whole app so the session's module list, the sidebar and
      // route guards all pick up the change.
      location.reload();
    } catch (cause) {
      error =
        cause instanceof APIRequestError || cause instanceof Error
          ? cause.message
          : 'Modül durumu değiştirilemedi.';
      pending = null;
    }
  }
</script>

<div class="page-header">
  <div>
    <h1>Modüller</h1>
  </div>
</div>

{#if loading}
  <section class="card muted-card">Yükleniyor…</section>
{:else if denied}
  <section class="card muted-card">
    <TriangleAlert size={16} /> Modülleri yönetme yetkiniz yok.
  </section>
{:else}
  {#if error}<p class="notice error">{error}</p>{/if}
  <div class="module-grid">
    {#each modules as item (item.code)}
      <section class="card module-card" class:off={!item.enabled}>
        <div class="card-icon"><LayoutGrid size={20} /></div>
        <h2>{item.name}</h2>
        <p class="card-desc">{item.description}</p>
        <div class="card-foot">
          <span class="state" class:on={item.enabled}>{item.enabled ? 'Etkin' : 'Kapalı'}</span>
          <button
            class="button"
            class:secondary={item.enabled}
            type="button"
            disabled={pending !== null}
            onclick={() => toggle(item)}
          >
            {item.enabled ? 'Devre dışı bırak' : 'Etkinleştir'}
          </button>
        </div>
      </section>
    {/each}
  </div>
{/if}

<style>
  .muted-card {
    display: flex;
    align-items: center;
    gap: 8px;
    color: var(--text-muted);
  }
  .module-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
    gap: 12px;
    margin-top: 14px;
    align-items: start;
  }
  .module-card {
    display: flex;
    flex-direction: column;
    gap: 8px;
    padding: 16px;
  }
  .module-card.off {
    opacity: 0.72;
  }
  .card-icon {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 38px;
    height: 38px;
    border-radius: var(--radius-control);
    background: var(--primary-soft);
    color: var(--primary);
  }
  .module-card h2 {
    margin: 4px 0 0;
    font-size: 15px;
    letter-spacing: -0.01em;
  }
  .card-desc {
    margin: 0;
    color: var(--text-subtle);
    font-size: 12.5px;
    line-height: 1.5;
    flex: 1;
  }
  .card-foot {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    margin-top: 6px;
  }
  .state {
    font-size: 12px;
    font-weight: 650;
    color: var(--text-muted);
  }
  .state.on {
    color: var(--primary);
  }
  .notice {
    display: flex;
    align-items: flex-start;
    gap: 7px;
  }
</style>
