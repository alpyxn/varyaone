package desktop

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"syscall"
	"time"

	"github.com/kardianos/service"
)

// ServiceName is the stable Windows SCM/system service identifier. The desktop
// client uses the same value for its low-privilege status query.
const ServiceName = "VaryaOne"

func serviceConfig() *service.Config {
	return &service.Config{
		Name:        ServiceName,
		DisplayName: "Varya One",
		Description: "Varya One ERP sunucusu (API, worker, veritabanı ve arayüz).",
		Arguments:   []string{"stack"},
		Option: service.KeyValue{
			"DelayedAutoStart": true,
			// Auto-restart on crash (e.g. a transient DB start failure): the
			// service exits non-zero, SCM waits 15s and starts it again.
			"OnFailure":              "restart",
			"OnFailureDelayDuration": "15s",
			"OnFailureResetPeriod":   86400,
		},
	}
}

type program struct {
	logger *slog.Logger
	cancel context.CancelFunc
	done   chan struct{}
}

func (p *program) Start(service.Service) error {
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.done = make(chan struct{})
	go func() {
		defer close(p.done)
		if err := NewSupervisor(p.logger).Run(ctx); err != nil && ctx.Err() == nil {
			// The stack died on its own (initdb/migration/bind failure). Log it
			// where it can be seen and exit non-zero so the OS service manager
			// reports the service as stopped/failed instead of a false "running"
			// with a dead worker inside.
			p.logger.Error("stack exited with error — terminating service", "error", err)
			os.Exit(1)
		}
	}()
	return nil
}

func (p *program) Stop(service.Service) error {
	if p.cancel != nil {
		p.cancel()
	}
	if p.done != nil {
		select {
		case <-p.done:
		case <-time.After(18 * time.Second):
			return errors.New("stack did not stop within 18 seconds")
		}
	}
	return nil
}

// RunAsService runs the supervisor under the OS service manager (or directly in
// the foreground when not managed, e.g. during development).
func RunAsService(logger *slog.Logger) error {
	svc, err := service.New(&program{logger: logger}, serviceConfig())
	if err != nil {
		return err
	}
	return svc.Run()
}

// Control performs install / uninstall / start / stop / restart / status.
func Control(action string) (resultErr error) {
	defer func() { recordControlResult("service "+action, resultErr) }()
	svc, err := service.New(&program{logger: slog.Default()}, serviceConfig())
	if err != nil {
		return err
	}
	if action == "ensure" {
		if err := ensureManagedService(svc); err != nil {
			return err
		}
		return reconcileInstalledServiceNetwork()
	}
	if action == "repair" {
		return repairManagedService(svc)
	}
	if action == "status" {
		status, statErr := svc.Status()
		if statErr != nil {
			return statErr
		}
		fmt.Println(statusText(status))
		return nil
	}
	if action == "wait-ready" {
		ctx, cancel := context.WithTimeout(context.Background(), serviceReadyTimeout)
		defer cancel()
		return WaitForReady(ctx, HTTPPort())
	}
	switch action {
	case "start", "stop", "restart":
		return controlManagedService(svc, action)
	default:
		return service.Control(svc, action)
	}
}

func reconcileInstalledServiceNetwork() error {
	if runtime.GOOS != "windows" {
		return nil
	}
	if err := reconcileFirewall(DiscoverLayout().NetworkMode(), HTTPPort()); err != nil {
		return fmt.Errorf("reconcile service firewall: %w", err)
	}
	return nil
}

// repairService deliberately rebuilds the SCM registration. It is kept as a
// separate explicit panel action because reinstalling a healthy registration
// would be surprising during an ordinary start.
type repairLifecycle interface {
	Status() (service.Status, error)
	Install() error
	Uninstall() error
	Start() error
	Stop() error
}

func repairService(svc repairLifecycle) error {
	return repairServiceWith(svc, svc.Stop, svc.Start)
}

// repairManagedService uses the platform's bounded start/stop implementation.
// On Windows this avoids an upstream service-library stop loop that can wait
// forever after its timeout has elapsed.
func repairManagedService(svc service.Service) error {
	// kardianos/service collapses PAUSED, STOP_PENDING and STOPPED into the same
	// status. Ask our idempotent native stop path first whenever registration is
	// present, so repair never deletes a still-running paused/pending process.
	if _, err := svc.Status(); err == nil {
		if err := stopManagedService(svc); err != nil {
			return fmt.Errorf("stop service for repair: %w", err)
		}
	}
	return repairServiceWith(
		svc,
		func() error { return nil },
		func() error { return startManagedService(svc) },
	)
}

