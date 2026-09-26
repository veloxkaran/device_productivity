package capture

/*
#cgo LDFLAGS: -framework CoreGraphics
#include <CoreGraphics/CoreGraphics.h>
#include <dispatch/dispatch.h>

static int hasScreenAccess(void) { return CGPreflightScreenCaptureAccess() ? 1 : 0; }
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
