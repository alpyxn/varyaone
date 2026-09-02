package exchange

import (
	"encoding/xml"
	"math/big"
	"strings"
	"testing"
)

func TestProviderRateOrientationConvertsBasePrice(t *testing.T) {
	// TCMB-style values: 1 USD = 40 TRY. A 400 TRY base price must become
	// 10 USD, never 16,000 USD or 400 USD.
	try := big.NewRat(1, 1)
	usd := big.NewRat(40, 1)
	rateToBase := new(big.Rat).Quo(usd, try)
	converted := new(big.Rat).Quo(big.NewRat(400, 1), rateToBase)
	if converted.Cmp(big.NewRat(10, 1)) != 0 {
		t.Fatalf("unexpected converted price: %s", converted.RatString())
	}
}

func TestECBRatesInvertToInternalOrientation(t *testing.T) {
	// ECB quotes foreign units per EUR: 1 EUR = 1.25 USD, 1 EUR = 50 TRY,
	// 1 EUR = 0.80 GBP. With a TRY base company a USD line must resolve to
	// 40 TRY per USD and a GBP line to 62.5 TRY per GBP.
	var document ecbDocument
	if err := xml.NewDecoder(strings.NewReader(`<gesmes:Envelope xmlns:gesmes="x"><Cube><Cube time="2026-08-29"><Cube currency="USD" rate="1.25"/><Cube currency="GBP" rate="0.80"/><Cube currency="TRY" rate="50"/></Cube></Cube></gesmes:Envelope>`)).Decode(&document); err != nil {
		t.Fatalf("ECB XML decode: %v", err)
	}
	rates := ecbRates(document)
	base := rates["TRY"]
	for code, want := range map[string]*big.Rat{"USD": big.NewRat(40, 1), "GBP": big.NewRat(625, 10), "EUR": big.NewRat(50, 1)} {
		got := new(big.Rat).Quo(rates[code], base)
		if got.Cmp(want) != 0 {
			t.Fatalf("%s rate_to_base = %s, want %s", code, got.RatString(), want.RatString())
		}
	}
}

func TestParseProviderDate(t *testing.T) {
	for _, value := range []string{"27.08.2026", "2026-08-27"} {
		if _, err := parseProviderDate(value); err != nil {
			t.Fatalf("parseProviderDate(%q): %v", value, err)
		}
	}
}

func TestTCMBDocumentReadsCurrencyCodeAttribute(t *testing.T) {
	var document tcmbDocument
	err := xml.NewDecoder(strings.NewReader(`<Tarih_Date Tarih="28.08.2026"><Currency CurrencyCode="USD"><Unit>1</Unit><ForexSelling>40,0000</ForexSelling></Currency></Tarih_Date>`)).Decode(&document)
	if err != nil {
		t.Fatalf("TCMB XML decode: %v", err)
	}
	if len(document.Items) != 1 || document.Items[0].Code != "USD" {
		t.Fatalf("TCMB currency code = %q, want USD", document.Items[0].Code)
	}
}

func TestDocumentRateUsesDocumentPrecisionWithoutTrailingZeros(t *testing.T) {
	for input, expected := range map[string]string{
		"1":           "1",
		"40.000000":   "40",
		"1.234567891": "1.23456789",
	} {
		actual, err := documentRate(input)
		if err != nil {
			t.Fatalf("documentRate(%q): %v", input, err)
		}
		if actual != expected {
			t.Fatalf("documentRate(%q) = %q, want %q", input, actual, expected)
		}
	}
}
