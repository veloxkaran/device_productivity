.PHONY: reset-permissions dev-cert
MIN_MACOS ?= 10.15
.PHONY: run build build-local build-all \
        build-mac-amd64 build-mac-arm64 \
        build-linux-amd64 build-linux-arm64 \
        build-windows-amd64 build-windows-386 \
        package package-macos package-macos-universal \
        package-linux-amd64 package-linux-arm64 \
        package-windows-amd64 package-windows-386 \
        installer-windows-nsis installer-windows-arm64-nsis \
        installer-gui-windows-amd64 installer-gui-windows-arm64 \
        publish-local publish-staging publish-prod \
        installer-gui-linux-amd64 installer-gui-linux-arm64 installer-gui-macos installer-gui-all \
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
# GO_TAGS: build tags for the APP binary. The legacy build (docker-build-legacy.sh) sets
# GO_TAGS=legacyos so the app updates on the "-legacy" lane; empty for normal builds.
GO_TAGS ?=
GO_TAGS_FLAG := $(if $(strip $(GO_TAGS)),-tags $(GO_TAGS),)
export HAJIR_API_URL HUB_URL PROVISION_TOKEN

# ── Development ────────────────────────────────────────────────────
run:
	go run .

hub:
	go run . hub

deps:
ifeq ($(strip $(SKIP_TIDY)),)
	go mod tidy
else
	@echo "deps: skipping 'go mod tidy' (SKIP_TIDY set -- e.g. the Go 1.20 legacy build, whose tidy rejects the go 1.22 directive; the build itself is unaffected)"
endif

# ── Native build (current machine) ────────────────────────────────
build-local: deps
	go build $(GO_TAGS_FLAG) -ldflags="$(LDFLAGS)" -o my-monitor .
	@echo "Binary: ./my-monitor"

build: build-local

# ── Cross-compile app binaries ─────────────────────────────────────
build-mac-amd64: deps
	MACOSX_DEPLOYMENT_TARGET=$(MIN_MACOS) CGO_ENABLED=1 CC="clang -arch x86_64" CGO_CFLAGS="-arch x86_64 -mmacosx-version-min=$(MIN_MACOS)" CGO_LDFLAGS="-arch x86_64 -mmacosx-version-min=$(MIN_MACOS)" GOOS=darwin  GOARCH=amd64  go build $(GO_TAGS_FLAG) -ldflags="$(LDFLAGS)" -o dist/my-monitor-darwin-amd64 .

build-mac-arm64: deps
	MACOSX_DEPLOYMENT_TARGET=$(MIN_MACOS) CGO_ENABLED=1 CC="clang -arch arm64" CGO_CFLAGS="-arch arm64 -mmacosx-version-min=$(MIN_MACOS)" CGO_LDFLAGS="-arch arm64 -mmacosx-version-min=$(MIN_MACOS)" GOOS=darwin  GOARCH=arm64  go build $(GO_TAGS_FLAG) -ldflags="$(LDFLAGS)" -o dist/my-monitor-darwin-arm64 .

# CGO_ENABLED=0: fully static binary (modernc.org/sqlite is pure Go, so no libc is
# needed). Runs on very old glibc/musl distros as well as new ones -- no
# "GLIBC_x.xx not found" when a laptop is on an older distro.
build-linux-amd64: deps
	GOOS=linux   GOARCH=amd64  CGO_ENABLED=0 go build $(GO_TAGS_FLAG) -ldflags="$(LDFLAGS)" -o dist/my-monitor-linux-amd64 .

build-linux-arm64: deps
	GOOS=linux   GOARCH=arm64  CGO_ENABLED=0 go build $(GO_TAGS_FLAG) -ldflags="$(LDFLAGS)" -o dist/my-monitor-linux-arm64 .

build-windows-amd64: deps
	# -H windowsgui: no console window (required for the hidden/covert app)
	GOOS=windows GOARCH=amd64  go build $(GO_TAGS_FLAG) -ldflags="$(LDFLAGS) -H windowsgui" -o dist/my-monitor-windows-amd64.exe .

