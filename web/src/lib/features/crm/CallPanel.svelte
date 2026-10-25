<script lang="ts">
  import { DateTimeInput } from '$lib/components/varya/date-time-input';
  import { onDestroy } from 'svelte';
  import { Ban, LoaderCircle, Phone, Play, Square } from '@lucide/svelte';
  import { APIRequestError } from '$lib/api';
  import { Button } from '$lib/components/ui/button';
  import * as Field from '$lib/components/ui/field';
  import { logCall } from './api';
  import {
    callbackPreset,
    callOutcomeLabel,
    callOutcomes,
    callOutcomeTone,
    formatDuration,
    fromLocalInput,
    telHref,
    toLocalInput,
    type CallOutcome,
    type CallResult,
    type Lead
  } from './types';

  let {
    lead,
    callListItemId = '',
    nextLabel = 'Kaydet ve sıradaki',
    onSaved,
    onDoNotCall
  }: {
    lead: Lead;
    /** Set when the call comes from a call list; the list's retry rules then apply. */
    callListItemId?: string;
    nextLabel?: string;
    onSaved: (result: CallResult, goNext: boolean) => void | Promise<void>;
    onDoNotCall?: () => void;
  } = $props();

  let outcome = $state<CallOutcome | ''>('');
  let note = $state('');
  let callbackAt = $state('');
  let remindMinutes = $state(10);
  let startedAt = $state<number | null>(null);
  let elapsed = $state(0);
  let recordedSeconds = $state<number | null>(null);
  let saving = $state(false);
  let error = $state('');
  let timer: ReturnType<typeof setInterval> | undefined;

  const needsCallbackTime = $derived(outcome === 'CALLBACK');
  const offersCallbackTime = $derived(
    outcome !== '' && outcome !== 'NOT_INTERESTED' && outcome !== 'DO_NOT_CALL'
  );

  // A different lead (next in the queue) starts with a clean panel.
  let panelFor = '';
  $effect(() => {
    if (lead.id !== panelFor) {
      panelFor = lead.id;
      reset();
    }
  });

  function startTimer() {
    stopTimer(false);
    startedAt = Date.now();
    elapsed = 0;
    recordedSeconds = null;
    timer = setInterval(() => {
      if (startedAt) elapsed = Math.round((Date.now() - startedAt) / 1000);
    }, 1000);
  }

  function stopTimer(record = true) {
    if (timer) clearInterval(timer);
    timer = undefined;
    if (record && startedAt) recordedSeconds = Math.round((Date.now() - startedAt) / 1000);
    startedAt = null;
  }

  function dial(phone: string) {
    startTimer();
    window.location.href = telHref(phone);
  }

  function choose(next: CallOutcome) {
    outcome = next;
    error = '';
    if (startedAt) stopTimer();
    if (next === 'CALLBACK' && !callbackAt) {
      callbackAt = toLocalInput(callbackPreset('tomorrow').toISOString());
    }
  }

  function setPreset(preset: 'hour' | 'tomorrow' | 'week') {
    callbackAt = toLocalInput(callbackPreset(preset).toISOString());
  }

  function reset() {
    outcome = '';
    note = '';
    callbackAt = '';
    recordedSeconds = null;
    elapsed = 0;
    error = '';
    stopTimer(false);
  }

  async function save(goNext: boolean) {
    if (!outcome || saving) return;
    if (needsCallbackTime && !callbackAt) {
      error = 'Tekrar arama için tarih ve saat seçin.';
      return;
    }
    if (startedAt) stopTimer();
    saving = true;
    error = '';
    try {
      const result = await logCall(lead.id, {
        call_list_item_id: callListItemId || undefined,
        outcome,
        note: note.trim(),
        duration_seconds: recordedSeconds,
        callback_at: offersCallbackTime ? fromLocalInput(callbackAt) : '',
        remind_minutes: remindMinutes
      });
      reset();
      await onSaved(result, goNext);
    } catch (cause) {
      error = cause instanceof APIRequestError ? cause.message : 'Arama kaydedilemedi.';
    } finally {
      saving = false;
    }
  }

  function onKeydown(event: KeyboardEvent) {
    if (event.ctrlKey || event.metaKey || event.altKey) return;
    const target = event.target as HTMLElement | null;
    if (target && /^(INPUT|TEXTAREA|SELECT)$/.test(target.tagName)) return;
    if (target?.isContentEditable || target?.closest('[role="dialog"]')) return;
    const index = Number(event.key) - 1;
    if (Number.isInteger(index) && index >= 0 && index < callOutcomes.length) {
      event.preventDefault();
      choose(callOutcomes[index]);
    }
  }

  onDestroy(() => stopTimer(false));
