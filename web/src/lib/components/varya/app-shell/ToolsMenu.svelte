<script lang="ts">
  import { MoreVertical, Moon, Sun, Calculator, CalendarDays } from '@lucide/svelte';
  import { Button } from '$lib/components/ui/button';
  import { themePreference } from '$lib/design/theme.svelte';

  let { onCalculator, onCalendar }: { onCalculator: () => void; onCalendar: () => void } = $props();

  let open = $state(false);
  let wrap = $state<HTMLDivElement>();

  function onWindowPointer(event: PointerEvent) {
    if (open && wrap && !wrap.contains(event.target as Node)) open = false;
  }
  function onWindowKey(event: KeyboardEvent) {
    if (open && event.key === 'Escape') open = false;
  }
  function run(action: () => void) {
    open = false;
    action();
  }
</script>

<svelte:window onpointerdown={onWindowPointer} onkeydown={onWindowKey} />

<div class="tools-menu" bind:this={wrap}>
  <Button
    variant="ghost"
    size="icon"
    aria-label="Araçlar"
    aria-haspopup="menu"
    aria-expanded={open}
    title="Araçlar"
    onclick={() => (open = !open)}><MoreVertical size={18} /></Button
  >
  {#if open}
    <div class="tools-pop" role="menu">
      <button type="button" role="menuitem" class="tools-item" onclick={() => run(onCalculator)}>
        <Calculator size={15} aria-hidden="true" /><span>Hesap makinesi</span>
      </button>
      <button type="button" role="menuitem" class="tools-item" onclick={() => run(onCalendar)}>
        <CalendarDays size={15} aria-hidden="true" /><span>Takvim</span>
      </button>
      <button
        type="button"
        role="menuitem"
        class="tools-item"
        onclick={() => run(() => themePreference.toggle())}
      >
        {#if themePreference.value === 'dark'}<Sun size={15} aria-hidden="true" /><span
            >Açık temaya geç</span
          >{:else}<Moon size={15} aria-hidden="true" /><span>Koyu temaya geç</span>{/if}
      </button>
    </div>
  {/if}
</div>

<style>
  .tools-menu {
    position: relative;
    display: inline-flex;
  }
  .tools-pop {
    position: absolute;
    top: calc(100% + 6px);
    right: 0;
    z-index: 40;
    min-width: 200px;
    max-width: calc(100vw - 24px);
    display: flex;
    flex-direction: column;
    padding: 5px;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-panel);
    background: var(--surface);
    box-shadow: 0 12px 34px rgb(2 6 23 / 18%);
  }
  .tools-item {
    display: flex;
    align-items: center;
    gap: 8px;
    width: 100%;
    min-height: 40px;
    padding: 8px 9px;
    border: 0;
    border-radius: 6px;
    background: transparent;
    color: var(--text);
    font-size: 12px;
    text-align: left;
    cursor: pointer;
  }
  .tools-item span {
    flex: 1;
  }
  .tools-item:hover {
    background: var(--surface-muted);
  }
</style>
