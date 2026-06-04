package monitor

import (
	"fmt"
	"os/exec"
)

// PermissionStatus reports what capabilities are available on this machine.
type PermissionStatus struct {
	ScreencaptureBinary bool `json:"screencaptureBinary"`
	ScreenRecording     bool `json:"screenRecording"`
	ActivityMonitor     bool `json:"activityMonitor"`
}

// CheckPermissions checks which tools are installed and usable.
func CheckPermissions() PermissionStatus {
	hasShot := commandExists("scrot") || commandExists("import") || commandExists("gnome-screenshot")
	return PermissionStatus{
		ScreencaptureBinary: hasShot,
		ScreenRecording:     hasShot,
		ActivityMonitor:     commandExists("xprintidle"),
	}
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// TakeTestScreenshot captures a screenshot to path for permission verification.
func TakeTestScreenshot(path string) error {
	if err := exec.Command("scrot", "--silent", path).Run(); err == nil {
		return nil
	}
	if err := exec.Command("import", "-window", "root", path).Run(); err == nil {
		return nil
	}
	if err := exec.Command("gnome-screenshot", "-f", path).Run(); err == nil {
		return nil
	}
	return fmt.Errorf("no screenshot tool found: install scrot (apt install scrot) or imagemagick")
}
