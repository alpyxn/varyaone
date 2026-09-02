//go:build windows

package main

// System-tray support for the control panel (--panel). Closing the window hides
// it to the notification area instead of quitting; a left click restores it and
// a right click shows an "Aç / Çıkış" menu. Launched with --tray (used by the
// login autostart entry) it starts hidden.

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procSetWindowLongPtr = user32.NewProc("SetWindowLongPtrW")
	procCallWindowProc   = user32.NewProc("CallWindowProcW")
	procDefWindowProc    = user32.NewProc("DefWindowProcW")
	procShowWindow       = user32.NewProc("ShowWindow")
	procSetForeground    = user32.NewProc("SetForegroundWindow")
	procCreatePopupMenu  = user32.NewProc("CreatePopupMenu")
	procAppendMenu       = user32.NewProc("AppendMenuW")
	procTrackPopupMenu   = user32.NewProc("TrackPopupMenu")
	procDestroyMenu      = user32.NewProc("DestroyMenu")
	procGetCursorPos     = user32.NewProc("GetCursorPos")
	procPostMessage      = user32.NewProc("PostMessageW")
	procLoadImage        = user32.NewProc("LoadImageW")
	procLoadIcon         = user32.NewProc("LoadIconW")

	procShellNotifyIcon = shell32.NewProc("Shell_NotifyIconW")
	procGetModuleHandle = kernel32.NewProc("GetModuleHandleW")
)

const (
	gwlpWndProc = -4

	wmDestroy    = 0x0002
	wmClose      = 0x0010
	wmCommand    = 0x0111
	wmTrayCB     = 0x0400 + 1 // WM_APP + 1
	wmLButtonUp  = 0x0202
	wmLButtonDbl = 0x0203
	wmRButtonUp  = 0x0205

	swHide    = 0
	swShow    = 5
	swRestore = 9

	nimAdd    = 0x0000
	nimModify = 0x0001
	nimDelete = 0x0002

	nifMessage = 0x0001
	nifIcon    = 0x0002
	nifTip     = 0x0004

	imageIcon      = 1
	lrLoadFromFile = 0x0010
	lrDefaultSize  = 0x0040

	mfString       = 0x0000
	mfSeparator    = 0x0800
	tpmRightButton = 0x0002

	menuOpen = 1
	menuExit = 2
)

type notifyIconData struct {
	CbSize           uint32
	HWnd             uintptr
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            uintptr
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UVersion         uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         windows.GUID
	HBalloonIcon     uintptr
}

type point struct{ X, Y int32 }

var (
	trayHWND    uintptr
	origWndProc uintptr
	trayNID     notifyIconData
)

// setupTray installs the tray icon and window subclass. startHidden hides the
// window immediately (login autostart). Safe to call once, after the window
// exists and before view.Run().
func setupTray(hwnd uintptr, startHidden bool) {
	trayHWND = hwnd

	trayNID = notifyIconData{
		HWnd:             hwnd,
		UID:              1,
		UFlags:           nifMessage | nifIcon | nifTip,
		UCallbackMessage: wmTrayCB,
		HIcon:            loadTrayIcon(),
	}
	trayNID.CbSize = uint32(unsafe.Sizeof(trayNID))
	copy(trayNID.SzTip[:], windows.StringToUTF16("Varya Kontrol Paneli"))
	added, _, _ := procShellNotifyIcon.Call(nimAdd, uintptr(unsafe.Pointer(&trayNID)))
	if added == 0 {
		// Keep the ordinary window usable if Explorer rejected the tray icon.
		return
	}

	cb := windows.NewCallback(trayWndProc)
	nIndex := int32(gwlpWndProc)
	r, _, _ := procSetWindowLongPtr.Call(hwnd, uintptr(nIndex), cb)
	if r == 0 {
		removeTrayIcon()
		return
	}
	origWndProc = r

	if startHidden {
		procShowWindow.Call(hwnd, swHide)
	}
}

func loadTrayIcon() uintptr {
	if p, err := windows.UTF16PtrFromString(siblingExe("panelicon.ico")); err == nil {
		h, _, _ := procLoadImage.Call(0, uintptr(unsafe.Pointer(p)), imageIcon, 0, 0, lrLoadFromFile|lrDefaultSize)
		if h != 0 {
			return h
		}
	}
	// Fall back to the stock application icon (IDI_APPLICATION = 32512).
	hInst, _, _ := procGetModuleHandle.Call(0)
	h, _, _ := procLoadIcon.Call(hInst, 32512)
	if h == 0 {
		h, _, _ = procLoadIcon.Call(0, 32512)
	}
	return h
}

func showTrayWindow() {
	procShowWindow.Call(trayHWND, swShow)
	procShowWindow.Call(trayHWND, swRestore)
	procSetForeground.Call(trayHWND)
}

func removeTrayIcon() {
	procShellNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(&trayNID)))
}

func trayWndProc(hwnd, msg, wparam, lparam uintptr) uintptr {
	switch msg {
	case wmTrayCB:
		switch lparam {
		case wmLButtonUp, wmLButtonDbl:
			showTrayWindow()
		case wmRButtonUp:
			showTrayMenu(hwnd)
		}
		return 0

	case wmCommand:
		switch wparam & 0xffff {
		case menuOpen:
			showTrayWindow()
			return 0
		case menuExit:
			removeTrayIcon()
			procPostMessage.Call(hwnd, wmClose, 1, 0) // 1 => real close, see below
			return 0
		}

	case wmClose:
		// A plain close hides to tray. wparam==1 is our "really quit" marker.
		if wparam != 1 {
			procShowWindow.Call(hwnd, swHide)
			return 0
		}
		removeTrayIcon()

	case wmDestroy:
		removeTrayIcon()
	}

	r, _, _ := procCallWindowProc.Call(origWndProc, hwnd, msg, wparam, lparam)
	return r
}

func showTrayMenu(hwnd uintptr) {
	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)

	appendItem := func(id uintptr, text string) {
		t, _ := windows.UTF16PtrFromString(text)
		procAppendMenu.Call(menu, mfString, id, uintptr(unsafe.Pointer(t)))
	}
	appendItem(menuOpen, "Aç")
	procAppendMenu.Call(menu, mfSeparator, 0, 0)
	appendItem(menuExit, "Çıkış")

	var pt point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	procSetForeground.Call(hwnd)
	procTrackPopupMenu.Call(menu, tpmRightButton, uintptr(pt.X), uintptr(pt.Y), 0, hwnd, 0)
	procPostMessage.Call(hwnd, 0, 0, 0) // flush, per MS docs
}
