package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

type Config struct {
	Environment string
	HTTPAddr    string
	// SecureCookies controls the Secure attribute independently from the
	// environment name. Reverse-proxied production deployments use HTTPS and
	// keep this enabled; the Windows desktop server intentionally serves plain
	// HTTP on the local network and disables it explicitly.
	SecureCookies           bool
	secureCookiesConfigured bool
	// DatabaseURL is the owner/superuser connection used for migrations and
	// backups.
	DatabaseURL string
	// AppDatabaseURL, when set, is the connection the server and worker use for
	// serving traffic. Point it at the non-superuser varyaone_app role so the
	// row-level-security company isolation policies are enforced. Empty falls
	// back to DatabaseURL (unchanged behaviour).
	AppDatabaseURL   string
	LogLevel         string
	Release          string
	ShutdownTimeout  time.Duration
	MasterKey        []byte
	StorageProvider  string
	StorageRoot      string
	StorageEndpoint  string
	StorageBucket    string
	StorageRegion    string
	StorageAccessKey string
	StorageSecretKey string
	StoragePathStyle bool
	PostgresBinDir   string
	PulseEndpoint    string
	PulseIngestKey   string
	// PulseInstallPing sends the one-off anonymous install-count ping whenever a
	// collector is configured. It is opt-out (default true). It is the only
	// thing the collector is told about this instance besides user-submitted
	// feedback: there is no usage telemetry.
	PulseInstallPing bool
	// UpdateAgentToken authenticates the host-side systemd update agent against
	// the /internal/update/* endpoints. Empty disables those endpoints
	// entirely (the UI-facing /api/v1/system/update routes stay available).
	UpdateAgentToken string
	// UpdateCatalogURLs are the release-catalog documents (latest.json) the
	// updater polls, tried in order until one parses. They are plain public
	// https objects — no key, no rate limit — so update checks are independent
	// of pulse. Empty disables update checking entirely; like PulseEndpoint,
	// there is no built-in default here — the desktop supervisor and
	// compose.yaml supply one, same as they do for PulseEndpoint.
	UpdateCatalogURLs []string
	// UpdateArtifactPrefix pins where a release artifact may be downloaded
	// from. A catalog entry whose artifact URL does not start with this prefix
	// has its artifact fields dropped, so a tampered catalog cannot point the
	// updater at a fork or a third-party host. Empty disables the check (any
	// https URL accepted) — only meaningful together with a non-default
	// UpdateCatalogURLs pointed at a fork/self-host.
	UpdateArtifactPrefix string
	// DemoMode turns this installation into the public showcase deployment: the
	// demo endpoints are mounted, the demo company may be seeded and purged, and
	// the outward-facing operations are refused. It defaults to false and must
	// never be enabled on an installation holding real data.
	DemoMode bool
	// DemoEmail and DemoPassword are the credentials of the single shared demo
	// user. They are not secrets - anyone may sign in to the demo - but they are
	// configurable so a deployment can pick its own.
	DemoEmail    string
	DemoPassword string
	// DemoResetInterval is how often the demo company is purged and reseeded.
	// Everyone shares one company, so this is the main defence against a visitor
	// leaving the data in a mess for the next one. Zero disables the timer and
	// leaves resetting to the CLI.
	DemoResetInterval time.Duration
}

// DemoConfigured reports whether this process is running as the demo
// deployment.
func (c Config) DemoConfigured() bool { return c.DemoMode }

// UpdateConfigured reports whether a release catalog is available, i.e. whether
// update checks can run.
func (c Config) UpdateConfigured() bool { return len(c.UpdateCatalogURLs) > 0 }

// PulseConfigured reports whether a collector endpoint + ingest key are set,
// i.e. whether the install ping and user feedback can reach anything.
func (c Config) PulseConfigured() bool {
	return c.PulseEndpoint != "" && c.PulseIngestKey != ""
}

// ServingDatabaseURL is the connection the server/worker should use to serve
// requests: the dedicated application role when configured, otherwise the owner
// connection.
func (c Config) ServingDatabaseURL() string {
	if c.AppDatabaseURL != "" {
		return c.AppDatabaseURL
	}
	return c.DatabaseURL
}

// CookiesSecure preserves the production-safe default for tests and internal
// callers that construct Config directly, while allowing Load to carry an
// explicit desktop HTTP override.
func (c Config) CookiesSecure() bool {
	if c.secureCookiesConfigured {
		return c.SecureCookies
	}
	return c.Environment != "development"
}

type Getenv func(string) string

