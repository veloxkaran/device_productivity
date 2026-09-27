//go:build !windows

package cloud

import (
	"os"
	"syscall"
)

// applyAndRestart replaces the running binary and re-executes it in place.
// On Unix a running process keeps its open file, so overwriting the path is
// safe; syscall.Exec then swaps the process image for the new binary.
func applyAndRestart(exe, newFile string) error {
	backup := exe + ".old"
	os.Remove(backup)
	if err := os.Rename(exe, backup); err != nil {
		return err
	}
	if err := os.Rename(newFile, exe); err != nil {
		os.Rename(backup, exe) // roll back
		return err
	}
	os.Remove(backup)
	// Replace this process with the freshly installed binary.
	return syscall.Exec(exe, os.Args, os.Environ())
}
