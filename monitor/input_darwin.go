package monitor

import (
	"os/exec"
	"strconv"
	"strings"
)

type MousePos struct{ X, Y int64 }

func getMousePosition() MousePos {
	// Use AppleScript to get mouse coordinates without elevated permissions.
	out, err := exec.Command("osascript", "-e",
		`tell application "System Events" to return (do shell script "python3 -c \"import Quartz; loc=Quartz.NSEvent.mouseLocation(); print(int(loc.x), int(loc.y))\"")`).Output()
	if err != nil {
		return MousePos{}
	}
	parts := strings.Fields(strings.TrimSpace(string(out)))
	if len(parts) < 2 {
		return MousePos{}
	}
	x, _ := strconv.ParseInt(parts[0], 10, 64)
	y, _ := strconv.ParseInt(parts[1], 10, 64)
	return MousePos{X: x, Y: y}
}
