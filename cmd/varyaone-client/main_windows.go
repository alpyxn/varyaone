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
	"time"

	"github.com/alpyxn/varyaone/internal/platform/desktop"
	"github.com/grandcat/zeroconf"
	"github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
)

func main() {
	runtime.LockOSThread()

	panel := false
	for _, a := range os.Args[1:] {
		if a == "--panel" {
			panel = true
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
		return fmt.Errorf("henüz bir sunucuya bağlanılmadı")
	}
	a.view.Dispatch(func() { a.view.Navigate(cur) })
	return nil
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
	installed, running := desktop.ServiceState()
	mode := string(layout.NetworkMode())

	urls := []string{fmt.Sprintf("http://127.0.0.1:%d", desktop.DefaultHTTPPort)}
	if mode == string(desktop.NetLAN) {
		urls = append(urls, desktop.LANURLs(desktop.DefaultHTTPPort)...)
	}
	return panelState{
		ServiceInstalled: installed,
		Running:          running,
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
	return runElevated(siblingExe("varyaone.exe"), "service", "restart")
}

func (a *clientApp) openLogs() error {
	dir := desktop.DiscoverLayout().Logs()
	_ = os.MkdirAll(dir, 0o755)
	return exec.Command("explorer.exe", dir).Start()
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

// runElevated launches exe with args through ShellExecute "runas", raising a
// single UAC prompt. A cancelled prompt surfaces as an error.
func runElevated(exe string, args ...string) error {
	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(exe)
	argp, _ := windows.UTF16PtrFromString(strings.Join(args, " "))
	if err := windows.ShellExecute(0, verb, file, argp, nil, windows.SW_HIDE); err != nil {
		return fmt.Errorf("yönetici olarak çalıştırılamadı: %w", err)
	}
	return nil
}

func popup(msg string) {
	t, _ := windows.UTF16PtrFromString(msg)
	c, _ := windows.UTF16PtrFromString("Varya One")
	_, _ = windows.MessageBox(0, t, c, windows.MB_OK|windows.MB_ICONERROR)
}
