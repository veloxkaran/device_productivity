package monitor

import (
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

var (
	procGetForegroundWindow      = modUser32Win.NewProc("GetForegroundWindow")
	procGetWindowThreadProcessId = modUser32Win.NewProc("GetWindowThreadProcessId")
	procOpenProcess              = modKernel32Win.NewProc("OpenProcess")
	procQueryFullProcessImageW   = modKernel32Win.NewProc("QueryFullProcessImageNameW")
	procCloseHandle              = modKernel32Win.NewProc("CloseHandle")
)

func getActiveApp() string {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return ""
	}
	var pid uint32
	procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid == 0 {
		return ""
	}
	h, _, _ := procOpenProcess.Call(0x1000, 0, uintptr(pid))
	if h == 0 {
		return ""
	}
	defer procCloseHandle.Call(h)
	buf := make([]uint16, 260)
	size := uint32(len(buf))
	r, _, _ := procQueryFullProcessImageW.Call(h, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if r == 0 {
		return ""
	}
	return strings.TrimSuffix(filepath.Base(syscall.UTF16ToString(buf[:size])), ".exe")
}
