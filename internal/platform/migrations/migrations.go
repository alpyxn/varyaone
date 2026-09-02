package migrations

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

//go:embed sql/*.sql
var migrationFiles embed.FS

const advisoryLockID int64 = 867_972_001

var ErrPending = errors.New("database migrations are pending")

type DB interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
	Begin(context.Context) (pgx.Tx, error)
}

type Runner struct {
	db DB
}

type Status struct {
	Current int64
	Latest  int64
	Pending int
}

type migration struct {
	Version int64
	Name    string
	SQL     string
}

func New(db DB) *Runner { return &Runner{db: db} }

// Latest returns the highest migration version embedded in this binary. It is
// used by the backup engine to reject restoring an archive whose schema is
// newer than the running binary understands.
func Latest() (int64, error) {
	items, err := load()
	if err != nil {
		return 0, err
	}
	if len(items) == 0 {
		return 0, nil
	}
	return items[len(items)-1].Version, nil
}

func (r *Runner) Up(ctx context.Context) error {
	migrations, err := load()
	if err != nil {
		return err
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin migration transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, advisoryLockID); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	if err := ensureTable(ctx, tx); err != nil {
		return err
	}
	applied, err := appliedVersions(ctx, tx)
	if err != nil {
		return err
	}
	for _, item := range migrations {
		if _, exists := applied[item.Version]; exists {
			continue
		}
		if _, err = tx.Exec(ctx, item.SQL); err == nil {
			_, err = tx.Exec(ctx, `INSERT INTO platform_schema_migrations (version, name) VALUES ($1, $2)`, item.Version, item.Name)
		}
		if err != nil {
			return fmt.Errorf("apply migration %d: %w", item.Version, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit migrations: %w", err)
	}
	return nil
}

func appliedVersions(ctx context.Context, db interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}) (map[int64]struct{}, error) {
	rows, err := db.Query(ctx, `SELECT version FROM platform_schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("read applied migration versions: %w", err)
	}
	defer rows.Close()
	versions := map[int64]struct{}{}
	for rows.Next() {
		var version int64
		if err := rows.Scan(&version); err != nil {
			return nil, fmt.Errorf("scan applied migration version: %w", err)
		}
		versions[version] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read applied migration versions: %w", err)
	}
	return versions, nil
}

func (r *Runner) Status(ctx context.Context) (Status, error) {
	items, err := load()
	if err != nil {
		return Status{}, err
	}
	latest := int64(0)
	if len(items) > 0 {
		latest = items[len(items)-1].Version
	}
	exists, err := r.tableExists(ctx)
	if err != nil {
		return Status{}, err
	}
	currentVersion := int64(0)
	applied := map[int64]struct{}{}
	if exists {
		currentVersion, err = current(ctx, r.db)
		if err != nil {
			return Status{}, err
		}
		applied, err = appliedVersions(ctx, r.db)
		if err != nil {
			return Status{}, err
		}
	}
	pending := 0
	for _, item := range items {
		if _, ok := applied[item.Version]; !ok {
			pending++
		}
	}
	return Status{Current: currentVersion, Latest: latest, Pending: pending}, nil
}

func (r *Runner) IsCurrent(ctx context.Context) error {
	status, err := r.Status(ctx)
	if err != nil {
		return err
	}
	if status.Pending > 0 {
		return ErrPending
	}
	return nil
}

type execer interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func ensureTable(ctx context.Context, db execer) error {
	_, err := db.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS platform_schema_migrations (
			version bigint PRIMARY KEY,
			name text NOT NULL,
			applied_at timestamptz NOT NULL DEFAULT now()
		)`)
	if err != nil {
		return fmt.Errorf("ensure migration table: %w", err)
	}
	return nil
}

func (r *Runner) tableExists(ctx context.Context) (bool, error) {
	var exists bool
	if err := r.db.QueryRow(ctx, `SELECT to_regclass('platform_schema_migrations') IS NOT NULL`).Scan(&exists); err != nil {
		return false, fmt.Errorf("check migration table: %w", err)
	}
	return exists, nil
}

type rowQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func current(ctx context.Context, db rowQuerier) (int64, error) {
	var version int64
	if err := db.QueryRow(ctx, `SELECT COALESCE(MAX(version), 0) FROM platform_schema_migrations`).Scan(&version); err != nil {
		return 0, fmt.Errorf("read migration status: %w", err)
	}
	return version, nil
}

func load() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFiles, "sql")
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}
	items := make([]migration, 0, len(entries))
	seen := map[int64]struct{}{}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sql" {
			continue
		}
		parts := strings.SplitN(strings.TrimSuffix(entry.Name(), ".sql"), "_", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid migration filename %q", entry.Name())
		}
		version, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil || version <= 0 {
			return nil, fmt.Errorf("invalid migration version in %q", entry.Name())
		}
		if _, duplicate := seen[version]; duplicate {
			return nil, fmt.Errorf("duplicate migration version %d", version)
		}
		seen[version] = struct{}{}
		contents, err := migrationFiles.ReadFile("sql/" + entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", entry.Name(), err)
		}
		items = append(items, migration{Version: version, Name: parts[1], SQL: string(contents)})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Version < items[j].Version })
	return items, nil
}
