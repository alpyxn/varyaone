<script lang="ts">
  import { UploadCloud, FileText, ImageIcon, X } from '@lucide/svelte';

  let {
    files = $bindable<File[]>([]),
    variant = 'document',
    accept,
    maxSizeKB,
    multiple = false,
    disabled = false,
    label,
    hint,
    id,
    ariaLabel,
    onFilesChange
  }: {
    files?: File[];
    variant?: 'document' | 'photo';
    accept?: string;
    maxSizeKB?: number;
    multiple?: boolean;
    disabled?: boolean;
    label?: string;
    hint?: string;
    id?: string;
    ariaLabel?: string;
    onFilesChange?: (files: File[]) => void;
  } = $props();

  let inputElement = $state<HTMLInputElement>();
  let dragging = $state(false);
  let error = $state('');
  let previews = $state<string[]>([]);

  const defaultLabel = $derived(
    label ?? (variant === 'photo' ? 'Fotoğrafı buraya bırakın' : 'Dosyayı buraya bırakın')
  );
  const acceptValue = $derived(accept ?? (variant === 'photo' ? 'image/*' : undefined));

  $effect(() => {
    // Rebuild object-URL previews whenever the selection changes. Reads only
    // `files`/`variant`; the cleanup revokes the URLs this run created.
    const urls =
      variant === 'photo'
        ? files.filter((f) => f.type.startsWith('image/')).map((f) => URL.createObjectURL(f))
        : [];
    previews = urls;
    return () => urls.forEach((url) => URL.revokeObjectURL(url));
  });

  function humanSize(bytes: number) {
    if (bytes < 1024) return `${bytes} B`;
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(0)} KB`;
    return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  }

  function accepts(file: File) {
    if (!acceptValue) return true;
    return acceptValue.split(',').some((rule) => {
      const r = rule.trim();
      if (!r) return false;
      if (r.endsWith('/*')) return file.type.startsWith(r.slice(0, -1));
      if (r.startsWith('.')) return file.name.toLowerCase().endsWith(r.toLowerCase());
      return file.type === r;
    });
  }

  function apply(list: FileList | null) {
    error = '';
    if (!list || !list.length) return;
    const picked = multiple ? Array.from(list) : [list[0]];
    for (const file of picked) {
      if (!accepts(file)) {
        error = `“${file.name}” desteklenmeyen bir dosya türü.`;
        return;
      }
      if (maxSizeKB && file.size > maxSizeKB * 1024) {
        error = `“${file.name}” çok büyük (en fazla ${humanSize(maxSizeKB * 1024)}).`;
        return;
      }
    }
    files = picked;
    onFilesChange?.(picked);
  }

  function remove(index: number) {
    files = files.filter((_, i) => i !== index);
    onFilesChange?.(files);
    if (inputElement) inputElement.value = '';
  }

  function onDrop(event: DragEvent) {
    event.preventDefault();
    dragging = false;
    if (disabled) return;
    apply(event.dataTransfer?.files ?? null);
  }
</script>

<div class="file-drop" class:disabled>
  <button
    type="button"
    class="zone"
    class:dragging
    class:has-files={files.length > 0}
    {disabled}
    aria-label={ariaLabel ?? defaultLabel}
    onclick={() => inputElement?.click()}
    ondragover={(e) => {
      e.preventDefault();
      if (!disabled) dragging = true;
    }}
    ondragleave={() => (dragging = false)}
    ondrop={onDrop}
  >
    {#if variant === 'photo'}
      <ImageIcon size={22} aria-hidden="true" />
    {:else}
      <UploadCloud size={22} aria-hidden="true" />
    {/if}
    <span class="prompt">{defaultLabel}</span>
    <span class="sub">veya <span class="link-text">bilgisayardan seçin</span></span>
    {#if hint}<span class="hint">{hint}</span>{/if}
  </button>

  <input
    bind:this={inputElement}
    {id}
    type="file"
    class="native"
    accept={acceptValue}
    {multiple}
    {disabled}
    onchange={(e) => apply(e.currentTarget.files)}
  />

  {#if error}<p class="error">{error}</p>{/if}

  {#if files.length}
    <ul class="selected">
      {#each files as file, index (file.name + index)}
        <li>
          {#if variant === 'photo' && previews[index]}
            <img src={previews[index]} alt={file.name} />
          {:else}
            <FileText size={16} aria-hidden="true" />
          {/if}
          <span class="name">{file.name}</span>
          <span class="size">{humanSize(file.size)}</span>
          <button type="button" class="remove" aria-label="Kaldır" onclick={() => remove(index)}>
            <X size={14} aria-hidden="true" />
          </button>
        </li>
      {/each}
    </ul>
  {/if}
</div>

<style>
  .file-drop {
    display: flex;
    flex-direction: column;
    gap: 8px;
    min-width: 0;
  }
  .zone {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 4px;
    padding: 18px 14px;
    border: 1.5px dashed var(--border-strong);
    border-radius: var(--radius-card);
    background: var(--surface-muted, var(--surface));
    color: var(--text-muted);
    cursor: pointer;
    text-align: center;
    transition:
      border-color 0.12s ease,
      background 0.12s ease;
  }
  .zone:hover:not(:disabled),
  .zone.dragging {
    border-color: var(--primary);
    background: color-mix(in srgb, var(--primary) 7%, var(--surface));
    color: var(--text);
  }
  .zone:focus-visible {
    outline: 2px solid var(--focus);
    outline-offset: 2px;
  }
  .zone:disabled {
    cursor: not-allowed;
    opacity: 0.6;
  }
  .prompt {
    font-size: 13px;
    font-weight: 650;
    color: var(--text);
  }
  .sub {
    font-size: 12px;
  }
  .link-text {
    color: var(--primary);
    text-decoration: underline;
  }
  .hint {
    margin-top: 2px;
    font-size: 11px;
  }
  .native {
    position: absolute;
    width: 1px;
    height: 1px;
    padding: 0;
    margin: -1px;
    overflow: hidden;
    clip: rect(0 0 0 0);
    white-space: nowrap;
    border: 0;
  }
  .error {
    margin: 0;
    color: var(--danger);
    font-size: 12px;
  }
  .selected {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 6px;
  }
  .selected li {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 6px 8px;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    background: var(--surface);
    font-size: 12px;
  }
  .selected img {
    width: 34px;
    height: 34px;
    object-fit: cover;
    border-radius: 4px;
  }
  .selected .name {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .selected .size {
    color: var(--text-muted);
  }
  .remove {
    display: grid;
    place-items: center;
    width: 24px;
    height: 24px;
    border: 0;
    border-radius: 4px;
    background: transparent;
    color: var(--text-muted);
    cursor: pointer;
  }
  .remove:hover {
    background: var(--surface-muted);
    color: var(--danger);
  }
</style>
