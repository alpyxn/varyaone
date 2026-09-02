package desktop

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// Default varya-pulse collector — the same shared endpoint and low-sensitivity
// ingest key the Docker deployment defaults to (see compose.yaml). It doubles as
// the release catalog the desktop updater polls. A self-hoster can override both
// in <Home>/settings.env.
const (
	defaultPulseEndpoint  = "https://varya-pulse.varyaone.workers.dev"
	defaultPulseIngestKey = "0290a2e2fa410b7b7d4656496635a36695a84419027c134225de36b3bf67ce56"

	// defaultUpdateCatalogURLs is the release catalog the desktop updater polls:
	// the asset attached to the newest published GitHub release, with a
	// raw.githubusercontent.com copy as a fallback for networks where the
	// release CDN is unreachable. A comma-separated list — see
	// internal/platform/config.Load / internal/update/catalog.go. It is a
	// public document with no key, so it works even with pulse disabled.
	defaultUpdateCatalogURLs = "https://github.com/alpyxn/varyaone/releases/latest/download/latest.json," +
		"https://raw.githubusercontent.com/alpyxn/varyaone/main/release/latest.json"
	// defaultUpdateArtifactPrefix is the only location a stock build will
	// download a Windows update artifact from — a catalog entry pointing
	// anywhere else has its artifact fields dropped before the updater ever
	// sees them (see internal/update/catalog.go toLatestInfo).
	defaultUpdateArtifactPrefix = "https://github.com/alpyxn/varyaone/releases/download/"
)

// settingsEnv reads optional KEY=VALUE overrides from <Home>/settings.env. Lines
// that are blank or start with '#' are ignored. Missing file → empty map.
func settingsEnv(l Layout) map[string]string {
	out := map[string]string{}
	f, err := os.Open(filepath.Join(l.Home, "settings.env"))
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
	}
	return out
}
