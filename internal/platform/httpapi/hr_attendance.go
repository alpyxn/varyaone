package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/alpyxn/varyaone/internal/hr/calendar"
	"github.com/alpyxn/varyaone/internal/hr/leave"
	"github.com/alpyxn/varyaone/internal/hr/schedule"
	"github.com/alpyxn/varyaone/internal/hr/timesheet"
	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/go-chi/chi/v5"
)

// ---- Work schedules ----

type hrScheduleHandler struct{ service *schedule.Service }

func mountHRScheduleRoutes(router chi.Router, identityService *identity.Service, service *schedule.Service) {
	auth := identityHandler{service: identityService}
	h := hrScheduleHandler{service: service}
	read := router.With(auth.requireSession)
	write := router.With(auth.requireSession, auth.requireCSRF)
	read.Get("/api/v1/hr/schedule-templates", h.listTemplates)
	read.Get("/api/v1/hr/schedule-templates/{templateID}", h.getTemplate)
	write.Post("/api/v1/hr/schedule-templates", h.createTemplate)
	write.Post("/api/v1/hr/schedule-templates/{templateID}/versions", h.addVersion)
	write.Delete("/api/v1/hr/schedule-templates/{templateID}/versions/{versionID}", h.deleteVersion)
	read.Get("/api/v1/hr/employees/{employeeID}/schedule-assignments", h.listAssignments)
	write.Post("/api/v1/hr/employees/{employeeID}/schedule-assignments", h.assign)
	write.Delete("/api/v1/hr/employees/{employeeID}/schedule-assignments/{assignmentID}", h.deleteAssignment)
}

func (h hrScheduleHandler) listTemplates(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.ListTemplates(r.Context(), sessionFromRequest(r))
	if err != nil {
		writeHRScheduleError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h hrScheduleHandler) getTemplate(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.GetTemplate(r.Context(), sessionFromRequest(r), chi.URLParam(r, "templateID"))
	if err != nil {
		writeHRScheduleError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h hrScheduleHandler) createTemplate(w http.ResponseWriter, r *http.Request) {
	var input schedule.TemplateInput
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Şablon bilgileri geçersiz.")
		return
	}
	item, err := h.service.CreateTemplate(r.Context(), sessionFromRequest(r), input, requestMeta(r))
	if err != nil {
		writeHRScheduleError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (h hrScheduleHandler) addVersion(w http.ResponseWriter, r *http.Request) {
	var input schedule.VersionInput
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Sürüm bilgileri geçersiz.")
		return
	}
	item, err := h.service.AddVersion(r.Context(), sessionFromRequest(r), chi.URLParam(r, "templateID"), input, requestMeta(r))
	if err != nil {
		writeHRScheduleError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (h hrScheduleHandler) deleteVersion(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.DeleteVersion(r.Context(), sessionFromRequest(r), chi.URLParam(r, "templateID"), chi.URLParam(r, "versionID"), requestMeta(r))
	if err != nil {
		writeHRScheduleError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h hrScheduleHandler) deleteAssignment(w http.ResponseWriter, r *http.Request) {
	err := h.service.DeleteAssignment(r.Context(), sessionFromRequest(r), chi.URLParam(r, "employeeID"), chi.URLParam(r, "assignmentID"), requestMeta(r))
	if err != nil {
		writeHRScheduleError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h hrScheduleHandler) listAssignments(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.ListAssignments(r.Context(), sessionFromRequest(r), chi.URLParam(r, "employeeID"))
	if err != nil {
		writeHRScheduleError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h hrScheduleHandler) assign(w http.ResponseWriter, r *http.Request) {
	var input schedule.AssignmentInput
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Atama bilgileri geçersiz.")
		return
	}
	item, err := h.service.AssignToEmployee(r.Context(), sessionFromRequest(r), chi.URLParam(r, "employeeID"), input, requestMeta(r))
	if err != nil {
		writeHRScheduleError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func writeHRScheduleError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, identity.ErrForbidden):
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu işlem için yetkiniz yok.")
	case errors.Is(err, schedule.ErrDaysInvalid):
		writeError(w, r, http.StatusUnprocessableEntity, "WORK_SCHEDULE_DAYS_INVALID", err.Error())
	case errors.Is(err, identity.ErrValidation):
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", err.Error())
	case errors.Is(err, schedule.ErrOverlap):
		writeError(w, r, http.StatusUnprocessableEntity, "WORK_SCHEDULE_OVERLAP", "Tarih aralığı mevcut bir kayıtla çakışıyor.")
	case errors.Is(err, schedule.ErrVersionInUse):
		writeError(w, r, http.StatusConflict, "WORK_SCHEDULE_VERSION_IN_USE", "Bu sürüm oluşturulmuş puantajlarda kullanıldığı için silinemez.")
	case errors.Is(err, schedule.ErrAssignmentGone):
		writeError(w, r, http.StatusNotFound, "WORK_SCHEDULE_ASSIGNMENT_NOT_FOUND", "Plan ataması bulunamadı.")
	case errors.Is(err, schedule.ErrNotFound):
		writeError(w, r, http.StatusNotFound, "WORK_SCHEDULE_TEMPLATE_NOT_FOUND", "Çalışma şablonu bulunamadı.")
	case errors.Is(err, schedule.ErrEmployeeGone):
		writeError(w, r, http.StatusUnprocessableEntity, "WORK_SCHEDULE_EMPLOYEE_NOT_FOUND", "Çalışan bulunamadı.")
	default:
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "İşlem tamamlanamadı.")
	}
}

// ---- Leave ----

type hrLeaveHandler struct{ service *leave.Service }

func mountHRLeaveRoutes(router chi.Router, identityService *identity.Service, service *leave.Service) {
	auth := identityHandler{service: identityService}
	h := hrLeaveHandler{service: service}
	read := router.With(auth.requireSession)
	write := router.With(auth.requireSession, auth.requireCSRF)
	read.Get("/api/v1/hr/leave-types", h.listTypes)
	write.Post("/api/v1/hr/leave-types", h.createType)
	write.Patch("/api/v1/hr/leave-types/{leaveTypeID}", h.updateType)
}

func (h hrLeaveHandler) listTypes(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.ListTypes(r.Context(), sessionFromRequest(r))
	if err != nil {
		writeHRLeaveError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h hrLeaveHandler) createType(w http.ResponseWriter, r *http.Request) {
	var input leave.LeaveTypeInput
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "İzin türü bilgileri geçersiz.")
		return
	}
	item, err := h.service.CreateType(r.Context(), sessionFromRequest(r), input, requestMeta(r))
	if err != nil {
		writeHRLeaveError(w, r, err)
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusCreated, item)
}

func (h hrLeaveHandler) updateType(w http.ResponseWriter, r *http.Request) {
	version, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "İzin türü güncellemesi için If-Match gereklidir.")
		return
	}
	var input leave.LeaveTypeInput
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "İzin türü bilgileri geçersiz.")
		return
	}
	item, err := h.service.UpdateType(r.Context(), sessionFromRequest(r), chi.URLParam(r, "leaveTypeID"), version, input, requestMeta(r))
	if err != nil {
		writeHRLeaveError(w, r, err)
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusOK, item)
}

