<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/state';
  import { ArrowLeft } from '@lucide/svelte';
  import { api, APIRequestError, type Session } from '$lib/api';
  import { Button } from '$lib/components/ui/button';
  import { Badge } from '$lib/components/ui/badge';
  import { formatDate } from '$lib/design/formatters';
  import * as hr from '$lib/features/hr/api';
  import { EmailComposer } from '$lib/components/varya/email-composer';
  import NegativeBalanceReason from '$lib/features/finance/NegativeBalanceReason.svelte';
  import { Dialog } from 'bits-ui';
  import { X } from '@lucide/svelte';
  import {
    money,
    payrollErrorDetails,
    payrollErrorInfo,
    payrollStatusLabel,
    periodLabel,
    statusTone,
    type EmailPreview,
    type PayrollRun
  } from '$lib/features/hr/types';

  let permissions = $state<string[]>([]);
  let run = $state<PayrollRun | null>(null);
  let payslips = $state<any[]>([]);
  let payments = $state<import('$lib/features/hr/types').PayrollPayment[]>([]);
  let accounts = $state<import('$lib/features/hr/types').PaymentAccount[]>([]);
  let payForm = $state({ account_id: '', payment_date: '', description: '', override_reason: '' });
  let payNeedsOverride = $state(false);
  let payOverrideSignature = $state('');
  let reverseReason = $state('');
  let showReverse = $state(false);
  let loading = $state(true);
  let error = $state('');
  let msg = $state('');
  let actionError = $state('');
  let busy = $state(false);

  const runID = $derived(page.params.id ?? '');
  const canCalc = $derived(permissions.includes('hr.payroll.calculate'));
  const canFinalize = $derived(permissions.includes('hr.payroll.finalize'));
  const canPayslip = $derived(permissions.includes('hr.payroll.payslip'));
  const canExport = $derived(permissions.includes('hr.payroll.bulk_export'));
  const canEmail = $derived(permissions.includes('hr.payroll.email'));
  const canPay = $derived(permissions.includes('hr.payroll.pay'));
  const activePayment = $derived(payments.find((p) => p.status === 'PAID') ?? null);
  const tryAccounts = $derived(
    accounts.filter((a) => a.currency === 'TRY' && a.account_type !== '')
  );
  const accountKind = (t: string) => (t === 'BANK' ? 'Banka' : t === 'CASH' ? 'Kasa' : t);

  async function loadSession() {
    try {
      permissions = (await api<Session>('/session')).permissions ?? [];
    } catch {
      permissions = [];
    }
  }

  async function load() {
    loading = true;
    error = '';
    try {
      run = await hr.getPayrollRun(runID);
      if (run.status === 'FINALIZED') {
        payslips = (await hr.listPayslips(runID)).items;
        payments = (await hr.listPayrollPayments(runID)).items;
        if (canPay) {
          try {
            accounts = (await hr.listPaymentAccounts()).items;
          } catch {
            accounts = [];
          }
        }
        if (!payForm.payment_date) {
          // Gelecek tarihli finans hareketi kaydedilemez; bordronun ödeme
          // tarihi ileride ise bugünü öner.
          const today = new Date().toISOString().slice(0, 10);
          payForm.payment_date = run.payment_date > today ? today : run.payment_date;
        }
      }
    } catch (cause) {
      error = cause instanceof APIRequestError ? cause.message : 'Bordro yüklenemedi.';
    } finally {
      loading = false;
    }
  }

  async function act(fn: () => Promise<unknown>, ok: string) {
    if (busy) return;
    busy = true;
    actionError = '';
    msg = '';
    try {
      await fn();
      msg = ok;
      await load();
    } catch (cause) {
      actionError = cause instanceof APIRequestError ? cause.message : 'İşlem başarısız.';
    } finally {
      busy = false;
    }
  }

  async function submitPayment() {
    if (busy || !payForm.account_id) return;
    const signature = payForm.account_id;
    if (payNeedsOverride && signature !== payOverrideSignature) {
      payNeedsOverride = false;
      payForm.override_reason = '';
    }
    if (payNeedsOverride && !payForm.override_reason.trim()) {
      actionError = 'Negatif bakiye için gerekçe zorunludur.';
      return;
    }
    busy = true;
    actionError = '';
    msg = '';
    try {
      await hr.createPayrollPayment(runID, {
        account_id: payForm.account_id,
        payment_date: payForm.payment_date || undefined,
        description: payForm.description.trim() || undefined,
        override_reason: payNeedsOverride ? payForm.override_reason.trim() : undefined
      });
      msg = 'Ödeme oluşturuldu; kasa/banka çıkışı kaydedildi.';
      payNeedsOverride = false;
      payForm.override_reason = '';
      await load();
    } catch (cause) {
      if (
        cause instanceof APIRequestError &&
        cause.code === 'NEGATIVE_BALANCE_CONFIRMATION_REQUIRED'
      ) {
        payNeedsOverride = true;
        payOverrideSignature = signature;
        actionError =
          'Seçili hesap bu ödeme sonrası negatife düşüyor. Gerekçe girip yeniden kaydedin.';
      } else if (cause instanceof APIRequestError && cause.code === 'NEGATIVE_BALANCE_BLOCKED') {
        actionError = 'Seçili hesabın bakiyesi bu ödeme için yetersiz.';
      } else {
        actionError = cause instanceof APIRequestError ? cause.message : 'İşlem başarısız.';
      }
    } finally {
      busy = false;
    }
  }

  function triggerDownload(url: string) {
    const a = document.createElement('a');
    a.href = url;
    a.rel = 'noopener';
    document.body.appendChild(a);
    a.click();
    a.remove();
  }

  async function makeExport(kind: string) {
    await act(async () => {
      const res = await hr.createExport(runID, kind);
      triggerDownload(hr.exportDownloadURL(res.id));
    }, 'Dışa aktarma indiriliyor.');
  }

  let preview = $state<EmailPreview | null>(null);
  let previewLoading = $state(false);
  let previewError = $state('');
  let resend = $state(false);

  const periodText = $derived(run ? periodLabel(run.period_year, run.period_month) : '');

  const failedPayrolls = $derived(
    (run?.employee_payrolls ?? [])
      .filter((ep) => ep.status === 'FAILED')
      .map((ep) => {
        const details = payrollErrorDetails(ep.error_details);
        const info = details[0]
          ? payrollErrorInfo(details[0])
          : { title: 'Bordro hesaplanamadı', hint: '' };
        return { name: ep.employee_name, employeeId: ep.employee_id, ...info };
      })
  );

  const composerRecipients = $derived(
    (preview?.recipients ?? []).map((r) => ({
      email: r.email,
      name: r.name,
      variables: r.variables ?? { ad_soyad: r.name, donem: periodText }
    }))
  );

  let emailOpen = $state(false);

  async function openEmailDialog() {
    previewLoading = true;
    previewError = '';
    emailOpen = true;
    try {
      preview = await hr.emailPreview(runID);
    } catch (cause) {
      previewError = cause instanceof APIRequestError ? cause.message : 'Önizleme alınamadı.';
      preview = null;
    } finally {
      previewLoading = false;
    }
  }

  async function sendPayslipEmail(p: { subject: string; body: string }) {
    const r = await hr.sendEmailBatch(runID, { resend, subject: p.subject, body: p.body });
    msg = 'E-posta gönderimi tamamlandı.';
    return { status: r.status, sent: r.sent, failed: r.failed, skipped: r.skipped };
  }

  onMount(async () => {
    await loadSession();
    await load();
  });
