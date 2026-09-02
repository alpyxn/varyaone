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
	PulseEndpoint    string
	PulseIngestKey   string
	// PulseEnabled turns on the daily anonymous usage summary (per-company
	// counts). It is opt-in.
	PulseEnabled bool
	// PulseInstallPing sends the one-off anonymous install-count ping whenever a
	// collector is configured. It is opt-out (default true) and independent of
	// PulseEnabled.
	PulseInstallPing bool
	// UpdateAgentToken authenticates the host-side systemd update agent against
	// the /internal/update/* endpoints. Empty disables those endpoints
	// entirely (the UI-facing /api/v1/system/update routes stay available).
	UpdateAgentToken string
}

// PulseConfigured reports whether a collector endpoint + ingest key are set, i.e.
// whether pulse (install ping and/or usage summary) can talk to anything.
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

type Getenv func(string) string

func Load(getenv Getenv) (Config, error) {
	cfg := Config{
		Environment:      valueOr(getenv("VARYAONE_ENV"), "development"),
		HTTPAddr:         valueOr(getenv("VARYAONE_HTTP_ADDR"), ":8080"),
		DatabaseURL:      strings.TrimSpace(getenv("VARYAONE_DATABASE_URL")),
		AppDatabaseURL:   strings.TrimSpace(getenv("VARYAONE_APP_DATABASE_URL")),
		LogLevel:         valueOr(getenv("VARYAONE_LOG_LEVEL"), "info"),
		Release:          valueOr(getenv("VARYAONE_RELEASE"), "dev"),
		ShutdownTimeout:  15 * time.Second,
		StorageProvider:  valueOr(getenv("VARYAONE_STORAGE_PROVIDER"), "local"),
		StorageRoot:      valueOr(getenv("VARYAONE_STORAGE_ROOT"), "/var/lib/varyaone/storage"),
		StorageEndpoint:  strings.TrimSpace(getenv("VARYAONE_STORAGE_ENDPOINT")),
		StorageBucket:    strings.TrimSpace(getenv("VARYAONE_STORAGE_BUCKET")),
		StorageRegion:    valueOr(getenv("VARYAONE_STORAGE_REGION"), "us-east-1"),
		StorageAccessKey: strings.TrimSpace(getenv("VARYAONE_STORAGE_ACCESS_KEY")),
		StorageSecretKey: strings.TrimSpace(getenv("VARYAONE_STORAGE_SECRET_KEY")),
		StoragePathStyle: strings.EqualFold(strings.TrimSpace(getenv("VARYAONE_STORAGE_PATH_STYLE")), "true"),
		PulseEndpoint:    strings.TrimRight(strings.TrimSpace(getenv("VARYAONE_PULSE_ENDPOINT")), "/"),
		PulseIngestKey:   strings.TrimSpace(getenv("VARYAONE_PULSE_INGEST_KEY")),
		PulseEnabled:     strings.EqualFold(strings.TrimSpace(getenv("VARYAONE_PULSE_ENABLED")), "true"),
		PulseInstallPing: !strings.EqualFold(strings.TrimSpace(getenv("VARYAONE_PULSE_INSTALL_PING")), "false"),
		UpdateAgentToken: strings.TrimSpace(getenv("VARYAONE_UPDATE_AGENT_TOKEN")),
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
	if cfg.PulseEnabled || cfg.PulseEndpoint != "" || cfg.PulseIngestKey != "" {
		if cfg.PulseEndpoint == "" || cfg.PulseIngestKey == "" {
			return Config{}, errors.New("VARYAONE_PULSE_ENDPOINT and VARYAONE_PULSE_INGEST_KEY must be set together")
		}
		pulseURL, perr := url.Parse(cfg.PulseEndpoint)
		if perr != nil || pulseURL.Scheme != "https" || pulseURL.Host == "" {
			return Config{}, errors.New("VARYAONE_PULSE_ENDPOINT must be a valid https URL")
		}
	}
	return cfg, nil
}

func valueOr(value, fallback string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return fallback
}
