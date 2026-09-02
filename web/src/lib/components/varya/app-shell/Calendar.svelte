<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { toast } from 'svelte-sonner';
  import {
    CalendarDays,
    ChevronLeft,
    ChevronRight,
    Bell,
    BellOff,
    Trash2,
    X,
    GripHorizontal,
    Plus,
    Check,
    RotateCcw
  } from '@lucide/svelte';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { TimeInput } from '$lib/components/varya/time-input';
  import {
    listEvents,
    createEvent,
    deleteEvent,
    markNotified,
    setCompleted,
    type AgendaEvent
  } from '$lib/features/agenda/api';
  import { playBell, isMuted, setMuted } from '$lib/features/agenda/bell';
  import {
    todayISO,
    monthMatrix,
    monthYearLabel,
    longDayLabel,
    groupByDate,
    eventStartMs,
    isPending,
    isDone,
    REMIND_OPTIONS
  } from '$lib/features/agenda/dates';

  const PANEL_W = 344;
  const PANEL_H = 452;

  const WEEKDAYS = ['Pt', 'Sa', 'Ça', 'Pe', 'Cu', 'Ct', 'Pz'];

  let open = $state(false);
  let isDesktop = $state(true);
  let pos = $state<{ x: number; y: number } | null>(null);
  let drag: { dx: number; dy: number } | null = null;

  const initial = new Date();
  let viewYear = $state(initial.getFullYear());
  let viewMonth = $state(initial.getMonth());
  let selectedDate = $state(todayISO());
  let events = $state<AgendaEvent[]>([]);
  let muted = $state(false);

  let formTime = $state('');
  let formTitle = $state('');
  let formRemind = $state(0);
  let saving = $state(false);
  let formError = $state('');

  const eventsByDate = $derived(groupByDate(events));
  const monthLabel = $derived(monthYearLabel(viewYear, viewMonth));
  const cells = $derived(
    monthMatrix(viewYear, viewMonth).map((cell) => {
      const dayEvents = eventsByDate.get(cell.key) ?? [];
      return {
        ...cell,
        count: dayEvents.length,
        hasPending: dayEvents.some((event) => isPending(event))
      };
    })
  );

  const selectedEvents = $derived(eventsByDate.get(selectedDate) ?? []);
  const selectedLabel = $derived(longDayLabel(selectedDate));

  // Drives the topbar icon's own highlight: an unfinished event dated today.
  const todayPendingCount = $derived(
    (eventsByDate.get(todayISO()) ?? []).filter((event) => isPending(event)).length
  );

  function shiftMonth(delta: number) {
    const d = new Date(viewYear, viewMonth + delta, 1);
    viewYear = d.getFullYear();
    viewMonth = d.getMonth();
  }
  function goToday() {
    const n = new Date();
    viewYear = n.getFullYear();
    viewMonth = n.getMonth();
    selectedDate = todayISO();
  }
  function toggleMute() {
    muted = !muted;
    setMuted(muted);
  }

  async function refresh() {
    try {
      events = await listEvents();
    } catch {
      /* keep last-known events */
    }
  }

  async function submit() {
    const title = formTitle.trim();
    if (!title) {
      formError = 'Başlık girin.';
      return;
    }
    saving = true;
    formError = '';
    try {
      const created = await createEvent({
        date: selectedDate,
        time: formTime,
        title,
        remind_minutes: formRemind
      });
      events = [...events, created];
      formTitle = '';
      formTime = '';
      formRemind = 0;
    } catch (cause) {
      formError =
        typeof cause === 'object' && cause && 'message' in cause
          ? String((cause as { message: unknown }).message)
          : 'Etkinlik kaydedilemedi.';
    } finally {
      saving = false;
    }
  }

  async function remove(id: string) {
    const previous = events;
    events = events.filter((event) => event.id !== id);
    try {
      await deleteEvent(id);
    } catch {
      events = previous;
      toast.error('Etkinlik silinemedi.');
    }
  }

  async function toggleDone(target: AgendaEvent) {
    const next = !target.completed_at;
    const stamp = next ? new Date().toISOString() : null;
    events = events.map((event) =>
      event.id === target.id ? { ...event, completed_at: stamp } : event
    );
    try {
      const updated = await setCompleted(target.id, next);
      events = events.map((event) => (event.id === updated.id ? updated : event));
    } catch {
      events = events.map((event) =>
        event.id === target.id ? { ...event, completed_at: target.completed_at } : event
      );
      toast.error('Etkinlik güncellenemedi.');
    }
  }

  async function tick() {
    const now = Date.now();
    const fired: string[] = [];
    const silent: string[] = [];
    for (const event of events) {
      if (event.notified_at) continue;
      const startAt = eventStartMs(event);
      if (Number.isNaN(startAt)) continue;
      const triggerAt = startAt - event.remind_minutes * 60_000;
      if (now > startAt + 60 * 60_000) {
        silent.push(event.id);
      } else if (now >= triggerAt) {
        fired.push(event.id);
        toast(event.title, {
          description: event.time ? `Bugün ${event.time}` : 'Bugün',
          duration: 8000
        });
        void playBell();
      }
    }
    const handled = [...fired, ...silent];
    if (!handled.length) return;
    const done = new Set(handled);
    events = events.map((event) =>
      done.has(event.id) ? { ...event, notified_at: new Date().toISOString() } : event
    );
    try {
      await markNotified(handled);
    } catch {
      /* will retry on next tick / reload */
    }
  }

  function onKeydown(event: KeyboardEvent) {
    if (!open || !isDesktop) return;
    const target = event.target as HTMLElement | null;
    if (
      target &&
      (target.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(target.tagName))
    ) {
      if (event.key === 'Escape') open = false;
      return;
    }
    if (event.key === 'Escape') open = false;
    else if (event.key === 'ArrowLeft') shiftMonth(-1);
    else if (event.key === 'ArrowRight') shiftMonth(1);
  }

  function clamp(x: number, y: number) {
    return {
      x: Math.min(Math.max(8, x), window.innerWidth - PANEL_W - 8),
      y: Math.min(Math.max(8, y), window.innerHeight - PANEL_H - 8)
    };
  }
  function startDrag(event: PointerEvent) {
    if (!pos) return;
    drag = { dx: event.clientX - pos.x, dy: event.clientY - pos.y };
    (event.target as HTMLElement).setPointerCapture(event.pointerId);
  }
  function onDrag(event: PointerEvent) {
    if (!drag) return;
    pos = clamp(event.clientX - drag.dx, event.clientY - drag.dy);
  }
  function endDrag(event: PointerEvent) {
    drag = null;
    try {
      (event.target as HTMLElement).releasePointerCapture(event.pointerId);
    } catch {
      /* noop */
    }
  }

  function toggle() {
    open = !open;
    if (open) {
      pos = clamp((window.innerWidth - PANEL_W) / 2, (window.innerHeight - PANEL_H) / 2 - 40);
      goToday();
      void refresh();
    }
  }

  let timer: ReturnType<typeof setInterval> | undefined;
  let reload: ReturnType<typeof setInterval> | undefined;

  function onVisibility() {
    if (document.visibilityState === 'visible') {
      void refresh().then(tick);
    }
  }

  onMount(() => {
    muted = isMuted();
    const mq = window.matchMedia('(min-width: 981px)');
    const update = () => {
      isDesktop = mq.matches;
      if (!isDesktop) open = false;
    };
    update();
    mq.addEventListener('change', update);

    void refresh().then(tick);
    timer = setInterval(tick, 30_000);
    reload = setInterval(refresh, 5 * 60_000);
    document.addEventListener('visibilitychange', onVisibility);

    return () => {
      mq.removeEventListener('change', update);
      document.removeEventListener('visibilitychange', onVisibility);
    };
  });

  onDestroy(() => {
    if (timer) clearInterval(timer);
    if (reload) clearInterval(reload);
  });
