<script lang="ts">
  import { errorMessage } from '$lib/errors';
  import { onMount, onDestroy } from 'svelte';
  import { goto } from '$app/navigation';
  import {
    DatabaseBackup,
    Download,
    Upload,
    TriangleAlert,
    CircleCheck,
    Loader,
    FileArchive,
    HardDriveDownload,
    RefreshCw
  } from '@lucide/svelte';
  import { api, APIRequestError, type Session } from '$lib/api';
  import { Button } from '$lib/components/ui/button';
  import { FileDrop } from '$lib/components/varya/file-drop';
  import { ConfirmDialog } from '$lib/components/varya/confirm-dialog';
  import {
    downloadBackup,
    restoreBackup,
    followOperation,
    operationStatus,
    needsRecovery,
    newOperationKey,
    waitForServer,
    type RestoreResult,
    type OperationRecord
  } from '$lib/features/settings/backup';

  let loading = $state(true);
  let denied = $state(false);

  let files = $state<File[]>([]);
  let force = $state(false);
  let confirmOpen = $state(false);
  let restoring = $state(false);
  let error = $state('');
  let result = $state<RestoreResult | null>(null);
  /** Set once the restore is over and the page is on its way to the login screen. */
  let reconnecting = $state<'restored' | 'disconnected' | null>(null);

  /**
   * The operation this page is following. It is loaded on mount as well as
   * after a restore: a restore outlives the page that started it, so someone
   * who closed the tab, lost the connection or signed in again must be able to
   * come back here and see what happened rather than be shown a blank form.
   */
  let tracked = $state<OperationRecord | null>(null);
  let recovery = $state<OperationRecord | null>(null);
  let follower: AbortController | null = null;

  const selected = $derived(files[0] ?? null);
  const locked = $derived(recovery !== null);

  const PHASE_LABELS: Record<string, string> = {
    VALIDATING: 'Dosya doğrulanıyor',
    PREPARED: 'Hazırlandı',
    QUIESCING: 'Yazmalar durduruluyor',
    SAFETY_VERIFIED: 'Yedek doğrulandı',
    CANDIDATE_READY: 'Yeni veritabanı hazırlandı',
    SWITCHING: 'Değiştiriliyor',
    CHECKING: 'Kontrol ediliyor',
    COMMITTED: 'Tamamlandı',
    FAILED_UNCHANGED: 'Başarısız — sistem değişmedi',
    ROLLING_BACK: 'Geri alınıyor',
    ROLLED_BACK: 'Geri alındı',
    RECOVERY_REQUIRED: 'Kurtarma gerekiyor'
  };

  function phaseLabel(record: OperationRecord): string {
    return PHASE_LABELS[record.phase] ?? record.phase;
  }

  onMount(async () => {
    try {
      const session = await api<Session>('/session');
      denied = !(session.permissions ?? []).includes('system.backup.manage');
    } catch {
      await goto('/giris');
      return;
    } finally {
      loading = false;
    }
    if (denied) return;
    await refreshStatus();
  });

  onDestroy(() => follower?.abort());

  async function refreshStatus() {
    try {
      const status = await operationStatus();
      if (!status.operation) return;
      if (needsRecovery(status.operation)) {
        recovery = status.operation;
        tracked = status.operation;
        return;
      }
      if (status.running) {
        tracked = status.operation;
        track(status.operation.id);
      }
    } catch {
      /* status is diagnostic; a page that cannot fetch it still works */
    }
  }

  function track(id: string) {
    follower?.abort();
    follower = new AbortController();
    restoring = true;
    followOperation(id, (record) => (tracked = record), follower.signal)
      .then((record) => {
        if (needsRecovery(record)) recovery = record;
        else if (record.phase === 'COMMITTED') {
          error = '';
          void returnToLogin('restored');
        } else if (record.error) error = record.error;
      })
      .catch((cause) => {
        // The API went away mid-restore, or the restored database no longer
        // knows this session. Either way the record survives on the server;
        // come back once it answers and let sign-in sort out the session.
        if (cause instanceof DOMException && cause.name === 'AbortError') return;
        void returnToLogin('disconnected');
      })
      .finally(() => (restoring = false));
  }

  /**
   * Leave for the login screen once the server is serving again.
   *
   * The restore replaced the sessions along with everything else, so the page
   * this user is looking at belongs to a session that may no longer exist. A
   * full load (not a client-side goto) also drops every cached store.
   */
  async function returnToLogin(reason: 'restored' | 'disconnected') {
    if (reconnecting) return;
    reconnecting = reason;
    follower?.abort();
    await waitForServer();
    // Long enough to read that it worked before the page changes under them.
    if (reason === 'restored') await new Promise((resolve) => setTimeout(resolve, 1500));
    window.location.href = '/giris';
  }

  function humanSize(bytes: number) {
    if (bytes < 1024) return `${bytes} B`;
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(0)} KB`;
    if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
    return `${(bytes / (1024 * 1024 * 1024)).toFixed(2)} GB`;
  }

  function onFilesChange() {
    result = null;
    error = '';
  }

  const INCONSISTENT_CODES = ['SYSTEM_INCONSISTENT', 'RESTORE_STORAGE_PARTIAL'];

  async function runRestore() {
    if (!selected) return;
    restoring = true;
    error = '';
    result = null;
    // One key per confirmed click. A retry after a lost response carries the
    // same key and is answered with the original operation instead of starting
    // a second restore on top of the first.
    const operationKey = newOperationKey();
    try {
      const outcome = await restoreBackup(selected, { force, operationKey });
      if (outcome.kind === 'running') {
        track(outcome.operationId);
        return;
      }
      result = outcome.result;
      files = [];
      void returnToLogin('restored');
    } catch (cause) {
      if (!(cause instanceof APIRequestError)) {
        // No response at all: the connection dropped while the server was
        // switching databases. What happened is in the server's record, which
        // this session may no longer be allowed to read.
        void returnToLogin('disconnected');
        return;
      }
      error = errorMessage(cause, 'Geri yükleme başarısız oldu.');
      if (cause instanceof APIRequestError && INCONSISTENT_CODES.includes(cause.code)) {
        // Not a retryable failure: the database has already changed. Refresh
        // so the recovery banner comes from the server's record rather than
        // from this one response, which the user may never see again.
        await refreshStatus();
      }
      throw cause;
    } finally {
      if (!follower) restoring = false;
    }
  }
</script>

<div class="page-header">
  <div>
    <h1>Yedekleme</h1>
  </div>
</div>

<p class="lead">
  Tüm sistemi tek bir <code>.varya</code> dosyası olarak indirin ya da daha önce alınmış bir yedekten
  geri dönün.
</p>

{#if loading}
  <section class="card muted-card">Yükleniyor…</section>
{:else if denied}
  <section class="card muted-card">
    <TriangleAlert size={16} /> Sistem yedeğini yönetme yetkiniz yok.
  </section>
{:else}
  <div class="backup-grid">
    <section class="card action-card">
      <div class="card-icon">
        <HardDriveDownload size={20} />
      </div>
      <h2>Yedek indir</h2>
      <p class="card-desc">
        Anlık durumun tam kopyasını bilgisayarınıza kaydeder. Büyük kurulumlarda hazırlanması birkaç
        dakika sürebilir.
      </p>
      <div class="card-foot">
        <Button type="button" onclick={downloadBackup}>
          <Download size={16} /> Yedeği indir
        </Button>
      </div>
    </section>

    <section class="card action-card danger-zone">
      <div class="card-icon danger">
        <DatabaseBackup size={20} />
      </div>
      <h2>Yedekten geri dön</h2>
      <p class="card-desc">
        Seçtiğiniz <code>.varya</code> dosyasındaki duruma dönülür. Mevcut
        <strong>tüm veri ve dosyalar</strong> bununla değiştirilir.
      </p>

      <div class="danger-callout">
        <TriangleAlert size={15} />
        <span>Bu işlem geri alınamaz. Tercihen bakım penceresinde, kimse çalışmazken yapın.</span>
      </div>

      <FileDrop
        bind:files
        accept=".varya"
        label="'.varya' dosyasını buraya bırakın"
        hint="ya da seçmek için tıklayın"
        ariaLabel="Yedek dosyası seç"
        {onFilesChange}
      />

      {#if selected}
        <div class="file-chip">
          <FileArchive size={15} />
          <span class="file-name">{selected.name}</span>
          <span class="file-size">{humanSize(selected.size)}</span>
        </div>
      {/if}

      <label class="force-row">
        <input type="checkbox" bind:checked={force} />
        <span>Yedek bu sürümden daha yeni ya da farklı bir anahtarla alınmışsa yine de zorla</span>
      </label>

      <div class="card-foot">
        <Button
          type="button"
          variant="danger"
          disabled={!selected || restoring || locked}
          onclick={() => (confirmOpen = true)}
        >
          {#if restoring}
            <Loader size={16} class="spin" /> Geri yükleniyor…
          {:else}
            <Upload size={16} /> Seçili dosyadan geri dön
          {/if}
        </Button>
      </div>

      {#if tracked && restoring}
        <p class="notice progress">
          <RefreshCw size={15} class="spin" />
          <span>
            {phaseLabel(tracked)} — işlem <code>{tracked.id}</code>. Bu sayfayı kapatabilirsiniz;
            işlem sunucuda sürer ve geri döndüğünüzde durumu burada görürsünüz.
          </span>
        </p>
      {/if}

      {#if recovery}
        <p class="notice critical">
          <TriangleAlert size={15} />
          <span>
            <strong>Sistem tutarsız durumda.</strong> Veritabanı değişti ancak geri yükleme
            tamamlanamadı (işlem <code>{recovery.id}</code>). Sistemi kullanmayın ve yeniden
            denemeyin. Sunucuda:
            <code>./deploy.sh system-status</code> ile durumu inceleyin, düzelttikten sonra
            <code>./deploy.sh system-resolve "ne yapıldı"</code> ile yeniden açın.
            {#if recovery.error}<br /><span class="detail">{recovery.error}</span>{/if}
          </span>
        </p>
      {:else if error}
        <p class="notice error">{error}</p>
      {/if}
      {#if result}
        <p class="notice ok">
          <CircleCheck size={15} />
          <span>
            {new Date(result.restored_from).toLocaleString('tr-TR')} tarihli yedek geri yüklendi —
            {result.objects} dosya, şema sürümü {result.migration_version}.
          </span>
        </p>
      {/if}
      {#if reconnecting}
        <p class="notice progress" role="status">
          <RefreshCw size={15} class="spin" />
          <span>
            {#if reconnecting === 'restored'}
              Geri yükleme tamamlandı. Giriş ekranına yönlendiriliyorsunuz…
            {:else}
              Sunucuyla bağlantı koptu. Sunucu hazır olunca giriş ekranına yönlendirileceksiniz…
            {/if}
          </span>
        </p>
      {/if}
    </section>
  </div>

  <ConfirmDialog
    bind:open={confirmOpen}
    title="Sistemi geri yükle"
    description="Mevcut tüm veri ve dosyalar silinip yedekteki içerikle değiştirilecek. Bu işlem geri alınamaz."
    confirmLabel="Evet, geri yükle"
    onConfirm={runRestore}
  />
{/if}

<style>
  .lead code,
  .card-desc code {
    padding: 1px 5px;
    border-radius: 5px;
    background: var(--surface-muted);
    font-size: 0.92em;
  }

  .muted-card {
    display: flex;
    align-items: center;
    gap: 8px;
    color: var(--text-muted);
  }

  .backup-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(320px, 1fr));
    gap: 12px;
    margin-top: 14px;
    align-items: start;
  }

  .action-card {
    display: flex;
    flex-direction: column;
    gap: 8px;
    padding: 16px;
  }

  .card-icon {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 38px;
    height: 38px;
    border-radius: var(--radius-control);
    background: var(--primary-soft);
    color: var(--primary);
  }
  .card-icon.danger {
    background: color-mix(in srgb, var(--danger) 12%, var(--surface));
    color: var(--danger);
  }

  .action-card h2 {
    margin: 4px 0 0;
    font-size: 15px;
    letter-spacing: -0.01em;
  }

  .card-desc {
    margin: 0;
    color: var(--text-subtle);
    font-size: 12.5px;
    line-height: 1.5;
  }

  .card-foot {
    margin-top: 6px;
  }

  .danger-zone {
    border-color: color-mix(in srgb, var(--danger) 28%, var(--border));
  }

  .danger-callout {
    display: flex;
    align-items: flex-start;
    gap: 7px;
    margin: 4px 0 2px;
    padding: 8px 10px;
    border-radius: var(--radius-control);
    background: color-mix(in srgb, var(--danger) 9%, var(--surface));
    color: var(--danger);
    font-size: 12px;
    line-height: 1.45;
  }

  .file-chip {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 7px 10px;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    background: var(--surface-muted);
    font-size: 12.5px;
  }
  .file-chip .file-name {
    font-weight: 650;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .file-chip .file-size {
    margin-left: auto;
    color: var(--text-muted);
    font-variant-numeric: tabular-nums;
  }

  .force-row {
    display: flex;
    align-items: flex-start;
    gap: 8px;
    font-size: 12px;
    color: var(--text-subtle);
    line-height: 1.4;
  }
  .force-row input {
    margin-top: 1px;
  }

  .notice {
    display: flex;
    align-items: flex-start;
    gap: 7px;
  }

  .notice.progress {
    padding: 9px 11px;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    background: var(--surface-muted);
    color: var(--text-subtle);
    font-size: 12.5px;
    line-height: 1.5;
  }

  .notice .detail {
    color: var(--text-muted);
    font-size: 11.5px;
  }

  .notice.critical {
    padding: 9px 11px;
    border: 1px solid var(--danger);
    border-radius: var(--radius-control);
    background: color-mix(in srgb, var(--danger) 10%, var(--surface));
    color: var(--danger);
    font-size: 12.5px;
    line-height: 1.5;
  }

  :global(.spin) {
    animation: backup-spin 900ms linear infinite;
  }
  @keyframes backup-spin {
    to {
      transform: rotate(360deg);
    }
  }
</style>
