//go:build !windows

package main

import (
	"encoding/json"
	"net/http"
	"os/exec"
	"strings"
)

func hideConsoleWindow(cmd *exec.Cmd) {}

func consoleOwnedByUs() bool { return false }
func hideOwnConsole()        {}
func spawnDetachedTerm()     {}
func launchDefaultWindow()   { serveTerminalWindow() }
func styleInvokeWindow()     {}

func adjustOpacity(direction string) (int, error) {
	return 100, nil
}

func setAppUserModelID() {}

func handlePorts(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte("[]"))
}

func handlePortsKill(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"ok": false})
}

func handlePortsHTML(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	htmlBytes, err := webFS.ReadFile("web/ports.html")
	if err != nil {
		http.Error(w, "Ports template not found", 404)
		return
	}
	w.Write(htmlBytes)
}

func cleanHost(host string) string {
	if !strings.HasPrefix(host, "http://") && !strings.HasPrefix(host, "https://") {
		return "http://" + host
	}
	return host
}

func handleFreePort(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error": "not implemented on linux"})
}

func readWindowsClipboardText() string {
	return ""
}

func handleClipboardRead(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"text": ""})
}
