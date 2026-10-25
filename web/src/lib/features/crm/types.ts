export const leadStatuses = ['NEW', 'CONTACTED', 'QUALIFIED', 'DISQUALIFIED', 'CONVERTED'] as const;

/** A is the hottest lead, E one that was ruled out; '' means not graded. */
export const leadPriorities = ['A', 'B', 'C', 'D', 'E'] as const;
export type LeadPriority = (typeof leadPriorities)[number] | '';

export function priorityLabel(priority: string): string {
  switch (priority) {
    case 'A':
      return 'A · Çok sıcak';
    case 'B':
      return 'B · Sıcak';
    case 'C':
      return 'C · Ilık';
    case 'D':
      return 'D · Düşük';
    case 'E':
      return 'E · Elendi';
    default:
      return 'Derecesiz';
  }
}
export type LeadStatus = (typeof leadStatuses)[number];

/** Statuses a user may set directly on the card. CONVERTED is reached only by opening a cari. */
export const editableLeadStatuses = ['NEW', 'CONTACTED', 'QUALIFIED', 'DISQUALIFIED'] as const;

/** Call results in the order the call screen offers them; the index + 1 is the keyboard shortcut. */
export const callOutcomes = [
  'NO_ANSWER',
  'BUSY',
  'VOICEMAIL',
  'WRONG_NUMBER',
  'CALLBACK',
  'INTERESTED',
  'MEETING_SET',
  'NOT_INTERESTED',
  'DO_NOT_CALL'
] as const;
export type CallOutcome = (typeof callOutcomes)[number];

export type ActivityKind = 'CALL' | 'MEETING' | 'EMAIL' | 'NOTE' | 'TASK';

export type Lead = {
  id: string;
  display_name: string;
  contact_name: string;
  contact_title: string;
  phone: string;
  phone2: string;
  email: string;
  city: string;
  district: string;
  sector: string;
  source: string;
  notes: string;
  address: string;
  website: string;
  priority: LeadPriority;
  status: LeadStatus;
  owner_user_id?: string | null;
  owner_name: string;
  next_action_at?: string | null;
  do_not_call: boolean;
  do_not_call_reason: string;
  do_not_call_at?: string | null;
  privacy_notice_at?: string | null;
  call_attempts: number;
  last_call_at?: string | null;
  last_call_outcome?: CallOutcome | null;
  converted_party_id?: string | null;
  converted_at?: string | null;
  created_at: string;
  updated_at: string;
  version: number;
};

export type LeadInput = {
  display_name: string;
  contact_name: string;
  contact_title: string;
  phone: string;
  phone2: string;
  email: string;
  city: string;
  district: string;
  sector: string;
  source: string;
  notes: string;
  address: string;
  website: string;
  priority: LeadPriority;
  status: LeadStatus;
  owner_user_id: string;
  next_action_at: string;
  privacy_notice: boolean;
  allow_duplicate?: boolean;
};

export type LeadSummary = {
  id: string;
  display_name: string;
  phone: string;
  status: LeadStatus;
  do_not_call: boolean;
  owner_name: string;
};

export type LeadList = { items: Lead[]; next_cursor?: string };

export type Activity = {
  id: string;
  lead_id?: string | null;
  lead_name?: string;
  party_id?: string | null;
  party_name?: string;
  opportunity_id?: string | null;
  kind: ActivityKind;
  subject: string;
  body: string;
  call_outcome?: CallOutcome | null;
  duration_seconds?: number | null;
  occurred_at: string;
  due_at?: string | null;
  remind_minutes: number;
  done_at?: string | null;
  assigned_user_id?: string | null;
  assigned_name: string;
  created_by: string;
  created_by_name: string;
  created_at: string;
  version: number;
};

export type ActivityInput = {
  kind: Exclude<ActivityKind, 'CALL'> | 'CALL';
  subject: string;
  body: string;
  call_outcome?: CallOutcome | '';
  duration_seconds?: number | null;
  occurred_at?: string;
  due_at?: string;
  remind_minutes?: number;
  assigned_user_id?: string;
};

export type CallInput = {
  call_list_item_id?: string;
  outcome: CallOutcome;
  note: string;
  duration_seconds?: number | null;
  callback_at?: string;
  remind_minutes?: number;
};

export type CallResult = { lead: Lead; activity: Activity; task?: Activity | null };

export type Member = { user_id: string; display_name: string };