func Load(getenv Getenv) (Config, error) {
	environment := valueOr(getenv("VARYAONE_ENV"), "development")
	secureCookies := environment != "development"
	if raw := strings.TrimSpace(getenv("VARYAONE_SECURE_COOKIES")); raw != "" {
		switch strings.ToLower(raw) {
		case "true", "1", "yes":
			secureCookies = true
		case "false", "0", "no":
			secureCookies = false
		default:
			return Config{}, errors.New("VARYAONE_SECURE_COOKIES must be true or false")
		}
	}
	cfg := Config{
		Environment:             environment,
		HTTPAddr:                valueOr(getenv("VARYAONE_HTTP_ADDR"), ":8080"),
		SecureCookies:           secureCookies,
		secureCookiesConfigured: true,
		DatabaseURL:             strings.TrimSpace(getenv("VARYAONE_DATABASE_URL")),
		AppDatabaseURL:          strings.TrimSpace(getenv("VARYAONE_APP_DATABASE_URL")),
		LogLevel:                valueOr(getenv("VARYAONE_LOG_LEVEL"), "info"),
		Release:                 valueOr(getenv("VARYAONE_RELEASE"), "dev"),
		ShutdownTimeout:         15 * time.Second,
		StorageProvider:         valueOr(getenv("VARYAONE_STORAGE_PROVIDER"), "local"),
		StorageRoot:             valueOr(getenv("VARYAONE_STORAGE_ROOT"), "/var/lib/varyaone/storage"),
		StorageEndpoint:         strings.TrimSpace(getenv("VARYAONE_STORAGE_ENDPOINT")),
		StorageBucket:           strings.TrimSpace(getenv("VARYAONE_STORAGE_BUCKET")),
		StorageRegion:           valueOr(getenv("VARYAONE_STORAGE_REGION"), "us-east-1"),
		StorageAccessKey:        strings.TrimSpace(getenv("VARYAONE_STORAGE_ACCESS_KEY")),
		StorageSecretKey:        strings.TrimSpace(getenv("VARYAONE_STORAGE_SECRET_KEY")),
		StoragePathStyle:        strings.EqualFold(strings.TrimSpace(getenv("VARYAONE_STORAGE_PATH_STYLE")), "true"),
		PostgresBinDir:          strings.TrimSpace(getenv("VARYAONE_POSTGRES_BIN")),
		PulseEndpoint:           strings.TrimRight(strings.TrimSpace(getenv("VARYAONE_PULSE_ENDPOINT")), "/"),
		PulseIngestKey:          strings.TrimSpace(getenv("VARYAONE_PULSE_INGEST_KEY")),
		PulseInstallPing:        !strings.EqualFold(strings.TrimSpace(getenv("VARYAONE_PULSE_INSTALL_PING")), "false"),
		UpdateAgentToken:        strings.TrimSpace(getenv("VARYAONE_UPDATE_AGENT_TOKEN")),
		UpdateCatalogURLs:       splitList(getenv("VARYAONE_UPDATE_CATALOG_URL")),
		UpdateArtifactPrefix:    strings.TrimSpace(getenv("VARYAONE_UPDATE_ARTIFACT_PREFIX")),
		DemoMode:                strings.EqualFold(strings.TrimSpace(getenv("VARYAONE_DEMO_MODE")), "true"),
		DemoEmail:               valueOr(getenv("VARYAONE_DEMO_EMAIL"), "demo@varyaone.com"),
		DemoPassword:            valueOr(getenv("VARYAONE_DEMO_PASSWORD"), "varyaone-demo-2026"),
		DemoResetInterval:       2 * time.Hour,
	}
	if raw := strings.TrimSpace(getenv("VARYAONE_DEMO_RESET_INTERVAL")); raw != "" {
		interval, parseErr := time.ParseDuration(raw)
		if parseErr != nil || interval < 0 {
			return Config{}, errors.New("VARYAONE_DEMO_RESET_INTERVAL must be a duration such as 2h (0 disables)")
		}
		cfg.DemoResetInterval = interval
	}
	masterKeyValue := strings.TrimSpace(getenv("VARYAONE_MASTER_KEY"))
	if masterKeyValue == "" {
		return Config{}, errors.New("VARYAONE_MASTER_KEY is required")
	}
	masterKey, err := base64.StdEncoding.DecodeString(masterKeyValue)
	if err != nil || len(masterKey) != 32 {
		return Config{}, errors.New("VARYAONE_MASTER_KEY must be a base64-encoded 32-byte key")
	}
	cfg.MasterKey = masterKey
	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("VARYAONE_DATABASE_URL is required")
	}
	parsed, err := url.Parse(cfg.DatabaseURL)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.Host == "" {
		return Config{}, errors.New("VARYAONE_DATABASE_URL must be a valid PostgreSQL URL")
	}
	if cfg.AppDatabaseURL != "" {
		appParsed, appErr := url.Parse(cfg.AppDatabaseURL)
		if appErr != nil || (appParsed.Scheme != "postgres" && appParsed.Scheme != "postgresql") || appParsed.Host == "" {
			return Config{}, errors.New("VARYAONE_APP_DATABASE_URL must be a valid PostgreSQL URL")
		}
	}
	switch cfg.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return Config{}, fmt.Errorf("VARYAONE_LOG_LEVEL must be one of debug, info, warn, error")
	}
	if cfg.PulseEndpoint != "" || cfg.PulseIngestKey != "" {
		if cfg.PulseEndpoint == "" || cfg.PulseIngestKey == "" {
			return Config{}, errors.New("VARYAONE_PULSE_ENDPOINT and VARYAONE_PULSE_INGEST_KEY must be set together")
		}
		pulseURL, perr := url.Parse(cfg.PulseEndpoint)
		if perr != nil || pulseURL.Scheme != "https" || pulseURL.Host == "" {
			return Config{}, errors.New("VARYAONE_PULSE_ENDPOINT must be a valid https URL")
		}
	}
	for _, raw := range cfg.UpdateCatalogURLs {
		catalogURL, cerr := url.Parse(raw)
		if cerr != nil || catalogURL.Scheme != "https" || catalogURL.Host == "" {
			return Config{}, fmt.Errorf("VARYAONE_UPDATE_CATALOG_URL entry %q must be a valid https URL", raw)
		}
	}
	if cfg.UpdateArtifactPrefix != "" {
		prefixURL, perr := url.Parse(cfg.UpdateArtifactPrefix)
		if perr != nil || prefixURL.Scheme != "https" || prefixURL.Host == "" {
			return Config{}, errors.New("VARYAONE_UPDATE_ARTIFACT_PREFIX must be a valid https URL")
		}
	}
	return cfg, nil
}

// splitList parses a comma-separated env value into a trimmed, non-empty slice.
func splitList(value string) []string {
	var out []string
	for _, part := range strings.Split(value, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func valueOr(value, fallback string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return fallback
}
