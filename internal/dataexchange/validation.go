package dataexchange

import (
	"fmt"
	"math/big"
	"strings"
)

// UnlimitedPrecision disables the scale limit in QuantityRule.
const UnlimitedPrecision = -1

// Quantity is an exact decimal quantity. It never uses floating-point math.
type Quantity struct {
	value *big.Rat
	scale int
}

// ParseQuantity accepts a signed decimal with either a dot or a comma as the
// decimal separator. Thousands separators and exponent notation are rejected
// so an adapter must make locale conversion explicit before calling the core.
func ParseQuantity(raw string) (Quantity, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return Quantity{}, fmt.Errorf("miktar boş")
	}

	sign := 1
	switch value[0] {
	case '+':
		value = value[1:]
	case '-':
		sign = -1
		value = value[1:]
	}
	if value == "" || strings.Contains(value, "e") || strings.Contains(value, "E") {
		return Quantity{}, fmt.Errorf("miktar yalnızca ondalık sayı olmalıdır")
	}
	if strings.Contains(value, ".") && strings.Contains(value, ",") {
		return Quantity{}, fmt.Errorf("miktarda birden fazla ondalık ayırıcı var")
	}

	separator := strings.IndexAny(value, ".,")
	scale := 0
	digits := value
	if separator >= 0 {
		scale = len(value) - separator - 1
		digits = value[:separator] + value[separator+1:]
	}
	if digits == "" {
		return Quantity{}, fmt.Errorf("miktarda rakam bulunamadı")
	}
	for _, character := range digits {
		if character < '0' || character > '9' {
			return Quantity{}, fmt.Errorf("miktar ondalık olmayan karakter içeriyor")
		}
	}

	numerator := new(big.Int)
	if _, ok := numerator.SetString(digits, 10); !ok {
		return Quantity{}, fmt.Errorf("miktar geçerli bir ondalık değil")
	}
	denominator := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(scale)), nil)
	rat := new(big.Rat).SetFrac(numerator, denominator)
	if sign < 0 {
		rat.Neg(rat)
	}
	return Quantity{value: rat, scale: scale}, nil
}

// Scale returns the number of fractional digits supplied by the source value.
func (q Quantity) Scale() int { return q.scale }

// IsNegative reports whether the exact quantity is less than zero.
func (q Quantity) IsNegative() bool { return q.value != nil && q.value.Sign() < 0 }

// IsZero reports whether the exact quantity equals zero.
func (q Quantity) IsZero() bool { return q.value == nil || q.value.Sign() == 0 }

// String formats the exact value without losing the source scale.
func (q Quantity) String() string {
	if q.value == nil {
		return ""
	}
	return q.value.FloatString(q.scale)
}

// QuantityRule validates one canonical field without converting through float64.
type QuantityRule struct {
	Field         string
	Required      bool
	AllowNegative bool
	MaxScale      int
}

// ValidateQuantity validates one row using a QuantityRule.
func ValidateQuantity(row MappedRow, rule QuantityRule) []Issue {
	if strings.TrimSpace(rule.Field) == "" {
		return []Issue{{RowNumber: row.RowNumber, Code: "invalid_quantity_rule", Severity: SeverityError, Message: "miktar alanı boş"}}
	}
	raw, exists := row.Values[rule.Field]
	if !exists || strings.TrimSpace(raw) == "" {
		if rule.Required {
			return []Issue{{RowNumber: row.RowNumber, Field: rule.Field, Code: "quantity_required", Severity: SeverityError, Message: "miktar zorunludur"}}
		}
		return nil
	}

	quantity, err := ParseQuantity(raw)
	if err != nil {
		return []Issue{{RowNumber: row.RowNumber, Field: rule.Field, Code: "invalid_quantity", Severity: SeverityError, Message: "miktar geçerli bir ondalık olmalıdır"}}
	}
	if !rule.AllowNegative && quantity.IsNegative() {
		return []Issue{{RowNumber: row.RowNumber, Field: rule.Field, Code: "negative_quantity", Severity: SeverityError, Message: "miktar negatif olamaz"}}
	}
	if rule.MaxScale != UnlimitedPrecision && rule.MaxScale < 0 {
		return []Issue{{RowNumber: row.RowNumber, Field: rule.Field, Code: "invalid_quantity_rule", Severity: SeverityError, Message: "miktar hassasiyet sınırı geçersiz"}}
	}
	if rule.MaxScale >= 0 && quantity.Scale() > rule.MaxScale {
		return []Issue{{RowNumber: row.RowNumber, Field: rule.Field, Code: "quantity_precision", Severity: SeverityError, Message: "miktar izin verilen ondalık basamak sayısını aşıyor"}}
	}
	return nil
}

// Validate runs a QuantityRule over all rows.
func (r QuantityRule) Validate(rows []MappedRow) []Issue {
	var issues []Issue
	for _, row := range rows {
		issues = append(issues, ValidateQuantity(row, r)...)
	}
	return issues
}

// DuplicateRule rejects repeated non-empty combinations of canonical fields.
// It is useful for preventing duplicate stock/count lines before an adapter
// creates append-only records.
type DuplicateRule struct {
	Fields []string
}

// ValidateDuplicateRows is the function form of DuplicateRule.
func ValidateDuplicateRows(rows []MappedRow, fields ...string) []Issue {
	return DuplicateRule{Fields: fields}.Validate(rows)
}

func (r DuplicateRule) Validate(rows []MappedRow) []Issue {
	if len(r.Fields) == 0 {
		return []Issue{{Code: "invalid_duplicate_rule", Severity: SeverityError, Message: "tekrar kontrolü için en az bir alan gereklidir"}}
	}

	seen := make(map[string]int, len(rows))
	var issues []Issue
	for _, row := range rows {
		parts := make([]string, len(r.Fields))
		empty := true
		complete := true
		for index, field := range r.Fields {
			value, exists := row.Values[field]
			if !exists {
				complete = false
				break
			}
			parts[index] = strings.TrimSpace(value)
			if strings.TrimSpace(value) != "" {
				empty = false
			}
		}
		if !complete || empty {
			continue
		}
		key := duplicateKey(parts)
		if firstRow, exists := seen[key]; exists {
			issues = append(issues, Issue{
				RowNumber: row.RowNumber,
				Field:     strings.Join(r.Fields, ","),
				Code:      "duplicate_row",
				Severity:  SeverityError,
				Message:   fmt.Sprintf("satır, %d numaralı kaynak satırın tekrarıdır", firstRow),
			})
			continue
		}
		seen[key] = row.RowNumber
	}
	return issues
}

func duplicateKey(parts []string) string {
	var builder strings.Builder
	for _, part := range parts {
		builder.WriteString(fmt.Sprintf("%d:", len(part)))
		builder.WriteString(part)
	}
	return builder.String()
}
