<script lang="ts">
  import { untrack } from 'svelte';
  import { errorMessage } from '$lib/errors';
  import { Dialog } from 'bits-ui';
  import { LoaderCircle, X } from '@lucide/svelte';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { UnsavedChangesGuard } from '$lib/forms/unsaved-changes.svelte';
  import { UnsavedChangesDialog } from '$lib/components/varya/unsaved-changes-dialog';

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

  // Gerekçe yazıldıysa X, Esc ve Vazgeç çıkış onayından geçer.
  const unsaved = new UnsavedChangesGuard({
    snapshot: () => ({ reason }),
    isBusy: () => busy,
    onClose: () => {
      open = false;
      reset();
    }
  });

  $effect(() => {
    if (open) {
      untrack(() => unsaved.reset());
    } else {
      reason = initialValue;
      untrack(() => unsaved.release());
    }
  });

  $effect(() => unsaved.registerPageGuard(title));

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
      unsaved.closeAfterSave();
    } catch (cause) {
      error = errorMessage(cause, 'İşlem tamamlanamadı.');
    } finally {
      busy = false;
    }
  }
</script>

<Dialog.Root bind:open>
  <Dialog.Portal>
    <Dialog.Overlay class="dialog-overlay" />
    <Dialog.Content
      class="reason-dialog"
      aria-describedby="reason-dialog-description"
      onInteractOutside={(event) => event.preventDefault()}
      onEscapeKeydown={(event) => {
        event.preventDefault();
        unsaved.requestClose();
      }}
    >
      <div class="dialog-heading">
        <div>
          <Dialog.Title>{title}</Dialog.Title>
          <Dialog.Description id="reason-dialog-description">{description}</Dialog.Description>
        </div>
        <button
          class="close-button"
          type="button"
          aria-label="Kapat"
          disabled={busy}
          onclick={() => unsaved.requestClose()}
        >
          <X size={17} />
        </button>
      </div>

      <form
        oninput={() => unsaved.noteUserInput()}
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
          <button
            type="button"
            class="cancel-button"
            disabled={busy}
            onclick={() => unsaved.requestClose()}>Vazgeç</button
          >
          <Button type="submit" disabled={busy || !reason.trim()}>
            {#if busy}<LoaderCircle class="spin" size={14} />{/if}{confirmLabel}
          </Button>
        </div>
      </form>
    </Dialog.Content>
  </Dialog.Portal>
</Dialog.Root>

<UnsavedChangesDialog
  bind:open={unsaved.confirmOpen}
  onKeepEditing={() => unsaved.keepEditing()}
  onDiscard={() => unsaved.discardAndClose()}
/>

<style>
  :global(.reason-dialog) {
    position: fixed;
    z-index: 61;
    top: 50%;
    left: 50%;
    width: min(440px, calc(100vw - 32px));
    max-height: calc(100dvh - 32px);
    overflow-y: auto;
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
    flex-wrap: wrap;
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
