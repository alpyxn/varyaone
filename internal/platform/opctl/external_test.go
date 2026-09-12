package opctl

import (
	"context"
	"errors"
	"testing"
	"time"
)

// The deploy script is the caller these tests stand in for: it opens an
// operation, records the intent to migrate, and then either commits or dies.
// What matters afterwards is whether the installation is allowed back into
// service, so every test here ends by asking exactly that.

func TestExternalOperationDoesNotBlockServiceOncePrepared(t *testing.T) {
	controller := newController(t)
	if _, err := controller.Begin(KindDeploy, "deploy", time.Hour, nil); err != nil {
		t.Fatal(err)
	}
	status, err := controller.Status()
	if err != nil {
		t.Fatal(err)
	}
	// A live deploy holds the installation: nothing else may start work on it
	// and no server may come up underneath it.
	if status.Serviceable || !status.Running {
		t.Fatalf("live deploy: %+v", status)
	}
}

func TestDeployKilledBeforeMigrationIsWrittenOff(t *testing.T) {
	controller := newController(t)
	if _, err := controller.Begin(KindDeploy, "deploy", time.Hour, nil); err != nil {
		t.Fatal(err)
	}
	// The claim lapses: the deploy died during preparation, and nothing live
	// was touched, so the installation comes back on its own.
	controller.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	status, err := controller.Status()
	if err != nil {
		t.Fatal(err)
	}
	if !status.Serviceable {
		t.Fatalf("expired preparation should be serviceable: %+v", status)
	}
	if err := controller.GateStartup(); err != nil {
		t.Fatalf("GateStartup = %v, want nil", err)
	}
}

// The P0 case. A deploy that wrote the intent to migrate and then died leaves
// an installation whose data may or may not match the images on disk. Starting
// either version against it is a guess, so nothing starts.
func TestDeployKilledDuringMigrationStaysOutOfService(t *testing.T) {
	controller := newController(t)
	record, err := controller.Begin(KindDeploy, "deploy", time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Advance(record.ID, PhaseSwitching, time.Hour, "migration başlıyor", nil); err != nil {
		t.Fatal(err)
	}
	controller.now = func() time.Time { return time.Now().Add(48 * time.Hour) }

	status, err := controller.Status()
	if err != nil {
		t.Fatal(err)
	}
	if status.Serviceable {
		t.Fatalf("interrupted migration reported serviceable: %+v", status)
	}
	if err := controller.GateStartup(); !errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("GateStartup = %v, want ErrRecoveryRequired", err)
	}
	// And a second deploy may not start on top of it, however long it has been.
	if _, err := controller.Begin(KindDeploy, "deploy", time.Hour, nil); !errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("Begin over interrupted migration = %v, want ErrRecoveryRequired", err)
	}
	if _, err := controller.Acquire(context.Background(), KindRestore, AcquireOptions{Actor: "cli"}); !errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("Acquire over interrupted migration = %v, want ErrRecoveryRequired", err)
	}
}

