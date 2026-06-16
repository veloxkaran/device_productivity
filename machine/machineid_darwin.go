package machine

import (
	"os"
	"os/exec"
	"strings"
)

func getMachineID() string {
	out, err := exec.Command(
		"ioreg", "-rd1", "-c", "IOPlatformExpertDevice",
	).Output()
	if err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if strings.Contains(line, "IOPlatformUUID") {
				parts := strings.SplitN(line, "=", 2)
				if len(parts) == 2 {
					raw := strings.Trim(strings.TrimSpace(parts[1]), `"`)
					if raw != "" {
						return hash(raw)
					}
				}
			}
		}
	}
	return fallbackID()
}

func getOSVersion() string {
	out, err := exec.Command("sw_vers", "-productVersion").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func fallbackID() string {
	const path = "data/machine_id"
	if b, err := os.ReadFile(path); err == nil {
		if id := strings.TrimSpace(string(b)); id != "" {
			return id
		}
	}
	out, err := exec.Command("uuidgen").Output()
	if err != nil {
		return "unknown"
	}
	id := strings.TrimSpace(string(out))
	_ = os.WriteFile(path, []byte(id), 0600)
	return id
}
