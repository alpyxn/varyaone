package httpapi

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/alpyxn/varyaone/internal/dataexchange"
	"github.com/alpyxn/varyaone/internal/identity"
	dataimports "github.com/alpyxn/varyaone/internal/imports"
	"github.com/go-chi/chi/v5"
)

type dataExchangeHandler struct{ service *dataimports.Service }

func mountDataExchangeRoutes(router chi.Router, identityService *identity.Service, service *dataimports.Service) {
	auth := identityHandler{service: identityService}
	h := dataExchangeHandler{service: service}
	read := router.With(auth.requireSession)
	read.Get("/api/v1/imports/capabilities", h.capabilities)
	read.Get("/api/v1/imports", h.listImports)
	read.Get("/api/v1/imports/{importID}", h.getImport)
	read.Get("/api/v1/imports/{importID}/rows", h.listImportRows)
	read.Get("/api/v1/imports/{importID}/errors", h.listImportErrors)
	read.Get("/api/v1/exports/{exportID}", h.getExport)
	read.Get("/api/v1/exports/{exportID}/download", h.downloadExport)
	write := router.With(auth.requireSession, auth.requireCSRF)
	write.Post("/api/v1/imports", h.createImport)
	write.Post("/api/v1/imports/{importID}/analyze", h.analyzeImport)
	write.Post("/api/v1/imports/{importID}/commit", h.commitImport)
	write.Post("/api/v1/exports", h.createExport)
}

func (h dataExchangeHandler) allowed(r *http.Request) bool {
	session := sessionFromRequest(r)
	return session.HasPermission("inventory.read") || session.HasPermission("product.read") || session.HasPermission("pricing.read") || session.HasPermission("organization.warehouse.manage") || session.HasPermission("party.read") || session.HasPermission("party.create") || session.HasPermission("party.edit")
}

func (h dataExchangeHandler) allowedForCountFlow(r *http.Request) bool {
	return h.allowed(r) || sessionFromRequest(r).HasPermission("inventory.count.post")
}

func (h dataExchangeHandler) allowedEntity(r *http.Request, entity string, write bool) bool {
	session := sessionFromRequest(r)
	entity = strings.ToUpper(strings.TrimSpace(entity))
	spec, known := importEntitySpec(entity)
	if !known {
		return false
	}
	if !write {
		if !spec.Exportable {
			return false
		}
		return h.readAllowedEntity(session, entity)
	}
	if !spec.Importable {
		return false
	}
	switch entity {
	case "PRODUCT", "VARIANT":
		return session.HasPermission("product.create") || session.HasPermission("product.edit")
	case "WAREHOUSE":
		return session.HasPermission("organization.warehouse.manage") || session.HasPermission("inventory.warehouse.manage")
	case "PRICE_LIST":
		return session.HasPermission("pricing.manage")
	case "OPENING_STOCK":
		return session.HasPermission("inventory.movement.post")
	case "STOCK_COUNT":
		return session.HasPermission("inventory.count.post")
	case "PARTY":
		return session.HasPermission("party.create") || session.HasPermission("party.edit")
	default:
		return false
	}
}

func (h dataExchangeHandler) readAllowedEntity(session identity.Session, entity string) bool {
	switch entity {
	case "PRODUCT", "VARIANT", "BARCODE":
		return session.HasPermission("product.read")
	case "WAREHOUSE", "OPENING_STOCK":
		return session.HasPermission("inventory.read")
	case "STOCK_COUNT":
		return session.HasPermission("inventory.read") || session.HasPermission("inventory.count.post")
	case "PRICE_LIST":
		return session.HasPermission("pricing.read") || session.HasPermission("pricing.manage")
	case "PARTY":
		return session.HasPermission("party.read")
	default:
		return false
	}
}

func (h dataExchangeHandler) allowedImportJob(r *http.Request, entity string) bool {
	return h.readAllowedEntity(sessionFromRequest(r), entity) || h.allowedEntity(r, entity, true)
}

func importEntitySpec(entity string) (dataexchange.EntitySpec, bool) {
	entity = strings.ToUpper(strings.TrimSpace(entity))
	for _, spec := range dataexchange.InitialEntitySpecs() {
		if string(spec.Type) == entity {
			return spec, true
		}
	}
	return dataexchange.EntitySpec{}, false
}

