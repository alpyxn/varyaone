package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/alpyxn/varyaone/internal/agenda"
	"github.com/alpyxn/varyaone/internal/backup"
	"github.com/alpyxn/varyaone/internal/dashboard"
	"github.com/alpyxn/varyaone/internal/demo"
	emailpkg "github.com/alpyxn/varyaone/internal/email"
	"github.com/alpyxn/varyaone/internal/exchange"
	"github.com/alpyxn/varyaone/internal/finance"
	"github.com/alpyxn/varyaone/internal/fixedasset"
	"github.com/alpyxn/varyaone/internal/hr/advance"
	"github.com/alpyxn/varyaone/internal/hr/calendar"
	"github.com/alpyxn/varyaone/internal/hr/document"
	"github.com/alpyxn/varyaone/internal/hr/employee"
	"github.com/alpyxn/varyaone/internal/hr/employment"
	"github.com/alpyxn/varyaone/internal/hr/leave"
	hrschedule "github.com/alpyxn/varyaone/internal/hr/schedule"
	hrtimesheet "github.com/alpyxn/varyaone/internal/hr/timesheet"
	"github.com/alpyxn/varyaone/internal/identity"
	dataimports "github.com/alpyxn/varyaone/internal/imports"
	"github.com/alpyxn/varyaone/internal/inventory"
	"github.com/alpyxn/varyaone/internal/media"
	"github.com/alpyxn/varyaone/internal/party"
	"github.com/alpyxn/varyaone/internal/payroll/delivery"
	"github.com/alpyxn/varyaone/internal/payroll/legislation"
	payrollpayment "github.com/alpyxn/varyaone/internal/payroll/payment"
	payrollrun "github.com/alpyxn/varyaone/internal/payroll/run"
	"github.com/alpyxn/varyaone/internal/platform/config"
	"github.com/alpyxn/varyaone/internal/platform/database"
	"github.com/alpyxn/varyaone/internal/platform/httpapi"
	"github.com/alpyxn/varyaone/internal/platform/migrations"
	"github.com/alpyxn/varyaone/internal/platform/opctl"
	"github.com/alpyxn/varyaone/internal/platform/posting"
	"github.com/alpyxn/varyaone/internal/preferences"
	"github.com/alpyxn/varyaone/internal/pricing"
	"github.com/alpyxn/varyaone/internal/products"
	"github.com/alpyxn/varyaone/internal/pulse"
	"github.com/alpyxn/varyaone/internal/purchasing"
	"github.com/alpyxn/varyaone/internal/reporting"
	"github.com/alpyxn/varyaone/internal/sales"
	"github.com/alpyxn/varyaone/internal/storage"
	"github.com/alpyxn/varyaone/internal/taxes"
	"github.com/jackc/pgx/v5/pgxpool"
)

