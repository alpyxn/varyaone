<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { Mail, ShieldCheck, Loader } from '@lucide/svelte';
  import { api, APIRequestError, type Session } from '$lib/api';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import * as Field from '$lib/components/ui/field';
  import {
    getEmailSettings,
    saveEmailSettings,
    testEmailSettings,
    type SMTPTestResult
  } from '$lib/features/settings/email';

  type Provider = {
    id: string;
    label: string;
    host: string;
    port: number;
    security: 'TLS' | 'STARTTLS';
    note?: string;
  };

  // Hazır sağlayıcılar — kullanıcı sunucu/port/güvenlik bilgisiyle uğraşmasın.
  const PROVIDERS: Provider[] = [
    {
      id: 'gmail',
      label: 'Gmail / Google Workspace',
      host: 'smtp.gmail.com',
      port: 587,
      security: 'STARTTLS',
      note: '2 adımlı doğrulama açıkken "Uygulama Şifresi" oluşturup buraya girin.'
    },
    {
      id: 'microsoft',
      label: 'Outlook / Microsoft 365',
      host: 'smtp.office365.com',
      port: 587,
      security: 'STARTTLS'
    },
    {
      id: 'yandex_tr',
      label: 'Yandex Türkiye (yandex.com.tr)',
      host: 'smtp.yandex.com.tr',
      port: 465,
      security: 'TLS',
      note: 'Yandex ayarlarından "SMTP ile erişim" izni açık olmalı.'
    },
    {
      id: 'yandex',
      label: 'Yandex (yandex.com)',
      host: 'smtp.yandex.com',
      port: 465,
      security: 'TLS'
    },
    { id: 'yaani', label: 'Yaani Mail', host: 'smtp.yaani.com', port: 587, security: 'STARTTLS' },
    {
      id: 'kurumsaleposta',
      label: 'Kurumsal E-posta (Turkticaret.net)',
      host: 'mail.kurumsaleposta.com',
      port: 587,
      security: 'STARTTLS'
    },
    { id: 'zoho', label: 'Zoho Mail', host: 'smtp.zoho.com', port: 465, security: 'TLS' },
    {
      id: 'zoho_eu',
      label: 'Zoho Mail (Avrupa)',
      host: 'smtp.zoho.eu',
      port: 465,
      security: 'TLS'
    },
    {
      id: 'brevo',
      label: 'Brevo (Sendinblue)',
      host: 'smtp-relay.brevo.com',
      port: 587,
      security: 'STARTTLS'
    },
    {
      id: 'sendgrid',
      label: 'SendGrid',
      host: 'smtp.sendgrid.net',
      port: 587,
      security: 'STARTTLS',
      note: 'Kullanıcı adı olarak "apikey" yazın, şifre olarak API anahtarınızı girin.'
    },
    {
      id: 'mailjet',
      label: 'Mailjet',
      host: 'in-v3.mailjet.com',
      port: 587,
      security: 'STARTTLS',
      note: 'Kullanıcı adı = API Key, şifre = Secret Key.'
    },
    { id: 'mailgun', label: 'Mailgun', host: 'smtp.mailgun.org', port: 587, security: 'STARTTLS' },
    {
      id: 'postmark',
      label: 'Postmark',
      host: 'smtp.postmarkapp.com',
      port: 587,
      security: 'STARTTLS',
      note: 'Kullanıcı adı ve şifre olarak Server API Token kullanılır.'
    },
    {
      id: 'ses_frankfurt',
      label: 'Amazon SES — Frankfurt',
      host: 'email-smtp.eu-central-1.amazonaws.com',
      port: 587,
      security: 'STARTTLS'
    },
    {
      id: 'ses_ireland',
      label: 'Amazon SES — İrlanda',
      host: 'email-smtp.eu-west-1.amazonaws.com',
      port: 587,
      security: 'STARTTLS'
    },
    { id: 'ionos', label: 'IONOS', host: 'smtp.ionos.com', port: 587, security: 'STARTTLS' },
    {
      id: 'godaddy',
      label: 'GoDaddy',
      host: 'smtpout.secureserver.net',
      port: 465,
      security: 'TLS'
    },
    { id: 'hostinger', label: 'Hostinger', host: 'smtp.hostinger.com', port: 465, security: 'TLS' },
    { id: 'natro', label: 'Natro', host: 'mail.natrohost.com', port: 587, security: 'STARTTLS' },
    { id: 'custom', label: 'Diğer / Özel (elle gir)', host: '', port: 587, security: 'STARTTLS' }
  ];

  let permissions = $state<string[]>([]);
  let denied = $state(false);
  let loading = $state(true);
  let saving = $state(false);
  let testing = $state(false);
  let error = $state('');
  let message = $state('');
  let testResult = $state<SMTPTestResult | null>(null);

  let configured = $state(false);
  let version = $state(0);
  let hasPassword = $state(false);

  let providerId = $state('gmail');
  let form = $state({
    host: '',
    port: 587,
    security_mode: 'STARTTLS' as 'TLS' | 'STARTTLS',
    username: '',
    password: '',
    from_email: '',
    from_name: ''
  });

  const provider = $derived(PROVIDERS.find((p) => p.id === providerId) ?? PROVIDERS[0]);
  const isCustom = $derived(providerId === 'custom');
  const canTest = $derived(permissions.includes('settings.email.test'));

  function applyProvider(id: string) {
    providerId = id;
    const p = PROVIDERS.find((x) => x.id === id);
    if (p && id !== 'custom') {
      form.host = p.host;
      form.port = p.port;
      form.security_mode = p.security;
    }
  }

  async function load() {
    try {
      const session = await api<Session>('/session');
      permissions = session.permissions ?? [];
      denied = !permissions.includes('settings.email.manage');
      if (denied) return;
      try {
        const s = await getEmailSettings();
        configured = true;
        version = s.version;
        hasPassword = s.has_password;
        form = {
          host: s.host,
          port: s.port,
          security_mode: s.security_mode,
          username: s.username,
          password: '',
          from_email: s.from_email,
          from_name: s.from_name
        };
        const match = PROVIDERS.find((p) => p.id !== 'custom' && p.host === s.host);
        providerId = match ? match.id : 'custom';
      } catch (cause) {
        if (!(cause instanceof APIRequestError && cause.status === 404)) throw cause;
        applyProvider('gmail');
      }
    } catch {
      await goto('/giris');
    } finally {
      loading = false;
    }
  }

  async function save(event: SubmitEvent) {
    event.preventDefault();
    if (saving) return;
    saving = true;
    error = '';
    message = '';
    testResult = null;
    try {
      const payload = {
        host: form.host.trim(),
        port: Number(form.port),
        security_mode: form.security_mode,
        username: form.username.trim(),
        from_email: form.from_email.trim(),
        from_name: form.from_name.trim(),
        connect_timeout_seconds: 10,
        ...(form.password ? { password: form.password } : {})
      };
      const s = await saveEmailSettings(version, payload);
      configured = true;
      version = s.version;
      hasPassword = s.has_password;
      form.password = '';
      message = 'E-posta ayarları kaydedildi.';
    } catch (cause) {
      error = cause instanceof APIRequestError ? cause.message : 'Ayarlar kaydedilemedi.';
    } finally {
      saving = false;
    }
  }

  async function runTest() {
    if (testing) return;
    testing = true;
    error = '';
    message = '';
    testResult = null;
    try {
      testResult = await testEmailSettings();
    } catch (cause) {
      error = cause instanceof APIRequestError ? cause.message : 'Bağlantı testi başarısız.';
    } finally {
      testing = false;
    }
  }

  onMount(() => void load());
