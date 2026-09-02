<script lang="ts">
  import { page } from '$app/state';
  import ReportRunner from '$lib/features/reporting/ReportRunner.svelte';
  import { reportByID } from '$lib/features/reporting/registry';

  const def = $derived(reportByID(page.params.report ?? ''));
</script>

<svelte:head>
  <title>{def ? def.label : 'Rapor bulunamadı'} · Raporlar · Varya One</title>
</svelte:head>

{#if def}
  {#key def.id}
    <ReportRunner {def} />
  {/key}
{:else}
  <section class="missing">
    <h1>Rapor bulunamadı</h1>
    <p><a href="/raporlar">Raporlara dön</a></p>
  </section>
{/if}

<style>
  .missing {
    padding: 48px;
    text-align: center;
    color: var(--text-muted);
  }
</style>
