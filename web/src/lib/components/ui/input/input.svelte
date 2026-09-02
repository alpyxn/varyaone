<script lang="ts">
  import type { HTMLInputAttributes } from 'svelte/elements';
  import { cn } from '$lib/utils';
  let {
    class: className,
    value = $bindable(),
    oninput,
    onchange,
    onfocus,
    onblur,
    onkeydown,
    onkeyup,
    ...rest
  }: HTMLInputAttributes = $props();
</script>

<input
  bind:value
  class={cn(
    'h-[var(--control-height)] w-full rounded-[var(--radius-control)] border border-[var(--border-strong)] bg-[var(--surface)] px-2.5 text-sm text-[var(--text)] outline-none placeholder:text-[var(--text-muted)] focus:border-[var(--primary)] focus:ring-2 focus:ring-[var(--focus)] disabled:bg-[var(--surface-muted)]',
    className
  )}
  {...rest}
  {oninput}
  {onchange}
  {onfocus}
  {onblur}
  {onkeydown}
  {onkeyup}
/>
