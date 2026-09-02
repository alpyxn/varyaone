<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { Button } from '$lib/components/ui/button';
  import { APIRequestError } from '$lib/api';
  import { createEmailTemplate, listEmailTemplates } from '$lib/features/email/api';
  import {
    renderVars,
    type EmailComposerRecipient,
    type EmailSendResult,
    type EmailTemplate
  } from '$lib/features/email/types';

  type SendResult = EmailSendResult & { preview?: unknown };

  let {
    scope,
    recipients = [],
    defaultSubject = '',
    defaultBody = '',
    variables = [],
    attachmentNote = '',
    lockRecipients = false,
    onSend,
    onDone
  }: {
    scope: string;
    recipients?: EmailComposerRecipient[];
    defaultSubject?: string;
    defaultBody?: string;
    variables?: string[];
    attachmentNote?: string;
    lockRecipients?: boolean;
    onSend: (p: {
      subject: string;
      body: string;
      recipientEmails: string[];
    }) => Promise<SendResult>;
    onDone?: () => void;
  } = $props();

  let subject = $state(defaultSubject);
  let body = $state(defaultBody);
  let rows = $state<EmailComposerRecipient[]>(recipients.map((r) => ({ ...r })));
  let newEmail = $state('');
  let newName = $state('');

  let templates = $state<EmailTemplate[]>([]);
  let selectedTemplate = $state('');

  let saveMode = $state(false);
  let saveName = $state('');
  let saving = $state(false);

  let sending = $state(false);
  let error = $state('');
  let result = $state<SendResult | null>(null);

  let bodyEl = $state<HTMLTextAreaElement | null>(null);
  let previewIdx = $state(0);

  const emailRe = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

  const classified = $derived(() => {
    const counts = new Map<string, number>();
    for (const r of rows) {
      const e = r.email.trim().toLowerCase();
      if (e) counts.set(e, (counts.get(e) ?? 0) + 1);
    }
    return rows.map((r) => {
      const e = r.email.trim().toLowerCase();
      let status: 'ready' | 'missing' | 'invalid' | 'duplicate' = 'ready';
      if (!e) status = 'missing';
      else if (!emailRe.test(e)) status = 'invalid';
      else if ((counts.get(e) ?? 0) > 1) status = 'duplicate';
      return { ...r, status };
    });
  });

  const readyRows = $derived(classified().filter((r) => r.status === 'ready'));
  const skippedCount = $derived(classified().length - readyRows.length);
  const previewRows = $derived(readyRows.length ? readyRows : classified());
  const previewRow = $derived(previewRows[Math.min(previewIdx, previewRows.length - 1)] ?? null);

  onMount(async () => {
    try {
      templates = (await listEmailTemplates(scope)).items.filter((t) => t.is_active);
    } catch {
      templates = [];
    }
  });

  function applyTemplate() {
    const tpl = templates.find((t) => t.id === selectedTemplate);
    if (!tpl) return;
    subject = tpl.subject;
    body = tpl.body;
  }

  async function insertVariable(name: string) {
    const token = `{{${name}}}`;
    const el = bodyEl;
    if (!el) {
      body += token;
      return;
    }
    const start = el.selectionStart ?? body.length;
    const end = el.selectionEnd ?? body.length;
    body = body.slice(0, start) + token + body.slice(end);
    await tick();
    el.focus();
    el.selectionStart = el.selectionEnd = start + token.length;
  }

  function addRecipient() {
    const email = newEmail.trim();
    if (!email) return;
    rows = [...rows, { email, name: newName.trim(), variables: {} }];
    newEmail = '';
    newName = '';
  }

  function removeRecipient(index: number) {
    rows = rows.filter((_, i) => i !== index);
  }

  async function saveTemplate() {
    if (!saveName.trim() || saving) return;
    saving = true;
    error = '';
    try {
      const created = await createEmailTemplate({
        name: saveName.trim(),
        scope: scope === 'PAYROLL_PAYSLIP' ? 'PAYROLL_PAYSLIP' : 'GENERIC',
        subject,
        body
      });
      templates = [...templates.filter((t) => t.id !== created.id), created].sort((a, b) =>
        a.name.localeCompare(b.name, 'tr')
      );
      selectedTemplate = created.id;
      saveMode = false;
      saveName = '';
    } catch (cause) {
      error = cause instanceof APIRequestError ? cause.message : 'Taslak kaydedilemedi.';
    } finally {
      saving = false;
    }
  }

  async function send() {
    if (sending) return;
    if (!subject.trim() && !body.trim()) {
      error = 'Konu veya gövde girin.';
      return;
    }
    if (readyRows.length === 0) {
      error = 'Gönderime hazır alıcı yok.';
      return;
    }
    sending = true;
    error = '';
    result = null;
    try {
      result = await onSend({
        subject,
        body,
        recipientEmails: readyRows.map((r) => r.email.trim().toLowerCase())
      });
    } catch (cause) {
      error = cause instanceof APIRequestError ? cause.message : 'Gönderim başarısız.';
    } finally {
      sending = false;
    }
  }
