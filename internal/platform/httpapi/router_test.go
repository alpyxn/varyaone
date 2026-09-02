package httpapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/pricing"
	"github.com/alpyxn/varyaone/internal/products"
	"github.com/alpyxn/varyaone/internal/taxes"
)

type readinessFunc func(context.Context) error

func (f readinessFunc) Check(ctx context.Context) error { return f(ctx) }

func TestLivenessDoesNotDependOnDatabase(t *testing.T) {
	router := testRouter(readinessFunc(func(context.Context) error { return errors.New("database unavailable") }))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health/live", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", response.Code)
	}
}

func TestReadinessUsesStableErrorContract(t *testing.T) {
	router := testRouter(readinessFunc(func(context.Context) error { return errors.New("database unavailable") }))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", response.Code)
	}
	if !strings.Contains(response.Body.String(), `"code":"NOT_READY"`) || !strings.Contains(response.Body.String(), `"trace_id":`) {
		t.Fatalf("unexpected error body: %s", response.Body.String())
	}
	if response.Header().Get("X-Request-ID") == "" {
		t.Fatal("expected request ID response header")
	}
}

func TestReady(t *testing.T) {
	router := testRouter(readinessFunc(func(context.Context) error { return nil }))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
}

func TestIfMatchParserAcceptsStrongAndWeakEntityTags(t *testing.T) {
	for _, value := range []string{`"7"`, `W/"7"`, "7"} {
		version, err := parseIfMatch(value)
		if err != nil || version != 7 {
			t.Fatalf("parseIfMatch(%q) = %d, %v", value, version, err)
		}
	}
	if _, err := parseIfMatch(""); err == nil {
		t.Fatal("empty If-Match was accepted")
	}
}

func TestSameOriginRejectsCrossSiteBrowserRequest(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "https://erp.example/api/v1/auth/login", nil)
	request.Host = "erp.example"
	request.Header.Set("Origin", "https://attacker.example")
	if sameOrigin(request) {
		t.Fatal("cross-site origin was accepted")
	}
	request.Header.Set("Origin", "https://erp.example")
	if !sameOrigin(request) {
		t.Fatal("same origin was rejected")
	}
}

func TestRouterMountsCatalogPricingAndTaxModules(t *testing.T) {
	// Route registration must be safe even before the first authenticated request.
	// The services are not called by this test, so a zero-value identity service
	// and nil pools are sufficient to exercise the Chi mount topology.
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("router registration panicked: %v", recovered)
		}
	}()
	_ = NewRouter(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		"test",
		readinessFunc(func(context.Context) error { return nil }),
		WithIdentity(&identity.Service{}, false),
		WithProducts(products.NewService(nil)),
		WithPricing(pricing.NewService(nil)),
		WithTaxes(taxes.NewService(nil)),
	)
}

func testRouter(readiness Readiness) http.Handler {
	return NewRouter(slog.New(slog.NewTextHandler(io.Discard, nil)), "test", readiness)
}
