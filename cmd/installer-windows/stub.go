//go:build !packaging

// Package main is the My Monitor Windows installer.
// Build the real installer with: make package-windows-amd64
package main

import "fmt"

func main() {
	fmt.Println("My Monitor Windows Installer stub.")
	fmt.Println("Build the real installer with: make package-windows-amd64")
}