build-windows-arm64: deps
	GOOS=windows GOARCH=arm64  go build $(GO_TAGS_FLAG) -ldflags="$(LDFLAGS) -H windowsgui" -o dist/my-monitor-windows-arm64.exe .

# 32-bit Windows, for old laptops (Windows 7/8 x86). Needs a Go 1.20 toolchain to RUN on
# Windows 7/8 at all -- Go 1.21+ binaries require Windows 10. Build via docker-build-legacy.sh.
build-windows-386: deps
	GOOS=windows GOARCH=386    CGO_ENABLED=0 go build $(GO_TAGS_FLAG) -ldflags="$(LDFLAGS) -H windowsgui" -o dist/my-monitor-windows-386.exe .

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

package-windows-arm64: build-windows-arm64
	@echo "==> Packaging Windows arm64 installer..."
	cp dist/my-monitor-windows-arm64.exe cmd/installer-windows/asset.bin
	GOOS=windows GOARCH=arm64 go build \
	    -tags packaging \
	    -ldflags="-s -w -H windowsgui" \
	    -o dist/MyMonitor-Setup-windows-arm64.exe \
	    ./cmd/installer-windows/
	rm -f cmd/installer-windows/asset.bin
	@echo ""; echo "  Installer: dist/MyMonitor-Setup-windows-arm64.exe"

# 32-bit Windows installer (Windows 7/8 x86). The installer stub is built for 386 too, so
# it runs on the same old machines. Build the whole thing under Go 1.20 (docker-build-legacy.sh)
# or neither the app nor the installer will launch on Windows 7/8.
package-windows-386: build-windows-386
	@echo "==> Packaging Windows 386 (32-bit) installer..."
	cp dist/my-monitor-windows-386.exe cmd/installer-windows/asset.bin
	GOOS=windows GOARCH=386 go build \
	    -tags packaging \
	    -ldflags="-s -w -H windowsgui" \
	    -o dist/MyMonitor-Setup-windows-386.exe \
	    ./cmd/installer-windows/
	rm -f cmd/installer-windows/asset.bin
	@echo ""; echo "  Installer: dist/MyMonitor-Setup-windows-386.exe"

# ── Build all platform packages ────────────────────────────────────
package: package-macos package-linux-amd64 package-linux-arm64 installer-windows-nsis
	@echo ""
	@echo "All installers in dist/:"
	@ls -lh dist/MyMonitor-Setup-* 2>/dev/null || true
	@ls -lh dist/MyMonitor-*.dmg  2>/dev/null || true

# ── Publish installers to the hub's downloads folder ──────────────
#   Builds every installer and copies them where the hub serves downloads,
#   so employers can grab them from the dashboard "Download app" tab.
#   Run on a Mac to also produce the .dmg; on Linux/Windows the .dmg is skipped.
HUB_DATA_DIR ?= hub-data
# ── Publish per environment (URLs stay in the .env files, injected here) ──
#   Each target builds + publishes the GUI wizard installers with that
#   environment's backend/hub URLs baked in. Edit the URLs in .env.staging /
#   .env.production — never here — so there is a single source of truth.
#     make publish-local      # localhost (Makefile defaults)
#     make publish-staging    # reads .env.staging
#     make publish-prod       # reads .env.production
publish-local:
	$(MAKE) publish-downloads

publish-staging:
	$(MAKE) publish-downloads $(shell grep -E '^(HAJIR_API_URL|HUB_URL)=' .env.staging | xargs)

publish-prod:
	$(MAKE) publish-downloads $(shell grep -E '^(HAJIR_API_URL|HUB_URL)=' .env.production | xargs)

