//go:build darwin

package monitor

import (
	"os"
	"os/exec"
	"strings"
)

// PermissionStatus reports macOS permission state for the app's required capabilities.
type PermissionStatus struct {
	ScreencaptureBinary bool `json:"screencaptureBinary"`
	ScreenRecording     bool `json:"screenRecording"`
	ActivityMonitor     bool `json:"activityMonitor"`
}

// CheckPermissions tests which macOS permissions are currently granted.
func CheckPermissions() PermissionStatus {
	return PermissionStatus{
		ScreencaptureBinary: commandExists("screencapture"),
		ScreenRecording:     testScreenRecording(),
		ActivityMonitor:     testActivityMonitor(),
	}
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// testScreenRecording takes a silent test screenshot to a temp file and checks the
// resulting file size. When Screen Recording is denied macOS still lets screencapture
// run but produces a minimal (< 10 KB) blank capture; a real screen is far larger.
func testScreenRecording() bool {
	if !commandExists("screencapture") {
		return false
	}
	f, err := os.CreateTemp("", "mm-perm-*.png")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	defer os.Remove(name)

	if err := exec.Command("screencapture", "-x", "-t", "png", name).Run(); err != nil {
		return false
	}
	info, err := os.Stat(name)
	if err != nil {
		return false
	}
	return info.Size() > 10240
}

// testActivityMonitor verifies that ioreg returns HID idle-time data.
// This call does not require any special macOS permission.
func testActivityMonitor() bool {
	out, err := exec.Command("ioreg", "-c", "IOHIDSystem").Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "HIDIdleTime")
}

// TakeTestScreenshot captures a screenshot to the given path without recording it
// in the database. It is used by the setup wizard to preview permission status.
func TakeTestScreenshot(path string) error {
	return exec.Command("screencapture", "-x", "-t", "png", path).Run()
}
