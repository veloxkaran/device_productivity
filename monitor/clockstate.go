package monitor

import "sync/atomic"

// clockedIn is true while a user has an active time-entry session.
// Capture goroutines read this before taking a screenshot.
var clockedIn atomic.Bool

func IsClockedIn() bool   { return clockedIn.Load() }
func SetClockedIn(v bool) { clockedIn.Store(v) }
