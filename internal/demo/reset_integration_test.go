package demo

import (
	"bytes"
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/platform/database"
	"github.com/alpyxn/varyaone/internal/platform/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestResetSchedulingAndLocking covers the machinery the shared demo depends
// on: the recorded schedule the worker polls, the cooldown that stops one
// visitor wiping the data under everyone repeatedly, and the lock that keeps
// two resets from running at once.
func TestResetSchedulingAndLocking(t *testing.T) {
	databaseURL := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err = migrations.New(pool).Up(ctx); err != nil {
		t.Fatal(err)
	}
	runner := New(pool, Options{
		MaintenanceDSN: databaseURL,
		MasterKey:      bytes.Repeat([]byte{9}, 32),
		Email:          "demo@varyaone.test",
		Password:       "varyaone-demo-2026",
		Now:            func() time.Time { return time.Date(2026, 6, 15, 9, 0, 0, 0, time.UTC) },
		ResetInterval:  2 * time.Hour,
		// A one-second cooldown keeps the test honest about the rule while
		// staying fast; production uses minutes.
		ResetCooldown: time.Second,
	})
	if err = runner.Ensure(ctx); err != nil {
		t.Fatal(err)
	}

	// Rebuild once so the schedule is this test's own: Ensure deliberately
	// keeps an existing next-reset time (a restart must not postpone a reset),
	// so a database another test already seeded would leave a partly elapsed
	// schedule here.
	if err = runner.Reset(ctx); err != nil {
		t.Fatal(err)
	}

	// A freshly rebuilt demo is ready and has its next reset scheduled, so the
	// settings screen can show a countdown and the worker knows when to act.
	state, err := runner.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Ready() {
		t.Fatalf("state after seeding = %q, want READY", state.Status)
	}
	if state.NextResetAt == nil {
		t.Fatal("no next reset was scheduled")
	}
	if delta := time.Until(*state.NextResetAt); delta < 119*time.Minute || delta > 2*time.Hour {
		t.Fatalf("next reset is %v away, want about two hours", delta)
	}
	due, err := runner.DueForReset(ctx)
	if err != nil || due {
		t.Fatalf("a just-seeded demo reported due=%v err=%v", due, err)
	}

	// A rebuild counts as a reset, so a visitor cannot immediately wipe a demo
	// that was just rebuilt.
	if err = runner.RequestReset(ctx); !errors.Is(err, ErrResetTooSoon) {
		t.Fatalf("reset right after seeding returned %v, want ErrResetTooSoon", err)
	}
	time.Sleep(1100 * time.Millisecond)
	if err = runner.RequestReset(ctx); err != nil {
		t.Fatalf("visitor reset after the cooldown failed: %v", err)
	}
	if err = runner.RequestReset(ctx); !errors.Is(err, ErrResetTooSoon) {
		t.Fatalf("second visitor reset returned %v, want ErrResetTooSoon", err)
	}

	// Two concurrent resets: one runs, the other is turned away rather than
	// both purging the same company at once.
	var wg sync.WaitGroup
	results := make([]error, 2)
	for i := range results {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			results[index] = runner.Reset(ctx)
		}(i)
	}
	wg.Wait()
	var succeeded, rejected int
	for _, result := range results {
		switch {
		case result == nil:
			succeeded++
		case errors.Is(result, ErrResetInProgress):
			rejected++
		default:
			t.Fatalf("concurrent reset failed unexpectedly: %v", result)
		}
	}
	if succeeded != 1 || rejected != 1 {
		t.Fatalf("concurrent resets: %d succeeded, %d rejected; want exactly one of each", succeeded, rejected)
	}
	assertSeeded(t, ctx, pool)
}

// TestResetIntervalZeroDisablesTheTimer proves the schedule can be turned off:
// a deployment that resets only by hand must never be rebuilt underneath its
// visitors.
func TestResetIntervalZeroDisablesTheTimer(t *testing.T) {
	databaseURL := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err = migrations.New(pool).Up(ctx); err != nil {
		t.Fatal(err)
	}
	runner := New(pool, Options{
		MaintenanceDSN: databaseURL,
		MasterKey:      bytes.Repeat([]byte{9}, 32),
		Email:          "demo@varyaone.test",
		Password:       "varyaone-demo-2026",
		Now:            func() time.Time { return time.Date(2026, 6, 15, 9, 0, 0, 0, time.UTC) },
	})
	if err = runner.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	due, err := runner.DueForReset(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if due {
		t.Fatal("a demo with no reset interval reported itself due for reset")
	}
}

// TestEnsureReconcilesTheDemoAccount covers the failure a visitor sees first:
// the login screen offers the configured demo credentials, so an installation
// whose stored account drifted from them (a changed environment variable, a
// database seeded elsewhere) would tell every visitor their password is wrong.
func TestEnsureReconcilesTheDemoAccount(t *testing.T) {
	databaseURL := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err = migrations.New(pool).Up(ctx); err != nil {
		t.Fatal(err)
	}
	masterKey := bytes.Repeat([]byte{9}, 32)
	options := Options{
		MaintenanceDSN: databaseURL,
		MasterKey:      masterKey,
		Email:          "first@varyaone.test",
		Password:       "varyaone-demo-2026",
		Now:            func() time.Time { return time.Date(2026, 6, 15, 9, 0, 0, 0, time.UTC) },
	}
	if err = New(pool, options).Ensure(ctx); err != nil {
		t.Fatal(err)
	}

	// The deployment changes its demo account; the company already exists.
	options.Email = "second@varyaone.test"
	options.Password = "baska-bir-demo-parolasi"
	if err = New(pool, options).Ensure(ctx); err != nil {
		t.Fatal(err)
	}

	identityService, err := identity.NewService(database.NewScoped(pool), masterKey)
	if err != nil {
		t.Fatal(err)
	}
	session, err := identityService.Login(ctx, options.Email, options.Password, "", identity.RequestMeta{TraceID: "demo-reconcile-test"})
	if err != nil {
		t.Fatalf("the reconciled demo account could not sign in: %v", err)
	}
	// Which company a password login lands on is not this test's business (the
	// account may belong to more than one); what matters is that the demo
	// company is reachable from the reconciled account.
	member := false
	for _, company := range session.Companies {
		if company.ID == CompanyID {
			member = true
		}
	}
	if !member {
		t.Fatalf("the reconciled demo account is not a member of the demo company: %+v", session.Companies)
	}
}
