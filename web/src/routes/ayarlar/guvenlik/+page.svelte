<script lang="ts">
  import { goto } from '$app/navigation';
  import { onMount } from 'svelte';
  import QRCode from 'qrcode';
  import { api, type APIError, type Session } from '$lib/api';

  let session = $state<Session | null>(null);
  let message = $state('');
  let messageTone = $state<'success' | 'error'>('success');
  let secret = $state('');
  let uri = $state('');
  let qrDataUrl = $state('');
  let code = $state('');
  let recoveryCodes = $state<string[]>([]);
  let confirming = $state(false);
  let showDisableForm = $state(false);
  let disablePassword = $state('');
  let disabling = $state(false);

  onMount(async () => {
    try {
      session = await api<Session>('/session');
    } catch {
      await goto('/giris');
    }
  });

  async function beginTOTP() {
    message = '';
    try {
      const response = await api<{ secret: string; otpauth_uri: string }>('/security/totp/setup', {
        method: 'POST',
        body: '{}'
      });
      secret = response.secret;
      uri = response.otpauth_uri;
      code = '';
      qrDataUrl = await QRCode.toDataURL(uri, { width: 200, margin: 1 });
    } catch (error) {
      messageTone = 'error';
      message = (error as APIError).message || 'İki adımlı doğrulama kurulumu başlatılamadı.';
    }
  }

  async function confirmTOTP() {
    const value = code.trim();
    if (value.length !== 6) {
      messageTone = 'error';
      message = 'Lütfen authenticator uygulamasındaki 6 haneli kodu girin.';
      return;
    }
    confirming = true;
    message = '';
    try {
      const response = await api<{ recovery_codes: string[] }>('/security/totp/confirm', {
        method: 'POST',
        body: JSON.stringify({ code: value })
      });
      recoveryCodes = response.recovery_codes;
      secret = '';
      uri = '';
      qrDataUrl = '';
      code = '';
      messageTone = 'success';
      message = 'İki adımlı doğrulama etkinleştirildi.';
      if (session) session.user.totp_enabled = true;
    } catch (error) {
      messageTone = 'error';
      message = (error as APIError).message || 'Doğrulama kodu geçersiz.';
    } finally {
      confirming = false;
    }
  }

  async function disableTOTP() {
    if (!disablePassword) {
      messageTone = 'error';
      message = 'Devam etmek için parolanızı girin.';
      return;
    }
    disabling = true;
    message = '';
    try {
      await api<void>('/security/totp/disable', {
        method: 'POST',
        body: JSON.stringify({ password: disablePassword })
      });
      disablePassword = '';
      showDisableForm = false;
      messageTone = 'success';
      message = 'İki adımlı doğrulama kapatıldı.';
      if (session) session.user.totp_enabled = false;
    } catch (error) {
      messageTone = 'error';
      message = (error as APIError).message || 'İki adımlı doğrulama kapatılamadı.';
    } finally {
      disabling = false;
    }
  }
</script>

<svelte:head><title>Güvenlik · Varya One</title></svelte:head>

<header class="page-header">
  <div>
    <h1>Oturum ve entegrasyon güvenliği</h1>
  </div>
</header>
{#if message}<div class="notice {messageTone}" role={messageTone === 'error' ? 'alert' : 'status'}>
    {message}
  </div>{/if}

<section class="panel-grid">
  <article class="card form">
    <h2 class="panel-title">İki adımlı doğrulama</h2>
    {#if session?.user.totp_enabled && !secret && recoveryCodes.length === 0}
      <div class="notice success">İki adımlı doğrulama hesabınızda etkin.</div>
      {#if !showDisableForm}
        <button class="button secondary" type="button" onclick={() => (showDisableForm = true)}
          >İki adımlı doğrulamayı kapat</button
        >
      {:else}
        <p class="lead">Kapatmak için parolanızı doğrulayın.</p>
        <label class="field"
          >Parola<input
            bind:value={disablePassword}
            type="password"
            autocomplete="current-password"
          /></label
        >
        <div class="actions-row">
          <button class="button danger" type="button" disabled={disabling} onclick={disableTOTP}
            >{disabling ? 'Kapatılıyor…' : 'Kapat'}</button
          >
          <button
            class="button secondary"
            type="button"
            onclick={() => {
              showDisableForm = false;
              disablePassword = '';
            }}>Vazgeç</button
          >
        </div>
      {/if}
    {:else if !secret && recoveryCodes.length === 0}
      <p class="lead">
        Google Authenticator, Microsoft Authenticator veya benzeri bir uygulamayla oturum açarken
        ikinci bir doğrulama adımı isteyin.
      </p>
      <button class="button" type="button" onclick={beginTOTP}>TOTP kurulumunu başlat</button>
    {:else if secret}
      <ol class="totp-steps">
        <li>
          <p class="lead">Authenticator uygulamanızla bu kodu okutun.</p>
          {#if qrDataUrl}<img
              class="totp-qr"
              src={qrDataUrl}
              alt="TOTP QR kodu"
              width="200"
              height="200"
            />{/if}
          <p class="hint">Kamerayla okutamıyorsanız anahtarı elle girin:</p>
          <code class="secret-value">{secret}</code>
        </li>
        <li>
          <label class="field"
            >Uygulamada görünen 6 haneli kod<input
              bind:value={code}
              inputmode="numeric"
              autocomplete="one-time-code"
              maxlength="6"
              placeholder="000000"
            /></label
          >
          <button class="button" type="button" disabled={confirming} onclick={confirmTOTP}
            >{confirming ? 'Doğrulanıyor…' : 'Doğrula ve etkinleştir'}</button
          >
        </li>
      </ol>
    {:else}
      <div class="notice success">
        <strong>Kurtarma kodlarını şimdi saklayın.</strong> Yeniden gösterilmezler.
      </div>
      <ul class="code-list">
        {#each recoveryCodes as recovery}<li><code>{recovery}</code></li>{/each}
      </ul>
    {/if}
  </article>
</section>

<style>
  .totp-steps {
    display: grid;
    gap: 16px;
    margin: 0;
    padding: 0 0 0 18px;
  }
  .totp-steps li {
    display: grid;
    gap: 8px;
  }
  .totp-qr {
    border: 1px solid var(--border);
    border-radius: var(--radius-panel);
    padding: 8px;
    background: #fff;
    width: 160px;
    height: 160px;
  }
  .actions-row {
    display: flex;
    gap: 8px;
    flex-wrap: wrap;
  }
  .button.danger {
    border-color: var(--danger);
    background: var(--danger);
    color: #fff;
  }
</style>
