package httpapi

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/party"
	"github.com/alpyxn/varyaone/internal/platform/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPartyAPIContractAndScopedToken(t *testing.T) {
	databaseURL := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := httpPartyTestPool(t, ctx, databaseURL)
	if err := migrations.New(pool).Up(ctx); err != nil {
		t.Fatal(err)
	}
	identityService, err := identity.NewService(pool, bytes.Repeat([]byte{8}, 32))
	if err != nil {
		t.Fatal(err)
	}
	session, err := identityService.Setup(ctx, identity.SetupInput{AdminName: "API Admin", AdminEmail: "api@example.test", Password: "uzun-ve-guvenli-parola", LegalName: "API AŞ", TradeName: "API", EntityType: "LEGAL_ENTITY"}, identity.RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(slog.New(slog.NewTextHandler(io.Discard, nil)), "test", readinessFunc(func(context.Context) error { return nil }), WithIdentity(identityService, false), WithParty(party.NewService(pool)))
	body := `{"kind":"ORGANIZATION","is_customer":true,"is_supplier":false,"display_name":"API Müşteri","legal_name":"API Müşteri AŞ","default_currency":"TRY","risk_policy":"WARN","addresses":[],"contacts":[],"group_ids":[],"tags":[],"custom_fields":{}}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/parties", strings.NewReader(body))
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session.Token})
	request.Header.Set("X-CSRF-Token", session.CSRFToken)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("create returned %d: %s", response.Code, response.Body.String())
	}
	token, err := identityService.CreateAPIToken(ctx, session, "Cari okuma", []string{"party:read"}, nil, identity.RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodGet, "/api/v1/parties", nil)
	request.Header.Set("Authorization", "Bearer "+token.PlainToken)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "API Müşteri") {
		t.Fatalf("scoped read returned %d: %s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodPost, "/api/v1/parties", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+token.PlainToken)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("read-only token mutation returned %d", response.Code)
	}
}

func TestTaxOfficeReferenceAPIRequiresPartyReadAndSupportsFilters(t *testing.T) {
	databaseURL := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := httpPartyTestPool(t, ctx, databaseURL)
	if err := migrations.New(pool).Up(ctx); err != nil {
		t.Fatal(err)
	}
	identityService, err := identity.NewService(pool, bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	session, err := identityService.Setup(ctx, identity.SetupInput{
		AdminName: "Vergi Dairesi API", AdminEmail: "tax-office-api@example.test", Password: "uzun-ve-guvenli-parola",
		LegalName: "Vergi Dairesi API AŞ", TradeName: "Vergi Dairesi API", EntityType: "LEGAL_ENTITY",
	}, identity.RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(slog.New(slog.NewTextHandler(io.Discard, nil)), "test", readinessFunc(func(context.Context) error { return nil }), WithIdentity(identityService, false), WithParty(party.NewService(pool)))

	request := httptest.NewRequest(http.MethodGet, "/api/v1/tax-office-references", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated tax-office reference request returned %d", response.Code)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/tax-office-references?province_id=34&district_name=Kad%C4%B1k%C3%B6y&q=kad%C4%B1k&limit=2000", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session.Token})
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"name":"Kadıköy Vergi Dairesi Müdürlüğü"`) {
		t.Fatalf("filtered tax-office request returned %d: %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/tax-office-references?province_id=6&district_name=Kad%C4%B1k%C3%B6y", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session.Token})
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid tax-office district combination returned %d: %s", response.Code, response.Body.String())
	}

	token, err := identityService.CreateAPIToken(ctx, session, "Vergi dairesi okuma", []string{"party:read"}, nil, identity.RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodGet, "/api/v1/tax-office-references?q=01250", nil)
	request.Header.Set("Authorization", "Bearer "+token.PlainToken)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"code":"01250"`) {
		t.Fatalf("scoped token tax-office request returned %d: %s", response.Code, response.Body.String())
	}
}

func httpPartyTestPool(t *testing.T, ctx context.Context, databaseURL string) *pgxpool.Pool {
	t.Helper()
	base, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("varya_http_party_test_%d", time.Now().UnixNano())
	if _, err = base.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = base.Exec(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
		base.Close()
	})
	return pool
}
