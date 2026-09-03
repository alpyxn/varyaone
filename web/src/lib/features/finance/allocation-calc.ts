import {
  addDecimalStrings,
  canonicalDecimal,
  compareDecimalStrings,
  multiplyDecimalStrings,
  subtractDecimalStrings
} from '$lib/design/decimal';

export type AllocationOpenItem = {
  id: string;
  document_id?: string;
  document_no?: string;
  document_date?: string;
  due_date?: string;
  open_amount: string;
};

export type AllocationRow = AllocationOpenItem & { applied: string };

/** Total of the "Uygulanacak" column as an exact decimal string. */
export function allocatedTotal(rows: Array<{ applied: string }>): string {
  return rows.reduce(
    (sum, row) => addDecimalStrings(sum, canonicalDecimal(row.applied) || '0'),
    '0'
  );
}

/**
 * Payment amount minus what has been distributed. Clamps at 0 (matching the
 * dialog's existing behaviour); use {@link isOverApplied} to detect the surplus.
 */
export function unappliedAmount(amount: string, rows: Array<{ applied: string }>): string {
  return subtractDecimalStrings(canonicalDecimal(amount) || '0', allocatedTotal(rows));
}

/** True when the distributed total exceeds the payment amount. */
export function isOverApplied(amount: string, rows: Array<{ applied: string }>): boolean {
  return compareDecimalStrings(allocatedTotal(rows), canonicalDecimal(amount) || '0') > 0;
}

/** Render an exact decimal with the four places the finance API speaks. */
function money(value: string): string {
  const rounded = multiplyDecimalStrings(value, '1', 4) ?? '0';
  const [integer = '0', fraction = ''] = rounded.split('.', 2);
  return `${integer}.${fraction.padEnd(4, '0')}`;
}

/** Days a due date is overdue relative to `now` (0 or negative when not late). */
export function daysOverdue(dueDate: string | undefined, now = new Date()): number {
  if (!dueDate) return 0;
  const due = new Date(dueDate).getTime();
  if (Number.isNaN(due)) return 0;
  return Math.floor((now.getTime() - due) / 86_400_000);
}

/**
 * Client-side FIFO preview mirroring the server's `FIFOAllocations`: oldest due
 * date first (undated last), then oldest document date, then id; each item takes
 * min(open, remaining); any surplus is left unallocated (an advance).
 */
export function previewFifo(items: AllocationOpenItem[], amount: string): Record<string, string> {
  // Exact decimals throughout: these amounts are submitted as the allocation
  // payload, and a float remainder drifting by a ten-thousandth is either a
  // kurus left open on the last invoice or a server-side over-allocation.
  let remaining = canonicalDecimal(amount) || '0';
  const ordered = [...items].sort((a, b) => {
    if (a.due_date || b.due_date) {
      if (!a.due_date) return 1;
      if (!b.due_date) return -1;
      if (a.due_date !== b.due_date) return a.due_date < b.due_date ? -1 : 1;
    }
    const ad = a.document_date ?? '';
    const bd = b.document_date ?? '';
    if (ad !== bd) return ad < bd ? -1 : 1;
    return a.id < b.id ? -1 : 1;
  });
  const result: Record<string, string> = {};
  for (const item of ordered) {
    if (compareDecimalStrings(remaining, '0') <= 0) break;
    const open = canonicalDecimal(item.open_amount) || '0';
    if (compareDecimalStrings(open, '0') <= 0) continue;
    const part = compareDecimalStrings(open, remaining) > 0 ? remaining : open;
    result[item.id] = money(part);
    remaining = subtractDecimalStrings(remaining, part);
  }
  return result;
}