func RunServer(ctx context.Context, cfg config.Config, logger *slog.Logger, rawPool *pgxpool.Pool, runner *migrations.Runner, extraRouterOptions ...httpapi.RouterOption) error {
	// Before anything else: is this installation in a state where serving
	// traffic is the right thing to do? An API that starts on top of an
	// interrupted restore begins writing into a database that matches no
	// backup, and every write it accepts makes the recovery harder.
	controller, err := opctl.New(cfg.ControlDir)
	if err != nil {
		return fmt.Errorf("initialize operation controller: %w", err)
	}
	if err := controller.GateStartup(); err != nil {
		return err
	}
	httpapi.SetTrustedProxyNets(cfg.TrustedProxyNets)
	warnIfServingRoleBypassesRLS(ctx, rawPool, logger)
	// Services talk to the database through a request-scoped wrapper: when a
	// request has pinned a connection (see requireSession), every query runs on
	// that connection with varyaone.company_id already set, so the row-level
	// security policies enforce company isolation. Outside a request (workers,
	// startup) it transparently falls back to rawPool.
	pool := database.NewScoped(rawPool)
	// cfg.DatabaseURL is the owner/superuser connection; identity needs it to purge
	// a company past RLS and the immutability triggers (DeleteCompany).
	identityService, err := identity.NewService(pool, cfg.MasterKey, identity.WithMaintenanceDSN(cfg.DatabaseURL))
	if err != nil {
		return fmt.Errorf("initialize identity service: %w", err)
	}
	partyService := party.NewService(pool)
	financeService := finance.NewService(pool)
	inventoryService := inventory.NewService(pool)
	exchangeService := exchange.NewService(pool)
	salesService := sales.NewService(pool, financeService, posting.InventoryStockPoster{Service: inventoryService}, exchangeService)
	storageProvider, err := storage.NewProvider(storage.Config{
		Provider:     storage.ProviderKind(cfg.StorageProvider),
		LocalRoot:    cfg.StorageRoot,
		Endpoint:     cfg.StorageEndpoint,
		Bucket:       cfg.StorageBucket,
		Region:       cfg.StorageRegion,
		AccessKey:    cfg.StorageAccessKey,
		SecretKey:    cfg.StorageSecretKey,
		UsePathStyle: cfg.StoragePathStyle,
	})
	if err != nil {
		return fmt.Errorf("initialize storage provider: %w", err)
	}
	webpCodec, err := storage.NewLibWebPCodec()
	if err != nil {
		return fmt.Errorf("initialize WebP codec: %w", err)
	}
	mediaService := media.NewService(pool, storageProvider, storage.ImageProcessor{Limits: storage.DefaultImageLimits(), Encoder: webpCodec, Decoder: webpCodec})
	dataExchangeService := dataimports.NewService(pool, storageProvider, inventoryService)
	productService := products.NewService(pool)
	pricingService := pricing.NewService(pool)
	taxService := taxes.NewService(pool)
	purchasingService := purchasing.NewService(pool, posting.InventoryStockPoster{Service: inventoryService}, posting.FinancePurchasePoster{Service: financeService}, exchangeService)
	reportingService := reporting.NewService(pool, financeService)
	preferenceService := preferences.NewService(pool)
	dashboardService := dashboard.NewService(pool)
	agendaService := agenda.NewService(pool)
	emailSettingsService, err := delivery.NewSMTPSettingsService(pool, cfg.MasterKey, cfg.Environment)
	if err != nil {
		return fmt.Errorf("initialize SMTP settings service: %w", err)
	}
	fixedAssetService := fixedasset.NewService(pool)
	legislationRepository := legislation.NewRepository(pool)
	hrEmployeeService, err := employee.NewService(pool, cfg.MasterKey, legislationRepository)
	if err != nil {
		return fmt.Errorf("initialize HR employee service: %w", err)
	}
	payrollLegislationService := legislation.NewService(pool)
	hrEmploymentService := employment.NewService(pool, legislationRepository)
	hrAdvanceService := advance.NewService(pool, financeService)
	hrDocumentService := document.NewService(pool, storageProvider)
	hrScheduleService := hrschedule.NewService(pool)
	hrLeaveService := leave.NewService(pool)
	hrCalendarService := calendar.NewService(pool)
	hrTimesheetService := hrtimesheet.NewService(pool)
	payrollRunService := payrollrun.NewService(pool, legislationRepository)
	payrollPaymentService := payrollpayment.NewService(pool, financeService)
	payrollDeliveryService, err := delivery.NewService(pool, storageProvider, cfg.MasterKey)
	if err != nil {
		return fmt.Errorf("initialize payroll delivery service: %w", err)
	}
	emailTemplateService := emailpkg.NewTemplateService(pool)
	emailComposeService, err := emailpkg.NewComposeService(pool, cfg.MasterKey)
	if err != nil {
		return fmt.Errorf("initialize email compose service: %w", err)
	}
	backupOptions := []httpapi.RouterOption{}
	backupEngine, err := backup.NewEngine(backup.Options{
		DatabaseURL:     cfg.DatabaseURL,
		AppDatabaseURL:  cfg.AppDatabaseURL,
		StorageRoot:     cfg.StorageRoot,
		StorageProvider: cfg.StorageProvider,
		Release:         cfg.Release,
		MasterKey:       cfg.MasterKey,
		PostgresBinDir:  cfg.PostgresBinDir,
	})
	switch {
	case errors.Is(err, backup.ErrToolMissing):
		logger.Warn("system backup disabled: postgresql-client not found in image")
	case err != nil:
		return fmt.Errorf("initialize backup engine: %w", err)
	default:
		backupOptions = append(backupOptions, httpapi.WithSystemBackup(backupEngine, controller, cfg.ControlDir))
	}
	// Pulse and system-update read cross-company/system state; they use the raw
	// pool so a request's company scope never narrows them. The two are
	// independent: system-update reads a public GitHub Releases catalog, not
	// the pulse collector, so it stays mounted even with pulse disabled.
	if cfg.PulseConfigured() {
		backupOptions = append(backupOptions, httpapi.WithPulse(pulse.NewService(rawPool, cfg)))
	}

	routerOptions := append([]httpapi.RouterOption{httpapi.WithIdentity(identityService, cfg.CookiesSecure()), httpapi.WithParty(partyService), httpapi.WithProducts(productService), httpapi.WithPricing(pricingService), httpapi.WithExchange(exchangeService), httpapi.WithTaxes(taxService), httpapi.WithPreferences(preferenceService), httpapi.WithDashboard(dashboardService), httpapi.WithAgenda(agendaService), httpapi.WithFinance(financeService), httpapi.WithInventory(inventoryService), httpapi.WithSales(salesService), httpapi.WithPurchasing(purchasingService), httpapi.WithMedia(mediaService), httpapi.WithSearch(httpapi.NewSearchService(pool)), httpapi.WithDataExchange(dataExchangeService), httpapi.WithReporting(reportingService), httpapi.WithEmailSettings(emailSettingsService), httpapi.WithHREmployee(hrEmployeeService), httpapi.WithHRAdvance(hrAdvanceService), httpapi.WithFixedAsset(fixedAssetService), httpapi.WithHREmployment(hrEmploymentService), httpapi.WithHRDocument(hrDocumentService), httpapi.WithHRSchedule(hrScheduleService), httpapi.WithHRLeave(hrLeaveService), httpapi.WithHRCalendar(hrCalendarService), httpapi.WithHRTimesheet(hrTimesheetService), httpapi.WithPayrollLegislation(payrollLegislationService), httpapi.WithLegislationRepository(legislationRepository), httpapi.WithPayrollRun(payrollRunService), httpapi.WithPayrollPayment(payrollPaymentService), httpapi.WithPayrollDelivery(payrollDeliveryService), httpapi.WithEmail(emailTemplateService, emailComposeService), httpapi.WithCompanyScope(rawPool)}, backupOptions...)
	// The demo endpoints exist only on the public showcase deployment; a normal
	// installation never mounts them.
	if cfg.DemoConfigured() {
		routerOptions = append(routerOptions, httpapi.WithDemo(httpapi.DemoRuntime{
			CompanyID: demo.CompanyID, UserID: demo.UserID, Runner: newDemoRunner(cfg, logger, rawPool),
			Email: cfg.DemoEmail, Password: cfg.DemoPassword,
		}))
	}
	routerOptions = append(routerOptions, extraRouterOptions...)
	server := &http.Server{Addr: cfg.HTTPAddr, Handler: httpapi.NewRouter(logger, cfg.Release, readiness{pool: rawPool, migrations: runner, controller: controller, storageRoot: cfg.StorageRoot}, routerOptions...), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 90 * time.Second}
	errCh := make(chan error, 1)
	go func() {
		logger.Info("api server starting", "address", cfg.HTTPAddr)
		errCh <- server.ListenAndServe()
	}()
	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve HTTP: %w", err)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		logger.Info("api server stopping")
		return server.Shutdown(shutdownCtx)
	}
}

