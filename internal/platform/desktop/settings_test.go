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
