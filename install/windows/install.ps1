# My Monitor — Windows Installer
# Requires: PowerShell 5.1+, Go 1.21+ (or downloads portable Go)
# Usage: Right-click → "Run with PowerShell", or:
#        powershell -ExecutionPolicy Bypass -File install.ps1 [install|uninstall|status|start]

param(
    [string]$Action = "install"
)

$ErrorActionPreference = "Stop"

# ── Config ────────────────────────────────────────────────────────
$AppName      = "my-monitor"
$RegRunKey    = "HKCU:\Software\Microsoft\Windows\CurrentVersion\Run"
$RegValueName = "MyMonitor"
$DefaultPort  = 8080
$ScriptDir    = Split-Path -Parent $MyInvocation.MyCommand.Path
$ProjectDir   = (Resolve-Path (Join-Path $ScriptDir "..\..")).Path
$BinaryPath   = Join-Path $ProjectDir "my-monitor.exe"

# ── Helpers ───────────────────────────────────────────────────────
function Write-Step  { param($msg) Write-Host "`n▶  $msg" -ForegroundColor Cyan }
function Write-OK    { param($msg) Write-Host "   ✓  $msg" -ForegroundColor Green }
function Write-Warn  { param($msg) Write-Host "   ⚠  $msg" -ForegroundColor Yellow }
function Write-Err   { param($msg) Write-Host "   ✗  $msg" -ForegroundColor Red }
function Write-Info  { param($msg) Write-Host "   ℹ  $msg" -ForegroundColor Gray }
function Write-HR    { Write-Host "──────────────────────────────────────────────" -ForegroundColor DarkGray }

function Print-Banner {
    Write-Host ""
    Write-Host "  My Monitor — Windows Installer" -ForegroundColor Blue
    Write-Host "  Activity & Screenshot Monitor"  -ForegroundColor DarkGray
    Write-Host ""
    Write-HR
}

# ── Check Windows ─────────────────────────────────────────────────
function Check-System {
    Write-Step "System requirements"

    $os = (Get-WmiObject Win32_OperatingSystem).Caption
    Write-OK $os

    $arch = $env:PROCESSOR_ARCHITECTURE
    Write-OK "Architecture: $arch"

    if ([System.Environment]::OSVersion.Version.Major -lt 10) {
        Write-Warn "Windows 10 or later is recommended."
    }
}

# ── Go toolchain ──────────────────────────────────────────────────
function Check-Go {
    Write-Step "Go toolchain"

    $go = Get-Command go -ErrorAction SilentlyContinue
    if ($go) {
        $ver = (go version)
        Write-OK "Go found: $ver"
        return
    }

    Write-Warn "Go not found."
    Write-Host ""
    Write-Host "  Please install Go 1.21+ from: https://go.dev/dl/" -ForegroundColor Yellow
    Write-Host "  Then re-run this installer." -ForegroundColor Yellow
    Write-Host ""

    $choice = Read-Host "  Open the Go download page now? [Y/n]"
    if ($choice -notmatch '^[Nn]') {
        Start-Process "https://go.dev/dl/"
    }
    exit 1
}

# ── Build ─────────────────────────────────────────────────────────
function Build-Binary {
    Write-Step "Building My Monitor"

    Set-Location $ProjectDir
    Write-Info "Resolving Go dependencies..."
    go mod tidy

    Write-Info "Compiling for windows/amd64..."
    $env:GOOS = "windows"
    $env:GOARCH = "amd64"
    go build -ldflags="-s -w" -o my-monitor.exe .
    Remove-Item Env:GOOS -ErrorAction SilentlyContinue
    Remove-Item Env:GOARCH -ErrorAction SilentlyContinue

    Write-OK "Binary ready: $BinaryPath"
}

# ── Windows Startup (registry) ────────────────────────────────────
function Setup-Startup {
    Write-Step "Auto-start on login (registry)"

    Write-Host ""
    $existing = Get-ItemProperty -Path $RegRunKey -Name $RegValueName -ErrorAction SilentlyContinue
    if ($existing) {
        Write-OK "Startup entry already installed: $($existing.$RegValueName)"
        $choice = Read-Host "   Re-install / update it? [y/N]"
        if ($choice -notmatch '^[Yy]') { return }
        Remove-ItemProperty -Path $RegRunKey -Name $RegValueName -ErrorAction SilentlyContinue
    } else {
        $choice = Read-Host "   Install startup entry so My Monitor runs on every login? [y/N]"
        if ($choice -notmatch '^[Yy]') {
            Write-Info "Skipping. Run my-monitor.exe manually to start."
            return
        }
    }

    Set-ItemProperty -Path $RegRunKey -Name $RegValueName -Value $BinaryPath
    Write-OK "Startup entry added: HKCU\...\Run\MyMonitor = $BinaryPath"
    Write-Info "My Monitor will start automatically on next login."
    Write-Info "To remove: Open Task Manager → Startup Apps → disable MyMonitor"
}

