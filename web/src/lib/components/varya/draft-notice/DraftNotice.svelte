<script lang="ts">
  // Açılışta bulunan yerel kurtarma taslağı için ortak bildirim.
  import { formatDate } from '$lib/design/formatters';

  type Props = {
    savedAt: Date | null;
    /** Taslak, sunucudaki kaydın başka bir sürümünden alınmış. */
    staleVersion?: boolean;
    onRestore: () => void;
    onDiscard: () => void;
  };

  let { savedAt, staleVersion = false, onRestore, onDiscard }: Props = $props();
</script>

<div class="draft-notice" role="status">
  <div class="draft-notice-copy">
    <strong>Kaydedilmemiş taslak bulundu</strong>
    <span>
      {savedAt ? formatDate(savedAt, true) : 'Tarih bilinmiyor'} tarihinde bu cihaza kaydedildi.
      {#if staleVersion}
        Kayıt o tarihten sonra değişti; geri yüklerseniz alanları kontrol edin.
      {/if}
    </span>
  </div>
  <div class="draft-notice-actions">
    <button type="button" class="draft-notice-restore" onclick={onRestore}
      >Taslağı geri yükle</button
    >
    <button type="button" class="draft-notice-discard" onclick={onDiscard}>Taslağı sil</button>
  </div>
</div>

<style>
  .draft-notice {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    margin-bottom: 14px;
    padding: 12px 14px;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-panel);
    background: var(--surface-muted);
  }
  .draft-notice-copy {
    display: grid;
    gap: 3px;
    min-width: 220px;
  }
  .draft-notice-copy strong {
    font-size: 13px;
  }
  .draft-notice-copy span {
    color: var(--text-muted);
    font-size: 12px;
    line-height: 1.5;
  }
  .draft-notice-actions {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
  }
  .draft-notice-actions button {
    display: inline-flex;
    height: var(--control-height);
    align-items: center;
    border-radius: var(--radius-control);
    padding: 0 12px;
    font-size: 12px;
    font-weight: 600;
  }
  .draft-notice-restore {
    border: 1px solid var(--border-strong);
    background: var(--surface);
    color: var(--text);
  }
  .draft-notice-restore:hover {
    background: var(--surface-muted);
  }
  .draft-notice-discard {
    border: 1px solid transparent;
    background: transparent;
    color: var(--text-muted);
  }
  .draft-notice-discard:hover {
    color: var(--text);
    text-decoration: underline;
  }
</style>
