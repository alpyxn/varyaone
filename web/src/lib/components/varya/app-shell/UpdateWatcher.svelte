<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { Loader, ArrowUpCircle, CircleCheck, TriangleAlert, X } from '@lucide/svelte';
  import {
    getUpdateStatus,
    applyUpdate,
    snoozeUpdate,
    ackUpdate,
    renderNotes,
    phaseLabel,
    type UpdateStatus
  } from '$lib/features/settings/update';

  let { canManage = false }: { canManage?: boolean } = $props();

  let status = $state<UpdateStatus | null>(null);
  let dismissed = $state(false);
  let failedDismissed = $state(false);
  let busy = $state(false);
  let timer: ReturnType<typeof setInterval> | null = null;

  const applying = $derived(status?.state === 'in_progress' || status?.state === 'apply_requested');
  const completed = $derived(
    status?.state === 'done' && status?.applied?.version === status?.current_version
  );
  const failed = $derived(status?.state === 'failed' && !failedDismissed);
  const showBanner = $derived(
    !!status?.update_available && !status?.snoozed && !applying && !completed && !failed
  );

  async function poll() {
    try {
      status = await getUpdateStatus();
      if (status?.state !== 'failed') failedDismissed = false;
    } catch {
      // API may be briefly unreachable during the restart phase — hold the last
      // state (the overlay stays up) rather than flashing it away.
    }
    reschedule();
  }

  function reschedule() {
    if (timer) clearInterval(timer);
    timer = setInterval(poll, applying ? 3000 : 60000);
  }

  onMount(() => {
    if (!canManage) return;
    void poll();
  });
  onDestroy(() => {
    if (timer) clearInterval(timer);
  });

  async function doApply() {
    busy = true;
    try {
      await applyUpdate();
      await poll();
    } finally {
      busy = false;
    }
  }
  async function doSnooze() {
    busy = true;
    try {
      await snoozeUpdate();
      dismissed = true;
      await poll();
    } finally {
      busy = false;
    }
  }
  async function reload() {
    try {
      await ackUpdate();
    } catch {
      /* reload anyway */
    }
    location.reload();
  }
</script>

