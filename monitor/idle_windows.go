package monitor

import (
	"syscall"
	"unsafe"
)

var (
	modUser32Win     = syscall.NewLazyDLL("user32.dll")
	modKernel32Win   = syscall.NewLazyDLL("kernel32.dll")
	procLastInput    = modUser32Win.NewProc("GetLastInputInfo")
	procGetTickCount = modKernel32Win.NewProc("GetTickCount")
)

type lastInputInfo struct {
	cbSize uint32
	dwTime uint32
}

// getIdleSeconds uses GetLastInputInfo to measure milliseconds since last input.
func getIdleSeconds() int64 {
	var li lastInputInfo
	li.cbSize = uint32(unsafe.Sizeof(li))
	procLastInput.Call(uintptr(unsafe.Pointer(&li)))
	tick, _, _ := procGetTickCount.Call()
	idleMs := int64(uint32(tick)) - int64(li.dwTime)
	if idleMs < 0 {
		idleMs = 0
	}
	return idleMs / 1000
}
