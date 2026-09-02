// Package commerce holds the shared commercial line resolution used by both
// sales and purchasing. Price, tax profile and discount defaults are resolved
// here on the server so neither side can become client-driven, and so a rule
// fixed for one direction is fixed for the other at the same time.
package commerce

// Direction selects the commercial side of a document. The catalog carries the
// same split (product_tax_profiles.direction, product_variant_price_overrides
// .direction, products.sales_price / purchase_price), so one resolver serves
// both sides instead of two implementations drifting apart.
type Direction string

const (
	DirectionSales    Direction = "SALES"
	DirectionPurchase Direction = "PURCHASE"
)

func (d Direction) Valid() bool {
	return d == DirectionSales || d == DirectionPurchase
}

// PriceOverridePermission names the permission a user needs to put a price on
// a line that the catalog did not produce.
func (d Direction) PriceOverridePermission() string {
	if d == DirectionPurchase {
		return "purchase.price.override"
	}
	return "sales.price.override"
}

// TaxOverridePermission names the permission a user needs to depart from the
// product's tax profile.
func (d Direction) TaxOverridePermission() string {
	if d == DirectionPurchase {
		return "purchase.tax.override"
	}
	return "sales.tax.override"
}

// Price sources, ordered by precedence within a direction.
const (
	PriceSourceSpecial           = "SPECIAL"
	PriceSourcePartyPriceList    = "PARTY_PRICE_LIST"
	PriceSourceDocumentPriceList = "DOCUMENT_PRICE_LIST"
	PriceSourceLastPurchase      = "LAST_PURCHASE"
	PriceSourceDefault           = "DEFAULT"
	PriceSourceManual            = "MANUAL"
)
