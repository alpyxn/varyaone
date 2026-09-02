<script lang="ts">
  import type { Snippet } from 'svelte';
  export type ToolbarMode = 'list' | 'card' | 'document';
  let {
    title,
    subtitle,
    mode = 'list',
    primary,
    tools,
    status
  }: {
    title: string;
    subtitle?: string;
    mode?: ToolbarMode;
    primary?: Snippet;
    tools?: Snippet;
    status?: Snippet;
  } = $props();
</script>

<header class="document-header" data-mode={mode}>
  <div>
    <h1>{title}</h1>
    {#if subtitle}<p>{subtitle}</p>{/if}
  </div>
  <div class="header-actions">
    {#if status}<div class="toolbar-status">{@render status()}</div>{/if}
    {#if primary}<div class="primary">{@render primary()}</div>{/if}
  </div>
</header>
{#if tools}<div class="document-tools">{@render tools()}</div>{/if}

<style>
  .document-header {
    min-height: 46px;
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 14px;
  }
  .document-header h1 {
    margin: 0;
    font-size: 21px;
    letter-spacing: -0.025em;
  }
  .document-header p {
    margin: 3px 0 0;
    color: var(--text-muted);
    font-size: 12px;
  }
  .document-tools {
    min-height: var(--toolbar-height);
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 7px;
    margin: 2px 0 8px;
    padding: 6px 8px;
    border: 1px solid var(--border);
    border-radius: var(--radius-panel);
    background: var(--surface);
  }
  .primary {
    display: flex;
    flex-wrap: wrap;
    gap: 7px;
  }
  .header-actions,
  .toolbar-status {
    display: flex;
    align-items: center;
    gap: 7px;
  }
  .toolbar-status {
    color: var(--text-muted);
    font-size: 11px;
  }
  .document-header[data-mode='card'] h1,
  .document-header[data-mode='document'] h1 {
    letter-spacing: -0.02em;
  }
  @media (max-width: 640px) {
    .document-header {
      flex-direction: column;
    }
    .document-tools {
      overflow-x: auto;
    }
    .header-actions {
      width: 100%;
      justify-content: space-between;
    }
  }
</style>
