// Package opctl coordinates the destructive whole-system operations — backup,
// restore, deploy, migration — across every entry point that can start one:
// the HTTP API, the CLI, the deploy script and the desktop supervisor.
//
// Three things live here, and they are inseparable:
//
//   - An exclusive lease. One operation at a time, across processes, held by a
//     kernel file lock rather than a PID file. A PID file cannot tell a dead
//     owner from one whose process table entry you may not read, and a reused
//     PID looks exactly like a live owner; a file lock is released by the
//     kernel when the holder dies, whatever killed it.
//
//   - A persistent journal. Every irreversible step writes its INTENT before it
//     acts and its RESULT after, each flushed to stable storage. After a crash
//     the journal is the only evidence of how far an operation got.
//
//   - A startup gate. A process that serves traffic or runs background work
//     asks this package whether the installation is in a known-good state, and
//     refuses to start if it is not. An interrupted restore must not be
//     followed by an API that happily writes into a half-restored database.
//
// The control directory holding all three lives OUTSIDE both the database being
// restored and the storage tree being swapped. Keeping the record of an
// operation inside the thing the operation replaces means losing the record
// exactly when it matters.
package opctl

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Kind identifies what an operation is doing. It is recorded in the journal and
// reported to operators; it does not change the locking rules, which are the
// same for every kind: one at a time.
type Kind string

const (
	KindBackup  Kind = "backup"
	KindRestore Kind = "restore"
	KindDeploy  Kind = "deploy"
	KindMigrate Kind = "migrate"
	KindRepair  Kind = "repair"
)

// Phase is the operation lifecycle. The ordering matters: everything up to and
// including SAFETY_VERIFIED and CANDIDATE_READY is preparation that leaves the
// live installation untouched, and SWITCHING is the point of no return.
type Phase string

const (
	PhaseValidating     Phase = "VALIDATING"
	PhasePrepared       Phase = "PREPARED"
	PhaseQuiescing      Phase = "QUIESCING"
	PhaseSafetyVerified Phase = "SAFETY_VERIFIED"
	PhaseCandidateReady Phase = "CANDIDATE_READY"
	PhaseSwitching      Phase = "SWITCHING"
	PhaseChecking       Phase = "CHECKING"
	PhaseCommitted      Phase = "COMMITTED"

	PhaseFailedUnchanged  Phase = "FAILED_UNCHANGED"
	PhaseRollingBack      Phase = "ROLLING_BACK"
	PhaseRolledBack       Phase = "ROLLED_BACK"
	PhaseRecoveryRequired Phase = "RECOVERY_REQUIRED"
)

// destructiveFrom is the first phase at which the live installation may already
// have been modified. A crash before it can be resolved automatically; a crash
// at or after it cannot.
func (p Phase) destructive() bool {
	switch p {
	case PhaseSwitching, PhaseChecking, PhaseRollingBack, PhaseRecoveryRequired:
		return true
	}
	return false
}

// terminal reports whether a phase ends an operation.
func (p Phase) terminal() bool {
	switch p {
	case PhaseCommitted, PhaseFailedUnchanged, PhaseRolledBack:
		return true
	}
	return false
}

// resolved reports whether an installation carrying this phase may serve
// traffic. RECOVERY_REQUIRED deliberately is not resolved: it needs a person.
func (p Phase) resolved() bool { return p.terminal() }

