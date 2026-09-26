//go:build !darwin

package capture

func ScreenPermission() string { return "granted" }

func RequestScreenPermission() {}

func OpenScreenPermissionSettings() error { return nil }
