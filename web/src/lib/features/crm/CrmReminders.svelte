<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { toast } from 'svelte-sonner';
  import { playBell } from '$lib/features/agenda/bell';
  import { listReminders, markRemindersNotified } from './api';
  import type { Activity } from './types';

  // Rings the agenda bell for CRM follow-up tasks ("tekrar ara" callbacks and
  // other tasks) the same way the calendar rings personal events: at
  // due_at - remind_minutes, once, then reports the task back so other tabs
  // and devices stay quiet.
  let { enabled = false }: { enabled?: boolean } = $props();

  let tasks: Activity[] = [];
  let timer: ReturnType<typeof setInterval> | undefined;
  let reload: ReturnType<typeof setInterval> | undefined;

  async function refresh() {
    if (!enabled) return;
    try {
      tasks = (await listReminders()).items;
    } catch {
      /* keep last-known reminders */
    }
  }

  async function tick() {
    if (!enabled || !tasks.length) return;
    const now = Date.now();
    const handled: string[] = [];
    for (const task of tasks) {
      if (!task.due_at) continue;
      const dueAt = new Date(task.due_at).getTime();
      const triggerAt = dueAt - task.remind_minutes * 60_000;
      if (now > dueAt + 60 * 60_000) {
        handled.push(task.id);
      } else if (now >= triggerAt) {
        handled.push(task.id);
        const href = task.lead_id
          ? `/crm/adaylar/${task.lead_id}`
          : task.party_id
            ? `/cari/kartlar/${task.party_id}`
            : '/crm/gorevler';
        toast(task.subject, {
          description: [
            task.lead_name || task.party_name,
            new Date(dueAt).toLocaleTimeString('tr-TR', { hour: '2-digit', minute: '2-digit' })
          ]
            .filter(Boolean)
            .join(' · '),
          duration: 15000,
          action: { label: 'Aç', onClick: () => void goto(href) }
        });
        void playBell();
      }
    }
    if (!handled.length) return;
    const done = new Set(handled);
    tasks = tasks.filter((task) => !done.has(task.id));
    try {
      await markRemindersNotified(handled);
    } catch {
      /* rings again on the next reload at worst */
    }
  }

  function onVisibility() {
    if (document.visibilityState === 'visible') void refresh().then(tick);
  }

  $effect(() => {
    if (enabled) void refresh().then(tick);
  });

  onMount(() => {
    timer = setInterval(() => void tick(), 30_000);
    reload = setInterval(() => void refresh(), 5 * 60_000);
    document.addEventListener('visibilitychange', onVisibility);
    return () => {
      clearInterval(timer);
      clearInterval(reload);
      document.removeEventListener('visibilitychange', onVisibility);
    };
  });
</script>
