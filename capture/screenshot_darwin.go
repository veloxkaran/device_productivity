package capture

import "os/exec"

func takeScreenshot(path string) error {
	return exec.Command("screencapture", "-x", "-t", "png", path).Run()
}
