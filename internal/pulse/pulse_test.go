package pulse

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

// countQueryShape asserts a metric query can only ever return an opaque
// company_id and a row count -- never a name, tax number or other identifier.
var countQueryShape = regexp.MustCompile(
	`^SELECT company_id::text, count\(\*\) FROM [a-z_]+( WHERE is_active)? GROUP BY 1$`,
)

func TestMetricQueriesSelectOnlyCounts(t *testing.T) {
	for metric, query := range metricQueries {
		if !countQueryShape.MatchString(query) {
			t.Errorf("metric %q query is not a plain per-company count: %q", metric, query)
		}
	}
}

func TestDocumentKindsQueryStaysAnonymous(t *testing.T) {
	// The only non-count column selected is dt.kind, which is a fixed enum
	// (QUOTE/ORDER/DELIVERY/INVOICE), not free-form text.
	code := stripComments(readSource(t))
	if !strings.Contains(code, "SELECT d.company_id::text, dt.kind, count(*)") {
		t.Fatal("documentKinds query shape changed; re-audit for identifying columns")
	}
	for _, banned := range []string{"legal_name", "trade_name", "display_name", "tax_number", "tax_office", "first_name", "last_name", "work_email", "document_no"} {
		if strings.Contains(code, banned) {
			t.Errorf("pulse.go code references identifying column %q", banned)
		}
	}
}

func TestReportMarshalsWithoutIdentifyingFields(t *testing.T) {
	report := Report{
		SchemaVersion: schemaVersion,
		InstallID:     "11111111-1111-4111-8111-111111111111",
		CapturedAt:    "2026-09-01T10:00:00Z",
		AppVersion:    "1.2.3",
		PGVersion:     "16.3",
		Companies: []CompanyReport{{
			CompanyID:    "22222222-2222-4222-8222-222222222222",
			BaseCurrency: "TRY",
			Timezone:     "Europe/Istanbul",
			Metrics:      map[string]int64{"parties_total": 12, "sales_invoices": 40},
		}},
	}
	blob, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var generic map[string]any
	if err := json.Unmarshal(blob, &generic); err != nil {
		t.Fatal(err)
	}
	allowedTop := map[string]bool{
		"schema_version": true, "install_id": true, "captured_at": true,
		"app_version": true, "pg_version": true, "companies": true,
	}
	for k := range generic {
		if !allowedTop[k] {
			t.Errorf("unexpected top-level field %q in report payload", k)
		}
	}
	company := generic["companies"].([]any)[0].(map[string]any)
	allowedCompany := map[string]bool{
		"company_id": true, "base_currency": true, "timezone": true, "metrics": true,
	}
	for k := range company {
		if !allowedCompany[k] {
			t.Errorf("unexpected company field %q in report payload", k)
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
