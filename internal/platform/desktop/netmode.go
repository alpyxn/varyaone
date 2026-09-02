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

const (
	firewallRuleHTTP = "Varya One (8080)"
	firewallRuleMDNS = "Varya One (mDNS)"
)

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
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Always clear both rules first (idempotent; ok if absent).
	for _, name := range []string{firewallRuleHTTP, firewallRuleMDNS} {
		_ = exec.CommandContext(ctx, "netsh", "advfirewall", "firewall", "delete", "rule",
			"name="+name).Run()
	}
	if m != NetLAN {
		return nil
	}

	// Inbound on the private + domain profiles only (never public): the HTTP API
	// on 8080/TCP and mDNS discovery on 5353/UDP. A service runs non-interactively
	// as LocalSystem, so Windows silently blocks inbound with no prompt — without
	// the 5353 rule the thin client's auto-discovery would not see this server.
	rules := []struct {
		name, proto, lport string
	}{
		{firewallRuleHTTP, "TCP", fmt.Sprintf("%d", port)},
		{firewallRuleMDNS, "UDP", "5353"},
	}
	for _, r := range rules {
		add := exec.CommandContext(ctx, "netsh", "advfirewall", "firewall", "add", "rule",
			"name="+r.name, "dir=in", "action=allow", "enable=yes",
			"profile=private,domain", "protocol="+r.proto, "localport="+r.lport)
		if out, err := add.CombinedOutput(); err != nil {
			return fmt.Errorf("netsh add rule %q: %w: %s", r.name, err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}
