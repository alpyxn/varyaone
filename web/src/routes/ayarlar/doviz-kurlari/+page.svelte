<script lang="ts">
  import { goto } from '$app/navigation';
  import { onMount } from 'svelte';
  import { api, type Session } from '$lib/api';
  import {
    getExchangeRateDashboard,
    refreshExchangeRates,
    updateExchangeRateSettings
  } from '$lib/features/pricing/api';
  import type { ExchangeRateDashboard } from '$lib/features/pricing/types';
  import { formatDate, formatMoney } from '$lib/design/formatters';

  let session = $state<Session | null>(null);
  let dashboard = $state<ExchangeRateDashboard>();
  let loading = $state(true);
  let refreshing = $state(false);
  let saving = $state(false);
  let error = $state('');
  let message = $state('');
  let sourcePreference = $state<'AUTO' | 'TCMB' | 'ECB'>('AUTO');
  let refreshIntervalHours = $state(6);

  const canManage = $derived(Boolean(session?.permissions.includes('pricing.manage')));

  function errorMessage(cause: unknown, fallback: string) {
    return typeof cause === 'object' && cause && 'message' in cause
      ? String(cause.message)
      : fallback;
  }

  function applyDashboard(next: ExchangeRateDashboard) {
    dashboard = next;
    sourcePreference = next.settings.source_preference;
    refreshIntervalHours = next.settings.refresh_interval_hours || 6;
  }

  async function load() {
    try {
      session = await api<Session>('/session');
      applyDashboard(await getExchangeRateDashboard());
    } catch (cause) {
      if (!session) await goto('/giris');
      error = errorMessage(cause, 'Döviz kuru ayarları okunamadı.');
    } finally {
      loading = false;
    }
  }

  async function saveSettings() {
    if (!canManage || saving || refreshing) return;
    saving = true;
    error = '';
    message = '';
    try {
      const settings = await updateExchangeRateSettings({
        source_preference: sourcePreference,
        refresh_interval_hours: refreshIntervalHours
      });
      if (dashboard) dashboard = { ...dashboard, settings };
      message = 'Kur ayarları kaydedildi.';
    } catch (cause) {
      error = errorMessage(cause, 'Kur ayarları kaydedilemedi.');
    } finally {
      saving = false;
    }
  }

  async function refresh() {
    if (!canManage || refreshing || saving) return;
    refreshing = true;
    error = '';
    message = '';
    try {
      applyDashboard(await refreshExchangeRates());
      message = 'Kurlar güncellendi.';
    } catch (cause) {
      error = errorMessage(cause, 'Kurlar güncellenemedi.');
    } finally {
      refreshing = false;
    }
  }

  onMount(() => void load());
</script>

<svelte:head><title>Döviz Kurları · Varya One</title></svelte:head>
<header class="page-header">
  <div>
    <h1>Döviz kurları</h1>
  </div>
  <div class="page-actions">
    <a class="button secondary" href="/ayarlar/tanimlar">Tüm tanımlar</a>
  </div>
</header>