export function leadStatusLabel(status: string): string {
  switch (status) {
    case 'NEW':
      return 'Yeni';
    case 'CONTACTED':
      return 'Görüşüldü';
    case 'QUALIFIED':
      return 'İlgili';
    case 'DISQUALIFIED':
      return 'Elendi';
    case 'CONVERTED':
      return 'Cariye dönüştü';
    default:
      return status;
  }
}

export function callOutcomeLabel(outcome: string): string {
  switch (outcome) {
    case 'NO_ANSWER':
      return 'Açmadı';
    case 'BUSY':
      return 'Meşgul';
    case 'VOICEMAIL':
      return 'Telesekreter';
    case 'WRONG_NUMBER':
      return 'Yanlış numara';
    case 'CALLBACK':
      return 'Tekrar ara';
    case 'INTERESTED':
      return 'İlgileniyor';
    case 'MEETING_SET':
      return 'Toplantı ayarlandı';
    case 'NOT_INTERESTED':
      return 'İlgilenmiyor';
    case 'DO_NOT_CALL':
      return 'Aranmasın';
    default:
      return outcome;
  }
}

/** Tone used for an outcome button and badge. */
export function callOutcomeTone(outcome: string): 'neutral' | 'positive' | 'negative' | 'pending' {
  switch (outcome) {
    case 'INTERESTED':
    case 'MEETING_SET':
      return 'positive';
    case 'NOT_INTERESTED':
    case 'DO_NOT_CALL':
    case 'WRONG_NUMBER':
      return 'negative';
    case 'CALLBACK':
      return 'pending';
    default:
      return 'neutral';
  }
}

export function activityKindLabel(kind: string): string {
  switch (kind) {
    case 'CALL':
      return 'Arama';
    case 'MEETING':
      return 'Toplantı';
    case 'EMAIL':
      return 'E-posta';
    case 'NOTE':
      return 'Not';
    case 'TASK':
      return 'Görev';
    default:
      return kind;
  }
}

export function emptyLeadInput(): LeadInput {
  return {
    display_name: '',
    contact_name: '',
    contact_title: '',
    phone: '',
    phone2: '',
    email: '',
    city: '',
    district: '',
    sector: '',
    source: '',
    notes: '',
    address: '',
    website: '',
    priority: '',
    status: 'NEW',
    owner_user_id: '',
    next_action_at: '',
    privacy_notice: false
  };
}

export function toLeadInput(lead: Lead): LeadInput {
  return {
    display_name: lead.display_name,
    contact_name: lead.contact_name,
    contact_title: lead.contact_title,
    phone: lead.phone,
    phone2: lead.phone2,
    email: lead.email,
    city: lead.city,
    district: lead.district,
    sector: lead.sector,
    source: lead.source,
    notes: lead.notes,
    address: lead.address,
    website: lead.website,
    priority: lead.priority,
    status: lead.status,
    owner_user_id: lead.owner_user_id ?? '',
    next_action_at: lead.next_action_at ? toLocalInput(lead.next_action_at) : '',
    privacy_notice: Boolean(lead.privacy_notice_at)
  };
}

/** `tel:` link target: keeps a leading + and the digits. */
export function telHref(phone: string): string {
  const trimmed = phone.trim();
  const digits = trimmed.replace(/[^\d]/g, '');
  return `tel:${trimmed.startsWith('+') ? '+' : ''}${digits}`;
}

