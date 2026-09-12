package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/agenda"
	"github.com/alpyxn/varyaone/internal/dashboard"
	"github.com/alpyxn/varyaone/internal/demo"
	"github.com/alpyxn/varyaone/internal/email"
	"github.com/alpyxn/varyaone/internal/exchange"
	"github.com/alpyxn/varyaone/internal/finance"
	"github.com/alpyxn/varyaone/internal/fixedasset"
	"github.com/alpyxn/varyaone/internal/hr/advance"
	"github.com/alpyxn/varyaone/internal/hr/calendar"
	"github.com/alpyxn/varyaone/internal/hr/document"
	"github.com/alpyxn/varyaone/internal/hr/employee"
	"github.com/alpyxn/varyaone/internal/hr/employment"
	"github.com/alpyxn/varyaone/internal/hr/leave"
	"github.com/alpyxn/varyaone/internal/hr/schedule"
	"github.com/alpyxn/varyaone/internal/hr/timesheet"
	"github.com/alpyxn/varyaone/internal/identity"
	dataimports "github.com/alpyxn/varyaone/internal/imports"
	"github.com/alpyxn/varyaone/internal/inventory"
	"github.com/alpyxn/varyaone/internal/media"
	"github.com/alpyxn/varyaone/internal/party"
	"github.com/alpyxn/varyaone/internal/payroll/delivery"
	"github.com/alpyxn/varyaone/internal/payroll/legislation"
	payrollpayment "github.com/alpyxn/varyaone/internal/payroll/payment"
	payrollrun "github.com/alpyxn/varyaone/internal/payroll/run"
	"github.com/alpyxn/varyaone/internal/platform/httpapi/contract"
	"github.com/alpyxn/varyaone/internal/platform/opctl"
	"github.com/alpyxn/varyaone/internal/preferences"
	"github.com/alpyxn/varyaone/internal/pricing"
	"github.com/alpyxn/varyaone/internal/products"
	"github.com/alpyxn/varyaone/internal/pulse"
	"github.com/alpyxn/varyaone/internal/purchasing"
	"github.com/alpyxn/varyaone/internal/reporting"
	"github.com/alpyxn/varyaone/internal/sales"
	"github.com/alpyxn/varyaone/internal/taxes"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Readiness interface {
	Check(context.Context) error
}

type apiHandler struct {
	release   string
	readiness Readiness
	logger    *slog.Logger
}

func (h apiHandler) GetLiveness(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, contract.HealthResponse{Status: contract.Ok, Service: contract.Api, Release: h.release})
}

func (h apiHandler) GetReadiness(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if err := h.readiness.Check(ctx); err != nil {
		// The response stays deliberately vague — it is served unauthenticated —
		// but the reason has to go somewhere. Without this, a readiness failure
		// is a 503 with no way to find out what is wrong short of guessing.
		if h.logger != nil {
			h.logger.Warn("readiness check failed", "error", err, "trace_id", TraceID(r.Context()))
		}
		writeError(w, r, http.StatusServiceUnavailable, "NOT_READY", "Varya One henüz istek almaya hazır değil.")
		return
	}
	writeJSON(w, http.StatusOK, contract.HealthResponse{Status: contract.Ok, Service: contract.Api, Release: h.release})
}

type RouterOption func(*routerOptions)

type routerOptions struct {
	identity        *identity.Service
	party           *party.Service
	products        *products.Service
	pricing         *pricing.Service
	taxes           *taxes.Service
	preferences     *preferences.Service
	pulse           *pulse.Service
	dashboard       *dashboard.Service
	agenda          *agenda.Service
	exchange        *exchange.Service
	finance         *finance.Service
	inventory       *inventory.Service
	sales           *sales.Service
	purchasing      *purchasing.Service
	media           *media.Service
	search          *SearchService
	dataExchange    *dataimports.Service
	reporting       *reporting.Service
	emailSettings   *delivery.SMTPSettingsService
	emailTemplate   *email.TemplateService
	emailCompose    *email.ComposeService
	hrEmployee      *employee.Service
	hrAdvance       *advance.Service
	hrEmployment    *employment.Service
	hrDocument      *document.Service
	hrSchedule      *schedule.Service
	hrLeave         *leave.Service
	hrCalendar      *calendar.Service
	hrTimesheet     *timesheet.Service
	payrollLegis    *legislation.Service
	legislationRepo *legislation.Repository
	payrollRun      *payrollrun.Service
	payrollPayment  *payrollpayment.Service
	payrollDeliv    *delivery.Service
	fixedAsset      *fixedasset.Service
	systemBackup    backupEngine
	opController    *opctl.Controller
	controlDir      string
	spa             http.Handler
	secureCookies   bool
	scopePool       *pgxpool.Pool
	// demo carries the identity of the shared showcase company when this
	// installation runs as the public demo. It is nil for every normal
	// installation, and the demo routes are then never mounted.
	demo *DemoRuntime
}

