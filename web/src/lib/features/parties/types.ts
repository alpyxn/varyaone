const partyGroupOrder = ['PERAKENDE', 'TOPTAN', 'BAYI', 'HIZMET_TED', 'MALZEME_TED'];

export function sortPartyGroups<T extends { code: string }>(groups: T[]): T[] {
  return [...groups].sort((left, right) => {
    const leftRank = partyGroupOrder.indexOf(left.code);
    const rightRank = partyGroupOrder.indexOf(right.code);
    const normalizedLeftRank = leftRank === -1 ? partyGroupOrder.length : leftRank;
    const normalizedRightRank = rightRank === -1 ? partyGroupOrder.length : rightRank;
    if (normalizedLeftRank !== normalizedRightRank) {
      return normalizedLeftRank - normalizedRightRank;
    }
    return left.code.localeCompare(right.code, 'tr');
  });
}

export type Address = {
  id?: string;
  address_line: string;
  district: string;
  neighborhood?: string;
  city: string;
  // Canonical API names are kept alongside the legacy city/district aliases
  // used by the form. This lets the local reference selection survive a
  // round-trip instead of falling back to free-text names.
  province_id?: string;
  province_name?: string;
  district_name?: string;
  neighborhood_name?: string;
  city_id?: string;
  district_id?: string;
  neighborhood_id?: string;
  is_default: boolean;
};
export type Contact = {
  id?: string;
  full_name: string;
  title: string;
  department: string;
  email: string;
  phone: string;
  is_primary: boolean;
};
export type PartyInput = {
  code: string;
  kind: 'PERSON' | 'ORGANIZATION';
  is_active: boolean;
  is_customer: boolean;
  is_supplier: boolean;
  display_name: string;
  legal_name: string;
  trade_name: string;
  first_name: string;
  last_name: string;
  tax_number: string;
  identity_number: string;
  tax_office: string;
  tax_office_id: string;
  default_currency: string;
  payment_term_id: string;
  price_list_id: string;
  default_discount_rate: string;
  sales_rep_user_id: string;
  credit_limit: string;
  risk_limit: string;
  risk_policy: 'ALLOW' | 'WARN' | 'BLOCK';
  addresses: Address[];
  contacts: Contact[];
  group_ids: string[];
  tags: string[];
  custom_fields: Record<string, unknown>;
};
export type Party = PartyInput & {
  id: string;
  is_active: boolean;
  version: number;
  phone: string;
  email: string;
  city: string;
  address_summary: string;
  contact_summary: string;
  group_summary: string;
  tag_summary: string;
  custom_field_summary: string;
  payment_term_name: string;
  sales_rep_name: string;
  balance: string;
  /** Company-base unit for the list/detail balance, not the party default. */
  balance_currency?: string;
  warnings?: string[];
  created_at?: string;
  updated_at?: string;
};
export type PartyList = { items: Party[]; next_cursor?: string };

export type PartyBalance = {
  currency: string;
  debit: string;
  credit: string;
  balance: string;
  base_balance?: string;
  base_currency?: string;
};

export type PartyLedgerEntry = {
  id: string;
  party_id: string;
  currency: string;
  entry_type: string;
  source_type: string;
  source_id: string;
  source_label?: string;
  source_href?: string;
  description: string;
  debit: string;
  credit: string;
  exchange_rate: string;
  document_date: string;
  due_date?: string;
  document_no?: string;
  reference_no?: string;
  posted_at: string;
  reversal_of_id?: string;
  running_balance?: string;
  snapshot: Record<string, unknown>;
};

export type PartyBalanceList = {
  items: PartyBalance[];
  /** Backend summary fields; optional for compatibility with older responses. */
  base_currency?: string;
  base_balance?: string;
};
export type PartyStatementList = { items: PartyLedgerEntry[] };

export type PartyStatementReport = {
  items: PartyLedgerEntry[];
  /** Present when the backend has another page for the selected period. */
  next_cursor?: string;
  currency?: string;
  opening_balance: string;
  closing_balance: string;
  total_debit: string;
  total_credit: string;
};

