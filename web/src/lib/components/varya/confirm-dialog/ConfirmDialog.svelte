<script lang="ts">
  import { errorMessage } from '$lib/errors';
  import type { Snippet } from 'svelte';
  import { Dialog } from 'bits-ui';
  import { LoaderCircle } from '@lucide/svelte';
  import { Button } from '$lib/components/ui/button';
  import type { UnsavedChangesGuard } from '$lib/forms/unsaved-changes.svelte';
  import { UnsavedChangesDialog } from '$lib/components/varya/unsaved-changes-dialog';

  type Props = {
    open?: boolean;
    title: string;
    description: string;
    cancelLabel?: string;
    confirmLabel?: string;
    children?: Snippet;
    /**
     * children alanında veri girişi varsa, o formun değişiklik denetimi.
     * Salt işlem onaylarında verilmez — onaya ikinci onay eklenmez.
     */
    guard?: UnsavedChangesGuard;
    onConfirm: () => Promise<void> | void;
  };

  let {
    open = $bindable(false),
    title,
    description,
    cancelLabel = 'Vazgeç',
    confirmLabel = 'Tamam',
    children,
    guard,
    onConfirm
  }: Props = $props();

  let busy = $state(false);
  let error = $state('');

  function reset() {
    error = '';
  }

  /** X, Esc, dış tıklama ve Vazgeç için tek kapanma yolu. */
  function requestClose() {
    if (busy) return;
    if (guard) {
      guard.requestClose();
      return;
    }
    reset();
    open = false;
  }

  async function submit() {
    busy = true;
    error = '';
    try {
      await onConfirm();
      guard?.markClean();
      reset();
      open = false;
    } catch (cause) {
      error = errorMessage(cause, 'İşlem tamamlanamadı.');
    } finally {
      busy = false;
    }
  }
</script>

<Dialog.Root bind:open>
  <Dialog.Portal>
    <Dialog.Overlay class="confirm-dialog-overlay" />
    <Dialog.Content
      class="confirm-dialog"
      aria-describedby="confirm-dialog-description"
      onInteractOutside={(event) => {
        // İşlem sürerken ve veri girişi varken dış tıklama kapatmaz.
        if (busy || guard) event.preventDefault();
      }}
      onEscapeKeydown={(event) => {
        event.preventDefault();
        requestClose();
      }}
    >
      <div class="confirm-dialog-heading">
        <Dialog.Title>{title}</Dialog.Title>
        <Dialog.Description id="confirm-dialog-description">{description}</Dialog.Description>
      </div>

      {#if error}<p class="confirm-dialog-error" role="alert">{error}</p>{/if}

      {#if children}<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
        <div
          class="confirm-dialog-body"
          oninput={() => guard?.noteUserInput()}
          onchange={() => guard?.noteUserInput()}
        >
          {@render children()}
        </div>{/if}

      <div class="confirm-dialog-actions">
        <button class="confirm-dialog-cancel" type="button" disabled={busy} onclick={requestClose}
          >{cancelLabel}</button
        >
        <Button disabled={busy} onclick={() => void submit()}>
          {#if busy}<LoaderCircle class="spin" size={14} />{/if}{confirmLabel}
        </Button>
      </div>
    </Dialog.Content>
  </Dialog.Portal>
</Dialog.Root>

{#if guard}
  <UnsavedChangesDialog
    bind:open={guard.confirmOpen}
    onKeepEditing={() => guard.keepEditing()}
    onDiscard={() => guard.discardAndClose()}
  />
{/if}

<style>
  :global(.confirm-dialog-overlay) {
    position: fixed;
    z-index: 70;
    inset: 0;
    background: rgb(8 26 23 / 42%);
  }
  :global(.confirm-dialog) {
    position: fixed;
    z-index: 71;
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
  .confirm-dialog-actions {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
  }
  .confirm-dialog-heading :global(h2) {
    margin: 0;
    font-size: 16px;
  }
  .confirm-dialog-heading :global([data-dialog-description]) {
    display: block;
    margin-top: 5px;
    color: var(--text-muted);
    font-size: 12px;
    line-height: 1.5;
  }
  .confirm-dialog-error {
    margin: 14px 0 0;
    color: var(--danger);
    font-size: 12px;
  }
  .confirm-dialog-body {
    margin-top: 14px;
  }
  .confirm-dialog-actions {
    justify-content: flex-end;
    margin-top: 22px;
  }
  :global(.confirm-dialog-cancel) {
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
  :global(.confirm-dialog-cancel:hover) {
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
