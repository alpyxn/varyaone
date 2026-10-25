package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/alpyxn/varyaone/internal/crm"
	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/platform/httpapi/contract"
	"github.com/go-chi/chi/v5"
)

type crmHandler struct{ service *crm.Service }

func mountCRMRoutes(router chi.Router, identityService *identity.Service, service *crm.Service) {
	auth := identityHandler{service: identityService}
	h := crmHandler{service: service}
	read := router.With(auth.requireSession)
	read.Get("/api/v1/crm/leads", h.listLeads)
	read.Get("/api/v1/crm/leads/duplicates", h.duplicates)
	read.Get("/api/v1/crm/leads/{leadID}", h.getLead)
	read.Get("/api/v1/crm/leads/{leadID}/activities", h.listActivities("lead", "leadID"))
	read.Get("/api/v1/crm/parties/{partyID}/activities", h.listActivities("party", "partyID"))
	read.Get("/api/v1/crm/tasks", h.listTasks)
	read.Get("/api/v1/crm/members", h.listMembers)
	read.Get("/api/v1/crm/call-lists", h.listCallLists)
	read.Get("/api/v1/crm/call-lists/{listID}", h.getCallList)
	read.Get("/api/v1/crm/call-lists/{listID}/items", h.listItems)
	read.Get("/api/v1/crm/scripts", h.listScripts)
	read.Get("/api/v1/crm/stages", h.listStages)
	read.Get("/api/v1/crm/opportunities", h.listOpportunities)
	read.Get("/api/v1/crm/opportunities/{opportunityID}", h.getOpportunity)
	read.Get("/api/v1/crm/opportunities/{opportunityID}/activities", h.listActivities("opportunity", "opportunityID"))
	read.Get("/api/v1/crm/reminders", h.listReminders)
	read.Get("/api/v1/crm/reports", h.report)
	write := router.With(auth.requireSession, auth.requireCSRF)
	write.Post("/api/v1/crm/leads", h.createLead)
	write.Patch("/api/v1/crm/leads/{leadID}", h.updateLead)
	write.Post("/api/v1/crm/leads/{leadID}/do-not-call", h.setDoNotCall)
	write.Post("/api/v1/crm/leads/{leadID}/calls", h.logCall)
	write.Post("/api/v1/crm/leads/{leadID}/activities", h.createActivity("lead", "leadID"))
	write.Post("/api/v1/crm/parties/{partyID}/activities", h.createActivity("party", "partyID"))
	write.Post("/api/v1/crm/tasks/{activityID}/done", h.setTaskDone)
	write.Post("/api/v1/crm/leads/{leadID}/convert", h.convertLead)
	write.Post("/api/v1/crm/leads/import/preview", h.previewImport)
	write.Post("/api/v1/crm/leads/import", h.importLeads)
	write.Post("/api/v1/crm/call-lists", h.createCallList)
	write.Patch("/api/v1/crm/call-lists/{listID}", h.updateCallList)
	write.Post("/api/v1/crm/call-lists/preview", h.previewItems)
	write.Post("/api/v1/crm/call-lists/{listID}/items", h.addItems)
	write.Post("/api/v1/crm/call-lists/{listID}/archive", h.archiveCallList)
	write.Delete("/api/v1/crm/call-lists/{listID}", h.deleteCallList)
	write.Post("/api/v1/crm/call-lists/{listID}/items/remove", h.removeItems)
	write.Post("/api/v1/crm/call-lists/{listID}/next", h.claimNext)
	write.Post("/api/v1/crm/call-lists/{listID}/items/{itemID}/release", h.releaseItem)
	write.Post("/api/v1/crm/scripts", h.createScript)
	write.Patch("/api/v1/crm/scripts/{scriptID}", h.updateScript)
	write.Post("/api/v1/crm/stages", h.createStage)
	write.Patch("/api/v1/crm/stages/{stageID}", h.updateStage)
	write.Post("/api/v1/crm/opportunities", h.createOpportunity)
	write.Patch("/api/v1/crm/opportunities/{opportunityID}", h.updateOpportunity)
	write.Post("/api/v1/crm/opportunities/{opportunityID}/stage", h.moveStage)
	write.Post("/api/v1/crm/opportunities/{opportunityID}/quote", h.linkQuote)
	write.Post("/api/v1/crm/opportunities/{opportunityID}/activities", h.createActivity("opportunity", "opportunityID"))
	write.Post("/api/v1/crm/reminders/notified", h.markReminders)
}

