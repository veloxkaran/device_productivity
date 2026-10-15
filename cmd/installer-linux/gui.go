//go:build packaging

// Graphical install flow for Linux desktops, using zenity (present on GNOME and
// most mainstream desktops). When a graphical session with zenity is available
// and the install is interactive, the installer shows native dialogs instead of
// terminal output. Everything degrades cleanly to the console flow otherwise.
package main

import (
	"os"
	"os/exec"
	"strings"
	"sync"
)

// useGUI is set once the installer decides to drive a graphical flow.
var useGUI bool

// guiEnv reports whether a graphical session with zenity is available.
func guiEnv() bool {
	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		return false
	}
	_, err := exec.LookPath("zenity")
	return err == nil
}

// guiWelcome shows the welcome/confirm dialog. Returns true if the user chose to
// install, false if they cancelled.
func guiWelcome() bool {
	err := exec.Command("zenity", "--question",
		"--title=Install Hajir Tracker",
		"--width=440",
		"--ok-label=Install",
		"--cancel-label=Cancel",
		"--text=Install Hajir Tracker on this computer?\n\nIt runs quietly in the background and starts automatically when you sign in. You can open it any time from the applications menu, and your data stays on this device.",
	).Run()
	// zenity exits 0 when the OK/Install button is pressed.
	return err == nil
}

// guiProgress opens an indeterminate progress dialog and returns a stop func.
// The stop func is idempotent, so it is safe to call from the finish path and
// again via defer.
func guiProgress() func() {
	cmd := exec.Command("zenity", "--progress",
		"--title=Installing Hajir Tracker",
		"--width=440",
		"--pulsate",
		"--auto-close",
		"--no-cancel",
		"--text=Installing Hajir Tracker…",
	)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return func() {}
	}
	if err := cmd.Start(); err != nil {
		return func() {}
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			// "100" lets --auto-close dismiss the dialog, then wait for it.
			_, _ = stdin.Write([]byte("100\n"))
			_ = stdin.Close()
			_ = cmd.Wait()
		})
	}
}

// guiFinish shows the completion dialog. The "Open Hajir Tracker now" box is
// UNCHECKED by default, so finishing the install never opens a browser on its
// own — the user opts in if they want it.
func guiFinish(openURL string) {
	out, _ := exec.Command("zenity", "--list", "--checklist",
		"--title=Installation complete",
		"--width=480", "--height=240",
		"--ok-label=Finish", "--cancel-label=Finish",
		"--text=Hajir Tracker is installed and will start automatically when you sign in.\nYou can open it any time from the applications menu.",
		"--column=", "--column=Option",
		"FALSE", "Open Hajir Tracker now",
	).Output()
	if strings.Contains(string(out), "Open Hajir Tracker now") {
		_ = exec.Command("xdg-open", openURL).Start()
	}
}
