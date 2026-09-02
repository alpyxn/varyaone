<script lang="ts">
  import { onMount } from 'svelte';
  import { api, APIRequestError, type Session } from '$lib/api';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { DateInput } from '$lib/components/varya/date-input';
  import { TimeInput } from '$lib/components/varya/time-input';
  import * as hr from '$lib/features/hr/api';
  import { formatDate } from '$lib/design/formatters';
  import type { ScheduleTemplate } from '$lib/features/hr/types';

  const WD = ['Pzt', 'Sal', 'Çar', 'Per', 'Cum', 'Cmt', 'Paz'];

  let permissions = $state<string[]>([]);
  let denied = $state(false);
  let loading = $state(true);
  let msg = $state('');
  let actionError = $state('');
  let templates = $state<ScheduleTemplate[]>([]);
  let selected = $state<ScheduleTemplate | null>(null);
  let confirmingVersion = $state<string | null>(null);

  let newCode = $state('');
  let newName = $state('');
  let versionFrom = $state('');
  let days = $state(
    Array.from({ length: 7 }, (_, i) => ({
      weekday: i + 1,
      is_workday: i < 5,
      starts_at: i < 5 ? '09:00' : '',
      ends_at: i < 5 ? '18:00' : '',
      break_minutes: i < 5 ? 60 : 0,
      planned_minutes: i < 5 ? 480 : 0
    }))
  );

  const canEdit = $derived(permissions.includes('hr.schedule.edit'));

  async function loadSession() {
    try {
      const s = await api<Session>('/session');
      permissions = s.permissions ?? [];
      denied = !permissions.includes('hr.schedule.read');
    } catch {
      denied = true;
    }
  }

  async function load() {
    if (denied) return;
    loading = true;
    try {
      templates = (await hr.listScheduleTemplates()).items;
    } catch (cause) {
      actionError = cause instanceof APIRequestError ? cause.message : 'Şablonlar yüklenemedi.';
    } finally {
      loading = false;
    }
  }

  async function open(t: ScheduleTemplate) {
    try {
      selected = await hr.getScheduleTemplate(t.id);
    } catch (cause) {
      actionError = cause instanceof APIRequestError ? cause.message : 'Şablon açılamadı.';
    }
  }

  function recalc(i: number) {
    const d = days[i];
    if (!d.is_workday || !d.starts_at || !d.ends_at) {
      d.planned_minutes = 0;
      return;
    }
    const [sh, sm] = d.starts_at.split(':').map(Number);
    const [eh, em] = d.ends_at.split(':').map(Number);
    const gross = eh * 60 + em - (sh * 60 + sm);
    d.planned_minutes = Math.max(0, gross - (Number(d.break_minutes) || 0));
  }

  async function createTemplate(e: SubmitEvent) {
    e.preventDefault();
    actionError = '';
    try {
      const t = await hr.createScheduleTemplate(newCode.trim(), newName.trim());
      newCode = '';
      newName = '';
      msg = 'Şablon oluşturuldu.';
      await load();
      await open(t);
    } catch (cause) {
      actionError = cause instanceof APIRequestError ? cause.message : 'Şablon oluşturulamadı.';
    }
  }

  async function deleteVersion(versionID: string) {
    if (!selected) return;
    actionError = '';
    msg = '';
    try {
      selected = await hr.deleteScheduleVersion(selected.id, versionID);
      confirmingVersion = null;
      msg = 'Geçerlilik dönemi silindi.';
    } catch (cause) {
      actionError = cause instanceof APIRequestError ? cause.message : 'Dönem silinemedi.';
    }
  }

  async function addVersion(e: SubmitEvent) {
    e.preventDefault();
    if (!selected) return;
    actionError = '';
    try {
      selected = await hr.addScheduleVersion(selected.id, {
        effective_from: versionFrom,
        effective_to: '',
        days: days.map((d) => ({
          weekday: d.weekday,
          is_workday: d.is_workday,
          starts_at: d.is_workday ? d.starts_at : null,
          ends_at: d.is_workday ? d.ends_at : null,
          ends_next_day: false,
          break_minutes: d.break_minutes,
          planned_minutes: d.planned_minutes
        }))
      });
      msg = 'Sürüm eklendi.';
    } catch (cause) {
      actionError = cause instanceof APIRequestError ? cause.message : 'Sürüm eklenemedi.';
    }
  }

  onMount(async () => {
    await loadSession();
    await load();
  });
