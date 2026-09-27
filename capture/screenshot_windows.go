package capture

import (
	"fmt"
	"image"
	"syscall"
	"unsafe"
)

var (
	modGdi32  = syscall.NewLazyDLL("gdi32.dll")
	modUser32 = syscall.NewLazyDLL("user32.dll")

	procGetDC                  = modUser32.NewProc("GetDC")
	procReleaseDC              = modUser32.NewProc("ReleaseDC")
	procGetSystemMetrics       = modUser32.NewProc("GetSystemMetrics")
	procCreateCompatibleDC     = modGdi32.NewProc("CreateCompatibleDC")
	procCreateCompatibleBitmap = modGdi32.NewProc("CreateCompatibleBitmap")
	procSelectObject           = modGdi32.NewProc("SelectObject")
	procBitBlt                 = modGdi32.NewProc("BitBlt")
	procDeleteObject           = modGdi32.NewProc("DeleteObject")
	procDeleteDC               = modGdi32.NewProc("DeleteDC")
	procGetDIBits              = modGdi32.NewProc("GetDIBits")
	procSetProcessDPIAware     = modUser32.NewProc("SetProcessDPIAware")
)

const (
	smCxScreen   = 78
	smCyScreen   = 79
	smXVirtual   = 76
	smYVirtual   = 77
	srcCopy      = 0x00CC0020
	captureBlt   = 0x40000000
	dibRGBColors = 0
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
	procSetProcessDPIAware.Call()
}

func grab() (image.Image, error) {
	w, _, _ := procGetSystemMetrics.Call(smCxScreen)
	h, _, _ := procGetSystemMetrics.Call(smCyScreen)
	vx, _, _ := procGetSystemMetrics.Call(smXVirtual)
	vy, _, _ := procGetSystemMetrics.Call(smYVirtual)
	originX, originY := int32(vx), int32(vy)
	width, height := int(w), int(h)
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("invalid screen size: %dx%d", width, height)
	}

	screenDC, _, _ := procGetDC.Call(0)
	if screenDC == 0 {
		return nil, fmt.Errorf("GetDC failed")
	}
	defer procReleaseDC.Call(0, screenDC)

	memDC, _, _ := procCreateCompatibleDC.Call(screenDC)
	if memDC == 0 {
		return nil, fmt.Errorf("CreateCompatibleDC failed")
	}
	defer procDeleteDC.Call(memDC)

	hBmp, _, _ := procCreateCompatibleBitmap.Call(screenDC, uintptr(width), uintptr(height))
	if hBmp == 0 {
		return nil, fmt.Errorf("CreateCompatibleBitmap failed")
	}
	defer procDeleteObject.Call(hBmp)

	prevObj, _, _ := procSelectObject.Call(memDC, hBmp)
	ret, _, _ := procBitBlt.Call(memDC, 0, 0, uintptr(width), uintptr(height), screenDC, uintptr(originX), uintptr(originY), srcCopy|captureBlt)
	if ret == 0 {
		return nil, fmt.Errorf("BitBlt failed")
	}
	// GetDIBits fails if the bitmap is still selected into a DC — restore the
	// DC's previous bitmap first so hBmp is free to read.
	if prevObj != 0 {
		procSelectObject.Call(memDC, prevObj)
	}

	bi := bitmapInfo{
		bmiHeader: bitmapInfoHeader{
			biSize:     uint32(unsafe.Sizeof(bitmapInfoHeader{})),
			biWidth:    int32(width),
			biHeight:   -int32(height), // negative = top-down
			biPlanes:   1,
			biBitCount: 32,
		},
	}
	pixels := make([]byte, width*height*4)
	r, _, _ := procGetDIBits.Call(
		screenDC, hBmp, 0, uintptr(height),
		uintptr(unsafe.Pointer(&pixels[0])),
		uintptr(unsafe.Pointer(&bi)),
		dibRGBColors,
	)
	if r == 0 {
		return nil, fmt.Errorf("GetDIBits failed")
	}

	// GDI DIB is BGRA; screen DCs have alpha=0 — force 255.
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			i := (y*width + x) * 4
			pi := img.PixOffset(x, y)
			img.Pix[pi+0] = pixels[i+2] // R
			img.Pix[pi+1] = pixels[i+1] // G
			img.Pix[pi+2] = pixels[i+0] // B
			img.Pix[pi+3] = 255         // A
		}
	}

	return img, nil
}
