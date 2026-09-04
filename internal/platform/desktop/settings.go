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

	// defaultUpdateCatalogURLs is empty: self-update is opt-in on the desktop
	// exactly as it is on a compose install. With no catalog configured the
	// update service is never constructed, the applier never runs, the
	// endpoints are not mounted and no Güncelleme page appears.
	//
	// Whoever turns it back on sets VARYAONE_UPDATE_CATALOG_URL in the
	// installation's settings file or environment to:
	//   https://github.com/alpyxn/varyaone/releases/latest/download/latest.json,
	//   https://raw.githubusercontent.com/alpyxn/varyaone/main/release/latest.json
	// (the release asset, with a raw.githubusercontent.com copy as fallback for
	// networks where the release CDN is unreachable).
	defaultUpdateCatalogURLs = ""
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
