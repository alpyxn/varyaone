package desktop

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

// stackLogMaxBytes caps stack.log; when it grows past this it is rotated to
// stack.log.1 (single generation). Keeps a runaway log from filling the disk
// without pulling in a rotation dependency.
const stackLogMaxBytes = 10 << 20

// logWriter appends to <Home>/logs/<name>, tee'd to stderr, rotating a single
// generation past stackLogMaxBytes. A Windows service (and a scheduled update
// task) has no console, so without this file every message — initdb failures,
// migration errors, update rollbacks — would be lost.
func logWriter(l Layout, name string) io.Writer {
	dir := l.Logs()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return os.Stderr
	}
	path := filepath.Join(dir, name)
	if fi, err := os.Stat(path); err == nil && fi.Size() > stackLogMaxBytes {
		_ = os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return os.Stderr
	}
	// A detached process (the spawned updater) has no valid stderr; a failed
	// write there must not stop the log line reaching the file.
	return teeWriter{primary: f, mirror: os.Stderr}
}

// teeWriter writes to primary and best-effort mirrors to mirror; only a primary
// write error is reported.
type teeWriter struct{ primary, mirror io.Writer }

func (t teeWriter) Write(p []byte) (int, error) {
	n, err := t.primary.Write(p)
	if t.mirror != nil {
		_, _ = t.mirror.Write(p)
	}
	return n, err
}

// NewStackLogger builds the JSON logger the `varyaone stack` service runs with.
func NewStackLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(logWriter(DiscoverLayout(), "stack.log"), nil))
}