</script>

<svelte:window onkeydown={onKeydown} />

<section class="panel call" aria-labelledby="call-title-{lead.id}">
  <div class="call-head">
    <h2 id="call-title-{lead.id}">Arama</h2>
    <div class="call-actions">
      {#if lead.phone}
        <Button onclick={() => dial(lead.phone)}>
          <Phone size={14} data-icon="inline-start" aria-hidden="true" />Ara
        </Button>
      {/if}
      {#if lead.phone2}
        <Button variant="outline" onclick={() => dial(lead.phone2)}>
          <Phone size={14} data-icon="inline-start" aria-hidden="true" />2. numara
        </Button>
      {/if}
      {#if startedAt}
        <Button variant="outline" onclick={() => stopTimer()}>
          <Square size={14} data-icon="inline-start" aria-hidden="true" />Bitir · {formatDuration(
            elapsed
          )}
        </Button>
      {:else}
        <Button variant="ghost" onclick={startTimer} title="Süreyi başlat">
          <Play size={14} data-icon="inline-start" aria-hidden="true" />{recordedSeconds != null
            ? formatDuration(recordedSeconds)
            : 'Süre'}
        </Button>
      {/if}
    </div>
  </div>
  <p class="hint">Sonucu seçin (klavyede 1–9).</p>
  <div class="outcomes" role="radiogroup" aria-label="Arama sonucu">
    {#each callOutcomes as item, index (item)}
      <button
        type="button"
        role="radio"
        aria-checked={outcome === item}
        class="outcome {callOutcomeTone(item)}"
        class:selected={outcome === item}
        onclick={() => choose(item)}
      >
        <kbd>{index + 1}</kbd>{callOutcomeLabel(item)}
      </button>
    {/each}
  </div>
  {#if outcome}
    <div class="call-form">
      <Field.Field>
        <Field.FieldLabel for="call-note-{lead.id}">Görüşme notu</Field.FieldLabel>
        <textarea
          id="call-note-{lead.id}"
          class="textarea"
          rows="3"
          maxlength={4000}
          bind:value={note}
          placeholder={outcome === 'DO_NOT_CALL'
            ? 'Neden aranmak istemedi?'
            : 'Ne konuşuldu, kimle görüşüldü…'}
        ></textarea>
      </Field.Field>
      {#if offersCallbackTime}
        <Field.Field>
          <Field.FieldLabel for="call-callback-{lead.id}">
            {needsCallbackTime ? 'Ne zaman tekrar aranacak?' : 'Tekrar arama (isteğe bağlı)'}
          </Field.FieldLabel>
          <div class="callback">
            <DateTimeInput
              id="call-callback-{lead.id}"
              bind:value={callbackAt}
              ariaLabel="Tekrar arama zamanı"
            />
            <div class="presets">
              <button type="button" onclick={() => setPreset('hour')}>2 saat sonra</button>
              <button type="button" onclick={() => setPreset('tomorrow')}>Yarın 10:00</button>
              <button type="button" onclick={() => setPreset('week')}>Haftaya</button>
            </div>
          </div>
          {#if needsCallbackTime}
            <div class="remind">
              <label for="call-remind-{lead.id}">Hatırlatma</label>
              <select id="call-remind-{lead.id}" class="select" bind:value={remindMinutes}>
                <option value={0}>Zamanında</option>
                <option value={5}>5 dk önce</option>
                <option value={10}>10 dk önce</option>
                <option value={30}>30 dk önce</option>
                <option value={60}>1 saat önce</option>
              </select>
            </div>
            <Field.FieldDescription>
              Size bir görev açılır; zamanı gelince zil çalar.
            </Field.FieldDescription>
          {:else if callListItemId}
            <Field.FieldDescription>
              Boş bırakırsanız liste kuralına göre yeniden sıraya girer.
            </Field.FieldDescription>
          {/if}
        </Field.Field>
      {/if}
      {#if error}<p class="notice error" role="alert">{error}</p>{/if}
      <div class="call-foot">
        <Button variant="ghost" type="button" onclick={reset}>Vazgeç</Button>
        <Button variant="outline" type="button" disabled={saving} onclick={() => void save(false)}
          >Kaydet</Button
        >
        <Button type="button" disabled={saving} onclick={() => void save(true)}>
          {#if saving}<LoaderCircle data-icon="inline-start" aria-hidden="true" />{/if}
          {nextLabel}
        </Button>
      </div>
    </div>
  {/if}
  {#if onDoNotCall}
    <div class="dnc-link">
      <button type="button" onclick={onDoNotCall}>
        <Ban size={12} aria-hidden="true" /> Aranmasın olarak işaretle
      </button>
    </div>
  {/if}
</section>

<style>
  .panel {
    margin-top: 16px;
    padding: 14px 16px;
  }
  .call {
    border: 1px solid color-mix(in srgb, var(--primary) 35%, var(--border));
  }
  .call-head {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
  }
  .call-head h2 {
    margin: 0;
    font-size: 14px;
  }
  .call-actions {
    display: flex;
    gap: 6px;
    flex-wrap: wrap;
  }
  .hint {
    margin: 8px 0;
    color: var(--text-muted);
    font-size: 12px;
  }
  .outcomes {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(min(100%, 140px), 1fr));
    gap: 6px;
  }
  .outcome {
    display: flex;
    align-items: center;
    gap: 6px;
    min-height: 34px;
    padding: 6px 10px;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    background: var(--surface);
    color: var(--text);
    font-size: 12px;
    text-align: left;
    cursor: pointer;
  }
  .outcome kbd {
    display: inline-grid;
    place-items: center;
    min-width: 18px;
    height: 18px;
    border-radius: 4px;
    background: color-mix(in srgb, var(--text-muted) 15%, transparent);
    font-size: 11px;
    font-family: inherit;
  }
  .outcome.positive {
    border-color: color-mix(in srgb, var(--success) 45%, var(--border-strong));
  }
  .outcome.negative {
    border-color: color-mix(in srgb, var(--danger) 40%, var(--border-strong));
  }
  .outcome.pending {
    border-color: color-mix(in srgb, var(--warning) 45%, var(--border-strong));
  }
  .outcome.selected {
    border-color: var(--primary);
    background: color-mix(in srgb, var(--primary) 12%, transparent);
    font-weight: 600;
  }
  .call-form {
    display: grid;
    gap: 12px;
    margin-top: 14px;
  }
  .callback {
    display: grid;
    gap: 6px;
  }
  .presets {
    display: flex;
    gap: 6px;
    flex-wrap: wrap;
  }
  .presets button {
    height: 26px;
    padding: 0 10px;
    border: 1px solid var(--border-strong);
    border-radius: 999px;
    background: var(--surface);
    color: var(--text);
    font-size: 12px;
    cursor: pointer;
  }
  .remind {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-top: 6px;
    font-size: 12px;
  }
  .remind .select {
    width: auto;
  }
  .call-foot {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
    flex-wrap: wrap;
  }
  .dnc-link {
    margin-top: 12px;
    padding-top: 10px;
    border-top: 1px dashed var(--border);
  }
  .dnc-link button {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    border: 0;
    background: transparent;
    color: var(--text-muted);
    font-size: 12px;
    cursor: pointer;
  }
  .dnc-link button:hover {
    color: var(--danger);
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
</style>