// warnIfServingRoleBypassesRLS is a read-only, best-effort check of the role
// the server/worker actually serve traffic as. Row-level-security company
// isolation only holds when that role is neither a superuser nor has
// BYPASSRLS — set VARYAONE_APP_DATABASE_URL to the non-superuser varyaone_app
// role (see deploy.sh ensure_app_role) to get that guarantee at the database
// layer. This never blocks startup and never touches data: an installation
// that has deliberately chosen VARYAONE_ALLOW_SUPERUSER_FALLBACK, or has not
// migrated to the app role yet, keeps working exactly as before — it only
// gets a log line naming the gap instead of an unannounced one.
func warnIfServingRoleBypassesRLS(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) {
	var bypasses bool
	err := pool.QueryRow(ctx, `SELECT rolsuper OR rolbypassrls FROM pg_roles WHERE rolname = current_user`).Scan(&bypasses)
	if err != nil {
		logger.Warn("could not check whether the serving database role bypasses row-level security", "error", err)
		return
	}
	if bypasses {
		logger.Warn("the serving database role is a superuser or has BYPASSRLS: company isolation between tenants is enforced only by the application, not by the database. Set VARYAONE_APP_DATABASE_URL to a non-superuser role without BYPASSRLS (see deploy.sh ensure_app_role / internal/platform/migrations/app_role.sql) to close this gap.")
	}
}