func writeHRLeaveError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, identity.ErrForbidden):
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu işlem için yetkiniz yok.")
	case errors.Is(err, identity.ErrValidation):
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", err.Error())
	case errors.Is(err, identity.ErrConflict):
		writeError(w, r, http.StatusPreconditionFailed, "VERSION_CONFLICT", "İzin türü kaydı başka bir kullanıcı tarafından değiştirildi.")
	case errors.Is(err, leave.ErrTypeNotFound):
		writeError(w, r, http.StatusNotFound, "LEAVE_TYPE_NOT_FOUND", "İzin türü bulunamadı.")
	default:
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "İşlem tamamlanamadı.")
	}
}

// ---- Public holiday calendars ----

type hrCalendarHandler struct{ service *calendar.Service }

func mountHRCalendarRoutes(router chi.Router, identityService *identity.Service, service *calendar.Service) {
	auth := identityHandler{service: identityService}
	h := hrCalendarHandler{service: service}
	read := router.With(auth.requireSession)
	write := router.With(auth.requireSession, auth.requireCSRF)
	read.Get("/api/v1/hr/holiday-calendars", h.list)
	read.Get("/api/v1/hr/holiday-calendars/{calendarID}", h.get)
	write.Post("/api/v1/hr/holiday-calendars", h.create)
	write.Post("/api/v1/hr/holiday-calendars/{calendarID}/holidays", h.addHoliday)
	write.Post("/api/v1/hr/holiday-calendars/{calendarID}/activate", h.activate)
}