func TestCommittedDeployReleasesTheInstallation(t *testing.T) {
	controller := newController(t)
	record, err := controller.Begin(KindDeploy, "deploy", time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Advance(record.ID, PhaseSwitching, time.Hour, "", nil); err != nil {
		t.Fatal(err)
	}
	if err := controller.Advance(record.ID, PhaseCommitted, time.Hour, "", nil); err != nil {
		t.Fatal(err)
	}
	if err := controller.GateStartup(); err != nil {
		t.Fatalf("GateStartup after commit = %v, want nil", err)
	}
	if err := controller.Advance(record.ID, PhaseRolledBack, time.Hour, "", nil); err == nil {
		t.Fatal("advancing a closed operation should fail")
	}
}

// Hold is what the deploy script calls when it cannot reason about what it has
// left behind. It must survive a restart, which means it must survive being
// read back by a fresh controller over the same directory.
func TestHoldSurvivesRestartAndNeedsAnOperator(t *testing.T) {
	controller := newController(t)
	record, err := controller.Begin(KindDeploy, "deploy", time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Advance(record.ID, PhaseCommitted, time.Hour, "", nil); err != nil {
		t.Fatal(err)
	}
	// The migration succeeded; the health gate did not. The operation is
	// already closed, and the hold has to work anyway.
	held, err := controller.Hold(KindDeploy, "deploy", "sağlık kontrolü geçilemedi")
	if err != nil {
		t.Fatal(err)
	}
	if held.Phase != PhaseRecoveryRequired {
		t.Fatalf("hold phase = %s", held.Phase)
	}

	restarted, err := New(controller.dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.GateStartup(); !errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("GateStartup after restart = %v, want ErrRecoveryRequired", err)
	}
	// Calling it twice is safe, and the first reason is the one kept.
	again, err := restarted.Hold(KindDeploy, "deploy", "ikinci hata")
	if err != nil {
		t.Fatal(err)
	}
	if again.Error != "sağlık kontrolü geçilemedi" {
		t.Fatalf("hold reason overwritten: %q", again.Error)
	}
	if err := restarted.Resolve("elle incelendi"); err != nil {
		t.Fatal(err)
	}
	if err := restarted.GateStartup(); err != nil {
		t.Fatalf("GateStartup after resolve = %v, want nil", err)
	}
}

// The migration container run by a deploy is a writer inside the very window
// the gate closes. It is let through by naming its own operation, and by
// nothing else.
func TestOnlyTheDeploysOwnMigrationPassesTheGate(t *testing.T) {
	controller := newController(t)
	record, err := controller.Begin(KindDeploy, "deploy", time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Advance(record.ID, PhaseSwitching, time.Hour, "", nil); err != nil {
		t.Fatal(err)
	}
	if err := controller.GateStartupFor(record.ID); err != nil {
		t.Fatalf("own migration blocked: %v", err)
	}
	if err := controller.GateStartupFor("20260101T000000Z-deadbeef"); !errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("foreign id passed the gate: %v", err)
	}
	if err := controller.GateStartupFor(""); !errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("empty id passed the gate: %v", err)
	}
	// Once held, not even its own operation may write.
	if _, err := controller.Hold(KindDeploy, "deploy", "migration yarıda kaldı"); err != nil {
		t.Fatal(err)
	}
	if err := controller.GateStartupFor(record.ID); !errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("held installation passed its own gate: %v", err)
	}
}

func TestExternalOperationRefusesConcurrentWork(t *testing.T) {
	controller := newController(t)
	if _, err := controller.Begin(KindDeploy, "deploy", time.Hour, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Begin(KindDeploy, "deploy", time.Hour, nil); !errors.Is(err, ErrBusy) {
		t.Fatalf("second Begin = %v, want ErrBusy", err)
	}
	// An API backup asking for the lease finds the lock free — the deploy holds
	// nothing the kernel knows about — and must still be turned away.
	if _, err := controller.Acquire(context.Background(), KindBackup, AcquireOptions{Actor: "api"}); !errors.Is(err, ErrBusy) {
		t.Fatalf("Acquire during deploy = %v, want ErrBusy", err)
	}
}

func TestExternalPhaseIsRestricted(t *testing.T) {
	controller := newController(t)
	record, err := controller.Begin(KindDeploy, "deploy", time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Advance(record.ID, PhaseValidating, time.Hour, "", nil); err == nil {
		t.Fatal("VALIDATING should not be writable from outside")
	}
	if err := controller.Advance("başka-id", PhaseCommitted, time.Hour, "", nil); err == nil {
		t.Fatal("advancing an unknown operation should fail")
	}
}

// A note is not an error. Recording "migration tamam" in the error field made
// a successful deploy read, in `system status` and in the journal, as one that
// had failed with that message.
func TestASuccessNoteIsNotRecordedAsAnError(t *testing.T) {
	controller := newController(t)
	record, err := controller.Begin(KindDeploy, "deploy", time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Advance(record.ID, PhaseSwitching, time.Hour, "migration başlıyor", nil); err != nil {
		t.Fatal(err)
	}
	if err := controller.Advance(record.ID, PhaseCommitted, time.Hour, "migration tamam", nil); err != nil {
		t.Fatal(err)
	}
	status, err := controller.Status()
	if err != nil {
		t.Fatal(err)
	}
	if status.Operation.Error != "" {
		t.Fatalf("başarılı deploy hata taşıyor: %q", status.Operation.Error)
	}
	// But the note is not thrown away either: the journal keeps it.
	entries, err := controller.History(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range entries {
		if entry.Phase == PhaseCommitted && entry.Fields["note"] == "migration tamam" {
			found = true
		}
	}
	if !found {
		t.Fatal("aşama notu günlüğe yazılmadı")
	}

	// A failure still records its cause where an operator looks for it.
	held, err := controller.Hold(KindDeploy, "deploy", "sağlık kontrolü geçilemedi")
	if err != nil {
		t.Fatal(err)
	}
	if held.Error != "sağlık kontrolü geçilemedi" {
		t.Fatalf("hata kaydedilmedi: %q", held.Error)
	}
}
