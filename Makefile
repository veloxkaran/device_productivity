.PHONY: run build build-local build-all \
        build-mac-amd64 build-mac-arm64 \
        build-linux-amd64 build-linux-arm64 \
        build-windows-amd64 \
        dist clean deps

# ── Development ────────────────────────────────────────────────
run:
	go run .

deps:
	go mod tidy

# ── Native build (current machine) ────────────────────────────
build-local:
	go build -ldflags="-s -w" -o my-monitor .
	@echo "Binary: ./my-monitor"

# Default 'build' target = current machine
build: build-local

# ── macOS ──────────────────────────────────────────────────────
build-mac-amd64:
	GOOS=darwin  GOARCH=amd64  go build -ldflags="-s -w" -o dist/my-monitor-darwin-amd64 .

build-mac-arm64:
	GOOS=darwin  GOARCH=arm64  go build -ldflags="-s -w" -o dist/my-monitor-darwin-arm64 .

# ── Linux ──────────────────────────────────────────────────────
build-linux-amd64:
	GOOS=linux   GOARCH=amd64  go build -ldflags="-s -w" -o dist/my-monitor-linux-amd64 .

build-linux-arm64:
	GOOS=linux   GOARCH=arm64  go build -ldflags="-s -w" -o dist/my-monitor-linux-arm64 .

# ── Windows ────────────────────────────────────────────────────
build-windows-amd64:
	GOOS=windows GOARCH=amd64  go build -ldflags="-s -w" -o dist/my-monitor-windows-amd64.exe .

# ── All platforms ──────────────────────────────────────────────
build-all: deps
	@mkdir -p dist
	$(MAKE) build-mac-amd64
	$(MAKE) build-mac-arm64
	$(MAKE) build-linux-amd64
	$(MAKE) build-linux-arm64
	$(MAKE) build-windows-amd64
	@echo ""
	@echo "All binaries in dist/:"
	@ls -lh dist/

# ── Release archives ───────────────────────────────────────────
dist: build-all
	@cd dist && \
	  zip my-monitor-darwin-amd64.zip  my-monitor-darwin-amd64  && \
	  zip my-monitor-darwin-arm64.zip  my-monitor-darwin-arm64  && \
	  zip my-monitor-linux-amd64.zip   my-monitor-linux-amd64   && \
	  zip my-monitor-linux-arm64.zip   my-monitor-linux-arm64   && \
	  zip my-monitor-windows-amd64.zip my-monitor-windows-amd64.exe
	@echo "Release archives in dist/"

# ── Clean ──────────────────────────────────────────────────────
clean:
	rm -f my-monitor
	rm -rf dist/
	rm -rf data/
