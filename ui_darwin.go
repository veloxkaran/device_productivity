package main

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Cocoa -framework WebKit
void runCocoa(int hidden);
void hideStatusItem(void);
*/
import "C"

import "runtime"

var uiOpen func()
var uiQuit func()

func init() { runtime.LockOSThread() }

//export goOpenWindow
func goOpenWindow() {
	if uiOpen != nil {
		uiOpen()
	}
}

//export goQuit
func goQuit() {
	if uiQuit != nil {
		go uiQuit()
	}
}

const nativeWindow = true

func hideMenuBar() { C.hideStatusItem() }

func runUI(open func(), quit func(), managed bool) {
	uiOpen, uiQuit = open, quit
	hidden := 0
	if managed {
		hidden = 1
	}
	C.runCocoa(C.int(hidden))
}
