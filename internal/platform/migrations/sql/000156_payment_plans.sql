-- Plans are scheduling metadata; the invoice and payment ledgers stay immutable.
CREATE TABLE finance_payment_plans (
    id uuid PRIMARY KEY,
    company_id uuid NOT NULL REFERENCES companies(id),
    party_id uuid NOT NULL,
    side text NOT NULL CHECK (side IN ('RECEIVABLE','PAYABLE')),
    currency text NOT NULL,
    description text NOT NULL CHECK (length(description) BETWEEN 1 AND 500),
    total_amount numeric(24,4) NOT NULL CHECK (total_amount > 0),
    idempotency_key text NOT NULL,
    request_hash text NOT NULL,
    created_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    cancelled_at timestamptz,
    cancel_reason text,
    version bigint NOT NULL DEFAULT 1,
    UNIQUE(company_id,id),
    UNIQUE(company_id,idempotency_key),
    FOREIGN KEY(company_id,party_id) REFERENCES parties(company_id,id),
    CHECK ((cancelled_at IS NULL AND cancel_reason IS NULL) OR
           (cancelled_at IS NOT NULL AND length(btrim(cancel_reason)) BETWEEN 1 AND 500))
);
CREATE TABLE finance_payment_plan_sources (
    company_id uuid NOT NULL,
    plan_id uuid NOT NULL,
    open_item_id uuid NOT NULL,
    amount numeric(24,4) NOT NULL CHECK(amount > 0),
    active boolean NOT NULL DEFAULT true,
    PRIMARY KEY(company_id,plan_id,open_item_id),
    FOREIGN KEY(company_id,plan_id) REFERENCES finance_payment_plans(company_id,id),
    FOREIGN KEY(company_id,open_item_id) REFERENCES finance_invoice_open_items(company_id,id)
);
CREATE UNIQUE INDEX finance_payment_plan_one_active_source
    ON finance_payment_plan_sources(company_id,open_item_id) WHERE active;
CREATE TABLE finance_payment_plan_parts (
    company_id uuid NOT NULL,
    plan_id uuid NOT NULL,
    open_item_id uuid NOT NULL,
    installment_no integer NOT NULL CHECK(installment_no BETWEEN 1 AND 120),
    due_date date NOT NULL,
    amount numeric(24,4) NOT NULL CHECK(amount > 0),
    start_amount numeric(24,4) NOT NULL CHECK(start_amount >= 0),
    PRIMARY KEY(company_id,plan_id,open_item_id,installment_no),
    FOREIGN KEY(company_id,plan_id,open_item_id)
      REFERENCES finance_payment_plan_sources(company_id,plan_id,open_item_id)
);
CREATE TRIGGER finance_payment_plan_parts_immutable BEFORE UPDATE OR DELETE
    ON finance_payment_plan_parts FOR EACH ROW EXECUTE FUNCTION reject_finance_posted_mutation();

-- Shared historical read model. A plan consumes the then-open balance; any
-- later reopening above that balance remains an unscheduled invoice residual.
-- Every invoice appears exactly once, possibly as multiple dated portions.
CREATE FUNCTION finance_scheduled_dues(p_company uuid, p_as_of date, p_open_item uuid DEFAULT NULL, p_plan uuid DEFAULT NULL)
RETURNS TABLE(open_item_id uuid, plan_id uuid, installment_no integer,
              due_date date, scheduled_amount numeric, open_amount numeric)
