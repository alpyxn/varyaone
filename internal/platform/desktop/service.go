package desktop

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

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
		<-p.done
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
func Control(action string) error {
	svc, err := service.New(&program{logger: slog.Default()}, serviceConfig())
	if err != nil {
		return err
	}
	if action == "ensure" {
		return ensureService(svc)
	}
	if action == "status" {
		status, statErr := svc.Status()
		if statErr != nil {
			return statErr
		}
		fmt.Println(statusText(status))
		return nil
	}
	return service.Control(svc, action)
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
	status, err := svc.Status()
	if errors.Is(err, service.ErrNotInstalled) {
		if err := svc.Install(); err != nil {
			return fmt.Errorf("install service: %w", err)
		}
		if err := svc.Start(); err != nil {
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
	if err := svc.Start(); err != nil {
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
	return service.Control(svc, "restart")
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
