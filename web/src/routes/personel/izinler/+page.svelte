<script lang="ts">
  import { onMount } from 'svelte';
  import { api, APIRequestError, type Session } from '$lib/api';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import * as hr from '$lib/features/hr/api';
  import { LEAVE_TREATMENT_LABELS, type LeaveType } from '$lib/features/hr/types';

  let permissions = $state<string[]>([]);
  let denied = $state(false);
  let loading = $state(true);
  let msg = $state('');
  let actionError = $state('');
  let types = $state<LeaveType[]>([]);

  let newType = $state({ code: '', name: '', payroll_treatment: 'PAID' });

  const canEdit = $derived(permissions.includes('hr.leave.edit'));

  async function loadSession() {
    try {
      const s = await api<Session>('/session');
      permissions = s.permissions ?? [];
      denied = !permissions.includes('hr.leave.read');
    } catch {
      denied = true;
    }
  }

  async function load() {
    if (denied) return;
    loading = true;
    try {
      types = (await hr.listLeaveTypes()).items;
    } catch (cause) {
      actionError = cause instanceof APIRequestError ? cause.message : 'İzin türleri yüklenemedi.';
    } finally {
      loading = false;
    }
  }

  async function act(fn: () => Promise<unknown>, ok: string) {
    actionError = '';
    msg = '';
    try {
      await fn();
      msg = ok;
      await load();
    } catch (cause) {
      actionError = cause instanceof APIRequestError ? cause.message : 'İşlem başarısız.';
    }
  }

  function toggleActive(t: LeaveType) {
    void act(
      () =>
        hr.updateLeaveType(t.id, t.version, {
          name: t.name,
          payroll_treatment: t.payroll_treatment,
          is_active: !t.is_active
        }),
      t.is_active ? 'İzin türü pasifleştirildi.' : 'İzin türü etkinleştirildi.'
    );
  }

  onMount(async () => {
    await loadSession();
    await load();
  });
</script>

<svelte:head><title>İzin Türleri · Varya One</title></svelte:head>

{#if denied}
  <section class="card">İzin türlerini görüntüleme yetkiniz yok.</section>
{:else}
  <header class="page-header">
    <div>
      <h1>İzin Türleri</h1>
    </div>
  </header>

  {#if msg}<p class="notice ok" role="status">{msg}</p>{/if}
  {#if actionError}<p class="notice error" role="alert">{actionError}</p>{/if}

  <section class="card">
    {#if canEdit}
      <form
        class="inline"
        onsubmit={(e) => {
          e.preventDefault();
          const payload = { ...newType };
          newType = { code: '', name: '', payroll_treatment: 'PAID' };
          void act(() => hr.createLeaveType(payload), 'İzin türü eklendi.');
        }}
      >
        <Input bind:value={newType.code} placeholder="Kod — boş bırakırsanız otomatik üretilir" />
        <Input bind:value={newType.name} placeholder="Ad" required />
        <select bind:value={newType.payroll_treatment} class="select">
          {#each Object.entries(LEAVE_TREATMENT_LABELS) as [value, label]}
            <option {value}>{label}</option>
          {/each}
        </select>
        <Button type="submit">Ekle</Button>
      </form>
    {/if}
    {#if loading}
      <p class="list-state">Yükleniyor…</p>
    {:else if !types.length}
      <p class="list-state">İzin türü tanımlı değil.</p>
    {:else}
      <div class="table-scroll">
        <table class="data-table">
          <thead
            ><tr
              ><th>Kod</th><th>Ad</th><th>Bordro etkisi</th><th>Aktif</th><th aria-label="İşlemler"
              ></th></tr
            ></thead
          >
          <tbody>
            {#each types as t}
              <tr
                ><td>{t.code}</td><td><strong>{t.name}</strong></td><td class="muted"
                  >{LEAVE_TREATMENT_LABELS[t.payroll_treatment] ?? t.payroll_treatment}</td
                ><td class="muted">{t.is_active ? 'Evet' : 'Hayır'}</td><td class="actions-cell">
                  {#if canEdit}
                    <button class="link" onclick={() => toggleActive(t)}
                      >{t.is_active ? 'Pasifleştir' : 'Etkinleştir'}</button
                    >
                  {/if}
                </td></tr
              >
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </section>
{/if}

<style>
  .card {
    margin-top: 14px;
  }
  .page-header p {
    margin: 4px 0 0;
    font-size: 12px;
    color: var(--text-muted);
    max-width: 60ch;
  }
  .inline {
    display: flex;
    gap: 8px;
    margin-bottom: 12px;
    flex-wrap: wrap;
  }
  .select {
    height: var(--control-height);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    background: var(--surface);
    color: var(--text);
    padding: 0 9px;
    font-size: 12px;
  }
  .list-state {
    padding: 14px 0;
    text-align: center;
    color: var(--text-muted);
    font-size: 13px;
    margin: 0;
  }
  .actions-cell {
    white-space: nowrap;
  }
  .link {
    border: 0;
    background: transparent;
    color: var(--primary);
    cursor: pointer;
    font-size: 12px;
    font-weight: 650;
    padding: 0;
  }
  .link:hover {
    text-decoration: underline;
  }
</style>
