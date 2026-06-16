package machine

import (
	"os"
	"os/exec"
	"strings"
)

func getMachineID() string {
	// /etc/machine-id is generated once at OS install — ideal hardware fingerprint
	if b, err := os.ReadFile("/etc/machine-id"); err == nil {
		id := strings.TrimSpace(string(b))
		if id != "" {
			return hash(id)
		}
	}
	// Fallback: DMI product UUID (requires root on some systems)
	if b, err := os.ReadFile("/sys/class/dmi/id/product_uuid"); err == nil {
		id := strings.TrimSpace(string(b))
		if id != "" {
			return hash(id)
		}
	}
	return fallbackID()
}

func getOSVersion() string {
	out, err := exec.Command("uname", "-r").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// fallbackID generates a UUID stored in data/machine_id so it survives reboots.
func fallbackID() string {
	const path = "data/machine_id"
	if b, err := os.ReadFile(path); err == nil {
		id := strings.TrimSpace(string(b))
		if id != "" {
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
