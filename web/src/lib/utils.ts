import { clsx, type ClassValue } from 'clsx';
import { twMerge } from 'tailwind-merge';
import type { Snippet } from 'svelte';

// Shared compatibility types for shadcn-svelte source components. The repo
// uses Svelte 5 runes and keeps the element ref intentionally broad so the
// same helper works for div, fieldset, table and native control wrappers.
export type WithElementRef<T> = T & { ref?: HTMLElement | null; children?: Snippet };
export type WithoutChildren<T> = Omit<T, 'children'>;

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}
