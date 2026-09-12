package opctl

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// External operations are the ones driven from outside a single Go process.
//
// The deploy script is a shell pipeline: it builds images, runs the migration
// in one throwaway container, starts the services in another and only then
// asks whether the result is healthy. No process lives for the whole of that,
// so no process can hold the lease for the whole of that either — and the
// kernel lock, which is exactly right for an in-process operation, is useless
// here. What the deploy needs is the other two halves of this package: the
// journal, so that an interrupted deploy is recognisable afterwards, and the
// startup gate, so that an installation whose migration died half-way does not
// quietly come back up and start writing.
//
// So an external operation records the same phases into the same state file,
// and claims the installation with a deadline rather than with a file lock. A
// deploy that is killed before its deadline leaves a record that says what it
// was doing when it died; after the deadline the record is written off if it
// never reached the destructive phase, and left needing a person if it did.

// DefaultExternalLifetime bounds how long an external operation may claim the
// installation without advancing. It is generous on purpose: the cost of a
// deadline that is too short is a deploy declared dead while its migration is
// still running, which is far worse than a stuck record an operator clears.
const DefaultExternalLifetime = 2 * time.Hour

// active reports whether an external record still claims the installation.
func (r *Record) active(now time.Time) bool {
	return r.External && !r.Phase.resolved() && !r.ExpiresAt.IsZero() && now.Before(r.ExpiresAt)
}

// externalPhases are the phases an external driver may write. Anything outside
// this set is a programming error in the caller, not an operator decision.
func externalPhaseAllowed(phase Phase) bool {
	switch phase {
	case PhaseSwitching, PhaseChecking, PhaseCommitted,
		PhaseFailedUnchanged, PhaseRolledBack, PhaseRecoveryRequired:
		return true
	}
	return false
}

// withLock runs fn while holding the installation lock, so that an external
// driver writing the state file cannot interleave with an in-process Acquire.
// The lock is held only for the write, never for the operation.
func (c *Controller) withLock(fn func() error) error {
	file, err := acquireLockFile(c.lockPath())
	if errors.Is(err, errLockBusy) {
		return ErrBusy
	}
	if err != nil {
		return err
	}
	defer func() { _ = releaseLockFile(file) }()
	return fn()
}

// Begin opens an external operation and returns its record.
//
// Like Acquire it refuses to start on top of an installation that still needs
// recovery, and it refuses while another operation — in-process or external —
// is live. Unlike Acquire it returns as soon as the record is written; the
// claim is kept alive by Advance and expires on its own if the caller dies.
func (c *Controller) Begin(kind Kind, actor string, lifetime time.Duration, fields map[string]any) (*Record, error) {
	if lifetime <= 0 {
		lifetime = DefaultExternalLifetime
	}
	var record Record
	err := c.withLock(func() error {
		previous, err := c.readState()
		if err != nil {
			return err
		}
		now := c.now().UTC()
		if previous != nil && !previous.Phase.resolved() {
			switch {
			case previous.Phase.destructive() || previous.Phase == PhaseRecoveryRequired:
				return fmt.Errorf("%w (%s %s, aşama %s)",
					ErrRecoveryRequired, previous.Kind, previous.ID, previous.Phase)
			case previous.active(now):
				return fmt.Errorf("%w (%s %s, aşama %s)",
					ErrBusy, previous.Kind, previous.ID, previous.Phase)
			}
			// Died or expired during preparation: nothing live was touched, so
			// write it off with the reason rather than silently overwriting it.
			previous.Phase = PhaseFailedUnchanged
			previous.UpdatedAt = now
			previous.Error = "işlem kesildi; canlı sistem değişmemişti"
			_ = c.appendEntry(previous.ID, Entry{
				At: now, OpID: previous.ID, Kind: previous.Kind,
				Phase: PhaseFailedUnchanged, Error: previous.Error,
				Fields: map[string]any{"expired": true},
			})
			if err := c.writeState(previous); err != nil {
				return err
			}
		}
		record = Record{
			ID: newID(now), Kind: kind, Phase: PhasePrepared,
			StartedAt: now, UpdatedAt: now, Token: newToken(), Actor: actor,
			External: true, ExpiresAt: now.Add(lifetime),
		}
		if err := c.appendEntry(record.ID, Entry{
			At: now, OpID: record.ID, Kind: kind, Phase: PhasePrepared,
			Intent: true, Fields: fields,
		}); err != nil {
			return err
		}
		return c.writeState(&record)
	})
	if err != nil {
		return nil, err
	}
	return &record, nil
}

