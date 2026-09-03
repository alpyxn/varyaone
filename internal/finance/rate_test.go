package finance

import "testing"

func TestNormalizeRateTextKeepsValueAndDropsStorageNoise(t *testing.T) {
	cases := []struct{ in, want string }{
		// exchange_rates.rate_to_base is numeric(38,18): a raw read looks like this.
		{"48.294300000000000000", "48.2943"},
		{"1.000000000000000000", "1"},
		{"55.893400000000000000", "55.8934"},
		// More precision than a rate column can hold is rounded, not refused.
		{"0.123456789123456789", "0.12345679"},
		{" 65.2762 ", "65.2762"},
		// Not a usable rate: returned untouched so the caller still rejects it.
		{"abc", "abc"},
		{"0", "0"},
		{"-3", "-3"},
		{"", ""},
	}
	for _, c := range cases {
		if got := NormalizeRateText(c.in); got != c.want {
			t.Errorf("NormalizeRateText(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// A rate straight out of storage must survive the payment/movement parser --
// this is the exact value that used to fail with "kur geçersiz".
func TestParseRateAcceptsStoredRatePrecision(t *testing.T) {
	rate, err := parseRate("48.294300000000000000")
	if err != nil {
		t.Fatalf("parseRate rejected a stored rate: %v", err)
	}
	if got := rate.FloatString(4); got != "48.2943" {
		t.Fatalf("parsed rate = %s, want 48.2943", got)
	}
	if _, err := parseRateDefault(""); err != nil {
		t.Fatalf("an omitted rate must default to 1: %v", err)
	}
	for _, invalid := range []string{"0", "-1", "abc", "1e5"} {
		if _, err := parseRate(invalid); err == nil {
			t.Fatalf("parseRate(%q) was accepted", invalid)
		}
	}
}
