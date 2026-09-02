<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { ArrowLeft } from '@lucide/svelte';
  import { api, APIRequestError, type Session } from '$lib/api';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import * as Field from '$lib/components/ui/field';
  import { createEmployee } from '$lib/features/hr/api';
  import type { EmployeeInput } from '$lib/features/hr/types';

  const OTHER_TABS = [
    'Kimlik & Banka',
    'İstihdam',
    'Ücret',
    'Belgeler',
    'Zimmet',
    'İzinler',
    'Plan'
  ];

  let denied = $state(false);
  let saving = $state(false);
  let error = $state('');
  let form = $state<EmployeeInput>({
    employee_code: '',
    first_name: '',
    last_name: '',
    status: 'ACTIVE',
    position_title: '',
    work_email: '',
    personal_email: '',
    phone: ''
  });

  async function loadSession() {
    try {
      const session = await api<Session>('/session');
      denied = !(session.permissions ?? []).includes('hr.employee.edit');
    } catch {
      denied = true;
    }
  }

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    if (saving) return;
    if (!form.first_name.trim() || !form.last_name.trim()) {
      error = 'Ad ve soyad zorunludur.';
      return;
    }
    saving = true;
    error = '';
    try {
      const created = await createEmployee(form);
      await goto(`/personel/calisanlar/${created.id}`);
    } catch (cause) {
      error = cause instanceof APIRequestError ? cause.message : 'Çalışan oluşturulamadı.';
      saving = false;
    }
  }

  onMount(loadSession);
</script>

<svelte:head><title>Yeni Çalışan · Varya One</title></svelte:head>

{#if denied}
  <section class="card" role="alert">Çalışan oluşturma yetkiniz yok.</section>
{:else}
  <header class="page-header">
    <div>
      <a class="back" href="/personel/calisanlar"
        ><ArrowLeft size={13} aria-hidden="true" />Çalışanlar</a
      >
      <div class="title-row"><h1>Yeni çalışan</h1></div>
      <p>Kart bilgileriyle başlayın. Kaydedince kartın diğer sekmeleri açılır.</p>
    </div>
  </header>

  <nav class="tabs" aria-label="Çalışan sekmeleri">
    <button class="active" type="button">Kart</button>
    {#each OTHER_TABS as t}
      <button type="button" disabled title="Kaydettikten sonra açılır">{t}</button>
    {/each}
  </nav>

  {#if error}<p class="notice error">{error}</p>{/if}

  <section class="card">
    <div class="card-head"><h2>Kart bilgileri</h2></div>
    <form class="grid-form" onsubmit={submit}>
      <Field.Field
        ><Field.FieldLabel for="cf-first">Ad</Field.FieldLabel><Input
          id="cf-first"
          bind:value={form.first_name}
          required
        /></Field.Field
      >
      <Field.Field
        ><Field.FieldLabel for="cf-last">Soyad</Field.FieldLabel><Input
          id="cf-last"
          bind:value={form.last_name}
          required
        /></Field.Field
      >
      <Field.Field
        ><Field.FieldLabel for="cf-code">Çalışan kodu</Field.FieldLabel><Input
          id="cf-code"
          bind:value={form.employee_code}
          placeholder="Boş bırakırsanız otomatik üretilir"
        /></Field.Field
      >
      <Field.Field
        ><Field.FieldLabel for="cf-pos">Pozisyon</Field.FieldLabel><Input
          id="cf-pos"
          bind:value={form.position_title}
        /></Field.Field
      >
      <Field.Field>
        <Field.FieldLabel for="cf-status">Durum</Field.FieldLabel>
        <select id="cf-status" bind:value={form.status} class="select">
          <option value="ACTIVE">Aktif</option>
          <option value="INACTIVE">Pasif</option>
          <option value="ARCHIVED">Arşivlendi</option>
        </select>
      </Field.Field>
      <Field.Field
        ><Field.FieldLabel for="cf-we">İş e-postası</Field.FieldLabel><Input
          id="cf-we"
          type="email"
          bind:value={form.work_email}
        /></Field.Field
      >
      <Field.Field
        ><Field.FieldLabel for="cf-pe">Kişisel e-posta</Field.FieldLabel><Input
          id="cf-pe"
          type="email"
          bind:value={form.personal_email}
        /></Field.Field
      >
      <Field.Field
        ><Field.FieldLabel for="cf-phone">Telefon</Field.FieldLabel><Input
          id="cf-phone"
          bind:value={form.phone}
        /></Field.Field
      >
      <div class="form-actions">
        <Button type="button" variant="ghost" onclick={() => goto('/personel/calisanlar')}
          >Vazgeç</Button
        >
        <Button type="submit" disabled={saving}
          >{saving ? 'Oluşturuluyor…' : 'Çalışanı oluştur'}</Button
        >
      </div>
    </form>
  </section>
{/if}

<style>
  .card {
    padding: 16px;
    margin-top: 14px;
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
  .title-row {
    display: flex;
    align-items: center;
    gap: 10px;
    flex-wrap: wrap;
  }
  .tabs {
    display: flex;
    gap: 2px;
    flex-wrap: wrap;
    margin-top: 12px;
    border-bottom: 1px solid var(--border);
  }
  .tabs button {
    border: 0;
    background: transparent;
    padding: 9px 14px;
    font-size: 13px;
    cursor: pointer;
    color: var(--text-muted);
    border-bottom: 2px solid transparent;
  }
  .tabs button.active {
    color: var(--text);
    border-bottom-color: var(--primary);
    font-weight: 650;
  }
  .tabs button[disabled] {
    cursor: not-allowed;
    opacity: 0.5;
  }
  .card-head h2 {
    margin: 0 0 14px;
    font-size: 15px;
  }
  .grid-form {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
    gap: 12px;
  }
  .form-actions {
    grid-column: 1 / -1;
    display: flex;
    justify-content: flex-end;
    gap: 8px;
    margin-top: 4px;
  }
  .select {
    height: var(--control-height, 34px);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    background: var(--surface);
    color: var(--text);
    padding: 0 8px;
    font-size: 13px;
    width: 100%;
  }
</style>
