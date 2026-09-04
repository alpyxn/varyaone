import { api } from '$lib/api';
import type {
  EmailPreview,
  Employee,
  EmployeeDocument,
  EmployeeInput,
  EmployeeReadiness,
  EmployeePayroll,
  Employment,
  EmploymentTerm,
  HolidayCalendar,
  LeaveType,
  LegislationPack,
  Payslip,
  PayrollRun,
  PrivateProfile,
  ScheduleTemplate,
  TimesheetPeriod,
  WagePreview
} from './types';
import type { EmployeeAdvance, EmployeeAdvancePage } from './types';

const ifMatch = (version: number) => ({ 'If-Match': `"${version}"` });
type List<T> = { items: T[]; next_cursor?: string };

// ---- Employees ----
export const listEmployees = (params: { q?: string; status?: string; cursor?: string } = {}) => {
  const search = new URLSearchParams();
  if (params.q) search.set('q', params.q);
  if (params.status) search.set('status', params.status);
  if (params.cursor) search.set('cursor', params.cursor);
  const qs = search.toString();
  return api<List<Employee>>(`/hr/employees${qs ? `?${qs}` : ''}`);
};
export const getEmployee = (id: string) => api<Employee>(`/hr/employees/${id}`);
export const searchOccupationCodes = (q: string) =>
  api<List<import('./types').OccupationCode>>(
    `/hr/occupation-codes?limit=50${q ? `&q=${encodeURIComponent(q)}` : ''}`
  );
export const listEmployeeReadiness = (year: number, month: number) =>
  api<List<EmployeeReadiness>>(`/hr/employees/readiness?year=${year}&month=${month}`);
export const listTimesheetReadiness = (periodID: string) =>
  api<List<EmployeeReadiness>>(`/hr/timesheet-periods/${periodID}/readiness`);
export const createEmployee = (input: EmployeeInput) =>
  api<Employee>('/hr/employees', { method: 'POST', body: JSON.stringify(input) });
export const updateEmployee = (id: string, version: number, input: EmployeeInput) =>
  api<Employee>(`/hr/employees/${id}`, {
    method: 'PATCH',
    headers: ifMatch(version),
    body: JSON.stringify(input)
  });

// ---- Employee cash advances (separate from payroll and party accounts) ----
export const listEmployeeAdvances = (params: Record<string, string | undefined> = {}) => {
  const search = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) if (value) search.set(key, value);
  const query = search.toString();
  return api<EmployeeAdvancePage>(`/hr/employee-advances${query ? `?${query}` : ''}`);
};
export const listAdvancesForEmployee = (employeeID: string) =>
  api<EmployeeAdvancePage>(`/hr/employees/${employeeID}/advances`);
export const getEmployeeAdvance = (id: string) =>
  api<EmployeeAdvance>(`/hr/employee-advances/${id}`);
export const createEmployeeAdvance = (body: Record<string, unknown>) =>
  api<EmployeeAdvance>('/hr/employee-advances', { method: 'POST', body: JSON.stringify(body) });
export const collectEmployeeAdvance = (id: string, body: Record<string, unknown>) =>
  api<EmployeeAdvance>(`/hr/employee-advances/${id}/repayments`, {
    method: 'POST',
    body: JSON.stringify(body)
  });
export const writeOffEmployeeAdvance = (id: string, body: Record<string, unknown>) =>
  api<EmployeeAdvance>(`/hr/employee-advances/${id}/write-off`, {
    method: 'POST',
    body: JSON.stringify(body)
  });
export const reverseEmployeeAdvanceTransaction = (id: string, body: Record<string, unknown>) =>
  api<EmployeeAdvance>(`/hr/employee-advance-transactions/${id}/reverse`, {
    method: 'POST',
    body: JSON.stringify(body)
  });

// ---- Private profile (kimlik & banka) ----
export const getPrivateProfile = (employeeID: string) =>
  api<PrivateProfile>(`/hr/employees/${employeeID}/private-profile`);
export const updatePrivateProfile = (
  employeeID: string,
  version: number,
  body: Record<string, unknown>
) =>
  api<PrivateProfile>(`/hr/employees/${employeeID}/private-profile`, {
    method: 'PATCH',
    headers: version ? ifMatch(version) : undefined,
    body: JSON.stringify(body)
  });

