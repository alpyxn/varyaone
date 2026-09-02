<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { api, APIRequestError, type Session } from '$lib/api';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { Badge } from '$lib/components/ui/badge';
  import * as Field from '$lib/components/ui/field';
  import * as hr from '$lib/features/hr/api';
  import { money, type LegislationPack, type WagePreview } from '$lib/features/hr/types';

  let session = $state<Session>();
  let loading = $state(true);
  let error = $state('');
  let msg = $state('');
  let saving = $state(false);

  let packs = $state<LegislationPack[]>([]);
  let current = $state<WagePreview | null>(null);
  let showForm = $state(false);

  let form = $state({ minimum_monthly_gross: '', change_reason: '' });

  const canManage = $derived(Boolean(session?.permissions.includes('hr.legislation.manage')));
  const canRead = $derived(Boolean(session?.permissions.includes('hr.legislation.read')));

  function statusLabel(s: string) {
    return s === 'ACTIVE' ? 'Aktif' : s === 'DRAFT' ? 'Taslak' : 'Geçmiş';
  }

  async function load() {
    try {
      session = await api<Session>('/session');
    } catch {
      await goto('/giris');
      return;
    }
    if (!session.permissions.includes('hr.legislation.read')) {
      loading = false;
      return;
    }
    await refresh();
    loading = false;
  }

  async function refresh() {
    error = '';
    try {
      const [packList, minWage] = await Promise.all([
        hr.listLegislationPacks(),
        hr.minimumWage().catch(() => null)
      ]);
      packs = packList.items;
      current = minWage;
    } catch (cause) {
      error =
        cause instanceof APIRequestError ? cause.message : 'Asgari ücret tanımları yüklenemedi.';
    }
  }

  async function save(e: SubmitEvent) {
    e.preventDefault();
    if (saving || !form.minimum_monthly_gross.trim()) return;
    saving = true;
    error = '';
    msg = '';
    try {
      const result = await hr.replaceMinimumWage({
        minimum_monthly_gross: form.minimum_monthly_gross.trim(),
        change_reason: form.change_reason.trim() || undefined
      });
      msg =
        result.warning ??
        'Yeni asgari ücret tanımlandı. Önceki tanım geçmişe alındı, asgari ücretli çalışanların ücreti güncellendi.';
      showForm = false;
      form = { minimum_monthly_gross: '', change_reason: '' };
      await refresh();
    } catch (cause) {
      error = cause instanceof APIRequestError ? cause.message : 'Kaydedilemedi.';
    } finally {
      saving = false;
    }
  }

  async function activateDraft(id: string) {
    if (saving) return;
    saving = true;
    error = '';
    msg = '';
    try {
      const result = await hr.activateLegislationPack(id);
      msg = result.warning ?? 'Taslak aktifleştirildi.';
      await refresh();
    } catch (cause) {
      error = cause instanceof APIRequestError ? cause.message : 'Aktifleştirilemedi.';
    } finally {
      saving = false;
    }
  }

  onMount(() => {
    void load();
  });
</script>

<svelte:head><title>Asgari Ücret · Varya One</title></svelte:head>

<header class="page-header">
  <div>
    <h1>Asgari ücret</h1>
    <p>
      Sistemin kullandığı güncel asgari ücret. Personel ücret formunda "asgari ücretli" işaretlenen
      çalışanlar bu brüt tutarı kullanır; yeni tanım yaptığınızda önceki tanım geçmişe alınır ve bu
      çalışanların ücreti otomatik güncellenir.
    </p>
  </div>
  <div class="page-actions">
    <a class="button secondary" href="/ayarlar/tanimlar">Tüm tanımlar</a>
  </div>
</header>

