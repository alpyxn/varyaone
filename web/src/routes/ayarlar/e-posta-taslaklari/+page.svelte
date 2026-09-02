<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { api, type Session } from '$lib/api';
  import {
    createEmailTemplate,
    listEmailTemplates,
    setEmailTemplateActive,
    updateEmailTemplate
  } from '$lib/features/email/api';
  import {
    SCOPE_LABELS,
    type EmailTemplate,
    type EmailTemplateScope
  } from '$lib/features/email/types';

  let session = $state<Session>();
  let items = $state<EmailTemplate[]>([]);
  let loading = $state(true);
  let saving = $state(false);
  let error = $state('');
  let message = $state('');

  let name = $state('');
  let code = $state('');
  let scope = $state<EmailTemplateScope>('GENERIC');
  let subject = $state('');
  let body = $state('');

  let editing = $state<string | null>(null);
  let editName = $state('');
  let editSubject = $state('');
  let editBody = $state('');

  const canManage = $derived(
    Boolean(session?.permissions.includes('communication.email.template.manage'))
  );
  const active = $derived(items.filter((t) => t.is_active));
  const passive = $derived(items.filter((t) => !t.is_active));

  async function load() {
    try {
      session = await api<Session>('/session');
      await refresh();
    } catch {
      await goto('/giris');
    } finally {
      loading = false;
    }
  }

  async function refresh() {
    items = (await listEmailTemplates(undefined, true)).items;
  }

  async function guard(fn: () => Promise<unknown>, ok: string) {
    if (!canManage || saving) return;
    saving = true;
    error = '';
    message = '';
    try {
      await fn();
      await refresh();
      message = ok;
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'İşlem tamamlanamadı.';
    } finally {
      saving = false;
    }
  }

  function create(event: SubmitEvent) {
    event.preventDefault();
    if (!name.trim() || (!subject.trim() && !body.trim())) return;
    void guard(
      () =>
        createEmailTemplate({
          name: name.trim(),
          code: code.trim(),
          scope,
          subject: subject.trim(),
          body
        }).then(() => {
          name = '';
          code = '';
          subject = '';
          body = '';
        }),
      'Taslak eklendi.'
    );
  }

  function startEdit(item: EmailTemplate) {
    editing = item.id;
    editName = item.name;
    editSubject = item.subject;
    editBody = item.body;
    error = '';
  }

  function saveEdit(item: EmailTemplate) {
    if (!editName.trim()) return;
    void guard(
      () =>
        updateEmailTemplate(item.id, item.version, {
          name: editName.trim(),
          scope: item.scope,
          subject: editSubject.trim(),
          body: editBody
        }).then(() => {
          editing = null;
        }),
      'Taslak güncellendi.'
    );
  }

  function toggle(item: EmailTemplate) {
    void guard(
      () => setEmailTemplateActive(item.id, !item.is_active),
      item.is_active ? 'Taslak pasifleştirildi.' : 'Taslak aktifleştirildi.'
    );
  }

  onMount(() => {
    void load();
  });
</script>

<svelte:head><title>E-posta Taslakları · Varya One</title></svelte:head>

<header class="page-header">
  <div>
    <h1>E-posta taslakları</h1>
    <p>
      Konu ve gövde şablonları. Gövdede <code>{'{{ad_soyad}}'}</code>, <code>{'{{donem}}'}</code>,
      <code>{'{{firma}}'}</code> gibi yer tutucular gönderim sırasında alıcıya göre doldurulur.
    </p>
  </div>
  <div class="page-actions">
    <a class="button secondary" href="/ayarlar/e-posta">SMTP ayarları</a>
  </div>
</header>

{#if message}<div class="notice success" role="status">{message}</div>{/if}
{#if error}<div class="notice error" role="alert">{error}</div>{/if}

{#if loading}
  <div class="card">Taslaklar yükleniyor…</div>
{:else}
  <div class="workspace-grid">
    <form class="card form" onsubmit={create}>
      <h2 class="panel-title">Yeni taslak</h2>
      <label class="field"
        >Taslak adı
        <input bind:value={name} required maxlength="120" disabled={!canManage} />
      </label>
      <label class="field"
        >Kapsam
        <select bind:value={scope} disabled={!canManage}>
          {#each Object.entries(SCOPE_LABELS) as [value, label] (value)}
            <option {value}>{label}</option>
          {/each}
        </select>
      </label>
      <label class="field"
        >Kod (opsiyonel)
        <input
          bind:value={code}
          maxlength="40"
          placeholder="Boş bırakırsanız otomatik üretilir"
          disabled={!canManage}
        />
      </label>
      <label class="field"
        >Konu
        <input bind:value={subject} maxlength="255" disabled={!canManage} />
      </label>
      <label class="field"
        >Gövde
        <textarea bind:value={body} rows="6" disabled={!canManage}></textarea>
      </label>
      <div class="form-actions">
        <button class="button" disabled={!canManage || saving}>Taslak ekle</button>
      </div>
    </form>

    <section class="card">
      <h2 class="panel-title">Taslaklar</h2>
      {#if items.length === 0}
        <p class="hint">Taslak tanımlı değil.</p>
      {:else}
        <div class="stack">
          {#each active as item (item.id)}
            {@render row(item)}
          {/each}
          {#if passive.length}
            <p class="group-label">Pasif taslaklar</p>
            {#each passive as item (item.id)}
              {@render row(item)}
            {/each}
          {/if}
        </div>
      {/if}
    </section>
  </div>
{/if}

{#snippet row(item: EmailTemplate)}
  <div class="list-row" class:passive={!item.is_active}>
    {#if editing === item.id}
      <div class="edit">
        <input bind:value={editName} maxlength="120" placeholder="Ad" />
        <input bind:value={editSubject} maxlength="255" placeholder="Konu" />
        <textarea bind:value={editBody} rows="5" placeholder="Gövde"></textarea>
        <div class="row-actions">
          <button class="link-button" type="button" onclick={() => saveEdit(item)}>Kaydet</button>
          <button class="link-button" type="button" onclick={() => (editing = null)}>Vazgeç</button>
        </div>
      </div>
    {:else}
      <span>
        <strong>{item.name}</strong>
        <small
          >{SCOPE_LABELS[item.scope]} · {item.code}{item.is_system ? ' · varsayılan' : ''}</small
        >
        <small class="subj">{item.subject || '(konu yok)'}</small>
      </span>
      {#if canManage}
        <div class="row-actions">
          {#if item.is_active}
            <button class="link-button" type="button" onclick={() => startEdit(item)}
              >Düzenle</button
            >
          {/if}
          <button class="link-button" type="button" disabled={saving} onclick={() => toggle(item)}
            >{item.is_active ? 'Pasifleştir' : 'Aktifleştir'}</button
          >
        </div>
      {/if}
    {/if}
  </div>
{/snippet}

<style>
  .group-label {
    margin: 0.75rem 0 0;
    font-size: 11px;
    font-weight: 700;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: var(--text-muted);
  }
  .list-row {
    align-items: flex-start;
  }
  .list-row.passive {
    opacity: 0.55;
  }
  .subj {
    font-style: italic;
  }
  .row-actions {
    display: flex;
    gap: 0.75rem;
    flex-shrink: 0;
  }
  .edit {
    display: grid;
    gap: 4px;
    flex: 1;
  }
  textarea {
    font: inherit;
    resize: vertical;
  }
</style>