// ---- Address ----
export type EmployeeAddress = {
  address_line: string;
  postal_code?: string;
  province_id?: number;
  province_name?: string;
  district_id?: number;
  district_name?: string;
  neighborhood_id?: number;
  neighborhood_name?: string;
  neighborhood?: string;
  version: number;
};
export const getEmployeeAddress = (employeeID: string) =>
  api<EmployeeAddress>(`/hr/employees/${employeeID}/address`);
export const saveEmployeeAddress = (
  employeeID: string,
  version: number,
  body: Record<string, unknown>
) =>
  api<EmployeeAddress>(`/hr/employees/${employeeID}/address`, {
    method: 'PUT',
    headers: version ? ifMatch(version) : undefined,
    body: JSON.stringify(body)
  });

// ---- Employment ----
export const listEmployments = (employeeID: string) =>
  api<List<Employment>>(`/hr/employees/${employeeID}/employments`);
export const createEmployment = (employeeID: string, start_date: string) =>
  api<Employment>(`/hr/employees/${employeeID}/employments`, {
    method: 'POST',
    body: JSON.stringify({ start_date })
  });
export const terminateEmployment = (
  employeeID: string,
  employmentID: string,
  version: number,
  body: { end_date: string; termination_reason: string }
) =>
  api<Employment>(`/hr/employees/${employeeID}/employments/${employmentID}/terminate`, {
    method: 'POST',
    headers: ifMatch(version),
    body: JSON.stringify(body)
  });
export const listTerms = (employeeID: string) =>
  api<List<EmploymentTerm>>(`/hr/employees/${employeeID}/employment-terms`);
export const createTerm = (
  employeeID: string,
  employmentID: string,
  body: Record<string, unknown>
) =>
  api<EmploymentTerm>(`/hr/employees/${employeeID}/employments/${employmentID}/terms`, {
    method: 'POST',
    body: JSON.stringify(body)
  });

// ---- Documents ----
export const listDocuments = (employeeID: string, archivedOnly = false) =>
  api<List<EmployeeDocument>>(
    `/hr/employees/${employeeID}/documents${archivedOnly ? '?archived=true' : ''}`
  );
export const uploadDocument = (employeeID: string, form: FormData) =>
  api<EmployeeDocument>(`/hr/employees/${employeeID}/documents`, { method: 'POST', body: form });
export const archiveDocument = (employeeID: string, documentID: string) =>
  api<void>(`/hr/employees/${employeeID}/documents/${documentID}`, { method: 'DELETE' });

// ---- Schedules ----
export const listScheduleTemplates = () => api<List<ScheduleTemplate>>('/hr/schedule-templates');
export const getScheduleTemplate = (id: string) =>
  api<ScheduleTemplate>(`/hr/schedule-templates/${id}`);
export const createScheduleTemplate = (code: string, name: string) =>
  api<ScheduleTemplate>('/hr/schedule-templates', {
    method: 'POST',
    body: JSON.stringify({ code, name })
  });
export const addScheduleVersion = (templateID: string, body: Record<string, unknown>) =>
  api<ScheduleTemplate>(`/hr/schedule-templates/${templateID}/versions`, {
    method: 'POST',
    body: JSON.stringify(body)
  });
export const deleteScheduleVersion = (templateID: string, versionID: string) =>
  api<ScheduleTemplate>(`/hr/schedule-templates/${templateID}/versions/${versionID}`, {
    method: 'DELETE'
  });
export const listScheduleAssignments = (employeeID: string) =>
  api<
    List<{
      id: string;
      template_code: string;
      template_name: string;
      effective_from: string;
      effective_to?: string | null;
    }>
  >(`/hr/employees/${employeeID}/schedule-assignments`);
export const assignSchedule = (employeeID: string, body: Record<string, unknown>) =>
  api(`/hr/employees/${employeeID}/schedule-assignments`, {
    method: 'POST',
    body: JSON.stringify(body)
  });
export const deleteScheduleAssignment = (employeeID: string, assignmentID: string) =>
  api<void>(`/hr/employees/${employeeID}/schedule-assignments/${assignmentID}`, {
    method: 'DELETE'
  });

// ---- Leave ----
export const listLeaveTypes = () => api<List<LeaveType>>('/hr/leave-types');
export const createLeaveType = (body: Record<string, unknown>) =>
  api<LeaveType>('/hr/leave-types', { method: 'POST', body: JSON.stringify(body) });
export const updateLeaveType = (id: string, version: number, body: Record<string, unknown>) =>
  api<LeaveType>(`/hr/leave-types/${id}`, {
    method: 'PATCH',
    headers: ifMatch(version),
    body: JSON.stringify(body)
  });
