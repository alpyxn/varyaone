package httpapi

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/platform/database"
	"github.com/alpyxn/varyaone/internal/platform/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

func scopeTestPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	pool, err := database.OpenServing(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func readScope(t *testing.T, ctx context.Context, q database.Querier) string {
	t.Helper()
	var v string
	if err := q.QueryRow(ctx, `SELECT current_setting('varyaone.company_id', true)`).Scan(&v); err != nil {
		t.Fatalf("read scope: %v", err)
	}
	return v
}

// TestScopeRequestConnectionPinsAndDoesNotLeak covers the requireSession wiring:
// a request's queries run with varyaone.company_id set to the session company,
// the scope is gone once the request's release runs, and a later request (or an
// unauthenticated one) never inherits it.
func TestScopeRequestConnectionPinsAndDoesNotLeak(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := scopeTestPool(t, ctx)
	scoped := database.NewScoped(pool)

	requestScopePool.Store(pool)
	t.Cleanup(func() { requestScopePool.Store(nil) })

	const companyA = "aaaaaaaa-0000-4000-8000-00000000aaaa"
	const companyB = "bbbbbbbb-0000-4000-8000-00000000bbbb"

	reqCtx, release, err := scopeRequestConnection(ctx, companyA, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := readScope(t, reqCtx, scoped); got != companyA {
		t.Fatalf("request A scope = %q, want %q", got, companyA)
	}
	release()

	// After release the pooled connection is back and clean.
	if got := readScope(t, ctx, scoped); got != "" {
		t.Fatalf("after release, pool scope = %q, want empty", got)
	}

	reqCtx, release, err = scopeRequestConnection(ctx, companyB, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := readScope(t, reqCtx, scoped); got != companyB {
		t.Fatalf("request B scope = %q, want %q", got, companyB)
	}
	release()

	// An empty company (session with no company selected) is transparent.
	reqCtx, release, err = scopeRequestConnection(ctx, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if got := readScope(t, reqCtx, scoped); got != "" {
		t.Fatalf("empty-company request scope = %q, want empty", got)
	}
	release()
}

func TestScopeRequestConnectionNoopWithoutPool(t *testing.T) {
	requestScopePool.Store(nil)
	ctx := context.Background()
	got, release, err := scopeRequestConnection(ctx, "whatever", false)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if got != ctx {
		t.Fatal("expected the original context back when no scope pool is configured")
	}
}

func TestScopeRequestConnectionFailsClosedWhenPoolIsUnavailable(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, "postgres://unused:unused@127.0.0.1:1/unused?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	pool.Close()
	requestScopePool.Store(pool)
	t.Cleanup(func() { requestScopePool.Store(nil) })
	got, release, err := scopeRequestConnection(ctx, "aaaaaaaa-0000-4000-8000-00000000aaaa", true)
	if err == nil {
		t.Fatal("unavailable scope pool must return an error instead of allowing unscoped queries")
	}
	if got != nil || release != nil {
		t.Fatal("failed scope initialization returned a usable request context")
	}
}

func TestRequireSessionRejectsUnavailableCompanyScope(t *testing.T) {
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
	service, err := identity.NewService(pool, bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	session, err := service.Setup(ctx, identity.SetupInput{
		AdminName: "Scope Admin", AdminEmail: "scope@example.test", Password: "uzun-ve-guvenli-parola",
		LegalName: "Scope AŞ", TradeName: "Scope", EntityType: "LEGAL_ENTITY",
	}, identity.RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	closedPool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	closedPool.Close()
	requestScopePool.Store(closedPool)
	t.Cleanup(func() { requestScopePool.Store(nil) })
	called := false
	handler := (identityHandler{service: service}).requireSession(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/session", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session.Token})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if called {
		t.Fatal("business handler ran without a company-scoped connection")
	}
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "COMPANY_SCOPE_UNAVAILABLE") {
		t.Fatalf("response = %d %s, want company scope unavailable", response.Code, response.Body)
	}
	if response.Header().Get("Set-Cookie") != "" {
		t.Fatal("temporary scope failure must not clear a valid session")
	}
	// A healthy scope pool must still allow the same authenticated request.
	requestScopePool.Store(pool)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if !called || response.Code != http.StatusNoContent {
		t.Fatalf("healthy request = %d %s", response.Code, response.Body)
	}
}
