package capture

/*
#cgo LDFLAGS: -framework CoreGraphics -framework CoreFoundation
#include <stdlib.h>
#include <CoreGraphics/CoreGraphics.h>
#include <CoreFoundation/CoreFoundation.h>
#include <dispatch/dispatch.h>

static int hasScreenAccess(void) { return CGPreflightScreenCaptureAccess() ? 1 : 0; }
// 1 when a user session is active on the console (a screen exists to capture);
// 0 at the login window, when fast-user-switched away, or with no GUI session.
static int sessionOnConsole(void) {
    CFDictionaryRef d = CGSessionCopyCurrentDictionary();
    if (!d) return 0;
    int on = 0;
    const void *v = CFDictionaryGetValue(d, kCGSessionOnConsoleKey);
    if (v) on = CFBooleanGetValue((CFBooleanRef)v) ? 1 : 0;
    CFRelease(d);
    return on;
}
static void requestScreenAccess(void) {
    dispatch_async(dispatch_get_main_queue(), ^{ CGRequestScreenCaptureAccess(); });
}

// Capture ALL active displays IN-PROCESS and compose them into one image laid
// out by their real arrangement (fixes dual-monitor capture). In-process means
// the TCC Screen Recording check is attributed to this app (which already holds
// the grant), so the permission sticks instead of re-prompting.
// Returns a malloc'd RGBA buffer (caller frees); sets w/h/stride; NULL on failure.
static unsigned char* captureAllDisplays(int* w, int* h, int* stride) {
    CGDirectDisplayID ids[16];
    uint32_t n = 0;
    if (CGGetActiveDisplayList(16, ids, &n) != kCGErrorSuccess || n == 0) return NULL;

    // Backing scale from the main display, so Retina detail is preserved.
    double scale = 1.0;
    {
        CGDirectDisplayID m = CGMainDisplayID();
        CGRect mb = CGDisplayBounds(m);
        size_t pw = CGDisplayPixelsWide(m);
        if (mb.size.width > 0 && pw > 0) scale = (double)pw / mb.size.width;
        if (scale < 1.0) scale = 1.0;
    }

    // Union of all display bounds (global, top-left origin, in points).
    CGRect uni = CGDisplayBounds(ids[0]);
    for (uint32_t i = 1; i < n; i++) uni = CGRectUnion(uni, CGDisplayBounds(ids[i]));
    int W = (int)(uni.size.width  * scale + 0.5);
    int H = (int)(uni.size.height * scale + 0.5);
    if (W <= 0 || H <= 0) return NULL;

    size_t bpr = (size_t)W * 4;
    unsigned char* buf = (unsigned char*)calloc(bpr, (size_t)H);
    if (!buf) return NULL;
    CGColorSpaceRef cs = CGColorSpaceCreateDeviceRGB();
    CGContextRef ctx = CGBitmapContextCreate(buf, W, H, 8, bpr, cs,
        kCGImageAlphaPremultipliedLast | kCGBitmapByteOrder32Big);
    if (!ctx) { free(buf); CGColorSpaceRelease(cs); return NULL; }

    int drew = 0;
    for (uint32_t i = 0; i < n; i++) {
        CGImageRef im = CGDisplayCreateImage(ids[i]);
        if (!im) continue;
        CGRect b = CGDisplayBounds(ids[i]);
        double x    = (b.origin.x - uni.origin.x) * scale;
        double ytop = (b.origin.y - uni.origin.y) * scale;
        double dw   = b.size.width  * scale;
        double dh   = b.size.height * scale;
        double y    = (double)H - ytop - dh; // CGBitmapContext is bottom-left origin
        CGContextDrawImage(ctx, CGRectMake(x, y, dw, dh), im);
        CGImageRelease(im);
        drew++;
    }
    CGContextRelease(ctx);
    CGColorSpaceRelease(cs);
    if (!drew) { free(buf); return NULL; }
    *w = W; *h = H; *stride = (int)bpr;
    return buf;
}
*/
import "C"

import (
	"fmt"
	"image"
	"os/exec"
	"time"
	"unsafe"
)

func ScreenPermission() string {
	if C.hasScreenAccess() == 1 {
		return "granted"
	}
	return "denied"
}

func RequestScreenPermission() {
	C.requestScreenAccess()
}

func OpenScreenPermissionSettings() error {
	return exec.Command("open", "x-apple.systempreferences:com.apple.preference.security?Privacy_ScreenCapture").Run()
}

func prepare() {
	go func() {
		time.Sleep(3 * time.Second)
		if ScreenPermission() != "granted" {
			RequestScreenPermission()
		}
	}()
}

func grab() (image.Image, error) {
	// No user on the console (login window / switched-out / headless daemon)
	// => no screen to capture; skip quietly instead of erroring.
	if C.sessionOnConsole() == 0 {
		return nil, ErrScreenUnavailable
	}
	var w, h, stride C.int
	buf := C.captureAllDisplays(&w, &h, &stride)
	if buf == nil {
		return nil, fmt.Errorf("screen capture failed (check Screen Recording permission)")
	}
	defer C.free(unsafe.Pointer(buf))

	width, height, st := int(w), int(h), int(stride)
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	src := C.GoBytes(unsafe.Pointer(buf), C.int(st*height))
	if st == img.Stride {
		copy(img.Pix, src)
	} else {
		row := width * 4
		for y := 0; y < height; y++ {
			copy(img.Pix[y*img.Stride:], src[y*st:y*st+row])
		}
	}
	return img, nil
}
