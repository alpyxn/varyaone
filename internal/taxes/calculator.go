package taxes

import (
	"errors"
	"math/big"
	"strings"

	"github.com/alpyxn/varyaone/internal/money"
)

// TaxMode determines whether the line price already contains tax.
type TaxMode string

const (
	TaxInclusive     TaxMode = "INCLUSIVE"
	TaxExclusive     TaxMode = "EXCLUSIVE"
	TaxModeInclusive         = TaxInclusive
	TaxModeExclusive         = TaxExclusive
)

type RoundPolicy string

const (
	RoundHalfUp RoundPolicy = "HALF_UP"
)

type DiscountKind string

const (
	DiscountPercent DiscountKind = "PERCENT"
	DiscountFixed   DiscountKind = "FIXED"
)

// TaxCalculationType is deliberately independent from any e-Document model.
type TaxCalculationType string

const (
	TaxPercentage    TaxCalculationType = "PERCENTAGE"
	TaxQuantityBased TaxCalculationType = "QUANTITY_BASED"
	// TaxFixedAmount is a flat per-line amount (not per unit). Product tax
	// profile components accept it, so the engine has to price it.
	TaxFixedAmount TaxCalculationType = "FIXED_AMOUNT"
	Percentage                        = TaxPercentage
	QuantityBased                     = TaxQuantityBased
	FixedAmount                       = TaxFixedAmount
)

type TaxDiscount struct {
	Kind   DiscountKind `json:"kind"`
	Amount string       `json:"amount"`
}

// Discount is kept as a short provider-neutral name for callers in this package.
type Discount = TaxDiscount

// TaxComponent describes one tax or withholding calculation without carrying
// provider-specific identifiers. Rate is a percentage for PERCENTAGE and a
// per-unit amount for QUANTITY_BASED.
type TaxComponent struct {
	Code string `json:"code,omitempty"`
	// Name is display-only; it travels with the component so a document line
	// can label its taxes without another catalog lookup.
	Name            string             `json:"name,omitempty"`
	CalculationType TaxCalculationType `json:"calculation_type"`
	Rate            string             `json:"rate"`
	// IncludedInBase marks a tax that belongs to the base of the components
	// that are not themselves included: Turkish ÖTV sits in the KDV base, so
	// KDV is charged on (net + ÖTV) while ÖTV is charged on net alone.
	IncludedInBase bool `json:"included_in_base,omitempty"`
	// Primary marks the line's VAT component, the one whose rate is the
	// document line's tax_rate.
	Primary                bool `json:"primary,omitempty"`
	Withholding            bool `json:"withholding,omitempty"`
	WithholdingNumerator   *int `json:"withholding_numerator,omitempty"`
	WithholdingDenominator *int `json:"withholding_denominator,omitempty"`
	Exempt                 bool `json:"exempt,omitempty"`
}

type TaxCalculationLine struct {
	UnitPrice string      `json:"unit_price"`
	Quantity  string      `json:"quantity"`
	Discount  TaxDiscount `json:"discount"`
	// Discounts is the cascading line discount chain: every component applies
	// to the remainder left by the previous one, which is how Logo/WOLVOX
	// style tiered discounts behave. When it is empty the single Discount
	// field is used, so existing callers keep their behaviour.
	Discounts     []TaxDiscount  `json:"discounts,omitempty"`
	Components    []TaxComponent `json:"components"`
	TaxComponents []TaxComponent `json:"tax_components,omitempty"`
}

type TaxCalculationInput struct {
	Lines      []TaxCalculationLine `json:"lines"`
	Components []TaxComponent       `json:"components,omitempty"`
	// HeaderDiscounts is the document level discount chain. It is resolved
	// against the sum of the line bases and then distributed back onto the
	// lines proportionally, so tax is always computed on the discounted base
	// and the distributed shares add up to the header discount exactly.
	HeaderDiscounts []TaxDiscount `json:"header_discounts,omitempty"`
	TaxMode         TaxMode       `json:"tax_mode"`
	RoundScale      int           `json:"round_scale"`
	RoundPolicy     RoundPolicy   `json:"round_policy"`
}

