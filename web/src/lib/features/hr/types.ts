import { formatAmount } from '$lib/design/formatters';

export type Employee = {
  id: string;
  employee_code: string;
  first_name: string;
  last_name: string;
  status: 'ACTIVE' | 'INACTIVE' | 'ARCHIVED';
  position_title: string;
  work_email?: string | null;
  personal_email?: string | null;
  phone?: string | null;
  occupation_code?: string | null;
  occupation_name?: string | null;
  hire_date?: string | null;
  termination_date?: string | null;
  version: number;
};

export type EmployeeInput = {
  employee_code: string;
  first_name: string;
  last_name: string;
  status: string;
  position_title: string;
  work_email: string;
  personal_email: string;
  phone: string;
  occupation_code?: string;
};

export type OccupationCode = { code: string; name: string };

export type EmployeeAdvanceTransaction = {
  id: string;
  advance_id: string;
  type: 'DISBURSEMENT' | 'REPAYMENT' | 'WRITE_OFF' | 'REVERSAL';
  amount: string;
  transaction_date: string;
  account_id?: string | null;
  finance_movement_id?: string | null;
  reversal_of_id?: string | null;
  reason?: string;
  description?: string;
  actor_user_id: string;
  created_at: string;
};

export type EmployeeAdvance = {
  id: string;
  employee_id: string;
  employee_code: string;
  employee_name: string;
  account_id: string;
  account_name: string;
  currency: 'TRY';
  original_amount: string;
  repaid_amount: string;
  written_off_amount: string;
  outstanding_amount: string;
  status: 'OPEN' | 'CLOSED' | 'WRITTEN_OFF' | 'REVERSED';
  advance_date: string;
  expected_repayment_date?: string | null;
  description: string;
  reference: string;
  requires_accounting_tax_review: boolean;
  transactions?: EmployeeAdvanceTransaction[];
};

export type EmployeeAdvancePage = { items: EmployeeAdvance[]; total_outstanding: string };

export type PrivateProfile = {
  birth_date?: string | null;
  emergency_contact_name?: string | null;
  emergency_contact_phone?: string | null;
  payroll_email?: string | null;
  bank_name?: string | null;
  tckn?: string;
  iban?: string;
  masked_tckn?: string;
  masked_iban?: string;
  has_tckn: boolean;
  has_iban: boolean;
  version: number;
};

export type Employment = {
  id: string;
  employee_id: string;
  start_date: string;
  end_date?: string | null;
  termination_reason?: string | null;
  version: number;
};

export type EmploymentTerm = {
  id: string;
  employment_id: string;
  effective_from: string;
  effective_to?: string | null;
  wage_type: string;
  wage_period: string;
  gross_wage: string;
  currency: string;
  work_type: string;
  weekly_minutes: number;
  contribution_scheme_code: string;
  prior_employer_tax_policy: string;
  sgk_status: string;
  is_minimum_wage: boolean;
  version: number;
};

export type WagePreview = {
  gross: string;
  net: string;
  employee_sgk: string;
  employee_unemployment: string;
  income_tax: string;
  stamp_tax: string;
  pack_id: string;
  pack_code: string;
  effective_from: string;
  effective_to: string;
};

export const SGK_STATUS_OPTIONS: { value: string; label: string; supported: boolean }[] = [
  { value: '4A', label: '4/a — SSK (özel sektör)', supported: true },
  { value: '4A_SGDP', label: '4/a — Emekli (Sosyal Güvenlik Destek Primi)', supported: true },
  { value: '4A_NO_UNEMPLOYMENT', label: '4/a — İşsizlik sigortası kapsam dışı', supported: true },
  { value: 'APPRENTICE', label: 'Çırak / Stajyer', supported: false },
  { value: '4B', label: '4/b — Bağ-Kur', supported: false },
  { value: '4C', label: '4/c — Emekli Sandığı', supported: false }
];

export function sgkStatusLabel(value: string): string {
  return (
    SGK_STATUS_OPTIONS.find((o) => o.value === value)?.label ?? (value || '4/a — SSK (özel sektör)')
  );
}

export type EmployeeDocument = {
  id: string;
  document_type: string;
  sensitivity: 'GENERAL' | 'IDENTITY' | 'HEALTH';
  mime_type: string;
  size_bytes: number;
  original_filename?: string;
  archived_at?: string | null;
  created_at: string;
};

