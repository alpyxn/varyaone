package inventory

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/alpyxn/varyaone/internal/money"
)

// ReconcileSnapshot calculates the expected quantity at posting time and the
// immutable adjustment required to reach the physical count.  Decimal values
// are handled as rationals so a count never loses a fraction through float
// conversion.
func ReconcileSnapshot(snapshotQuantity string, movementsSinceSnapshot []string, countedQuantity string) (expected, difference string, err error) {
	snapshot, err := decimalRat(snapshotQuantity)
	if err != nil {
		return "", "", err
	}
	if snapshot.Sign() < 0 {
		return "", "", fmt.Errorf("snapshot quantity cannot be negative")
	}
	expectedRat := new(big.Rat).Set(snapshot)
	for _, movement := range movementsSinceSnapshot {
		delta, parseErr := decimalRat(movement)
		if parseErr != nil {
			return "", "", parseErr
		}
		expectedRat.Add(expectedRat, delta)
	}
	counted, err := decimalRat(countedQuantity)
	if err != nil {
		return "", "", err
	}
	if counted.Sign() < 0 {
		return "", "", fmt.Errorf("counted quantity cannot be negative")
	}
	differenceRat := new(big.Rat).Sub(counted, expectedRat)
	return formatRat(expectedRat), formatRat(differenceRat), nil
}

func decimalRat(input string) (*big.Rat, error) {
	parsed, err := money.ParseDecimal(input, 8)
	if err != nil {
		return nil, fmt.Errorf("invalid quantity %q: %w", input, err)
	}
	rat, ok := new(big.Rat).SetString(parsed.String())
	if !ok {
		return nil, fmt.Errorf("invalid quantity %q", input)
	}
	return rat, nil
}

func formatRat(value *big.Rat) string {
	if value == nil {
		return "0"
	}
	text := value.FloatString(8)
	text = strings.TrimRight(strings.TrimRight(text, "0"), ".")
	if text == "-0" || text == "" {
		return "0"
	}
	return text
}
