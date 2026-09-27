//go:build windows

package main

import (
	"runtime"
	"syscall"
	"unsafe"
)

// Windows system-tray implementation (pure Go, no cgo). Gives the app a
// persistent taskbar/tray presence like the macOS menu-bar item: left-click
// opens the tracker window, right-click shows Open / Quit. Managed (covert)
// devices create no tray icon and stay hidden.

const nativeWindow = false

var (
	moduser32   = syscall.NewLazyDLL("user32.dll")
	modshell32  = syscall.NewLazyDLL("shell32.dll")
	modkernel32 = syscall.NewLazyDLL("kernel32.dll")

	pRegisterClassEx  = moduser32.NewProc("RegisterClassExW")
	pCreateWindowEx   = moduser32.NewProc("CreateWindowExW")
	pDefWindowProc    = moduser32.NewProc("DefWindowProcW")
	pGetMessage       = moduser32.NewProc("GetMessageW")
	pTranslateMessage = moduser32.NewProc("TranslateMessage")
	pDispatchMessage  = moduser32.NewProc("DispatchMessageW")
	pPostQuitMessage  = moduser32.NewProc("PostQuitMessage")
	pLoadIcon         = moduser32.NewProc("LoadIconW")
	pCreatePopupMenu  = moduser32.NewProc("CreatePopupMenu")
	pAppendMenu       = moduser32.NewProc("AppendMenuW")
	pTrackPopupMenu   = moduser32.NewProc("TrackPopupMenu")
	pDestroyMenu      = moduser32.NewProc("DestroyMenu")
	pGetCursorPos     = moduser32.NewProc("GetCursorPos")
	pSetForegroundWin = moduser32.NewProc("SetForegroundWindow")
	pShellNotifyIcon  = modshell32.NewProc("Shell_NotifyIconW")
	pGetModuleHandle  = modkernel32.NewProc("GetModuleHandleW")
)

const (
	wsOverlapped = 0x00000000
	cwUseDefault = 0x80000000

	wmDestroy   = 0x0002
	wmCommand   = 0x0111
	wmApp       = 0x8000
	trayMessage = wmApp + 1
	wmLButtonUp = 0x0202
	wmRButtonUp = 0x0205

	idiApplication = 32512

	nimAdd     = 0x00000000
	nimDelete  = 0x00000002
	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004

	mfString     = 0x00000000
	mfSeparator  = 0x00000800
	tpmRightBtn  = 0x0002
	tpmReturnCmd = 0x0100

	idmOpen = 1
	idmQuit = 2
)

type wndClassEx struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       uintptr
}

type point struct{ X, Y int32 }

type msg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      point
}

type notifyIconData struct {
	cbSize            uint32
	hWnd              uintptr
	uID               uint32
	uFlags            uint32
	uCallbackMessage  uint32
	hIcon             uintptr
	szTip             [128]uint16
	dwState           uint32
	dwStateMask       uint32
	szInfo            [256]uint16
	uVersionOrTimeout uint32
	szInfoTitle       [64]uint16
	dwInfoFlags       uint32
	guidItem          [16]byte
	hBalloonIcon      uintptr
}

var (
	uiOpen     func()
	uiQuit     func()
	trayNID    notifyIconData
	trayHwnd   uintptr
	trayActive bool
)

func init() { runtime.LockOSThread() }

func hideMenuBar() {
	if trayActive {
		pShellNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(&trayNID)))
		trayActive = false
	}
}

func wndProc(hwnd, message, wParam, lParam uintptr) uintptr {
	switch message {
	case trayMessage:
		switch lParam & 0xffff { // LOWORD(lParam) = mouse event
		case wmLButtonUp:
			if uiOpen != nil {
				go uiOpen()
			}
		case wmRButtonUp:
			showTrayMenu(hwnd)
		}
	case wmCommand:
		switch wParam & 0xffff {
		case idmOpen:
			if uiOpen != nil {
				go uiOpen()
			}
		case idmQuit:
			if uiQuit != nil {
				go uiQuit()
			}
		}
	case wmDestroy:
		pPostQuitMessage.Call(0)
	}
	r, _, _ := pDefWindowProc.Call(hwnd, message, wParam, lParam)
	return r
}

func showTrayMenu(hwnd uintptr) {
	menu, _, _ := pCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer pDestroyMenu.Call(menu)
	pAppendMenu.Call(menu, mfString, idmOpen, uintptr(unsafe.Pointer(u16("Open Hajir Tracker"))))
	pAppendMenu.Call(menu, mfSeparator, 0, 0)
	pAppendMenu.Call(menu, mfString, idmQuit, uintptr(unsafe.Pointer(u16("Quit Hajir Tracker"))))

	var pt point
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	pSetForegroundWin.Call(hwnd)
	cmd, _, _ := pTrackPopupMenu.Call(menu, tpmRightBtn|tpmReturnCmd, uintptr(pt.X), uintptr(pt.Y), 0, hwnd, 0)
	switch cmd {
	case idmOpen:
		if uiOpen != nil {
			go uiOpen()
		}
	case idmQuit:
		if uiQuit != nil {
			go uiQuit()
		}
	}
}

func u16(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

func runUI(open func(), quit func(), managed bool) {
	uiOpen, uiQuit = open, quit

	// Covert/managed device: no tray icon, just keep the process alive.
	if managed {
		select {}
	}

	hInstance, _, _ := pGetModuleHandle.Call(0)
	className := u16("HajirTrackerTray")

	wc := wndClassEx{
		lpfnWndProc:   syscall.NewCallback(wndProc),
		hInstance:     hInstance,
		lpszClassName: className,
	}
	wc.cbSize = uint32(unsafe.Sizeof(wc))
	pRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc)))

	// Hidden message-only-ish window to receive tray callbacks.
	hwnd, _, _ := pCreateWindowEx.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(u16("Hajir Tracker"))),
		wsOverlapped,
		cwUseDefault, cwUseDefault, cwUseDefault, cwUseDefault,
		0, 0, hInstance, 0,
	)
	if hwnd == 0 {
		select {} // couldn't create window; stay alive so tracking continues
	}
	trayHwnd = hwnd

	icon, _, _ := pLoadIcon.Call(hInstance, 1) // embedded Hajir icon (rsrc_windows_amd64.syso)
	if icon == 0 {
		icon, _, _ = pLoadIcon.Call(0, idiApplication)
	}

	trayNID = notifyIconData{
		hWnd:             hwnd,
		uID:              1,
		uFlags:           nifMessage | nifIcon | nifTip,
		uCallbackMessage: trayMessage,
		hIcon:            icon,
	}
	trayNID.cbSize = uint32(unsafe.Sizeof(trayNID))
	copy(trayNID.szTip[:], syscall.StringToUTF16("Hajir Tracker"))
	pShellNotifyIcon.Call(nimAdd, uintptr(unsafe.Pointer(&trayNID)))
	trayActive = true

	var m msg
	for {
		r, _, _ := pGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 { // 0 = WM_QUIT, -1 = error
			break
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
	hideMenuBar()
}
