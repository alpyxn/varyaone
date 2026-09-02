//go:build windows

// Command varyaone-client is the Varya One desktop app: a WebView2 window that
// discovers a Varya One server on the LAN (or takes a typed address), remembers
// the last working one, and — launched with --panel — shows a small control
// panel for the local service (status, network mode, restart, logs).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/alpyxn/varyaone/internal/platform/desktop"
	"github.com/grandcat/zeroconf"
	"github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
)

func main() {
	runtime.LockOSThread()

	panel, hidden := false, false
	for _, a := range os.Args[1:] {
		switch a {
		case "--panel":
			panel = true
		case "--tray": // login autostart: control panel, start hidden in the tray
			panel, hidden = true, true
		}
	}

	title := "Varya One"
	w, h := 1100, 760
	if panel {
		title, w, h = "Varya Kontrol Paneli", 520, 560
	}

	view := webview2.NewWithOptions(webview2.WebViewOptions{
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title: title, Width: uint(w), Height: uint(h), Center: true,
		},
	})
	if view == nil {
		popup("Microsoft Edge WebView2 çalışma zamanı bulunamadı.\n" +
			"Lütfen WebView2 Runtime'ı kurup tekrar deneyin.")
		return
	}
	defer view.Destroy()

	app := &clientApp{view: view}
	app.bind()

	if panel {
		view.SetHtml(panelHTML)
		setupTray(uintptr(view.Window()), hidden)
	} else {
		view.SetHtml(connectHTML)
	}
	view.Run()
}

type serverInfo struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type clientConfig struct {
	LastURL string   `json:"lastURL"`
	History []string `json:"history"`
}

type panelState struct {
	ServiceInstalled bool     `json:"serviceInstalled"`
	Running          bool     `json:"running"`
	Serving          bool     `json:"serving"` // HTTP server actually answering on 127.0.0.1
	Mode             string   `json:"mode"`
	URLs             []string `json:"urls"`
	Version          string   `json:"version"`
}

type clientApp struct {
	view    webview2.WebView
	mu      sync.Mutex
	current string
}

func (a *clientApp) bind() {
	_ = a.view.Bind("hostDiscover", a.discover)
	_ = a.view.Bind("hostSaved", a.saved)
	_ = a.view.Bind("hostConnect", a.connect)
	_ = a.view.Bind("hostOpenServer", a.openServer)
	_ = a.view.Bind("hostOpenPanel", a.openPanel)
	_ = a.view.Bind("hostPanelState", a.panelState)
	_ = a.view.Bind("hostApplyMode", a.applyMode)
	_ = a.view.Bind("hostRestart", a.restartService)
	_ = a.view.Bind("hostOpenLogs", a.openLogs)
}

// discover browses mDNS for ~2.5s and returns the servers it saw.
func (a *clientApp) discover() ([]serverInfo, error) {
	resolver, err := zeroconf.NewResolver(nil)
	if err != nil {
		return nil, err
	}
	entries := make(chan *zeroconf.ServiceEntry, 8)
	ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel()

	seen := map[string]serverInfo{}
	var mu sync.Mutex
	done := make(chan struct{})
	go func() {
		defer close(done)
		for e := range entries {
			for _, ip := range e.AddrIPv4 {
				u := fmt.Sprintf("http://%s:%d", ip.String(), e.Port)
				mu.Lock()
				seen[u] = serverInfo{Name: strings.TrimSuffix(e.Instance, " — Varya One"), URL: u}
				mu.Unlock()
			}
		}
	}()
	if err := resolver.Browse(ctx, desktop.ServiceType, "local.", entries); err != nil {
		return nil, err
	}
	<-ctx.Done()
	<-done

	mu.Lock()
	defer mu.Unlock()
	out := make([]serverInfo, 0, len(seen))
	for _, s := range seen {
		out = append(out, s)
	}
	return out, nil
}

func (a *clientApp) saved() (clientConfig, error) {
	return loadConfig(), nil
}

func (a *clientApp) connect(raw string) error {
	target, err := normalizeURL(raw)
	if err != nil {
		return fmt.Errorf("geçersiz adres: %w", err)
	}
	if err := ping(target); err != nil {
		return fmt.Errorf("bağlanılamadı (%s)", target)
	}
	a.mu.Lock()
	a.current = target
	a.mu.Unlock()
	rememberURL(target)

	a.view.Dispatch(func() {
		// Float a small "⚙ Kontrol Paneli" affordance over the served app.
		a.view.Init(controlPanelButtonJS)
		a.view.SetTitle("Varya One — " + target)
		a.view.Navigate(target)
	})
	return nil
}

const controlPanelButtonJS = `(function(){
	if (window.__voBar) return; window.__voBar = 1;
	var b = document.createElement('button');
	b.textContent = '⚙';
	b.title = 'Kontrol Paneli';
	b.style.cssText = 'position:fixed;right:12px;bottom:12px;z-index:2147483647;'+
		'width:38px;height:38px;border-radius:50%;border:0;cursor:pointer;'+
		'background:#c1272d;color:#fff;font-size:18px;box-shadow:0 2px 8px rgba(0,0,0,.3)';
	b.onclick = function(){ window.hostOpenPanel && window.hostOpenPanel(); };
	(document.body||document.documentElement).appendChild(b);
})();`

func (a *clientApp) openServer() error {
	a.mu.Lock()
	cur := a.current
	a.mu.Unlock()
	if cur == "" {
		cur = loadConfig().LastURL
	}
	if cur == "" {
		cur = fmt.Sprintf("http://127.0.0.1:%d", desktop.DefaultHTTPPort)
	}
	// Validate the endpoint and remember it just like the normal connection
	// screen. Previously a fresh control panel had no current/saved URL, so the
	// button failed silently and never opened its own local server.
	return a.connect(cur)
}

