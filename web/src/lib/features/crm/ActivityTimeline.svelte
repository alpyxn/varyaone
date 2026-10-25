<script lang="ts">
  import { DateTimeInput } from '$lib/components/varya/date-time-input';
  import { CalendarClock, LoaderCircle } from '@lucide/svelte';
  import { APIRequestError } from '$lib/api';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { formatDate } from '$lib/design/formatters';
  import { setTaskDone } from './api';
  import {
    activityKindLabel,
    callOutcomeLabel,
    callOutcomes,
    callOutcomeTone,
    formatDuration,
    fromLocalInput,
    type Activity,
    type ActivityInput,
    type CallOutcome,
    type Member
  } from './types';

  let {
    activities = $bindable([]),
    create,
    canEdit = false,
    canManage = false,
    userID = '',
    members = [],
    allowCall = false,
    showOrigin = false,
    title = 'Geçmiş',
    onCreated
  }: {
    activities: Activity[];
    /** Omit to hide the entry form. */
    create?: (input: ActivityInput) => Promise<Activity>;
    canEdit?: boolean;
    canManage?: boolean;
    userID?: string;
    members?: Member[];
    /** Offer "Arama" with a result (cari timelines; lead calls use the call panel). */
    allowCall?: boolean;
    /** Show which lead/opportunity an entry came from (cari timeline). */
    showOrigin?: boolean;
    title?: string;
    onCreated?: (activity: Activity) => void;
  } = $props();

  const id = $props.id();
  type EntryKind = 'NOTE' | 'MEETING' | 'EMAIL' | 'TASK' | 'CALL';
  const kinds = $derived<EntryKind[]>(
    allowCall ? ['NOTE', 'CALL', 'MEETING', 'EMAIL', 'TASK'] : ['NOTE', 'MEETING', 'EMAIL', 'TASK']
  );
  let kind = $state<EntryKind>('NOTE');
  let subject = $state('');
  let body = $state('');
  let due = $state('');
  let assignee = $state('');
  let callOutcome = $state<CallOutcome>('INTERESTED');
  let saving = $state(false);
  let error = $state('');

  async function save(event: SubmitEvent) {
    event.preventDefault();
    if (!create || saving) return;
    if (kind === 'TASK' && (!subject.trim() || !due)) {
      error = 'Görev için konu ve tarih girin.';
      return;
    }
    if (kind !== 'TASK' && kind !== 'CALL' && !subject.trim() && !body.trim()) {
      error = 'Konu ya da açıklama girin.';
      return;
    }
    saving = true;
    error = '';
    try {
      const created = await create({
        kind,
        subject: subject.trim(),
        body: body.trim(),
        call_outcome: kind === 'CALL' ? callOutcome : '',
        due_at: kind === 'TASK' ? fromLocalInput(due) : '',
        remind_minutes: kind === 'TASK' ? 10 : 0,
        assigned_user_id: kind === 'TASK' ? assignee : ''
      });
      activities = [created, ...activities];
      subject = '';
      body = '';
      due = '';
      onCreated?.(created);
    } catch (cause) {
      error = cause instanceof APIRequestError ? cause.message : 'Kayıt eklenemedi.';
    } finally {
      saving = false;
    }
  }

  async function toggle(task: Activity) {
    try {
      const updated = await setTaskDone(task.id, !task.done_at);
      activities = activities.map((item) => (item.id === updated.id ? updated : item));
    } catch (cause) {
      error = cause instanceof APIRequestError ? cause.message : 'Görev güncellenemedi.';
    }
  }
</script>