// DemoRuntime gives the HTTP layer what it needs to serve the public showcase:
// which company visitors are signed in to, and the runner that reports the
// reset schedule and performs a rebuild.
type DemoRuntime struct {
	CompanyID string
	UserID    string
	Runner    *demo.Runner
	// Email and Password are the shared demo account, handed to the login
	// screen so it can present a filled-in form instead of asking a visitor for
	// credentials they were never given. They are not secrets: demo mode
	// already opens a session on this company without any password at all.
	Email    string
	Password string
}

// WithDemo mounts the demo endpoints for the public showcase deployment. Never
// pass it on an installation holding real data: it opens a passwordless session
// on the named company and exposes a button that wipes it.
func WithDemo(runtime DemoRuntime) RouterOption {
	return func(options *routerOptions) { options.demo = &runtime }
}

// WithSPA serves the embedded single-page frontend for every non-API GET/HEAD
// request. Used by the `varyaone stack` desktop runtime so one binary serves
// both the API and the UI; the Docker deployment leaves it unset and keeps the
// separate adapter-node frontend container.
func WithSPA(handler http.Handler) RouterOption {
	return func(o *routerOptions) { o.spa = handler }
}

// WithSystemBackup mounts the backup, restore and operation-status routes. The
// controller is not optional: the destructive half of these routes is only safe
// because every operation takes the installation's lease and leaves a durable
// record, and a route mounted without one would be neither coordinated nor
// recoverable.
func WithSystemBackup(engine backupEngine, controller *opctl.Controller, controlDir string) RouterOption {
	return func(options *routerOptions) {
		options.systemBackup = engine
		options.opController = controller
		options.controlDir = controlDir
	}
}

func WithParty(service *party.Service) RouterOption {
	return func(options *routerOptions) { options.party = service }
}

func WithPreferences(service *preferences.Service) RouterOption {
	return func(options *routerOptions) { options.preferences = service }
}

func WithPulse(service *pulse.Service) RouterOption {
	return func(options *routerOptions) { options.pulse = service }
}

func WithDashboard(service *dashboard.Service) RouterOption {
	return func(options *routerOptions) { options.dashboard = service }
}

func WithAgenda(service *agenda.Service) RouterOption {
	return func(options *routerOptions) { options.agenda = service }
}

func WithProducts(service *products.Service) RouterOption {
	return func(options *routerOptions) { options.products = service }
}
func WithPricing(service *pricing.Service) RouterOption {
	return func(options *routerOptions) { options.pricing = service }
}

func WithExchange(service *exchange.Service) RouterOption {
	return func(options *routerOptions) { options.exchange = service }
}
func WithTaxes(service *taxes.Service) RouterOption {
	return func(options *routerOptions) { options.taxes = service }
}

func WithFinance(service *finance.Service) RouterOption {
	return func(options *routerOptions) { options.finance = service }
}

func WithInventory(service *inventory.Service) RouterOption {
	return func(options *routerOptions) { options.inventory = service }
}

func WithSales(service *sales.Service) RouterOption {
	return func(options *routerOptions) { options.sales = service }
}

func WithPurchasing(service *purchasing.Service) RouterOption {
	return func(options *routerOptions) { options.purchasing = service }
}

func WithMedia(service *media.Service) RouterOption {
	return func(options *routerOptions) { options.media = service }
}

func WithSearch(service *SearchService) RouterOption {
	return func(options *routerOptions) { options.search = service }
}

func WithIdentity(service *identity.Service, secureCookies bool) RouterOption {
	return func(options *routerOptions) {
		options.identity = service
		options.secureCookies = secureCookies
	}
}

func WithDataExchange(service *dataimports.Service) RouterOption {
	return func(options *routerOptions) { options.dataExchange = service }
}

func WithReporting(service *reporting.Service) RouterOption {
	return func(options *routerOptions) { options.reporting = service }
}

func WithEmail(templates *email.TemplateService, compose *email.ComposeService) RouterOption {
	return func(options *routerOptions) {
		options.emailTemplate = templates
		options.emailCompose = compose
	}
}

func WithEmailSettings(service *delivery.SMTPSettingsService) RouterOption {
	return func(options *routerOptions) { options.emailSettings = service }
}

func WithHREmployee(service *employee.Service) RouterOption {
	return func(options *routerOptions) { options.hrEmployee = service }
}

func WithHRAdvance(service *advance.Service) RouterOption {
	return func(options *routerOptions) { options.hrAdvance = service }
}

func WithFixedAsset(service *fixedasset.Service) RouterOption {
	return func(options *routerOptions) { options.fixedAsset = service }
}

