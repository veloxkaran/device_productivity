//go:build packaging && windows && uninstaller

package main

func doInstall(func(string)) error { return nil }
func displayInstallDir() string    { return installDirectory() }
func openTracker()                 {}
