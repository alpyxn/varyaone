<script lang="ts">
  import { onMount } from 'svelte';
  import { ArrowLeft, LoaderCircle, Plus } from '@lucide/svelte';
  import { api, APIRequestError, type Session } from '$lib/api';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { createScript, listScripts, updateScript } from '$lib/features/crm/api';
  import type { CallScript } from '$lib/features/crm/types';

  let permissions = $state<string[]>([]);
  let denied = $state(false);
  let loading = $state(true);
  let error = $state('');
  let message = $state('');
  let scripts = $state<CallScript[]>([]);
  let editingID = $state<string | null>(null);
  let saving = $state(false);
  let form = $state({ name: '', body: '', is_active: true });

  const canManage = $derived(permissions.includes('crm.manage'));

  async function load() {
    loading = true;
    try {
      scripts = (await listScripts()).items;
    } catch (cause) {
      error = cause instanceof APIRequestError ? cause.message : 'Metinler yüklenemedi.';
    } finally {
      loading = false;
    }
  }

  function edit(script: CallScript | null) {
    editingID = script?.id ?? 'new';
    form = script
      ? { name: script.name, body: script.body, is_active: script.is_active }
      : {
          name: '',
          body: 'Merhaba, ben [adınız], [firmanız]dan arıyorum.\n\nSize kısaca … hakkında bilgi vermek istiyorum. Uygun musunuz?\n\nİtirazlar:\n- "İlgilenmiyoruz": …\n- "Şu an meşgulüm": Ne zaman tekrar arayayım?',
          is_active: true
        };
    error = '';
  }

  async function save(event: SubmitEvent) {
    event.preventDefault();
    if (saving || !editingID) return;
    if (!form.name.trim()) {
      error = 'Metin adı girin.';
      return;
    }
    saving = true;
    error = '';
    try {
      if (editingID === 'new') {
        await createScript({ name: form.name.trim(), body: form.body });
      } else {
        const current = scripts.find((s) => s.id === editingID);
        await updateScript(editingID, current?.version ?? 1, { ...form, name: form.name.trim() });
      }
      editingID = null;
      message = 'Metin kaydedildi.';
      await load();
    } catch (cause) {
      error = cause instanceof APIRequestError ? cause.message : 'Metin kaydedilemedi.';
    } finally {
      saving = false;
    }
  }

  onMount(async () => {
    try {
      const session = await api<Session>('/session');
      permissions = session.permissions ?? [];
      denied = !permissions.includes('crm.read');
    } catch {
      denied = true;
    }
    if (denied) {
      loading = false;
      return;
    }
    await load();
  });
</script>

<svelte:head><title>Konuşma Metinleri · Varya One</title></svelte:head>

<header class="page-header">
  <div>
    <a class="back" href="/crm/listeler"
      ><ArrowLeft size={14} aria-hidden="true" /> Arama listeleri</a
    >
    <h1>Konuşma Metinleri</h1>
    <p class="meta">Arama ekranında yan panelde görünen konuşma ve itiraz cevapları.</p>
  </div>
  {#if canManage && !editingID}
    <Button onclick={() => edit(null)}>
      <Plus size={14} data-icon="inline-start" aria-hidden="true" />Yeni Metin
    </Button>
  {/if}
</header>

{#if denied}
  <section class="panel error-panel" role="alert">
    <strong>Erişim yok.</strong>
    <span>CRM yetkiniz bulunmuyor.</span>
  </section>
{:else}
  {#if error}<p class="notice error" role="alert">{error}</p>{/if}
  {#if message}<p class="notice success" role="status">{message}</p>{/if}

  {#if editingID}
    <section class="panel">
      <form class="editor" onsubmit={save}>
        <Input
          bind:value={form.name}
          maxlength={120}
          placeholder="Metin adı"
          aria-label="Metin adı"
        />
        <textarea
          class="textarea"
          rows="16"
          maxlength={20000}
          bind:value={form.body}
          aria-label="Metin"
        ></textarea>
        {#if editingID !== 'new'}
          <label class="check">
            <input type="checkbox" bind:checked={form.is_active} /> Kullanımda
          </label>
        {/if}
        <div class="actions">
          <Button variant="ghost" type="button" onclick={() => (editingID = null)}>Vazgeç</Button>
          <Button type="submit" disabled={saving}>
            {#if saving}<LoaderCircle data-icon="inline-start" aria-hidden="true" />{/if}Kaydet
          </Button>
        </div>
      </form>
    </section>
  {/if}

  {#if loading}
    <p class="state" role="status">Yükleniyor…</p>
  {:else if !scripts.length}
    <p class="state">Henüz konuşma metni yok.</p>
  {:else}
    <div class="cards">
      {#each scripts as script (script.id)}
        <article class="panel card" class:inactive={!script.is_active}>
          <div class="card-head">
            <h2>{script.name}</h2>
            {#if !script.is_active}<span class="muted">Kullanım dışı</span>{/if}
            {#if canManage}
              <Button variant="ghost" size="sm" onclick={() => edit(script)}>Düzenle</Button>
            {/if}
          </div>
          <div class="body">{script.body}</div>
        </article>
      {/each}
    </div>
  {/if}
{/if}

<style>
  .page-header {
    display: flex;
    justify-content: space-between;
    align-items: flex-start;
    gap: 12px;
    flex-wrap: wrap;
  }
  .back {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    color: var(--text-muted);
    font-size: 12px;
    text-decoration: none;
  }
  .meta {
    margin: 4px 0 0;
    color: var(--text-muted);
    font-size: 12px;
  }
  .panel {
    margin-top: 16px;
    padding: 14px 16px;
  }
  .editor {
    display: grid;
    gap: 10px;
  }
  .textarea {
    width: 100%;
    box-sizing: border-box;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    background: var(--surface);
    color: var(--text);
    padding: 8px 10px;
    font: inherit;
    font-size: 13px;
    line-height: 1.5;
    resize: vertical;
  }
  .check {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 13px;
  }
  .actions {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
  }
  .cards {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(min(100%, 340px), 1fr));
    gap: 0 16px;
  }
  .card-head {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .card-head h2 {
    margin: 0;
    flex: 1;
    font-size: 14px;
  }
  .body {
    margin-top: 8px;
    max-height: 220px;
    overflow-y: auto;
    font-size: 12px;
    white-space: pre-wrap;
    color: var(--text);
  }
  .inactive {
    opacity: 0.6;
  }
  .muted {
    color: var(--text-muted);
    font-size: 12px;
  }
  .state {
    margin: 0;
    padding: 16px 0;
    color: var(--text-muted);
    font-size: 13px;
    text-align: center;
  }
</style>
