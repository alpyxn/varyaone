package pulse

import (
	"encoding/json"
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

func TestInstallPayloadCarriesOnlyOpaqueFields(t *testing.T) {
	payload := map[string]any{
		"install_id":  "11111111-1111-4111-8111-111111111111",
		"app_version": "1.2.3",
		"setup_at":    "2026-09-01T10:00:00Z",
	}
	blob, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var generic map[string]any
	if err := json.Unmarshal(blob, &generic); err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"install_id": true, "app_version": true, "setup_at": true}
	for k := range generic {
		if !allowed[k] {
			t.Errorf("unexpected field %q in install payload", k)
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
