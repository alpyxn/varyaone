<script lang="ts">
  import ListFilters from '$lib/components/varya/list-filters/ListFilters.svelte';
  import type { ListFilter } from '$lib/components/varya/list-filters/types';
  import { readListState, writeListState } from '$lib/components/varya/list-filters/state';
  import type { PaymentPlanSummary } from '$lib/features/finance/payment-plans';
  import { onMount, tick } from 'svelte';
  import {
    CalendarDays,
    Plus,
    RefreshCw,
    ArrowDownLeft,
    ArrowUpRight,
    FileText,
    Check
  } from '@lucide/svelte';
  import { page } from '$app/state';
  import { api, type Session } from '$lib/api';
  import { errorMessage } from '$lib/errors';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { DateInput } from '$lib/components/varya/date-input';
  import { CurrencySelect } from '$lib/components/varya/currency-select';
  import { EntityCombobox } from '$lib/components/varya/entity-combobox';
  import type { EntityOption } from '$lib/components/varya/entity-picker-dialog/types';
  import ReasonDialog from '$lib/components/varya/reason-dialog/ReasonDialog.svelte';
  import { getParty, listParties } from '$lib/features/parties/api';
  import { trimDecimalZeros } from '$lib/design/decimal';
  import { formatDate, formatMoney } from '$lib/design/formatters';
  import { downloadXls } from '$lib/design/spreadsheet';
  import {
    equalInstallments,
    planUnits,
    planAmount,
    partialPlanAllocations,
    type InstallmentDraft,
    type PaymentPlan,
    type PlanInstallment
  } from '$lib/features/finance/payment-plans';

  type OpenItem = {
    id: string;
    document_id: string;
    document_no: string;
    open_amount: string;
    currency: string;
    due_date?: string;
  };
  type Account = {
    id: string;
    name: string;
    code: string;
    currency: string;
    account_type: 'CASH' | 'BANK';
    is_active: boolean;
  };
  let session = $state<Session>();
  let side = $state<'RECEIVABLE' | 'PAYABLE'>('RECEIVABLE');
  let party = $state<EntityOption | null>(null);
  let currency = $state('TRY');
  let plans = $state<PaymentPlanSummary[]>([]);
  let search = $state('');
  let listValues = $state<Record<string, string>>({ status: 'OPEN' });
  let sort = $state('next_due:asc');
  let cursor = $state('');
  let nextCursor = $state('');
  let cursorHistory = $state<string[]>([]);
  let detailLoading = $state(false);
  let detailRequest: AbortController | undefined;
  let selectedID = $state('');
  let searchTimer: ReturnType<typeof setTimeout>;
  const planFilters: ListFilter[] = [
    {
      field: 'status',
      label: 'Plan durumu',
      kind: 'select',
      options: [
        { value: 'OPEN', label: 'Açık' },
        { value: 'CLOSED', label: 'Kapandı' },
        { value: 'CANCELLED', label: 'İptal' }
      ]
    },
    { field: 'due_from', label: 'Açık taksit vadesi başlangıç', kind: 'date' },
    { field: 'due_to', label: 'Açık taksit vadesi bitiş', kind: 'date' },
    { field: 'currency', label: 'Para birimi', kind: 'currency' },
    {
      field: 'preset',
      label: 'Vade seçimi',
      kind: 'select',
      options: [
        { value: 'OVERDUE', label: 'Gecikenler' },
        { value: 'TODAY', label: 'Bugün' },
        { value: 'NEXT_7_DAYS', label: 'Önümüzdeki 7 gün' }
      ]
    }
  ];
  function remember() {
    if (session)
      writeListState(session, 'payment-plans', {
        search,
        listValues,
        sort,
        cursor,
        cursorHistory,
        side,
        party,
        selectedID,
        day: new Date().toISOString().slice(0, 10)
      });
  }
  function resetList() {
    clearTimeout(searchTimer);
    cursor = '';
    cursorHistory = [];
    void load();
  }
  function setListFilter(field: string, value: string) {
    listValues = { ...listValues, [field]: value };
    resetList();
  }
  function quickFilter(preset: string) {
    listValues = { ...listValues, status: 'OPEN', preset, due_from: '', due_to: '' };
    resetList();
  }
  async function selectPlan(id: string) {
    detailRequest?.abort();
    selectedID = id;
    selectedPlan = undefined;
    payment = undefined;
    remember();
    if (!id) {
      detailLoading = false;
      return;
    }
    const current = new AbortController();
    detailRequest = current;
    detailLoading = true;
    try {
      const result = await api<PaymentPlan>(`/finance/payment-plans/${id}`, {
        signal: current.signal
      });
      if (!current.signal.aborted) selectedPlan = result;
    } catch (e) {
      if (!current.signal.aborted) error = errorMessage(e, 'Plan okunamadı.');
    } finally {
      if (detailRequest === current) detailLoading = false;
    }
  }
  let selectedPlan = $state<PaymentPlan>();
  let loading = $state(true);
  let error = $state('');
  let message = $state('');
  let busy = $state(false);
  let creating = $state(false);
  let items = $state<OpenItem[]>([]);
  let selectedIDs = $state<string[]>([]);
  let description = $state('');
  let count = $state(3);
  let firstDate = $state(new Date().toISOString().slice(0, 10));
  let installments = $state<InstallmentDraft[]>([]);
  let createKey = $state('');
  let cancelOpen = $state(false);
  let payment = $state<PlanInstallment>();
  let accounts = $state<Account[]>([]);
  let accountID = $state('');
  let accountSelect = $state<HTMLSelectElement>();
  let paymentAmount = $state('');
  let paymentDate = $state(new Date().toISOString().slice(0, 10));
  let exchangeRate = $state('');
  let paymentKey = $state('');
  let paymentError = $state('');
  let request: AbortController | undefined;
  const canManage = $derived(session?.permissions.includes('finance.allocation.manage') ?? false);
  const canPay = $derived(
    session?.permissions.includes(
      side === 'RECEIVABLE' ? 'finance.collection.post' : 'finance.payment.post'
    ) ?? false
  );
  const total = $derived(
    planAmount(
      items
        .filter((i) => selectedIDs.includes(i.id))
        .reduce((sum, i) => sum + planUnits(i.open_amount), 0n)
    )
  );
  const totalDraft = $derived.by(() => {
    try {
      return planAmount(installments.reduce((sum, i) => sum + planUnits(i.amount), 0n));
    } catch {
      return '';
    }
  });
  const baseCurrency = $derived(
    session?.companies.find((c) => c.id === session?.current_company_id)?.base_currency
  );
  const needsExchangeRate = $derived(
    !!baseCurrency && !!selectedPlan && selectedPlan.currency !== baseCurrency
  );
  const planRemaining = $derived(
    planAmount(
      (selectedPlan?.installments ?? []).reduce((sum, row) => sum + planUnits(row.open_amount), 0n)
    )
  );
  const planClosed = $derived(
    planAmount(
      (selectedPlan?.installments ?? []).reduce(
        (sum, row) => sum + planUnits(row.closed_amount),
        0n
      )
    )
  );
  const closedCount = $derived(
    selectedPlan?.installments.filter((row) => row.status === 'CLOSED').length ?? 0
  );
  const nextInstallment = $derived(
    selectedPlan?.installments.find((row) => planUnits(row.open_amount) > 0n)
  );
  const firstOpen = $derived(
    selectedPlan?.installments.find((i) => planUnits(i.open_amount) > 0n)?.number
  );
  const statusLabels: Record<string, string> = {
    PENDING: 'Bekliyor',
    PARTIAL: 'Kısmen kapandı',
    OVERDUE: 'Gecikti',
    CLOSED: 'Kapandı',
    CANCELLED: 'İptal'
  };

  async function searchParties(q: string, signal: AbortSignal) {
    const result = await listParties(new URLSearchParams({ q, limit: '50' }), signal);
    return result.items.map((p) => ({ id: p.id, title: p.display_name, subtitle: p.code }));
  }
  async function load() {
    request?.abort();
    const current = new AbortController();
    request = current;
    loading = true;
    error = '';
    creating = false;
    payment = undefined;
    try {
      const params = new URLSearchParams({ side, q: search, sort, limit: '50', cursor });
      for (const [key, value] of Object.entries(listValues)) if (value) params.set(key, value);
      if (party) params.set('party_id', party.id);
      const result = await api<{ items: PaymentPlanSummary[]; next_cursor?: string }>(
        `/finance/payment-plans?${params}`,
        {
          signal: current.signal
        }
      );
      if (current.signal.aborted) return;
      plans = result.items;
      nextCursor = result.next_cursor ?? '';
      await selectPlan(
        plans.find((p) => p.id === (selectedPlan?.id || selectedID))?.id ?? plans[0]?.id ?? ''
      );
      remember();
    } catch (e) {
      if (!current.signal.aborted) error = errorMessage(e, 'Planlar okunamadı.');
    } finally {
      if (request === current) loading = false;
    }
  }
  async function startPlan(documentID = '') {
    if (!party || !canManage) return;
    busy = true;
    error = '';
    payment = undefined;
    try {
      const loaded: OpenItem[] = [];
      let cursor = '';
      do {
        const params = new URLSearchParams({
          party_id: party.id,
          side,
          currency,
          limit: '500',
          unplanned_only: 'true'
        });
        if (cursor) params.set('cursor', cursor);
        const result = await api<{ items: OpenItem[]; next_cursor?: string }>(
          `/invoice-open-items?${params}`
        );
        loaded.push(...result.items);
        cursor = result.next_cursor ?? '';
      } while (cursor);
      items = loaded;
      selectedIDs = documentID
        ? items.filter((i) => i.document_id === documentID).map((i) => i.id)
        : [];
      installments = [];
      description = `${party.title} ${side === 'RECEIVABLE' ? 'tahsilat' : 'ödeme'} planı`;
      createKey = crypto.randomUUID();
      creating = true;
      if (documentID && !selectedIDs.length)
        message =
          'Bu belgenin açık tutarı yok veya aktif bir planı var. Mevcut planları kontrol edin.';
    } catch (e) {
      error = errorMessage(e, 'Açık belgeler okunamadı.');
    } finally {
      busy = false;
    }
  }
  function generate() {
    try {
      installments = equalInstallments(total, count, firstDate);
      error = '';
    } catch (e) {
      error = errorMessage(e);
    }
  }
  async function savePlan() {
    if (!party || busy) return;
    busy = true;
    error = '';
    message = '';
    try {
      if (!installments.length || totalDraft !== total)
        throw new Error('Taksit toplamı seçilen belgelerin kalan tutarına eşit olmalıdır.');
      const result = await api<PaymentPlan>('/finance/payment-plans', {
        method: 'POST',
        headers: { 'Idempotency-Key': createKey },
        body: JSON.stringify({
          party_id: party.id,
          side,
          currency,
          description,
          open_item_ids: selectedIDs,
          installments: installments.map((i) => ({ ...i, amount: planAmount(planUnits(i.amount)) }))
        })
      });
      selectedPlan = result;
      selectedID = result.id;
      cursor = '';
      cursorHistory = [];
      listValues = { ...listValues, status: 'OPEN', preset: '', due_from: '', due_to: '' };
      sort = 'created_at:desc';
      search = '';
      await load();
      message = 'Vade planı oluşturuldu. Cari bakiyesi değişmedi.';
    } catch (e) {
      error = errorMessage(e, 'Plan oluşturulamadı.');
    } finally {
      busy = false;
    }
  }
  async function cancelPlan(reason: string) {
    if (!selectedPlan) return;
    await api(`/finance/payment-plans/${selectedPlan.id}/cancel`, {
      method: 'POST',
      headers: { 'If-Match': `"${selectedPlan.version}"` },
      body: JSON.stringify({ reason })
    });
    await load();
    message = 'Plan iptal edildi. Kalan tutarlar belgelerin asıl vadelerinden takip ediliyor.';
  }
  async function openPayment(row: PlanInstallment) {
    if (!selectedPlan) return;
    busy = true;
    paymentError = '';
    try {
      const result = await api<{ items: Account[] }>('/finance/accounts');
      accounts = result.items.filter(
        (a) =>
          a.is_active &&
          a.currency === selectedPlan?.currency &&
          ['CASH', 'BANK'].includes(a.account_type)
      );
      accountID = '';
      paymentAmount = trimDecimalZeros(row.open_amount);
      paymentDate = new Date().toISOString().slice(0, 10);
      exchangeRate = '';
      paymentKey = crypto.randomUUID();
      payment = row;
    } catch (e) {
      error = errorMessage(e, 'Hesaplar okunamadı.');
    } finally {
      busy = false;
    }
    await tick();
    accountSelect?.focus();
  }
  async function postPayment() {
    if (!payment || !selectedPlan || busy) return;
    busy = true;
    paymentError = '';
    try {
      const account = accounts.find((a) => a.id === accountID);
      if (!account) throw new Error('Kasa veya banka hesabını seçin.');
      const allocations = partialPlanAllocations(payment.allocations, paymentAmount);
      const result = await api<{ id: string; document_no: string }>(
        side === 'RECEIVABLE' ? '/finance/collections' : '/finance/payments',
        {
          method: 'POST',
          headers: { 'Idempotency-Key': paymentKey },
          body: JSON.stringify({
            party_id: selectedPlan.party_id,
            account_id: accountID,
            payment_method: account.account_type,
            currency: selectedPlan.currency,
            amount: planAmount(planUnits(paymentAmount)),
            exchange_rate: needsExchangeRate ? exchangeRate || undefined : undefined,
            description: `${selectedPlan.description} — ${payment.number}. taksit`,
            transaction_date: `${paymentDate}T00:00:00Z`,
            allocations,
            payment_plan_id: selectedPlan.id,
            installment_no: payment.number
          })
        }
      );
      await load();
      message = `${result.document_no} kaydedildi. Taksit ve belge bakiyeleri güncellendi.`;
    } catch (e) {
      paymentError = errorMessage(e, 'Ödeme kaydedilemedi.');
    } finally {
      busy = false;
    }
  }
  function exportPlan() {
    if (!selectedPlan) return;
    downloadXls(
      'taksit-plani',
      'Taksit Planı',
      selectedPlan.installments.map((i) => [
        String(i.number),
        i.due_date,
        selectedPlan!.currency,
        trimDecimalZeros(i.amount),
        trimDecimalZeros(i.closed_amount),
        trimDecimalZeros(i.open_amount),
        statusLabels[i.status]
      ]),
      ['Taksit', 'Vade', 'Para Birimi', 'Planlanan', 'Kapanan', 'Kalan', 'Durum']
    );
  }
  onMount(() => {
    let alive = true;
    void (async () => {
      try {
        session = await api<Session>('/session');
        if (!alive) return;
        const saved = readListState<{
          search: string;
          listValues: Record<string, string>;
          sort: string;
          cursor: string;
          cursorHistory: string[];
          side: 'RECEIVABLE' | 'PAYABLE';
          party: EntityOption | null;
          selectedID: string;
          day: string;
        }>(session, 'payment-plans');
        if (saved && !page.url.searchParams.has('party_id') && !page.url.searchParams.has('side')) {
          search = saved.search ?? '';
          listValues = saved.listValues ?? { status: 'OPEN' };
          sort = saved.sort ?? 'next_due:asc';
          cursor = saved.day === new Date().toISOString().slice(0, 10) ? (saved.cursor ?? '') : '';
          cursorHistory = cursor ? (saved.cursorHistory ?? []) : [];
          party = saved.party ?? null;
          selectedID = saved.selectedID ?? '';
        }
        const requested = page.url.searchParams.get('side') ?? saved?.side;
        side =
          requested === 'PAYABLE'
            ? 'PAYABLE'
            : requested === 'RECEIVABLE'
              ? 'RECEIVABLE'
              : session.permissions.includes('finance.collection.read')
                ? 'RECEIVABLE'
                : 'PAYABLE';
        const id = page.url.searchParams.get('party_id');
        if (id) {
          const p = await getParty(id);
          if (!alive) return;
          party = { id: p.id, title: p.display_name, subtitle: p.code };
          currency = page.url.searchParams.get('currency') || p.default_currency || 'TRY';
        }
        await load();
        const documentID = page.url.searchParams.get('document_id');
        if (documentID && alive) await startPlan(documentID);
      } catch (e) {
        error = errorMessage(e);
        loading = false;
      }
    })();
    return () => {
      alive = false;
      request?.abort();
      detailRequest?.abort();
      clearTimeout(searchTimer);
    };
  });
