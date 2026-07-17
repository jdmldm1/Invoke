//go:build windows

package main

import (
	_ "embed"
	"fmt"
	"html"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"
	"unsafe"

	webview2 "github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
)

//go:embed splash.html
var splashHTML string

type startResult struct {
	cmd *exec.Cmd
	err error
}

func main() {
	self, err := os.Executable()
	if err != nil {
		self = "invoke.exe"
	}
	dir := filepath.Dir(self)

	serverExe := filepath.Join(dir, "invoke-server.exe")
	if _, err := os.Stat(serverExe); err != nil {
		serverExe = filepath.Join(dir, "invoke.exe")
		if _, err := os.Stat(serverExe); err != nil {
			showError("Cannot find invoke-server.exe next to invoke-app.exe.\n\nMake sure both files are in the same directory.")
			os.Exit(1)
		}
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		showError(fmt.Sprintf("Could not bind a local port:\n\n%v", err))
		os.Exit(1)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()

	url := fmt.Sprintf("http://127.0.0.1:%d", port)

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
		// No WebView2 runtime: fall back to launching the server and
		// opening it in the system browser (no splash — nothing to host it in).
		cmd := exec.Command(serverExe, "term")
		cmd.Env = append(os.Environ(), fmt.Sprintf("INVOKE_TERM_PORT=%d", port), "INVOKE_NO_WINDOW=1")
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		if err := cmd.Start(); err != nil {
			showError(fmt.Sprintf("Could not start invoke-server.exe:\n\n%v", err))
			os.Exit(1)
		}
		defer cmd.Process.Kill()
		waitForServer(url)
		exec.Command("cmd", "/c", "start", url).Run()
		cmd.Wait()
		return
	}
	defer w.Destroy()

	setAppUserModelID("Invoke.Terminal.App")
	w.SetHtml(splashHTML)

	resultCh := make(chan startResult, 1)
	go func() {
		cmd := exec.Command(serverExe, "term")
		cmd.Env = append(os.Environ(),
			fmt.Sprintf("INVOKE_TERM_PORT=%d", port),
			"INVOKE_NO_WINDOW=1",
		)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		if err := cmd.Start(); err != nil {
			resultCh <- startResult{nil, fmt.Errorf("Could not start invoke-server.exe:\n\n%v", err)}
			return
		}
		if !waitForServer(url) {
			cmd.Process.Kill()
			resultCh <- startResult{nil, fmt.Errorf("invoke-server.exe started but did not respond within 10 seconds.\n\nURL: %s", url)}
			return
		}
		resultCh <- startResult{cmd, nil}
	}()

	done := make(chan struct{})
	go func() {
		res := <-resultCh
		if res.err != nil {
			w.Dispatch(func() { w.SetHtml(errorPageHTML(res.err.Error())) })
			return
		}
		// Point the splash's hidden iframe at the real app instead of
		// navigating the top-level document — a top-level Navigate briefly
		// shows WebView2's default white background before the new
		// document's stylesheet applies. The iframe swap has no such flash;
		// splash.html fades its overlay once the iframe finishes loading.
		w.Dispatch(func() { w.Eval("document.getElementById('app-frame').src=" + strconv.Quote(url) + ";") })
		<-done
		res.cmd.Process.Kill()
	}()

	w.Run()
	close(done)
}

func waitForServer(url string) bool {
	for i := 0; i < 100; i++ {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

func errorPageHTML(msg string) string {
	return `<!DOCTYPE html><html><head><meta charset="utf-8"><title>Invoke</title><style>
html,body{margin:0;height:100%;background:#000;font-family:'Segoe UI',sans-serif;
  display:flex;align-items:center;justify-content:center}
.box{max-width:520px;padding:28px 32px;text-align:center}
.title{color:#f87171;font-weight:700;font-size:15px;margin-bottom:14px}
.msg{color:#999;font-size:12.5px;white-space:pre-wrap;line-height:1.6}
</style></head><body><div class="box"><div class="title">Invoke failed to start</div><div class="msg">` +
		html.EscapeString(msg) + `</div></div></body></html>`
}

func showError(msg string) {
	user32 := windows.NewLazySystemDLL("user32.dll")
	mbw := user32.NewProc("MessageBoxW")
	title, _ := windows.UTF16PtrFromString("Invoke — Error")
	text, _ := windows.UTF16PtrFromString(msg)
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
