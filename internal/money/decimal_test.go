package money

import "testing"

func TestParseDecimalNeverRoundsThroughFloat(t *testing.T) {
	value, err := ParseDecimal("9007199254740993.1234", 4)
	if err != nil || value.String() != "9007199254740993.1234" {
		t.Fatalf("exact decimal lost precision: value=%q err=%v", value.String(), err)
	}
	if _, err := ParseDecimal("1.00001", 4); err == nil {
		t.Fatal("accepted a value beyond the configured scale")
	}
	if _, err := ParseDecimal("1e3", 4); err == nil {
		t.Fatal("accepted exponential notation")
	}
	whole, _ := ParseDecimal("0", 4)
	scaled, _ := ParseDecimal("0.0000", 4)
	if !whole.Equal(scaled) {
		t.Fatal("equal decimals with different scales did not compare equal")
	}
}

func TestDecimalArithmeticAndHalfUpRounding(t *testing.T) {
	left, _ := ParseDecimal("10.125", 3)
	right, _ := ParseDecimal("2.50", 2)

	if got := left.Add(right).String(); got != "12.625" {
		t.Fatalf("add = %s", got)
	}
	if got := left.Sub(right).String(); got != "7.625" {
		t.Fatalf("sub = %s", got)
	}
	if got := left.Mul(right).String(); got != "25.31250" {
		t.Fatalf("mul = %s", got)
	}
	rounded, err := left.Quantize(2, HalfUp)
	if err != nil || rounded.String() != "10.13" {
		t.Fatalf("quantize = %s, %v", rounded.String(), err)
	}
	negative, _ := ParseDecimal("-1.005", 3)
	negative, err = negative.Quantize(2, HalfUp)
	if err != nil || negative.String() != "-1.01" {
		t.Fatalf("negative quantize = %s, %v", negative.String(), err)
	}
}

func TestDecimalDivisionIsExactUntilExplicitScale(t *testing.T) {
	one, _ := ParseDecimal("1", 0)
	three, _ := ParseDecimal("3", 0)
	result, err := one.Div(three, 4)
	if err != nil || result.String() != "0.3333" {
		t.Fatalf("division = %s, %v", result.String(), err)
	}
	zero, _ := ParseDecimal("0", 0)
	if _, err := one.Div(zero, 2); err != ErrDivisionByZero {
		t.Fatalf("division by zero error = %v", err)
	}
}
