// Package demo builds and rebuilds the shared showcase company.
//
// The demo deployment is a separate installation with its own database; nothing
// here ever runs against a normal one. Two things enforce that: the caller must
// opt in through VARYAONE_DEMO_MODE, and every destructive step goes through
// identity.PurgeDemoCompany, which refuses any company not flagged is_demo
// (migration 000151).
//
// Data is produced by calling the real domain services, never by inserting
// rows. Documents therefore go through the same posting chain as a user's:
// stock movements, party ledger entries, finance postings and their idempotency
// keys all exist and agree, so reports and balances in the demo are as correct
// as in production. Hand-written INSERTs would leave the ledgers inconsistent
// and every report screen wrong.
package demo

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/alpyxn/varyaone/internal/finance"
	"github.com/alpyxn/varyaone/internal/fixedasset"
	"github.com/alpyxn/varyaone/internal/hr/employee"
	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/inventory"
	"github.com/alpyxn/varyaone/internal/party"
	"github.com/alpyxn/varyaone/internal/platform/database"
	"github.com/alpyxn/varyaone/internal/platform/posting"
	"github.com/alpyxn/varyaone/internal/products"
	"github.com/alpyxn/varyaone/internal/purchasing"
	"github.com/alpyxn/varyaone/internal/sales"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The demo company and user carry fixed identifiers so a reseed recreates the
// same entities. Links people share into the demo keep working across resets,
// and the reset itself has a single, well-known target.
const (
	CompanyID = "d0e00000-0000-4000-8000-000000000001"
	UserID    = "d0e00000-0000-4000-8000-000000000002"

	companyLegalName = "Varya Demo Ticaret Anonim Şirketi"
	companyTradeName = "Varya Demo"
)

// Options configures a Runner. MaintenanceDSN is the owner/superuser connection
// a reset needs to purge past RLS and the immutability triggers; without it
// Reset fails and Ensure still works.
type Options struct {
	MaintenanceDSN string
	MasterKey      []byte
	Email          string
	Password       string
	Logger         *slog.Logger
	// Now anchors the generated dates. Documents are dated relative to it, so
	// the demo never looks abandoned; tests pin it for reproducibility.
	Now func() time.Time
	// ResetInterval is how often the demo is rebuilt. Zero leaves resetting to
	// explicit commands.
	ResetInterval time.Duration
	// ResetCooldown is the shortest gap between two visitor-triggered resets.
	// Rebuilding is cheap but not free, and one visitor should not be able to
	// wipe the data under everyone else repeatedly.
	ResetCooldown time.Duration
}

type Runner struct {
	pool *pgxpool.Pool
	opts Options
}

func New(pool *pgxpool.Pool, opts Options) *Runner {
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Runner{pool: pool, opts: opts}
}

// Ensure provisions and seeds the demo company when it is missing, and does
// nothing when it is already there. It is safe to call on every start-up.
func (r *Runner) Ensure(ctx context.Context) error {
	identityService, err := r.identityService()
	if err != nil {
		return err
	}
	exists, err := identityService.DemoCompanyExists(ctx, CompanyID)
	if err != nil {
		return err
	}
	if exists {
		// The company survived a restart. Make sure it still has a reset
		// scheduled (or the worker would find no due time and rebuild a
		// perfectly good demo the moment it starts) and that the demo account
		// still matches the configured credentials, which the login screen
		// shows filled in.
		if err = identityService.ReconcileDemoUser(ctx, r.demoInput()); err != nil {
			return err
		}
		return r.ensureSchedule(ctx)
	}
	if err = r.provisionAndSeed(ctx, identityService); err != nil {
		return err
	}
	return r.markReady(ctx)
}

// Reset purges the demo company and builds it again from scratch. This is what
// runs on the reset timer and behind the "reset the demo" action: everyone
// shares one company, so a periodic rebuild is the only thing that keeps one
// visitor's mess from becoming the next visitor's first impression.
//
// While it runs, the shared state says RESETTING so the API can tell visitors
// the demo is being rebuilt rather than failing with whatever error a
// half-purged company produces. Sessions themselves survive a reset - they are
// not company-scoped rows - but the company they point at does not, so clients
// re-enter the rebuilt demo afterwards.
func (r *Runner) Reset(ctx context.Context) error {
	return r.withResetLock(ctx, func(ctx context.Context) error {
		identityService, err := r.identityService()
		if err != nil {
			return err
		}
		if err = r.markResetting(ctx); err != nil {
			return err
		}
		if err = identityService.PurgeDemoCompany(ctx, CompanyID); err != nil {
			return fmt.Errorf("purge demo company: %w", err)
		}
		if err = r.provisionAndSeed(ctx, identityService); err != nil {
			return err
		}
		return r.markReady(ctx)
	})
}