// Record is one operation's current state, as written to state.json.
type Record struct {
	ID        string    `json:"id"`
	Kind      Kind      `json:"kind"`
	Phase     Phase     `json:"phase"`
	StartedAt time.Time `json:"started_at"`
	UpdatedAt time.Time `json:"updated_at"`
	// Token fences a lease: a lease whose token no longer matches the record
	// has been superseded and may not write anything further.
	Token string `json:"token"`
	// Actor is who started it ("api", "cli", "deploy"), for diagnostics only.
	Actor string `json:"actor,omitempty"`
	// Error is the last failure message. It is operator-facing text, never a
	// secret: callers must not put connection strings or keys in it.
	Error string `json:"error,omitempty"`
	// ResolvedBy records the operator note that cleared a RECOVERY_REQUIRED.
	ResolvedBy string `json:"resolved_by,omitempty"`
	// External marks an operation driven from outside a single process — the
	// deploy script. It is claimed by a deadline instead of by the lease; see
	// external.go.
	External bool `json:"external,omitempty"`
	// ExpiresAt is when an external claim lapses. Zero for in-process leases,
	// whose liveness the kernel reports exactly.
	ExpiresAt time.Time `json:"expires_at,omitempty"`
}

// Entry is one appended journal line.
type Entry struct {
	At     time.Time      `json:"at"`
	OpID   string         `json:"op_id"`
	Kind   Kind           `json:"kind"`
	Phase  Phase          `json:"phase"`
	Intent bool           `json:"intent,omitempty"`
	Error  string         `json:"error,omitempty"`
	Fields map[string]any `json:"fields,omitempty"`
}

// ErrBusy is returned when another process holds the lease.
var ErrBusy = errors.New("başka bir sistem işlemi sürüyor")

// ErrFenced is returned when a lease tries to write after being superseded. It
// means this process lost ownership — it must stop immediately rather than
// carry on operating on an installation someone else now owns.
var ErrFenced = errors.New("bu işlemin sahipliği düştü")

// ErrRecoveryRequired is returned by the startup gate when a previous operation
// stopped after it had begun changing the installation.
var ErrRecoveryRequired = errors.New("önceki sistem işlemi yarıda kaldı; kurtarma gerekiyor")

// Controller is the coordination point for one installation.
type Controller struct {
	dir string
	now func() time.Time
}

// New opens (creating if needed) the control directory. The directory is 0700:
// it records what an installation is doing and when, and on a shared host that
// is nobody else's business.
func New(dir string) (*Controller, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, errors.New("opctl: control directory is required")
	}
	if err := os.MkdirAll(filepath.Join(dir, "journal"), 0o700); err != nil {
		return nil, fmt.Errorf("opctl: create control directory: %w", err)
	}
	_ = os.Chmod(dir, 0o700)
	return &Controller{dir: dir, now: time.Now}, nil
}

// Dir returns the control directory.
func (c *Controller) Dir() string { return c.dir }

func (c *Controller) statePath() string { return filepath.Join(c.dir, "state.json") }
func (c *Controller) lockPath() string  { return filepath.Join(c.dir, "operation.lock") }
func (c *Controller) journalPath(id string) string {
	return filepath.Join(c.dir, "journal", id+".jsonl")
}

// Status describes the installation's operational state. It is readable without
// holding the lease, because the processes that need it most — a starting API,
// a starting worker — must not block behind a running operation to learn that
// they should not start.
type Status struct {
	// Operation is the most recent operation, or nil if there has never been one.
	Operation *Record
	// Running is true when a process currently holds the lease.
	Running bool
	// Serviceable is true when normal traffic and background work may run.
	Serviceable bool
	// Reason explains a false Serviceable in operator-facing Turkish.
	Reason string
}

