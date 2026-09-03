package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/alpyxn/varyaone/internal/finance"
	"github.com/alpyxn/varyaone/internal/hr/employment"
	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/money"
	"github.com/alpyxn/varyaone/internal/payroll/calculation"
	"github.com/alpyxn/varyaone/internal/payroll/legislation"
	payrollpayment "github.com/alpyxn/varyaone/internal/payroll/payment"
	payrollrun "github.com/alpyxn/varyaone/internal/payroll/run"
	"github.com/go-chi/chi/v5"
)

// ---- Legislation ----

type payrollLegislationHandler struct {
	service    *legislation.Service
	employment *employment.Service
}

func mountPayrollLegislationRoutes(router chi.Router, identityService *identity.Service, service *legislation.Service, employmentService *employment.Service) {
	auth := identityHandler{service: identityService}
	h := payrollLegislationHandler{service: service, employment: employmentService}
	read := router.With(auth.requireSession)
	write := router.With(auth.requireSession, auth.requireCSRF)
	read.Get("/api/v1/hr/legislation-packs", h.list)
	read.Get("/api/v1/hr/legislation-packs/{packID}", h.get)
	read.Get("/api/v1/hr/legislation-packs/{packID}/manual-components", h.manualComponents)
	write.Post("/api/v1/hr/legislation-packs", h.createDraft)
	write.Post("/api/v1/hr/legislation-packs/{packID}/activate", h.activate)
	write.Post("/api/v1/hr/minimum-wage", h.replaceMinimumWage)
	read.Get("/api/v1/hr/payroll-settings", h.getPayrollSettings)
	write.Put("/api/v1/hr/payroll-settings", h.putPayrollSettings)
}

