package monitor

import "sync/atomic"

// clockedIn is true while a user has an active time-entry session.
// Capture goroutines read this before taking a screenshot.
var clockedIn atomic.Bool

func IsClockedIn() bool   { return clockedIn.Load() }
func SetClockedIn(v bool) { clockedIn.Store(v) }

var onBreak atomic.Bool

func IsOnBreak() bool   { return onBreak.Load() }
func SetOnBreak(v bool) { onBreak.Store(v) }

// trackingDisabled is set when the employer's Activity module is switched off
// (reported by the hub on every heartbeat). While set, no screenshots or
// activity samples are recorded. Zero value = tracking allowed.
var trackingDisabled atomic.Bool

func TrackingEnabled() bool     { return !trackingDisabled.Load() }
func SetTrackingEnabled(v bool) { trackingDisabled.Store(!v) }

// autoMode is true when the employer has set this member to "auto" tracking:
// the agent clocks in automatically whenever it is running and signed in, with
// no Clock In press. Reported by the hub on every heartbeat. Zero value = manual.
var autoMode atomic.Bool

func AutoMode() bool     { return autoMode.Load() }
func SetAutoMode(v bool) { autoMode.Store(v) }
