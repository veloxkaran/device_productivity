//go:build windows

package cloud

import (
	"os"
	"os/exec"
)

// applyAndRestart swaps the binary and restarts. Windows cannot overwrite a
// running .exe, so the current one is renamed aside first; the new process
// cleans up the .old file on its next launch (best-effort below).
func applyAndRestart(exe, newFile string) error {
	os.Remove(exe + ".old") // remove any leftover from a previous update
	if err := os.Rename(exe, exe+".old"); err != nil {
		return err
	}
	if err := os.Rename(newFile, exe); err != nil {
		os.Rename(exe+".old", exe) // roll back
		return err
	}
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Env = os.Environ()
	if err := cmd.Start(); err != nil {
		return err
	}
	os.Exit(0)
	return nil
}