export type ScheduleDay = {
  weekday: number;
  is_workday: boolean;
  starts_at?: string | null;
  ends_at?: string | null;
  ends_next_day: boolean;
  break_minutes: number;
  planned_minutes: number;
};

export type ScheduleVersion = {
  id: string;
  effective_from: string;
  effective_to?: string | null;
  days: ScheduleDay[];
};

export type ScheduleTemplate = {
  id: string;
  code: string;
  name: string;
  is_active: boolean;
  versions?: ScheduleVersion[];
  version: number;
};

export type LeaveType = {
  id: string;
  code: string;
  name: string;
  payroll_treatment: 'PAID' | 'UNPAID' | 'SICK_REQUIRES_REVIEW';
  is_active: boolean;
  version: number;
};

export type TimesheetDay = {
  id: string;
  employee_id: string;
  employee_name: string;
  work_date: string;
  source: 'GENERATED' | 'MANUAL';
  planned_minutes: number;
  worked_minutes: number;
  paid_leave_minutes: number;
  unpaid_leave_minutes: number;
  overtime_minutes: number;
  week_rest_minutes: number;
  public_holiday_minutes: number;
  absence_minutes: number;
  explanation: string;
  leave_type_id?: string | null;
  leave_code?: string | null;
  leave_name?: string | null;
  version: number;
};

export type TimesheetPeriod = {
  id: string;
  period_year: number;
  period_month: number;
  status: 'DRAFT' | 'FINALIZED';
  generation: number;
  checksum?: string | null;
  finalized_at?: string | null;
  days?: TimesheetDay[];
  version: number;
};

export type LegislationPack = {
  id: string;
  code: string;
  version: number;
  status: 'DRAFT' | 'ACTIVE' | 'SUPERSEDED';
  effective_from: string;
  effective_to: string;
  minimum_monthly_gross?: string;
  sgk_daily_floor?: string;
  sgk_daily_ceiling?: string;
  stamp_tax_rate?: string;
  income_tax_brackets?: { sequence: number; upper_bound?: string | null; rate: string }[];
  contribution_schemes?: { code: string; name: string }[];
  components?: { code: string; name: string; ownership: string; component_kind: string }[];
};

export type PayrollComponent = {
  component_code: string;
  component_name: string;
  component_kind: string;
  amount: string;
  calculation_order: number;
};

export type EmployeePayroll = {
  id: string;
  employee_id: string;
  employee_name: string;
  status: 'CALCULATED' | 'FAILED' | 'FINALIZED';
  gross?: string;
  net?: string;
  employer_cost?: string;
  error_details?: unknown;
  components?: PayrollComponent[];
};

export type PayrollGeneration = {
  id: string;
  generation_no: number;
  status: string;
  error_summary?: unknown;
  started_at: string;
};

export type PayrollRun = {
  id: string;
  run_number: string;
  run_type: string;
  period_year: number;
  period_month: number;
  payment_date: string;
  timesheet_period_id: string;
  legislation_pack_id: string;
  status: 'DRAFT' | 'CALCULATING' | 'CALCULATED' | 'CALCULATION_FAILED' | 'FINALIZED';
  active_generation_id?: string | null;
  total_gross?: string;
  total_net?: string;
  total_employer_cost?: string;
  finalized_at?: string | null;
  version: number;
  generations?: PayrollGeneration[];
  employee_payrolls?: EmployeePayroll[];
};

export type Payslip = {
  id: string;
  employee_payroll_id: string;
  employee_name: string;
  template_version: string;
  size_bytes: number;
  sha256: string;
  created_at: string;
};

export type PayrollPayment = {
  id: string;
  payroll_run_id: string;
  account_id: string;
  account_name: string;
  account_type: string;
  amount: string;
  currency: string;
  payment_date: string;
  status: 'PAID' | 'REVERSED';
  description: string;
  finance_movement_id: string;
  reversed_at?: string | null;
  reversal_reason?: string | null;
  created_at: string;
};

export type PaymentAccount = {
  id: string;
  code: string;
  name: string;
  account_type: string;
  currency: string;
};