func (h payrollLegislationHandler) getPayrollSettings(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.GetPayrollSettings(r.Context(), sessionFromRequest(r))
	if err != nil {
		writePayrollLegislationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h payrollLegislationHandler) putPayrollSettings(w http.ResponseWriter, r *http.Request) {
	var input struct {
		DefaultContributionSchemeCode string `json:"default_contribution_scheme_code"`
	}
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Bordro ayarları geçersiz.")
		return
	}
	item, err := h.service.SetDefaultContributionScheme(r.Context(), sessionFromRequest(r), input.DefaultContributionSchemeCode)
	if err != nil {
		writePayrollLegislationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h payrollLegislationHandler) list(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.ListPacks(r.Context(), sessionFromRequest(r))
	if err != nil {
		writePayrollLegislationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h payrollLegislationHandler) get(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.GetPack(r.Context(), sessionFromRequest(r), chi.URLParam(r, "packID"))
	if err != nil {
		writePayrollLegislationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h payrollLegislationHandler) manualComponents(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.ManualComponents(r.Context(), sessionFromRequest(r), chi.URLParam(r, "packID"))
	if err != nil {
		writePayrollLegislationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h payrollLegislationHandler) createDraft(w http.ResponseWriter, r *http.Request) {
	var input legislation.DraftPackInput
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Mevzuat paketi bilgileri geçersiz.")
		return
	}
	item, err := h.service.CreateDraft(r.Context(), sessionFromRequest(r), input, requestMeta(r))
	if err != nil {
		writePayrollLegislationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (h payrollLegislationHandler) activate(w http.ResponseWriter, r *http.Request) {
	session := sessionFromRequest(r)
	item, err := h.service.Activate(r.Context(), session, chi.URLParam(r, "packID"), requestMeta(r))
	if err != nil {
		writePayrollLegislationError(w, r, err)
		return
	}
	// Asgari ücretli olarak işaretlenmiş çalışanların ücretini otomatik olarak
	// yeni pakete taşı. Bu adım paket aktivasyonunun bir parçası olarak
	// görünür, ama ayrı bir işlemdir: hata olursa paket aktif kalır, sadece
	// ücret güncellemesi uyarı olarak dönülür.
	warning := ""
	if h.employment != nil {
		today := time.Now().UTC().Format("2006-01-02")
		if _, applyErr := h.employment.ApplyMinimumWageChange(r.Context(), session.CurrentCompanyID, today, item.MinimumMonthlyGross, session.User.ID, requestMeta(r)); applyErr != nil {
			warning = "Paket aktifleşti ama asgari ücretli çalışanların ücreti otomatik güncellenemedi: " + applyErr.Error()
		}
	}
	if warning != "" {
		writeJSON(w, http.StatusOK, map[string]any{"pack": item, "warning": warning})
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h payrollLegislationHandler) replaceMinimumWage(w http.ResponseWriter, r *http.Request) {
	var input legislation.MinimumWageInput
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Asgari ücret bilgileri geçersiz.")
		return
	}
	session := sessionFromRequest(r)
	item, err := h.service.ReplaceMinimumWage(r.Context(), session, input, requestMeta(r))
	if err != nil {
		writePayrollLegislationError(w, r, err)
		return
	}
	warning := ""
	if h.employment != nil {
		today := time.Now().UTC().Format("2006-01-02")
		if _, applyErr := h.employment.ApplyMinimumWageChange(r.Context(), session.CurrentCompanyID, today, item.MinimumMonthlyGross, session.User.ID, requestMeta(r)); applyErr != nil {
			warning = "Asgari ücret güncellendi ama asgari ücretli çalışanların ücreti otomatik güncellenemedi: " + applyErr.Error()
		}
	}
	if warning != "" {
		writeJSON(w, http.StatusOK, map[string]any{"pack": item, "warning": warning})
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func writePayrollLegislationError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, identity.ErrForbidden):
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu işlem için yetkiniz yok.")
	case errors.Is(err, identity.ErrValidation):
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", err.Error())
	case errors.Is(err, legislation.ErrNotFound):
		writeError(w, r, http.StatusNotFound, "PAYROLL_LEGISLATION_PACK_NOT_FOUND", "Mevzuat paketi bulunamadı.")
	case errors.Is(err, legislation.ErrNotDraft):
		writeError(w, r, http.StatusConflict, "PAYROLL_LEGISLATION_PACK_NOT_DRAFT", "Yalnızca taslak paket düzenlenebilir.")
	case errors.Is(err, legislation.ErrImmutable):
		writeError(w, r, http.StatusConflict, "PAYROLL_LEGISLATION_PACK_IMMUTABLE", "Aktif veya geçmiş mevzuat paketi değiştirilemez.")
	case errors.Is(err, legislation.ErrOverlap):
		writeError(w, r, http.StatusConflict, "PAYROLL_LEGISLATION_ACTIVE_OVERLAP", "Aktif mevzuat aralığı çakışıyor.")
	default:
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "İşlem tamamlanamadı.")
	}
}

// ---- Wage preview (gross↔net calculator) ----

type payrollWagePreviewHandler struct{ repo *legislation.Repository }

func mountPayrollWagePreviewRoutes(router chi.Router, identityService *identity.Service, repo *legislation.Repository) {
	auth := identityHandler{service: identityService}
	h := payrollWagePreviewHandler{repo: repo}
	read := router.With(auth.requireSession)
	read.Get("/api/v1/hr/payroll/wage-preview", h.preview)
	read.Get("/api/v1/hr/payroll/minimum-wage", h.minimumWage)
}

type wagePreviewResponse struct {
	Gross                string `json:"gross"`
	Net                  string `json:"net"`
	EmployeeSGK          string `json:"employee_sgk"`
	EmployeeUnemployment string `json:"employee_unemployment"`
	IncomeTax            string `json:"income_tax"`
	StampTax             string `json:"stamp_tax"`
	PackID               string `json:"pack_id"`
	PackCode             string `json:"pack_code"`
	EffectiveFrom        string `json:"effective_from"`
	EffectiveTo          string `json:"effective_to"`
}

// resolvePack loads the active legislation pack and a contribution scheme
// (falling back to NO_DISCOUNT) for the given date query params.
func (h payrollWagePreviewHandler) resolvePack(r *http.Request) (*legislation.Pack, *legislation.ContributionScheme, error) {
	session := sessionFromRequest(r)
	date := time.Now().UTC()
	if raw := r.URL.Query().Get("date"); raw != "" {
		if parsed, err := time.Parse("2006-01-02", raw); err == nil {
			date = parsed
		}
	}
	pack, err := h.repo.ActivePack(r.Context(), session.CurrentCompanyID, date)
	if err != nil {
		return nil, nil, err
	}
	schemeCode := r.URL.Query().Get("scheme")
	if schemeCode == "" {
		schemeCode = "NO_DISCOUNT"
	}
	scheme, err := h.repo.ContributionScheme(r.Context(), session.CurrentCompanyID, pack.ID, schemeCode)
	if err != nil {
		return nil, nil, err
	}
	return pack, scheme, nil
}

func (h payrollWagePreviewHandler) preview(w http.ResponseWriter, r *http.Request) {
	if !sessionFromRequest(r).HasPermission("hr.payroll.read") {
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu işlem için yetkiniz yok.")
		return
	}
	pack, scheme, err := h.resolvePack(r)
	if err != nil {
		writePayrollWagePreviewError(w, r, err)
		return
	}
	mode := "GROSS"
	if r.URL.Query().Get("mode") == "net" {
		mode = "NET"
	}
	amount, err := money.ParseDecimal(r.URL.Query().Get("amount"), 12)
	if err != nil {
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Tutar geçersiz.")
		return
	}
	result, err := (calculation.PayrollCalculator{}).Preview(calculation.Context{
		Pack: pack, ContributionScheme: scheme,
	}, mode, amount)
	if err != nil {
		writePayrollWagePreviewError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, wagePreviewResponse{
		Gross: result.Gross.String(), Net: result.Net.String(),
		EmployeeSGK: result.EmployeeSGK.String(), EmployeeUnemployment: result.EmployeeUnemployment.String(),
		IncomeTax: result.IncomeTax.String(), StampTax: result.StampTax.String(),
		PackID: pack.ID, PackCode: pack.Code,
		EffectiveFrom: pack.EffectiveFrom.Format("2006-01-02"), EffectiveTo: pack.EffectiveTo.Format("2006-01-02"),
	})
}

func (h payrollWagePreviewHandler) minimumWage(w http.ResponseWriter, r *http.Request) {
	if !sessionFromRequest(r).HasPermission("hr.payroll.read") {
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu işlem için yetkiniz yok.")
		return
	}
	pack, scheme, err := h.resolvePack(r)
	if err != nil {
		writePayrollWagePreviewError(w, r, err)
		return
	}
	result, err := (calculation.PayrollCalculator{}).Preview(calculation.Context{
		Pack: pack, ContributionScheme: scheme,
	}, "GROSS", pack.MinimumMonthlyGross)
	if err != nil {
		writePayrollWagePreviewError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, wagePreviewResponse{
		Gross: result.Gross.String(), Net: result.Net.String(),
		EmployeeSGK: result.EmployeeSGK.String(), EmployeeUnemployment: result.EmployeeUnemployment.String(),
		IncomeTax: result.IncomeTax.String(), StampTax: result.StampTax.String(),
		PackID: pack.ID, PackCode: pack.Code,
		EffectiveFrom: pack.EffectiveFrom.Format("2006-01-02"), EffectiveTo: pack.EffectiveTo.Format("2006-01-02"),
	})
}

func writePayrollWagePreviewError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, identity.ErrForbidden):
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu işlem için yetkiniz yok.")
	case errors.Is(err, legislation.ErrPackNotFound):
		writeError(w, r, http.StatusUnprocessableEntity, "PAYROLL_LEGISLATION_NOT_FOUND", "Bu tarih için aktif bordro mevzuatı yok.")
	case isCalculationError(err):
		// Any calculator-domain failure (population out of scope, target net too
		// low to be feasible, reconciliation, missing legislation, …) is caused by
		// the requested figures, not a server fault.
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", calculationMessage(err))
	default:
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "İşlem tamamlanamadı.")
	}
}

