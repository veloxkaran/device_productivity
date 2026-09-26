.PHONY: reset-permissions dev-cert
.PHONY: run build build-local build-all \
        build-mac-amd64 build-mac-arm64 \
        build-linux-amd64 build-linux-arm64 \
        build-windows-amd64 \
        package package-macos package-macos-universal \
        package-linux-amd64 package-linux-arm64 \
        package-windows-amd64 \
        dist clean deps

# ── Server URLs baked into the desktop app ─────────────────────────
#   make package-macos HAJIR_API_URL=https://api.example.com/api/v2 HUB_URL=https://hub.example.com
HAJIR_API_URL ?= http://localhost:8001/api/v2
HUB_URL       ?= http://localhost:4010
# Optional: bake an employer-issued device token into the build to image a fleet
# of managed (silent) devices. Leave empty for interactive installs.
#   make package-macos PROVISION_TOKEN=hdv_xxxxx
PROVISION_TOKEN ?=
LDFLAGS := -s -w -X my-monitor/web.DefaultHajirAPIURL=$(HAJIR_API_URL) -X my-monitor/web.DefaultHubURL=$(HUB_URL) -X my-monitor/web.DefaultProvisionToken=$(PROVISION_TOKEN)
export HAJIR_API_URL HUB_URL PROVISION_TOKEN

# ── Development ────────────────────────────────────────────────────
run:
	go run .

hub:
	go run . hub

deps:
	go mod tidy

# ── Native build (current machine) ────────────────────────────────
build-local: deps
	go build -ldflags="$(LDFLAGS)" -o my-monitor .
	@echo "Binary: ./my-monitor"

build: build-local

# ── Cross-compile app binaries ─────────────────────────────────────
build-mac-amd64: deps
	GOOS=darwin  GOARCH=amd64  go build -ldflags="$(LDFLAGS)" -o dist/my-monitor-darwin-amd64 .

build-mac-arm64: deps
	GOOS=darwin  GOARCH=arm64  go build -ldflags="$(LDFLAGS)" -o dist/my-monitor-darwin-arm64 .

build-linux-amd64: deps
	GOOS=linux   GOARCH=amd64  go build -ldflags="$(LDFLAGS)" -o dist/my-monitor-linux-amd64 .

build-linux-arm64: deps
	GOOS=linux   GOARCH=arm64  go build -ldflags="$(LDFLAGS)" -o dist/my-monitor-linux-arm64 .

build-windows-amd64: deps
	# -H windowsgui: no console window (required for the hidden/covert app)
	GOOS=windows GOARCH=amd64  go build -ldflags="$(LDFLAGS) -H windowsgui" -o dist/my-monitor-windows-amd64.exe .

build-all: deps
	@mkdir -p dist
	$(MAKE) build-mac-amd64
	$(MAKE) build-mac-arm64
	$(MAKE) build-linux-amd64
	$(MAKE) build-linux-arm64
	$(MAKE) build-windows-amd64
	@echo ""; ls -lh dist/

# ── macOS installer (.dmg) ─────────────────────────────────────────
#   native arch (arm64 or amd64 depending on host machine)
package-macos:
	@mkdir -p dist
	bash build-dmg.sh

#   universal binary (arm64 + amd64) — requires Xcode cross-compile tools
package-macos-universal:
	@mkdir -p dist
	ARCH=universal bash build-dmg.sh

# ── Linux self-extracting installer ───────────────────────────────
#   Build app binary → copy to installer/asset.bin → build installer → cleanup

package-linux-amd64: build-linux-amd64
	@echo "==> Packaging Linux amd64 installer..."
	cp dist/my-monitor-linux-amd64 cmd/installer-linux/asset.bin
	GOOS=linux GOARCH=amd64 go build \
	    -tags packaging \
	    -ldflags="$(LDFLAGS)" \
	    -o dist/MyMonitor-Setup-linux-amd64 \
	    ./cmd/installer-linux/
	rm -f cmd/installer-linux/asset.bin
	@echo ""; echo "  Installer: dist/MyMonitor-Setup-linux-amd64"
	@echo "  Usage on target: chmod +x MyMonitor-Setup-linux-amd64 && ./MyMonitor-Setup-linux-amd64"

package-linux-arm64: build-linux-arm64
	@echo "==> Packaging Linux arm64 installer..."
	cp dist/my-monitor-linux-arm64 cmd/installer-linux/asset.bin
	GOOS=linux GOARCH=arm64 go build \
	    -tags packaging \
	    -ldflags="$(LDFLAGS)" \
	    -o dist/MyMonitor-Setup-linux-arm64 \
	    ./cmd/installer-linux/
	rm -f cmd/installer-linux/asset.bin
	@echo ""; echo "  Installer: dist/MyMonitor-Setup-linux-arm64"

# ── Windows self-extracting installer (.exe) ───────────────────────
package-windows-amd64: build-windows-amd64
	@echo "==> Packaging Windows amd64 installer..."
	cp dist/my-monitor-windows-amd64.exe cmd/installer-windows/asset.bin
	GOOS=windows GOARCH=amd64 go build \
	    -tags packaging \
	    -ldflags="-s -w -H windowsgui" \
	    -o dist/MyMonitor-Setup-windows-amd64.exe \
	    ./cmd/installer-windows/
	rm -f cmd/installer-windows/asset.bin
	@echo ""; echo "  Installer: dist/MyMonitor-Setup-windows-amd64.exe"
	@echo "  Usage on target: double-click MyMonitor-Setup-windows-amd64.exe"

# ── Build all platform packages ────────────────────────────────────
package: package-macos package-linux-amd64 package-linux-arm64 package-windows-amd64
	@echo ""
	@echo "All installers in dist/:"
	@ls -lh dist/MyMonitor-Setup-* 2>/dev/null || true
	@ls -lh dist/MyMonitor-*.dmg  2>/dev/null || true

# ── One-time local signing certificate (keeps macOS permissions across rebuilds)
dev-cert:
	bash scripts-dev-cert.sh

# ── Reset macOS permissions (after a dev rebuild) ─────────────────
reset-permissions:
	-tccutil reset ScreenCapture com.hajir.tracker
	-tccutil reset Accessibility com.hajir.tracker
	-tccutil reset ScreenCapture com.mymonitor.app
	-tccutil reset Accessibility com.mymonitor.app
	@echo "Permissions reset. Open MyMonitor and allow Screen Recording again."

# ── Clean ─────────────────────────────────────────────────────────
clean:
	rm -f my-monitor my-monitor.exe
	rm -rf dist/
	rm -f cmd/installer-linux/asset.bin
	rm -f cmd/installer-windows/asset.bin
