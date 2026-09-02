package commerce

import (
	"context"
	"errors"
	"math/big"
	"strings"

	"github.com/alpyxn/varyaone/internal/taxes"
	"github.com/jackc/pgx/v5"
)

var (
	// ErrPriceUnavailable means the catalog produced no price for the line and
	// the user supplied none either.
	ErrPriceUnavailable = errors.New("commerce: price unavailable")
	// ErrExchangeRateUnavailable means a base-currency price could not be
	// expressed in the document currency.
	ErrExchangeRateUnavailable = errors.New("commerce: exchange rate unavailable")
)

// Querier is satisfied by both *pgxpool.Pool and pgx.Tx, so a resolution can
// run inside the posting transaction or on its own.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// DocumentContext is the header-level input a line resolution depends on.
type DocumentContext struct {
	Direction    Direction
	CompanyID    string
	PartyID      string
	CurrencyCode string
	BaseCurrency string
	ExchangeRate string
	// DocumentDate selects the price list entry and the last purchase price
	// that were valid on the document's own date, not on today.
	DocumentDate string
	PriceListID  string
}

// LineContext identifies the catalog item a line points at.
type LineContext struct {
	ProductID string
	VariantID string
	UnitCode  string
}

// LineDefaults is what the catalog says a line should be. Callers copy these
// into the line snapshot; the client never becomes the source of truth for a
// posted amount or tax profile.
type LineDefaults struct {
	UnitPrice        string
	PriceSource      string
	PriceListID      string
	TaxRate          string
	TaxIncluded      bool
	WithholdingRate  string
	WithholdingCode  string
	Treatment        string
	TaxCode          string
	ExemptionCode    string
	TaxNote          string
	ProfileVersion   int64
	ConversionFactor string
	// Components carries the full directional tax profile, so excise style
	// taxes (ÖTV/ÖİV) and quantity-based components survive onto the document
	// instead of collapsing into a single VAT rate.
	Components []taxes.TaxComponent
}

// ResolveLineDefaults resolves price, tax profile and unit conversion for one
// line from the catalog, in the direction of the document. When suppliedPrice
// is empty the first non-empty catalog candidate is used and its source is
// recorded; when it is not empty it is checked against the catalog
// candidates and classified as that source, or as PriceSourceManual when it
// matches none of them.
func ResolveLineDefaults(ctx context.Context, q Querier, doc DocumentContext, line LineContext, suppliedPrice string) (LineDefaults, error) {
	if !doc.Direction.Valid() {
		return LineDefaults{}, errors.New("commerce: invalid direction")
	}
	defaults, candidates, err := resolveCatalog(ctx, q, doc, line)
	if err != nil {
		return LineDefaults{}, err
	}
	supplied := strings.TrimSpace(suppliedPrice)
	if supplied == "" {
		for _, candidate := range candidates {
			if strings.TrimSpace(candidate.value) == "" {
				continue
			}
			defaults.UnitPrice = candidate.value
			defaults.PriceSource = candidate.source
			defaults.PriceListID = candidate.listID
			break
		}
	} else {
		defaults.UnitPrice = supplied
		if source := ClassifyPrice(candidates, supplied); source != "" {
			defaults.PriceSource = source
			for _, candidate := range candidates {
				if candidate.source == source {
					defaults.PriceListID = candidate.listID
					break
				}
			}
		} else {
			defaults.PriceSource = PriceSourceManual
		}
	}
	if strings.TrimSpace(defaults.UnitPrice) == "" {
		return LineDefaults{}, ErrPriceUnavailable
	}
	defaults.Components, err = resolveComponents(ctx, q, doc, line, defaults)
	if err != nil {
		return LineDefaults{}, err
	}
	return defaults, nil
}

// PriceCandidates returns the ordered catalog prices for a line so a caller
// can tell whether a user-supplied price matches one of them or is a manual
// override needing permission.
func PriceCandidates(ctx context.Context, q Querier, doc DocumentContext, line LineContext) ([]PriceCandidate, error) {
	_, candidates, err := resolveCatalog(ctx, q, doc, line)
	return candidates, err
}

// PriceCandidate is one catalog price with the source that produced it.
type PriceCandidate struct {
	value  string
	source string
	listID string
}

func (c PriceCandidate) Value() string  { return c.value }
func (c PriceCandidate) Source() string { return c.source }
func (c PriceCandidate) ListID() string { return c.listID }

// ClassifyPrice reports the source a supplied price came from. An empty source
// means the price matches nothing in the catalog and is a manual override.
func ClassifyPrice(candidates []PriceCandidate, price string) string {
	supplied := strings.TrimSpace(price)
	if supplied == "" {
		return ""
	}
	suppliedRat, ok := new(big.Rat).SetString(supplied)
	if !ok {
		return ""
	}
	for _, candidate := range candidates {
		value := strings.TrimSpace(candidate.value)
		if value == "" {
			continue
		}
		candidateRat, ok := new(big.Rat).SetString(value)
		if !ok {
			continue
		}
		if candidateRat.Cmp(suppliedRat) == 0 {
			return candidate.source
		}
	}
	return ""
}

// ConvertBasePrice expresses a base-currency catalog price in the document
// currency using the document's own immutable rate. A missing or zero rate is
// an error rather than a silent fallback to 1.
func ConvertBasePrice(price, rate string) (string, error) {
	if strings.TrimSpace(price) == "" {
		return "", nil
	}
	base, ok := new(big.Rat).SetString(strings.TrimSpace(price))
	if !ok {
		return "", ErrExchangeRateUnavailable
	}
	conversion, ok := new(big.Rat).SetString(strings.TrimSpace(rate))
	if !ok || conversion.Sign() <= 0 {
		return "", ErrExchangeRateUnavailable
	}
	converted := new(big.Rat).Quo(base, conversion).FloatString(8)
	return strings.TrimRight(strings.TrimRight(converted, "0"), "."), nil
}
