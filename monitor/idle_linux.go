package monitor

import (
	"os/exec"
	"strconv"
	"strings"
)

// getIdleSeconds uses xprintidle which returns milliseconds of X11 idle time.
// Returns 0 if xprintidle is not installed or X11 is unavailable.
func getIdleSeconds() int64 {
	out, err := exec.Command("xprintidle").Output()
	if err != nil {
		return 0
	}
	ms, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return 0
	}
	return ms / 1000
}
