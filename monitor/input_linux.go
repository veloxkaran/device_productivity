package monitor

import (
	"os/exec"
	"strconv"
	"strings"
)

// getMousePos queries the cursor position via xdotool.
// Returns (0,0) if xdotool is not installed.
func getMousePos() (float64, float64) {
	out, err := exec.Command("xdotool", "getmouselocation", "--shell").Output()
	if err != nil {
		return 0, 0
	}
	var x, y float64
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "X=") {
			x, _ = strconv.ParseFloat(strings.TrimPrefix(line, "X="), 64)
		} else if strings.HasPrefix(line, "Y=") {
			y, _ = strconv.ParseFloat(strings.TrimPrefix(line, "Y="), 64)
		}
	}
	return x, y
}

// sampleClicks returns 0 on Linux — click counting requires /dev/input access.
// Clicks are approximated via idle-time resets in input.go instead.
func sampleClicks() int64 {
	return 0
}
