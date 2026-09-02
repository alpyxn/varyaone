package pricing

import (
	"errors"
	"math/big"
	"strings"

	"github.com/alpyxn/varyaone/internal/money"
)

// TaxMode and RoundPolicy are price-list card values persisted in
// price_lists.tax_mode / round_policy. Line arithmetic itself lives in
// internal/taxes, which is the single commercial calculation engine.
type TaxMode string

const (
	TaxInclusive TaxMode = "INCLUSIVE"
	TaxExclusive TaxMode = "EXCLUSIVE"
)

type RoundPolicy string

const (
	RoundHalfUp   RoundPolicy = "HALF_UP"
	RoundHalfEven RoundPolicy = "HALF_EVEN"
	RoundDown     RoundPolicy = "DOWN"
	RoundUp       RoundPolicy = "UP"
)

var ErrInvalidCalculation = errors.New("invalid pricing calculation")

// parseAmount keeps price-list decimals on exact rational arithmetic; no float
// is introduced at any point.
func parseAmount(value string, nonEmpty bool) (*big.Rat, error) {
	value = strings.TrimSpace(value)
	if !nonEmpty && value == "" {
		value = "0"
	}
	if value == "" {
		return nil, ErrInvalidCalculation
	}
	if _, err := money.ParseDecimal(value, 18); err != nil {
		return nil, ErrInvalidCalculation
	}
	ratio, ok := new(big.Rat).SetString(value)
	if !ok {
		return nil, ErrInvalidCalculation
	}
	return ratio, nil
}

func validTaxMode(value TaxMode) bool {
	return value == TaxInclusive || value == TaxExclusive
}

func validRoundPolicy(value RoundPolicy) bool {
	return value == RoundHalfUp || value == RoundHalfEven || value == RoundDown || value == RoundUp
}

func formatExactScale(value *big.Rat, scale int) string {
	negative := value.Sign() < 0
	abs := new(big.Rat).Abs(value)
	factor := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(scale)), nil)
	integer, remainder := new(big.Int), new(big.Int)
	integer.QuoRem(abs.Num(), abs.Denom(), remainder)
	if scale == 0 {
		if negative && integer.Sign() != 0 {
			return "-" + integer.String()
		}
		return integer.String()
	}
	digits := new(big.Int).Quo(new(big.Int).Mul(remainder, factor), abs.Denom()).String()
	for len(digits) < scale {
		digits = "0" + digits
	}
	digits = strings.TrimRight(digits, "0")
	result := integer.String()
	if digits != "" {
		result += "." + digits
	}
	if negative && result != "0" {
		result = "-" + result
	}
	return result
}
