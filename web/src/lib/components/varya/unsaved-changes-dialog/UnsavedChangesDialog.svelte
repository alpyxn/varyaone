<script lang="ts">
  // Veri giriş pencerelerinden çıkışta gösterilen ortak onay. Metinler
  // sabittir; her ekranda aynı dil kullanılır.
  import { Dialog } from 'bits-ui';

  type Props = {
    open?: boolean;
    /** "Düzenlemeye devam et" — veri ve odak korunur. */
    onKeepEditing: () => void;
    /** "Değişiklikleri sil ve çık". */
    onDiscard: () => void;
    title?: string;
    description?: string;
    keepLabel?: string;
    discardLabel?: string;
  };

  let {
    open = $bindable(false),
    onKeepEditing,
    onDiscard,
    title = 'Kaydedilmemiş değişiklikleriniz var.',
    description = 'Çıkarsanız bu pencerede yaptığınız değişiklikler silinecek.',
    keepLabel = 'Düzenlemeye devam et',
    discardLabel = 'Değişiklikleri sil ve çık'
  }: Props = $props();

  let keepButton = $state<HTMLButtonElement | null>(null);
</script>

<Dialog.Root
  bind:open
  onOpenChange={(next) => {
    // Kapanma yalnızca iki butondan biriyle olur; başka bir yolla kapanırsa
    // düzenlemeye dönülür, alttaki form kapanmaz.
    if (!next) onKeepEditing();
  }}
>
  <Dialog.Portal>
    <Dialog.Overlay class="unsaved-dialog-overlay" />
    <Dialog.Content
      class="unsaved-dialog"
      role="alertdialog"
      aria-describedby="unsaved-dialog-description"
      onOpenAutoFocus={(event) => {
        // İlk odak "Düzenlemeye devam et" butonunda olur.
        event.preventDefault();
        keepButton?.focus();
      }}
      onEscapeKeydown={(event) => {
        // Esc yalnızca bu onayı kapatır; alttaki formu kapatmaz.
        event.preventDefault();
        onKeepEditing();
      }}
      onInteractOutside={(event) => {
        event.preventDefault();
      }}
    >
      <Dialog.Title>{title}</Dialog.Title>
      <Dialog.Description id="unsaved-dialog-description">{description}</Dialog.Description>
      <div class="unsaved-dialog-actions">
        <button
          bind:this={keepButton}
          class="unsaved-dialog-keep"
          type="button"
          onclick={onKeepEditing}>{keepLabel}</button
        >
        <button class="unsaved-dialog-discard" type="button" onclick={onDiscard}
          >{discardLabel}</button
        >
      </div>
    </Dialog.Content>
  </Dialog.Portal>
</Dialog.Root>

<style>
  /* Çıkış onayı her zaman kendisini açan pencerenin üstünde durur. */
  :global(.unsaved-dialog-overlay) {
    position: fixed;
    z-index: 120;
    inset: 0;
    background: rgb(8 26 23 / 46%);
  }
  :global(.unsaved-dialog) {
    position: fixed;
    z-index: 121;
    top: 50%;
    left: 50%;
    width: min(430px, calc(100vw - 32px));
    max-height: calc(100dvh - 32px);
    overflow-y: auto;
    transform: translate(-50%, -50%);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-panel);
    background: var(--surface);
    box-shadow: 0 20px 60px rgb(10 30 27 / 24%);
    padding: 20px;
  }
  :global(.unsaved-dialog [data-dialog-title]) {
    margin: 0;
    font-size: 16px;
  }
  :global(.unsaved-dialog [data-dialog-description]) {
    display: block;
    margin-top: 8px;
    color: var(--text-muted);
    font-size: 12px;
    line-height: 1.55;
  }
  .unsaved-dialog-actions {
    display: flex;
    flex-wrap: wrap;
    justify-content: flex-end;
    gap: 10px;
    margin-top: 22px;
  }
  .unsaved-dialog-actions button {
    display: inline-flex;
    height: var(--control-height);
    align-items: center;
    border-radius: var(--radius-control);
    padding: 0 14px;
    font-size: 12px;
    font-weight: 600;
  }
  .unsaved-dialog-keep {
    border: 1px solid var(--border-strong);
    background: var(--surface);
    color: var(--text);
  }
  .unsaved-dialog-keep:hover {
    background: var(--surface-muted);
  }
  .unsaved-dialog-discard {
    border: 1px solid var(--danger);
    background: var(--danger);
    color: #fff;
  }
  .unsaved-dialog-discard:hover {
    filter: brightness(0.95);
  }
</style>
