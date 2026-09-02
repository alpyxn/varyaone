<script lang="ts">
  import { Dialog } from 'bits-ui';
  import { Boxes, FileText, Search, UsersRound, X } from '@lucide/svelte';
  import { Input } from '$lib/components/ui/input';
  import { api } from '$lib/api';
  import { matchesSearch } from '$lib/filtering';
  import { navigationSearchItems, type NavigationSearchItem } from '$lib/navigation';
  let {
    open = $bindable(false),
    permissions,
    permissionsReady = false,
    modules
  }: {
    open?: boolean;
    permissions?: readonly string[];
    permissionsReady?: boolean;
    modules?: readonly string[];
  } = $props();
  let query = $state('');
  let selectedIndex = $state(0);
  let records = $state<NavigationSearchItem[]>([]);
  let searchError = $state('');
  let requestSequence = 0;
  let activeRequest: AbortController | undefined;
  const items = $derived(permissionsReady ? navigationSearchItems(permissions, modules) : []);
  const allItems = $derived([...items, ...records]);
  const filtered = $derived(allItems.filter((item) => matchesSearch(item, query)));

  $effect(() => {
    selectedIndex = Math.min(selectedIndex, Math.max(0, filtered.length - 1));
  });

  $effect(() => {
    const search = query.trim();
    activeRequest?.abort();
    if (!open || !permissionsReady || search.length < 2) {
      records = [];
      searchError = '';
      return;
    }
    const request = new AbortController();
    activeRequest = request;
    const sequence = ++requestSequence;
    const timer = setTimeout(async () => {
      try {
        const response = await api<{ items?: Array<Record<string, unknown>> }>(
          `/search?q=${encodeURIComponent(search)}&limit=12`,
          { signal: request.signal }
        );
        if (sequence !== requestSequence || request.signal.aborted) return;
        records = (response.items ?? []).flatMap((item) => {
          const type = String(item.type ?? item.kind ?? 'Kayıt');
          const title = String(item.title ?? item.name ?? '');
          if (!title) return [];
          const normalizedType = type.toLocaleLowerCase('tr-TR');
          const displayType =
            normalizedType === 'party'
              ? 'Cari'
              : normalizedType === 'product'
                ? 'Stok Kartı'
                : normalizedType === 'document'
                  ? 'Belge'
                  : type;
          const icon =
            normalizedType.includes('cari') || normalizedType.includes('party')
              ? UsersRound
              : normalizedType.includes('stok') ||
                  normalizedType.includes('ürün') ||
                  normalizedType.includes('product')
                ? Boxes
                : FileText;
          return [
            {
              type: displayType,
              title,
              detail: String(item.detail ?? item.code ?? ''),
              href: typeof item.href === 'string' ? item.href : undefined,
              icon,
              state: item.href ? 'active' : 'coming'
            } satisfies NavigationSearchItem
          ];
        });
        searchError = '';
      } catch (cause) {
        if (request.signal.aborted || sequence !== requestSequence) return;
        records = [];
        searchError =
          typeof cause === 'object' && cause && 'message' in cause
            ? String(cause.message)
            : 'Kayıt araması kullanılamıyor.';
      }
    }, 220);
    return () => clearTimeout(timer);
  });

  function close() {
    open = false;
    selectedIndex = 0;
    activeRequest?.abort();
  }

  function resultId(index: number) {
    return `global-search-result-${index}`;
  }

  function selectResult(item: NavigationSearchItem) {
    if (item.state !== 'active' || !item.href) return;
    close();
  }

  function handleInputKeydown(event: KeyboardEvent) {
    if (event.key === 'ArrowDown' && filtered.length) {
      event.preventDefault();
      selectedIndex = Math.min(filtered.length - 1, selectedIndex + 1);
    } else if (event.key === 'ArrowUp' && filtered.length) {
      event.preventDefault();
      selectedIndex = Math.max(0, selectedIndex - 1);
    } else if (event.key === 'Enter' && filtered[selectedIndex]) {
      const item = filtered[selectedIndex];
      if (item.state === 'active' && item.href) {
        event.preventDefault();
        selectResult(item);
        window.location.href = item.href;
      }
    }
  }
</script>

<Dialog.Root bind:open>
  <Dialog.Trigger class="search-trigger" aria-label="Global aramayı aç"
    ><Search size={15} aria-hidden="true" /><span>Modül, Cari veya Stok ara…</span><kbd>Ctrl K</kbd
    ></Dialog.Trigger
  >
  <Dialog.Portal
    ><Dialog.Overlay class="dialog-overlay" /><Dialog.Content class="search-dialog">
      <Dialog.Title class="sr-only">Global arama</Dialog.Title><Dialog.Description class="sr-only"
        >Yetkili modül, Cari, Stok Kartı ve belge kayıtlarında arama yapın.</Dialog.Description
      >
      <div class="search-input-row">
        <Search size={18} aria-hidden="true" /><Input
          bind:value={query}
          autofocus
          placeholder="Modül, Cari, Stok Kartı veya belge ara"
          aria-label="Global arama"
          aria-activedescendant={filtered.length ? resultId(selectedIndex) : undefined}
          oninput={() => (selectedIndex = 0)}
          onkeydown={handleInputKeydown}
        /><Dialog.Close class="icon-button" aria-label="Kapat"><X size={18} /></Dialog.Close>
      </div>
      <div class="search-results" role="listbox" aria-label="Arama sonuçları">
        {#if searchError}<p class="search-feedback" role="status">{searchError}</p>{/if}
        {#if filtered.length === 0}<p class="search-empty">
            {query.trim().length < 2
              ? 'Aramak için en az iki karakter yazın.'
              : 'Eşleşen kayıt bulunamadı.'}
          </p>{/if}
        {#each filtered as item, index}{@const Icon =
            item.icon}{#if item.state === 'active' && item.href}<a
              id={resultId(index)}
              role="option"
              aria-selected={selectedIndex === index}
              href={item.href}
              onclick={() => selectResult(item)}
              class:selected={selectedIndex === index}
              ><Icon size={17} aria-hidden="true" /><span
                ><strong>{item.title}</strong><small>{item.type} · {item.detail}</small></span
              ></a
            >{:else}<span
              id={resultId(index)}
              class="disabled"
              role="option"
              aria-disabled="true"
              aria-selected={selectedIndex === index}
              class:selected={selectedIndex === index}
              ><Icon size={17} aria-hidden="true" /><span
                ><strong>{item.title}</strong><small>{item.type} · {item.detail}</small></span
              ></span
            >{/if}{/each}
      </div>
    </Dialog.Content></Dialog.Portal
  >
</Dialog.Root>
