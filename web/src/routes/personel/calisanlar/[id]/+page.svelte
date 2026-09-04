<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/state';
  import { ArrowLeft, Plus, ChevronRight } from '@lucide/svelte';
  import { goto } from '$app/navigation';
  import { api, APIRequestError, type Session } from '$lib/api';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { Badge } from '$lib/components/ui/badge';
  import * as Field from '$lib/components/ui/field';
  import { DateInput } from '$lib/components/varya/date-input';
  import { FileDrop } from '$lib/components/varya/file-drop';
  import { formatDate } from '$lib/design/formatters';
  import { parseMoneyInput, trimDecimalZeros } from '$lib/design/decimal';
  import { localizedEnum } from '$lib/design/labels';
  import { advanceStatusLabel } from '$lib/features/hr/advance';
  import * as hr from '$lib/features/hr/api';
  import {
    listProvinces,
    listDistricts,
    listNeighborhoods,
    getPartyLocationDefaults
  } from '$lib/features/parties/api';
  import { EntityCombobox } from '$lib/components/varya/entity-combobox';
  import { listEmployeeAssetAssignments } from '$lib/features/fixed-assets/api';
  import {
    money,
    payrollStatusLabel,
    sgkStatusLabel,
    statusTone,
    SGK_STATUS_OPTIONS,
    type Employee
  } from '$lib/features/hr/types';

  type Tab =
    | 'kart'
    | 'adres'
    | 'kimlik'
    | 'istihdam'
    | 'ucret'
    | 'belgeler'
    | 'zimmet'
    | 'avanslar'
    | 'plan';
  const ALL_TABS: [Tab, string, string][] = [
    ['kart', 'Kart', 'İletişim bilgileri'],
    ['adres', 'Adres', 'İl, ilçe ve açık adres'],
    ['kimlik', 'Kimlik & Banka', 'TC kimlik no, IBAN ve acil durum bilgileri'],
    ['istihdam', 'İstihdam', 'Çalışma dönemleri ve sonlandırma'],
    ['ucret', 'Ücret', 'Aylık brüt ve net ücret'],
    ['belgeler', 'Belgeler', 'Sözleşme, kimlik ve sağlık belgeleri'],
    ['zimmet', 'Zimmet', 'Teslim edilen sabit kıymetler'],
    ['avanslar', 'Avanslar', 'Açık bakiye ve geçmiş personel avansları'],
    ['plan', 'Plan', 'Atanmış çalışma şablonları']
  ];

  let tab = $state<Tab>('kart');
  let permissions = $state<string[]>([]);
  let sessionObj = $state<Session | null>(null);
  let employee = $state<Employee | null>(null);
  let loading = $state(true);
  let error = $state('');
  let msg = $state('');
  let actionError = $state('');
  let tabLoading = $state(false);

  let employments = $state<any[]>([]);
  let terms = $state<any[]>([]);
  let documents = $state<any[]>([]);
  let assetAssignments = $state<any[]>([]);
  let scheduleAssignments = $state<any[]>([]);
  let advances = $state<import('$lib/features/hr/types').EmployeeAdvance[]>([]);
  let advanceTotal = $state('0.00');
  let templates = $state<any[]>([]);

  const employeeID = $derived(page.params.id ?? '');
  const canEdit = $derived(permissions.includes('hr.employee.edit'));
  const canReadPrivate = $derived(permissions.includes('hr.employee_private.read'));
  const canEditPrivate = $derived(permissions.includes('hr.employee_private.edit'));
  const TABS = $derived(
    ALL_TABS.filter(
      (t) =>
        (t[0] !== 'kimlik' || canReadPrivate) &&
        (t[0] !== 'avanslar' || permissions.includes('hr.employee_advance.read'))
    )
  );
  const activeEmploymentID = $derived(
    (employments.find((e: any) => !e.end_date) ?? employments[0])?.id ?? ''
  );

  // inline form state
  let empStart = $state('');
  let terminating = $state<string | null>(null);
  let terminateForm = $state({ end_date: '', termination_reason: '' });
  let editingCard = $state(false);
  let cardForm = $state({
    first_name: '',
    last_name: '',
    position_title: '',
    status: 'ACTIVE',
    work_email: '',
    personal_email: '',
    phone: '',
    occupation_code: ''
  });
  let cardOccupationName = $state('');
  let cardSaving = $state(false);
  const occSelected = $derived(
    cardForm.occupation_code
      ? {
          id: cardForm.occupation_code,
          title: cardOccupationName || cardForm.occupation_code,
          subtitle: cardForm.occupation_code
        }
      : null
  );
  let showTermForm = $state(false);
  let termForm = $state({
    gross_wage: '',
    net_wage: '',
    work_type: 'FULL_TIME',
    sgk_status: '4A',
    is_minimum_wage: false,
    contribution_scheme_code: ''
  });
  let contributionSchemes = $state<{ code: string; name: string }[]>([]);
  let defaultSchemeCode = $state('');
  let savingDefaultScheme = $state(false);
  let wageCalculating = $state(false);
  let wageCalcError = $state('');
  let wageEditTimer: ReturnType<typeof setTimeout> | undefined;
  let activeTermNet = $state('');

  const activeTerm = $derived(terms.find((t: any) => !t.effective_to) ?? null);

  async function loadPayrollMeta() {
    try {
      if (!contributionSchemes.length) {
        const packs = (await hr.listLegislationPacks()).items;
        const active = packs.find((p: any) => p.status === 'ACTIVE') ?? packs[0];
        if (active) {
          const detail = await hr.getLegislationPack(active.id);
          contributionSchemes = detail.contribution_schemes ?? [];
        }
      }
      defaultSchemeCode = (await hr.getPayrollSettings()).default_contribution_scheme_code;
    } catch {
      /* teşvik paketi listesi alınamadı; form NO_DISCOUNT ile çalışır */
    }
  }

  function prefillTermScheme() {
    termForm.contribution_scheme_code =
      activeTerm?.contribution_scheme_code || defaultSchemeCode || 'NO_DISCOUNT';
  }

  async function saveDefaultScheme() {
    if (savingDefaultScheme || !termForm.contribution_scheme_code) return;
    savingDefaultScheme = true;
    actionError = '';
    try {
      const r = await hr.savePayrollSettings(termForm.contribution_scheme_code);
      defaultSchemeCode = r.default_contribution_scheme_code;
      msg = 'Varsayılan teşvik paketi kaydedildi.';
    } catch (cause) {
      actionError = cause instanceof APIRequestError ? cause.message : 'Varsayılan kaydedilemedi.';
    } finally {
      savingDefaultScheme = false;
    }
  }

  const schemeLabel = (code: string) =>
    contributionSchemes.find((s) => s.code === code)?.name ||
    (code === 'NO_DISCOUNT' ? 'İndirimsiz' : code);

  async function loadActiveTermNet() {
    activeTermNet = '';
    const t = terms.find((x: any) => !x.effective_to);
    if (!t?.gross_wage) return;
    try {
      const preview = await hr.wagePreview({
        mode: 'gross',
        amount: parseMoneyInput(t.gross_wage),
        scheme: t.contribution_scheme_code || undefined
      });
      activeTermNet = preview.net;
    } catch {
      /* net tahmini gösterilemedi */
    }
  }

  async function applyMinimumWageToForm() {
    wageCalcError = '';
    wageCalculating = true;
    try {
      const preview = await hr.minimumWage();
      termForm.gross_wage = trimDecimalZeros(preview.gross);
      termForm.net_wage = trimDecimalZeros(preview.net);
    } catch (cause) {
      wageCalcError =
        cause instanceof APIRequestError ? cause.message : 'Güncel asgari ücret alınamadı.';
    } finally {
      wageCalculating = false;
    }
  }

  function onMinimumWageToggle() {
    if (termForm.is_minimum_wage) void applyMinimumWageToForm();
    else {
      termForm.gross_wage = '';
      termForm.net_wage = '';
      wageCalcError = '';
    }
  }

  function scheduleWageRecalc(mode: 'gross' | 'net') {
    if (termForm.is_minimum_wage) return;
    clearTimeout(wageEditTimer);
    wageEditTimer = setTimeout(() => void recalcWage(mode), 350);
  }

  async function recalcWage(mode: 'gross' | 'net') {
    // Read in Turkish notation before asking the server, so "12.500" is
    // twelve thousand five hundred lira rather than twelve and a half.
    const amount = parseMoneyInput(mode === 'gross' ? termForm.gross_wage : termForm.net_wage);
    // Only ask the server once the field holds a plain positive number.
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
        scheme: termForm.contribution_scheme_code || undefined
      });
      if (mode === 'gross') termForm.net_wage = trimDecimalZeros(preview.net);
      else termForm.gross_wage = trimDecimalZeros(preview.gross);
    } catch (cause) {
      wageCalcError = cause instanceof APIRequestError ? cause.message : 'Hesaplanamadı.';
    } finally {
      wageCalculating = false;
    }
  }

  let priv = $state<import('$lib/features/hr/types').PrivateProfile | null>(null);
  let privForm = $state({
    tckn: '',
    iban: '',
    bank_name: '',
    birth_date: '',
    emergency_contact_name: '',
    emergency_contact_phone: '',
    payroll_email: ''
  });
  let privSaving = $state(false);
  let docArchived = $state(false);

  type LocOpt = { id: string; name: string };
  let address = $state<hr.EmployeeAddress | null>(null);
  let addressForm = $state({
    province_id: '',
    district_id: '',
    neighborhood_id: '',
    address_line: '',
    postal_code: ''
  });
  let provinceOpts = $state<LocOpt[]>([]);
  let districtOpts = $state<LocOpt[]>([]);
  let neighborhoodOpts = $state<LocOpt[]>([]);
  let addressSaving = $state(false);
  let districtSeq = 0;
  let neighborhoodSeq = 0;
  let locationDefaults = $state<{ city?: LocOpt; district?: LocOpt; neighborhood?: LocOpt }>({});
  let savingLocDefault = $state('');

  async function onProvinceChange(reset = true) {
    if (reset) {
      addressForm.district_id = '';
      addressForm.neighborhood_id = '';
    }
    const mine = ++districtSeq;
    const provinceID = addressForm.province_id;
    districtOpts = [];
    neighborhoodOpts = [];
    if (!provinceID) return;
    try {
      const items = await listDistricts(provinceID);
      if (mine === districtSeq && provinceID === addressForm.province_id) districtOpts = items;
    } catch {
      /* ilçe listesi alınamadı */
    }
  }

  async function onDistrictChange(reset = true) {
    if (reset) addressForm.neighborhood_id = '';
    const mine = ++neighborhoodSeq;
    const districtID = addressForm.district_id;
    neighborhoodOpts = [];
    if (!districtID) return;
    try {
      const items = await listNeighborhoods(districtID);
      if (mine === neighborhoodSeq && districtID === addressForm.district_id)
        neighborhoodOpts = items;
    } catch {
      /* mahalle listesi alınamadı */
    }
  }

  async function saveLocationDefault(level: 'city' | 'district' | 'neighborhood') {
    if (savingLocDefault) return;
    // The default always carries the full province→district→neighbourhood chain
    // that is currently selected, up to the chosen level.
    const province = addressForm.province_id ? Number(addressForm.province_id) : null;
    const district =
      level !== 'city' && addressForm.district_id ? Number(addressForm.district_id) : null;
    const neighborhood =
      level === 'neighborhood' && addressForm.neighborhood_id
        ? Number(addressForm.neighborhood_id)
        : null;
    if (!province) return;
    savingLocDefault = level;
    actionError = '';
    try {
      const saved = await api<{
        province_id?: number;
        province_name?: string;
        district_id?: number;
        district_name?: string;
        neighborhood_id?: number;
        neighborhood_name?: string;
      }>('/address-preferences/default', {
        method: 'PUT',
        body: JSON.stringify({
          province_id: province,
          district_id: district,
          neighborhood_id: neighborhood
        })
      });
      locationDefaults = {
        city: saved.province_id
          ? { id: String(saved.province_id), name: saved.province_name ?? '' }
          : undefined,
        district: saved.district_id
          ? { id: String(saved.district_id), name: saved.district_name ?? '' }
          : undefined,
        neighborhood: saved.neighborhood_id
          ? { id: String(saved.neighborhood_id), name: saved.neighborhood_name ?? '' }
          : undefined
      };
      msg = `Varsayılan ${level === 'city' ? 'il' : level === 'district' ? 'ilçe' : 'mahalle'} kaydedildi.`;
    } catch (cause) {
      actionError = cause instanceof APIRequestError ? cause.message : 'Varsayılan kaydedilemedi.';
    } finally {
      savingLocDefault = '';
    }
  }

  async function applyLocationDefaults() {
    if (!sessionObj) return;
    try {
      const d = await getPartyLocationDefaults(sessionObj);
      locationDefaults = {
        city: d.city ? { id: String(d.city.id), name: d.city.name } : undefined,
        district: d.district ? { id: String(d.district.id), name: d.district.name } : undefined,
        neighborhood: d.neighborhood
          ? { id: String(d.neighborhood.id), name: d.neighborhood.name }
          : undefined
      };
    } catch {
      /* varsayılan adres tercihi yok */
    }
  }

  async function saveAddress() {
    if (addressSaving) return;
    addressSaving = true;
    actionError = '';
    msg = '';
    try {
      address = await hr.saveEmployeeAddress(employeeID, address?.version ?? 0, {
        address_line: addressForm.address_line.trim(),
        postal_code: addressForm.postal_code.trim(),
        province_id: addressForm.province_id ? Number(addressForm.province_id) : null,
        district_id: addressForm.district_id ? Number(addressForm.district_id) : null,
        neighborhood_id: addressForm.neighborhood_id ? Number(addressForm.neighborhood_id) : null
      });
      msg = 'Adres kaydedildi.';
    } catch (cause) {
      actionError = cause instanceof APIRequestError ? cause.message : 'Adres kaydedilemedi.';
    } finally {
      addressSaving = false;
    }
  }

  const DOC_TYPES = [
    'İş sözleşmesi',
    'Kimlik fotokopisi',
    'İkametgah belgesi',
    'Diploma / öğrenim belgesi',
    'SGK işe giriş bildirgesi',
    'Sağlık raporu',
    'Adli sicil kaydı',
    'Vesikalık fotoğraf',
    'Banka hesap bilgisi',
    'Aile durum bildirimi'
  ];

  async function savePrivate() {
    if (privSaving) return;
    privSaving = true;
    actionError = '';
    msg = '';
    const body: Record<string, unknown> = {
      tckn: privForm.tckn.trim(),
      iban: privForm.iban.replace(/\s+/g, ''),
      birth_date: privForm.birth_date,
      emergency_contact_name: privForm.emergency_contact_name,
      emergency_contact_phone: privForm.emergency_contact_phone,
      payroll_email: privForm.payroll_email,
      bank_name: privForm.bank_name
    };
    try {
      priv = await hr.updatePrivateProfile(employeeID, priv?.version ?? 0, body);
      privForm.tckn = priv.tckn ?? '';
      privForm.iban = priv.iban ?? '';
      msg = 'Kimlik ve banka bilgileri kaydedildi.';
    } catch (cause) {
      actionError = cause instanceof APIRequestError ? cause.message : 'Kaydedilemedi.';
    } finally {
      privSaving = false;
    }
  }

  function startCardEdit() {
    if (!employee) return;
    cardForm = {
      first_name: employee.first_name,
      last_name: employee.last_name,
      position_title: employee.position_title ?? '',
      status: employee.status,
      work_email: employee.work_email ?? '',
      personal_email: employee.personal_email ?? '',
      phone: employee.phone ?? '',
      occupation_code: employee.occupation_code ?? ''
    };
    cardOccupationName = employee.occupation_name ?? '';
    editingCard = true;
  }

  async function saveCard() {
    if (!employee || cardSaving) return;
    cardSaving = true;
    actionError = '';
    msg = '';
    try {
      employee = await hr.updateEmployee(employee.id, employee.version, {
        employee_code: employee.employee_code,
        ...cardForm
      });
      editingCard = false;
      msg = 'Çalışan kartı güncellendi.';
    } catch (cause) {
      actionError = cause instanceof APIRequestError ? cause.message : 'Kart güncellenemedi.';
    } finally {
      cardSaving = false;
    }
  }
  let schedForm = $state({ template_id: '', effective_from: '' });
  let docFiles = $state<File[]>([]);
  let docType = $state('');
  let docSensitivity = $state('GENERAL');

  async function loadSession() {
    try {
      sessionObj = await api<Session>('/session');
      permissions = sessionObj.permissions ?? [];
    } catch {
      permissions = [];
    }
  }

  async function loadEmployee() {
    loading = true;
    error = '';
    try {
      employee = await hr.getEmployee(employeeID);
    } catch (cause) {
      error = cause instanceof APIRequestError ? cause.message : 'Çalışan yüklenemedi.';
    } finally {
      loading = false;
    }
  }

  async function loadTab(next: Tab) {
    tab = next;
    actionError = '';
    msg = '';
    tabLoading = true;
    try {
      if (next === 'kimlik') {
        try {
          priv = await hr.getPrivateProfile(employeeID);
        } catch (cause) {
          if (cause instanceof APIRequestError && cause.status === 404) priv = null;
          else throw cause;
        }
        privForm = {
          tckn: priv?.tckn ?? '',
          iban: priv?.iban ?? '',
          bank_name: priv?.bank_name ?? '',
          birth_date: priv?.birth_date ?? '',
          emergency_contact_name: priv?.emergency_contact_name ?? '',
          emergency_contact_phone: priv?.emergency_contact_phone ?? '',
          payroll_email: priv?.payroll_email ?? ''
        };
      } else if (next === 'adres') {
        if (!provinceOpts.length) provinceOpts = await listProvinces();
        await applyLocationDefaults();
        address = await hr.getEmployeeAddress(employeeID);
        const hasSaved =
          address.version > 0 &&
          (address.province_id || address.address_line || address.postal_code);
        addressForm = {
          province_id: address.province_id
            ? String(address.province_id)
            : hasSaved
              ? ''
              : (locationDefaults.city?.id ?? ''),
          district_id: address.district_id
            ? String(address.district_id)
            : hasSaved
              ? ''
              : (locationDefaults.district?.id ?? ''),
          neighborhood_id: address.neighborhood_id
            ? String(address.neighborhood_id)
            : hasSaved
              ? ''
              : (locationDefaults.neighborhood?.id ?? ''),
          address_line: address.address_line ?? '',
          postal_code: address.postal_code ?? ''
        };
        if (addressForm.province_id) await onProvinceChange(false);
        if (addressForm.district_id) await onDistrictChange(false);
      } else if (next === 'istihdam') employments = (await hr.listEmployments(employeeID)).items;
      else if (next === 'ucret') {
        employments = (await hr.listEmployments(employeeID)).items;
        terms = (await hr.listTerms(employeeID)).items;
        await loadPayrollMeta();
        void loadActiveTermNet();
      } else if (next === 'belgeler')
        documents = (await hr.listDocuments(employeeID, docArchived)).items;
      else if (next === 'zimmet')
        assetAssignments = (await listEmployeeAssetAssignments(employeeID)).items ?? [];
      else if (next === 'avanslar') {
        const result = await hr.listAdvancesForEmployee(employeeID);
        advances = result.items;
        advanceTotal = result.total_outstanding;
      } else if (next === 'plan') {
        scheduleAssignments = (await hr.listScheduleAssignments(employeeID)).items;
        templates = (await hr.listScheduleTemplates()).items;
      }
    } catch (cause) {
      actionError = cause instanceof APIRequestError ? cause.message : 'Veriler yüklenemedi.';
    } finally {
      tabLoading = false;
    }
  }

  async function run(fn: () => Promise<unknown>, ok: string, reload: Tab) {
    actionError = '';
    msg = '';
    try {
      await fn();
      msg = ok;
      await loadTab(reload);
      await loadEmployee();
    } catch (cause) {
      actionError = cause instanceof APIRequestError ? cause.message : 'İşlem başarısız oldu.';
    }
  }

  onMount(async () => {
    await loadSession();
    await loadEmployee();
  });