func (h dataExchangeHandler) capabilities(w http.ResponseWriter, r *http.Request) {
	if !h.allowed(r) {
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu aktarım için yetkiniz yok.")
		return
	}
	type field struct {
		Name     string                 `json:"name"`
		Label    string                 `json:"label"`
		Type     dataexchange.FieldType `json:"type"`
		Required bool                   `json:"required"`
		Example  string                 `json:"example,omitempty"`
	}
	type entity struct {
		Type       string  `json:"type"`
		Label      string  `json:"label"`
		Importable bool    `json:"importable"`
		Exportable bool    `json:"exportable"`
		Fields     []field `json:"fields"`
	}
	items := make([]entity, 0)
	for _, spec := range dataexchange.InitialEntitySpecs() {
		canImport := spec.Importable && h.allowedEntity(r, string(spec.Type), true)
		canExport := spec.Exportable && h.allowedEntity(r, string(spec.Type), false)
		if !canImport && !canExport {
			continue
		}
		fields := make([]field, 0, len(spec.Fields))
		for _, item := range spec.Fields {
			fields = append(fields, field{Name: item.Name, Label: item.Label, Type: item.Type, Required: item.Required, Example: item.Example})
		}
		items = append(items, entity{Type: string(spec.Type), Label: spec.Label, Importable: canImport, Exportable: canExport, Fields: fields})
	}
	writeJSON(w, http.StatusOK, map[string]any{"max_upload_bytes": 64 << 20, "entities": items})
}

