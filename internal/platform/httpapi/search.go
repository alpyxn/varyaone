package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/platform/database"
	"github.com/go-chi/chi/v5"
)

const (
	searchMaxQueryRunes = 128
	searchMaxLimit      = 50
)

// SearchService owns only the read model needed by the global search. It does
// not return domain aggregates, amounts, tax identifiers, or authentication
// data to the UI.
type SearchService struct{ pool database.Querier }

func NewSearchService(pool database.Querier) *SearchService { return &SearchService{pool: pool} }

type SearchResult struct {
	Items []SearchItem `json:"items"`
}

type SearchItem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Href   string `json:"href"`
}

type searchHandler struct{ service *SearchService }

func mountSearchRoutes(router chi.Router, identityService *identity.Service, service *SearchService) {
	auth := identityHandler{service: identityService}
	handler := searchHandler{service: service}
	router.With(auth.requireSession).Get("/api/v1/search", handler.search)
}

func (h searchHandler) search(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.Search(r.Context(), sessionFromRequest(r), r.URL.Query().Get("q"), queryLimit(r, 12, searchMaxLimit))
	if err != nil {
		writeSearchError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *SearchService) Search(ctx context.Context, session identity.Session, rawQuery string, limit int) (SearchResult, error) {
	if identity.ValidateExternalActor(session) != nil {
		return SearchResult{}, identity.ErrForbidden
	}
	if limit < 1 || limit > searchMaxLimit {
		limit = 12
	}
	query, searchableRunes := normalizeGlobalSearchQuery(rawQuery)
	if utf8.RuneCountInString(strings.TrimSpace(rawQuery)) > searchMaxQueryRunes {
		return SearchResult{}, fmt.Errorf("%w: arama metni çok uzun", identity.ErrValidation)
	}
	if searchableRunes < 2 || query == "" {
		return SearchResult{Items: []SearchItem{}}, nil
	}

	items := make([]SearchItem, 0, limit)
	// Each branch checks its own permission before touching the database. This
	// keeps a product-only token from probing party or document existence.
	if session.HasPermission("party.read") {
		partyItems, err := s.searchParties(ctx, session, query, limit)
		if err != nil {
			return SearchResult{}, err
		}
		items = append(items, partyItems...)
	}
	if session.HasPermission("product.read") {
		productItems, err := s.searchProducts(ctx, session, query, limit)
		if err != nil {
			return SearchResult{}, err
		}
		items = append(items, productItems...)
	}
	if session.HasPermission("document.read") {
		documentItems, err := s.searchDocuments(ctx, session, query, limit)
		if err != nil {
			return SearchResult{}, err
		}
		items = append(items, documentItems...)
	}
	if len(items) > limit {
		items = items[:limit]
	}
	return SearchResult{Items: items}, nil
}

func (s *SearchService) searchParties(ctx context.Context, session identity.Session, query string, limit int) ([]SearchItem, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT p.code,p.display_name,p.kind::text,p.id
		FROM parties p
		JOIN party_search_documents psd ON psd.company_id=p.company_id AND psd.party_id=p.id
		WHERE p.company_id=$1 AND p.is_active
		  AND psd.search_vector @@ to_tsquery('simple',$2)
		ORDER BY lower(p.display_name),p.id
		LIMIT $3`, session.CurrentCompanyID, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]SearchItem, 0, limit)
	for rows.Next() {
		var code, title, kind, id string
		if err := rows.Scan(&code, &title, &kind, &id); err != nil {
			return nil, err
		}
		kindLabel := "Kurum"
		if kind == "PERSON" {
			kindLabel = "Kişi"
		}
		items = append(items, SearchItem{Type: "party", Title: title, Detail: code + " · " + kindLabel, Href: "/cari/kartlar/" + id})
	}
	return items, rows.Err()
}

func (s *SearchService) searchProducts(ctx context.Context, session identity.Session, query string, limit int) ([]SearchItem, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT p.code,p.name,p.kind::text,p.id
		FROM products p
		WHERE p.company_id=$1 AND p.is_active
		  AND p.search_vector @@ to_tsquery('simple',$2)
		ORDER BY lower(p.name),p.id
		LIMIT $3`, session.CurrentCompanyID, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]SearchItem, 0, limit)
	for rows.Next() {
		var code, title, kind, id string
		if err := rows.Scan(&code, &title, &kind, &id); err != nil {
			return nil, err
		}
		kindLabel := "Stok Kartı"
		if kind == "SERVICE" {
			kindLabel = "Hizmet Kartı"
		}
		items = append(items, SearchItem{Type: "product", Title: title, Detail: code + " · " + kindLabel, Href: "/stok/urunler/" + id})
	}
	return items, rows.Err()
}

