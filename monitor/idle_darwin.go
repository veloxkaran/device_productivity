package monitor

import (
	"bytes"
	"os/exec"
	"strconv"
	"strings"
)

func getIdleSeconds() int64 {
	out, err := exec.Command("ioreg", "-c", "IOHIDSystem").Output()
	if err != nil {
		return 0
	}
	for _, line := range bytes.Split(out, []byte("\n")) {
		if bytes.Contains(line, []byte("HIDIdleTime")) {
			parts := strings.Fields(string(line))
			if len(parts) == 0 {
				continue
			}
			raw := strings.TrimRight(parts[len(parts)-1], ",")
			ns, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				continue
			}
			return ns / 1_000_000_000
		}
	}
	return 0
}
