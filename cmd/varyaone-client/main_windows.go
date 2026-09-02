//go:build windows

// Command varyaone-client is the Varya One desktop app: a WebView2 window that
// discovers a Varya One server on the LAN (or takes a typed address), remembers
// the last working one, and — launched with --panel — shows a small control
// panel for the local service (status, network mode, restart, logs).
package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
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

	app := &clientApp{view: view, panelToken: newCapability(), clientToken: newCapability()}
	app.bind()

	if panel {
		view.SetHtml(app.panelDocument())
		setupTray(uintptr(view.Window()), hidden)
	} else {
		view.SetHtml(app.clientDocument())
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
	ServiceStatus    string   `json:"serviceStatus"`
	ServiceError     string   `json:"serviceError,omitempty"`
	Mode             string   `json:"mode"`
	URLs             []string `json:"urls"`
	Version          string   `json:"version"`
}

type clientApp struct {
	view        webview2.WebView
	mu          sync.Mutex
	current     string
	panelToken  string
	clientToken string
}

func (a *clientApp) bind() {
	_ = a.view.Bind("hostDiscover", a.discover)
	_ = a.view.Bind("hostSaved", a.saved)
	_ = a.view.Bind("hostConnect", a.connectAuthorized)
	_ = a.view.Bind("hostOpenServer", a.openLocalServer)
	_ = a.view.Bind("hostPanelConnect", a.panelConnect)
	_ = a.view.Bind("hostOpenPanel", a.openPanel)
	_ = a.view.Bind("hostPanelState", a.panelState)
	_ = a.view.Bind("hostApplyMode", a.applyMode)
	_ = a.view.Bind("hostRestart", a.restartService)
	_ = a.view.Bind("hostRepair", a.repairService)
	_ = a.view.Bind("hostOpenLogs", a.openLogs)
}

func newCapability() string {
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		panic(fmt.Sprintf("generate panel capability: %v", err))
	}
	return hex.EncodeToString(token[:])
}

func (a *clientApp) authorizePanel(token string) error {
	if len(token) != len(a.panelToken) || subtle.ConstantTimeCompare([]byte(token), []byte(a.panelToken)) != 1 {
		return errors.New("panel authorization failed")
	}
	return nil
}

func (a *clientApp) panelDocument() string {
	return strings.Replace(panelHTML, "__VARYA_PANEL_TOKEN__", a.panelToken, 1)
}

func (a *clientApp) authorizeClient(token string) error {
	if len(token) != len(a.clientToken) || subtle.ConstantTimeCompare([]byte(token), []byte(a.clientToken)) != 1 {
		return errors.New("client authorization failed")
	}
	return nil
}

func (a *clientApp) clientDocument() string {
	return strings.Replace(connectHTML, "__VARYA_CLIENT_TOKEN__", a.clientToken, 1)
}