/** ISO timestamp → value for an `<input type="datetime-local">` in the browser's zone. */
export function toLocalInput(iso: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return '';
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

/** `<input type="datetime-local">` value → RFC 3339 instant, or '' when empty/invalid. */
export function fromLocalInput(value: string): string {
  if (!value) return '';
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? '' : date.toISOString().replace(/\.\d{3}Z$/, 'Z');
}

/** Seconds → "m:ss" (or "h:mm:ss"). */
export function formatDuration(seconds?: number | null): string {
  if (seconds == null) return '';
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  const s = seconds % 60;
  const ss = String(s).padStart(2, '0');
  return h ? `${h}:${String(m).padStart(2, '0')}:${ss}` : `${m}:${ss}`;
}

/** Quick callback presets offered next to the date picker. */
export function callbackPreset(preset: 'hour' | 'tomorrow' | 'week', now = new Date()): Date {
  const date = new Date(now);
  if (preset === 'hour') {
    date.setHours(date.getHours() + 2, 0, 0, 0);
  } else if (preset === 'tomorrow') {
    date.setDate(date.getDate() + 1);
    date.setHours(10, 0, 0, 0);
  } else {
    date.setDate(date.getDate() + 7);
    date.setHours(10, 0, 0, 0);
  }
  return date;
}

// --- Arama listeleri ---------------------------------------------------------

export type CallScript = {
  id: string;
  name: string;
  body: string;
  is_active: boolean;
  created_at: string;
  updated_at: string;
  version: number;
};

export type CallList = {
  id: string;
  name: string;
  description: string;
  script_id?: string | null;
  script_name: string;
  retry_minutes: number;
  max_attempts: number;
  is_active: boolean;
  total: number;
  due: number;
  waiting: number;
  done: number;
  skipped: number;
  created_at: string;
  updated_at: string;
  version: number;
};

export type CallListInput = {
  name: string;
  description: string;
  script_id: string;
  retry_minutes: number;
  max_attempts: number;
  is_active?: boolean;
};

export type CallItemStatus = 'PENDING' | 'CALLBACK' | 'DONE' | 'SKIPPED';

export type CallListItem = {
  id: string;
  list_id: string;
  lead_id: string;
  lead_name: string;
  contact_name: string;
  phone: string;
  lead_status: LeadStatus;
  do_not_call: boolean;
  status: CallItemStatus;
  attempts: number;
  available_at: string;
  last_outcome?: CallOutcome | null;
  last_called_at?: string | null;
  claimed_by?: string | null;
  claimed_name: string;
  position: number;
};

export type NextCall = {
  list: CallList;
  item: CallListItem;
  lead: Lead;
  script?: CallScript | null;
  remaining: number;
};

/** Lead filter as the call-list screens send it; priority/status may be comma lists ("A,B"). */
export type LeadFilterInput = {
  q?: string;
  hide_closed?: boolean;
  never_called?: boolean;
  priority?: string;
  status?: string;
  owner?: string;
  source?: string;
  city?: string;
  district?: string;
  due?: boolean;
  do_not_call?: string;
};

/** What adding leads to a list did (or, for a preview, would do). */
export type AddLeadsResult = {
  matched: number;
  added: number;
  already_in_list: number;
  in_other_list: number;
  called_before: number;
  not_callable: number;
  skipped: number;
};

/** One line explaining an add result, e.g. "120 aday eklendi; 4 başka listede…". */
export function describeAddResult(result: AddLeadsResult, preview = false): string {
  const head = preview ? `${result.added} aday eklenecek` : `${result.added} aday eklendi`;
  const left: string[] = [];
  if (result.already_in_list) left.push(`${result.already_in_list} zaten bu listede`);
  if (result.in_other_list) left.push(`${result.in_other_list} başka bir listede sırada`);
  if (result.called_before) left.push(`${result.called_before} daha önce arandı`);
  if (result.not_callable)
    left.push(`${result.not_callable} aranamaz (elendi, aranmasın ya da cari)`);
  return left.length ? `${head}; dışarıda kalan: ${left.join(', ')}.` : `${head}.`;
}

export function callItemStatusLabel(status: string, availableAt?: string): string {
  switch (status) {
    case 'PENDING':
      return availableAt && new Date(availableAt).getTime() > Date.now()
        ? 'Tekrar denenecek'
        : 'Aranacak';
    case 'CALLBACK':
      return 'Geri aranacak';
    case 'DONE':
      return 'Tamamlandı';
    case 'SKIPPED':
      return 'Atlandı';
    default:
      return status;
  }
}

/** Minutes → "2 saat", "1 gün", "45 dk". */
export function formatMinutes(minutes: number): string {
  if (minutes % 1440 === 0) return `${minutes / 1440} gün`;
  if (minutes % 60 === 0) return `${minutes / 60} saat`;
  return `${minutes} dk`;
}

// --- İçe aktarma -------------------------------------------------------------

export const importFields = [
  { key: 'display_name', label: 'Firma / kişi adı', required: true },
  { key: 'contact_name', label: 'Yetkili' },
  { key: 'contact_title', label: 'Ünvanı' },
  { key: 'phone', label: 'Telefon' },
  { key: 'phone2', label: 'İkinci telefon' },
  { key: 'email', label: 'E-posta' },
  { key: 'city', label: 'İl' },
  { key: 'district', label: 'İlçe' },
  { key: 'address', label: 'Adres' },
  { key: 'website', label: 'Web sitesi' },
  { key: 'sector', label: 'Sektör' },
  { key: 'source', label: 'Kaynak' },
  { key: 'priority', label: 'Öncelik (A–E)' },
  { key: 'status', label: 'Arama durumu' },
  { key: 'notes', label: 'Not' }
] as const;
export type ImportFieldKey = (typeof importFields)[number]['key'];

export type ImportPreview = {
  sheets: { name: string; rows: number }[];
  sheet: string;
  headers: string[];
  rows: string[][];
  mapping: Record<string, number>;
  total: number;
};

export type ImportRow = { row: number } & Partial<Record<ImportFieldKey, string>>;

export type ImportInput = {
  rows: ImportRow[];
  owner_user_id: string;
  source: string;
  call_list_id: string;
  allow_duplicates: boolean;
};

export type ImportIssue = { row: number; name: string; message: string };

export type ImportResult = {
  created: number;
  added_to_list: number;
  duplicates: ImportIssue[];
  do_not_call: ImportIssue[];
  invalid: ImportIssue[];
};

/** Builds import rows from a parsed file and a field → column mapping. */
export function mapImportRows(
  rows: string[][],
  mapping: Partial<Record<ImportFieldKey, number>>
): ImportRow[] {
  return rows.map((values, index) => {
    const row: ImportRow = { row: index + 2 };
    for (const field of importFields) {
      const column = mapping[field.key];
      if (column != null && column >= 0) row[field.key] = (values[column] ?? '').trim();
    }
    return row;
  });
}

// --- Fırsatlar ---------------------------------------------------------------

export type StageKind = 'OPEN' | 'WON' | 'LOST';

export type Stage = {
  id: string;
  name: string;
  sort_order: number;
  probability: number;
  kind: StageKind;
  is_active: boolean;
  version: number;
};

export type StageInput = {
  name: string;
  sort_order: number;
  probability: number;
  kind: StageKind;
  is_active?: boolean;
};

export type Opportunity = {
  id: string;
  title: string;
  lead_id?: string | null;
  lead_name: string;
  party_id?: string | null;
  party_name: string;
  stage_id: string;
  stage_name: string;
  stage_kind: StageKind;
  probability: number;
  amount: string;
  currency: string;
  expected_close_date?: string | null;
  owner_user_id?: string | null;
  owner_name: string;
  notes: string;
  lost_reason: string;
  quote_id?: string | null;
  quote_no: string;
  quote_status: string;
  quote_total: string;
  quote_currency: string;
  closed_at?: string | null;
  created_at: string;
  updated_at: string;
  version: number;
};

export type OpportunityInput = {
  title: string;
  lead_id?: string;
  party_id?: string;
  stage_id?: string;
  amount: string;
  currency: string;
  expected_close_date: string;
  owner_user_id: string;
  notes: string;
};

export function stageKindLabel(kind: string): string {
  switch (kind) {
    case 'WON':
      return 'Kazanıldı';
    case 'LOST':
      return 'Kaybedildi';
    default:
      return 'Açık';
  }
}

export function quoteStatusLabel(status: string): string {
  switch (status) {
    case 'DRAFT':
      return 'Taslak';
    case 'SENT':
      return 'Gönderildi';
    case 'ACCEPTED':
      return 'Kabul edildi';
    case 'REJECTED':
      return 'Reddedildi';
    case 'EXPIRED':
      return 'Süresi doldu';
    case 'CANCELLED':
      return 'İptal';
    default:
      return status;
  }
}

/** Sums amounts per currency without floating-point drift (4 decimals). */
export function sumByCurrency(
  items: { amount: string; currency: string }[]
): { currency: string; amount: string }[] {
  const totals = new Map<string, bigint>();
  for (const item of items) {
    const [whole, fraction = ''] = (item.amount || '0').split('.');
    const scaled = BigInt(whole || '0') * 10000n + BigInt((fraction + '0000').slice(0, 4));
    totals.set(item.currency, (totals.get(item.currency) ?? 0n) + scaled);
  }
  return [...totals.entries()].map(([currency, value]) => {
    const whole = value / 10000n;
    const fraction = (value % 10000n).toString().padStart(4, '0').replace(/0+$/, '');
    return { currency, amount: fraction ? `${whole}.${fraction}` : `${whole}` };
  });
}

// --- Raporlar ----------------------------------------------------------------

export type RepStats = {
  user_id: string;
  name: string;
  calls: number;
  reached: number;
  positive: number;
  meetings: number;
  talk_seconds: number;
  leads_created: number;
  won: number;
};

export type CountRow = { key: string; count: number };
export type DayRow = { day: string; calls: number; reached: number };
export type StageRow = {
  stage_id: string;
  name: string;
  kind: StageKind;
  probability: number;
  currency: string;
  count: number;
  amount: string;
  weighted: string;
};
export type MoneyRow = { currency: string; count: number; amount: string };

export type Report = {
  from: string;
  to: string;
  reps: RepStats[];
  outcomes: CountRow[];
  days: DayRow[];
  lead_status: CountRow[];
  sources: CountRow[];
  pipeline: StageRow[];
  won: MoneyRow[];
  lost_reasons: CountRow[];
};

/** Percentage with no decimals, '—' when the base is zero. */
export function percent(part: number, whole: number): string {
  if (!whole) return '—';
  return `%${Math.round((part / whole) * 100)}`;
}

/** Link target for a website typed without a scheme ("ornek.com"). */
export function websiteHref(website: string): string {
  const trimmed = website.trim();
  return /^https?:\/\//i.test(trimmed) ? trimmed : `https://${trimmed}`;
}

// --- Aday listesi bağlamı ------------------------------------------------------

export type LeadView = 'all' | 'fresh' | 'mine' | 'pool' | 'due' | 'dnc';
export type LeadSort = 'priority' | 'newest' | 'name';

/**
 * The filters and order of the leads list. It travels in the URL of a lead
 * opened from the list, so "next lead" walks that same list and "back"
 * restores it.
 */
export type LeadListContext = {
  q: string;
  status: string;
  priority: string;
  city: string;
  source: string;
  view: LeadView;
  hideClosed: boolean;
  sort: LeadSort;
};

export const defaultLeadListContext = (): LeadListContext => ({
  q: '',
  status: '',
  priority: '',
  city: '',
  source: '',
  view: 'all',
  hideClosed: true,
  sort: 'priority'
});

const leadViews: LeadView[] = ['all', 'fresh', 'mine', 'pool', 'due', 'dnc'];
const leadSorts: LeadSort[] = ['priority', 'newest', 'name'];

export function leadContextFromParams(params: URLSearchParams): LeadListContext {
  const context = defaultLeadListContext();
  context.q = params.get('q') ?? '';
  context.status = params.get('durum') ?? '';
  context.priority = params.get('oncelik') ?? '';
  context.city = params.get('il') ?? '';
  context.source = params.get('kaynak') ?? '';
  const view = params.get('gorunum') as LeadView | null;
  if (view && leadViews.includes(view)) context.view = view;
  const sort = params.get('sira') as LeadSort | null;
  if (sort && leadSorts.includes(sort)) context.sort = sort;
  if (params.get('kapali') === 'goster') context.hideClosed = false;
  return context;
}

/** Only what differs from the defaults, so plain links stay short. */
export function leadContextToParams(context: LeadListContext): URLSearchParams {
  const params = new URLSearchParams();
  if (context.q.trim()) params.set('q', context.q.trim());
  if (context.status) params.set('durum', context.status);
  if (context.priority) params.set('oncelik', context.priority);
  if (context.city.trim()) params.set('il', context.city.trim());
  if (context.source.trim()) params.set('kaynak', context.source.trim());
  if (context.view !== 'all') params.set('gorunum', context.view);
  if (context.sort !== 'priority') params.set('sira', context.sort);
  if (!context.hideClosed) params.set('kapali', 'goster');
  return params;
}

export function withLeadContext(path: string, context: LeadListContext): string {
  const query = leadContextToParams(context).toString();
  return query ? `${path}?${query}` : path;
}

/** The list endpoint's query for a context. */
export function leadContextQuery(context: LeadListContext) {
  return {
    q: context.q.trim() || undefined,
    status: context.status || undefined,
    priority: context.priority || undefined,
    city: context.city.trim() || undefined,
    source: context.source.trim() || undefined,
    owner: context.view === 'mine' ? 'me' : context.view === 'pool' ? 'none' : undefined,
    due: context.view === 'due',
    neverCalled: context.view === 'fresh',
    doNotCall: (context.view === 'dnc' ? 'only' : '') as '' | 'only' | 'exclude',
    hideClosed: context.hideClosed,
    sort: context.sort
  };
}
