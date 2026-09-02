//go:build !windows

package desktop

import "github.com/kardianos/service"

func controlManagedService(svc service.Service, action string) error {
	return service.Control(svc, action)
}

func stopManagedService(svc service.Service) error {
	return svc.Stop()
}

func startManagedService(svc service.Service) error {
	return svc.Start()
}

func managedServiceStopped() (bool, error) {
	installed, running := ServiceState()
	return !installed || !running, nil
}
