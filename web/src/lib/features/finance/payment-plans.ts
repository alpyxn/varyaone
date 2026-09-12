import { trimDecimalZeros } from '$lib/design/decimal';

export type InstallmentDraft = { due_date: string; amount: string };
export type PlanAllocation = { open_item_id: string; amount: string };
export type PlanInstallment = InstallmentDraft & {
  number: number;
  open_amount: string;
  closed_amount: string;
  status: 'PENDING' | 'PARTIAL' | 'OVERDUE' | 'CLOSED' | 'CANCELLED';
  allocations: PlanAllocation[];
};
export type PaymentPlan = {
  id: string;
  party_id: string;
  party_name: string;
  side: 'RECEIVABLE' | 'PAYABLE';
  currency: string;
  description: string;
  total_amount: string;
  created_at: string;
  cancelled_at?: string;
  cancel_reason?: string;
  version: number;
  installments: PlanInstallment[];
  sources: { open_item_id: string; document_id: string; document_no: string; amount: string }[];
};

/** Fixed point throughout: schedules must sum exactly to the open invoice balance. */
export function planUnits(value: string): bigint {
  const text = value.trim().replace(',', '.');
  if (!/^\d+(\.\d{1,4})?$/.test(text))
    throw new Error('Tutar en fazla dört ondalıklı pozitif bir sayı olmalıdır.');
  const [whole, fraction = ''] = text.split('.');
  return BigInt(whole) * 10000n + BigInt(fraction.padEnd(4, '0'));
}
export function planAmount(value: bigint): string {
  return `${value / 10000n}.${(value % 10000n).toString().padStart(4, '0')}`;
}
export function equalInstallments(
  total: string,
  count: number,
  firstDate: string,
  intervalMonths = 1
): InstallmentDraft[] {
  const units = planUnits(total);
  if (!Number.isInteger(count) || count < 1 || count > 120 || units < BigInt(count))
    throw new Error('Taksit sayısı 1–120 arasında ve tutara uygun olmalıdır.');
  const start = new Date(`${firstDate}T12:00:00Z`);
  if (Number.isNaN(start.valueOf()) || start.toISOString().slice(0, 10) !== firstDate)
    throw new Error('İlk vade tarihini seçin.');
  // Preserve the original day, clamping Jan 31 -> Feb 28 -> Mar 31.
  const step = units % 100n === 0n && units / 100n >= BigInt(count) ? 100n : 1n;
  const each = (units / step / BigInt(count)) * step;
  return Array.from({ length: count }, (_, i) => {
    const last = new Date(
      Date.UTC(start.getUTCFullYear(), start.getUTCMonth() + i * intervalMonths + 1, 0)
    );
    const date = new Date(
      Date.UTC(
        last.getUTCFullYear(),
        last.getUTCMonth(),
        Math.min(start.getUTCDate(), last.getUTCDate())
      )
    );
    return {
      due_date: date.toISOString().slice(0, 10),
      amount: trimDecimalZeros(
        planAmount(i === count - 1 ? units - each * BigInt(count - 1) : each)
      )
    };
  });
}
export function partialPlanAllocations(rows: PlanAllocation[], amount: string): PlanAllocation[] {
  let left = planUnits(amount);
  if (left <= 0n) throw new Error('Ödeme tutarı sıfırdan büyük olmalıdır.');
  const result: PlanAllocation[] = [];
  for (const row of rows) {
    const available = planUnits(row.amount);
    const part = available < left ? available : left;
    if (part > 0n) result.push({ open_item_id: row.open_item_id, amount: planAmount(part) });
    left -= part;
  }
  if (left > 0n) throw new Error('Tutar taksitin kalan tutarını aşamaz.');
  return result;
}

export type PaymentPlanSummary = Pick<
  PaymentPlan,
  'id' | 'party_id' | 'party_name' | 'description' | 'currency' | 'total_amount' | 'created_at'
> & {
  open_amount: string;
  overdue_amount: string;
  next_due_date?: string;
  status: 'OPEN' | 'CLOSED' | 'CANCELLED';
};
