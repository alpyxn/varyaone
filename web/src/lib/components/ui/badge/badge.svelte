<script lang="ts">
  import type { HTMLAttributes } from 'svelte/elements';
  import { cn } from '$lib/utils';
  let {
    class: className,
    tone = 'neutral',
    children,
    ...rest
  }: HTMLAttributes<HTMLSpanElement> & {
    tone?: 'neutral' | 'success' | 'warning' | 'danger' | 'info';
  } = $props();
</script>

<span
  class={cn(
    'inline-flex min-h-5 items-center rounded-[var(--radius-control)] border px-1.5 text-xs font-semibold',
    tone === 'neutral' &&
      'border-[var(--border)] bg-[var(--surface-muted)] text-[var(--text-subtle)]',
    tone === 'success' && 'badge-success',
    tone === 'warning' && 'badge-warning',
    tone === 'danger' && 'badge-danger',
    tone === 'info' && 'badge-info',
    className
  )}
  {...rest}>{@render children?.()}</span
>

<style>
  .badge-success,
  .badge-warning,
  .badge-danger,
  .badge-info {
    border-color: color-mix(in srgb, var(--badge-color) 32%, var(--border));
    background: color-mix(in srgb, var(--badge-color) 10%, var(--surface));
    color: var(--badge-color);
  }
  .badge-success {
    --badge-color: var(--success);
  }
  .badge-warning {
    --badge-color: var(--warning);
  }
  .badge-danger {
    --badge-color: var(--danger);
  }
  .badge-info {
    --badge-color: var(--info);
  }
</style>
