package commerce

import (
	"context"
	"strings"

	"github.com/alpyxn/varyaone/internal/taxes"
)

// resolveCatalog reads the product card, the directional tax profile and the
// unit conversion in one round trip, and returns the ordered price candidates
// for the direction.
func resolveCatalog(ctx context.Context, q Querier, doc DocumentContext, line LineContext) (LineDefaults, []PriceCandidate, error) {
	var defaults LineDefaults
	var variantPrice, partyListPrice, documentListPrice, cardPrice, lastPurchasePrice string
	var unitFactor string

	// The variant override, the card price and the tax profile all carry the
	// direction, so the same statement serves sales and purchasing.
	query := `
		SELECT
		COALESCE((SELECT v.unit_price::text
		           FROM product_variant_price_overrides v
		          WHERE v.company_id=p.company_id
		            AND v.variant_id=NULLIF($3,'')::uuid
		            AND v.direction=$9),''),
		COALESCE((SELECT e.unit_price::text
		           FROM price_list_entries e
		           JOIN price_lists pl ON pl.company_id=e.company_id AND pl.id=e.price_list_id
		          WHERE $9='SALES' AND e.company_id=p.company_id
		            AND e.price_list_id=(SELECT party.price_list_id FROM parties party WHERE party.company_id=p.company_id AND party.id=$4)
		            AND pl.is_active AND pl.currency_code=$5 AND e.item_id=p.id
		            AND (e.variant_id=NULLIF($3,'')::uuid OR e.variant_id IS NULL)
		            AND e.valid_from <= $6::date AND (e.valid_to IS NULL OR e.valid_to >= $6::date)
		          ORDER BY (e.variant_id IS NULL),e.valid_from DESC,e.id LIMIT 1),''),
		COALESCE((SELECT e.unit_price::text
		           FROM price_list_entries e
		           JOIN price_lists pl ON pl.company_id=e.company_id AND pl.id=e.price_list_id
		          WHERE $9='SALES' AND e.company_id=p.company_id
		            AND e.price_list_id=NULLIF($7,'')::uuid
		            AND pl.is_active AND pl.currency_code=$5 AND e.item_id=p.id
		            AND (e.variant_id=NULLIF($3,'')::uuid OR e.variant_id IS NULL)
		            AND e.valid_from <= $6::date AND (e.valid_to IS NULL OR e.valid_to >= $6::date)
		          ORDER BY (e.variant_id IS NULL),e.valid_from DESC,e.id LIMIT 1),''),
		CASE WHEN $9='PURCHASE' THEN p.purchase_price::text ELSE p.sales_price::text END,
		COALESCE((SELECT r.unit_price::text
		           FROM supplier_purchase_prices r
		          WHERE $9='PURCHASE' AND r.company_id=p.company_id
		            AND r.supplier_id=$4 AND r.product_id=p.id
		            AND r.variant_id IS NOT DISTINCT FROM NULLIF($3,'')::uuid
		            AND r.currency=$5
		            AND r.observed_at::date <= $6::date
		          ORDER BY r.observed_at DESC LIMIT 1),''),
		COALESCE(ptp.rate,CASE WHEN $9='PURCHASE' THEN p.purchase_tax_rate ELSE p.sales_tax_rate END,0)::text,
		COALESCE(ptp.tax_included,CASE WHEN $9='PURCHASE' THEN p.purchase_tax_included ELSE p.sales_tax_included END,false),
		COALESCE(ptp.withholding_rate,p.withholding_rate,0)::text,
		COALESCE(ptp.withholding_code,NULLIF(p.withholding_code,''),''),
		COALESCE(ptp.treatment,'STANDARD'),
		COALESCE(ptp.tax_code,NULLIF(CASE WHEN $9='PURCHASE' THEN p.purchase_tax_type ELSE p.sales_tax_type END,''),''),
		COALESCE(ptp.exemption_code,NULLIF(p.exemption_code,''),''),
		COALESCE(ptp.tax_note,NULLIF(p.tax_note,''),''),
		COALESCE(ptp.version,0),
		COALESCE((SELECT pu.conversion_factor::text
		           FROM product_units pu
		          WHERE pu.company_id=p.company_id AND pu.product_id=p.id AND pu.unit_code=$8),'')
		FROM products p
		LEFT JOIN product_tax_profiles ptp
		       ON ptp.company_id=p.company_id AND ptp.product_id=p.id AND ptp.direction=$9
		WHERE p.company_id=$1 AND p.id=$2`

	if err := q.QueryRow(ctx, query,
		doc.CompanyID, line.ProductID, line.VariantID, doc.PartyID, doc.CurrencyCode,
		doc.DocumentDate, doc.PriceListID, line.UnitCode, string(doc.Direction),
	).Scan(
		&variantPrice, &partyListPrice, &documentListPrice, &cardPrice, &lastPurchasePrice,
		&defaults.TaxRate, &defaults.TaxIncluded, &defaults.WithholdingRate,
		&defaults.WithholdingCode, &defaults.Treatment, &defaults.TaxCode,
		&defaults.ExemptionCode, &defaults.TaxNote, &defaults.ProfileVersion, &unitFactor,
	); err != nil {
		return LineDefaults{}, nil, err
	}
	defaults.ConversionFactor = unitFactor

	// Card prices are held in the company base currency; a foreign currency
	// document divides them by its own immutable rate.
	if base := strings.ToUpper(strings.TrimSpace(doc.BaseCurrency)); base != "" && base != strings.ToUpper(strings.TrimSpace(doc.CurrencyCode)) {
		var err error
		if variantPrice, err = ConvertBasePrice(variantPrice, doc.ExchangeRate); err != nil {
			return LineDefaults{}, nil, err
		}
		if cardPrice, err = ConvertBasePrice(cardPrice, doc.ExchangeRate); err != nil {
			return LineDefaults{}, nil, err
		}
	}

	var candidates []PriceCandidate
	if doc.Direction == DirectionPurchase {
		// What this supplier last charged for the item beats the generic card
		// price, which is how a buyer expects a line to prefill.
		candidates = []PriceCandidate{
			{variantPrice, PriceSourceSpecial, ""},
			{lastPurchasePrice, PriceSourceLastPurchase, ""},
			{cardPrice, PriceSourceDefault, ""},
		}
	} else {
		candidates = []PriceCandidate{
			{variantPrice, PriceSourceSpecial, ""},
			{partyListPrice, PriceSourcePartyPriceList, ""},
			{documentListPrice, PriceSourceDocumentPriceList, doc.PriceListID},
			{cardPrice, PriceSourceDefault, ""},
		}
	}
	return defaults, candidates, nil
}