func (h crmHandler) listLeads(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	limit, _ := strconv.Atoi(query.Get("limit"))
	result, err := h.service.ListLeads(r.Context(), sessionFromRequest(r), crm.LeadFilter{
		Query:       query.Get("q"),
		Status:      query.Get("status"),
		Owner:       query.Get("owner"),
		Source:      query.Get("source"),
		City:        query.Get("city"),
		District:    query.Get("district"),
		NeverCalled: query.Get("never_called") == "true",
		Priority:    query.Get("priority"),
		HideClosed:  query.Get("hide_closed") == "true",
		Sort:        query.Get("sort"),
		Callable:    query.Get("callable") == "true",
		DueOnly:     query.Get("due") == "true",
		DoNotCall:   query.Get("do_not_call"),
	}, query.Get("cursor"), limit)
	if err != nil {
		writeCRMError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h crmHandler) duplicates(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.FindDuplicates(r.Context(), sessionFromRequest(r), r.URL.Query().Get("phone"), r.URL.Query().Get("exclude"))
	if err != nil {
		writeCRMError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h crmHandler) getLead(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.GetLead(r.Context(), sessionFromRequest(r), chi.URLParam(r, "leadID"))
	if err != nil {
		writeCRMError(w, r, err)
		return
	}
	writeLead(w, http.StatusOK, item)
}

func (h crmHandler) createLead(w http.ResponseWriter, r *http.Request) {
	var input crm.LeadInput
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Aday bilgileri geçersiz.")
		return
	}
	item, err := h.service.CreateLead(r.Context(), sessionFromRequest(r), input, requestMeta(r))
	if err != nil {
		writeCRMError(w, r, err)
		return
	}
	writeLead(w, http.StatusCreated, item)
}

func (h crmHandler) updateLead(w http.ResponseWriter, r *http.Request) {
	version, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Aday güncellemesi için If-Match gereklidir.")
		return
	}
	var input crm.LeadInput
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Aday bilgileri geçersiz.")
		return
	}
	item, err := h.service.UpdateLead(r.Context(), sessionFromRequest(r), chi.URLParam(r, "leadID"), version, input, requestMeta(r))
	if err != nil {
		writeCRMError(w, r, err)
		return
	}
	writeLead(w, http.StatusOK, item)
}

func (h crmHandler) setDoNotCall(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DoNotCall bool   `json:"do_not_call"`
		Reason    string `json:"reason"`
	}
	if decodeJSON(r, &body) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "İstek geçersiz.")
		return
	}
	item, err := h.service.SetDoNotCall(r.Context(), sessionFromRequest(r), chi.URLParam(r, "leadID"), body.DoNotCall, body.Reason, requestMeta(r))
	if err != nil {
		writeCRMError(w, r, err)
		return
	}
	writeLead(w, http.StatusOK, item)
}

