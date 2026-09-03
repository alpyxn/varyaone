package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/alpyxn/varyaone/internal/demo"
	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/platform/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestDemoEndpointsAndGuard covers the public showcase's HTTP surface: the
// passwordless session, the state the banner reads, and the guard that refuses
// the operations a shared demo must not perform.
func TestDemoEndpointsAndGuard(t *testing.T) {
	databaseURL := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err = migrations.New(pool).Up(ctx); err != nil {
		t.Fatal(err)
	}
	masterKey := bytes.Repeat([]byte{8}, 32)
	runner := demo.New(pool, demo.Options{
		MaintenanceDSN: databaseURL,
		MasterKey:      masterKey,
		Email:          "demo@varyaone.test",
		Password:       "varyaone-demo-2026",
		ResetInterval:  2 * time.Hour,
		ResetCooldown:  time.Hour,
	})
	if err = runner.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	identityService, err := identity.NewService(pool, masterKey, identity.WithMaintenanceDSN(databaseURL))
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ready := readinessFunc(func(context.Context) error { return nil })
	demoRouter := NewRouter(logger, "test", ready,
		WithIdentity(identityService, false),
		WithDemo(DemoRuntime{
			CompanyID: demo.CompanyID, UserID: demo.UserID, Runner: runner,
			Email: "demo@varyaone.test", Password: "varyaone-demo-2026",
		}))
	plainRouter := NewRouter(logger, "test", ready, WithIdentity(identityService, false))

	// A visitor reaches the demo with no credentials, and the session lands on
	// the demo company.
	response := httptest.NewRecorder()
	demoRouter.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/demo/session", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("demo session returned %d: %s", response.Code, response.Body.String())
	}
	var session identity.Session
	if err = json.Unmarshal(response.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	if session.CurrentCompanyID != demo.CompanyID {
		t.Fatalf("demo session landed on company %q, want the demo company", session.CurrentCompanyID)
	}
	// The session token is only ever handed out as a cookie, never in the body.
	var sessionToken string
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == sessionCookieName {
			sessionToken = cookie.Value
		}
	}
	if sessionToken == "" {
		t.Fatal("demo session set no session cookie")
	}

	// The banner's state read reports a ready demo with a scheduled reset.
	response = httptest.NewRecorder()
	demoRouter.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/demo/state", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("demo state returned %d: %s", response.Code, response.Body.String())
	}
	var state struct {
		demo.State
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err = json.Unmarshal(response.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if !state.Ready() || state.NextResetAt == nil {
		t.Fatalf("demo state = %+v, want READY with a scheduled reset", state.State)
	}
	// The login screen shows the shared account already filled in, so the state
	// read has to carry it.
	if state.Email != "demo@varyaone.test" || state.Password != "varyaone-demo-2026" {
		t.Fatalf("demo state did not carry the shared account: %+v", state)
	}

	// The cooldown is enforced through the endpoint too: seeding counts as a
	// reset, so an immediate visitor reset is refused rather than wiping a demo
	// somebody else just started using.
	response = httptest.NewRecorder()
	demoRouter.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/demo/reset", nil))
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("immediate demo reset returned %d, want 429: %s", response.Code, response.Body.String())
	}

	// Guarded operations are refused even with a valid demo session.
	guarded := []struct {
		method string
		path   string
	}{
		{http.MethodDelete, "/api/v1/companies/" + demo.CompanyID},
		{http.MethodPost, "/api/v1/companies"},
		{http.MethodPost, "/api/v1/api-tokens"},
		{http.MethodPost, "/api/v1/users"},
		{http.MethodPost, "/api/v1/roles"},
		{http.MethodPost, "/api/v1/system/backups"},
	}
	for _, item := range guarded {
		request := httptest.NewRequest(item.method, item.path, nil)
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionToken})
		request.Header.Set("X-CSRF-Token", session.CSRFToken)
		response = httptest.NewRecorder()
		demoRouter.ServeHTTP(response, request)
		if response.Code != http.StatusForbidden {
			t.Errorf("%s %s returned %d, want 403 in demo mode", item.method, item.path, response.Code)
		}
	}

	// Reading still works: a demo that refuses everything shows nothing.
	request := httptest.NewRequest(http.MethodGet, "/api/v1/session", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionToken})
	response = httptest.NewRecorder()
	demoRouter.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("reading the session in demo mode returned %d: %s", response.Code, response.Body.String())
	}

	// None of this exists on a normal installation: no demo endpoints, and the
	// guarded routes behave exactly as they always did (unauthenticated here).
	for _, path := range []string{"/api/v1/demo/session", "/api/v1/demo/state", "/api/v1/demo/reset"} {
		response = httptest.NewRecorder()
		plainRouter.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, nil))
		if response.Code != http.StatusNotFound {
			t.Errorf("%s exists without demo mode (%d)", path, response.Code)
		}
	}
	response = httptest.NewRecorder()
	plainRouter.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/api-tokens", nil))
	if response.Code != http.StatusUnauthorized {
		t.Errorf("without demo mode /api/v1/api-tokens returned %d, want the usual 401", response.Code)
	}
}
