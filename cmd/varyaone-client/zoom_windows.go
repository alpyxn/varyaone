//go:build windows

package main

import (
	"math"
	"unsafe"

	"github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
)

// The display scale of the desktop window is WebView2's own zoom factor, not a
// CSS zoom on the served page: it scales everything (the connection screen,
// the ⚙ button, popovers measured in CSS pixels) the same way Ctrl + wheel
// does, and it is in place before the first paint.
//
// It is a preference of this Windows user on this PC, so it lives in
// %AppData%\VaryaOne\client.json next to the remembered server address — never
// on the server, where every PC connecting to it would share one value.
//
// go-webview2 does not wrap the zoom members, so they are called at the COM
// level. ICoreWebView2Controller vtable: IUnknown 3, get/put_IsVisible,
// get/put_Bounds, get_ZoomFactor, put_ZoomFactor, add_ZoomFactorChanged.
const (
	slotControllerGetZoomFactor  = 7
	slotControllerPutZoomFactor  = 8
	slotControllerAddZoomChanged = 9

	zoomMin, zoomMax, zoomDefault = 0.5, 2.0, 1.0
)

type zoomState struct {
	Factor  float64 `json:"factor"`
	Min     float64 `json:"min"`
	Max     float64 `json:"max"`
	Default float64 `json:"default"`
}

// zoomMemory is package state because native code holds the handler for the
// life of the process; keeping it reachable here keeps the GC off it.
var zoomMemory struct {
	view    webview2.WebView
	handler *comHandler
}

func clampZoom(f float64) float64 {
	if math.IsNaN(f) || math.IsInf(f, 0) || f <= 0 {
		return zoomDefault
	}
	return math.Min(zoomMax, math.Max(zoomMin, math.Round(f*100)/100))
}

func controllerOf(view webview2.WebView) unsafe.Pointer {
	chromium := chromiumOf(view)
	if chromium == nil {
		return nil
	}
	controller := chromium.GetController()
	if controller == nil {
		return nil
	}
	return unsafe.Pointer(controller)
}

// applyZoom sets the window's zoom factor. The double travels in the integer
// slot; the Windows amd64 syscall path mirrors the first four arguments into
// XMM0-3, which is where the callee reads a floating-point parameter.
func applyZoom(view webview2.WebView, factor float64) bool {
	controller := controllerOf(view)
	if controller == nil {
		return false
	}
	return !failed(comCall(controller, slotControllerPutZoomFactor, uintptr(math.Float64bits(factor))))
}

func currentZoom(view webview2.WebView) (float64, bool) {
	controller := controllerOf(view)
	if controller == nil {
		return 0, false
	}
	var factor float64
	if failed(comCall(controller, slotControllerGetZoomFactor, uintptr(unsafe.Pointer(&factor)))) {
		return 0, false
	}
	return factor, true
}

// installZoomMemory starts the window at the saved scale and saves every later
// change, including Ctrl + wheel and Ctrl +/−, so the next launch opens the way
// the user left it. On a runtime where the calls fail the window simply stays
// at 100%.
func installZoomMemory(view webview2.WebView) {
	if saved := loadConfig().Zoom; saved > 0 {
		applyZoom(view, clampZoom(saved))
	}
	controller := controllerOf(view)
	if controller == nil {
		return
	}
	zoomMemory.view = view
	zoomMemory.handler = &comHandler{vtbl: &comHandlerVtbl{
		QueryInterface: windows.NewCallback(func(this, _ uintptr, out *uintptr) uintptr {
			*out = this
			return 0
		}),
		AddRef:  windows.NewCallback(func(uintptr) uintptr { return 1 }),
		Release: windows.NewCallback(func(uintptr) uintptr { return 1 }),
		Invoke:  windows.NewCallback(onZoomFactorChanged),
	}}
	var token int64
	comCall(controller, slotControllerAddZoomChanged,
		uintptr(unsafe.Pointer(zoomMemory.handler)), uintptr(unsafe.Pointer(&token)))
}

func onZoomFactorChanged(_, _, _ uintptr) uintptr {
	if factor, ok := currentZoom(zoomMemory.view); ok {
		saveZoom(factor)
	}
	return 0
}

func saveZoom(factor float64) {
	factor = clampZoom(factor)
	updateConfig(func(c *clientConfig) {
		if factor == zoomDefault {
			c.Zoom = 0 // omitted: a later default change reaches users who never chose one
			return
		}
		c.Zoom = factor
	})
}

// getZoom and setZoom are bound for the served page's Program ayarları screen.
// They carry no capability: the served page must reach them, and the worst a
// page can do is change this window's scale within the clamp.
func (a *clientApp) getZoom() zoomState {
	factor, ok := currentZoom(a.view)
	if !ok {
		factor = zoomDefault
	}
	return zoomState{Factor: clampZoom(factor), Min: zoomMin, Max: zoomMax, Default: zoomDefault}
}

func (a *clientApp) setZoom(factor float64) zoomState {
	factor = clampZoom(factor)
	if applyZoom(a.view, factor) {
		saveZoom(factor)
	}
	return a.getZoom()
}
