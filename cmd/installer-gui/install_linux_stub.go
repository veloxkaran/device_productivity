//go:build packaging && linux && uninstaller

package main

func doInstall(func(string)) error { return nil }
func displayInstallDir() string    { return linuxInstallDir() }
func openTracker()                 {}