func (h crmHandler) logCall(w http.ResponseWriter, r *http.Request) {
	var input crm.CallInput
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Arama bilgileri geçersiz.")
		return
	}
	result, err := h.service.LogCall(r.Context(), sessionFromRequest(r), chi.URLParam(r, "leadID"), input, requestMeta(r))
	if err != nil {
		writeCRMError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (h crmHandler) listActivities(target, param string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		items, err := h.service.ListActivities(r.Context(), sessionFromRequest(r), target, chi.URLParam(r, param), limit)
		if err != nil {
			writeCRMError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

func (h crmHandler) createActivity(target, param string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input crm.ActivityInput
		if decodeJSON(r, &input) != nil {
			writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Kayıt bilgileri geçersiz.")
			return
		}
		item, err := h.service.CreateActivity(r.Context(), sessionFromRequest(r), target, chi.URLParam(r, param), input, requestMeta(r))
		if err != nil {
			writeCRMError(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, item)
	}
}

func (h crmHandler) listTasks(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := h.service.ListTasks(r.Context(), sessionFromRequest(r), crm.TaskFilter{
		Scope: r.URL.Query().Get("scope"),
		State: r.URL.Query().Get("state"),
	}, limit)
	if err != nil {
		writeCRMError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h crmHandler) setTaskDone(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Done bool `json:"done"`
	}
	if decodeJSON(r, &body) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "İstek geçersiz.")
		return
	}
	item, err := h.service.SetTaskDone(r.Context(), sessionFromRequest(r), chi.URLParam(r, "activityID"), body.Done, requestMeta(r))
	if err != nil {
		writeCRMError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h crmHandler) listMembers(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.ListMembers(r.Context(), sessionFromRequest(r))
	if err != nil {
		writeCRMError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func writeLead(w http.ResponseWriter, status int, item crm.Lead) {
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, status, item)
}

func writeCRMError(w http.ResponseWriter, r *http.Request, err error) {
	var duplicate *crm.DuplicateError
	switch {
	case errors.As(err, &duplicate):
		writeJSON(w, http.StatusConflict, contract.ErrorResponse{
			Code:    "CRM_LEAD_DUPLICATE_PHONE",
			Message: "Bu telefon numarası başka bir adayda kayıtlı.",
			Details: map[string]any{"matches": duplicate.Matches},
			TraceId: TraceID(r.Context()),
		})
	case errors.Is(err, identity.ErrForbidden):
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu işlem için yetkiniz yok.")
	case errors.Is(err, identity.ErrValidation):
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", err.Error())
	case errors.Is(err, identity.ErrConflict):
		writeError(w, r, http.StatusPreconditionFailed, "VERSION_CONFLICT", "Aday kaydı başka bir kullanıcı tarafından değiştirildi.")
	case errors.Is(err, crm.ErrLeadNotFound):
		writeError(w, r, http.StatusNotFound, "CRM_LEAD_NOT_FOUND", "Aday bulunamadı.")
	case errors.Is(err, crm.ErrActivityNotFound):
		writeError(w, r, http.StatusNotFound, "CRM_ACTIVITY_NOT_FOUND", "Kayıt bulunamadı.")
	case errors.Is(err, crm.ErrPartyNotFound):
		writeError(w, r, http.StatusNotFound, "CRM_PARTY_NOT_FOUND", "Cari bulunamadı.")
	case errors.Is(err, crm.ErrDoNotCall):
		writeError(w, r, http.StatusConflict, "CRM_LEAD_DO_NOT_CALL", "Bu aday aranmak istemiyor.")
	case errors.Is(err, crm.ErrLeadConverted):
		writeError(w, r, http.StatusConflict, "CRM_LEAD_CONVERTED", "Aday cariye dönüştürülmüş; görüşmeleri cari kartından kaydedin.")
	case errors.Is(err, crm.ErrCallListNotFound):
		writeError(w, r, http.StatusNotFound, "CRM_CALL_LIST_NOT_FOUND", "Arama listesi bulunamadı.")
	case errors.Is(err, crm.ErrCallItemNotFound):
		writeError(w, r, http.StatusNotFound, "CRM_CALL_ITEM_NOT_FOUND", "Bu aday artık listede aranmayı beklemiyor.")
	case errors.Is(err, crm.ErrScriptNotFound):
		writeError(w, r, http.StatusNotFound, "CRM_SCRIPT_NOT_FOUND", "Konuşma metni bulunamadı.")
	case errors.Is(err, crm.ErrOpportunityNotFound):
		writeError(w, r, http.StatusNotFound, "CRM_OPPORTUNITY_NOT_FOUND", "Fırsat bulunamadı.")
	case errors.Is(err, crm.ErrStageNotFound):
		writeError(w, r, http.StatusNotFound, "CRM_STAGE_NOT_FOUND", "Aşama bulunamadı.")
	case errors.Is(err, crm.ErrQuoteNotFound):
		writeError(w, r, http.StatusNotFound, "CRM_QUOTE_NOT_FOUND", "Teklif bulunamadı.")
	case errors.Is(err, crm.ErrQuoteNeedsParty):
		writeError(w, r, http.StatusConflict, "CRM_QUOTE_NEEDS_PARTY", "Teklif hazırlamak için önce adayı cariye dönüştürün.")
	case errors.Is(err, crm.ErrOwnerNotMember):
		writeError(w, r, http.StatusUnprocessableEntity, "CRM_OWNER_NOT_MEMBER", "Seçilen kullanıcı bu firmada çalışmıyor.")
	default:
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "CRM işlemi tamamlanamadı.")
	}
}

func (h crmHandler) convertLead(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PartyID string `json:"party_id"`
	}
	if decodeJSON(r, &body) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "İstek geçersiz.")
		return
	}
	item, err := h.service.ConvertLead(r.Context(), sessionFromRequest(r), chi.URLParam(r, "leadID"), body.PartyID, requestMeta(r))
	if err != nil {
		writeCRMError(w, r, err)
		return
	}
	writeLead(w, http.StatusOK, item)
}