// Advance moves an external operation to its next phase and renews its claim.
//
// The phase is the caller's assertion about the live installation, and the
// journal records it as an intent when it is the point of no return: the line
// between "the migration is about to run" and "the migration ran" is the line
// that decides, after a crash, whether the old images may be started again.
func (c *Controller) Advance(id string, phase Phase, lifetime time.Duration, message string, fields map[string]any) error {
	if !externalPhaseAllowed(phase) {
		return fmt.Errorf("opctl: %q dışarıdan yazılabilir bir aşama değil", phase)
	}
	if lifetime <= 0 {
		lifetime = DefaultExternalLifetime
	}
	return c.withLock(func() error {
		record, err := c.readState()
		if err != nil {
			return err
		}
		if record == nil || record.ID != id {
			return fmt.Errorf("opctl: %s işlemi kayıtlı değil", id)
		}
		if !record.External {
			return fmt.Errorf("opctl: %s dışarıdan yürütülen bir işlem değil", id)
		}
		if record.Phase.resolved() {
			return fmt.Errorf("opctl: %s işlemi %s aşamasında zaten kapandı", id, record.Phase)
		}
		now := c.now().UTC()
		record.Phase = phase
		record.UpdatedAt = now
		// The note explains the phase; it is only an *error* when the phase is
		// one. Putting "migration tamam" in the error field made a successful
		// deploy read, in `system status` and in the journal, as one that had
		// failed with that message.
		note := strings.TrimSpace(message)
		if phase == PhaseRecoveryRequired || phase == PhaseFailedUnchanged {
			record.Error = note
			note = ""
		} else {
			record.Error = ""
		}
		if phase.resolved() {
			record.ExpiresAt = time.Time{}
		} else {
			record.ExpiresAt = now.Add(lifetime)
		}
		// The journal keeps the note either way — as an error when it is one,
		// and as a plain field when it is the caller saying what it just did.
		if note != "" {
			if fields == nil {
				fields = map[string]any{}
			}
			fields["note"] = note
		}
		if err := c.appendEntry(id, Entry{
			At: now, OpID: id, Kind: record.Kind, Phase: phase,
			Intent: phase == PhaseSwitching, Error: record.Error, Fields: fields,
		}); err != nil {
			return err
		}
		return c.writeState(record)
	})
}

// Hold puts the installation out of service and keeps it there across restarts.
//
// It is the honest answer to a deploy that failed somewhere it cannot reason
// about: rather than start the previous images against a database the previous
// images may no longer understand, say so, refuse to serve, and wait for a
// person. Calling it twice is safe, and the first reason is the one kept — the
// second failure is usually a consequence of the first.
func (c *Controller) Hold(kind Kind, actor, reason string) (*Record, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, errors.New("opctl: bakım kaydı için sebep gerekiyor")
	}
	var record Record
	err := c.withLock(func() error {
		now := c.now().UTC()
		previous, err := c.readState()
		if err != nil {
			// The state file is unreadable. Replacing it with a hold is still
			// the right outcome — the installation must not serve — but the
			// unreadable content is preserved as the reason.
			previous = nil
		}
		if previous != nil && !previous.Phase.resolved() {
			record = *previous
			if record.Error == "" {
				record.Error = reason
			}
		} else {
			record = Record{
				ID: newID(now), Kind: kind, StartedAt: now,
				Token: newToken(), External: true, Error: reason,
			}
		}
		record.Phase = PhaseRecoveryRequired
		record.Actor = actor
		record.UpdatedAt = now
		record.ExpiresAt = time.Time{}
		record.External = true
		if err := c.appendEntry(record.ID, Entry{
			At: now, OpID: record.ID, Kind: record.Kind, Phase: PhaseRecoveryRequired,
			Error: reason, Fields: map[string]any{"actor": actor, "held": true},
		}); err != nil {
			return err
		}
		return c.writeState(&record)
	})
	if err != nil {
		return nil, err
	}
	return &record, nil
}

// GateStartupFor is GateStartup for a process that is part of an operation.
//
// The migration container run by a deploy is the case: it is a writer, so the
// plain gate must refuse to let it start while an operation is live — but it
// is that operation's own work. A process that can name the live operation is
// let through; every other process, including a second migration container
// started by something else, is not.
func (c *Controller) GateStartupFor(opID string) error {
	opID = strings.TrimSpace(opID)
	if opID == "" {
		return c.GateStartup()
	}
	status, err := c.Status()
	if err != nil {
		return fmt.Errorf("kontrol dizini okunamadı: %w", err)
	}
	if status.Serviceable {
		return nil
	}
	operation := status.Operation
	if operation != nil && operation.ID == opID && operation.External &&
		operation.Phase != PhaseRecoveryRequired && operation.active(c.now().UTC()) {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrRecoveryRequired, status.Reason)
}
