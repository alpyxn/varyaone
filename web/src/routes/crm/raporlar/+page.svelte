<script lang="ts">
  import { DateInput } from '$lib/components/varya/date-input';
  import { onMount } from 'svelte';
  import { RefreshCw } from '@lucide/svelte';
  import { api, APIRequestError, type Session } from '$lib/api';
  import { Button } from '$lib/components/ui/button';
  import { formatMoney } from '$lib/design/formatters';
  import { getReport, listCRMMembers } from '$lib/features/crm/api';
  import {
    callOutcomeLabel,
    formatDuration,
    leadStatusLabel,
    percent,
    stageKindLabel,
    type Member,
    type Report
  } from '$lib/features/crm/types';

  let session = $state<Session | null>(null);
  let denied = $state(false);
  let loading = $state(true);
  let error = $state('');
  let report = $state<Report | null>(null);
  let members = $state<Member[]>([]);

  const isoDay = (d: Date) =>
    `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
  const today = new Date();
  let to = $state(isoDay(today));
  let from = $state(isoDay(new Date(today.getFullYear(), today.getMonth(), today.getDate() - 29)));
  let userID = $state('');

  const canSeeAll = $derived(session?.permissions.includes('crm.view_all') ?? false);

  const totals = $derived.by(() => {
    const reps = report?.reps ?? [];
    return {
      calls: reps.reduce((n, r) => n + r.calls, 0),
      reached: reps.reduce((n, r) => n + r.reached, 0),
      positive: reps.reduce((n, r) => n + r.positive, 0),
      meetings: reps.reduce((n, r) => n + r.meetings, 0),
      talk: reps.reduce((n, r) => n + r.talk_seconds, 0),
      leads: reps.reduce((n, r) => n + r.leads_created, 0)
    };
  });

  // Every calendar day in the range, so quiet days show as empty bars.
  const days = $derived.by(() => {
    if (!report) return [];
    const byDay = new Map(report.days.map((d) => [d.day, d]));
    const out: { day: string; calls: number; reached: number }[] = [];
    const cursor = new Date(`${from}T00:00:00`);
    const end = new Date(`${to}T00:00:00`);
    while (cursor <= end && out.length < 400) {
      const key = isoDay(cursor);
      out.push(byDay.get(key) ?? { day: key, calls: 0, reached: 0 });
      cursor.setDate(cursor.getDate() + 1);
    }
    return out;
  });
  const maxDay = $derived(Math.max(1, ...days.map((d) => d.calls)));
  const maxOutcome = $derived(Math.max(1, ...(report?.outcomes ?? []).map((o) => o.count)));

  async function load() {
    loading = true;
    error = '';
    try {
      report = await getReport({ from, to, user_id: userID || undefined });
    } catch (cause) {
      error = cause instanceof APIRequestError ? cause.message : 'Rapor yüklenemedi.';
    } finally {
      loading = false;
    }
  }

  function shortDay(day: string) {
    const [, m, d] = day.split('-');
    return `${d}.${m}`;
  }

  onMount(async () => {
    try {
      session = await api<Session>('/session');
      denied = !session.permissions.includes('crm.read');
    } catch {
      denied = true;
    }
    if (denied) {
      loading = false;
      return;
    }
    if (session?.permissions.includes('crm.view_all')) {
      listCRMMembers()
        .then((r) => (members = r.items))
        .catch(() => (members = []));
    }
    await load();
  });
</script>

<svelte:head><title>CRM Raporları · Varya One</title></svelte:head>

{#if denied}
  <section class="panel error-panel" role="alert">
    <strong>Erişim yok.</strong>
    <span>CRM yetkiniz bulunmuyor.</span>
  </section>
{:else}
  <header class="page-header">
    <div>
      <h1>CRM Raporları</h1>
      <p class="meta">
        {canSeeAll ? 'Ekibin' : 'Sizin'} arama performansı ve satış hattı.
      </p>
    </div>
  </header>

  <form
    class="panel filters"
    onsubmit={(e) => {
      e.preventDefault();
      void load();
    }}
  >
    <label>Başlangıç <DateInput bind:value={from} ariaLabel="Başlangıç tarihi" /></label>
    <label>Bitiş <DateInput bind:value={to} ariaLabel="Bitiş tarihi" /></label>
    {#if canSeeAll}
      <label>
        Temsilci
        <select class="select" bind:value={userID}>
          <option value="">Tüm ekip</option>
          {#each members as member (member.user_id)}
            <option value={member.user_id}>{member.display_name}</option>
          {/each}
        </select>
      </label>
    {/if}
    <Button type="submit" variant="outline" disabled={loading}>
      <RefreshCw size={14} data-icon="inline-start" aria-hidden="true" />Göster
    </Button>
  </form>

  {#if error}<p class="notice error" role="alert">{error}</p>{/if}

  {#if loading && !report}
    <p class="state" role="status">Yükleniyor…</p>
  {:else if report}
    <section class="tiles" aria-label="Özet">
      <div class="tile panel">
        <span>Arama</span><strong>{totals.calls}</strong>
      </div>
      <div class="tile panel">
        <span>Ulaşılan</span><strong>{totals.reached}</strong>
        <small>{percent(totals.reached, totals.calls)} ulaşma</small>
      </div>
      <div class="tile panel">
        <span>İlgilenen</span><strong>{totals.positive}</strong>
        <small>{percent(totals.positive, totals.reached)} ulaşılanların</small>
      </div>
      <div class="tile panel">
        <span>Toplantı</span><strong>{totals.meetings}</strong>
      </div>
      <div class="tile panel">
        <span>Konuşma süresi</span><strong>{formatDuration(totals.talk) || '0:00'}</strong>
      </div>
      <div class="tile panel">
        <span>Kazanılan</span>
        {#if report.won.length}
          {#each report.won as won (won.currency)}
            <strong>{formatMoney(won.amount, won.currency)}</strong>
          {/each}
          <small>{report.won.reduce((n, w) => n + w.count, 0)} fırsat</small>
        {:else}<strong>—</strong>{/if}
      </div>
    </section>

    <section class="panel" aria-labelledby="daily-title">
      <h2 id="daily-title">Günlük aramalar</h2>
      {#if totals.calls === 0}
        <p class="state">Bu aralıkta arama yok.</p>
      {:else}
        <div class="bars" role="img" aria-label="Günlük arama sayıları; ayrıntı aşağıdaki tabloda">
          {#each days as day (day.day)}
            <div
              class="bar-slot"
              title="{shortDay(day.day)}: {day.calls} arama, {day.reached} ulaşılan"
            >
              <div class="bar" style="height: {(day.calls / maxDay) * 100}%"></div>
            </div>
          {/each}
        </div>
        <div class="bar-axis">
          <span>{shortDay(days[0]?.day ?? from)}</span>
          <span>en yüksek {maxDay}</span>
          <span>{shortDay(days[days.length - 1]?.day ?? to)}</span>
        </div>
      {/if}
    </section>

    <div class="two">
      <section class="panel" aria-labelledby="outcome-title">
        <h2 id="outcome-title">Arama sonuçları</h2>
        {#if !report.outcomes.length}
          <p class="state">Veri yok.</p>
        {:else}
          <ul class="hbars">
            {#each report.outcomes as row (row.key)}
              <li>
                <span class="label">{callOutcomeLabel(row.key)}</span>
                <span class="track"
                  ><span class="fill" style="width: {(row.count / maxOutcome) * 100}%"></span></span
                >
                <span class="value">{row.count} · {percent(row.count, totals.calls)}</span>
              </li>
            {/each}
          </ul>
        {/if}
      </section>

      <section class="panel" aria-labelledby="funnel-title">
        <h2 id="funnel-title">Aday durumları</h2>
        <p class="hint">
          Şu anki aday havuzu{canSeeAll && !userID ? '' : ' (sorumlu olduklarınız)'}.
        </p>
        <div class="table-scroll">
          <table>
            <tbody>
              {#each ['NEW', 'CONTACTED', 'QUALIFIED', 'CONVERTED', 'DISQUALIFIED'] as status (status)}
                <tr>
                  <td>{leadStatusLabel(status)}</td>
                  <td class="num">{report.lead_status.find((r) => r.key === status)?.count ?? 0}</td
                  >
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
        {#if report.sources.length}
          <h3>Bu aralıkta eklenen adayların kaynağı</h3>
          <div class="table-scroll">
            <table>
              <tbody>
                {#each report.sources as row (row.key)}
                  <tr><td>{row.key}</td><td class="num">{row.count}</td></tr>
                {/each}
              </tbody>
            </table>
          </div>
        {/if}
      </section>
    </div>

    <section class="panel" aria-labelledby="reps-title">
      <h2 id="reps-title">Temsilciler</h2>
      {#if !report.reps.length}
        <p class="state">Bu aralıkta kayıt yok.</p>
      {:else}
        <!-- svelte-ignore a11y_no_noninteractive_tabindex (a scrollable region must be reachable by keyboard) -->
        <div class="table-scroll" tabindex="0" role="region" aria-label="Temsilci tablosu">
          <table>
            <thead>
              <tr>
                <th>Temsilci</th><th class="num">Arama</th><th class="num">Ulaşılan</th>
                <th class="num">Ulaşma</th><th class="num">İlgilenen</th><th class="num"
                  >Toplantı</th
                >
                <th class="num">Konuşma</th><th class="num">Yeni aday</th><th class="num"
                  >Kazanılan</th
                >
              </tr>
            </thead>
            <tbody>
              {#each report.reps as rep (rep.user_id)}
                <tr>
                  <td>{rep.name}</td>
                  <td class="num">{rep.calls}</td>
                  <td class="num">{rep.reached}</td>
                  <td class="num">{percent(rep.reached, rep.calls)}</td>
                  <td class="num">{rep.positive}</td>
                  <td class="num">{rep.meetings}</td>
                  <td class="num">{formatDuration(rep.talk_seconds)}</td>
                  <td class="num">{rep.leads_created}</td>
                  <td class="num">{rep.won}</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {/if}
    </section>

    <div class="two">
      <section class="panel" aria-labelledby="pipeline-title">
        <h2 id="pipeline-title">Satış hattı</h2>
        <p class="hint">
          Açık aşamalar bugünkü durumu, kapanan aşamalar seçili aralıkta kapananları gösterir.
          Beklenen = tutar × olasılık.
        </p>
        <div class="table-scroll">
          <table>
            <thead>
              <tr
                ><th>Aşama</th><th class="num">Fırsat</th><th class="num">Tutar</th><th class="num"
                  >Beklenen</th
                ></tr
              >
            </thead>
            <tbody>
              {#each report.pipeline as row (row.stage_id + row.currency)}
                <tr>
                  <td>
                    {row.name}
                    {#if row.kind !== 'OPEN'}<span class="muted">({stageKindLabel(row.kind)})</span
                      >{/if}
                  </td>
                  <td class="num">{row.count}</td>
                  <td class="num">{row.currency ? formatMoney(row.amount, row.currency) : '—'}</td>
                  <td class="num">
                    {row.currency && row.kind === 'OPEN'
                      ? formatMoney(row.weighted, row.currency)
                      : '—'}
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      </section>

      <section class="panel" aria-labelledby="lost-title">
        <h2 id="lost-title">Kayıp nedenleri</h2>
        {#if !report.lost_reasons.length}
          <p class="state">Bu aralıkta kaybedilen fırsat yok.</p>
        {:else}
          <div class="table-scroll">
            <table>
              <tbody>
                {#each report.lost_reasons as row (row.key)}
                  <tr><td>{row.key}</td><td class="num">{row.count}</td></tr>
                {/each}
              </tbody>
            </table>
          </div>
        {/if}
      </section>
    </div>

    <details class="panel data">
      <summary>Günlük aramalar tablosu</summary>
      <div class="table-scroll">
        <table>
          <thead><tr><th>Gün</th><th class="num">Arama</th><th class="num">Ulaşılan</th></tr></thead
          >
          <tbody>
            {#each days.filter((d) => d.calls) as day (day.day)}
              <tr
                ><td>{shortDay(day.day)}</td><td class="num">{day.calls}</td><td class="num"
                  >{day.reached}</td
                ></tr
              >
            {/each}
          </tbody>
        </table>
      </div>
    </details>
  {/if}
{/if}

<style>
  .page-header {
    display: flex;
    justify-content: space-between;
    gap: 12px;
    flex-wrap: wrap;
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
    margin: 0 0 10px;
    font-size: 14px;
  }
  .panel h3 {
    margin: 14px 0 6px;
    font-size: 12px;
    color: var(--text-muted);
  }
  .filters {
    display: flex;
    align-items: flex-end;
    gap: 10px;
    flex-wrap: wrap;
  }
  .filters label {
    display: grid;
    gap: 4px;
    font-size: 12px;
    color: var(--text-muted);
  }
  .select {
    height: var(--control-height);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    background: var(--surface);
    color: var(--text);
    padding: 0 10px;
    font-size: 13px;
  }
  .tiles {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(min(100%, 160px), 1fr));
    gap: 12px;
  }
  .tile {
    display: grid;
    gap: 2px;
  }
  .tile span {
    color: var(--text-muted);
    font-size: 12px;
  }
  .tile strong {
    font-size: 22px;
    font-variant-numeric: tabular-nums;
    color: var(--text);
  }
  .tile small {
    color: var(--text-muted);
    font-size: 11px;
  }
  .bars {
    display: flex;
    align-items: flex-end;
    gap: 2px;
    height: 160px;
    border-bottom: 1px solid var(--border);
  }
  .bar-slot {
    flex: 1 1 0;
    height: 100%;
    display: flex;
    align-items: flex-end;
    min-width: 2px;
  }
  .bar-slot:hover .bar {
    opacity: 0.75;
  }
  .bar {
    width: 100%;
    min-height: 0;
    border-radius: 4px 4px 0 0;
    background: var(--primary);
  }
  .bar-axis {
    display: flex;
    justify-content: space-between;
    margin-top: 4px;
    color: var(--text-muted);
    font-size: 11px;
  }
  .two {
    display: grid;
    grid-template-columns: minmax(0, 1fr);
    gap: 0 16px;
  }
  @media (min-width: 900px) {
    .two {
      grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
    }
  }
  .hbars {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    gap: 8px;
  }
  .hbars li {
    display: grid;
    grid-template-columns: 120px minmax(0, 1fr) 80px;
    align-items: center;
    gap: 8px;
    font-size: 12px;
  }
  .track {
    height: 10px;
    border-radius: 999px;
    background: color-mix(in srgb, var(--text-muted) 12%, transparent);
    overflow: hidden;
  }
  .fill {
    display: block;
    height: 100%;
    border-radius: 999px;
    background: var(--primary);
  }
  .value {
    text-align: right;
    color: var(--text-muted);
    font-variant-numeric: tabular-nums;
  }
  .hint {
    margin: 0 0 8px;
    color: var(--text-muted);
    font-size: 12px;
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
    padding: 7px 8px;
    border-bottom: 1px solid var(--border);
    text-align: left;
    white-space: nowrap;
  }
  th {
    color: var(--text-muted);
    font-weight: 650;
  }
  .num {
    text-align: right;
    font-variant-numeric: tabular-nums;
  }
  .muted {
    color: var(--text-muted);
  }
  .data summary {
    cursor: pointer;
    font-size: 13px;
    font-weight: 600;
  }
  .state {
    margin: 0;
    padding: 12px 0;
    color: var(--text-muted);
    font-size: 13px;
    text-align: center;
  }
</style>
