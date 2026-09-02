package imports

import (
	"testing"

	"github.com/alpyxn/varyaone/internal/dataexchange"
)

func TestExportFilenameStemUsesTurkishASCIISlugs(t *testing.T) {
	tests := map[string]string{
		"PRODUCT":       "urunler",
		"VARIANT":       "varyantlar",
		"BARCODE":       "barkodlar",
		"WAREHOUSE":     "depolar",
		"PARTY":         "cariler",
		"PRICE_LIST":    "fiyat-listeleri",
		"OPENING_STOCK": "acilis-stoku",
		"STOCK_COUNT":   "stok-sayimi",
	}
	for entity, want := range tests {
		if got := exportFilenameStem(entity); got != want {
			t.Errorf("exportFilenameStem(%q) = %q, want %q", entity, got, want)
		}
	}
}

func TestErrorReportLabelsUseTurkishDisplayValues(t *testing.T) {
	if got := localizedIssueField("PRODUCT", "product_code,is_active"); got != "Stok Kodu, Aktif" {
		t.Fatalf("localizedIssueField() = %q", got)
	}
	if got := localizedIssueField("PARTY", "kind,roles"); got != "Cari Türü, Roller" {
		t.Fatalf("localizedIssueField() = %q", got)
	}
	if got := localizedIssueSeverity(dataexchange.SeverityError); got != "Hata" {
		t.Fatalf("localizedIssueSeverity(ERROR) = %q", got)
	}
	if got := localizedIssueSeverity(dataexchange.SeverityWarning); got != "Uyarı" {
		t.Fatalf("localizedIssueSeverity(WARNING) = %q", got)
	}
}