// ErrResetTooSoon is returned when a visitor asks for a reset inside the
// cooldown window.
var ErrResetTooSoon = errors.New("demo was reset too recently")

// RequestReset is the visitor-facing reset: the same rebuild, refused if the
// demo was already rebuilt within the cooldown window.
func (r *Runner) RequestReset(ctx context.Context) error {
	state, err := r.State(ctx)
	if err != nil {
		return err
	}
	if state.Status == statusResetting {
		return ErrResetInProgress
	}
	if r.opts.ResetCooldown > 0 && state.LastResetAt != nil && time.Since(*state.LastResetAt) < r.opts.ResetCooldown {
		return ErrResetTooSoon
	}
	return r.Reset(ctx)
}

func (r *Runner) identityService() (*identity.Service, error) {
	options := []identity.Option{}
	if r.opts.MaintenanceDSN != "" {
		options = append(options, identity.WithMaintenanceDSN(r.opts.MaintenanceDSN))
	}
	service, err := identity.NewService(database.NewScoped(r.pool), r.opts.MasterKey, options...)
	if err != nil {
		return nil, fmt.Errorf("initialize identity service: %w", err)
	}
	return service, nil
}

func (r *Runner) provisionAndSeed(ctx context.Context, identityService *identity.Service) error {
	started := time.Now()
	session, err := identityService.ProvisionDemoCompany(ctx, r.demoInput(), identity.RequestMeta{TraceID: "demo-seed"})
	if err != nil {
		return fmt.Errorf("provision demo company: %w", err)
	}
	if err = r.seed(ctx, session); err != nil {
		return fmt.Errorf("seed demo data: %w", err)
	}
	r.opts.Logger.Info("demo company seeded", "company_id", CompanyID, "duration", time.Since(started).Round(time.Millisecond))
	return nil
}

// demoInput is the fixed identity of the demo company and its account, used
// both when creating it and when reconciling an existing one.
func (r *Runner) demoInput() identity.DemoCompanyInput {
	return identity.DemoCompanyInput{
		CompanyID:   CompanyID,
		UserID:      UserID,
		Email:       r.opts.Email,
		DisplayName: "Demo Kullanıcı",
		Password:    r.opts.Password,
		LegalName:   companyLegalName,
		TradeName:   companyTradeName,
	}
}

// services are the domain services the seeder drives. They are built here
// rather than reused from the server wiring so seeding can run from the CLI
// without an HTTP server.
type services struct {
	party      *party.Service
	products   *products.Service
	finance    *finance.Service
	inventory  *inventory.Service
	purchasing *purchasing.Service
	sales      *sales.Service
	employee   *employee.Service
	fixedAsset *fixedasset.Service
}

// services builds the domain services the seeder drives. No exchange-rate
// resolver is wired in: every seeded document is in the company's base currency,
// so nothing here ever needs a rate.
func (r *Runner) services() (*services, error) {
	pool := database.NewScoped(r.pool)
	financeService := finance.NewService(pool)
	inventoryService := inventory.NewService(pool)
	employeeService, err := employee.NewService(pool, r.opts.MasterKey)
	if err != nil {
		return nil, fmt.Errorf("initialize HR employee service: %w", err)
	}
	return &services{
		party:      party.NewService(pool),
		products:   products.NewService(pool),
		finance:    financeService,
		inventory:  inventoryService,
		purchasing: purchasing.NewService(pool, posting.InventoryStockPoster{Service: inventoryService}, posting.FinancePurchasePoster{Service: financeService}),
		sales:      sales.NewService(pool, financeService, posting.InventoryStockPoster{Service: inventoryService}),
		employee:   employeeService,
		fixedAsset: fixedasset.NewService(pool),
	}, nil
}
