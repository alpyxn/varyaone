<script lang="ts">
  import { onMount } from 'svelte';
  import { RefreshCw, ChevronLeft, ChevronRight, Check } from '@lucide/svelte';
  import { api, APIRequestError, type Session } from '$lib/api';
  import { matchesSearch } from '$lib/filtering';
  import { Button } from '$lib/components/ui/button';
  import { Badge } from '$lib/components/ui/badge';
  import { TimeInput } from '$lib/components/varya/time-input';
  import * as hr from '$lib/features/hr/api';
  import type { Employee, EmployeeReadiness, LeaveType } from '$lib/features/hr/types';
  import {
    MONTH_NAMES,
    periodLabel,
    payrollStatusLabel,
    statusTone,
    dayKindOf,
    dayKindLabel,
    dayKindTone,
    minutesToHours,
    ASSIGNABLE_DAY_KINDS,
    TR_PUBLIC_HOLIDAYS,
    type TimesheetDay,
    type TimesheetDayKind,
    type TimesheetPeriod,
    timesheetBlocker
  } from '$lib/features/hr/types';

  // İzin, ayrı bir talep/onay akışı yerine doğrudan burada — puantaj gününde
  // bir izin türü seçilerek — işaretlenir.
  const NON_LEAVE_KINDS = ASSIGNABLE_DAY_KINDS.filter(
    (k) => k !== 'PAID_LEAVE' && k !== 'UNPAID_LEAVE'
  );

  const WD = ['Pzt', 'Sal', 'Çar', 'Per', 'Cum', 'Cmt', 'Paz'];

  let permissions = $state<string[]>([]);
  let denied = $state(false);
  let loading = $state(true);
  let error = $state('');
  let msg = $state('');
  let actionError = $state('');
  let busy = $state(false);

  let periods = $state<TimesheetPeriod[]>([]);
  let selected = $state<TimesheetPeriod | null>(null);
  let employees = $state<Employee[]>([]);
  let activeEmployeeID = $state('');
  let empQuery = $state('');

  const now = new Date();
  let viewYear = $state(now.getFullYear());
  let viewMonth = $state(now.getMonth() + 1);

  let selection = $state<string[]>([]);
  let editHours = $state('');
  let editOvertime = $state('');
  let editNote = $state('');
  let selectedLeaveTypeID = $state('');
  let leaveTypes = $state<LeaveType[]>([]);
  let reopening = $state(false);
  let reopenReason = $state('');

  // Eksik kartlar: puantaja hiç girilemeyen çalışanlar ve bordroyu durduracak
  // eksikler burada, ilk tıklamadan önce görünür.
  let readiness = $state<EmployeeReadiness[]>([]);
  const readinessByID = $derived(new Map(readiness.map((r) => [r.employee_id, r])));

  const canEdit = $derived(permissions.includes('hr.timesheet.edit'));
  const canFinalize = $derived(permissions.includes('hr.timesheet.finalize'));
  const canReopen = $derived(permissions.includes('hr.timesheet.reopen'));
  const isDraft = $derived(selected?.status === 'DRAFT');
  const activeBlocker = $derived(timesheetBlocker(readinessByID.get(activeEmployeeID)));
  const canPick = $derived(!!selected && isDraft && canEdit && !activeBlocker);
  // Bordroyu durduracak eksikler (ücret, SGK kodu…) puantajı engellemez ama
  // ay kapanmadan düzeltilmeli.
  const payrollGaps = $derived(
    readiness.filter((r) => r.timesheet_ready && !r.payroll_ready && r.issues.length)
  );

  const filteredEmployees = $derived(
    employees.filter((e) =>
      matchesSearch(`${e.first_name} ${e.last_name} ${e.employee_code}`, empQuery)
    )
  );

  const daysByDate = $derived.by(() => {
    const map = new Map<string, TimesheetDay>();
    for (const d of selected?.days ?? []) {
      if (d.employee_id === activeEmployeeID) map.set(d.work_date, d);
    }
    return map;
  });

  type Cell = { iso: string; day: number; outside: boolean };
  const calendarCells = $derived.by<Cell[]>(() => {
    const y = viewYear;
    const m = viewMonth - 1;
    const offset = (new Date(Date.UTC(y, m, 1)).getUTCDay() + 6) % 7; // Monday-based
    const lastDay = new Date(Date.UTC(y, m + 1, 0)).getUTCDate();
    const total = Math.ceil((offset + lastDay) / 7) * 7;
    const cells: Cell[] = [];
    for (let i = 0; i < total; i += 1) {
      const date = new Date(Date.UTC(y, m, i - offset + 1));
      cells.push({
        iso: date.toISOString().slice(0, 10),
        day: date.getUTCDate(),
        outside: date.getUTCMonth() !== m
      });
    }
    return cells;
  });

  const summary = $derived.by(() => {
    const rows = new Map<
      string,
      {
        name: string;
        worked: number;
        paidLeave: number;
        unpaidLeave: number;
        holiday: number;
        absent: number;
      }
    >();
    for (const d of selected?.days ?? []) {
      let r = rows.get(d.employee_id);
      if (!r) {
        r = {
          name: d.employee_name,
          worked: 0,
          paidLeave: 0,
          unpaidLeave: 0,
          holiday: 0,
          absent: 0
        };
        rows.set(d.employee_id, r);
      }
      const kind = dayKindOf(d);
      if (kind === 'WORKED' || kind === 'HALF_DAY') r.worked += kind === 'HALF_DAY' ? 0.5 : 1;
      else if (kind === 'PAID_LEAVE') r.paidLeave += 1;
      else if (kind === 'UNPAID_LEAVE') r.unpaidLeave += 1;
      else if (kind === 'PUBLIC_HOLIDAY') r.holiday += 1;
      else if (kind === 'ABSENT') r.absent += 1;
    }
    return [...rows.values()].sort((a, b) => a.name.localeCompare(b.name, 'tr'));
  });

  const activeEmployeeName = $derived(
    (() => {
      const e = employees.find((x) => x.id === activeEmployeeID);
      return e ? `${e.first_name} ${e.last_name}` : '';
    })()
  );

  async function loadSession() {
    try {
      const s = await api<Session>('/session');
      permissions = s.permissions ?? [];
      denied = !permissions.includes('hr.timesheet.read');
    } catch {
      denied = true;
    }
  }

  async function load() {
    if (denied) return;
    loading = true;
    try {
      const [p, e, lt] = await Promise.all([
        hr.listTimesheetPeriods(),
        hr.listEmployees({ status: 'ACTIVE' }),
        hr.listLeaveTypes()
      ]);
      periods = p.items;
      employees = e.items;
      leaveTypes = lt.items.filter((t) => t.is_active);
      if (!activeEmployeeID && employees.length) activeEmployeeID = employees[0].id;
      await syncToView();
    } catch (cause) {
      error = cause instanceof APIRequestError ? cause.message : 'Puantaj yüklenemedi.';
    } finally {
      loading = false;
    }
  }

  // Show whichever period matches the visible month (open it, don't auto-create).
  async function syncToView() {
    selection = [];
    const match = periods.find((p) => p.period_year === viewYear && p.period_month === viewMonth);
    if (!match) {
      selected = null;
      readiness = [];
      return;
    }
    if (selected?.id === match.id && selected.version === match.version) return;
    try {
      selected = await hr.getTimesheetPeriod(match.id);
      await loadReadiness(match.id);
      if (selected.status === 'DRAFT' && !(selected.days ?? []).length && canEdit) {
        try {
          selected = await hr.generateTimesheet(selected.id);
        } catch {
          /* no schedule / employees yet */
        }
      }
    } catch (cause) {
      actionError = cause instanceof APIRequestError ? cause.message : 'Dönem açılamadı.';
    }
  }

  // Dönem her açıldığında eksik kartları da tazele: bir çalışanın işe giriş
  // tarihi bu ekrandan görünmez, ama puantaja girilip girilemeyeceğini belirler.
  async function loadReadiness(periodID: string) {
    try {
      readiness = (await hr.listTimesheetReadiness(periodID)).items;
    } catch {
      readiness = [];
    }
  }

  function stepMonth(delta: number) {
    let y = viewYear;
    let m = viewMonth + delta;
    if (m < 1) {
      m = 12;
      y -= 1;
    } else if (m > 12) {
      m = 1;
      y += 1;
    }
    viewYear = y;
    viewMonth = m;
    actionError = '';
    msg = '';
    void syncToView();
  }

  async function createForView() {
    if (busy) return;
    busy = true;
    actionError = '';
    msg = '';
    try {
      await hr.createTimesheetPeriod(viewYear, viewMonth);
    } catch (cause) {
      // 409 = zaten var; sadece açacağız.
      if (!(cause instanceof APIRequestError) || cause.status !== 409) {
        actionError = cause instanceof APIRequestError ? cause.message : 'Dönem oluşturulamadı.';
        busy = false;
        return;
      }
    }
    try {
      periods = (await hr.listTimesheetPeriods()).items;
      await syncToView();
      msg = 'Dönem açıldı.';
    } catch (cause) {
      actionError = cause instanceof APIRequestError ? cause.message : 'İşlem başarısız.';
    } finally {
      busy = false;
    }
  }

  async function run(fn: () => Promise<TimesheetPeriod>, ok = '') {
    if (busy) return;
    busy = true;
    actionError = '';
    msg = '';
    try {
      const updated = await fn();
      selected = updated;
      periods = periods.map((p) =>
        p.id === updated.id ? { ...p, ...updated, days: undefined } : p
      );
      if (ok) msg = ok;
    } catch (cause) {
      actionError = cause instanceof APIRequestError ? cause.message : 'İşlem başarısız.';
    } finally {
      busy = false;
    }
  }

  function toggleDay(iso: string) {
    if (!canPick) return;
    if (selection.includes(iso)) selection = selection.filter((d) => d !== iso);
    else selection = [...selection, iso];
    // seed the hour/note editor from the first selected day
    const first = selection[0];
    const day = first ? daysByDate.get(first) : undefined;
    editHours =
      day && day.worked_minutes > 0
        ? `${String(Math.floor(day.worked_minutes / 60)).padStart(2, '0')}:${String(day.worked_minutes % 60).padStart(2, '0')}`
        : '';
    editOvertime =
      day && day.overtime_minutes > 0
        ? `${String(Math.floor(day.overtime_minutes / 60)).padStart(2, '0')}:${String(day.overtime_minutes % 60).padStart(2, '0')}`
        : '';
    editNote = day?.explanation ?? '';
    selectedLeaveTypeID = day?.leave_type_id ?? '';
  }

  function selectWorkdays() {
    if (!canPick) return;
    selection = calendarCells
      .filter((c) => !c.outside)
      .map((c) => c.iso)
      .filter((iso) => {
        const wd = new Date(iso + 'T00:00:00Z').getUTCDay();
        return wd >= 1 && wd <= 5;
      });
  }

  async function setKind(kind: TimesheetDayKind, leaveTypeID?: string) {
    if (!selected || !selection.length || busy) return;
    const minutes = editHours
      ? Number(editHours.split(':')[0]) * 60 + Number(editHours.split(':')[1])
      : undefined;
    // Overtime only means something on a day the employee was present; leaving
    // the field blank keeps whatever the day already carries.
    const worked = kind === 'WORKED' || kind === 'HALF_DAY' || kind === 'PUBLIC_HOLIDAY';
    const overtime =
      worked && editOvertime
        ? Number(editOvertime.split(':')[0]) * 60 + Number(editOvertime.split(':')[1])
        : undefined;
    busy = true;
    actionError = '';
    msg = '';
    try {
      let last = selected;
      for (const date of selection) {
        last = await hr.upsertTimesheetDay(selected.id, {
          employee_id: activeEmployeeID,
          work_date: date,
          kind,
          minutes: kind === 'WORKED' || kind === 'HALF_DAY' ? minutes : undefined,
          overtime_minutes: overtime,
          explanation: editNote.trim() || undefined,
          leave_type_id: leaveTypeID || undefined
        });
      }
      selected = last;
      periods = periods.map((p) => (p.id === last.id ? { ...p, ...last, days: undefined } : p));
      msg = `${selection.length} gün güncellendi.`;
      selection = [];
      selectedLeaveTypeID = '';
    } catch (cause) {
      actionError = cause instanceof APIRequestError ? cause.message : 'İşlem başarısız.';
    } finally {
      busy = false;
    }
  }

  function applyLeaveType() {
    const type = leaveTypes.find((t) => t.id === selectedLeaveTypeID);
    if (!type) return;
    const kind: TimesheetDayKind =
      type.payroll_treatment === 'UNPAID' ? 'UNPAID_LEAVE' : 'PAID_LEAVE';
    void setKind(kind, type.id);
  }

  async function clearSelection() {
    if (!selected || !selection.length || busy) return;
    busy = true;
    actionError = '';
    msg = '';
    try {
      let last = selected;
      for (const date of selection) {
        const day = last.days?.find(
          (d) => d.work_date === date && d.employee_id === activeEmployeeID
        );
        if (day) last = await hr.deleteTimesheetDay(selected.id, day.id);
      }
      selected = last;
      msg = 'Seçili günler temizlendi.';
      selection = [];
    } catch (cause) {
      actionError = cause instanceof APIRequestError ? cause.message : 'İşlem başarısız.';
    } finally {
      busy = false;
    }
  }

  async function markTurkishHolidays(allEmployees: boolean) {
    if (!selected || busy) return;
    const dates = (TR_PUBLIC_HOLIDAYS[selected.period_year] ?? []).filter(
      (d) => Number(d.slice(5, 7)) === selected!.period_month
    );
    if (!dates.length) {
      actionError = `${selected.period_year} için tanımlı resmî tatil listesi yok.`;
      return;
    }
    const targets = allEmployees ? employees.map((e) => e.id) : [activeEmployeeID];
    busy = true;
    actionError = '';
    msg = '';
    try {
      for (const empID of targets) {
        for (const d of dates) {
          selected = await hr.upsertTimesheetDay(selected!.id, {
            employee_id: empID,
            work_date: d,
            kind: 'PUBLIC_HOLIDAY'
          });
        }
      }
      msg = 'Resmî tatiller işaretlendi.';
    } catch (cause) {
      actionError = cause instanceof APIRequestError ? cause.message : 'İşlem başarısız.';
    } finally {
      busy = false;
    }
  }

  onMount(async () => {
    await loadSession();
    await load();
  });
