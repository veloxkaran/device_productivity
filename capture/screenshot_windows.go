package capture

import (
	"fmt"
	"image"
	"runtime"
	"syscall"
	"unsafe"
)

var (
	modGdi32  = syscall.NewLazyDLL("gdi32.dll")
	modUser32 = syscall.NewLazyDLL("user32.dll")

	procGetDC                     = modUser32.NewProc("GetDC")
	procReleaseDC                 = modUser32.NewProc("ReleaseDC")
	procGetSystemMetrics          = modUser32.NewProc("GetSystemMetrics")
	procOpenInputDesktop          = modUser32.NewProc("OpenInputDesktop")
	procCloseDesktop              = modUser32.NewProc("CloseDesktop")
	procGetUserObjectInformation  = modUser32.NewProc("GetUserObjectInformationW")
	procSetProcessDPIAware        = modUser32.NewProc("SetProcessDPIAware")
	procSetProcessDpiAwarenessCtx = modUser32.NewProc("SetProcessDpiAwarenessContext")
	procCreateCompatibleDC        = modGdi32.NewProc("CreateCompatibleDC")
	procCreateDIBSection          = modGdi32.NewProc("CreateDIBSection")
	procSelectObject              = modGdi32.NewProc("SelectObject")
	procBitBlt                    = modGdi32.NewProc("BitBlt")
	procGdiFlush                  = modGdi32.NewProc("GdiFlush")
	procDeleteObject              = modGdi32.NewProc("DeleteObject")
	procDeleteDC                  = modGdi32.NewProc("DeleteDC")
)

const (
	smCxScreen        = 0
	smCyScreen        = 1
	smXVirtualScreen  = 76
	smYVirtualScreen  = 77
	smCxVirtualScreen = 78
	smCyVirtualScreen = 79

	srcCopy            = 0x00CC0020
	captureBlt         = 0x40000000
	dibRGBColors       = 0
	uoiName            = 2
	desktopReadObjects = 0x0001

	// Guard against absurd allocations on huge multi-monitor walls.
	maxPixels = 16384 * 8192
)

type bitmapInfoHeader struct {
	biSize          uint32
	biWidth         int32
	biHeight        int32
	biPlanes        uint16
	biBitCount      uint16
	biCompression   uint32
	biSizeImage     uint32
	biXPelsPerMeter int32
	biYPelsPerMeter int32
	biClrUsed       uint32
	biClrImportant  uint32
}

type bitmapInfo struct {
	bmiHeader bitmapInfoHeader
	bmiColors [1]uint32
}

func prepare() {
	// Per-monitor DPI awareness (Win10 1703+) so we get real pixel sizes on
	// mixed-DPI setups; fall back to the legacy system-aware call.
	if procSetProcessDpiAwarenessCtx.Find() == nil {
		// DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 = -4
		if r, _, _ := procSetProcessDpiAwarenessCtx.Call(^uintptr(3)); r != 0 {
			return
		}
	}
	procSetProcessDPIAware.Call()
}

// inputDesktopAvailable reports whether the interactive "Default" desktop is
// the one receiving input. It is not while the workstation is locked, on the
// Ctrl+Alt+Del / UAC secure desktop, or when there is no interactive session
// (Session 0 service, disconnected RDP).
func inputDesktopAvailable() bool {
	h, _, _ := procOpenInputDesktop.Call(0, 0, desktopReadObjects)
	if h == 0 {
		return false
	}
	defer procCloseDesktop.Call(h)
	var buf [64]uint16
	var needed uint32
	r, _, _ := procGetUserObjectInformation.Call(h, uoiName,
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)*2), uintptr(unsafe.Pointer(&needed)))
	if r == 0 {
		return true // can't tell; let the capture attempt decide
	}
	return syscall.UTF16ToString(buf[:]) == "Default"
}

type rect struct{ x, y, w, h int }