publish-downloads:
	@mkdir -p $(HUB_DATA_DIR)/downloads $(HUB_DATA_DIR)/downloads/_old
	# Build the branded GUI wizard installers for every platform.
	# macOS MUST build first: build-dmg.sh wipes dist/, which would delete the
	# other platforms' installers if they were built before it.
	-@[ "$$(uname)" = "Darwin" ] && $(MAKE) installer-gui-macos || echo "  (skipping macOS .app — not on macOS)"
	-$(MAKE) installer-gui-linux-amd64
	-$(MAKE) installer-gui-linux-arm64
	-$(MAKE) installer-gui-windows-amd64
	-$(MAKE) installer-gui-windows-arm64
	# Retire any previously published installers so the dashboard shows only the
	# current ones (sweeps old console installers, dmgs, and prior GUI builds).
	-@mv -f "$(HUB_DATA_DIR)"/downloads/MyMonitor-Setup-*     "$(HUB_DATA_DIR)/downloads/_old/" 2>/dev/null || true
	-@mv -f "$(HUB_DATA_DIR)"/downloads/*.dmg                 "$(HUB_DATA_DIR)/downloads/_old/" 2>/dev/null || true
	-@mv -f "$(HUB_DATA_DIR)"/downloads/MyMonitor-GUI-Setup-* "$(HUB_DATA_DIR)/downloads/_old/" 2>/dev/null || true
	# Publish the GUI installers with a version in the filename (the dashboard reads it from the name).
	-@cp -f dist/MyMonitor-GUI-Setup-windows-amd64.zip "$(HUB_DATA_DIR)/downloads/MyMonitor-GUI-Setup-windows-amd64-v$(VERSION).zip" 2>/dev/null || true
	-@cp -f dist/MyMonitor-GUI-Setup-windows-arm64.zip "$(HUB_DATA_DIR)/downloads/MyMonitor-GUI-Setup-windows-arm64-v$(VERSION).zip" 2>/dev/null || true
	-@cp -f dist/MyMonitor-GUI-Setup-linux-amd64.zip   "$(HUB_DATA_DIR)/downloads/MyMonitor-GUI-Setup-linux-amd64-v$(VERSION).zip" 2>/dev/null || true
	-@cp -f dist/MyMonitor-GUI-Setup-linux-arm64.zip   "$(HUB_DATA_DIR)/downloads/MyMonitor-GUI-Setup-linux-arm64-v$(VERSION).zip" 2>/dev/null || true
	-@cp -f dist/MyMonitor-GUI-Setup-macos.zip         "$(HUB_DATA_DIR)/downloads/MyMonitor-GUI-Setup-macos-v$(VERSION).zip" 2>/dev/null || true
	@echo ""; echo "Published to $(HUB_DATA_DIR)/downloads:"; ls -lh "$(HUB_DATA_DIR)/downloads/" 2>/dev/null || true

# ── One-time local signing certificate (keeps macOS permissions across rebuilds)
# ── Android (Hajir Tracker mobile app) ─────────────────────────────
# Needs Android SDK + JDK 17 (Android Studio). Copies the APK into hub-data/downloads
# so it shows on the dashboard's "Download app" tab.
ANDROID_VERSION ?= $(shell sed -n 's/^APP_VERSION=//p' mobile/android/gradle.properties)
.PHONY: package-android
package-android:
	# Same UI as the desktop tracker: bundle its app.html, logo and icon.
	mkdir -p mobile/android/app/src/main/assets mobile/android/app/src/main/res/mipmap-xxxhdpi
	cp -f web/templates/app.html mobile/android/app/src/main/assets/app.html
	cp -f web/assets/hajirlogo.svg mobile/android/app/src/main/assets/logo.svg
	cp -f web/assets/hajir-app-icon-1024.png mobile/android/app/src/main/res/mipmap-xxxhdpi/ic_launcher.png
	cd mobile/android && ./gradlew assembleRelease -PHAJIR_API_URL=$(HAJIR_API_URL) -PHUB_URL=$(HUB_URL)
	mkdir -p hub-data/downloads
	cp -f mobile/android/app/build/outputs/apk/release/app-release.apk hub-data/downloads/MyMonitor-android-universal-v$(ANDROID_VERSION).apk
	@echo "→ hub-data/downloads/MyMonitor-android-universal-v$(ANDROID_VERSION).apk"

# Production build: bakes the production API + hub URLs. Signs with
# mobile/android/keystore.properties (monitor-release.keystore) when present,
# otherwise the debug key. Bump APP_VERSION / APP_VERSION_CODE in
# mobile/android/gradle.properties for each release, or pass them here:
#   make package-android-prod APP_VERSION_CODE=5 APP_VERSION=1.0.1
PROD_API ?= https://backend.veloxlabs.net/api/v2
PROD_HUB ?= https://monitor.veloxlabs.net
.PHONY: package-android-prod bundle-android-prod _android-assets
_android-assets:
	mkdir -p mobile/android/app/src/main/assets mobile/android/app/src/main/res/mipmap-xxxhdpi
	cp -f web/templates/app.html mobile/android/app/src/main/assets/app.html
	cp -f web/assets/hajirlogo.svg mobile/android/app/src/main/assets/logo.svg
	cp -f web/assets/hajir-app-icon-1024.png mobile/android/app/src/main/res/mipmap-xxxhdpi/ic_launcher.png

package-android-prod: _android-assets
	cd mobile/android && ./gradlew clean assembleRelease -PHAJIR_API_URL=$(PROD_API) -PHUB_URL=$(PROD_HUB) $(if $(APP_VERSION),-PAPP_VERSION=$(APP_VERSION)) $(if $(APP_VERSION_CODE),-PAPP_VERSION_CODE=$(APP_VERSION_CODE))
	mkdir -p hub-data/downloads
	cp -f mobile/android/app/build/outputs/apk/release/app-release.apk hub-data/downloads/MyMonitor-android-universal-v$(ANDROID_VERSION).apk
	@echo "→ PROD APK: hub-data/downloads/MyMonitor-android-universal-v$(ANDROID_VERSION).apk (api=$(PROD_API) hub=$(PROD_HUB))"

# Google Play bundle (.aab). Upload to Play Console; Play re-signs for delivery.
bundle-android-prod: _android-assets
	cd mobile/android && ./gradlew clean bundleRelease -PHAJIR_API_URL=$(PROD_API) -PHUB_URL=$(PROD_HUB) $(if $(APP_VERSION),-PAPP_VERSION=$(APP_VERSION)) $(if $(APP_VERSION_CODE),-PAPP_VERSION_CODE=$(APP_VERSION_CODE))
	@echo "→ PROD AAB: mobile/android/app/build/outputs/bundle/release/app-release.aab"

dev-cert:
	bash scripts-dev-cert.sh

# One-shot local macOS setup: create the stable signing cert and clear stale
# permission grants, so Screen Recording sticks across rebuilds.
dev-setup: dev-cert reset-permissions
	@echo "✓ Local signing ready. Now run: make publish-local"

# ── Reset macOS permissions (after a dev rebuild) ─────────────────
reset-permissions:
	-tccutil reset ScreenCapture com.hajir.tracker
	-tccutil reset Accessibility com.hajir.tracker
	-tccutil reset ScreenCapture com.mymonitor.app
	-tccutil reset Accessibility com.mymonitor.app
	@echo "Permissions reset. Open MyMonitor and allow Screen Recording again."

# ── Windows NSIS wizard installer ─────────────────────────────────
#   Requires makensis (Linux/mac: `apt install nsis` / `brew install nsis`).
#   Produces a proper setup wizard with Add/Remove Programs + uninstaller.
#     make installer-windows-nsis HUB_URL=https://monitor.veloxlabs.net HAJIR_API_URL=https://dev.veloxlabs.net/api/v2 VERSION=2.1.0
VERSION ?= $(shell sed -n 's/.*AppVersion = "\(.*\)".*/\1/p' cloud/heartbeat.go)
installer-windows-nsis: deps
	@mkdir -p dist
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="$(LDFLAGS) -H windowsgui" -o dist/MyMonitor.exe .
	makensis -DVERSION=$(VERSION) -DSRCEXE=$(CURDIR)/dist/MyMonitor.exe -DOUTFILE=MyMonitor-Setup-windows-amd64.exe installer-nsis/installer.nsi
	mv -f MyMonitor-Setup-windows-amd64.exe dist/ 2>/dev/null || true
	rm -f dist/MyMonitor.exe
	@echo ""; echo "  Installer: dist/MyMonitor-Setup-windows-amd64.exe"

