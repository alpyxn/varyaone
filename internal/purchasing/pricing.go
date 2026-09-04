package purchasing

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/alpyxn/varyaone/internal/commerce"
	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/taxes"
	"github.com/jackc/pgx/v5"
)

// resolvePurchaseInvoiceLineDefaults is the purchasing counterpart to sales'
// resolveCommercialLineDefaults: the server resolves the supplier's price and
// the product's PURCHASE-direction tax profile from the catalog through the
// shared internal/commerce resolver, and validateInvoiceLine's arithmetic
// checks are no longer the only thing standing between a client payload and
// a posted tax amount. A price or tax amount that disagrees with the
// resolved catalog now requires purchase.price.override / purchase.tax.override.
func (s *Service) resolvePurchaseInvoiceLineDefaults(ctx context.Context, tx pgx.Tx, session identity.Session, input PurchaseInvoiceInput, line *PurchaseInvoiceLine, lineNumber int) error {
	if line.LineType != "PRODUCT" && line.LineType != "SERVICE" {
		return nil
	}
	var baseCurrency string
	if err := tx.QueryRow(ctx, `SELECT base_currency::text FROM companies WHERE id=$1`, session.CurrentCompanyID).Scan(&baseCurrency); err != nil {
		return err
	}
	doc := commerce.DocumentContext{
		Direction:    commerce.DirectionPurchase,
		CompanyID:    session.CurrentCompanyID,
		PartyID:      input.SupplierID,
		CurrencyCode: strings.ToUpper(strings.TrimSpace(input.Currency)),
		BaseCurrency: baseCurrency,
		ExchangeRate: input.ExchangeRate,
		DocumentDate: input.InvoiceDate.Format("2006-01-02"),
	}
	defaults, err := commerce.ResolveLineDefaults(ctx, tx, doc,
		commerce.LineContext{ProductID: line.ProductID, VariantID: line.VariantID, UnitCode: line.UnitCode},
		line.UnitPrice)
	if err != nil {
		switch {
		case errors.Is(err, commerce.ErrExchangeRateUnavailable):
			return validation("temel fiyat belge para birimine çevrilemedi")
		case errors.Is(err, commerce.ErrPriceUnavailable):
			return validation("alış fiyatı bulunamadı")
		default:
			return err
		}
	}
	if line.UnitPrice == "" {
		line.UnitPrice = defaults.UnitPrice
	} else if defaults.PriceSource == commerce.PriceSourceManual && !session.HasPermission(commerce.DirectionPurchase.PriceOverridePermission()) {
		return identity.ErrForbidden
	}

	// The invoice line does not carry a tax_rate field of its own; the client
	// posts computed amounts instead. The resolved profile - VAT plus every
	// additional tax on the product card - is the only server-verified source
	// of what those amounts should be, so a mismatch is treated the same way a
	// rate override is treated on the sales side. The line amounts themselves
	// are taken from the engine, which is what makes a tax-included purchase
	// price and an ÖTV-style component add up.
	quantity := line.Quantity
	if strings.TrimSpace(quantity) == "" {
		quantity = "0"
	}
	taxMode := taxes.TaxModeExclusive
	if defaults.TaxIncluded {
		taxMode = taxes.TaxModeInclusive
	}
	expected, calcErr := taxes.Calculate(taxes.TaxCalculationInput{
		Lines: []taxes.TaxCalculationLine{{
			UnitPrice:  line.UnitPrice,
			Quantity:   quantity,
			Discounts:  purchaseDiscountChain(line),
			Components: defaults.Components,
		}},
		TaxMode:     taxMode,
		RoundScale:  8,
		RoundPolicy: taxes.RoundHalfUp,
	})
	if calcErr != nil {
		return validation("vergi hesaplaması geçersiz")
	}
	computed := expected.Lines[0]
	expectedTax := computed.TaxAmount
	suppliedTax := strings.TrimSpace(line.TaxAmount)
	overridden := suppliedTax != "" && taxDiffersMaterially(suppliedTax, expectedTax)
	if overridden && !session.HasPermission(commerce.DirectionPurchase.TaxOverridePermission()) {
		return validation("vergi tutarı ürünün vergi profiliyle eşleşmiyor; geçersiz kılmak için yetki gereklidir")
	}
	if !overridden {
		line.TaxAmount = expectedTax
	}
	line.GrossAmount = computed.GrossAmount
	line.DiscountAmount = computed.DiscountAmount
	line.TaxBase = computed.TaxableAmount
	if strings.TrimSpace(line.WithholdingAmount) == "" {
		line.WithholdingAmount = "0"
	}
	// Tevkifat on a purchase follows the supplier-facing product profile the
	// same way sales does, and is withheld from the VAT part alone.
	if compare(line.WithholdingAmount, "0") == 0 && compare(zero(defaults.WithholdingRate), "0") > 0 {
		line.WithholdingAmount = divide(multiply(primaryComponentAmount(computed.Components, line.TaxAmount), defaults.WithholdingRate), "100")
	}
	line.PayableAmount = subtract(add(line.TaxBase, line.TaxAmount), line.WithholdingAmount)
	if compare(line.PayableAmount, "0") < 0 {
		return validation("alış faturası ödenecek tutarı satır tutarlarıyla eşleşmiyor")
	}

	// The stored breakdown is the engine's, not the client's: it names every
	// tax on the line with the base it was charged on. The VAT entry also
	// carries whether the price included tax, which is the only line-level
	// place a purchase invoice has for it.
	line.TaxComponentsSnapshot = componentsSnapshot(computed.Components, defaults.TaxIncluded)
	return nil
}

// taxDiffersMaterially ignores sub-kuruş drift between the client's preview
// arithmetic and the engine's: only a real override should have to be one.
func taxDiffersMaterially(supplied, expected string) bool {
	difference := subtract(supplied, expected)
	if compare(difference, "0") < 0 {
		difference = subtract("0", difference)
	}
	return compare(difference, "0.005") > 0
}

// primaryComponentAmount is the VAT part of a computed breakdown; a profile
// with no VAT component falls back to the line's whole tax.
func primaryComponentAmount(components []taxes.TaxCalculationComponentResult, fallback string) string {
	for _, component := range components {
		if component.Primary && strings.TrimSpace(component.Amount) != "" {
			return component.Amount
		}
	}
	return fallback
}

func componentsSnapshot(components []taxes.TaxCalculationComponentResult, taxIncluded bool) []any {
	encoded, err := json.Marshal(components)
	if err != nil {
		return nil
	}
	var generic []map[string]any
	if err = json.Unmarshal(encoded, &generic); err != nil {
		return nil
	}
	snapshot := make([]any, 0, len(generic))
	for index, entry := range generic {
		if index == 0 || entry["primary"] == true {
			entry["included"] = taxIncluded
		}
		snapshot = append(snapshot, entry)
	}
	return snapshot
}

// purchaseDiscountChain expresses a purchase invoice line's single
// discount_amount as the same one-step cascade the sales side would build,
// so both directions run the exact same engine call shape.
func purchaseDiscountChain(line *PurchaseInvoiceLine) []taxes.TaxDiscount {
	if strings.TrimSpace(line.DiscountAmount) == "" || line.DiscountAmount == "0" {
		return nil
	}
	return []taxes.TaxDiscount{{Kind: taxes.DiscountFixed, Amount: line.DiscountAmount}}
}