</script>

<svelte:head><title>Taksit Planları — Varya One</title></svelte:head>
<div class="plan-page">
  <div class="heading">
    <div>
      <h1>Taksit Planları</h1>
      <p>Belge bazında taksit takibi</p>
    </div>
    <Button variant="outline" disabled={busy || loading} onclick={() => load()}
      ><RefreshCw size={15} /> Yenile</Button
    >
  </div>
  <div class="filters">
    <div class="direction-switch" role="group" aria-label="Plan türü">
      {#if session?.permissions.includes('finance.collection.read')}
        <button
          class:active={side === 'RECEIVABLE'}
          aria-pressed={side === 'RECEIVABLE'}
          disabled={busy}
          onclick={() => {
            side = 'RECEIVABLE';
            selectedPlan = undefined;
            resetList();
          }}><ArrowDownLeft size={16} /> Alacaklar</button
        >
      {/if}
      {#if session?.permissions.includes('finance.payment.read')}
        <button
          class:active={side === 'PAYABLE'}
          aria-pressed={side === 'PAYABLE'}
          disabled={busy}
          onclick={() => {
            side = 'PAYABLE';
            selectedPlan = undefined;
            resetList();
          }}><ArrowUpRight size={16} /> Borçlar</button
        >
      {/if}
    </div>
    <div class="party-filter">
      <span>Cari</span><EntityCombobox
        selected={party}
        onSearch={searchParties}
        triggerPlaceholder="Tüm cariler"
        clearable
        disabled={busy}
        onSelect={(p) => {
          party = p;
          selectedPlan = undefined;
          void load();
        }}
        onClear={() => {
          party = null;
          selectedPlan = undefined;
          void load();
        }}
      />
    </div>
    <Button disabled={!canManage || !party || busy || loading} onclick={() => startPlan()}
      ><Plus size={16} /> Yeni plan</Button
    >
  </div>
  <div class="list-controls">
    <Input
      disabled={busy}
      value={search}
      aria-label="Plan ara"
      placeholder="Cari, plan açıklaması veya belge no ara"
      oninput={(e) => {
        search = e.currentTarget.value;
        clearTimeout(searchTimer);
        searchTimer = setTimeout(resetList, 250);
      }}
    />
    <select disabled={busy} aria-label="Plan sıralaması" bind:value={sort} onchange={resetList}>
      <option value="next_due:asc">En yakın açık vade</option><option value="created_at:desc"
        >En yeni plan</option
      ><option value="created_at:asc">En eski plan</option>
    </select>
    <div class="quick-filters" role="group" aria-label="Hızlı vade filtreleri">
      {#each [{ value: '', label: 'Açık planlar' }, { value: 'OVERDUE', label: 'Gecikenler' }, { value: 'TODAY', label: 'Bugün' }, { value: 'NEXT_7_DAYS', label: 'Önümüzdeki 7 gün' }] as option}
        <Button
          variant={listValues.status === 'OPEN' && (listValues.preset ?? '') === option.value
            ? 'default'
            : 'outline'}
          size="sm"
          disabled={busy}
          aria-pressed={listValues.status === 'OPEN' && (listValues.preset ?? '') === option.value}
          onclick={() => quickFilter(option.value)}>{option.label}</Button
        >
      {/each}
    </div>
    <ListFilters
      filters={planFilters}
      values={listValues}
      disabled={busy}
      onChange={setListFilter}
      onClear={() => {
        listValues = {};
        resetList();
      }}
    />
    {#if search || party}<Button
        variant="ghost"
        size="sm"
        disabled={busy}
        onclick={() => {
          search = '';
          party = null;
          listValues = {};
          resetList();
        }}>Arama ve filtreleri temizle</Button
      >{/if}
  </div>
  {#if !party && canManage}<p class="hint">
      Yeni plan oluşturmak için cari seçin. Tek vadeyle erteleme veya birden fazla taksitle ödeme
      planlayabilirsiniz.
    </p>{/if}
  {#if error}<p class="error" role="alert">{error}</p>{/if}
  {#if message}<p class="success" role="status">{message}</p>{/if}
  {#if creating}
    <section class="panel" aria-labelledby="create-title">
      <div class="heading">
        <h2 id="create-title">Yeni {side === 'RECEIVABLE' ? 'tahsilat' : 'ödeme'} planı</h2>
        <Button variant="ghost" disabled={busy} onclick={() => (creating = false)}>Vazgeç</Button>
      </div>
      <p class="hint">
        {party?.title} için kalan belge tutarını tek vadeye taşıyın veya taksitlere bölün.
      </p>
      <div class="step-heading">
        <div>
          <h3>Belgeleri seçin</h3>
          <p class="hint">
            Aynı para birimindeki bir veya birden fazla belgeyi birleştirebilirsiniz.
          </p>
        </div>
      </div>
      <div class="form-grid">
        <label
          >Para birimi<CurrencySelect bind:value={currency} onChange={() => startPlan()} /></label
        ><label>Plan adı<Input bind:value={description} maxlength={500} disabled={busy} /></label>
      </div>
      <div class="table-wrap">
        <table>
          <thead
            ><tr><th>Seç</th><th>Belge</th><th>Asıl vade</th><th class="money">Kalan</th></tr
            ></thead
          ><tbody>
            {#each items as item}<tr class:selected-source={selectedIDs.includes(item.id)}
                ><td
                  ><input
                    type="checkbox"
                    aria-label={`${item.document_no} belgesini seç`}
                    checked={selectedIDs.includes(item.id)}
                    disabled={busy}
                    onchange={(e) => {
                      selectedIDs = e.currentTarget.checked
                        ? [...selectedIDs, item.id]
                        : selectedIDs.filter((id) => id !== item.id);
                      installments = [];
                    }}
                  /></td
                ><td>{item.document_no}</td><td
                  >{item.due_date ? formatDate(item.due_date) : 'Vadesiz'}</td
                ><td class="money">{formatMoney(item.open_amount, currency)}</td></tr
              >{:else}<tr
                ><td colspan="4">Bu para biriminde planlanabilecek açık belge bulunamadı.</td></tr
              >{/each}
          </tbody>
        </table>
      </div>
      <div class="selection-total">
        <span>{selectedIDs.length} belge seçildi</span>
        <div><span>Planlanacak tutar</span><strong>{formatMoney(total, currency)}</strong></div>
      </div>
      <div class="step-heading">
        <div>
          <h3>Vadeleri belirleyin</h3>
          <p class="hint">
            Vade ertelemek için 1 taksit seçin. Birden fazla taksit aylık olarak hazırlanır.
          </p>
        </div>
      </div>
      <div class="form-grid">
        <label
          >Taksit sayısı<input
            type="number"
            min="1"
            max="120"
            disabled={busy}
            bind:value={count}
          /></label
        ><label>İlk vade<DateInput bind:value={firstDate} /></label><Button
          variant="outline"
          disabled={!selectedIDs.length || busy}
          onclick={generate}><CalendarDays size={16} /> Vadeleri oluştur</Button
        >
      </div>
      {#if installments.length}<p class="hint">
          Hazırlanan tarih ve tutarları aşağıdan düzenleyebilirsiniz.
        </p>
        <div class="table-wrap">
          <table>
            <thead><tr><th>Taksit</th><th>Vade</th><th>Tutar ({currency})</th></tr></thead><tbody
              >{#each installments as row, i}<tr
                  ><td>{i + 1}</td><td><DateInput bind:value={row.due_date} /></td><td
                    ><input
                      disabled={busy}
                      aria-label={`${i + 1}. taksit tutarı`}
                      inputmode="decimal"
                      bind:value={row.amount}
                      onblur={() => (row.amount = trimDecimalZeros(row.amount))}
                    /></td
                  ></tr
                >{/each}</tbody
            >
          </table>
        </div>
        <p class:invalid={totalDraft !== total}>
          Plan toplamı: {totalDraft ? formatMoney(totalDraft, currency) : 'Geçersiz tutar'}
          {totalDraft !== total ? '— seçili açık tutara eşit olmalı' : ''}
        </p>{/if}
      <div class="save-footer">
        <p class="hint">Plan oluşturulduğunda cari bakiyesi değişmez.</p>
        <Button
          disabled={busy || !installments.length || totalDraft !== total || !description.trim()}
          onclick={savePlan}><Check size={16} /> {busy ? 'Kaydediliyor…' : 'Planı kaydet'}</Button
        >
      </div>
    </section>
  {:else if loading}<p role="status">Planlar yükleniyor…</p>
  {:else}
    <div class="workspace">
      <section class="panel plan-list" aria-label="Plan listesi">
        <h2>Planlar <small>({plans.length})</small></h2>

        {#each plans as p}<button
            class:chosen={selectedID === p.id}
            onclick={() => {
              void selectPlan(p.id);
            }}
            disabled={busy}
            ><strong>{p.party_name}</strong><span>{p.description}</span><small
              >{p.status === 'CANCELLED' ? 'İptal' : p.status === 'CLOSED' ? 'Kapandı' : 'Açık'} · Toplam
              {formatMoney(p.total_amount, p.currency)}</small
            >
            <small
              >Kalan {formatMoney(p.open_amount, p.currency)} · {p.next_due_date
                ? formatDate(p.next_due_date)
                : 'Açık vade yok'}</small
            >
            {#if planUnits(p.overdue_amount) > 0n}<small class="error"
                >Geciken {formatMoney(p.overdue_amount, p.currency)}</small
              >{/if}
          </button>{:else}<div class="empty-state">
            <h3>Eşleşen plan yok</h3>
            <p>Arama veya filtreleri değiştirin; yeni plan için cari seçebilirsiniz.</p>
          </div>{/each}
        <div class="pagination">
          <Button
            variant="outline"
            size="sm"
            disabled={busy || !cursorHistory.length}
            onclick={() => {
              cursor = cursorHistory.at(-1) ?? '';
              cursorHistory = cursorHistory.slice(0, -1);
              void load();
            }}>Önceki</Button
          >
          <span>{cursorHistory.length + 1}. sayfa</span>
          <Button
            variant="outline"
            size="sm"
            disabled={busy || !nextCursor}
            onclick={() => {
              cursorHistory = [...cursorHistory, cursor];
              cursor = nextCursor;
              void load();
            }}>Sonraki</Button
          >
        </div>
      </section>
      {#if detailLoading}<p role="status">Plan detayı yükleniyor…</p>{/if}
      {#if !selectedPlan && !detailLoading}<section class="panel empty-detail">
          <h2>Plan detayı</h2>
          <p>
            Görüntülemek için listeden bir plan seçin. Yeni kayıt için cari seçip Yeni plan
            düğmesini kullanın.
          </p>
        </section>{/if}
      {#if selectedPlan}<section class="panel detail" aria-labelledby="plan-title">
          <div class="heading">
            <div>
              <h2 id="plan-title">{selectedPlan.description}</h2>
              <a href={`/cari/kartlar/${selectedPlan.party_id}`}>{selectedPlan.party_name}</a>
            </div>
            <div class="actions">
              <Button variant="outline" onclick={exportPlan}>Excel</Button
              >{#if !selectedPlan.cancelled_at && canManage}<Button
                  variant="outline"
                  disabled={busy}
                  onclick={() => (cancelOpen = true)}>Planı İptal Et</Button
                >{/if}
            </div>
          </div>
          {#if selectedPlan.cancelled_at}<p class="hint">
              İptal: {formatDate(selectedPlan.cancelled_at)} — {selectedPlan.cancel_reason}.
              Belgelerin kalan borcu kendi vadelerinde takip edilir.
            </p>{/if}
          {#if !selectedPlan.cancelled_at}
            <div class="plan-summary">
              <div>
                <span>Plan tutarı</span><strong
                  >{formatMoney(selectedPlan.total_amount, selectedPlan.currency)}</strong
                ><small
                  >{selectedPlan.sources.length} belge · {selectedPlan.installments.length} taksit</small
                >
              </div>
              <div class="remaining">
                <span>Kalan {side === 'RECEIVABLE' ? 'alacak' : 'borç'}</span><strong
                  >{formatMoney(planRemaining, selectedPlan.currency)}</strong
                ><small>{formatMoney(planClosed, selectedPlan.currency)} kapandı</small>
              </div>
              <div>
                <span>Sıradaki vade</span><strong
                  >{nextInstallment ? formatDate(nextInstallment.due_date) : 'Tamamlandı'}</strong
                ><small
                  >{nextInstallment
                    ? `${nextInstallment.number}. taksit · ${formatMoney(nextInstallment.open_amount, selectedPlan.currency)}`
                    : 'Tüm taksitler kapandı'}</small
                >
              </div>
            </div>
            <p class="completion-info">
              Kapanan taksit: {closedCount} / {selectedPlan.installments.length}
            </p>
          {/if}
          <p class="source-links">
            <FileText size={14} /> Kaynak belgeler: {#each selectedPlan.sources as source, i}{i
                ? ', '
                : ''}{#if source.document_id}<a
                  href={`/${side === 'RECEIVABLE' ? 'satis' : 'alis'}/faturalar/${source.document_id}`}
                  >{source.document_no}</a
                >{:else}{source.document_no}{/if}{/each}
          </p>
          {#if payment}<form
              class="payment-form"
              onsubmit={(e) => {
                e.preventDefault();
                void postPayment();
              }}
            >
              <div class="payment-heading">
                <div>
                  <h3>
                    {payment.number}. taksit için {side === 'RECEIVABLE' ? 'tahsilat' : 'ödeme'}
                  </h3>
                  <p class="hint">
                    {formatDate(payment.due_date)} vadeli · Kalan {formatMoney(
                      payment.open_amount,
                      selectedPlan.currency
                    )}
                  </p>
                </div>
              </div>
              <div class="form-grid">
                <label
                  >{side === 'RECEIVABLE'
                    ? 'Paranın gireceği hesap'
                    : 'Paranın çıkacağı hesap'}<select
                    bind:this={accountSelect}
                    bind:value={accountID}
                    disabled={busy}
                    required
                    ><option value="">Hesap seçin</option>{#each accounts as a}<option value={a.id}
                        >{a.name} · {a.currency}</option
                      >{/each}</select
                  ></label
                ><label
                  >{side === 'RECEIVABLE' ? 'Tahsil edilecek' : 'Ödenecek'} tutar ({selectedPlan.currency})<input
                    disabled={busy}
                    bind:value={paymentAmount}
                    onblur={() => (paymentAmount = trimDecimalZeros(paymentAmount))}
                    inputmode="decimal"
                    required
                  /></label
                ><label>İşlem tarihi<DateInput bind:value={paymentDate} /></label>
                {#if needsExchangeRate}<label
                    >1 {selectedPlan.currency} karşılığı ({baseCurrency})<input
                      bind:value={exchangeRate}
                      inputmode="decimal"
                      disabled={busy}
                      placeholder="İşlem gününün kuru"
                      required
                    /><small class="hint">Şirket para birimine çevirmek için kullanılır.</small
                    ></label
                  >{/if}
              </div>
              {#if !accounts.length}<p class="hint">
                  Bu para biriminde erişebildiğiniz aktif kasa/banka hesabı yok.
                </p>{/if}{#if paymentError}<p class="error" role="alert">{paymentError}</p>{/if}
              <div class="actions">
                <Button type="submit" disabled={busy || !accountID}
                  >{busy
                    ? 'Kaydediliyor…'
                    : side === 'RECEIVABLE'
                      ? 'Tahsilatı kaydet'
                      : 'Ödemeyi kaydet'}</Button
                ><Button
                  variant="ghost"
                  type="button"
                  disabled={busy}
                  onclick={() => (payment = undefined)}>Vazgeç</Button
                >
              </div>
            </form>{/if}
          <div class="table-wrap">
            <table>
              <thead
                ><tr
                  ><th>Taksit</th><th>Vade</th><th class="money">Planlanan</th><th class="money"
                    >Kapanan</th
                  ><th class="money">Kalan</th><th>Durum</th><th>İşlem</th></tr
                ></thead
              ><tbody>
                {#each selectedPlan.installments as row}<tr class:overdue={row.status === 'OVERDUE'}
                    ><td>{row.number}</td><td>{formatDate(row.due_date)}</td><td class="money"
                      >{formatMoney(row.amount, selectedPlan.currency)}</td
                    ><td class="money"
                      >{selectedPlan.cancelled_at
                        ? '—'
                        : formatMoney(row.closed_amount, selectedPlan.currency)}</td
                    ><td class="money"
                      >{selectedPlan.cancelled_at
                        ? '—'
                        : formatMoney(row.open_amount, selectedPlan.currency)}</td
                    ><td
                      ><span class="status-badge" data-status={row.status}
                        >{statusLabels[row.status]}</span
                      ></td
                    ><td
                      >{#if !selectedPlan.cancelled_at && row.number === firstOpen && canPay}<Button
                          variant="outline"
                          disabled={busy}
                          onclick={() => openPayment(row)}
                          >{side === 'RECEIVABLE' ? 'Tahsilat Al' : 'Ödeme Yap'}</Button
                        >{/if}</td
                    ></tr
                  >{/each}
              </tbody>
            </table>
          </div>
          <p class="hint">
            Kapanan tutara ödemeler ve iadeler dahildir. Kısmi ödeme yapabilirsiniz; önce en eski
            açık taksit kapanır.
          </p>
        </section>{/if}
    </div>
  {/if}
</div>
<ReasonDialog
  bind:open={cancelOpen}
  title="Vade planını iptal et"
  description="Plan geçmişte kalır. Kalan borç silinmez; kaynak belgelerin asıl vadeleri yeniden geçerli olur. Yeniden planlamak için iptalden sonra yeni plan oluşturabilirsiniz."
  confirmLabel="Planı İptal Et"
  onConfirm={cancelPlan}
/>

<style>
  .list-controls {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 10px;
    margin-bottom: 16px;
  }
  .list-controls :global(input) {
    max-width: 420px;
  }
  .list-controls select {
    width: auto;
    flex: 0 1 240px;
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 8px;
    background: var(--background);
  }
  .quick-filters,
  .pagination {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 8px;
  }
  .pagination {
    margin-top: 12px;
    justify-content: space-between;
    font-size: 12px;
  }

  .plan-page {
    display: grid;
    gap: 12px;
    margin: 0 auto;
    padding: 16px 20px;
    max-width: 1600px;
    color: var(--text);
    font-size: 12px;
  }
  h1 {
    margin: 0;
    font-size: 18px;
    font-weight: 650;
  }
  h2 {
    margin: 0;
    font-size: 13px;
    font-weight: 650;
  }
  h3 {
    margin: 0;
    font-size: 12px;
    font-weight: 650;
  }
  p {
    margin: 4px 0;
    font-size: 12px;
    line-height: 1.5;
  }
  .heading {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
  }
  .heading p,
  .hint {
    color: var(--text-muted);
  }
  .filters {
    display: flex;
    align-items: end;
    flex-wrap: wrap;
    gap: 12px;
    padding: 10px 12px;
    border: 1px solid var(--border);
    background: var(--surface);
    border-radius: var(--radius-control);
  }
  .direction-switch {
    display: flex;
    align-self: end;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    overflow: hidden;
  }
  .direction-switch button {
    display: flex;
    align-items: center;
    gap: 5px;
    min-height: 32px;
    padding: 0 12px;
    border: 0;
    background: var(--surface);
    color: var(--text-muted);
    cursor: pointer;
  }
  .direction-switch button + button {
    border-left: 1px solid var(--border);
  }
  .direction-switch button.active {
    background: var(--surface-muted);
    color: var(--text);
    box-shadow: inset 0 -2px var(--primary);
  }
  .party-filter {
    display: grid;
    gap: 4px;
    flex: 1;
    min-width: 220px;
    max-width: 500px;
  }
  .party-filter > span,
  label {
    font-size: 11px;
    font-weight: 500;
  }
  label {
    display: grid;
    gap: 4px;
  }
  .panel {
    min-width: 0;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    padding: 12px;
    background: var(--surface);
  }
  .workspace {
    display: grid;
    grid-template-columns: 240px minmax(0, 1fr);
    gap: 12px;
    align-items: start;
  }
  .plan-list {
    padding: 0;
    overflow: hidden;
  }
  .plan-list > h2 {
    padding: 10px 12px;
    background: var(--surface-muted);
    border-bottom: 1px solid var(--border);
  }
  .plan-list > .hint {
    padding: 0 10px;
  }
  .plan-list > button {
    display: grid;
    width: 100%;
    text-align: left;
    gap: 4px;
    padding: 10px 12px;
    border: 0;
    border-bottom: 1px solid var(--border);
    border-left: 2px solid transparent;
    background: transparent;
    color: inherit;
    cursor: pointer;
  }
  .plan-list > button:last-child {
    border-bottom: 0;
  }
  .plan-list > button:hover {
    background: var(--surface-muted);
  }
  .plan-list > button.chosen {
    border-left-color: var(--primary);
    background: var(--surface-muted);
  }
  .plan-list strong {
    font-size: 12px;
    font-weight: 600;
  }
  .plan-list span,
  .plan-list small {
    font-size: 11px;
    color: var(--text-muted);
    overflow-wrap: anywhere;
  }
  .form-grid {
    display: flex;
    flex-wrap: wrap;
    align-items: end;
    gap: 12px;
    margin: 12px 0;
  }
  .form-grid > label {
    flex: 1 1 180px;
    min-width: 0;
  }
  input:not([type='checkbox']),
  select {
    box-sizing: border-box;
    width: 100%;
    min-height: 32px;
    max-width: 100%;
    padding: 5px 8px;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    color: var(--text);
    background: var(--surface);
    font: inherit;
  }
  input[type='checkbox'] {
    width: 14px;
    height: 14px;
    accent-color: var(--primary);
  }
  button:focus-visible,
  input:focus-visible,
  select:focus-visible {
    outline: 2px solid var(--primary);
    outline-offset: 2px;
  }
  button:disabled {
    opacity: 0.55;
    cursor: not-allowed;
  }
  .table-wrap {
    overflow: auto;
    margin: 10px 0;
    border: 1px solid var(--border);
  }
  table {
    width: 100%;
    border-collapse: collapse;
    white-space: nowrap;
    font-size: 12px;
  }
  th,
  td {
    padding: 8px 10px;
    border-bottom: 1px solid var(--border);
    text-align: left;
  }
  th {
    font-size: 11px;
    font-weight: 600;
    background: var(--surface-muted);
    color: var(--text-muted);
  }
  tbody tr:last-child td {
    border-bottom: 0;
  }
  tbody tr:hover {
    background: var(--surface-muted);
  }
  .money {
    text-align: right;
    font-variant-numeric: tabular-nums;
  }
  .step-heading {
    margin: 16px 0 10px;
    padding: 8px 0;
    border-top: 1px solid var(--border);
  }
  .step-heading h3 {
    text-transform: uppercase;
    font-size: 11px;
    letter-spacing: 0.03em;
  }
  .step-heading p {
    font-size: 11px;
  }
  .selected-source {
    background: var(--surface-muted);
  }
  .selection-total {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 12px;
    padding: 8px 10px;
    border-bottom: 1px solid var(--border);
    font-size: 11px;
    color: var(--text-muted);
  }
  .selection-total > div {
    display: flex;
    gap: 12px;
    align-items: baseline;
  }
  .selection-total strong {
    font-size: 13px;
    color: var(--text);
    font-variant-numeric: tabular-nums;
  }
  .save-footer {
    display: flex;
    justify-content: space-between;
    align-items: center;
    flex-wrap: wrap;
    gap: 10px;
    padding-top: 12px;
    margin-top: 12px;
    border-top: 1px solid var(--border);
  }
  .plan-summary {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    border-top: 1px solid var(--border);
    border-bottom: 1px solid var(--border);
    margin: 12px 0 6px;
    background: var(--surface-muted);
  }
  .plan-summary > div {
    display: grid;
    gap: 5px;
    padding: 10px 12px;
  }
  .plan-summary > div + div {
    border-left: 1px solid var(--border);
  }
  .plan-summary span,
  .plan-summary small {
    font-size: 11px;
    color: var(--text-muted);
  }
  .plan-summary strong {
    font-size: 15px;
    font-weight: 600;
    font-variant-numeric: tabular-nums;
  }
  .completion-info {
    font-size: 11px;
    color: var(--text-muted);
    text-align: right;
  }
  .source-links {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 4px;
    font-size: 11px;
    color: var(--text-muted);
    margin: 8px 0;
  }
  .status-badge {
    display: inline-flex;
    padding: 2px 5px;
    border: 1px solid var(--border);
    border-radius: 3px;
    font-size: 10px;
    color: var(--text-muted);
  }
  .status-badge[data-status='OVERDUE'] {
    color: var(--danger);
    border-color: var(--danger);
  }
  .status-badge[data-status='CLOSED'] {
    color: var(--text);
  }
  .status-badge[data-status='PARTIAL'] {
    color: var(--primary);
  }
  .overdue {
    background: color-mix(in srgb, var(--danger) 4%, transparent);
  }
  .payment-form {
    border: 1px solid var(--border-strong);
    border-left: 3px solid var(--primary);
    padding: 12px;
    margin: 12px 0;
    background: var(--surface-muted);
  }
  .payment-heading h3 {
    margin: 0;
  }
  .payment-heading p {
    font-size: 11px;
  }
  .actions {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 8px;
  }
  .error,
  .invalid {
    color: var(--danger);
  }
  .error,
  .success {
    border: 1px solid var(--border);
    border-left: 3px solid var(--primary);
    padding: 8px 10px;
    background: var(--surface);
    font-size: 12px;
  }
  .error {
    border-left-color: var(--danger);
  }
  a {
    color: var(--primary);
    text-decoration: underline;
    text-underline-offset: 2px;
  }
  .empty-state {
    padding: 20px 12px;
    color: var(--text-muted);
  }
  .empty-state h3 {
    margin-bottom: 6px;
  }
  .empty-detail {
    padding: 20px;
    min-height: 160px;
  }
  .empty-detail p {
    margin-top: 8px;
    max-width: 500px;
    color: var(--text-muted);
  }
  @media (max-width: 1100px) {
    .workspace {
      grid-template-columns: 200px minmax(0, 1fr);
    }
    .heading {
      flex-wrap: wrap;
    }
  }
  @media (max-width: 800px) {
    .workspace {
      grid-template-columns: 1fr;
    }
    .plan-list {
      max-height: 220px;
      overflow: auto;
    }
    .plan-page {
      padding: 12px;
    }
  }
  @media (max-width: 560px) {
    .plan-summary {
      grid-template-columns: 1fr;
    }
    .plan-summary > div + div {
      border-left: 0;
      border-top: 1px solid var(--border);
    }
    .party-filter {
      min-width: 100%;
    }
    .form-grid > label {
      min-width: 100%;
    }
    .selection-total,
    .selection-total > div {
      flex-wrap: wrap;
    }
  }
</style>