</script>

<svelte:head><title>Puantaj · Varya One</title></svelte:head>

{#if denied}
  <section class="card">Puantajı görüntüleme yetkiniz yok.</section>
{:else}
  <header class="page-header">
    <div>
      <h1>Puantaj</h1>
    </div>
    <div class="page-actions">
      <Button variant="outline" onclick={() => void load()} disabled={busy}>
        <RefreshCw size={14} aria-hidden="true" />Yenile
      </Button>
    </div>
  </header>

  {#if msg}<p class="notice ok" role="status">{msg}</p>{/if}
  {#if actionError}<p class="notice error" role="alert">{actionError}</p>{/if}
  {#if payrollGaps.length}
    <div class="notice gaps">
      <strong>{payrollGaps.length} çalışanın bordrosu bu haliyle hesaplanamaz.</strong>
      <ul>
        {#each payrollGaps as gap}
          <li>
            <a href={`/personel/calisanlar/${gap.employee_id}`}>{gap.name}</a>
            — {gap.issues[0].message}
          </li>
        {/each}
      </ul>
    </div>
  {/if}
  {#if error}<p class="notice error" role="alert">{error}</p>{/if}

  {#if loading}
    <section class="card"><p class="state">Yükleniyor…</p></section>
  {:else}
    <section class="card period-bar">
      <button class="nav" aria-label="Önceki ay" onclick={() => stepMonth(-1)} disabled={busy}>
        <ChevronLeft size={18} aria-hidden="true" />
      </button>
      <div class="period-title">
        <strong>{periodLabel(viewYear, viewMonth)}</strong>
        {#if selected}
          <Badge tone={statusTone(selected.status)}>{payrollStatusLabel(selected.status)}</Badge>
        {:else}
          <span class="muted">dönem açılmamış</span>
        {/if}
      </div>
      <button class="nav" aria-label="Sonraki ay" onclick={() => stepMonth(1)} disabled={busy}>
        <ChevronRight size={18} aria-hidden="true" />
      </button>

      <span class="spacer"></span>

      {#if !selected && canEdit}
        <Button onclick={createForView} disabled={busy}>Bu ay için dönem aç</Button>
      {/if}
    </section>

    {#if selected}
      <section class="card grid-layout">
        <aside class="emp-panel">
          <input
            class="emp-search"
            placeholder="Çalışan ara…"
            bind:value={empQuery}
            aria-label="Çalışan ara"
          />
          <ul class="emp-list">
            {#each filteredEmployees as e}
              {@const blocker = timesheetBlocker(readinessByID.get(e.id))}
              <li>
                <button
                  class="emp"
                  class:active={activeEmployeeID === e.id}
                  class:blocked={!!blocker}
                  title={blocker}
                  onclick={() => {
                    activeEmployeeID = e.id;
                    selection = [];
                  }}
                >
                  {e.first_name}
                  {e.last_name}
                  {#if blocker}<span class="flag" aria-label="Eksik kayıt">eksik</span>{/if}
                </button>
              </li>
            {/each}
            {#if !filteredEmployees.length}<li class="muted small">Çalışan bulunamadı.</li>{/if}
          </ul>
        </aside>

        <div class="cal-panel">
          <div class="cal-toolbar">
            <span class="cal-title">{activeEmployeeName || 'Çalışan seçin'}</span>
            {#if canPick}
              <div class="cal-tools">
                <Button
                  variant="outline"
                  onclick={() =>
                    run(() => hr.generateTimesheet(selected!.id), 'Plandan yenilendi.')}
                  disabled={busy}>Çalışma planından yenile</Button
                >
                <Button variant="outline" onclick={selectWorkdays} disabled={busy}
                  >Hafta içi günleri seç</Button
                >
                <Button variant="outline" onclick={() => markTurkishHolidays(false)} disabled={busy}
                  >Resmî tatiller (bu çalışan)</Button
                >
                <Button variant="outline" onclick={() => markTurkishHolidays(true)} disabled={busy}
                  >Resmî tatiller (herkes)</Button
                >
              </div>
            {/if}
          </div>

          {#if activeBlocker}
            <p class="notice error blocker" role="alert">
              {activeBlocker}
              <a href={`/personel/calisanlar/${activeEmployeeID}`}>Çalışan kartını aç</a>
            </p>
          {:else if canPick}
            <p class="pick-hint">
              Bir veya birden çok güne tıklayıp seçin, sonra aşağıdan durum atayın.
            </p>
          {/if}

          <div class="weekdays" aria-hidden="true">
            {#each WD as d}<span>{d}</span>{/each}
          </div>
          <div class="calendar">
            {#each calendarCells as c (c.iso)}
              {@const day = c.outside ? undefined : daysByDate.get(c.iso)}
              {@const kind = c.outside ? 'NONE' : dayKindOf(day)}
              <button
                class="cell tone-{dayKindTone(kind)}"
                class:outside={c.outside}
                class:selected={selection.includes(c.iso)}
                class:empty={!c.outside && kind === 'NONE'}
                disabled={c.outside || !canPick}
                onclick={() => toggleDay(c.iso)}
              >
                <span class="num">{c.day}</span>
                {#if !c.outside && kind !== 'NONE'}
                  <span class="tag"
                    >{(kind === 'PAID_LEAVE' || kind === 'UNPAID_LEAVE') && day?.leave_code
                      ? day.leave_name || day.leave_code
                      : dayKindLabel(kind)}</span
                  >
                  {#if day && day.worked_minutes > 0}
                    <span class="hrs">{minutesToHours(day.worked_minutes)} s</span>
                  {/if}
                {/if}
              </button>
            {/each}
          </div>

          {#if selection.length}
            <div class="editor">
              <div class="editor-head">
                <strong>{selection.length} gün seçildi</strong>
                <button class="link" onclick={() => (selection = [])}>Seçimi bırak</button>
              </div>
              <div class="kinds">
                {#each NON_LEAVE_KINDS as k}
                  <button
                    class="kind-btn tone-{dayKindTone(k)}"
                    onclick={() => setKind(k)}
                    disabled={busy}
                  >
                    {dayKindLabel(k)}
                  </button>
                {/each}
              </div>
              {#if leaveTypes.length}
                <div class="leave-picker">
                  <label for="leave-type-select">İzin türü seçerek işaretle</label>
                  <div class="leave-picker-row">
                    <select id="leave-type-select" class="select" bind:value={selectedLeaveTypeID}>
                      <option value="">İzin türü seçin…</option>
                      {#each leaveTypes as lt}
                        <option value={lt.id}>{lt.code} · {lt.name}</option>
                      {/each}
                    </select>
                    <Button
                      variant="outline"
                      onclick={applyLeaveType}
                      disabled={busy || !selectedLeaveTypeID}>İzni uygula</Button
                    >
                  </div>
                </div>
              {/if}
              <div class="editor-row">
                <label>
                  Çalışılan saat (opsiyonel)
                  <TimeInput bind:value={editHours} ariaLabel="Çalışılan saat" />
                </label>
                <label>
                  Fazla mesai (opsiyonel)
                  <TimeInput bind:value={editOvertime} ariaLabel="Fazla mesai saati" />
                </label>
                <label class="grow">
                  Açıklama
                  <input class="note" bind:value={editNote} placeholder="Örn. yarım gün rapor" />
                </label>
              </div>
              <button class="link danger" onclick={clearSelection} disabled={busy}
                >Seçili günleri temizle (kaydı sil)</button
              >
            </div>
          {/if}
        </div>
      </section>

      <section class="card">
        <div class="section-heading">
          <h2>Ay özeti</h2>
          <div class="actions">
            {#if canFinalize && isDraft}
              <Button
                onclick={() =>
                  run(
                    () => hr.finalizeTimesheet(selected!.id, selected!.version),
                    'Puantaj kesinleşti.'
                  )}
                disabled={busy}
              >
                <Check size={14} aria-hidden="true" />Ayı kesinleştir
              </Button>
            {/if}
            {#if canReopen && selected.status === 'FINALIZED'}
              <Button
                variant="outline"
                onclick={() => {
                  reopening = !reopening;
                  reopenReason = '';
                }}
                disabled={busy}>Yeniden aç</Button
              >
            {/if}
          </div>
        </div>

        {#if reopening && selected.status === 'FINALIZED'}
          <form
            class="reopen"
            onsubmit={(e) => {
              e.preventDefault();
              void run(
                () => hr.reopenTimesheet(selected!.id, selected!.version, reopenReason),
                'Puantaj yeniden açıldı.'
              ).then(() => (reopening = false));
            }}
          >
            <input bind:value={reopenReason} placeholder="Yeniden açma gerekçesi" class="note" />
            <Button type="submit" disabled={!reopenReason.trim() || busy}>Onayla</Button>
          </form>
        {/if}

        {#if !summary.length}
          <p class="state">Henüz kayıt yok.</p>
        {:else}
          <div class="scroll">
            <table>
              <thead>
                <tr>
                  <th>Çalışan</th>
                  <th class="numeric">Çalışılan gün</th>
                  <th class="numeric">Ücretli izin</th>
                  <th class="numeric">Ücretsiz izin</th>
                  <th class="numeric">Resmî tatil</th>
                  <th class="numeric">Devamsız</th>
                </tr>
              </thead>
              <tbody>
                {#each summary as r}
                  <tr>
                    <td>{r.name}</td>
                    <td class="numeric">{r.worked}</td>
                    <td class="numeric">{r.paidLeave}</td>
                    <td class="numeric">{r.unpaidLeave}</td>
                    <td class="numeric">{r.holiday}</td>
                    <td class="numeric">{r.absent}</td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
        {/if}
      </section>
    {/if}
  {/if}
{/if}

<style>
  .emp.blocked {
    color: var(--text-muted);
    text-decoration: line-through;
  }
  .flag {
    margin-left: 6px;
    font-size: 10px;
    text-decoration: none;
    color: var(--danger, #b42318);
  }
  .blocker a {
    margin-left: 8px;
  }
  .gaps ul {
    margin: 6px 0 0;
    padding-left: 18px;
    font-size: 12px;
  }

  .card {
    padding: 16px;
    margin-top: 14px;
  }
  .card h2 {
    margin: 0;
    font-size: 15px;
  }
  .state {
    padding: 14px 0;
    text-align: center;
    color: var(--text-muted);
    font-size: 13px;
    margin: 0;
  }
  .muted {
    color: var(--text-muted);
  }
  .small {
    font-size: 12px;
  }

  .period-bar {
    display: flex;
    align-items: center;
    gap: 10px;
  }
  .period-bar .spacer {
    flex: 1;
  }
  .period-title {
    display: flex;
    align-items: center;
    gap: 8px;
    min-width: 190px;
    justify-content: center;
  }
  .period-title strong {
    font-size: 15px;
  }
  .nav {
    display: grid;
    place-items: center;
    width: 32px;
    height: 32px;
    padding: 0;
    overflow: hidden;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    background: var(--surface);
    color: var(--text-subtle);
    cursor: pointer;
    flex-shrink: 0;
  }
  .nav :global(svg) {
    display: block;
  }
  .nav:hover:not(:disabled) {
    color: var(--primary);
    border-color: var(--primary);
  }
  .nav:disabled {
    opacity: 0.5;
    cursor: default;
  }

  .grid-layout {
    display: grid;
    grid-template-columns: 220px 1fr;
    gap: 16px;
  }
  @media (max-width: 820px) {
    .grid-layout {
      grid-template-columns: 1fr;
    }
  }
  .emp-panel {
    display: flex;
    flex-direction: column;
    gap: 8px;
    min-width: 0;
  }
  .emp-search,
  .note {
    height: var(--control-height, 34px);
    width: 100%;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    background: var(--surface);
    color: var(--text);
    padding: 0 9px;
    font-size: 12px;
  }
  .emp-list {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 2px;
    max-height: 460px;
    overflow-y: auto;
  }
  .emp {
    width: 100%;
    text-align: left;
    padding: 7px 9px;
    border: 1px solid transparent;
    border-radius: var(--radius-control);
    background: transparent;
    color: var(--text);
    font-size: 12px;
    cursor: pointer;
  }
  .emp:hover {
    background: var(--surface-muted);
  }
  .emp.active {
    background: color-mix(in srgb, var(--primary) 12%, var(--surface));
    border-color: var(--primary);
    font-weight: 650;
  }

  .cal-panel {
    min-width: 0;
  }
  .cal-toolbar {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 10px;
    flex-wrap: wrap;
    margin-bottom: 8px;
  }
  .cal-title {
    font-size: 14px;
    font-weight: 700;
  }
  .cal-tools {
    display: flex;
    gap: 6px;
    flex-wrap: wrap;
  }
  .pick-hint {
    margin: 0 0 10px;
    font-size: 12px;
    color: var(--text-muted);
  }
  .weekdays {
    display: grid;
    grid-template-columns: repeat(7, 1fr);
    gap: 4px;
    margin-bottom: 4px;
    font-size: 10px;
    font-weight: 700;
    text-transform: uppercase;
    color: var(--text-muted);
    text-align: center;
  }
  .calendar {
    display: grid;
    grid-template-columns: repeat(7, 1fr);
    gap: 4px;
  }
  .cell {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: 2px;
    min-height: 62px;
    padding: 5px 6px;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    background: var(--surface);
    color: var(--text);
    cursor: pointer;
    text-align: left;
  }
  .cell:disabled {
    cursor: default;
  }
  .cell.outside {
    background: var(--surface-muted);
    opacity: 0.4;
  }
  .cell.empty {
    border-style: dashed;
    color: var(--text-muted);
  }
  .cell.selected {
    outline: 2px solid var(--primary);
    outline-offset: 1px;
    border-color: var(--primary);
  }
  .cell:not(:disabled):hover {
    border-color: var(--primary);
  }
  .cell .num {
    font-size: 11px;
    font-weight: 700;
    color: var(--text-muted);
  }
  .cell .tag {
    font-size: 10px;
    font-weight: 650;
    line-height: 1.2;
  }
  .cell .hrs {
    font-size: 10px;
    color: var(--text-muted);
  }
  .tone-success {
    background: color-mix(in srgb, var(--success) 12%, var(--surface));
  }
  .tone-info {
    background: color-mix(in srgb, var(--info, var(--primary)) 12%, var(--surface));
  }
  .tone-warning {
    background: color-mix(in srgb, var(--warning) 15%, var(--surface));
  }
  .tone-danger {
    background: color-mix(in srgb, var(--danger) 13%, var(--surface));
  }

  .editor {
    margin-top: 12px;
    padding: 12px;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-card);
    background: var(--surface);
  }
  .editor-head {
    display: flex;
    justify-content: space-between;
    align-items: center;
    margin-bottom: 8px;
  }
  .kinds {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    margin-bottom: 10px;
  }
  .leave-picker {
    margin-bottom: 12px;
  }
  .leave-picker label {
    display: block;
    font-size: 11px;
    color: var(--text-muted);
    margin-bottom: 4px;
  }
  .leave-picker-row {
    display: flex;
    gap: 8px;
    flex-wrap: wrap;
  }
  .select {
    height: var(--control-height, 34px);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    background: var(--surface);
    color: var(--text);
    padding: 0 9px;
    font-size: 12px;
    min-width: 220px;
  }
  .kind-btn {
    padding: 6px 10px;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    color: var(--text);
    font-size: 12px;
    cursor: pointer;
  }
  .kind-btn:hover {
    border-color: var(--primary);
  }
  .editor-row {
    display: flex;
    gap: 12px;
    flex-wrap: wrap;
  }
  .editor-row label {
    display: flex;
    flex-direction: column;
    gap: 4px;
    font-size: 11px;
    color: var(--text-muted);
  }
  .editor-row label.grow {
    flex: 1;
    min-width: 180px;
  }
  .link {
    border: 0;
    background: transparent;
    color: var(--primary);
    cursor: pointer;
    font-size: 12px;
    padding: 0;
  }
  .link.danger {
    color: var(--danger);
    margin-top: 10px;
  }

  .section-heading {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 10px;
    flex-wrap: wrap;
    margin-bottom: 10px;
  }
  .actions {
    display: flex;
    gap: 8px;
    flex-wrap: wrap;
  }
  .reopen {
    display: flex;
    gap: 8px;
    margin-bottom: 12px;
    max-width: 420px;
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
</style>