export type EmailRecipientRow = {
  employee_id: string;
  employee_payroll_id: string;
  name: string;
  email: string;
  status: 'ready' | 'missing' | 'invalid' | 'duplicate' | 'missing_payslip' | 'already_sent' | '';
  payslip_filename: string;
  variables: Record<string, string>;
};

export type EmailPreview = {
  ready: string[];
  missing: string[];
  invalid: string[];
  duplicate: string[];
  missing_payslip: string[];
  already_sent: string[];
  recipients: EmailRecipientRow[];
  default_subject: string;
  default_body: string;
  variables: string[];
};

export type HolidayCalendar = {
  id: string;
  country_code: string;
  calendar_year: number;
  version: number;
  status: 'DRAFT' | 'ACTIVE' | 'SUPERSEDED';
  source_name: string;
  source_url: string;
  holidays?: { id: string; holiday_date: string; name: string; duration: string }[];
};

export const MONTH_NAMES = [
  'Ocak',
  'Şubat',
  'Mart',
  'Nisan',
  'Mayıs',
  'Haziran',
  'Temmuz',
  'Ağustos',
  'Eylül',
  'Ekim',
  'Kasım',
  'Aralık'
];

export function periodLabel(year: number, month: number): string {
  return `${MONTH_NAMES[month - 1] ?? month} ${year}`;
}

export function payrollStatusLabel(status: string): string {
  switch (status) {
    case 'ACTIVE':
      return 'Aktif';
    case 'INACTIVE':
      return 'Pasif';
    case 'ARCHIVED':
      return 'Arşivlendi';
    case 'SUPERSEDED':
      return 'Eski sürüm';
    case 'FAILED':
      return 'Hatalı';
    case 'DRAFT':
      return 'Taslak';
    case 'CALCULATING':
    case 'RUNNING':
      return 'Hesaplanıyor';
    case 'CALCULATED':
      return 'Hesaplandı';
    case 'CALCULATION_FAILED':
      return 'Hesaplama hatası';
    case 'FINALIZED':
      return 'Kesinleşti';
    case 'PENDING':
      return 'Beklemede';
    case 'APPROVED':
      return 'Onaylandı';
    case 'REJECTED':
      return 'Reddedildi';
    case 'CANCELLED':
      return 'İptal';
    default:
      return status;
  }
}

export type PayrollErrorDetail = {
  code: string;
  field?: string;
  component?: string;
  message?: string;
};

/** Normalizes the raw error_details / error_summary payload into typed rows. */
export function payrollErrorDetails(value: unknown): PayrollErrorDetail[] {
  if (!Array.isArray(value)) return [];
  const out: PayrollErrorDetail[] = [];
  for (const raw of value) {
    if (!raw || typeof raw !== 'object') continue;
    const e = raw as Record<string, unknown>;
    // generation error_summary rows wrap the detail under `error`
    const d = (e.error && typeof e.error === 'object' ? e.error : e) as Record<string, unknown>;
    out.push({
      code: String(d.code ?? ''),
      field: d.field ? String(d.field) : undefined,
      component: d.component ? String(d.component) : undefined,
      message: d.message ? String(d.message) : undefined
    });
  }
  return out;
}

