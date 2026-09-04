package desktop

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSettingsEnv(t *testing.T) {
	dir := t.TempDir()
	l := Layout{Home: dir}

	if got := settingsEnv(l); len(got) != 0 {
		t.Fatalf("missing file: want empty, got %v", got)
	}

	body := "" +
		"# a comment\n" +
		"\n" +
		"VARYAONE_PULSE_ENDPOINT=https://example.test\n" +
		`VARYAONE_PULSE_INGEST_KEY = "abc123"` + "\n" +
		"MALFORMED_LINE\n"
	if err := os.WriteFile(filepath.Join(dir, "settings.env"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	got := settingsEnv(l)
	if got["VARYAONE_PULSE_ENDPOINT"] != "https://example.test" {
		t.Errorf("endpoint = %q", got["VARYAONE_PULSE_ENDPOINT"])
	}
	if got["VARYAONE_PULSE_INGEST_KEY"] != "abc123" {
		t.Errorf("key = %q (quotes/space not trimmed)", got["VARYAONE_PULSE_INGEST_KEY"])
	}
	if _, ok := got["MALFORMED_LINE"]; ok {
		t.Errorf("malformed line should be skipped")
	}
}

// TestSelfUpdateIsOffByDefault pins the shipped Windows behaviour: the desktop
// build must not carry a release catalog of its own.
//
// A baked-in default here is invisible from compose.yaml, so the deployment
// that turned self-update off everywhere else would still have had the desktop
// polling, applying and offering releases. If someone puts an address back in
// this constant, updates silently return for every Windows installation.
func TestSelfUpdateIsOffByDefault(t *testing.T) {
	if defaultUpdateCatalogURLs != "" {
		t.Fatalf("desktop ships a release catalog by default (%q); self-update must stay opt-in",
			defaultUpdateCatalogURLs)
	}
}
