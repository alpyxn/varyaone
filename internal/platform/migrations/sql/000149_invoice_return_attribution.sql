-- Attributing a return to the invoice(s) it actually reduces.
--
-- Numbered 000149, not 000004: the repository squashed migrations 000002..000148
-- into the baseline, but databases created before that squash still record those
-- 148 versions individually. The runner skips any version already in
-- platform_schema_migrations, so a new migration numbered inside that range
-- would be silently treated as applied on every existing installation and only
-- ever run on a fresh one. New migrations continue from 000149.
--
-- Finance had six copies of the same "how much of this invoice has been
-- returned" expression (open item list, aging, settlement, allocation capacity
-- and the sales/purchasing status projections). All six matched a return to an
-- invoice by document relation: the return belongs to the invoice when it names
-- the invoice as its source, or when it names any document the invoice also
-- lists as a source (typically the irsaliye/mal kabul the invoice was raised
-- from).
--
-- That last rule double counts. One dispatch can be invoiced by more than one
-- invoice (partial invoicing is quantity based), so a return raised against the
-- dispatch was deducted in full from every invoice sharing it: an 1.000 TL
-- receivable split into two 500 TL invoices went to zero after a single 500 TL
-- return, and both invoices reported themselves as fully collected.
--
-- This view keeps the same candidate set but gives each candidate a share of
-- the return instead of all of it. The share is the fraction of the return's
-- allocated line quantity that actually reaches that invoice, resolved through
-- the line ledger: a return line points at the invoice line it returns
-- (share 1), or at a dispatch/receipt line, in which case the share follows the
-- INVOICING allocations that turned that line into invoice lines. The shares of
-- one return therefore sum to at most 1 -- never more -- and a return that maps
-- entirely to one invoice yields exactly 1, which is what every existing
-- single-invoice case already computed.
--
-- Two deliberate limits, both bounded by the conservation rule above:
--   * the share is a quantity ratio applied to the return's money total, so a
--     return whose lines carry different unit prices AND is split across
--     invoices distributes proportionally to quantity rather than to value;
--   * a return with no line allocations at all has nothing to resolve, so it
--     keeps the old whole-amount behaviour against each candidate. Both return
--     paths write those allocations today, so this only covers data that
--     predates the line ledger.
--
-- Shape matters as much as content here: the view is written as a single flat
-- query with no CTEs, because a CTE referenced twice is materialized and the
-- callers' `document_id = $n` never reaches the index scans. Every caller reads
-- one invoice (or one page of them) at a time, so the candidate pairs must stay
-- pushdown-friendly: both branches of the UNION resolve through
-- commercial_document_sources' primary key or its source index.

CREATE INDEX IF NOT EXISTS commercial_line_registry_line_document_idx
    ON commercial_line_registry USING btree (company_id, document_id);

CREATE VIEW finance_invoice_return_attributions AS
SELECT pair.company_id,
       pair.invoice_document_id AS document_id,
       pair.return_document_id,
       return_item.document_date AS return_document_date,
       round(GREATEST(return_item.original_amount - COALESCE(return_reversal.amount, 0), 0)
             * COALESCE(share.fraction, 1), 4) AS amount
  FROM (
      -- The invoice named directly by the return, plus every invoice that
      -- lists the same source document the return was raised against.
      SELECT relation.company_id,
             relation.document_id AS return_document_id,
             relation.source_document_id AS invoice_document_id
        FROM commercial_document_sources relation
       WHERE relation.relation_type = 'RETURN'
      UNION
      SELECT relation.company_id,
             relation.document_id,
             sibling.document_id
        FROM commercial_document_sources relation
        JOIN commercial_document_sources sibling
          ON sibling.company_id = relation.company_id
         AND sibling.source_document_id = relation.source_document_id
         AND sibling.document_id <> relation.document_id
       WHERE relation.relation_type = 'RETURN'
  ) pair
  JOIN documents invoice_document
    ON invoice_document.company_id = pair.company_id
   AND invoice_document.id = pair.invoice_document_id
   AND invoice_document.document_type_code IN ('SALES_INVOICE', 'PURCHASE_INVOICE')
  JOIN documents return_document
    ON return_document.company_id = pair.company_id
   AND return_document.id = pair.return_document_id
   AND return_document.status = 'POSTED'
   AND return_document.document_type_code IN ('SALES_RETURN_INVOICE', 'PURCHASE_RETURN_INVOICE')
  JOIN finance_invoice_open_items return_item
    ON return_item.company_id = pair.company_id
   AND return_item.document_id = pair.return_document_id
  LEFT JOIN finance_invoice_open_item_reversals return_reversal
    ON return_reversal.company_id = return_item.company_id
   AND return_reversal.open_item_id = return_item.id
  LEFT JOIN LATERAL (
      SELECT SUM(return_allocation.base_quantity * share_of_line.fraction)
             / NULLIF(SUM(return_allocation.base_quantity), 0) AS fraction
        FROM commercial_line_registry return_line
        JOIN commercial_line_allocations return_allocation
          ON return_allocation.company_id = return_line.company_id
         AND return_allocation.target_line_id = return_line.line_id
         AND return_allocation.allocation_type = 'RETURN'
        JOIN LATERAL (
            SELECT CASE
                       WHEN EXISTS (SELECT 1
                                      FROM commercial_line_registry invoice_line
                                     WHERE invoice_line.company_id = pair.company_id
                                       AND invoice_line.line_id = return_allocation.source_line_id
                                       AND invoice_line.document_id = pair.invoice_document_id)
                       -- The return line points straight at a line of this invoice.
                       THEN 1::numeric
                       -- Otherwise it points at a dispatch/receipt line: follow the
                       -- INVOICING allocations that turned it into invoice lines and
                       -- take this invoice's share of them.
                       ELSE COALESCE((SELECT SUM(invoicing.base_quantity)
                                        FROM commercial_line_allocations invoicing
                                        JOIN commercial_line_registry invoice_line
                                          ON invoice_line.company_id = invoicing.company_id
                                         AND invoice_line.line_id = invoicing.target_line_id
                                       WHERE invoicing.company_id = pair.company_id
                                         AND invoicing.source_line_id = return_allocation.source_line_id
                                         AND invoicing.allocation_type = 'INVOICING'
                                         AND invoice_line.document_id = pair.invoice_document_id)
                                     / NULLIF((SELECT SUM(all_invoicing.base_quantity)
                                                 FROM commercial_line_allocations all_invoicing
                                                WHERE all_invoicing.company_id = pair.company_id
                                                  AND all_invoicing.source_line_id = return_allocation.source_line_id
                                                  AND all_invoicing.allocation_type = 'INVOICING'), 0), 0)
                   END AS fraction
        ) share_of_line ON TRUE
       WHERE return_line.company_id = pair.company_id
         AND return_line.document_id = pair.return_document_id
  ) share ON TRUE;
