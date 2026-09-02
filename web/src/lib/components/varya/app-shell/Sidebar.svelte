<script lang="ts">
  import { Collapsible } from 'bits-ui';
  import { ChevronDown, PanelLeftClose, X } from '@lucide/svelte';
  import { isNavigationActive, visibleNavigation } from '$lib/navigation';
  import { page } from '$app/state';
  import { Button } from '$lib/components/ui/button';
  import Logo from '$lib/components/varya/Logo.svelte';
  let {
    open = $bindable(false),
    permissions = [],
    permissionsReady = false,
    modules = []
  }: {
    open?: boolean;
    permissions?: string[];
    permissionsReady?: boolean;
    modules?: string[];
  } = $props();
  const visibleGroups = $derived(visibleNavigation(permissions, permissionsReady, modules));
</script>

<aside class:mobile-open={open} class="sidebar" aria-label="Ana navigasyon">
  <div class="brand-row">
    <a class="brand" href="/" aria-label="Varya One Ana Sayfa"
      ><span class="brand-mark" aria-hidden="true"><Logo size={20} /></span><span
        ><strong>Varya One</strong></span
      ></a
    ><Button
      class="mobile-close"
      variant="ghost"
      size="icon"
      aria-label="Menüyü kapat"
      onclick={() => (open = false)}><X size={18} /></Button
    >
  </div>
  <nav class="nav" aria-label="ERP modülleri">
    {#each visibleGroups as group}
      {@const Icon = group.icon}
      {#if group.href}
        <a
          class:active={isNavigationActive(page.url.pathname, group.href)}
          class="nav-root"
          href={group.href}
          onclick={() => (open = false)}><Icon size={17} /><span>{group.label}</span></a
        >
      {:else if group.children}
        {@const expanded = group.children.some((child) =>
          isNavigationActive(page.url.pathname, child.href)
        )}
        <Collapsible.Root open={expanded}>
          <Collapsible.Trigger class="nav-root nav-trigger"
            ><Icon size={17} /><span>{group.label}</span><ChevronDown
              class="chevron"
              size={14}
            /></Collapsible.Trigger
          >
          <Collapsible.Content class="nav-children">
            {#each group.children as child}
              {#if child.href}<a
                  class:active={isNavigationActive(page.url.pathname, child.href)}
                  href={child.href}
                  onclick={() => (open = false)}>{child.label}</a
                >
              {:else}<span
                  class="nav-child-disabled"
                  aria-disabled="true"
                  title={child.detail ?? 'İlgili fazda etkinleşecek'}
                  ><span>{child.label}</span><small>Yakında</small></span
                >{/if}
            {/each}
          </Collapsible.Content>
        </Collapsible.Root>
      {:else}<span class="nav-root disabled" aria-disabled="true"
          ><Icon size={17} /><span>{group.label}</span></span
        >{/if}
    {/each}
  </nav>
  <div class="sidebar-meta"><PanelLeftClose size={14} /><span>Kaosa biçim vermek</span></div>
</aside>
{#if open}<button class="sidebar-scrim" aria-label="Menüyü kapat" onclick={() => (open = false)}
  ></button>{/if}
