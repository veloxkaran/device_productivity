//go:build !go1.21

// min and max became predeclared builtins in Go 1.21. The desktop app imports this
// package (main.go -> my-monitor/hub), so when the app is built with an older Go
// toolchain (Go 1.20) to reach legacy Windows 7/8 and macOS 10.13/10.14 — which newer
// Go releases no longer support — those builtins are missing. These shims provide them
// for that legacy build only; on Go 1.21+ this file is excluded and the real builtins win.
package hub

type orderedForCompat interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~uintptr |
		~float32 | ~float64 | ~string
}

func min[T orderedForCompat](a, b T) T {
	if a < b {
		return a
	}
	return b
}

func max[T orderedForCompat](a, b T) T {
	if a > b {
		return a
	}
	return b
}