func isCalculationError(err error) bool {
	var target *calculation.CalculationError
	return errors.As(err, &target)
}

func calculationMessage(err error) string {
	var target *calculation.CalculationError
	if errors.As(err, &target) {
		if target.Code == calculation.ErrNegativeNet {
			return "Girilen net ücret çok düşük; SGK tabanı ve kesintiler bu tutarı karşılamıyor."
		}
		return target.Message
	}
	return "Ücret önizlemesi hesaplanamadı."
}

// ---- Payroll runs ----

type payrollRunHandler struct{ service *payrollrun.Service }

func mountPayrollRunRoutes(router chi.Router, identityService *identity.Service, service *payrollrun.Service) {
	auth := identityHandler{service: identityService}
	h := payrollRunHandler{service: service}
	read := router.With(auth.requireSession)
	write := router.With(auth.requireSession, auth.requireCSRF)
	read.Get("/api/v1/hr/payroll-runs", h.list)
	read.Get("/api/v1/hr/payroll-runs/{runID}", h.get)
	write.Post("/api/v1/hr/payroll-runs", h.create)
	write.Post("/api/v1/hr/payroll-runs/{runID}/calculate", h.calculate)
	write.Post("/api/v1/hr/payroll-runs/{runID}/finalize", h.finalize)
	read.Get("/api/v1/hr/payroll-runs/{runID}/manual-components", h.listManual)
	write.Post("/api/v1/hr/payroll-runs/{runID}/manual-components", h.addManual)
	write.Delete("/api/v1/hr/payroll-runs/{runID}/manual-components/{componentID}", h.archiveManual)
}

