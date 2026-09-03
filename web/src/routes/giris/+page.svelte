<script lang="ts">
  import { goto } from '$app/navigation';
  import { Dialog } from 'bits-ui';
  import { KeyRound, LogIn, ShieldCheck, X } from '@lucide/svelte';
  import { api, type APIError, type Session } from '$lib/api';
  import { Button } from '$lib/components/ui/button';
  import AuthShell from '$lib/components/varya/auth/AuthShell.svelte';

  let email = $state('');
  let password = $state('');
  let totpCode = $state('');
  let busy = $state(false);
  let message = $state('');
  // The code is asked for only after the password verified and the server
  // answered TOTP_REQUIRED, so the form never shows a field the account
  // does not use.
  let totpOpen = $state(false);
  let totpMessage = $state('');

  async function login(code?: string) {
    await api<Session>('/auth/login', {
      method: 'POST',
      body: JSON.stringify({ email, password, totp_code: code || undefined })
    });
    await goto('/');
    location.reload();
  }

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    busy = true;
    message = '';
    try {
      await login();
    } catch (error) {
      const failure = error as APIError;
      if (failure.code === 'TOTP_REQUIRED') {
        totpCode = '';
        totpMessage = '';
        totpOpen = true;
      } else {
        message = failure.message || 'Giriş yapılamadı.';
      }
    } finally {
      busy = false;
    }
  }

  async function submitCode(event: SubmitEvent) {
    event.preventDefault();
    if (!totpCode.trim()) return;
    busy = true;
    totpMessage = '';
    try {
      await login(totpCode.trim());
    } catch (error) {
      const failure = error as APIError;
      totpMessage = failure.message || 'Doğrulama kodu kabul edilmedi.';
      totpCode = '';
    } finally {
      busy = false;
    }
  }
</script>

<svelte:head><title>Giriş · Varya One</title></svelte:head>

<AuthShell>
  <div class="auth-view">
    <span class="auth-eyebrow"><ShieldCheck size={14} aria-hidden="true" /> Güvenli oturum</span>
    <h1>Tekrar hoş geldiniz</h1>
    <p class="auth-lead">Şirket yetkilerinize güvenli sunucu oturumuyla erişin.</p>

    {#if message}<div class="auth-alert" role="alert">{message}</div>{/if}

    <form class="auth-form" aria-label="Giriş formu" onsubmit={submit}>
      <label class="field">
        E-posta
        <input
          bind:value={email}
          type="email"
          required
          autocomplete="username"
          placeholder="ornek@firma.com"
        />
      </label>
      <label class="field">
        Parola
        <input
          bind:value={password}
          type="password"
          required
          autocomplete="current-password"
          placeholder="••••••••••••"
        />
      </label>
      <button class="auth-submit block" type="submit" disabled={busy}>
        <LogIn size={16} aria-hidden="true" />
        {busy ? 'Kontrol ediliyor…' : 'Giriş yap'}
      </button>
    </form>
  </div>
</AuthShell>

<Dialog.Root bind:open={totpOpen} onOpenChange={(next) => !next && !busy && (totpCode = '')}>
  <Dialog.Portal>
    <Dialog.Overlay class="dialog-overlay" />
    <Dialog.Content class="totp-dialog" aria-describedby="totp-dialog-description">
      <div class="dialog-heading">
        <div>
          <Dialog.Title>İki adımlı doğrulama</Dialog.Title>
          <Dialog.Description id="totp-dialog-description">
            Authenticator uygulamanızdaki 6 haneli kodu girin. Uygulamaya erişemiyorsanız kurtarma
            kodunuzu kullanın.
          </Dialog.Description>
        </div>
        <Dialog.Close class="totp-close" aria-label="Kapat" disabled={busy}>
          <X size={17} />
        </Dialog.Close>
      </div>

      <form onsubmit={submitCode}>
        <label class="totp-label" for="login-totp-code">Doğrulama kodu</label>
        <!-- svelte-ignore a11y_autofocus -->
        <input
          id="login-totp-code"
          class="totp-input"
          bind:value={totpCode}
          autocomplete="one-time-code"
          inputmode="text"
          maxlength="32"
          autofocus
          disabled={busy}
          placeholder="6 haneli kod veya kurtarma kodu"
          aria-invalid={Boolean(totpMessage)}
        />
        {#if totpMessage}<p class="totp-error" role="alert">{totpMessage}</p>{/if}
        <div class="dialog-actions">
          <Dialog.Close type="button" class="totp-cancel" disabled={busy}>Vazgeç</Dialog.Close>
          <Button type="submit" disabled={busy || !totpCode.trim()}>
            <KeyRound size={14} />
            {busy ? 'Doğrulanıyor…' : 'Doğrula'}
          </Button>
        </div>
      </form>
    </Dialog.Content>
  </Dialog.Portal>
</Dialog.Root>

<style>
  :global(.totp-dialog) {
    position: fixed;
    z-index: 61;
    top: 50%;
    left: 50%;
    width: min(420px, calc(100vw - 32px));
    transform: translate(-50%, -50%);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-panel);
    background: var(--surface);
    box-shadow: 0 20px 60px rgb(10 30 27 / 22%);
    padding: 18px;
  }
  .dialog-heading,
  .dialog-actions {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
  }
  .dialog-heading {
    align-items: flex-start;
    margin-bottom: 18px;
  }
  .dialog-heading :global(h2) {
    margin: 0;
    font-size: 16px;
  }
  .dialog-heading :global([data-dialog-description]) {
    display: block;
    margin-top: 4px;
    color: var(--text-muted);
    font-size: 12px;
  }
  :global(.totp-close) {
    display: inline-grid;
    place-items: center;
    width: 28px;
    height: 28px;
    border: 0;
    border-radius: var(--radius-control);
    background: transparent;
    color: var(--text-muted);
  }
  :global(.totp-close:hover) {
    background: var(--surface-muted);
    color: var(--text);
  }
  :global(.totp-cancel) {
    display: inline-flex;
    height: var(--control-height);
    align-items: center;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    background: transparent;
    color: var(--text);
    padding: 0 12px;
    font-size: 12px;
  }
  :global(.totp-cancel:hover) {
    background: var(--surface-muted);
  }
  .totp-label {
    display: block;
    margin-bottom: 5px;
    color: var(--text-subtle);
    font-size: 12px;
    font-weight: 650;
  }
  .totp-input {
    width: 100%;
    height: var(--control-height);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    background: var(--surface);
    color: var(--text);
    padding: 0 12px;
    font-size: 14px;
    letter-spacing: 0.08em;
  }
  .totp-error {
    margin: 6px 0 0;
    color: var(--danger);
    font-size: 12px;
  }
  .dialog-actions {
    justify-content: flex-end;
    margin-top: 20px;
  }
</style>
