//go:build windows

package main

import (
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"github.com/jchv/go-webview2"
	"github.com/jchv/go-webview2/pkg/edge"
	"golang.org/x/sys/windows"
)

// WebView2 on its own drops every download into the Downloads folder and shows
// the Edge download bubble — the user is never asked where a backup goes. The
// DownloadStarting event (ICoreWebView2_4) lets the host choose the path, but
// go-webview2 does not wrap it, so it is wired here at the COM level.
//
// Vtable slots, counted from WebView2.h:
//
//	ICoreWebView2_4: IUnknown 3 + ICoreWebView2 58 + _2 7 + _3 5,
//	                 then add_FrameCreated, remove_FrameCreated, add_DownloadStarting
//	ICoreWebView2DownloadStartingEventArgs: IUnknown 3, then get_DownloadOperation,
//	                 get_Cancel, put_Cancel, get_ResultFilePath, put_ResultFilePath,
//	                 get_Handled, put_Handled, GetDeferral
const (
	slotRelease = 2

	slotAddDownloadStarting = 75

	slotArgsPutCancel         = 5
	slotArgsGetResultFilePath = 6
	slotArgsPutResultFilePath = 7
	slotArgsGetDeferral       = 10

	slotDeferralComplete = 3
)

var iidICoreWebView2_4 = windows.GUID{
	Data1: 0x20d02d59, Data2: 0x6df2, Data3: 0x42dc,
	Data4: [8]byte{0xbd, 0x06, 0xf9, 0x8a, 0x69, 0x4b, 0x13, 0x02},
}

type comHandlerVtbl struct {
	QueryInterface uintptr
	AddRef         uintptr
	Release        uintptr
	Invoke         uintptr
}

type comHandler struct {
	vtbl *comHandlerVtbl
}

// downloadPrompt is package state because native code holds the handler for
// the life of the process; keeping it reachable here keeps the GC off it.
var downloadPrompt struct {
	view    webview2.WebView
	handler *comHandler
}

// installDownloadPrompt makes every download in this window open the Windows
// "Save as" dialog first. On a WebView2 runtime too old for the event, or if
// the webview internals ever change shape, downloads keep the default
// behaviour rather than failing.
func installDownloadPrompt(view webview2.WebView) {
	chromium := chromiumOf(view)
	if chromium == nil {
		return
	}
	core3 := chromium.GetICoreWebView2_3()
	if core3 == nil {
		return
	}
	var core4 unsafe.Pointer
	if hr := comCall(unsafe.Pointer(core3), 0, uintptr(unsafe.Pointer(&iidICoreWebView2_4)), uintptr(unsafe.Pointer(&core4))); failed(hr) || core4 == nil {
		return
	}

	downloadPrompt.view = view
	downloadPrompt.handler = &comHandler{vtbl: &comHandlerVtbl{
		QueryInterface: windows.NewCallback(func(this, _ uintptr, out *uintptr) uintptr {
			*out = this
			return 0
		}),
		AddRef:  windows.NewCallback(func(uintptr) uintptr { return 1 }),
		Release: windows.NewCallback(func(uintptr) uintptr { return 1 }),
		Invoke:  windows.NewCallback(onDownloadStarting),
	}}
	var token int64
	comCall(core4, slotAddDownloadStarting,
		uintptr(unsafe.Pointer(downloadPrompt.handler)), uintptr(unsafe.Pointer(&token)))
}

// onDownloadStarting takes a deferral and returns at once. The dialog is modal
// and runs its own message loop; opening it inside the event callback would
// re-enter WebView2, which its documentation warns against.
func onDownloadStarting(_, _ uintptr, argsPtr unsafe.Pointer) uintptr {
	var deferral unsafe.Pointer
	if hr := comCall(argsPtr, slotArgsGetDeferral, uintptr(unsafe.Pointer(&deferral))); failed(hr) || deferral == nil {
		return 0
	}
	defaultPath := resultFilePath(argsPtr)
	comCall(argsPtr, 1) // AddRef: args must outlive this callback.

	downloadPrompt.view.Dispatch(func() {
		hwnd := uintptr(downloadPrompt.view.Window())
		if chosen, ok := saveFileDialog(hwnd, defaultPath); ok {
			if p, err := windows.UTF16PtrFromString(chosen); err == nil {
				comCall(argsPtr, slotArgsPutResultFilePath, uintptr(unsafe.Pointer(p)))
			}
		} else {
			comCall(argsPtr, slotArgsPutCancel, 1)
		}
		comCall(deferral, slotDeferralComplete)
		comCall(deferral, slotRelease)
		comCall(argsPtr, slotRelease)
	})
	return 0
}

