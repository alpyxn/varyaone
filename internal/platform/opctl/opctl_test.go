package opctl

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func newController(t *testing.T) *Controller {
	t.Helper()
	controller, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return controller
}

func TestFreshInstallationIsServiceable(t *testing.T) {
	controller := newController(t)
	status, err := controller.Status()
	if err != nil {
		t.Fatal(err)
	}
	if !status.Serviceable || status.Operation != nil {
		t.Fatalf("fresh installation not serviceable: %+v", status)
	}
	if err := controller.GateStartup(); err != nil {
		t.Fatalf("GateStartup = %v, want nil", err)
	}
}

func TestLeaseIsExclusive(t *testing.T) {
	controller := newController(t)
	first, err := controller.Acquire(context.Background(), KindBackup, AcquireOptions{Actor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Acquire(context.Background(), KindRestore, AcquireOptions{Actor: "test"}); !errors.Is(err, ErrBusy) {
		t.Fatalf("second Acquire = %v, want ErrBusy", err)
	}
	if err := first.Result(PhaseCommitted, nil); err != nil {
		t.Fatal(err)
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	second, err := controller.Acquire(context.Background(), KindRestore, AcquireOptions{Actor: "test"})
	if err != nil {
		t.Fatalf("Acquire after release = %v", err)
	}
	_ = second.Result(PhaseCommitted, nil)
	_ = second.Release()
}

// TestLeaseIsExclusiveAcrossProcesses is the one that matters: the mutex this
// replaced protected a single API process and nothing else, so a CLI restore
// could run straight through an API backup. The lock has to be held by the
// kernel for that to be true.
func TestLeaseIsExclusiveAcrossProcesses(t *testing.T) {
	if _, err := exec.LookPath("flock"); err != nil {
		t.Skip("flock(1) not available")
	}
	controller := newController(t)
	lease, err := controller.Acquire(context.Background(), KindRestore, AcquireOptions{Actor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Result(PhaseCommitted, nil); _ = lease.Release() })

	// A separate process must not be able to take the same lock.
	cmd := exec.Command("flock", "--nonblock", controller.lockPath(), "true")
	if err := cmd.Run(); err == nil {
		t.Fatal("another process acquired the lease while it was held")
	}
}

// TestCrashDuringPreparationIsWrittenOff: the process died before anything live
// was touched, so the installation is fine and the next operation may proceed —
// but the journal must say the previous one was interrupted, not that it
// succeeded.
func TestCrashDuringPreparationIsWrittenOff(t *testing.T) {
	controller := newController(t)
	lease, err := controller.Acquire(context.Background(), KindRestore, AcquireOptions{Actor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Result(PhaseCandidateReady, nil); err != nil {
		t.Fatal(err)
	}
	crash(t, lease)

	status, err := controller.Status()
	if err != nil {
		t.Fatal(err)
	}
	if !status.Serviceable {
		t.Fatalf("preparation crash blocked startup: %s", status.Reason)
	}
	if err := controller.GateStartup(); err != nil {
		t.Fatalf("GateStartup = %v, want nil", err)
	}
	next, err := controller.Acquire(context.Background(), KindBackup, AcquireOptions{Actor: "test"})
	if err != nil {
		t.Fatalf("Acquire after a preparation crash = %v", err)
	}
	_ = next.Result(PhaseCommitted, nil)
	_ = next.Release()

	entries, err := controller.History(lease.ID())
	if err != nil {
		t.Fatal(err)
	}
	last := entries[len(entries)-1]
	if last.Phase != PhaseFailedUnchanged {
		t.Fatalf("interrupted operation recorded as %s, want FAILED_UNCHANGED", last.Phase)
	}
}

// TestCrashAfterSwitchingBlocksEverything is the core guarantee. The intent to
// switch was written, the process died, and nothing can prove whether the
// database committed. The installation must not serve traffic and must not
// accept a second operation stacked on top.
func TestCrashAfterSwitchingBlocksEverything(t *testing.T) {
	controller := newController(t)
	lease, err := controller.Acquire(context.Background(), KindRestore, AcquireOptions{Actor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Switching(map[string]any{"database": "varyaone"}); err != nil {
		t.Fatal(err)
	}
	crash(t, lease)

	status, err := controller.Status()
	if err != nil {
		t.Fatal(err)
	}
	if status.Serviceable {
		t.Fatal("an installation interrupted mid-switch reported itself serviceable")
	}
	if err := controller.GateStartup(); !errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("GateStartup = %v, want ErrRecoveryRequired", err)
	}
	if _, err := controller.Acquire(context.Background(), KindRestore, AcquireOptions{Actor: "test"}); !errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("Acquire = %v, want ErrRecoveryRequired", err)
	}

	if err := controller.Resolve("veritabanı elle doğrulandı"); err != nil {
		t.Fatal(err)
	}
	if err := controller.GateStartup(); err != nil {
		t.Fatalf("GateStartup after resolve = %v", err)
	}
	record, err := controller.FindRecord(lease.ID())
	if err != nil {
		t.Fatal(err)
	}
	if record.ResolvedBy != "veritabanı elle doğrulandı" {
		t.Fatalf("resolution note not recorded: %+v", record)
	}
}

// TestReleaseWithoutResultIsHonest: a caller that returns through an
// unexpected path must not leave a record implying success.
func TestReleaseWithoutResultIsHonest(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reach func(*Lease) error
		want  Phase
	}{
		{"before switching", func(l *Lease) error { return l.Result(PhaseCandidateReady, nil) }, PhaseFailedUnchanged},
		{"after switching", func(l *Lease) error { return l.Switching(nil) }, PhaseRecoveryRequired},
	} {
		t.Run(tc.name, func(t *testing.T) {
			controller := newController(t)
			lease, err := controller.Acquire(context.Background(), KindRestore, AcquireOptions{Actor: "test"})
			if err != nil {
				t.Fatal(err)
			}
			if err := tc.reach(lease); err != nil {
				t.Fatal(err)
			}
			if err := lease.Release(); err != nil {
				t.Fatal(err)
			}
			record, err := controller.FindRecord(lease.ID())
			if err != nil {
				t.Fatal(err)
			}
			if record.Phase != tc.want {
				t.Fatalf("phase = %s, want %s", record.Phase, tc.want)
			}
		})
	}
}

// TestFencedLeaseCannotWrite: if the record no longer names this lease, the
// process has lost ownership and must stop rather than race the new owner.
func TestFencedLeaseCannotWrite(t *testing.T) {
	controller := newController(t)
	lease, err := controller.Acquire(context.Background(), KindRestore, AcquireOptions{Actor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a takeover: someone else's record is now current.
	other := Record{ID: "someone-else", Kind: KindDeploy, Phase: PhaseSwitching, Token: "other-token"}
	if err := controller.writeState(&other); err != nil {
		t.Fatal(err)
	}
	if err := lease.Result(PhaseCommitted, nil); !errors.Is(err, ErrFenced) {
		t.Fatalf("fenced write = %v, want ErrFenced", err)
	}
	if err := lease.Switching(nil); !errors.Is(err, ErrFenced) {
		t.Fatalf("fenced intent = %v, want ErrFenced", err)
	}
}

// TestUnreadableStateRefusesStartup: a control directory that cannot be read is
// not the same as one with nothing in it. Treating the two alike would serve
// traffic while blind to an interrupted restore.
func TestUnreadableStateRefusesStartup(t *testing.T) {
	controller := newController(t)
	if err := os.WriteFile(controller.statePath(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := controller.GateStartup(); err == nil {
		t.Fatal("GateStartup accepted an unreadable control state")
	}
}

// TestIdempotencyKeyIsRecordedOnce guards the double-click case.
func TestIdempotencyKeyIsRecordedOnce(t *testing.T) {
	controller := newController(t)
	if err := controller.RememberKey("click-1", "op-1"); err != nil {
		t.Fatal(err)
	}
	if err := controller.RememberKey("click-1", "op-2"); err == nil {
		t.Fatal("an idempotency key was reassigned to a second operation")
	}
	id, found, err := controller.LookupKey("click-1")
	if err != nil || !found || id != "op-1" {
		t.Fatalf("LookupKey = %q, %v, %v", id, found, err)
	}
	if _, found, err = controller.LookupKey("click-2"); err != nil || found {
		t.Fatalf("unknown key reported as found: %v %v", found, err)
	}
}

// TestJournalSurvivesATornFinalLine: a crash mid-append leaves a partial line.
// The earlier lines are the evidence and must still be readable.
func TestJournalSurvivesATornFinalLine(t *testing.T) {
	controller := newController(t)
	lease, err := controller.Acquire(context.Background(), KindRestore, AcquireOptions{Actor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Switching(nil); err != nil {
		t.Fatal(err)
	}
	path := controller.journalPath(lease.ID())
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(body, []byte(`{"at":"2026-`)...), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, err := controller.History(lease.ID())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("history = %d entries, want the 2 complete ones", len(entries))
	}
	crash(t, lease)
}

// TestControlDirectoryIsPrivate: the journal records when an installation was
// restored and from what. On a shared host that is nobody else's business.
func TestControlDirectoryIsPrivate(t *testing.T) {
	if strings.HasPrefix(os.Getenv("GOOS"), "windows") {
		t.Skip("POSIX permissions")
	}
	controller := newController(t)
	lease, err := controller.Acquire(context.Background(), KindBackup, AcquireOptions{Actor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	_ = lease.Result(PhaseCommitted, nil)
	_ = lease.Release()
	for _, path := range []string{controller.dir, filepath.Join(controller.dir, "journal")} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o700 {
			t.Errorf("%s mode = %o, want 700", path, info.Mode().Perm())
		}
	}
	info, err := os.Stat(controller.statePath())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Errorf("state.json mode = %o, want no group/other access", info.Mode().Perm())
	}
}

// crash drops the lock the way a killed process would: the file descriptor
// goes away without any terminal phase being recorded.
func crash(t *testing.T, lease *Lease) {
	t.Helper()
	if err := releaseLockFile(lease.file); err != nil {
		t.Fatal(err)
	}
	lease.released = true
}
