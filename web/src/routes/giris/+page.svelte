<script lang="ts">
  import { goto } from '$app/navigation';
  import { LogIn, ShieldCheck } from '@lucide/svelte';
  import { api, type APIError, type Session } from '$lib/api';
  import AuthShell from '$lib/components/varya/auth/AuthShell.svelte';

  let email = $state('');
  let password = $state('');
  let totpCode = $state('');
  let busy = $state(false);
  let message = $state('');

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    busy = true;
    message = '';
    try {
      await api<Session>('/auth/login', {
        method: 'POST',
        body: JSON.stringify({ email, password, totp_code: totpCode || undefined })
      });
      await goto('/');
      location.reload();
    } catch (error) {
      message = (error as APIError).message || 'Giriş yapılamadı.';
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
      <label class="field">
        Doğrulama kodu
        <span class="hint"
          >İki adımlı doğrulama etkinse; authenticator koduna erişemiyorsanız kurtarma kodunuzu
          girin</span
        >
        <input
          bind:value={totpCode}
          autocomplete="one-time-code"
          maxlength="32"
          placeholder="6 haneli kod veya kurtarma kodu"
        />
      </label>
      <button class="auth-submit block" type="submit" disabled={busy}>
        <LogIn size={16} aria-hidden="true" />
        {busy ? 'Kontrol ediliyor…' : 'Giriş yap'}
      </button>
    </form>
  </div>
</AuthShell>