func (h payrollRunHandler) list(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.List(r.Context(), sessionFromRequest(r))
	if err != nil {
		writePayrollRunError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h payrollRunHandler) get(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.Get(r.Context(), sessionFromRequest(r), chi.URLParam(r, "runID"))
	if err != nil {
		writePayrollRunError(w, r, err)
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusOK, item)
}

func (h payrollRunHandler) create(w http.ResponseWriter, r *http.Request) {
	var input payrollrun.RunInput
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Bordro run bilgileri geçersiz.")
		return
	}
	item, err := h.service.Create(r.Context(), sessionFromRequest(r), input, requestMeta(r))
	if err != nil {
		writePayrollRunError(w, r, err)
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusCreated, item)
}

func (h payrollRunHandler) calculate(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.Calculate(r.Context(), sessionFromRequest(r), chi.URLParam(r, "runID"), requestMeta(r))
	if err != nil {
		writePayrollRunError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h payrollRunHandler) finalize(w http.ResponseWriter, r *http.Request) {
	version, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Kesinleştirme için If-Match gereklidir.")
		return
	}
	item, err := h.service.Finalize(r.Context(), sessionFromRequest(r), chi.URLParam(r, "runID"), version, requestMeta(r))
	if err != nil {
		writePayrollRunError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h payrollRunHandler) listManual(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.ListManualComponents(r.Context(), sessionFromRequest(r), chi.URLParam(r, "runID"))
	if err != nil {
		writePayrollRunError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h payrollRunHandler) addManual(w http.ResponseWriter, r *http.Request) {
	var input payrollrun.ManualComponentInput
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Manuel bileşen bilgileri geçersiz.")
		return
	}
	item, err := h.service.AddManualComponent(r.Context(), sessionFromRequest(r), chi.URLParam(r, "runID"), input, requestMeta(r))
	if err != nil {
		writePayrollRunError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (h payrollRunHandler) archiveManual(w http.ResponseWriter, r *http.Request) {
	err := h.service.ArchiveManualComponent(r.Context(), sessionFromRequest(r), chi.URLParam(r, "runID"), chi.URLParam(r, "componentID"), requestMeta(r))
	if err != nil {
		writePayrollRunError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- Payroll payments (kasa/banka çıkışı) ----

type payrollPaymentHandler struct{ service *payrollpayment.Service }

func mountPayrollPaymentRoutes(router chi.Router, identityService *identity.Service, service *payrollpayment.Service) {
	auth := identityHandler{service: identityService}
	h := payrollPaymentHandler{service: service}
	read := router.With(auth.requireSession)
	write := router.With(auth.requireSession, auth.requireCSRF)
	read.Get("/api/v1/hr/payroll-runs/{runID}/payments", h.list)
	write.Post("/api/v1/hr/payroll-runs/{runID}/payments", h.create)
	write.Post("/api/v1/hr/payroll-payments/{paymentID}/reverse", h.reverse)
}

func (h payrollPaymentHandler) list(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.ForRun(r.Context(), sessionFromRequest(r), chi.URLParam(r, "runID"))
	if err != nil {
		writePayrollPaymentError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h payrollPaymentHandler) create(w http.ResponseWriter, r *http.Request) {
	var input payrollpayment.CreateInput
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Ödeme bilgileri geçersiz.")
		return
	}
	item, err := h.service.Create(r.Context(), sessionFromRequest(r), chi.URLParam(r, "runID"), input)
	if err != nil {
		writePayrollPaymentError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (h payrollPaymentHandler) reverse(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Reason string `json:"reason"`
	}
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Geri alma bilgisi geçersiz.")
		return
	}
	item, err := h.service.Reverse(r.Context(), sessionFromRequest(r), chi.URLParam(r, "paymentID"), input.Reason)
	if err != nil {
		writePayrollPaymentError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func writePayrollPaymentError(w http.ResponseWriter, r *http.Request, err error) {
	financeCode := finance.ErrorCode(err)
	financeMsg := finance.ErrorMessage(err)
	switch {
	case errors.Is(err, identity.ErrForbidden):
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu işlem için yetkiniz veya hesap erişiminiz yok.")
	case errors.Is(err, identity.ErrValidation):
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", err.Error())
	case financeCode != "":
		msg := financeMsg
		if msg == "" {
			msg = "Kasa/banka hareketi kaydedilemedi."
		}
		writeError(w, r, http.StatusUnprocessableEntity, financeCode, msg)
	case errors.Is(err, payrollpayment.ErrRunNotFound):
		writeError(w, r, http.StatusNotFound, "PAYROLL_RUN_NOT_FOUND", "Bordro bulunamadı.")
	case errors.Is(err, payrollpayment.ErrRunNotFinalized):
		writeError(w, r, http.StatusUnprocessableEntity, "PAYROLL_RUN_NOT_FINALIZED", "Ödeme için bordro kesinleşmiş olmalı.")
	case errors.Is(err, payrollpayment.ErrNothingToPay):
		writeError(w, r, http.StatusUnprocessableEntity, "PAYROLL_NOTHING_TO_PAY", "Bu bordroda ödenecek net tutar yok.")
	case errors.Is(err, payrollpayment.ErrAlreadyPaid):
		writeError(w, r, http.StatusConflict, "PAYROLL_ALREADY_PAID", "Bu bordronun ödemesi zaten oluşturulmuş.")
	case errors.Is(err, payrollpayment.ErrPaymentNotFound):
		writeError(w, r, http.StatusNotFound, "PAYROLL_PAYMENT_NOT_FOUND", "Bordro ödemesi bulunamadı.")
	case errors.Is(err, payrollpayment.ErrNotReversible):
		writeError(w, r, http.StatusConflict, "PAYROLL_PAYMENT_NOT_REVERSIBLE", "Bu ödeme geri alınamaz.")
	default:
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "İşlem tamamlanamadı.")
	}
}

func writePayrollRunError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, identity.ErrForbidden):
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu işlem için yetkiniz yok.")
	case errors.Is(err, identity.ErrValidation):
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", err.Error())
	case errors.Is(err, identity.ErrConflict):
		writeError(w, r, http.StatusPreconditionFailed, "VERSION_CONFLICT", "Bordro run başka bir kullanıcı tarafından değiştirildi.")
	case errors.Is(err, payrollrun.ErrRunNotFound):
		writeError(w, r, http.StatusNotFound, "PAYROLL_RUN_NOT_FOUND", "Bordro run bulunamadı.")
	case errors.Is(err, payrollrun.ErrRunNotDraft):
		writeError(w, r, http.StatusConflict, "PAYROLL_RUN_NOT_DRAFT", "Kesinleşmiş bordro değiştirilemez.")
	case errors.Is(err, payrollrun.ErrRunExists):
		writeError(w, r, http.StatusConflict, "PAYROLL_RUN_EXISTS", "Bu run numarası zaten kullanımda.")
	case errors.Is(err, payrollrun.ErrTimesheetNotFinal):
		writeError(w, r, http.StatusUnprocessableEntity, "PAYROLL_TIMESHEET_NOT_FINALIZED", "Bordro için puantaj dönemi kesinleşmiş olmalı.")
	case errors.Is(err, payrollrun.ErrLegislationMissing):
		writeError(w, r, http.StatusUnprocessableEntity, "PAYROLL_LEGISLATION_NOT_FOUND", "Dönem için aktif bordro mevzuatı yok.")
	case errors.Is(err, payrollrun.ErrJobInProgress):
		writeError(w, r, http.StatusConflict, "PAYROLL_JOB_IN_PROGRESS", "Bu run için bir hesaplama zaten sürüyor.")
	case errors.Is(err, payrollrun.ErrNoActiveGeneration):
		writeError(w, r, http.StatusConflict, "PAYROLL_ACTIVE_GENERATION_NOT_FOUND", "Kesinleştirilecek aktif hesaplama yok.")
	case errors.Is(err, payrollrun.ErrInputChanged):
		writeError(w, r, http.StatusConflict, "PAYROLL_INPUT_CHANGED",
			"Hesaplamadan bu yana bordro girdileri (ücret, çalışan listesi, puantaj ya da manuel bileşenler) değişti. Bordroyu yeniden hesaplayın.")
	case errors.Is(err, payrollrun.ErrManualNotFound):
		writeError(w, r, http.StatusNotFound, "PAYROLL_MANUAL_COMPONENT_NOT_FOUND", "Manuel bileşen bulunamadı.")
	case errors.Is(err, payrollrun.ErrEmployeeGone):
		writeError(w, r, http.StatusUnprocessableEntity, "PAYROLL_EMPLOYEE_NOT_FOUND", "Çalışan bulunamadı.")
	default:
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "İşlem tamamlanamadı.")
	}
}
