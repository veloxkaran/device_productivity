package monitor

import "unsafe"

var (
	procGetCursorPos = modUser32Win.NewProc("GetCursorPos")
	procGetAsyncKey  = modUser32Win.NewProc("GetAsyncKeyState")
)

type winPoint struct{ X, Y int32 }

const (
	vkLButton = 0x01
	vkRButton = 0x02
)

var prevL, prevR bool

// getMousePos returns the current cursor position via Win32 GetCursorPos.
func getMousePos() (float64, float64) {
	var p winPoint
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	return float64(p.X), float64(p.Y)
}

// sampleClicks detects left/right mouse button press transitions via GetAsyncKeyState.
func sampleClicks() int64 {
	var clicks int64
	l, _, _ := procGetAsyncKey.Call(vkLButton)
	r, _, _ := procGetAsyncKey.Call(vkRButton)
	lDown := l&0x8000 != 0
	rDown := r&0x8000 != 0
	if lDown && !prevL {
		clicks++
	}
	if rDown && !prevR {
		clicks++
	}
	prevL, prevR = lDown, rDown
	return clicks
}
