package capture

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// resolveDisplay returns a DISPLAY value to use for screenshot commands.
// Prefers the process env; falls back to scanning /tmp/.X11-unix/.
func resolveDisplay() string {
	if d := os.Getenv("DISPLAY"); d != "" {
		return d
	}
	entries, err := os.ReadDir("/tmp/.X11-unix")
	if err != nil {
		return ""
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "X") {
			return ":" + name[1:]
		}
	}
	return ""
}

func takeScreenshot(path string) error {
	display := resolveDisplay()

	env := os.Environ()
	if display != "" {
		env = append(env, "DISPLAY="+display)
	}

	runCmd := func(name string, args ...string) error {
		cmd := exec.Command(name, args...)
		cmd.Env = env
		return cmd.Run()
	}

	// Capture raw PNG to a temp file, then compress to JPEG via ImageMagick.
	tmp := path + ".tmp.png"
	defer os.Remove(tmp)

	captured := false
	if runCmd("scrot", "--silent", tmp) == nil {
		captured = true
	} else if runCmd("import", "-window", "root", tmp) == nil {
		captured = true
	} else if runCmd("gnome-screenshot", "-f", tmp) == nil {
		captured = true
	}

	if !captured {
		return fmt.Errorf("no screenshot tool found: install scrot (apt install scrot) or imagemagick")
	}

	// Resize to max 1280px wide and encode as JPEG at quality 70 (~60-80 KB).
	if runCmd("convert", tmp, "-resize", "1280x>", "-quality", "70", path) != nil {
		// ImageMagick unavailable — fall back to plain JPEG via scrot directly.
		os.Remove(tmp)
		return runCmd("scrot", "--silent", "-q", "70", path)
	}
	return nil
}