func (h crmHandler) previewImport(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 5<<20+1024)
	if err := r.ParseMultipartForm(5 << 20); err != nil {
		writeError(w, r, http.StatusRequestEntityTooLarge, "FILE_TOO_LARGE", "Dosya en fazla 5 MB olabilir.")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Dosya gereklidir.")
		return
	}
	defer file.Close()
	preview, err := h.service.PreviewImport(sessionFromRequest(r), file, header.Filename, r.FormValue("sheet"))
	if err != nil {
		writeCRMError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

// maxImportBody bounds a lead import request: 5000 rows with full notes fit
// comfortably, while the shared 1 MB request limit does not.
const maxImportBody = 16 << 20

func (h crmHandler) importLeads(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBody))
	decoder.DisallowUnknownFields()
	var input crm.ImportInput
	if err := decoder.Decode(&input); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, r, http.StatusRequestEntityTooLarge, "IMPORT_TOO_LARGE", "Aktarım çok büyük; dosyayı bölerek yükleyin.")
			return
		}
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Aktarım bilgileri geçersiz.")
		return
	}
	result, err := h.service.ImportLeads(r.Context(), sessionFromRequest(r), input, requestMeta(r))
	if err != nil {
		writeCRMError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h crmHandler) listCallLists(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.ListCallLists(r.Context(), sessionFromRequest(r), r.URL.Query().Get("include_inactive") == "true")
	if err != nil {
		writeCRMError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h crmHandler) getCallList(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.GetCallList(r.Context(), sessionFromRequest(r), chi.URLParam(r, "listID"))
	if err != nil {
		writeCRMError(w, r, err)
		return
	}
	writeVersioned(w, http.StatusOK, item.Version, item)
}

func (h crmHandler) createCallList(w http.ResponseWriter, r *http.Request) {
	var input crm.CallListInput
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Liste bilgileri geçersiz.")
		return
	}
	item, err := h.service.CreateCallList(r.Context(), sessionFromRequest(r), input, requestMeta(r))
	if err != nil {
		writeCRMError(w, r, err)
		return
	}
	writeVersioned(w, http.StatusCreated, item.Version, item)
}