func WithHREmployment(service *employment.Service) RouterOption {
	return func(options *routerOptions) { options.hrEmployment = service }
}

func WithHRDocument(service *document.Service) RouterOption {
	return func(options *routerOptions) { options.hrDocument = service }
}

func WithHRSchedule(service *schedule.Service) RouterOption {
	return func(options *routerOptions) { options.hrSchedule = service }
}

func WithHRLeave(service *leave.Service) RouterOption {
	return func(options *routerOptions) { options.hrLeave = service }
}

func WithHRCalendar(service *calendar.Service) RouterOption {
	return func(options *routerOptions) { options.hrCalendar = service }
}

func WithHRTimesheet(service *timesheet.Service) RouterOption {
	return func(options *routerOptions) { options.hrTimesheet = service }
}

func WithPayrollLegislation(service *legislation.Service) RouterOption {
	return func(options *routerOptions) { options.payrollLegis = service }
}

// WithLegislationRepository wires the read-only legislation repository used by
// the wage-preview / minimum-wage endpoints (gross↔net calculator).
func WithLegislationRepository(repo *legislation.Repository) RouterOption {
	return func(options *routerOptions) { options.legislationRepo = repo }
}

func WithPayrollRun(service *payrollrun.Service) RouterOption {
	return func(options *routerOptions) { options.payrollRun = service }
}

func WithPayrollPayment(service *payrollpayment.Service) RouterOption {
	return func(options *routerOptions) { options.payrollPayment = service }
}

func WithPayrollDelivery(service *delivery.Service) RouterOption {
	return func(options *routerOptions) { options.payrollDeliv = service }
}