func resultFilePath(args unsafe.Pointer) string {
	var raw *uint16
	if hr := comCall(args, slotArgsGetResultFilePath, uintptr(unsafe.Pointer(&raw))); failed(hr) || raw == nil {
		return ""
	}
	defer windows.CoTaskMemFree(unsafe.Pointer(raw))
	return windows.UTF16PtrToString(raw)
}

type openFileName struct {
	structSize    uint32
	owner         uintptr
	instance      uintptr
	filter        *uint16
	customFilter  *uint16
	maxCustFilter uint32
	filterIndex   uint32
	file          *uint16
	maxFile       uint32
	fileTitle     *uint16
	maxFileTitle  uint32
	initialDir    *uint16
	title         *uint16
	flags         uint32
	fileOffset    uint16
	fileExtension uint16
	defExt        *uint16
	custData      uintptr
	hook          uintptr
	templateName  *uint16
	reserved      uintptr
	reservedDword uint32
	flagsEx       uint32
}

const (
	ofnOverwritePrompt = 0x00000002
	ofnNoChangeDir     = 0x00000008
	ofnPathMustExist   = 0x00000800
	ofnExplorer        = 0x00080000
)

var procGetSaveFileNameW = windows.NewLazySystemDLL("comdlg32.dll").NewProc("GetSaveFileNameW")

// saveFileDialog shows the native "Save as" dialog, prefilled with the name the
// server suggested. ok is false when the user cancels.
func saveFileDialog(owner uintptr, defaultPath string) (string, bool) {
	name := filepath.Base(defaultPath)
	if defaultPath == "" {
		name = ""
	}
	buf := make([]uint16, 32768)
	copy(buf[:len(buf)-1], utf16.Encode([]rune(name)))

	ext := strings.TrimPrefix(filepath.Ext(name), ".")
	filterText := "Tüm dosyalar (*.*)\x00*.*\x00\x00"
	if ext != "" {
		filterText = strings.ToUpper(ext) + " dosyası (*." + ext + ")\x00*." + ext + "\x00" + filterText
	}
	filter := utf16.Encode([]rune(filterText))

	ofn := openFileName{
		owner:       owner,
		filter:      &filter[0],
		filterIndex: 1,
		file:        &buf[0],
		maxFile:     uint32(len(buf)),
		flags:       ofnOverwritePrompt | ofnNoChangeDir | ofnPathMustExist | ofnExplorer,
	}
	ofn.structSize = uint32(unsafe.Sizeof(ofn))
	if dir := filepath.Dir(defaultPath); defaultPath != "" && dir != "." {
		if p, err := windows.UTF16PtrFromString(dir); err == nil {
			ofn.initialDir = p
		}
	}
	if ext != "" {
		if p, err := windows.UTF16PtrFromString(ext); err == nil {
			ofn.defExt = p
		}
	}
	if r, _, _ := procGetSaveFileNameW.Call(uintptr(unsafe.Pointer(&ofn))); r == 0 {
		return "", false
	}
	return windows.UTF16ToString(buf), true
}

// chromiumOf reaches the *edge.Chromium that go-webview2 keeps in an
// unexported field. The module version is pinned in go.mod; if the field ever
// moves this returns nil and the caller skips the feature.
func chromiumOf(view webview2.WebView) (chromium *edge.Chromium) {
	defer func() {
		if recover() != nil {
			chromium = nil
		}
	}()
	v := reflect.ValueOf(view)
	if v.Kind() != reflect.Pointer || v.Elem().Kind() != reflect.Struct {
		return nil
	}
	field := v.Elem().FieldByName("browser")
	if !field.IsValid() {
		return nil
	}
	field = reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem()
	chromium, _ = field.Interface().(*edge.Chromium)
	return chromium
}

// comCall invokes method slot of a COM object with obj as the implicit this.
func comCall(obj unsafe.Pointer, slot int, args ...uintptr) uintptr {
	vtbl := *(*unsafe.Pointer)(obj)
	fn := *(*uintptr)(unsafe.Add(vtbl, slot*int(unsafe.Sizeof(uintptr(0)))))
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{uintptr(obj)}, args...)...)
	return r
}

func failed(hr uintptr) bool { return int32(hr) < 0 }
