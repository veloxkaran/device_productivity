package machine

import (
	"os"
	"os/exec"
	"strings"
)

func getMachineID() string {
	out, err := exec.Command(
		"powershell", "-Command",
		`(Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Cryptography' -Name MachineGuid).MachineGuid`,
	).Output()
	if err == nil {
		id := strings.TrimSpace(string(out))
		if id != "" {
			return hash(id)
		}
	}
	return fallbackID()
}

func getOSVersion() string {
	out, err := exec.Command(
		"powershell", "-Command",
		`(Get-WmiObject Win32_OperatingSystem).Version`,
	).Output()
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
	out, err := exec.Command("powershell", "-Command", "[System.Guid]::NewGuid().ToString()").Output()
	if err != nil {
		return "unknown"
	}
	id := strings.TrimSpace(string(out))
	_ = os.WriteFile(path, []byte(id), 0600)
	return id
}