type TaxCalculationComponentResult struct {
	Code              string             `json:"code,omitempty"`
	Name              string             `json:"name,omitempty"`
	Primary           bool               `json:"primary,omitempty"`
	IncludedInBase    bool               `json:"included_in_base,omitempty"`
	CalculationType   TaxCalculationType `json:"calculation_type"`
	Rate              string             `json:"rate"`
	BaseAmount        string             `json:"base_amount"`
	Amount            string             `json:"amount"`
	Withholding       bool               `json:"withholding"`
	WithholdingAmount string             `json:"withholding_amount"`
	Exempt            bool               `json:"exempt"`
}

type TaxCalculationLineResult struct {
	GrossAmount string `json:"gross_amount"`
	// DiscountAmount is everything taken off this line: the line discount
	// chain plus the share of the document discount allocated to it.
	DiscountAmount string `json:"discount_amount"`
	// LineDiscountAmount and HeaderDiscountAmount keep that split visible so a
	// document can persist where each part of the discount came from.
	LineDiscountAmount   string                          `json:"line_discount_amount"`
	HeaderDiscountAmount string                          `json:"header_discount_amount"`
	TaxableAmount        string                          `json:"taxable_amount"`
	TaxAmount            string                          `json:"tax_amount"`
	WithholdingAmount    string                          `json:"withholding_amount"`
	TotalAmount          string                          `json:"total_amount"`
	PayableAmount        string                          `json:"payable_amount"`
	Components           []TaxCalculationComponentResult `json:"components"`
}

// TaxCalculationResult is a value-oriented snapshot. All decimal values are
// strings so callers cannot accidentally reintroduce binary floating point.
type TaxCalculationResult struct {
	Lines                []TaxCalculationLineResult      `json:"lines"`
	Components           []TaxCalculationComponentResult `json:"components"`
	GrossAmount          string                          `json:"gross_amount"`
	DiscountAmount       string                          `json:"discount_amount"`
	LineDiscountAmount   string                          `json:"line_discount_amount"`
	HeaderDiscountAmount string                          `json:"header_discount_amount"`
	TaxableAmount        string                          `json:"taxable_amount"`
	NetAmount            string                          `json:"net_amount"`
	TaxAmount            string                          `json:"tax_amount"`
	TotalTaxAmount       string                          `json:"total_tax_amount"`
	WithholdingAmount    string                          `json:"withholding_amount"`
	TotalAmount          string                          `json:"total_amount"`
	PayableAmount        string                          `json:"payable_amount"`
}

var (
	ErrInvalidTaxCalculation  = errors.New("invalid tax calculation")
	ErrDiscountExceedsTaxBase = errors.New("discount exceeds tax base")
)

const maxDecimalScale = 18

// Calculate performs all intermediate arithmetic as exact rational arithmetic.
// Components on a line use the same discounted taxable base; quantity-based
// components use the line quantity. In inclusive mode the net base is solved
// after subtracting quantity-based tax from the tax-included amount.
func Calculate(input TaxCalculationInput) (TaxCalculationResult, error) {
	if input.RoundScale < 0 || input.RoundScale > maxDecimalScale {
		return TaxCalculationResult{}, ErrInvalidTaxCalculation
	}
	if input.TaxMode == "" {
		input.TaxMode = TaxExclusive
	}
	if input.RoundPolicy == "" {
		input.RoundPolicy = RoundHalfUp
	}
	if input.TaxMode != TaxInclusive && input.TaxMode != TaxExclusive || input.RoundPolicy != RoundHalfUp || len(input.Lines) == 0 {
		return TaxCalculationResult{}, ErrInvalidTaxCalculation
	}

	// Pass one resolves every line base (gross minus the line discount chain)
	// so the document discount has a total to work against. Pass two computes
	// tax on the base that survives both discounts.
	bases := make([]lineBase, 0, len(input.Lines))
	for _, line := range input.Lines {
		if len(line.Components) == 0 && len(line.TaxComponents) > 0 {
			line.Components = line.TaxComponents
		}
		if len(line.Components) == 0 && len(input.Components) > 0 {
			line.Components = input.Components
		}
		base, err := resolveLineBase(line)
		if err != nil {
			return TaxCalculationResult{}, err
		}
		bases = append(bases, base)
	}

	shares, headerDiscount, err := distributeHeaderDiscount(bases, input.HeaderDiscounts, input.RoundScale)
	if err != nil {
		return TaxCalculationResult{}, err
	}

	result := TaxCalculationResult{Lines: make([]TaxCalculationLineResult, 0, len(input.Lines))}
	for index, base := range bases {
		lineResult, err := calculateLineFrom(base, shares[index], input.TaxMode, input.RoundScale)
		if err != nil {
			return TaxCalculationResult{}, err
		}
		result.Lines = append(result.Lines, lineResult)
		result.GrossAmount = addFormatted(result.GrossAmount, lineResult.GrossAmount, input.RoundScale)
		result.DiscountAmount = addFormatted(result.DiscountAmount, lineResult.DiscountAmount, input.RoundScale)
		result.LineDiscountAmount = addFormatted(result.LineDiscountAmount, lineResult.LineDiscountAmount, input.RoundScale)
		result.TaxableAmount = addFormatted(result.TaxableAmount, lineResult.TaxableAmount, input.RoundScale)
		result.TaxAmount = addFormatted(result.TaxAmount, lineResult.TaxAmount, input.RoundScale)
		result.WithholdingAmount = addFormatted(result.WithholdingAmount, lineResult.WithholdingAmount, input.RoundScale)
		result.TotalAmount = addFormatted(result.TotalAmount, lineResult.TotalAmount, input.RoundScale)
		result.Components = append(result.Components, lineResult.Components...)
	}
	// The distributed shares are reconciled to the header discount exactly, so
	// the document total is the resolved header discount, never a re-sum.
	result.HeaderDiscountAmount = formatRounded(headerDiscount, input.RoundScale)
	result.NetAmount = result.TaxableAmount
	result.TotalTaxAmount = result.TaxAmount
	result.PayableAmount = subtractFormatted(result.TotalAmount, result.WithholdingAmount, input.RoundScale)
	return result, nil
}

