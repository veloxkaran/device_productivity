package monitor

import "syscall"

// Shared lazy DLL handles used by idle_windows.go and input_windows.go.
var (
	modUser32Win   = syscall.NewLazyDLL("user32.dll")
	modKernel32Win = syscall.NewLazyDLL("kernel32.dll")
)
