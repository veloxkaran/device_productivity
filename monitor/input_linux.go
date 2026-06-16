package monitor

import (
	"os/exec"
	"strconv"
	"strings"
)

type MousePos struct{ X, Y int64 }

// getMousePosition reads the current cursor coordinates via xdotool.
// Returns (0, 0) if xdotool is not installed or display is unavailable.
func getMousePosition() MousePos {
	display := resolveDisplay()
	cmd := exec.Command("xdotool", "getmouselocation")
	cmd.Env = append(cmd.Environ(), "DISPLAY="+display)
	out, err := cmd.Output()
	if err != nil {
		return MousePos{}
	}
	// output: "x:123 y:456 screen:0 window:12345"
	var pos MousePos
	for _, field := range strings.Fields(string(out)) {
		kv := strings.SplitN(field, ":", 2)
		if len(kv) != 2 {
			continue
		}
		v, _ := strconv.ParseInt(kv[1], 10, 64)
		switch kv[0] {
		case "x":
			pos.X = v
		case "y":
			pos.Y = v
		}
	}
	return pos
}
