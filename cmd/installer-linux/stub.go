//go:build !packaging

// Package main is the My Monitor Linux installer.
// Build the real installer with: make package-linux-amd64
package main

import "fmt"

func main() {
	fmt.Println("My Monitor Linux Installer stub.")
	fmt.Println("Build the real installer with: make package-linux-amd64")
}