// lineBase is the part of a line that must be known before the document
// discount can be resolved and shared out.
type lineBase struct {
	line     TaxCalculationLine
	quantity *big.Rat
	gross    *big.Rat
	discount *big.Rat
	base     *big.Rat
}

func resolveLineBase(line TaxCalculationLine) (lineBase, error) {
	unitPrice, err := parseDecimal(line.UnitPrice, false)
	if err != nil || unitPrice.Sign() < 0 {
		return lineBase{}, ErrInvalidTaxCalculation
	}
	quantity, err := parseDecimal(line.Quantity, false)
	if err != nil || quantity.Sign() <= 0 {
		return lineBase{}, ErrInvalidTaxCalculation
	}
	gross := new(big.Rat).Mul(unitPrice, quantity)
	discounts := line.Discounts
	if len(discounts) == 0 {
		discounts = []TaxDiscount{line.Discount}
	}
	discount, err := cascadingDiscount(gross, discounts)
	if err != nil {
		return lineBase{}, err
	}
	return lineBase{
		line:     line,
		quantity: quantity,
		gross:    gross,
		discount: discount,
		base:     new(big.Rat).Sub(gross, discount),
	}, nil
}

// cascadingDiscount applies each discount to what the previous one left, which
// is what a tiered "%10 + %5" discount means commercially. It never lets the
// chain take more than the amount it started from.
func cascadingDiscount(gross *big.Rat, discounts []TaxDiscount) (*big.Rat, error) {
	total := new(big.Rat)
	remaining := new(big.Rat).Set(gross)
	for _, discount := range discounts {
		amount, err := calculateDiscount(remaining, discount)
		if err != nil {
			return nil, err
		}
		if amount.Cmp(remaining) > 0 {
			return nil, ErrDiscountExceedsTaxBase
		}
		total.Add(total, amount)
		remaining.Sub(remaining, amount)
	}
	return total, nil
}

