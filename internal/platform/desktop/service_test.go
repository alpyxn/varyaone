package desktop

import (
	"errors"
	"testing"

	"github.com/kardianos/service"
)

type fakeServiceLifecycle struct {
	status     service.Status
	statusErr  error
	installErr error
	startErr   error
	installed  int
	started    int
}

func (f *fakeServiceLifecycle) Status() (service.Status, error) { return f.status, f.statusErr }
func (f *fakeServiceLifecycle) Install() error {
	f.installed++
	return f.installErr
}
func (f *fakeServiceLifecycle) Start() error {
	f.started++
	return f.startErr
}

func TestEnsureServiceInstallsMissingServiceThenStarts(t *testing.T) {
	fake := &fakeServiceLifecycle{statusErr: service.ErrNotInstalled}
	if err := ensureService(fake); err != nil {
		t.Fatal(err)
	}
	if fake.installed != 1 || fake.started != 1 {
		t.Fatalf("install/start calls = %d/%d, want 1/1", fake.installed, fake.started)
	}
}

func TestEnsureServiceStartsStoppedService(t *testing.T) {
	fake := &fakeServiceLifecycle{status: service.StatusStopped}
	if err := ensureService(fake); err != nil {
		t.Fatal(err)
	}
	if fake.installed != 0 || fake.started != 1 {
		t.Fatalf("install/start calls = %d/%d, want 0/1", fake.installed, fake.started)
	}
}

func TestEnsureServiceLeavesRunningServiceAlone(t *testing.T) {
	fake := &fakeServiceLifecycle{status: service.StatusRunning}
	if err := ensureService(fake); err != nil {
		t.Fatal(err)
	}
	if fake.installed != 0 || fake.started != 0 {
		t.Fatalf("install/start calls = %d/%d, want 0/0", fake.installed, fake.started)
	}
}

func TestEnsureServiceDoesNotInstallOnUnknownStatusError(t *testing.T) {
	fake := &fakeServiceLifecycle{statusErr: errors.New("access denied")}
	if err := ensureService(fake); err == nil {
		t.Fatal("expected status error")
	}
	if fake.installed != 0 || fake.started != 0 {
		t.Fatalf("install/start calls = %d/%d, want 0/0", fake.installed, fake.started)
	}
}

type fakeRepairLifecycle struct {
	statuses []struct {
		status service.Status
		err    error
	}
	statusCall  int
	installed   int
	uninstalled int
	started     int
	stopped     int
}

func (f *fakeRepairLifecycle) Status() (service.Status, error) {
	index := f.statusCall
	f.statusCall++
	if index >= len(f.statuses) {
		index = len(f.statuses) - 1
	}
	return f.statuses[index].status, f.statuses[index].err
}
func (f *fakeRepairLifecycle) Install() error   { f.installed++; return nil }
func (f *fakeRepairLifecycle) Uninstall() error { f.uninstalled++; return nil }
func (f *fakeRepairLifecycle) Start() error     { f.started++; return nil }
func (f *fakeRepairLifecycle) Stop() error      { f.stopped++; return nil }

func TestRepairServiceReplacesRunningRegistration(t *testing.T) {
	fake := &fakeRepairLifecycle{statuses: []struct {
		status service.Status
		err    error
	}{
		{status: service.StatusRunning},
		{status: service.StatusUnknown, err: service.ErrNotInstalled},
	}}
	if err := repairService(fake); err != nil {
		t.Fatal(err)
	}
	if fake.stopped != 1 || fake.uninstalled != 1 || fake.installed != 1 || fake.started != 1 {
		t.Fatalf("stop/uninstall/install/start = %d/%d/%d/%d", fake.stopped, fake.uninstalled, fake.installed, fake.started)
	}
}

func TestRepairServiceInstallsMissingRegistration(t *testing.T) {
	fake := &fakeRepairLifecycle{statuses: []struct {
		status service.Status
		err    error
	}{{status: service.StatusUnknown, err: service.ErrNotInstalled}}}
	if err := repairService(fake); err != nil {
		t.Fatal(err)
	}
	if fake.uninstalled != 0 || fake.installed != 1 || fake.started != 1 {
		t.Fatalf("uninstall/install/start = %d/%d/%d", fake.uninstalled, fake.installed, fake.started)
	}
}
