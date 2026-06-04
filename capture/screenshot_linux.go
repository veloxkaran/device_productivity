package capture

import (
	"fmt"
	"os/exec"
)

func takeScreenshot(path string) error {
	if err := exec.Command("scrot", "--silent", path).Run(); err == nil {
		return nil
	}
	if err := exec.Command("import", "-window", "root", path).Run(); err == nil {
		return nil
	}
	if err := exec.Command("gnome-screenshot", "-f", path).Run(); err == nil {
		return nil
	}
	return fmt.Errorf("no screenshot tool found: install scrot (apt install scrot) or imagemagick")
}