export type PartyOpenItem = {
  id: string;
  document_id: string;
  document_no?: string;
  party_id: string;
  side: 'RECEIVABLE' | 'PAYABLE';
  currency: string;
  original_amount: string;
  allocated_amount: string;
  open_amount: string;
  exchange_rate?: string;
  base_currency?: string;
  document_date: string;
  due_date?: string;
};

export type PartyOpenItemList = { items: PartyOpenItem[]; next_cursor?: string };

/** One party's open balance in one currency, split into due-date buckets. */
export type PartyAgingRow = {
  party_id: string;
  party_code: string;
  party_name: string;
  side: 'RECEIVABLE' | 'PAYABLE';
  currency: string;
  not_due: string;
  days_0_30: string;
  days_31_60: string;
  days_61_90: string;
  days_90_plus: string;
  overdue_total: string;
  total: string;
};

export type PartyAgingReport = { as_of: string; items: PartyAgingRow[] };

export type LocationOption = {
  id: string;
  name: string;
};

export type LocationSelection = {
  id: string;
  name: string;
  parent_id?: string;
};

export type TaxOfficeReference = {
  id: string;
  code: string | null;
  name: string;
  province_id: number;
  province_name: string;
  district_name: string;
  office_type: 'Vergi Dairesi Müdürlüğü' | 'Şube' | 'Malmüdürlüğü' | 'Defterdarlık';
  is_active: boolean;
};

export type PartyLocationDefaults = {
  city?: LocationSelection;
  district?: LocationSelection;
  neighborhood?: LocationSelection;
};

/**
 * Türkiye'nin 81 ili. İlçe ve mahalleler kullanıcı il seçtiğinde uzaktan
 * kademeli yüklenir; bu yerel liste ilk ekranı ağ bağlantısından bağımsız
 * kullanılabilir tutar.
 */
export const TURKEY_PROVINCES: LocationOption[] = [
  ['01', 'Adana'],
  ['02', 'Adıyaman'],
  ['03', 'Afyonkarahisar'],
  ['04', 'Ağrı'],
  ['05', 'Amasya'],
  ['06', 'Ankara'],
  ['07', 'Antalya'],
  ['08', 'Artvin'],
  ['09', 'Aydın'],
  ['10', 'Balıkesir'],
  ['11', 'Bilecik'],
  ['12', 'Bingöl'],
  ['13', 'Bitlis'],
  ['14', 'Bolu'],
  ['15', 'Burdur'],
  ['16', 'Bursa'],
  ['17', 'Çanakkale'],
  ['18', 'Çankırı'],
  ['19', 'Çorum'],
  ['20', 'Denizli'],
  ['21', 'Diyarbakır'],
  ['22', 'Edirne'],
  ['23', 'Elazığ'],
  ['24', 'Erzincan'],
  ['25', 'Erzurum'],
  ['26', 'Eskişehir'],
  ['27', 'Gaziantep'],
  ['28', 'Giresun'],
  ['29', 'Gümüşhane'],
  ['30', 'Hakkâri'],
  ['31', 'Hatay'],
  ['32', 'Isparta'],
  ['33', 'Mersin'],
  ['34', 'İstanbul'],
  ['35', 'İzmir'],
  ['36', 'Kars'],
  ['37', 'Kastamonu'],
  ['38', 'Kayseri'],
  ['39', 'Kırklareli'],
  ['40', 'Kırşehir'],
  ['41', 'Kocaeli'],
  ['42', 'Konya'],
  ['43', 'Kütahya'],
  ['44', 'Malatya'],
  ['45', 'Manisa'],
  ['46', 'Kahramanmaraş'],
  ['47', 'Mardin'],
  ['48', 'Muğla'],
  ['49', 'Muş'],
  ['50', 'Nevşehir'],
  ['51', 'Niğde'],
  ['52', 'Ordu'],
  ['53', 'Rize'],
  ['54', 'Sakarya'],
  ['55', 'Samsun'],
  ['56', 'Siirt'],
  ['57', 'Sinop'],
  ['58', 'Sivas'],
  ['59', 'Tekirdağ'],
  ['60', 'Tokat'],
  ['61', 'Trabzon'],
  ['62', 'Tunceli'],
  ['63', 'Şanlıurfa'],
  ['64', 'Uşak'],
  ['65', 'Van'],
  ['66', 'Yozgat'],
  ['67', 'Zonguldak'],
  ['68', 'Aksaray'],
  ['69', 'Bayburt'],
  ['70', 'Karaman'],
  ['71', 'Kırıkkale'],
  ['72', 'Batman'],
  ['73', 'Şırnak'],
  ['74', 'Bartın'],
  ['75', 'Ardahan'],
  ['76', 'Iğdır'],
  ['77', 'Yalova'],
  ['78', 'Karabük'],
  ['79', 'Kilis'],
  ['80', 'Osmaniye'],
  ['81', 'Düzce']
].map(([id, name]) => ({ id, name }));

