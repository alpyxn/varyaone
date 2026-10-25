<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { ArrowLeft, FileUp, LoaderCircle } from '@lucide/svelte';
  import { api, APIRequestError, type Session } from '$lib/api';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import * as Field from '$lib/components/ui/field';
  import {
    importLeads,
    listCallLists,
    listCRMMembers,
    previewLeadImport
  } from '$lib/features/crm/api';
  import {
    importFields,
    mapImportRows,
    type CallList,
    type ImportFieldKey,
    type ImportIssue,
    type ImportPreview,
    type ImportResult,
    type Member
  } from '$lib/features/crm/types';

  let permissions = $state<string[]>([]);
  let denied = $state(false);
  let members = $state<Member[]>([]);
  let callLists = $state<CallList[]>([]);

  let file = $state<File | null>(null);
  let preview = $state<ImportPreview | null>(null);
  let mapping = $state<Partial<Record<ImportFieldKey, number>>>({});
  let source = $state('');
  let owner = $state('');
  let callList = $state('');
  let allowDuplicates = $state(false);
  let busy = $state(false);
  let error = $state('');
  let result = $state<ImportResult | null>(null);

  const canManage = $derived(permissions.includes('crm.manage'));
  const sampleRows = $derived(preview ? preview.rows.slice(0, 5) : []);

  async function upload(event: SubmitEvent) {
    event.preventDefault();
    await readFile('');
  }

  // A workbook may hold several lists (and a summary sheet in front); the
  // server picks the first sheet that looks like a lead list, the user can
  // switch to another one.
  async function readFile(sheet: string) {
    if (!file || busy) return;
    busy = true;
    error = '';
    result = null;
    try {
      preview = await previewLeadImport(file, sheet);
      mapping = Object.fromEntries(
        importFields.map((field) => [field.key, preview?.mapping[field.key] ?? -1])
      );
    } catch (cause) {
      preview = null;
      error = cause instanceof APIRequestError ? cause.message : 'Dosya okunamadı.';
    } finally {
      busy = false;
    }
  }

  async function run() {
    if (!preview || busy) return;
    if ((mapping.display_name ?? -1) < 0) {
      error = 'Firma / kişi adı sütununu seçin.';
      return;
    }
    if ((mapping.phone ?? -1) < 0 && (mapping.email ?? -1) < 0 && (mapping.phone2 ?? -1) < 0) {
      error = 'Telefon ya da e-posta sütunu seçin.';
      return;
    }
    busy = true;
    error = '';
    try {
      result = await importLeads({
        rows: mapImportRows(preview.rows, mapping),
        owner_user_id: owner,
        source: source.trim(),
        call_list_id: callList,
        allow_duplicates: allowDuplicates
      });
      preview = null;
      file = null;
    } catch (cause) {
      error = cause instanceof APIRequestError ? cause.message : 'Aktarım tamamlanamadı.';
    } finally {
      busy = false;
    }
  }

  onMount(async () => {
    try {
      const session = await api<Session>('/session');
      permissions = session.permissions ?? [];
      denied = !permissions.includes('crm.edit');
    } catch {
      denied = true;
    }
    if (denied) return;
    listCRMMembers()
      .then((r) => (members = r.items))
      .catch(() => (members = []));
    if (permissions.includes('crm.manage')) {
      listCallLists()
        .then((r) => (callLists = r.items))
        .catch(() => (callLists = []));
    }
  });
</script>

