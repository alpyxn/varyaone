package pulse

import (
	"os"
	"strings"
	"testing"
)

// TestPulseSourceReferencesNoIdentifyingColumns is the privacy guard: this
// package may only ever read the two opaque keys it needs (install id, setup
// timestamp). If someone reintroduces a query touching an identifying column,
// this fails.
func TestPulseSourceReferencesNoIdentifyingColumns(t *testing.T) {
	code := stripComments(readSource(t))
	for _, banned := range []string{
		"legal_name", "trade_name", "display_name", "tax_number", "tax_office",
		"first_name", "last_name", "work_email", "document_no", "email",
	} {
		if strings.Contains(code, banned) {
			t.Errorf("pulse.go code references identifying column %q", banned)
		}
	}
}

// TestNoUsageTelemetry pins the decision that this package collects no usage
// statistics. Reintroducing a metrics snapshot means reopening the privacy
// review, not quietly adding a query here.
func TestNoUsageTelemetry(t *testing.T) {
	code := stripComments(readSource(t))
	for _, banned := range []string{
		"count(*)", "metricQueries", "Snapshot", "documentKinds", "/pulse/v1/report",
	} {
		if strings.Contains(code, banned) {
			t.Errorf("pulse.go reintroduces usage telemetry (%q)", banned)
		}
	}
}

func stripComments(src string) string {
	var b strings.Builder
	for _, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

func readSource(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("pulse.go")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
