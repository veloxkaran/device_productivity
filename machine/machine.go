package machine

import (
	"crypto/sha256"
	"fmt"
	"net"
	"os"
	"runtime"
	"strings"
)

// Info holds everything about this machine that gets sent to Hajir on registration.
type Info struct {
	MachineID   string
	MachineName string
	OS          string
	OSVersion   string
	IPAddress   string
	AppVersion  string
}

const AppVersion = "1.0.0"

// GetInfo returns the current machine's identity and metadata.
func GetInfo() Info {
	id := getMachineID()
	hostname, _ := os.Hostname()
	return Info{
		MachineID:   id,
		MachineName: hostname,
		OS:          runtime.GOOS,
		OSVersion:   getOSVersion(),
		IPAddress:   getOutboundIP(),
		AppVersion:  AppVersion,
	}
}

// hash produces a stable short fingerprint from a raw ID string.
func hash(raw string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(raw)))
	return fmt.Sprintf("%x", sum[:16])
}

func getOutboundIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}