func NewRouter(logger *slog.Logger, release string, readiness Readiness, options ...RouterOption) http.Handler {
	configuration := routerOptions{}
	for _, option := range options {
		option(&configuration)
	}
	requestScopePool.Store(configuration.scopePool)
	router := chi.NewRouter()
	router.Use(RequestContext)
	router.Use(func(next http.Handler) http.Handler { return Recover(logger, next) })
	router.Use(func(next http.Handler) http.Handler { return AccessLog(logger, next) })
	// The 30s request timeout is right for the ordinary API and wrong for the
	// two operations that legitimately take hours. Those carry their own
	// bounded contexts and their own socket deadlines, so they are exempted
	// here by path rather than the limit being relaxed for everything.
	router.Use(timeoutExcept(30*time.Second, "/api/v1/system/"))
	// The demo guard runs before every route: it refuses the operations the
	// public showcase must not perform and answers "being rebuilt" during a
	// reset. A normal installation never installs it.
	var demoState *demoStateCache
	if configuration.demo != nil && configuration.demo.Runner != nil {
		// One cache shared by the guard and the endpoints, so a reset triggered
		// through the API immediately shows up in the guard too.
		demoState = newDemoStateCache(configuration.demo.Runner)
		router.Use(demoGuard(demoState))
	}
	contract.HandlerFromMux(apiHandler{release: release, readiness: readiness, logger: logger}, router)
	if demoState != nil && configuration.identity != nil {
		mountDemoRoutes(router, configuration.identity, configuration.secureCookies, configuration.demo, demoState)
	}
	if configuration.identity != nil {
		mountIdentityRoutes(router, configuration.identity, configuration.secureCookies, configuration.demo)
		mountModuleRoutes(router, configuration.identity)
		if configuration.party != nil {
			mountPartyRoutes(router, configuration.identity, configuration.party)
			mountPartyMovementRoutes(router, configuration.identity, configuration.party)
		}
		if configuration.preferences != nil {
			mountPreferenceRoutes(router, configuration.identity, configuration.preferences)
		}
		if configuration.pulse != nil {
			mountPulseRoutes(router, configuration.identity, configuration.pulse)
		}
		if configuration.dashboard != nil {
			mountDashboardRoutes(router, configuration.identity, configuration.dashboard)
		}
		if configuration.agenda != nil {
			mountAgendaRoutes(router, configuration.identity, configuration.agenda)
		}
		if configuration.products != nil {
			mountProductRoutes(router, configuration.identity, configuration.products)
		}
		if configuration.pricing != nil {
			mountPricingRoutes(router, configuration.identity, configuration.pricing)
		}
		if configuration.exchange != nil {
			mountExchangeRoutes(router, configuration.identity, configuration.exchange)
		}
		if configuration.taxes != nil {
			mountTaxRoutes(router, configuration.identity, configuration.taxes)
		}
		if configuration.finance != nil {
			mountFinanceRoutes(router, configuration.identity, configuration.finance)
		}
		if configuration.inventory != nil {
			mountInventoryRoutes(router, configuration.identity, configuration.inventory)
		}
		if configuration.sales != nil {
			mountSalesRoutes(router, configuration.identity, configuration.sales)
		}
		if configuration.purchasing != nil {
			mountPurchasingRoutes(router, configuration.identity, configuration.purchasing)
		}
		if configuration.media != nil {
			mountMediaRoutes(router, configuration.identity, configuration.media)
		}
		if configuration.search != nil {
			mountSearchRoutes(router, configuration.identity, configuration.search)
		}
		if configuration.dataExchange != nil {
			mountDataExchangeRoutes(router, configuration.identity, configuration.dataExchange)
		}
		if configuration.reporting != nil {
			mountReportingRoutes(router, configuration.identity, configuration.reporting)
		}
		if configuration.emailSettings != nil {
			mountEmailSettingsRoutes(router, configuration.identity, configuration.emailSettings)
		}
		if configuration.emailTemplate != nil && configuration.emailCompose != nil {
			mountEmailRoutes(router, configuration.identity, configuration.emailTemplate, configuration.emailCompose)
		}
		if configuration.hrEmployee != nil {
			mountHREmployeeRoutes(router, configuration.identity, configuration.hrEmployee)
		}
		if configuration.hrAdvance != nil {
			mountHRAdvanceRoutes(router, configuration.identity, configuration.hrAdvance)
		}
		if configuration.fixedAsset != nil {
			mountFixedAssetRoutes(router, configuration.identity, configuration.fixedAsset)
		}
		if configuration.hrEmployment != nil {
			mountHREmploymentRoutes(router, configuration.identity, configuration.hrEmployment)
		}
		if configuration.hrDocument != nil {
			mountHRDocumentRoutes(router, configuration.identity, configuration.hrDocument)
		}
		if configuration.hrSchedule != nil {
			mountHRScheduleRoutes(router, configuration.identity, configuration.hrSchedule)
		}
		if configuration.hrLeave != nil {
			mountHRLeaveRoutes(router, configuration.identity, configuration.hrLeave)
		}
		if configuration.hrCalendar != nil {
			mountHRCalendarRoutes(router, configuration.identity, configuration.hrCalendar)
		}
		if configuration.hrTimesheet != nil {
			mountHRTimesheetRoutes(router, configuration.identity, configuration.hrTimesheet)
		}
		if configuration.payrollLegis != nil {
			mountPayrollLegislationRoutes(router, configuration.identity, configuration.payrollLegis, configuration.hrEmployment)
		}
		if configuration.legislationRepo != nil {
			mountPayrollWagePreviewRoutes(router, configuration.identity, configuration.legislationRepo)
		}
		if configuration.payrollRun != nil {
			mountPayrollRunRoutes(router, configuration.identity, configuration.payrollRun)
		}
		if configuration.payrollPayment != nil {
			mountPayrollPaymentRoutes(router, configuration.identity, configuration.payrollPayment)
		}
		if configuration.payrollDeliv != nil {
			mountPayrollDeliveryRoutes(router, configuration.identity, configuration.payrollDeliv)
		}
		if configuration.systemBackup != nil && configuration.opController != nil {
			mountSystemRoutes(router, configuration.identity, configuration.systemBackup,
				configuration.opController, defaultSpoolDir(configuration.controlDir))
		}
	}
	router.NotFound(func(w http.ResponseWriter, r *http.Request) {
		// API/health paths always get the JSON error contract; anything else
		// (deep links, static assets) falls through to the embedded SPA when the
		// desktop runtime mounted one.
		if configuration.spa != nil &&
			(r.Method == http.MethodGet || r.Method == http.MethodHead) &&
			!strings.HasPrefix(r.URL.Path, "/api/") &&
			!strings.HasPrefix(r.URL.Path, "/health/") {
			configuration.spa.ServeHTTP(w, r)
			return
		}
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "İstenen kaynak bulunamadı.")
	})
	return router
}

// timeoutExcept applies a request timeout to everything except the given path
// prefixes.
//
// chi's Timeout middleware cancels the request context after a fixed duration.
// That is the right default and a wrong fit for the backup routes, where a
// legitimate operation runs for hours; those routes set their own bounded,
// detached contexts and their own socket deadlines instead. Exempting them by
// prefix keeps the decision visible in one place, rather than lifting the limit
// for the whole API to accommodate two endpoints.
func timeoutExcept(limit time.Duration, prefixes ...string) func(http.Handler) http.Handler {
	timeout := middleware.Timeout(limit)
	return func(next http.Handler) http.Handler {
		limited := timeout(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for _, prefix := range prefixes {
				if strings.HasPrefix(r.URL.Path, prefix) {
					next.ServeHTTP(w, r)
					return
				}
			}
			limited.ServeHTTP(w, r)
		})
	}
}
