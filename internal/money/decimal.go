// Package money contains exact decimal primitives used at domain boundaries.
package money

import (
	"errors"
	"math/big"
	"strconv"
	"strings"
)

var ErrInvalidDecimal = errors.New("invalid decimal")

var (
	ErrDivisionByZero = errors.New("decimal division by zero")
	ErrInvalidScale   = errors.New("invalid decimal scale")
)

// RoundingMode describes how a Decimal is reduced to a smaller scale.
// Payroll deliberately exposes only the legally required HALF_UP policy.
type RoundingMode uint8

const HalfUp RoundingMode = iota

// Decimal is an exact base-10 value. It deliberately has no float conversion.
type Decimal struct {
	text  string
	scale int
	value big.Int
}

func ParseDecimal(input string, maxScale int) (Decimal, error) {
	input = strings.TrimSpace(input)
	if input == "" || maxScale < 0 || strings.ContainsAny(input, "eE") {
		return Decimal{}, ErrInvalidDecimal
	}
	negative := strings.HasPrefix(input, "-")
	unsigned := strings.TrimPrefix(strings.TrimPrefix(input, "+"), "-")
	parts := strings.Split(unsigned, ".")
	if len(parts) > 2 || parts[0] == "" || (len(parts) == 2 && parts[1] == "") || (len(parts) == 2 && len(parts[1]) > maxScale) {
		return Decimal{}, ErrInvalidDecimal
	}
	for _, part := range parts {
		for _, digit := range part {
			if digit < '0' || digit > '9' {
				return Decimal{}, ErrInvalidDecimal
			}
		}
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	digits := strings.TrimLeft(parts[0]+fraction, "0")
	if digits == "" {
		digits = "0"
		negative = false
	}
	var value big.Int
	if _, ok := value.SetString(digits, 10); !ok {
		return Decimal{}, ErrInvalidDecimal
	}
	if negative {
		value.Neg(&value)
	}
	integer := strings.TrimLeft(parts[0], "0")
	if integer == "" {
		integer = "0"
	}
	text := integer
	if fraction != "" {
		text += "." + fraction
	}
	if negative {
		text = "-" + text
	}
	return Decimal{text: text, scale: len(fraction), value: value}, nil
}

func (d Decimal) String() string {
	if d.text == "" {
		return "0"
	}
	return d.text
}
func (d Decimal) Sign() int  { return d.value.Sign() }
func (d Decimal) Scale() int { return d.scale }

func (d Decimal) Equal(other Decimal) bool {
	left := new(big.Int).Set(&d.value)
	right := new(big.Int).Set(&other.value)
	if d.scale < other.scale {
		left.Mul(left, pow10(other.scale-d.scale))
	} else if other.scale < d.scale {
		right.Mul(right, pow10(d.scale-other.scale))
	}
	return left.Cmp(right) == 0
}

// Cmp compares exact values without converting them to binary floating point.
func (d Decimal) Cmp(other Decimal) int {
	left, right := aligned(d, other)
	return left.Cmp(right)
}

func (d Decimal) Add(other Decimal) Decimal {
	left, right := aligned(d, other)
	left.Add(left, right)
	return fromScaledInt(left, max(d.scale, other.scale))
}

func (d Decimal) Sub(other Decimal) Decimal {
	left, right := aligned(d, other)
	left.Sub(left, right)
	return fromScaledInt(left, max(d.scale, other.scale))
}

func (d Decimal) Mul(other Decimal) Decimal {
	value := new(big.Int).Mul(new(big.Int).Set(&d.value), new(big.Int).Set(&other.value))
	return fromScaledInt(value, d.scale+other.scale)
}

// Div returns d/other at scale decimal places using HALF_UP rounding.
func (d Decimal) Div(other Decimal, scale int) (Decimal, error) {
	if scale < 0 {
		return Decimal{}, ErrInvalidScale
	}
	if other.Sign() == 0 {
		return Decimal{}, ErrDivisionByZero
	}
	numerator := new(big.Int).Set(&d.value)
	denominator := new(big.Int).Set(&other.value)
	exponent := scale + other.scale - d.scale
	if exponent >= 0 {
		numerator.Mul(numerator, pow10(exponent))
	} else {
		denominator.Mul(denominator, pow10(-exponent))
	}
	return fromScaledInt(divHalfUp(numerator, denominator), scale), nil
}

// Quantize returns the same value at the requested scale. Reducing scale uses
// HALF_UP; increasing scale is exact. Keeping the mode argument makes the
// rounding decision explicit at every legal calculation boundary.
func (d Decimal) Quantize(scale int, mode RoundingMode) (Decimal, error) {
	if scale < 0 || mode != HalfUp {
		return Decimal{}, ErrInvalidScale
	}
	if scale >= d.scale {
		value := new(big.Int).Set(&d.value)
		value.Mul(value, pow10(scale-d.scale))
		return fromScaledInt(value, scale), nil
	}
	return fromScaledInt(divHalfUp(new(big.Int).Set(&d.value), pow10(d.scale-scale)), scale), nil
}

func aligned(left, right Decimal) (*big.Int, *big.Int) {
	l := new(big.Int).Set(&left.value)
	r := new(big.Int).Set(&right.value)
	if left.scale < right.scale {
		l.Mul(l, pow10(right.scale-left.scale))
	} else if right.scale < left.scale {
		r.Mul(r, pow10(left.scale-right.scale))
	}
	return l, r
}

func divHalfUp(numerator, denominator *big.Int) *big.Int {
	negative := numerator.Sign()*denominator.Sign() < 0
	n := new(big.Int).Abs(new(big.Int).Set(numerator))
	d := new(big.Int).Abs(new(big.Int).Set(denominator))
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(n, d, remainder)
	if remainder.Mul(remainder, big.NewInt(2)).Cmp(d) >= 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	if negative {
		quotient.Neg(quotient)
	}
	return quotient
}

func fromScaledInt(value *big.Int, scale int) Decimal {
	if value == nil || value.Sign() == 0 {
		return Decimal{text: formatScaled(big.NewInt(0), scale), scale: scale}
	}
	copyValue := new(big.Int).Set(value)
	return Decimal{text: formatScaled(copyValue, scale), scale: scale, value: *copyValue}
}

func formatScaled(value *big.Int, scale int) string {
	negative := value.Sign() < 0
	digits := new(big.Int).Abs(new(big.Int).Set(value)).String()
	if scale > 0 {
		if len(digits) <= scale {
			digits = strings.Repeat("0", scale-len(digits)+1) + digits
		}
		digits = digits[:len(digits)-scale] + "." + digits[len(digits)-scale:]
	}
	if negative {
		return "-" + digits
	}
	return digits
}

func max(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func pow10(power int) *big.Int {
	if power < 0 {
		panic("negative power of ten: " + strconv.Itoa(power))
	}
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(power)), nil)
}
