package backup

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// The candidate-database restore.
//
// The old approach loaded the archive straight into the live database with
// `pg_restore --clean --if-exists`. Two things are wrong with that, and neither
// is fixable by being more careful:
//
//   - `--clean` drops only the objects the archive itself contains. Restoring an
//     older backup onto a newer schema therefore leaves every table the new
//     schema added still present, holding the old installation's rows, wired to
//     nothing. The result is a database that is neither the backup nor what was
//     there before.
//   - The live database is the thing being written to. From the moment
//     pg_restore starts until it finishes there is no intact copy to go back to
//     except the safety backup, and the window is as long as the restore.
//
// Restoring into a fresh database and swapping it in at the end fixes both. The
// candidate is built, migrated, privilege-checked and actually connected to
// before anything live is touched; the switch itself is two renames. Failure at
// any point before the renames leaves the installation exactly as it was, and
// the previous database is kept afterwards rather than dropped.

// ErrCandidateUnsupported is returned when this installation cannot create a
// database — no CREATEDB privilege, or a managed provider that forbids it.
//
// It is deliberately an error rather than a silent fall back to the in-place
// path. The in-place path has materially weaker guarantees, and an operator who
// believes they are getting a staged restore should not quietly receive a
// destructive one.
var ErrCandidateUnsupported = errors.New("bu kurulumda aday veritabanı oluşturulamıyor")

// RestoreMode selects how the database half of a restore is applied.
type RestoreMode string

const (
	// ModeCandidate builds a new database and swaps it in. The default.
	ModeCandidate RestoreMode = "candidate"
	// ModeInPlace loads the archive directly into the live database. It exists
	// for installations that cannot create databases, and it must be asked for
	// explicitly: it cannot roll back, and it cannot remove objects the archive
	// does not know about.
	ModeInPlace RestoreMode = "in-place"
)

// maintenanceDSN returns the given DSN pointed at the `postgres` database.
// Renaming a database requires a connection that is not to that database.
func maintenanceDSN(dsn string) (string, error) {
	parsed, err := url.Parse(dsn)
	if err != nil {
		return "", err
	}
	parsed.Path = "/postgres"
	return parsed.String(), nil
}

// withDatabase returns the given DSN pointed at another database, preserving
// credentials and every connection parameter.
func withDatabase(dsn, name string) (string, error) {
	parsed, err := url.Parse(dsn)
	if err != nil {
		return "", err
	}
	parsed.Path = "/" + name
	return parsed.String(), nil
}

// quoteIdentifier renders a SQL identifier safely. Database names come from the
// installation's own configuration rather than from an archive, but they still
// reach the server as SQL text, and "it cannot contain a quote today" is not a
// property worth depending on.
func quoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// candidateSession owns the maintenance connection used to create, inspect,
// rename and drop databases.
type candidateSession struct {
	conn     *pgx.Conn
	liveName string
}

func (e *Engine) openMaintenance(ctx context.Context) (*candidateSession, error) {
	live := databaseName(e.databaseURL)
	if live == "" {
		return nil, errors.New("veritabanı adı belirlenemedi")
	}
	dsn, err := maintenanceDSN(e.databaseURL)
	if err != nil {
		return nil, err
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("bakım bağlantısı kurulamadı: %w", err)
	}
	return &candidateSession{conn: conn, liveName: live}, nil
}

func (s *candidateSession) Close(ctx context.Context) {
	_ = s.conn.Close(context.WithoutCancel(ctx))
}

// canCreateDatabase reports whether this role may create databases at all.
// Checking up front means the answer arrives before an upload and a staging
// pass, not after.
func (s *candidateSession) canCreateDatabase(ctx context.Context) (bool, error) {
	var allowed bool
	err := s.conn.QueryRow(ctx,
		`SELECT rolsuper OR rolcreatedb FROM pg_roles WHERE rolname = current_user`).Scan(&allowed)
	if err != nil {
		return false, err
	}
	return allowed, nil
}

