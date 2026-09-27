package capture

import (
	"errors"
	"image"
	"os"
	"os/exec"
	"strings"
)

type linuxTool struct {
	name string
	args func(path string) []string
}

func linuxTools() []linuxTool {
	wayland := os.Getenv("WAYLAND_DISPLAY") != "" || strings.EqualFold(os.Getenv("XDG_SESSION_TYPE"), "wayland")
	x11 := []linuxTool{
		{"scrot", func(p string) []string { return []string{"--silent", "--overwrite", p} }},
		{"import", func(p string) []string { return []string{"-window", "root", p} }},
		{"maim", func(p string) []string { return []string{p} }},
	}
	wl := []linuxTool{
		{"grim", func(p string) []string { return []string{p} }},
		{"gnome-screenshot", func(p string) []string { return []string{"-f", p} }},
		{"spectacle", func(p string) []string { return []string{"-b", "-n", "-o", p} }},
	}
	if wayland {
		return append(wl, x11...)
	}
	return append(x11, wl...)
}

func prepare() {}

func grab() (image.Image, error) {
	// No graphical session (e.g. running before login or a disconnected
	// session) => nothing to capture; skip quietly instead of erroring.
	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		return nil, ErrScreenUnavailable
	}
	var tried []string
	for _, t := range linuxTools() {
		if _, err := exec.LookPath(t.name); err != nil {
			continue
		}
		tried = append(tried, t.name)
		tool := t
		img, err := grabViaFile(func(path string) error {
			return exec.Command(tool.name, tool.args(path)...).Run()
		})
		if err == nil {
			return img, nil
		}
	}
	if len(tried) == 0 {
		return nil, errors.New("no screenshot tool found: install grim (Wayland) or scrot (X11)")
	}
	return nil, errors.New("screenshot failed with: " + strings.Join(tried, ", "))
}
