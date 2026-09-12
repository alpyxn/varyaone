-- Preserve allocation history without rewriting posted financial rows.
-- NULL is intentional for legacy rows and older binaries: payment allocations
-- use the payment business date; same-payment unallocations use their own date.
-- New allocation/reallocation commands explicitly record their effective date.
ALTER TABLE finance_payment_allocations ADD COLUMN effective_date date;

CREATE OR REPLACE FUNCTION finance_scheduled_dues(p_company uuid, p_as_of date, p_open_item uuid DEFAULT NULL, p_plan uuid DEFAULT NULL)
RETURNS TABLE(open_item_id uuid, plan_id uuid, installment_no integer,
              due_date date, scheduled_amount numeric, open_amount numeric)
LANGUAGE sql STABLE AS $$
WITH balances AS (
 SELECT oi.id, oi.due_date, oi.document_date,
   GREATEST(oi.original_amount
    - COALESCE((SELECT sum(CASE WHEN a.reversal_of_id IS NULL THEN a.amount ELSE -a.amount END)
        FROM finance_payment_allocations a JOIN finance_payments p
          ON p.company_id=a.company_id AND p.id=a.payment_id
       WHERE a.company_id=oi.company_id AND a.open_item_id=oi.id AND COALESCE(a.effective_date,
         CASE WHEN a.reversal_of_id IS NOT NULL AND p.reversal_of_id IS NULL
              THEN (a.allocated_at AT TIME ZONE 'UTC')::date
              ELSE p.transaction_date END)<=p_as_of),0)
    - COALESCE((SELECT r.amount FROM finance_invoice_open_item_reversals r
        JOIN party_ledger_entries l ON l.company_id=r.company_id AND l.id=r.reversal_ledger_entry_id
       WHERE r.company_id=oi.company_id AND r.open_item_id=oi.id AND l.document_date<=p_as_of),0)
    - COALESCE((SELECT sum(r.amount) FROM finance_invoice_return_attributions r
       WHERE r.company_id=oi.company_id AND r.document_id=oi.document_id AND r.return_document_date<=p_as_of),0),0) AS remaining
 FROM finance_invoice_open_items oi WHERE oi.company_id=p_company AND oi.document_date<=p_as_of
 AND (p_open_item IS NULL OR oi.id=p_open_item)
 AND (p_plan IS NULL OR EXISTS(SELECT 1 FROM finance_payment_plan_sources ps WHERE ps.company_id=p_company AND ps.plan_id=p_plan AND ps.open_item_id=oi.id))
), sources AS (
 SELECT s.* FROM finance_payment_plan_sources s JOIN finance_payment_plans p
   ON p.company_id=s.company_id AND p.id=s.plan_id
 WHERE s.company_id=p_company AND p.created_at::date<=p_as_of
   AND (p.cancelled_at IS NULL OR p.cancelled_at::date>p_as_of)
), parts AS (
 SELECT b.id, s.plan_id, d.installment_no, d.due_date, d.amount,
   LEAST(d.amount,GREATEST(b.remaining-(s.amount-d.start_amount-d.amount),0)) AS remaining
 FROM balances b JOIN sources s ON s.open_item_id=b.id
 JOIN finance_payment_plan_parts d ON d.company_id=s.company_id AND d.plan_id=s.plan_id AND d.open_item_id=s.open_item_id
)
SELECT id,plan_id,installment_no,due_date,amount,remaining FROM parts
UNION ALL
SELECT b.id,NULL::uuid,0,b.due_date,
       GREATEST(b.remaining-COALESCE(s.amount,0),0),GREATEST(b.remaining-COALESCE(s.amount,0),0)
 FROM balances b LEFT JOIN sources s ON s.open_item_id=b.id
 WHERE s.plan_id IS NULL OR b.remaining>s.amount
$$;