// create makes an empty database matching the live one's encoding and locale.
//
// template0 rather than template1: template1 can carry objects a local
// administrator added, and those would land in the candidate and then in the
// restored installation, which is not what the archive says should be there.
// Encoding and locale are copied from the live database because a mismatch
// turns into collation differences — index ordering, comparisons — that do not
// surface until much later.
func (s *candidateSession) create(ctx context.Context, name string) error {
	var encoding, collate, ctype string
	err := s.conn.QueryRow(ctx, `
		SELECT pg_encoding_to_char(encoding), datcollate, datctype
		FROM pg_database WHERE datname = $1`, s.liveName).Scan(&encoding, &collate, &ctype)
	if errors.Is(err, pgx.ErrNoRows) {
		// No live database yet (first install restoring into an empty cluster).
		encoding, collate, ctype = "UTF8", "C", "C"
	} else if err != nil {
		return fmt.Errorf("kaynak veritabanı ayarları okunamadı: %w", err)
	}
	statement := fmt.Sprintf(
		`CREATE DATABASE %s TEMPLATE template0 ENCODING %s LC_COLLATE %s LC_CTYPE %s`,
		quoteIdentifier(name), quoteLiteral(encoding), quoteLiteral(collate), quoteLiteral(ctype))
	if _, err := s.conn.Exec(ctx, statement); err != nil {
		return fmt.Errorf("aday veritabanı oluşturulamadı: %w", err)
	}
	return nil
}

func quoteLiteral(value string) string {
	return `'` + strings.ReplaceAll(value, `'`, `''`) + `'`
}

func (s *candidateSession) drop(ctx context.Context, name string) error {
	s.terminate(ctx, name)
	_, err := s.conn.Exec(ctx, fmt.Sprintf(`DROP DATABASE IF EXISTS %s`, quoteIdentifier(name)))
	return err
}

// terminate closes every other session on a database.
//
// On its own this is not enough to make a rename possible, and the reason is
// the API: a restore driven through the HTTP endpoint runs inside the process
// whose own connection pool is talking to this database. Terminating its
// connections makes the pool immediately open new ones, so between the
// terminate and the rename there is a window in which the database is busy
// again — and ALTER DATABASE ... RENAME refuses to run while any session
// remains. Callers must therefore close the database to new connections first;
// see sealDatabase.
func (s *candidateSession) terminate(ctx context.Context, name string) {
	_, _ = s.conn.Exec(ctx, `
		SELECT pg_terminate_backend(pid) FROM pg_stat_activity
		WHERE datname = $1 AND pid <> pg_backend_pid()`, name)
}

// sealDatabase stops PostgreSQL accepting new connections to a database, then
// closes the ones already open.
//
// This is what makes the rename reliable rather than merely likely. Terminating
// sessions and hoping nothing reconnects in the microseconds that follow is a
// race, and the process most likely to lose it is the one performing the
// restore. With connections disallowed, a reconnecting pool is refused instead
// of re-occupying the database.
//
// The flag travels with the database through a rename, so the retired database
// stays sealed afterwards — which is the right default for something being kept
// only as a rollback point. unsealDatabase reopens it when it is promoted back.
func (s *candidateSession) sealDatabase(ctx context.Context, name string) error {
	if _, err := s.conn.Exec(ctx, fmt.Sprintf(
		`ALTER DATABASE %s WITH ALLOW_CONNECTIONS false`, quoteIdentifier(name))); err != nil {
		return fmt.Errorf("%q veritabanı yeni bağlantılara kapatılamadı: %w", name, err)
	}
	s.terminate(ctx, name)
	return nil
}

func (s *candidateSession) unsealDatabase(ctx context.Context, name string) error {
	if _, err := s.conn.Exec(ctx, fmt.Sprintf(
		`ALTER DATABASE %s WITH ALLOW_CONNECTIONS true`, quoteIdentifier(name))); err != nil {
		return fmt.Errorf("%q veritabanı bağlantılara açılamadı: %w", name, err)
	}
	return nil
}