func (h crmHandler) updateCallList(w http.ResponseWriter, r *http.Request) {
	version, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Güncelleme için If-Match gereklidir.")
		return
	}
	var input crm.CallListInput
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Liste bilgileri geçersiz.")
		return
	}
	item, err := h.service.UpdateCallList(r.Context(), sessionFromRequest(r), chi.URLParam(r, "listID"), version, input, requestMeta(r))
	if err != nil {
		writeCRMError(w, r, err)
		return
	}
	writeVersioned(w, http.StatusOK, item.Version, item)
}

func (h crmHandler) listItems(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := h.service.ListItems(r.Context(), sessionFromRequest(r), chi.URLParam(r, "listID"), r.URL.Query().Get("status"), limit)
	if err != nil {
		writeCRMError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// leadFilterBody is the lead filter as the call-list screens send it.
type leadFilterBody struct {
	Query       string `json:"q"`
	Priority    string `json:"priority"`
	Status      string `json:"status"`
	Owner       string `json:"owner"`
	Source      string `json:"source"`
	City        string `json:"city"`
	District    string `json:"district"`
	Due         bool   `json:"due"`
	DoNotCall   string `json:"do_not_call"`
	HideClosed  bool   `json:"hide_closed"`
	NeverCalled bool   `json:"never_called"`
	Sort        string `json:"sort"`
}

func (f *leadFilterBody) filter() *crm.LeadFilter {
	if f == nil {
		return nil
	}
	return &crm.LeadFilter{Query: f.Query, Priority: f.Priority, Status: f.Status, Owner: f.Owner, Source: f.Source,
		City: f.City, District: f.District, DueOnly: f.Due, DoNotCall: f.DoNotCall, HideClosed: f.HideClosed,
		NeverCalled: f.NeverCalled}
}

type addItemsBody struct {
	LeadIDs       []string        `json:"lead_ids"`
	Filter        *leadFilterBody `json:"filter"`
	IncludeCalled bool            `json:"include_called"`
	DryRun        bool            `json:"dry_run"`
}

func (h crmHandler) addItems(w http.ResponseWriter, r *http.Request) {
	h.addItemsTo(w, r, chi.URLParam(r, "listID"), false)
}

// previewItems counts what a new list would receive before it is created.
func (h crmHandler) previewItems(w http.ResponseWriter, r *http.Request) {
	h.addItemsTo(w, r, "", true)
}

func (h crmHandler) addItemsTo(w http.ResponseWriter, r *http.Request, listID string, forceDryRun bool) {
	var body addItemsBody
	if decodeJSON(r, &body) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "İstek geçersiz.")
		return
	}
	result, err := h.service.AddLeads(r.Context(), sessionFromRequest(r), listID, crm.AddLeadsInput{
		LeadIDs: body.LeadIDs, Filter: body.Filter.filter(), IncludeCalled: body.IncludeCalled, DryRun: body.DryRun || forceDryRun,
	}, requestMeta(r))
	if err != nil {
		writeCRMError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h crmHandler) archiveCallList(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Archived bool `json:"archived"`
	}
	if decodeJSON(r, &body) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "İstek geçersiz.")
		return
	}
	item, err := h.service.ArchiveCallList(r.Context(), sessionFromRequest(r), chi.URLParam(r, "listID"), body.Archived, requestMeta(r))
	if err != nil {
		writeCRMError(w, r, err)
		return
	}
	writeVersioned(w, http.StatusOK, item.Version, item)
}

func (h crmHandler) deleteCallList(w http.ResponseWriter, r *http.Request) {
	if err := h.service.DeleteCallList(r.Context(), sessionFromRequest(r), chi.URLParam(r, "listID"), requestMeta(r)); err != nil {
		writeCRMError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h crmHandler) removeItems(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ItemIDs []string `json:"item_ids"`
	}
	if decodeJSON(r, &body) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "İstek geçersiz.")
		return
	}
	removed, err := h.service.RemoveItems(r.Context(), sessionFromRequest(r), chi.URLParam(r, "listID"), body.ItemIDs, requestMeta(r))
	if err != nil {
		writeCRMError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"removed": removed})
}

