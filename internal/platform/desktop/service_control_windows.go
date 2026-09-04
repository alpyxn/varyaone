//go:build windows

package desktop

import (
	"errors"
	"fmt"
	"time"

	"github.com/kardianos/service"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const serviceTransitionTimeout = 30 * time.Second

// Recovery-action settings mirrored from serviceConfig(); reconcileServiceStartup
// writes them onto registrations that predate them.
const (
	serviceRestartDelay       = 15 * time.Second
	serviceFailureResetPeriod = 86400
)

// controlManagedService deliberately does not call kardianos/service's Windows
// Stop/Restart implementation: v1.3.0 breaks only out of its select when its
// timeout fires, leaving the surrounding polling loop alive forever.
func controlManagedService(_ service.Service, action string) error {
	m, managed, err := openManagedWindowsService()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	defer managed.Close()

	switch action {
	case "start":
		return startWindowsService(managed)
	case "stop":
		return stopWindowsService(managed)
	case "restart":
		if err := stopWindowsService(managed); err != nil {
			return err
		}
		return startWindowsService(managed)
	default:
		return fmt.Errorf("unsupported Windows service action %q", action)
	}
}

func stopManagedService(svc service.Service) error {
	return controlManagedService(svc, "stop")
}

func startManagedService(svc service.Service) error {
	return controlManagedService(svc, "start")
}
func openManagedWindowsService() (*mgr.Mgr, *mgr.Service, error) {
	h, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return nil, nil, fmt.Errorf("connect to Windows service manager: %w", err)
	}
	m := &mgr.Mgr{Handle: h}
	name, err := windows.UTF16PtrFromString(ServiceName)
	if err != nil {
		m.Disconnect()
		return nil, nil, err
	}
	serviceHandle, err := windows.OpenService(
		m.Handle,
		name,
		windows.SERVICE_QUERY_STATUS|windows.SERVICE_START|windows.SERVICE_STOP,
	)
	if err != nil {
		m.Disconnect()
		return nil, nil, fmt.Errorf("open Windows service %s: %w", ServiceName, err)
	}
	return m, &mgr.Service{Name: ServiceName, Handle: serviceHandle}, nil
}

func startWindowsService(managed *mgr.Service) error {
	status, err := managed.Query()
	if err != nil {
		return fmt.Errorf("query service before start: %w", err)
	}
	switch status.State {
	case svc.Running:
		return nil
	case svc.StartPending:
		return waitForWindowsService(managed, svc.Running, serviceTransitionTimeout)
	case svc.ContinuePending:
		return waitForWindowsService(managed, svc.Running, serviceTransitionTimeout)
	case svc.PausePending:
		if err := waitForWindowsService(managed, svc.Paused, serviceTransitionTimeout); err != nil {
			return err
		}
		if _, err := managed.Control(svc.Continue); err != nil {
			return fmt.Errorf("request service continue: %w", err)
		}
		return waitForWindowsService(managed, svc.Running, serviceTransitionTimeout)
	case svc.Paused:
		if _, err := managed.Control(svc.Continue); err != nil {
			return fmt.Errorf("request service continue: %w", err)
		}
		return waitForWindowsService(managed, svc.Running, serviceTransitionTimeout)
	case svc.StopPending:
		if err := waitForWindowsService(managed, svc.Stopped, serviceTransitionTimeout); err != nil {
			return err
		}
	case svc.Stopped:
		// Start below.
	default:
		return fmt.Errorf("service cannot be started from state %s", windowsServiceState(status.State))
	}
	if err := managed.Start(); err != nil && !errors.Is(err, windows.ERROR_SERVICE_ALREADY_RUNNING) {
		return fmt.Errorf("request service start: %w", err)
	}
	return waitForWindowsService(managed, svc.Running, serviceTransitionTimeout)
}