// ---- Timesheet ----
export const listTimesheetPeriods = () => api<List<TimesheetPeriod>>('/hr/timesheet-periods');
export const getTimesheetPeriod = (id: string) =>
  api<TimesheetPeriod>(`/hr/timesheet-periods/${id}`);
export const createTimesheetPeriod = (period_year: number, period_month: number) =>
  api<TimesheetPeriod>('/hr/timesheet-periods', {
    method: 'POST',
    body: JSON.stringify({ period_year, period_month })
  });
export const generateTimesheet = (id: string) =>
  api<TimesheetPeriod>(`/hr/timesheet-periods/${id}/generate`, { method: 'POST' });
export const updateTimesheetDay = (
  periodID: string,
  dayID: string,
  version: number,
  body: Record<string, unknown>
) =>
  api<TimesheetPeriod>(`/hr/timesheet-periods/${periodID}/days/${dayID}`, {
    method: 'PATCH',
    headers: ifMatch(version),
    body: JSON.stringify(body)
  });
export const upsertTimesheetDay = (
  periodID: string,
  body: {
    employee_id: string;
    work_date: string;
    kind: string;
    minutes?: number;
    overtime_minutes?: number;
    explanation?: string;
    leave_type_id?: string;
  }
) =>
  api<TimesheetPeriod>(`/hr/timesheet-periods/${periodID}/days`, {
    method: 'PUT',
    body: JSON.stringify(body)
  });
export const deleteTimesheetDay = (periodID: string, dayID: string) =>
  api<TimesheetPeriod>(`/hr/timesheet-periods/${periodID}/days/${dayID}`, { method: 'DELETE' });
export const finalizeTimesheet = (id: string, version: number) =>
  api<TimesheetPeriod>(`/hr/timesheet-periods/${id}/finalize`, {
    method: 'POST',
    headers: ifMatch(version)
  });
export const reopenTimesheet = (id: string, version: number, reason: string) =>
  api<TimesheetPeriod>(`/hr/timesheet-periods/${id}/reopen`, {
    method: 'POST',
    headers: ifMatch(version),
    body: JSON.stringify({ reason })
  });

// ---- Legislation ----
export const listLegislationPacks = () => api<List<LegislationPack>>('/hr/legislation-packs');
export const getLegislationPack = (id: string) =>
  api<LegislationPack>(`/hr/legislation-packs/${id}`);

export type PayrollSettings = { default_contribution_scheme_code: string };
export const getPayrollSettings = () => api<PayrollSettings>('/hr/payroll-settings');
export const savePayrollSettings = (default_contribution_scheme_code: string) =>
  api<PayrollSettings>('/hr/payroll-settings', {
    method: 'PUT',
    body: JSON.stringify({ default_contribution_scheme_code })
  });
export const createLegislationDraft = (body: Record<string, unknown>) =>
  api<LegislationPack>('/hr/legislation-packs', { method: 'POST', body: JSON.stringify(body) });
export type ActivatePackResult = { pack: LegislationPack; warning?: string };
export const activateLegislationPack = async (id: string): Promise<ActivatePackResult> => {
  const res = await api<LegislationPack | ActivatePackResult>(
    `/hr/legislation-packs/${id}/activate`,
    {
      method: 'POST'
    }
  );
  if (res && typeof res === 'object' && 'pack' in res) return res as ActivatePackResult;
  return { pack: res as LegislationPack };
};
// Yeni asgari ücret tanımı: mevcut aktif dönemden yeni bir dönem oluşturup tek
// adımda aktifleştirir; eskisi otomatik geçmişe düşer.
export const replaceMinimumWage = async (body: {
  minimum_monthly_gross: string;
  change_reason?: string;
}): Promise<ActivatePackResult> => {
  const res = await api<LegislationPack | ActivatePackResult>('/hr/minimum-wage', {
    method: 'POST',
    body: JSON.stringify(body)
  });
  if (res && typeof res === 'object' && 'pack' in res) return res as ActivatePackResult;
  return { pack: res as LegislationPack };
};

// ---- Wage preview (brüt↔net hesaplayıcı) ----
export const wagePreview = (params: {
  mode: 'gross' | 'net';
  amount: string;
  scheme?: string;
  date?: string;
}) => {
  const search = new URLSearchParams({ mode: params.mode, amount: params.amount });
  if (params.scheme) search.set('scheme', params.scheme);
  if (params.date) search.set('date', params.date);
  return api<WagePreview>(`/hr/payroll/wage-preview?${search.toString()}`);
};
export const minimumWage = (params: { scheme?: string; date?: string } = {}) => {
  const search = new URLSearchParams();
  if (params.scheme) search.set('scheme', params.scheme);
  if (params.date) search.set('date', params.date);
  const qs = search.toString();
  return api<WagePreview>(`/hr/payroll/minimum-wage${qs ? `?${qs}` : ''}`);
};

