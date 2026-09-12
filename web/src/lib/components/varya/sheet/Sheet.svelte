<script lang="ts">
  import { Dialog } from 'bits-ui';
  import { X } from '@lucide/svelte';
  import type { Snippet } from 'svelte';
  import { cn } from '$lib/utils';
  import type { UnsavedChangesGuard } from '$lib/forms/unsaved-changes.svelte';
  import { UnsavedChangesDialog } from '$lib/components/varya/unsaved-changes-dialog';

  let {
    open = $bindable(false),
    title,
    description,
    side = 'right',
    guard,
    children
  }: {
    open?: boolean;
    title: string;
    description?: string;
    side?: 'left' | 'right';
    /**
     * Form modu. Verildiğinde dış alana tıklamak pencereyi kapatmaz; X, Esc ve
     * Vazgeç aynı kontrollü kapatma akışından geçer.
     */
    guard?: UnsavedChangesGuard;
    children?: Snippet;
  } = $props();

  function requestClose() {
    if (guard) guard.requestClose();
    else open = false;
  }
</script>

<Dialog.Root bind:open>
  <Dialog.Portal>
    <Dialog.Overlay class="varya-sheet-overlay" />
    <Dialog.Content
      class={cn('varya-sheet', side === 'left' ? 'varya-sheet-left' : 'varya-sheet-right')}
      onInteractOutside={(event) => {
        if (guard) event.preventDefault();
      }}
      onEscapeKeydown={(event) => {
        if (!guard) return;
        event.preventDefault();
        guard.requestClose();
      }}
    >
      <div class="varya-sheet-heading">
        <div>
          <Dialog.Title>{title}</Dialog.Title>{#if description}<Dialog.Description
              >{description}</Dialog.Description
            >{/if}
        </div>
        <button
          class="varya-sheet-close"
          type="button"
          aria-label="Kapat"
          disabled={guard?.busy}
          onclick={requestClose}><X size={17} aria-hidden="true" /></button
        >
      </div>
      <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
      <div
        class="varya-sheet-body"
        oninput={() => guard?.noteUserInput()}
        onchange={() => guard?.noteUserInput()}
      >
        {@render children?.()}
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
  :global(.varya-sheet-overlay) {
    position: fixed;
    inset: 0;
    z-index: 60;
    background: rgb(2 6 23 / 48%);
  }
  :global(.varya-sheet) {
    position: fixed;
    top: 0;
    bottom: 0;
    z-index: 61;
    width: min(480px, calc(100vw - 24px));
    padding: 16px;
    overflow-y: auto;
    border: 1px solid var(--border);
    background: var(--surface);
    box-shadow: -12px 0 35px rgb(2 6 23 / 16%);
  }
  :global(.varya-sheet-right) {
    right: 0;
  }
  :global(.varya-sheet-left) {
    left: 0;
    box-shadow: 12px 0 35px rgb(2 6 23 / 16%);
  }
  .varya-sheet-heading {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 12px;
    padding-bottom: 12px;
    border-bottom: 1px solid var(--border);
  }
  .varya-sheet-heading :global(h2) {
    margin: 0;
    font-size: 16px;
  }
  .varya-sheet-heading :global([data-dialog-description]) {
    display: block;
    margin-top: 4px;
    color: var(--text-muted);
    font-size: 11px;
  }
  :global(.varya-sheet-close) {
    display: grid;
    min-width: 32px;
    min-height: 32px;
    place-items: center;
    border: 0;
    border-radius: var(--radius-control);
    background: transparent;
    color: var(--text-muted);
  }
  :global(.varya-sheet-close:hover) {
    background: var(--surface-muted);
    color: var(--text);
  }
  .varya-sheet-body {
    padding-top: 14px;
  }
</style>