</script>

<svelte:window onkeydown={onKeydown} />

{#if isDesktop}
  <span class="calendar-slot" class:has-today={todayPendingCount > 0}>
    <Button
      variant="ghost"
      size="icon"
      class="calendar-toggle"
      aria-label={todayPendingCount > 0 ? `Takvim — bugün ${todayPendingCount} etkinlik` : 'Takvim'}
      aria-pressed={open}
      title={todayPendingCount > 0 ? `Bugün ${todayPendingCount} etkinlik` : 'Takvim'}
      onclick={toggle}><CalendarDays size={17} /></Button
    >
    {#if todayPendingCount > 0}
      <span class="calendar-badge" aria-hidden="true">{todayPendingCount}</span>
    {/if}
  </span>
{/if}

{#if open && pos && isDesktop}
  <div class="cal-panel" style="left:{pos.x}px; top:{pos.y}px;" role="dialog" aria-label="Takvim">
    <div
      class="cal-head"
      role="toolbar"
      tabindex="-1"
      aria-label="Pencereyi taşı"
      onpointerdown={startDrag}
      onpointermove={onDrag}
      onpointerup={endDrag}
    >
      <GripHorizontal size={15} aria-hidden="true" />
      <span>Takvim</span>
      <button
        type="button"
        class="cal-icon-btn"
        aria-label={muted ? 'Zil sesini aç' : 'Zil sesini kapat'}
        aria-pressed={muted}
        title={muted ? 'Zil kapalı' : 'Zil açık'}
        onclick={toggleMute}
      >
        {#if muted}<BellOff size={14} />{:else}<Bell size={14} />{/if}
      </button>
      <button type="button" class="cal-icon-btn" aria-label="Kapat" onclick={() => (open = false)}>
        <X size={14} />
      </button>
    </div>

    <div class="cal-monthbar">
      <button type="button" class="cal-nav" aria-label="Önceki ay" onclick={() => shiftMonth(-1)}>
        <ChevronLeft size={16} />
      </button>
      <strong>{monthLabel}</strong>
      <button type="button" class="cal-nav" aria-label="Sonraki ay" onclick={() => shiftMonth(1)}>
        <ChevronRight size={16} />
      </button>
      <button type="button" class="cal-today" onclick={goToday}>Bugün</button>
    </div>

    <div class="cal-grid cal-weekdays">
      {#each WEEKDAYS as label}<span>{label}</span>{/each}
    </div>
    <div class="cal-grid cal-days">
      {#each cells as cell}
        <button
          type="button"
          class="cal-day"
          class:out={!cell.inMonth}
          class:is-today={cell.isToday}
          class:selected={cell.key === selectedDate}
          class:has-pending={cell.hasPending}
          onclick={() => (selectedDate = cell.key)}
        >
          <span>{cell.day}</span>
          {#if cell.count > 0}
            <em class="cal-dot" class:many={cell.count > 1} class:pending={cell.hasPending}
              >{cell.count > 1 ? cell.count : ''}</em
            >
          {/if}
        </button>
      {/each}
    </div>

    <div class="cal-detail">
      <div class="cal-detail-head">{selectedLabel}</div>
      <div class="cal-events">
        {#if selectedEvents.length === 0}
          <p class="cal-empty">Bu güne etkinlik yok.</p>
        {:else}
          {#each selectedEvents as event (event.id)}
            <div class="cal-event" class:pending={isPending(event)} class:done={isDone(event)}>
              <button
                type="button"
                class="cal-check"
                class:checked={isDone(event)}
                aria-label={isDone(event)
                  ? 'Tamamlanmadı olarak işaretle'
                  : 'Tamamlandı olarak işaretle'}
                aria-pressed={isDone(event)}
                onclick={() => toggleDone(event)}
              >
                {#if isDone(event)}<RotateCcw size={12} />{:else}<Check size={12} />{/if}
              </button>
              <span class="cal-time">{event.time || 'Tüm gün'}</span>
              <span class="cal-title">{event.title}</span>
              <button
                type="button"
                class="cal-icon-btn cal-del"
                aria-label="Etkinliği sil"
                onclick={() => remove(event.id)}><Trash2 size={13} /></button
              >
            </div>
          {/each}
        {/if}
      </div>

      <form
        class="cal-form"
        onsubmit={(e) => {
          e.preventDefault();
          void submit();
        }}
      >
        <div class="cal-form-row">
          <TimeInput bind:value={formTime} ariaLabel="Etkinlik saati" />
          <Input
            bind:value={formTitle}
            placeholder="Etkinlik başlığı"
            aria-label="Etkinlik başlığı"
            maxlength={200}
          />
        </div>
        <div class="cal-form-row">
          <select bind:value={formRemind} aria-label="Hatırlatma">
            {#each REMIND_OPTIONS as option}<option value={option.value}>{option.label}</option
              >{/each}
          </select>
          <Button type="submit" size="sm" disabled={saving}>
            <Plus size={14} /> Ekle
          </Button>
        </div>
        {#if formError}<p class="cal-form-error">{formError}</p>{/if}
      </form>
    </div>
  </div>
{/if}

<style>
  .calendar-slot {
    position: relative;
    display: inline-flex;
    border-radius: var(--radius-control, 6px);
  }
  .calendar-slot.has-today :global(.calendar-toggle) {
    color: var(--warning, #d97706);
    background: color-mix(in srgb, var(--warning, #d97706) 14%, transparent);
    animation: calendar-slot-pulse 2s ease-in-out infinite;
  }
  .calendar-badge {
    position: absolute;
    top: -2px;
    right: -2px;
    min-width: 15px;
    height: 15px;
    padding: 0 3px;
    display: grid;
    place-items: center;
    border-radius: 999px;
    background: var(--warning, #d97706);
    color: #fff;
    font-size: 9px;
    font-weight: 700;
    line-height: 1;
    pointer-events: none;
  }
  @keyframes calendar-slot-pulse {
    0%,
    100% {
      box-shadow: 0 0 0 0 color-mix(in srgb, var(--warning, #d97706) 45%, transparent);
    }
    60% {
      box-shadow: 0 0 0 5px transparent;
    }
  }
  @media (prefers-reduced-motion: reduce) {
    .calendar-slot.has-today :global(.calendar-toggle) {
      animation: none;
    }
  }

  .cal-panel {
    position: fixed;
    z-index: 2147483000;
    width: 344px;
    display: flex;
    flex-direction: column;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-panel);
    background: var(--surface);
    box-shadow: 0 24px 70px rgb(2 6 23 / 32%);
    overflow: hidden;
    user-select: none;
  }
  .cal-head {
    display: flex;
    align-items: center;
    gap: 7px;
    padding: 7px 8px 7px 10px;
    background: var(--surface-muted);
    border-bottom: 1px solid var(--border);
    color: var(--text-muted);
    font-size: 11px;
    font-weight: 650;
    cursor: grab;
    touch-action: none;
  }
  .cal-head:active {
    cursor: grabbing;
  }
  .cal-head span {
    flex: 1;
  }
  .cal-icon-btn {
    display: inline-grid;
    place-items: center;
    width: 22px;
    height: 22px;
    border: 0;
    border-radius: 5px;
    background: transparent;
    color: var(--text-muted);
    cursor: pointer;
    text-decoration: none;
  }
  .cal-icon-btn:hover {
    background: var(--surface);
    color: var(--text);
  }
  .cal-icon-btn[aria-pressed='true'] {
    color: var(--primary);
  }
  .cal-monthbar {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 9px 10px;
  }
  .cal-monthbar strong {
    flex: 1;
    text-align: center;
    color: var(--text);
    font-size: 13px;
    font-weight: 650;
    text-transform: capitalize;
  }
  .cal-nav {
    display: inline-grid;
    place-items: center;
    width: 26px;
    height: 26px;
    border: 1px solid var(--border);
    border-radius: 6px;
    background: var(--surface);
    color: var(--text);
    cursor: pointer;
  }
  .cal-nav:hover {
    background: var(--surface-muted);
  }
  .cal-today {
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 0 8px;
    height: 26px;
    background: var(--surface);
    color: var(--text-muted);
    font-size: 11px;
    font-weight: 600;
    cursor: pointer;
  }
  .cal-today:hover {
    background: var(--surface-muted);
    color: var(--text);
  }
  .cal-grid {
    display: grid;
    grid-template-columns: repeat(7, 1fr);
    gap: 2px;
    padding: 0 10px;
  }
  .cal-weekdays span {
    text-align: center;
    padding: 2px 0 6px;
    color: var(--text-muted);
    font-size: 10px;
    font-weight: 650;
    text-transform: uppercase;
  }
  .cal-day {
    position: relative;
    display: flex;
    align-items: center;
    justify-content: center;
    height: 32px;
    border: 1px solid transparent;
    border-radius: 6px;
    background: transparent;
    color: var(--text);
    font-size: 12px;
    font-weight: 550;
    cursor: pointer;
  }
  .cal-day:hover {
    background: var(--surface-muted);
  }
  .cal-day.out {
    color: var(--text-muted);
    opacity: 0.5;
  }
  .cal-day.is-today {
    border-color: var(--primary);
    color: var(--primary);
    font-weight: 700;
  }
  .cal-day.selected {
    background: var(--primary-soft);
    color: var(--primary);
  }
  .cal-dot {
    position: absolute;
    bottom: 3px;
    left: 50%;
    transform: translateX(-50%);
    min-width: 5px;
    height: 5px;
    padding: 0 2px;
    border-radius: 999px;
    background: var(--primary);
    color: var(--primary-foreground, #fff);
    font-size: 8px;
    font-style: normal;
    font-weight: 700;
    line-height: 5px;
  }
  .cal-dot.many {
    height: auto;
    padding: 1px 3px;
    line-height: 1;
  }
  .cal-dot.pending {
    background: var(--warning, #d97706);
    animation: cal-pulse 1.6s ease-in-out infinite;
  }
  .cal-day.has-pending:not(.is-today) {
    border-color: color-mix(in srgb, var(--warning, #d97706) 55%, transparent);
  }
  @keyframes cal-pulse {
    0%,
    100% {
      box-shadow: 0 0 0 0 color-mix(in srgb, var(--warning, #d97706) 60%, transparent);
    }
    50% {
      box-shadow: 0 0 0 3px transparent;
    }
  }
  .cal-detail {
    margin-top: 8px;
    border-top: 1px solid var(--border);
    padding: 10px;
  }
  .cal-detail-head {
    color: var(--text);
    font-size: 12px;
    font-weight: 650;
    text-transform: capitalize;
    margin-bottom: 6px;
  }
  .cal-events {
    max-height: 132px;
    overflow-y: auto;
    display: flex;
    flex-direction: column;
    gap: 3px;
  }
  .cal-empty {
    margin: 0;
    padding: 8px 0;
    color: var(--text-muted);
    font-size: 11px;
  }
  .cal-event {
    display: flex;
    align-items: center;
    gap: 7px;
    padding: 5px 6px;
    border-radius: 6px;
    background: var(--surface-muted);
    border-left: 2px solid transparent;
  }
  .cal-event:hover .cal-del {
    opacity: 1;
  }
  .cal-event.pending {
    background: color-mix(in srgb, var(--warning, #d97706) 14%, var(--surface));
    border-left-color: var(--warning, #d97706);
  }
  .cal-event.done {
    opacity: 0.6;
  }
  .cal-event.done .cal-title {
    text-decoration: line-through;
  }
  .cal-event.done .cal-time {
    color: var(--text-muted);
  }
  .cal-check {
    display: inline-grid;
    place-items: center;
    width: 18px;
    height: 18px;
    flex-shrink: 0;
    border: 1px solid var(--border-strong);
    border-radius: 999px;
    background: var(--surface);
    color: var(--text-muted);
    cursor: pointer;
  }
  .cal-check:hover {
    border-color: var(--primary);
    color: var(--primary);
  }
  .cal-check.checked {
    background: var(--primary);
    border-color: var(--primary);
    color: var(--primary-foreground, #fff);
  }
  .cal-time {
    color: var(--primary);
    font-size: 11px;
    font-weight: 700;
    font-variant-numeric: tabular-nums;
    flex-shrink: 0;
  }
  .cal-title {
    flex: 1;
    color: var(--text);
    font-size: 12px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .cal-del {
    opacity: 0;
    color: var(--danger);
  }
  .cal-form {
    margin-top: 8px;
    display: flex;
    flex-direction: column;
    gap: 6px;
  }
  .cal-form-row {
    display: flex;
    gap: 6px;
    align-items: center;
  }
  .cal-form-row :global(input) {
    height: 30px;
    font-size: 12px;
  }
  .cal-form select {
    flex: 1;
    height: 30px;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    background: var(--surface);
    color: var(--text);
    font-size: 12px;
    padding: 0 6px;
  }
  .cal-form-error {
    margin: 0;
    color: var(--danger);
    font-size: 11px;
  }
</style>
