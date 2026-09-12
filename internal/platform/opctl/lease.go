package opctl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

// Lease is an exclusive hold on the installation for the duration of one
// operation. It is obtained from Acquire and must be released; the kernel
// releases it anyway if the process dies, which is the point.
type Lease struct {
	controller *Controller
	file       *os.File
	record     Record
	released   bool
}

// AcquireOptions tunes how long Acquire waits for a busy installation.
type AcquireOptions struct {
	// Wait is how long to keep trying. Zero fails immediately when busy.
	Wait time.Duration
	// Actor names the entry point ("api", "cli", "deploy") for the journal.
	Actor string
}

// Acquire takes the installation's exclusive lease.
//
// It refuses to start a new operation on an installation whose previous
// operation needs recovery: starting a restore on top of a half-finished
// restore is how one recoverable problem becomes two unrecoverable ones. The
// operator must resolve the old operation first.
func (c *Controller) Acquire(ctx context.Context, kind Kind, opts AcquireOptions) (*Lease, error) {
	deadline := c.now().Add(opts.Wait)
	var file *os.File
	for {
		var err error
		file, err = acquireLockFile(c.lockPath())
		if err == nil {
			break
		}
		if !errors.Is(err, errLockBusy) {
			return nil, err
		}
		if !c.now().Before(deadline) {
			return nil, ErrBusy
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	ok := false
	defer func() {
		if !ok {
			_ = releaseLockFile(file)
		}
	}()

	// Holding the lock, settle what the previous operation left behind before
	// declaring a new one.
	previous, err := c.readState()
	if err != nil {
		return nil, err
	}
	if previous != nil && !previous.Phase.resolved() {
		if previous.Phase.destructive() || previous.Phase == PhaseRecoveryRequired {
			return nil, fmt.Errorf("%w (%s %s, aşama %s)",
				ErrRecoveryRequired, previous.Kind, previous.ID, previous.Phase)
		}
		// An external operation still inside its claim is live work, even
		// though this process now holds the lock: the deploy script holds the
		// installation with a deadline, not with the lock file.
		if previous.active(c.now().UTC()) {
			return nil, fmt.Errorf("%w (%s %s, aşama %s)",
				ErrBusy, previous.Kind, previous.ID, previous.Phase)
		}
		// Died during preparation: write it off, with the reason, so the
		// journal records that this was an automatic decision and not a
		// successful run.
		previous.Phase = PhaseFailedUnchanged
		previous.UpdatedAt = c.now().UTC()
		previous.Error = "işlem kesildi; canlı sistem değişmemişti"
		_ = c.appendEntry(previous.ID, Entry{
			At: previous.UpdatedAt, OpID: previous.ID, Kind: previous.Kind,
			Phase: PhaseFailedUnchanged, Error: previous.Error,
			Fields: map[string]any{"recovered_at_startup": true},
		})
		if err := c.writeState(previous); err != nil {
			return nil, err
		}
	}

	now := c.now().UTC()
	record := Record{
		ID: newID(now), Kind: kind, Phase: PhaseValidating,
		StartedAt: now, UpdatedAt: now, Token: newToken(), Actor: opts.Actor,
	}
	if err := c.appendEntry(record.ID, Entry{
		At: now, OpID: record.ID, Kind: kind, Phase: PhaseValidating,
		Fields: map[string]any{"actor": opts.Actor, "pid": os.Getpid()},
	}); err != nil {
		return nil, err
	}
	if err := c.writeState(&record); err != nil {
		return nil, err
	}
	ok = true
	return &Lease{controller: c, file: file, record: record}, nil
}

// ID is the operation identifier. It appears in the journal, in API responses
// and in operator messages, so a user who lost the connection mid-restore can
// ask what happened to a specific operation rather than to "the restore".
func (l *Lease) ID() string { return l.record.ID }

// Phase is the last recorded phase.
func (l *Lease) Phase() Phase { return l.record.Phase }

// check verifies this lease still owns the installation. A lease is fenced when
// state.json carries a different token, which happens if the lock was forcibly
// taken over. Every write goes through here so a fenced process stops acting
// rather than racing the new owner.
func (l *Lease) check() error {
	if l.released {
		return ErrFenced
	}
	current, err := l.controller.readState()
	if err != nil {
		return err
	}
	if current == nil || current.ID != l.record.ID || current.Token != l.record.Token {
		return ErrFenced
	}
	return nil
}

// Intent records what is about to happen, before it happens, and flushes it.
// Every irreversible step must be preceded by one: after a crash the difference
// between "intent written, no result" and "no intent" is the difference between
// knowing a step may be half-done and having to guess.
func (l *Lease) Intent(phase Phase, fields map[string]any) error {
	if err := l.check(); err != nil {
		return err
	}
	now := l.controller.now().UTC()
	if err := l.controller.appendEntry(l.record.ID, Entry{
		At: now, OpID: l.record.ID, Kind: l.record.Kind, Phase: phase,
		Intent: true, Fields: fields,
	}); err != nil {
		return err
	}
	l.record.Phase = phase
	l.record.UpdatedAt = now
	return l.controller.writeState(&l.record)
}

// Result records that a phase completed.
func (l *Lease) Result(phase Phase, fields map[string]any) error {
	if err := l.check(); err != nil {
		return err
	}
	now := l.controller.now().UTC()
	if err := l.controller.appendEntry(l.record.ID, Entry{
		At: now, OpID: l.record.ID, Kind: l.record.Kind, Phase: phase, Fields: fields,
	}); err != nil {
		return err
	}
	l.record.Phase = phase
	l.record.UpdatedAt = now
	return l.controller.writeState(&l.record)
}

// Fail closes the operation with a failure phase. Callers choose the phase
// deliberately: FAILED_UNCHANGED asserts the live installation was never
// touched, RECOVERY_REQUIRED asserts it was and needs a person. Guessing
// between them is exactly the mistake this package exists to prevent.
func (l *Lease) Fail(phase Phase, cause error) error {
	if err := l.check(); err != nil {
		return err
	}
	now := l.controller.now().UTC()
	message := ""
	if cause != nil {
		message = cause.Error()
	}
	if err := l.controller.appendEntry(l.record.ID, Entry{
		At: now, OpID: l.record.ID, Kind: l.record.Kind, Phase: phase, Error: message,
	}); err != nil {
		return err
	}
	l.record.Phase = phase
	l.record.UpdatedAt = now
	l.record.Error = message
	return l.controller.writeState(&l.record)
}

// Release drops the lease. An operation that has not reached a terminal phase
// by the time it releases is recorded as needing recovery if it had begun
// changing the installation, and written off if it had not — a caller that
// returns early through an unexpected path still leaves an honest record.
func (l *Lease) Release() error {
	if l.released {
		return nil
	}
	var err error
	if !l.record.Phase.resolved() {
		phase := PhaseFailedUnchanged
		if l.record.Phase.destructive() {
			phase = PhaseRecoveryRequired
		}
		err = l.Fail(phase, errors.New("işlem sonuç yazmadan sona erdi"))
	}
	l.released = true
	if closeErr := releaseLockFile(l.file); err == nil {
		err = closeErr
	}
	return err
}

// leaseHeld reports whether any process currently holds the lease.
func (c *Controller) leaseHeld() (bool, error) {
	file, err := acquireLockFile(c.lockPath())
	if errors.Is(err, errLockBusy) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return false, releaseLockFile(file)
}

func (c *Controller) readState() (*Record, error) {
	body, err := os.ReadFile(c.statePath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var record Record
	if err := json.Unmarshal(body, &record); err != nil {
		// An unreadable state file is not "no operation". Report it; the
		// startup gate turns this into a refusal to serve.
		return nil, fmt.Errorf("kontrol durumu okunamadı: %w", err)
	}
	return &record, nil
}

// writeState replaces state.json atomically and flushes both the file and its
// directory. Without the directory flush a power loss can lose the rename and
// leave the previous state — which would report an interrupted restore as a
// completed one.
func (c *Controller) writeState(record *Record) error {
	body, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(c.dir, ".state-*.json")
	if err != nil {
		return err
	}
	path := temporary.Name()
	defer func() { _ = os.Remove(path) }()
	if _, err = temporary.Write(body); err != nil {
		_ = temporary.Close()
		return err
	}
	if err = temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err = temporary.Close(); err != nil {
		return err
	}
	if err = os.Rename(path, c.statePath()); err != nil {
		return err
	}
	return syncDir(c.dir)
}

// appendEntry appends one journal line and flushes it. O_APPEND plus a single
// write keeps concurrent appends from interleaving mid-line.
func (c *Controller) appendEntry(id string, entry Entry) error {
	body, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(c.journalPath(id), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err = file.Write(append(body, '\n')); err != nil {
		_ = file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

// The four methods below let a Lease act as a backup.RestoreJournal directly.
// They are deliberately named for what the engine is doing rather than for the
// phase constants, so a reader of the restore path sees the point of no return
// where it actually is.

// Prepared records that staging finished with the live installation untouched.
func (l *Lease) Prepared(fields map[string]any) error {
	return l.Result(PhaseCandidateReady, fields)
}

// Switching records the intent to commit, before the commit.
func (l *Lease) Switching(fields map[string]any) error {
	return l.Intent(PhaseSwitching, fields)
}

// Switched records that the database transaction committed.
func (l *Lease) Switched(fields map[string]any) error {
	return l.Result(PhaseChecking, fields)
}

// Committed records that the whole switch finished.
func (l *Lease) Committed(fields map[string]any) error {
	return l.Result(PhaseCommitted, fields)
}
