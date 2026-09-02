<script lang="ts">
  let {
    size = 28,
    variant = 'mark',
    class: className = ''
  }: {
    /** Height of the mark in pixels. */
    size?: number;
    /** `mark` renders only the glyph; `full` adds the "Varya One" wordmark. */
    variant?: 'mark' | 'full';
    class?: string;
  } = $props();
</script>

<span class="varya-logo {className}" class:full={variant === 'full'} style="--logo-size: {size}px">
  <svg
    class="varya-logo-mark"
    viewBox="0 0 220 220"
    xmlns="http://www.w3.org/2000/svg"
    role="img"
    aria-label="Varya One"
  >
    <g fill="#C1272D">
      <rect x="20" y="100" width="30" height="90" rx="3" transform="rotate(-26 35 190)" />
      <rect x="70" y="55" width="30" height="135" rx="3" transform="rotate(-12 85 190)" />
      <rect x="120" y="35" width="30" height="155" rx="3" transform="rotate(-4 135 190)" />
    </g>
    <rect x="170" y="10" width="30" height="180" rx="3" fill="currentColor" />
  </svg>
  {#if variant === 'full'}
    <span class="varya-logo-word"><strong>Varya</strong> One</span>
  {/if}
</span>

<style>
  .varya-logo {
    display: inline-flex;
    align-items: center;
    gap: 0.55em;
    /* Inherit the surrounding text color so the wordmark and the dark bar
       (fill: currentColor) stay legible on both light and dark surfaces. */
    color: inherit;
    line-height: 1;
  }
  .varya-logo-mark {
    display: block;
    width: var(--logo-size);
    height: var(--logo-size);
    flex: none;
  }
  .varya-logo-word {
    font-size: calc(var(--logo-size) * 0.62);
    letter-spacing: -0.02em;
    white-space: nowrap;
  }
  .varya-logo-word strong {
    font-weight: 800;
  }
</style>