export const emptyAddress = (): Address => ({
  address_line: '',
  district: '',
  neighborhood: '',
  city: '',
  is_default: true
});

export const emptyContact = (): Contact => ({
  full_name: '',
  title: '',
  department: '',
  email: '',
  phone: '',
  is_primary: true
});

export const emptyParty = (): PartyInput => ({
  code: '',
  kind: 'ORGANIZATION',
  is_active: true,
  is_customer: true,
  is_supplier: false,
  display_name: '',
  legal_name: '',
  trade_name: '',
  first_name: '',
  last_name: '',
  tax_number: '',
  identity_number: '',
  tax_office: '',
  tax_office_id: '',
  default_currency: 'TRY',
  payment_term_id: '',
  price_list_id: '',
  default_discount_rate: '0',
  sales_rep_user_id: '',
  credit_limit: '0',
  risk_limit: '0',
  risk_policy: 'WARN',
  addresses: [emptyAddress()],
  contacts: [emptyContact()],
  group_ids: [],
  tags: [],
  custom_fields: {}
});

function asText(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

function asID(value: unknown): string | undefined {
  if (typeof value === 'number' && Number.isFinite(value)) return String(value);
  if (typeof value === 'string' && value.trim()) return value.trim();
  return undefined;
}

function normalizeAddress(address: Address): Address {
  const cityID = asID(address?.city_id) ?? asID(address?.province_id);
  const districtID = asID(address?.district_id);
  const neighborhoodID = asID(address?.neighborhood_id);
  return {
    id: address?.id,
    address_line: asText(address?.address_line).trim(),
    district: asText(address?.district).trim() || asText(address?.district_name).trim(),
    neighborhood: asText(address?.neighborhood).trim() || asText(address?.neighborhood_name).trim(),
    city: asText(address?.city).trim() || asText(address?.province_name).trim(),
    province_id: cityID,
    province_name: asText(address?.province_name).trim(),
    district_name: asText(address?.district_name).trim(),
    neighborhood_name: asText(address?.neighborhood_name).trim(),
    city_id: cityID,
    district_id: districtID,
    neighborhood_id: neighborhoodID,
    is_default: Boolean(address?.is_default)
  };
}

/**
 * Convert a Turkish or API decimal string into the canonical dot-decimal form
 * expected by PostgreSQL/Go without going through a floating point number.
 */
export function normalizeDecimal(
  value: string | null | undefined,
  maxScale: number
): string | undefined {
  const raw = asText(value)
    .trim()
    .replace(/[\s\u00a0]/g, '');
  if (!raw) return '0';

  const canonical = raw.includes(',') ? raw.replace(/\./g, '').replace(',', '.') : raw;
  const match = /^(-?)(\d+)(?:\.(\d+))?$/.exec(canonical);
  if (!match || (match[3] && match[3].length > maxScale)) return undefined;

  const integer = match[2].replace(/^0+(?=\d)/, '');
  return `${match[1]}${integer}${match[3] ? `.${match[3]}` : ''}`;
}

export function normalizePartyInput(input: PartyInput): PartyInput {
  const kind = asText(input.kind).trim().toUpperCase() as PartyInput['kind'];
  const legalName = asText(input.legal_name).trim();
  const tradeName = asText(input.trade_name).trim();
  const firstName = asText(input.first_name).trim();
  const lastName = asText(input.last_name).trim();
  const explicitDisplayName = asText(input.display_name).trim();
  // The form exposes legal/trade name (or first/last name), not a separate
  // display-name field. Keep the list and detail heading in sync with those
  // visible fields when an existing card is edited.
  const displayName =
    (kind === 'ORGANIZATION'
      ? tradeName || legalName
      : [firstName, lastName].filter(Boolean).join(' ')) || explicitDisplayName;
  const contacts = (Array.isArray(input.contacts) ? input.contacts : [])
    .filter((contact) =>
      [
        contact?.full_name,
        contact?.title,
        contact?.department,
        contact?.email,
        contact?.phone
      ].some((field) => asText(field).trim())
    )
    .map((contact) => ({
      ...contact,
      full_name: asText(contact?.full_name).trim() || displayName,
      title: asText(contact?.title).trim(),
      department: asText(contact?.department).trim(),
      email: asText(contact?.email).trim().toLowerCase(),
      phone: asText(contact?.phone).trim(),
      is_primary: Boolean(contact?.is_primary)
    }))
    .filter((contact) => Boolean(contact.email || contact.phone));
  if (contacts.length && !contacts.some((contact) => contact.is_primary)) {
    contacts[0].is_primary = true;
  }
  const normalizedAddresses = (Array.isArray(input.addresses) ? input.addresses : [])
    .map(normalizeAddress)
    .filter((address) =>
      [
        address.address_line,
        address.district,
        address.neighborhood,
        address.city,
        address.city_id,
        address.district_id,
        address.neighborhood_id
      ].some(Boolean)
    );
  const primaryAddress =
    normalizedAddresses.find((address) => address.is_default) ?? normalizedAddresses[0];
  const addresses = primaryAddress ? [primaryAddress] : [];
  if (addresses.length && !addresses.some((address) => address.is_default)) {
    addresses[0].is_default = true;
  }
  return {
    ...input,
    code: asText(input.code).trim().toUpperCase(),
    kind,
    is_active: input.is_active !== false,
    is_customer: Boolean(input.is_customer),
    is_supplier: Boolean(input.is_supplier),
    display_name: displayName,
    legal_name: legalName,
    trade_name: tradeName,
    first_name: firstName,
    last_name: lastName,
    tax_number: asText(input.tax_number).replace(/[\s-]/g, ''),
    identity_number: asText(input.identity_number).replace(/[\s-]/g, ''),
    tax_office: asText(input.tax_office).trim(),
    tax_office_id: asText(input.tax_office_id).trim(),
    default_currency: asText(input.default_currency).trim().toUpperCase(),
    payment_term_id: asText(input.payment_term_id),
    price_list_id: asText(input.price_list_id),
    sales_rep_user_id: asText(input.sales_rep_user_id),
    default_discount_rate:
      normalizeDecimal(input.default_discount_rate, 6) ?? asText(input.default_discount_rate),
    credit_limit: normalizeDecimal(input.credit_limit, 4) ?? asText(input.credit_limit),
    risk_limit: normalizeDecimal(input.risk_limit, 4) ?? asText(input.risk_limit),
    risk_policy: asText(input.risk_policy).trim().toUpperCase() as PartyInput['risk_policy'],
    addresses,
    contacts,
    group_ids: Array.isArray(input.group_ids) ? input.group_ids : [],
    tags: Array.isArray(input.tags) ? input.tags : [],
    custom_fields:
      input.custom_fields &&
      typeof input.custom_fields === 'object' &&
      !Array.isArray(input.custom_fields)
        ? input.custom_fields
        : {}
  };
}

export function validatePartyInput(input: PartyInput): string | undefined {
  const normalized = normalizePartyInput(input);
  if (!normalized.is_customer && !normalized.is_supplier) {
    return 'En az bir cari rolü seçin: Müşteri veya Tedarikçi.';
  }
  if (normalized.default_currency.length !== 3) return 'Üç harfli bir para birimi seçin.';
  if (normalized.kind !== 'PERSON' && normalized.kind !== 'ORGANIZATION') {
    return 'Cari türü kişi veya kurum olmalıdır.';
  }
  if (normalized.kind === 'ORGANIZATION' && !normalized.legal_name) {
    return 'Resmî unvan zorunludur.';
  }
  if (normalized.kind === 'PERSON' && !normalized.first_name) {
    return 'Ad zorunludur.';
  }
  if (normalized.kind === 'PERSON' && !normalized.last_name) {
    return 'Soyad zorunludur.';
  }
  if (!normalized.display_name) {
    return 'Cari adı zorunludur.';
  }
  if (normalized.tax_number && !/^\d{10}$/.test(normalized.tax_number)) {
    return 'Vergi numarası 10 haneli olmalıdır.';
  }
  if (normalized.identity_number && !/^\d{11}$/.test(normalized.identity_number)) {
    return 'T.C. kimlik numarası 11 haneli olmalıdır.';
  }
  for (const contact of normalized.contacts) {
    if (contact.email && !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(contact.email)) {
      return 'E-posta adresi geçerli değil.';
    }
  }
  for (const address of normalized.addresses) {
    const hasAddressData = [
      address.address_line,
      address.district,
      address.neighborhood,
      address.city,
      address.city_id,
      address.district_id,
      address.neighborhood_id
    ].some(Boolean);
    if (hasAddressData && !address.city_id) {
      return 'Adres bilgisi gönderildiğinde il seçimi zorunludur.';
    }
  }
  const decimalFields: Array<[keyof PartyInput, string, number]> = [
    ['default_discount_rate', 'Varsayılan iskonto', 6],
    ['credit_limit', 'Kredi limiti', 4],
    ['risk_limit', 'Risk limiti', 4]
  ];
  for (const [field, label, scale] of decimalFields) {
    const value = normalizeDecimal(String(normalized[field]), scale);
    if (value === undefined) return `${label} geçerli bir sayı olmalıdır.`;
    if (value.startsWith('-')) return `${label} negatif olamaz.`;
  }
  if (!['ALLOW', 'WARN', 'BLOCK'].includes(normalized.risk_policy)) {
    return 'Risk politikası geçersiz.';
  }
  return undefined;
}

export function partyProvinceSelectionRequired(input: PartyInput): boolean {
  const normalized = normalizePartyInput(input);
  return normalized.addresses.some((address) => {
    const hasAddressData = [
      address.address_line,
      address.district,
      address.neighborhood,
      address.city,
      address.city_id,
      address.district_id,
      address.neighborhood_id
    ].some(Boolean);
    return hasAddressData && !address.city_id;
  });
}

export function isPartyProvinceValidationMessage(message: string): boolean {
  const normalized = message.toLocaleLowerCase('tr-TR');
  return normalized.includes('il seçimi') || normalized.includes('ilçe ve mahalle için il');
}

export function partyToInput(party: Party): PartyInput {
  const {
    id: _id,
    version: _version,
    phone: _phone,
    email: _email,
    city: _city,
    address_summary: _addressSummary,
    contact_summary: _contactSummary,
    group_summary: _groupSummary,
    tag_summary: _tagSummary,
    custom_field_summary: _customFieldSummary,
    payment_term_name: _paymentTermName,
    sales_rep_name: _salesRepName,
    balance: _balance,
    balance_currency: _balanceCurrency,
    warnings: _warnings,
    created_at: _created,
    updated_at: _updated,
    ...input
  } = party;
  const normalized = normalizePartyInput(input);
  return {
    ...normalized,
    is_active: party.is_active,
    addresses: normalized.addresses.length ? normalized.addresses : [emptyAddress()],
    contacts: normalized.contacts.length ? normalized.contacts : [emptyContact()]
  };
}
