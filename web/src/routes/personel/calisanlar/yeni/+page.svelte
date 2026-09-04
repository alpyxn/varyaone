<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { ArrowLeft } from '@lucide/svelte';
  import { api, APIRequestError, type Session } from '$lib/api';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import * as Field from '$lib/components/ui/field';
  import { DateInput } from '$lib/components/varya/date-input';
  import { EntityCombobox } from '$lib/components/varya/entity-combobox';
  import { parseMoneyInput, trimDecimalZeros } from '$lib/design/decimal';
  import { todayISO } from '$lib/features/agenda/dates';
  import * as hr from '$lib/features/hr/api';
  import { createEmployee } from '$lib/features/hr/api';
  import { SGK_STATUS_OPTIONS, type EmployeeInput } from '$lib/features/hr/types';

  const OTHER_TABS = ['Kimlik & Banka', 'Belgeler', 'Zimmet', 'İzinler', 'Plan'];

  let denied = $state(false);
  let saving = $state(false);
  let error = $state('');
  // Yerel tarih: toISOString() UTC verdiği için gece yarısından sonra bir
  // önceki günü varsayılan yapıyordu.
  const today = todayISO();

  let form = $state<EmployeeInput>({
    employee_code: '',
    first_name: '',
    last_name: '',
    status: 'ACTIVE',
    position_title: '',
    work_email: '',
    personal_email: '',
    phone: ''
  });
  // İşe giriş tarihi ve ücret: puantaj ve bordronun çalışması için gereken en
  // küçük set. Kartla birlikte tek istekte kaydedilir; eksik kalan bir çalışan
  // puantajda görünmez, bordroda sessizce atlanırdı.
  let employment = $state({
    start_date: today,
    is_minimum_wage: false,
    gross_wage: '',
    net_wage: '',
    work_type: 'FULL_TIME',
    sgk_status: '4A',
    contribution_scheme_code: ''
  });

  // Meslek kodu (SGK/İŞKUR, ISCO-08) kart alanıdır ama SGK bildirimleriyle
  // birlikte girilmesi doğal olduğu için bu bölümde sorulur.
  let occupationName = $state('');
  const occupationSelected = $derived(
    form.occupation_code
      ? {
          id: form.occupation_code,
          title: occupationName || form.occupation_code,
          subtitle: form.occupation_code
        }
      : null
  );

  let contributionSchemes = $state<{ code: string; name: string }[]>([]);
  let defaultSchemeCode = $state('');
  let wageCalculating = $state(false);
  let wageCalcError = $state('');
  let wageEditTimer: ReturnType<typeof setTimeout> | undefined;

  // Arşiv/geçmiş kayıtları için ücret zorunlu değil; aktif çalışan için zorunlu.
  const needsEmployment = $derived(form.status === 'ACTIVE');
  const sgkSupported = $derived(
    SGK_STATUS_OPTIONS.find((o) => o.value === employment.sgk_status)?.supported ?? true
  );
  const schemeLabel = (code: string) =>
    contributionSchemes.find((s) => s.code === code)?.name ||
    (code === 'NO_DISCOUNT' ? 'İndirimsiz' : code);

  async function loadSession() {
    try {
      const session = await api<Session>('/session');
      denied = !(session.permissions ?? []).includes('hr.employee.edit');
    } catch {
      denied = true;
    }
  }

  // Teşvik paketleri ve varsayılan kod, çalışan kartının Ücret sekmesindeki
  // listenin aynısı olsun diye aktif mevzuat paketinden okunur.
  async function loadPayrollMeta() {
    try {
      const packs = (await hr.listLegislationPacks()).items;
      const active = packs.find((p) => p.status === 'ACTIVE') ?? packs[0];
      if (active) {
        contributionSchemes = (await hr.getLegislationPack(active.id)).contribution_schemes ?? [];
      }
      defaultSchemeCode = (await hr.getPayrollSettings()).default_contribution_scheme_code;
    } catch {
      /* liste alınamadı; form indirimsiz (NO_DISCOUNT) ile çalışır */
    }
    employment.contribution_scheme_code = defaultSchemeCode || 'NO_DISCOUNT';
  }

  // Asgari ücret tutarı yalnızca gösterim içindir: sunucu, asgari ücretli bir
  // kaydın brütünü her zaman kendi mevzuat paketinden yazar.
  async function applyMinimumWage() {
    wageCalcError = '';
    wageCalculating = true;
    try {
      const preview = await hr.minimumWage({
        scheme: employment.contribution_scheme_code || undefined,
        date: employment.start_date || undefined
      });
      employment.gross_wage = trimDecimalZeros(preview.gross);
      employment.net_wage = trimDecimalZeros(preview.net);
    } catch (cause) {
      wageCalcError =
        cause instanceof APIRequestError ? cause.message : 'Güncel asgari ücret alınamadı.';
    } finally {
      wageCalculating = false;
    }
  }

  function onMinimumWageToggle() {
    if (employment.is_minimum_wage) void applyMinimumWage();
    else {
      employment.gross_wage = '';
      employment.net_wage = '';
      wageCalcError = '';
    }
  }

  function scheduleWageRecalc(mode: 'gross' | 'net') {
    if (employment.is_minimum_wage) return;
    clearTimeout(wageEditTimer);
    wageEditTimer = setTimeout(() => void recalcWage(mode), 350);
  }

  // Brüt ↔ net: kullanıcı hangisini yazarsa diğeri sunucudaki mevzuatla
  // hesaplanır. Türkçe yazımı ("12.500") önce sayıya çeviririz.
  async function recalcWage(mode: 'gross' | 'net') {
    const amount = parseMoneyInput(mode === 'gross' ? employment.gross_wage : employment.net_wage);
    if (!/^\d+(\.\d{1,2})?$/.test(amount) || Number(amount) <= 0) {
      wageCalcError = '';
      return;
    }
    wageCalculating = true;
    wageCalcError = '';
    try {
      const preview = await hr.wagePreview({
        mode,
        amount,
        scheme: employment.contribution_scheme_code || undefined,
        date: employment.start_date || undefined
      });
      if (mode === 'gross') employment.net_wage = trimDecimalZeros(preview.net);
      else employment.gross_wage = trimDecimalZeros(preview.gross);
    } catch (cause) {
      wageCalcError = cause instanceof APIRequestError ? cause.message : 'Hesaplanamadı.';
    } finally {
      wageCalculating = false;
    }
  }

  function onSchemeChange() {
    if (employment.is_minimum_wage) void applyMinimumWage();
    else if (employment.gross_wage.trim()) scheduleWageRecalc('gross');
  }

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    if (saving) return;
    if (!form.first_name.trim() || !form.last_name.trim()) {
      error = 'Ad ve soyad zorunludur.';
      return;
    }
    if (needsEmployment) {
      if (!employment.start_date) {
        error = 'İşe giriş tarihi zorunludur.';
        return;
      }
      if (!employment.is_minimum_wage && !employment.gross_wage.trim()) {
        error = 'Brüt ücret zorunludur (ya da “Asgari ücretli” seçin).';
        return;
      }
    }
    saving = true;
    error = '';
    try {
      const created = await createEmployee({
        ...form,
        employment: needsEmployment
          ? {
              start_date: employment.start_date,
              is_minimum_wage: employment.is_minimum_wage,
              gross_wage: employment.is_minimum_wage ? '' : parseMoneyInput(employment.gross_wage),
              work_type: employment.work_type,
              sgk_status: employment.sgk_status,
              contribution_scheme_code: employment.contribution_scheme_code || undefined
            }
          : undefined
      });
      await goto(`/personel/calisanlar/${created.id}`);
    } catch (cause) {
      error = cause instanceof APIRequestError ? cause.message : 'Çalışan oluşturulamadı.';
      saving = false;
    }
  }

  onMount(() => {
    void loadSession();
    void loadPayrollMeta();
  });
