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

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/platform/migrations"
	"github.com/google/uuid"
)

func TestNormalizeGlobalSearchQuery(t *testing.T) {
	query, count := normalizeGlobalSearchQuery("  Çelik STK-001  ")
	if query != "celik:* & stk:* & 001:*" || count != 11 {
		t.Fatalf("unexpected normalized query %q with %d searchable runes", query, count)
	}
	query, count = normalizeGlobalSearchQuery("---")
	if query != "" || count != 0 {
		t.Fatalf("punctuation should not become a search query: %q (%d)", query, count)
	}
}

func TestSearchReturnsEmptyForEmptyShortAndUnauthorizedCategories(t *testing.T) {
	service := NewSearchService(nil)
	session := identity.Session{CurrentCompanyID: uuid.NewString(), User: identity.User{ID: uuid.NewString()}}
	for _, query := range []string{"", "a", "---"} {
		result, err := service.Search(context.Background(), session, query, 12)
		if err != nil {
			t.Fatalf("query %q returned error: %v", query, err)
		}
		if result.Items == nil || len(result.Items) != 0 {
			t.Fatalf("query %q returned unexpected items: %#v", query, result.Items)
		}
	}
	result, err := service.Search(context.Background(), session, "Arama", 12)
	if err != nil {
		t.Fatalf("permission-filtered query returned error: %v", err)
	}
	if len(result.Items) != 0 {
		t.Fatalf("a session without category permissions returned items: %#v", result.Items)
	}
}

func TestSearchAPIIsCompanyAndPermissionFiltered(t *testing.T) {
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
	session, err := identityService.Setup(ctx, identity.SetupInput{
		AdminName: "Search Admin", AdminEmail: "search@example.test", Password: "uzun-ve-guvenli-parola",
		LegalName: "Search AŞ", TradeName: "Search", EntityType: "LEGAL_ENTITY",
	}, identity.RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	foreignCompany := uuid.NewString()
	foreignParty := uuid.NewString()
	visibleParty := uuid.NewString()
	productID := uuid.NewString()
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO companies(id,legal_name,trade_name,entity_type) VALUES($1,'Gizli AŞ','Gizli','LEGAL_ENTITY')`, []any{foreignCompany}},
		{`INSERT INTO parties(id,company_id,code,kind,is_customer,display_name,legal_name,default_currency) VALUES($1,$2,'GIZLI','ORGANIZATION',true,'Gizli Arama','Gizli AŞ','TRY')`, []any{foreignParty, foreignCompany}},
		{`INSERT INTO parties(id,company_id,code,kind,is_customer,display_name,legal_name,default_currency) VALUES($1,$2,'GORUNEN','ORGANIZATION',true,'Görünen Arama','Görünen AŞ','TRY')`, []any{visibleParty, session.CurrentCompanyID}},
		{`INSERT INTO products(id,company_id,code,name,kind) VALUES($1,$2,'STK-ARAMA','Arama Ürün','PHYSICAL')`, []any{productID, session.CurrentCompanyID}},
		{`UPDATE products SET search_vector=to_tsvector('simple','STK-ARAMA Arama-Ürün') WHERE company_id=$1 AND id=$2`, []any{session.CurrentCompanyID, productID}},
	} {
		if _, err = pool.Exec(ctx, statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	token, err := identityService.CreateAPIToken(ctx, session, "Sadece cari", []string{"party:read"}, nil, identity.RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(slog.New(slog.NewTextHandler(io.Discard, nil)), "test", readinessFunc(func(context.Context) error { return nil }), WithIdentity(identityService, false), WithSearch(NewSearchService(pool)))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/search?q=Arama&limit=20", nil)
	request.Header.Set("Authorization", "Bearer "+token.PlainToken)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("search returned %d: %s", response.Code, response.Body.String())
	}
	var result SearchResult
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || result.Items[0].Title != "Görünen Arama" || result.Items[0].Type != "party" {
		t.Fatalf("search leaked categories or companies: %#v", result.Items)
	}
}
