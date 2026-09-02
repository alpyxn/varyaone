<script lang="ts">
  import { UserRound, LogOut, MessageSquarePlus } from '@lucide/svelte';
  import { Button } from '$lib/components/ui/button';

  let {
    displayName,
    onLogout,
    onFeedback
  }: {
    displayName: string;
    onLogout: () => void | Promise<void>;
    onFeedback?: () => void;
  } = $props();

  let open = $state(false);
  let wrap = $state<HTMLDivElement>();

  function toggle() {
    open = !open;
  }

  function onWindowPointer(event: PointerEvent) {
    if (open && wrap && !wrap.contains(event.target as Node)) open = false;
  }

  function onWindowKey(event: KeyboardEvent) {
    if (open && event.key === 'Escape') open = false;
  }
</script>

<svelte:window onpointerdown={onWindowPointer} onkeydown={onWindowKey} />

<div class="user-menu" bind:this={wrap}>
  <Button
    variant="ghost"
    size="icon"
    aria-label="Hesap menüsü"
    aria-haspopup="menu"
    aria-expanded={open}
    title={displayName}
    onclick={toggle}><UserRound size={17} /></Button
  >
  {#if open}
    <div class="user-menu-pop" role="menu">
      <div class="user-menu-name">
        <UserRound size={14} aria-hidden="true" />
        <span>{displayName}</span>
      </div>
      {#if onFeedback}
        <button
          type="button"
          role="menuitem"
          class="user-menu-item"
          onclick={() => {
            open = false;
            onFeedback?.();
          }}
        >
          <MessageSquarePlus size={14} aria-hidden="true" />
          <span>Geri bildirim gönder</span>
        </button>
      {/if}
      <button
        type="button"
        role="menuitem"
        class="user-menu-item danger"
        onclick={() => void onLogout()}
      >
        <LogOut size={14} aria-hidden="true" />
        <span>Çıkış yap</span>
      </button>
    </div>
  {/if}
</div>

<style>
  .user-menu {
    position: relative;
    display: inline-flex;
  }
  .user-menu-pop {
    position: absolute;
    top: calc(100% + 6px);
    right: 0;
    z-index: 40;
    min-width: 190px;
    display: flex;
    flex-direction: column;
    padding: 5px;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-panel);
    background: var(--surface);
    box-shadow: 0 12px 34px rgb(2 6 23 / 18%);
  }
  .user-menu-name {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 7px 9px;
    color: var(--text);
    font-size: 12px;
    font-weight: 650;
    border-bottom: 1px solid var(--border);
    margin-bottom: 4px;
  }
  .user-menu-name span {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .user-menu-item {
    display: flex;
    align-items: center;
    gap: 8px;
    width: 100%;
    padding: 8px 9px;
    border: 0;
    border-radius: 6px;
    background: transparent;
    color: var(--text);
    font-size: 12px;
    text-align: left;
    cursor: pointer;
  }
  .user-menu-item:hover {
    background: var(--surface-muted);
  }
  .user-menu-item.danger:hover {
    color: var(--danger);
  }
</style>