func (a *clientApp) openPanel() error {
	a.view.Dispatch(func() {
		a.view.SetTitle("Varya Kontrol Paneli")
		a.view.SetHtml(panelHTML)
	})
	return nil
}

func (a *clientApp) panelState() (panelState, error) {
	layout := desktop.DiscoverLayout()
	installed, running := serviceState()
	mode := string(layout.NetworkMode())

	urls := []string{fmt.Sprintf("http://127.0.0.1:%d", desktop.DefaultHTTPPort)}
	if mode == string(desktop.NetLAN) {
		urls = append(urls, desktop.LANURLs(desktop.DefaultHTTPPort)...)
	}
	return panelState{
		ServiceInstalled: installed,
		Running:          running,
		Serving:          ping(fmt.Sprintf("http://127.0.0.1:%d", desktop.DefaultHTTPPort)) == nil,
		Mode:             mode,
		URLs:             urls,
		Version:          version,
	}, nil
}

func (a *clientApp) applyMode(mode string) error {
	if mode != string(desktop.NetLocal) && mode != string(desktop.NetLAN) {
		return fmt.Errorf("geçersiz mod")
	}
	return runElevated(siblingExe("varyaone.exe"), "netmode", mode)
}

func (a *clientApp) restartService() error {
	installed, running := serviceState()
	action := "restart"
	if !installed {
		action = "ensure"
	} else if !running {
		action = "start"
	}
	return runElevated(siblingExe("varyaone.exe"), "service", action)
}

func (a *clientApp) openLogs() error {
	dir := desktop.DiscoverLayout().Logs()
	_ = os.MkdirAll(dir, 0o755)
	return exec.Command("explorer.exe", dir).Start()
}

// serviceState reports whether the "VaryaOne" service is registered and running.
//
// It queries the SCM directly asking only for SC_MANAGER_CONNECT +
// SERVICE_QUERY_STATUS, both of which a standard (non-elevated) user holds. The
// kardianos/service Status() call opens the service with SERVICE_START|STOP too,
// which a standard user lacks, so it fails with ERROR_ACCESS_DENIED and the
// panel wrongly shows the service as "not installed" when run without UAC.
func serviceState() (installed, running bool) {
	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return false, false
	}
	defer windows.CloseServiceHandle(scm)

	name, _ := windows.UTF16PtrFromString(desktop.ServiceName)
	h, err := windows.OpenService(scm, name, windows.SERVICE_QUERY_STATUS)
	if err != nil {
		// Only ERROR_SERVICE_DOES_NOT_EXIST means "not installed"; any other
		// error (e.g. access denied) leaves us unsure, so assume installed.
		return err != windows.ERROR_SERVICE_DOES_NOT_EXIST, false
	}
	defer windows.CloseServiceHandle(h)

	var st windows.SERVICE_STATUS
	if err := windows.QueryServiceStatus(h, &st); err != nil {
		return true, false
	}
	return true, st.CurrentState == windows.SERVICE_RUNNING ||
		st.CurrentState == windows.SERVICE_START_PENDING
}

/* ---------------------------------------------------------------- helpers -- */

func normalizeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("boş")
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if u.Host == "" {
		return "", fmt.Errorf("host yok")
	}
	if u.Port() == "" {
		u.Host = u.Host + fmt.Sprintf(":%d", desktop.DefaultHTTPPort)
	}
	u.Path = ""
	u.RawQuery = ""
	return u.String(), nil
}

func ping(base string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/health/ready", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

func configPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.Getenv("APPDATA")
	}
	return filepath.Join(dir, "VaryaOne", "client.json")
}

func loadConfig() clientConfig {
	var c clientConfig
	if b, err := os.ReadFile(configPath()); err == nil {
		_ = json.Unmarshal(b, &c)
	}
	return c
}

func rememberURL(u string) {
	c := loadConfig()
	c.LastURL = u
	hist := []string{u}
	for _, h := range c.History {
		if h != u && len(hist) < 8 {
			hist = append(hist, h)
		}
	}
	c.History = hist
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return
	}
	p := configPath()
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	_ = os.WriteFile(p, b, 0o644)
}

func siblingExe(name string) string {
	self, err := os.Executable()
	if err != nil {
		return name
	}
	return filepath.Join(filepath.Dir(self), name)
}

// runElevated launches exe with args through PowerShell's Start-Process/runas,
// raises a single UAC prompt, waits for completion and propagates the real exit
// code. ShellExecute alone returns as soon as UAC launches the helper, which made
// the panel claim success even when service install/start failed afterwards.
func runElevated(exe string, args ...string) error {
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	quotedArgs := make([]string, 0, len(args))
	for _, arg := range args {
		quotedArgs = append(quotedArgs, quote(arg))
	}
	script := "$p = Start-Process -FilePath " + quote(exe) +
		" -ArgumentList @(" + strings.Join(quotedArgs, ",") + ")" +
		" -Verb RunAs -WindowStyle Hidden -Wait -PassThru -ErrorAction Stop; exit $p.ExitCode"
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail != "" {
			return fmt.Errorf("yönetici komutu başarısız: %s", detail)
		}
		return fmt.Errorf("yönetici komutu başarısız: %w", err)
	}
	return nil
}

func popup(msg string) {
	t, _ := windows.UTF16PtrFromString(msg)
	c, _ := windows.UTF16PtrFromString("Varya One")
	_, _ = windows.MessageBox(0, t, c, windows.MB_OK|windows.MB_ICONERROR)
}