installer-windows-arm64-nsis: deps
	@mkdir -p dist
	GOOS=windows GOARCH=arm64 CGO_ENABLED=0 go build -ldflags="$(LDFLAGS) -H windowsgui" -o dist/MyMonitor.exe .
	makensis -DVERSION=$(VERSION) -DSRCEXE=$(CURDIR)/dist/MyMonitor.exe -DOUTFILE=MyMonitor-Setup-windows-arm64.exe installer-nsis/installer.nsi
	mv -f MyMonitor-Setup-windows-arm64.exe dist/ 2>/dev/null || true
	rm -f dist/MyMonitor.exe
	@echo ""; echo "  Installer: dist/MyMonitor-Setup-windows-arm64.exe"

# ── Custom GUI installer (branded webview wizard) ──────────────────
#   A small Go app renders the Hajir setup wizard in a Chrome/Edge app-window,
#   identical on every OS. Windows/Linux embed and run the console installer
#   (--silent); macOS lays down the .app bundle directly. The "open now" step is
#   opt-in, so installing never opens a browser on its own.
#   Requires a Chrome/Edge/Chromium browser on the target machine.
GUI_LDFLAGS := -s -w -X main.appVersion=$(VERSION)
MAC_SIGN := $(if $(SIGN_IDENTITY),$(SIGN_IDENTITY),-)

