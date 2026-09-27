package capture

/*
#cgo LDFLAGS: -framework CoreGraphics -framework CoreFoundation
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
*/
import "C"

import (
	"fmt"
	"image"
	"os/exec"
	"strings"
	"time"
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
	return grabViaFile(func(path string) error {
		out, err := exec.Command("/usr/sbin/screencapture", "-x", "-t", "png", path).CombinedOutput()
		if err != nil {
			msg := strings.TrimSpace(string(out))
			if msg == "" {
				msg = err.Error()
			}
			return fmt.Errorf("screencapture: %s (check Screen Recording permission)", msg)
		}
		return nil
	})
}