</script>

<div class="composer">
  {#if result}
    <div class="done">
      <p class="notice ok">
        Gönderildi: {result.sent} · Başarısız: {result.failed} · Atlandı: {result.skipped}
      </p>
      <Button onclick={() => onDone?.()}>Kapat</Button>
    </div>
  {:else}
    <div class="field">
      <span class="label">Taslak</span>
      <div class="inline">
        <select bind:value={selectedTemplate} class="select">
          <option value="">— Taslak seç —</option>
          {#each templates as tpl (tpl.id)}
            <option value={tpl.id}>{tpl.name}</option>
          {/each}
        </select>
        <Button variant="outline" onclick={applyTemplate} disabled={!selectedTemplate}
          >Uygula</Button
        >
      </div>
    </div>

    <label class="field">
      <span class="label">Konu</span>
      <input class="input" bind:value={subject} placeholder="E-posta konusu" />
    </label>

    <label class="field">
      <span class="label">Gövde</span>
      <textarea class="input body" rows="7" bind:value={body} bind:this={bodyEl}></textarea>
    </label>

    {#if variables.length}
      <div class="vars">
        <span class="hint">Yer tutucular (tıkla ekle):</span>
        {#each variables as v (v)}
          <button type="button" class="chip" onclick={() => insertVariable(v)}>{`{{${v}}}`}</button>
        {/each}
      </div>
    {/if}

    <div class="field">
      <div class="inline between">
        <span class="label">Alıcılar</span>
        {#if !saveMode}
          <button type="button" class="linkish" onclick={() => (saveMode = true)}
            >Taslak olarak kaydet</button
          >
        {/if}
      </div>

      {#if saveMode}
        <div class="inline">
          <input class="input" bind:value={saveName} placeholder="Taslak adı" />
          <Button onclick={saveTemplate} disabled={!saveName.trim() || saving}>Kaydet</Button>
          <Button variant="outline" onclick={() => (saveMode = false)}>Vazgeç</Button>
        </div>
      {/if}

      <p class="recap">
        <strong>{readyRows.length}</strong> kişiye gönderilecek.
        {#if skippedCount > 0}
          <span class="muted">{skippedCount} kişi atlanacak (e-posta yok / geçersiz).</span>
        {/if}
      </p>

      {#if !lockRecipients}
        <div class="rows">
          {#each classified() as row, i (row.email + i)}
            <div class="recipient" class:bad={row.status !== 'ready'}>
              <span class="who">{row.name || row.email || '—'}</span>
              {#if row.status !== 'ready'}<span class="muted">
                  {row.status === 'missing'
                    ? 'e-posta yok'
                    : row.status === 'invalid'
                      ? 'geçersiz'
                      : 'tekrar'}
                </span>{/if}
              <button type="button" class="x" aria-label="Kaldır" onclick={() => removeRecipient(i)}
                >×</button
              >
            </div>
          {/each}
        </div>
        <div class="inline add">
          <input class="input" bind:value={newName} placeholder="Ad (isteğe bağlı)" />
          <input
            class="input"
            bind:value={newEmail}
            placeholder="e-posta@ornek.com"
            onkeydown={(e) => e.key === 'Enter' && (e.preventDefault(), addRecipient())}
          />
          <Button variant="outline" onclick={addRecipient} disabled={!newEmail.trim()}>Ekle</Button>
        </div>
      {/if}
    </div>

    {#if previewRow}
      <div class="preview">
        <div class="preview-head">
          <span class="hint">Önizleme</span>
          <select class="select sm" bind:value={previewIdx}>
            {#each previewRows as opt, i}
              <option value={i}>{opt.name || opt.email}</option>
            {/each}
          </select>
        </div>
        <div class="pv-subject">{renderVars(subject, previewRow.variables ?? {})}</div>
        <pre class="pv-body">{renderVars(body, previewRow.variables ?? {})}</pre>
        {#if attachmentNote}<p class="attach">📎 {attachmentNote}</p>{/if}
      </div>
    {/if}

    {#if error}<p class="notice error">{error}</p>{/if}

    <div class="foot">
      {#if onDone}<Button variant="outline" onclick={() => onDone?.()}>Vazgeç</Button>{/if}
      <Button onclick={send} disabled={sending || readyRows.length === 0}>
        {sending ? 'Gönderiliyor…' : `${readyRows.length} kişiye gönder`}
      </Button>
    </div>
  {/if}
</div>

<style>
  .composer {
    display: flex;
    flex-direction: column;
    gap: 14px;
  }
  .field {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }
  .label {
    font-size: 12px;
    font-weight: 650;
    color: var(--text-muted);
  }
  .inline {
    display: flex;
    gap: 8px;
    flex-wrap: wrap;
    align-items: center;
  }
  .inline.between {
    justify-content: space-between;
  }
  .inline.add {
    margin-top: 8px;
  }
  .input,
  .select {
    width: 100%;
    padding: 8px 10px;
    font: inherit;
    border: 1px solid var(--border);
    border-radius: 8px;
    background: var(--surface);
    color: var(--text);
  }
  .select {
    max-width: 280px;
  }
  .select.sm {
    max-width: 220px;
    padding: 5px 8px;
    font-size: 12px;
  }
  .body {
    resize: vertical;
    font-family: inherit;
  }
  .vars {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    align-items: center;
  }
  .hint {
    font-size: 11px;
    color: var(--text-muted);
  }
  .chip {
    font-size: 11px;
    padding: 3px 8px;
    border-radius: 999px;
    border: 1px solid var(--border);
    background: var(--surface);
    color: var(--primary);
    cursor: pointer;
  }
  .linkish {
    background: none;
    border: none;
    color: var(--primary);
    font-size: 12px;
    cursor: pointer;
    padding: 0;
  }
  .recap {
    margin: 4px 0 0;
    font-size: 13px;
  }
  .muted {
    color: var(--text-muted);
    font-size: 12px;
  }
  .rows {
    display: flex;
    flex-direction: column;
    gap: 4px;
    margin-top: 6px;
  }
  .recipient {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 5px 8px;
    border: 1px solid var(--border);
    border-radius: 8px;
    font-size: 12px;
  }
  .recipient.bad {
    border-color: color-mix(in srgb, var(--danger) 30%, var(--border));
  }
  .who {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .x {
    background: none;
    border: none;
    font-size: 16px;
    line-height: 1;
    color: var(--text-muted);
    cursor: pointer;
  }
  .preview {
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 10px 12px;
    background: color-mix(in srgb, var(--primary) 4%, var(--surface));
  }
  .preview-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 10px;
    margin-bottom: 8px;
  }
  .pv-subject {
    font-weight: 650;
    margin: 4px 0;
  }
  .pv-body {
    white-space: pre-wrap;
    font-family: inherit;
    font-size: 12px;
    margin: 0;
  }
  .attach {
    margin: 8px 0 0;
    font-size: 11px;
    color: var(--text-muted);
  }
  .notice {
    padding: 8px 10px;
    border-radius: 8px;
    font-size: 12px;
    border: 1px solid var(--border);
  }
  .notice.error {
    border-color: color-mix(in srgb, var(--danger) 40%, var(--border));
    background: color-mix(in srgb, var(--danger) 10%, var(--surface));
    color: var(--danger);
  }
  .notice.ok {
    border-color: color-mix(in srgb, var(--success) 35%, var(--border));
    background: color-mix(in srgb, var(--success) 10%, var(--surface));
    color: var(--success);
  }
  .done {
    display: flex;
    flex-direction: column;
    gap: 12px;
    align-items: flex-start;
  }
  .foot {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
    border-top: 1px solid var(--border);
    padding-top: 12px;
  }
</style>