// ---- Payroll runs ----
export const listPayrollRuns = () => api<List<PayrollRun>>('/hr/payroll-runs');
export const getPayrollRun = (id: string) => api<PayrollRun>(`/hr/payroll-runs/${id}`);
export const createPayrollRun = (body: Record<string, unknown>) =>
  api<PayrollRun>('/hr/payroll-runs', { method: 'POST', body: JSON.stringify(body) });
export const calculatePayrollRun = (id: string) =>
  api<PayrollRun>(`/hr/payroll-runs/${id}/calculate`, { method: 'POST' });
export const finalizePayrollRun = (id: string, version: number) =>
  api<PayrollRun>(`/hr/payroll-runs/${id}/finalize`, {
    method: 'POST',
    headers: ifMatch(version)
  });
export const listManualComponents = (runID: string) =>
  api<
    List<{
      id: string;
      employee_name: string;
      component_code: string;
      amount: string;
      explanation: string;
    }>
  >(`/hr/payroll-runs/${runID}/manual-components`);
export const addManualComponent = (runID: string, body: Record<string, unknown>) =>
  api(`/hr/payroll-runs/${runID}/manual-components`, {
    method: 'POST',
    body: JSON.stringify(body)
  });
export const archiveManualComponent = (runID: string, componentID: string) =>
  api<void>(`/hr/payroll-runs/${runID}/manual-components/${componentID}`, { method: 'DELETE' });

// ---- Payroll payments (kasa/banka çıkışı) ----
export const listPayrollPayments = (runID: string) =>
  api<{ items: import('./types').PayrollPayment[] }>(`/hr/payroll-runs/${runID}/payments`);
export const createPayrollPayment = (
  runID: string,
  body: {
    account_id: string;
    payment_date?: string;
    description?: string;
    override_reason?: string;
  }
) =>
  api<import('./types').PayrollPayment>(`/hr/payroll-runs/${runID}/payments`, {
    method: 'POST',
    body: JSON.stringify(body)
  });
export const reversePayrollPayment = (paymentID: string, reason: string) =>
  api<import('./types').PayrollPayment>(`/hr/payroll-payments/${paymentID}/reverse`, {
    method: 'POST',
    body: JSON.stringify({ reason })
  });
export const listPaymentAccounts = () =>
  api<{ items: import('./types').PaymentAccount[] }>('/finance/accounts?limit=100');

// ---- Payslips / export / email ----
export const listPayslips = (runID: string) =>
  api<List<Payslip>>(`/hr/payroll-runs/${runID}/payslips`);
export const generatePayslips = (runID: string) =>
  api<List<Payslip>>(`/hr/payroll-runs/${runID}/payslips`, { method: 'POST' });
export const payslipDownloadURL = (payslipID: string) =>
  `/api/v1/hr/payslips/${payslipID}/download`;
export const createExport = (runID: string, export_type: string) =>
  api<{ id: string; export_type: string; status: string }>(`/hr/payroll-runs/${runID}/exports`, {
    method: 'POST',
    body: JSON.stringify({ export_type })
  });
export const exportDownloadURL = (exportID: string) =>
  `/api/v1/hr/payroll-exports/${exportID}/download`;
export const emailPreview = (runID: string) =>
  api<EmailPreview>(`/hr/payroll-runs/${runID}/email-preview`);
export const sendEmailBatch = (
  runID: string,
  opts: { resend?: boolean; subject?: string; body?: string } = {}
) =>
  api<{ batch_id: string; status: string; sent: number; failed: number; skipped: number }>(
    `/hr/payroll-runs/${runID}/email-batches`,
    {
      method: 'POST',
      body: JSON.stringify({
        resend: opts.resend ?? false,
        subject: opts.subject ?? '',
        body: opts.body ?? ''
      })
    }
  );

// ---- Holiday calendars ----
export const listHolidayCalendars = (year?: number) =>
  api<List<HolidayCalendar>>(`/hr/holiday-calendars${year ? `?year=${year}` : ''}`);
export const getHolidayCalendar = (id: string) =>
  api<HolidayCalendar>(`/hr/holiday-calendars/${id}`);