LANGUAGE sql STABLE AS $$
WITH balances AS (
 SELECT oi.id, oi.due_date, oi.document_date,
   GREATEST(oi.original_amount
    - COALESCE((SELECT sum(CASE WHEN a.reversal_of_id IS NULL THEN a.amount ELSE -a.amount END)
        FROM finance_payment_allocations a JOIN finance_payments p
          ON p.company_id=a.company_id AND p.id=a.payment_id
       WHERE a.company_id=oi.company_id AND a.open_item_id=oi.id AND p.transaction_date<=p_as_of),0)
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

DO $$ DECLARE t text; BEGIN
 FOREACH t IN ARRAY ARRAY['finance_payment_plans','finance_payment_plan_sources','finance_payment_plan_parts'] LOOP
  EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY',t);
  EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY',t);
  EXECUTE format($p$CREATE POLICY company_isolation ON %I USING (
   NULLIF(current_setting('varyaone.company_id',true),'') IS NULL OR company_id::text=current_setting('varyaone.company_id',true))
   WITH CHECK (NULLIF(current_setting('varyaone.company_id',true),'') IS NULL OR company_id::text=current_setting('varyaone.company_id',true))$p$,t);
 END LOOP;
END $$;

-- A source's dated portions must partition exactly the agreed open amount.
-- Deferred checks allow the complete plan to be inserted in one transaction.
CREATE FUNCTION check_payment_plan_totals() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE c uuid := NEW.company_id; p uuid; BEGIN
 IF TG_TABLE_NAME='finance_payment_plans' THEN p:=NEW.id; ELSE p:=NEW.plan_id; END IF;
 IF EXISTS(SELECT 1 FROM finance_payment_plans h WHERE h.company_id=c AND h.id=p AND
   h.total_amount<>(SELECT COALESCE(sum(s.amount),0) FROM finance_payment_plan_sources s WHERE s.company_id=c AND s.plan_id=p))
 OR EXISTS(SELECT 1 FROM finance_payment_plan_sources s JOIN finance_payment_plans h ON h.company_id=s.company_id AND h.id=s.plan_id
   JOIN finance_invoice_open_items oi ON oi.company_id=s.company_id AND oi.id=s.open_item_id
   WHERE s.company_id=c AND s.plan_id=p AND
   (s.amount<>(SELECT COALESCE(sum(d.amount),0) FROM finance_payment_plan_parts d WHERE d.company_id=c AND d.plan_id=p AND d.open_item_id=s.open_item_id)
    OR oi.party_id<>h.party_id OR oi.currency<>h.currency OR oi.side<>h.side OR s.active<>(h.cancelled_at IS NULL)))
 OR EXISTS(SELECT 1 FROM (
   SELECT start_amount,COALESCE(sum(amount) OVER(PARTITION BY open_item_id ORDER BY installment_no ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING),0) AS expected
   FROM finance_payment_plan_parts WHERE company_id=c AND plan_id=p) d WHERE start_amount<>expected)
 THEN RAISE EXCEPTION 'payment plan sources and installments must balance' USING ERRCODE='23514'; END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER payment_plan_totals AFTER INSERT OR UPDATE ON finance_payment_plans DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION check_payment_plan_totals();
CREATE CONSTRAINT TRIGGER payment_plan_source_totals AFTER INSERT OR UPDATE ON finance_payment_plan_sources DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION check_payment_plan_totals();
CREATE CONSTRAINT TRIGGER payment_plan_part_totals AFTER INSERT ON finance_payment_plan_parts DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION check_payment_plan_totals();
CREATE FUNCTION guard_payment_plan_history() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'payment plan history cannot be deleted' USING ERRCODE='55000'; END IF;
 IF TG_TABLE_NAME='finance_payment_plans' THEN
  IF OLD.cancelled_at IS NOT NULL OR NEW.cancelled_at IS NULL OR NEW.version<>OLD.version+1
   OR (to_jsonb(NEW)-ARRAY['cancelled_at','cancel_reason','version'])<>(to_jsonb(OLD)-ARRAY['cancelled_at','cancel_reason','version'])
  THEN RAISE EXCEPTION 'payment plan only supports cancellation' USING ERRCODE='55000'; END IF;
 ELSE
  IF NOT OLD.active OR NEW.active OR (to_jsonb(NEW)-'active')<>(to_jsonb(OLD)-'active')
  THEN RAISE EXCEPTION 'payment plan source is immutable' USING ERRCODE='55000'; END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER payment_plan_history BEFORE UPDATE OR DELETE ON finance_payment_plans FOR EACH ROW EXECUTE FUNCTION guard_payment_plan_history();
CREATE TRIGGER payment_plan_source_history BEFORE UPDATE OR DELETE ON finance_payment_plan_sources FOR EACH ROW EXECUTE FUNCTION guard_payment_plan_history();
