package backup

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// The write barrier and the shared snapshot.
//
// A backup used to be two independent readings of the installation: pg_dump
// produced a transactionally consistent database, and a separate directory walk
// produced a file tree. Each was fine on its own; together they described two
// different moments. A file uploaded between the two appeared in the archive
// with no row referring to it. A file deleted between them left a row pointing
// at nothing. Neither shows up as an error — the backup verifies perfectly and
// is quietly wrong.
//
// The fix has two halves:
//
//   - An exported database snapshot. A repeatable-read transaction publishes
//     its snapshot id and holds it open; pg_dump joins that same snapshot with
//     --snapshot. The dump is therefore the database exactly as of the instant
//     the transaction began, not as of whenever pg_dump got round to starting.
//
//   - A write barrier taken at that same instant, held only while the storage
//     tree is hard-linked aside. Application writers hold the barrier in shared
//     mode for the duration of a mutating request; the backup takes it
//     exclusively. Hard-linking is a directory walk, not a copy, so the pause is
//     short — and it is a real pause, not a claim of one.
//
// The barrier is a PostgreSQL advisory lock rather than a file or an in-memory
// flag. Every writer in this system already holds a database connection, an
// advisory lock is visible across processes and hosts, and the server releases
// it if the holder's connection dies — which is exactly the failure mode a
// hand-rolled barrier gets wrong.

// WriteBarrierKey is the advisory lock id shared by the backup engine and every
// application writer. It must never collide with the migration lock.
const WriteBarrierKey int64 = 867_972_002

// barrierAcquireTimeout bounds how long the backup waits for in-flight writes
// to finish. A long-running write should delay a backup, not stall it forever;
// a backup that cannot get a quiet moment must say so rather than hang.
const barrierAcquireTimeout = 30 * time.Second

// ErrBarrierTimeout is returned when in-flight writers did not finish in time.
var ErrBarrierTimeout = errors.New("yazma işlemleri zamanında tamamlanmadı; tutarlı yedek alınamadı")

// sourceSnapshot is an open repeatable-read transaction whose snapshot pg_dump
// will reuse, plus the barrier taken at the same instant.
type sourceSnapshot struct {
	conn       *pgx.Conn
	snapshotID string
	// barrierConn is a second connection: the barrier must be released as soon
	// as the storage tree is pinned, while the snapshot transaction stays open
	// for the whole of pg_dump.
	barrierConn *pgx.Conn
	held        bool
}

// beginSourceSnapshot opens the snapshot transaction and takes the write
// barrier. On return the database view is fixed and writers are paused; the
// caller must pin the storage tree and then call releaseBarrier promptly.
func (e *Engine) beginSourceSnapshot(ctx context.Context) (*sourceSnapshot, error) {
	barrierConn, err := pgx.Connect(ctx, e.databaseURL)
	if err != nil {
		return nil, fmt.Errorf("yazma bariyeri bağlantısı kurulamadı: %w", err)
	}
	snapshot := &sourceSnapshot{barrierConn: barrierConn}

	// Take the barrier first. Opening the snapshot inside the barrier is what
	// makes the database view and the file view the same instant; the other
	// order leaves a window in which a write lands in one but not the other.
	barrierCtx, cancel := context.WithTimeout(ctx, barrierAcquireTimeout)
	defer cancel()
	if _, err = barrierConn.Exec(barrierCtx, `SELECT pg_advisory_lock($1)`, WriteBarrierKey); err != nil {
		snapshot.Close(ctx)
		if barrierCtx.Err() != nil {
			return nil, ErrBarrierTimeout
		}
		return nil, fmt.Errorf("yazma bariyeri alınamadı: %w", err)
	}
	snapshot.held = true

	conn, err := pgx.Connect(ctx, e.databaseURL)
	if err != nil {
		snapshot.Close(ctx)
		return nil, fmt.Errorf("snapshot bağlantısı kurulamadı: %w", err)
	}
	snapshot.conn = conn
	if _, err = conn.Exec(ctx, `BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY`); err != nil {
		snapshot.Close(ctx)
		return nil, err
	}
	if err = conn.QueryRow(ctx, `SELECT pg_export_snapshot()`).Scan(&snapshot.snapshotID); err != nil {
		snapshot.Close(ctx)
		return nil, fmt.Errorf("veritabanı snapshot'ı dışa aktarılamadı: %w", err)
	}
	return snapshot, nil
}

// releaseBarrier lets writers continue. The snapshot transaction stays open, so
// pg_dump still sees the pinned view; only the pause ends.
func (s *sourceSnapshot) releaseBarrier(ctx context.Context) {
	if !s.held {
		return
	}
	s.held = false
	_, _ = s.barrierConn.Exec(ctx, `SELECT pg_advisory_unlock($1)`, WriteBarrierKey)
}

// meta reads the schema version and server version inside the snapshot, so the
// metadata describes the same instant as the data. Reading it afterwards on a
// separate connection could report a version a concurrent migration had moved
// past.
func (s *sourceSnapshot) meta(ctx context.Context) (migrationVersion int64, serverNum int, err error) {
	if err = s.conn.QueryRow(ctx, `SELECT COALESCE(MAX(version), 0) FROM platform_schema_migrations`).Scan(&migrationVersion); err != nil {
		return 0, 0, fmt.Errorf("read migration version: %w", err)
	}
	if err = s.conn.QueryRow(ctx, `SELECT current_setting('server_version_num')::int`).Scan(&serverNum); err != nil {
		return 0, 0, fmt.Errorf("read server version: %w", err)
	}
	return migrationVersion, serverNum, nil
}

func (s *sourceSnapshot) Close(ctx context.Context) {
	detached := context.WithoutCancel(ctx)
	s.releaseBarrier(detached)
	if s.conn != nil {
		_, _ = s.conn.Exec(detached, `ROLLBACK`)
		_ = s.conn.Close(detached)
		s.conn = nil
	}
	if s.barrierConn != nil {
		_ = s.barrierConn.Close(detached)
		s.barrierConn = nil
	}
}

// Executor is the narrow slice of a database connection the barrier needs. It
// is declared here rather than imported so callers can pass a pooled
// connection, a raw connection or a transaction without this package caring.
type Executor interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// AcquireWriteBarrier blocks until no backup is pinning the installation, then
// holds the barrier in shared mode on conn until ReleaseWriteBarrier.
//
// Shared mode means writers never block each other: only a backup's exclusive
// hold makes them wait, and only for as long as it takes to pin the storage
// tree. Callers must hold it on the SAME connection they write with, and must
// release it — the lock is session-scoped, so a connection returned to a pool
// still holding it would block the next backup indefinitely.
func AcquireWriteBarrier(ctx context.Context, conn Executor) error {
	_, err := conn.Exec(ctx, `SELECT pg_advisory_lock_shared($1)`, WriteBarrierKey)
	if err != nil {
		return fmt.Errorf("yazma bariyeri alınamadı: %w", err)
	}
	return nil
}

// ReleaseWriteBarrier drops a shared hold. It takes its own context because it
// runs on the way out, often after the request's context is already cancelled.
func ReleaseWriteBarrier(conn Executor) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = conn.Exec(ctx, `SELECT pg_advisory_unlock_shared($1)`, WriteBarrierKey)
}