func (h hrCalendarHandler) list(w http.ResponseWriter, r *http.Request) {
	year, _ := strconv.Atoi(r.URL.Query().Get("year"))
	items, err := h.service.List(r.Context(), sessionFromRequest(r), year)
	if err != nil {
		writeHRCalendarError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h hrCalendarHandler) get(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.Get(r.Context(), sessionFromRequest(r), chi.URLParam(r, "calendarID"))
	if err != nil {
		writeHRCalendarError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h hrCalendarHandler) create(w http.ResponseWriter, r *http.Request) {
	var input calendar.CalendarInput
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Takvim bilgileri geçersiz.")
		return
	}
	item, err := h.service.CreateDraft(r.Context(), sessionFromRequest(r), input, requestMeta(r))
	if err != nil {
		writeHRCalendarError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (h hrCalendarHandler) addHoliday(w http.ResponseWriter, r *http.Request) {
	var input calendar.HolidayInput
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Tatil bilgileri geçersiz.")
		return
	}
	item, err := h.service.AddHoliday(r.Context(), sessionFromRequest(r), chi.URLParam(r, "calendarID"), input, requestMeta(r))
	if err != nil {
		writeHRCalendarError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h hrCalendarHandler) activate(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.Activate(r.Context(), sessionFromRequest(r), chi.URLParam(r, "calendarID"), requestMeta(r))
	if err != nil {
		writeHRCalendarError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func writeHRCalendarError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, identity.ErrForbidden):
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu işlem için yetkiniz yok.")
	case errors.Is(err, identity.ErrValidation):
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", err.Error())
	case errors.Is(err, calendar.ErrNotFound):
		writeError(w, r, http.StatusNotFound, "PUBLIC_HOLIDAY_CALENDAR_NOT_FOUND", "Tatil takvimi bulunamadı.")
	case errors.Is(err, calendar.ErrNotDraft):
		writeError(w, r, http.StatusConflict, "PUBLIC_HOLIDAY_CALENDAR_NOT_DRAFT", "Yalnızca taslak takvim düzenlenebilir.")
	case errors.Is(err, calendar.ErrAlreadyActive):
		writeError(w, r, http.StatusConflict, "PUBLIC_HOLIDAY_CALENDAR_ACTIVE_EXISTS", "Bu yıl için zaten aktif bir takvim var.")
	default:
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "İşlem tamamlanamadı.")
	}
}

// ---- Timesheet ----

type hrTimesheetHandler struct{ service *timesheet.Service }

func mountHRTimesheetRoutes(router chi.Router, identityService *identity.Service, service *timesheet.Service) {
	auth := identityHandler{service: identityService}
	h := hrTimesheetHandler{service: service}
	read := router.With(auth.requireSession)
	write := router.With(auth.requireSession, auth.requireCSRF)
	read.Get("/api/v1/hr/timesheet-periods", h.list)
	read.Get("/api/v1/hr/timesheet-periods/{periodID}", h.get)
	read.Get("/api/v1/hr/timesheet-periods/{periodID}/readiness", h.readiness)
	write.Post("/api/v1/hr/timesheet-periods", h.create)
	write.Post("/api/v1/hr/timesheet-periods/{periodID}/generate", h.generate)
	write.Put("/api/v1/hr/timesheet-periods/{periodID}/days", h.upsertDay)
	write.Patch("/api/v1/hr/timesheet-periods/{periodID}/days/{dayID}", h.updateDay)
	write.Delete("/api/v1/hr/timesheet-periods/{periodID}/days/{dayID}", h.deleteDay)
	write.Post("/api/v1/hr/timesheet-periods/{periodID}/finalize", h.finalize)
	write.Post("/api/v1/hr/timesheet-periods/{periodID}/reopen", h.reopen)
}

func (h hrTimesheetHandler) list(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.ListPeriods(r.Context(), sessionFromRequest(r))
	if err != nil {
		writeHRTimesheetError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h hrTimesheetHandler) get(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.GetPeriod(r.Context(), sessionFromRequest(r), chi.URLParam(r, "periodID"))
	if err != nil {
		writeHRTimesheetError(w, r, err)
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusOK, item)
}

// readiness lists, per employee, what stops this period from being entered or
// calculated for them, so the puantaj screen can say so before the first click.
func (h hrTimesheetHandler) readiness(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.Readiness(r.Context(), sessionFromRequest(r), chi.URLParam(r, "periodID"))
	if err != nil {
		writeHRTimesheetError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h hrTimesheetHandler) create(w http.ResponseWriter, r *http.Request) {
	var input timesheet.PeriodInput
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Dönem bilgileri geçersiz.")
		return
	}
	item, err := h.service.CreatePeriod(r.Context(), sessionFromRequest(r), input, requestMeta(r))
	if err != nil {
		writeHRTimesheetError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (h hrTimesheetHandler) generate(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.Generate(r.Context(), sessionFromRequest(r), chi.URLParam(r, "periodID"), requestMeta(r))
	if err != nil {
		writeHRTimesheetError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h hrTimesheetHandler) updateDay(w http.ResponseWriter, r *http.Request) {
	version, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Gün güncellemesi için If-Match gereklidir.")
		return
	}
	var input timesheet.DayInput
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Gün bilgileri geçersiz.")
		return
	}
	item, err := h.service.UpdateDay(r.Context(), sessionFromRequest(r), chi.URLParam(r, "periodID"), chi.URLParam(r, "dayID"), version, input, requestMeta(r))
	if err != nil {
		writeHRTimesheetError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h hrTimesheetHandler) upsertDay(w http.ResponseWriter, r *http.Request) {
	var input timesheet.DayUpsertInput
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Gün bilgileri geçersiz.")
		return
	}
	item, err := h.service.UpsertDay(r.Context(), sessionFromRequest(r), chi.URLParam(r, "periodID"), input, requestMeta(r))
	if err != nil {
		writeHRTimesheetError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h hrTimesheetHandler) deleteDay(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.DeleteDay(r.Context(), sessionFromRequest(r), chi.URLParam(r, "periodID"), chi.URLParam(r, "dayID"), requestMeta(r))
	if err != nil {
		writeHRTimesheetError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h hrTimesheetHandler) finalize(w http.ResponseWriter, r *http.Request) {
	version, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Kesinleştirme için If-Match gereklidir.")
		return
	}
	item, err := h.service.Finalize(r.Context(), sessionFromRequest(r), chi.URLParam(r, "periodID"), version, requestMeta(r))
	if err != nil {
		writeHRTimesheetError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h hrTimesheetHandler) reopen(w http.ResponseWriter, r *http.Request) {
	version, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Yeniden açma için If-Match gereklidir.")
		return
	}
	var input struct {
		Reason string `json:"reason"`
	}
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Gerekçe bilgisi geçersiz.")
		return
	}
	item, err := h.service.Reopen(r.Context(), sessionFromRequest(r), chi.URLParam(r, "periodID"), version, input.Reason, requestMeta(r))
	if err != nil {
		writeHRTimesheetError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func writeHRTimesheetError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, identity.ErrForbidden):
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu işlem için yetkiniz yok.")
	case errors.Is(err, identity.ErrValidation):
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", err.Error())
	case errors.Is(err, identity.ErrConflict):
		writeError(w, r, http.StatusPreconditionFailed, "VERSION_CONFLICT", "Puantaj dönemi başka bir kullanıcı tarafından değiştirildi.")
	case errors.Is(err, timesheet.ErrPeriodNotFound):
		writeError(w, r, http.StatusNotFound, "TIMESHEET_PERIOD_NOT_FOUND", "Puantaj dönemi bulunamadı.")
	case errors.Is(err, timesheet.ErrDayNotFound):
		writeError(w, r, http.StatusNotFound, "TIMESHEET_DAY_NOT_FOUND", "Puantaj günü bulunamadı.")
	case errors.Is(err, timesheet.ErrFinalized):
		writeError(w, r, http.StatusConflict, "TIMESHEET_PERIOD_FINALIZED", "Kesinleşmiş puantaj değiştirilemez.")
	case errors.Is(err, timesheet.ErrNotFinalized):
		writeError(w, r, http.StatusConflict, "TIMESHEET_PERIOD_NOT_FINALIZED", "Yalnızca kesinleşmiş puantaj yeniden açılabilir.")
	case errors.Is(err, timesheet.ErrUsedByPayroll):
		writeError(w, r, http.StatusConflict, "TIMESHEET_USED_BY_FINALIZED_PAYROLL", "Kesinleşmiş bordroda kullanılan puantaj yeniden açılamaz.")
	case errors.Is(err, timesheet.ErrEmployeeNotReady):
		// The employee card is missing something the puantaj needs; err.Error()
		// already names the employee and the exact gap.
		writeError(w, r, http.StatusUnprocessableEntity, "TIMESHEET_EMPLOYEE_NOT_READY", err.Error())
	case errors.Is(err, timesheet.ErrPeriodExists):
		writeError(w, r, http.StatusConflict, "TIMESHEET_PERIOD_EXISTS", "Bu dönem için puantaj zaten var.")
	default:
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "İşlem tamamlanamadı.")
	}
}
