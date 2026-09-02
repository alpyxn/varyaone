<script lang="ts">
  import type { HTMLButtonAttributes } from 'svelte/elements';
  import { cn } from '$lib/utils';

  let {
    class: className,
    variant = 'default',
    size = 'default',
    children,
    onclick,
    ...rest
  }: HTMLButtonAttributes & {
    variant?: 'default' | 'secondary' | 'ghost' | 'outline' | 'danger';
    size?: 'default' | 'sm' | 'icon';
  } = $props();
</script>

<button
  class={cn(
    'inline-flex shrink-0 items-center justify-center gap-1.5 rounded-[var(--radius-control)] border text-sm font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--focus)] disabled:pointer-events-none disabled:opacity-50',
    variant === 'default' &&
      'border-[var(--primary)] bg-[var(--primary)] text-[var(--primary-foreground)] hover:bg-[var(--primary-hover)]',
    variant === 'secondary' &&
      'border-[var(--border)] bg-[var(--surface)] text-[var(--text)] hover:bg-[var(--surface-muted)]',
    variant === 'outline' &&
      'border-[var(--border-strong)] bg-transparent text-[var(--text)] hover:bg-[var(--surface-muted)]',
    variant === 'ghost' && 'border-transparent bg-transparent hover:bg-[var(--surface-muted)]',
    variant === 'danger' &&
      'border-[var(--danger)] bg-[var(--danger)] text-[var(--danger-foreground)] hover:brightness-90',
    size === 'default' && 'h-[var(--control-height)] px-3',
    size === 'sm' && 'h-7 px-2 text-xs',
    size === 'icon' && 'size-[var(--control-height)] p-0',
    className
  )}
  {...rest}
  {onclick}>{@render children?.()}</button
>
