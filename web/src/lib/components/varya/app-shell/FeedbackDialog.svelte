<script lang="ts">
  import { onMount } from 'svelte';
  import { Bug, Lightbulb, X, GripHorizontal } from '@lucide/svelte';
  import { toast } from 'svelte-sonner';
  import { api, APIRequestError } from '$lib/api';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';

  let { open = $bindable(false) }: { open?: boolean } = $props();

  const PANEL_W = 460;
  const PANEL_H = 520;

  let category = $state<'bug' | 'idea'>('bug');
  let message = $state('');
  let contact = $state('');
  let submitting = $state(false);

  let isDesktop = $state(true);
  let pos = $state<{ x: number; y: number } | null>(null);
  let drag: { dx: number; dy: number } | null = null;

  onMount(() => {
    const mq = window.matchMedia('(min-width: 641px)');
    const update = () => (isDesktop = mq.matches);
    update();
    mq.addEventListener('change', update);
    return () => mq.removeEventListener('change', update);
  });

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

  $effect(() => {
    if (open && isDesktop && !pos) {
      pos = clamp((window.innerWidth - PANEL_W) / 2, (window.innerHeight - PANEL_H) / 2 - 20);
    }
  });

  function reset() {
    category = 'bug';
    message = '';
    contact = '';
    submitting = false;
  }

  function close() {
    if (submitting) return;
    open = false;
    pos = null;
    reset();
  }

  function onKey(event: KeyboardEvent) {
    if (open && event.key === 'Escape') close();
  }

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    const trimmed = message.trim();
    if (!trimmed) {
      toast.error('Lütfen bir mesaj yazın.');
      return;
    }
    submitting = true;
    try {
      await api('/pulse/feedback', {
        method: 'POST',
        body: JSON.stringify({ category, message: trimmed, contact: contact.trim() })
      });
      toast.success('Geri bildiriminiz için teşekkürler!');
      open = false;
      pos = null;
      reset();
    } catch (error) {
      const detail =
        error instanceof APIRequestError ? error.message : 'Geri bildirim gönderilemedi.';
      toast.error(detail);
      submitting = false;
    }
  }
</script>

<svelte:window onkeydown={onKey} />

