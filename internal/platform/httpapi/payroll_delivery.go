package httpapi

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/payroll/delivery"
	"github.com/go-chi/chi/v5"
)

type payrollDeliveryHandler struct{ service *delivery.Service }

func mountPayrollDeliveryRoutes(router chi.Router, identityService *identity.Service, service *delivery.Service) {
	auth := identityHandler{service: identityService}
	h := payrollDeliveryHandler{service: service}
	read := router.With(auth.requireSession)
	write := router.With(auth.requireSession, auth.requireCSRF)

	read.Get("/api/v1/hr/payroll-runs/{runID}/payslips", h.listPayslips)
	write.Post("/api/v1/hr/payroll-runs/{runID}/payslips", h.generatePayslips)
	write.Post("/api/v1/hr/employee-payrolls/{employeePayrollID}/payslip", h.generatePayslip)
	read.Get("/api/v1/hr/payslips/{payslipID}/download", h.downloadPayslip)

	write.Post("/api/v1/hr/payroll-runs/{runID}/exports", h.createExport)
	read.Get("/api/v1/hr/payroll-exports/{exportID}/download", h.downloadExport)

	read.Get("/api/v1/hr/payroll-runs/{runID}/email-preview", h.emailPreview)
	write.Post("/api/v1/hr/payroll-runs/{runID}/email-batches", h.sendEmail)
}

func (h payrollDeliveryHandler) listPayslips(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.ListPayslips(r.Context(), sessionFromRequest(r), chi.URLParam(r, "runID"))
	if err != nil {
		writePayrollDeliveryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h payrollDeliveryHandler) generatePayslips(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.GeneratePayslipsForRun(r.Context(), sessionFromRequest(r), chi.URLParam(r, "runID"), requestMeta(r))
	if err != nil {
		writePayrollDeliveryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h payrollDeliveryHandler) generatePayslip(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.GeneratePayslip(r.Context(), sessionFromRequest(r), chi.URLParam(r, "employeePayrollID"), requestMeta(r))
	if err != nil {
		writePayrollDeliveryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h payrollDeliveryHandler) downloadPayslip(w http.ResponseWriter, r *http.Request) {
	reader, info, filename, err := h.service.DownloadPayslip(r.Context(), sessionFromRequest(r), chi.URLParam(r, "payslipID"))
	if err != nil {
		writePayrollDeliveryError(w, r, err)
		return
	}
	defer reader.Close()
	streamFile(w, reader, info.ContentType, "application/pdf", info.Size, filename)
}

func (h payrollDeliveryHandler) createExport(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ExportType string `json:"export_type"`
	}
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Dışa aktarma türü geçersiz.")
		return
	}
	item, err := h.service.CreateExport(r.Context(), sessionFromRequest(r), chi.URLParam(r, "runID"), input.ExportType, requestMeta(r))
	if err != nil {
		writePayrollDeliveryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (h payrollDeliveryHandler) downloadExport(w http.ResponseWriter, r *http.Request) {
	reader, info, filename, err := h.service.DownloadExport(r.Context(), sessionFromRequest(r), chi.URLParam(r, "exportID"))
	if err != nil {
		writePayrollDeliveryError(w, r, err)
		return
	}
	defer reader.Close()
	streamFile(w, reader, info.ContentType, "application/octet-stream", info.Size, filename)
}

func (h payrollDeliveryHandler) emailPreview(w http.ResponseWriter, r *http.Request) {
	preview, err := h.service.PreviewEmail(r.Context(), sessionFromRequest(r), chi.URLParam(r, "runID"))
	if err != nil {
		writePayrollDeliveryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, previewJSON(preview))
}

func (h payrollDeliveryHandler) sendEmail(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Resend  bool   `json:"resend"`
		Subject string `json:"subject"`
		Body    string `json:"body"`
	}
	_ = decodeJSON(r, &input)
	result, err := h.service.SendEmailBatch(r.Context(), sessionFromRequest(r), chi.URLParam(r, "runID"), input.Resend, input.Subject, input.Body, requestMeta(r))
	if err != nil {
		writePayrollDeliveryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func previewJSON(p delivery.EmailPreview) map[string]any {
	orEmptyStr := func(v []string) []string {
		if v == nil {
			return []string{}
		}
		return v
	}
	recipients := p.Recipients
	if recipients == nil {
		recipients = []delivery.EmailRecipientRow{}
	}
	return map[string]any{
		"ready":           orEmptyStr(p.Ready),
		"missing":         orEmptyStr(p.Missing),
		"invalid":         orEmptyStr(p.Invalid),
		"duplicate":       orEmptyStr(p.Duplicate),
		"missing_payslip": orEmptyStr(p.MissingPayslip),
		"already_sent":    orEmptyStr(p.AlreadySent),
		"recipients":      recipients,
		"default_subject": p.DefaultSubject,
		"default_body":    p.DefaultBody,
		"variables":       p.Variables,
	}
}

func streamFile(w http.ResponseWriter, reader io.Reader, contentType, fallbackType string, size int64, filename string) {
	if contentType == "" {
		contentType = fallbackType
	}
	w.Header().Set("Content-Type", contentType)
	if size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	}
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, reader)
}

func writePayrollDeliveryError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, identity.ErrForbidden):
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu işlem için yetkiniz yok.")
	case errors.Is(err, identity.ErrValidation):
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", err.Error())
	case errors.Is(err, delivery.ErrPayslipNotFound):
		writeError(w, r, http.StatusNotFound, "PAYSLIP_NOT_FOUND", "Ücret pusulası bulunamadı.")
	case errors.Is(err, delivery.ErrExportNotFound):
		writeError(w, r, http.StatusNotFound, "PAYROLL_EXPORT_NOT_FOUND", "Dışa aktarma bulunamadı.")
	case errors.Is(err, delivery.ErrPayrollNotFinalized), errors.Is(err, delivery.ErrRunNotFinalized):
		writeError(w, r, http.StatusUnprocessableEntity, "PAYROLL_NOT_FINALIZED", "Bordro kesinleşmeden pusula/teslim işlemi yapılamaz.")
	case errors.Is(err, delivery.ErrSMTPNotConfigured):
		writeError(w, r, http.StatusUnprocessableEntity, "SMTP_SETTINGS_NOT_FOUND", "E-posta gönderimi için SMTP ayarları yapılmamış.")
	case errors.Is(err, delivery.ErrNothingToSend):
		writeError(w, r, http.StatusUnprocessableEntity, "PAYROLL_EMAIL_NOTHING_TO_SEND", "Gönderilecek uygun alıcı yok.")
	default:
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "İşlem tamamlanamadı.")
	}
}
