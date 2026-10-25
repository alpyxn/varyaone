-- Adayda adres, web sitesi ve öncelik. Soğuk arama listeleri bu bilgileri
-- zaten taşıyor; öncelik (A en sıcak … E elenmiş) arama sırasını belirler.
ALTER TABLE crm_leads ADD COLUMN address text NOT NULL DEFAULT '' CHECK (char_length(address) <= 500);
ALTER TABLE crm_leads ADD COLUMN website text NOT NULL DEFAULT '' CHECK (char_length(website) <= 200);
ALTER TABLE crm_leads ADD COLUMN priority text NOT NULL DEFAULT '' CHECK (priority ~ '^[A-E]?$');

CREATE INDEX crm_leads_company_priority_idx ON crm_leads (company_id, priority);
