<script lang="ts">
  import { onMount } from 'svelte';
  import {
    CheckCircle2,
    TriangleAlert,
    LoaderCircle,
    Server,
    Database,
    Package
  } from '@lucide/svelte';

  type State = 'checking' | 'ok' | 'error';
  let liveness: State = 'checking';
  let readiness: State = 'checking';
  let traceID = '';
  let currentVersion = '';
  let latestVersion = '';
  let updateAvailable = false;

  async function check(path: string): Promise<State> {
    try {
      const response = await fetch(`/api${path}`);
      traceID = response.headers.get('x-request-id') ?? traceID;
      if (response.ok) {
        const body = await response
          .clone()
          .json()
          .catch(() => null);
        if (body?.release) currentVersion = body.release;
      }
      return response.ok ? 'ok' : 'error';
    } catch {
      return 'error';
    }
  }

  async function loadVersion() {
    try {
      const response = await fetch('/api/v1/system/update');
      if (!response.ok) return;
      const body = await response.json();
      currentVersion = body.current_version ?? currentVersion;
      latestVersion = body.latest?.version ?? '';
      updateAvailable = !!body.update_available;
    } catch {
      /* not permitted or offline — current version still comes from /health */
    }
  }

  onMount(async () => {
    [liveness, readiness] = await Promise.all([check('/health/live'), check('/health/ready')]);
    await loadVersion();
  });

  const label = (state: State) =>
    state === 'checking' ? 'Kontrol ediliyor' : state === 'ok' ? 'Hazır' : 'Sorun var';
</script>

<svelte:head><title>Sistem Durumu · Varya One</title></svelte:head>

<header class="page-header">
  <div>
    <h1>Sistem durumu</h1>
  </div>
</header>

<section class="status-grid" aria-live="polite">
  <article class="card status-card">
    <div class="status-icon" class:ok={liveness === 'ok'} class:error={liveness === 'error'}>
      <Server size={20} aria-hidden="true" />
    </div>
    <div class="status-body">
      <strong>API canlılık</strong>
      <span class="status-desc">Süreç ve HTTP sunucusu</span>
    </div>
    <span class="status-pill" class:ok={liveness === 'ok'} class:error={liveness === 'error'}>
      {#if liveness === 'checking'}<LoaderCircle size={14} class="spin" aria-hidden="true" />
      {:else if liveness === 'ok'}<CheckCircle2 size={14} aria-hidden="true" />
      {:else}<TriangleAlert size={14} aria-hidden="true" />{/if}
      {label(liveness)}
    </span>
  </article>

  <article class="card status-card">
    <div class="status-icon" class:ok={readiness === 'ok'} class:error={readiness === 'error'}>
      <Database size={20} aria-hidden="true" />
    </div>
    <div class="status-body">
      <strong>API hazırlık</strong>
      <span class="status-desc">PostgreSQL ve migration durumu</span>
    </div>
    <span class="status-pill" class:ok={readiness === 'ok'} class:error={readiness === 'error'}>
      {#if readiness === 'checking'}<LoaderCircle size={14} class="spin" aria-hidden="true" />
      {:else if readiness === 'ok'}<CheckCircle2 size={14} aria-hidden="true" />
      {:else}<TriangleAlert size={14} aria-hidden="true" />{/if}
      {label(readiness)}
    </span>
  </article>

  <article class="card status-card">
    <div class="status-icon" class:ok={!updateAvailable && !!currentVersion}>
      <Package size={20} aria-hidden="true" />
    </div>
    <div class="status-body">
      <strong>Sürüm</strong>
      <span class="status-desc">
        {currentVersion || '—'}{#if latestVersion && updateAvailable}
          · en yeni {latestVersion}{/if}
      </span>
    </div>
    {#if updateAvailable}
      <a class="status-pill" href="/ayarlar/guncelleme">Güncelleme var</a>
    {:else if currentVersion}
      <span class="status-pill ok"><CheckCircle2 size={14} aria-hidden="true" /> Güncel</span>
    {/if}
  </article>
</section>

{#if traceID}
  <p class="trace-note">Son istek kimliği <code>{traceID}</code></p>
{/if}

<style>
  .status-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
    gap: 12px;
  }
  .status-card {
    display: flex;
    align-items: center;
    gap: 12px;
  }
  .status-icon {
    display: grid;
    place-items: center;
    flex: 0 0 auto;
    width: 40px;
    height: 40px;
    border-radius: 10px;
    background: var(--surface-muted);
    color: var(--text-muted);
  }
  .status-icon.ok {
    background: color-mix(in srgb, var(--success) 14%, var(--surface));
    color: var(--success);
  }
  .status-icon.error {
    background: color-mix(in srgb, var(--danger) 14%, var(--surface));
    color: var(--danger);
  }
  .status-body {
    display: grid;
    gap: 2px;
    flex: 1 1 auto;
    min-width: 0;
  }
  .status-desc {
    color: var(--text-muted);
    font-size: 12px;
  }
  .status-pill {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    flex: 0 0 auto;
    padding: 4px 9px;
    border-radius: 999px;
    border: 1px solid var(--border);
    color: var(--text-muted);
    font-size: 11px;
    font-weight: 700;
  }
  .status-pill.ok {
    border-color: color-mix(in srgb, var(--success) 35%, var(--border));
    color: var(--success);
  }
  .status-pill.error {
    border-color: color-mix(in srgb, var(--danger) 35%, var(--border));
    color: var(--danger);
  }
  .status-pill :global(.spin) {
    animation: spin 1s linear infinite;
  }
  @keyframes spin {
    to {
      transform: rotate(360deg);
    }
  }
  .trace-note {
    margin: 12px 2px 0;
    color: var(--text-muted);
    font-size: 11px;
  }
  .trace-note code {
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    padding: 1px 6px;
  }
</style>