// resolveComponents builds the line's full component list: the profile's own
// VAT component first (the one whose rate is the document line's tax_rate),
// then every additional tax the product card carries (ÖTV, ÖİV, TRT payı or a
// company-defined tax). A component's value is the rate row it points at, the
// rate typed on the card, or the definition's rate in force on the document
// date - in that order - so both catalog-managed and hand-entered taxes price
// the same way.
func resolveComponents(ctx context.Context, q Querier, doc DocumentContext, line LineContext, defaults LineDefaults) ([]taxes.TaxComponent, error) {
	exempt := defaults.Treatment == "EXEMPT" || defaults.Treatment == "NOT_APPLICABLE"
	components := []taxes.TaxComponent{{
		Code:            defaults.TaxCode,
		Name:            "KDV",
		CalculationType: taxes.TaxPercentage,
		Rate:            defaultRate(defaults.TaxRate),
		Exempt:          exempt,
		Primary:         true,
	}}
	additional, err := AdditionalComponents(ctx, q, doc.CompanyID, line.ProductID, string(doc.Direction), doc.DocumentDate)
	if err != nil {
		return nil, err
	}
	return append(components, additional...), nil
}

// AdditionalComponents reads the non-VAT components of a product's directional
// tax profile. VAT itself lives on the profile row, so a component pointing at
// a KDV definition would double-count and is skipped.
func AdditionalComponents(ctx context.Context, q Querier, companyID, productID, direction, documentDate string) ([]taxes.TaxComponent, error) {
	rows, err := q.Query(ctx, `
		SELECT COALESCE(td.code,''), COALESCE(td.name,''), c.calculation_type,
		       COALESCE(
		           tr.rate::text,
		           CASE WHEN replace(COALESCE(c.metadata->>'rate',''),',','.') ~ '^[0-9]+(\.[0-9]+)?$'
		                THEN replace(c.metadata->>'rate',',','.') END,
		           (SELECT dr.rate::text FROM tax_rates dr
		             WHERE dr.company_id=c.company_id AND dr.tax_definition_id=c.tax_definition_id
		               AND dr.valid_from <= COALESCE(NULLIF($4,'')::date, CURRENT_DATE)
		               AND (dr.valid_to IS NULL OR dr.valid_to >= COALESCE(NULLIF($4,'')::date, CURRENT_DATE))
		             ORDER BY dr.valid_from DESC, dr.id LIMIT 1),
		           '0') AS rate,
		       c.included_in_tax_base,
		       COALESCE(c.metadata->>'withholding','') = 'true'
		  FROM product_tax_profile_components c
		  JOIN tax_definitions td ON td.company_id=c.company_id AND td.id=c.tax_definition_id
		  LEFT JOIN tax_rates tr ON tr.company_id=c.company_id AND tr.id=c.tax_rate_id
		 WHERE c.company_id=$1 AND c.product_id=$2 AND c.direction=$3
		   AND UPPER(td.code) NOT LIKE 'KDV%'
		 ORDER BY c.sequence`, companyID, productID, direction, documentDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	components := make([]taxes.TaxComponent, 0, 2)
	for rows.Next() {
		var component taxes.TaxComponent
		var calculationType string
		if err := rows.Scan(&component.Code, &component.Name, &calculationType, &component.Rate, &component.IncludedInBase, &component.Withholding); err != nil {
			return nil, err
		}
		component.CalculationType = normalizeCalculationType(calculationType)
		component.Rate = defaultRate(component.Rate)
		components = append(components, component)
	}
	return components, rows.Err()
}

// normalizeCalculationType maps a stored component/rate calculation type onto
// the engine's own set.
func normalizeCalculationType(value string) taxes.TaxCalculationType {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "QUANTITY_BASED":
		return taxes.TaxQuantityBased
	case "FIXED_AMOUNT":
		return taxes.TaxFixedAmount
	default:
		return taxes.TaxPercentage
	}
}

func defaultRate(value string) string {
	if strings.TrimSpace(value) == "" {
		return "0"
	}
	return value
}
