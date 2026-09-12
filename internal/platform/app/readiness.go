package app

import (
	"context"
	"fmt"
	"os"

	"github.com/alpyxn/varyaone/internal/platform/migrations"
	"github.com/alpyxn/varyaone/internal/platform/opctl"
	"github.com/jackc/pgx/v5/pgxpool"
)

// readiness answers /health/ready, which is what Docker's healthcheck, the
// deploy script's gates and any external monitor use to decide whether this
// process should receive traffic.
//
// It checks the things whose failure would make serving traffic wrong rather
// than merely slow. A database it cannot reach and a schema it does not
// understand are obvious. The other two are the ones a restore breaks: the
// operation record, because an interrupted restore can be discovered after the
// process started; and the storage root, because a swapped-in file tree that is
// missing or unwritable turns every document the database references into a
// broken link — and nothing else notices until a user clicks one.
type readiness struct {
	pool        *pgxpool.Pool
	migrations  *migrations.Runner
	controller  *opctl.Controller
	storageRoot string
}

func (r readiness) Check(ctx context.Context) error {
	if err := r.pool.Ping(ctx); err != nil {
		return fmt.Errorf("database ping: %w", err)
	}
	if err := r.migrations.IsCurrent(ctx); err != nil {
		return fmt.Errorf("migration state: %w", err)
	}
	if r.controller != nil {
		status, err := r.controller.Status()
		if err != nil {
			return fmt.Errorf("operation state: %w", err)
		}
		if !status.Serviceable {
			return fmt.Errorf("operation state: %s", status.Reason)
		}
	}
	if err := r.checkStorage(); err != nil {
		return fmt.Errorf("storage: %w", err)
	}
	return nil
}

// checkStorage proves the storage root is a directory this process can actually
// write to, by writing. A stat alone would pass on a read-only mount and on a
// tree owned by another user.
func (r readiness) checkStorage() error {
	if r.storageRoot == "" {
		return nil
	}
	info, err := os.Stat(r.storageRoot)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", r.storageRoot)
	}
	probe, err := os.CreateTemp(r.storageRoot, ".varya-readiness-*")
	if err != nil {
		return err
	}
	name := probe.Name()
	closeErr := probe.Close()
	_ = os.Remove(name)
	return closeErr
}
