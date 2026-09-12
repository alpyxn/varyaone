package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// ErrArchiveExists is returned by CreateFile when the target path is already
// taken. Backups are never overwritten: the existing file may be the only copy
// of an installation, and a second run in the same second must not silently
// replace it.
var ErrArchiveExists = errors.New("bu adda bir yedek zaten var")

// PublishOptions tunes CreateFile.
type PublishOptions struct {
	// RequireComplete refuses to publish an archive that is not a full
	// recovery point. The safety backup taken before a restore and the
	// pre-deploy recovery point both set it: those exist to be restored from,
	// and an incomplete one discovers its gap at the moment it is needed.
	RequireComplete bool
}

// CreateFile writes a complete archive to path and publishes it atomically.
//
// The archive is built under a temporary name in the SAME directory, flushed to
// stable storage, re-read end to end through Verify, and only then renamed onto
// its final name. Consequences of that order:
//
//   - A file bearing the final name has been fully written and fully verified.
//     A crash, a full disk or a failed fsync leaves the temporary file behind
//     and no archive at the final path, rather than a plausible-looking
//     truncated one.
//   - Verify reads what actually landed on disk, not what was intended, so a
//     write that was silently short or corrupted is caught before the file is
//     presented to anyone as a backup.
func (e *Engine) CreateFile(ctx context.Context, path string, opts PublishOptions) (Manifest, error) {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return Manifest{}, err
	}
	if _, err := os.Lstat(path); err == nil {
		return Manifest{}, fmt.Errorf("%w: %s", ErrArchiveExists, path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Manifest{}, err
	}

	temporary, err := os.CreateTemp(directory, filepath.Base(path)+".partial-*")
	if err != nil {
		return Manifest{}, err
	}
	temporaryPath := temporary.Name()
	published := false
	defer func() {
		_ = temporary.Close()
		if !published {
			_ = os.Remove(temporaryPath)
		}
	}()

	manifest, err := e.Create(ctx, temporary)
	if err != nil {
		return Manifest{}, err
	}
	// Sync before Close: a Close error on a buffered filesystem can be the first
	// and only report of a write that never reached the disk.
	if err = temporary.Sync(); err != nil {
		return Manifest{}, fmt.Errorf("yedek diske yazılamadı: %w", err)
	}
	if err = temporary.Close(); err != nil {
		return Manifest{}, fmt.Errorf("yedek kapatılamadı: %w", err)
	}

	written, err := os.Open(temporaryPath)
	if err != nil {
		return Manifest{}, err
	}
	verified, err := e.Verify(ctx, written)
	_ = written.Close()
	if err != nil {
		return Manifest{}, fmt.Errorf("yazılan yedek doğrulanamadı: %w", err)
	}
	if opts.RequireComplete {
		if err = verified.RequireComplete(); err != nil {
			return Manifest{}, err
		}
	}

	if err = os.Rename(temporaryPath, path); err != nil {
		return Manifest{}, err
	}
	published = true
	// Without this the rename itself can be lost in a power failure, leaving
	// neither name on disk.
	if err = syncDir(directory); err != nil {
		return Manifest{}, fmt.Errorf("yedek dizini diske yazılamadı: %w", err)
	}
	return manifest, nil
}

// VerifyFile checks an archive on disk without touching the database or the
// storage tree.
func (e *Engine) VerifyFile(ctx context.Context, path string) (Manifest, error) {
	file, err := os.Open(path)
	if err != nil {
		return Manifest{}, err
	}
	defer func() { _ = file.Close() }()
	return e.Verify(ctx, file)
}

// WriteArchive streams an archive to w. It exists so callers that must produce
// the archive on a pipe (the deploy script reads it from the container's
// stdout) still go through one place.
func (e *Engine) WriteArchive(ctx context.Context, w io.Writer) (Manifest, error) {
	return e.Create(ctx, w)
}
