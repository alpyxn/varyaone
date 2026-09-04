<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { goto } from '$app/navigation';
  import {
    RefreshCw,
    TriangleAlert,
    CircleCheck,
    Loader,
    ArrowUpCircle,
    Clock,
    ShieldCheck
  } from '@lucide/svelte';
  import { api, type Session } from '$lib/api';
  import { Button } from '$lib/components/ui/button';
  import {
    getUpdateStatus,
    checkForUpdates,
    applyUpdate,
    snoozeUpdate,
    ackUpdate,
    setUpdateChecks,
    renderNotes,
    phaseLabel,
    type UpdateStatus
  } from '$lib/features/settings/update';

  let loading = $state(true);
  let denied = $state(false);
  let status = $state<UpdateStatus | null>(null);
  let busy = $state('');
  let error = $state('');
  let timer: ReturnType<typeof setInterval> | null = null;

  const inProgress = $derived(
    status?.state === 'in_progress' || status?.state === 'apply_requested'
  );

  async function refresh() {
    try {
      status = await getUpdateStatus();
      error = '';
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Durum alınamadı.';
    }
  }

  async function toggleChecks() {
    if (!status) return;
    busy = 'toggle';
    error = '';
    try {
      status = await setUpdateChecks(status.checks_disabled);
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Ayar kaydedilemedi.';
      await refresh();
    } finally {
      busy = '';
    }
  }

  // The button asks the catalog itself; refresh() alone would only redraw a
  // status the worker may have stored hours ago.
  async function checkNow() {
    busy = 'check';
    error = '';
    try {
      status = await checkForUpdates();
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Kontrol edilemedi.';
      await refresh();
    } finally {
      busy = '';
    }
  }

  function schedule() {
    if (timer) clearInterval(timer);
    timer = setInterval(refresh, inProgress ? 3000 : 30000);
  }

  $effect(() => {
    // Re-arm the poll cadence whenever the in-progress flag flips.
    void inProgress;
    schedule();
  });

  onMount(async () => {
    try {
      const session = await api<Session>('/session');
      denied = !(session.permissions ?? []).includes('system.update.manage');
    } catch {
      await goto('/giris');
      return;
    }
    if (!denied) await refresh();
    loading = false;
    schedule();
  });

  onDestroy(() => {
    if (timer) clearInterval(timer);
  });

  async function run(label: string, fn: () => Promise<unknown>) {
    busy = label;
    error = '';
    try {
      await fn();
      await refresh();
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'İşlem başarısız.';
    } finally {
      busy = '';
    }
  }

  function fmt(ts?: string) {
    if (!ts) return '—';
    const d = new Date(ts);
    return isNaN(+d)
      ? ts
      : d.toLocaleString('tr-TR', {
          day: '2-digit',
          month: 'short',
          year: 'numeric',
          hour: '2-digit',
          minute: '2-digit'
        });
  }
</script>