// swap renames the live database aside and puts the candidate in its place.
//
// The two renames are not one atomic operation, and pretending otherwise would
// be the same mistake this whole design exists to avoid. What can be said is
// narrower and more useful: each rename is atomic, the window between them is
// two statements wide, new connections are refused throughout, and the
// intermediate state — live renamed aside, candidate not yet in place — is
// recoverable, because both databases still exist under known names. The caller
// records the intent first, so a crash in between leaves a journal entry naming
// exactly these two databases.
func (s *candidateSession) swap(ctx context.Context, candidate, retired string) error {
	liveExists, err := s.exists(ctx, s.liveName)
	if err != nil {
		return err
	}
	if liveExists {
		// Close the door before emptying the room. See sealDatabase: the
		// process running an API-driven restore is itself a client of this
		// database, and terminating its pool just makes it reconnect.
		if err := s.sealDatabase(ctx, s.liveName); err != nil {
			return err
		}
	}
	s.terminate(ctx, candidate)

	if liveExists {
		if _, err := s.conn.Exec(ctx, fmt.Sprintf(`ALTER DATABASE %s RENAME TO %s`,
			quoteIdentifier(s.liveName), quoteIdentifier(retired))); err != nil {
			// Reopen: the rename failed, so this database is still the live one
			// and leaving it sealed would take the installation down.
			_ = s.unsealDatabase(ctx, s.liveName)
			return fmt.Errorf("mevcut veritabanı kenara alınamadı: %w", err)
		}
	}
	if _, err := s.conn.Exec(ctx, fmt.Sprintf(`ALTER DATABASE %s RENAME TO %s`,
		quoteIdentifier(candidate), quoteIdentifier(s.liveName))); err != nil {
		// Put the old one back. If this also fails the caller reports an
		// inconsistent system; what it must not do is leave without trying.
		if liveExists {
			if _, undo := s.conn.Exec(ctx, fmt.Sprintf(`ALTER DATABASE %s RENAME TO %s`,
				quoteIdentifier(retired), quoteIdentifier(s.liveName))); undo != nil {
				return fmt.Errorf("aday yerine konulamadı (%w) ve eski veritabanı %s adıyla kenarda kaldı: %v",
					err, retired, undo)
			}
			_ = s.unsealDatabase(ctx, s.liveName)
		}
		return fmt.Errorf("aday veritabanı yerine konulamadı: %w", err)
	}
	// The new live database must accept connections; the candidate was created
	// open, but a promoted database that had been retired was sealed.
	if err := s.unsealDatabase(ctx, s.liveName); err != nil {
		return err
	}
	return nil
}

func (s *candidateSession) exists(ctx context.Context, name string) (bool, error) {
	var present bool
	if err := s.conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)`, name).Scan(&present); err != nil {
		return false, err
	}
	return present, nil
}

// restoreInto loads the dump into an empty database.
//
// No --clean here: the target was created empty moments ago, so there is
// nothing to drop, and asking pg_restore to drop objects it is about to create
// only invents failure modes. --single-transaction keeps a partial load from
// leaving a half-populated candidate; --exit-on-error makes the first problem
// the reported one rather than the last.
func (e *Engine) restoreInto(ctx context.Context, dsn, dumpPath string) error {
	cmd := exec.CommandContext(ctx, e.pgRestore,
		"--no-owner", "--no-privileges",
		"--single-transaction", "--exit-on-error",
		"--dbname="+dsn, dumpPath)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pg_restore: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// candidateName and retiredName are derived from the live name so an operator
// looking at \l can see at a glance what each database is and when it appeared.
func candidateName(live string, at time.Time) string {
	return truncateIdentifier(live+"_cand_"+at.UTC().Format("20060102T150405Z"), 63)
}

func retiredName(live string, at time.Time) string {
	return truncateIdentifier(live+"_old_"+at.UTC().Format("20060102T150405Z"), 63)
}

// truncateIdentifier keeps a generated name inside PostgreSQL's 63-byte
// identifier limit. The server truncates silently, which would make two
// long-named databases collide; truncating the prefix here keeps the timestamp,
// which is the part that makes the name unique.
func truncateIdentifier(name string, limit int) string {
	if len(name) <= limit {
		return name
	}
	return name[len(name)-limit:]
}
