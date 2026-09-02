<script lang="ts">
  import { tick, onMount } from 'svelte';
  import { ArrowLeft, LoaderCircle, RefreshCw, ScanLine, Send } from '@lucide/svelte';
  import { api, type Session } from '$lib/api';
  import { formatDate, formatQuantity } from '$lib/design/formatters';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { Badge } from '$lib/components/ui/badge';
  import {
    analyzeImport,
    commitImport,
    createExport,
    uploadImport
  } from '$lib/features/dataexchange/api';
  import { ConfirmDialog } from '$lib/components/varya/confirm-dialog';
  import CountStockPickerDialog, {
    type CountStockPickerSelection
  } from './CountStockPickerDialog.svelte';
  import {
    addCountLine,
    correctLine,
    createPass,
    createSession,
    getCount,
    postCount,
    recountCount,
    sendScanEvents,
    submitPass,
    syncCount,
    cancelCount
  } from './api';
  import { createStableEventId, parseWedgeInput } from './scan-utils';
  import { asRecord, normalizeCount, type CountLine, type CountView } from './types';

  type Stage = 'count' | 'review';

  let { id }: { id: string } = $props();
  let count = $state<CountView | null>(null);
  let activeSessionID = $state('');
  let stage = $state<Stage>('count');
  let scanInput = $state('');
  let multiplier = $state('1');
  let scanElement = $state<HTMLInputElement | null>(null);
  let lineSearch = $state('');
  let lineQuantities = $state<Record<string, string>>({});
  let lineErrors = $state<Record<string, string>>({});
  let loading = $state(true);
  let busy = $state(false);
  let error = $state('');
  let errorElement = $state<HTMLDivElement | null>(null);
  let feedback = $state('');
  let syncCursor = $state('');
  let syncState = $state<'synced' | 'syncing' | 'offline'>('synced');
  let postKey = '';
  let recountKey = '';
  let countImportElement = $state<HTMLInputElement | null>(null);
  let stockPickerOpen = $state(false);
  let canEditCount = $state(false);

  const activePass = $derived(
    count?.passes.find((pass) => pass.status === 'IN_PROGRESS' && pass.mode === 'OPEN')
  );
  const filteredLines = $derived(
    (count?.lines ?? []).filter((line) => {
      const needle = lineSearch.trim().toLocaleLowerCase('tr-TR');
      if (!needle) return true;
      return [line.product_name, line.product_code, line.variant_name, line.barcode]
        .filter(Boolean)
        .join(' ')
        .toLocaleLowerCase('tr-TR')
        .includes(needle);
    })
  );
  const countedLineCount = $derived(
    (count?.lines ?? []).filter(
      (line) => line.has_response === true || line.counted_quantity != null
    ).length
  );
  const differenceLineCount = $derived(
    (count?.lines ?? []).filter(
      (line) => line.difference != null && displayedQuantity(line.difference) !== '0'
    ).length
  );
  const remainingLineCount = $derived(Math.max((count?.lines.length ?? 0) - countedLineCount, 0));
  const openExceptionCount = $derived(
    (count?.exceptions ?? []).filter((exception) => exception.status !== 'RESOLVED').length
  );
  const lineExceptionCount = $derived(
    (count?.lines ?? []).filter((line) => Boolean(line.exception)).length
  );
  const lineExceptionOnlyCount = $derived(
    (count?.lines ?? []).filter(
      (line) =>
        Boolean(line.exception) &&
        !(count?.exceptions ?? []).some(
          (exception) =>
            exception.status !== 'RESOLVED' && exception.scope_id && exception.scope_id === line.id
        )
    ).length
  );
  const reviewBlocked = $derived(
    count?.status === 'REVIEW' &&
      (remainingLineCount > 0 || openExceptionCount > 0 || lineExceptionCount > 0)
  );
  const reviewSubmissionBlocked = $derived(
    count?.status === 'IN_PROGRESS' && remainingLineCount > 0
  );

  const reviewSubmissionMessage = $derived(
    remainingLineCount > 0
      ? `İncelemeye göndermek için ${remainingLineCount} satırın sayılan miktarını girip kaydedin.`
      : ''
  );

  function text(value: unknown, fallback = '—') {
    return value === undefined || value === null || value === '' ? fallback : String(value);
  }

  function countStateLabel(status: string) {
    switch (status.toUpperCase()) {
      case 'REVIEW':
        return 'İnceleme';
      case 'POSTED':
        return 'İşlendi';
      case 'CANCELLED':
        return 'İptal';
      default:
        return 'Açık sayım';
    }
  }

  function lineResponded(line: CountLine) {
    return (
      line.has_response === true ||
      (line.counted_quantity !== undefined && line.counted_quantity !== null)
    );
  }

  function lineStatus(line: CountLine) {
    if (line.exception) return { label: 'İncele', tone: 'danger' as const };
    if (!lineResponded(line)) return { label: 'Sayılmadı', tone: 'neutral' as const };
    if (line.difference != null && displayedQuantity(line.difference) !== '0') {
      return { label: 'Fark var', tone: 'warning' as const };
    }
    return { label: 'Sayıldı', tone: 'success' as const };
  }

  function displayedQuantity(value: unknown) {
    return formatQuantity(text(value, '0'));
  }

  function setInputs(next: CountView) {
    const values: Record<string, string> = {};
    for (const line of next.lines) {
      values[line.id] = lineResponded(line)
        ? formatQuantity(String(line.counted_quantity ?? '0'))
        : '';
    }
    // The API is authoritative after a scan/save/refresh. Keep raw text only
    // while the user is typing; never rehydrate a fixed database scale such as
    // 3.00000000 into the editable input.
    lineQuantities = values;
  }

  function applyPayload(payload: unknown) {
    const source = asRecord(payload);
    const nested = asRecord(source.count);
    const next = normalizeCount(Object.keys(nested).length ? nested : source, id);
    if (!next.id) return;
    count = next;
    setInputs(next);
    if (next.status === 'REVIEW' || next.status === 'POSTED') stage = 'review';
    else if (next.status === 'IN_PROGRESS') stage = 'count';
  }

  async function openSession(next: CountView) {
    if (activeSessionID) return;
    let pass = next.passes.find((item) => item.mode === 'OPEN' && item.status === 'IN_PROGRESS');
    if (!pass && next.status === 'IN_PROGRESS') {
      try {
        // A previous client may have closed the pass without moving the count
        // header to review. CreatePass is idempotent when another tab already
        // repaired the same count, then reload the authoritative view.
        await createPass(id, 'OPEN');
        applyPayload(await getCount(id));
        pass = count?.passes.find((item) => item.mode === 'OPEN' && item.status === 'IN_PROGRESS');
      } catch (cause) {
        error = messageFrom(cause, 'Sayım turu başlatılamadı.');
        return;
      }
    }
    if (!pass || activeSessionID) return;
    try {
      const result = await createSession(id, pass.id, deviceID());
      const source = asRecord(result);
      activeSessionID = String(source.session_id ?? source.id ?? '');
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Sayım oturumu başlatılamadı.';
    }
  }

  function deviceID() {
    if (typeof window === 'undefined') return 'ssr-device';
    const key = 'varyaone:count-device-id';
    const saved = localStorage.getItem(key);
    if (saved) return saved;
    const value = `device-${globalThis.crypto?.randomUUID?.() ?? Date.now()}`;
    localStorage.setItem(key, value);
    return value;
  }

  async function load() {
    loading = true;
    error = '';
    syncState = 'syncing';
    try {
      const result = await getCount(id);
      applyPayload(result);
      try {
        const session = await api<Session>('/session');
        canEditCount = session.permissions.includes('inventory.count.post');
      } catch {
        canEditCount = false;
      }
      if (count && canEditCount) await openSession(count);
      syncState = 'synced';
    } catch (cause) {
      syncState = 'offline';
      error = cause instanceof Error ? cause.message : 'Sayım çalışma alanı alınamadı.';
    } finally {
      loading = false;
      await tick();
      scanElement?.focus();
    }
  }

  function quantityText(value: string) {
    const normalized = value.trim().replace(',', '.');
    return /^(?:0|[1-9]\d*)(?:\.\d{1,8})?$/.test(normalized) ? normalized : null;
  }

  function messageFrom(cause: unknown, fallback: string) {
    if (cause instanceof Error && cause.message) return cause.message;
    if (cause && typeof cause === 'object' && 'message' in cause) {
      const message = (cause as { message?: unknown }).message;
      if (typeof message === 'string' && message.trim()) return message;
    }
    return fallback;
  }

  async function focusErrorNotice() {
    await tick();
    errorElement?.focus();
  }

  function positiveQuantity(value: string | null) {
    return Boolean(value && !/^0(?:\.0*)?$/.test(value));
  }

  async function submitScan(event: SubmitEvent) {
    event.preventDefault();
    const parsed = parseWedgeInput(scanInput);
    if (!parsed || !activeSessionID || !activePass) {
      feedback = !activePass ? 'Sayım geçişi hazır değil.' : 'Barkod alanını doldurun.';
      return;
    }
    const entered = parsed.quantity ?? quantityText(multiplier);
    if (!entered || !positiveQuantity(entered)) {
      feedback = 'Barkod okutma miktarı 0’dan büyük olmalıdır.';
      return;
    }
    busy = true;
    feedback = '';
    try {
      const response = await sendScanEvents(id, activeSessionID, [
        {
          event_id: createStableEventId(),
          barcode: parsed.barcode,
          quantity: entered,
          scanned_at: new Date().toISOString()
        }
      ]);
      const events = Array.isArray(asRecord(response).events)
        ? (asRecord(response).events as unknown[])
        : [];
      const status = String(asRecord(events[0]).resolution_status ?? '');
      if (status === 'UNKNOWN')
        feedback = 'Bilinmeyen barkod sayıma eklenmedi; inceleme kaydı oluştu.';
      else if (status === 'AMBIGUOUS')
        feedback = 'Barkod birden fazla ürüne bağlı; sayıma eklenmedi.';
      else if (status === 'OUT_OF_SCOPE')
        feedback = 'Barkod tek satıra bağlanamadı; inceleme gerekiyor.';
      else feedback = `${formatQuantity(entered)} miktar kaydedildi.`;
      applyPayload(await getCount(id));
      scanInput = '';
      multiplier = '1';
    } catch (cause) {
      error = messageFrom(cause, 'Barkod kaydedilemedi.');
    } finally {
      busy = false;
      await tick();
      scanElement?.focus();
    }
  }

  async function saveLine(line: CountLine): Promise<boolean> {
    if (busy) return false;
    const value = quantityText(lineQuantities[line.id] ?? '');
    if (value === null) {
      lineErrors = { ...lineErrors, [line.id]: '0 veya geçerli bir miktar girin.' };
      return false;
    }
    if (!activePass) return false;
    busy = true;
    lineErrors = { ...lineErrors, [line.id]: '' };
    try {
      const response = await correctLine(
        id,
        line.id,
        activePass.id,
        createStableEventId('correction'),
        value,
        'Sayım çalışma alanı manuel miktar girişi'
      );
      void response;
      applyPayload(await getCount(id));
      feedback = `${line.product_name} miktarı kaydedildi.`;
      return true;
    } catch (cause) {
      lineErrors = {
        ...lineErrors,
        [line.id]: messageFrom(cause, 'Miktar kaydedilemedi.')
      };
      return false;
    } finally {
      busy = false;
    }
  }

  async function handleQuantityKeydown(event: KeyboardEvent, line: CountLine) {
    if (event.key !== 'Enter' || event.isComposing) return;
    event.preventDefault();
    const saved = await saveLine(line);
    if (!saved) return;
    await tick();
    const inputs = Array.from(
      document.querySelectorAll<HTMLInputElement>('[data-counted-quantity]')
    ).filter((input) => !input.disabled && input.getClientRects().length > 0);
    const currentIndex = inputs.findIndex((input) => input.dataset.countedQuantity === line.id);
    const next = inputs[currentIndex + 1];
    if (next) {
      next.focus();
      next.scrollIntoView({ block: 'nearest' });
    } else {
      inputs[currentIndex]?.focus();
    }
  }

  async function addStockLine(selection: CountStockPickerSelection) {
    if (!count || !canEditCount || count.scope_mode !== 'FULL') return;
    const duplicate = count.lines.some(
      (line) =>
        line.product_id === selection.productID &&
        (line.variant_id ?? '') === (selection.variantID ?? '')
    );
    if (duplicate) {
      feedback = 'Bu ürün veya varyant sayım satırlarında zaten bulunuyor.';
      return;
    }
    busy = true;
    error = '';
    try {
      applyPayload(await addCountLine(id, selection.productID, selection.variantID ?? ''));
      feedback = `${selection.productName}${selection.variantLabel ? ` · ${selection.variantLabel}` : ''} sayıma eklendi.`;
    } catch (cause) {
      error = messageFrom(cause, 'Stok sayıma eklenemedi.');
    } finally {
      busy = false;
    }
  }

  async function submitReview() {
    if (!activePass || !count || count.status !== 'IN_PROGRESS') return;
    if (reviewSubmissionBlocked) {
      error = reviewSubmissionMessage;
      feedback = '';
      await focusErrorNotice();
      return;
    }
    busy = true;
    try {
      const response = await submitPass(id, activePass.id);
      applyPayload(response);
      await load();
      recountKey = '';
      stage = 'review';
      feedback = 'Sayım incelemeye gönderildi.';
    } catch (cause) {
      error = messageFrom(cause, 'Sayım incelemeye gönderilemedi.');
    } finally {
      busy = false;
    }
  }

  async function returnToCounting() {
    if (!count || count.status !== 'REVIEW' || !canEditCount) return;
    busy = true;
    error = '';
    feedback = '';
    try {
      recountKey ||= `stock-count-recount:${id}:${Date.now()}`;
      const response = await recountCount(id, count.version, recountKey);
      activeSessionID = '';
      lineErrors = {};
      applyPayload(response);
      stage = 'count';
      if (count && canEditCount) await openSession(count);
      feedback = 'Sayım tekrar sürüyor moduna alındı.';
    } catch (cause) {
      error = messageFrom(cause, 'Sayım yeniden sayıma alınamadı.');
      await focusErrorNotice();
    } finally {
      busy = false;
    }
  }

  async function syncNow() {
    syncState = 'syncing';
    try {
      const response = await syncCount(id, syncCursor);
      const source = asRecord(response);
      syncCursor = String(source.next_cursor ?? source.cursor ?? syncCursor);
      applyPayload(response);
      syncState = 'synced';
      feedback = 'Sayım güncellendi.';
    } catch (cause) {
      syncState = 'offline';
      error = messageFrom(cause, 'Sayım güncellenemedi.');
    }
  }

  async function post() {
    if (!count) return;
    if (reviewBlocked) {
      error = 'Eksik sayım satırları veya açık inceleme kayıtları tamamlanmadan sayıma işlenemez.';
      feedback = '';
      await focusErrorNotice();
      return;
    }
    busy = true;
    error = '';
    feedback = '';
    try {
      postKey ||= `stock-count-post:${id}:${Date.now()}`;
      applyPayload(await postCount(id, count.version, postKey));
      feedback = 'Sayım işlendi.';
    } catch (cause) {
      error = messageFrom(cause, 'Sayım işlenemedi. Fiş güncel olmayabilir.');
      await focusErrorNotice();
    } finally {
      busy = false;
    }
  }

  let cancelConfirmOpen = $state(false);

  function cancel() {
    if (!count) return;
    cancelConfirmOpen = true;
  }

  async function runCancel() {
    if (!count) return;
    busy = true;
    try {
      applyPayload(await cancelCount(id, count.version));
      feedback = 'Sayım iptal edildi.';
    } catch (cause) {
      error = messageFrom(cause, 'Sayım iptal edilemedi.');
    } finally {
      busy = false;
    }
  }

  async function downloadExcel() {
    if (!count) return;
    busy = true;
    try {
      const job = await createExport('STOCK_COUNT', count.id, 'XLSX');
      window.location.assign(`/api/v1/exports/${encodeURIComponent(job.id)}/download`);
    } catch (cause) {
      error = messageFrom(cause, 'Excel dosyası oluşturulamadı.');
    } finally {
      busy = false;
    }
  }

  async function importExcel(event: Event) {
    const input = event.currentTarget as HTMLInputElement;
    const file = input.files?.[0];
    input.value = '';
    if (!file) return;
    if (!/\.(csv|xlsx)$/i.test(file.name)) {
      error = 'CSV veya XLSX dosyası seçin.';
      return;
    }
    busy = true;
    error = '';
    try {
      const job = await uploadImport(file, 'STOCK_COUNT', id);
      const analysis = await analyzeImport(job.id);
      if (!analysis.preview.can_commit) {
        feedback = `${analysis.preview.invalid_rows} satır düzeltilmeli; stok değişmedi.`;
        return;
      }
      await commitImport(job.id, false, undefined, analysis.analysis_revision);
      applyPayload(await getCount(id));
      feedback = `${analysis.preview.total_rows} sayım satırı aktarıldı.`;
    } catch (cause) {
      error = messageFrom(cause, 'Excel aktarımı tamamlanamadı.');
    } finally {
      busy = false;
    }
  }

  onMount(async () => {
    await load();
  });
