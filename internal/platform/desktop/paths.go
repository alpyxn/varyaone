// Package desktop turns the Varya One binary into a self-contained Windows
// service: it manages a bundled PostgreSQL, runs migrations, serves the API plus
// the embedded SPA, advertises itself on the LAN over mDNS, and applies updates
// by swapping prebuilt release artifacts.
//
// It is intentionally OS-agnostic in structure (so it builds and unit-tests on
// Linux); only tool discovery and the service integration differ per platform.
package desktop

import (
	"os"
	"path/filepath"
	"runtime"
)

// Layout resolves the on-disk locations the desktop runtime uses. Everything is
// overridable with VARYAONE_DESKTOP_HOME for development and tests.
type Layout struct {
	// Home is the writable data root (pgdata, storage, backups, logs, rollback).
	Home string
	// InstallDir is the read-only program directory (binary, pgsql/, web bundle).
	InstallDir string
}

// DiscoverLayout picks sensible defaults for the current OS.
func DiscoverLayout() Layout {
	install := installDir()
	home := os.Getenv("VARYAONE_DESKTOP_HOME")
	if home == "" {
		home = defaultHome()
	}
	return Layout{Home: home, InstallDir: install}
}

func (l Layout) PGData() string    { return filepath.Join(l.Home, "pgdata") }
func (l Layout) Storage() string   { return filepath.Join(l.Home, "storage") }
func (l Layout) Backups() string   { return filepath.Join(l.Home, "backups") }
func (l Layout) Logs() string      { return filepath.Join(l.Home, "logs") }
func (l Layout) Rollback() string  { return filepath.Join(l.Home, "rollback") }
func (l Layout) Downloads() string { return filepath.Join(l.Home, "downloads") }

// EnsureDirs creates the writable directories.
func (l Layout) EnsureDirs() error {
	for _, d := range []string{l.Home, l.Storage(), l.Backups(), l.Logs(), l.Downloads()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func installDir() string {
	exe, err := os.Executable()
	if err != nil {
		if wd, wdErr := os.Getwd(); wdErr == nil {
			return wd
		}
		return "."
	}
	return filepath.Dir(exe)
}

func defaultHome() string {
	switch runtime.GOOS {
	case "windows":
		if pd := os.Getenv("ProgramData"); pd != "" {
			return filepath.Join(pd, "VaryaOne")
		}
		return filepath.Join(installDir(), "data")
	default:
		if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
			return filepath.Join(xdg, "varyaone")
		}
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, ".local", "share", "varyaone")
		}
		return filepath.Join(installDir(), "data")
	}
}
