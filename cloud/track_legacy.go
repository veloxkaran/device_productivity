//go:build legacyos

package cloud

// updateTrack puts a legacy-OS build (Go 1.20, for Windows 7/8 and macOS 10.13/10.14) on
// its own auto-update lane: it looks up "<goos>/<goarch>-legacy" in latest.json. This keeps
// an old machine on legacy binaries instead of self-updating to a modern build its OS
// cannot run — which was exactly the "upgrade to a new version" dead end.
const updateTrack = "-legacy"
