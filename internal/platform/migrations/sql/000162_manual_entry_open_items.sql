-- Manuel cari hareketleri (borçlandırma/alacaklandırma) hiçbir açık kalem
-- üretmiyordu; bu yüzden yaşlandırmada görünmüyor ve tahsilat/ödeme
-- ekranından bu tutar üstüne tahsilat ya da ödeme uygulanamıyordu. Açık
-- kalemler artık ya bir belgeye ya da bir manuel harekete bağlanabilir.
ALTER TABLE finance_invoice_open_items ALTER COLUMN document_id DROP NOT NULL;
ALTER TABLE finance_invoice_open_items ADD COLUMN manual_entry_id uuid;
ALTER TABLE finance_invoice_open_items ADD CONSTRAINT finance_invoice_open_items_company_id_manual_entry_id_fkey
  FOREIGN KEY (company_id, manual_entry_id) REFERENCES finance_manual_entries(company_id, id);
ALTER TABLE finance_invoice_open_items ADD CONSTRAINT finance_invoice_open_items_source_check
  CHECK (((document_id IS NOT NULL) AND (manual_entry_id IS NULL)) OR ((document_id IS NULL) AND (manual_entry_id IS NOT NULL)));
CREATE UNIQUE INDEX finance_invoice_open_items_manual_entry_id_key ON finance_invoice_open_items(manual_entry_id) WHERE manual_entry_id IS NOT NULL;

ALTER TABLE finance_invoice_open_item_reversals ALTER COLUMN document_id DROP NOT NULL;

ALTER TABLE finance_payment_allocations DROP CONSTRAINT finance_payment_allocations_target_type_check;
ALTER TABLE finance_payment_allocations ADD CONSTRAINT finance_payment_allocations_target_type_check
  CHECK ((target_type = ANY (ARRAY['PARTY_LEDGER'::text, 'DOCUMENT'::text, 'MANUAL_ENTRY'::text])));

ALTER TABLE finance_payment_allocations DROP CONSTRAINT finance_payment_allocations_open_item_target_check;
ALTER TABLE finance_payment_allocations ADD CONSTRAINT finance_payment_allocations_open_item_target_check
  CHECK ((((target_type = 'DOCUMENT'::text OR target_type = 'MANUAL_ENTRY'::text) AND (open_item_id IS NOT NULL)) OR ((target_type = 'PARTY_LEDGER'::text) AND (open_item_id IS NULL))));