// distributeHeaderDiscount resolves the document discount against the summed
// line bases and hands each line a share proportional to its base. Shares are
// truncated at the rounding scale and the leftover units are handed out one at
// a time, largest fractional remainder first, so the shares always add up to
// the resolved header discount to the last unit.
func distributeHeaderDiscount(bases []lineBase, discounts []TaxDiscount, scale int) ([]*big.Rat, *big.Rat, error) {
	shares := make([]*big.Rat, len(bases))
	for index := range shares {
		shares[index] = new(big.Rat)
	}
	if len(discounts) == 0 {
		return shares, new(big.Rat), nil
	}
	total := new(big.Rat)
	for _, base := range bases {
		total.Add(total, base.base)
	}
	headerDiscount, err := cascadingDiscount(total, discounts)
	if err != nil {
		return nil, nil, err
	}
	if headerDiscount.Sign() == 0 {
		return shares, headerDiscount, nil
	}
	if total.Sign() <= 0 {
		// There is nothing to take a discount off.
		return nil, nil, ErrDiscountExceedsTaxBase
	}

	unit := new(big.Rat).SetFrac(big.NewInt(1), new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(scale)), nil))
	target := roundRat(headerDiscount, scale)
	assigned := new(big.Rat)
	remainders := make([]*big.Rat, len(bases))
	for index, base := range bases {
		exact := new(big.Rat).Quo(new(big.Rat).Mul(target, base.base), total)
		truncated := truncateRat(exact, scale)
		shares[index] = truncated
		remainders[index] = new(big.Rat).Sub(exact, truncated)
		assigned.Add(assigned, truncated)
	}
	leftover := new(big.Rat).Sub(target, assigned)
	for leftover.Sign() > 0 {
		best := -1
		for index := range bases {
			if best == -1 || remainders[index].Cmp(remainders[best]) > 0 {
				best = index
			}
		}
		if best == -1 {
			break
		}
		shares[best].Add(shares[best], unit)
		remainders[best] = new(big.Rat)
		leftover.Sub(leftover, unit)
	}
	return shares, target, nil
}

func truncateRat(value *big.Rat, scale int) *big.Rat {
	factor := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(scale)), nil)
	numerator := new(big.Int).Mul(value.Num(), factor)
	quotient := new(big.Int).Quo(numerator, value.Denom())
	return new(big.Rat).SetFrac(quotient, factor)
}

func calculateLineFrom(resolved lineBase, headerShare *big.Rat, mode TaxMode, scale int) (TaxCalculationLineResult, error) {
	line := resolved.line
	quantity := resolved.quantity
	gross := resolved.gross
	if headerShare == nil {
		headerShare = new(big.Rat)
	}
	base := new(big.Rat).Sub(resolved.base, headerShare)
	if base.Sign() < 0 {
		return TaxCalculationLineResult{}, ErrDiscountExceedsTaxBase
	}
	discount := new(big.Rat).Add(resolved.discount, headerShare)

	components := make([]validatedComponent, 0, len(line.Components))
	// Components split into two layers: the ones flagged as part of the tax
	// base (ÖTV and friends) are charged on the net amount and then join the
	// base of every other component, which is how KDV is charged on top of
	// ÖTV.
	innerRate, outerRate := new(big.Rat), new(big.Rat)
	innerFixed, outerFixed := new(big.Rat), new(big.Rat)
	for _, component := range line.Components {
		validated, err := validateComponent(component)
		if err != nil {
			return TaxCalculationLineResult{}, err
		}
		components = append(components, validated)
		if validated.exempt {
			continue
		}
		rate, fixed := outerRate, outerFixed
		if validated.includedInBase {
			rate, fixed = innerRate, innerFixed
		}
		switch validated.kind {
		case TaxPercentage:
			rate.Add(rate, validated.rate)
		case TaxQuantityBased:
			fixed.Add(fixed, new(big.Rat).Mul(quantity, validated.rate))
		default:
			fixed.Add(fixed, validated.rate)
		}
	}

	hundred := big.NewRat(100, 1)
	innerMultiplier := new(big.Rat).Add(big.NewRat(1, 1), new(big.Rat).Quo(innerRate, hundred))
	outerMultiplier := new(big.Rat).Add(big.NewRat(1, 1), new(big.Rat).Quo(outerRate, hundred))
	net := new(big.Rat).Set(base)
	if mode == TaxInclusive {
		// base = net*innerMultiplier*outerMultiplier + innerFixed*outerMultiplier
		//        + outerFixed, solved for net.
		remaining := new(big.Rat).Sub(base, new(big.Rat).Mul(innerFixed, outerMultiplier))
		remaining.Sub(remaining, outerFixed)
		if remaining.Sign() < 0 {
			return TaxCalculationLineResult{}, ErrInvalidTaxCalculation
		}
		net.Quo(remaining, new(big.Rat).Mul(innerMultiplier, outerMultiplier))
	}
	// The base every component that is not itself part of the base is charged
	// on: the net amount plus the taxes that belong to it.
	outerBase := new(big.Rat).Add(new(big.Rat).Mul(net, innerMultiplier), innerFixed)

	lineResult := TaxCalculationLineResult{
		GrossAmount:          formatRounded(gross, scale),
		DiscountAmount:       formatRounded(discount, scale),
		LineDiscountAmount:   formatRounded(resolved.discount, scale),
		HeaderDiscountAmount: formatRounded(headerShare, scale),
		Components:           make([]TaxCalculationComponentResult, 0, len(components)),
	}
	lineResult.TaxableAmount = formatRounded(net, scale)
	lineResult.TotalAmount = formatRounded(base, scale)

	taxAmount := new(big.Rat)
	withholdingAmount := new(big.Rat)
	for _, component := range components {
		amount := new(big.Rat)
		componentBase := net
		if !component.includedInBase {
			componentBase = outerBase
		}
		switch {
		case component.exempt:
			amount.SetInt64(0)
		case component.kind == TaxQuantityBased:
			componentBase = quantity
			amount.Mul(quantity, component.rate)
		case component.kind == TaxFixedAmount:
			amount.Set(component.rate)
		default:
			amount.Mul(componentBase, component.rate)
			amount.Quo(amount, hundred)
		}
		componentWithholding, err := withholdingAmountFor(amount, component)
		if err != nil {
			return TaxCalculationLineResult{}, err
		}
		taxAmount.Add(taxAmount, amount)
		withholdingAmount.Add(withholdingAmount, componentWithholding)
		lineResult.Components = append(lineResult.Components, TaxCalculationComponentResult{
			Code:              component.code,
			Name:              component.name,
			Primary:           component.primary,
			IncludedInBase:    component.includedInBase,
			CalculationType:   component.kind,
			Rate:              component.rateText,
			BaseAmount:        formatRounded(componentBase, scale),
			Amount:            formatRounded(amount, scale),
			Withholding:       component.withholding,
			WithholdingAmount: formatRounded(componentWithholding, scale),
			Exempt:            component.exempt,
		})
	}

	lineResult.TaxAmount = formatRounded(taxAmount, scale)
	lineResult.WithholdingAmount = formatRounded(withholdingAmount, scale)
	if mode == TaxExclusive {
		lineResult.TotalAmount = addFormatted(lineResult.TaxableAmount, lineResult.TaxAmount, scale)
	}
	lineResult.PayableAmount = subtractFormatted(lineResult.TotalAmount, lineResult.WithholdingAmount, scale)
	if mode == TaxInclusive {
		// The included total is authoritative; reconciliation is explicit.
		lineResult.TaxableAmount = subtractFormatted(lineResult.TotalAmount, lineResult.TaxAmount, scale)
	}
	return lineResult, nil
}

