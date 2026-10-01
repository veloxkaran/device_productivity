//go:build !legacyos

package main

import (
	"my-monitor/hub"
	"os"
)

// maybeRunHub runs the embedded hub server when invoked as `my-monitor hub` and reports
// whether it handled the command. Present only in the normal (Go 1.22) build.
func maybeRunHub() bool {
	if len(os.Args) > 1 && os.Args[1] == "hub" {
		hub.Run()
		return true
	}
	return false
}
