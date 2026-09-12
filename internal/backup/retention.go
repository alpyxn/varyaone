package backup

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Retention.
//
// Every candidate restore leaves the previous database behind under a
// timestamped name. That is deliberate — it is the only real rollback point
// once a restore has committed — but it is also unbounded, and a copy of the
// whole database per restore will eventually fill the volume. Filling the
// database volume is not a tidiness problem: PostgreSQL stops accepting writes.
//
// So retained databases are pruned, under rules that keep the reason they exist
// intact:
//
//   - The newest retained database is NEVER dropped, however old it is. It is
//     the rollback point for the restore that is currently live.
//   - Nothing inside the protection window is dropped, so a problem discovered
//     a few days later still has something to go back to.
//   - Only databases this engine created are considered, matched by the naming
//     scheme AND by parsing a valid timestamp out of the name. A database an
//     operator happens to have called something similar is not ours to drop.
//
// Pruning is never automatic during a restore. It runs as its own command, so
// dropping data is always something someone asked for.

// RetentionPolicy bounds how much history is kept.
type RetentionPolicy struct {
	// KeepDatabases is how many retained databases to keep, newest first.
	// Values below 1 are raised to 1: the newest is the live rollback point.
	KeepDatabases int
	// ProtectFor keeps anything younger than this regardless of count.
	ProtectFor time.Duration
}

// DefaultRetention is deliberately generous. A retained database costs disk;
// not having one costs the ability to undo a restore.
var DefaultRetention = RetentionPolicy{KeepDatabases: 3, ProtectFor: 14 * 24 * time.Hour}

// RetainedDatabase is one previous database kept after a restore.
type RetainedDatabase struct {
	Name      string
	RetiredAt time.Time
	Bytes     int64
}

// ListRetainedDatabases returns the databases previous restores set aside,
// newest first.
func (e *Engine) ListRetainedDatabases(ctx context.Context) ([]RetainedDatabase, error) {
	session, err := e.openMaintenance(ctx)
	if err != nil {
		return nil, err
	}
	defer session.Close(ctx)
	return session.listRetained(ctx)
}

func (s *candidateSession) listRetained(ctx context.Context) ([]RetainedDatabase, error) {
	prefix := s.liveName + "_old_"
	rows, err := s.conn.Query(ctx, `
		SELECT datname, pg_database_size(datname)
		FROM pg_database
		WHERE datname LIKE $1`, prefix+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var retained []RetainedDatabase
	for rows.Next() {
		var name string
		var size int64
		if err := rows.Scan(&name, &size); err != nil {
			return nil, err
		}
		stamp, ok := parseRetiredStamp(name, prefix)
		if !ok {
			// Matches the prefix but not the format. Not ours; leave it alone.
			continue
		}
		retained = append(retained, RetainedDatabase{Name: name, RetiredAt: stamp, Bytes: size})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(retained, func(i, j int) bool { return retained[i].RetiredAt.After(retained[j].RetiredAt) })
	return retained, nil
}

func parseRetiredStamp(name, prefix string) (time.Time, bool) {
	if !strings.HasPrefix(name, prefix) {
		return time.Time{}, false
	}
	stamp, err := time.Parse("20060102T150405Z", strings.TrimPrefix(name, prefix))
	if err != nil {
		return time.Time{}, false
	}
	return stamp, true
}

// PruneRetainedDatabases drops retained databases the policy no longer keeps
// and returns the names it dropped.
func (e *Engine) PruneRetainedDatabases(ctx context.Context, policy RetentionPolicy, dryRun bool) ([]string, error) {
	if policy.KeepDatabases < 1 {
		policy.KeepDatabases = 1
	}
	session, err := e.openMaintenance(ctx)
	if err != nil {
		return nil, err
	}
	defer session.Close(ctx)

	retained, err := session.listRetained(ctx)
	if err != nil {
		return nil, err
	}
	cutoff := e.now().Add(-policy.ProtectFor)
	var dropped []string
	for index, database := range retained {
		if index < policy.KeepDatabases {
			continue
		}
		if database.RetiredAt.After(cutoff) {
			continue
		}
		if dryRun {
			dropped = append(dropped, database.Name)
			continue
		}
		if err := session.drop(ctx, database.Name); err != nil {
			return dropped, fmt.Errorf("eski veritabanı %q silinemedi: %w", database.Name, err)
		}
		dropped = append(dropped, database.Name)
	}
	return dropped, nil
}

// PromoteRetainedDatabase makes a retained database live again.
//
// This is the rollback that the retained databases exist for, and it is a
// deliberate, separate act rather than something a failed restore does on its
// own. Once traffic has been served from the restored database, rolling back
// discards whatever was written since — so the decision belongs to a person who
// knows what those writes were.
func (e *Engine) PromoteRetainedDatabase(ctx context.Context, name string) error {
	session, err := e.openMaintenance(ctx)
	if err != nil {
		return err
	}
	defer session.Close(ctx)

	if _, ok := parseRetiredStamp(name, session.liveName+"_old_"); !ok {
		return fmt.Errorf("%q bu kurulumun sakladığı bir veritabanı değil", name)
	}
	exists, err := session.exists(ctx, name)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("veritabanı bulunamadı: %s", name)
	}
	// The database being replaced is itself retained, not dropped: rolling back
	// must not become a new way to lose data.
	replaced := retiredName(session.liveName, e.now().UTC())
	return session.swap(ctx, name, replaced)
}
