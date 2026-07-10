//go:build windows

package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	webview2 "github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
)

func main() {
	// Find the server binary next to this launcher
	self, err := os.Executable()
	if err != nil {
		self = "invoke.exe"
	}
	dir := filepath.Dir(self)

	// Try invoke-server.exe first, fall back to invoke.exe
	serverExe := filepath.Join(dir, "invoke-server.exe")
	if _, err := os.Stat(serverExe); err != nil {
		serverExe = filepath.Join(dir, "invoke.exe")
		if _, err := os.Stat(serverExe); err != nil {
			showError("Cannot find invoke-server.exe next to invoke-app.exe.\n\nMake sure both files are in the same directory.")
			os.Exit(1)
		}
	}

	// Grab a free port
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		showError(fmt.Sprintf("Could not bind a local port:\n\n%v", err))
		os.Exit(1)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()

	url := fmt.Sprintf("http://127.0.0.1:%d", port)

	// Launch the server in the background, suppressing its console window
	cmd := exec.Command(serverExe, "term")
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("INVOKE_TERM_PORT=%d", port),
		"INVOKE_NO_WINDOW=1", // tells invoke.exe not to open its own browser
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Start(); err != nil {
		showError(fmt.Sprintf("Could not start invoke-server.exe:\n\n%v", err))
		os.Exit(1)
	}
	defer cmd.Process.Kill()

	// Tell Windows this is its own app (not Edge/Chrome)
	setAppUserModelID("Invoke.Terminal.App")

	// Wait for the server to accept connections (up to 10s)
	ready := false
	for i := 0; i < 100; i++ {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			ready = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		showError(fmt.Sprintf("invoke-server.exe started but did not respond within 10 seconds.\n\nURL: %s", url))
		os.Exit(1)
	}

	// WebView2 must run on an OS thread with its own message pump
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	w := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug: false,
		WindowOptions: webview2.WindowOptions{
			Title:  "Invoke",
			Width:  1280,
			Height: 820,
		},
	})
	if w == nil {
		// WebView2 runtime not found — fall back to launching Edge directly
		exec.Command("cmd", "/c", "start", url).Run()
		cmd.Wait()
		return
	}
	defer w.Destroy()

	w.Navigate(url)
	w.Run() // blocks until the user closes the window
}

// showError displays a modal error dialog — required because -H windowsgui
// suppresses all console output, making os.Stderr writes invisible.
func showError(msg string) {
	user32 := windows.NewLazySystemDLL("user32.dll")
	mbw := user32.NewProc("MessageBoxW")
	title, _ := windows.UTF16PtrFromString("Invoke — Error")
	text, _ := windows.UTF16PtrFromString(msg)
	// MB_OK | MB_ICONERROR | MB_SETFOREGROUND
	mbw.Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), 0x10|0x10000)
}

func setAppUserModelID(appID string) {
	shell32 := windows.NewLazySystemDLL("shell32.dll")
	proc := shell32.NewProc("SetCurrentProcessExplicitAppUserModelID")
	ptr, err := windows.UTF16PtrFromString(appID)
	if err != nil {
		return
	}
	proc.Call(uintptr(unsafe.Pointer(ptr)))
}
