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
	}

	// Grab a free port
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintln(os.Stderr, "No free port:", err)
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
		fmt.Fprintln(os.Stderr, "Could not start server:", err)
		os.Exit(1)
	}
	defer cmd.Process.Kill()

	// Tell Windows this is its own app (not Edge/Chrome)
	setAppUserModelID("Invoke.Terminal.App")

	// Wait for the server to accept connections (up to 5s)
	for i := 0; i < 50; i++ {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
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

func setAppUserModelID(appID string) {
	shell32 := windows.NewLazySystemDLL("shell32.dll")
	proc := shell32.NewProc("SetCurrentProcessExplicitAppUserModelID")
	ptr, err := windows.UTF16PtrFromString(appID)
	if err != nil {
		return
	}
	proc.Call(uintptr(unsafe.Pointer(ptr)))
}