type validatedComponent struct {
	code, name, rateText string
	kind                 TaxCalculationType
	rate                 *big.Rat
	withholding          bool
	numerator            *int
	denominator          *int
	exempt               bool
	includedInBase       bool
	primary              bool
}

func validateComponent(component TaxComponent) (validatedComponent, error) {
	kind := component.CalculationType
	if kind == "" {
		kind = TaxPercentage
	}
	rate, err := parseDecimal(component.Rate, false)
	if err != nil || rate.Sign() < 0 {
		return validatedComponent{}, ErrInvalidTaxCalculation
	}
	if kind == TaxPercentage && rate.Cmp(big.NewRat(100, 1)) > 0 {
		return validatedComponent{}, ErrInvalidTaxCalculation
	}
	if kind != TaxPercentage && kind != TaxQuantityBased && kind != TaxFixedAmount {
		return validatedComponent{}, ErrInvalidTaxCalculation
	}
	if (component.WithholdingNumerator == nil) != (component.WithholdingDenominator == nil) {
		return validatedComponent{}, ErrInvalidTaxCalculation
	}
	if component.WithholdingNumerator != nil {
		if *component.WithholdingNumerator <= 0 || *component.WithholdingDenominator <= 0 || *component.WithholdingNumerator > *component.WithholdingDenominator {
			return validatedComponent{}, ErrInvalidTaxCalculation
		}
	}
	return validatedComponent{
		code: component.Code, name: component.Name, rateText: canonicalDecimal(rate), kind: kind, rate: rate,
		withholding: component.Withholding, numerator: component.WithholdingNumerator,
		denominator: component.WithholdingDenominator, exempt: component.Exempt,
		includedInBase: component.IncludedInBase, primary: component.Primary,
	}, nil
}

