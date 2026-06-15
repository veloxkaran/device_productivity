package monitor

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
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

func resolveDisplay() string {
	if d := os.Getenv("DISPLAY"); d != "" {
		return d
	}
	entries, err := os.ReadDir("/tmp/.X11-unix")
	if err != nil {
		return ""
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "X") {
			return ":" + name[1:]
		}
	}
	return ""
}

// TakeTestScreenshot captures a screenshot to path for permission verification.
func TakeTestScreenshot(path string) error {
	display := resolveDisplay()
	env := os.Environ()
	if display != "" {
		env = append(env, "DISPLAY="+display)
	}

	run := func(name string, args ...string) error {
		cmd := exec.Command(name, args...)
		cmd.Env = env
		return cmd.Run()
	}

	if run("scrot", "--silent", path) == nil {
		return nil
	}
	if run("import", "-window", "root", path) == nil {
		return nil
	}
	if run("gnome-screenshot", "-f", path) == nil {
		return nil
	}
	return fmt.Errorf("no screenshot tool found: install scrot (apt install scrot) or imagemagick")
}
