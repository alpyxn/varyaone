package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestComposeShipsSelfUpdateOff pins the compose default alongside the desktop
// one (internal/platform/desktop.TestSelfUpdateIsOffByDefault).
//
// Self-update stays opt-in until the operator asks for it by name. It has to be
// pinned in both places because the two installation paths read different
// files: emptying compose.yaml once left every Windows build still updating,
// and the discrepancy was invisible from either file alone.
func TestComposeShipsSelfUpdateOff(t *testing.T) {
	path := filepath.Join("..", "..", "..", "compose.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("compose.yaml not reachable from here: %v", err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") || !strings.Contains(trimmed, "VARYAONE_UPDATE_CATALOG_URL:") {
			continue
		}
		if !strings.Contains(trimmed, "${VARYAONE_UPDATE_CATALOG_URL:-}") {
			t.Fatalf("compose.yaml ships a release catalog by default: %q\n"+
				"self-update must stay opt-in; the default has to be ${VARYAONE_UPDATE_CATALOG_URL:-}", trimmed)
		}
		return
	}
	t.Fatal("VARYAONE_UPDATE_CATALOG_URL not found in compose.yaml")
}
