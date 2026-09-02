<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { Plus, RefreshCw } from '@lucide/svelte';
  import { api, APIRequestError, type Session } from '$lib/api';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { Badge } from '$lib/components/ui/badge';
  import * as Field from '$lib/components/ui/field';
  import { DateInput } from '$lib/components/varya/date-input';
  import { formatDate } from '$lib/design/formatters';
  import {
    createPayrollRun,
    listLegislationPacks,
    listPayrollRuns,
    listTimesheetPeriods
  } from '$lib/features/hr/api';
  import {
    money,
    payrollStatusLabel,
    periodLabel,
    statusTone,
    type PayrollRun
  } from '$lib/features/hr/types';

  let permissions = $state<string[]>([]);
  let denied = $state(false);
  let loading = $state(true);
  let error = $state('');
  let rows = $state<PayrollRun[]>([]);

  let creating = $state(false);
  let saving = $state(false);
  let createError = $state('');
  let periods = $state<{ id: string; period_year: number; period_month: number; status: string }[]>(
    []
  );
  let packs = $state<{ id: string; code: string; status: string }[]>([]);
  let form = $state({
    run_number: '',
    payment_date: '',
    timesheet_period_id: '',
    legislation_pack_id: ''
  });

  const canCalc = $derived(permissions.includes('hr.payroll.calculate'));

  async function loadSession() {
    try {
      const session = await api<Session>('/session');
      permissions = session.permissions ?? [];
      denied = !permissions.includes('hr.payroll.read');
    } catch {
      denied = true;
    }
  }

  async function load() {
    if (denied) return;
    loading = true;
    error = '';
    try {
      rows = (await listPayrollRuns()).items;
    } catch (cause) {
      error = cause instanceof APIRequestError ? cause.message : 'Bordrolar yüklenemedi.';
    } finally {
      loading = false;
    }
  }

  async function openCreate() {
    creating = true;
    createError = '';
    try {
      periods = (await listTimesheetPeriods()).items.filter((p) => p.status === 'FINALIZED');
      packs = (await listLegislationPacks()).items.filter((p) => p.status === 'ACTIVE');
      if (packs[0]) form.legislation_pack_id = packs[0].id;
    } catch {
      /* ignore */
    }
  }

  async function submitCreate(event: SubmitEvent) {
    event.preventDefault();
    if (saving) return;
    saving = true;
    createError = '';
    try {
      const created = await createPayrollRun(form);
      creating = false;
      await goto(`/personel/bordro/${created.id}`);
    } catch (cause) {
      createError = cause instanceof APIRequestError ? cause.message : 'Bordro oluşturulamadı.';
    } finally {
      saving = false;
    }
  }

  onMount(async () => {
    await loadSession();
    await load();
  });
</script>

<svelte:head><title>Bordro · Varya One</title></svelte:head>

{#if denied}
  <section class="card">Bordroyu görüntüleme yetkiniz yok.</section>
{:else}
  <header class="page-header">
    <div>
      <h1>Bordro</h1>
    </div>
    <div class="page-actions">
      <Button variant="outline" onclick={() => void load()}
        ><RefreshCw size={14} aria-hidden="true" />Yenile</Button
      >
      {#if canCalc}<Button onclick={openCreate}
          ><Plus size={14} aria-hidden="true" />Yeni Bordro</Button
        >{/if}
    </div>
  </header>

  {#if creating}
    <section class="card">
      <div class="section-heading">
        <h2>Yeni bordro</h2>
        <Button variant="ghost" onclick={() => (creating = false)}>Vazgeç</Button>
      </div>
      <form onsubmit={submitCreate}>
        <Field.FieldGroup class="grid2">
          <Field.Field>
            <Field.FieldLabel for="r-num">Bordro no</Field.FieldLabel>
            <Input
              id="r-num"
              bind:value={form.run_number}
              placeholder="Boş bırakırsanız otomatik üretilir"
            />
          </Field.Field>
          <Field.Field
            ><Field.FieldLabel for="r-ts">Puantaj dönemi</Field.FieldLabel>
            <select id="r-ts" bind:value={form.timesheet_period_id} class="select" required>
              <option value="">Kesinleşmiş dönem seçin</option>
              {#each periods as p}<option value={p.id}
                  >{periodLabel(p.period_year, p.period_month)}</option
                >{/each}
            </select>
            <Field.FieldDescription
              >Bordro dönemi seçtiğiniz puantajdan alınır.</Field.FieldDescription
            >
          </Field.Field>
          <Field.Field>
            <Field.FieldLabel for="r-pay">Ödeme tarihi</Field.FieldLabel>
            <DateInput id="r-pay" bind:value={form.payment_date} ariaLabel="Ödeme tarihi" />
            <Field.FieldDescription
              >Boş bırakılırsa dönemin son günü kullanılır.</Field.FieldDescription
            >
          </Field.Field>
          {#if packs.length > 1}
            <Field.Field
              ><Field.FieldLabel for="r-pack">Mevzuat paketi</Field.FieldLabel>
              <select id="r-pack" bind:value={form.legislation_pack_id} class="select" required>
                {#each packs as p}<option value={p.id}>{p.code}</option>{/each}
              </select>
            </Field.Field>
          {/if}
          {#if createError}<p class="notice error full">{createError}</p>{/if}
          <div class="full end"><Button type="submit" disabled={saving}>Oluştur</Button></div>
        </Field.FieldGroup>
      </form>
    </section>
  {/if}

  {#if error}<p class="notice error" role="alert">{error}</p>{/if}
  <section class="card list-card">
    {#if loading}
      <p class="list-state">Yükleniyor…</p>
    {:else if !rows.length}
      <p class="list-state">Bordro yok.</p>
    {:else}
      <div class="table-scroll">
        <table class="data-table">
          <thead
            ><tr
              ><th>Bordro</th><th>Dönem</th><th>Ödeme</th><th>Durum</th><th class="num"
                >Toplam net</th
              ></tr
            ></thead
          >
          <tbody>
            {#each rows as r}
              <tr class="row-link" onclick={() => goto(`/personel/bordro/${r.id}`)}>
                <td><strong>{r.run_number}</strong></td>
                <td class="muted">{periodLabel(r.period_year, r.period_month)}</td>
                <td class="muted">{formatDate(r.payment_date)}</td>
                <td><Badge tone={statusTone(r.status)}>{payrollStatusLabel(r.status)}</Badge></td>
                <td class="num">{money(r.total_net)} ₺</td>
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
  }
  .section-heading {
    display: flex;
    justify-content: space-between;
    align-items: center;
    margin-bottom: 12px;
  }
  .section-heading h2 {
    margin: 0;
    font-size: 15px;
  }
  :global(.grid2) {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 12px;
  }
  :global(.grid2) .full {
    grid-column: 1 / -1;
  }
  .end {
    display: flex;
    justify-content: flex-end;
  }
  .select {
    height: var(--control-height);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    background: var(--surface);
    color: var(--text);
    padding: 0 9px;
    font-size: 12px;
  }
</style>