func (h crmHandler) claimNext(w http.ResponseWriter, r *http.Request) {
	next, err := h.service.ClaimNext(r.Context(), sessionFromRequest(r), chi.URLParam(r, "listID"))
	if errors.Is(err, crm.ErrCallItemNotFound) {
		writeJSON(w, http.StatusOK, map[string]any{"empty": true})
		return
	}
	if err != nil {
		writeCRMError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, next)
}

func (h crmHandler) releaseItem(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Skip bool `json:"skip"`
	}
	if decodeJSON(r, &body) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "İstek geçersiz.")
		return
	}
	if err := h.service.ReleaseItem(r.Context(), sessionFromRequest(r), chi.URLParam(r, "listID"), chi.URLParam(r, "itemID"), body.Skip, requestMeta(r)); err != nil {
		writeCRMError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h crmHandler) listScripts(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.ListScripts(r.Context(), sessionFromRequest(r))
	if err != nil {
		writeCRMError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h crmHandler) createScript(w http.ResponseWriter, r *http.Request) {
	var input crm.CallScriptInput
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Metin bilgileri geçersiz.")
		return
	}
	item, err := h.service.CreateScript(r.Context(), sessionFromRequest(r), input, requestMeta(r))
	if err != nil {
		writeCRMError(w, r, err)
		return
	}
	writeVersioned(w, http.StatusCreated, item.Version, item)
}

func (h crmHandler) updateScript(w http.ResponseWriter, r *http.Request) {
	version, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Güncelleme için If-Match gereklidir.")
		return
	}
	var input crm.CallScriptInput
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Metin bilgileri geçersiz.")
		return
	}
	item, err := h.service.UpdateScript(r.Context(), sessionFromRequest(r), chi.URLParam(r, "scriptID"), version, input, requestMeta(r))
	if err != nil {
		writeCRMError(w, r, err)
		return
	}
	writeVersioned(w, http.StatusOK, item.Version, item)
}

func (h crmHandler) listStages(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.ListStages(r.Context(), sessionFromRequest(r))
	if err != nil {
		writeCRMError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h crmHandler) createStage(w http.ResponseWriter, r *http.Request) {
	var input crm.StageInput
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Aşama bilgileri geçersiz.")
		return
	}
	item, err := h.service.CreateStage(r.Context(), sessionFromRequest(r), input, requestMeta(r))
	if err != nil {
		writeCRMError(w, r, err)
		return
	}
	writeVersioned(w, http.StatusCreated, item.Version, item)
}

func (h crmHandler) updateStage(w http.ResponseWriter, r *http.Request) {
	version, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Güncelleme için If-Match gereklidir.")
		return
	}
	var input crm.StageInput
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Aşama bilgileri geçersiz.")
		return
	}
	item, err := h.service.UpdateStage(r.Context(), sessionFromRequest(r), chi.URLParam(r, "stageID"), version, input, requestMeta(r))
	if err != nil {
		writeCRMError(w, r, err)
		return
	}
	writeVersioned(w, http.StatusOK, item.Version, item)
}

func (h crmHandler) listOpportunities(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	limit, _ := strconv.Atoi(query.Get("limit"))
	items, err := h.service.ListOpportunities(r.Context(), sessionFromRequest(r), crm.OpportunityFilter{
		State: query.Get("state"), Owner: query.Get("owner"), PartyID: query.Get("party_id"),
		LeadID: query.Get("lead_id"), Query: query.Get("q"),
	}, limit)
	if err != nil {
		writeCRMError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h crmHandler) getOpportunity(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.GetOpportunity(r.Context(), sessionFromRequest(r), chi.URLParam(r, "opportunityID"))
	if err != nil {
		writeCRMError(w, r, err)
		return
	}
	writeVersioned(w, http.StatusOK, item.Version, item)
}

func (h crmHandler) createOpportunity(w http.ResponseWriter, r *http.Request) {
	var input crm.OpportunityInput
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Fırsat bilgileri geçersiz.")
		return
	}
	item, err := h.service.CreateOpportunity(r.Context(), sessionFromRequest(r), input, requestMeta(r))
	if err != nil {
		writeCRMError(w, r, err)
		return
	}
	writeVersioned(w, http.StatusCreated, item.Version, item)
}