</script>

<svelte:head><title>Yeni Çalışan · Varya One</title></svelte:head>

{#if denied}
  <section class="card" role="alert">Çalışan oluşturma yetkiniz yok.</section>
{:else}
  <header class="page-header">
    <div>
      <a class="back" href="/personel/calisanlar"
        ><ArrowLeft size={13} aria-hidden="true" />Çalışanlar</a
      >
      <div class="title-row"><h1>Yeni çalışan</h1></div>
      <p>Kart bilgileriyle başlayın. Kaydedince kartın diğer sekmeleri açılır.</p>
    </div>
  </header>

  <nav class="tabs" aria-label="Çalışan sekmeleri">
    <button class="active" type="button">Kart</button>
    {#each OTHER_TABS as t}
      <button type="button" disabled title="Kaydettikten sonra açılır">{t}</button>
    {/each}
  </nav>

  {#if error}<p class="notice error">{error}</p>{/if}

  <section class="card">
    <div class="card-head"><h2>Kart bilgileri</h2></div>
    <form class="grid-form" onsubmit={submit}>
      <Field.Field
        ><Field.FieldLabel for="cf-first">Ad</Field.FieldLabel><Input
          id="cf-first"
          bind:value={form.first_name}
          required
        /></Field.Field
      >
      <Field.Field
        ><Field.FieldLabel for="cf-last">Soyad</Field.FieldLabel><Input
          id="cf-last"
          bind:value={form.last_name}
          required
        /></Field.Field
      >
      <Field.Field
        ><Field.FieldLabel for="cf-code">Çalışan kodu</Field.FieldLabel><Input
          id="cf-code"
          bind:value={form.employee_code}
          placeholder="Boş bırakırsanız otomatik üretilir"
        /></Field.Field
      >
      <Field.Field
        ><Field.FieldLabel for="cf-pos">Pozisyon</Field.FieldLabel><Input
          id="cf-pos"
          bind:value={form.position_title}
        /></Field.Field
      >
      <Field.Field>
        <Field.FieldLabel for="cf-status">Durum</Field.FieldLabel>
        <select id="cf-status" bind:value={form.status} class="select">
          <option value="ACTIVE">Aktif</option>
          <option value="INACTIVE">Pasif</option>
          <option value="ARCHIVED">Arşivlendi</option>
        </select>
      </Field.Field>
      <Field.Field
        ><Field.FieldLabel for="cf-we">İş e-postası</Field.FieldLabel><Input
          id="cf-we"
          type="email"
          bind:value={form.work_email}
        /></Field.Field
      >
      <Field.Field
        ><Field.FieldLabel for="cf-pe">Kişisel e-posta</Field.FieldLabel><Input
          id="cf-pe"
          type="email"
          bind:value={form.personal_email}
        /></Field.Field
      >
      <Field.Field
        ><Field.FieldLabel for="cf-phone">Telefon</Field.FieldLabel><Input
          id="cf-phone"
          bind:value={form.phone}
        /></Field.Field
      >
      {#if needsEmployment}
        <div class="section-head">
          <h3>İşe giriş ve ücret</h3>
        </div>
        <div class="subgrid">
          <Field.Field>
            <Field.FieldLabel for="cf-start">İşe giriş tarihi</Field.FieldLabel>
            <DateInput
              id="cf-start"
              bind:value={employment.start_date}
              ariaLabel="İşe giriş tarihi"
            />
          </Field.Field>
          <Field.Field>
            <Field.FieldLabel for="cf-work">Çalışma türü</Field.FieldLabel>
            <select id="cf-work" bind:value={employment.work_type} class="select">
              <option value="FULL_TIME">Tam zamanlı</option>
              <option value="PART_TIME">Yarı zamanlı</option>
              <option value="INTERN">Stajyer</option>
              <option value="CONTRACT">Sözleşmeli</option>
            </select>
            {#if employment.work_type !== 'FULL_TIME'}
              <Field.FieldDescription>
                Tam zamanlı olmayan çalışanların bordrosu otomatik hesaplanmaz.
              </Field.FieldDescription>
            {/if}
          </Field.Field>
          <Field.Field>
            <Field.FieldLabel for="cf-sgk">Sigortalılık statüsü</Field.FieldLabel>
            <select id="cf-sgk" bind:value={employment.sgk_status} class="select">
              {#each SGK_STATUS_OPTIONS as option}
                <option value={option.value}
                  >{option.label}{option.supported ? '' : ' (bordro hesaplanmaz)'}</option
                >
              {/each}
            </select>
            {#if !sgkSupported}
              <Field.FieldDescription>
                Bu statüde otomatik bordro hesaplanmaz; çalışan bordroda hata ile işaretlenir.
              </Field.FieldDescription>
            {/if}
          </Field.Field>
          <Field.Field>
            <Field.FieldLabel for="cf-scheme">SGK indirimi / teşvik paketi</Field.FieldLabel>
            <select
              id="cf-scheme"
              class="select"
              bind:value={employment.contribution_scheme_code}
              onchange={onSchemeChange}
            >
              {#if !contributionSchemes.some((s) => s.code === 'NO_DISCOUNT')}
                <option value="NO_DISCOUNT">İndirimsiz</option>
              {/if}
              {#each contributionSchemes as scheme}
                <option value={scheme.code}>{scheme.name}</option>
              {/each}
            </select>
            {#if defaultSchemeCode}
              <Field.FieldDescription>
                Varsayılan: {schemeLabel(defaultSchemeCode)}.
              </Field.FieldDescription>
            {/if}
          </Field.Field>
          <Field.Field>
            <Field.FieldLabel>Meslek kodu</Field.FieldLabel>
            <EntityCombobox
              selected={occupationSelected}
              clearable
              title="Meslek kodu seç"
              description="SGK / İŞKUR meslek kodu (ISCO-08)."
              triggerLabel="Meslek kodu"
              triggerPlaceholder="Kod ya da meslek adı yazın"
              searchPlaceholder="Kod ya da meslek adı ara…"
              emptyText="Eşleşen meslek kodu bulunamadı."
              initialEmptyText="Aramak için yazmaya başlayın."
              onSearch={async (q) => {
                const res = await hr.searchOccupationCodes(q);
                return {
                  items: res.items.map((o) => ({ id: o.code, title: o.name, subtitle: o.code }))
                };
              }}
              onSelect={(o) => {
                form.occupation_code = o.id;
                occupationName = o.title;
              }}
              onClear={() => {
                form.occupation_code = '';
                occupationName = '';
              }}
            />
          </Field.Field>

          <Field.Field class="grow-full">
            <label class="checkbox-field">
              <input
                type="checkbox"
                bind:checked={employment.is_minimum_wage}
                onchange={onMinimumWageToggle}
              />
              Asgari ücretli — güncel asgari ücretle kilitlenir, asgari ücret değişince ücreti otomatik
              güncellenir
            </label>
          </Field.Field>
          <Field.Field>
            <Field.FieldLabel for="cf-gross">Aylık brüt ücret (₺)</Field.FieldLabel>
            <Input
              id="cf-gross"
              bind:value={employment.gross_wage}
              oninput={() => scheduleWageRecalc('gross')}
              inputmode="decimal"
              placeholder="50000"
              disabled={employment.is_minimum_wage}
              required={!employment.is_minimum_wage}
            />
          </Field.Field>
          <Field.Field>
            <Field.FieldLabel for="cf-net">Aylık net ücret (₺, tahmini)</Field.FieldLabel>
            <Input
              id="cf-net"
              bind:value={employment.net_wage}
              oninput={() => scheduleWageRecalc('net')}
              inputmode="decimal"
              placeholder="42000"
              disabled={employment.is_minimum_wage}
            />
            <Field.FieldDescription>
              {#if wageCalculating}
                hesaplanıyor…
              {:else}
                Brüt veya net birini yazmanız yeterli.
              {/if}
            </Field.FieldDescription>
          </Field.Field>
          {#if wageCalcError}
            <p class="notice error span-all">{wageCalcError}</p>
          {/if}
        </div>
      {:else}
        <p class="notice hint">
          Pasif/arşiv kartlarda işe giriş ve ücret istenmez. Çalışan işe başladığında kartın
          İstihdam ve Ücret sekmelerinden girin.
        </p>
      {/if}
      <div class="form-actions">
        <Button type="button" variant="ghost" onclick={() => goto('/personel/calisanlar')}
          >Vazgeç</Button
        >
        <Button type="submit" disabled={saving}
          >{saving ? 'Oluşturuluyor…' : 'Çalışanı oluştur'}</Button
        >
      </div>
    </form>
  </section>
{/if}

<style>
  .card {
    padding: 16px;
    margin-top: 14px;
  }
  .back {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    color: var(--primary);
    font-size: 12px;
    text-decoration: none;
    margin-bottom: 4px;
  }
  .title-row {
    display: flex;
    align-items: center;
    gap: 10px;
    flex-wrap: wrap;
  }
  .tabs {
    display: flex;
    gap: 2px;
    flex-wrap: wrap;
    margin-top: 12px;
    border-bottom: 1px solid var(--border);
  }
  .tabs button {
    border: 0;
    background: transparent;
    padding: 9px 14px;
    font-size: 13px;
    cursor: pointer;
    color: var(--text-muted);
    border-bottom: 2px solid transparent;
  }
  .tabs button.active {
    color: var(--text);
    border-bottom-color: var(--primary);
    font-weight: 650;
  }
  .tabs button[disabled] {
    cursor: not-allowed;
    opacity: 0.5;
  }
  .card-head h2 {
    margin: 0 0 14px;
    font-size: 15px;
  }
  .grid-form {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
    gap: 12px;
  }
  .section-head {
    grid-column: 1 / -1;
    margin-top: 18px;
    padding-top: 14px;
    border-top: 1px solid var(--border);
  }
  .subgrid {
    grid-column: 1 / -1;
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
    gap: 12px 14px;
    margin-top: 10px;
  }
  .subgrid :global(.grow-full),
  .span-all {
    grid-column: 1 / -1;
  }
  .checkbox-field {
    display: flex;
    align-items: flex-start;
    gap: 8px;
    font-size: 13px;
    line-height: 1.5;
  }
  .section-head h3 {
    margin: 0;
    font-size: 13px;
  }
  .section-head p {
    margin: 2px 0 0;
    font-size: 12px;
    color: var(--text-muted);
  }
  .notice {
    grid-column: 1 / -1;
    font-size: 12px;
  }
  .form-actions {
    grid-column: 1 / -1;
    display: flex;
    justify-content: flex-end;
    gap: 8px;
    margin-top: 4px;
  }
  .select {
    height: var(--control-height, 34px);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    background: var(--surface);
    color: var(--text);
    padding: 0 8px;
    font-size: 13px;
    width: 100%;
  }
</style>
