package config

import (
	"strings"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	values := map[string]string{"VARYAONE_DATABASE_URL": "postgres://user:secret@db:5432/varyaone", "VARYAONE_MASTER_KEY": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="}
	cfg, err := Load(func(key string) string { return values[key] })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != ":8080" || cfg.Environment != "development" || cfg.LogLevel != "info" || cfg.SecureCookies {
		t.Fatalf("unexpected defaults: %#v", cfg)
	}
}

func TestLoadSecureCookiesCanBeOverriddenForPlainHTTPDesktop(t *testing.T) {
	values := map[string]string{
		"VARYAONE_ENV":            "production",
		"VARYAONE_SECURE_COOKIES": "false",
		"VARYAONE_DATABASE_URL":   "postgres://user:secret@db:5432/varyaone",
		"VARYAONE_MASTER_KEY":     "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
	}
	cfg, err := Load(func(key string) string { return values[key] })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SecureCookies {
		t.Fatal("desktop HTTP override must disable Secure cookies")
	}
	values["VARYAONE_SECURE_COOKIES"] = "sometimes"
	if _, err := Load(func(key string) string { return values[key] }); err == nil {
		t.Fatal("invalid secure-cookie setting must be rejected")
	}
}

func TestCookiesSecureDefaultsSafelyForDirectProductionConfig(t *testing.T) {
	if !(Config{Environment: "production"}).CookiesSecure() {
		t.Fatal("direct production config must default to secure cookies")
	}
}

func TestLoadNeverLeaksDatabaseURLInValidationErrors(t *testing.T) {
	secret := "top-secret-password"
	_, err := Load(func(key string) string {
		if key == "VARYAONE_DATABASE_URL" {
			return "postgres://user:" + secret + "@"
		}
		if key == "VARYAONE_MASTER_KEY" {
			return "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
		}
		return ""
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatal("validation error leaked database credentials")
	}
}

func TestServingDatabaseURLFallsBackToOwnerConnection(t *testing.T) {
	base := map[string]string{
		"VARYAONE_DATABASE_URL": "postgres://owner:secret@db:5432/varyaone",
		"VARYAONE_MASTER_KEY":   "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
	}
	cfg, err := Load(func(key string) string { return base[key] })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ServingDatabaseURL() != cfg.DatabaseURL {
		t.Fatalf("expected serving URL to fall back to owner URL, got %q", cfg.ServingDatabaseURL())
	}

	base["VARYAONE_APP_DATABASE_URL"] = "postgres://varyaone_app:secret@db:5432/varyaone"
	cfg, err = Load(func(key string) string { return base[key] })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ServingDatabaseURL() != base["VARYAONE_APP_DATABASE_URL"] {
		t.Fatalf("expected serving URL to use the app connection, got %q", cfg.ServingDatabaseURL())
	}
}

func TestLoadRejectsMalformedAppDatabaseURLWithoutLeaking(t *testing.T) {
	secret := "app-role-password"
	_, err := Load(func(key string) string {
		switch key {
		case "VARYAONE_DATABASE_URL":
			return "postgres://owner:secret@db:5432/varyaone"
		case "VARYAONE_APP_DATABASE_URL":
			return "postgres://varyaone_app:" + secret + "@"
		case "VARYAONE_MASTER_KEY":
			return "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
		}
		return ""
	})
	if err == nil {
		t.Fatal("expected malformed VARYAONE_APP_DATABASE_URL to be rejected")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatal("validation error leaked app database credentials")
	}
}

func TestLoadRequiresA32ByteMasterKey(t *testing.T) {
	_, err := Load(func(key string) string {
		if key == "VARYAONE_DATABASE_URL" {
			return "postgres://user:secret@db:5432/varyaone"
		}
		if key == "VARYAONE_MASTER_KEY" {
			return "dG9vLXNob3J0"
		}
		return ""
	})
	if err == nil {
		t.Fatal("expected invalid master key to be rejected")
	}
}

const requiredBase = "VARYAONE_DATABASE_URL"

func baseValues() map[string]string {
	return map[string]string{
		requiredBase:          "postgres://user:secret@db:5432/varyaone",
		"VARYAONE_MASTER_KEY": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
	}
}

func TestLoadUpdateCatalogURLsUnsetByDefault(t *testing.T) {
	values := baseValues()
	cfg, err := Load(func(key string) string { return values[key] })
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.UpdateCatalogURLs) != 0 || cfg.UpdateConfigured() {
		t.Fatalf("expected no default catalog URLs, got %#v", cfg.UpdateCatalogURLs)
	}
	if cfg.UpdateArtifactPrefix != "" {
		t.Fatalf("expected no default artifact prefix, got %q", cfg.UpdateArtifactPrefix)
	}
}

func TestLoadUpdateCatalogURLsCommaSeparated(t *testing.T) {
	values := baseValues()
	values["VARYAONE_UPDATE_CATALOG_URL"] = "https://a.example/latest.json, https://b.example/latest.json"
	cfg, err := Load(func(key string) string { return values[key] })
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"https://a.example/latest.json", "https://b.example/latest.json"}
	if len(cfg.UpdateCatalogURLs) != 2 || cfg.UpdateCatalogURLs[0] != want[0] || cfg.UpdateCatalogURLs[1] != want[1] {
		t.Fatalf("UpdateCatalogURLs = %#v, want %#v", cfg.UpdateCatalogURLs, want)
	}
	if !cfg.UpdateConfigured() {
		t.Fatal("expected UpdateConfigured() true with catalog URLs set")
	}
}

func TestLoadRejectsNonHTTPSUpdateCatalogURL(t *testing.T) {
	values := baseValues()
	values["VARYAONE_UPDATE_CATALOG_URL"] = "http://a.example/latest.json"
	if _, err := Load(func(key string) string { return values[key] }); err == nil {
		t.Fatal("expected non-https catalog URL to be rejected")
	}
}

func TestLoadRejectsNonHTTPSUpdateArtifactPrefix(t *testing.T) {
	values := baseValues()
	values["VARYAONE_UPDATE_ARTIFACT_PREFIX"] = "http://github.com/alpyxn/varyaone/releases/download/"
	if _, err := Load(func(key string) string { return values[key] }); err == nil {
		t.Fatal("expected non-https artifact prefix to be rejected")
	}
}