</script>

<svelte:head><title>E-posta Ayarları · Varya One</title></svelte:head>

{#if denied}
  <section class="card">E-posta ayarlarını yönetme yetkiniz yok.</section>
{:else}
  <header class="page-header">
    <div>
      <h1>E-posta Ayarları</h1>
    </div>
  </header>

  {#if message}<p class="notice success">{message}</p>{/if}
  {#if error}<p class="notice error">{error}</p>{/if}

  {#if loading}
    <section class="card">Yükleniyor…</section>
  {:else}
    <section class="card">
      <div class="status-row">
        <span class="badge" class:on={configured}>
          <Mail size={13} aria-hidden="true" />
          {configured ? 'Yapılandırıldı' : 'Henüz yapılandırılmadı'}
        </span>
        {#if hasPassword}<span class="hint">Kayıtlı şifre var</span>{/if}
      </div>

      <form onsubmit={save}>
        <div class="form-grid">
          <Field.Field class="full">
            <Field.FieldLabel for="e-provider">Sağlayıcı</Field.FieldLabel>
            <select
              id="e-provider"
              class="select"
              value={providerId}
              onchange={(e) => applyProvider(e.currentTarget.value)}
            >
              {#each PROVIDERS as p}<option value={p.id}>{p.label}</option>{/each}
            </select>
            {#if !isCustom}
              <Field.FieldDescription>
                Sunucu: <code>{provider.host}</code> · Port {provider.port} · {provider.security}
              </Field.FieldDescription>
            {/if}
            {#if provider.note}
              <Field.FieldDescription>{provider.note}</Field.FieldDescription>
            {/if}
          </Field.Field>

          {#if isCustom}
            <Field.Field>
              <Field.FieldLabel for="e-host">Sunucu (host)</Field.FieldLabel>
              <Input id="e-host" bind:value={form.host} placeholder="smtp.firma.com" required />
            </Field.Field>
            <Field.Field>
              <Field.FieldLabel for="e-port">Port</Field.FieldLabel>
              <Input
                id="e-port"
                type="number"
                min="1"
                max="65535"
                bind:value={form.port}
                required
              />
            </Field.Field>
            <Field.Field>
              <Field.FieldLabel for="e-sec">Güvenlik</Field.FieldLabel>
              <select id="e-sec" bind:value={form.security_mode} class="select">
                <option value="STARTTLS">STARTTLS (genelde 587)</option>
                <option value="TLS">TLS / SSL (genelde 465)</option>
              </select>
            </Field.Field>
          {/if}

          <Field.Field>
            <Field.FieldLabel for="e-user">Kullanıcı adı</Field.FieldLabel>
            <Input
              id="e-user"
              bind:value={form.username}
              autocomplete="off"
              placeholder="genelde e-posta adresiniz"
            />
          </Field.Field>
          <Field.Field>
            <Field.FieldLabel for="e-pass">Şifre</Field.FieldLabel>
            <Input
              id="e-pass"
              type="password"
              bind:value={form.password}
              autocomplete="new-password"
              placeholder={hasPassword ? '•••••••• (değiştirmek için yazın)' : ''}
            />
          </Field.Field>
          <Field.Field>
            <Field.FieldLabel for="e-from">Gönderen e-posta</Field.FieldLabel>
            <Input
              id="e-from"
              type="email"
              bind:value={form.from_email}
              placeholder="bordro@firma.com"
              required
            />
          </Field.Field>
          <Field.Field>
            <Field.FieldLabel for="e-fromname">Gönderen adı</Field.FieldLabel>
            <Input id="e-fromname" bind:value={form.from_name} placeholder="Firma İK" required />
          </Field.Field>

          <div class="actions full">
            <Button type="submit" disabled={saving}>{saving ? 'Kaydediliyor…' : 'Kaydet'}</Button>
            {#if configured && canTest}
              <Button type="button" variant="outline" onclick={runTest} disabled={testing}>
                {#if testing}<Loader size={14} aria-hidden="true" /> Test ediliyor…
                {:else}<ShieldCheck size={14} aria-hidden="true" /> Bağlantıyı test et{/if}
              </Button>
            {/if}
          </div>
        </div>
      </form>

      {#if testResult}
        <div class="test-result" class:ok={testResult.connected}>
          <strong>{testResult.connected ? 'Bağlantı başarılı' : 'Bağlantı kurulamadı'}</strong>
          <ul>
            <li>Sunucuya bağlantı: {testResult.connected ? 'evet' : 'hayır'}</li>
            <li>Kimlik doğrulama: {testResult.authenticated ? 'başarılı' : '—'}</li>
            <li>Güvenlik: {testResult.security_mode}</li>
          </ul>
        </div>
      {/if}
    </section>
  {/if}
{/if}

<style>
  .page-header {
    margin-bottom: 14px;
  }
  .page-header h1 {
    margin: 0;
    font-size: 19px;
  }
  .page-header p {
    margin: 4px 0 0;
    color: var(--text-muted);
    font-size: 13px;
    max-width: 62ch;
  }
  .card {
    padding: 16px;
  }
  .status-row {
    display: flex;
    align-items: center;
    gap: 10px;
    margin-bottom: 14px;
    flex-wrap: wrap;
  }
  .badge {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    font-size: 12px;
    font-weight: 650;
    padding: 3px 8px;
    border-radius: 999px;
    border: 1px solid var(--border);
    color: var(--text-muted);
  }
  .badge.on {
    color: var(--success, #15803d);
    border-color: color-mix(in srgb, currentColor 40%, transparent);
  }
  .hint {
    font-size: 12px;
    color: var(--text-muted);
  }
  .form-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 240px), 1fr));
    gap: 12px 14px;
  }
  .form-grid > :global(*) {
    min-width: 0;
  }
  .form-grid .full {
    grid-column: 1 / -1;
  }
  .form-grid :global(input),
  .select {
    width: 100%;
    box-sizing: border-box;
  }
  .actions {
    display: flex;
    gap: 8px;
    flex-wrap: wrap;
  }
  .select {
    height: 34px;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    background: var(--surface);
    color: var(--text);
    padding: 0 8px;
    font-size: 13px;
  }
  code {
    font-size: 12px;
    background: var(--surface-hover, rgba(0, 0, 0, 0.05));
    padding: 1px 4px;
    border-radius: 4px;
  }
  .test-result {
    margin-top: 14px;
    padding: 10px 12px;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    font-size: 13px;
  }
  .test-result.ok {
    border-color: color-mix(in srgb, var(--success, #15803d) 45%, transparent);
  }
  .test-result ul {
    margin: 6px 0 0;
    padding-left: 18px;
    color: var(--text-muted);
  }
</style>