{#if create && canEdit}
  <section class="panel entry" aria-labelledby="{id}-entry">
    <h2 id="{id}-entry">Kayıt ekle</h2>
    <form onsubmit={save}>
      <div class="kinds" role="radiogroup" aria-label="Kayıt türü">
        {#each kinds as item (item)}
          <button
            type="button"
            role="radio"
            aria-checked={kind === item}
            class:active={kind === item}
            onclick={() => (kind = item)}>{activityKindLabel(item)}</button
          >
        {/each}
      </div>
      {#if kind === 'CALL'}
        <select class="select" bind:value={callOutcome} aria-label="Arama sonucu">
          {#each callOutcomes as outcome (outcome)}
            <option value={outcome}>{callOutcomeLabel(outcome)}</option>
          {/each}
        </select>
      {/if}
      <Input
        bind:value={subject}
        maxlength={200}
        placeholder={kind === 'TASK' ? 'Ne yapılacak?' : 'Konu'}
        aria-label="Konu"
      />
      <textarea
        class="textarea"
        rows="2"
        maxlength={4000}
        bind:value={body}
        placeholder="Açıklama"
        aria-label="Açıklama"
      ></textarea>
      {#if kind === 'TASK'}
        <div class="task-row">
          <DateTimeInput bind:value={due} ariaLabel="Görev tarihi" />
          {#if canManage}
            <select class="select" bind:value={assignee} aria-label="Görevi alan">
              <option value="">Bana</option>
              {#each members.filter((m) => m.user_id !== userID) as member (member.user_id)}
                <option value={member.user_id}>{member.display_name}</option>
              {/each}
            </select>
          {/if}
        </div>
      {/if}
      {#if error}<p class="notice error" role="alert">{error}</p>{/if}
      <div class="entry-foot">
        <Button type="submit" disabled={saving}>
          {#if saving}<LoaderCircle data-icon="inline-start" aria-hidden="true" />{/if}Ekle
        </Button>
      </div>
    </form>
  </section>
{/if}

<section class="panel timeline" aria-labelledby="{id}-timeline">
  <h2 id="{id}-timeline">{title}</h2>
  {#if !activities.length}
    <p class="state">Henüz kayıt yok.</p>
  {:else}
    <ol>
      {#each activities as item (item.id)}
        <li class="entry-item kind-{item.kind.toLowerCase()}">
          <div class="entry-top">
            {#if item.kind === 'TASK'}
              <label class="task-check">
                <input
                  type="checkbox"
                  checked={Boolean(item.done_at)}
                  disabled={!canEdit || (!canManage && item.assigned_user_id !== userID)}
                  onchange={() => void toggle(item)}
                />
                <span class:done={item.done_at}>{item.subject}</span>
              </label>
            {:else}
              <strong>{activityKindLabel(item.kind)}</strong>
              {#if item.call_outcome}
                <span class="pill {callOutcomeTone(item.call_outcome)}"
                  >{callOutcomeLabel(item.call_outcome)}</span
                >
              {/if}
              {#if item.subject}<span>{item.subject}</span>{/if}
            {/if}
            {#if showOrigin && item.lead_id && item.lead_name}
              <a class="origin" href="/crm/adaylar/{item.lead_id}">aday: {item.lead_name}</a>
            {/if}
            {#if showOrigin && item.opportunity_id}
              <a class="origin" href="/crm/firsatlar/{item.opportunity_id}">fırsat</a>
            {/if}
          </div>
          {#if item.body}<p class="body">{item.body}</p>{/if}
          <p class="entry-meta">
            {#if item.kind === 'TASK' && item.due_at}
              <CalendarClock size={12} aria-hidden="true" />
              <span class:overdue={!item.done_at && new Date(item.due_at).getTime() < Date.now()}
                >{formatDate(item.due_at, true)}</span
              >
              · {item.assigned_name || '—'}
            {:else}
              {formatDate(item.occurred_at, true)} · {item.created_by_name}
              {#if item.duration_seconds != null}· {formatDuration(item.duration_seconds)}{/if}
            {/if}
          </p>
        </li>
      {/each}
    </ol>
  {/if}
</section>

<style>
  .panel {
    margin-top: 16px;
    padding: 14px 16px;
  }
  .panel h2 {
    margin: 0 0 10px;
    font-size: 14px;
  }
  .entry form {
    display: grid;
    gap: 8px;
  }
  .kinds {
    display: flex;
    gap: 6px;
    flex-wrap: wrap;
  }
  .kinds button {
    height: 26px;
    padding: 0 10px;
    border: 1px solid var(--border-strong);
    border-radius: 999px;
    background: var(--surface);
    color: var(--text);
    font-size: 12px;
    cursor: pointer;
  }
  .kinds button.active {
    border-color: var(--primary);
    background: color-mix(in srgb, var(--primary) 12%, transparent);
    color: var(--primary);
    font-weight: 600;
  }
  .task-row {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 180px), 1fr));
    gap: 8px;
  }
  .entry-foot {
    display: flex;
    justify-content: flex-end;
  }
  .textarea,
  .select {
    width: 100%;
    box-sizing: border-box;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    background: var(--surface);
    color: var(--text);
    font-size: 13px;
  }
  .textarea {
    padding: 8px 10px;
    font: inherit;
    font-size: 13px;
    resize: vertical;
  }
  .select {
    height: var(--control-height);
    padding: 0 10px;
  }
  .timeline ol {
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .entry-item {
    padding: 10px 0 10px 12px;
    border-left: 2px solid var(--border);
    border-bottom: 1px solid var(--border);
  }
  .entry-item.kind-call {
    border-left-color: var(--primary);
  }
  .entry-item.kind-task {
    border-left-color: var(--warning);
  }
  .entry-top {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
    font-size: 13px;
  }
  .origin {
    color: var(--text-muted);
    font-size: 11px;
  }
  .task-check {
    display: flex;
    align-items: center;
    gap: 8px;
    cursor: pointer;
  }
  .done {
    text-decoration: line-through;
    color: var(--text-muted);
  }
  .body {
    margin: 4px 0 0;
    font-size: 13px;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }
  .entry-meta {
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
  .pill {
    display: inline-block;
    padding: 1px 7px;
    border-radius: 999px;
    font-size: 11px;
    font-weight: 600;
    background: color-mix(in srgb, var(--text-muted) 14%, transparent);
  }
  .pill.positive {
    background: color-mix(in srgb, var(--success) 16%, transparent);
    color: var(--success);
  }
  .pill.negative {
    background: color-mix(in srgb, var(--danger) 14%, transparent);
    color: var(--danger);
  }
  .pill.pending {
    background: color-mix(in srgb, var(--warning) 16%, transparent);
    color: var(--warning);
  }
  .state {
    margin: 0;
    padding: 12px 0;
    color: var(--text-muted);
    font-size: 13px;
    text-align: center;
  }
</style>