func screenRects() []rect {
	m := func(i uintptr) int { v, _, _ := procGetSystemMetrics.Call(i); return int(int32(v)) }
	var out []rect
	if w, h := m(smCxVirtualScreen), m(smCyVirtualScreen); w > 0 && h > 0 {
		out = append(out, rect{m(smXVirtualScreen), m(smYVirtualScreen), w, h})
	}
	// Fallback: primary monitor only.
	if w, h := m(smCxScreen), m(smCyScreen); w > 0 && h > 0 {
		if len(out) == 0 || out[0].w != w || out[0].h != h {
			out = append(out, rect{0, 0, w, h})
		}
	}
	return out
}

func grab() (image.Image, error) {
	// GDI handles and the input-desktop check are thread-bound.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if !inputDesktopAvailable() {
		return nil, ErrScreenUnavailable
	}
	rects := screenRects()
	if len(rects) == 0 {
		return nil, ErrScreenUnavailable
	}

	var lastErr error
	for _, r := range rects {
		// Try with CAPTUREBLT (includes layered windows), then without — some
		// drivers / RDP / VM display adapters reject it.
		for _, rop := range []uintptr{srcCopy | captureBlt, srcCopy} {
			img, err := grabRect(r, rop)
			if err == nil {
				return img, nil
			}
			lastErr = err
		}
	}
	// If the desktop went away mid-attempt (lock, UAC), don't report an error.
	if !inputDesktopAvailable() {
		return nil, ErrScreenUnavailable
	}
	return nil, lastErr
}

// grabRect copies the screen straight into a DIB section, so no GetDIBits
// call (and its DC/selection pitfalls) is needed.
func grabRect(r rect, rop uintptr) (image.Image, error) {
	if r.w*r.h > maxPixels {
		return nil, fmt.Errorf("screen too large: %dx%d", r.w, r.h)
	}
	screenDC, _, e := procGetDC.Call(0)
	if screenDC == 0 {
		return nil, fmt.Errorf("GetDC failed: %v", e)
	}
	defer procReleaseDC.Call(0, screenDC)

	memDC, _, e := procCreateCompatibleDC.Call(screenDC)
	if memDC == 0 {
		return nil, fmt.Errorf("CreateCompatibleDC failed: %v", e)
	}
	defer procDeleteDC.Call(memDC)

	bi := bitmapInfo{bmiHeader: bitmapInfoHeader{
		biWidth:    int32(r.w),
		biHeight:   -int32(r.h), // top-down
		biPlanes:   1,
		biBitCount: 32,
	}}
	bi.bmiHeader.biSize = uint32(unsafe.Sizeof(bi.bmiHeader))

	var bits unsafe.Pointer
	hBmp, _, e := procCreateDIBSection.Call(memDC, uintptr(unsafe.Pointer(&bi)), dibRGBColors,
		uintptr(unsafe.Pointer(&bits)), 0, 0)
	if hBmp == 0 || bits == nil {
		return nil, fmt.Errorf("CreateDIBSection failed (%dx%d): %v", r.w, r.h, e)
	}
	defer procDeleteObject.Call(hBmp)

	prev, _, _ := procSelectObject.Call(memDC, hBmp)
	if prev != 0 {
		defer procSelectObject.Call(memDC, prev)
	}
	ok, _, e := procBitBlt.Call(memDC, 0, 0, uintptr(r.w), uintptr(r.h), screenDC,
		uintptr(r.x), uintptr(r.y), rop)
	if ok == 0 {
		return nil, fmt.Errorf("BitBlt failed: %v", e)
	}
	procGdiFlush.Call()

	n := r.w * r.h * 4
	src := unsafe.Slice((*byte)(bits), n)
	img := image.NewNRGBA(image.Rect(0, 0, r.w, r.h))
	dst := img.Pix
	allBlack := true
	for i := 0; i < n; i += 4 {
		b, g, rr := src[i], src[i+1], src[i+2]
		dst[i], dst[i+1], dst[i+2], dst[i+3] = rr, g, b, 255
		if allBlack && (rr|g|b) != 0 {
			allBlack = false
		}
	}
	// An all-black frame usually means a locked / disconnected display that
	// still let BitBlt "succeed". Treat it as unavailable, not as a shot.
	if allBlack && !inputDesktopAvailable() {
		return nil, ErrScreenUnavailable
	}
	return img, nil
}
