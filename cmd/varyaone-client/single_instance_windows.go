//go:build windows

package main

// Single-instance guard. Both the desktop app window and the control panel are
// the same executable, and the panel is additionally launched from the login
// autostart entry, the Start-menu shortcut and the ⚙ button inside the served
// app — without a guard those all stack up as separate processes, each adding
// its own notification-area icon.
//
// A named mutex per mode marks the running instance; a second launch broadcasts
// a registered window message that tells the first one to show itself, then
// exits. The mutex lives in the Local\ namespace, so a second logged-in Windows
// user still gets their own panel.

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	instanceModePanel = "panel"
	instanceModeApp   = "app"

	hwndBroadcast = 0xffff
)

var procRegisterWindowMessage = user32.NewProc("RegisterWindowMessageW")

// instanceHandles are kept for the process lifetime: closing the mutex handle
// would release the claim while this instance is still running.
var instanceMutex windows.Handle

// showInstanceMessage returns the system-wide message id used to raise the
// running instance of mode. RegisterWindowMessage returns the same value in
// every process for the same string.
func showInstanceMessage(mode string) uintptr {
	name, err := windows.UTF16PtrFromString("VaryaOne.Show." + mode)
	if err != nil {
		return 0
	}
	msg, _, _ := procRegisterWindowMessage.Call(uintptr(unsafe.Pointer(name)))
	return msg
}

// claimSingleInstance reports whether this process may continue. When another
// instance of the same mode already holds the claim it is asked to show its
// window and false is returned, so the caller should exit without creating a
// second window or tray icon.
func claimSingleInstance(mode string) bool {
	name, err := windows.UTF16PtrFromString("Local\\VaryaOne.Client." + mode)
	if err != nil {
		return true // Never block startup over a name we failed to build.
	}
	handle, err := windows.CreateMutex(nil, false, name)
	if err == windows.ERROR_ALREADY_EXISTS {
		if handle != 0 {
			windows.CloseHandle(handle)
		}
		raiseRunningInstance(mode)
		return false
	}
	if err != nil {
		return true
	}
	instanceMutex = handle
	return true
}

func raiseRunningInstance(mode string) {
	msg := showInstanceMessage(mode)
	if msg == 0 {
		return
	}
	// Broadcast rather than hunting for the window: the WebView2 host window has
	// no stable class name to search for, and only our own subclass reacts to
	// this id.
	procPostMessage.Call(hwndBroadcast, msg, 0, 0)
}
