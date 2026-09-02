package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alpyxn/varyaone/internal/identity"
)

func TestDataExchangeReadPermissionIsEntityScoped(t *testing.T) {
	h := dataExchangeHandler{}
	tests := []struct {
		name        string
		entity      string
		permissions []string
		allowed     bool
	}{
		{name: "product export needs product read", entity: "PRODUCT", permissions: []string{"inventory.read"}},
		{name: "product read allows product export", entity: "PRODUCT", permissions: []string{"product.read"}, allowed: true},
		{name: "price list read allows price export", entity: "PRICE_LIST", permissions: []string{"pricing.read"}, allowed: true},
		{name: "warehouse export needs inventory read", entity: "WAREHOUSE", permissions: []string{"organization.warehouse.manage"}},
		{name: "count post can read its count workbook", entity: "STOCK_COUNT", permissions: []string{"inventory.count.post"}, allowed: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := h.readAllowedEntity(identity.Session{Permissions: test.permissions}, test.entity)
			if got != test.allowed {
				t.Fatalf("readAllowedEntity(%q, %v) = %v, want %v", test.entity, test.permissions, got, test.allowed)
			}
		})
	}
}

func TestDataExchangeWritePermissionRemainsSeparate(t *testing.T) {
	h := dataExchangeHandler{}
	request := requestWithSession(identity.Session{Permissions: []string{"product.read"}})
	if h.allowedEntity(request, "PRODUCT", true) {
		t.Fatal("product.read must not authorize product import")
	}
	request = requestWithSession(identity.Session{Permissions: []string{"product.create"}})
	if !h.allowedEntity(request, "PRODUCT", true) {
		t.Fatal("product.create should authorize product import")
	}
	request = requestWithSession(identity.Session{Permissions: []string{"product.edit"}})
	if !h.allowedEntity(request, "PRODUCT", true) {
		t.Fatal("product.edit should authorize product import")
	}

	request = requestWithSession(identity.Session{Permissions: []string{"inventory.read"}})
	if h.allowedEntity(request, "OPENING_STOCK", true) {
		t.Fatal("inventory.read must not authorize opening-stock import")
	}
	request = requestWithSession(identity.Session{Permissions: []string{"inventory.movement.post"}})
	if !h.allowedEntity(request, "OPENING_STOCK", true) {
		t.Fatal("inventory.movement.post should authorize opening-stock import")
	}
	request = requestWithSession(identity.Session{Permissions: []string{"product.create"}})
	if h.allowedEntity(request, "BARCODE", true) {
		t.Fatal("barcode import must remain disabled even with product write permission")
	}
	request = requestWithSession(identity.Session{Permissions: []string{"party.create"}})
	if !h.allowedEntity(request, "PARTY", true) {
		t.Fatal("party.create should authorize party import")
	}
}

func TestDataExchangeCapabilitiesHideUnauthorizedEntities(t *testing.T) {
	h := dataExchangeHandler{}
	request := requestWithSession(identity.Session{Permissions: []string{"product.read", "product.create", "party.read", "party.create"}})
	response := httptest.NewRecorder()
	h.capabilities(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("capabilities status = %d, body = %s", response.Code, response.Body.String())
	}
	var payload struct {
		MaxUploadBytes int `json:"max_upload_bytes"`
		Entities       []struct {
			Type       string `json:"type"`
			Importable bool   `json:"importable"`
			Exportable bool   `json:"exportable"`
		} `json:"entities"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.MaxUploadBytes != 64<<20 {
		t.Fatalf("max upload bytes = %d", payload.MaxUploadBytes)
	}
	seen := map[string]struct{ importable, exportable bool }{}
	for _, entity := range payload.Entities {
		seen[entity.Type] = struct{ importable, exportable bool }{entity.Importable, entity.Exportable}
	}
	if seen["BARCODE"].importable || !seen["BARCODE"].exportable {
		t.Fatalf("barcode capability = %#v", seen["BARCODE"])
	}
	if !seen["VARIANT"].importable || !seen["VARIANT"].exportable {
		t.Fatalf("variant capability = %#v", seen["VARIANT"])
	}
	if !seen["PARTY"].importable || !seen["PARTY"].exportable {
		t.Fatalf("party capability = %#v", seen["PARTY"])
	}
	if _, ok := seen["WAREHOUSE"]; ok {
		t.Fatal("warehouse must be hidden without warehouse read/write permission")
	}
}

func requestWithSession(session identity.Session) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/api/v1/imports", nil)
	return request.WithContext(contextWithSession(request, session))
}
