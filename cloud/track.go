//go:build !legacyos

package cloud

// updateTrack is appended to the auto-update asset key ("<goos>/<goarch>"). The normal
// build has no suffix, so it keeps pulling the standard assets from latest.json.
const updateTrack = ""
