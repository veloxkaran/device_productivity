package monitor

/*
#cgo LDFLAGS: -framework ApplicationServices
#include <ApplicationServices/ApplicationServices.h>

static void cursorPos(CGFloat *x, CGFloat *y) {
    CGEventRef e = CGEventCreate(NULL);
    CGPoint p = CGEventGetLocation(e);
    CFRelease(e);
    *x = p.x;
    *y = p.y;
}
*/
import "C"

// getMousePos returns the current cursor coordinates via CoreGraphics.
// No special macOS permission is required.
func getMousePos() (float64, float64) {
	var x, y C.CGFloat
	C.cursorPos(&x, &y)
	return float64(x), float64(y)
}

// sampleClicks returns 0 on macOS — click counting needs Accessibility permission
// (CGEventTap). Clicks are approximated via idle-time resets in input.go instead.
func sampleClicks() int64 {
	return 0
}
