<script lang="ts">
  import { onMount } from 'svelte';
  import { desktopZoom, isDesktopClient, type ZoomState } from '$lib/desktop-client';

  const presets = [0.8, 0.9, 1, 1.1, 1.25, 1.5, 1.75, 2];
  const step = 0.05;

  let desktop = $state(false);
  let zoom = $state<ZoomState | null>(null);
  let busy = $state(false);
  let error = $state('');

  const percent = (factor: number) => `%${Math.round(factor * 100)}`;
  const same = (a: number, b: number) => Math.abs(a - b) < 0.001;

  async function refresh() {
    try {
      zoom = await desktopZoom.get();
      error = '';
    } catch {
      error = 'Ekran ölçeği okunamadı.';
    }
  }

  onMount(() => {
    desktop = isDesktopClient();
    if (!desktop) return;
    void refresh();
    // Ctrl + tekerlek ölçeği değiştirdiğinde görünüm yeniden boyutlanır;
    // sayfadaki değer de güncel kalsın.
    const onResize = () => void refresh();
    window.addEventListener('resize', onResize);
    return () => window.removeEventListener('resize', onResize);
  });

  async function choose(factor: number) {
    if (!zoom || busy) return;
    busy = true;
    try {
      zoom = await desktopZoom.set(Math.min(zoom.max, Math.max(zoom.min, factor)));
      error = '';
    } catch {
      error = 'Ekran ölçeği değiştirilemedi.';
    } finally {
      busy = false;
    }
  }
</script>

<svelte:head><title>Program Ayarları · Varya One</title></svelte:head>

<header class="page-header">
  <div>
    <h1>Program ayarları</h1>
  </div>
</header>

{#if error}<div class="notice error" role="alert">{error}</div>{/if}

{#if !desktop}
  <div class="notice">
    Bu ayarlar yalnızca Varya One masaüstü uygulamasında kullanılabilir. Tarayıcıda yazıları
    büyütmek ya da küçültmek için Ctrl tuşunu basılı tutup + veya − tuşuna basabilirsiniz.
  </div>
{:else}
  <section class="panel-grid">
    <article class="card form">
      <h2 class="panel-title">Ekran ölçeği</h2>
      <p class="lead">
        Yazıların, düğmelerin ve tabloların ekranda ne kadar büyük görüneceğini belirler. Yazılar
        küçük geliyor ya da okumakta zorlanıyorsanız büyütün. Aynı anda daha çok satır görmek
        istiyorsanız küçültün.
      </p>

      {#if zoom}
        <div class="scale-now" aria-live="polite">
          <button
            class="button secondary step"
            type="button"
            aria-label="Küçült"
            disabled={busy || zoom.factor <= zoom.min}
            onclick={() => zoom && choose(zoom.factor - step)}>−</button
          >
          <strong>{percent(zoom.factor)}</strong>
          <button
            class="button secondary step"
            type="button"
            aria-label="Büyüt"
            disabled={busy || zoom.factor >= zoom.max}
            onclick={() => zoom && choose(zoom.factor + step)}>+</button
          >
        </div>

        <div class="segmented" role="group" aria-label="Hazır ölçekler">
          {#each presets as preset (preset)}
            <button
              type="button"
              class:on={same(zoom.factor, preset)}
              disabled={busy}
              onclick={() => choose(preset)}>{percent(preset)}</button
            >
          {/each}
        </div>

        <div class="actions-row">
          <button
            class="button secondary"
            type="button"
            disabled={busy || same(zoom.factor, zoom.default)}
            onclick={() => zoom && choose(zoom.default)}
            >Varsayılana dön ({percent(zoom.default)})</button
          >
        </div>
      {/if}

      <ul class="notes">
        <li>
          Seçiminiz hemen uygulanır ve kaydedilir. Program bir sonraki açılışta aynı ölçekle başlar.
        </li>
        <li>
          Ayar yalnızca <strong>bu bilgisayarda, bu Windows kullanıcısı için</strong> geçerlidir. Aynı
          sunucuya bağlanan diğer bilgisayarları ve bu bilgisayardaki diğer kullanıcıları etkilemez.
        </li>
        <li>
          Kısayol: Ctrl tuşunu basılı tutup fare tekerleğini çevirerek ya da Ctrl + / Ctrl − ile de
          değiştirebilirsiniz. Bu şekilde yaptığınız değişiklik de hatırlanır.
        </li>
      </ul>
    </article>
  </section>
{/if}

<style>
  .scale-now {
    display: flex;
    align-items: center;
    gap: 12px;
  }
  .scale-now strong {
    min-width: 64px;
    font-size: 22px;
    text-align: center;
    font-variant-numeric: tabular-nums;
  }
  .step {
    width: 36px;
    padding: 0;
    font-size: 18px;
  }
  .segmented {
    display: flex;
    flex-wrap: wrap;
    border: 1px solid var(--border);
    border-radius: var(--radius-panel);
    overflow: hidden;
    width: fit-content;
    max-width: 100%;
  }
  .segmented button {
    padding: 6px 12px;
    border: 0;
    border-left: 1px solid var(--border);
    background: var(--surface);
    color: var(--text);
    cursor: pointer;
    font-variant-numeric: tabular-nums;
  }
  .segmented button:first-child {
    border-left: 0;
  }
  .segmented button:hover:not(:disabled) {
    background: var(--surface-muted);
  }
  .segmented button.on {
    background: var(--primary);
    color: var(--primary-foreground);
  }
  .actions-row {
    display: flex;
    gap: 8px;
    flex-wrap: wrap;
  }
  .notes {
    display: grid;
    gap: 6px;
    margin: 4px 0 0;
    padding-left: 18px;
    color: var(--text-subtle);
    font-size: 13px;
    line-height: 1.5;
  }
</style>
