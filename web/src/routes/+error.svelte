<script lang="ts">
  import { page } from '$app/state';
  import { errorMessage } from '$lib/errors';
</script>

<svelte:head><title>Sayfa açılamadı · Varya One</title></svelte:head>
<section class="error-page" role="alert">
  <h1>{page.status === 404 ? 'Sayfa bulunamadı' : 'Sayfa açılamadı'}</h1>
  <p>
    {page.status === 404
      ? 'Bu adres mevcut değil veya sayfa taşınmış olabilir.'
      : errorMessage(page.error, 'Sayfa yüklenirken bir sorun oluştu. Lütfen tekrar deneyin.')}
  </p>
  <a href="/">Ana sayfaya dön</a>
</section>

<style>
  .error-page {
    max-width: 60ch;
    padding: clamp(16px, 6vw, 32px);
  }
  h1 {
    margin: 0;
    font-size: clamp(18px, 5vw, 22px);
    overflow-wrap: anywhere;
  }
  p {
    margin: 12px 0;
    overflow-wrap: anywhere;
  }
  a {
    text-decoration: underline;
  }
</style>
