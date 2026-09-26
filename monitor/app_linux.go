package monitor

import (
	"os/exec"
	"strings"
)

func getActiveApp() string {
	pid, err := exec.Command("xdotool", "getactivewindow", "getwindowpid").Output()
	if err == nil {
		if name, err := exec.Command("ps", "-p", strings.TrimSpace(string(pid)), "-o", "comm=").Output(); err == nil {
			return strings.TrimSpace(string(name))
		}
	}
	return ""
}
