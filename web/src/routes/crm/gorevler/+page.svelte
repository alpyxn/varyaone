<script lang="ts">
  import { onMount } from 'svelte';
  import { CalendarClock, RefreshCw } from '@lucide/svelte';
  import { api, APIRequestError, type Session } from '$lib/api';
  import { Button } from '$lib/components/ui/button';
  import { formatDate } from '$lib/design/formatters';
  import { listTasks, setTaskDone } from '$lib/features/crm/api';
  import type { Activity } from '$lib/features/crm/types';

  type TaskState = 'today' | 'overdue' | 'open' | 'done';

  let permissions = $state<string[]>([]);
  let userID = $state('');
  let denied = $state(false);
  let loading = $state(true);
  let error = $state('');
  let tasks = $state<Activity[]>([]);
  let taskState = $state<TaskState>('today');
  let scope = $state<'mine' | 'all'>('mine');

  const canSeeAll = $derived(permissions.includes('crm.view_all'));
  const canEdit = $derived(permissions.includes('crm.edit'));
  const canManage = $derived(permissions.includes('crm.manage'));

  const tabs: { key: TaskState; label: string }[] = [
    { key: 'today', label: 'Bugün' },
    { key: 'overdue', label: 'Geciken' },
    { key: 'open', label: 'Tüm açık' },
    { key: 'done', label: 'Tamamlanan' }
  ];

  async function load() {
    if (denied) return;
    loading = true;
    error = '';
    try {
      tasks = (await listTasks({ scope, state: taskState })).items;
    } catch (cause) {
      error = cause instanceof APIRequestError ? cause.message : 'Görevler yüklenemedi.';
    } finally {
      loading = false;
    }
  }

  async function toggle(task: Activity) {
    try {
      const updated = await setTaskDone(task.id, !task.done_at);
      // Tamamlanan görev açık listelerden, yeniden açılan görev tamamlananlardan düşer.
      tasks = tasks.filter((item) => item.id !== updated.id);
    } catch (cause) {
      error = cause instanceof APIRequestError ? cause.message : 'Görev güncellenemedi.';
    }
  }

  function overdue(task: Activity) {
    return !task.done_at && task.due_at && new Date(task.due_at).getTime() < Date.now();
  }

  function targetHref(task: Activity) {
    if (task.lead_id) return `/crm/adaylar/${task.lead_id}`;
    if (task.party_id) return `/cari/kartlar/${task.party_id}`;
    return undefined;
  }

  onMount(async () => {
    try {
      const session = await api<Session>('/session');
      permissions = session.permissions ?? [];
      userID = session.user.id;
      denied = !permissions.includes('crm.read');
    } catch {
      denied = true;
    }
    await load();
    if (denied) loading = false;
  });
</script>

<svelte:head><title>Görevler · Varya One</title></svelte:head>

