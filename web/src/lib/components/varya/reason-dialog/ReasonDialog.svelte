<script lang="ts">
  import { Dialog } from 'bits-ui';
  import { LoaderCircle, X } from '@lucide/svelte';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';

  type Props = {
    open?: boolean;
    title?: string;
    description?: string;
    label?: string;
    initialValue?: string;
    placeholder?: string;
    confirmLabel?: string;
    onConfirm: (reason: string) => Promise<void> | void;
  };

  let {
    open = $bindable(false),
    title = 'İşlem nedeni',
    description = 'Bu işlem için kısa bir neden girin.',
    label = 'Neden',
    initialValue = '',
    placeholder = 'Nedeni yazın…',
    confirmLabel = 'Kaydet',
    onConfirm
  }: Props = $props();

  let reason = $state('');
  let busy = $state(false);
  let error = $state('');

  $effect(() => {
    if (!open) reason = initialValue;
  });

  function reset() {
    reason = initialValue;
    error = '';
  }

  async function submit() {
    const value = reason.trim();
    if (!value) {
      error = 'Neden alanı boş bırakılamaz.';
      return;
    }
    busy = true;
    error = '';
    try {
      await onConfirm(value);
      open = false;
      reset();
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'İşlem tamamlanamadı.';
    } finally {
      busy = false;
    }
  }
</script>

<Dialog.Root bind:open onOpenChange={(next) => !next && !busy && reset()}>
  <Dialog.Portal>
    <Dialog.Overlay class="dialog-overlay" />
    <Dialog.Content class="reason-dialog" aria-describedby="reason-dialog-description">
      <div class="dialog-heading">
        <div>
          <Dialog.Title>{title}</Dialog.Title>
          <Dialog.Description id="reason-dialog-description">{description}</Dialog.Description>
        </div>
        <Dialog.Close class="close-button" aria-label="Kapat" disabled={busy}>
          <X size={17} />
        </Dialog.Close>
      </div>

      <form
        onsubmit={(event) => {
          event.preventDefault();
          void submit();
        }}
      >
        <label class="reason-label" for="operation-reason">{label}</label>
        <Input
          id="operation-reason"
          bind:value={reason}
          {placeholder}
          maxlength={500}
          autocomplete="off"
          disabled={busy}
          aria-invalid={Boolean(error)}
        />
        {#if error}<p class="reason-error" role="alert">{error}</p>{/if}
        <div class="dialog-actions">
          <Dialog.Close type="button" class="cancel-button" disabled={busy}>Vazgeç</Dialog.Close>
          <Button type="submit" disabled={busy || !reason.trim()}>
            {#if busy}<LoaderCircle class="spin" size={14} />{/if}{confirmLabel}
          </Button>
        </div>
      </form>
    </Dialog.Content>
  </Dialog.Portal>
</Dialog.Root>

<style>
  :global(.reason-dialog) {
    position: fixed;
    z-index: 61;
    top: 50%;
    left: 50%;
    width: min(440px, calc(100vw - 32px));
    transform: translate(-50%, -50%);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-panel);
    background: var(--surface);
    box-shadow: 0 20px 60px rgb(10 30 27 / 22%);
    padding: 18px;
  }
  .dialog-heading,
  .dialog-actions {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
  }
  .dialog-heading {
    align-items: flex-start;
    margin-bottom: 18px;
  }
  .dialog-heading :global(h2) {
    margin: 0;
    font-size: 16px;
  }
  .dialog-heading :global([data-dialog-description]) {
    display: block;
    margin-top: 4px;
    color: var(--text-muted);
    font-size: 12px;
  }
  :global(.close-button) {
    display: inline-grid;
    place-items: center;
    width: 28px;
    height: 28px;
    border: 0;
    border-radius: var(--radius-control);
    background: transparent;
    color: var(--text-muted);
  }
  :global(.close-button:hover) {
    background: var(--surface-muted);
    color: var(--text);
  }
  .reason-label {
    display: block;
    margin-bottom: 5px;
    color: var(--text-subtle);
    font-size: 12px;
    font-weight: 650;
  }
  .reason-error {
    margin: 6px 0 0;
    color: var(--danger);
    font-size: 12px;
  }
  .dialog-actions {
    justify-content: flex-end;
    margin-top: 20px;
  }
  :global(.cancel-button) {
    display: inline-flex;
    height: var(--control-height);
    align-items: center;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    background: transparent;
    color: var(--text);
    padding: 0 12px;
    font-size: 12px;
  }
  :global(.cancel-button:hover) {
    background: var(--surface-muted);
  }
  :global(.spin) {
    animation: spin 0.9s linear infinite;
  }
  @keyframes spin {
    to {
      transform: rotate(360deg);
    }
  }
</style>
