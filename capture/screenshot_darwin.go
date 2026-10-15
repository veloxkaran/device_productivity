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

// Capture the main display IN-PROCESS. Because the TCC Screen Recording check is
// then attributed to this app (which already holds the grant) instead of a
// spawned /usr/sbin/screencapture child, the permission sticks and macOS stops
// re-prompting on every launch/update.
// Returns a malloc'd RGBA buffer (caller frees) and sets w/h/stride; NULL on failure.
static unsigned char* captureMainDisplay(int* w, int* h, int* stride) {
    CGDirectDisplayID disp = CGMainDisplayID();
    CGImageRef img = CGDisplayCreateImage(disp);
    if (!img) return NULL;
    size_t width  = CGImageGetWidth(img);
    size_t height = CGImageGetHeight(img);
    size_t bpr    = width * 4;
    unsigned char* buf = (unsigned char*)malloc(bpr * height);
    if (!buf) { CGImageRelease(img); return NULL; }
    CGColorSpaceRef cs = CGColorSpaceCreateDeviceRGB();
    CGContextRef ctx = CGBitmapContextCreate(buf, width, height, 8, bpr, cs,
        kCGImageAlphaPremultipliedLast | kCGBitmapByteOrder32Big);
    if (!ctx) { free(buf); CGColorSpaceRelease(cs); CGImageRelease(img); return NULL; }
    CGContextDrawImage(ctx, CGRectMake(0, 0, (CGFloat)width, (CGFloat)height), img);
    CGContextRelease(ctx);
    CGColorSpaceRelease(cs);
    CGImageRelease(img);
    *w = (int)width; *h = (int)height; *stride = (int)bpr;
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
	buf := C.captureMainDisplay(&w, &h, &stride)
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
