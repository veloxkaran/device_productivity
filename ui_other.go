//go:build !darwin && !windows

package main

const nativeWindow = false

func hideMenuBar() {}

func runUI(open func(), quit func(), managed bool) {
	_ = managed
	select {}
}
