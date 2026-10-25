package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alpyxn/varyaone/internal/crm"
	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/party"
	"github.com/alpyxn/varyaone/internal/platform/migrations"
	"github.com/google/uuid"
)

func TestCRMCallListsImportAndPipeline(t *testing.T) {
	databaseURL := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool := httpPartyTestPool(t, ctx, databaseURL)
	if err := migrations.New(pool).Up(ctx); err != nil {
		t.Fatal(err)
	}
	identityService, err := identity.NewService(pool, bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	admin, err := identityService.Setup(ctx, identity.SetupInput{AdminName: "Satış Müdürü", AdminEmail: "mudur@example.test", Password: "uzun-ve-guvenli-parola", LegalName: "Arama AŞ", TradeName: "Arama", EntityType: "LEGAL_ENTITY"}, identity.RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	crmService := crm.NewService(pool)
	router := NewRouter(slog.New(slog.NewTextHandler(io.Discard, nil)), "test",
		readinessFunc(func(context.Context) error { return nil }),
		WithIdentity(identityService, false),
		WithParty(party.NewService(pool)),
		WithCRM(crmService))

	send := func(method, path, contentType string, body io.Reader, headers map[string]string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, path, body)
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: admin.Token})
		request.Header.Set("X-CSRF-Token", admin.CSRFToken)
		if contentType != "" {
			request.Header.Set("Content-Type", contentType)
		}
		for k, v := range headers {
			request.Header.Set(k, v)
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response
	}
	do := func(method, path, body string) *httptest.ResponseRecorder {
		return send(method, path, "", strings.NewReader(body), nil)
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
			t.Fatalf("decode %s: %v", response.Body.String(), err)
		}
	}

	// --- Import: header guessing, in-file duplicates, invalid rows, do-not-call numbers.
	blocked := do(http.MethodPost, "/api/v1/crm/leads", `{"display_name":"Eski Müşteri","phone":"0533 000 00 01"}`)
	expect(blocked, http.StatusCreated, "seed lead")
	var blockedLead crm.Lead
	decode(blocked, &blockedLead)
	expect(do(http.MethodPost, "/api/v1/crm/leads/"+blockedLead.ID+"/do-not-call", `{"do_not_call":true,"reason":"KVKK"}`), http.StatusOK, "mark dnc")

	csv := "Firma Adı,Yetkili,Telefon,E-Posta,Şehir\n" +
		"Alfa Ltd,Ali,0532 100 00 01,,İzmir\n" +
		"Beta AŞ,Banu,0532 100 00 02,banu@beta.test,Ankara\n" +
		"Beta Şube,Banu,+90 532 100 0002,,Ankara\n" +
		",Kimsesiz,0532 100 00 03,,\n" +
		"Gama Gıda,Gül,0533 000 00 01,,Bursa\n" +
		"Delta,Deniz,0532 100 00 04,,İstanbul\n"
	var form bytes.Buffer
	writer := multipart.NewWriter(&form)
	part, _ := writer.CreateFormFile("file", "adaylar.csv")
	_, _ = part.Write([]byte(csv))
	_ = writer.Close()
	response := send(http.MethodPost, "/api/v1/crm/leads/import/preview", writer.FormDataContentType(), &form, nil)
	expect(response, http.StatusOK, "import preview")
	var preview crm.ImportPreview
	decode(response, &preview)
	if preview.Total != 6 || preview.Mapping["display_name"] != 0 || preview.Mapping["contact_name"] != 1 ||
		preview.Mapping["phone"] != 2 || preview.Mapping["email"] != 3 || preview.Mapping["city"] != 4 {
		t.Fatalf("preview mapping: %+v", preview)
	}
	rows := make([]crm.ImportRow, 0, len(preview.Rows))
	for i, values := range preview.Rows {
		rows = append(rows, crm.ImportRow{Row: i + 2, DisplayName: values[0], ContactName: values[1], Phone: values[2], Email: values[3], City: values[4]})
	}

	listResp := do(http.MethodPost, "/api/v1/crm/call-lists", `{"name":"Ekim kampanyası","retry_minutes":60,"max_attempts":2}`)
	expect(listResp, http.StatusCreated, "create call list")
	var list crm.CallList
	decode(listResp, &list)

	payload, _ := json.Marshal(crm.ImportInput{Rows: rows, Source: "Fuar listesi", CallListID: list.ID})
	response = do(http.MethodPost, "/api/v1/crm/leads/import", string(payload))
	expect(response, http.StatusOK, "import")
	var imported crm.ImportResult
	decode(response, &imported)
	if imported.Created != 3 || imported.AddedToList != 3 || len(imported.Duplicates) != 1 || len(imported.DoNotCall) != 1 || len(imported.Invalid) != 1 {
		t.Fatalf("import result: %+v", imported)
	}

	// --- Two reps share the list; neither may get the lead the other holds.
	roleResp, err := identityService.CreateRole(ctx, admin, "Temsilci", []string{"crm.read", "crm.edit"}, identity.RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	reps := make([]identity.Session, 2)
	for i := range reps {
		email := "temsilci" + strconv.Itoa(i) + "@example.test"
		if _, err = identityService.AddMember(ctx, admin, identity.MemberInput{Email: email, DisplayName: "Temsilci " + strconv.Itoa(i), Password: "uzun-ve-guvenli-parola", RoleIDs: []string{roleResp.ID}}, identity.RequestMeta{}); err != nil {
			t.Fatal(err)
		}
		if reps[i], err = identityService.Login(ctx, email, "uzun-ve-guvenli-parola", "", identity.RequestMeta{IP: "127.0.0." + strconv.Itoa(i+1)}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := crmService.ClaimNext(ctx, reps[0], list.ID)
	if err != nil {
		t.Fatal(err)
	}
	again, err := crmService.ClaimNext(ctx, reps[0], list.ID)
	if err != nil || again.Item.ID != first.Item.ID {
		t.Fatalf("a refresh must return the held lead: %v %s/%s", err, again.Item.ID, first.Item.ID)
	}
	second, err := crmService.ClaimNext(ctx, reps[1], list.ID)
	if err != nil {
		t.Fatal(err)
	}
	if second.Item.ID == first.Item.ID {
		t.Fatal("two reps were handed the same lead")
	}
	if first.Item.LeadName != "Alfa Ltd" || first.Remaining != 3 {
		t.Fatalf("queue order/remaining: %s remaining %d", first.Item.LeadName, first.Remaining)
	}

	// Rep 0: no answer twice exhausts the item (max_attempts 2) with a retry gap between.
	call, err := crmService.LogCall(ctx, reps[0], first.Lead.ID, crm.CallInput{CallListItemID: first.Item.ID, Outcome: crm.OutcomeNoAnswer}, identity.RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if call.Lead.OwnerUserID == nil || *call.Lead.OwnerUserID != reps[0].User.ID {
		t.Fatalf("calling a pool lead claims it: %+v", call.Lead.OwnerUserID)
	}
	items, err := crmService.ListItems(ctx, admin, list.ID, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	var alfa crm.CallListItem
	for _, item := range items {
		if item.ID == first.Item.ID {
			alfa = item
		}
	}
	if alfa.Status != crm.ItemPending || alfa.Attempts != 1 || alfa.AvailableAt.Before(time.Now().Add(55*time.Minute)) || alfa.ClaimedBy != nil {
		t.Fatalf("after first no-answer: %+v", alfa)
	}
	if _, err = crmService.LogCall(ctx, reps[0], first.Lead.ID, crm.CallInput{CallListItemID: first.Item.ID, Outcome: crm.OutcomeBusy}, identity.RequestMeta{}); err != nil {
		t.Fatal(err)
	}
	items, _ = crmService.ListItems(ctx, admin, list.ID, crm.ItemDone, 50)
	if len(items) != 1 || items[0].ID != first.Item.ID {
		t.Fatalf("exhausted item not done: %+v", items)
	}
	if _, err = crmService.LogCall(ctx, reps[0], first.Lead.ID, crm.CallInput{CallListItemID: first.Item.ID, Outcome: crm.OutcomeBusy}, identity.RequestMeta{}); !errors.Is(err, crm.ErrCallItemNotFound) {
		t.Fatalf("calling a finished item: %v", err)
	}

	// Rep 1 skips; the skipped lead never comes back.
	if err = crmService.ReleaseItem(ctx, reps[1], list.ID, second.Item.ID, true, identity.RequestMeta{}); err != nil {
		t.Fatal(err)
	}
	third, err := crmService.ClaimNext(ctx, reps[1], list.ID)
	if err != nil {
		t.Fatal(err)
	}
	if third.Item.ID == second.Item.ID || third.Item.ID == first.Item.ID {
		t.Fatalf("skipped or finished lead returned: %s", third.Item.LeadName)
	}
	if _, err = crmService.LogCall(ctx, reps[1], third.Lead.ID, crm.CallInput{CallListItemID: third.Item.ID, Outcome: crm.OutcomeInterested}, identity.RequestMeta{}); err != nil {
		t.Fatal(err)
	}
	if _, err = crmService.ClaimNext(ctx, reps[1], list.ID); !errors.Is(err, crm.ErrCallItemNotFound) {
		t.Fatalf("queue should be empty: %v", err)
	}
	expect(do(http.MethodPost, "/api/v1/crm/call-lists/"+list.ID+"/next", ""), http.StatusOK, "empty queue over HTTP")

	// Adding by filter skips leads already listed and do-not-call ones.
	var added crm.AddItemsResult
	response = do(http.MethodPost, "/api/v1/crm/call-lists/"+list.ID+"/items", `{"filter":{}}`)
	expect(response, http.StatusOK, "add by filter")
	decode(response, &added)
	if added.Added != 0 {
		t.Fatalf("add by filter re-added leads: %+v", added)
	}

	// --- Pipeline: opportunity on a lead, conversion to cari, quote win.
	var stages struct {
		Items []crm.Stage `json:"items"`
	}
	decode(do(http.MethodGet, "/api/v1/crm/stages", ""), &stages)
	if len(stages.Items) != 6 {
		t.Fatalf("default stages: %d", len(stages.Items))
	}
	var lostStage, openStage crm.Stage
	for _, st := range stages.Items {
		if st.Kind == crm.StageLost {
			lostStage = st
		}
		if st.Name == "Pazarlık" {
			openStage = st
		}
	}
	response = do(http.MethodPost, "/api/v1/crm/opportunities", `{"title":"Filo yazılımı","lead_id":"`+third.Lead.ID+`","amount":"125000,50","currency":"try"}`)
	expect(response, http.StatusCreated, "create opportunity")
	var opp crm.Opportunity
	decode(response, &opp)
	if opp.Amount != "125000.5" || opp.Currency != "TRY" || opp.StageName != "Yeni" || opp.LeadID == nil {
		t.Fatalf("opportunity: %+v", opp)
	}
	expect(do(http.MethodPost, "/api/v1/crm/opportunities/"+opp.ID+"/activities", `{"kind":"MEETING","subject":"Demo"}`), http.StatusCreated, "opportunity meeting")
	moveHeaders := func(version int64) map[string]string {
		return map[string]string{"If-Match": `"` + strconv.FormatInt(version, 10) + `"`}
	}
	expect(send(http.MethodPost, "/api/v1/crm/opportunities/"+opp.ID+"/stage", "", strings.NewReader(`{"stage_id":"`+lostStage.ID+`"}`), moveHeaders(opp.Version)),
		http.StatusUnprocessableEntity, "lost without reason")
	response = send(http.MethodPost, "/api/v1/crm/opportunities/"+opp.ID+"/stage", "", strings.NewReader(`{"stage_id":"`+openStage.ID+`"}`), moveHeaders(opp.Version))
	expect(response, http.StatusOK, "move stage")
	decode(response, &opp)
	if opp.StageName != "Pazarlık" || opp.ClosedAt != nil {
		t.Fatalf("moved opportunity: %+v", opp)
	}
	expect(do(http.MethodPost, "/api/v1/crm/opportunities/"+opp.ID+"/quote", `{"quote_id":"`+uuid.NewString()+`"}`), http.StatusConflict, "quote before conversion")

	partyResp := do(http.MethodPost, "/api/v1/parties", `{"kind":"ORGANIZATION","is_customer":true,"is_supplier":false,"display_name":"Delta","legal_name":"Delta Lojistik AŞ","default_currency":"TRY","risk_policy":"WARN","addresses":[],"contacts":[],"group_ids":[],"tags":[],"custom_fields":{}}`)
	expect(partyResp, http.StatusCreated, "create party")
	var createdParty struct {
		ID string `json:"id"`
	}
	decode(partyResp, &createdParty)
	response = do(http.MethodPost, "/api/v1/crm/leads/"+third.Lead.ID+"/convert", `{"party_id":"`+createdParty.ID+`"}`)
	expect(response, http.StatusOK, "convert lead")
	var converted crm.Lead
	decode(response, &converted)
	if converted.Status != crm.StatusConverted || converted.ConvertedPartyID == nil {
		t.Fatalf("converted lead: %+v", converted)
	}
	expect(do(http.MethodPost, "/api/v1/crm/leads/"+third.Lead.ID+"/convert", `{"party_id":"`+createdParty.ID+`"}`), http.StatusConflict, "convert twice")
	if opp, err = crmService.GetOpportunity(ctx, admin, opp.ID); err != nil || opp.PartyID == nil || *opp.PartyID != createdParty.ID || opp.LeadID != nil {
		t.Fatalf("opportunity did not follow the conversion: %+v %v", opp, err)
	}
	var timeline struct {
		Items []crm.Activity `json:"items"`
	}
	decode(do(http.MethodGet, "/api/v1/crm/parties/"+createdParty.ID+"/activities", ""), &timeline)
	if len(timeline.Items) < 2 {
		t.Fatalf("cari timeline should include the lead's history: %d entries", len(timeline.Items))
	}
	expect(do(http.MethodPost, "/api/v1/crm/opportunities/"+opp.ID+"/quote", `{"quote_id":"`+uuid.NewString()+`"}`), http.StatusNotFound, "unknown quote")

	// The outbox handler wins every open opportunity tied to an accepted quote.
	quoteID := uuid.NewString()
	if _, err = pool.Exec(ctx, `UPDATE crm_opportunities SET quote_id=$1 WHERE id=$2`, quoteID, opp.ID); err != nil {
		t.Fatal(err)
	}
	payloadJSON, _ := json.Marshal(map[string]any{"entity_id": quoteID})
	if err = crmService.HandleQuoteAccepted(ctx, admin.CurrentCompanyID, payloadJSON); err != nil {
		t.Fatal(err)
	}
	if opp, err = crmService.GetOpportunity(ctx, admin, opp.ID); err != nil || opp.StageKind != crm.StageWon || opp.ClosedAt == nil {
		t.Fatalf("accepted quote did not win the opportunity: %+v %v", opp, err)
	}

	// --- Reports and reminders.
	response = do(http.MethodGet, "/api/v1/crm/reports", "")
	expect(response, http.StatusOK, "report")
	var report crm.Report
	decode(response, &report)
	totalCalls := 0
	for _, rep := range report.Reps {
		totalCalls += rep.Calls
	}
	if totalCalls != 3 || len(report.Won) != 1 || report.Won[0].Amount != "125000.5" {
		t.Fatalf("report: calls=%d won=%+v", totalCalls, report.Won)
	}
	repReport, err := crmService.BuildReport(ctx, reps[1], crm.ReportFilter{From: time.Now().Add(-time.Hour), To: time.Now().Add(time.Hour), UserID: reps[0].User.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(repReport.Reps) != 1 || repReport.Reps[0].UserID != reps[1].User.ID || repReport.Reps[0].Calls != 1 {
		t.Fatalf("a rep's report must cover only their own work: %+v", repReport.Reps)
	}

	due := time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339)
	expect(do(http.MethodPost, "/api/v1/crm/parties/"+createdParty.ID+"/activities", `{"kind":"TASK","subject":"Sözleşmeyi gönder","due_at":"`+due+`","remind_minutes":10}`), http.StatusCreated, "task")
	var reminders struct {
		Items []crm.Activity `json:"items"`
	}
	decode(do(http.MethodGet, "/api/v1/crm/reminders", ""), &reminders)
	if len(reminders.Items) != 1 {
		t.Fatalf("reminders: %+v", reminders.Items)
	}
	expect(do(http.MethodPost, "/api/v1/crm/reminders/notified", `{"ids":["`+reminders.Items[0].ID+`"]}`), http.StatusNoContent, "mark notified")
	decode(do(http.MethodGet, "/api/v1/crm/reminders", ""), &reminders)
	if len(reminders.Items) != 0 {
		t.Fatal("a rung reminder must not ring again")
	}
}

func TestCRMImportCarriesPriorityStatusAndDetails(t *testing.T) {
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
	identityService, err := identity.NewService(pool, bytes.Repeat([]byte{6}, 32))
	if err != nil {
		t.Fatal(err)
	}
	admin, err := identityService.Setup(ctx, identity.SetupInput{AdminName: "Müdür", AdminEmail: "aktarim@example.test", Password: "uzun-ve-guvenli-parola", LegalName: "Aktarım AŞ", TradeName: "Aktarım", EntityType: "LEGAL_ENTITY"}, identity.RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	crmService := crm.NewService(pool)

	// The standard file's columns, as people write them.
	csv := "Firma Adı,Telefon,İl,İlçe,Adres,Web Sitesi,Öncelik,Arama Durumu,Not\n" +
		"Sıcak Emlak,0532 200 00 01,Antalya,Alanya,Saray Mah. 1,,A - Çok sıcak,Aranmadı,\n" +
		"Görüşülen Emlak,0532 200 00 02,Aydın,Didim,,,b,Görüşüldü,Sonra aranacak\n" +
		"İlgili Emlak,0532 200 00 03,Muğla,Bodrum,,ilgili.com,C,İlgili,Demo gönderildi\n" +
		"Elenen Emlak,0532 200 00 04,Mersin,Erdemli,,,E,Elendi,İlgilenmiyor\n" +
		"Aramayın Emlak,0532 200 00 05,İzmir,Çeşme,,,D,Aranmasın,\n" +
		"Belirsiz Emlak,0532 200 00 06,İzmir,Çeşme,,,C,yessir,\n" +
		"Harfsiz Emlak,0532 200 00 07,İzmir,Çeşme,,,X,,\n"
	preview, err := crmService.PreviewImport(admin, strings.NewReader(csv), "liste.csv", "")
	if err != nil {
		t.Fatal(err)
	}
	for field, column := range map[string]int{"display_name": 0, "phone": 1, "city": 2, "district": 3, "address": 4, "website": 5, "priority": 6, "status": 7, "notes": 8} {
		if preview.Mapping[field] != column {
			t.Fatalf("mapping %s = %d, want %d (%v)", field, preview.Mapping[field], column, preview.Mapping)
		}
	}
	list, err := crmService.CreateCallList(ctx, admin, crm.CallListInput{Name: "Aktarım listesi"}, identity.RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	rows := make([]crm.ImportRow, 0, len(preview.Rows))
	for i, v := range preview.Rows {
		rows = append(rows, crm.ImportRow{Row: i + 2, DisplayName: v[0], Phone: v[1], City: v[2], District: v[3], Address: v[4], Website: v[5], Priority: v[6], Status: v[7], Notes: v[8]})
	}
	result, err := crmService.ImportLeads(ctx, admin, crm.ImportInput{Rows: rows, CallListID: list.ID}, identity.RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Created != 5 || len(result.Invalid) != 2 || result.AddedToList != 3 {
		t.Fatalf("import: created=%d invalid=%+v listed=%d", result.Created, result.Invalid, result.AddedToList)
	}
	leads, err := crmService.ListLeads(ctx, admin, crm.LeadFilter{}, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]crm.Lead{}
	for _, lead := range leads.Items {
		byName[lead.DisplayName] = lead
	}
	checks := []struct {
		name, status, priority string
		dnc                    bool
	}{
		{"Sıcak Emlak", crm.StatusNew, "A", false},
		{"Görüşülen Emlak", crm.StatusContacted, "B", false},
		{"İlgili Emlak", crm.StatusQualified, "C", false},
		{"Elenen Emlak", crm.StatusDisqualified, "E", false},
		{"Aramayın Emlak", crm.StatusDisqualified, "D", true},
	}
	for _, c := range checks {
		lead := byName[c.name]
		if lead.Status != c.status || lead.Priority != c.priority || lead.DoNotCall != c.dnc {
			t.Fatalf("%s: status=%s priority=%s dnc=%v", c.name, lead.Status, lead.Priority, lead.DoNotCall)
		}
	}
	if byName["Sıcak Emlak"].Address != "Saray Mah. 1" || byName["İlgili Emlak"].Website != "ilgili.com" {
		t.Fatalf("details not imported: %+v", byName["Sıcak Emlak"])
	}
	open, err := crmService.ListLeads(ctx, admin, crm.LeadFilter{HideClosed: true}, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, lead := range open.Items {
		if lead.Status == crm.StatusDisqualified || lead.DoNotCall {
			t.Fatalf("hidden lead shown: %s", lead.DisplayName)
		}
	}
	if len(open.Items) != 3 {
		t.Fatalf("hide closed: %d leads, want 3", len(open.Items))
	}
	// Asking for ruled-out leads explicitly still shows them.
	ruledOut, err := crmService.ListLeads(ctx, admin, crm.LeadFilter{HideClosed: true, Status: "DISQUALIFIED"}, "", 50)
	if err != nil || len(ruledOut.Items) != 2 {
		t.Fatalf("explicit disqualified filter: %v %d", err, len(ruledOut.Items))
	}
	dnc, err := crmService.ListLeads(ctx, admin, crm.LeadFilter{HideClosed: true, DoNotCall: "only"}, "", 50)
	if err != nil || len(dnc.Items) != 1 {
		t.Fatalf("explicit do-not-call filter: %v %d", err, len(dnc.Items))
	}
	// Walking the list one lead at a time follows priority and never repeats.
	walk := crm.LeadFilter{HideClosed: true, Callable: true, Sort: "priority"}
	var order []string
	cursor := ""
	for step := 0; step < 10; step++ {
		page, err := crmService.ListLeads(ctx, admin, walk, cursor, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) == 0 {
			break
		}
		order = append(order, page.Items[0].Priority)
		cursor = page.Items[0].ID
		// Calling the lead touches it; the walk must not loop back to it.
		if _, err = crmService.CreateActivity(ctx, admin, "lead", cursor, crm.ActivityInput{Kind: "NOTE", Body: "arandı"}, identity.RequestMeta{}); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Join(order, "") != "ABC" {
		t.Fatalf("walk order %v, want A B C once each", order)
	}
	// The lead after a ruled-out one (opened from elsewhere) is still found.
	after, err := crmService.ListLeads(ctx, admin, walk, byName["Elenen Emlak"].ID, 1)
	if err != nil || len(after.Items) != 0 {
		t.Fatalf("after the last grade: %v %+v", err, after.Items)
	}
	newest, err := crmService.ListLeads(ctx, admin, crm.LeadFilter{Sort: "newest"}, "", 2)
	if err != nil || len(newest.Items) != 2 || newest.NextCursor != newest.Items[1].ID {
		t.Fatalf("newest sort paging: %v %+v", err, newest)
	}
	if _, err = crmService.ListLeads(ctx, admin, crm.LeadFilter{Sort: "rastgele"}, "", 1); err == nil {
		t.Fatal("unknown sort must be refused")
	}
	hot, err := crmService.ListLeads(ctx, admin, crm.LeadFilter{Priority: "a"}, "", 50)
	if err != nil || len(hot.Items) != 1 || hot.Items[0].DisplayName != "Sıcak Emlak" {
		t.Fatalf("priority filter: %v %+v", err, hot.Items)
	}
	// Leads go on the list hottest first, ruled-out and do-not-call ones not at all.
	items, err := crmService.ListItems(ctx, admin, list.ID, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 || items[0].LeadName != "Sıcak Emlak" {
		t.Fatalf("list order: %+v", items)
	}
	other, err := crmService.CreateCallList(ctx, admin, crm.CallListInput{Name: "Öncelik sırası"}, identity.RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	// The leads are still waiting in the first list, so they are not queued twice…
	blocked, err := crmService.AddLeads(ctx, admin, other.ID, crm.AddLeadsInput{Filter: &crm.LeadFilter{Status: "NEW"}}, identity.RequestMeta{})
	if err != nil || blocked.Added != 0 || blocked.InOtherList != 1 {
		t.Fatalf("lead waiting in another list: %v %+v", err, blocked)
	}
	// …until that list is archived.
	if _, err = crmService.ArchiveCallList(ctx, admin, list.ID, true, identity.RequestMeta{}); err != nil {
		t.Fatal(err)
	}
	if _, err = crmService.AddLeads(ctx, admin, other.ID, crm.AddLeadsInput{Filter: &crm.LeadFilter{Status: "NEW"}}, identity.RequestMeta{}); err != nil {
		t.Fatal(err)
	}
	next, err := crmService.ClaimNext(ctx, admin, other.ID)
	if err != nil || next.Lead.DisplayName != "Sıcak Emlak" {
		t.Fatalf("hottest lead first: %v %s", err, next.Lead.DisplayName)
	}
}

// A real list (thousands of rows with notes) is well over the shared 1 MB
// request limit; the import endpoint must take it over HTTP.
func TestCRMImportAcceptsLargeRequest(t *testing.T) {
	databaseURL := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool := httpPartyTestPool(t, ctx, databaseURL)
	if err := migrations.New(pool).Up(ctx); err != nil {
		t.Fatal(err)
	}
	identityService, err := identity.NewService(pool, bytes.Repeat([]byte{4}, 32))
	if err != nil {
		t.Fatal(err)
	}
	admin, err := identityService.Setup(ctx, identity.SetupInput{AdminName: "Müdür", AdminEmail: "buyuk@example.test", Password: "uzun-ve-guvenli-parola", LegalName: "Büyük AŞ", TradeName: "Büyük", EntityType: "LEGAL_ENTITY"}, identity.RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(slog.New(slog.NewTextHandler(io.Discard, nil)), "test",
		readinessFunc(func(context.Context) error { return nil }),
		WithIdentity(identityService, false), WithCRM(crm.NewService(pool)))
	note := strings.Repeat("Emlakjet: 74 aktif ilan · son 3 ayda 25 işlem · ", 12)
	rows := make([]crm.ImportRow, 0, 2000)
	for i := 0; i < 2000; i++ {
		rows = append(rows, crm.ImportRow{Row: i + 2, DisplayName: "Ofis " + strconv.Itoa(i),
			Phone: "0532 " + strconv.Itoa(1000000+i), Priority: "B", Status: "Yeni", Notes: note})
	}
	body, _ := json.Marshal(crm.ImportInput{Rows: rows})
	if len(body) < 1<<20 {
		t.Fatalf("payload only %d bytes; the test must exceed 1 MB", len(body))
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/crm/leads/import", bytes.NewReader(body))
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: admin.Token})
	request.Header.Set("X-CSRF-Token", admin.CSRFToken)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("large import: %d %s", response.Code, response.Body.String())
	}
	var result crm.ImportResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.Created != 2000 {
		t.Fatalf("large import result: %v %+v", err, result)
	}
}

// Nobody is called a second time by accident: once a lead has been called it
// only comes back through a due callback or a deliberate choice.
func TestCRMNoAccidentalSecondCall(t *testing.T) {
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
	identityService, err := identity.NewService(pool, bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatal(err)
	}
	admin, err := identityService.Setup(ctx, identity.SetupInput{AdminName: "Müdür", AdminEmail: "tekrar@example.test", Password: "uzun-ve-guvenli-parola", LegalName: "Tekrar AŞ", TradeName: "Tekrar", EntityType: "LEGAL_ENTITY"}, identity.RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	svc := crm.NewService(pool)
	meta := identity.RequestMeta{}
	lead := func(name, phone, priority string) crm.Lead {
		created, err := svc.CreateLead(ctx, admin, crm.LeadInput{DisplayName: name, Phone: phone, Priority: priority, City: "İzmir", District: "Çeşme"}, meta)
		if err != nil {
			t.Fatal(err)
		}
		return created
	}
	fresh := lead("Taze", "0532 300 00 01", "A")
	refused := lead("Reddeden", "0532 300 00 02", "A")
	unanswered := lead("Açmayan", "0532 300 00 03", "B")
	callback := lead("Geri Arayacak", "0532 300 00 04", "B")
	waiting := lead("Bekleyen", "0532 300 00 05", "C")

	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	for _, call := range []struct {
		lead    crm.Lead
		outcome string
		at      string
	}{
		{refused, crm.OutcomeNotInterested, ""},
		{unanswered, crm.OutcomeNoAnswer, ""},
		{callback, crm.OutcomeCallback, past},
	} {
		if _, err = svc.LogCall(ctx, admin, call.lead.ID, crm.CallInput{Outcome: call.outcome, CallbackAt: call.at}, meta); err != nil {
			t.Fatal(err)
		}
	}
	first, err := svc.CreateCallList(ctx, admin, crm.CallListInput{Name: "Birinci"}, meta)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.AddLeads(ctx, admin, first.ID, crm.AddLeadsInput{LeadIDs: []string{waiting.ID}}, meta); err != nil {
		t.Fatal(err)
	}

	// Preview a new list for every lead in the district.
	preview, err := svc.AddLeads(ctx, admin, "", crm.AddLeadsInput{Filter: &crm.LeadFilter{District: "çeşme"}, DryRun: true}, meta)
	if err != nil {
		t.Fatal(err)
	}
	want := crm.AddItemsResult{Matched: 5, Added: 2, InOtherList: 1, CalledBefore: 1, NotCallable: 1, Skipped: 3}
	if preview != want {
		t.Fatalf("preview %+v, want %+v (fresh + due callback only)", preview, want)
	}
	second, err := svc.CreateCallList(ctx, admin, crm.CallListInput{Name: "İkinci"}, meta)
	if err != nil {
		t.Fatal(err)
	}
	added, err := svc.AddLeads(ctx, admin, second.ID, crm.AddLeadsInput{Filter: &crm.LeadFilter{Priority: "A,B"}}, meta)
	if err != nil || added.Added != 2 || added.CalledBefore != 1 {
		t.Fatalf("add A+B: %v %+v", err, added)
	}
	// Taking the unanswered lead on purpose is allowed.
	deliberate, err := svc.AddLeads(ctx, admin, second.ID, crm.AddLeadsInput{LeadIDs: []string{unanswered.ID}, IncludeCalled: true}, meta)
	if err != nil || deliberate.Added != 1 {
		t.Fatalf("deliberate add: %v %+v", err, deliberate)
	}

	// A call made outside the list closes the lead's place in every list.
	if _, err = svc.LogCall(ctx, admin, waiting.ID, crm.CallInput{Outcome: crm.OutcomeNotInterested}, meta); err != nil {
		t.Fatal(err)
	}
	items, err := svc.ListItems(ctx, admin, first.ID, "", 10)
	if err != nil || len(items) != 1 || items[0].Status != crm.ItemDone || items[0].Attempts != 1 {
		t.Fatalf("other list not updated: %v %+v", err, items)
	}

	// Walking the leads list offers only the fresh lead and the due callback.
	walk, err := svc.ListLeads(ctx, admin, crm.LeadFilter{Callable: true}, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, item := range walk.Items {
		names = append(names, item.DisplayName)
	}
	if strings.Join(names, ",") != fresh.DisplayName+","+callback.DisplayName {
		t.Fatalf("callable walk: %v", names)
	}

	// Archived lists cannot be called or filled; deleting keeps the history.
	if _, err = svc.ArchiveCallList(ctx, admin, second.ID, true, meta); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.ClaimNext(ctx, admin, second.ID); err == nil {
		t.Fatal("archived list must not hand out leads")
	}
	if _, err = svc.AddLeads(ctx, admin, second.ID, crm.AddLeadsInput{LeadIDs: []string{fresh.ID}}, meta); err == nil {
		t.Fatal("archived list must not accept leads")
	}
	if err = svc.DeleteCallList(ctx, admin, first.ID, meta); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.GetCallList(ctx, admin, first.ID); err != crm.ErrCallListNotFound {
		t.Fatalf("deleted list still readable: %v", err)
	}
	timeline, err := svc.ListActivities(ctx, admin, "lead", waiting.ID, 10)
	if err != nil || len(timeline) != 1 {
		t.Fatalf("history lost with the list: %v %d", err, len(timeline))
	}
}

func TestCRMCallListRoutes(t *testing.T) {
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
	identityService, err := identity.NewService(pool, bytes.Repeat([]byte{2}, 32))
	if err != nil {
		t.Fatal(err)
	}
	admin, err := identityService.Setup(ctx, identity.SetupInput{AdminName: "Müdür", AdminEmail: "rota@example.test", Password: "uzun-ve-guvenli-parola", LegalName: "Rota AŞ", TradeName: "Rota", EntityType: "LEGAL_ENTITY"}, identity.RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(slog.New(slog.NewTextHandler(io.Discard, nil)), "test",
		readinessFunc(func(context.Context) error { return nil }),
		WithIdentity(identityService, false), WithCRM(crm.NewService(pool)))
	do := func(method, path, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: admin.Token})
		request.Header.Set("X-CSRF-Token", admin.CSRFToken)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response
	}
	for i, phone := range []string{"0532 400 00 01", "0532 400 00 02"} {
		if r := do(http.MethodPost, "/api/v1/crm/leads", `{"display_name":"Rota `+strconv.Itoa(i)+`","phone":"`+phone+`","priority":"A"}`); r.Code != http.StatusCreated {
			t.Fatalf("lead: %d %s", r.Code, r.Body.String())
		}
	}
	r := do(http.MethodPost, "/api/v1/crm/call-lists/preview", `{"filter":{"priority":"A","never_called":true}}`)
	if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), `"added":2`) {
		t.Fatalf("preview: %d %s", r.Code, r.Body.String())
	}
	r = do(http.MethodPost, "/api/v1/crm/call-lists", `{"name":"Rota"}`)
	var list crm.CallList
	if err := json.Unmarshal(r.Body.Bytes(), &list); err != nil || r.Code != http.StatusCreated {
		t.Fatalf("create list: %d %s", r.Code, r.Body.String())
	}
	if r = do(http.MethodPost, "/api/v1/crm/call-lists/"+list.ID+"/items", `{"filter":{"priority":"A"},"dry_run":true}`); !strings.Contains(r.Body.String(), `"added":2`) {
		t.Fatalf("dry run: %s", r.Body.String())
	}
	if r = do(http.MethodPost, "/api/v1/crm/call-lists/"+list.ID+"/archive", `{"archived":true}`); r.Code != http.StatusOK || !strings.Contains(r.Body.String(), `"is_active":false`) {
		t.Fatalf("archive: %d %s", r.Code, r.Body.String())
	}
	if r = do(http.MethodPost, "/api/v1/crm/call-lists/"+list.ID+"/items", `{"filter":{}}`); r.Code != http.StatusUnprocessableEntity {
		t.Fatalf("add to archived list: %d %s", r.Code, r.Body.String())
	}
	if r = do(http.MethodDelete, "/api/v1/crm/call-lists/"+list.ID, ""); r.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.Code, r.Body.String())
	}
	if r = do(http.MethodGet, "/api/v1/crm/call-lists/"+list.ID, ""); r.Code != http.StatusNotFound {
		t.Fatalf("deleted list: %d", r.Code)
	}
}