{#if msg}<p class="notice ok" role="status">{msg}</p>{/if}
{#if error}<p class="notice error" role="alert">{error}</p>{/if}

{#if loading}
  <section class="card"><p class="state">Yükleniyor…</p></section>
{:else if !canRead}
  <section class="card">Bu sayfayı görüntüleme yetkiniz yok.</section>
{:else}
  <section class="card">
    <div class="card-head">
      <h2>Güncel asgari ücret</h2>
      {#if canManage}
        <Button
          variant="outline"
          onclick={() => {
            showForm = !showForm;
            form = { minimum_monthly_gross: current?.gross ?? '', change_reason: '' };
          }}>{showForm ? 'Vazgeç' : 'Yeni tanım'}</Button
        >
      {/if}
    </div>

    {#if current}
      <div class="current-grid">
        <div class="figure">
          <span class="label">Brüt</span>
          <strong>{money(current.gross)} ₺</strong>
        </div>
        <div class="figure">
          <span class="label">Net (tahmini)</span>
          <strong>{money(current.net)} ₺</strong>
        </div>
        <div class="figure">
          <span class="label">SGK işçi payı</span>
          <span>{money(current.employee_sgk)} ₺</span>
        </div>
        <div class="figure">
          <span class="label">İşsizlik işçi payı</span>
          <span>{money(current.employee_unemployment)} ₺</span>
        </div>
      </div>
    {:else}
      <p class="state">Tanımlı asgari ücret bulunamadı.</p>
    {/if}

    {#if showForm && canManage}
      <form class="new-form" onsubmit={save}>
        <Field.Field>
          <Field.FieldLabel for="mw-gross">Yeni aylık brüt asgari ücret (₺)</Field.FieldLabel>
          <Input
            id="mw-gross"
            bind:value={form.minimum_monthly_gross}
            inputmode="decimal"
            placeholder="Örn. 33030"
            required
          />
        </Field.Field>
        <Field.Field class="grow">
          <Field.FieldLabel for="mw-reason">Not (opsiyonel)</Field.FieldLabel>
          <Input
            id="mw-reason"
            bind:value={form.change_reason}
            placeholder="Örn. resmî asgari ücret zammı"
          />
        </Field.Field>
        <Field.FieldDescription class="grow">
          SGK taban/tavan ve damga vergisi oranı önceki tanımdan otomatik hesaplanır. Vergi
          dilimleri ve kesinti şemaları aynen korunur.
        </Field.FieldDescription>
        <div class="form-actions">
          <Button type="submit" disabled={saving}>Kaydet ve uygula</Button>
        </div>
      </form>
    {/if}
  </section>

  <section class="card">
    <h2>Tanım geçmişi</h2>
    {#if !packs.length}
      <p class="state">Kayıt yok.</p>
    {:else}
      <div class="scroll">
        <table>
          <thead>
            <tr
              ><th>Durum</th><th class="numeric">Aylık brüt asgari ücret</th><th
                aria-label="İşlemler"
              ></th></tr
            >
          </thead>
          <tbody>
            {#each packs as p}
              <tr>
                <td
                  ><Badge
                    tone={p.status === 'ACTIVE'
                      ? 'success'
                      : p.status === 'DRAFT'
                        ? 'warning'
                        : 'neutral'}>{statusLabel(p.status)}</Badge
                  ></td
                >
                <td class="numeric"
                  >{p.minimum_monthly_gross ? `${money(p.minimum_monthly_gross)} ₺` : '—'}</td
                >
                <td class="actions-cell">
                  {#if canManage && p.status === 'DRAFT'}
                    <button class="link" disabled={saving} onclick={() => activateDraft(p.id)}
                      >Aktifleştir</button
                    >
                  {/if}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </section>
{/if}

<style>
  .card {
    margin-top: 14px;
    padding: 16px;
  }
  .card h2 {
    margin: 0 0 10px;
    font-size: 15px;
  }
  .card-head {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 10px;
    margin-bottom: 12px;
  }
  .card-head h2 {
    margin: 0;
  }
  .state {
    padding: 10px 0;
    text-align: center;
    color: var(--text-muted);
    font-size: 13px;
    margin: 0;
  }
  .current-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
    gap: 14px;
  }
  .figure {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }
  .figure .label {
    font-size: 11px;
    color: var(--text-muted);
  }
  .figure strong {
    font-size: 18px;
  }
  .new-form {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
    gap: 12px;
    margin-top: 16px;
    padding: 12px;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    background: var(--surface-muted, var(--surface));
  }
  .new-form :global(.grow) {
    grid-column: 1 / -1;
  }
  .form-actions {
    grid-column: 1 / -1;
    display: flex;
    justify-content: flex-end;
  }
  .scroll {
    overflow-x: auto;
  }
  table {
    width: 100%;
    border-collapse: collapse;
    font-size: 12px;
  }
  th,
  td {
    padding: 8px 10px;
    border-bottom: 1px solid var(--border);
    text-align: left;
    white-space: nowrap;
  }
  th {
    color: var(--text-muted);
    font-weight: 650;
  }
  td.numeric,
  th.numeric {
    text-align: right;
    font-variant-numeric: tabular-nums;
  }
  .actions-cell {
    white-space: nowrap;
  }
  .link {
    border: 0;
    background: transparent;
    color: var(--primary);
    cursor: pointer;
    font-size: 12px;
    font-weight: 650;
    padding: 0;
  }
  .link:hover {
    text-decoration: underline;
  }
</style>
