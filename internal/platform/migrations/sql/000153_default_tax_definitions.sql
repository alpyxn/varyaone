-- Two more line taxes in the company tax catalog.
--
-- A product card can carry taxes besides KDV, and the picker on the card only
-- offers definitions the company owns. The catalog a company is seeded with
-- (tax_seed_turkey_simple_tax_definitions_for_company) covers ÖTV, ÖİV, BSMV,
-- damga and a generic "Diğer Vergi", but not TRT payı or konaklama vergisi,
-- which are ordinary line taxes a retailer or a hotel charges. The two are
-- added to the seed and backfilled onto existing companies.
--
-- ÖİV and konaklama vergisi also get the rate the law fixes, so a card that
-- names one without typing a rate prices it correctly; the rates that depend on
-- the goods (ÖTV, TRT payı) stay on the card.
CREATE OR REPLACE FUNCTION tax_seed_turkey_simple_tax_definitions_for_company(company_uuid uuid) RETURNS void
    LANGUAGE plpgsql
    AS $$
DECLARE
    item record;
    definition_id uuid;
BEGIN
    FOR item IN SELECT * FROM (VALUES
        ('BSMV',      'BSMV',              NULL::numeric),
        ('DAMGA',     'Damga Vergisi',     NULL::numeric),
        ('DIGER',     'Diğer Vergi',       NULL::numeric),
        ('KDV_0',     'KDV %0',            0::numeric),
        ('KDV_1',     'KDV %1',            1::numeric),
        ('KDV_10',    'KDV %10',          10::numeric),
        ('KDV_20',    'KDV %20',          20::numeric),
        ('KONAKLAMA', 'Konaklama Vergisi', 2::numeric),
        ('OIV',       'ÖİV',              10::numeric),
        ('OTV',       'ÖTV',               NULL::numeric),
        ('TRT',       'TRT Payı',          NULL::numeric)
    ) AS defaults(code, name, default_rate)
    LOOP
        INSERT INTO tax_definitions(id, company_id, code, name, description, source, source_reference, source_version)
        VALUES (
            gen_random_uuid(), company_uuid, item.code, item.name,
            CASE WHEN item.code LIKE 'KDV_%' THEN 'Türkiye KDV tanımı' ELSE 'Türkiye ek vergi tanımı' END,
            'TR_TAX_LOCALIZATION', 'Varya temel vergi kataloğu', '2026-01'
        )
        ON CONFLICT (company_id, code) DO NOTHING;

        IF item.default_rate IS NOT NULL THEN
            SELECT id INTO definition_id
            FROM tax_definitions
            WHERE company_id = company_uuid AND code = item.code;

            INSERT INTO tax_rates(
                id, company_id, tax_definition_id, rate, calculation_type,
                valid_from, source, source_reference, source_version
            )
            SELECT gen_random_uuid(), company_uuid, definition_id, item.default_rate, 'PERCENTAGE',
                   DATE '2023-07-10', 'TR_TAX_LOCALIZATION', 'Varya temel vergi kataloğu', '2026-01'
            WHERE NOT EXISTS (
                SELECT 1 FROM tax_rates
                WHERE company_id = company_uuid
                  AND tax_definition_id = definition_id
                  AND valid_from = DATE '2023-07-10'
            );
        END IF;
    END LOOP;
END;
$$;

SELECT tax_seed_turkey_simple_tax_definitions_for_company(id) FROM companies;

-- ÖTV is part of the KDV base by law, and the flag that says so has defaulted
-- to false on the component rows. Nothing priced these components before this
-- release, so no posted document changes; the cards start from the correct
-- treatment and each one stays editable on the product card.
UPDATE product_tax_profile_components c
   SET included_in_tax_base = true
  FROM tax_definitions d
 WHERE d.company_id = c.company_id
   AND d.id = c.tax_definition_id
   AND d.code = 'OTV'
   AND NOT c.included_in_tax_base;
