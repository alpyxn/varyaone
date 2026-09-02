<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import {
    DatabaseBackup,
    Download,
    Upload,
    TriangleAlert,
    CircleCheck,
    Loader,
    FileArchive,
    HardDriveDownload
  } from '@lucide/svelte';
  import { api, APIRequestError, type Session } from '$lib/api';
  import { Button } from '$lib/components/ui/button';
  import { FileDrop } from '$lib/components/varya/file-drop';
  import { ConfirmDialog } from '$lib/components/varya/confirm-dialog';
  import { downloadBackup, restoreBackup, type RestoreResult } from '$lib/features/settings/backup';

  let loading = $state(true);
  let denied = $state(false);

  let files = $state<File[]>([]);
  let force = $state(false);
  let confirmOpen = $state(false);
  let restoring = $state(false);
  let error = $state('');
  let result = $state<RestoreResult | null>(null);

  const selected = $derived(files[0] ?? null);

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
  });

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

  async function runRestore() {
    if (!selected) return;
    restoring = true;
    error = '';
    try {
      result = await restoreBackup(selected, force);
      files = [];
    } catch (cause) {
      error =
        cause instanceof APIRequestError || cause instanceof Error
          ? cause.message
          : 'Geri yükleme başarısız oldu.';
      throw cause;
    } finally {
      restoring = false;
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
          disabled={!selected || restoring}
          onclick={() => (confirmOpen = true)}
        >
          {#if restoring}
            <Loader size={16} class="spin" /> Geri yükleniyor…
          {:else}
            <Upload size={16} /> Seçili dosyadan geri dön
          {/if}
        </Button>
      </div>

      {#if error}
        <p class="notice error">{error}</p>
      {/if}
      {#if result}
        <p class="notice ok">
          <CircleCheck size={15} />
          {new Date(result.restored_from).toLocaleString('tr-TR')} tarihli yedek geri yüklendi —
          {result.objects} dosya, şema sürümü {result.migration_version}. Oturumunuz sona ermiş
          olabilir; gerekirse yeniden giriş yapın.
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

  :global(.spin) {
    animation: backup-spin 900ms linear infinite;
  }
  @keyframes backup-spin {
    to {
      transform: rotate(360deg);
    }
  }
</style>