{#snippet issueList(title: string, issues: ImportIssue[])}
  {#if issues.length}
    <details class="issues">
      <summary>{title} ({issues.length})</summary>
      <ul>
        {#each issues as issue (issue.row)}
          <li><b>{issue.row}. satır</b> {issue.name ? `· ${issue.name}` : ''} — {issue.message}</li>
        {/each}
      </ul>
    </details>
  {/if}
{/snippet}

<svelte:head><title>Aday Aktarımı · Varya One</title></svelte:head>

<header class="page-header">
  <div>
    <a class="back" href="/crm/adaylar"><ArrowLeft size={14} aria-hidden="true" /> Adaylar</a>
    <h1>Aday Aktarımı</h1>
    <p class="meta">
      Excel (.xlsx) ya da CSV dosyasından aday listesi yükleyin. İlk satır başlık olmalıdır; en
      fazla 5000 satır.
    </p>
  </div>
</header>

{#if denied}
  <section class="panel error-panel" role="alert">
    <strong>Erişim yok.</strong>
    <span>Aday ekleme yetkiniz bulunmuyor.</span>
  </section>
{:else}
  {#if error}<p class="notice error" role="alert">{error}</p>{/if}

  {#if result}
    <section class="panel result" aria-labelledby="result-title">
      <h2 id="result-title">Aktarım tamamlandı</h2>
      <p>
        <strong>{result.created}</strong> aday eklendi{#if result.added_to_list}, {result.added_to_list}
          tanesi arama listesine kondu{/if}.
      </p>
      {@render issueList('Zaten kayıtlı numaralar', result.duplicates)}
      {@render issueList('Aranmak istemeyen numaralar', result.do_not_call)}
      {@render issueList('Hatalı satırlar', result.invalid)}
      <div class="actions">
        <Button variant="outline" onclick={() => (result = null)}>Başka dosya yükle</Button>
        <Button onclick={() => goto('/crm/adaylar')}>Adaylara git</Button>
      </div>
    </section>
  {:else if !preview}
    <section class="panel">
      <form class="upload" onsubmit={upload}>
        <input
          type="file"
          accept=".xlsx,.csv,text/csv,application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
          aria-label="Aday dosyası"
          onchange={(e) => (file = e.currentTarget.files?.[0] ?? null)}
        />
        <Button type="submit" disabled={!file || busy}>
          {#if busy}<LoaderCircle data-icon="inline-start" aria-hidden="true" />{:else}<FileUp
              size={14}
              data-icon="inline-start"
              aria-hidden="true"
            />{/if}Dosyayı oku
        </Button>
      </form>
      <p class="hint">
        Örnek başlıklar: Firma Adı, Yetkili, Telefon, E-Posta, Şehir, İlçe, Sektör. Başlıklar
        otomatik eşleştirilir; sonraki adımda değiştirebilirsiniz.
      </p>
    </section>
  {:else}
    <section class="panel" aria-labelledby="map-title">
      <h2 id="map-title">Sütunları eşleştirin · {preview.total} satır</h2>
      {#if preview.sheets.length > 1}
        <div class="sheets" role="tablist" aria-label="Çalışma sayfası">
          {#each preview.sheets as sheet (sheet.name)}
            <button
              type="button"
              role="tab"
              aria-selected={sheet.name === preview.sheet}
              class:active={sheet.name === preview.sheet}
              disabled={busy}
              onclick={() => void readFile(sheet.name)}>{sheet.name} ({sheet.rows})</button
            >
          {/each}
        </div>
        <p class="hint">
          Her seferinde bir sayfa aktarılır. Aynı numara başka sayfada da varsa ikinci kez eklenmez.
        </p>
      {/if}
      <div class="mapping">
        {#each importFields as field (field.key)}
          <Field.Field>
            <Field.FieldLabel for="map-{field.key}"
              >{field.label}{'required' in field && field.required ? ' *' : ''}</Field.FieldLabel
            >
            <select id="map-{field.key}" class="select" bind:value={mapping[field.key]}>
              <option value={-1}>— Kullanma —</option>
              {#each preview.headers as header, index (index)}
                <option value={index}>{header}</option>
              {/each}
            </select>
          </Field.Field>
        {/each}
      </div>

      <h3>İlk satırlar</h3>
      <!-- svelte-ignore a11y_no_noninteractive_tabindex (a scrollable region must be reachable by keyboard) -->
      <div class="table-scroll" tabindex="0" role="region" aria-label="Önizleme">
        <table>
          <thead>
            <tr>
              {#each importFields.filter((f) => (mapping[f.key] ?? -1) >= 0) as field (field.key)}
                <th>{field.label}</th>
              {/each}
            </tr>
          </thead>
          <tbody>
            {#each sampleRows as row, rowIndex (rowIndex)}
              <tr>
                {#each importFields.filter((f) => (mapping[f.key] ?? -1) >= 0) as field (field.key)}
                  <td>{row[mapping[field.key] ?? -1] ?? ''}</td>
                {/each}
              </tr>
            {/each}
          </tbody>
        </table>
      </div>

      <h3>Seçenekler</h3>
      <div class="options">
        <Field.Field>
          <Field.FieldLabel for="imp-source">Kaynak</Field.FieldLabel>
          <Input
            id="imp-source"
            bind:value={source}
            maxlength={120}
            placeholder="Örn. 2026 fuar katılımcıları"
          />
          <Field.FieldDescription>Dosyada kaynak sütunu boşsa bu yazılır.</Field.FieldDescription>
        </Field.Field>
        {#if canManage}
          <Field.Field>
            <Field.FieldLabel for="imp-owner">Sorumlu</Field.FieldLabel>
            <select id="imp-owner" class="select" bind:value={owner}>
              <option value="">Atanmamış (ortak havuz)</option>
              {#each members as member (member.user_id)}
                <option value={member.user_id}>{member.display_name}</option>
              {/each}
            </select>
          </Field.Field>
          <Field.Field>
            <Field.FieldLabel for="imp-list">Arama listesine ekle</Field.FieldLabel>
            <select id="imp-list" class="select" bind:value={callList}>
              <option value="">Ekleme</option>
              {#each callLists as list (list.id)}
                <option value={list.id}>{list.name}</option>
              {/each}
            </select>
          </Field.Field>
        {/if}
        <label class="check">
          <input type="checkbox" bind:checked={allowDuplicates} />
          Başka adayda kayıtlı numaraları da ekle
        </label>
      </div>
      <p class="hint">
        "Aranmasın" işaretli numaralar hiçbir durumda yeniden eklenmez. Arama durumu sütununda Yeni,
        Görüşüldü, İlgili, Elendi ya da Aranmasın yazabilir; elenen ve aranmasın denenler arama
        listesine konmaz.
      </p>
      <div class="actions">
        <Button variant="ghost" onclick={() => (preview = null)}>Vazgeç</Button>
        <Button onclick={() => void run()} disabled={busy}>
          {#if busy}<LoaderCircle data-icon="inline-start" aria-hidden="true" />{/if}
          {preview.total} satırı aktar
        </Button>
      </div>
    </section>
  {/if}
{/if}

<style>
  .page-header {
    display: flex;
    justify-content: space-between;
    align-items: flex-start;
    gap: 12px;
    flex-wrap: wrap;
  }
  .back {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    color: var(--text-muted);
    font-size: 12px;
    text-decoration: none;
  }
  .meta {
    margin: 4px 0 0;
    color: var(--text-muted);
    font-size: 12px;
  }
  .panel {
    margin-top: 16px;
    padding: 14px 16px;
  }
  .panel h2 {
    margin: 0 0 12px;
    font-size: 14px;
  }
  .panel h3 {
    margin: 18px 0 8px;
    font-size: 13px;
  }
  .sheets {
    display: flex;
    gap: 6px;
    flex-wrap: wrap;
    margin-bottom: 4px;
  }
  .sheets button {
    height: 28px;
    padding: 0 12px;
    border: 1px solid var(--border-strong);
    border-radius: 999px;
    background: var(--surface);
    color: var(--text);
    font-size: 12px;
    cursor: pointer;
  }
  .sheets button.active {
    border-color: var(--primary);
    background: color-mix(in srgb, var(--primary) 12%, transparent);
    color: var(--primary);
    font-weight: 600;
  }
  .upload {
    display: flex;
    align-items: center;
    gap: 10px;
    flex-wrap: wrap;
  }
  .hint {
    margin: 10px 0 0;
    color: var(--text-muted);
    font-size: 12px;
  }
  .mapping,
  .options {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(min(100%, 200px), 1fr));
    gap: 12px 14px;
  }
  .options :global(input:not([type='checkbox'])) {
    width: 100%;
    box-sizing: border-box;
  }
  .check {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 13px;
    grid-column: 1 / -1;
  }
  .select {
    height: var(--control-height);
    width: 100%;
    box-sizing: border-box;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    background: var(--surface);
    color: var(--text);
    padding: 0 10px;
    font-size: 13px;
  }
  .table-scroll {
    overflow-x: auto;
  }
  table {
    width: 100%;
    border-collapse: collapse;
    font-size: 12px;
  }
  th,
  td {
    padding: 7px 10px;
    border-bottom: 1px solid var(--border);
    text-align: left;
    white-space: nowrap;
  }
  th {
    color: var(--text-muted);
    font-weight: 650;
  }
  .actions {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
    margin-top: 14px;
  }
  .result p {
    font-size: 14px;
  }
  .issues {
    margin-top: 8px;
    font-size: 12px;
  }
  .issues summary {
    cursor: pointer;
    font-weight: 600;
  }
  .issues ul {
    margin: 6px 0 0;
    padding-left: 18px;
    max-height: 240px;
    overflow-y: auto;
  }
</style>