installer-gui-windows-amd64: package-windows-amd64
	@echo "==> Building Windows amd64 GUI installer + uninstaller..."
	cp dist/MyMonitor-Setup-windows-amd64.exe cmd/installer-gui/asset_installer.exe
	rm -rf dist/win-amd64 && mkdir -p dist/win-amd64
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -tags packaging -ldflags="$(GUI_LDFLAGS) -H windowsgui" -o "dist/win-amd64/Install Hajir Tracker.exe" ./cmd/installer-gui/
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -tags "packaging uninstaller" -ldflags="$(GUI_LDFLAGS) -X main.defaultMode=uninstall -H windowsgui" -o "dist/win-amd64/Uninstall Hajir Tracker.exe" ./cmd/installer-gui/
	rm -f cmd/installer-gui/asset_installer.exe
	-bash sign-windows.sh "dist/win-amd64/Install Hajir Tracker.exe"
	-bash sign-windows.sh "dist/win-amd64/Uninstall Hajir Tracker.exe"
	cd dist/win-amd64 && zip -qr ../MyMonitor-GUI-Setup-windows-amd64.zip "Install Hajir Tracker.exe" "Uninstall Hajir Tracker.exe"
	rm -rf dist/win-amd64
	@echo "  Installer: dist/MyMonitor-GUI-Setup-windows-amd64.zip"

installer-gui-windows-arm64: package-windows-arm64
	@echo "==> Building Windows arm64 GUI installer + uninstaller..."
	cp dist/MyMonitor-Setup-windows-arm64.exe cmd/installer-gui/asset_installer.exe
	rm -rf dist/win-arm64 && mkdir -p dist/win-arm64
	GOOS=windows GOARCH=arm64 CGO_ENABLED=0 go build -tags packaging -ldflags="$(GUI_LDFLAGS) -H windowsgui" -o "dist/win-arm64/Install Hajir Tracker.exe" ./cmd/installer-gui/
	GOOS=windows GOARCH=arm64 CGO_ENABLED=0 go build -tags "packaging uninstaller" -ldflags="$(GUI_LDFLAGS) -X main.defaultMode=uninstall -H windowsgui" -o "dist/win-arm64/Uninstall Hajir Tracker.exe" ./cmd/installer-gui/
	rm -f cmd/installer-gui/asset_installer.exe
	-bash sign-windows.sh "dist/win-arm64/Install Hajir Tracker.exe"
	-bash sign-windows.sh "dist/win-arm64/Uninstall Hajir Tracker.exe"
	cd dist/win-arm64 && zip -qr ../MyMonitor-GUI-Setup-windows-arm64.zip "Install Hajir Tracker.exe" "Uninstall Hajir Tracker.exe"
	rm -rf dist/win-arm64
	@echo "  Installer: dist/MyMonitor-GUI-Setup-windows-arm64.zip"