func repairServiceWith(svc repairLifecycle, stop, start func() error) error {
	status, err := svc.Status()
	markedForDelete := errors.Is(err, syscall.Errno(1072)) // ERROR_SERVICE_MARKED_FOR_DELETE
	if err != nil && !errors.Is(err, service.ErrNotInstalled) && !markedForDelete {
		return fmt.Errorf("query service before repair: %w", err)
	}
	if err == nil {
		if status == service.StatusRunning {
			if stopErr := stop(); stopErr != nil {
				return fmt.Errorf("stop service for repair: %w", stopErr)
			}
		}
		if uninstallErr := svc.Uninstall(); uninstallErr != nil {
			return fmt.Errorf("remove old service registration: %w", uninstallErr)
		}
		markedForDelete = true
	}
	if markedForDelete {
		deadline := time.Now().Add(15 * time.Second)
		for {
			_, statusErr := svc.Status()
			if errors.Is(statusErr, service.ErrNotInstalled) {
				break
			}
			if statusErr != nil && !errors.Is(statusErr, syscall.Errno(1072)) {
				return fmt.Errorf("wait for old service deletion: %w", statusErr)
			}
			if time.Now().After(deadline) {
				return errors.New("old service registration was not deleted within 15 seconds")
			}
			time.Sleep(250 * time.Millisecond)
		}
	}
	if err := svc.Install(); err != nil {
		return fmt.Errorf("install repaired service: %w", err)
	}
	if err := reconcileInstalledServiceNetwork(); err != nil {
		_ = svc.Uninstall()
		return err
	}
	if err := start(); err != nil {
		return fmt.Errorf("start repaired service: %w", err)
	}
	return nil
}

// serviceLifecycle is the small portion of service.Service needed by ensure.
// Keeping it narrow makes the install/start recovery path unit-testable without
// touching the host service manager.
type serviceLifecycle interface {
	Status() (service.Status, error)
	Install() error
	Start() error
}

// ensureService makes the service usable from either a clean/portable bundle or
// an installation whose service registration was removed. It is idempotent: an
// already-running service is left alone, a stopped one is started, and a missing
// one is installed before it is started.
func ensureService(svc serviceLifecycle) error {
	return ensureServiceWith(svc, svc.Start)
}

func ensureManagedService(svc service.Service) error {
	return ensureServiceWith(svc, func() error { return startManagedService(svc) })
}

func ensureServiceWith(svc serviceLifecycle, start func() error) error {
	status, err := svc.Status()
	if errors.Is(err, service.ErrNotInstalled) {
		if err := svc.Install(); err != nil {
			return fmt.Errorf("install service: %w", err)
		}
		if err := start(); err != nil {
			return fmt.Errorf("start newly installed service: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("query service: %w", err)
	}
	if status == service.StatusRunning {
		return nil
	}
	if err := start(); err != nil {
		return fmt.Errorf("start service: %w", err)
	}
	return nil
}

// ServiceState reports whether the OS service is registered and, if so, running.
func ServiceState() (installed, running bool) {
	svc, err := service.New(&program{logger: slog.Default()}, serviceConfig())
	if err != nil {
		return false, false
	}
	status, err := svc.Status()
	if err != nil {
		return false, false
	}
	return true, status == service.StatusRunning
}

// ServiceInstalled reports whether the OS service is registered.
func ServiceInstalled() bool {
	installed, _ := ServiceState()
	return installed
}

// RestartIfRunning restarts the service only when it is currently running, so it
// is a no-op during install (service registered but not yet started).
func RestartIfRunning() error {
	installed, running := ServiceState()
	if !installed || !running {
		return nil
	}
	svc, err := service.New(&program{logger: slog.Default()}, serviceConfig())
	if err != nil {
		return err
	}
	return controlManagedService(svc, "restart")
}

func statusText(s service.Status) string {
	switch s {
	case service.StatusRunning:
		return "running"
	case service.StatusStopped:
		return "stopped"
	default:
		return "unknown (not installed?)"
	}
}