</script>

<svelte:head><title>Çalışma Planı · Varya One</title></svelte:head>

{#if denied}
  <section class="card">Çalışma planlarını görüntüleme yetkiniz yok.</section>
{:else}
  <header class="page-header">
    <div>
      <h1>Çalışma Planı</h1>
    </div>
  </header>

  {#if msg}<p class="notice ok" role="status">{msg}</p>{/if}
  {#if actionError}<p class="notice error" role="alert">{actionError}</p>{/if}

  <section class="card">
    {#if canEdit}
      <form class="inline" onsubmit={createTemplate}>
        <Input bind:value={newCode} placeholder="Kod — boş bırakırsanız otomatik üretilir" />
        <Input bind:value={newName} placeholder="Ad" required />
        <Button type="submit">Şablon ekle</Button>
      </form>
    {/if}
    {#if loading}
      <p class="list-state">Yükleniyor…</p>
    {:else if !templates.length}
      <p class="list-state">Şablon yok.</p>
    {:else}
      <div class="table-scroll">
        <table class="data-table">
          <thead><tr><th>Kod</th><th>Ad</th><th aria-label="İşlemler"></th></tr></thead>
          <tbody>
            {#each templates as t}
              <tr
                ><td>{t.code}</td><td><strong>{t.name}</strong></td><td
                  ><button class="link" onclick={() => open(t)}>Aç</button></td
                ></tr
              >
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </section>

  {#if selected}
    <section class="card">
      <div class="sel-head">
        <h2>{selected.code} · {selected.name}</h2>
        <Button
          variant="ghost"
          onclick={() => {
            selected = null;
            confirmingVersion = null;
          }}>Kapat</Button
        >
      </div>
      {#if !(selected.versions ?? []).length}
        <p class="list-state">Bu şablonda henüz geçerlilik dönemi yok.</p>
      {/if}
      {#each selected.versions ?? [] as v}
        <div class="version">
          <div class="version-head">
            <strong
              >{formatDate(v.effective_from)} – {v.effective_to
                ? formatDate(v.effective_to)
                : 'Açık'}</strong
            >
            {#if canEdit}
              {#if confirmingVersion === v.id}
                <span class="confirm">
                  Silinsin mi?
                  <button class="link danger" onclick={() => deleteVersion(v.id)}>Evet, sil</button>
                  <button class="link" onclick={() => (confirmingVersion = null)}>Vazgeç</button>
                </span>
              {:else}
                <button class="link danger" onclick={() => (confirmingVersion = v.id)}>Sil</button>
              {/if}
            {/if}
          </div>
          <div class="days">
            {#each v.days as d}
              <span class:off={!d.is_workday}>
                {WD[d.weekday - 1]}: {d.is_workday
                  ? `${d.starts_at}-${d.ends_at} (${d.planned_minutes}dk)`
                  : 'Tatil'}
              </span>
            {/each}
          </div>
        </div>
      {/each}
      {#if canEdit}
        <form class="new-version" onsubmit={addVersion}>
          <h3>Yeni geçerlilik dönemi</h3>
          <label class="from-field">
            Geçerlilik başlangıcı
            <DateInput bind:value={versionFrom} ariaLabel="Geçerlilik başlangıcı" />
          </label>
          <p class="grid-hint">
            Her gün için mesai başlangıç/bitiş saatini ve toplam mola süresini (dakika) girin.
            <strong>Net süre</strong> = bitiş − başlangıç − mola, otomatik hesaplanır.
          </p>
          <div class="day-grid">
            {#each days as d, i}
              <div class="day" class:off={!d.is_workday}>
                <label class="day-toggle">
                  <input type="checkbox" bind:checked={d.is_workday} onchange={() => recalc(i)} />
                  <span>{WD[i]}</span>
                </label>
                {#if d.is_workday}
                  <div class="day-fields">
                    <label>
                      <span>Başlangıç</span>
                      <TimeInput
                        bind:value={d.starts_at}
                        onValueChange={() => recalc(i)}
                        ariaLabel="{WD[i]} başlangıç saati"
                      />
                    </label>
                    <label>
                      <span>Bitiş</span>
                      <TimeInput
                        bind:value={d.ends_at}
                        onValueChange={() => recalc(i)}
                        ariaLabel="{WD[i]} bitiş saati"
                      />
                    </label>
                    <label>
                      <span>Mola (dk)</span>
                      <Input
                        type="number"
                        min="0"
                        bind:value={d.break_minutes}
                        oninput={() => recalc(i)}
                        aria-label="{WD[i]} mola süresi (dakika)"
                      />
                    </label>
                    <div class="net">
                      <span>Net süre</span>
                      <strong>{d.planned_minutes} dk</strong>
                    </div>
                  </div>
                {:else}
                  <span class="day-off-label">Tatil</span>
                {/if}
              </div>
            {/each}
          </div>
          <Button type="submit">Sürüm ekle</Button>
        </form>
      {/if}
    </section>
  {/if}
{/if}

<style>
  .card {
    margin-top: 14px;
  }
  .card h2 {
    margin: 0 0 10px;
    font-size: 15px;
  }
  .card h3 {
    margin: 12px 0 8px;
    font-size: 13px;
  }
  .inline {
    display: flex;
    gap: 8px;
    margin-bottom: 12px;
    flex-wrap: wrap;
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
  .sel-head {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 10px;
  }
  .version {
    margin: 10px 0;
    padding: 8px;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
  }
  .version-head {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 10px;
  }
  .confirm {
    display: inline-flex;
    gap: 10px;
    align-items: center;
    font-size: 12px;
    color: var(--text-muted);
  }
  .link.danger {
    color: var(--danger);
  }
  .days {
    display: flex;
    gap: 10px;
    flex-wrap: wrap;
    margin-top: 6px;
    font-size: 12px;
  }
  .days .off {
    color: var(--text-muted);
  }
  .new-version {
    margin-top: 12px;
    border-top: 1px solid var(--border);
    padding-top: 12px;
  }
  .from-field {
    display: grid;
    gap: 5px;
    max-width: 220px;
    font-size: 12px;
    font-weight: 650;
    color: var(--text-muted);
  }
  .grid-hint {
    margin: 12px 0 4px;
    font-size: 12px;
    line-height: 1.5;
    color: var(--text-muted);
  }
  .grid-hint strong {
    color: var(--text);
  }
  .day-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(260px, 1fr));
    gap: 10px;
    margin: 6px 0 14px;
  }
  .day {
    padding: 10px;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    background: var(--surface);
  }
  .day.off {
    background: var(--surface-muted);
  }
  .day-toggle {
    display: flex;
    align-items: center;
    gap: 7px;
    font-size: 13px;
    font-weight: 650;
  }
  .day-off-label {
    display: block;
    margin-top: 6px;
    font-size: 12px;
    color: var(--text-muted);
  }
  .day-fields {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 8px;
    margin-top: 8px;
  }
  .day-fields label {
    display: grid;
    gap: 3px;
  }
  .day-fields label span {
    font-size: 10px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--text-muted);
  }
  .net {
    display: grid;
    gap: 3px;
    align-content: start;
    padding-left: 8px;
    border-left: 1px solid var(--border);
  }
  .net span {
    font-size: 10px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--text-muted);
  }
  .net strong {
    font-size: 14px;
    font-variant-numeric: tabular-nums;
  }
</style>