{#if open && (isDesktop ? pos : true)}
  {#if !isDesktop}
    <div
      class="feedback-overlay"
      role="presentation"
      onclick={(e) => {
        if (e.target === e.currentTarget) close();
      }}
    ></div>
  {/if}
  <div
    class="feedback-dialog"
    class:is-sheet={!isDesktop}
    style={isDesktop && pos ? `left:${pos.x}px; top:${pos.y}px;` : ''}
    role="dialog"
    aria-modal={!isDesktop}
    aria-labelledby="feedback-title"
  >
    <div
      class="feedback-head"
      role="toolbar"
      tabindex="-1"
      aria-label={isDesktop ? 'Pencereyi taşı' : 'Geri bildirim'}
      onpointerdown={isDesktop ? startDrag : undefined}
      onpointermove={isDesktop ? onDrag : undefined}
      onpointerup={isDesktop ? endDrag : undefined}
    >
      {#if isDesktop}<GripHorizontal size={15} aria-hidden="true" />{/if}
      <h2 id="feedback-title">Geri bildirim gönder</h2>
      <button type="button" class="feedback-close" aria-label="Kapat" onclick={close}>
        <X size={14} />
      </button>
    </div>

    <div class="feedback-body">
      <form onsubmit={submit}>
        <div class="feedback-types" role="radiogroup" aria-label="Geri bildirim türü">
          <button
            type="button"
            role="radio"
            aria-checked={category === 'bug'}
            class="feedback-type"
            class:active={category === 'bug'}
            onclick={() => (category = 'bug')}
          >
            <Bug size={15} aria-hidden="true" />
            <span>Hata bildir</span>
          </button>
          <button
            type="button"
            role="radio"
            aria-checked={category === 'idea'}
            class="feedback-type"
            class:active={category === 'idea'}
            onclick={() => (category = 'idea')}
          >
            <Lightbulb size={15} aria-hidden="true" />
            <span>Öneride bulun</span>
          </button>
        </div>

        <label class="feedback-label" for="feedback-message">Mesaj</label>
        <textarea
          id="feedback-message"
          bind:value={message}
          rows="5"
          maxlength="4000"
          required
          placeholder={category === 'bug'
            ? 'Ne oldu? Hangi adımlardan sonra karşılaştınız?'
            : 'Neyi geliştirmek istersiniz?'}
        ></textarea>

        <label class="feedback-label" for="feedback-contact">
          İletişim <span class="feedback-optional">(isteğe bağlı)</span>
        </label>
        <Input
          id="feedback-contact"
          bind:value={contact}
          maxlength={200}
          placeholder="E-posta veya telefon — geri dönüş isterseniz"
        />

        <div class="feedback-actions">
          <Button type="button" variant="ghost" onclick={close} disabled={submitting}>Vazgeç</Button
          >
          <Button type="submit" disabled={submitting}>
            {submitting ? 'Gönderiliyor…' : 'Gönder'}
          </Button>
        </div>
      </form>
    </div>
  </div>
{/if}

<style>
  .feedback-overlay {
    position: fixed;
    inset: 0;
    z-index: 2147482999;
    background: rgb(2 6 23 / 45%);
  }
  .feedback-dialog {
    position: fixed;
    z-index: 2147483000;
    width: 460px;
    max-width: calc(100vw - 16px);
    display: flex;
    flex-direction: column;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-panel);
    background: var(--surface);
    box-shadow: 0 24px 70px rgb(2 6 23 / 32%);
    overflow: hidden;
  }
  .feedback-dialog.is-sheet {
    left: 0;
    right: 0;
    bottom: 0;
    width: 100%;
    max-width: 100%;
    max-height: 92dvh;
    border-width: 1px 0 0;
    border-radius: var(--radius-panel) var(--radius-panel) 0 0;
  }
  .feedback-head {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 8px 8px 8px 10px;
    background: var(--surface-muted);
    border-bottom: 1px solid var(--border);
    color: var(--text-muted);
    cursor: grab;
    touch-action: none;
  }
  .feedback-dialog.is-sheet .feedback-head {
    flex-shrink: 0;
    cursor: default;
    touch-action: auto;
    padding: 12px 10px 12px 14px;
  }
  .feedback-head:active {
    cursor: grab;
  }
  .feedback-dialog:not(.is-sheet) .feedback-head:active {
    cursor: grabbing;
  }
  .feedback-head h2 {
    flex: 1;
    margin: 0;
    font-size: 13px;
    font-weight: 700;
    color: var(--text);
  }
  .feedback-close {
    display: inline-grid;
    place-items: center;
    width: 22px;
    height: 22px;
    border: 0;
    border-radius: 5px;
    background: transparent;
    color: var(--text-muted);
    cursor: pointer;
  }
  .feedback-close:hover {
    background: var(--surface);
    color: var(--text);
  }
  .feedback-body {
    padding: 16px 18px 18px;
  }
  .feedback-dialog.is-sheet .feedback-body {
    min-height: 0;
    overflow-y: auto;
    padding-bottom: max(18px, env(safe-area-inset-bottom));
    -webkit-overflow-scrolling: touch;
  }
  .feedback-dialog.is-sheet .feedback-close {
    width: 30px;
    height: 30px;
  }
  .feedback-note {
    margin: 6px 0 14px;
    font-size: 12px;
    line-height: 1.5;
    color: var(--text-muted);
  }
  .feedback-types {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 8px;
    margin-bottom: 14px;
  }
  .feedback-type {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 7px;
    padding: 9px 10px;
    border: 1px solid var(--border-strong);
    border-radius: 8px;
    background: var(--surface);
    color: var(--text-muted);
    font-size: 12px;
    font-weight: 600;
    cursor: pointer;
  }
  .feedback-type.active {
    border-color: var(--primary);
    background: var(--primary-soft, var(--surface-muted));
    color: var(--text);
  }
  .feedback-label {
    display: block;
    margin: 0 0 5px;
    font-size: 12px;
    font-weight: 650;
    color: var(--text);
  }
  .feedback-optional {
    font-weight: 400;
    color: var(--text-muted);
  }
  textarea {
    width: 100%;
    margin-bottom: 12px;
    padding: 8px 10px;
    border: 1px solid var(--border-strong);
    border-radius: 8px;
    background: var(--surface);
    color: var(--text);
    font: inherit;
    font-size: 12px;
    resize: vertical;
  }
  textarea:focus-visible {
    outline: 2px solid var(--primary);
    outline-offset: 1px;
  }
  .feedback-actions {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
    margin-top: 14px;
  }
</style>
