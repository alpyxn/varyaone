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
	"strings"
	"testing"
	"time"

	"github.com/alpyxn/varyaone/internal/fixedasset"
	"github.com/alpyxn/varyaone/internal/hr/employee"
	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/payroll/legislation"
	"github.com/alpyxn/varyaone/internal/platform/migrations"
)

func TestFixedAssetAssignmentLifecycle(t *testing.T) {
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
	masterKey := bytes.Repeat([]byte{8}, 32)
	identityService, err := identity.NewService(pool, masterKey)
	if err != nil {
		t.Fatal(err)
	}
	session, err := identityService.Setup(ctx, identity.SetupInput{AdminName: "HR Admin", AdminEmail: "hr@example.test", Password: "uzun-ve-guvenli-parola", LegalName: "HR AŞ", TradeName: "HR", EntityType: "LEGAL_ENTITY"}, identity.RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	employeeService, err := employee.NewService(pool, masterKey, legislation.NewRepository(pool))
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(slog.New(slog.NewTextHandler(io.Discard, nil)), "test",
		readinessFunc(func(context.Context) error { return nil }),
		WithIdentity(identityService, false),
		WithHREmployee(employeeService),
		WithFixedAsset(fixedasset.NewService(pool)))

	do := func(method, path, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session.Token})
		request.Header.Set("X-CSRF-Token", session.CSRFToken)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response
	}

	empResp := do(http.MethodPost, "/api/v1/hr/employees", `{"employee_code":"E001","first_name":"Ada","last_name":"Yılmaz","status":"ACTIVE","position_title":"Mühendis","work_email":"","personal_email":"","phone":"",
 "employment":{"start_date":"2026-01-02","gross_wage":"40000"}}`)
	if empResp.Code != http.StatusCreated {
		t.Fatalf("employee create %d: %s", empResp.Code, empResp.Body.String())
	}
	var emp struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(empResp.Body.Bytes(), &emp)

	assetResp := do(http.MethodPost, "/api/v1/fixed-assets", `{"asset_code":"","name":"Dizüstü","category":"BT","serial_number":"SN-1","description":"","status":"AVAILABLE"}`)
	if assetResp.Code != http.StatusCreated {
		t.Fatalf("asset create %d: %s", assetResp.Code, assetResp.Body.String())
	}
	var asset struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	_ = json.Unmarshal(assetResp.Body.Bytes(), &asset)

	assignResp := do(http.MethodPost, "/api/v1/fixed-assets/"+asset.ID+"/assignments",
		`{"employee_id":"`+emp.ID+`","assigned_at":"2026-08-01","note":"ilk teslim"}`)
	if assignResp.Code != http.StatusOK {
		t.Fatalf("assign %d: %s", assignResp.Code, assignResp.Body.String())
	}
	var assigned struct {
		Status     string `json:"status"`
		AssignedTo *struct {
			AssignmentID string `json:"assignment_id"`
		} `json:"assigned_to"`
	}
	_ = json.Unmarshal(assignResp.Body.Bytes(), &assigned)
	if assigned.Status != "ASSIGNED" || assigned.AssignedTo == nil {
		t.Fatalf("expected ASSIGNED with assignment, got %s", assignResp.Body.String())
	}

	// A second active assignment must be rejected.
	dup := do(http.MethodPost, "/api/v1/fixed-assets/"+asset.ID+"/assignments",
		`{"employee_id":"`+emp.ID+`","assigned_at":"2026-08-02","note":""}`)
	if dup.Code != http.StatusConflict {
		t.Fatalf("expected 409 on double assign, got %d: %s", dup.Code, dup.Body.String())
	}

	returnResp := do(http.MethodPost, "/api/v1/fixed-assets/"+asset.ID+"/assignments/"+assigned.AssignedTo.AssignmentID+"/return",
		`{"returned_at":"2026-08-10","note":"iade"}`)
	if returnResp.Code != http.StatusOK {
		t.Fatalf("return %d: %s", returnResp.Code, returnResp.Body.String())
	}
	if !strings.Contains(returnResp.Body.String(), `"status":"AVAILABLE"`) {
		t.Fatalf("expected AVAILABLE after return, got %s", returnResp.Body.String())
	}

	// Returning an already-returned assignment is immutable.
	again := do(http.MethodPost, "/api/v1/fixed-assets/"+asset.ID+"/assignments/"+assigned.AssignedTo.AssignmentID+"/return",
		`{"returned_at":"2026-08-11","note":""}`)
	if again.Code != http.StatusConflict {
		t.Fatalf("expected 409 on second return, got %d: %s", again.Code, again.Body.String())
	}

	history := do(http.MethodGet, "/api/v1/hr/employees/"+emp.ID+"/asset-assignments", "")
	if history.Code != http.StatusOK || !strings.Contains(history.Body.String(), "SK-0001") {
		t.Fatalf("employee history %d: %s", history.Code, history.Body.String())
	}
}
