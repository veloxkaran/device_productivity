#!/bin/bash
# My Monitor — Installation Script
# Supports macOS 12+ (Monterey and later)
# Usage: ./install.sh [install|uninstall|start|status]

set -euo pipefail

# ── Colors ──────────────────────────────────────────────────────
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
BOLD='\033[1m'
DIM='\033[2m'
NC='\033[0m'

# ── Config ───────────────────────────────────────────────────────
APP_NAME="my-monitor"
PLIST_LABEL="com.mymonitor.app"
DEFAULT_PORT=8090
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PLIST_PATH="$HOME/Library/LaunchAgents/${PLIST_LABEL}.plist"

# ── Helpers ──────────────────────────────────────────────────────
step()  { echo -e "\n${BOLD}${CYAN}▶  $1${NC}"; }
ok()    { echo -e "   ${GREEN}✓${NC}  $1"; }
warn()  { echo -e "   ${YELLOW}⚠${NC}  $1"; }
err()   { echo -e "   ${RED}✗${NC}  $1"; }
info()  { echo -e "   ${DIM}ℹ  $1${NC}"; }
hr()    { echo -e "${DIM}──────────────────────────────────────────────${NC}"; }

print_banner() {
  echo ""
  echo -e "${BOLD}${BLUE}  ███╗   ███╗██╗   ██╗    ███╗   ███╗ ██████╗ ███╗   ██╗██╗████████╗ ██████╗ ██████╗ ${NC}"
  echo -e "${BOLD}${BLUE}  ████╗ ████║╚██╗ ██╔╝    ████╗ ████║██╔═══██╗████╗  ██║██║╚══██╔══╝██╔═══██╗██╔══██╗${NC}"
  echo -e "${BOLD}${BLUE}  ██╔████╔██║ ╚████╔╝     ██╔████╔██║██║   ██║██╔██╗ ██║██║   ██║   ██║   ██║██████╔╝${NC}"
  echo -e "${BOLD}${BLUE}  ██║╚██╔╝██║  ╚██╔╝      ██║╚██╔╝██║██║   ██║██║╚██╗██║██║   ██║   ██║   ██║██╔══██╗${NC}"
  echo -e "${BOLD}${BLUE}  ██║ ╚═╝ ██║   ██║       ██║ ╚═╝ ██║╚██████╔╝██║ ╚████║██║   ██║   ╚██████╔╝██║  ██║${NC}"
  echo -e "${BOLD}${BLUE}  ╚═╝     ╚═╝   ╚═╝       ╚═╝     ╚═╝ ╚═════╝ ╚═╝  ╚═══╝╚═╝   ╚═╝    ╚═════╝ ╚═╝  ╚═╝${NC}"
  echo ""
  echo -e "  ${DIM}macOS Activity & Screenshot Monitor — Installer${NC}"
  echo ""
  hr
}

# ── Check macOS ──────────────────────────────────────────────────
check_macos() {
  step "System requirements"

  if [[ "$(uname -s)" != "Darwin" ]]; then
    err "My Monitor requires macOS. Exiting."
    exit 1
  fi

  MACOS_VERSION=$(sw_vers -productVersion)
  MAJOR=$(echo "$MACOS_VERSION" | cut -d. -f1)

  ok "macOS $MACOS_VERSION"

  if [[ "$MAJOR" -lt 12 ]]; then
    warn "macOS 12 (Monterey) or later recommended for full Screen Recording support."
  fi

  ARCH=$(uname -m)
  if [[ "$ARCH" == "arm64" ]]; then
    GOARCH="arm64"
    ok "Apple Silicon (arm64)"
  else
    GOARCH="amd64"
    ok "Intel x86_64 (amd64)"
  fi

  MACOS_MAJOR="$MAJOR"
  export GOARCH MACOS_MAJOR
}

# ── Homebrew ─────────────────────────────────────────────────────
check_homebrew() {
  step "Homebrew"

  if command -v brew &>/dev/null; then
    ok "Homebrew already installed ($(brew --version | head -1))"
    return
  fi

  warn "Homebrew not found — installing now."
  info "This may prompt for your administrator password."
  echo ""
  /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"

  # Activate brew on Apple Silicon
  if [[ "$ARCH" == "arm64" ]] && [[ -f "/opt/homebrew/bin/brew" ]]; then
    eval "$(/opt/homebrew/bin/brew shellenv)"
  fi

  ok "Homebrew installed"
}