const PAYROLL_ERROR_MAP: Record<string, { title: string; hint: string }> = {
  'PAYROLL_OPENING_BALANCE_REQUIRED:company_cumulative_tax_base': {
    title: 'Yıl içi kümülatif matrah eksik',
    hint:
      'Çalışanın işe giriş tarihi bu bordro döneminden önce, ancak sistemde önceki aylara ait ' +
      'kesinleşmiş bordro yok. Gelir vergisi kümülatif hesaplandığı için önceki ayların matrahı ' +
      'gerekli. En temiz çözüm: eksik ayların bordrosunu sırayla oluşturup kesinleştirin. Çalışan ' +
      'gerçekten bu ay başladıysa İstihdam sekmesinden çalışma dönemi başlangıç tarihini düzeltin.'
  },
  'PAYROLL_OPENING_BALANCE_REQUIRED:prior_cumulative_tax_base': {
    title: 'Önceki işveren matrah devri eksik',
    hint:
      'Çalışanın ücret kaydında “önceki işveren vergi politikası = Devir” seçili ama devredilen ' +
      'kümülatif gelir vergisi matrahı girilmemiş. Çalışan sizde yeni başladıysa Ücret sekmesinden ' +
      'bu ayarı “Ayrı hesap” yapın (kümülatif matrah sıfırdan başlar). Gerçekten devir varsa önceki ' +
      'işverenin yıl başından bugüne kümülatif gelir vergisi matrahını girmeniz gerekir.'
  },
  PAYROLL_OPENING_BALANCE_REQUIRED: {
    title: 'Kümülatif vergi matrahı açılışı eksik',
    hint: 'Bu çalışan için yıl içi kümülatif gelir vergisi matrahı bilgisi eksik.'
  },
  PAYROLL_LEGISLATION_NOT_FOUND: {
    title: 'Dönem için mevzuat paketi yok',
    hint: 'Bu bordro dönemini kapsayan aktif bir bordro mevzuatı paketi tanımlı değil.'
  },
  PAYROLL_RUN_TYPE_NOT_SUPPORTED: {
    title: 'Bordro türü desteklenmiyor',
    hint: 'Şu an yalnızca normal (aylık) bordro hesaplanabiliyor.'
  },
  PAYROLL_POPULATION_NOT_SUPPORTED: {
    title: 'Çalışan otomatik bordro kapsamı dışında',
    hint:
      'Çalışanın ücret/çalışma bilgileri otomatik hesaplama kapsamı dışında (ör. brüt/aylık/TRY ' +
      've tam zamanlı olmayan kayıt, ya da geçersiz gün/ücret). Ücret ve İstihdam sekmelerini kontrol edin.'
  },
  PAYROLL_COMPONENT_NOT_SUPPORTED: {
    title: 'Bordro bileşeni desteklenmiyor',
    hint: 'Bu bordroya eklenen manuel bir bileşen otomatik hesaplama kapsamı dışında.'
  },
  PAYROLL_SGK_STATUS_NOT_SUPPORTED: {
    title: 'Sigortalılık statüsü otomatik hesaplanamıyor',
    hint:
      'Çırak/stajyer, 4/b (Bağ-Kur) ve 4/c için otomatik bordro üretilmez. Ücret sekmesinden ' +
      'sigortalılık statüsünü kontrol edin; bu çalışan için bordro manuel hazırlanmalıdır.'
  },
  SICK_LEAVE_TREATMENT_REQUIRED: {
    title: 'Rapor/hastalık uygulaması belirsiz',
    hint: 'Döneme denk gelen bir hastalık izninin ücret uygulaması netleştirilmeli (İzinler sekmesi).'
  },
  PAYROLL_NEGATIVE_NET: {
    title: 'Net ücret sıfırın altına düşüyor',
    hint: 'Onaylı kesintiler net ücreti negatife çekiyor. Manuel kesinti bileşenlerini gözden geçirin.'
  },
  PAYROLL_RECONCILIATION_FAILED: {
    title: 'Bordro tutarları uzlaştırılamadı',
    hint: 'Hesaplanan bileşenler toplamı tutmuyor. Lütfen destek ekibine bildirin.'
  }
};

/** Returns a Turkish title + resolution hint for a payroll calculation error. */
export function payrollErrorInfo(detail: PayrollErrorDetail): { title: string; hint: string } {
  const byField = detail.field ? PAYROLL_ERROR_MAP[`${detail.code}:${detail.field}`] : undefined;
  if (byField) return byField;
  const byCode = PAYROLL_ERROR_MAP[detail.code];
  if (byCode) return byCode;
  return {
    title: detail.message || 'Bordro hesaplanamadı',
    hint: detail.message || 'Bu çalışan için bordro otomatik hesaplanamadı.'
  };
}

export function minutesToHours(minutes: number): string {
  return (minutes / 60).toFixed(1).replace('.0', '');
}

export function statusTone(status: string): 'neutral' | 'success' | 'warning' | 'danger' | 'info' {
  switch (status) {
    case 'ACTIVE':
    case 'CALCULATED':
    case 'APPROVED':
    case 'FINALIZED':
      return 'success';
    case 'CALCULATION_FAILED':
    case 'REJECTED':
    case 'CANCELLED':
    case 'ARCHIVED':
    case 'FAILED':
      return 'danger';
    case 'DRAFT':
    case 'INACTIVE':
      return 'neutral';
    case 'CALCULATING':
    case 'PENDING':
      return 'warning';
    default:
      return 'info';
  }
}