# ── Start the app ─────────────────────────────────────────────────
function Start-App {
    Write-Step "Starting My Monitor"

    $conn = Test-NetConnection -ComputerName localhost -Port $DefaultPort -WarningAction SilentlyContinue
    if ($conn.TcpTestSucceeded) {
        Write-OK "My Monitor is already running on port $DefaultPort"
        return
    }

    Set-Location $ProjectDir
    Start-Process -FilePath $BinaryPath -WorkingDirectory $ProjectDir -WindowStyle Hidden
    Start-Sleep -Seconds 2

    $conn = Test-NetConnection -ComputerName localhost -Port $DefaultPort -WarningAction SilentlyContinue
    if ($conn.TcpTestSucceeded) {
        Write-OK "Started successfully"
    } else {
        Write-Warn "App may still be initialising. Check Task Manager for my-monitor.exe"
    }
}

# ── Status check ──────────────────────────────────────────────────
function Show-Status {
    Write-Step "Status"

    $conn = Test-NetConnection -ComputerName localhost -Port $DefaultPort -WarningAction SilentlyContinue
    if ($conn.TcpTestSucceeded) {
        Write-OK "Running on port $DefaultPort"
    } else {
        Write-Warn "Not running"
    }

    $existing = Get-ItemProperty -Path $RegRunKey -Name $RegValueName -ErrorAction SilentlyContinue
    if ($existing) {
        Write-OK "Startup entry installed: $($existing.$RegValueName)"
    } else {
        Write-Info "Startup entry not installed"
    }

    if (Test-Path $BinaryPath) {
        Write-OK "Binary: $BinaryPath"
    } else {
        Write-Warn "Binary not built. Run: .\install.ps1 install"
    }

    $setupFile = Join-Path $ProjectDir "data\.setup_complete"
    if (Test-Path $setupFile) {
        Write-OK "Setup wizard completed"
    } else {
        Write-Info "Setup wizard not completed. Visit http://localhost:$DefaultPort/setup"
    }
}

# ── Uninstall ─────────────────────────────────────────────────────
function Uninstall-App {
    Write-Step "Uninstalling My Monitor"

    # Remove startup entry
    $existing = Get-ItemProperty -Path $RegRunKey -Name $RegValueName -ErrorAction SilentlyContinue
    if ($existing) {
        Remove-ItemProperty -Path $RegRunKey -Name $RegValueName
        Write-OK "Startup entry removed"
    } else {
        Write-Info "No startup entry found"
    }

    # Kill running process
    $proc = Get-Process -Name "my-monitor" -ErrorAction SilentlyContinue
    if ($proc) {
        Stop-Process -Name "my-monitor" -Force
        Write-OK "Stopped running process"
    }

    # Remove binary
    if (Test-Path $BinaryPath) {
        Remove-Item $BinaryPath -Force
        Write-OK "Binary removed"
    }

    Write-Host ""
    $choice = Read-Host "   Remove all data (database, screenshots)? [y/N]"
    if ($choice -match '^[Yy]') {
        $dataDir = Join-Path $ProjectDir "data"
        if (Test-Path $dataDir) {
            Remove-Item $dataDir -Recurse -Force
            Write-OK "Data directory removed"
        }
    } else {
        Write-Info "Data kept at: $(Join-Path $ProjectDir 'data')"
    }

    Write-Host ""
    Write-OK "My Monitor uninstalled"
}

# ── Post-install summary ──────────────────────────────────────────
function Print-Done {
    Write-Host ""
    Write-HR
    Write-Host ""
    Write-Host "  Installation complete!" -ForegroundColor Green
    Write-Host ""
    Write-Host "  Dashboard:     http://localhost:$DefaultPort"
    Write-Host "  Setup wizard:  http://localhost:$DefaultPort/setup"
    Write-Host "  Login:         admin / admin"
    Write-Host ""
    Write-Host "  ⚠  Change the default password in the setup wizard." -ForegroundColor Yellow
    Write-Host ""
    Write-HR
    Write-Host ""

    $choice = Read-Host "  Open setup wizard in browser now? [Y/n]"
    if ($choice -notmatch '^[Nn]') {
        Start-Process "http://localhost:$DefaultPort/setup"
    }
}

# ── Main ─────────────────────────────────────────────────────────
Print-Banner

switch ($Action.ToLower()) {
    "uninstall" { Uninstall-App }
    "remove"    { Uninstall-App }
    "status"    { Show-Status   }
    "start"     { Start-App; Show-Status }
    default     {
        Check-System
        Check-Go
        Build-Binary
        Setup-Startup
        Start-App
        Print-Done
    }
}
