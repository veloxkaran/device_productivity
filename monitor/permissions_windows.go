package monitor

import "os/exec"

// PermissionStatus reports what capabilities are available on this machine.
type PermissionStatus struct {
	ScreencaptureBinary bool `json:"screencaptureBinary"`
	ScreenRecording     bool `json:"screenRecording"`
	ActivityMonitor     bool `json:"activityMonitor"`
}

// CheckPermissions always returns true on Windows — no special grants required.
func CheckPermissions() PermissionStatus {
	return PermissionStatus{
		ScreencaptureBinary: true,
		ScreenRecording:     true,
		ActivityMonitor:     true,
	}
}

// TakeTestScreenshot uses PowerShell + .NET to take a one-off test screenshot.
func TakeTestScreenshot(path string) error {
	script := `Add-Type -AssemblyName System.Windows.Forms,System.Drawing
$s = [System.Windows.Forms.Screen]::PrimaryScreen.Bounds
$b = New-Object System.Drawing.Bitmap($s.Width, $s.Height)
$g = [System.Drawing.Graphics]::FromImage($b)
$g.CopyFromScreen(0, 0, 0, 0, $s.Size)
$b.Save('` + path + `')
$g.Dispose()
$b.Dispose()`
	return exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script).Run()
}