</script>

<svelte:head><title>{count?.number ?? 'Stok Sayımı'} · Varya One</title></svelte:head>

<ConfirmDialog
  bind:open={cancelConfirmOpen}
  title="Sayım fişini iptal et"
  description="Bu sayım fişi iptal edilsin mi? Bu işlem geri alınamaz."
  confirmLabel="Sayımı iptal et"
  onConfirm={runCancel}
/>

{#if loading}
  <div class="panel loading" role="status">Sayım çalışma alanı yükleniyor…</div>
{:else if !count}
  <section class="panel error-panel" role="alert">
    <h1>Sayım açılamadı</h1>
    <p>{error || 'Kayıt bulunamadı.'}</p>
    <a class="button secondary" href="/stok/sayim">Listeye dön</a>
  </section>
{:else}
  <header class="workspace-header">
    <div>
      <a class="back-link" href="/stok/sayim"><ArrowLeft aria-hidden="true" /> Sayım listesi</a>
      <h1>{count.number}</h1>
      <p>
        {#if count.warehouse_id}
          <a
            class="warehouse-link"
            href={`/stok/depolar/${encodeURIComponent(count.warehouse_id)}`}
          >
            {count.warehouse}{count.warehouse_code ? ` · ${count.warehouse_code}` : ''}
          </a>
        {:else}
          <span>{count.warehouse}{count.warehouse_code ? ` · ${count.warehouse_code}` : ''}</span>
        {/if}
        · {count.status === 'REVIEW'
          ? 'İnceleme'
          : count.status === 'POSTED'
            ? 'İşlendi'
            : 'Sayım sürüyor'}
      </p>
      {#if count.description}<p class="count-description">{count.description}</p>{/if}
    </div>
    <div class="header-actions">
      <span class="sync-status" role="status"
        >{syncState === 'syncing'
          ? 'Güncelleniyor'
          : syncState === 'offline'
            ? 'Bağlantı yok'
            : 'Güncel'}</span
      >
      <Button
        variant="secondary"
        size="sm"
        onclick={() => void syncNow()}
        disabled={syncState === 'syncing'}
      >
        <RefreshCw aria-hidden="true" /> Yenile
      </Button>
      <Button
        variant="secondary"
        size="sm"
        onclick={() => countImportElement?.click()}
        disabled={busy ||
          !canEditCount ||
          !activePass ||
          count.status === 'POSTED' ||
          count.status === 'CANCELLED'}>Excel’den yükle</Button
      >
      <Button variant="secondary" size="sm" onclick={() => void downloadExcel()} disabled={busy}
        >Excel indir</Button
      >
      <input
        bind:this={countImportElement}
        class="sr-only"
        type="file"
        accept=".csv,.xlsx"
        onchange={importExcel}
        aria-label="Sayım Excel dosyası"
      />
      <Button
        variant="secondary"
        size="sm"
        onclick={() => void cancel()}
        disabled={busy ||
          !canEditCount ||
          count.status === 'POSTED' ||
          count.status === 'CANCELLED'}>İptal</Button
      >
    </div>
  </header>

  {#if error}<div bind:this={errorElement} class="notice error" role="alert" tabindex="-1">
      {error}
    </div>{/if}
  {#if !canEditCount}<div class="notice" role="status">
      Bu sayımda değişiklik yapmak için sayım işlem yetkiniz gerekir.
    </div>{/if}
  {#if feedback}<div class="notice success" role="status">{feedback}</div>{/if}

  <section class="count-summary" aria-label="Sayım özeti">
    <div><span>Depo</span><strong>{count.warehouse}</strong></div>
    <div><span>Toplam satır</span><strong>{count.lines.length}</strong></div>
    <div><span>Sayılmadı</span><strong>{remainingLineCount}</strong></div>
    <div class:attention={differenceLineCount > 0}>
      <span>Fark</span><strong>{differenceLineCount}</strong>
    </div>
  </section>

  {#if stage === 'count' && count.status === 'IN_PROGRESS'}
    <section class="panel scan-panel" aria-labelledby="scan-heading">
      <div class="section-heading">
        <div>
          <h2 id="scan-heading">Barkod okut</h2>
          <p>
            Donanım barkod okuyucu veya manuel giriş kullanın. Sistem miktarı tabloda her zaman
            görünür.
          </p>
        </div>
        <ScanLine aria-hidden="true" />
      </div>
      <form class="scan-form" aria-label="Barkod girişi" onsubmit={submitScan}>
        <label class="scan-input"
          ><span>Barkod</span><input
            bind:this={scanElement}
            bind:value={scanInput}
            class="scan-control"
            placeholder="Barkodu okutun veya yazın"
            autocomplete="off"
          /></label
        >
        <label class="multiplier"
          ><span>Miktar / çarpan</span><Input
            bind:value={multiplier}
            inputmode="decimal"
            aria-label="Miktar veya çarpan"
          /></label
        >
        <Button type="submit" disabled={busy || !canEditCount || !activeSessionID}
          ><ScanLine aria-hidden="true" /> Ekle <span class="shortcut">Enter</span></Button
        >
      </form>
      <p class="helper">Barkod sonuna <kbd>x2</kbd> yazarak çarpan gönderebilirsiniz.</p>
    </section>
  {/if}

  <section class="panel lines-panel" aria-labelledby="lines-heading">
    <div class="section-heading">
      <div>
        <h2 id="lines-heading">Sayım satırları</h2>
        <p>{filteredLines.length} satır · {countStateLabel(count.status)}</p>
      </div>
      <div class="line-actions">
        {#if stage === 'count' && count.status === 'IN_PROGRESS' && count.scope_mode === 'FULL'}
          <Button
            variant="outline"
            size="sm"
            onclick={() => (stockPickerOpen = true)}
            disabled={busy || !canEditCount || !count.warehouse_id}>Stok seç</Button
          >
        {/if}
        <label class="line-search"
          ><span class="sr-only">Satır ara</span><Input
            bind:value={lineSearch}
            placeholder="Ürün, varyant veya barkod ara"
          /></label
        >
      </div>
    </div>
    {#if stage === 'review'}
      <div class="review-bar">
        <div class="review-bar-copy">
          <strong>Sayım incelemede</strong>
          <p>Sorunlu satırları yeniden kontrol etmek için sayımı tekrar sayıma alabilirsiniz.</p>
        </div>
        <Button
          variant="outline"
          size="sm"
          onclick={() => void returnToCounting()}
          disabled={busy || !canEditCount || count.status !== 'REVIEW'}
          >{#if busy}<LoaderCircle class="spin" aria-hidden="true" />{/if} Yeniden sayıma dön</Button
        >
      </div>
      {#if reviewBlocked}
        <p id="review-blocked" class="review-blocked" role="status">
          {#if remainingLineCount > 0}{remainingLineCount} satır henüz sayılmadı.{/if}
          {#if openExceptionCount > 0}
            {#if remainingLineCount > 0}{' '}{/if}{openExceptionCount} inceleme kaydı çözülmeli.
          {/if}
          {#if lineExceptionOnlyCount > 0}
            {#if remainingLineCount > 0 || openExceptionCount > 0}{' '}{/if}{lineExceptionOnlyCount} satırdaki
            inceleme kaydı çözülmeli.
          {/if}
          Sayımı işlemek için bunları tamamlayın.
        </p>
      {/if}
    {/if}
    {#if stage === 'count' && count.status === 'IN_PROGRESS' && openExceptionCount > 0}
      <p class="review-note" role="status">
        {openExceptionCount} açık inceleme kaydı korunuyor; yeniden incelemeye gönderildiğinde tekrar
        değerlendirilecek.
      </p>
    {/if}
    {#if stage === 'count' && count.status === 'IN_PROGRESS' && reviewSubmissionBlocked}
      <p id="review-submit-blocked" class="review-blocked" role="status">
        {reviewSubmissionMessage}
      </p>
    {/if}
    <div class="table-scroll">
      <table>
        <thead
          ><tr
            ><th scope="col">Ürün / varyant</th><th scope="col">Barkod</th><th scope="col">Birim</th
            ><th scope="col" class="numeric">Sistem</th><th scope="col" class="numeric">Sayılan</th
            ><th scope="col" class="numeric">Fark</th><th scope="col">Durum</th><th scope="col"
              ><span class="sr-only">İşlem</span></th
            ></tr
          ></thead
        >
        <tbody>
          {#each filteredLines as line (line.id)}
            {@const status = lineStatus(line)}
            <tr class:has-difference={status.label === 'Fark var'}>
              <td
                ><strong>{line.product_name}</strong><small
                  >{line.product_code ?? 'Kod yok'}{line.variant_name
                    ? ` · ${line.variant_name}`
                    : ''}</small
                ></td
              >
              <td class="mono">{line.barcode ?? '—'}</td>
              <td>{line.unit ?? '—'}</td>
              <td class="numeric quantity">{displayedQuantity(line.expected_quantity)}</td>
              <td class="numeric counted-cell"
                ><Input
                  bind:value={lineQuantities[line.id]}
                  placeholder="—"
                  inputmode="decimal"
                  aria-label={`${line.product_name} sayılan miktar`}
                  data-counted-quantity={line.id}
                  onkeydown={(event) => void handleQuantityKeydown(event, line)}
                  aria-invalid={Boolean(lineErrors[line.id])}
                  aria-describedby={lineErrors[line.id] ? `line-error-${line.id}` : undefined}
                  disabled={stage === 'review' || count.status !== 'IN_PROGRESS'}
                /></td
              >
              <td class="numeric quantity"
                >{line.difference == null ? '—' : displayedQuantity(line.difference)}</td
              >
              <td
                ><Badge tone={status.tone}>{status.label}</Badge>{#if lineErrors[line.id]}<small
                    id={`line-error-${line.id}`}
                    class="field-error">{lineErrors[line.id]}</small
                  >{/if}</td
              >
              <td
                ><Button
                  variant="outline"
                  size="sm"
                  onclick={() => void saveLine(line)}
                  disabled={busy ||
                    !canEditCount ||
                    !activePass ||
                    stage === 'review' ||
                    count.status !== 'IN_PROGRESS'}>Kaydet</Button
                ></td
              >
            </tr>
          {:else}<tr
              ><td colspan="8" class="empty"
                >Başlangıçta stok bakiyesi olan ürün veya varyant bulunamadı. Bilinen barkodu
                okutarak sıfır beklentili satır ekleyebilirsiniz.</td
              ></tr
            >{/each}
        </tbody>
      </table>
    </div>
  </section>

  {#if count.warehouse_id}
    <CountStockPickerDialog
      bind:open={stockPickerOpen}
      warehouseID={count.warehouse_id}
      onSelect={(selection) => void addStockLine(selection)}
    />
  {/if}

  <footer class="footer-actions">
    {#if stage === 'count' && count.status === 'IN_PROGRESS'}
      <Button
        onclick={() => void submitReview()}
        disabled={busy || !canEditCount || !activePass || reviewSubmissionBlocked}
        aria-describedby={reviewSubmissionBlocked ? 'review-submit-blocked' : undefined}
        ><Send aria-hidden="true" /> İncelemeye gönder</Button
      >
    {:else if stage === 'review' && count.status === 'REVIEW'}
      <Button
        onclick={() => void post()}
        disabled={busy || !canEditCount || reviewBlocked}
        aria-describedby={reviewBlocked ? 'review-blocked' : undefined}
        >{#if busy}<LoaderCircle class="spin" aria-hidden="true" /> İşleniyor…{:else}<Send
            aria-hidden="true"
          /> Sayımı işle{/if}</Button
      >
    {/if}
    <small>Başlangıç: {formatDate(count.started_at ?? count.snapshot_at, true)}</small>
    <small>Bitiş: {formatDate(count.finished_at, true)}</small>
  </footer>
{/if}

<style>
  .workspace-header,
  .section-heading,
  .header-actions,
  .footer-actions,
  .scan-form,
  .review-bar,
  .line-actions {
    display: flex;
    align-items: center;
    gap: 10px;
  }
  .workspace-header,
  .section-heading,
  .review-bar {
    justify-content: space-between;
    align-items: flex-start;
  }
  .workspace-header {
    margin-bottom: 12px;
  }
  h1 {
    margin: 4px 0 2px;
    font-size: 22px;
  }
  h2 {
    margin: 0;
    font-size: 15px;
  }
  p {
    margin: 3px 0 0;
    color: var(--text-muted);
    font-size: 12px;
  }
  .back-link {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    color: var(--primary);
    font-size: 12px;
    text-decoration: none;
  }
  .warehouse-link {
    color: var(--primary);
    font-weight: 650;
    text-decoration: none;
  }
  .warehouse-link:hover {
    text-decoration: underline;
  }
  .count-description {
    max-width: 680px;
    white-space: pre-wrap;
  }
  .sync-status {
    color: var(--text-muted);
    font-size: 11px;
  }
  .panel {
    margin-bottom: 12px;
    padding: 16px;
  }
  .notice {
    margin-bottom: 12px;
    padding: 10px 12px;
    border-radius: var(--radius-control);
    font-size: 12px;
  }
  .notice.error {
    border: 1px solid var(--danger);
    color: var(--danger);
    background: color-mix(in srgb, var(--danger) 8%, var(--surface));
  }
  .notice.success {
    border: 1px solid var(--success);
    color: var(--success);
    background: color-mix(in srgb, var(--success) 8%, var(--surface));
  }
  .count-summary {
    display: grid;
    grid-template-columns: repeat(4, minmax(0, 1fr));
    gap: 8px;
    margin-bottom: 12px;
  }
  .count-summary > div {
    display: grid;
    gap: 3px;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    padding: 10px 12px;
    background: var(--surface);
  }
  .count-summary span {
    color: var(--text-muted);
    font-size: 10px;
    font-weight: 650;
  }
  .count-summary strong {
    overflow: hidden;
    font-size: 18px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .count-summary .attention {
    border-color: color-mix(in srgb, var(--warning) 55%, var(--border));
  }
  .scan-panel {
    border-color: color-mix(in srgb, var(--primary) 35%, var(--border));
  }
  .scan-form {
    align-items: end;
    margin-top: 14px;
  }
  .scan-input {
    flex: 1;
  }
  .scan-control {
    box-sizing: border-box;
    width: 100%;
    height: var(--control-height);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    padding: 0 10px;
    background: var(--surface);
    color: var(--text);
    outline: none;
  }
  .scan-control:focus {
    border-color: var(--primary);
    box-shadow: 0 0 0 2px var(--focus);
  }
  .multiplier {
    width: 150px;
  }
  label span {
    display: block;
    margin-bottom: 4px;
    color: var(--text-muted);
    font-size: 11px;
    font-weight: 650;
  }
  .helper {
    margin-top: 8px;
  }
  kbd {
    border: 1px solid var(--border);
    border-radius: 4px;
    padding: 1px 4px;
    background: var(--surface-muted);
    font-family: ui-monospace, monospace;
    font-size: 11px;
  }
  .line-search {
    min-width: min(280px, 42%);
  }
  .line-actions {
    justify-content: flex-end;
    flex-wrap: wrap;
  }
  .table-scroll {
    overflow-x: auto;
    margin-top: 12px;
  }
  table {
    width: 100%;
    min-width: 980px;
    border-collapse: collapse;
    font-size: 12px;
  }
  th {
    padding: 8px;
    border-bottom: 1px solid var(--border-strong);
    color: var(--text-muted);
    text-align: left;
    font-size: 11px;
  }
  td {
    padding: 9px 8px;
    border-bottom: 1px solid var(--border);
    vertical-align: middle;
  }
  td strong,
  td small {
    display: block;
  }
  td small {
    margin-top: 2px;
    color: var(--text-muted);
    font-size: 10px;
  }
  .numeric {
    text-align: right;
  }
  .quantity {
    white-space: nowrap;
    font-variant-numeric: tabular-nums;
  }
  .counted-cell {
    min-width: 130px;
  }
  .counted-cell :global(input) {
    text-align: right;
  }
  .mono {
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
    font-size: 11px;
  }
  .has-difference {
    background: color-mix(in srgb, var(--warning) 5%, var(--surface));
  }
  .field-error {
    color: var(--danger);
  }
  .review-bar {
    margin-top: 12px;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    padding: 9px 10px;
    color: var(--text-muted);
    font-size: 11px;
  }
  .review-bar-copy {
    min-width: 0;
  }
  .review-bar-copy p {
    margin-top: 4px;
  }
  .review-bar strong {
    color: var(--text);
  }
  .review-note {
    margin: 8px 0 0;
    color: var(--warning);
    font-size: 12px;
  }
  .review-blocked {
    margin: 8px 0 0;
    color: var(--danger);
    font-size: 12px;
  }
  .footer-actions {
    justify-content: flex-start;
    margin: 14px 0;
  }
  .footer-actions small {
    color: var(--text-muted);
    font-size: 11px;
  }
  .empty {
    padding: 24px 12px;
    color: var(--text-muted);
    text-align: center;
  }
  .error-panel {
    text-align: center;
  }
  .loading {
    color: var(--text-muted);
  }
  .shortcut {
    margin-left: 4px;
    opacity: 0.75;
    font-size: 10px;
  }
  :global(.spin) {
    animation: spin 0.9s linear infinite;
  }
  @keyframes spin {
    to {
      transform: rotate(360deg);
    }
  }
  @media (prefers-reduced-motion: reduce) {
    :global(.spin) {
      animation: none;
    }
  }
  @media (max-width: 760px) {
    .workspace-header,
    .section-heading,
    .review-bar,
    .scan-form,
    .footer-actions {
      align-items: stretch;
      flex-direction: column;
    }
    .line-actions {
      align-items: stretch;
      flex-direction: column;
    }
    .header-actions {
      justify-content: flex-start;
    }
    .count-summary {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
    .multiplier,
    .line-search {
      width: 100%;
      min-width: 0;
    }
    .scan-form :global(button) {
      width: 100%;
    }
  }
</style>
