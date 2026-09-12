package opctl

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Idempotency maps a caller-supplied key to the operation it started.
//
// It exists because of what a lost HTTP response means for a destructive
// operation. The client cannot tell "the restore never started" from "the
// restore finished and the reply was lost", and a user looking at a spinner
// that never resolves will click again. Without a key, that second click is a
// second restore — on top of the first one's result.
//
// The mapping is recorded BEFORE the operation runs, so a crash between
// recording and running leaves a key pointing at an operation whose journal
// says how far it got. That is the answer the second click needs.

// RememberKey associates key with an operation id. It refuses to overwrite an
// existing association: a key that already names an operation is the whole
// point.
func (c *Controller) RememberKey(key, operationID string) error {
	path, err := c.keyPath(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err = file.WriteString(operationID); err != nil {
		_ = file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}

// LookupKey returns the operation id a key already names, if any.
func (c *Controller) LookupKey(key string) (string, bool, error) {
	path, err := c.keyPath(key)
	if err != nil {
		return "", false, err
	}
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return strings.TrimSpace(string(body)), true, nil
}

// FindRecord returns the recorded state of one operation. Only the most recent
// operation's full record is kept in state.json, so for older ones the answer
// is reconstructed from the journal — which is authoritative anyway.
func (c *Controller) FindRecord(id string) (*Record, error) {
	current, err := c.readState()
	if err != nil {
		return nil, err
	}
	if current != nil && current.ID == id {
		return current, nil
	}
	entries, err := c.History(id)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, os.ErrNotExist
	}
	first, last := entries[0], entries[len(entries)-1]
	return &Record{
		ID: id, Kind: first.Kind, Phase: last.Phase,
		StartedAt: first.At, UpdatedAt: last.At, Error: last.Error,
	}, nil
}

// keyPath hashes the key rather than using it as a filename. Keys come from
// clients: they can contain separators, be absurdly long, or differ only in
// case on a case-insensitive filesystem.
func (c *Controller) keyPath(key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", errors.New("opctl: idempotency key is required")
	}
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(c.dir, "idempotency", hex.EncodeToString(sum[:])), nil
}