{#if canManage}
  {#if showBanner && !dismissed && status?.latest}
    <div class="upd-banner" class:mandatory={status.mandatory} role="status">
      <ArrowUpCircle size={16} />
      <span>
        Yeni sürüm <strong>{status.latest.version}</strong> yayınlandı.
        {#if status.mandatory}Bu güncelleme zorunlu.{/if}
      </span>
      <div class="upd-banner-actions">
        <button type="button" class="primary" onclick={doApply} disabled={busy}
          >Şimdi güncelle</button
        >
        {#if !status.mandatory}
          <button type="button" onclick={doSnooze} disabled={busy}>Daha sonra</button>
          <button type="button" class="icon" aria-label="Kapat" onclick={() => (dismissed = true)}
            ><X size={15} /></button
          >
        {/if}
      </div>
    </div>
  {/if}

  {#if applying}
    <div
      class="upd-overlay"
      role="alertdialog"
      aria-modal="true"
      aria-label="Güncelleme uygulanıyor"
    >
      <div class="upd-panel">
        <Loader size={30} class="upd-spin" />
        <h2>Güncelleme uygulanıyor</h2>
        <p class="phase">{phaseLabel(status?.progress?.phase ?? 'queued')}</p>
        {#if status?.progress?.message}<p class="msg">{status.progress.message}</p>{/if}
        <p class="hint">
          Sistem bakım modunda. İşlem bittiğinde bu ekran otomatik yenilenir. Bir sorun çıkarsa
          önceki sürüme güvenle geri dönülür.
        </p>
      </div>
    </div>
  {:else if completed}
    <div
      class="upd-overlay"
      role="alertdialog"
      aria-modal="true"
      aria-label="Güncelleme tamamlandı"
    >
      <div class="upd-panel">
        <CircleCheck size={30} class="ok" />
        <h2>Güncelleme tamamlandı</h2>
        <p class="phase">Sürüm {status?.current_version}</p>
        {#if status?.applied?.notes_md}
          <div class="upd-notes">
            <!-- eslint-disable-next-line svelte/no-at-html-tags -->
            {@html renderNotes(status.applied.notes_md)}
          </div>
        {/if}
        <button type="button" class="primary big" onclick={reload}>Yenile</button>
      </div>
    </div>
  {:else if failed && status?.result}
    <div class="upd-overlay" role="alertdialog" aria-modal="true" aria-label="Güncelleme başarısız">
      <div class="upd-panel">
        <TriangleAlert size={30} class="warn" />
        <h2>Güncelleme başarısız oldu</h2>
        <p class="phase">
          {status.result.rolled_back
            ? `Sistem önceki sürüme (${status.result.from_version}) geri alındı.`
            : 'Geri alma denendi.'}
        </p>
        {#if status.result.error}<p class="msg">{status.result.error}</p>{/if}
        <div class="upd-panel-actions">
          <button type="button" onclick={() => (failedDismissed = true)}>Kapat</button>
          <a class="primary" href="/ayarlar/guncelleme">Ayrıntılar</a>
        </div>
      </div>
    </div>
  {/if}
{/if}

<style>
  .upd-banner {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 9px 16px;
    background: #eef2ff;
    color: #1e2a5a;
    border-bottom: 1px solid #c7d2fe;
    font-size: 13px;
  }
  .upd-banner.mandatory {
    background: #fef2f2;
    color: #7f1d1d;
    border-bottom-color: #fecaca;
  }
  .upd-banner span {
    flex: 1;
  }
  .upd-banner-actions {
    display: flex;
    gap: 6px;
    align-items: center;
  }
  .upd-banner button {
    border: 1px solid currentColor;
    background: transparent;
    color: inherit;
    border-radius: 6px;
    padding: 4px 10px;
    font-size: 12px;
    cursor: pointer;
  }
  .upd-banner button.primary {
    background: #4f46e5;
    color: #fff;
    border-color: #4f46e5;
  }
  .upd-banner button.icon {
    border: 0;
    padding: 4px;
  }
  .upd-overlay {
    position: fixed;
    inset: 0;
    z-index: 9999;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 24px;
    background: rgba(15, 23, 42, 0.55);
    backdrop-filter: blur(2px);
  }
  .upd-panel {
    background: var(--surface, #fff);
    color: var(--foreground, #0f172a);
    border-radius: 16px;
    padding: 32px;
    max-width: 460px;
    width: 100%;
    text-align: center;
    box-shadow: 0 24px 60px rgba(0, 0, 0, 0.35);
  }
  .upd-panel h2 {
    margin: 14px 0 4px;
    font-size: 18px;
  }
  .upd-panel .phase {
    margin: 0;
    font-weight: 600;
  }
  .upd-panel .msg {
    margin: 6px 0 0;
    font-size: 13px;
    color: var(--muted-foreground, #64748b);
  }
  .upd-panel .hint {
    margin: 14px 0 0;
    font-size: 12.5px;
    color: var(--muted-foreground, #64748b);
  }
  .upd-notes {
    text-align: left;
    margin: 16px 0;
    max-height: 40vh;
    overflow: auto;
    font-size: 13px;
  }
  .upd-notes :global(h2),
  .upd-notes :global(h3),
  .upd-notes :global(h4) {
    font-size: 14px;
    margin: 10px 0 4px;
  }
  .upd-notes :global(ul),
  .upd-notes :global(ol) {
    padding-left: 20px;
    margin: 4px 0;
  }
  .upd-panel .primary,
  .upd-panel button {
    margin-top: 8px;
    border-radius: 8px;
    padding: 9px 16px;
    border: 1px solid var(--border, #cbd5e1);
    background: transparent;
    color: inherit;
    cursor: pointer;
    font: inherit;
    text-decoration: none;
    display: inline-block;
  }
  .upd-panel .primary {
    background: #4f46e5;
    color: #fff;
    border-color: #4f46e5;
  }
  .upd-panel .big {
    padding: 11px 28px;
    font-weight: 600;
  }
  .upd-panel-actions {
    display: flex;
    gap: 10px;
    justify-content: center;
  }
  :global(.upd-spin) {
    animation: upd-w-spin 1s linear infinite;
  }
  @keyframes upd-w-spin {
    to {
      transform: rotate(360deg);
    }
  }
</style>
