//go:build legacyos

package main

import (
	"fmt"
	"os"
)

// maybeRunHub is a stub for the legacy-OS build, which does not include the hub server
// (it relies on Go 1.22 net/http features unavailable to the Go 1.20 toolchain). Old
// desktops only ever run the agent, never the hub.
func maybeRunHub() bool {
	if len(os.Args) > 1 && os.Args[1] == "hub" {
		fmt.Fprintln(os.Stderr, "hub mode is not available in this (legacy) build")
		os.Exit(1)
	}
	return false
}
