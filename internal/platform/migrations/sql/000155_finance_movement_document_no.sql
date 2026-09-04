-- Manuel kasa/banka hareketlerine insan tarafından okunabilir bir numara.
--
-- Bir tahsilat, ödeme veya hesap transferinden doğan hareket zaten kaynak
-- belgesinin numarasıyla anılabiliyor (TAH-/ODM-/TRF-). Elle girilen hareketin
-- ve açılış bakiyesinin ise kaynağı kendisidir; numarası olmadığı için ekranda
-- kimliği (UUID) görünüyordu.
--
-- Numara KH-YYYY-NNNNNN biçiminde, company_business_sequences üzerinden,
-- tahsilat/ödeme numaralarıyla aynı mekanizmayla üretilir.
--
-- Tablo append-only (finance_account_movements_no_update), bu yüzden mevcut
-- satırlar geriye dönük numaralandırılmaz: onlar boş kalır ve ekranda tutar +
-- tarihle tarif edilir. Bu migrasyondan sonra oluşan manuel hareketler numara
-- alır. Kolon eklemek satır tetikleyicisini çalıştırmaz.
ALTER TABLE finance_account_movements
    ADD COLUMN IF NOT EXISTS document_no text DEFAULT ''::text NOT NULL;

-- Aynı şirkette bir numara iki harekete verilemez. Numarasız (boş) satırlar
-- kısmi indeks sayesinde kısıtın dışında kalır.
CREATE UNIQUE INDEX IF NOT EXISTS finance_account_movements_company_id_document_no_key
    ON finance_account_movements (company_id, document_no)
    WHERE btrim(document_no) <> ''::text;
