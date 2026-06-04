#!/bin/bash
# My Monitor — Linux Installer
# Supports Ubuntu 20.04+, Debian 11+, Fedora 36+, Arch Linux
# Usage: ./install.sh [install|uninstall|start|status]

set -euo pipefail

# ── Colors ────────────────────────────────────────────────────────
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
BOLD='\033[1m'
DIM='\033[2m'
NC='\033[0m'

# ── Config ────────────────────────────────────────────────────────
APP_NAME="my-monitor"
SERVICE_NAME="my-monitor"
DEFAULT_PORT=8080
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# Resolve from install/linux/ up to the project root
PROJECT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
SERVICE_FILE="$HOME/.config/systemd/user/${SERVICE_NAME}.service"

# ── Helpers ───────────────────────────────────────────────────────
step()  { echo -e "\n${BOLD}${CYAN}▶  $1${NC}"; }
ok()    { echo -e "   ${GREEN}✓${NC}  $1"; }
warn()  { echo -e "   ${YELLOW}⚠${NC}  $1"; }
err()   { echo -e "   ${RED}✗${NC}  $1"; }
info()  { echo -e "   ${DIM}ℹ  $1${NC}"; }
hr()    { echo -e "${DIM}──────────────────────────────────────────────${NC}"; }

print_banner() {
  echo ""
  echo -e "${BOLD}${BLUE}  My Monitor — Linux Installer${NC}"
  echo -e "  ${DIM}Activity & Screenshot Monitor${NC}"
  echo ""
  hr
}

# ── Check Linux ───────────────────────────────────────────────────
check_system() {
  step "System requirements"

  if [[ "$(uname -s)" != "Linux" ]]; then
    err "This script is for Linux. Use install.sh (macOS) or install.ps1 (Windows)."
    exit 1
  fi

  ARCH=$(uname -m)
  ok "Linux $(uname -r) — $ARCH"

  # Check display server (X11 needed for screenshots/idle)
  if [[ -z "${DISPLAY:-}" ]] && [[ -z "${WAYLAND_DISPLAY:-}" ]]; then
    warn "No display detected (\$DISPLAY / \$WAYLAND_DISPLAY not set)."
    warn "Screenshots and idle detection require a graphical session."
  else
    ok "Display: ${DISPLAY:-$WAYLAND_DISPLAY}"
  fi
}

# ── Dependencies ──────────────────────────────────────────────────
install_deps() {
  step "Dependencies"

  # Go
  if command -v go &>/dev/null; then
    ok "Go $(go version | awk '{print $3}') found"
  else
    warn "Go not found — installing via system package manager..."
    if command -v apt-get &>/dev/null; then
      sudo apt-get update -qq && sudo apt-get install -y golang-go
    elif command -v dnf &>/dev/null; then
      sudo dnf install -y golang
    elif command -v pacman &>/dev/null; then
      sudo pacman -S --noconfirm go
    else
      err "Cannot install Go automatically. Please install Go 1.21+ from https://go.dev/dl/"
      exit 1
    fi
    ok "Go installed: $(go version | awk '{print $3}')"
  fi

  # scrot (screenshot tool)
  if command -v scrot &>/dev/null; then
    ok "scrot found (screenshot tool)"
  else
    warn "scrot not found — installing..."
    if command -v apt-get &>/dev/null; then
      sudo apt-get install -y scrot || warn "Could not install scrot. Try: sudo apt install scrot"
    elif command -v dnf &>/dev/null; then
      sudo dnf install -y scrot || warn "Could not install scrot. Try: sudo dnf install scrot"
    elif command -v pacman &>/dev/null; then
      sudo pacman -S --noconfirm scrot || warn "Could not install scrot"
    else
      warn "Please install scrot manually: sudo apt install scrot"
    fi
  fi

  # xprintidle (idle detection — optional)
  if command -v xprintidle &>/dev/null; then
    ok "xprintidle found (idle detection)"
  else
    warn "xprintidle not found (optional — idle detection disabled without it)"
    info "Install: sudo apt install xprintidle"
  fi
}

# ── Build ─────────────────────────────────────────────────────────
build_binary() {
  step "Building My Monitor"

  cd "$PROJECT_DIR"
  info "Resolving Go dependencies..."
  go mod tidy

  ARCH=$(uname -m)
  if [[ "$ARCH" == "x86_64" ]]; then
    GOARCH="amd64"
  elif [[ "$ARCH" == "aarch64" ]]; then
    GOARCH="arm64"
  else
    GOARCH="$ARCH"
  fi

  info "Compiling for linux/$GOARCH..."
  GOARCH="$GOARCH" GOOS=linux go build -ldflags="-s -w" -o my-monitor .

  ok "Binary ready: $PROJECT_DIR/my-monitor"
}