<div class="page-header">
  <div>
    <h1>Güncelleme</h1>
  </div>
  {#if !loading && !denied && !status?.checks_disabled}
    <Button type="button" variant="ghost" onclick={checkNow} disabled={!!busy}>
      <RefreshCw size={15} />
      {busy === 'check' ? 'Kontrol ediliyor…' : 'Kontrol et'}
    </Button>
  {/if}
</div>

<p class="lead">
  Varya One sürümünüzü izler; yeni bir sürüm yayınlandığında buradan tek tıkla güncelleyebilirsiniz.
  Güncelleme sırasında sistem bakım moduna alınır, bitince yenilemeniz istenir.
</p>

{#if loading}
  <section class="card muted-card">Yükleniyor…</section>
{:else if denied}
  <section class="card muted-card">
    <TriangleAlert size={16} /> Güncellemeyi yönetme yetkiniz yok.
  </section>
{:else if status}
  <div class="upd-grid">
    <section class="card">
      <div class="row">
        <span class="k">Çalışan sürüm</span>
        <span class="v mono">{status.current_version}</span>
      </div>
      <div class="row">
        <span class="k">Kanal</span>
        <span class="v">{status.channel}</span>
      </div>
      <div class="row">
        <span class="k">Son kontrol</span>
        <span class="v">{fmt(status.checked_at)}</span>
      </div>
      {#if status.latest}
        <div class="row">
          <span class="k">En yeni sürüm</span>
          <span class="v mono">{status.latest.version}</span>
        </div>
      {/if}
      <div class="row upd-toggle-row">
        <span class="k">Güncelleme kontrolü</span>
        <label class="upd-toggle">
          <input
            type="checkbox"
            checked={!status.checks_disabled}
            disabled={!!busy || inProgress}
            onchange={toggleChecks}
          />
          <span>{status.checks_disabled ? 'Kapalı' : 'Açık'}</span>
        </label>
      </div>
    </section>

    {#if status.checks_disabled}
      <section class="card upd-off">
        <ShieldCheck size={18} />
        <div>
          <strong>Güncelleme kontrolü kapalı</strong>
          <span class="muted"
            >Yeni sürümler aranmıyor ve önerilmiyor. Açtığınızda kontrol yeniden başlar.</span
          >
        </div>
      </section>
    {/if}

    {#if inProgress}
      <section class="card upd-progress">
        <div class="upd-progress-head">
          <Loader size={18} class="spin" />
          <div>
            <strong>Güncelleme uygulanıyor</strong>
            <span class="muted"
              >{phaseLabel(status.progress?.phase ?? 'queued')}{status.progress?.message
                ? ` — ${status.progress.message}`
                : ''}</span
            >
          </div>
        </div>
        <p class="upd-note">
          Bu pencereyi kapatabilirsiniz; işlem sunucuda sürüyor. Bittiğinde bütün oturumlar
          yenilenmeniz için uyarılır. Bir sorun çıkarsa sistem otomatik olarak önceki sürüme döner.
        </p>
      </section>
    {:else if status.state === 'done' && status.result}
      <section class="card upd-done">
        <CircleCheck size={18} />
        <div>
          <strong>Güncelleme tamamlandı</strong>
          <span class="muted"
            >{status.result.from_version} → {status.result.to_version ||
              status.current_version}</span
          >
        </div>
        <Button
          type="button"
          variant="ghost"
          onclick={() => run('ack', ackUpdate)}
          disabled={!!busy}>Tamam</Button
        >
      </section>
    {:else if status.state === 'failed' && status.result}
      <section class="card upd-failed">
        <div class="upd-progress-head">
          <TriangleAlert size={18} />
          <div>
            <strong>Güncelleme başarısız oldu</strong>
            <span class="muted"
              >{status.result.rolled_back
                ? 'Sistem önceki sürüme geri alındı.'
                : 'Geri alma denendi.'}</span
            >
          </div>
        </div>
        {#if status.result.error}<p class="upd-err">{status.result.error}</p>{/if}
        {#if status.result.log_tail}
          <details class="upd-log">
            <summary>Günlük kaydı</summary>
            <pre>{status.result.log_tail}</pre>
          </details>
        {/if}
        <div class="upd-actions">
          {#if status.update_available}
            <Button type="button" onclick={() => run('apply', applyUpdate)} disabled={!!busy}>
              Tekrar dene
            </Button>
          {/if}
          <Button
            type="button"
            variant="ghost"
            onclick={() => run('ack', ackUpdate)}
            disabled={!!busy}>Kapat</Button
          >
        </div>
      </section>
    {:else if status.update_available && status.latest}
      <section class="card upd-available">
        <div class="upd-progress-head">
          <ArrowUpCircle size={18} />
          <div>
            <strong>Yeni sürüm mevcut: {status.latest.version}</strong>
            {#if status.latest.published_at}
              <span class="muted">Yayın: {fmt(status.latest.published_at)}</span>
            {/if}
          </div>
        </div>
        {#if status.mandatory}
          <p class="upd-mandatory">
            <ShieldCheck size={14} /> Bu güncelleme zorunludur ve ertelenemez.
          </p>
        {/if}
        <div class="upd-actions">
          <Button type="button" onclick={() => run('apply', applyUpdate)} disabled={!!busy}>
            Şimdi güncelle
          </Button>
          {#if !status.mandatory}
            <Button
              type="button"
              variant="ghost"
              onclick={() => run('snooze', snoozeUpdate)}
              disabled={!!busy}
            >
              <Clock size={15} /> Daha sonra hatırlat
            </Button>
          {/if}
        </div>
        {#if status.snoozed}
          <p class="muted sm">Bir sonraki hatırlatma: {fmt(status.snooze_until)}</p>
        {/if}
      </section>
    {:else}
      <section class="card upd-current">
        <CircleCheck size={18} />
        <div>
          <strong>Sisteminiz güncel</strong>
          <span class="muted">En son sürümü kullanıyorsunuz.</span>
        </div>
      </section>
    {/if}
  </div>

  {#if error}<p class="upd-err">{error}</p>{/if}

  {#if (status.update_available && status.latest?.notes_md) || status.applied?.notes_md}
    <section class="card upd-notes">
      <h2>Sürüm notları</h2>
      <!-- eslint-disable-next-line svelte/no-at-html-tags -->
      <div class="notes-body">
        {@html renderNotes((status.applied?.notes_md || status.latest?.notes_md || '') as string)}
      </div>
    </section>
  {/if}
{/if}

<style>
  .lead {
    max-width: 60ch;
    color: var(--muted-foreground, #64748b);
    margin: 0 0 20px;
  }
  .upd-grid {
    display: grid;
    gap: 16px;
    grid-template-columns: minmax(220px, 320px) 1fr;
    align-items: start;
  }
  @media (max-width: 720px) {
    .upd-grid {
      grid-template-columns: 1fr;
    }
  }
  .card .row {
    display: flex;
    justify-content: space-between;
    gap: 12px;
    padding: 7px 0;
    border-bottom: 1px solid var(--border, #e2e8f0);
  }
  .card .row:last-child {
    border-bottom: 0;
  }
  .k {
    color: var(--muted-foreground, #64748b);
  }
  .v {
    font-weight: 600;
  }
  .mono {
    font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  }
  .muted {
    color: var(--muted-foreground, #64748b);
    display: block;
    font-size: 13px;
  }
  .sm {
    font-size: 12px;
  }
  .upd-progress-head {
    display: flex;
    gap: 10px;
    align-items: flex-start;
  }
  .upd-progress,
  .upd-available,
  .upd-failed {
    display: flex;
    flex-direction: column;
    gap: 12px;
  }
  .upd-done,
  .upd-off,
  .upd-current {
    display: flex;
    gap: 10px;
    align-items: center;
  }
  .upd-done div,
  .upd-off div,
  .upd-current div {
    flex: 1;
  }
  .upd-off div {
    display: grid;
    gap: 2px;
  }
  .upd-toggle-row {
    align-items: center;
  }
  .upd-toggle {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    cursor: pointer;
    font-size: 13px;
  }
  .upd-toggle input {
    cursor: pointer;
  }
  .upd-note,
  .upd-err {
    margin: 0;
    font-size: 13px;
  }
  .upd-err {
    color: var(--destructive, #dc2626);
    font-weight: 500;
  }
  .upd-mandatory {
    display: flex;
    align-items: center;
    gap: 6px;
    margin: 0;
    font-size: 13px;
    color: var(--destructive, #dc2626);
  }
  .upd-actions {
    display: flex;
    gap: 10px;
    flex-wrap: wrap;
  }
  .upd-log {
    font-size: 12px;
  }
  .upd-log pre {
    max-height: 260px;
    overflow: auto;
    background: var(--muted, #f1f5f9);
    padding: 10px;
    border-radius: 8px;
    white-space: pre-wrap;
    word-break: break-word;
  }
  .upd-notes {
    margin-top: 16px;
  }
  .notes-body :global(h2),
  .notes-body :global(h3),
  .notes-body :global(h4) {
    margin: 14px 0 6px;
    font-size: 15px;
  }
  .notes-body :global(ul),
  .notes-body :global(ol) {
    margin: 6px 0;
    padding-left: 20px;
  }
  .notes-body :global(li) {
    margin: 3px 0;
  }
  .notes-body :global(code) {
    font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
    background: var(--muted, #f1f5f9);
    padding: 1px 5px;
    border-radius: 4px;
    font-size: 0.9em;
  }
  :global(.spin) {
    animation: upd-spin 1s linear infinite;
  }
  @keyframes upd-spin {
    to {
      transform: rotate(360deg);
    }
  }
</style>