</script>

<svelte:head><title>{run ? run.run_number : 'Bordro'} · Varya One</title></svelte:head>

{#if loading}
  <div class="card">Yükleniyor…</div>
{:else if !run}
  <section class="card" role="alert">
    {error || 'Bordro bulunamadı.'} <a href="/personel/bordro">Listeye dön</a>
  </section>
{:else}
  <header class="page-header">
    <div>
      <a class="back" href="/personel/bordro"><ArrowLeft size={13} aria-hidden="true" />Bordro</a>
      <div class="title-row">
        <h1>{run.run_number}</h1>
        <Badge tone={statusTone(run.status)}>{payrollStatusLabel(run.status)}</Badge>
      </div>
      <p>{periodLabel(run.period_year, run.period_month)} · Ödeme {formatDate(run.payment_date)}</p>
    </div>
    <div class="page-actions">
      {#if canCalc && run.status !== 'FINALIZED'}
        <Button
          onclick={() => act(() => hr.calculatePayrollRun(runID), 'Hesaplama tamamlandı.')}
          disabled={busy}>Hesapla</Button
        >
      {/if}
      {#if canFinalize && run.status === 'CALCULATED'}
        <Button
          onclick={() =>
            act(() => hr.finalizePayrollRun(runID, run!.version), 'Bordro kesinleşti.')}
          disabled={busy}>Kesinleştir</Button
        >
      {/if}
    </div>
  </header>

  {#if msg}<p class="notice ok" role="status">{msg}</p>{/if}
  {#if actionError}<p class="notice error" role="alert">{actionError}</p>{/if}

  <section class="card">
    <div class="totals">
      <div><span>Toplam brüt</span><strong>{money(run.total_gross)} ₺</strong></div>
      <div><span>Toplam net</span><strong>{money(run.total_net)} ₺</strong></div>
      <div><span>İşveren maliyeti</span><strong>{money(run.total_employer_cost)} ₺</strong></div>
    </div>
  </section>

  {#if run.status === 'FINALIZED'}
    <section class="card">
      <h2>Ödeme</h2>
      {#if activePayment}
        <div class="pay-done">
          <div class="totals">
            <div><span>Ödenen net</span><strong>{money(activePayment.amount)} ₺</strong></div>
            <div>
              <span>Hesap</span><strong
                >{activePayment.account_name} · {accountKind(activePayment.account_type)}</strong
              >
            </div>
            <div>
              <span>Ödeme tarihi</span><strong>{formatDate(activePayment.payment_date)}</strong>
            </div>
          </div>
          <p class="pay-note">
            {money(activePayment.amount)} ₺ tutarında çıkış işlemi
            <strong>{accountKind(activePayment.account_type)}</strong> hesabına kaydedildi.
          </p>
          {#if canPay}
            {#if showReverse}
              <div class="reverse-form">
                <input
                  class="reason"
                  bind:value={reverseReason}
                  placeholder="Geri alma gerekçesi"
                />
                <Button
                  variant="outline"
                  disabled={busy || !reverseReason.trim()}
                  onclick={() =>
                    act(
                      () => hr.reversePayrollPayment(activePayment!.id, reverseReason.trim()),
                      'Ödeme geri alındı.'
                    ).then(() => {
                      showReverse = false;
                      reverseReason = '';
                    })}>Onayla</Button
                >
                <button class="link" onclick={() => (showReverse = false)}>Vazgeç</button>
              </div>
            {:else}
              <button class="link danger" onclick={() => (showReverse = true)}
                >Ödemeyi geri al</button
              >
            {/if}
          {/if}
        </div>
      {:else if canPay}
        <form
          class="pay-form"
          onsubmit={(e) => {
            e.preventDefault();
            void submitPayment();
          }}
        >
          <label>
            Kasa / banka hesabı
            <select bind:value={payForm.account_id} class="select" required>
              <option value="">Hesap seçin…</option>
              {#each tryAccounts as a}
                <option value={a.id}>{accountKind(a.account_type)} · {a.name}</option>
              {/each}
            </select>
          </label>
          <label class="grow">
            Açıklama (opsiyonel)
            <input class="select" bind:value={payForm.description} placeholder="Bordro ödemesi" />
          </label>
          <NegativeBalanceReason bind:reason={payForm.override_reason} active={payNeedsOverride} />
          <Button type="submit" disabled={busy || !payForm.account_id}
            >Ödemeyi oluştur ({money(run.total_net)} ₺)</Button
          >
        </form>
        {#if !tryAccounts.length}
          <p class="state">Önce Banka&Kasa’dan bir TRY kasa veya banka hesabı tanımlayın.</p>
        {/if}
      {:else}
        <p class="state">
          {payments.length ? 'Bu bordronun ödemesi geri alınmış.' : 'Ödeme henüz oluşturulmadı.'}
        </p>
      {/if}

      {#if payments.some((p) => p.status === 'REVERSED')}
        <table class="reversed">
          <thead
            ><tr
              ><th>Geri alınan ödeme</th><th class="num">Tutar</th><th>Tarih</th><th>Gerekçe</th
              ></tr
            ></thead
          >
          <tbody>
            {#each payments.filter((p) => p.status === 'REVERSED') as p}
              <tr>
                <td>{p.account_name} · {accountKind(p.account_type)}</td>
                <td class="num">{money(p.amount)} ₺</td>
                <td>{formatDate(p.payment_date)}</td>
                <td>{p.reversal_reason ?? '—'}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      {/if}
    </section>
  {/if}

  {#if run.generations?.length}
    <section class="card">
      <h2>Hesaplama sürümleri</h2>
      <table>
        <thead><tr><th>#</th><th>Durum</th><th>Başlangıç</th><th>Hatalar</th></tr></thead>
        <tbody>
          {#each run.generations as g}
            <tr>
              <td>{g.generation_no}</td>
              <td><Badge tone={statusTone(g.status)}>{payrollStatusLabel(g.status)}</Badge></td>
              <td>{formatDate(g.started_at, true)}</td>
              <td
                >{Array.isArray(g.error_summary) && g.error_summary.length
                  ? `${g.error_summary.length} hata`
                  : '—'}</td
              >
            </tr>
          {/each}
        </tbody>
      </table>
    </section>
  {/if}

  <section class="card">
    <h2>Çalışan bordroları</h2>
    {#if !run.employee_payrolls?.length}
      <p class="state">Henüz hesaplanmadı.</p>
    {:else}
      <div class="scroll">
        <table>
          <thead
            ><tr
              ><th>Çalışan</th><th>Durum</th><th class="num">Brüt</th><th class="num">Net</th><th
                class="num">İşveren maliyeti</th
              ><th>Hata</th></tr
            ></thead
          >
          <tbody>
            {#each run.employee_payrolls as ep}
              <tr>
                <td>{ep.employee_name}</td>
                <td><Badge tone={statusTone(ep.status)}>{payrollStatusLabel(ep.status)}</Badge></td>
                <td class="num">{money(ep.gross)} ₺</td>
                <td class="num">{money(ep.net)} ₺</td>
                <td class="num">{money(ep.employer_cost)} ₺</td>
                <td class="err">
                  {#if ep.status === 'FAILED'}
                    {payrollErrorInfo(payrollErrorDetails(ep.error_details)[0] ?? { code: '' })
                      .title}
                  {:else}—{/if}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}

    {#if failedPayrolls.length}
      <div class="fail-guide">
        <h3>Hesaplanamayan çalışanlar ({failedPayrolls.length})</h3>
        <ul>
          {#each failedPayrolls as f}
            <li>
              <strong>{f.name}</strong> — {f.title}
              {#if f.hint}<p>{f.hint}</p>{/if}
              <a class="fix-link" href={`/personel/calisanlar/${f.employeeId}`}
                >Çalışan kartını aç →</a
              >
            </li>
          {/each}
        </ul>
        <p class="fail-foot">
          Düzeltmeleri yaptıktan sonra bu sayfada <strong>“Hesapla”</strong> ile yeniden deneyin.
        </p>
      </div>
    {/if}
  </section>

  {#if run.status === 'FINALIZED'}
    <section class="card">
      <h2>Ücret pusulaları</h2>
      <div class="actions">
        {#if canPayslip}<Button
            variant="outline"
            onclick={() => act(() => hr.generatePayslips(runID), 'Pusulalar üretildi.')}
            disabled={busy}>Pusulaları üret</Button
          >{/if}
        {#if canExport}
          <Button variant="outline" onclick={() => makeExport('PAYSLIP_ZIP')} disabled={busy}
            >ZIP indir</Button
          >
          <Button variant="outline" onclick={() => makeExport('SUMMARY_CSV')} disabled={busy}
            >CSV özet</Button
          >
        {/if}
      </div>
      {#if payslips.length}
        <table>
          <thead><tr><th>Çalışan</th><th class="num">Boyut</th><th>Tarih</th><th></th></tr></thead>
          <tbody>
            {#each payslips as ps}
              <tr>
                <td>{ps.employee_name}</td>
                <td class="num">{(ps.size_bytes / 1024).toFixed(0)} KB</td>
                <td>{formatDate(ps.created_at)}</td>
                <td><a href={hr.payslipDownloadURL(ps.id)}>İndir</a></td>
              </tr>
            {/each}
          </tbody>
        </table>
      {:else}
        <p class="state">Henüz pusula üretilmedi.</p>
      {/if}
    </section>

    {#if canEmail}
      <section class="card">
        <div class="email-head">
          <div>
            <h2>E-posta ile gönderim</h2>
          </div>
          <Button onclick={openEmailDialog}>E-posta gönder…</Button>
        </div>
      </section>

      <Dialog.Root bind:open={emailOpen}>
        <Dialog.Portal>
          <Dialog.Overlay class="ec-overlay" />
          <Dialog.Content class="ec-dialog">
            <div class="ec-head">
              <div>
                <Dialog.Title>Ücret pusulası e-postası</Dialog.Title>
                <Dialog.Description
                  >{periodText} · pusulalar seçili taslak/metinle gönderilir.</Dialog.Description
                >
              </div>
              <Dialog.Close class="ec-close" aria-label="Kapat"><X size={17} /></Dialog.Close>
            </div>

            <div class="ec-body">
              {#if previewLoading}
                <p class="state">Hazırlanıyor…</p>
              {:else if previewError}
                <p class="notice error">{previewError}</p>
              {:else if preview}
                <label class="ec-resend">
                  <input type="checkbox" bind:checked={resend} /> Daha önce gönderilenleri tekrar gönder
                </label>
                <EmailComposer
                  scope="PAYROLL_PAYSLIP"
                  recipients={composerRecipients}
                  defaultSubject={preview.default_subject}
                  defaultBody={preview.default_body}
                  variables={preview.variables}
                  attachmentNote="Her çalışana kendi ücret hesap pusulası PDF'i eklenir."
                  lockRecipients
                  onSend={sendPayslipEmail}
                  onDone={() => (emailOpen = false)}
                />
              {/if}
            </div>
          </Dialog.Content>
        </Dialog.Portal>
      </Dialog.Root>
    {/if}
  {/if}
{/if}

<style>
  .card {
    margin-top: 14px;
  }
  .card h2 {
    margin: 0 0 10px;
    font-size: 15px;
  }
  .back {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    color: var(--primary);
    font-size: 12px;
    text-decoration: none;
    margin-bottom: 4px;
  }
  .totals {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 12px;
  }
  .totals span {
    display: block;
    color: var(--text-muted);
    font-size: 11px;
  }
  .totals strong {
    font-size: 16px;
  }
  .pay-form {
    display: flex;
    flex-wrap: wrap;
    gap: 12px;
    align-items: flex-end;
  }
  .pay-form label {
    display: flex;
    flex-direction: column;
    gap: 4px;
    font-size: 11px;
    color: var(--text-muted);
  }
  .pay-form label.grow {
    flex: 1;
    min-width: 180px;
  }
  .pay-form :global(.negative-balance) {
    flex-basis: 100%;
  }
  .select {
    height: var(--control-height, 34px);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    background: var(--surface);
    color: var(--text);
    padding: 0 9px;
    font-size: 12px;
  }
  .pay-done .pay-note {
    margin: 10px 0 6px;
    font-size: 12px;
    color: var(--text-muted);
  }
  .reverse-form {
    display: flex;
    gap: 8px;
    align-items: center;
    margin-top: 8px;
    flex-wrap: wrap;
  }
  .reason {
    height: var(--control-height, 34px);
    min-width: 240px;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    background: var(--surface);
    color: var(--text);
    padding: 0 9px;
    font-size: 12px;
  }
  .link {
    border: 0;
    background: transparent;
    color: var(--primary);
    cursor: pointer;
    font-size: 12px;
    font-weight: 650;
    padding: 0;
  }
  .link:hover {
    text-decoration: underline;
  }
  .link.danger {
    color: var(--danger);
    margin-top: 8px;
  }
  table.reversed {
    margin-top: 14px;
  }
  .actions {
    display: flex;
    gap: 8px;
    flex-wrap: wrap;
    margin-bottom: 10px;
  }
  .scroll {
    overflow-x: auto;
  }
  table {
    width: 100%;
    border-collapse: collapse;
    font-size: 12px;
    margin-top: 6px;
  }
  th,
  td {
    padding: 8px 10px;
    border-bottom: 1px solid var(--border);
    text-align: left;
  }
  th {
    color: var(--text-muted);
    font-weight: 650;
  }
  .err {
    color: var(--danger);
  }
  .fail-guide {
    margin-top: 16px;
    padding: 12px 14px;
    border: 1px solid color-mix(in srgb, var(--danger) 35%, var(--border));
    background: color-mix(in srgb, var(--danger) 7%, var(--surface));
    border-radius: var(--radius-control);
  }
  .fail-guide h3 {
    margin: 0 0 8px;
    font-size: 13px;
  }
  .fail-guide ul {
    margin: 0;
    padding: 0;
    list-style: none;
    display: flex;
    flex-direction: column;
    gap: 10px;
  }
  .fail-guide li {
    font-size: 13px;
  }
  .fail-guide li p {
    margin: 4px 0;
    font-size: 12px;
    color: var(--text-muted);
    max-width: 72ch;
  }
  .fix-link {
    font-size: 12px;
    color: var(--primary);
  }
  .fail-foot {
    margin: 12px 0 0;
    font-size: 12px;
    color: var(--text-muted);
  }
  .state {
    padding: 14px 0;
    text-align: center;
    color: var(--text-muted);
    font-size: 13px;
    margin: 0;
  }
  .title-row {
    display: flex;
    align-items: center;
    gap: 10px;
    flex-wrap: wrap;
  }
  .sub {
    margin: -4px 0 12px;
    font-size: 12px;
    color: var(--text-muted);
  }
  .email-head {
    display: flex;
    justify-content: space-between;
    align-items: flex-start;
    gap: 12px;
    flex-wrap: wrap;
  }
  .ec-resend {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    font-size: 12px;
    color: var(--text-muted);
    margin-bottom: 12px;
  }
  :global(.ec-overlay) {
    position: fixed;
    inset: 0;
    z-index: 70;
    background: rgb(8 26 23 / 42%);
  }
  :global(.ec-dialog) {
    position: fixed;
    z-index: 71;
    top: 50%;
    left: 50%;
    transform: translate(-50%, -50%);
    width: min(620px, calc(100vw - 28px));
    max-height: calc(100vh - 48px);
    display: flex;
    flex-direction: column;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-panel);
    background: var(--surface);
    box-shadow: 0 20px 60px rgb(10 30 27 / 22%);
  }
  .ec-head {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 12px;
    padding: 16px 18px;
    border-bottom: 1px solid var(--border);
  }
  .ec-head :global(h2) {
    margin: 0;
    font-size: 16px;
  }
  .ec-head :global([data-dialog-description]) {
    display: block;
    margin-top: 4px;
    color: var(--text-muted);
    font-size: 12px;
  }
  :global(.ec-close) {
    display: inline-grid;
    place-items: center;
    width: 28px;
    height: 28px;
    border: 0;
    border-radius: var(--radius-control);
    background: transparent;
    color: var(--text-muted);
  }
  :global(.ec-close:hover) {
    background: var(--surface-muted);
    color: var(--text);
  }
  .ec-body {
    padding: 16px 18px;
    overflow-y: auto;
  }
  td.num,
  th.num {
    text-align: right;
    font-variant-numeric: tabular-nums;
  }
</style>