</script>

<svelte:head
  ><title>{employee ? `${employee.first_name} ${employee.last_name}` : 'Çalışan'} · Varya One</title
  ></svelte:head
>

{#if loading}
  <div class="card">Yükleniyor…</div>
{:else if !employee}
  <section class="card" role="alert">
    <strong>{error || 'Çalışan bulunamadı.'}</strong>
    <p><a href="/personel/calisanlar">Çalışan listesine dön</a></p>
  </section>
{:else}
  <header class="page-header">
    <div>
      <a class="back" href="/personel/calisanlar"
        ><ArrowLeft size={13} aria-hidden="true" />Çalışanlar</a
      >
      <div class="title-row">
        <h1>{employee.employee_code} · {employee.first_name} {employee.last_name}</h1>
        <Badge tone={statusTone(employee.status)}>{payrollStatusLabel(employee.status)}</Badge>
      </div>
      <p>{employee.position_title || 'Pozisyon belirtilmemiş'}</p>
    </div>
  </header>

  <nav class="tabs" aria-label="Çalışan sekmeleri">
    {#each TABS as [id, label]}
      <button class:active={tab === id} onclick={() => loadTab(id)}>{label}</button>
    {/each}
  </nav>
  <p class="tab-desc">{TABS.find((t) => t[0] === tab)?.[2]}</p>

  {#if msg}<p class="notice ok" role="status">{msg}</p>{/if}
  {#if actionError}<p class="notice error" role="alert">{actionError}</p>{/if}

  {#if tab === 'kart'}
    <section class="card">
      <div class="card-head">
        <div><h2>Kart bilgileri</h2></div>
        {#if canEdit && !editingCard}
          <Button variant="outline" onclick={startCardEdit}>Düzenle</Button>
        {/if}
      </div>

      {#if editingCard}
        <form
          class="grid-form"
          onsubmit={(e) => {
            e.preventDefault();
            void saveCard();
          }}
        >
          <Field.Field
            ><Field.FieldLabel for="cf-first">Ad</Field.FieldLabel><Input
              id="cf-first"
              bind:value={cardForm.first_name}
              required
            /></Field.Field
          >
          <Field.Field
            ><Field.FieldLabel for="cf-last">Soyad</Field.FieldLabel><Input
              id="cf-last"
              bind:value={cardForm.last_name}
              required
            /></Field.Field
          >
          <Field.Field
            ><Field.FieldLabel for="cf-pos">Pozisyon</Field.FieldLabel><Input
              id="cf-pos"
              bind:value={cardForm.position_title}
            /></Field.Field
          >
          <Field.Field
            ><Field.FieldLabel for="cf-status">Durum</Field.FieldLabel>
            <select id="cf-status" bind:value={cardForm.status} class="select">
              <option value="ACTIVE">Aktif</option>
              <option value="INACTIVE">Pasif</option>
              <option value="ARCHIVED">Arşivlendi</option>
            </select>
          </Field.Field>
          <Field.Field
            ><Field.FieldLabel for="cf-we">İş e-postası</Field.FieldLabel><Input
              id="cf-we"
              type="email"
              bind:value={cardForm.work_email}
            /></Field.Field
          >
          <Field.Field
            ><Field.FieldLabel for="cf-pe">Kişisel e-posta</Field.FieldLabel><Input
              id="cf-pe"
              type="email"
              bind:value={cardForm.personal_email}
            /></Field.Field
          >
          <Field.Field
            ><Field.FieldLabel for="cf-phone">Telefon</Field.FieldLabel><Input
              id="cf-phone"
              bind:value={cardForm.phone}
            /></Field.Field
          >
          <Field.Field>
            <Field.FieldLabel>Meslek kodu</Field.FieldLabel>
            <EntityCombobox
              selected={occSelected}
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
                cardForm.occupation_code = o.id;
                cardOccupationName = o.title;
              }}
              onClear={() => {
                cardForm.occupation_code = '';
                cardOccupationName = '';
              }}
            />
          </Field.Field>
          <div class="form-actions">
            <Button type="button" variant="ghost" onclick={() => (editingCard = false)}
              >Vazgeç</Button
            >
            <Button type="submit" disabled={cardSaving}>Kaydet</Button>
          </div>
        </form>
      {:else}
        <dl class="fields">
          <div>
            <dt>Çalışan kodu</dt>
            <dd>{employee.employee_code}</dd>
          </div>
          <div>
            <dt>Ad Soyad</dt>
            <dd>{employee.first_name} {employee.last_name}</dd>
          </div>
          <div>
            <dt>Pozisyon</dt>
            <dd>{employee.position_title || '—'}</dd>
          </div>
          <div>
            <dt>Durum</dt>
            <dd>
              <Badge tone={statusTone(employee.status)}>{payrollStatusLabel(employee.status)}</Badge
              >
            </dd>
          </div>
          <div>
            <dt>İş e-postası</dt>
            <dd>{employee.work_email || '—'}</dd>
          </div>
          <div>
            <dt>Kişisel e-posta</dt>
            <dd>{employee.personal_email || '—'}</dd>
          </div>
          <div>
            <dt>Meslek kodu</dt>
            <dd>
              {#if employee.occupation_code}
                {employee.occupation_code}{employee.occupation_name
                  ? ` · ${employee.occupation_name}`
                  : ''}
              {:else}—{/if}
            </dd>
          </div>
          <div>
            <dt>Telefon</dt>
            <dd>{employee.phone || '—'}</dd>
          </div>
          <div>
            <dt>İşe giriş</dt>
            <dd>{employee.hire_date ? formatDate(employee.hire_date) : 'İstihdam kaydı yok'}</dd>
          </div>
          <div>
            <dt>İşten çıkış</dt>
            <dd>{employee.termination_date ? formatDate(employee.termination_date) : '—'}</dd>
          </div>
        </dl>
        <p class="hint">İşe giriş / çıkış tarihleri istihdam kayıtlarından gelir.</p>
      {/if}
    </section>
  {:else if tab === 'adres'}
    <section class="card">
      <div class="card-head">
        <div>
          <h2>Adres</h2>
        </div>
      </div>
      {#if tabLoading}
        <p class="state">Yükleniyor…</p>
      {:else}
        <form
          class="row-form"
          onsubmit={(e) => {
            e.preventDefault();
            void saveAddress();
          }}
        >
          <Field.Field>
            <Field.FieldLabel for="af-il">İl</Field.FieldLabel>
            <select
              id="af-il"
              class="select"
              bind:value={addressForm.province_id}
              disabled={!canEdit}
              onchange={() => void onProvinceChange()}
            >
              <option value="">İl seçin</option>
              {#each provinceOpts as p}<option value={p.id}>{p.name}</option>{/each}
            </select>
            {#if canEdit && addressForm.province_id && addressForm.province_id !== locationDefaults.city?.id}
              <button
                type="button"
                class="link"
                disabled={savingLocDefault === 'city'}
                onclick={() => saveLocationDefault('city')}>Varsayılan il yap</button
              >
            {/if}
          </Field.Field>
          <Field.Field>
            <Field.FieldLabel for="af-ilce">İlçe</Field.FieldLabel>
            <select
              id="af-ilce"
              class="select"
              bind:value={addressForm.district_id}
              disabled={!canEdit || !addressForm.province_id}
              onchange={() => void onDistrictChange()}
            >
              <option value="">İlçe seçin</option>
              {#each districtOpts as d}<option value={d.id}>{d.name}</option>{/each}
            </select>
            {#if canEdit && addressForm.district_id && addressForm.district_id !== locationDefaults.district?.id}
              <button
                type="button"
                class="link"
                disabled={savingLocDefault === 'district'}
                onclick={() => saveLocationDefault('district')}>Varsayılan ilçe yap</button
              >
            {/if}
          </Field.Field>
          <Field.Field>
            <Field.FieldLabel for="af-mah">Mahalle</Field.FieldLabel>
            <select
              id="af-mah"
              class="select"
              bind:value={addressForm.neighborhood_id}
              disabled={!canEdit || !addressForm.district_id}
            >
              <option value="">Mahalle seçin</option>
              {#each neighborhoodOpts as n}<option value={n.id}>{n.name}</option>{/each}
            </select>
            {#if canEdit && addressForm.neighborhood_id && addressForm.neighborhood_id !== locationDefaults.neighborhood?.id}
              <button
                type="button"
                class="link"
                disabled={savingLocDefault === 'neighborhood'}
                onclick={() => saveLocationDefault('neighborhood')}>Varsayılan mahalle yap</button
              >
            {/if}
          </Field.Field>
          <Field.Field>
            <Field.FieldLabel for="af-posta">Posta kodu</Field.FieldLabel>
            <Input
              id="af-posta"
              bind:value={addressForm.postal_code}
              maxlength={5}
              inputmode="numeric"
              disabled={!canEdit}
            />
          </Field.Field>
          <Field.Field class="grow-full">
            <Field.FieldLabel for="af-acik">Açık adres</Field.FieldLabel>
            <textarea
              id="af-acik"
              class="select"
              rows="3"
              maxlength="500"
              bind:value={addressForm.address_line}
              disabled={!canEdit}
            ></textarea>
          </Field.Field>
          {#if canEdit}
            <Button type="submit" disabled={addressSaving}
              >{addressSaving ? 'Kaydediliyor…' : 'Adresi kaydet'}</Button
            >
          {/if}
        </form>
      {/if}
    </section>
  {:else if tab === 'kimlik'}
    <section class="card">
      <div class="card-head">
        <div>
          <h2>Kimlik & Banka</h2>
        </div>
      </div>
      {#if tabLoading}
        <p class="state">Yükleniyor…</p>
      {:else}
        <form
          class="grid-form"
          onsubmit={(e) => {
            e.preventDefault();
            void savePrivate();
          }}
        >
          <Field.Field>
            <Field.FieldLabel for="pf-tckn">TC kimlik no</Field.FieldLabel>
            <Input
              id="pf-tckn"
              bind:value={privForm.tckn}
              inputmode="numeric"
              maxlength={11}
              autocomplete="off"
              disabled={!canEditPrivate}
            />
          </Field.Field>
          <Field.Field>
            <Field.FieldLabel for="pf-iban">IBAN</Field.FieldLabel>
            <Input
              id="pf-iban"
              bind:value={privForm.iban}
              autocomplete="off"
              disabled={!canEditPrivate}
            />
          </Field.Field>
          <Field.Field>
            <Field.FieldLabel for="pf-bank">Banka</Field.FieldLabel>
            <Input
              id="pf-bank"
              bind:value={privForm.bank_name}
              autocomplete="off"
              placeholder="Banka adı"
              disabled={!canEditPrivate}
            />
          </Field.Field>
          <Field.Field>
            <Field.FieldLabel for="pf-birth">Doğum tarihi</Field.FieldLabel>
            <DateInput
              id="pf-birth"
              bind:value={privForm.birth_date}
              ariaLabel="Doğum tarihi"
              disabled={!canEditPrivate}
            />
          </Field.Field>
          <Field.Field>
            <Field.FieldLabel for="pf-pe">Bordro e-postası</Field.FieldLabel>
            <Input
              id="pf-pe"
              type="email"
              bind:value={privForm.payroll_email}
              placeholder="Pusula bu adrese gönderilir"
              disabled={!canEditPrivate}
            />
          </Field.Field>
          <Field.Field>
            <Field.FieldLabel for="pf-ecn">Acil durumda aranacak kişi</Field.FieldLabel>
            <Input
              id="pf-ecn"
              bind:value={privForm.emergency_contact_name}
              disabled={!canEditPrivate}
            />
          </Field.Field>
          <Field.Field>
            <Field.FieldLabel for="pf-ecp">Acil durum telefonu</Field.FieldLabel>
            <Input
              id="pf-ecp"
              bind:value={privForm.emergency_contact_phone}
              disabled={!canEditPrivate}
            />
          </Field.Field>
          {#if canEditPrivate}
            <div class="form-actions">
              <Button type="submit" disabled={privSaving}>Kaydet</Button>
            </div>
          {/if}
        </form>
      {/if}
    </section>
  {:else if tab === 'istihdam'}
    <section class="card">
      <div class="card-head">
        <div>
          <h2>Çalışma dönemleri</h2>
        </div>
      </div>
      {#if canEdit}
        <form
          class="row-form"
          onsubmit={(e) => {
            e.preventDefault();
            if (empStart)
              void run(
                () => hr.createEmployment(employeeID, empStart),
                'İstihdam dönemi eklendi.',
                'istihdam'
              );
          }}
        >
          <Field.Field
            ><Field.FieldLabel for="emp-start">Başlangıç tarihi</Field.FieldLabel><DateInput
              id="emp-start"
              bind:value={empStart}
              ariaLabel="Başlangıç tarihi"
            /></Field.Field
          >
          <Button type="submit" disabled={!empStart}
            ><Plus size={14} aria-hidden="true" />Dönem ekle</Button
          >
        </form>
      {/if}
      {#if tabLoading}<p class="state">Yükleniyor…</p>
      {:else if !employments.length}<p class="state">
          Henüz çalışma dönemi yok. Yukarıdan bir başlangıç tarihi ekleyin.
        </p>
      {:else}
        <table>
          <thead
            ><tr><th>Başlangıç</th><th>Bitiş</th><th>Sonlandırma gerekçesi</th><th></th></tr></thead
          >
          <tbody>
            {#each employments as em}
              <tr>
                <td>{formatDate(em.start_date)}</td>
                <td>{em.end_date ? formatDate(em.end_date) : 'Açık'}</td>
                <td>{em.termination_reason || '—'}</td>
                <td>
                  {#if canEdit && !em.end_date}
                    <button
                      class="link"
                      onclick={() => {
                        terminating = em.id;
                        terminateForm = { end_date: '', termination_reason: '' };
                      }}>Sonlandır</button
                    >
                  {/if}
                </td>
              </tr>
              {#if terminating === em.id}
                <tr class="inline-row">
                  <td colspan="4">
                    <form
                      class="row-form"
                      onsubmit={(e) => {
                        e.preventDefault();
                        void run(
                          () =>
                            hr.terminateEmployment(employeeID, em.id, em.version, terminateForm),
                          'Çalışma dönemi sonlandırıldı.',
                          'istihdam'
                        ).then(() => (terminating = null));
                      }}
                    >
                      <Field.Field
                        ><Field.FieldLabel for="t-end">Bitiş tarihi</Field.FieldLabel><DateInput
                          id="t-end"
                          bind:value={terminateForm.end_date}
                          ariaLabel="Bitiş tarihi"
                        /></Field.Field
                      >
                      <Field.Field class="grow"
                        ><Field.FieldLabel for="t-reason">Gerekçe</Field.FieldLabel><Input
                          id="t-reason"
                          bind:value={terminateForm.termination_reason}
                          placeholder="Örn. İstifa"
                        /></Field.Field
                      >
                      <Button
                        type="submit"
                        disabled={!terminateForm.end_date || !terminateForm.termination_reason}
                        >Kaydet</Button
                      >
                      <Button type="button" variant="ghost" onclick={() => (terminating = null)}
                        >Vazgeç</Button
                      >
                    </form>
                  </td>
                </tr>
              {/if}
            {/each}
          </tbody>
        </table>
      {/if}
    </section>
  {:else if tab === 'ucret'}
    <section class="card">
      <div class="card-head">
        <div>
          <h2>Ücret</h2>
        </div>
        {#if canEdit}<Button
            variant="outline"
            onclick={() => {
              showTermForm = !showTermForm;
              if (showTermForm) prefillTermScheme();
            }}>{showTermForm ? 'Kapat' : 'Yeni ücret'}</Button
          >{/if}
      </div>
      {#if showTermForm && canEdit}
        {#if !activeEmploymentID}
          <p class="notice error">Önce “İstihdam” sekmesinden bir çalışma dönemi ekleyin.</p>
        {:else}
          <form
            class="row-form"
            onsubmit={(e) => {
              e.preventDefault();
              const grossWage = parseMoneyInput(termForm.gross_wage);
              if (!grossWage) return;
              void run(
                () =>
                  hr.createTerm(employeeID, activeEmploymentID, {
                    gross_wage: grossWage,
                    work_type: termForm.work_type,
                    sgk_status: termForm.sgk_status,
                    is_minimum_wage: termForm.is_minimum_wage,
                    contribution_scheme_code: termForm.contribution_scheme_code || undefined
                  }),
                'Ücret kaydedildi.',
                'ucret'
              ).then(() => {
                showTermForm = false;
                termForm = {
                  gross_wage: '',
                  net_wage: '',
                  work_type: 'FULL_TIME',
                  sgk_status: '4A',
                  is_minimum_wage: false,
                  contribution_scheme_code: ''
                };
              });
            }}
          >
            <Field.Field class="grow-full">
              <label class="checkbox-field">
                <input
                  type="checkbox"
                  bind:checked={termForm.is_minimum_wage}
                  onchange={onMinimumWageToggle}
                />
                Asgari ücretli — güncel asgari ücretle kilitlenir, asgari ücret değişince ücreti otomatik
                güncellenir
              </label>
            </Field.Field>
            <Field.Field
              ><Field.FieldLabel for="tf-gross">Aylık brüt ücret (₺)</Field.FieldLabel><Input
                id="tf-gross"
                bind:value={termForm.gross_wage}
                oninput={() => scheduleWageRecalc('gross')}
                inputmode="decimal"
                placeholder="50000"
                disabled={termForm.is_minimum_wage}
                required
              /></Field.Field
            >
            <Field.Field
              ><Field.FieldLabel for="tf-net">Aylık net ücret (₺, tahmini)</Field.FieldLabel><Input
                id="tf-net"
                bind:value={termForm.net_wage}
                oninput={() => scheduleWageRecalc('net')}
                inputmode="decimal"
                placeholder="42000"
                disabled={termForm.is_minimum_wage}
              /></Field.Field
            >
            <Field.Field
              ><Field.FieldLabel for="tf-work">Çalışma türü</Field.FieldLabel>
              <select id="tf-work" bind:value={termForm.work_type} class="select">
                <option value="FULL_TIME">Tam zamanlı</option>
                <option value="PART_TIME">Yarı zamanlı</option>
                <option value="INTERN">Stajyer</option>
                <option value="CONTRACT">Sözleşmeli</option>
              </select>
            </Field.Field>
            <Field.Field>
              <Field.FieldLabel for="tf-sgk">Sigortalılık statüsü</Field.FieldLabel>
              <select id="tf-sgk" bind:value={termForm.sgk_status} class="select">
                {#each SGK_STATUS_OPTIONS as o}
                  <option value={o.value}
                    >{o.label}{o.supported ? '' : ' (bordro hesaplanmaz)'}</option
                  >
                {/each}
              </select>
              {#if !SGK_STATUS_OPTIONS.find((o) => o.value === termForm.sgk_status)?.supported}
                <Field.FieldDescription>
                  Bu statüde otomatik bordro hesaplanmaz; ilgili çalışan bordroda hata ile
                  işaretlenir.
                </Field.FieldDescription>
              {/if}
            </Field.Field>
            <Field.Field>
              <Field.FieldLabel for="tf-scheme">SGK indirimi / teşvik paketi</Field.FieldLabel>
              <select
                id="tf-scheme"
                class="select"
                bind:value={termForm.contribution_scheme_code}
                onchange={() => termForm.gross_wage.trim() && scheduleWageRecalc('gross')}
              >
                {#if !contributionSchemes.some((s) => s.code === 'NO_DISCOUNT')}
                  <option value="NO_DISCOUNT">İndirimsiz</option>
                {/if}
                {#each contributionSchemes as s}
                  <option value={s.code}>{s.name}</option>
                {/each}
              </select>
              <Field.FieldDescription>
                {#if defaultSchemeCode}
                  Varsayılan: {schemeLabel(defaultSchemeCode)}.
                {/if}
                {#if canEdit && termForm.contribution_scheme_code && termForm.contribution_scheme_code !== defaultSchemeCode}
                  <button
                    type="button"
                    class="link"
                    disabled={savingDefaultScheme}
                    onclick={saveDefaultScheme}>bunu varsayılan yap</button
                  >
                {/if}
              </Field.FieldDescription>
            </Field.Field>
            {#if wageCalculating}<span class="muted small">hesaplanıyor…</span>{/if}
            {#if wageCalcError}<p class="notice error">{wageCalcError}</p>{/if}
            <Button type="submit">Kaydet</Button>
          </form>
        {/if}
      {/if}
      {#if tabLoading}<p class="state">Yükleniyor…</p>
      {:else if !terms.length}<p class="state">
          Ücret tanımlı değil. Bordro hesaplaması için gereklidir.
        </p>
      {:else}
        {#if activeTerm}
          <div class="wage-now">
            <div class="wage-fig">
              <span class="wage-label">Aylık brüt</span>
              <strong>{money(activeTerm.gross_wage)} ₺</strong>
            </div>
            <div class="wage-fig">
              <span class="wage-label">Aylık net (tahmini)</span>
              <strong>{activeTermNet ? `${money(activeTermNet)} ₺` : '—'}</strong>
            </div>
            <div class="wage-fig">
              <span class="wage-label">Ücret tipi</span>
              <span>{activeTerm.is_minimum_wage ? 'Asgari ücretli' : 'Belirli tutar'}</span>
            </div>
            <div class="wage-fig">
              <span class="wage-label">Yürürlük</span>
              <span>{formatDate(activeTerm.effective_from)} · devam ediyor</span>
            </div>
          </div>
        {/if}

        <table class="wage-history">
          <thead
            ><tr
              ><th>Dönem</th><th class="num">Aylık brüt</th><th>Ücret tipi</th><th>Çalışma türü</th
              ><th>Sigortalılık</th><th>Teşvik</th></tr
            ></thead
          >
          <tbody>
            {#each terms as t}
              <tr class:current={!t.effective_to}>
                <td>
                  {#if t.effective_to}
                    {formatDate(t.effective_from)} – {formatDate(t.effective_to)}
                  {:else}
                    <span class="pill">Güncel</span>
                    {formatDate(t.effective_from)}’den itibaren
                  {/if}
                </td>
                <td class="num">{money(t.gross_wage)} ₺</td>
                <td>{t.is_minimum_wage ? 'Asgari ücretli' : 'Belirli tutar'}</td>
                <td>{localizedEnum(t.work_type, 'work_type')}</td>
                <td>{sgkStatusLabel(t.sgk_status)}</td>
                <td>{schemeLabel(t.contribution_scheme_code || 'NO_DISCOUNT')}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      {/if}
    </section>
  {:else if tab === 'belgeler'}
    <section class="card">
      <div class="card-head">
        <div>
          <h2>Belgeler</h2>
        </div>
        <div class="seg">
          <button
            class:active={!docArchived}
            onclick={() => {
              docArchived = false;
              void loadTab('belgeler');
            }}>Aktif</button
          >
          <button
            class:active={docArchived}
            onclick={() => {
              docArchived = true;
              void loadTab('belgeler');
            }}>Arşiv</button
          >
        </div>
      </div>
      {#if permissions.includes('hr.employee_document.edit') && !docArchived}
        <form
          class="doc-form"
          onsubmit={(e) => {
            e.preventDefault();
            if (!docFiles.length || !docType.trim()) return;
            const fd = new FormData();
            fd.append('file', docFiles[0]);
            fd.append('document_type', docType.trim());
            fd.append('sensitivity', docSensitivity);
            void run(() => hr.uploadDocument(employeeID, fd), 'Belge yüklendi.', 'belgeler').then(
              () => {
                docFiles = [];
                docType = '';
              }
            );
          }}
        >
          <FileDrop
            bind:files={docFiles}
            variant="document"
            maxSizeKB={10240}
            hint="PDF, resim veya Office belgesi · en fazla 10 MB"
            ariaLabel="Belge seç"
          />
          <div class="doc-meta">
            <Field.Field class="grow">
              <Field.FieldLabel for="doc-type">Belge türü</Field.FieldLabel>
              <Input
                id="doc-type"
                bind:value={docType}
                list="doc-types"
                placeholder="Örn. İş sözleşmesi"
              />
              <datalist id="doc-types">
                {#each DOC_TYPES as t}<option value={t}></option>{/each}
              </datalist>
            </Field.Field>
            <Field.Field>
              <Field.FieldLabel for="doc-sens">Hassasiyet</Field.FieldLabel>
              <select id="doc-sens" bind:value={docSensitivity} class="select">
                <option value="GENERAL">Genel</option>
                <option value="IDENTITY">Kimlik</option>
                <option value="HEALTH">Sağlık</option>
              </select>
            </Field.Field>
            <Button type="submit">Yükle</Button>
          </div>
        </form>
      {/if}
      {#if tabLoading}<p class="state">Yükleniyor…</p>
      {:else if !documents.length}<p class="state">
          {docArchived ? 'Arşivde belge yok.' : 'Belge yok.'}
        </p>
      {:else}
        <table>
          <thead>
            <tr>
              <th>Belge türü</th>
              <th>Dosya</th>
              <th>Hassasiyet</th>
              <th class="num">Boyut</th>
              <th>Yüklenme</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {#each documents as d}
              <tr>
                <td>{d.document_type}</td>
                <td class="muted">{d.original_filename || '—'}</td>
                <td>{localizedEnum(d.sensitivity, 'sensitivity')}</td>
                <td class="num">{(d.size_bytes / 1024).toFixed(0)} KB</td>
                <td>{formatDate(d.created_at)}</td>
                <td>
                  <a href={`/api/v1/hr/employees/${employeeID}/documents/${d.id}/download`}>İndir</a
                  >
                  {#if permissions.includes('hr.employee_document.edit') && !d.archived_at}
                    · <button
                      class="link"
                      onclick={() =>
                        void run(
                          () => hr.archiveDocument(employeeID, d.id),
                          'Belge arşivlendi.',
                          'belgeler'
                        )}>Arşivle</button
                    >
                  {/if}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      {/if}
    </section>
  {:else if tab === 'zimmet'}
    <section class="card">
      <div class="card-head">
        <div>
          <h2>Zimmetli sabit kıymetler</h2>
          <p>
            Bu çalışana teslim edilmiş/edilmiş olan varlıklar. Zimmet işlemleri Sabit Kıymetler
            modülünden yapılır.
          </p>
        </div>
      </div>
      {#if tabLoading}<p class="state">Yükleniyor…</p>
      {:else if !assetAssignments.length}<p class="state">Zimmet kaydı yok.</p>
      {:else}
        <table>
          <thead
            ><tr><th>Sabit kıymet</th><th>Zimmet tarihi</th><th>İade tarihi</th><th>Not</th></tr
            ></thead
          >
          <tbody>
            {#each assetAssignments as a}
              <tr
                ><td>{a.asset_code} · {a.asset_name}</td><td>{formatDate(a.assigned_at)}</td><td
                  >{a.returned_at ? formatDate(a.returned_at) : 'Açık'}</td
                ><td>{a.assignment_note || '—'}</td></tr
              >
            {/each}
          </tbody>
        </table>
      {/if}
    </section>
  {:else if tab === 'avanslar'}
    <section class="card">
      <div class="card-head">
        <div>
          <h2>Personel avansları</h2>
          <p>Toplam açık bakiye: <strong>{money(advanceTotal)} ₺</strong></p>
        </div>
        {#if permissions.includes('hr.employee_advance.post')}<a
            class="button secondary"
            href="/personel/avanslar">Avans ver</a
          >{/if}
      </div>
      {#if tabLoading}<p class="state">Yükleniyor…</p>
      {:else if !advances.length}<p class="state">Avans kaydı yok.</p>
      {:else}<table class="rows-link">
          <thead
            ><tr
              ><th>Tarih</th><th>Açıklama</th><th>Durum</th><th class="num">Verilen</th><th
                class="num">Kalan</th
              ><th></th></tr
            ></thead
          ><tbody
            >{#each advances as advance}<tr onclick={() => goto(`/personel/avanslar/${advance.id}`)}
                ><td>{formatDate(advance.advance_date)}</td><td>{advance.description}</td><td
                  >{advanceStatusLabel(advance.status)}</td
                ><td class="num">{money(advance.original_amount)} ₺</td><td class="num"
                  >{money(advance.outstanding_amount)} ₺</td
                ><td class="go"
                  ><a href={`/personel/avanslar/${advance.id}`} onclick={(e) => e.stopPropagation()}
                    >Detay<ChevronRight size={13} /></a
                  ></td
                ></tr
              >{/each}</tbody
          >
        </table>{/if}
    </section>
  {:else if tab === 'plan'}
    <section class="card">
      <div class="card-head">
        <div>
          <h2>Çalışma planı ataması</h2>
        </div>
      </div>
      {#if permissions.includes('hr.schedule.edit')}
        <form
          class="row-form"
          onsubmit={(e) => {
            e.preventDefault();
            void run(
              () => hr.assignSchedule(employeeID, { ...schedForm, effective_to: '' }),
              'Plan atandı.',
              'plan'
            );
          }}
        >
          <Field.Field>
            <Field.FieldLabel for="sf-tmpl">Şablon</Field.FieldLabel>
            <select id="sf-tmpl" bind:value={schedForm.template_id} class="select" required>
              <option value="">Seçin</option>
              {#each templates as t}<option value={t.id}>{t.code} · {t.name}</option>{/each}
            </select>
          </Field.Field>
          <Field.Field
            ><Field.FieldLabel for="sf-from">Geçerlilik başlangıcı</Field.FieldLabel><DateInput
              id="sf-from"
              bind:value={schedForm.effective_from}
              ariaLabel="Geçerlilik başlangıcı"
            /></Field.Field
          >
          <Button type="submit" disabled={!schedForm.template_id || !schedForm.effective_from}
            >Ata</Button
          >
        </form>
      {/if}
      {#if tabLoading}<p class="state">Yükleniyor…</p>
      {:else if !scheduleAssignments.length}<p class="state">
          Plan ataması yok. Puantaj üretmek için gereklidir.
        </p>
      {:else}
        <table>
          <thead><tr><th>Şablon</th><th>Geçerlilik</th></tr></thead>
          <tbody>
            {#each scheduleAssignments as a}
              <tr
                ><td>{a.template_code} · {a.template_name}</td><td
                  >{formatDate(a.effective_from)} – {a.effective_to
                    ? formatDate(a.effective_to)
                    : 'Açık'}</td
                ></tr
              >
            {/each}
          </tbody>
        </table>
      {/if}
    </section>
  {/if}
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
  .tabs button:hover {
    color: var(--text);
  }
  .tabs button.active {
    color: var(--text);
    border-bottom-color: var(--primary);
    font-weight: 650;
  }
  .tab-desc {
    margin: 8px 0 0;
    color: var(--text-muted);
    font-size: 12px;
  }
  .card-head {
    display: flex;
    justify-content: space-between;
    align-items: flex-start;
    gap: 12px;
    margin-bottom: 14px;
  }
  .card-head h2 {
    margin: 0;
    font-size: 15px;
  }
  .card-head p {
    margin: 3px 0 0;
    color: var(--text-muted);
    font-size: 12px;
    max-width: 60ch;
  }
  .fields {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
    gap: 14px 24px;
    margin: 0;
  }
  .fields dt {
    color: var(--text-muted);
    font-size: 11px;
    margin-bottom: 3px;
  }
  .fields dd {
    margin: 0;
    font-size: 13px;
  }
  .hint {
    margin: 14px 0 0;
    color: var(--text-muted);
    font-size: 11px;
  }
  .row-form {
    display: flex;
    gap: 10px;
    align-items: flex-end;
    flex-wrap: wrap;
    margin-bottom: 14px;
  }
  .seg {
    display: inline-flex;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    overflow: hidden;
  }
  .seg button {
    border: 0;
    background: var(--surface);
    color: var(--text-muted);
    padding: 6px 14px;
    font-size: 12px;
    cursor: pointer;
  }
  .seg button.active {
    background: var(--primary);
    color: var(--primary-foreground);
    font-weight: 650;
  }
  .doc-form {
    display: flex;
    flex-direction: column;
    gap: 12px;
    margin-bottom: 16px;
    max-width: 560px;
  }
  .doc-meta {
    display: flex;
    gap: 10px;
    align-items: flex-end;
    flex-wrap: wrap;
  }
  .doc-meta :global(.grow) {
    flex: 1;
    min-width: 200px;
  }
  td.muted {
    color: var(--text-muted);
  }
  .row-form :global(.grow) {
    flex: 1;
    min-width: 180px;
  }
  .row-form :global(.grow-full) {
    flex-basis: 100%;
  }
  .checkbox-field {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 12px;
    color: var(--text);
  }
  .muted.small {
    font-size: 11px;
    color: var(--text-muted);
  }
  .grid-form {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
    gap: 12px;
    margin-bottom: 16px;
    padding: 12px;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    background: var(--surface-muted, var(--surface));
  }
  .grid-form :global(.grow) {
    grid-column: 1 / -1;
  }
  .form-actions {
    grid-column: 1 / -1;
    display: flex;
    justify-content: flex-end;
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
  table {
    width: 100%;
    border-collapse: collapse;
    font-size: 12px;
  }
  th,
  td {
    padding: 9px 10px;
    border-bottom: 1px solid var(--border);
    text-align: left;
  }
  th {
    color: var(--text-muted);
    font-weight: 650;
  }
  td.num,
  th.num {
    text-align: right;
    font-variant-numeric: tabular-nums;
  }
  .wage-now {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
    gap: 14px;
    padding: 14px;
    margin-bottom: 14px;
    border: 1px solid var(--border);
    border-radius: var(--radius-card, var(--radius-control));
    background: var(--surface-muted, rgba(0, 0, 0, 0.02));
  }
  .wage-fig {
    display: flex;
    flex-direction: column;
    gap: 3px;
    font-size: 12px;
  }
  .wage-label {
    font-size: 11px;
    color: var(--text-muted);
  }
  .wage-fig strong {
    font-size: 16px;
  }
  .wage-history tr.current td {
    background: color-mix(in srgb, var(--primary) 6%, transparent);
  }
  .pill {
    display: inline-block;
    padding: 1px 7px;
    border-radius: 999px;
    background: color-mix(in srgb, var(--primary) 14%, var(--surface));
    color: var(--primary);
    font-size: 10px;
    font-weight: 700;
    margin-right: 6px;
    vertical-align: middle;
  }
  .inline-row td {
    background: var(--surface-muted, rgba(0, 0, 0, 0.02));
  }
  .rows-link tbody tr {
    cursor: pointer;
  }
  .rows-link tbody tr:hover {
    background: var(--surface-muted);
  }
  td.go {
    text-align: right;
    width: 1%;
    white-space: nowrap;
  }
  td.go a {
    display: inline-flex;
    align-items: center;
    gap: 2px;
    color: var(--primary);
    text-decoration: none;
    font-weight: 650;
  }
  .link {
    border: 0;
    background: transparent;
    color: var(--primary);
    cursor: pointer;
    font-size: 12px;
    padding: 0;
  }
  .state {
    padding: 20px 0;
    text-align: center;
    color: var(--text-muted);
    font-size: 13px;
    margin: 0;
  }
</style>