# ── Go toolchain ─────────────────────────────────────────────────
check_go() {
  step "Go toolchain"

  if command -v go &>/dev/null; then
    ok "Go $(go version | awk '{print $3}') found"
    return
  fi

  warn "Go not found — installing via Homebrew..."
  brew install go
  ok "Go installed: $(go version | awk '{print $3}')"
}

# ── Build ────────────────────────────────────────────────────────
build_binary() {
  step "Building My Monitor"

  cd "$SCRIPT_DIR"
  info "Resolving Go dependencies..."
  go mod tidy

  info "Compiling for GOARCH=$GOARCH..."
  GOARCH="$GOARCH" GOOS=darwin go build -ldflags="-s -w" -o my-monitor .

  ok "Binary ready: $SCRIPT_DIR/my-monitor"
}

# ── Screen Recording permission guidance ─────────────────────────
guide_permissions() {
  step "macOS Permissions"

  echo ""
  echo -e "   ${BOLD}My Monitor needs Screen Recording permission${NC} to take screenshots."
  echo -e "   ${DIM}Without it, screencapture captures a blank screen.${NC}"
  echo ""

  if [[ "${MACOS_MAJOR:-13}" -ge 13 ]]; then
    SETTINGS_LABEL="System Settings"
    SETTINGS_PATH="System Settings → Privacy & Security → Screen Recording"
  else
    SETTINGS_LABEL="System Preferences"
    SETTINGS_PATH="System Preferences → Security & Privacy → Privacy → Screen Recording"
  fi

  echo -e "   ${BOLD}How to grant Screen Recording:${NC}"
  echo ""
  echo -e "     1. Open ${BOLD}$SETTINGS_LABEL${NC}  (Apple  → $SETTINGS_LABEL)"
  echo -e "     2. Navigate to  ${BOLD}$SETTINGS_PATH${NC}"
  echo -e "     3. Click ${BOLD}+${NC} and add ${BOLD}Terminal${NC} (or drag the my-monitor binary)"
  echo -e "     4. Enable the toggle next to the app"
  echo -e "     5. Restart Terminal or the binary if prompted"
  echo ""
  info "The setup wizard at http://localhost:$DEFAULT_PORT/setup will verify permission."
  echo ""

  read -p "   Press [Enter] to open Screen Recording settings, or Ctrl+C to skip…" -r || true
  echo ""

  open "x-apple.systempreferences:com.apple.settings.PrivacySecurity.extension?Privacy_ScreenRecording" 2>/dev/null \
  || open "x-apple.systempreferences:com.apple.preference.security?Privacy_ScreenCapture" 2>/dev/null \
  || warn "Could not open System Settings automatically — please open it manually."

  echo ""
  info "After granting permission you may need to restart the app."
  read -p "   Press [Enter] to continue…" -r || true
}

# ── LaunchAgent ──────────────────────────────────────────────────
setup_launchagent() {
  step "Auto-start on login (optional)"

  echo ""
  if [[ -f "$PLIST_PATH" ]]; then
    ok "LaunchAgent already installed: $PLIST_PATH"
    read -p "   Re-install / update it? [y/N] " -r; echo ""
    [[ "$REPLY" =~ ^[Yy]$ ]] || return
    launchctl unload "$PLIST_PATH" 2>/dev/null || true
  else
    echo -e "   Install a LaunchAgent so My Monitor starts every time you log in?"
    echo ""
    read -p "   Install LaunchAgent? [y/N] " -r; echo ""
    [[ "$REPLY" =~ ^[Yy]$ ]] || { info "Skipping. Run ./my-monitor manually to start."; return; }
  fi

  BINARY_PATH="$SCRIPT_DIR/my-monitor"
  LOG_DIR="$HOME/Library/Logs"
  mkdir -p "$HOME/Library/LaunchAgents"

  cat > "$PLIST_PATH" <<PLIST_EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>${PLIST_LABEL}</string>
    <key>ProgramArguments</key>
    <array>
        <string>${BINARY_PATH}</string>
    </array>
    <key>WorkingDirectory</key>
    <string>${SCRIPT_DIR}</string>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <dict>
        <key>SuccessfulExit</key>
        <false/>
    </dict>
    <key>StandardOutPath</key>
    <string>${LOG_DIR}/my-monitor.log</string>
    <key>StandardErrorPath</key>
    <string>${LOG_DIR}/my-monitor-error.log</string>
    <key>ThrottleInterval</key>
    <integer>30</integer>
</dict>
</plist>
PLIST_EOF

  launchctl load "$PLIST_PATH" 2>/dev/null || true

  ok "LaunchAgent installed: $PLIST_PATH"
  ok "My Monitor will start automatically on next login"
  info "Logs: $LOG_DIR/my-monitor.log"
  info "To disable: launchctl unload $PLIST_PATH"
}

