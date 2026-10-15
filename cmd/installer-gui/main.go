//go:build packaging

// Hajir Tracker — graphical installer.
//
// A small local web server renders the branded setup wizard (wizard.html) and
// opens it in a Chrome/Edge app-window, so the installer looks identical on
// Windows, macOS and Linux. The per-OS files (install_*.go) do the actual work:
// on Windows/Linux the GUI runs the proven console installer silently; on macOS
// it lays down the .app bundle and a login item.
//
// Build: see the Makefile `installer-gui-*` targets. Managed/silent fleet
// installs still use the console installers; this GUI is for interactive use.
package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"runtime"
	"time"
)

//go:embed wizard.html
var wizardHTML []byte

// appVersion is injected at build time (-X main.appVersion=...).
var appVersion = "dev"

type finishReq struct {
	Open bool `json:"open"`
}

// defaultMode is "install"; a build with -X main.defaultMode=uninstall produces a
// standalone uninstaller that removes the app on launch (used for the bundled
// "Uninstall Hajir Tracker" executable on Windows and Linux).
var defaultMode = "install"

func main() {
	uninstallWanted := defaultMode == "uninstall"
	for _, a := range os.Args[1:] {
		if a == "--uninstall" {
			uninstallWanted = true
		}
	}
	if uninstallWanted {
		uninstall()
		return
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatalf("installer: cannot open local port: %v", err)
	}
	url := "http://" + ln.Addr().String() + "/"

	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(wizardHTML)
	})

	mux.HandleFunc("/api/info", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{
			"version":    appVersion,
			"os":         runtime.GOOS,
			"installDir": displayInstallDir(),
		})
	})

	mux.HandleFunc("/api/install", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		if err := doInstall(func(s string) { log.Println(s) }); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			writeJSON(w, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("/api/finish", func(w http.ResponseWriter, r *http.Request) {
		var req finishReq
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Open {
			openTracker()
		}
		writeJSON(w, map[string]string{"status": "ok"})
		// Close the window and exit shortly after the response is flushed.
		go func() {
			time.Sleep(400 * time.Millisecond)
			closeWindow()
			os.Exit(0)
		}()
	})

	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()

	if !openWindow(url) {
		fmt.Println("Open this address to finish installing Hajir Tracker:")
		fmt.Println("  " + url)
	}

	select {} // serve until /api/finish exits the process
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
