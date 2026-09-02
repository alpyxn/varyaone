<script lang="ts">
  import { onMount } from 'svelte';
  import { api, type Session } from '$lib/api';
  import { Input } from '$lib/components/ui/input';
  import { TaxOfficePicker } from '$lib/components/varya/tax-office-picker';
  import { CurrencySelect } from '$lib/components/varya/currency-select';
  import {
    emptyAddress,
    emptyContact,
    TURKEY_PROVINCES,
    type Address,
    type LocationOption,
    type PartyInput,
    type PartyLocationDefaults,
    type TaxOfficeReference
  } from './types';
  import {
    getPartyLocationDefaults,
    listDistricts,
    listNeighborhoods,
    listProvinces,
    savePartyLocationDefault
  } from './api';

  type PaymentTerm = {
    id: string;
    code: string;
    name: string;
    due_days: number;
    is_active: boolean;
  };
  type PartyGroup = { id: string; code: string; name: string; is_active: boolean };
  type Member = {
    user: { id: string; display_name: string };
    is_active: boolean;
  };

  let {
    value = $bindable(),
    disabled = false,
    newRecord = false,
    activeTab = $bindable('basic'),
    errors = {}
  }: {
    value: PartyInput;
    disabled?: boolean;
    newRecord?: boolean;
    activeTab?: string;
    errors?: Record<string, string>;
  } = $props();

  let paymentTerms = $state<PaymentTerm[]>([]);
  let groups = $state<PartyGroup[]>([]);
  let members = $state<Member[]>([]);
  let session = $state<Session>();
  let provinces = $state<LocationOption[]>(TURKEY_PROVINCES);
  let districts = $state<Record<number, LocationOption[]>>({});
  let neighborhoods = $state<Record<number, LocationOption[]>>({});
  let optionsLoading = $state(false);
  let optionsError = $state('');
  let locationLoading = $state<Record<string, boolean>>({});
  let locationError = $state<Record<string, string>>({});
  let locationControllers: Record<string, AbortController> = {};
  let locationDefaults = $state<PartyLocationDefaults>({});
  let defaultMessage = $state('');
  let defaultSaving = $state<Record<string, boolean>>({});
  const selectedGroupCount = $derived(
    groups.filter((group) => value.group_ids.includes(group.id)).length
  );

  function changePartyKind(kind: string) {
    value.kind = kind === 'PERSON' ? 'PERSON' : 'ORGANIZATION';
    if (value.kind === 'PERSON') {
      value.tax_office = '';
      value.tax_office_id = '';
    }
  }

  function taxOfficeProvinceID(): string {
    const address = addressAt(0);
    if (address.city_id || address.province_id) return address.city_id || address.province_id || '';
    return (
      provinces.find(
        (province) => province.name.localeCompare(address.city, 'tr', { sensitivity: 'base' }) === 0
      )?.id ?? ''
    );
  }

  function taxOfficeDistrictName(): string {
    const address = addressAt(0);
    return address.district_name || address.district || '';
  }

  function selectTaxOffice(reference: TaxOfficeReference) {
    value.tax_office_id = reference.id;
    value.tax_office = reference.name;
  }

  function clearTaxOffice() {
    value.tax_office_id = '';
    value.tax_office = '';
  }

  function ensurePrimaryAddress() {
    const addresses = Array.isArray(value.addresses) ? value.addresses : [];
    const primary =
      addresses.find((address) => address.is_default) ?? addresses[0] ?? emptyAddress();
    value.addresses = [{ ...primary, is_default: true }];
  }

  function ensurePrimaryContact() {
    if (!Array.isArray(value.contacts) || value.contacts.length === 0) {
      value.contacts = [emptyContact()];
    }
  }

  function addContact() {
    if (disabled) return;
    value.contacts = [
      ...value.contacts,
      { ...emptyContact(), is_primary: value.contacts.length === 0 }
    ];
  }

  function removeContact(index: number) {
    if (disabled || value.contacts.length <= 1) return;
    const contacts = value.contacts.filter((_, contactIndex) => contactIndex !== index);
    if (!contacts.some((contact) => contact.is_primary)) contacts[0].is_primary = true;
    value.contacts = contacts;
  }

  function makePrimaryContact(index: number) {
    if (disabled) return;
    value.contacts = value.contacts.map((contact, contactIndex) => ({
      ...contact,
      is_primary: contactIndex === index
    }));
  }

  function toggleGroup(groupID: string, checked: boolean) {
    if (disabled) return;
    const next = new Set(value.group_ids);
    if (checked) next.add(groupID);
    else next.delete(groupID);
    value.group_ids = [...next];
  }

  function addressAt(index: number): Address {
    return value.addresses[index] ?? emptyAddress();
  }

  function updateAddress(index: number, patch: Partial<Address>) {
    value.addresses = value.addresses.map((address, addressIndex) =>
      addressIndex === index ? { ...address, ...patch } : address
    );
  }

  function selectValue(event: Event): string {
    return (event.currentTarget as HTMLSelectElement).value;
  }

  function locationKey(level: 'district' | 'neighborhood', index: number): string {
    return `${level}:${index}`;
  }

  function cancelLocationRequest(key: string) {
    locationControllers[key]?.abort();
    delete locationControllers[key];
  }

  async function loadDistrictOptions(index: number, provinceID: string, signal?: AbortSignal) {
    const key = locationKey('district', index);
    cancelLocationRequest(key);
    cancelLocationRequest(locationKey('neighborhood', index));
    districts = { ...districts, [index]: [] };
    neighborhoods = { ...neighborhoods, [index]: [] };
    if (!provinceID) return;
    const controller = new AbortController();
    locationControllers[key] = controller;
    locationLoading = { ...locationLoading, [key]: true };
    locationError = { ...locationError, [key]: '' };
    try {
      const items = await listDistricts(provinceID, signal ?? controller.signal);
      districts = { ...districts, [index]: items };
    } catch {
      if (!controller.signal.aborted) {
        locationError = {
          ...locationError,
          [key]: 'İlçe listesi alınamadı; ilçeyi elle yazabilirsiniz.'
        };
      }
    } finally {
      if (locationControllers[key] === controller) {
        delete locationControllers[key];
        locationLoading = { ...locationLoading, [key]: false };
      }
    }
  }

  async function loadNeighborhoodOptions(index: number, districtID: string, signal?: AbortSignal) {
    const key = locationKey('neighborhood', index);
    cancelLocationRequest(key);
    neighborhoods = { ...neighborhoods, [index]: [] };
    if (!districtID) return;
    const controller = new AbortController();
    locationControllers[key] = controller;
    locationLoading = { ...locationLoading, [key]: true };
    locationError = { ...locationError, [key]: '' };
    try {
      const items = await listNeighborhoods(districtID, signal ?? controller.signal);
      neighborhoods = { ...neighborhoods, [index]: items };
    } catch {
      if (!controller.signal.aborted) {
        locationError = {
          ...locationError,
          [key]: 'Mahalle listesi alınamadı; mahalleyi elle yazabilirsiniz.'
        };
      }
    } finally {
      if (locationControllers[key] === controller) {
        delete locationControllers[key];
        locationLoading = { ...locationLoading, [key]: false };
      }
    }
  }

  async function changeCity(index: number, provinceID: string) {
    const province = provinces.find((item) => item.id === provinceID);
    updateAddress(index, {
      city_id: province?.id || '',
      city: province?.name || '',
      district_id: '',
      district: '',
      neighborhood_id: '',
      neighborhood: ''
    });
    await loadDistrictOptions(index, provinceID);
  }

  async function changeDistrict(index: number, districtID: string) {
    const district = (districts[index] ?? []).find((item) => item.id === districtID);
    updateAddress(index, {
      district_id: district?.id || '',
      district: district?.name || '',
      neighborhood_id: '',
      neighborhood: ''
    });
    await loadNeighborhoodOptions(index, districtID);
  }

  function changeNeighborhood(index: number, neighborhoodID: string) {
    const neighborhood = (neighborhoods[index] ?? []).find((item) => item.id === neighborhoodID);
    updateAddress(index, {
      neighborhood_id: neighborhood?.id || '',
      neighborhood: neighborhood?.name || ''
    });
  }

  async function saveDefault(level: 'city' | 'district' | 'neighborhood', index: number) {
    if (!session || !value.addresses[index]) return;
    const saveKey = `${level}:${index}`;
    if (defaultSaving[saveKey]) return;
    const address = value.addresses[index];
    const selection =
      level === 'city'
        ? address.city_id && address.city
          ? { id: address.city_id, name: address.city }
          : undefined
        : level === 'district'
          ? address.district && (address.district_id || address.city_id)
            ? {
                id: address.district_id || address.district,
                name: address.district,
                parent_id: address.city_id || address.city
              }
            : undefined
          : address.neighborhood &&
              (address.neighborhood_id || address.district_id || address.district)
            ? {
                id: address.neighborhood_id || address.neighborhood,
                name: address.neighborhood,
                parent_id: address.district_id || address.district
              }
            : undefined;
    if (!selection) return;
    defaultMessage = '';
    defaultSaving = { ...defaultSaving, [saveKey]: true };
    try {
      locationDefaults = await savePartyLocationDefault(session, level, selection);
      defaultMessage = `Varsayılan ${level === 'city' ? 'il' : level === 'district' ? 'ilçe' : 'mahalle'} kaydedildi.`;
    } catch {
      defaultMessage =
        'Varsayılan adres tercihi kaydedilemedi; kartı kaydetmeye devam edebilirsiniz.';
    } finally {
      defaultSaving = { ...defaultSaving, [saveKey]: false };
    }
  }

  async function applyLocationDefaults(currentSession: Session) {
    if (!newRecord || !value.addresses[0]) return;
    locationDefaults = await getPartyLocationDefaults(currentSession);
    const defaults = locationDefaults;
    const addressIndex = 0;
    if (defaults.city) {
      const province = provinces.find(
        (item) =>
          item.id === defaults.city?.id ||
          item.name.localeCompare(defaults.city?.name || '', 'tr', { sensitivity: 'base' }) === 0
      );
      const provinceID = province?.id || defaults.city.id;
      updateAddress(addressIndex, {
        city_id: provinceID,
        city: province?.name || defaults.city.name,
        district_id: '',
        district: '',
        neighborhood_id: '',
        neighborhood: ''
      });
      await loadDistrictOptions(addressIndex, provinceID);
    }
    const defaultDistrict = defaults.district;
    const district = defaultDistrict
      ? (districts[addressIndex] ?? []).find(
          (item) =>
            item.id === defaultDistrict.id ||
            item.name.localeCompare(defaultDistrict.name, 'tr', { sensitivity: 'base' }) === 0
        )
      : undefined;
    if (
      defaultDistrict &&
      (!defaultDistrict.parent_id || defaultDistrict.parent_id === defaults.city?.id)
    ) {
      const districtID = district?.id || defaultDistrict.id;
      updateAddress(addressIndex, {
        district_id: districtID,
        district: district?.name || defaultDistrict.name,
        neighborhood_id: '',
        neighborhood: ''
      });
      await loadNeighborhoodOptions(addressIndex, districtID);
    }
    const defaultNeighborhood = defaults.neighborhood;
    const neighborhood = defaultNeighborhood
      ? (neighborhoods[addressIndex] ?? []).find(
          (item) =>
            item.id === defaultNeighborhood.id ||
            item.name.localeCompare(defaultNeighborhood.name, 'tr', { sensitivity: 'base' }) === 0
        )
      : undefined;
    if (
      defaultNeighborhood &&
      (!defaultNeighborhood.parent_id || defaultNeighborhood.parent_id === defaultDistrict?.id)
    ) {
      updateAddress(addressIndex, {
        neighborhood_id: neighborhood?.id || defaultNeighborhood.id,
        neighborhood: neighborhood?.name || defaultNeighborhood.name
      });
    }
  }

  async function hydrateAddressSelections() {
    for (let index = 0; index < value.addresses.length; index += 1) {
      const address = addressAt(index);
      const province = provinces.find(
        (item) => item.name.localeCompare(address.city, 'tr', { sensitivity: 'base' }) === 0
      );
      const provinceID = address.city_id || province?.id || '';
      if (!provinceID) continue;
      if (address.city_id !== provinceID || (province && address.city !== province.name)) {
        updateAddress(index, { city_id: provinceID, city: province?.name || address.city });
      }
      await loadDistrictOptions(index, provinceID);
      const district = (districts[index] ?? []).find(
        (item) => item.name.localeCompare(address.district, 'tr', { sensitivity: 'base' }) === 0
      );
      const districtID = address.district_id || district?.id || '';
      if (!districtID) continue;
      if (address.district_id !== districtID || (district && address.district !== district.name)) {
        updateAddress(index, {
          district_id: districtID,
          district: district?.name || address.district
        });
      }
      await loadNeighborhoodOptions(index, districtID);
      const neighborhood = (neighborhoods[index] ?? []).find(
        (item) =>
          item.name.localeCompare(address.neighborhood || '', 'tr', { sensitivity: 'base' }) === 0
      );
      if (neighborhood && address.neighborhood_id !== neighborhood.id) {
        updateAddress(index, {
          neighborhood_id: neighborhood.id,
          neighborhood: neighborhood.name
        });
      }
    }
  }

  async function loadOptions() {
    optionsLoading = true;
    optionsError = '';
    try {
      const sessionResult = await Promise.allSettled([api<Session>('/session'), listProvinces()]);
      if (sessionResult[0].status === 'fulfilled') {
        const activeSession = sessionResult[0].value;
        session = activeSession;
        const company = activeSession.companies.find(
          (item) => item.id === activeSession.current_company_id
        );
        if (newRecord && company?.base_currency && value.default_currency === 'TRY') {
          value.default_currency = company.base_currency;
        }
      }
      if (sessionResult[1].status === 'fulfilled') provinces = sessionResult[1].value;
      else optionsError = 'İl listesi alınamadı; yerel 81 il listesi kullanılıyor.';
      await hydrateAddressSelections();
      if (newRecord && session) await applyLocationDefaults(session);

      const [termsResult, groupsResult, membersResult] = await Promise.allSettled([
        api<{ items: PaymentTerm[] }>('/party-settings/payment-terms'),
        api<{ items: PartyGroup[] }>('/party-settings/groups'),
        api<{ items: Member[] }>('/users')
      ]);
      if (termsResult.status === 'fulfilled') {
        paymentTerms = termsResult.value.items.filter((item) => item.is_active);
      }
      if (groupsResult.status === 'fulfilled') {
        groups = groupsResult.value.items.filter((item) => item.is_active);
      }
      if (membersResult.status === 'fulfilled') {
        members = membersResult.value.items.filter((item) => item.is_active);
      }
    } catch {
      optionsError = 'Seçenekler yüklenemedi; formu doldurmaya devam edebilirsiniz.';
    } finally {
      optionsLoading = false;
    }
  }

  ensurePrimaryContact();
  ensurePrimaryAddress();
  onMount(() => {
    ensurePrimaryContact();
    ensurePrimaryAddress();
    void loadOptions();
  });
