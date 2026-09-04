<script lang="ts">
  import Logo from '$lib/components/varya/Logo.svelte';

  let {
    title,
    children
  }: {
    /** Modül başlığı, örn. "e-Belge". */
    title: string;
    children?: import('svelte').Snippet;
  } = $props();
</script>

<section class="coming">
  <div class="coming-mark" aria-hidden="true"><Logo size={72} /></div>
  <span class="eyebrow">{title}</span>
  <h1>Üzerinde çalışıyoruz</h1>
  {#if children}
    <div class="coming-body">{@render children()}</div>
  {/if}
</section>

<style>
  .coming {
    display: flex;
    flex-direction: column;
    align-items: center;
    text-align: center;
    gap: 0.75rem;
    max-width: 44rem;
    margin: clamp(2rem, 8vh, 6rem) auto 0;
    padding: 0 1rem;
    /* Başlık ve gövde bunu miras alır; .main bir metin rengi vermiyor, o yüzden
       burada verilmezse koyu temada siyah metin kalır. */
    color: var(--text);
  }
  .coming-mark {
    margin-bottom: 0.5rem;
  }
  .eyebrow {
    text-transform: uppercase;
    letter-spacing: 0.08em;
    font-size: 0.75rem;
    font-weight: 600;
    color: var(--text-muted);
  }
  h1 {
    margin: 0;
    font-size: 1.6rem;
    font-weight: 700;
  }
  .coming-body {
    margin-top: 1.25rem;
    text-align: left;
    line-height: 1.65;
  }
  .coming-body :global(h2) {
    font-size: 1.05rem;
    font-weight: 700;
    margin: 1.75rem 0 0.5rem;
  }
  .coming-body :global(p) {
    margin: 0.5rem 0;
  }
  .coming-body :global(ul) {
    margin: 0.5rem 0;
    padding-left: 1.25rem;
  }
  .coming-body :global(li) {
    margin: 0.2rem 0;
  }
  .coming-body :global(.cta) {
    display: inline-block;
    margin-top: 1.5rem;
    padding: 0.6rem 1.1rem;
    border-radius: 0.5rem;
    background: var(--primary);
    color: var(--primary-foreground);
    font-weight: 600;
    text-decoration: none;
  }
</style>
