package monitor

import (
	"os/exec"
	"strconv"
	"strings"
)

type MousePos struct{ X, Y int64 }

func getMousePosition() MousePos {
	out, err := exec.Command("powershell", "-NoProfile", "-Command",
		`Add-Type -AssemblyName System.Windows.Forms; $p=[System.Windows.Forms.Cursor]::Position; Write-Output "$($p.X) $($p.Y)"`).Output()
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