installer-gui-linux-amd64: package-linux-amd64
	@echo "==> Building Linux amd64 GUI installer + uninstaller..."
	cp dist/MyMonitor-Setup-linux-amd64 cmd/installer-gui/asset_installer.bin
	rm -rf dist/lin-amd64 && mkdir -p dist/lin-amd64
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -tags packaging -ldflags="$(GUI_LDFLAGS)" -o "dist/lin-amd64/Install Hajir Tracker" ./cmd/installer-gui/
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -tags "packaging uninstaller" -ldflags="$(GUI_LDFLAGS) -X main.defaultMode=uninstall" -o "dist/lin-amd64/Uninstall Hajir Tracker" ./cmd/installer-gui/
	chmod +x "dist/lin-amd64/Install Hajir Tracker" "dist/lin-amd64/Uninstall Hajir Tracker"
	rm -f cmd/installer-gui/asset_installer.bin
	cd dist/lin-amd64 && zip -qr ../MyMonitor-GUI-Setup-linux-amd64.zip "Install Hajir Tracker" "Uninstall Hajir Tracker"
	rm -rf dist/lin-amd64
	@echo "  Installer: dist/MyMonitor-GUI-Setup-linux-amd64.zip"

installer-gui-linux-arm64: package-linux-arm64
	@echo "==> Building Linux arm64 GUI installer + uninstaller..."
	cp dist/MyMonitor-Setup-linux-arm64 cmd/installer-gui/asset_installer.bin
	rm -rf dist/lin-arm64 && mkdir -p dist/lin-arm64
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -tags packaging -ldflags="$(GUI_LDFLAGS)" -o "dist/lin-arm64/Install Hajir Tracker" ./cmd/installer-gui/
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -tags "packaging uninstaller" -ldflags="$(GUI_LDFLAGS) -X main.defaultMode=uninstall" -o "dist/lin-arm64/Uninstall Hajir Tracker" ./cmd/installer-gui/
	chmod +x "dist/lin-arm64/Install Hajir Tracker" "dist/lin-arm64/Uninstall Hajir Tracker"
	rm -f cmd/installer-gui/asset_installer.bin
	cd dist/lin-arm64 && zip -qr ../MyMonitor-GUI-Setup-linux-arm64.zip "Install Hajir Tracker" "Uninstall Hajir Tracker"
	rm -rf dist/lin-arm64
	@echo "  Installer: dist/MyMonitor-GUI-Setup-linux-arm64.zip"

