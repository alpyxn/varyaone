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
	firewallRuleHTTP       = "Varya One (HTTP)"
	legacyFirewallRuleHTTP = "Varya One (8080)"
	firewallRuleMDNS       = "Varya One (mDNS)"
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

// networkModeFile is the on-disk toggle under Home; absent means local-only.
// The installer writes an explicit choice, while a portable bundle must never
// expose itself to the LAN merely because a file is missing.
func (l Layout) networkModeFile() string { return filepath.Join(l.Home, "network-mode") }

// NetworkMode reads the persisted mode, defaulting safely to NetLocal.
func (l Layout) NetworkMode() NetMode {
	b, err := os.ReadFile(l.networkModeFile())
	if err != nil {
		return NetLocal
	}
	if NetMode(strings.TrimSpace(string(b))) == NetLAN {
		return NetLAN
	}
	return NetLocal
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
func ApplyNetworkMode(m NetMode, logger *slog.Logger) (resultErr error) {
	defer func() { recordControlResult("netmode "+string(m), resultErr) }()
	if m != NetLocal && m != NetLAN {
		return os.ErrInvalid
	}
	if logger == nil {
		logger = slog.Default()
	}
	l := DiscoverLayout()
	oldMode := l.NetworkMode()

	if runtime.GOOS == "windows" {
		port := HTTPPort()
		if err := reconcileFirewall(m, port); err != nil {
			return fmt.Errorf("reconcile firewall: %w", err)
		}
	}
	if err := l.SetNetworkMode(m); err != nil {
		if runtime.GOOS == "windows" {
			_ = reconcileFirewall(oldMode, HTTPPort())
		}
		return fmt.Errorf("persist network mode: %w", err)
	}
	logger.Info("network mode set", "mode", m)

	if err := RestartIfRunning(); err != nil {
		_ = l.SetNetworkMode(oldMode)
		if runtime.GOOS == "windows" {
			_ = reconcileFirewall(oldMode, HTTPPort())
		}
		return fmt.Errorf("restart service: %w", err)
	}
	return nil
}

func reconcileFirewall(m NetMode, port int) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Always clear both rules first (idempotent; ok if absent).
	for _, name := range []string{firewallRuleHTTP, legacyFirewallRuleHTTP, firewallRuleMDNS} {
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