/** Formats a decimal string like "50000.00" as "50.000,00". The pages that
 *  call this print the ₺ themselves, so only the amount is returned. */
export function money(value?: string | null): string {
  if (value == null || value === '') return '—';
  const amount = formatAmount(value);
  return amount === '—' ? value : amount;
}

export const LEAVE_TREATMENT_LABELS: Record<string, string> = {
  PAID: 'Ücretli',
  UNPAID: 'Ücretsiz',
  SICK_REQUIRES_REVIEW: 'Raporlu (ücretli)'
};

export type TimesheetDayKind =
  | 'WORKED'
  | 'HALF_DAY'
  | 'PAID_LEAVE'
  | 'UNPAID_LEAVE'
  | 'PUBLIC_HOLIDAY'
  | 'ABSENT'
  | 'WEEK_REST'
  | 'NONE';

/** Collapses the per-bucket minute fields of a day into a single UI state. */
export function dayKindOf(day: Partial<TimesheetDay> | undefined): TimesheetDayKind {
  if (!day) return 'NONE';
  if ((day.public_holiday_minutes ?? 0) > 0) return 'PUBLIC_HOLIDAY';
  if ((day.paid_leave_minutes ?? 0) > 0) return 'PAID_LEAVE';
  if ((day.unpaid_leave_minutes ?? 0) > 0) return 'UNPAID_LEAVE';
  if ((day.absence_minutes ?? 0) > 0) return 'ABSENT';
  if ((day.worked_minutes ?? 0) > 0) {
    const planned = day.planned_minutes ?? 0;
    return planned > 0 && (day.worked_minutes ?? 0) * 2 <= planned ? 'HALF_DAY' : 'WORKED';
  }
  if ((day.week_rest_minutes ?? 0) > 0) return 'WEEK_REST';
  return 'NONE';
}

export const DAY_KIND_LABELS: Record<TimesheetDayKind, string> = {
  WORKED: 'Çalıştı',
  HALF_DAY: 'Yarım gün',
  PAID_LEAVE: 'Ücretli izin',
  UNPAID_LEAVE: 'Ücretsiz izin',
  PUBLIC_HOLIDAY: 'Resmî tatil',
  ABSENT: 'Devamsız',
  WEEK_REST: 'Hafta tatili',
  NONE: 'Kayıt yok'
};

export function dayKindLabel(kind: TimesheetDayKind): string {
  return DAY_KIND_LABELS[kind] ?? kind;
}

export function dayKindTone(
  kind: TimesheetDayKind
): 'neutral' | 'success' | 'warning' | 'danger' | 'info' {
  switch (kind) {
    case 'WORKED':
      return 'success';
    case 'HALF_DAY':
      return 'info';
    case 'PAID_LEAVE':
      return 'info';
    case 'UNPAID_LEAVE':
      return 'warning';
    case 'PUBLIC_HOLIDAY':
      return 'info';
    case 'ABSENT':
      return 'danger';
    case 'WEEK_REST':
      return 'neutral';
    default:
      return 'neutral';
  }
}

/** The days the UI lets a user assign, in menu order. */
export const ASSIGNABLE_DAY_KINDS: TimesheetDayKind[] = [
  'WORKED',
  'HALF_DAY',
  'PAID_LEAVE',
  'UNPAID_LEAVE',
  'PUBLIC_HOLIDAY',
  'ABSENT',
  'WEEK_REST'
];

/** Turkey national holidays (fixed + officially declared) for timesheet marking. */
export const TR_PUBLIC_HOLIDAYS: Record<number, string[]> = {
  2026: [
    '2026-01-01',
    '2026-03-20',
    '2026-03-21',
    '2026-03-22',
    '2026-04-23',
    '2026-05-01',
    '2026-05-19',
    '2026-05-27',
    '2026-05-28',
    '2026-05-29',
    '2026-05-30',
    '2026-07-15',
    '2026-08-30',
    '2026-10-29'
  ],
  2027: [
    '2027-01-01',
    '2027-03-09',
    '2027-03-10',
    '2027-03-11',
    '2027-04-23',
    '2027-05-01',
    '2027-05-16',
    '2027-05-17',
    '2027-05-18',
    '2027-05-19',
    '2027-07-15',
    '2027-08-30',
    '2027-10-29'
  ]
};