func stopWindowsService(managed *mgr.Service) error {
	status, err := managed.Query()
	if err != nil {
		return fmt.Errorf("query service before stop: %w", err)
	}
	switch status.State {
	case svc.Stopped:
		return nil
	case svc.StopPending:
		return waitForWindowsService(managed, svc.Stopped, serviceTransitionTimeout)
	case svc.StartPending:
		if err := waitForWindowsService(managed, svc.Running, serviceTransitionTimeout); err != nil {
			return err
		}
	case svc.ContinuePending:
		if err := waitForWindowsService(managed, svc.Running, serviceTransitionTimeout); err != nil {
			return err
		}
	case svc.PausePending:
		if err := waitForWindowsService(managed, svc.Paused, serviceTransitionTimeout); err != nil {
			return err
		}
	case svc.Running, svc.Paused:
		// Stop below.
	default:
		return fmt.Errorf("service cannot be stopped from state %s", windowsServiceState(status.State))
	}
	if _, err := managed.Control(svc.Stop); err != nil && !errors.Is(err, windows.ERROR_SERVICE_NOT_ACTIVE) {
		return fmt.Errorf("request service stop: %w", err)
	}
	return waitForWindowsService(managed, svc.Stopped, serviceTransitionTimeout)
}

func waitForWindowsService(managed *mgr.Service, wanted svc.State, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		status, err := managed.Query()
		if err != nil {
			return fmt.Errorf("query service transition: %w", err)
		}
		if status.State == wanted {
			return nil
		}
		if wanted == svc.Running && status.State == svc.Stopped {
			return fmt.Errorf(
				"service stopped during startup (win32=%d, service=%d)",
				status.Win32ExitCode,
				status.ServiceSpecificExitCode,
			)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf(
				"service did not reach %s within %s (last state: %s)",
				windowsServiceState(wanted), timeout, windowsServiceState(status.State),
			)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func windowsServiceState(state svc.State) string {
	switch state {
	case svc.Stopped:
		return "stopped"
	case svc.StartPending:
		return "start-pending"
	case svc.StopPending:
		return "stop-pending"
	case svc.Running:
		return "running"
	case svc.ContinuePending:
		return "continue-pending"
	case svc.PausePending:
		return "pause-pending"
	case svc.Paused:
		return "paused"
	default:
		return fmt.Sprintf("unknown(%d)", state)
	}
}

// reconcileServiceStartup forces an already-registered service back to plain
// automatic (boot) start and re-applies the crash recovery action.
//
// Without it a machine keeps whatever start type its first install wrote:
// builds before this one registered the service as *delayed* auto-start, which
// makes Windows hold the whole stack back for ~2 minutes after a reboot, and a
// manual/disabled start type (set by hand, or by an aborted update) means the
// server simply never comes back after a restart. Both look identical to the
// user — "I restarted the PC and the server is not running".
//
// A missing registration is not an error: `service ensure` installs it right
// after, already with the correct configuration.
func reconcileServiceStartup() error {
	h, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return fmt.Errorf("connect to Windows service manager: %w", err)
	}
	m := &mgr.Mgr{Handle: h}
	defer m.Disconnect()

	name, err := windows.UTF16PtrFromString(ServiceName)
	if err != nil {
		return err
	}
	serviceHandle, err := windows.OpenService(
		m.Handle,
		name,
		// SERVICE_START is required alongside SERVICE_CHANGE_CONFIG to write a
		// failure action of type "restart".
		windows.SERVICE_QUERY_CONFIG|windows.SERVICE_CHANGE_CONFIG|windows.SERVICE_START,
	)
	if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open Windows service %s: %w", ServiceName, err)
	}
	managed := &mgr.Service{Name: ServiceName, Handle: serviceHandle}
	defer managed.Close()

	cfg, err := managed.Config()
	if err != nil {
		return fmt.Errorf("read Windows service config: %w", err)
	}
	if cfg.StartType != mgr.StartAutomatic || cfg.DelayedAutoStart {
		cfg.StartType = mgr.StartAutomatic
		cfg.DelayedAutoStart = false
		if err := managed.UpdateConfig(cfg); err != nil {
			return fmt.Errorf("set Windows service to automatic start: %w", err)
		}
	}
	if err := managed.SetRecoveryActions(
		[]mgr.RecoveryAction{{Type: mgr.ServiceRestart, Delay: serviceRestartDelay}},
		serviceFailureResetPeriod,
	); err != nil {
		return fmt.Errorf("set Windows service recovery actions: %w", err)
	}
	return nil
}