{#if message}<div class="notice success" role="status">{message}</div>{/if}
{#if error}<div class="notice error" role="alert">{error}</div>{/if}

{#if loading}
  <div class="card">Döviz kurları yükleniyor…</div>
{:else if dashboard}
  <div class="workspace-grid">
    <section class="card form" aria-labelledby="rate-settings-title">
      <h2 id="rate-settings-title" class="panel-title">Otomatik güncelleme</h2>
      <label class="field"
        >Birincil kaynak
        <select bind:value={sourcePreference} disabled={!canManage || saving || refreshing}>
          <option value="AUTO">Otomatik (TCMB, sonra ECB)</option>
          <option value="TCMB">TCMB</option>
          <option value="ECB">ECB</option>
        </select>
      </label>
      <label class="field"
        >Yenileme aralığı
        <select bind:value={refreshIntervalHours} disabled={!canManage || saving || refreshing}>
          <option value={6}>6 saat</option>
          <option value={12}>12 saat</option>
          <option value={24}>24 saat</option>
        </select>
      </label>
      <div class="actions">
        <button
          class="button"
          type="button"
          onclick={() => void saveSettings()}
          disabled={!canManage || saving || refreshing}>Ayarları kaydet</button
        >
        <button
          class="button secondary"
          type="button"
          onclick={() => void refresh()}
          disabled={!canManage || saving || refreshing}
          >{refreshing ? 'Güncelleniyor…' : 'Şimdi güncelle'}</button
        >
      </div>
      <dl class="status-list">
        <div>
          <dt>Son kaynak</dt>
          <dd>{dashboard.settings.last_source || 'Henüz yok'}</dd>
        </div>
        <div>
          <dt>Son kur tarihi</dt>
          <dd>{dashboard.settings.last_rate_date || 'Henüz yok'}</dd>
        </div>
        <div>
          <dt>Son başarılı güncelleme</dt>
          <dd>{formatDate(dashboard.settings.last_success_at, true)}</dd>
        </div>
        {#if dashboard.settings.last_error}<div class="error-row">
            <dt>Son hata</dt>
            <dd>{dashboard.settings.last_error}</dd>
          </div>{/if}
      </dl>
    </section>

    <section class="card form" aria-labelledby="rate-list-title">
      <div class="section-heading">
        <div>
          <h2 id="rate-list-title" class="panel-title">Kayıtlı kurlar</h2>
          <p class="help">1 birim dövizin {dashboard.base_currency} karşılığı.</p>
        </div>
        <span class="badge">Temel: {dashboard.base_currency}</span>
      </div>
      {#if dashboard.items.length === 0}
        <p class="help">Henüz kayıtlı kur yok. Şimdi güncelle ile güvenilir kaynaktan çekin.</p>
      {:else}
        <div class="rate-list">
          {#each dashboard.items as item}
            <div class="rate-row">
              <span
                ><strong>{item.currency_code}</strong><small>{item.rate_date} · {item.source}</small
                ></span
              >
              <strong>{formatMoney(item.rate_to_base, dashboard.base_currency)}</strong>
              <a href={item.source_url} target="_blank" rel="noreferrer">Kaynağı gör</a>
            </div>
          {/each}
        </div>
      {/if}
    </section>
  </div>
{/if}

<style>
  .workspace-grid {
    display: grid;
    grid-template-columns: minmax(260px, 0.8fr) minmax(0, 1.4fr);
    gap: 12px;
  }
  .help {
    margin: 0 0 12px;
    color: var(--text-muted);
    font-size: 12px;
    line-height: 1.5;
  }
  .field {
    display: grid;
    gap: 5px;
    margin-bottom: 10px;
    color: var(--text-muted);
    font-size: 11px;
    font-weight: 700;
  }
  select {
    min-height: var(--control-height);
    width: 100%;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    background: var(--surface);
    color: var(--text);
    padding: 6px 9px;
    font-size: 13px;
  }
  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: 7px;
    margin-top: 4px;
  }
  .status-list {
    display: grid;
    gap: 7px;
    margin: 16px 0 0;
  }
  .status-list div {
    display: flex;
    justify-content: space-between;
    gap: 12px;
    padding-top: 7px;
    border-top: 1px solid var(--border);
    font-size: 12px;
  }
  .status-list dt {
    color: var(--text-muted);
  }
  .status-list dd {
    margin: 0;
    text-align: right;
  }
  .error-row dd {
    color: var(--danger);
    max-width: 65%;
  }
  .section-heading {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 10px;
  }
  .badge {
    padding: 4px 8px;
    border: 1px solid var(--border);
    border-radius: 999px;
    color: var(--text-muted);
    font-size: 11px;
    white-space: nowrap;
  }
  .rate-list {
    display: grid;
    gap: 7px;
  }
  .rate-row {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto auto;
    align-items: center;
    gap: 14px;
    padding: 10px 0;
    border-top: 1px solid var(--border);
  }
  .rate-row small {
    display: block;
    margin-top: 3px;
    color: var(--text-muted);
    font-size: 11px;
  }
  .rate-row a {
    color: var(--primary);
    font-size: 11px;
  }
  @media (max-width: 760px) {
    .workspace-grid {
      grid-template-columns: 1fr;
    }
    .rate-row {
      grid-template-columns: 1fr auto;
    }
    .rate-row a {
      grid-column: 1 / -1;
    }
  }
</style>
