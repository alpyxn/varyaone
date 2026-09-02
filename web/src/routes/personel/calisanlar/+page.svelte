<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { Plus, RefreshCw } from '@lucide/svelte';
  import { api, APIRequestError, type Session } from '$lib/api';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { formatDate } from '$lib/design/formatters';
  import { listEmployees } from '$lib/features/hr/api';
  import { Badge } from '$lib/components/ui/badge';
  import { payrollStatusLabel, statusTone, type Employee } from '$lib/features/hr/types';

  let permissions = $state<string[]>([]);
  let denied = $state(false);
  let loading = $state(true);
  let error = $state('');
  let rows = $state<Employee[]>([]);
  let nextCursor = $state<string | undefined>();
  let search = $state('');
  let statusFilter = $state('ACTIVE');

  const canEdit = $derived(permissions.includes('hr.employee.edit'));

  async function loadSession() {
    try {
      const session = await api<Session>('/session');
      permissions = session.permissions ?? [];
      denied = !permissions.includes('hr.employee.read');
    } catch {
      denied = true;
    }
  }

  async function load(reset = true) {
    if (denied) return;
    if (reset) {
      loading = true;
      rows = [];
      nextCursor = undefined;
    }
    error = '';
    try {
      const result = await listEmployees({
        q: search.trim() || undefined,
        status: statusFilter || undefined,
        cursor: reset ? undefined : nextCursor
      });
      rows = reset ? result.items : [...rows, ...result.items];
      nextCursor = result.next_cursor;
    } catch (cause) {
      error = cause instanceof APIRequestError ? cause.message : 'Çalışanlar yüklenemedi.';
    } finally {
      loading = false;
    }
  }

  onMount(async () => {
    await loadSession();
    await load();
  });
</script>

<svelte:head><title>Çalışanlar · Varya One</title></svelte:head>

{#if denied}
  <section class="card" role="alert">Çalışanları görüntüleme yetkiniz yok.</section>
{:else}
  <header class="page-header">
    <div>
      <h1>Çalışanlar</h1>
    </div>
    <div class="page-actions">
      <Button variant="outline" onclick={() => void load()} disabled={loading}>
        <RefreshCw size={14} aria-hidden="true" />Yenile
      </Button>
      {#if canEdit}
        <Button onclick={() => goto('/personel/calisanlar/yeni')}>
          <Plus size={14} aria-hidden="true" />Yeni Çalışan
        </Button>
      {/if}
    </div>
  </header>

  {#if error}<p class="notice error" role="alert">{error}</p>{/if}

  <section class="card list-card">
    <form
      class="list-filters"
      onsubmit={(e) => {
        e.preventDefault();
        void load();
      }}
    >
      <Input bind:value={search} placeholder="Kod, ad veya soyad" aria-label="Ara" />
      <select bind:value={statusFilter} onchange={() => void load()} aria-label="Durum">
        <option value="">Tümü</option>
        <option value="ACTIVE">Aktif</option>
        <option value="INACTIVE">Pasif</option>
        <option value="ARCHIVED">Arşiv</option>
      </select>
      <Button type="submit" variant="outline">Ara</Button>
    </form>
    {#if loading}
      <p class="list-state">Yükleniyor…</p>
    {:else if !rows.length}
      <p class="list-state">Kayıt bulunamadı.</p>
    {:else}
      <div class="table-scroll">
        <table class="data-table">
          <thead
            ><tr><th>Kod</th><th>Ad Soyad</th><th>Pozisyon</th><th>Durum</th><th>İşe giriş</th></tr
            ></thead
          >
          <tbody>
            {#each rows as e}
              <tr class="row-link" onclick={() => goto(`/personel/calisanlar/${e.id}`)}>
                <td>{e.employee_code}</td>
                <td><strong>{e.first_name} {e.last_name}</strong></td>
                <td class="muted">{e.position_title || '—'}</td>
                <td><Badge tone={statusTone(e.status)}>{payrollStatusLabel(e.status)}</Badge></td>
                <td class="muted">{e.hire_date ? formatDate(e.hire_date) : '—'}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
      {#if nextCursor}
        <div class="end">
          <Button variant="outline" onclick={() => void load(false)}>Daha fazla</Button>
        </div>
      {/if}
    {/if}
  </section>
{/if}

<style>
  .end {
    display: flex;
    justify-content: flex-end;
    padding: 12px 14px;
    border-top: 1px solid var(--border);
  }
</style>