// discover browses mDNS for ~2.5s and returns the servers it saw.
func (a *clientApp) discover(token string) ([]serverInfo, error) {
	if err := a.authorizeClient(token); err != nil {
		return nil, err
	}
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

func (a *clientApp) saved(token string) (clientConfig, error) {
	if err := a.authorizeClient(token); err != nil {
		return clientConfig{}, err
	}
	return loadConfig(), nil
}

func (a *clientApp) connectAuthorized(token, raw string) error {
	if err := a.authorizeClient(token); err != nil {
		return err
	}
	return a.connect(raw)
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

func (a *clientApp) openLocalServer(token string) error {
	if err := a.authorizePanel(token); err != nil {
		return err
	}
	return a.connect(fmt.Sprintf("http://127.0.0.1:%d", desktop.HTTPPort()))
}

func (a *clientApp) panelConnect(token, raw string) error {
	if err := a.authorizePanel(token); err != nil {
		return err
	}
	return a.connect(raw)
}

func (a *clientApp) openPanel() error {
	a.view.Dispatch(func() {
		a.view.SetTitle("Varya Kontrol Paneli")
		a.view.SetHtml(a.panelDocument())
	})
	return nil
}

func (a *clientApp) panelState(token string) (panelState, error) {
	if err := a.authorizePanel(token); err != nil {
		return panelState{}, err
	}
	layout := desktop.DiscoverLayout()
	service := queryServiceState()
	mode := string(layout.NetworkMode())

	port := desktop.HTTPPort()
	urls := []string{fmt.Sprintf("http://127.0.0.1:%d", port)}
	if mode == string(desktop.NetLAN) {
		urls = append(urls, desktop.LANURLs(port)...)
	}
	serviceError := ""
	if service.err != nil {
		serviceError = service.err.Error()
	}
	return panelState{
		ServiceInstalled: service.installed,
		Running:          service.running,
		Serving:          ping(fmt.Sprintf("http://127.0.0.1:%d", port)) == nil,
		ServiceStatus:    service.status,
		ServiceError:     serviceError,
		Mode:             mode,
		URLs:             urls,
		Version:          version,
	}, nil
}

func (a *clientApp) applyMode(token, mode string) error {
	if err := a.authorizePanel(token); err != nil {
		return err
	}
	if mode != string(desktop.NetLocal) && mode != string(desktop.NetLAN) {
		return fmt.Errorf("geçersiz mod")
	}
	wasRunning := queryServiceState().running
	if err := runElevated(siblingExe("varyaone.exe"), "netmode", mode); err != nil {
		return err
	}
	if !wasRunning {
		return nil
	}
	return a.waitUntilReady(restartReadyWait)
}

func (a *clientApp) restartService(token string) error {
	if err := a.authorizePanel(token); err != nil {
		return err
	}
	service := queryServiceState()
	if service.err != nil {
		return fmt.Errorf("Windows servis durumu okunamadı: %w", service.err)
	}
	action := "restart"
	switch {
	case !service.installed:
		action = "ensure"
	case service.status == "start_pending":
		return a.waitUntilReady(coldReadyWait)
	case service.status == "stop_pending":
		if err := waitUntilServiceStopped(30 * time.Second); err != nil {
			return err
		}
		action = "start"
	case service.status == "stopped":
		action = "start"
	}
	if err := runElevated(siblingExe("varyaone.exe"), "service", action); err != nil {
		return err
	}
	// "ensure" installs a never-yet-started service, so it can be a cold boot.
	if action == "ensure" {
		return a.waitUntilReady(coldReadyWait)
	}
	return a.waitUntilReady(restartReadyWait)
}

func waitUntilServiceStopped(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		state := queryServiceState()
		if state.err != nil {
			return fmt.Errorf("servis durumu okunamadı: %w", state.err)
		}
		if state.status == "stopped" || !state.installed {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("servis 30 saniye içinde durmadı; Servisi onar seçeneğini kullanın")
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func (a *clientApp) repairService(token string) error {
	if err := a.authorizePanel(token); err != nil {
		return err
	}
	if err := runElevated(siblingExe("varyaone.exe"), "service", "repair"); err != nil {
		return err
	}
	return a.waitUntilReady(coldReadyWait)
}

func (a *clientApp) openLogs(token string) error {
	if err := a.authorizePanel(token); err != nil {
		return err
	}
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
type windowsServiceState struct {
	installed bool
	running   bool
	status    string
	err       error
}

func queryServiceState() windowsServiceState {
	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return windowsServiceState{status: "unknown", err: err}
	}
	defer windows.CloseServiceHandle(scm)

	name, _ := windows.UTF16PtrFromString(desktop.ServiceName)
	h, err := windows.OpenService(scm, name, windows.SERVICE_QUERY_STATUS)
	if err != nil {
		if err == windows.ERROR_SERVICE_DOES_NOT_EXIST {
			return windowsServiceState{status: "not_installed"}
		}
		return windowsServiceState{installed: true, status: "unknown", err: err}
	}
	defer windows.CloseServiceHandle(h)

	var st windows.SERVICE_STATUS
	if err := windows.QueryServiceStatus(h, &st); err != nil {
		return windowsServiceState{installed: true, status: "unknown", err: err}
	}
	status := map[uint32]string{
		windows.SERVICE_STOPPED:          "stopped",
		windows.SERVICE_START_PENDING:    "start_pending",
		windows.SERVICE_STOP_PENDING:     "stop_pending",
		windows.SERVICE_RUNNING:          "running",
		windows.SERVICE_CONTINUE_PENDING: "continue_pending",
		windows.SERVICE_PAUSE_PENDING:    "pause_pending",
		windows.SERVICE_PAUSED:           "paused",
	}[st.CurrentState]
	if status == "" {
		status = fmt.Sprintf("unknown_%d", st.CurrentState)
	}
	return windowsServiceState{
		installed: true,
		running:   st.CurrentState == windows.SERVICE_RUNNING || st.CurrentState == windows.SERVICE_START_PENDING,
		status:    status,
	}
}

// restartReadyWait covers a restart of an already-initialized install.
// coldReadyWait additionally covers a first boot: initdb plus the squashed
// baseline migration, with Defender scanning every file the installer wrote.
const (
	restartReadyWait = 2 * time.Minute
	coldReadyWait    = 6 * time.Minute
)

func (a *clientApp) waitUntilReady(timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		if ping(fmt.Sprintf("http://127.0.0.1:%d", desktop.HTTPPort())) == nil {
			return nil
		}
		state := queryServiceState()
		if state.err != nil {
			return fmt.Errorf("servis durumu okunamadı: %w", state.err)
		}
		if state.installed && state.status == "stopped" {
			return desktop.StackFailure("servis başladıktan sonra durdu")
		}
		select {
		case <-ctx.Done():
			return desktop.StackFailure(fmt.Sprintf("sunucu %.0f dakika içinde hazır olmadı", timeout.Minutes()))
		case <-ticker.C:
		}
	}
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
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("yalnızca http veya https desteklenir")
	}
	if u.User != nil {
		return "", fmt.Errorf("adres içinde kullanıcı bilgisi desteklenmez")
	}
	if u.Port() == "" {
		u.Host = net.JoinHostPort(u.Hostname(), fmt.Sprintf("%d", desktop.HTTPPort()))
	}
	u.Path = ""
	u.RawPath = ""
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	u.RawFragment = ""
	return u.String(), nil
}

func ping(base string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/health/ready", nil)
	if err != nil {
		return err
	}
	client := &http.Client{
		Timeout: 3 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var health struct {
		Service string `json:"service"`
		Status  string `json:"status"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<10)).Decode(&health); err != nil {
		return fmt.Errorf("geçersiz sağlık yanıtı: %w", err)
	}
	if health.Service != "api" || health.Status != "ok" {
		return fmt.Errorf("bu adres bir Varya One sunucusu değil")
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
	started := time.Now().UTC()
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
		action := strings.Join(args, " ")
		if result, readErr := desktop.LastControlResult(); readErr == nil &&
			result.Action == action && !result.OK && result.Error != "" &&
			!result.At.Before(started.Add(-time.Second)) {
			return errors.New(result.Error)
		}
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
