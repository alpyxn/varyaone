import { api } from '$lib/api';
import { canonicalDecimal } from '$lib/design/decimal';
import type { AllocationOpenItem } from './allocation-calc';

export type FinancePaymentKind = 'collection' | 'payment';

const base = (kind: FinancePaymentKind) =>
  kind === 'collection' ? '/finance/collections' : '/finance/payments';

export type AllocationPreview = {
  items: AllocationOpenItem[];
  allocations: Array<{ open_item_id: string; amount: string }>;
  reason?: 'NO_OPEN_ITEMS';
};

/** Server-side FIFO suggestion for a not-yet-posted payment. */
export async function previewAllocation(input: {
  partyID: string;
  currency: string;
  kind: FinancePaymentKind;
  amount: string;
}): Promise<AllocationPreview> {
  return api<AllocationPreview>('/finance/allocation-preview', {
    method: 'POST',
    body: JSON.stringify({
      party_id: input.partyID,
      currency: input.currency,
      payment_kind: input.kind === 'payment' ? 'PAYMENT' : 'COLLECTION',
      amount: canonicalDecimal(input.amount)
    })
  });
}

/** Open invoices for a party in one currency and side. */
export async function loadOpenItems(input: {
  partyID: string;
  currency: string;
  kind: FinancePaymentKind;
}): Promise<AllocationOpenItem[]> {
  const side = input.kind === 'payment' ? 'PAYABLE' : 'RECEIVABLE';
  const params = new URLSearchParams({
    party_id: input.partyID,
    currency: input.currency,
    side,
    limit: '100'
  });
  const result = await api<{ items?: Array<Record<string, unknown>> }>(
    `/invoice-open-items?${params}`
  );
  return (result.items ?? []).map((item) => ({
    id: String(item.id),
    document_id: item.document_id ? String(item.document_id) : undefined,
    document_no: item.document_no ? String(item.document_no) : undefined,
    document_date: item.document_date ? String(item.document_date) : undefined,
    due_date: item.due_date ? String(item.due_date) : undefined,
    open_amount: String(item.open_amount ?? '0')
  }));
}

/** Distribute a posted payment's remaining advance across oldest debts (server FIFO). */
export async function autoAllocatePosted(kind: FinancePaymentKind, id: string): Promise<void> {
  await api(`${base(kind)}/${encodeURIComponent(id)}/allocations/auto`, {
    method: 'POST',
    headers: { 'Idempotency-Key': crypto.randomUUID() },
    body: JSON.stringify({})
  });
}

/** Apply explicit allocations to a posted payment. */
export async function allocatePosted(
  kind: FinancePaymentKind,
  id: string,
  allocations: Array<{ open_item_id: string; amount: string }>
): Promise<void> {
  await api(`${base(kind)}/${encodeURIComponent(id)}/allocations`, {
    method: 'POST',
    headers: { 'Idempotency-Key': crypto.randomUUID() },
    body: JSON.stringify({ allocations })
  });
}

/** Reverse allocations on a posted payment (append-only). */
export async function unallocatePosted(
  kind: FinancePaymentKind,
  id: string,
  allocationIDs: string[]
): Promise<void> {
  await api(`${base(kind)}/${encodeURIComponent(id)}/allocations/reverse`, {
    method: 'POST',
    headers: { 'Idempotency-Key': crypto.randomUUID() },
    body: JSON.stringify({ allocation_ids: allocationIDs })
  });
}