func (h crmHandler) updateOpportunity(w http.ResponseWriter, r *http.Request) {
	version, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Güncelleme için If-Match gereklidir.")
		return
	}
	var input crm.OpportunityInput
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Fırsat bilgileri geçersiz.")
		return
	}
	item, err := h.service.UpdateOpportunity(r.Context(), sessionFromRequest(r), chi.URLParam(r, "opportunityID"), version, input, requestMeta(r))
	if err != nil {
		writeCRMError(w, r, err)
		return
	}
	writeVersioned(w, http.StatusOK, item.Version, item)
}

func (h crmHandler) moveStage(w http.ResponseWriter, r *http.Request) {
	version, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Güncelleme için If-Match gereklidir.")
		return
	}
	var body struct {
		StageID    string `json:"stage_id"`
		LostReason string `json:"lost_reason"`
	}
	if decodeJSON(r, &body) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "İstek geçersiz.")
		return
	}
	item, err := h.service.MoveStage(r.Context(), sessionFromRequest(r), chi.URLParam(r, "opportunityID"), version, body.StageID, body.LostReason, requestMeta(r))
	if err != nil {
		writeCRMError(w, r, err)
		return
	}
	writeVersioned(w, http.StatusOK, item.Version, item)
}

func (h crmHandler) linkQuote(w http.ResponseWriter, r *http.Request) {
	var body struct {
		QuoteID string `json:"quote_id"`
	}
	if decodeJSON(r, &body) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "İstek geçersiz.")
		return
	}
	item, err := h.service.LinkQuote(r.Context(), sessionFromRequest(r), chi.URLParam(r, "opportunityID"), body.QuoteID, requestMeta(r))
	if err != nil {
		writeCRMError(w, r, err)
		return
	}
	writeVersioned(w, http.StatusOK, item.Version, item)
}

func (h crmHandler) listReminders(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.DueReminders(r.Context(), sessionFromRequest(r))
	if err != nil {
		writeCRMError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h crmHandler) markReminders(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs []string `json:"ids"`
	}
	if decodeJSON(r, &body) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "İstek geçersiz.")
		return
	}
	if err := h.service.MarkRemindersNotified(r.Context(), sessionFromRequest(r), body.IDs); err != nil {
		writeCRMError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// report takes from/to as calendar days (to inclusive) in the team's zone and
// defaults to the last 30 days.
func (h crmHandler) report(w http.ResponseWriter, r *http.Request) {
	zone, err := time.LoadLocation("Europe/Istanbul")
	if err != nil {
		zone = time.FixedZone("TRT", 3*60*60)
	}
	today := time.Now().In(zone)
	end := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, zone).AddDate(0, 0, 1)
	start := end.AddDate(0, 0, -30)
	if raw := r.URL.Query().Get("from"); raw != "" {
		parsed, err := time.ParseInLocation("2006-01-02", raw, zone)
		if err != nil {
			writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Başlangıç tarihi geçersiz.")
			return
		}
		start = parsed
	}
	if raw := r.URL.Query().Get("to"); raw != "" {
		parsed, err := time.ParseInLocation("2006-01-02", raw, zone)
		if err != nil {
			writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Bitiş tarihi geçersiz.")
			return
		}
		end = parsed.AddDate(0, 0, 1)
	}
	result, err := h.service.BuildReport(r.Context(), sessionFromRequest(r), crm.ReportFilter{From: start, To: end, UserID: r.URL.Query().Get("user_id")})
	if err != nil {
		writeCRMError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func writeVersioned(w http.ResponseWriter, status int, version int64, payload any) {
	w.Header().Set("ETag", `"`+strconv.FormatInt(version, 10)+`"`)
	writeJSON(w, status, payload)
}