# ── Systemd user service ──────────────────────────────────────────
setup_service() {
  step "Auto-start on login (systemd user service)"

  echo ""
  if [[ -f "$SERVICE_FILE" ]]; then
    ok "Service already installed: $SERVICE_FILE"
    read -p "   Re-install / update it? [y/N] " -r; echo ""
    [[ "$REPLY" =~ ^[Yy]$ ]] || return
    systemctl --user stop "$SERVICE_NAME" 2>/dev/null || true
    systemctl --user disable "$SERVICE_NAME" 2>/dev/null || true
  else
    echo -e "   Install a systemd user service so My Monitor starts on every login?"
    read -p "   Install service? [y/N] " -r; echo ""
    [[ "$REPLY" =~ ^[Yy]$ ]] || { info "Skipping. Run ./my-monitor manually to start."; return; }
  fi

  mkdir -p "$HOME/.config/systemd/user"

  cat > "$SERVICE_FILE" <<SERVICE_EOF
[Unit]
Description=My Monitor — Activity & Screenshot Tracker
After=graphical-session.target

[Service]
Type=simple
ExecStart=${PROJECT_DIR}/my-monitor
WorkingDirectory=${PROJECT_DIR}
Restart=on-failure
RestartSec=30

[Install]
WantedBy=default.target
SERVICE_EOF

  systemctl --user daemon-reload
  systemctl --user enable "$SERVICE_NAME"

  ok "Service installed: $SERVICE_FILE"
  ok "My Monitor will start automatically on next login"
  info "Logs: journalctl --user -u my-monitor -f"
  info "To disable: systemctl --user disable my-monitor"
}

# ── Start the app ─────────────────────────────────────────────────
start_app() {
  step "Starting My Monitor"

  if ss -tlnp 2>/dev/null | grep -q ":$DEFAULT_PORT " || \
     netstat -tlnp 2>/dev/null | grep -q ":$DEFAULT_PORT "; then
    ok "My Monitor is already running on port $DEFAULT_PORT"
    return
  fi

  cd "$PROJECT_DIR"
  nohup ./my-monitor > /tmp/my-monitor-startup.log 2>&1 &
  local PID=$!

  local i
  for i in $(seq 1 15); do
    sleep 0.4
    if ss -tlnp 2>/dev/null | grep -q ":$DEFAULT_PORT " || \
       netstat -tlnp 2>/dev/null | grep -q ":$DEFAULT_PORT "; then
      ok "Started (PID $PID)"
      return
    fi
  done

  warn "App may still be initialising. Check: /tmp/my-monitor-startup.log"
}

# ── Status check ──────────────────────────────────────────────────
show_status() {
  step "Status"

  if ss -tlnp 2>/dev/null | grep -q ":$DEFAULT_PORT " || \
     netstat -tlnp 2>/dev/null | grep -q ":$DEFAULT_PORT "; then
    ok "Running on port $DEFAULT_PORT"
  else
    warn "Not running"
  fi

  if [[ -f "$SERVICE_FILE" ]]; then
    ok "Systemd service installed: $SERVICE_FILE"
    systemctl --user is-active "$SERVICE_NAME" &>/dev/null && ok "Service is active" || info "Service is not active"
  else
    info "Systemd service not installed"
  fi

  BINARY="$PROJECT_DIR/my-monitor"
  if [[ -f "$BINARY" ]]; then
    ok "Binary: $BINARY"
  else
    warn "Binary not built. Run: ./install.sh install"
  fi

  if [[ -f "$PROJECT_DIR/data/.setup_complete" ]]; then
    ok "Setup wizard completed"
  else
    info "Setup wizard not completed. Visit http://localhost:$DEFAULT_PORT/setup"
  fi
}

# ── Uninstall ─────────────────────────────────────────────────────
uninstall() {
  step "Uninstalling My Monitor"

  if [[ -f "$SERVICE_FILE" ]]; then
    systemctl --user stop "$SERVICE_NAME" 2>/dev/null || true
    systemctl --user disable "$SERVICE_NAME" 2>/dev/null || true
    systemctl --user daemon-reload 2>/dev/null || true
    rm -f "$SERVICE_FILE"
    ok "Systemd service removed"
  else
    info "No systemd service found"
  fi

  # Kill running process
  PID=$(ss -tlnp 2>/dev/null | grep ":$DEFAULT_PORT " | grep -oP 'pid=\K[0-9]+' | head -1 || true)
  if [[ -n "$PID" ]]; then
    kill "$PID" 2>/dev/null || true
    ok "Stopped running process (PID $PID)"
  fi

  if [[ -f "$PROJECT_DIR/my-monitor" ]]; then
    rm -f "$PROJECT_DIR/my-monitor"
    ok "Binary removed"
  fi

  echo ""
  read -p "   Remove all data (database, screenshots)? [y/N] " -r; echo ""
  if [[ "$REPLY" =~ ^[Yy]$ ]]; then
    rm -rf "$PROJECT_DIR/data"
    ok "Data directory removed"
  else
    info "Data kept at: $PROJECT_DIR/data"
  fi

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
      check_system
      install_deps
      build_binary
      setup_service
      start_app
      print_done
      ;;
  esac
}

main "$@"