# macOS: build a universal GUI binary, wrap it in a double-clickable .app, and
# zip it for download. Run on a Mac (needs build-dmg.sh's universal app binary).
installer-gui-macos: package-macos-universal
	@echo "==> Building macOS GUI installer (.app)..."
	# Embed the already-signed .app (ditto preserves its code signature), so the
	# installed app keeps a stable identity and macOS stops re-prompting for
	# Screen Recording on every install.
	ditto -c -k --sequesterRsrc --keepParent dist/MyMonitor.app cmd/installer-gui/app.zip
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -tags packaging -ldflags="$(GUI_LDFLAGS)" -o dist/.gui-arm64 ./cmd/installer-gui/
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -tags packaging -ldflags="$(GUI_LDFLAGS)" -o dist/.gui-amd64 ./cmd/installer-gui/
	lipo -create -output dist/.gui-universal dist/.gui-arm64 dist/.gui-amd64
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -tags "packaging uninstaller" -ldflags="$(GUI_LDFLAGS) -X main.defaultMode=uninstall" -o dist/.gui-un-arm64 ./cmd/installer-gui/
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -tags "packaging uninstaller" -ldflags="$(GUI_LDFLAGS) -X main.defaultMode=uninstall" -o dist/.gui-un-amd64 ./cmd/installer-gui/
	lipo -create -output dist/.gui-un-universal dist/.gui-un-arm64 dist/.gui-un-amd64
	rm -rf "dist/Install Hajir Tracker.app"
	mkdir -p "dist/Install Hajir Tracker.app/Contents/MacOS" "dist/Install Hajir Tracker.app/Contents/Resources"
	cp dist/.gui-universal "dist/Install Hajir Tracker.app/Contents/MacOS/installer"
	chmod +x "dist/Install Hajir Tracker.app/Contents/MacOS/installer"
	cp web/assets/icons/hajir.icns "dist/Install Hajir Tracker.app/Contents/Resources/AppIcon.icns"
	printf '%s' '<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict><key>CFBundleExecutable</key><string>installer</string><key>CFBundleIdentifier</key><string>com.hajir.installer</string><key>CFBundleName</key><string>Install Hajir Tracker</string><key>CFBundleIconFile</key><string>AppIcon</string><key>CFBundlePackageType</key><string>APPL</string><key>LSMinimumSystemVersion</key><string>10.15</string></dict></plist>' > "dist/Install Hajir Tracker.app/Contents/Info.plist"
	codesign --force --deep --options runtime --sign "$(MAC_SIGN)" --identifier com.hajir.installer "dist/Install Hajir Tracker.app" || true
	# Uninstaller app: same binary, launched with --uninstall via a tiny shell shim.
	rm -rf "dist/Uninstall Hajir Tracker.app"
	mkdir -p "dist/Uninstall Hajir Tracker.app/Contents/MacOS" "dist/Uninstall Hajir Tracker.app/Contents/Resources"
	cp dist/.gui-un-universal "dist/Uninstall Hajir Tracker.app/Contents/MacOS/installer"
	chmod +x "dist/Uninstall Hajir Tracker.app/Contents/MacOS/installer"
	cp web/assets/icons/hajir.icns "dist/Uninstall Hajir Tracker.app/Contents/Resources/AppIcon.icns"
	printf '%s' '<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict><key>CFBundleExecutable</key><string>installer</string><key>CFBundleIdentifier</key><string>com.hajir.uninstaller</string><key>CFBundleName</key><string>Uninstall Hajir Tracker</string><key>CFBundleIconFile</key><string>AppIcon</string><key>CFBundlePackageType</key><string>APPL</string><key>LSMinimumSystemVersion</key><string>10.15</string></dict></plist>' > "dist/Uninstall Hajir Tracker.app/Contents/Info.plist"
	codesign --force --deep --options runtime --sign "$(MAC_SIGN)" --identifier com.hajir.uninstaller "dist/Uninstall Hajir Tracker.app" || true
	cd dist && zip -qr MyMonitor-GUI-Setup-macos.zip "Install Hajir Tracker.app" "Uninstall Hajir Tracker.app"
	rm -rf dist/.gui-arm64 dist/.gui-amd64 dist/.gui-universal dist/.gui-un-arm64 dist/.gui-un-amd64 dist/.gui-un-universal "dist/Uninstall Hajir Tracker.app" cmd/installer-gui/app.zip
	@echo "  Installer: dist/MyMonitor-GUI-Setup-macos.zip"

installer-gui-all: installer-gui-windows-amd64 installer-gui-windows-arm64 installer-gui-linux-amd64 installer-gui-linux-arm64
	@echo ""; echo "GUI installers in dist/:"; ls -lh dist/MyMonitor-GUI-Setup-* 2>/dev/null || true

# ── Docker (hub server) ───────────────────────────────────────────
docker-build:
	docker build -t hajir-hub:latest .

docker-up:
	docker compose up -d --build

docker-down:
	docker compose down

docker-logs:
	docker compose logs -f hub

# ── Clean ─────────────────────────────────────────────────────────
clean:
	rm -f my-monitor my-monitor.exe
	rm -rf dist/
	rm -f cmd/installer-linux/asset.bin
	rm -f cmd/installer-windows/asset.bin
