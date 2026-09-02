<script lang="ts">
  import type { Snippet } from 'svelte';
  import { Dialog } from 'bits-ui';
  import { LoaderCircle, X } from '@lucide/svelte';
  import { Button } from '$lib/components/ui/button';

  type Props = {
    open?: boolean;
    title: string;
    description: string;
    cancelLabel?: string;
    confirmLabel?: string;
    children?: Snippet;
    onConfirm: () => Promise<void> | void;
  };

  let {
    open = $bindable(false),
    title,
    description,
    cancelLabel = 'Vazgeç',
    confirmLabel = 'Tamam',
    children,
    onConfirm
  }: Props = $props();

  let busy = $state(false);
  let error = $state('');

  function reset() {
    error = '';
  }

  async function submit() {
    busy = true;
    error = '';
    try {
      await onConfirm();
      open = false;
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'İşlem tamamlanamadı.';
    } finally {
      busy = false;
    }
  }
</script>

<Dialog.Root bind:open onOpenChange={(next) => !next && !busy && reset()}>
  <Dialog.Portal>
    <Dialog.Overlay class="confirm-dialog-overlay" />
    <Dialog.Content class="confirm-dialog" aria-describedby="confirm-dialog-description">
      <div class="confirm-dialog-heading">
        <div>
          <Dialog.Title>{title}</Dialog.Title>
          <Dialog.Description id="confirm-dialog-description">{description}</Dialog.Description>
        </div>
        <Dialog.Close class="confirm-dialog-close" aria-label="Kapat" disabled={busy}>
          <X size={17} />
        </Dialog.Close>
      </div>

      {#if error}<p class="confirm-dialog-error" role="alert">{error}</p>{/if}

      {#if children}<div class="confirm-dialog-body">{@render children()}</div>{/if}

      <div class="confirm-dialog-actions">
        <Dialog.Close class="confirm-dialog-cancel" disabled={busy}>{cancelLabel}</Dialog.Close>
        <Button disabled={busy} onclick={() => void submit()}>
          {#if busy}<LoaderCircle class="spin" size={14} />{/if}{confirmLabel}
        </Button>
      </div>
    </Dialog.Content>
  </Dialog.Portal>
</Dialog.Root>

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
    transform: translate(-50%, -50%);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-panel);
    background: var(--surface);
    box-shadow: 0 20px 60px rgb(10 30 27 / 22%);
    padding: 18px;
  }
  .confirm-dialog-heading,
  .confirm-dialog-actions {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
  }
  .confirm-dialog-heading {
    align-items: flex-start;
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
  :global(.confirm-dialog-close) {
    display: inline-grid;
    place-items: center;
    width: 28px;
    height: 28px;
    border: 0;
    border-radius: var(--radius-control);
    background: transparent;
    color: var(--text-muted);
  }
  :global(.confirm-dialog-close:hover) {
    background: var(--surface-muted);
    color: var(--text);
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