// Status reports whether the installation may serve traffic.
//
// The interesting case is a record that is neither terminal nor held by a live
// process: the operation died. Where it died decides what happens next, and the
// journal is what says where. Before the switch, nothing live was touched and
// the operation can be written off automatically. At or after the switch, the
// installation may hold a mixture of old and new state that only a person can
// untangle, so it stays out of service.
func (c *Controller) Status() (Status, error) {
	record, err := c.readState()
	if err != nil {
		return Status{}, err
	}
	if record == nil {
		return Status{Serviceable: true}, nil
	}
	running, err := c.leaseHeld()
	if err != nil {
		return Status{}, err
	}
	// An external operation holds no lock, so its claim is read from the
	// record's own deadline. Without this a running deploy looks exactly like
	// a deploy that died mid-migration.
	if !running {
		running = record.active(c.now().UTC())
	}
	status := Status{Operation: record, Running: running}
	switch {
	case record.Phase.resolved():
		status.Serviceable = true
	case record.Phase == PhaseRecoveryRequired:
		status.Reason = fmt.Sprintf("%s işlemi (%s) yarıda kaldı ve kurtarma bekliyor: %s",
			record.Kind, record.ID, record.Error)
	case running:
		status.Reason = fmt.Sprintf("%s işlemi (%s) sürüyor, aşama %s", record.Kind, record.ID, record.Phase)
	case record.Phase.destructive():
		status.Reason = fmt.Sprintf("%s işlemi (%s) %s aşamasında kesildi; sistem bilinen bir durumda değil",
			record.Kind, record.ID, record.Phase)
	default:
		// Died during preparation. Nothing live was touched.
		status.Serviceable = true
		status.Reason = fmt.Sprintf("%s işlemi (%s) %s aşamasında kesildi; canlı sistem değişmedi",
			record.Kind, record.ID, record.Phase)
	}
	return status, nil
}

// GateStartup is what a server or worker calls before it does anything else. It
// returns ErrRecoveryRequired when the installation must not be put back into
// service.
func (c *Controller) GateStartup() error {
	status, err := c.Status()
	if err != nil {
		// A control directory that cannot be read is itself a reason not to
		// start: the alternative is serving traffic while blind to whether a
		// restore was interrupted.
		return fmt.Errorf("kontrol dizini okunamadı: %w", err)
	}
	if !status.Serviceable {
		return fmt.Errorf("%w: %s", ErrRecoveryRequired, status.Reason)
	}
	return nil
}

// Resolve clears a stuck operation after an operator has dealt with it. The
// note is recorded: an installation that was manually recovered should say so
// for as long as its journal survives.
func (c *Controller) Resolve(note string) error {
	record, err := c.readState()
	if err != nil {
		return err
	}
	if record == nil {
		return errors.New("kayıtlı bir sistem işlemi yok")
	}
	if record.Phase.resolved() {
		return nil
	}
	held, err := c.leaseHeld()
	if err != nil {
		return err
	}
	if held {
		return fmt.Errorf("%w: süren bir işlem elle çözülemez", ErrBusy)
	}
	record.Phase = PhaseRolledBack
	record.ResolvedBy = strings.TrimSpace(note)
	record.UpdatedAt = c.now().UTC()
	if err := c.appendEntry(record.ID, Entry{
		At: record.UpdatedAt, OpID: record.ID, Kind: record.Kind, Phase: PhaseRolledBack,
		Fields: map[string]any{"resolved_by": record.ResolvedBy, "manual": true},
	}); err != nil {
		return err
	}
	return c.writeState(record)
}

// History returns the journal entries of one operation, oldest first.
func (c *Controller) History(id string) ([]Entry, error) {
	body, err := os.ReadFile(c.journalPath(id))
	if err != nil {
		return nil, err
	}
	var entries []Entry
	for _, line := range strings.Split(string(body), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var entry Entry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			// A torn final line is expected after a crash mid-append; earlier
			// lines are still evidence and must not be discarded with it.
			continue
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// Operations lists known operation ids, newest first.
func (c *Controller) Operations() ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(c.dir, "journal"))
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, entry := range entries {
		if name := entry.Name(); strings.HasSuffix(name, ".jsonl") {
			ids = append(ids, strings.TrimSuffix(name, ".jsonl"))
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(ids)))
	return ids, nil
}

func newID(now time.Time) string {
	var suffix [4]byte
	_, _ = rand.Read(suffix[:])
	return now.UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(suffix[:])
}

func newToken() string {
	var token [16]byte
	_, _ = rand.Read(token[:])
	return hex.EncodeToString(token[:])
}