# ── Start the app ─────────────────────────────────────────────────
start_app() {
  step "Starting My Monitor"

  # Already running?
  if lsof -ti ":$DEFAULT_PORT" &>/dev/null; then
    ok "My Monitor is already running on port $DEFAULT_PORT"
    return
  fi

  cd "$SCRIPT_DIR"
  nohup ./my-monitor > /tmp/my-monitor-startup.log 2>&1 &
  local PID=$!

  local i
  for i in $(seq 1 15); do
    sleep 0.4
    if lsof -ti ":$DEFAULT_PORT" &>/dev/null; then
      ok "Started (PID $PID)"
      return
    fi
  done

  warn "App may still be initialising. Check: /tmp/my-monitor-startup.log"
}

# ── Status check ──────────────────────────────────────────────────
show_status() {
  step "Status"

  if lsof -ti ":$DEFAULT_PORT" &>/dev/null; then
    ok "Running on port $DEFAULT_PORT"
  else
    warn "Not running"
  fi

  if [[ -f "$PLIST_PATH" ]]; then
    ok "LaunchAgent installed: $PLIST_PATH"
  else
    info "LaunchAgent not installed"
  fi

  BINARY="$SCRIPT_DIR/my-monitor"
  if [[ -f "$BINARY" ]]; then
    ok "Binary: $BINARY"
  else
    warn "Binary not built. Run: ./install.sh install"
  fi

  if [[ -f "$SCRIPT_DIR/data/.setup_complete" ]]; then
    ok "Setup wizard completed"
  else
    info "Setup wizard not completed. Visit http://localhost:$DEFAULT_PORT/setup"
  fi
}

# ── Uninstall ─────────────────────────────────────────────────────
uninstall() {
  step "Uninstalling My Monitor"

  # Stop LaunchAgent
  if [[ -f "$PLIST_PATH" ]]; then
    launchctl unload "$PLIST_PATH" 2>/dev/null || true
    rm -f "$PLIST_PATH"
    ok "LaunchAgent removed"
  else
    info "No LaunchAgent found"
  fi

  # Kill running process
  if lsof -ti ":$DEFAULT_PORT" &>/dev/null; then
    kill "$(lsof -ti ":$DEFAULT_PORT")" 2>/dev/null || true
    ok "Stopped running process"
  fi

  # Remove binary
  if [[ -f "$SCRIPT_DIR/my-monitor" ]]; then
    rm -f "$SCRIPT_DIR/my-monitor"
    ok "Binary removed"
  fi

  echo ""
  read -p "   Remove all data (database, screenshots)? [y/N] " -r; echo ""
  if [[ "$REPLY" =~ ^[Yy]$ ]]; then
    rm -rf "$SCRIPT_DIR/data"
    ok "Data directory removed"
  else
    info "Data kept at: $SCRIPT_DIR/data"
  fi

  echo ""
  ok "My Monitor uninstalled"
}

# ── Post-install summary ──────────────────────────────────────────
print_done() {
  echo ""
  hr
  echo ""
  echo -e "${BOLD}${GREEN}  Installation complete!${NC}"
  echo ""
  echo -e "  ${BOLD}Dashboard:${NC}     http://localhost:$DEFAULT_PORT"
  echo -e "  ${BOLD}Setup wizard:${NC}  http://localhost:$DEFAULT_PORT/setup"
  echo -e "  ${BOLD}Login:${NC}         admin / admin"
  echo ""
  echo -e "  ${YELLOW}⚠  Change the default password in the setup wizard.${NC}"
  echo ""
  hr
  echo ""

  read -p "  Open setup wizard in browser now? [Y/n] " -r; echo ""
  if [[ ! "$REPLY" =~ ^[Nn]$ ]]; then
    open "http://localhost:$DEFAULT_PORT/setup" 2>/dev/null || true
  fi
}

# ── Main ─────────────────────────────────────────────────────────
main() {
  print_banner

  case "${1:-install}" in
    uninstall|remove)
      uninstall
      ;;
    status)
      show_status
      ;;
    start)
      start_app
      show_status
      ;;
    install|*)
      check_macos
      check_homebrew
      check_go
      build_binary
      guide_permissions
      setup_launchagent
      start_app
      print_done
      ;;
  esac
}

main "$@"
