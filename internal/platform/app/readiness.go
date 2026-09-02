package app

import (
	"context"
	"fmt"

	"github.com/alpyxn/varyaone/internal/platform/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

type readiness struct {
	pool       *pgxpool.Pool
	migrations *migrations.Runner
}

func (r readiness) Check(ctx context.Context) error {
	if err := r.pool.Ping(ctx); err != nil {
		return fmt.Errorf("database ping: %w", err)
	}
	if err := r.migrations.IsCurrent(ctx); err != nil {
		return fmt.Errorf("migration state: %w", err)
	}
	return nil
}
