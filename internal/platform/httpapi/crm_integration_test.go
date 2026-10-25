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
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alpyxn/varyaone/internal/crm"
	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/platform/migrations"
	"github.com/google/uuid"
)

func TestCRMLeadCallFlow(t *testing.T) {
	databaseURL := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool := httpPartyTestPool(t, ctx, databaseURL)
	if err := migrations.New(pool).Up(ctx); err != nil {
		t.Fatal(err)
	}
	identityService, err := identity.NewService(pool, bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	session, err := identityService.Setup(ctx, identity.SetupInput{AdminName: "Satış Müdürü", AdminEmail: "crm@example.test", Password: "uzun-ve-guvenli-parola", LegalName: "CRM AŞ", TradeName: "CRM", EntityType: "LEGAL_ENTITY"}, identity.RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	crmService := crm.NewService(pool)
	router := NewRouter(slog.New(slog.NewTextHandler(io.Discard, nil)), "test",
		readinessFunc(func(context.Context) error { return nil }),
		WithIdentity(identityService, false),
		WithCRM(crmService))

	do := func(method, path, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session.Token})
		request.Header.Set("X-CSRF-Token", session.CSRFToken)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response
	}
	expect := func(response *httptest.ResponseRecorder, status int, what string) {
		t.Helper()
		if response.Code != status {
			t.Fatalf("%s: status %d, want %d: %s", what, response.Code, status, response.Body.String())
		}
	}
	decode := func(response *httptest.ResponseRecorder, into any) {
		t.Helper()
		if err := json.Unmarshal(response.Body.Bytes(), into); err != nil {
			t.Fatal(err)
		}
	}

	created := do(http.MethodPost, "/api/v1/crm/leads", `{"display_name":"Deniz Lojistik","contact_name":"Ayşe Kara","phone":"0532 111 22 33","source":"Fuar"}`)
	expect(created, http.StatusCreated, "create lead")
	var lead crm.Lead
	decode(created, &lead)
	if lead.Status != crm.StatusNew || lead.OwnerUserID != nil {
		t.Fatalf("a manager's new lead starts NEW in the shared pool: %+v", lead)
	}

	// The same number written differently is caught as a duplicate.
	dup := do(http.MethodPost, "/api/v1/crm/leads", `{"display_name":"Deniz Lojistik Şube","phone":"+90 532 111 2233"}`)
	expect(dup, http.StatusConflict, "duplicate lead")
	var dupBody struct {
		Code    string `json:"code"`
		Details struct {
			Matches []crm.LeadSummary `json:"matches"`
		} `json:"details"`
	}
	decode(dup, &dupBody)
	if dupBody.Code != "CRM_LEAD_DUPLICATE_PHONE" || len(dupBody.Details.Matches) != 1 || dupBody.Details.Matches[0].ID != lead.ID {
		t.Fatalf("duplicate response: %s", dup.Body.String())
	}
	expect(do(http.MethodPost, "/api/v1/crm/leads", `{"display_name":"Deniz Lojistik Şube","phone":"+90 532 111 2233","allow_duplicate":true}`),
		http.StatusCreated, "confirmed duplicate")

	// Nobody answered: the attempt is counted, the lead stays NEW and is now the caller's.
	var call crm.CallResult
	response := do(http.MethodPost, "/api/v1/crm/leads/"+lead.ID+"/calls", `{"outcome":"NO_ANSWER","duration_seconds":20}`)
	expect(response, http.StatusCreated, "no-answer call")
	decode(response, &call)
	if call.Lead.CallAttempts != 1 || call.Lead.Status != crm.StatusNew || call.Lead.OwnerUserID == nil || *call.Lead.OwnerUserID != session.User.ID {
		t.Fatalf("after no answer: %+v", call.Lead)
	}

	expect(do(http.MethodPost, "/api/v1/crm/leads/"+lead.ID+"/calls", `{"outcome":"CALLBACK"}`), http.StatusUnprocessableEntity, "callback without time")

	callbackAt := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	response = do(http.MethodPost, "/api/v1/crm/leads/"+lead.ID+"/calls", `{"outcome":"CALLBACK","note":"Perşembe tekrar arayın","callback_at":"`+callbackAt+`","remind_minutes":15}`)
	expect(response, http.StatusCreated, "callback call")
	decode(response, &call)
	if call.Lead.Status != crm.StatusContacted || call.Lead.CallAttempts != 2 || call.Lead.NextActionAt == nil || call.Task == nil {
		t.Fatalf("after callback: %+v task=%v", call.Lead, call.Task)
	}

	var tasks struct {
		Items []crm.Activity `json:"items"`
	}
	response = do(http.MethodGet, "/api/v1/crm/tasks?state=overdue", "")
	expect(response, http.StatusOK, "overdue tasks")
	decode(response, &tasks)
	if len(tasks.Items) != 1 || tasks.Items[0].ID != call.Task.ID || tasks.Items[0].LeadName != "Deniz Lojistik" {
		t.Fatalf("overdue tasks: %s", response.Body.String())
	}
	response = do(http.MethodGet, "/api/v1/crm/leads?due=true", "")
	expect(response, http.StatusOK, "due leads")
	var due crm.LeadList
	decode(response, &due)
	if len(due.Items) != 1 || due.Items[0].ID != lead.ID {
		t.Fatalf("due leads: %s", response.Body.String())
	}
	expect(do(http.MethodPost, "/api/v1/crm/tasks/"+call.Task.ID+"/done", `{"done":true}`), http.StatusOK, "complete task")
	decode(do(http.MethodGet, "/api/v1/crm/tasks", ""), &tasks)
	if len(tasks.Items) != 0 {
		t.Fatalf("completed task still open: %+v", tasks.Items)
	}

	expect(do(http.MethodPost, "/api/v1/crm/leads/"+lead.ID+"/activities", `{"kind":"NOTE","body":"Filo 40 araç"}`), http.StatusCreated, "note")
	expect(do(http.MethodPost, "/api/v1/crm/leads/"+lead.ID+"/activities", `{"kind":"CALL","call_outcome":"BUSY"}`), http.StatusUnprocessableEntity, "lead call via activities")
	var timeline struct {
		Items []crm.Activity `json:"items"`
	}
	decode(do(http.MethodGet, "/api/v1/crm/leads/"+lead.ID+"/activities", ""), &timeline)
	if len(timeline.Items) != 4 {
		t.Fatalf("timeline has %d entries, want 2 calls + task + note", len(timeline.Items))
	}

	// Asked not to be called: the lead leaves every call flow until a manager lifts it.
	response = do(http.MethodPost, "/api/v1/crm/leads/"+lead.ID+"/calls", `{"outcome":"DO_NOT_CALL","note":"Aramayın dedi"}`)
	expect(response, http.StatusCreated, "do-not-call outcome")
	decode(response, &call)
	if !call.Lead.DoNotCall || call.Lead.Status != crm.StatusDisqualified || call.Lead.DoNotCallReason != "Aramayın dedi" {
		t.Fatalf("after do-not-call: %+v", call.Lead)
	}
	response = do(http.MethodPost, "/api/v1/crm/leads/"+lead.ID+"/calls", `{"outcome":"NO_ANSWER"}`)
	expect(response, http.StatusConflict, "call to do-not-call lead")
	if !strings.Contains(response.Body.String(), "CRM_LEAD_DO_NOT_CALL") {
		t.Fatalf("do-not-call refusal: %s", response.Body.String())
	}
	expect(do(http.MethodPost, "/api/v1/crm/leads/"+lead.ID+"/do-not-call", `{"do_not_call":false}`), http.StatusOK, "manager lifts do-not-call")

	// Optimistic locking on the card.
	expect(do(http.MethodPatch, "/api/v1/crm/leads/"+lead.ID, `{"display_name":"X","phone":"1"}`), http.StatusPreconditionRequired, "patch without If-Match")
	current, err := crmService.GetLead(ctx, session, lead.ID)
	if err != nil {
		t.Fatal(err)
	}
	patch := func(version int64, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPatch, "/api/v1/crm/leads/"+lead.ID, strings.NewReader(body))
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session.Token})
		request.Header.Set("X-CSRF-Token", session.CSRFToken)
		request.Header.Set("If-Match", `"`+strconv.FormatInt(version, 10)+`"`)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		return recorder
	}
	body := `{"display_name":"Deniz Lojistik A.Ş.","phone":"0532 111 22 33","status":"QUALIFIED","owner_user_id":"` + session.User.ID + `","allow_duplicate":true}`
	expect(patch(current.Version, body), http.StatusOK, "update lead")
	expect(patch(current.Version, body), http.StatusPreconditionFailed, "stale update")

	// A rep without crm.view_all sees their own leads and the unassigned pool only.
	rep := session
	rep.User.ID = uuid.NewString()
	rep.Permissions = []string{"crm.read", "crm.edit"}
	visible, err := crmService.ListLeads(ctx, rep, crm.LeadFilter{}, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(visible.Items) != 1 || visible.Items[0].ID == lead.ID {
		t.Fatalf("rep sees %d leads, want only the unassigned duplicate", len(visible.Items))
	}
	if _, err = crmService.GetLead(ctx, rep, lead.ID); err != crm.ErrLeadNotFound {
		t.Fatalf("rep reading a colleague's lead: %v", err)
	}
	if _, err = crmService.ListTasks(ctx, rep, crm.TaskFilter{Scope: "all"}, 10); err != identity.ErrForbidden {
		t.Fatalf("rep listing everyone's tasks: %v", err)
	}

	expect(do(http.MethodGet, "/api/v1/crm/parties/"+uuid.NewString()+"/activities", ""), http.StatusOK, "unknown party timeline is empty")
	expect(do(http.MethodPost, "/api/v1/crm/parties/"+uuid.NewString()+"/activities", `{"kind":"NOTE","body":"x"}`), http.StatusNotFound, "note on unknown party")

	// Switching the module off closes the API.
	disable := httptest.NewRequest(http.MethodPut, "/api/v1/modules/crm", strings.NewReader(`{"enabled":false}`))
	disable.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session.Token})
	disable.Header.Set("X-CSRF-Token", session.CSRFToken)
	disable.Header.Set("If-Match", `"1"`)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, disable)
	expect(response, http.StatusOK, "disable module")
	response = do(http.MethodGet, "/api/v1/crm/leads", "")
	expect(response, http.StatusForbidden, "disabled module")
	if !strings.Contains(response.Body.String(), "MODULE_DISABLED") {
		t.Fatalf("disabled module response: %s", response.Body.String())
	}
}
