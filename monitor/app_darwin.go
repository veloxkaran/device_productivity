package monitor

import (
	"os/exec"
	"regexp"
	"strings"
)

var lsNameRe = regexp.MustCompile(`"(?:LSDisplayName|name)"="([^"]*)"`)

func getActiveApp() string {
	asn, err := exec.Command("lsappinfo", "front").Output()
	if err != nil {
		return ""
	}
	out, err := exec.Command("lsappinfo", "info", "-only", "name", strings.TrimSpace(string(asn))).Output()
	if err != nil {
		return ""
	}
	if m := lsNameRe.FindStringSubmatch(string(out)); m != nil {
		return m[1]
	}
	return ""
}