{#if denied}
  <section class="panel error-panel" role="alert">
    <strong>Erişim yok.</strong>
    <span>CRM görevlerini görüntüleme yetkiniz bulunmuyor.</span>
  </section>
{:else}
  <header class="page-header">
    <div>
      <h1>Görevler</h1>
      <p class="meta">Geri aramalar ve takip edilecek işler.</p>
    </div>
    <div class="page-actions">
      {#if canSeeAll}
        <select
          class="select"
          bind:value={scope}
          onchange={() => void load()}
          aria-label="Kimin görevleri"
        >
          <option value="mine">Benim görevlerim</option>
          <option value="all">Bütün ekip</option>
        </select>
      {/if}
      <Button variant="outline" onclick={() => void load()} disabled={loading}>
        <RefreshCw size={14} data-icon="inline-start" aria-hidden="true" />Yenile
      </Button>
    </div>
  </header>

  <section class="panel filters">
    <div class="views" role="tablist" aria-label="Görev durumu">
      {#each tabs as tab (tab.key)}
        <button
          type="button"
          role="tab"
          aria-selected={taskState === tab.key}
          class:active={taskState === tab.key}
          onclick={() => {
            taskState = tab.key;
            void load();
          }}>{tab.label}</button
        >
      {/each}
    </div>
  </section>

  {#if error}<p class="notice error" role="alert">{error}</p>{/if}

  <section class="panel list">
    {#if loading}
      <p class="state" role="status">Yükleniyor…</p>
    {:else if !tasks.length}
      <p class="state">
        {taskState === 'done' ? 'Tamamlanan görev yok.' : 'Bekleyen görev yok.'}
      </p>
    {:else}
      <ul>
        {#each tasks as task (task.id)}
          {@const href = targetHref(task)}
          <li>
            <input
              type="checkbox"
              checked={Boolean(task.done_at)}
              disabled={!canEdit || (!canManage && task.assigned_user_id !== userID)}
              onchange={() => void toggle(task)}
              aria-label="{task.subject} tamamlandı"
            />
            <div class="task">
              <div class="task-top">
                <strong class:done={task.done_at}>{task.subject}</strong>
                {#if href}
                  <a {href}>{task.lead_name || task.party_name}</a>
                {/if}
              </div>
              {#if task.body}<p class="body">{task.body}</p>{/if}
              <p class="meta-line">
                <CalendarClock size={12} aria-hidden="true" />
                <span class:overdue={overdue(task)}
                  >{formatDate(task.due_at ?? undefined, true)}</span
                >
                {#if scope === 'all'}· {task.assigned_name}{/if}
                {#if task.done_at}· tamamlandı {formatDate(task.done_at, true)}{/if}
              </p>
            </div>
          </li>
        {/each}
      </ul>
    {/if}
  </section>
{/if}

<style>
  .page-header {
    display: flex;
    justify-content: space-between;
    align-items: flex-start;
    gap: 12px;
    flex-wrap: wrap;
  }
  .page-actions {
    display: flex;
    gap: 8px;
    flex-wrap: wrap;
    align-items: center;
  }
  .meta {
    margin: 4px 0 0;
    color: var(--text-muted);
    font-size: 12px;
  }
  .panel {
    margin-top: 16px;
    padding: 12px 14px;
  }
  .views {
    display: flex;
    gap: 6px;
    flex-wrap: wrap;
  }
  .views button {
    height: 28px;
    padding: 0 12px;
    border: 1px solid var(--border-strong);
    border-radius: 999px;
    background: var(--surface);
    color: var(--text);
    font-size: 12px;
    cursor: pointer;
  }
  .views button.active {
    border-color: var(--primary);
    background: color-mix(in srgb, var(--primary) 12%, transparent);
    color: var(--primary);
    font-weight: 600;
  }
  .select {
    height: var(--control-height);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    background: var(--surface);
    color: var(--text);
    padding: 0 10px;
    font-size: 13px;
  }
  ul {
    list-style: none;
    margin: 0;
    padding: 0;
  }
  li {
    display: flex;
    gap: 10px;
    align-items: flex-start;
    padding: 10px 0;
    border-bottom: 1px solid var(--border);
  }
  li input {
    margin-top: 3px;
  }
  .task {
    min-width: 0;
    flex: 1;
  }
  .task-top {
    display: flex;
    gap: 8px;
    flex-wrap: wrap;
    align-items: baseline;
    font-size: 13px;
  }
  .task-top a {
    color: var(--primary);
    font-size: 12px;
  }
  .done {
    text-decoration: line-through;
    color: var(--text-muted);
  }
  .body {
    margin: 4px 0 0;
    font-size: 12px;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }
  .meta-line {
    display: flex;
    align-items: center;
    gap: 4px;
    margin: 4px 0 0;
    color: var(--text-muted);
    font-size: 11px;
  }
  .overdue {
    color: var(--danger);
    font-weight: 600;
  }
  .state {
    margin: 0;
    padding: 16px 0;
    color: var(--text-muted);
    font-size: 13px;
    text-align: center;
  }
</style>
