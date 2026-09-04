import { parseMoneyInput } from '$lib/design/decimal';

export const TRY_AMOUNT_PATTERN = /^(0|[1-9][0-9]*)(?:\.([0-9]{1,2}))?$/;

export function normalizeTRYAmount(value: string): string | null {
  const match = TRY_AMOUNT_PATTERN.exec(parseMoneyInput(value));
  if (!match) return null;
  const normalized = `${match[1]}.${(match[2] ?? '').padEnd(2, '0')}`;
  return normalized === '0.00' ? null : normalized;
}

export function validTRYAmount(value: string): boolean {
  return normalizeTRYAmount(value) !== null;
}

export function localTodayISO(now = new Date()): string {
  const pad = (part: number) => String(part).padStart(2, '0');
  return `${now.getFullYear()}-${pad(now.getMonth() + 1)}-${pad(now.getDate())}`;
}

export function advanceActionVisibility(permissions: readonly string[], status: string) {
  const open = status === 'OPEN';
  return {
    collect: open && permissions.includes('hr.employee_advance.collect'),
    writeOff: open && permissions.includes('hr.employee_advance.writeoff'),
    reverse: permissions.includes('hr.employee_advance.reverse')
  };
}

export const advanceStatusLabel = (status: string) =>
  ({ OPEN: 'Açık', CLOSED: 'Kapalı', WRITTEN_OFF: 'Vazgeçildi', REVERSED: 'Ters kayıt' })[status] ??
  status;

export const advanceTransactionLabel = (type: string) =>
  ({
    DISBURSEMENT: 'Avans verildi',
    REPAYMENT: 'Geri ödeme',
    WRITE_OFF: 'Alacaktan vazgeçildi',
    REVERSAL: 'Ters kayıt'
  })[type] ?? type;
