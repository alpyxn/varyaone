package desktop

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const firewallRuleName = "Varya One (8080)"

// NetMode is how far the desktop server exposes itself.
//
//	NetLocal — bind 127.0.0.1 only, no firewall rule, no mDNS: this machine only.
//	NetLAN   — bind 0.0.0.0, firewall rule on 8080, advertise over mDNS.
type NetMode string

const (
	NetLocal NetMode = "local"
	NetLAN   NetMode = "lan"
)

// networkModeFile is the on-disk toggle under Home; absent means NetLAN.
func (l Layout) networkModeFile() string { return filepath.Join(l.Home, "network-mode") }

// NetworkMode reads the persisted mode, defaulting to NetLAN.
func (l Layout) NetworkMode() NetMode {
	b, err := os.ReadFile(l.networkModeFile())
	if err != nil {
		return NetLAN
	}
	if NetMode(strings.TrimSpace(string(b))) == NetLocal {
		return NetLocal
	}
	return NetLAN
}

// SetNetworkMode persists the mode. An unknown value is rejected.
func (l Layout) SetNetworkMode(m NetMode) error {
	if m != NetLocal && m != NetLAN {
		return os.ErrInvalid
	}
	if err := os.MkdirAll(l.Home, 0o755); err != nil {
		return err
	}
	return os.WriteFile(l.networkModeFile(), []byte(m), 0o644)
}

// BindHost is the interface the HTTP server should listen on for this mode.
func (m NetMode) BindHost() string {
	if m == NetLocal {
		return "127.0.0.1"
	}
	return "0.0.0.0"
}

// ApplyNetworkMode is the `varyaone netmode <local|lan>` action: persist the mode,
// reconcile the Windows Firewall rule for port 8080, and restart the service if it
// is running so the new bind address takes effect. Requires administrator rights
// on Windows (the desktop client invokes it elevated).
func ApplyNetworkMode(m NetMode, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}
	l := DiscoverLayout()
	if err := l.SetNetworkMode(m); err != nil {
		return fmt.Errorf("persist network mode: %w", err)
	}
	logger.Info("network mode set", "mode", m)

	if runtime.GOOS == "windows" {
		port := DefaultHTTPPort
		if err := reconcileFirewall(m, port); err != nil {
			// A missing rule on removal, or a duplicate on add, is not fatal.
			logger.Warn("firewall reconcile reported an error", "error", err)
		}
	}

	if err := RestartIfRunning(); err != nil {
		return fmt.Errorf("restart service: %w", err)
	}
	return nil
}

func reconcileFirewall(m NetMode, port int) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	del := exec.CommandContext(ctx, "netsh", "advfirewall", "firewall", "delete", "rule",
		"name="+firewallRuleName)
	_ = del.Run() // ok if it did not exist

	if m != NetLAN {
		return nil
	}
	add := exec.CommandContext(ctx, "netsh", "advfirewall", "firewall", "add", "rule",
		"name="+firewallRuleName, "dir=in", "action=allow", "protocol=TCP",
		fmt.Sprintf("localport=%d", port))
	if out, err := add.CombinedOutput(); err != nil {
		return fmt.Errorf("netsh add rule: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