</script>

<div class="product-tabs" aria-label="Cari kartı bölümleri" role="tablist">
  <button
    type="button"
    class:active={activeTab === 'basic'}
    role="tab"
    aria-selected={activeTab === 'basic'}
    id="party-basic-tab"
    onclick={() => (activeTab = 'basic')}>Temel bilgiler</button
  >
  <button
    type="button"
    class:active={activeTab === 'contact'}
    role="tab"
    aria-selected={activeTab === 'contact'}
    id="party-contact-tab"
    onclick={() => (activeTab = 'contact')}>İletişim ve adres</button
  >
  <button
    type="button"
    class:active={activeTab === 'commercial'}
    role="tab"
    aria-selected={activeTab === 'commercial'}
    id="party-commercial-tab"
    onclick={() => (activeTab = 'commercial')}>Ticari ve risk</button
  >
</div>

{#if activeTab === 'basic'}
  <div
    class="party-tabpanel"
    id="party-basic-panel"
    role="tabpanel"
    aria-labelledby="party-basic-tab"
    tabindex="0"
  >
    <section class="form-section" aria-labelledby="party-basic-heading">
      <div class="section-heading">
        <div>
          <h2 id="party-basic-heading">Temel bilgiler</h2>
        </div>
      </div>

      <div class="form-grid">
        <label class="field"
          ><span>Cari türü</span><select
            value={value.kind}
            onchange={(event) => changePartyKind((event.currentTarget as HTMLSelectElement).value)}
            disabled={disabled || !newRecord}
            ><option value="ORGANIZATION">Kurum</option><option value="PERSON">Kişi</option></select
          ></label
        >
        <label class="field"
          ><span>Cari kodu</span><Input
            id="party-code"
            bind:value={value.code}
            {disabled}
            readonly={!newRecord}
            placeholder="Boş bırakırsanız otomatik üretilir"
          /><small>Boş bırakırsanız otomatik üretilir.</small></label
        >

        {#if value.kind === 'ORGANIZATION'}
          <label class="field wide"
            ><span>Resmî unvan <b aria-hidden="true">*</b></span><Input
              id="party-legal-name"
              bind:value={value.legal_name}
              {disabled}
              required
              autocomplete="organization"
            /></label
          >
          <label class="field"
            ><span>Ticari ad</span><Input
              bind:value={value.trade_name}
              {disabled}
              autocomplete="organization"
            /></label
          >
          <label class="field"
            ><span>Vergi numarası</span><Input
              bind:value={value.tax_number}
              maxlength={10}
              inputmode="numeric"
              {disabled}
              autocomplete="off"
            /></label
          >
          <label class="field"
            ><span>Vergi dairesi</span><TaxOfficePicker
              selectedId={value.tax_office_id}
              selectedName={value.tax_office}
              initialProvinceId={taxOfficeProvinceID()}
              initialDistrictName={taxOfficeDistrictName()}
              {provinces}
              {disabled}
              onSelect={selectTaxOffice}
              onClear={clearTaxOffice}
            /></label
          >
          <label class="active-party-field" class:disabled>
            <input
              type="checkbox"
              checked={value.is_active}
              {disabled}
              aria-label="Aktif cari"
              onchange={(event) => (value.is_active = event.currentTarget.checked)}
            />
            <span>Aktif cari</span>
          </label>
        {:else}
          <label class="field"
            ><span>Ad <b aria-hidden="true">*</b></span><Input
              id="party-first-name"
              bind:value={value.first_name}
              {disabled}
              required
              autocomplete="given-name"
            /></label
          >
          <label class="field"
            ><span>Soyad <b aria-hidden="true">*</b></span><Input
              id="party-last-name"
              bind:value={value.last_name}
              {disabled}
              required
              autocomplete="family-name"
            /></label
          >
          <label class="field"
            ><span>TCKN</span><Input
              bind:value={value.identity_number}
              maxlength={11}
              inputmode="numeric"
              {disabled}
              autocomplete="off"
            /></label
          >
        {/if}
      </div>
    </section>

    <fieldset class="form-section roles-section" aria-labelledby="party-role-heading">
      <legend id="party-role-heading">Cari rolü</legend>
      <div class="role-options">
        <label><input type="checkbox" bind:checked={value.is_customer} {disabled} /> Müşteri</label>
        <label
          ><input type="checkbox" bind:checked={value.is_supplier} {disabled} /> Tedarikçi</label
        >
      </div>
    </fieldset>
  </div>
{:else if activeTab === 'contact'}
  <div
    class="party-tabpanel"
    id="party-contact-panel"
    role="tabpanel"
    aria-labelledby="party-contact-tab"
    tabindex="0"
  >
    {#if value.kind === 'PERSON'}
      <fieldset class="person-contact-card" aria-describedby="party-person-contact-help">
        <legend>Kişi iletişim bilgileri</legend>
        <p id="party-person-contact-help" class="section-note">
          Bu kişinin doğrudan telefon ve e-posta bilgilerini girin.
        </p>
        <div class="form-grid">
          <label class="field"
            ><span>Telefon</span><Input
              bind:value={value.contacts[0].phone}
              type="tel"
              autocomplete="tel"
              {disabled}
              placeholder="05xx xxx xx xx"
            /></label
          >
          <label class="field"
            ><span>E-posta</span><Input
              bind:value={value.contacts[0].email}
              type="email"
              autocomplete="email"
              {disabled}
            /></label
          >
        </div>
      </fieldset>
    {/if}
    {#if value.kind === 'ORGANIZATION'}
      <section class="form-section" aria-labelledby="party-contact-heading">
        <div class="section-heading compact-heading">
          <div>
            <h2 id="party-contact-heading">İletişim</h2>
          </div>
          <button class="inline-action" type="button" onclick={addContact} {disabled}
            >+ Kişi ekle</button
          >
        </div>
        {#each value.contacts as contact, index}
          <div class="contact-card">
            <div class="contact-card-header">
              <strong
                >{contact.is_primary ? 'Birincil kişi' : `İletişim kişisi ${index + 1}`}</strong
              >
              <div class="contact-actions">
                {#if !contact.is_primary}<button
                    class="inline-action"
                    type="button"
                    onclick={() => makePrimaryContact(index)}
                    {disabled}>Birincil yap</button
                  >{/if}
                {#if value.contacts.length > 1}<button
                    class="inline-action danger-action"
                    type="button"
                    onclick={() => removeContact(index)}
                    {disabled}>Kaldır</button
                  >{/if}
              </div>
            </div>
            <div class="form-grid">
              <label class="field"
                ><span>Ad soyad</span><Input
                  bind:value={contact.full_name}
                  {disabled}
                  autocomplete="name"
                /></label
              >
              <label class="field"
                ><span>Görevi</span><Input bind:value={contact.title} {disabled} /></label
              >
              <label class="field"
                ><span>Departman</span><Input bind:value={contact.department} {disabled} /></label
              >
              <label class="field"
                ><span>Telefon</span><Input
                  bind:value={contact.phone}
                  type="tel"
                  autocomplete="tel"
                  {disabled}
                /></label
              >
              <label class="field wide"
                ><span>E-posta</span><Input
                  bind:value={contact.email}
                  type="email"
                  autocomplete="email"
                  {disabled}
                /></label
              >
            </div>
          </div>
        {/each}
      </section>
    {/if}

    <section class="form-section address-section" aria-labelledby="party-address-heading">
      <div class="section-heading">
        <div>
          <h2 id="party-address-heading">Adres</h2>
        </div>
      </div>

      {#each value.addresses.slice(0, 1) as address, index}
        <fieldset class="address-card" aria-labelledby={`address-heading-${index}`}>
          <legend id={`address-heading-${index}`}>Adres</legend>
          <div class="form-grid">
            <label class="field"
              ><span>İl <b aria-hidden="true">*</b></span><select
                id={`party-province-${index}`}
                value={address.city_id ?? ''}
                onchange={(event) => void changeCity(index, selectValue(event))}
                {disabled}
                required={Boolean(
                  address.address_line ||
                  address.district ||
                  address.neighborhood ||
                  address.city ||
                  address.district_id ||
                  address.neighborhood_id
                )}
                aria-invalid={Boolean(errors[`party-province-${index}`])}
                aria-describedby={`city-help-${index}${errors[`party-province-${index}`] ? ` party-province-${index}-error` : ''}`}
                ><option value="">İl seçin</option>{#each provinces as province}<option
                    value={province.id}>{province.name}</option
                  >{/each}</select
              ><small id={`city-help-${index}`} class="default-action"
                ><button
                  type="button"
                  onclick={() => void saveDefault('city', index)}
                  disabled={disabled || !address.city_id || defaultSaving[`city:${index}`]}
                  >{defaultSaving[`city:${index}`] ? 'Kaydediliyor…' : 'Varsayılan il yap'}</button
                ></small
              >{#if errors[`party-province-${index}`]}<small
                  id={`party-province-${index}-error`}
                  class="field-error"
                  role="alert">{errors[`party-province-${index}`]}</small
                >{/if}
            </label>
            <label class="field"
              ><span>İlçe</span
              >{#if districts[index]?.length || locationLoading[locationKey('district', index)] || !address.city_id}<select
                  value={address.district_id ?? ''}
                  onchange={(event) => void changeDistrict(index, selectValue(event))}
                  disabled={disabled ||
                    !address.city_id ||
                    locationLoading[locationKey('district', index)]}
                  aria-describedby={`district-help-${index}`}
                  ><option value=""
                    >{locationLoading[locationKey('district', index)]
                      ? 'İlçeler yükleniyor…'
                      : 'İlçe seçin'}</option
                  >{#each districts[index] ?? [] as district}<option value={district.id}
                      >{district.name}</option
                    >{/each}</select
                >{:else}<Input
                  value={address.district}
                  oninput={(event) =>
                    updateAddress(index, {
                      district: (event.currentTarget as HTMLInputElement).value,
                      district_id: '',
                      neighborhood: '',
                      neighborhood_id: ''
                    })}
                  {disabled}
                  placeholder="İlçeyi elle yazın"
                />{/if}<small id={`district-help-${index}`} class="default-action"
                ><button
                  type="button"
                  onclick={() => void saveDefault('district', index)}
                  disabled={disabled || !address.district || defaultSaving[`district:${index}`]}
                  >{defaultSaving[`district:${index}`]
                    ? 'Kaydediliyor…'
                    : 'Varsayılan ilçe yap'}</button
                ></small
              >{#if locationError[locationKey('district', index)]}<small class="location-error"
                  >{locationError[locationKey('district', index)]}</small
                >{/if}</label
            >
            <label class="field"
              ><span>Mahalle</span
              >{#if neighborhoods[index]?.length || locationLoading[locationKey('neighborhood', index)] || !(address.district_id || address.district)}<select
                  value={address.neighborhood_id ?? ''}
                  onchange={(event) => changeNeighborhood(index, selectValue(event))}
                  disabled={disabled ||
                    !(address.district_id || address.district) ||
                    locationLoading[locationKey('neighborhood', index)]}
                  aria-describedby={`neighborhood-help-${index}`}
                  ><option value=""
                    >{locationLoading[locationKey('neighborhood', index)]
                      ? 'Mahalleler yükleniyor…'
                      : 'Mahalle seçin'}</option
                  >{#each neighborhoods[index] ?? [] as neighborhood}<option value={neighborhood.id}
                      >{neighborhood.name}</option
                    >{/each}</select
                >{:else}<Input
                  value={address.neighborhood}
                  oninput={(event) =>
                    updateAddress(index, {
                      neighborhood: (event.currentTarget as HTMLInputElement).value,
                      neighborhood_id: ''
                    })}
                  {disabled}
                  placeholder="Mahalle seçin veya yazın"
                />{/if}<small id={`neighborhood-help-${index}`} class="default-action"
                ><button
                  type="button"
                  onclick={() => void saveDefault('neighborhood', index)}
                  disabled={disabled ||
                    !address.neighborhood ||
                    defaultSaving[`neighborhood:${index}`]}
                  >{defaultSaving[`neighborhood:${index}`]
                    ? 'Kaydediliyor…'
                    : 'Varsayılan mahalle yap'}</button
                ></small
              >{#if locationError[locationKey('neighborhood', index)]}<small class="location-error"
                  >{locationError[locationKey('neighborhood', index)]}</small
                >{/if}</label
            >
          </div>
          <details class="address-details" open>
            <summary>Adres bilgileri</summary>
            <label class="field wide"
              ><span>Adres</span><textarea
                class="address-textarea"
                bind:value={address.address_line}
                {disabled}
                rows="6"
                placeholder="Mahalle, sokak/cadde, bina ve kapı numarası"
              ></textarea></label
            >
          </details>
        </fieldset>
      {/each}
      {#if defaultMessage}<p class="form-status" role="status">{defaultMessage}</p>{/if}
      {#if optionsLoading}<p class="form-status" role="status">
          Cari seçenekleri hazırlanıyor…
        </p>{/if}
      {#if optionsError}<p class="form-status warning" role="status">{optionsError}</p>{/if}
    </section>
  </div>
{:else}
  <div
    class="party-tabpanel"
    id="party-commercial-panel"
    role="tabpanel"
    aria-labelledby="party-commercial-tab"
    tabindex="0"
  >
    <details class="advanced-section" open>
      <summary>Ticari ayarlar</summary>
      <div class="form-grid nested-grid">
        <label class="field"
          ><span>Varsayılan para birimi</span><CurrencySelect
            bind:value={value.default_currency}
            {disabled}
            ariaLabel="Varsayılan para birimi"
          /></label
        >
        <label class="field"
          ><span>Ödeme koşulu</span><select bind:value={value.payment_term_id} {disabled}
            ><option value="">Peşin (vade yok)</option>{#each paymentTerms as term}<option
                value={term.id}>{term.name} · {term.due_days} gün</option
              >{/each}</select
          ></label
        >
        <label class="field"
          ><span>Satış temsilcisi</span><select bind:value={value.sales_rep_user_id} {disabled}
            ><option value="">Seçilmedi</option>{#each members as member}<option
                value={member.user.id}>{member.user.display_name}</option
              >{/each}</select
          ></label
        >
        <fieldset class="group-field wide" aria-describedby="party-group-help party-group-status">
          <legend>Cari grupları</legend>
          <div id="party-group-status" class="group-selection-summary" role="status">
            {selectedGroupCount
              ? `${selectedGroupCount} cari grubu seçildi.`
              : 'Henüz cari grubu seçilmedi.'}
          </div>
          <div class="group-picker" aria-label="Cari grubu listesi">
            <div class="group-picker-header" aria-hidden="true">
              <span>Seçim</span>
              <span>Kod</span>
              <span>Cari grubu</span>
              <span>Durum</span>
            </div>
            {#if groups.length}
              {#each groups as group (group.id)}
                {@const selected = value.group_ids.includes(group.id)}
                <label class="group-option" class:selected>
                  <input
                    type="checkbox"
                    checked={selected}
                    onchange={(event) =>
                      toggleGroup(group.id, (event.currentTarget as HTMLInputElement).checked)}
                    {disabled}
                    aria-label={`${group.code} ${group.name}${selected ? ' - seçili' : ' - seçili değil'}`}
                  />
                  <span class="group-code">{group.code}</span>
                  <span class="group-name">{group.name}</span>
                  <span class="group-row-status">{selected ? 'Seçili' : 'Seçili değil'}</span>
                </label>
              {/each}
            {:else}<small class="group-empty">Henüz cari grubu tanımlanmadı.</small>{/if}
          </div>
          <a class="settings-link" href="/ayarlar/cari-gruplari">Cari gruplarını yönet</a>
        </fieldset>
      </div>
    </details>
  </div>
{/if}

<style>
  .form-section {
    padding: 16px;
    border-bottom: 1px solid var(--border);
  }
  .form-section:first-child {
    padding-top: 16px;
  }
  .section-heading {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    margin-bottom: 12px;
  }
  .compact-heading {
    margin-bottom: 10px;
  }
  .section-note {
    margin: 3px 0 0;
    color: var(--text-muted);
    font-size: 10.5px;
  }
  .inline-action {
    padding: 2px 0;
    border: 0;
    background: transparent;
    color: var(--primary);
    cursor: pointer;
    font: inherit;
    font-size: 10.5px;
  }
  .inline-action:hover:not(:disabled) {
    text-decoration: underline;
  }
  .inline-action:disabled {
    cursor: not-allowed;
    opacity: 0.45;
  }
  .danger-action {
    color: var(--danger);
  }
  .contact-card {
    margin-top: 10px;
    padding: 10px;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    background: var(--surface-muted);
  }
  .contact-card-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 10px;
    margin-bottom: 9px;
    color: var(--text);
    font-size: 11px;
  }
  .contact-actions {
    display: flex;
    gap: 10px;
  }
  h2 {
    margin: 0;
    color: var(--text);
    font-size: 13px;
    font-weight: 750;
    letter-spacing: -0.01em;
  }
  .form-grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 12px;
  }
  .field {
    display: flex;
    min-width: 0;
    flex-direction: column;
    gap: 4px;
  }
  .field > span {
    display: flex;
    align-items: baseline;
    gap: 3px;
    color: var(--text-subtle);
    font-size: 11px;
    font-weight: 650;
  }
  .field b {
    color: var(--danger);
    font-weight: 750;
  }
  .field small {
    color: var(--text-muted);
    font-size: 10.5px;
    line-height: 1.35;
  }
  .active-party-field {
    display: flex;
    align-items: center;
    align-self: end;
    gap: 8px;
    min-height: var(--control-height);
    margin-top: 17px;
    padding: 0 10px;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    background: var(--surface-muted);
    color: var(--text);
    font-size: 13px;
  }
  .active-party-field input {
    flex: 0 0 auto;
    accent-color: var(--primary);
  }
  .active-party-field.disabled {
    opacity: 0.7;
  }
  .person-contact-card {
    min-width: 0;
    margin: 0 0 12px;
    padding: 12px;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    background: var(--surface-muted);
  }
  .person-contact-card legend {
    padding: 0 5px;
    color: var(--text);
    font-size: 12px;
    font-weight: 750;
  }
  .person-contact-card .section-note {
    margin: 0 0 10px;
  }
  .wide {
    grid-column: 1 / -1;
  }
  .address-card {
    min-width: 0;
    margin: 0 0 12px;
    padding: 12px;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    background: var(--surface-muted);
  }
  .address-card legend {
    padding: 0 5px;
    color: var(--text);
    font-size: 11px;
    font-weight: 750;
  }
  .default-action {
    min-height: 15px;
  }
  .default-action button {
    padding: 0;
    border: 0;
    background: transparent;
    color: var(--primary);
    cursor: pointer;
    font: inherit;
    font-size: 10px;
    text-align: left;
  }
  .default-action button:hover:not(:disabled) {
    text-decoration: underline;
  }
  .default-action button:disabled {
    cursor: not-allowed;
    opacity: 0.45;
  }
  .location-error {
    color: var(--danger);
    font-size: 10px;
    line-height: 1.3;
  }
  .address-details {
    margin-top: 12px;
    padding-top: 10px;
    border-top: 1px solid var(--border);
  }
  .address-details summary {
    color: var(--text-subtle);
    font-size: 11px;
  }
  .address-details[open] summary {
    margin-bottom: 12px;
  }
  .address-textarea {
    min-height: 150px;
    resize: vertical;
    line-height: 1.45;
  }
  .form-status {
    margin: 4px 0 0;
    color: var(--success);
    font-size: 10.5px;
  }
  .form-status.warning {
    color: var(--text-muted);
  }
  select {
    width: 100%;
    height: var(--control-height);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    background: var(--surface);
    color: var(--text);
    padding: 0 8px;
    font-size: 13px;
  }
  .address-card select {
    appearance: none;
    -webkit-appearance: none;
    background-image: none;
  }
  select:focus-visible {
    outline: 2px solid var(--focus);
    outline-offset: 1px;
  }
  .roles-section {
    border-bottom: 1px solid var(--border);
  }
  .roles-section legend {
    padding: 0;
    color: var(--text);
    font-size: 13px;
    font-weight: 750;
  }
  .role-options {
    display: flex;
    flex-wrap: wrap;
    gap: 20px;
    margin-top: 10px;
  }
  .role-options label {
    display: inline-flex;
    align-items: center;
    gap: 7px;
    color: var(--text);
    font-size: 13px;
  }
  .advanced-section {
    padding: 14px 16px;
    border-bottom: 1px solid var(--border);
  }
  .advanced-section:last-child {
    border-bottom: 0;
  }
  summary {
    cursor: pointer;
    color: var(--text);
    font-size: 12px;
    font-weight: 750;
    outline: none;
  }
  summary:focus-visible {
    border-radius: 3px;
    box-shadow: 0 0 0 2px var(--focus);
  }
  .nested-grid {
    margin-top: 12px;
  }
  .group-field {
    min-width: 0;
    margin: 0;
    padding: 0;
    border: 0;
  }
  .group-field legend {
    padding: 0;
    color: var(--text);
    font-size: 12px;
    font-weight: 750;
  }
  .group-help {
    margin: 3px 0 8px;
    color: var(--text-muted);
    font-size: 10.5px;
    line-height: 1.35;
  }
  .group-selection-summary {
    min-height: 28px;
    margin-bottom: 7px;
    padding: 6px 8px;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    background: var(--surface-muted);
    color: var(--text-subtle);
    font-size: 11px;
    font-weight: 650;
  }
  .group-picker {
    min-width: 0;
    overflow: hidden;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    background: var(--surface);
  }
  .group-picker-header,
  .group-option {
    display: grid;
    grid-template-columns: 52px minmax(90px, 0.3fr) minmax(0, 1fr) 74px;
    align-items: center;
    gap: 8px;
  }
  .group-picker-header {
    min-height: 30px;
    padding: 0 9px;
    border-bottom: 1px solid var(--border);
    background: var(--surface-muted);
    color: var(--text-muted);
    font-size: 10px;
    font-weight: 750;
    letter-spacing: 0.02em;
    text-transform: uppercase;
  }
  .group-option {
    min-width: 0;
    min-height: 42px;
    padding: 5px 9px 5px 6px;
    border: 0;
    border-left: 3px solid transparent;
    border-bottom: 1px solid var(--border);
    background: var(--surface);
    color: var(--text);
    font-size: 12px;
    cursor: pointer;
  }
  .group-option:last-child {
    border-bottom: 0;
  }
  .group-option:hover {
    background: var(--surface);
  }
  .group-option.selected {
    border-left-color: var(--primary);
    background: var(--primary-soft);
  }
  .group-option:focus-within {
    position: relative;
    z-index: 1;
    outline: 2px solid var(--focus);
    outline-offset: -2px;
  }
  .group-option input {
    justify-self: start;
    margin: 0;
  }
  .group-code {
    min-width: 0;
    overflow: hidden;
    color: var(--primary);
    font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
    font-size: 10.5px;
    font-weight: 750;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .group-name {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .group-row-status {
    color: var(--text-muted);
    font-size: 10px;
    font-weight: 700;
    white-space: nowrap;
  }
  .group-option.selected .group-row-status {
    color: var(--primary);
  }
  .group-empty {
    display: block;
    padding: 10px 9px;
    color: var(--text-muted);
    font-size: 11px;
  }
  .settings-link {
    display: inline-block;
    margin-top: 7px;
    color: var(--primary);
    font-size: 10.5px;
    text-decoration: none;
  }
  .settings-link:hover {
    text-decoration: underline;
  }
  @media (max-width: 640px) {
    .form-grid {
      grid-template-columns: 1fr;
    }
    .wide {
      grid-column: auto;
    }
    .group-picker-header,
    .group-option {
      grid-template-columns: 46px 72px minmax(0, 1fr) 64px;
    }
  }
</style>