func (s *SearchService) searchDocuments(ctx context.Context, session identity.Session, query string, limit int) ([]SearchItem, error) {
	if strings.TrimSpace(session.User.ID) == "" {
		return []SearchItem{}, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT d.document_no,dt.display_name,to_char(d.document_date,'YYYY-MM-DD'),d.status,d.id
		FROM documents d
		JOIN document_types dt ON dt.code=d.document_type_code
		WHERE d.company_id=$1
		  AND d.search_vector @@ to_tsquery('simple',$2)
		  AND EXISTS (
			SELECT 1 FROM branches b
			WHERE b.company_id=d.company_id AND b.id=d.branch_id AND b.is_active
			  AND (NOT EXISTS(SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=d.company_id AND bs.user_id=$3)
			       OR EXISTS(SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=d.company_id AND bs.user_id=$3 AND bs.branch_id=b.id))
			  AND (d.warehouse_id IS NULL OR EXISTS(
				SELECT 1 FROM warehouses w
				WHERE w.company_id=d.company_id AND w.id=d.warehouse_id AND w.branch_id=b.id AND w.is_active
				  AND (NOT EXISTS(SELECT 1 FROM membership_warehouse_scopes ws WHERE ws.company_id=d.company_id AND ws.user_id=$3)
				       OR EXISTS(SELECT 1 FROM membership_warehouse_scopes ws WHERE ws.company_id=d.company_id AND ws.user_id=$3 AND ws.warehouse_id=w.id))
			  ))
		  )
		ORDER BY d.document_date DESC,d.id DESC
		LIMIT $4`, session.CurrentCompanyID, query, session.User.ID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]SearchItem, 0, limit)
	for rows.Next() {
		var documentNo, typeName, date, status, id string
		if err := rows.Scan(&documentNo, &typeName, &date, &status, &id); err != nil {
			return nil, err
		}
		items = append(items, SearchItem{Type: "document", Title: documentNo, Detail: typeName + " · " + date + " · " + status, Href: "/belgeler/" + id})
	}
	return items, rows.Err()
}

func normalizeGlobalSearchQuery(value string) (string, int) {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.NewReplacer(
		"ı", "i", "ğ", "g", "i̇", "i", "ü", "u", "ş", "s", "ö", "o", "ç", "c",
		"â", "a", "ä", "a", "à", "a", "á", "a", "ã", "a", "å", "a", "é", "e", "è", "e", "ë", "e", "ê", "e",
		"î", "i", "ï", "i", "ì", "i", "í", "i", "ñ", "n", "ó", "o", "ò", "o", "ô", "o", "õ", "o", "ú", "u", "ù", "u", "û", "u", "ý", "y", "ÿ", "y",
	).Replace(value)
	var builder strings.Builder
	searchableRunes := 0
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			builder.WriteRune(r)
			searchableRunes++
			continue
		}
		builder.WriteByte(' ')
	}
	tokens := strings.Fields(builder.String())
	for index := range tokens {
		tokens[index] += ":*"
	}
	return strings.Join(tokens, " & "), searchableRunes
}

func writeSearchError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, identity.ErrForbidden):
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu işlem için yetkiniz yok.")
	case errors.Is(err, identity.ErrValidation):
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", strings.TrimPrefix(err.Error(), identity.ErrValidation.Error()+": "))
	default:
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Arama yapılamadı.")
	}
}
