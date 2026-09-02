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
	// posts a computed tax_amount instead. The resolved profile's components
	// are the only server-verified source of what that amount should be, so
	// a mismatch is treated the same way a rate override is treated on the
	// sales side.
	quantity := line.Quantity
	if strings.TrimSpace(quantity) == "" {
		quantity = "0"
	}
	expected, calcErr := taxes.Calculate(taxes.TaxCalculationInput{
		Lines: []taxes.TaxCalculationLine{{
			UnitPrice: line.UnitPrice,
			Quantity:  quantity,
			Discounts: purchaseDiscountChain(line),
			Components: func() []taxes.TaxComponent {
				if len(defaults.Components) > 0 {
					return defaults.Components
				}
				return []taxes.TaxComponent{{CalculationType: taxes.TaxPercentage, Rate: "0"}}
			}(),
		}},
		RoundScale:  8,
		RoundPolicy: taxes.RoundHalfUp,
	})
	if calcErr == nil && len(expected.Lines) == 1 {
		expectedTax := expected.Lines[0].TaxAmount
		suppliedTax := strings.TrimSpace(line.TaxAmount)
		if suppliedTax != "" && compare(suppliedTax, expectedTax) != 0 && !session.HasPermission(commerce.DirectionPurchase.TaxOverridePermission()) {
			return validation("vergi tutarı ürünün vergi profiliyle eşleşmiyor; geçersiz kılmak için yetki gereklidir")
		}
		if suppliedTax == "" {
			line.TaxAmount = expectedTax
		}
	}

	if len(line.TaxComponentsSnapshot) == 0 && len(defaults.Components) > 0 {
		encoded, marshalErr := json.Marshal(defaults.Components)
		if marshalErr == nil {
			var generic []any
			if json.Unmarshal(encoded, &generic) == nil {
				line.TaxComponentsSnapshot = generic
			}
		}
	}
	return nil
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
