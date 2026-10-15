//go:build packaging && darwin && uninstaller

package main

// Stubs so the slim uninstaller build links without embedding the app payload.
// The uninstaller never runs these (defaultMode == "uninstall").
func doInstall(func(string)) error { return nil }
func displayInstallDir() string    { return appBundlePath() }
func openTracker()                 {}