func withholdingAmountFor(amount *big.Rat, component validatedComponent) (*big.Rat, error) {
	if !component.withholding || amount.Sign() == 0 {
		return new(big.Rat), nil
	}
	if component.numerator == nil {
		return new(big.Rat).Set(amount), nil
	}
	ratio := new(big.Rat).SetFrac64(int64(*component.numerator), int64(*component.denominator))
	return new(big.Rat).Mul(amount, ratio), nil
}

func calculateDiscount(gross *big.Rat, discount TaxDiscount) (*big.Rat, error) {
	if strings.TrimSpace(discount.Amount) == "" || strings.TrimSpace(discount.Amount) == "0" {
		return new(big.Rat), nil
	}
	amount, err := parseDecimal(discount.Amount, false)
	if err != nil || amount.Sign() < 0 {
		return nil, ErrInvalidTaxCalculation
	}
	switch discount.Kind {
	case DiscountPercent:
		if amount.Cmp(big.NewRat(100, 1)) > 0 {
			return nil, ErrInvalidTaxCalculation
		}
		return new(big.Rat).Mul(gross, new(big.Rat).Quo(amount, big.NewRat(100, 1))), nil
	case DiscountFixed:
		if amount.Cmp(gross) > 0 {
			return nil, ErrDiscountExceedsTaxBase
		}
		return amount, nil
	default:
		return nil, ErrInvalidTaxCalculation
	}
}

func parseDecimal(value string, allowEmpty bool) (*big.Rat, error) {
	raw := strings.TrimSpace(value)
	if raw == "" {
		if allowEmpty {
			return new(big.Rat), nil
		}
		return nil, ErrInvalidTaxCalculation
	}
	parsed, err := money.ParseDecimal(raw, maxDecimalScale)
	if err != nil {
		return nil, ErrInvalidTaxCalculation
	}
	ratio, ok := new(big.Rat).SetString(parsed.String())
	if !ok {
		return nil, ErrInvalidTaxCalculation
	}
	return ratio, nil
}

func formatRounded(value *big.Rat, scale int) string {
	return formatExact(roundRat(value, scale), scale)
}

func roundRat(value *big.Rat, scale int) *big.Rat {
	negative := value.Sign() < 0
	abs := new(big.Rat).Abs(value)
	factor := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(scale)), nil)
	numerator := new(big.Int).Mul(abs.Num(), factor)
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(numerator, abs.Denom(), remainder)
	if new(big.Int).Mul(remainder, big.NewInt(2)).Cmp(abs.Denom()) >= 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	if negative {
		quotient.Neg(quotient)
	}
	return new(big.Rat).SetFrac(quotient, factor)
}

func formatExact(value *big.Rat, scale int) string {
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
	fraction := new(big.Int).Quo(new(big.Int).Mul(remainder, factor), abs.Denom()).String()
	for len(fraction) < scale {
		fraction = "0" + fraction
	}
	fraction = strings.TrimRight(fraction, "0")
	result := integer.String()
	if fraction != "" {
		result += "." + fraction
	}
	if negative && result != "0" {
		result = "-" + result
	}
	return result
}

func canonicalDecimal(value *big.Rat) string {
	return formatExact(value, decimalScale(value))
}

func decimalScale(value *big.Rat) int {
	denominator := new(big.Int).Set(value.Denom())
	scale := 0
	for denominator.Cmp(big.NewInt(1)) != 0 && scale <= maxDecimalScale {
		if new(big.Int).Mod(denominator, big.NewInt(2)).Sign() == 0 {
			denominator.Quo(denominator, big.NewInt(2))
		} else if new(big.Int).Mod(denominator, big.NewInt(5)).Sign() == 0 {
			denominator.Quo(denominator, big.NewInt(5))
		} else {
			return maxDecimalScale
		}
		scale++
	}
	return scale
}

func addFormatted(left, right string, scale int) string {
	leftRat, _ := parseDecimal(left, true)
	rightRat, _ := parseDecimal(right, true)
	return formatRounded(new(big.Rat).Add(leftRat, rightRat), scale)
}

func subtractFormatted(left, right string, scale int) string {
	leftRat, _ := parseDecimal(left, true)
	rightRat, _ := parseDecimal(right, true)
	return formatRounded(new(big.Rat).Sub(leftRat, rightRat), scale)
}