func (h dataExchangeHandler) createImport(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, (64<<20)+1024)
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Dosya yüklenemedi.")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Dosya seçilmelidir.")
		return
	}
	defer file.Close()
	session := sessionFromRequest(r)
	if !h.allowedEntity(r, r.FormValue("entity_type"), true) {
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu aktarım için yetkiniz yok.")
		return
	}
	job, err := h.service.Upload(r.Context(), dataimports.UploadInput{CompanyID: session.CurrentCompanyID, ActorUserID: session.User.ID, EntityType: r.FormValue("entity_type"), TargetID: r.FormValue("target_id"), CommitMode: r.FormValue("commit_mode"), Filename: header.Filename, ContentType: header.Header.Get("Content-Type"), Source: file})
	if err != nil {
		writeError(w, r, http.StatusUnprocessableEntity, "IMPORT_UPLOAD_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, job)
}

func (h dataExchangeHandler) getImport(w http.ResponseWriter, r *http.Request) {
	if !h.allowedForCountFlow(r) {
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu aktarım için yetkiniz yok.")
		return
	}
	job, err := h.service.Get(r.Context(), sessionFromRequest(r).CurrentCompanyID, chi.URLParam(r, "importID"))
	if err != nil {
		writeInventoryError(w, r, err, "Aktarım okunamadı.")
		return
	}
	if !h.allowedImportJob(r, job.EntityType) {
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu aktarım için yetkiniz yok.")
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (h dataExchangeHandler) listImports(w http.ResponseWriter, r *http.Request) {
	if !h.allowed(r) {
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu aktarım için yetkiniz yok.")
		return
	}
	items, err := h.service.List(r.Context(), sessionFromRequest(r).CurrentCompanyID, queryLimit(r, 50, 100))
	if err != nil {
		writeInventoryError(w, r, err, "Aktarım geçmişi okunamadı.")
		return
	}
	visible := make([]dataimports.Job, 0, len(items))
	for _, item := range items {
		if h.allowedImportJob(r, item.EntityType) {
			visible = append(visible, item)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": visible})
}

func (h dataExchangeHandler) analyzeImport(w http.ResponseWriter, r *http.Request) {
	if !h.allowedForCountFlow(r) {
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu aktarım için yetkiniz yok.")
		return
	}
	job, err := h.service.Get(r.Context(), sessionFromRequest(r).CurrentCompanyID, chi.URLParam(r, "importID"))
	if err != nil {
		writeInventoryError(w, r, err, "Aktarım okunamadı.")
		return
	}
	if !h.allowedImportJob(r, job.EntityType) {
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu aktarım için yetkiniz yok.")
		return
	}
	var input dataimports.AnalyzeInput
	if err := decodeJSON(r, &input); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Kolon eşleme bilgisi geçersiz.")
		return
	}
	input.ActorUserID = sessionFromRequest(r).User.ID
	input.AllowOpeningStock = sessionFromRequest(r).HasPermission("inventory.movement.post")
	result, err := h.service.Analyze(r.Context(), sessionFromRequest(r).CurrentCompanyID, chi.URLParam(r, "importID"), input)
	if err != nil {
		writeError(w, r, http.StatusUnprocessableEntity, "IMPORT_ANALYZE_FAILED", "Dosya analiz edilemedi.")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h dataExchangeHandler) listImportRows(w http.ResponseWriter, r *http.Request) {
	if !h.allowedForCountFlow(r) {
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu aktarım için yetkiniz yok.")
		return
	}
	job, err := h.service.Get(r.Context(), sessionFromRequest(r).CurrentCompanyID, chi.URLParam(r, "importID"))
	if err != nil {
		writeInventoryError(w, r, err, "Aktarım okunamadı.")
		return
	}
	if !h.allowedImportJob(r, job.EntityType) {
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu aktarım için yetkiniz yok.")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	rows, err := h.service.Rows(r.Context(), sessionFromRequest(r).CurrentCompanyID, chi.URLParam(r, "importID"), limit, offset)
	if err != nil {
		writeInventoryError(w, r, err, "Aktarım satırları okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rows})
}

func (h dataExchangeHandler) listImportErrors(w http.ResponseWriter, r *http.Request) {
	if !h.allowedForCountFlow(r) {
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu aktarım için yetkiniz yok.")
		return
	}
	job, err := h.service.Get(r.Context(), sessionFromRequest(r).CurrentCompanyID, chi.URLParam(r, "importID"))
	if err != nil {
		writeInventoryError(w, r, err, "Aktarım okunamadı.")
		return
	}
	if !h.allowedImportJob(r, job.EntityType) {
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu aktarım için yetkiniz yok.")
		return
	}
	format := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("format")))
	if format == "CSV" || format == "XLSX" {
		table, err := h.service.ErrorReportTable(r.Context(), sessionFromRequest(r).CurrentCompanyID, chi.URLParam(r, "importID"))
		if err != nil {
			writeInventoryError(w, r, err, "Hata raporu oluşturulamadı.")
			return
		}
		var payload bytes.Buffer
		if format == "CSV" {
			err = dataexchange.WriteCSV(&payload, table)
		} else {
			err = dataexchange.WriteXLSX(&payload, table)
		}
		if err != nil {
			writeInventoryError(w, r, err, "Hata raporu oluşturulamadı.")
			return
		}
		if format == "CSV" {
			w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		} else {
			w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
		}
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="aktarim-hatalari-%s.%s"`, chi.URLParam(r, "importID"), strings.ToLower(format)))
		_, _ = w.Write(payload.Bytes())
		return
	}
	rows, err := h.service.Errors(r.Context(), sessionFromRequest(r).CurrentCompanyID, chi.URLParam(r, "importID"))
	if err != nil {
		writeInventoryError(w, r, err, "Aktarım hataları okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rows})
}

func (h dataExchangeHandler) commitImport(w http.ResponseWriter, r *http.Request) {
	if !h.allowedForCountFlow(r) {
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu aktarım için yetkiniz yok.")
		return
	}
	job, err := h.service.Get(r.Context(), sessionFromRequest(r).CurrentCompanyID, chi.URLParam(r, "importID"))
	if err != nil {
		writeInventoryError(w, r, err, "Aktarım okunamadı.")
		return
	}
	if !h.allowedEntity(r, job.EntityType, true) {
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu aktarım için yetkiniz yok.")
		return
	}
	var body struct {
		DryRun           bool   `json:"dry_run"`
		AnalysisRevision string `json:"analysis_revision"`
	}
	if err := decodeJSON(r, &body); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Aktarım işlem bilgileri geçersiz.")
		return
	}
	if body.DryRun {
		result, err := h.service.Analyze(r.Context(), sessionFromRequest(r).CurrentCompanyID, chi.URLParam(r, "importID"), dataimports.AnalyzeInput{DryRun: true})
		if err != nil {
			writeError(w, r, http.StatusUnprocessableEntity, "IMPORT_DRY_RUN_FAILED", "Önizleme çalıştırılamadı.")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"dry_run": true, "analysis": result})
		return
	}
	job, err = h.service.Commit(r.Context(), sessionFromRequest(r).CurrentCompanyID, sessionFromRequest(r).User.ID, chi.URLParam(r, "importID"), sessionFromRequest(r).HasPermission("inventory.movement.post"), body.AnalysisRevision)
	if err != nil {
		if errors.Is(err, dataimports.ErrOpeningStockNotAuthorized) {
			writeError(w, r, http.StatusForbidden, dataimports.ErrOpeningStockNotAuthorized.Error(), "Açılış stoğu aktarmak için stok hareketi yetkiniz yok.")
			return
		}
		if errors.Is(err, dataimports.ErrOpeningStockExistingProduct) {
			writeError(w, r, http.StatusUnprocessableEntity, dataimports.ErrOpeningStockExistingProduct.Error(), "Mevcut ürüne açılış stoğu eklenemez.")
			return
		}
		if errors.Is(err, dataimports.ErrIdentityConflict) {
			writeError(w, r, http.StatusUnprocessableEntity, dataimports.ErrIdentityConflict.Error(), "Aktarım satırı mevcut bir ürün, varyant, barkod veya depo kaydıyla çakışıyor.")
			return
		}
		if errors.Is(err, dataimports.ErrNotReady) {
			writeError(w, r, http.StatusUnprocessableEntity, "IMPORT_NOT_READY", "Aktarımı tamamlamadan önce dosyayı yeniden kontrol edin.")
			return
		}
		if errors.Is(err, dataimports.ErrStalePreview) {
			writeError(w, r, http.StatusConflict, "IMPORT_PREVIEW_STALE", "Önizleme güncel değil; dosyayı yeniden analiz edin.")
			return
		}
		if errors.Is(err, dataimports.ErrCommitInProgress) {
			writeError(w, r, http.StatusConflict, "IMPORT_COMMIT_IN_PROGRESS", "Aktarımın mevcut durumunu kontrol edin.")
			return
		}
		writeError(w, r, http.StatusUnprocessableEntity, "IMPORT_COMMIT_FAILED", "Aktarım atomik olarak tamamlanamadı.")
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (h dataExchangeHandler) createExport(w http.ResponseWriter, r *http.Request) {
	if !h.allowedForCountFlow(r) {
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu aktarım için yetkiniz yok.")
		return
	}
	var body struct {
		EntityType string `json:"entity_type"`
		TargetID   string `json:"target_id"`
		Format     string `json:"format"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Dışa aktarım bilgileri geçersiz.")
		return
	}
	if !h.allowedEntity(r, body.EntityType, false) {
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu aktarım için yetkiniz yok.")
		return
	}
	if strings.EqualFold(body.EntityType, "STOCK_COUNT") && strings.TrimSpace(body.TargetID) == "" {
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Stok sayımı dışa aktarımı için sayım kimliği zorunludur.")
		return
	}
	session := sessionFromRequest(r)
	job, err := h.service.CreateExport(r.Context(), session.CurrentCompanyID, session.User.ID, body.EntityType, body.TargetID, body.Format)
	if err != nil {
		writeInventoryError(w, r, err, "Dışa aktarım oluşturulamadı.")
		return
	}
	writeJSON(w, http.StatusCreated, job)
}

func (h dataExchangeHandler) getExport(w http.ResponseWriter, r *http.Request) {
	if !h.allowedForCountFlow(r) {
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu aktarım için yetkiniz yok.")
		return
	}
	job, err := h.service.GetExport(r.Context(), sessionFromRequest(r).CurrentCompanyID, chi.URLParam(r, "exportID"))
	if err != nil {
		writeInventoryError(w, r, err, "Dışa aktarım okunamadı.")
		return
	}
	if !h.allowedEntity(r, job.EntityType, false) {
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu aktarım için yetkiniz yok.")
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (h dataExchangeHandler) downloadExport(w http.ResponseWriter, r *http.Request) {
	if !h.allowedForCountFlow(r) {
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu aktarım için yetkiniz yok.")
		return
	}
	job, err := h.service.GetExport(r.Context(), sessionFromRequest(r).CurrentCompanyID, chi.URLParam(r, "exportID"))
	if err != nil {
		writeInventoryError(w, r, err, "Dosya indirilemedi.")
		return
	}
	if !h.allowedEntity(r, job.EntityType, false) {
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu aktarım için yetkiniz yok.")
		return
	}
	reader, job, contentType, err := h.service.OpenExport(r.Context(), sessionFromRequest(r).CurrentCompanyID, chi.URLParam(r, "exportID"))
	if err != nil {
		writeInventoryError(w, r, err, "Dosya indirilemedi.")
		return
	}
	defer reader.Close()
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, job.Filename))
	_, _ = io.Copy(w, reader)
}
